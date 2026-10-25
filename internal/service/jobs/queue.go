package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agent-fox-dev/aflib/jobqueue"
)

// What the layer adds to aflib/jobqueue, and why. The queue keeps the items durable,
// claims them, hands one back after a delay and, when it starts, hands over again the
// ones that were claimed when the process before it ended. Each attempt is one item of
// the type attempt, with the nonce <job id>:<n> and the payload {job_id, attempt}. Four
// things the queue does are not what the layer needs, and the layer works around them:
//
//   - Enqueue refuses a second queued or running item with the same type and key, so the
//     key cannot be the workspace, which has many attempts waiting. The key is the nonce,
//     and the workspace is the group of the item, which the queue serializes by: one item
//     of a group runs at a time.
//   - The queue claims by a time of one second, so two attempts of a workspace made in the
//     same second can come in either order. The attempts of a workspace are numbered in the
//     order they were made (queue_seq), and a handler whose attempt is not the first open
//     attempt of its workspace hands its item back (see Store.Plan).
//   - A handler that returns without an error completes its item. A handler that is to
//     leave its attempt where it stands, for the queue to hand over again, returns an error
//     that the queue is to retry, and the item comes back after the same delay each time.
//   - The nonce is unique for as long as the item is in the queue's table, so enqueuing an
//     attempt that the queue has, in any state, is a no-op.
const attemptType = "attempt"

// attemptPayload is the payload of an item.
type attemptPayload struct {
	JobID   string `json:"job_id"`
	Attempt int    `json:"attempt"`
}

// Errors of a handler that ask the queue to hand the item back.
var (
	// errYield: the attempt is not the first of its workspace.
	errYield = errors.New("the attempt is not the first of its workspace")
	// errHandedBack: the service is stopping, and the attempt is left as it is.
	errHandedBack = errors.New("the service is stopping")
)

// Enqueue puts the attempt in the queue, belonging to the workspace of its job. An
// attempt that the queue has already, pending, delayed, claimed or done, is left as it
// is.
func (s *Service) Enqueue(ctx context.Context, jobID string, attempt int) error {
	workspace, err := s.store.WorkspaceOf(ctx, jobID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(attemptPayload{JobID: jobID, Attempt: attempt})
	if err != nil {
		return err
	}
	nonce := jobID + ":" + strconv.Itoa(attempt)
	_, _, err = s.queue.Enqueue(jobqueue.EnqueueParams{
		Type: attemptType, Key: nonce, Nonce: nonce, Payload: payload, SubmittedBy: "agent-fox", Group: workspace,
	})
	if err != nil {
		return fmt.Errorf("attempt %d of %s: %w", attempt, jobID, err)
	}
	return nil
}

// attemptKey names an attempt.
type attemptKey struct {
	job     string
	attempt int
}

// attemptRun is an attempt that is in a runner.
type attemptRun struct {
	key       attemptKey
	workspace string
	cancel    context.CancelCauseFunc
	recorder  *Recorder
	// returned is closed when the runner has returned. settled is closed when it has,
	// or when the attempt has been recorded cancelled without it.
	returned   chan struct{}
	settled    chan struct{}
	settleOnce sync.Once
	graceOnce  sync.Once
	// overran is set when the attempt was recorded cancelled without its runner, so
	// that what the runner returns later is dropped.
	overran atomic.Bool
	// done is closed when the handler is through with what the runner returned: the
	// attempt is recorded, and a cancel cleanup it marked has its first try started.
	done     chan struct{}
	doneOnce sync.Once
	// callMu orders the call of the runner with a cancel: called is set, under it, when the
	// runner is about to be called, and a cancel under it tells whether it reached the runner.
	callMu sync.Mutex
	called bool
}

// stop cancels the run with the cause, and says whether its runner has been called: one
// that has not is never called after this.
func (r *attemptRun) stop(cause error) (reached bool) {
	r.callMu.Lock()
	defer r.callMu.Unlock()
	r.cancel(cause)
	return r.called
}

// call says that the runner is about to be called, unless the run was stopped first.
func (r *attemptRun) call(ctx context.Context) bool {
	r.callMu.Lock()
	defer r.callMu.Unlock()
	if context.Cause(ctx) != nil {
		return false
	}
	r.called = true
	return true
}

// handled says that the handler is through with what the runner returned.
func (r *attemptRun) handled() { r.doneOnce.Do(func() { close(r.done) }) }

// settle says that the attempt is no longer in the hands of its runner, as far as the
// jobs are concerned.
func (r *attemptRun) settle() { r.settleOnce.Do(func() { close(r.settled) }) }

// makeDir makes the directory, private to the user, when it is missing.
func makeDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("attempts directory %s: %w", dir, err)
	}
	return nil
}

