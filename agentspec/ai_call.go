package agentspec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// CachePolicy controls prompt caching behavior for AICall.
type CachePolicy string

const (
	// CacheNone disables cache_control injection.
	CacheNone CachePolicy = "none"
	// CacheDefault injects ephemeral cache_control when the system prompt
	// exceeds the model-specific token threshold.
	CacheDefault CachePolicy = "default"
	// CacheExtended injects ephemeral cache_control with a 1-hour TTL
	// when the system prompt exceeds the model-specific token threshold.
	CacheExtended CachePolicy = "extended"
)

// Token thresholds for cache_control injection.
const (
	// sonnetCacheThreshold is the estimated token count above which
	// cache_control is injected for sonnet models.
	sonnetCacheThreshold = 2048
	// defaultCacheThreshold is the estimated token count above which
	// cache_control is injected for opus/haiku models.
	defaultCacheThreshold = 4096
)

// Message represents a conversation message.
// Content may be a plain string for simple messages, or []ContentBlock for
// structured multi-part messages (e.g. tool_result blocks in repair turns).
type Message struct {
	Role    string
	Content any // string or []ContentBlock
}

// SystemBlock represents a block within the system prompt,
// potentially annotated with cache_control metadata.
type SystemBlock struct {
	Type         string
	Text         string
	CacheControl map[string]string
}

// Tool represents an Anthropic tool definition.
type Tool struct {
	Name        string
	Description string
	InputSchema any
}

// ContentBlock represents a single content block in an LLM response or request.
// For tool_use blocks: Type="tool_use", ID=<tool use id>, Name=<tool name>, Input=<payload>.
// For tool_result blocks: Type="tool_result", ToolUseID=<tool use id>, Text=<result text>.
type ContentBlock struct {
	Type      string
	Text      string
	ID        string
	Name      string
	Input     any
	ToolUseID string // for tool_result blocks: ID of the corresponding tool_use block
}

// MessageRequest holds the parameters for a single LLM API call.
type MessageRequest struct {
	Model       string
	MaxTokens   int
	Messages    []Message
	System      []SystemBlock
	Tools       []Tool
	ToolChoice  any
	Temperature *float64
}

// MessageResponse holds the result of a single LLM API call.
type MessageResponse struct {
	Content    []ContentBlock
	StopReason string
}

// Doer is the interface for sending LLM requests. AICall uses this
// internally; tests can provide a mock implementation via AICallOptions.Doer.
type Doer interface {
	CreateMessage(ctx context.Context, req MessageRequest) (*MessageResponse, error)
}

// APIError represents an HTTP API error with a status code.
// AICall treats 429 and 5xx status codes as retryable.
type APIError struct {
	StatusCode int
	Msg        string
	// RetryAfter, when non-nil, carries the delay parsed from the
	// Retry-After response header. AICall's retry loop uses this duration
	// instead of the fixed RetryDelays schedule when present.
	RetryAfter *time.Duration
}

func (e *APIError) Error() string { return e.Msg }

// IsRetryable reports whether the error is a transient error that
// should be retried (rate limit 429, server 5xx, or connection error).
func IsRetryable(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 429 || apiErr.StatusCode >= 500
	}
	// Treat non-APIError errors (e.g. connection errors) as retryable,
	// unless they are context errors.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

// isCacheRejection checks whether the error is a cache_control rejection
// (a 400-class error with "cache_control" in the message).
func isCacheRejection(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 400 && strings.Contains(strings.ToLower(apiErr.Msg), "cache_control")
	}
	return false
}

// AICallOptions configures an AICall invocation.
type AICallOptions struct {
	// ModelTier is the model tier or model ID to use.
	ModelTier string
	// ModelVariant is the optional variant for tier resolution (e.g. "extended").
	ModelVariant string
	// MaxTokens is the maximum number of tokens to generate.
	// Defaults to 65536 when zero.
	MaxTokens int
	// Messages is the conversation message history.
	Messages []Message
	// System is the system prompt text.
	System string
	// Context describes the call site for logging/error messages.
	Context string
	// CachePolicy controls prompt caching behavior.
	// Defaults to CacheDefault when empty.
	CachePolicy CachePolicy
	// Temperature controls response randomness.
	Temperature *float64
	// Tools is the list of tool definitions available to the model.
	Tools []Tool
	// ToolChoice constrains which tool the model should use.
	ToolChoice any

	// Doer overrides the default Anthropic client for testing.
	// When nil, AICall creates a platform-aware client based on
	// environment variables.
	Doer Doer

	// ClientFactory overrides client creation for testing.
	// Called with the detected platform string ("direct", "vertex",
	// "bedrock"). When nil and Doer is nil, AICall creates the
	// appropriate Anthropic SDK client.
	ClientFactory func(platform string) (Doer, error)

	// SleepFunc overrides time.Sleep for testing retry delays.
	// When nil, AICall uses time.Sleep.
	SleepFunc func(time.Duration)
}

