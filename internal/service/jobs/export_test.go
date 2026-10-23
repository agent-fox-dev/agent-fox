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
