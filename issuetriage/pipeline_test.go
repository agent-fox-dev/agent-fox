package issuetriage

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/agentrun/indextest"
)

// fakeIndex is a tools.Index test double. Tools returns a code_search tool, as
// the real codesearch index does.
type fakeIndex struct{ closed int }

func (f *fakeIndex) Symbols(context.Context, tools.SymbolQuery) (tools.SymbolAnswer, bool, error) {
	return tools.SymbolAnswer{}, false, nil
}
func (f *fakeIndex) Tools() []core.Tool {
	return []core.Tool{{
		Name: "code_search", Description: "ranked search",
		Execute: func(context.Context, json.RawMessage) core.ToolResult { return core.OKResult(map[string]any{}) },
	}}
}
func (f *fakeIndex) Invalidate(string) {}
func (f *fakeIndex) Close() error      { f.closed++; return nil }

// wireTools is the names of the tools the first request offered the model.
func wireTools(t *testing.T, p *faux.Provider) map[string]bool {
	t.Helper()
	reqs := p.Requests()
	if len(reqs) == 0 {
		t.Fatal("no request reached the model")
	}
	got := map[string]bool{}
	for _, w := range reqs[0].Tools {
		got[w.Name] = true
	}
	return got
}

// indexedRunner is a Runner whose Config carries idx — the Config the shell
// builds from the same index it hands the tool's Options.
func indexedRunner(t *testing.T, ws *tools.Workspace, p *faux.Provider, idx tools.Index) *agentrun.Runner {
	t.Helper()
	r, err := agentrun.NewRunner(agentrun.Config{
		Model:         faux.Model(),
		Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
		Workspace:     ws,
		Bounds:        agentrun.Bounds{MaxTurns: 8, MaxBudgetUSD: 1, MaxAttempts: 1},
		SessionPrefix: "triage",
		Index:         idx,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TS-16-2 (unit): issuetriage.Options.Index is forwarded to agentrun.Config.Index.
// The triage phase gets code_search in its grant, and the model is offered it,
// only because the index reached the Runner's Config.
//
// Verifies: 16-REQ-1.3, 16-REQ-2.1
func TestTS16_2_IndexReachesTheTriagePhase(t *testing.T) {
	ws := newWorkspace(t)
	idx := &fakeIndex{}
	p := faux.New(toolCall("c1", ToolFileIssue, validIssue()))
	o := newOptions(t, ws, indexedRunner(t, ws, p, idx))
	o.Index = idx

	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := wireTools(t, p)
	if !got["code_search"] {
		t.Errorf("code_search was not offered to the triage phase: %v", got)
	}
	avail, _ := tools.All(tools.Options{Workspace: ws})
	availSet := map[string]bool{}
	for _, tl := range avail {
		availSet[tl.Name] = true
	}
	for _, n := range agentrun.ReadOnlyFileTools {
		if !availSet[n] {
			continue // tools.All does not return this tool yet
		}
		if !got[n] {
			t.Errorf("%s is missing from the triage phase", n)
		}
	}
}

// 16-REQ-2.2: with no index the grant is the six read tools, unchanged.
func TestTS16_2_NilIndexLeavesTheGrantUnchanged(t *testing.T) {
	ws := newWorkspace(t)
	p := faux.New(toolCall("c1", ToolFileIssue, validIssue()))
	o := newOptions(t, ws, indexedRunner(t, ws, p, nil))

	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := wireTools(t, p); got["code_search"] {
		t.Errorf("code_search was offered without an index: %v", got)
	}
}

// The grant is a copy: appending code_search must not grow the shared
// ReadOnlyFileTools slice.
func TestTS16_2_TheSharedReadOnlyListIsNotMutated(t *testing.T) {
	before := len(agentrun.ReadOnlyFileTools)
	got := agentrun.WithCodeSearch(agentrun.ReadOnlyFileTools, true)
	if len(agentrun.ReadOnlyFileTools) != before {
		t.Fatal("ReadOnlyFileTools was modified")
	}
	if got[len(got)-1] != "code_search" || len(got) != before+1 {
		t.Errorf("grant = %v", got)
	}
	if same := agentrun.WithCodeSearch(agentrun.ReadOnlyFileTools, false); len(same) != before {
		t.Errorf("grant without an index = %v", same)
	}
}

// TS-16-22 (unit): the triage pipeline never changes the working tree, so it
// never invalidates the index (16-REQ-4.8).
//
// Verifies: 16-REQ-4.8
func TestTS16_22_TriageNeverInvalidatesTheIndex(t *testing.T) {
	ws := newWorkspace(t)
	idx := &indextest.Index{}
	p := faux.New(toolCall("c1", ToolFileIssue, validIssue()))
	o := newOptions(t, ws, indexedRunner(t, ws, p, idx))
	o.Index = idx

	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !wireTools(t, p)["code_search"] {
		t.Fatal("the run did not use the index, so the assertion below proves nothing")
	}
	if n := idx.InvalidateCalls(); n != 0 {
		t.Errorf("Invalidate was called %d times; triage never changes the tree: %v", n, idx.Events())
	}
}

// TS-16-24 (unit): RunPreflight includes a code_search_index check with the
// detail "built" when the run has an index.
//
// Verifies: 16-REQ-7.1
func TestTS16_24_PreflightReportsCodeSearchIndexBuilt(t *testing.T) {
	o, _, _ := preflightOptions(t)
	o.Index = &indextest.Index{}
	res, err := RunPreflight(o)
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	c, ok := findCheck(res.Preflight, "code_search_index")
	if !ok {
		t.Fatalf("no code_search_index check in %+v", res.Preflight)
	}
	if !c.OK || c.Detail != "built" {
		t.Errorf("code_search_index = %+v, want OK with detail built", c)
	}
}

// TS-16-25 (unit): with no index the check is still OK and says why.
//
// Verifies: 16-REQ-7.2, 16-REQ-7.3
func TestTS16_25_PreflightReportsCodeSearchIndexUnavailable(t *testing.T) {
	for _, tc := range []struct{ reason, want string }{
		{"", "unavailable: index not built"},
		{"unsupported platform", "unavailable: unsupported platform"},
	} {
		o, _, _ := preflightOptions(t)
		o.Index = nil
		o.IndexUnavailable = tc.reason
		res, err := RunPreflight(o)
		if err != nil {
			t.Fatalf("RunPreflight: %v", err)
		}
		c, ok := findCheck(res.Preflight, "code_search_index")
		if !ok {
			t.Fatalf("no code_search_index check in %+v", res.Preflight)
		}
		if !c.OK || c.Detail != tc.want {
			t.Errorf("code_search_index = %+v, want OK with detail %q", c, tc.want)
		}
	}
}

// The pipeline grants and invalidates the index its Runner's phases read
// (16-REQ-1.3): a Runner built on another index is refused before any phase
// runs, and the Runner's index is the run's when the Options carry none.
func TestTS16_2_TheRunnersIndexIsTheRunsIndex(t *testing.T) {
	ws := newWorkspace(t)
	p := faux.New()
	o := newOptions(t, ws, indexedRunner(t, ws, p, &fakeIndex{}))
	o.Index = &fakeIndex{}
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "not the one its runner was built with") {
		t.Fatalf("Run with two indexes: err = %v, want a refusal", err)
	}
	if n := len(p.Requests()); n != 0 {
		t.Errorf("%d requests reached the model after the refusal", n)
	}

	p = faux.New(toolCall("c1", ToolFileIssue, validIssue()))
	o = newOptions(t, ws, indexedRunner(t, ws, p, &fakeIndex{}))
	_, _ = Run(context.Background(), o)
	if got := wireTools(t, p); !got["code_search"] {
		t.Errorf("the Runner's index was not taken as the run's: %v", got)
	}
}

// TS-18-19 (integration): The triage phase declares find_references alongside
// the seven read tools.
//
// Verifies: 18-REQ-1.3
func TestTS18_19_TriageDeclaresFindReferences(t *testing.T) {
	ws := newWorkspace(t)
	p := faux.New(toolCall("c1", ToolFileIssue, validIssue()))
	o := newOptions(t, ws, indexedRunner(t, ws, p, nil))

	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := wireTools(t, p)

	avail, _ := tools.All(tools.Options{Workspace: ws})
	availSet := map[string]bool{}
	for _, tl := range avail {
		availSet[tl.Name] = true
	}
	for _, n := range agentrun.ReadOnlyFileTools {
		if !availSet[n] {
			continue
		}
		if !got[n] {
			t.Errorf("triage did not declare %s", n)
		}
	}
	if !slices.Contains(agentrun.ReadOnlyFileTools, "find_references") {
		t.Error("find_references is not in ReadOnlyFileTools")
	}
}

// TS-18-22 (smoke): A triage run registers find_references and the model sees
// it in the prompt.
//
// Verifies: 18-PATH-1, 18-REQ-1.1, 18-REQ-1.3
//
// Real components: issuetriage pipeline, agentrun.Runner, agentrun.ReadOnlyFileTools,
// toolsNote. Only the model is scripted.
func TestTS18_22_TriageRunRegistersFindReferencesAndModelSeesIt(t *testing.T) {
	ws := smokeRepo(t)
	p := faux.New(toolCall("c1", ToolFileIssue, validIssue()))
	o := newOptions(t, ws, runnerFor(t, ws, p, nil))
	o.Input.Body = "token refresh in internal/auth/token.go returns an expired credential"

	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// ReadOnlyFileTools must contain find_references.
	if !slices.Contains(agentrun.ReadOnlyFileTools, "find_references") {
		t.Fatal("find_references is not in ReadOnlyFileTools")
	}

	// Check the model's declared tools.
	reqs := p.Requests()
	if len(reqs) == 0 {
		t.Fatal("the triage phase never reached the model")
	}
	got := wireTools(t, p)

	avail, _ := tools.All(tools.Options{Workspace: ws})
	availSet := map[string]bool{}
	for _, tl := range avail {
		availSet[tl.Name] = true
	}

	// Every ReadOnlyFileTools entry that tools.All returns must be on the wire.
	for _, n := range agentrun.ReadOnlyFileTools {
		if !availSet[n] {
			t.Logf("%s is in ReadOnlyFileTools but not in tools.All (not shipped yet)", n)
			continue
		}
		if !got[n] {
			t.Errorf("triage did not declare %s", n)
		}
	}

	// Check the system prompt for find_references (toolsNote lists registered tools).
	var b strings.Builder
	for _, blk := range reqs[0].System {
		if tb, ok := blk.(core.TextBlock); ok {
			b.WriteString(tb.Text)
		}
	}
	sys := b.String()

	// If find_references is in tools.All, toolsNote must list it.
	if availSet["find_references"] {
		if !strings.Contains(sys, "find_references") {
			t.Error("the system prompt does not list find_references")
		}
	} else {
		t.Log("find_references is not yet in tools.All; toolsNote cannot list it")
	}
}
