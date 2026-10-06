package conform

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/agentrun/indextest"
)

func turn(id, name string, args any) faux.Turn {
	raw, err := json.Marshal(args)
	if err != nil {
		panic(err)
	}
	return faux.Turn{Blocks: []core.ContentBlock{faux.FauxToolCall(id, name, string(raw))},
		StopReason: core.StopReasonToolUse}
}

// The review phase end to end through the real runner: the model's first
// submission skips an id and is refused by the tool, the second answers
// every one, and RunReview returns it.
func TestRunReviewThroughTheRealRunner(t *testing.T) {
	_, root, _ := newRepo(t, map[string]string{"main.go": "package main\n\nfunc main() {}\n"})
	ws, err := tools.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	scope := ReviewScope{Requirements: []string{"01-REQ-1"}, Tests: []string{"TS-01-1"}}
	incomplete := map[string]any{"summary": "Looks fine.", "requirements": []map[string]any{
		{"id": "01-REQ-1", "status": "implemented", "evidence": "main.go:3 defines main"}}}
	complete := map[string]any{"summary": "Main exists; its test asserts nothing.",
		"requirements": []map[string]any{{"id": "01-REQ-1", "status": "implemented", "evidence": "main.go:3 defines main"}},
		"tests":        []map[string]any{{"id": "TS-01-1", "assessment": "missing", "evidence": "no test function exists for it"}}}
	p := faux.New(turn("r1", ToolSubmitReview, incomplete), turn("r2", ToolSubmitReview, complete))
	runner, err := agentrun.NewRunner(agentrun.Config{
		Model:     faux.Model(),
		Providers: core.ProviderRegistry{faux.API: p.APIProvider()},
		Workspace: ws,
		Bounds:    agentrun.Bounds{MaxTurns: 5, MaxBudgetUSD: 1, MaxAttempts: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, res, err := RunReview(context.Background(), runner, ReviewInput{Root: root, Base: "HEAD", Spec: "spec", Scope: scope})
	if err != nil {
		t.Fatalf("RunReview: %v", err)
	}
	if res.Name != PhaseReview || res.Turns != 2 {
		t.Errorf("phase %q ran %d turns; the incomplete review should have cost one", res.Name, res.Turns)
	}
	if len(got.Tests) != 1 || got.Tests[0].Assessment != AssessMissing || len(got.Blockers()) != 1 {
		t.Errorf("review = %+v", got)
	}
}

// The review phase is granted code_search exactly when the run has an index
// and says so (16-REQ-2.1, 16-REQ-2.2).
func TestRunReviewOffersCodeSearchOnlyWithAnIndex(t *testing.T) {
	_, root, _ := newRepo(t, map[string]string{"main.go": "package main\n\nfunc main() {}\n"})
	ws, err := tools.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	scope := ReviewScope{Requirements: []string{"01-REQ-1"}}
	answer := map[string]any{"summary": "Fine.", "requirements": []map[string]any{
		{"id": "01-REQ-1", "status": "implemented", "evidence": "main.go:3 defines main"}}}
	for _, tc := range []struct {
		name  string
		index tools.Index
		grant bool
	}{
		{"an index", &indextest.Index{}, true},
		{"an index but no grant", &indextest.Index{}, false},
		{"no index", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := faux.New(turn("r1", ToolSubmitReview, answer))
			runner, err := agentrun.NewRunner(agentrun.Config{
				Model:     faux.Model(),
				Providers: core.ProviderRegistry{faux.API: p.APIProvider()},
				Workspace: ws,
				Index:     tc.index,
				Bounds:    agentrun.Bounds{MaxTurns: 5, MaxBudgetUSD: 1, MaxAttempts: 1},
			})
			if err != nil {
				t.Fatal(err)
			}
			in := ReviewInput{Root: root, Base: "HEAD", Spec: "spec", Scope: scope, CodeSearch: tc.grant && tc.index != nil}
			if _, _, err := RunReview(context.Background(), runner, in); err != nil {
				t.Fatalf("RunReview: %v", err)
			}
			offered := indextest.Offered(p)
			if len(offered) == 0 {
				t.Fatal("the model received no request")
			}
			if got := indextest.Has(offered[0], "code_search"); got != in.CodeSearch {
				t.Errorf("code_search offered = %v, want %v: %v", got, in.CodeSearch, offered[0])
			}
		})
	}
}
