package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/agentspec"
)

// --- Test helpers ---

// setupMockGenerate injects a mock generateFunc for the duration of the
// test. The mock writes minimal valid artifact files and returns success.
func setupMockGenerate(t *testing.T) {
	t.Helper()
	orig := generateFunc
	generateFunc = func(ctx context.Context, specPath string) (agentspec.GenerateResult, error) {
		reqContent := `{"spec_id":"TST-001","spec_name":"test_spec","requirements":[{"id":"REQ-TST-1","text":"Test requirement"}]}`
		tsContent := `{"spec_id":"TST-001","spec_name":"test_spec","test_cases":[{"id":"TC-TST-1","name":"Test case"}]}`
		tasksContent := `{"spec_id":"TST-001","spec_name":"test_spec","tasks":[{"id":"T-TST-1","title":"Test task"}]}`
		if err := os.WriteFile(filepath.Join(specPath, "requirements.json"), []byte(reqContent), 0644); err != nil {
			return agentspec.GenerateResult{}, err
		}
		if err := os.WriteFile(filepath.Join(specPath, "test_spec.json"), []byte(tsContent), 0644); err != nil {
			return agentspec.GenerateResult{}, err
		}
		if err := os.WriteFile(filepath.Join(specPath, "tasks.json"), []byte(tasksContent), 0644); err != nil {
			return agentspec.GenerateResult{}, err
		}
		return agentspec.GenerateResult{
			Artifacts: []string{"requirements", "test_spec", "tasks"},
		}, nil
	}
	t.Cleanup(func() { generateFunc = orig })
}

// --- TS-08-23: Verify that spec generate auto-accepts the PRD via AcceptPRD
//     before running Generate when session state is ASSESSING or REFINING ---

// TestTS08_23_GenerateAutoAcceptsAssessing verifies that when a spec's
// session state is ASSESSING, the generate command auto-accepts the PRD
// via AcceptPRD, transitions the session to an accepted state, runs
// Generate, and emits JSON with ok: true and an artifacts array.
// Covers: TS-08-23, Requirement: 08-REQ-9.1
func TestTS08_23_GenerateAutoAcceptsAssessing(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "08_my_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a session in ASSESSING state.
	sessionData := map[string]any{
		"state":               "assessing",
		"mode":                "standard",
		"prd_path":            "prd.md",
		"assessment_history":  []any{},
		"qa_exchanges":        []any{},
		"generated_artifacts": []any{},
	}
	sessionJSON, _ := json.Marshal(sessionData)
	if err := os.WriteFile(filepath.Join(specPath, "_session.json"), sessionJSON, 0644); err != nil {
		t.Fatal(err)
	}

	// Create a minimal prd.md.
	if err := os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# Test PRD\nContent"), 0644); err != nil {
		t.Fatal(err)
	}

	// Inject mock to avoid real AI call.
	setupMockGenerate(t)

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	stderrBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{"--spec-dir", specDir, "generate", "08_my_spec"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v\nstderr: %s", err, stderrBuf.String())
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", err, output)
	}

	if ok, _ := parsed["ok"].(bool); !ok {
		t.Errorf("parsed.ok = %v; want true", parsed["ok"])
	}

	artifacts, ok := parsed["artifacts"].([]any)
	if !ok {
		t.Fatal("parsed.artifacts is not an array")
	}
	if len(artifacts) == 0 {
		t.Error("parsed.artifacts is empty; want at least one artifact")
	}

	// Verify session state is no longer ASSESSING.
	data, err := os.ReadFile(filepath.Join(specPath, "_session.json"))
	if err != nil {
		t.Fatalf("failed to read _session.json: %v", err)
	}
	var session map[string]any
	if err := json.Unmarshal(data, &session); err != nil {
		t.Fatalf("_session.json is not valid JSON: %v", err)
	}
	state, _ := session["state"].(string)
	if state == "assessing" || state == "refining" {
		t.Errorf("session.state = %q after generate; want state not in [assessing, refining]", state)
	}
}