// handle is the handler of the queue for an attempt. It returns a nil error when the
// item is done with, and an error that asks for a retry when the item is to come back.
func (s *Service) handle(qctx context.Context, payload json.RawMessage) (any, bool, error) {
	var p attemptPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, false, fmt.Errorf("the payload of the item: %w", err) // no retry can mend it
	}
	// The work is not cut short by the queue; the layer says when it ends.
	ctx := context.WithoutCancel(qctx)

	what, err := s.store.Plan(ctx, p.JobID, p.Attempt)
	if err != nil {
		return nil, true, err
	}
	switch what {
	case dispAck:
		return nil, false, nil
	case dispReap:
		// A runner was running when the job was cancelled, and the process ended before it
		// returned: no runner has it now.
		if _, cleanup := s.settleCancelled(ctx, attemptKey{p.JobID, p.Attempt}, "the service stopped while the attempt was running, after the job was cancelled"); cleanup {
			s.startTry(p.JobID)
		}
		return nil, false, nil
	case dispYield:
		return nil, true, errYield
	}
	return s.attempt(ctx, p)
}

// stopped reports whether the service is stopping.
func (s *Service) stopped() bool {
	select {
	case <-s.stopping:
		return true
	default:
		return false
	}
}

// acquire waits for a slot, and says whether it got one. A service that is stopping
// grants none, even when a slot is free: a select between the two would choose either. (An
// attempt that gets its slot as the service begins to stop is told of the shutdown by
// invoke, and is handed back.)
func (s *Service) acquire() bool {
	if s.stopped() {
		return false
	}
	select {
	case s.slots <- struct{}{}:
		return true
	case <-s.stopping:
		return false
	}
}

// release gives the slot back.
func (s *Service) release() { <-s.slots }

// watchWait registers that the handler of the attempt is about to wait out a backoff, and
// returns what wakes it: a cancel of the job. It is registered before the attempt is put
// back, so that a cancel in between is not missed.
func (s *Service) watchWait(key attemptKey) <-chan struct{} {
	wake := make(chan struct{})
	s.mu.Lock()
	s.waiting[key] = wake
	s.mu.Unlock()
	return wake
}

// unwatchWait forgets the wait.
func (s *Service) unwatchWait(key attemptKey) {
	s.mu.Lock()
	delete(s.waiting, key)
	s.mu.Unlock()
}

// pause waits for d on the clock, and says whether it did: it gives up when the service
// stops. A cancel of the job ends the wait too, and the attempt that the handler goes
// round to claim again is cancelled, so the claim fails and the handler lets go.
func (s *Service) pause(d time.Duration, wake <-chan struct{}) bool {
	select {
	case <-s.clock.After(d):
		return true
	case <-wake:
		return true
	case <-s.stopping:
		return false
	}
}

// attempt hands the attempt over to the runner and sees it through. Each pass is a slot,
// a claim and a call. A transient failure puts the attempt back, gives up the slot for the
// wait that the failure calls for (the attempt keeps the place of its workspace, because
// this handler is the one the queue runs for the workspace) and goes round again; a
// shutdown leaves the attempt as it is; anything else ends it.
func (s *Service) attempt(ctx context.Context, p attemptPayload) (any, bool, error) {
	for {
		if !s.acquire() {
			return nil, true, errHandedBack
		}
		_, err := s.store.Claim(ctx, p.JobID, p.Attempt)
		switch {
		case errors.Is(err, ErrNotClaimable):
			s.release()
			return nil, false, nil
		case err != nil:
			s.release()
			s.log.Warn("an attempt could not be claimed; the queue will hand it over again",
				"job", p.JobID, "attempt", p.Attempt, "error", err.Error())
			return nil, true, err
		}

		req, out, cause, overran, run, err := s.invoke(ctx, p)
		if err == nil {
			switch {
			case overran:
				// The attempt was recorded cancelled when the grace ran out; what its runner
				// returned now is dropped.
				run.handled()
				s.release()
				return nil, false, nil
			case errors.Is(cause, ErrShutdown):
				// The attempt was stopped so that it can go on after the restart: what the
				// runner returned is not what the attempt came to.
				run.handled()
				s.release()
				return nil, true, errHandedBack
			case IsTransient(out.err) && !IsCancel(cause):
				key := attemptKey{p.JobID, p.Attempt}
				wake := s.watchWait(key)
				var wait time.Duration
				if wait, err = s.transient(ctx, req, out); err == nil && wait > 0 {
					run.handled()
					s.release()
					ok := s.pause(wait, wake)
					s.unwatchWait(key)
					if !ok {
						return nil, true, errHandedBack
					}
					continue
				}
				s.unwatchWait(key)
			default:
				err = s.finish(ctx, req, out, false, cause)
			}
		}
		run.handled()
		s.release()
		if err != nil {
			s.log.Warn("an attempt could not be ended or started; the queue will hand it over again",
				"job", p.JobID, "attempt", p.Attempt, "error", err.Error())
			return nil, true, err
		}
		return nil, false, nil
	}
}

