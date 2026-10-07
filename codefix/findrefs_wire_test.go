package codefix

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/agentrun/indextest"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-17-23 (property): For any phase of fix and any index setting, the real
// brain declares every ReadOnlyFileTools name, keeps its base grant otherwise,
// and sends the base system prompt plus only the clause and find_references in
// the tools list.
//
// Verifies: 17-REQ-1.2, 17-REQ-1.4, 17-REQ-3.4, 17-REQ-5.3
func TestTS17_23_FixBrainDeclaresAllReadToolsAndPromptParity(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := tools.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	skipIfNoFindReferences(t, ws)

	baseAnalyseGrant := []string{
		"read_file", "list_files", "find_files", "search_files",
		"file_outline", "find_symbol",
		"execute", ToolSubmitAnalysis,
	}
	baseImplementGrant := []string{
		"read_file", "list_files", "find_files", "search_files",
		"file_outline", "find_symbol",
		"write_file", "edit_file", "execute", ToolSubmitImplementation,
	}

	baseAnalysis, err := os.ReadFile(filepath.Join("..", "testdata", "prompts", "base", "fix_analysis.txt"))
	if err != nil {
		t.Fatalf("read base analysis prompt: %v", err)
	}

	clauseRe := regexp.MustCompile(`(?s) ?\(` + "`" + `find_references` + "`" + `[^)]*\)`)

	in := toolio.Input{Kind: toolio.KindText, Origin: "argument", Body: "the widget is broken"}

	for _, tc := range []struct {
		name  string
		index tools.Index
	}{
		{"no-index", nil},
		{"with-index", &indextest.Index{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			analysisArgs := map[string]any{
				"classification": "bug", "title": "fix the widget",
				"summary": "It is broken.", "root_cause": "widget.go is wrong.",
				"approach": "Fix it.", "files": []any{map[string]any{"path": "main.go", "change": "fix"}},
			}
			implArgs := map[string]any{
				"summary": "Fixed.", "commit_subject": "fix the widget",
				"changes": []any{map[string]any{"path": "main.go", "change": "fixed"}},
			}
			aRaw, _ := json.Marshal(analysisArgs)
			iRaw, _ := json.Marshal(implArgs)
			p := faux.New(
				faux.Turn{Blocks: []core.ContentBlock{faux.FauxToolCall("a1", ToolSubmitAnalysis, string(aRaw))}, StopReason: core.StopReasonToolUse},
				faux.Turn{Blocks: []core.ContentBlock{faux.FauxToolCall("i1", ToolSubmitImplementation, string(iRaw))}, StopReason: core.StopReasonToolUse},
			)
			cfg := agentrun.Config{
				Model:         faux.Model(),
				Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
				Workspace:     ws,
				Index:         tc.index,
				Bounds:        agentrun.Bounds{MaxTurns: 6, MaxBudgetUSD: 1, MaxAttempts: 1},
				SessionPrefix: "fix",
			}
			runner, err := agentrun.NewRunner(cfg)
			if err != nil {
				t.Fatal(err)
			}
			brain := &agentBrain{runner: runner, codeSearch: tc.index != nil}

			// Run analyse.
			_, _, err = brain.Analyze(context.Background(), analysisInput{
				Input: in, Root: ws.Root,
			})
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			nAnalyse := len(p.Requests())

			// Run implement.
			_, _, err = brain.Implement(context.Background(), implementInput{
				Input: in, Root: ws.Root,
				Analysis: Analysis{Classification: ClassBug, Title: "fix"},
			})
			if err != nil {
				t.Fatalf("Implement: %v", err)
			}

			reqs := p.Requests()
			if nAnalyse == 0 || len(reqs) <= nAnalyse {
				t.Fatalf("expected requests from both phases, got %d then %d", nAnalyse, len(reqs))
			}

			toolNames := func(req core.Request) map[string]bool {
				m := map[string]bool{}
				for _, tl := range req.Tools {
					m[tl.Name] = true
				}
				return m
			}

			analyseReq := reqs[0]
			implementReq := reqs[nAnalyse]
			aDeclared := toolNames(analyseReq)
			iDeclared := toolNames(implementReq)

			// Every ReadOnlyFileTools name is declared.
			for _, n := range agentrun.ReadOnlyFileTools {
				if !aDeclared[n] {
					t.Errorf("analyse did not declare %s", n)
				}
				if !iDeclared[n] {
					t.Errorf("implement did not declare %s", n)
				}
			}

			// Declared minus find_references equals the base grant.
			checkGrant := func(declared map[string]bool, phase string, base []string) {
				t.Helper()
				want := make(map[string]bool, len(base))
				for _, n := range base {
					want[n] = true
				}
				if tc.index != nil {
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
						t.Errorf("%s: missing %s from base grant", phase, n)
					}
				}
				for n := range got {
					if !want[n] {
						t.Errorf("%s: unexpected tool %s beyond base grant", phase, n)
					}
				}
			}
			checkGrant(aDeclared, "analyse", baseAnalyseGrant)
			checkGrant(iDeclared, "implement", baseImplementGrant)

			// Analyse declares no write_file or edit_file.
			for _, n := range []string{"write_file", "edit_file"} {
				if aDeclared[n] {
					t.Errorf("analyse declared %s", n)
				}
			}

			// System prompt parity: removing the clause and 'find_references, '
			// from the tools sentence gives the base prompt + base tools sentence.
			sys := systemText(analyseReq)
			sys = clauseRe.ReplaceAllString(sys, "")
			sys = strings.ReplaceAll(sys, "find_references, ", "")
			if !strings.Contains(sys, string(baseAnalysis)) {
				t.Errorf("analyse system prompt (clause removed) does not contain the base analysis prompt")
			}

			// Implement system prompt: no clause, just find_references in tools list.
			iSys := systemText(implementReq)
			iSys = strings.ReplaceAll(iSys, "find_references, ", "")
			if !strings.Contains(iSys, implementSystemPrompt) {
				t.Errorf("implement system prompt does not contain the base implement prompt")
			}

			// User message does not contain find_references.
			aUser := firstUserText(reqs[:nAnalyse])
			if strings.Contains(aUser, "find_references") {
				t.Error("analyse user message contains find_references")
			}
			iUser := firstUserText(reqs[nAnalyse:])
			if strings.Contains(iUser, "find_references") {
				t.Error("implement user message contains find_references")
			}
		})
	}
}
