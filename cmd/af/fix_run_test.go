package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// writeStep is a step of a script that writes a file in the box.
func writeStep(path, content string) map[string]any {
	return map[string]any{"tool": "write_file", "args": map[string]any{"path": path, "content": content}, "expect": "ok"}
}

// helloReport is the report of the change that says hello.
const helloReport = `{"subject":"say hello","root_cause":"the script says helo","change":"say hello","files":[{"path":"src/greet.sh","why":"the word"}]}`

// fixRun runs af fix run on the run of an envelope.
func (e *fixEnv) fixRun(brief map[string]any, args ...string) (int, map[string]any) {
	e.t.Helper()
	return e.run("", append([]string{"fix", "run", "--run-id", brief["run_id"].(string), "--dir", e.dir, "--sandbox", "local"}, args...)...)
}

// TestFixRunAppliesOneRoundWithoutMeasuringBranchingOrCommitting_TS_09_8 verifies
// TS-09-8: a scripted brain edits a file and submits a report, and the edit is in the
// working tree, HEAD is where it was, there is no new branch and no gate run was
// recorded.
// Verifies: 09-REQ-1.8
func TestFixRunAppliesOneRoundWithoutMeasuringBranchingOrCommitting_TS_09_8(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	_, brief := e.brief(e.forge.IssueURL(7))
	baseHead := resultOf(t, brief)["base_head"]
	scriptPath := script(t, writeStep("src/greet.sh", "echo hello\n"), submitStep(helloReport, "accepted"))

	code, env := e.fixRun(brief, "--brain", "scripted", "--script", scriptPath, "--sandbox", "local")

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	res := resultOf(t, env)
	apply, _ := res["apply"].(map[string]any)
	if res["run_id"] != brief["run_id"] || res["round"] != float64(1) || res["submission"] != "report" || apply["written"] != float64(1) || apply["deleted"] != float64(0) || apply["ignored"] != float64(0) {
		t.Errorf("result = %v, want round 1, a report, and apply {1, 0, 0}", res)
	}
	if data, _ := os.ReadFile(filepath.Join(e.dir, "src", "greet.sh")); string(data) != "echo hello\n" {
		t.Errorf("src/greet.sh = %q, want the edit in the working tree", data)
	}
	if strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "HEAD")) != baseHead || !slices.Equal(e.branches(), []string{"main"}) {
		t.Errorf("HEAD or the branches moved: %v", e.branches())
	}
	kinds := ledgerKinds(t, runDirOf(e, brief))
	if !slices.Contains(kinds, "apply") || !slices.Contains(kinds, "fix.run") || slices.Contains(kinds, "measure") {
		t.Errorf("ledger kinds = %v, want apply and fix.run and no measure", kinds)
	}
	if next, _ := env["next"].([]any); len(next) != 1 || next[0].(map[string]any)["argv"].([]any)[2] != "land" {
		t.Errorf("next = %v, want fix land", env["next"])
	}
	if fj := readFixJSON(t, runDirOf(e, brief)); fj["rounds"] != float64(1) {
		t.Errorf("rounds = %v in fix.json, want 1: the round is counted", fj["rounds"])
	}
	if status := gitDo(t, e.dir, "status", "--porcelain"); !strings.Contains(status, "M src/greet.sh") {
		t.Errorf("git status = %q, want the edit uncommitted", status)
	}

	// A second round is the second: its brief is kept beside the first.
	code, env = e.fixRun(brief, "--brain", "scripted", "--script", script(t, submitStep(helloReport, "accepted")), "--sandbox", "local")
	if code != 0 || resultOf(t, env)["round"] != float64(2) {
		t.Errorf("second run: exit code %d, result %v, want round 2", code, env["result"])
	}
	for _, name := range []string{"brief.1.md", "brief.2.md"} {
		if _, err := os.Stat(filepath.Join(runDirOf(e, brief), name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestFixRunWithAQuestionOrNoSubmissionAppliesNothing guards 09-REQ-5.7 and 09-REQ-4 at
// the command: a question is reported and the tree is left, and a brain that never
// submits fails the command with the tree as it was, whatever it wrote in the box.
func TestFixRunWithAQuestionOrNoSubmissionAppliesNothing(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	_, brief := e.brief(e.forge.IssueURL(7))
	before := snapshot(t, e.dir)

	question := `{"question":{"text":"Which greeting?","options":["hello","hi"]}}`
	code, env := e.fixRun(brief, "--brain", "scripted", "--sandbox", "local", "--script", script(t, writeStep("src/greet.sh", "echo half\n"), submitStep(question, "accepted")))

	if code != 0 {
		t.Fatalf("a question: exit code %d, error %v", code, env["error"])
	}
	res := resultOf(t, env)
	q, _ := res["question"].(map[string]any)
	if res["submission"] != "question" || res["apply"] != nil || q["text"] != "Which greeting?" {
		t.Errorf("result = %v, want the question and no apply", res)
	}
	if next, _ := env["next"].([]any); len(next) != 0 {
		t.Errorf("next = %v, want nothing to land after a question", next)
	}
	if !equalMaps(before, snapshot(t, e.dir)) {
		t.Error("a question changed the checkout")
	}

	code, env = e.fixRun(brief, "--brain", "scripted", "--sandbox", "local", "--script", script(t, writeStep("src/greet.sh", "echo lost\n")))
	if code == 0 || env["status"] != "failed" {
		t.Errorf("no submission: exit code %d, status %v, want a failure", code, env["status"])
	}
	if !equalMaps(before, snapshot(t, e.dir)) {
		t.Error("a round without a submission changed the checkout")
	}
}

// TestFixRunRefusesWhatItCannotRun guards 09-REQ-1.3 and the refusals of a run that is
// not one: the flags it takes, no run id, an id of no run, and a brief that is not the
// one the run was made with.
func TestFixRunRefusesWhatItCannotRun(t *testing.T) {
	requireMake(t)
	e := newFixEnv(t, fakeforge.GitHub)
	_, brief := e.brief(e.forge.IssueURL(7))
	scripted := []string{"--brain", "scripted", "--sandbox", "local", "--script", script(t, submitStep(helloReport, "accepted"))}

	code, env := e.run("", append([]string{"fix", "run", "--dir", e.dir}, scripted...)...)
	if msg := refusedBy(t, code, env, 2, "usage")["message"].(string); !strings.Contains(msg, "--run-id") {
		t.Errorf("no run id: %q", msg)
	}
	code, env = e.run("", append([]string{"fix", "run", "--run-id", "20260101_000000_abcdef", "--dir", e.dir}, scripted...)...)
	refusedBy(t, code, env, 2, "usage")
	code, env = e.run("", append([]string{"fix", "run", "--run-id", "../x", "--dir", e.dir}, scripted...)...)
	refusedBy(t, code, env, 2, "usage")
	bare := "20260101_000000_abcdef"
	if err := os.MkdirAll(filepath.Join(e.dir, ".agent-fox", "run", bare), 0o755); err != nil {
		t.Fatal(err)
	}
	code, env = e.run("", append([]string{"fix", "run", "--run-id", bare, "--dir", e.dir}, scripted...)...)
	if msg := refusedBy(t, code, env, 2, "usage")["message"].(string); !strings.Contains(msg, "fix.json") {
		t.Errorf("a run directory without fix.json: %q, want it to say so", msg)
	}

	briefPath := filepath.Join(runDirOf(e, brief), "brief.md")
	original, _ := os.ReadFile(briefPath)
	if err := os.WriteFile(briefPath, []byte(strings.Replace(string(original), "# Fix mandate", "# Fix mandate (edited)", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	code, env = e.fixRun(brief, scripted...)
	if msg := refusedBy(t, code, env, 2, "usage")["message"].(string); !strings.Contains(msg, "brief") {
		t.Errorf("an edited brief: %q, want it to say so", msg)
	}

	var out strings.Builder
	e.app.Main(t.Context(), []string{"af", "help", "fix", "run"}, strings.NewReader(""), &out, &out)
	for _, want := range []string{"--run-id", "--brain", "--sandbox", "--budget", "--max-turns", "--script", "--dir", "--timeout", "(default 10)", "(default 150)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("af help fix run does not show %q:\n%s", want, out.String())
		}
	}
	for _, not := range []string{"--land", "--attempts", "--base", "--no-verify", "--dry-run", "--repo", "--context"} {
		if strings.Contains(out.String(), not) {
			t.Errorf("af help fix run shows %q, which it does not take", not)
		}
	}
}
