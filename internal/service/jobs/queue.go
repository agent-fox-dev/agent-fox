package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/agent-fox-dev/aflib/jobqueue"
)

// What the layer adds to aflib/jobqueue, and why. The queue keeps the items durable,
// claims them, hands one back after a delay and, when it starts, hands over again the
// ones that were claimed when the process before it ended. Each attempt is one item of
// the type attempt, with the nonce <job id>:<n> and the payload {job_id, attempt}. Four
// things differ from what the spec assumed of the queue:
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

// errYield is the error of a handler that asks the queue to hand its item back
// because the attempt is not the first of its workspace.
var errYield = errors.New("the attempt is not the first of its workspace")

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
	returned  chan struct{}
}

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
	case dispYield:
		return nil, true, errYield
	}

	_, err = s.store.Claim(ctx, p.JobID, p.Attempt)
	switch {
	case errors.Is(err, ErrNotClaimable):
		return nil, false, nil
	case err != nil:
		s.log.Warn("an attempt could not be claimed; the queue will hand it over again",
			"job", p.JobID, "attempt", p.Attempt, "error", err.Error())
		return nil, true, err
	}
	if err := s.run(ctx, p); err != nil {
		s.log.Warn("an attempt could not be started; the queue will hand it over again",
			"job", p.JobID, "attempt", p.Attempt, "error", err.Error())
		return nil, true, err
	}
	return nil, false, nil
}

// run calls the runner for the attempt that was claimed, and records what it came to.
func (s *Service) run(ctx context.Context, p attemptPayload) error {
	req, err := s.store.RunRequest(ctx, p.JobID, p.Attempt)
	if err != nil {
		return err
	}
	req.Dir = filepath.Join(s.attemptsDir, p.JobID, strconv.Itoa(p.Attempt))
	if err := makeDir(req.Dir); err != nil {
		return err
	}
	req.Recorder = newRecorder(s.store, s.log, p.JobID, p.Attempt)

	rctx, cancel := context.WithCancelCause(context.Background())
	run := &attemptRun{key: attemptKey{p.JobID, p.Attempt}, workspace: req.Workspace, cancel: cancel, recorder: req.Recorder, returned: make(chan struct{})}
	s.mu.Lock()
	s.inflight[run.key] = run
	s.mu.Unlock()

	out := s.call(rctx, req)

	req.Recorder.close()
	s.mu.Lock()
	delete(s.inflight, run.key)
	s.mu.Unlock()
	close(run.returned)
	cancel(nil)
	return s.finish(ctx, req, out)
}
