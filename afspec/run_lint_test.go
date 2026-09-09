package afspec

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// writeLintSpecDir creates a complete spec directory with all four artifact
// files (prd.md, requirements.json, test_spec.json, tasks.json).
// subtaskStates, if non-nil, creates a task group with subtasks in those states.
// reqSpecIDOverride, if non-empty, replaces spec_id in requirements.json to
// create a spec_id mismatch that produces a guaranteed validation error.
func writeLintSpecDir(t *testing.T, dir, specID, specName string, subtaskStates []string, reqSpecIDOverride string) {
	t.Helper()
	mustMkdir(t, dir)

	effectiveReqSpecID := specID
	if reqSpecIDOverride != "" {
		effectiveReqSpecID = reqSpecIDOverride
	}

	prd := fmt.Sprintf("---\nspec_id: %q\nspec_name: %q\ntitle: %q\nstatus: \"draft\"\n"+
		"created_at: \"2026-01-01T00:00:00Z\"\nupdated_at: \"2026-01-01T00:00:00Z\"\n"+
		"owner: \"test\"\nsource: \"test\"\nsupersedes: []\nintent_hash: null\nschema_version: 1\n---\n# %s\n",
		specID, specName, specName, specName)
	mustWriteFile(t, filepath.Join(dir, "prd.md"), prd)

	req := fmt.Sprintf(
		`{"$schema":"https://agent-fox.dev/schemas/requirements.v1.json",`+
			`"spec_id":%q,"spec_name":%q,"schema_version":1,"introduction":"Test",`+
			`"glossary":{},"requirements":[],"correctness_properties":[],`+
			`"execution_paths":[],"error_handling":[]}`,
		effectiveReqSpecID, specName)
	mustWriteFile(t, filepath.Join(dir, "requirements.json"), req)

	ts := fmt.Sprintf(
		`{"$schema":"https://agent-fox.dev/schemas/test_spec.v1.json",`+
			`"spec_id":%q,"spec_name":%q,"schema_version":1,`+
			`"test_cases":[],"property_tests":[],"edge_case_tests":[],`+
			`"smoke_tests":[],"coverage":{}}`,
		specID, specName)
	mustWriteFile(t, filepath.Join(dir, "test_spec.json"), ts)

	taskGroupsJSON := "[]"
	if subtaskStates != nil {
		var subtasks []string
		for i, state := range subtaskStates {
			subtasks = append(subtasks, fmt.Sprintf(
				`{"id":"%d.%d","title":"Subtask %d","details":["d"],`+
					`"test_spec_refs":[],"requirement_refs":[],"state":%q,"optional":false}`,
				1, i+1, i+1, state))
		}
		taskGroupsJSON = fmt.Sprintf(
			`[{"id":1,"kind":"standard","title":"G1","subtasks":[%s],`+
				`"verification":{"id":"1.V","checks":["c"]}}]`,
			strings.Join(subtasks, ","))
	}
	tasks := fmt.Sprintf(
		`{"$schema":"https://agent-fox.dev/schemas/tasks.v1.json",`+
			`"spec_id":%q,"spec_name":%q,"schema_version":1,`+
			`"test_commands":{"spec_tests":"go test","all_tests":"go test","linter":"go vet"},`+
			`"dependencies":[],"task_groups":%s,"traceability":[]}`,
		specID, specName, taskGroupsJSON)
	mustWriteFile(t, filepath.Join(dir, "tasks.json"), tasks)
}

// ---------------------------------------------------------------------------
// TS-05-32: RunLintSpecs with lintAll=true discovers all specs, validates
// each, collects findings, sorts them, computes exit code, and returns a
// LintResult.
// Requirement: 05-REQ-9.1
// ---------------------------------------------------------------------------

