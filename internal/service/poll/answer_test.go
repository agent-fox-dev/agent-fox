package poll

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
)

// answering is a runner whose first attempts end blocked on the question Which?, and whose later attempts wait.
func answering(blocked int) *fakerunner.Runner {
	steps := make([]fakerunner.Step, blocked)
	for i := range steps {
		steps[i] = blockedStep()
	}
	return fakerunner.New(steps...).Then(fakerunner.Block(fakerunner.Step{}))
}

// issuePath is the path of the read of the issue.
func issuePath(n int) string { return fmt.Sprintf("/api/v1/workspaces/%s/issues/%d", slug, n) }

// blockFix makes a fix job of the issue as the poll does, waits until its attempt is blocked, and posts its question
// comment on the issue as the worker does. It returns the job's id.
func (e *env) blockFix(n int) string {
	e.t.Helper()
	if _, ok := e.Hub.IssueOf(slug, n); !ok {
		e.issue(n)
	}
	id := e.startedJob(jobs.KindFix, n, "af:fix")
	e.waitState(id, jobs.StateBlocked)
	e.comment(n, "agent-fox", fmt.Sprintf("**agent-fox needs an answer to go on.**\n\nWhich?\n\n<!-- agent-fox job=%s attempt=1 kind=question -->", id))
	return id
}

// comment adds a comment of the login to the issue.
func (e *env) comment(n int, login, body string) { e.Hub.AddPersonComment(slug, n, login, body) }

// comments are how many comments the issue has.
func (e *env) comments(n int) int {
	issue, _ := e.Hub.IssueOf(slug, n)
	return len(issue.Comments)
}

