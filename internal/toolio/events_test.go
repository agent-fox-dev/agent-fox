package toolio

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"slices"
	"strings"
	"testing"
	"time"
)

// documentedFields is the per-type field table from the spec (07-REQ-2), not
// including the shared ts/tool/type envelope.
var documentedFields = map[string][]string{
	"run_start":   {"input_kind", "model", "schema_version"},
	"step":        {"stage", "message"},
	"phase_start": {"phase", "task", "max_turns", "budget_usd"},
	"turn": {"phase", "turn", "cost_usd", "input_tokens", "output_tokens", "cache_read_tokens",
		"cache_write_tokens", "context_tokens"},
	"tool_call": {"phase", "name", "blocked", "arguments", "ok", "exit_code", "error"},
	"check":     {"command", "ok", "exit_code", "duration_ms"},
	"phase_end": {"phase", "stop_reason", "turns", "cost_usd", "duration_ms", "tool_calls"},
	"warning":   {"code", "severity", "stage", "message"},
	"heartbeat": {"stage", "elapsed_ms", "cost_usd"},
	"run_end":   {"status", "exit_code", "report_file"},
	"text":      {"phase", "turn", "text"},
}

// everyEvent returns one event of every type, with the optional phase_start
// task present.
func everyEvent() []event {
	exitOne := 1
	return []event{
		newRunStartEvent("text", EventModelInfo{Spec: "s", ID: "i", Vendor: "v"}),
		newStepEvent("analyse", "working"),
		newPhaseStartEvent("implement", "3", 40, 5),
		newTurnEvent("implement", 1, 0.25, 100, 50, 0, 0),
		newToolCallEvent("implement", "bash", true, json.RawMessage(`{"cmd":"ls"}`), false, &exitOne, "refused"),
		newCheckEvent("go test ./...", false, 1, 1200),
		newPhaseEndEvent("implement", "end_turn", 4, 1.5, 9000, map[string]int{"bash": 1}),
		newWarningEvent(Warning{Code: WarnInputTruncated, Severity: "warning", Stage: "input", Message: "m"}),
		newHeartbeatEvent("implement", 15000, 1.5),
		newRunEndEvent("done", 0, ""),
		newTextEvent("implement", 1, "hello"),
	}
}

func decodeLines(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("line is not one JSON object (%v): %q", err, line)
		}
		out = append(out, obj)
	}
	return out
}

// TS-07-7: every event carries ts (RFC 3339 UTC), tool and type.
func TestTS07_7_EventCarriesTsToolAndType(t *testing.T) {
	var buf bytes.Buffer
	s := newEventsSink("impl", &buf)
	s.Emit(newStepEvent("analyse", "working"))

	objs := decodeLines(t, buf.String())
	if len(objs) != 1 {
		t.Fatalf("got %d events, want 1", len(objs))
	}
	obj := objs[0]
	ts, _ := obj["ts"].(string)
	parsed, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t.Fatalf("ts %q is not RFC 3339: %v", ts, err)
	}
	if !strings.HasSuffix(ts, "Z") || parsed.Location() != time.UTC {
		t.Errorf("ts %q is not UTC", ts)
	}
	if obj["tool"] != "impl" || obj["type"] != "step" {
		t.Errorf("tool/type = %v/%v, want impl/step", obj["tool"], obj["type"])
	}
	if obj["stage"] != "analyse" || obj["message"] != "working" {
		t.Errorf("step fields = %v", obj)
	}
}

// TS-07-8: every emitted type is drawn from the closed set.
func TestTS07_8_EveryTypeIsInTheClosedSet(t *testing.T) {
	closed := map[string]bool{}
	for _, ty := range EventTypes {
		closed[string(ty)] = true
	}
	if len(closed) != 11 {
		t.Fatalf("closed set has %d types, want 11", len(closed))
	}

	var buf bytes.Buffer
	s := newEventsSink("fix", &buf)
	for _, e := range everyEvent() {
		s.Emit(e)
	}
	objs := decodeLines(t, buf.String())
	if len(objs) != 11 {
		t.Fatalf("got %d events, want 11", len(objs))
	}
	seen := map[string]bool{}
	for _, obj := range objs {
		ty, _ := obj["type"].(string)
		if !closed[ty] {
			t.Errorf("type %q is outside the closed set", ty)
		}
		seen[ty] = true
	}
	if len(seen) != 11 {
		t.Errorf("only %d distinct types exercised: %v", len(seen), seen)
	}
}

