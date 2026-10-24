package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// The cancel cleanup puts the labels of a cancelled job's issue back in line with the job's record when no runner does:
// a job cancelled while it was queued or blocked, or whose attempt was in no runner of this process, or whose runner was
// cancelled before it opened its hub session. A cleanup is marked pending, durably, with the cancel (or with the cancelled
// attempt), and is then tried in the background, by a Cleaner that this layer does not know the workings of: the layer
// never imports hub. A try reads the issue and writes only what is missing, so it may be repeated.

// Cleaner carries out one try of a job's cancel cleanup and says what it came to. It must never put a token in what it
// returns.
type Cleaner interface {
	Clean(ctx context.Context, req CleanupRequest) CleanupResult
}

// CleanupRequest is what a try is about: the job, its issue, the request label it consumed ("" for none), the status
// labels its own attempts set as its runners last reported them, the labels the other open jobs of the issue show set,
// and whether another job of the issue is running.
type CleanupRequest struct {
	Workspace, JobID string
	Issue            int64
	RequestLabel     string
	LabelsSet        []string
	OthersSet        []string
	OtherRunning     bool
}

// CleanupOutcome is the state a try leaves the cleanup in.
type CleanupOutcome string

// The states of a cleanup, as labels.cleanup shows them.
const (
	// CleanupPending: the cleanup has not finished, and tries go on.
	CleanupPending CleanupOutcome = "pending"
	// CleanupDone: the request label is on the issue, and the status labels the job accounts for are gone.
	CleanupDone CleanupOutcome = "done"
	// CleanupLabelNotSet: the cleanup ended without that.
	CleanupLabelNotSet CleanupOutcome = "label_not_set"
)

// CleanupResult is what a try came to: the state it leaves, the labels it set and removed (nothing for a try that wrote
// nothing), whether the issue carries the request label after it, and, for a try that did not finish the cleanup or
// ended it without the labels, what is logged and at which level.
type CleanupResult struct {
	Outcome        CleanupOutcome
	Set, Removed   []string
	RequestLabelOn bool
	Level          slog.Level
	Message        string
}

const (
	// tryBound is how long a whole try may take, by the layer's clock.
	tryBound = time.Minute
	// maxTries is how many tries may be in flight at once.
	maxTries = 4
)

// cleanups are the tries in flight, one at most for a job, and the slots that bound how many talk to hub at once.
type cleanups struct {
	mu    sync.Mutex
	tries map[string]chan struct{} // job id -> closed when its try ends
	slots chan struct{}
	// unrecorded are the jobs whose try's result the store failed to record, which is warned of once.
	unrecorded map[string]bool
}

// errTryBound is the cause of a try that ran out of its minute.
var errTryBound = errors.New("the try of the cancel cleanup ran out of its minute")

// startTry starts a try of the job's cleanup in the background, unless one is in flight, and returns what is closed when
// the try in flight ends. It never waits for the try. A layer without a cleaner, or one that is stopping, starts none, and
// what it returns is closed.
func (s *Service) startTry(id string) <-chan struct{} {
	c := &s.cleanups
	c.mu.Lock()
	defer c.mu.Unlock()
	if done, ok := c.tries[id]; ok {
		return done
	}
	done := make(chan struct{})
	if s.cleaner == nil || s.stopped() {
		close(done)
		return done
	}
	c.tries[id] = done
	s.loops.Add(1)
	go func() {
		defer s.loops.Done()
		defer func() {
			c.mu.Lock()
			delete(c.tries, id)
			c.mu.Unlock()
			close(done)
		}()
		s.try(id)
	}()
	return done
}

// try makes one try of the job's cleanup, once a slot is free, bounded by a minute of the layer's clock and cut short by a
// shutdown, which leaves the cleanup pending.
func (s *Service) try(id string) {
	select {
	case s.cleanups.slots <- struct{}{}:
	case <-s.stopping:
		return
	}
	defer func() { <-s.cleanups.slots }()

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	bound := s.clock.After(tryBound)
	go func() {
		select {
		case <-bound:
			cancel(errTryBound)
		case <-s.stopping:
			cancel(ErrShutdown)
		case <-ctx.Done():
		}
	}()

	req, pending, err := s.store.CleanupRequest(ctx, id)
	if err != nil || !pending {
		if err != nil && !s.stopped() {
			s.noteCleanup(context.Background(), id, req, CleanupResult{Outcome: CleanupPending, Level: slog.LevelWarn, Message: "the cancel cleanup could not read its job: " + err.Error()})
		}
		return
	}
	res := s.cleaner.Clean(ctx, req)
	if errors.Is(context.Cause(ctx), ErrShutdown) {
		return // the cleanup stays pending, for the first sweep after the restart
	}
	if res.Outcome == "" {
		res.Outcome = CleanupPending
	}
	s.noteCleanup(context.Background(), id, req, res)
}

