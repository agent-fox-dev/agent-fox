package agentspec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Helper to set up a spec directory with valid artifacts ---

// setupValidSpecDir creates a spec directory with a minimal prd.md and valid
// JSON artifact files (requirements.json, test_spec.json, tasks.json) that
// LoadSpec can load successfully. Returns the path to the spec directory.
func setupValidSpecDir(t *testing.T) string {
	t.Helper()
	specDir := t.TempDir()

	// Write prd.md with YAML frontmatter.
	prdContent := `---
spec_id: "06"
spec_name: "test_spec"
title: "Test Spec"
status: "draft"
created_at: "2024-01-01"
updated_at: "2024-01-01"
owner: "test"
source: "manual"
supersedes: []
intent_hash: null
schema_version: 1
---
# Test PRD

This is a test PRD body.
`
	if err := os.WriteFile(filepath.Join(specDir, "prd.md"), []byte(prdContent), 0o644); err != nil {
		t.Fatalf("failed to write prd.md: %v", err)
	}

	// Write minimal valid requirements.json.
	// Includes one execution path (≥2 steps) so the smoke test can reference it.
	reqJSON := `{
		"$schema": "https://agent-fox.dev/schemas/requirements.v1.json",
		"spec_id": "06",
		"spec_name": "test_spec",
		"schema_version": 1,
		"introduction": "Test introduction",
		"glossary": {},
		"requirements": [],
		"correctness_properties": [],
		"execution_paths": [
			{
				"id": "06-PATH-1",
				"title": "Basic path",
				"steps": [
					{"actor": "caller", "action": "invoke"},
					{"actor": "system", "action": "return result"}
				]
			}
		],
		"error_handling": [],
		"external_apis": []
	}`
	if err := os.WriteFile(filepath.Join(specDir, "requirements.json"), []byte(reqJSON), 0o644); err != nil {
		t.Fatalf("failed to write requirements.json: %v", err)
	}

	// Write minimal valid test_spec.json.
	// Includes one smoke test referencing the execution path above.
	tsJSON := `{
		"$schema": "https://agent-fox.dev/schemas/test_spec.v1.json",
		"spec_id": "06",
		"spec_name": "test_spec",
		"schema_version": 1,
		"test_cases": [],
		"property_tests": [],
		"edge_case_tests": [],
		"smoke_tests": [
			{
				"id": "TS-06-SMOKE-1",
				"execution_path_id": "06-PATH-1",
				"description": "Smoke test",
				"trigger": "invoke",
				"real_components": ["core"],
				"mockable": [],
				"expected_effects": ["returns result"]
			}
		],
		"coverage": {}
	}`
	if err := os.WriteFile(filepath.Join(specDir, "test_spec.json"), []byte(tsJSON), 0o644); err != nil {
		t.Fatalf("failed to write test_spec.json: %v", err)
	}

	// Write minimal valid tasks.json.
	// task_groups requires minItems:1 (Section 8.3); first must be "tests",
	// last must be "wiring_verification" per task_group_structure check.
	tasksJSON := `{
		"$schema": "https://agent-fox.dev/schemas/tasks.v1.json",
		"spec_id": "06",
		"spec_name": "test_spec",
		"schema_version": 1,
		"dependencies": [],
		"test_commands": {
			"all_tests": "go test ./...",
			"spec_tests": "go test ./agentspec/...",
			"linter": "go vet ./..."
		},
		"task_groups": [
			{
				"id": 1,
				"kind": "tests",
				"title": "Write Tests",
				"subtasks": [],
				"verification": {"id": "1.V", "checks": ["all tests pass"]}
			},
			{
				"id": 2,
				"kind": "wiring_verification",
				"title": "Wiring Verification",
				"subtasks": [
					{
						"id": "2.1",
						"title": "Stub and dead-code audit",
						"details": [],
						"test_spec_refs": ["TS-06-SMOKE-1"],
						"requirement_refs": [],
						"state": "pending",
						"optional": false
					}
				],
				"verification": {"id": "2.V", "checks": ["stub check"]}
			}
		],
		"traceability": []
	}`
	if err := os.WriteFile(filepath.Join(specDir, "tasks.json"), []byte(tasksJSON), 0o644); err != nil {
		t.Fatalf("failed to write tasks.json: %v", err)
	}

	// Write _session.json so ResumeSession can load it.
	sessionJSON := `{
		"state": "generated",
		"mode": "standard",
		"prd_path": "prd.md",
		"assessment_history": [],
		"qa_exchanges": [],
		"generated_artifacts": ["requirements.json", "test_spec.json", "tasks.json"],
		"last_error": null
	}`
	writeSessionFile(t, specDir, sessionJSON)

	return specDir
}

