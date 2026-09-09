package afspec

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// makeTasksV1 creates a minimal TasksV1Json with the given task groups.
func makeTasksV1(groups ...TaskGroup) TasksV1Json {
	return TasksV1Json{
		SpecId:        "05",
		SpecName:      "test_spec",
		SchemaVersion: 1,
		TaskGroups:    groups,
		Dependencies:  []TaskDependency{},
		Traceability:  []TraceabilityEntry{},
		TestCommands: TestCommands{
			AllTests:  "go test ./...",
			Linter:    "go vet ./...",
			SpecTests: "go test ./...",
		},
	}
}

// makeTaskGroup creates a minimal TaskGroup with the given int ID and subtasks.
func makeTaskGroup(id int, subtasks ...Subtask) TaskGroup {
	return TaskGroup{
		Id:       id,
		Kind:     TaskGroupKindStandard,
		Title:    "Group",
		Subtasks: subtasks,
		Verification: VerificationSubtask{
			Id:     "V",
			Checks: []string{"check"},
		},
	}
}

// makeSubtask creates a minimal Subtask with the given string ID.
func makeSubtask(id string) Subtask {
	return Subtask{
		Id:              id,
		Title:           "Subtask " + id,
		State:           SubtaskStatePending,
		Details:         []string{"detail"},
		RequirementRefs: []string{"05-REQ-1"},
		TestSpecRefs:    []string{"TS-05-1"},
	}
}

// makeTraceabilityEntry creates a TraceabilityEntry with the given IDs.
func makeTraceabilityEntry(reqID, testSpecID string) TraceabilityEntry {
	return TraceabilityEntry{
		RequirementId: reqID,
		TestSpecId:    testSpecID,
		TaskId:        "1.1",
	}
}

// ---------------------------------------------------------------------------
// TS-05-15: AddTaskGroup with a non-duplicate ID
// Requirement: 05-REQ-4.1
// ---------------------------------------------------------------------------

func TestMutateAddTaskGroup(t *testing.T) {
	defer requireImplemented(t)

	original := makeTasksV1(makeTaskGroup(1))

	newT, err := AddTaskGroup(original, makeTaskGroup(2))

	if err != nil {
		t.Fatalf("AddTaskGroup returned unexpected error: %v", err)
	}
	if len(newT.TaskGroups) != 2 {
		t.Errorf("new TasksV1Json has %d task groups, want 2", len(newT.TaskGroups))
	}
	if newT.TaskGroups[1].Id != 2 {
		t.Errorf("appended task group ID = %d, want 2", newT.TaskGroups[1].Id)
	}
	// Original must be unchanged.
	if len(original.TaskGroups) != 1 {
		t.Errorf("original has %d task groups, want 1 (immutability violated)", len(original.TaskGroups))
	}
}

// ---------------------------------------------------------------------------
// TS-05-16: AddSubtask with a non-duplicate ID
// Requirement: 05-REQ-4.2
// ---------------------------------------------------------------------------

func TestMutateAddSubtask(t *testing.T) {
	defer requireImplemented(t)

	original := makeTaskGroup(1, makeSubtask("1.1"))

	newG, err := AddSubtask(original, makeSubtask("1.2"))

	if err != nil {
		t.Fatalf("AddSubtask returned unexpected error: %v", err)
	}
	if len(newG.Subtasks) != 2 {
		t.Errorf("new TaskGroup has %d subtasks, want 2", len(newG.Subtasks))
	}
	if newG.Subtasks[1].Id != "1.2" {
		t.Errorf("appended subtask ID = %q, want %q", newG.Subtasks[1].Id, "1.2")
	}
	// Original must be unchanged.
	if len(original.Subtasks) != 1 {
		t.Errorf("original has %d subtasks, want 1 (immutability violated)", len(original.Subtasks))
	}
}

// ---------------------------------------------------------------------------
// TS-05-17: AddTraceabilityEntry with a unique pair
// Requirement: 05-REQ-4.3
// ---------------------------------------------------------------------------

