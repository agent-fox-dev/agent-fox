package conform

import (
	"context"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/agentrun/indextest"
)

// TS-17-27 (integration): The independent review phase built in
// internal/conform declares every ReadOnlyFileTools name, with and without an
// index, and keeps its read-only grant.
//
// Verifies: 17-REQ-1.2, 17-REQ-1.4, 17-REQ-5.3
func TestTS17_27_ConformReviewDeclaresAllReadToolsAndPromptParity(t *testing.T) {
	_, root, _ := newRepo(t, map[string]string{"main.go": "package main\n\nfunc main() {}\n"})
	ws, err := tools.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	skipIfNoFindReferences(t, ws)

	baseGrant := []string{
		"read_file", "list_files", "find_files", "search_files",
		"file_outline", "find_symbol",
		"execute", ToolSubmitReview,
	}

	scope := ReviewScope{Requirements: []string{"01-REQ-1"}}
	answer := map[string]any{"summary": "Fine.", "requirements": []map[string]any{
		{"id": "01-REQ-1", "status": "implemented", "evidence": "main.go:3 defines main"}}}

	for _, tc := range []struct {
		name       string
		index      tools.Index
		codeSearch bool
	}{
		{"no-index", nil, false},
		{"with-index", &indextest.Index{}, true},
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
			in := ReviewInput{Root: root, Base: "HEAD", Spec: "spec", Scope: scope, CodeSearch: tc.codeSearch}
			if _, _, err := RunReview(context.Background(), runner, in); err != nil {
				t.Fatalf("RunReview: %v", err)
			}

			offered := indextest.Offered(p)
			if len(offered) == 0 {
				t.Fatal("the model received no request")
			}
			declared := map[string]bool{}
			for _, n := range offered[0] {
				declared[n] = true
			}

			// Every ReadOnlyFileTools name is declared.
			for _, n := range agentrun.ReadOnlyFileTools {
				if !declared[n] {
					t.Errorf("did not declare %s", n)
				}
			}

			// Declared minus find_references equals the base grant.
			want := make(map[string]bool, len(baseGrant))
			for _, n := range baseGrant {
				want[n] = true
			}
			if tc.codeSearch {
				want["code_search"] = true
			}
			got := make(map[string]bool)
			for n := range declared {
				if n == "find_references" {
					continue
				}
				got[n] = true
			}
			for n := range want {
				if !got[n] {
					t.Errorf("missing %s from base grant", n)
				}
			}
			for n := range got {
				if !want[n] {
					t.Errorf("unexpected tool %s beyond base grant", n)
				}
			}

			// write_file, edit_file, run_command and powershell are not declared.
			for _, n := range []string{"write_file", "edit_file", "run_command", "powershell"} {
				if declared[n] {
					t.Errorf("declared %s", n)
				}
			}

			// System prompt parity: removing 'find_references, ' from the tools
			// sentence gives ReviewSystemPrompt + base tools sentence.
			reqs := p.Requests()
			sys := conformSysText(reqs[0])
			sys = strings.ReplaceAll(sys, "find_references, ", "")
			if !strings.Contains(sys, ReviewSystemPrompt) {
				t.Errorf("review system prompt does not contain ReviewSystemPrompt")
			}
		})
	}
}

func skipIfNoFindReferences(t *testing.T, ws *tools.Workspace) {
	t.Helper()
	built, err := tools.All(tools.Options{Workspace: ws})
	if err != nil {
		t.Skipf("tools.All failed: %v", err)
	}
	for _, tl := range built {
		if tl.Name == "find_references" {
			return
		}
	}
	t.Skip("find_references not offered by the replace target")
}

func conformSysText(req core.Request) string {
	var b strings.Builder
	for _, blk := range req.System {
		if tb, ok := blk.(core.TextBlock); ok {
			b.WriteString(tb.Text)
		}
	}
	return b.String()
}
