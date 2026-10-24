package poll

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
)

// TestTheStreamAnswersABlockedJobByTheHubEventsID_TS_19_3 verifies TS-19-3.
// Verifies: 19-REQ-2.3
func TestTheStreamAnswersABlockedJobByTheHubEventsID_TS_19_3(t *testing.T) {
	e := streaming(t, withRunner(answering(1)))
	id := e.blockFix(42)
	e.comment(42, "alice", "use the greeting from the README")
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.labeled("evt-7", 42, "af:fix")
	waitFor(t, "the event", func() bool { return e.taken("evt-7") })
	d := e.doc(id)
	if len(d.Attempts) != 2 || d.Attempts[1].Trigger != (jobs.Trigger{Kind: jobs.TriggerAnswer, By: "evt-7"}) {
		t.Fatalf("the attempts are %+v, want attempt 2 answered by evt-7", d.Attempts)
	}
	if a := d.Attempts[1].Answer; a == nil || !strings.Contains(a.Text, "use the greeting") || !strings.Contains(a.Text, "alice") {
		t.Errorf("the answer is %+v, want the comment and its author", a)
	}
	if e.hasLabel(42, "af:fix") {
		t.Error("af:fix is still on 42")
	}
}

// sightingRow is a row of the table of the rules: how the issue stands when a fix label is seen on it, and what the
// sighting is to come to.
type sightingRow struct {
	name    string
	runner  func() *fakerunner.Runner
	setup   func(e *env) string // returns the job the sighting concerns, or ""
	outcome string
	// info is what the INFO record of the outcome says, for the outcomes that log one.
	info string
	// pollOnly says that the row is the poll's alone: no record is newer than a sighting of the stream.
	pollOnly bool
}

// sightingRows is the table of TS-19-4.
func sightingRows() []sightingRow {
	return []sightingRow{
		{name: "an open blocked job and a comment", runner: func() *fakerunner.Runner { return answering(1) }, outcome: "answer", info: "answered",
			setup: func(e *env) string { id := e.blockFix(42); e.comment(42, "alice", "an answer"); return id }},
		{name: "an open blocked job and no comment", runner: func() *fakerunner.Runner { return answering(1) }, outcome: "no answer", info: "no answer",
			setup: func(e *env) string { return e.blockFix(42) }},
		{name: "an open blocked job with an owed removal", runner: func() *fakerunner.Runner { return answering(1) }, outcome: "removed",
			setup: func(e *env) string {
				id := e.blockFix(42)
				e.comment(42, "alice", "an answer")
				e.record(Record{Issue: 42, Label: "af:fix", Owed: true, Seen: SeenEnded})
				return id
			}},
		{name: "a held label", runner: func() *fakerunner.Runner { return fakerunner.New(fakerunner.Block(cancelledResult())) }, outcome: "nothing",
			setup: func(e *env) string {
				e.takenByThePoll()
				id := e.cancelRunning()
				e.runnerReturned(id)
				return id
			}},
		{name: "an owed removal", runner: func() *fakerunner.Runner { return fakerunner.New() }, outcome: "removed",
			setup: func(e *env) string {
				e.issue(42)
				e.record(Record{Issue: 42, Label: "af:fix", Owed: true, Seen: SeenEnded})
				return ""
			}},
		{name: "a newer record and a failed latest job", runner: func() *fakerunner.Runner { return failingRuns(1) }, outcome: "nothing", pollOnly: true,
			setup: func(e *env) string { return e.failFix(42) }},
		{name: "a failed latest job", runner: func() *fakerunner.Runner { return failingRuns(1) }, outcome: "retry", info: "retried",
			setup: func(e *env) string { id := e.failFix(42); e.comment(42, "bob", "more"); return id }},
		{name: "no job", runner: func() *fakerunner.Runner { return fakerunner.New().Then(fakerunner.Block(fakerunner.Step{})) }, outcome: "new request",
			setup: func(e *env) string { e.issue(42); return "" }},
	}
}

