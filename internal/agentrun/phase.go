package agentrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	agentkit "github.com/agentfox/agentkit-go"
	"github.com/agentfox/agentkit-go/compaction"
	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/middleware"
	"github.com/agentfox/agentkit-go/prompt"
	"github.com/agentfox/agentkit-go/skills"
	"github.com/agentfox/agentkit-go/stop"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/project"
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

// ToolCallInfo carries the details of one model tool call. It is the
// single argument to Observer.ToolCall.
type ToolCallInfo struct {
	Phase     string
	Name      string
	Blocked   bool
	Arguments json.RawMessage // compacted JSON; {} when empty; raw text as a JSON string when invalid
	OK        bool
	ExitCode  *int   // present only for shell tools with a readable exit status
	Error     string // present only when OK is false
}

// TurnUsage is one model turn's spend. Input is net of the prompt cache:
// CacheRead and CacheWrite are the tokens read from and written to it, which a
// cached prompt sends on every turn.
type TurnUsage struct {
	CostUSD                              float64
	Input, Output, CacheRead, CacheWrite int64
}

// Observer receives a phase's progress. toolio.Progress satisfies it; a test
// passes nil.
type Observer interface {
	// Detail reports one line of tracing, shown only under --verbose.
	Detail(format string, args ...any)
	// Raw writes model prose verbatim, shown only under --show-text.
	Raw(s string)
	// PhaseStart reports that a model phase begins, with the ceilings its
	// run resolved. task is empty except for impl's per-task phase.
	PhaseStart(phase, task string, maxTurns int, budgetUSD float64)
	// PhaseEnd reports that the phase ended.
	PhaseEnd(phase, stopReason string, turns int, costUSD float64, durationMS int64, toolCalls map[string]int)
	// Turn reports one finished model turn.
	Turn(phase string, turn int, usage TurnUsage)
	// ToolCall reports one model tool call. It is emitted for every call,
	// regardless of --verbose.
	ToolCall(info ToolCallInfo)
	// Text reports the accumulated prose of one model turn, emitted
	// immediately before the turn event with the same turn number.
	Text(phase string, turn int, text string)
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
	// ReadRoots are directories outside the workspace the phases may read but
	// not change — the local targets of the repository's go.mod replaces. The
	// shell's read programs reach them, and the tools say so; the file tools
	// stay confined to the workspace.
	ReadRoots []project.ReadRoot
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
	// Index is the code-search index shared by every phase of this run.
	// When non-nil, registeredTools passes it as tools.Options.Index, and
	// code_search appears in tools.All's result. Nil means no indexed search.
	// Closing it is the caller's job: the Runner only reads it.
	Index tools.Index
}

