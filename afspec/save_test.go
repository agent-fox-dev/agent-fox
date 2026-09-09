package afspec

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestSave_RoundTrip verifies that Spec.Save writes all artifacts atomically
// and produces byte-for-byte identical output to the original fixture files.
// Test Spec: TS-01-4, Requirement: 01-REQ-2.1
func TestSave_RoundTrip(t *testing.T) {
	defer requireImplemented(t)

	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	tmpDir := t.TempDir()

	if err := spec.Save(tmpDir); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Check all artifact files are byte-for-byte identical
	artifactFiles := []string{
		"prd.md",
		"requirements.json",
		"test_spec.json",
		"tasks.json",
	}

	for _, name := range artifactFiles {
		t.Run(name, func(t *testing.T) {
			expected, err := os.ReadFile(filepath.Join("./../testdata/valid_spec", name))
			if err != nil {
				t.Fatalf("failed to read expected file: %v", err)
			}

			actual, err := os.ReadFile(filepath.Join(tmpDir, name))
			if err != nil {
				t.Fatalf("failed to read actual file: %v", err)
			}

			if diff := cmp.Diff(string(expected), string(actual)); diff != "" {
				t.Errorf("byte mismatch for %s (-want +got):\n%s", name, diff)
			}
		})
	}

	// Verify no temp files remain (temp files are named "<artifact>.tmp.<random>")
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to read tmpDir: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp.") {
			t.Errorf("temp file %q was not cleaned up after successful save", entry.Name())
		}
	}
}

// TestSave_SealedSpec verifies that Spec.Save on a sealed Spec returns
// a LifecycleError without writing any files.
// Test Spec: TS-01-5, Requirement: 01-REQ-2.2
func TestSave_SealedSpec(t *testing.T) {
	defer requireImplemented(t)

	spec := &Spec{
		SpecID:        "01",
		SpecName:      "test_feature",
		Title:         "Test Feature",
		Status:        "sealed",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test-author",
		Source:        "https://github.com/test/repo/issues/1",
		SchemaVersion: 1,
		PRDBody:       "# Test Feature\n",
	}

	tmpDir := t.TempDir()

	err := spec.Save(tmpDir)
	if err == nil {
		t.Fatal("expected error when saving sealed spec, got nil")
	}

	var lifecycleErr *LifecycleError
	if !errors.As(err, &lifecycleErr) {
		t.Errorf("expected errors.As(err, &LifecycleError{}) to return true, got false; err type = %T", err)
	}

	var specErr *SpecError
	if !errors.As(err, &specErr) {
		t.Errorf("expected errors.As(err, &SpecError{}) to return true, got false; err type = %T", err)
	}

	// Verify no files were created
	entries, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("failed to read tmpDir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Errorf("expected no files in tmpDir for sealed spec, got %d files", len(entries))
	}
}

// TestSave_NonexistentDir verifies that Spec.Save returns a SaveError
// when the target directory does not exist.
// Requirement: 01-REQ-2.E3
func TestSave_NonexistentDir(t *testing.T) {
	defer requireImplemented(t)

	spec := &Spec{
		SpecID:        "01",
		SpecName:      "test_feature",
		Title:         "Test Feature",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test-author",
		Source:        "https://github.com/test/repo/issues/1",
		SchemaVersion: 1,
		PRDBody:       "# Test Feature\n",
	}

	err := spec.Save("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for nonexistent directory, got nil")
	}

	var saveErr *SaveError
	if !errors.As(err, &saveErr) {
		t.Errorf("expected errors.As(err, &SaveError{}) to return true, got false; err type = %T", err)
	}

	var specErr *SpecError
	if !errors.As(err, &specErr) {
		t.Errorf("expected errors.As(err, &SpecError{}) to return true, got false; err type = %T", err)
	}
}

