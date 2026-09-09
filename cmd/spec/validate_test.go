package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupValidSpec creates a spec root holding one complete, valid format v2
// spec directory named specName, and returns the spec root path.
func setupValidSpec(t *testing.T, tmpDir, specName string) string {
	t.Helper()
	specRoot := filepath.Join(tmpDir, ".specs")
	specID, suffix := splitSpecDirName(t, specName)
	writeSpecFixture(t, filepath.Join(specRoot, specName), specID, suffix, specFixture{})
	return specRoot
}

// loadableSpecOpts tunes the spec setupLoadableSpec writes: extra glossary
// entries, cross-spec dependencies, and the actors of its execution path
// (which the cross-spec actor rule of §10.4 compares between specs).
type loadableSpecOpts struct {
	glossary     map[string]string
	dependencies []string
	pathActors   []string
}

// setupLoadableSpec writes a complete, loadable v2 spec into
// specRoot/specName. specName must be a {NN}_{snake_case_name} directory
// name; its two halves become the spec's identity, so that rule C1 holds.
func setupLoadableSpec(t *testing.T, specRoot, specName string, opts *loadableSpecOpts) {
	t.Helper()
	specID, suffix := splitSpecDirName(t, specName)

	fixture := specFixture{}
	if opts != nil {
		fixture.Glossary = opts.glossary
		fixture.Dependencies = opts.dependencies
		fixture.PathActors = opts.pathActors
	}
	writeSpecFixture(t, filepath.Join(specRoot, specName), specID, suffix, fixture)
}

// splitSpecDirName splits "08_spec_a" into "08" and "spec_a".
func splitSpecDirName(t *testing.T, specName string) (specID, suffix string) {
	t.Helper()
	parts := strings.SplitN(specName, "_", 2)
	if len(parts) != 2 {
		t.Fatalf("spec directory name %q is not {NN}_{snake_case_name}", specName)
	}
	return parts[0], parts[1]
}

// --- TS-08-32: Verify that spec validate with a SPEC argument checks
//     required files, runs ValidateStructured, and emits ValidationResult
//     JSON; exits 1 on errors ---

// TestTS08_32_ValidateSingleSpecAllPresent verifies that running
// spec validate with a SPEC argument checks that all required files
// (prd.md, requirements.json, test_spec.json, tasks.json) exist,
// verifies JSON readability, and emits the ValidationResult as JSON.
// NS-REQ-2: 'ok' key only present when valid=true.
// Covers: TS-08-32, TS-NS-2, Requirement: 08-REQ-11.1, NS-REQ-2
func TestTS08_32_ValidateSingleSpecAllPresent(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	setupLoadableSpec(t, specDir, "08_my_spec", nil)

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "validate", "08_my_spec"})

	err := cmd.Execute()

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	// The result should contain validation result fields.
	// Exit code depends on whether there are errors.
	valid, _ := parsed["valid"].(bool)
	if errorCount, ok := parsed["error_count"].(float64); ok && errorCount > 0 {
		if err == nil {
			t.Error("exit code should be 1 when error_count > 0")
		}
		// NS-REQ-2: 'ok' key should be absent when invalid.
		if _, exists := parsed["ok"]; exists {
			t.Error("output has 'ok' key when valid=false (NS-REQ-2)")
		}
	} else {
		if err != nil {
			t.Fatalf("Execute() returned error: %v; want exit 0 for valid spec", err)
		}
		// NS-REQ-2: 'ok' key should be true when valid.
		if valid {
			if okVal, exists := parsed["ok"]; !exists {
				t.Error("output missing 'ok' key when valid=true (NS-REQ-2)")
			} else if okBool, isBool := okVal.(bool); !isBool || !okBool {
				t.Errorf("expected ok=true, got %v (NS-REQ-2)", okVal)
			}
		}
	}
}

// TestTS08_32_ValidateSingleSpecExitCodeReflectsErrors verifies the
// correctness property 08-PROP-4: exit code is 1 if and only if the
// ValidationResult contains at least one error.
// Covers: TS-08-32, 08-PROP-4, Requirement: 08-REQ-11.1
func TestTS08_32_ValidateSingleSpecExitCodeReflectsErrors(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	setupLoadableSpec(t, specDir, "08_my_spec", nil)

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "validate", "08_my_spec"})

	err := cmd.Execute()

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	errorCount, _ := parsed["error_count"].(float64)
	if errorCount > 0 && err == nil {
		t.Error("exit code 0 with error_count > 0; want exit 1 (08-PROP-4)")
	}
	if errorCount == 0 && err != nil {
		t.Errorf("exit code 1 with error_count 0; want exit 0 (08-PROP-4); err: %v", err)
	}
}

// --- TS-08-33: Verify that spec validate without a SPEC argument discovers
//     all specs, validates each, aggregates results, and exits 1 if any
//     spec has errors ---

