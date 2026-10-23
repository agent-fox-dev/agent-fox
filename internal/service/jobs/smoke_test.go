package jobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/service/poll"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The smoke tests run a path of the spec through the real HTTP mux and handlers, the real
// jobs service, the SQLite file, aflib/jobqueue, the SSE handler and the timers of the service,
// against the core's fake hub; only the runner and, where a path has an hour in it, the clock
// are the tests' own.

// readDoc reads a job through the API.
func (e *env) readDoc(id string) jobs.Doc {
	e.t.Helper()
	r := e.call("GET", "/api/v1/jobs/"+id, nil)
	if r.Status != 200 {
		e.t.Fatalf("GET /jobs/%s = %d %s", id, r.Status, r.Body)
	}
	return r.Doc(e.t)
}

// pollRest reads a job through the API until it is at rest.
func (e *env) pollRest(id string) jobs.Doc {
	e.t.Helper()
	var doc jobs.Doc
	waitFor(e.t, id+" to be at rest", func() bool {
		doc = e.readDoc(id)
		return atRest(doc)
	})
	return doc
}

// TestAFixRequestRunsToDoneThroughTheHTTPAPIAndTheStoredEnvelopeReadsBack_TS_12_71 verifies
// TS-12-71 and 12-PATH-1: a fix is posted and answered 202 queued; the runner is called once for
// attempt 1; the job reads done with the artifacts and the usage of the envelope, exit code 0 and
// one attempt; and the envelope route returns the stored bytes exactly.
// Verifies: 12-PATH-1, 12-REQ-2.3, 12-REQ-9.6
func TestAFixRequestRunsToDoneThroughTheHTTPAPIAndTheStoredEnvelopeReadsBack_TS_12_71(t *testing.T) {
	artifacts := []toolio.Artifact{{Kind: "pull_request", URL: "https://github.example.com/acme/widgets/pull/9", Number: 9}}
	usage := toolio.Usage{Brain: "scripted", CostUSD: 1.25, CostSource: toolio.CostMeasured, InputTokens: 1000, OutputTokens: 200, Turns: 7}
	given := fakerunner.Env(toolio.StatusDone, fakerunner.WithSummary("fixed it"), fakerunner.WithArtifacts(artifacts...), fakerunner.WithUsage(usage))
	r := fakerunner.New(fakerunner.Return(jobs.Result{Envelope: given, Report: "# Fixed\n"}))
	e := newEnv(t, withRunner(r))
	e.Start()

	posted := e.call("POST", "/api/v1/workspaces/widgets/fix", map[string]any{"issue": 42})
	if posted.Status != http.StatusAccepted {
		t.Fatalf("POST = %d %s, want 202", posted.Status, posted.Body)
	}
	job := posted.Doc(t)
	if job.State != jobs.StateQueued || job.Kind != jobs.KindFix || job.Workspace != "widgets" {
		t.Errorf("the answer to the POST = %s %s on %s, want a fix on widgets, queued", job.State, job.Kind, job.Workspace)
	}

	doc := e.pollRest(job.ID)
	waitCalls(t, r, 1)
	if calls := r.Calls(); len(calls) != 1 || calls[0].JobID != job.ID || calls[0].Attempt != 1 || calls[0].Delivery != 1 {
		t.Fatalf("the runner was called %d times, first with %+v, want once for attempt 1", len(calls), r.Calls())
	}
	if doc.State != jobs.StateDone || doc.Result == nil || doc.Result.ExitCode != 0 || doc.Result.Summary != "fixed it" || len(doc.Attempts) != 1 || doc.AttemptCount != 1 {
		t.Fatalf("the job reads %s, result %+v, %d attempts, want done, exit code 0, one attempt", doc.State, doc.Result, len(doc.Attempts))
	}
	if mustJSON(doc.Artifacts) != mustJSON(artifacts) {
		t.Errorf("artifacts = %s, want those of the envelope", mustJSON(doc.Artifacts))
	}
	if doc.Usage.CostUSD != 1.25 || doc.Usage.InputTokens != 1000 || doc.Usage.OutputTokens != 200 || doc.Usage.Turns != 7 {
		t.Errorf("usage = %+v, want the sum over the one attempt", doc.Usage)
	}
	if doc.StartedAt == nil || doc.FinishedAt == nil {
		t.Errorf("started_at %v and finished_at %v, want both set", doc.StartedAt, doc.FinishedAt)
	}

	envelope := e.call("GET", "/api/v1/jobs/"+job.ID+"/attempts/1/envelope", nil)
	onDisk, err := os.ReadFile(e.fileOf(job.ID, 1, "envelope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Status != 200 || string(envelope.Body) != string(onDisk) || string(envelope.Body) != string(given) {
		t.Errorf("the envelope route returned %d and %d bytes, the file has %d and the runner gave %d: want the same bytes", envelope.Status, len(envelope.Body), len(onDisk), len(given))
	}
	if report := e.call("GET", "/api/v1/jobs/"+job.ID+"/attempts/1/report", nil); report.Status != 200 || string(report.Body) != "# Fixed\n" {
		t.Errorf("the report route returned %d %q", report.Status, report.Body)
	}
	if list := e.call("GET", "/api/v1/jobs", nil).page(t); !slices.Equal(list.ids(), []string{job.ID}) {
		t.Errorf("the list holds %v, want the job", list.ids())
	}
	// The visibility of the workspace was asked of hub once, and kept for the rest.
	if n := e.Hub.Count("/api/v1/workspaces/widgets", fakehub.TokenHash(callerToken)); n != 1 {
		t.Errorf("hub was asked %d times whether the caller sees widgets, want once and then the cache", n)
	}
}

// TestATriageJobBlocksOnAQuestionIsAnsweredWithOptionBAndRunsAttemptTwo_TS_12_72 verifies
// TS-12-72 and 12-PATH-2: the job reads blocked with lettered options; the answer by option B is
// 202; attempt 2 reaches the runner with the question, the options and B's label as the answer;
// and the job ends done with two attempts, the second started by the answer.
// Verifies: 12-PATH-2, 12-REQ-8.3
func TestATriageJobBlocksOnAQuestionIsAnsweredWithOptionBAndRunsAttemptTwo_TS_12_72(t *testing.T) {
	r := fakerunner.New(
		fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which branch?", "main", "release", "next"))),
		fakerunner.Done(fakerunner.WithSummary("triaged on release")),
	)
	e := newEnv(t, withRunner(r))
	e.Start()
	posted := e.call("POST", "/api/v1/workspaces/widgets/triage", map[string]any{"issue": 7})
	if posted.Status != 202 {
		t.Fatalf("POST = %d %s", posted.Status, posted.Body)
	}
	id := posted.Doc(t).ID

	var blocked jobs.Doc
	waitFor(t, "the job to be blocked", func() bool { blocked = e.readDoc(id); return blocked.State == jobs.StateBlocked })
	b := blocked.Blocked
	if b == nil || b.Reason != "needs_human" || b.Question != "Which branch?" || b.AnswerWith != "POST /api/v1/jobs/"+id+"/answer" ||
		!slices.Equal(b.Options, []jobs.Option{{ID: "A", Label: "main"}, {ID: "B", Label: "release"}, {ID: "C", Label: "next"}}) {
		t.Fatalf("blocked = %+v, want the question with options A main, B release, C next", b)
	}

	if answer := e.answer(id, map[string]string{"option": "B"}); answer.Status != 202 {
		t.Fatalf("answer = %d %s, want 202", answer.Status, answer.Body)
	}
	doc := e.pollRest(id)
	if doc.State != jobs.StateDone || len(doc.Attempts) != 2 || doc.Attempts[1].Trigger != (jobs.Trigger{Kind: jobs.TriggerAnswer, By: callerID}) {
		t.Fatalf("the job reads %s with attempts %+v, want done, and attempt 2 started by the answer", doc.State, doc.Attempts)
	}
	calls := r.Calls()
	if len(calls) != 2 || calls[1].Attempt != 2 || calls[1].Delivery != 1 || len(calls[1].Answers) != 1 {
		t.Fatalf("the runner's calls = %d, the second %+v, want two, and attempt 2 with one answer", len(calls), calls[1].Request)
	}
	want := jobs.Answer{Question: "Which branch?", Options: b.Options, Option: strPtr("B"), Text: "release"}
	if got := calls[1].Answers[0]; mustJSON(got) != mustJSON(want) {
		t.Errorf("the answer given to attempt 2 = %s, want %s", mustJSON(got), mustJSON(want))
	}
	if len(calls[0].Answers) != 0 {
		t.Errorf("attempt 1 was given answers %v", calls[0].Answers)
	}
}

func strPtr(s string) *string { return &s }

// TestARunningJobIsCancelledWhileAClientFollowsItsEventsAndTheNextJobStartsAfterTheRunnerReturns_TS_12_73
// verifies TS-12-73 and 12-PATH-3: the cancel is 200 with the job cancelled and finished_at set;
// the runner sees a cancel cause; the second job of the workspace does not start while that runner
// is in progress and does as soon as it returns; and the stream that followed the job ends with the
// job_state at rest as its last frame, and not before.
// Verifies: 12-PATH-3, 12-REQ-8.1, 12-REQ-10.6
func TestARunningJobIsCancelledWhileAClientFollowsItsEventsAndTheNextJobStartsAfterTheRunnerReturns_TS_12_73(t *testing.T) {
	r := fakerunner.New(fakerunner.BlockIgnoringContext(fakerunner.Step{}), fakerunner.Done())
	e := newEnv(t, withRunner(r))
	first := e.create("widgets", jobs.KindFix, 1)
	second := e.create("widgets", jobs.KindFix, 2)
	e.Start()
	waitCalls(t, r, 1)
	e.waitState(first.ID, jobs.StateRunning)
	follower := e.stream(first.ID, "", nil)

	cancelled := e.call("POST", "/api/v1/jobs/"+first.ID+"/cancel", nil)
	doc := cancelled.Doc(t)
	if cancelled.Status != 200 || doc.State != jobs.StateCancelled || doc.FinishedAt == nil {
		t.Fatalf("cancel = %d, state %s, finished_at %v, want 200, cancelled, set", cancelled.Status, doc.State, doc.FinishedAt)
	}
	waitFor(t, "the runner to see the cancel", func() bool { return jobs.IsCancel(r.Calls()[0].Cause()) })
	if cause := r.Calls()[0].Cause(); errors.Is(cause, jobs.ErrShutdown) || !errors.Is(cause, jobs.ErrCancelled) {
		t.Errorf("the cause the runner saw = %v, want the cancel", cause)
	}

	// The runner is still in progress: the stream is open, and the second job has not begun.
	var sawCancel bool
	for !sawCancel {
		f, err := follower.next(5 * time.Second)
		if err != nil {
			t.Fatalf("the stream, before the runner returned: %v", err)
		}
		if f.Event == jobs.EventJobState && !f.isComment() {
			if f.atRest(t) {
				t.Fatal("a job_state at rest came while the runner was in progress")
			}
			var data struct{ State jobs.State }
			if err := json.Unmarshal(f.event(t).Data, &data); err != nil {
				t.Fatal(err)
			}
			sawCancel = data.State == jobs.StateCancelled
		}
	}
	if f, err := follower.next(300 * time.Millisecond); !errors.Is(err, errTimeout) {
		t.Fatalf("the stream, with the runner in progress: %+v %v, want it open and quiet", f, err)
	}
	if n := len(r.Calls()); n != 1 {
		t.Fatalf("the runner has had %d calls, want the second job to wait for the first runner", n)
	}

	released := time.Now()
	r.Release(0)
	var rest []frame
	for {
		f, err := follower.next(5 * time.Second)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("the stream after the release: %v", err)
		}
		rest = append(rest, f)
	}
	follower.check()
	if len(rest) == 0 || !rest[len(rest)-1].atRest(t) {
		t.Fatalf("the frames after the release = %+v, want the last to be the job_state at rest", rest)
	}
	if last := rest[len(rest)-1].event(t); last.JobID != first.ID {
		t.Errorf("the last frame is an event of %s, want %s", last.JobID, first.ID)
	}

	waitCalls(t, r, 2)
	if next := r.Calls()[1]; next.JobID != second.ID || next.Started.Before(released) {
		t.Errorf("the second call = %s started %v, released at %v: want the second job, after the first runner returned", next.JobID, next.Started, released)
	}
	final := e.pollRest(first.ID)
	if final.State != jobs.StateCancelled || final.Attempts[0].State != jobs.AttemptCancelled {
		t.Errorf("the first job reads %s with attempt %s, want cancelled", final.State, final.Attempts[0].State)
	}
	if e.pollRest(second.ID).State != jobs.StateDone {
		t.Error("the second job did not run to done")
	}
}