// ApplyDefaults fills in default values for unset AICallOptions fields.
// MaxTokens defaults to 65536 and CachePolicy defaults to CacheDefault.
func ApplyDefaults(opts *AICallOptions) {
	if opts.MaxTokens == 0 {
		opts.MaxTokens = 65536
	}
	if opts.CachePolicy == "" {
		opts.CachePolicy = CacheDefault
	}
}

// RetryDelays are the exponential backoff delays between retry attempts.
var RetryDelays = []time.Duration{
	2 * time.Second,
	30 * time.Second,
	60 * time.Second,
}

// detectPlatform determines which Anthropic API platform to use based
// on environment variables. Returns "vertex", "bedrock", or "direct".
func detectPlatform() string {
	if os.Getenv("CLAUDE_CODE_USE_VERTEX") != "" {
		return "vertex"
	}
	if os.Getenv("CLAUDE_CODE_USE_BEDROCK") != "" {
		return "bedrock"
	}
	return "direct"
}

// cacheThresholdForModel returns the token threshold above which
// cache_control should be injected, based on the resolved model ID.
// Sonnet models use 2048; opus/haiku models use 4096.
func cacheThresholdForModel(modelID string) int {
	if strings.Contains(modelID, "sonnet") {
		return sonnetCacheThreshold
	}
	return defaultCacheThreshold
}

// estimateTokens estimates the token count of text using the chars/4 heuristic.
func estimateTokens(text string) int {
	return len(text) / 4
}

// buildSystemBlocks converts a plain system prompt string into SystemBlock
// slices, optionally injecting cache_control based on the policy and model.
func buildSystemBlocks(system string, policy CachePolicy, modelID string) []SystemBlock {
	if system == "" {
		return nil
	}

	block := SystemBlock{
		Type: "text",
		Text: system,
	}

	// Inject cache_control if policy allows and token count exceeds threshold.
	if policy != CacheNone {
		threshold := cacheThresholdForModel(modelID)
		tokens := estimateTokens(system)
		if tokens > threshold {
			cc := map[string]string{"type": "ephemeral"}
			if policy == CacheExtended {
				cc["ttl"] = "1h"
			}
			block.CacheControl = cc
		}
	}

	return []SystemBlock{block}
}

// stripCacheControl returns a copy of the system blocks with all
// CacheControl fields set to nil.
func stripCacheControl(blocks []SystemBlock) []SystemBlock {
	out := make([]SystemBlock, len(blocks))
	for i, b := range blocks {
		out[i] = SystemBlock{
			Type: b.Type,
			Text: b.Text,
		}
	}
	return out
}

// extractText extracts the concatenated text from all text-type content
// blocks in a MessageResponse. Returns the text and true if at least one
// text block was found, or ("", false) otherwise.
func extractText(resp *MessageResponse) (string, bool) {
	if resp == nil || len(resp.Content) == 0 {
		return "", false
	}

	var texts []string
	found := false
	for _, block := range resp.Content {
		if block.Type == "text" {
			texts = append(texts, block.Text)
			found = true
		}
	}
	if !found {
		return "", false
	}
	return strings.Join(texts, ""), true
}

