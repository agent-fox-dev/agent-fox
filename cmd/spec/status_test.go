package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// --- TS-08-38: Verify that spec status emits JSON with state,
//     has_assessment, generated_artifacts, and optional last_error/quality
//     fields ---

// TestTS08_38_StatusEmitsJSON verifies that running spec status with a
// SPEC argument resumes the session in read-only mode and emits JSON
// with the required fields: ok, state, has_assessment, generated_artifacts.
// Covers: TS-08-38, Requirement: 08-REQ-13.1
func TestTS08_38_StatusEmitsJSON(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "08_my_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a session with known state.
	sessionData := map[string]any{
		"state":    "assessing",
		"mode":     "standard",
		"prd_path": "prd.md",
		"assessment_history": []any{
			map[string]any{"quality": "good", "summary": "Looks fine"},
		},
		"qa_exchanges":        []any{},
		"generated_artifacts": []string{"requirements.json"},
	}
	sessionJSON, _ := json.Marshal(sessionData)
	if err := os.WriteFile(filepath.Join(specPath, "_session.json"), sessionJSON, 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# Test PRD"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "status", "08_my_spec"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	// Verify required fields.
	if ok, _ := parsed["ok"].(bool); !ok {
		t.Errorf("parsed.ok = %v; want true", parsed["ok"])
	}

	if _, exists := parsed["state"]; !exists {
		t.Error("parsed missing 'state' field")
	}
	if _, ok := parsed["state"].(string); !ok {
		t.Errorf("parsed.state is %T; want string", parsed["state"])
	}

	if _, exists := parsed["has_assessment"]; !exists {
		t.Error("parsed missing 'has_assessment' field")
	}
	if _, ok := parsed["has_assessment"].(bool); !ok {
		t.Errorf("parsed.has_assessment is %T; want bool", parsed["has_assessment"])
	}

	if _, exists := parsed["generated_artifacts"]; !exists {
		t.Error("parsed missing 'generated_artifacts' field")
	}
	if _, ok := parsed["generated_artifacts"].([]any); !ok {
		t.Errorf("parsed.generated_artifacts is %T; want array", parsed["generated_artifacts"])
	}
}

// TestTS08_38_StatusWithAssessment verifies that when a session has
// assessment history, has_assessment is true.
// Covers: TS-08-38, Requirement: 08-REQ-13.1
func TestTS08_38_StatusWithAssessment(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "08_my_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	sessionData := map[string]any{
		"state":    "assessing",
		"mode":     "standard",
		"prd_path": "prd.md",
		"assessment_history": []any{
			map[string]any{"quality": "good", "summary": "Looks fine"},
		},
		"qa_exchanges":        []any{},
		"generated_artifacts": []any{},
	}
	sessionJSON, _ := json.Marshal(sessionData)
	if err := os.WriteFile(filepath.Join(specPath, "_session.json"), sessionJSON, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# PRD"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "status", "08_my_spec"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	hasAssessment, _ := parsed["has_assessment"].(bool)
	if !hasAssessment {
		t.Error("has_assessment = false; want true when assessment_history is non-empty")
	}
}

// TestTS08_38_StatusIncludesLastError verifies that the status output
// includes the last_error field when present in the session.
// Covers: TS-08-38, Requirement: 08-REQ-13.1
func TestTS08_38_StatusIncludesLastError(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "08_my_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	sessionData := map[string]any{
		"state":               "assessing",
		"mode":                "standard",
		"prd_path":            "prd.md",
		"assessment_history":  []any{},
		"qa_exchanges":        []any{},
		"generated_artifacts": []any{},
		"last_error":          "AI service returned 503",
	}
	sessionJSON, _ := json.Marshal(sessionData)
	if err := os.WriteFile(filepath.Join(specPath, "_session.json"), sessionJSON, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# PRD"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "status", "08_my_spec"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	lastError, exists := parsed["last_error"]
	if !exists {
		t.Error("parsed missing 'last_error' field; want it included when present in session")
	}
	if le, ok := lastError.(string); !ok || le == "" {
		t.Errorf("last_error = %v; want non-empty string", lastError)
	}
}

// TestTS08_38_StatusIncludesQuality verifies that the status output
// includes the quality field when present in the session.
// Covers: TS-08-38, Requirement: 08-REQ-13.1
func TestTS08_38_StatusIncludesQuality(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "08_my_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	sessionData := map[string]any{
		"state":    "assessing",
		"mode":     "standard",
		"prd_path": "prd.md",
		"assessment_history": []any{
			map[string]any{"quality": "excellent", "summary": "Perfect"},
		},
		"qa_exchanges":        []any{},
		"generated_artifacts": []any{},
		"quality":             "excellent",
	}
	sessionJSON, _ := json.Marshal(sessionData)
	if err := os.WriteFile(filepath.Join(specPath, "_session.json"), sessionJSON, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# PRD"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "status", "08_my_spec"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	quality, exists := parsed["quality"]
	if !exists {
		t.Error("parsed missing 'quality' field; want it included when present in session")
	}
	if q, ok := quality.(string); !ok || q == "" {
		t.Errorf("quality = %v; want non-empty string", quality)
	}
}

// --- 08-REQ-13.E1: Spec does not exist ---

// TestTS08_38_StatusNonexistentSpec verifies that when the spec does not
// exist or cannot be resolved, the status command returns a resolution
// error and exits 1.
// Covers: 08-REQ-13.E1
func TestTS08_38_StatusNonexistentSpec(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0755); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	stderrBuf := new(bytes.Buffer)
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{"--spec-dir", specDir, "status", "nonexistent_spec"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() with non-existent spec returned nil; want error")
	}
}