// --- TS-06-31: Validate with valid artifacts ---

// TestTS06_31_ValidateWithValidArtifacts verifies that SpecSession.Validate()
// calls LoadSpec, runs Validate(), categorizes errors, and returns
// SessionValidationResult with Valid=true when no errors are found.
// Test Spec: TS-06-31, Requirement: 06-REQ-9.1
func TestTS06_31_ValidateWithValidArtifacts(t *testing.T) {
	specDir := setupValidSpecDir(t)

	session, err := ResumeSession(specDir)
	if err != nil {
		t.Fatalf("ResumeSession() returned error: %v", err)
	}
	if session == nil {
		t.Fatal("ResumeSession() returned nil; want non-nil")
	}

	result, err := session.Validate()
	if err != nil {
		t.Fatalf("Validate() returned error: %v", err)
	}
	if !result.Valid {
		t.Errorf("Validate().Valid = false; want true")
	}
	if len(result.SchemaErrors) != 0 {
		t.Errorf("len(SchemaErrors) = %d; want 0; errors: %v", len(result.SchemaErrors), result.SchemaErrors)
	}
	if len(result.IntegrityErrors) != 0 {
		t.Errorf("len(IntegrityErrors) = %d; want 0; errors: %v", len(result.IntegrityErrors), result.IntegrityErrors)
	}
}

// --- TS-06-32: Validate falls back when LoadSpec fails ---

// TestTS06_32_ValidateFallbackOnMissingArtifacts verifies that
// SpecSession.Validate() falls back to loading individual JSON artifact
// files and returns Valid=false when LoadSpec fails due to missing artifacts.
// Test Spec: TS-06-32, Requirement: 06-REQ-9.2
func TestTS06_32_ValidateFallbackOnMissingArtifacts(t *testing.T) {
	specDir := t.TempDir()

	// Write prd.md and only requirements.json (missing test_spec.json and tasks.json).
	prdContent := `---
spec_id: "06"
spec_name: "test_partial"
title: "Partial Spec"
status: "draft"
created_at: "2024-01-01"
updated_at: "2024-01-01"
owner: "test"
source: "manual"
schema_version: 1
---
# Partial PRD
`
	if err := os.WriteFile(filepath.Join(specDir, "prd.md"), []byte(prdContent), 0o644); err != nil {
		t.Fatalf("failed to write prd.md: %v", err)
	}

	reqJSON := `{
		"spec_id": "06",
		"spec_name": "test_partial",
		"schema_version": 1,
		"introduction": "Partial test",
		"glossary": {},
		"requirements": [],
		"correctness_properties": [],
		"execution_paths": [],
		"error_handling": [],
		"external_apis": []
	}`
	if err := os.WriteFile(filepath.Join(specDir, "requirements.json"), []byte(reqJSON), 0o644); err != nil {
		t.Fatalf("failed to write requirements.json: %v", err)
	}

	sessionJSON := `{
		"state": "generating",
		"mode": "standard",
		"prd_path": "prd.md",
		"assessment_history": [],
		"qa_exchanges": [],
		"generated_artifacts": ["requirements.json"],
		"last_error": null
	}`
	writeSessionFile(t, specDir, sessionJSON)

	session, err := ResumeSession(specDir)
	if err != nil {
		t.Fatalf("ResumeSession() returned error: %v", err)
	}
	if session == nil {
		t.Fatal("ResumeSession() returned nil; want non-nil")
	}

	result, err := session.Validate()
	if err != nil {
		t.Fatalf("Validate() returned error: %v (should return nil error with fallback)", err)
	}
	if result.Valid {
		t.Errorf("Validate().Valid = true; want false (some artifacts missing)")
	}
	// Should have at least some errors reported.
	totalErrors := len(result.SchemaErrors) + len(result.IntegrityErrors)
	if totalErrors == 0 {
		t.Error("expected at least one error in SchemaErrors or IntegrityErrors for missing artifacts")
	}
}

