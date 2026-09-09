package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// --- TS-NS-2: spec archive moves the spec directory to archive/ ---

// TestArchive_Success verifies that archiving a spec moves the directory
// to <spec-dir>/archive/<name>/ and emits {"ok": true, "archived": "<name>"}.
// Covers: NS-REQ-2, TS-NS-2
func TestArchive_Success(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a sealed spec (active specs must be sealed before archiving).
	createSealedSpecForCLI(t, specDir, "30_archive_me")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "archive", "30_archive_me"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	// Verify ok=true and archived field.
	if ok, _ := parsed["ok"].(bool); !ok {
		t.Errorf("parsed.ok = %v; want true", parsed["ok"])
	}
	if _, exists := parsed["archived"]; !exists {
		t.Error("parsed missing 'archived' field")
	}

	// Verify the original directory no longer exists.
	originalPath := filepath.Join(specDir, "30_archive_me")
	if _, statErr := os.Stat(originalPath); !os.IsNotExist(statErr) {
		t.Error("original spec directory still exists after archive; want it removed")
	}

	// Verify the spec moved to the archive directory.
	archivePath := filepath.Join(specDir, "archive", "30_archive_me")
	if _, statErr := os.Stat(archivePath); os.IsNotExist(statErr) {
		t.Errorf("archive directory %s does not exist after archive", archivePath)
	}
}

// TestArchive_NumericResolution verifies that the archive command resolves
// a spec by numeric prefix.
// Covers: NS-REQ-5, TS-NS-5
func TestArchive_NumericResolution(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	createSealedSpecForCLI(t, specDir, "42_numeric_spec")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "archive", "42"})

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

// TestArchive_NonexistentSpec verifies that archiving a non-existent spec
// returns an error.
// Covers: NS-REQ-4
func TestArchive_NonexistentSpec(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "archive", "nonexistent"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() returned nil error for non-existent spec; want error")
	}
}

// TestArchive_MissingArg verifies that spec archive requires a positional argument.
// Covers: NS-REQ-5
func TestArchive_MissingArg(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"archive"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() with no spec argument returned nil; want error")
	}
}

// TestArchive_BannerSuppressed verifies that the banner is suppressed for
// the archive subcommand.
// Covers: NS-REQ-5
func TestArchive_BannerSuppressed(t *testing.T) {
	if shouldShowBanner(false, "archive", nil) {
		t.Error("shouldShowBanner(quiet=false, subcmd='archive') = true; want false")
	}
}
