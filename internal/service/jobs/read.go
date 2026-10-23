package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

// Envelope is the envelope.json of an attempt of a job, byte for byte as the attempt
// stored it. It is ErrNotFound when the job or the attempt is not there, and when the attempt
// has no envelope yet; the reader is told the same in every case.
func (s *Service) Envelope(ctx context.Context, id string, n int) ([]byte, error) {
	return s.attemptFile(ctx, id, n, "has_envelope", "envelope.json")
}

// Report is the report.md of an attempt of a job, byte for byte as the attempt stored it. It
// is ErrNotFound when the job or the attempt is not there, and when the attempt has no
// report.
func (s *Service) Report(ctx context.Context, id string, n int) ([]byte, error) {
	return s.attemptFile(ctx, id, n, "has_report", "report.md")
}

// attemptFile reads a file of an attempt that its row says is there. flag is the column of
// the row that says so; it is a name from this package.
func (s *Service) attemptFile(ctx context.Context, id string, n int, flag, name string) ([]byte, error) {
	var stored bool
	err := s.store.db.QueryRowContext(ctx, "SELECT "+flag+" FROM af_attempts WHERE job_id = ? AND n = ?", id, n).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !stored {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("attempt %d of %s: %w", n, id, err)
	}
	data, err := os.ReadFile(filepath.Join(s.attemptsDir, id, strconv.Itoa(n), name))
	if errors.Is(err, fs.ErrNotExist) {
		// The row says it is there and the file is not: a purge is under way, or someone took it.
		s.log.Warn("a file of an attempt that the store lists is not on disk", "job", id, "attempt", n, "file", name)
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("attempt %d of %s: %w", n, id, err)
	}
	return data, nil
}

// Standing is what a trigger that makes jobs needs to know of one of them afterwards: how it stands, and whether it
// can have put its request label back.
type Standing struct {
	State State
	// Source and RequestLabel are the job's own; RequestLabel is empty for a job that consumed none.
	Source       Source
	RequestLabel string
	// CancelledInRunner says that the job was cancelled while one of its attempts was in a runner (an unsubscribe
	// included), which is when the cancel cleanup of the worker writes the request label back to the issue. It is read
	// from the attempts, so it holds after the runner has returned and after its grace has run out.
	CancelledInRunner bool
	// RunnerInProgress says that a runner of the job has not returned yet, though its attempt may have been recorded
	// cancelled when it overran its grace.
	RunnerInProgress bool
	// CleanupPending says that the job's cancel cleanup has not finished, and CleanupPutBack that it finished with the
	// request label on the issue (labels.restore null), whoever put it there.
	CleanupPending, CleanupPutBack bool
}

// Standing is the standing of a job: its state, source and request label, whether its cancel reached an attempt in a
// runner, whether a runner of it is still in progress, and how its cancel cleanup stands. A job that is not there, because retention purged it, is
// ErrNotFound.
//
// An attempt that was in a runner when the job was cancelled is marked as one whose runner was called, and is running still, or
// ended cancelled with an envelope; an attempt that was only queued, or that was claimed and cancelled before its runner was
// called, is not. That is how the cancel of a running attempt is told from the cancel of a job that waited, whatever became
// of the runner since.
func (s *Service) Standing(ctx context.Context, id string) (Standing, error) {
	var st Standing
	var label sql.NullString
	err := s.store.db.QueryRowContext(ctx,
		`SELECT state, source_kind, source_by, request_label,
		        state = 'cancelled' AND EXISTS (SELECT 1 FROM af_attempts a WHERE a.job_id = j.id AND a.in_runner = 1 AND (a.state = 'running' OR (a.state = 'cancelled' AND a.has_envelope = 1))),
		        cleanup IS 'pending', (cleanup IS 'done' OR cleanup IS 'label_not_set') AND labels_restore IS NULL
		 FROM af_jobs j WHERE j.id = ?`, id).Scan(&st.State, &st.Source.Kind, &st.Source.By, &label, &st.CancelledInRunner, &st.CleanupPending, &st.CleanupPutBack)
	if errors.Is(err, sql.ErrNoRows) {
		return Standing{}, ErrNotFound
	}
	if err != nil {
		return Standing{}, fmt.Errorf("job %s: %w", id, err)
	}
	st.RequestLabel = label.String
	st.RunnerInProgress = s.runnerInProgress(id)
	return st, nil
}