// TestTS08_23_GenerateAutoAcceptsRefining verifies that when a spec's
// session state is REFINING, the generate command auto-accepts the PRD
// and proceeds with generation.
// Covers: TS-08-23, Requirement: 08-REQ-9.1
func TestTS08_23_GenerateAutoAcceptsRefining(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "08_my_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a session in REFINING state.
	sessionData := map[string]any{
		"state":    "refining",
		"mode":     "standard",
		"prd_path": "prd.md",
		"assessment_history": []any{
			map[string]any{
				"quality":   "fair",
				"summary":   "Needs work",
				"gaps":      []string{},
				"questions": []any{},
			},
		},
		"qa_exchanges":        []any{},
		"generated_artifacts": []any{},
	}
	sessionJSON, _ := json.Marshal(sessionData)
	if err := os.WriteFile(filepath.Join(specPath, "_session.json"), sessionJSON, 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# Test PRD\nContent"), 0644); err != nil {
		t.Fatal(err)
	}

	// Inject mock to avoid real AI call.
	setupMockGenerate(t)

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "generate", "08_my_spec"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", err, output)
	}

	if ok, _ := parsed["ok"].(bool); !ok {
		t.Errorf("parsed.ok = %v; want true", parsed["ok"])
	}

	artifacts, ok := parsed["artifacts"].([]any)
	if !ok {
		t.Fatal("parsed.artifacts is not an array")
	}
	if len(artifacts) == 0 {
		t.Error("parsed.artifacts is empty; want at least one artifact")
	}

	// Verify session state is no longer REFINING.
	data, err := os.ReadFile(filepath.Join(specPath, "_session.json"))
	if err != nil {
		t.Fatalf("failed to read _session.json: %v", err)
	}
	var session map[string]any
	if err := json.Unmarshal(data, &session); err != nil {
		t.Fatalf("_session.json is not valid JSON: %v", err)
	}
	state, _ := session["state"].(string)
	if state == "assessing" || state == "refining" {
		t.Errorf("session.state = %q after generate; want state not in [assessing, refining]", state)
	}
}

// --- TS-08-24: Verify that spec generate runs Generate with a spinner and
//     emits JSON listing generated artifacts for a spec in accepted state ---

// TestTS08_24_GenerateAcceptedState verifies that running generate on
// a spec already in accepted state runs Generate with a spinner and
// emits JSON with ok: true and an artifacts array listing generated
// artifact paths (requirements.json, test_spec.json, tasks.json).
// Covers: TS-08-24, Requirement: 08-REQ-9.2
func TestTS08_24_GenerateAcceptedState(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "08_my_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a session in prd_accepted state (the valid agentspec state).
	sessionData := map[string]any{
		"state":               "prd_accepted",
		"mode":                "standard",
		"prd_path":            "prd.md",
		"assessment_history":  []any{},
		"qa_exchanges":        []any{},
		"generated_artifacts": []any{},
	}
	sessionJSON, _ := json.Marshal(sessionData)
	if err := os.WriteFile(filepath.Join(specPath, "_session.json"), sessionJSON, 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# Test PRD\nContent"), 0644); err != nil {
		t.Fatal(err)
	}

	// Inject mock to avoid real AI call.
	setupMockGenerate(t)

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	stderrBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{"--spec-dir", specDir, "generate", "08_my_spec"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v\nstderr: %s", err, stderrBuf.String())
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", err, output)
	}

	if ok, _ := parsed["ok"].(bool); !ok {
		t.Errorf("parsed.ok = %v; want true", parsed["ok"])
	}

	artifacts, ok := parsed["artifacts"].([]any)
	if !ok {
		t.Fatal("parsed.artifacts is not an array")
	}
	if len(artifacts) < 1 {
		t.Error("parsed.artifacts is empty; want at least one artifact path")
	}

	// Verify at least requirements.json is in the artifacts list.
	foundReqs := false
	for _, a := range artifacts {
		if s, ok := a.(string); ok {
			if filepath.Base(s) == "requirements.json" || s == "requirements.json" {
				foundReqs = true
			}
		}
	}
	if !foundReqs {
		t.Errorf("artifacts = %v; want requirements.json to be included", artifacts)
	}
}

// --- TS-08-25: Verify that spec generate --force deletes existing artifact
//     files before running Generate ---

