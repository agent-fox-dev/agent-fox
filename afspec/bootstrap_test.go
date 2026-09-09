package afspec

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 4.4: BootstrapSpec and Finalize
// ---------------------------------------------------------------------------

// buildValidBootstrapArtifacts returns valid artifacts suitable for a
// BootstrapSpec that should pass validation.
func buildValidBootstrapArtifacts() (*RequirementsV1Json, *TestSpecV1Json, *TasksV1Json, string) {
	req := &RequirementsV1Json{
		Schema:        "https://agent-fox.dev/schemas/requirements.v1.json",
		SchemaVersion: 1,
		SpecId:        "01",
		SpecName:      "my_spec",
		Introduction:  "Test introduction",
		Glossary:      RequirementsV1JsonGlossary{"spec": "A package"},
		Requirements: []Requirement{
			{
				Id:    "01-REQ-1",
				Title: "First Requirement",
				UserStory: UserStory{
					Role: "developer", Goal: "have models", Benefit: "type safety",
				},
				AcceptanceCriteria: []Criterion{
					{
						Id:          "01-REQ-1.1",
						EarsPattern: CriterionEarsPatternUbiquitous,
						System:      "the system",
						Action:      "returns value",
					},
				},
				EdgeCases: []Criterion{},
			},
		},
		CorrectnessProperties: []CorrectnessProperty{},
		ExecutionPaths: []ExecutionPath{
			{
				Id:    "01-PATH-1",
				Title: "Main path",
				Steps: []PathStep{
					{Actor: "caller", Action: "calls function"},
					{Actor: "system", Action: "returns result"},
				},
			},
		},
		ErrorHandling: []ErrorHandlingEntry{},
	}

	testSpec := &TestSpecV1Json{
		Schema:        "https://agent-fox.dev/schemas/test_spec.v1.json",
		SchemaVersion: 1,
		SpecId:        "01",
		SpecName:      "my_spec",
		TestCases: []TestCase{
			{
				Id:                  "TS-01-1",
				RequirementId:       "01-REQ-1.1",
				Kind:                TestCaseKindUnit,
				Description:         "Test case",
				Preconditions:       []string{},
				Expected:            "result",
				AssertionPseudocode: "assert true",
			},
		},
		PropertyTests: []PropertyTest{},
		EdgeCaseTests: []EdgeCaseTest{},
		SmokeTests: []SmokeTest{
			{
				Id:              "TS-01-SMOKE-1",
				ExecutionPathId: "01-PATH-1",
				Description:     "Smoke test",
				Trigger:         "call function",
				RealComponents:  []string{"system"},
				Mockable:        []string{},
				ExpectedEffects: []string{"returns result"},
			},
		},
		Coverage: Coverage{
			RequirementsCovered: []string{"01-REQ-1.1"},
			PropertiesCovered:   []string{},
			PathsCovered:        []string{"01-PATH-1"},
		},
	}

	tasks := &TasksV1Json{
		Schema:        "https://agent-fox.dev/schemas/tasks.v1.json",
		SchemaVersion: 1,
		SpecId:        "01",
		SpecName:      "my_spec",
		TaskGroups: []TaskGroup{
			{
				Id:    1,
				Kind:  TaskGroupKindTests,
				Title: "Write tests",
				Subtasks: []Subtask{
					{
						Id:              "1.1",
						Title:           "Write unit tests",
						Details:         []string{"Write tests for the feature"},
						TestSpecRefs:    []string{"TS-01-1"},
						RequirementRefs: []string{"01-REQ-1.1"},
						State:           SubtaskStatePending,
						Optional:        false,
					},
				},
				Verification: VerificationSubtask{Id: "1.V", Checks: []string{"Tests pass"}},
			},
			{
				Id:    2,
				Kind:  TaskGroupKindWiringVerification,
				Title: "Wiring verification",
				Subtasks: []Subtask{
					{
						Id:              "2.1",
						Title:           "Stub and dead-code audit",
						Details:         []string{"Verify no stubs remain"},
						TestSpecRefs:    []string{"TS-01-SMOKE-1"},
						RequirementRefs: []string{"01-REQ-1.1"},
						State:           SubtaskStatePending,
						Optional:        false,
					},
				},
				Verification: VerificationSubtask{Id: "2.V", Checks: []string{"All smoke tests pass"}},
			},
		},
		Dependencies: []TaskDependency{},
		TestCommands: TestCommands{AllTests: "go test", SpecTests: "go test", Linter: "go vet"},
		Traceability: []TraceabilityEntry{
			{
				RequirementId: "01-REQ-1.1",
				TestSpecId:    "TS-01-1",
				TaskId:        "1.1",
			},
		},
	}

	prdBody := "# My Spec\n\n## Intent\n\nBuild a test spec.\n\n## Goals\n\n- Test goal\n"

	return req, testSpec, tasks, prdBody
}

