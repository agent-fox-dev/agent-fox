package agentspec

import (
	"errors"
	"testing"
)

// intPtr returns a pointer to the given int value.
func intPtr(n int) *int { return &n }

// TestTS06_01_AgentSpecErrorInterface verifies that any agentspec error
// satisfies the AgentSpecError interface and is matchable via errors.As,
// returning a non-empty Category() string.
// Test Spec: TS-06-1, Requirement: 06-REQ-1.1
func TestTS06_01_AgentSpecErrorInterface(t *testing.T) {
	// Use ConfigError as a representative agentspec error type.
	err := &ConfigError{Msg: "test"}

	var target AgentSpecError
	if !errors.As(err, &target) {
		t.Fatal("errors.As(ConfigError, &AgentSpecError) returned false; want true")
	}
	if cat := target.Category(); len(cat) == 0 {
		t.Errorf("target.Category() returned empty string; want non-empty")
	}
}

// TestTS06_02_ConfigErrorCategory verifies that ConfigError.Category()
// returns exactly "config" and satisfies the AgentSpecError interface.
// Test Spec: TS-06-2, Requirement: 06-REQ-1.2
func TestTS06_02_ConfigErrorCategory(t *testing.T) {
	err := &ConfigError{Msg: "invalid toml"}

	var target AgentSpecError
	if !errors.As(err, &target) {
		t.Fatal("errors.As(ConfigError, &AgentSpecError) returned false; want true")
	}
	if got := target.Category(); got != "config" {
		t.Errorf("target.Category() = %q; want %q", got, "config")
	}
}

// TestTS06_03_CampaignErrorCategory verifies that CampaignError.Category()
// returns exactly "campaign" and satisfies the AgentSpecError interface.
// Test Spec: TS-06-3, Requirement: 06-REQ-1.3
func TestTS06_03_CampaignErrorCategory(t *testing.T) {
	err := &CampaignError{Msg: "duplicate path"}

	var target AgentSpecError
	if !errors.As(err, &target) {
		t.Fatal("errors.As(CampaignError, &AgentSpecError) returned false; want true")
	}
	if got := target.Category(); got != "campaign" {
		t.Errorf("target.Category() = %q; want %q", got, "campaign")
	}
}

// TestTS06_04_SessionErrorCategory verifies that SessionError.Category()
// returns exactly "state" and satisfies the AgentSpecError interface.
// Test Spec: TS-06-4, Requirement: 06-REQ-1.4
func TestTS06_04_SessionErrorCategory(t *testing.T) {
	err := &SessionError{Msg: "illegal transition"}

	var target AgentSpecError
	if !errors.As(err, &target) {
		t.Fatal("errors.As(SessionError, &AgentSpecError) returned false; want true")
	}
	if got := target.Category(); got != "state" {
		t.Errorf("target.Category() = %q; want %q", got, "state")
	}
}

// TestTS06_05_AgentErrorFields verifies that AgentError carries Detail,
// ErrorCategory, Retryable, HTTPStatus fields, implements Unwrap()
// returning the original error, and satisfies AgentSpecError.
// Test Spec: TS-06-5, Requirement: 06-REQ-1.5
func TestTS06_05_AgentErrorFields(t *testing.T) {
	wrapped := errors.New("original")
	err := &AgentError{
		Detail:        "rate limited",
		ErrorCategory: "rate_limit",
		Retryable:     true,
		HTTPStatus:    intPtr(429),
		Cause:         wrapped,
	}

	// Verify AgentSpecError interface satisfaction via errors.As.
	var target AgentSpecError
	if !errors.As(err, &target) {
		t.Fatal("errors.As(AgentError, &AgentSpecError) returned false; want true")
	}

	// Verify Unwrap returns the original wrapped error.
	if got := errors.Unwrap(err); got != wrapped {
		t.Errorf("errors.Unwrap(err) = %v; want %v", got, wrapped)
	}

	// Verify field values.
	if err.Detail != "rate limited" {
		t.Errorf("Detail = %q; want %q", err.Detail, "rate limited")
	}
	if err.ErrorCategory != "rate_limit" {
		t.Errorf("ErrorCategory = %q; want %q", err.ErrorCategory, "rate_limit")
	}
	if !err.Retryable {
		t.Error("Retryable = false; want true")
	}
	if err.HTTPStatus == nil {
		t.Fatal("HTTPStatus is nil; want non-nil pointer to 429")
	}
	if *err.HTTPStatus != 429 {
		t.Errorf("*HTTPStatus = %d; want %d", *err.HTTPStatus, 429)
	}

	// Verify ErrorCategory is one of the valid ErrorCategory values.
	validCategories := map[string]bool{
		"rate_limit":     true,
		"auth":           true,
		"transient":      true,
		"overloaded":     true,
		"input":          true,
		"internal":       true,
		"validation":     true,
		"refusal":        true,
		"context_window": true,
		"pause_turn":     true,
	}
	if !validCategories[err.ErrorCategory] {
		t.Errorf("ErrorCategory = %q; want one of the valid ErrorCategory values", err.ErrorCategory)
	}
}

// TestAgentError_HTTPStatusNil verifies that when HTTPStatus is not
// applicable, it is represented as nil.
// Edge Case: 06-REQ-1.E1
func TestAgentError_HTTPStatusNil(t *testing.T) {
	err := &AgentError{
		Detail:        "internal failure",
		ErrorCategory: "internal",
		Retryable:     false,
		HTTPStatus:    nil,
		Cause:         nil,
	}

	if err.HTTPStatus != nil {
		t.Errorf("HTTPStatus = %v; want nil", err.HTTPStatus)
	}

	// Verify the error still satisfies AgentSpecError.
	var target AgentSpecError
	if !errors.As(err, &target) {
		t.Fatal("errors.As(AgentError with nil HTTPStatus, &AgentSpecError) returned false")
	}
}

// TestAgentError_InvalidCategory verifies that AgentError accepts an
// ErrorCategory outside the defined set without panicking.
// Edge Case: 06-REQ-1.E2
func TestAgentError_InvalidCategory(t *testing.T) {
	// Should not panic when given an invalid category.
	err := &AgentError{
		Detail:        "unknown error",
		ErrorCategory: "totally_made_up",
		Retryable:     false,
		HTTPStatus:    nil,
		Cause:         nil,
	}

	if err.ErrorCategory != "totally_made_up" {
		t.Errorf("ErrorCategory = %q; want %q", err.ErrorCategory, "totally_made_up")
	}

	// Verify it doesn't panic on Error() or Category().
	_ = err.Error()
	_ = err.Category()
}
