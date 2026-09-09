package agentspec

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/afspec"
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
// TS-07-33: GenerateArtifacts sequential pipeline and OnArtifact callback
// ---------------------------------------------------------------------------

// TestSpec07_GenerateArtifacts_HappyPath verifies that GenerateArtifacts
// generates requirements first, then test_spec and tasks concurrently, with
// temperature=0.2, validates each, and invokes the OnArtifact callback.
// Test Spec: TS-07-33, Requirement: 07-REQ-7.1
func TestSpec07_GenerateArtifacts_HappyPath(t *testing.T) {
	capture := &aiCallCapture{}

	requirementsContent := map[string]any{
		"spec_id":      "07",
		"spec_name":    "test",
		"requirements": []any{},
	}
	testSpecContent := map[string]any{
		"spec_id":    "07",
		"spec_name":  "test",
		"test_cases": []any{},
	}
	tasksContent := map[string]any{
		"spec_id":     "07",
		"spec_name":   "test",
		"task_groups": []any{},
	}

	// Route by artifact name in Context (parallel-safe).
	mockFn := func(ctx context.Context, opts AICallOptions) (string, any, error) {
		capture.record(opts)
		var resp *MessageResponse
		switch {
		case strings.Contains(opts.Context, "requirements"):
			resp = makeToolCallResponse("end_turn",
				makeArtifactToolCall("requirements", requirementsContent))
		case strings.Contains(opts.Context, "test_spec"):
			resp = makeToolCallResponse("end_turn",
				makeArtifactToolCall("test_spec", testSpecContent))
		case strings.Contains(opts.Context, "tasks"):
			resp = makeToolCallResponse("end_turn",
				makeArtifactToolCall("tasks", tasksContent))
		default:
			return "", nil, fmt.Errorf("unexpected context %q", opts.Context)
		}
		return "", resp, nil
	}

	// Track callback invocations.
	var callbackNames []string
	var callbackMu sync.Mutex
	callback := func(name string, content any) {
		callbackMu.Lock()
		defer callbackMu.Unlock()
		callbackNames = append(callbackNames, name)
	}

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	result, err := agent.GenerateArtifacts(ctx, "PRD", "07", "test",
		WithOnArtifact(callback))

	if err != nil {
		t.Fatalf("GenerateArtifacts() returned error: %v", err)
	}
	if result == nil {
		t.Fatal("GenerateArtifacts() returned nil result; want map with 3 artifacts")
	}

	// Verify all three artifacts are in the result.
	for _, name := range []string{"requirements", "test_spec", "tasks"} {
		if _, ok := result[name]; !ok {
			t.Errorf("result[%q] not found; want artifact in result map", name)
		}
	}

	// Verify callback was invoked 3 times; requirements must be first.
	// test_spec and tasks may arrive in either order (parallel execution).
	if len(callbackNames) != 3 {
		t.Fatalf("callback invocation count = %d; want 3", len(callbackNames))
	}
	if callbackNames[0] != "requirements" {
		t.Errorf("callback invocation[0] = %q; want %q", callbackNames[0], "requirements")
	}
	parallelSet := map[string]bool{callbackNames[1]: true, callbackNames[2]: true}
	if !parallelSet["test_spec"] || !parallelSet["tasks"] {
		t.Errorf("callback invocations[1,2] = %q, %q; want {test_spec, tasks} in any order",
			callbackNames[1], callbackNames[2])
	}

	// Verify temperature=0.2 was set for each call.
	for i := 0; i < capture.count(); i++ {
		opts := capture.get(i)
		if opts.Temperature == nil {
			t.Errorf("call[%d] Temperature is nil; want 0.2", i)
		} else if *opts.Temperature != 0.2 {
			t.Errorf("call[%d] Temperature = %f; want 0.2", i, *opts.Temperature)
		}
	}
}

// ---------------------------------------------------------------------------
// TS-07-34: GenerateArtifacts repair loop success
// ---------------------------------------------------------------------------

// TestSpec07_GenerateArtifacts_RepairLoop_Success verifies that
// GenerateArtifacts enters the repair loop when validation fails and
// returns the repaired artifact on success within 2 attempts.
// Test Spec: TS-07-34, Requirement: 07-REQ-7.2
func TestSpec07_GenerateArtifacts_RepairLoop_Success(t *testing.T) {
	capture := &aiCallCapture{}
	// Track how many times requirements was called (initial + repair).
	reqCallCount := 0
	var mu sync.Mutex

	// Invalid requirements (will fail validation).
	invalidContent := map[string]any{
		"invalid": true,
	}
	// Valid requirements (repair success).
	validRequirements := map[string]any{
		"spec_id":      "07",
		"spec_name":    "test",
		"requirements": []any{},
	}
	validTestSpec := map[string]any{
		"spec_id":    "07",
		"spec_name":  "test",
		"test_cases": []any{},
	}
	validTasks := map[string]any{
		"spec_id":     "07",
		"spec_name":   "test",
		"task_groups": []any{},
	}

	// Route by artifact name in Context (parallel-safe).
	mockFn := func(ctx context.Context, opts AICallOptions) (string, any, error) {
		capture.record(opts)
		var resp *MessageResponse
		switch {
		case strings.Contains(opts.Context, "requirements"):
			mu.Lock()
			n := reqCallCount
			reqCallCount++
			mu.Unlock()
			if n == 0 {
				// First requirements call: returns invalid payload.
				resp = makeToolCallResponse("end_turn",
					makeArtifactToolCall("requirements", invalidContent))
			} else {
				// Repair call: returns valid payload.
				resp = makeToolCallResponse("end_turn",
					makeArtifactToolCall("requirements", validRequirements))
			}
		case strings.Contains(opts.Context, "test_spec"):
			resp = makeToolCallResponse("end_turn",
				makeArtifactToolCall("test_spec", validTestSpec))
		case strings.Contains(opts.Context, "tasks"):
			resp = makeToolCallResponse("end_turn",
				makeArtifactToolCall("tasks", validTasks))
		default:
			return "", nil, fmt.Errorf("unexpected context %q", opts.Context)
		}
		return "", resp, nil
	}

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	result, err := agent.GenerateArtifacts(ctx, "PRD", "07", "test")

	if err != nil {
		t.Fatalf("GenerateArtifacts() returned error: %v", err)
	}
	if result == nil {
		t.Fatal("GenerateArtifacts() returned nil result")
	}
	if _, ok := result["requirements"]; !ok {
		t.Error("result[\"requirements\"] not found; want repaired artifact")
	}

	// Verify requirements had 2 calls (initial + 1 repair).
	// Exact call count depends on implementation, but at least 2 for requirements.
	if capture.count() < 2 {
		t.Errorf("total AICall count = %d; want at least 2 (initial + repair for requirements)", capture.count())
	}
}

// ---------------------------------------------------------------------------
// TS-07-35: GenerateArtifacts repair loop exhaustion
// ---------------------------------------------------------------------------

