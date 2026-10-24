package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildBinary builds af with make build, into a directory of its own, with
// the version 9.9.9-test, and returns its path.
func buildBinary(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}
	dir := t.TempDir()
	build := exec.Command("make", "build", "VERSION=9.9.9-test", "BIN_DIR="+dir)
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("make build: %v\n%s", err, out)
	}
	return filepath.Join(dir, "af")
}

// execute runs the binary in dir and returns its exit code and output.
func execute(t *testing.T, binary, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("%s %v: %v", binary, args, err)
	}
	return code, out.String(), errb.String()
}

// oneDocument fails the test unless s is exactly one JSON document.
func oneDocument(t *testing.T, s string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("not a JSON document: %v\n%s", err, s)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		t.Fatalf("more than one JSON document (err = %v)\n%s", err, s)
	}
	return doc
}

// TestSmokeVersionAndSchemaFromTheBuiltBinary_TS_05_74 verifies TS-05-74:
// A tool fetches the version and the schema from the real built binary.
// Verifies: 05-PATH-1, 05-REQ-1.6, 05-REQ-10.1, 05-REQ-10.3
func TestSmokeVersionAndSchemaFromTheBuiltBinary_TS_05_74(t *testing.T) {
	binary := buildBinary(t)
	work := t.TempDir()

	code, stdout, stderr := execute(t, binary, work, "--version")
	version := oneDocument(t, stdout)
	if code != 0 || stderr != "" || version["name"] != "af" || version["version"] != "9.9.9-test" {
		t.Errorf("--version: exit code %d, stderr %q, document %v; want 0, nothing, af 9.9.9-test", code, stderr, version)
	}

	code, stdout, stderr = execute(t, binary, work, "--schema")
	oneDocument(t, stdout)
	golden, err := os.ReadFile(filepath.Join("testdata", "schema.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 || stderr != "" || stdout != string(golden) {
		t.Errorf("--schema: exit code %d, stderr %q, %d bytes; want 0, nothing and the golden file (%d bytes)", code, stderr, len(stdout), len(golden))
	}

	if _, err := os.Stat(filepath.Join(work, ".agent-fox")); err == nil {
		t.Error("the binary created a .agent-fox directory")
	}
}

// TestSmokeUnknownCommand_TS_05_77 verifies TS-05-77:
// An unknown command yields a usage envelope with nothing on stderr, from
// the shell wired like main and from the built binary.
// Verifies: 05-PATH-4, 05-REQ-1.2
func TestSmokeUnknownCommand_TS_05_77(t *testing.T) {
	check := func(t *testing.T, code int, stdout, stderr string) {
		t.Helper()
		env := oneDocument(t, stdout)
		errorObject, _ := env["error"].(map[string]any)
		message, _ := errorObject["message"].(string)
		if code != 2 || env["tool"] != "af" || env["status"] != "usage" || stderr != "" {
			t.Errorf("exit code %d, tool %v, status %v, stderr %q; want 2, af, usage, nothing", code, env["tool"], env["status"], stderr)
		}
		if !strings.Contains(message, `"nosuch"`) || !strings.Contains(message, "the commands are:") {
			t.Errorf("error.message = %q, want it to name the command and list the commands", message)
		}
	}

	t.Run("through Main", func(t *testing.T) {
		code, stdout, stderr := run(t, "nosuch")
		check(t, code, stdout, stderr)
	})
	t.Run("through the binary", func(t *testing.T) {
		code, stdout, stderr := execute(t, buildBinary(t), t.TempDir(), "nosuch")
		check(t, code, stdout, stderr)
	})
}

// gitDo runs git in dir, with an identity of its own and none of the
// machine's configuration, and fails the test if it fails.
func gitDo(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// afJSON runs the binary and returns its exit code and the envelope it printed.
func afJSON(t *testing.T, binary, dir string, args ...string) (int, map[string]any) {
	t.Helper()
	code, stdout, stderr := execute(t, binary, dir, args...)
	if stderr != "" {
		t.Errorf("%v: stderr = %q, want nothing", args, stderr)
	}
	return code, oneDocument(t, stdout)
}

// TestSmokeDetectAndWriteTheGate_TS_06_77 verifies TS-06-77:
// a person detects languages and writes the gate table for a repository.
// Verifies: 06-PATH-1, 06-REQ-2.1, 06-REQ-2.8
func TestSmokeDetectAndWriteTheGate_TS_06_77(t *testing.T) {
	binary := buildBinary(t)
	dir := gitRepo(t, map[string]string{
		"Makefile":     "check: lint test\n\t@true\nlint:\n\t@true\ntest:\n\t@true\n",
		"go.mod":       "module example.com/x\n",
		"main.go":      "package main\n\nfunc main() {}\n",
		"util/util.go": "package util\n\nfunc U() {}\n",
	})

	code, env := afJSON(t, binary, dir, "lang", "detect", "--dir", dir, "--write")

	if code != 0 || env["status"] != "done" || env["tool"] != "lang detect" {
		t.Fatalf("exit code %d, status %v, tool %v; want 0, done, lang detect", code, env["status"], env["tool"])
	}
	langs, _ := field(env, "result", "languages").([]any)
	if len(langs) != 1 || field(langs[0].(map[string]any), "name") != "go" || field(langs[0].(map[string]any), "files") != float64(2) {
		t.Errorf("languages = %v, want go with two files", langs)
	}
	if field(env, "result", "gate", "source") != "detected" || fmt.Sprint(checksOf(env)) != "[[make check]]" {
		t.Errorf("gate = %v, want the detected make check", field(env, "result", "gate"))
	}
	if suite, _ := field(env, "result", "suite").([]any); len(suite) < 3 {
		t.Errorf("suite = %v", suite)
	}
	if programs, _ := field(env, "result", "programs").([]any); len(programs) < 2 {
		t.Errorf("programs = %v", programs)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".agent-fox", "gate.json"))
	if err != nil {
		t.Fatal(err)
	}
	var written struct {
		Version int `json:"version"`
		Checks  []struct {
			Argv []string `json:"argv"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(data, &written); err != nil || written.Version != 1 || len(written.Checks) != 1 || fmt.Sprint(written.Checks[0].Argv) != "[make check]" {
		t.Errorf("gate.json = %s (%v)", data, err)
	}
	if status := gitDo(t, dir, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf("git status lists:\n%s", status)
	}

	code, again := afJSON(t, binary, dir, "lang", "detect", "--dir", dir)
	if code != 0 || field(again, "result", "gate", "source") != "file" || fmt.Sprint(checksOf(again)) != "[[make check]]" {
		t.Errorf("the second run: exit code %d, source %v, checks %v; want file and make check", code, field(again, "result", "gate", "source"), checksOf(again))
	}
}

// TestSmokeGateUnderLabels_TS_06_78 verifies TS-06-78:
// the loop runs the gate under labels, hits the cache and compares to
// pass_was_already_failing.
// Verifies: 06-PATH-2, 06-REQ-4.2, 06-REQ-5.1
func TestSmokeGateUnderLabels_TS_06_78(t *testing.T) {
	requireSh(t)
	binary := buildBinary(t)
	dir := gitRepo(t, map[string]string{"src.txt": "content\n"})
	counter := filepath.Join(t.TempDir(), "counter")
	writeGate(t, dir, []string{"counted", "sh", "-c", fmt.Sprintf("echo run >> '%s'; test -f marker.txt", counter)})
	gate := func(label string) map[string]any {
		t.Helper()
		code, env := afJSON(t, binary, dir, "gate", "run", "--dir", dir, "--label", label)
		if code != 0 || env["status"] != "done" {
			t.Fatalf("gate run --label %s: exit code %d, status %v", label, code, env["status"])
		}
		return env
	}

	baseline := gate("baseline")
	if field(baseline, "result", "passed") != false || field(baseline, "result", "cached") != false {
		t.Errorf("baseline: passed %v, cached %v; want a red gate that ran", field(baseline, "result", "passed"), field(baseline, "result", "cached"))
	}
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after := gate("after")
	again := gate("after")

	if field(after, "result", "passed") != true || field(after, "result", "cached") != false {
		t.Errorf("after: passed %v, cached %v; want a green gate that ran", field(after, "result", "passed"), field(after, "result", "cached"))
	}
	if field(again, "result", "cached") != true || field(again, "result", "id") != field(after, "result", "id") {
		t.Errorf("the second after: cached %v, id %v; want a hit of %v", field(again, "result", "cached"), field(again, "result", "id"), field(after, "result", "id"))
	}
	if lines(t, counter) != 2 {
		t.Errorf("the check ran %d times, want 2: the baseline and the first after", lines(t, counter))
	}
	labels := labelsBytes(t, dir)
	var ids map[string]string
	if err := json.Unmarshal([]byte(labels), &ids); err != nil || ids["baseline"] == "" || ids["after"] == "" || ids["baseline"] == ids["after"] {
		t.Errorf("labels.json = %s (%v), want two labels with different ids", labels, err)
	}

	code, compare := afJSON(t, binary, dir, "gate", "compare", "--dir", dir, "--before", "baseline", "--after", "after")
	if code != 0 || compare["status"] != "done" || field(compare, "result", "verdict") != "pass_was_already_failing" || field(compare, "result", "landable") != true {
		t.Errorf("compare: exit code %d, status %v, result %v; want pass_was_already_failing and landable", code, compare["status"], compare["result"])
	}
}

// revertFixture is a repository whose HEAD~ is the base: the working tree
// fixes lib.txt and adds a test that checks for the fix, the fix staged and the
// test not. With vacuous the test passes without the fix.
func revertFixture(t *testing.T, vacuous bool) (dir, base string) {
	t.Helper()
	requireSh(t)
	dir = gitRepo(t, map[string]string{"lib.txt": "broken\n"})
	base = baseOf(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("a later commit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDo(t, dir, "add", "notes.txt")
	gitDo(t, dir, "commit", "-q", "-m", "chore: notes")

	if err := os.WriteFile(filepath.Join(dir, "lib.txt"), []byte("fixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDo(t, dir, "add", "lib.txt")
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

// TestSmokeRevertCheck_TS_06_79 verifies TS-06-79:
// the loop proves a fix with revert-check and gets its tree and index back.
// Verifies: 06-PATH-3, 06-REQ-6.6, 06-REQ-6.7
func TestSmokeRevertCheck_TS_06_79(t *testing.T) {
	binary := buildBinary(t)

	dir, base := revertFixture(t, false)
	before := stateOf(t, dir)
	code, env := afJSON(t, binary, dir, "revert-check", "--dir", dir, "--since", base)

	if code != 0 || env["status"] != "done" || field(env, "result", "outcome") != "proven" || field(env, "result", "closes") != true || field(env, "result", "restored") != true {
		t.Fatalf("exit code %d, status %v, result %v; want proven, closes, restored", code, env["status"], env["result"])
	}
	if slicesContain(codes(env), "fix_not_proven") {
		t.Error("a proven fix carries fix_not_proven")
	}
	reverted, _ := field(env, "result", "reverted_files").([]any)
	if len(reverted) != 2 {
		t.Errorf("reverted_files = %v, want lib.txt and notes.txt", reverted)
	}
	requireGiven(t, dir, before)
	if got, _ := os.ReadFile(filepath.Join(dir, "lib.txt")); string(got) != "fixed\n" {
		t.Errorf("lib.txt holds %q after the check", got)
	}

	dir, base = revertFixture(t, true)
	before = stateOf(t, dir)
	code, env = afJSON(t, binary, dir, "revert-check", "--dir", dir, "--since", base)
	if code != 0 || field(env, "result", "outcome") != "not_proven" || field(env, "result", "closes") != false || !slicesContain(codes(env), "fix_not_proven") {
		t.Errorf("a test that passes without the fix: exit code %d, outcome %v, warnings %v", code, field(env, "result", "outcome"), codes(env))
	}
	requireGiven(t, dir, before)
}

func slicesContain(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// TestSmokeMap_TS_06_81 verifies TS-06-81:
// a brain fetches a budgeted repository map and gets the same bytes every time.
// Verifies: 06-PATH-5, 06-REQ-9.4, 06-REQ-9.5
func TestSmokeMap_TS_06_81(t *testing.T) {
	binary := buildBinary(t)
	files := map[string]string{"go.mod": "module example.com/x\n"}
	for p := range 6 {
		for f := range 4 {
			var src, test strings.Builder
			fmt.Fprintf(&src, "package pkg%d\n\n", p)
			fmt.Fprintf(&test, "package pkg%d\n\n", p)
			for _, fn := range []string{"A", "B", "C"} {
				fmt.Fprintf(&src, "func Pkg%dFile%d%s() {}\n", p, f, fn)
				fmt.Fprintf(&test, "func TestPkg%dFile%d%s() {}\n", p, f, fn)
			}
			files[fmt.Sprintf("pkg%d/file%d.go", p, f)] = src.String()
			files[fmt.Sprintf("pkg%d/file%d_test.go", p, f)] = test.String()
		}
	}
	dir := gitRepo(t, files)

	code, first := afJSON(t, binary, dir, "map", "--dir", dir, "--tokens", "400")
	_, second := afJSON(t, binary, dir, "map", "--dir", dir, "--tokens", "400")
	_, large := afJSON(t, binary, dir, "map", "--dir", dir, "--tokens", "4000")

	if code != 0 || first["status"] != "done" || first["tool"] != "map" {
		t.Fatalf("exit code %d, status %v, tool %v", code, first["status"], first["tool"])
	}
	estimated, _ := field(first, "result", "estimated_tokens").(float64)
	level, _ := field(first, "result", "level").(float64)
	if estimated == 0 || estimated > 400 || level <= 1 || field(first, "result", "truncated") != true {
		t.Errorf("400 tokens: estimated %v, level %v, truncated %v; want a map within the budget that is cut", estimated, level, field(first, "result", "truncated"))
	}
	if m, _ := field(first, "result", "map").(string); (len(m)+3)/4 != int(estimated) {
		t.Errorf("estimated_tokens = %v for a map of %d bytes", estimated, len(m))
	}
	if field(first, "result", "map") != field(second, "result", "map") || field(first, "result", "level") != field(second, "result", "level") {
		t.Error("two runs on one tree gave two maps")
	}

	m, _ := field(large, "result", "map").(string)
	if field(large, "result", "level") != float64(1) || field(large, "result", "truncated") != false ||
		!strings.Contains(m, "  file0.go: func Pkg0File0A L3 · func Pkg0File0B L4 · func Pkg0File0C L5\n") || !strings.Contains(m, "pkg5/\n") {
		t.Errorf("4000 tokens: level %v; map:\n%.600s", field(large, "result", "level"), m)
	}

	// The level is the first that fits, which the test works out from the
	// files it wrote: every level it could be, in order, as it would be laid
	// out, and the first of them that is within 400 tokens.
	layout := func(decls func(test bool, p, f int) string) string {
		var b strings.Builder
		b.WriteString("./\n  go.mod\n")
		for p := range 6 {
			fmt.Fprintf(&b, "pkg%d/\n", p)
			for f := range 4 {
				fmt.Fprintf(&b, "  file%d.go%s\n  file%d_test.go%s\n", f, decls(false, p, f), f, decls(true, p, f))
			}
		}
		return b.String()
	}
	// showing lists the first k declarations of a file, all of them when k is
	// negative, and none of those of a test unless tests is set.
	showing := func(k int, tests bool) func(bool, int, int) string {
		return func(test bool, p, f int) string {
			if test && !tests {
				return ""
			}
			var items []string
			for n := range 3 {
				if k < 0 || n < k {
					prefix := map[bool]string{false: "", true: "Test"}[test]
					items = append(items, fmt.Sprintf("func %sPkg%dFile%d%c L%d", prefix, p, f, 'A'+n, 3+n))
				}
			}
			if k >= 0 && k < 3 {
				items = append(items, fmt.Sprintf("… +%d more", 3-k))
			}
			return ": " + strings.Join(items, " · ")
		}
	}
	candidates := []struct {
		level int
		text  string
	}{
		{1, layout(showing(-1, true))},
		{2, layout(showing(-1, false))},
		{3, layout(showing(2, false))},
		{3, layout(showing(1, false))},
		{4, layout(func(bool, int, int) string { return "" })},
	}
	wantLevel, want := 0, ""
	for _, c := range candidates {
		if len(c.text) <= 400*4 {
			wantLevel, want = c.level, c.text
			break
		}
	}
	if wantLevel <= 1 {
		t.Fatalf("the repository of the test does not make a map that is cut at 400 tokens: level %d", wantLevel)
	}
	if got, _ := field(first, "result", "map").(string); level != float64(wantLevel) || got != want {
		t.Errorf("400 tokens: level %v; want %d, the first that fits:\n%s\nmap:\n%s", level, wantLevel, want, got)
	}
}