// TestTS08_25_GenerateForceDeletesArtifacts verifies that running
// generate with --force deletes existing artifact files before
// regenerating fresh ones. The new artifact content should differ
// from the old marker content.
// Covers: TS-08-25, Requirement: 08-REQ-9.3
func TestTS08_25_GenerateForceDeletesArtifacts(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "08_my_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a session in prd_accepted state (the valid agentspec state).
	sessionData := map[string]any{
		"state":               "prd_accepted",
		"mode":                "standard",
		"prd_path":            "prd.md",
		"assessment_history":  []any{},
		"qa_exchanges":        []any{},
		"generated_artifacts": []string{"requirements.json", "test_spec.json", "tasks.json"},
	}
	sessionJSON, _ := json.Marshal(sessionData)
	if err := os.WriteFile(filepath.Join(specPath, "_session.json"), sessionJSON, 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# Test PRD\nContent"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create existing artifact files with known content.
	oldContent := []byte(`{"old": "marker_content_that_should_be_replaced"}`)
	for _, artifact := range []string{"requirements.json", "test_spec.json", "tasks.json"} {
		if err := os.WriteFile(filepath.Join(specPath, artifact), oldContent, 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Inject mock to avoid real AI call.
	setupMockGenerate(t)

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "generate", "08_my_spec", "--force"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", err, output)
	}

	if ok, _ := parsed["ok"].(bool); !ok {
		t.Errorf("parsed.ok = %v; want true", parsed["ok"])
	}

	artifacts, ok := parsed["artifacts"].([]any)
	if !ok {
		t.Fatal("parsed.artifacts is not an array")
	}
	if len(artifacts) == 0 {
		t.Error("parsed.artifacts is empty; want at least one artifact")
	}

	// Verify at least one artifact file was regenerated (content differs from old marker).
	reqsPath := filepath.Join(specPath, "requirements.json")
	if data, err := os.ReadFile(reqsPath); err == nil {
		if string(data) == string(oldContent) {
			t.Error("requirements.json still has old content after --force; want fresh content")
		}
	}
}

// --- 08-REQ-9.E2: Generate returns an error from the AI layer ---

// TestTS08_23_GenerateAILayerError verifies that when the Generate call
// returns an error from the AI layer, the command exits 1 with an error
// message and the spinner is stopped cleanly.
// Covers: 08-REQ-9.E2
func TestTS08_23_GenerateAILayerError(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "08_my_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a session in prd_accepted state.
	sessionData := map[string]any{
		"state":               "prd_accepted",
		"mode":                "standard",
		"prd_path":            "prd.md",
		"assessment_history":  []any{},
		"qa_exchanges":        []any{},
		"generated_artifacts": []any{},
	}
	sessionJSON, _ := json.Marshal(sessionData)
	if err := os.WriteFile(filepath.Join(specPath, "_session.json"), sessionJSON, 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# Test PRD"), 0644); err != nil {
		t.Fatal(err)
	}

	// Inject a mock that returns an AI layer error.
	orig := generateFunc
	generateFunc = func(ctx context.Context, sp string) (agentspec.GenerateResult, error) {
		return agentspec.GenerateResult{}, fmt.Errorf("AI layer error: model returned an error")
	}
	t.Cleanup(func() { generateFunc = orig })

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "generate", "08_my_spec"})

	err := cmd.Execute()
	// The AI layer error should be propagated and the command should fail.
	if err == nil {
		t.Error("Execute() returned nil; want error when AI fails")
	}

	// Verify no partial artifacts remain after AI error.
	for _, artifact := range []string{"requirements.json", "test_spec.json", "tasks.json"} {
		artifactPath := filepath.Join(specPath, artifact)
		if _, statErr := os.Stat(artifactPath); !os.IsNotExist(statErr) {
			t.Errorf("partial artifact %s exists after AI error; want cleaned up", artifact)
		}
	}
}

// --- 08-REQ-9.E3: --force with unremovable artifact file ---

