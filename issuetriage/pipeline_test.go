package issuetriage

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
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
	for _, n := range agentrun.ReadOnlyFileTools {
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
	got := withCodeSearch(agentrun.ReadOnlyFileTools, true)
	if len(agentrun.ReadOnlyFileTools) != before {
		t.Fatal("ReadOnlyFileTools was modified")
	}
	if got[len(got)-1] != "code_search" || len(got) != before+1 {
		t.Errorf("grant = %v", got)
	}
	if same := withCodeSearch(agentrun.ReadOnlyFileTools, false); len(same) != before {
		t.Errorf("grant without an index = %v", same)
	}
}
