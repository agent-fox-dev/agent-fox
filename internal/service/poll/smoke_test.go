package poll

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
)

// The smoke tests follow the paths of the spec through the real code: the HTTP API, the jobs layer on a state file, the fake
// runner, the poll's cycle and its records, the hub forge client and the fake hub. The end to end test with the real worker is
// in internal/worker, which has the harness that runs it (TS-15-65).

// TestALabelAddedToAnIssueWithARunningApiFixJobIsAbsorbed_TS_15_66 verifies TS-15-66 and 15-PATH-2: POST /fix makes a job
// that runs, af:fix is added to the issue, and a cycle makes no second job, removes the label through hub and logs the
// absorbed label with the workspace, the kind, the issue and the job.
// Verifies: 15-PATH-2, 15-REQ-5.1
func TestALabelAddedToAnIssueWithARunningApiFixJobIsAbsorbed_TS_15_66(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))))
	e.issue(42)
	status, body := e.call("POST", "/api/v1/workspaces/"+slug+"/fix", `{"issue": 42}`)
	if status != http.StatusAccepted {
		t.Fatalf("POST fix = %d %s", status, body)
	}
	id := e.jobList()[0].ID
	e.waitState(id, jobs.StateRunning)
	e.Hub.AddLabel(slug, 42, "af:fix")

	e.cycle()

	if n := len(e.jobList()); n != 1 {
		t.Errorf("%d jobs, want the one of the API", n)
	}
	if got := e.removals(); !slices.Equal(got, []string{"remove(42,af:fix)"}) {
		t.Errorf("removals %v, want one of af:fix from 42", got)
	}
	infos := e.Logs.find("INFO", "absorbed")
	if len(infos) != 1 || infos[0]["workspace"] != slug || infos[0]["kind"] != "fix" || infos[0]["issue"] != float64(42) || infos[0]["job"] != id {
		t.Errorf("the INFO records are %v, want one that names the workspace, the kind, the issue and the job %s", infos, id)
	}
	if len(e.labelsOf(42)) != 0 {
		t.Errorf("the label is still on the issue: %v", e.labelsOf(42))
	}
}

// TestALabelTheCancelPutBackIsHeldReleasedAfterAManualRemovalAndThenMakesOneNewJob_TS_15_67 verifies TS-15-67 and
// 15-PATH-3: the poll makes a fix job for 42 and takes the label; the job is cancelled through the API while its runner
// holds it, the label is put back and the runner returns reporting it as set; cycles make no job and remove nothing; a person
// removes the label; 5 minutes pass and a complete listing without 42 releases the hold; and adding the label again makes
// exactly one new job.
// Verifies: 15-PATH-3, 15-REQ-7.4
func TestALabelTheCancelPutBackIsHeldReleasedAfterAManualRemovalAndThenMakesOneNewJob_TS_15_67(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Block(cancelledResult())).Then(fakerunner.Block(fakerunner.Step{}))))
	e.takenByThePoll()
	id := e.jobList()[0].ID

	if status, body := e.call("POST", "/api/v1/jobs/"+id+"/cancel", ""); status != http.StatusOK {
		t.Fatalf("cancel = %d %s", status, body)
	}
	e.Hub.AddLabel(slug, 42, "af:fix") // the cancel cleanup of the worker puts it back
	e.runnerReturned(id)

	for i := range 3 {
		e.cycle()
		if e.jobsFor(42) != 1 || e.removalsOf(42) != 1 || !slices.Contains(e.labelsOf(42), "af:fix") {
			t.Fatalf("during the hold, cycle %d: jobs %d, removals %d, labels %v; want no job made and no removal", i+1, e.jobsFor(42), e.removalsOf(42), e.labelsOf(42))
		}
		e.Clock.Advance(time.Minute)
	}

	e.Hub.RemoveLabel(slug, 42, "af:fix") // a person takes the label off
	e.Clock.Advance(5 * time.Minute)
	e.cycle() // a complete listing without 42, requested long enough after the runner returned
	if _, ok := e.held(42); ok {
		t.Fatalf("the hold is not released: %+v", e.recordsOf())
	}

	e.Hub.AddLabel(slug, 42, "af:fix") // and asks again
	e.cycle()
	if e.jobsFor(42) != 2 {
		t.Errorf("%d jobs for 42 after the label was added again, want exactly one new job: 2", e.jobsFor(42))
	}
	e.cycle()
	if e.jobsFor(42) != 2 {
		t.Errorf("%d jobs for 42 after another cycle, want 2", e.jobsFor(42))
	}
}

