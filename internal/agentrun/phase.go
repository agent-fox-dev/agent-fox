package agentrun

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	agentkit "github.com/agentfox/agentkit-go"
	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/skills"
	"github.com/agentfox/agentkit-go/tools"
)

// Bounds are the ceilings on one phase. They are not the model's to choose,
// and they fail differently: turns catch a model looping cheaply, the budget
// catches one reading large files a few expensive times, and the timeout
// catches a provider that never answers.
type Bounds struct {
	// MaxTurns bounds one phase. Zero means DefaultMaxTurns.
	//
	// It is also the repair budget. A schema or rule violation is not a
	// hand-assembled second conversation; it is a tool error the loop
	// appends, so "how many attempts does the model get" and "how many turns
	// may this phase take" are the same number.
	MaxTurns int
	// MaxBudgetUSD bounds one phase's spend. Zero means DefaultMaxBudgetUSD.
	MaxBudgetUSD float64
	// MaxAttempts is how many times one model call is retried on a transient
	// failure. Zero means DefaultMaxAttempts; one means no retries.
	MaxAttempts int
	// Timeout is the wall-clock ceiling on one phase. Zero means no ceiling
	// beyond the caller's context.
	Timeout time.Duration
}

// Defaults applied when Bounds leaves a field at zero.
const (
	// DefaultMaxTurns is generous because a phase with a workspace spends
	// most of its turns reading, and stingy enough that a model looping on a
	// validation error it cannot fix stops costing money.
	DefaultMaxTurns = 60
	// DefaultMaxBudgetUSD bounds one phase, not a whole run.
	DefaultMaxBudgetUSD = 5.00
	// DefaultMaxAttempts retries a transient provider failure twice.
	//
	// AgentKit's own default is 1 — no retries — on the argument that a
	// hidden retry spends money the caller did not authorize. These tools ask
	// for three because a phase is one long call something is waiting on, and
	// a transport hiccup ten minutes into an autonomous run should not cost
	// the run.
	DefaultMaxAttempts = 3
	// compactionReserveTokens bounds the summary a compaction writes: its
	// max_tokens is 0.8 x this, clamped to the model's own ceiling.
	compactionReserveTokens = 8000
	// compactionThreshold is the fraction of the context window at which the
	// transcript is summarized in place.
	compactionThreshold = 0.6
)

func (b Bounds) maxTurns() int {
	if b.MaxTurns > 0 {
		return b.MaxTurns
	}
	return DefaultMaxTurns
}

func (b Bounds) maxBudget() float64 {
	if b.MaxBudgetUSD > 0 {
		return b.MaxBudgetUSD
	}
	return DefaultMaxBudgetUSD
}

func (b Bounds) maxAttempts() int {
	if b.MaxAttempts > 0 {
		return b.MaxAttempts
	}
	return DefaultMaxAttempts
}

// Observer receives a phase's progress. toolio.Progress satisfies it; a test
// passes nil.
type Observer interface {
	// Detail reports one line of tracing, shown only under --verbose.
	Detail(format string, args ...any)
	// Raw writes model prose verbatim, shown only under --show-text.
	Raw(s string)
}

// Config is what a Runner is built with: everything that is the same for
// every phase of one tool invocation.
type Config struct {
	// Model is the resolved model. Required.
	Model *core.Model
	// Thinking is the reasoning level the tier prescribed, if any.
	Thinking core.ThinkingLevel
	// Providers overrides the wire implementations. Nil means
	// DefaultProviders. A test supplies provider/faux here and needs no
	// network, no key and no mock of the tool's own types.
	Providers core.ProviderRegistry
	// Workspace roots the file tools. Every path a tool is handed is
	// resolved against it, symlinks included, so a path that escapes is
	// refused by the tool rather than by a paragraph in a prompt.
	Workspace *tools.Workspace
	// TrustProject admits repository-authored skills and context files —
	// AGENTS.md, CLAUDE.md, .specs/steering.md — into the system prompt. Its
	// zero value is false because a repository that is merely the current
	// directory would otherwise author part of the prompt that reads it.
	TrustProject bool
	// WorkDir is where skills and context files are discovered. Empty means
	// the workspace root.
	WorkDir string
	// Bounds are the per-phase ceilings.
	Bounds Bounds
	// Observer receives progress. May be nil.
	Observer Observer
	// ShowText streams the model's own prose to the observer.
	ShowText bool
	// SessionPrefix namespaces the SessionID each phase reports, which
	// becomes prompt_cache_key on the OpenAI wires. It is the TOOL's name,
	// not a per-run identifier: every "issue/triage" call in the world shares
	// this program's system prompt and tool schemas, and making the key
	// unique per run would fragment exactly the cache it exists to help.
	SessionPrefix string
}

