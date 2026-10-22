package worker

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/worker/recovery"
)

// TestTheLandingPlanIsJournalledBeforeTheFirstPush_TS_14_29 verifies TS-14-29: when git pushes the branch,
// journal.json holds the plan of the landing, with the branch and the commit that is pushed.
// Verifies: 14-REQ-5.1
func TestTheLandingPlanIsJournalledBeforeTheFirstPush_TS_14_29(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	var atPush []*recovery.Plan
	e.runner.gitWrap = func(env []string) gitx.Runner {
		run := gitx.ExplicitEnvRunner(env)
		return func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
			if len(argv) > 1 && argv[1] == "push" {
				j, err := recovery.NewStore(e.dir).Load()
				if err != nil {
					t.Errorf("the journal cannot be read when the branch is pushed: %v", err)
				}
				atPush = append(atPush, j.Landing.Plan)
			}
			return run(ctx, dir, argv, stdin...)
		}
	}

	res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if len(atPush) != 1 || atPush[0] == nil {
		t.Fatalf("the plan when the branch was pushed: %v (%d pushes), want a plan at the one push", atPush, len(atPush))
	}
	plan, result := atPush[0], resultMap(t, res)
	if plan.Branch != result["branch"] || plan.Commit != result["commit"] || plan.Outcome != "landed" || plan.Verdict != result["verdict"] || plan.Closes != result["closes"] {
		t.Errorf("the plan %+v does not say what the landing reports: branch %v commit %v verdict %v closes %v", plan, result["branch"], result["commit"], result["verdict"], result["closes"])
	}
	if pulls := e.hub.Pulls(slug); len(pulls) != 1 || pulls[0].Title != plan.Title || pulls[0].Body != plan.Body || pulls[0].Draft != plan.Draft {
		t.Errorf("pull requests = %+v, want the one the plan describes (%q, draft %v)", pulls, plan.Title, plan.Draft)
	}
}

// TestAChangeThatIsOnHubsGitServerIsFinishedFromThePlanWithoutMeasuringLeasingOrRunningTheBrain_TS_14_30
// verifies TS-14-30.
// Verifies: 14-REQ-5.2
func TestAChangeThatIsOnHubsGitServerIsFinishedFromThePlanWithoutMeasuringLeasingOrRunningTheBrain_TS_14_30(t *testing.T) {
	for _, tc := range []struct {
		stage string
		// pulls and publishes are what the first delivery had done by then.
		pulls, publishes int
	}{
		{"pushed", 0, 0},
		{"hub_push", 0, 1},
		{"pull_request", 1, 1},
	} {
		t.Run("the first delivery stopped after "+tc.stage, func(t *testing.T) {
			e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
			spy := e.spyOnGit()
			e.stopAfter(tc.stage)
			j := journalOf(t, e)
			plan := j.Landing.Plan
			if plan == nil || len(e.hub.Pulls(slug)) != tc.pulls || len(e.hub.BranchPushes()) != tc.publishes {
				t.Fatalf("after the first delivery: plan %v, %d pull requests, %d branch pushes, want a plan, %d and %d", plan, len(e.hub.Pulls(slug)), len(e.hub.BranchPushes()), tc.pulls, tc.publishes)
			}
			leases, runs, commits := len(e.hub.Leases()), e.brainRuns(), len(spy.with("commit"))
			loops := countEvents(e, "run_start")

			res, err := e.deliver(2)

			if err != nil || envelope(t, res)["status"] != "done" {
				t.Fatalf("result %s, error %v", res.Envelope, err)
			}
			if got := len(e.hub.Leases()) - leases; got != 0 {
				t.Errorf("%d leases were taken, want none", got)
			}
			if got := e.brainRuns() - runs; got != 0 {
				t.Errorf("the brain was started %d times, want never", got)
			}
			if got := len(spy.with("commit")) - commits; got != 0 {
				t.Errorf("%d commits were made, want none: the commit is the one that was pushed", got)
			}
			if got := countEvents(e, "run_start") - loops; got != 0 {
				t.Errorf("the loop ran %d times, want never: nothing is measured", got)
			}
			if pulls := e.hub.Pulls(slug); len(pulls) != 1 || pulls[0].Head != plan.Branch || pulls[0].Title != plan.Title {
				t.Errorf("pull requests = %+v, want the one for %s", pulls, plan.Branch)
			}
			if pushes := e.hub.BranchPushes(); len(pushes) != 1 {
				t.Errorf("hub's branch push route was called %d times, want once", len(pushes))
			}
			if got := receivePacks(e); got != 1 {
				t.Errorf("%d pushes to hub's git server, want the one of the first delivery", got)
			}
			result := resultMap(t, res)
			pull, _ := result["pull_request"].(map[string]any)
			if result["branch"] != plan.Branch || result["commit"] != plan.Commit || result["verdict"] != plan.Verdict || result["outcome"] != "landed" || pull == nil || pull["url"] == "" {
				t.Errorf("result = %v, want the branch %s, the commit %.8s, the verdict %s and a pull request", result, plan.Branch, plan.Commit, plan.Verdict)
			}
			if tc.pulls == 1 {
				if want := j.Landing.PullRequest; want == nil || pull["number"] != float64(want.Number) {
					t.Errorf("the pull request of the result is %v, want the number %v that the first delivery opened", pull, want)
				}
			}
			if issue := e.issueNow(); !strings.Contains(strings.Join(issue.Labels, ","), "af:landed") || len(issue.Comments) != 1 {
				t.Errorf("issue 42 has the labels %v and %d comments, want landed and one comment", issue.Labels, len(issue.Comments))
			}
			if leases := e.hub.Leases(); !leases[0].Released {
				t.Error("the lease of the first delivery was not released")
			}
			if s := e.hub.Sessions(); len(s) != 1 || s[0].Status != "completed" {
				t.Errorf("sessions = %+v, want the one of the attempt, completed", s)
			}
		})
	}
}