// Phase is one model-facing step: a system prompt, a user prompt, the tools
// it may call, and the one tool whose arguments are the step's result.
type Phase struct {
	// Name identifies the step in errors, events and the cost report.
	Name string
	// Task labels the phase with the unit of work it serves, for progress
	// events only. It is set only by impl's per-task phase and is empty
	// everywhere else. It is a separate field because Name is the prompt
	// cache key shared across every task of a run.
	Task string
	// System and User are the two prompts.
	System string
	User   string
	// RepoMap is the repository map the tool put into User. The runner never
	// injects it (14-REQ-7.3): each tool's prompt function places it among its
	// own blocks. The field exists so a test can assert on the map without
	// parsing User and so the runner can log its size (14-REQ-7.2).
	RepoMap string
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
	// scratch is the phase's scratch directory, relative to the workspace,
	// set by Run for a writing phase. See scratch.go.
	scratch string
	// Suite is the command that runs the project's whole test suite, when
	// the program runs it itself after the phase: the shell refuses it. See
	// GuardOptions.Suite.
	Suite []string
	// TargetedRun is the project's targeted test run, which the refusal of
	// the suite names. See GuardOptions.TargetedRun.
	TargetedRun string
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
	Name string
	// Task is the unit of work the phase served (Phase.Task), empty for a
	// phase that serves the whole run.
	Task       string
	Turns      int
	StopReason core.RunStopReason
	Usage      core.Usage
	Elapsed    time.Duration
	// Blocked counts the tool calls the authorization guard refused. A
	// nonzero count is the guard working; a large one usually means the
	// phase's allowlist is too narrow for the project.
	Blocked int
	// ToolErrors counts the tool calls that came back as errors, keyed
	// "tool" or "tool/code" when the error carries a code. Each one cost the
	// model a turn, and none of it shows in the transcript the report keeps.
	ToolErrors map[string]int
	// ToolCalls counts the number of calls to each tool by name, including
	// blocked calls. Nil when no tool calls were made.
	ToolCalls map[string]int
	// ToolResultBytes sums the byte length of Content.Text() of each
	// ToolResultMessage by tool name. Blocked calls do not contribute.
	// Nil when no tool calls were made.
	ToolResultBytes map[string]int64
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

// Thinking is the reasoning effort this Runner's model runs at; ThinkingUnset
// when none was asked for and the vendor's default applies.
func (r *Runner) Thinking() core.ThinkingLevel { return r.cfg.Thinking }

// ResolvedBounds reports the per-phase ceilings this Runner will apply —
// the tool's own defaults after any --max-turns/--budget override, the same
// defaulting Run applies internally. It is for a caller that needs to
// report a ceiling before any phase runs, such as --preflight's estimate.
func (r *Runner) ResolvedBounds() (maxTurns int, maxBudgetUSD float64) {
	return r.cfg.Bounds.maxTurns(), r.cfg.Bounds.maxBudget()
}

// Run drives one phase to its terminating tool.
//
// The (Result, error) pair is the honest return: a run can end without a
// result — budget, turn limit, a model that answered in prose — and the
// caller has to be able to tell that apart from a finished phase. The
// terminator is what "there is a result" means, and the error names the stop
// reason when there is not one.
func (r *Runner) Run(ctx context.Context, p Phase) (Result, error) {
	if o := r.cfg.Observer; o != nil {
		o.PhaseStart(p.Name, p.Task, r.cfg.Bounds.maxTurns(), r.cfg.Bounds.maxBudget())
	}
	out, err := r.run(ctx, p)
	if o := r.cfg.Observer; o != nil {
		o.PhaseEnd(p.Name, string(out.StopReason), out.Turns, out.Usage.CostUSD, out.Elapsed.Milliseconds(), out.ToolCalls)
	}
	return out, err
}

// run is Run's body; Run reports the phase's start and end around it, so
// every return path is paired.
func (r *Runner) run(ctx context.Context, p Phase) (Result, error) {
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

	// Observer.Detail is shown only under --verbose (14-REQ-7.2).
	if p.RepoMap != "" {
		r.detail("repo map: %d tokens", afspec.EstimateTokens(p.RepoMap))
	}

	// A writing phase has a scratch directory for its lifetime; see
	// scratch.go. It is removed on every way out of this function.
	if !p.ReadOnly && r.cfg.Workspace != nil && writes(p.BuiltinTools) {
		rel, cleanup, err := newScratch(r.cfg.Workspace.Root)
		if err != nil {
			r.detail("no scratch directory: %v", err)
		} else {
			defer cleanup()
			p.scratch = rel
		}
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
	var turn int
	var toolErrs toolErrorCounter
	var tc toolCallCounter
	var tb textBuffer
	pending := make(map[string]core.ToolUseBlock)
	for e := range stream.Events() {
		r.trace(p.Name, &turn, &toolErrs, blocked, &tb, pending, &tc, e)
	}
	// Flush any prose buffered in an interrupted turn (cancellation,
	// stream error) before PhaseEnd.
	tb.flush(r.cfg.Observer, p.Name, turn+1)
	res, runErr := stream.RunResult()

	out := Result{
		Name:            p.Name,
		Task:            p.Task,
		Turns:           res.TurnCount,
		StopReason:      res.StopReason,
		Usage:           res.Usage,
		Elapsed:         time.Since(start),
		Blocked:         blocked.count(),
		ToolErrors:      toolErrs.snapshot(),
		ToolCalls:       tc.snapshotCalls(),
		ToolResultBytes: tc.snapshotBytes(),
	}
	return out, r.wrap(p, res, runErr)
}

// readRootDirs is the read roots' directories, for the guard.
func readRootDirs(roots []project.ReadRoot) []string {
	out := make([]string, len(roots))
	for i, r := range roots {
		out[i] = r.Abs
	}
	return out
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
		// include stop.WhenToolCalled(p.Terminator): that ends the run when
		// the terminator is CALLED, and a call the handler rejects is the
		// repair loop's first step, not its last. The intended ending is the
		// handler's ToolResult.Terminate, which is set on acceptance and not
		// on a rejection — so a malformed submission comes back to the model
		// as an error it can fix, and these two bounds are what stop a phase
		// that never converges.
		StopPolicy: stop.Any(
			stop.AfterTurns(r.cfg.Bounds.maxTurns()),
			stop.OverBudget(r.cfg.Bounds.maxBudget()),
		),
		Middleware: []core.Middleware{
			middleware.Retry(middleware.RetryOptions{MaxAttempts: r.cfg.Bounds.maxAttempts()}),
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

	cfg.SystemPrompt += toolsNote(registered)

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
			Suite:          p.Suite,
			TargetedRun:    p.TargetedRun,
			ReadRoots:      readRootDirs(r.cfg.ReadRoots),
			ResolvePath:    r.cfg.Workspace.Resolve,
			OnBlock: func(name, reason string) {
				counter.inc(name)
				r.detail("blocked %s: %s", name, reason)
			},
		})
	} else {
		// BeforeToolCall is deliberately nil. AgentKit's own guard then fails
		// any run in which a shell survived the policy, so the nil is the
		// check that the exclusions below actually held rather than an
		// omission.
		cfg.ToolPolicy = core.ToolPolicy{ExcludeTools: MutatingTools}
		if err := AssertReadOnly(cfg.ToolPolicy.Resolve(registered)); err != nil {
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
	built, err := tools.All(tools.Options{Workspace: r.cfg.Workspace, Index: r.cfg.Index})
	if err != nil {
		return nil, newError(p.Name, CategoryInternal, err, "building the file tools: %v", err)
	}
	return append(out, noteScratch(noteReadRoots(SelectTools(built, p.ReadOnly, p.Programs, p.BuiltinTools...),
		r.cfg.ReadRoots, p.Programs), p.scratch)...), nil
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
	cfg.TransformContext = compaction.NewContextTransform(compaction.Deps{
		Strategy:       compaction.Summarization{ThresholdFraction: compactionThreshold},
		Summarizer:     compaction.ModelSummarizer(client, cfg.Model, compactionReserveTokens),
		TurnSummarizer: compaction.ModelTurnSummarizer(client, cfg.Model, compactionReserveTokens),
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
	cfg := skills.ConfigFor(core.AgentConfig{TrustProject: r.cfg.TrustProject}, r.cfg.WorkDir, "")
	selected := agent.LoadSkills(skills.Discover(cfg), p.Name, "", cfg)
	files, _ := skills.DiscoverContext(cfg)
	if len(selected) == 0 && len(files) == 0 {
		return nil
	}
	return prompt.SkillBlocks(selected, files, agent.Tools())
}

// trace reports one stream event. turn is the phase's turn counter, which
// the loop's own TurnEndEvent advances. tb accumulates prose for the
// per-turn text event. pending maps tool-call IDs to their ToolUseBlock
// so the result can be matched to its arguments.
func (r *Runner) trace(phase string, turn *int, errs *toolErrorCounter, blocks *blockCounter, tb *textBuffer, pending map[string]core.ToolUseBlock, tc *toolCallCounter, e core.Event) {
	switch v := e.(type) {
	case core.TurnEndEvent:
		*turn++
		// Emit the accumulated text event before the turn event.
		tb.flush(r.cfg.Observer, phase, *turn)
		if r.cfg.Observer != nil {
			r.cfg.Observer.Turn(phase, *turn, TurnUsage{CostUSD: v.Usage.CostUSD, Input: v.Usage.InputTokens,
				Output: v.Usage.OutputTokens, CacheRead: v.Usage.CacheReadTokens, CacheWrite: v.Usage.CacheWriteTokens})
		}
	case core.ToolCallEndEvent:
		r.detail("→ %s %s", v.Block.Name, firstLine(string(v.Block.Input), 100))
		// Remember the block by its tool-call ID so the result can look it up.
		pending[v.Block.ID] = v.Block
	case core.ToolExecutionEndEvent:
		// A refused call was already reported as "blocked <tool>: <reason>";
		// repeating it here as an ERROR would make a refusal look like a
		// tool failure.
		if blocks.isPending(v.Name) {
			break
		}
		status := "ok"
		if v.IsError {
			status = "ERROR"
		}
		r.detail("← %s: %s (%dms)", v.Name, status, v.ElapsedMS)
	case core.ToolResultEvent:
		// Every call ends in exactly one result, so this is where the one
		// tool_call event per call is emitted, with whether it was refused.
		refused := blocks.take(v.Message.ToolName)
		// Look up the call's arguments by tool-call ID, not by tool name,
		// because calls can run in parallel.
		block, found := pending[v.Message.ToolUseID]
		if found {
			delete(pending, v.Message.ToolUseID)
		}
		r.toolCall(phase, v.Message, block, found, refused)
		// Count every call, including blocked ones.
		tc.addCall(v.Message.ToolName)
		// Count bytes only for non-blocked calls. A blocked call's result
		// is the guard's refusal message, already counted as a tool error.
		if !refused {
			tc.addBytes(v.Message.ToolName, int64(len(v.Message.Content.Text())))
		}
		if v.Message.IsError && !refused {
			// Counted here and not at ToolExecutionEndEvent: a call to a tool
			// that does not exist never executes, and still costs a turn.
			errs.add(v.Message.ToolName, v.Message.Content.Text())
			r.detail("   %s", firstLine(v.Message.Content.Text(), 160))
		}
	case core.TextStartEvent:
		// Mark the start of a new text block; consecutive blocks within a
		// turn are joined with "\n".
		tb.startBlock()
	case core.TextDeltaEvent:
		// Always accumulate prose for the per-turn text event.
		tb.appendDelta(v.Delta)
		// Stream live prose to the observer under --show-text.
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

// toolCall reports one model tool call to the observer. It is emitted for
// every call, regardless of --verbose.
func (r *Runner) toolCall(phase string, msg core.ToolResultMessage, block core.ToolUseBlock, hasBlock, refused bool) {
	if r.cfg.Observer == nil {
		return
	}
	info := ToolCallInfo{
		Phase:   phase,
		Name:    msg.ToolName,
		Blocked: refused,
		OK:      !msg.IsError,
	}
	// Arguments from the matched ToolCallEndEvent.
	if hasBlock {
		info.Arguments = compactArguments(block.Input)
	} else {
		info.Arguments = json.RawMessage(`{}`)
	}
	// Exit code only for shell tools with a readable status. A command that
	// ran and exited non-zero is a tool call that worked: its exit code says
	// how the command ended, and its output is not an error. ok and error are
	// left for a refusal and for a call that did not run to an exit — killed,
	// timed out, never started — or a tool that failed (see
	// docs/errata/tool_call_exit_status.md).
	if isShellTool(msg.ToolName) && !refused {
		if code, ok := parseExitCode(msg.Content.Text(), msg.IsError); ok {
			info.ExitCode = &code
			info.OK = true
		}
	}
	// Error text when the call failed; for a refused call, the guard's own
	// message rather than the SDK's JSON envelope around it.
	if !info.OK {
		info.Error = msg.Content.Text()
		if refused {
			info.Error = guardMessage(info.Error)
		}
	}
	r.cfg.Observer.ToolCall(info)
}

// compactArguments compacts valid JSON, returns {} for empty input, and
// encodes invalid JSON as a JSON string so the event line is never dropped.
func compactArguments(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		// Invalid JSON: encode the raw text as a JSON string.
		encoded, _ := json.Marshal(string(raw))
		return json.RawMessage(encoded)
	}
	return json.RawMessage(buf.Bytes())
}

// isShellTool reports whether the tool is one of the shell tools whose
// result may carry an exit code.
func isShellTool(name string) bool {
	switch name {
	case "execute", "run_command", "powershell":
		return true
	}
	return false
}

// exitTrailer matches the status line AgentKit appends to a shell command's
// output when it exited non-zero: `[exit 3]`.
var exitTrailer = regexp.MustCompile(`^\[exit (-?\d+)\]$`)

// parseExitCode reads a shell call's exit status from its result, as the
// model is shown it. AgentKit renders that as the command's own output
// followed by one status line only when there is something to say, so a
// result is read in the order it can take:
//
//   - the JSON envelope {"data":{"exit_code":N}}, which is what a result with
//     no output is shown as;
//   - for a failed call, a final `[exit N]` line. `[killed by signal N]`,
//     `[timeout ...]` and `[aborted]` carry no exit code, and a failed call
//     with no trailer (the command could not be started) has none to read;
//   - for a call that did not fail, no line at all: a command that exited 0
//     adds nothing to its output, so a result that is not an error and has no
//     envelope is an exit 0.
//
// It returns (code, true) when the status is readable and (0, false)
// otherwise, and never guesses: an unreadable status is absent, not 0.
func parseExitCode(resultText string, isError bool) (int, bool) {
	var envelope struct {
		Data struct {
			ExitCode *json.Number `json:"exit_code"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(resultText), &envelope) == nil && envelope.Data.ExitCode != nil {
		n, err := envelope.Data.ExitCode.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	}
	if !isError {
		return 0, true
	}
	last := resultText[strings.LastIndex(resultText, "\n")+1:]
	if m := exitTrailer.FindStringSubmatch(last); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n, true
		}
	}
	return 0, false
}

// guardMessage is the reason a guard gave for refusing a call. AgentKit shows
// the model a refusal as {"detail":"<reason>","error":"blocked_by_policy",
// "ok":false}; any other text is returned as it is.
func guardMessage(text string) string {
	var envelope struct {
		Error  string `json:"error"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal([]byte(text), &envelope) == nil && envelope.Error == core.BlockErrorCode && envelope.Detail != "" {
		return envelope.Detail
	}
	return text
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

// RunStopReason aliases core.RunStopReason for tool-level callers.
type RunStopReason = core.RunStopReason

const (
	// RunStopBudgetExceeded signals a phase reached its per-phase spend ceiling.
	RunStopBudgetExceeded = core.RunStopBudgetExceeded
	// RunStopMaxTurns signals a phase reached its per-phase turn ceiling.
	RunStopMaxTurns = core.RunStopMaxTurns
)

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
	err := newError(phase, CategoryNoResult, nil,
		"the phase ended (%s) without calling %s: %s", res.StopReason, terminator, hint)
	err.StopReason = res.StopReason
	return err
}

// textBuffer accumulates the prose of one model turn. Consecutive text
// blocks within a turn are joined with "\n", with no trailing newline.
type textBuffer struct {
	buf       strings.Builder
	hasBlocks bool // true once at least one text block has started
}

// startBlock marks the beginning of a new text block. If there is already
// accumulated text from a previous block, a "\n" separator is inserted.
func (tb *textBuffer) startBlock() {
	if tb.hasBlocks && tb.buf.Len() > 0 {
		tb.buf.WriteByte('\n')
	}
	tb.hasBlocks = true
}

// appendDelta adds a text delta to the current block.
func (tb *textBuffer) appendDelta(s string) {
	tb.buf.WriteString(s)
}

// flush emits the accumulated prose as a text event if the buffer is
// non-empty, then resets it. It is called at turn end (with the turn number)
// and at phase end (with completed turns + 1 for an interrupted turn).
func (tb *textBuffer) flush(obs Observer, phase string, turn int) {
	if tb.buf.Len() == 0 {
		return
	}
	text := tb.buf.String()
	tb.buf.Reset()
	tb.hasBlocks = false
	if obs != nil {
		obs.Text(phase, turn, text)
	}
}

func firstLine(s string, limit int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if limit > 0 && len(s) > limit {
		// Cut on a rune boundary: a byte offset can land inside a multi-byte
		// character and leave invalid UTF-8 in the log.
		cut := limit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		return fmt.Sprintf("%s…[+%d bytes]", s[:cut], len(s)-cut)
	}
	return s
}

// toolErrorCounter counts a phase's tool errors by tool and error code.
type toolErrorCounter struct {
	mu sync.Mutex
	n  map[string]int
}

func (c *toolErrorCounter) add(tool, text string) {
	key := tool
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(text), &body) == nil && body.Error != "" {
		key += "/" + body.Error
	}
	c.mu.Lock()
	if c.n == nil {
		c.n = map[string]int{}
	}
	c.n[key]++
	c.mu.Unlock()
}

func (c *toolErrorCounter) snapshot() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.n) == 0 {
		return nil
	}
	out := make(map[string]int, len(c.n))
	for k, v := range c.n {
		out[k] = v
	}
	return out
}

// blockCounter counts guard refusals across the goroutines the loop uses to
// execute tool calls.
type blockCounter struct {
	mu sync.Mutex
	n  int
	// pending counts, per tool, refusals whose tool result has not been
	// traced yet. A refused call still ends in an errored result; knowing it
	// was a refusal keeps it from being reported a second and third time.
	pending map[string]int
}

func (b *blockCounter) inc(tool string) {
	b.mu.Lock()
	b.n++
	if b.pending == nil {
		b.pending = map[string]int{}
	}
	b.pending[tool]++
	b.mu.Unlock()
}

// isPending reports whether a refusal of tool is awaiting its result.
func (b *blockCounter) isPending(tool string) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pending[tool] > 0
}

// take consumes one pending refusal of tool, reporting whether there was one.
func (b *blockCounter) take(tool string) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pending[tool] == 0 {
		return false
	}
	b.pending[tool]--
	return true
}

func (b *blockCounter) count() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.n
}

// toolCallCounter counts per-tool call counts and byte sums across the
// goroutines the loop uses to execute tool calls.
type toolCallCounter struct {
	mu    sync.Mutex
	calls map[string]int
	bytes map[string]int64
}

func (c *toolCallCounter) addCall(tool string) {
	c.mu.Lock()
	if c.calls == nil {
		c.calls = map[string]int{}
	}
	c.calls[tool]++
	c.mu.Unlock()
}

func (c *toolCallCounter) addBytes(tool string, n int64) {
	c.mu.Lock()
	if c.bytes == nil {
		c.bytes = map[string]int64{}
	}
	c.bytes[tool] += n
	c.mu.Unlock()
}

func (c *toolCallCounter) snapshotCalls() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.calls) == 0 {
		return nil
	}
	out := make(map[string]int, len(c.calls))
	for k, v := range c.calls {
		out[k] = v
	}
	return out
}

