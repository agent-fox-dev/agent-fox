package afspec

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// makeCriterion creates a minimal Criterion with the given ID.
func makeCriterion(id string) Criterion {
	return Criterion{
		Id:          id,
		Action:      "test action",
		EarsPattern: CriterionEarsPatternUbiquitous,
		System:      "test system",
	}
}

// ---------------------------------------------------------------------------
// TS-05-8: AddCriterion with a non-duplicate ID
// Requirement: 05-REQ-2.1
// ---------------------------------------------------------------------------

func TestMutateAddCriterion_NonDuplicate(t *testing.T) {
	defer requireImplemented(t)

	original := Requirement{
		Id:                 "05-REQ-1",
		Title:              "Test",
		UserStory:          UserStory{Role: "dev", Goal: "test", Benefit: "coverage"},
		AcceptanceCriteria: []Criterion{makeCriterion("05-REQ-1.1")},
		EdgeCases:          []Criterion{},
	}

	newReq, err := AddCriterion(original, makeCriterion("05-REQ-1.2"))

	if err != nil {
		t.Fatalf("AddCriterion returned unexpected error: %v", err)
	}
	if len(newReq.AcceptanceCriteria) != 2 {
		t.Errorf("new Requirement has %d acceptance_criteria, want 2", len(newReq.AcceptanceCriteria))
	}
	if newReq.AcceptanceCriteria[1].Id != "05-REQ-1.2" {
		t.Errorf("appended criterion ID = %q, want %q", newReq.AcceptanceCriteria[1].Id, "05-REQ-1.2")
	}
	// Original must be unchanged.
	if len(original.AcceptanceCriteria) != 1 {
		t.Errorf("original has %d acceptance_criteria, want 1 (immutability violated)", len(original.AcceptanceCriteria))
	}
}

// ---------------------------------------------------------------------------
// TS-05-9: AddEdgeCase with a non-duplicate ID
// Requirement: 05-REQ-2.2
// ---------------------------------------------------------------------------

func TestMutateAddEdgeCase_NonDuplicate(t *testing.T) {
	defer requireImplemented(t)

	original := Requirement{
		Id:                 "05-REQ-1",
		Title:              "Test",
		UserStory:          UserStory{Role: "dev", Goal: "test", Benefit: "coverage"},
		AcceptanceCriteria: []Criterion{},
		EdgeCases:          []Criterion{makeCriterion("05-REQ-1.E1")},
	}

	newReq, err := AddEdgeCase(original, makeCriterion("05-REQ-1.E2"))

	if err != nil {
		t.Fatalf("AddEdgeCase returned unexpected error: %v", err)
	}
	if len(newReq.EdgeCases) != 2 {
		t.Errorf("new Requirement has %d edge_cases, want 2", len(newReq.EdgeCases))
	}
	if newReq.EdgeCases[1].Id != "05-REQ-1.E2" {
		t.Errorf("appended edge case ID = %q, want %q", newReq.EdgeCases[1].Id, "05-REQ-1.E2")
	}
	// Original must be unchanged.
	if len(original.EdgeCases) != 1 {
		t.Errorf("original has %d edge_cases, want 1 (immutability violated)", len(original.EdgeCases))
	}
}

// ---------------------------------------------------------------------------
// TS-05-10: GetCriterion searches both acceptance_criteria and edge_cases
// Requirement: 05-REQ-2.3
// ---------------------------------------------------------------------------

func TestMutateGetCriterion(t *testing.T) {
	defer requireImplemented(t)

	r := Requirement{
		Id:                 "05-REQ-1",
		Title:              "Test",
		UserStory:          UserStory{Role: "dev", Goal: "test", Benefit: "coverage"},
		AcceptanceCriteria: []Criterion{makeCriterion("05-REQ-1.1")},
		EdgeCases:          []Criterion{makeCriterion("05-REQ-1.E1")},
	}

	// Found in acceptance_criteria.
	p1, ok1 := GetCriterion(r, "05-REQ-1.1")
	if !ok1 {
		t.Error("GetCriterion(05-REQ-1.1) returned false, want true")
	}
	if p1 == nil {
		t.Fatal("GetCriterion(05-REQ-1.1) returned nil, want non-nil")
	}
	if p1.Id != "05-REQ-1.1" {
		t.Errorf("GetCriterion returned ID = %q, want %q", p1.Id, "05-REQ-1.1")
	}

	// Found in edge_cases.
	p2, ok2 := GetCriterion(r, "05-REQ-1.E1")
	if !ok2 {
		t.Error("GetCriterion(05-REQ-1.E1) returned false, want true")
	}
	if p2 == nil {
		t.Fatal("GetCriterion(05-REQ-1.E1) returned nil, want non-nil")
	}

	// Not found.
	p3, ok3 := GetCriterion(r, "05-REQ-1.99")
	if ok3 {
		t.Error("GetCriterion(05-REQ-1.99) returned true, want false")
	}
	if p3 != nil {
		t.Error("GetCriterion(05-REQ-1.99) returned non-nil, want nil")
	}
}