// TestNewBootstrapSpec verifies that NewBootstrapSpec returns a BootstrapSpec
// with specID and specName set and all artifact fields nil/zero.
// Test Spec: TS-01-42, Requirement: 01-REQ-21.1
func TestNewBootstrapSpec(t *testing.T) {
	defer requireImplemented(t)

	bs := NewBootstrapSpec("01", "my_spec")
	if bs == nil {
		t.Fatal("NewBootstrapSpec returned nil")
	}
	if bs.SpecID != "01" {
		t.Errorf("SpecID = %q, want %q", bs.SpecID, "01")
	}
	if bs.SpecName != "my_spec" {
		t.Errorf("SpecName = %q, want %q", bs.SpecName, "my_spec")
	}
	if bs.Requirements != nil {
		t.Error("expected Requirements to be nil")
	}
	if bs.TestSpec != nil {
		t.Error("expected TestSpec to be nil")
	}
	if bs.Tasks != nil {
		t.Error("expected Tasks to be nil")
	}
	if bs.PRDBody != "" {
		t.Errorf("PRDBody = %q, want empty string", bs.PRDBody)
	}
}

// TestBootstrapSpec_Finalize_AllArtifacts verifies that Finalize with all
// required artifacts set assembles a Spec and returns (*Spec, nil).
// Test Spec: TS-01-43, Requirement: 01-REQ-21.2
func TestBootstrapSpec_Finalize_AllArtifacts(t *testing.T) {
	defer requireImplemented(t)

	bs := NewBootstrapSpec("01", "my_spec")
	req, testSpec, tasks, prdBody := buildValidBootstrapArtifacts()
	bs.Requirements = req
	bs.TestSpec = testSpec
	bs.Tasks = tasks
	bs.PRDBody = prdBody

	spec, errs := bs.Finalize()

	if errs != nil {
		t.Fatalf("Finalize returned errors: %v", errs)
	}
	if spec == nil {
		t.Fatal("Finalize returned nil Spec")
	}
	if spec.Requirements == nil {
		t.Error("expected assembled Spec to have non-nil Requirements")
	}
	if spec.TestSpec == nil {
		t.Error("expected assembled Spec to have non-nil TestSpec")
	}
	if spec.Tasks == nil {
		t.Error("expected assembled Spec to have non-nil Tasks")
	}
	if spec.SpecID != "01" {
		t.Errorf("SpecID = %q, want %q", spec.SpecID, "01")
	}
	if spec.SpecName != "my_spec" {
		t.Errorf("SpecName = %q, want %q", spec.SpecName, "my_spec")
	}
}

// TestBootstrapSpec_Finalize_MissingArtifacts verifies that Finalize with
// missing required artifacts returns (nil, []ValidationError) with bootstrap
// rule entries for each missing artifact.
// Test Spec: TS-01-44, Requirement: 01-REQ-21.3
func TestBootstrapSpec_Finalize_MissingArtifacts(t *testing.T) {
	defer requireImplemented(t)

	bs := NewBootstrapSpec("01", "my_spec")
	req, testSpec, _, _ := buildValidBootstrapArtifacts()

	// Set only Requirements and TestSpec; leave Tasks and PRDBody missing
	bs.Requirements = req
	bs.TestSpec = testSpec

	spec, errs := bs.Finalize()

	if spec != nil {
		t.Error("expected nil Spec when artifacts are missing")
	}
	if errs == nil {
		t.Fatal("expected non-nil errors when artifacts are missing")
	}
	if len(errs) < 2 {
		t.Fatalf("expected at least 2 errors (tasks + prd missing), got %d", len(errs))
	}

	// All missing-artifact errors should have rule "bootstrap"
	bootstrapErrors := 0
	for _, e := range errs {
		if e.Rule == "bootstrap" {
			bootstrapErrors++
			if !strings.HasPrefix(e.Message, "Missing artifact:") {
				t.Errorf("expected bootstrap error message to start with 'Missing artifact:', got %q", e.Message)
			}
		}
	}
	if bootstrapErrors < 2 {
		t.Errorf("expected at least 2 bootstrap errors, got %d", bootstrapErrors)
	}
}

