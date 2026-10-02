package agentrun

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
)

// TS-12-30 (integration): tool_call and text events are emitted without
// --verbose or --show-text.
func TestTS12_30_ToolCallAndTextWithoutVerboseOrShowText(t *testing.T) {
	// A Runner with ShowText false and an Observer whose Verbose() is false,
	// scripted to make two tool calls and produce prose.
	p := faux.New(
		// Turn 1: text + tool call (submit with empty value → rejected)
		textAndToolTurn("some prose", "c1", "submit", map[string]any{"value": ""}),
		// Turn 2: tool call (submit with value → accepted)
		toolCallTurn("c2", "submit", map[string]any{"value": "done"}),
	)
	obs := &recObserver{verbose: false}
	cfg := fauxConfig(p, newWorkspace(t))
	cfg.Observer = obs
	cfg.ShowText = false
	var got string
	var calls int
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), submitPhase("ph", "", &got, &calls)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Two tool_call events must reach the observer regardless of Verbose().
	if len(obs.toolCalls) != 2 {
		t.Fatalf("got %d tool calls, want 2: %+v", len(obs.toolCalls), obs.toolCalls)
	}
	// One text event for the prose in turn 1.
	if len(obs.texts) < 1 {
		t.Fatalf("got %d text events, want at least 1", len(obs.texts))
	}
}

// TS-12-43 (unit): Arguments are recorded verbatim apart from whitespace
// and {} when empty.
func TestTS12_43_ArgumentsRecordedVerbatimAndEmptyIsEmptyObject(t *testing.T) {
	obs := &recObserver{verbose: false}
	r := &Runner{cfg: Config{Observer: obs}}
	var turn int
	var tb textBuffer
	blocks := &blockCounter{}
	errs := &toolErrorCounter{}
	pending := make(map[string]core.ToolUseBlock)

	// First call: input with whitespace.
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolCallEndEvent{
		Block: core.ToolUseBlock{
			ID:    "c1",
			Name:  "read_file",
			Input: json.RawMessage(`{ "path": "a b",  "n": 1 }`),
		},
	})
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolResultEvent{
		Message: core.ToolResultMessage{ToolUseID: "c1", ToolName: "read_file"},
	})

	// Second call: empty input (the SDK normalizes nil/empty to {}).
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolCallEndEvent{
		Block: core.ToolUseBlock{
			ID:    "c2",
			Name:  "list_files",
			Input: json.RawMessage(`{}`),
		},
	})
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolResultEvent{
		Message: core.ToolResultMessage{ToolUseID: "c2", ToolName: "list_files"},
	})

	if len(obs.toolCalls) != 2 {
		t.Fatalf("got %d tool calls, want 2", len(obs.toolCalls))
	}

	// First: compacted JSON equal to the input.
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, []byte(`{ "path": "a b",  "n": 1 }`)); err != nil {
		t.Fatal(err)
	}
	if string(obs.toolCalls[0].info.Arguments) != compacted.String() {
		t.Errorf("arguments[0] = %s, want %s", obs.toolCalls[0].info.Arguments, compacted.String())
	}

	// Second: {}.
	if string(obs.toolCalls[1].info.Arguments) != "{}" {
		t.Errorf("arguments[1] = %s, want {}", obs.toolCalls[1].info.Arguments)
	}
}

