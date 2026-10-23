package jobs_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
)

// causeOf is the job's cancel_cause, "" when it is null.
func causeOf(d jobs.Doc) string {
	if d.CancelCause == nil {
		return ""
	}
	return *d.CancelCause
}

// TestTheRunnerOfAnIssueClosedCancelIsToldACancelThatSaysTheIssueClosed_TS_18_57 verifies TS-18-57.
// Verifies: 18-REQ-8.5
func TestTheRunnerOfAnIssueClosedCancelIsToldACancelThatSaysTheIssueClosed_TS_18_57(t *testing.T) {
	r := fakerunner.New(fakerunner.Block(fakerunner.Done()))
	e := newEnv(t, withRunner(r))
	e.Start()
	running := e.createFromLabel("widgets", 42, "af:fix")
	waitCalls(t, r, 1)
	queued, err := e.Svc.Create(ctx, jobs.CreateRequest{Workspace: "widgets", Kind: jobs.KindTriage, Body: jobs.Body{Issue: json.RawMessage("42")}, Source: jobs.Source{Kind: "label", By: "poll"}, RequestLabel: "af:triage"})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := e.Svc.CancelIssue(ctx, "widgets", 42)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(ids)
	want := []string{running.ID, queued.ID}
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Errorf("CancelIssue cancelled %v, want %v", ids, want)
	}
	cause := r.Calls()[0].Cause()
	if !errors.Is(cause, jobs.ErrCancelled) || !errors.Is(cause, jobs.ErrIssueClosed) || !jobs.IsCancel(cause) {
		t.Errorf("the runner's cause is %v, want a cancel that is ErrIssueClosed", cause)
	}
	if d := e.get(queued.ID); d.State != jobs.StateCancelled || d.Attempts[0].State != jobs.AttemptCancelled {
		t.Errorf("the queued job is %s with its attempt %s, want both cancelled", d.State, d.Attempts[0].State)
	}
	for _, id := range ids {
		if last := e.lastEvent(id); last.Type != jobs.EventJobState {
			t.Errorf("%s: the last event is %s, want job_state", id, last.Type)
		}
	}
	e.waitRest(running.ID)
}

// TestAJobThatChangesStateBetweenTheLookAndTheCancelIsLookedAtAgain_TS_18_58 verifies TS-18-58.
// Verifies: 18-REQ-8.6
func TestAJobThatChangesStateBetweenTheLookAndTheCancelIsLookedAtAgain_TS_18_58(t *testing.T) {
	e := newEnv(t, withoutEnqueue())
	once := e.createFromLabel("widgets", 42, "af:fix")
	looks := 0
	e.Svc.AfterLook(func(id string, state jobs.State) {
		if id == once.ID && looks == 0 {
			e.exec("UPDATE af_jobs SET state = 'running' WHERE id = ?", id) // claimed in the meantime
		}
		looks++
	})
	if ids, err := e.Svc.CancelIssue(ctx, "widgets", 42); err != nil || len(ids) != 1 {
		t.Fatalf("CancelIssue = %v, %v", ids, err)
	}
	if d := e.get(once.ID); d.State != jobs.StateCancelled || causeOf(d) != "issue_closed" || looks != 2 {
		t.Errorf("the job is %s with cause %q after %d looks, want cancelled issue_closed after 2", d.State, causeOf(d), looks)
	}

	always := e.createFromLabel("widgets", 43, "af:fix")
	tries := 0
	e.Svc.AfterLook(func(id string, state jobs.State) {
		tries++
		next := map[jobs.State]string{jobs.StateQueued: "blocked", jobs.StateBlocked: "queued"}[state]
		e.exec("UPDATE af_jobs SET state = ? WHERE id = ?", next, id)
	})
	if _, err := e.Svc.CancelIssue(ctx, "widgets", 43); err == nil || tries != 8 {
		t.Errorf("CancelIssue of a job that keeps changing: %v after %d tries, want an error after 8", err, tries)
	}
	if d := e.get(always.ID); d.State == jobs.StateCancelled {
		t.Error("the job that kept changing was cancelled")
	}
}

