package issuetriage

import (
	"context"
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

// TS-17-26 (property): For any index setting, the real triage phase declares
// every ReadOnlyFileTools name, gains no write or shell tool, and changes only
// the prompt's clause and the tools list.
//
// Verifies: 17-REQ-1.2, 17-REQ-3.4, 17-REQ-5.3
func TestTS17_26_TriageDeclaresAllReadToolsAndPromptParity(t *testing.T) {
	ws := newWorkspace(t)
	skipIfNoFindReferences(t, ws)

	baseTriageGrant := []string{
		"read_file", "list_files", "find_files", "search_files",
		"file_outline", "find_symbol",
		ToolFileIssue,
	}

	baseSystem, err := os.ReadFile(filepath.Join("..", "testdata", "prompts", "base", "triage_system.txt"))
	if err != nil {
		t.Fatalf("read base triage prompt: %v", err)
	}

	clauseRe := regexp.MustCompile(`(?s) ?\(` + "`" + `find_references` + "`" + `[^)]*\)`)

	in := toolio.Input{Kind: toolio.KindText, Origin: "argument", Body: "token refresh returns an expired credential"}

	for _, tc := range []struct {
		name  string
		index tools.Index
	}{
		{"no-index", nil},
		{"with-index", &indextest.Index{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := faux.New(toolCall("c1", ToolFileIssue, validIssue()))
			o := newOptions(t, ws, indexedRunner(t, ws, p, tc.index))
			if tc.index != nil {
				o.Index = tc.index
			}

			if _, err := Run(context.Background(), o); err != nil {
				t.Fatalf("Run: %v", err)
			}

			reqs := p.Requests()
			if len(reqs) == 0 {
				t.Fatal("no request reached the wire")
			}
			req := reqs[0]
			declared := map[string]bool{}
			for _, tl := range req.Tools {
				declared[tl.Name] = true
			}

			// Every ReadOnlyFileTools name is declared.
			for _, n := range agentrun.ReadOnlyFileTools {
				if !declared[n] {
					t.Errorf("did not declare %s", n)
				}
			}

			// No MutatingTools name is declared.
			for _, n := range agentrun.MutatingTools {
				if declared[n] {
					t.Errorf("declared mutating tool %s", n)
				}
			}

			// Declared minus find_references equals the base grant.
			want := make(map[string]bool, len(baseTriageGrant))
			for _, n := range baseTriageGrant {
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
					t.Errorf("missing %s from base grant", n)
				}
			}
			for n := range got {
				if !want[n] {
					t.Errorf("unexpected tool %s beyond base grant", n)
				}
			}

			// System prompt parity: removing the clause and 'find_references, '
			// from the tools sentence gives the base prompt + base tools sentence.
			sys := triageSysText(req)
			sys = clauseRe.ReplaceAllString(sys, "")
			sys = strings.ReplaceAll(sys, "find_references, ", "")
			if !strings.Contains(sys, string(baseSystem)) {
				t.Errorf("triage system prompt (clause removed) does not contain the base prompt")
			}

			// User message equals the reference task prompt and has no find_references.
			user := triageUsrText(req)
			ref := taskPrompt(in, ws.Root, "")
			if user != ref {
				t.Errorf("user message differs from reference task prompt")
			}
			if strings.Contains(user, "find_references") {
				t.Error("user message contains find_references")
			}
		})
	}
}

func triageSysText(req core.Request) string {
	var b strings.Builder
	for _, blk := range req.System {
		if tb, ok := blk.(core.TextBlock); ok {
			b.WriteString(tb.Text)
		}
	}
	return b.String()
}

func triageUsrText(req core.Request) string {
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