// TestSave_ErrorTypes verifies that save failures produce the correct
// error types wrapping SpecError.
// Requirement: 01-REQ-26.1, 01-REQ-26.2
func TestSave_ErrorTypes(t *testing.T) {
	tests := []struct {
		name   string
		spec   *Spec
		dir    string
		errTyp string // "LifecycleError" or "SaveError"
	}{
		{
			name: "sealed spec",
			spec: &Spec{
				Status:        "sealed",
				SpecID:        "01",
				SpecName:      "test",
				SchemaVersion: 1,
			},
			dir:    t.TempDir(),
			errTyp: "LifecycleError",
		},
		{
			name: "nonexistent directory",
			spec: &Spec{
				Status:        "draft",
				SpecID:        "01",
				SpecName:      "test",
				SchemaVersion: 1,
			},
			dir:    "/nonexistent/dir",
			errTyp: "SaveError",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer requireImplemented(t)

			err := tt.spec.Save(tt.dir)
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			// All save errors should be unwrappable to SpecError
			var specErr *SpecError
			if !errors.As(err, &specErr) {
				t.Errorf("errors.As(err, &SpecError{}) = false, want true")
			}

			switch tt.errTyp {
			case "LifecycleError":
				var le *LifecycleError
				if !errors.As(err, &le) {
					t.Errorf("errors.As(err, &LifecycleError{}) = false, want true")
				}
			case "SaveError":
				var se *SaveError
				if !errors.As(err, &se) {
					t.Errorf("errors.As(err, &SaveError{}) = false, want true")
				}
			}
		})
	}
}

// TestSave_TwoPhase_WriteFailure verifies that when temp-file creation fails
// (write phase), no on-disk artifact is modified and no temp files remain
// (NS-REQ-2, NS-REQ-3).
//
// The test makes the target directory read-only so that os.CreateTemp fails
// immediately, exercising the "write phase fails → no renames performed"
// invariant.
func TestSave_TwoPhase_WriteFailure(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("cannot test read-only directory restrictions as root")
	}

	defer requireImplemented(t)

	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	fixtureDir := "./../testdata/valid_spec"
	artifactNames := []string{"prd.md", "requirements.json", "test_spec.json", "tasks.json"}

	// Populate a writable temp dir with the fixture files and record originals.
	tmpDir := t.TempDir()
	origContents := make(map[string][]byte, len(artifactNames))
	for _, name := range artifactNames {
		data, err := os.ReadFile(filepath.Join(fixtureDir, name))
		if err != nil {
			t.Fatalf("failed to read fixture %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, name), data, 0644); err != nil {
			t.Fatalf("failed to copy fixture %s: %v", name, err)
		}
		origContents[name] = data
	}

	// Make the directory read-only: os.CreateTemp will fail, so the write
	// phase cannot start and no rename will occur.
	if err := os.Chmod(tmpDir, 0555); err != nil {
		t.Fatalf("chmod 0555 failed: %v", err)
	}
	// Restore write permission so t.TempDir cleanup can remove the directory.
	t.Cleanup(func() { os.Chmod(tmpDir, 0755) }) //nolint:errcheck

	saveErr := spec.saveToDisk(tmpDir)
	if saveErr == nil {
		t.Fatal("expected saveToDisk to return an error for a read-only directory, got nil")
	}

	var se *SaveError
	if !errors.As(saveErr, &se) {
		t.Errorf("expected *SaveError, got %T: %v", saveErr, saveErr)
	}

	// Restore permissions for inspection.
	if err := os.Chmod(tmpDir, 0755); err != nil {
		t.Fatalf("failed to restore dir permissions: %v", err)
	}

	// NS-REQ-3: no orphaned temp files.
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp.") {
			t.Errorf("orphaned temp file %q found after write failure", entry.Name())
		}
	}

	// NS-REQ-2: every original artifact file must be byte-for-byte unchanged.
	for name, original := range origContents {
		actual, err := os.ReadFile(filepath.Join(tmpDir, name))
		if err != nil {
			t.Fatalf("cannot read %s after failed save: %v", name, err)
		}
		if !bytes.Equal(original, actual) {
			t.Errorf("artifact %s was modified by a failed save", name)
		}
	}
}