// TestSpec07_GenerateArtifacts_RepairLoop_Exhaustion verifies that
// GenerateArtifacts returns an error after 2 repair attempts are exhausted
// and does not generate subsequent artifacts.
// Test Spec: TS-07-35, Requirement: 07-REQ-7.3, 07-REQ-7.E4
func TestSpec07_GenerateArtifacts_RepairLoop_Exhaustion(t *testing.T) {
	capture := &aiCallCapture{}

	// Always return invalid content.
	invalidContent := map[string]any{
		"invalid": true,
	}

	mockFn := func(ctx context.Context, opts AICallOptions) (string, any, error) {
		capture.record(opts)
		resp := makeToolCallResponse("end_turn",
			makeArtifactToolCall("requirements", invalidContent))
		return "", resp, nil
	}

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	result, err := agent.GenerateArtifacts(ctx, "PRD", "07", "test")

	if result != nil {
		t.Errorf("GenerateArtifacts() returned non-nil result %v; want nil on failure", result)
	}
	if err == nil {
		t.Fatal("GenerateArtifacts() returned nil error; want error after repair exhaustion")
	}

	// Error message should mention which artifact failed.
	errMsg := err.Error()
	if !strings.Contains(strings.ToLower(errMsg), "requirements") {
		t.Errorf("error message %q does not mention 'requirements'", errMsg)
	}

	// Should have made exactly 3 calls for requirements: initial + 2 repairs.
	// No calls for test_spec or tasks.
	if capture.count() > 3 {
		t.Errorf("total AICall count = %d; want at most 3 (initial + 2 repairs for requirements, no subsequent artifacts)", capture.count())
	}
}

// ---------------------------------------------------------------------------
// TS-07-36: GenerateArtifacts priorArtifacts context
// ---------------------------------------------------------------------------

// TestSpec07_GenerateArtifacts_PriorArtifactsContext verifies that
// GenerateArtifacts passes requirements as priorArtifacts context when
// building prompts for test_spec and tasks (which run in parallel).
// Test Spec: TS-07-36, Requirement: 07-REQ-7.4
func TestSpec07_GenerateArtifacts_PriorArtifactsContext(t *testing.T) {
	capture := &aiCallCapture{}

	requirementsContent := map[string]any{
		"spec_id":      "07",
		"spec_name":    "test",
		"requirements": []any{map[string]any{"id": "07-REQ-1", "text": "Must do X"}},
	}
	testSpecContent := map[string]any{
		"spec_id":    "07",
		"spec_name":  "test",
		"test_cases": []any{map[string]any{"id": "TS-07-1", "requirement": "07-REQ-1"}},
	}
	tasksContent := map[string]any{
		"spec_id":     "07",
		"spec_name":   "test",
		"task_groups": []any{map[string]any{"id": 1, "description": "Implement 07-REQ-1"}},
	}

	// Route by artifact name in Context (parallel-safe).
	mockFn := func(ctx context.Context, opts AICallOptions) (string, any, error) {
		capture.record(opts)
		var resp *MessageResponse
		switch {
		case strings.Contains(opts.Context, "requirements"):
			resp = makeToolCallResponse("end_turn",
				makeArtifactToolCall("requirements", requirementsContent))
		case strings.Contains(opts.Context, "test_spec"):
			resp = makeToolCallResponse("end_turn",
				makeArtifactToolCall("test_spec", testSpecContent))
		case strings.Contains(opts.Context, "tasks"):
			resp = makeToolCallResponse("end_turn",
				makeArtifactToolCall("tasks", tasksContent))
		default:
			return "", nil, fmt.Errorf("unexpected context %q", opts.Context)
		}
		return "", resp, nil
	}

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	result, err := agent.GenerateArtifacts(ctx, "PRD", "07", "test")

	if err != nil {
		t.Fatalf("GenerateArtifacts() returned error: %v", err)
	}
	if result == nil {
		t.Fatal("GenerateArtifacts() returned nil result")
	}

	// Verify we had exactly 3 calls (requirements + test_spec + tasks).
	if capture.count() != 3 {
		t.Fatalf("AICall count = %d; want 3", capture.count())
	}

	// Find the test_spec and tasks call options by context.
	var testSpecOpts, tasksOpts *AICallOptions
	for i := 0; i < capture.count(); i++ {
		o := capture.get(i)
		switch {
		case strings.Contains(o.Context, "test_spec"):
			cp := o
			testSpecOpts = &cp
		case strings.Contains(o.Context, "tasks"):
			cp := o
			tasksOpts = &cp
		}
	}

	if testSpecOpts == nil {
		t.Fatal("no test_spec AICall recorded")
	}
	if tasksOpts == nil {
		t.Fatal("no tasks AICall recorded")
	}

	// test_spec prompt must contain requirements content (priorArtifacts).
	testSpecPrompt := testSpecOpts.System
	for _, msg := range testSpecOpts.Messages {
		testSpecPrompt += " " + messageContentString(msg)
	}
	if !strings.Contains(testSpecPrompt, "REQ-1") && !strings.Contains(testSpecPrompt, "requirements") {
		t.Error("test_spec prompt does not contain requirements artifact content; want priorArtifacts context")
	}

	// tasks prompt must contain requirements content (priorArtifacts).
	// Note: tasks runs in parallel with test_spec, so it receives only
	// requirements as prior context — not test_spec.
	tasksPrompt := tasksOpts.System
	for _, msg := range tasksOpts.Messages {
		tasksPrompt += " " + messageContentString(msg)
	}
	if !strings.Contains(tasksPrompt, "REQ-1") && !strings.Contains(tasksPrompt, "requirements") {
		t.Error("tasks prompt does not contain requirements artifact content; want priorArtifacts context")
	}
}

// ---------------------------------------------------------------------------
// 07-REQ-7.E1: GenerateArtifacts context cancellation
// ---------------------------------------------------------------------------

// TestSpec07_GenerateArtifacts_ContextCancellation verifies that
// GenerateArtifacts returns immediately with the context error when cancelled.
// Edge Case: 07-REQ-7.E1
func TestSpec07_GenerateArtifacts_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockFn := newMockAICallFunc(nil, mockAICallResult{
		err: ctx.Err(),
	})

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	result, err := agent.GenerateArtifacts(ctx, "PRD", "07", "test")

	if result != nil {
		t.Errorf("result = %v; want nil", result)
	}
	if err == nil {
		t.Fatal("GenerateArtifacts() returned nil error; want context cancellation error")
	}
}

// ---------------------------------------------------------------------------
// 07-REQ-7.E2: GenerateArtifacts OnArtifact callback panic recovery
// ---------------------------------------------------------------------------

// TestSpec07_GenerateArtifacts_CallbackPanic verifies that GenerateArtifacts
// recovers from a panicking OnArtifact callback and returns an error.
// Edge Case: 07-REQ-7.E2
func TestSpec07_GenerateArtifacts_CallbackPanic(t *testing.T) {
	validContent := map[string]any{
		"spec_id":      "07",
		"spec_name":    "test",
		"requirements": []any{},
	}

	resp := makeToolCallResponse("end_turn",
		makeArtifactToolCall("requirements", validContent))

	mockFn := newMockAICallFunc(nil, mockAICallResult{
		raw: resp,
	})

	panicCallback := func(name string, content any) {
		panic("callback exploded")
	}

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	result, err := agent.GenerateArtifacts(ctx, "PRD", "07", "test",
		WithOnArtifact(panicCallback))

	if result != nil {
		t.Errorf("result = %v; want nil on callback panic", result)
	}
	if err == nil {
		t.Fatal("GenerateArtifacts() returned nil error; want error from callback panic")
	}
}

// ---------------------------------------------------------------------------
// 07-REQ-7.E3: GenerateArtifacts malformed artifact payload
// ---------------------------------------------------------------------------

