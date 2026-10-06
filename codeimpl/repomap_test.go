package codeimpl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/repomap"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// buildSpy stands in for repomap.Build: it records each call's budget and
// input paths and answers "map-<n>" for the nth call, or err.
type buildSpy struct {
	budgets []int
	inputs  [][]string
	err     error
}

func (s *buildSpy) build(_ context.Context, _ *tools.Workspace, budget int, inputPaths []string) (string, error) {
	s.budgets = append(s.budgets, budget)
	s.inputs = append(s.inputs, inputPaths)
	if s.err != nil {
		return "", s.err
	}
	return mapN(len(s.budgets)), nil
}

func mapN(n int) string { return fmt.Sprintf("```\nmap-%d\n```\n", n) }

// fakeTree is the git state the tree-change check reads; tests move it to say
// the tree changed.
type fakeTree struct {
	head  string
	dirty []string
	err   error
}

func (f *fakeTree) Head(context.Context) (string, error)         { return f.head, f.err }
func (f *fakeTree) DirtyFiles(context.Context) ([]string, error) { return f.dirty, f.err }

func mapOptions(t *testing.T, tokens int, spy *buildSpy, tree *fakeTree, b *scriptedBrain) (Options, *scriptedBrain) {
	t.Helper()
	ws, g, _ := newSpecRepo(t)
	o := newOptions(ws, g, b)
	o.RepoMapTokens = tokens
	if spy != nil {
		o.buildMap = spy.build
	}
	if tree != nil {
		o.treeState = tree
	}
	return o, b
}

// TS-14-22 (integration): impl passes --repo-map-tokens as the budget to
// Build, and the survey carries the map that was built.
func TestTS14_22_ImplPassesTheBudgetToBuild(t *testing.T) {
	spy := &buildSpy{}
	o, b := mapOptions(t, 3000, spy, &fakeTree{head: "abc"}, &scriptedBrain{})
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(spy.budgets) == 0 || spy.budgets[0] != 3000 {
		t.Fatalf("Build budgets = %v, want the first to be 3000", spy.budgets)
	}
	if b.surveyIn.RepoMap != mapN(1) {
		t.Errorf("survey RepoMap = %q, want %q", b.surveyIn.RepoMap, mapN(1))
	}
	if got := strings.Join(spy.inputs[0], ","); !strings.Contains(got, ".specs/09_agent_mode") {
		t.Errorf("input paths = %q, want the spec package among them", got)
	}
}

// TS-14-30 (unit): the map is built before the survey and rebuilt before a
// later phase only when the tree changed. HEAD moves during task 1 only, so
// task 2 gets a rebuild and task 3 reuses it: two builds in all.
func TestTS14_30_ImplRebuildsOnlyWhenTheTreeChanged(t *testing.T) {
	spy := &buildSpy{}
	tree := &fakeTree{head: "h1"}
	b := &scriptedBrain{implement: func(root string, task afspec.Task, attempt int) (Submission, error) {
		if task.Id == 1 {
			tree.head = "h2" // task 1 commits; tasks 2 and 3 leave the tree as it was
		}
		return goodWork(root, task, attempt)
	}}
	o, _ := mapOptions(t, 6000, spy, tree, b)
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(spy.budgets) != 2 {
		t.Fatalf("Build ran %d times, want 2 (survey, then task 2)", len(spy.budgets))
	}
	if b.surveyIn.RepoMap != mapN(1) {
		t.Errorf("survey RepoMap = %q", b.surveyIn.RepoMap)
	}
	if len(b.inputs) != 3 {
		t.Fatalf("%d task phases", len(b.inputs))
	}
	want := []string{mapN(1), mapN(2), mapN(2)}
	for i, in := range b.inputs {
		if in.RepoMap != want[i] {
			t.Errorf("task %d RepoMap = %q, want %q", i+1, in.RepoMap, want[i])
		}
	}
}

// TS-14-30 against the real git: every task commits, so each later task gets a
// map rebuilt from the tree the one before it left, and a declaration task N
// added is in task N+1's map (14-REQ-9.2).
func TestTS14_30_RealGitShowsTaskNsDeclarationsToTaskNPlus1(t *testing.T) {
	b := &scriptedBrain{implement: func(root string, task afspec.Task, attempt int) (Submission, error) {
		src := fmt.Sprintf("package x\n\nfunc Task%dFeature() {}\n", task.Id)
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("task%d.go", task.Id)), []byte(src), 0o644); err != nil {
			return Submission{}, err
		}
		return passingReport(task, "implemented task "+itoa(task.Id)), nil
	}}
	o, _ := mapOptions(t, 6000, nil, nil, b)
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(b.inputs) != 3 {
		t.Fatalf("%d task phases", len(b.inputs))
	}
	if strings.Contains(b.inputs[0].RepoMap, "Task1Feature") {
		t.Errorf("task 1's map shows a declaration that does not exist yet:\n%s", b.inputs[0].RepoMap)
	}
	for i := 1; i < 3; i++ {
		if want := fmt.Sprintf("Task%dFeature", i); !strings.Contains(b.inputs[i].RepoMap, want) {
			t.Errorf("task %d's map lacks %s:\n%s", i+1, want, b.inputs[i].RepoMap)
		}
	}
}

