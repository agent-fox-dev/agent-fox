package agentspec

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Smoke Tests (TS-06-SMOKE-1 through TS-06-SMOKE-6) ---
//
// These end-to-end smoke tests verify cross-module wiring and integration
// across the agentspec package. They exercise the full execution paths
// described in the spec (06-PATH-1 through 06-PATH-6).

// TestSmoke_CreateCampaignAndProvisionFirstSpec creates a campaign directory
// and provisions the first spec, verifying all files are written correctly
// and the session is initialized.
// Test Spec: TS-06-SMOKE-1, Execution Path: 06-PATH-1
func TestSmoke_CreateCampaignAndProvisionFirstSpec(t *testing.T) {
	tmpDir := t.TempDir()
	campaignDir := filepath.Join(tmpDir, "smoke_campaign")

	// Step 1: Create campaign.
	campaign, err := CreateCampaign(campaignDir, "Smoke Campaign", "End-to-end smoke test campaign")
	if err != nil {
		t.Fatalf("CreateCampaign() returned error: %v", err)
	}
	if campaign == nil {
		t.Fatal("CreateCampaign() returned nil")
	}

	// Verify campaign.yaml exists with correct content.
	yamlPath := filepath.Join(campaignDir, "campaign.yaml")
	yamlData, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("failed to read campaign.yaml: %v", err)
	}
	yamlStr := string(yamlData)
	if !strings.Contains(yamlStr, "Smoke Campaign") {
		t.Error("campaign.yaml does not contain campaign name")
	}
	if !strings.Contains(yamlStr, "created_at") {
		t.Error("campaign.yaml does not contain created_at field")
	}

	// Step 2: Write a PRD file.
	prdPath := filepath.Join(tmpDir, "test_prd.md")
	if err := os.WriteFile(prdPath, []byte("# My PRD\nSome content."), 0o644); err != nil {
		t.Fatalf("failed to write PRD file: %v", err)
	}

	// Step 3: Provision first spec.
	session, err := campaign.NewSpec("my_spec", prdPath, "standard", "docs/prds/my.md")
	if err != nil {
		t.Fatalf("NewSpec() returned error: %v", err)
	}
	if session == nil {
		t.Fatal("NewSpec() returned nil session")
	}

	// Verify session is in StateInit.
	if session.State() != StateInit {
		t.Errorf("session.State() = %q; want %q", session.State(), StateInit)
	}

	// Verify spec directory exists as 01_my_spec.
	specDir := filepath.Join(campaignDir, "01_my_spec")
	if _, err := os.Stat(specDir); err != nil {
		t.Fatalf("spec directory does not exist: %v", err)
	}

	// Verify SpecDir() points to the spec directory.
	if session.SpecDir() != specDir {
		t.Errorf("session.SpecDir() = %q; want %q", session.SpecDir(), specDir)
	}

	// Verify prd.md exists with correct frontmatter.
	prdFilePath := filepath.Join(specDir, "prd.md")
	prdContent, err := os.ReadFile(prdFilePath)
	if err != nil {
		t.Fatalf("failed to read prd.md: %v", err)
	}
	prdStr := string(prdContent)
	for _, expected := range []string{
		"spec_id:", "spec_name:", "status: draft",
		"schema_version: 1", "source: docs/prds/my.md",
		"# My PRD",
	} {
		if !strings.Contains(prdStr, expected) {
			t.Errorf("prd.md does not contain %q", expected)
		}
	}

	// Verify _session.json exists with correct state.
	sessionPath := filepath.Join(specDir, "_session.json")
	sessionData, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("failed to read _session.json: %v", err)
	}
	var sessionJSON map[string]any
	if err := json.Unmarshal(sessionData, &sessionJSON); err != nil {
		t.Fatalf("failed to parse _session.json: %v", err)
	}
	if sessionJSON["state"] != "init" {
		t.Errorf("_session.json state = %v; want %q", sessionJSON["state"], "init")
	}
}

