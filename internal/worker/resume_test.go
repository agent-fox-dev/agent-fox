package worker

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

// scriptRounds writes a script of the scripted brain with a round of steps for each run of the brain.
func scriptRounds(t *testing.T, rounds ...[]map[string]any) string {
	t.Helper()
	list := make([]map[string]any, len(rounds))
	for i, steps := range rounds {
		list[i] = map[string]any{"steps": steps}
	}
	data, err := json.Marshal(map[string]any{"rounds": list})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// refusedRound is a round of a brain whose change the gate refuses: the greeting is still wrong.
func refusedRound() []map[string]any {
	return []map[string]any{writeStep("src/greet.sh", "echo hallo\n"), submitStep(fixReport)}
}

// useScript makes the loop of the environment run the scripted brain with the script.
func (e *env) useScript(script string) {
	e.runner.loopFlags = func(*attempt) []string { return []string{"--brain", "scripted", "--script", script} }
}

// atRelease calls f when the attempt gives its lease back: the clone is still there then.
func (e *env) atRelease(f func()) {
	e.sink.hook = func(typ string, data json.RawMessage) {
		var step struct{ Name string }
		if typ == "step" && json.Unmarshal(data, &step) == nil && step.Name == "release" {
			f()
		}
	}
}

// brainRuns are the times the brain was started: the loop says it in a step event.
func (e *env) brainRuns() int {
	n := 0
	for _, ev := range e.sink.emitted() {
		var step struct{ Name string }
		if ev.Type == "step" && json.Unmarshal(ev.Data, &step) == nil && step.Name == "brain" {
			n++
		}
	}
	return n
}

// runStateOf is fix.json of the run of the attempt, while the clone is there.
func (e *env) runStateOf(t *testing.T) fix.State {
	t.Helper()
	st, err := fix.LoadState(e.runDirOf())
	if err != nil {
		t.Fatalf("fix.json: %v", err)
	}
	return st
}

// TestAnUnpushedLandingCommitIsBroughtBackIntoTheWorkingTreeAndTheBranchKeepsItsName_TS_14_18
// verifies TS-14-18.
// Verifies: 14-REQ-3.2
func TestAnUnpushedLandingCommitIsBroughtBackIntoTheWorkingTreeAndTheBranchKeepsItsName_TS_14_18(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.stopAfter("plan_recorded")
	plan := journalOf(t, e).Landing.Plan
	if plan == nil || len(localBranches(t, e.dir)) != 1 {
		t.Fatalf("the first delivery left the plan %v and the branches %v, want a plan and the one branch of the landing", plan, localBranches(t, e.dir))
	}
	runs := e.brainRuns()
	clone := filepath.Join(e.dir, cloneDirName)
	var status, current string
	var branches []string
	flags := e.runner.loopFlags
	e.runner.loopFlags = func(a *attempt) []string {
		git := func(args ...string) string {
			out, _ := exec.Command("git", append([]string{"-C", clone}, args...)...).Output()
			return strings.TrimSpace(string(out))
		}
		status, current = git("status", "--porcelain"), git("rev-parse", "--abbrev-ref", "HEAD")
		branches = strings.Fields(git("for-each-ref", "--format=%(refname:short)", "refs/heads"))
		return flags(a)
	}

	res, err := e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if got := e.brainRuns() - runs; got != 0 {
		t.Errorf("the brain was started %d times by the second delivery, want never", got)
	}
	if current != "main" || !strings.Contains(status, "src/greet.sh") || !slices.Equal(branches, []string{"main"}) {
		t.Errorf("when the loop began the clone was on %q with the status %q and the branches %v, want the base with the change in the working tree and the parked branch deleted", current, status, branches)
	}
	result := resultMap(t, res)
	if result["branch"] != plan.Branch || strings.HasSuffix(result["branch"].(string), "-2") || result["pull_request"] == nil {
		t.Errorf("the landing is on %v with the pull request %v, want the branch %s that the first delivery named, and no -2", result["branch"], result["pull_request"], plan.Branch)
	}
	if got := receivePacks(e); got != 1 {
		t.Errorf("%d pushes, want the one of the second delivery", got)
	}
	if pulls := e.hub.Pulls(slug); len(pulls) != 1 || pulls[0].Head != plan.Branch {
		t.Errorf("pull requests = %+v, want one for %s", pulls, plan.Branch)
	}
}

// TestWhenTheBrainRunsAgainTheCloneStartsFromTheBaseHeadAndTheRoundCountCarriesOn_TS_14_19 verifies
// TS-14-19.
// Verifies: 14-REQ-3.3, 14-REQ-3.4
func TestWhenTheBrainRunsAgainTheCloneStartsFromTheBaseHeadAndTheRoundCountCarriesOn_TS_14_19(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.useScript(scriptRounds(t, refusedRound(), fixSteps()))
	e.stopAfter("submission_accepted#2") // the second round: fix.json has one round, and the tree holds the change of the first
	clone := filepath.Join(e.dir, cloneDirName)
	runID := e.hub.Sessions()[0].RunID
	if st := e.runStateOf(t); st.Rounds != 1 {
		t.Fatalf("fix.json records %d rounds after the first delivery, want 1", st.Rounds)
	}
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", clone, "-c", "user.name=x", "-c", "user.email=x@example.com"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if !strings.Contains(git("status", "--porcelain"), "src/greet.sh") {
		t.Fatal("the clone holds no change of the first round")
	}
	git("branch", "fix/left-by-the-run")
	git("commit", "-q", "--allow-empty", "-m", "left\n\nAgent-Fox-Run: "+runID) // moves main: undo it after the branch is made
	git("branch", "-f", "fix/left-by-the-run", "HEAD")
	git("reset", "-q", "--hard", "HEAD~1")

	var status string
	var branches []string
	var rounds int
	var firstBrief, secondBrief string
	flags := e.runner.loopFlags
	e.runner.loopFlags = func(a *attempt) []string {
		status = git("status", "--porcelain")
		branches = strings.Fields(git("for-each-ref", "--format=%(refname:short)", "refs/heads"))
		return flags(a)
	}
	e.atRelease(func() {
		rounds = e.runStateOf(t).Rounds
		first, _ := os.ReadFile(filepath.Join(e.runDirOf(), "brief.2.md"))
		second, _ := os.ReadFile(filepath.Join(e.runDirOf(), "brief.3.md"))
		firstBrief, secondBrief = string(first), string(second)
	})

	res, err := e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if status != "" || !slices.Equal(branches, []string{"main"}) {
		t.Errorf("when the loop began the status was %q and the branches %v, want the clone clean on the base alone", status, branches)
	}
	if rounds != 3 {
		t.Errorf("fix.json records %d rounds after the second delivery, want the 1 of the first and the 2 it ran", rounds)
	}
	if firstBrief == "" || strings.Contains(firstBrief, "verdict: still_failing)") || !strings.Contains(secondBrief, "verdict: still_failing)") {
		t.Errorf("the first resumed round (brief.2.md) carries a refusal: %v; the second (brief.3.md) carries it: %v", strings.Contains(firstBrief, "verdict: still_failing)"), strings.Contains(secondBrief, "verdict: still_failing)"))
	}
}