// doc is the document of the job with its attempts.
func (e *env) doc(id string) jobs.Doc {
	e.t.Helper()
	d, err := e.Jobs.Get(ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return d
}

// hasLabel says whether the issue carries the label on hub.
func (e *env) hasLabel(n int, label string) bool {
	for _, l := range e.labelsOf(n) {
		if l == label {
			return true
		}
	}
	return false
}

// TestAPersonsCommentAndTheLabelAgainAnswerTheBlockedFixJob_TS_19_1 verifies TS-19-1.
// Verifies: 19-REQ-1.2, 19-REQ-2.1
func TestAPersonsCommentAndTheLabelAgainAnswerTheBlockedFixJob_TS_19_1(t *testing.T) {
	e := newEnv(t, withRunner(answering(1)))
	id := e.blockFix(42)
	e.comment(42, "alice", "use the greeting from the README")
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.cycle()
	d := e.doc(id)
	if len(d.Attempts) != 2 || d.Attempts[1].Trigger != (jobs.Trigger{Kind: jobs.TriggerAnswer, By: "label af:fix"}) {
		t.Fatalf("the attempts are %+v, want attempt 2 answered by the label", d.Attempts)
	}
	a := d.Attempts[1].Answer
	if a == nil || a.Question != "Which?" || a.Option != nil || !strings.Contains(a.Text, "use the greeting from the README") || !strings.Contains(a.Text, "alice") {
		t.Errorf("attempt 2's answer is %+v, want the question, no option, and the comment with its author", a)
	}
	if e.hasLabel(42, "af:fix") || e.removalsOf(42) != 1 {
		t.Errorf("af:fix on 42: %v, %d removals, want it removed once", e.labelsOf(42), e.removalsOf(42))
	}
	if n := e.jobsFor(42); n != 1 {
		t.Errorf("%d jobs for 42, want the one", n)
	}
	if got := e.Logs.find("INFO", "answered"); len(got) != 1 || got[0]["workspace"] != slug || got[0]["kind"] != "fix" ||
		got[0]["issue"] != float64(42) || got[0]["label"] != "af:fix" || got[0]["job"] != id {
		t.Errorf("the INFO records of the answer are %v", got)
	}
}

// TestTheRunnerOfAForgeAnswersAttemptIsGivenTheAnswer_TS_19_2 verifies TS-19-2.
// Verifies: 19-REQ-2.2
func TestTheRunnerOfAForgeAnswersAttemptIsGivenTheAnswer_TS_19_2(t *testing.T) {
	r := answering(1)
	e := newEnv(t, withRunner(r))
	e.blockFix(42)
	e.comment(42, "alice", "use the greeting from the README")
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.cycle()
	waitFor(t, "attempt 2's runner", func() bool { return len(r.Calls()) == 2 })
	answers := r.Calls()[1].Answers
	if len(answers) != 1 || answers[0].Question != "Which?" || !strings.Contains(answers[0].Text, "use the greeting") || !strings.Contains(answers[0].Text, "alice") {
		t.Errorf("the runner was given the answers %+v", answers)
	}
}

// TestARecordNewerThanTheListingMakesTheSightingDoNothing_TS_19_5 verifies TS-19-5: a blocked job, a failed job and an issue
// with no job, each with a record of af:fix on 42 that another trigger wrote after the listing was asked for.
// Verifies: 19-REQ-1.3
func TestARecordNewerThanTheListingMakesTheSightingDoNothing_TS_19_5(t *testing.T) {
	for _, state := range []string{"blocked", "failed", "no job"} {
		t.Run(state, func(t *testing.T) {
			var e *env
			inserted := false
			e = newEnv(t, withRunner(fakerunner.New(blockedStep(), fakerunner.Failed("sandbox", false)).Then(fakerunner.Block(fakerunner.Step{}))),
				withBeforeRequest(func(r *http.Request) {
					if !inserted && strings.Contains(r.URL.RawQuery, "labels=af%3Afix") {
						inserted = true
						e.record(Record{Issue: 42, Label: "af:fix", Seen: SeenEnded, CreatedAt: e.Clock.Now()})
					}
				}))
			e.issue(42)
			id := ""
			switch state {
			case "blocked":
				id = e.blockFix(42)
			case "failed":
				e.startedJob(jobs.KindFix, 7, "af:fix") // the first step, blocked, goes to another issue
				id = e.startedJob(jobs.KindFix, 42, "af:fix")
				e.waitState(id, jobs.StateFailed)
			}
			e.comment(42, "alice", "here is more")
			e.Hub.AddLabel(slug, 42, "af:fix")
			comments, before := e.comments(42), len(e.jobList())
			attempts := 0
			if id != "" {
				attempts = len(e.doc(id).Attempts)
			}
			e.cycle()
			if !e.hasLabel(42, "af:fix") || e.comments(42) != comments || len(e.jobList()) != before {
				t.Errorf("label %v, %d comments (was %d), %d jobs (was %d): want nothing done", e.labelsOf(42), e.comments(42), comments, len(e.jobList()), before)
			}
			if id != "" && len(e.doc(id).Attempts) != attempts {
				t.Errorf("the job has %d attempts, want %d", len(e.doc(id).Attempts), attempts)
			}
		})
	}
}

// TestALabelOnAnIssueWhoseJobIsQueuedRunningOrBlockedWithAnOwedRemovalIsAbsorbed_TS_19_6 verifies TS-19-6.
// Verifies: 19-REQ-1.4
func TestALabelOnAnIssueWhoseJobIsQueuedRunningOrBlockedWithAnOwedRemovalIsAbsorbed_TS_19_6(t *testing.T) {
	for _, state := range []string{"queued", "running", "blocked with an owed removal"} {
		t.Run(state, func(t *testing.T) {
			e := newEnv(t, withRunner(answering(1)))
			var id string
			switch state {
			case "queued":
				e.blockFix(1)
				other := e.startedJob(jobs.KindFix, 2, "af:fix") // the workspace's runner holds this one
				e.waitState(other, jobs.StateRunning)
				e.issue(42)
				id = e.startedJob(jobs.KindFix, 42, "af:fix")
			case "running":
				e.blockFix(1)
				e.issue(42)
				id = e.startedJob(jobs.KindFix, 42, "af:fix")
				e.waitState(id, jobs.StateRunning)
			default:
				id = e.blockFix(42)
				e.record(Record{Issue: 42, Label: "af:fix", Owed: true, Seen: SeenEnded})
			}
			e.comment(42, "alice", "here is an answer")
			e.Hub.AddLabel(slug, 42, "af:fix")
			attempts, jobsBefore := len(e.doc(id).Attempts), len(e.jobList())
			e.cycle()
			if len(e.doc(id).Attempts) != attempts || len(e.jobList()) != jobsBefore {
				t.Errorf("%d attempts and %d jobs, want %d and %d", len(e.doc(id).Attempts), len(e.jobList()), attempts, jobsBefore)
			}
			if e.hasLabel(42, "af:fix") {
				t.Errorf("af:fix is still on 42")
			}
		})
	}
}

// TestAnAPIAnswerBetweenThePollsReadAndItsAnswerMakesThePollAbsorbTheLabel_TS_19_12 verifies TS-19-12.
// Verifies: 19-REQ-2.4
func TestAnAPIAnswerBetweenThePollsReadAndItsAnswerMakesThePollAbsorbTheLabel_TS_19_12(t *testing.T) {
	var e *env
	var id string
	answered := false
	e = newEnv(t, withRunner(answering(1)), withBeforeRequest(func(r *http.Request) {
		if !answered && r.Method == http.MethodGet && r.URL.Path == issuePath(42) {
			answered = true
			text := "by the API"
			if _, err := e.Jobs.Answer(context.Background(), id, "user-1", jobs.AnswerRequest{Answer: &text}); err != nil {
				t.Errorf("the API answer: %v", err)
			}
		}
	}))
	id = e.blockFix(42)
	e.comment(42, "alice", "use the greeting")
	e.Hub.AddLabel(slug, 42, "af:fix")
	comments := e.comments(42)
	e.cycle()
	if !answered {
		t.Fatal("the poll did not read the issue")
	}
	d := e.doc(id)
	if len(d.Attempts) != 2 || d.Attempts[1].Trigger.By != "user-1" {
		t.Errorf("the attempts are %+v, want the API's one answer", d.Attempts)
	}
	if e.hasLabel(42, "af:fix") || e.comments(42) != comments {
		t.Errorf("label %v and %d comments, want the label absorbed and no comment", e.labelsOf(42), e.comments(42))
	}
}

// answerRefused is a jobs layer whose answers and retries are refused with an error.
type answerRefused struct {
	Jobs
	err error
}

func (a answerRefused) Answer(context.Context, string, string, jobs.AnswerRequest) (jobs.Doc, error) {
	return jobs.Doc{}, a.err
}

func (a answerRefused) RetryWithContext(context.Context, string, string, []string) (jobs.Doc, error) {
	return jobs.Doc{}, a.err
}

// writes are the requests of the poll that wrote to the forge: comments and label changes.
func (e *env) writes() []string {
	var out []string
	for _, r := range e.Hub.Requests() {
		if r.Method != http.MethodGet && strings.Contains(r.Path, "/issues/") {
			out = append(out, r.Method+" "+r.Path)
		}
	}
	return out
}

// TestAnAnswerForAWorkspaceThatIsNotSubscribedWritesNothingAndEndsTheCycle_TS_19_13 verifies TS-19-13.
// Verifies: 19-REQ-2.5
func TestAnAnswerForAWorkspaceThatIsNotSubscribedWritesNothingAndEndsTheCycle_TS_19_13(t *testing.T) {
	e := newEnv(t, withRunner(answering(2)), withJobs(func(j Jobs) Jobs { return answerRefused{Jobs: j, err: jobs.ErrNotSubscribed} }))
	for _, n := range []int{42, 43} {
		e.blockFix(n)
		e.comment(n, "alice", "an answer")
		e.Hub.AddLabel(slug, n, "af:fix")
	}
	before := len(e.writes())
	e.cycle()
	if got := e.writes()[before:]; len(got) != 0 {
		t.Errorf("the poll wrote %v, want nothing", got)
	}
	if reads := len(e.Hub.RequestsTo(http.MethodGet, issuePath(43))); reads != 0 {
		t.Errorf("43 was read %d times, want the cycle ended before it", reads)
	}
}

// TestAnUnreadableTokenOrAnotherErrorOfTheAnswerLeavesTheLabelAndLogsOnce_TS_19_14 verifies TS-19-14.
// Verifies: 19-REQ-2.6
func TestAnUnreadableTokenOrAnotherErrorOfTheAnswerLeavesTheLabelAndLogsOnce_TS_19_14(t *testing.T) {
	refused := &answerRefused{err: jobs.ErrTokenUnreadable}
	e := newEnv(t, withRunner(answering(1)), withJobs(func(j Jobs) Jobs { refused.Jobs = j; return refused }))
	id := e.blockFix(42)
	e.comment(42, "alice", "an answer")
	e.Hub.AddLabel(slug, 42, "af:fix")
	for _, err := range []error{jobs.ErrTokenUnreadable, errors.New("disk full")} {
		refused.err = err
		e.cycle()
		if !e.hasLabel(42, "af:fix") || len(e.doc(id).Attempts) != 1 {
			t.Errorf("%v: label %v and %d attempts, want the label left and no attempt", err, e.labelsOf(42), len(e.doc(id).Attempts))
		}
	}
	if got := e.Logs.find("WARN", "answer"); len(got) != 1 {
		t.Errorf("%d failure records of the answer, want 1: %v", len(got), got)
	}
}

// TestTheCommentsAreReadThroughTheIssueReadAndNeverTheCommentList_TS_19_15 verifies TS-19-15: the poll reads the issue to
// answer, and the stream answers with the issue read it made already, one read for its sighting.
// Verifies: 19-REQ-3.1
func TestTheCommentsAreReadThroughTheIssueReadAndNeverTheCommentList_TS_19_15(t *testing.T) {
	e := streaming(t, withRunner(answering(3))) // 42's first attempt and its answer, then 43's first attempt
	e.blockFix(42)
	e.comment(42, "alice", "by the poll")
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.cycle()
	if n := len(e.Hub.RequestsTo(http.MethodGet, issuePath(42))); n != 1 {
		t.Errorf("the poll read 42 %d times, want once", n)
	}
	e.blockFix(43)
	e.comment(43, "alice", "by the stream")
	e.Hub.AddLabel(slug, 43, "af:fix")
	e.labeled("evt-43", 43, "af:fix")
	waitFor(t, "the stream's answer", func() bool { return e.taken("evt-43") })
	if n := len(e.Hub.RequestsTo(http.MethodGet, issuePath(43))); n != 1 {
		t.Errorf("the stream read 43 %d times, want once", n)
	}
	for _, n := range []int{42, 43} {
		if got := len(e.Hub.RequestsTo(http.MethodGet, issuePath(n)+"/comments")); got != 0 {
			t.Errorf("the comment list of %d was read %d times, want never", n, got)
		}
	}
}

// TestAHubFailureOfTheIssueReadLeavesTheLabelAndTheNextCycleAnswers_TS_19_20 verifies TS-19-20.
// Verifies: 19-REQ-3.6
func TestAHubFailureOfTheIssueReadLeavesTheLabelAndTheNextCycleAnswers_TS_19_20(t *testing.T) {
	for _, status := range []int{503, 401, 403, 502} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			e := newEnv(t, withRunner(answering(2)))
			id := e.blockFix(42)
			e.blockFix(43)
			for _, n := range []int{42, 43} {
				e.comment(n, "alice", "an answer")
				e.Hub.AddLabel(slug, n, "af:fix")
			}
			if status == 502 { // the forge refuses hub's credential, which would fail every issue
				e.Hub.Inject(http.MethodGet, issuePath(42), fakehub.Fault{Status: 502,
					Body: `{"error":{"code":"forge_forbidden","message":"the forge forbids hub's credential","error_type":"forge"}}`})
			} else {
				e.Hub.Fail(issuePath(42), status)
			}
			comments := e.comments(42)
			e.cycle()
			if !e.hasLabel(42, "af:fix") || len(e.doc(id).Attempts) != 1 || e.comments(42) != comments {
				t.Errorf("label %v, %d attempts, %d comments: want nothing done", e.labelsOf(42), len(e.doc(id).Attempts), e.comments(42))
			}
			if reads := len(e.Hub.RequestsTo(http.MethodGet, issuePath(43))); reads != 0 {
				t.Errorf("43 was read %d times, want the cycle ended", reads)
			}
			if status == 503 && len(e.Logs.level("WARN")) != 1 {
				t.Errorf("WARN records %v, want one", e.Logs.level("WARN"))
			}
			if (status == 401 || status == 403) && len(e.Logs.level("ERROR")) != 1 {
				t.Errorf("ERROR records %v, want one, as the poll logs a token hub refuses", e.Logs.level("ERROR"))
			}
			e.Hub.Clear()
			e.cycle()
			if len(e.doc(id).Attempts) != 2 {
				t.Errorf("the next cycle made %d attempts, want the answer", len(e.doc(id).Attempts))
			}
		})
	}
}

