package afspec

import (
	"errors"
	"testing"
)

// ---------------------------------------------------------------------------
// Subtask 3.4: Tasks.TransitionSubtask state machine
// ---------------------------------------------------------------------------

// newTestTasks creates a TasksV1Json with a single group containing one
// subtask in the specified state.
func newTestTasks(subtaskID string, state SubtaskState) *TasksV1Json {
	return &TasksV1Json{
		SpecId:        "01",
		SpecName:      "test_spec",
		SchemaVersion: 1,
		TaskGroups: []TaskGroup{
			{
				Id:    1,
				Kind:  TaskGroupKindTests,
				Title: "Test group",
				Subtasks: []Subtask{
					{
						Id:              subtaskID,
						Title:           "Test subtask",
						State:           state,
						Details:         []string{"detail"},
						RequirementRefs: []string{"01-REQ-1.1"},
						TestSpecRefs:    []string{"TS-01-1"},
					},
				},
				Verification: VerificationSubtask{
					Id:     "1.V",
					Checks: []string{"check"},
				},
			},
		},
		Dependencies: []TaskDependency{},
		Traceability: []TraceabilityEntry{},
		TestCommands: TestCommands{
			AllTests:  "go test ./...",
			Linter:    "go vet ./...",
			SpecTests: "go test ./...",
		},
	}
}

// newMultiGroupTasks creates a TasksV1Json with multiple groups, each
// containing subtasks in different states. Used for bulk state mutation tests.
func newMultiGroupTasks() *TasksV1Json {
	return &TasksV1Json{
		SpecId:        "01",
		SpecName:      "test_spec",
		SchemaVersion: 1,
		TaskGroups: []TaskGroup{
			{
				Id:    1,
				Kind:  TaskGroupKindTests,
				Title: "Group 1",
				Subtasks: []Subtask{
					{Id: "1.1", Title: "Sub 1.1", State: SubtaskStatePending, Details: []string{"d"}, RequirementRefs: []string{"r"}, TestSpecRefs: []string{"t"}},
					{Id: "1.2", Title: "Sub 1.2", State: SubtaskStateQueued, Details: []string{"d"}, RequirementRefs: []string{"r"}, TestSpecRefs: []string{"t"}},
					{Id: "1.3", Title: "Sub 1.3", State: SubtaskStateInProgress, Details: []string{"d"}, RequirementRefs: []string{"r"}, TestSpecRefs: []string{"t"}},
				},
				Verification: VerificationSubtask{Id: "1.V", Checks: []string{"c"}},
			},
			{
				Id:    2,
				Kind:  TaskGroupKindStandard,
				Title: "Group 2",
				Subtasks: []Subtask{
					{Id: "2.1", Title: "Sub 2.1", State: SubtaskStateDone, Details: []string{"d"}, RequirementRefs: []string{"r"}, TestSpecRefs: []string{"t"}},
					{Id: "2.2", Title: "Sub 2.2", State: SubtaskStateInProgress, Details: []string{"d"}, RequirementRefs: []string{"r"}, TestSpecRefs: []string{"t"}},
					{Id: "2.3", Title: "Sub 2.3", State: SubtaskStateQueued, Details: []string{"d"}, RequirementRefs: []string{"r"}, TestSpecRefs: []string{"t"}},
				},
				Verification: VerificationSubtask{Id: "2.V", Checks: []string{"c"}},
			},
		},
		Dependencies: []TaskDependency{},
		Traceability: []TraceabilityEntry{},
		TestCommands: TestCommands{
			AllTests:  "go test ./...",
			Linter:    "go vet ./...",
			SpecTests: "go test ./...",
		},
	}
}

