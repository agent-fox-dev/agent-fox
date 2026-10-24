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

// failingRuns is a runner whose first attempts fail, not to be tried again, and whose later attempts wait.
func failingRuns(failed int) *fakerunner.Runner {
	steps := make([]fakerunner.Step, failed)
	for i := range steps {
		steps[i] = fakerunner.Failed("sandbox", false)
	}
	return fakerunner.New(steps...).Then(fakerunner.Block(fakerunner.Step{}))
}

// failFix makes a fix job of the issue as the poll does, waits until it failed, and posts its failure comment on the
// issue as the worker does. It returns the job's id.
func (e *env) failFix(n int) string {
	e.t.Helper()
	if _, ok := e.Hub.IssueOf(slug, n); !ok {
		e.issue(n)
	}
	id := e.startedJob(jobs.KindFix, n, "af:fix")
	e.waitState(id, jobs.StateFailed)
	e.comment(n, "agent-fox", fmt.Sprintf("**agent-fox could not finish this attempt.**\n\n<!-- agent-fox job=%s attempt=1 kind=failure -->", id))
	return id
}

// TestTheLabelAgainOnAFailedFixJobRetriesItWithTheCommentsAsContext_TS_19_7 verifies TS-19-7.
// Verifies: 19-REQ-1.5, 19-REQ-4.1
func TestTheLabelAgainOnAFailedFixJobRetriesItWithTheCommentsAsContext_TS_19_7(t *testing.T) {
	r := failingRuns(1)
	e := newEnv(t, withRunner(r))
	id := e.failFix(42)
	e.comment(42, "bob", "the log is in CI run 812")
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.cycle()
	d := e.doc(id)
	if len(d.Attempts) != 2 || d.Attempts[1].Trigger != (jobs.Trigger{Kind: jobs.TriggerRetry, By: "label af:fix"}) {
		t.Fatalf("the attempts are %+v, want attempt 2 retried by the label", d.Attempts)
	}
	if c := d.Attempts[1].Context; len(c) != 1 || !strings.Contains(c[0], "the log is in CI run 812") || !strings.Contains(c[0], "bob") {
		t.Errorf("attempt 2's context is %q, want the comment with its author", c)
	}
	if n := e.jobsFor(42); n != 1 {
		t.Errorf("%d jobs for 42, want the one retried", n)
	}
	if e.hasLabel(42, "af:fix") {
		t.Error("af:fix is still on 42")
	}
	if got := e.Logs.find("INFO", "retried"); len(got) != 1 || got[0]["job"] != id || got[0]["label"] != "af:fix" || got[0]["issue"] != float64(42) {
		t.Errorf("the INFO records of the retry are %v", got)
	}
	waitFor(t, "attempt 2's runner", func() bool { return len(r.Calls()) == 2 })
	if c := r.Calls()[1].Context; len(c) == 0 || !strings.Contains(c[len(c)-1], "CI run 812") {
		t.Errorf("the runner was given the context %q, want it to end with the comment", c)
	}
}

// TestAForgeRetryWithNoCommentsSinceTheFailureRunsWithNoContextAndNoReply_TS_19_8 verifies TS-19-8.
// Verifies: 19-REQ-4.2
func TestAForgeRetryWithNoCommentsSinceTheFailureRunsWithNoContextAndNoReply_TS_19_8(t *testing.T) {
	e := newEnv(t, withRunner(failingRuns(1)))
	id := e.failFix(42)
	e.Hub.AddLabel(slug, 42, "af:fix")
	comments := e.comments(42)
	e.cycle()
	d := e.doc(id)
	if len(d.Attempts) != 2 || d.Attempts[1].Trigger.Kind != jobs.TriggerRetry || d.Attempts[1].Context != nil {
		t.Errorf("the attempts are %+v, want a retry with the context null", d.Attempts)
	}
	if e.comments(42) != comments {
		t.Errorf("%d comments, want no reply", e.comments(42)-comments)
	}
}

// TestALabelOnAnIssueWhoseLatestJobIsDoneOrCancelledMakesANewJob_TS_19_9 verifies TS-19-9: a triaged issue labelled af:triage
// again is a new triage, a fix label on an issue whose latest fix job was cancelled is a new fix job, and the earlier job's
// document is the same before and after.
// Verifies: 19-REQ-1.6
func TestALabelOnAnIssueWhoseLatestJobIsDoneOrCancelledMakesANewJob_TS_19_9(t *testing.T) {
	t.Run("a done triage", func(t *testing.T) {
		e := newEnv(t, withRunner(fakerunner.New(fakerunner.Done()).Then(fakerunner.Block(fakerunner.Step{}))))
		e.Hub.AddIssue(slug, fakehub.Issue{Number: 42, Body: "Steps\n\n<!-- agent-fox:triage v1 run=r1 -->\n## Diagnosis\nold\n<!-- /agent-fox:triage -->"})
		t0 := e.startedJob(jobs.KindTriage, 42, "af:triage")
		e.waitState(t0, jobs.StateDone)
		before, _ := json.Marshal(e.doc(t0))
		e.Hub.AddLabel(slug, 42, "af:triage")
		e.cycle()
		after, _ := json.Marshal(e.doc(t0))
		if string(before) != string(after) {
			t.Errorf("the done triage changed:\n%s\n%s", before, after)
		}
		jobsOf := e.jobList()
		if len(jobsOf) != 2 || jobsOf[1].Kind != "triage" || jobsOf[1].SourceKind != "label" || jobsOf[1].SourceBy != "label af:triage" {
			t.Errorf("the jobs are %+v, want a new triage job of the label", jobsOf)
		}
	})
	t.Run("a cancelled fix", func(t *testing.T) {
		e := newEnv(t, withRunner(fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))))
		e.issue(42)
		f0 := e.startedJob(jobs.KindFix, 42, "af:fix")
		if _, err := e.Jobs.Cancel(ctx, f0); err != nil {
			t.Fatal(err)
		}
		e.runnerReturned(f0)
		before, _ := json.Marshal(e.doc(f0))
		e.Hub.AddLabel(slug, 42, "af:fix")
		e.cycle()
		after, _ := json.Marshal(e.doc(f0))
		if string(before) != string(after) {
			t.Errorf("the cancelled fix changed:\n%s\n%s", before, after)
		}
		if n := e.jobsFor(42); n != 2 {
			t.Errorf("%d jobs for 42, want a new one", n)
		}
		if j := e.jobList(); j[len(j)-1].SourceKind != "label" || j[len(j)-1].SourceBy != "label af:fix" {
			t.Errorf("the new job is from %s %s, want the label", j[len(j)-1].SourceKind, j[len(j)-1].SourceBy)
		}
	})
}

