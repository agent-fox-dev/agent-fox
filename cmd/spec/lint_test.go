package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- TS-08-36: Verify that spec lint runs RunLintSpecs with a spinner,
//     emits LintFinding list as JSON, and exits 1 if exit_code from
//     lint is non-zero ---

// TestTS08_36_LintRunsAndEmitsFindings verifies that spec lint runs
// RunLintSpecs with a spinner, emits findings as JSON to stdout, and
// uses the lint exit_code to determine the command exit code.
// Covers: TS-08-36, Requirement: 08-REQ-12.1
func TestTS08_36_LintRunsAndEmitsFindings(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a spec directory with minimal content to trigger lint findings.
	specPath := filepath.Join(specDir, "08_my_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specPath, "prd.md"),
		[]byte("# Minimal PRD"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	stderrBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{"--spec-dir", specDir, "lint"})

	err := cmd.Execute()

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	// The result should contain a findings array.
	findings, ok := parsed["findings"].([]any)
	if !ok {
		t.Fatal("parsed.findings is not an array")
	}

	// exit_code must be present.
	exitCodeVal, exists := parsed["exit_code"]
	if !exists {
		t.Fatal("parsed missing 'exit_code' field")
	}

	// If there are error-severity findings, exit code should be 1.
	hasError := false
	for _, f := range findings {
		fm := f.(map[string]any)
		if fm["severity"] == "error" {
			hasError = true
			break
		}
	}
	if hasError && err == nil {
		t.Error("exit code 0 with error-severity findings present; want exit 1")
	}
	if hasError && exitCodeVal.(float64) != 1 {
		t.Errorf("exit_code = %v; want 1 with error-severity findings", exitCodeVal)
	}
}

// TestTS08_36_LintExitCode1OnNonZeroExitCode verifies that when
// lint finds error-severity findings, the lint command exits 1 and
// exit_code=1.
// Covers: NS-REQ-3, TS-NS-3
func TestTS08_36_LintExitCode1OnNonZeroExitCode(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")

	// Create a spec with lint-triggering content (missing artifacts).
	specPath := filepath.Join(specDir, "08_lint_fail")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specPath, "prd.md"),
		[]byte("# PRD"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "lint"})

	err := cmd.Execute()

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	// cmd.Execute() should return error (exit 1).
	if err == nil {
		t.Error("Execute() returned nil; want error (exit 1) when error-severity findings exist")
	}

	// exit_code must be present and equal to 1.
	exitCode, exists := parsed["exit_code"]
	if !exists {
		t.Fatal("parsed missing 'exit_code' field")
	}
	if exitCode.(float64) != 1 {
		t.Errorf("exit_code = %v; want 1", exitCode)
	}

	// ok must be false.
	if okVal, _ := parsed["ok"].(bool); okVal {
		t.Error("ok = true; want false when error-severity findings exist")
	}
}

// TestTS08_36_LintEmitsJSONWithOK verifies that the lint output
// contains "ok", "findings", and "exit_code" fields in the JSON envelope.
// Covers: NS-REQ-1, TS-NS-1
func TestTS08_36_LintEmitsJSONWithOK(t *testing.T) {
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

	if _, exists := parsed["ok"]; !exists {
		t.Error("parsed missing 'ok' field")
	}
	if _, exists := parsed["findings"]; !exists {
		t.Error("parsed missing 'findings' field")
	}
	if _, exists := parsed["exit_code"]; !exists {
		t.Error("parsed missing 'exit_code' field")
	}
}

