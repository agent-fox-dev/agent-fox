package agentspec

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Mock infrastructure
// ---------------------------------------------------------------------------

type mockResult struct {
	resp *MessageResponse
	err  error
}

// mockDoer implements the Doer interface for testing AICall.
type mockDoer struct {
	calls   []MessageRequest
	results []mockResult
}

func newMockDoer(results ...mockResult) *mockDoer {
	return &mockDoer{results: results}
}

func newSuccessDoer(text string) *mockDoer {
	return newMockDoer(mockResult{
		resp: &MessageResponse{
			Content: []ContentBlock{{Type: "text", Text: text}},
		},
	})
}

func (m *mockDoer) CreateMessage(ctx context.Context, req MessageRequest) (*MessageResponse, error) {
	m.calls = append(m.calls, req)
	idx := len(m.calls) - 1
	if idx < len(m.results) {
		return m.results[idx].resp, m.results[idx].err
	}
	// Default: success with generic response.
	return &MessageResponse{
		Content: []ContentBlock{{Type: "text", Text: "default mock response"}},
	}, nil
}

// delayCapturer records time.Duration values for verifying retry delays.
type delayCapturer struct {
	delays []time.Duration
}

func (d *delayCapturer) sleep(dur time.Duration) {
	d.delays = append(d.delays, dur)
}

// ---------------------------------------------------------------------------
// CachePolicy constants
// ---------------------------------------------------------------------------

