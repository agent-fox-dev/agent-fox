package agentrun

import (
	"context"
	"encoding/json"
	"math/rand"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
)

// TS-13-1 (unit): agentrun.Result carries ToolCalls and ToolResultBytes fields
// of the correct types.
func TestTS_13_1_ResultCarriesToolCallsAndToolResultBytes(t *testing.T) {
	r := Result{
		ToolCalls:       map[string]int{"read_file": 3},
		ToolResultBytes: map[string]int64{"read_file": 4096},
	}
	if r.ToolCalls["read_file"] != 3 {
		t.Errorf("ToolCalls[read_file] = %d, want 3", r.ToolCalls["read_file"])
	}
	if r.ToolResultBytes["read_file"] != 4096 {
		t.Errorf("ToolResultBytes[read_file] = %d, want 4096", r.ToolResultBytes["read_file"])
	}
}

// TS-13-2 (unit): Runner.run counts ToolCalls and ToolResultBytes in the trace
// method on every tool call.
func TestTS_13_2_RunnerCountsToolCallsAndBytes(t *testing.T) {
	var got string
	var calls int
	p := faux.New(
		toolCallTurn("c1", "read_file", map[string]any{"path": "main.go"}),
		toolCallTurn("c2", "list_files", map[string]any{"path": "."}),
		toolCallTurn("c3", "submit", map[string]any{"value": "done"}),
	)
	obs := &recObserver{}
	cfg := fauxConfig(p, newWorkspace(t))
	cfg.Observer = obs
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Run(context.Background(), Phase{
		Name: "phase", System: "system", User: "user",
		Terminator:   "submit",
		Custom:       []core.Tool{submitTool(&got, &calls)},
		BuiltinTools: ReadOnlyFileTools,
		ReadOnly:     true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// read_file, list_files, and submit should each have 1 call
	if res.ToolCalls["read_file"] != 1 {
		t.Errorf("ToolCalls[read_file] = %d, want 1", res.ToolCalls["read_file"])
	}
	if res.ToolCalls["list_files"] != 1 {
		t.Errorf("ToolCalls[list_files] = %d, want 1", res.ToolCalls["list_files"])
	}
	if res.ToolCalls["submit"] != 1 {
		t.Errorf("ToolCalls[submit] = %d, want 1", res.ToolCalls["submit"])
	}
	// ToolResultBytes should have positive values for tools that returned content
	if res.ToolResultBytes["read_file"] <= 0 {
		t.Errorf("ToolResultBytes[read_file] = %d, want > 0", res.ToolResultBytes["read_file"])
	}
	if res.ToolResultBytes["submit"] <= 0 {
		t.Errorf("ToolResultBytes[submit] = %d, want > 0", res.ToolResultBytes["submit"])
	}
}

// TS-13-3 (unit): A blocked tool call increments ToolCalls but not
// ToolResultBytes.
func TestTS_13_3_BlockedCallIncrementsToolCallsOnly(t *testing.T) {
	var got string
	var calls int
	p := faux.New(
		toolCallTurn("c1", "execute", map[string]any{"command": "git push"}),
		toolCallTurn("c2", "submit", map[string]any{"value": "done"}),
	)
	obs := &recObserver{}
	cfg := fauxConfig(p, newWorkspace(t))
	cfg.Observer = obs
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ph := submitPhase("phase", "", &got, &calls)
	ph.BuiltinTools = []string{"execute"}
	ph.Programs = []string{"ls", "git"}
	res, err := r.Run(context.Background(), ph)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Blocked != 1 {
		t.Fatalf("Blocked = %d, want 1", res.Blocked)
	}
	if res.ToolCalls["execute"] != 1 {
		t.Errorf("ToolCalls[execute] = %d, want 1", res.ToolCalls["execute"])
	}
	// Blocked calls should NOT increment ToolResultBytes
	if res.ToolResultBytes["execute"] != 0 {
		t.Errorf("ToolResultBytes[execute] = %d, want 0 (blocked call)", res.ToolResultBytes["execute"])
	}
}

// TS-13-4 (unit): A tool call returning an error result increments both
// ToolCalls and ToolResultBytes.
func TestTS_13_4_ErrorResultIncrementsBothCounters(t *testing.T) {
	// Use the trace method directly to test error result counting.
	obs := &recObserver{}
	r := &Runner{cfg: Config{Observer: obs}}
	var turn int
	var tb textBuffer
	var toolErrs toolErrorCounter
	blocks := &blockCounter{}
	pending := make(map[string]core.ToolUseBlock)
	var tc toolCallCounter

	errorText := "file not found: /nonexistent"

	// Simulate a tool call end event
	r.trace("ph", &turn, &toolErrs, blocks, &tb, pending, &tc,
		core.ToolCallEndEvent{Block: core.ToolUseBlock{ID: "c1", Name: "read_file", Input: json.RawMessage(`{}`)}})

	// Simulate a tool result event with an error
	r.trace("ph", &turn, &toolErrs, blocks, &tb, pending, &tc,
		core.ToolResultEvent{Message: core.ToolResultMessage{
			ToolUseID: "c1", ToolName: "read_file", IsError: true,
			Content: core.Content{core.TextBlock{Text: errorText}},
		}})

	snap := tc.snapshotCalls()
	snapBytes := tc.snapshotBytes()

	if snap["read_file"] != 1 {
		t.Errorf("ToolCalls[read_file] = %d, want 1", snap["read_file"])
	}
	wantBytes := int64(len(errorText))
	if snapBytes["read_file"] != wantBytes {
		t.Errorf("ToolResultBytes[read_file] = %d, want %d", snapBytes["read_file"], wantBytes)
	}
}

// TS-13-5 (unit): A phase with zero tool calls reports nil ToolCalls and nil
// ToolResultBytes.
func TestTS_13_5_ZeroCallsYieldsNilMaps(t *testing.T) {
	var got string
	var calls int
	p := faux.New(textTurn("hello"))
	obs := &recObserver{}
	cfg := fauxConfig(p, newWorkspace(t))
	cfg.Observer = obs
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := r.Run(context.Background(), submitPhase("phase", "", &got, &calls))
	if res.ToolCalls != nil {
		t.Errorf("ToolCalls = %v, want nil", res.ToolCalls)
	}
	if res.ToolResultBytes != nil {
		t.Errorf("ToolResultBytes = %v, want nil", res.ToolResultBytes)
	}
}

// TS-13-38 (property): For any sequence of tool calls, ToolCalls counts equal
// the number of calls and ToolResultBytes sums equal the total byte length.
func TestTS_13_38_PropertyToolCallCountsAndByteSums(t *testing.T) {
	toolNames := []string{"read_file", "list_files", "find_files", "search_files"}
	rng := rand.New(rand.NewSource(42))

	for trial := 0; trial < 20; trial++ {
		numCalls := 1 + rng.Intn(10)

		// Build expected counts and byte sums
		expectedCounts := map[string]int{}
		expectedBytes := map[string]int64{}

		obs := &recObserver{}
		r := &Runner{cfg: Config{Observer: obs}}
		var turn int
		var tb textBuffer
		var toolErrs toolErrorCounter
		blocks := &blockCounter{}
		pending := make(map[string]core.ToolUseBlock)
		var tc toolCallCounter

		for i := 0; i < numCalls; i++ {
			name := toolNames[rng.Intn(len(toolNames))]
			textLen := 1 + rng.Intn(500)
			text := make([]byte, textLen)
			for j := range text {
				text[j] = 'a' + byte(rng.Intn(26))
			}
			resultText := string(text)

			id := "c" + string(rune('0'+i))
			expectedCounts[name]++
			expectedBytes[name] += int64(len(resultText))

			r.trace("ph", &turn, &toolErrs, blocks, &tb, pending, &tc,
				core.ToolCallEndEvent{Block: core.ToolUseBlock{ID: id, Name: name, Input: json.RawMessage(`{}`)}})
			r.trace("ph", &turn, &toolErrs, blocks, &tb, pending, &tc,
				core.ToolResultEvent{Message: core.ToolResultMessage{
					ToolUseID: id, ToolName: name,
					Content: core.Content{core.TextBlock{Text: resultText}},
				}})
		}

		snapCalls := tc.snapshotCalls()
		snapBytes := tc.snapshotBytes()

		for name, count := range expectedCounts {
			if snapCalls[name] != count {
				t.Errorf("trial %d: ToolCalls[%s] = %d, want %d", trial, name, snapCalls[name], count)
			}
		}
		for name, bytes := range expectedBytes {
			if snapBytes[name] != bytes {
				t.Errorf("trial %d: ToolResultBytes[%s] = %d, want %d", trial, name, snapBytes[name], bytes)
			}
		}
	}
}