// TestADuplicateOrStateRefusalOfTheRetryAbsorbsTheLabel_TS_19_26 verifies TS-19-26: the job retried through the API after the
// poll read the issue, and another fix job of the issue made then.
// Verifies: 19-REQ-4.6
func TestADuplicateOrStateRefusalOfTheRetryAbsorbsTheLabel_TS_19_26(t *testing.T) {
	for _, how := range []string{"a retry through the API", "another job of the issue"} {
		t.Run(how, func(t *testing.T) {
			var e *env
			var id string
			hooked := false
			e = newEnv(t, withRunner(failingRuns(1)), withBeforeRequest(func(r *http.Request) {
				if hooked || r.Method != http.MethodGet || r.URL.Path != issuePath(42) {
					return
				}
				hooked = true
				var err error
				if how == "a retry through the API" {
					_, err = e.Jobs.Retry(context.Background(), id, "user-1")
				} else {
					_, err = e.Jobs.Create(context.Background(), jobs.CreateRequest{Workspace: slug, Kind: jobs.KindFix,
						Body: jobs.Body{Issue: json.RawMessage("42")}, Source: jobs.Source{Kind: "api", By: "user-1"}})
				}
				if err != nil {
					t.Errorf("%s: %v", how, err)
				}
			}))
			id = e.failFix(42)
			e.comment(42, "bob", "try again")
			e.Hub.AddLabel(slug, 42, "af:fix")
			e.cycle()
			if !hooked {
				t.Fatal("the poll did not read the issue")
			}
			wantAttempts, wantJobs := 1, 2
			if how == "a retry through the API" {
				wantAttempts, wantJobs = 2, 1
			}
			if got := len(e.doc(id).Attempts); got != wantAttempts {
				t.Errorf("the failed job has %d attempts, want %d", got, wantAttempts)
			}
			if got := e.jobsFor(42); got != wantJobs {
				t.Errorf("%d jobs for 42, want %d", got, wantJobs)
			}
			if e.hasLabel(42, "af:fix") {
				t.Error("af:fix is still on 42")
			}
		})
	}
}

// TestARetryRefusedForAnotherReasonBehavesAsAnAnswerDoes_TS_19_27 verifies TS-19-27.
// Verifies: 19-REQ-4.7
func TestARetryRefusedForAnotherReasonBehavesAsAnAnswerDoes_TS_19_27(t *testing.T) {
	for _, refusal := range []error{jobs.ErrNotSubscribed, jobs.ErrTokenUnreadable, errors.New("disk full")} {
		t.Run(refusal.Error(), func(t *testing.T) {
			e := newEnv(t, withRunner(failingRuns(2)), withJobs(func(j Jobs) Jobs { return answerRefused{Jobs: j, err: refusal} }))
			id := e.failFix(42)
			e.failFix(43)
			for _, n := range []int{42, 43} {
				e.comment(n, "bob", "try again")
				e.Hub.AddLabel(slug, n, "af:fix")
			}
			before, comments := len(e.writes()), e.comments(42)
			e.cycle()
			if len(e.doc(id).Attempts) != 1 || e.comments(42) != comments {
				t.Errorf("%d attempts and %d comments, want no attempt and no comment", len(e.doc(id).Attempts), e.comments(42))
			}
			if !e.hasLabel(42, "af:fix") {
				t.Error("the label was taken")
			}
			if reads := len(e.Hub.RequestsTo(http.MethodGet, issuePath(43))); reads != 0 { // every refusal ends the cycle
				t.Errorf("43 was read %d times, want the cycle ended", reads)
			}
			if errors.Is(refusal, jobs.ErrNotSubscribed) {
				if got := e.writes()[before:]; len(got) != 0 {
					t.Errorf("the poll wrote %v, want nothing", got)
				}
			} else if len(e.Logs.find("WARN", "retry")) != 1 {
				t.Errorf("the failure records are %v, want one", e.Logs.find("WARN", "retry"))
			}
		})
	}
}
