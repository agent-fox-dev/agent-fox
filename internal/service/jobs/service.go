package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"path/filepath"
	"sync"
	"time"

	"github.com/agent-fox-dev/aflib/jobqueue"

	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

// Subscriptions is where the layer reads the subscription of a workspace.
type Subscriptions interface {
	Get(ctx context.Context, slug string) (store.Subscription, error)
}

// Config is what the Service is built from.
type Config struct {
	DB            *sql.DB
	Subscriptions Subscriptions
	Logger        *slog.Logger
	// Clock is the clock of the layer; the system's when nil.
	Clock Clock
	// Enqueue puts an attempt in the queue when a job or an automatic attempt is made. A
	// job whose attempt it could not queue stays queued, and the sweep, which uses the
	// queue itself, queues it. The queue's own when nil; the tests replace it.
	Enqueue func(ctx context.Context, jobID string, attempt int) error
	// StoreFault is a seam for the tests, see Store.SetFault.
	StoreFault func(op string) error

	// Workers is [workers].count, the number of attempts in a runner at once.
	Workers int
	// AttemptsDir is the directory that holds the files of the attempts.
	AttemptsDir string
	// Runner carries out the attempts.
	Runner Runner
	// PollInterval is how often the queue looks for work it was not told of (1s when 0).
	PollInterval time.Duration
	// RetryDelay is how long the queue waits before it hands an item back that was
	// put back (1s when 0).
	RetryDelay time.Duration
}

// Service is the jobs layer.
type Service struct {
	store   *Store
	subs    Subscriptions
	log     *slog.Logger
	clock   Clock
	enqueue func(ctx context.Context, jobID string, attempt int) error

	runner      Runner
	attemptsDir string
	queue       *jobqueue.Queue

	// gate keeps a job from being made for a workspace between the moment it is
	// checked and the moment the job is stored, while the workspace is marked as
	// being unsubscribed: marking takes the write lock, making a job the read lock.
	gate          sync.RWMutex
	unsubscribing map[string]int

	// slots holds one token for each attempt that is in a runner. The queue has more
	// workers than slots, so that an attempt that waits out a backoff, or waits for a
	// slot, holds a worker of the queue and its workspace's place, and no slot.
	slots chan struct{}

	mu       sync.Mutex
	inflight map[attemptKey]*attemptRun
	started  bool
	stopping chan struct{}
	stopOnce sync.Once
	loops    sync.WaitGroup
}

// parkedWorkers is how many more workers the queue has than there are slots.
const parkedWorkers = 16

// New builds the Service: it makes the tables of the queue in the state file, and
// registers the handler of the attempts. Nothing runs until Start.
func New(cfg Config) (*Service, error) {
	switch {
	case cfg.DB == nil:
		return nil, errors.New("jobs: no database")
	case cfg.Subscriptions == nil:
		return nil, errors.New("jobs: no subscriptions")
	case cfg.Runner == nil:
		return nil, errors.New("jobs: no runner")
	case cfg.AttemptsDir == "":
		return nil, errors.New("jobs: no directory for the attempts")
	case cfg.Workers < 1:
		return nil, fmt.Errorf("jobs: %d workers, want at least 1", cfg.Workers)
	}
	dir, err := filepath.Abs(cfg.AttemptsDir)
	if err != nil {
		return nil, fmt.Errorf("jobs: attempts directory: %w", err)
	}
	s := &Service{
		subs: cfg.Subscriptions, log: cfg.Logger, clock: cfg.Clock, enqueue: cfg.Enqueue,
		runner: cfg.Runner, attemptsDir: dir, unsubscribing: map[string]int{},
		inflight: map[attemptKey]*attemptRun{}, stopping: make(chan struct{}), slots: make(chan struct{}, cfg.Workers),
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.clock == nil {
		s.clock = SystemClock{}
	}
	s.store = NewStore(cfg.DB, s.clock.Now)
	if cfg.StoreFault != nil {
		s.store.SetFault(cfg.StoreFault)
	}

	// The table of the queue, with the columns this layer needs of it (group_key and
	// progress are in it when it is made).
	if err := jobqueue.InitSchema(cfg.DB); err != nil {
		return nil, err
	}
	poll, delay := cfg.PollInterval, cfg.RetryDelay
	if poll <= 0 {
		poll = time.Second
	}
	if delay <= 0 {
		delay = time.Second
	}
	s.queue, err = jobqueue.New(cfg.DB, s.log, jobqueue.WithWorkers(cfg.Workers+parkedWorkers), jobqueue.WithPollInterval(poll), jobqueue.WithGracePeriod(queueGrace))
	if err != nil {
		return nil, err
	}
	// An item that is handed back comes again after a constant delay, however many
	// times it has been handed back, and is never given up.
	policy := &jobqueue.RetryPolicy{Base: delay, Multiplier: 1, Cap: delay, MaxRetries: math.MaxInt32}
	if err := s.queue.Register(attemptType, s.handle, policy); err != nil {
		return nil, err
	}
	if s.enqueue == nil {
		s.enqueue = s.Enqueue
	}
	return s, nil
}

// Start starts the queue: it makes the directory of the attempts, hands over again the
// items that were claimed when the process before this one ended, starts the workers,
// and sweeps once and then every minute.
func (s *Service) Start() error {
	if err := makeDir(s.attemptsDir); err != nil {
		return err
	}
	if err := s.queue.Start(); err != nil {
		return err
	}
	s.mu.Lock()
	s.started = true
	s.mu.Unlock()
	// The attempts that were queued when the process before this one ended are in the
	// queue, but one that was stored and not queued is not: the sweep queues it.
	s.sweep(context.Background())
	ticker := s.clock.NewTicker(sweepEvery)
	s.loops.Add(1)
	go s.sweepLoop(ticker)
	return nil
}

// Get is the document of a job with its attempts.
func (s *Service) Get(ctx context.Context, id string) (Doc, error) {
	return s.store.Doc(ctx, id, true)
}

// MarkUnsubscribing marks the workspace as being unsubscribed, so that no job is
// made for it, and returns the function that clears the mark. The mark counts, so
// that two unsubscribes of one workspace clear it only when both are over.
func (s *Service) MarkUnsubscribing(slug string) (clear func()) {
	s.gate.Lock()
	s.unsubscribing[slug]++
	s.gate.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.gate.Lock()
			defer s.gate.Unlock()
			if s.unsubscribing[slug]--; s.unsubscribing[slug] <= 0 {
				delete(s.unsubscribing, slug)
			}
		})
	}
}

// subscribed checks that the workspace can take work: it is subscribed, it is not
// being unsubscribed, and its token opens. The caller holds the gate.
func (s *Service) subscribed(ctx context.Context, slug string) error {
	if s.unsubscribing[slug] > 0 {
		return ErrNotSubscribed
	}
	sub, err := s.subs.Get(ctx, slug)
	switch {
	case errors.Is(err, store.ErrNotSubscribed):
		return ErrNotSubscribed
	case err != nil:
		return fmt.Errorf("subscription of %s: %w", slug, err)
	case sub.TokenState != store.TokenOK:
		return ErrTokenUnreadable
	}
	return nil
}
