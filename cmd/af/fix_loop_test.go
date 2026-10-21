package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// TestAQuestionEndsTheRunWithNeedsHumanAndTouchesNothing_TS_09_30 verifies TS-09-30: a
// scripted brain that edits the tree in its box and asks a question ends the run at exit 3
// with the question, its options labelled A, B, C and the way to answer, and the checkout
// has no branch, no commit and no change.
// Verifies: 09-REQ-4.5
func TestAQuestionEndsTheRunWithNeedsHumanAndTouchesNothing_TS_09_30(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	before, branches, head := snapshot(t, e.dir), e.branches(), gitDo(t, e.dir, "rev-parse", "HEAD")
	question := `{"question":{"text":"Which greeting?","options":["x","y","z"]}}`

	code, env := e.fix(e.forge.IssueURL(7), "--brain", "scripted", "--sandbox", "local", "--script", script(t, writeStep("src/greet.sh", "echo half\n"), submitStep(question, "accepted")))

	if code != 3 || env["status"] != "needs_human" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	nh, _ := env["needs_human"].(map[string]any)
	var ids, labels []string
	for _, o := range nh["options"].([]any) {
		ids, labels = append(ids, o.(map[string]any)["id"].(string)), append(labels, o.(map[string]any)["label"].(string))
	}
	if nh["question"] != "Which greeting?" || !slices.Equal(ids, []string{"A", "B", "C"}) || !slices.Equal(labels, []string{"x", "y", "z"}) {
		t.Errorf("needs_human = %v", nh)
	}
	resume, _ := nh["resume"].(map[string]any)
	if argv, _ := resume["argv"].([]any); len(argv) == 0 || resume["flag"] != "--context" {
		t.Errorf("resume = %v", resume)
	}
	if !equalMaps(before, snapshot(t, e.dir)) || !slices.Equal(e.branches(), branches) || gitDo(t, e.dir, "rev-parse", "HEAD") != head {
		t.Error("the checkout changed")
	}
	if status := gitDo(t, e.dir, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf("git status = %q, want a clean tree", status)
	}
	res := resultOf(t, env)
	if bx, _ := res["box"].(map[string]any); res["outcome"] != "needs_human" || res["rounds"] != float64(1) || res["attempts"] != float64(1) || res["attempts_max"] != float64(2) || bx["provider"] != "local" || bx["network"] != "host" {
		t.Errorf("result = %v", res)
	}
	if res["land"] != "pr" || res["verdict"] != nil || res["branch"] != nil {
		t.Errorf("result = %v: a run that asked measured nothing and made no branch", res)
	}
	if u, _ := env["usage"].(map[string]any); u["brain"] != "scripted" {
		t.Errorf("usage = %v, want the brain of the run", env["usage"])
	}
	if kinds := ledgerKinds(t, runDirOf(e, env)); !slices.Contains(kinds, "fix.question") || slices.Contains(kinds, "apply") {
		t.Errorf("ledger kinds = %v", kinds)
	}
	if n := len(e.forge.Writes()); n != 0 {
		t.Errorf("%d writes to the forge", n)
	}
}

// TestAnswerResumesTheRunWithTheSamePrefixAndTheQuestion guards 09-REQ-3.4 through a real
// needs_human: the invocation the envelope offers, with the answer, renders a brief with
// the prefix of the run it resumes, whose task block holds the question and the answer.
func TestAnswerResumesTheRunWithTheSamePrefixAndTheQuestion(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	question := `{"question":{"text":"Which greeting?","options":["x","y","z"]}}`
	_, first := e.fix(e.forge.IssueURL(7), "--brain", "scripted", "--sandbox", "local", "--script", script(t, submitStep(question, "accepted")))
	resume := first["needs_human"].(map[string]any)["resume"].(map[string]any)
	var argv []string
	for _, a := range resume["argv"].([]any) {
		argv = append(argv, a.(string))
	}
	if argv[0] == "af" {
		argv = argv[1:]
	}

	_, second := e.run("", append(argv, resume["flag"].(string), "B")...)

	secondDir := runDirOf(e, second)
	if readFixJSON(t, secondDir)["prefix_sha256"] != readFixJSON(t, runDirOf(e, first))["prefix_sha256"] {
		t.Error("the prefix of the run that resumes is not the prefix of the run it resumes")
	}
	task := fixTaskBlockOf(t, secondDir)
	for _, want := range []string{"Which greeting?", "A. x", "C. z", "\n```\nB\n```\n"} {
		if !strings.Contains(task, want) {
			t.Errorf("the task block lacks %q:\n%s", want, task)
		}
	}
	if fj := readFixJSON(t, secondDir); len(fj["context"].([]any)) != 1 {
		t.Errorf("context = %v", fj["context"])
	}
	if _, err := os.Stat(filepath.Join(secondDir, "brief.2.md")); err == nil {
		t.Error("the resumed run has a second round already")
	}
}

// TestTheBoundsOfTheCommandLineAreTheBoundsOfTheRound guards that --budget, --max-turns and
// --attempts reach the rounds: the brain gets the budget and the turns of an attempt, and the
// loop makes as many attempts as it is told to.
func TestTheBoundsOfTheCommandLineAreTheBoundsOfTheRound(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	spy := &spyBrain{}
	useFixSpy(t, spy)

	_, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", "local", "--budget", "3.5", "--max-turns", "17", "--attempts", "3")

	if spy.runs != 3 {
		t.Errorf("the brain ran %d times, want one round for each of the 3 attempts: it ends with no report each time", spy.runs)
	}
	if b := spy.brief; b == nil || b.MaxCostUSD != 3.5 || b.MaxTurns != 17 || b.Attempt != 3 || b.Kind.Name != "fix_report" {
		t.Errorf("brief = %+v", b)
	}
	if env["run_id"] == nil {
		t.Errorf("envelope = %v", env)
	}
	briefMD, err := os.ReadFile(filepath.Join(runDirOf(e, env), "brief.md"))
	if err != nil || spy.brief.Prefix == "" || !strings.HasPrefix(string(briefMD), spy.brief.Prefix) || !strings.HasPrefix(spy.brief.Prefix, "# Fix mandate") {
		t.Errorf("the brain was given a prefix that is not the one of brief.md: %q (%v)", spy.brief.Prefix, err)
	}
}