// threadBody is the body of hub's issue read of the issue as the fake holds it, with the cut and the failure to read the
// comments that hub may say.
func (e *env) threadBody(n int, truncated, commentsErr bool) string {
	e.t.Helper()
	issue, _ := e.Hub.IssueOf(slug, n)
	comments := []map[string]any{}
	for _, c := range issue.Comments {
		comments = append(comments, map[string]any{"id": c.ID, "body": c.Body, "user": map[string]any{"login": c.Author}, "created_at": c.CreatedAt})
	}
	out := map[string]any{
		"issue":     map[string]any{"number": n, "title": issue.Title, "body": issue.Body, "state": issue.State, "labels": issue.Labels},
		"comments":  comments,
		"truncated": truncated,
	}
	if commentsErr {
		out["comments_err"] = map[string]any{}
		out["comments"] = []any{}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(raw)
}

// TestA4xxOnOneIssuesReadOrCommentsErrReportsAndTheCycleGoesOn_TS_19_21 verifies TS-19-21.
// Verifies: 19-REQ-3.7
func TestA4xxOnOneIssuesReadOrCommentsErrReportsAndTheCycleGoesOn_TS_19_21(t *testing.T) {
	for _, how := range []string{"404", "422", "comments_err"} {
		t.Run(how, func(t *testing.T) {
			e := newEnv(t, withRunner(answering(2)))
			j42, j43 := e.blockFix(42), e.blockFix(43)
			for _, n := range []int{42, 43} {
				e.comment(n, "alice", "an answer")
				e.Hub.AddLabel(slug, n, "af:fix")
			}
			switch how {
			case "comments_err":
				e.Hub.Inject(http.MethodGet, issuePath(42), fakehub.Fault{Status: 200, Body: e.threadBody(42, false, true)})
			case "404":
				e.Hub.Fail(issuePath(42), 404)
			default:
				e.Hub.Fail(issuePath(42), 422)
			}
			comments := e.comments(42)
			e.cycle()
			if !e.hasLabel(42, "af:fix") || len(e.doc(j42).Attempts) != 1 || e.comments(42) != comments {
				t.Errorf("42: label %v, %d attempts, %d comments: want nothing done", e.labelsOf(42), len(e.doc(j42).Attempts), e.comments(42))
			}
			if len(e.Logs.level("ERROR"))+len(e.Logs.level("WARN")) == 0 {
				t.Error("the failure was not reported")
			}
			if len(e.doc(j43).Attempts) != 2 {
				t.Errorf("43 has %d attempts, want it answered in the same cycle", len(e.doc(j43).Attempts))
			}
		})
	}
}