// TestSpec07_CachePolicy_Constants verifies that CachePolicy constants
// have the correct string values.
// Requirement: 07-REQ-2.2
func TestSpec07_CachePolicy_Constants(t *testing.T) {
	tests := []struct {
		name string
		got  CachePolicy
		want string
	}{
		{"CacheNone", CacheNone, "none"},
		{"CacheDefault", CacheDefault, "default"},
		{"CacheExtended", CacheExtended, "extended"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.got) != tt.want {
				t.Errorf("%s = %q; want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TS-07-5: AICallOptions struct fields
// ---------------------------------------------------------------------------

// TestSpec07_AICall_OptionsFields verifies that all expected fields on the
// AICallOptions struct are accessible and correctly typed.
// Test Spec: TS-07-5, Requirement: 07-REQ-2.1
func TestSpec07_AICall_OptionsFields(t *testing.T) {
	temp := 0.7
	opts := AICallOptions{
		ModelTier:   "STANDARD",
		MaxTokens:   4096,
		Messages:    []Message{{Role: "user", Content: "hello"}},
		System:      "You are helpful.",
		Context:     "test-context",
		CachePolicy: CacheDefault,
		Temperature: &temp,
		Tools:       []Tool{{Name: "test_tool"}},
		ToolChoice:  "any",
	}

	if opts.ModelTier != "STANDARD" {
		t.Errorf("ModelTier = %q; want %q", opts.ModelTier, "STANDARD")
	}
	if opts.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d; want %d", opts.MaxTokens, 4096)
	}
	if len(opts.Messages) != 1 {
		t.Errorf("len(Messages) = %d; want %d", len(opts.Messages), 1)
	}
	if opts.System != "You are helpful." {
		t.Errorf("System = %q; want %q", opts.System, "You are helpful.")
	}
	if opts.Context != "test-context" {
		t.Errorf("Context = %q; want %q", opts.Context, "test-context")
	}
	if opts.CachePolicy != CacheDefault {
		t.Errorf("CachePolicy = %q; want %q", opts.CachePolicy, CacheDefault)
	}
	if opts.Temperature == nil || *opts.Temperature != 0.7 {
		t.Errorf("Temperature = %v; want 0.7", opts.Temperature)
	}
	if len(opts.Tools) != 1 {
		t.Errorf("len(Tools) = %d; want %d", len(opts.Tools), 1)
	}
}

// ---------------------------------------------------------------------------
// TS-07-6: AICall defaults
// ---------------------------------------------------------------------------

// TestSpec07_AICall_DefaultsApplied verifies that ApplyDefaults fills in
// MaxTokens=65536 and CachePolicy=CacheDefault when not set.
// Test Spec: TS-07-6, Requirement: 07-REQ-2.2
func TestSpec07_AICall_DefaultsApplied(t *testing.T) {
	opts := AICallOptions{}
	ApplyDefaults(&opts)

	if opts.MaxTokens != 65536 {
		t.Errorf("after ApplyDefaults, MaxTokens = %d; want %d", opts.MaxTokens, 65536)
	}
	if opts.CachePolicy != CacheDefault {
		t.Errorf("after ApplyDefaults, CachePolicy = %q; want %q", opts.CachePolicy, CacheDefault)
	}
}

// TestSpec07_AICall_DefaultsPreserveExplicit verifies that ApplyDefaults
// does NOT override explicitly set values.
// Requirement: 07-REQ-2.2
func TestSpec07_AICall_DefaultsPreserveExplicit(t *testing.T) {
	opts := AICallOptions{
		MaxTokens:   1024,
		CachePolicy: CacheNone,
	}
	ApplyDefaults(&opts)

	if opts.MaxTokens != 1024 {
		t.Errorf("after ApplyDefaults, MaxTokens = %d; want %d (explicitly set)", opts.MaxTokens, 1024)
	}
	if opts.CachePolicy != CacheNone {
		t.Errorf("after ApplyDefaults, CachePolicy = %q; want %q (explicitly set)", opts.CachePolicy, CacheNone)
	}
}

// ---------------------------------------------------------------------------
// TS-07-5: AICall success path
// ---------------------------------------------------------------------------

// TestSpec07_AICall_Success verifies that AICall returns the response text
// from a successful LLM call via mock Doer.
// Test Spec: TS-07-5, Requirement: 07-REQ-2.1
func TestSpec07_AICall_Success(t *testing.T) {
	mock := newSuccessDoer("Hello from the LLM!")

	ctx := context.Background()
	opts := AICallOptions{
		ModelTier: "SIMPLE",
		Messages:  []Message{{Role: "user", Content: "Say hello"}},
		System:    "You are a test assistant.",
		Doer:      mock,
	}

	text, raw, err := AICall(ctx, opts)
	if err != nil {
		t.Fatalf("AICall() returned error: %v", err)
	}
	if text == "" {
		t.Error("AICall() returned empty text; want non-empty response")
	}
	if raw == nil {
		t.Error("AICall() returned nil rawResponse; want non-nil")
	}
	if len(mock.calls) != 1 {
		t.Errorf("mock call count = %d; want 1", len(mock.calls))
	}
}

// ---------------------------------------------------------------------------
// AICall platform selection
// ---------------------------------------------------------------------------

// TestSpec07_AICall_PlatformVertex verifies that when CLAUDE_CODE_USE_VERTEX
// is set, AICall selects the Vertex AI platform for client creation.
// Requirement: 07-REQ-2.1
func TestSpec07_AICall_PlatformVertex(t *testing.T) {
	t.Setenv("CLAUDE_CODE_USE_VERTEX", "1")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	var detectedPlatform string
	mock := newSuccessDoer("vertex response")

	ctx := context.Background()
	opts := AICallOptions{
		ModelTier: "STANDARD",
		Messages:  []Message{{Role: "user", Content: "test"}},
		ClientFactory: func(platform string) (Doer, error) {
			detectedPlatform = platform
			return mock, nil
		},
	}

	text, _, err := AICall(ctx, opts)
	if err != nil {
		t.Fatalf("AICall() returned error: %v", err)
	}
	if text == "" {
		t.Error("AICall() returned empty text")
	}
	if detectedPlatform != "vertex" {
		t.Errorf("detected platform = %q; want %q", detectedPlatform, "vertex")
	}
}

// TestSpec07_AICall_PlatformBedrock verifies that when CLAUDE_CODE_USE_BEDROCK
// is set (and VERTEX is not), AICall selects the Bedrock platform.
// Requirement: 07-REQ-2.1
func TestSpec07_AICall_PlatformBedrock(t *testing.T) {
	t.Setenv("CLAUDE_CODE_USE_VERTEX", "")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	t.Setenv("ANTHROPIC_API_KEY", "")

	var detectedPlatform string
	mock := newSuccessDoer("bedrock response")

	ctx := context.Background()
	opts := AICallOptions{
		ModelTier: "STANDARD",
		Messages:  []Message{{Role: "user", Content: "test"}},
		ClientFactory: func(platform string) (Doer, error) {
			detectedPlatform = platform
			return mock, nil
		},
	}

	text, _, err := AICall(ctx, opts)
	if err != nil {
		t.Fatalf("AICall() returned error: %v", err)
	}
	if text == "" {
		t.Error("AICall() returned empty text")
	}
	if detectedPlatform != "bedrock" {
		t.Errorf("detected platform = %q; want %q", detectedPlatform, "bedrock")
	}
}

// TestSpec07_AICall_PlatformDirect verifies that when neither VERTEX nor
// BEDROCK env vars are set, AICall uses the direct API with ANTHROPIC_API_KEY.
// Requirement: 07-REQ-2.1
func TestSpec07_AICall_PlatformDirect(t *testing.T) {
	t.Setenv("CLAUDE_CODE_USE_VERTEX", "")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "")
	t.Setenv("ANTHROPIC_API_KEY", "test-api-key")

	var detectedPlatform string
	mock := newSuccessDoer("direct response")

	ctx := context.Background()
	opts := AICallOptions{
		ModelTier: "STANDARD",
		Messages:  []Message{{Role: "user", Content: "test"}},
		ClientFactory: func(platform string) (Doer, error) {
			detectedPlatform = platform
			return mock, nil
		},
	}

	text, _, err := AICall(ctx, opts)
	if err != nil {
		t.Fatalf("AICall() returned error: %v", err)
	}
	if text == "" {
		t.Error("AICall() returned empty text")
	}
	if detectedPlatform != "direct" {
		t.Errorf("detected platform = %q; want %q", detectedPlatform, "direct")
	}
}

// TestSpec07_AICall_MissingCredentials verifies that AICall returns an
// AgentError with category "auth" when no API credentials are available.
// Edge Case: 07-REQ-2.E2
func TestSpec07_AICall_MissingCredentials(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("CLAUDE_CODE_USE_VERTEX", "")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "")

	ctx := context.Background()
	opts := AICallOptions{
		ModelTier: "STANDARD",
		Messages:  []Message{{Role: "user", Content: "hello"}},
	}

	text, raw, err := AICall(ctx, opts)
	if err == nil {
		t.Fatal("AICall() returned nil error; want AgentError with category auth")
	}
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("error type = %T; want *AgentError", err)
	}
	if agentErr.ErrorCategory != "auth" {
		t.Errorf("ErrorCategory = %q; want %q", agentErr.ErrorCategory, "auth")
	}
	if text != "" {
		t.Errorf("text = %q; want empty string", text)
	}
	if raw != nil {
		t.Errorf("raw = %v; want nil", raw)
	}
}

