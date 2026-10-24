package afcmd

import (
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

// requireGit skips the test where git is missing.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
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

// counterScript is a check that appends a line to a counter file outside the
// repository.
func counterScript(counter string) []string {
	return []string{"counted", "sh", "-c", fmt.Sprintf("echo run >> '%s'; echo ran", counter)}
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

// gateRun runs af gate run in dir and returns the exit code and the envelope.
func gateRun(t *testing.T, dir string, args ...string) (int, map[string]any) {
	t.Helper()
	code, stdout, stderr := run(t, append([]string{"gate", "run", "--dir", dir}, args...)...)
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
	return code, oneDocument(t, stdout)
}

// TestGateRunRedAndEmpty_TS_06_22 verifies TS-06-22:
// A red gate and an empty table are both done with exit 0 and passed false,
// the empty table with a no_checks warning.
// Verifies: 06-REQ-3.5, 06-REQ-3.6
func TestGateRunRedAndEmpty_TS_06_22(t *testing.T) {
	requireSh(t)

	t.Run("A, a check that fails", func(t *testing.T) {
		dir := gitRepo(t, nil)
		writeGate(t, dir, []string{"red", "sh", "-c", "echo broken; exit 1"})

		code, env := gateRun(t, dir)

		checksRun, _ := field(env, "result", "checks").([]any)
		if code != 0 || env["status"] != "done" || field(env, "result", "passed") != false || len(checksRun) != 1 {
			t.Fatalf("exit code %d, status %v, passed %v, %d checks; want 0, done, false and one check", code, env["status"], field(env, "result", "passed"), len(checksRun))
		}
		if c := checksRun[0].(map[string]any); c["status"] != "fail" || c["exit_code"] != float64(1) || !strings.Contains(c["tail"].(string), "broken") {
			t.Errorf("check = %v, want fail, exit code 1 and the output in the tail", c)
		}
		for _, w := range codes(env) {
			if w == "no_checks" {
				t.Error("a gate that ran is told it has no checks")
			}
		}
		if env["tool"] != "gate run" || env["summary"] == "" {
			t.Errorf("tool %v, summary %q", env["tool"], env["summary"])
		}
	})

	t.Run("B, a program that is not there", func(t *testing.T) {
		dir := gitRepo(t, nil)
		writeGate(t, dir, []string{"absent", "no-such-program-for-af"})

		code, env := gateRun(t, dir)

		skipped, _ := field(env, "result", "skipped").([]any)
		checksRun, _ := field(env, "result", "checks").([]any)
		if code != 0 || env["status"] != "done" || field(env, "result", "passed") != false || len(checksRun) != 0 || len(skipped) != 1 {
			t.Fatalf("exit code %d, status %v, passed %v, %d checks, %d skipped; want 0, done, false, none and one", code, env["status"], field(env, "result", "passed"), len(checksRun), len(skipped))
		}
		if s := skipped[0].(map[string]any); s["name"] != "absent" || s["reason"] != "unavailable" {
			t.Errorf("skipped = %v", s)
		}
		got := map[string]bool{}
		for _, c := range codes(env) {
			got[c] = true
		}
		if !got["no_checks"] || got["check_unavailable"] {
			t.Errorf("warnings = %v, want no_checks and not check_unavailable", codes(env))
		}
	})
}

// TestGateRunCaches_TS_06_26 verifies TS-06-26 through the command: two runs on
// an unchanged tree execute the check once, and a change runs it again.
// Verifies: 06-REQ-4.2
func TestGateRunCaches_TS_06_26(t *testing.T) {
	requireSh(t)
	dir := gitRepo(t, map[string]string{"a.txt": "a\n"})
	counter := filepath.Join(t.TempDir(), "counter")
	writeGate(t, dir, counterScript(counter))

	_, first := gateRun(t, dir)
	id, _ := field(first, "result", "id").(string)
	before := listingOf(t, filepath.Join(dir, ".agent-fox", "gate", id))
	_, second := gateRun(t, dir)

	if lines(t, counter) != 1 {
		t.Errorf("the check ran %d times, want once", lines(t, counter))
	}
	if field(first, "result", "cached") != false || field(second, "result", "cached") != true || field(second, "result", "id") != id {
		t.Errorf("cached %v then %v, ids %v and %v; want false then true and one id", field(first, "result", "cached"), field(second, "result", "cached"), id, field(second, "result", "id"))
	}
	if fmt.Sprint(field(first, "result", "checks")) != fmt.Sprint(field(second, "result", "checks")) {
		t.Errorf("the hit reports %v, want the stored checks %v", field(second, "result", "checks"), field(first, "result", "checks"))
	}
	if after := listingOf(t, filepath.Join(dir, ".agent-fox", "gate", id)); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Errorf("the hit changed the result directory: %v, was %v", after, before)
	}

	// One byte of a file in the view, and the tree is another.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, fourth := gateRun(t, dir)
	if lines(t, counter) != 2 || field(fourth, "result", "cached") != false || field(fourth, "result", "id") == id {
		t.Errorf("after a change: %d runs, cached %v, id %v; want 2, false and another id", lines(t, counter), field(fourth, "result", "cached"), field(fourth, "result", "id"))
	}
}

// listingOf is the names and modification times of the entries of a directory.
func listingOf(t *testing.T, dir string) map[string]int64 {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = info.ModTime().UnixNano()
	}
	return out
}

