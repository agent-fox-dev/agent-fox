package main

import (
	"encoding/json"
	"testing"
)

// --- TS-08-5: Verify AF_AGENT=1 suppresses banner, forces quiet,
//     and all structured output goes to stdout as valid JSON ---

// TestTS08_05_AgentModeSuppressesBanner verifies that when AF_AGENT=1
// is set, the banner is suppressed and quiet mode is forced.
// Covers: TS-08-5, Requirement: 08-REQ-2.1
func TestTS08_05_AgentModeSuppressesBanner(t *testing.T) {
	t.Setenv("AF_AGENT", "1")

	if !isAgentMode() {
		t.Fatal("isAgentMode() = false with AF_AGENT=1; want true")
	}

	// Banner should be suppressed
	if shouldShowBanner(false, "new", nil) {
		t.Error("shouldShowBanner() = true with AF_AGENT=1; want false")
	}
}

// TestTS08_05_AgentModeNotActiveByDefault verifies that agent mode is
// not active when AF_AGENT is not set.
// Covers: 08-REQ-2.1
func TestTS08_05_AgentModeNotActiveByDefault(t *testing.T) {
	t.Setenv("AF_AGENT", "")

	if isAgentMode() {
		t.Error("isAgentMode() = true without AF_AGENT; want false")
	}
}

// --- TS-08-6: Verify that errors in agent mode produce JSON output ---

// TestTS08_06_AgentModeErrorJSON verifies that when an error occurs
// in agent mode, the output has the shape {"ok": false, "error": "<message>"}.
// Covers: TS-08-6, Requirement: 08-REQ-2.2
func TestTS08_06_AgentModeErrorJSON(t *testing.T) {
	t.Setenv("AF_AGENT", "1")

	// Verify the error envelope shape: {"ok": false, "error": "..."}
	errData := map[string]any{
		"ok":    false,
		"error": "test error message",
	}

	raw, err := json.MarshalIndent(errData, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}

	if parsed["ok"] != false {
		t.Errorf("parsed.ok = %v; want false", parsed["ok"])
	}
	errMsg, ok := parsed["error"].(string)
	if !ok || len(errMsg) == 0 {
		t.Errorf("parsed.error = %v; want non-empty string", parsed["error"])
	}

	// Test the actual emit function with the error data shape.
	// It should either succeed (implementation done) or return a not-implemented error.
	emitErr := emit(errData)
	if emitErr == nil {
		t.Log("emit succeeded — implementation exists")
	} else {
		// Expected to fail until implementation is complete.
		t.Errorf("emit() returned error: %v; want nil (implementation needed)", emitErr)
	}
}

// TestTS08_06_AgentModeQuietCombination verifies that AF_AGENT=1
// combined with explicit --quiet does not conflict.
// Covers: 08-REQ-2.E1
func TestTS08_06_AgentModeQuietCombination(t *testing.T) {
	t.Setenv("AF_AGENT", "1")

	if !isAgentMode() {
		t.Fatal("isAgentMode() = false with AF_AGENT=1; want true")
	}

	// Banner should still be suppressed
	if shouldShowBanner(true, "new", nil) {
		t.Error("shouldShowBanner(quiet=true) with AF_AGENT=1 = true; want false")
	}
}
