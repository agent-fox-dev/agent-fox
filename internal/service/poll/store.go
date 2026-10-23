// Package poll turns the request labels that it finds through hub into jobs, and consumes each label once.
package poll

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Seen is what the poll has seen of the job of a record.
type Seen string

// The things the poll can have seen of a job: that it has not ended, that it ended and holds nothing, and that it
// holds the request label that its cancel put back on the issue.
const (
	SeenOpen    Seen = "open"
	SeenEnded   Seen = "ended"
	SeenHolding Seen = "holding"
)

// Record is a row of af_poll_requests: the label of an issue of a workspace that the poll took, or absorbed. It carries
// what the rules of the poll need without the row of its job, which retention may have purged.
type Record struct {
	ID        int64
	Workspace string
	Issue     int
	Label     string
	// JobID is the job the poll made for the label, empty for a label that was absorbed.
	JobID string
	// Owed says that the removal of the label from the issue is still to be confirmed.
	Owed bool
	Seen Seen
	// ReleaseClock is when the release delay of a hold started to count, zero until it has.
	ReleaseClock time.Time
	CreatedAt    time.Time
}

// Records is the store of the records, over the state file.
type Records struct{ db *sql.DB }

// NewRecords returns the store over the state file's handle.
func NewRecords(db *sql.DB) *Records { return &Records{db: db} }

// columns are the columns every read selects, in the order scan reads them.
const columns = "id, workspace, issue, label, job_id, owed, seen, release_clock, created_at"

// formatTime is how a time is stored: RFC 3339 in UTC, to the nanosecond.
func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// Insert stores the record, which gets its id, and returns it as it was stored. An empty Seen is open.
func (r *Records) Insert(ctx context.Context, rec Record) (Record, error) {
	if rec.Seen == "" {
		rec.Seen = SeenOpen
	}
	var job, clock any
	if rec.JobID != "" {
		job = rec.JobID
	}
	if !rec.ReleaseClock.IsZero() {
		clock = formatTime(rec.ReleaseClock)
	}
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO af_poll_requests (workspace, issue, label, job_id, owed, seen, release_clock, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		rec.Workspace, rec.Issue, rec.Label, job, rec.Owed, string(rec.Seen), clock, formatTime(rec.CreatedAt)).Scan(&rec.ID)
	if err != nil {
		return Record{}, fmt.Errorf("poll record of %s #%d %s: %w", rec.Workspace, rec.Issue, rec.Label, err)
	}
	return rec, nil
}

// ForWorkspace are the records of the workspace, in the order they were made.
func (r *Records) ForWorkspace(ctx context.Context, workspace string) ([]Record, error) {
	return r.query(ctx, "SELECT "+columns+" FROM af_poll_requests WHERE workspace = ? ORDER BY id", workspace)
}

// Find are the records of the label on the issue of the workspace, in the order they were made.
func (r *Records) Find(ctx context.Context, workspace string, issue int, label string) ([]Record, error) {
	return r.query(ctx, "SELECT "+columns+" FROM af_poll_requests WHERE workspace = ? AND issue = ? AND label = ? ORDER BY id", workspace, issue, label)
}

// query reads the records the query selects.
func (r *Records) query(ctx context.Context, query string, args ...any) ([]Record, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("poll records: %w", err)
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var (
			rec          Record
			job, clock   sql.NullString
			seen, create string
		)
		if err := rows.Scan(&rec.ID, &rec.Workspace, &rec.Issue, &rec.Label, &job, &rec.Owed, &seen, &clock, &create); err != nil {
			return nil, fmt.Errorf("poll records: %w", err)
		}
		rec.JobID, rec.Seen = job.String, Seen(seen)
		if rec.CreatedAt, err = time.Parse(time.RFC3339Nano, create); err != nil {
			return nil, fmt.Errorf("poll record %d: created_at: %w", rec.ID, err)
		}
		if clock.Valid {
			if rec.ReleaseClock, err = time.Parse(time.RFC3339Nano, clock.String); err != nil {
				return nil, fmt.Errorf("poll record %d: release_clock: %w", rec.ID, err)
			}
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("poll records: %w", err)
	}
	return out, nil
}

// errNoRecord is the error of a change to a record that is not there.
var errNoRecord = errors.New("there is no such poll record")

// exec runs a change to the records, and says it when it changed none.
func (r *Records) exec(ctx context.Context, query string, args ...any) (int, error) {
	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("poll records: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// SetSeen records what the poll has seen of the job of the record.
func (r *Records) SetSeen(ctx context.Context, id int64, seen Seen) error {
	n, err := r.exec(ctx, "UPDATE af_poll_requests SET seen = ? WHERE id = ?", string(seen), id)
	if err == nil && n == 0 {
		err = fmt.Errorf("record %d: %w", id, errNoRecord)
	}
	return err
}

// StartClock starts the release clock of the record at the time, if it has not started.
func (r *Records) StartClock(ctx context.Context, id int64, at time.Time) error {
	_, err := r.exec(ctx, "UPDATE af_poll_requests SET release_clock = COALESCE(release_clock, ?) WHERE id = ?", formatTime(at), id)
	return err
}

// ClearOwed clears the owed removal of every record of the label on the issue of the workspace, whether the removal
// was confirmed or a complete listing settled it, and returns how many records it cleared.
func (r *Records) ClearOwed(ctx context.Context, workspace string, issue int, label string) (int, error) {
	return r.exec(ctx, "UPDATE af_poll_requests SET owed = 0 WHERE workspace = ? AND issue = ? AND label = ? AND owed = 1", workspace, issue, label)
}

// Drop deletes the records of the workspace for which nothing is owed and nothing holds: the removal was confirmed or
// settled, and the job ended holding nothing, or there is no job. It returns how many it deleted.
func (r *Records) Drop(ctx context.Context, workspace string) (int, error) {
	return r.exec(ctx, "DELETE FROM af_poll_requests WHERE workspace = ? AND owed = 0 AND (job_id IS NULL OR seen = 'ended')", workspace)
}

// Delete deletes the record.
func (r *Records) Delete(ctx context.Context, id int64) error {
	_, err := r.exec(ctx, "DELETE FROM af_poll_requests WHERE id = ?", id)
	return err
}