// ---------------------------------------------------------------------------
// TS-07-7: AICall retry with exponential backoff
// ---------------------------------------------------------------------------

// TestSpec07_AICall_RetryExponentialBackoff verifies that AICall retries
// transient errors with exponential backoff delays of 2s, 30s, 60s and
// succeeds on the fourth attempt.
// Test Spec: TS-07-7, Requirement: 07-REQ-2.3
func TestSpec07_AICall_RetryExponentialBackoff(t *testing.T) {
	rateLimitErr := &APIError{StatusCode: 429, Msg: "rate limited"}
	mock := newMockDoer(
		mockResult{err: rateLimitErr},
		mockResult{err: rateLimitErr},
		mockResult{err: rateLimitErr},
		mockResult{resp: &MessageResponse{
			Content: []ContentBlock{{Type: "text", Text: "success after retries"}},
		}},
	)

	delays := &delayCapturer{}
	ctx := context.Background()
	opts := AICallOptions{
		ModelTier: "SIMPLE",
		Messages:  []Message{{Role: "user", Content: "test"}},
		Doer:      mock,
		SleepFunc: delays.sleep,
	}

	text, _, err := AICall(ctx, opts)
	if err != nil {
		t.Fatalf("AICall() returned error: %v", err)
	}
	if text != "success after retries" {
		t.Errorf("text = %q; want %q", text, "success after retries")
	}
	if len(mock.calls) != 4 {
		t.Errorf("mock call count = %d; want 4", len(mock.calls))
	}

	wantDelays := []time.Duration{2 * time.Second, 30 * time.Second, 60 * time.Second}
	if len(delays.delays) != len(wantDelays) {
		t.Fatalf("len(delays) = %d; want %d", len(delays.delays), len(wantDelays))
	}
	for i, want := range wantDelays {
		if delays.delays[i] != want {
			t.Errorf("delay[%d] = %v; want %v", i, delays.delays[i], want)
		}
	}
}

// TestSpec07_AICall_RetryOnServerError verifies that 5xx server errors
// trigger retries, succeeding when the error resolves.
// Requirement: 07-REQ-2.3
func TestSpec07_AICall_RetryOnServerError(t *testing.T) {
	serverErr := &APIError{StatusCode: 500, Msg: "internal server error"}
	mock := newMockDoer(
		mockResult{err: serverErr},
		mockResult{err: serverErr},
		mockResult{resp: &MessageResponse{
			Content: []ContentBlock{{Type: "text", Text: "recovered"}},
		}},
	)

	delays := &delayCapturer{}
	ctx := context.Background()
	opts := AICallOptions{
		ModelTier: "SIMPLE",
		Messages:  []Message{{Role: "user", Content: "test"}},
		Doer:      mock,
		SleepFunc: delays.sleep,
	}

	text, _, err := AICall(ctx, opts)
	if err != nil {
		t.Fatalf("AICall() returned error: %v", err)
	}
	if text != "recovered" {
		t.Errorf("text = %q; want %q", text, "recovered")
	}
	if len(mock.calls) != 3 {
		t.Errorf("mock call count = %d; want 3", len(mock.calls))
	}
}

// TestSpec07_AICall_RetryOnConnectionError verifies that connection errors
// (non-APIError, non-context errors) trigger retries, succeeding when the
// error resolves.
// Requirement: 07-REQ-2.3
func TestSpec07_AICall_RetryOnConnectionError(t *testing.T) {
	connErr := errors.New("dial tcp: connection refused")
	mock := newMockDoer(
		mockResult{err: connErr},
		mockResult{err: connErr},
		mockResult{resp: &MessageResponse{
			Content: []ContentBlock{{Type: "text", Text: "connected"}},
		}},
	)

	delays := &delayCapturer{}
	ctx := context.Background()
	opts := AICallOptions{
		ModelTier: "SIMPLE",
		Messages:  []Message{{Role: "user", Content: "test"}},
		Doer:      mock,
		SleepFunc: delays.sleep,
	}

	text, _, err := AICall(ctx, opts)
	if err != nil {
		t.Fatalf("AICall() returned error: %v", err)
	}
	if text != "connected" {
		t.Errorf("text = %q; want %q", text, "connected")
	}
	if len(mock.calls) != 3 {
		t.Errorf("mock call count = %d; want 3", len(mock.calls))
	}
	if len(delays.delays) != 2 {
		t.Errorf("len(delays) = %d; want 2", len(delays.delays))
	}
}

