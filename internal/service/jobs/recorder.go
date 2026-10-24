package jobs

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// Recorder is where a runner reports while its attempt runs: events for the log of
// the job, and the references to its session on hub. It is safe for concurrent use.
//
// It belongs to one attempt, and ends with it: once the attempt has ended, what the
// runner still sends is dropped, and the end waits for the calls that are in
// progress, so that nothing of the runner is stored after the attempt's last event.
type Recorder struct {
	store   *Store
	log     *slog.Logger
	jobID   string
	attempt int

	mu     sync.RWMutex // read by the calls of the runner, written by the end
	closed bool
}

func newRecorder(st *Store, log *slog.Logger, jobID string, attempt int) *Recorder {
	return &Recorder{store: st, log: log, jobID: jobID, attempt: attempt}
}

// Emit records an event of the attempt in the log of the job, where a client can
// follow it. A type that is malformed or that is one of the layer's own, data that is
// too large and data that cannot be encoded are replaced, and an event that cannot be
// stored is logged and dropped: reporting never fails the attempt.
func (r *Recorder) Emit(typ string, data any) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return
	}
	if err := r.store.AppendEvent(context.Background(), r.jobID, r.attempt, typ, data); err != nil {
		r.log.Warn("an event of the attempt could not be stored and is dropped",
			"job", r.jobID, "attempt", r.attempt, "error", err.Error())
	}
}

// Hub records the references of the attempt's session on hub, which the job shows
// while the attempt runs. An empty reference is none.
func (r *Recorder) Hub(sessionID, runID, sandboxID string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return
	}
	if err := r.store.SetHub(context.Background(), r.jobID, r.attempt, sessionID, runID, sandboxID); err != nil {
		r.log.Warn("the hub references of the attempt could not be stored and are dropped",
			"job", r.jobID, "attempt", r.attempt, "error", err.Error())
	}
}

// errRecorderClosed is the refusal of a call of a runner whose attempt has ended.
var errRecorderClosed = errors.New("the attempt has ended")

// RecordFollowUp records that the attempt's triage job asks for a fix of the issue with the label, as
// Service.RecordFollowUp does, and returns the state of the follow-up as recorded and the fix job it names, if any. The
// worker calls it for a triage that ends ready on a workspace with AF_AUTO_FIX, before it adds the label.
func (r *Recorder) RecordFollowUp(issue int64, label string) (state, job string, err error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return "", "", errRecorderClosed
	}
	f, err := r.store.RecordFollowUp(context.Background(), r.jobID, issue, label)
	if err != nil {
		return "", "", err
	}
	if f.JobID != nil {
		job = *f.JobID
	}
	return f.State, job, nil
}

// WithdrawFollowUp withdraws the pending follow-up of the attempt's triage job: the worker could not add its label.
func (r *Recorder) WithdrawFollowUp() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return errRecorderClosed
	}
	return r.store.settleFollowUp(context.Background(), r.jobID, FollowUpWithdrawn, nil)
}

// close ends the recorder: the calls in progress finish, and the ones that follow do
// nothing.
func (r *Recorder) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
}
