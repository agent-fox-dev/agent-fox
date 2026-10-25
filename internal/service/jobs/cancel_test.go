package jobs_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// cancelGrace is how long the runner of a cancelled attempt has to return.
const cancelGrace = 60 * time.Second

func TestCancel_AnOpenJobIsCancelledAtOnceAndWhatWaitsNeverReachesTheRunner(t *testing.T) {
	r := fakerunner.New(fakerunner.Block(fakerunner.Done()), fakerunner.TransientFailure(sandboxFailure), question()).Then(fakerunner.Done())
	e := newEnv(t, withRunner(r), withWorkers(3))
	e.Subs.set("sprockets", "ok")
	e.start()
	running := e.create("widgets", 1, "")
	e.waitCalls(1)
	queued := e.create("widgets", 2, "af:fix") // behind the running job
	backoff := e.create("gadgets", 3, "")
	e.waitWaits(1)
	blocked := e.waitState(e.create("sprockets", 4, "af:fix").ID, jobs.StateBlocked)
	// The running job is cancelled last: once its runner returns, the job behind it would start.
	for _, tc := range []struct {
		doc     jobs.Doc
		restore bool
	}{{queued, true}, {backoff, false}, {blocked, true}, {running, false}} {
		doc, err := e.Svc.Cancel(ctx, tc.doc.ID)
		if err != nil || doc.State != jobs.StateCancelled || doc.FinishedAt == nil || *doc.CancelCause != "cancelled" {
			t.Fatalf("cancel of %s: %+v, %v, want it cancelled now, by a person", tc.doc.ID, doc, err)
		}
		if got := doc.Labels != nil && doc.Labels.Restore != nil && *doc.Labels.Restore == "af:fix"; got != tc.restore || !tc.restore && doc.Labels != nil {
			t.Errorf("cancel of %s: labels %+v, want the request label to be put back exactly when there is one", tc.doc.ID, doc.Labels)
		}
	}
	if cause := r.Calls()[0].Cause(); !jobs.IsCancel(cause) || !errors.Is(cause, jobs.ErrCancelled) {
		t.Errorf("the runner was stopped with %v, want the cancel", cause)
	}
	if a := e.waitRest(running.ID).Attempts[0]; a.State != jobs.AttemptCancelled || a.FinishedAt == nil {
		t.Errorf("the running attempt is %s once its runner returned, want cancelled", a.State)
	}
	for _, id := range []string{queued.ID, backoff.ID} {
		if a := e.get(id).Attempts[0]; a.State != jobs.AttemptCancelled || a.FinishedAt == nil {
			t.Errorf("%s: attempt %s, want cancelled", id, a.State)
		}
		if got := e.states(id); got[len(got)-1] != "cancelled:true" {
			t.Errorf("%s: job_state events %v, want the cancel at rest last", id, got)
		}
	}
	// The handler that waited out the backoff let go of its workspace: the next job of gadgets runs.
	next := e.create("gadgets", 5, "")
	e.waitState(next.ID, jobs.StateDone)
	if n := len(r.Calls()); n != 4 {
		t.Errorf("%d calls, want the 3 before the cancels and the next job's", n)
	}
	var state *jobs.StateError
	if _, err := e.Svc.Cancel(ctx, running.ID); !errors.As(err, &state) || state.State != jobs.StateCancelled {
		t.Errorf("a second cancel: %v, want a *StateError for cancelled", err)
	}
	if _, err := e.Svc.Cancel(ctx, "job_00000000000000000000"); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("a cancel of no job: %v, want ErrNotFound", err)
	}
}

