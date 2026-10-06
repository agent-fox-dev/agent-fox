package codefix

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
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
	return fmt.Sprintf("```\nmap-%d\n```\n", len(s.budgets)), nil
}

// fakeTree is the git state the tree-change check reads; tests move it to say
// the tree changed.
type fakeTree struct {
	head  string
	dirty []string
	err   error
}

func (f *fakeTree) Head(context.Context) (string, error) { return f.head, f.err }
func (f *fakeTree) DirtyFiles(context.Context) ([]string, error) {
	return f.dirty, f.err
}

// mapBrain records the inputs both phases were given and lets a test move the
// tree during the analysis phase.
type mapBrain struct {
	*scriptedBrain
	analyzeIn  analysisInput
	implIn     implementInput
	onAnalysis func()
}

func (b *mapBrain) Analyze(ctx context.Context, in analysisInput) (Analysis, agentrun.Result, error) {
	b.analyzeIn = in
	_ = analysisPrompt(in) // every prompt must render
	if b.onAnalysis != nil {
		b.onAnalysis()
	}
	return b.scriptedBrain.Analyze(ctx, in)
}

func (b *mapBrain) Implement(ctx context.Context, in implementInput) (Implementation, agentrun.Result, error) {
	b.implIn = in
	return b.scriptedBrain.Implement(ctx, in)
}

func mapOptions(t *testing.T, tokens int, spy *buildSpy, tree *fakeTree, b brain) Options {
	t.Helper()
	ws, g := newRepo(t, 0)
	o := newOptions(ws, g, b)
	o.RepoMapTokens = tokens
	o.buildMap = spy.build
	if tree != nil {
		o.treeState = tree
	}
	return o
}

// TS-14-22 (integration): fix passes --repo-map-tokens as the budget to Build,
// and the phases carry the map that was built.
func TestTS14_22_FixPassesTheBudgetToBuild(t *testing.T) {
	spy := &buildSpy{}
	b := &mapBrain{scriptedBrain: defaultBrain()}
	o := mapOptions(t, 3000, spy, &fakeTree{head: "abc"}, b)

	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(spy.budgets) == 0 || spy.budgets[0] != 3000 {
		t.Fatalf("Build budgets = %v, want the first to be 3000", spy.budgets)
	}
	if want := "```\nmap-1\n```\n"; b.analyzeIn.RepoMap != want {
		t.Errorf("analysis RepoMap = %q, want %q", b.analyzeIn.RepoMap, want)
	}
}

// TS-14-29 (unit): the map is built before the analysis and rebuilt before the
// implementation only when HEAD or the dirty files moved in between.
func TestTS14_29_FixRebuildsOnlyWhenTheTreeChanged(t *testing.T) {
	t.Run("unchanged tree reuses the map", func(t *testing.T) {
		spy := &buildSpy{}
		b := &mapBrain{scriptedBrain: defaultBrain()}
		o := mapOptions(t, 6000, spy, &fakeTree{head: "abc"}, b)
		if _, err := Run(context.Background(), o); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(spy.budgets) != 1 {
			t.Errorf("Build ran %d times, want 1 (no tree change, no rebuild)", len(spy.budgets))
		}
		if b.implIn.RepoMap != b.analyzeIn.RepoMap || b.implIn.RepoMap == "" {
			t.Errorf("implement RepoMap = %q, analysis RepoMap = %q; want the same, non-empty map",
				b.implIn.RepoMap, b.analyzeIn.RepoMap)
		}
	})

	t.Run("HEAD moved: rebuilt", func(t *testing.T) {
		spy := &buildSpy{}
		tree := &fakeTree{head: "abc"}
		b := &mapBrain{scriptedBrain: defaultBrain(), onAnalysis: func() { tree.head = "def" }}
		o := mapOptions(t, 6000, spy, tree, b)
		if _, err := Run(context.Background(), o); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(spy.budgets) != 2 {
			t.Fatalf("Build ran %d times, want 2 (tree changed, rebuilt)", len(spy.budgets))
		}
		if want := "```\nmap-2\n```\n"; b.implIn.RepoMap != want {
			t.Errorf("implement RepoMap = %q, want the rebuilt %q", b.implIn.RepoMap, want)
		}
		// The analysis's files steer the rebuild's reduction (14-REQ-3).
		if got := strings.Join(spy.inputs[1], ","); !strings.Contains(got, "count.go") {
			t.Errorf("rebuild input paths = %q, want the analysis's count.go among them", got)
		}
	})

	t.Run("dirty files moved: rebuilt", func(t *testing.T) {
		spy := &buildSpy{}
		tree := &fakeTree{head: "abc"}
		b := &mapBrain{scriptedBrain: defaultBrain(), onAnalysis: func() { tree.dirty = []string{" M count.go"} }}
		o := mapOptions(t, 6000, spy, tree, b)
		if _, err := Run(context.Background(), o); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(spy.budgets) != 2 {
			t.Errorf("Build ran %d times, want 2", len(spy.budgets))
		}
	})

	t.Run("failed tree check: rebuilt unconditionally", func(t *testing.T) {
		spy := &buildSpy{}
		b := &mapBrain{scriptedBrain: defaultBrain()}
		o := mapOptions(t, 6000, spy, &fakeTree{err: errors.New("not a git repo")}, b)
		if _, err := Run(context.Background(), o); err != nil {
			t.Fatalf("Run: %v (no error may reach the user)", err)
		}
		if len(spy.budgets) != 2 {
			t.Errorf("Build ran %d times, want 2 (14-REQ-9.4)", len(spy.budgets))
		}
		if b.implIn.RepoMap == "" {
			t.Error("the implementation phase got no map")
		}
	})
}