// TestSave_TwoPhase_NoOrphanedTempsOnSuccess confirms that no *.tmp.* files
// remain after a fully successful two-phase save (NS-REQ-3 success path).
// This supplements TestSave_RoundTrip with an explicit naming-pattern check.
func TestSave_TwoPhase_NoOrphanedTempsOnSuccess(t *testing.T) {
	defer requireImplemented(t)

	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	tmpDir := t.TempDir()
	if err := spec.Save(tmpDir); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp.") {
			t.Errorf("orphaned temp file %q found after successful save", entry.Name())
		}
	}
}

// buildMinimalSpec constructs a minimal Spec containing one requirement with
// one acceptance criterion and one test case that references that criterion.
// It is used across multiple Save coverage tests.
func buildMinimalSpec(reqID, criterionID string) *Spec {
	const (
		reqSchemaRef   = "https://agent-fox.dev/schemas/requirements.v1.json"
		schemaRef      = "https://agent-fox.dev/schemas/test_spec.v1.json"
		tasksSchemaRef = "https://agent-fox.dev/schemas/tasks.v1.json"
	)

	return &Spec{
		SpecID:        "99",
		SpecName:      "coverage_test",
		Title:         "Coverage Test",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test-author",
		Source:        "https://github.com/test/repo/issues/99",
		SchemaVersion: 1,
		PRDBody:       "# Coverage Test\n",
		Requirements: &RequirementsV1Json{
			Schema:        reqSchemaRef,
			SchemaVersion: 1,
			SpecId:        "99",
			SpecName:      "coverage_test",
			Introduction:  "Coverage test spec.",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements: []Requirement{
				{
					Id:    reqID,
					Title: "Test Requirement",
					UserStory: UserStory{
						Role:    "developer",
						Goal:    "verify coverage",
						Benefit: "correct coverage data",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          criterionID,
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "behave correctly",
						},
					},
					EdgeCases: []Criterion{},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			Schema:        schemaRef,
			SchemaVersion: 1,
			SpecId:        "99",
			SpecName:      "coverage_test",
			TestCases: []TestCase{
				{
					Id:                  "TS-99-1",
					RequirementId:       criterionID,
					Kind:                TestCaseKindUnit,
					Description:         "Covers the requirement",
					Preconditions:       []string{},
					Expected:            "ok",
					AssertionPseudocode: "assert true",
				},
			},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			Schema:        tasksSchemaRef,
			SchemaVersion: 1,
			SpecId:        "99",
			SpecName:      "coverage_test",
			TestCommands:  TestCommands{},
			Dependencies:  []TaskDependency{},
			TaskGroups:    []TaskGroup{},
			Traceability:  []TraceabilityEntry{},
		},
	}
}

// TestSave_CoveragePopulated verifies that after calling Save on a spec with
// requirements and test cases, the persisted test_spec.json contains a
// populated coverage object.
// Requirements: NS-REQ-1, NS-REQ-2; Test Spec: TS-NS-1, TS-NS-2
func TestSave_CoveragePopulated(t *testing.T) {
	defer requireImplemented(t)

	reqID := "99-REQ-1"
	criterionID := "99-REQ-1.1"
	spec := buildMinimalSpec(reqID, criterionID)

	tmpDir := t.TempDir()
	if err := spec.Save(tmpDir); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Read and unmarshal the persisted test_spec.json.
	data, err := os.ReadFile(filepath.Join(tmpDir, "test_spec.json"))
	if err != nil {
		t.Fatalf("failed to read test_spec.json: %v", err)
	}
	var ts TestSpecV1Json
	if err := json.Unmarshal(data, &ts); err != nil {
		t.Fatalf("failed to unmarshal test_spec.json: %v", err)
	}

	// NS-REQ-5.1: requirements_covered must include the criterion ID, not the
	// parent requirement ID.
	if diff := cmp.Diff([]string{criterionID}, ts.Coverage.RequirementsCovered); diff != "" {
		t.Errorf("RequirementsCovered mismatch (-want +got):\n%s", diff)
	}

	// properties_covered and paths_covered are empty; gaps is empty.
	if diff := cmp.Diff([]string{}, ts.Coverage.PropertiesCovered); diff != "" {
		t.Errorf("PropertiesCovered mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{}, ts.Coverage.PathsCovered); diff != "" {
		t.Errorf("PathsCovered mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{}, ts.Coverage.Gaps); diff != "" {
		t.Errorf("Gaps mismatch (-want +got):\n%s", diff)
	}

	// The parent requirement ID must not appear anywhere — only criterion IDs.
	for _, id := range append(ts.Coverage.RequirementsCovered, ts.Coverage.Gaps...) {
		if id == reqID {
			t.Errorf("parent requirement ID %q must not appear in coverage; only criterion IDs", reqID)
		}
	}
}

// TestSave_CoverageAllFields verifies that requirements, properties, and paths
// are correctly separated into their respective coverage fields.
// Requirements: NS-REQ-2; Test Spec: TS-NS-2
func TestSave_CoverageAllFields(t *testing.T) {
	defer requireImplemented(t)

	const (
		reqSchemaRef   = "https://agent-fox.dev/schemas/requirements.v1.json"
		schemaRef      = "https://agent-fox.dev/schemas/test_spec.v1.json"
		tasksSchemaRef = "https://agent-fox.dev/schemas/tasks.v1.json"
	)

	spec := &Spec{
		SpecID:        "99",
		SpecName:      "coverage_all",
		Title:         "Coverage All Fields",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test-author",
		Source:        "https://github.com/test/repo/issues/99",
		SchemaVersion: 1,
		PRDBody:       "# Coverage All\n",
		Requirements: &RequirementsV1Json{
			Schema:        reqSchemaRef,
			SchemaVersion: 1,
			SpecId:        "99",
			SpecName:      "coverage_all",
			Introduction:  "All-fields coverage test.",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements: []Requirement{
				{
					Id:    "99-REQ-1",
					Title: "The Requirement",
					UserStory: UserStory{
						Role:    "dev",
						Goal:    "coverage",
						Benefit: "accuracy",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "99-REQ-1.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "sys",
							Action:      "act",
						},
					},
					EdgeCases: []Criterion{},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{
				{
					Id:        "99-PROP-1",
					Title:     "The Property",
					ForAny:    "any input",
					Invariant: "invariant holds",
					Validates: []string{"99-REQ-1.1"},
				},
			},
			ExecutionPaths: []ExecutionPath{
				{
					Id:    "99-PATH-1",
					Title: "The Path",
					Steps: []PathStep{
						{Actor: "caller", Action: "invokes"},
						{Actor: "system", Action: "responds"},
					},
				},
			},
			ErrorHandling: []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			Schema:        schemaRef,
			SchemaVersion: 1,
			SpecId:        "99",
			SpecName:      "coverage_all",
			TestCases: []TestCase{
				{
					Id:                  "TS-99-1",
					RequirementId:       "99-REQ-1.1",
					Kind:                TestCaseKindUnit,
					Description:         "Covers the requirement",
					Preconditions:       []string{},
					Expected:            "ok",
					AssertionPseudocode: "assert true",
				},
			},
			PropertyTests: []PropertyTest{
				{
					Id:             "TS-99-P1",
					PropertyId:     "99-PROP-1",
					Validates:      []string{"99-REQ-1.1"},
					Description:    "Property test",
					ForAnyStrategy: "any strategy",
					InvariantCheck: "check",
				},
			},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests: []SmokeTest{
				{
					Id:              "TS-99-SMOKE-1",
					ExecutionPathId: "99-PATH-1",
					Description:     "Smoke test",
					Trigger:         "invoke",
					RealComponents:  []string{"core"},
					Mockable:        []string{},
					ExpectedEffects: []string{"responds"},
				},
			},
			Coverage: Coverage{},
		},
		Tasks: &TasksV1Json{
			Schema:        tasksSchemaRef,
			SchemaVersion: 1,
			SpecId:        "99",
			SpecName:      "coverage_all",
			TestCommands:  TestCommands{},
			Dependencies:  []TaskDependency{},
			TaskGroups:    []TaskGroup{},
			Traceability:  []TraceabilityEntry{},
		},
	}

	tmpDir := t.TempDir()
	if err := spec.Save(tmpDir); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "test_spec.json"))
	if err != nil {
		t.Fatalf("failed to read test_spec.json: %v", err)
	}
	var ts TestSpecV1Json
	if err := json.Unmarshal(data, &ts); err != nil {
		t.Fatalf("failed to unmarshal test_spec.json: %v", err)
	}

	// Each field has exactly the right IDs, nothing mixed up.
	// RequirementsCovered must contain the criterion ID, not the parent req ID.
	if diff := cmp.Diff([]string{"99-REQ-1.1"}, ts.Coverage.RequirementsCovered); diff != "" {
		t.Errorf("RequirementsCovered mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"99-PROP-1"}, ts.Coverage.PropertiesCovered); diff != "" {
		t.Errorf("PropertiesCovered mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"99-PATH-1"}, ts.Coverage.PathsCovered); diff != "" {
		t.Errorf("PathsCovered mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{}, ts.Coverage.Gaps); diff != "" {
		t.Errorf("Gaps mismatch (-want +got):\n%s", diff)
	}
}

// TestSave_CoverageNoTestCases verifies that Save on a spec with no test cases
// does not panic and produces a coverage object where all requirement IDs
// appear in gaps.
// Requirements: NS-REQ-3; Test Spec: TS-NS-3
func TestSave_CoverageNoTestCases(t *testing.T) {
	defer requireImplemented(t)

	const (
		reqSchemaRef   = "https://agent-fox.dev/schemas/requirements.v1.json"
		schemaRef      = "https://agent-fox.dev/schemas/test_spec.v1.json"
		tasksSchemaRef = "https://agent-fox.dev/schemas/tasks.v1.json"
	)

	spec := &Spec{
		SpecID:        "99",
		SpecName:      "coverage_empty",
		Title:         "Coverage Empty",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test-author",
		Source:        "https://github.com/test/repo/issues/99",
		SchemaVersion: 1,
		PRDBody:       "# Coverage Empty\n",
		Requirements: &RequirementsV1Json{
			Schema:        reqSchemaRef,
			SchemaVersion: 1,
			SpecId:        "99",
			SpecName:      "coverage_empty",
			Introduction:  "Empty test cases.",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements: []Requirement{
				{
					Id:    "99-REQ-1",
					Title: "The Requirement",
					UserStory: UserStory{
						Role:    "dev",
						Goal:    "gap",
						Benefit: "visibility",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "99-REQ-1.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "sys",
							Action:      "act",
						},
					},
					EdgeCases: []Criterion{},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			Schema:        schemaRef,
			SchemaVersion: 1,
			SpecId:        "99",
			SpecName:      "coverage_empty",
			TestCases:     []TestCase{},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			Schema:        tasksSchemaRef,
			SchemaVersion: 1,
			SpecId:        "99",
			SpecName:      "coverage_empty",
			TestCommands:  TestCommands{},
			Dependencies:  []TaskDependency{},
			TaskGroups:    []TaskGroup{},
			Traceability:  []TraceabilityEntry{},
		},
	}

	tmpDir := t.TempDir()
	// NS-REQ-3: must not panic; Save must return nil.
	err := spec.Save(tmpDir)
	if err != nil {
		t.Fatalf("Save returned unexpected error: %v", err)
	}

	data, readErr := os.ReadFile(filepath.Join(tmpDir, "test_spec.json"))
	if readErr != nil {
		t.Fatalf("failed to read test_spec.json: %v", readErr)
	}
	var ts TestSpecV1Json
	if jsonErr := json.Unmarshal(data, &ts); jsonErr != nil {
		t.Fatalf("failed to unmarshal test_spec.json: %v", jsonErr)
	}

	// NS-REQ-3.1: coverage.gaps contains the criterion ID (not the parent req ID).
	found := false
	for _, g := range ts.Coverage.Gaps {
		if g == "99-REQ-1.1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected gaps to contain %q, got %v", "99-REQ-1.1", ts.Coverage.Gaps)
	}

	// Parent requirement ID must not appear in gaps.
	for _, g := range ts.Coverage.Gaps {
		if g == "99-REQ-1" {
			t.Errorf("parent requirement ID %q must not appear in gaps; only criterion IDs", "99-REQ-1")
		}
	}

	// requirements_covered, properties_covered, paths_covered must be empty.
	if len(ts.Coverage.RequirementsCovered) != 0 {
		t.Errorf("expected RequirementsCovered to be empty, got %v", ts.Coverage.RequirementsCovered)
	}
	if len(ts.Coverage.PropertiesCovered) != 0 {
		t.Errorf("expected PropertiesCovered to be empty, got %v", ts.Coverage.PropertiesCovered)
	}
	if len(ts.Coverage.PathsCovered) != 0 {
		t.Errorf("expected PathsCovered to be empty, got %v", ts.Coverage.PathsCovered)
	}
}

// ---------------------------------------------------------------------------
// Issue #64 — immutable-field enforcement on active specs
// ---------------------------------------------------------------------------

// createActiveSpecFromDisk creates a draft spec, transitions it to active
// (persisting to disk), then reloads it with LoadSpec so that the returned
// Spec has the immutable snapshot captured by LoadSpec.
// Requirement: NS-REQ-1, NS-REQ-2, NS-REQ-3; Test Spec: TS-NS-1, TS-NS-2, TS-NS-3
func createActiveSpecFromDisk(t *testing.T, dir string) *Spec {
	t.Helper()

	draft := createDraftSpec(t, dir)

	_, err := draft.Transition("active", dir)
	if err != nil {
		t.Fatalf("Transition draft→active failed: %v", err)
	}

	// Reload so LoadSpec captures the immutable snapshot.
	active, err := LoadSpec(dir)
	if err != nil {
		t.Fatalf("LoadSpec after activation failed: %v", err)
	}
	if active.Status != "active" {
		t.Fatalf("expected active status after reload, got %q", active.Status)
	}
	return active
}

// TestSave_ActiveSpec_RejectsCreatedAtMutation verifies that Save returns a
// LifecycleError (also unwrappable to SpecError) when created_at is mutated on
// an active spec loaded via LoadSpec, without writing any file.
// Test Spec: TS-NS-1; Requirement: NS-REQ-1
func TestSave_ActiveSpec_RejectsCreatedAtMutation(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()
	spec := createActiveSpecFromDisk(t, tmpDir)

	spec.CreatedAt = "2099-01-01T00:00:00Z"

	err := spec.Save(tmpDir)
	if err == nil {
		t.Fatal("expected error when mutating created_at on active spec, got nil")
	}

	var le *LifecycleError
	if !errors.As(err, &le) {
		t.Errorf("errors.As(err, &LifecycleError{}) = false, want true; err = %v (type %T)", err, err)
	}

	var se *SpecError
	if !errors.As(err, &se) {
		t.Errorf("errors.As(err, &SpecError{}) = false, want true; err = %v (type %T)", err, err)
	}
}

// TestSave_ActiveSpec_RejectsSpecIDMutation verifies that Save returns a
// LifecycleError when spec_id is mutated on an active spec loaded via LoadSpec.
// Test Spec: TS-NS-2; Requirement: NS-REQ-2
func TestSave_ActiveSpec_RejectsSpecIDMutation(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()
	spec := createActiveSpecFromDisk(t, tmpDir)

	spec.SpecID = "99"

	err := spec.Save(tmpDir)
	if err == nil {
		t.Fatal("expected error when mutating spec_id on active spec, got nil")
	}

	var le *LifecycleError
	if !errors.As(err, &le) {
		t.Errorf("errors.As(err, &LifecycleError{}) = false, want true; err = %v (type %T)", err, err)
	}

	var se *SpecError
	if !errors.As(err, &se) {
		t.Errorf("errors.As(err, &SpecError{}) = false, want true; err = %v (type %T)", err, err)
	}
}

// TestSave_ActiveSpec_RejectsSpecNameMutation verifies that Save returns a
// LifecycleError when spec_name is mutated on an active spec loaded via LoadSpec.
// Test Spec: TS-NS-3; Requirement: NS-REQ-3
func TestSave_ActiveSpec_RejectsSpecNameMutation(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()
	spec := createActiveSpecFromDisk(t, tmpDir)

	spec.SpecName = "other_feature"

	err := spec.Save(tmpDir)
	if err == nil {
		t.Fatal("expected error when mutating spec_name on active spec, got nil")
	}

	var le *LifecycleError
	if !errors.As(err, &le) {
		t.Errorf("errors.As(err, &LifecycleError{}) = false, want true; err = %v (type %T)", err, err)
	}

	var se *SpecError
	if !errors.As(err, &se) {
		t.Errorf("errors.As(err, &SpecError{}) = false, want true; err = %v (type %T)", err, err)
	}
}

// TestSave_DraftSpec_AllowsCreatedAtMutation verifies that Save succeeds and
// persists the new value when created_at is mutated on a draft spec.
// Immutability applies only after activation.
// Test Spec: TS-NS-4; Requirement: NS-REQ-4
func TestSave_DraftSpec_AllowsCreatedAtMutation(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()
	spec := createDraftSpec(t, tmpDir)

	const newDate = "2099-01-01T00:00:00Z"
	spec.CreatedAt = newDate

	if err := spec.Save(tmpDir); err != nil {
		t.Fatalf("Save returned unexpected error for draft spec: %v", err)
	}

	// Verify the new value was actually written to disk.
	saved, err := os.ReadFile(filepath.Join(tmpDir, "prd.md"))
	if err != nil {
		t.Fatalf("failed to read prd.md after save: %v", err)
	}
	if !strings.Contains(string(saved), newDate) {
		t.Errorf("persisted prd.md does not contain new created_at %q", newDate)
	}
}

// TestSave_Transition_CapturesSnapshot verifies that a Spec returned by
// Transition("active") has its immutable snapshot set, so subsequent Save
// calls detect mutations without requiring a round-trip through LoadSpec.
// Test Spec: TS-NS-5; Requirement: NS-REQ-5
func TestSave_Transition_CapturesSnapshot(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()
	draft := createDraftSpec(t, tmpDir)

	active, err := draft.Transition("active", tmpDir)
	if err != nil {
		t.Fatalf("Transition draft→active failed: %v", err)
	}

	// Mutate spec_id on the Spec returned directly by Transition.
	active.SpecID = "99"

	err = active.Save(tmpDir)
	if err == nil {
		t.Fatal("expected LifecycleError after mutating spec_id on Transition result, got nil")
	}

	var le *LifecycleError
	if !errors.As(err, &le) {
		t.Errorf("errors.As(err, &LifecycleError{}) = false, want true; err = %v (type %T)", err, err)
	}
}

// TestSave_CoverageNilTestSpec verifies that calling Save when TestSpec is nil
// does not cause a nil-pointer dereference from the coverage computation guard.
// Requirements: NS-REQ-4; Test Spec: TS-NS-4
func TestSave_CoverageNilTestSpec(t *testing.T) {
	defer requireImplemented(t)

	spec := &Spec{
		SpecID:        "99",
		SpecName:      "nil_testspec",
		Title:         "Nil TestSpec",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test-author",
		Source:        "https://github.com/test/repo/issues/99",
		SchemaVersion: 1,
		PRDBody:       "# Nil TestSpec\n",
		// TestSpec intentionally nil; Requirements is also nil.
	}

	tmpDir := t.TempDir()

	// NS-REQ-4.1: must not panic. The guard `if s.TestSpec != nil && s.Requirements != nil`
	// in Save prevents ComputeCoverageStruct from being called on nil receivers.
	// MarshalJSON handles nil pointers by writing "null", so Save may succeed or
	// return a SaveError — either outcome is acceptable. A nil-pointer panic is not.
	err := spec.Save(tmpDir)
	if err != nil {
		var saveErr *SaveError
		if !errors.As(err, &saveErr) {
			t.Errorf("expected nil or *SaveError, got %T: %v", err, err)
		}
	}
	// If we reach here without panicking, the guard is working correctly.
}
