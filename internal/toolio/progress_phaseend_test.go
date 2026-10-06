package toolio

import (
	"encoding/json"
	"testing"
)

// TS-13-11 (unit, Progress side): Progress.PhaseEnd emits a phase_end event
// carrying tool_calls.
func TestTS_13_11_ProgressPhaseEndEmitsToolCalls(t *testing.T) {
	p, _, events := sinkProgress(true, false, true)
	tc := map[string]int{"read_file": 2}
	p.PhaseEnd("prd", "end_turn", 5, 0.50, 3000, tc)
	lines := decodeLines(t, events.String())
	if len(lines) != 1 {
		t.Fatalf("expected 1 event, got %d", len(lines))
	}
	ev := lines[0]
	if ev["type"] != "phase_end" {
		t.Fatalf("type = %v, want phase_end", ev["type"])
	}
	raw, err := json.Marshal(ev["tool_calls"])
	if err != nil {
		t.Fatalf("marshal tool_calls: %v", err)
	}
	var got map[string]int
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal tool_calls: %v", err)
	}
	if got["read_file"] != 2 {
		t.Errorf("tool_calls[read_file] = %d, want 2", got["read_file"])
	}
}
