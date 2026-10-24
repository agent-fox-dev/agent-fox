package poll

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
)

// closedEvent sends hub.forge.issue.closed for the issue.
func (e *env) closedEvent(id string, issue int) {
	e.Hub.SendEvent(slug, fakehub.HubEvent{ID: id, Type: fakehub.EventIssueClosed, Issue: issue})
}

// labelJob makes a job of the kind for the issue, as the poll does for its label, and returns its id.
func (e *env) labelJob(kind jobs.Kind, issue int) string {
	e.t.Helper()
	doc, err := e.Jobs.Create(ctx, jobs.CreateRequest{Workspace: slug, Kind: kind, Body: jobs.Body{Issue: json.RawMessage(fmt.Sprint(issue))},
		Source: jobs.Source{Kind: "label", By: "label af:" + string(kind)}, RequestLabel: "af:" + string(kind)})
	if err != nil {
		e.t.Fatal(err)
	}
	return doc.ID
}

// stateOf is the state of the job.
func (e *env) stateOf(id string) jobs.State {
	e.t.Helper()
	s, err := e.Jobs.Standing(ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return s.State
}

// TestAClosedEventForAnIssueWithNoOpenJobCostsNothing_TS_18_53 verifies TS-18-53.
// Verifies: 18-REQ-8.1
func TestAClosedEventForAnIssueWithNoOpenJobCostsNothing_TS_18_53(t *testing.T) {
	e := streaming(t)
	before, records := e.Hub.Total(), len(e.Logs.records())
	e.closedEvent("c70", 70)
	settle()
	if e.Hub.Total() != before || e.taken("c70") || len(e.Logs.records()) != records {
		t.Errorf("hub had %d requests, taken %v, %d records: want none", e.Hub.Total()-before, e.taken("c70"), len(e.Logs.records())-records)
	}
	// An id taken already is ignored before the look.
	e.issue(71)
	id := e.labelJob(jobs.KindFix, 71)
	if err := NewRecords(e.DB).TakeEvent(ctx, slug, "c71", e.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	e.closedEvent("c71", 71)
	settle()
	if e.Hub.Total() != before || e.stateOf(id) == jobs.StateCancelled {
		t.Errorf("a taken id was acted on")
	}
}

// TestAClosedEventForAnIssueHubReadsOpenCancelsNothing_TS_18_54 verifies TS-18-54.
// Verifies: 18-REQ-8.2
func TestAClosedEventForAnIssueHubReadsOpenCancelsNothing_TS_18_54(t *testing.T) {
	e := streaming(t, withRunner(fakerunner.New(fakerunner.Block(fakerunner.Step{}))))
	e.issue(71)
	running := e.labelJob(jobs.KindTriage, 71)
	waitFor(t, "the triage to run", func() bool { return e.stateOf(running) == jobs.StateRunning })
	queued := e.labelJob(jobs.KindFix, 71)
	e.closedEvent("c71", 71)
	waitFor(t, "the event to be decided", func() bool { return e.taken("c71") })
	if e.stateOf(running) != jobs.StateRunning || e.stateOf(queued) != jobs.StateQueued {
		t.Errorf("the jobs are %s and %s, want them running and queued", e.stateOf(running), e.stateOf(queued))
	}
}

// TestAClosedEventCancelsEveryOpenJobOfTheIssue_TS_18_55 verifies TS-18-55.
// Verifies: 18-REQ-8.3
func TestAClosedEventCancelsEveryOpenJobOfTheIssue_TS_18_55(t *testing.T) {
	e := streaming(t, withRunner(fakerunner.New(blockedStep()).Then(fakerunner.Block(fakerunner.Step{}))))
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 1})
	e.issue(72)
	triage := e.labelJob(jobs.KindTriage, 72)
	e.waitState(triage, jobs.StateBlocked)
	e.labelJob(jobs.KindFix, 1) // holds the workspace, so that the fix of 72 stays queued
	fix := e.labelJob(jobs.KindFix, 72)
	e.Hub.SetIssueState(slug, 72, "closed")
	e.closedEvent("c72", 72)
	waitFor(t, "the cancels", func() bool { return e.stateOf(triage) == jobs.StateCancelled && e.stateOf(fix) == jobs.StateCancelled })
	for _, id := range []string{triage, fix} {
		if s, _ := e.Jobs.Standing(ctx, id); s.CancelCause != "issue_closed" {
			t.Errorf("%s: cancel cause %q, want issue_closed", id, s.CancelCause)
		}
	}
	waitFor(t, "the INFO", func() bool { return len(e.Logs.find("INFO", "issue was closed")) == 1 })
	rec := e.Logs.find("INFO", "issue was closed")[0]
	if rec["workspace"] != slug || rec["issue"] != float64(72) || !strings.Contains(fmt.Sprint(rec["jobs"]), triage) || !strings.Contains(fmt.Sprint(rec["jobs"]), fix) {
		t.Errorf("the record is %v", rec)
	}
}

