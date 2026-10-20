package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/submission"
	agentkit "github.com/agent-fox-dev/agentkit-go"
	"github.com/agent-fox-dev/agentkit-go/core"
)

func init() {
	Default.Register(Entry{Name: AgentkitName, CostSource: CostMeasured, New: newAgentkit})
}

// agentkitBrain runs the AgentKit driver on the host, over the tools of the
// box: the model, the driver and the loop are here, and only the results of
// tools cross into the box and back. It holds no credential in the box.
type agentkitBrain struct {
	model    string
	provider core.ProviderClient
}

// newAgentkit makes the brain. The provider is Options.NewProvider's, or the
// vendor's for the model with the credentials of the environment, and a missing
// credential is failed/auth, before any box opens.
func newAgentkit(opts Options) (Brain, error) {
	model := opts.Model
	if model == "" {
		model = defaultModel()
	}
	var provider core.ProviderClient
	var err error
	if opts.NewProvider != nil {
		provider, err = opts.NewProvider(model)
	} else {
		provider, err = anthropicProvider(model, opts.lookup())
	}
	if err != nil {
		return nil, err
	}
	return &agentkitBrain{model: model, provider: provider}, nil
}

// agentkitRun is one run of the driver: what its guard and its interceptor
// share.
type agentkitRun struct {
	gate     *Gate
	brief    Brief
	cancel   context.CancelFunc
	accepted atomic.Bool
	denied   sync.Map // the ids of the calls the gate denied, which it has recorded already
}