func TestCancel_WhatARunnerReturnsAfterTheCancelIsRecordedCancelled(t *testing.T) {
	cases := []struct {
		name    string
		then    fakerunner.Step
		labels  bool // the runner reports that it put the request label back
		message string
	}{
		{"the cause of the cancel", fakerunner.Step{}, false, "the job was cancelled"},
		{"a plain error", fakerunner.Fail(errors.New("stopped")), false, "the job was cancelled"},
		{"a failure of the forge", fakerunner.Fail(&toolio.Failure{Category: toolio.CategoryForge, Message: "hung up", Retryable: true}), false, "the job was cancelled"},
		{"a transient failure", fakerunner.TransientFailure(sandboxFailure), false, "the job was cancelled"},
		{"no envelope", fakerunner.Return(jobs.Result{}), false, "the job was cancelled"},
		{"something that is not an envelope", fakerunner.Envelope([]byte("{")), false, "the job was cancelled"},
		{"its own envelope and labels", fakerunner.Return(jobs.Result{Envelope: fakerunner.Env(toolio.StatusFailed, fakerunner.WithError("cancelled", "stopped", false)),
			LabelsSet: []string{"af:fix"}, LabelsRemoved: []string{"af:in-progress"}}), true, "stopped"},
	}
	var steps []fakerunner.Step
	for _, tc := range cases {
		steps = append(steps, fakerunner.Block(tc.then))
	}
	e := newEnv(t, withRunner(fakerunner.New(steps...)), withWorkers(1))
	e.start()
	for i, tc := range cases {
		job := e.create("widgets", i+1, "af:fix")
		e.waitCalls(i + 1)
		if _, err := e.Svc.Cancel(ctx, job.ID); err != nil {
			t.Fatal(err)
		}
		d := e.waitRest(job.ID)
		a := d.Attempts[0]
		if d.State != jobs.StateCancelled || a.State != jobs.AttemptCancelled || a.Error.Category != "cancelled" || a.Error.Message != tc.message || *a.ExitCode != 1 || a.TransientFailures != 0 {
			t.Errorf("%s: job %s, attempt %+v %+v, want it cancelled with an envelope of the category cancelled", tc.name, d.State, a, a.Error)
		}
		if !strings.Contains(testutil.ReadFile(t, e.file(job.ID, 1, "envelope.json")), `"category": "cancelled"`) {
			t.Errorf("%s: the envelope.json is not one of a cancel", tc.name)
		}
		if restored := d.Labels.Restore == nil; restored != tc.labels {
			t.Errorf("%s: labels %+v, want restore cleared only when the runner set the request label", tc.name, d.Labels)
		}
		if got := e.states(job.ID); !slices.Equal(got, []string{"queued:false", "running:false", "cancelled:false", "cancelled:true"}) {
			t.Errorf("%s: job_state events %v, want the cancel not at rest while the runner runs, and at rest once it returned", tc.name, got)
		}
		if evs := e.events(job.ID); evs[len(evs)-2].Type != jobs.EventAttemptEnd {
			t.Errorf("%s: the event before the last is %s, want attempt_end", tc.name, evs[len(evs)-2].Type)
		}
	}
	if recs := e.Log.Find("WARN", ""); len(recs) != 0 {
		t.Errorf("warnings = %v, want none: every attempt ended at once", recs)
	}
}