// TestTS08_33_ValidateMultiSpecAggregated verifies that running spec
// validate without a SPEC argument discovers all specs in the spec
// directory, validates each, wraps per-spec results under a "specs" key,
// and exits 1 if any spec has errors.
// Covers: TS-08-33, TS-NS-1, Requirement: 08-REQ-11.2, NS-REQ-1
func TestTS08_33_ValidateMultiSpecAggregated(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")

	// Create a valid spec.
	spec1 := filepath.Join(specDir, "08_spec_a")
	if err := os.MkdirAll(spec1, 0755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"prd.md", "requirements.json", "test_spec.json", "tasks.json"} {
		content := "# PRD"
		if f != "prd.md" {
			content = `{"valid": true}`
		}
		if err := os.WriteFile(filepath.Join(spec1, f), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Create a spec missing tasks.json.
	spec2 := filepath.Join(specDir, "09_spec_b")
	if err := os.MkdirAll(spec2, 0755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"prd.md", "requirements.json", "test_spec.json"} {
		content := "# PRD"
		if f != "prd.md" {
			content = `{"valid": true}`
		}
		if err := os.WriteFile(filepath.Join(spec2, f), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// tasks.json intentionally omitted for 09_spec_b.

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "validate"})

	err := cmd.Execute()

	// Should exit 1 because 09_spec_b is missing tasks.json.
	if err == nil {
		t.Error("Execute() returned nil; want exit 1 because spec_b is missing tasks.json")
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	// NS-REQ-1: Multi-spec output must have "specs" key with per-spec results.
	specsMap, ok := parsed["specs"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'specs' key to be a map, got %T", parsed["specs"])
	}

	// Verify per-spec results are keyed by directory name.
	if _, exists := specsMap["08_spec_a"]; !exists {
		t.Error("specs map missing '08_spec_a' key (NS-REQ-1)")
	}
	if _, exists := specsMap["09_spec_b"]; !exists {
		t.Error("specs map missing '09_spec_b' key (NS-REQ-1)")
	}

	// NS-REQ-2: Since invalid, 'ok' key should be absent.
	if _, exists := parsed["ok"]; exists {
		t.Error("output has 'ok' key but should be absent when valid=false (NS-REQ-2)")
	}

	// Verify valid=false at top level.
	if valid, _ := parsed["valid"].(bool); valid {
		t.Error("expected valid=false because spec_b is missing tasks.json")
	}
}

// --- TS-08-34: Verify that spec validate --cross discovers specs, builds
//     dependency graph, runs ValidateCrossSpec, and emits merged
//     ValidationResult JSON ---

// TestTS08_34_ValidateCrossSpec verifies that running spec validate
// with --cross discovers all specs, builds a dependency graph via
// BuildDependencyGraph, runs ValidateCrossSpec, wraps results under
// a "specs" key, and includes "ok":true when valid.
// Covers: TS-08-34, TS-NS-1, TS-NS-2, Requirement: 08-REQ-11.3, NS-REQ-1, NS-REQ-2
func TestTS08_34_ValidateCrossSpec(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")

	// Create two valid loadable specs with all required files.
	setupLoadableSpec(t, specDir, "08_spec_a", nil)
	setupLoadableSpec(t, specDir, "09_spec_b", nil)

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "validate", "--cross"})

	err := cmd.Execute()

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	// Valid specs should produce exit 0, valid=true.
	if err != nil {
		t.Fatalf("Execute() returned error: %v; want exit 0 when no cross-spec errors\noutput: %s", err, output)
	}
	if valid, ok := parsed["valid"].(bool); !ok || !valid {
		t.Errorf("expected valid=true, got %v", parsed["valid"])
	}

	// NS-REQ-1: Multi-spec output must have "specs" key with per-spec results.
	specsMap, ok := parsed["specs"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'specs' key to be a map, got %T", parsed["specs"])
	}
	if _, exists := specsMap["08_spec_a"]; !exists {
		t.Error("specs map missing '08_spec_a' key (NS-REQ-1)")
	}
	if _, exists := specsMap["09_spec_b"]; !exists {
		t.Error("specs map missing '09_spec_b' key (NS-REQ-1)")
	}

	// NS-REQ-2: When valid=true, "ok" key must be present and true.
	okVal, okExists := parsed["ok"]
	if !okExists {
		t.Error("output missing 'ok' key, want 'ok':true when valid=true (NS-REQ-2)")
	} else if okBool, isBool := okVal.(bool); !isBool || !okBool {
		t.Errorf("expected ok=true, got %v (NS-REQ-2)", okVal)
	}
}

// TestTS_NS1_CrossSpecLibraryChecks verifies that runValidateCross calls
// afspec.ValidateCrossSpec and surfaces its errors under the _cross_spec key.
// The error case of §10.4 is the actor rule: a spec that declares a dependency
// must have an execution path step whose actor also appears upstream, or the
// two specs do not actually meet.
// Covers: TS-NS-1, NS-REQ-1
func TestTS_NS1_CrossSpecLibraryChecks(t *testing.T) {
	tmpDir := t.TempDir()
	specRoot := filepath.Join(tmpDir, ".specs")

	setupLoadableSpec(t, specRoot, "08_spec_a", nil)
	setupLoadableSpec(t, specRoot, "09_spec_b", &loadableSpecOpts{
		dependencies: []string{"08"},
		pathActors:   []string{"auditor", "ledger"},
	})

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specRoot, "validate", "--cross"})

	if err := cmd.Execute(); err == nil {
		t.Fatalf("Execute() returned nil; want exit 1 for a dependency with no shared actor\noutput: %s", stdoutBuf.String())
	}

	var parsed map[string]any
	if jsonErr := json.Unmarshal(stdoutBuf.Bytes(), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, stdoutBuf.String())
	}
	specsMap, ok := parsed["specs"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'specs' to be a map, got %T", parsed["specs"])
	}
	crossSpec, ok := specsMap["_cross_spec"].(map[string]any)
	if !ok {
		t.Fatalf("expected '_cross_spec' in the specs map, got %T", specsMap["_cross_spec"])
	}
	if ec, _ := crossSpec["error_count"].(float64); ec < 1 {
		t.Errorf("_cross_spec error_count = %v; want at least 1", crossSpec["error_count"])
	}

	errorsArr, _ := crossSpec["errors"].([]any)
	found := false
	for _, e := range errorsArr {
		em, _ := e.(map[string]any)
		msg, _ := em["message"].(string)
		if strings.Contains(msg, "actor") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no error mentioning the missing shared actor; errors: %v", errorsArr)
	}
}