// A failed tree check rebuilds the map before every phase and surfaces
// nothing (14-REQ-9.4).
func TestTS14_30_FailedTreeCheckRebuildsUnconditionally(t *testing.T) {
	spy := &buildSpy{}
	o, b := mapOptions(t, 6000, spy, &fakeTree{err: errors.New("not a git repo")}, &scriptedBrain{})
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Survey, then each of the three tasks.
	if len(spy.budgets) != 4 {
		t.Errorf("Build ran %d times, want 4", len(spy.budgets))
	}
	if len(b.inputs) == 3 && b.inputs[2].RepoMap != mapN(4) {
		t.Errorf("task 3 RepoMap = %q, want %q", b.inputs[2].RepoMap, mapN(4))
	}
}

// The repair loop's phases are phases too: they get the map, and the commit
// the repair made is a tree change the first task sees.
func TestTS14_30_RepairPhaseGetsAMapAndTheNextTaskARebuild(t *testing.T) {
	spy := &buildSpy{}
	b := &scriptedBrain{}
	o, _ := mapOptions(t, 6000, spy, nil, b)
	write(t, o.Workspace.Root, "FAIL", "the suite is red")
	if _, err := o.Git.CommitAll(context.Background(), "chore: break the build\n"); err != nil {
		t.Fatal(err)
	}
	b.repair = func(root string, attempt int) (RepairSubmission, error) {
		if err := os.Remove(filepath.Join(root, "FAIL")); err != nil {
			return RepairSubmission{}, err
		}
		return RepairSubmission{
			Cause: "A FAIL marker was committed.", Summary: "Removed the marker.",
			CommitSubject: "remove the FAIL marker the test target trips on.",
			Changes:       []FileChange{{Path: "FAIL", Change: "deleted"}},
		}, nil
	}
	o.Repair = true
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(b.repairIns) != 1 || b.repairIns[0].RepoMap != mapN(1) {
		t.Fatalf("repair inputs = %+v, want one carrying the survey's map", b.repairIns)
	}
	if len(b.inputs) == 0 || b.inputs[0].RepoMap != mapN(2) {
		t.Errorf("task 1 did not get a rebuild after the repair commit: %+v", b.inputs)
	}
}

// TS-14-33 (unit): a failing Build never fails the run; the phases run without
// a map and a low repo_map_build_failed warning is recorded.
func TestTS14_33_ImplBuildFailureWarnsAndRunsWithoutAMap(t *testing.T) {
	spy := &buildSpy{err: errors.New("walk failed")}
	o, b := mapOptions(t, 6000, spy, &fakeTree{head: "abc"}, &scriptedBrain{})
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v (the map build must not fail the run)", err)
	}
	if got.Stage != "landed" {
		t.Errorf("Stage = %q", got.Stage)
	}
	if b.surveyIn.RepoMap != "" {
		t.Errorf("survey RepoMap = %q, want empty", b.surveyIn.RepoMap)
	}
	for i, in := range b.inputs {
		if in.RepoMap != "" {
			t.Errorf("task %d RepoMap = %q, want empty", i+1, in.RepoMap)
		}
	}
	var found bool
	for _, w := range o.Run.Warnings() {
		if w.Code == toolio.WarnRepoMapBuildFailed {
			found = true
			if w.Severity != "low" {
				t.Errorf("severity = %q, want low", w.Severity)
			}
		}
	}
	if !found {
		t.Errorf("no repo_map_build_failed warning: %+v", o.Run.Warnings())
	}
}

// 14-REQ-5.1 and 14-REQ-5.3: an empty map leaves every prompt as it was, and
// a map is placed after the language block and before the specification.
func TestImplPromptsCarryTheMapBlock(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &scriptedBrain{}
	o := newOptions(ws, g, b)
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	m := repomap.Block(mapN(7))

	sin := b.surveyIn
	plain := surveyPrompt(sin)
	if strings.Contains(plain, "## Repository map") {
		t.Fatal("an empty map produced a block in the survey prompt")
	}
	sin.RepoMap = mapN(7)
	with := surveyPrompt(sin)
	if i, j := strings.Index(with, "## Repository map"), strings.Index(with, "## The specification"); i < 0 || i > j {
		t.Errorf("survey map misplaced (map %d, spec %d)", i, j)
	}
	if strings.Replace(with, m+"\n", "", 1) != plain {
		t.Error("the survey prompt differs from the reference beyond the map block")
	}

	tin := b.inputs[0]
	plain = taskPrompt(tin)
	tin.RepoMap = mapN(7)
	with = taskPrompt(tin)
	if i, j := strings.Index(with, "## Repository map"), strings.Index(with, "## The specification, scoped"); i < 0 || i > j {
		t.Errorf("task map misplaced (map %d, spec %d)", i, j)
	}
	if strings.Replace(with, m+"\n", "", 1) != plain {
		t.Error("the task prompt differs from the reference beyond the map block")
	}
}