// --- 08-REQ-13.E2: Missing or malformed _session.json ---

// TestTS08_38_StatusMissingSession verifies that when _session.json is
// missing, the status command reports state as 'no_session' with
// has_assessment false and empty generated_artifacts.
// Covers: 08-REQ-13.E2
func TestTS08_38_StatusMissingSession(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "08_my_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	// Create prd.md but no _session.json.
	if err := os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# PRD"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "status", "08_my_spec"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v; want exit 0 for missing session", err)
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	if ok, _ := parsed["ok"].(bool); !ok {
		t.Errorf("parsed.ok = %v; want true", parsed["ok"])
	}

	state, _ := parsed["state"].(string)
	if state != "no_session" {
		t.Errorf("parsed.state = %q; want %q", state, "no_session")
	}

	hasAssessment, _ := parsed["has_assessment"].(bool)
	if hasAssessment {
		t.Error("has_assessment = true; want false for missing session")
	}

	artifacts, ok := parsed["generated_artifacts"].([]any)
	if !ok {
		t.Fatal("parsed.generated_artifacts is not an array")
	}
	if len(artifacts) != 0 {
		t.Errorf("generated_artifacts has %d entries; want 0 for missing session", len(artifacts))
	}
}

// TestTS08_38_StatusMalformedSession verifies that when _session.json
// contains malformed JSON, the status command reports state as
// 'no_session' with has_assessment false and empty generated_artifacts.
// Covers: 08-REQ-13.E2
func TestTS08_38_StatusMalformedSession(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "08_my_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# PRD"), 0644); err != nil {
		t.Fatal(err)
	}
	// Write malformed session JSON.
	if err := os.WriteFile(filepath.Join(specPath, "_session.json"),
		[]byte(`{not valid json!!!`), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "status", "08_my_spec"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v; want exit 0 for malformed session", err)
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	state, _ := parsed["state"].(string)
	if state != "no_session" {
		t.Errorf("parsed.state = %q; want %q", state, "no_session")
	}

	hasAssessment, _ := parsed["has_assessment"].(bool)
	if hasAssessment {
		t.Error("has_assessment = true; want false for malformed session")
	}

	artifacts, ok := parsed["generated_artifacts"].([]any)
	if !ok {
		t.Fatal("parsed.generated_artifacts is not an array")
	}
	if len(artifacts) != 0 {
		t.Errorf("generated_artifacts has %d entries; want 0 for malformed session", len(artifacts))
	}
}

// --- Status: requires spec argument ---

// TestTS08_38_StatusMissingArg verifies that spec status requires a
// positional SPEC argument.
// Covers: 08-REQ-13.1
func TestTS08_38_StatusMissingArg(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"status"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() with no spec argument returned nil; want error")
	}
}

// --- Status: banner suppressed for status subcommand ---

// TestTS08_38_StatusBannerSuppressed verifies that the banner is
// suppressed for the status subcommand (per 08-REQ-1.3, 08-PROP-6).
// Covers: 08-PROP-6
func TestTS08_38_StatusBannerSuppressed(t *testing.T) {
	if shouldShowBanner(false, "status", nil) {
		t.Error("shouldShowBanner(quiet=false, subcmd='status') = true; want false")
	}
}
