package afspec

import "fmt"

// taskTransitions is the task state machine of format v2 §8.3.1:
//
//	pending ──→ in_progress ──→ done
//	   │             │            │
//	   │             └──→ pending ┘   (released, or reopened upstream)
//	   └──→ dropped
//
// The v1 states `queued` and `pending_reevaluation` are gone; a runtime that
// needs them keeps that state in its own store.
var taskTransitions = map[TaskState][]TaskState{
	TaskStatePending:    {TaskStateInProgress, TaskStateDropped},
	TaskStateInProgress: {TaskStateDone, TaskStatePending},
	TaskStateDone:       {TaskStatePending},
	TaskStateDropped:    {},
}

// ValidTaskTransition reports whether current → target is allowed.
func ValidTaskTransition(current, target TaskState) bool {
	for _, allowed := range taskTransitions[current] {
		if allowed == target {
			return true
		}
	}
	return false
}

// GetTask returns a pointer to a copy of the task with the given ID.
func (t *TasksV2Json) GetTask(id int) (*Task, bool) {
	for i := range t.Tasks {
		if t.Tasks[i].Id == id {
			task := t.Tasks[i]
			return &task, true
		}
	}
	return nil, false
}

// TransitionTask moves one task to the target state and returns a new Tasks
// copy; the receiver is not modified. It returns a LifecycleError when the
// task does not exist or the transition is not allowed by §8.3.1.
func (t *TasksV2Json) TransitionTask(taskID int, target string) (*TasksV2Json, error) {
	targetState := TaskState(target)

	if _, ok := taskTransitions[targetState]; !ok {
		msg := fmt.Sprintf("unknown task state %q", target)
		return nil, &LifecycleError{Msg: msg, Err: &SpecError{Msg: msg}}
	}

	current, found := TaskState(""), false
	for _, task := range t.Tasks {
		if task.Id == taskID {
			current, found = task.State, true
			break
		}
	}
	if !found {
		msg := fmt.Sprintf("task %d not found", taskID)
		return nil, &LifecycleError{Msg: msg, Err: &SpecError{Msg: msg}}
	}

	if !ValidTaskTransition(current, targetState) {
		msg := fmt.Sprintf("transition from %s to %s is not allowed for task %d", current, target, taskID)
		return nil, &LifecycleError{
			Msg: msg,
			Err: &SpecError{Msg: fmt.Sprintf("transition from %s to %s is not allowed", current, target)},
		}
	}

	newTasks := t.deepCopy()
	for i := range newTasks.Tasks {
		if newTasks.Tasks[i].Id == taskID {
			newTasks.Tasks[i].State = targetState
			break
		}
	}
	return newTasks, nil
}

// CompleteTaskStates sets the named tasks to done, bypassing the state
// machine. Unknown IDs are skipped. Returns a new copy.
func (t *TasksV2Json) CompleteTaskStates(taskIDs []int) *TasksV2Json {
	return t.setStates(taskIDs, TaskStateDone)
}

// ResetTaskStates sets the named tasks to pending, bypassing the state
// machine. Unknown IDs are skipped. Returns a new copy.
func (t *TasksV2Json) ResetTaskStates(taskIDs []int) *TasksV2Json {
	return t.setStates(taskIDs, TaskStatePending)
}

func (t *TasksV2Json) setStates(taskIDs []int, state TaskState) *TasksV2Json {
	wanted := make(map[int]bool, len(taskIDs))
	for _, id := range taskIDs {
		wanted[id] = true
	}
	newTasks := t.deepCopy()
	for i := range newTasks.Tasks {
		if wanted[newTasks.Tasks[i].Id] {
			newTasks.Tasks[i].State = state
		}
	}
	return newTasks
}

// IsOptional reports whether the task is marked optional (default false).
func (t Task) IsOptional() bool {
	return t.Optional != nil && *t.Optional
}

// deepCopy returns a copy of the artifact whose slices are independent of the
// receiver's, so that mutations on the copy cannot alias the original.
func (t *TasksV2Json) deepCopy() *TasksV2Json {
	newTasks := *t

	newTasks.Tasks = make([]Task, len(t.Tasks))
	for i, task := range t.Tasks {
		newTasks.Tasks[i] = task
		newTasks.Tasks[i].Criteria = copyStrings(task.Criteria)
		newTasks.Tasks[i].Tests = copyStrings(task.Tests)
		newTasks.Tasks[i].Steps = copyStrings(task.Steps)
		newTasks.Tasks[i].Touches = copyStrings(task.Touches)
		newTasks.Tasks[i].DoneWhen = copyStrings(task.DoneWhen)
		if task.DependsOn != nil {
			newTasks.Tasks[i].DependsOn = make([]int, len(task.DependsOn))
			copy(newTasks.Tasks[i].DependsOn, task.DependsOn)
		}
		if task.Optional != nil {
			opt := *task.Optional
			newTasks.Tasks[i].Optional = &opt
		}
	}

	if t.Dependencies != nil {
		newTasks.Dependencies = make([]Dependency, len(t.Dependencies))
		copy(newTasks.Dependencies, t.Dependencies)
	}

	if t.TestCommands.SpecTests != nil {
		st := *t.TestCommands.SpecTests
		newTasks.TestCommands.SpecTests = &st
	}

	return &newTasks
}

// copyStrings returns an independent copy of a string slice, preserving the
// distinction between nil (field absent) and empty (field present but empty).
func copyStrings(src []string) []string {
	if src == nil {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}