// TestARefusalInAResumedLoopIsGivenToTheNextRoundAndTheLoopEndsThroughFinish_TS_14_20 verifies
// TS-14-20.
// Verifies: 14-REQ-3.4
func TestARefusalInAResumedLoopIsGivenToTheNextRoundAndTheLoopEndsThroughFinish_TS_14_20(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.useScript(scriptRounds(t, refusedRound(), fixSteps()))
	e.stopAfter("briefed")
	var rounds int
	var firstBrief, secondBrief string
	e.atRelease(func() {
		rounds = e.runStateOf(t).Rounds
		first, _ := os.ReadFile(filepath.Join(e.runDirOf(), "brief.1.md"))
		second, _ := os.ReadFile(filepath.Join(e.runDirOf(), "brief.2.md"))
		firstBrief, secondBrief = string(first), string(second)
	})

	res, err := e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if rounds != 2 || strings.Contains(firstBrief, "verdict: still_failing)") || !strings.Contains(secondBrief, "verdict: still_failing)") {
		t.Errorf("rounds %d; the first round carries a refusal: %v; the second carries it: %v; want 2 rounds and the refusal in the second alone", rounds, strings.Contains(firstBrief, "verdict: still_failing)"), strings.Contains(secondBrief, "verdict: still_failing)"))
	}
	if result := resultMap(t, res); result["outcome"] != "landed" || result["pull_request"] == nil {
		t.Errorf("result = %v, want the change landed with a pull request", result)
	}
}

