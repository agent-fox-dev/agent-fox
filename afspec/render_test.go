package afspec

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 4.1: RenderCombined and RenderIndividual
// ---------------------------------------------------------------------------

// TestRenderCombined verifies that Spec.RenderCombined returns a non-empty
// string containing Markdown sections for PRD body, requirements, test spec,
// tasks, and architecture.
// Test Spec: TS-01-35, Requirement: 01-REQ-18.1
func TestRenderCombined(t *testing.T) {
	defer requireImplemented(t)

	spec, err := LoadSpec("./../testdata/valid_spec_with_arch")
	if err != nil {
		t.Fatalf("LoadSpec returned unexpected error: %v", err)
	}

	result := spec.RenderCombined()

	if result == "" {
		t.Fatal("RenderCombined returned empty string")
	}

	// Should contain Markdown section headers
	if !strings.Contains(result, "## ") && !strings.Contains(result, "# ") {
		t.Error("expected result to contain Markdown section headers")
	}

	// Should contain content from the PRD body
	assertContains(t, result, "Test Feature", "PRD body content")
}

// TestRenderCombined_EmptyRequirements verifies that RenderCombined does not
// panic when the requirements section is empty.
// Requirement: 01-REQ-18.E2
func TestRenderCombined_EmptyRequirements(t *testing.T) {
	defer requireImplemented(t)

	spec := &Spec{
		SpecID:   "01",
		SpecName: "test",
		Status:   "draft",
		PRDBody:  "# Test\n\nSome content.\n",
		Requirements: &RequirementsV1Json{
			SchemaVersion: 1,
			SpecId:        "01",
			SpecName:      "test",
			Introduction:  "Test intro",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements:  []Requirement{},
		},
		TestSpec: &TestSpecV1Json{
			SchemaVersion: 1,
			SpecId:        "01",
			SpecName:      "test",
			TestCases:     []TestCase{},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SchemaVersion: 1,
			SpecId:        "01",
			SpecName:      "test",
			TaskGroups:    []TaskGroup{},
			Dependencies:  []TaskDependency{},
			TestCommands:  TestCommands{AllTests: "go test", SpecTests: "go test", Linter: "go vet"},
			Traceability:  []TraceabilityEntry{},
		},
	}

	result := spec.RenderCombined()
	if result == "" {
		t.Error("RenderCombined should return non-empty string even with empty requirements")
	}
}

// TestRenderIndividual verifies that Spec.RenderIndividual returns a
// map[string]string with keys for each artifact and non-empty values.
// Test Spec: TS-01-36, Requirement: 01-REQ-18.2
func TestRenderIndividual(t *testing.T) {
	defer requireImplemented(t)

	spec, err := LoadSpec("./../testdata/valid_spec_with_arch")
	if err != nil {
		t.Fatalf("LoadSpec returned unexpected error: %v", err)
	}

	result := spec.RenderIndividual()

	expectedKeys := []string{"prd", "requirements", "test_spec", "tasks", "architecture"}
	for _, key := range expectedKeys {
		val, ok := result[key]
		if !ok {
			t.Errorf("expected key %q in result map, but it was missing", key)
			continue
		}
		if val == "" {
			t.Errorf("expected non-empty value for key %q", key)
		}
	}
}

// TestRenderIndividual_NoArchitecture verifies that RenderIndividual omits the
// "architecture" key from the returned map when the spec has no architecture.md.
// Requirement: 01-REQ-18.E1
func TestRenderIndividual_NoArchitecture(t *testing.T) {
	defer requireImplemented(t)

	spec := &Spec{
		SpecID:   "01",
		SpecName: "test",
		Status:   "draft",
		PRDBody:  "# Test\n\nSome content.\n",
		Requirements: &RequirementsV1Json{
			SchemaVersion: 1,
			SpecId:        "01",
			SpecName:      "test",
			Introduction:  "Test intro",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements:  []Requirement{},
		},
		TestSpec: &TestSpecV1Json{
			SchemaVersion: 1,
			SpecId:        "01",
			SpecName:      "test",
			TestCases:     []TestCase{},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SchemaVersion: 1,
			SpecId:        "01",
			SpecName:      "test",
			TaskGroups:    []TaskGroup{},
			Dependencies:  []TaskDependency{},
			TestCommands:  TestCommands{AllTests: "go test", SpecTests: "go test", Linter: "go vet"},
			Traceability:  []TraceabilityEntry{},
		},
		Architecture: "", // no architecture
	}

	result := spec.RenderIndividual()

	requiredKeys := []string{"prd", "requirements", "test_spec", "tasks"}
	for _, key := range requiredKeys {
		if _, ok := result[key]; !ok {
			t.Errorf("expected key %q in result map", key)
		}
	}

	if _, ok := result["architecture"]; ok {
		t.Error("expected 'architecture' key to be absent when Architecture is empty")
	}
}