// Phase is one model-facing step: a system prompt, a user prompt, the tools
// it may call, and the one tool whose arguments are the step's result.
type Phase struct {
	// Name identifies the step in errors, events and the cost report.
	Name string
	// System and User are the two prompts.
	System string
	User   string
	// Terminator is the name of the tool that ends the phase by producing
	// its result. A run that ends without it is a failure, not a partial
	// success, which is what makes "there is a result" a fact rather than a
	// judgement about a stop reason.
	Terminator string
	// Custom are the phase's own tools, including the terminator.
	Custom []core.Tool
	// BuiltinTools names the tools from tools.All to register. Empty means
	// none: a phase with no workspace declares only its terminator.
	BuiltinTools []string
	// ReadOnly refuses every mutating file tool and every shell operator.
	ReadOnly bool
	// Programs is the shell allowlist, used only when BuiltinTools includes
	// a shell tool.
	Programs []string
	// ProtectedPaths are directories the file tools may not write under
	// even though the phase writes elsewhere. See GuardOptions.
	ProtectedPaths []string
	// MaxTokens caps one response. Zero leaves the provider's default.
	MaxTokens int
	// Temperature is low for every phase in these tools, because each one
	// produces a structured artifact that is then validated: creativity here
	// shows up as a schema violation.
	Temperature float64
	// LoadProjectContext enables skill and context-file discovery for this
	// phase. It has no effect without a work directory, and its project tier
	// has none without Config.TrustProject.
	LoadProjectContext bool
}

// Result is what a finished phase reports.
type Result struct {
	Name       string
	Turns      int
	StopReason core.RunStopReason
	Usage      core.Usage
	Elapsed    time.Duration
	// Blocked counts the tool calls the authorization guard refused. A
	// nonzero count is the guard working; a large one usually means the
	// phase's allowlist is too narrow for the project.
	Blocked int
}

// Runner builds and drives one agent per phase.
//
// Phases are separate agents rather than one conversation because sharing a
// transcript carries each phase's reasoning into the next, and the next phase
// is usually meant to start from the first one's *conclusion* rather than
// from how it got there.
type Runner struct {
	cfg Config
}

// NewRunner returns a Runner. The model must already be resolved: resolution
// and the credential check happen before any prompt is built, so an operator
// learns about a missing key in the first second.
func NewRunner(cfg Config) (*Runner, error) {
	if cfg.Model == nil {
		return nil, newError("", CategoryModel, nil, "no model resolved")
	}
	if cfg.Providers == nil {
		cfg.Providers = DefaultProviders()
	}
	if cfg.WorkDir == "" && cfg.Workspace != nil {
		cfg.WorkDir = cfg.Workspace.Root
	}
	if cfg.SessionPrefix == "" {
		cfg.SessionPrefix = "agent-fox"
	}
	return &Runner{cfg: cfg}, nil
}

// Model reports the model every phase runs on.
func (r *Runner) Model() *core.Model { return r.cfg.Model }

// Run drives one phase to its terminating tool.
//
// The (Result, error) pair is the honest return: a run can end without a
// result — budget, turn limit, a model that answered in prose — and the
// caller has to be able to tell that apart from a finished phase. The
// terminator is what "there is a result" means, and the error names the stop
// reason when there is not one.
func (r *Runner) Run(ctx context.Context, p Phase) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{Name: p.Name}, err
	}
	if p.Terminator == "" {
		return Result{Name: p.Name}, newError(p.Name, CategoryInternal, nil,
			"phase declares no terminating tool")
	}
	if r.cfg.Bounds.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.cfg.Bounds.Timeout)
		defer cancel()
	}

	agent, blocked, err := r.newAgent(p)
	if err != nil {
		return Result{Name: p.Name}, err
	}

	start := time.Now()
	stream, err := agent.Stream(ctx, p.User)
	if err != nil {
		return Result{Name: p.Name, Elapsed: time.Since(start)}, r.wrap(p, core.RunResult{}, err)
	}
	for e := range stream.Events() {
		r.trace(e)
	}
	res, runErr := stream.RunResult()

	out := Result{
		Name:       p.Name,
		Turns:      res.TurnCount,
		StopReason: res.StopReason,
		Usage:      res.Usage,
		Elapsed:    time.Since(start),
		Blocked:    blocked.count(),
	}
	return out, r.wrap(p, res, runErr)
}