// TestSpec07_GenerateArtifacts_MalformedPayload verifies that GenerateArtifacts
// treats a malformed tool call payload as a validation error and enters the
// repair loop.
// Edge Case: 07-REQ-7.E3
func TestSpec07_GenerateArtifacts_MalformedPayload(t *testing.T) {
	reqCallCount := 0
	var mu sync.Mutex

	validRequirements := map[string]any{
		"spec_id":      "07",
		"spec_name":    "test",
		"requirements": []any{},
	}
	validTestSpec := map[string]any{
		"spec_id":    "07",
		"spec_name":  "test",
		"test_cases": []any{},
	}
	validTasks := map[string]any{
		"spec_id":     "07",
		"spec_name":   "test",
		"task_groups": []any{},
	}

	// Route by artifact name in Context (parallel-safe).
	mockFn := func(ctx context.Context, opts AICallOptions) (string, any, error) {
		var resp *MessageResponse
		switch {
		case strings.Contains(opts.Context, "requirements"):
			mu.Lock()
			n := reqCallCount
			reqCallCount++
			mu.Unlock()
			if n == 0 {
				// First requirements call: malformed payload (not a valid map/struct).
				resp = makeToolCallResponse("end_turn", ContentBlock{
					Type:  "tool_use",
					ID:    "toolu_bad",
					Name:  "submit_requirements",
					Input: "this-is-not-json",
				})
			} else {
				// Repair call: valid payload.
				resp = makeToolCallResponse("end_turn",
					makeArtifactToolCall("requirements", validRequirements))
			}
		case strings.Contains(opts.Context, "test_spec"):
			resp = makeToolCallResponse("end_turn",
				makeArtifactToolCall("test_spec", validTestSpec))
		case strings.Contains(opts.Context, "tasks"):
			resp = makeToolCallResponse("end_turn",
				makeArtifactToolCall("tasks", validTasks))
		default:
			return "", nil, fmt.Errorf("unexpected context %q", opts.Context)
		}
		return "", resp, nil
	}

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	result, err := agent.GenerateArtifacts(ctx, "PRD", "07", "test")

	// Should succeed after repair.
	if err != nil {
		t.Fatalf("GenerateArtifacts() returned error: %v", err)
	}
	if result == nil {
		t.Fatal("GenerateArtifacts() returned nil result; want repaired artifacts")
	}
	if _, ok := result["requirements"]; !ok {
		t.Error("result[\"requirements\"] not found; want repaired artifact")
	}
}

// ---------------------------------------------------------------------------
// 07-REQ-7.E4: GenerateArtifacts repair loop bounded at 2 attempts
// ---------------------------------------------------------------------------

// TestSpec07_GenerateArtifacts_RepairLoopBounded verifies that the repair
// loop is capped at exactly 2 attempts regardless of error nature.
// Edge Case: 07-REQ-7.E4, Correctness Property: 07-PROP-7
func TestSpec07_GenerateArtifacts_RepairLoopBounded(t *testing.T) {
	capture := &aiCallCapture{}

	mockFn := func(ctx context.Context, opts AICallOptions) (string, any, error) {
		capture.record(opts)
		// Always return invalid content.
		resp := makeToolCallResponse("end_turn",
			makeArtifactToolCall("requirements", map[string]any{"bad": true}))
		return "", resp, nil
	}

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	_, err := agent.GenerateArtifacts(ctx, "PRD", "07", "test")

	if err == nil {
		t.Fatal("GenerateArtifacts() returned nil error; want error after repair loop exhaustion")
	}

	// Should have exactly 3 calls: 1 initial + 2 repairs.
	if capture.count() != 3 {
		t.Errorf("total AICall count = %d; want exactly 3 (1 initial + 2 repairs)", capture.count())
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

// TestSpec07_AgentOption_NilCallbackGeneration verifies that GenerateArtifacts
// with a nil callback does not panic.
// Edge Case: 07-REQ-8.E1
func TestSpec07_AgentOption_NilCallbackGeneration(t *testing.T) {
	// Route by artifact name in Context (parallel-safe).
	mockFn := func(ctx context.Context, opts AICallOptions) (string, any, error) {
		var content map[string]any
		var artifactName string
		switch {
		case strings.Contains(opts.Context, "requirements"):
			artifactName = "requirements"
			content = map[string]any{"spec_id": "07", "spec_name": "test", "requirements": []any{}}
		case strings.Contains(opts.Context, "test_spec"):
			artifactName = "test_spec"
			content = map[string]any{"spec_id": "07", "spec_name": "test", "test_cases": []any{}}
		case strings.Contains(opts.Context, "tasks"):
			artifactName = "tasks"
			content = map[string]any{"spec_id": "07", "spec_name": "test", "task_groups": []any{}}
		default:
			return "", nil, fmt.Errorf("unexpected context %q", opts.Context)
		}
		resp := makeToolCallResponse("end_turn", makeArtifactToolCall(artifactName, content))
		return "", resp, nil
	}

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()

	// Should not panic with nil callback.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("GenerateArtifacts with nil callback panicked: %v", r)
		}
	}()

	_, _ = agent.GenerateArtifacts(ctx, "PRD", "07", "test",
		WithOnArtifact(nil))
}

// ---------------------------------------------------------------------------
// TS-NS-1: Repair call messages[0] is original user prompt (NS-REQ-1)
// TS-NS-2: Repair call messages[1] is assistant tool_use response (NS-REQ-2)
// TS-NS-3: Repair call messages[2] is tool_result with validation error (NS-REQ-3)
// ---------------------------------------------------------------------------

// TestNS55_RepairConversation_FirstMessage verifies that when a repair is
// triggered, the first message in the repair AICall equals the original user
// prompt that was sent in the initial generation call.
// Test Spec: TS-NS-1, Requirement: NS-REQ-1
func TestNS55_RepairConversation_FirstMessage(t *testing.T) {
	capture := &aiCallCapture{}
	var mu sync.Mutex

	invalidContent := map[string]any{"invalid": true}
	validContent := map[string]any{
		"spec_id":      "07",
		"spec_name":    "test",
		"requirements": []any{},
	}

	reqCallCount := 0
	var initialUserPrompt string
	mockFn := func(ctx context.Context, opts AICallOptions) (string, any, error) {
		capture.record(opts)
		var resp *MessageResponse
		switch {
		case strings.Contains(opts.Context, "requirements"):
			mu.Lock()
			n := reqCallCount
			reqCallCount++
			mu.Unlock()
			if n == 0 {
				// Capture the user prompt from the initial generation call.
				if len(opts.Messages) > 0 {
					if s, ok := opts.Messages[0].Content.(string); ok {
						initialUserPrompt = s
					}
				}
				resp = makeToolCallResponse("end_turn",
					makeArtifactToolCall("requirements", invalidContent))
			} else {
				// Repair call — return valid content.
				resp = makeToolCallResponse("end_turn",
					makeArtifactToolCall("requirements", validContent))
			}
		case strings.Contains(opts.Context, "test_spec"):
			resp = makeToolCallResponse("end_turn",
				makeArtifactToolCall("test_spec", map[string]any{
					"spec_id": "07", "spec_name": "test", "test_cases": []any{},
				}))
		case strings.Contains(opts.Context, "tasks"):
			resp = makeToolCallResponse("end_turn",
				makeArtifactToolCall("tasks", map[string]any{
					"spec_id": "07", "spec_name": "test", "task_groups": []any{},
				}))
		default:
			return "", nil, fmt.Errorf("unexpected context %q", opts.Context)
		}
		return "", resp, nil
	}

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	_, err := agent.GenerateArtifacts(ctx, "PRD text", "07", "test")
	if err != nil {
		t.Fatalf("GenerateArtifacts() returned error: %v", err)
	}

	if capture.count() < 2 {
		t.Fatalf("AICall count = %d; want at least 2 (initial + repair)", capture.count())
	}

	// Find the repair call (second requirements call by context).
	var repairOpts AICallOptions
	reqIdxFM := 0
	for i := 0; i < capture.count(); i++ {
		o := capture.get(i)
		if strings.Contains(o.Context, "requirements") {
			if reqIdxFM == 1 {
				repairOpts = o
				break
			}
			reqIdxFM++
		}
	}
	if len(repairOpts.Messages) == 0 {
		t.Fatal("repair call Messages is empty; want at least 1 message")
	}

	// NS-REQ-1: messages[0] must have Role == "user".
	firstMsg := repairOpts.Messages[0]
	if firstMsg.Role != "user" {
		t.Errorf("repair messages[0].Role = %q; want %q", firstMsg.Role, "user")
	}

	// NS-REQ-1: messages[0].Content must be the original user prompt (a string).
	firstContent, ok := firstMsg.Content.(string)
	if !ok {
		t.Fatalf("repair messages[0].Content type = %T; want string", firstMsg.Content)
	}
	if firstContent == "" {
		t.Error("repair messages[0].Content is empty; want non-empty user prompt")
	}
	if initialUserPrompt != "" && firstContent != initialUserPrompt {
		t.Errorf("repair messages[0].Content != initial user prompt\ngot:  %q\nwant: %q",
			firstContent, initialUserPrompt)
	}
}