// TestTS_NS1_GlossaryConflictIsAWarning covers the other half of §10.4: two
// specs that define the same term differently produce a warning, not a
// failure. Under v1 this was an error and blocked the whole run.
func TestTS_NS1_GlossaryConflictIsAWarning(t *testing.T) {
	tmpDir := t.TempDir()
	specRoot := filepath.Join(tmpDir, ".specs")

	setupLoadableSpec(t, specRoot, "08_spec_a", &loadableSpecOpts{
		glossary: map[string]string{"widget": "A small UI component"},
	})
	setupLoadableSpec(t, specRoot, "09_spec_b", &loadableSpecOpts{
		glossary: map[string]string{"widget": "A mechanical device"},
	})

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specRoot, "validate", "--cross"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() = %v; a glossary conflict must not fail the run\noutput: %s", err, stdoutBuf.String())
	}

	var parsed map[string]any
	if jsonErr := json.Unmarshal(stdoutBuf.Bytes(), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, stdoutBuf.String())
	}
	specsMap, _ := parsed["specs"].(map[string]any)
	crossSpec, ok := specsMap["_cross_spec"].(map[string]any)
	if !ok {
		t.Fatalf("expected '_cross_spec' in the specs map, got %T", specsMap["_cross_spec"])
	}
	if wc, _ := crossSpec["warning_count"].(float64); wc < 1 {
		t.Errorf("_cross_spec warning_count = %v; want at least 1", crossSpec["warning_count"])
	}
	if ec, _ := crossSpec["error_count"].(float64); ec != 0 {
		t.Errorf("_cross_spec error_count = %v; want 0", crossSpec["error_count"])
	}

	errorsArr, _ := crossSpec["errors"].([]any)
	found := false
	for _, e := range errorsArr {
		em, _ := e.(map[string]any)
		msg, _ := em["message"].(string)
		severity, _ := em["severity"].(string)
		if strings.Contains(msg, "glossary") && severity == "warning" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no glossary warning in the _cross_spec findings: %v", errorsArr)
	}
}

// TestTS_NS2_ForeignRequirementPrefixIsRejected replaces the v1 CLI-level
// duplicate-requirement-ID check, which format v2 makes unreachable: rule C1
// ties spec_id to the folder prefix and rule C2 requires every requirement ID
// to carry it, so a requirement copied from another spec is caught by C2 with
// a message that names the spec it belongs to.
// Covers: NS-REQ-2
func TestTS_NS2_ForeignRequirementPrefixIsRejected(t *testing.T) {
	tmpDir := t.TempDir()
	specRoot := filepath.Join(tmpDir, ".specs")

	setupLoadableSpec(t, specRoot, "08_spec_a", nil)
	setupLoadableSpec(t, specRoot, "09_spec_b", nil)

	// Give spec 09 a requirement carrying spec 08's prefix.
	reqPath := filepath.Join(specRoot, "09_spec_b", "requirements.json")
	data, err := os.ReadFile(reqPath)
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.ReplaceAll(string(data), "09-REQ-1", "08-REQ-1")
	if err := os.WriteFile(reqPath, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specRoot, "validate"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("Execute() returned nil; want exit 1 for a requirement carrying a foreign prefix")
	}

	var parsed map[string]any
	if jsonErr := json.Unmarshal(stdoutBuf.Bytes(), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, stdoutBuf.String())
	}
	specsMap, ok := parsed["specs"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'specs' to be a map, got %T", parsed["specs"])
	}
	specB, ok := specsMap["09_spec_b"].(map[string]any)
	if !ok {
		t.Fatalf("expected a result for 09_spec_b, got %T", specsMap["09_spec_b"])
	}
	if valid, _ := specB["valid"].(bool); valid {
		t.Error("09_spec_b validated despite a requirement carrying spec 08's prefix")
	}

	errorsArr, _ := specB["errors"].([]any)
	found := false
	for _, e := range errorsArr {
		em, _ := e.(map[string]any)
		msg, _ := em["message"].(string)
		if strings.Contains(msg, "08-REQ-1") && strings.Contains(msg, "prefix") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no error naming the foreign prefix; errors: %v", errorsArr)
	}
}