// TestAFailedRemovalIsOwedAndTheLabelIsTakenOnceNotTwice_TS_15_68 verifies TS-15-68 and 15-PATH-4: hub answers the first
// removal of af:fix from 42 with 503, so the cycle makes the job, leaves the removal owed and logs a WARN; the job ends
// done; the next cycle removes the label and makes no job, and the record is dropped; the cycle after it makes nothing.
// Verifies: 15-PATH-4, 15-REQ-6.6
func TestAFailedRemovalIsOwedAndTheLabelIsTakenOnceNotTwice_TS_15_68(t *testing.T) {
	e := newEnv(t)
	e.issue(42, "af:fix")
	e.Hub.Inject("DELETE", removalPath(42, "af:fix"), fakehub.Fault{Status: 503, Once: true})

	e.cycle()

	got := e.jobList()
	if len(got) != 1 {
		t.Fatalf("%d jobs after the first cycle, want one", len(got))
	}
	if warns := e.Logs.level("WARN"); len(warns) != 1 {
		t.Errorf("%d warnings after the failed removal, want exactly one: %v", len(warns), warns)
	}
	if recs := e.recordsOf(); len(recs) != 1 || !recs[0].Owed || recs[0].JobID != got[0].ID {
		t.Errorf("records %+v, want one with the removal owed and the job", recs)
	}
	e.waitState(got[0].ID, jobs.StateDone)

	e.cycle()

	if len(e.jobList()) != 1 || len(e.labelsOf(42)) != 0 || len(e.removals()) != 2 {
		t.Errorf("jobs %d, labels %v, removals %v; want the label removed by the second removal and no second job", len(e.jobList()), e.labelsOf(42), e.removals())
	}
	if recs := e.recordsOf(); len(recs) != 0 {
		t.Errorf("records %+v, want the record dropped", recs)
	}
	e.cycle()
	if len(e.jobList()) != 1 || len(e.removals()) != 2 {
		t.Errorf("the cycle after made %d jobs and %d removals, want nothing", len(e.jobList()), len(e.removals()))
	}
}

// TestALabelAddedToIssue42BecomesAJobWithinASecondThroughTheRunningSupervisor_TS_18_67 verifies TS-18-67 and 18-PATH-1: the
// supervisor runs with its stream, hub's SSE reader reads the event of af:fix on 42 from the fake hub, and within a second
// one fix job of the event exists, with its poll record, the id of the event recorded, and one removal of af:fix.
// Verifies: 18-PATH-1, 18-REQ-6.3
func TestALabelAddedToIssue42BecomesAJobWithinASecondThroughTheRunningSupervisor_TS_18_67(t *testing.T) {
	e := streaming(t)
	e.issue(42, "af:fix")
	sent := time.Now()
	e.labeled("evt-1", 42, "af:fix")
	j := e.jobOf(42)
	if took := time.Since(sent); took > time.Second {
		t.Errorf("the job came %v after the event, want within a second", took)
	}
	doc, err := e.Jobs.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Source != (jobs.Source{Kind: "event", By: "evt-1"}) || *doc.RequestLabel != "af:fix" || string(doc.Input.Issue) != "42" {
		t.Errorf("the job is %+v", doc)
	}
	waitFor(t, "the removal and the id", func() bool { return e.removalsOf(42) == 1 && e.taken("evt-1") })
	if recs, _ := NewRecords(e.DB).Find(ctx, slug, 42, "af:fix"); len(recs) != 1 || recs[0].JobID != j.ID {
		t.Errorf("the poll records are %+v, want the one of the job", recs)
	}
}