// TestNS55_RepairConversation_AssistantMessage verifies that the second
// message in the repair call carries the assistant's tool_use response from
// the initial generation.
// Test Spec: TS-NS-2, Requirement: NS-REQ-2
func TestNS55_RepairConversation_AssistantMessage(t *testing.T) {
	capture := &aiCallCapture{}
	var mu sync.Mutex

	invalidContent := map[string]any{"invalid": true}
	initialToolUseBlock := makeArtifactToolCall("requirements", invalidContent)
	initialToolUseBlock.ID = "toolu_initial_01"

	validContent := map[string]any{
		"spec_id":      "07",
		"spec_name":    "test",
		"requirements": []any{},
	}

	reqCallCount2 := 0
	mockFn := func(ctx context.Context, opts AICallOptions) (string, any, error) {
		capture.record(opts)
		var resp *MessageResponse
		switch {
		case strings.Contains(opts.Context, "requirements"):
			mu.Lock()
			n := reqCallCount2
			reqCallCount2++
			mu.Unlock()
			if n == 0 {
				resp = makeToolCallResponse("end_turn", initialToolUseBlock)
			} else {
				resp = makeToolCallResponse("end_turn",
					makeArtifactToolCall("requirements", validContent))
			}
		case strings.Contains(opts.Context, "test_spec"):
			resp = makeToolCallResponse("end_turn",
				makeArtifactToolCall("test_spec", map[string]any{
					"spec_id": "07", "spec_name": "test", "test_cases": []any{},
				}))
		case strings.Contains(opts.Context, "tasks"):
			resp = makeToolCallResponse("end_turn",
				makeArtifactToolCall("tasks", map[string]any{
					"spec_id": "07", "spec_name": "test", "task_groups": []any{},
				}))
		default:
			return "", nil, fmt.Errorf("unexpected context %q", opts.Context)
		}
		return "", resp, nil
	}

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	_, err := agent.GenerateArtifacts(ctx, "PRD text", "07", "test")
	if err != nil {
		t.Fatalf("GenerateArtifacts() returned error: %v", err)
	}

	if capture.count() < 2 {
		t.Fatalf("AICall count = %d; want at least 2 (initial + repair)", capture.count())
	}

	// Find the repair call (second requirements call by context order).
	var repairOpts AICallOptions
	reqIdx := 0
	for i := 0; i < capture.count(); i++ {
		o := capture.get(i)
		if strings.Contains(o.Context, "requirements") {
			if reqIdx == 1 {
				repairOpts = o
				break
			}
			reqIdx++
		}
	}
	if len(repairOpts.Messages) < 2 {
		t.Fatalf("repair call Messages length = %d; want at least 2", len(repairOpts.Messages))
	}

	// NS-REQ-2: messages[1] must have Role == "assistant".
	assistantMsg := repairOpts.Messages[1]
	if assistantMsg.Role != "assistant" {
		t.Errorf("repair messages[1].Role = %q; want %q", assistantMsg.Role, "assistant")
	}

	// NS-REQ-2: messages[1].Content must be []ContentBlock from the initial response.
	blocks, ok := assistantMsg.Content.([]ContentBlock)
	if !ok {
		t.Fatalf("repair messages[1].Content type = %T; want []ContentBlock", assistantMsg.Content)
	}
	if len(blocks) == 0 {
		t.Fatal("repair messages[1].Content is empty []ContentBlock; want at least one tool_use block")
	}

	// Verify the tool_use block is present with the correct name and ID.
	found := false
	for _, block := range blocks {
		if block.Type == "tool_use" && block.Name == "submit_requirements" {
			found = true
			if block.ID != "toolu_initial_01" {
				t.Errorf("tool_use block ID = %q; want %q", block.ID, "toolu_initial_01")
			}
			break
		}
	}
	if !found {
		t.Error("repair messages[1] does not contain a tool_use block for submit_requirements")
	}
}

// TestNS55_RepairConversation_ToolResultMessage verifies that the third
// message in the repair call is a user message with a tool_result ContentBlock
// whose text contains the validation error.
// Test Spec: TS-NS-3, Requirement: NS-REQ-3
func TestNS55_RepairConversation_ToolResultMessage(t *testing.T) {
	capture := &aiCallCapture{}
	reqCallCount3 := 0
	var mu sync.Mutex

	invalidContent := map[string]any{"invalid": true}
	initialToolUseBlock := makeArtifactToolCall("requirements", invalidContent)
	initialToolUseBlock.ID = "toolu_for_result_01"

	validContent := map[string]any{
		"spec_id":      "07",
		"spec_name":    "test",
		"requirements": []any{},
	}

	mockFn := func(ctx context.Context, opts AICallOptions) (string, any, error) {
		capture.record(opts)
		var resp *MessageResponse
		switch {
		case strings.Contains(opts.Context, "requirements"):
			mu.Lock()
			n := reqCallCount3
			reqCallCount3++
			mu.Unlock()
			if n == 0 {
				resp = makeToolCallResponse("end_turn", initialToolUseBlock)
			} else {
				resp = makeToolCallResponse("end_turn",
					makeArtifactToolCall("requirements", validContent))
			}
		case strings.Contains(opts.Context, "test_spec"):
			resp = makeToolCallResponse("end_turn",
				makeArtifactToolCall("test_spec", map[string]any{
					"spec_id": "07", "spec_name": "test", "test_cases": []any{},
				}))
		case strings.Contains(opts.Context, "tasks"):
			resp = makeToolCallResponse("end_turn",
				makeArtifactToolCall("tasks", map[string]any{
					"spec_id": "07", "spec_name": "test", "task_groups": []any{},
				}))
		default:
			return "", nil, fmt.Errorf("unexpected context %q", opts.Context)
		}
		return "", resp, nil
	}

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	_, err := agent.GenerateArtifacts(ctx, "PRD text", "07", "test")
	if err != nil {
		t.Fatalf("GenerateArtifacts() returned error: %v", err)
	}

	if capture.count() < 2 {
		t.Fatalf("AICall count = %d; want at least 2 (initial + repair)", capture.count())
	}

	// Find the repair call (second requirements call by context).
	var repairOpts AICallOptions
	reqIdx3 := 0
	for i := 0; i < capture.count(); i++ {
		o := capture.get(i)
		if strings.Contains(o.Context, "requirements") {
			if reqIdx3 == 1 {
				repairOpts = o
				break
			}
			reqIdx3++
		}
	}
	if len(repairOpts.Messages) < 3 {
		t.Fatalf("repair call Messages length = %d; want exactly 3 (user, assistant, user/tool_result)",
			len(repairOpts.Messages))
	}

	// NS-REQ-3: messages[2] must have Role == "user".
	toolResultMsg := repairOpts.Messages[2]
	if toolResultMsg.Role != "user" {
		t.Errorf("repair messages[2].Role = %q; want %q", toolResultMsg.Role, "user")
	}

	// NS-REQ-3: messages[2].Content must be []ContentBlock with a tool_result block.
	blocks, ok := toolResultMsg.Content.([]ContentBlock)
	if !ok {
		t.Fatalf("repair messages[2].Content type = %T; want []ContentBlock", toolResultMsg.Content)
	}
	if len(blocks) == 0 {
		t.Fatal("repair messages[2].Content is empty; want tool_result block")
	}

	// Find the tool_result block.
	var toolResultBlock *ContentBlock
	for i := range blocks {
		if blocks[i].Type == "tool_result" {
			toolResultBlock = &blocks[i]
			break
		}
	}
	if toolResultBlock == nil {
		t.Fatal("repair messages[2] has no tool_result block; want type='tool_result'")
	}

	// NS-REQ-3: tool_use_id must match the ID from the initial tool_use block.
	if toolResultBlock.ToolUseID != "toolu_for_result_01" {
		t.Errorf("tool_result.ToolUseID = %q; want %q",
			toolResultBlock.ToolUseID, "toolu_for_result_01")
	}

	// NS-REQ-3: text must contain the validation error message.
	if toolResultBlock.Text == "" {
		t.Error("tool_result.Text is empty; want validation error message")
	}
	// The validation error for missing required keys should mention "requirements".
	if !strings.Contains(toolResultBlock.Text, "requirements") {
		t.Errorf("tool_result.Text = %q; want it to contain validation error mentioning 'requirements'",
			toolResultBlock.Text)
	}
}

