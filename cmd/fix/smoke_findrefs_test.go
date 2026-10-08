package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/agentrun/indextest"
	"github.com/agent-fox-dev/agentfox/internal/envtest"
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

// TS-17-49 (smoke): fix --preflight through the real shell ends its checklist
// with symbol_backend, go_typecheck and code_search_index, ok true, exit 0,
// with no model call and nothing written to the tree.
//
// Verifies: 17-PATH-1, 17-REQ-8.1
//
// Real components: cmd/fix App, toolio.App.Main, codefix.RunPreflight,
// agentrun.DetectSymbolBackend, agentrun.DetectGoTypecheck, tools.All,
// toolio.PreflightCheck
func TestTS17_49_FixPreflightReportsGoTypecheck(t *testing.T) {
	skipIfNoFindRefs(t)

	// Install a faux provider so any model request is observable.
	p := faux.New()
	t.Cleanup(toolio.SetRunnerConfigHook(func(cfg *agentrun.Config) {
		cfg.Model = faux.Model()
		cfg.Thinking = core.ThinkingUnset
		cfg.Providers = core.ProviderRegistry{faux.API: p.APIProvider()}
	}))

	r, dir := runSmoke(t, "--preflight")
	if r.code != toolio.ExitOK {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.out, r.err)
	}
	if r.stage() != "preflight" {
		t.Errorf("stage = %q, want preflight", r.stage())
	}

	// Extract the preflight list.
	res, _ := r.env["result"].(map[string]any)
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

	// The faux provider received no request.
	if n := p.Calls(); n != 0 {
		t.Errorf("the faux provider received %d requests, want 0", n)
	}

	// No usage in the envelope.
	if _, ok := r.env["usage"]; ok {
		t.Error("no phase ran, so the envelope must carry no usage")
	}

	// git status --porcelain is empty.
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("git status is not clean: %s", out)
	}

	// Branch list is unchanged (just main).
	branches, err := exec.Command("git", "-C", dir, "branch", "--format=%(refname:short)").Output()
	if err != nil {
		t.Fatalf("git branch: %v", err)
	}
	if strings.TrimSpace(string(branches)) != "main" {
		t.Errorf("branches = %q, want just main", branches)
	}
}