func (c *toolCallCounter) snapshotBytes() map[string]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bytes) == 0 {
		return nil
	}
	out := make(map[string]int64, len(c.bytes))
	for k, v := range c.bytes {
		out[k] = v
	}
	return out
}

// toolsNote closes a phase's system prompt with the tools it has, generated
// from the registered set so it cannot drift from it, and says so when none of
// them is a shell. A model that is not told reaches for `bash`: one read-only
// run called it, got an unknown-tool error, and lost a turn.
//
// The tools' own PromptGuidelines are not repeated here: prompt.Build renders
// them, and the search-over-execute line, in a "Guidelines:" block right after
// a custom system prompt, so this note is followed by them (see
// docs/errata/17_tool_guidelines_rendered_by_agentkit.md). A phase with a
// shell and the file tools is told which to use for what (17-REQ-3).
func toolsNote(registered []core.Tool) string {
	sorted := slices.Clone(registered)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	names := make([]string, 0, len(sorted))
	for _, t := range sorted {
		names = append(names, t.Name)
	}
	note := "\n\nYour tools are exactly: " + strings.Join(names, ", ") + ". Calling any other tool is an error."
	shell := shellName(sorted)
	if shell == "" {
		note += " There is no shell: read with the file tools, and do not call `bash`, `execute` or `run_command`."
	} else if line := preferenceLine(sorted, shell); line != "" {
		note += " " + line
	}
	return note
}

