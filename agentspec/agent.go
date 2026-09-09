package agentspec

import (
	"context"
	"fmt"

	"github.com/agentfox/agentkit-go/core"

	"github.com/agent-fox-dev/agentfox/afspec"
)

// Per-phase output caps, clamped to the model's own ceiling by the catalog.
const (
	assessMaxTokens   = 4096
	refineMaxTokens   = 16384
	generateMaxTokens = 65536
)

// specTemperature is low because every phase produces a structured artifact
// that is then validated: creativity here shows up as a schema violation.
// A model whose catalog row rejects sampling parameters has them dropped by
// its provider rather than by a branch here.
const specTemperature = 0.2

// agentOptions holds the resolved configuration from AgentOption functional options.
type agentOptions struct {
	specLandscape       []map[string]any
	dependentInterfaces []map[string]any
	projectDir          string
	onArtifact          func(name string, content any)
}

// AgentOption is a functional option type for SpecAgent methods.
type AgentOption func(*agentOptions)

// WithSpecLandscape returns an AgentOption that sets the spec landscape slice.
func WithSpecLandscape(landscape []map[string]any) AgentOption {
	return func(o *agentOptions) {
		o.specLandscape = landscape
	}
}

// WithDependentInterfaces returns an AgentOption that sets the dependent
// spec interfaces slice.
func WithDependentInterfaces(interfaces []map[string]any) AgentOption {
	return func(o *agentOptions) {
		o.dependentInterfaces = interfaces
	}
}

// WithProjectDir returns an AgentOption that sets the project directory.
func WithProjectDir(dir string) AgentOption {
	return func(o *agentOptions) {
		o.projectDir = dir
	}
}

// WithOnArtifact returns an AgentOption that sets a callback invoked after
// each artifact is successfully generated. A nil callback is stored and
// skipped during invocation without panicking.
func WithOnArtifact(fn func(name string, content any)) AgentOption {
	return func(o *agentOptions) {
		o.onArtifact = fn
	}
}

