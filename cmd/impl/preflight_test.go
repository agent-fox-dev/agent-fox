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

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/codeimpl"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// preflightSpecRepo makes a clean git repository on main holding the active
// v2 example package as .specs/09_agent_mode, with test commands that
// resolve to a Makefile. It returns the repository root and the package dir.
func preflightSpecRepo(t *testing.T) (string, string) {
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
		gitIn(t, dir, argv...)
	}
	put := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(filepath.Join(dir, "go.mod"), "module x\n")
	put(filepath.Join(dir, "Makefile"), "test:\n\t@exit 0\nlint:\n\t@exit 0\n")

	specDir := filepath.Join(dir, ".specs", "09_agent_mode")
	src := filepath.Join("..", "..", "testdata", "v2_example")
	for _, name := range []string{"prd.md", "requirements.json", "test_spec.json", "tasks.json"} {
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "tasks.json" {
			var doc map[string]any
			if err := json.Unmarshal(b, &doc); err != nil {
				t.Fatal(err)
			}
			doc["test_commands"] = map[string]any{"all_tests": "make test", "linter": "make lint"}
			if b, err = json.MarshalIndent(doc, "", "  "); err != nil {
				t.Fatal(err)
			}
		}
		put(filepath.Join(specDir, name), string(b))
	}
	spec, err := afspec.LoadSpec(specDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spec.Transition("active", specDir); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "chore: initial commit")
	return dir, specDir
}

// runImpl drives the real App, as main does, and returns the exit code and
// the decoded envelope.
func runImpl(t *testing.T, argv ...string) (int, map[string]any) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := newApp().Main(context.Background(), argv, strings.NewReader(""), &stdout, &stderr)
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	return code, env
}

func implEnv(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
}

// TS-11-9 (unit): impl's Exec and PreflightExec closures build identical
// codeimpl.Options from the same flags, because both call implOptions.
//
// Verifies: 11-REQ-2.4
func TestTS11_9_ImplOptionsIsOneFunctionForBothClosures(t *testing.T) {
	f := &implFlags{
		specsDir:      "specs",
		task:          2,
		branch:        "impl/custom",
		repo:          "acme/widgets",
		land:          "branch",
		verify:        "make check",
		verifyTimeout: 3 * time.Minute,
		pushAttempts:  7,
		allow:         "make, jq",
		draft:         true,
		pull:          true,
		noSurvey:      true,
		noTestFirst:   true,
		attempts:      3,
		repair:        true,
		repairTries:   5,
	}
	d := toolio.Deps{Common: &toolio.Common{DryRun: true, TotalBudgetUSD: 12}}

	optsA, optsB := f.implOptions(d, nil), f.implOptions(d, nil)
	if optsA.CheckRunner == nil {
		t.Error("CheckRunner is unset: the verification command would inherit the model credential")
	}
	// Func values are never DeepEqual; the runner is checked above.
	optsA.CheckRunner, optsB.CheckRunner = nil, nil
	if !reflect.DeepEqual(optsA, optsB) {
		t.Errorf("two builds differ:\n%+v\n%+v", optsA, optsB)
	}

	if got := optsA.Repo; got.Owner != "acme" || got.Name != "widgets" {
		t.Errorf("Repo = %+v", got)
	}
	if optsA.SpecsDir != "specs" || optsA.Task != 2 || optsA.Branch != "impl/custom" ||
		optsA.Land != codeimpl.LandBranch || optsA.VerifyCommand != "make check" || optsA.NoVerify ||
		optsA.VerifyTimeout != 3*time.Minute || optsA.PushAttempts != 7 || !optsA.Draft || !optsA.Pull ||
		!optsA.NoSurvey || !optsA.NoTestFirst || optsA.TaskAttempts != 3 || !optsA.Repair || optsA.RepairAttempts != 5 ||
		!optsA.DryRun || optsA.TotalBudgetUSD != 12 ||
		!reflect.DeepEqual(optsA.AllowPrograms, []string{"make", "jq"}) {
		t.Errorf("flags did not reach the options: %+v", optsA)
	}

	app := newApp()
	if app.Exec == nil || app.PreflightExec == nil {
		t.Errorf("Exec = %v, PreflightExec = %v: impl must register both", app.Exec != nil, app.PreflightExec != nil)
	}
}

// TS-11-12 (unit): --repair-model is resolved by one function, called the
// same way from Exec and PreflightExec.
//
// Verifies: 11-REQ-2.5
func TestTS11_12_ResolveRepairRunnerIsShared(t *testing.T) {
	implEnv(t)
	dir, _ := preflightSpecRepo(t)

	app := newApp()
	called := false
	app.PreflightExec = func(_ context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
		called = true
		rA, mA, errA := resolveRepairRunner(d, "ADVANCED", "")
		rB, mB, errB := resolveRepairRunner(d, "ADVANCED", "")
		if errA != nil || errB != nil {
			t.Errorf("errors: %v, %v", errA, errB)
			return toolio.ExitOK, &codeimpl.Result{}, nil
		}
		if rA == nil || rB == nil || mA == nil || mB == nil {
			t.Fatalf("runner/choice missing: %v %v %v %v", rA, rB, mA, mB)
		}
		if mA.Model.ID != mB.Model.ID || mA.Thinking != mB.Thinking {
			t.Errorf("resolutions differ: %v/%v vs %v/%v", mA.Model.ID, mA.Thinking, mB.Model.ID, mB.Thinking)
		}
		if rA.Model().ID != mA.Model.ID {
			t.Errorf("runner model %q, choice %q", rA.Model().ID, mA.Model.ID)
		}
		// No --repair-model: nothing to resolve.
		if r, m, err := resolveRepairRunner(d, "", ""); r != nil || m != nil || err != nil {
			t.Errorf("empty spec resolved to %v %v %v", r, m, err)
		}
		return toolio.ExitOK, &codeimpl.Result{}, nil
	}
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--preflight", "--dir", dir, "--land", "none", "09"},
		strings.NewReader(""), &stdout, &stderr)
	if code != toolio.ExitOK || !called {
		t.Fatalf("code = %d, called = %v\nstdout:\n%s\nstderr:\n%s", code, called, stdout.String(), stderr.String())
	}
}