// TS-17-54 (smoke): fix --preflight with a failing go_typecheck detection
// omits only that entry, keeps the other checks, the stage and the estimate
// as in a successful run, and exits 0.
//
// Verifies: 17-PATH-2, 17-REQ-4.5
//
// Real components: cmd/fix App, toolio.App.Main, codefix.RunPreflight,
// agentrun.DetectSymbolBackend, toolio.PreflightCheck
func TestTS17_54_FixPreflightOmitsGoTypecheckOnFailure(t *testing.T) {
	// Run 1: real detection.
	r1, _ := runSmoke(t, "--preflight")
	if r1.code != toolio.ExitOK {
		t.Fatalf("real detection: exit %d\nstdout:\n%s\nstderr:\n%s", r1.code, r1.out, r1.err)
	}

	// Run 2: failing detection.
	old := agentrun.DetectGoTypecheck
	agentrun.DetectGoTypecheck = func(ws *tools.Workspace) (string, error) {
		return "", errors.New("find_references is not offered")
	}
	t.Cleanup(func() { agentrun.DetectGoTypecheck = old })

	r2, _ := runSmoke(t, "--preflight")
	if r2.code != toolio.ExitOK {
		t.Fatalf("failing detection: exit %d\nstdout:\n%s\nstderr:\n%s", r2.code, r2.out, r2.err)
	}

	// Both runs: stage preflight.
	if r1.stage() != "preflight" || r2.stage() != "preflight" {
		t.Errorf("stages: %q, %q", r1.stage(), r2.stage())
	}

	// The failing run has no go_typecheck entry.
	res2, _ := r2.env["result"].(map[string]any)
	list2, _ := res2["preflight"].([]any)
	for _, entry := range list2 {
		m, _ := entry.(map[string]any)
		if m["check"] == "go_typecheck" {
			t.Error("failing run has a go_typecheck entry")
		}
	}

	// The successful run may or may not have go_typecheck (depends on whether
	// find_references is offered). Extract the checks minus go_typecheck from
	// both and compare.
	extractChecks := func(env map[string]any) []map[string]any {
		res, _ := env["result"].(map[string]any)
		list, _ := res["preflight"].([]any)
		var out []map[string]any
		for _, entry := range list {
			m, _ := entry.(map[string]any)
			if m["check"] != "go_typecheck" {
				out = append(out, m)
			}
		}
		return out
	}

	checks1 := extractChecks(r1.env)
	checks2 := extractChecks(r2.env)

	if len(checks1) != len(checks2) {
		t.Fatalf("check counts (minus go_typecheck) differ: %d vs %d", len(checks1), len(checks2))
	}
	for i := range checks1 {
		if checks1[i]["check"] != checks2[i]["check"] {
			t.Errorf("check[%d]: %v vs %v", i, checks1[i]["check"], checks2[i]["check"])
		}
		if checks1[i]["ok"] != checks2[i]["ok"] {
			t.Errorf("check[%d].ok: %v vs %v", i, checks1[i]["ok"], checks2[i]["ok"])
		}
		if checks1[i]["detail"] != checks2[i]["detail"] {
			t.Errorf("check[%d].detail: %v vs %v", i, checks1[i]["detail"], checks2[i]["detail"])
		}
	}

	// The last check in the failing run should be code_search_index.
	if len(checks2) > 0 {
		last := checks2[len(checks2)-1]
		if last["check"] != "code_search_index" {
			t.Errorf("last check in failing run = %v, want code_search_index", last["check"])
		}
	}

	// Estimates are equal.
	est1, _ := r1.env["result"].(map[string]any)["estimate"]
	est2, _ := r2.env["result"].(map[string]any)["estimate"]
	e1, _ := json.Marshal(est1)
	e2, _ := json.Marshal(est2)
	if string(e1) != string(e2) {
		t.Errorf("estimates differ:\n%s\n%s", e1, e2)
	}
}