// noteCleanup records what a try came to and logs it: one INFO record for a cleanup that is done, the record the result
// carries for one that ended otherwise, and a WARN for the first failure of a cleanup that stays pending only.
func (s *Service) noteCleanup(ctx context.Context, id string, req CleanupRequest, res CleanupResult) {
	recorded, firstWarning, err := s.store.EndCleanup(ctx, id, res)
	if err != nil {
		s.cleanups.mu.Lock()
		first := !s.cleanups.unrecorded[id]
		s.cleanups.unrecorded[id] = true
		s.cleanups.mu.Unlock()
		if first {
			s.log.Warn("what a try of the cancel cleanup came to could not be recorded; the cleanup stays pending",
				"workspace", req.Workspace, "job", id, "error", err.Error())
		}
		return
	}
	if !recorded {
		return // the cleanup had ended meanwhile, and what this try came to is not its outcome
	}
	attrs := []any{"workspace", req.Workspace, "job", id, "issue", req.Issue}
	switch res.Outcome {
	case CleanupDone:
		s.log.Info("the cancel cleanup of a job put its issue's labels back in line with its record",
			append(attrs, "set", nonNil(res.Set), "removed", nonNil(res.Removed))...)
	case CleanupLabelNotSet:
		s.log.Log(ctx, res.Level, "the cancel cleanup of a job ended without its labels: "+res.Message,
			append(attrs, "set", nonNil(res.Set), "removed", nonNil(res.Removed))...)
	default:
		if firstWarning && res.Message != "" {
			s.log.Warn("a try of the cancel cleanup of a job failed; it is tried again at the next sweep: "+res.Message, attrs...)
		}
	}
}

// tryOf is what is closed when the job's try in flight ends, or nil when none is in flight.
func (s *Service) tryOf(id string) <-chan struct{} {
	s.cleanups.mu.Lock()
	defer s.cleanups.mu.Unlock()
	if done, ok := s.cleanups.tries[id]; ok {
		return done
	}
	return nil
}

// tryInFlight says that a try of the job's cleanup is in flight.
func (s *Service) tryInFlight(id string) bool {
	s.cleanups.mu.Lock()
	defer s.cleanups.mu.Unlock()
	_, ok := s.cleanups.tries[id]
	return ok
}

// pendingCleanup is a cleanup that is pending, and when its job was cancelled.
type pendingCleanup struct {
	id, workspace string
	cancelled     time.Time
}