// TestRequirementsRender verifies that RequirementsV1Json.Render produces
// valid Markdown output.
// Requirement: 01-REQ-18.2
func TestRequirementsRender(t *testing.T) {
	defer requireImplemented(t)

	req := &RequirementsV1Json{
		SchemaVersion: 1,
		SpecId:        "01",
		SpecName:      "test_feature",
		Introduction:  "The test feature validates the spec library.",
		Glossary:      RequirementsV1JsonGlossary{"spec": "A package"},
		Requirements: []Requirement{
			{
				Id:    "01-REQ-1",
				Title: "Data Model",
				UserStory: UserStory{
					Role:    "developer",
					Goal:    "have typed models",
					Benefit: "type safety",
				},
				AcceptanceCriteria: []Criterion{
					{
						Id:          "01-REQ-1.1",
						EarsPattern: CriterionEarsPatternEventDriven,
						Trigger:     strPtr("a spec is loaded"),
						System:      "the system",
						Action:      "return a populated Spec",
					},
				},
				EdgeCases: []Criterion{},
			},
		},
		CorrectnessProperties: []CorrectnessProperty{},
		ExecutionPaths:        []ExecutionPath{},
		ErrorHandling:         []ErrorHandlingEntry{},
	}

	result := req.Render()
	if result == "" {
		t.Fatal("Requirements.Render returned empty string")
	}
	assertContains(t, result, "01-REQ-1", "requirement ID")
	assertContains(t, result, "Data Model", "requirement title")
}

// TestTestSpecRender verifies that TestSpecV1Json.Render produces valid
// Markdown output.
// Requirement: 01-REQ-18.2
func TestTestSpecRender(t *testing.T) {
	defer requireImplemented(t)

	ts := &TestSpecV1Json{
		SchemaVersion: 1,
		SpecId:        "01",
		SpecName:      "test_feature",
		TestCases: []TestCase{
			{
				Id:                  "TS-01-1",
				RequirementId:       "01-REQ-1.1",
				Kind:                TestCaseKindUnit,
				Description:         "Spec type exports all four artifacts",
				Preconditions:       []string{},
				Expected:            "populated spec",
				AssertionPseudocode: "assert spec.prd is not None",
			},
		},
		PropertyTests: []PropertyTest{},
		EdgeCaseTests: []EdgeCaseTest{},
		SmokeTests:    []SmokeTest{},
		Coverage:      Coverage{},
	}

	result := ts.Render()
	if result == "" {
		t.Fatal("TestSpec.Render returned empty string")
	}
	assertContains(t, result, "TS-01-1", "test case ID")
}