// TestCancelCauseShowsWhoCancelledTheJob_TS_18_59 verifies TS-18-59.
// Verifies: 18-REQ-8.7
func TestCancelCauseShowsWhoCancelledTheJob_TS_18_59(t *testing.T) {
	r := fakerunner.New().Then(fakerunner.Block(fakerunner.Step{})) // every job holds its workspace until it is cancelled
	e := newEnv(t, withRunner(r))
	e.addWorkspace("gadgets")
	e.Start()
	running := e.create("widgets", jobs.KindFix, 1)
	waitCalls(t, r, 1)
	person := e.createFromLabel("widgets", 2, "af:fix")
	closed := e.createFromLabel("widgets", 3, "af:fix")
	old := e.createFromLabel("widgets", 4, "af:fix")
	unsub, err := e.Svc.Create(ctx, jobs.CreateRequest{Workspace: "gadgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage("5")}, Source: jobs.Source{Kind: "label", By: "poll"}, RequestLabel: "af:fix"})
	if err != nil {
		t.Fatal(err)
	}
	waitCalls(t, r, 2)
	e.cancel(person.ID)
	if _, err := e.Svc.CancelIssue(ctx, "widgets", 3); err != nil {
		t.Fatal(err)
	}
	if err := e.Svc.Unsubscribe(ctx, "gadgets", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	e.cancel(old.ID)
	e.exec("UPDATE af_jobs SET cancel_cause = NULL WHERE id = ?", old.ID) // cancelled before this release
	for id, want := range map[string]string{person.ID: "cancelled", unsub.ID: "unsubscribed", closed.ID: "issue_closed", running.ID: "", old.ID: ""} {
		if got := causeOf(e.readDoc(id)); got != want {
			t.Errorf("%s: cancel_cause %q, want %q", id, got, want)
		}
		st, err := e.Svc.Standing(ctx, id)
		if err != nil || st.CancelCause != want {
			t.Errorf("%s: Standing's cause %q (%v), want %q", id, st.CancelCause, err, want)
		}
	}
	raw := e.call("GET", "/api/v1/jobs/"+running.ID, nil).JSON(t)
	if v, ok := raw["cancel_cause"]; !ok || v != nil {
		t.Errorf("a running job's document has cancel_cause %v (present %v), want null", v, ok)
	}
	if d := e.get(closed.ID); d.Labels != nil && d.Labels.Restore != nil {
		t.Errorf("an issue-closed cancel marks %q to be put back, want nothing", *d.Labels.Restore)
	}
	e.cancel(running.ID)
	e.waitRest(running.ID)
}

// TestTheEnvelopeOfAnIssueClosedAttemptSaysTheIssueClosed_TS_18_60 verifies TS-18-60.
// Verifies: 18-REQ-8.8
func TestTheEnvelopeOfAnIssueClosedAttemptSaysTheIssueClosed_TS_18_60(t *testing.T) {
	envelopeOf := func(e *env, id string) map[string]any {
		t.Helper()
		raw, err := e.Svc.Envelope(ctx, id, 1)
		if err != nil {
			t.Fatal(err)
		}
		var env map[string]any
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatal(err)
		}
		failure, _ := env["error"].(map[string]any)
		return failure
	}
	t.Run("the runner returns without an envelope", func(t *testing.T) {
		r := fakerunner.New(fakerunner.Block(fakerunner.Step{}))
		e := newEnv(t, withRunner(r))
		e.Start()
		job := e.createFromLabel("widgets", 42, "af:fix")
		waitCalls(t, r, 1)
		if _, err := e.Svc.CancelIssue(ctx, "widgets", 42); err != nil {
			t.Fatal(err)
		}
		e.waitRest(job.ID)
		if f := envelopeOf(e, job.ID); f["category"] != "cancelled" || !strings.Contains(f["message"].(string), "issue was closed") {
			t.Errorf("the envelope's error is %v", f)
		}
	})
	t.Run("the runner overruns the grace", func(t *testing.T) {
		clk := fakeclock.New(epoch)
		r := fakerunner.New(fakerunner.BlockIgnoringContext(fakerunner.Done()))
		e := newEnv(t, withRunner(r), withClock(clk))
		e.Start()
		job := e.createFromLabel("widgets", 42, "af:fix")
		waitCalls(t, r, 1)
		waits := len(clk.Waits())
		if _, err := e.Svc.CancelIssue(ctx, "widgets", 42); err != nil {
			t.Fatal(err)
		}
		clk.WaitForWaits(waits+1, 10*time.Second)
		clk.Advance(60 * time.Second)
		waitFor(t, "the attempt to be recorded", func() bool { return e.get(job.ID).Attempts[0].State == jobs.AttemptCancelled })
		if f := envelopeOf(e, job.ID); f["category"] != "cancelled" || !strings.Contains(f["message"].(string), "issue was closed") {
			t.Errorf("the envelope's error is %v", f)
		}
		r.ReleaseAll()
	})
}
