package toolio

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// EventType names one kind of progress event. The set is closed: nothing
// outside the constants below is ever written to an event stream, so a caller
// can write one parser against it.
type EventType string

// The closed set of event types.
const (
	EventRunStart   EventType = "run_start"
	EventStep       EventType = "step"
	EventPhaseStart EventType = "phase_start"
	EventTurn       EventType = "turn"
	EventToolCall   EventType = "tool_call"
	EventCheck      EventType = "check"
	EventPhaseEnd   EventType = "phase_end"
	EventWarning    EventType = "warning"
	EventHeartbeat  EventType = "heartbeat"
	EventRunEnd     EventType = "run_end"
	EventText       EventType = "text"
)

// EventTypes lists the closed set, in documentation order.
var EventTypes = []EventType{
	EventRunStart, EventStep, EventPhaseStart, EventTurn, EventToolCall,
	EventCheck, EventPhaseEnd, EventWarning, EventHeartbeat, EventRunEnd,
	EventText,
}

// eventHeader is the envelope every event shares: when, which tool, and which
// type. It is embedded first in every event struct so those three keys lead
// each line.
type eventHeader struct {
	Ts   string    `json:"ts"`
	Tool string    `json:"tool"`
	Type EventType `json:"type"`
}

func (h *eventHeader) stamp(ts, tool string) { h.Ts, h.Tool = ts, tool }

// event is implemented by every event struct through its embedded header.
type event interface {
	stamp(ts, tool string)
}

// One struct per type, carrying exactly the fields documented for it and no
// others. There is deliberately no shared struct with omitempty fields: a
// field that does not belong to a type cannot be present on it. The single
// omitempty is phase_start's task, which is documented as present only for
// impl's per-task phase.

// RunStartEvent is emitted once, after the input is classified and the model
// resolved.
type RunStartEvent struct {
	eventHeader
	InputKind string         `json:"input_kind"`
	Model     EventModelInfo `json:"model"`
}

// EventModelInfo is the (spec, id, vendor) triple Envelope.Model carries,
// without the envelope's other model fields.
type EventModelInfo struct {
	Spec   string `json:"spec"`
	ID     string `json:"id"`
	Vendor string `json:"vendor"`
}

