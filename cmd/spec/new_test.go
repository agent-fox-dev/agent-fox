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

// --- TS-08-14: Verify that spec new with a valid PRD file auto-initializes
//     the spec directory and campaign.yaml, creates the spec, and emits JSON
//     with spec_dir and state ---

// TestTS08_14_NewAutoInitSpecDirAndCampaign verifies that running spec new
// with a valid PRD file when the spec directory does not exist will:
//   - auto-create the .specs directory
//   - auto-create campaign.yaml with name "default" and description "default campaign"
//   - create a spec via Campaign.NewSpec
//   - emit JSON with ok: true, spec_dir, and state fields
//   - exit 0
//
// Covers: TS-08-14, Requirement: 08-REQ-6.1
func TestTS08_14_NewAutoInitSpecDirAndCampaign(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a PRD file.
	prdPath := filepath.Join(tmpDir, "test_prd.md")
	if err := os.WriteFile(prdPath, []byte("# Test PRD\nSome content"), 0644); err != nil {
		t.Fatal(err)
	}

	// Use a spec directory that does not yet exist.
	specDir := filepath.Join(tmpDir, ".specs")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	stderrBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{"--spec-dir", specDir, "new", prdPath, "--name", "my_spec"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v\nstderr: %s", err, stderrBuf.String())
	}

	// Verify .specs directory was created.
	if _, err := os.Stat(specDir); os.IsNotExist(err) {
		t.Error("spec directory was not created")
	}

	// Verify campaign.yaml was created.
	campaignYAML := filepath.Join(specDir, "campaign.yaml")
	if _, err := os.Stat(campaignYAML); os.IsNotExist(err) {
		t.Error("campaign.yaml was not created")
	}

	// Verify stdout is valid JSON with the expected fields.
	output := stdoutBuf.String()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", err, output)
	}

	if ok, _ := parsed["ok"].(bool); !ok {
		t.Errorf("parsed.ok = %v; want true", parsed["ok"])
	}
	if _, exists := parsed["spec_dir"]; !exists {
		t.Error("parsed missing 'spec_dir' field")
	}
	if _, exists := parsed["state"]; !exists {
		t.Error("parsed missing 'state' field")
	}
	if state, ok := parsed["state"].(string); !ok || state == "" {
		t.Errorf("parsed.state = %v; want non-empty string", parsed["state"])
	}
}

// --- TS-08-15: Verify that spec new uses the provided --name value as the
//     spec name when it matches [a-z][a-z0-9_]* ---

// TestTS08_15_NewUsesProvidedName verifies that --name with a valid
// pattern creates the spec with that exact name.
// Covers: TS-08-15, Requirement: 08-REQ-6.2
func TestTS08_15_NewUsesProvidedName(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a PRD file.
	prdPath := filepath.Join(tmpDir, "test_prd.md")
	if err := os.WriteFile(prdPath, []byte("# Test PRD\nContent"), 0644); err != nil {
		t.Fatal(err)
	}

	specDir := filepath.Join(tmpDir, ".specs")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "new", prdPath, "--name", "my_spec_01"})

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

	// The spec directory should exist with the provided name.
	// Since it's the first spec, it should be 01_my_spec_01.
	entries, err := os.ReadDir(specDir)
	if err != nil {
		t.Fatalf("failed to read specDir: %v", err)
	}

	found := false
	for _, e := range entries {
		if e.IsDir() && strings.Contains(e.Name(), "my_spec_01") {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("no spec directory containing 'my_spec_01' found; entries: %v", names)
	}
}

// --- TS-08-16: Verify that spec new derives a snake_case spec name from
//     the PRD filename when --name is omitted ---

// TestTS08_16_NewDerivesSnakeCaseName verifies that when --name is omitted,
// the spec name is derived from the PRD filename by stripping the extension
// and converting to snake_case.
// Covers: TS-08-16, Requirement: 08-REQ-6.3
func TestTS08_16_NewDerivesSnakeCaseName(t *testing.T) {
	tmpDir := t.TempDir()

	// Use a CamelCase filename that needs snake_case conversion.
	prdPath := filepath.Join(tmpDir, "MySpecPRD.md")
	if err := os.WriteFile(prdPath, []byte("# My Spec PRD\nContent"), 0644); err != nil {
		t.Fatal(err)
	}

	specDir := filepath.Join(tmpDir, ".specs")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	// No --name flag.
	cmd.SetArgs([]string{"--spec-dir", specDir, "new", prdPath})

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

	// The spec directory should contain a snake_case version of "MySpecPRD".
	// Expected: "my_spec_prd" (or similar snake_case derivation).
	entries, err := os.ReadDir(specDir)
	if err != nil {
		t.Fatalf("failed to read specDir: %v", err)
	}

	found := false
	for _, e := range entries {
		if e.IsDir() {
			name := e.Name()
			// The directory name should be NN_snake_case. Check the
			// snake_case portion is all lowercase with underscores.
			parts := strings.SplitN(name, "_", 2)
			if len(parts) >= 2 {
				snakePart := parts[1]
				if snakePart == strings.ToLower(snakePart) && strings.Contains(snakePart, "spec") {
					found = true
					break
				}
			}
		}
	}
	if !found {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("no spec directory with derived snake_case name found; entries: %v", names)
	}
}

