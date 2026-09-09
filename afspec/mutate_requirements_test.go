package afspec

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// makeRequirement creates a minimal Requirement with the given ID.
func makeRequirement(id string) Requirement {
	return Requirement{
		Id:    id,
		Title: "Requirement " + id,
		UserStory: UserStory{
			Role:    "developer",
			Goal:    "test",
			Benefit: "coverage",
		},
		AcceptanceCriteria: []Criterion{},
		EdgeCases:          []Criterion{},
	}
}

// makeRequirementsV1 creates a RequirementsV1Json with the given requirements.
func makeRequirementsV1(reqs ...Requirement) RequirementsV1Json {
	return RequirementsV1Json{
		SpecId:                "05",
		SpecName:              "test_spec",
		SchemaVersion:         1,
		Introduction:          "Test introduction",
		Requirements:          reqs,
		Glossary:              RequirementsV1JsonGlossary{},
		CorrectnessProperties: []CorrectnessProperty{},
		ExecutionPaths:        []ExecutionPath{},
		ErrorHandling:         []ErrorHandlingEntry{},
	}
}

// ---------------------------------------------------------------------------
// TS-05-1: AddRequirement with a non-duplicate ID
// Requirement: 05-REQ-1.1
// ---------------------------------------------------------------------------

func TestMutateAddRequirement_NonDuplicate(t *testing.T) {
	defer requireImplemented(t)

	original := makeRequirementsV1(makeRequirement("05-REQ-1"))
	newReq, err := AddRequirement(original, makeRequirement("05-REQ-2"))

	if err != nil {
		t.Fatalf("AddRequirement returned unexpected error: %v", err)
	}
	if len(newReq.Requirements) != 2 {
		t.Errorf("new RequirementsV1Json has %d requirements, want 2", len(newReq.Requirements))
	}
	if newReq.Requirements[1].Id != "05-REQ-2" {
		t.Errorf("appended requirement ID = %q, want %q", newReq.Requirements[1].Id, "05-REQ-2")
	}
	// Original must be unchanged (immutable-copy pattern).
	if len(original.Requirements) != 1 {
		t.Errorf("original RequirementsV1Json has %d requirements, want 1 (immutability violated)", len(original.Requirements))
	}
}

// ---------------------------------------------------------------------------
// TS-05-2: GetRequirement with an existing ID
// Requirement: 05-REQ-1.2
// ---------------------------------------------------------------------------

func TestMutateGetRequirement_Exists(t *testing.T) {
	defer requireImplemented(t)

	req := makeRequirementsV1(makeRequirement("05-REQ-3"))
	req.Requirements[0].Title = "Test Req"

	ptr, found := GetRequirement(req, "05-REQ-3")

	if !found {
		t.Fatal("GetRequirement returned found=false, want true")
	}
	if ptr == nil {
		t.Fatal("GetRequirement returned nil pointer, want non-nil")
	}
	if ptr.Id != "05-REQ-3" {
		t.Errorf("GetRequirement returned requirement ID = %q, want %q", ptr.Id, "05-REQ-3")
	}
}

// ---------------------------------------------------------------------------
// TS-05-3: GetRequirement with a non-existent ID
// Requirement: 05-REQ-1.3
// ---------------------------------------------------------------------------

func TestMutateGetRequirement_NotExists(t *testing.T) {
	defer requireImplemented(t)

	req := makeRequirementsV1(makeRequirement("05-REQ-1"))

	ptr, found := GetRequirement(req, "05-REQ-99")

	if found {
		t.Error("GetRequirement returned found=true, want false")
	}
	if ptr != nil {
		t.Errorf("GetRequirement returned non-nil pointer, want nil")
	}
}

// ---------------------------------------------------------------------------
// TS-05-4: RemoveRequirement with an existing ID
// Requirement: 05-REQ-1.4
// ---------------------------------------------------------------------------

