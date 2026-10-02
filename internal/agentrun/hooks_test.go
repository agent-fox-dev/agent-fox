package agentrun

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
)

// recObserver records every Observer call in order, so a test can assert
// both what was reported and when.
type recObserver struct {
	mu        sync.Mutex
	verbose   bool
	log       []string
	starts    []startCall
	ends      []endCall
	turns     []turnCall
	toolCalls []toolCall
	texts     []textCall
	details   []string
}

type startCall struct {
	phase, task string
	maxTurns    int
	budget      float64
}

type endCall struct {
	phase, stop string
	turns       int
	cost        float64
	durationMS  int64
}

type turnCall struct {
	phase         string
	turn          int
	cost          float64
	input, output int64
}

type toolCall struct {
	info ToolCallInfo
}

type textCall struct {
	phase string
	turn  int
	text  string
}

func (o *recObserver) Verbose() bool { return o.verbose }

func (o *recObserver) Detail(format string, args ...any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.details = append(o.details, fmt.Sprintf(format, args...))
}

func (o *recObserver) Raw(string) {}

func (o *recObserver) PhaseStart(phase, task string, maxTurns int, budgetUSD float64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.log = append(o.log, "phase_start")
	o.starts = append(o.starts, startCall{phase, task, maxTurns, budgetUSD})
}

func (o *recObserver) PhaseEnd(phase, stopReason string, turns int, costUSD float64, durationMS int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.log = append(o.log, "phase_end")
	o.ends = append(o.ends, endCall{phase, stopReason, turns, costUSD, durationMS})
}

func (o *recObserver) Turn(phase string, turn int, costUSD float64, in, out int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.log = append(o.log, "turn")
	o.turns = append(o.turns, turnCall{phase, turn, costUSD, in, out})
}

func (o *recObserver) ToolCall(info ToolCallInfo) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.log = append(o.log, "tool_call")
	o.toolCalls = append(o.toolCalls, toolCall{info: info})
}

func (o *recObserver) Text(phase string, turn int, text string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.log = append(o.log, "text")
	o.texts = append(o.texts, textCall{phase, turn, text})
}

func submitPhase(name, task string, got *string, calls *int) Phase {
	return Phase{
		Name: name, Task: task, System: "system", User: "user",
		Terminator: "submit",
		Custom:     []core.Tool{submitTool(got, calls)},
	}
}

// TS-07-26: PhaseStart fires once, first, with the resolved ceilings.
func TestTS07_26_PhaseStartOnceBeforeAnythingElse(t *testing.T) {
	var got string
	var calls int
	p := faux.New(toolCallTurn("c1", "submit", map[string]any{"value": "done"}))
	obs := &recObserver{verbose: true}
	cfg := fauxConfig(p, newWorkspace(t))
	cfg.Bounds = Bounds{MaxTurns: 12, MaxBudgetUSD: 3, MaxAttempts: 1}
	cfg.Observer = obs
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), submitPhase("implement", "task-3", &got, &calls)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []startCall{{"implement", "task-3", 12, 3.0}}
	if !reflect.DeepEqual(obs.starts, want) {
		t.Fatalf("PhaseStart calls = %+v, want %+v", obs.starts, want)
	}
	if obs.log[0] != "phase_start" {
		t.Errorf("first observer call = %q, want phase_start; log %v", obs.log[0], obs.log)
	}
}

// PhaseStart resolves zero bounds to the defaults actually applied.
func TestPhaseStartReportsDefaultsForZeroBounds(t *testing.T) {
	var got string
	var calls int
	p := faux.New(toolCallTurn("c1", "submit", map[string]any{"value": "done"}))
	obs := &recObserver{}
	cfg := fauxConfig(p, newWorkspace(t))
	cfg.Bounds = Bounds{MaxAttempts: 1}
	cfg.Observer = obs
	r, _ := NewRunner(cfg)
	if _, err := r.Run(context.Background(), submitPhase("p", "", &got, &calls)); err != nil {
		t.Fatal(err)
	}
	want := []startCall{{"p", "", DefaultMaxTurns, DefaultMaxBudgetUSD}}
	if !reflect.DeepEqual(obs.starts, want) {
		t.Fatalf("PhaseStart calls = %+v, want %+v", obs.starts, want)
	}
}