// TestSpec07_AICall_ConnectionErrorExhausted verifies that persistent
// connection errors are eventually returned as AgentError after max retries.
// Requirement: 07-REQ-2.3, 07-REQ-2.6
func TestSpec07_AICall_ConnectionErrorExhausted(t *testing.T) {
	connErr := errors.New("dial tcp: connection refused")
	mock := newMockDoer(
		mockResult{err: connErr},
		mockResult{err: connErr},
		mockResult{err: connErr},
		mockResult{err: connErr},
	)

	delays := &delayCapturer{}
	ctx := context.Background()
	opts := AICallOptions{
		ModelTier: "SIMPLE",
		Messages:  []Message{{Role: "user", Content: "test"}},
		Doer:      mock,
		SleepFunc: delays.sleep,
	}

	text, raw, err := AICall(ctx, opts)
	if err == nil {
		t.Fatal("AICall() returned nil error; want AgentError after max retries on connection error")
	}
	if text != "" {
		t.Errorf("text = %q; want empty string", text)
	}
	if raw != nil {
		t.Errorf("raw = %v; want nil", raw)
	}

	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("error type = %T; want *AgentError", err)
	}
	if agentErr.Retryable {
		t.Error("AgentError.Retryable = true; want false")
	}
	if len(mock.calls) != 4 {
		t.Errorf("mock call count = %d; want 4", len(mock.calls))
	}
}

// TestSpec07_AICall_NonRetryableError verifies that non-retryable errors
// (e.g. 400 Bad Request) are returned immediately without retrying.
// Requirement: 07-REQ-2.3
func TestSpec07_AICall_NonRetryableError(t *testing.T) {
	badRequestErr := &APIError{StatusCode: 400, Msg: "bad request"}
	mock := newMockDoer(
		mockResult{err: badRequestErr},
	)

	delays := &delayCapturer{}
	ctx := context.Background()
	opts := AICallOptions{
		ModelTier: "SIMPLE",
		Messages:  []Message{{Role: "user", Content: "test"}},
		Doer:      mock,
		SleepFunc: delays.sleep,
	}

	_, _, err := AICall(ctx, opts)
	if err == nil {
		t.Fatal("AICall() returned nil error; want error for non-retryable 400")
	}
	if len(mock.calls) != 1 {
		t.Errorf("mock call count = %d; want 1 (no retries for non-retryable errors)", len(mock.calls))
	}
	if len(delays.delays) != 0 {
		t.Errorf("len(delays) = %d; want 0 (no sleep for non-retryable errors)", len(delays.delays))
	}
}

// ---------------------------------------------------------------------------
// TS-07-10: AICall exhausted retries
// ---------------------------------------------------------------------------

// TestSpec07_AICall_ExhaustedRetries verifies that AICall returns an
// AgentError with Retryable=false after all 4 retry attempts are exhausted.
// Test Spec: TS-07-10, Requirement: 07-REQ-2.6
func TestSpec07_AICall_ExhaustedRetries(t *testing.T) {
	serverErr := &APIError{StatusCode: 500, Msg: "server error"}
	mock := newMockDoer(
		mockResult{err: serverErr},
		mockResult{err: serverErr},
		mockResult{err: serverErr},
		mockResult{err: serverErr},
	)

	delays := &delayCapturer{}
	ctx := context.Background()
	opts := AICallOptions{
		ModelTier: "SIMPLE",
		Messages:  []Message{{Role: "user", Content: "test"}},
		Doer:      mock,
		SleepFunc: delays.sleep,
	}

	text, raw, err := AICall(ctx, opts)
	if err == nil {
		t.Fatal("AICall() returned nil error; want AgentError after max retries")
	}
	if text != "" {
		t.Errorf("text = %q; want empty string", text)
	}
	if raw != nil {
		t.Errorf("raw = %v; want nil", raw)
	}

	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("error type = %T; want *AgentError", err)
	}
	if agentErr.Retryable {
		t.Error("AgentError.Retryable = true; want false")
	}
	if agentErr.ErrorCategory == "" {
		t.Error("AgentError.ErrorCategory is empty; want non-empty category")
	}
	if len(mock.calls) != 4 {
		t.Errorf("mock call count = %d; want 4", len(mock.calls))
	}
}

// ---------------------------------------------------------------------------
// TS-07-8: AICall cache_control injection
// ---------------------------------------------------------------------------

