package codeimpl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/afspec"
)

// Issue #200: when the checks pass with the work and still pass with its
// implementation taken out, the code is not what is wrong — the tests are too
// weak. The next attempt continues on the work, told so, instead of starting
// again from nothing; what it adds lands with what the first attempt wrote.
func TestWeakTestsAreStrengthenedOnTheKeptWork(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	// strong_test.go stands for a test that drives task1.go: with it present,
	// the suite fails when task1.go is taken out.
	write(t, ws.Root, "Makefile", "test:\n\t@test ! -f FAIL\n\t@test ! -f strong_test.go || test -f task1.go\nlint:\n\t@exit 0\n")
	if _, err := g.CommitAll(context.Background(), "chore: a suite the tests can depend on\n"); err != nil {
		t.Fatal(err)
	}
	var sawKept bool
	b := &scriptedBrain{}
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		if attempt == 1 {
			write(t, root, "weak_test.go", "package x // asserts nothing about task1.go\n")
			write(t, root, "docs.md", "# what task 1 does\n")
			return goodWork(root, task, attempt)
		}
		for _, f := range []string{"task1.go", "weak_test.go", "docs.md"} {
			if _, err := os.Stat(filepath.Join(root, f)); err != nil {
				t.Errorf("attempt 2 starts without the first attempt's %s", f)
			}
		}
		sawKept = true
		write(t, root, "strong_test.go", "package x // fails without task1.go\n")
		sub := passingReport(task, "implemented task 1")
		sub.Changes = append(sub.Changes, FileChange{Path: "strong_test.go", Change: "added"})
		return sub, nil
	}
	o := newOptions(ws, g, b)
	o.Task, o.NoTestFirst = 1, true
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sawKept || len(b.inputs) != 2 {
		t.Fatalf("attempts = %d", len(b.inputs))
	}
	prompt := taskPrompt(b.inputs[1])
	for _, want := range []string{"still in the tree", "task1.go", "do not rewrite the implementation"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the second attempt's prompt lacks %q", want)
		}
	}
	if strings.Contains(prompt, "nothing of it remains in the tree") {
		t.Error("the second attempt is told its predecessor was discarded")
	}
	task := got.Tasks[0]
	if task.Outcome != OutcomeDone || task.RevertCheck == nil || !task.RevertCheck.Proves {
		t.Fatalf("task 1 = %+v", task)
	}
	files := gitOut(t, ws.Root, "show", "--name-only", "--format=", "HEAD")
	for _, f := range []string{"task1.go", "weak_test.go", "docs.md", "strong_test.go"} {
		if !strings.Contains(files, f) {
			t.Errorf("task 1's commit lacks %s:\n%s", f, files)
		}
	}
}

// A red gate is still a failed attempt: it is discarded, and the next one is
// told so.
func TestARedGateStillDiscardsTheAttempt(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &scriptedBrain{}
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		if attempt == 1 {
			write(t, root, "FAIL", "red\n")
		} else if _, err := os.Stat(filepath.Join(root, "FAIL")); err == nil {
			t.Error("the red attempt's file survived into the next attempt")
		}
		return goodWork(root, task, attempt)
	}
	o := newOptions(ws, g, b)
	o.Task = 1
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if p := taskPrompt(b.inputs[1]); !strings.Contains(p, "It was discarded") {
		t.Errorf("the retry after a red gate is not told the attempt was discarded")
	}
}
