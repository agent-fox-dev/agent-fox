package toolio

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TS-13-9 (unit): PhaseEndEvent carries ToolCalls with JSON tag tool_calls,omitempty.
func TestTS_13_9_PhaseEndEventToolCallsJSON(t *testing.T) {
	// When ToolCalls is populated, the JSON contains "tool_calls".
	e := PhaseEndEvent{ToolCalls: map[string]int{"read_file": 3}}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(b), `"tool_calls"`) {
		t.Errorf("JSON should contain tool_calls key: %s", b)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	tc, ok := decoded["tool_calls"].(map[string]any)
	if !ok {
		t.Fatalf("tool_calls is not a map: %v", decoded["tool_calls"])
	}
	if tc["read_file"] != float64(3) {
		t.Errorf("tool_calls[read_file] = %v, want 3", tc["read_file"])
	}

	// When ToolCalls is nil, the key is absent.
	e2 := PhaseEndEvent{}
	b2, err := json.Marshal(e2)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b2), `"tool_calls"`) {
		t.Errorf("JSON should not contain tool_calls key when nil: %s", b2)
	}
}

// TS-13-10 (unit): newPhaseEndEvent accepts a toolCalls parameter and sets it
// on the returned event.
func TestTS_13_10_NewPhaseEndEventAcceptsToolCalls(t *testing.T) {
	tc := map[string]int{"read_file": 2, "list_files": 1}
	e := newPhaseEndEvent("prd", "end_turn", 5, 0.50, 3000, tc)
	if !reflect.DeepEqual(e.ToolCalls, tc) {
		t.Errorf("ToolCalls = %v, want %v", e.ToolCalls, tc)
	}
}

// TS-13-13 (unit): PhaseEndEvent does not carry ToolResultBytes.
func TestTS_13_13_PhaseEndEventNoToolResultBytes(t *testing.T) {
	ty := reflect.TypeOf(PhaseEndEvent{})
	for i := 0; i < ty.NumField(); i++ {
		f := ty.Field(i)
		if f.Name == "ToolResultBytes" {
			t.Errorf("PhaseEndEvent should not have a ToolResultBytes field")
		}
		if strings.Contains(f.Tag.Get("json"), "tool_result_bytes") {
			t.Errorf("PhaseEndEvent should not have a tool_result_bytes JSON tag")
		}
	}
}