// TestSmoke_LoadConfigWithEnvOverride loads configuration with AF_SPEC_MODEL
// set, verifying the env var overrides the config file value.
// Test Spec: TS-06-SMOKE-2, Execution Path: 06-PATH-2
func TestSmoke_LoadConfigWithEnvOverride(t *testing.T) {
	tmpDir := t.TempDir()

	// Create .specs/config.toml with model="STANDARD" and provider settings.
	specsDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specsDir, 0o755); err != nil {
		t.Fatalf("failed to create .specs dir: %v", err)
	}
	configContent := `[model]
model = "STANDARD"

[provider]
auth_method = "vertex"
vertex_project = "smoke-project"
vertex_region = "us-central1"
`
	if err := os.WriteFile(filepath.Join(specsDir, "config.toml"), []byte(configContent), 0o644); err != nil {
		t.Fatalf("failed to write config.toml: %v", err)
	}

	// Change to tmpDir so LoadConfig finds .specs/config.toml.
	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer os.Chdir(origDir) //nolint:errcheck

	// Set AF_SPEC_MODEL to override.
	t.Setenv("AF_SPEC_MODEL", "FAST")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() returned error: %v", err)
	}

	// Model should be overridden by env var.
	if cfg.Model != "FAST" {
		t.Errorf("cfg.Model = %q; want %q (from AF_SPEC_MODEL)", cfg.Model, "FAST")
	}

	// Provider fields should be populated from config file.
	if cfg.AuthMethod != "vertex" {
		t.Errorf("cfg.AuthMethod = %q; want %q", cfg.AuthMethod, "vertex")
	}
	if cfg.VertexProject != "smoke-project" {
		t.Errorf("cfg.VertexProject = %q; want %q", cfg.VertexProject, "smoke-project")
	}
	if cfg.VertexRegion != "us-central1" {
		t.Errorf("cfg.VertexRegion = %q; want %q", cfg.VertexRegion, "us-central1")
	}
}

// TestSmoke_ResumeSessionAndAcceptPRD resumes a session from disk and
// accepts the PRD, verifying state transition and atomic persistence.
// Test Spec: TS-06-SMOKE-3, Execution Path: 06-PATH-3
func TestSmoke_ResumeSessionAndAcceptPRD(t *testing.T) {
	specDir := t.TempDir()

	// Write a _session.json in StateAssessing with assessment history.
	sessionJSON := `{
		"state": "assessing",
		"mode": "standard",
		"prd_path": "prd.md",
		"assessment_history": [
			{"quality": "needs_refinement", "summary": "needs work", "gaps": ["gap1"], "questions": []}
		],
		"qa_exchanges": [],
		"generated_artifacts": [],
		"last_error": null
	}`
	if err := os.WriteFile(filepath.Join(specDir, "_session.json"), []byte(sessionJSON), 0o644); err != nil {
		t.Fatalf("failed to write _session.json: %v", err)
	}

	// Step 1: Resume session.
	session, err := ResumeSession(specDir)
	if err != nil {
		t.Fatalf("ResumeSession() returned error: %v", err)
	}
	if session.State() != StateAssessing {
		t.Fatalf("session.State() = %q; want %q", session.State(), StateAssessing)
	}

	// Verify assessment was loaded.
	assessment := session.Assessment()
	if assessment == nil {
		t.Fatal("session.Assessment() returned nil; want non-nil")
	}
	if assessment.Quality != "needs_refinement" {
		t.Errorf("assessment.Quality = %q; want %q", assessment.Quality, "needs_refinement")
	}

	// Step 2: Accept PRD.
	if err := session.AcceptPRD(); err != nil {
		t.Fatalf("AcceptPRD() returned error: %v", err)
	}

	// Verify in-memory state.
	if session.State() != StatePRDAccepted {
		t.Errorf("session.State() = %q; want %q", session.State(), StatePRDAccepted)
	}

	// Verify persisted state.
	data, err := os.ReadFile(filepath.Join(specDir, "_session.json"))
	if err != nil {
		t.Fatalf("failed to read _session.json: %v", err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("failed to parse _session.json: %v", err)
	}
	if persisted["state"] != "prd_accepted" {
		t.Errorf("persisted state = %v; want %q", persisted["state"], "prd_accepted")
	}
}

// TestSmoke_ArtifactToolRequirements builds the artifact tool schema for
// "requirements", verifying InlineRefs and CleanSchema are applied correctly
// and the schema is used directly as input_schema without a 'content' wrapper.
// Test Spec: TS-06-SMOKE-4, Execution Path: 06-PATH-4
func TestSmoke_ArtifactToolRequirements(t *testing.T) {
	tools := ArtifactTool("requirements")
	if len(tools) != 1 {
		t.Fatalf("ArtifactTool(\"requirements\") returned %d tools; want 1", len(tools))
	}

	tool := tools[0]

	// Verify tool name.
	if tool["name"] != "submit_requirements" {
		t.Errorf("tool name = %v; want %q", tool["name"], "submit_requirements")
	}

	// Get the input schema — this IS the cleaned artifact schema directly.
	inputSchema, ok := tool["input_schema"].(map[string]any)
	if !ok {
		t.Fatal("tool input_schema is not a map")
	}
	props, ok := inputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("input_schema properties is not a map")
	}

	// The schema must NOT have a 'content' wrapper.
	if _, hasContent := props["content"]; hasContent {
		t.Error("input_schema.properties has a 'content' key; want flat artifact fields (no wrapper)")
	}

	// The flat schema must expose 'requirements' directly.
	if _, hasReqs := props["requirements"]; !hasReqs {
		t.Error("input_schema.properties missing 'requirements'; want top-level artifact field")
	}

	// Check that $schema is stripped at the input_schema top level.
	if _, ok := inputSchema["$schema"]; ok {
		t.Error("input_schema has $schema at top level; should be stripped by CleanSchema")
	}

	// Walk the entire input schema tree and verify no $ref, $defs, title,
	// or default keys exist at ANY nesting level.
	var walkSchema func(path string, m map[string]any)
	walkSchema = func(path string, m map[string]any) {
		for key, val := range m {
			switch key {
			case "$ref":
				t.Errorf("found $ref at %s", path)
			case "$defs":
				t.Errorf("found $defs at %s", path)
			case "title":
				t.Errorf("found title at %s", path)
			case "default":
				t.Errorf("found default at %s", path)
			}
			// Recurse into nested maps.
			if nested, ok := val.(map[string]any); ok {
				walkSchema(path+"."+key, nested)
			}
			// Recurse into arrays.
			if arr, ok := val.([]any); ok {
				for i, item := range arr {
					if nestedMap, ok := item.(map[string]any); ok {
						walkSchema(fmt.Sprintf("%s.%s[%d]", path, key, i), nestedMap)
					}
				}
			}
		}
	}

	walkSchema("input_schema", inputSchema)

	// Verify that description fields are preserved (the requirements schema
	// should have at least some description fields).
	var countDescriptions func(m map[string]any) int
	countDescriptions = func(m map[string]any) int {
		count := 0
		for key, val := range m {
			if key == "description" {
				count++
			}
			if nested, ok := val.(map[string]any); ok {
				count += countDescriptions(nested)
			}
			if arr, ok := val.([]any); ok {
				for _, item := range arr {
					if nestedMap, ok := item.(map[string]any); ok {
						count += countDescriptions(nestedMap)
					}
				}
			}
		}
		return count
	}

	descCount := countDescriptions(inputSchema)
	if descCount == 0 {
		t.Error("input_schema has no description fields; expected descriptions to be preserved")
	}
}

