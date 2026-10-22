package jobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// cancel posts the cancel of the job as the caller.
func (e *env) cancel(id string) response {
	e.t.Helper()
	return e.call("POST", "/api/v1/jobs/"+id+"/cancel", nil)
}

// createFromLabel makes a job the way a trigger does, with the request label it consumed.
func (e *env) createFromLabel(workspace string, issue int, label string) jobs.Doc {
	e.t.Helper()
	doc, err := e.Svc.Create(ctx, jobs.CreateRequest{
		Workspace: workspace, Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage(fmt.Sprint(issue))},
		Source: jobs.Source{Kind: "label", By: "poll"}, RequestLabel: label,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return doc
}

// lastEvent is the last stored event of the job.
func (e *env) lastEvent(id string) jobs.Event {
	e.t.Helper()
	evs := e.events(id)
	if len(evs) == 0 {
		e.t.Fatalf("job %s has no events", id)
	}
	return evs[len(evs)-1]
}

// jobState reads the data of a job_state event.
func jobState(t *testing.T, ev jobs.Event) (state string, attempt int, atRest bool) {
	t.Helper()
	var data struct {
		State   string `json:"state"`
		Attempt int    `json:"attempt"`
		AtRest  bool   `json:"at_rest"`
	}
	if ev.Type != "job_state" || json.Unmarshal(ev.Data, &data) != nil {
		t.Fatalf("event %d is %s %s, want a job_state", ev.Seq, ev.Type, ev.Data)
	}
	return data.State, data.Attempt, data.AtRest
}

// TestCancelMovesAJobToCancelledAtOnceAndStopsWhatRunsWithTheCancelCause_TS_12_50
// verifies TS-12-50: a cancel of a running, a queued, a backing-off and a blocked job
// each answers 200 with the job cancelled and finished_at set; labels.restore is the
// request label for the jobs that have one and labels is null otherwise; the queued
// attempt and the one in its backoff become cancelled and never reach the runner; and
// the running attempt's context is cancelled with a cancel cause, its row becoming
// cancelled when the runner has returned.
// Verifies: 12-REQ-8.1
func TestCancelMovesAJobToCancelledAtOnceAndStopsWhatRunsWithTheCancelCause_TS_12_50(t *testing.T) {
	clk := fakeclock.New(epoch)
	needsHuman := fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which?", "a", "b")))
	r := fakerunner.New(fakerunner.Block(fakerunner.Done()), fakerunner.TransientFailure(sandboxFailure), needsHuman).Then(fakerunner.Done())
	e := newEnv(t, withRunner(r), withWorkers(3), withClock(clk))
	e.addWorkspace("gadgets")
	e.addWorkspace("sprockets")
	e.Start()

	running := e.create("widgets", jobs.KindFix, 1)
	waitCalls(t, r, 1)
	queued := e.createFromLabel("widgets", 2, "af:fix") // behind the running job
	backoff := e.create("gadgets", jobs.KindFix, 3)
	waitCalls(t, r, 2)
	if !clk.WaitForWaits(1, 10*time.Second) {
		t.Fatal("the transient failure did not go into its backoff")
	}
	blocked := e.createFromLabel("sprockets", 4, "af:fix")
	e.waitState(blocked.ID, jobs.StateBlocked)
	if d := e.get(queued.ID); d.State != jobs.StateQueued {
		t.Fatalf("the queued job is %s", d.State)
	}

	// The running job is cancelled last: once its runner has returned, the job queued behind
	// it would start.
	for _, tc := range []struct {
		name    string
		id      string
		restore bool
	}{{"queued", queued.ID, true}, {"backing off", backoff.ID, false}, {"blocked", blocked.ID, true}, {"running", running.ID, false}} {
		name := tc.name
		resp := e.cancel(tc.id)
		doc := resp.Doc(t)
		if resp.Status != http.StatusOK || doc.State != jobs.StateCancelled || doc.FinishedAt == nil {
			t.Fatalf("%s: status %d, state %s, finished_at %v, want 200, cancelled and finished_at set\n%s", name, resp.Status, doc.State, doc.FinishedAt, resp.Body)
		}
		switch {
		case tc.restore && (doc.Labels == nil || doc.Labels.Restore == nil || *doc.Labels.Restore != "af:fix"):
			t.Errorf("%s: labels = %+v, want restore af:fix", name, doc.Labels)
		case !tc.restore && doc.Labels != nil:
			t.Errorf("%s: labels = %+v, want null for a job that has no request label", name, doc.Labels)
		}
	}

	// The running attempt was told with a cancel; its row follows when the runner returns.
	if cause := r.Calls()[0].Cause(); !jobs.IsCancel(cause) || !errors.Is(cause, jobs.ErrCancelled) {
		t.Errorf("the running attempt's cause is %v, want a cancel", cause)
	}
	waitFor(t, "the running attempt to end", func() bool { return atRest(e.get(running.ID)) })
	if a := e.get(running.ID).Attempts[0]; a.State != jobs.AttemptCancelled || a.FinishedAt == nil {
		t.Errorf("the running attempt is %s, want cancelled", a.State)
	}
	// The queued attempt and the one in its backoff are cancelled and never reach the runner.
	for _, id := range []string{queued.ID, backoff.ID} {
		if a := e.get(id).Attempts[0]; a.State != jobs.AttemptCancelled {
			t.Errorf("job %s: attempt %s, want cancelled", id, a.State)
		}
	}
	clk.Advance(30 * time.Second) // the backoff would be over
	settle()
	if n := len(r.Calls()); n != 3 {
		t.Errorf("the runner had %d calls, want only the 3 before the cancels: nothing cancelled while queued reaches it", n)
	}
	// A job that is not open cannot be cancelled.
	resp := e.cancel(running.ID)
	if code, message := resp.errorOf(t); resp.Status != http.StatusConflict || code != "invalid_state" || !strings.Contains(message, "cancelled") {
		t.Errorf("a second cancel: status %d %s %q, want 409 invalid_state naming cancelled", resp.Status, code, message)
	}
}

// TestWhenTheJobWasCancelledMeanwhileTheAttemptIsCancelledAndLabelsFollowTheRunner_TS_12_37
// verifies TS-12-37: a runner that returns after the job was cancelled has its attempt
// recorded cancelled and the job stays cancelled; the job's labels follow its report;
// labels.restore is cleared when it reports having set the request label and kept when
// it reports nothing; and the last event is a job_state at rest.
// Verifies: 12-REQ-6.2
func TestWhenTheJobWasCancelledMeanwhileTheAttemptIsCancelledAndLabelsFollowTheRunner_TS_12_37(t *testing.T) {
	setLabel := fakerunner.Return(jobs.Result{Envelope: fakerunner.Env(toolio.StatusFailed, fakerunner.WithError("cancelled", "stopped", false)), LabelsSet: []string{"af:fix"}, LabelsRemoved: []string{"af:needs-human"}})
	nothing := fakerunner.Return(jobs.Result{Envelope: fakerunner.Env(toolio.StatusFailed, fakerunner.WithError("cancelled", "stopped", false))})
	r := fakerunner.New(fakerunner.Block(setLabel), fakerunner.Block(nothing))
	e := newEnv(t, withRunner(r))
	e.addWorkspace("gadgets")
	first := e.createFromLabel("widgets", 1, "af:fix")
	e.Start()
	waitCalls(t, r, 1)
	second := e.createFromLabel("gadgets", 2, "af:fix")
	waitCalls(t, r, 2)

	for _, id := range []string{first.ID, second.ID} {
		if resp := e.cancel(id); resp.Status != 200 {
			t.Fatalf("cancel: status %d\n%s", resp.Status, resp.Body)
		}
	}
	a, b := e.waitRest(first.ID), e.waitRest(second.ID)
	for _, d := range []jobs.Doc{a, b} {
		if d.State != jobs.StateCancelled || d.Attempts[0].State != jobs.AttemptCancelled || d.FinishedAt == nil {
			t.Errorf("job %s is %s, attempt %s, want both cancelled", d.ID, d.State, d.Attempts[0].State)
		}
		if state, _, atRestNow := jobState(t, e.lastEvent(d.ID)); state != "cancelled" || !atRestNow {
			t.Errorf("job %s: the last event says %s at rest %v, want cancelled at rest", d.ID, state, atRestNow)
		}
	}
	if a.Labels == nil || !slices.Equal(a.Labels.Set, []string{"af:fix"}) || !slices.Equal(a.Labels.Removed, []string{"af:needs-human"}) || a.Labels.Restore != nil {
		t.Errorf("first job: labels = %+v, want what the runner reported and no restore: it set the request label", a.Labels)
	}
	if b.Labels == nil || len(b.Labels.Set) != 0 || b.Labels.Restore == nil || *b.Labels.Restore != "af:fix" {
		t.Errorf("second job: labels = %+v, want restore af:fix kept: the runner reported nothing", b.Labels)
	}
}

// TestACancelledJobsLastEventIsAJobStateAtRestAndEveryTransitionHasOne_TS_12_43
// verifies TS-12-43: blocked, done, failed and cancelled jobs each end their log with a
// job_state at rest; a job cancelled while its runner still runs says first that it is
// cancelled and not at rest, and once more, at rest, when the runner has returned.
// Verifies: 12-REQ-6.8
func TestACancelledJobsLastEventIsAJobStateAtRestAndEveryTransitionHasOne_TS_12_43(t *testing.T) {
	r := fakerunner.New(
		fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which?", "a"))),
		fakerunner.Done(),
		fakerunner.Failed("sandbox", false),
		fakerunner.Block(fakerunner.Done()),
	)
	e := newEnv(t, withRunner(r))
	var ids []string
	for i := range 4 {
		ids = append(ids, e.create("widgets", jobs.KindFix, i+1).ID)
	}
	e.Start()
	for _, want := range []struct {
		id    string
		state jobs.State
	}{{ids[0], jobs.StateBlocked}, {ids[1], jobs.StateDone}, {ids[2], jobs.StateFailed}} {
		e.waitState(want.id, want.state)
		if state, _, atRestNow := jobState(t, e.lastEvent(want.id)); state != string(want.state) || !atRestNow {
			t.Errorf("job %s: the last event says %s at rest %v, want %s at rest", want.id, state, atRestNow, want.state)
		}
	}

	// The fourth: queued, running, cancelled while the runner runs, at rest.
	waitCalls(t, r, 4)
	var states []string
	var rests []bool
	if resp := e.cancel(ids[3]); resp.Status != 200 {
		t.Fatalf("cancel: %d\n%s", resp.Status, resp.Body)
	}
	e.waitRest(ids[3])
	for _, ev := range e.events(ids[3]) {
		if ev.Type == "job_state" {
			state, _, rest := jobState(t, ev)
			states, rests = append(states, state), append(rests, rest)
		}
	}
	if want := []string{"queued", "running", "cancelled", "cancelled"}; !slices.Equal(states, want) {
		t.Errorf("job_state events say %v, want %v", states, want)
	}
	if want := []bool{false, false, false, true}; !slices.Equal(rests, want) {
		t.Errorf("at_rest is %v, want %v: not at rest while the runner runs", rests, want)
	}
	if evs := e.events(ids[3]); evs[len(evs)-2].Type != "attempt_end" {
		t.Errorf("the event before the last is %s, want attempt_end", evs[len(evs)-2].Type)
	}
	// A job cancelled while it is queued has no attempt that ran: one job_state, at rest.
	idle := newEnv(t)
	queued := idle.create("widgets", jobs.KindFix, 1)
	idle.cancel(queued.ID)
	if state, _, rest := jobState(t, idle.lastEvent(queued.ID)); state != "cancelled" || !rest {
		t.Errorf("a job cancelled while queued: %s at rest %v, want cancelled at rest", state, rest)
	}
}

// TestARunnerThatOverrunsTheGraceHasItsAttemptRecordedCancelledAndKeepsItsPlaceAndSlot_TS_12_51
// verifies TS-12-51: when the runner of a cancelled job has not returned 60 s on the
// clock after the cancel, the attempt is recorded cancelled with a synthesized envelope
// of category cancelled, attempt_end and an at-rest job_state are stored, an ERROR
// names the job and the attempt, the next job of the workspace does not reach the
// runner, and what the runner reports and returns after it returns is dropped.
// Verifies: 12-REQ-8.2
func TestARunnerThatOverrunsTheGraceHasItsAttemptRecordedCancelledAndKeepsItsPlaceAndSlot_TS_12_51(t *testing.T) {
	clk := fakeclock.New(epoch)
	var recorder *jobs.Recorder
	returned := make(chan struct{})
	hold := make(chan struct{})
	r := fakerunner.New(fakerunner.Do(func(_ context.Context, req jobs.Request) (jobs.Result, error) {
		recorder = req.Recorder
		<-hold // it ignores its context
		req.Recorder.Emit("late", map[string]int{"n": 1})
		req.Recorder.Hub("late-session", "late-run", "late-box")
		defer close(returned)
		return jobs.Result{Envelope: fakerunner.Env(toolio.StatusDone, fakerunner.WithSummary("too late")), LabelsSet: []string{"too-late"}}, nil
	}))
	e := newEnv(t, withRunner(r), withWorkers(1), withClock(clk))
	first, second := e.create("widgets", jobs.KindFix, 1), e.create("widgets", jobs.KindFix, 2)
	e.Start()
	waitCalls(t, r, 1)
	if resp := e.cancel(first.ID); resp.Status != 200 {
		t.Fatalf("cancel: %d", resp.Status)
	}
	if !clk.WaitForWaits(1, 10*time.Second) || clk.Waits()[0] != 60*time.Second {
		t.Fatalf("waits = %v, want the 60 s of grace", clk.Waits())
	}
	settle()
	if a := e.get(first.ID).Attempts[0]; a.State != jobs.AttemptRunning {
		t.Fatalf("the attempt is %s before the grace is over, want running: the runner has not returned", a.State)
	}
	clk.Advance(60 * time.Second)

	doc := e.waitRest(first.ID)
	if a := doc.Attempts[0]; a.State != jobs.AttemptCancelled || a.Status == nil || *a.Status != toolio.StatusFailed || a.Error == nil || a.Error.Category != "cancelled" {
		t.Fatalf("attempt = %+v, want cancelled with a failed envelope of the category cancelled", a)
	}
	if doc.State != jobs.StateCancelled || doc.Result == nil || doc.Result.ExitCode != 1 || doc.Result.Error.Category != "cancelled" {
		t.Errorf("job %s with result %+v, want cancelled, exit code 1, category cancelled", doc.State, doc.Result)
	}
	if raw, err := os.ReadFile(e.fileOf(first.ID, 1, "envelope.json")); err != nil || !strings.Contains(string(raw), `"category": "cancelled"`) {
		t.Errorf("envelope.json: %v\n%s", err, raw)
	}
	evs := e.events(first.ID)
	if n := len(evs); n < 2 || evs[n-2].Type != "attempt_end" {
		t.Errorf("the last events are %v, want attempt_end and a job_state", evs)
	}
	if state, _, rest := jobState(t, e.lastEvent(first.ID)); state != "cancelled" || !rest {
		t.Errorf("the last event says %s at rest %v, want cancelled at rest", state, rest)
	}
	logged := false
	for _, rec := range e.Logs.find("ERROR", "") {
		logged = logged || (rec["job"] == first.ID && fmt.Sprint(rec["attempt"]) == "1")
	}
	if !logged {
		t.Errorf("no ERROR names the job and the attempt: %v", e.Logs.records())
	}

	// It still holds its place and its slot.
	settle()
	if len(r.Calls()) != 1 {
		t.Fatalf("the next job reached the runner while the runner of the first had not returned")
	}
	before := e.snapshot(first.ID)
	close(hold)
	<-returned
	waitCalls(t, r, 2)
	if got := r.Calls()[1].JobID; got != second.ID {
		t.Errorf("the second call is %s, want the next job %s once the runner has returned", got, second.ID)
	}
	if after := e.snapshot(first.ID); after != before {
		t.Errorf("what the runner reported or returned late changed the job:\nbefore %s\nafter  %s", before, after)
	}
	if d := e.get(first.ID); d.Hub != nil || d.Labels != nil && len(d.Labels.Set) > 0 {
		t.Errorf("hub %+v, labels %+v after a late report, want them as they were", d.Hub, d.Labels)
	}
	recorder.Emit("later still", 1) // a handle that was kept
	if after := e.snapshot(first.ID); after != before {
		t.Errorf("an event sent by a kept handle changed the job")
	}
}

// snapshot is the state of a job that a late report must not change: its row, its attempts,
// its events and the files of its attempts.
func (e *env) snapshot(id string) string {
	e.t.Helper()
	doc := e.get(id)
	raw, _ := json.Marshal(doc)
	out := string(raw)
	for _, ev := range e.events(id) {
		out += fmt.Sprintf("|%d:%s:%s", ev.Seq, ev.Type, ev.Data)
	}
	for n := 1; n <= doc.AttemptCount; n++ {
		for _, name := range []string{"envelope.json", "report.md"} {
			data, _ := os.ReadFile(e.fileOf(id, n, name))
			out += fmt.Sprintf("|%s:%x", name, data)
		}
	}
	return out
}

// TestAnOverrunningRunnerKeepsTheOnlySlotSoAnotherWorkspaceWaitsToo_TS_12_26 is the
// half of TS-12-26 about an overrun: with count 1, while the runner of a cancelled
// job ignores the cancel past the grace, neither the next job of its workspace nor a
// job of another workspace reaches the runner, until it returns.
// Verifies: 12-REQ-4.4
func TestAnOverrunningRunnerKeepsTheOnlySlotSoAnotherWorkspaceWaitsToo_TS_12_26(t *testing.T) {
	clk := fakeclock.New(epoch)
	r := fakerunner.New(fakerunner.BlockIgnoringContext(fakerunner.Done()))
	e := newEnv(t, withRunner(r), withWorkers(1), withClock(clk))
	e.addWorkspace("gadgets")
	a, b := e.create("widgets", jobs.KindFix, 1), e.create("widgets", jobs.KindFix, 2)
	e.Start()
	waitCalls(t, r, 1)
	e.cancel(a.ID)
	clk.WaitForWaits(1, 10*time.Second)
	clk.Advance(60 * time.Second)
	e.waitRest(a.ID)
	c := e.create("gadgets", jobs.KindFix, 3)
	settle()
	if len(r.Calls()) != 1 {
		t.Fatalf("%d calls while the runner of the cancelled job is still in the runner, want 1: it holds its place and its slot", len(r.Calls()))
	}
	r.Release(0)
	waitCalls(t, r, 3)
	got := []string{r.Calls()[1].JobID, r.Calls()[2].JobID}
	slices.Sort(got)
	if want := []string{b.ID, c.ID}; !slices.Equal(got, []string{min(want[0], want[1]), max(want[0], want[1])}) {
		t.Errorf("the calls after the return are for %v, want %s and %s", got, b.ID, c.ID)
	}
}

// TestHubReferencesAndLabelsShowAsTheRunnerReportedThem_TS_12_13 verifies TS-12-13: a job
// shows the hub references that its runner reported only while the attempt runs;
// labels carries set and removed as reported and restore null; a job with no label
// values has labels null; and a job with a request label that is cancelled while
// queued has labels.restore.
// Verifies: 12-REQ-2.6
func TestHubReferencesAndLabelsShowAsTheRunnerReportedThem_TS_12_13(t *testing.T) {
	reported, release := make(chan struct{}), make(chan struct{})
	r := fakerunner.New(
		fakerunner.Do(func(_ context.Context, req jobs.Request) (jobs.Result, error) {
			req.Recorder.Hub("session-7", "run-7", "box-7")
			close(reported)
			<-release
			return jobs.Result{Envelope: fakerunner.Env(toolio.StatusDone), LabelsSet: []string{"af:done"}, LabelsRemoved: []string{"af:fix"}}, nil
		}),
		fakerunner.Done(),
	)
	e := newEnv(t, withRunner(r))
	first := e.create("widgets", jobs.KindFix, 1)
	second := e.create("widgets", jobs.KindFix, 2)
	waiting := e.createFromLabel("widgets", 3, "af:fix")
	e.Start()
	<-reported

	during := e.get(first.ID)
	if during.Hub == nil || *during.Hub.SessionID != "session-7" || *during.Hub.RunID != "run-7" || *during.Hub.SandboxID != "box-7" {
		t.Errorf("hub = %+v during the run, want session-7, run-7 and box-7", during.Hub)
	}
	if during.Labels != nil {
		t.Errorf("labels = %+v during the run, want null: nothing is reported until the attempt ends", during.Labels)
	}
	// A job with a request label, cancelled while it waits.
	cancelled := e.cancel(waiting.ID).Doc(t)
	if cancelled.Labels == nil || cancelled.Labels.Restore == nil || *cancelled.Labels.Restore != "af:fix" || len(cancelled.Labels.Set) != 0 || len(cancelled.Labels.Removed) != 0 {
		t.Errorf("cancelled while queued: labels = %+v, want only restore af:fix", cancelled.Labels)
	}

	close(release)
	after := e.waitRest(first.ID)
	if after.Hub != nil {
		t.Errorf("hub = %+v after the attempt ended, want null", after.Hub)
	}
	if after.Labels == nil || !slices.Equal(after.Labels.Set, []string{"af:done"}) || !slices.Equal(after.Labels.Removed, []string{"af:fix"}) || after.Labels.Restore != nil {
		t.Errorf("labels = %+v, want set af:done, removed af:fix and no restore", after.Labels)
	}
	if d := e.waitRest(second.ID); d.Labels != nil || d.Hub != nil {
		t.Errorf("a job with no label activity: labels %+v, hub %+v, want null and null", d.Labels, d.Hub)
	}
}

// TestUnsubscribeMarksTheWorkspaceCancelsItsOpenJobsWaitsForRunnersAndClearsTheMark_TS_12_56
// verifies TS-12-56: the DELETE of a workspace that has a queued, a running and a
// blocked job marks it so that a create is a 404, cancels the three with the
// unsubscribe cause, which the runner reads as a cancel, answers 204 after the running
// attempt has returned, and leaves the jobs and their history; and when the removal
// fails the mark is cleared and a create is accepted again.
// Verifies: 12-REQ-8.7
func TestUnsubscribeMarksTheWorkspaceCancelsItsOpenJobsWaitsForRunnersAndClearsTheMark_TS_12_56(t *testing.T) {
	r := fakerunner.New(
		fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which?", "a"))),
		fakerunner.BlockIgnoringContext(fakerunner.Done()), // it returns when it is released, not when it is told
	)
	e := newEnv(t, withRunner(r))
	blocked := e.create("widgets", jobs.KindFix, 1)
	e.Start()
	e.waitState(blocked.ID, jobs.StateBlocked)
	running := e.create("widgets", jobs.KindFix, 2)
	waitCalls(t, r, 2)
	queued := e.create("widgets", jobs.KindFix, 3)

	deleted := make(chan response, 1)
	go func() { deleted <- e.call("DELETE", "/api/v1/workspaces/widgets", nil) }()
	waitFor(t, "the runner to be told", func() bool { return r.Calls()[1].Cause() != nil })
	if cause := r.Calls()[1].Cause(); !jobs.IsCancel(cause) || !errors.Is(cause, jobs.ErrUnsubscribed) {
		t.Errorf("cause = %v, want the unsubscribe, which is a cancel", cause)
	}
	// While the DELETE waits for the runner, nothing new is made for the workspace.
	resp := e.call("POST", "/api/v1/workspaces/widgets/fix", map[string]any{"issue": 99})
	if code, _ := resp.errorOf(t); resp.Status != 404 || code != "not_found" {
		t.Errorf("a create during the DELETE: status %d %s, want 404 not_found", resp.Status, code)
	}
	select {
	case resp := <-deleted:
		t.Fatalf("the DELETE answered %d before the running attempt had returned", resp.Status)
	case <-time.After(150 * time.Millisecond):
	}
	for _, id := range []string{blocked.ID, running.ID, queued.ID} {
		if d := e.get(id); d.State != jobs.StateCancelled {
			t.Errorf("job %s is %s during the DELETE, want cancelled", id, d.State)
		}
	}
	r.ReleaseAll()
	if resp := <-deleted; resp.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d, want 204\n%s", resp.Status, resp.Body)
	}
	for _, id := range []string{blocked.ID, running.ID, queued.ID} {
		d := e.waitRest(id)
		if d.State != jobs.StateCancelled || len(d.Attempts) == 0 {
			t.Errorf("job %s is %s with %d attempts after the DELETE, want it cancelled and its history there", id, d.State, len(d.Attempts))
		}
	}
	if d := e.get(running.ID); d.Attempts[0].State != jobs.AttemptCancelled {
		t.Errorf("the running attempt is %s, want cancelled", d.Attempts[0].State)
	}
	if got := e.call("GET", "/api/v1/workspaces/widgets", nil); got.Status != 404 {
		t.Errorf("GET of the workspace = %d, want 404: the subscription is gone", got.Status)
	}
	if state, _, rest := jobState(t, e.lastEvent(queued.ID)); state != "cancelled" || !rest {
		t.Errorf("the queued job's last event says %s at rest %v", state, rest)
	}

	// A removal that fails leaves the subscription, and clears the mark.
	e.subscribe("widgets")
	if _, err := e.DB.Exec(`CREATE TRIGGER no_removal BEFORE DELETE ON af_subscriptions BEGIN SELECT RAISE(ABORT, 'removal refused'); END`); err != nil {
		t.Fatal(err)
	}
	if resp := e.call("DELETE", "/api/v1/workspaces/widgets", nil); resp.Status != http.StatusInternalServerError {
		t.Errorf("DELETE with a removal that fails = %d, want 500", resp.Status)
	}
	if resp := e.call("POST", "/api/v1/workspaces/widgets/fix", map[string]any{"issue": 100}); resp.Status != http.StatusAccepted {
		t.Errorf("a create after a removal that failed = %d, want 202: the mark is cleared\n%s", resp.Status, resp.Body)
	}
}

