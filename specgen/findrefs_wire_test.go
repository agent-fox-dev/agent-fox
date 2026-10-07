package specgen

import (
	"context"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/agentrun/indextest"
)

// TS-17-25 (property): For any phase of spec and any index setting, the real
// author declares every ReadOnlyFileTools name, gains no write or shell tool,
// and changes only find_references in the tools list.
//
// Verifies: 17-REQ-1.2, 17-REQ-3.4, 17-REQ-5.3
func TestTS17_25_SpecAuthorDeclaresAllReadToolsAndPromptParity(t *testing.T) {
	ws := newWorkspace(t)
	skipIfNoFindReferences(t, ws)

	basePRDGrant := []string{
		"read_file", "list_files", "find_files", "search_files",
		"file_outline", "find_symbol",
		ToolSubmitPRD,
	}
	baseGenerateGrant := []string{
		"read_file", "list_files", "find_files", "search_files",
		"file_outline", "find_symbol",
		// The generate tool name varies by step; we check it separately.
	}
	baseArchGrant := []string{
		"read_file", "list_files", "find_files", "search_files",
		"file_outline", "find_symbol",
		ToolSubmitArchitecture,
	}

	for _, tc := range []struct {
		name  string
		index tools.Index
	}{
		{"no-index", nil},
		{"with-index", &indextest.Index{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// PRD phase.
			t.Run("prd", func(t *testing.T) {
				p := faux.New()
				r := specRunner(t, ws, p, tc.index)
				a := &agentAuthor{runner: r, ws: ws, codeSearch: tc.index != nil}
				_, _, _ = a.WritePRD(context.Background(), prdRequest{Root: ws.Root})
				checkSpecPhase(t, p, "prd", basePRDGrant, tc.index != nil)
			})

			// Generate phase (requirements step).
			t.Run("generate", func(t *testing.T) {
				p := faux.New()
				r := specRunner(t, ws, p, tc.index)
				a := &agentAuthor{runner: r, ws: ws, codeSearch: tc.index != nil}
				_, _, _ = a.GenerateArtifact(context.Background(), artifactRequest{
					Step:    afspec.StepRequirements,
					Partial: &afspec.PartialSpec{},
				})
				// The generate tool name is step-specific.
				genGrant := append(append([]string(nil), baseGenerateGrant...), ArtifactToolName(afspec.StepRequirements))
				checkSpecPhase(t, p, "generate", genGrant, tc.index != nil)
			})

			// Architecture phase.
			t.Run("architecture", func(t *testing.T) {
				p := faux.New()
				r := specRunner(t, ws, p, tc.index)
				a := &agentAuthor{runner: r, ws: ws, codeSearch: tc.index != nil}
				_, _, _ = a.WriteArchitecture(context.Background(), architectureRequest{Partial: &afspec.PartialSpec{}})
				checkSpecPhase(t, p, "architecture", baseArchGrant, tc.index != nil)
			})
		})
	}
}

func specRunner(t *testing.T, ws *tools.Workspace, p *faux.Provider, idx tools.Index) *agentrun.Runner {
	t.Helper()
	r, err := agentrun.NewRunner(agentrun.Config{
		Model:         faux.Model(),
		Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
		Workspace:     ws,
		Index:         idx,
		Bounds:        agentrun.Bounds{MaxTurns: 6, MaxBudgetUSD: 1, MaxAttempts: 1},
		SessionPrefix: "spec",
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func checkSpecPhase(t *testing.T, p *faux.Provider, phase string, baseGrant []string, hasIndex bool) {
	t.Helper()
	reqs := p.Requests()
	if len(reqs) == 0 {
		t.Fatalf("%s: no request reached the wire", phase)
	}
	req := reqs[0]
	declared := map[string]bool{}
	for _, tl := range req.Tools {
		declared[tl.Name] = true
	}

	// Every ReadOnlyFileTools name is declared.
	for _, n := range agentrun.ReadOnlyFileTools {
		if !declared[n] {
			t.Errorf("%s: did not declare %s", phase, n)
		}
	}

	// No MutatingTools name is declared.
	for _, n := range agentrun.MutatingTools {
		if declared[n] {
			t.Errorf("%s: declared mutating tool %s", phase, n)
		}
	}

	// Declared minus find_references equals the base grant.
	want := make(map[string]bool, len(baseGrant))
	for _, n := range baseGrant {
		want[n] = true
	}
	if hasIndex {
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

	// System prompt: no clause anywhere, just find_references in tools list.
	sys := specSysText(req)
	sys = strings.ReplaceAll(sys, "find_references, ", "")
	if strings.Contains(sys, "find_references") {
		t.Errorf("%s: system prompt still contains find_references after removing from tools list", phase)
	}

	// User message does not contain find_references.
	user := specUsrText(req)
	if strings.Contains(user, "find_references") {
		t.Errorf("%s: user message contains find_references", phase)
	}
}

func specSysText(req core.Request) string {
	var b strings.Builder
	for _, blk := range req.System {
		if tb, ok := blk.(core.TextBlock); ok {
			b.WriteString(tb.Text)
		}
	}
	return b.String()
}

func specUsrText(req core.Request) string {
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