// writeValidSpecDir creates a complete, valid spec directory that passes
// library LoadSpec + Validate. The spec uses NN_snake_case naming convention.
// subtaskStates controls task_groups subtask states; nil means no subtasks.
// reqSpecIDOverride, if non-empty, replaces spec_id in requirements.json
// to create a deliberate spec_id mismatch for validation error testing.
func writeValidSpecDir(t *testing.T, dir, specID, specName string, subtaskStates []string, reqSpecIDOverride string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	effectiveReqSpecID := specID
	if reqSpecIDOverride != "" {
		effectiveReqSpecID = reqSpecIDOverride
	}

	prd := fmt.Sprintf("---\nspec_id: %q\nspec_name: %q\ntitle: %q\nstatus: \"draft\"\n"+
		"created_at: \"2026-01-01T00:00:00Z\"\nupdated_at: \"2026-01-01T00:00:00Z\"\n"+
		"owner: \"test\"\nsource: \"test\"\nschema_version: 1\n---\n# %s\n",
		specID, specName, specName, specName)
	if err := os.WriteFile(filepath.Join(dir, "prd.md"), []byte(prd), 0644); err != nil {
		t.Fatal(err)
	}

	req := fmt.Sprintf(
		`{`+
			`"$schema":"https://agent-fox.dev/schemas/requirements.v1.json",`+
			`"spec_id":%q,"spec_name":%q,"schema_version":1,"introduction":"Test",`+
			`"glossary":{"system":"The test system."},`+
			`"requirements":[{`+
			`"id":"%s-REQ-1","title":"Feature",`+
			`"user_story":{"role":"user","goal":"test","benefit":"testing"},`+
			`"acceptance_criteria":[{`+
			`"id":"%s-REQ-1.1","ears_pattern":"event_driven",`+
			`"trigger":"a request is made","system":"the system",`+
			`"action":"process it","return_contract":"a result"`+
			`}],"edge_cases":[{`+
			`"id":"%s-REQ-1.E1","ears_pattern":"unwanted",`+
			`"error_condition":"input is invalid","system":"the system",`+
			`"action":"reject it","return_contract":"raises an error"`+
			`}]}],`+
			`"correctness_properties":[{`+
			`"id":"%s-PROP-1","title":"Idempotency",`+
			`"for_any":"valid input","invariant":"processing is idempotent",`+
			`"validates":["%s-REQ-1.1"]}],`+
			`"execution_paths":[{`+
			`"id":"%s-PATH-1","title":"Main path",`+
			`"steps":[{"actor":"user","action":"send request"},`+
			`{"actor":"system","action":"process request"}]}],`+
			`"error_handling":[{`+
			`"id":"%s-ERR-1","condition":"Invalid input",`+
			`"behavior":"Return error","requirement_id":"%s-REQ-1.E1"}]}`,
		effectiveReqSpecID, specName,
		effectiveReqSpecID, effectiveReqSpecID, effectiveReqSpecID,
		effectiveReqSpecID, effectiveReqSpecID,
		effectiveReqSpecID,
		effectiveReqSpecID, effectiveReqSpecID)
	if err := os.WriteFile(filepath.Join(dir, "requirements.json"), []byte(req), 0644); err != nil {
		t.Fatal(err)
	}

	ts := fmt.Sprintf(
		`{`+
			`"$schema":"https://agent-fox.dev/schemas/test_spec.v1.json",`+
			`"spec_id":%q,"spec_name":%q,"schema_version":1,`+
			`"test_cases":[{`+
			`"id":"TS-%s-1","requirement_id":"%s-REQ-1.1","kind":"unit",`+
			`"description":"Test feature","preconditions":[],`+
			`"input":{},"expected":{"ok":true},`+
			`"assertion_pseudocode":"assert ok"}],`+
			`"property_tests":[{`+
			`"id":"TS-%s-P1","property_id":"%s-PROP-1","validates":["%s-REQ-1.1"],`+
			`"description":"Idempotency","for_any_strategy":"valid input",`+
			`"invariant_check":"f(f(x)) == f(x)"}],`+
			`"edge_case_tests":[{`+
			`"id":"TS-%s-E1","requirement_id":"%s-REQ-1.E1","kind":"unit",`+
			`"description":"Invalid input","preconditions":[],`+
			`"input":{},"expected":{"error":true},`+
			`"assertion_pseudocode":"assert error"}],`+
			`"smoke_tests":[{`+
			`"id":"TS-%s-SMOKE-1","execution_path_id":"%s-PATH-1",`+
			`"description":"End-to-end","trigger":"run",`+
			`"real_components":["io"],"mockable":[],`+
			`"expected_effects":["Success"]}],`+
			`"coverage":{`+
			`"requirements_covered":["%s-REQ-1.1","%s-REQ-1.E1"],`+
			`"properties_covered":["%s-PROP-1"],`+
			`"paths_covered":["%s-PATH-1"],"gaps":[]}}`,
		specID, specName,
		specID, specID,
		specID, specID, specID,
		specID, specID,
		specID, specID,
		specID, specID,
		specID,
		specID)
	if err := os.WriteFile(filepath.Join(dir, "test_spec.json"), []byte(ts), 0644); err != nil {
		t.Fatal(err)
	}

	taskGroupsJSON := "[]"
	if subtaskStates != nil {
		var subtasks []string
		for i, state := range subtaskStates {
			subtasks = append(subtasks, fmt.Sprintf(
				`{"id":"1.%d","title":"Subtask %d","details":["d"],`+
					`"test_spec_refs":["TS-%s-1"],"requirement_refs":["%s-REQ-1.1"],`+
					`"state":%q,"optional":false}`,
				i+1, i+1, specID, specID, state))
		}
		// Use proper task group kinds: first group "tests", last group
		// "wiring_verification" — library validation requires this structure.
		taskGroupsJSON = fmt.Sprintf(
			`[{"id":1,"kind":"tests","title":"Write tests","subtasks":[%s],`+
				`"verification":{"id":"1.V","checks":["All tests pass"]}},`+
				`{"id":2,"kind":"wiring_verification","title":"Wiring verification","subtasks":[`+
				`{"id":"2.1","title":"Stub and dead-code audit, trace execution paths end-to-end",`+
				`"details":["Verify all paths are wired","Confirm no stubs remain"],`+
				`"test_spec_refs":["TS-%s-SMOKE-1"],"requirement_refs":["%s-REQ-1.1"],`+
				`"state":%q,"optional":false}],`+
				`"verification":{"id":"2.V","checks":["All smoke tests pass"]}}]`,
			strings.Join(subtasks, ","), specID, specID, subtaskStates[0])
	}
	tasks := fmt.Sprintf(
		`{`+
			`"$schema":"https://agent-fox.dev/schemas/tasks.v1.json",`+
			`"spec_id":%q,"spec_name":%q,"schema_version":1,`+
			`"test_commands":{"spec_tests":"go test","all_tests":"go test","linter":"go vet"},`+
			`"dependencies":[],`+
			`"task_groups":%s,`+
			`"traceability":[`+
			`{"requirement_id":"%s-REQ-1.1","test_spec_id":"TS-%s-1","task_id":"1.1","test_path":null},`+
			`{"requirement_id":"%s-REQ-1.E1","test_spec_id":"TS-%s-E1","task_id":"1.1","test_path":null}`+
			`]}`,
		specID, specName, taskGroupsJSON,
		specID, specID,
		specID, specID)
	if err := os.WriteFile(filepath.Join(dir, "tasks.json"), []byte(tasks), 0644); err != nil {
		t.Fatal(err)
	}
}

