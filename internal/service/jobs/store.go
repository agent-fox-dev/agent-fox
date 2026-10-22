package jobs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"modernc.org/sqlite"
)

// ErrNotFound is the error for a job that does not exist.
var ErrNotFound = errors.New("job not found")

// ErrDuplicate is the error for a second open job of the same kind for the same
// issue on a workspace.
var ErrDuplicate = errors.New("an open job of this kind exists for the issue")

// StateError is the error of a transition that the state of the job does not allow.
type StateError struct{ State State }

func (e *StateError) Error() string { return "the job is " + string(e.State) }

// NewJob is what a job is made from.
type NewJob struct {
	Workspace string
	Kind      Kind
	Input     Input
	Context   []string
	// Issue is the number of the issue the job is about, nil when it is about a
	// text or a file.
	Issue        *int64
	Source       Source
	RequestLabel string
}

// Event is a line of the log of a job. It is also the data of a frame of the event
// stream.
type Event struct {
	Seq     int64           `json:"seq"`
	TS      string          `json:"ts"`
	JobID   string          `json:"job_id"`
	Attempt int             `json:"attempt"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data"`
}

// The types of the events that the layer itself emits, which a runner may not.
const (
	EventJobState     = "job_state"
	EventAttemptStart = "attempt_start"
	EventAttemptEnd   = "attempt_end"
)

// jobStateData is the data of a job_state event.
type jobStateData struct {
	State   State      `json:"state"`
	Attempt int        `json:"attempt"`
	AtRest  bool       `json:"at_rest"`
	Blocked *Blocked   `json:"blocked,omitempty"`
	Result  *ResultDoc `json:"result,omitempty"`
}

// The operations that the fault seam of the store names.
const (
	OpInsertJob     = "insert_job"
	OpInsertAttempt = "insert_attempt"
	OpInsertEvent   = "insert_event"
	OpClaim         = "claim"
)

// Store keeps the jobs, their attempts and their events in the state file. Every
// change of a job is one transaction, taken under a lock of that job, and the events
// that it stores are published once it has committed, in the order of their numbers.
type Store struct {
	db  *sql.DB
	now func() time.Time

	locks keyedLock

	mu      sync.RWMutex
	publish func(Event)
	fault   func(op string) error
}

// NewStore returns the store over the state file's handle, with the clock that
// stamps every time it writes.
func NewStore(db *sql.DB, now func() time.Time) *Store { return &Store{db: db, now: now} }

// SetPublisher sets where the store hands the events it has stored. Publishing
// must not block.
func (s *Store) SetPublisher(publish func(Event)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publish = publish
}

// SetFault sets a seam for the tests: fault is asked before each operation of the
// store that has a name (see the Op constants), and an error it returns is the
// error of the operation.
func (s *Store) SetFault(fault func(op string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fault = fault
}

// fails is the error that the seam gives the operation, if any.
func (s *Store) fails(op string) error {
	s.mu.RLock()
	fault := s.fault
	s.mu.RUnlock()
	if fault == nil {
		return nil
	}
	return fault(op)
}

// keyedLock is a lock for each of a set of keys, forgotten when nobody holds it.
type keyedLock struct {
	mu    sync.Mutex
	locks map[string]*heldLock
}

type heldLock struct {
	sync.Mutex
	users int
}

// lock takes the lock of the key and returns the function that gives it back.
func (k *keyedLock) lock(key string) func() {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = map[string]*heldLock{}
	}
	l := k.locks[key]
	if l == nil {
		l = &heldLock{}
		k.locks[key] = l
	}
	l.users++
	k.mu.Unlock()

	l.Lock()
	return func() {
		l.Unlock()
		k.mu.Lock()
		if l.users--; l.users == 0 {
			delete(k.locks, key)
		}
		k.mu.Unlock()
	}
}

// txn is a transaction of one job, and the events it has stored.
type txn struct {
	*sql.Tx
	s      *Store
	jobID  string
	events []Event
}

// run does fn in a transaction, under the lock of the job. The first statement fn
// makes must be a write: a transaction that reads first cannot be upgraded to a
// write when another has written since. Once the transaction has committed, the
// events fn stored are published.
func (s *Store) run(ctx context.Context, jobID string, fn func(x *txn) error) error {
	unlock := s.locks.lock(jobID)
	defer unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	x := &txn{Tx: tx, s: s, jobID: jobID}
	if err := fn(x); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.mu.RLock()
	publish := s.publish
	s.mu.RUnlock()
	if publish != nil {
		for _, ev := range x.events {
			publish(ev)
		}
	}
	return nil
}

// emit stores an event of the layer, numbered after the last one, in the
// transaction. The number is taken by the statement that stores it.
func (x *txn) emit(ctx context.Context, attempt int, typ string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return x.emitRaw(ctx, attempt, typ, raw)
}