// TestSpec07_AICall_CacheControl_SonnetAboveThreshold verifies that AICall
// injects cache_control with type "ephemeral" on the last system block when
// CacheDefault is set and the system prompt exceeds the sonnet token threshold
// (2048 tokens, estimated as len(text)/4 > 2048, so > 8192 chars).
// Test Spec: TS-07-8, Requirement: 07-REQ-2.4
func TestSpec07_AICall_CacheControl_SonnetAboveThreshold(t *testing.T) {
	// Sonnet threshold is 2048 tokens. At ~4 chars/token, need > 8192 chars.
	longSystem := strings.Repeat("x", 9000)
	mock := newSuccessDoer("cached response")

	ctx := context.Background()
	opts := AICallOptions{
		ModelTier:   "STANDARD", // resolves to claude-sonnet-4-6
		Messages:    []Message{{Role: "user", Content: "test"}},
		System:      longSystem,
		CachePolicy: CacheDefault,
		Doer:        mock,
	}

	_, _, err := AICall(ctx, opts)
	if err != nil {
		t.Fatalf("AICall() returned error: %v", err)
	}
	if len(mock.calls) == 0 {
		t.Fatal("mock received no calls")
	}

	req := mock.calls[0]
	if len(req.System) == 0 {
		t.Fatal("request has no system blocks; want at least one with cache_control")
	}
	lastBlock := req.System[len(req.System)-1]
	if lastBlock.CacheControl == nil {
		t.Fatal("last system block has no cache_control; want {\"type\": \"ephemeral\"}")
	}
	if lastBlock.CacheControl["type"] != "ephemeral" {
		t.Errorf("cache_control[\"type\"] = %q; want %q", lastBlock.CacheControl["type"], "ephemeral")
	}
}

// TestSpec07_AICall_CacheControl_OpusAboveThreshold verifies that opus/haiku
// models use a 4096 token threshold (> 16384 chars) for cache injection.
// Requirement: 07-REQ-2.4
func TestSpec07_AICall_CacheControl_OpusAboveThreshold(t *testing.T) {
	// Opus threshold is 4096 tokens. At ~4 chars/token, need > 16384 chars.
	longSystem := strings.Repeat("x", 17000)
	mock := newSuccessDoer("cached opus response")

	ctx := context.Background()
	opts := AICallOptions{
		ModelTier:   "ADVANCED", // resolves to claude-opus-4-6
		Messages:    []Message{{Role: "user", Content: "test"}},
		System:      longSystem,
		CachePolicy: CacheDefault,
		Doer:        mock,
	}

	_, _, err := AICall(ctx, opts)
	if err != nil {
		t.Fatalf("AICall() returned error: %v", err)
	}
	if len(mock.calls) == 0 {
		t.Fatal("mock received no calls")
	}

	req := mock.calls[0]
	if len(req.System) == 0 {
		t.Fatal("request has no system blocks")
	}
	lastBlock := req.System[len(req.System)-1]
	if lastBlock.CacheControl == nil {
		t.Fatal("last system block has no cache_control; want {\"type\": \"ephemeral\"}")
	}
	if lastBlock.CacheControl["type"] != "ephemeral" {
		t.Errorf("cache_control[\"type\"] = %q; want %q", lastBlock.CacheControl["type"], "ephemeral")
	}
}

// TestSpec07_AICall_CacheControl_BelowThreshold verifies that AICall does
// NOT inject cache_control when the system prompt is below the token threshold.
// Requirement: 07-REQ-2.4
func TestSpec07_AICall_CacheControl_BelowThreshold(t *testing.T) {
	// Short system prompt well below any threshold.
	shortSystem := "You are a helpful assistant."
	mock := newSuccessDoer("uncached response")

	ctx := context.Background()
	opts := AICallOptions{
		ModelTier:   "STANDARD",
		Messages:    []Message{{Role: "user", Content: "test"}},
		System:      shortSystem,
		CachePolicy: CacheDefault,
		Doer:        mock,
	}

	_, _, err := AICall(ctx, opts)
	if err != nil {
		t.Fatalf("AICall() returned error: %v", err)
	}
	if len(mock.calls) == 0 {
		t.Fatal("mock received no calls")
	}

	req := mock.calls[0]
	for i, block := range req.System {
		if block.CacheControl != nil {
			t.Errorf("system block[%d] has cache_control = %v; want nil (below threshold)", i, block.CacheControl)
		}
	}
}

// TestSpec07_AICall_CacheExtended_TTL verifies that CacheExtended injects
// cache_control with both type "ephemeral" and ttl "1h".
// Requirement: 07-REQ-2.4
func TestSpec07_AICall_CacheExtended_TTL(t *testing.T) {
	longSystem := strings.Repeat("x", 9000)
	mock := newSuccessDoer("extended cached response")

	ctx := context.Background()
	opts := AICallOptions{
		ModelTier:   "STANDARD",
		Messages:    []Message{{Role: "user", Content: "test"}},
		System:      longSystem,
		CachePolicy: CacheExtended,
		Doer:        mock,
	}

	_, _, err := AICall(ctx, opts)
	if err != nil {
		t.Fatalf("AICall() returned error: %v", err)
	}
	if len(mock.calls) == 0 {
		t.Fatal("mock received no calls")
	}

	req := mock.calls[0]
	if len(req.System) == 0 {
		t.Fatal("request has no system blocks")
	}
	lastBlock := req.System[len(req.System)-1]
	if lastBlock.CacheControl == nil {
		t.Fatal("last system block has no cache_control")
	}
	if lastBlock.CacheControl["type"] != "ephemeral" {
		t.Errorf("cache_control[\"type\"] = %q; want %q", lastBlock.CacheControl["type"], "ephemeral")
	}
	if lastBlock.CacheControl["ttl"] != "1h" {
		t.Errorf("cache_control[\"ttl\"] = %q; want %q", lastBlock.CacheControl["ttl"], "1h")
	}
}