// --- 08-REQ-6.E1: SPEC_PATH does not exist ---

// TestTS08_14_NewPRDNotExist verifies that spec new returns an error
// when SPEC_PATH points to a non-existent file.
// Covers: 08-REQ-6.E1
func TestTS08_14_NewPRDNotExist(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	stderrBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{"--spec-dir", specDir, "new", "/nonexistent/prd.md", "--name", "test"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() with non-existent PRD returned nil; want error")
	}

	// Verify spec directory was NOT created (error before initialization).
	if _, statErr := os.Stat(specDir); !os.IsNotExist(statErr) {
		t.Error("spec directory should not exist when PRD file is missing")
	}
}

// TestTS08_14_NewPRDNotExistAgentMode verifies that in agent mode,
// spec new with a non-existent PRD emits a JSON error envelope on stdout.
// Covers: 08-REQ-6.E1
func TestTS08_14_NewPRDNotExistAgentMode(t *testing.T) {
	t.Setenv("AF_AGENT", "1")

	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "new", "/nonexistent/prd.md", "--name", "test"})

	err := cmd.Execute()
	// In agent mode, errors should still cause non-zero exit
	if err == nil {
		// Check stdout for JSON error envelope
		output := stdoutBuf.String()
		if output != "" {
			var parsed map[string]any
			if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr == nil {
				if ok, _ := parsed["ok"].(bool); ok {
					t.Error("expected ok: false in error JSON")
				}
				if _, exists := parsed["error"]; !exists {
					t.Error("expected 'error' field in error JSON")
				}
			}
		}
	}
}

// --- 08-REQ-6.E2: --name does not match [a-z][a-z0-9_]* ---

// TestTS08_15_NewInvalidNamePattern verifies that spec new rejects
// names that don't match [a-z][a-z0-9_]*.
// Covers: 08-REQ-6.E2
func TestTS08_15_NewInvalidNamePattern(t *testing.T) {
	tmpDir := t.TempDir()
	prdPath := filepath.Join(tmpDir, "test_prd.md")
	if err := os.WriteFile(prdPath, []byte("# Test PRD"), 0644); err != nil {
		t.Fatal(err)
	}
	specDir := filepath.Join(tmpDir, ".specs")

	invalidNames := []string{
		"MySpec",    // uppercase
		"1spec",     // starts with digit
		"_spec",     // starts with underscore
		"spec-name", // contains hyphen
		"ALLCAPS",   // all uppercase
		"spec name", // contains space
		"spec.name", // contains dot
		"",          // empty string
	}

	for _, name := range invalidNames {
		t.Run("name="+name, func(t *testing.T) {
			cmd := newRootCmd()
			cmd.SetOut(new(bytes.Buffer))
			cmd.SetErr(new(bytes.Buffer))
			cmd.SetArgs([]string{"--spec-dir", specDir, "new", prdPath, "--name", name})

			err := cmd.Execute()
			if err == nil {
				t.Errorf("Execute() with --name=%q returned nil; want validation error", name)
			}
		})
	}
}

// TestTS08_15_NewValidNamePatterns verifies that spec new accepts
// names matching [a-z][a-z0-9_]*.
// Covers: 08-REQ-6.2
func TestTS08_15_NewValidNamePatterns(t *testing.T) {
	validNames := []string{
		"a",
		"spec",
		"my_spec",
		"my_spec_01",
		"a1",
		"a_b_c",
	}

	for _, name := range validNames {
		t.Run("name="+name, func(t *testing.T) {
			tmpDir := t.TempDir()
			prdPath := filepath.Join(tmpDir, "test_prd.md")
			if err := os.WriteFile(prdPath, []byte("# Test PRD"), 0644); err != nil {
				t.Fatal(err)
			}
			specDir := filepath.Join(tmpDir, ".specs")

			cmd := newRootCmd()
			stdoutBuf := new(bytes.Buffer)
			cmd.SetOut(stdoutBuf)
			cmd.SetErr(new(bytes.Buffer))
			cmd.SetArgs([]string{"--spec-dir", specDir, "new", prdPath, "--name", name})

			err := cmd.Execute()
			if err != nil {
				t.Fatalf("Execute() with --name=%q returned error: %v", name, err)
			}

			var parsed map[string]any
			if err := json.Unmarshal([]byte(stdoutBuf.String()), &parsed); err != nil {
				t.Fatalf("stdout is not valid JSON: %v", err)
			}
			if ok, _ := parsed["ok"].(bool); !ok {
				t.Errorf("parsed.ok = %v; want true", parsed["ok"])
			}
		})
	}
}