// TS-07-27: PhaseEnd fires once, from the Result Run returns.
func TestTS07_27_PhaseEndOnceFromTheResult(t *testing.T) {
	var got string
	var calls int
	p := faux.New(
		toolCallTurn("c1", "submit", map[string]any{"value": ""}),
		toolCallTurn("c2", "submit", map[string]any{"value": "done"}),
	)
	obs := &recObserver{}
	cfg := fauxConfig(p, newWorkspace(t))
	cfg.Observer = obs
	r, _ := NewRunner(cfg)
	res, err := r.Run(context.Background(), submitPhase("phase", "", &got, &calls))
	if err != nil {
		t.Fatal(err)
	}
	want := []endCall{{"phase", string(res.StopReason), res.Turns, res.Usage.CostUSD, res.Elapsed.Milliseconds()}}
	if !reflect.DeepEqual(obs.ends, want) {
		t.Fatalf("PhaseEnd calls = %+v, want %+v", obs.ends, want)
	}
	if res.Turns != 2 {
		t.Errorf("Turns = %d, want 2", res.Turns)
	}
	if obs.log[len(obs.log)-1] != "phase_end" {
		t.Errorf("phase_end was not last: %v", obs.log)
	}
}

// PhaseEnd is paired with PhaseStart on the error path too.
func TestPhaseEndOnErrorPath(t *testing.T) {
	var got string
	var calls int
	p := faux.New(textTurn("no tool call"))
	obs := &recObserver{}
	cfg := fauxConfig(p, newWorkspace(t))
	cfg.Observer = obs
	r, _ := NewRunner(cfg)
	ph := submitPhase("phase", "", &got, &calls)
	ph.Terminator = ""
	if _, err := r.Run(context.Background(), ph); err == nil {
		t.Fatal("want an error for a phase without a terminator")
	}
	if len(obs.starts) != 1 || len(obs.ends) != 1 {
		t.Fatalf("starts=%d ends=%d, want 1 each", len(obs.starts), len(obs.ends))
	}
}

// TS-07-28: Turn fires once per turn boundary with a monotonic counter.
func TestTS07_28_TurnOncePerBoundary(t *testing.T) {
	var got string
	var calls int
	p := faux.New(
		toolCallTurn("c1", "submit", map[string]any{"value": ""}),
		toolCallTurn("c2", "submit", map[string]any{"value": ""}),
		toolCallTurn("c3", "submit", map[string]any{"value": "done"}),
	)
	obs := &recObserver{}
	cfg := fauxConfig(p, newWorkspace(t))
	cfg.Observer = obs
	r, _ := NewRunner(cfg)
	res, err := r.Run(context.Background(), submitPhase("phase", "", &got, &calls))
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.turns) != 3 {
		t.Fatalf("Turn calls = %+v, want 3", obs.turns)
	}
	var cost float64
	for i, c := range obs.turns {
		if c.phase != "phase" || c.turn != i+1 {
			t.Errorf("turn call %d = %+v", i, c)
		}
		if c.cost < 0 || c.input < 0 || c.output < 0 {
			t.Errorf("turn call %d has negative usage: %+v", i, c)
		}
		cost += c.cost
	}
	if diff := cost - res.Usage.CostUSD; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("turn costs sum to %v, phase cost is %v", cost, res.Usage.CostUSD)
	}
}

// TS-07-29: a traced tool result reports one non-blocked call.
// The event is emitted when the result arrives, once per call.
func TestTS07_29_TraceReportsToolCallWhenVerbose(t *testing.T) {
	obs := &recObserver{verbose: true}
	r := &Runner{cfg: Config{Observer: obs}}
	var turn int
	var tb textBuffer
	pending := make(map[string]core.ToolUseBlock)
	r.trace("implement", &turn, &toolErrorCounter{}, &blockCounter{}, &tb, pending, core.ToolCallEndEvent{Block: core.ToolUseBlock{ID: "c1", Name: "write_file", Input: json.RawMessage(`{}`)}})
	if len(obs.toolCalls) != 0 {
		t.Fatalf("a call was reported before its result: %+v", obs.toolCalls)
	}
	r.trace("implement", &turn, &toolErrorCounter{}, &blockCounter{}, &tb, pending, core.ToolResultEvent{Message: core.ToolResultMessage{ToolUseID: "c1", ToolName: "write_file"}})
	if len(obs.toolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1: %+v", len(obs.toolCalls), obs.toolCalls)
	}
	if obs.toolCalls[0].info.Name != "write_file" || obs.toolCalls[0].info.Phase != "implement" || obs.toolCalls[0].info.Blocked {
		t.Fatalf("ToolCall = %+v, want phase=implement name=write_file blocked=false", obs.toolCalls[0].info)
	}
}