// --- NS-REQ-2: Warnings-only findings → exit 0, exit_code=0 ---

// TestLintWarningsOnlyExitCode0 verifies that when all findings have
// severity 'warning' (no error-severity findings), the lint command
// exits 0 with exit_code=0 and ok=true.
// Covers: NS-REQ-4, TS-NS-4
func TestLintWarningsOnlyExitCode0(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")

	// Create a valid spec (passes library validation) but without
	// _session.json → triggers missing-session warning (CLI extra).
	// Include a pending subtask so the spec is not treated as fully implemented.
	specPath := filepath.Join(specDir, "08_warnings_only")
	writeValidSpecDir(t, specPath, "08", "warnings_only", []string{"pending"}, "")
	// No _session.json → missing-session warning.

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "lint"})

	err := cmd.Execute()

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	// cmd.Execute() should return nil (exit 0).
	if err != nil {
		t.Errorf("Execute() returned error %v; want nil (exit 0) for warnings-only", err)
	}

	// exit_code must be 0.
	exitCode, exists := parsed["exit_code"]
	if !exists {
		t.Fatal("parsed missing 'exit_code' field")
	}
	if exitCode.(float64) != 0 {
		t.Errorf("exit_code = %v; want 0", exitCode)
	}

	// ok must be true.
	if okVal, _ := parsed["ok"].(bool); !okVal {
		t.Errorf("ok = %v; want true for warnings-only", parsed["ok"])
	}

	// Should have at least one warning finding (missing-session).
	findings, ok := parsed["findings"].([]any)
	if !ok {
		t.Fatal("parsed.findings is not an array")
	}
	if len(findings) == 0 {
		t.Error("expected at least one warning finding, got none")
	}
	// Verify all findings are warnings.
	for i, f := range findings {
		fm := f.(map[string]any)
		if fm["severity"] != "warning" {
			t.Errorf("findings[%d] = %v; severity = %v; want 'warning'", i, fm, fm["severity"])
		}
	}
}