// TestTS08_25_GenerateForcePermissionError verifies that when --force is
// set but an artifact file cannot be deleted due to permissions, the
// command returns an error before attempting generation.
// Covers: 08-REQ-9.E3
func TestTS08_25_GenerateForcePermissionError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping permission test when running as root")
	}

	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "08_my_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	sessionData := map[string]any{
		"state":               "prd_accepted",
		"mode":                "standard",
		"prd_path":            "prd.md",
		"assessment_history":  []any{},
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

	// Create an artifact file and make the directory read-only so the file
	// cannot be deleted.
	artifactPath := filepath.Join(specPath, "requirements.json")
	if err := os.WriteFile(artifactPath, []byte(`{"old": true}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(specPath, 0555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(specPath, 0755) // restore so TempDir cleanup works

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "generate", "08_my_spec", "--force"})

	err := cmd.Execute()
	if err == nil {
		t.Error("Execute() with --force on permission-denied artifact returned nil; want error")
	}
}

// --- Generate: missing spec argument ---

// TestTS08_23_GenerateMissingSpecArg verifies that spec generate requires
// a positional spec argument.
// Covers: 08-REQ-9
func TestTS08_23_GenerateMissingSpecArg(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"generate"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() with no spec argument returned nil; want error")
	}
}

// --- Generate: non-existent spec ---

// TestTS08_23_GenerateNonexistentSpec verifies that spec generate returns
// an error when the referenced spec does not exist.
// Covers: 08-REQ-9
func TestTS08_23_GenerateNonexistentSpec(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0755); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "generate", "nonexistent_spec"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() with non-existent spec returned nil; want error")
	}
}

// --- TS-NS-1: spec generate calls agentspec.GenerateSpec() instead of
//     emitting hardcoded stub artifacts ---

// TestTSNS1_GenerateUsesAIPipeline verifies that generated artifact files
// contain AI-generated content with real IDs — no REQ-GEN-1, TS-GEN-1,
// or T-GEN-1 placeholder values.
// Covers: NS-REQ-1, TS-NS-1
func TestTSNS1_GenerateUsesAIPipeline(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "47_ai_pipeline_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	sessionData := map[string]any{
		"state":               "prd_accepted",
		"mode":                "standard",
		"prd_path":            "prd.md",
		"assessment_history":  []any{},
		"qa_exchanges":        []any{},
		"generated_artifacts": []any{},
	}
	sessionJSON, _ := json.Marshal(sessionData)
	if err := os.WriteFile(filepath.Join(specPath, "_session.json"), sessionJSON, 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# AI Pipeline Test PRD\nContent"), 0644); err != nil {
		t.Fatal(err)
	}

	// Inject a mock that writes real (non-stub) artifact content.
	orig := generateFunc
	generateFunc = func(ctx context.Context, sp string) (agentspec.GenerateResult, error) {
		reqContent := `{"spec_id":"47","spec_name":"ai_pipeline_spec","requirements":[{"id":"REQ-47-1","text":"The system shall do X"}]}`
		tsContent := `{"spec_id":"47","spec_name":"ai_pipeline_spec","test_cases":[{"id":"TC-47-1","name":"Verify X"}]}`
		tasksContent := `{"spec_id":"47","spec_name":"ai_pipeline_spec","tasks":[{"id":"T-47-1","title":"Implement X"}]}`
		_ = os.WriteFile(filepath.Join(sp, "requirements.json"), []byte(reqContent), 0644)
		_ = os.WriteFile(filepath.Join(sp, "test_spec.json"), []byte(tsContent), 0644)
		_ = os.WriteFile(filepath.Join(sp, "tasks.json"), []byte(tasksContent), 0644)
		return agentspec.GenerateResult{Artifacts: []string{"requirements", "test_spec", "tasks"}}, nil
	}
	t.Cleanup(func() { generateFunc = orig })

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "generate", "47_ai_pipeline_spec"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// Verify artifact files do not contain stub IDs.
	stubIDs := []string{"REQ-GEN-1", "TS-GEN-1", "T-GEN-1"}
	for _, artifact := range []string{"requirements.json", "test_spec.json", "tasks.json"} {
		data, err := os.ReadFile(filepath.Join(specPath, artifact))
		if err != nil {
			t.Fatalf("cannot read %s: %v", artifact, err)
		}
		content := string(data)
		for _, stubID := range stubIDs {
			if strings.Contains(content, stubID) {
				t.Errorf("%s contains stub ID %q; want AI-generated content", artifact, stubID)
			}
		}
	}
}

// --- TS-NS-2: acceptPRD() writes state "prd_accepted" so that
//     agentspec.ResumeSession() can load the session without error ---

// TestTSNS2_GenerateAutoAcceptsWritesPRDAcceptedState verifies that after
// auto-accepting the PRD, _session.json contains state "prd_accepted"
// (not "accepted"), and agentspec.ResumeSession() can load it without error.
// Covers: NS-REQ-2, TS-NS-2
func TestTSNS2_GenerateAutoAcceptsWritesPRDAcceptedState(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "47_accept_state_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	// Start in ASSESSING state — generate should auto-accept to prd_accepted.
	sessionData := map[string]any{
		"state":               "assessing",
		"mode":                "standard",
		"prd_path":            "prd.md",
		"assessment_history":  []any{},
		"qa_exchanges":        []any{},
		"generated_artifacts": []any{},
	}
	sessionJSON, _ := json.Marshal(sessionData)
	if err := os.WriteFile(filepath.Join(specPath, "_session.json"), sessionJSON, 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# PRD\nContent"), 0644); err != nil {
		t.Fatal(err)
	}

	// Inject mock to avoid real AI call.
	setupMockGenerate(t)

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "generate", "47_accept_state_spec"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// Read _session.json and check state is "prd_accepted".
	data, err := os.ReadFile(filepath.Join(specPath, "_session.json"))
	if err != nil {
		t.Fatalf("failed to read _session.json: %v", err)
	}
	var session map[string]any
	if err := json.Unmarshal(data, &session); err != nil {
		t.Fatalf("_session.json is not valid JSON: %v", err)
	}
	state, _ := session["state"].(string)
	if state != "prd_accepted" {
		t.Errorf("session.state = %q after generate; want %q", state, "prd_accepted")
	}

	// Verify agentspec.ResumeSession can load the session without error.
	// Note: the mock wrote valid artifact files, so the session should have
	// generated_artifacts set, but ResumeSession only needs a valid state.
	_, resumeErr := agentspec.ResumeSession(specPath)
	if resumeErr != nil {
		t.Errorf("agentspec.ResumeSession() returned error: %v; want nil", resumeErr)
	}
}

// --- TS-NS-5 (generate): Missing API key produces clear error ---

// TestTSNS5_GenerateMissingAPIKeyError verifies that when ANTHROPIC_API_KEY
// is absent and no alternative provider is set, spec generate exits with
// non-zero status and an error message containing "ANTHROPIC_API_KEY".
// Covers: NS-REQ-5, TS-NS-5
func TestTSNS5_GenerateMissingAPIKeyError(t *testing.T) {
	// Unset every way a credential can reach the vendor.
	//
	// A base URL has to be cleared alongside the keys, because a credential
	// has three states and a gateway that authenticates by URL is one of
	// them: leaving ANTHROPIC_BASE_URL set is a configured environment, so
	// the preflight would pass and the run would go to the network for its
	// rejection instead of failing before it spends anything.
	for _, name := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN",
		"ANTHROPIC_BASE_URL", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_BEDROCK",
	} {
		t.Setenv(name, "")
	}

	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "47_no_key_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	sessionData := map[string]any{
		"state":               "prd_accepted",
		"mode":                "standard",
		"prd_path":            "prd.md",
		"assessment_history":  []any{},
		"qa_exchanges":        []any{},
		"generated_artifacts": []any{},
	}
	sessionJSON, _ := json.Marshal(sessionData)
	if err := os.WriteFile(filepath.Join(specPath, "_session.json"), sessionJSON, 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# PRD\nContent"), 0644); err != nil {
		t.Fatal(err)
	}

	// Do NOT inject a mock — use the real generateFunc so the API key check fires.
	cmd := newRootCmd()
	var stderrBuf bytes.Buffer
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(&stderrBuf)
	cmd.SetArgs([]string{"--spec-dir", specDir, "generate", "47_no_key_spec"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() returned nil; want error for missing API key")
	}

	// The error message should contain "ANTHROPIC_API_KEY".
	errMsg := err.Error()
	if !strings.Contains(errMsg, "ANTHROPIC_API_KEY") {
		t.Errorf("error message = %q; want it to contain %q", errMsg, "ANTHROPIC_API_KEY")
	}
}

// ---------------------------------------------------------------------------
// TS-NS-3 / TS-NS-4: Validation errors are surfaced to CLI user as warnings
// on stderr and cause a non-zero exit code.
// Requirements: NS-REQ-3, NS-REQ-4
// ---------------------------------------------------------------------------

// setupSpecWithSession creates a minimal spec directory with a prd_accepted
// session and prd.md, returning the spec directory path.
func setupSpecWithSession(t *testing.T, specDir, specName string) string {
	t.Helper()
	specPath := filepath.Join(specDir, specName)
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatalf("MkdirAll %s: %v", specPath, err)
	}
	sessionData := map[string]any{
		"state":               "prd_accepted",
		"mode":                "standard",
		"prd_path":            "prd.md",
		"assessment_history":  []any{},
		"qa_exchanges":        []any{},
		"generated_artifacts": []any{},
	}
	sessionJSON, _ := json.Marshal(sessionData)
	if err := os.WriteFile(filepath.Join(specPath, "_session.json"), sessionJSON, 0644); err != nil {
		t.Fatalf("write _session.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# Test PRD\nContent"), 0644); err != nil {
		t.Fatalf("write prd.md: %v", err)
	}
	return specPath
}

// TestTSNS3_GenerateValidationWarningsSurfacedToStderr verifies that when
// generateFunc returns a GenerateResult with non-empty Warnings, the
// spec generate command emits each warning to stderr with a "warning:" prefix.
// Test Spec: TS-NS-3, Requirement: NS-REQ-3
func TestTSNS3_GenerateValidationWarningsSurfacedToStderr(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	setupSpecWithSession(t, specDir, "66_validation_test")

	const warningMsg = "test_spec.json: dangling reference to 66-REQ-NONEXISTENT"

	orig := generateFunc
	generateFunc = func(ctx context.Context, sp string) (agentspec.GenerateResult, error) {
		// Write artifact stubs so the session save succeeds.
		for _, f := range []string{"requirements.json", "test_spec.json", "tasks.json"} {
			_ = os.WriteFile(filepath.Join(sp, f), []byte(`{"spec_id":"66"}`), 0644)
		}
		return agentspec.GenerateResult{
			Artifacts: []string{"requirements", "test_spec", "tasks"},
			Warnings:  []string{warningMsg},
		}, nil
	}
	t.Cleanup(func() { generateFunc = orig })

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	stderrBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{"--spec-dir", specDir, "generate", "66_validation_test"})

	err := cmd.Execute()

	// NS-REQ-3: warnings must appear in stderr.
	stderrOutput := stderrBuf.String()
	if !strings.Contains(stderrOutput, "warning:") {
		t.Errorf("stderr = %q; want 'warning:' prefix for validation errors", stderrOutput)
	}
	if !strings.Contains(stderrOutput, warningMsg) {
		t.Errorf("stderr = %q; want warning message %q", stderrOutput, warningMsg)
	}

	// NS-REQ-4: must exit non-zero when validation errors are present.
	if err == nil {
		t.Error("Execute() returned nil; want non-zero exit when validation warnings are present")
	}
}

// TestTSNS4_GenerateValidationExitsNonZero verifies that when generateFunc
// returns a GenerateResult with non-empty Warnings, spec generate exits with
// a non-zero exit code (returns an error from RunE).
// Test Spec: TS-NS-4, Requirement: NS-REQ-4
func TestTSNS4_GenerateValidationExitsNonZero(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	setupSpecWithSession(t, specDir, "66_exitcode_test")

	orig := generateFunc
	generateFunc = func(ctx context.Context, sp string) (agentspec.GenerateResult, error) {
		for _, f := range []string{"requirements.json", "test_spec.json", "tasks.json"} {
			_ = os.WriteFile(filepath.Join(sp, f), []byte(`{"spec_id":"66"}`), 0644)
		}
		return agentspec.GenerateResult{
			Artifacts: []string{"requirements", "test_spec", "tasks"},
			Warnings:  []string{"integrity error: dangling reference found"},
		}, nil
	}
	t.Cleanup(func() { generateFunc = orig })

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "generate", "66_exitcode_test"})

	err := cmd.Execute()
	if err == nil {
		t.Error("Execute() returned nil; want error (exit code 1) when validation warnings present")
	}
}

// TestTSNS3_GenerateNoWarningsExitsZero verifies that when generateFunc
// returns a GenerateResult with empty Warnings, spec generate succeeds
// (exit code 0) and emits OK JSON to stdout.
// Regression guard for NS-REQ-3/4: no false positives.
func TestTSNS3_GenerateNoWarningsExitsZero(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	setupSpecWithSession(t, specDir, "66_ok_test")

	orig := generateFunc
	generateFunc = func(ctx context.Context, sp string) (agentspec.GenerateResult, error) {
		for _, f := range []string{"requirements.json", "test_spec.json", "tasks.json"} {
			_ = os.WriteFile(filepath.Join(sp, f), []byte(`{"spec_id":"66"}`), 0644)
		}
		return agentspec.GenerateResult{
			Artifacts: []string{"requirements", "test_spec", "tasks"},
			Warnings:  nil,
		}, nil
	}
	t.Cleanup(func() { generateFunc = orig })

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	stderrBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{"--spec-dir", specDir, "generate", "66_ok_test"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("Execute() returned error %v; want nil when no validation warnings", err)
	}

	// stdout should contain the OK JSON response.
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(stdoutBuf.String()), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, stdoutBuf.String())
	}
	if ok, _ := parsed["ok"].(bool); !ok {
		t.Errorf("parsed.ok = %v; want true", parsed["ok"])
	}
}