// emitRaw is emit for data that is JSON already.
func (x *txn) emitRaw(ctx context.Context, attempt int, typ string, raw json.RawMessage) error {
	ev, err := x.s.insertEvent(ctx, x.Tx, x.jobID, attempt, typ, raw)
	if err != nil {
		return err
	}
	x.events = append(x.events, ev)
	return nil
}

// insertEvent stores an event of the job with the next number, and returns it.
func (s *Store) insertEvent(ctx context.Context, tx *sql.Tx, jobID string, attempt int, typ string, data json.RawMessage) (Event, error) {
	if err := s.fails(OpInsertEvent); err != nil {
		return Event{}, err
	}
	ev := Event{JobID: jobID, Attempt: attempt, TS: formatTime(s.now()), Type: typ, Data: data}
	err := tx.QueryRowContext(ctx,
		`INSERT INTO af_events (job_id, seq, attempt, ts, type, data)
		 SELECT ?, COALESCE(MAX(seq), 0) + 1, ?, ?, ?, ? FROM af_events WHERE job_id = ?
		 RETURNING seq`,
		jobID, attempt, ev.TS, typ, string(data), jobID).Scan(&ev.Seq)
	if err != nil {
		return Event{}, fmt.Errorf("event of %s: %w", jobID, err)
	}
	return ev, nil
}

// formatTime is how a time is stored and shown: RFC 3339 in UTC, with milliseconds.
func formatTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// newJobID is job_ and 20 lowercase hex characters from the system's source of
// randomness.
func newJobID() (string, error) {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("job id: %w", err)
	}
	return "job_" + hex.EncodeToString(b[:]), nil
}

// uniqueViolation reports whether err is a violation of a unique index.
func uniqueViolation(err error) bool {
	var se *sqlite.Error
	// SQLITE_CONSTRAINT_UNIQUE; the only unique index that a job write can meet is
	// the one that keeps a second open job of a kind out of an issue.
	return errors.As(err, &se) && se.Code() == 2067
}

// AttemptsDir is the directory that holds the files of the attempts, beside the
// state file.
func AttemptsDir(databasePath string) string {
	return filepath.Join(filepath.Dir(databasePath), "attempts")
}

// EnsureAttemptsDir makes the directory of the attempts, with the mode 0700, when it
// is missing. A directory that is there is left as it is.
func EnsureAttemptsDir(databasePath string) (string, error) {
	dir := AttemptsDir(databasePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("attempts directory %s: %w", dir, err)
	}
	return dir, nil
}

