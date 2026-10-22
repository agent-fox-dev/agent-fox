package jobs

import (
	"context"
	"fmt"
	"time"
)

// maxTransient is how many transient failures an attempt may have: the third ends it.
const maxTransient = 3

// backoffs are the waits after the first and the second transient failure of an
// attempt, before the same attempt is handed over again.
var backoffs = [maxTransient - 1]time.Duration{30 * time.Second, 2 * time.Minute}

// NoteTransient counts a transient failure of a running attempt, and returns how many
// it has had, this one included.
func (s *Store) NoteTransient(ctx context.Context, jobID string, attempt int) (int, error) {
	var failures int
	err := s.db.QueryRowContext(ctx,
		"UPDATE af_attempts SET transient_failures = transient_failures + 1 WHERE job_id = ? AND n = ? AND state = 'running' RETURNING transient_failures",
		jobID, attempt).Scan(&failures)
	if err != nil {
		return 0, fmt.Errorf("attempt %d of %s: %w", attempt, jobID, err)
	}
	return failures, nil
}

// Backoff returns a running attempt to queued, and its job with it, to be handed over
// again once a wait is over: no envelope is stored, and nothing is said to be ended. It
// says whether it did; an attempt that is not running is left as it is.
func (s *Store) Backoff(ctx context.Context, jobID string, attempt int) (bool, error) {
	var returned bool
	err := s.run(ctx, jobID, func(x *txn) error {
		res, err := x.ExecContext(ctx, "UPDATE af_attempts SET state = 'queued' WHERE job_id = ? AND n = ? AND state = 'running'", jobID, attempt)
		if err != nil {
			return fmt.Errorf("attempt %d of %s: %w", attempt, jobID, err)
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		changed, err := s.transition(ctx, x.Tx, jobID, []State{StateRunning}, StateQueued)
		if err != nil {
			return err
		}
		if !changed {
			return x.refusal(ctx)
		}
		returned = true
		return x.emit(ctx, attempt, EventJobState, jobStateData{State: StateQueued, Attempt: attempt})
	})
	return returned, err
}
