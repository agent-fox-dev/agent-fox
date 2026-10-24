package worker

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/afcmd"
	"github.com/agent-fox-dev/agentfox/internal/fakeaf"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

// The fixtures of the tests that run the loop: a repository with a gate that fails its test, a
// scripted brain that fixes it, and a repository to triage. They are the ones of the tests of
// internal/afcmd, which the loops are run with here, in a gateway box, through the fake hub.

// fixFiles is a repository whose gate fails: the script says helo. The target box-commit is what
// the brain runs to commit in its box, which its own tools do not let it do with git.
var fixFiles = map[string]string{
	"Makefile":            "check:\n\tsh tests/run.sh\nbox-commit:\n\tgit add src tests\n\tgit -c user.name=box -c user.email=box@example.com commit -q -m 'a commit made in the box'\n",
	"tests/run.sh":        "set -e\nfor t in tests/*_test.sh; do sh \"$t\"; done\n",
	"tests/greet_test.sh": "[ \"$(sh src/greet.sh)\" = hello ] || { echo \"FAIL: greet printed $(sh src/greet.sh)\"; exit 1; }\n",
	"src/greet.sh":        "echo helo\n",
}

// triageFiles is a repository to triage.
var triageFiles = map[string]string{
	"go.mod":       "module example.com/x\n",
	"main.go":      "package main\n\nimport \"example.com/x/util\"\n\n// Run starts the program.\nfunc Run() {\n\tutil.Help()\n}\n\nfunc main() { Run() }\n",
	"util/util.go": "package util\n\n// Help helps.\nfunc Help() {}\n",
}

const (
	// helloTest is the test the brain adds to prove its change.
	helloTest = "[ \"$(sh src/greet.sh)\" = hello ] || { echo \"FAIL: not hello\"; exit 1; }\n"
	// fixReport is the report of the change that says hello.
	fixReport = `{"subject":"say hello","root_cause":"the script says helo","change":"say hello","files":[{"path":"src/greet.sh","why":"the word"},{"path":"tests/hello_test.sh","why":"the proof"}]}`
	// probableDiagnosis is a diagnosis of a triage that is probable, so that the issue is ready.
	probableDiagnosis = `{"title":"Run starts the wrong thing","severity":"high","confidence":"probable","root_cause":"Run calls Help before it checks its arguments.","affected_files":[{"path":"main.go","line":6,"note":"Run"},{"path":"util/util.go","note":"Help"}],"suggested_fix":"Check the arguments first.","acceptance_criteria":["Run with no arguments prints the usage"]}`
)

func writeStep(path, content string) map[string]any {
	return map[string]any{"tool": "write_file", "args": map[string]any{"path": path, "content": content}, "expect": "ok"}
}

func submitStep(payload string) map[string]any {
	var p any
	_ = json.Unmarshal([]byte(payload), &p)
	return map[string]any{"submit": p, "expect": "accepted"}
}

// fixSteps is the round of a brain that fixes the greeting, proves it with a test of its own,
// commits in its box and hands over its report.
func fixSteps() []map[string]any {
	return []map[string]any{
		writeStep("src/greet.sh", "echo hello\n"),
		writeStep("tests/hello_test.sh", helloTest),
		{"tool": "execute", "args": map[string]any{"command": "make box-commit"}, "expect": "ok"},
		submitStep(fixReport),
	}
}

// scriptFile writes a script of the scripted brain, one round of steps.
func scriptFile(t *testing.T, steps ...map[string]any) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"steps": steps})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// requireMake skips the test where the fixture gate cannot run.
func requireMake(t *testing.T) {
	t.Helper()
	for _, name := range []string{"sh", "make", "git"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s is not installed", name)
		}
	}
}

// loopEnv is an environment for a test that runs the loop: a workspace on a repository of the
// files with the variables, an issue 42, a lease that outlives the timeouts, and the test binary
// as af first on the PATH that the boxes of the gateway are given. steps is what the scripted
// brain does; it replaces the agentkit brain of the worker.
func loopEnv(t *testing.T, files map[string]string, vars map[string]string, mutate func(*fakehub.Workspace), steps ...map[string]any) *env {
	t.Helper()
	requireMake(t)
	bin, _ := fakeaf.Install(t)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	w := fakehub.Workspace{Branch: "main", Files: files, Vars: vars}
	if mutate != nil {
		mutate(&w)
	}
	e := newEnv(t, w)
	e.hub.SetLeaseTTL(3 * time.Hour)
	e.hub.AddIssue(slug, fakehub.Issue{Number: 42, Title: "Greeting is wrong", Body: "The greeting should say hello."})
	script := scriptFile(t, steps...)
	e.runner.loopOptions = func(_ *attempt, o *afcmd.LoopOptions) { o.Brain, o.Script = "scripted", script }
	return e
}
