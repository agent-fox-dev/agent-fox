package afcmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/checks"
)

// baseOf is the hash of HEAD.
func baseOf(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// revertRun runs af revert-check in dir and returns the exit code and the
// envelope.
func revertRun(t *testing.T, dir string, args ...string) (int, map[string]any) {
	t.Helper()
	code, stdout, stderr := run(t, append([]string{"revert-check", "--dir", dir}, args...)...)
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
	return code, oneDocument(t, stdout)
}

// treeState is what a revert check has to give back.
type treeState struct {
	tree  string
	index []byte
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

// requireGiven fails the test unless dir is as it was and has no patch left.
func requireGiven(t *testing.T, dir string, before treeState) {
	t.Helper()
	after := stateOf(t, dir)
	if after.tree != before.tree || !bytes.Equal(after.index, before.index) {
		t.Errorf("the tree or the index is not as it was (tree %s, was %s)", after.tree, before.tree)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".agent-fox", "revert.patch")); err == nil {
		t.Error("a patch is left in .agent-fox/revert.patch")
	}
}

// provenRepo is a repository whose change fixes lib.txt and adds a test that
// checks for the fix. With vacuous the test passes whatever lib.txt holds.
func provenRepo(t *testing.T, vacuous bool) (dir, base string) {
	t.Helper()
	requireSh(t)
	dir = gitRepo(t, map[string]string{"lib.txt": "broken\n"})
	base = baseOf(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "lib.txt"), []byte("fixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	test := "grep -q fixed lib.txt\n"
	if vacuous {
		test = "exit 0\n"
	}
	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tests", "check.sh"), []byte(test), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGate(t, dir, []string{"tests", "sh", "tests/check.sh"})
	return dir, base
}

// TestRevertCheckRefusals_TS_06_38 verifies TS-06-38 through the command:
// a --dir that is not the top level, a missing --since and an unresolvable
// --since are usage errors that name the argument, and touch nothing.
// Verifies: 06-REQ-6.1
func TestRevertCheckRefusals_TS_06_38(t *testing.T) {
	dir, base := provenRepo(t, false)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := stateOf(t, dir)

	for _, tc := range []struct {
		name  string
		dir   string
		args  []string
		names string
	}{
		{"a subdirectory", filepath.Join(dir, "sub"), []string{"--since", base}, "--dir"},
		{"no --since", dir, nil, "--since"},
		{"an unresolvable --since", dir, []string{"--since", "nosuchref"}, "--since"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, env := revertRun(t, tc.dir, tc.args...)

			if code != 2 || env["status"] != "usage" {
				t.Fatalf("exit code %d, status %v; want 2 and usage", code, env["status"])
			}
			if msg, _ := field(env, "error", "message").(string); !strings.Contains(msg, tc.names) {
				t.Errorf("message %q does not name %s", msg, tc.names)
			}
			requireGiven(t, dir, before)
		})
	}
}

// TestRevertCheckWarnings_TS_06_40 verifies TS-06-40 through the command:
// every outcome except no_changes carries the warning fix_not_proven, and
// proven does not.
// Verifies: 06-REQ-6.3, 06-REQ-6.6
func TestRevertCheckWarnings_TS_06_40(t *testing.T) {
	requireSh(t)
	cases := []struct {
		name    string
		prepare func(t *testing.T, dir string)
		gate    func(t *testing.T, dir string)
		outcome string
	}{
		{"A, no changes", func(*testing.T, string) {}, nil, "no_changes"},
		{"B, only tests", func(t *testing.T, dir string) {
			_ = os.MkdirAll(filepath.Join(dir, "tests"), 0o755)
			_ = os.WriteFile(filepath.Join(dir, "tests", "t.txt"), []byte("t\n"), 0o644)
		}, nil, "no_code_changes"},
		{"C, only code", func(t *testing.T, dir string) { _ = os.WriteFile(filepath.Join(dir, "lib.txt"), []byte("v2\n"), 0o644) }, nil, "no_test_changes"},
		{"D, no test check", func(t *testing.T, dir string) {
			_ = os.MkdirAll(filepath.Join(dir, "tests"), 0o755)
			_ = os.WriteFile(filepath.Join(dir, "lib.txt"), []byte("v2\n"), 0o644)
			_ = os.WriteFile(filepath.Join(dir, "tests", "t.txt"), []byte("t\n"), 0o644)
		}, func(t *testing.T, dir string) {
			data := `{"version":1,"checks":[{"name":"lint","argv":["sh","-c","exit 0"],"kind":"lint"}]}`
			if err := os.WriteFile(filepath.Join(dir, ".agent-fox", "gate.json"), []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "inconclusive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := gitRepo(t, map[string]string{"lib.txt": "v1\n"})
			base := baseOf(t, dir)
			writeGate(t, dir, []string{"tests", "sh", "-c", "exit 0"})
			tc.prepare(t, dir)
			if tc.gate != nil {
				tc.gate(t, dir)
			}
			before := stateOf(t, dir)

			code, env := revertRun(t, dir, "--since", base)

			if code != 0 || env["status"] != "done" || field(env, "result", "outcome") != tc.outcome || field(env, "result", "restored") != true {
				t.Fatalf("exit code %d, status %v, outcome %v, restored %v; want 0, done, %s, true", code, env["status"], field(env, "result", "outcome"), field(env, "result", "restored"), tc.outcome)
			}
			if got := slices.Contains(codes(env), "fix_not_proven"); got != (tc.outcome != "no_changes") {
				t.Errorf("fix_not_proven present = %v for %s", got, tc.outcome)
			}
			requireGiven(t, dir, before)
		})
	}
}

// TestRevertCheckProvenAndNotProven_TS_06_43 verifies TS-06-43 through the
// command: both exit 0, closes is true only for proven, and only the others
// carry fix_not_proven.
// Verifies: 06-REQ-6.6
func TestRevertCheckProvenAndNotProven_TS_06_43(t *testing.T) {
	t.Run("proven", func(t *testing.T) {
		dir, base := provenRepo(t, false)
		before := stateOf(t, dir)

		code, env := revertRun(t, dir, "--since", base)

		if code != 0 || env["status"] != "done" || env["tool"] != "revert-check" || env["summary"] == "" {
			t.Fatalf("exit code %d, status %v", code, env["status"])
		}
		result, _ := env["result"].(map[string]any)
		if result["outcome"] != "proven" || result["closes"] != true || result["since"] != base || result["restored"] != true || result["after"] == "" {
			t.Errorf("result = %v", result)
		}
		if field(env, "result", "reverted", "passed") != false || len(field(env, "result", "reverted_files").([]any)) != 1 || len(field(env, "result", "test_files").([]any)) != 1 {
			t.Errorf("reverted = %v, reverted_files %v, test_files %v", field(env, "result", "reverted"), result["reverted_files"], result["test_files"])
		}
		if slices.Contains(codes(env), "fix_not_proven") {
			t.Error("a proven fix carries the warning fix_not_proven")
		}
		requireGiven(t, dir, before)
	})

	t.Run("not proven", func(t *testing.T) {
		dir, base := provenRepo(t, true)
		before := stateOf(t, dir)

		code, env := revertRun(t, dir, "--since", base)

		if code != 0 || field(env, "result", "outcome") != "not_proven" || field(env, "result", "closes") != false || field(env, "result", "reverted", "passed") != true {
			t.Errorf("exit code %d, result %v", code, env["result"])
		}
		if !slices.Contains(codes(env), "fix_not_proven") {
			t.Errorf("warnings = %v, want fix_not_proven", codes(env))
		}
		requireGiven(t, dir, before)
	})
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

// TestRevertCheckTimeout_TS_06_45 verifies TS-06-45, the timeout case:
// the tree and the index are as they were when --timeout stops the run.
// Verifies: 06-REQ-6.7
func TestRevertCheckTimeout_TS_06_45(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no process groups to look at")
	}
	dir, base, _, pidFile := slowRepo(t)
	before := stateOf(t, dir)

	code, env := revertRun(t, dir, "--since", base, "--timeout", "3s")

	if code != 1 || env["status"] != "failed" || field(env, "error", "category") != "timeout" {
		t.Fatalf("exit code %d, status %v, category %v; want 1, failed, timeout", code, env["status"], field(env, "error", "category"))
	}
	requireGiven(t, dir, before)
	if !waitGone(pidOf(t, pidFile)) {
		t.Error("the child of the check is still running")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "lib.txt")); string(data) != "fixed\n" {
		t.Errorf("lib.txt holds %q", data)
	}
}

// leavePatch puts a patch of an earlier check in dir, and returns its path.
func leavePatch(t *testing.T, dir string) string {
	t.Helper()
	old := filepath.Join(dir, ".agent-fox", "revert.patch")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("old patch"), 0o644); err != nil {
		t.Fatal(err)
	}
	return old
}