// PendingCleanups are the cleanups that are pending, oldest cancel first.
func (s *Store) PendingCleanups(ctx context.Context) ([]pendingCleanup, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, workspace, finished_at FROM af_jobs WHERE cleanup = 'pending' ORDER BY finished_at, id")
	if err != nil {
		return nil, fmt.Errorf("pending cleanups: %w", err)
	}
	defer rows.Close()
	var out []pendingCleanup
	for rows.Next() {
		var p pendingCleanup
		var finished sql.NullString
		if err := rows.Scan(&p.id, &p.workspace, &finished); err != nil {
			return nil, fmt.Errorf("pending cleanups: %w", err)
		}
		if p.cancelled, err = time.Parse(timeLayout, finished.String); err != nil {
			return nil, fmt.Errorf("pending cleanup of %s: the cancel time %q: %w", p.id, finished.String, err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AbandonCleanup ends a pending cleanup label_not_set, and says whether it did.
func (s *Store) AbandonCleanup(ctx context.Context, id string) (bool, error) {
	res, err := s.db.ExecContext(ctx, "UPDATE af_jobs SET cleanup = 'label_not_set' WHERE id = ? AND cleanup = 'pending'", id)
	if err != nil {
		return false, fmt.Errorf("cleanup of %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// CleanupPending says whether the job's cleanup is pending.
func (s *Store) CleanupPending(ctx context.Context, id string) (bool, error) {
	var pending bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM af_jobs WHERE id = ? AND cleanup = 'pending')", id).Scan(&pending)
	return pending, err
}

// MarkCleanup marks the cancel cleanup of a cancelled job pending, if it is wanted and not marked already, and says
// whether it did.
func (s *Store) MarkCleanup(ctx context.Context, id string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		"UPDATE af_jobs SET cleanup = 'pending' WHERE id = ? AND state = 'cancelled' AND cleanup_wanted = 1 AND cleanup IS NULL", id)
	if err != nil {
		return false, fmt.Errorf("cleanup of %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// CleanupRequest is what a try of the job's cleanup is about, and whether the cleanup is pending.
func (s *Store) CleanupRequest(ctx context.Context, id string) (CleanupRequest, bool, error) {
	req := CleanupRequest{JobID: id}
	var issue sql.NullInt64
	var label, set, cleanup, cause sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT workspace, issue, request_label, labels_set, cleanup, cancel_cause FROM af_jobs WHERE id = ?", id).
		Scan(&req.Workspace, &issue, &label, &set, &cleanup, &cause)
	if errors.Is(err, sql.ErrNoRows) {
		return req, false, nil
	}
	if err != nil {
		return req, false, fmt.Errorf("job %s: %w", id, err)
	}
	if cleanup.String != string(CleanupPending) || !issue.Valid {
		return req, false, nil
	}
	req.Issue, req.RequestLabel = issue.Int64, label.String
	if err := decode("labels_set", set, &req.LabelsSet); err != nil {
		return req, false, fmt.Errorf("job %s: %w", id, err)
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT state, labels_set FROM af_jobs WHERE workspace = ? AND issue = ? AND id <> ? AND state IN ('queued', 'running', 'blocked')",
		req.Workspace, req.Issue, id)
	if err != nil {
		return req, false, fmt.Errorf("the other jobs of the issue of %s: %w", id, err)
	}
	defer rows.Close()
	for rows.Next() {
		var state State
		var others sql.NullString
		if err := rows.Scan(&state, &others); err != nil {
			return req, false, err
		}
		var labels []string
		if err := decode("labels_set", others, &labels); err != nil {
			return req, false, err
		}
		req.OthersSet = append(req.OthersSet, labels...)
		req.OtherRunning = req.OtherRunning || state == StateRunning
	}
	return req, true, rows.Err()
}

// EndCleanup records what a try of a pending cleanup came to, in one transaction: what a try that ends the cleanup set and
// removed replaces the job's labels when it wrote anything, the request label is no longer to be put back when the issue carries it, and the
// cleanup takes the outcome. A cleanup that stays pending is noted as warned. It says whether this was the cleanup's first
// failure, and whether the result was recorded: a cleanup that is no longer pending is left as it is.
func (s *Store) EndCleanup(ctx context.Context, id string, res CleanupResult) (recorded, firstFailure bool, err error) {
	err = s.run(ctx, id, func(x *txn) error {
		// The first statement is a write, which takes the lock of the file (see Store.run).
		touched, err := x.ExecContext(ctx, "UPDATE af_jobs SET cleanup_warned = cleanup_warned WHERE id = ? AND cleanup = 'pending'", id)
		if err != nil {
			return fmt.Errorf("cleanup of %s: %w", id, err)
		}
		if n, err := touched.RowsAffected(); err != nil || n == 0 {
			return err // not pending: another try or the abandonment ended it
		}
		recorded = true
		var warned bool
		if err := x.QueryRowContext(ctx, "SELECT cleanup_warned FROM af_jobs WHERE id = ?", id).Scan(&warned); err != nil {
			return fmt.Errorf("cleanup of %s: %w", id, err)
		}
		// What a try that leaves the cleanup pending wrote is not recorded: the next try reads the issue again, and must
		// still find in labels.set what the job's attempts set.
		if res.Outcome != CleanupPending && (len(res.Set) > 0 || len(res.Removed) > 0) {
			if _, err := x.ExecContext(ctx, "UPDATE af_jobs SET labels_set = ?, labels_removed = ? WHERE id = ?",
				mustJSON(nonNil(res.Set)), mustJSON(nonNil(res.Removed)), id); err != nil {
				return fmt.Errorf("labels of %s: %w", id, err)
			}
		}
		if res.RequestLabelOn {
			if _, err := x.ExecContext(ctx, "UPDATE af_jobs SET labels_restore = NULL WHERE id = ?", id); err != nil {
				return fmt.Errorf("labels of %s: %w", id, err)
			}
		}
		if res.Outcome == CleanupPending {
			firstFailure = !warned
			_, err = x.ExecContext(ctx, "UPDATE af_jobs SET cleanup_warned = 1 WHERE id = ?", id)
			return err
		}
		_, err = x.ExecContext(ctx, "UPDATE af_jobs SET cleanup = ? WHERE id = ?", string(res.Outcome), id)
		return err
	})
	return recorded && err == nil, firstFailure, err
}

// abandonCleanup ends a cleanup whose hour has passed, unless a try of it is in flight, which keeps the lock of the tries
// so that no try starts meanwhile. It says whether it ended it.
func (s *Service) abandonCleanup(ctx context.Context, id string) (bool, error) {
	s.cleanups.mu.Lock()
	defer s.cleanups.mu.Unlock()
	if _, inFlight := s.cleanups.tries[id]; inFlight {
		return false, nil
	}
	return s.store.AbandonCleanup(ctx, id)
}
