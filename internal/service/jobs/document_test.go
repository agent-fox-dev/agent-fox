package jobs_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestDoc_ABlockedJobShowsItsQuestionWithTheOptionsLetteredInOrder(t *testing.T) {
	_, docs := runEach(t,
		fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, func(env *toolio.Envelope) {
			env.NeedsHuman = &toolio.NeedsHuman{Question: "Which?", Options: []toolio.Option{{ID: "3", Label: "three"}, {ID: "x", Label: "ex"}, {ID: "A", Label: "again"}}}
		})),
		fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Why?"))),
		fakerunner.Done(),
	)
	asked, free, done := docs[0], docs[1], docs[2]
	want := `{"reason":"needs_human","question":"Which?","options":[{"id":"A","label":"three"},{"id":"B","label":"ex"},{"id":"C","label":"again"}],"answer_with":"POST /api/v1/jobs/` + asked.ID + `/answer"}`
	if got := string(testutil.JSON(t, asked.Blocked)); got != want {
		t.Errorf("blocked = %s, want %s", got, want)
	}
	if asked.FinishedAt != nil || asked.Result.Status != toolio.StatusNeedsHuman || asked.Result.ExitCode != 3 {
		t.Errorf("finished_at %v, result %+v, want none and needs_human with exit code 3", asked.FinishedAt, asked.Result)
	}
	if !strings.Contains(string(testutil.JSON(t, free.Blocked)), `"options":[]`) {
		t.Errorf("a question without options: %s, want options []", testutil.JSON(t, free.Blocked))
	}
	if done.Blocked != nil {
		t.Errorf("a job that is not blocked shows %+v", done.Blocked)
	}
}

func TestDoc_TheResultArtifactsAndSideEffectsAreThoseOfTheLatestAttemptThatEnded(t *testing.T) {
	artifacts := []toolio.Artifact{{Kind: "pull_request", Number: 3}}
	e := newEnv(t, withRunner(fakerunner.New(
		fakerunner.Return(jobs.Result{Envelope: fakerunner.Env(toolio.StatusFailed, fakerunner.WithError("brain", "gave up", true), fakerunner.WithArtifacts(toolio.Artifact{Kind: "stale"}))}),
		fakerunner.Block(fakerunner.Done(fakerunner.WithSummary("second"), fakerunner.WithArtifacts(artifacts...))),
	)))
	job := e.create("widgets", 1, "")
	if string(testutil.JSON(t, job.Artifacts))+string(testutil.JSON(t, job.SideEffects)) != "[][]" || job.Result != nil {
		t.Errorf("a job nothing has ended: result %+v, artifacts %s, side effects %s", job.Result, testutil.JSON(t, job.Artifacts), testutil.JSON(t, job.SideEffects))
	}
	e.start()
	testutil.Eventually(t, "the second attempt to run", func() bool { d := e.get(job.ID); return d.CurrentAttempt == 2 && d.State == jobs.StateRunning })
	if d := e.get(job.ID); d.Result == nil || d.Result.Status != toolio.StatusFailed || d.Artifacts[0].Kind != "stale" || d.Attempts[1].Status != nil {
		t.Errorf("while attempt 2 runs: result %+v, artifacts %+v, want those of attempt 1", d.Result, d.Artifacts)
	}
	e.Runner.ReleaseAll()
	d := e.waitRest(job.ID)
	if d.Result.Status != toolio.StatusDone || d.Result.Summary != "second" || d.Result.Error != nil || !slices.Equal(d.Artifacts, artifacts) {
		t.Errorf("result %+v, artifacts %+v, want those of attempt 2", d.Result, d.Artifacts)
	}
}