// TestTS_NS3_ValidationEntryMapping verifies that afspec.ValidationEntry
// fields are mapped onto the CLI's validationError struct under the
// _cross_spec key in multi-spec mode.
// Covers: TS-NS-3, NS-REQ-3
func TestTS_NS3_ValidationEntryMapping(t *testing.T) {
	tmpDir := t.TempDir()
	specRoot := filepath.Join(tmpDir, ".specs")

	setupLoadableSpec(t, specRoot, "08_spec_a", nil)
	setupLoadableSpec(t, specRoot, "09_spec_b", &loadableSpecOpts{
		dependencies: []string{"08"},
		pathActors:   []string{"auditor", "ledger"},
	})

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specRoot, "validate", "--cross"})
	_ = cmd.Execute()

	var parsed map[string]any
	if jsonErr := json.Unmarshal(stdoutBuf.Bytes(), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, stdoutBuf.String())
	}
	specsMap, ok := parsed["specs"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'specs' to be a map, got %T", parsed["specs"])
	}
	crossSpecResult, ok := specsMap["_cross_spec"].(map[string]any)
	if !ok {
		t.Fatalf("expected '_cross_spec' in the specs map, got %T", specsMap["_cross_spec"])
	}
	errorsArr, ok := crossSpecResult["errors"].([]any)
	if !ok {
		t.Fatalf("expected _cross_spec errors to be an array, got %T", crossSpecResult["errors"])
	}
	if len(errorsArr) == 0 {
		t.Fatal("_cross_spec carries no findings")
	}

	for i, e := range errorsArr {
		em, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("finding %d is %T, not an object", i, e)
		}
		severity, _ := em["severity"].(string)
		if severity != "error" && severity != "warning" {
			t.Errorf("finding %d has severity %q", i, severity)
		}
		if msg, _ := em["message"].(string); msg == "" {
			t.Errorf("finding %d has an empty message", i)
		}
	}
}

// TestTS_NS4_LoadSpecFailure verifies that when afspec.LoadSpec() fails
// (e.g. malformed prd.md frontmatter), the failure is surfaced as a
// validation error under the specs key rather than crashing the command.
// Covers: TS-NS-4, NS-REQ-4
func TestTS_NS4_LoadSpecFailure(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")

	// Create one valid spec.
	setupLoadableSpec(t, specDir, "08_spec_a", nil)

	// Create a spec with malformed prd.md (no frontmatter delimiters).
	badPath := filepath.Join(specDir, "09_spec_bad")
	if err := os.MkdirAll(badPath, 0755); err != nil {
		t.Fatal(err)
	}
	// prd.md without --- delimiters
	if err := os.WriteFile(filepath.Join(badPath, "prd.md"),
		[]byte("No frontmatter here, just text."), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badPath, "requirements.json"),
		[]byte(`{"requirements": []}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badPath, "test_spec.json"),
		[]byte(`{"tests": []}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badPath, "tasks.json"),
		[]byte(`{"tasks": []}`), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "validate", "--cross"})

	err := cmd.Execute()

	// (a) The command should return a non-nil error (exit code 1).
	if err == nil {
		t.Error("Execute() returned nil; want exit 1 for malformed prd.md")
	}

	// (b) stdout should still be valid JSON with valid=false.
	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	// NS-REQ-1: Multi-spec output must have "specs" key.
	specsMap, ok := parsed["specs"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'specs' key to be a map, got %T", parsed["specs"])
	}

	// The malformed spec should have errors in its per-spec result.
	badResult, ok := specsMap["09_spec_bad"].(map[string]any)
	if !ok {
		t.Fatalf("expected '09_spec_bad' key in specs map, got %T", specsMap["09_spec_bad"])
	}

	if valid, _ := badResult["valid"].(bool); valid {
		t.Error("expected valid=false for malformed spec")
	}
}

// --- TS-08-35: Verify that spec validate --short emits condensed output
//     with only valid, error_count, and warning_count fields ---

// TestTS08_35_ValidateShort verifies that running spec validate with
// --short emits condensed output containing valid, error_count, and
// warning_count fields (and ok only when valid).
// Covers: TS-08-35, TS-NS-2, Requirement: 08-REQ-11.4, NS-REQ-2
func TestTS08_35_ValidateShort(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	setupLoadableSpec(t, specDir, "08_my_spec", nil)

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "validate", "08_my_spec", "--short"})

	err := cmd.Execute()
	_ = err // exit code depends on validation result

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	// Verify required fields are present.
	if _, exists := parsed["valid"]; !exists {
		t.Error("parsed missing 'valid' field")
	}
	if _, exists := parsed["error_count"]; !exists {
		t.Error("parsed missing 'error_count' field")
	}
	if _, exists := parsed["warning_count"]; !exists {
		t.Error("parsed missing 'warning_count' field")
	}

	// NS-REQ-2: 'ok' key is only present when valid=true.
	valid, _ := parsed["valid"].(bool)
	if valid {
		if _, exists := parsed["ok"]; !exists {
			t.Error("parsed missing 'ok' field when valid=true (NS-REQ-2)")
		}
	} else {
		if _, exists := parsed["ok"]; exists {
			t.Error("parsed has 'ok' field when valid=false (NS-REQ-2)")
		}
	}

	// Verify types.
	if _, ok := parsed["error_count"].(float64); !ok {
		t.Errorf("error_count is %T; want number", parsed["error_count"])
	}
	if _, ok := parsed["warning_count"].(float64); !ok {
		t.Errorf("warning_count is %T; want number", parsed["warning_count"])
	}
}

// TestTS08_35_ValidateShortFieldsOnly verifies that --short output
// contains ONLY the condensed fields (valid, error_count, warning_count,
// and ok when valid) and no extra verbose fields.
// Covers: TS-08-35, TS-NS-2, Requirement: 08-REQ-11.4, NS-REQ-2
func TestTS08_35_ValidateShortFieldsOnly(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	setupLoadableSpec(t, specDir, "08_my_spec", nil)

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "validate", "08_my_spec", "--short"})

	_ = cmd.Execute()

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	// The only allowed keys are: ok (conditional), valid, error_count, warning_count.
	allowedKeys := map[string]bool{
		"ok":            true,
		"valid":         true,
		"error_count":   true,
		"warning_count": true,
	}
	for key := range parsed {
		if !allowedKeys[key] {
			t.Errorf("--short output contains unexpected key %q; want only valid/error_count/warning_count (and ok when valid)", key)
		}
	}
}

