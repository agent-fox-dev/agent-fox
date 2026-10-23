package jobs

import "context"

// MaxCleanupTries is how many tries of cleanups may be in flight at once.
const MaxCleanupTries = maxTries

// TryCleanup starts a try of the job's cancel cleanup, as the sweep does, unless one is in flight, and returns what is
// closed when the try in flight ends; for the tests.
func (s *Service) TryCleanup(id string) <-chan struct{} { return s.startTry(id) }

// CleanupsInFlight is how many tries of cleanups are in flight; for the tests.
func (s *Service) CleanupsInFlight() int {
	s.cleanups.mu.Lock()
	defer s.cleanups.mu.Unlock()
	return len(s.cleanups.tries)
}

// CancelFor cancels an open job with the cause, as a cancel that is neither a person's nor an unsubscribe would; for the
// tests.
func (s *Service) CancelFor(ctx context.Context, id string, cause error) error {
	state, err := s.store.JobState(ctx, id)
	if err != nil {
		return err
	}
	_, err = s.cancel(ctx, id, state, cause)
	return err
}

// RegisterRun makes the attempt known as a run of this process whose runner was not yet called, as a claim does a moment
// before it calls the runner, and returns what forgets it; for the tests.
func (s *Service) RegisterRun(id string, attempt int) (forget func()) {
	_, cancel := context.WithCancelCause(context.Background())
	run := &attemptRun{key: attemptKey{id, attempt}, cancel: cancel, recorder: newRecorder(s.store, s.log, id, attempt),
		returned: make(chan struct{}), settled: make(chan struct{}), done: make(chan struct{})}
	s.mu.Lock()
	s.inflight[run.key] = run
	s.mu.Unlock()
	return func() { s.forget(run) }
}