// TS-11-21 (unit): an ordinary failing run's envelope carries no preflight or
// estimate key.
//
// Verifies: 11-REQ-3.5
func TestTS11_21_OrdinaryFailingRunCarriesNoPreflightOrEstimate(t *testing.T) {
	implEnv(t)
	dir, _ := preflightSpecRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, env := runImpl(t, "--dir", dir, "--land", "none", "09")
	if code != toolio.ExitUsage {
		t.Fatalf("code = %d, want %d: %v", code, toolio.ExitUsage, env)
	}
	if res, ok := env["result"].(map[string]any); ok {
		for _, k := range []string{"preflight", "estimate"} {
			if _, has := res[k]; has {
				t.Errorf("an ordinary run's result carries %q: %v", k, res)
			}
		}
	}
}

// TS-11-23 (integration): impl --preflight on a dirty working tree fails with
// the exact same error object as impl without --preflight.
//
// Verifies: 11-REQ-4.2
func TestTS11_23_PreflightOnADirtyTreeFailsLikeTheOrdinaryRun(t *testing.T) {
	implEnv(t)
	dir, _ := preflightSpecRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"--dir", dir, "--land", "none"}

	codeA, envA := runImpl(t, append(append([]string{}, args...), "09")...)
	codeB, envB := runImpl(t, append(append([]string{"--preflight"}, args...), "09")...)
	if codeA != toolio.ExitUsage || codeB != toolio.ExitUsage {
		t.Fatalf("exit codes = %d, %d; want %d", codeA, codeB, toolio.ExitUsage)
	}
	a, _ := json.Marshal(envA["error"])
	b, _ := json.Marshal(envB["error"])
	if len(a) == 0 || string(a) == "null" {
		t.Fatalf("no error object: %v", envA)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("error objects differ:\n%s\n%s", a, b)
	}
	if envA["status"] != envB["status"] {
		t.Errorf("status = %v vs %v", envA["status"], envB["status"])
	}
}

// TS-11-24 (integration): a --preflight run that refuses partway through its
// checks reports no partial checklist of the checks that passed before it.
//
// Verifies: 11-REQ-4.3
func TestTS11_24_RefusalPartwayReportsNoPartialChecklist(t *testing.T) {
	implEnv(t)
	dir, specDir := preflightSpecRepo(t)
	raw, err := os.ReadFile(filepath.Join(specDir, "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	// The clean-tree check passes; the shape check of test_commands refuses.
	bad := strings.Replace(string(raw), `"make test"`, `"make test && make lint"`, 1)
	if bad == string(raw) {
		t.Fatal("the fixture's test command was not replaced")
	}
	if err := os.WriteFile(filepath.Join(specDir, "tasks.json"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "chore: compound test command")

	code, env := runImpl(t, "--preflight", "--dir", dir, "--land", "none", "09")
	if code != toolio.ExitUsage {
		t.Fatalf("code = %d, want %d: %v", code, toolio.ExitUsage, env)
	}
	if res, ok := env["result"].(map[string]any); ok {
		if pf, has := res["preflight"]; has {
			if list, _ := pf.([]any); len(list) != 0 {
				t.Errorf("a refused run carries a partial checklist: %v", pf)
			}
		}
		if _, has := res["estimate"]; has {
			t.Errorf("a refused run carries an estimate: %v", res)
		}
	}
	// And the same refusal as the ordinary run.
	_, ord := runImpl(t, "--dir", dir, "--land", "none", "09")
	a, _ := json.Marshal(ord["error"])
	b, _ := json.Marshal(env["error"])
	if !bytes.Equal(a, b) {
		t.Errorf("error objects differ:\n%s\n%s", a, b)
	}
}

// A --preflight run that passes reports its checklist and estimate in the
// default (summary) view, exits 0, carries no usage, and creates no branch.
func TestImplPreflightSuccessThroughTheShell(t *testing.T) {
	implEnv(t)
	dir, _ := preflightSpecRepo(t)

	code, env := runImpl(t, "--preflight", "--dir", dir, "--land", "none", "09")
	if code != toolio.ExitOK {
		t.Fatalf("code = %d: %v", code, env)
	}
	res, _ := env["result"].(map[string]any)
	if res["stage"] != "preflight" {
		t.Errorf("stage = %v", res["stage"])
	}
	if list, ok := res["preflight"].([]any); !ok || len(list) == 0 {
		t.Errorf("the default view dropped result.preflight: %v", res)
	}
	if _, ok := res["estimate"].(map[string]any); !ok {
		t.Errorf("the default view dropped result.estimate: %v", res)
	}
	if _, ok := env["usage"]; ok {
		t.Error("no phase ran, so the envelope must carry no usage")
	}
	if got := gitIn(t, dir, "branch", "--format=%(refname:short)"); got != "main" {
		t.Errorf("branches after --preflight = %q", got)
	}
}