// TestSpec07_AICall_CacheNone_NoInjection verifies that CacheNone never
// injects cache_control regardless of system prompt length.
// Requirement: 07-REQ-2.4
func TestSpec07_AICall_CacheNone_NoInjection(t *testing.T) {
	longSystem := strings.Repeat("x", 20000)
	mock := newSuccessDoer("no cache response")

	ctx := context.Background()
	opts := AICallOptions{
		ModelTier:   "STANDARD",
		Messages:    []Message{{Role: "user", Content: "test"}},
		System:      longSystem,
		CachePolicy: CacheNone,
		Doer:        mock,
	}

	_, _, err := AICall(ctx, opts)
	if err != nil {
		t.Fatalf("AICall() returned error: %v", err)
	}
	if len(mock.calls) == 0 {
		t.Fatal("mock received no calls")
	}

	req := mock.calls[0]
	for i, block := range req.System {
		if block.CacheControl != nil {
			t.Errorf("system block[%d] has cache_control = %v; want nil (CacheNone)", i, block.CacheControl)
		}
	}
}

// ---------------------------------------------------------------------------
// TS-07-9: AICall cache_control rejection retry
// ---------------------------------------------------------------------------

// TestSpec07_AICall_CacheControl_RejectionRetry verifies that AICall retries
// without cache_control when the API rejects the request due to cache_control.
// Test Spec: TS-07-9, Requirement: 07-REQ-2.5
func TestSpec07_AICall_CacheControl_RejectionRetry(t *testing.T) {
	cacheRejectionErr := &APIError{StatusCode: 400, Msg: "cache_control not supported"}
	mock := newMockDoer(
		mockResult{err: cacheRejectionErr},
		mockResult{resp: &MessageResponse{
			Content: []ContentBlock{{Type: "text", Text: "success without cache"}},
		}},
	)

	longSystem := strings.Repeat("x", 9000)
	ctx := context.Background()
	opts := AICallOptions{
		ModelTier:   "STANDARD",
		Messages:    []Message{{Role: "user", Content: "test"}},
		System:      longSystem,
		CachePolicy: CacheDefault,
		Doer:        mock,
	}

	text, _, err := AICall(ctx, opts)
	if err != nil {
		t.Fatalf("AICall() returned error: %v", err)
	}
	if text != "success without cache" {
		t.Errorf("text = %q; want %q", text, "success without cache")
	}
	if len(mock.calls) != 2 {
		t.Fatalf("mock call count = %d; want 2", len(mock.calls))
	}

	// Second call should NOT have cache_control.
	secondReq := mock.calls[1]
	for i, block := range secondReq.System {
		if block.CacheControl != nil {
			t.Errorf("retry request system block[%d] has cache_control = %v; want nil", i, block.CacheControl)
		}
	}
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

// TestSpec07_AICall_ContextCancellation verifies that AICall stops immediately
// and returns an error when the context is cancelled, without further retries.
// Edge Case: 07-REQ-2.E1
func TestSpec07_AICall_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	mock := newSuccessDoer("should not reach here")
	opts := AICallOptions{
		ModelTier: "SIMPLE",
		Messages:  []Message{{Role: "user", Content: "test"}},
		Doer:      mock,
	}

	text, _, err := AICall(ctx, opts)
	if err == nil {
		t.Fatal("AICall() returned nil error; want context cancellation error")
	}
	if text != "" {
		t.Errorf("text = %q; want empty string", text)
	}
	// Verify no retries were attempted after cancellation.
	if len(mock.calls) > 1 {
		t.Errorf("mock call count = %d; want 0 or 1 (no retries on context cancellation)", len(mock.calls))
	}
}

// TestSpec07_AICall_NoPanic verifies that AICall never panics, even with
// unusual inputs. All errors are returned via the error return value.
// Edge Case: 07-REQ-2.E4
func TestSpec07_AICall_NoPanic(t *testing.T) {
	ctx := context.Background()

	edgeCases := []struct {
		name string
		opts AICallOptions
	}{
		{"zero_value", AICallOptions{}},
		{"nonexistent_tier", AICallOptions{ModelTier: "NONEXISTENT"}},
		{"nil_messages", AICallOptions{ModelTier: "SIMPLE", Messages: nil}},
		{"empty_messages", AICallOptions{ModelTier: "SIMPLE", Messages: []Message{}}},
	}

	for _, tc := range edgeCases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("AICall panicked with opts %+v: %v", tc.opts, r)
				}
			}()
			// We don't care about the result, just that it doesn't panic.
			_, _, _ = AICall(ctx, tc.opts)
		})
	}
}