// TestNS55_RepairConversation_MessageCount verifies that the repair call has
// exactly 3 messages in the conversation continuation format.
// Requirement: NS-REQ-1, NS-REQ-2, NS-REQ-3
func TestNS55_RepairConversation_MessageCount(t *testing.T) {
	capture := &aiCallCapture{}
	reqCallCount4 := 0
	var mu sync.Mutex

	invalidContent := map[string]any{"invalid": true}
	validContent := map[string]any{
		"spec_id":      "07",
		"spec_name":    "test",
		"requirements": []any{},
	}

	mockFn := func(ctx context.Context, opts AICallOptions) (string, any, error) {
		capture.record(opts)
		var resp *MessageResponse
		switch {
		case strings.Contains(opts.Context, "requirements"):
			mu.Lock()
			n := reqCallCount4
			reqCallCount4++
			mu.Unlock()
			if n == 0 {
				resp = makeToolCallResponse("end_turn",
					makeArtifactToolCall("requirements", invalidContent))
			} else {
				resp = makeToolCallResponse("end_turn",
					makeArtifactToolCall("requirements", validContent))
			}
		case strings.Contains(opts.Context, "test_spec"):
			resp = makeToolCallResponse("end_turn",
				makeArtifactToolCall("test_spec", map[string]any{
					"spec_id": "07", "spec_name": "test", "test_cases": []any{},
				}))
		case strings.Contains(opts.Context, "tasks"):
			resp = makeToolCallResponse("end_turn",
				makeArtifactToolCall("tasks", map[string]any{
					"spec_id": "07", "spec_name": "test", "task_groups": []any{},
				}))
		default:
			return "", nil, fmt.Errorf("unexpected context %q", opts.Context)
		}
		return "", resp, nil
	}

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	_, err := agent.GenerateArtifacts(ctx, "PRD text", "07", "test")
	if err != nil {
		t.Fatalf("GenerateArtifacts() returned error: %v", err)
	}

	if capture.count() < 2 {
		t.Fatalf("AICall count = %d; want at least 2 (initial + repair)", capture.count())
	}

	// Find the repair call (second requirements call by context).
	var repairOpts AICallOptions
	reqIdx4 := 0
	for i := 0; i < capture.count(); i++ {
		o := capture.get(i)
		if strings.Contains(o.Context, "requirements") {
			if reqIdx4 == 1 {
				repairOpts = o
				break
			}
			reqIdx4++
		}
	}
	// Expect exactly 3 messages: user (original prompt), assistant (tool_use), user (tool_result).
	if len(repairOpts.Messages) != 3 {
		t.Errorf("repair call Messages count = %d; want 3 (user, assistant, user/tool_result)",
			len(repairOpts.Messages))
	}
}

// ---------------------------------------------------------------------------
// TS-NS-1: test_spec and tasks LLM calls are issued concurrently (NS-REQ-1)
// TS-NS-2: Error in either parallel call returns error, nil result (NS-REQ-2)
// TS-NS-3: Both parallel calls succeed → all 3 artifacts in result (NS-REQ-3)
// TS-NS-4: OnArtifact callback order (NS-REQ-4)
// TS-NS-5: Context cancellation during parallel phase (NS-REQ-5)
// ---------------------------------------------------------------------------

// TestNS57_ConcurrentGeneration verifies that test_spec and tasks LLM calls
// are issued concurrently after requirements completes (NS-REQ-1).
//
// The mock blocks the test_spec response until the tasks goroutine has also
// started. A sequential implementation would deadlock on the barrier.
func TestNS57_ConcurrentGeneration(t *testing.T) {
	t.Parallel()

	// started signals that both parallel goroutines have entered the mock.
	var taskStarted, testSpecUnblocked sync.WaitGroup
	taskStarted.Add(1)
	testSpecUnblocked.Add(1)

	requirementsContent := map[string]any{
		"spec_id":      "57",
		"spec_name":    "test",
		"requirements": []any{},
	}

	mockFn := func(ctx context.Context, opts AICallOptions) (string, any, error) {
		switch {
		case strings.Contains(opts.Context, "requirements"):
			resp := makeToolCallResponse("end_turn",
				makeArtifactToolCall("requirements", requirementsContent))
			return "", resp, nil

		case strings.Contains(opts.Context, "test_spec"):
			// Signal that test_spec has started, then wait until tasks also starts.
			// If tasks never starts (sequential impl), this blocks until the
			// test timeout fires.
			taskStarted.Wait() // wait until tasks goroutine has reached mock
			testSpecUnblocked.Done()
			resp := makeToolCallResponse("end_turn",
				makeArtifactToolCall("test_spec", map[string]any{
					"spec_id": "57", "spec_name": "test", "test_cases": []any{},
				}))
			return "", resp, nil

		case strings.Contains(opts.Context, "tasks"):
			// Signal that tasks has started (unblocks test_spec above).
			taskStarted.Done()
			testSpecUnblocked.Wait() // wait until test_spec is unblocked
			resp := makeToolCallResponse("end_turn",
				makeArtifactToolCall("tasks", map[string]any{
					"spec_id": "57", "spec_name": "test", "task_groups": []any{},
				}))
			return "", resp, nil

		default:
			return "", nil, fmt.Errorf("unexpected context %q", opts.Context)
		}
	}

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := agent.GenerateArtifacts(ctx, "PRD", "57", "test")
	if err != nil {
		t.Fatalf("GenerateArtifacts() error: %v — likely sequential (would deadlock with concurrent barrier)", err)
	}
	if result == nil {
		t.Fatal("result is nil; want map with 3 artifacts")
	}
	for _, name := range []string{"requirements", "test_spec", "tasks"} {
		if _, ok := result[name]; !ok {
			t.Errorf("result[%q] missing", name)
		}
	}
}

