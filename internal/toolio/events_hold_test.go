package toolio

import (
	"strings"
	"testing"
	"time"
)

func lineTypes(t *testing.T, buf *syncBuffer) string {
	t.Helper()
	var types []string
	for _, ev := range decodeLines(t, buf.String()) {
		types = append(types, ev["type"].(string))
	}
	return strings.Join(types, ",")
}

// HoldUntilRunStart makes run_start the first line: what is emitted before it
// is written straight after it, in order, and no heartbeat precedes it.
func TestHoldUntilRunStartWritesRunStartFirstThenWhatWasHeld(t *testing.T) {
	s, clock, buf := heartbeatSink(t, nil)
	s.HoldUntilRunStart()

	s.Emit(newWarningEvent(Warning{Code: WarnInputTruncated, Severity: "high", Stage: "input", Message: "m"}))
	s.Emit(newStepEvent("input", "reading"))
	clock.Advance(30 * time.Second)
	if got := buf.String(); got != "" {
		t.Fatalf("something was written before run_start:\n%s", got)
	}

	s.Emit(newRunStartEvent("text", EventModelInfo{Spec: "STANDARD", ID: "m", Vendor: "v"}))
	if got := lineTypes(t, buf); got != "run_start,warning,step" {
		t.Fatalf("events = %s, want run_start then what was held, in order", got)
	}

	// Once started the sink writes as it goes, and the heartbeat is running.
	s.Emit(newStepEvent("verify", "checking"))
	clock.Advance(15 * time.Second)
	if got := lineTypes(t, buf); got != "run_start,warning,step,step,heartbeat" {
		t.Fatalf("events = %s", got)
	}
}

// A run that ends before run_start (it failed before the model was resolved)
// still writes what was held, before run_end.
func TestHoldUntilRunStartFlushesBeforeRunEndWhenTheRunNeverStarts(t *testing.T) {
	s, _, buf := heartbeatSink(t, nil)
	s.HoldUntilRunStart()
	s.Emit(newWarningEvent(Warning{Code: WarnInputTruncated, Severity: "high", Stage: "input", Message: "m"}))
	s.Emit(newRunEndEvent("failed", 1, ""))
	if got := lineTypes(t, buf); got != "warning,run_end" {
		t.Fatalf("events = %s, want the held warning, then run_end", got)
	}
}

// A sink that is never told to hold writes every event as it happens.
func TestASinkWithoutAHoldWritesEveryEventImmediately(t *testing.T) {
	s, _, buf := heartbeatSink(t, nil)
	s.Emit(newStepEvent("input", "reading"))
	if got := lineTypes(t, buf); got != "step" {
		t.Fatalf("events = %s", got)
	}
}