// AICall is the central LLM interface function. It resolves the model tier,
// creates a platform-aware Anthropic client, streams the response with retry
// and prompt caching, and returns the complete response text.
//
// Returns (responseText, rawResponse, nil) on success, or ("", nil, error)
// on failure. The error is wrapped as an *AgentError when appropriate.
func AICall(ctx context.Context, opts AICallOptions) (string, any, error) {
	// Check context cancellation early.
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}

	// Apply defaults.
	ApplyDefaults(&opts)

	// Resolve model tier to a model ID.
	modelID, err := ResolveModel(opts.ModelTier, opts.ModelVariant)
	if err != nil {
		return "", nil, &AgentError{
			Detail:        fmt.Sprintf("failed to resolve model: %v", err),
			ErrorCategory: "model",
			Retryable:     false,
			Cause:         err,
		}
	}

	// Obtain a Doer (LLM client).
	doer := opts.Doer
	if doer == nil {
		platform := detectPlatform()

		if opts.ClientFactory != nil {
			var factoryErr error
			doer, factoryErr = opts.ClientFactory(platform)
			if factoryErr != nil {
				return "", nil, &AgentError{
					Detail:        fmt.Sprintf("client factory error: %v", factoryErr),
					ErrorCategory: "client",
					Retryable:     false,
					Cause:         factoryErr,
				}
			}
		} else {
			// No Doer and no ClientFactory — check credentials.
			if platform == "direct" && os.Getenv("ANTHROPIC_API_KEY") == "" {
				return "", nil, &AgentError{
					Detail:        "missing API credentials: ANTHROPIC_API_KEY is empty and neither CLAUDE_CODE_USE_VERTEX nor CLAUDE_CODE_USE_BEDROCK is set",
					ErrorCategory: "auth",
					Retryable:     false,
				}
			}
			// In production, we'd create the real SDK client here.
			// For now, return an error since no client is available.
			return "", nil, &AgentError{
				Detail:        "no Doer or ClientFactory provided and real SDK client creation is not yet implemented",
				ErrorCategory: "client",
				Retryable:     false,
			}
		}
	}

	// Build system blocks with cache_control injection.
	systemBlocks := buildSystemBlocks(opts.System, opts.CachePolicy, modelID)

	// Build the request.
	req := MessageRequest{
		Model:       modelID,
		MaxTokens:   opts.MaxTokens,
		Messages:    opts.Messages,
		System:      systemBlocks,
		Tools:       opts.Tools,
		ToolChoice:  opts.ToolChoice,
		Temperature: opts.Temperature,
	}

	sleepFn := opts.SleepFunc
	if sleepFn == nil {
		sleepFn = time.Sleep
	}

	// Attempt the call with retry logic.
	maxAttempts := len(RetryDelays) + 1 // 4 total attempts
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// Check context cancellation before each attempt.
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}

		// Sleep before retry (not before first attempt).
		if attempt > 0 {
			delay := RetryDelays[attempt-1]
			// Honor Retry-After header from 429 responses when present.
			var prevAPIErr *APIError
			if errors.As(lastErr, &prevAPIErr) && prevAPIErr.RetryAfter != nil {
				delay = *prevAPIErr.RetryAfter
			}
			sleepFn(delay)
		}

		resp, callErr := doer.CreateMessage(ctx, req)
		if callErr == nil {
			// Success — extract text.
			text, found := extractText(resp)
			if !found {
				return "", resp, fmt.Errorf("AICall: response missing expected text content")
			}
			return text, resp, nil
		}

		// Check for cache_control rejection — retry without cache_control.
		if isCacheRejection(callErr) && hasCacheControl(req.System) {
			req.System = stripCacheControl(req.System)
			// Retry immediately (don't count as a retry attempt).
			resp2, callErr2 := doer.CreateMessage(ctx, req)
			if callErr2 == nil {
				text, found := extractText(resp2)
				if !found {
					return "", resp2, fmt.Errorf("AICall: response missing expected text content")
				}
				return text, resp2, nil
			}
			// Cache-free retry also failed — return that error.
			return "", nil, &AgentError{
				Detail:        fmt.Sprintf("AICall: cache-free retry also failed: %v", callErr2),
				ErrorCategory: "api",
				Retryable:     false,
				Cause:         callErr2,
			}
		}

		// If not retryable, fail immediately.
		if !IsRetryable(callErr) {
			return "", nil, callErr
		}

		lastErr = callErr
	}

	// All retries exhausted.
	return "", nil, &AgentError{
		Detail:        fmt.Sprintf("AICall: all %d attempts failed: %v", maxAttempts, lastErr),
		ErrorCategory: "api",
		Retryable:     false,
		Cause:         lastErr,
	}
}

// hasCacheControl reports whether any system block has cache_control set.
func hasCacheControl(blocks []SystemBlock) bool {
	for _, b := range blocks {
		if b.CacheControl != nil {
			return true
		}
	}
	return false
}