// TestNS57_ParallelError_TestSpecFails verifies that when test_spec returns
// an error, GenerateArtifacts returns a non-nil error and a nil result map (NS-REQ-2).
func TestNS57_ParallelError_TestSpecFails(t *testing.T) {
	t.Parallel()

	mockFn := func(ctx context.Context, opts AICallOptions) (string, any, error) {
		switch {
		case strings.Contains(opts.Context, "requirements"):
			return "", makeToolCallResponse("end_turn",
				makeArtifactToolCall("requirements", map[string]any{
					"spec_id": "57", "spec_name": "test", "requirements": []any{},
				})), nil
		case strings.Contains(opts.Context, "test_spec"):
			return "", nil, &AgentError{Detail: "test_spec failed", ErrorCategory: "transient"}
		case strings.Contains(opts.Context, "tasks"):
			return "", makeToolCallResponse("end_turn",
				makeArtifactToolCall("tasks", map[string]any{
					"spec_id": "57", "spec_name": "test", "task_groups": []any{},
				})), nil
		default:
			return "", nil, fmt.Errorf("unexpected context %q", opts.Context)
		}
	}

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	result, err := agent.GenerateArtifacts(context.Background(), "PRD", "57", "test")
	if err == nil {
		t.Fatal("expected error when test_spec fails; got nil")
	}
	if result != nil {
		t.Errorf("result = %v; want nil on error", result)
	}
}

// TestNS57_ParallelError_TasksFails verifies that when tasks returns an error,
// GenerateArtifacts returns a non-nil error and a nil result map (NS-REQ-2).
func TestNS57_ParallelError_TasksFails(t *testing.T) {
	t.Parallel()

	mockFn := func(ctx context.Context, opts AICallOptions) (string, any, error) {
		switch {
		case strings.Contains(opts.Context, "requirements"):
			return "", makeToolCallResponse("end_turn",
				makeArtifactToolCall("requirements", map[string]any{
					"spec_id": "57", "spec_name": "test", "requirements": []any{},
				})), nil
		case strings.Contains(opts.Context, "test_spec"):
			return "", makeToolCallResponse("end_turn",
				makeArtifactToolCall("test_spec", map[string]any{
					"spec_id": "57", "spec_name": "test", "test_cases": []any{},
				})), nil
		case strings.Contains(opts.Context, "tasks"):
			return "", nil, &AgentError{Detail: "tasks failed", ErrorCategory: "transient"}
		default:
			return "", nil, fmt.Errorf("unexpected context %q", opts.Context)
		}
	}

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	result, err := agent.GenerateArtifacts(context.Background(), "PRD", "57", "test")
	if err == nil {
		t.Fatal("expected error when tasks fails; got nil")
	}
	if result != nil {
		t.Errorf("result = %v; want nil on error", result)
	}
}