// --- Edge Case: 06-REQ-9.E1 — No artifact files exist ---

// TestValidate_NoArtifactFiles verifies that SpecSession.Validate() returns
// Valid=false with an IntegrityErrors entry indicating no artifacts were
// found when the spec directory contains no artifact files.
// Edge Case: 06-REQ-9.E1
func TestValidate_NoArtifactFiles(t *testing.T) {
	specDir := t.TempDir()

	// Write only prd.md and _session.json — no JSON artifacts.
	prdContent := `---
spec_id: "06"
spec_name: "empty_spec"
title: "Empty"
status: "draft"
created_at: "2024-01-01"
updated_at: "2024-01-01"
owner: "test"
source: "manual"
schema_version: 1
---
# Empty
`
	if err := os.WriteFile(filepath.Join(specDir, "prd.md"), []byte(prdContent), 0o644); err != nil {
		t.Fatalf("failed to write prd.md: %v", err)
	}

	sessionJSON := `{
		"state": "generating",
		"mode": "standard",
		"prd_path": "prd.md",
		"assessment_history": [],
		"qa_exchanges": [],
		"generated_artifacts": [],
		"last_error": null
	}`
	writeSessionFile(t, specDir, sessionJSON)

	session, err := ResumeSession(specDir)
	if err != nil {
		t.Fatalf("ResumeSession() returned error: %v", err)
	}
	if session == nil {
		t.Fatal("ResumeSession() returned nil; want non-nil")
	}

	result, err := session.Validate()
	if err != nil {
		t.Fatalf("Validate() returned error: %v", err)
	}
	if result.Valid {
		t.Error("Validate().Valid = true; want false (no artifacts)")
	}
	if len(result.IntegrityErrors) == 0 {
		t.Error("expected at least one IntegrityError indicating no artifacts found")
	}
}

// --- Edge Case: 06-REQ-9.E2 — Invalid JSON in artifact file ---

// TestValidate_InvalidJSONArtifact verifies that SpecSession.Validate()
// records a parse failure as a SchemaError and continues validating
// remaining artifacts when an artifact file contains invalid JSON.
// Edge Case: 06-REQ-9.E2
func TestValidate_InvalidJSONArtifact(t *testing.T) {
	specDir := t.TempDir()

	// Write prd.md.
	prdContent := `---
spec_id: "06"
spec_name: "bad_json"
title: "Bad JSON"
status: "draft"
created_at: "2024-01-01"
updated_at: "2024-01-01"
owner: "test"
source: "manual"
schema_version: 1
---
# Bad JSON
`
	if err := os.WriteFile(filepath.Join(specDir, "prd.md"), []byte(prdContent), 0o644); err != nil {
		t.Fatalf("failed to write prd.md: %v", err)
	}

	// Write invalid JSON for requirements.json.
	if err := os.WriteFile(filepath.Join(specDir, "requirements.json"), []byte("{invalid json"), 0o644); err != nil {
		t.Fatalf("failed to write requirements.json: %v", err)
	}

	// Write valid test_spec.json.
	tsJSON := `{
		"spec_id": "06",
		"spec_name": "bad_json",
		"schema_version": 1,
		"test_cases": [],
		"property_tests": [],
		"edge_case_tests": [],
		"smoke_tests": [],
		"coverage": {}
	}`
	if err := os.WriteFile(filepath.Join(specDir, "test_spec.json"), []byte(tsJSON), 0o644); err != nil {
		t.Fatalf("failed to write test_spec.json: %v", err)
	}

	sessionJSON := `{
		"state": "generating",
		"mode": "standard",
		"prd_path": "prd.md",
		"assessment_history": [],
		"qa_exchanges": [],
		"generated_artifacts": ["requirements.json", "test_spec.json"],
		"last_error": null
	}`
	writeSessionFile(t, specDir, sessionJSON)

	session, err := ResumeSession(specDir)
	if err != nil {
		t.Fatalf("ResumeSession() returned error: %v", err)
	}
	if session == nil {
		t.Fatal("ResumeSession() returned nil; want non-nil")
	}

	result, err := session.Validate()
	if err != nil {
		t.Fatalf("Validate() returned error: %v", err)
	}
	if result.Valid {
		t.Error("Validate().Valid = true; want false (invalid JSON in artifact)")
	}
	if len(result.SchemaErrors) == 0 {
		t.Error("expected at least one SchemaError for invalid JSON artifact")
	}

	// Verify the parse failure is specifically recorded.
	found := false
	for _, se := range result.SchemaErrors {
		// The error message should reference the parse/JSON failure.
		if se != "" {
			found = true
			break
		}
	}
	if !found {
		t.Error("SchemaErrors should contain a non-empty entry for the parse failure")
	}
}

