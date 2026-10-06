package codeimpl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// surveyHook is the scripted brain with something done to the tree while the
// survey runs: a person working in the same checkout.
type surveyHook struct {
	*scriptedBrain
	during func()
}

func (h *surveyHook) Survey(ctx context.Context, in surveyInput) (Survey, agentrun.Result, error) {
	h.during()
	return h.scriptedBrain.Survey(ctx, in)
}

func mkfile(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Dir(full), filepath.Base(full), body)
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func warningsOf(r *toolio.Run, code toolio.WarnCode) []string {
	var out []string
	for _, w := range r.Warnings() {
		if w.Code == code {
			out = append(out, w.Message)
		}
	}
	return out
}

// Issue #193: a file someone else put in the checkout after the run started
// is not swept into a task's commit, and a discarded attempt does not delete
// it. The attempt's own new file is still taken away by the discard.
func TestAFileThatAppearedBeforeATaskIsLeftAlone(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &scriptedBrain{}
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		if task.Id == 1 && attempt == 1 {
			write(t, root, "FAIL", "the first attempt breaks the checks\n")
		}
		return goodWork(root, task, attempt)
	}
	o := newOptions(ws, g, b)
	o.brain = &surveyHook{scriptedBrain: b, during: func() {
		mkfile(t, ws.Root, "wip/user.go", "package wip // the person's own work\n")
	}}
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v (a file the run left alone must not fail it)", err)
	}
	if !exists(filepath.Join(ws.Root, "wip/user.go")) {
		t.Fatal("the discarded attempt deleted a file the run did not create")
	}
	if exists(filepath.Join(ws.Root, "FAIL")) {
		t.Error("the discarded attempt's own file survived")
	}
	if out := gitOut(t, ws.Root, "log", "--name-only", "--format=", "main..HEAD"); strings.Contains(out, "wip/user.go") {
		t.Errorf("a task's commit carries the person's file:\n%s", out)
	}
	if got := warningsOf(o.Run, toolio.WarnUntrackedFilesLeftAlone); len(got) != 1 || !strings.Contains(got[0], "wip/user.go") {
		t.Errorf("untracked_files_left_alone = %q, want one naming wip/user.go", got)
	}
}

// A spec package another tool writes into the specs directory while a task
// runs is never the task's work, though it appeared during the phase. A new
// file the task wrote but did not list in its changes is committed, with a
// warning.
func TestASpecPackageWrittenDuringATaskIsLeftAlone(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &scriptedBrain{}
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		if task.Id == 2 {
			mkfile(t, root, ".specs/17_prefer_file_tools/prd.md", "# Prefer file tools\n")
			write(t, root, "unlisted.go", "package x // the task forgot to list it\n")
		}
		return goodWork(root, task, attempt)
	}
	o := newOptions(ws, g, b)
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !exists(filepath.Join(ws.Root, ".specs/17_prefer_file_tools/prd.md")) {
		t.Fatal("the spec package was removed")
	}
	files := gitOut(t, ws.Root, "log", "--name-only", "--format=", "main..HEAD")
	if strings.Contains(files, "17_prefer_file_tools") {
		t.Errorf("a task's commit carries another spec's package:\n%s", files)
	}
	if !strings.Contains(files, "unlisted.go") {
		t.Errorf("the task's own unlisted file was not committed:\n%s", files)
	}
	if got := warningsOf(o.Run, toolio.WarnUntrackedFilesLeftAlone); len(got) != 1 ||
		!strings.Contains(got[0], ".specs/17_prefer_file_tools/prd.md") {
		t.Errorf("untracked_files_left_alone = %q", got)
	}
	if got := warningsOf(o.Run, toolio.WarnUnlistedFileCommitted); len(got) != 1 || !strings.Contains(got[0], "unlisted.go") {
		t.Errorf("unlisted_file_committed = %q", got)
	}
}