// TestClosingAnIssueWithABlockedJobRunsTheCleanupAndLaterRequestsAreNew_TS_18_69 verifies TS-18-69 and 18-PATH-3.
// Verifies: 18-PATH-3, 18-REQ-9.2
func TestClosingAnIssueWithABlockedJobRunsTheCleanupAndLaterRequestsAreNew_TS_18_69(t *testing.T) {
	e := streaming(t, withCleanup(), withRunner(fakerunner.New(blockedStep()).Then(fakerunner.Block(fakerunner.Step{}))))
	e.issue(43, "af:triage")
	e.labeled("t43", 43, "af:triage")
	triage := e.jobOf(43).ID
	e.waitState(triage, jobs.StateBlocked)
	e.Hub.AddLabel(slug, 43, "af:needs-human")
	e.Hub.AddLabel(slug, 43, "af:landed") // of an earlier fix job
	e.Hub.Delay("/api/v1/workspaces/widgets/issues/43/labels/af:needs-human", 200*time.Millisecond)
	e.Hub.SetIssueState(slug, 43, "closed")
	e.closedEvent("c43", 43)
	waitFor(t, "the cancel", func() bool { return e.stateOf(triage) == jobs.StateCancelled })
	seenPending := e.cleanupOf(triage) == "pending"
	waitFor(t, "the cleanup", func() bool { return e.cleanupOf(triage) == "done" })
	doc, _ := e.Jobs.Get(ctx, triage)
	if !seenPending || doc.CancelCause == nil || *doc.CancelCause != "issue_closed" || doc.Labels.Restore != nil {
		t.Errorf("pending seen %v, cause %v, restore %v: want the cleanup pending then done, issue_closed and restore null", seenPending, doc.CancelCause, doc.Labels.Restore)
	}
	if got := e.labelsOf(43); !slices.Equal(got, []string{"af:landed"}) {
		t.Errorf("issue 43 carries %v, want af:needs-human removed and af:landed kept", got)
	}
	e.Hub.SetIssueState(slug, 43, "open")
	e.Hub.AddLabel(slug, 43, "af:fix")
	e.cycle()
	if _, ok := e.held(43); ok {
		t.Error("the record of the closed issue's job holds")
	}
	if e.jobsFor(43) != 2 {
		t.Errorf("%d jobs for 43 after the cycle, want one new fix job", e.jobsFor(43))
	}
}

// TestALabelAddedWhileTheStreamIsDownIsMadeIntoOneJobByThePoll_TS_18_70 verifies TS-18-70 and 18-PATH-4.
// Verifies: 18-PATH-4
func TestALabelAddedWhileTheStreamIsDownIsMadeIntoOneJobByThePoll_TS_18_70(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))))
	e.started() // the first cycle runs at once, and the next one interval (5 minutes) later
	e.conns(slug, 1)
	waitFor(t, "the first cycle", func() bool { return len(e.Hub.ListRequests(slug)) == 2 })
	e.Hub.Heartbeat(slug)
	settle()
	e.Hub.Fail("/api/v1/events", 503)
	e.Hub.EndEvents(slug)
	e.issue(45, "af:fix") // no event is sent for it
	for range 4 {         // the retries after 1, 1, 2 and 4 s; the clock moves on in steps until each comes
		n := len(e.Hub.EventsRequests())
		waitFor(t, "an attempt", func() bool {
			if len(e.Hub.EventsRequests()) > n {
				return true
			}
			e.Clock.Advance(500 * time.Millisecond)
			time.Sleep(5 * time.Millisecond)
			return false
		})
	}
	waitFor(t, "the WARN", func() bool { return len(e.Logs.find("WARN", "open the event stream")) >= 1 })
	e.Clock.Advance(5 * time.Minute)
	waitFor(t, "the cycle's job", func() bool { return e.jobsFor(45) == 1 })
	j := e.jobOf(45)
	if j.SourceKind != "label" {
		t.Errorf("the job's source is %s, want the label's", j.SourceKind)
	}
	waitFor(t, "the removal", func() bool { return !slices.Contains(e.labelsOf(45), "af:fix") })
	if got := e.Logs.find("WARN", "open the event stream"); len(got) != 1 {
		t.Errorf("%d WARN records of the connects, want 1: %v", len(got), got)
	}
}