// TS-12-45 (unit): ok follows the result's error flag, not the exit status,
// and is false for a guard-refused call.
func TestTS12_45_OKFollowsIsErrorNotExitStatus(t *testing.T) {
	obs := &recObserver{verbose: false}
	r := &Runner{cfg: Config{Observer: obs}}
	var turn int
	var tb textBuffer
	blocks := &blockCounter{}
	errs := &toolErrorCounter{}
	pending := make(map[string]core.ToolUseBlock)

	// Case 1: non-error read (ok=true).
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolCallEndEvent{
		Block: core.ToolUseBlock{ID: "c1", Name: "read_file", Input: json.RawMessage(`{}`)},
	})
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolResultEvent{
		Message: core.ToolResultMessage{ToolUseID: "c1", ToolName: "read_file", IsError: false},
	})

	// Case 2: error result (ok=false).
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolCallEndEvent{
		Block: core.ToolUseBlock{ID: "c2", Name: "read_file", Input: json.RawMessage(`{}`)},
	})
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolResultEvent{
		Message: core.ToolResultMessage{ToolUseID: "c2", ToolName: "read_file", IsError: true,
			Content: core.Content{core.TextBlock{Text: "not found"}}},
	})

	// Case 3: shell result with IsError false and exit 1 (ok=true).
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolCallEndEvent{
		Block: core.ToolUseBlock{ID: "c3", Name: "execute", Input: json.RawMessage(`{"command":"false"}`)},
	})
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolResultEvent{
		Message: core.ToolResultMessage{ToolUseID: "c3", ToolName: "execute", IsError: false,
			Content: core.Content{core.TextBlock{Text: `{"ok":true,"data":{"exit_code":1,"output":""}}`}}},
	})

	// Case 4: guard-refused call (ok=false, blocked=true).
	blocks.inc("execute")
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolCallEndEvent{
		Block: core.ToolUseBlock{ID: "c4", Name: "execute", Input: json.RawMessage(`{"command":"rm -rf /"}`)},
	})
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolExecutionEndEvent{Name: "execute", IsError: true})
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolResultEvent{
		Message: core.ToolResultMessage{ToolUseID: "c4", ToolName: "execute", IsError: true,
			Content: core.Content{core.TextBlock{Text: "guard refused"}}},
	})

	if len(obs.toolCalls) != 4 {
		t.Fatalf("got %d tool calls, want 4: %+v", len(obs.toolCalls), obs.toolCalls)
	}

	wantOK := []bool{true, false, true, false}
	for i, want := range wantOK {
		if obs.toolCalls[i].info.OK != want {
			t.Errorf("call %d: ok = %v, want %v", i, obs.toolCalls[i].info.OK, want)
		}
	}
	// Case 4 must be blocked.
	if !obs.toolCalls[3].info.Blocked {
		t.Error("case 4 (guard-refused) should be blocked")
	}
}

// TS-12-46 (unit): exit_code is recorded only for a shell call with a
// readable status, never guessed.
func TestTS12_46_ExitCodeOnlyForShellWithReadableStatus(t *testing.T) {
	obs := &recObserver{verbose: false}
	r := &Runner{cfg: Config{Observer: obs}}
	var turn int
	var tb textBuffer
	blocks := &blockCounter{}
	errs := &toolErrorCounter{}
	pending := make(map[string]core.ToolUseBlock)

	// Case 1: shell result with readable exit_code 3.
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolCallEndEvent{
		Block: core.ToolUseBlock{ID: "c1", Name: "execute", Input: json.RawMessage(`{"command":"exit 3"}`)},
	})
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolResultEvent{
		Message: core.ToolResultMessage{ToolUseID: "c1", ToolName: "execute", IsError: true,
			Content: core.Content{core.TextBlock{Text: `{"ok":false,"data":{"exit_code":3,"output":""},"error":"exit"}`}}},
	})

	// Case 2: shell result with no readable status (no data.exit_code).
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolCallEndEvent{
		Block: core.ToolUseBlock{ID: "c2", Name: "execute", Input: json.RawMessage(`{"command":"ls"}`)},
	})
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolResultEvent{
		Message: core.ToolResultMessage{ToolUseID: "c2", ToolName: "execute", IsError: true,
			Content: core.Content{core.TextBlock{Text: `some error text`}}},
	})

	// Case 3: refused shell call.
	blocks.inc("execute")
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolCallEndEvent{
		Block: core.ToolUseBlock{ID: "c3", Name: "execute", Input: json.RawMessage(`{"command":"rm -rf /"}`)},
	})
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolExecutionEndEvent{Name: "execute", IsError: true})
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolResultEvent{
		Message: core.ToolResultMessage{ToolUseID: "c3", ToolName: "execute", IsError: true,
			Content: core.Content{core.TextBlock{Text: "guard refused"}}},
	})

	// Case 4: non-shell tool result.
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolCallEndEvent{
		Block: core.ToolUseBlock{ID: "c4", Name: "read_file", Input: json.RawMessage(`{}`)},
	})
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolResultEvent{
		Message: core.ToolResultMessage{ToolUseID: "c4", ToolName: "read_file"},
	})

	if len(obs.toolCalls) != 4 {
		t.Fatalf("got %d tool calls, want 4: %+v", len(obs.toolCalls), obs.toolCalls)
	}

	// Case 1: exit_code is 3.
	if obs.toolCalls[0].info.ExitCode == nil || *obs.toolCalls[0].info.ExitCode != 3 {
		t.Errorf("case 1: exit_code = %v, want 3", obs.toolCalls[0].info.ExitCode)
	}
	// Cases 2, 3, 4: exit_code is absent.
	for _, i := range []int{1, 2, 3} {
		if obs.toolCalls[i].info.ExitCode != nil {
			t.Errorf("case %d: exit_code = %v, want nil", i+1, *obs.toolCalls[i].info.ExitCode)
		}
	}
}