// TestSpec07_AICall_MissingTextContent verifies that AICall returns an error
// when the API response is successful but missing expected text content.
// Edge Case: 07-REQ-2.E3
func TestSpec07_AICall_MissingTextContent(t *testing.T) {
	// Response with no content blocks.
	mock := newMockDoer(
		mockResult{resp: &MessageResponse{Content: nil}},
	)

	ctx := context.Background()
	opts := AICallOptions{
		ModelTier: "SIMPLE",
		Messages:  []Message{{Role: "user", Content: "test"}},
		Doer:      mock,
	}

	text, raw, err := AICall(ctx, opts)
	if err == nil {
		t.Fatal("AICall() returned nil error; want error for missing text content")
	}
	if text != "" {
		t.Errorf("text = %q; want empty string", text)
	}
	// rawResponse should still be returned even when text extraction fails.
	if raw == nil {
		t.Error("rawResponse = nil; want non-nil (raw response should be preserved)")
	}
}

// TestSpec07_AICall_MissingTextContentEmptyBlocks verifies the edge case
// where the response has content blocks but none with type "text".
// Edge Case: 07-REQ-2.E3
func TestSpec07_AICall_MissingTextContentEmptyBlocks(t *testing.T) {
	// Response with content blocks but no text block.
	mock := newMockDoer(
		mockResult{resp: &MessageResponse{
			Content: []ContentBlock{{Type: "tool_use", Name: "test", Input: "{}"}},
		}},
	)

	ctx := context.Background()
	opts := AICallOptions{
		ModelTier: "SIMPLE",
		Messages:  []Message{{Role: "user", Content: "test"}},
		Doer:      mock,
	}

	text, raw, err := AICall(ctx, opts)
	if err == nil {
		t.Fatal("AICall() returned nil error; want error for missing text content")
	}
	if text != "" {
		t.Errorf("text = %q; want empty string", text)
	}
	if raw == nil {
		t.Error("rawResponse = nil; want non-nil")
	}
}

// ---------------------------------------------------------------------------
// IsRetryable unit tests
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// TS-NS-2: APIError.RetryAfter field
// ---------------------------------------------------------------------------

// durationPtr is a helper that returns a pointer to the given duration.
func durationPtr(d time.Duration) *time.Duration { return &d }

// TestNS_APIError_RetryAfterField verifies that APIError carries a RetryAfter
// field and that Error() still returns Msg unchanged.
// Test Spec: TS-NS-2, Requirement: NS-REQ-2
func TestNS_APIError_RetryAfterField(t *testing.T) {
	ra := durationPtr(5 * time.Second)
	apiErr := &APIError{StatusCode: 429, Msg: "rate limited", RetryAfter: ra}

	if apiErr.RetryAfter == nil {
		t.Fatal("RetryAfter = nil; want non-nil")
	}
	if *apiErr.RetryAfter != 5*time.Second {
		t.Errorf("*RetryAfter = %v; want %v", *apiErr.RetryAfter, 5*time.Second)
	}
	if apiErr.Error() != "rate limited" {
		t.Errorf("Error() = %q; want %q", apiErr.Error(), "rate limited")
	}
}

// TestNS_APIError_RetryAfterNil verifies that a nil RetryAfter is valid and
// that APIError without Retry-After behaves as before.
// Test Spec: TS-NS-4, Requirement: NS-REQ-4
func TestNS_APIError_RetryAfterNil(t *testing.T) {
	apiErr := &APIError{StatusCode: 429, Msg: "rate limited"}
	if apiErr.RetryAfter != nil {
		t.Errorf("RetryAfter = %v; want nil", apiErr.RetryAfter)
	}
}

// ---------------------------------------------------------------------------
// TS-NS-3: AICall uses RetryAfter when present on 429
// ---------------------------------------------------------------------------

// TestNS_AICall_RetryAfter_UsesHeaderDelay verifies that when a 429 APIError
// has a non-nil RetryAfter, AICall uses that duration for the sleep instead
// of RetryDelays[attempt-1].
// Test Spec: TS-NS-3, Requirement: NS-REQ-3
func TestNS_AICall_RetryAfter_UsesHeaderDelay(t *testing.T) {
	ra := durationPtr(7 * time.Second)
	rateLimitErr := &APIError{StatusCode: 429, Msg: "rate limited", RetryAfter: ra}
	mock := newMockDoer(
		mockResult{err: rateLimitErr},
		mockResult{resp: &MessageResponse{
			Content: []ContentBlock{{Type: "text", Text: "success"}},
		}},
	)

	delays := &delayCapturer{}
	ctx := context.Background()
	opts := AICallOptions{
		ModelTier: "SIMPLE",
		Messages:  []Message{{Role: "user", Content: "test"}},
		Doer:      mock,
		SleepFunc: delays.sleep,
	}

	text, _, err := AICall(ctx, opts)
	if err != nil {
		t.Fatalf("AICall() returned error: %v", err)
	}
	if text != "success" {
		t.Errorf("text = %q; want %q", text, "success")
	}
	if len(mock.calls) != 2 {
		t.Errorf("mock call count = %d; want 2", len(mock.calls))
	}
	if len(delays.delays) != 1 {
		t.Fatalf("len(delays) = %d; want 1", len(delays.delays))
	}
	// Must use RetryAfter (7s), NOT RetryDelays[0] (2s).
	if delays.delays[0] != 7*time.Second {
		t.Errorf("delay[0] = %v; want %v (RetryAfter)", delays.delays[0], 7*time.Second)
	}
}

