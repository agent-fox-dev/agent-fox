package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mapRun runs af map in dir and returns the exit code and the envelope.
func mapRun(t *testing.T, dir string, args ...string) (int, map[string]any) {
	t.Helper()
	code, stdout, stderr := run(t, append([]string{"map", "--dir", dir}, args...)...)
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
	return code, oneDocument(t, stdout)
}

// mapRepo is a repository with a few files, two of them Go.
func mapRepo(t *testing.T) string {
	t.Helper()
	return repoWith(t, map[string]string{
		"main.go":          "package main\n\nfunc main() {}\n\ntype Server struct{}\n",
		"pkg/util.go":      "package pkg\n\nfunc Helper() {}\n",
		"pkg/util_test.go": "package pkg\n\nfunc TestHelper() {}\n",
		"README.md":        "# readme\n",
	})
}

// TestMapBudgets_TS_06_64 verifies TS-06-64:
// af map defaults to 4000 tokens and rejects budgets below 200 or above
// 100000 as usage.
// Verifies: 06-REQ-9.1, 06-REQ-9.2
func TestMapBudgets_TS_06_64(t *testing.T) {
	dir := mapRepo(t)

	code, env := mapRun(t, dir)
	if code != 0 || env["status"] != "done" || env["tool"] != "map" || field(env, "result", "tokens") != float64(4000) {
		t.Fatalf("exit code %d, status %v, tokens %v; want 0, done and 4000", code, env["status"], field(env, "result", "tokens"))
	}
	if field(env, "result", "level") != float64(1) || field(env, "result", "truncated") != false || field(env, "result", "files_total") != float64(4) {
		t.Errorf("result = %v", env["result"])
	}

	for _, n := range []string{"199", "100001", "0", "-5"} {
		code, env := mapRun(t, dir, "--tokens", n)
		msg, _ := field(env, "error", "message").(string)
		if code != 2 || env["status"] != "usage" || !strings.Contains(msg, "200") || !strings.Contains(msg, "100000") || env["result"] != nil {
			t.Errorf("--tokens %s: exit code %d, status %v, message %q; want usage that names the range", n, code, env["status"], msg)
		}
	}
	for _, n := range []string{"200", "100000"} {
		if code, env := mapRun(t, dir, "--tokens", n); code != 0 || field(env, "result", "tokens") != parse(t, n) {
			t.Errorf("--tokens %s: exit code %d, tokens %v", n, code, field(env, "result", "tokens"))
		}
	}
	if code, env := mapRun(t, dir, "--tokens", "many"); code != 2 || env["status"] != "usage" {
		t.Errorf("--tokens many: exit code %d, status %v", code, env["status"])
	}
}

func parse(t *testing.T, s string) float64 {
	t.Helper()
	var n float64
	if _, err := fmt.Sscan(s, &n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestMapOfARepository guards 06-REQ-9.1 and 06-REQ-9.3 on the real outline:
// the map shows the declarations of Go files, lists the others by name, and is
// the same bytes every time.
func TestMapOfARepository(t *testing.T) {
	dir := mapRepo(t)

	_, first := mapRun(t, dir)
	_, second := mapRun(t, dir)

	want := "./\n  README.md\n  main.go: func main L3 · type Server L5\npkg/\n  util.go: func Helper L3\n  util_test.go: func TestHelper L3\n"
	if got := field(first, "result", "map"); got != want {
		t.Errorf("map = %q, want %q", got, want)
	}
	if field(first, "result", "files_outlined") != float64(3) {
		t.Errorf("files_outlined = %v, want the three Go files... and not README.md", field(first, "result", "files_outlined"))
	}
	if field(first, "result", "map") != field(second, "result", "map") {
		t.Error("two runs on one tree give two maps")
	}
	if first["summary"] == "" || len(codes(first)) != 0 {
		t.Errorf("summary %q, warnings %v", first["summary"], codes(first))
	}
	if _, err := os.Stat(filepath.Join(dir, ".agent-fox")); err == nil {
		t.Error("af map created .agent-fox in a directory that is not a repository")
	}
}

// TestMapSkipsTheOutlinerWhenNamesDoNotFit guards 06-REQ-9.6 on the command:
// the warning is raised, nothing is outlined, and the map still fits.
func TestMapSkipsTheOutlinerWhenNamesDoNotFit(t *testing.T) {
	dir := t.TempDir()
	for i := range 600 {
		path := filepath.Join(dir, fmt.Sprintf("some/long/directory/name/number_%03d", i%30), fmt.Sprintf("file_with_a_long_name_%05d.go", i))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package x\n\nfunc F() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	code, env := mapRun(t, dir, "--tokens", "200")

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v", code, env["status"])
	}
	if got := codes(env); len(got) != 1 || got[0] != "map_outline_skipped" {
		t.Errorf("warnings = %v, want map_outline_skipped", got)
	}
	m, _ := field(env, "result", "map").(string)
	if field(env, "result", "files_outlined") != float64(0) || len(m) > 800 || field(env, "result", "level").(float64) < 4 || field(env, "result", "truncated") != true {
		t.Errorf("result: files_outlined %v, %d bytes, level %v, truncated %v", field(env, "result", "files_outlined"), len(m), field(env, "result", "level"), field(env, "result", "truncated"))
	}
}

// TestMapOfAGitRepository guards 06-REQ-9.1: the map is of the file view, so it
// has the untracked files that are not ignored and not the ignored ones.
func TestMapOfAGitRepository(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.go": "package a\n", ".gitignore": "ignored.go\n", "ignored.go": "package a\n"})
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package a\n\nfunc New() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, env := mapRun(t, dir)

	m, _ := field(env, "result", "map").(string)
	if !strings.Contains(m, "  new.go: func New L3\n") || strings.Contains(m, "ignored.go") || strings.Contains(m, ".git/") || strings.Contains(m, ".agent-fox") {
		t.Errorf("map:\n%s", m)
	}
	if env["run_id"] != nil {
		t.Errorf("run_id = %v", env["run_id"])
	}
}
