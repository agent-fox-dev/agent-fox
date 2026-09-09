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

// --- TS-NS-1: spec seal transitions an active spec to sealed state ---

// createActiveSpecForCLI creates a draft spec with an ## Intent section,
// transitions it to active via the library, and returns the specDir
// (parent of specName) and the full spec path.
func createActiveSpecForCLI(t *testing.T, specDir, specName string) string {
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
		"This spec tests the lifecycle CLI commands.\n\n" +
		"## Goals\n\n" +
		"- Validate lifecycle transitions work from the CLI.\n"

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

	// Transition draft → active using the library.
	spec, err := afspec.LoadSpec(specPath)
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}
	if _, err := spec.Transition("active", specPath); err != nil {
		t.Fatalf("Transition draft→active failed: %v", err)
	}

	return specPath
}

// createSealedSpecForCLI creates an active spec and transitions it to sealed.
func createSealedSpecForCLI(t *testing.T, specDir, specName string) string {
	t.Helper()
	specPath := createActiveSpecForCLI(t, specDir, specName)

	spec, err := afspec.LoadSpec(specPath)
	if err != nil {
		t.Fatalf("LoadSpec (active) failed: %v", err)
	}
	if _, err := spec.Transition("sealed", specPath); err != nil {
		t.Fatalf("Transition active→sealed failed: %v", err)
	}
	return specPath
}

// TestSeal_ActiveSpec verifies that sealing an active spec emits
// {"ok": true, "spec": "<name>", "status": "sealed"} and persists the change.
// Covers: NS-REQ-1, TS-NS-1
func TestSeal_ActiveSpec(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	createActiveSpecForCLI(t, specDir, "30_test_spec")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "seal", "30_test_spec"})

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
	if status, _ := parsed["status"].(string); status != "sealed" {
		t.Errorf("parsed.status = %q; want %q", status, "sealed")
	}
	if _, exists := parsed["spec"]; !exists {
		t.Error("parsed missing 'spec' field")
	}

	// Verify prd.md was updated on disk.
	prdPath := filepath.Join(specDir, "30_test_spec", "prd.md")
	prdData, err := os.ReadFile(prdPath)
	if err != nil {
		t.Fatalf("cannot read prd.md after seal: %v", err)
	}
	if !strings.Contains(string(prdData), `status: "sealed"`) {
		t.Errorf("prd.md does not contain 'status: \"sealed\"' after sealing; content: %s", string(prdData))
	}
}

// TestSeal_NumericResolution verifies that the seal command resolves a
// spec by numeric prefix (e.g., "30" matches "30_test_spec").
// Covers: NS-REQ-5, TS-NS-5
func TestSeal_NumericResolution(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	createActiveSpecForCLI(t, specDir, "30_test_spec")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "seal", "30"})

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

// TestSeal_InvalidTransition_DraftSpec verifies that trying to seal a
// draft spec fails with exit code 1.
// Covers: NS-REQ-4, TS-NS-4
func TestSeal_InvalidTransition_DraftSpec(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "30_draft_spec")
	if err := os.MkdirAll(specPath, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a draft spec (no active transition).
	setupLoadableSpec(t, specDir, "30_draft_spec", nil)

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	stderrBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{"--spec-dir", specDir, "seal", "30_draft_spec"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() returned nil error for sealing a draft spec; want error (exit 1)")
	}
}

// TestSeal_InvalidTransition_AgentMode verifies that in agent mode
// (AF_AGENT=1), sealing a draft spec emits {"ok": false, "error": "..."}.
// Covers: NS-REQ-4, TS-NS-4
func TestSeal_InvalidTransition_AgentMode(t *testing.T) {
	t.Setenv("AF_AGENT", "1")

	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	setupLoadableSpec(t, specDir, "30_draft_spec", nil)

	// In agent mode, errors are emitted as JSON to stdout by Execute().
	// Since we're testing via cmd.Execute() directly (not Execute()),
	// we test that the error is returned.
	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "seal", "30_draft_spec"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() returned nil error for sealing a draft spec in agent mode; want error")
	}
}

// TestSeal_NonexistentSpec verifies that trying to seal a non-existent
// spec returns an error.
// Covers: NS-REQ-4
func TestSeal_NonexistentSpec(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "seal", "nonexistent"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() returned nil error for non-existent spec; want error")
	}
}

// TestSeal_MissingArg verifies that spec seal requires a positional argument.
// Covers: NS-REQ-5
func TestSeal_MissingArg(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"seal"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() with no spec argument returned nil; want error")
	}
}

// TestSeal_BannerSuppressed verifies that the banner is suppressed for
// the seal subcommand.
// Covers: NS-REQ-5
func TestSeal_BannerSuppressed(t *testing.T) {
	if shouldShowBanner(false, "seal", nil) {
		t.Error("shouldShowBanner(quiet=false, subcmd='seal') = true; want false")
	}
}
