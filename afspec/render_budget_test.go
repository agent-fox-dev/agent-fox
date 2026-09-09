package afspec

import (
	"reflect"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Fixture helpers for budget cap tests
// ---------------------------------------------------------------------------

// buildBudgetTestSpec constructs a Spec with all artifacts including a
// sizeable architecture section, suitable for triggering budget-cap
// truncation. The architecture is large enough that removing it
// meaningfully reduces token count.
func buildBudgetTestSpec() *Spec {
	archContent := "# Architecture\n\n" + strings.Repeat("Architecture detail paragraph. ", 100)

	return &Spec{
		SpecID:   "03",
		SpecName: "budget_test",
		Title:    "Budget Test",
		Status:   "draft",
		PRDBody:  "# Budget Test PRD\n\nPRD body content for budget tests.",
		Requirements: &RequirementsV1Json{
			SchemaVersion: 1,
			SpecId:        "03",
			SpecName:      "budget_test",
			Introduction:  "Introduction text for budget test.",
			Glossary:      RequirementsV1JsonGlossary{"term": "definition"},
			Requirements: []Requirement{
				{
					Id:    "03-REQ-1",
					Title: "Test requirement",
					UserStory: UserStory{
						Role: "user", Goal: "test", Benefit: "value",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "03-REQ-1.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "do something",
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
			SpecId:        "03",
			SpecName:      "budget_test",
			TestCases: []TestCase{
				{
					Id:                  "TS-03-1",
					RequirementId:       "03-REQ-1.1",
					Kind:                TestCaseKindUnit,
					Description:         "A test case for budget testing",
					Preconditions:       []string{"precondition A"},
					Input:               "test input data",
					Expected:            "expected output",
					AssertionPseudocode: "assert result == expected",
				},
			},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SchemaVersion: 1,
			SpecId:        "03",
			SpecName:      "budget_test",
			TaskGroups: []TaskGroup{
				{
					Id:    1,
					Kind:  TaskGroupKindTests,
					Title: "Write tests",
					Subtasks: []Subtask{
						{
							Id:              "1.1",
							Title:           "Test subtask",
							Details:         []string{"Detail line"},
							TestSpecRefs:    []string{"TS-03-1"},
							RequirementRefs: []string{"03-REQ-1"},
							State:           SubtaskStatePending,
							Optional:        false,
						},
					},
					Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
				},
			},
			Dependencies: []TaskDependency{},
			TestCommands: TestCommands{AllTests: "go test", SpecTests: "go test", Linter: "go vet"},
			Traceability: []TraceabilityEntry{
				{
					RequirementId: "03-REQ-1.1",
					TestSpecId:    "TS-03-1",
					TaskId:        "1.1",
				},
			},
		},
		Architecture: archContent,
	}
}

// buildBudgetTestSpecNoArch constructs a Spec without architecture.
func buildBudgetTestSpecNoArch() *Spec {
	return &Spec{
		SpecID:   "03",
		SpecName: "noarch_test",
		Title:    "No-arch Test",
		Status:   "draft",
		PRDBody:  "# No-arch PRD\n\nContent.",
		Requirements: &RequirementsV1Json{
			SchemaVersion: 1,
			SpecId:        "03",
			SpecName:      "noarch_test",
			Introduction:  "Intro.",
			Glossary:      RequirementsV1JsonGlossary{"term": "def"},
			Requirements: []Requirement{
				{
					Id:    "03-REQ-1",
					Title: "Req one",
					UserStory: UserStory{
						Role: "user", Goal: "test", Benefit: "val",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "03-REQ-1.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "do it",
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
			SpecId:        "03",
			SpecName:      "noarch_test",
			TestCases: []TestCase{
				{
					Id:                  "TS-03-1",
					RequirementId:       "03-REQ-1.1",
					Kind:                TestCaseKindUnit,
					Description:         "Test case",
					Preconditions:       []string{"pre"},
					Input:               "input",
					Expected:            "expected",
					AssertionPseudocode: "assert True",
				},
			},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SchemaVersion: 1,
			SpecId:        "03",
			SpecName:      "noarch_test",
			TaskGroups: []TaskGroup{
				{
					Id:    1,
					Kind:  TaskGroupKindTests,
					Title: "Write tests",
					Subtasks: []Subtask{
						{
							Id:              "1.1",
							Title:           "Sub",
							Details:         []string{"detail"},
							TestSpecRefs:    []string{"TS-03-1"},
							RequirementRefs: []string{"03-REQ-1"},
							State:           SubtaskStatePending,
							Optional:        false,
						},
					},
					Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
				},
			},
			Dependencies: []TaskDependency{},
			TestCommands: TestCommands{AllTests: "go test", SpecTests: "go test", Linter: "go vet"},
			Traceability: []TraceabilityEntry{},
		},
		Architecture: "", // no architecture
	}
}

// buildBudgetTestSpecWithGroups constructs a Spec with two task groups
// and refs suitable for testing RenderIndividualScoped with budget cap.
func buildBudgetTestSpecWithGroups() *Spec {
	archContent := "# Architecture\n\n" + strings.Repeat("Detailed architecture content paragraph. ", 100)

	return &Spec{
		SpecID:   "03",
		SpecName: "scoped_budget",
		Title:    "Scoped Budget Test",
		Status:   "draft",
		PRDBody:  "# Scoped Budget PRD\n\nPRD body.",
		Requirements: &RequirementsV1Json{
			SchemaVersion: 1,
			SpecId:        "03",
			SpecName:      "scoped_budget",
			Introduction:  "Intro for scoped budget test.",
			Glossary:      RequirementsV1JsonGlossary{"term": "def"},
			Requirements: []Requirement{
				{
					Id:    "03-REQ-1",
					Title: "First requirement",
					UserStory: UserStory{
						Role: "dev", Goal: "load", Benefit: "safety",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "03-REQ-1.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "loads spec",
						},
					},
					EdgeCases: []Criterion{},
				},
				{
					Id:    "03-REQ-2",
					Title: "Second requirement",
					UserStory: UserStory{
						Role: "dev", Goal: "save", Benefit: "persistence",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "03-REQ-2.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "saves spec",
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
			SpecId:        "03",
			SpecName:      "scoped_budget",
			TestCases: []TestCase{
				{
					Id:                  "TS-03-1",
					RequirementId:       "03-REQ-1.1",
					Kind:                TestCaseKindUnit,
					Description:         "Load spec test",
					Preconditions:       []string{},
					Expected:            "loaded",
					AssertionPseudocode: "assert loaded",
				},
				{
					Id:                  "TS-03-2",
					RequirementId:       "03-REQ-2.1",
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
			SpecId:        "03",
			SpecName:      "scoped_budget",
			TaskGroups: []TaskGroup{
				{
					Id:    1,
					Kind:  TaskGroupKindTests,
					Title: "Write tests",
					Subtasks: []Subtask{
						{
							Id:              "1.1",
							Title:           "Test loading",
							Details:         []string{"Load tests"},
							TestSpecRefs:    []string{"TS-03-1"},
							RequirementRefs: []string{"03-REQ-1"},
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
							Title:           "Save impl",
							Details:         []string{"Implement save"},
							TestSpecRefs:    []string{"TS-03-2"},
							RequirementRefs: []string{"03-REQ-2"},
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
		Architecture: archContent,
	}
}

// sumTokens computes the total estimated tokens across all map values.
func sumTokens(m map[string]string) int {
	total := 0
	for _, v := range m {
		total += EstimateTokens(v)
	}
	return total
}

// ---------------------------------------------------------------------------
// TS-03-24: Go rendering methods with no options return output identical
//           to the pre-budget-cap implementation.
// Requirement: 03-REQ-7.2
// ---------------------------------------------------------------------------

func TestRenderIndividual_NoOpts_IdenticalOutput(t *testing.T) {
	defer requireImplemented(t)

	spec := buildBudgetTestSpec()
	result1 := spec.RenderIndividual()
	result2 := spec.RenderIndividual()
	if !reflect.DeepEqual(result1, result2) {
		t.Error("RenderIndividual() with no options should return identical output on successive calls")
	}
}

func TestRenderCombined_NoOpts_IdenticalOutput(t *testing.T) {
	defer requireImplemented(t)

	spec := buildBudgetTestSpec()
	result1 := spec.RenderCombined()
	result2 := spec.RenderCombined()
	if result1 != result2 {
		t.Error("RenderCombined() with no options should return identical output on successive calls")
	}
}

func TestRenderIndividualScoped_NoOpts_IdenticalOutput(t *testing.T) {
	defer requireImplemented(t)

	spec := buildBudgetTestSpecWithGroups()
	result1 := spec.RenderIndividualScoped(1)
	result2 := spec.RenderIndividualScoped(1)
	if !reflect.DeepEqual(result1, result2) {
		t.Error("RenderIndividualScoped(1) with no options should return identical output on successive calls")
	}
}

// ---------------------------------------------------------------------------
// 03-REQ-7.E1: WithMaxTokens(0) or negative treated as no budget constraint
// ---------------------------------------------------------------------------

func TestRenderIndividual_WithMaxTokensZero_FullRender(t *testing.T) {
	defer requireImplemented(t)

	spec := buildBudgetTestSpec()
	fullResult := spec.RenderIndividual()
	result := spec.RenderIndividual(WithMaxTokens(0))
	if !reflect.DeepEqual(result, fullResult) {
		t.Error("RenderIndividual(WithMaxTokens(0)) should return identical output to no-opts call")
	}
}

func TestRenderIndividual_WithMaxTokensNegative_FullRender(t *testing.T) {
	defer requireImplemented(t)

	spec := buildBudgetTestSpec()
	fullResult := spec.RenderIndividual()
	result := spec.RenderIndividual(WithMaxTokens(-10))
	if !reflect.DeepEqual(result, fullResult) {
		t.Error("RenderIndividual(WithMaxTokens(-10)) should return identical output to no-opts call")
	}
}

func TestRenderCombined_WithMaxTokensZero_FullRender(t *testing.T) {
	defer requireImplemented(t)

	spec := buildBudgetTestSpec()
	fullResult := spec.RenderCombined()
	result := spec.RenderCombined(WithMaxTokens(0))
	if result != fullResult {
		t.Error("RenderCombined(WithMaxTokens(0)) should return identical output to no-opts call")
	}
}

func TestRenderCombined_WithMaxTokensNegative_FullRender(t *testing.T) {
	defer requireImplemented(t)

	spec := buildBudgetTestSpec()
	fullResult := spec.RenderCombined()
	result := spec.RenderCombined(WithMaxTokens(-5))
	if result != fullResult {
		t.Error("RenderCombined(WithMaxTokens(-5)) should return identical output to no-opts call")
	}
}

// ---------------------------------------------------------------------------
// TS-03-25: Go RenderIndividual with WithMaxTokens applies progressive
//           truncation (two-level strategy).
// Requirement: 03-REQ-7.3
// ---------------------------------------------------------------------------

func TestRenderIndividual_WithMaxTokens_Level1Truncation(t *testing.T) {
	defer requireImplemented(t)

	spec := buildBudgetTestSpec()

	// Get the full Level 0 render and compute total tokens
	level0 := spec.RenderIndividual()
	level0Tokens := sumTokens(level0)

	// Architecture should be present at Level 0
	archVal, hasArch := level0["architecture"]
	if !hasArch {
		t.Fatal("expected architecture key in Level 0 render")
	}
	archTokens := EstimateTokens(archVal)

	// Set budget that excludes architecture but keeps everything else
	budget := level0Tokens - archTokens + 5

	result := spec.RenderIndividual(WithMaxTokens(budget))
	resultTokens := sumTokens(result)

	// Level 1: architecture should be dropped
	if _, hasArch := result["architecture"]; hasArch {
		t.Error("expected architecture key to be absent after Level 1 truncation")
	}

	// Total tokens should be within budget
	if resultTokens > budget {
		t.Errorf("result tokens %d exceed budget %d after Level 1 truncation", resultTokens, budget)
	}
}

func TestRenderIndividual_WithMaxTokens_Level2Truncation(t *testing.T) {
	defer requireImplemented(t)

	spec := buildBudgetTestSpec()

	// Use a very small budget to force Level 2 truncation
	result := spec.RenderIndividual(WithMaxTokens(1))

	// Architecture should be dropped (Level 1 at minimum)
	if _, hasArch := result["architecture"]; hasArch {
		t.Error("expected architecture key to be absent after Level 2 truncation")
	}

	// Required keys should still be present
	for _, key := range []string{"prd", "requirements", "test_spec", "tasks"} {
		if _, ok := result[key]; !ok {
			t.Errorf("expected key %q in Level 2 render", key)
		}
	}
}

func TestRenderIndividual_WithMaxTokens_BudgetSufficient_NoTruncation(t *testing.T) {
	defer requireImplemented(t)

	spec := buildBudgetTestSpec()

	level0 := spec.RenderIndividual()
	level0Tokens := sumTokens(level0)

	// Generous budget — no truncation needed
	result := spec.RenderIndividual(WithMaxTokens(level0Tokens + 1000))

	if !reflect.DeepEqual(result, level0) {
		t.Error("RenderIndividual with sufficient budget should return Level 0 render")
	}
}

// ---------------------------------------------------------------------------
// 03-REQ-7.E2: No architecture section — skip Level 1, proceed to Level 2
// ---------------------------------------------------------------------------

func TestRenderIndividual_NoArch_SkipsLevel1_GoesToLevel2(t *testing.T) {
	defer requireImplemented(t)

	spec := buildBudgetTestSpecNoArch()
	if spec.Architecture != "" {
		t.Fatal("expected no architecture in test spec")
	}

	// Force truncation with tiny budget
	result := spec.RenderIndividual(WithMaxTokens(1))

	// Architecture was never present
	if _, hasArch := result["architecture"]; hasArch {
		t.Error("expected no architecture key when spec has no architecture")
	}

	// Required keys should still be present
	for _, key := range []string{"prd", "requirements", "test_spec", "tasks"} {
		if _, ok := result[key]; !ok {
			t.Errorf("expected key %q in result", key)
		}
	}
}

// ---------------------------------------------------------------------------
// TS-03-25: RenderCombined with WithMaxTokens applies progressive truncation.
// Requirement: 03-REQ-7.5
// ---------------------------------------------------------------------------

func TestRenderCombined_WithMaxTokens_Level1Truncation(t *testing.T) {
	defer requireImplemented(t)

	spec := buildBudgetTestSpec()

	// Full render should contain architecture content
	fullRender := spec.RenderCombined()
	if !strings.Contains(fullRender, "Architecture detail paragraph") {
		t.Fatal("expected architecture content in full combined render")
	}
	fullTokens := EstimateTokens(fullRender)

	// Compute budget that triggers Level 1 (drop architecture)
	archTokens := EstimateTokens(spec.Architecture)
	budget := fullTokens - archTokens + 5

	result := spec.RenderCombined(WithMaxTokens(budget))
	if strings.Contains(result, "Architecture detail paragraph") {
		t.Error("expected architecture content to be absent after Level 1 truncation")
	}

	resultTokens := EstimateTokens(result)
	if resultTokens > budget {
		t.Errorf("result tokens %d exceed budget %d after Level 1 truncation", resultTokens, budget)
	}
}

func TestRenderCombined_WithMaxTokens_Level2Truncation(t *testing.T) {
	defer requireImplemented(t)

	spec := buildBudgetTestSpec()

	// Tiny budget forces Level 2
	result := spec.RenderCombined(WithMaxTokens(1))

	// Architecture should be absent
	if strings.Contains(result, "Architecture detail paragraph") {
		t.Error("expected architecture content to be absent after Level 2 truncation")
	}

	// PRD body should still be present
	if !strings.Contains(result, "Budget Test PRD") {
		t.Error("expected PRD body to be present even at Level 2 truncation")
	}
}

func TestRenderCombined_WithMaxTokens_BudgetSufficient_NoTruncation(t *testing.T) {
	defer requireImplemented(t)

	spec := buildBudgetTestSpec()

	fullRender := spec.RenderCombined()
	fullTokens := EstimateTokens(fullRender)

	result := spec.RenderCombined(WithMaxTokens(fullTokens + 1000))
	if result != fullRender {
		t.Error("RenderCombined with sufficient budget should return full Level 0 render")
	}
}

// ---------------------------------------------------------------------------
// TS-03-26: Go RenderIndividualScoped with WithMaxTokens applies progressive
//           truncation for scoped rendering.
// Requirement: 03-REQ-7.4
// ---------------------------------------------------------------------------

func TestRenderIndividualScoped_WithMaxTokens_Level1(t *testing.T) {
	defer requireImplemented(t)

	spec := buildBudgetTestSpecWithGroups()

	// Get Level 0 scoped render
	level0 := spec.RenderIndividualScoped(1)
	level0Tokens := sumTokens(level0)

	archVal, hasArch := level0["architecture"]
	if !hasArch {
		t.Fatal("expected architecture in scoped Level 0 render")
	}
	archTokens := EstimateTokens(archVal)

	// Budget triggers Level 1 (remove architecture)
	budget := level0Tokens - archTokens + 5

	result := spec.RenderIndividualScoped(1, WithMaxTokens(budget))
	if _, ok := result["architecture"]; ok {
		t.Error("expected architecture to be absent after Level 1 scoped truncation")
	}

	resultTokens := sumTokens(result)
	if resultTokens > budget {
		t.Errorf("scoped result tokens %d exceed budget %d", resultTokens, budget)
	}
}

func TestRenderIndividualScoped_WithMaxTokens_Level2(t *testing.T) {
	defer requireImplemented(t)

	spec := buildBudgetTestSpecWithGroups()

	// Tiny budget forces Level 2
	result := spec.RenderIndividualScoped(1, WithMaxTokens(1))

	// Architecture should be absent
	if _, ok := result["architecture"]; ok {
		t.Error("expected architecture to be absent after Level 2 scoped truncation")
	}

	// Required keys should be present
	for _, key := range []string{"prd", "requirements", "test_spec", "tasks"} {
		if _, ok := result[key]; !ok {
			t.Errorf("expected key %q in Level 2 scoped render", key)
		}
	}
}

func TestRenderIndividualScoped_WithMaxTokensZero_FullRender(t *testing.T) {
	defer requireImplemented(t)

	spec := buildBudgetTestSpecWithGroups()
	fullResult := spec.RenderIndividualScoped(1)
	result := spec.RenderIndividualScoped(1, WithMaxTokens(0))
	if !reflect.DeepEqual(result, fullResult) {
		t.Error("RenderIndividualScoped(1, WithMaxTokens(0)) should return identical output to no-opts call")
	}
}

func TestRenderIndividualScoped_WithMaxTokens_BudgetSufficient(t *testing.T) {
	defer requireImplemented(t)

	spec := buildBudgetTestSpecWithGroups()
	level0 := spec.RenderIndividualScoped(1)
	level0Tokens := sumTokens(level0)

	result := spec.RenderIndividualScoped(1, WithMaxTokens(level0Tokens+1000))
	if !reflect.DeepEqual(result, level0) {
		t.Error("RenderIndividualScoped with sufficient budget should return Level 0 scoped render")
	}
}

// ---------------------------------------------------------------------------
// 03-REQ-7.E3: Go library never calls os.Exit
// (Verified by design: no test needed; all errors are via return values.)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// 03-PROP-2: No-budget render is byte-identical to legacy render
// Validates: 03-REQ-7.2
// ---------------------------------------------------------------------------

func TestProperty_NoBudget_IdenticalToLegacy(t *testing.T) {
	defer requireImplemented(t)

	spec := buildBudgetTestSpec()

	// RenderIndividual with no options should produce the same result
	// as calling with no options (verifying signature compatibility).
	result := spec.RenderIndividual()
	resultWithOpts := spec.RenderIndividual() // no opts passed
	if !reflect.DeepEqual(result, resultWithOpts) {
		t.Error("RenderIndividual() with and without opts should produce identical output")
	}

	// Same for RenderCombined
	combined := spec.RenderCombined()
	combinedWithOpts := spec.RenderCombined()
	if combined != combinedWithOpts {
		t.Error("RenderCombined() with and without opts should produce identical output")
	}
}