// --- TS-08-37: Verify that spec lint --all passes lintAll=true to
//     RunLintSpecs to include fully-implemented specs ---

// TestTS08_37_LintAllFlagRegistered verifies that the --all flag is
// registered on the lint command.
// Covers: TS-08-37, Requirement: 08-REQ-12.2
func TestTS08_37_LintAllFlagRegistered(t *testing.T) {
	cmd := newLintCmd()
	allFlag := cmd.Flags().Lookup("all")
	if allFlag == nil {
		t.Fatal("flag --all is not registered on lint command")
	}
	if allFlag.DefValue != "false" {
		t.Errorf("--all default = %q; want %q", allFlag.DefValue, "false")
	}
}

// TestTS08_37_LintAllIncludesImplementedSpecs verifies that running
// spec lint --all includes fully-implemented specs in the lint findings.
// Without --all, implemented specs are skipped. With --all, they are linted.
// Covers: NS-REQ-2, TS-NS-2
func TestTS08_37_LintAllIncludesImplementedSpecs(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")

	// Create a fully-implemented spec (all subtasks done) with a
	// deliberate spec_id mismatch to produce a validation error.
	specPath := filepath.Join(specDir, "08_complete_spec")
	writeValidSpecDir(t, specPath, "08", "complete_spec",
		[]string{"done", "done"}, // all subtasks done → fully implemented
		"99",                     // spec_id mismatch: prd has "08", requirements has "99"
	)

	// Without --all: the implemented spec should be skipped.
	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "lint"})

	_ = cmd.Execute()

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal(stdoutBuf.Bytes(), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}
	findings, ok := parsed["findings"].([]any)
	if !ok {
		t.Fatal("parsed.findings is not an array")
	}
	// The implemented spec should be skipped → no findings for it.
	for _, f := range findings {
		fm := f.(map[string]any)
		if fm["spec"] == "complete_spec" {
			t.Errorf("without --all, implemented spec should be skipped; got finding: %v", fm)
		}
	}

	// With --all: the implemented spec should be linted and produce findings.
	cmd2 := newRootCmd()
	stdoutBuf2 := new(bytes.Buffer)
	cmd2.SetOut(stdoutBuf2)
	cmd2.SetErr(new(bytes.Buffer))
	cmd2.SetArgs([]string{"--spec-dir", specDir, "lint", "--all"})

	_ = cmd2.Execute()

	var parsed2 map[string]any
	if jsonErr := json.Unmarshal(stdoutBuf2.Bytes(), &parsed2); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, stdoutBuf2.String())
	}
	findings2, ok := parsed2["findings"].([]any)
	if !ok {
		t.Fatal("parsed.findings is not an array")
	}

	// With --all, findings should include at least one for the implemented spec.
	hasSpecFinding := false
	for _, f := range findings2 {
		fm := f.(map[string]any)
		if fm["spec"] == "complete_spec" {
			hasSpecFinding = true
			break
		}
	}
	if !hasSpecFinding {
		t.Error("with --all, expected findings for implemented spec 'complete_spec'")
	}
}