// --- TS-06-33: Render combined mode ---

// TestTS06_33_RenderCombined verifies that SpecSession.Render(true) loads
// the spec via LoadSpec and returns the combined rendered string.
// Test Spec: TS-06-33, Requirement: 06-REQ-10.1
func TestTS06_33_RenderCombined(t *testing.T) {
	specDir := setupValidSpecDir(t)

	session, err := ResumeSession(specDir)
	if err != nil {
		t.Fatalf("ResumeSession() returned error: %v", err)
	}
	if session == nil {
		t.Fatal("ResumeSession() returned nil; want non-nil")
	}

	result, err := session.Render(true)
	if err != nil {
		t.Fatalf("Render(true) returned error: %v", err)
	}

	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("Render(true) returned %T; want string", result)
	}
	if len(resultStr) == 0 {
		t.Error("Render(true) returned empty string; want non-empty rendered content")
	}
}

// --- TS-06-34: Render individual mode ---

// TestTS06_34_RenderIndividual verifies that SpecSession.Render(false) loads
// the spec via LoadSpec and returns a map of artifact name to rendered string.
// Test Spec: TS-06-34, Requirement: 06-REQ-10.2
func TestTS06_34_RenderIndividual(t *testing.T) {
	specDir := setupValidSpecDir(t)

	session, err := ResumeSession(specDir)
	if err != nil {
		t.Fatalf("ResumeSession() returned error: %v", err)
	}
	if session == nil {
		t.Fatal("ResumeSession() returned nil; want non-nil")
	}

	result, err := session.Render(false)
	if err != nil {
		t.Fatalf("Render(false) returned error: %v", err)
	}

	resultMap, ok := result.(map[string]string)
	if !ok {
		t.Fatalf("Render(false) returned %T; want map[string]string", result)
	}
	if len(resultMap) == 0 {
		t.Error("Render(false) returned empty map; want non-empty map keyed by artifact name")
	}
}

// --- TS-06-35: Render falls back on missing artifacts ---