// TestAnAttemptThatReturnsAfterACancelWithoutAnEnvelopeGetsACancelledOne_TS_12_42 is the
// case of TS-12-42 about a cancel: a runner that returns, after the job was cancelled,
// an error, nothing, or an envelope that cannot be used, has its attempt cancelled with
// an envelope of the category cancelled and exit code 1, and the job stays cancelled.
// Verifies: 12-REQ-6.7
func TestAnAttemptThatReturnsAfterACancelWithoutAnEnvelopeGetsACancelledOne_TS_12_42(t *testing.T) {
	for name, then := range map[string]fakerunner.Step{
		"the cause of the cancel":           {},
		"a plain error":                     fakerunner.Fail(errors.New("stopped")),
		"nothing":                           fakerunner.Return(jobs.Result{}),
		"a failure of the forge":            fakerunner.Fail(&toolio.Failure{Category: toolio.CategoryForge, Message: "hung up", Retryable: true}),
		"something that is not an envelope": fakerunner.Envelope([]byte("{")),
		"a transient failure":               fakerunner.TransientFailure(sandboxFailure),
	} {
		t.Run(name, func(t *testing.T) {
			r := fakerunner.New(fakerunner.Block(then))
			e := newEnv(t, withRunner(r))
			job := e.create("widgets", jobs.KindFix, 1)
			e.Start()
			waitCalls(t, r, 1)
			e.cancel(job.ID)
			doc := e.waitRest(job.ID)
			a := doc.Attempts[0]
			if doc.State != jobs.StateCancelled || a.State != jobs.AttemptCancelled || a.Error == nil || a.Error.Category != "cancelled" || a.ExitCode == nil || *a.ExitCode != 1 {
				t.Errorf("job %s, attempt %s %+v, want cancelled, with an error of the category cancelled and exit code 1", doc.State, a.State, a.Error)
			}
			if raw, err := os.ReadFile(e.fileOf(job.ID, 1, "envelope.json")); err != nil || !strings.Contains(string(raw), `"category": "cancelled"`) {
				t.Errorf("envelope.json: %v\n%s", err, raw)
			}
			// It ended at once, and not by way of a failed write that the queue retried.
			if recs := e.Logs.find("WARN", ""); len(recs) != 0 {
				t.Errorf("a WARN was logged: %v", recs)
			}
			if doc.Attempts[0].TransientFailures != 0 {
				t.Errorf("a failure after a cancel was counted as transient: %d", doc.Attempts[0].TransientFailures)
			}
		})
	}
}