// TestSmoke_ValidateWithMissingArtifacts validates a session with some
// missing artifact files, verifying the fallback path loads individual
// artifacts and returns a structured result.
// Test Spec: TS-06-SMOKE-5, Execution Path: 06-PATH-5
func TestSmoke_ValidateWithMissingArtifacts(t *testing.T) {
	specDir := t.TempDir()

	// Create a session.
	session, err := CreateSession(specDir, "standard", "test.md")
	if err != nil {
		t.Fatalf("CreateSession() returned error: %v", err)
	}

	// Write only requirements.json (missing test_spec.json and tasks.json).
	// Use a minimal valid-structure JSON that will parse but may not validate.
	reqJSON := `{
		"spec_id": "01",
		"spec_name": "test_spec",
		"introduction": "Test",
		"glossary": [],
		"requirements": [],
		"correctness_properties": [],
		"execution_paths": [],
		"error_handling": [],
		"external_apis": []
	}`
	if err := os.WriteFile(filepath.Join(specDir, "requirements.json"), []byte(reqJSON), 0o644); err != nil {
		t.Fatalf("failed to write requirements.json: %v", err)
	}

	// Validate.
	result, err := session.Validate()
	if err != nil {
		t.Fatalf("Validate() returned error: %v", err)
	}

	// Should be invalid (missing artifacts).
	if result.Valid {
		t.Error("Validate() returned Valid=true; want false (missing artifacts)")
	}

	// Should have integrity errors for missing artifacts.
	allErrors := append(result.IntegrityErrors, result.SchemaErrors...)
	if len(allErrors) == 0 {
		t.Error("Validate() returned no errors; want errors for missing artifacts")
	}

	// Validate should not panic or return a non-nil error.
	// (Already checked above by checking err == nil.)
}

