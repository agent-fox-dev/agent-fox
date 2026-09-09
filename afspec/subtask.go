package afspec

import "fmt"

// subtaskTransitions defines the allowed state transitions for subtasks.
var subtaskTransitions = map[SubtaskState][]SubtaskState{
	SubtaskStatePending:             {SubtaskStateQueued, SubtaskStateDropped},
	SubtaskStateQueued:              {SubtaskStateInProgress, SubtaskStatePending, SubtaskStateDropped},
	SubtaskStateInProgress:          {SubtaskStateDone, SubtaskStatePendingReevaluation},
	SubtaskStateDone:                {SubtaskStatePendingReevaluation},
	SubtaskStatePendingReevaluation: {SubtaskStatePending, SubtaskStateDropped},
	SubtaskStateDropped:             {},
}

// TransitionSubtask looks up a subtask by ID, verifies that the
// transition from the subtask's current state to the target state is
// in the allowed transition table, updates the subtask state, and
// returns a new Tasks copy. The receiver is not modified.
//
// Returns a LifecycleError if the transition is not allowed or the
// subtask ID does not exist.
func (t *TasksV1Json) TransitionSubtask(subtaskID string, target string) (*TasksV1Json, error) {
	targetState := SubtaskState(target)

	// Find the subtask and its current state.
	found := false
	var currentState SubtaskState
	for _, g := range t.TaskGroups {
		for _, s := range g.Subtasks {
			if s.Id == subtaskID {
				found = true
				currentState = s.State
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		return nil, &LifecycleError{
			Msg: fmt.Sprintf("subtask %q not found", subtaskID),
			Err: &SpecError{Msg: fmt.Sprintf("subtask %q not found", subtaskID)},
		}
	}

	// Check if the transition is allowed.
	allowed := subtaskTransitions[currentState]
	valid := false
	for _, a := range allowed {
		if a == targetState {
			valid = true
			break
		}
	}
	if !valid {
		return nil, &LifecycleError{
			Msg: fmt.Sprintf("transition from %s to %s is not allowed for subtask %s", currentState, target, subtaskID),
			Err: &SpecError{Msg: fmt.Sprintf("transition from %s to %s is not allowed", currentState, target)},
		}
	}

	// Deep copy and update the target subtask.
	newTasks := t.deepCopy()
	for gi := range newTasks.TaskGroups {
		for si := range newTasks.TaskGroups[gi].Subtasks {
			if newTasks.TaskGroups[gi].Subtasks[si].Id == subtaskID {
				newTasks.TaskGroups[gi].Subtasks[si].State = targetState
				return newTasks, nil
			}
		}
	}

	return newTasks, nil
}

// CompleteSubtaskStates sets every subtask in each specified group to
// the done state, bypassing the state machine transition rules.
// Returns a new Tasks copy. The receiver is not modified.
//
// Missing group IDs are silently skipped.
func (t *TasksV1Json) CompleteSubtaskStates(groupIDs []int) (*TasksV1Json, error) {
	newTasks := t.deepCopy()
	groupSet := make(map[int]bool, len(groupIDs))
	for _, id := range groupIDs {
		groupSet[id] = true
	}
	for gi := range newTasks.TaskGroups {
		if groupSet[newTasks.TaskGroups[gi].Id] {
			for si := range newTasks.TaskGroups[gi].Subtasks {
				newTasks.TaskGroups[gi].Subtasks[si].State = SubtaskStateDone
			}
		}
	}
	return newTasks, nil
}

// ResetSubtaskStates sets every subtask in each specified group to the
// pending state, bypassing the state machine transition rules.
// Returns a new Tasks copy. The receiver is not modified.
//
// Missing group IDs are silently skipped.
func (t *TasksV1Json) ResetSubtaskStates(groupIDs []int) (*TasksV1Json, error) {
	newTasks := t.deepCopy()
	groupSet := make(map[int]bool, len(groupIDs))
	for _, id := range groupIDs {
		groupSet[id] = true
	}
	for gi := range newTasks.TaskGroups {
		if groupSet[newTasks.TaskGroups[gi].Id] {
			for si := range newTasks.TaskGroups[gi].Subtasks {
				newTasks.TaskGroups[gi].Subtasks[si].State = SubtaskStatePending
			}
		}
	}
	return newTasks, nil
}

// deepCopy creates a deep copy of the TasksV1Json struct, ensuring that
// slice mutations on the copy do not affect the original.
func (t *TasksV1Json) deepCopy() *TasksV1Json {
	newTasks := *t // shallow copy

	// Deep copy TaskGroups slice
	newTasks.TaskGroups = make([]TaskGroup, len(t.TaskGroups))
	for i, g := range t.TaskGroups {
		newTasks.TaskGroups[i] = g

		// Deep copy Subtasks
		newTasks.TaskGroups[i].Subtasks = make([]Subtask, len(g.Subtasks))
		for j, s := range g.Subtasks {
			newTasks.TaskGroups[i].Subtasks[j] = s

			// Deep copy slice fields within each Subtask
			newTasks.TaskGroups[i].Subtasks[j].Details = make([]string, len(s.Details))
			copy(newTasks.TaskGroups[i].Subtasks[j].Details, s.Details)

			newTasks.TaskGroups[i].Subtasks[j].RequirementRefs = make([]string, len(s.RequirementRefs))
			copy(newTasks.TaskGroups[i].Subtasks[j].RequirementRefs, s.RequirementRefs)

			newTasks.TaskGroups[i].Subtasks[j].TestSpecRefs = make([]string, len(s.TestSpecRefs))
			copy(newTasks.TaskGroups[i].Subtasks[j].TestSpecRefs, s.TestSpecRefs)
		}

		// Deep copy Verification.Checks
		newTasks.TaskGroups[i].Verification.Checks = make([]string, len(g.Verification.Checks))
		copy(newTasks.TaskGroups[i].Verification.Checks, g.Verification.Checks)
	}

	// Deep copy Dependencies
	newTasks.Dependencies = make([]TaskDependency, len(t.Dependencies))
	copy(newTasks.Dependencies, t.Dependencies)

	// Deep copy Traceability
	newTasks.Traceability = make([]TraceabilityEntry, len(t.Traceability))
	copy(newTasks.Traceability, t.Traceability)

	return &newTasks
}