// TestWhatARunnerDoesAfterTheGraceIsNotAnErrorOfTheService checks that an attempt that
// was recorded cancelled when the grace ran out is left alone when its runner returns:
// a runner that panics then is not logged as a panic, since nothing it does counts.
// Verifies: 12-REQ-5.5
func TestWhatARunnerDoesAfterTheGraceIsNotAnErrorOfTheService(t *testing.T) {
	clk := fakeclock.New(epoch)
	hold, returned := make(chan struct{}), make(chan struct{})
	r := fakerunner.New(fakerunner.Do(func(context.Context, jobs.Request) (jobs.Result, error) {
		<-hold
		close(returned)
		panic("late bang")
	}))
	e := newEnv(t, withRunner(r), withClock(clk))
	job := e.create("widgets", jobs.KindFix, 1)
	e.Start()
	waitCalls(t, r, 1)
	e.cancel(job.ID)
	clk.WaitForWaits(1, 10*time.Second)
	clk.Advance(cancelGraceForTests)
	e.waitRest(job.ID)
	close(hold)
	<-returned
	settle()
	if recs := e.Logs.find("ERROR", "panic"); len(recs) != 0 {
		t.Errorf("a panic after the grace was logged: %v", recs)
	}
	if doc := e.get(job.ID); doc.State != jobs.StateCancelled || doc.Attempts[0].Error == nil || doc.Attempts[0].Error.Category != "cancelled" {
		t.Errorf("job %s, error %+v, want the attempt left as the grace recorded it", doc.State, doc.Attempts[0].Error)
	}
}

const cancelGraceForTests = 60 * time.Second