// StepEvent mirrors a Progress.Step call.
type StepEvent struct {
	eventHeader
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

// PhaseStartEvent is emitted when a model phase begins.
type PhaseStartEvent struct {
	eventHeader
	Phase     string  `json:"phase"`
	Task      string  `json:"task,omitempty"`
	MaxTurns  int     `json:"max_turns"`
	BudgetUSD float64 `json:"budget_usd"`
}

// TurnEvent is emitted after every model turn.
type TurnEvent struct {
	eventHeader
	Phase        string  `json:"phase"`
	Turn         int     `json:"turn"`
	CostUSD      float64 `json:"cost_usd"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
}

// ToolCallEvent is emitted per model tool call, under --verbose only.
type ToolCallEvent struct {
	eventHeader
	Phase   string `json:"phase"`
	Name    string `json:"name"`
	Blocked bool   `json:"blocked"`
}

// CheckEvent is emitted after every verification command.
type CheckEvent struct {
	eventHeader
	Command    string `json:"command"`
	OK         bool   `json:"ok"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
}

// PhaseEndEvent is emitted when a model phase ends.
type PhaseEndEvent struct {
	eventHeader
	Phase      string  `json:"phase"`
	StopReason string  `json:"stop_reason"`
	Turns      int     `json:"turns"`
	CostUSD    float64 `json:"cost_usd"`
	DurationMS int64   `json:"duration_ms"`
}

// WarningEvent mirrors a recorded Warning.
type WarningEvent struct {
	eventHeader
	Code     WarnCode `json:"code"`
	Severity string   `json:"severity"`
	Stage    string   `json:"stage"`
	Message  string   `json:"message"`
}

// HeartbeatEvent is emitted when the run has been silent for a while, so a
// caller can tell a working run from a hung one.
type HeartbeatEvent struct {
	eventHeader
	Stage     string  `json:"stage"`
	ElapsedMS int64   `json:"elapsed_ms"`
	CostUSD   float64 `json:"cost_usd"`
}

// RunEndEvent is emitted once, immediately before the envelope is written.
type RunEndEvent struct {
	eventHeader
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code"`
}

// TextEvent carries a model's own prose under --show-text.
type TextEvent struct {
	eventHeader
	Phase string `json:"phase"`
	Delta string `json:"delta"`
}

// The constructors below set the type, so a call site cannot pair a struct
// with the wrong type string.

func newRunStartEvent(inputKind string, m EventModelInfo) *RunStartEvent {
	e := &RunStartEvent{InputKind: inputKind, Model: m}
	e.Type = EventRunStart
	return e
}

func newStepEvent(stage, message string) *StepEvent {
	e := &StepEvent{Stage: stage, Message: message}
	e.Type = EventStep
	return e
}

func newPhaseStartEvent(phase, task string, maxTurns int, budgetUSD float64) *PhaseStartEvent {
	e := &PhaseStartEvent{Phase: phase, Task: task, MaxTurns: maxTurns, BudgetUSD: budgetUSD}
	e.Type = EventPhaseStart
	return e
}

func newTurnEvent(phase string, turn int, costUSD float64, in, out int64) *TurnEvent {
	e := &TurnEvent{Phase: phase, Turn: turn, CostUSD: costUSD, InputTokens: in, OutputTokens: out}
	e.Type = EventTurn
	return e
}

func newToolCallEvent(phase, name string, blocked bool) *ToolCallEvent {
	e := &ToolCallEvent{Phase: phase, Name: name, Blocked: blocked}
	e.Type = EventToolCall
	return e
}

func newCheckEvent(command string, ok bool, exitCode int, durationMS int64) *CheckEvent {
	e := &CheckEvent{Command: command, OK: ok, ExitCode: exitCode, DurationMS: durationMS}
	e.Type = EventCheck
	return e
}

func newPhaseEndEvent(phase, stopReason string, turns int, costUSD float64, durationMS int64) *PhaseEndEvent {
	e := &PhaseEndEvent{Phase: phase, StopReason: stopReason, Turns: turns, CostUSD: costUSD, DurationMS: durationMS}
	e.Type = EventPhaseEnd
	return e
}

func newWarningEvent(w Warning) *WarningEvent {
	e := &WarningEvent{Code: w.Code, Severity: w.Severity, Stage: w.Stage, Message: w.Message}
	e.Type = EventWarning
	return e
}

func newHeartbeatEvent(stage string, elapsedMS int64, costUSD float64) *HeartbeatEvent {
	e := &HeartbeatEvent{Stage: stage, ElapsedMS: elapsedMS, CostUSD: costUSD}
	e.Type = EventHeartbeat
	return e
}

func newRunEndEvent(status string, exitCode int) *RunEndEvent {
	e := &RunEndEvent{Status: status, ExitCode: exitCode}
	e.Type = EventRunEnd
	return e
}

func newTextEvent(phase, delta string) *TextEvent {
	e := &TextEvent{Phase: phase, Delta: delta}
	e.Type = EventText
	return e
}

// eventsSink serializes events as JSONL onto zero or more writers. A nil sink,
// or one with no writers, is inactive: every method is then a no-op, so with
// neither --events jsonl nor --events-file given no event object is produced.
type eventsSink struct {
	mu      sync.Mutex
	tool    string
	writers []io.Writer
	now     func() time.Time

	// lastEmit is when the last event of any type was written; the heartbeat
	// window is measured from it. stage is the last stage a step or
	// phase_start event named.
	lastEmit time.Time
	stage    string

	// Heartbeat state. interval, idle and newTicker are injectable so a test
	// advances a fake clock instead of sleeping.
	interval  time.Duration
	idle      time.Duration
	newTicker func(d time.Duration) (<-chan time.Time, func())
	cost      func() float64
	started   time.Time
	ticking   bool
	stopped   bool
	stopCh    chan struct{}
}

// The heartbeat defaults: the ticker checks every second whether the stream
// has been silent for fifteen. They are variables only so a test of the whole
// shell can shorten the window (see export_test.go); nothing else assigns them.
var (
	heartbeatInterval = time.Second
	heartbeatIdle     = 15 * time.Second
)

func realTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// newEventsSink returns a sink for the named tool writing to each non-nil
// writer. With no writer the sink is inactive.
func newEventsSink(tool string, writers ...io.Writer) *eventsSink {
	s := &eventsSink{
		tool: tool, now: time.Now,
		interval: heartbeatInterval, idle: heartbeatIdle,
		newTicker: realTicker,
	}
	for _, w := range writers {
		if w != nil {
			s.writers = append(s.writers, w)
		}
	}
	return s
}

// Active reports whether the sink has anywhere to write.
func (s *eventsSink) Active() bool { return s != nil && len(s.writers) > 0 }

// Emit stamps the event with the time and tool, marshals it as one line of
// compact JSON and writes the line to every writer in a single Write call, so
// a line is never split and a killed process keeps what was already written.
// JSON escapes any newline inside a string, so the line never embeds one.
//
// Every event resets the heartbeat window from its own timestamp, and run_end
// stops the heartbeat ticker, so no heartbeat can follow it.
func (s *eventsSink) Emit(e event) {
	if !s.Active() || e == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emitLocked(e)
	if _, end := e.(*RunEndEvent); end {
		s.stopLocked()
	}
}

// emitLocked writes one event and updates the heartbeat bookkeeping. The
// caller holds s.mu.
func (s *eventsSink) emitLocked(e event) {
	now := s.now()
	e.stamp(now.UTC().Format(time.RFC3339), s.tool)
	switch ev := e.(type) {
	case *StepEvent:
		s.stage = ev.Stage
	case *PhaseStartEvent:
		s.stage = ev.Phase
	}
	s.lastEmit = now
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	line = append(line, '\n')
	for _, w := range s.writers {
		_, _ = w.Write(line)
	}
}

// StartHeartbeat runs the one ticker for the run's lifetime: every interval it
// checks whether idle has passed since the last event, and if so emits a
// heartbeat. cost reads the run's accumulated spend — Run.CostUSD, the same
// total usage.phases[] sums — and is called without the sink's lock held. The
// moment of the call is the start of the run and counts as the first event.
// It does nothing on an inactive sink, and a second call while the ticker
// runs is ignored.
func (s *eventsSink) StartHeartbeat(cost func() float64) {
	if !s.Active() {
		return
	}
	s.mu.Lock()
	if s.ticking || s.stopped {
		s.mu.Unlock()
		return
	}
	s.ticking = true
	s.cost = cost
	s.started = s.now()
	if s.lastEmit.IsZero() {
		s.lastEmit = s.started
	}
	s.stopCh = make(chan struct{})
	stop := s.stopCh
	ticks, stopTicker := s.newTicker(s.interval)
	s.mu.Unlock()

	go func() {
		defer stopTicker()
		for {
			select {
			case <-stop:
				return
			case <-ticks:
				s.checkHeartbeat()
			}
		}
	}()
}

// StopHeartbeat stops the ticker. It is safe to call more than once.
func (s *eventsSink) StopHeartbeat() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
}

func (s *eventsSink) stopLocked() {
	if s.ticking {
		s.ticking = false
		close(s.stopCh)
	}
	s.stopped = true
}

// checkHeartbeat is one tick: emit a heartbeat when the stream has been
// silent for the idle window.
func (s *eventsSink) checkHeartbeat() {
	s.mu.Lock()
	costFn := s.cost
	s.mu.Unlock()
	var cost float64
	if costFn != nil {
		cost = costFn()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ticking {
		return
	}
	now := s.now()
	if now.Sub(s.lastEmit) < s.idle {
		return
	}
	s.emitLocked(newHeartbeatEvent(s.stage, now.Sub(s.started).Milliseconds(), cost))
}

// activeTickerCount is the number of heartbeat tickers running: one between
// StartHeartbeat and run_end, none otherwise.
func (s *eventsSink) activeTickerCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ticking {
		return 1
	}
	return 0
}

// tickerInterval is how often the ticker checks for silence.
func (s *eventsSink) tickerInterval() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.interval
}