// TestRevertCheckLeftoverPatch_TS_06_46 verifies TS-06-46 through the
// command: a leftover patch is failed/restore, names the file and is left
// alone.
// Verifies: 06-REQ-6.8
func TestRevertCheckLeftoverPatch_TS_06_46(t *testing.T) {
	dir, base := provenRepo(t, false)
	old := leavePatch(t, dir)
	before := stateOf(t, dir)

	code, env := revertRun(t, dir, "--since", base)

	if code != 1 || env["status"] != "failed" || field(env, "error", "category") != "restore" {
		t.Fatalf("exit code %d, status %v, category %v; want 1, failed, restore", code, env["status"], field(env, "error", "category"))
	}
	if msg, _ := field(env, "error", "message").(string); !strings.Contains(msg, old) {
		t.Errorf("message %q does not name %s", msg, old)
	}
	if data, err := os.ReadFile(old); err != nil || string(data) != "old patch" {
		t.Errorf("the old patch was touched: %q, %v", data, err)
	}
	after := stateOf(t, dir)
	if after.tree != before.tree || !bytes.Equal(after.index, before.index) {
		t.Error("the tree changed although the check refused")
	}
}

// TestFailureCategories_TS_06_74 verifies TS-06-74: non-usage failures use the
// categories config, restore, git and io.
// Verifies: 06-REQ-10.6
func TestFailureCategories_TS_06_74(t *testing.T) {
	requireSh(t)

	t.Run("config, a gate.json that is not one", func(t *testing.T) {
		dir := gitRepo(t, nil)
		if err := os.MkdirAll(filepath.Join(dir, ".agent-fox"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".agent-fox", "gate.json"), []byte("{broken"), 0o644); err != nil {
			t.Fatal(err)
		}
		code, env := revertRun(t, dir, "--since", baseOf(t, dir))
		expectFailure(t, code, env, "config")
	})

	t.Run("restore, a patch from an earlier check", func(t *testing.T) {
		dir, base := provenRepo(t, false)
		leavePatch(t, dir)
		code, env := revertRun(t, dir, "--since", base)
		expectFailure(t, code, env, "restore")
	})

	t.Run("git, git that cannot be run", func(t *testing.T) {
		dir, base := provenRepo(t, false)
		t.Setenv("PATH", t.TempDir())
		code, env := revertRun(t, dir, "--since", base)
		expectFailure(t, code, env, "git")
	})

	t.Run("io, an .agent-fox that is a file", func(t *testing.T) {
		dir := gitRepo(t, map[string]string{"a.txt": "a\n"})
		if err := os.WriteFile(filepath.Join(dir, ".agent-fox"), []byte("not a directory"), 0o644); err != nil {
			t.Fatal(err)
		}
		code, stdout, _ := run(t, "gate", "run", "--dir", dir)
		expectFailure(t, code, oneDocument(t, stdout), "io")
	})
}