// getSubtaskState finds a subtask by ID in a TasksV1Json and returns its state.
func getSubtaskState(t *testing.T, tasks *TasksV1Json, subtaskID string) SubtaskState {
	t.Helper()
	for _, g := range tasks.TaskGroups {
		for _, s := range g.Subtasks {
			if s.Id == subtaskID {
				return s.State
			}
		}
	}
	t.Fatalf("subtask %q not found in tasks", subtaskID)
	return ""
}

// TestTransitionSubtask_AllValidTransitions verifies that TransitionSubtask
// accepts all 10 valid transitions from the state machine table and
// returns updated Tasks with the new state.
// Test Spec: TS-01-32, Requirement: 01-REQ-16.1
func TestTransitionSubtask_AllValidTransitions(t *testing.T) {
	defer requireImplemented(t)

	transitions := []struct {
		from SubtaskState
		to   string
	}{
		{SubtaskStatePending, "queued"},
		{SubtaskStatePending, "dropped"},
		{SubtaskStateQueued, "in_progress"},
		{SubtaskStateQueued, "pending"},
		{SubtaskStateQueued, "dropped"},
		{SubtaskStateInProgress, "done"},
		{SubtaskStateInProgress, "pending_reevaluation"},
		{SubtaskStateDone, "pending_reevaluation"},
		{SubtaskStatePendingReevaluation, "pending"},
		{SubtaskStatePendingReevaluation, "dropped"},
	}

	for _, tt := range transitions {
		name := string(tt.from) + "→" + tt.to
		t.Run(name, func(t *testing.T) {
			defer requireImplemented(t)

			tasks := newTestTasks("sub-1", tt.from)
			updated, err := tasks.TransitionSubtask("sub-1", tt.to)
			if err != nil {
				t.Fatalf("TransitionSubtask(%q, %q) returned unexpected error: %v", "sub-1", tt.to, err)
			}
			if updated == nil {
				t.Fatal("TransitionSubtask returned nil Tasks")
			}

			gotState := getSubtaskState(t, updated, "sub-1")
			if string(gotState) != tt.to {
				t.Errorf("after transition, subtask state = %q, want %q", gotState, tt.to)
			}
		})
	}
}

// TestTransitionSubtask_Immutability verifies that TransitionSubtask returns
// a new copy and does not modify the original Tasks.
// Test Spec: TS-01-32, Requirement: 01-REQ-16.1
func TestTransitionSubtask_Immutability(t *testing.T) {
	defer requireImplemented(t)

	tasks := newTestTasks("sub-1", SubtaskStatePending)

	updated, err := tasks.TransitionSubtask("sub-1", "queued")
	if err != nil {
		t.Fatalf("TransitionSubtask returned unexpected error: %v", err)
	}

	// Original should remain pending
	origState := getSubtaskState(t, tasks, "sub-1")
	if origState != SubtaskStatePending {
		t.Errorf("original subtask state changed to %q, want %q (immutability violated)", origState, SubtaskStatePending)
	}

	// Updated should be queued
	newState := getSubtaskState(t, updated, "sub-1")
	if newState != SubtaskStateQueued {
		t.Errorf("updated subtask state = %q, want %q", newState, SubtaskStateQueued)
	}
}