// TestBootstrapSpec_Finalize_ValidationFailure verifies that Finalize with
// all artifacts present but schema violations returns (nil, []ValidationError)
// with non-bootstrap rule entries.
// Test Spec: TS-01-45, Requirement: 01-REQ-21.4
func TestBootstrapSpec_Finalize_ValidationFailure(t *testing.T) {
	defer requireImplemented(t)

	bs := NewBootstrapSpec("01", "my_spec")
	req, testSpec, tasks, prdBody := buildValidBootstrapArtifacts()

	// Introduce a schema violation: mismatched spec_id in requirements
	req.SpecId = "WRONG_ID"

	bs.Requirements = req
	bs.TestSpec = testSpec
	bs.Tasks = tasks
	bs.PRDBody = prdBody

	spec, errs := bs.Finalize()

	if spec != nil {
		t.Error("expected nil Spec when validation fails")
	}
	if errs == nil {
		t.Fatal("expected non-nil errors when validation fails")
	}
	if len(errs) == 0 {
		t.Fatal("expected at least one error")
	}
	// Errors should NOT be bootstrap errors (all artifacts are present)
	for _, e := range errs {
		if e.Rule == "bootstrap" {
			t.Errorf("unexpected bootstrap error when all artifacts are present: %v", e)
		}
	}
}

// TestBootstrapSpec_Finalize_MultipleCallsIndependent verifies that calling
// Finalize multiple times on the same BootstrapSpec produces independent
// results and does not mutate the BootstrapSpec.
// Requirement: 01-REQ-21.E1
func TestBootstrapSpec_Finalize_MultipleCallsIndependent(t *testing.T) {
	defer requireImplemented(t)

	bs := NewBootstrapSpec("01", "my_spec")
	req, testSpec, tasks, prdBody := buildValidBootstrapArtifacts()
	bs.Requirements = req
	bs.TestSpec = testSpec
	bs.Tasks = tasks
	bs.PRDBody = prdBody

	// First call should succeed
	spec1, errs1 := bs.Finalize()
	if errs1 != nil {
		t.Fatalf("first Finalize returned errors: %v", errs1)
	}
	if spec1 == nil {
		t.Fatal("first Finalize returned nil Spec")
	}

	// Second call should also succeed independently
	spec2, errs2 := bs.Finalize()
	if errs2 != nil {
		t.Fatalf("second Finalize returned errors: %v", errs2)
	}
	if spec2 == nil {
		t.Fatal("second Finalize returned nil Spec")
	}

	// Results should be independent (different pointers)
	if spec1 == spec2 {
		t.Error("expected independent Spec instances from multiple Finalize calls")
	}
}

// TestBootstrapSpec_Finalize_MissingAndValidationErrors verifies that when
// both missing artifacts and validation errors are present, all errors are
// returned together.
// Requirement: 01-REQ-21.E2
func TestBootstrapSpec_Finalize_MissingAndValidationErrors(t *testing.T) {
	defer requireImplemented(t)

	bs := NewBootstrapSpec("01", "my_spec")

	// Set no artifacts at all — should get missing artifact errors
	spec, errs := bs.Finalize()

	if spec != nil {
		t.Error("expected nil Spec")
	}
	if errs == nil {
		t.Fatal("expected non-nil errors")
	}

	// Should have bootstrap errors for all 4 required artifacts
	bootstrapCount := 0
	for _, e := range errs {
		if e.Rule == "bootstrap" {
			bootstrapCount++
		}
	}
	if bootstrapCount < 4 {
		t.Errorf("expected at least 4 bootstrap errors for all missing artifacts, got %d", bootstrapCount)
	}
}