// TestTasksRender verifies that TasksV1Json.Render produces valid Markdown
// output with checkbox-formatted subtasks.
// Requirement: 01-REQ-18.2
func TestTasksRender(t *testing.T) {
	defer requireImplemented(t)

	tasks := &TasksV1Json{
		SchemaVersion: 1,
		SpecId:        "01",
		SpecName:      "test_feature",
		TaskGroups: []TaskGroup{
			{
				Id:    1,
				Kind:  TaskGroupKindTests,
				Title: "Write failing tests",
				Subtasks: []Subtask{
					{
						Id:              "1.1",
						Title:           "Create test infrastructure",
						Details:         []string{"Set up fixtures"},
						TestSpecRefs:    []string{"TS-01-1"},
						RequirementRefs: []string{"01-REQ-1.1"},
						State:           SubtaskStateDone,
						Optional:        false,
					},
					{
						Id:              "1.2",
						Title:           "Write load tests",
						Details:         []string{"Test loading"},
						TestSpecRefs:    []string{},
						RequirementRefs: []string{},
						State:           SubtaskStatePending,
						Optional:        false,
					},
				},
				Verification: VerificationSubtask{Id: "1.V", Checks: []string{"Tests pass"}},
			},
		},
		Dependencies: []TaskDependency{},
		TestCommands: TestCommands{AllTests: "go test", SpecTests: "go test", Linter: "go vet"},
		Traceability: []TraceabilityEntry{},
	}

	result := tasks.Render()
	if result == "" {
		t.Fatal("Tasks.Render returned empty string")
	}
	assertContains(t, result, "1.1", "subtask ID")
	// Expect checkbox-formatted subtasks: done tasks get [x], pending get [ ]
	if !strings.Contains(result, "[x]") && !strings.Contains(result, "[X]") {
		t.Error("expected done subtasks to have checkbox [x] or [X]")
	}
	if !strings.Contains(result, "[ ]") {
		t.Error("expected pending subtasks to have checkbox [ ]")
	}
}

// ---------------------------------------------------------------------------
// 4.2: RenderIndividualScoped
// ---------------------------------------------------------------------------