// TestSmoke_OpenCampaignAndListSpecs opens an existing campaign and lists
// specs sorted by numeric prefix, excluding archive/.
// Test Spec: TS-06-SMOKE-6, Execution Path: 06-PATH-6
func TestSmoke_OpenCampaignAndListSpecs(t *testing.T) {
	tmpDir := t.TempDir()

	// Step 1: Create campaign.
	campaign, err := CreateCampaign(tmpDir, "List Specs Campaign", "Smoke test for spec listing")
	if err != nil {
		t.Fatalf("CreateCampaign() returned error: %v", err)
	}

	// Create spec subdirectories in non-sequential order.
	for _, name := range []string{"03_gamma", "01_alpha", "02_beta"} {
		if err := os.MkdirAll(filepath.Join(tmpDir, name), 0o755); err != nil {
			t.Fatalf("failed to create dir %s: %v", name, err)
		}
	}

	// Create archive/ with an archived spec.
	archiveDir := filepath.Join(tmpDir, "archive")
	if err := os.MkdirAll(filepath.Join(archiveDir, "05_old"), 0o755); err != nil {
		t.Fatalf("failed to create archive/05_old: %v", err)
	}

	// Create a non-matching directory.
	if err := os.MkdirAll(filepath.Join(tmpDir, "notes"), 0o755); err != nil {
		t.Fatalf("failed to create notes dir: %v", err)
	}

	// Step 2: Open campaign.
	opened, err := OpenCampaign(tmpDir)
	if err != nil {
		t.Fatalf("OpenCampaign() returned error: %v", err)
	}
	if opened.Metadata.Name != "List Specs Campaign" {
		t.Errorf("opened campaign name = %q; want %q", opened.Metadata.Name, "List Specs Campaign")
	}

	// Step 3: List specs.
	specs, err := campaign.Specs()
	if err != nil {
		t.Fatalf("Specs() returned error: %v", err)
	}

	// Verify sorted order and exclusion of archive and non-matching dirs.
	expected := []string{"01_alpha", "02_beta", "03_gamma"}
	if len(specs) != len(expected) {
		t.Fatalf("Specs() returned %d entries; want %d: %v", len(specs), len(expected), specs)
	}
	for i, want := range expected {
		if specs[i] != want {
			t.Errorf("specs[%d] = %q; want %q", i, specs[i], want)
		}
	}

	// Verify archive is excluded.
	for _, s := range specs {
		if s == "archive" || strings.HasPrefix(s, "archive") {
			t.Errorf("specs contains %q; archive should be excluded", s)
		}
	}
}

// TestSmoke_ErrorTypesMatchable verifies the errors.As cross-cutting
// property: all agentspec error types are matchable via errors.As and
// expose a non-empty Category().
// This supplements TS-06-1 through TS-06-5 with an integration-level check.
func TestSmoke_ErrorTypesMatchable(t *testing.T) {
	errs := []error{
		&ConfigError{Msg: "smoke config error"},
		&CampaignError{Msg: "smoke campaign error"},
		&SessionError{Msg: "smoke session error"},
		&AgentError{Detail: "smoke agent error", ErrorCategory: "transient"},
	}

	for _, err := range errs {
		var target AgentSpecError
		if !errors.As(err, &target) {
			t.Errorf("errors.As(%T) = false; want true", err)
			continue
		}
		cat := target.Category()
		if cat == "" {
			t.Errorf("%T.Category() = empty; want non-empty", err)
		}
	}
}

// TestSmoke_CreateSessionResumeRoundTrip verifies the round-trip property:
// CreateSession -> ResumeSession returns identical session state.
// This supplements TS-06-22 and TS-06-23 with a property-level check.
func TestSmoke_CreateSessionResumeRoundTrip(t *testing.T) {
	specDir := t.TempDir()

	original, err := CreateSession(specDir, "standard", "docs/prds/roundtrip.md")
	if err != nil {
		t.Fatalf("CreateSession() returned error: %v", err)
	}

	resumed, err := ResumeSession(specDir)
	if err != nil {
		t.Fatalf("ResumeSession() returned error: %v", err)
	}

	// Compare key fields.
	if resumed.State() != original.State() {
		t.Errorf("resumed.State() = %q; want %q", resumed.State(), original.State())
	}
	if resumed.Mode != original.Mode {
		t.Errorf("resumed.Mode = %q; want %q", resumed.Mode, original.Mode)
	}
	if resumed.PRDPath != original.PRDPath {
		t.Errorf("resumed.PRDPath = %q; want %q", resumed.PRDPath, original.PRDPath)
	}
	if len(resumed.AssessmentHistory) != len(original.AssessmentHistory) {
		t.Errorf("resumed.AssessmentHistory length = %d; want %d",
			len(resumed.AssessmentHistory), len(original.AssessmentHistory))
	}
	if len(resumed.QAExchanges) != len(original.QAExchanges) {
		t.Errorf("resumed.QAExchanges length = %d; want %d",
			len(resumed.QAExchanges), len(original.QAExchanges))
	}
	if len(resumed.GeneratedArtifacts) != len(original.GeneratedArtifacts) {
		t.Errorf("resumed.GeneratedArtifacts length = %d; want %d",
			len(resumed.GeneratedArtifacts), len(original.GeneratedArtifacts))
	}
}
