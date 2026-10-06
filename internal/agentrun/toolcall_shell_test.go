package agentrun

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/tools"
)

// shellToolCall runs command through AgentKit's real execute tool and feeds
// the result it produces, as the model would be shown it, through trace. The
// shapes under test are the SDK's own, not ones written out by hand: for a
// shell call the result is the command's output followed by a status trailer,
// not the JSON envelope.
func shellToolCall(t *testing.T, command string) ToolCallInfo {
	t.Helper()
	ws := newWorkspace(t)
	all, err := tools.All(tools.Options{Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	var execute core.Tool
	for _, tl := range all {
		if tl.Name == "execute" {
			execute = tl
		}
	}
	if execute.Execute == nil {
		t.Fatal("tools.All has no execute tool")
	}
	args, _ := json.Marshal(map[string]any{"command": command})
	res := execute.Execute(context.Background(), args)

	obs := &recObserver{verbose: false}
	r := &Runner{cfg: Config{Observer: obs}}
	var turn int
	var tb textBuffer
	pending := make(map[string]core.ToolUseBlock)
	r.trace("ph", &turn, &toolErrorCounter{}, &blockCounter{}, &tb, pending, &toolCallCounter{}, core.ToolCallEndEvent{
		Block: core.ToolUseBlock{ID: "c1", Name: "execute", Input: args},
	})
	r.trace("ph", &turn, &toolErrorCounter{}, &blockCounter{}, &tb, pending, &toolCallCounter{}, core.ToolResultEvent{
		Message: core.ToolResultMessage{ToolUseID: "c1", ToolName: "execute", IsError: !res.OK,
			Content: core.Content{core.TextBlock{Text: res.LLMText()}}},
	})
	if len(obs.toolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(obs.toolCalls))
	}
	return obs.toolCalls[0].info
}

// 12-REQ-8.5: a tool_call event for a shell call carries exit_code whenever the
// exit status can be read from the result, a failing command included.
func TestExitCodeFromTheRealShellToolsResults(t *testing.T) {
	cases := []struct {
		command string
		want    *int
	}{
		{"true", intp(0)},
		{"echo hi", intp(0)},
		{"exit 3", intp(3)},
		{"false", intp(1)},
		{"echo before; exit 2", intp(2)},
		// Killed by a signal: there is no exit code to read, and none is guessed.
		{"kill -9 $$", nil},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			info := shellToolCall(t, tc.command)
			switch {
			case tc.want == nil && info.ExitCode != nil:
				t.Errorf("exit_code = %d, want absent", *info.ExitCode)
			case tc.want != nil && info.ExitCode == nil:
				t.Errorf("exit_code absent, want %d", *tc.want)
			case tc.want != nil && *info.ExitCode != *tc.want:
				t.Errorf("exit_code = %d, want %d", *info.ExitCode, *tc.want)
			}
			// A command that ran to an exit status is ok, whatever the
			// status; one killed by a signal did not
			// (docs/errata/tool_call_exit_status.md).
			if wantOK := tc.want != nil; info.OK != wantOK {
				t.Errorf("ok = %v, want %v", info.OK, wantOK)
			}
			if info.OK && info.Error != "" {
				t.Errorf("error = %q on an ok call", info.Error)
			}
		})
	}
}

// 12-REQ-8.6: a refused call's error is the guard's message, not the SDK's
// whole JSON envelope around it.
func TestARefusedCallsErrorIsTheGuardsMessage(t *testing.T) {
	const reason = "git push is not allowed in this phase"
	obs := &recObserver{verbose: false}
	r := &Runner{cfg: Config{Observer: obs}}
	var turn int
	var tb textBuffer
	blocks := &blockCounter{}
	pending := make(map[string]core.ToolUseBlock)

	blocks.inc("execute")
	r.trace("ph", &turn, &toolErrorCounter{}, blocks, &tb, pending, &toolCallCounter{}, core.ToolCallEndEvent{
		Block: core.ToolUseBlock{ID: "c1", Name: "execute", Input: json.RawMessage(`{"command":"git push"}`)},
	})
	r.trace("ph", &turn, &toolErrorCounter{}, blocks, &tb, pending, &toolCallCounter{}, core.ToolExecutionEndEvent{Name: "execute", IsError: true})
	r.trace("ph", &turn, &toolErrorCounter{}, blocks, &tb, pending, &toolCallCounter{}, core.ToolResultEvent{
		Message: core.ToolResultMessage{ToolUseID: "c1", ToolName: "execute", IsError: true,
			Content: core.Content{core.TextBlock{Text: core.ErrResult(core.BlockErrorCode, reason).LLMText()}}},
	})
	if len(obs.toolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(obs.toolCalls))
	}
	if got := obs.toolCalls[0].info; !got.Blocked || got.Error != reason {
		t.Errorf("blocked = %v, error = %q, want blocked with %q", got.Blocked, got.Error, reason)
	}
}

func intp(n int) *int { return &n }
