package agentspec

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	agentkit "github.com/agentfox/agentkit-go"
	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/anthropic"
	"github.com/agentfox/agentkit-go/provider/google"
	"github.com/agentfox/agentkit-go/provider/ollama"
	"github.com/agentfox/agentkit-go/provider/openai"
	"github.com/agentfox/agentkit-go/provider/openairesponses"
	"github.com/agentfox/agentkit-go/skills"
	"github.com/agentfox/agentkit-go/tools"
)

// mutatingTools is the read-only mandate as a list of names rather than a
// sentence in a prompt.
//
// Writing a spec is analysis. Every prompt template in this package says so,
// and a model that ignores the sentence still edits the file; excluding the
// tools is what makes the mandate true, because there is then nothing to call.
// fetch_url is absent for a different reason — it is not in tools.All() to
// begin with, and reaching it takes a second affirmative act this package does
// not make.
var mutatingTools = []string{"write_file", "edit_file", "execute", "run_command", "powershell"}

// ErrNotReadOnly is returned when a mutating tool reaches the resolved set.
var ErrNotReadOnly = errors.New("agentspec: read-only invariant violated")

// RunOptions are the knobs an embedder sets once for a whole spec session.
// The zero value is a legal configuration: no codebase access, no project
// trust, the default bounds, and the first-party providers.
type RunOptions struct {
	// Vendor selects which tier table SIMPLE/STANDARD/ADVANCED resolve
	// against. Empty means DefaultVendor. It has no effect on a model named
	// by id.
	Vendor string

	// Model, when non-nil, is used instead of resolving the phase's tier or
	// spec through the catalog.
	//
	// It is the seam a test drives: paired with a Providers registry holding
	// provider/faux, a whole phase runs with no key, no network and no mock
	// of this package's own types — the assertions are then about what
	// reached the wire, which is what the hand-rolled Doer interface could
	// never show. An embedder that has already resolved a model uses the same
	// field rather than round-tripping it through a spec string.
	Model *core.Model

	// Workspace, when non-nil, gives the model read-only access to the
	// codebase the spec describes: read_file, find_files, search_files and
	// the rest of the non-mutating built-ins, every path resolved against
	// this root with symlinks followed.
	//
	// It is nil by default and that is deliberate rather than conservative.
	// Reading a repository costs turns and tokens the caller has not asked
	// for, and a spec written from a PRD alone is the behaviour every
	// existing caller of this package has.
	Workspace *tools.Workspace

	// TrustProject admits skills and context files authored inside the
	// working directory — AGENTS.md, CLAUDE.md, .specs/steering.md — into the
	// system prompt. Its zero value is false because a repository that is
	// merely the current directory would otherwise author part of the prompt
	// that reads it (REQ-SKILL-12).
	TrustProject bool

	// WorkDir is where skills and context files are discovered. Empty means
	// the workspace root when there is one, and no discovery otherwise.
	WorkDir string

	// MaxTurns bounds one phase. Zero means DefaultMaxTurns.
	//
	// It is the replacement for the old maxRepairs constant. A validation
	// failure is no longer a hand-assembled second conversation; it is a tool
	// error the loop appends, so "how many attempts does the model get" and
	// "how many turns may the phase take" became the same number.
	MaxTurns int

	// MaxBudgetUSD bounds one phase's spend. Zero means DefaultMaxBudgetUSD.
	MaxBudgetUSD float64

	// MaxAttempts is how many times a single model call is retried on a
	// transient failure. Zero means DefaultMaxAttempts. One means no retries.
	MaxAttempts int

	// Providers overrides the wire implementations. Nil means the five
	// first-party ones. A test supplies provider/faux here and needs no
	// network, no key and no mock of this package's own types.
	Providers core.ProviderRegistry

	// OnEvent, when non-nil, receives every loop event: which tool the model
	// called, what came back, what it is thinking. It is how a CLI shows
	// progress that is not a spinner.
	OnEvent func(core.Event)
}

// Bounds that apply when RunOptions leaves them at zero.
const (
	// DefaultMaxTurns is generous because a phase with a workspace spends
	// most of its turns reading, and stingy enough that a model looping on a
	// validation error it cannot fix stops costing money.
	DefaultMaxTurns = 40
	// DefaultMaxBudgetUSD bounds one phase, not a whole spec.
	DefaultMaxBudgetUSD = 5.00
	// DefaultMaxAttempts retries a transient provider failure twice.
	//
	// AgentKit's own default is 1 — no retries — on the argument that a
	// hidden retry spends money the caller did not authorize. This package
	// asks for three because the phase it wraps is a single long call a human
	// is waiting on, and the ladder it replaces (2s/30s/60s) already retried
	// three times.
	DefaultMaxAttempts = 3
)

func (o RunOptions) maxTurns() int {
	if o.MaxTurns > 0 {
		return o.MaxTurns
	}
	return DefaultMaxTurns
}

