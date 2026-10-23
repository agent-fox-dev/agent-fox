package jobs_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// standing is the standing of the job, which must be there.
func (e *env) standing(id string) jobs.Standing {
	e.t.Helper()
	s, err := e.Svc.Standing(ctx, id)
	if err != nil {
		e.t.Fatalf("Standing(%s): %v", id, err)
	}
	return s
}

// TestTheJobsLayerReadsAJobByIdWithItsStateSourceLabelAndTheCancelAndRunnerFacts_TS_15_61 verifies TS-15-61: the
// standing of a job gives its state, its source, its request label, whether its cancel reached an attempt in a runner,
// and whether a runner of it is still in progress; the first is true from the cancel of a running attempt, whether the
// runner returns or overruns the grace, and stays true, the second is true until the runner has returned, a job
// cancelled while queued or blocked has neither, and a job that retention purged is the not-found error.
// Verifies: 15-REQ-9.2
func TestTheJobsLayerReadsAJobByIdWithItsStateSourceLabelAndTheCancelAndRunnerFacts_TS_15_61(t *testing.T) {
	clk := fakeclock.New(epoch)
	needsHuman := fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which?", "a", "b")))
	r := fakerunner.New(fakerunner.BlockIgnoringContext(fakerunner.Step{}), needsHuman)
	e := newEnv(t, withRunner(r), withClock(clk), withRetain(time.Hour), withWorkers(1))
	e.Start()

	running := e.createFromLabel("widgets", 1, "af:fix")
	waitCalls(t, r, 1)
	if s := e.standing(running.ID); s.State != jobs.StateRunning || s.Source != (jobs.Source{Kind: "label", By: "poll"}) || s.RequestLabel != "af:fix" ||
		s.CancelledInRunner || !s.RunnerInProgress {
		t.Errorf("a running job: %+v, want running, from the label source, af:fix, not cancelled and with its runner in progress", s)
	}

	queued := e.createFromLabel("widgets", 2, "af:fix") // behind the running one
	if s := e.standing(queued.ID); s.State != jobs.StateQueued || s.CancelledInRunner || s.RunnerInProgress {
		t.Errorf("a queued job: %+v, want queued with neither fact", s)
	}
	if resp := e.cancel(queued.ID); resp.Status != 200 {
		t.Fatalf("cancel the queued job: %d %s", resp.Status, resp.Body)
	}
	if s := e.standing(queued.ID); s.State != jobs.StateCancelled || s.CancelledInRunner || s.RunnerInProgress {
		t.Errorf("a job cancelled while queued: %+v, want cancelled with no runner to have reached", s)
	}

	if resp := e.cancel(running.ID); resp.Status != 200 {
		t.Fatalf("cancel the running job: %d %s", resp.Status, resp.Body)
	}
	if s := e.standing(running.ID); s.State != jobs.StateCancelled || !s.CancelledInRunner || !s.RunnerInProgress {
		t.Errorf("a job cancelled while its runner holds the attempt: %+v, want the cancel to have reached the runner, which is still in progress", s)
	}
	// The runner overruns the grace: its attempt is recorded cancelled without it, and it is in progress all the same.
	clk.Advance(61 * time.Second)
	waitFor(t, "the attempt to be recorded cancelled", func() bool { return e.get(running.ID).Attempts[0].State == jobs.AttemptCancelled })
	if s := e.standing(running.ID); !s.CancelledInRunner || !s.RunnerInProgress {
		t.Errorf("after the grace: %+v, want the fact kept and the runner still in progress", s)
	}
	r.Release(0)
	waitFor(t, "the runner to return", func() bool { return !e.standing(running.ID).RunnerInProgress })
	if s := e.standing(running.ID); s.State != jobs.StateCancelled || !s.CancelledInRunner || s.RunnerInProgress {
		t.Errorf("after the runner returned: %+v, want the fact kept and no runner in progress", s)
	}

	blocked := e.createFromLabel("widgets", 3, "af:fix")
	e.waitState(blocked.ID, jobs.StateBlocked)
	if resp := e.cancel(blocked.ID); resp.Status != 200 {
		t.Fatalf("cancel the blocked job: %d %s", resp.Status, resp.Body)
	}
	if s := e.standing(blocked.ID); s.State != jobs.StateCancelled || s.CancelledInRunner || s.RunnerInProgress {
		t.Errorf("a job cancelled while blocked: %+v, want cancelled with neither fact", s)
	}

	// Retention purges all three; the read says so with the error that a job that is not there gives.
	clk.Advance(2 * time.Hour)
	waitFor(t, "the purge", func() bool {
		_, err := e.Svc.Standing(ctx, running.ID)
		return errors.Is(err, jobs.ErrNotFound)
	})
	for _, id := range []string{queued.ID, blocked.ID, "job_00000000000000000000"} {
		waitFor(t, "the purge of "+id, func() bool {
			_, err := e.Svc.Standing(ctx, id)
			return errors.Is(err, jobs.ErrNotFound)
		})
	}
}

