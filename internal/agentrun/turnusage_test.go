package agentrun

import (
	"context"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
)

// Issue #194 (1): a turn reports the tokens read from and written to the
// prompt cache, which the input count excludes.
func TestATurnReportsItsCacheTokens(t *testing.T) {
	turn := toolCallTurn("c1", "submit", map[string]any{"value": "done"})
	turn.Usage = core.Usage{InputTokens: 3, OutputTokens: 249, CacheReadTokens: 98000, CacheWriteTokens: 1200,
		CostUSD: 0.11}
	obs := &recObserver{}
	cfg := fauxConfig(faux.New(turn), newWorkspace(t))
	cfg.Observer = obs
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	var calls int
	if _, err := r.Run(context.Background(), Phase{Name: "phase", User: "u", Terminator: "submit",
		Custom: []core.Tool{submitTool(&got, &calls)}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(obs.turns) != 1 {
		t.Fatalf("turns = %+v", obs.turns)
	}
	u := obs.turns[0].usage
	if u.Input != 3 || u.Output != 249 || u.CacheRead != 98000 || u.CacheWrite != 1200 || u.CostUSD != 0.11 {
		t.Errorf("turn usage = %+v", u)
	}
}

// Issue #194 (4): a shell command that ran and exited non-zero is a tool call
// that worked: ok, its exit code, and no error. The output is not an error.
func TestAShellCommandThatExitedNonZeroIsOK(t *testing.T) {
	obs := &recObserver{}
	r := &Runner{cfg: Config{Observer: obs}}
	var turn int
	var tb textBuffer
	pending := make(map[string]core.ToolUseBlock)
	r.trace("ph", &turn, &toolErrorCounter{}, &blockCounter{}, &tb, pending, &toolCallCounter{}, core.ToolCallEndEvent{
		Block: core.ToolUseBlock{ID: "c1", Name: "execute", Input: []byte(`{"command":"ls x | grep -i mut"}`)},
	})
	r.trace("ph", &turn, &toolErrorCounter{}, &blockCounter{}, &tb, pending, &toolCallCounter{}, core.ToolResultEvent{
		Message: core.ToolResultMessage{ToolUseID: "c1", ToolName: "execute", IsError: true,
			Content: core.Content{core.TextBlock{Text: " M codeimpl/pipeline_test.go\n[exit 1]"}}},
	})
	info := obs.toolCalls[0].info
	if !info.OK || info.Error != "" || info.ExitCode == nil || *info.ExitCode != 1 {
		t.Errorf("tool call = ok %v, error %q, exit %v", info.OK, info.Error, info.ExitCode)
	}
}