// countEvents is how many events of the type the attempts of the environment have emitted.
func countEvents(e *env, typ string) int {
	n := 0
	for _, ev := range e.sink.emitted() {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

// TestAtMostOnePullRequestOneCreatedIssueAndOneCommentPerMarkerWhateverTheDeliveriesStopAt_TS_14_31
// verifies TS-14-31, a property: for deliveries that are stopped at generated stages of a landing, and then
// finished, the forge holds at most one pull request, at most one issue made for a report, and at most one
// comment for each marker.
// Verifies: 14-REQ-5.3
func TestAtMostOnePullRequestOneCreatedIssueAndOneCommentPerMarkerWhateverTheDeliveriesStopAt_TS_14_31(t *testing.T) {
	check := func(t *testing.T, e *env, createdIssues int) {
		t.Helper()
		if pulls := e.hub.Pulls(slug); len(pulls) > 1 {
			t.Errorf("%d pull requests, want at most one", len(pulls))
		}
		issues := e.hub.Issues(slug)
		if len(issues)-1 > createdIssues { // issue 42 was there
			t.Errorf("%d issues were created, want at most %d", len(issues)-1, createdIssues)
		}
		seen := map[string]int{}
		for _, issue := range issues {
			for _, c := range issue.Comments {
				if i := strings.Index(c.Body, "<!-- agent-fox"); i >= 0 {
					seen[strings.TrimSpace(c.Body[i:])]++
				}
			}
		}
		for marker, n := range seen {
			if n > 1 {
				t.Errorf("the marker %q is on %d comments, want at most one", marker, n)
			}
		}
	}
	// run stops the deliveries after the stages in turn, then finishes the attempt.
	run := func(t *testing.T, e *env, kind jobs.Kind, input jobs.Input, stages []string) {
		t.Helper()
		delivery := 0
		for _, stage := range stages {
			delivery++
			e.runner.abortAfter = stage
			_, err := e.runner.Run(context.Background(), e.request(kind, input, func(r *jobs.Request) { r.Delivery = delivery }))
			e.runner.abortAfter = ""
			var stopped stageAbort
			if !errors.As(err, &stopped) { // the delivery came to its end before the stage
				if err != nil && !jobs.IsTransient(err) {
					t.Fatalf("delivery %d: %v", delivery, err)
				}
				return
			}
		}
		for range 3 {
			delivery++
			res, err := e.runner.Run(context.Background(), e.request(kind, input, func(r *jobs.Request) { r.Delivery = delivery }))
			if err == nil && len(res.Envelope) > 0 {
				return
			}
		}
		t.Fatalf("the attempt did not come to an end in %d deliveries", delivery)
	}

	rng := rand.New(rand.NewSource(14031))
	fixStages := []string{"plan_recorded", "pushed", "hub_push", "pull_request"}
	for i := range 6 {
		var stages []string
		for range rng.Intn(3) + 1 {
			stages = append(stages, fixStages[rng.Intn(len(fixStages))])
		}
		t.Run(fmt.Sprintf("a fix %d: %v", i, stages), func(t *testing.T) {
			e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
			run(t, e, jobs.KindFix, issueInput("42"), stages)
			check(t, e, 0)
		})
	}
	triageStages := []string{"briefed", "submission_accepted", "created_issue"}
	for i := range 4 {
		var stages []string
		for range rng.Intn(3) + 1 {
			stages = append(stages, triageStages[rng.Intn(len(triageStages))])
		}
		t.Run(fmt.Sprintf("a triage of text %d: %v", i, stages), func(t *testing.T) {
			e := loopEnv(t, triageFiles, nil, nil, submitStep(probableDiagnosis))
			run(t, e, jobs.KindTriage, jobs.Input{Title: "Run starts the wrong thing", Text: "The app crashes on submit."}, stages)
			check(t, e, 1)
		})
	}
}

// TestATriageOfTextThatWasInterruptedAfterHubCreatedItsIssueLandsIntoThatIssue_TS_14_38 verifies TS-14-38.
// Verifies: 14-REQ-5.8, 14-REQ-5.3
func TestATriageOfTextThatWasInterruptedAfterHubCreatedItsIssueLandsIntoThatIssue_TS_14_38(t *testing.T) {
	e := loopEnv(t, triageFiles, nil, nil, submitStep(probableDiagnosis))
	input := jobs.Input{Title: "Run starts the wrong thing", Text: "The app crashes on submit."}
	e.runner.abortAfter = "created_issue"
	if _, err := e.runner.Run(context.Background(), e.request(jobs.KindTriage, input)); err == nil {
		t.Fatal("the first delivery ran to its end")
	}
	e.runner.abortAfter = ""
	created := journalOf(t, e).Landing.CreatedIssue
	if created == nil || created.Number == 0 {
		t.Fatalf("the journal records the created issue %v, want its number", created)
	}
	if n := len(e.hub.Issues(slug)); n != 2 {
		t.Fatalf("the repository holds %d issues after the first delivery, want issue 42 and the one that was created", n)
	}
	before, _ := e.hub.IssueOf(slug, created.Number)

	res, err := e.run(e.request(jobs.KindTriage, input, func(r *jobs.Request) { r.Delivery = 2 }))

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if n := len(e.hub.Issues(slug)); n != 2 {
		t.Errorf("the repository holds %d issues, want only the one that was created: the diagnosis lands into it", n)
	}
	after, _ := e.hub.IssueOf(slug, created.Number)
	if !strings.Contains(after.Body, "Run starts the wrong thing") || after.Body == before.Body && !strings.Contains(before.Body, "Run starts the wrong thing") {
		t.Errorf("the body of the issue is %q, want the diagnosis in it", after.Body)
	}
	if issue := resultMap(t, res)["issue"]; issue == nil || fmt.Sprint(issue.(map[string]any)["number"]) != fmt.Sprint(float64(created.Number)) {
		t.Errorf("the result names the issue %v, want #%d", issue, created.Number)
	}
}

// TestAPublishedBranchWhosePlanWantsNoPullRequestGetsNone pins that the pull request of the plan is the plan's:
// a landing of a branch alone is finished with the publication of the branch.
// Verifies: 14-REQ-5.2
func TestAPublishedBranchWhosePlanWantsNoPullRequestGetsNone(t *testing.T) {
	e := loopEnv(t, fixFiles, map[string]string{"AF_FIX_LAND": "branch"}, nil, fixSteps()...)
	e.stopAfter("pushed")
	if plan := journalOf(t, e).Landing.Plan; plan == nil || plan.PullRequest {
		t.Fatalf("the plan is %+v, want one that wants no pull request", plan)
	}

	res, err := e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if pulls := e.hub.Pulls(slug); len(pulls) != 0 {
		t.Errorf("%d pull requests, want none", len(pulls))
	}
	if pushes := e.hub.BranchPushes(); len(pushes) != 1 {
		t.Errorf("hub's branch push route was called %d times, want once", len(pushes))
	}
	if result := resultMap(t, res); result["pull_request"] != nil || result["pushed"] != true {
		t.Errorf("result = %v, want the branch pushed and no pull request", result)
	}
}

// TestAnIssueThatHubCreatedIsLandedIntoEvenWhenTheResumeIsDiscarded pins that the issue outlives the clone: a
// delivery that begins from a fresh clone goes on with the issue the journal records.
// Verifies: 14-REQ-5.3
func TestAnIssueThatHubCreatedIsLandedIntoEvenWhenTheResumeIsDiscarded(t *testing.T) {
	e := loopEnv(t, triageFiles, nil, nil, submitStep(probableDiagnosis))
	input := jobs.Input{Title: "Run starts the wrong thing", Text: "The app crashes on submit."}
	e.runner.abortAfter = "created_issue"
	if _, err := e.runner.Run(context.Background(), e.request(jobs.KindTriage, input)); err == nil {
		t.Fatal("the first delivery ran to its end")
	}
	e.runner.abortAfter = ""
	created := journalOf(t, e).Landing.CreatedIssue
	if created == nil {
		t.Fatal("the journal records no created issue")
	}
	editJournal(t, e, func(j *recovery.Journal) { j.Forge = "Other/Repo" }) // the resume is discarded

	res, err := e.run(e.request(jobs.KindTriage, input, func(r *jobs.Request) { r.Delivery = 2 }))

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if w := warningOf(envelope(t, res), "resume_discarded"); w == nil {
		t.Error("the resume was not discarded")
	}
	if n := len(e.hub.Issues(slug)); n != 2 {
		t.Errorf("the repository holds %d issues, want issue 42 and the one that was created", n)
	}
}

// TestAnIssueThatHubCreatedForAFixOfATextIsTheIssueOfTheResumedPullRequest pins the same for a fix: the
// issue made for a text is made once, and the pull request refers to it.
// Verifies: 14-REQ-5.3
func TestAnIssueThatHubCreatedForAFixOfATextIsTheIssueOfTheResumedPullRequest(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	input := jobs.Input{Title: "Greeting is wrong", Text: "The greeting says helo."}
	e.runner.abortAfter = "created_issue"
	if _, err := e.runner.Run(context.Background(), e.request(jobs.KindFix, input)); err == nil {
		t.Fatal("the first delivery ran to its end")
	}
	e.runner.abortAfter = ""
	created := journalOf(t, e).Landing.CreatedIssue
	if created == nil || created.Number == 0 {
		t.Fatalf("the journal records the created issue %v, want its number", created)
	}

	res, err := e.run(e.request(jobs.KindFix, input, func(r *jobs.Request) { r.Delivery = 2 }))

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if n := len(e.hub.Issues(slug)); n != 2 {
		t.Errorf("the repository holds %d issues, want issue 42 and the one that was created", n)
	}
	if pulls := e.hub.Pulls(slug); len(pulls) != 1 || !strings.Contains(pulls[0].Body, fmt.Sprintf("/issues/%d", created.Number)) {
		t.Errorf("pull requests = %+v, want one that refers to the issue #%d", pulls, created.Number)
	}
}

// TestAParkedChangeThatWasPushedGetsItsDraftPullRequestFromThePlan pins what the plan carries of a change
// that did not land: the outcome parked, the title and the body of the draft pull request, and the
// flag that says it is a draft.
// Verifies: 14-REQ-5.2
func TestAParkedChangeThatWasPushedGetsItsDraftPullRequestFromThePlan(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.useScript(scriptRounds(t, refusedRound()))
	e.stopAfter("pushed")
	plan := journalOf(t, e).Landing.Plan
	if plan == nil || plan.Outcome != "parked" || !plan.Draft || !strings.HasPrefix(plan.Title, "wip: ") {
		t.Fatalf("the plan is %+v, want a parked change with a draft pull request", plan)
	}

	res, err := e.deliver(2)

	if err != nil {
		t.Fatal(err)
	}
	pulls := e.hub.Pulls(slug)
	if len(pulls) != 1 || !pulls[0].Draft || pulls[0].Title != plan.Title || pulls[0].Body != plan.Body {
		t.Errorf("pull requests = %+v, want the draft the plan describes", pulls)
	}
	if env := envelope(t, res); env["status"] != "unverified" || resultMap(t, res)["outcome"] != "parked" {
		t.Errorf("status %v and outcome %v, want a parked change", env["status"], resultMap(t, res)["outcome"])
	}
	if issue := e.issueNow(); !strings.Contains(strings.Join(issue.Labels, ","), "af:needs-human") {
		t.Errorf("issue 42 has the labels %v, want needs-human for a parked change", issue.Labels)
	}
}