// newAgent configures the agent for one phase. The returned counter is where
// the authorization guard records its refusals.
func (r *Runner) newAgent(p Phase) (*agentkit.Agent, *blockCounter, error) {
	maxTokens := p.MaxTokens
	temperature := p.Temperature

	cfg := core.AgentConfig{
		Model:         r.cfg.Model,
		Provider:      r.cfg.Model.Provider,
		Providers:     r.cfg.Providers,
		SystemPrompt:  p.System,
		ThinkingLevel: r.cfg.Thinking,
		SessionID:     r.cfg.SessionPrefix + "/" + p.Name,
		TrustProject:  r.cfg.TrustProject,
		// The stop policy is turns and budget only. It deliberately does NOT
		// include StopWhenToolCalled(p.Terminator): that ends the run when
		// the terminator is CALLED, and a call the handler rejects is the
		// repair loop's first step, not its last. The intended ending is the
		// handler's ToolResult.Terminate, which is set on acceptance and not
		// on a rejection — so a malformed submission comes back to the model
		// as an error it can fix, and these two bounds are what stop a phase
		// that never converges.
		StopPolicy: agentkit.StopAny(
			agentkit.StopAfterTurns(r.cfg.Bounds.maxTurns()),
			agentkit.StopOverBudget(r.cfg.Bounds.maxBudget()),
		),
		Middleware: []core.Middleware{
			agentkit.RetryMiddleware(agentkit.RetryOptions{MaxAttempts: r.cfg.Bounds.maxAttempts()}),
		},
	}
	if maxTokens > 0 {
		cfg.MaxTokens = &maxTokens
	}
	if temperature > 0 {
		cfg.Temperature = &temperature
	}

	registered, err := r.registeredTools(p)
	if err != nil {
		return nil, nil, err
	}

	counter := &blockCounter{}
	if hasShell(registered) {
		// A shell reached the resolved set, so an interceptor is mandatory:
		// AgentKit fails any such run with ErrUnguardedExecute before the
		// first request. The guard is the answer to that, not a way around
		// it — it narrows the shipped restricted policy with the rules this
		// program cares about.
		cfg.BeforeToolCall = Guard(GuardOptions{
			Programs:       p.Programs,
			AllowOperators: !p.ReadOnly,
			ReadOnlyFiles:  p.ReadOnly,
			ProtectedPaths: p.ProtectedPaths,
			ResolvePath:    r.cfg.Workspace.Resolve,
			OnBlock: func(msg string) {
				counter.inc()
				r.detail("blocked %s", msg)
			},
		})
	} else {
		// BeforeToolCall is deliberately nil. AgentKit's own guard then fails
		// any run in which a shell survived the policy, so the nil is the
		// check that the exclusions below actually held rather than an
		// omission.
		cfg.ToolPolicy = core.ToolPolicy{ExcludeTools: MutatingTools}
		if err := AssertReadOnly(agentkit.ResolveToolPolicy(registered, cfg.ToolPolicy)); err != nil {
			return nil, nil, newError(p.Name, CategoryInternal, err, "%v", err)
		}
	}

	// Compaction. A phase that reads a dozen large files fills the context
	// window before it reaches its terminating tool, and the run then ends on
	// a provider error rather than on a result. The transform binds the
	// agent's own history, so the history is made first and handed to the
	// constructor.
	history := core.NewConversationHistory()
	r.installCompaction(&cfg, history)

	agent, err := agentkit.NewAgentWithHistory(cfg, history)
	if err != nil {
		return nil, nil, newError(p.Name, CategoryInternal, err, "building the agent: %v", err)
	}
	for _, t := range registered {
		if err := agent.RegisterTool(t); err != nil {
			return nil, nil, newError(p.Name, CategoryInternal, err,
				"registering tool %s: %v", t.Name, err)
		}
	}
	if p.LoadProjectContext {
		if blocks := r.promptBlocks(agent, p); len(blocks) > 0 {
			if err := agent.SetPromptBlocks(blocks); err != nil {
				return nil, nil, newError(p.Name, CategoryInternal, err, "%v", err)
			}
		}
	}
	return agent, counter, nil
}

// registeredTools is the set one phase declares: its own tools, plus the
// named built-ins when a workspace exists to root them.
func (r *Runner) registeredTools(p Phase) ([]core.Tool, error) {
	out := append([]core.Tool(nil), p.Custom...)
	if len(p.BuiltinTools) == 0 {
		return out, nil
	}
	if r.cfg.Workspace == nil {
		return nil, newError(p.Name, CategoryInternal, nil,
			"phase asks for built-in tools but no workspace is configured")
	}
	built, err := tools.All(tools.Options{Workspace: r.cfg.Workspace})
	if err != nil {
		return nil, newError(p.Name, CategoryInternal, err, "building the file tools: %v", err)
	}
	return append(out, SelectTools(built, p.ReadOnly, p.BuiltinTools...)...), nil
}