func TestCancel_ARunnerThatOverrunsTheGraceIsRecordedCancelledAndKeepsItsSlotUntilItReturns(t *testing.T) {
	hold, returned := make(chan struct{}), make(chan struct{})
	var kept *jobs.Recorder
	r := fakerunner.New(fakerunner.Do(func(_ context.Context, req jobs.Request) (jobs.Result, error) {
		kept = req.Recorder
		<-hold // it does not look at its context
		req.Recorder.Emit("late", 1)
		req.Recorder.Hub("late-session", "late-run", "late-box")
		close(returned)
		panic("late bang") // what it does now counts for nothing, a panic included
	}))
	e := newEnv(t, withRunner(r), withWorkers(1))
	e.openAtEnd(hold)
	first := e.create("widgets", 1, "")
	e.start()
	e.waitCalls(1)
	if _, err := e.Svc.Cancel(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	e.waitWaits(1)
	if got := e.Clock.Waits(); got[0] != cancelGrace {
		t.Fatalf("waits = %v, want the grace of 60 s", got)
	}
	if a := e.get(first.ID).Attempts[0]; a.State != jobs.AttemptRunning {
		t.Fatalf("the attempt is %s before the grace is over, want running", a.State)
	}
	e.Clock.Advance(cancelGrace)
	doc := e.waitRest(first.ID)
	if a := doc.Attempts[0]; doc.State != jobs.StateCancelled || a.State != jobs.AttemptCancelled || *a.Status != toolio.StatusFailed ||
		a.Error.Category != "cancelled" || a.Error.Message != "the runner did not return within 1m0s of the cancel" {
		t.Fatalf("job %s, attempt %+v %+v, want it recorded cancelled without the runner", doc.State, a, a.Error)
	}
	if !strings.Contains(testutil.ReadFile(t, e.file(first.ID, 1, "envelope.json")), `"category": "cancelled"`) || e.lastState(first.ID) != "cancelled:true" {
		t.Error("the overrun is not recorded with a cancelled envelope and a job_state at rest")
	}
	testutil.Eventually(t, "the ERROR of the overrun", func() bool { return len(e.Log.Find("ERROR", "cancel grace")) == 1 })
	if logged := e.Log.Find("ERROR", "cancel grace"); logged[0]["job"] != first.ID || logged[0]["attempt"] != float64(1) {
		t.Errorf("ERROR records = %v, want one naming the job and the attempt", e.Log.Find("ERROR", ""))
	}
	// The runner keeps the only slot, for its workspace and the others: the next jobs run once it has returned.
	second, third := e.create("widgets", 2, ""), e.create("gadgets", 3, "")
	before := e.snapshot(first.ID)
	close(hold)
	<-returned
	e.waitState(second.ID, jobs.StateDone)
	e.waitState(third.ID, jobs.StateDone)
	kept.Emit("later still", 2)
	if after := e.snapshot(first.ID); after != before || r.MaxConcurrent() != 1 {
		t.Errorf("what the runner did late changed the job, or another job ran beside it:\n%s\n%s", before, after)
	}
	if n := len(e.Log.Find("ERROR", "")); n != 1 {
		t.Errorf("%d ERROR records, want only the overrun's: %v", n, e.Log.Find("ERROR", ""))
	}
}

func TestCancel_AJobCancelledBeforeItsRunnerIsCalledNeverReachesIt(t *testing.T) {
	var e *env
	var once sync.Once
	var id string
	e = newEnv(t, withFault(func(op string) error {
		if op == jobs.OpInvoke { // claimed, known to be in flight, its request not read yet
			once.Do(func() {
				if _, err := e.Svc.Cancel(ctx, id); err != nil {
					t.Errorf("cancel: %v", err)
				}
			})
		}
		return nil
	}))
	id = e.create("widgets", 42, "af:fix").ID
	e.start()
	doc := e.waitRest(id)
	if len(e.Runner.Calls()) != 0 || doc.State != jobs.StateCancelled || doc.Attempts[0].State != jobs.AttemptCancelled || doc.Attempts[0].Error.Category != "cancelled" {
		t.Errorf("%d calls, job %s, attempt %+v, want the attempt cancelled without a call", len(e.Runner.Calls()), doc.State, doc.Attempts[0])
	}
	if st, err := e.Svc.Standing(ctx, id); err != nil || st.CancelledInRunner || st.RunnerInProgress {
		t.Errorf("standing = %+v, %v, want a cancel that reached no runner", st, err)
	}
}

func TestCancel_AnAttemptLeftRunningUnderACancelledJobIsRecordedCancelledAfterARestart(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.BlockIgnoringContext(fakerunner.Done()))))
	job := e.create("widgets", 1, "")
	e.start()
	e.waitCalls(1)
	if _, err := e.Svc.Cancel(ctx, job.ID); err != nil { // the runner ignores it, and the process ends
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	e.closed = true
	go func() { closed <- e.Svc.Close() }()
	e.waitWaits(2) // the grace, and the wait for the runners at shutdown
	e.Clock.Advance(30 * time.Second)
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if a := e.get(job.ID).Attempts[0]; a.State != jobs.AttemptRunning {
		t.Fatalf("the attempt is %s after the stop, want it left running", a.State)
	}
	r := fakerunner.New()
	e.restart(r)
	doc := e.waitRest(job.ID)
	if a := doc.Attempts[0]; doc.State != jobs.StateCancelled || a.State != jobs.AttemptCancelled || a.Error.Category != "cancelled" || len(r.Calls()) != 0 {
		t.Errorf("job %s, attempt %s %+v, %d calls, want it recorded cancelled without a runner", doc.State, a.State, a.Error, len(r.Calls()))
	}
}