func TestRunLintSpecs_LintAllTrue(t *testing.T) {
	defer requireImplemented(t)

	specsDir := t.TempDir()
	// alpha_spec: valid (matching spec_id)
	writeLintSpecDir(t, filepath.Join(specsDir, "05_alpha_spec"), "05", "alpha_spec", nil, "")
	// beta_spec: spec_id mismatch between prd.md ("06") and requirements.json ("99")
	writeLintSpecDir(t, filepath.Join(specsDir, "06_beta_spec"), "06", "beta_spec", nil, "99")

	result, err := RunLintSpecs(specsDir, true)
	if err != nil {
		t.Fatalf("RunLintSpecs returned unexpected error: %v", err)
	}

	if len(result.Findings) == 0 {
		t.Fatal("Findings should not be empty when a spec has a spec_id mismatch")
	}

	// Verify findings are sorted by SpecName.
	for i := 1; i < len(result.Findings); i++ {
		prev := result.Findings[i-1]
		curr := result.Findings[i]
		if prev.SpecName > curr.SpecName {
			t.Errorf("findings not sorted by SpecName: [%d]=%q > [%d]=%q",
				i-1, prev.SpecName, i, curr.SpecName)
		}
	}

	// Verify exit code matches ComputeExitCode semantics.
	hasError := false
	for _, f := range result.Findings {
		if f.Severity == "error" {
			hasError = true
			break
		}
	}
	expectedExit := 0
	if hasError {
		expectedExit = 1
	}
	if result.ExitCode != expectedExit {
		t.Errorf("ExitCode = %d, but expected %d based on findings severities",
			result.ExitCode, expectedExit)
	}
}

// ---------------------------------------------------------------------------
// TS-05-33: RunLintSpecs with lintAll=false skips specs where all subtasks
// are in state 'done' or 'dropped' and excludes them from findings.
// Requirement: 05-REQ-9.2
// ---------------------------------------------------------------------------

func TestRunLintSpecs_LintAllFalseSkipsFullyImplemented(t *testing.T) {
	defer requireImplemented(t)

	specsDir := t.TempDir()
	// my_spec: fully implemented (done + dropped), with spec_id mismatch to
	// guarantee a validation error IF it were linted.
	writeLintSpecDir(t, filepath.Join(specsDir, "05_my_spec"), "05", "my_spec",
		[]string{"done", "dropped"}, "99")
	// other_spec: not fully implemented (pending subtask), with spec_id mismatch.
	writeLintSpecDir(t, filepath.Join(specsDir, "06_other_spec"), "06", "other_spec",
		[]string{"pending"}, "88")

	result, err := RunLintSpecs(specsDir, false)
	if err != nil {
		t.Fatalf("RunLintSpecs returned unexpected error: %v", err)
	}

	// Fully-implemented spec must be absent from findings.
	for _, f := range result.Findings {
		if f.SpecName == "my_spec" {
			t.Errorf("expected my_spec absent from findings (fully implemented), got: %+v", f)
		}
	}

	// Non-fully-implemented spec must be present (it has a validation error).
	foundOther := false
	for _, f := range result.Findings {
		if f.SpecName == "other_spec" {
			foundOther = true
			break
		}
	}
	if !foundOther {
		t.Error("expected other_spec present in findings (pending subtask), but absent")
	}
}

// ---------------------------------------------------------------------------
// TS-05-34: RunLintSpecs with lintAll=false includes specs that have no
// tasks.json or have at least one subtask not in 'done' or 'dropped' state.
// Requirement: 05-REQ-9.3
// ---------------------------------------------------------------------------

func TestRunLintSpecs_LintAllFalseIncludesIncomplete(t *testing.T) {
	defer requireImplemented(t)

	specsDir := t.TempDir()

	// no_tasks: has requirements.json but deliberately no tasks.json.
	dir05 := filepath.Join(specsDir, "05_no_tasks")
	writeMinimalPRD(t, dir05, "05", "no_tasks", "draft")
	mustWriteFile(t, filepath.Join(dir05, "requirements.json"),
		`{"spec_id":"05","spec_name":"no_tasks","schema_version":1,`+
			`"introduction":"t","glossary":{},"requirements":[],`+
			`"correctness_properties":[],"execution_paths":[],"error_handling":[]}`)
	mustWriteFile(t, filepath.Join(dir05, "test_spec.json"),
		`{"spec_id":"05","spec_name":"no_tasks","schema_version":1,`+
			`"test_cases":[],"property_tests":[],"edge_case_tests":[],`+
			`"smoke_tests":[],"coverage":{}}`)
	// Deliberately no tasks.json — spec must still be linted.

	// in_progress: has a subtask in "in_progress" state, with spec_id mismatch.
	writeLintSpecDir(t, filepath.Join(specsDir, "06_in_progress"), "06", "in_progress",
		[]string{"in_progress"}, "88")

	result, err := RunLintSpecs(specsDir, false)
	if err != nil {
		t.Fatalf("RunLintSpecs returned unexpected error: %v", err)
	}

	// no_tasks spec should be linted (may produce a load-failure finding if
	// LoadSpec requires tasks.json, or validation findings).
	foundNoTasks := false
	for _, f := range result.Findings {
		if f.SpecName == "no_tasks" {
			foundNoTasks = true
			break
		}
	}
	if !foundNoTasks {
		t.Error("expected no_tasks spec in findings (load-failure or validation), but absent")
	}

	// in_progress spec should be linted (has validation error from mismatch).
	foundInProgress := false
	for _, f := range result.Findings {
		if f.SpecName == "in_progress" {
			foundInProgress = true
			break
		}
	}
	if !foundInProgress {
		t.Error("expected in_progress spec in findings, but absent")
	}
}