func TestMutateRemoveRequirement_Exists(t *testing.T) {
	defer requireImplemented(t)

	original := makeRequirementsV1(
		makeRequirement("05-REQ-1"),
		makeRequirement("05-REQ-2"),
	)

	newReq, ok := RemoveRequirement(original, "05-REQ-1")

	if !ok {
		t.Fatal("RemoveRequirement returned ok=false, want true")
	}
	if len(newReq.Requirements) != 1 {
		t.Fatalf("new RequirementsV1Json has %d requirements, want 1", len(newReq.Requirements))
	}
	if newReq.Requirements[0].Id != "05-REQ-2" {
		t.Errorf("remaining requirement ID = %q, want %q", newReq.Requirements[0].Id, "05-REQ-2")
	}
	// Original must be unchanged.
	if len(original.Requirements) != 2 {
		t.Errorf("original RequirementsV1Json has %d requirements, want 2 (immutability violated)", len(original.Requirements))
	}
}

// ---------------------------------------------------------------------------
// TS-05-5: SetGlossaryEntry inserts or overwrites
// Requirement: 05-REQ-1.5
// ---------------------------------------------------------------------------

func TestMutateSetGlossaryEntry(t *testing.T) {
	defer requireImplemented(t)

	original := makeRequirementsV1()

	// Insert new term.
	newReq := SetGlossaryEntry(original, "Foo", "A foo is a bar")

	if newReq.Glossary["Foo"] != "A foo is a bar" {
		t.Errorf("glossary[Foo] = %q, want %q", newReq.Glossary["Foo"], "A foo is a bar")
	}
	// Original must be unchanged.
	if len(original.Glossary) != 0 {
		t.Errorf("original glossary has %d entries, want 0 (immutability violated)", len(original.Glossary))
	}

	// Overwrite existing term.
	newReq2 := SetGlossaryEntry(newReq, "Foo", "Updated definition")

	if newReq2.Glossary["Foo"] != "Updated definition" {
		t.Errorf("glossary[Foo] = %q, want %q", newReq2.Glossary["Foo"], "Updated definition")
	}
}

// ---------------------------------------------------------------------------
// TS-05-6: RemoveGlossaryEntry with an existing term
// Requirement: 05-REQ-1.6
// ---------------------------------------------------------------------------

func TestMutateRemoveGlossaryEntry_Exists(t *testing.T) {
	defer requireImplemented(t)

	original := makeRequirementsV1()
	original.Glossary["Foo"] = "A foo is a bar"

	newReq, ok := RemoveGlossaryEntry(original, "Foo")

	if !ok {
		t.Fatal("RemoveGlossaryEntry returned ok=false, want true")
	}
	if _, exists := newReq.Glossary["Foo"]; exists {
		t.Error("Foo still present in new glossary")
	}
	// Original must be unchanged.
	if original.Glossary["Foo"] != "A foo is a bar" {
		t.Error("original glossary was modified (immutability violated)")
	}
}

// ---------------------------------------------------------------------------
// TS-05-7: AddCorrectnessProperty, AddExecutionPath, AddErrorHandling
// Requirement: 05-REQ-1.7
// ---------------------------------------------------------------------------

func TestMutateAddCorrectnessProperty_AddExecutionPath_AddErrorHandling(t *testing.T) {
	defer requireImplemented(t)

	req := makeRequirementsV1()

	// AddCorrectnessProperty
	r1, err1 := AddCorrectnessProperty(req, CorrectnessProperty{
		Id:        "05-PROP-1",
		Title:     "Immutability",
		ForAny:    "any mutation",
		Invariant: "input unchanged",
		Validates: []string{"05-REQ-1.1"},
	})
	if err1 != nil {
		t.Fatalf("AddCorrectnessProperty returned unexpected error: %v", err1)
	}
	if len(r1.CorrectnessProperties) != 1 {
		t.Errorf("CorrectnessProperties count = %d, want 1", len(r1.CorrectnessProperties))
	}

	// AddExecutionPath
	r2, err2 := AddExecutionPath(req, ExecutionPath{
		Id:    "05-PATH-1",
		Title: "Happy path",
		Steps: []PathStep{
			{Actor: "caller", Action: "calls function"},
			{Actor: "package", Action: "returns result"},
		},
	})
	if err2 != nil {
		t.Fatalf("AddExecutionPath returned unexpected error: %v", err2)
	}
	if len(r2.ExecutionPaths) != 1 {
		t.Errorf("ExecutionPaths count = %d, want 1", len(r2.ExecutionPaths))
	}

	// AddErrorHandling
	r3, err3 := AddErrorHandling(req, ErrorHandlingEntry{
		Id:            "05-ERR-1",
		Condition:     "duplicate ID",
		Behavior:      "return error",
		RequirementId: "05-REQ-1",
	})
	if err3 != nil {
		t.Fatalf("AddErrorHandling returned unexpected error: %v", err3)
	}
	if len(r3.ErrorHandling) != 1 {
		t.Errorf("ErrorHandling count = %d, want 1", len(r3.ErrorHandling))
	}
}