func TestDoc_ShowsTheHubReferencesOfTheCurrentDeliveryOnlyWhileItRuns(t *testing.T) {
	reported, release, hubbed, release2 := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	e := newEnv(t, withRunner(fakerunner.New(
		fakerunner.Do(func(_ context.Context, req jobs.Request) (jobs.Result, error) {
			req.Recorder.Hub("session-1", "run-1", "box-1")
			return jobs.Result{}, jobs.Transient(sandboxFailure)
		}),
		fakerunner.Do(func(_ context.Context, req jobs.Request) (jobs.Result, error) {
			close(reported)
			<-release
			req.Recorder.Hub("session-2", "", "box-2")
			close(hubbed)
			<-release2
			return jobs.Result{Envelope: fakerunner.Env(toolio.StatusDone), LabelsSet: []string{"af:done"}}, nil
		}),
	)))
	e.openAtEnd(release, release2)
	job := e.create("widgets", 1, "")
	e.start()
	e.waitWaits(1)
	if d := e.get(job.ID); d.Hub != nil {
		t.Errorf("hub = %+v while the attempt waits out its backoff, want null", d.Hub)
	}
	e.Clock.Advance(30 * time.Second)
	<-reported
	if d := e.get(job.ID); d.State != jobs.StateRunning || d.Hub != nil || d.Labels != nil {
		t.Errorf("the second delivery before it reported: %s, hub %+v, labels %+v, want running and nulls", d.State, d.Hub, d.Labels)
	}
	close(release)
	<-hubbed
	if h := e.get(job.ID).Hub; h == nil || *h.SessionID != "session-2" || h.RunID != nil || *h.SandboxID != "box-2" {
		t.Errorf("hub = %+v during the run, want session-2, no run and box-2", h)
	}
	close(release2)
	d := e.waitRest(job.ID)
	if d.Hub != nil || d.Labels == nil || !slices.Equal(d.Labels.Set, []string{"af:done"}) {
		t.Errorf("after the end: hub %+v, labels %+v, want null and the labels reported", d.Hub, d.Labels)
	}
}

func TestDoc_ListsTheAttemptsInOrderWithTheirTriggersAnswersAndCounts(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(question(), fakerunner.TransientFailure(sandboxFailure), fakerunner.Failed("sandbox", false))))
	job := e.create("widgets", 1, "")
	e.start()
	e.waitState(job.ID, jobs.StateBlocked)
	if _, err := e.Svc.Answer(ctx, job.ID, "user-2", jobs.AnswerRequest{Option: ptr("B")}); err != nil {
		t.Fatal(err)
	}
	e.waitWaits(1)
	e.Clock.Advance(30 * time.Second)
	doc := e.waitRest(job.ID)
	if len(doc.Attempts) != 2 {
		t.Fatalf("%d attempts, want 2", len(doc.Attempts))
	}
	one, two := doc.Attempts[0], doc.Attempts[1]
	if one.N != 1 || one.State != jobs.AttemptNeedsHuman || one.Trigger != (jobs.Trigger{Kind: jobs.TriggerCreate, By: "user-1"}) || one.Answer != nil || one.Deliveries != 1 || one.TransientFailures != 0 || one.Error != nil {
		t.Errorf("attempt 1 = %+v", one)
	}
	if two.N != 2 || two.State != jobs.AttemptFailed || two.Trigger != (jobs.Trigger{Kind: jobs.TriggerAnswer, By: "user-2"}) || two.Deliveries != 2 || two.TransientFailures != 1 || two.Error.Category != "sandbox" {
		t.Errorf("attempt 2 = %+v", two)
	}
	if a := two.Answer; a == nil || a.Question != "Which?" || *a.Option != "B" || a.Text != "two" {
		t.Errorf("the answer of attempt 2 = %+v", a)
	}
	for _, a := range doc.Attempts {
		if a.Status == nil || a.ExitCode == nil || *a.ExitCode != toolio.ExitCode(*a.Status) || a.Summary == nil || a.Usage == nil || a.StartedAt == nil || a.FinishedAt == nil {
			t.Errorf("attempt %d lacks what it came to: %+v", a.N, a)
		}
	}
}
