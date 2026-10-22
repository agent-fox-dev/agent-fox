package jobs

import (
	"context"
	"fmt"
)

// cancelled is what a cancel found of the attempts of a job.
type cancelled struct {
	// running is the number of the attempt that is in a runner, or 0.
	running int
	// queued are the numbers of the attempts that were queued, which are cancelled.
	queued []int
}

// CancelJob moves a job that is queued, running or blocked to cancelled at once, with
// finished_at set, and the request label it consumed marked to be put back. The
// attempts that are queued, one that waits out a backoff included, become cancelled and
// never reach a runner; an attempt that is running stays running until its runner has
// returned. A job in another state is a *StateError, and nothing is changed.
func (s *Store) CancelJob(ctx context.Context, id string) (cancelled, error) {
	var found cancelled
	err := s.run(ctx, id, func(x *txn) error {
		now := formatTime(s.now())
		res, err := x.ExecContext(ctx,
			`UPDATE af_jobs SET state = 'cancelled', finished_at = ?, labels_restore = COALESCE(request_label, labels_restore)
			 WHERE id = ? AND state IN ('queued', 'running', 'blocked')`, now, id)
		if err != nil {
			return fmt.Errorf("job %s: %w", id, err)
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			if err != nil {
				return err
			}
			return x.refusal(ctx)
		}
		rows, err := x.QueryContext(ctx, "SELECT n, state FROM af_attempts WHERE job_id = ? AND state IN ('queued', 'running') ORDER BY n", id)
		if err != nil {
			return fmt.Errorf("attempts of %s: %w", id, err)
		}
		for rows.Next() {
			var n int
			var state AttemptState
			if err := rows.Scan(&n, &state); err != nil {
				rows.Close()
				return err
			}
			if state == AttemptRunning {
				found.running = n
			} else {
				found.queued = append(found.queued, n)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if _, err := x.ExecContext(ctx, "UPDATE af_attempts SET state = 'cancelled', finished_at = ? WHERE job_id = ? AND state = 'queued'", now, id); err != nil {
			return fmt.Errorf("attempts of %s: %w", id, err)
		}
		var current int
		if err := x.QueryRowContext(ctx, "SELECT MAX(n) FROM af_attempts WHERE job_id = ?", id).Scan(&current); err != nil {
			return err
		}
		return x.emit(ctx, current, EventJobState, jobStateData{State: StateCancelled, Attempt: current, AtRest: found.running == 0})
	})
	return found, err
}

// OpenJobIDs are the jobs of the workspace that are queued, running or blocked.
func (s *Store) OpenJobIDs(ctx context.Context, workspace string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM af_jobs WHERE workspace = ? AND state IN ('queued', 'running', 'blocked') ORDER BY created_at, id", workspace)
	if err != nil {
		return nil, fmt.Errorf("open jobs of %s: %w", workspace, err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Cancel cancels a job that is queued, running or blocked, as a person asks, and
// returns it: it reads cancelled at once, and the runner of its running attempt, if it
// has one, is told with the cancel cause. See cancel.
func (s *Service) Cancel(ctx context.Context, id string) (Doc, error) {
	if _, err := s.cancel(ctx, id, ErrCancelled); err != nil {
		return Doc{}, err
	}
	return s.store.Doc(ctx, id, false)
}

// cancel does what Cancel does with the cause, and returns the attempt that is in a
// runner, if there is one in this process. The queued attempts are cancelled in the
// file, and a handler that waits out the backoff of one is woken, so that it lets go of
// the workspace; the running attempt's context is cancelled with the cause, and a
// timer of the grace starts: a runner that has not returned when it ends has its
// attempt recorded cancelled without it.
func (s *Service) cancel(ctx context.Context, id string, cause error) (*attemptRun, error) {
	found, err := s.store.CancelJob(ctx, id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	for _, n := range found.queued {
		if wake := s.waiting[attemptKey{id, n}]; wake != nil {
			close(wake)
			delete(s.waiting, attemptKey{id, n})
		}
	}
	var run *attemptRun
	if found.running > 0 {
		run = s.inflight[attemptKey{id, found.running}]
	}
	s.mu.Unlock()
	if run != nil {
		run.cancel(cause)
		s.startGrace(run)
	}
	return run, nil
}
