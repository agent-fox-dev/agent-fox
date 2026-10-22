package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/checks"
)

var _ = testing.Short

// baseOf is the hash of HEAD.
func baseOf(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// checksOf lists the argv of the checks of a lang detect result.
func checksOf(env map[string]any) [][]string {
	var out [][]string
	list, _ := field(env, "result", "gate", "checks").([]any)
	for _, c := range list {
		var argv []string
		for _, a := range field(c.(map[string]any), "argv").([]any) {
			argv = append(argv, a.(string))
		}
		out = append(out, argv)
	}
	return out
}

// claude is the payload Claude Code gives a PreToolUse hook.
func claude(t *testing.T, event, tool string, input map[string]any, cwd string) string {
	t.Helper()
	return jsonOf(t, map[string]any{
		"session_id": "abc", "transcript_path": "/x/y.jsonl", "cwd": cwd,
		"hook_event_name": event, "tool_name": tool, "tool_input": input,
	})
}

// codes lists the codes of the warnings of an envelope.
func codes(env map[string]any) []string {
	var out []string
	warnings, _ := env["warnings"].([]any)
	for _, w := range warnings {
		out = append(out, field(w.(map[string]any), "code").(string))
	}
	return out
}

// field follows keys through nested objects.
func field(doc map[string]any, keys ...string) any {
	var v any = doc
	for _, k := range keys {
		obj, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = obj[k]
	}
	return v
}

// gitRepo makes a git repository holding files, committed, built so that
// nothing of the machine's git configuration matters.
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	requireGit(t)
	dir := repoWith(t, files)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "chore: initial commit"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// goRepo is a directory that detection says runs go test ./... as its suite.
func goRepo(t *testing.T) string {
	t.Helper()
	return repoWith(t, map[string]string{"go.mod": "module x\n", "a.go": "package x\n"})
}

// hookDeny parses a --host claude output, which must be a deny.
func hookDeny(t *testing.T, stdout string) string {
	t.Helper()
	var doc struct {
		Hook struct {
			Event    string `json:"hookEventName"`
			Decision string `json:"permissionDecision"`
			Reason   string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	dec := json.NewDecoder(strings.NewReader(stdout))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("not a hook document: %v\n%s", err, stdout)
	}
	if dec.More() {
		t.Fatalf("more than one document:\n%s", stdout)
	}
	if doc.Hook.Event != "PreToolUse" || doc.Hook.Decision != "deny" || doc.Hook.Reason == "" {
		t.Fatalf("hook = %+v, want a PreToolUse denial with a reason\n%s", doc.Hook, stdout)
	}
	return doc.Hook.Reason
}

// labelsBytes is labels.json as it is, or nothing.
func labelsBytes(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".agent-fox", "gate", "labels.json"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

// lines is how many lines a counter file holds.
func lines(t *testing.T, counter string) int {
	t.Helper()
	data, err := os.ReadFile(counter)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

// pidOf reads the pid a check wrote.
func pidOf(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("pid file = %q", data)
	}
	return pid
}

// requireGiven fails the test unless dir is as it was and has no backup.
func requireGiven(t *testing.T, dir string, before treeState) {
	t.Helper()
	after := stateOf(t, dir)
	if after.tree != before.tree || !bytes.Equal(after.index, before.index) {
		t.Errorf("the tree or the index is not as it was (tree %s, was %s)", after.tree, before.tree)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, ".agent-fox", "revert")); len(entries) != 0 {
		t.Errorf("a backup is left in .agent-fox/revert: %d entries", len(entries))
	}
}

// requireSh skips the test where checks cannot be shell scripts.
func requireSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the checks of these tests are shell scripts")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is not installed")
	}
}

func revertCheckSignal(t *testing.T, signal os.Signal) {
	binary := buildBinary(t)
	dir, base, started, pidFile := slowRepo(t)
	before := stateOf(t, dir)

	cmd := exec.Command(binary, "revert-check", "--dir", dir, "--since", base)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(started); err == nil {
			break
		}
	}
	if err := cmd.Process.Signal(signal); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()

	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("af ended with %v, want exit code 1", err)
	}
	env := oneDocument(t, stdout.String())
	if env["status"] != "failed" || field(env, "error", "category") != "cancelled" {
		t.Errorf("status %v, category %v; want failed and cancelled", env["status"], field(env, "error", "category"))
	}
	requireGiven(t, dir, before)
	if !waitGone(pidOf(t, pidFile)) {
		t.Error("the child of the check is still running")
	}
}

// spawningGate writes a gate.json whose check starts a child that sleeps and
// waits for it, and returns the file the child's pid is written to.
func spawningGate(t *testing.T, dir string) string {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "pid")
	writeGate(t, dir, []string{"spawns", "sh", "-c", fmt.Sprintf("sleep 60 & echo $! > '%s'; wait", pidFile)})
	return pidFile
}

// stateOf reads the tree hash and the index of dir.
func stateOf(t *testing.T, dir string) treeState {
	t.Helper()
	tree, err := checks.TreeHash(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	index, _ := os.ReadFile(filepath.Join(dir, ".git", "index"))
	return treeState{tree, index}
}

// waitGone waits for the process to end.
func waitGone(pid int) bool {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if process, err := os.FindProcess(pid); err != nil || process.Signal(syscall.Signal(0)) != nil {
			return true
		}
	}
	return false
}

// writeGate writes dir's gate.json with checks, each a name and a command.
func writeGate(t *testing.T, dir string, checks ...[]string) {
	t.Helper()
	var list []map[string]any
	for _, c := range checks {
		list = append(list, map[string]any{"name": c[0], "argv": c[1:]})
	}
	data, err := json.Marshal(map[string]any{"version": 1, "checks": list})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".agent-fox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".agent-fox", "gate.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// jsonOf is v as JSON.
func jsonOf(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// repoWith makes a directory holding files (path to content).
func repoWith(t *testing.T, files map[string]string) string {
	t.Helper()
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
	return dir
}

// requireGit skips the test where git is missing.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// slowRepo is provenRepo whose check hangs on the reverted tree, and the files
// it reports through.
func slowRepo(t *testing.T) (dir, base, started, pidFile string) {
	t.Helper()
	requireSh(t)
	dir = gitRepo(t, map[string]string{"lib.txt": "broken\n"})
	base = baseOf(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "lib.txt"), []byte("fixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tests", "t.txt"), []byte("t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	aside := t.TempDir()
	started, pidFile = filepath.Join(aside, "started"), filepath.Join(aside, "pid")
	writeGate(t, dir, []string{"tests", "sh", "-c", fmt.Sprintf("if grep -q fixed lib.txt; then exit 0; fi; sleep 60 & echo $! > '%s'; touch '%s'; wait", pidFile, started)})
	return dir, base, started, pidFile
}

// treeState is what a revert check has to give back.
type treeState struct {
	tree  string
	index []byte
}
