package afspec

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Test fixture constants — two requirements and two test cases
// ---------------------------------------------------------------------------

const (
	infReqA  = "02-REQ-1"
	infReqB  = "02-REQ-2"
	infCritA = "02-REQ-1.1"
	infCritB = "02-REQ-2.1"
	infTSA   = "TS-02-1"
	infTSB   = "TS-02-2"
)

// ---------------------------------------------------------------------------
// Fixture builders
// ---------------------------------------------------------------------------

// buildInferenceSpec constructs a *Spec suitable for inference testing.
//
// The spec contains two requirements (infReqA with criterion infCritA, and
// infReqB with criterion infCritB), two test cases (infTSA and infTSB), a
// group 1 (tests kind) with populated refs, and a target group (standard
// kind) with one subtask whose title/details are provided by the caller and
// whose requirement_refs and test_spec_refs are empty.
//
// Traceability entries are passed explicitly; an empty slice means no
// traceability data.
func buildInferenceSpec(targetGroup int, traceability []TraceabilityEntry, title string, details []string) *Spec {
	targetSubtask := Subtask{
		Id:              fmt.Sprintf("%d.1", targetGroup),
		Title:           title,
		Details:         details,
		RequirementRefs: []string{},
		TestSpecRefs:    []string{},
		State:           SubtaskStatePending,
		Optional:        false,
	}

	var groups []TaskGroup

	if targetGroup == 1 {
		// When testing group 1, make the target group the tests group
		groups = append(groups, TaskGroup{
			Id:           1,
			Kind:         TaskGroupKindTests,
			Title:        "Tests group",
			Subtasks:     []Subtask{targetSubtask},
			Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
		})
	} else {
		// Add a tests group at id=1 with populated refs
		groups = append(groups, TaskGroup{
			Id:    1,
			Kind:  TaskGroupKindTests,
			Title: "Tests group",
			Subtasks: []Subtask{
				{
					Id:              "1.1",
					Title:           "Write tests",
					Details:         []string{"Write test infrastructure"},
					RequirementRefs: []string{infReqA},
					TestSpecRefs:    []string{infTSA},
					State:           SubtaskStatePending,
					Optional:        false,
				},
			},
			Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
		})
		// Add the target group
		groups = append(groups, TaskGroup{
			Id:           targetGroup,
			Kind:         TaskGroupKindStandard,
			Title:        "Main group",
			Subtasks:     []Subtask{targetSubtask},
			Verification: VerificationSubtask{Id: fmt.Sprintf("%d.V", targetGroup), Checks: []string{"check"}},
		})
	}

	return &Spec{
		SpecID:   "02",
		SpecName: "test_inference",
		Title:    "Test Inference",
		Status:   "draft",
		PRDBody:  "# Test Inference\n\n## Intent\n\nTest inference.\n",
		Requirements: &RequirementsV1Json{
			SchemaVersion: 1,
			SpecId:        "02",
			SpecName:      "test_inference",
			Introduction:  "Test spec for inference.",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements: []Requirement{
				{
					Id:    infReqA,
					Title: "First Requirement",
					UserStory: UserStory{
						Role: "developer", Goal: "test inference", Benefit: "coverage",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          infCritA,
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "performs action A",
						},
					},
					EdgeCases: []Criterion{},
				},
				{
					Id:    infReqB,
					Title: "Second Requirement",
					UserStory: UserStory{
						Role: "developer", Goal: "test inference B", Benefit: "parity",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          infCritB,
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "performs action B",
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
			SpecName:      "test_inference",
			TestCases: []TestCase{
				{
					Id:                  infTSA,
					RequirementId:       infCritA,
					Kind:                TestCaseKindIntegration,
					Description:         "Test case A",
					Preconditions:       []string{},
					Expected:            "scoped result",
					AssertionPseudocode: "assert scoped",
				},
				{
					Id:                  infTSB,
					RequirementId:       infCritB,
					Kind:                TestCaseKindUnit,
					Description:         "Test case B",
					Preconditions:       []string{},
					Expected:            "scoped result",
					AssertionPseudocode: "assert scoped",
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
			SpecName:      "test_inference",
			TaskGroups:    groups,
			Dependencies:  []TaskDependency{},
			TestCommands:  TestCommands{AllTests: "go test", SpecTests: "go test", Linter: "go vet"},
			Traceability:  traceability,
		},
	}
}

// buildInferenceSpecWithExplicitRefs is like buildInferenceSpec but sets
// explicit requirement_refs and test_spec_refs on the target group subtask.
func buildInferenceSpecWithExplicitRefs(targetGroup int, traceability []TraceabilityEntry, reqRefs, tsRefs []string) *Spec {
	spec := buildInferenceSpec(targetGroup, traceability, "Implement feature", []string{"Some details"})
	// Set explicit refs on the target group subtask
	for i, g := range spec.Tasks.TaskGroups {
		if g.Id == targetGroup {
			spec.Tasks.TaskGroups[i].Subtasks[0].RequirementRefs = reqRefs
			spec.Tasks.TaskGroups[i].Subtasks[0].TestSpecRefs = tsRefs
			break
		}
	}
	return spec
}

// ---------------------------------------------------------------------------
// TS-02-1: Traceability inference activates scoped rendering
// (02-REQ-1.1)
//
// When all subtasks in the target group have empty refs,
// render_individual_scoped invokes traceability inference and returns a
// scoped result filtered to the inferred refs.
// ---------------------------------------------------------------------------

func TestInferRefsTraceabilityActivatesScoping(t *testing.T) {
	spec := buildInferenceSpec(3,
		[]TraceabilityEntry{
			{TaskId: "3.1", RequirementId: infReqA, TestSpecId: infTSA},
		},
		"Implement feature", []string{"Some details"},
	)
	result := spec.RenderIndividualScoped(3)
	unscoped := spec.RenderIndividual()

	t.Run("result_contains_inferred_requirement", func(t *testing.T) {
		assertContains(t, result["requirements"], infReqA, "inferred requirement")
	})

	t.Run("result_contains_inferred_test_spec", func(t *testing.T) {
		assertContains(t, result["test_spec"], infTSA, "inferred test spec")
	})

	t.Run("excludes_unreferenced_test_spec", func(t *testing.T) {
		// TS-B should not appear in the scoped test spec section
		assertNotContains(t, result["test_spec"], infTSB, "unreferenced test spec excluded")
	})

	t.Run("scoped_output_differs_from_unscoped", func(t *testing.T) {
		if result["requirements"] == unscoped["requirements"] {
			t.Error("expected scoped requirements to differ from unscoped")
		}
		if result["test_spec"] == unscoped["test_spec"] {
			t.Error("expected scoped test_spec to differ from unscoped")
		}
	})
}

// ---------------------------------------------------------------------------
// TS-02-2: inferRefsFromTraceability filters matching entries
// (02-REQ-1.2)
//
// Returns only requirement_id and test_spec_id values from entries whose
// task_id starts with the target group prefix.
// ---------------------------------------------------------------------------

func TestInferRefsFromTraceability(t *testing.T) {
	spec := buildInferenceSpec(3,
		[]TraceabilityEntry{
			{TaskId: "3.1", RequirementId: infReqA, TestSpecId: infTSA},
			{TaskId: "5.1", RequirementId: infReqB, TestSpecId: infTSB},
		},
		"Implement feature", nil,
	)

	t.Run("collects_matching_requirement_id", func(t *testing.T) {
		defer requireImplemented(t)
		reqRefs, _ := inferRefsFromTraceability(spec, 3)
		found := false
		for _, r := range reqRefs {
			if r == infReqA {
				found = true
			}
		}
		if !found {
			t.Errorf("expected %s in req refs, got %v", infReqA, reqRefs)
		}
	})

	t.Run("collects_matching_test_spec_id", func(t *testing.T) {
		defer requireImplemented(t)
		_, tsRefs := inferRefsFromTraceability(spec, 3)
		found := false
		for _, r := range tsRefs {
			if r == infTSA {
				found = true
			}
		}
		if !found {
			t.Errorf("expected %s in ts refs, got %v", infTSA, tsRefs)
		}
	})

	t.Run("excludes_non_matching_group_entries", func(t *testing.T) {
		defer requireImplemented(t)
		reqRefs, tsRefs := inferRefsFromTraceability(spec, 3)
		for _, r := range reqRefs {
			if r == infReqB {
				t.Errorf("should not contain %s from group 5", infReqB)
			}
		}
		for _, r := range tsRefs {
			if r == infTSB {
				t.Errorf("should not contain %s from group 5", infTSB)
			}
		}
	})

	t.Run("empty_traceability_returns_empty", func(t *testing.T) {
		defer requireImplemented(t)
		emptySpec := buildInferenceSpec(3, []TraceabilityEntry{}, "Implement feature", nil)
		reqRefs, tsRefs := inferRefsFromTraceability(emptySpec, 3)
		if len(reqRefs) != 0 || len(tsRefs) != 0 {
			t.Errorf("expected empty collections, got reqRefs=%v, tsRefs=%v", reqRefs, tsRefs)
		}
	})

	t.Run("multiple_matching_entries", func(t *testing.T) {
		defer requireImplemented(t)
		multiSpec := buildInferenceSpec(3,
			[]TraceabilityEntry{
				{TaskId: "3.1", RequirementId: infReqA, TestSpecId: infTSA},
				{TaskId: "3.2", RequirementId: infReqB, TestSpecId: infTSB},
			},
			"Implement feature", nil,
		)
		reqRefs, tsRefs := inferRefsFromTraceability(multiSpec, 3)
		if len(reqRefs) < 2 {
			t.Errorf("expected at least 2 req refs, got %v", reqRefs)
		}
		if len(tsRefs) < 2 {
			t.Errorf("expected at least 2 ts refs, got %v", tsRefs)
		}
	})

	t.Run("empty_requirement_id_skipped", func(t *testing.T) {
		defer requireImplemented(t)
		partialSpec := buildInferenceSpec(3,
			[]TraceabilityEntry{
				{TaskId: "3.1", RequirementId: "", TestSpecId: infTSA},
			},
			"Implement feature", nil,
		)
		reqRefs, tsRefs := inferRefsFromTraceability(partialSpec, 3)
		for _, r := range reqRefs {
			if r == "" {
				t.Error("empty requirement_id should be skipped")
			}
		}
		found := false
		for _, r := range tsRefs {
			if r == infTSA {
				found = true
			}
		}
		if !found {
			t.Errorf("expected %s in ts refs even with empty requirement_id", infTSA)
		}
	})

	t.Run("empty_test_spec_id_skipped", func(t *testing.T) {
		defer requireImplemented(t)
		partialSpec := buildInferenceSpec(3,
			[]TraceabilityEntry{
				{TaskId: "3.1", RequirementId: infReqA, TestSpecId: ""},
			},
			"Implement feature", nil,
		)
		reqRefs, tsRefs := inferRefsFromTraceability(partialSpec, 3)
		for _, r := range tsRefs {
			if r == "" {
				t.Error("empty test_spec_id should be skipped")
			}
		}
		found := false
		for _, r := range reqRefs {
			if r == infReqA {
				found = true
			}
		}
		if !found {
			t.Errorf("expected %s in req refs even with empty test_spec_id", infReqA)
		}
	})
}

// ---------------------------------------------------------------------------
// TS-02-3: Traceability inference short-circuits text inference
// (02-REQ-1.3)
//
// When traceability inference yields at least one ref, scoped rendering
// activates immediately without attempting text inference. Go omits the
// INFO log that Python emits.
// ---------------------------------------------------------------------------

func TestInferRefsTraceabilityShortCircuits(t *testing.T) {
	t.Run("scoped_result_when_traceability_has_refs", func(t *testing.T) {
		spec := buildInferenceSpec(3,
			[]TraceabilityEntry{
				{TaskId: "3.1", RequirementId: infReqA, TestSpecId: infTSA},
			},
			"Implement feature", nil,
		)
		result := spec.RenderIndividualScoped(3)
		unscoped := spec.RenderIndividual()
		if result["requirements"] == unscoped["requirements"] {
			t.Error("expected scoped requirements (not full dump) when traceability has refs")
		}
	})

	t.Run("empty_traceability_falls_through_to_unscoped", func(t *testing.T) {
		spec := buildInferenceSpec(3,
			[]TraceabilityEntry{}, // no traceability
			"Do some work",        // no IDs in text
			[]string{"Plain detail with no IDs"},
		)
		result := spec.RenderIndividualScoped(3)
		unscoped := spec.RenderIndividual()
		// With no inference possible, requirements and test_spec should match unscoped
		if result["requirements"] != unscoped["requirements"] {
			t.Error("expected requirements to match unscoped when traceability is empty and no text IDs")
		}
		if result["test_spec"] != unscoped["test_spec"] {
			t.Error("expected test_spec to match unscoped when traceability is empty and no text IDs")
		}
	})

	t.Run("no_traceability_for_target_group_proceeds", func(t *testing.T) {
		// Traceability entries exist but none match group 3 prefix
		spec := buildInferenceSpec(3,
			[]TraceabilityEntry{
				{TaskId: "5.1", RequirementId: infReqB, TestSpecId: infTSB},
			},
			"Do some work",
			[]string{"Plain detail"},
		)
		result := spec.RenderIndividualScoped(3)
		unscoped := spec.RenderIndividual()
		if result["requirements"] != unscoped["requirements"] {
			t.Error("expected unscoped requirements when no traceability matches target group")
		}
	})
}

// ---------------------------------------------------------------------------
// TS-02-4: Text-based inference activates scoped rendering
// (02-REQ-2.1)
//
// When traceability inference returns empty, render_individual_scoped
// invokes text-based inference and returns a scoped result from validated
// regex matches found in subtask title and details.
// ---------------------------------------------------------------------------

func TestInferRefsTextActivatesScoping(t *testing.T) {
	t.Run("req_id_in_title_activates_scoping", func(t *testing.T) {
		spec := buildInferenceSpec(2,
			[]TraceabilityEntry{}, // no traceability
			"Implement "+infReqA+" logic",
			nil,
		)
		result := spec.RenderIndividualScoped(2)
		assertContains(t, result["requirements"], infReqA, "text-inferred requirement")
		assertNotContains(t, result["test_spec"], infTSB, "unreferenced test spec excluded")
	})

	t.Run("req_id_in_details_activates_scoping", func(t *testing.T) {
		spec := buildInferenceSpec(2,
			[]TraceabilityEntry{},
			"Implement feature",
			[]string{"Must satisfy " + infReqA},
		)
		result := spec.RenderIndividualScoped(2)
		unscoped := spec.RenderIndividual()
		if result["requirements"] == unscoped["requirements"] {
			t.Error("expected scoped requirements from req ID in details")
		}
	})

	t.Run("ts_id_in_title_activates_scoping", func(t *testing.T) {
		spec := buildInferenceSpec(2,
			[]TraceabilityEntry{},
			"Cover "+infTSA+" tests",
			nil,
		)
		result := spec.RenderIndividualScoped(2)
		assertContains(t, result["test_spec"], infTSA, "text-inferred test spec")
		assertNotContains(t, result["test_spec"], infTSB, "unreferenced test spec excluded")
	})

	t.Run("ts_id_in_details_activates_scoping", func(t *testing.T) {
		spec := buildInferenceSpec(2,
			[]TraceabilityEntry{},
			"Implement feature",
			[]string{"See " + infTSA + " for coverage"},
		)
		result := spec.RenderIndividualScoped(2)
		assertContains(t, result["test_spec"], infTSA, "text-inferred test spec from details")
	})

	t.Run("scoped_result_differs_from_unscoped", func(t *testing.T) {
		spec := buildInferenceSpec(2,
			[]TraceabilityEntry{},
			"Implement "+infReqA+" logic",
			nil,
		)
		result := spec.RenderIndividualScoped(2)
		unscoped := spec.RenderIndividual()
		if result["requirements"] == unscoped["requirements"] {
			t.Error("expected scoped requirements to differ from unscoped when text inference finds IDs")
		}
	})
}

// ---------------------------------------------------------------------------
// TS-02-5: Regex patterns are package-level compiled constants
// (02-REQ-2.2)
//
// reqIDRe and tsIDRe are package-level *regexp.Regexp variables
// initialized via regexp.MustCompile at package init time.
// ---------------------------------------------------------------------------

func TestInferRefsRegexCompiled(t *testing.T) {
	t.Run("reqIDRe_is_non_nil", func(t *testing.T) {
		if reqIDRe == nil {
			t.Fatal("reqIDRe is nil; expected a compiled *regexp.Regexp at package level")
		}
		if _, ok := any(reqIDRe).(*regexp.Regexp); !ok {
			t.Fatal("reqIDRe is not a *regexp.Regexp")
		}
	})

	t.Run("tsIDRe_is_non_nil", func(t *testing.T) {
		if tsIDRe == nil {
			t.Fatal("tsIDRe is nil; expected a compiled *regexp.Regexp at package level")
		}
		if _, ok := any(tsIDRe).(*regexp.Regexp); !ok {
			t.Fatal("tsIDRe is not a *regexp.Regexp")
		}
	})

	t.Run("reqIDRe_matches_requirement_id", func(t *testing.T) {
		match := reqIDRe.FindString("Implement 02-REQ-1 logic")
		if match == "" {
			t.Error("expected reqIDRe to match '02-REQ-1' in text")
		}
	})

	t.Run("reqIDRe_matches_criterion_id", func(t *testing.T) {
		match := reqIDRe.FindString("See criterion 02-REQ-1.1 for details")
		if match == "" {
			t.Error("expected reqIDRe to match '02-REQ-1.1' in text")
		}
	})

	t.Run("reqIDRe_matches_edge_case_id", func(t *testing.T) {
		match := reqIDRe.FindString("Edge case 02-REQ-1.E1 applies")
		if match == "" {
			t.Error("expected reqIDRe to match '02-REQ-1.E1' in text")
		}
	})

	t.Run("tsIDRe_matches_test_spec_id", func(t *testing.T) {
		match := tsIDRe.FindString("See TS-02-1 for tests")
		if match == "" {
			t.Error("expected tsIDRe to match 'TS-02-1' in text")
		}
	})
}

// ---------------------------------------------------------------------------
// TS-02-6: inferRefsFromSubtaskText filters to known IDs
// (02-REQ-2.3, 02-REQ-2.E1, 02-REQ-2.E2, 02-REQ-2.E3)
//
// Scans title and all details strings, collects regex matches, and filters
// to only IDs present in the spec. Invalid matches are discarded.
// ---------------------------------------------------------------------------

func TestInferRefsFromSubtaskText(t *testing.T) {
	t.Run("valid_req_and_ts_ids_returned", func(t *testing.T) {
		defer requireImplemented(t)
		spec := buildInferenceSpec(2,
			[]TraceabilityEntry{},
			"Work on "+infReqA,
			[]string{"See " + infTSA + " for tests", "Also 99-REQ-999 is mentioned"},
		)
		reqRefs, tsRefs := inferRefsFromSubtaskText(spec, 2)
		if !slices.Contains(reqRefs, infReqA) {
			t.Errorf("expected %s in req refs, got %v", infReqA, reqRefs)
		}
		if !slices.Contains(tsRefs, infTSA) {
			t.Errorf("expected %s in ts refs, got %v", infTSA, tsRefs)
		}
	})

	t.Run("unknown_req_id_discarded", func(t *testing.T) {
		defer requireImplemented(t)
		spec := buildInferenceSpec(2,
			[]TraceabilityEntry{},
			"Work on 99-REQ-999",
			[]string{"Also 99-REQ-888"},
		)
		reqRefs, _ := inferRefsFromSubtaskText(spec, 2)
		if slices.Contains(reqRefs, "99-REQ-999") {
			t.Error("99-REQ-999 should be discarded (not in spec)")
		}
		if slices.Contains(reqRefs, "99-REQ-888") {
			t.Error("99-REQ-888 should be discarded (not in spec)")
		}
	})

	t.Run("unknown_ts_id_discarded", func(t *testing.T) {
		defer requireImplemented(t)
		spec := buildInferenceSpec(2,
			[]TraceabilityEntry{},
			"See TS-99-1 for tests",
			nil,
		)
		_, tsRefs := inferRefsFromSubtaskText(spec, 2)
		if slices.Contains(tsRefs, "TS-99-1") {
			t.Error("TS-99-1 should be discarded (not in spec)")
		}
	})

	t.Run("unmentioned_spec_ids_not_inferred", func(t *testing.T) {
		defer requireImplemented(t)
		spec := buildInferenceSpec(2,
			[]TraceabilityEntry{},
			"Work on "+infReqA,
			[]string{"See " + infTSA + " for tests"},
		)
		reqRefs, tsRefs := inferRefsFromSubtaskText(spec, 2)
		// REQ-B and TS-B exist in spec but are not mentioned in text
		if slices.Contains(reqRefs, infReqB) {
			t.Errorf("%s should not be inferred (not in subtask text)", infReqB)
		}
		if slices.Contains(tsRefs, infTSB) {
			t.Errorf("%s should not be inferred (not in subtask text)", infTSB)
		}
	})

	t.Run("all_invalid_matches_fall_through_to_unscoped", func(t *testing.T) {
		// Regex matches exist but none correspond to known IDs
		spec := buildInferenceSpec(2,
			[]TraceabilityEntry{},
			"Work on 99-REQ-999",
			[]string{"See TS-99-1 for tests"},
		)
		result := spec.RenderIndividualScoped(2)
		unscoped := spec.RenderIndividual()
		if result["requirements"] != unscoped["requirements"] {
			t.Error("expected unscoped requirements when all text matches are invalid")
		}
		if result["test_spec"] != unscoped["test_spec"] {
			t.Error("expected unscoped test_spec when all text matches are invalid")
		}
	})

	t.Run("no_regex_matches_returns_empty", func(t *testing.T) {
		defer requireImplemented(t)
		spec := buildInferenceSpec(2,
			[]TraceabilityEntry{},
			"Plain title with no IDs",
			[]string{"Plain detail with no IDs"},
		)
		reqRefs, tsRefs := inferRefsFromSubtaskText(spec, 2)
		if len(reqRefs) != 0 {
			t.Errorf("expected empty req refs, got %v", reqRefs)
		}
		if len(tsRefs) != 0 {
			t.Errorf("expected empty ts refs, got %v", tsRefs)
		}
	})

	t.Run("empty_details_scans_title_only", func(t *testing.T) {
		defer requireImplemented(t)
		spec := buildInferenceSpec(2,
			[]TraceabilityEntry{},
			"Implement "+infReqA+" logic",
			[]string{}, // empty details
		)
		reqRefs, _ := inferRefsFromSubtaskText(spec, 2)
		if !slices.Contains(reqRefs, infReqA) {
			t.Errorf("expected %s from title scan even with empty details, got %v", infReqA, reqRefs)
		}
	})
}

// ---------------------------------------------------------------------------
// TS-02-7: Text inference activates and Go has no logging
// (02-REQ-2.4, 02-REQ-6.3)
//
// When text inference yields at least one validated ref, scoped rendering
// activates. Go has no logging infrastructure, so no log output is
// produced (unlike Python which emits an INFO log).
// ---------------------------------------------------------------------------

func TestInferRefsTextNoLogging(t *testing.T) {
	t.Run("scoped_result_returned_without_panic", func(t *testing.T) {
		spec := buildInferenceSpec(2,
			[]TraceabilityEntry{},
			"Handle "+infReqA,
			nil,
		)
		result := spec.RenderIndividualScoped(2)
		if result == nil {
			t.Fatal("RenderIndividualScoped returned nil")
		}
		if len(result) == 0 {
			t.Fatal("RenderIndividualScoped returned empty map")
		}
	})

	t.Run("scoped_to_text_inferred_refs", func(t *testing.T) {
		spec := buildInferenceSpec(2,
			[]TraceabilityEntry{},
			"Handle "+infReqA,
			nil,
		)
		result := spec.RenderIndividualScoped(2)
		assertContains(t, result["requirements"], infReqA, "text-inferred requirement")
		assertNotContains(t, result["test_spec"], infTSB, "unreferenced test spec excluded")
	})
}

// ---------------------------------------------------------------------------
// TS-02-8: Partial inference — only requirement refs inferred
// (02-REQ-3.1)
//
// When inference yields refs for only one ref type, scoped rendering
// activates for that type and the other type is fully rendered.
// ---------------------------------------------------------------------------

func TestInferRefsPartialReqOnly(t *testing.T) {
	// Traceability has requirement_id but empty test_spec_id
	spec := buildInferenceSpec(4,
		[]TraceabilityEntry{
			{TaskId: "4.1", RequirementId: infReqA, TestSpecId: ""},
		},
		"Implement feature", nil,
	)

	t.Run("requirements_scoped_to_inferred_req", func(t *testing.T) {
		result := spec.RenderIndividualScoped(4)
		assertContains(t, result["requirements"], infReqA, "inferred requirement")
	})

	t.Run("unreferenced_requirement_excluded", func(t *testing.T) {
		result := spec.RenderIndividualScoped(4)
		unscoped := spec.RenderIndividual()
		if result["requirements"] == unscoped["requirements"] {
			t.Error("expected scoped requirements to differ from unscoped (req-only inference)")
		}
	})

	t.Run("test_spec_fully_rendered", func(t *testing.T) {
		result := spec.RenderIndividualScoped(4)
		unscoped := spec.RenderIndividual()
		// All test specs should be present (full render)
		assertContains(t, result["test_spec"], infTSA, "test spec A present in full render")
		assertContains(t, result["test_spec"], infTSB, "test spec B present in full render")
		if result["test_spec"] != unscoped["test_spec"] {
			t.Error("expected test_spec to match unscoped when no ts refs inferred")
		}
	})
}

// ---------------------------------------------------------------------------
// TS-02-8 mirror: Partial inference — only test spec refs inferred
// (02-REQ-3.1)
// ---------------------------------------------------------------------------

func TestInferRefsPartialTSOnly(t *testing.T) {
	// Traceability has test_spec_id but empty requirement_id
	spec := buildInferenceSpec(4,
		[]TraceabilityEntry{
			{TaskId: "4.1", RequirementId: "", TestSpecId: infTSA},
		},
		"Implement feature", nil,
	)

	t.Run("test_spec_scoped_to_inferred_ts", func(t *testing.T) {
		result := spec.RenderIndividualScoped(4)
		assertContains(t, result["test_spec"], infTSA, "inferred test spec")
	})

	t.Run("unreferenced_test_spec_excluded", func(t *testing.T) {
		result := spec.RenderIndividualScoped(4)
		assertNotContains(t, result["test_spec"], infTSB, "unreferenced test spec excluded")
	})

	t.Run("requirements_fully_rendered", func(t *testing.T) {
		result := spec.RenderIndividualScoped(4)
		unscoped := spec.RenderIndividual()
		// All requirements should be present (full render)
		assertContains(t, result["requirements"], infReqA, "requirement A in full render")
		assertContains(t, result["requirements"], infReqB, "requirement B in full render")
		if result["requirements"] != unscoped["requirements"] {
			t.Error("expected requirements to match unscoped when no req refs inferred")
		}
	})

	t.Run("test_spec_differs_from_unscoped", func(t *testing.T) {
		result := spec.RenderIndividualScoped(4)
		unscoped := spec.RenderIndividual()
		if result["test_spec"] == unscoped["test_spec"] {
			t.Error("expected scoped test_spec to differ from unscoped (ts-only inference)")
		}
	})
}

// ---------------------------------------------------------------------------
// TS-02-9, TS-02-10: Unscoped fallback with scoped tasks
// (02-REQ-3.2, 02-REQ-3.3)
//
// When both inference strategies return empty, render_individual_scoped
// falls back to full unscoped rendering for requirements and test spec,
// but still scopes tasks via renderScopedTasks. This also tests the Go
// fallback bug fix: replacing return s.RenderIndividual() with logic
// that calls s.renderScopedTasks(targetGroup).
// ---------------------------------------------------------------------------

func TestInferRefsFallbackScopedTasks(t *testing.T) {
	// Build spec with group 2 having no refs, no traceability, no ID text
	spec := buildInferenceSpec(2,
		[]TraceabilityEntry{},
		"Do some work",
		[]string{"Plain detail with no IDs"},
	)

	t.Run("full_requirements_in_fallback", func(t *testing.T) {
		result := spec.RenderIndividualScoped(2)
		assertContains(t, result["requirements"], infReqA, "requirement A in fallback")
		assertContains(t, result["requirements"], infReqB, "requirement B in fallback")
	})

	t.Run("full_test_spec_in_fallback", func(t *testing.T) {
		result := spec.RenderIndividualScoped(2)
		assertContains(t, result["test_spec"], infTSA, "test spec A in fallback")
		assertContains(t, result["test_spec"], infTSB, "test spec B in fallback")
	})

	t.Run("requirements_match_unscoped", func(t *testing.T) {
		result := spec.RenderIndividualScoped(2)
		unscoped := spec.RenderIndividual()
		if result["requirements"] != unscoped["requirements"] {
			t.Error("expected requirements to match unscoped in fallback")
		}
	})

	t.Run("test_spec_matches_unscoped", func(t *testing.T) {
		result := spec.RenderIndividualScoped(2)
		unscoped := spec.RenderIndividual()
		if result["test_spec"] != unscoped["test_spec"] {
			t.Error("expected test_spec to match unscoped in fallback")
		}
	})

	t.Run("tasks_scoped_to_target_group", func(t *testing.T) {
		result := spec.RenderIndividualScoped(2)
		tasksSection := result["tasks"]
		// Target group subtask should be present in full detail
		assertContains(t, tasksSection, "2.1", "target group subtask ID")
		assertContains(t, tasksSection, "Do some work", "target group subtask title")
		// Group 1's subtask should NOT appear in full detail (only summarised)
		assertNotContains(t, tasksSection, "1.1", "group 1 subtask should be summarised, not in full detail")
	})

	t.Run("tasks_differ_from_fully_unscoped", func(t *testing.T) {
		result := spec.RenderIndividualScoped(2)
		unscoped := spec.RenderIndividual()
		if result["tasks"] == unscoped["tasks"] {
			t.Error("expected scoped tasks to differ from fully unscoped tasks (fallback bug fix)")
		}
	})

	t.Run("tasks_match_renderScopedTasks", func(t *testing.T) {
		result := spec.RenderIndividualScoped(2)
		expected := spec.renderScopedTasks(2)
		if result["tasks"] != expected {
			t.Errorf("expected tasks section to match renderScopedTasks(2)\ngot:  %s\nwant: %s",
				truncate(result["tasks"], 200), truncate(expected, 200))
		}
	})
}

// ---------------------------------------------------------------------------
// TS-02-17: Inference chain order — traceability first
// (02-REQ-6.1)
//
// Both Python and Go follow the same chain: explicit refs → traceability →
// text → fallback. When traceability yields refs, text inference is not
// attempted.
// ---------------------------------------------------------------------------

func TestInferRefsChainOrder(t *testing.T) {
	// Traceability points to REQ-A/TS-A; subtask text mentions REQ-B
	spec := buildInferenceSpec(3,
		[]TraceabilityEntry{
			{TaskId: "3.1", RequirementId: infReqA, TestSpecId: infTSA},
		},
		"Handle "+infReqB, // text mentions REQ-B
		nil,
	)

	t.Run("traceability_refs_used_over_text", func(t *testing.T) {
		result := spec.RenderIndividualScoped(3)
		// Traceability refs (TS-A) should be in scoped output
		assertContains(t, result["test_spec"], infTSA, "traceability-inferred TS-A")
		// Text-inferred refs (TS-B from REQ-B mention) should NOT be in scoped output
		assertNotContains(t, result["test_spec"], infTSB, "text-inferred TS-B excluded by traceability short-circuit")
	})

	t.Run("explicit_refs_skip_inference", func(t *testing.T) {
		// Subtasks have explicit refs pointing to REQ-A/TS-A; traceability
		// points to REQ-B/TS-B. Explicit refs should win.
		explicitSpec := buildInferenceSpecWithExplicitRefs(3,
			[]TraceabilityEntry{
				{TaskId: "3.1", RequirementId: infReqB, TestSpecId: infTSB},
			},
			[]string{infReqA}, []string{infTSA},
		)
		result := explicitSpec.RenderIndividualScoped(3)
		// Explicit refs (TS-A) should be used
		assertContains(t, result["test_spec"], infTSA, "explicit TS-A")
		// Traceability refs (TS-B) should NOT be used
		assertNotContains(t, result["test_spec"], infTSB, "traceability TS-B excluded by explicit refs")
	})
}

// ---------------------------------------------------------------------------
// TS-02-19: Go RenderIndividualScoped has no logging
// (02-REQ-6.3)
//
// Verifies that inference returns a scoped result without any log output
// or panics — Go has no logging infrastructure for inference.
// ---------------------------------------------------------------------------

func TestInferRefsGoNoLoggingTraceability(t *testing.T) {
	spec := buildInferenceSpec(3,
		[]TraceabilityEntry{
			{TaskId: "3.1", RequirementId: infReqA, TestSpecId: infTSA},
		},
		"Implement feature", nil,
	)
	result := spec.RenderIndividualScoped(3)
	if result == nil {
		t.Fatal("RenderIndividualScoped returned nil")
	}
	if len(result) == 0 {
		t.Fatal("RenderIndividualScoped returned empty map")
	}
	// Verify scoped (only inferred req present)
	assertContains(t, result["test_spec"], infTSA, "inferred test spec present")
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

func TestInferRefsNonexistentGroupFallback(t *testing.T) {
	// 02-REQ-3.E1: target_group doesn't match any TaskGroup
	spec := buildInferenceSpec(3,
		[]TraceabilityEntry{},
		"Implement feature", nil,
	)
	// Should not panic
	result := spec.RenderIndividualScoped(999)
	if result == nil {
		t.Fatal("expected non-nil result for nonexistent group")
	}
}

func TestInferRefsNilTasksFallback(t *testing.T) {
	// 02-REQ-3.E2: Spec with nil Tasks
	spec := buildInferenceSpec(3,
		[]TraceabilityEntry{},
		"Implement feature", nil,
	)
	spec.Tasks = nil
	// Should not panic
	result := spec.RenderIndividualScoped(3)
	if result == nil {
		t.Fatal("expected non-nil result when Tasks is nil")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// Compile-time assertion that the imports are used.
var (
	_ = fmt.Sprintf
	_ = regexp.MustCompile
	_ = strings.Contains
	_ = slices.Contains[[]string]
)
