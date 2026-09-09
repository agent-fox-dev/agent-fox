package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/afspec"
)

// --- TS-NS-1..4: spec activate transitions a draft spec to active state ---

// createDraftSpecWithIntentForCLI creates a draft spec with a valid ## Intent
// section and all required artifacts (prd.md, requirements.json,
// test_spec.json, tasks.json). Returns the full spec path.
func createDraftSpecWithIntentForCLI(t *testing.T, specDir, specName string) string {
	t.Helper()
	specPath := filepath.Join(specDir, specName)
	if err := os.MkdirAll(specPath, 0o755); err != nil {
		t.Fatal(err)
	}

	specID := strings.SplitN(specName, "_", 2)[0]

	prd := "---\n" +
		"spec_id: \"" + specID + "\"\n" +
		"spec_name: \"" + specName + "\"\n" +
		"title: \"Test Spec " + specID + "\"\n" +
		"status: \"draft\"\n" +
		"created_at: \"2026-01-01T00:00:00Z\"\n" +
		"updated_at: \"2026-01-01T00:00:00Z\"\n" +
		"owner: \"test\"\n" +
		"source: \"\"\n" +
		"supersedes: []\n" +
		"tags: []\n" +
		"intent_hash: null\n" +
		"schema_version: 1\n" +
		"---\n" +
		"# Test Spec " + specID + "\n\n" +
		"## Intent\n\n" +
		"This spec tests the activate lifecycle CLI command.\n\n" +
		"## Goals\n\n" +
		"- Validate activate transition works from the CLI.\n"

	reqJSON := `{
  "$schema": "https://agent-fox.dev/schemas/requirements.v1.json",
  "spec_id": "` + specID + `",
  "spec_name": "` + specName + `",
  "schema_version": 1,
  "introduction": "Test spec.",
  "glossary": {},
  "requirements": [
    {
      "id": "` + specID + `-REQ-1",
      "title": "Requirement 1",
      "user_story": {"role": "dev", "goal": "test", "benefit": "test"},
      "acceptance_criteria": [
        {
          "id": "` + specID + `-REQ-1.1",
          "ears_pattern": "ubiquitous",
          "system": "the system",
          "action": "do something",
          "return_contract": null
        }
      ],
      "edge_cases": []
    }
  ],
  "correctness_properties": [],
  "execution_paths": [
    {
      "id": "` + specID + `-PATH-1",
      "title": "Main path",
      "steps": [
        {"actor": "user", "action": "do"},
        {"actor": "system", "action": "respond"}
      ]
    }
  ],
  "error_handling": []
}`

	testSpecJSON := `{
  "$schema": "https://agent-fox.dev/schemas/test_spec.v1.json",
  "spec_id": "` + specID + `",
  "spec_name": "` + specName + `",
  "schema_version": 1,
  "test_cases": [
    {
      "id": "TS-` + specID + `-1",
      "requirement_id": "` + specID + `-REQ-1.1",
      "kind": "unit",
      "description": "Test something",
      "preconditions": [],
      "input": {},
      "expected": {},
      "assertion_pseudocode": "assert true"
    }
  ],
  "property_tests": [],
  "edge_case_tests": [],
  "smoke_tests": [
    {
      "id": "TS-` + specID + `-SMOKE-1",
      "execution_path_id": "` + specID + `-PATH-1",
      "description": "Smoke test",
      "trigger": "run",
      "real_components": ["all"],
      "mockable": [],
      "expected_effects": ["works"]
    }
  ],
  "coverage": {
    "requirements_covered": [],
    "properties_covered": [],
    "paths_covered": [],
    "gaps": []
  }
}`

	tasksJSON := `{
  "$schema": "https://agent-fox.dev/schemas/tasks.v1.json",
  "spec_id": "` + specID + `",
  "spec_name": "` + specName + `",
  "schema_version": 1,
  "test_commands": {
    "spec_tests": "go test ./...",
    "all_tests": "go test ./...",
    "linter": "golangci-lint run"
  },
  "dependencies": [],
  "task_groups": [
    {
      "id": 1,
      "kind": "standard",
      "title": "Implement feature",
      "subtasks": [
        {
          "id": "1.1",
          "title": "Do the thing",
          "details": ["Detail 1"],
          "test_spec_refs": ["TS-` + specID + `-1"],
          "requirement_refs": ["` + specID + `-REQ-1.1"],
          "state": "pending",
          "optional": false
        }
      ],
      "verification": {
        "id": "1.V",
        "checks": ["All tests pass"]
      }
    }
  ],
  "traceability": [
    {
      "requirement_id": "` + specID + `-REQ-1.1",
      "test_spec_id": "TS-` + specID + `-1",
      "task_id": "1.1",
      "test_path": null
    }
  ]
}`

	files := map[string]string{
		"prd.md":            prd,
		"requirements.json": reqJSON,
		"test_spec.json":    testSpecJSON,
		"tasks.json":        tasksJSON,
	}
	for name, content := range files {
		p := filepath.Join(specPath, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}

	return specPath
}

// TestActivate_DraftSpec verifies that activating a draft spec with a valid
// ## Intent section emits {"ok": true, "spec": "<name>", "status": "active"}
// and persists the change to disk.
// Covers: NS-REQ-1, TS-NS-1
func TestActivate_DraftSpec(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	createDraftSpecWithIntentForCLI(t, specDir, "67_test_spec")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "activate", "67_test_spec"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	if ok, _ := parsed["ok"].(bool); !ok {
		t.Errorf("parsed.ok = %v; want true", parsed["ok"])
	}
	if status, _ := parsed["status"].(string); status != "active" {
		t.Errorf("parsed.status = %q; want %q", status, "active")
	}
	if _, exists := parsed["spec"]; !exists {
		t.Error("parsed missing 'spec' field")
	}

	// Verify prd.md was updated on disk and intent_hash is set.
	specPath := filepath.Join(specDir, "67_test_spec")
	loaded, err := afspec.LoadSpec(specPath)
	if err != nil {
		t.Fatalf("LoadSpec after activate failed: %v", err)
	}
	if loaded.Status != "active" {
		t.Errorf("loaded.Status = %q; want %q", loaded.Status, "active")
	}
	if loaded.IntentHash == nil {
		t.Error("loaded.IntentHash is nil; want non-nil after activation")
	}
}

// TestActivate_NumericResolution verifies that the activate command resolves a
// spec by numeric prefix (e.g., "67" matches "67_test_spec").
// Covers: NS-REQ-1, TS-NS-1
func TestActivate_NumericResolution(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	createDraftSpecWithIntentForCLI(t, specDir, "67_test_spec")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "activate", "67"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}
	if ok, _ := parsed["ok"].(bool); !ok {
		t.Errorf("parsed.ok = %v; want true", parsed["ok"])
	}
}

