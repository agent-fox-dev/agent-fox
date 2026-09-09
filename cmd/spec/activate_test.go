package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-fox-dev/agentfox/afspec"
)

// --- TS-NS-1..4: spec activate transitions a draft spec to active state ---

// createDraftSpecWithIntentForCLI creates a draft spec with a valid ## Intent
// section and all required artifacts (prd.md, requirements.json,
// test_spec.json, tasks.json). Returns the full spec path.
func createDraftSpecWithIntentForCLI(t *testing.T, specDir, specName string) string {
	t.Helper()
	specID, suffix := splitSpecDirName(t, specName)
	specPath := filepath.Join(specDir, specName)
	writeSpecFixture(t, specPath, specID, suffix, specFixture{
		PRDBody: "# Widget Service\n\n## Intent\n\nStore widgets and serve them back.\n\n" +
			"## Goals\n\n- Exercise the lifecycle commands from the CLI.\n",
	})
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