// --- TS-NS-1: Single-spec validate invokes library validation and surfaces
//     schema/integrity errors (cross-file integrity violation). ---

// TestTS_NS1_SingleSpecIntegrityError verifies that when a spec has a
// cross-file integrity violation (test case referencing a non-existent
// requirement), spec validate <SPEC> exits 1, error_count > 0, and the
// errors array contains an integrity-category message.
// Covers: TS-NS-1, NS-REQ-1
func TestTS_NS1_SingleSpecIntegrityError(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")

	// A spec that is schema-valid throughout but whose first test verifies a
	// criterion that does not exist: rule C3.
	testSpec := `{
  "$schema": "https://agent-fox.dev/schemas/test_spec.v2.json",
  "spec_id": "08",
  "spec_name": "broken",
  "schema_version": 2,
  "tests": [
    {
      "id": "TS-08-1",
      "kind": "unit",
      "verifies": ["08-REQ-999.1"],
      "title": "A test that verifies a criterion which does not exist",
      "given": [],
      "when": "the store is exercised",
      "then": ["something happens"]
    },
    {
      "id": "TS-08-2",
      "kind": "smoke",
      "verifies": ["08-PATH-1"],
      "title": "A client stores a widget end to end",
      "given": [],
      "when": "a widget is submitted and read back",
      "then": ["the read returns the widget that was submitted"],
      "real_components": ["widget service", "store"]
    }
  ]
}`
	writeSpecFixture(t, filepath.Join(specDir, "08_broken"), "08", "broken", specFixture{
		TestSpecOverride: testSpec,
	})

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "validate", "08_broken"})

	err := cmd.Execute()

	// Must exit 1 (validation errors present).
	if err == nil {
		t.Fatal("Execute() returned nil; want exit 1 for cross-file integrity error")
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	// error_count > 0
	errorCount, ok := parsed["error_count"].(float64)
	if !ok || errorCount < 1 {
		t.Errorf("expected error_count > 0, got %v", parsed["error_count"])
	}

	// errors array contains an integrity-category message (not just file-missing or parse error).
	errorsArr, ok := parsed["errors"].([]any)
	if !ok {
		t.Fatalf("expected errors to be an array, got %T", parsed["errors"])
	}
	foundIntegrity := false
	for _, e := range errorsArr {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		msg, _ := em["message"].(string)
		// C3 names the dangling reference; C4 names the criterion no test
		// verifies. Either proves a cross-file rule ran.
		if strings.Contains(msg, "08-REQ-999.1") || strings.Contains(msg, "not verified by any test") {
			foundIntegrity = true
			break
		}
	}
	if !foundIntegrity {
		t.Errorf("expected an integrity error message (not just file-missing or parse), got errors: %s", output)
	}
}

// --- TS-NS-2: Single-spec validate on conforming spec reports valid=true, exit 0. ---

// TestTS_NS2_SingleSpecValid verifies that spec validate on a fully
// conforming spec exits 0, valid=true, error_count=0, ok=true.
// Covers: TS-NS-2, NS-REQ-2
func TestTS_NS2_SingleSpecValid(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	setupLoadableSpec(t, specDir, "08_good_spec", nil)

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "validate", "08_good_spec"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v; want exit 0 for valid spec\noutput: %s", err, stdoutBuf.String())
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	if valid, _ := parsed["valid"].(bool); !valid {
		t.Errorf("expected valid=true, got %v\noutput: %s", parsed["valid"], output)
	}
	if ec, _ := parsed["error_count"].(float64); ec != 0 {
		t.Errorf("expected error_count=0, got %v\noutput: %s", ec, output)
	}
	if okVal, exists := parsed["ok"]; !exists {
		t.Error("expected 'ok' key present when valid=true")
	} else if okBool, isBool := okVal.(bool); !isBool || !okBool {
		t.Errorf("expected ok=true, got %v", okVal)
	}
}

// --- TS-NS-3: Multi-spec validate invokes library validation for each spec. ---