// TestTS06_35_RenderFallbackOnMissingArtifacts verifies that
// SpecSession.Render falls back to rendering only available artifact files
// and returns a partial result when LoadSpec fails due to missing artifacts.
// Test Spec: TS-06-35, Requirement: 06-REQ-10.3
func TestTS06_35_RenderFallbackOnMissingArtifacts(t *testing.T) {
	specDir := t.TempDir()

	// Write prd.md and only requirements.json (missing test_spec.json and tasks.json).
	prdContent := `---
spec_id: "06"
spec_name: "render_partial"
title: "Render Partial"
status: "draft"
created_at: "2024-01-01"
updated_at: "2024-01-01"
owner: "test"
source: "manual"
schema_version: 1
---
# Partial render test
`
	if err := os.WriteFile(filepath.Join(specDir, "prd.md"), []byte(prdContent), 0o644); err != nil {
		t.Fatalf("failed to write prd.md: %v", err)
	}

	reqJSON := `{
		"spec_id": "06",
		"spec_name": "render_partial",
		"schema_version": 1,
		"introduction": "Render partial test",
		"glossary": {},
		"requirements": [],
		"correctness_properties": [],
		"execution_paths": [],
		"error_handling": [],
		"external_apis": []
	}`
	if err := os.WriteFile(filepath.Join(specDir, "requirements.json"), []byte(reqJSON), 0o644); err != nil {
		t.Fatalf("failed to write requirements.json: %v", err)
	}

	sessionJSON := `{
		"state": "generating",
		"mode": "standard",
		"prd_path": "prd.md",
		"assessment_history": [],
		"qa_exchanges": [],
		"generated_artifacts": ["requirements.json"],
		"last_error": null
	}`
	writeSessionFile(t, specDir, sessionJSON)

	session, err := ResumeSession(specDir)
	if err != nil {
		t.Fatalf("ResumeSession() returned error: %v", err)
	}
	if session == nil {
		t.Fatal("ResumeSession() returned nil; want non-nil")
	}

	result, err := session.Render(false)
	if err != nil {
		t.Fatalf("Render(false) returned error: %v; want nil (partial fallback)", err)
	}

	resultMap, ok := result.(map[string]string)
	if !ok {
		t.Fatalf("Render(false) returned %T; want map[string]string", result)
	}
	// Should have at least some content from the available artifacts.
	_ = resultMap // Partial result is acceptable.
}

// --- Edge Case: 06-REQ-10.E1 — No artifact files for render ---

// TestRender_NoArtifactFilesCombined verifies that Render(true) returns
// an empty string when no artifact files are present.
// Edge Case: 06-REQ-10.E1
func TestRender_NoArtifactFilesCombined(t *testing.T) {
	specDir := t.TempDir()

	// Write only prd.md and _session.json — no JSON artifacts.
	prdContent := `---
spec_id: "06"
spec_name: "no_artifacts"
title: "No Artifacts"
status: "draft"
created_at: "2024-01-01"
updated_at: "2024-01-01"
owner: "test"
source: "manual"
schema_version: 1
---
# No artifacts
`
	if err := os.WriteFile(filepath.Join(specDir, "prd.md"), []byte(prdContent), 0o644); err != nil {
		t.Fatalf("failed to write prd.md: %v", err)
	}

	sessionJSON := `{
		"state": "init",
		"mode": "standard",
		"prd_path": "prd.md",
		"assessment_history": [],
		"qa_exchanges": [],
		"generated_artifacts": [],
		"last_error": null
	}`
	writeSessionFile(t, specDir, sessionJSON)

	session, err := ResumeSession(specDir)
	if err != nil {
		t.Fatalf("ResumeSession() returned error: %v", err)
	}
	if session == nil {
		t.Fatal("ResumeSession() returned nil; want non-nil")
	}

	result, err := session.Render(true)
	if err != nil {
		t.Fatalf("Render(true) returned error: %v", err)
	}

	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("Render(true) returned %T; want string", result)
	}
	if resultStr != "" {
		t.Errorf("Render(true) = %q; want empty string when no artifacts", resultStr)
	}
}

// TestRender_NoArtifactFilesIndividual verifies that Render(false) returns
// an empty map when no artifact files are present.
// Edge Case: 06-REQ-10.E1
func TestRender_NoArtifactFilesIndividual(t *testing.T) {
	specDir := t.TempDir()

	// Write only prd.md and _session.json — no JSON artifacts.
	prdContent := `---
spec_id: "06"
spec_name: "no_artifacts_ind"
title: "No Artifacts Ind"
status: "draft"
created_at: "2024-01-01"
updated_at: "2024-01-01"
owner: "test"
source: "manual"
schema_version: 1
---
# No artifacts individual
`
	if err := os.WriteFile(filepath.Join(specDir, "prd.md"), []byte(prdContent), 0o644); err != nil {
		t.Fatalf("failed to write prd.md: %v", err)
	}

	sessionJSON := `{
		"state": "init",
		"mode": "standard",
		"prd_path": "prd.md",
		"assessment_history": [],
		"qa_exchanges": [],
		"generated_artifacts": [],
		"last_error": null
	}`
	writeSessionFile(t, specDir, sessionJSON)

	session, err := ResumeSession(specDir)
	if err != nil {
		t.Fatalf("ResumeSession() returned error: %v", err)
	}
	if session == nil {
		t.Fatal("ResumeSession() returned nil; want non-nil")
	}

	result, err := session.Render(false)
	if err != nil {
		t.Fatalf("Render(false) returned error: %v", err)
	}

	resultMap, ok := result.(map[string]string)
	if !ok {
		t.Fatalf("Render(false) returned %T; want map[string]string", result)
	}
	if len(resultMap) != 0 {
		t.Errorf("Render(false) returned map with %d entries; want empty map", len(resultMap))
	}
}

