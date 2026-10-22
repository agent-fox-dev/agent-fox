package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

// TestAnAppliedFixReportIsMeasuredOnceInOneNewLeaseAndLandedWithoutARoundOfTheBrain_TS_14_32 verifies
// TS-14-32.
// Verifies: 14-REQ-5.4
func TestAnAppliedFixReportIsMeasuredOnceInOneNewLeaseAndLandedWithoutARoundOfTheBrain_TS_14_32(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.stopAfter("applied")
	leases, runs := len(e.hub.Leases()), e.brainRuns()

	res, err := e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if got := len(e.hub.Leases()) - leases; got != 1 {
		t.Errorf("the second delivery took %d leases, want one", got)
	}
	if got := e.brainRuns() - runs; got != 0 {
		t.Errorf("the brain was started %d times, want never", got)
	}
	if pulls := e.hub.Pulls(slug); len(pulls) != 1 {
		t.Errorf("%d pull requests, want one", len(pulls))
	}
	if issue := e.issueNow(); len(issue.Comments) != 1 || !strings.Contains(strings.Join(issue.Labels, ","), "af:landed") {
		t.Errorf("issue 42 has the labels %v and %d comments, want landed and one comment", issue.Labels, len(issue.Comments))
	}
}

// TestAChangeThatTheGateRefusesOnTheRemeasureIsParkedWithoutARoundOfTheBrain_TS_14_33 verifies TS-14-33.
// Verifies: 14-REQ-5.4
func TestAChangeThatTheGateRefusesOnTheRemeasureIsParkedWithoutARoundOfTheBrain_TS_14_33(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.stopAfter("applied")
	// The change is not the one that was applied any more: the gate refuses it now.
	if err := os.WriteFile(filepath.Join(e.dir, cloneDirName, "src", "greet.sh"), []byte("echo hallo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runs := e.brainRuns()

	res, err := e.deliver(2)

	if err != nil {
		t.Fatal(err)
	}
	result := resultMap(t, res)
	if envelope(t, res)["status"] != "unverified" || result["outcome"] != "parked" || result["verdict"] != "still_failing" {
		t.Errorf("status %v, outcome %v, verdict %v, want a parked change that the gate refused", envelope(t, res)["status"], result["outcome"], result["verdict"])
	}
	if got := e.brainRuns() - runs; got != 0 {
		t.Errorf("the brain was started %d times, want never: a refused re-measure parks", got)
	}
	if pulls := e.hub.Pulls(slug); len(pulls) != 1 || !pulls[0].Draft {
		t.Errorf("pull requests = %+v, want one draft", pulls)
	}
}

// TestABaselineResultThatTheCloneLacksIsRunAgainInTheBoxWithTheChangeSetAsideAndBackAfterwards_TS_14_34
// verifies TS-14-34. A gate result is named by the tree, the checks and the mode, so the baseline that is run
// again has the id of the one that was lost: it is there again, fix.json names it, and the change is measured
// against it. A fix.json that names a result that was never there is made to name the one that ran.
// Verifies: 14-REQ-5.5
func TestABaselineResultThatTheCloneLacksIsRunAgainInTheBoxWithTheChangeSetAsideAndBackAfterwards_TS_14_34(t *testing.T) {
	for name, named := range map[string]string{"the result was removed": "", "fix.json names a result that never was": "0123456789ab"} {
		t.Run(name, func(t *testing.T) {
			e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
			e.stopAfter("applied")
			state := e.runStateOf(t)
			if state.BaselineGateID == "" || state.BaselinePassed {
				t.Fatalf("the run pins the baseline %q (passed %v), want one that failed, as the repository's gate does", state.BaselineGateID, state.BaselinePassed)
			}
			gate := filepath.Join(e.dir, cloneDirName, ".agent-fox", "gate", state.BaselineGateID)
			if named == "" {
				if err := os.RemoveAll(gate); err != nil {
					t.Fatal(err)
				}
			} else {
				pinned := state
				pinned.BaselineGateID, pinned.BaselinePassed = named, true
				if err := pinned.Write(e.runDirOf()); err != nil {
					t.Fatal(err)
				}
			}
			var restored, pinned bool
			var landed, stashes string
			e.atRelease(func() {
				_, err := os.Stat(filepath.Join(gate, "result.json"))
				restored = err == nil
				st := e.runStateOf(t)
				pinned = st.BaselineGateID == state.BaselineGateID && !st.BaselinePassed
				clone := filepath.Join(e.dir, cloneDirName)
				landed = strings.TrimSpace(gitOut(t, clone, "show", "fix/42-greeting-wrong:src/greet.sh"))
				stashes = strings.TrimSpace(gitOut(t, clone, "stash", "list"))
			})

			res, err := e.deliver(2)

			if err != nil || envelope(t, res)["status"] != "done" {
				t.Fatalf("result %s, error %v", res.Envelope, err)
			}
			if !restored || !pinned {
				t.Errorf("the stored baseline is back: %v; fix.json names it, as a baseline that failed (the tree it ran on had no change): %v", restored, pinned)
			}
			if verdict := resultMap(t, res)["verdict"]; verdict == "unverified" {
				t.Errorf("verdict %v, want the change measured against the baseline", verdict)
			}
			if landed != "echo hello" || stashes != "" {
				t.Errorf("the branch holds src/greet.sh %q and the clone the stashes %q, want the change of the run put back, landed, and nothing left set aside", landed, stashes)
			}
		})
	}
}

// TestAResumedChangeThatIsNothingOnceTheProtectedPathsAreBackEndsFailedWithNoResult_TS_14_35 verifies
// TS-14-35.
// Verifies: 14-REQ-5.6
func TestAResumedChangeThatIsNothingOnceTheProtectedPathsAreBackEndsFailedWithNoResult_TS_14_35(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.stopAfter("applied")
	clone := filepath.Join(e.dir, cloneDirName)
	// What is applied is only under a protected path now.
	gitOut(t, clone, "checkout", "--", "src/greet.sh")
	if err := os.Remove(filepath.Join(clone, "tests", "hello_test.sh")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(clone, ".specs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, ".specs", "x.md"), []byte("protected\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := e.deliver(2)

	if err != nil {
		t.Fatal(err)
	}
	category, _, retryable, _ := errorOf(envelope(t, res))
	if envelope(t, res)["status"] != "failed" || category != "no_result" || retryable {
		t.Errorf("envelope %s, want a failed one of category no_result that is not retryable", res.Envelope)
	}
	if n := receivePacks(e); n != 0 || len(e.hub.Pulls(slug)) != 0 {
		t.Errorf("%d pushes and %d pull requests, want none: nothing is landed or parked", n, len(e.hub.Pulls(slug)))
	}
}

// TestAResumeThatRunsTheBrainAgainRunsALostBaselineAgainOnTheCleanTree pins that a baseline is run again
// for every stage of a resume that measures, and that a tree with no change has nothing set aside.
// Verifies: 14-REQ-5.5
func TestAResumeThatRunsTheBrainAgainRunsALostBaselineAgainOnTheCleanTree(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.stopAfter("briefed")
	state := e.runStateOf(t)
	gate := filepath.Join(e.dir, cloneDirName, ".agent-fox", "gate", state.BaselineGateID)
	if err := os.RemoveAll(gate); err != nil {
		t.Fatal(err)
	}
	var restored bool
	e.atRelease(func() {
		_, err := os.Stat(filepath.Join(gate, "result.json"))
		restored = err == nil
	})

	res, err := e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if verdict := resultMap(t, res)["verdict"]; !restored || verdict == "unverified" {
		t.Errorf("the baseline is back: %v, the verdict is %v, want the change measured against the baseline", restored, verdict)
	}
}

// questionReport is a fix question.
const questionReport = `{"question":{"text":"Which greeting?","options":["hello","hi"]}}`

// TestAResumedQuestionTakesNoLeaseAndTheIssueHasExactlyOneQuestionComment_TS_14_36 verifies TS-14-36.
// Verifies: 14-REQ-5.7
func TestAResumedQuestionTakesNoLeaseAndTheIssueHasExactlyOneQuestionComment_TS_14_36(t *testing.T) {
	for name, comment := range map[string]bool{"the first delivery had not commented": false, "the first delivery had already commented": true} {
		t.Run(name, func(t *testing.T) {
			e := loopEnv(t, fixFiles, nil, nil, submitStep(questionReport))
			e.stopAfter("submission_accepted")
			if comment {
				req := e.request(jobs.KindFix, issueInput("42"))
				marker := fmt.Sprintf("<!-- agent-fox job=%s attempt=%d kind=question -->", req.JobID, req.Attempt)
				issue, _ := e.hub.IssueOf(slug, 42)
				issue.Comments = append(issue.Comments, fakehub.Comment{Author: "agent-fox", Body: "**agent-fox needs an answer to go on.**\n\n" + marker})
				e.hub.AddIssue(slug, issue)
			}
			leases, runs := len(e.hub.Leases()), e.brainRuns()

			res, err := e.deliver(2)

			if err != nil {
				t.Fatal(err)
			}
			if envelope(t, res)["status"] != "needs_human" {
				t.Fatalf("envelope %s, want needs_human", res.Envelope)
			}
			if got := len(e.hub.Leases()) - leases; got != 0 {
				t.Errorf("%d leases were taken, want none", got)
			}
			if got := e.brainRuns() - runs; got != 0 {
				t.Errorf("the brain was started %d times, want never", got)
			}
			result := resultMap(t, res)
			if box, ok := result["box"]; !ok || box != nil {
				t.Errorf("the result names the box %v, want none: no box was opened to ask the question", box)
			}
			if result["outcome"] != "needs_human" || result["attempts"] != float64(1) || result["rounds"] != float64(1) {
				t.Errorf("the result says outcome %v, %v attempts and %v rounds, want the question of the first round of the first attempt", result["outcome"], result["attempts"], result["rounds"])
			}
			n := 0
			for _, c := range e.issueNow().Comments {
				if strings.Contains(c.Body, "kind=question") {
					n++
				}
			}
			if n != 1 || !strings.Contains(strings.Join(e.issueNow().Labels, ","), "af:needs-human") {
				t.Errorf("issue 42 has %d question comments and the labels %v, want one and needs-human", n, e.issueNow().Labels)
			}
		})
	}
}

// TestAResumedTriageDiagnosisIsLandedAgainWithTheOriginalReportUntouchedAndNoLease_TS_14_37 verifies
// TS-14-37.
// Verifies: 14-REQ-5.8
func TestAResumedTriageDiagnosisIsLandedAgainWithTheOriginalReportUntouchedAndNoLease_TS_14_37(t *testing.T) {
	e := loopEnv(t, triageFiles, nil, nil, submitStep(probableDiagnosis))
	original, _ := e.hub.IssueOf(slug, 42)
	req := func(n int) jobs.Request {
		return e.request(jobs.KindTriage, issueInput("42"), func(r *jobs.Request) { r.Delivery = n })
	}
	e.runner.abortAfter = "submission_accepted"
	if _, err := e.runner.Run(t.Context(), req(1)); err == nil {
		t.Fatal("the first delivery ran to its end")
	}
	e.runner.abortAfter = ""
	leases, runs := len(e.hub.Leases()), e.brainRuns()

	res, err := e.run(req(2))

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if got := len(e.hub.Leases()) - leases; got != 0 {
		t.Errorf("%d leases were taken, want none", got)
	}
	if got := e.brainRuns() - runs; got != 0 {
		t.Errorf("the brain was started %d times, want never", got)
	}
	after, _ := e.hub.IssueOf(slug, 42)
	i := strings.Index(after.Body, "## Original report")
	if i < 0 || !strings.Contains(after.Body[i:], original.Body) {
		t.Errorf("the body is %q, want the ## Original report section to hold the original body %q byte for byte", after.Body, original.Body)
	}
	if !strings.Contains(after.Body, "Run calls Help before it checks its arguments") {
		t.Errorf("the body is %q, want the diagnosis in it", after.Body)
	}
}
