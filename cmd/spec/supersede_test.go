package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- TS-NS-3: spec supersede transitions a sealed spec to superseded ---

// TestSupersede_SealedSpec verifies that superseding a sealed spec emits
// {"ok": true, "spec": "<name>", "status": "superseded", "superseded_by": "<id>"}
// and persists the deprecation banner to prd.md.
// Covers: NS-REQ-3, TS-NS-3
func TestSupersede_SealedSpec(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	createSealedSpecForCLI(t, specDir, "30_sealed_spec")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{
		"--spec-dir", specDir,
		"supersede", "30_sealed_spec",
		"--by", "99_replacement",
	})

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
	if status, _ := parsed["status"].(string); status != "superseded" {
		t.Errorf("parsed.status = %q; want %q", status, "superseded")
	}
	if _, exists := parsed["spec"]; !exists {
		t.Error("parsed missing 'spec' field")
	}
	if supersededBy, _ := parsed["superseded_by"].(string); supersededBy != "99_replacement" {
		t.Errorf("parsed.superseded_by = %q; want %q", supersededBy, "99_replacement")
	}

	// Verify prd.md contains 'status: superseded' and deprecation banner.
	prdPath := filepath.Join(specDir, "30_sealed_spec", "prd.md")
	prdData, err := os.ReadFile(prdPath)
	if err != nil {
		t.Fatalf("cannot read prd.md after supersede: %v", err)
	}
	prdStr := string(prdData)
	if !strings.Contains(prdStr, `status: "superseded"`) {
		t.Errorf("prd.md does not contain 'status: \"superseded\"'; content:\n%s", prdStr)
	}
	if !strings.Contains(prdStr, "99_replacement") {
		t.Errorf("prd.md does not contain '99_replacement' deprecation banner; content:\n%s", prdStr)
	}
}

// TestSupersede_NumericResolution verifies that the supersede command
// resolves a spec by numeric prefix.
// Covers: NS-REQ-5, TS-NS-5
func TestSupersede_NumericResolution(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	createSealedSpecForCLI(t, specDir, "30_sealed_spec")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{
		"--spec-dir", specDir,
		"supersede", "30",
		"--by", "99_replacement",
	})

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

// TestSupersede_MissingByFlag verifies that spec supersede without --by
// fails with a usage error.
// Covers: NS-REQ-4, TS-NS-4
func TestSupersede_MissingByFlag(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	createSealedSpecForCLI(t, specDir, "30_sealed_spec")

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "supersede", "30_sealed_spec"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() returned nil error for supersede without --by; want error")
	}
}

// TestSupersede_ActiveSpec verifies that trying to supersede an active
// (non-sealed) spec fails with a LifecycleError.
// Covers: NS-REQ-4, TS-NS-4
func TestSupersede_ActiveSpec(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	createActiveSpecForCLI(t, specDir, "30_active_spec")

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{
		"--spec-dir", specDir,
		"supersede", "30_active_spec",
		"--by", "99_replacement",
	})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() returned nil error for superseding an active spec; want LifecycleError")
	}
}

// TestSupersede_NonexistentSpec verifies that superseding a non-existent
// spec returns an error.
// Covers: NS-REQ-4
func TestSupersede_NonexistentSpec(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{
		"--spec-dir", specDir,
		"supersede", "nonexistent",
		"--by", "99_replacement",
	})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() returned nil error for non-existent spec; want error")
	}
}

// TestSupersede_MissingArg verifies that spec supersede requires a
// positional argument.
// Covers: NS-REQ-5
func TestSupersede_MissingArg(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"supersede"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() with no spec argument returned nil; want error")
	}
}

// TestSupersede_BannerSuppressed verifies that the banner is suppressed
// for the supersede subcommand.
// Covers: NS-REQ-5
func TestSupersede_BannerSuppressed(t *testing.T) {
	if shouldShowBanner(false, "supersede", nil) {
		t.Error("shouldShowBanner(quiet=false, subcmd='supersede') = true; want false")
	}
}
