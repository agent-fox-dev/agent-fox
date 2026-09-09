package afspec

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Fixture builder for validation-warning tests
// ---------------------------------------------------------------------------

// buildValidationSpec constructs a minimal valid *Spec with a single
// TaskGroup whose Kind and subtasks are provided by the caller.
// The spec has one requirement (02-REQ-1) and one test case (TS-02-1)
// so that schema validation passes.
func buildValidationSpec(groups []TaskGroup) *Spec {
	return &Spec{
		SpecID:   "02",
		SpecName: "validation_test",
		Title:    "Validation Test",
		Status:   "draft",
		PRDBody:  "# Validation Test\n\n## Intent\n\nValidation test.\n",
		Requirements: &RequirementsV1Json{
			SchemaVersion: 1,
			SpecId:        "02",
			SpecName:      "validation_test",
			Introduction:  "Test spec for validation.",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements: []Requirement{
				{
					Id:    "02-REQ-1",
					Title: "Requirement One",
					UserStory: UserStory{
						Role: "developer", Goal: "validate", Benefit: "correctness",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "02-REQ-1.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "validates",
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
			SpecId:        "02",
			SpecName:      "validation_test",
			TestCases: []TestCase{
				{
					Id:                  "TS-02-1",
					RequirementId:       "02-REQ-1.1",
					Kind:                TestCaseKindUnit,
					Description:         "Test case one",
					Preconditions:       []string{},
					Expected:            "pass",
					AssertionPseudocode: "assert true",
				},
			},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SchemaVersion: 1,
			SpecId:        "02",
			SpecName:      "validation_test",
			TaskGroups:    groups,
			Dependencies:  []TaskDependency{},
			TestCommands:  TestCommands{AllTests: "go test", SpecTests: "go test", Linter: "go vet"},
			Traceability:  []TraceabilityEntry{},
		},
	}
}

// ---------------------------------------------------------------------------
// TS-02-11: validate / Validate invokes checkMissingSubtaskRefs
// (02-REQ-4.1)
//
// validate / Validate invokes checkMissingSubtaskRefs and appends its
// warnings to the ValidationResult when subtasks have empty refs.
// ---------------------------------------------------------------------------

func TestValidateMissingRefsViaValidate(t *testing.T) {
	groups := []TaskGroup{
		{
			Id:    1,
			Kind:  TaskGroupKindStandard,
			Title: "Standard group",
			Subtasks: []Subtask{
				{
					Id:              "1.1",
					Title:           "Subtask with no refs",
					Details:         []string{},
					RequirementRefs: []string{},
					TestSpecRefs:    []string{},
					State:           SubtaskStatePending,
				},
			},
			Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
		},
	}
	spec := buildValidationSpec(groups)
	result := spec.Validate()

	t.Run("warnings_include_subtask_1_1", func(t *testing.T) {
		found := false
		for _, w := range result.Warnings {
			if w.EntityID == "1.1" {
				found = true
				break
			}
		}
		if !found {
			msgs := make([]string, len(result.Warnings))
			for i, w := range result.Warnings {
				msgs[i] = w.Message
			}
			t.Errorf("expected a warning for subtask 1.1, got warnings: %v", msgs)
		}
	})

	t.Run("at_least_one_warning", func(t *testing.T) {
		hasRelevant := false
		for _, w := range result.Warnings {
			if strings.Contains(w.Message, "1.1") {
				hasRelevant = true
				break
			}
		}
		if !hasRelevant {
			t.Error("expected at least one warning mentioning subtask 1.1")
		}
	})
}

// ---------------------------------------------------------------------------
// TS-02-12: checkMissingSubtaskRefs emits exactly one warning per subtask
// (02-REQ-4.2)
//
// One warning per subtask with the correct message format including the
// joined field names.
// ---------------------------------------------------------------------------

func TestValidateMissingRefsCheckFunction(t *testing.T) {
	group := TaskGroup{
		Id:    1,
		Kind:  TaskGroupKindStandard,
		Title: "Standard group",
		Subtasks: []Subtask{
			{
				Id:              "1.1",
				Title:           "Empty requirement_refs only",
				Details:         []string{},
				RequirementRefs: []string{},
				TestSpecRefs:    []string{"TS-02-1"},
				State:           SubtaskStatePending,
			},
			{
				Id:              "1.2",
				Title:           "Empty test_spec_refs only",
				Details:         []string{},
				RequirementRefs: []string{"02-REQ-1"},
				TestSpecRefs:    []string{},
				State:           SubtaskStatePending,
			},
			{
				Id:              "1.3",
				Title:           "Both refs empty",
				Details:         []string{},
				RequirementRefs: []string{},
				TestSpecRefs:    []string{},
				State:           SubtaskStatePending,
			},
		},
		Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
	}

	t.Run("exactly_three_warnings", func(t *testing.T) {
		defer requireImplemented(t)
		warnings := checkMissingSubtaskRefs(group)
		if len(warnings) != 3 {
			t.Errorf("expected exactly 3 warnings, got %d", len(warnings))
			for _, w := range warnings {
				t.Logf("  warning: %s", w.Message)
			}
		}
	})

	t.Run("correct_message_for_empty_req_refs", func(t *testing.T) {
		defer requireImplemented(t)
		warnings := checkMissingSubtaskRefs(group)
		expected := "Subtask 1.1 has empty requirement_refs — scoped rendering will fall back to full spec dump"
		found := false
		for _, w := range warnings {
			if w.Message == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected warning message %q, got:", expected)
			for _, w := range warnings {
				t.Logf("  %q", w.Message)
			}
		}
	})

	t.Run("correct_message_for_empty_ts_refs", func(t *testing.T) {
		defer requireImplemented(t)
		warnings := checkMissingSubtaskRefs(group)
		expected := "Subtask 1.2 has empty test_spec_refs — scoped rendering will fall back to full spec dump"
		found := false
		for _, w := range warnings {
			if w.Message == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected warning message %q, got:", expected)
			for _, w := range warnings {
				t.Logf("  %q", w.Message)
			}
		}
	})

	t.Run("correct_message_for_both_empty", func(t *testing.T) {
		defer requireImplemented(t)
		warnings := checkMissingSubtaskRefs(group)
		expected := "Subtask 1.3 has empty requirement_refs and test_spec_refs — scoped rendering will fall back to full spec dump"
		found := false
		for _, w := range warnings {
			if w.Message == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected warning message %q, got:", expected)
			for _, w := range warnings {
				t.Logf("  %q", w.Message)
			}
		}
	})

	t.Run("entity_ids_match_subtask_ids", func(t *testing.T) {
		defer requireImplemented(t)
		warnings := checkMissingSubtaskRefs(group)
		ids := map[string]bool{}
		for _, w := range warnings {
			ids[w.EntityID] = true
		}
		for _, expected := range []string{"1.1", "1.2", "1.3"} {
			if !ids[expected] {
				t.Errorf("expected EntityID %q in warnings", expected)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// TS-02-13: checkMissingSubtaskRefs skips WIRING_VERIFICATION groups
// (02-REQ-4.3, 02-REQ-4.E3, 02-REQ-4.E4)
//
// Subtasks in WIRING_VERIFICATION groups produce no warnings even if
// they have empty refs.
// ---------------------------------------------------------------------------

func TestValidateMissingRefsWVSkipped(t *testing.T) {
	t.Run("wiring_verification_group_skipped", func(t *testing.T) {
		defer requireImplemented(t)
		wvGroup := TaskGroup{
			Id:    5,
			Kind:  TaskGroupKindWiringVerification,
			Title: "Wiring verification",
			Subtasks: []Subtask{
				{
					Id:              "5.1",
					Title:           "Verify wiring",
					Details:         []string{},
					RequirementRefs: []string{},
					TestSpecRefs:    []string{},
					State:           SubtaskStatePending,
				},
			},
			Verification: VerificationSubtask{Id: "5.V", Checks: []string{"check"}},
		}
		warnings := checkMissingSubtaskRefs(wvGroup)
		if len(warnings) != 0 {
			t.Errorf("expected 0 warnings for WIRING_VERIFICATION group, got %d", len(warnings))
			for _, w := range warnings {
				t.Logf("  warning: %s", w.Message)
			}
		}
	})

	t.Run("standard_group_with_empty_refs_warns", func(t *testing.T) {
		defer requireImplemented(t)
		stdGroup := TaskGroup{
			Id:    2,
			Kind:  TaskGroupKindStandard,
			Title: "Standard",
			Subtasks: []Subtask{
				{
					Id:              "2.1",
					Title:           "Standard subtask",
					Details:         []string{},
					RequirementRefs: []string{},
					TestSpecRefs:    []string{},
					State:           SubtaskStatePending,
				},
			},
			Verification: VerificationSubtask{Id: "2.V", Checks: []string{"check"}},
		}
		warnings := checkMissingSubtaskRefs(stdGroup)
		if len(warnings) != 1 {
			t.Errorf("expected 1 warning for STANDARD group, got %d", len(warnings))
		}
		if len(warnings) == 1 && warnings[0].EntityID != "2.1" {
			t.Errorf("expected EntityID '2.1', got %q", warnings[0].EntityID)
		}
	})

	t.Run("non_empty_refs_no_warning", func(t *testing.T) {
		defer requireImplemented(t)
		group := TaskGroup{
			Id:    1,
			Kind:  TaskGroupKindStandard,
			Title: "Standard",
			Subtasks: []Subtask{
				{
					Id:              "1.1",
					Title:           "Has refs",
					Details:         []string{},
					RequirementRefs: []string{"02-REQ-1"},
					TestSpecRefs:    []string{"TS-02-1"},
					State:           SubtaskStatePending,
				},
			},
			Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
		}
		warnings := checkMissingSubtaskRefs(group)
		if len(warnings) != 0 {
			t.Errorf("expected 0 warnings for subtask with non-empty refs, got %d", len(warnings))
		}
	})

	t.Run("nil_refs_treated_as_empty", func(t *testing.T) {
		defer requireImplemented(t)
		group := TaskGroup{
			Id:    1,
			Kind:  TaskGroupKindStandard,
			Title: "Standard",
			Subtasks: []Subtask{
				{
					Id:              "1.1",
					Title:           "Has nil refs",
					Details:         []string{},
					RequirementRefs: nil,
					TestSpecRefs:    nil,
					State:           SubtaskStatePending,
				},
			},
			Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
		}
		warnings := checkMissingSubtaskRefs(group)
		if len(warnings) != 1 {
			t.Errorf("expected 1 warning for nil refs (treated as empty), got %d", len(warnings))
		}
	})

	t.Run("placeholder_refs_no_warning", func(t *testing.T) {
		defer requireImplemented(t)
		group := TaskGroup{
			Id:    1,
			Kind:  TaskGroupKindStandard,
			Title: "Standard",
			Subtasks: []Subtask{
				{
					Id:              "1.1",
					Title:           "Has placeholder refs",
					Details:         []string{},
					RequirementRefs: []string{"TBD"},
					TestSpecRefs:    []string{"TBD"},
					State:           SubtaskStatePending,
				},
			},
			Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
		}
		warnings := checkMissingSubtaskRefs(group)
		if len(warnings) != 0 {
			t.Errorf("expected 0 warnings for placeholder refs, got %d", len(warnings))
		}
	})

	t.Run("validate_integration_mixed_groups", func(t *testing.T) {
		groups := []TaskGroup{
			{
				Id:    1,
				Kind:  TaskGroupKindWiringVerification,
				Title: "Wiring",
				Subtasks: []Subtask{
					{
						Id:              "1.1",
						Title:           "WV subtask",
						RequirementRefs: []string{},
						TestSpecRefs:    []string{},
						State:           SubtaskStatePending,
					},
				},
				Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
			},
			{
				Id:    2,
				Kind:  TaskGroupKindStandard,
				Title: "Standard",
				Subtasks: []Subtask{
					{
						Id:              "2.1",
						Title:           "Standard subtask",
						RequirementRefs: []string{},
						TestSpecRefs:    []string{},
						State:           SubtaskStatePending,
					},
				},
				Verification: VerificationSubtask{Id: "2.V", Checks: []string{"check"}},
			},
		}
		spec := buildValidationSpec(groups)
		result := spec.Validate()
		// Should have warning for 2.1 (standard) but NOT 1.1 (wiring_verification)
		found21 := false
		found11 := false
		for _, w := range result.Warnings {
			if w.EntityID == "2.1" {
				found21 = true
			}
			if w.EntityID == "1.1" {
				found11 = true
			}
		}
		if !found21 {
			t.Error("expected warning for standard group subtask 2.1")
		}
		if found11 {
			t.Error("unexpected warning for wiring_verification group subtask 1.1")
		}
	})
}

// ---------------------------------------------------------------------------
// TS-02-14: Warning message uses ' and ' joiner
// (02-REQ-4.4)
//
// The field_names portion of the warning message uses ' and '.join(missing)
// serialisation.
// ---------------------------------------------------------------------------

func TestValidateMissingRefsFieldJoin(t *testing.T) {
	t.Run("both_empty_joined_with_and", func(t *testing.T) {
		defer requireImplemented(t)
		group := TaskGroup{
			Id:    1,
			Kind:  TaskGroupKindStandard,
			Title: "Standard",
			Subtasks: []Subtask{
				{
					Id:              "1.1",
					Title:           "Both empty",
					Details:         []string{},
					RequirementRefs: []string{},
					TestSpecRefs:    []string{},
					State:           SubtaskStatePending,
				},
			},
			Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
		}
		warnings := checkMissingSubtaskRefs(group)
		if len(warnings) != 1 {
			t.Fatalf("expected 1 warning, got %d", len(warnings))
		}
		msg := warnings[0].Message
		if !strings.Contains(msg, "requirement_refs and test_spec_refs") {
			t.Errorf("expected 'requirement_refs and test_spec_refs' in message, got %q", msg)
		}
		// Must NOT be comma-joined
		if strings.Contains(msg, "requirement_refs, test_spec_refs") {
			t.Error("field names should be joined with ' and ', not comma")
		}
	})

	t.Run("only_req_refs_empty_no_joiner", func(t *testing.T) {
		defer requireImplemented(t)
		group := TaskGroup{
			Id:    1,
			Kind:  TaskGroupKindStandard,
			Title: "Standard",
			Subtasks: []Subtask{
				{
					Id:              "1.1",
					Title:           "Only req empty",
					Details:         []string{},
					RequirementRefs: []string{},
					TestSpecRefs:    []string{"TS-02-1"},
					State:           SubtaskStatePending,
				},
			},
			Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
		}
		warnings := checkMissingSubtaskRefs(group)
		if len(warnings) != 1 {
			t.Fatalf("expected 1 warning, got %d", len(warnings))
		}
		msg := warnings[0].Message
		if !strings.Contains(msg, "requirement_refs") {
			t.Errorf("expected 'requirement_refs' in message, got %q", msg)
		}
		if strings.Contains(msg, "test_spec_refs") {
			t.Error("should not mention test_spec_refs when only requirement_refs is empty")
		}
	})

	t.Run("only_ts_refs_empty_no_joiner", func(t *testing.T) {
		defer requireImplemented(t)
		group := TaskGroup{
			Id:    1,
			Kind:  TaskGroupKindStandard,
			Title: "Standard",
			Subtasks: []Subtask{
				{
					Id:              "1.1",
					Title:           "Only ts empty",
					Details:         []string{},
					RequirementRefs: []string{"02-REQ-1"},
					TestSpecRefs:    []string{},
					State:           SubtaskStatePending,
				},
			},
			Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
		}
		warnings := checkMissingSubtaskRefs(group)
		if len(warnings) != 1 {
			t.Fatalf("expected 1 warning, got %d", len(warnings))
		}
		msg := warnings[0].Message
		if !strings.Contains(msg, "test_spec_refs") {
			t.Errorf("expected 'test_spec_refs' in message, got %q", msg)
		}
		if strings.Contains(msg, "requirement_refs") {
			t.Error("should not mention requirement_refs when only test_spec_refs is empty")
		}
	})
}

// ---------------------------------------------------------------------------
// TS-02-18: Python/Go parity — identical warning message format
// (02-REQ-6.2)
//
// Both Python _check_missing_subtask_refs and Go checkMissingSubtaskRefs
// must produce identical warning message strings for the same input.
// ---------------------------------------------------------------------------

func TestValidateMissingRefsMessageParity(t *testing.T) {
	t.Run("exact_format_both_empty", func(t *testing.T) {
		defer requireImplemented(t)
		group := TaskGroup{
			Id:    1,
			Kind:  TaskGroupKindStandard,
			Title: "Standard",
			Subtasks: []Subtask{
				{
					Id:              "1.1",
					Title:           "Both empty",
					Details:         []string{},
					RequirementRefs: []string{},
					TestSpecRefs:    []string{},
					State:           SubtaskStatePending,
				},
			},
			Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
		}
		expected := "Subtask 1.1 has empty requirement_refs and test_spec_refs — scoped rendering will fall back to full spec dump"
		warnings := checkMissingSubtaskRefs(group)
		if len(warnings) != 1 {
			t.Fatalf("expected 1 warning, got %d", len(warnings))
		}
		if warnings[0].Message != expected {
			t.Errorf("message mismatch:\n  got:  %q\n  want: %q", warnings[0].Message, expected)
		}
	})

	t.Run("exact_format_req_only", func(t *testing.T) {
		defer requireImplemented(t)
		group := TaskGroup{
			Id:    1,
			Kind:  TaskGroupKindStandard,
			Title: "Standard",
			Subtasks: []Subtask{
				{
					Id:              "1.1",
					Title:           "Req only",
					Details:         []string{},
					RequirementRefs: []string{},
					TestSpecRefs:    []string{"TS-02-1"},
					State:           SubtaskStatePending,
				},
			},
			Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
		}
		expected := "Subtask 1.1 has empty requirement_refs — scoped rendering will fall back to full spec dump"
		warnings := checkMissingSubtaskRefs(group)
		if len(warnings) != 1 {
			t.Fatalf("expected 1 warning, got %d", len(warnings))
		}
		if warnings[0].Message != expected {
			t.Errorf("message mismatch:\n  got:  %q\n  want: %q", warnings[0].Message, expected)
		}
	})

	t.Run("exact_format_ts_only", func(t *testing.T) {
		defer requireImplemented(t)
		group := TaskGroup{
			Id:    1,
			Kind:  TaskGroupKindStandard,
			Title: "Standard",
			Subtasks: []Subtask{
				{
					Id:              "1.1",
					Title:           "TS only",
					Details:         []string{},
					RequirementRefs: []string{"02-REQ-1"},
					TestSpecRefs:    []string{},
					State:           SubtaskStatePending,
				},
			},
			Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
		}
		expected := "Subtask 1.1 has empty test_spec_refs — scoped rendering will fall back to full spec dump"
		warnings := checkMissingSubtaskRefs(group)
		if len(warnings) != 1 {
			t.Fatalf("expected 1 warning, got %d", len(warnings))
		}
		if warnings[0].Message != expected {
			t.Errorf("message mismatch:\n  got:  %q\n  want: %q", warnings[0].Message, expected)
		}
	})
}

// Compile-time assertion that the import is used.
var _ = strings.Contains
