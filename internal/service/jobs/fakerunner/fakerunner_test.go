package fakerunner_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func request(job string, attempt, delivery int) jobs.Request {
	return jobs.Request{
		JobID: job, Attempt: attempt, Delivery: delivery, Workspace: "widgets", Kind: jobs.KindFix,
		Input: jobs.Input{Issue: json.RawMessage("42")}, Context: []string{"a note"},
		Answers: []jobs.Answer{{Question: "which?", Text: "that one"}},
		Source:  jobs.Source{Kind: "api", By: "user-1"}, RequestLabel: "af:fix", Dir: "/tmp/attempts/" + job,
	}
}

// outcome is what a call of the runner came to.
type outcome struct {
	res      jobs.Result
	err      error
	panicked any
}

// runAsync makes a call in a goroutine and reports what it came to.
func runAsync(r *fakerunner.Runner, ctx context.Context, req jobs.Request) <-chan outcome {
	out := make(chan outcome, 1)
	go func() {
		var o outcome
		defer func() {
			o.panicked = recover()
			out <- o
		}()
		o.res, o.err = r.Run(ctx, req)
	}()
	return out
}

func get(t *testing.T, ch <-chan outcome) outcome {
	t.Helper()
	select {
	case o := <-ch:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("the call did not return within 5s")
		return outcome{}
	}
}

