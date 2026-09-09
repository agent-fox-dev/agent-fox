package checks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectPrefersTheMakefile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module x\n")
	if got := Detect(dir); got != "go test ./... -count=1" {
		t.Errorf("with only go.mod: %q", got)
	}

	write(t, dir, "Makefile", "build:\n\tgo build ./...\ntest:\n\tgo test ./...\n")
	if got := Detect(dir); got != "make test" {
		t.Errorf("with a test target: %q", got)
	}

	write(t, dir, "Makefile", "check: lint test\n\ntest:\n\tgo test ./...\n")
	if got := Detect(dir); got != "make check" {
		t.Errorf("with a check target: %q", got)
	}
}

func TestDetectPerLanguage(t *testing.T) {
	cases := []struct{ manifest, extra, want string }{
		{"go.mod", "", "go test ./... -count=1"},
		{"package.json", `{"scripts":{"test":"jest"}}`, "npm test"},
		{"pyproject.toml", "", "pytest -q"},
		{"Cargo.toml", "", "cargo test"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		content := c.extra
		if content == "" {
			content = "x\n"
		}
		write(t, dir, c.manifest, content)
		if got := Detect(dir); got != c.want {
			t.Errorf("%s: Detect = %q, want %q", c.manifest, got, c.want)
		}
	}

	// uv.lock changes the answer for a Python project.
	dir := t.TempDir()
	write(t, dir, "pyproject.toml", "x\n")
	write(t, dir, "uv.lock", "x\n")
	if got := Detect(dir); got != "uv run pytest -q" {
		t.Errorf("with uv.lock: %q", got)
	}
}

// "I cannot tell" is a first-class outcome, not a failure: the pipeline then
// refuses to claim the work is verified instead of inventing a command.
func TestDetectReturnsEmptyWhenItCannotTell(t *testing.T) {
	if got := Detect(t.TempDir()); got != "" {
		t.Errorf("Detect on an empty directory = %q, want the empty string", got)
	}
}

func TestRunReportsExitStatus(t *testing.T) {
	ctx := context.Background()
	runner := func(_ context.Context, dir string, argv []string, _ ...string) (string, int, error) {
		if argv[0] != "make" || argv[1] != "check" {
			t.Errorf("argv = %v", argv)
		}
		return "FAIL\tpkg/x\n", 2, nil
	}
	res := Run(ctx, runner, "/tmp", "make check", time.Minute)
	if res.OK || res.ExitCode != 2 {
		t.Errorf("res = %+v", res)
	}
	if !res.Ran() {
		t.Error("Ran() should be true")
	}
	if !strings.Contains(res.Output, "FAIL") {
		t.Errorf("Output = %q", res.Output)
	}
}

func TestRunSkipsAnEmptyCommand(t *testing.T) {
	res := Run(context.Background(), nil, "/tmp", "  ", time.Minute)
	if !res.Skipped || res.Ran() || res.OK {
		t.Errorf("res = %+v", res)
	}
}

func TestRunReportsAMissingProgram(t *testing.T) {
	runner := func(context.Context, string, []string, ...string) (string, int, error) {
		return "", -1, errors.New("exec: \"nope\": executable file not found in $PATH")
	}
	res := Run(context.Background(), runner, "/tmp", "nope --version", time.Minute)
	if res.OK || res.ExitCode != -1 {
		t.Errorf("res = %+v", res)
	}
	if !strings.Contains(res.Output, "executable file not found") {
		t.Errorf("Output = %q", res.Output)
	}
}

// "The tests must pass" and "note the pre-existing failures" cannot both be
// satisfied on a repository that was already red. Compare names the case.
func TestCompareNamesEveryBeforeAfterPair(t *testing.T) {
	ran := func(ok bool, code int) Result {
		return Result{Command: "make check", OK: ok, ExitCode: code}
	}
	skipped := Result{Skipped: true}

	cases := []struct {
		name            string
		baseline, after Result
		want            Verdict
		wantLandable    bool
	}{
		{"green to green", ran(true, 0), ran(true, 0), VerdictPass, true},
		{"red to green", ran(false, 1), ran(true, 0), VerdictRepaired, true},
		{"green to red", ran(true, 0), ran(false, 1), VerdictRegressed, false},
		{"red to red", ran(false, 1), ran(false, 1), VerdictStillFailing, false},
		{"nothing ran", skipped, skipped, VerdictUnverified, false},
		{"no baseline, green after", skipped, ran(true, 0), VerdictPass, true},
	}
	for _, c := range cases {
		got := Compare(c.baseline, c.after)
		if got != c.want {
			t.Errorf("%s: Compare = %s, want %s", c.name, got, c.want)
		}
		if got.Landable() != c.wantLandable {
			t.Errorf("%s: Landable = %v, want %v", c.name, got.Landable(), c.wantLandable)
		}
	}
}

func TestProgram(t *testing.T) {
	if got := Program("uv run pytest -q"); got != "uv" {
		t.Errorf("Program = %q", got)
	}
	if got := Program("/usr/bin/make check"); got != "make" {
		t.Errorf("Program = %q", got)
	}
	if got := Program(""); got != "" {
		t.Errorf("Program = %q", got)
	}
}

func TestTailKeepsTheEnd(t *testing.T) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, string(rune('a'+i%26)))
	}
	got := Tail(strings.Join(lines, "\n"), 5)
	if strings.Count(got, "\n") != 5 {
		t.Errorf("Tail kept %d lines:\n%s", strings.Count(got, "\n")+1, got)
	}
	if !strings.HasPrefix(got, "[…]") {
		t.Error("a truncated tail should say so")
	}
	if !strings.HasSuffix(got, lines[len(lines)-1]) {
		t.Error("the last line must survive: it is where the failure is")
	}
}