// TestAPersonAnswersABlockedFixJobFromTheIssue_TS_19_58 verifies TS-19-58 and 19-PATH-1: the poll's cycle, the real jobs
// layer and store, the fake hub, the fake runner and clock. A person comments below the question of issue 42's blocked fix
// job and adds af:fix again; one cycle answers the job, removes the label, and the runner of attempt 2 gets the answer.
// Verifies: 19-PATH-1, 19-REQ-2.1
func TestAPersonAnswersABlockedFixJobFromTheIssue_TS_19_58(t *testing.T) {
	r := answering(1)
	e := newEnv(t, withRunner(r))
	id := e.blockFix(42)
	e.comment(42, "alice", "use the greeting from the README")
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.cycle()
	d := e.doc(id)
	if len(d.Attempts) != 2 || d.Attempts[1].Trigger != (jobs.Trigger{Kind: jobs.TriggerAnswer, By: "label af:fix"}) ||
		d.Attempts[1].Answer == nil || !strings.Contains(d.Attempts[1].Answer.Text, "use the greeting from the README") {
		t.Fatalf("the attempts are %+v, want attempt 2 answered by the label with the comment", d.Attempts)
	}
	if len(e.Hub.RequestsTo("DELETE", removalPath(42, "af:fix"))) != 1 || e.hasLabel(42, "af:fix") {
		t.Errorf("af:fix on 42: %v, want it removed through hub", e.labelsOf(42))
	}
	waitFor(t, "attempt 2's runner", func() bool { return len(r.Calls()) == 2 })
	if a := r.Calls()[1].Answers; len(a) != 1 || !strings.Contains(a[0].Text, "use the greeting from the README") {
		t.Errorf("the runner was given the answers %+v", a)
	}
}

// TestTheStreamAnswersABlockedJobAndAStalePollListingDoesNothing_TS_19_59 verifies TS-19-59 and 19-PATH-2: the stream
// answers issue 42's blocked fix job with evt-7 while a poll listing asked for before it is still to be handled, and the
// fake runner ends attempt 2 blocked at once: the listing, which still shows 42 under af:fix, adds no attempt and posts no
// reply. A retry by the stream whose attempt fails at once, and a job of the API answered by the stream, behave the same.
// Verifies: 19-PATH-2, 19-REQ-1.8
func TestTheStreamAnswersABlockedJobAndAStalePollListingDoesNothing_TS_19_59(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state jobs.State
		src   jobs.Source
		label string
	}{
		{"an answer", jobs.StateBlocked, jobs.Source{Kind: "label", By: "label af:fix"}, "af:fix"},
		{"a retry", jobs.StateFailed, jobs.Source{Kind: "label", By: "label af:fix"}, "af:fix"},
		{"a job of the API", jobs.StateBlocked, jobs.Source{Kind: "api", By: "user-1"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := blockedStep()
			if tc.state == jobs.StateFailed {
				step = fakerunner.Failed("sandbox", false)
			}
			var e *env
			stale := false
			e = streaming(t, withRunner(fakerunner.New(step, step).Then(fakerunner.Block(fakerunner.Step{}))), withBeforeRequest(func(r *http.Request) {
				if stale && strings.Contains(r.URL.RawQuery, "labels=af%3Afix") {
					stale = false
					e.labeled("evt-7", 42, "af:fix")
					waitFor(t, "the stream's outcome", func() bool { return e.taken("evt-7") })
				}
			}))
			e.issue(42)
			doc, err := e.Jobs.Create(ctx, jobs.CreateRequest{Workspace: slug, Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage("42")}, Source: tc.src, RequestLabel: tc.label})
			if err != nil {
				t.Fatal(err)
			}
			e.waitState(doc.ID, tc.state)
			kind := map[jobs.State]string{jobs.StateBlocked: "question", jobs.StateFailed: "failure"}[tc.state]
			e.comment(42, "agent-fox", fmt.Sprintf("<!-- agent-fox job=%s attempt=1 kind=%s -->", doc.ID, kind))
			e.comment(42, "alice", "here it is")
			e.Hub.AddLabel(slug, 42, "af:fix")
			comments := e.comments(42)
			stale = true
			e.cycle()
			e.waitState(doc.ID, tc.state) // attempt 2 ended at once
			d := e.doc(doc.ID)
			want := map[jobs.State]jobs.TriggerKind{jobs.StateBlocked: jobs.TriggerAnswer, jobs.StateFailed: jobs.TriggerRetry}[tc.state]
			if len(d.Attempts) != 2 || d.Attempts[1].Trigger != (jobs.Trigger{Kind: want, By: "evt-7"}) {
				t.Errorf("the attempts are %+v, want one more, by evt-7", d.Attempts)
			}
			if e.comments(42) != comments {
				t.Errorf("%d comments were posted, want none", e.comments(42)-comments)
			}
		})
	}
}

