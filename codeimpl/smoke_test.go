package codeimpl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/repomap"
)

// twoTasks folds the example's three tasks into the two-task spec TS-14-37
// describes: task 1 takes the first two tasks' criteria and tests, and the
// integration task becomes task 2.
func twoTasks(tasks []any) []any {
	first, second, last := tasks[0].(map[string]any), tasks[1].(map[string]any), tasks[2].(map[string]any)
	first["criteria"] = append(first["criteria"].([]any), second["criteria"].([]any)...)
	first["tests"] = append(first["tests"].([]any), second["tests"].([]any)...)
	last["id"] = 2
	last["depends_on"] = []any{1}
	return []any{first, last}
}

// TS-14-37 (smoke): impl builds the map before the survey and rebuilds it
// after task 1 commits, so task 2's prompt shows the declarations task 1
// created.
//
// Verifies: 14-PATH-2, 14-REQ-9.2, 14-REQ-9.3
//
// Real components: repomap.Build (wrapped only to record the HEAD each build
// saw), the repomap tree-change check over the real gitx.Git (Head and
// DirtyFiles), taskPrompt, and the agentrun.Phase the real agentBrain builds
// for task 2. The model is the scripted brain, whose edits are real files the
// pipeline commits with real git; the runner is not among the components the
// spec names, and its prompt handling has tests of its own (TS-14-26).
func TestTS14_37_ImplRebuildsTheMapWhenTheTreeChangesBetweenTasks(t *testing.T) {
	ws, g, _ := newSpecRepoWith(t, twoTasks)

	b := &scriptedBrain{implement: func(root string, task afspec.Task, attempt int) (Submission, error) {
		src := fmt.Sprintf("package x\n\nfunc Task%dFeature() {}\n", task.Id)
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("task%d.go", task.Id)), []byte(src), 0o644); err != nil {
			return Submission{}, err
		}
		return passingReport(task, "implemented task "+itoa(task.Id)), nil
	}}
	o := newOptions(ws, g, b)
	o.RepoMapTokens = 6000

	// Each build records the HEAD it was built at and the map it returned.
	var heads, maps []string
	var budgets []int
	o.buildMap = func(ctx context.Context, w *tools.Workspace, budget int, in []string) (string, error) {
		head, err := g.Head(ctx)
		if err != nil {
			return "", err
		}
		m, err := repomap.Build(ctx, w, budget, in)
		heads, maps, budgets = append(heads, head), append(maps, m), append(budgets, budget)
		return m, err
	}

	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(b.inputs) != 2 {
		t.Fatalf("%d task phases, want the spec's 2", len(b.inputs))
	}

	// The map is built before the survey, at the budget the flag gave.
	if len(maps) == 0 || budgets[0] != 6000 {
		t.Fatalf("Build budgets = %v, want a first build at 6000", budgets)
	}
	if b.surveyIn.RepoMap == "" || b.surveyIn.RepoMap != maps[0] {
		t.Errorf("the survey's map = %q, want the first build's %q", b.surveyIn.RepoMap, maps[0])
	}
	if !strings.Contains(surveyPrompt(b.surveyIn), "## Repository map") {
		t.Error("the survey prompt has no map block")
	}

	// Nothing changed the tree between the survey and task 1, so task 1 got
	// the survey's map; task 1's commit moved HEAD, so task 2 got a rebuild.
	if len(maps) != 2 {
		t.Fatalf("%d builds at HEADs %v, want 2: before the survey, and after task 1 committed", len(maps), heads)
	}
	if heads[0] == heads[1] {
		t.Errorf("both builds saw HEAD %s; the rebuild must follow task 1's commit", heads[0])
	}
	task1, task2 := b.inputs[0], b.inputs[1]
	if task1.RepoMap != maps[0] || strings.Contains(task1.RepoMap, "Task1Feature") {
		t.Errorf("task 1's map is not the survey's, or shows a declaration that does not exist yet:\n%s", task1.RepoMap)
	}
	if task2.RepoMap != maps[1] || !strings.Contains(task2.RepoMap, "task1.go") || !strings.Contains(task2.RepoMap, "func Task1Feature") {
		t.Errorf("task 2's map is not the rebuild, or lacks the declaration task 1 created:\n%s", task2.RepoMap)
	}

	// Task 2's prompt, and the phase the real brain builds from it, carry it.
	if prompt := taskPrompt(task2); !strings.Contains(prompt, "## Repository map") || !strings.Contains(prompt, "func Task1Feature") {
		t.Errorf("task 2's prompt lacks the map:\n%s", prompt)
	}
	var out sink[Submission]
	phase := (&agentBrain{protected: ws.Root}).implementPhase(task2, &out)
	if phase.RepoMap != task2.RepoMap || !strings.Contains(phase.User, "func Task1Feature") {
		t.Errorf("the implementation Phase lacks the rebuilt map: RepoMap %q", phase.RepoMap)
	}
}