// installCompaction summarizes the transcript in place once it passes a
// fraction of the model's context window. The summarizer calls the provider
// directly, off the middleware path, which is why it needs the provider
// rather than the agent. A model with no registered provider gets no
// compaction; the run fails on the missing provider anyway.
func (r *Runner) installCompaction(cfg *core.AgentConfig, history *core.ConversationHistory) {
	p, ok := cfg.Providers.Get(cfg.Model.API)
	if !ok {
		return
	}
	client := core.ClientFunc(p.Stream)
	cfg.TransformContext = agentkit.NewContextTransform(agentkit.CompactionDeps{
		Strategy:       agentkit.SummarizationCompaction{ThresholdFraction: compactionThreshold},
		Summarizer:     agentkit.ModelSummarizer(client, cfg.Model, compactionReserveTokens),
		TurnSummarizer: agentkit.ModelTurnSummarizer(client, cfg.Model, compactionReserveTokens),
		History:        history,
		Model:          cfg.Model,
		OnError:        func(err error) { r.detail("compaction: %v", err) },
	})
}

// promptBlocks assembles the skills and project-context section. Discovery is
// an affirmative act, so nothing happens without a work directory, and the
// project tier of it happens only under TrustProject.
func (r *Runner) promptBlocks(agent *agentkit.Agent, p Phase) []string {
	if r.cfg.WorkDir == "" {
		return nil
	}
	cfg := agentkit.SkillsConfigFor(core.AgentConfig{TrustProject: r.cfg.TrustProject}, r.cfg.WorkDir, "")
	selected := agent.LoadSkills(skills.Discover(cfg), p.Name, "", cfg)
	files, _ := skills.DiscoverContext(cfg)
	if len(selected) == 0 && len(files) == 0 {
		return nil
	}
	return agentkit.SkillBlocks(selected, files, agent.Tools())
}

func (r *Runner) trace(e core.Event) {
	switch v := e.(type) {
	case core.ToolCallEndEvent:
		r.detail("→ %s %s", v.Block.Name, firstLine(string(v.Block.Input), 100))
	case core.ToolExecutionEndEvent:
		status := "ok"
		if v.IsError {
			status = "ERROR"
		}
		r.detail("← %s: %s (%dms)", v.Name, status, v.ElapsedMS)
	case core.ToolResultEvent:
		if v.Message.IsError {
			r.detail("   %s", firstLine(v.Message.Content.Text(), 160))
		}
	case core.TextDeltaEvent:
		if r.cfg.ShowText && r.cfg.Observer != nil {
			r.cfg.Observer.Raw(v.Delta)
		}
	case core.TextEndEvent:
		if r.cfg.ShowText && r.cfg.Observer != nil {
			r.cfg.Observer.Raw("\n")
		}
	case core.ErrorEvent:
		r.detail("[stream error] %s", v.Message)
	}
}

func (r *Runner) detail(format string, args ...any) {
	if r.cfg.Observer != nil {
		r.cfg.Observer.Detail(format, args...)
	}
}

// wrap turns a finished run into this package's error vocabulary. The
// categories are derived from the RunStopReason the loop set, not from a
// stop_reason string parsed out of one response.
func (r *Runner) wrap(p Phase, res core.RunResult, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return newError(p.Name, CategoryAborted, err, "%v", err)
	}
	var already *Error
	if errors.As(err, &already) {
		return already
	}

	category := CategoryAPI
	switch res.StopReason {
	case core.RunStopMaxTurns:
		category = CategoryMaxTurns
	case core.RunStopBudgetExceeded:
		category = CategoryBudget
	case core.RunStopAborted:
		category = CategoryAborted
	}
	if errors.Is(err, core.ErrUnguardedExecute) {
		category = CategoryInternal
	}
	return newError(p.Name, category, err, "%v", err)
}

// NoResultError is the error a caller reports when a phase finished cleanly
// but never called its terminating tool. It is separated from a transport
// failure because the remedy is different: raise the bounds, or narrow the
// task.
func NoResultError(phase, terminator string, res Result) error {
	hint := "raise --max-turns or --budget, or narrow the input"
	switch res.StopReason {
	case core.RunStopBudgetExceeded:
		hint = "raise --budget, or choose a cheaper model"
	case core.RunStopMaxTurns:
		hint = "raise --max-turns; the model was still working"
	}
	return newError(phase, CategoryNoResult, nil,
		"the phase ended (%s) without calling %s: %s", res.StopReason, terminator, hint)
}

func firstLine(s string, limit int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if limit > 0 && len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}

// blockCounter counts guard refusals across the goroutines the loop uses to
// execute tool calls.
type blockCounter struct {
	mu sync.Mutex
	n  int
}

func (b *blockCounter) inc() {
	b.mu.Lock()
	b.n++
	b.mu.Unlock()
}

func (b *blockCounter) count() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.n
}