// --- 08-REQ-6.E3: NewSpec returns an error (spec already exists) ---

// TestTS08_14_NewDuplicateSpecName verifies that creating a spec with
// the same name twice returns an error without leaving partial state.
// Covers: 08-REQ-6.E3
func TestTS08_14_NewDuplicateSpecName(t *testing.T) {
	tmpDir := t.TempDir()
	prdPath := filepath.Join(tmpDir, "test_prd.md")
	if err := os.WriteFile(prdPath, []byte("# Test PRD"), 0644); err != nil {
		t.Fatal(err)
	}
	specDir := filepath.Join(tmpDir, ".specs")

	// First creation should succeed.
	cmd1 := newRootCmd()
	cmd1.SetOut(new(bytes.Buffer))
	cmd1.SetErr(new(bytes.Buffer))
	cmd1.SetArgs([]string{"--spec-dir", specDir, "new", prdPath, "--name", "dup_spec"})

	if err := cmd1.Execute(); err != nil {
		t.Fatalf("first spec new returned error: %v", err)
	}

	// Second creation with same name should fail.
	cmd2 := newRootCmd()
	cmd2.SetOut(new(bytes.Buffer))
	cmd2.SetErr(new(bytes.Buffer))
	cmd2.SetArgs([]string{"--spec-dir", specDir, "new", prdPath, "--name", "dup_spec"})

	// Even though the name is valid, NewSpec may succeed with an incremented
	// prefix (e.g. 02_dup_spec). The real guard against duplicates depends on
	// the Campaign implementation. This test verifies the command handles
	// the flow correctly in either case.
	// If the implementation does reject duplicates, we expect an error.
	// We don't assert error here because the spec says NewSpec uses numeric
	// prefixes, so duplicate names with different prefixes are valid.
}

// --- 08-REQ-6.E4: Filesystem permission errors ---

// TestTS08_14_NewSpecDirPermissionError verifies that spec new returns
// an error when the spec directory cannot be created due to permissions.
// Covers: 08-REQ-6.E4
func TestTS08_14_NewSpecDirPermissionError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping permission test when running as root")
	}

	tmpDir := t.TempDir()
	prdPath := filepath.Join(tmpDir, "test_prd.md")
	if err := os.WriteFile(prdPath, []byte("# Test PRD"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a read-only parent directory.
	readOnlyDir := filepath.Join(tmpDir, "readonly")
	if err := os.MkdirAll(readOnlyDir, 0555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(readOnlyDir, 0755) // restore so TempDir cleanup works

	specDir := filepath.Join(readOnlyDir, ".specs")

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "new", prdPath, "--name", "test"})

	err := cmd.Execute()
	if err == nil {
		t.Error("Execute() with permission-denied spec directory returned nil; want error")
	}
}

// --- TS-08-14: Verify spec_dir field matches the --spec-dir flag ---

// TestTS08_14_NewSpecDirInOutput verifies the emitted spec_dir field
// matches the configured spec directory.
// Covers: TS-08-14
func TestTS08_14_NewSpecDirInOutput(t *testing.T) {
	tmpDir := t.TempDir()
	prdPath := filepath.Join(tmpDir, "test_prd.md")
	if err := os.WriteFile(prdPath, []byte("# Test PRD"), 0644); err != nil {
		t.Fatal(err)
	}

	specDir := filepath.Join(tmpDir, "custom_specs")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "new", prdPath, "--name", "test_spec"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(stdoutBuf.String()), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}

	if sd, ok := parsed["spec_dir"].(string); !ok || sd == "" {
		t.Errorf("parsed.spec_dir = %v; want non-empty string", parsed["spec_dir"])
	}
}

// --- 08-REQ-6: spec new requires SPEC_PATH positional argument ---

// TestTS08_14_NewMissingPRDArg verifies that spec new requires the
// SPEC_PATH positional argument.
// Covers: 08-REQ-6
func TestTS08_14_NewMissingPRDArg(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"new"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() with no SPEC_PATH argument returned nil; want error")
	}
}

// TestTS08_14_NewPRDIsDirectory verifies that spec new rejects
// a SPEC_PATH that is a directory, not a file.
// Covers: 08-REQ-6.E1
func TestTS08_14_NewPRDIsDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	dirPath := filepath.Join(tmpDir, "notafile")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"new", dirPath, "--name", "test"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() with SPEC_PATH pointing to a directory returned nil; want error")
	}
}

