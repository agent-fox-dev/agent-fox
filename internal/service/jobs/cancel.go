package jobs

import (
	"context"
	"errors"
	"fmt"
)

// cancelled is what a cancel found of the attempts of a job.
type cancelled struct {
	// running is the number of the attempt that is in a runner, or 0.
	running int
	// queued are the numbers of the attempts that were queued, which are cancelled.
	queued []int
	// cleanup says that the job's cancel cleanup was marked pending: the job has an issue, a cleanup was asked for, and no
	// attempt of it is running.
	cleanup bool
}

// CancelJob moves a job that is in state from, which is queued, running or blocked, to
// cancelled at once, with finished_at set, and the request label it consumed marked to be
// put back. The attempts that are queued, one that waits out a backoff included, become
// cancelled and never reach a runner; an attempt that is running stays running until its
// runner has returned. A job that is in another state than from, even another that could
// be cancelled, is a *StateError, and nothing is changed: of two actions that were each
// taken on the state the job was seen in, the second finds the state gone.
//
// When cleanup is set and the job has an issue, the cancel cleanup is owed: it is marked
// pending in the same transaction when no attempt of the job is running, and otherwise marked
// wanted, for the layer to settle once it knows whether a runner of this process holds the
// attempt (see Service.cancel).
//
// The cause is stored with the job (cancel_cause). A cancel because the issue was closed marks
// no request label to be put back.
func (s *Store) CancelJob(ctx context.Context, id string, from State, cleanup bool, cause error) (cancelled, error) {
	var found cancelled
	err := s.run(ctx, id, func(x *txn) error {
		now := formatTime(s.now())
		res, err := x.ExecContext(ctx,
			`UPDATE af_jobs SET state = 'cancelled', finished_at = ?, cancel_cause = ?,
			   labels_restore = CASE WHEN ? THEN labels_restore ELSE COALESCE(request_label, labels_restore) END
			 WHERE id = ? AND state = ?`, now, causeName(cause), errors.Is(cause, ErrIssueClosed), id, string(from))
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
				_ = rows.Close()
				return err
			}
			if state == AttemptRunning {
				found.running = n
			} else {
				found.queued = append(found.queued, n)
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if _, err := x.ExecContext(ctx, "UPDATE af_attempts SET state = 'cancelled', finished_at = ? WHERE job_id = ? AND state = 'queued'", now, id); err != nil {
			return fmt.Errorf("attempts of %s: %w", id, err)
		}
		if cleanup {
			res, err := x.ExecContext(ctx,
				`UPDATE af_jobs SET cleanup_wanted = 1, cleanup = CASE WHEN ? THEN 'pending' ELSE cleanup END
				 WHERE id = ? AND issue IS NOT NULL`, found.running == 0, id)
			if err != nil {
				return fmt.Errorf("cleanup of %s: %w", id, err)
			}
			if n, err := res.RowsAffected(); err != nil {
				return err
			} else if n > 0 && found.running == 0 {
				found.cleanup = true
			}
		}
		var current int
		if err := x.QueryRowContext(ctx, "SELECT MAX(n) FROM af_attempts WHERE job_id = ?", id).Scan(&current); err != nil {
			return err
		}
		return x.emit(ctx, current, EventJobState, jobStateData{State: StateCancelled, Attempt: current, AtRest: found.running == 0})
	})
	return found, err
}

// OpenIssueJobIDs are the jobs of the workspace for the issue that are queued, running or blocked, of either kind and
// whoever made them.
func (s *Store) OpenIssueJobIDs(ctx context.Context, workspace string, issue int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id FROM af_jobs WHERE workspace = ? AND issue = ? AND state IN ('queued', 'running', 'blocked') ORDER BY created_at, id", workspace, issue)
	if err != nil {
		return nil, fmt.Errorf("open jobs of %s#%d: %w", workspace, issue, err)
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
	state, err := s.store.JobState(ctx, id)
	if err != nil {
		return Doc{}, err
	}
	if !state.Open() {
		return Doc{}, &StateError{State: state}
	}
	if err := s.store.fails(OpObserved); err != nil {
		return Doc{}, err
	}
	if _, err := s.cancel(ctx, id, state, ErrCancelled); err != nil {
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
//
// A cancel by a person or by an unsubscribe owes the job's cancel cleanup when the layer has a
// cleaner and no runner of this process writes the labels: the cleanup is marked pending, and
// its first try starts at once, when no attempt was running or when the running one is held
// by no runner here (it was claimed and its runner not yet called, or its process ended). A
// running attempt that a runner holds is settled when it is recorded cancelled (see
// Store.EndAttempt).
func (s *Service) cancel(ctx context.Context, id string, from State, cause error) (*attemptRun, error) {
	wantCleanup := s.cleaner != nil && IsCancel(cause) && !errors.Is(cause, ErrIssueClosed)
	found, err := s.store.CancelJob(ctx, id, from, wantCleanup, cause)
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
	reached := false
	if run != nil {
		reached = run.stop(cause)
		s.startGrace(run)
	}
	if wantCleanup && found.running > 0 && !reached {
		// The attempt was looked for after the cancel committed, and a claim registers its run before it reads the job:
		// no runner of this process will be called for it, whether it has a run here that was stopped before its runner
		// was called or none.
		marked, err := s.store.MarkCleanup(ctx, id)
		if err != nil {
			s.log.Warn("the cancel cleanup of a job could not be marked; it is marked when its attempt is recorded cancelled",
				"job", id, "error", err.Error())
		}
		found.cleanup = marked
	}
	if found.cleanup {
		s.startTry(id)
	}
	return run, nil
}
