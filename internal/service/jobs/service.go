package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"

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
	// Enqueue puts an attempt in the queue once it is stored. A job whose attempt it
	// could not queue stays queued, and the sweep queues it.
	Enqueue func(ctx context.Context, jobID string, attempt int) error
	// StoreFault is a seam for the tests, see Store.SetFault.
	StoreFault func(op string) error
}

// Service is the jobs layer.
type Service struct {
	store   *Store
	subs    Subscriptions
	log     *slog.Logger
	clock   Clock
	enqueue func(ctx context.Context, jobID string, attempt int) error

	// gate keeps a job from being made for a workspace between the moment it is
	// checked and the moment the job is stored, while the workspace is marked as
	// being unsubscribed: marking takes the write lock, making a job the read lock.
	gate          sync.RWMutex
	unsubscribing map[string]int
}

// New builds the Service.
func New(cfg Config) (*Service, error) {
	if cfg.DB == nil {
		return nil, errors.New("jobs: no database")
	}
	if cfg.Subscriptions == nil {
		return nil, errors.New("jobs: no subscriptions")
	}
	s := &Service{subs: cfg.Subscriptions, log: cfg.Logger, clock: cfg.Clock, enqueue: cfg.Enqueue, unsubscribing: map[string]int{}}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.clock == nil {
		s.clock = SystemClock{}
	}
	if s.enqueue == nil {
		s.enqueue = func(context.Context, string, int) error { return nil }
	}
	s.store = NewStore(cfg.DB, s.clock.Now)
	if cfg.StoreFault != nil {
		s.store.SetFault(cfg.StoreFault)
	}
	return s, nil
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
