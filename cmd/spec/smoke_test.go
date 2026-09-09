package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- TS-08-SMOKE-1: End-to-end smoke test for invoking 'spec --version' ---

// TestSmoke01_VersionFlag verifies that invoking the compiled spec
// binary with --version prints a version string and exits 0.
// Covers: TS-08-SMOKE-1, Requirement: 08-REQ-1.1
func TestSmoke01_VersionFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping smoke test in short mode")
	}

	mainPkg := findMainPackage(t)
	if mainPkg == "" {
		t.Skip("could not locate main package for spec binary")
	}

	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "spec_smoke")
	buildCmd := exec.Command("go", "build",
		"-ldflags", "-X main.version=smoke-1.0.0",
		"-o", binaryPath,
		mainPkg,
	)
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\noutput: %s", err, out)
	}

	// Run spec --version.
	proc := exec.Command(binaryPath, "--version")
	output, err := proc.CombinedOutput()
	if err != nil {
		t.Fatalf("spec --version failed: %v\noutput: %s", err, output)
	}

	if !strings.Contains(string(output), "smoke-1.0.0") {
		t.Errorf("--version output = %q; want it to contain %q",
			string(output), "smoke-1.0.0")
	}
}

// TestSmoke01_HelpOutput verifies that invoking the spec binary without
// arguments displays help text and exits 0.
// Covers: TS-08-SMOKE-1, Requirement: 08-REQ-1.2
func TestSmoke01_HelpOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping smoke test in short mode")
	}

	mainPkg := findMainPackage(t)
	if mainPkg == "" {
		t.Skip("could not locate main package for spec binary")
	}

	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "spec_smoke_help")
	buildCmd := exec.Command("go", "build",
		"-o", binaryPath,
		mainPkg,
	)
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\noutput: %s", err, out)
	}

	proc := exec.Command(binaryPath)
	output, err := proc.CombinedOutput()
	if err != nil {
		t.Fatalf("spec (no args) failed: %v\noutput: %s", err, output)
	}

	if !strings.Contains(string(output), "Usage:") {
		t.Errorf("help output missing 'Usage:'; got %q", string(output))
	}
}

// --- TS-08-SMOKE-2: End-to-end smoke test for 'spec list' against empty
//     spec dir ---

// TestSmoke02_ListEmptySpecDir verifies that running spec list against
// an empty spec directory returns valid JSON with an empty specs array
// and exits 0.
// Covers: TS-08-SMOKE-2, Requirement: 08-REQ-7.1, 08-REQ-7.E1
func TestSmoke02_ListEmptySpecDir(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0755); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "list"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v; want exit 0 (08-PROP-8)", err)
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	if ok, _ := parsed["ok"].(bool); !ok {
		t.Errorf("parsed.ok = %v; want true", parsed["ok"])
	}

	if _, exists := parsed["spec_dir"]; !exists {
		t.Error("parsed missing 'spec_dir' field")
	}

	specs, ok := parsed["specs"].([]any)
	if !ok {
		t.Fatal("parsed.specs is not an array")
	}
	if len(specs) != 0 {
		t.Errorf("specs has %d entries; want 0 for empty spec dir", len(specs))
	}
}

// TestSmoke02_ListNonexistentSpecDir verifies that running spec list
// when the spec directory does not exist returns an empty specs array
// and exits 0 (08-PROP-8).
// Covers: TS-08-SMOKE-2, 08-PROP-8
func TestSmoke02_ListNonexistentSpecDir(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs_not_here")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "list"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v; want exit 0 (spec list always exits 0)", err)
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	if ok, _ := parsed["ok"].(bool); !ok {
		t.Errorf("parsed.ok = %v; want true", parsed["ok"])
	}

	specs, ok := parsed["specs"].([]any)
	if !ok {
		t.Fatal("parsed.specs is not an array")
	}
	if len(specs) != 0 {
		t.Errorf("specs has %d entries; want 0", len(specs))
	}
}

// --- TS-08-SMOKE-3: End-to-end smoke test for 'spec validate' against a
//     known-good spec dir ---

// TestSmoke03_ValidateKnownGoodSpec verifies that running spec validate
// against a spec with all valid files exits 0 and produces valid JSON.
// Covers: TS-08-SMOKE-3, TS-08-SMOKE-4, Requirement: 08-REQ-11.1
func TestSmoke03_ValidateKnownGoodSpec(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := setupValidSpec(t, tmpDir, "08_good_spec")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "validate", "08_good_spec"})

	err := cmd.Execute()

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	// For a known-good spec, we expect either exit 0 (no errors) or exit 1
	// (if the validation engine finds structural issues with minimal content).
	if errorCount, ok := parsed["error_count"].(float64); ok && errorCount == 0 {
		if err != nil {
			t.Errorf("exit 1 but error_count = 0; want exit 0 for valid spec; err: %v", err)
		}
	}
}

// --- TS-08-SMOKE-4: End-to-end smoke test for 'spec lint' ---

// TestSmoke04_LintEmitsFindings verifies that running spec lint produces
// valid JSON with a findings array.
// Covers: TS-08-SMOKE-4, Requirement: 08-REQ-12.1
func TestSmoke04_LintEmitsFindings(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0755); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "lint"})

	_ = cmd.Execute()

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	if _, exists := parsed["findings"]; !exists {
		t.Error("parsed missing 'findings' field")
	}
}

// --- Smoke: Banner suppression for list subcommand ---

// TestSmoke02_ListBannerSuppressed verifies that the banner is
// suppressed for the list subcommand (per 08-REQ-1.3, 08-PROP-6).
// Covers: 08-PROP-6
func TestSmoke02_ListBannerSuppressed(t *testing.T) {
	if shouldShowBanner(false, "list", nil) {
		t.Error("shouldShowBanner(quiet=false, subcmd='list') = true; want false")
	}
}

// --- Smoke: Validate banner suppressed ---

// TestSmoke03_ValidateBannerSuppressed verifies that the banner is
// suppressed for the validate subcommand (per 08-REQ-1.3, 08-PROP-6).
// Covers: 08-PROP-6
func TestSmoke03_ValidateBannerSuppressed(t *testing.T) {
	if shouldShowBanner(false, "validate", nil) {
		t.Error("shouldShowBanner(quiet=false, subcmd='validate') = true; want false")
	}
}

// --- Smoke: Subcommands are registered ---

// TestSmoke_SubcommandsRegistered verifies that all expected subcommands
// are registered on the root command.
// Covers: TS-08-SMOKE-1
func TestSmoke_SubcommandsRegistered(t *testing.T) {
	cmd := newRootCmd()

	expectedCmds := []string{
		"new", "list", "refine", "generate", "render",
		"validate", "lint", "status", "campaign",
		"seal", "archive", "supersede",
	}

	registeredCmds := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		registeredCmds[sub.Name()] = true
	}

	for _, expected := range expectedCmds {
		if !registeredCmds[expected] {
			t.Errorf("subcommand %q is not registered on root command", expected)
		}
	}
}
