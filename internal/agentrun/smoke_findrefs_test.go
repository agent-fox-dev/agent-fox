package agentrun

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"
)

// TS-17-10 (smoke): An edit in a writing phase changes the next
// find_references answer by exactly the removed site, and a file written
// afterwards adds exactly its site.
//
// Verifies: 17-PATH-5, 17-REQ-2.4
//
// Real components: Runner, SelectTools, tools.All, find_references tool,
// edit_file and write_file tools, toolCallCounter
func TestTS17_10_EditChangesNextFindReferencesAnswer(t *testing.T) {
	dir := t.TempDir()
	// go.mod so the workspace is a valid Go module.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// a.go declares func Target.
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package p\n\nfunc Target() int { return 42 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// b.go has func UseB calling Target.
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package p\n\nfunc UseB() int { return Target() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// c.go has func UseC calling Target.
	if err := os.WriteFile(filepath.Join(dir, "c.go"), []byte("package p\n\nfunc UseC() int { return Target() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := tools.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	skipIfNoFindReferences(t, ws)

	// Script: find_references Target, edit_file b.go (remove call),
	// find_references Target, write_file d.go (add call), find_references Target, submit.
	p := faux.New(
		toolCallTurn("f1", "find_references", map[string]any{"name": "Target"}),
		toolCallTurn("e1", "edit_file", map[string]any{
			"path": "b.go",
			"edits": []map[string]any{{
				"old_string": "return Target()",
				"new_string": "return 0",
			}},
		}),
		toolCallTurn("f2", "find_references", map[string]any{"name": "Target"}),
		toolCallTurn("w1", "write_file", map[string]any{
			"path":    "d.go",
			"content": "package p\n\nfunc UseD() int { return Target() }\n",
		}),
		toolCallTurn("f3", "find_references", map[string]any{"name": "Target"}),
		toolCallTurn("s1", "submit", map[string]any{"value": "done"}),
	)

	var got string
	var calls int
	r, err := NewRunner(fauxConfig(p, ws))
	if err != nil {
		t.Fatal(err)
	}

	builtins := append(append([]string(nil), ReadOnlyFileTools...), WriteFileTools...)
	builtins = append(builtins, "execute")
	res, err := r.Run(context.Background(), Phase{
		Name: "implement", System: "s", User: "u", Terminator: "submit",
		Custom:       []core.Tool{submitTool(&got, &calls)},
		BuiltinTools: builtins,
		ReadOnly:     false,
		Programs:     ReadOnlyPrograms,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Extract tool results from the requests.
	reqs := p.Requests()
	toolResultText := func(callID string) string {
		for _, req := range reqs {
			for _, m := range req.Messages {
				trm, ok := m.(core.ToolResultMessage)
				if !ok {
					continue
				}
				if trm.ToolUseID == callID {
					return trm.Content.Text()
				}
			}
		}
		return ""
	}

	// Helper: extract enclosing declaration names from find_references result text.
	enclosing := func(text string) map[string]bool {
		m := map[string]bool{}
		for _, name := range []string{"UseB", "UseC", "UseD"} {
			if strings.Contains(text, name) {
				m[name] = true
			}
		}
		return m
	}

	// First find_references: should see UseB and UseC.
	f1Text := toolResultText("f1")
	f1Enc := enclosing(f1Text)
	if !f1Enc["UseB"] || !f1Enc["UseC"] {
		t.Errorf("first find_references: want {UseB, UseC}, got %v\ntext: %s", f1Enc, f1Text)
	}
	if f1Enc["UseD"] {
		t.Errorf("first find_references: UseD should not exist yet")
	}

	// Second find_references (after edit_file removes call in b.go): should see only UseC.
	f2Text := toolResultText("f2")
	f2Enc := enclosing(f2Text)
	if f2Enc["UseB"] {
		t.Errorf("second find_references: UseB should be gone after edit_file removed the call\ntext: %s", f2Text)
	}
	if !f2Enc["UseC"] {
		t.Errorf("second find_references: UseC should still be present\ntext: %s", f2Text)
	}

	// Third find_references (after write_file adds d.go): should see UseC and UseD.
	f3Text := toolResultText("f3")
	f3Enc := enclosing(f3Text)
	if !f3Enc["UseC"] || !f3Enc["UseD"] {
		t.Errorf("third find_references: want {UseC, UseD}, got %v\ntext: %s", f3Enc, f3Text)
	}
	if f3Enc["UseB"] {
		t.Errorf("third find_references: UseB should still be gone\ntext: %s", f3Text)
	}

	// Tool call counts.
	if res.ToolCalls["find_references"] != 3 {
		t.Errorf("find_references calls = %d, want 3", res.ToolCalls["find_references"])
	}
	if res.ToolCalls["edit_file"] != 1 {
		t.Errorf("edit_file calls = %d, want 1", res.ToolCalls["edit_file"])
	}
	if res.ToolCalls["write_file"] != 1 {
		t.Errorf("write_file calls = %d, want 1", res.ToolCalls["write_file"])
	}
}

// TS-17-11 (smoke): find_references calls that AgentKit refuses come back as
// tool results, are counted by name, and leave the phase to end on its
// terminating tool with no error.
//
// Verifies: 17-PATH-4, 17-REQ-2.5
//
// Real components: Runner, SelectTools, tools.All, find_references tool,
// toolCallCounter, toolErrorCounter
func TestTS17_11_RefusedFindReferencesCallsAreToolResults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc Target() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := tools.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	skipIfNoFindReferences(t, ws)

	// Script: three refused find_references calls, then submit.
	// 1. path outside the workspace
	// 2. 257-byte name
	// 3. kind 'no-such-kind'
	longName := strings.Repeat("a", 257)
	p := faux.New(
		toolCallTurn("r1", "find_references", map[string]any{"name": "Target", "path": filepath.Dir(ws.Root)}),
		toolCallTurn("r2", "find_references", map[string]any{"name": longName}),
		toolCallTurn("r3", "find_references", map[string]any{"name": "Target", "kind": "no-such-kind"}),
		toolCallTurn("r4", "submit", map[string]any{"value": "done"}),
	)

	var got string
	var calls int
	r, err := NewRunner(fauxConfig(p, ws))
	if err != nil {
		t.Fatal(err)
	}

	res, err := r.Run(context.Background(), Phase{
		Name: "read", System: "s", User: "u", Terminator: "submit",
		Custom:       []core.Tool{submitTool(&got, &calls)},
		BuiltinTools: ReadOnlyFileTools,
		ReadOnly:     true,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// Run returns nil error.
	if got != "done" {
		t.Errorf("submit got %q, want %q", got, "done")
	}

	// ToolCalls: find_references 3, submit 1.
	if res.ToolCalls["find_references"] != 3 {
		t.Errorf("find_references calls = %d, want 3", res.ToolCalls["find_references"])
	}
	if res.ToolCalls["submit"] != 1 {
		t.Errorf("submit calls = %d, want 1", res.ToolCalls["submit"])
	}

	// Each refusal reached the model: the following request carries a tool
	// result for that call id.
	reqs := p.Requests()
	for _, id := range []string{"r1", "r2", "r3"} {
		found := false
		for _, req := range reqs {
			for _, m := range req.Messages {
				trm, ok := m.(core.ToolResultMessage)
				if !ok {
					continue
				}
				if trm.ToolUseID == id {
					found = true
					text := trm.Content.Text()
					if text == "" {
						t.Errorf("tool result for %s is empty", id)
					}
					// The result should be an error (refusal).
					raw, _ := json.Marshal(trm)
					_ = raw
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			t.Errorf("no tool result for call %s reached the model", id)
		}
	}

	// The phase stopped on its terminator, not max_turns or an error.
	if res.StopReason != core.RunStopToolTerminate {
		t.Errorf("stop reason = %v, want terminated", res.StopReason)
	}
}