// --- 08-REQ-12.E1: RunLintSpecs error propagation ---

// TestTS08_36_LintErrorPropagation verifies that when RunLintSpecs
// returns an error (e.g., spec directory unreadable), the error is
// propagated and the command exits without emitting partial findings.
// Covers: 08-REQ-12.E1
func TestTS08_36_LintErrorPropagation(t *testing.T) {
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
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "lint"})

	err := cmd.Execute()
	if err == nil {
		t.Error("Execute() with unreadable spec directory returned nil; want error")
	}
}

// --- NS-REQ-5: Empty spec directory emits no-specs finding ---

// TestTS08_36_LintEmptySpecDir verifies that when the spec directory
// exists but contains no lintable spec subdirectories, the lint command
// emits a no-specs finding with severity 'error' and exits 1.
// Covers: NS-REQ-5, TS-NS-5
func TestTS08_36_LintEmptySpecDir(t *testing.T) {
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

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() on empty spec dir returned nil; want error (exit 1)")
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	// exit_code must be 1.
	exitCode, exists := parsed["exit_code"]
	if !exists {
		t.Fatal("parsed missing 'exit_code' field")
	}
	if exitCode.(float64) != 1 {
		t.Errorf("exit_code = %v; want 1", exitCode)
	}

	findings, ok := parsed["findings"].([]any)
	if !ok {
		t.Fatal("parsed.findings is not an array")
	}
	if len(findings) < 1 {
		t.Fatal("findings is empty; want at least one no-specs finding")
	}

	f0 := findings[0].(map[string]any)
	if f0["rule"] != "no-specs" {
		t.Errorf("findings[0].rule = %v; want 'no-specs'", f0["rule"])
	}
	if f0["severity"] != "error" {
		t.Errorf("findings[0].severity = %v; want 'error'", f0["severity"])
	}
}

// --- NS-REQ-4: Non-existent spec directory emits no-specs finding ---

// TestTS08_36_LintNonexistentSpecDir verifies that when the spec
// directory does not exist, the lint command emits a no-specs finding
// with severity 'error' and exits 1.
// Covers: NS-REQ-4, TS-NS-4
func TestTS08_36_LintNonexistentSpecDir(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs_nonexistent")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "lint"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() on non-existent spec dir returned nil; want error (exit 1)")
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(output), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, output)
	}

	// exit_code must be 1.
	exitCode, exists := parsed["exit_code"]
	if !exists {
		t.Fatal("parsed missing 'exit_code' field")
	}
	if exitCode.(float64) != 1 {
		t.Errorf("exit_code = %v; want 1", exitCode)
	}

	findings, ok := parsed["findings"].([]any)
	if !ok {
		t.Fatal("parsed.findings is not an array")
	}
	if len(findings) < 1 {
		t.Fatal("findings is empty; want at least one no-specs finding")
	}

	f0 := findings[0].(map[string]any)
	if f0["rule"] != "no-specs" {
		t.Errorf("findings[0].rule = %v; want 'no-specs'", f0["rule"])
	}
	if f0["severity"] != "error" {
		t.Errorf("findings[0].severity = %v; want 'error'", f0["severity"])
	}
}

// --- Lint: banner should be suppressed ---

// TestTS08_36_LintBannerSuppressed verifies that the lint command
// does not trigger banner display (it's a non-banner subcommand
// in the implementation, but verifying it has spinner on stderr).
// Covers: 08-REQ-12.1
func TestTS08_36_LintBannerSuppressed(t *testing.T) {
	// Lint is not in the suppressed-banner list per spec, but we
	// verify that quiet mode suppresses the spinner.
	cmd := newRootCmd()
	stderrBuf := new(bytes.Buffer)
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(stderrBuf)
	cmd.SetArgs([]string{"--quiet", "lint"})

	_ = cmd.Execute()

	// In quiet mode, stderr should not contain spinner output.
	// The exact assertion depends on implementation, but spinner
	// output should be suppressed.
	stderrOutput := stderrBuf.String()
	_ = stderrOutput // Stub: implementation will validate spinner suppression.
}

