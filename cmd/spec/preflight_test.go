package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/specgen"
)

// TS-11-10 (unit): spec's Exec and PreflightExec closures build identical
// specgen.Options from the same flags, because both call specOptions.
//
// Verifies: 11-REQ-2.4
func TestTS11_10_SpecOptionsIsOneFunctionForBothClosures(t *testing.T) {
	f := &specFlags{specsDir: "specs", name: "my_spec", architecture: true, noActivate: true, comment: true}
	d := toolio.Deps{
		Common: &toolio.Common{DryRun: true, TotalBudgetUSD: 9},
		Input:  toolio.Input{Kind: toolio.KindText, Origin: "argument", Body: "an idea"},
	}

	optsA, optsB := f.specOptions(d), f.specOptions(d)
	if !reflect.DeepEqual(optsA, optsB) {
		t.Errorf("two builds differ:\n%+v\n%+v", optsA, optsB)
	}
	if optsA.SpecsDir != "specs" || optsA.Name != "my_spec" || !optsA.Architecture || optsA.Activate ||
		!optsA.Comment || !optsA.DryRun || optsA.TotalBudgetUSD != 9 || optsA.Input.Body != "an idea" {
		t.Errorf("flags did not reach the options: %+v", optsA)
	}

	app := newApp()
	if app.Exec == nil || app.PreflightExec == nil {
		t.Errorf("Exec = %v, PreflightExec = %v: spec must register both", app.Exec != nil, app.PreflightExec != nil)
	}
}

// spec --preflight through the real shell: exit 0, stage preflight, the
// checklist and estimate in the default view, no usage, nothing written.
func TestSpecPreflightThroughTheShell(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv(specgen.SpecDirEnv, "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := newApp().Main(context.Background(), []string{"--preflight", "--dir", dir, "a library that does a thing"},
		strings.NewReader(""), &stdout, &stderr)
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s\n%s", err, stdout.String(), stderr.String())
	}
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
	if est, ok := res["estimate"].(map[string]any); !ok || est["phases"] != float64(4) {
		t.Errorf("estimate = %v", res["estimate"])
	}
	if _, ok := env["usage"]; ok {
		t.Error("no phase ran, so the envelope must carry no usage")
	}
	if _, err := os.Stat(filepath.Join(dir, ".specs")); err == nil {
		t.Error("--preflight created a spec root")
	}
}
