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
