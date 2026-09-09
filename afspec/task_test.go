package afspec

import (
	"errors"
	"testing"
)

// TestTaskStateMachine covers §8.3.1. The v1 states queued and
// pending_reevaluation are gone; only the four listed states remain.
func TestTaskStateMachine(t *testing.T) {
	allowed := []struct{ from, to TaskState }{
		{TaskStatePending, TaskStateInProgress},
		{TaskStatePending, TaskStateDropped},
		{TaskStateInProgress, TaskStateDone},
		{TaskStateInProgress, TaskStatePending},
		{TaskStateDone, TaskStatePending},
	}
	for _, tc := range allowed {
		if !ValidTaskTransition(tc.from, tc.to) {
			t.Errorf("%s → %s should be allowed", tc.from, tc.to)
		}
	}

	rejected := []struct{ from, to TaskState }{
		{TaskStatePending, TaskStateDone},
		{TaskStateDone, TaskStateInProgress},
		{TaskStateDone, TaskStateDropped},
		{TaskStateDropped, TaskStatePending},
		{TaskStateDropped, TaskStateInProgress},
		{TaskStateInProgress, TaskStateDropped},
	}
	for _, tc := range rejected {
		if ValidTaskTransition(tc.from, tc.to) {
			t.Errorf("%s → %s should be rejected", tc.from, tc.to)
		}
	}
}

func TestTransitionTaskReturnsANewCopy(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)

	updated, err := spec.Tasks.TransitionTask(1, string(TaskStateInProgress))
	if err != nil {
		t.Fatalf("TransitionTask = %v", err)
	}
	if spec.Tasks.Tasks[0].State != TaskStatePending {
		t.Error("TransitionTask mutated the receiver")
	}
	if updated.Tasks[0].State != TaskStateInProgress {
		t.Errorf("state = %q; want in_progress", updated.Tasks[0].State)
	}

	// The copy's slices must not alias the original's.
	updated.Tasks[0].Steps[0] = "changed"
	if spec.Tasks.Tasks[0].Steps[0] == "changed" {
		t.Error("the copy's Steps slice aliases the original")
	}
}

func TestTransitionTaskRejectsIllegalMoves(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)

	_, err := spec.Tasks.TransitionTask(1, string(TaskStateDone))
	if err == nil {
		t.Fatal("pending → done was accepted; it must go through in_progress")
	}
	var lifecycleErr *LifecycleError
	if !errors.As(err, &lifecycleErr) {
		t.Errorf("error is %T; want *LifecycleError", err)
	}
}

func TestTransitionTaskRejectsUnknownTaskAndState(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)

	if _, err := spec.Tasks.TransitionTask(99, string(TaskStateInProgress)); err == nil {
		t.Error("an unknown task ID was accepted")
	}
	if _, err := spec.Tasks.TransitionTask(1, "queued"); err == nil {
		t.Error("the removed v1 state \"queued\" was accepted")
	}
}

func TestCompleteAndResetTaskStates(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)

	done := spec.Tasks.CompleteTaskStates([]int{1, 2, 99})
	if done.Tasks[0].State != TaskStateDone || done.Tasks[1].State != TaskStateDone {
		t.Error("CompleteTaskStates did not mark the named tasks done")
	}
	if done.Tasks[2].State != TaskStatePending {
		t.Error("CompleteTaskStates touched a task it was not given")
	}
	if spec.Tasks.Tasks[0].State != TaskStatePending {
		t.Error("CompleteTaskStates mutated the receiver")
	}

	reset := done.ResetTaskStates([]int{1})
	if reset.Tasks[0].State != TaskStatePending {
		t.Errorf("state = %q; want pending", reset.Tasks[0].State)
	}
}

func TestGetTaskReturnsACopy(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)

	task, ok := spec.Tasks.GetTask(2)
	if !ok {
		t.Fatal("task 2 not found")
	}
	if task.Title == "" {
		t.Error("the returned task has no title")
	}
	task.Title = "changed"
	if spec.Tasks.Tasks[1].Title == "changed" {
		t.Error("GetTask returned a pointer into the artifact")
	}

	if _, ok := spec.Tasks.GetTask(99); ok {
		t.Error("GetTask found a task that does not exist")
	}
}

func TestIsOptionalDefaultsToFalse(t *testing.T) {
	if (Task{}).IsOptional() {
		t.Error("a task with no optional field reported itself optional")
	}
	yes := true
	if !(Task{Optional: &yes}).IsOptional() {
		t.Error("optional: true was not honoured")
	}
}