// TestAResumedTriageReusesTheCloneAndRunsTheLoopAgainInTheSameRunDirectory_TS_14_21 verifies TS-14-21.
// Verifies: 14-REQ-3.5
func TestAResumedTriageReusesTheCloneAndRunsTheLoopAgainInTheSameRunDirectory_TS_14_21(t *testing.T) {
	e := loopEnv(t, triageFiles, nil, nil, submitStep(probableDiagnosis))
	spy := e.spyOnGit()
	req := func(n int) jobs.Request {
		return e.request(jobs.KindTriage, issueInput("42"), func(r *jobs.Request) { r.Delivery = n })
	}
	e.runner.abortAfter = "briefed"
	if _, err := e.runner.Run(t.Context(), req(1)); err == nil {
		t.Fatal("the first delivery ran to its end")
	}
	e.runner.abortAfter = ""
	runID := e.hub.Sessions()[0].RunID
	runDir := filepath.Join(e.dir, cloneDirName, ".agent-fox", "run", runID)
	briefBefore, err := os.ReadFile(filepath.Join(runDir, "brief.md"))
	if err != nil {
		t.Fatalf("the first delivery left no brief: %v", err)
	}
	var briefAtStart []byte
	flags := e.runner.loopFlags
	e.runner.loopFlags = func(a *attempt) []string {
		briefAtStart, _ = os.ReadFile(filepath.Join(runDir, "brief.md"))
		return flags(a)
	}

	res, err := e.run(req(2))

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if n := clonesOf(spy); n != 1 {
		t.Errorf("%d clones, want only the first delivery's", n)
	}
	if string(briefAtStart) != string(briefBefore) {
		t.Error("the brief of the run was rendered again")
	}
	if got := envelope(t, res)["run_id"]; got != runID {
		t.Errorf("the run is %v, want the run %s of the first delivery", got, runID)
	}
	if n := e.brainRuns(); n != 1 {
		t.Errorf("the brain was started %d times, want once, by the second delivery", n)
	}
}

