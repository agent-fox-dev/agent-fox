package main

import (
	"regexp"
	"testing"

	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
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

// TS-17-50 (smoke): impl --preflight through the real shell ends its
// checklist with symbol_backend, go_typecheck and code_search_index, ok true,
// exit 0, with no model call and no new branch.
//
// Verifies: 17-PATH-1, 17-REQ-8.1
//
// Real components: cmd/impl App, toolio.App.Main, codeimpl.RunPreflight,
// agentrun.DetectSymbolBackend, agentrun.DetectGoTypecheck, tools.All,
// toolio.PreflightCheck
func TestTS17_50_ImplPreflightReportsGoTypecheck(t *testing.T) {
	skipIfNoFindRefs(t)
	implEnv(t)
	dir, _ := preflightSpecRepo(t)

	code, env := runImpl(t, "--preflight", "--dir", dir, "--land", "none", "09")
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

	// go_typecheck detail matches the expected pattern.
	gtc, _ := last3[1].(map[string]any)
	detail, _ := gtc["detail"].(string)
	re := regexp.MustCompile(`^[0-9]+ packages checked, [0-9]+ errors(, partial)?$`)
	if !re.MatchString(detail) {
		t.Errorf("go_typecheck.detail = %q, does not match pattern", detail)
	}

	// No usage.
	if _, ok := env["usage"]; ok {
		t.Error("no phase ran, so the envelope must carry no usage")
	}

	// Branch list is still just main.
	if got := gitIn(t, dir, "branch", "--format=%(refname:short)"); got != "main" {
		t.Errorf("branches after --preflight = %q", got)
	}
}