// ---------------------------------------------------------------------------
// TS-NS-4: 429 without RetryAfter uses fixed RetryDelays (no regression)
// ---------------------------------------------------------------------------

// TestNS_AICall_RetryAfter_NilUsesFixedDelay verifies that a 429 APIError
// with nil RetryAfter causes AICall to use RetryDelays[0] as before.
// Test Spec: TS-NS-4, Requirement: NS-REQ-4
func TestNS_AICall_RetryAfter_NilUsesFixedDelay(t *testing.T) {
	rateLimitErr := &APIError{StatusCode: 429, Msg: "rate limited"} // RetryAfter is nil
	mock := newMockDoer(
		mockResult{err: rateLimitErr},
		mockResult{resp: &MessageResponse{
			Content: []ContentBlock{{Type: "text", Text: "success"}},
		}},
	)

	delays := &delayCapturer{}
	ctx := context.Background()
	opts := AICallOptions{
		ModelTier: "SIMPLE",
		Messages:  []Message{{Role: "user", Content: "test"}},
		Doer:      mock,
		SleepFunc: delays.sleep,
	}

	text, _, err := AICall(ctx, opts)
	if err != nil {
		t.Fatalf("AICall() returned error: %v", err)
	}
	if text != "success" {
		t.Errorf("text = %q; want %q", text, "success")
	}
	if len(delays.delays) != 1 {
		t.Fatalf("len(delays) = %d; want 1", len(delays.delays))
	}
	// Must use fixed RetryDelays[0] = 2s, not any header value.
	if delays.delays[0] != RetryDelays[0] {
		t.Errorf("delay[0] = %v; want %v (RetryDelays[0])", delays.delays[0], RetryDelays[0])
	}
}

// ---------------------------------------------------------------------------
// TS-NS-5: Non-retryable 401 is not retried
// ---------------------------------------------------------------------------

// TestNS_AICall_NonRetryable_401 verifies that a 401 APIError causes AICall
// to fail immediately with no sleep calls.
// Test Spec: TS-NS-5, Requirement: NS-REQ-5
func TestNS_AICall_NonRetryable_401(t *testing.T) {
	authErr := &APIError{StatusCode: 401, Msg: "unauthorized"}
	mock := newMockDoer(
		mockResult{err: authErr},
	)

	delays := &delayCapturer{}
	ctx := context.Background()
	opts := AICallOptions{
		ModelTier: "SIMPLE",
		Messages:  []Message{{Role: "user", Content: "test"}},
		Doer:      mock,
		SleepFunc: delays.sleep,
	}

	_, _, err := AICall(ctx, opts)
	if err == nil {
		t.Fatal("AICall() returned nil error; want error for non-retryable 401")
	}
	if len(mock.calls) != 1 {
		t.Errorf("mock call count = %d; want 1 (no retries)", len(mock.calls))
	}
	if len(delays.delays) != 0 {
		t.Errorf("len(delays) = %d; want 0 (no sleep for non-retryable)", len(delays.delays))
	}
}

// ---------------------------------------------------------------------------
// TS-07-11: IsRetryable
// ---------------------------------------------------------------------------

// TestSpec07_IsRetryable verifies IsRetryable classification of various
// error types per 07-REQ-2.3.
func TestSpec07_IsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"rate_limit_429", &APIError{StatusCode: 429, Msg: "rate limited"}, true},
		{"server_500", &APIError{StatusCode: 500, Msg: "internal server error"}, true},
		{"server_502", &APIError{StatusCode: 502, Msg: "bad gateway"}, true},
		{"server_503", &APIError{StatusCode: 503, Msg: "service unavailable"}, true},
		{"bad_request_400", &APIError{StatusCode: 400, Msg: "bad request"}, false},
		{"unauthorized_401", &APIError{StatusCode: 401, Msg: "unauthorized"}, false},
		{"forbidden_403", &APIError{StatusCode: 403, Msg: "forbidden"}, false},
		{"not_found_404", &APIError{StatusCode: 404, Msg: "not found"}, false},
		{"connection_error", errors.New("dial tcp: connection refused"), true},
		{"dns_error", errors.New("no such host"), true},
		{"context_canceled", context.Canceled, false},
		{"context_deadline", context.DeadlineExceeded, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsRetryable(tt.err)
			if got != tt.want {
				t.Errorf("IsRetryable(%v) = %v; want %v", tt.err, got, tt.want)
			}
		})
	}
}