// applyOptions resolves a slice of AgentOption values into an agentOptions struct.
func applyOptions(opts []AgentOption) agentOptions {
	var o agentOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// SpecAgent runs the three model-facing stages of spec authoring: AssessPRD,
// RefinePRD and GenerateArtifacts.
//
// It holds configuration, not a connection. Each stage builds its own agent
// with its own tool set, bounds and model, because the three have genuinely
// different shapes — an assessment is one judgement, a generation is three
// dependent artifacts each of which may need correcting — and sharing a
// transcript between them would carry the PRD critique into the requirements
// the critique was supposed to improve.
type SpecAgent struct {
	modelTier    string
	modelVariant string
	opts         RunOptions
}

// NewSpecAgent creates a SpecAgent with the given model tier string.
// An optional model variant may be provided for variant-aware tier resolution.
func NewSpecAgent(modelTier string, modelVariant ...string) *SpecAgent {
	a := &SpecAgent{modelTier: modelTier}
	if len(modelVariant) > 0 {
		a.modelVariant = modelVariant[0]
	}
	return a
}

// NewSpecAgentWith creates a SpecAgent with explicit run options: which vendor
// the tiers resolve against, whether the model may read the codebase, the
// bounds on a phase, and which providers serve it.
func NewSpecAgentWith(modelTier, modelVariant string, o RunOptions) *SpecAgent {
	return &SpecAgent{modelTier: modelTier, modelVariant: modelVariant, opts: o}
}

// RunOptions reports the options this agent was built with.
func (sa *SpecAgent) RunOptions() RunOptions { return sa.opts }

// AssessPRD judges a PRD's quality and returns the assessment the model
// submitted.
func (sa *SpecAgent) AssessPRD(ctx context.Context, prdText, specName string, opts ...AgentOption) (Assessment, error) {
	o := applyOptions(opts)

	systemPrompt, err := AssessmentSystemPrompt(o.projectDir)
	if err != nil {
		return Assessment{}, promptError("AssessPRD", "system", err)
	}
	userPrompt, err := AssessmentUserPrompt(prdText, specName, o.projectDir, o.specLandscape)
	if err != nil {
		return Assessment{}, promptError("AssessPRD", "user", err)
	}

	var assessment Assessment
	var submitted bool

	res, err := sa.run(ctx, phase{
		name:        "assess",
		model:       sa.modelTier,
		variant:     sa.modelVariant,
		system:      systemPrompt,
		user:        userPrompt,
		submit:      submitAssessmentTool(&assessment, &submitted),
		maxTokens:   assessMaxTokens,
		temperature: specTemperature,
	})
	if err != nil {
		return Assessment{}, err
	}
	if !submitted {
		return Assessment{}, unsubmitted("AssessPRD", ToolSubmitAssessment, res)
	}
	return assessment, nil
}

// RefinePRD rewrites a PRD from the author's answers and returns the new text
// together with a fresh assessment of it.
func (sa *SpecAgent) RefinePRD(ctx context.Context, prdText string, answers map[string]string, prevAssessment Assessment, opts ...AgentOption) (string, Assessment, error) {
	o := applyOptions(opts)

	systemPrompt, err := RefinementSystemPrompt(o.projectDir)
	if err != nil {
		return "", Assessment{}, promptError("RefinePRD", "system", err)
	}
	userPrompt, err := RefinementUserPrompt(prdText, answers, prevAssessment, o.projectDir, o.specLandscape)
	if err != nil {
		return "", Assessment{}, promptError("RefinePRD", "user", err)
	}

	var updatedPRD string
	var assessment Assessment
	var submitted bool

	res, err := sa.run(ctx, phase{
		name:        "refine",
		model:       sa.modelTier,
		variant:     sa.modelVariant,
		system:      systemPrompt,
		user:        userPrompt,
		submit:      submitPRDUpdateTool(&updatedPRD, &assessment, &submitted),
		maxTokens:   refineMaxTokens,
		temperature: specTemperature,
	})
	if err != nil {
		return "", Assessment{}, err
	}
	if !submitted {
		return "", Assessment{}, unsubmitted("RefinePRD", ToolSubmitPRDUpdate, res)
	}
	return updatedPRD, assessment, nil
}

// GenerateArtifacts generates the three JSON artifacts in the order format v2
// §12.1 mandates — requirements, then test_spec, then tasks — passing each
// step the complete artifacts produced before it. Returns the three artifacts
// in a map on success.
//
// The v1 pipeline ran test_spec and tasks concurrently off a summary of
// requirement IDs. The task generator therefore never saw a test ID and could
// not own the tests it was supposed to own; in all eight archived v1 specs
// every edge-case and property test ended up owned by no task. Sequential
// generation with the full upstream artifact is what removes that failure, and
// ValidateGenerationStep is what proves it did.
//
// Each step runs the artifact's schema plus every cross-file rule decidable so
// far inside the submit tool, so a violation returns to the model as an error
// on the turn it made the call. A step that never produces a valid artifact
// within the phase's turn budget is a failed generation: it returns an error
// and no partial result (§12.2).
func (sa *SpecAgent) GenerateArtifacts(ctx context.Context, prdText, specID, specName string, opts ...AgentOption) (map[string]any, error) {
	o := applyOptions(opts)

	systemPrompt, err := GenerationSystemPrompt(o.projectDir)
	if err != nil {
		return nil, promptError("GenerateArtifacts", "system", err)
	}

	// partial accumulates the typed artifacts as they are produced, so that
	// each step is validated against everything generated before it.
	partial := afspec.PartialSpec{SpecID: specID, SpecName: specName}

	// priorArtifacts is the raw content passed to the prompt builder, which
	// renders it in full via the library renderer.
	priorArtifacts := map[string]any{}
	result := map[string]any{}

	for _, step := range afspec.GenerationSteps {
		content, err := sa.generateArtifact(ctx, step, prdText, specID, systemPrompt, o, priorArtifacts, &partial)
		if err != nil {
			return nil, err
		}

		name := string(step)
		priorArtifacts[name] = content
		result[name] = content

		if o.onArtifact != nil {
			if cbErr := safeCallback(o.onArtifact, name, content); cbErr != nil {
				return nil, cbErr
			}
		}
	}

	return result, nil
}

// generateArtifact runs one generation step.
func (sa *SpecAgent) generateArtifact(
	ctx context.Context,
	step afspec.GenerationStep,
	prdText, specID, systemPrompt string,
	o agentOptions,
	priorArtifacts map[string]any,
	partial *afspec.PartialSpec,
) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name := string(step)

	userPrompt, err := GenerationUserPrompt(
		prdText, name, specID, o.projectDir,
		priorArtifacts, o.dependentInterfaces, o.specLandscape,
	)
	if err != nil {
		return nil, promptError("GenerateArtifacts", "user prompt for "+name, err)
	}

	toolSchema, err := ArtifactSchema(step)
	if err != nil {
		return nil, &AgentError{
			Detail:        fmt.Sprintf("GenerateArtifacts: %v", err),
			ErrorCategory: "internal",
			Cause:         err,
		}
	}

	var content map[string]any
	var submitted bool

	res, err := sa.run(ctx, phase{
		name:        "generate:" + name,
		model:       sa.modelTier,
		variant:     sa.modelVariant,
		system:      systemPrompt,
		user:        userPrompt,
		submit:      submitArtifactTool(step, toolSchema, partial, &content, &submitted),
		maxTokens:   generateMaxTokens,
		temperature: specTemperature,
	})
	if err != nil {
		return nil, err
	}
	if !submitted {
		return nil, unsubmitted("GenerateArtifacts:"+name, ArtifactToolName(step), res)
	}
	return content, nil
}

// unsubmitted is the error for a run that ended without the phase's tool
// producing a result.
//
// It names the RunStopReason because the three ways this happens want three
// different responses from the operator: max_turns means the model kept
// failing validation and the errors are in the transcript, budget_exceeded
// means raise the cap or use a cheaper tier, and end_turn means the model
// answered in prose instead of calling the tool.
func unsubmitted(where, tool string, res core.RunResult) error {
	category := "validation"
	switch res.StopReason {
	case core.RunStopMaxTurns:
		category = "max_turns"
	case core.RunStopBudgetExceeded:
		category = "budget"
	case core.RunStopAborted:
		category = "aborted"
	}
	detail := fmt.Sprintf("%s: the run ended (%s, %d turns) without a %s call",
		where, res.StopReason, res.TurnCount, tool)
	if text := res.FinalText(); text != "" {
		detail += ". The model's last message was: " + firstLine(text)
	}
	return &AgentError{Detail: detail, ErrorCategory: category}
}

func firstLine(s string) string {
	const max = 300
	for i, r := range s {
		if r == '\n' {
			s = s[:i]
			break
		}
	}
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func promptError(where, which string, err error) error {
	return &AgentError{
		Detail:        fmt.Sprintf("%s: failed to load %s prompt: %v", where, which, err),
		ErrorCategory: "internal",
		Cause:         err,
	}
}

// safeCallback invokes a callback function, recovering from panics and
// returning them as errors.
func safeCallback(fn func(string, any), name string, content any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &AgentError{
				Detail:        fmt.Sprintf("OnArtifact callback panicked for %s: %v", name, r),
				ErrorCategory: "internal",
			}
		}
	}()
	fn(name, content)
	return nil
}