// buildScopedTestSpec constructs a Spec suitable for testing
// RenderIndividualScoped with two task groups that have different refs.
func buildScopedTestSpec() *Spec {
	return &Spec{
		SpecID:   "01",
		SpecName: "test_feature",
		Title:    "Test Feature",
		Status:   "draft",
		PRDBody:  "# Test Feature\n\n## Intent\n\nBuild a test feature.\n",
		Requirements: &RequirementsV1Json{
			SchemaVersion: 1,
			SpecId:        "01",
			SpecName:      "test_feature",
			Introduction:  "The test feature.",
			Glossary:      RequirementsV1JsonGlossary{"spec": "A package"},
			Requirements: []Requirement{
				{
					Id:    "01-REQ-1",
					Title: "First Requirement",
					UserStory: UserStory{
						Role: "developer", Goal: "load specs", Benefit: "type safety",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "01-REQ-1.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "returns loaded spec",
						},
					},
					EdgeCases: []Criterion{},
				},
				{
					Id:    "01-REQ-2",
					Title: "Second Requirement",
					UserStory: UserStory{
						Role: "developer", Goal: "save specs", Benefit: "persistence",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "01-REQ-2.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "saves spec to disk",
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
			SchemaVersion: 1,
			SpecId:        "01",
			SpecName:      "test_feature",
			TestCases: []TestCase{
				{
					Id:                  "TS-01-1",
					RequirementId:       "01-REQ-1.1",
					Kind:                TestCaseKindUnit,
					Description:         "Load spec test",
					Preconditions:       []string{},
					Expected:            "loaded",
					AssertionPseudocode: "assert loaded",
				},
				{
					Id:                  "TS-01-2",
					RequirementId:       "01-REQ-2.1",
					Kind:                TestCaseKindUnit,
					Description:         "Save spec test",
					Preconditions:       []string{},
					Expected:            "saved",
					AssertionPseudocode: "assert saved",
				},
			},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SchemaVersion: 1,
			SpecId:        "01",
			SpecName:      "test_feature",
			TaskGroups: []TaskGroup{
				{
					Id:    1,
					Kind:  TaskGroupKindTests,
					Title: "Write loading tests",
					Subtasks: []Subtask{
						{
							Id:              "1.1",
							Title:           "Test loading",
							Details:         []string{"Write load tests"},
							TestSpecRefs:    []string{"TS-01-1"},
							RequirementRefs: []string{"01-REQ-1"},
							State:           SubtaskStatePending,
							Optional:        false,
						},
					},
					Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
				},
				{
					Id:    2,
					Kind:  TaskGroupKindStandard,
					Title: "Implement saving",
					Subtasks: []Subtask{
						{
							Id:              "2.1",
							Title:           "Implement save",
							Details:         []string{"Write save logic"},
							TestSpecRefs:    []string{"TS-01-2"},
							RequirementRefs: []string{"01-REQ-2"},
							State:           SubtaskStatePending,
							Optional:        false,
						},
					},
					Verification: VerificationSubtask{Id: "2.V", Checks: []string{"check"}},
				},
			},
			Dependencies: []TaskDependency{},
			TestCommands: TestCommands{AllTests: "go test", SpecTests: "go test", Linter: "go vet"},
			Traceability: []TraceabilityEntry{},
		},
		Architecture: "# Architecture\n\nOverview of the architecture.\n",
	}
}

// TestRenderIndividualScoped verifies that RenderIndividualScoped renders
// only the referenced requirements and test entries for the target group,
// shows the target group with full subtask detail, and other groups as
// one-line summaries.
// Test Spec: TS-01-37, Requirement: 01-REQ-19.1
func TestRenderIndividualScoped(t *testing.T) {
	defer requireImplemented(t)

	spec := buildScopedTestSpec()
	result := spec.RenderIndividualScoped(1)

	// Requirements section should contain 01-REQ-1 (in scope)
	reqSection := result["requirements"]
	assertContains(t, reqSection, "01-REQ-1", "scoped requirement")

	// Test spec section should contain TS-01-1 (in scope)
	tsSection := result["test_spec"]
	assertContains(t, tsSection, "TS-01-1", "scoped test entry")

	// Tasks section should show group 1 with full detail
	tasksSection := result["tasks"]
	assertContains(t, tasksSection, "1.1", "group 1 subtask detail")
	assertContains(t, tasksSection, "Test loading", "group 1 subtask title")

	// PRD should be unfiltered
	prdSection := result["prd"]
	assertContains(t, prdSection, "Test Feature", "unfiltered PRD")

	// Architecture should be unfiltered
	archSection := result["architecture"]
	assertContains(t, archSection, "Architecture", "unfiltered architecture")
}

// TestRenderIndividualScoped_NoRefs verifies that RenderIndividualScoped
// falls back to full unscoped rendering when the target group has no
// requirement_refs or test_spec_refs.
// Test Spec: TS-01-38, Requirement: 01-REQ-19.2
func TestRenderIndividualScoped_NoRefs(t *testing.T) {
	defer requireImplemented(t)

	spec := buildScopedTestSpec()

	// Add a group 3 with no refs
	spec.Tasks.TaskGroups = append(spec.Tasks.TaskGroups, TaskGroup{
		Id:    3,
		Kind:  TaskGroupKindStandard,
		Title: "No-ref group",
		Subtasks: []Subtask{
			{
				Id:              "3.1",
				Title:           "No-ref task",
				Details:         []string{"No refs here"},
				TestSpecRefs:    []string{},
				RequirementRefs: []string{},
				State:           SubtaskStatePending,
				Optional:        false,
			},
		},
		Verification: VerificationSubtask{Id: "3.V", Checks: []string{"check"}},
	})

	scoped := spec.RenderIndividualScoped(3)
	full := spec.RenderIndividual()

	// Should fall back to full rendering for requirements and test_spec
	if scoped["requirements"] != full["requirements"] {
		t.Error("expected scoped requirements to equal full requirements when group has no refs")
	}
	if scoped["test_spec"] != full["test_spec"] {
		t.Error("expected scoped test_spec to equal full test_spec when group has no refs")
	}
}

// TestRenderIndividualScoped_SpecOverview verifies that the scoped
// requirements section includes a Spec Overview listing ALL requirement
// IDs and titles, even those not in the scoped set.
// Test Spec: TS-01-39, Requirement: 01-REQ-19.3
func TestRenderIndividualScoped_SpecOverview(t *testing.T) {
	defer requireImplemented(t)

	spec := buildScopedTestSpec()

	// Scope to group 1, which only references 01-REQ-1
	result := spec.RenderIndividualScoped(1)
	reqSection := result["requirements"]

	// Should contain a Spec Overview section
	assertContains(t, reqSection, "Spec Overview", "Spec Overview header")

	// Spec Overview should list BOTH requirements even though only 01-REQ-1 is scoped
	assertContains(t, reqSection, "01-REQ-1", "first requirement in overview")
	assertContains(t, reqSection, "01-REQ-2", "second requirement in overview")
}

// TestRenderIndividualScoped_NonexistentGroup verifies that
// RenderIndividualScoped falls back to full unscoped rendering when
// the target group ID does not exist.
// Requirement: 01-REQ-19.E1
func TestRenderIndividualScoped_NonexistentGroup(t *testing.T) {
	defer requireImplemented(t)

	spec := buildScopedTestSpec()

	scoped := spec.RenderIndividualScoped(999)
	full := spec.RenderIndividual()

	if scoped["requirements"] != full["requirements"] {
		t.Error("expected nonexistent group to fall back to full requirements rendering")
	}
	if scoped["test_spec"] != full["test_spec"] {
		t.Error("expected nonexistent group to fall back to full test_spec rendering")
	}
}

// ---------------------------------------------------------------------------
// Issue #36: RenderCombined section ordering (Section 11.1)
// ---------------------------------------------------------------------------

// TestRenderCombined_ArchitectureAfterPRD verifies that when Architecture is
// present, RenderCombined places it immediately after the PRD section and
// before Requirements.
// Requirements: NS-REQ-1, NS-REQ-3 | Test Spec: TS-NS-1, TS-NS-3
func TestRenderCombined_ArchitectureAfterPRD(t *testing.T) {
	defer requireImplemented(t)

	spec, err := LoadSpec("./../testdata/valid_spec_with_arch")
	if err != nil {
		t.Fatalf("LoadSpec returned unexpected error: %v", err)
	}

	result := spec.RenderCombined()

	idxPRD := strings.Index(result, "# PRD")
	idxArch := strings.Index(result, "# Architecture")
	idxReq := strings.Index(result, "# Requirements")
	idxTestSpec := strings.Index(result, "# Test Specification")
	idxTasks := strings.Index(result, "# Tasks")

	if idxPRD < 0 {
		t.Fatal("expected '# PRD' in combined output")
	}
	if idxArch < 0 {
		t.Fatal("expected '# Architecture' in combined output when architecture is present")
	}
	if idxReq < 0 {
		t.Fatal("expected '# Requirements' in combined output")
	}
	if idxTestSpec < 0 {
		t.Fatal("expected '# Test Specification' in combined output")
	}
	if idxTasks < 0 {
		t.Fatal("expected '# Tasks' in combined output")
	}

	// NS-REQ-3: PRD < Architecture < Requirements < Test Specification < Tasks
	if !(idxPRD < idxArch) {
		t.Errorf("expected '# PRD' (idx %d) before '# Architecture' (idx %d)", idxPRD, idxArch)
	}
	if !(idxArch < idxReq) {
		t.Errorf("expected '# Architecture' (idx %d) before '# Requirements' (idx %d)", idxArch, idxReq)
	}
	if !(idxReq < idxTestSpec) {
		t.Errorf("expected '# Requirements' (idx %d) before '# Test Specification' (idx %d)", idxReq, idxTestSpec)
	}
	if !(idxTestSpec < idxTasks) {
		t.Errorf("expected '# Test Specification' (idx %d) before '# Tasks' (idx %d)", idxTestSpec, idxTasks)
	}
}

// ---------------------------------------------------------------------------
// Issue #38: Deterministic glossary output (Section 11)
// ---------------------------------------------------------------------------

// buildGlossarySpec builds a RequirementsV1Json with a multi-term glossary
// for determinism testing.
func buildGlossarySpec() *RequirementsV1Json {
	return &RequirementsV1Json{
		SchemaVersion: 1,
		SpecId:        "01",
		SpecName:      "test_feature",
		Introduction:  "Test intro.",
		Glossary: RequirementsV1JsonGlossary{
			"delta": "The fourth letter",
			"alpha": "The first letter",
			"gamma": "The third letter",
			"beta":  "The second letter",
		},
		Requirements:          []Requirement{},
		CorrectnessProperties: []CorrectnessProperty{},
		ExecutionPaths:        []ExecutionPath{},
		ErrorHandling:         []ErrorHandlingEntry{},
	}
}

// TestRequirementsRender_GlossaryDeterminism verifies that Render() produces
// byte-identical output across 100 calls when the glossary has 4+ terms, and
// that glossary rows appear in ascending alphabetical order.
// Requirements: NS-REQ-1 | Test Spec: TS-NS-1
func TestRequirementsRender_GlossaryDeterminism(t *testing.T) {
	defer requireImplemented(t)

	req := buildGlossarySpec()

	first := req.Render()
	if first == "" {
		t.Fatal("Render returned empty string")
	}

	for i := 1; i < 100; i++ {
		got := req.Render()
		if got != first {
			t.Fatalf("Render is non-deterministic: call %d differed from call 0", i)
		}
	}

	// Verify glossary rows appear in sorted order: alpha < beta < delta < gamma
	idxAlpha := strings.Index(first, "| alpha |")
	idxBeta := strings.Index(first, "| beta |")
	idxDelta := strings.Index(first, "| delta |")
	idxGamma := strings.Index(first, "| gamma |")

	if idxAlpha < 0 || idxBeta < 0 || idxDelta < 0 || idxGamma < 0 {
		t.Fatalf("expected all glossary terms in output; got:\n%s", first)
	}
	if !(idxAlpha < idxBeta) {
		t.Errorf("expected '| alpha |' (idx %d) before '| beta |' (idx %d)", idxAlpha, idxBeta)
	}
	if !(idxBeta < idxDelta) {
		t.Errorf("expected '| beta |' (idx %d) before '| delta |' (idx %d)", idxBeta, idxDelta)
	}
	if !(idxDelta < idxGamma) {
		t.Errorf("expected '| delta |' (idx %d) before '| gamma |' (idx %d)", idxDelta, idxGamma)
	}
}

// TestRequirementsRender_SingleTermGlossary verifies that Render() handles a
// single-term glossary correctly (no regression for the trivial case).
// Requirements: NS-REQ-5 | Test Spec: TS-NS-5
func TestRequirementsRender_SingleTermGlossary(t *testing.T) {
	defer requireImplemented(t)

	req := &RequirementsV1Json{
		SchemaVersion:         1,
		SpecId:                "01",
		SpecName:              "test",
		Introduction:          "Test intro.",
		Glossary:              RequirementsV1JsonGlossary{"term": "definition"},
		Requirements:          []Requirement{},
		CorrectnessProperties: []CorrectnessProperty{},
		ExecutionPaths:        []ExecutionPath{},
		ErrorHandling:         []ErrorHandlingEntry{},
	}

	first := req.Render()
	if first == "" {
		t.Fatal("Render returned empty string for single-term glossary")
	}
	if !strings.Contains(first, "| term | definition |") {
		t.Errorf("expected '| term | definition |' in output; got:\n%s", first)
	}

	for i := 1; i < 10; i++ {
		got := req.Render()
		if got != first {
			t.Fatalf("Render is non-deterministic on call %d for single-term glossary", i)
		}
	}
}

// buildScopedGlossarySpec builds a Spec with a multi-term glossary for
// RenderIndividualScoped determinism testing.
func buildScopedGlossarySpec() *Spec {
	spec := buildScopedTestSpec()
	spec.Requirements.Glossary = RequirementsV1JsonGlossary{
		"delta": "The fourth letter",
		"alpha": "The first letter",
		"gamma": "The third letter",
		"beta":  "The second letter",
	}
	return spec
}

// TestRenderIndividualScoped_GlossaryDeterminism verifies that
// RenderIndividualScoped produces byte-identical output across 100 calls and
// that glossary rows appear in ascending alphabetical order.
// Requirements: NS-REQ-2 | Test Spec: TS-NS-2
func TestRenderIndividualScoped_GlossaryDeterminism(t *testing.T) {
	defer requireImplemented(t)

	spec := buildScopedGlossarySpec()

	first := spec.RenderIndividualScoped(1)
	reqSection := first["requirements"]
	if reqSection == "" {
		t.Fatal("RenderIndividualScoped 'requirements' section is empty")
	}

	for i := 1; i < 100; i++ {
		got := spec.RenderIndividualScoped(1)
		if got["requirements"] != reqSection {
			t.Fatalf("RenderIndividualScoped 'requirements' is non-deterministic on call %d", i)
		}
	}

	// Verify sorted order: alpha < beta < delta < gamma
	idxAlpha := strings.Index(reqSection, "| alpha |")
	idxBeta := strings.Index(reqSection, "| beta |")
	idxDelta := strings.Index(reqSection, "| delta |")
	idxGamma := strings.Index(reqSection, "| gamma |")

	if idxAlpha < 0 || idxBeta < 0 || idxDelta < 0 || idxGamma < 0 {
		t.Fatalf("expected all glossary terms in requirements section; got:\n%s", reqSection)
	}
	if !(idxAlpha < idxBeta) {
		t.Errorf("expected '| alpha |' (idx %d) before '| beta |' (idx %d)", idxAlpha, idxBeta)
	}
	if !(idxBeta < idxDelta) {
		t.Errorf("expected '| beta |' (idx %d) before '| delta |' (idx %d)", idxBeta, idxDelta)
	}
	if !(idxDelta < idxGamma) {
		t.Errorf("expected '| delta |' (idx %d) before '| gamma |' (idx %d)", idxDelta, idxGamma)
	}
}

// TestRenderCombined_NoArchitectureWhenEmpty verifies that when Architecture is
// empty, RenderCombined omits the Architecture section and all other sections
// are still present.
// Requirements: NS-REQ-2 | Test Spec: TS-NS-2
func TestRenderCombined_NoArchitectureWhenEmpty(t *testing.T) {
	defer requireImplemented(t)

	spec := &Spec{
		SpecID:   "01",
		SpecName: "test",
		Status:   "draft",
		PRDBody:  "# Test\n\nSome content.\n",
		Requirements: &RequirementsV1Json{
			SchemaVersion: 1,
			SpecId:        "01",
			SpecName:      "test",
			Introduction:  "Test intro",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements:  []Requirement{},
		},
		TestSpec: &TestSpecV1Json{
			SchemaVersion: 1,
			SpecId:        "01",
			SpecName:      "test",
			TestCases:     []TestCase{},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SchemaVersion: 1,
			SpecId:        "01",
			SpecName:      "test",
			TaskGroups:    []TaskGroup{},
			Dependencies:  []TaskDependency{},
			TestCommands:  TestCommands{AllTests: "go test", SpecTests: "go test", Linter: "go vet"},
			Traceability:  []TraceabilityEntry{},
		},
		Architecture: "", // no architecture
	}

	result := spec.RenderCombined()

	if strings.Contains(result, "# Architecture") {
		t.Error("expected no '# Architecture' section when Architecture is empty")
	}

	for _, section := range []string{"# PRD", "# Requirements", "# Test Specification", "# Tasks"} {
		if !strings.Contains(result, section) {
			t.Errorf("expected %q to be present in combined output even without architecture", section)
		}
	}
}

// TestRenderTestCaseObjectInput verifies that a map-typed Input is rendered
// as compact JSON, not as a Go map literal. (NS-REQ-1 / TS-NS-1)
func TestRenderTestCaseObjectInput(t *testing.T) {
	defer requireImplemented(t)

	ts := &TestSpecV1Json{
		SchemaVersion: 1,
		SpecId:        "01",
		SpecName:      "test_feature",
		TestCases: []TestCase{
			{
				Id:            "TS-01-2",
				RequirementId: "01-REQ-1.1",
				Kind:          TestCaseKindUnit,
				Description:   "Object input is JSON",
				Preconditions: []string{},
				Input:         map[string]interface{}{"prompt": "test", "max_tokens": 100},
				Expected:      "ok",
			},
		},
		PropertyTests: []PropertyTest{},
		EdgeCaseTests: []EdgeCaseTest{},
		SmokeTests:    []SmokeTest{},
		Coverage:      Coverage{},
	}

	result := ts.Render()

	if strings.Contains(result, "map[") {
		t.Errorf("rendered output contains Go map literal; want JSON: %s", result)
	}
	assertContains(t, result, `**Input:**`, "Input label")
	assertContains(t, result, `"prompt"`, "JSON key prompt")
	assertContains(t, result, `"max_tokens"`, "JSON key max_tokens")
	assertContains(t, result, `{`, "opening brace")
	assertContains(t, result, `}`, "closing brace")
}

// TestRenderTestCaseObjectExpected verifies that a map-typed Expected is
// rendered as compact JSON. (NS-REQ-2 / TS-NS-2)
func TestRenderTestCaseObjectExpected(t *testing.T) {
	defer requireImplemented(t)

	ts := &TestSpecV1Json{
		SchemaVersion: 1,
		SpecId:        "01",
		SpecName:      "test_feature",
		TestCases: []TestCase{
			{
				Id:            "TS-01-3",
				RequirementId: "01-REQ-1.1",
				Kind:          TestCaseKindUnit,
				Description:   "Object expected is JSON",
				Preconditions: []string{},
				Expected:      map[string]interface{}{"result": "success", "code": 200},
			},
		},
		PropertyTests: []PropertyTest{},
		EdgeCaseTests: []EdgeCaseTest{},
		SmokeTests:    []SmokeTest{},
		Coverage:      Coverage{},
	}

	result := ts.Render()

	if strings.Contains(result, "map[") {
		t.Errorf("rendered output contains Go map literal; want JSON: %s", result)
	}
	assertContains(t, result, `**Expected:**`, "Expected label")
	assertContains(t, result, `"result"`, "JSON key result")
	assertContains(t, result, `"code"`, "JSON key code")
}

// TestRenderTestCaseStringInput verifies that string-typed Input is rendered
// as-is, without JSON quoting. (NS-REQ-3 / TS-NS-3)
func TestRenderTestCaseStringInput(t *testing.T) {
	defer requireImplemented(t)

	ts := &TestSpecV1Json{
		SchemaVersion: 1,
		SpecId:        "01",
		SpecName:      "test_feature",
		TestCases: []TestCase{
			{
				Id:            "TS-01-4",
				RequirementId: "01-REQ-1.1",
				Kind:          TestCaseKindUnit,
				Description:   "String input is literal",
				Preconditions: []string{},
				Input:         "hello",
				Expected:      "world",
			},
		},
		PropertyTests: []PropertyTest{},
		EdgeCaseTests: []EdgeCaseTest{},
		SmokeTests:    []SmokeTest{},
		Coverage:      Coverage{},
	}

	result := ts.Render()

	assertContains(t, result, "**Input:** `hello`", "literal string input")
	assertContains(t, result, "**Expected:** world", "literal string expected")
}

// TestRenderTestCaseNilInput verifies that a nil Input omits the Input line
// entirely. (NS-REQ-4 / TS-NS-4)
func TestRenderTestCaseNilInput(t *testing.T) {
	defer requireImplemented(t)

	ts := &TestSpecV1Json{
		SchemaVersion: 1,
		SpecId:        "01",
		SpecName:      "test_feature",
		TestCases: []TestCase{
			{
				Id:            "TS-01-5",
				RequirementId: "01-REQ-1.1",
				Kind:          TestCaseKindUnit,
				Description:   "Nil input is omitted",
				Preconditions: []string{},
				// Input intentionally omitted (nil)
				Expected: "anything",
			},
		},
		PropertyTests: []PropertyTest{},
		EdgeCaseTests: []EdgeCaseTest{},
		SmokeTests:    []SmokeTest{},
		Coverage:      Coverage{},
	}

	result := ts.Render()

	if strings.Contains(result, "**Input:**") {
		t.Errorf("expected no **Input:** line when Input is nil, got: %s", result)
	}
}

// TestRenderTestCaseArrayInput verifies that a slice-typed Input is rendered
// as a compact JSON array. (NS-REQ-5 / TS-NS-5)
func TestRenderTestCaseArrayInput(t *testing.T) {
	defer requireImplemented(t)

	ts := &TestSpecV1Json{
		SchemaVersion: 1,
		SpecId:        "01",
		SpecName:      "test_feature",
		TestCases: []TestCase{
			{
				Id:            "TS-01-6",
				RequirementId: "01-REQ-1.1",
				Kind:          TestCaseKindUnit,
				Description:   "Array input is JSON",
				Preconditions: []string{},
				Input:         []interface{}{"a", "b"},
				Expected:      "ok",
			},
		},
		PropertyTests: []PropertyTest{},
		EdgeCaseTests: []EdgeCaseTest{},
		SmokeTests:    []SmokeTest{},
		Coverage:      Coverage{},
	}

	result := ts.Render()

	assertContains(t, result, "**Input:** `[\"a\",\"b\"]`", "JSON array input")
}
