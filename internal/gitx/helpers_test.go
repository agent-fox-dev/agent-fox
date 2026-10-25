package gitx

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// newRepo is a repository on main whose first commit holds README.md, and a
// Git on it that does not wait between pushes.
func newRepo(t *testing.T) (*Git, string) {
	t.Helper()
	testutil.IsolateGit(t)
	dir := testutil.Repo(t, nil)
	g := New(dir, nil)
	g.SetSleep(func(time.Duration) {})
	return g, dir
}

// write writes files (path to content) under dir.
func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		testutil.WriteFile(t, dir, name, content)
	}
}

// commit writes files and commits them through g, and returns the full hash.
func commit(t *testing.T, g *Git, files map[string]string) string {
	t.Helper()
	write(t, g.Dir, files)
	if _, err := g.CommitAll(t.Context(), "chore: files\n"); err != nil {
		t.Fatal(err)
	}
	head, err := g.RevParse(t.Context(), "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return head
}

// fake is a Runner that answers each command with answer, and counts them.
type fake struct {
	calls  int
	answer func(argv []string) (string, int, error)
}

func (f *fake) run(_ context.Context, _ string, argv []string, _ ...string) (string, int, error) {
	f.calls++
	return f.answer(argv)
}

// alive says whether the process with the pid is still running. A killed
// process that init has not reaped yet is a zombie, and dead.
func alive(pid int) bool {
	if stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		i := bytes.LastIndexByte(stat, ')')
		return i < 0 || i+2 >= len(stat) || stat[i+2] != 'Z'
	}
	p, err := os.FindProcess(pid)
	return err == nil && p.Signal(syscall.Signal(0)) == nil
}