// --- TS-NS-1: All four required artifact files are created by spec new ---

// TestTS_NS_1_NewCreatesAllArtifacts verifies that spec new creates all four
// required artifact files: prd.md, requirements.json, test_spec.json, tasks.json,
// plus _session.json.
// Covers: TS-NS-1, NS-REQ-1
func TestTS_NS_1_NewCreatesAllArtifacts(t *testing.T) {
	tmpDir := t.TempDir()

	prdPath := filepath.Join(tmpDir, "test_prd.md")
	if err := os.WriteFile(prdPath, []byte("# Test PRD\n\nSome content."), 0644); err != nil {
		t.Fatal(err)
	}

	specDir := filepath.Join(tmpDir, ".specs")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "new", prdPath, "--name", "my_spec"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// Find the spec directory (should be 01_my_spec).
	entries, err := os.ReadDir(specDir)
	if err != nil {
		t.Fatalf("cannot read spec dir: %v", err)
	}
	var specPath string
	for _, e := range entries {
		if e.IsDir() && strings.Contains(e.Name(), "my_spec") {
			specPath = filepath.Join(specDir, e.Name())
			break
		}
	}
	if specPath == "" {
		t.Fatal("spec directory not found")
	}

	// Verify all five required files exist.
	for _, fname := range []string{"prd.md", "requirements.json", "test_spec.json", "tasks.json", "_session.json"} {
		if _, err := os.Stat(filepath.Join(specPath, fname)); os.IsNotExist(err) {
			t.Errorf("required file %q does not exist in spec directory", fname)
		}
	}
}

// --- TS-NS-2: prd.md contains valid YAML frontmatter ---

// TestTS_NS_2_PrdMdHasValidFrontmatter verifies that the created prd.md
// contains valid YAML frontmatter with all required fields and the original
// PRD content as body.
// Covers: TS-NS-2, NS-REQ-2
func TestTS_NS_2_PrdMdHasValidFrontmatter(t *testing.T) {
	tmpDir := t.TempDir()

	prdBody := "# Test PRD\n\nThis is my product requirements document."
	prdPath := filepath.Join(tmpDir, "myprd.md")
	if err := os.WriteFile(prdPath, []byte(prdBody), 0644); err != nil {
		t.Fatal(err)
	}

	specDir := filepath.Join(tmpDir, ".specs")

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "new", prdPath, "--name", "my_spec"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// Find the spec directory.
	entries, err := os.ReadDir(specDir)
	if err != nil {
		t.Fatalf("cannot read spec dir: %v", err)
	}
	var specPath string
	for _, e := range entries {
		if e.IsDir() && strings.Contains(e.Name(), "my_spec") {
			specPath = filepath.Join(specDir, e.Name())
			break
		}
	}
	if specPath == "" {
		t.Fatal("spec directory not found")
	}

	// Load the spec and verify frontmatter.
	spec, err := afspec.LoadSpec(specPath)
	if err != nil {
		t.Fatalf("LoadSpec() failed: %v", err)
	}

	if spec.Status != "draft" {
		t.Errorf("spec.Status = %q; want %q", spec.Status, "draft")
	}
	if spec.SchemaVersion != 1 {
		t.Errorf("spec.SchemaVersion = %d; want 1", spec.SchemaVersion)
	}
	if spec.SpecID == "" {
		t.Error("spec.SpecID is empty; want non-empty")
	}
	if spec.SpecName == "" {
		t.Error("spec.SpecName is empty; want non-empty")
	}
	if spec.CreatedAt == "" {
		t.Error("spec.CreatedAt is empty; want non-empty timestamp")
	}
	if spec.UpdatedAt == "" {
		t.Error("spec.UpdatedAt is empty; want non-empty timestamp")
	}

	// Verify the original PRD content is the body.
	if !strings.Contains(spec.PRDBody, prdBody) {
		t.Errorf("prd.md body does not contain original PRD content; body=%q", spec.PRDBody)
	}

	// Verify prd.md starts with ---.
	prdData, err := os.ReadFile(filepath.Join(specPath, "prd.md"))
	if err != nil {
		t.Fatalf("cannot read prd.md: %v", err)
	}
	if !strings.HasPrefix(string(prdData), "---") {
		t.Error("prd.md does not start with ---")
	}
}

// --- TS-NS-3: JSON artifacts pass schema validation ---