func TestMutateAddTraceabilityEntry(t *testing.T) {
	defer requireImplemented(t)

	original := makeTasksV1()
	original.Traceability = []TraceabilityEntry{
		makeTraceabilityEntry("05-REQ-1", "05"),
	}

	newT, err := AddTraceabilityEntry(original, makeTraceabilityEntry("05-REQ-2", "05"))

	if err != nil {
		t.Fatalf("AddTraceabilityEntry returned unexpected error: %v", err)
	}
	if len(newT.Traceability) != 2 {
		t.Errorf("new TasksV1Json has %d traceability entries, want 2", len(newT.Traceability))
	}
	// Original must be unchanged.
	if len(original.Traceability) != 1 {
		t.Errorf("original has %d traceability entries, want 1 (immutability violated)", len(original.Traceability))
	}
}

// ---------------------------------------------------------------------------
// TS-05-18: AddDependency unconditionally appends
// Requirement: 05-REQ-4.4
// ---------------------------------------------------------------------------

func TestMutateAddDependency(t *testing.T) {
	defer requireImplemented(t)

	original := makeTasksV1()

	dep := TaskDependency{
		DependsOnSpec: "04",
		FromGroup:     1,
		ToGroup:       1,
		Relationship:  "blocks",
	}
	newT := AddDependency(original, dep)

	if len(newT.Dependencies) != 1 {
		t.Errorf("new TasksV1Json has %d dependencies, want 1", len(newT.Dependencies))
	}
	if newT.Dependencies[0].DependsOnSpec != "04" {
		t.Errorf("dependency DependsOnSpec = %q, want %q", newT.Dependencies[0].DependsOnSpec, "04")
	}
	// Original must be unchanged.
	if len(original.Dependencies) != 0 {
		t.Errorf("original has %d dependencies, want 0 (immutability violated)", len(original.Dependencies))
	}
}

// ---------------------------------------------------------------------------
// TS-05-46: AddTaskGroup with a duplicate ID
// Requirement: 05-REQ-4.E1
// ---------------------------------------------------------------------------

func TestMutateAddTaskGroup_Duplicate(t *testing.T) {
	defer requireImplemented(t)

	original := makeTasksV1(makeTaskGroup(1))

	result, err := AddTaskGroup(original, makeTaskGroup(1))

	if err == nil {
		t.Fatal("AddTaskGroup with duplicate ID returned nil error, want non-nil")
	}
	if !strings.Contains(err.Error(), "1") {
		t.Errorf("error message %q does not contain duplicate ID", err.Error())
	}
	if len(result.TaskGroups) != 1 {
		t.Errorf("result has %d task groups, want 1", len(result.TaskGroups))
	}
}

// ---------------------------------------------------------------------------
// TS-05-47: AddSubtask with a duplicate ID
// Requirement: 05-REQ-4.E2
// ---------------------------------------------------------------------------

func TestMutateAddSubtask_Duplicate(t *testing.T) {
	defer requireImplemented(t)

	original := makeTaskGroup(1, makeSubtask("1.1"))

	result, err := AddSubtask(original, makeSubtask("1.1"))

	if err == nil {
		t.Fatal("AddSubtask with duplicate ID returned nil error, want non-nil")
	}
	if !strings.Contains(err.Error(), "1.1") {
		t.Errorf("error message %q does not contain duplicate ID %q", err.Error(), "1.1")
	}
	if len(result.Subtasks) != 1 {
		t.Errorf("result has %d subtasks, want 1", len(result.Subtasks))
	}
}

// ---------------------------------------------------------------------------
// TS-05-48: AddTraceabilityEntry with duplicate pair
// Requirement: 05-REQ-4.E3
// ---------------------------------------------------------------------------

func TestMutateAddTraceabilityEntry_Duplicate(t *testing.T) {
	defer requireImplemented(t)

	original := makeTasksV1()
	original.Traceability = []TraceabilityEntry{
		makeTraceabilityEntry("05-REQ-1", "05"),
	}

	result, err := AddTraceabilityEntry(original, makeTraceabilityEntry("05-REQ-1", "05"))

	if err == nil {
		t.Fatal("AddTraceabilityEntry with duplicate pair returned nil error, want non-nil")
	}
	if !strings.Contains(err.Error(), "05-REQ-1") {
		t.Errorf("error message %q does not contain requirement_id %q", err.Error(), "05-REQ-1")
	}
	if !strings.Contains(err.Error(), "05") {
		t.Errorf("error message %q does not contain test_spec_id %q", err.Error(), "05")
	}
	if len(result.Traceability) != 1 {
		t.Errorf("result has %d traceability entries, want 1", len(result.Traceability))
	}
}
