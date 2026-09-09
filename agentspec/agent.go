package agentspec

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/agent-fox-dev/agentfox/afspec"
)

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

// SpecAgent holds the model tier and implements the AI agent pipeline
// methods: AssessPRD, RefinePRD, and GenerateArtifacts.
type SpecAgent struct {
	modelTier    string
	modelVariant string

	// aiCallFunc is an internal hook for testing. When non-nil, it replaces
	// the real AICall function. Exported tests set this via the unexported
	// field (package-level tests have access).
	aiCallFunc func(ctx context.Context, opts AICallOptions) (string, any, error)
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

// AssessPRD sends a PRD to the LLM with the assessment system prompt and
// submit_assessment tool, returning a validated Assessment.
func (sa *SpecAgent) AssessPRD(ctx context.Context, prdText, specName string, opts ...AgentOption) (Assessment, error) {
	// Apply agent options.
	o := applyOptions(opts)

	// Build system and user prompts.
	systemPrompt, err := AssessmentSystemPrompt(o.projectDir)
	if err != nil {
		return Assessment{}, &AgentError{
			Detail:        fmt.Sprintf("AssessPRD: failed to load system prompt: %v", err),
			ErrorCategory: "internal",
			Cause:         err,
		}
	}

	userPrompt, err := AssessmentUserPrompt(prdText, specName, o.projectDir, o.specLandscape)
	if err != nil {
		return Assessment{}, &AgentError{
			Detail:        fmt.Sprintf("AssessPRD: failed to load user prompt: %v", err),
			ErrorCategory: "internal",
			Cause:         err,
		}
	}

	// Convert assessment tool definitions to Tool structs.
	toolDefs := mapToTools(AssessmentTools())

	// Build AICall options.
	assessTemp := 0.2
	callOpts := AICallOptions{
		ModelTier:    sa.modelTier,
		ModelVariant: sa.modelVariant,
		System:       systemPrompt,
		Messages:     []Message{{Role: "user", Content: userPrompt}},
		Tools:        toolDefs,
		ToolChoice:   map[string]any{"type": "any"},
		Temperature:  &assessTemp,
		MaxTokens:    4096,
		Context:      "AssessPRD",
	}

	// Invoke AICall (or test mock).
	callFn := sa.resolveCallFunc()
	_, raw, err := callFn(ctx, callOpts)
	if err != nil {
		return Assessment{}, wrapCallError(err)
	}

	// Process response.
	resp, ok := raw.(*MessageResponse)
	if !ok {
		return Assessment{}, &AgentError{
			Detail:        "AssessPRD: unexpected response type from AICall",
			ErrorCategory: "internal",
		}
	}

	// Check stop reason for error conditions.
	if err := checkStopReason(resp.StopReason); err != nil {
		return Assessment{}, err
	}

	// Extract submit_assessment tool call.
	toolInput, err := extractToolCall(resp, "submit_assessment")
	if err != nil {
		return Assessment{}, err
	}

	// Parse tool input into Assessment.
	assessment, err := parseAssessment(toolInput)
	if err != nil {
		return Assessment{}, &AgentError{
			Detail:        fmt.Sprintf("AssessPRD: failed to parse assessment: %v", err),
			ErrorCategory: "internal",
			Cause:         err,
		}
	}

	return assessment, nil
}

// RefinePRD sends a PRD with user answers and prior assessment to the LLM,
// returning an updated PRD text and new Assessment.
func (sa *SpecAgent) RefinePRD(ctx context.Context, prdText string, answers map[string]string, prevAssessment Assessment, opts ...AgentOption) (string, Assessment, error) {
	// Apply agent options.
	o := applyOptions(opts)

	// Build system and user prompts.
	systemPrompt, err := RefinementSystemPrompt(o.projectDir)
	if err != nil {
		return "", Assessment{}, &AgentError{
			Detail:        fmt.Sprintf("RefinePRD: failed to load system prompt: %v", err),
			ErrorCategory: "internal",
			Cause:         err,
		}
	}

	userPrompt, err := RefinementUserPrompt(prdText, answers, prevAssessment, o.projectDir, o.specLandscape)
	if err != nil {
		return "", Assessment{}, &AgentError{
			Detail:        fmt.Sprintf("RefinePRD: failed to load user prompt: %v", err),
			ErrorCategory: "internal",
			Cause:         err,
		}
	}

	// Convert refinement tool definitions to Tool structs.
	toolDefs := mapToTools(RefinementTools())

	// Build AICall options.
	refineTemp := 0.2
	callOpts := AICallOptions{
		ModelTier:    sa.modelTier,
		ModelVariant: sa.modelVariant,
		System:       systemPrompt,
		Messages:     []Message{{Role: "user", Content: userPrompt}},
		Tools:        toolDefs,
		ToolChoice:   map[string]any{"type": "any"},
		Temperature:  &refineTemp,
		MaxTokens:    16384,
		Context:      "RefinePRD",
	}

	// Invoke AICall (or test mock).
	callFn := sa.resolveCallFunc()
	_, raw, err := callFn(ctx, callOpts)
	if err != nil {
		return "", Assessment{}, wrapCallError(err)
	}

	// Process response.
	resp, ok := raw.(*MessageResponse)
	if !ok {
		return "", Assessment{}, &AgentError{
			Detail:        "RefinePRD: unexpected response type from AICall",
			ErrorCategory: "internal",
		}
	}

	// Check stop reason for error conditions.
	if err := checkStopReason(resp.StopReason); err != nil {
		return "", Assessment{}, err
	}

	// Extract submit_prd_update tool call.
	prdInput, err := extractToolCall(resp, "submit_prd_update")
	if err != nil {
		return "", Assessment{}, err
	}

	// Parse updated PRD text.
	updatedPRD, err := parsePRDUpdate(prdInput)
	if err != nil {
		return "", Assessment{}, &AgentError{
			Detail:        fmt.Sprintf("RefinePRD: failed to parse PRD update: %v", err),
			ErrorCategory: "internal",
			Cause:         err,
		}
	}

	// Extract submit_assessment from the same response — both tools must
	// appear in a single LLM call; no fallback second call is made.
	assessmentInput, err := extractToolCall(resp, "submit_assessment")
	if err != nil {
		return "", Assessment{}, err
	}

	assessment, parseErr := parseAssessment(assessmentInput)
	if parseErr != nil {
		return "", Assessment{}, &AgentError{
			Detail:        fmt.Sprintf("RefinePRD: failed to parse assessment: %v", parseErr),
			ErrorCategory: "internal",
			Cause:         parseErr,
		}
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
// After each step the pipeline runs the artifact's schema plus every
// cross-file rule decidable so far, and sends any violation back to the model
// as a tool_result for repair. A run that is still invalid after maxRepairs
// attempts is a failed generation: it returns an error and no partial result
// (§12.2).
func (sa *SpecAgent) GenerateArtifacts(ctx context.Context, prdText, specID, specName string, opts ...AgentOption) (map[string]any, error) {
	o := applyOptions(opts)

	systemPrompt, err := GenerationSystemPrompt(o.projectDir)
	if err != nil {
		return nil, &AgentError{
			Detail:        fmt.Sprintf("GenerateArtifacts: failed to load system prompt: %v", err),
			ErrorCategory: "internal",
			Cause:         err,
		}
	}

	callFn := sa.resolveCallFunc()
	temp := 0.2

	// partial accumulates the typed artifacts as they are produced, so that
	// each step can be validated against everything generated before it.
	partial := afspec.PartialSpec{SpecID: specID, SpecName: specName}

	// priorArtifacts is the raw content passed to the prompt builder, which
	// renders it in full via the library renderer.
	priorArtifacts := map[string]any{}
	result := map[string]any{}

	for _, step := range afspec.GenerationSteps {
		artifactName := string(step)

		content, err := sa.generateArtifact(ctx, generateArtifactRequest{
			step:           step,
			prdText:        prdText,
			specID:         specID,
			systemPrompt:   systemPrompt,
			temperature:    &temp,
			callFn:         callFn,
			options:        o,
			priorArtifacts: priorArtifacts,
			partial:        &partial,
		})
		if err != nil {
			return nil, err
		}

		priorArtifacts[artifactName] = content
		result[artifactName] = content

		if o.onArtifact != nil {
			if cbErr := safeCallback(o.onArtifact, artifactName, content); cbErr != nil {
				return nil, cbErr
			}
		}
	}

	return result, nil
}

// maxRepairs is how many times a step may be sent back to the model after a
// validation failure. §12.2 makes inline repair the mechanism that keeps an
// invalid artifact from ever being written.
const maxRepairs = 3

// generateArtifactRequest carries the per-step inputs of generateArtifact.
type generateArtifactRequest struct {
	step           afspec.GenerationStep
	prdText        string
	specID         string
	systemPrompt   string
	temperature    *float64
	callFn         func(ctx context.Context, opts AICallOptions) (string, any, error)
	options        agentOptions
	priorArtifacts map[string]any
	partial        *afspec.PartialSpec
}

// generateArtifact runs one generation step and its repair loop. On success it
// returns the artifact content and has recorded the typed artifact in
// req.partial, so the next step validates against it.
func (sa *SpecAgent) generateArtifact(ctx context.Context, req generateArtifactRequest) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	artifactName := string(req.step)

	userPrompt, err := GenerationUserPrompt(
		req.prdText, artifactName, req.specID, req.options.projectDir,
		req.priorArtifacts, req.options.dependentInterfaces, req.options.specLandscape,
	)
	if err != nil {
		return nil, &AgentError{
			Detail:        fmt.Sprintf("GenerateArtifacts: failed to build prompt for %s: %v", artifactName, err),
			ErrorCategory: "internal",
			Cause:         err,
		}
	}

	toolDefs := mapToTools(ArtifactTool(artifactName))
	toolName := "submit_" + artifactName

	callOpts := AICallOptions{
		ModelTier:    sa.modelTier,
		ModelVariant: sa.modelVariant,
		System:       req.systemPrompt,
		Messages:     []Message{{Role: "user", Content: userPrompt}},
		Tools:        toolDefs,
		ToolChoice:   map[string]any{"type": "any"},
		Temperature:  req.temperature,
		Context:      fmt.Sprintf("GenerateArtifacts:%s", artifactName),
	}

	_, raw, err := req.callFn(ctx, callOpts)
	if err != nil {
		return nil, wrapCallError(err)
	}

	resp, ok := raw.(*MessageResponse)
	if !ok {
		return nil, &AgentError{
			Detail:        fmt.Sprintf("GenerateArtifacts: unexpected response type for %s", artifactName),
			ErrorCategory: "internal",
		}
	}
	if err := checkStopReason(resp.StopReason); err != nil {
		return nil, err
	}

	toolInput, err := extractToolCall(resp, toolName)
	if err != nil {
		toolInput = nil
	}

	content, validErr := validateArtifactContent(toolInput, req.step, req.partial)

	// Repair loop. Each attempt continues the same conversation: the original
	// user prompt, the model's tool_use response, and a tool_result carrying
	// the validation failure. That preserves generation context and lets the
	// system-prompt prefix stay in the prompt cache.
	for repair := 0; repair < maxRepairs && validErr != nil; repair++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		repairMessages := []Message{
			{Role: "user", Content: userPrompt},
			{Role: "assistant", Content: resp.Content},
			{Role: "user", Content: []ContentBlock{{
				Type:      "tool_result",
				ToolUseID: findToolUseID(resp, toolName),
				Text:      validErr.Error(),
			}}},
		}

		repairOpts := AICallOptions{
			ModelTier:    sa.modelTier,
			ModelVariant: sa.modelVariant,
			System:       req.systemPrompt,
			Messages:     repairMessages,
			Tools:        toolDefs,
			ToolChoice:   map[string]any{"type": "any"},
			Temperature:  req.temperature,
			Context:      fmt.Sprintf("GenerateArtifacts:%s:repair:%d", artifactName, repair+1),
		}

		_, raw, err = req.callFn(ctx, repairOpts)
		if err != nil {
			return nil, wrapCallError(err)
		}

		resp, ok = raw.(*MessageResponse)
		if !ok {
			return nil, &AgentError{
				Detail:        fmt.Sprintf("GenerateArtifacts: unexpected response type for %s repair", artifactName),
				ErrorCategory: "internal",
			}
		}
		if err := checkStopReason(resp.StopReason); err != nil {
			return nil, err
		}

		toolInput, err = extractToolCall(resp, toolName)
		if err != nil {
			toolInput = nil
		}

		content, validErr = validateArtifactContent(toolInput, req.step, req.partial)
	}

	if validErr != nil {
		return nil, &AgentError{
			Detail:        fmt.Sprintf("GenerateArtifacts: validation failed for %s after %d repair attempts: %v", artifactName, maxRepairs, validErr),
			ErrorCategory: "validation",
			Cause:         validErr,
		}
	}

	return content, nil
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

// resolveCallFunc returns the AI call function to use — the test mock
// if set, or the real AICall function.
func (sa *SpecAgent) resolveCallFunc() func(ctx context.Context, opts AICallOptions) (string, any, error) {
	if sa.aiCallFunc != nil {
		return sa.aiCallFunc
	}
	return func(ctx context.Context, opts AICallOptions) (string, any, error) {
		return AICall(ctx, opts)
	}
}

// mapToTools converts tool definitions from map format (as returned by
// AssessmentTools, RefinementTools, etc.) to the Tool struct format
// used by AICallOptions.
func mapToTools(defs []map[string]any) []Tool {
	tools := make([]Tool, len(defs))
	for i, def := range defs {
		name, _ := def["name"].(string)
		desc, _ := def["description"].(string)
		tools[i] = Tool{
			Name:        name,
			Description: desc,
			InputSchema: def["input_schema"],
		}
	}
	return tools
}

// checkStopReason checks the LLM response stop reason and returns an
// AgentError for known error stop reasons. Returns nil for acceptable
// stop reasons like "end_turn" or "tool_use".
func checkStopReason(stopReason string) error {
	switch stopReason {
	case "refusal":
		return &AgentError{
			Detail:        "LLM refused the request",
			ErrorCategory: "refusal",
		}
	case "context_window_exceeded":
		return &AgentError{
			Detail:        "context window exceeded",
			ErrorCategory: "context_window",
		}
	case "pause_turn":
		return &AgentError{
			Detail:        "turn paused by the API",
			ErrorCategory: "pause_turn",
		}
	default:
		return nil
	}
}

// findToolUseID returns the ID of the first tool_use ContentBlock whose Name
// matches toolName in resp. Returns an empty string if not found.
func findToolUseID(resp *MessageResponse, toolName string) string {
	if resp == nil {
		return ""
	}
	for _, block := range resp.Content {
		if block.Type == "tool_use" && block.Name == toolName {
			return block.ID
		}
	}
	return ""
}

// extractToolCall finds a tool_use content block with the given name in
// the response and returns its Input. Returns an AgentError with
// category "internal" if the tool call is not found.
func extractToolCall(resp *MessageResponse, toolName string) (any, error) {
	if resp == nil {
		return nil, &AgentError{
			Detail:        fmt.Sprintf("missing %s tool call: nil response", toolName),
			ErrorCategory: "internal",
		}
	}
	for _, block := range resp.Content {
		if block.Type == "tool_use" && block.Name == toolName {
			return block.Input, nil
		}
	}
	return nil, &AgentError{
		Detail:        fmt.Sprintf("missing %s tool call in response", toolName),
		ErrorCategory: "internal",
	}
}

// parseAssessment converts a tool call input (expected to be
// map[string]any) into an Assessment struct.
func parseAssessment(input any) (Assessment, error) {
	m, ok := input.(map[string]any)
	if !ok {
		return Assessment{}, fmt.Errorf("assessment payload is not a map: %T", input)
	}

	var a Assessment

	// Validate the quality enum value (NS-REQ-2, NS-REQ-3).
	quality, _ := m["quality"].(string)
	if quality == "" {
		return Assessment{}, fmt.Errorf("assessment payload missing required 'quality' field")
	}
	validQualities := map[string]bool{
		"ready":            true,
		"needs_refinement": true,
		"incomplete":       true,
	}
	if !validQualities[quality] {
		return Assessment{}, fmt.Errorf(
			"invalid assessment quality %q: must be one of [incomplete, needs_refinement, ready]",
			quality,
		)
	}
	a.Quality = quality

	a.Summary, _ = m["summary"].(string)

	// Parse gaps — could be []string or []any.
	switch g := m["gaps"].(type) {
	case []string:
		a.Gaps = g
	case []any:
		for _, item := range g {
			if s, ok := item.(string); ok {
				a.Gaps = append(a.Gaps, s)
			}
		}
	}

	// Parse questions — could be []map[string]any or []any.
	switch q := m["questions"].(type) {
	case []map[string]any:
		a.Questions = q
	case []any:
		for _, item := range q {
			if qm, ok := item.(map[string]any); ok {
				a.Questions = append(a.Questions, qm)
			}
		}
	}

	return a, nil
}

// parsePRDUpdate extracts the updated_prd string from a submit_prd_update
// tool call input.
func parsePRDUpdate(input any) (string, error) {
	m, ok := input.(map[string]any)
	if !ok {
		return "", fmt.Errorf("PRD update payload is not a map: %T", input)
	}
	prd, ok := m["updated_prd"].(string)
	if !ok {
		return "", fmt.Errorf("PRD update payload missing 'updated_prd' string field")
	}
	return prd, nil
}

// wrapCallError ensures that errors from AICall are returned as
// *AgentError. If the error is already an *AgentError, it is returned
// as-is. Other errors are wrapped with category "internal".
func wrapCallError(err error) error {
	var agentErr *AgentError
	if errors.As(err, &agentErr) {
		return agentErr
	}
	return &AgentError{
		Detail:        err.Error(),
		ErrorCategory: "internal",
		Cause:         err,
	}
}

// validateArtifactContent decodes a model's tool input for one generation
// step, runs the artifact's v2 schema and every cross-file rule decidable at
// that point, and records the typed artifact in partial on success.
//
// A step that fails leaves partial untouched, so a repair attempt validates
// against the same upstream artifacts as the attempt before it.
func validateArtifactContent(input any, step afspec.GenerationStep, partial *afspec.PartialSpec) (map[string]any, error) {
	if input == nil {
		return nil, fmt.Errorf("the model returned no %s artifact", step)
	}
	m, ok := input.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("the %s artifact is not a JSON object but a %T", step, input)
	}

	decoded, err := afspec.DecodeArtifact(step, m)
	if err != nil {
		return nil, err
	}

	// Validate a copy of the accumulated state so that a failure cannot leave
	// a rejected artifact behind for the next attempt to validate against.
	candidate := *partial
	switch artifact := decoded.(type) {
	case *afspec.RequirementsV2Json:
		candidate.Requirements = artifact
	case *afspec.TestSpecV2Json:
		candidate.TestSpec = artifact
	case *afspec.TasksV2Json:
		candidate.Tasks = artifact
	}

	if result := afspec.ValidateGenerationStep(step, candidate); !result.Valid {
		return nil, formatValidationEntries(step, result.Errors)
	}

	*partial = candidate
	return m, nil
}

// formatValidationEntries turns validation errors into the tool_result text
// the repair loop sends back to the model. Each line names the rule that
// failed so the model can act on it rather than guess.
func formatValidationEntries(step afspec.GenerationStep, entries []afspec.ValidationEntry) error {
	return fmt.Errorf("the %s artifact has %d validation error(s):\n%s",
		step, len(entries), strings.TrimRight(afspec.FormatValidationEntries(entries), "\n"))
}