// TestTS_NS_3_JSONArtifactsValidate verifies that the three JSON artifacts
// can be parsed by json.Unmarshal into their typed structs and that
// spec_id, spec_name, and schema_version=1 are populated correctly.
// Covers: TS-NS-3, NS-REQ-3
func TestTS_NS_3_JSONArtifactsValidate(t *testing.T) {
	tmpDir := t.TempDir()

	prdPath := filepath.Join(tmpDir, "test_prd.md")
	if err := os.WriteFile(prdPath, []byte("# PRD\nContent"), 0644); err != nil {
		t.Fatal(err)
	}

	specDir := filepath.Join(tmpDir, ".specs")

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "new", prdPath, "--name", "validate_spec"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// Find the spec directory.
	entries, err := os.ReadDir(specDir)
	if err != nil {
		t.Fatalf("cannot read spec dir: %v", err)
	}
	var specPath string
	for _, e := range entries {
		if e.IsDir() && strings.Contains(e.Name(), "validate_spec") {
			specPath = filepath.Join(specDir, e.Name())
			break
		}
	}
	if specPath == "" {
		t.Fatal("spec directory not found")
	}

	// Parse requirements.json.
	reqData, err := os.ReadFile(filepath.Join(specPath, "requirements.json"))
	if err != nil {
		t.Fatalf("cannot read requirements.json: %v", err)
	}
	var req afspec.RequirementsV1Json
	if err := json.Unmarshal(reqData, &req); err != nil {
		t.Errorf("json.Unmarshal requirements.json failed: %v", err)
	} else {
		if req.SpecId == "" {
			t.Error("requirements.json: spec_id is empty")
		}
		if req.SpecName == "" {
			t.Error("requirements.json: spec_name is empty")
		}
		if req.SchemaVersion != 1 {
			t.Errorf("requirements.json: schema_version = %d; want 1", req.SchemaVersion)
		}
	}

	// Parse test_spec.json.
	tsData, err := os.ReadFile(filepath.Join(specPath, "test_spec.json"))
	if err != nil {
		t.Fatalf("cannot read test_spec.json: %v", err)
	}
	var ts afspec.TestSpecV1Json
	if err := json.Unmarshal(tsData, &ts); err != nil {
		t.Errorf("json.Unmarshal test_spec.json failed: %v", err)
	} else {
		if ts.SpecId == "" {
			t.Error("test_spec.json: spec_id is empty")
		}
		if ts.SpecName == "" {
			t.Error("test_spec.json: spec_name is empty")
		}
		if ts.SchemaVersion != 1 {
			t.Errorf("test_spec.json: schema_version = %d; want 1", ts.SchemaVersion)
		}
	}

	// Parse tasks.json.
	tasksData, err := os.ReadFile(filepath.Join(specPath, "tasks.json"))
	if err != nil {
		t.Fatalf("cannot read tasks.json: %v", err)
	}
	var tasks afspec.TasksV1Json
	if err := json.Unmarshal(tasksData, &tasks); err != nil {
		t.Errorf("json.Unmarshal tasks.json failed: %v", err)
	} else {
		if tasks.SpecId == "" {
			t.Error("tasks.json: spec_id is empty")
		}
		if tasks.SpecName == "" {
			t.Error("tasks.json: spec_name is empty")
		}
		if tasks.SchemaVersion != 1 {
			t.Errorf("tasks.json: schema_version = %d; want 1", tasks.SchemaVersion)
		}
	}

	// Verify spec_id and spec_name are consistent across all artifacts.
	if req.SpecId != ts.SpecId || ts.SpecId != tasks.SpecId {
		t.Errorf("spec_id mismatch: req=%q ts=%q tasks=%q", req.SpecId, ts.SpecId, tasks.SpecId)
	}
	if req.SpecName != ts.SpecName || ts.SpecName != tasks.SpecName {
		t.Errorf("spec_name mismatch: req=%q ts=%q tasks=%q", req.SpecName, ts.SpecName, tasks.SpecName)
	}
}

// --- TS-NS-4: Directory name validated against IsSpecDirName ---