// TestTS_NS3_MultiSpecLibraryErrors verifies that multi-spec validate
// (no --cross) invokes library validation per-spec and reports library-
// sourced errors for invalid specs while valid specs show error_count=0.
// Covers: TS-NS-3, NS-REQ-3
func TestTS_NS3_MultiSpecLibraryErrors(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")

	// Create one fully valid spec.
	setupLoadableSpec(t, specDir, "08_valid_spec", nil)

	// Create one spec with a cross-file integrity error (dangling test reference).
	setupLoadableSpec(t, specDir, "09_invalid_spec", nil)
	// Overwrite test_spec.json to reference a non-existent requirement.
	testSpec := `{
  "$schema": "https://agent-fox.dev/schemas/test_spec.v1.json",
  "spec_id": "09",
  "spec_name": "09_invalid_spec",
  "schema_version": 1,
  "test_cases": [{
    "id": "TS-09-1",
    "requirement_id": "09-REQ-999.1",
    "kind": "unit",
    "description": "Test referencing non-existent requirement",
    "preconditions": [],
    "input": {},
    "expected": {},
    "assertion_pseudocode": "assert true"
  }],
  "property_tests": [],
  "edge_case_tests": [],
  "smoke_tests": [{
    "id": "TS-09-SMOKE-1",
    "execution_path_id": "09-PATH-1",
    "description": "Smoke test",
    "trigger": "run",
    "real_components": ["all"],
    "mockable": [],
    "expected_effects": ["works"]
  }],
  "coverage": {
    "requirements_covered": [],
    "properties_covered": [],
    "paths_covered": [],
    "gaps": []
  }
}`
	if err := os.WriteFile(filepath.Join(specDir, "09_invalid_spec", "test_spec.json"), []byte(testSpec), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "validate"})

	err := cmd.Execute()

	// Must exit 1 because one spec is invalid.
	if err == nil {
		t.Fatal("Execute() returned nil; want exit 1 because 09_invalid_spec has errors")
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	// Top-level valid=false.
	if valid, _ := parsed["valid"].(bool); valid {
		t.Error("expected top-level valid=false")
	}

	specsMap, ok := parsed["specs"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'specs' key, got %T", parsed["specs"])
	}

	// Valid spec should have error_count=0.
	validEntry, ok := specsMap["08_valid_spec"].(map[string]any)
	if !ok {
		t.Fatal("missing '08_valid_spec' in specs map")
	}
	if ec, _ := validEntry["error_count"].(float64); ec != 0 {
		t.Errorf("valid spec error_count=%v, want 0\nentry: %v", ec, validEntry)
	}

	// Invalid spec should have error_count > 0 with library-sourced errors.
	invalidEntry, ok := specsMap["09_invalid_spec"].(map[string]any)
	if !ok {
		t.Fatal("missing '09_invalid_spec' in specs map")
	}
	if ec, _ := invalidEntry["error_count"].(float64); ec < 1 {
		t.Errorf("invalid spec error_count=%v, want > 0", ec)
	}

	// Check that errors contain a library-sourced message (integrity/schema).
	errorsArr, ok := invalidEntry["errors"].([]any)
	if !ok {
		t.Fatalf("expected errors to be an array, got %T", invalidEntry["errors"])
	}
	foundLibrary := false
	for _, e := range errorsArr {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		msg, _ := em["message"].(string)
		// Library errors reference schema/integrity violations, not 'malformed JSON'.
		if !strings.Contains(msg, "malformed JSON") && msg != "" {
			foundLibrary = true
			break
		}
	}
	if !foundLibrary {
		t.Error("expected library-sourced error (not just 'malformed JSON')")
	}
}

// --- TS-NS-4: Library warnings counted in warning_count. ---

// TestTS_NS4_WarningsInWarningCount verifies that library warnings
// (e.g. vague language) are counted in warning_count, that exit code
// is 0 (warnings don't cause exit 1), and valid=true.
// Covers: TS-NS-4, NS-REQ-4
func TestTS_NS4_WarningsInWarningCount(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")

	// A complete, valid spec whose first criterion uses vague language: §6.5.4
	// makes that a warning, and a warning must not fail the run.
	setupLoadableSpec(t, specDir, "08_vague_spec", nil)

	reqPath := filepath.Join(specDir, "08_vague_spec", "requirements.json")
	data, err := os.ReadFile(reqPath)
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(data),
		"persist the widget and return its identifier",
		"handle the input in an appropriate manner and return a reasonable result", 1)
	if patched == string(data) {
		t.Fatal("the fixture text to patch was not found")
	}
	if err := os.WriteFile(reqPath, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "validate", "08_vague_spec"})

	err = cmd.Execute()

	// Exit 0 — warnings do NOT cause exit 1.
	if err != nil {
		t.Fatalf("Execute() returned error: %v; want exit 0 (warnings only)\noutput: %s", err, stdoutBuf.String())
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	// valid=true (no errors).
	if valid, _ := parsed["valid"].(bool); !valid {
		t.Errorf("expected valid=true, got %v\noutput: %s", parsed["valid"], output)
	}

	// warning_count > 0 (vague language detected).
	wc, _ := parsed["warning_count"].(float64)
	if wc < 1 {
		t.Errorf("expected warning_count > 0, got %v\noutput: %s", wc, output)
	}
}

// --- TS-NS-5: File-existence pre-flight errors reported before library load. ---