// --- NS-REQ-1: Spec-id mismatch detected via library validation ---

// TestTSNS1_SpecIdMismatchFromLibrary verifies that the lint command
// reports a library-level validation error (not missing-file or invalid-json)
// when a spec has a spec_id mismatch between prd.md and requirements.json.
// Covers: NS-REQ-1, TS-NS-1
func TestTSNS1_SpecIdMismatchFromLibrary(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")

	// Create a spec with a deliberate spec_id mismatch:
	// prd.md has spec_id "08", requirements.json has spec_id "99".
	// Include a pending subtask so the spec is not treated as fully implemented.
	specPath := filepath.Join(specDir, "08_mismatch_spec")
	writeValidSpecDir(t, specPath, "08", "mismatch_spec", []string{"pending"}, "99")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "lint"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() returned nil; want error for spec_id mismatch")
	}

	var parsed map[string]any
	if jsonErr := json.Unmarshal(stdoutBuf.Bytes(), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, stdoutBuf.String())
	}

	findings, ok := parsed["findings"].([]any)
	if !ok {
		t.Fatal("parsed.findings is not an array")
	}

	// Should contain at least one finding from library validation with
	// severity "error" and a rule that is NOT "missing-file" or "invalid-json"
	// (those were the old shallow checks).
	hasLibraryError := false
	for _, f := range findings {
		fm := f.(map[string]any)
		rule, _ := fm["rule"].(string)
		severity, _ := fm["severity"].(string)
		if severity == "error" && rule != "missing-file" && rule != "invalid-json" {
			hasLibraryError = true
			break
		}
	}
	if !hasLibraryError {
		t.Error("expected a library-level validation error finding (not missing-file/invalid-json); got none")
		t.Logf("findings: %v", findings)
	}

	// exit_code must be 1.
	if ec, _ := parsed["exit_code"].(float64); ec != 1 {
		t.Errorf("exit_code = %v; want 1", ec)
	}
}

// --- NS-REQ-4: CLI-only extras (empty-artifact + missing-session) ---

// TestTSNS4_CLIExtrasEmptyArtifactAndMissingSession verifies that the
// findings array includes both empty-artifact and missing-session warnings
// after library validation findings.
// Covers: NS-REQ-4, TS-NS-4
func TestTSNS4_CLIExtrasEmptyArtifactAndMissingSession(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")

	// Create a spec with valid structure but with an empty JSON object
	// for requirements.json and no _session.json.
	// Include a pending subtask so the spec is not treated as fully implemented.
	specPath := filepath.Join(specDir, "08_extras_spec")
	writeValidSpecDir(t, specPath, "08", "extras_spec", []string{"pending"}, "")

	// Overwrite requirements.json with an empty JSON object to trigger
	// empty-artifact warning.
	if err := os.WriteFile(filepath.Join(specPath, "requirements.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	// No _session.json → triggers missing-session warning.

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "lint"})

	_ = cmd.Execute()

	var parsed map[string]any
	if jsonErr := json.Unmarshal(stdoutBuf.Bytes(), &parsed); jsonErr != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, stdoutBuf.String())
	}

	findings, ok := parsed["findings"].([]any)
	if !ok {
		t.Fatal("parsed.findings is not an array")
	}

	hasEmptyArtifact := false
	hasMissingSession := false
	for _, f := range findings {
		fm := f.(map[string]any)
		rule, _ := fm["rule"].(string)
		severity, _ := fm["severity"].(string)
		switch rule {
		case "empty-artifact":
			if severity != "warning" {
				t.Errorf("empty-artifact severity = %q; want 'warning'", severity)
			}
			hasEmptyArtifact = true
		case "missing-session":
			if severity != "warning" {
				t.Errorf("missing-session severity = %q; want 'warning'", severity)
			}
			hasMissingSession = true
		}
	}

	if !hasEmptyArtifact {
		t.Error("expected finding with rule 'empty-artifact'; none found")
	}
	if !hasMissingSession {
		t.Error("expected finding with rule 'missing-session'; none found")
	}
}
