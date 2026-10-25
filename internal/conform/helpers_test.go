package conform

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// newRepo is a repository whose one commit holds files, and the full hash of
// the commit. The git that the code under test runs itself reads none of the
// machine's configuration.
func newRepo(t *testing.T, files map[string]string) (dir, base string) {
	t.Helper()
	testutil.Require(t, "git", "sh")
	testutil.IsolateGit(t)
	dir = testutil.Repo(t, files)
	return dir, strings.TrimSpace(testutil.Git(t, dir, "rev-parse", "HEAD"))
}

// fixed is a repository whose change since base fixes lib.txt and adds
// tests/check.sh holding test, and a check of kind test that runs it.
func fixed(t *testing.T, test string) (dir, base string, check project.Check) {
	t.Helper()
	dir, base = newRepo(t, map[string]string{"lib.txt": "broken\n"})
	testutil.WriteFile(t, dir, "lib.txt", "fixed\n")
	testutil.WriteFile(t, dir, "tests/check.sh", test+"\n")
	return dir, base, shCheck("tests", project.KindTest, "sh tests/check.sh")
}

// writeFile writes a file with the mode under dir.
func writeFile(t *testing.T, dir, name, content string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(testutil.WriteFile(t, dir, name, content), mode); err != nil {
		t.Fatal(err)
	}
}

// shCheck is a check of the kind that runs script with sh.
func shCheck(name, kind, script string) project.Check {
	return project.Check{Name: name, Argv: []string{"sh", "-c", script}, Kind: kind, Source: "test", Available: true}
}

func tableOf(c ...project.Check) project.Table {
	return project.Table{Source: project.SourceFile, Checks: c}
}

func mustCheck(t *testing.T, dir, since string, opts Options) Result {
	t.Helper()
	res, err := Check(t.Context(), dir, since, opts)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return res
}

// state is what a revert check must give back.
type state struct {
	tree, status string
	index        []byte
}

func snapshot(t *testing.T, dir string) state {
	t.Helper()
	tree, err := checks.TreeHash(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	index, _ := os.ReadFile(filepath.Join(dir, ".git", "index"))
	return state{tree, testutil.Git(t, dir, "--no-optional-locks", "status", "--porcelain=v1", "-z", "-uall"), index}
}

// requireState fails the test unless dir is as it was, with no patch left.
func requireState(t *testing.T, dir string, before state) {
	t.Helper()
	after := snapshot(t, dir)
	if after.tree != before.tree || after.status != before.status {
		t.Errorf("the tree hash is %s and git status %q; they were %s and %q", after.tree, after.status, before.tree, before.status)
	}
	if !bytes.Equal(after.index, before.index) {
		t.Error(".git/index is not byte for byte as it was")
	}
	if _, err := os.Lstat(filepath.Join(dir, ".agent-fox", "revert.patch")); err == nil {
		t.Error(".agent-fox/revert.patch is left after the check")
	}
}

// counterCheck is a check of the kind that adds a line to a file outside the
// repository each time it runs, and the file.
func counterCheck(t *testing.T, name, kind string) (counter string, check project.Check) {
	t.Helper()
	counter = filepath.Join(t.TempDir(), "counter")
	return counter, shCheck(name, kind, fmt.Sprintf("echo run >> '%s'", counter))
}

// runs is how often the check of the counter ran.
func runs(t *testing.T, counter string) int {
	t.Helper()
	data, err := os.ReadFile(counter)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

// fifo makes a named pipe: a check that writes to it, or reads it, waits for
// the test, which is how a test learns that a check has got so far.
func fifo(t *testing.T) string {
	t.Helper()
	testutil.Require(t, "mkfifo")
	path := filepath.Join(t.TempDir(), "fifo")
	if out, err := exec.Command("mkfifo", path).CombinedOutput(); err != nil {
		t.Skipf("named pipes are not available: %v %s", err, out)
	}
	return path
}

// requireGone waits until the process whose id the check wrote is no longer
// running. A killed process that init has not reaped yet is a zombie, and gone.
func requireGone(t *testing.T, written string) {
	t.Helper()
	pid, err := strconv.Atoi(strings.TrimSpace(written))
	if err != nil {
		t.Fatalf("the check wrote %q for its child's pid", written)
	}
	testutil.Eventually(t, fmt.Sprintf("the child of the check (pid %d) to be killed", pid), func() bool {
		if stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
			i := bytes.LastIndexByte(stat, ')')
			return i > 0 && i+2 < len(stat) && stat[i+2] == 'Z'
		}
		p, err := os.FindProcess(pid)
		return err != nil || p.Signal(syscall.Signal(0)) != nil
	})
}