// TestTransitionSubtask_InvalidTransitions verifies that TransitionSubtask
// returns a LifecycleError for all invalid state transitions.
// Requirement: 01-REQ-16.E1, Property: 01-PROP-5
func TestTransitionSubtask_InvalidTransitions(t *testing.T) {
	defer requireImplemented(t)

	invalidTransitions := []struct {
		from SubtaskState
		to   string
	}{
		{SubtaskStatePending, "done"},
		{SubtaskStatePending, "in_progress"},
		{SubtaskStatePending, "pending_reevaluation"},
		{SubtaskStateQueued, "done"},
		{SubtaskStateQueued, "pending_reevaluation"},
		{SubtaskStateInProgress, "pending"},
		{SubtaskStateInProgress, "queued"},
		{SubtaskStateInProgress, "dropped"},
		{SubtaskStateDone, "pending"},
		{SubtaskStateDone, "queued"},
		{SubtaskStateDone, "in_progress"},
		{SubtaskStateDone, "dropped"},
		{SubtaskStateDropped, "pending"},
		{SubtaskStateDropped, "queued"},
		{SubtaskStateDropped, "in_progress"},
		{SubtaskStateDropped, "done"},
		{SubtaskStateDropped, "pending_reevaluation"},
		{SubtaskStatePendingReevaluation, "queued"},
		{SubtaskStatePendingReevaluation, "in_progress"},
		{SubtaskStatePendingReevaluation, "done"},
	}

	for _, tt := range invalidTransitions {
		name := string(tt.from) + "→" + tt.to
		t.Run(name, func(t *testing.T) {
			defer requireImplemented(t)

			tasks := newTestTasks("sub-1", tt.from)
			originalState := getSubtaskState(t, tasks, "sub-1")

			result, err := tasks.TransitionSubtask("sub-1", tt.to)
			if err == nil {
				t.Fatalf("expected LifecycleError for %s→%s, got nil", tt.from, tt.to)
			}

			var lifecycleErr *LifecycleError
			if !errors.As(err, &lifecycleErr) {
				t.Errorf("errors.As(err, &LifecycleError{}) = false, want true; err type = %T", err)
			}

			var specErr *SpecError
			if !errors.As(err, &specErr) {
				t.Errorf("errors.As(err, &SpecError{}) = false, want true; err type = %T", err)
			}

			// Result should be nil on error
			if result != nil {
				t.Error("expected nil result on error")
			}

			// Original should be unchanged
			afterState := getSubtaskState(t, tasks, "sub-1")
			if afterState != originalState {
				t.Errorf("original subtask state changed from %q to %q after failed transition", originalState, afterState)
			}
		})
	}
}

// TestTransitionSubtask_NonExistentID verifies that TransitionSubtask returns
// a LifecycleError when the subtask ID does not exist.
// Requirement: 01-REQ-16.E2
func TestTransitionSubtask_NonExistentID(t *testing.T) {
	defer requireImplemented(t)

	tasks := newTestTasks("sub-1", SubtaskStatePending)

	_, err := tasks.TransitionSubtask("nonexistent-id", "queued")
	if err == nil {
		t.Fatal("expected LifecycleError for non-existent subtask ID, got nil")
	}

	var lifecycleErr *LifecycleError
	if !errors.As(err, &lifecycleErr) {
		t.Errorf("errors.As(err, &LifecycleError{}) = false, want true; err type = %T", err)
	}

	var specErr *SpecError
	if !errors.As(err, &specErr) {
		t.Errorf("errors.As(err, &SpecError{}) = false, want true; err type = %T", err)
	}
}

// ---------------------------------------------------------------------------
// Subtask 3.5: Tasks.CompleteSubtaskStates and Tasks.ResetSubtaskStates
// ---------------------------------------------------------------------------

// TestCompleteSubtaskStates_SetsAllDone verifies that CompleteSubtaskStates
// sets every subtask in specified groups to 'done' regardless of prior state,
// bypassing the state machine.
// Test Spec: TS-01-33, Requirement: 01-REQ-17.1
func TestCompleteSubtaskStates_SetsAllDone(t *testing.T) {
	defer requireImplemented(t)

	tasks := newMultiGroupTasks()

	updated, err := tasks.CompleteSubtaskStates([]int{1})
	if err != nil {
		t.Fatalf("CompleteSubtaskStates returned unexpected error: %v", err)
	}
	if updated == nil {
		t.Fatal("CompleteSubtaskStates returned nil Tasks")
	}

	// All subtasks in group 1 should be done
	for _, g := range updated.TaskGroups {
		if g.Id == 1 {
			for _, s := range g.Subtasks {
				if s.State != SubtaskStateDone {
					t.Errorf("subtask %s state = %q, want %q", s.Id, s.State, SubtaskStateDone)
				}
			}
		}
	}
}