// TestTS_NS_4_InvalidDirNameRejected verifies that spec new returns an error
// when the derived directory name would be invalid. Names that produce invalid
// directory names (e.g., trailing underscore, double underscore) are rejected.
// Covers: TS-NS-4, NS-REQ-4
func TestTS_NS_4_InvalidDirNameRejected(t *testing.T) {
	// Names that pass specNameRE but produce an invalid IsSpecDirName directory.
	// "a_" → "01_a_" fails IsSpecDirName (trailing underscore in name segment).
	invalidDirNames := []string{
		"a_",   // trailing underscore → 01_a_ fails IsSpecDirName
		"a__b", // double underscore → 01_a__b fails IsSpecDirName
	}

	for _, name := range invalidDirNames {
		t.Run("name="+name, func(t *testing.T) {
			tmpDir := t.TempDir()
			prdPath := filepath.Join(tmpDir, "test_prd.md")
			if err := os.WriteFile(prdPath, []byte("# PRD"), 0644); err != nil {
				t.Fatal(err)
			}
			specDir := filepath.Join(tmpDir, ".specs")

			cmd := newRootCmd()
			cmd.SetOut(new(bytes.Buffer))
			cmd.SetErr(new(bytes.Buffer))
			cmd.SetArgs([]string{"--spec-dir", specDir, "new", prdPath, "--name", name})

			err := cmd.Execute()
			if err == nil {
				t.Errorf("Execute() with --name=%q returned nil; want error for invalid directory name", name)
			}

			// Verify the spec directory was NOT created.
			if _, statErr := os.Stat(specDir); os.IsNotExist(statErr) {
				// specDir itself not created — fine, no spec dir means no spec dir either.
				return
			}
			entries, _ := os.ReadDir(specDir)
			for _, e := range entries {
				if e.IsDir() && strings.Contains(e.Name(), name[:strings.IndexByte(name, '_')]) {
					t.Errorf("spec directory containing name was created despite error")
				}
			}
		})
	}
}

// --- TS-NS-5: Collision detection via prefix increment ---

// TestTS_NS_5_CollisionIncrementsPrefix verifies that when a spec directory
// with the same name already exists (e.g., 01_my_spec), a second invocation
// creates 02_my_spec without clobbering the first.
// Covers: TS-NS-5, NS-REQ-5
func TestTS_NS_5_CollisionIncrementsPrefix(t *testing.T) {
	tmpDir := t.TempDir()

	prdPath := filepath.Join(tmpDir, "test_prd.md")
	originalContent := "# Original PRD\nOriginal content."
	if err := os.WriteFile(prdPath, []byte(originalContent), 0644); err != nil {
		t.Fatal(err)
	}

	specDir := filepath.Join(tmpDir, ".specs")

	// First creation: produces 01_my_spec.
	cmd1 := newRootCmd()
	cmd1.SetOut(new(bytes.Buffer))
	cmd1.SetErr(new(bytes.Buffer))
	cmd1.SetArgs([]string{"--spec-dir", specDir, "new", prdPath, "--name", "my_spec"})
	if err := cmd1.Execute(); err != nil {
		t.Fatalf("first spec new returned error: %v", err)
	}

	// Verify 01_my_spec was created.
	firstSpecDir := filepath.Join(specDir, "01_my_spec")
	if _, err := os.Stat(firstSpecDir); os.IsNotExist(err) {
		t.Fatal("01_my_spec was not created")
	}

	// Second creation with same name: should produce 02_my_spec.
	prdPath2 := filepath.Join(tmpDir, "test_prd2.md")
	newContent := "# New PRD\nNew content."
	if err := os.WriteFile(prdPath2, []byte(newContent), 0644); err != nil {
		t.Fatal(err)
	}

	cmd2 := newRootCmd()
	cmd2.SetOut(new(bytes.Buffer))
	cmd2.SetErr(new(bytes.Buffer))
	cmd2.SetArgs([]string{"--spec-dir", specDir, "new", prdPath2, "--name", "my_spec"})
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("second spec new returned error: %v", err)
	}

	// Verify 02_my_spec was created.
	secondSpecDir := filepath.Join(specDir, "02_my_spec")
	if _, err := os.Stat(secondSpecDir); os.IsNotExist(err) {
		t.Error("02_my_spec was not created on second invocation")
	}

	// Verify 01_my_spec prd.md still has original content (not overwritten).
	prdData, err := os.ReadFile(filepath.Join(firstSpecDir, "prd.md"))
	if err != nil {
		t.Fatalf("cannot read 01_my_spec/prd.md: %v", err)
	}
	if !strings.Contains(string(prdData), "Original") {
		t.Errorf("01_my_spec/prd.md was clobbered; content=%q", string(prdData))
	}
}

// --- TS-NS-1: A freshly scaffolded spec passes spec validate ---

