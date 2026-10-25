package fakerunner_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The fake runner is a runner of the jobs layer.
var _ jobs.Runner = (*fakerunner.Runner)(nil)

var ctx = context.Background()

func request(job string) jobs.Request {
	return jobs.Request{
		JobID: job, Attempt: 2, Delivery: 1, Workspace: "widgets", Kind: jobs.KindFix,
		Input: jobs.Input{Issue: json.RawMessage("42")}, Source: jobs.Source{Kind: "api", By: "user-1"}, RequestLabel: "af:fix",
	}
}

// outcome is what a call of the runner came to.
type outcome struct {
	res      jobs.Result
	err      error
	panicked any
}

// runAsync makes a call in a goroutine and reports what it came to.
func runAsync(r *fakerunner.Runner, ctx context.Context, job string) <-chan outcome {
	out := make(chan outcome, 1)
	go func() {
		var o outcome
		defer func() { o.panicked = recover(); out <- o }()
		o.res, o.err = r.Run(ctx, request(job))
	}()
	return out
}

func get(t *testing.T, ch <-chan outcome) outcome {
	t.Helper()
	select {
	case o := <-ch:
		return o
	case <-time.After(testutil.WaitTimeout):
		t.Fatal("the call did not return")
		return outcome{}
	}
}

// stillBlocked fails the test when the call returns within a short while; it can only miss a call that returns late.
func stillBlocked(t *testing.T, ch <-chan outcome) {
	t.Helper()
	select {
	case o := <-ch:
		t.Fatalf("the call returned %+v, want it blocked", o)
	case <-time.After(20 * time.Millisecond):
	}
}

// begun waits for the runner to have had n calls.
func begun(t *testing.T, r *fakerunner.Runner, n int) {
	t.Helper()
	if !r.Wait(n, testutil.WaitTimeout) {
		t.Fatalf("%d calls did not begin", n)
	}
}

func TestRun_FollowsTheScriptThenTheFallback(t *testing.T) {
	boom := errors.New("boom")
	failure := &toolio.Failure{Category: toolio.CategorySandbox, Message: "no box", Retryable: true}
	r := fakerunner.New(
		fakerunner.Fail(boom),
		fakerunner.TransientFailure(failure),
		fakerunner.Return(jobs.Result{Report: "a report", MaxAttempts: 3}),
		fakerunner.Do(func(_ context.Context, req jobs.Request) (jobs.Result, error) {
			return jobs.Result{Report: "of " + req.JobID}, nil
		}),
	)
	if _, err := r.Run(ctx, request("job_1")); err != boom || jobs.IsTransient(err) {
		t.Errorf("Fail returned %v, want the error given, not transient", err)
	}
	_, err := r.Run(ctx, request("job_1"))
	if got, ok := errors.AsType[*toolio.Failure](err); !jobs.IsTransient(err) || !ok || got != failure {
		t.Errorf("TransientFailure returned %v, want the failure, marked transient", err)
	}
	if res, err := r.Run(ctx, request("job_1")); err != nil || res.Report != "a report" || res.MaxAttempts != 3 {
		t.Errorf("Return returned %+v, %v", res, err)
	}
	if res, err := r.Run(ctx, request("job_2")); err != nil || res.Report != "of job_2" {
		t.Errorf("Do returned %+v, %v", res, err)
	}
	res, err := r.Run(ctx, request("job_3"))
	if env := testutil.Decode[toolio.Envelope](t, res.Envelope); err != nil || env.Status != toolio.StatusDone {
		t.Errorf("a call after the script returned %s, %v, want a done envelope", res.Envelope, err)
	}
	r.Then(fakerunner.Fail(boom))
	if _, err := r.Run(ctx, request("job_4")); err != boom {
		t.Errorf("a call after Then returned %v, want the step Then set", err)
	}
}

func TestRun_RecordsEveryCallInOrder(t *testing.T) {
	r := fakerunner.New()
	for _, job := range []string{"job_1", "job_2"} {
		if _, err := r.Run(ctx, request(job)); err != nil {
			t.Fatal(err)
		}
	}
	calls := r.Calls()
	if len(calls) != 2 {
		t.Fatalf("%d calls recorded, want 2", len(calls))
	}
	for i, c := range calls {
		if want := request(c.JobID); c.Index != i || c.Attempt != want.Attempt || c.Workspace != "widgets" || string(c.Input.Issue) != "42" ||
			c.Source != want.Source || c.RequestLabel != "af:fix" || c.Started.IsZero() {
			t.Errorf("call %d recorded as %+v", i, c)
		}
	}
	if calls[0].JobID != "job_1" || calls[1].JobID != "job_2" {
		t.Errorf("the calls are of %s and %s", calls[0].JobID, calls[1].JobID)
	}
}

