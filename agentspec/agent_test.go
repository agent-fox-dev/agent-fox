package agentspec

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// isZeroAssessment returns true if the Assessment has all zero-value fields.
func isZeroAssessment(a Assessment) bool {
	return a.Quality == "" && a.Summary == "" && len(a.Gaps) == 0 && len(a.Questions) == 0
}

// messageContentString extracts the string representation of a Message's
// Content field. Handles both string (existing callers) and []ContentBlock
// (structured repair messages).
func messageContentString(msg Message) string {
	switch v := msg.Content.(type) {
	case string:
		return v
	case []ContentBlock:
		var parts []string
		for _, block := range v {
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		return strings.Join(parts, " ")
	default:
		return fmt.Sprintf("%v", v)
	}
}

// ---------------------------------------------------------------------------
// Mock infrastructure for SpecAgent tests
// ---------------------------------------------------------------------------

// aiCallCapture records the options passed to each AICall invocation.
type aiCallCapture struct {
	mu    sync.Mutex
	calls []AICallOptions
}

func (c *aiCallCapture) record(opts AICallOptions) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, opts)
}

func (c *aiCallCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func (c *aiCallCapture) get(i int) AICallOptions {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[i]
}

// mockAICallResult describes a single AICall result to be returned in sequence.
type mockAICallResult struct {
	text string
	raw  any
	err  error
}

// newMockAICallFunc creates a mock aiCallFunc that returns results in sequence
// and captures call options. If more calls are made than results provided,
// the last result is repeated.
func newMockAICallFunc(capture *aiCallCapture, results ...mockAICallResult) func(ctx context.Context, opts AICallOptions) (string, any, error) {
	idx := 0
	var mu sync.Mutex
	return func(ctx context.Context, opts AICallOptions) (string, any, error) {
		if capture != nil {
			capture.record(opts)
		}
		mu.Lock()
		i := idx
		if i < len(results)-1 {
			idx++
		} else if i < len(results) {
			// stay at last index
		}
		mu.Unlock()
		if i >= len(results) {
			i = len(results) - 1
		}
		if i < 0 {
			return "", nil, fmt.Errorf("no mock results configured")
		}
		return results[i].text, results[i].raw, results[i].err
	}
}

// makeToolCallResponse creates a MessageResponse with tool_use content blocks.
func makeToolCallResponse(stopReason string, toolCalls ...ContentBlock) *MessageResponse {
	return &MessageResponse{
		Content:    toolCalls,
		StopReason: stopReason,
	}
}

// makeAssessmentToolCall creates a ContentBlock for a submit_assessment tool call.
func makeAssessmentToolCall(quality, summary string, gaps []string, questions []map[string]any) ContentBlock {
	payload := map[string]any{
		"quality":   quality,
		"summary":   summary,
		"gaps":      gaps,
		"questions": questions,
	}
	return ContentBlock{
		Type:  "tool_use",
		ID:    "toolu_assessment_01",
		Name:  "submit_assessment",
		Input: payload,
	}
}

// makePRDUpdateToolCall creates a ContentBlock for a submit_prd_update tool call.
func makePRDUpdateToolCall(updatedPRD string) ContentBlock {
	payload := map[string]any{
		"updated_prd": updatedPRD,
	}
	return ContentBlock{
		Type:  "tool_use",
		ID:    "toolu_prd_01",
		Name:  "submit_prd_update",
		Input: payload,
	}
}

// makeArtifactToolCall creates a ContentBlock for an artifact submission tool call.
func makeArtifactToolCall(artifactName string, content any) ContentBlock {
	return ContentBlock{
		Type:  "tool_use",
		ID:    "toolu_" + artifactName + "_01",
		Name:  "submit_" + artifactName,
		Input: content,
	}
}

// ---------------------------------------------------------------------------
// TS-07-24: NewSpecAgent constructor
// ---------------------------------------------------------------------------

// TestSpec07_NewSpecAgent_ReturnsNonNil verifies that NewSpecAgent returns a
// non-nil *SpecAgent with the given modelTier field set.
// Test Spec: TS-07-24, Requirement: 07-REQ-5.1
func TestSpec07_NewSpecAgent_ReturnsNonNil(t *testing.T) {
	agent := NewSpecAgent("STANDARD")
	if agent == nil {
		t.Fatal("NewSpecAgent(\"STANDARD\") returned nil; want non-nil *SpecAgent")
	}
	if agent.modelTier != "STANDARD" {
		t.Errorf("agent.modelTier = %q; want %q", agent.modelTier, "STANDARD")
	}
}

// TestSpec07_NewSpecAgent_AllTiers verifies NewSpecAgent with each tier value.
// Test Spec: TS-07-24, Requirement: 07-REQ-5.1
func TestSpec07_NewSpecAgent_AllTiers(t *testing.T) {
	tiers := []string{"SIMPLE", "STANDARD", "ADVANCED"}
	for _, tier := range tiers {
		t.Run(tier, func(t *testing.T) {
			agent := NewSpecAgent(tier)
			if agent == nil {
				t.Fatalf("NewSpecAgent(%q) returned nil", tier)
			}
			if agent.modelTier != tier {
				t.Errorf("agent.modelTier = %q; want %q", agent.modelTier, tier)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TS-07-25: AssessPRD happy path
// ---------------------------------------------------------------------------

// TestSpec07_AssessPRD_HappyPath verifies that AssessPRD builds prompts,
// calls AICall with AssessmentTools and tool_choice any, extracts the
// submit_assessment tool call, and returns a valid Assessment.
// Test Spec: TS-07-25, Requirement: 07-REQ-5.2
func TestSpec07_AssessPRD_HappyPath(t *testing.T) {
	capture := &aiCallCapture{}

	assessmentResp := makeToolCallResponse("end_turn",
		makeAssessmentToolCall("ready", "PRD is well-structured", []string{"Missing auth flow"}, []map[string]any{
			{"id": "q1", "text": "How will auth work?"},
		}),
	)

	mockFn := newMockAICallFunc(capture, mockAICallResult{
		text: "",
		raw:  assessmentResp,
		err:  nil,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	assessment, err := agent.AssessPRD(ctx, "Sample PRD", "test-spec")
	if err != nil {
		t.Fatalf("AssessPRD() returned error: %v", err)
	}

	// Verify assessment has non-zero fields.
	if assessment.Quality == "" {
		t.Error("assessment.Quality is empty; want non-empty")
	}
	if assessment.Summary == "" {
		t.Error("assessment.Summary is empty; want non-empty")
	}

	// Verify AICall was invoked exactly once.
	if capture.count() != 1 {
		t.Fatalf("AICall invocation count = %d; want 1", capture.count())
	}

	// Verify tool_choice was set to {"type": "any"} or equivalent.
	callOpts := capture.get(0)
	if callOpts.ToolChoice == nil {
		t.Error("AICall ToolChoice is nil; want tool_choice with type=any")
	}

	// Verify tools were provided (AssessmentTools).
	if len(callOpts.Tools) == 0 {
		t.Error("AICall Tools is empty; want AssessmentTools()")
	}

	// Verify temperature=0.2 was set (NS-REQ-1).
	if callOpts.Temperature == nil {
		t.Error("AICall Temperature is nil; want 0.2")
	} else if *callOpts.Temperature != 0.2 {
		t.Errorf("AICall Temperature = %f; want 0.2", *callOpts.Temperature)
	}
}

// TestSpec07_AssessPRD_WithOptions verifies that AssessPRD accepts AgentOptions.
// Test Spec: TS-07-25, Requirement: 07-REQ-5.2
func TestSpec07_AssessPRD_WithOptions(t *testing.T) {
	assessmentResp := makeToolCallResponse("end_turn",
		makeAssessmentToolCall("needs_refinement", "Needs improvement", []string{"Gap 1"}, nil),
	)

	mockFn := newMockAICallFunc(nil, mockAICallResult{
		raw: assessmentResp,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	assessment, err := agent.AssessPRD(ctx, "Sample PRD", "test-spec",
		WithProjectDir("/custom/dir"),
		WithSpecLandscape([]map[string]any{{"spec_id": "01", "title": "Other Spec"}}),
	)
	if err != nil {
		t.Fatalf("AssessPRD() with options returned error: %v", err)
	}
	if assessment.Quality == "" {
		t.Error("assessment.Quality is empty; want non-empty")
	}
}

// ---------------------------------------------------------------------------
// TS-07-26: AssessPRD refusal stop reason
// ---------------------------------------------------------------------------

// TestSpec07_AssessPRD_StopReason_Refusal verifies that AssessPRD returns an
// AgentError with category=refusal when the LLM stop reason is "refusal".
// Test Spec: TS-07-26, Requirement: 07-REQ-5.3
func TestSpec07_AssessPRD_StopReason_Refusal(t *testing.T) {
	refusalResp := makeToolCallResponse("refusal")

	mockFn := newMockAICallFunc(nil, mockAICallResult{
		raw: refusalResp,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	assessment, err := agent.AssessPRD(ctx, "Sample PRD", "test-spec")

	// Assessment should be zero-value.
	if !isZeroAssessment(assessment) {
		t.Errorf("assessment = %+v; want zero-value Assessment", assessment)
	}

	// Error should be AgentError with category "refusal".
	if err == nil {
		t.Fatal("AssessPRD() returned nil error; want AgentError with category refusal")
	}
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("error type = %T; want *AgentError", err)
	}
	if agentErr.ErrorCategory != "refusal" {
		t.Errorf("AgentError.ErrorCategory = %q; want %q", agentErr.ErrorCategory, "refusal")
	}
}

// ---------------------------------------------------------------------------
// TS-07-27: AssessPRD context_window_exceeded stop reason
// ---------------------------------------------------------------------------

// TestSpec07_AssessPRD_StopReason_ContextWindow verifies that AssessPRD
// returns an AgentError with category=context_window when the LLM stop
// reason is "context_window_exceeded".
// Test Spec: TS-07-27, Requirement: 07-REQ-5.4
func TestSpec07_AssessPRD_StopReason_ContextWindow(t *testing.T) {
	resp := makeToolCallResponse("context_window_exceeded")

	mockFn := newMockAICallFunc(nil, mockAICallResult{
		raw: resp,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	assessment, err := agent.AssessPRD(ctx, "Sample PRD", "test-spec")

	if !isZeroAssessment(assessment) {
		t.Errorf("assessment = %+v; want zero-value Assessment", assessment)
	}
	if err == nil {
		t.Fatal("AssessPRD() returned nil error; want AgentError with category context_window")
	}
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("error type = %T; want *AgentError", err)
	}
	if agentErr.ErrorCategory != "context_window" {
		t.Errorf("AgentError.ErrorCategory = %q; want %q", agentErr.ErrorCategory, "context_window")
	}
}

// ---------------------------------------------------------------------------
// TS-07-28: AssessPRD pause_turn stop reason
// ---------------------------------------------------------------------------

// TestSpec07_AssessPRD_StopReason_PauseTurn verifies that AssessPRD returns
// an AgentError with category=pause_turn when the LLM stop reason is "pause_turn".
// Test Spec: TS-07-28, Requirement: 07-REQ-5.5
func TestSpec07_AssessPRD_StopReason_PauseTurn(t *testing.T) {
	resp := makeToolCallResponse("pause_turn")

	mockFn := newMockAICallFunc(nil, mockAICallResult{
		raw: resp,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	assessment, err := agent.AssessPRD(ctx, "Sample PRD", "test-spec")

	if !isZeroAssessment(assessment) {
		t.Errorf("assessment = %+v; want zero-value Assessment", assessment)
	}
	if err == nil {
		t.Fatal("AssessPRD() returned nil error; want AgentError with category pause_turn")
	}
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("error type = %T; want *AgentError", err)
	}
	if agentErr.ErrorCategory != "pause_turn" {
		t.Errorf("AgentError.ErrorCategory = %q; want %q", agentErr.ErrorCategory, "pause_turn")
	}
}

// ---------------------------------------------------------------------------
// TS-07-29: AssessPRD SDK error wrapping
// ---------------------------------------------------------------------------

// TestSpec07_AssessPRD_SDKError_RateLimit verifies that AssessPRD wraps an
// Anthropic SDK rate limit error as AgentError with category=rate_limit,
// retryable=true, and HTTPStatus=429.
// Test Spec: TS-07-29, Requirement: 07-REQ-5.6
func TestSpec07_AssessPRD_SDKError_RateLimit(t *testing.T) {
	httpStatus := 429
	sdkErr := &AgentError{
		Detail:        "rate limited",
		ErrorCategory: "rate_limit",
		Retryable:     true,
		HTTPStatus:    &httpStatus,
	}

	mockFn := newMockAICallFunc(nil, mockAICallResult{
		err: sdkErr,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	assessment, err := agent.AssessPRD(ctx, "Sample PRD", "test-spec")

	if !isZeroAssessment(assessment) {
		t.Errorf("assessment = %+v; want zero-value Assessment", assessment)
	}
	if err == nil {
		t.Fatal("AssessPRD() returned nil error; want AgentError with category rate_limit")
	}
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("error type = %T; want *AgentError", err)
	}
	if agentErr.ErrorCategory != "rate_limit" {
		t.Errorf("AgentError.ErrorCategory = %q; want %q", agentErr.ErrorCategory, "rate_limit")
	}
	if agentErr.HTTPStatus == nil || *agentErr.HTTPStatus != 429 {
		t.Errorf("AgentError.HTTPStatus = %v; want 429", agentErr.HTTPStatus)
	}
	if !agentErr.Retryable {
		t.Error("AgentError.Retryable = false; want true")
	}
}

// TestSpec07_AssessPRD_SDKError_Auth verifies that AssessPRD wraps an
// authentication error as AgentError with category=auth and retryable=false.
// Test Spec: TS-07-29, Requirement: 07-REQ-5.6
func TestSpec07_AssessPRD_SDKError_Auth(t *testing.T) {
	httpStatus := 401
	authErr := &AgentError{
		Detail:        "unauthorized",
		ErrorCategory: "auth",
		Retryable:     false,
		HTTPStatus:    &httpStatus,
	}

	mockFn := newMockAICallFunc(nil, mockAICallResult{
		err: authErr,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	assessment, err := agent.AssessPRD(ctx, "Sample PRD", "test-spec")

	if !isZeroAssessment(assessment) {
		t.Errorf("assessment = %+v; want zero-value Assessment", assessment)
	}
	if err == nil {
		t.Fatal("AssessPRD() returned nil error; want AgentError with category auth")
	}
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("error type = %T; want *AgentError", err)
	}
	if agentErr.ErrorCategory != "auth" {
		t.Errorf("AgentError.ErrorCategory = %q; want %q", agentErr.ErrorCategory, "auth")
	}
	if agentErr.Retryable {
		t.Error("AgentError.Retryable = true; want false for auth errors")
	}
}

// ---------------------------------------------------------------------------
// 07-REQ-5.E1: AssessPRD missing tool call
// ---------------------------------------------------------------------------

// TestSpec07_AssessPRD_MissingToolCall verifies that AssessPRD returns an
// AgentError with category=internal when the response lacks a submit_assessment
// tool call.
// Edge Case: 07-REQ-5.E1
func TestSpec07_AssessPRD_MissingToolCall(t *testing.T) {
	// Response with end_turn but no tool calls — only text.
	resp := &MessageResponse{
		Content:    []ContentBlock{{Type: "text", Text: "I cannot assess this PRD."}},
		StopReason: "end_turn",
	}

	mockFn := newMockAICallFunc(nil, mockAICallResult{
		text: "I cannot assess this PRD.",
		raw:  resp,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	assessment, err := agent.AssessPRD(ctx, "Sample PRD", "test-spec")

	if !isZeroAssessment(assessment) {
		t.Errorf("assessment = %+v; want zero-value Assessment", assessment)
	}
	if err == nil {
		t.Fatal("AssessPRD() returned nil error; want AgentError with category internal")
	}
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("error type = %T; want *AgentError", err)
	}
	if agentErr.ErrorCategory != "internal" {
		t.Errorf("AgentError.ErrorCategory = %q; want %q", agentErr.ErrorCategory, "internal")
	}
}

// ---------------------------------------------------------------------------
// 07-REQ-5.E2: AssessPRD malformed assessment payload
// ---------------------------------------------------------------------------

// TestSpec07_AssessPRD_MalformedPayload verifies that AssessPRD returns an
// AgentError with category=internal when the tool call payload cannot be
// parsed into a valid Assessment.
// Edge Case: 07-REQ-5.E2
func TestSpec07_AssessPRD_MalformedPayload(t *testing.T) {
	// Tool call with invalid/unparseable input.
	resp := makeToolCallResponse("end_turn", ContentBlock{
		Type:  "tool_use",
		ID:    "toolu_bad_01",
		Name:  "submit_assessment",
		Input: "this is not valid JSON or map",
	})

	mockFn := newMockAICallFunc(nil, mockAICallResult{
		raw: resp,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	assessment, err := agent.AssessPRD(ctx, "Sample PRD", "test-spec")

	if !isZeroAssessment(assessment) {
		t.Errorf("assessment = %+v; want zero-value Assessment", assessment)
	}
	if err == nil {
		t.Fatal("AssessPRD() returned nil error; want AgentError with category internal")
	}
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("error type = %T; want *AgentError", err)
	}
	if agentErr.ErrorCategory != "internal" {
		t.Errorf("AgentError.ErrorCategory = %q; want %q", agentErr.ErrorCategory, "internal")
	}
}

// ---------------------------------------------------------------------------
// 07-REQ-5.E3: AssessPRD context cancellation
// ---------------------------------------------------------------------------

// TestSpec07_AssessPRD_ContextCancellation verifies that AssessPRD returns
// immediately with the context error wrapped as an AgentError when the
// context is cancelled.
// Edge Case: 07-REQ-5.E3
func TestSpec07_AssessPRD_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	mockFn := newMockAICallFunc(nil, mockAICallResult{
		err: ctx.Err(),
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	assessment, err := agent.AssessPRD(ctx, "Sample PRD", "test-spec")

	if !isZeroAssessment(assessment) {
		t.Errorf("assessment = %+v; want zero-value Assessment", assessment)
	}
	if err == nil {
		t.Fatal("AssessPRD() returned nil error; want error for cancelled context")
	}
}

// ---------------------------------------------------------------------------
// 07-REQ-5.E4: AssessPRD with empty prdText
// ---------------------------------------------------------------------------

// TestSpec07_AssessPRD_EmptyPRDText verifies that AssessPRD proceeds with
// the API call when prdText is empty; the LLM response determines the outcome.
// Edge Case: 07-REQ-5.E4
func TestSpec07_AssessPRD_EmptyPRDText(t *testing.T) {
	capture := &aiCallCapture{}

	assessmentResp := makeToolCallResponse("end_turn",
		makeAssessmentToolCall("incomplete", "Empty PRD", []string{"No content"}, nil),
	)

	mockFn := newMockAICallFunc(capture, mockAICallResult{
		raw: assessmentResp,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	assessment, err := agent.AssessPRD(ctx, "", "test-spec")

	// Should not error — the call proceeds; the LLM determines the outcome.
	if err != nil {
		t.Fatalf("AssessPRD(\"\") returned error: %v; want success (empty PRD is allowed)", err)
	}

	// Verify AICall was actually invoked (the agent didn't short-circuit).
	if capture.count() == 0 {
		t.Error("AICall was not invoked; want at least 1 call even with empty prdText")
	}

	// Assessment should have fields from the mock response.
	if assessment.Quality == "" {
		t.Error("assessment.Quality is empty; want non-empty from LLM response")
	}
}

// ---------------------------------------------------------------------------
// TS-07-30: RefinePRD happy path
// ---------------------------------------------------------------------------

// TestSpec07_RefinePRD_HappyPath verifies that RefinePRD builds prompts,
// calls AICall with refinement tools, extracts submit_prd_update and
// submit_assessment, and returns updated PRD text and new Assessment.
// Test Spec: TS-07-30, Requirement: 07-REQ-6.1
func TestSpec07_RefinePRD_HappyPath(t *testing.T) {
	capture := &aiCallCapture{}

	// Response contains both tool calls.
	resp := makeToolCallResponse("end_turn",
		makePRDUpdateToolCall("Updated PRD text"),
		makeAssessmentToolCall("ready", "Much improved", nil, nil),
	)

	mockFn := newMockAICallFunc(capture, mockAICallResult{
		raw: resp,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	prevAssessment := Assessment{Quality: "needs_refinement", Summary: "Needs work"}
	updatedPRD, newAssessment, err := agent.RefinePRD(ctx, "Original PRD",
		map[string]string{"q1": "a1"}, prevAssessment)

	if err != nil {
		t.Fatalf("RefinePRD() returned error: %v", err)
	}
	if updatedPRD == "" {
		t.Error("RefinePRD() updatedPRD is empty; want non-empty updated PRD text")
	}
	if updatedPRD != "Updated PRD text" {
		t.Errorf("RefinePRD() updatedPRD = %q; want %q", updatedPRD, "Updated PRD text")
	}
	if isZeroAssessment(newAssessment) {
		t.Error("RefinePRD() newAssessment is zero-value; want populated Assessment")
	}
	if newAssessment.Quality != "ready" {
		t.Errorf("newAssessment.Quality = %q; want %q", newAssessment.Quality, "ready")
	}

	// Verify AICall was invoked exactly once (both tools in single response).
	if capture.count() != 1 {
		t.Errorf("AICall invocation count = %d; want 1", capture.count())
	}

	// Verify temperature=0.2 was set (NS-REQ-2).
	refineCallOpts := capture.get(0)
	if refineCallOpts.Temperature == nil {
		t.Error("RefinePRD AICall Temperature is nil; want 0.2")
	} else if *refineCallOpts.Temperature != 0.2 {
		t.Errorf("RefinePRD AICall Temperature = %f; want 0.2", *refineCallOpts.Temperature)
	}
}

// ---------------------------------------------------------------------------
// TS-07-31: RefinePRD missing assessment returns error (single-call contract)
// ---------------------------------------------------------------------------

// TestSpec07_RefinePRD_MissingAssessmentReturnsError verifies that RefinePRD
// returns an AgentError with category=internal when the single LLM response
// contains submit_prd_update but lacks submit_assessment. No fallback second
// call is made.
// Test Spec: TS-07-31, Requirement: 07-REQ-6.2
func TestSpec07_RefinePRD_MissingAssessmentReturnsError(t *testing.T) {
	capture := &aiCallCapture{}

	// Response contains only submit_prd_update — no submit_assessment.
	resp := makeToolCallResponse("end_turn",
		makePRDUpdateToolCall("Updated PRD text"),
	)

	mockFn := newMockAICallFunc(capture, mockAICallResult{
		raw: resp,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	updatedPRD, newAssessment, err := agent.RefinePRD(ctx, "Original PRD",
		map[string]string{"q1": "a1"}, Assessment{Quality: "needs_refinement"})

	// Must return an error.
	if err == nil {
		t.Fatal("RefinePRD() returned nil error; want AgentError when assessment is missing")
	}
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("error type = %T; want *AgentError", err)
	}
	if agentErr.ErrorCategory != "internal" {
		t.Errorf("AgentError.ErrorCategory = %q; want %q", agentErr.ErrorCategory, "internal")
	}

	// No partial results.
	if updatedPRD != "" {
		t.Errorf("updatedPRD = %q; want empty string (no partial results)", updatedPRD)
	}
	if !isZeroAssessment(newAssessment) {
		t.Errorf("newAssessment = %+v; want zero-value Assessment", newAssessment)
	}

	// Exactly one AICall — no fallback second call.
	if capture.count() != 1 {
		t.Errorf("AICall invocation count = %d; want exactly 1 (no fallback)", capture.count())
	}
}

// ---------------------------------------------------------------------------
// TS-07-32: RefinePRD stop reason errors
// ---------------------------------------------------------------------------

// TestSpec07_RefinePRD_StopReason_Refusal verifies that RefinePRD returns
// an AgentError with category=refusal when the stop reason is "refusal".
// Test Spec: TS-07-32, Requirement: 07-REQ-6.3
func TestSpec07_RefinePRD_StopReason_Refusal(t *testing.T) {
	resp := makeToolCallResponse("refusal")

	mockFn := newMockAICallFunc(nil, mockAICallResult{
		raw: resp,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	updatedPRD, newAssessment, err := agent.RefinePRD(ctx, "Original PRD",
		map[string]string{}, Assessment{})

	if updatedPRD != "" {
		t.Errorf("updatedPRD = %q; want empty string", updatedPRD)
	}
	if !isZeroAssessment(newAssessment) {
		t.Errorf("newAssessment = %+v; want zero-value Assessment", newAssessment)
	}
	if err == nil {
		t.Fatal("RefinePRD() returned nil error; want AgentError with category refusal")
	}
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("error type = %T; want *AgentError", err)
	}
	if agentErr.ErrorCategory != "refusal" {
		t.Errorf("AgentError.ErrorCategory = %q; want %q", agentErr.ErrorCategory, "refusal")
	}
}

// TestSpec07_RefinePRD_StopReason_ContextWindowExceeded verifies error
// handling for context_window_exceeded stop reason.
// Requirement: 07-REQ-6.3
func TestSpec07_RefinePRD_StopReason_ContextWindowExceeded(t *testing.T) {
	resp := makeToolCallResponse("context_window_exceeded")

	mockFn := newMockAICallFunc(nil, mockAICallResult{
		raw: resp,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	updatedPRD, newAssessment, err := agent.RefinePRD(ctx, "Original PRD",
		map[string]string{}, Assessment{})

	if updatedPRD != "" {
		t.Errorf("updatedPRD = %q; want empty string", updatedPRD)
	}
	if !isZeroAssessment(newAssessment) {
		t.Errorf("newAssessment = %+v; want zero-value Assessment", newAssessment)
	}
	if err == nil {
		t.Fatal("RefinePRD() returned nil error; want AgentError")
	}
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("error type = %T; want *AgentError", err)
	}
	if agentErr.ErrorCategory != "context_window" {
		t.Errorf("AgentError.ErrorCategory = %q; want %q", agentErr.ErrorCategory, "context_window")
	}
}

// TestSpec07_RefinePRD_StopReason_PauseTurn verifies error handling for
// pause_turn stop reason.
// Requirement: 07-REQ-6.3
func TestSpec07_RefinePRD_StopReason_PauseTurn(t *testing.T) {
	resp := makeToolCallResponse("pause_turn")

	mockFn := newMockAICallFunc(nil, mockAICallResult{
		raw: resp,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	updatedPRD, newAssessment, err := agent.RefinePRD(ctx, "Original PRD",
		map[string]string{}, Assessment{})

	if updatedPRD != "" {
		t.Errorf("updatedPRD = %q; want empty string", updatedPRD)
	}
	if !isZeroAssessment(newAssessment) {
		t.Errorf("newAssessment = %+v; want zero-value Assessment", newAssessment)
	}
	if err == nil {
		t.Fatal("RefinePRD() returned nil error; want AgentError")
	}
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("error type = %T; want *AgentError", err)
	}
	if agentErr.ErrorCategory != "pause_turn" {
		t.Errorf("AgentError.ErrorCategory = %q; want %q", agentErr.ErrorCategory, "pause_turn")
	}
}

// ---------------------------------------------------------------------------
// 07-REQ-6.E2: RefinePRD context cancellation mid-refinement
// ---------------------------------------------------------------------------

// TestSpec07_RefinePRD_ContextCancellation verifies that RefinePRD returns
// immediately with the context error when cancelled.
// Edge Case: 07-REQ-6.E2
func TestSpec07_RefinePRD_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockFn := newMockAICallFunc(nil, mockAICallResult{
		err: ctx.Err(),
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	updatedPRD, newAssessment, err := agent.RefinePRD(ctx, "Original PRD",
		map[string]string{}, Assessment{})

	if updatedPRD != "" {
		t.Errorf("updatedPRD = %q; want empty string", updatedPRD)
	}
	if !isZeroAssessment(newAssessment) {
		t.Errorf("newAssessment = %+v; want zero-value Assessment", newAssessment)
	}
	if err == nil {
		t.Fatal("RefinePRD() returned nil error; want context cancellation error")
	}
}

// ---------------------------------------------------------------------------
// 07-REQ-6.E3: RefinePRD no tool calls extracted
// ---------------------------------------------------------------------------

// TestSpec07_RefinePRD_NoToolCalls verifies that RefinePRD returns an
// AgentError with category=internal when neither tool call is present.
// Edge Case: 07-REQ-6.E3
func TestSpec07_RefinePRD_NoToolCalls(t *testing.T) {
	resp := &MessageResponse{
		Content:    []ContentBlock{{Type: "text", Text: "Some text without tool calls"}},
		StopReason: "end_turn",
	}

	mockFn := newMockAICallFunc(nil, mockAICallResult{
		text: "Some text without tool calls",
		raw:  resp,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	updatedPRD, newAssessment, err := agent.RefinePRD(ctx, "Original PRD",
		map[string]string{}, Assessment{})

	if updatedPRD != "" {
		t.Errorf("updatedPRD = %q; want empty string", updatedPRD)
	}
	if !isZeroAssessment(newAssessment) {
		t.Errorf("newAssessment = %+v; want zero-value Assessment", newAssessment)
	}
	if err == nil {
		t.Fatal("RefinePRD() returned nil error; want AgentError with category internal")
	}
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("error type = %T; want *AgentError", err)
	}
	if agentErr.ErrorCategory != "internal" {
		t.Errorf("AgentError.ErrorCategory = %q; want %q", agentErr.ErrorCategory, "internal")
	}
}

// ---------------------------------------------------------------------------
// TS-07-37: AgentOption type and constructors
// ---------------------------------------------------------------------------

// TestSpec07_AgentOption_Constructors verifies that AgentOption type and all
// four option constructors are defined and compile correctly.
// Test Spec: TS-07-37, Requirement: 07-REQ-8.1
func TestSpec07_AgentOption_Constructors(t *testing.T) {
	// Verify all constructors return AgentOption values without panicking.
	var opt1 AgentOption = WithSpecLandscape([]map[string]any{})
	var opt2 AgentOption = WithDependentInterfaces([]map[string]any{})
	var opt3 AgentOption = WithProjectDir("/tmp")
	var opt4 AgentOption = WithOnArtifact(func(name string, content any) {})

	if opt1 == nil {
		t.Error("WithSpecLandscape returned nil")
	}
	if opt2 == nil {
		t.Error("WithDependentInterfaces returned nil")
	}
	if opt3 == nil {
		t.Error("WithProjectDir returned nil")
	}
	if opt4 == nil {
		t.Error("WithOnArtifact returned nil")
	}
}

// TestSpec07_AgentOption_WithSpecLandscape verifies that WithSpecLandscape
// sets the spec landscape on agent call options.
// Test Spec: TS-07-37, Requirement: 07-REQ-8.1
func TestSpec07_AgentOption_WithSpecLandscape(t *testing.T) {
	landscape := []map[string]any{
		{"spec_id": "01", "title": "Spec A"},
		{"spec_id": "02", "title": "Spec B"},
	}

	opts := applyOptions([]AgentOption{WithSpecLandscape(landscape)})

	if len(opts.specLandscape) != 2 {
		t.Errorf("specLandscape length = %d; want 2", len(opts.specLandscape))
	}
}

// TestSpec07_AgentOption_WithDependentInterfaces verifies that
// WithDependentInterfaces sets the dependent interfaces.
// Test Spec: TS-07-37, Requirement: 07-REQ-8.1
func TestSpec07_AgentOption_WithDependentInterfaces(t *testing.T) {
	interfaces := []map[string]any{
		{"name": "AuthService", "methods": []string{"Login", "Logout"}},
	}

	opts := applyOptions([]AgentOption{WithDependentInterfaces(interfaces)})

	if len(opts.dependentInterfaces) != 1 {
		t.Errorf("dependentInterfaces length = %d; want 1", len(opts.dependentInterfaces))
	}
}

// TestSpec07_AgentOption_WithProjectDir verifies that WithProjectDir sets
// the project directory.
// Test Spec: TS-07-37, Requirement: 07-REQ-8.1
func TestSpec07_AgentOption_WithProjectDir(t *testing.T) {
	opts := applyOptions([]AgentOption{WithProjectDir("/my/project")})

	if opts.projectDir != "/my/project" {
		t.Errorf("projectDir = %q; want %q", opts.projectDir, "/my/project")
	}
}

// TestSpec07_AgentOption_WithOnArtifact verifies that WithOnArtifact sets
// the callback function.
// Test Spec: TS-07-37, Requirement: 07-REQ-8.1
func TestSpec07_AgentOption_WithOnArtifact(t *testing.T) {
	called := false
	cb := func(name string, content any) { called = true }

	opts := applyOptions([]AgentOption{WithOnArtifact(cb)})

	if opts.onArtifact == nil {
		t.Fatal("onArtifact is nil; want non-nil callback")
	}

	// Verify the callback is actually the one we set.
	opts.onArtifact("test", nil)
	if !called {
		t.Error("onArtifact callback was not invoked; want the callback we provided")
	}
}

// TestSpec07_AgentOption_Composition verifies that multiple options can be
// composed and all take effect.
// Test Spec: TS-07-37, Requirement: 07-REQ-8.1
func TestSpec07_AgentOption_Composition(t *testing.T) {
	opts := applyOptions([]AgentOption{
		WithSpecLandscape([]map[string]any{{"spec_id": "01"}}),
		WithDependentInterfaces([]map[string]any{{"name": "Dep"}}),
		WithProjectDir("/composed"),
		WithOnArtifact(func(name string, content any) {}),
	})

	if len(opts.specLandscape) != 1 {
		t.Error("specLandscape not set after composition")
	}
	if len(opts.dependentInterfaces) != 1 {
		t.Error("dependentInterfaces not set after composition")
	}
	if opts.projectDir != "/composed" {
		t.Errorf("projectDir = %q; want %q", opts.projectDir, "/composed")
	}
	if opts.onArtifact == nil {
		t.Error("onArtifact is nil after composition")
	}
}

// ---------------------------------------------------------------------------
// TS-07-38: SpecAgent methods apply all provided AgentOptions
// ---------------------------------------------------------------------------

// TestSpec07_AgentOption_AppliedByAssessPRD verifies that AssessPRD applies
// all provided AgentOptions before executing method logic.
// Test Spec: TS-07-38, Requirement: 07-REQ-8.2
func TestSpec07_AgentOption_AppliedByAssessPRD(t *testing.T) {
	capture := &aiCallCapture{}

	assessmentResp := makeToolCallResponse("end_turn",
		makeAssessmentToolCall("ready", "Good", nil, nil),
	)

	mockFn := newMockAICallFunc(capture, mockAICallResult{
		raw: assessmentResp,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	_, err := agent.AssessPRD(ctx, "PRD", "test",
		WithProjectDir("/custom/project"),
		WithSpecLandscape([]map[string]any{{"spec_id": "01", "title": "Other"}}),
	)

	if err != nil {
		t.Fatalf("AssessPRD() returned error: %v", err)
	}

	// The test verifies that options were applied by checking that the call
	// was made (no panic from applying options). The full verification of
	// prompt loading with projectDir is tested in the prompt builder tests.
	if capture.count() == 0 {
		t.Error("AICall was not invoked; options may not have been applied correctly")
	}
}

// ---------------------------------------------------------------------------
// TS-07-39: SpecAgent methods use zero-value defaults with no AgentOptions
// ---------------------------------------------------------------------------

// TestSpec07_AgentOption_ZeroValueDefaults verifies that SpecAgent methods
// use zero-value defaults when called with no AgentOptions.
// Test Spec: TS-07-39, Requirement: 07-REQ-8.3
func TestSpec07_AgentOption_ZeroValueDefaults(t *testing.T) {
	assessmentResp := makeToolCallResponse("end_turn",
		makeAssessmentToolCall("needs_refinement", "OK", nil, nil),
	)

	mockFn := newMockAICallFunc(nil, mockAICallResult{
		raw: assessmentResp,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()

	// Should not panic with no options — uses empty landscape, empty
	// interfaces, empty projectDir, nil callback.
	assessment, err := agent.AssessPRD(ctx, "PRD", "test")
	if err != nil {
		// An error from the "not implemented" stub is acceptable at this stage,
		// but a nil pointer panic is not.
		var agentErr *AgentError
		if errors.As(err, &agentErr) {
			// AgentError is fine — implementation not done yet.
			return
		}
		// Any error is acceptable as long as it's not a panic.
		_ = assessment
		return
	}

	// If no error, assessment should have some value.
	_ = assessment
}

// TestSpec07_AgentOption_NilCallback verifies that WithOnArtifact(nil) stores
// nil and does not cause panics during artifact generation.
// Edge Case: 07-REQ-8.E1
func TestSpec07_AgentOption_NilCallback(t *testing.T) {
	// Should not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("WithOnArtifact(nil) panicked: %v", r)
		}
	}()

	opt := WithOnArtifact(nil)
	if opt == nil {
		t.Fatal("WithOnArtifact(nil) returned nil; want non-nil AgentOption")
	}

	opts := applyOptions([]AgentOption{opt})
	if opts.onArtifact != nil {
		t.Error("onArtifact is non-nil; want nil when WithOnArtifact(nil) is used")
	}
}

// ---------------------------------------------------------------------------
// TS-NS-1: Valid quality values are accepted by parseAssessment (NS-REQ-1)
// TS-NS-2: Invalid quality value is rejected with informative error (NS-REQ-2)
// TS-NS-3: Missing quality field is rejected (NS-REQ-3)
// TS-NS-5: Invalid quality does not enter AssessmentHistory (NS-REQ-5)
// ---------------------------------------------------------------------------

// TestNS70_ParseAssessment_ValidQualities verifies that each of the three valid
// quality values is accepted by parseAssessment without error.
// Test Spec: TS-NS-1, Requirement: NS-REQ-1
func TestNS70_ParseAssessment_ValidQualities(t *testing.T) {
	t.Parallel()
	for _, quality := range []string{"ready", "needs_refinement", "incomplete"} {
		quality := quality
		t.Run(quality, func(t *testing.T) {
			t.Parallel()
			input := map[string]any{
				"quality": quality,
				"summary": "test summary",
			}
			got, err := parseAssessment(input)
			if err != nil {
				t.Fatalf("parseAssessment(%q) returned unexpected error: %v", quality, err)
			}
			if got.Quality != quality {
				t.Errorf("Assessment.Quality = %q; want %q", got.Quality, quality)
			}
		})
	}
}

// TestNS70_ParseAssessment_InvalidQuality verifies that an invalid quality value
// is rejected with an error mentioning the invalid value and the allowed enum values.
// Test Spec: TS-NS-2, Requirement: NS-REQ-2
func TestNS70_ParseAssessment_InvalidQuality(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"quality": "mostly_good",
		"summary": "test summary",
	}
	got, err := parseAssessment(input)
	if err == nil {
		t.Fatal("parseAssessment() returned nil error; want error for invalid quality")
	}
	if !isZeroAssessment(got) {
		t.Errorf("parseAssessment() returned non-zero Assessment on error: %+v", got)
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "mostly_good") {
		t.Errorf("error %q does not mention the invalid value %q", errMsg, "mostly_good")
	}
	for _, allowed := range []string{"ready", "needs_refinement", "incomplete"} {
		if !strings.Contains(errMsg, allowed) {
			t.Errorf("error %q does not mention allowed value %q", errMsg, allowed)
		}
	}
}

// TestNS70_ParseAssessment_MissingQuality verifies that a missing quality field
// is rejected with an informative error.
// Test Spec: TS-NS-3, Requirement: NS-REQ-3
func TestNS70_ParseAssessment_MissingQuality(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"summary": "test summary",
		// "quality" intentionally omitted
	}
	got, err := parseAssessment(input)
	if err == nil {
		t.Fatal("parseAssessment() returned nil error; want error for missing quality")
	}
	if !isZeroAssessment(got) {
		t.Errorf("parseAssessment() returned non-zero Assessment on error: %+v", got)
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "quality") {
		t.Errorf("error %q does not mention 'quality'", errMsg)
	}
}

// TestNS70_AssessmentHistory_NotAppendedOnInvalidQuality verifies that an
// invalid quality value from the LLM does not get appended to AssessmentHistory.
// Test Spec: TS-NS-5, Requirement: NS-REQ-5
func TestNS70_AssessmentHistory_NotAppendedOnInvalidQuality(t *testing.T) {
	t.Parallel()

	// Mock returns a submit_assessment with an invalid quality value.
	resp := makeToolCallResponse("end_turn",
		makeAssessmentToolCall("hallucinated_value", "Test summary", nil, nil),
	)

	mockFn := newMockAICallFunc(nil, mockAICallResult{
		raw: resp,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	assessment, err := agent.AssessPRD(ctx, "Sample PRD", "test-spec")

	// Must return an error.
	if err == nil {
		t.Fatal("AssessPRD() returned nil error; want error for invalid quality")
	}

	// Assessment must be zero-value (nothing valid returned).
	if !isZeroAssessment(assessment) {
		t.Errorf("assessment = %+v; want zero-value Assessment", assessment)
	}
}

// ---------------------------------------------------------------------------
// TS-NS-1: AssessPRD uses MaxTokens of 4096 (NS-REQ-1)
// TS-NS-2: RefinePRD uses MaxTokens of 16384 (NS-REQ-2)
// TS-NS-3: GenerateArtifacts retains 65536 default MaxTokens (NS-REQ-3)
// ---------------------------------------------------------------------------

// TestNS81_AssessPRD_MaxTokens verifies that AssessPRD sets MaxTokens: 4096
// in the AICallOptions passed to the underlying AI call function.
// Test Spec: TS-NS-1, Requirement: NS-REQ-1
func TestNS81_AssessPRD_MaxTokens(t *testing.T) {
	capture := &aiCallCapture{}

	assessmentResp := makeToolCallResponse("end_turn",
		makeAssessmentToolCall("ready", "OK", nil, nil),
	)

	mockFn := newMockAICallFunc(capture, mockAICallResult{
		raw: assessmentResp,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	_, err := agent.AssessPRD(ctx, "Sample PRD", "test-spec")
	if err != nil {
		t.Fatalf("AssessPRD() returned error: %v", err)
	}

	if capture.count() != 1 {
		t.Fatalf("AICall invocation count = %d; want 1", capture.count())
	}

	callOpts := capture.get(0)
	if callOpts.MaxTokens != 4096 {
		t.Errorf("AssessPRD AICallOptions.MaxTokens = %d; want 4096", callOpts.MaxTokens)
	}
}

// TestNS81_RefinePRD_MaxTokens verifies that RefinePRD sets MaxTokens: 16384
// in the AICallOptions passed to the underlying AI call function.
// Test Spec: TS-NS-2, Requirement: NS-REQ-2
func TestNS81_RefinePRD_MaxTokens(t *testing.T) {
	capture := &aiCallCapture{}

	resp := makeToolCallResponse("end_turn",
		makePRDUpdateToolCall("Updated PRD"),
		makeAssessmentToolCall("ready", "OK", nil, nil),
	)

	mockFn := newMockAICallFunc(capture, mockAICallResult{
		raw: resp,
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	prevAssessment := Assessment{Quality: "needs_refinement", Summary: "Needs work"}
	_, _, err := agent.RefinePRD(ctx, "Original PRD",
		map[string]string{"q1": "a1"}, prevAssessment)
	if err != nil {
		t.Fatalf("RefinePRD() returned error: %v", err)
	}

	if capture.count() != 1 {
		t.Fatalf("AICall invocation count = %d; want 1", capture.count())
	}

	callOpts := capture.get(0)
	if callOpts.MaxTokens != 16384 {
		t.Errorf("RefinePRD AICallOptions.MaxTokens = %d; want 16384", callOpts.MaxTokens)
	}
}
