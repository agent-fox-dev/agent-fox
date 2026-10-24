package jobs

import (
	"context"
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

// close ends the recorder: the calls in progress finish, and the ones that follow do
// nothing.
func (r *Recorder) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
}