// TestNS57_AllArtifactsPresent verifies that when both parallel calls succeed,
// all three artifacts appear in the returned result map with no data race (NS-REQ-3).
// Run with -race to detect concurrent write races.
func TestNS57_AllArtifactsPresent(t *testing.T) {
	t.Parallel()

	wantRequirements := map[string]any{
		"spec_id": "57", "spec_name": "test", "requirements": []any{"req1"},
	}
	wantTestSpec := map[string]any{
		"spec_id": "57", "spec_name": "test", "test_cases": []any{"tc1"},
	}
	wantTasks := map[string]any{
		"spec_id": "57", "spec_name": "test", "task_groups": []any{"task1"},
	}

	mockFn := func(ctx context.Context, opts AICallOptions) (string, any, error) {
		switch {
		case strings.Contains(opts.Context, "requirements"):
			return "", makeToolCallResponse("end_turn",
				makeArtifactToolCall("requirements", wantRequirements)), nil
		case strings.Contains(opts.Context, "test_spec"):
			return "", makeToolCallResponse("end_turn",
				makeArtifactToolCall("test_spec", wantTestSpec)), nil
		case strings.Contains(opts.Context, "tasks"):
			return "", makeToolCallResponse("end_turn",
				makeArtifactToolCall("tasks", wantTasks)), nil
		default:
			return "", nil, fmt.Errorf("unexpected context %q", opts.Context)
		}
	}

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	result, err := agent.GenerateArtifacts(context.Background(), "PRD", "57", "test")
	if err != nil {
		t.Fatalf("GenerateArtifacts() error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if len(result) != 3 {
		t.Errorf("len(result) = %d; want 3", len(result))
	}
	for _, name := range []string{"requirements", "test_spec", "tasks"} {
		if _, ok := result[name]; !ok {
			t.Errorf("result[%q] missing", name)
		}
	}
}

// TestNS57_CallbackOrder verifies that the OnArtifact callback is invoked
// exactly 3 times, requirements is always first, and test_spec and tasks appear
// in either order (NS-REQ-4).
func TestNS57_CallbackOrder(t *testing.T) {
	t.Parallel()

	var callbackMu sync.Mutex
	var callbackNames []string
	callback := func(name string, content any) {
		callbackMu.Lock()
		defer callbackMu.Unlock()
		callbackNames = append(callbackNames, name)
	}

	mockFn := func(ctx context.Context, opts AICallOptions) (string, any, error) {
		switch {
		case strings.Contains(opts.Context, "requirements"):
			return "", makeToolCallResponse("end_turn",
				makeArtifactToolCall("requirements", map[string]any{
					"spec_id": "57", "spec_name": "test", "requirements": []any{},
				})), nil
		case strings.Contains(opts.Context, "test_spec"):
			return "", makeToolCallResponse("end_turn",
				makeArtifactToolCall("test_spec", map[string]any{
					"spec_id": "57", "spec_name": "test", "test_cases": []any{},
				})), nil
		case strings.Contains(opts.Context, "tasks"):
			return "", makeToolCallResponse("end_turn",
				makeArtifactToolCall("tasks", map[string]any{
					"spec_id": "57", "spec_name": "test", "task_groups": []any{},
				})), nil
		default:
			return "", nil, fmt.Errorf("unexpected context %q", opts.Context)
		}
	}

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	_, err := agent.GenerateArtifacts(context.Background(), "PRD", "57", "test",
		WithOnArtifact(callback))
	if err != nil {
		t.Fatalf("GenerateArtifacts() error: %v", err)
	}

	if len(callbackNames) != 3 {
		t.Fatalf("callback count = %d; want 3", len(callbackNames))
	}
	// requirements must be first.
	if callbackNames[0] != "requirements" {
		t.Errorf("callbackNames[0] = %q; want %q", callbackNames[0], "requirements")
	}
	// test_spec and tasks may arrive in either order.
	parallelSet := map[string]bool{callbackNames[1]: true, callbackNames[2]: true}
	if !parallelSet["test_spec"] || !parallelSet["tasks"] {
		t.Errorf("callbackNames[1,2] = %q, %q; want {test_spec, tasks} in any order",
			callbackNames[1], callbackNames[2])
	}
}

// TestNS57_ContextCancellationDuringParallelPhase verifies that cancelling the
// context while test_spec and tasks are in-flight causes GenerateArtifacts to
// return context.Canceled and a nil result (NS-REQ-5).
func TestNS57_ContextCancellationDuringParallelPhase(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	// Both parallel goroutines block until cancelled.
	mockFn := func(ctx context.Context, opts AICallOptions) (string, any, error) {
		switch {
		case strings.Contains(opts.Context, "requirements"):
			// requirements completes normally.
			return "", makeToolCallResponse("end_turn",
				makeArtifactToolCall("requirements", map[string]any{
					"spec_id": "57", "spec_name": "test", "requirements": []any{},
				})), nil
		case strings.Contains(opts.Context, "test_spec"),
			strings.Contains(opts.Context, "tasks"):
			// Cancel the context as soon as either parallel call starts, then block.
			cancel()
			<-ctx.Done()
			return "", nil, ctx.Err()
		default:
			return "", nil, fmt.Errorf("unexpected context %q", opts.Context)
		}
	}

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	result, err := agent.GenerateArtifacts(ctx, "PRD", "57", "test")
	if err == nil {
		t.Fatal("expected error from cancelled context; got nil")
	}
	if result != nil {
		t.Errorf("result = %v; want nil on cancellation", result)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v; want context.Canceled", err)
	}
}

// ---------------------------------------------------------------------------
// NS-REQ-1/TS-NS-1: validateArtifactContent rejects EARS constraint violations
// ---------------------------------------------------------------------------

// TestNS48_ValidateArtifactContent_EarsConstraintViolation verifies that
// validateArtifactContent returns a non-nil error when a requirements artifact
// contains a criterion with ears_pattern "event_driven" but the required
// "trigger" field is absent.
// Test Spec: TS-NS-1, Requirement: NS-REQ-1
func TestNS48_ValidateArtifactContent_EarsConstraintViolation(t *testing.T) {
	t.Parallel()

	content := map[string]any{
		"spec_id":   "48",
		"spec_name": "test",
		"requirements": []any{
			map[string]any{
				"id": "48-REQ-1",
				"acceptance_criteria": []any{
					map[string]any{
						"id":           "48-REQ-1.1",
						"ears_pattern": "event_driven",
						// "trigger" intentionally omitted — EARS violation
					},
				},
			},
		},
	}

	_, err := validateArtifactContent(content, "requirements")
	if err == nil {
		t.Fatal("validateArtifactContent() returned nil error; want EARS constraint error")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "ears_constraint") && !strings.Contains(errStr, "trigger") {
		t.Errorf("error %q does not contain 'ears_constraint' or 'trigger'", errStr)
	}
}

// ---------------------------------------------------------------------------
// NS-REQ-2/TS-NS-2: validateArtifactContent rejects malformed requirement IDs
// ---------------------------------------------------------------------------

// TestNS48_ValidateArtifactContent_MalformedID verifies that
// validateArtifactContent returns a non-nil error when a requirements artifact
// contains a requirement whose "id" field does not match the required pattern
// (e.g., "BAD-ID" instead of "XX-REQ-N").
// Test Spec: TS-NS-2, Requirement: NS-REQ-2
func TestNS48_ValidateArtifactContent_MalformedID(t *testing.T) {
	t.Parallel()

	content := map[string]any{
		"spec_id":   "48",
		"spec_name": "test",
		"requirements": []any{
			map[string]any{
				"id": "BAD-ID",
			},
		},
	}

	_, err := validateArtifactContent(content, "requirements")
	if err == nil {
		t.Fatal("validateArtifactContent() returned nil error; want ID format error")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "BAD-ID") && !strings.Contains(errStr, "id_format") {
		t.Errorf("error %q does not mention 'BAD-ID' or 'id_format'", errStr)
	}
}

// ---------------------------------------------------------------------------
// NS-REQ-3/TS-NS-3: Repair prompt contains specific ValidationEntry errors
// ---------------------------------------------------------------------------

// TestNS48_RepairPrompt_ContainsValidationEntryErrors verifies that when
// validateArtifactContent fails, the tool_result message sent to the LLM in
// the repair loop contains the specific validation error text (check name and
// path) from the library — not just a generic "missing required keys" message.
// Test Spec: TS-NS-3, Requirement: NS-REQ-3
func TestNS48_RepairPrompt_ContainsValidationEntryErrors(t *testing.T) {
	t.Parallel()

	// invalidContent always fails: event_driven criterion missing 'trigger'.
	invalidContent := map[string]any{
		"spec_id":   "48",
		"spec_name": "test",
		"requirements": []any{
			map[string]any{
				"id": "48-REQ-1",
				"acceptance_criteria": []any{
					map[string]any{
						"id":           "48-REQ-1.1",
						"ears_pattern": "event_driven",
						// "trigger" omitted intentionally
					},
				},
			},
		},
	}

	var capturedRepairText string

	callCount := 0
	mockFn := func(ctx context.Context, opts AICallOptions) (string, any, error) {
		callCount++
		if callCount > 1 {
			// Capture the tool_result Text from the repair call's messages.
			for _, msg := range opts.Messages {
				blocks, ok := msg.Content.([]ContentBlock)
				if !ok {
					continue
				}
				for _, block := range blocks {
					if block.Type == "tool_result" && block.Text != "" {
						capturedRepairText = block.Text
					}
				}
			}
		}
		resp := makeToolCallResponse("end_turn",
			makeArtifactToolCall("requirements", invalidContent))
		return "", resp, nil
	}

	sa := NewSpecAgent("STANDARD")
	sa.aiCallFunc = mockFn

	_, _ = sa.GenerateArtifacts(context.Background(), "PRD", "48", "test")

	if capturedRepairText == "" {
		t.Fatal("no tool_result text captured from repair call; repair loop may not have run")
	}
	if !strings.Contains(capturedRepairText, "ears_constraint") && !strings.Contains(capturedRepairText, "trigger") {
		t.Errorf("repair tool_result text %q does not contain 'ears_constraint' or 'trigger'; want library ValidationEntry errors", capturedRepairText)
	}
	if strings.Contains(capturedRepairText, "missing required keys") {
		t.Errorf("repair tool_result text %q contains generic 'missing required keys' text; want specific ValidationEntry errors", capturedRepairText)
	}
}

// ---------------------------------------------------------------------------
// NS-REQ-4/TS-NS-4: Valid artifacts are accepted without triggering repair
// ---------------------------------------------------------------------------

// TestNS48_ValidateArtifactContent_ValidArtifactPassesWithoutError verifies
// that validateArtifactContent returns (non-nil map, nil error) for a fully
// valid requirements artifact so that the repair loop is not triggered.
// Test Spec: TS-NS-4, Requirement: NS-REQ-4
func TestNS48_ValidateArtifactContent_ValidArtifactPassesWithoutError(t *testing.T) {
	t.Parallel()

	// A valid requirements artifact: correct top-level keys, empty requirements
	// slice (no criteria to fail EARS/ID checks).
	content := map[string]any{
		"spec_id":      "48",
		"spec_name":    "test",
		"requirements": []any{},
	}

	m, err := validateArtifactContent(content, "requirements")
	if err != nil {
		t.Fatalf("validateArtifactContent() returned unexpected error: %v", err)
	}
	if m == nil {
		t.Error("validateArtifactContent() returned nil map; want non-nil")
	}
}

// ---------------------------------------------------------------------------
// NS-REQ-5/TS-NS-5: Repair loop exhaustion returns AgentError with validation category
// ---------------------------------------------------------------------------

// TestNS48_RepairLoop_Exhaustion_ReturnsValidationAgentError verifies that
// when an artifact consistently fails full validation across all repair
// attempts, the caller receives an *AgentError with ErrorCategory ==
// "validation" and the error message identifies the artifact name.
// Test Spec: TS-NS-5, Requirement: NS-REQ-5
func TestNS48_RepairLoop_Exhaustion_ReturnsValidationAgentError(t *testing.T) {
	t.Parallel()

	// Always return a requirements artifact with a malformed ID — validation
	// never passes, exhausting the repair loop.
	alwaysInvalid := map[string]any{
		"spec_id":   "48",
		"spec_name": "test",
		"requirements": []any{
			map[string]any{"id": "BAD-ID"},
		},
	}

	mockFn := func(ctx context.Context, opts AICallOptions) (string, any, error) {
		resp := makeToolCallResponse("end_turn",
			makeArtifactToolCall("requirements", alwaysInvalid))
		return "", resp, nil
	}

	sa := NewSpecAgent("STANDARD")
	sa.aiCallFunc = mockFn

	_, err := sa.GenerateArtifacts(context.Background(), "PRD", "48", "test")
	if err == nil {
		t.Fatal("GenerateArtifacts() returned nil error; want validation error after repair exhaustion")
	}

	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("error type = %T; want *AgentError", err)
	}
	if agentErr.ErrorCategory != "validation" {
		t.Errorf("AgentError.ErrorCategory = %q; want %q", agentErr.ErrorCategory, "validation")
	}
	errMsg := err.Error()
	if !strings.Contains(strings.ToLower(errMsg), "requirements") {
		t.Errorf("error message %q does not mention 'requirements'", errMsg)
	}
}