// TestAChangeThatCouldBeInTwoPlacesOrOnAForeignCommitIsNotGuessedAt pins the restore of a change: a clone
// that holds the change of the run in its working tree and on a branch, or on a branch with a commit that
// is not the run's, ends the attempt with a failure of category internal and publishes nothing.
// Verifies: 14-REQ-3.2, 14-REQ-2.7
func TestAChangeThatCouldBeInTwoPlacesOrOnAForeignCommitIsNotGuessedAt(t *testing.T) {
	for name, tc := range map[string]struct {
		spoil func(t *testing.T, clone, runID string)
		want  string
	}{
		"a branch and a working tree that is not clean": {func(t *testing.T, clone, _ string) {
			if err := os.WriteFile(filepath.Join(clone, "extra.txt"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "more than one place"},
		"a commit of somebody else under the commit of the run": {func(t *testing.T, clone, runID string) {
			for _, msg := range []string{"a foreign commit", "the run again\n\nAgent-Fox-Run: " + runID} {
				gitOut(t, clone, "-c", "user.name=x", "-c", "user.email=x@example.com", "commit", "-q", "--allow-empty", "-m", msg)
			}
		}, "Agent-Fox-Run"},
	} {
		t.Run(name, func(t *testing.T) {
			e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
			e.stopAfter("plan_recorded")
			tc.spoil(t, filepath.Join(e.dir, cloneDirName), e.hub.Sessions()[0].RunID)

			res, err := e.deliver(2)

			if err != nil {
				t.Fatal(err)
			}
			category, message, retryable, _ := errorOf(envelope(t, res))
			if envelope(t, res)["status"] != "failed" || category != "internal" || retryable || !strings.Contains(message, tc.want) {
				t.Errorf("envelope %s, want a failed one of category internal, not retryable, that names %q", res.Envelope, tc.want)
			}
			if n := receivePacks(e); n != 0 || len(e.hub.Pulls(slug)) != 0 {
				t.Errorf("%d pushes and %d pull requests, want none", n, len(e.hub.Pulls(slug)))
			}
		})
	}
}

// TestASubmissionThatNoLongerPassesItsCheckAgainstTheTreeMakesTheBrainRunAgain pins the check that follows
// the restore of the change: a report that cites a test the tree no longer has is not one to measure.
// Verifies: 14-REQ-3.1
func TestASubmissionThatNoLongerPassesItsCheckAgainstTheTreeMakesTheBrainRunAgain(t *testing.T) {
	const reportWithTests = `{"subject":"say hello","root_cause":"the script says helo","change":"say hello","files":[{"path":"src/greet.sh","why":"the word"}],"tests":[{"path":"tests/hello_test.sh","proves":"the greeting"}]}`
	steps := []map[string]any{
		writeStep("src/greet.sh", "echo hello\n"), writeStep("tests/hello_test.sh", helloTest),
		{"tool": "execute", "args": map[string]any{"command": "make box-commit"}, "expect": "ok"}, submitStep(reportWithTests),
	}
	e := loopEnv(t, fixFiles, nil, nil, steps...)
	e.stopAfter("applied")
	if err := os.Remove(filepath.Join(e.dir, cloneDirName, "tests", "hello_test.sh")); err != nil {
		t.Fatal(err)
	}
	runs := e.brainRuns()

	res, err := e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if got := e.brainRuns() - runs; got != 1 {
		t.Errorf("the brain was started %d times by the second delivery, want once: the test of the report is gone", got)
	}
}

// gitOut runs git in the clone and returns what it printed.
func gitOut(t *testing.T, clone string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", clone}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestABranchThatALandingMadeAndDiedBeforeItCommittedDoesNotMakeTheLandingTakeAnotherName pins 14-REQ-3.2 for the
// window between the branch that a landing makes and its commit: a process that died there leaves a local branch with no
// commit of its own, which carries no trailer to be known by, and which the name of the landing would otherwise be
// made to avoid with a suffix.
// Verifies: 14-REQ-3.2
func TestABranchThatALandingMadeAndDiedBeforeItCommittedDoesNotMakeTheLandingTakeAnotherName(t *testing.T) {
	for name, onIt := range map[string]bool{"the clone is on the empty branch": true, "the clone is on the base": false} {
		t.Run(name, func(t *testing.T) {
			e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
			e.stopAfter("applied")
			clone := filepath.Join(e.dir, cloneDirName)
			// As the landing makes it: marked as the run's, and at the commit the run began at, with the change in the tree.
			gitOut(t, clone, "config", "branch.fix/42-greeting-wrong."+fix.BranchRunKey, e.hub.Sessions()[0].RunID)
			if onIt {
				gitOut(t, clone, "checkout", "-b", "fix/42-greeting-wrong")
			} else {
				gitOut(t, clone, "branch", "fix/42-greeting-wrong")
			}

			res, err := e.deliver(2)

			if err != nil || envelope(t, res)["status"] != "done" {
				t.Fatalf("result %s, error %v", res.Envelope, err)
			}
			if branch := resultMap(t, res)["branch"]; branch != "fix/42-greeting-wrong" {
				t.Errorf("the landing is on the branch %v, want fix/42-greeting-wrong without a suffix", branch)
			}
			if pulls := e.hub.Pulls(slug); len(pulls) != 1 || pulls[0].Head != "fix/42-greeting-wrong" {
				t.Errorf("pull requests %+v, want one from the branch with the name the run would have had", pulls)
			}
		})
	}
}

// TestABranchThatALandingMadeAndDiedBeforeItCommittedIsDeletedBeforeTheBrainRunsAgain pins that the loop that is run again
// from the base does not find the debris of the landing before it either.
// Verifies: 14-REQ-3.2
func TestABranchThatALandingMadeAndDiedBeforeItCommittedIsDeletedBeforeTheBrainRunsAgain(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.stopAfter("briefed")
	clone := filepath.Join(e.dir, cloneDirName)
	gitOut(t, clone, "config", "branch.fix/42-greeting-wrong."+fix.BranchRunKey, e.hub.Sessions()[0].RunID)
	gitOut(t, clone, "branch", "fix/42-greeting-wrong")

	res, err := e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if branch := resultMap(t, res)["branch"]; branch != "fix/42-greeting-wrong" {
		t.Errorf("the landing is on the branch %v, want fix/42-greeting-wrong without a suffix", branch)
	}
}
