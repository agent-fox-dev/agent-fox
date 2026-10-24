package jobs

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

const (
	// purgeEvery is how often the jobs that have been finished for longer than the
	// retention are removed.
	purgeEvery = time.Hour
	// defaultRetain is [database].retain when the configuration of the layer has none.
	defaultRetain = 720 * time.Hour
	// purgeBatch is how many expired jobs one query of the purge reads.
	purgeBatch = 200
)

// expiredCondition says that a job is one the purge may remove: it is finished, no attempt of it
// is running, its cancel cleanup is not pending, and it was finished before the cutoff. finished_at is when the job last entered
// done, failed or cancelled, so the time a question was asked, or a job was made, counts for
// nothing. The arguments are the job and the cutoff.
const expiredCondition = `j.id = ? AND j.state IN ('done', 'failed', 'cancelled') AND j.finished_at < ?
	AND j.cleanup IS NOT 'pending'
	AND NOT EXISTS (SELECT 1 FROM af_attempts a WHERE a.job_id = j.id AND a.state = 'running')`

// expiredKey is where in the order of the expired jobs a read of them is.
type expiredKey struct{ finishedAt, id string }

// Expired are the jobs that were finished before the cutoff, after the key, in the order of
// when they were finished, at most limit of them.
func (s *Store) Expired(ctx context.Context, cutoff string, after expiredKey, limit int) ([]expiredKey, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT finished_at, id FROM af_jobs
		 WHERE state IN ('done', 'failed', 'cancelled') AND finished_at < ? AND cleanup IS NOT 'pending'
		   AND (finished_at > ? OR (finished_at = ? AND id > ?))
		 ORDER BY finished_at, id LIMIT ?`, cutoff, after.finishedAt, after.finishedAt, after.id, limit)
	if err != nil {
		return nil, fmt.Errorf("expired jobs: %w", err)
	}
	defer rows.Close()
	var out []expiredKey
	for rows.Next() {
		var k expiredKey
		if err := rows.Scan(&k.finishedAt, &k.id); err != nil {
			return nil, fmt.Errorf("expired jobs: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// DirectoryError is the failure to remove the directory of a job that is to be purged.
type DirectoryError struct {
	JobID string
	Err   error
}

func (e *DirectoryError) Error() string {
	return fmt.Sprintf("the attempts directory of %s: %v", e.JobID, e.Err)
}
func (e *DirectoryError) Unwrap() error { return e.Err }

// Purge removes a job that was finished before the cutoff, and says whether it did. Under the
// lock of the job it looks again at whether the job may go, calls removeDir, and only when
// that has worked removes the rows of the job from af_events, af_attempts and af_jobs, in one
// transaction. A removeDir that fails is a *DirectoryError, and the rows stay.
func (s *Store) Purge(ctx context.Context, id, cutoff string, removeDir func() error) (bool, error) {
	unlock := s.locks.lock(id)
	defer unlock()

	var expired bool
	if err := s.db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM af_jobs j WHERE "+expiredCondition+")", id, cutoff).Scan(&expired); err != nil {
		return false, fmt.Errorf("job %s: %w", id, err)
	}
	if !expired {
		return false, nil
	}
	if err := removeDir(); err != nil {
		return false, &DirectoryError{JobID: id, Err: err}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("job %s: %w", id, err)
	}
	defer tx.Rollback()
	for _, table := range []string{"af_events", "af_attempts"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE job_id = ?", id); err != nil {
			return false, fmt.Errorf("job %s: %w", id, err)
		}
	}
	// The follow-up of a triage job goes with it; a fix job keeps the id of its parent.
	if _, err := tx.ExecContext(ctx, "DELETE FROM af_followups WHERE triage_job_id = ?", id); err != nil {
		return false, fmt.Errorf("job %s: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM af_jobs WHERE id = ?", id); err != nil {
		return false, fmt.Errorf("job %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("job %s: %w", id, err)
	}
	return true, nil
}

// runnerInProgress reports whether a runner of the job has not returned yet, though its attempt
// may have been recorded cancelled when it overran its grace.
func (s *Service) runnerInProgress(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.inflight {
		if key.job == id {
			return true
		}
	}
	return false
}

// Purge removes the jobs that were finished more than the retention ago: done, failed and
// cancelled jobs with no running attempt and no runner still in progress, by the time they
// were finished. For each it removes the directory attempts/<job id>/ first and then its rows.
// A job whose directory cannot be removed keeps its rows, and a WARN names it; the next purge
// tries it again. It returns how many jobs it removed. A blocked job is never removed.
func (s *Service) Purge(ctx context.Context) (int, error) {
	cutoff := formatTime(s.clock.Now().Add(-s.retain))
	removed := 0
	var after expiredKey
	for {
		page, err := s.store.Expired(ctx, cutoff, after, purgeBatch)
		if err != nil {
			return removed, err
		}
		for _, k := range page {
			if s.stopped() {
				return removed, nil
			}
			if s.runnerInProgress(k.id) {
				continue
			}
			dir := filepath.Join(s.attemptsDir, k.id)
			gone, err := s.store.Purge(ctx, k.id, cutoff, func() error { return s.removeAll(dir) })
			var dirErr *DirectoryError
			switch {
			case errors.As(err, &dirErr):
				s.log.Warn("the attempts directory of an expired job could not be removed; its rows are kept for the next purge",
					"job", k.id, "error", dirErr.Err.Error())
			case err != nil:
				s.log.Warn("an expired job could not be removed; the next purge will try again", "job", k.id, "error", err.Error())
			case gone:
				removed++
			}
		}
		if len(page) < purgeBatch {
			return removed, nil
		}
		after = page[len(page)-1]
	}
}

// purgeLoop purges when it starts and then on every tick until the service stops.
func (s *Service) purgeLoop(ticker Ticker) {
	defer s.loops.Done()
	defer ticker.Stop()
	for {
		switch n, err := s.Purge(context.Background()); {
		case err != nil:
			s.log.Warn("the expired jobs could not be listed; the next purge will try again", "error", err.Error())
		case n > 0:
			s.log.Info("expired jobs were removed", "jobs", n)
		}
		select {
		case <-ticker.C():
		case <-s.stopping:
			return
		}
	}
}