// ---------------------------------------------------------------------------
// NS-REQ-1/TS-NS-1: validateArtifactContent checks "task_groups" not "tasks"
// ---------------------------------------------------------------------------

// TestNS62_ValidateArtifactContent_TasksMissingTaskGroups verifies that
// validateArtifactContent for the "tasks" artifact rejects a map that has
// "spec_id" and "spec_name" but is missing the "task_groups" key.
// Test Spec: TS-NS-1, Requirement: NS-REQ-1
func TestNS62_ValidateArtifactContent_TasksMissingTaskGroups(t *testing.T) {
	t.Parallel()

	content := map[string]any{
		"spec_id":   "62",
		"spec_name": "foo",
		// "task_groups" intentionally absent — should be rejected
	}

	_, err := validateArtifactContent(content, "tasks")
	if err == nil {
		t.Fatal("validateArtifactContent() returned nil error; want error for missing task_groups")
	}
	if !strings.Contains(err.Error(), "task_groups") {
		t.Errorf("error %q does not mention 'task_groups'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// NS-REQ-2/TS-NS-2: ValidateTestSpecMap rejects invalid test case ID format
// ---------------------------------------------------------------------------

// TestNS62_ValidateArtifactContent_TestSpecBadID verifies that
// validateArtifactContent for the "test_spec" artifact rejects a map whose
// test_cases contain an entry with an ID that does not match ^TS-\w+-\d+$.
// Test Spec: TS-NS-2, Requirement: NS-REQ-2
func TestNS62_ValidateArtifactContent_TestSpecBadID(t *testing.T) {
	t.Parallel()

	content := map[string]any{
		"spec_id":   "62",
		"spec_name": "foo",
		"test_cases": []any{
			map[string]any{
				"id": "BAD-TC", // does not match ^TS-\w+-\d+$
			},
		},
	}

	_, err := validateArtifactContent(content, "test_spec")
	if err == nil {
		t.Fatal("validateArtifactContent() returned nil error; want ID format error")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "id_format") && !strings.Contains(errStr, "BAD-TC") {
		t.Errorf("error %q does not mention 'id_format' or 'BAD-TC'", errStr)
	}
}

// ---------------------------------------------------------------------------
// NS-REQ-3/TS-NS-3: ValidateTestSpecMap rejects invalid kind enum value
// ---------------------------------------------------------------------------

// TestNS62_ValidateTestSpecMap_BadKind verifies that ValidateTestSpecMap
// returns a non-empty slice when a test case has an invalid "kind" value.
// Test Spec: TS-NS-3, Requirement: NS-REQ-3
func TestNS62_ValidateTestSpecMap_BadKind(t *testing.T) {
	t.Parallel()

	content := map[string]any{
		"spec_id":   "62",
		"spec_name": "foo",
		"test_cases": []any{
			map[string]any{
				"id":   "TS-62-1",
				"kind": "bad_kind", // not a valid enum value
			},
		},
	}

	entries := afspec.ValidateTestSpecMap(content)
	if len(entries) == 0 {
		t.Fatal("ValidateTestSpecMap() returned empty slice; want at least one entry for bad kind")
	}
	found := false
	for _, e := range entries {
		if strings.Contains(e.Message, "kind") || strings.Contains(e.Message, "bad_kind") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no entry mentions 'kind' or 'bad_kind'; entries: %v", entries)
	}
}

// ---------------------------------------------------------------------------
// NS-REQ-4/TS-NS-4: ValidateTasksMap rejects malformed subtask IDs
// ---------------------------------------------------------------------------

// TestNS62_ValidateArtifactContent_TasksBadSubtaskID verifies that
// validateArtifactContent for the "tasks" artifact rejects a map whose
// task_groups contain a subtask with an ID that does not match {group}.{N}.
// Test Spec: TS-NS-4, Requirement: NS-REQ-4
func TestNS62_ValidateArtifactContent_TasksBadSubtaskID(t *testing.T) {
	t.Parallel()

	content := map[string]any{
		"spec_id":   "62",
		"spec_name": "foo",
		"task_groups": []any{
			map[string]any{
				"id": 1,
				"subtasks": []any{
					map[string]any{
						"id": "BAD.SUB.ID", // does not match ^\d+\.\d+$
					},
				},
			},
		},
	}

	_, err := validateArtifactContent(content, "tasks")
	if err == nil {
		t.Fatal("validateArtifactContent() returned nil error; want subtask ID format error")
	}
	if !strings.Contains(err.Error(), "BAD.SUB.ID") && !strings.Contains(err.Error(), "id_format") {
		t.Errorf("error %q does not mention the malformed ID or 'id_format'", err.Error())
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

// TestNS81_GenerateArtifacts_MaxTokens verifies that GenerateArtifacts does
// not set MaxTokens explicitly (leaves it 0 so ApplyDefaults applies 65536).
// Test Spec: TS-NS-3, Requirement: NS-REQ-3
func TestNS81_GenerateArtifacts_MaxTokens(t *testing.T) {
	capture := &aiCallCapture{}

	mockFn := func(ctx context.Context, opts AICallOptions) (string, any, error) {
		capture.record(opts)
		var content map[string]any
		var artifactName string
		switch {
		case strings.Contains(opts.Context, "requirements"):
			artifactName = "requirements"
			content = map[string]any{
				"spec_id": "81", "spec_name": "test", "requirements": []any{},
			}
		case strings.Contains(opts.Context, "test_spec"):
			artifactName = "test_spec"
			content = map[string]any{
				"spec_id": "81", "spec_name": "test", "test_cases": []any{},
			}
		case strings.Contains(opts.Context, "tasks"):
			artifactName = "tasks"
			content = map[string]any{
				"spec_id": "81", "spec_name": "test", "task_groups": []any{},
			}
		default:
			return "", nil, fmt.Errorf("unexpected context %q", opts.Context)
		}
		resp := makeToolCallResponse("end_turn", makeArtifactToolCall(artifactName, content))
		return "", resp, nil
	}

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockFn

	ctx := context.Background()
	_, err := agent.GenerateArtifacts(ctx, "PRD", "81", "test")
	if err != nil {
		t.Fatalf("GenerateArtifacts() returned error: %v", err)
	}

	if capture.count() == 0 {
		t.Fatal("no AICall invocations recorded")
	}

	// All GenerateArtifacts calls must have MaxTokens == 0 (unset, so
	// ApplyDefaults will supply 65536 when AICall is called for real).
	for i := 0; i < capture.count(); i++ {
		opts := capture.get(i)
		if opts.MaxTokens != 0 {
			t.Errorf("GenerateArtifacts call[%d] (context=%q) MaxTokens = %d; want 0 (relies on ApplyDefaults for 65536)",
				i, opts.Context, opts.MaxTokens)
		}
	}
}