// TS-12-47 (unit): error is present exactly when ok is false and holds the
// result text or guard message.
func TestTS12_47_ErrorPresentExactlyWhenOKFalse(t *testing.T) {
	obs := &recObserver{verbose: false}
	r := &Runner{cfg: Config{Observer: obs}}
	var turn int
	var tb textBuffer
	blocks := &blockCounter{}
	errs := &toolErrorCounter{}
	pending := make(map[string]core.ToolUseBlock)

	// Case 1: error result with text 'boom'.
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolCallEndEvent{
		Block: core.ToolUseBlock{ID: "c1", Name: "read_file", Input: json.RawMessage(`{}`)},
	})
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolResultEvent{
		Message: core.ToolResultMessage{ToolUseID: "c1", ToolName: "read_file", IsError: true,
			Content: core.Content{core.TextBlock{Text: "boom"}}},
	})

	// Case 2: refused call with guard message.
	blocks.inc("execute")
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolCallEndEvent{
		Block: core.ToolUseBlock{ID: "c2", Name: "execute", Input: json.RawMessage(`{"command":"rm -rf /"}`)},
	})
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolExecutionEndEvent{Name: "execute", IsError: true})
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolResultEvent{
		Message: core.ToolResultMessage{ToolUseID: "c2", ToolName: "execute", IsError: true,
			Content: core.Content{core.TextBlock{Text: "guard refused: dangerous"}}},
	})

	// Case 3: successful call.
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolCallEndEvent{
		Block: core.ToolUseBlock{ID: "c3", Name: "read_file", Input: json.RawMessage(`{}`)},
	})
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolResultEvent{
		Message: core.ToolResultMessage{ToolUseID: "c3", ToolName: "read_file",
			Content: core.Content{core.TextBlock{Text: "RESULT_CONTENT_HERE"}}},
	})

	if len(obs.toolCalls) != 3 {
		t.Fatalf("got %d tool calls, want 3: %+v", len(obs.toolCalls), obs.toolCalls)
	}

	// Case 1: error is 'boom'.
	if obs.toolCalls[0].info.Error != "boom" {
		t.Errorf("case 1: error = %q, want 'boom'", obs.toolCalls[0].info.Error)
	}
	// Case 2: error is the guard message.
	if obs.toolCalls[1].info.Error != "guard refused: dangerous" {
		t.Errorf("case 2: error = %q, want 'guard refused: dangerous'", obs.toolCalls[1].info.Error)
	}
	// Case 3: error is absent (empty string).
	if obs.toolCalls[2].info.Error != "" {
		t.Errorf("case 3: error = %q, want empty", obs.toolCalls[2].info.Error)
	}
}

// TS-12-48 (integration): Parallel calls to the same tool each carry their
// own arguments, matched by tool-call id, one event per call, without
// Verbose().
func TestTS12_48_ParallelCallsMatchedByID(t *testing.T) {
	obs := &recObserver{verbose: false}
	r := &Runner{cfg: Config{Observer: obs}}
	var turn int
	var tb textBuffer
	blocks := &blockCounter{}
	errs := &toolErrorCounter{}
	pending := make(map[string]core.ToolUseBlock)

	// Two ToolCallEndEvents for the tool 'read_file' with ids c1, c2 and
	// different inputs.
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolCallEndEvent{
		Block: core.ToolUseBlock{ID: "c1", Name: "read_file", Input: json.RawMessage(`{"p":1}`)},
	})
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolCallEndEvent{
		Block: core.ToolUseBlock{ID: "c2", Name: "read_file", Input: json.RawMessage(`{"p":2}`)},
	})

	// Results arrive in the order c2, c1.
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolResultEvent{
		Message: core.ToolResultMessage{ToolUseID: "c2", ToolName: "read_file"},
	})
	r.trace("ph", &turn, errs, blocks, &tb, pending, core.ToolResultEvent{
		Message: core.ToolResultMessage{ToolUseID: "c1", ToolName: "read_file"},
	})

	if len(obs.toolCalls) != 2 {
		t.Fatalf("got %d tool calls, want 2", len(obs.toolCalls))
	}

	// The c2 result event carries c2's arguments.
	if string(obs.toolCalls[0].info.Arguments) != `{"p":2}` {
		t.Errorf("call 0 arguments = %s, want {\"p\":2}", obs.toolCalls[0].info.Arguments)
	}
	// The c1 result event carries c1's arguments.
	if string(obs.toolCalls[1].info.Arguments) != `{"p":1}` {
		t.Errorf("call 1 arguments = %s, want {\"p\":1}", obs.toolCalls[1].info.Arguments)
	}
}
