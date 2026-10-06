package toolio

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/checks"
)

// Interface satisfaction is locked in at compile time (07-REQ-5.1).
var _ agentrun.Observer = (*Progress)(nil)

// sinkProgress returns a Progress writing human text to the first buffer and,
// when active, a JSONL sink writing to the second.
func sinkProgress(verbose, showText, active bool) (*Progress, *bytes.Buffer, *bytes.Buffer) {
	human, events := &bytes.Buffer{}, &bytes.Buffer{}
	p := NewProgress(human, "t", verbose, false)
	if active {
		p.SetEvents(newEventsSink("t", events))
	} else {
		p.SetEvents(newEventsSink("t"))
	}
	p.SetShowText(showText)
	return p, human, events
}

func lastEvent(t *testing.T, events *bytes.Buffer) map[string]any {
	t.Helper()
	all := decodeLines(t, events.String())
	if len(all) == 0 {
		t.Fatal("no event was emitted")
	}
	return all[len(all)-1]
}

// TS-07-17 (unit): Step emits a step event carrying stage and the formatted
// message when a JSONL sink is active.
func TestTS07_17_StepEmitsStepEvent(t *testing.T) {
	p, _, events := sinkProgress(false, false, true)
	p.Step("verify", "running %s", "tests")
	ev := lastEvent(t, events)
	if ev["type"] != "step" || ev["stage"] != "verify" || ev["message"] != "running tests" {
		t.Fatalf("event = %v", ev)
	}
}

// TS-07-18 (unit): Step's human line is exactly "[tool] message", stage never
// shown, with or without a sink.
func TestTS07_18_StepHumanLineUnchanged(t *testing.T) {
	for _, active := range []bool{true, false} {
		p, human, _ := sinkProgress(false, false, active)
		p.Step("verify", "running tests")
		if got := human.String(); got != "[t] running tests\n" {
			t.Errorf("sink active=%v: human line = %q", active, got)
		}
		if strings.Contains(human.String(), "verify") {
			t.Errorf("sink active=%v: stage leaked into the human line", active)
		}
	}
}

// Under --emit-events stderr carries the stream, so Step and Detail write no
// human line beside it, while the step event is still emitted.
func TestProgressEventsOnStderrSuppressesHumanLines(t *testing.T) {
	p, human, events := sinkProgress(true, false, true)
	p.SetEmitEvents(true)
	p.Step("verify", "running tests")
	p.Detail("tracing")
	if human.Len() != 0 {
		t.Fatalf("human text written under jsonl: %q", human.String())
	}
	if ev := lastEvent(t, events); ev["type"] != "step" || ev["message"] != "running tests" {
		t.Fatalf("event = %v", ev)
	}
}

// TS-07-19 (unit): Begin prints nothing and returns a no-op end function
// under --emit-events.
func TestTS07_19_BeginSilentUnderEmitEvents(t *testing.T) {
	p, human, events := sinkProgress(false, false, true)
	p.SetEmitEvents(true)
	end := p.Begin("checking")
	if human.Len() != 0 {
		t.Fatalf("Begin wrote %q", human.String())
	}
	end("done")
	if human.Len() != 0 || events.Len() != 0 {
		t.Fatalf("end wrote human %q events %q", human.String(), events.String())
	}
}

// Begin prints when --emit-events is not set, even with an active sink.
func TestTS07_19_BeginPrintsWithActiveSinkNoEmitEvents(t *testing.T) {
	p, human, _ := sinkProgress(false, false, true)
	p.SetEmitEvents(false)
	end := p.Begin("checking")
	end("ok")
	if human.Len() == 0 {
		t.Fatal("Begin should print with an active sink when --emit-events is not set")
	}
}

// Begin is unchanged without a sink.
func TestTS07_19_BeginUnchangedWithoutSink(t *testing.T) {
	p, human, _ := sinkProgress(true, false, false)
	p.SetEmitEvents(false)
	end := p.Begin("checking")
	end("ok")
	if !strings.Contains(human.String(), "[t] checking") {
		t.Fatalf("Begin printed %q", human.String())
	}
}

// TS-07-20 (unit): Detail never emits an event, and still prints under
// --verbose.
func TestTS07_20_DetailNeverEmits(t *testing.T) {
	p, human, events := sinkProgress(true, false, true)
	p.Detail("tracing something")
	if events.Len() != 0 {
		t.Fatalf("Detail emitted %q", events.String())
	}
	if !strings.Contains(human.String(), "tracing something") {
		t.Fatalf("Detail printed %q", human.String())
	}
}