// TestTS_NS5_PreflightMissingFile verifies that when a required file
// is missing, the error explicitly names the missing file (pre-flight
// error), and exits 1.
// Covers: TS-NS-5, NS-REQ-5
func TestTS_NS5_PreflightMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "08_missing")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	// Create all files except tasks.json.
	if err := os.WriteFile(filepath.Join(specPath, "prd.md"),
		[]byte("# Test PRD\nContent."), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specPath, "requirements.json"),
		[]byte(`{"requirements": []}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specPath, "test_spec.json"),
		[]byte(`{"tests": []}`), 0644); err != nil {
		t.Fatal(err)
	}
	// tasks.json intentionally missing.

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "validate", "08_missing"})

	err := cmd.Execute()

	// Must exit 1.
	if err == nil {
		t.Fatal("Execute() returned nil; want exit 1 for missing required file")
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	// error_count > 0.
	ec, _ := parsed["error_count"].(float64)
	if ec < 1 {
		t.Errorf("expected error_count > 0, got %v", ec)
	}

	// Error message explicitly names the missing file.
	errorsArr, ok := parsed["errors"].([]any)
	if !ok {
		t.Fatalf("expected errors array, got %T", parsed["errors"])
	}
	foundMissing := false
	for _, e := range errorsArr {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		msg, _ := em["message"].(string)
		if strings.Contains(msg, "tasks.json") && strings.Contains(msg, "missing") {
			foundMissing = true
			break
		}
	}
	if !foundMissing {
		t.Errorf("expected error message naming 'tasks.json' as missing, got: %s", output)
	}
}

// --- 08-REQ-11.E1: Missing required file reported as validation error ---

// TestTS08_32_ValidateMissingRequiredFile verifies that when a required
// file (prd.md, requirements.json, test_spec.json, or tasks.json) is
// missing in single-spec mode, the missing file is reported as a
// validation error in the result (not a command failure) and exit code is 1.
// Covers: 08-REQ-11.E1
func TestTS08_32_ValidateMissingRequiredFile(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "08_my_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	// Only create prd.md and requirements.json — tasks.json and test_spec.json missing.
	if err := os.WriteFile(filepath.Join(specPath, "prd.md"),
		[]byte("# Test PRD"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specPath, "requirements.json"),
		[]byte(`{"requirements": []}`), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "validate", "08_my_spec"})

	err := cmd.Execute()

	// Should exit 1 — missing required files are validation errors.
	if err == nil {
		t.Error("Execute() returned nil; want exit 1 for missing required files")
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	// The result should mention the missing files as validation errors.
	if errorCount, ok := parsed["error_count"].(float64); !ok || errorCount == 0 {
		t.Error("error_count is 0 or missing; want > 0 for missing required files")
	}
}

// --- 08-REQ-11.E2: Malformed JSON reported as validation error ---

// TestTS08_32_ValidateMalformedJSON verifies that when a JSON artifact
// file contains malformed JSON, the parse error is reported as a
// validation error in the result.
// Covers: 08-REQ-11.E2
func TestTS08_32_ValidateMalformedJSON(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "08_my_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(specPath, "prd.md"),
		[]byte("# Test PRD"), 0644); err != nil {
		t.Fatal(err)
	}
	// Malformed JSON in requirements.json.
	if err := os.WriteFile(filepath.Join(specPath, "requirements.json"),
		[]byte(`{not valid json!!!`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specPath, "test_spec.json"),
		[]byte(`{"tests": []}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specPath, "tasks.json"),
		[]byte(`{"tasks": []}`), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "validate", "08_my_spec"})

	err := cmd.Execute()

	// Should exit 1 — parse error is a validation error.
	if err == nil {
		t.Error("Execute() returned nil; want exit 1 for malformed JSON")
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	if errorCount, ok := parsed["error_count"].(float64); !ok || errorCount == 0 {
		t.Error("error_count is 0 or missing; want > 0 for malformed JSON")
	}
}

// --- 08-REQ-11.E3: DiscoverSpecs error propagation ---

// TestTS08_33_ValidateDiscoverError verifies that when the spec
// directory does not exist or cannot be read in multi-spec mode,
// the error is propagated and the command exits without emitting
// a partial result.
// Covers: 08-REQ-11.E3
func TestTS08_33_ValidateDiscoverError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping permission test when running as root")
	}

	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Make the spec directory unreadable.
	if err := os.Chmod(specDir, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(specDir, 0755)

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	stderrBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{"--spec-dir", specDir, "validate"})

	err := cmd.Execute()
	if err == nil {
		t.Error("Execute() with unreadable spec directory returned nil; want error")
	}
}

// --- Validate: non-existent spec in single-spec mode ---

// TestTS08_32_ValidateNonexistentSpec verifies that spec validate
// returns an error when the referenced spec does not exist.
// Covers: 08-REQ-11.1
func TestTS08_32_ValidateNonexistentSpec(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0755); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "validate", "nonexistent_spec"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() with non-existent spec returned nil; want error")
	}
}

// --- 08-PROP-4: Exit code reflects error presence ---

// TestTS08_32_ValidateExitCodeProperty verifies the correctness
// property 08-PROP-4 across multiple scenarios: exit code 1 iff
// validation errors exist.
// Covers: 08-PROP-4
func TestTS08_32_ValidateExitCodeProperty(t *testing.T) {
	scenarios := []struct {
		name      string
		wantError bool
		setup     func(t *testing.T, specDir string)
	}{
		{
			name:      "all_files_valid",
			wantError: false,
			setup: func(t *testing.T, specDir string) {
				setupLoadableSpec(t, specDir, "08_my_spec", nil)
			},
		},
		{
			name:      "missing_tasks_json",
			wantError: true,
			setup: func(t *testing.T, specDir string) {
				specPath := filepath.Join(specDir, "08_my_spec")
				os.MkdirAll(specPath, 0755)
				os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# PRD"), 0644)
				os.WriteFile(filepath.Join(specPath, "requirements.json"), []byte(`{"requirements":[]}`), 0644)
				os.WriteFile(filepath.Join(specPath, "test_spec.json"), []byte(`{"tests":[]}`), 0644)
				// tasks.json intentionally missing
			},
		},
		{
			name:      "malformed_json",
			wantError: true,
			setup: func(t *testing.T, specDir string) {
				specPath := filepath.Join(specDir, "08_my_spec")
				os.MkdirAll(specPath, 0755)
				os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# PRD"), 0644)
				os.WriteFile(filepath.Join(specPath, "requirements.json"), []byte(`INVALID`), 0644)
				os.WriteFile(filepath.Join(specPath, "test_spec.json"), []byte(`{"tests":[]}`), 0644)
				os.WriteFile(filepath.Join(specPath, "tasks.json"), []byte(`{"tasks":[]}`), 0644)
			},
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			specDir := filepath.Join(tmpDir, ".specs")
			sc.setup(t, specDir)

			cmd := newRootCmd()
			stdoutBuf := new(bytes.Buffer)
			cmd.SetOut(stdoutBuf)
			cmd.SetErr(new(bytes.Buffer))
			cmd.SetArgs([]string{"--spec-dir", specDir, "validate", "08_my_spec"})

			err := cmd.Execute()
			if sc.wantError && err == nil {
				t.Error("Execute() returned nil; want exit 1 for validation errors")
			}
			if !sc.wantError && err != nil {
				t.Errorf("Execute() returned error: %v; want exit 0 for no validation errors", err)
			}
		})
	}
}

