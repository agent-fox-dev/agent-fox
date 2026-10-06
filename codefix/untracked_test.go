package codefix

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// analyzeHook is the scripted brain with a file written into the checkout
// while the analyse phase runs: a person working in the same checkout.
type analyzeHook struct {
	*scriptedBrain
	during func()
}

func (h *analyzeHook) Analyze(ctx context.Context, in analysisInput) (Analysis, agentrun.Result, error) {
	h.during()
	return h.scriptedBrain.Analyze(ctx, in)
}

// Issue #193: fix does not commit a file that was in the checkout before its
// implementation phase started, and leaves it where it is.
func TestFixLeavesAFileItDidNotCreateOutOfItsCommit(t *testing.T) {
	ws, g := newRepo(t, 0)
	b := defaultBrain()
	o := newOptions(ws, g, &analyzeHook{scriptedBrain: b, during: func() {
		write(t, ws.Root, "notes.txt", "the person's own notes\n")
	}})
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws.Root, "notes.txt")); err != nil {
		t.Fatalf("notes.txt is gone: %v", err)
	}
	files := gitOutFix(t, ws.Root, "show", "--name-only", "--format=", "HEAD")
	if strings.Contains(files, "notes.txt") {
		t.Errorf("the fix commit carries a file the run did not create:\n%s", files)
	}
	if !strings.Contains(files, "count_test.go") {
		t.Errorf("the fix commit lost its own new file:\n%s", files)
	}
	var left bool
	for _, w := range o.Run.Warnings() {
		left = left || (w.Code == toolio.WarnUntrackedFilesLeftAlone && strings.Contains(w.Message, "notes.txt"))
	}
	if !left {
		t.Errorf("warnings = %+v", o.Run.Warnings())
	}
}

func gitOutFix(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, code, err := gitx.ExecRunner(context.Background(), dir, append([]string{"git"}, args...))
	if err != nil || code != 0 {
		t.Fatalf("git %v: %v (%d) %s", args, err, code, out)
	}
	return strings.TrimSpace(out)
}
