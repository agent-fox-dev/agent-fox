package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	agentkit "github.com/agent-fox-dev/agentkit-go"
	"github.com/agent-fox-dev/agentkit-go/core"
)

func init() {
	Default.Register(Entry{Name: AgentkitName, CostSource: CostMeasured, Credentials: []string{VendorKeyVar}, DriverInBox: true, New: newAgentkit})
}

// agentkitBrain runs the AgentKit driver on the host, over the tools of the
// box: the model, the driver and the loop are here, and only the results of
// tools cross into the box and back. It holds no credential in the box.
type agentkitBrain struct {
	model    string
	provider core.ProviderClient
	// priced says the cost of a run can be measured: the catalog lists the model,
	// or the provider is one the caller made.
	priced bool
}

// newAgentkit makes the brain. Under landlock and podman, unless it borrows the box's
// hands, it is the driver in the box (see agentkitInBox). Otherwise the provider is
// Options.NewProvider's, or the vendor's for the model with the credentials of the
// environment, and a missing credential is failed/auth, before any box opens.
func newAgentkit(opts Options) (Brain, error) {
	if driverInBox(opts) {
		return newAgentkitInBox(opts)
	}
	model := opts.Model
	if model == "" {
		model = defaultModel()
	}
	var provider core.ProviderClient
	var err error
	priced := true
	if opts.NewProvider != nil {
		provider, err = opts.NewProvider(model)
	} else {
		provider, priced, err = anthropicProvider(model, opts.lookup())
	}
	if err != nil {
		return nil, err
	}
	return &agentkitBrain{model: model, provider: provider, priced: priced}, nil
}

// agentkitRun is one run of the driver: what its guard and its interceptor
// share.
type agentkitRun struct {
	gate     *Gate
	brief    Brief
	cancel   context.CancelFunc
	accepted atomic.Bool
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
	if !a.priced && b.MaxCostUSD > 0 {
		return Outcome{StopReason: StopError}, &toolio.Failure{
			Category: toolio.CategoryUsage,
			Message:  fmt.Sprintf("the cost bound of $%s cannot be enforced: the model %s is not in the catalog, so what a run of it costs is not known", decimal(b.MaxCostUSD), a.model),
			FixHint:  &toolio.FixHint{Env: modelVar},
		}
	}
	tr, err := openTranscript(b)
	if err != nil {
		return Outcome{StopReason: StopError}, err
	}
	defer closeTranscript(tr, b)
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

	if !a.priced && b.Warn != nil {
		b.Warn("model_unpriced", fmt.Sprintf("the model %s is not in the catalog, so the run has no cost to report", a.model))
	}
	res, runErr := agent.Run(runCtx, b.Task)
	out, err := r.outcome(ctx, res, runErr, b.TranscriptPath)
	if !a.priced {
		out.CostUSD, out.CostSource = 0, CostNone
	}
	return out, err
}

// toolset is the driver's tools: the box's tools that the brief allows, the
// others as stubs that deny (which the model is not offered and whose guidelines
// stay out of the system prompt), and the terminating tool. It returns the names
// of the tools that are hidden.
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
		stub.PromptGuidelines = nil
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
		// One call at a time: the submit tool reads the box and writes the one
		// file, and the second of two submits in a turn comes after the first.
		ExecutionMode: core.Sequential,
		Execute:       r.submit,
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
// denied call is recorded here, since the driver does not run it and does not
// give it to the interceptor after a call.
func (r *agentkitRun) guard(ctx context.Context, in core.BeforeToolCallContext) core.BeforeToolCallDecision {
	v := r.gate.Decide(in.ToolName, in.Arguments)
	if v.Decision == DecisionAllow {
		return core.BeforeToolCallDecision{}
	}
	r.gate.Record(in.ToolName, in.RawInput, v, false, v.Reason, 0)
	return core.BeforeToolCallDecision{Block: true, Reason: v.Reason}
}

// after is the driver's interceptor after a call: what ran is recorded with its
// result. An accepted submission ends the run, by the vote of the terminating
// tool and by this interceptor's own, and cancels the driver's context, which
// aborts the calls of the same turn that have not started.
func (r *agentkitRun) after(ctx context.Context, in core.AfterToolCallContext) core.AfterToolCallDecision {
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
