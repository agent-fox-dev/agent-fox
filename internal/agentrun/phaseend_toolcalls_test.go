package agentrun

import (
	"context"
	"reflect"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
)

// TS-13-11 (unit): Observer.PhaseEnd signature accepts toolCalls and all
// implementations are updated.
func TestTS_13_11_ObserverPhaseEndAcceptsToolCalls(t *testing.T) {
	obs := &recObserver{}
	tc := map[string]int{"read_file": 2}
	obs.PhaseEnd("prd", "end_turn", 5, 0.50, 3000, tc)
	if len(obs.ends) != 1 {
		t.Fatalf("expected 1 end call, got %d", len(obs.ends))
	}
	if !reflect.DeepEqual(obs.ends[0].toolCalls, tc) {
		t.Errorf("endCall.toolCalls = %v, want %v", obs.ends[0].toolCalls, tc)
	}
}

// TS-13-12 (unit): Progress.PhaseEnd passes toolCalls through to the
// phase_end event and Runner.Run passes Result.ToolCalls to Observer.PhaseEnd.
func TestTS_13_12_RunnerPassesToolCallsToPhaseEnd(t *testing.T) {
	var got string
	var calls int
	p := faux.New(
		toolCallTurn("c1", "read_file", map[string]any{"path": "main.go"}),
		toolCallTurn("c2", "submit", map[string]any{"value": "done"}),
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
	if len(obs.ends) != 1 {
		t.Fatalf("expected 1 end call, got %d", len(obs.ends))
	}
	if !reflect.DeepEqual(obs.ends[0].toolCalls, res.ToolCalls) {
		t.Errorf("endCall.toolCalls = %v, want %v (from Result.ToolCalls)", obs.ends[0].toolCalls, res.ToolCalls)
	}
	// Verify the result actually has tool calls
	if res.ToolCalls["read_file"] != 1 {
		t.Errorf("Result.ToolCalls[read_file] = %d, want 1", res.ToolCalls["read_file"])
	}
}
