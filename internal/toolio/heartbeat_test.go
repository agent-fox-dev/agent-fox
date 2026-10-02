package toolio

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock is an injectable clock and ticker source. Advance moves time one
// interval at a time and delivers a tick for each, so the sink's own ticker
// goroutine does the checking, with no real sleeping.
type fakeClock struct {
	mu    sync.Mutex
	t     time.Time
	ticks chan time.Time
	every time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC), ticks: make(chan time.Time)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) newTicker(d time.Duration) (<-chan time.Time, func()) {
	c.mu.Lock()
	c.every = d
	c.mu.Unlock()
	return c.ticks, func() {}
}

// deliver hands the ticker goroutine one tick; it reports false when nobody
// is listening any more (the ticker has stopped).
func (c *fakeClock) deliver() bool {
	select {
	case c.ticks <- c.Now():
		return true
	case <-time.After(100 * time.Millisecond):
		return false
	}
}

// Advance moves the clock forward by d, one tick interval at a time, then
// sends a final tick as a barrier: the unbuffered channel only accepts it once
// the goroutine has finished handling the previous one.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	step := c.every
	c.mu.Unlock()
	if step <= 0 {
		step = time.Second
	}
	for elapsed := time.Duration(0); elapsed < d; elapsed += step {
		c.mu.Lock()
		c.t = c.t.Add(step)
		c.mu.Unlock()
		if !c.deliver() {
			return
		}
	}
	c.deliver()
}

// heartbeatSink builds an active sink on a fake clock writing to the returned
// buffer, with its heartbeat started and reading cost from cost.
func heartbeatSink(t *testing.T, cost func() float64) (*eventsSink, *fakeClock, *syncBuffer) {
	t.Helper()
	clock := newFakeClock()
	buf := &syncBuffer{}
	s := newEventsSink("fix", buf)
	s.now = clock.Now
	s.newTicker = clock.newTicker
	s.StartHeartbeat(cost)
	t.Cleanup(s.StopHeartbeat)
	return s, clock, buf
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func eventsOfType(t *testing.T, buf *syncBuffer, typ string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, ev := range decodeLines(t, buf.String()) {
		if ev["type"] == typ {
			out = append(out, ev)
		}
	}
	return out
}

// TS-07-37 (unit): one ticker runs for the run's lifetime once a sink is
// active, checking every second.
func TestTS07_37_OneHeartbeatTickerChecksEverySecond(t *testing.T) {
	s, _, _ := heartbeatSink(t, func() float64 { return 0 })
	if got := s.activeTickerCount(); got != 1 {
		t.Fatalf("activeTickerCount = %d, want 1", got)
	}
	if got := s.tickerInterval(); got != time.Second {
		t.Errorf("tickerInterval = %v, want 1s", got)
	}
	// Starting again does not add a second ticker.
	s.StartHeartbeat(func() float64 { return 0 })
	if got := s.activeTickerCount(); got != 1 {
		t.Errorf("after a second Start, activeTickerCount = %d, want 1", got)
	}

	// An inactive sink never starts one.
	inactive := newEventsSink("fix")
	inactive.StartHeartbeat(func() float64 { return 0 })
	if got := inactive.activeTickerCount(); got != 0 {
		t.Errorf("inactive sink runs %d tickers, want 0", got)
	}
	var nilSink *eventsSink
	nilSink.StartHeartbeat(nil)
	nilSink.StopHeartbeat()

	// With Main, --emit-events starts one; and it ends with the run.
	app, _ := newApp(t, nil)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	var ticking int
	app.Exec = func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		ticking = d.Run.eventSink().activeTickerCount()
		return ExitOK, map[string]string{"stage": "done"}, nil
	}
	app.Main(context.Background(), []string{"--dir", t.TempDir(), "--emit-events", "x"},
		strings.NewReader(""), &stdout, &stderr)
	if ticking != 1 {
		t.Errorf("with --emit-events the run had %d tickers, want 1", ticking)
	}
	ticking = -1
	stdout.Reset()
	stderr.Reset()
	app.Main(context.Background(), []string{"--dir", t.TempDir(), "x"}, strings.NewReader(""), &stdout, &stderr)
	if ticking != 0 {
		t.Errorf("with neither flag the run had %d tickers, want 0", ticking)
	}
}

// TS-07-38 (unit): fifteen seconds of silence produce a heartbeat with the
// last stage, elapsed_ms and the run's accumulated cost.
func TestTS07_38_HeartbeatAfterFifteenSecondsOfSilence(t *testing.T) {
	run := NewRun("fix", "v1")
	run.AddPhase(PhaseInfo{Name: "p", CostUSD: 0.42})
	s, clock, buf := heartbeatSink(t, run.CostUSD)

	s.Emit(newStepEvent("verify", "checking"))
	clock.Advance(14 * time.Second)
	if n := len(eventsOfType(t, buf, "heartbeat")); n != 0 {
		t.Fatalf("heartbeat after 14s of silence: %d", n)
	}
	clock.Advance(time.Second)

	hbs := eventsOfType(t, buf, "heartbeat")
	if len(hbs) != 1 {
		t.Fatalf("got %d heartbeats after 15s, want 1:\n%s", len(hbs), buf.String())
	}
	hb := hbs[0]
	if hb["stage"] != "verify" || hb["cost_usd"] != 0.42 {
		t.Errorf("heartbeat = %v, want stage verify, cost 0.42", hb)
	}
	if ms, _ := hb["elapsed_ms"].(float64); ms != 15000 {
		t.Errorf("elapsed_ms = %v, want 15000", hb["elapsed_ms"])
	}
	if hb["tool"] != "fix" {
		t.Errorf("tool = %v", hb["tool"])
	}

	// The heartbeat is the reset for the next one: another 15s, another beat,
	// and none in between.
	clock.Advance(14 * time.Second)
	if n := len(eventsOfType(t, buf, "heartbeat")); n != 1 {
		t.Fatalf("a second heartbeat arrived early: %d", n)
	}
	clock.Advance(time.Second)
	if n := len(eventsOfType(t, buf, "heartbeat")); n != 2 {
		t.Fatalf("got %d heartbeats after 30s, want 2", n)
	}
}