// ---------------------------------------------------------------------------
// TS-05-36: AddRequirement with a duplicate ID
// Requirement: 05-REQ-1.E1
// ---------------------------------------------------------------------------

func TestMutateAddRequirement_Duplicate(t *testing.T) {
	defer requireImplemented(t)

	original := makeRequirementsV1(makeRequirement("05-REQ-1"))

	result, err := AddRequirement(original, makeRequirement("05-REQ-1"))

	if err == nil {
		t.Fatal("AddRequirement with duplicate ID returned nil error, want non-nil")
	}
	if !strings.Contains(err.Error(), "05-REQ-1") {
		t.Errorf("error message %q does not contain duplicate ID %q", err.Error(), "05-REQ-1")
	}
	// Returned collection must be unchanged.
	if len(result.Requirements) != 1 {
		t.Errorf("result has %d requirements, want 1 (original unchanged)", len(result.Requirements))
	}
}

// ---------------------------------------------------------------------------
// TS-05-37: AddCorrectnessProperty, AddExecutionPath, AddErrorHandling
//           with duplicate IDs
// Requirement: 05-REQ-1.E2
// ---------------------------------------------------------------------------

func TestMutateAddCorrectnessProperty_Duplicate(t *testing.T) {
	defer requireImplemented(t)

	req := makeRequirementsV1()
	req.CorrectnessProperties = []CorrectnessProperty{
		{Id: "05-PROP-1", Title: "P", ForAny: "a", Invariant: "i", Validates: []string{"r"}},
	}

	r1, e1 := AddCorrectnessProperty(req, CorrectnessProperty{
		Id: "05-PROP-1", Title: "P", ForAny: "a", Invariant: "i", Validates: []string{"r"},
	})
	if e1 == nil {
		t.Fatal("AddCorrectnessProperty with duplicate ID returned nil error")
	}
	if !strings.Contains(e1.Error(), "05-PROP-1") {
		t.Errorf("error message %q does not contain %q", e1.Error(), "05-PROP-1")
	}
	if len(r1.CorrectnessProperties) != 1 {
		t.Errorf("CorrectnessProperties count = %d, want 1", len(r1.CorrectnessProperties))
	}
}

func TestMutateAddExecutionPath_Duplicate(t *testing.T) {
	defer requireImplemented(t)

	req := makeRequirementsV1()
	req.ExecutionPaths = []ExecutionPath{
		{Id: "05-PATH-1", Title: "P", Steps: []PathStep{{Actor: "a", Action: "b"}, {Actor: "c", Action: "d"}}},
	}

	r2, e2 := AddExecutionPath(req, ExecutionPath{
		Id: "05-PATH-1", Title: "P", Steps: []PathStep{{Actor: "a", Action: "b"}, {Actor: "c", Action: "d"}},
	})
	if e2 == nil {
		t.Fatal("AddExecutionPath with duplicate ID returned nil error")
	}
	if !strings.Contains(e2.Error(), "05-PATH-1") {
		t.Errorf("error message %q does not contain %q", e2.Error(), "05-PATH-1")
	}
	if len(r2.ExecutionPaths) != 1 {
		t.Errorf("ExecutionPaths count = %d, want 1", len(r2.ExecutionPaths))
	}
}