// TS-17-55 (smoke): fix's analyse phase lists a function's callers in one
// find_references call, and the envelope counts that call under
// usage.phases[0].tool_calls.find_references.
//
// Verifies: 17-PATH-3, 17-REQ-3.1
//
// Real components: cmd/fix App, toolio.App.Main, agentrun.Runner,
// agentrun.SelectTools, tools.All, find_references tool, codefix.Run,
// toolCallCounter, git
func TestTS17_55_FixAnalysePhaseListsCallers(t *testing.T) {
	skipIfNoFindRefs(t)

	// Build a committed repository with main.go declaring func Count and
	// util.go with func Total calling Count.
	envtest.Clean(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

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
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package testmod\n\nfunc Count() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "util.go"), []byte("package testmod\n\nfunc Total() int { return Count() + Count() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "chore: initial commit"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, argv...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", argv, err, out)
		}
	}

	// Script: analyse calls find_references {name: Count} then submit_analysis;
	// implement calls write_file then submit_implementation.
	analysisArgs := map[string]any{
		"classification": "bug", "title": "fix the counter",
		"summary":    "the counter double-counts",
		"root_cause": "main.go counts twice",
		"approach":   "count once",
		"files":      []any{map[string]any{"path": "main.go", "change": "count once"}},
	}
	aRaw, _ := json.Marshal(analysisArgs)
	implArgs := map[string]any{
		"commit_subject": "fix: count once",
		"summary":        "counted once",
		"changes":        []any{map[string]any{"path": "main.go", "change": "count once"}},
	}
	iRaw, _ := json.Marshal(implArgs)

	p := faux.New(
		indextest.ToolTurn("fr1", "find_references", map[string]any{"name": "Count"}),
		faux.Turn{
			Blocks:     []core.ContentBlock{faux.FauxToolCall("a1", "submit_analysis", string(aRaw))},
			StopReason: core.StopReasonToolUse,
		},
		indextest.ToolTurn("i1", "write_file", map[string]any{"path": "main.go", "content": "package testmod\n\nfunc Count() int { return 2 }\n"}),
		faux.Turn{
			Blocks:     []core.ContentBlock{faux.FauxToolCall("i2", "submit_implementation", string(iRaw))},
			StopReason: core.StopReasonToolUse,
		},
	)
	t.Cleanup(toolio.SetRunnerConfigHook(func(cfg *agentrun.Config) {
		cfg.Model = faux.Model()
		cfg.Thinking = core.ThinkingUnset
		cfg.Providers = core.ProviderRegistry{faux.API: p.APIProvider()}
	}))

	// Drive the app directly (not through runSmoke, which creates its own repo).
	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	code := newApp().Main(ctx, normalizeArgs([]string{
		"--repo-map-tokens", "0",
		"--land", "none",
		"--verify", "true",
		"--no-review",
		"--dir", dir,
		"the counter double-counts",
	}), strings.NewReader(""), &stdout, &stderr)
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if code != toolio.ExitOK {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	// The first request's system prompt names find_references in step 2.
	reqs := p.Requests()
	if len(reqs) == 0 {
		t.Fatal("the model received no requests")
	}
	sys := ""
	for _, blk := range reqs[0].System {
		if tb, ok := blk.(core.TextBlock); ok {
			sys += tb.Text
		}
	}
	if !strings.Contains(sys, "find_references") {
		t.Error("the first request's system prompt does not name find_references")
	}
	if !strings.Contains(sys, "callers") {
		t.Error("the first request's system prompt does not mention callers")
	}

	// The 'Your tools are exactly:' list includes find_references.
	if !strings.Contains(sys, "Your tools are exactly:") {
		t.Error("the system prompt lacks the 'Your tools are exactly:' list")
	}
	toolsLine := ""
	for _, line := range strings.Split(sys, "\n") {
		if strings.Contains(line, "Your tools are exactly:") {
			toolsLine = line
			break
		}
	}
	if !strings.Contains(toolsLine, "find_references") {
		t.Errorf("the tools list does not include find_references: %s", toolsLine)
	}

	// The second request carries the find_references result, which lists the
	// site attributed to its enclosing declaration Total with a confidence
	// label (resolved, lexical or text).
	if len(reqs) < 2 {
		t.Fatal("the model received fewer than 2 requests")
	}
	secondReqJSON, _ := json.Marshal(reqs[1].Messages)
	secondReqStr := string(secondReqJSON)
	if !strings.Contains(secondReqStr, "Total") {
		t.Errorf("the second request does not mention Total (the enclosing declaration):\n%s", secondReqStr)
	}
	hasLabel := false
	for _, label := range []string{"resolved", "lexical", "text"} {
		if strings.Contains(secondReqStr, label) {
			hasLabel = true
			break
		}
	}
	if !hasLabel {
		t.Errorf("the second request does not contain a confidence label (resolved, lexical, text):\n%s", secondReqStr)
	}

	// The envelope's usage.phases[0].name is analyse with
	// tool_calls.find_references == 1.
	usage, _ := env["usage"].(map[string]any)
	phases, _ := usage["phases"].([]any)
	if len(phases) == 0 {
		t.Fatal("no phases in usage")
	}
	phase0, _ := phases[0].(map[string]any)
	if phase0["name"] != "analyse" {
		t.Errorf("phases[0].name = %v, want analyse", phase0["name"])
	}
	tc0, _ := phase0["tool_calls"].(map[string]any)
	if tc0["find_references"] != float64(1) {
		t.Errorf("phases[0].tool_calls.find_references = %v, want 1", tc0["find_references"])
	}

	// usage.phases[1] (implement) records no find_references call.
	if len(phases) > 1 {
		phase1, _ := phases[1].(map[string]any)
		tc1, _ := phase1["tool_calls"].(map[string]any)
		if tc1["find_references"] != nil && tc1["find_references"] != float64(0) {
			t.Errorf("phases[1].tool_calls.find_references = %v, want nil or 0", tc1["find_references"])
		}
	}
}