func (o RunOptions) maxBudget() float64 {
	if o.MaxBudgetUSD > 0 {
		return o.MaxBudgetUSD
	}
	return DefaultMaxBudgetUSD
}

func (o RunOptions) maxAttempts() int {
	if o.MaxAttempts > 0 {
		return o.MaxAttempts
	}
	return DefaultMaxAttempts
}

func (o RunOptions) workDir() string {
	if o.WorkDir != "" {
		return o.WorkDir
	}
	if o.Workspace != nil {
		return o.Workspace.Root
	}
	return ""
}

// DefaultProviders returns a registry holding every first-party wire API.
//
// All five are registered rather than only Anthropic's, because the model is
// resolved from a catalog spec the operator writes and refusing to serve
// "openai/gpt-6-astra" after resolving it would be a failure two layers away
// from its cause. Registration is a pure function returning a fresh map;
// nothing is installed by import side effect.
func DefaultProviders() core.ProviderRegistry {
	reg := agentkit.DefaultProviders()
	reg.Register(anthropic.Provider(anthropic.Options{}))
	reg.Register(openai.Provider(openai.Options{}))
	reg.Register(openairesponses.Provider(openairesponses.Options{}))
	reg.Register(google.Provider(google.Options{}))
	reg.Register(ollama.Provider(ollama.Options{}))
	return reg
}

// phase is one model-facing step: a system prompt, a user prompt, and the one
// tool whose arguments are the step's result.
type phase struct {
	// name identifies the step in errors and events ("assess",
	// "generate:requirements").
	name string
	// model is the tier name or catalog spec this step runs on.
	model   string
	variant string

	system string
	user   string

	// submit is the terminating tool. Its handler is where the step's output
	// is decoded, validated and kept; the loop is what feeds a rejection back
	// to the model.
	submit core.Tool
	// extra are further tools the step may call, none of which terminate.
	extra []core.Tool

	maxTokens   int
	temperature float64
}

// run configures an agent for one phase and drives it to a result.
//
// Everything the old AICall did by hand happens here by configuration:
// credential resolution and the wire body are the provider's, retry is
// middleware, prompt caching is the provider's cache prefix over a system
// prompt that no longer changes between attempts, and the repair loop is the
// loop.
func (sa *SpecAgent) run(ctx context.Context, p phase) (core.RunResult, error) {
	if err := ctx.Err(); err != nil {
		return core.RunResult{}, err
	}

	// A retired platform variable is checked before the model is resolved,
	// because the answer it changes is which service the request goes to and
	// the operator should learn that before paying for a prompt.
	if sa.opts.Model == nil {
		if err := CheckRetiredPlatformVars(); err != nil {
			return core.RunResult{}, err
		}
	}

	model := sa.opts.Model
	var thinking core.ThinkingLevel
	if model == nil {
		resolved, tl, err := ResolveModel(p.model, p.variant, sa.opts.Vendor)
		if err != nil {
			return core.RunResult{}, &AgentError{
				Detail:        err.Error(),
				ErrorCategory: "model",
				Cause:         err,
			}
		}
		model = resolved
		thinking = tl
		if err := CheckCredentials(model); err != nil {
			return core.RunResult{}, err
		}
	}

	providers := sa.opts.Providers
	if providers == nil {
		providers = DefaultProviders()
	}

	maxTokens := p.maxTokens
	cfg := core.AgentConfig{
		Model:         model,
		Provider:      model.Provider,
		Providers:     providers,
		SystemPrompt:  p.system,
		MaxTokens:     &maxTokens,
		Temperature:   &p.temperature,
		ThinkingLevel: thinking,
		// SessionID is the phase name, not a per-run identifier. On the
		// OpenAI wires it becomes prompt_cache_key, whose job is to group
		// requests that share a prefix so they land on a backend holding it —
		// and every "generate:requirements" call in the world shares this
		// package's system prompt and tool schema. Making it unique per spec
		// would fragment exactly the cache it exists to help.
		SessionID: p.name,
		StopPolicy: agentkit.StopAny(
			agentkit.StopAfterTurns(sa.opts.maxTurns()),
			agentkit.StopOverBudget(sa.opts.maxBudget()),
		),
		Middleware: []core.Middleware{
			agentkit.RetryMiddleware(agentkit.RetryOptions{MaxAttempts: sa.opts.maxAttempts()}),
		},
		// BeforeToolCall is deliberately nil. AgentKit fails any run whose
		// resolved set carries a shell tool and no interceptor
		// (ErrUnguardedExecute), so the nil is the check that the read-only
		// policy below actually held, not an omission.
		TrustProject: sa.opts.TrustProject,
	}

	registered, err := sa.resolveTools(p)
	if err != nil {
		return core.RunResult{}, err
	}
	cfg.ToolPolicy = core.ToolPolicy{ExcludeTools: mutatingTools}

	if err := assertReadOnly(agentkit.ResolveToolPolicy(registered, cfg.ToolPolicy)); err != nil {
		return core.RunResult{}, err
	}

	agent, err := agentkit.NewAgent(cfg)
	if err != nil {
		return core.RunResult{}, &AgentError{
			Detail:        fmt.Sprintf("%s: %v", p.name, err),
			ErrorCategory: "internal",
			Cause:         err,
		}
	}
	for _, t := range registered {
		if err := agent.RegisterTool(t); err != nil {
			return core.RunResult{}, &AgentError{
				Detail:        fmt.Sprintf("%s: registering tool %s: %v", p.name, t.Name, err),
				ErrorCategory: "internal",
				Cause:         err,
			}
		}
	}

	if blocks := sa.promptBlocks(agent); len(blocks) > 0 {
		if err := agent.SetPromptBlocks(blocks); err != nil {
			return core.RunResult{}, &AgentError{
				Detail:        fmt.Sprintf("%s: %v", p.name, err),
				ErrorCategory: "internal",
				Cause:         err,
			}
		}
	}

	if sa.opts.OnEvent == nil {
		res, err := agent.Run(ctx, p.user)
		return res, sa.wrapRun(p, res, err)
	}

	stream, err := agent.Stream(ctx, p.user)
	if err != nil {
		return core.RunResult{}, sa.wrapRun(p, core.RunResult{}, err)
	}
	for e := range stream.Events() {
		sa.opts.OnEvent(e)
	}
	res, err := stream.RunResult()
	return res, sa.wrapRun(p, res, err)
}