func TestMutateAddErrorHandling_Duplicate(t *testing.T) {
	defer requireImplemented(t)

	req := makeRequirementsV1()
	req.ErrorHandling = []ErrorHandlingEntry{
		{Id: "05-ERR-1", Condition: "c", Behavior: "b", RequirementId: "r"},
	}

	r3, e3 := AddErrorHandling(req, ErrorHandlingEntry{
		Id: "05-ERR-1", Condition: "c", Behavior: "b", RequirementId: "r",
	})
	if e3 == nil {
		t.Fatal("AddErrorHandling with duplicate ID returned nil error")
	}
	if !strings.Contains(e3.Error(), "05-ERR-1") {
		t.Errorf("error message %q does not contain %q", e3.Error(), "05-ERR-1")
	}
	if len(r3.ErrorHandling) != 1 {
		t.Errorf("ErrorHandling count = %d, want 1", len(r3.ErrorHandling))
	}
}

// ---------------------------------------------------------------------------
// TS-05-38: RemoveRequirement with a non-existent ID
// Requirement: 05-REQ-1.E3
// ---------------------------------------------------------------------------

func TestMutateRemoveRequirement_NotExists(t *testing.T) {
	defer requireImplemented(t)

	original := makeRequirementsV1(makeRequirement("05-REQ-1"))

	result, ok := RemoveRequirement(original, "05-REQ-99")

	if ok {
		t.Error("RemoveRequirement with non-existent ID returned ok=true, want false")
	}
	if len(result.Requirements) != 1 {
		t.Errorf("result has %d requirements, want 1", len(result.Requirements))
	}
}

// ---------------------------------------------------------------------------
// TS-05-39: RemoveGlossaryEntry with a non-existent term
// Requirement: 05-REQ-1.E4
// ---------------------------------------------------------------------------

func TestMutateRemoveGlossaryEntry_NotExists(t *testing.T) {
	defer requireImplemented(t)

	original := makeRequirementsV1()
	original.Glossary["Foo"] = "bar"

	result, ok := RemoveGlossaryEntry(original, "NonExistentTerm")

	if ok {
		t.Error("RemoveGlossaryEntry with non-existent term returned ok=true, want false")
	}
	if result.Glossary["Foo"] != "bar" {
		t.Errorf("result glossary[Foo] = %q, want %q", result.Glossary["Foo"], "bar")
	}
}

// ---------------------------------------------------------------------------
// TS-05-40: Deep-copy verification — modifying returned struct does not
//           affect the original
// Requirement: 05-REQ-1.E5
// ---------------------------------------------------------------------------

func TestMutateDeepCopy_Requirements(t *testing.T) {
	defer requireImplemented(t)

	original := makeRequirementsV1(makeRequirement("05-REQ-1"))
	original.Glossary["Foo"] = "bar"

	// AddRequirement deep-copy check: mutating the returned slice must not
	// affect the original.
	newReq, err := AddRequirement(original, makeRequirement("05-REQ-2"))
	if err != nil {
		t.Fatalf("AddRequirement returned unexpected error: %v", err)
	}

	// Mutate the returned copy's first requirement ID.
	newReq.Requirements[0].Id = "MUTATED"
	if original.Requirements[0].Id != "05-REQ-1" {
		t.Errorf("original.Requirements[0].Id = %q, want %q (deep copy failed)", original.Requirements[0].Id, "05-REQ-1")
	}

	// SetGlossaryEntry deep-copy check: mutating the returned glossary must
	// not affect the original.
	newGloss := SetGlossaryEntry(original, "Baz", "qux")
	newGloss.Glossary["Foo"] = "MUTATED"
	if original.Glossary["Foo"] != "bar" {
		t.Errorf("original.Glossary[Foo] = %q, want %q (deep copy failed)", original.Glossary["Foo"], "bar")
	}
}