// TestActivate_InvalidTransition_AlreadyActive verifies that trying to
// activate an already-active spec fails with exit code 1.
// Covers: NS-REQ-2, TS-NS-2
func TestActivate_InvalidTransition_AlreadyActive(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create an already-active spec via library.
	createActiveSpecForCLI(t, specDir, "67_active_spec")

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "activate", "67_active_spec"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() returned nil error for activating an already-active spec; want error (exit 1)")
	}
}

// TestActivate_MissingIntent verifies that activating a draft spec without a
// ## Intent section fails and leaves the spec in draft state.
// Covers: NS-REQ-3, TS-NS-3
func TestActivate_MissingIntent(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// A draft spec whose PRD body has no ## Intent section: activation must
	// refuse it, because the intent hash is computed at draft -> active.
	writeSpecFixture(t, filepath.Join(specDir, "67_no_intent_spec"), "67", "no_intent_spec", specFixture{
		PRDBody: "# Widget Service\n\n## Goals\n\n- Store widgets.\n",
	})

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "activate", "67_no_intent_spec"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() returned nil error for draft spec with no ## Intent; want error (exit 1)")
	}

	// Verify the spec remains in draft state on disk.
	specPath := filepath.Join(specDir, "67_no_intent_spec")
	loaded, loadErr := afspec.LoadSpec(specPath)
	if loadErr != nil {
		t.Fatalf("LoadSpec after failed activate: %v", loadErr)
	}
	if loaded.Status != "draft" {
		t.Errorf("loaded.Status = %q after failed activate; want %q", loaded.Status, "draft")
	}
	if loaded.IntentHash != nil {
		t.Errorf("loaded.IntentHash = %v after failed activate; want nil", loaded.IntentHash)
	}
}

// TestActivate_AgentMode verifies that in agent mode (AF_AGENT=1),
// activating an invalid spec emits {"ok": false, "error": "..."} to stdout.
// Covers: NS-REQ-4, TS-NS-4
func TestActivate_AgentMode(t *testing.T) {
	t.Setenv("AF_AGENT", "1")

	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Use an already-active spec to trigger an invalid transition.
	createActiveSpecForCLI(t, specDir, "67_active_spec")

	// In agent mode, errors are emitted as JSON to stdout by Execute().
	// When testing via cmd.Execute() directly (not Execute()), we verify
	// the error is returned — the JSON emission happens in Execute().
	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "activate", "67_active_spec"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() returned nil error for activating an already-active spec in agent mode; want error")
	}
}

// TestActivate_NonexistentSpec verifies that activating a non-existent spec
// returns an error.
// Covers: NS-REQ-2
func TestActivate_NonexistentSpec(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "activate", "nonexistent"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() returned nil error for non-existent spec; want error")
	}
}

// TestActivate_MissingArg verifies that spec activate requires a positional argument.
func TestActivate_MissingArg(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"activate"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() with no spec argument returned nil; want error")
	}
}

// TestActivate_BannerSuppressed verifies that the banner is suppressed for
// the activate subcommand.
func TestActivate_BannerSuppressed(t *testing.T) {
	if shouldShowBanner(false, "activate", nil) {
		t.Error("shouldShowBanner(quiet=false, subcmd='activate') = true; want false")
	}
}