// resolveTools is the registered set for one phase: the submit tool, the
// phase's own extras, and — when a workspace was configured — the read-only
// half of the built-in tools.
func (sa *SpecAgent) resolveTools(p phase) ([]core.Tool, error) {
	registered := []core.Tool{p.submit}
	registered = append(registered, p.extra...)

	if sa.opts.Workspace == nil {
		return registered, nil
	}
	built, err := tools.All(tools.Options{Workspace: sa.opts.Workspace})
	if err != nil {
		return nil, &AgentError{
			Detail:        fmt.Sprintf("%s: building the read tools: %v", p.name, err),
			ErrorCategory: "internal",
			Cause:         err,
		}
	}
	return append(registered, built...), nil
}

// promptBlocks assembles the skills and project-context section.
//
// Discovery is an affirmative act, so nothing here happens without a work
// directory, and the project tier of it happens only under TrustProject.
// .specs/steering.md, AGENTS.md and CLAUDE.md are exactly the material a spec
// author is supposed to have read before writing requirements, and until now
// this package read none of them.
func (sa *SpecAgent) promptBlocks(agent *agentkit.Agent) []string {
	dir := sa.opts.workDir()
	if dir == "" {
		return nil
	}
	cfg := agentkit.SkillsConfigFor(core.AgentConfig{TrustProject: sa.opts.TrustProject}, dir, "")
	selected := agent.LoadSkills(skills.Discover(cfg), "spec", "", cfg)
	files, _ := skills.DiscoverContext(cfg)
	if len(selected) == 0 && len(files) == 0 {
		return nil
	}
	return agentkit.SkillBlocks(selected, files, agent.Tools())
}

// wrapRun turns a finished run into this package's error vocabulary.
//
// The categories are the ones AgentError already had and the CLI already
// reports; what changed is that they are now derived from a RunStopReason the
// loop set rather than from a stop_reason string parsed out of one response.
func (sa *SpecAgent) wrapRun(p phase, res core.RunResult, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var agentErr *AgentError
	if errors.As(err, &agentErr) {
		return agentErr
	}

	category := "api"
	switch res.StopReason {
	case core.RunStopMaxTurns:
		category = "max_turns"
	case core.RunStopBudgetExceeded:
		category = "budget"
	case core.RunStopAborted:
		category = "aborted"
	}
	if errors.Is(err, core.ErrUnguardedExecute) {
		category = "internal"
	}
	return &AgentError{
		Detail:        fmt.Sprintf("%s: %v", p.name, err),
		ErrorCategory: category,
		Cause:         err,
	}
}

// assertReadOnly is the invariant, checked in Go before the first request.
//
// It is redundant with the ExcludeTools policy above and with AgentKit's own
// unguarded-shell guard, and that is the point: widen the excludes by mistake
// and the run does not start rather than starting with a shell.
func assertReadOnly(resolved []core.Tool) error {
	deny := make(map[string]bool, len(mutatingTools))
	for _, n := range mutatingTools {
		deny[n] = true
	}
	var found []string
	for _, t := range resolved {
		if deny[t.Name] {
			found = append(found, t.Name)
		}
	}
	if len(found) == 0 {
		return nil
	}
	sort.Strings(found)
	return fmt.Errorf("%w: %s reached the resolved tool set", ErrNotReadOnly, strings.Join(found, ", "))
}