// TS-07-21 (unit): Raw writes prose to stderr when --show-text is set and
// --emit-events is not, and never emits an event.
func TestTS07_21_RawWritesProseWithShowTextNoEmitEvents(t *testing.T) {
	p, human, events := sinkProgress(false, true, true)
	p.SetEmitEvents(false)
	p.PhaseStart("implement", "", 10, 1)
	p.Raw("model prose")
	if human.String() != "model prose" {
		t.Fatalf("human = %q, want 'model prose'", human.String())
	}
	// Raw should never emit events.
	for _, ev := range decodeLines(t, events.String()) {
		if ev["type"] == "text" {
			t.Fatalf("Raw emitted a text event: %v", ev)
		}
	}
}

// Raw does not write prose when --emit-events is set.
func TestTS07_21_RawSilentUnderEmitEvents(t *testing.T) {
	p, human, events := sinkProgress(false, true, true)
	p.SetEmitEvents(true)
	p.Raw("model prose")
	if human.Len() != 0 {
		t.Fatalf("prose written under --emit-events: %q", human.String())
	}
	if events.Len() != 0 {
		t.Fatalf("event emitted under --emit-events: %q", events.String())
	}
}

// Without a sink, --show-text still writes the prose, as before.
func TestTS07_21_RawWritesProseWithoutSink(t *testing.T) {
	p, human, _ := sinkProgress(false, true, false)
	p.SetEmitEvents(false)
	p.Raw("model prose")
	if human.String() != "model prose" {
		t.Fatalf("Raw wrote %q", human.String())
	}
}

// TS-07-22 (unit): Raw does nothing without --show-text, with or without a
// sink.
func TestTS07_22_RawNoopWithoutShowText(t *testing.T) {
	for _, active := range []bool{true, false} {
		p, human, events := sinkProgress(false, false, active)
		p.Raw("model prose")
		if events.Len() != 0 || human.Len() != 0 {
			t.Errorf("sink active=%v: wrote human %q events %q", active, human.String(), events.String())
		}
	}
}

// TS-07-23 (unit): Check emits a check event with the exact fields of the
// checks.Result.
func TestTS07_23_CheckEmitsCheckEvent(t *testing.T) {
	p, _, events := sinkProgress(false, false, true)
	res := checks.Result{Command: "make test", OK: true, ExitCode: 0, DurationMS: 4200}
	p.Check(res)
	ev := lastEvent(t, events)
	if ev["type"] != "check" || ev["command"] != res.Command || ev["ok"] != res.OK ||
		ev["exit_code"] != float64(res.ExitCode) || ev["duration_ms"] != float64(res.DurationMS) {
		t.Fatalf("event = %v", ev)
	}
}

// TS-07-24 (unit): Check does nothing without an active sink.
func TestTS07_24_CheckNoopWithoutSink(t *testing.T) {
	p, human, events := sinkProgress(false, false, false)
	before := human.String()
	p.Check(checks.Result{Command: "make test", OK: true})
	if events.Len() != 0 || human.String() != before {
		t.Fatalf("Check wrote human %q events %q", human.String(), events.String())
	}
}

// TS-07-25 (unit): Progress is assignable to agentrun.Observer, and usable in
// agentrun.Config.
func TestTS07_25_ProgressIsAnObserver(t *testing.T) {
	var o agentrun.Observer = NewProgress(nil, "t", false, false)
	_ = agentrun.Config{Observer: o}
}

// The phase, turn and tool-call hooks emit their events.
func TestProgressObserverHooksEmit(t *testing.T) {
	p, _, events := sinkProgress(true, false, true)
	p.PhaseStart("implement", "3", 40, 5)
	p.Turn("implement", 1, 0.25, 100, 50)
	p.ToolCall(agentrun.ToolCallInfo{Phase: "implement", Name: "bash", Blocked: true, Arguments: json.RawMessage(`{}`), OK: true})
	p.PhaseEnd("implement", "end_turn", 1, 0.25, 900, nil)
	var types []string
	for _, ev := range decodeLines(t, events.String()) {
		types = append(types, ev["type"].(string))
	}
	if got := strings.Join(types, ","); got != "phase_start,turn,tool_call,phase_end" {
		t.Fatalf("types = %s", got)
	}

	// tool_call is emitted regardless of --verbose.
	q, _, qEvents := sinkProgress(false, false, true)
	q.ToolCall(agentrun.ToolCallInfo{Phase: "implement", Name: "bash", Arguments: json.RawMessage(`{}`)})
	if qEvents.Len() == 0 {
		t.Fatal("tool_call should be emitted even without --verbose")
	}
}

// --quiet silences the human line but not the event.
func TestProgressStepQuietStillEmits(t *testing.T) {
	human, events := &bytes.Buffer{}, &bytes.Buffer{}
	p := NewProgress(human, "t", false, true)
	p.SetEvents(newEventsSink("t", events))
	p.Step("verify", "x")
	if human.Len() != 0 || events.Len() == 0 {
		t.Fatalf("human %q events %q", human.String(), events.String())
	}
}