// TestCompleteSubtaskStates_MultipleGroups verifies CompleteSubtaskStates
// works across multiple groups.
// Requirement: 01-REQ-17.1
func TestCompleteSubtaskStates_MultipleGroups(t *testing.T) {
	defer requireImplemented(t)

	tasks := newMultiGroupTasks()

	updated, err := tasks.CompleteSubtaskStates([]int{1, 2})
	if err != nil {
		t.Fatalf("CompleteSubtaskStates returned unexpected error: %v", err)
	}

	// All subtasks in both groups should be done
	for _, g := range updated.TaskGroups {
		for _, s := range g.Subtasks {
			if s.State != SubtaskStateDone {
				t.Errorf("subtask %s in group %d state = %q, want %q", s.Id, g.Id, s.State, SubtaskStateDone)
			}
		}
	}
}

// TestCompleteSubtaskStates_Immutability verifies that CompleteSubtaskStates
// does not modify the original Tasks.
// Requirement: 01-REQ-17.1
func TestCompleteSubtaskStates_Immutability(t *testing.T) {
	defer requireImplemented(t)

	tasks := newMultiGroupTasks()

	// Record original states
	origStates := make(map[string]SubtaskState)
	for _, g := range tasks.TaskGroups {
		for _, s := range g.Subtasks {
			origStates[s.Id] = s.State
		}
	}

	_, err := tasks.CompleteSubtaskStates([]int{1})
	if err != nil {
		t.Fatalf("CompleteSubtaskStates returned unexpected error: %v", err)
	}

	// Original should be unchanged
	for _, g := range tasks.TaskGroups {
		for _, s := range g.Subtasks {
			if s.State != origStates[s.Id] {
				t.Errorf("original subtask %s state changed from %q to %q (immutability violated)", s.Id, origStates[s.Id], s.State)
			}
		}
	}
}

// TestResetSubtaskStates_SetsAllPending verifies that ResetSubtaskStates
// sets every subtask in specified groups to 'pending' regardless of prior
// state, bypassing the state machine.
// Test Spec: TS-01-34, Requirement: 01-REQ-17.2
func TestResetSubtaskStates_SetsAllPending(t *testing.T) {
	defer requireImplemented(t)

	tasks := newMultiGroupTasks()

	updated, err := tasks.ResetSubtaskStates([]int{2})
	if err != nil {
		t.Fatalf("ResetSubtaskStates returned unexpected error: %v", err)
	}
	if updated == nil {
		t.Fatal("ResetSubtaskStates returned nil Tasks")
	}

	// All subtasks in group 2 should be pending
	for _, g := range updated.TaskGroups {
		if g.Id == 2 {
			for _, s := range g.Subtasks {
				if s.State != SubtaskStatePending {
					t.Errorf("subtask %s state = %q, want %q", s.Id, s.State, SubtaskStatePending)
				}
			}
		}
	}
}

// TestResetSubtaskStates_Immutability verifies that ResetSubtaskStates
// does not modify the original Tasks.
// Requirement: 01-REQ-17.2
func TestResetSubtaskStates_Immutability(t *testing.T) {
	defer requireImplemented(t)

	tasks := newMultiGroupTasks()

	origStates := make(map[string]SubtaskState)
	for _, g := range tasks.TaskGroups {
		for _, s := range g.Subtasks {
			origStates[s.Id] = s.State
		}
	}

	_, err := tasks.ResetSubtaskStates([]int{2})
	if err != nil {
		t.Fatalf("ResetSubtaskStates returned unexpected error: %v", err)
	}

	for _, g := range tasks.TaskGroups {
		for _, s := range g.Subtasks {
			if s.State != origStates[s.Id] {
				t.Errorf("original subtask %s state changed from %q to %q (immutability violated)", s.Id, origStates[s.Id], s.State)
			}
		}
	}
}