// TestAnUnreadableIssueOnAClosedEventCancelsNothing_TS_18_56 verifies TS-18-56.
// Verifies: 18-REQ-8.4
func TestAnUnreadableIssueOnAClosedEventCancelsNothing_TS_18_56(t *testing.T) {
	e := streaming(t)
	e.issue(73)
	id := e.labelJob(jobs.KindFix, 73)
	e.Hub.Fail("/api/v1/workspaces/widgets/issues/73", 503)
	e.closedEvent("c1", 73)
	e.closedEvent("c2", 73)
	waitFor(t, "both reads", func() bool { return len(e.Hub.RequestsTo("GET", "/api/v1/workspaces/widgets/issues/73")) == 2 })
	settle()
	if e.stateOf(id) == jobs.StateCancelled || e.taken("c1") || e.taken("c2") {
		t.Errorf("the job is %s, taken %v %v: want nothing cancelled or recorded", e.stateOf(id), e.taken("c1"), e.taken("c2"))
	}
	if n := len(e.Logs.find("WARN", "left to the poll")); n != 1 {
		t.Errorf("%d WARN records, want 1", n)
	}
}

// TestAReopenedAndRelabelledIssueMakesANewJob_TS_18_66 verifies TS-18-66.
// Verifies: 18-REQ-9.7
func TestAReopenedAndRelabelledIssueMakesANewJob_TS_18_66(t *testing.T) {
	for _, by := range []string{"an event", "a cycle"} {
		t.Run(by, func(t *testing.T) {
			e := streaming(t, withRunner(fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))))
			e.issue(42, "af:fix")
			e.labeled("first", 42, "af:fix")
			first := e.jobOf(42).ID
			waitFor(t, "the label taken", func() bool { return e.removalsTo(42, "af:fix") == 1 })
			e.Hub.SetIssueState(slug, 42, "closed")
			e.closedEvent("closed", 42)
			waitFor(t, "the cancel", func() bool { return e.stateOf(first) == jobs.StateCancelled })
			e.runnerReturned(first)
			e.Hub.SetIssueState(slug, 42, "open")
			e.Hub.AddLabel(slug, 42, "af:fix")
			if by == "an event" {
				e.labeled("again", 42, "af:fix")
				waitFor(t, "a new job", func() bool { return e.jobsFor(42) == 2 })
			} else {
				e.cycle()
				if e.jobsFor(42) != 2 {
					t.Fatalf("%d jobs for 42 after the cycle, want a new one", e.jobsFor(42))
				}
			}
			waitFor(t, "the label taken again", func() bool { return !slices.Contains(e.labelsOf(42), "af:fix") })
			if _, ok := e.held(42); ok {
				t.Error("a record holds the label of the closed issue's job")
			}
		})
	}
}

// TestAStaleReadingOfZeroStopsAClosedEvent_TS_18_23 verifies the closed event of TS-18-23.
// Verifies: 18-REQ-4.3
func TestAStaleReadingOfZeroStopsAClosedEvent_TS_18_23(t *testing.T) {
	e := streaming(t)
	e.issue(42)
	id := e.labelJob(jobs.KindFix, 42)
	for range 11 {
		e.Hub.Heartbeat(slug)
		settle()
		e.Clock.Advance(30 * time.Second)
	}
	e.setVar("AF_POLL_INTERVAL", "0")
	e.Hub.SetIssueState(slug, 42, "closed")
	e.closedEvent("c42", 42)
	e.conns(slug, 0)
	if e.stateOf(id) == jobs.StateCancelled || e.taken("c42") || len(e.forgeRequests()) != 0 {
		t.Errorf("the job is %s, taken %v, forge requests %v: want nothing done", e.stateOf(id), e.taken("c42"), e.forgeRequests())
	}
}

// TestADecidedClosedEventLeavesOneRowAndARepeatDoesNothing_TS_18_45 verifies the closed event of TS-18-45.
// Verifies: 18-REQ-7.1
func TestADecidedClosedEventLeavesOneRowAndARepeatDoesNothing_TS_18_45(t *testing.T) {
	e := streaming(t)
	e.issue(42)
	id := e.labelJob(jobs.KindFix, 42)
	e.Hub.SetIssueState(slug, 42, "closed")
	e.closedEvent("c42", 42)
	waitFor(t, "the row", func() bool { return e.taken("c42") })
	if e.stateOf(id) != jobs.StateCancelled {
		t.Fatalf("the job is %s", e.stateOf(id))
	}
	e.issue(43)
	other := e.labelJob(jobs.KindFix, 43)
	e.Hub.SetIssueState(slug, 43, "closed")
	before := len(e.forgeRequests())
	e.Hub.SendEvent(slug, fakehub.HubEvent{ID: "c42", Type: fakehub.EventIssueClosed, Issue: 43}) // the same id
	settle()
	if len(e.forgeRequests()) != before || e.stateOf(other) == jobs.StateCancelled {
		t.Error("the repeated id was acted on")
	}
}