// decide runs the row with a sighting of the poll or of the stream, and says what it came to.
func decide(t *testing.T, row sightingRow, by string) (string, *env, string) {
	var e *env
	newer := row.pollOnly
	e = streaming(t, withRunner(row.runner()), withBeforeRequest(func(r *http.Request) {
		if newer && strings.Contains(r.URL.RawQuery, "labels=af%3Afix") {
			newer = false
			e.record(Record{Issue: 42, Label: "af:fix", Seen: SeenEnded, CreatedAt: e.Clock.Now()})
		}
	}))
	id := row.setup(e)
	e.Hub.AddLabel(slug, 42, "af:fix")
	attempts, jobsBefore, comments := 0, e.jobsFor(42), e.comments(42)
	if id != "" {
		attempts = len(e.doc(id).Attempts)
	}
	if by == "the poll" {
		e.cycle()
	} else {
		e.labeled("evt-row", 42, "af:fix")
		waitFor(t, "the event to be decided", func() bool { return e.taken("evt-row") })
	}
	switch {
	case e.jobsFor(42) > jobsBefore:
		return "new request", e, id
	case id != "" && len(e.doc(id).Attempts) > attempts:
		return string(e.doc(id).Attempts[attempts].Trigger.Kind), e, id
	case e.comments(42) > comments:
		return "no answer", e, id
	case e.hasLabel(42, "af:fix"):
		return "nothing", e, id
	}
	return "removed", e, id
}

// TestTheRulesOfASightingApplyInOrderAlikeForThePollAndTheStream_TS_19_4 verifies TS-19-4: each row of the table comes to its
// outcome for a sighting of the poll and for one of the stream, and answered, retried and no answer found each log one INFO
// that names the workspace, the kind, the issue, the label and the job. (The row of a pending follow-up is the rule of
// TS-19-50 and TS-19-51.)
// Verifies: 19-REQ-1.1
func TestTheRulesOfASightingApplyInOrderAlikeForThePollAndTheStream_TS_19_4(t *testing.T) {
	for _, row := range sightingRows() {
		for _, by := range []string{"the poll", "the stream"} {
			if row.pollOnly && by == "the stream" {
				continue
			}
			t.Run(fmt.Sprintf("%s, %s", row.name, by), func(t *testing.T) {
				got, e, id := decide(t, row, by)
				if got != row.outcome {
					t.Fatalf("the sighting came to %q, want %q", got, row.outcome)
				}
				want := map[string]string{"the poll": "label af:fix", "the stream": "evt-row"}[by]
				switch got {
				case "answer", "retry":
					if a := e.doc(id).Attempts; a[len(a)-1].Trigger.By != want {
						t.Errorf("the attempt is by %q, want %q", a[len(a)-1].Trigger.By, want)
					}
				case "new request":
					if j := e.jobList(); j[len(j)-1].SourceBy != want {
						t.Errorf("the new job is by %q, want %q", j[len(j)-1].SourceBy, want)
					}
				}
				if row.info == "" {
					return
				}
				infos := e.Logs.find("INFO", row.info)
				if len(infos) != 1 {
					t.Fatalf("%d INFO records of %q, want 1", len(infos), row.info)
				}
				r := infos[0]
				if r["workspace"] != slug || r["kind"] != "fix" || r["issue"] != float64(42) || r["label"] != "af:fix" || r["job"] != id {
					t.Errorf("the INFO record is %v", r)
				}
			})
		}
	}
}

// TestTheStreamLeavesASightingWhoseCommentsItCannotReadToThePoll_TS_19_22 verifies TS-19-22.
// Verifies: 19-REQ-3.8
func TestTheStreamLeavesASightingWhoseCommentsItCannotReadToThePoll_TS_19_22(t *testing.T) {
	for _, how := range []string{"a failed read", "comments_err"} {
		t.Run(how, func(t *testing.T) {
			e := streaming(t, withRunner(answering(1)))
			id := e.blockFix(42)
			e.comment(42, "alice", "an answer")
			e.Hub.AddLabel(slug, 42, "af:fix")
			if how == "comments_err" {
				e.Hub.Inject(http.MethodGet, issuePath(42), fakehub.Fault{Status: 200, Body: e.threadBody(42, false, true)})
			} else {
				e.Hub.Fail(issuePath(42), 503)
			}
			comments := e.comments(42)
			e.labeled("evt-1", 42, "af:fix")
			waitFor(t, "the stream's read", func() bool { return len(e.Hub.RequestsTo(http.MethodGet, issuePath(42))) >= 1 })
			waitFor(t, "the failure record", func() bool {
				return len(e.Logs.find("WARN", "left to the poll"))+len(e.Logs.find("ERROR", "left to the poll")) == 1
			})
			if len(e.doc(id).Attempts) != 1 || e.comments(42) != comments || !e.hasLabel(42, "af:fix") || e.taken("evt-1") {
				t.Fatalf("%d attempts, %d comments, label %v, taken %v: want no action", len(e.doc(id).Attempts), e.comments(42)-comments, e.labelsOf(42), e.taken("evt-1"))
			}
			e.Hub.Clear()
			e.cycle()
			if len(e.doc(id).Attempts) != 2 {
				t.Errorf("the poll's cycle made %d attempts, want the answer", len(e.doc(id).Attempts))
			}
		})
	}
}