// Run sends the task as the first user message of the driver, with the prefix
// of the brief as the prefix of every request, the allowed tools of the box and
// the terminating tool of the kind as its tools, and the gate as its guard. The
// terminating tool submits against the session: a refusal comes back as its error
// result, with every problem, and the loop goes on; an acceptance ends it. The
// outcome has the cost, turns and tokens that the driver counted.
func (a *agentkitBrain) Run(ctx context.Context, b Brief) (Outcome, error) {
	if b.Box == nil {
		return Outcome{StopReason: StopError}, errors.New("the agentkit brain needs a box")
	}
	if err := ValidateTools(b.Tools, b.Box.Tools()); err != nil {
		return Outcome{StopReason: StopError}, err
	}
	var tr *Transcript
	if b.TranscriptPath != "" {
		var err error
		if tr, err = NewTranscript(b.TranscriptPath); err != nil {
			return Outcome{StopReason: StopError}, fmt.Errorf("the transcript cannot be written: %w", err)
		}
		defer tr.Close()
	}
	gate, err := GateFor(ctx, b, AgentkitName, tr)
	if err != nil {
		return Outcome{StopReason: StopError}, err
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	r := &agentkitRun{gate: gate, brief: b, cancel: cancel}
	toolset, hidden, err := r.toolset()
	if err != nil {
		return Outcome{StopReason: StopError}, err
	}
	cfg := agentkit.Config{
		Provider: visibleTools{inner: a.provider, hidden: hidden},
		Model:    a.model,
		Toolset:  toolset,
		MaxTurns: b.MaxTurns, MaxCostUSD: b.MaxCostUSD,
	}
	if b.Prefix != "" {
		cfg.Prefix = []core.Message{core.UserMessage{Content: core.Content{core.TextBlock{Text: b.Prefix}}, Timestamp: time.Now()}}
	}
	agent, err := agentkit.New(cfg)
	if err != nil {
		return Outcome{StopReason: StopError}, err
	}

	res, runErr := agent.Run(runCtx, b.Task)
	return r.outcome(ctx, res, runErr, b.TranscriptPath)
}

// toolset is the driver's tools: the box's tools that the brief allows, the
// others as stubs that deny (and that the model is not told of), and the
// terminating tool. It returns the names of the tools that are hidden.
func (r *agentkitRun) toolset() (agentkit.Toolset, []string, error) {
	b := r.brief
	var tools []core.Tool
	var hidden []string
	for _, t := range b.Box.Tools() {
		if slices.Contains(b.Tools, t.Name) {
			tools = append(tools, t)
			continue
		}
		stub := t
		stub.Handler = nil
		stub.Execute = func(context.Context, json.RawMessage) core.ToolResult {
			return Denial(fmt.Sprintf("tool %s is not in this run's allowlist", t.Name))
		}
		tools = append(tools, stub)
		hidden = append(hidden, t.Name)
	}
	in, err := inputSchema(b.Kind.Schema)
	if err != nil {
		return agentkit.Toolset{}, nil, err
	}
	tools = append(tools, core.Tool{
		Name: b.Kind.ToolName, Description: b.Kind.Description, InputSchema: in, Terminating: true,
		Execute: r.submit,
	})
	return agentkit.Toolset{Tools: tools, Guard: r.guard, After: r.after}, hidden, nil
}

// submit is the terminating tool: it submits the payload against the session. A
// refusal is the tool's error result with every problem, and the loop goes on.
func (r *agentkitRun) submit(ctx context.Context, in json.RawMessage) core.ToolResult {
	b := r.brief
	_, err := submission.Submit(ctx, submission.Target{Tree: b.Box.FS(ctx), Write: b.Box.WriteFile, RunID: b.RunID}, b.Kind, in, time.Now())
	var refused *submission.Refused
	switch {
	case err == nil:
		r.accepted.Store(true)
		return core.ToolResult{OK: true, Text: "The submission was accepted. Nothing more is needed.", Terminate: true}
	case errors.As(err, &refused):
		return core.ToolResult{OK: false, Error: "submission_refused", Detail: refused.Error(), Text: refused.Error()}
	}
	return core.ErrResult("submit_failed", err.Error())
}

// guard is the driver's interceptor before a call: the gate decides, and a
// denied call is recorded here, since it will not run.
func (r *agentkitRun) guard(ctx context.Context, in core.BeforeToolCallContext) core.BeforeToolCallDecision {
	v := r.gate.Decide(in.ToolName, in.Arguments)
	if v.Decision == DecisionAllow {
		return core.BeforeToolCallDecision{}
	}
	r.denied.Store(in.ToolUseID, true)
	r.gate.Record(in.ToolName, in.RawInput, v, false, v.Reason, 0)
	return core.BeforeToolCallDecision{Block: true, Reason: v.Reason}
}

// after is the driver's interceptor after a call: what ran is recorded with its
// result. An accepted submission ends the run, by the vote of the terminating
// tool and, for a model that submitted among other calls of the same turn, by
// cancelling the driver's context.
func (r *agentkitRun) after(ctx context.Context, in core.AfterToolCallContext) core.AfterToolCallDecision {
	if _, was := r.denied.LoadAndDelete(in.ToolUseID); was {
		return core.AfterToolCallDecision{}
	}
	args, _ := json.Marshal(in.Arguments)
	r.gate.Record(in.ToolName, args, Verdict{Decision: DecisionAllow}, in.ToolResult.OK, in.ToolResult.LLMText(), in.Elapsed)
	if in.ToolName == r.brief.Kind.ToolName && r.accepted.Load() {
		r.cancel()
		stop := true
		return core.AfterToolCallDecision{Terminate: &stop}
	}
	return core.AfterToolCallDecision{}
}

// outcome is what the run came to, from the driver's result. A submission that
// was accepted is the outcome, whatever the context's cancellation made of the
// run. A limit is a stop and not an error; anything else the driver could not do
// is the error of Run.
func (r *agentkitRun) outcome(ctx context.Context, res core.RunResult, runErr error, transcript string) (Outcome, error) {
	out := Outcome{
		CostUSD: res.Usage.CostUSD, CostSource: CostMeasured, Turns: res.TurnCount, Transcript: transcript,
		InputTokens:  int(res.Usage.InputTokens + res.Usage.CacheReadTokens + res.Usage.CacheWriteTokens),
		OutputTokens: int(res.Usage.OutputTokens),
	}
	if r.accepted.Load() {
		out.Submitted, out.StopReason = true, StopSubmitted
		return out, nil
	}
	switch res.StopReason {
	case core.RunStopBudgetExceeded:
		out.StopReason = StopBudget
		return out, nil
	case core.RunStopMaxTurns:
		out.StopReason = StopTurns
		return out, nil
	case core.RunStopEndTurn:
		out.StopReason = StopEnded
		return out, nil
	}
	out.StopReason = StopError
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if runErr == nil {
		runErr = fmt.Errorf("the driver stopped: %s", res.StopReason)
	}
	return out, runErr
}
