package fix

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
)

// testRepo is a checkout with a local bare origin, built so that nothing of the
// machine's git configuration matters.
type testRepo struct {
	Dir    string
	Origin string
	Git    *gitx.Git
}

// hermeticGit makes git ignore the configuration of the machine for the rest of
// the test.
func hermeticGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
}

// gitDo runs git in dir.
func gitDo(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, code, err := gitx.ExecRunner(context.Background(), dir, append([]string{"git"}, args...))
	if err != nil || code != 0 {
		t.Fatalf("git %v: %v (%d)\n%s", args, err, code, out)
	}
	return out
}

// newTestRepo makes a repository holding files on the branch main, with a bare
// origin that main is pushed to when withOrigin is set.
func newTestRepo(t *testing.T, files map[string]string, withOrigin bool) *testRepo {
	t.Helper()
	hermeticGit(t)
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.com"}, {"config", "commit.gpgsign", "false"},
		{"add", "-A"}, {"commit", "-q", "--allow-empty", "-m", "chore: initial commit"},
	} {
		gitDo(t, dir, args...)
	}
	r := &testRepo{Dir: dir, Git: gitx.New(dir, gitx.ExecRunner)}
	if withOrigin {
		r.Origin = filepath.Join(t.TempDir(), "origin.git")
		gitDo(t, t.TempDir(), "init", "-q", "--bare", "-b", "main", r.Origin)
		gitDo(t, dir, "remote", "add", "origin", r.Origin)
		gitDo(t, dir, "push", "-q", "-u", "origin", "main")
	}
	return r
}

// A fixture gate: a Makefile whose check target runs every test under tests/
// against a script that is not a test.
var gateFiles = map[string]string{
	"Makefile":            "check:\n\tsh tests/run.sh\n",
	"tests/run.sh":        "set -e\nfor t in tests/*_test.sh; do sh \"$t\"; done\n",
	"tests/greet_test.sh": "[ \"$(sh src/greet.sh)\" = hello ] || { echo \"FAIL: greet printed $(sh src/greet.sh)\"; exit 1; }\n",
	"src/greet.sh":        "echo hello\n",
}

// requireMake skips the test where the fixture gate cannot run.
func requireMake(t *testing.T) {
	t.Helper()
	for _, program := range []string{"make", "sh"} {
		if _, err := exec.LookPath(program); err != nil {
			t.Skipf("%s is not installed", program)
		}
	}
}
