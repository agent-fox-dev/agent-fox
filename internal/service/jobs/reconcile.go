package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
)

// AttemptRef is an attempt of the database: its job, its number, the workspace it is for and its state.
type AttemptRef struct {
	JobID     string
	Attempt   int
	Workspace string
	State     AttemptState
}

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

// EndedAttempts are the attempts of the database that have ended: done, failed, cancelled or needs_human, oldest first.
func (s *Service) EndedAttempts(ctx context.Context) ([]AttemptRef, error) {
	rows, err := s.store.db.QueryContext(ctx, `SELECT a.job_id, a.n, j.workspace, a.state FROM af_attempts a JOIN af_jobs j ON j.id = a.job_id
		WHERE a.state IN ('done', 'needs_human', 'failed', 'cancelled') ORDER BY a.queue_seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AttemptRef
	for rows.Next() {
		var a AttemptRef
		if err := rows.Scan(&a.JobID, &a.Attempt, &a.Workspace, &a.State); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AttemptDir is the directory of the files of the attempt, attempts/<job id>/<n>/ beside the database, which
// the worker keeps its journal in.
func (s *Service) AttemptDir(jobID string, attempt int) string {
	return filepath.Join(s.attemptsDir, jobID, strconv.Itoa(attempt))
}
