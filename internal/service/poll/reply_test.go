package poll

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
)

// lastComment is the body of the last comment of the issue.
func (e *env) lastComment(n int) string {
	issue, _ := e.Hub.IssueOf(slug, n)
	if len(issue.Comments) == 0 {
		return ""
	}
	return issue.Comments[len(issue.Comments)-1].Body
}

// cutRead makes hub answer the read of the issue as it holds it, with the comments cut.
func (e *env) cutRead(n int) {
	e.Hub.Inject(http.MethodGet, issuePath(n), fakehub.Fault{Status: 200, Body: e.threadBody(n, true, false)})
}

// TestNoAnswerFoundIsRepliedOnceAndTheLabelIsRemoved_TS_19_28 verifies TS-19-28.
// Verifies: 19-REQ-5.1, 19-REQ-5.2
func TestNoAnswerFoundIsRepliedOnceAndTheLabelIsRemoved_TS_19_28(t *testing.T) {
	e := newEnv(t, withRunner(answering(1)))
	id := e.blockFix(42)
	e.Hub.AddLabel(slug, 42, "af:fix")
	comments := e.comments(42)
	e.cycle()
	want := "No answer found since the question; comment, then add the label `af:fix` again.\n\n" +
		fmt.Sprintf("<!-- agent-fox job=%s attempt=1 kind=no_answer -->", id)
	if e.comments(42) != comments+1 || !strings.HasPrefix(e.lastComment(42), want) {
		t.Fatalf("%d new comments, the last %q, want the one reply %q", e.comments(42)-comments, e.lastComment(42), want)
	}
	if e.hasLabel(42, "af:fix") || len(e.doc(id).Attempts) != 1 {
		t.Errorf("label %v and %d attempts, want the label removed and no attempt", e.labelsOf(42), len(e.doc(id).Attempts))
	}
	if got := e.Logs.find("INFO", "no answer"); len(got) != 1 || got[0]["job"] != id || got[0]["issue"] != float64(42) || got[0]["label"] != "af:fix" {
		t.Errorf("the INFO records are %v", got)
	}
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.cycle()
	if e.comments(42) != comments+1 || e.hasLabel(42, "af:fix") || len(e.doc(id).Attempts) != 1 {
		t.Errorf("the second sighting: %d new comments, label %v, %d attempts; want no second reply, the label removed and no attempt",
			e.comments(42)-comments, e.labelsOf(42), len(e.doc(id).Attempts))
	}
}