// TestStoppingTheServiceWhileARunnerHoldsAttemptOneAndRestartingHandsItOverAgain_TS_12_74
// verifies TS-12-74 and 12-PATH-4: a service closed while a runner holds attempt 1 leaves it
// running, whatever the runner returned, and the two jobs behind it queued; a service started on
// the same database file hands attempt 1 over again with the handover count 2, makes no attempt
// 2, and then runs the other two once each, in order.
// Verifies: 12-PATH-4, 12-REQ-7.5, 12-REQ-7.6
func TestStoppingTheServiceWhileARunnerHoldsAttemptOneAndRestartingHandsItOverAgain_TS_12_74(t *testing.T) {
	// The runner returns a done envelope when it is told to stop, which has to be thrown away.
	r1 := fakerunner.New(fakerunner.Block(fakerunner.Done()))
	e := newEnv(t, withRunner(r1))
	first := e.create("widgets", jobs.KindFix, 1)
	second := e.create("widgets", jobs.KindFix, 2)
	third := e.create("widgets", jobs.KindFix, 3)
	e.Start()
	waitCalls(t, r1, 1)
	e.waitState(first.ID, jobs.StateRunning)

	if err := e.Svc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if r1.Active() != 0 {
		t.Fatalf("%d runners in progress after Close, want none", r1.Active())
	}
	doc := e.get(first.ID)
	if doc.State != jobs.StateRunning || doc.Attempts[0].State != jobs.AttemptRunning || doc.Attempts[0].Deliveries != 1 || doc.Result != nil {
		t.Fatalf("after the stop the first job reads %s, attempt %+v, result %v: want it running, delivered once, and nothing recorded of what the runner returned", doc.State, doc.Attempts[0], doc.Result)
	}
	if e.count("SELECT COUNT(*) FROM af_attempts WHERE job_id = ? AND has_envelope = 1", first.ID) != 0 {
		t.Error("an envelope was stored for the attempt that was stopped")
	}
	for _, id := range []string{second.ID, third.ID} {
		if st := e.get(id).State; st != jobs.StateQueued {
			t.Errorf("job %s is %s at the stop, want queued", id, st)
		}
	}

	r2 := fakerunner.New()
	svc := e.newService(r2, nil)
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	waitCalls(t, r2, 3)
	calls := r2.Calls()
	if calls[0].JobID != first.ID || calls[0].Attempt != 1 || calls[0].Delivery != 2 {
		t.Errorf("the first call after the restart = job %s attempt %d delivery %d, want attempt 1 of the first job, delivery 2", calls[0].JobID, calls[0].Attempt, calls[0].Delivery)
	}
	for i, want := range []string{first.ID, second.ID, third.ID} {
		if calls[i].JobID != want || calls[i].Attempt != 1 {
			t.Errorf("call %d = job %s attempt %d, want attempt 1 of %s: the jobs run once each, in order", i+1, calls[i].JobID, calls[i].Attempt, want)
		}
	}
	for _, id := range []string{first.ID, second.ID, third.ID} {
		waitFor(t, id+" to be done", func() bool { d, err := svc.Get(ctx, id); return err == nil && d.State == jobs.StateDone })
	}
	if n := e.count("SELECT COUNT(*) FROM af_attempts WHERE job_id = ?", first.ID); n != 1 {
		t.Errorf("the first job has %d attempt rows, want only attempt 1, handed over again", n)
	}
	if n := len(r2.Calls()); n != 3 {
		t.Errorf("the runner after the restart was called %d times, want 3", n)
	}
}