// shellName is the registered shell tool's name, or "" when there is none.
func shellName(sorted []core.Tool) string {
	for _, t := range sorted {
		if slices.Contains(ShellTools, t.Name) {
			return t.Name
		}
	}
	return ""
}

// fileToolUses are the built-in file tools a phase with a shell is told to use
// instead of it, each with what it is for, in the order the sentence names them.
var fileToolUses = []struct{ tool, use string }{
	{"read_file", "read files with"},
	{"search_files", "search with"},
	{"find_files", "find files with"},
	{"list_files", "list directories with"},
}

// preferenceLine names the registered file tools and what each is for, and
// limits the shell to git — and, in a phase that writes, to building,
// formatting and testing. A phase with none of the file tools has none.
func preferenceLine(sorted []core.Tool, shell string) string {
	has := func(n string) bool {
		return slices.ContainsFunc(sorted, func(t core.Tool) bool { return t.Name == n })
	}
	var parts []string
	for _, f := range fileToolUses {
		if has(f.tool) {
			parts = append(parts, f.use+" `"+f.tool+"`")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	list := parts[0]
	if len(parts) > 1 {
		list = strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
	list = strings.ToUpper(list[:1]) + list[1:]
	limit := "use `" + shell + "` only for git"
	if has("write_file") || has("edit_file") {
		limit += " and for building, formatting and testing"
	}
	return list + "; " + limit + "."
}
