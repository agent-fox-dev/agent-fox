package codeimpl

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/afspec"
)

// Task state is the one thing this package writes into the spec package,
// and it writes exactly one file for it.
//
// afspec.Save rewrites every artifact from memory and refuses an active spec
// whose intent drifted. Both are right for a tool that owns the package and
// wrong here: a task's commit would then carry reformatting diffs to files
// the task never touched, and an operator who edited the PRD's intent before
// implementing it would see the first task refused after its phase was paid
// for. So the state write is tasks.json alone, through the same
// temp-and-rename the library uses, and the rest of the package is left
// exactly as it was found.

// saveTasks writes tasks.json for the spec's current task states.
func saveTasks(spec *afspec.Spec, dir string) error {
	data, err := afspec.MarshalJSON(spec.Tasks)
	if err != nil {
		return fmt.Errorf("encoding tasks.json: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "tasks.json.tmp.*")
	if err != nil {
		return fmt.Errorf("writing tasks.json: %w", err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("writing tasks.json: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("writing tasks.json: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("writing tasks.json: %w", err)
	}
	if err := os.Rename(name, filepath.Join(dir, "tasks.json")); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("writing tasks.json: %w", err)
	}
	return nil
}

// transition moves one task through the format's state machine, in memory.
func transition(spec *afspec.Spec, id int, target afspec.TaskState) error {
	next, err := spec.Tasks.TransitionTask(id, string(target))
	if err != nil {
		return err
	}
	spec.Tasks = next
	spec.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return nil
}

// reopen releases a task a parked run left in_progress. It is the format's
// own edge — in_progress → pending — and it is what makes a second run
// continue.
func reopen(spec *afspec.Spec, id int) error {
	t, ok := spec.Tasks.GetTask(id)
	if !ok || t.State != afspec.TaskStateInProgress {
		return nil
	}
	return transition(spec, id, afspec.TaskStatePending)
}

// dependenciesOf is what a task waits for: its depends_on when it lists
// one, else the task before it (§8.3), else nothing.
func dependenciesOf(tasks []afspec.Task, i int) []int {
	if len(tasks[i].DependsOn) > 0 {
		return tasks[i].DependsOn
	}
	if i == 0 {
		return nil
	}
	return []int{tasks[i-1].Id}
}

// notReady names the dependency that stops a task from running, if any.
// A dependency that was dropped is a stop rather than a pass: a person
// decided that work would not happen, and whether the dependent still makes
// sense is theirs to decide too.
func notReady(spec *afspec.Spec, i int) (int, afspec.TaskState, bool) {
	for _, dep := range dependenciesOf(spec.Tasks.Tasks, i) {
		t, ok := spec.Tasks.GetTask(dep)
		if !ok {
			return dep, "", true
		}
		if t.State != afspec.TaskStateDone {
			return dep, t.State, true
		}
	}
	return 0, "", false
}

// The commit trailer this tool writes, and the prefix of a parked commit.
// Together they are how a later run recognizes its own work on a branch.
const (
	specTrailer  = "Spec:"
	wipPrefix    = "wip:"
	repairMarker = "repair"
)

// parkedTask reports whether a commit message is this tool's parked
// attempt at a task, and which task.
func parkedTask(message string) (int, bool) {
	what, ok := parked(message)
	if !ok || what == repairMarker {
		return 0, false
	}
	n, err := strconv.Atoi(what)
	return n, err == nil
}

// parkedRepair reports whether a commit message is this tool's parked
// attempt at repairing the checks.
func parkedRepair(message string) bool {
	what, ok := parked(message)
	return ok && what == repairMarker
}

// parked reads the trailer of a wip: commit: "task N" gives "N", "repair"
// gives repairMarker.
func parked(message string) (string, bool) {
	if !strings.HasPrefix(message, wipPrefix) {
		return "", false
	}
	for _, line := range strings.Split(message, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, specTrailer) {
			continue
		}
		// "Spec: 09_agent_mode, task 2" or "Spec: 09_agent_mode, repair"
		_, after, ok := strings.Cut(line, ", ")
		if !ok {
			return "", false
		}
		after = strings.TrimSpace(after)
		if after == repairMarker {
			return repairMarker, true
		}
		if n, ok := strings.CutPrefix(after, "task "); ok {
			return strings.TrimSpace(n), true
		}
		return "", false
	}
	return "", false
}