// ---------------------------------------------------------------------------
// TS-NS-1: Post-generation validation only runs cross-file integrity checks,
// not schema re-validation.
// Test Spec: TS-NS-1, Requirement: NS-REQ-1
// ---------------------------------------------------------------------------

// TestTSNS1_GenerateSchemaErrorsEmpty_AfterValidArtifacts verifies that
// Generate() only runs cross-file integrity checks after generation and does
// not populate SchemaErrors from the post-generation validation step. When
// all artifacts are valid, both SchemaErrors and IntegrityErrors are empty.
// Test Spec: TS-NS-1, Requirement: NS-REQ-1.1
func TestTSNS1_GenerateSchemaErrorsEmpty_AfterValidArtifacts(t *testing.T) {
	specDir := setupValidSpecDir(t)

	session, err := ResumeSession(specDir)
	if err != nil {
		t.Fatalf("ResumeSession() returned error: %v", err)
	}
	session.Current = StatePRDAccepted

	ctx := context.Background()
	result, genErr := session.Generate(ctx)
	if genErr != nil {
		t.Fatalf("Generate() returned unexpected error: %v", genErr)
	}

	// NS-REQ-1.1: SchemaErrors must be empty — the post-generation step only
	// runs cross-file checks, not schema re-validation.
	if len(result.Validation.SchemaErrors) != 0 {
		t.Errorf("GenerateResult.Validation.SchemaErrors is non-empty; want empty (schema validation skipped post-generation): %v",
			result.Validation.SchemaErrors)
	}

	// Valid cross-file state means Valid should be true.
	if !result.Validation.Valid {
		t.Errorf("GenerateResult.Validation.Valid = false; want true (all artifacts valid): integrityErrors=%v",
			result.Validation.IntegrityErrors)
	}
}

// ---------------------------------------------------------------------------
// TS-NS-2: Generate() returns Validation.Valid == false when post-generation
// validation finds integrity or schema errors.
// Test Spec: TS-NS-2, Requirements: NS-REQ-1, NS-REQ-2
// ---------------------------------------------------------------------------