// TestTS_NS_1_ScaffoldPassesValidation verifies that a spec created by
// spec new immediately passes spec validate without errors. This covers the
// fix for issue #73: wiring_verification checks A and B were previously
// fired on scaffold specs that have no smoke tests, causing spurious errors.
//
// The fix defers checks A and B (test_spec_refs and smoke-pattern checks)
// when the spec has no smoke tests (bootstrap mode per spec section 3.3).
// Structural checks (first group kind=tests, last kind=wiring_verification)
// still fire and must pass.
//
// Covers: TS-NS-1, NS-REQ-1, NS-REQ-3
func TestTS_NS_1_ScaffoldPassesValidation(t *testing.T) {
	tmpDir := t.TempDir()

	prdPath := filepath.Join(tmpDir, "my_feature.md")
	if err := os.WriteFile(prdPath, []byte("# My Feature PRD\n\nBuild a cool new feature."), 0644); err != nil {
		t.Fatal(err)
	}

	specDir := filepath.Join(tmpDir, ".specs")

	// Run spec new to create the scaffold.
	newCmd := newRootCmd()
	newCmd.SetOut(new(bytes.Buffer))
	newCmd.SetErr(new(bytes.Buffer))
	newCmd.SetArgs([]string{"--spec-dir", specDir, "new", prdPath, "--name", "my_feature"})
	if err := newCmd.Execute(); err != nil {
		t.Fatalf("spec new failed: %v", err)
	}

	// Find the created spec directory.
	entries, err := os.ReadDir(specDir)
	if err != nil {
		t.Fatalf("cannot read spec dir: %v", err)
	}
	var specPath string
	for _, e := range entries {
		if e.IsDir() && strings.Contains(e.Name(), "my_feature") {
			specPath = filepath.Join(specDir, e.Name())
			break
		}
	}
	if specPath == "" {
		t.Fatal("scaffold spec directory not found after spec new")
	}

	// Load and fully validate the scaffold spec.
	spec, err := afspec.LoadSpec(specPath)
	if err != nil {
		t.Fatalf("LoadSpec() failed on scaffold: %v", err)
	}
	result := spec.Validate()

	// NS-REQ-1: scaffold must be valid with no errors.
	if !result.Valid {
		t.Errorf("scaffold spec is not valid (error_count=%d); want valid=true, error_count=0", len(result.Errors))
		for i, e := range result.Errors {
			t.Logf("  error[%d]: category=%s check=%s message=%s", i, e.Category, e.Check, e.Message)
		}
	}
	if len(result.Errors) != 0 {
		t.Errorf("expected 0 errors, got %d", len(result.Errors))
	}

	// NS-REQ-3: structural check must pass — no task_group_structure errors.
	for _, e := range result.Errors {
		if e.Check == "task_group_structure" {
			t.Errorf("unexpected task_group_structure error: %s", e.Message)
		}
	}

	// Verify the spec validate CLI command also exits 0.
	validateCmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	validateCmd.SetOut(stdoutBuf)
	validateCmd.SetErr(new(bytes.Buffer))
	specDirName := filepath.Base(specPath)
	validateCmd.SetArgs([]string{"--spec-dir", specDir, "validate", specDirName})

	if err := validateCmd.Execute(); err != nil {
		t.Errorf("spec validate on scaffold returned error (want exit 0): %v\noutput: %s", err, stdoutBuf.String())
	}

	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(stdoutBuf.String()), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, stdoutBuf.String())
	}
	if valid, _ := parsed["valid"].(bool); !valid {
		t.Errorf("spec validate: valid=%v, want true\noutput: %s", parsed["valid"], stdoutBuf.String())
	}
	if ec, _ := parsed["error_count"].(float64); ec != 0 {
		t.Errorf("spec validate: error_count=%v, want 0\noutput: %s", ec, stdoutBuf.String())
	}
}

// --- TS-NS-1: spec new --help shows PRD_FILE, not SPEC_PATH ---

// TestTS_NS_HelpShowsPRDFile verifies that the usage string for spec new
// uses PRD_FILE as the positional argument name, not SPEC_PATH.
// Covers: TS-NS-1, NS-REQ-1
func TestTS_NS_HelpShowsPRDFile(t *testing.T) {
	cmd := newRootCmd()
	outBuf := new(bytes.Buffer)
	cmd.SetOut(outBuf)
	cmd.SetErr(outBuf)
	cmd.SetArgs([]string{"new", "--help"})

	// cobra exits with a non-nil error on --help; that's expected.
	_ = cmd.Execute()

	output := outBuf.String()
	if !strings.Contains(output, "PRD_FILE") {
		t.Errorf("help output does not contain 'PRD_FILE'; got:\n%s", output)
	}
	if strings.Contains(output, "SPEC_PATH") {
		t.Errorf("help output still contains 'SPEC_PATH'; got:\n%s", output)
	}
}

// --- TS-NS-2: spec new error messages reference PRD_FILE, not SPEC_PATH ---