// ---------------------------------------------------------------------------
// TS-05-41: AddCriterion with a duplicate ID
// Requirement: 05-REQ-2.E1
// ---------------------------------------------------------------------------

func TestMutateAddCriterion_Duplicate(t *testing.T) {
	defer requireImplemented(t)

	original := Requirement{
		Id:                 "05-REQ-1",
		Title:              "Test",
		UserStory:          UserStory{Role: "dev", Goal: "test", Benefit: "coverage"},
		AcceptanceCriteria: []Criterion{makeCriterion("05-REQ-1.1")},
		EdgeCases:          []Criterion{},
	}

	result, err := AddCriterion(original, makeCriterion("05-REQ-1.1"))

	if err == nil {
		t.Fatal("AddCriterion with duplicate ID returned nil error, want non-nil")
	}
	if !strings.Contains(err.Error(), "05-REQ-1.1") {
		t.Errorf("error message %q does not contain duplicate ID %q", err.Error(), "05-REQ-1.1")
	}
	if len(result.AcceptanceCriteria) != 1 {
		t.Errorf("result has %d acceptance_criteria, want 1", len(result.AcceptanceCriteria))
	}
}

// ---------------------------------------------------------------------------
// TS-05-42: AddEdgeCase with a duplicate ID
// Requirement: 05-REQ-2.E2
// ---------------------------------------------------------------------------

func TestMutateAddEdgeCase_Duplicate(t *testing.T) {
	defer requireImplemented(t)

	original := Requirement{
		Id:                 "05-REQ-1",
		Title:              "Test",
		UserStory:          UserStory{Role: "dev", Goal: "test", Benefit: "coverage"},
		AcceptanceCriteria: []Criterion{},
		EdgeCases:          []Criterion{makeCriterion("05-REQ-1.E1")},
	}

	result, err := AddEdgeCase(original, makeCriterion("05-REQ-1.E1"))

	if err == nil {
		t.Fatal("AddEdgeCase with duplicate ID returned nil error, want non-nil")
	}
	if !strings.Contains(err.Error(), "05-REQ-1.E1") {
		t.Errorf("error message %q does not contain duplicate ID %q", err.Error(), "05-REQ-1.E1")
	}
	if len(result.EdgeCases) != 1 {
		t.Errorf("result has %d edge_cases, want 1", len(result.EdgeCases))
	}
}

// ---------------------------------------------------------------------------
// TS-05-43: AddCriterion and AddEdgeCase on Requirement with nil slices
// Requirement: 05-REQ-2.E3
// ---------------------------------------------------------------------------

func TestMutateAddCriterion_AddEdgeCase_NilSlices(t *testing.T) {
	defer requireImplemented(t)

	r := Requirement{
		Id:        "05-REQ-1",
		Title:     "Test",
		UserStory: UserStory{Role: "dev", Goal: "test", Benefit: "coverage"},
		// AcceptanceCriteria and EdgeCases intentionally nil.
	}

	// AddCriterion on nil slice.
	r1, err1 := AddCriterion(r, makeCriterion("05-REQ-1.1"))
	if err1 != nil {
		t.Fatalf("AddCriterion on nil slice returned error: %v", err1)
	}
	if r1.AcceptanceCriteria == nil {
		t.Fatal("AddCriterion returned nil AcceptanceCriteria, want non-nil")
	}
	if len(r1.AcceptanceCriteria) != 1 {
		t.Errorf("AcceptanceCriteria length = %d, want 1", len(r1.AcceptanceCriteria))
	}

	// AddEdgeCase on nil slice.
	r2, err2 := AddEdgeCase(r, makeCriterion("05-REQ-1.E1"))
	if err2 != nil {
		t.Fatalf("AddEdgeCase on nil slice returned error: %v", err2)
	}
	if r2.EdgeCases == nil {
		t.Fatal("AddEdgeCase returned nil EdgeCases, want non-nil")
	}
	if len(r2.EdgeCases) != 1 {
		t.Errorf("EdgeCases length = %d, want 1", len(r2.EdgeCases))
	}
}