// TestAPersonRetriesAFailedFixJobWithContextFromTheIssue_TS_19_60 verifies TS-19-60 and 19-PATH-3.
// Verifies: 19-PATH-3, 19-REQ-4.4
func TestAPersonRetriesAFailedFixJobWithContextFromTheIssue_TS_19_60(t *testing.T) {
	r := failingRuns(1)
	e := newEnv(t, withRunner(r))
	id := e.failFix(42)
	e.comment(42, "bob", "the log is in CI run 812")
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.cycle()
	d := e.doc(id)
	if len(d.Attempts) != 2 || d.Attempts[1].Trigger != (jobs.Trigger{Kind: jobs.TriggerRetry, By: "label af:fix"}) ||
		len(d.Attempts[1].Context) != 1 || !strings.Contains(d.Attempts[1].Context[0], "CI run 812") {
		t.Fatalf("the attempts are %+v, want attempt 2 retried by the label with the comment as its context", d.Attempts)
	}
	waitFor(t, "attempt 2's runner", func() bool { return len(r.Calls()) == 2 })
	if c := r.Calls()[1].Context; len(c) == 0 || !strings.Contains(c[len(c)-1], "CI run 812") {
		t.Errorf("the runner's context is %q, want it to end with the comment", c)
	}
	if e.hasLabel(42, "af:fix") || e.jobsFor(42) != 1 {
		t.Errorf("af:fix %v and %d jobs, want the label removed and no new job", e.labelsOf(42), e.jobsFor(42))
	}
}

// TestNoAnswerFoundIsRepliedThenACutThreadIsRepliedWithAfAndTheAPI_TS_19_61 verifies TS-19-61 and 19-PATH-4.
// Verifies: 19-PATH-4, 19-REQ-5.1, 19-REQ-5.3
func TestNoAnswerFoundIsRepliedThenACutThreadIsRepliedWithAfAndTheAPI_TS_19_61(t *testing.T) {
	e := newEnv(t, withRunner(answering(1)))
	id := e.blockFix(42)
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.cycle()
	if !strings.Contains(e.lastComment(42), "kind=no_answer") || e.hasLabel(42, "af:fix") {
		t.Fatalf("the last comment is %q and af:fix %v, want the no_answer reply and the label removed", e.lastComment(42), e.labelsOf(42))
	}
	e.comment(42, "alice", "use the greeting from the README")
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.cutRead(42)
	e.cycle()
	last := e.lastComment(42)
	if !strings.Contains(last, "kind=thread_cut") || !strings.Contains(last, "af jobs answer "+id) || !strings.Contains(last, "POST /api/v1/jobs/"+id+"/answer") {
		t.Errorf("the last comment is %q, want the thread_cut reply naming af and the API", last)
	}
	if len(e.Logs.level("WARN")) != 1 || e.hasLabel(42, "af:fix") || len(e.doc(id).Attempts) != 1 {
		t.Errorf("%d WARN records, af:fix %v, %d attempts: want one WARN, the label removed and no attempt", len(e.Logs.level("WARN")), e.labelsOf(42), len(e.doc(id).Attempts))
	}
}
