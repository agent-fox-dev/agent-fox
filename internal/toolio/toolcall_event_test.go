package toolio

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
)

// TS-12-42 (unit): tool_call carries phase, name, blocked, arguments, ok
// always, and exit_code/error only when set.
func TestTS12_42_ToolCallFieldPresence(t *testing.T) {
	var buf bytes.Buffer
	s := newEventsSink("t", &buf)

	// (a) An ok non-shell call.
	s.Emit(newToolCallEvent("ph", "read_file", false,
		json.RawMessage(`{"path":"a.go"}`), true, nil, ""))
	// (b) An ok shell call with exit 0.
	exitZero := 0
	s.Emit(newToolCallEvent("ph", "execute", false,
		json.RawMessage(`{"command":"ls"}`), true, &exitZero, ""))
	// (c) An error shell call with exit 1.
	exitOne := 1
	s.Emit(newToolCallEvent("ph", "execute", false,
		json.RawMessage(`{"command":"false"}`), false, &exitOne, "exit"))

	lines := decodeLines(t, buf.String())
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}

	a, b, c := lines[0], lines[1], lines[2]

	// (a): arguments and ok are present; exit_code and error are absent.
	if _, ok := a["arguments"]; !ok {
		t.Error("(a) missing arguments")
	}
	if _, ok := a["ok"]; !ok {
		t.Error("(a) missing ok")
	}
	if _, ok := a["exit_code"]; ok {
		t.Error("(a) exit_code should be absent")
	}
	if _, ok := a["error"]; ok {
		t.Error("(a) error should be absent")
	}

	// (b): exit_code is 0 (present even though zero), error is absent.
	if v, ok := b["exit_code"]; !ok {
		t.Error("(b) missing exit_code")
	} else if v != float64(0) {
		t.Errorf("(b) exit_code = %v, want 0", v)
	}
	if _, ok := b["error"]; ok {
		t.Error("(b) error should be absent")
	}

	// (c): exit_code is 1, error is present.
	if v, ok := c["exit_code"]; !ok {
		t.Error("(c) missing exit_code")
	} else if v != float64(1) {
		t.Errorf("(c) exit_code = %v, want 1", v)
	}
	if v, ok := c["error"]; !ok {
		t.Error("(c) missing error")
	} else if v != "exit" {
		t.Errorf("(c) error = %v, want 'exit'", v)
	}
}

// TS-12-44 (unit): Invalid JSON tool input is recorded as a JSON string and
// the line is kept.
func TestTS12_44_InvalidJSONInputRecordedAsString(t *testing.T) {
	var buf bytes.Buffer
	s := newEventsSink("t", &buf)

	// The arguments field is invalid JSON: a truncated object.
	raw := json.RawMessage(`{"a": `)
	s.Emit(newToolCallEvent("ph", "read_file", false, raw, true, nil, ""))

	lines := decodeLines(t, buf.String())
	if len(lines) == 0 {
		t.Fatal("no event line written — the line was dropped")
	}

	// The line must parse as JSON (the event was not dropped).
	obj := lines[len(lines)-1]

	// arguments must be a string equal to the raw input.
	argsRaw, ok := obj["arguments"]
	if !ok {
		t.Fatalf("no arguments field in line")
	}
	argsStr, ok := argsRaw.(string)
	if !ok {
		t.Fatalf("arguments is not a string: %T %v", argsRaw, argsRaw)
	}
	if argsStr != string(raw) {
		t.Errorf("arguments = %q, want %q", argsStr, string(raw))
	}
}

// TS-12-49 (unit): Observer.ToolCall takes one struct with phase, name,
// blocked, arguments, ok, exit code and error.
func TestTS12_49_ProgressToolCallTakesStructAndEmitsAllFields(t *testing.T) {
	var eventBuf bytes.Buffer
	sink := newEventsSink("t", &eventBuf)
	p := NewProgress(nil, "t", true, false)
	p.SetEvents(sink)

	exitCode := 2
	info := agentrun.ToolCallInfo{
		Phase:     "ph",
		Name:      "execute",
		Blocked:   false,
		Arguments: json.RawMessage(`{"command":"ls"}`),
		OK:        false,
		ExitCode:  &exitCode,
		Error:     "exit",
	}
	p.ToolCall(info)

	// Parse the emitted event line.
	lines := decodeLines(t, eventBuf.String())
	if len(lines) == 0 {
		t.Fatal("no event line written")
	}
	obj := lines[len(lines)-1]
	if obj["type"] != "tool_call" {
		t.Errorf("type = %v, want tool_call", obj["type"])
	}
	if obj["phase"] != "ph" {
		t.Errorf("phase = %v, want ph", obj["phase"])
	}
	if obj["name"] != "execute" {
		t.Errorf("name = %v, want execute", obj["name"])
	}
	if obj["exit_code"] != float64(2) {
		t.Errorf("exit_code = %v, want 2", obj["exit_code"])
	}
	if obj["error"] != "exit" {
		t.Errorf("error = %v, want 'exit'", obj["error"])
	}
	if obj["ok"] != false {
		t.Errorf("ok = %v, want false", obj["ok"])
	}
}
