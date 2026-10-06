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

// TS-14-37 (smoke): impl builds the map before the survey and rebuilds it
// after task 1 commits, so task 2's prompt shows the declarations task 1
// created.
//
// Verifies: 14-PATH-2, 14-REQ-9.2, 14-REQ-9.3
//
// Real components: repomap.Build (wrapped only to record the HEAD each build
// saw), the repomap tree-change check over the real gitx.Git (Head and
// DirtyFiles), taskPrompt, and the real agentBrain's agentrun.Phase for the
// implementation phase. The model is the scripted brain, whose edits are real
// files committed by the pipeline's real git.
func TestTS14_37_ImplRebuildsTheMapWhenTheTreeChangesBetweenTasks(t *testing.T) {
	ws, g, _ := newSpecRepo(t)

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

	// Task 1 committed, so HEAD moved and the map was rebuilt for task 2.
	if len(maps) < 2 || heads[0] == heads[1] {
		t.Fatalf("builds at HEADs %v, want a rebuild at a HEAD different from the first", heads)
	}
	if len(b.inputs) != 3 {
		t.Fatalf("%d task phases, want 3", len(b.inputs))
	}
	if strings.Contains(b.inputs[0].RepoMap, "Task1Feature") {
		t.Errorf("task 1's map shows a declaration that does not exist yet:\n%s", b.inputs[0].RepoMap)
	}
	task2 := b.inputs[1]
	if !strings.Contains(task2.RepoMap, "task1.go") || !strings.Contains(task2.RepoMap, "func Task1Feature") {
		t.Errorf("task 2's map lacks the declaration task 1 created:\n%s", task2.RepoMap)
	}

	// The prompt and the phase the real brain builds for task 2 carry it too.
	if prompt := taskPrompt(task2); !strings.Contains(prompt, "## Repository map") || !strings.Contains(prompt, "func Task1Feature") {
		t.Errorf("task 2's prompt lacks the map:\n%s", prompt)
	}
	var out sink[Submission]
	phase := (&agentBrain{protected: ws.Root}).implementPhase(task2, &out)
	if phase.RepoMap != task2.RepoMap || !strings.Contains(phase.User, "func Task1Feature") {
		t.Errorf("the implementation Phase lacks the rebuilt map: RepoMap %q", phase.RepoMap)
	}
}
