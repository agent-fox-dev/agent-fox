package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/codefix"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// preflightRepo makes a clean git repository with one commit on main.
func preflightRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	for _, argv := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, argv...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", argv, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "chore: initial commit"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, argv...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", argv, err, out)
		}
	}
	return dir
}

// runFix drives the real App, as main does, and returns its decoded envelope.
func runFix(t *testing.T, argv ...string) (int, map[string]any) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := newApp().Main(context.Background(), normalizeArgs(argv), strings.NewReader(""), &stdout, &stderr)
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	return code, env
}

// stable drops the fields that differ between any two runs: wall-clock time
// and the per-run report file.
func stable(env map[string]any) []byte {
	for _, k := range []string{"duration_ms", "started_at", "report_file"} {
		delete(env, k)
	}
	if res, ok := env["result"].(map[string]any); ok {
		for _, key := range []string{"baseline", "verification"} {
			if b, ok := res[key].(map[string]any); ok {
				delete(b, "duration_ms")
			}
		}
	}
	b, _ := json.Marshal(env)
	return b
}

// TS-11-4 (integration): fix --preflight --dry-run produces the same
// envelope as fix --preflight alone.
//
// Verifies: 11-REQ-1.4
func TestTS11_4_PreflightDryRunEnvelopeEqualsPreflight(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	args := []string{"--land", "none", "--verify", "true"}
	dirA, dirB := preflightRepo(t), preflightRepo(t)

	codeA, envA := runFix(t, append([]string{"--preflight", "--dry-run", "--dir", dirA}, append(args, "the counter double-counts")...)...)
	codeB, envB := runFix(t, append([]string{"--preflight", "--dir", dirB}, append(args, "the counter double-counts")...)...)
	if codeA != toolio.ExitOK || codeB != toolio.ExitOK {
		t.Fatalf("exit codes = %d, %d; envelopes:\n%s\n%s", codeA, codeB, stable(envA), stable(envB))
	}
	a, b := stable(envA), stable(envB)
	if !bytes.Equal(a, b) {
		t.Errorf("envelopes differ:\n%s\n%s", a, b)
	}

	res, _ := envB["result"].(map[string]any)
	if res["stage"] != "preflight" {
		t.Errorf("result.stage = %v", res["stage"])
	}
	if _, ok := res["preflight"].([]any); !ok {
		t.Errorf("the default --detail summary dropped result.preflight: %v", res)
	}
	if _, ok := res["estimate"].(map[string]any); !ok {
		t.Errorf("the default --detail summary dropped result.estimate: %v", res)
	}
	if _, ok := envB["usage"]; ok {
		t.Error("no phase ran, so the envelope must carry no usage")
	}
	// A preflight run makes no branch.
	out, err := exec.Command("git", "-C", dirB, "branch", "--format=%(refname:short)").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "main" {
		t.Errorf("branches after --preflight = %q", out)
	}
}

// TS-11-8 (unit): fix's Exec and PreflightExec closures build identical
// codefix.Options from the same flags, because both call fixOptions.
//
// Verifies: 11-REQ-2.4
func TestTS11_8_FixOptionsIsOneFunctionForBothClosures(t *testing.T) {
	f := &fixFlags{
		repo:          "acme/widgets",
		land:          "none",
		verify:        "make check",
		verifyTimeout: 3 * time.Minute,
		pushAttempts:  7,
		allow:         "make, jq",
		draft:         true,
	}
	_ = f.pull.Set("release")
	d := toolio.Deps{Common: &toolio.Common{DryRun: true, TotalBudgetUSD: 12}}

	optsA, optsB := f.fixOptions(d), f.fixOptions(d)
	if optsA.CheckRunner == nil {
		t.Error("CheckRunner is unset: the verification command would inherit the model credential")
	}
	// Func values are never DeepEqual; the runner is checked above.
	optsA.CheckRunner, optsB.CheckRunner = nil, nil
	if !reflect.DeepEqual(optsA, optsB) {
		t.Errorf("two builds differ:\n%+v\n%+v", optsA, optsB)
	}

	want := issuex.Repo{Owner: "acme", Name: "widgets"}
	if got := optsA.Repo; got.Owner != want.Owner || got.Name != want.Name {
		t.Errorf("Repo = %+v", got)
	}
	if optsA.Land != codefix.LandNone || optsA.VerifyCommand != "make check" || optsA.PushAttempts != 7 ||
		optsA.VerifyTimeout != 3*time.Minute || !optsA.Draft || !optsA.DryRun || optsA.TotalBudgetUSD != 12 ||
		!optsA.Pull || optsA.PullBranch != "release" || !reflect.DeepEqual(optsA.AllowPrograms, []string{"make", "jq"}) {
		t.Errorf("flags did not reach the options: %+v", optsA)
	}

	app := newApp()
	if app.Exec == nil || app.PreflightExec == nil {
		t.Errorf("Exec = %v, PreflightExec = %v: fix must register both", app.Exec != nil, app.PreflightExec != nil)
	}
}