// TestGateRunTreeChanged_TS_06_23 verifies TS-06-23 through the command:
// a tree changed by the checks is kept under the earlier hash and warned
// about.
// Verifies: 06-REQ-3.7
func TestGateRunTreeChanged_TS_06_23(t *testing.T) {
	requireSh(t)
	dir := gitRepo(t, map[string]string{"a.txt": "a\n"})
	writeGate(t, dir, []string{"grows", "sh", "-c", "echo more >> a.txt"})
	pre, err := checks.TreeHash(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	code, env := gateRun(t, dir)

	if code != 0 || env["status"] != "done" || field(env, "result", "tree") != pre {
		t.Fatalf("exit code %d, status %v, tree %v; want 0, done and %s", code, env["status"], field(env, "result", "tree"), pre)
	}
	named := false
	for _, c := range codes(env) {
		named = named || c == "tree_changed_during_gate"
	}
	if !named {
		t.Errorf("warnings = %v, want tree_changed_during_gate", codes(env))
	}
	now, _ := checks.TreeHash(context.Background(), dir)
	if now == pre {
		t.Error("the check did not change the tree")
	}
	id, _ := field(env, "result", "id").(string)
	if _, err := os.Stat(filepath.Join(dir, ".agent-fox", "gate", id, "result.json")); err != nil {
		t.Errorf("the result is not stored: %v", err)
	}
}

// TestGateRunResultAndFiles guards 06-REQ-3 and 06-REQ-10.2: the result of a run,
// a broken gate.json is failed/config, and no run directory is made.
func TestGateRunResultAndFiles(t *testing.T) {
	requireSh(t)
	dir := gitRepo(t, nil)
	writeGate(t, dir, []string{"ok", "sh", "-c", "exit 0"})

	code, env := gateRun(t, dir)

	if code != 0 || field(env, "result", "passed") != true {
		t.Errorf("exit code %d, passed %v; want 0, true", code, field(env, "result", "passed"))
	}
	if field(env, "result", "table_source") != "file" {
		t.Errorf("table_source = %v, want file", field(env, "result", "table_source"))
	}
	if _, err := os.Stat(filepath.Join(dir, ".agent-fox", "run")); err == nil {
		t.Error("gate run made a run directory")
	}
	if env["run_id"] != nil {
		t.Errorf("run_id = %v, want null", env["run_id"])
	}
	artifacts, _ := env["artifacts"].([]any)
	if len(artifacts) != 1 || field(artifacts[0].(map[string]any), "kind") != "gate_result" {
		t.Errorf("artifacts = %v, want the stored result", artifacts)
	}

	if err := os.WriteFile(filepath.Join(dir, ".agent-fox", "gate.json"), []byte(`{"version":3,"checks":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	code, env = gateRun(t, dir)
	if code != 1 || env["status"] != "failed" || field(env, "error", "category") != "config" {
		t.Errorf("a broken gate.json: exit code %d, status %v, category %v; want 1, failed, config", code, env["status"], field(env, "error", "category"))
	}
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

// waitGone waits for the process to end.
func waitGone(pid int) bool {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if process, err := os.FindProcess(pid); err != nil || process.Signal(syscall.Signal(0)) != nil {
			return true
		}
	}
	return false
}

// spawningGate writes a gate.json whose check starts a child that sleeps and
// waits for it, and returns the file the child's pid is written to.
func spawningGate(t *testing.T, dir string) string {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "pid")
	writeGate(t, dir, []string{"spawns", "sh", "-c", fmt.Sprintf("sleep 60 & echo $! > '%s'; wait", pidFile)})
	return pidFile
}

// TestGateRunTimeout_TS_06_24 verifies TS-06-24, the timeout case:
// the command's own timeout stops the check's process group, stores no
// result.json and fails the command.
// Verifies: 06-REQ-3.8
func TestGateRunTimeout_TS_06_24(t *testing.T) {
	requireSh(t)
	if runtime.GOOS == "windows" {
		t.Skip("no process groups to look at")
	}
	dir := gitRepo(t, nil)
	pidFile := spawningGate(t, dir)

	code, env := gateRun(t, dir, "--timeout", "1s")

	if code != 1 || env["status"] != "failed" || field(env, "error", "category") != "timeout" {
		t.Fatalf("exit code %d, status %v, category %v; want 1, failed, timeout", code, env["status"], field(env, "error", "category"))
	}
	if !waitGone(pidOf(t, pidFile)) {
		t.Error("the child of the check is still running")
	}
	if results, _ := filepath.Glob(filepath.Join(dir, ".agent-fox", "gate", "*", "result.json")); len(results) != 0 {
		t.Errorf("a run that timed out stored %v", results)
	}

	writeGate(t, dir, []string{"spawns", "sh", "-c", "exit 0"})
	if _, again := gateRun(t, dir); field(again, "result", "cached") != false || field(again, "result", "passed") != true {
		t.Errorf("the next run: cached %v, passed %v; want a run that passes", field(again, "result", "cached"), field(again, "result", "passed"))
	}
}
