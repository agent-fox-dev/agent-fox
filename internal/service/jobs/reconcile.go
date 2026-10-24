package jobs

import (
	"context"
	"errors"
)

// Known says whether the database has the job.
func (s *Service) Known(ctx context.Context, jobID string) (bool, error) {
	_, err := s.store.JobState(ctx, jobID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// Running says whether the attempt is in a runner of this process: one that is waiting for a slot, or out of a
// runner while it waits out a backoff, is not.
func (s *Service) Running(jobID string, attempt int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.inflight[attemptKey{jobID, attempt}]
	return ok
}
