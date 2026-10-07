package toolio

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"reflect"
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

// eventHeader is the envelope every event shares: when, which tool, the
// session and which type. It is embedded first in every event struct so
// those four keys lead each line in the order ts, tool, session_id, type.
type eventHeader struct {
	Ts        string    `json:"ts"`
	Tool      string    `json:"tool"`
	SessionID string    `json:"session_id"`
	Type      EventType `json:"type"`
}

func (h *eventHeader) stamp(ts, tool, sessionID string) {
	h.Ts, h.Tool, h.SessionID = ts, tool, sessionID
}

// event is implemented by every event struct through its embedded header.
type event interface {
	stamp(ts, tool, sessionID string)
}

// One struct per type, carrying exactly the fields documented for it and no
// others. There is deliberately no shared struct with omitempty fields: a
// field that does not belong to a type cannot be present on it. The fields
// with omitempty are: phase_start's task (present only for impl's per-task
// phase), and tool_call's exit_code and error (present only under the rules
// of 12-REQ-8.5 and 12-REQ-8.6).

// RunStartEvent is emitted once, after the input is classified and the model
// resolved.
type RunStartEvent struct {
	eventHeader
	InputKind     string         `json:"input_kind"`
	Model         EventModelInfo `json:"model"`
	SchemaVersion string         `json:"schema_version"`
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
	// CacheReadTokens and CacheWriteTokens are the prompt-cache tokens the
	// turn read and wrote: input_tokens is net of them, so with caching it is
	// a handful while the prompt is tens of thousands. ContextTokens is the
	// whole prompt the turn sent, the three together.
	CacheReadTokens  int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64 `json:"cache_write_tokens,omitempty"`
	ContextTokens    int64 `json:"context_tokens,omitempty"`
}

// ToolCallEvent is emitted per model tool call. omitempty is applied to
// exit_code and error only: arguments, ok and blocked are always present.
type ToolCallEvent struct {
	eventHeader
	Phase     string          `json:"phase"`
	Name      string          `json:"name"`
	Blocked   bool            `json:"blocked"`
	Arguments json.RawMessage `json:"arguments"`
	OK        bool            `json:"ok"`
	ExitCode  *int            `json:"exit_code,omitempty"`
	Error     string          `json:"error,omitempty"`
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
	Phase      string         `json:"phase"`
	StopReason string         `json:"stop_reason"`
	Turns      int            `json:"turns"`
	CostUSD    float64        `json:"cost_usd"`
	DurationMS int64          `json:"duration_ms"`
	ToolCalls  map[string]int `json:"tool_calls,omitempty"`
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
	Status     string `json:"status"`
	ExitCode   int    `json:"exit_code"`
	ReportFile string `json:"report_file"`
}

// TextEvent carries the accumulated prose of one model turn.
type TextEvent struct {
	eventHeader
	Phase string `json:"phase"`
	Turn  int    `json:"turn"`
	Text  string `json:"text"`
}

// The constructors below set the type, so a call site cannot pair a struct
// with the wrong type string.

func newRunStartEvent(inputKind string, m EventModelInfo) *RunStartEvent {
	e := &RunStartEvent{InputKind: inputKind, Model: m, SchemaVersion: SchemaVersion}
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

func newTurnEvent(phase string, turn int, costUSD float64, in, out, cacheRead, cacheWrite int64) *TurnEvent {
	e := &TurnEvent{Phase: phase, Turn: turn, CostUSD: costUSD, InputTokens: in, OutputTokens: out,
		CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite, ContextTokens: in + cacheRead + cacheWrite}
	e.Type = EventTurn
	return e
}

func newToolCallEvent(phase, name string, blocked bool, arguments json.RawMessage, ok bool, exitCode *int, errMsg string) *ToolCallEvent {
	args := safeArguments(arguments)
	e := &ToolCallEvent{
		Phase: phase, Name: name, Blocked: blocked,
		Arguments: args, OK: ok, ExitCode: exitCode, Error: errMsg,
	}
	e.Type = EventToolCall
	return e
}

// safeArguments compacts valid JSON arguments, returns {} for empty input,
// and encodes invalid JSON as a JSON string so the event line is never
// dropped by a marshal failure.
func safeArguments(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, raw); err != nil {
		// Invalid JSON: encode the raw text as a JSON string.
		encoded, _ := json.Marshal(string(raw))
		return json.RawMessage(encoded)
	}
	return json.RawMessage(compacted.Bytes())
}

func newCheckEvent(command string, ok bool, exitCode int, durationMS int64) *CheckEvent {
	e := &CheckEvent{Command: command, OK: ok, ExitCode: exitCode, DurationMS: durationMS}
	e.Type = EventCheck
	return e
}