// --- spec validate --trace: the derived traceability matrix (§8.5) ---

// TestValidateTracePrintsTheDerivedMatrix verifies that --trace reports, for
// every criterion and path, the tests that verify it and the tasks that own
// those tests. Nothing is read from a stored traceability array, because v2
// does not have one.
func TestValidateTracePrintsTheDerivedMatrix(t *testing.T) {
	tmpDir := t.TempDir()
	specRoot := newSpecRoot(t, tmpDir, "08", "my_spec", specFixture{})

	cmd := newRootCmd()
	stdout := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specRoot, "validate", "--trace", "08_my_spec"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("spec validate --trace = %v\noutput: %s", err, stdout.String())
	}

	var parsed map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", err, stdout.String())
	}
	if uncovered, _ := parsed["uncovered"].(float64); uncovered != 0 {
		t.Errorf("uncovered = %v; the fixture verifies everything", parsed["uncovered"])
	}
	if unowned, _ := parsed["unowned"].(float64); unowned != 0 {
		t.Errorf("unowned = %v; the fixture owns everything", parsed["unowned"])
	}

	rows, ok := parsed["trace"].([]any)
	if !ok {
		t.Fatalf("trace is %T; want an array", parsed["trace"])
	}
	// Two criteria plus one path.
	if len(rows) != 3 {
		t.Fatalf("trace has %d rows; want one per criterion and path", len(rows))
	}

	byID := map[string]map[string]any{}
	for _, row := range rows {
		m, _ := row.(map[string]any)
		id, _ := m["id"].(string)
		byID[id] = m
	}

	criterion, ok := byID["08-REQ-1.1"]
	if !ok {
		t.Fatalf("the matrix has no row for 08-REQ-1.1: %v", byID)
	}
	if criterion["kind"] != "criterion" {
		t.Errorf("kind = %v; want criterion", criterion["kind"])
	}
	if tests, _ := criterion["tests"].([]any); len(tests) != 1 || tests[0] != "TS-08-1" {
		t.Errorf("tests = %v; want TS-08-1", criterion["tests"])
	}
	if tasks, _ := criterion["tasks"].([]any); len(tasks) != 1 || tasks[0].(float64) != 1 {
		t.Errorf("tasks = %v; want task 1", criterion["tasks"])
	}

	path, ok := byID["08-PATH-1"]
	if !ok {
		t.Fatalf("the matrix has no row for 08-PATH-1: %v", byID)
	}
	if path["kind"] != "path" {
		t.Errorf("kind = %v; want path", path["kind"])
	}
	if tasks, _ := path["tasks"].([]any); len(tasks) != 1 || tasks[0].(float64) != 2 {
		t.Errorf("tasks = %v; want the integration task", path["tasks"])
	}
}

// TestValidateTraceReportsGaps checks that the matrix marks what nothing
// verifies and what nothing owns, rather than hiding it.
func TestValidateTraceReportsGaps(t *testing.T) {
	tmpDir := t.TempDir()
	specRoot := newSpecRoot(t, tmpDir, "08", "my_spec", specFixture{})

	// Take TS-08-2 away from its task: the test is still verified but unowned.
	tasksPath := filepath.Join(specRoot, "08_my_spec", "tasks.json")
	data, err := os.ReadFile(tasksPath)
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(data), `"tests": ["TS-08-1", "TS-08-2"]`, `"tests": ["TS-08-1"]`, 1)
	if patched == string(data) {
		t.Fatal("the fixture text to patch was not found")
	}
	if err := os.WriteFile(tasksPath, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdout := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specRoot, "validate", "--trace", "08_my_spec"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("spec validate --trace = %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if unowned, _ := parsed["unowned"].(float64); unowned != 1 {
		t.Errorf("unowned = %v; want 1 for the criterion whose only test no task owns", parsed["unowned"])
	}
	if uncovered, _ := parsed["uncovered"].(float64); uncovered != 0 {
		t.Errorf("uncovered = %v; the test still exists, it is just unowned", parsed["uncovered"])
	}
}

func TestValidateTraceRequiresASpecArgument(t *testing.T) {
	tmpDir := t.TempDir()
	specRoot := newSpecRoot(t, tmpDir, "08", "my_spec", specFixture{})

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specRoot, "validate", "--trace"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("spec validate --trace with no SPEC argument exited 0")
	}
	if !strings.Contains(err.Error(), "SPEC") {
		t.Errorf("the error does not say a SPEC argument is needed: %v", err)
	}
}
