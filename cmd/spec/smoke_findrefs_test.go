package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/specgen"
)

// skipIfNoFindRefs skips the test when the replace target does not offer
// find_references.
func skipIfNoFindRefs(t *testing.T) {
	t.Helper()
	ws, err := tools.NewWorkspace(t.TempDir())
	if err != nil {
		t.Skipf("NewWorkspace: %v", err)
	}
	built, err := tools.All(tools.Options{Workspace: ws})
	if err != nil {
		t.Skipf("tools.All: %v", err)
	}
	for _, tl := range built {
		if tl.Name == "find_references" {
			return
		}
	}
	t.Skip("find_references not offered by the replace target")
}

// TS-17-51 (smoke): spec --preflight through the real shell on a workspace
// with no Go package reports go_typecheck as '0 packages checked, 0 errors'
// before code_search_index.
//
// Verifies: 17-PATH-1, 17-REQ-8.1
//
// Real components: cmd/spec App, toolio.App.Main, specgen.RunPreflight,
// agentrun.DetectSymbolBackend, agentrun.DetectGoTypecheck, tools.All,
// toolio.PreflightCheck
func TestTS17_51_SpecPreflightReportsGoTypecheck(t *testing.T) {
	skipIfNoFindRefs(t)

	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv(specgen.SpecDirEnv, "")
	dir := t.TempDir()
	// Only go.mod, no Go source files.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := newApp().Main(context.Background(),
		[]string{"--preflight", "--dir", dir, "a library that does a thing"},
		strings.NewReader(""), &stdout, &stderr)
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s\n%s", err, stdout.String(), stderr.String())
	}
	if code != toolio.ExitOK {
		t.Fatalf("exit %d: %v", code, env)
	}

	res, _ := env["result"].(map[string]any)
	if res["stage"] != "preflight" {
		t.Errorf("stage = %v", res["stage"])
	}

	list, _ := res["preflight"].([]any)
	if len(list) < 3 {
		t.Fatalf("preflight has %d entries, want at least 3: %v", len(list), res["preflight"])
	}

	// Last three: symbol_backend, go_typecheck, code_search_index.
	last3 := list[len(list)-3:]
	wantNames := []string{"symbol_backend", "go_typecheck", "code_search_index"}
	for i, want := range wantNames {
		entry, _ := last3[i].(map[string]any)
		if entry["check"] != want {
			t.Errorf("preflight[%d from end].check = %v, want %q", 3-i, entry["check"], want)
		}
		if entry["ok"] != true {
			t.Errorf("%s.ok = %v, want true", want, entry["ok"])
		}
	}

	// go_typecheck detail is exactly '0 packages checked, 0 errors'.
	gtc, _ := last3[1].(map[string]any)
	if gtc["detail"] != "0 packages checked, 0 errors" {
		t.Errorf("go_typecheck.detail = %v, want %q", gtc["detail"], "0 packages checked, 0 errors")
	}

	// No usage.
	if _, ok := env["usage"]; ok {
		t.Error("no phase ran, so the envelope must carry no usage")
	}

	// No .specs directory was created.
	if _, err := os.Stat(filepath.Join(dir, ".specs")); err == nil {
		t.Error("--preflight created a spec root")
	}
}