func newPhaseEndEvent(phase, stopReason string, turns int, costUSD float64, durationMS int64, toolCalls map[string]int) *PhaseEndEvent {
	e := &PhaseEndEvent{Phase: phase, StopReason: stopReason, Turns: turns, CostUSD: costUSD, DurationMS: durationMS, ToolCalls: toolCalls}
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

func newRunEndEvent(status string, exitCode int, reportFile string) *RunEndEvent {
	e := &RunEndEvent{Status: status, ExitCode: exitCode, ReportFile: reportFile}
	e.Type = EventRunEnd
	return e
}

func newTextEvent(phase string, turn int, text string) *TextEvent {
	e := &TextEvent{Phase: phase, Turn: turn, Text: text}
	e.Type = EventText
	return e
}

// eventsSink serializes events as JSONL onto zero or more writers. A nil sink,
// or one with no writers, is inactive: every method is then a no-op.
type eventsSink struct {
	mu        sync.Mutex
	tool      string
	sessionID string
	writers   []io.Writer
	now       func() time.Time

	// lastEmit is when the last event of any type was written; the heartbeat
	// window is measured from it. stage is the last stage a step or
	// phase_start event named.
	lastEmit time.Time
	stage    string
	// slow are the writers heartbeats reach only after a longer silence of
	// their own: the events file. See slowHeartbeats.
	slow map[io.Writer]*slowWriter

	// holding is true from HoldUntilRunStart until run_start is written or the
	// run ends: every other event is stamped when it happens, kept in held and
	// written, in order, straight after run_start.
	holding bool
	held    [][]byte

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
	// heartbeatFileIdle is the events file's own window: a heartbeat reaches
	// the file only after this long with nothing written to it.
	heartbeatFileIdle = 60 * time.Second
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
		// Nothing has named a stage before the first step or phase, and what
		// runs then — the baseline checks among it — is the preflight.
		stage:    "preflight",
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

// slowHeartbeats writes a heartbeat to w only after idle of silence on w
// itself, rather than the stream's window. The events file is tailed by a
// supervisor, which needs to tell a long silent phase from a dead run, but a
// heartbeat every fifteen seconds was more than a quarter of its lines.
func (s *eventsSink) slowHeartbeats(w io.Writer, idle time.Duration) {
	if s == nil || w == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.slow == nil {
		s.slow = map[io.Writer]*slowWriter{}
	}
	s.slow[w] = &slowWriter{idle: idle, last: s.now()}
}

// slowWriter is a writer heartbeats reach only after its own idle window:
// last is when anything was last written to it.
type slowWriter struct {
	idle time.Duration
	last time.Time
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
	e.stamp(now.UTC().Format(time.RFC3339), s.tool, s.sessionID)
	switch ev := e.(type) {
	case *StepEvent:
		s.stage = ev.Stage
	case *PhaseStartEvent:
		s.stage = ev.Phase
	}
	s.lastEmit = now
	line, err := encodeEvent(e)
	if err != nil {
		return
	}
	line = append(line, '\n')
	if s.holding {
		switch e.(type) {
		case *RunStartEvent:
			// Written first; what happened before it follows.
		case *RunEndEvent:
			// The run ends without a run_start (it failed before the model
			// was resolved); what was held is still written, before run_end.
			s.flushHeldLocked()
		default:
			s.held = append(s.held, line)
			return
		}
		defer func() {
			s.holding = false
			s.flushHeldLocked()
		}()
	}
	if _, beat := e.(*HeartbeatEvent); beat {
		for _, w := range s.writers {
			if sw, ok := s.slow[w]; ok {
				if now.Sub(sw.last) < sw.idle {
					continue
				}
				sw.last = now
			}
			_, _ = w.Write(line)
		}
		return
	}
	for _, sw := range s.slow {
		sw.last = now
	}
	s.writeLocked(line)
}

// writeLocked writes one finished line to every writer in a single Write each.
func (s *eventsSink) writeLocked(line []byte) {
	for _, w := range s.writers {
		_, _ = w.Write(line)
	}
}

// flushHeldLocked writes the held lines in the order they were recorded.
func (s *eventsSink) flushHeldLocked() {
	for _, line := range s.held {
		s.writeLocked(line)
	}
	s.held = nil
}

// HoldUntilRunStart makes run_start the stream's first line: until it is
// emitted, every other event is held and written right after it, so a warning
// recorded while the input is read or the model resolved cannot precede it. A
// run that ends first (run_end) writes what was held, then run_end. Heartbeats
// are not held but skipped: they are liveness for the model phases, and start
// with run_start. A sink that is never told to hold writes every event as it
// happens.
func (s *eventsSink) HoldUntilRunStart() {
	if !s.Active() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.holding = true
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
	if !s.ticking || s.holding {
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

// encodeEvent marshals one event. encoding/json refuses a NaN or infinite
// number — a cost_usd computed from a broken usage report — and dropping the
// event would break the stream's own guarantees (run_end is the last line).
// Such a number is written as 0 instead, and the event is kept.
func encodeEvent(e event) ([]byte, error) {
	line, err := json.Marshal(e)
	if err == nil {
		return line, nil
	}
	zeroNonFinite(reflect.ValueOf(e))
	return json.Marshal(e)
}

// zeroNonFinite sets every NaN or infinite float field reachable from v to 0.
func zeroNonFinite(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			zeroNonFinite(v.Elem())
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Field(i).CanSet() || v.Field(i).Kind() == reflect.Struct || v.Field(i).Kind() == reflect.Pointer {
				zeroNonFinite(v.Field(i))
			}
		}
	case reflect.Float32, reflect.Float64:
		if f := v.Float(); (math.IsNaN(f) || math.IsInf(f, 0)) && v.CanSet() {
			v.SetFloat(0)
		}
	}
}