// TS-07-9: an event carries exactly the fields documented for its own type —
// none from another type, and none missing (phase_start's task is the one
// optional field).
func TestTS07_9_EventCarriesOnlyItsOwnFields(t *testing.T) {
	var buf bytes.Buffer
	s := newEventsSink("spec", &buf)
	events := everyEvent()
	// A phase_start without a task must omit the field, and with one carry it.
	events = append(events, newPhaseStartEvent("analyse", "", 10, 2))
	rng := rand.New(rand.NewSource(7))
	// Zero values must still be present: a false/0 field is not omitted.
	events = append(events,
		newToolCallEvent("p", "n", false, json.RawMessage(`{}`), false, nil, ""),
		newTurnEvent("p", 0, 0, 0, 0, 0, 0),
		newCheckEvent("", false, 0, 0),
		newStepEvent("", string(rune('a'+rng.Intn(26)))),
	)
	for _, e := range events {
		s.Emit(e)
	}

	base := []string{"ts", "tool", "session_id", "type"}
	for i, obj := range decodeLines(t, buf.String()) {
		ty := obj["type"].(string)
		want := slices.Clone(documentedFields[ty])
		if ty == "phase_start" {
			if _, has := obj["task"]; !has {
				want = slices.DeleteFunc(want, func(f string) bool { return f == "task" })
			}
		}
		if ty == "turn" {
			// The cache and context counts are omitted when zero.
			for _, f := range []string{"cache_read_tokens", "cache_write_tokens", "context_tokens"} {
				if _, has := obj[f]; !has {
					want = slices.DeleteFunc(want, func(g string) bool { return g == f })
				}
			}
		}
		if ty == "tool_call" {
			if _, has := obj["exit_code"]; !has {
				want = slices.DeleteFunc(want, func(f string) bool { return f == "exit_code" })
			}
			if _, has := obj["error"]; !has {
				want = slices.DeleteFunc(want, func(f string) bool { return f == "error" })
			}
		}
		for k := range obj {
			if !slices.Contains(base, k) && !slices.Contains(documentedFields[ty], k) {
				t.Errorf("event %d (%s) carries undocumented field %q", i, ty, k)
			}
		}
		for _, k := range append(slices.Clone(base), want...) {
			if _, ok := obj[k]; !ok {
				t.Errorf("event %d (%s) is missing field %q", i, ty, k)
			}
		}
	}

	// The task is omitted when empty, present when set.
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if !strings.Contains(lines[2], `"task":"3"`) {
		t.Errorf("per-task phase_start lacks task: %s", lines[2])
	}
	if strings.Contains(lines[11], `"task"`) {
		t.Errorf("taskless phase_start carries task: %s", lines[11])
	}
}

// TS-07-10: with no active destination nothing is emitted; and a default run
// writes no event JSON to stderr.
func TestTS07_10_InactiveSinkEmitsNothing(t *testing.T) {
	var nilSink *eventsSink
	nilSink.Emit(newStepEvent("a", "b")) // must not panic
	if nilSink.Active() {
		t.Error("nil sink reports active")
	}

	inactive := newEventsSink("fix")
	if inactive.Active() {
		t.Error("sink with no writers reports active")
	}
	inactive.Emit(newStepEvent("a", "b"))

	var out bytes.Buffer
	withNil := newEventsSink("fix", nil, &out)
	withNil.Emit(newStepEvent("a", "b"))
	if out.Len() == 0 {
		t.Error("a nil writer should be skipped, not disable the live one")
	}

	app, _ := newApp(t, nil)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	_, _, stderr := runApp(t, app, []string{"--dir", dir, "some text"}, "")
	if strings.Contains(stderr, `"type":`) {
		t.Errorf("default run wrote event JSON to stderr:\n%s", stderr)
	}
}

// TS-07-11: each event is one line, however many lines its message has.
func TestTS07_11_EventIsOneLine(t *testing.T) {
	var buf bytes.Buffer
	s := newEventsSink("fix", &buf)
	s.Emit(newStepEvent("verify", "line1\nline2\r\nline3"))
	s.Emit(newTextEvent("implement", 1, "a\nb\n"))

	raw := buf.String()
	if strings.Count(raw, "\n") != 2 || !strings.HasSuffix(raw, "\n") {
		t.Fatalf("want exactly two newline-terminated lines, got %q", raw)
	}
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("line does not parse: %v: %q", err, line)
		}
	}
	objs := decodeLines(t, raw)
	if objs[0]["message"] != "line1\nline2\r\nline3" {
		t.Errorf("message not preserved: %q", objs[0]["message"])
	}
}

// Every writer receives each line in one Write call.
func TestEventsSinkWritesEachLineOnceToEveryWriter(t *testing.T) {
	var a, b bytes.Buffer
	s := newEventsSink("impl", &a, &b)
	s.Emit(newRunEndEvent("failed", 1, ""))
	if a.String() != b.String() || a.Len() == 0 {
		t.Errorf("writers differ: %q vs %q", a.String(), b.String())
	}
}