// TS-14-29 against the real git: the analysis writes nothing and the branch
// is cut from the same commit, so the tree is unchanged and one build serves
// both phases.
func TestTS14_29_RealGitReusesTheMapAcrossTheBranch(t *testing.T) {
	spy := &buildSpy{}
	b := &mapBrain{scriptedBrain: defaultBrain()}
	o := mapOptions(t, 6000, spy, nil, b)
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(spy.budgets) != 1 {
		t.Errorf("Build ran %d times, want 1", len(spy.budgets))
	}
}

// TS-14-33 (unit): a failing Build never fails the run; the phases run without
// a map and a low repo_map_build_failed warning is recorded.
func TestTS14_33_FixBuildFailureWarnsAndRunsWithoutAMap(t *testing.T) {
	spy := &buildSpy{err: errors.New("walk failed")}
	b := &mapBrain{scriptedBrain: defaultBrain()}
	o := mapOptions(t, 6000, spy, &fakeTree{head: "abc"}, b)

	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v (the map build must not fail the run)", err)
	}
	if got.Stage != "landed" {
		t.Errorf("Stage = %q", got.Stage)
	}
	if b.analyzeIn.RepoMap != "" || b.implIn.RepoMap != "" {
		t.Errorf("RepoMap = %q / %q, want empty", b.analyzeIn.RepoMap, b.implIn.RepoMap)
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

// 14-REQ-5.3: an empty map leaves both prompts as they were; a map is placed
// where the task steps say.
func TestFixPromptsCarryTheMapBlock(t *testing.T) {
	const m = "```\n./\n  count.go  func Count L3\n```\n"
	ain := analysisInput{
		Input: toolio.Input{Kind: toolio.KindText, Origin: "argument", Body: "it breaks"},
		Root:  "/repo", VerifyCommand: "make test", Instructions: "Use tabs.",
	}
	plain := analysisPrompt(ain)
	if strings.Contains(plain, "## Repository map") {
		t.Fatal("an empty map produced a block")
	}
	ain.RepoMap = m
	with := analysisPrompt(ain)
	if i, j := strings.Index(with, "## Repository map"), strings.Index(with, "make test"); i < 0 || i > j {
		t.Errorf("the analysis map is misplaced (map %d, baseline %d):\n%s", i, j, with)
	}
	if strings.Replace(with, repomap.Block(m)+"\n", "", 1) != plain {
		t.Error("the analysis prompt differs from the reference beyond the map block")
	}

	iin := implementInput{
		Input: toolio.Input{Kind: toolio.KindText, Origin: "argument", Body: "it breaks"},
		Root:  "/repo", Branch: "fix/x", VerifyCommand: "make test",
		Analysis: Analysis{Classification: ClassBug, Summary: "s", RootCause: "r", Approach: "a"},
	}
	plain = implementPrompt(iin)
	iin.RepoMap = m
	with = implementPrompt(iin)
	if i, j := strings.Index(with, "## Repository map"), strings.Index(with, "## The diagnosis"); i < 0 || i > j {
		t.Errorf("the implement map is misplaced (map %d, diagnosis %d)", i, j)
	}
	if strings.Replace(with, "\n"+repomap.Block(m), "", 1) != plain {
		t.Error("the implement prompt differs from the reference beyond the map block")
	}
}