// CreateJob stores a job, its attempt 1 and the job_state event for queued, all or
// none of them, and returns the id of the job. A second open job of the same kind
// for the same issue is ErrDuplicate.
func (s *Store) CreateJob(ctx context.Context, in NewJob) (string, error) {
	id, err := newJobID()
	if err != nil {
		return "", err
	}
	input, err := json.Marshal(in.Input)
	if err != nil {
		return "", err
	}
	contextStrings := in.Context
	if contextStrings == nil {
		contextStrings = []string{}
	}
	notes, err := json.Marshal(contextStrings)
	if err != nil {
		return "", err
	}
	var label any
	if in.RequestLabel != "" {
		label = in.RequestLabel
	}
	var issue any
	if in.Issue != nil {
		issue = *in.Issue
	}
	now := formatTime(s.now())

	err = s.run(ctx, id, func(x *txn) error {
		if err := s.fails(OpInsertJob); err != nil {
			return err
		}
		if _, err := x.ExecContext(ctx,
			`INSERT INTO af_jobs (id, workspace, kind, input, context, issue, source_kind, source_by, request_label, state, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'queued', ?)`,
			id, in.Workspace, string(in.Kind), string(input), string(notes), issue, in.Source.Kind, in.Source.By, label, now); err != nil {
			if uniqueViolation(err) {
				return ErrDuplicate
			}
			return fmt.Errorf("job %s: %w", id, err)
		}
		if err := x.insertAttempt(ctx, 1, Trigger{Kind: TriggerCreate, By: in.Source.By}, nil); err != nil {
			return err
		}
		return x.emit(ctx, 1, EventJobState, jobStateData{State: StateQueued, Attempt: 1})
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// insertAttempt stores attempt n of the job as queued, last in the order of all
// attempts.
func (x *txn) insertAttempt(ctx context.Context, n int, trigger Trigger, answer *Answer) error {
	if err := x.s.fails(OpInsertAttempt); err != nil {
		return err
	}
	var stored any
	if answer != nil {
		raw, err := json.Marshal(answer)
		if err != nil {
			return err
		}
		stored = string(raw)
	}
	if _, err := x.ExecContext(ctx,
		`INSERT INTO af_attempts (job_id, n, state, queue_seq, trigger_kind, trigger_by, answer)
		 VALUES (?, ?, 'queued', (SELECT COALESCE(MAX(queue_seq), 0) + 1 FROM af_attempts), ?, ?, ?)`,
		x.jobID, n, string(trigger.Kind), trigger.By, stored); err != nil {
		return fmt.Errorf("attempt %d of %s: %w", n, x.jobID, err)
	}
	return nil
}

// Requeue queues the next attempt of a job that is in state from, with the trigger
// and, for an answer, the answer; the job goes to queued. It returns the number of
// the new attempt. A job that is in another state is a *StateError, a job that does
// not exist is ErrNotFound, and a job that would be a second open job for its
// issue is ErrDuplicate; in each case nothing is changed.
func (s *Store) Requeue(ctx context.Context, id string, from State, trigger Trigger, answer *Answer) (int, error) {
	var n int
	err := s.run(ctx, id, func(x *txn) (err error) {
		n, err = x.requeue(ctx, from, trigger, answer)
		return err
	})
	return n, err
}

// requeue is Requeue inside a transaction of the job.
func (x *txn) requeue(ctx context.Context, from State, trigger Trigger, answer *Answer) (int, error) {
	changed, err := x.s.transition(ctx, x.Tx, x.jobID, []State{from}, StateQueued)
	if err != nil {
		return 0, err
	}
	if !changed {
		return 0, x.refusal(ctx)
	}
	var n int
	if err := x.QueryRowContext(ctx, "SELECT COALESCE(MAX(n), 0) + 1 FROM af_attempts WHERE job_id = ?", x.jobID).Scan(&n); err != nil {
		return 0, err
	}
	if err := x.insertAttempt(ctx, n, trigger, answer); err != nil {
		return 0, err
	}
	return n, x.emit(ctx, n, EventJobState, jobStateData{State: StateQueued, Attempt: n})
}

// refusal is the error of a transition that changed nothing: the job is not there,
// or it is in another state.
func (x *txn) refusal(ctx context.Context) error {
	var state State
	err := x.QueryRowContext(ctx, "SELECT state FROM af_jobs WHERE id = ?", x.jobID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return &StateError{State: state}
}

// transition moves a job, inside tx, from one of the states in from to the state
// to, and says whether it did: a job that is in another state is left as it is.
// It keeps the times that depend on the state: started_at is set when the job first
// runs, and finished_at when it enters done, failed or cancelled, and it is null
// otherwise, so that a job that queues again loses it. A move that would make a
// second open job for an issue is ErrDuplicate.
func (s *Store) transition(ctx context.Context, tx *sql.Tx, id string, from []State, to State) (bool, error) {
	now := formatTime(s.now())
	set := []string{"state = ?"}
	args := []any{string(to)}
	switch {
	case to.Finished():
		set = append(set, "finished_at = ?")
		args = append(args, now)
	default:
		set = append(set, "finished_at = NULL")
	}
	if to == StateRunning {
		set = append(set, "started_at = COALESCE(started_at, ?)")
		args = append(args, now)
	}
	marks := make([]string, len(from))
	args = append(args, id)
	for i, state := range from {
		marks[i] = "?"
		args = append(args, string(state))
	}
	res, err := tx.ExecContext(ctx,
		"UPDATE af_jobs SET "+strings.Join(set, ", ")+" WHERE id = ? AND state IN ("+strings.Join(marks, ", ")+")", args...)
	if err != nil {
		if uniqueViolation(err) {
			return false, ErrDuplicate
		}
		return false, fmt.Errorf("job %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// OpenJobID is the id of the open job of a kind for an issue on a workspace, or
// ErrNotFound.
func (s *Store) OpenJobID(ctx context.Context, workspace string, kind Kind, issue int64) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx,
		"SELECT id FROM af_jobs WHERE workspace = ? AND kind = ? AND issue = ? AND state IN ('queued', 'running', 'blocked')",
		workspace, string(kind), issue).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

// SetHub stores the references of the session of a running attempt on hub; an empty
// reference is none.
func (s *Store) SetHub(ctx context.Context, jobID string, attempt int, sessionID, runID, sandboxID string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE af_attempts SET hub_session = NULLIF(?, ''), hub_run = NULLIF(?, ''), hub_sandbox = NULLIF(?, '') WHERE job_id = ? AND n = ? AND state = 'running'",
		sessionID, runID, sandboxID, jobID, attempt)
	if err != nil {
		return fmt.Errorf("hub references of attempt %d of %s: %w", attempt, jobID, err)
	}
	return nil
}
