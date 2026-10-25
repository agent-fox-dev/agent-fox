package jobs_test

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestEnvelopeAndReport_AreTheBytesTheAttemptStored(t *testing.T) {
	// Leading white space and a field of its own: no encoder writes these bytes again.
	asked := append([]byte("  "), bytes.Replace(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which? — ünï", "one", "two")),
		[]byte(`"status": "needs_human",`), []byte(`"x_extension": {"z": 1},   "status": "needs_human",`), 1)...)
	done := fakerunner.Env(toolio.StatusDone, fakerunner.WithSummary("fixed it"))
	e := newEnv(t, withRunner(fakerunner.New(
		fakerunner.Return(jobs.Result{Envelope: asked, Report: "# Question\n"}),
		fakerunner.Return(jobs.Result{Envelope: done, Report: "# Done\n\n- fixed *it*\n\tand a tab\n"}),
	)))
	job := e.create("widgets", 1, "")
	e.start()
	e.waitState(job.ID, jobs.StateBlocked)
	if _, err := e.Svc.Answer(ctx, job.ID, "u", jobs.AnswerRequest{Option: ptr("B")}); err != nil {
		t.Fatal(err)
	}
	e.waitState(job.ID, jobs.StateDone)
	for _, tc := range []struct {
		n    int
		read func(string, int) ([]byte, error)
		want string
	}{
		{1, func(id string, n int) ([]byte, error) { return e.Svc.Envelope(ctx, id, n) }, string(asked)},
		{2, func(id string, n int) ([]byte, error) { return e.Svc.Envelope(ctx, id, n) }, string(done)},
		{1, func(id string, n int) ([]byte, error) { return e.Svc.Report(ctx, id, n) }, "# Question\n"},
		{2, func(id string, n int) ([]byte, error) { return e.Svc.Report(ctx, id, n) }, "# Done\n\n- fixed *it*\n\tand a tab\n"},
	} {
		if got, err := tc.read(job.ID, tc.n); err != nil || string(got) != tc.want {
			t.Errorf("attempt %d: %.40q, %v, want %.40q", tc.n, got, err, tc.want)
		}
	}
}

func TestEnvelopeAndReport_AreNotFoundUnlessTheRowsListThemAndTheyAreOnDisk(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Return(jobs.Result{Envelope: fakerunner.Env(toolio.StatusDone), Report: "# Done\n"}), fakerunner.Block(fakerunner.Step{}))))
	finished := e.create("widgets", 1, "")
	e.start()
	e.waitState(finished.ID, jobs.StateDone)
	running := e.create("widgets", 2, "")
	e.waitCalls(2)
	// What a runner that has begun to write its files would leave: the rows do not list them.
	testutil.WriteFile(t, e.Dir, running.ID+"/1/envelope.json", "{}")
	testutil.WriteFile(t, e.Dir, running.ID+"/1/report.md", "# Early\n")
	for name, key := range map[string]struct {
		id string
		n  int
	}{"no job": {"job_00000000000000000000", 1}, "attempt 0": {finished.ID, 0}, "attempt 9": {finished.ID, 9}, "a running attempt": {running.ID, 1}} {
		if _, err := e.Svc.Envelope(ctx, key.id, key.n); !errors.Is(err, jobs.ErrNotFound) {
			t.Errorf("the envelope of %s: %v, want ErrNotFound", name, err)
		}
		if _, err := e.Svc.Report(ctx, key.id, key.n); !errors.Is(err, jobs.ErrNotFound) {
			t.Errorf("the report of %s: %v, want ErrNotFound", name, err)
		}
	}
	// A file the rows list that is not on disk is not found either, and the log says so.
	if err := os.Remove(e.file(finished.ID, 1, "report.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Svc.Report(ctx, finished.ID, 1); !errors.Is(err, jobs.ErrNotFound) || len(e.Log.Find("WARN", "not on disk")) != 1 {
		t.Errorf("a report gone from disk: %v, warnings %v", err, e.Log.Records())
	}
	if got, err := e.Svc.Envelope(ctx, finished.ID, 1); err != nil || len(got) == 0 {
		t.Errorf("the envelope of the finished attempt: %v", err)
	}
}