// A refused call is reported once: one tool_call event, flagged blocked, and
// no ERROR or reason line repeating what "blocked <tool>: <reason>" said (#64).
func TestARefusedCallIsReportedOnce(t *testing.T) {
	obs := &recObserver{verbose: true}
	r := &Runner{cfg: Config{Observer: obs}}
	var turn int
	var tb textBuffer
	blocks := &blockCounter{}
	errs := &toolErrorCounter{}
	pending := make(map[string]core.ToolUseBlock)
	blocks.inc("execute")
	r.trace("p", &turn, errs, blocks, &tb, pending, core.ToolCallEndEvent{Block: core.ToolUseBlock{ID: "c1", Name: "execute", Input: json.RawMessage(`{}`)}})
	r.trace("p", &turn, errs, blocks, &tb, pending, core.ToolExecutionEndEvent{Name: "execute", IsError: true})
	r.trace("p", &turn, errs, blocks, &tb, pending, core.ToolResultEvent{Message: core.ToolResultMessage{
		ToolUseID: "c1", ToolName: "execute", IsError: true, Content: core.Content{core.TextBlock{Text: "guard refused"}}}})
	if len(obs.toolCalls) != 1 || obs.toolCalls[0].info.Name != "execute" || obs.toolCalls[0].info.Phase != "p" || !obs.toolCalls[0].info.Blocked {
		t.Errorf("tool calls = %+v, want one blocked execute in phase p", obs.toolCalls)
	}
	for _, d := range obs.details {
		if strings.Contains(d, "ERROR") || strings.Contains(d, "guard refused") {
			t.Errorf("a refusal was reported again: %q", d)
		}
	}
	if errs.snapshot() != nil {
		t.Errorf("a refusal was counted as a tool error: %v", errs.snapshot())
	}
}

func blockedExecuteRun(t *testing.T, verbose bool) *recObserver {
	t.Helper()
	var got string
	var calls int
	p := faux.New(
		toolCallTurn("c1", "execute", map[string]any{"command": "git push"}),
		toolCallTurn("c2", "submit", map[string]any{"value": "done"}),
	)
	obs := &recObserver{verbose: verbose}
	cfg := fauxConfig(p, newWorkspace(t))
	cfg.Observer = obs
	r, _ := NewRunner(cfg)
	ph := submitPhase("phase", "", &got, &calls)
	ph.BuiltinTools = []string{"execute"}
	ph.Programs = []string{"ls", "git"}
	res, err := r.Run(context.Background(), ph)
	if err != nil {
		t.Fatal(err)
	}
	if res.Blocked != 1 {
		t.Fatalf("Blocked = %d, want 1", res.Blocked)
	}
	return obs
}

// TS-07-30: a guard refusal is reported as a blocked tool call, and the
// counter and detail line are unchanged.
func TestTS07_30_GuardBlockReportsToolCall(t *testing.T) {
	obs := blockedExecuteRun(t, true)
	found := false
	for _, c := range obs.toolCalls {
		if c.info.Phase == "phase" && c.info.Name == "execute" && c.info.Blocked {
			found = true
		}
	}
	if !found {
		t.Errorf("no blocked execute ToolCall in %+v", obs.toolCalls)
	}
	detail := false
	for _, d := range obs.details {
		if strings.HasPrefix(d, "blocked execute: ") {
			detail = true
		}
	}
	if !detail {
		t.Errorf("no 'blocked execute: …' detail line in %q", obs.details)
	}
}

// TS-07-32: ToolCall is emitted for every call, regardless of verbose.
// (Updated from the original assertion that no ToolCall was made without
// --verbose, because the Verbose() check was removed in 12-REQ-6.2.)
func TestTS07_32_ToolCallEmittedWithoutVerbose(t *testing.T) {
	obs := blockedExecuteRun(t, false)
	if len(obs.toolCalls) == 0 {
		t.Fatal("ToolCall should be emitted even without --verbose")
	}
}