func stillRunning(t *testing.T, ch <-chan outcome) {
	t.Helper()
	select {
	case o := <-ch:
		t.Fatalf("the call returned %+v, want it to block", o)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestTheFakeRunnerDoesWhatEachCallIsScriptedToAndRecordsTheInputs_TS_12_35 verifies
// TS-12-35: a script of five steps (an envelope, a plain error, a transient failure,
// a panic, and a block until the context is cancelled or the call is released) is
// followed by five calls, every call's inputs are recorded in order, and the runner
// needs no network, hub, key or container engine.
// Verifies: 12-REQ-5.7
func TestTheFakeRunnerDoesWhatEachCallIsScriptedToAndRecordsTheInputs_TS_12_35(t *testing.T) {
	boom := errors.New("boom")
	failure := &toolio.Failure{Category: toolio.CategorySandbox, Message: "no box", Retryable: true}
	r := fakerunner.New(
		fakerunner.Done(fakerunner.WithSummary("all good"), fakerunner.WithTool("fix")),
		fakerunner.Fail(boom),
		fakerunner.TransientFailure(failure),
		fakerunner.Panic("bang"),
		fakerunner.Block(fakerunner.Step{}),
	)
	var jobs_ jobs.Runner = r // it is a runner

	// 1. an envelope
	res, err := jobs_.Run(context.Background(), request("job_1", 1, 1))
	var env toolio.Envelope
	if err != nil || json.Unmarshal(res.Envelope, &env) != nil || env.Status != toolio.StatusDone || env.Summary != "all good" || env.Tool != "fix" {
		t.Fatalf("call 1 = %s, %v, want a done envelope", res.Envelope, err)
	}
	if res.Envelope[len(res.Envelope)-1] != '\n' || res.Envelope[0] != '{' || string(res.Envelope[:3]) == "{\"s" {
		t.Errorf("the envelope is %.40q, want indented JSON as a command writes it", res.Envelope)
	}

	// 2. a plain error
	if _, err := jobs_.Run(context.Background(), request("job_1", 2, 1)); err != boom || jobs.IsTransient(err) {
		t.Errorf("call 2 returned %v, want the error given, not transient", err)
	}

	// 3. a transient failure
	_, err = jobs_.Run(context.Background(), request("job_1", 3, 1))
	var got *toolio.Failure
	if !jobs.IsTransient(err) || !errors.As(err, &got) || got != failure {
		t.Errorf("call 3 returned %v, want the failure, marked transient", err)
	}

	// 4. a panic
	o := get(t, runAsync(r, context.Background(), request("job_1", 4, 1)))
	if o.panicked != "bang" {
		t.Errorf("call 4 panicked with %v, want bang", o.panicked)
	}

	// 5. a block, ended by the cancellation of its context
	ctx, cancel := context.WithCancelCause(context.Background())
	blocked := runAsync(r, ctx, request("job_2", 1, 2))
	if !r.Wait(5, 5*time.Second) {
		t.Fatal("the fifth call did not begin")
	}
	stillRunning(t, blocked)
	if r.Active() != 1 {
		t.Errorf("Active = %d, want the blocked call", r.Active())
	}
	if calls := r.Calls(); calls[4].Cause() != nil {
		t.Errorf("the cause of a context that is not cancelled is %v", calls[4].Cause())
	}
	cancel(jobs.ErrCancelled)
	o = get(t, blocked)
	if !errors.Is(o.err, jobs.ErrCancelled) || o.panicked != nil {
		t.Errorf("call 5 returned %v, want the cause of the cancellation", o.err)
	}
	if !jobs.IsCancel(r.Calls()[4].Cause()) {
		t.Errorf("the cause recorded is %v, want a cancel", r.Calls()[4].Cause())
	}

	// The inputs of all five calls, in order.
	calls := r.Calls()
	if len(calls) != 5 {
		t.Fatalf("%d calls recorded, want 5", len(calls))
	}
	for i, c := range calls {
		if c.Index != i || c.Workspace != "widgets" || c.Kind != jobs.KindFix || string(c.Input.Issue) != "42" || len(c.Context) != 1 ||
			len(c.Answers) != 1 || c.Source.By != "user-1" || c.RequestLabel != "af:fix" || c.Started.IsZero() {
			t.Errorf("call %d recorded as %+v", i, c)
		}
	}
	if calls[0].JobID != "job_1" || calls[0].Attempt != 1 || calls[3].Attempt != 4 || calls[4].JobID != "job_2" || calls[4].Delivery != 2 {
		t.Errorf("the calls recorded are %v %d, %v %d, %v %d/%d", calls[0].JobID, calls[0].Attempt, calls[3].JobID, calls[3].Attempt, calls[4].JobID, calls[4].Attempt, calls[4].Delivery)
	}

	// After the script, a call returns a done envelope.
	res, err = r.Run(context.Background(), request("job_3", 1, 1))
	if err != nil || json.Unmarshal(res.Envelope, &env) != nil || env.Status != toolio.StatusDone {
		t.Errorf("a call after the script returned %s, %v, want a done envelope", res.Envelope, err)
	}
}

// TestABlockedCallEndsWhenItIsReleasedAndMayIgnoreItsContext verifies the rest of what
// a test can script: a block that is released returns what it was given to do; a
// block that ignores its context stays until it is released; Do runs a function; the
// peak of concurrent calls is counted; and the steps after the script are the ones
// that Then sets.
// Verifies: 12-REQ-5.7
func TestABlockedCallEndsWhenItIsReleasedAndMayIgnoreItsContext(t *testing.T) {
	r := fakerunner.New(
		fakerunner.Block(fakerunner.Failed("brain", true)),
		fakerunner.BlockIgnoringContext(fakerunner.Done()),
		fakerunner.Do(func(ctx context.Context, req jobs.Request) (jobs.Result, error) {
			return jobs.Result{Report: "report of " + req.JobID, MaxAttempts: 3}, nil
		}),
	).Then(fakerunner.Fail(errors.New("later")))

	// The calls are made one after the other, so that the steps fall to them in order.
	first := runAsync(r, context.Background(), request("job_1", 1, 1))
	if !r.Wait(1, 5*time.Second) {
		t.Fatal("the first call did not begin")
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	second := runAsync(r, ctx, request("job_2", 1, 1))
	if !r.Wait(2, 5*time.Second) {
		t.Fatal("two calls did not begin")
	}
	if r.MaxConcurrent() != 2 || r.Active() != 2 {
		t.Errorf("MaxConcurrent %d, Active %d, want 2 and 2", r.MaxConcurrent(), r.Active())
	}

	cancel(jobs.ErrShutdown)
	stillRunning(t, second) // it ignores its context
	if jobs.IsCancel(r.Calls()[1].Cause()) || !errors.Is(r.Calls()[1].Cause(), jobs.ErrShutdown) {
		t.Errorf("cause = %v, want the shutdown", r.Calls()[1].Cause())
	}

	r.Release(0)
	o := get(t, first)
	var env toolio.Envelope
	if o.err != nil || json.Unmarshal(o.res.Envelope, &env) != nil || env.Status != toolio.StatusFailed || env.Error == nil || env.Error.Category != "brain" || !env.Error.Retryable {
		t.Errorf("the released call returned %s, %v, want the failed envelope it was given", o.res.Envelope, o.err)
	}
	stillRunning(t, second)
	r.Release(1)
	if o := get(t, second); o.err != nil || json.Unmarshal(o.res.Envelope, &env) != nil || env.Status != toolio.StatusDone {
		t.Errorf("the second released call returned %s, %v", o.res.Envelope, o.err)
	}

	res, err := r.Run(context.Background(), request("job_3", 1, 1))
	if err != nil || res.Report != "report of job_3" || res.MaxAttempts != 3 {
		t.Errorf("Do returned %+v, %v", res, err)
	}
	if _, err := r.Run(context.Background(), request("job_4", 1, 1)); err == nil || err.Error() != "later" {
		t.Errorf("a call after the script returned %v, want the step that Then set", err)
	}
	if r.Active() != 0 {
		t.Errorf("Active = %d after every call returned", r.Active())
	}

	// ReleaseAll lets every blocked call go, those that come later included.
	all := fakerunner.New(fakerunner.Block(fakerunner.Step{}), fakerunner.Block(fakerunner.Step{}))
	a := runAsync(all, context.Background(), request("job_a", 1, 1))
	all.Wait(1, 5*time.Second)
	all.ReleaseAll()
	get(t, a)
	b := runAsync(all, context.Background(), request("job_b", 1, 1))
	if o := get(t, b); o.err != nil {
		t.Errorf("a call after ReleaseAll returned %v, want it released at once", o.err)
	}
}