// TestACutThreadOnAnAnswerRepliesWarnsOnceAndMakesNoAttempt_TS_19_29 verifies TS-19-29.
// Verifies: 19-REQ-5.3
func TestACutThreadOnAnAnswerRepliesWarnsOnceAndMakesNoAttempt_TS_19_29(t *testing.T) {
	e := newEnv(t, withRunner(answering(1)))
	id := e.blockFix(42)
	e.comment(42, "alice", "use the greeting from the README")
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.cutRead(42)
	e.cycle()
	if e.hasLabel(42, "af:fix") || len(e.doc(id).Attempts) != 1 {
		t.Errorf("label %v and %d attempts, want the label removed and no attempt", e.labelsOf(42), len(e.doc(id).Attempts))
	}
	warns := e.Logs.level("WARN")
	if len(warns) != 1 || warns[0]["workspace"] != slug || warns[0]["issue"] != float64(42) || warns[0]["job"] != id {
		t.Errorf("the WARN records are %v, want one naming the workspace, 42 and the job", warns)
	}
	last := e.lastComment(42)
	for _, want := range []string{"could not read the whole thread", "af jobs answer " + id, "POST /api/v1/jobs/" + id + "/answer"} {
		if !strings.Contains(last, want) {
			t.Errorf("the reply %q lacks %q", last, want)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(last), fmt.Sprintf("<!-- agent-fox job=%s attempt=1 kind=thread_cut -->", id)) {
		t.Errorf("the reply %q does not end with the thread_cut marker", last)
	}
}

// TestACutThreadOnARetryMakesNoRetryAndNamesTheRetryPaths_TS_19_30 verifies TS-19-30.
// Verifies: 19-REQ-5.4
func TestACutThreadOnARetryMakesNoRetryAndNamesTheRetryPaths_TS_19_30(t *testing.T) {
	e := newEnv(t, withRunner(failingRuns(1)))
	id := e.failFix(42)
	e.comment(42, "bob", "the log is in CI run 812")
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.cutRead(42)
	e.cycle()
	d := e.doc(id)
	if d.State != jobs.StateFailed || len(d.Attempts) != 1 || e.hasLabel(42, "af:fix") {
		t.Errorf("the job is %s with %d attempts, label %v: want it failed, no retry, the label removed", d.State, len(d.Attempts), e.labelsOf(42))
	}
	if n := len(e.Logs.level("WARN")); n != 1 {
		t.Errorf("%d WARN records, want 1", n)
	}
	last := e.lastComment(42)
	for _, want := range []string{"af jobs retry " + id, "POST /api/v1/jobs/" + id + "/retry", "kind=thread_cut"} {
		if !strings.Contains(last, want) {
			t.Errorf("the reply %q lacks %q", last, want)
		}
	}
}

// TestAReplyThatCannotBePostedWarnsAndTheLabelIsRemovedAllTheSame_TS_19_31 verifies TS-19-31.
// Verifies: 19-REQ-5.5
func TestAReplyThatCannotBePostedWarnsAndTheLabelIsRemovedAllTheSame_TS_19_31(t *testing.T) {
	for _, reply := range []string{"no answer", "a cut thread"} {
		t.Run(reply, func(t *testing.T) {
			e := newEnv(t, withRunner(answering(1)))
			id := e.blockFix(42)
			if reply == "a cut thread" {
				e.comment(42, "alice", "an answer")
				e.cutRead(42)
			}
			e.Hub.AddLabel(slug, 42, "af:fix")
			e.Hub.Inject(http.MethodPost, issuePath(42)+"/comments", fakehub.Fault{Status: 503})
			e.cycle()
			if e.hasLabel(42, "af:fix") || len(e.doc(id).Attempts) != 1 {
				t.Errorf("label %v and %d attempts, want the label removed and no attempt", e.labelsOf(42), len(e.doc(id).Attempts))
			}
			if len(e.Logs.find("WARN", "could not be posted")) != 1 {
				t.Errorf("the WARN records are %v, want one that the reply could not be posted", e.Logs.level("WARN"))
			}
		})
	}
}

// TestEveryForgeOutcomeWritesItsRecordBeforeTheRemovalAndNamesTheJobOnlyForItsOwnLabel_TS_19_10 verifies TS-19-10: an answer,
// a retry, a no-answer reply and a cut-thread reply, each for a job of the poll (request label af:fix) and for a job of the
// API (none), with the first removal of af:fix failing: the record is there, owed, before the removal, it names the job
// only for the job of the poll, and the next cycle removes the label.
// Verifies: 19-REQ-1.7
func TestEveryForgeOutcomeWritesItsRecordBeforeTheRemovalAndNamesTheJobOnlyForItsOwnLabel_TS_19_10(t *testing.T) {
	for _, outcome := range []string{"answer", "retry", "no_answer", "thread_cut"} {
		for _, label := range []string{"af:fix", ""} {
			t.Run(fmt.Sprintf("%s, request label %q", outcome, label), func(t *testing.T) {
				first := blockedStep()
				if outcome == "retry" {
					first = fakerunner.Failed("sandbox", false)
				}
				var e *env
				var atRemoval []Record
				e = newEnv(t, withRunner(fakerunner.New(first).Then(fakerunner.Block(fakerunner.Step{}))), withBeforeRequest(func(r *http.Request) {
					if r.Method == http.MethodDelete && atRemoval == nil {
						atRemoval = e.recordsOf()
					}
				}))
				e.issue(42)
				doc, err := e.Jobs.Create(ctx, jobs.CreateRequest{Workspace: slug, Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage("42")},
					Source: map[bool]jobs.Source{true: {Kind: "label", By: "label af:fix"}, false: {Kind: "api", By: "user-1"}}[label != ""], RequestLabel: label})
				if err != nil {
					t.Fatal(err)
				}
				kind := "question"
				if outcome == "retry" {
					kind = "failure"
					e.waitState(doc.ID, jobs.StateFailed)
				} else {
					e.waitState(doc.ID, jobs.StateBlocked)
				}
				e.comment(42, "agent-fox", fmt.Sprintf("<!-- agent-fox job=%s attempt=1 kind=%s -->", doc.ID, kind))
				if outcome != "no_answer" {
					e.comment(42, "alice", "an answer")
				}
				if outcome == "thread_cut" {
					e.cutRead(42)
				}
				e.Hub.AddLabel(slug, 42, "af:fix")
				e.Hub.FailOnce(removalPath(42, "af:fix"), 503)
				e.cycle()
				if len(atRemoval) != 1 || !atRemoval[0].Owed || atRemoval[0].Label != "af:fix" || atRemoval[0].Issue != 42 {
					t.Fatalf("the records at the removal were %+v, want the one owed record of af:fix on 42", atRemoval)
				}
				if want := map[bool]string{true: doc.ID, false: ""}[label != ""]; atRemoval[0].JobID != want {
					t.Errorf("the record names the job %q, want %q", atRemoval[0].JobID, want)
				}
				if recs := e.recordsOf(); len(recs) == 0 || !recs[0].Owed {
					t.Errorf("after the failed removal the records are %+v, want it owed", recs)
				}
				e.Hub.Clear()
				e.cycle()
				if e.hasLabel(42, "af:fix") {
					t.Errorf("the next cycle left af:fix on 42")
				}
			})
		}
	}
}