// TestAJobWhoseEarlierAttemptRanAndWhoseNextWaitsForItsTurnIsNotCancelledInARunner is the case that the fact of a
// cancel must not take for one: an attempt that ran and ended in the past, and a next attempt that is queued behind a
// job of the workspace when the cancel comes.
// Verifies: 15-REQ-9.2
func TestAJobWhoseEarlierAttemptRanAndWhoseNextWaitsForItsTurnIsNotCancelledInARunner(t *testing.T) {
	r := fakerunner.New(fakerunner.Failed("brain", true), fakerunner.BlockIgnoringContext(fakerunner.Step{}))
	e := newEnv(t, withRunner(r), withWorkers(1))
	first := e.createFromLabel("widgets", 5, "af:fix")
	second := e.createFromLabel("widgets", 6, "af:fix")
	e.Start()
	// The first job's attempt fails in a way that makes another, which queues behind the second job's.
	waitCalls(t, r, 2)
	waitFor(t, "the second attempt of the first job to be queued", func() bool { return len(e.get(first.ID).Attempts) == 2 })
	if resp := e.cancel(first.ID); resp.Status != 200 {
		t.Fatalf("cancel: %d %s", resp.Status, resp.Body)
	}
	if s := e.standing(first.ID); s.State != jobs.StateCancelled || s.CancelledInRunner || s.RunnerInProgress {
		t.Errorf("a job whose first attempt ran and whose second was queued: %+v, want cancelled with no runner reached", s)
	}
	if s := e.standing(second.ID); s.State != jobs.StateRunning || s.CancelledInRunner || !s.RunnerInProgress {
		t.Errorf("the running job: %+v", s)
	}
}

// TestAJobCancelledByAnUnsubscribeWhileItsAttemptRunsIsCancelledInARunner is the other cause of a cancel that reaches a
// runner: the unsubscribe of the workspace cancels its jobs with the cause of the unsubscribe, and the worker's cleanup
// puts the label back for it as it does for a person's cancel.
// Verifies: 15-REQ-9.2
func TestAJobCancelledByAnUnsubscribeWhileItsAttemptRunsIsCancelledInARunner(t *testing.T) {
	r := fakerunner.New(fakerunner.Block(fakerunner.Step{}))
	e := newEnv(t, withRunner(r))
	e.addWorkspace("gadgets")
	e.Start()
	job := e.createFromLabel("gadgets", 7, "af:fix")
	waitCalls(t, r, 1)

	if err := e.Svc.Unsubscribe(ctx, "gadgets", func() error { return nil }); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "the runner to return", func() bool { return !e.standing(job.ID).RunnerInProgress })
	if s := e.standing(job.ID); s.State != jobs.StateCancelled || !s.CancelledInRunner {
		t.Errorf("a job cancelled by the unsubscribe: %+v, want cancelled, and the cancel to have reached the runner", s)
	}
}

// TestAJobCancelledAfterItsAttemptWasClaimedAndBeforeItsRunnerWasCalledIsNotCancelledInARunner is the window that the fact
// of a cancel must not take for a runner: the attempt is running in the file, the layer has it in flight, and the job is
// cancelled before the runner is called. The runner is never called, so nothing put the request label back, and the poll
// must not hold a label that nothing restored.
// Verifies: 15-REQ-9.2
func TestAJobCancelledAfterItsAttemptWasClaimedAndBeforeItsRunnerWasCalledIsNotCancelledInARunner(t *testing.T) {
	var e *env
	var once sync.Once
	var id string
	r := fakerunner.New()
	e = newEnv(t, withRunner(r), func(c *jobs.Config) {
		c.StoreFault = func(op string) error {
			if op == jobs.OpInvoke {
				once.Do(func() {
					if _, err := e.Svc.Cancel(ctx, id); err != nil {
						t.Errorf("cancel: %v", err)
					}
				})
			}
			return nil
		}
	})
	job := e.createFromLabel("widgets", 42, "af:fix")
	id = job.ID

	e.Start()
	e.waitRest(id)

	if len(r.Calls()) != 0 {
		t.Fatalf("the runner was called %d times, want none: the job was cancelled before it", len(r.Calls()))
	}
	if s := e.standing(id); s.State != jobs.StateCancelled || s.CancelledInRunner || s.RunnerInProgress {
		t.Errorf("a job cancelled before its runner was called: %+v, want cancelled, and the cancel not to have reached a runner", s)
	}
}
