package checks

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

	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// newRepo is a repository whose first commit holds README.md. The git that
// the code under test runs itself reads none of the machine's configuration.
func newRepo(t *testing.T) string {
	t.Helper()
	testutil.IsolateGit(t)
	return testutil.Repo(t, nil)
}

// writeFile writes a file with the mode under dir.
func writeFile(t *testing.T, dir, name, content string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(testutil.WriteFile(t, dir, name, content), mode); err != nil {
		t.Fatal(err)
	}
}

// shCheck is a check that runs script with sh.
func shCheck(name, script string) project.Check {
	return project.Check{Name: name, Argv: []string{"sh", "-c", script}, Kind: project.KindCheck, Source: "test", Available: true}
}

func tableOf(checks ...project.Check) project.Table {
	return project.Table{Source: project.SourceFile, Checks: checks}
}

func mustRun(t *testing.T, dir string, opts Options) Result {
	t.Helper()
	res, err := Run(t.Context(), dir, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func mustHash(t *testing.T, dir string) string {
	t.Helper()
	h, err := TreeHash(context.Background(), dir)
	if err != nil {
		t.Fatalf("TreeHash(%s): %v", dir, err)
	}
	return h
}

// counterCheck is a check that adds a line to a file outside the repository
// each time it runs, and the file.
func counterCheck(t *testing.T, name string) (counter string, check project.Check) {
	t.Helper()
	counter = filepath.Join(t.TempDir(), "counter")
	return counter, shCheck(name, fmt.Sprintf("echo run >> '%s'; echo ran", counter))
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

// readLog is the log that c names, relative to dir.
func readLog(t *testing.T, dir string, c CheckResult) string {
	t.Helper()
	return testutil.ReadFile(t, filepath.Join(dir, filepath.FromSlash(c.Log)))
}

// readResult is the stored result of id.
func readResult(t *testing.T, dir, id string) Result {
	t.Helper()
	return testutil.Decode[Result](t, []byte(testutil.ReadFile(t, filepath.Join(dir, ".agent-fox", "gate", id, "result.json"))))
}

// fifo makes a named pipe: a check that writes to it waits until the test
// reads, which is how a test learns that a check has got so far.
func fifo(t *testing.T) string {
	t.Helper()
	testutil.Require(t, "mkfifo")
	path := filepath.Join(t.TempDir(), "fifo")
	if out, err := exec.Command("mkfifo", path).CombinedOutput(); err != nil {
		t.Skipf("named pipes are not available: %v %s", err, out)
	}
	return path
}

// pid is the process id that a check wrote.
func pid(t *testing.T, written string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.TrimSpace(written))
	if err != nil {
		t.Fatalf("the check wrote %q for its child's pid", written)
	}
	return n
}

// requireGone waits until the process is no longer running. A killed process
// that init has not reaped yet is a zombie, and gone.
func requireGone(t *testing.T, pid int) {
	t.Helper()
	testutil.Eventually(t, fmt.Sprintf("the child of the check (pid %d) to be killed", pid), func() bool {
		if stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
			i := bytes.LastIndexByte(stat, ')')
			return i > 0 && i+2 < len(stat) && stat[i+2] == 'Z'
		}
		p, err := os.FindProcess(pid)
		return err != nil || p.Signal(syscall.Signal(0)) != nil
	})
}

// storeFixture stores a result under id whose checks, named a, b, c, …, have
// the statuses.
func storeFixture(t *testing.T, dir, id string, statuses ...string) Result {
	t.Helper()
	res := Result{ID: id, Tree: strings.Repeat("0", 64), Checks: []CheckResult{}, Skipped: []Skipped{}, Passed: len(statuses) > 0}
	for i, status := range statuses {
		res.Checks = append(res.Checks, CheckResult{Name: string(rune('a' + i)), Status: status})
		res.Passed = res.Passed && status == StatusPass
	}
	return storeNamed(t, dir, res)
}

// storeNamed stores res as it is.
func storeNamed(t *testing.T, dir string, res Result) Result {
	t.Helper()
	key := Key{ID: res.ID}
	if err := os.MkdirAll(key.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := storeResult(dir, key, res); err != nil {
		t.Fatal(err)
	}
	return res
}