// TestCompleteSubtaskStates_EmptyGroupIDs verifies that calling
// CompleteSubtaskStates with an empty slice returns Tasks unchanged.
// Requirement: 01-REQ-17.E2
func TestCompleteSubtaskStates_EmptyGroupIDs(t *testing.T) {
	defer requireImplemented(t)

	tasks := newMultiGroupTasks()

	updated, err := tasks.CompleteSubtaskStates([]int{})
	if err != nil {
		t.Fatalf("CompleteSubtaskStates returned unexpected error: %v", err)
	}
	if updated == nil {
		t.Fatal("CompleteSubtaskStates returned nil Tasks")
	}

	// No subtasks should have changed
	for _, g := range updated.TaskGroups {
		for _, s := range g.Subtasks {
			orig := getSubtaskState(t, tasks, s.Id)
			if s.State != orig {
				t.Errorf("subtask %s state changed from %q to %q with empty groupIDs", s.Id, orig, s.State)
			}
		}
	}
}

// TestResetSubtaskStates_EmptyGroupIDs verifies that calling
// ResetSubtaskStates with an empty slice returns Tasks unchanged.
// Requirement: 01-REQ-17.E2
func TestResetSubtaskStates_EmptyGroupIDs(t *testing.T) {
	defer requireImplemented(t)

	tasks := newMultiGroupTasks()

	updated, err := tasks.ResetSubtaskStates([]int{})
	if err != nil {
		t.Fatalf("ResetSubtaskStates returned unexpected error: %v", err)
	}
	if updated == nil {
		t.Fatal("ResetSubtaskStates returned nil Tasks")
	}

	for _, g := range updated.TaskGroups {
		for _, s := range g.Subtasks {
			orig := getSubtaskState(t, tasks, s.Id)
			if s.State != orig {
				t.Errorf("subtask %s state changed from %q to %q with empty groupIDs", s.Id, orig, s.State)
			}
		}
	}
}

// TestCompleteSubtaskStates_MissingGroupID verifies that calling
// CompleteSubtaskStates with a non-existent group ID silently skips it
// and processes valid group IDs.
// Requirement: 01-REQ-17.E1
func TestCompleteSubtaskStates_MissingGroupID(t *testing.T) {
	defer requireImplemented(t)

	tasks := newMultiGroupTasks()

	// Group 99 doesn't exist; group 1 does
	updated, err := tasks.CompleteSubtaskStates([]int{1, 99})
	if err != nil {
		t.Fatalf("CompleteSubtaskStates returned unexpected error: %v", err)
	}

	// Group 1 subtasks should all be done
	for _, g := range updated.TaskGroups {
		if g.Id == 1 {
			for _, s := range g.Subtasks {
				if s.State != SubtaskStateDone {
					t.Errorf("subtask %s in group 1 state = %q, want %q", s.Id, s.State, SubtaskStateDone)
				}
			}
		}
	}
}

// TestResetSubtaskStates_MissingGroupID verifies that calling
// ResetSubtaskStates with a non-existent group ID silently skips it
// and processes valid group IDs.
// Requirement: 01-REQ-17.E1
func TestResetSubtaskStates_MissingGroupID(t *testing.T) {
	defer requireImplemented(t)

	tasks := newMultiGroupTasks()

	// Group 99 doesn't exist; group 2 does
	updated, err := tasks.ResetSubtaskStates([]int{2, 99})
	if err != nil {
		t.Fatalf("ResetSubtaskStates returned unexpected error: %v", err)
	}

	// Group 2 subtasks should all be pending
	for _, g := range updated.TaskGroups {
		if g.Id == 2 {
			for _, s := range g.Subtasks {
				if s.State != SubtaskStatePending {
					t.Errorf("subtask %s in group 2 state = %q, want %q", s.Id, s.State, SubtaskStatePending)
				}
			}
		}
	}
}