// ---------------------------------------------------------------------------
// TS-05-58: RunLintSpecs propagates the error from DiscoverLintSpecs to the
// caller without returning a partial LintResult.
// Requirement: 05-REQ-9.E1
// ---------------------------------------------------------------------------

func TestRunLintSpecs_NonexistentDir(t *testing.T) {
	defer requireImplemented(t)

	result, err := RunLintSpecs("/nonexistent/specs/path", true)
	if err == nil {
		t.Fatal("RunLintSpecs with nonexistent dir returned nil error, want non-nil")
	}

	if len(result.Findings) != 0 {
		t.Errorf("Findings should be empty on error, got %d entries", len(result.Findings))
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 for error return", result.ExitCode)
	}
}

// ---------------------------------------------------------------------------
// TS-05-59: RunLintSpecs records a LintFinding with Severity 'error' and
// Rule 'load-failure' for a spec where LoadSpec fails, and continues
// processing remaining specs.
// Requirement: 05-REQ-9.E2
// ---------------------------------------------------------------------------

func TestRunLintSpecs_LoadFailure(t *testing.T) {
	defer requireImplemented(t)

	specsDir := t.TempDir()

	// bad_spec: has prd.md but malformed requirements.json — LoadSpec will fail.
	dir05 := filepath.Join(specsDir, "05_bad_spec")
	writeMinimalPRD(t, dir05, "05", "bad_spec", "draft")
	mustWriteFile(t, filepath.Join(dir05, "requirements.json"), "{{not valid json}}")
	mustWriteFile(t, filepath.Join(dir05, "test_spec.json"),
		`{"spec_id":"05","spec_name":"bad_spec","schema_version":1,`+
			`"test_cases":[],"property_tests":[],"edge_case_tests":[],`+
			`"smoke_tests":[],"coverage":{}}`)
	mustWriteFile(t, filepath.Join(dir05, "tasks.json"),
		`{"spec_id":"05","spec_name":"bad_spec","schema_version":1,`+
			`"test_commands":{"spec_tests":"t","all_tests":"t","linter":"t"},`+
			`"dependencies":[],"task_groups":[],"traceability":[]}`)

	// good_spec: valid.
	writeLintSpecDir(t, filepath.Join(specsDir, "06_good_spec"), "06", "good_spec", nil, "")

	result, err := RunLintSpecs(specsDir, true)
	if err != nil {
		t.Fatalf("RunLintSpecs returned unexpected error: %v", err)
	}

	// Expect a load-failure finding for bad_spec.
	foundLoadFailure := false
	for _, f := range result.Findings {
		if f.Rule == "load-failure" && f.Severity == "error" && f.SpecName == "bad_spec" {
			foundLoadFailure = true
			break
		}
	}
	if !foundLoadFailure {
		t.Error("expected load-failure finding with Severity 'error' for bad_spec, but none found")
	}

	if len(result.Findings) < 1 {
		t.Error("expected at least one finding (load-failure), got none")
	}
}

// ---------------------------------------------------------------------------
// TS-05-60: RunLintSpecs never calls os.Exit directly; all errors are
// returned to the caller via the error return value.
// Requirement: 05-REQ-9.E3
// ---------------------------------------------------------------------------

func TestRunLintSpecs_NoOsExit(t *testing.T) {
	defer requireImplemented(t)

	// If RunLintSpecs called os.Exit, this test process would terminate.
	// The fact that the test continues after the call proves no os.Exit.
	_, err := RunLintSpecs("/nonexistent/specs", true)
	if err == nil {
		t.Fatal("RunLintSpecs with nonexistent dir returned nil error, want non-nil")
	}
	// If we reach here, os.Exit was not called.
}
