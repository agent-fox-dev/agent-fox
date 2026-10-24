package jobs_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TestTheAttemptDocumentShowsItsContextAsAListOrNull_TS_19_23 verifies TS-19-23: a retry from the forge stores its text as the
// new attempt's context, which the document shows as a list of strings, and an attempt without one shows null.
// Verifies: 19-REQ-4.3
func TestTheAttemptDocumentShowsItsContextAsAListOrNull_TS_19_23(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Failed("sandbox", false)).Then(fakerunner.Done())))
	job := e.create("widgets", jobs.KindFix, 42)
	e.Start()
	e.waitState(job.ID, jobs.StateFailed)
	text := "--- comment by alice ---\nthe log is in CI run 812"
	if _, err := e.Svc.RetryWithContext(ctx, job.ID, "label af:fix", []string{text}); err != nil {
		t.Fatal(err)
	}
	d := e.waitRest(job.ID)
	if len(d.Attempts) != 2 || d.Attempts[0].Context != nil || !slices.Equal(d.Attempts[1].Context, []string{text}) {
		t.Fatalf("the attempts are %+v, want attempt 1 without context and attempt 2 with the text", d.Attempts)
	}
	if d.Attempts[1].Trigger != (jobs.Trigger{Kind: jobs.TriggerRetry, By: "label af:fix"}) {
		t.Errorf("attempt 2's trigger is %+v", d.Attempts[1].Trigger)
	}
	raw, _ := json.Marshal(d.Attempts)
	var attempts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &attempts); err != nil {
		t.Fatal(err)
	}
	if string(attempts[0]["context"]) != "null" || string(attempts[1]["context"]) != mustJSON([]string{text}) {
		t.Errorf("the documents show the context as %s and %s, want null and the list", attempts[0]["context"], attempts[1]["context"])
	}
}

// TestTheRunnersContextIsTheJobsThenEachAttemptsInOrderKeptByAnAutomaticAttempt_TS_19_24 verifies TS-19-24: a job made with
// the context "job ctx" fails, is retried from the forge with the text R1, and the retry's attempt fails in a way that is
// tried again by itself: the automatic attempt's runner is given "job ctx" and then R1.
// Verifies: 19-REQ-4.4
func TestTheRunnersContextIsTheJobsThenEachAttemptsInOrderKeptByAnAutomaticAttempt_TS_19_24(t *testing.T) {
	r := fakerunner.New(
		fakerunner.Failed("sandbox", false),
		fakerunner.Return(jobs.Result{Envelope: fakerunner.Env(toolio.StatusFailed, fakerunner.WithError("brain", "gave up", true)), MaxAttempts: 3}),
		fakerunner.Done(),
	)
	e := newEnv(t, withRunner(r))
	job, err := e.Svc.Create(ctx, jobs.CreateRequest{
		Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage("42"), Context: []string{"job ctx"}},
		Source: jobs.Source{Kind: "label", By: "label af:fix"}, RequestLabel: "af:fix",
	})
	if err != nil {
		t.Fatal(err)
	}
	e.Start()
	e.waitState(job.ID, jobs.StateFailed)
	if _, err := e.Svc.RetryWithContext(ctx, job.ID, "label af:fix", []string{"R1"}); err != nil {
		t.Fatal(err)
	}
	d := e.waitRest(job.ID)
	if d.State != jobs.StateDone || len(d.Attempts) != 3 || d.Attempts[2].Trigger.Kind != jobs.TriggerAuto {
		t.Fatalf("job %s with attempts %+v, want done after the automatic attempt 3", d.State, d.Attempts)
	}
	calls := r.Calls()
	if len(calls) != 3 {
		t.Fatalf("%d calls, want 3", len(calls))
	}
	for i, want := range [][]string{{"job ctx"}, {"job ctx", "R1"}, {"job ctx", "R1"}} {
		if !slices.Equal(calls[i].Context, want) {
			t.Errorf("attempt %d's runner was given the context %q, want %q", i+1, calls[i].Context, want)
		}
	}
	if d.Attempts[2].Context != nil {
		t.Errorf("the automatic attempt has the context %q of its own, want null", d.Attempts[2].Context)
	}
}

// TestTheAPIsRetryStaysBodylessAndQueuesAnAttemptWithNoContext_TS_19_25 verifies TS-19-25.
// Verifies: 19-REQ-4.5
func TestTheAPIsRetryStaysBodylessAndQueuesAnAttemptWithNoContext_TS_19_25(t *testing.T) {
	failure := fakerunner.Failed("sandbox", false)
	e := newEnv(t, withRunner(fakerunner.New(failure, failure).Then(fakerunner.Done())))
	a, b := e.create("widgets", jobs.KindFix, 1), e.create("widgets", jobs.KindFix, 2)
	e.Start()
	e.waitState(a.ID, jobs.StateFailed)
	e.waitState(b.ID, jobs.StateFailed)
	if resp := e.retry(a.ID, nil); resp.Status != http.StatusAccepted {
		t.Fatalf("retry with no body = %d\n%s", resp.Status, resp.Body)
	}
	if d := e.waitRest(a.ID); len(d.Attempts) != 2 || d.Attempts[1].Context != nil {
		t.Errorf("the attempts are %+v, want attempt 2 with the context null", d.Attempts)
	}
	if resp := e.retry(b.ID, `{"context": ["x"]}`); resp.Status != http.StatusBadRequest {
		t.Errorf("retry with a body = %d, want 400", resp.Status)
	}
	if d := e.get(b.ID); d.State != jobs.StateFailed || len(d.Attempts) != 1 {
		t.Errorf("a refused retry changed the job: %s with %d attempts", d.State, len(d.Attempts))
	}
}