// expectFailure fails the test unless the envelope is a failure of the
// category.
func expectFailure(t *testing.T, code int, env map[string]any, category string) {
	t.Helper()
	if code != 1 || env["status"] != "failed" || field(env, "error", "category") != category {
		t.Errorf("exit code %d, status %v, category %v, message %v; want 1, failed, %s",
			code, env["status"], field(env, "error", "category"), field(env, "error", "message"), category)
	}
}

// TestRevertCheckRedTree_TS_06_41 verifies TS-06-41 through the command: a red
// current tree is a measured fact, done, inconclusive for the reason
// after_not_green, with the warning, and nothing is reverted.
// Verifies: 06-REQ-6.4
func TestRevertCheckRedTree_TS_06_41(t *testing.T) {
	requireSh(t)
	dir, base := provenRepo(t, false)
	writeGate(t, dir, []string{"tests", "sh", "-c", "echo red; exit 1"})
	before := stateOf(t, dir)

	code, env := revertRun(t, dir, "--since", base)

	if code != 0 || env["status"] != "done" || field(env, "result", "outcome") != "inconclusive" || field(env, "result", "reason") != "after_not_green" {
		t.Fatalf("exit code %d, status %v, result %v", code, env["status"], env["result"])
	}
	if !slicesContain(codes(env), "fix_not_proven") || field(env, "result", "after") == "" || field(env, "result", "reverted") != nil {
		t.Errorf("warnings %v, after %v, reverted %v", codes(env), field(env, "result", "after"), field(env, "result", "reverted"))
	}
	requireGiven(t, dir, before)
}

// TestRevertCheckChangedTable guards 06-REQ-6.6: a Makefile that the change
// added is the whole table of checks, and a revert takes it away; a check
// that is not there to run is no proof that the tests fail without the fix.
func TestRevertCheckChangedTable(t *testing.T) {
	requireSh(t)
	dir := gitRepo(t, map[string]string{"lib.txt": "broken\n"})
	base := baseOf(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "lib.txt"), []byte("fixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tests", "t.txt"), []byte("t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("check:\n\t@true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := stateOf(t, dir)

	code, env := revertRun(t, dir, "--since", base)

	if code != 0 || field(env, "result", "outcome") != "inconclusive" || field(env, "result", "reason") != "table_changed_by_revert" || field(env, "result", "closes") != false {
		t.Errorf("exit code %d, result %v; want inconclusive because the table changed, and no proof", code, env["result"])
	}
	requireGiven(t, dir, before)
}