// TestUnsubscribingAWorkspaceCancelsItsOpenJobsRefusesNewWorkMeanwhileAndThenAnswers204_TS_12_75
// verifies TS-12-75 and 12-PATH-5: with a blocked, a running and a queued job on widgets, a DELETE
// that is waiting for the runner marks the workspace, and a POST fix meanwhile is 404; when the
// runner returns the DELETE answers 204, the subscription is gone, and the three jobs read
// cancelled and are still readable.
// Verifies: 12-PATH-5, 12-REQ-8.7
func TestUnsubscribingAWorkspaceCancelsItsOpenJobsRefusesNewWorkMeanwhileAndThenAnswers204_TS_12_75(t *testing.T) {
	r := fakerunner.New(question(), fakerunner.BlockIgnoringContext(fakerunner.Step{}))
	e := newEnv(t, withRunner(r))
	blocked := e.create("widgets", jobs.KindFix, 1)
	running := e.create("widgets", jobs.KindFix, 2)
	queued := e.create("widgets", jobs.KindFix, 3)
	e.Start()
	e.waitState(blocked.ID, jobs.StateBlocked)
	waitCalls(t, r, 2)
	e.waitState(running.ID, jobs.StateRunning)
	if st := e.get(queued.ID).State; st != jobs.StateQueued {
		t.Fatalf("the third job is %s, want it queued behind the running one", st)
	}

	type answer struct {
		status int
		body   []byte
	}
	deleted := make(chan answer, 1)
	go func() {
		req, _ := http.NewRequest("DELETE", e.URL+"/api/v1/workspaces/widgets", nil)
		req.Header.Set("Authorization", "Bearer "+callerToken)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			deleted <- answer{status: -1, body: []byte(err.Error())}
			return
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		deleted <- answer{res.StatusCode, body}
	}()

	// The DELETE is waiting for the runner that it told to stop.
	waitFor(t, "the running job to be cancelled by the unsubscribe", func() bool { return e.get(running.ID).State == jobs.StateCancelled })
	if cause := r.Calls()[1].Cause(); !errors.Is(cause, jobs.ErrUnsubscribed) || !jobs.IsCancel(cause) {
		t.Errorf("the runner's cause = %v, want the unsubscribe, which reads as a cancel", cause)
	}
	if refused := e.call("POST", "/api/v1/workspaces/widgets/fix", map[string]any{"issue": 99}); refused.Status != 404 {
		t.Errorf("a POST fix during the unsubscribe = %d %s, want 404", refused.Status, refused.Body)
	}
	select {
	case a := <-deleted:
		t.Fatalf("the DELETE answered %d before the runner returned", a.status)
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := e.Store.Get(ctx, "widgets"); err != nil {
		t.Errorf("the subscription is gone while the runner is in progress: %v", err)
	}

	r.Release(1)
	select {
	case a := <-deleted:
		if a.status != 204 || len(a.body) != 0 {
			t.Fatalf("the DELETE answered %d %q, want 204 with no body", a.status, a.body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the DELETE did not answer after the runner returned")
	}
	if _, err := e.Store.Get(ctx, "widgets"); err == nil {
		t.Error("the subscription is still there after the 204")
	}
	for name, id := range map[string]string{"blocked": blocked.ID, "running": running.ID, "queued": queued.ID} {
		if doc := e.pollRest(id); doc.State != jobs.StateCancelled || doc.FinishedAt == nil {
			t.Errorf("the %s job reads %s, finished_at %v, want cancelled", name, doc.State, doc.FinishedAt)
		}
	}
	if n := len(r.Calls()); n != 2 {
		t.Errorf("the runner had %d calls, want 2: the queued job never ran", n)
	}
}

// TestTheHourlyPurgeOnAnInjectedClockRemovesExpiredJobsAndKeepsBlockedOnes_TS_12_76 verifies
// TS-12-76 and 12-PATH-6: with a done, a failed, a cancelled-while-queued and a blocked job
// and a retention of an hour, the retention timer of the service removes the first three, their
// directories and rows, once the clock has passed the hour, and GET answers 404 for them; the blocked
// job and one finished 31 minutes before remain readable.
// Verifies: 12-PATH-6, 12-REQ-1.5, 12-REQ-1.6
func TestTheHourlyPurgeOnAnInjectedClockRemovesExpiredJobsAndKeepsBlockedOnes_TS_12_76(t *testing.T) {
	clk := fakeclock.New(epoch)
	r := fakerunner.New(fakerunner.Done(), fakerunner.Failed("sandbox", false), question())
	e := newEnv(t, withRunner(r), withClock(clk), withRetain(time.Hour), withWorkers(1))
	done := e.create("widgets", jobs.KindFix, 1)
	failed := e.create("widgets", jobs.KindFix, 2)
	blocked := e.create("widgets", jobs.KindFix, 3)
	cancelled := e.create("widgets", jobs.KindFix, 4)
	if resp := e.call("POST", "/api/v1/jobs/"+cancelled.ID+"/cancel", nil); resp.Status != 200 {
		t.Fatalf("cancel: %d %s", resp.Status, resp.Body)
	}
	e.Start()
	e.pollRest(done.ID)
	e.pollRest(failed.ID)
	e.pollRest(blocked.ID)
	if st := e.pollRest(cancelled.ID); st.State != jobs.StateCancelled {
		t.Fatalf("the fourth job reads %s", st.State)
	}

	clk.Advance(30 * time.Minute)
	recent := e.create("widgets", jobs.KindFix, 5)
	e.pollRest(recent.ID)
	for _, id := range []string{done.ID, failed.ID, blocked.ID, recent.ID} {
		if !e.has(id) {
			t.Fatalf("job %s has no directory before the purge", id)
		}
	}

	// 61 minutes after the first jobs were finished, 31 after the last, and the timer fires at 60.
	clk.Advance(31 * time.Minute)
	expired := []string{done.ID, failed.ID, cancelled.ID}
	waitFor(t, "the retention timer to remove the expired jobs", func() bool {
		for _, id := range expired {
			if e.status(id) != 404 {
				return false
			}
		}
		return true
	})
	for _, id := range expired {
		e.gone(id)
	}
	for name, id := range map[string]string{"blocked": blocked.ID, "recent": recent.ID} {
		if e.status(id) != 200 || !e.has(id) {
			t.Errorf("the %s job: status %d, directory %v, want it kept", name, e.status(id), e.has(id))
		}
	}
	if list := e.call("GET", "/api/v1/jobs", nil).page(t); len(list.Jobs) != 2 {
		t.Errorf("the list holds %v, want the blocked job and the recent one", list.ids())
	}
}

// labelEnv is the service with the hub-backed cancel cleanup, the fake clock and the runner, whose fake hub serves widgets
// with the issue 42 carrying the labels, and the label poll of widgets over the same layer.
func labelEnv(t *testing.T, r *fakerunner.Runner, labels ...string) (*env, *fakeclock.Clock, *poll.Supervisor) {
	t.Helper()
	clk := fakeclock.New(epoch)
	e := newEnv(t, withHubCleaner(), withRunner(r), withClock(clk))
	e.Hub.AddWorkspace(t, fakehub.Workspace{Slug: "widgets", PAT: "pat-of-widgets"})
	e.Hub.AddIssue("widgets", fakehub.Issue{Number: 42, Labels: labels})
	e.Start()
	sup := poll.New(poll.Config{
		Subscriptions: e.Store, Hub: hub.New(e.Hub.URL, "test"), Jobs: e.Svc, DB: e.DB, Clock: clk,
		Logger: slog.New(slog.NewJSONHandler(e.Logs, nil)), FirstDelay: func() time.Duration { return 0 },
	})
	return e, clk, sup
}

// blockedFromThePoll runs a cycle of the poll that makes the fix job of issue 42 from af:fix, and waits for the job to be
// blocked, with needs-human on the issue as the worker sets it.
func (e *env) blockedFromThePoll(sup *poll.Supervisor) jobs.Doc {
	e.t.Helper()
	sup.Cycle(ctx, "widgets")
	var id string
	waitFor(e.t, "the poll's job", func() bool {
		page, err := e.Svc.List(ctx, []string{"widgets"}, jobs.ListFilter{})
		if err != nil || len(page.Jobs) == 0 {
			return false
		}
		id = page.Jobs[0].ID
		return true
	})
	doc := e.waitState(id, jobs.StateBlocked)
	e.Hub.AddLabel("widgets", 42, "af:needs-human")
	return doc
}

// labelsJSON is the labels member of the job as the API serves it.
func (e *env) labelsJSON(id string) string {
	e.t.Helper()
	r := e.call("GET", "/api/v1/jobs/"+id, nil)
	var doc struct {
		Labels json.RawMessage `json:"labels"`
	}
	if err := json.Unmarshal(r.Body, &doc); err != nil {
		e.t.Fatal(err)
	}
	return string(doc.Labels)
}

const cleanedUp = `{"set":["af:fix"],"removed":["af:needs-human"],"restore":null,"cleanup":"done"}`

// TestAPersonCancelsABlockedPollFixJobAndAfFixIsPutBackAndHeld_TS_17_65 verifies TS-17-65 and 17-PATH-2.
// Verifies: 17-PATH-2, 17-REQ-6.6, 17-REQ-10.1
func TestAPersonCancelsABlockedPollFixJobAndAfFixIsPutBackAndHeld_TS_17_65(t *testing.T) {
	e, clk, sup := labelEnv(t, fakerunner.New(needsHumanStep()), "af:fix")
	job := e.blockedFromThePoll(sup)

	answer := e.cancel(job.ID)
	if d := answer.Doc(t); cleanupOf(d) != "pending" {
		t.Fatalf("the cancel answered labels %+v, want cleanup pending", d.Labels)
	}
	waitFor(t, "the cleanup", func() bool { return e.cleanupIn(job.ID) == "done" })
	if got := e.issueLabels(); !slices.Equal(got, []string{"af:fix"}) {
		t.Errorf("issue 42 carries %v, want af:fix and no status label", got)
	}
	if got := e.labelsJSON(job.ID); got != cleanedUp {
		t.Errorf("labels = %s, want %s", got, cleanedUp)
	}
	removals := len(e.Hub.RequestsTo("DELETE", "/api/v1/workspaces/widgets/issues/42/labels/af:fix"))
	for range 3 {
		sup.Cycle(ctx, "widgets")
		clk.Advance(time.Minute)
	}
	if page, _ := e.Svc.List(ctx, []string{"widgets"}, jobs.ListFilter{}); len(page.Jobs) != 1 {
		t.Errorf("%d jobs after three cycles, want no new one", len(page.Jobs))
	}
	if n := len(e.Hub.RequestsTo("DELETE", "/api/v1/workspaces/widgets/issues/42/labels/af:fix")); n != removals {
		t.Errorf("the cycles removed af:fix %d times, want none", n-removals)
	}
}

// TestARunnerCancelledBeforeItsHubSessionLeavesAfFixAndNoNeedsHuman_TS_17_66 verifies TS-17-66 and 17-PATH-3.
// Verifies: 17-PATH-3, 17-REQ-5.2
func TestARunnerCancelledBeforeItsHubSessionLeavesAfFixAndNoNeedsHuman_TS_17_66(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	second := fakerunner.Do(func(ctx context.Context, req jobs.Request) (jobs.Result, error) {
		close(started)
		<-ctx.Done()
		<-release
		return jobs.Result{}, ctx.Err()
	})
	e, _, sup := labelEnv(t, fakerunner.New(needsHumanStep(), second), "af:fix")
	job := e.blockedFromThePoll(sup)
	option := "A"
	if _, err := e.Svc.Answer(ctx, job.ID, callerID, jobs.AnswerRequest{Option: &option}); err != nil {
		t.Fatal(err)
	}
	<-started
	before := len(e.issueRequests())
	e.cancel(job.ID)
	time.Sleep(50 * time.Millisecond)
	if r := e.issueRequests()[before:]; len(r) != 0 {
		close(release)
		t.Fatalf("a try ran while the runner was in progress: %v", r)
	}
	close(release)
	waitFor(t, "the cancelled attempt", func() bool {
		d := e.get(job.ID)
		return len(d.Attempts) == 2 && d.Attempts[1].State == jobs.AttemptCancelled
	})
	if c := e.cleanupIn(job.ID); c != "pending" && c != "done" {
		t.Fatalf("cleanup %q once the attempt is recorded, want it marked", c)
	}
	waitFor(t, "the cleanup", func() bool { return e.cleanupIn(job.ID) == "done" })
	if got := e.issueLabels(); !slices.Equal(got, []string{"af:fix"}) {
		t.Errorf("issue 42 carries %v, want af:fix and no af:needs-human", got)
	}
}

// TestACleanupThatMeetsAnUnavailableHubIsRetriedByTheSweepAndCompletes_TS_17_67 verifies TS-17-67 and 17-PATH-4.
// Verifies: 17-PATH-4, 17-REQ-7.2, 17-REQ-8.1
func TestACleanupThatMeetsAnUnavailableHubIsRetriedByTheSweepAndCompletes_TS_17_67(t *testing.T) {
	e, clk, sup := labelEnv(t, fakerunner.New(needsHumanStep()), "af:fix")
	job := e.blockedFromThePoll(sup)
	e.Hub.Fail("/api/v1/workspaces/widgets/issues/42/labels", 503)
	e.cancel(job.ID)
	waitFor(t, "the warning", func() bool { return len(e.warnsFor(job.ID)) == 1 })
	waitFor(t, "the try to end", func() bool { return e.Svc.CleanupsInFlight() == 0 })
	if d := e.get(job.ID); cleanupOf(d) != "pending" || d.Labels.Restore == nil || *d.Labels.Restore != "af:fix" {
		t.Fatalf("labels %+v, want cleanup pending with restore af:fix", d.Labels)
	}
	e.Hub.Clear()
	clk.Advance(time.Minute)
	waitFor(t, "the sweep to complete the cleanup", func() bool { return e.cleanupIn(job.ID) == "done" })
	if got := e.issueLabels(); !slices.Equal(got, []string{"af:fix"}) {
		t.Errorf("issue 42 carries %v", got)
	}
	if n := len(e.warnsFor(job.ID)); n != 1 {
		t.Errorf("%d WARN records, want 1", n)
	}
}

// TestAnUnsubscribeCleansUpItsBlockedJobBeforeRemovingTheSubscription_TS_17_68 verifies TS-17-68 and 17-PATH-5.
// Verifies: 17-PATH-5, 17-REQ-9.2
func TestAnUnsubscribeCleansUpItsBlockedJobBeforeRemovingTheSubscription_TS_17_68(t *testing.T) {
	e, _, sup := labelEnv(t, fakerunner.New(needsHumanStep()), "af:fix")
	job := e.blockedFromThePoll(sup)
	var mu sync.Mutex
	var labelsWhenRemoved []string
	var subscribedAtWrites []bool
	e.Hub.OnRequest(func(r fakehub.Request) {
		if strings.HasPrefix(r.Path, "/api/v1/workspaces/widgets/issues/42/labels") {
			_, err := e.Store.Get(ctx, "widgets")
			mu.Lock()
			subscribedAtWrites = append(subscribedAtWrites, err == nil)
			mu.Unlock()
		}
	})
	r := e.call("DELETE", "/api/v1/workspaces/widgets", nil)
	if r.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", r.Status, r.Body)
	}
	labelsWhenRemoved = e.issueLabels()
	mu.Lock()
	defer mu.Unlock()
	if len(subscribedAtWrites) != 2 || slices.Contains(subscribedAtWrites, false) {
		t.Errorf("the subscription at the label writes: %v, want it there at both", subscribedAtWrites)
	}
	if !slices.Equal(labelsWhenRemoved, []string{"af:fix"}) {
		t.Errorf("issue 42 carries %v, want af:fix and no af:needs-human", labelsWhenRemoved)
	}
	if e.cleanupIn(job.ID) != "done" {
		t.Errorf("cleanup %s when the DELETE answered, want done", e.cleanupIn(job.ID))
	}
	if n := e.count("SELECT count(*) FROM af_subscriptions WHERE slug = 'widgets'"); n != 0 {
		t.Error("the subscription and its token are still there")
	}
}