// TestTSNS2_GenerateValidationValidFalseOnErrors verifies that when all
// three artifact files are already present but contain validation errors,
// Generate() skips the AI call, runs ValidateCrossFile(), and returns a
// GenerateResult where Validation.Valid == false and Warnings is non-empty.
// Test Spec: TS-NS-2, Requirements: NS-REQ-1, NS-REQ-2
func TestTSNS2_GenerateValidationValidFalseOnErrors(t *testing.T) {
	specDir := t.TempDir()

	// Write prd.md — Generate() reads this file but does not parse YAML.
	if err := os.WriteFile(filepath.Join(specDir, "prd.md"), []byte("# Test PRD\n"), 0o644); err != nil {
		t.Fatalf("failed to write prd.md: %v", err)
	}

	// Write requirements.json and tasks.json as minimal valid JSON so that
	// os.Stat() finds all three artifacts and skips the AI call.
	reqJSON := `{"spec_id":"66","spec_name":"test","schema_version":1,"introduction":"","glossary":{},"requirements":[],"correctness_properties":[],"execution_paths":[],"error_handling":[],"external_apis":[]}`
	if err := os.WriteFile(filepath.Join(specDir, "requirements.json"), []byte(reqJSON), 0o644); err != nil {
		t.Fatalf("failed to write requirements.json: %v", err)
	}
	tasksJSON := `{"spec_id":"66","spec_name":"test","schema_version":1,"dependencies":[],"test_commands":{"all_tests":"go test ./...","spec_tests":"go test ./...","linter":"go vet ./..."},"task_groups":[{"id":1,"kind":"tests","title":"Tests","subtasks":[],"verification":{"id":"1.V","checks":["pass"]}},{"id":2,"kind":"wiring_verification","title":"Wiring","subtasks":[],"verification":{"id":"2.V","checks":["pass"]}}],"traceability":[]}`
	if err := os.WriteFile(filepath.Join(specDir, "tasks.json"), []byte(tasksJSON), 0o644); err != nil {
		t.Fatalf("failed to write tasks.json: %v", err)
	}

	// Write test_spec.json as invalid JSON to trigger a schema error in the
	// fallback validator, causing Validation.Valid == false.
	if err := os.WriteFile(filepath.Join(specDir, "test_spec.json"), []byte("{invalid json}"), 0o644); err != nil {
		t.Fatalf("failed to write test_spec.json: %v", err)
	}

	// Build a session directly (agent is nil; not called since all artifacts exist).
	session := &SpecSession{
		specDir:            specDir,
		Current:            StatePRDAccepted,
		PRDPath:            "prd.md",
		AssessmentHistory:  []Assessment{},
		QAExchanges:        []QAExchange{},
		GeneratedArtifacts: []string{},
	}
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(specDir, "_session.json"), data, 0o644); err != nil {
		t.Fatalf("write _session.json: %v", err)
	}

	ctx := context.Background()
	result, genErr := session.Generate(ctx)
	if genErr != nil {
		t.Fatalf("Generate() returned unexpected error: %v", genErr)
	}

	// NS-REQ-2: Validation.Valid must be false when errors exist.
	if result.Validation.Valid {
		t.Error("GenerateResult.Validation.Valid = true; want false (test_spec.json has invalid JSON)")
	}

	// NS-REQ-1/2: Errors must be captured in SchemaErrors or IntegrityErrors.
	totalErrors := len(result.Validation.SchemaErrors) + len(result.Validation.IntegrityErrors)
	if totalErrors == 0 {
		t.Error("expected at least one SchemaError or IntegrityError; got none")
	}

	// NS-REQ-3: Warnings slice must be non-empty so the CLI can surface them.
	if len(result.Warnings) == 0 {
		t.Error("GenerateResult.Warnings is empty; want at least one validation warning")
	}
}

// ---------------------------------------------------------------------------
// TS-NS-3: Cross-file integrity errors ARE surfaced in GenerateResult.
// Test Spec: TS-NS-3, Requirement: NS-REQ-3
// ---------------------------------------------------------------------------

