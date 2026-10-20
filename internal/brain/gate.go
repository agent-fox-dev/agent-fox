package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/policy"
	"github.com/agent-fox-dev/agentkit-go/core"
)

// GateConfig is what a gate is made of.
type GateConfig struct {
	// Brain is the brain's name, for what the gate reports.
	Brain string
	// Allowed is Brief.Tools: the box tools the brain may call. Terminating is
	// the tool that ends the run, which is allowed whatever Allowed holds.
	Allowed     []string
	Terminating string
	// Hook is the policy the guard decides with, and Suite the command forms
	// that count as the full test suite.
	Hook  HookConfig
	Suite [][]string
	// Dir is the box's workspace, which the paths of a call resolve against.
	Dir string
	// Box is the box's tool set.
	Box []core.Tool
	// Observe, when it is not nil, hears of every decided call once.
	Observe func(ToolCall)
	// Transcript, when it is not nil, gets a line for every decided call.
	Transcript *Transcript
}

// Verdict is the gate's decision on one call.
type Verdict struct {
	Decision string
	Reason   string
}

// Gate is the one check every tool call of an in-process brain passes. A call
// is allowed only when its tool is on the brief's allowlist (the terminating
// tool always is) and the ring-1 guard, given the brief's hook, allows it: no
// shell command that the guard refuses, and no write to a path that the hook
// protects, which always includes .agent-fox/, where only af writes. A denied
// call is never executed, and its reason goes back to the brain as the tool's
// error result, which is the brain's next turn.
type Gate struct {
	brain      string
	allowed    map[string]bool
	byName     map[string]core.Tool
	policy     policy.Policy
	dir        string
	observe    func(ToolCall)
	transcript *Transcript
}

// NewGate makes the gate. An allowlist that names a tool the box does not
// provide is an error, which a brain raises before it makes a request or runs a
// step.
func NewGate(cfg GateConfig) (*Gate, error) {
	if err := ValidateTools(cfg.Allowed, cfg.Box); err != nil {
		return nil, err
	}
	g := &Gate{
		brain: cfg.Brain, allowed: map[string]bool{}, byName: map[string]core.Tool{}, dir: cfg.Dir,
		observe: cfg.Observe, transcript: cfg.Transcript,
	}
	for _, name := range cfg.Allowed {
		g.allowed[name] = true
	}
	if cfg.Terminating != "" {
		g.allowed[cfg.Terminating] = true
	}
	for _, t := range cfg.Box {
		g.byName[t.Name] = t
	}
	if g.dir == "" {
		g.dir = "."
	}
	protected := slices.Clone(cfg.Hook.ProtectedPaths)
	if !slices.Contains(protected, protectedAgentDir) {
		protected = append(protected, protectedAgentDir)
	}
	g.policy = policy.Policy{Suite: cfg.Suite, ProtectedPaths: protected, AllowPrograms: cfg.Hook.AllowPrograms}
	return g, nil
}

// GateFor makes the gate of a brief: its allowlist, its hook, the box's tools and
// workspace, and the suite of the host checkout, which is what the guard refuses
// to run.
func GateFor(ctx context.Context, b Brief, brainName string, transcript *Transcript) (*Gate, error) {
	pol, err := policy.LoadPolicy(ctx, b.Box.HostDir(), "")
	if err != nil {
		return nil, fmt.Errorf("brain: the guard policy of %s cannot be loaded: %w", b.Box.HostDir(), err)
	}
	return NewGate(GateConfig{
		Brain: brainName, Allowed: b.Tools, Terminating: b.Kind.ToolName, Hook: b.Hook, Suite: pol.Suite,
		Dir: b.Box.Spec().Workdir, Box: b.Box.Tools(), Observe: b.Observe, Transcript: transcript,
	})
}

// ValidateTools checks an allowlist against the tools of the box, and returns an
// error that names every tool the box does not provide. It is an internal
// error: a brief that asks for a tool that is not there is a mistake of the
// caller's.
func ValidateTools(allowed []string, box []core.Tool) error {
	var unknown []string
	for _, name := range allowed {
		if !slices.ContainsFunc(box, func(t core.Tool) bool { return t.Name == name }) && !slices.Contains(unknown, name) {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	have := make([]string, len(box))
	for i, t := range box {
		have[i] = t.Name
	}
	slices.Sort(have)
	return fmt.Errorf("brain: the tools of the brief name %s, which the box does not provide; it has %s", strings.Join(unknown, ", "), strings.Join(have, ", "))
}

// Decide decides a call from its tool and arguments, and executes nothing.
func (g *Gate) Decide(tool string, args map[string]any) Verdict {
	if !g.allowed[tool] {
		return Verdict{Decision: DecisionDeny, Reason: fmt.Sprintf("tool %s is not in this run's allowlist", tool)}
	}
	if _, isBox := g.byName[tool]; !isBox {
		return Verdict{Decision: DecisionAllow} // the terminating tool, which the brain wires in
	}
	if d := policy.DecideAt(g.policy, g.dir, g.dir, tool, args); d.Decision == policy.Deny {
		return Verdict{Decision: DecisionDeny, Reason: d.Reason}
	}
	return Verdict{Decision: DecisionAllow}
}

// Record notes a call that was decided: Observe is told, and the transcript gets
// a line. result is the text the brain was given back.
func (g *Gate) Record(tool string, args []byte, v Verdict, ok bool, result string, took time.Duration) {
	if g.observe != nil {
		g.observe(ToolCall{Brain: g.brain, Tool: tool, Decision: v.Decision, Reason: v.Reason, OK: ok, DurationMS: took.Milliseconds()})
	}
	if g.transcript != nil {
		g.transcript.Add(tool, string(args), v.Decision, v.Reason, ok, result)
	}
}

// Call decides a call, executes it on the box when it is allowed, records it and
// returns the result. A denied call returns its reason as the error result.
func (g *Gate) Call(ctx context.Context, tool string, args json.RawMessage) core.ToolResult {
	start := time.Now()
	var decoded map[string]any
	_ = json.Unmarshal(args, &decoded) // arguments that are no object are the tool's to refuse

	v := g.Decide(tool, decoded)
	if v.Decision == DecisionDeny {
		r := Denial(v.Reason)
		g.Record(tool, args, v, false, r.LLMText(), time.Since(start))
		return r
	}
	t, ok := g.byName[tool]
	if !ok {
		r := core.ErrResult("unknown_tool", fmt.Sprintf("the box has no tool %s", tool))
		g.Record(tool, args, v, false, r.LLMText(), time.Since(start))
		return r
	}
	var r core.ToolResult
	switch {
	case t.Execute != nil:
		r = t.Execute(ctx, args)
	case t.Handler != nil:
		out, err := t.Handler(ctx, args)
		if err != nil {
			r = core.ErrResult("handler_error", err.Error())
		} else {
			r = core.ToolResult{OK: true, Text: string(out)}
		}
	}
	g.Record(tool, args, v, r.OK, r.LLMText(), time.Since(start))
	return r
}

// Denial is the result of a call that was denied: the reason is what the brain
// reads.
func Denial(reason string) core.ToolResult {
	return core.ToolResult{OK: false, Error: core.BlockErrorCode, Detail: reason, Text: reason}
}