func TestPanic_PanicsWithTheValue(t *testing.T) {
	if o := get(t, runAsync(fakerunner.New(fakerunner.Panic("bang")), ctx, "job_1")); o.panicked != "bang" {
		t.Errorf("the call panicked with %v, want bang", o.panicked)
	}
}

func TestBlock_EndsWithTheCauseOfTheCancellation(t *testing.T) {
	r := fakerunner.New(fakerunner.Block(fakerunner.Step{}))
	cctx, cancel := context.WithCancelCause(ctx)
	call := runAsync(r, cctx, "job_1")
	begun(t, r, 1)
	if r.Active() != 1 || r.Calls()[0].Cause() != nil {
		t.Errorf("Active %d, cause %v; want the call blocked with its context live", r.Active(), r.Calls()[0].Cause())
	}
	cancel(jobs.ErrCancelled)
	if o := get(t, call); !errors.Is(o.err, jobs.ErrCancelled) {
		t.Errorf("the call returned %v, want the cause of the cancellation", o.err)
	}
	if !jobs.IsCancel(r.Calls()[0].Cause()) {
		t.Errorf("the cause recorded is %v", r.Calls()[0].Cause())
	}
}

func TestBlock_ReleasedCallDoesItsThenStep(t *testing.T) {
	r := fakerunner.New(fakerunner.Block(fakerunner.Failed("brain", false)), fakerunner.Block(fakerunner.Step{}))
	first := runAsync(r, ctx, "job_1")
	begun(t, r, 1)
	second := runAsync(r, ctx, "job_2")
	begun(t, r, 2)
	if r.MaxConcurrent() != 2 {
		t.Errorf("MaxConcurrent = %d, want 2", r.MaxConcurrent())
	}
	r.Release(0)
	r.Release(0) // a second release is a no-op
	o := get(t, first)
	if env := testutil.Decode[toolio.Envelope](t, o.res.Envelope); o.err != nil || env.Status != toolio.StatusFailed {
		t.Errorf("the released call returned %s, %v, want its then step", o.res.Envelope, o.err)
	}
	stillBlocked(t, second)
	r.Release(1)
	if o := get(t, second); o.err != nil || testutil.Decode[toolio.Envelope](t, o.res.Envelope).Status != toolio.StatusDone {
		t.Errorf("a released block with no then step returned %s, %v, want a done envelope", o.res.Envelope, o.err)
	}
	if r.Active() != 0 {
		t.Errorf("Active = %d after every call returned", r.Active())
	}
}

func TestBlockIgnoringContext_WaitsForTheRelease(t *testing.T) {
	r := fakerunner.New(fakerunner.BlockIgnoringContext(fakerunner.Done()))
	cctx, cancel := context.WithCancelCause(ctx)
	call := runAsync(r, cctx, "job_1")
	begun(t, r, 1)
	cancel(jobs.ErrShutdown)
	stillBlocked(t, call)
	if !errors.Is(r.Calls()[0].Cause(), jobs.ErrShutdown) {
		t.Errorf("cause = %v, want the shutdown", r.Calls()[0].Cause())
	}
	r.Release(0)
	if o := get(t, call); o.err != nil {
		t.Errorf("the released call returned %v", o.err)
	}
}

func TestReleaseAll_ReleasesTheBlockedCallsAndTheLaterOnes(t *testing.T) {
	r := fakerunner.New(fakerunner.Block(fakerunner.Step{}), fakerunner.Block(fakerunner.Step{}))
	first := runAsync(r, ctx, "job_1")
	begun(t, r, 1)
	r.ReleaseAll()
	get(t, first)
	if o := get(t, runAsync(r, ctx, "job_2")); o.err != nil {
		t.Errorf("a call after ReleaseAll returned %v, want it released at once", o.err)
	}
}

func TestWait_SaysWhetherTheCallsBegan(t *testing.T) {
	if fakerunner.New().Wait(1, time.Millisecond) {
		t.Error("Wait said a call began where none did")
	}
}