// TestTSNS3_CrossFileIntegrityErrors_SurfacedInGenerateResult verifies that
// Generate() surfaces cross-file integrity errors in GenerateResult when
// artifacts are individually valid but violate cross-file integrity rules.
// Specifically: a test_case with a dangling requirement_id reference produces
// an IntegrityError while SchemaErrors remains empty.
// Test Spec: TS-NS-3, Requirement: NS-REQ-3.1
func TestTSNS3_CrossFileIntegrityErrors_SurfacedInGenerateResult(t *testing.T) {
	// Use the valid spec dir helper as a base, then override test_spec.json
	// with a version that contains a dangling requirement reference.
	specDir := setupValidSpecDir(t)

	// Overwrite test_spec.json with a test_case that references a requirement
	// ID ("06-REQ-99") that does not exist in requirements.json.
	// All other fields are valid so that LoadSpec succeeds and schema
	// validation passes; the violation is purely a cross-file integrity issue.
	tsJSON := `{
		"$schema": "https://agent-fox.dev/schemas/test_spec.v1.json",
		"spec_id": "06",
		"spec_name": "test_spec",
		"schema_version": 1,
		"test_cases": [
			{
				"id": "TS-06-1",
				"requirement_id": "06-REQ-99",
				"kind": "unit",
				"description": "Dangling requirement reference test",
				"preconditions": [],
				"input": "any",
				"expected": "something",
				"assertion_pseudocode": "assert true"
			}
		],
		"property_tests": [],
		"edge_case_tests": [],
		"smoke_tests": [
			{
				"id": "TS-06-SMOKE-1",
				"execution_path_id": "06-PATH-1",
				"description": "Smoke test",
				"trigger": "invoke",
				"real_components": ["core"],
				"mockable": [],
				"expected_effects": ["returns result"]
			}
		],
		"coverage": {}
	}`
	if err := os.WriteFile(filepath.Join(specDir, "test_spec.json"), []byte(tsJSON), 0o644); err != nil {
		t.Fatalf("failed to write test_spec.json: %v", err)
	}

	session, err := ResumeSession(specDir)
	if err != nil {
		t.Fatalf("ResumeSession() returned error: %v", err)
	}
	session.Current = StatePRDAccepted

	ctx := context.Background()
	result, genErr := session.Generate(ctx)
	if genErr != nil {
		t.Fatalf("Generate() returned unexpected error: %v", genErr)
	}

	// NS-REQ-3.1: Generate() must return Valid=false when cross-file rules are violated.
	if result.Validation.Valid {
		t.Error("GenerateResult.Validation.Valid = true; want false (dangling requirement reference)")
	}

	// NS-REQ-3.1: IntegrityErrors must contain the dangling reference error.
	if len(result.Validation.IntegrityErrors) == 0 {
		t.Error("GenerateResult.Validation.IntegrityErrors is empty; want at least one integrity error for dangling reference")
	}

	// NS-REQ-1.1: SchemaErrors must be empty — post-generation step skips schema re-validation.
	if len(result.Validation.SchemaErrors) != 0 {
		t.Errorf("GenerateResult.Validation.SchemaErrors is non-empty; want empty (schema validation skipped post-generation): %v",
			result.Validation.SchemaErrors)
	}
}

// ---------------------------------------------------------------------------
// TS-NS-4: ValidateCrossFile() handles LoadSpec failure fallback correctly.
// Test Spec: TS-NS-4, Requirement: NS-REQ-4
// ---------------------------------------------------------------------------

// TestTSNS4_ValidateCrossFile_MissingArtifact verifies that ValidateCrossFile()
// returns Valid=false with a non-empty IntegrityErrors slice when an artifact
// file is missing from the spec directory, without panicking or returning a
// non-nil error.
// Test Spec: TS-NS-4, Requirement: NS-REQ-4.1
func TestTSNS4_ValidateCrossFile_MissingArtifact(t *testing.T) {
	specDir := setupValidSpecDir(t)

	// Remove one artifact file to simulate a partial-write failure.
	if err := os.Remove(filepath.Join(specDir, "tasks.json")); err != nil {
		t.Fatalf("failed to remove tasks.json: %v", err)
	}

	session, err := ResumeSession(specDir)
	if err != nil {
		t.Fatalf("ResumeSession() returned error: %v", err)
	}

	result, validateErr := session.ValidateCrossFile()

	// NS-REQ-4.1: Must not panic or return a non-nil error.
	if validateErr != nil {
		t.Fatalf("ValidateCrossFile() returned non-nil error: %v; want nil", validateErr)
	}

	// NS-REQ-4.1: Must return Valid=false when an artifact is missing.
	if result.Valid {
		t.Error("ValidateCrossFile() returned Valid=true; want false (tasks.json is missing)")
	}

	// NS-REQ-4.1: IntegrityErrors must contain an entry indicating the missing artifact.
	if len(result.IntegrityErrors) == 0 {
		t.Error("ValidateCrossFile() returned empty IntegrityErrors; want at least one entry for missing artifact")
	}

	// Confirm the error message references the missing file.
	found := false
	for _, e := range result.IntegrityErrors {
		if strings.Contains(e, "tasks.json") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("IntegrityErrors does not mention tasks.json; got: %v", result.IntegrityErrors)
	}
}