// transient deals with a transient failure of a runner. The third ends the attempt, as
// failed, and never as a reason for an automatic attempt. The first and the second put the
// attempt back to queued and return how long to wait before it is handed over again.
func (s *Service) transient(ctx context.Context, req Request, out called) (time.Duration, error) {
	failures, err := s.store.NoteTransient(ctx, req.JobID, req.Attempt)
	if err != nil {
		return 0, err
	}
	if failures >= maxTransient {
		return 0, s.finish(ctx, req, out, true, nil)
	}
	returned, err := s.store.Backoff(ctx, req.JobID, req.Attempt)
	if err != nil || !returned {
		return 0, err
	}
	return backoffs[failures-1], nil
}

// invoke makes the request for the attempt that was claimed, calls the runner with a
// context that is cancelled with a cause when the attempt has to stop, and returns what it
// came to, with the cause of the cancellation of the context at the moment it returned,
// and whether the attempt was recorded cancelled without the runner in the meantime. The
// attempt is known to the layer before its request is read, so that a cancel is either
// seen by the read or finds the attempt: a runner is not called for a job that is cancelled.
func (s *Service) invoke(ctx context.Context, p attemptPayload) (req Request, out called, cause error, overran bool, run *attemptRun, err error) {
	rctx, cancel := context.WithCancelCause(context.Background())
	run = &attemptRun{
		key: attemptKey{p.JobID, p.Attempt}, cancel: cancel, recorder: newRecorder(s.store, s.log, p.JobID, p.Attempt),
		returned: make(chan struct{}), settled: make(chan struct{}), done: make(chan struct{}),
	}
	s.mu.Lock()
	s.inflight[run.key] = run
	s.mu.Unlock()
	defer s.forget(run)
	if s.stopped() { // the service began to stop after the claim, and before the attempt was known
		cancel(ErrShutdown)
	}

	if err := s.store.fails(OpInvoke); err != nil {
		return req, out, nil, false, run, err
	}
	req, err = s.store.RunRequest(ctx, p.JobID, p.Attempt)
	if err != nil {
		return req, out, nil, false, run, err
	}
	run.workspace = req.Workspace
	req.Dir = filepath.Join(s.attemptsDir, p.JobID, strconv.Itoa(p.Attempt))
	if err := makeDir(req.Dir); err != nil {
		return req, out, nil, false, run, err
	}
	req.Recorder = run.recorder
	state, err := s.store.JobState(ctx, p.JobID)
	if err != nil {
		return req, out, nil, false, run, err
	}
	if state == StateCancelled {
		cancel(ErrCancelled)
	}

	if !run.call(rctx) {
		out = called{err: context.Cause(rctx)} // stopped before it began: the runner is not called
	} else {
		if err := s.store.MarkInRunner(ctx, p.JobID, p.Attempt); err != nil {
			return req, out, nil, false, run, err
		}
		out = s.callRunner(rctx, req)
	}
	return req, out, context.Cause(rctx), run.overran.Load(), run, nil
}

// forget takes the attempt out of the runners: its recorder ends, its context is released,
// and whoever waits for it is told.
func (s *Service) forget(run *attemptRun) {
	run.recorder.close()
	s.mu.Lock()
	delete(s.inflight, run.key)
	s.mu.Unlock()
	close(run.returned)
	run.settle()
	run.cancel(nil)
	if run.overran.Load() {
		// The attempt was recorded cancelled when the grace ran out, which may have marked a cleanup that waited for the
		// runner to return.
		if pending, err := s.store.CleanupPending(context.Background(), run.key.job); err == nil && pending {
			s.startTry(run.key.job)
		}
	}
}
