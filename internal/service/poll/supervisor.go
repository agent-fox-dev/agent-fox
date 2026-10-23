// Package poll turns the request labels that it finds through hub into jobs, and consumes each label once.
//
// For every subscribed workspace the service asks hub, every AF_POLL_INTERVAL, for the open issues that carry the
// workspace's triage or fix request label. A Supervisor keeps the loops that do so; a cycle is one run of the poll for
// one workspace.
package poll

import (
	"context"
	"database/sql"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

// Subscriptions are the subscribed workspaces and the tokens that open them.
type Subscriptions interface {
	List(ctx context.Context) ([]store.Subscription, error)
	Get(ctx context.Context, slug string) (store.Subscription, error)
	Token(ctx context.Context, slug string) (string, error)
}

// Jobs is what the poll asks of the jobs layer: to make a job, to name the open job of a kind for an issue, and to say
// how a job stands.
type Jobs interface {
	Create(ctx context.Context, req jobs.CreateRequest) (jobs.Doc, error)
	OpenJob(ctx context.Context, workspace string, kind jobs.Kind, issue int64) (jobs.Doc, error)
	Standing(ctx context.Context, id string) (jobs.Standing, error)
}

// The defaults of the settings that a test may change.
const (
	defaultScanPeriod   = 60 * time.Second
	defaultReleaseDelay = 5 * time.Minute
	maxFirstDelay       = 30 * time.Second
)

// Config is what a Supervisor is made of.
type Config struct {
	Subscriptions Subscriptions
	Hub           *hub.Client
	Jobs          Jobs
	// DB is the state file, which holds the records of the poll.
	DB     *sql.DB
	Logger *slog.Logger
	// Clock is the clock of the poll; the wall clock when nil.
	Clock jobs.Clock
	// FirstDelay gives the wait before the first cycle of a loop; a random time from 0 to 30 seconds when nil.
	FirstDelay func() time.Duration
	// ScanPeriod is how often the subscriptions are read for new and gone workspaces; 60 seconds when 0.
	ScanPeriod time.Duration
	// ReleaseDelay is how long after the release clock of a hold started a complete listing may release it; 5 minutes
	// when 0.
	ReleaseDelay time.Duration
}

// Supervisor runs the poll.
type Supervisor struct {
	cfg     Config
	records *Records

	// reconciler and handler are what a cycle does with the records and with the issues it lists.
	reconciler reconciler
	handler    handler

	mu       sync.Mutex
	states   map[string]*wsState
	loops    map[string]*loop
	slots    chan struct{}
	started  bool
	stopped  bool
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	inflight atomic.Int32
}

// reconciler brings the records of the poll for a workspace up to date with their jobs, without a request of hub.
type reconciler interface {
	reconcile(ctx context.Context, workspace string)
}

// handler decides what becomes of the issues of a listing, and says whether the cycle goes on.
type handler interface {
	handle(ctx context.Context, c *cycleRun, l *listing) bool
}

// New makes the Supervisor, with the defaults for what the configuration leaves out.
func New(cfg Config) *Supervisor {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Clock == nil {
		cfg.Clock = jobs.SystemClock{}
	}
	if cfg.FirstDelay == nil {
		cfg.FirstDelay = randomDelay
	}
	if cfg.ScanPeriod <= 0 {
		cfg.ScanPeriod = defaultScanPeriod
	}
	if cfg.ReleaseDelay <= 0 {
		cfg.ReleaseDelay = defaultReleaseDelay
	}
	s := &Supervisor{
		cfg: cfg, states: map[string]*wsState{}, loops: map[string]*loop{}, slots: make(chan struct{}, maxCycles),
		handler: issueHandler{},
	}
	s.reconciler = recordReconciler{s}
	if cfg.DB != nil {
		s.records = NewRecords(cfg.DB)
	}
	return s
}

// randomDelay is a time from 0 to 30 seconds, both included.
func randomDelay() time.Duration { return time.Duration(rand.Int64N(int64(maxFirstDelay) + 1)) }

// wsState is what the poll remembers of a workspace between its cycles.
type wsState struct {
	// last is the interval the workspace last read, zero when none was read or the last value turned the poll off.
	last time.Duration
	// warnedInterval and warnedPrefix are the values that have been warned of, and incomplete the labels whose
	// incomplete listing has been warned of and that have not listed complete since.
	warnedInterval, warnedPrefix map[string]bool
	incomplete                   map[string]bool
}

// stateOf is the state of the workspace, made when the poll first needs it.
func (s *Supervisor) stateOf(slug string) *wsState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[slug]
	if st == nil {
		st = &wsState{warnedInterval: map[string]bool{}, warnedPrefix: map[string]bool{}, incomplete: map[string]bool{}}
		s.states[slug] = st
	}
	return st
}

// wait is how long the workspace waits after a cycle that ended before it read the interval: the interval it last read,
// or 5 minutes when the last value turned the poll off or none was read.
func (st *wsState) wait() time.Duration {
	if st.last > 0 {
		return st.last
	}
	return defaultInterval
}
