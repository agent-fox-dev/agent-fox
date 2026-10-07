package codeimpl

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

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/agentrun/indextest"
)

// TS-17-24 (property): For any phase of impl and any index setting, the real
// brain declares every ReadOnlyFileTools name, keeps its base grant otherwise,
// and changes only the survey prompt's clause and the tools list.
//
// Verifies: 17-REQ-1.2, 17-REQ-3.4, 17-REQ-5.3
func TestTS17_24_ImplBrainDeclaresAllReadToolsAndPromptParity(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := tools.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	skipIfNoFindReferences(t, ws)

	baseSurveyGrant := []string{
		"read_file", "list_files", "find_files", "search_files",
		"file_outline", "find_symbol",
		"execute", ToolSubmitSurvey,
	}
	baseWritingGrant := []string{
		"read_file", "list_files", "find_files", "search_files",
		"file_outline", "find_symbol",
		"write_file", "edit_file", "execute",
	}

	baseSurvey, err := os.ReadFile(filepath.Join("..", "testdata", "prompts", "base", "impl_survey.txt"))
	if err != nil {
		t.Fatalf("read base survey prompt: %v", err)
	}

	clauseRe := regexp.MustCompile(`(?s) ?\(` + "`" + `find_references` + "`" + `[^)]*\)`)

	spec := &afspec.Spec{}
	task := afspec.Task{Id: 1, Title: "one"}

	for _, tc := range []struct {
		name  string
		index tools.Index
	}{
		{"no-index", nil},
		{"with-index", &indextest.Index{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			surveyArgs := map[string]any{
				"summary": "Fine.", "conventions": []string{"go test"},
			}
			repairArgs := map[string]any{
				"summary": "Fixed.", "commit_subject": "fix the baseline",
				"changes": []any{map[string]any{"path": "main.go", "change": "fixed"}},
			}
			taskArgs := map[string]any{
				"summary": "Done.", "commit_subject": "implement task 1",
				"changes": []any{map[string]any{"path": "main.go", "change": "done"}},
			}
			resolveArgs := map[string]any{
				"summary": "Resolved.",
				"changes": []any{map[string]any{"path": "main.go", "change": "resolved"}},
			}
			sRaw, _ := json.Marshal(surveyArgs)
			rRaw, _ := json.Marshal(repairArgs)
			tRaw, _ := json.Marshal(taskArgs)
			xRaw, _ := json.Marshal(resolveArgs)

			p := faux.New(
				faux.Turn{Blocks: []core.ContentBlock{faux.FauxToolCall("s1", ToolSubmitSurvey, string(sRaw))}, StopReason: core.StopReasonToolUse},
				faux.Turn{Blocks: []core.ContentBlock{faux.FauxToolCall("r1", ToolSubmitRepair, string(rRaw))}, StopReason: core.StopReasonToolUse},
				faux.Turn{Blocks: []core.ContentBlock{faux.FauxToolCall("t1", ToolSubmitTask, string(tRaw))}, StopReason: core.StopReasonToolUse},
				faux.Turn{Blocks: []core.ContentBlock{faux.FauxToolCall("x1", ToolSubmitResolve, string(xRaw))}, StopReason: core.StopReasonToolUse},
			)
			runner, err := agentrun.NewRunner(agentrun.Config{
				Model:         faux.Model(),
				Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
				Workspace:     ws,
				Index:         tc.index,
				Bounds:        agentrun.Bounds{MaxTurns: 6, MaxBudgetUSD: 1, MaxAttempts: 1},
				SessionPrefix: "impl",
			})
			if err != nil {
				t.Fatal(err)
			}
			brain := &agentBrain{runner: runner, codeSearch: tc.index != nil}

			type phaseRun struct {
				name       string
				submitTool string
				run        func() error
				isWriting  bool
			}

			phases := []phaseRun{
				{
					name: "survey", submitTool: ToolSubmitSurvey,
					run: func() error {
						_, _, err := brain.Survey(context.Background(), surveyInput{
							Spec: spec, Root: ws.Root,
						})
						return err
					},
				},
				{
					name: "repair", submitTool: ToolSubmitRepair, isWriting: true,
					run: func() error {
						_, _, err := brain.Repair(context.Background(), repairInput{
							Spec: spec, Root: ws.Root,
						})
						return err
					},
				},
				{
					name: "implement", submitTool: ToolSubmitTask, isWriting: true,
					run: func() error {
						_, _, err := brain.Implement(context.Background(), taskInput{
							Spec: spec, Task: task, Root: ws.Root,
						})
						return err
					},
				},
				{
					name: "resolve", submitTool: ToolSubmitResolve, isWriting: true,
					run: func() error {
						_, _, err := brain.Resolve(context.Background(), resolveInput{
							Spec: spec, Root: ws.Root,
						})
						return err
					},
				},
			}

			reqStart := 0
			for _, ph := range phases {
				if err := ph.run(); err != nil {
					t.Fatalf("%s: %v", ph.name, err)
				}
				reqs := p.Requests()
				if len(reqs) <= reqStart {
					t.Fatalf("%s: no request reached the wire", ph.name)
				}
				req := reqs[reqStart]
				reqStart = len(reqs)

				declared := map[string]bool{}
				for _, tl := range req.Tools {
					declared[tl.Name] = true
				}

				// Every ReadOnlyFileTools name is declared.
				for _, n := range agentrun.ReadOnlyFileTools {
					if !declared[n] {
						t.Errorf("%s: did not declare %s", ph.name, n)
					}
				}

				// Declared minus find_references equals the base grant.
				var base []string
				if ph.name == "survey" {
					base = baseSurveyGrant
				} else {
					base = append(baseWritingGrant, ph.submitTool)
				}
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
						t.Errorf("%s: missing %s from base grant", ph.name, n)
					}
				}
				for n := range got {
					if !want[n] {
						t.Errorf("%s: unexpected tool %s beyond base grant", ph.name, n)
					}
				}

				// System prompt parity.
				sys := sysText(req)
				sys = strings.ReplaceAll(sys, "find_references, ", "")
				if ph.name == "survey" {
					// Only survey's system prompt differs from base beyond the tools list.
					stripped := clauseRe.ReplaceAllString(sys, "")
					if !strings.Contains(stripped, string(baseSurvey)) {
						t.Errorf("survey system prompt (clause removed) does not contain the base survey prompt")
					}
				} else {
					// Repair, implement and resolve: no clause, just find_references in tools list.
					var basePrompt string
					switch ph.name {
					case "repair":
						basePrompt = repairSystemPrompt
					case "implement":
						basePrompt = implementSystemPrompt
					case "resolve":
						basePrompt = resolveSystemPrompt
					}
					if !strings.Contains(sys, basePrompt) {
						t.Errorf("%s system prompt does not contain the base prompt", ph.name)
					}
				}

				// No user message contains find_references.
				user := usrText(req)
				if strings.Contains(user, "find_references") {
					t.Errorf("%s user message contains find_references", ph.name)
				}
			}
		})
	}
}

func sysText(req core.Request) string {
	var b strings.Builder
	for _, blk := range req.System {
		if tb, ok := blk.(core.TextBlock); ok {
			b.WriteString(tb.Text)
		}
	}
	return b.String()
}

func usrText(req core.Request) string {
	for _, m := range req.Messages {
		um, ok := m.(core.UserMessage)
		if !ok {
			continue
		}
		var b strings.Builder
		for _, blk := range um.Content {
			if tb, ok := blk.(core.TextBlock); ok {
				b.WriteString(tb.Text)
			}
		}
		return b.String()
	}
	return ""
}