// A phase_start names the stage the heartbeat carries, as a step does.
func TestHeartbeatCarriesPhaseStartAsStage(t *testing.T) {
	s, clock, buf := heartbeatSink(t, func() float64 { return 0 })
	s.Emit(newStepEvent("analyse", "x"))
	s.Emit(newPhaseStartEvent("implement", "2", 10, 1))
	clock.Advance(15 * time.Second)
	hbs := eventsOfType(t, buf, "heartbeat")
	if len(hbs) != 1 || hbs[0]["stage"] != "implement" {
		t.Fatalf("heartbeats = %v, want one with stage implement", hbs)
	}
}

// run_start counts as the first event: silence is measured from it.
func TestHeartbeatWindowStartsAtRunStart(t *testing.T) {
	s, clock, buf := heartbeatSink(t, func() float64 { return 0 })
	clock.Advance(10 * time.Second)
	s.Emit(newRunStartEvent("text", EventModelInfo{}))
	clock.Advance(14 * time.Second)
	if n := len(eventsOfType(t, buf, "heartbeat")); n != 0 {
		t.Fatalf("heartbeat %d within 15s of run_start", n)
	}
	clock.Advance(time.Second)
	if n := len(eventsOfType(t, buf, "heartbeat")); n != 1 {
		t.Fatalf("got %d heartbeats 15s after run_start, want 1", n)
	}
}

// TS-07-39 (unit): no heartbeat between two events less than 15s apart, and
// the window resets from the second event.
func TestTS07_39_NoHeartbeatBetweenCloseEvents(t *testing.T) {
	s, clock, buf := heartbeatSink(t, func() float64 { return 0 })
	p := NewProgress(&bytes.Buffer{}, "fix", false, true)
	p.SetEvents(s)

	p.Step("a", "first")
	clock.Advance(10 * time.Second)
	p.Step("b", "second")
	if n := len(eventsOfType(t, buf, "heartbeat")); n != 0 {
		t.Fatalf("heartbeat between two steps 10s apart: %d", n)
	}

	// 10s after the second step is 20s after the first: the window was reset
	// from the second, so still nothing.
	clock.Advance(10 * time.Second)
	if n := len(eventsOfType(t, buf, "heartbeat")); n != 0 {
		t.Fatalf("window was not reset from the second step: %d heartbeats", n)
	}
	clock.Advance(5 * time.Second)
	if n := len(eventsOfType(t, buf, "heartbeat")); n != 1 {
		t.Fatalf("got %d heartbeats 15s after the second step, want 1", n)
	}
}

// TS-07-40 (unit): run_end stops the ticker; nothing follows it.
func TestTS07_40_RunEndStopsTheHeartbeat(t *testing.T) {
	s, clock, buf := heartbeatSink(t, func() float64 { return 0 })
	s.Emit(newStepEvent("push", "x"))
	s.Emit(newRunEndEvent("done", 0))
	before := buf.String()

	clock.Advance(15 * time.Second)
	clock.Advance(15 * time.Second)

	if got := s.activeTickerCount(); got != 0 {
		t.Errorf("activeTickerCount after run_end = %d, want 0", got)
	}
	if after := buf.String(); after != before {
		t.Errorf("events after run_end:\n%s", strings.TrimPrefix(after, before))
	}
	evs := decodeLines(t, buf.String())
	if last := evs[len(evs)-1]; last["type"] != "run_end" {
		t.Errorf("last event is %v, want run_end", last["type"])
	}
	// A start after run_end does not revive it.
	s.StartHeartbeat(func() float64 { return 0 })
	if got := s.activeTickerCount(); got != 0 {
		t.Errorf("ticker restarted after run_end: %d", got)
	}
}

// TS-07-41 (unit): the heartbeat's cost is the very total Run exposes for
// usage.phases[].
func TestTS07_41_HeartbeatCostIsTheRunsTotal(t *testing.T) {
	run := NewRun("fix", "v1")
	run.AddPhase(PhaseInfo{Name: "a", CostUSD: 1.0})
	run.AddPhase(PhaseInfo{Name: "b", CostUSD: 1.75})
	s, clock, buf := heartbeatSink(t, run.CostUSD)

	clock.Advance(15 * time.Second)
	hbs := eventsOfType(t, buf, "heartbeat")
	if len(hbs) != 1 {
		t.Fatalf("got %d heartbeats, want 1", len(hbs))
	}
	env := run.Envelope(ExitOK, nil, nil)
	if hbs[0]["cost_usd"] != env.Usage.CostUSD || env.Usage.CostUSD != 2.75 {
		t.Errorf("heartbeat cost %v, envelope usage cost %v, want both 2.75", hbs[0]["cost_usd"], env.Usage.CostUSD)
	}

	// Spend recorded later is seen by the next heartbeat: it is read live.
	run.AddPhase(PhaseInfo{Name: "c", CostUSD: 0.25})
	clock.Advance(15 * time.Second)
	hbs = eventsOfType(t, buf, "heartbeat")
	if len(hbs) != 2 || hbs[1]["cost_usd"] != 3.0 {
		t.Errorf("second heartbeat = %v, want cost 3.0", hbs)
	}
	_ = s
}