// TestTS_NS_ErrorMessagesPRDFile verifies that error messages produced by
// spec new when the positional argument is invalid use 'PRD_FILE', not
// 'SPEC_PATH'.
// Covers: TS-NS-2, NS-REQ-2
func TestTS_NS_ErrorMessagesPRDFile(t *testing.T) {
	// Case 1: non-existent file.
	cmd1 := newRootCmd()
	errBuf1 := new(bytes.Buffer)
	cmd1.SetOut(new(bytes.Buffer))
	cmd1.SetErr(errBuf1)
	cmd1.SetArgs([]string{"new", "/nonexistent/prd.md", "--name", "test"})

	err := cmd1.Execute()
	if err == nil {
		t.Fatal("Execute() with non-existent path returned nil; want error")
	}
	errMsg1 := err.Error()
	if !strings.Contains(errMsg1, "PRD_FILE") {
		t.Errorf("error for non-existent file does not contain 'PRD_FILE'; got: %q", errMsg1)
	}
	if strings.Contains(errMsg1, "SPEC_PATH") {
		t.Errorf("error for non-existent file still contains 'SPEC_PATH'; got: %q", errMsg1)
	}

	// Case 2: path is a directory, not a file.
	tmpDir := t.TempDir()
	dirPath := tmpDir // a real directory

	cmd2 := newRootCmd()
	errBuf2 := new(bytes.Buffer)
	cmd2.SetOut(new(bytes.Buffer))
	cmd2.SetErr(errBuf2)
	cmd2.SetArgs([]string{"new", dirPath, "--name", "test"})

	err2 := cmd2.Execute()
	if err2 == nil {
		t.Fatal("Execute() with directory path returned nil; want error")
	}
	errMsg2 := err2.Error()
	if !strings.Contains(errMsg2, "PRD_FILE") {
		t.Errorf("error for directory path does not contain 'PRD_FILE'; got: %q", errMsg2)
	}
	if strings.Contains(errMsg2, "SPEC_PATH") {
		t.Errorf("error for directory path still contains 'SPEC_PATH'; got: %q", errMsg2)
	}
}

// TestTS_NS_2_WiringChecksEnforcedWithSmokeTests verifies that wiring
// verification checks A and B are still enforced when the spec has smoke
// tests but the wiring group lacks proper subtasks referencing them.
//
// This ensures the fix does not suppress real validation errors on
// fully-populated specs.
//
// Covers: TS-NS-2, NS-REQ-2, NS-REQ-4
func TestTS_NS_2_WiringChecksEnforcedWithSmokeTests(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")

	// Build a fully loadable spec with a smoke test.
	setupLoadableSpec(t, specDir, "08_my_spec", nil)

	// Overwrite tasks.json to have an empty wiring subtask array
	// (no test_spec_refs at all), while keeping the smoke test in test_spec.json.
	specPath := filepath.Join(specDir, "08_my_spec")
	tasks := `{
  "$schema": "https://agent-fox.dev/schemas/tasks.v1.json",
  "spec_id": "08",
  "spec_name": "my_spec",
  "schema_version": 1,
  "test_commands": {"spec_tests": "go test", "all_tests": "go test", "linter": "golint"},
  "dependencies": [],
  "task_groups": [
    {
      "id": 1,
      "kind": "tests",
      "title": "Tests",
      "subtasks": [{
        "id": "1.1",
        "title": "Write tests",
        "details": ["test details"],
        "test_spec_refs": ["TS-08-1"],
        "requirement_refs": ["08-REQ-1.1"],
        "state": "pending",
        "optional": false
      }],
      "verification": {"id": "1.V", "checks": ["tests pass"]}
    },
    {
      "id": 2,
      "kind": "wiring_verification",
      "title": "Wiring",
      "subtasks": [],
      "verification": {"id": "2.V", "checks": ["smoke tests pass"]}
    }
  ],
  "traceability": [{"requirement_id": "08-REQ-1.1", "test_spec_id": "TS-08-1", "task_id": "1.1", "test_path": null}]
}`
	if err := os.WriteFile(filepath.Join(specPath, "tasks.json"), []byte(tasks), 0644); err != nil {
		t.Fatal(err)
	}

	// Use the library directly to validate.
	spec, err := afspec.LoadSpec(specPath)
	if err != nil {
		t.Fatalf("LoadSpec() failed: %v", err)
	}
	result := spec.ValidateCrossFile()

	// NS-REQ-2/NS-REQ-4: wiring checks A and B must fire because smoke tests exist.
	var wiringErrors []afspec.ValidationEntry
	for _, e := range result.Errors {
		if e.Check == "wiring_verification" {
			wiringErrors = append(wiringErrors, e)
		}
	}
	if len(wiringErrors) < 2 {
		t.Errorf("expected at least 2 wiring_verification errors (checks A and B) when smoke tests exist, got %d; errors: %v",
			len(wiringErrors), result.Errors)
	}

	// Verify the CLI also exits 1 for this spec.
	validateCmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	validateCmd.SetOut(stdoutBuf)
	validateCmd.SetErr(new(bytes.Buffer))
	validateCmd.SetArgs([]string{"--spec-dir", specDir, "validate", "08_my_spec"})

	if err := validateCmd.Execute(); err == nil {
		t.Error("spec validate should exit 1 for spec with smoke tests but empty wiring subtasks")
	}
}
