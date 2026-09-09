package afspec

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// makeReqWithCriteria builds a RequirementsV1Json containing a single
// requirement with the supplied acceptance-criterion IDs and edge-case IDs.
func makeReqWithCriteria(reqID string, acIDs []string, ecIDs []string) *RequirementsV1Json {
	criteria := make([]Criterion, len(acIDs))
	for i, id := range acIDs {
		criteria[i] = Criterion{
			Id:          id,
			EarsPattern: CriterionEarsPatternUbiquitous,
			System:      "sys",
			Action:      "act",
		}
	}
	errCond := "some condition"
	edgeCases := make([]Criterion, len(ecIDs))
	for i, id := range ecIDs {
		edgeCases[i] = Criterion{
			Id:             id,
			EarsPattern:    CriterionEarsPatternUnwanted,
			ErrorCondition: &errCond,
			System:         "sys",
			Action:         "handle it",
		}
	}
	return &RequirementsV1Json{
		SchemaVersion: 1,
		SpecId:        "05",
		SpecName:      "test",
		Introduction:  "Test",
		Glossary:      RequirementsV1JsonGlossary{},
		Requirements: []Requirement{
			{
				Id:    reqID,
				Title: "Test Requirement",
				UserStory: UserStory{
					Role:    "dev",
					Goal:    "goal",
					Benefit: "benefit",
				},
				AcceptanceCriteria: criteria,
				EdgeCases:          edgeCases,
			},
		},
		CorrectnessProperties: []CorrectnessProperty{},
		ExecutionPaths:        []ExecutionPath{},
		ErrorHandling:         []ErrorHandlingEntry{},
	}
}

// makeTestSpecWithTestCase builds a TestSpecV1Json with a single test case
// referencing the given criterion ID.
func makeTestSpecWithTestCase(criterionID string) *TestSpecV1Json {
	return &TestSpecV1Json{
		SchemaVersion: 1,
		SpecId:        "05",
		SpecName:      "test",
		TestCases: []TestCase{
			{
				Id:                  "TS-05-1",
				RequirementId:       criterionID,
				Kind:                TestCaseKindUnit,
				Description:         "Covers one criterion",
				Preconditions:       []string{},
				Expected:            "ok",
				AssertionPseudocode: "assert true",
			},
		},
		PropertyTests: []PropertyTest{},
		EdgeCaseTests: []EdgeCaseTest{},
		SmokeTests:    []SmokeTest{},
		Coverage:      Coverage{},
	}
}

// makeTestSpecWithEdgeCaseTest builds a TestSpecV1Json with a single edge
// case test referencing the given criterion ID.
func makeTestSpecWithEdgeCaseTest(criterionID string) *TestSpecV1Json {
	return &TestSpecV1Json{
		SchemaVersion: 1,
		SpecId:        "05",
		SpecName:      "test",
		TestCases:     []TestCase{},
		PropertyTests: []PropertyTest{},
		EdgeCaseTests: []EdgeCaseTest{
			{
				Id:                  "TS-05-E1",
				RequirementId:       criterionID,
				Kind:                EdgeCaseTestKindUnit,
				Description:         "Covers one edge case criterion",
				Preconditions:       []string{},
				Expected:            "ok",
				AssertionPseudocode: "assert true",
			},
		},
		SmokeTests: []SmokeTest{},
		Coverage:   Coverage{},
	}
}

// TestComputeCoverageStruct_PartialCriterion verifies that when a requirement
// has multiple criteria but only one is tested, only the tested criterion ID
// appears in RequirementsCovered and the untested criterion IDs appear in Gaps.
// No parent requirement ID appears in either field.
// Test Spec: TS-NS-1, Requirement: NS-REQ-1.1
func TestComputeCoverageStruct_PartialCriterion(t *testing.T) {
	defer requireImplemented(t)

	req := makeReqWithCriteria("05-REQ-1", []string{"05-REQ-1.1", "05-REQ-1.2", "05-REQ-1.3"}, nil)
	ts := makeTestSpecWithTestCase("05-REQ-1.1")

	cov := ts.ComputeCoverageStruct(req)

	// Only the tested criterion ID should appear in RequirementsCovered.
	if diff := cmp.Diff([]string{"05-REQ-1.1"}, cov.RequirementsCovered); diff != "" {
		t.Errorf("RequirementsCovered mismatch (-want +got):\n%s", diff)
	}

	// Untested criteria appear in Gaps (order: criteria order in requirements).
	wantGaps := []string{"05-REQ-1.2", "05-REQ-1.3"}
	if diff := cmp.Diff(wantGaps, cov.Gaps, cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
		t.Errorf("Gaps mismatch (-want +got):\n%s", diff)
	}

	// Parent requirement ID must NOT appear in either field.
	for _, id := range append(cov.RequirementsCovered, cov.Gaps...) {
		if id == "05-REQ-1" {
			t.Errorf("parent requirement ID %q must not appear in coverage output; only criterion IDs", id)
		}
	}
}