func TestStanding_SaysWhetherTheCancelReachedARunnerAndWhetherARunnerIsInProgress(t *testing.T) {
	r := fakerunner.New(fakerunner.BlockIgnoringContext(fakerunner.Step{}), question())
	e := newEnv(t, withRunner(r), withRetain(time.Hour), withWorkers(1))
	e.start()
	running := e.create("widgets", 1, "af:fix")
	e.waitCalls(1)
	standing := func(id string) jobs.Standing {
		t.Helper()
		st, err := e.Svc.Standing(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	if s := standing(running.ID); s.State != jobs.StateRunning || s.Source != (jobs.Source{Kind: "label", By: "poll"}) || s.RequestLabel != "af:fix" || s.CancelledInRunner || !s.RunnerInProgress || s.CancelCause != "" {
		t.Errorf("a running job: %+v", s)
	}
	queued := e.create("widgets", 2, "af:fix")
	if _, err := e.Svc.Cancel(ctx, queued.ID); err != nil {
		t.Fatal(err)
	}
	if s := standing(queued.ID); s.State != jobs.StateCancelled || s.CancelledInRunner || s.RunnerInProgress || s.CancelCause != "cancelled" {
		t.Errorf("a job cancelled while queued: %+v", s)
	}
	if _, err := e.Svc.Cancel(ctx, running.ID); err != nil {
		t.Fatal(err)
	}
	if s := standing(running.ID); !s.CancelledInRunner || !s.RunnerInProgress {
		t.Errorf("a job cancelled while its runner holds it: %+v", s)
	}
	e.waitWaits(1)
	e.Clock.Advance(cancelGrace + time.Second)
	testutil.Eventually(t, "the overrun", func() bool { return e.get(running.ID).Attempts[0].State == jobs.AttemptCancelled })
	if s := standing(running.ID); !s.CancelledInRunner || !s.RunnerInProgress {
		t.Errorf("after the grace: %+v, want the cancel in a runner whose runner is still in progress", s)
	}
	r.Release(0)
	testutil.Eventually(t, "the runner to return", func() bool { return !standing(running.ID).RunnerInProgress })
	if s := standing(running.ID); !s.CancelledInRunner {
		t.Errorf("after the runner returned: %+v, want the cancel still known to have reached it", s)
	}
	blocked := e.waitState(e.create("widgets", 3, "af:fix").ID, jobs.StateBlocked)
	if _, err := e.Svc.Cancel(ctx, blocked.ID); err != nil {
		t.Fatal(err)
	}
	if s := standing(blocked.ID); s.CancelledInRunner || s.RunnerInProgress {
		t.Errorf("a job cancelled while blocked: %+v", s)
	}
	e.Clock.Advance(2 * time.Hour)
	for _, id := range []string{running.ID, queued.ID, blocked.ID, "job_00000000000000000000"} {
		testutil.Eventually(t, "the purge of "+id, func() bool { _, err := e.Svc.Standing(ctx, id); return errors.Is(err, jobs.ErrNotFound) })
	}
}

func TestStanding_AnEarlierAttemptThatRanIsNotACancelInARunner(t *testing.T) {
	r := fakerunner.New(failedBrain("brain", true, 0), fakerunner.Block(fakerunner.Step{}))
	e := newEnv(t, withRunner(r), withWorkers(1))
	first, second := e.create("widgets", 5, "af:fix"), e.create("widgets", 6, "af:fix")
	e.start()
	// The first job's attempt fails in a way that makes another, which queues behind the second job's.
	e.waitCalls(2)
	testutil.Eventually(t, "the automatic attempt", func() bool { return len(e.get(first.ID).Attempts) == 2 })
	if _, err := e.Svc.Cancel(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if s, _ := e.Svc.Standing(ctx, first.ID); s.CancelledInRunner || s.RunnerInProgress {
		t.Errorf("a job whose first attempt ran and whose second waited: %+v", s)
	}
	if s, _ := e.Svc.Standing(ctx, second.ID); s.State != jobs.StateRunning || !s.RunnerInProgress {
		t.Errorf("the running job: %+v", s)
	}
}

func TestKnownRunningAndWorkspaceOf_AnswerTheReconciliation(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Done(), fakerunner.Block(fakerunner.Done()))))
	e.start()
	ended := e.waitState(e.create("widgets", 1, "").ID, jobs.StateDone)
	running := e.create("gadgets", 2, "")
	e.waitCalls(2)
	if known, err := e.Svc.Known(ctx, ended.ID); err != nil || !known {
		t.Errorf("Known(%s) = %v, %v", ended.ID, known, err)
	}
	if known, err := e.Svc.Known(ctx, "job_00000000000000000000"); err != nil || known {
		t.Errorf("Known of no job = %v, %v, want false", known, err)
	}
	testutil.Eventually(t, "the attempt in the runner", func() bool { return e.Svc.Running(running.ID, 1) })
	if e.Svc.Running(running.ID, 2) || e.Svc.Running(ended.ID, 1) {
		t.Error("an attempt that is not in a runner is said to be")
	}
	if ws, err := e.Svc.WorkspaceOf(ctx, running.ID); err != nil || ws != "gadgets" {
		t.Errorf("WorkspaceOf = %s, %v", ws, err)
	}
	if _, err := e.Svc.WorkspaceOf(ctx, "job_00000000000000000000"); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("WorkspaceOf no job: %v", err)
	}
}