// TestComputeCoverageStruct_EdgeCaseCriterion verifies that when an edge case
// criterion is referenced by an EdgeCaseTest, its criterion ID appears
// individually in RequirementsCovered, and the uncovered acceptance criterion
// appears in Gaps.
// Test Spec: TS-NS-2, Requirement: NS-REQ-2.1
func TestComputeCoverageStruct_EdgeCaseCriterion(t *testing.T) {
	defer requireImplemented(t)

	req := makeReqWithCriteria("05-REQ-1", []string{"05-REQ-1.1"}, []string{"05-REQ-1.E1"})
	ts := makeTestSpecWithEdgeCaseTest("05-REQ-1.E1")

	cov := ts.ComputeCoverageStruct(req)

	// Edge case criterion ID must appear in RequirementsCovered.
	if diff := cmp.Diff([]string{"05-REQ-1.E1"}, cov.RequirementsCovered); diff != "" {
		t.Errorf("RequirementsCovered mismatch (-want +got):\n%s", diff)
	}

	// The uncovered acceptance criterion must appear in Gaps.
	foundAC := false
	for _, g := range cov.Gaps {
		if g == "05-REQ-1.1" {
			foundAC = true
		}
		if g == "05-REQ-1" {
			t.Errorf("parent requirement ID %q must not appear in Gaps", g)
		}
	}
	if !foundAC {
		t.Errorf("expected Gaps to contain %q, got %v", "05-REQ-1.1", cov.Gaps)
	}
}

// TestComputeCoverageStruct_FullCoverage verifies that when every criterion has
// a corresponding test, RequirementsCovered contains all criterion IDs and Gaps
// is empty.
// Test Spec: TS-NS-3, Requirement: NS-REQ-3.1
func TestComputeCoverageStruct_FullCoverage(t *testing.T) {
	defer requireImplemented(t)

	req := makeReqWithCriteria("05-REQ-1", []string{"05-REQ-1.1", "05-REQ-1.2"}, nil)

	ts := &TestSpecV1Json{
		SchemaVersion: 1,
		SpecId:        "05",
		SpecName:      "test",
		TestCases: []TestCase{
			{
				Id:                  "TS-05-1",
				RequirementId:       "05-REQ-1.1",
				Kind:                TestCaseKindUnit,
				Description:         "Covers criterion 1",
				Preconditions:       []string{},
				Expected:            "ok",
				AssertionPseudocode: "assert true",
			},
			{
				Id:                  "TS-05-2",
				RequirementId:       "05-REQ-1.2",
				Kind:                TestCaseKindUnit,
				Description:         "Covers criterion 2",
				Preconditions:       []string{},
				Expected:            "ok",
				AssertionPseudocode: "assert true",
			},
		},
		PropertyTests: []PropertyTest{},
		EdgeCaseTests: []EdgeCaseTest{},
		SmokeTests:    []SmokeTest{},
		Coverage:      Coverage{},
	}

	cov := ts.ComputeCoverageStruct(req)

	wantCovered := []string{"05-REQ-1.1", "05-REQ-1.2"}
	if diff := cmp.Diff(wantCovered, cov.RequirementsCovered); diff != "" {
		t.Errorf("RequirementsCovered mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{}, cov.Gaps); diff != "" {
		t.Errorf("Gaps mismatch (-want +got):\n%s", diff)
	}
}

// TestComputeCoverage_CriterionLevel verifies that ComputeCoverage reports
// criterion-level IDs in Covered and Uncovered, matching the behavior of
// ComputeCoverageStruct. Parent requirement IDs must not appear.
// Test Spec: TS-NS-4, Requirement: NS-REQ-4.1
func TestComputeCoverage_CriterionLevel(t *testing.T) {
	defer requireImplemented(t)

	req := makeReqWithCriteria("05-REQ-1", []string{"05-REQ-1.1", "05-REQ-1.2", "05-REQ-1.3"}, nil)
	ts := makeTestSpecWithTestCase("05-REQ-1.1")

	report := ts.ComputeCoverage(req)

	// Only the tested criterion ID should be covered.
	if diff := cmp.Diff([]string{"05-REQ-1.1"}, report.Covered); diff != "" {
		t.Errorf("Covered mismatch (-want +got):\n%s", diff)
	}

	// Untested criteria must appear in Uncovered.
	wantUncovered := []string{"05-REQ-1.2", "05-REQ-1.3"}
	if diff := cmp.Diff(wantUncovered, report.Uncovered, cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
		t.Errorf("Uncovered mismatch (-want +got):\n%s", diff)
	}

	// Parent requirement ID must NOT appear in either list.
	for _, id := range append(report.Covered, report.Uncovered...) {
		if id == "05-REQ-1" {
			t.Errorf("parent requirement ID %q must not appear in coverage report; only criterion IDs", id)
		}
	}
}
