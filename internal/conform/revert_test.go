package conform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/project"
)

var hash40 = regexp.MustCompile(`^[0-9a-f]{40}$`)

// requireGitAndSh skips the test where there is no git or no POSIX shell.
func requireGitAndSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the checks of these tests are shell scripts")
	}
	for _, program := range []string{"git", "sh"} {
		if _, err := exec.LookPath(program); err != nil {
			t.Skipf("%s is not installed", program)
		}
	}
}

// gitIn runs git in dir with an identity of its own and none of the user's
// configuration, and returns what it printed.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{
		"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false",
	}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// writeFile creates a file, and its directories, under dir.
func writeFile(t *testing.T, dir, name, content string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// fileSpec is a file of a fixture.
type fileSpec struct {
	content string
	mode    os.FileMode
}

// newRepo makes a git repository whose one commit holds files, and returns it
// with the hash of the commit.
func newRepo(t *testing.T, files map[string]fileSpec) (dir, base string) {
	t.Helper()
	requireGitAndSh(t)
	dir = t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	writeFile(t, dir, "README.md", "# x\n", 0o644)
	for name, f := range files {
		writeFile(t, dir, name, f.content, f.mode)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "chore: base")
	return dir, strings.TrimSpace(gitIn(t, dir, "rev-parse", "HEAD"))
}

// plain is a regular file of a fixture.
func plain(content string) fileSpec { return fileSpec{content, 0o644} }

// shCheck is a check of the given kind that runs script with sh.
func shCheck(name, kind, script string) project.Check {
	return project.Check{Name: name, Argv: []string{"sh", "-c", script}, Kind: kind, Source: "test", Available: true}
}

// tableOf is a gate table of the given checks.
func tableOf(c ...project.Check) project.Table {
	return project.Table{Source: project.SourceFile, Checks: c}
}

// state is everything about a repository that a revert check must give back.
type state struct {
	tree   string
	index  []byte
	status string
}

// snapshot reads the state of dir.
func snapshot(t *testing.T, dir string) state {
	t.Helper()
	tree, err := checks.TreeHash(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	index, _ := os.ReadFile(filepath.Join(dir, ".git", "index"))
	status := gitIn(t, dir, "--no-optional-locks", "status", "--porcelain=v1", "-z", "-uall")
	return state{tree, index, status}
}

// requireState fails the test unless dir is as it was.
func requireState(t *testing.T, dir string, before state) {
	t.Helper()
	after := snapshot(t, dir)
	if after.tree != before.tree {
		t.Errorf("the tree hash is %s, was %s", after.tree, before.tree)
	}
	if !bytes.Equal(after.index, before.index) {
		t.Error(".git/index is not byte for byte as it was")
	}
	if after.status != before.status {
		t.Errorf("git status is %q, was %q", after.status, before.status)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, ".agent-fox", "revert"))
	if len(entries) != 0 {
		t.Errorf(".agent-fox/revert holds %d entries after the check", len(entries))
	}
}

// mustCheck is Check, failing the test on an error.
func mustCheck(t *testing.T, dir, since string, opts Options) Result {
	t.Helper()
	res, err := Check(context.Background(), dir, since, opts)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return res
}

// counterCheck is a check of the given kind that appends a line to a counter
// file outside the repository.
func counterCheck(t *testing.T, name, kind string) (counter string, check project.Check) {
	t.Helper()
	counter = filepath.Join(t.TempDir(), "counter")
	return counter, shCheck(name, kind, fmt.Sprintf("echo run >> '%s'", counter))
}

// runs is how many lines the counter file holds.
func runs(t *testing.T, counter string) int {
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

// TestCheckRefusesBeforeTouchingAnything_TS_06_38 verifies TS-06-38:
// revert-check refuses a --dir that is not the top level, a missing --since and
// an unresolvable --since before touching any file.
// Verifies: 06-REQ-6.1
func TestCheckRefusesBeforeTouchingAnything_TS_06_38(t *testing.T) {
	dir, _ := newRepo(t, map[string]fileSpec{"sub/a.txt": plain("a\n")})
	before := snapshot(t, dir)
	notARepo := t.TempDir()

	for _, tc := range []struct {
		name, dir, since, names string
	}{
		{"a subdirectory", filepath.Join(dir, "sub"), "HEAD", "--dir"},
		{"not a repository", notARepo, "HEAD", "--dir"},
		{"no --since", dir, "", "--since"},
		{"a ref that does not exist", dir, "nosuchref", "--since"},
		{"a ref that is not a commit", dir, "HEAD:README.md", "--since"},
		{"an option for a ref", dir, "--output=x", "--since"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Check(context.Background(), tc.dir, tc.since, Options{Table: tableOf(shCheck("t", project.KindTest, "exit 0"))})

			var usage *checks.UsageError
			if !errors.As(err, &usage) || !strings.Contains(usage.Message, tc.names) {
				t.Fatalf("Check = %v, want a usage error that names %s", err, tc.names)
			}
			requireState(t, dir, before)
			// Taking the snapshot made .agent-fox, with its .gitignore; a refused
			// check adds nothing to it.
			if entries, _ := os.ReadDir(filepath.Join(dir, ".agent-fox")); len(entries) != 1 || entries[0].Name() != ".gitignore" {
				t.Errorf(".agent-fox holds %d entries after a refused check, want only .gitignore", len(entries))
			}
			if _, err := os.Stat(filepath.Join(notARepo, ".agent-fox")); err == nil {
				t.Error("a refused check created .agent-fox in the directory that is no repository")
			}
		})
	}
}

// TestNothingToMeasure_TS_06_40 verifies TS-06-40:
// nothing-to-measure cases return their outcome without modifying any file.
// Verifies: 06-REQ-6.3
func TestNothingToMeasure_TS_06_40(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T, dir string)
		kind    string
		outcome string
		paths   int
	}{
		{"A, no changes", func(*testing.T, string) {}, project.KindTest, OutcomeNoChanges, 0},
		{"B, only test files", func(t *testing.T, dir string) { writeFile(t, dir, "tests/new.txt", "t\n", 0o644) }, project.KindTest, OutcomeNoCodeChanges, 1},
		{"C, only code", func(t *testing.T, dir string) { writeFile(t, dir, "src/main.txt", "v2\n", 0o644) }, project.KindTest, OutcomeNoTestChanges, 1},
		{"D, no test check", func(t *testing.T, dir string) {
			writeFile(t, dir, "src/main.txt", "v2\n", 0o644)
			writeFile(t, dir, "tests/new.txt", "t\n", 0o644)
		}, project.KindLint, OutcomeInconclusive, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, base := newRepo(t, map[string]fileSpec{"src/main.txt": plain("v1\n")})
			tc.prepare(t, dir)
			before := snapshot(t, dir)
			counter, check := counterCheck(t, "the check", tc.kind)

			res := mustCheck(t, dir, base, Options{Table: tableOf(check)})

			if res.Outcome != tc.outcome || !res.Restored || res.Closes || res.Reason == "" {
				t.Errorf("outcome %q (reason %q), restored %v, closes %v; want %q, a reason, true and false", res.Outcome, res.Reason, res.Restored, res.Closes, tc.outcome)
			}
			if !hash40.MatchString(res.Since) || res.Since != base {
				t.Errorf("since = %q, want the full hash %s", res.Since, base)
			}
			if len(res.RevertedFiles) != 0 || res.Reverted != nil || res.After != "" {
				t.Errorf("reverted %v, reverted result %v, after %q; want nothing", res.RevertedFiles, res.Reverted, res.After)
			}
			if runs(t, counter) != 0 {
				t.Errorf("a check ran %d times, want none", runs(t, counter))
			}
			requireState(t, dir, before)
		})
	}
}

// TestRedTreeIsInconclusive_TS_06_41 verifies TS-06-41:
// a red current tree is inconclusive with reason after_not_green and nothing
// is reverted.
// Verifies: 06-REQ-6.4
func TestRedTreeIsInconclusive_TS_06_41(t *testing.T) {
	dir, base := newRepo(t, map[string]fileSpec{"src/main.txt": plain("v1\n")})
	writeFile(t, dir, "src/main.txt", "v2\n", 0o644)
	writeFile(t, dir, "tests/fix.txt", "t\n", 0o644)
	before := snapshot(t, dir)
	lint, lintCheck := counterCheck(t, "lint", project.KindLint)
	_ = lint

	res := mustCheck(t, dir, base, Options{Table: tableOf(shCheck("tests", project.KindTest, "echo red; exit 1"), lintCheck)})

	if res.Outcome != OutcomeInconclusive || res.Reason != "after_not_green" || res.Closes || !res.Restored {
		t.Errorf("outcome %q, reason %q, closes %v, restored %v", res.Outcome, res.Reason, res.Closes, res.Restored)
	}
	if _, err := os.Stat(filepath.Join(dir, ".agent-fox", "gate", res.After, "result.json")); err != nil || res.After == "" {
		t.Errorf("after = %q: %v; want the id of a result in the gate cache", res.After, err)
	}
	if len(res.RevertedFiles) != 0 || res.Reverted != nil {
		t.Errorf("reverted %v, %v; want nothing", res.RevertedFiles, res.Reverted)
	}
	if !slices.Equal(res.TestFiles, []string{"tests/fix.txt"}) {
		t.Errorf("test files = %v", res.TestFiles)
	}
	requireState(t, dir, before)
}

// TestRevertsToTheRefsContent_TS_06_42 verifies TS-06-42:
// non-test paths are reverted to the ref's content and mode without touching
// the index, added files are moved aside and test paths stay.
// Verifies: 06-REQ-6.5
func TestRevertsToTheRefsContent_TS_06_42(t *testing.T) {
	dir, base := newRepo(t, map[string]fileSpec{"lib.sh": {"v1\n", 0o755}})
	writeFile(t, dir, "lib.sh", "v2\n", 0o755)
	writeFile(t, dir, "new.go", "package x\n", 0o644)
	writeFile(t, dir, "fix_test.go", "package x\n", 0o644)
	before := snapshot(t, dir)
	obs := filepath.Join(t.TempDir(), "observation")
	observe := fmt.Sprintf(`{
echo "lib=$(cat lib.sh)"
echo "mode=$(ls -l lib.sh | cut -c1-10)"
if [ -e new.go ]; then echo new=1; else echo new=0; fi
if [ -e fix_test.go ]; then echo test=1; else echo test=0; fi
echo "backup=$(ls .agent-fox/revert/*/ 2>/dev/null | tr '\n' ' ')"
echo "staged=$(git --no-optional-locks diff --cached --name-only | tr '\n' ' ')"
} > '%s'`, obs)
	lintRuns, lint := counterCheck(t, "lint", project.KindLint)

	res := mustCheck(t, dir, base, Options{Table: tableOf(lint, shCheck("tests", project.KindTest, observe))})

	data, err := os.ReadFile(obs)
	if err != nil {
		t.Fatal(err)
	}
	seen := string(data)
	for _, want := range []string{"lib=v1", "mode=-rwxr-xr-x", "new=0", "test=1", "staged=\n"} {
		if !strings.Contains(seen, want) {
			t.Errorf("during the reverted run %q was not seen; observation:\n%s", want, seen)
		}
	}
	if m := regexp.MustCompile(`backup=(.*)\n`).FindStringSubmatch(seen); m == nil || !strings.Contains(m[1], "manifest.json") || !strings.Contains(m[1], "files") {
		t.Errorf("the backup held %q, want a manifest and the copies", seen)
	}
	if !slices.Equal(res.RevertedFiles, []string{"lib.sh", "new.go"}) || !slices.Equal(res.TestFiles, []string{"fix_test.go"}) {
		t.Errorf("reverted %v, test files %v; want [lib.sh new.go] and [fix_test.go]", res.RevertedFiles, res.TestFiles)
	}
	if runs(t, lintRuns) != 1 {
		t.Errorf("the lint check ran %d times, want once: only test and check checks run on the reverted tree", runs(t, lintRuns))
	}
	if res.Outcome != OutcomeNotProven || !res.Restored {
		t.Errorf("outcome %q, restored %v; want not_proven (the check passes whatever it sees) and true", res.Outcome, res.Restored)
	}
	requireState(t, dir, before)
	if got, _ := os.ReadFile(filepath.Join(dir, "lib.sh")); string(got) != "v2\n" {
		t.Errorf("lib.sh holds %q after the check", got)
	}
	if info, err := os.Stat(filepath.Join(dir, "lib.sh")); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("lib.sh: %v, %v; want mode 755", info, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.go")); err != nil {
		t.Errorf("new.go did not come back: %v", err)
	}
}

// TestRevertsDeletionsModesAndLinks guards 06-REQ-6.5 and 06-REQ-6.7: a file
// deleted from the tree is back during the run and gone after it, a mode and a
// link are the ref's during the run and the tree's after it, and a directory
// the revert had to make is not left behind.
func TestRevertsDeletionsModesAndLinks(t *testing.T) {
	dir, base := newRepo(t, map[string]fileSpec{
		"bin/tool":         {"#!/bin/sh\n", 0o755},
		"docs/readme.txt":  plain("read me\n"),
		"gone/inner/f.txt": plain("f\n"),
	})
	if err := os.Symlink("docs/readme.txt", filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}
	gitIn(t, dir, "add", "link")
	gitIn(t, dir, "commit", "-q", "-m", "chore: a link")
	base = strings.TrimSpace(gitIn(t, dir, "rev-parse", "HEAD"))

	writeFile(t, dir, "bin/tool", "#!/bin/sh\necho changed\n", 0o644) // content and mode
	if err := os.RemoveAll(filepath.Join(dir, "gone")); err != nil {  // a whole directory
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "link")); err != nil { // a link that became a file
		t.Fatal(err)
	}
	writeFile(t, dir, "link", "not a link any more\n", 0o644)
	writeFile(t, dir, "fresh/sub/x.go", "package x\n", 0o644)
	writeFile(t, dir, "tests/t.txt", "t\n", 0o644)
	mtime := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(dir, "docs/readme.txt"), mtime, mtime); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dir)
	obs := filepath.Join(t.TempDir(), "observation")
	observe := fmt.Sprintf(`{
echo "tool=$(ls -l bin/tool | cut -c1-10) $(cat bin/tool | tail -1)"
if [ -e gone/inner/f.txt ]; then echo gone=1; else echo gone=0; fi
if [ -L link ]; then echo "link=symlink $(readlink link)"; else echo link=file; fi
if [ -e fresh/sub/x.go ]; then echo fresh=1; else echo fresh=0; fi
} > '%s'`, obs)

	res := mustCheck(t, dir, base, Options{Table: tableOf(shCheck("tests", project.KindTest, observe))})

	data, _ := os.ReadFile(obs)
	for _, want := range []string{"tool=-rwxr-xr-x", "gone=1", "link=symlink docs/readme.txt", "fresh=0"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("during the reverted run %q was not seen; observation:\n%s", want, data)
		}
	}
	if res.Outcome != OutcomeNotProven || !res.Restored {
		t.Errorf("outcome %q, restored %v", res.Outcome, res.Restored)
	}
	requireState(t, dir, before)
	if _, err := os.Stat(filepath.Join(dir, "gone")); err == nil {
		t.Error("the directory the revert had to make was left behind")
	}
	if info, err := os.Lstat(filepath.Join(dir, "link")); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("link: %v, %v; want the file it was", info, err)
	}
	if info, err := os.Stat(filepath.Join(dir, "bin/tool")); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("bin/tool: %v, %v; want mode 644", info, err)
	}
	if info, err := os.Stat(filepath.Join(dir, "docs/readme.txt")); err != nil || !info.ModTime().Equal(mtime) {
		t.Errorf("a path the check did not change has the modification time %v, want %v", info.ModTime(), mtime)
	}
}

// provenFixture makes a repository whose change fixes lib.txt and adds a test
// that checks for the fix, and returns the repository, the base and the
// check. With vacuous the test passes whatever lib.txt holds.
func provenFixture(t *testing.T, vacuous bool) (dir, base string, check project.Check) {
	t.Helper()
	dir, base = newRepo(t, map[string]fileSpec{"lib.txt": plain("broken\n")})
	writeFile(t, dir, "lib.txt", "fixed\n", 0o644)
	test := "grep -q fixed lib.txt\n"
	if vacuous {
		test = "exit 0\n"
	}
	writeFile(t, dir, "tests/check.sh", test, 0o644)
	return dir, base, shCheck("tests", project.KindTest, "sh tests/check.sh")
}

// TestProvenAndNotProven_TS_06_43 verifies TS-06-43:
// proven and not_proven both come back with closes true only for proven and
// the full result shape.
// Verifies: 06-REQ-6.6
func TestProvenAndNotProven_TS_06_43(t *testing.T) {
	t.Run("proven", func(t *testing.T) {
		dir, base, check := provenFixture(t, false)
		before := snapshot(t, dir)

		res := mustCheck(t, dir, base, Options{Table: tableOf(check)})

		if res.Outcome != OutcomeProven || !res.Closes || res.Reason == "" {
			t.Errorf("outcome %q, closes %v, reason %q; want proven, true and a reason", res.Outcome, res.Closes, res.Reason)
		}
		if res.Reverted == nil || res.Reverted.Passed || len(res.Reverted.Checks) != 1 || res.Reverted.Checks[0].Status != checks.StatusFail || res.Reverted.ID == "" {
			t.Errorf("reverted = %+v, want one failing check", res.Reverted)
		}
		shape(t, res, base)
		requireState(t, dir, before)
	})

	t.Run("not proven", func(t *testing.T) {
		dir, base, check := provenFixture(t, true)
		before := snapshot(t, dir)

		res := mustCheck(t, dir, base, Options{Table: tableOf(check)})

		if res.Outcome != OutcomeNotProven || res.Closes {
			t.Errorf("outcome %q, closes %v; want not_proven and false", res.Outcome, res.Closes)
		}
		if res.Reverted == nil || !res.Reverted.Passed || res.Reverted.ID == res.After {
			t.Errorf("reverted = %+v, after %s; want a passing result of its own", res.Reverted, res.After)
		}
		shape(t, res, base)
		requireState(t, dir, before)
	})
}

// shape checks the parts of a result that every measured outcome has.
func shape(t *testing.T, res Result, base string) {
	t.Helper()
	if !hash40.MatchString(res.Since) || res.Since != base || !res.Restored || res.After == "" {
		t.Errorf("since %q, restored %v, after %q", res.Since, res.Restored, res.After)
	}
	if !slices.Equal(res.RevertedFiles, []string{"lib.txt"}) || !slices.Equal(res.TestFiles, []string{"tests/check.sh"}) {
		t.Errorf("reverted %v, test files %v", res.RevertedFiles, res.TestFiles)
	}
}

// TestRevertedErrorIsInconclusive guards 06-REQ-6.6: a reverted run in which
// the check could not start says nothing about the tests.
func TestRevertedErrorIsInconclusive(t *testing.T) {
	dir, base := newRepo(t, map[string]fileSpec{"run.sh": {"#!/bin/sh\nexit 0\n", 0o755}})
	writeFile(t, dir, "run.sh", "#!/bin/sh\nexit 0\n# changed\n", 0o755)
	writeFile(t, dir, "added.sh", "#!/bin/sh\nexit 0\n", 0o755)
	writeFile(t, dir, "tests/t.txt", "t\n", 0o644)
	before := snapshot(t, dir)
	runner := project.Check{Name: "added", Argv: []string{"./added.sh"}, Kind: project.KindTest, Available: true}

	res := mustCheck(t, dir, base, Options{Table: tableOf(runner)})

	if res.Outcome != OutcomeInconclusive || res.Closes || res.Reverted == nil || res.Reverted.Checks[0].Status != checks.StatusError {
		t.Errorf("outcome %q, closes %v, reverted %+v; want inconclusive: the check that failed to start proves nothing", res.Outcome, res.Closes, res.Reverted)
	}
	requireState(t, dir, before)
}

// TestEveryOutcomeGivesTheTreeBack_TS_06_44 verifies TS-06-44:
// every outcome leaves the tree hash, the index bytes and the status as they
// were, for repositories with random mixes of changes.
// Verifies: 06-REQ-6.7
func TestEveryOutcomeGivesTheTreeBack_TS_06_44(t *testing.T) {
	requireGitAndSh(t)
	rng := rand.New(rand.NewPCG(6, 44))
	measured := 0

	for i := range 16 {
		files := map[string]fileSpec{"src/s0.txt": plain("base\n")}
		for j := 1; j < 5; j++ {
			files[fmt.Sprintf("src/s%d.txt", j)] = fileSpec{fmt.Sprintf("source %d\n", j), []os.FileMode{0o644, 0o755}[rng.IntN(2)]}
		}
		for j := range 3 {
			files[fmt.Sprintf("tests/t%d.txt", j)] = plain(fmt.Sprintf("test %d\n", j))
			files[fmt.Sprintf("lib/l%d.sh", j)] = fileSpec{fmt.Sprintf("lib %d\n", j), 0o755}
		}
		dir, base := newRepo(t, files)

		if rng.IntN(5) > 0 {
			writeFile(t, dir, "src/s0.txt", "fixed\n", 0o644)
		}
		for name := range files {
			if name == "src/s0.txt" {
				continue
			}
			path := filepath.Join(dir, filepath.FromSlash(name))
			switch rng.IntN(7) {
			case 0:
				writeFile(t, dir, name, files[name].content+"changed\n", files[name].mode)
			case 1:
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case 2:
				renamed := name + ".renamed"
				if rng.IntN(2) == 0 {
					gitIn(t, dir, "mv", name, renamed)
				} else if err := os.Rename(path, filepath.Join(dir, filepath.FromSlash(renamed))); err != nil {
					t.Fatal(err)
				}
			case 3:
				if err := os.Chmod(path, files[name].mode^0o111); err != nil {
					t.Fatal(err)
				}
			}
		}
		for j := range rng.IntN(4) {
			writeFile(t, dir, fmt.Sprintf("%s/added%d.txt", []string{"src", "tests", "lib"}[rng.IntN(3)], j), "added\n", 0o644)
		}
		if rng.IntN(2) == 0 {
			gitIn(t, dir, "add", "-A", "src")
		}
		if rng.IntN(2) == 0 {
			gitIn(t, dir, "add", "-u")
		}
		before := snapshot(t, dir)

		res := mustCheck(t, dir, base, Options{Table: tableOf(shCheck("tests", project.KindTest, "grep -q fixed src/s0.txt"))})

		if !res.Restored {
			t.Errorf("#%d: restored is false", i)
		}
		if res.Outcome == OutcomeProven || res.Outcome == OutcomeNotProven {
			measured++
		}
		if t.Failed() {
			t.Fatalf("#%d (%s) broke the invariants above", i, res.Outcome)
		}
		requireState(t, dir, before)
		if t.Failed() {
			t.Fatalf("#%d (%s) did not give the tree back", i, res.Outcome)
		}
	}
	if measured < 6 {
		t.Errorf("only %d of 16 repositories were measured: the property did not exercise the revert", measured)
	}
}

// slowFixture is a proven-style repository whose check hangs on the reverted
// tree, and the files it reports through.
func slowFixture(t *testing.T) (dir, base string, check project.Check, started, pidFile string) {
	t.Helper()
	dir, base = newRepo(t, map[string]fileSpec{"lib.txt": plain("broken\n")})
	writeFile(t, dir, "lib.txt", "fixed\n", 0o644)
	writeFile(t, dir, "tests/t.txt", "t\n", 0o644)
	aside := t.TempDir()
	started, pidFile = filepath.Join(aside, "started"), filepath.Join(aside, "pid")
	script := fmt.Sprintf("if grep -q fixed lib.txt; then exit 0; fi; sleep 60 & echo $! > '%s'; touch '%s'; wait", pidFile, started)
	return dir, base, shCheck("tests", project.KindTest, script), started, pidFile
}

// requireGone fails the test if the process the file names is still there.
func requireGone(t *testing.T, pidFile string) {
	t.Helper()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the check never started its child: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("pid file = %q", data)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
	}
	t.Errorf("the child of the check (pid %d) is still running", pid)
}

// TestCancelledRunStillRestores_TS_06_45 verifies TS-06-45:
// a signal or timeout during the reverted run still restores the tree and the
// index.
// Verifies: 06-REQ-6.7
func TestCancelledRunStillRestores_TS_06_45(t *testing.T) {
	t.Run("a signal", func(t *testing.T) {
		dir, base, check, started, pidFile := slowFixture(t)
		before := snapshot(t, dir)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			for ctx.Err() == nil {
				if _, err := os.Stat(started); err == nil {
					cancel()
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()

		_, err := Check(ctx, dir, base, Options{Table: tableOf(check)})

		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Check = %v, want context.Canceled", err)
		}
		requireState(t, dir, before)
		requireGone(t, pidFile)
	})

	t.Run("a timeout", func(t *testing.T) {
		dir, base, check, _, pidFile := slowFixture(t)
		before := snapshot(t, dir)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		_, err := Check(ctx, dir, base, Options{Table: tableOf(check)})

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Check = %v, want context.DeadlineExceeded", err)
		}
		requireState(t, dir, before)
		requireGone(t, pidFile)
	})
}

// TestLeftoverBackup_TS_06_46 verifies TS-06-46:
// a leftover backup, or a restore that fails or ends with a different tree
// hash, is a restore error that keeps the backup.
// Verifies: 06-REQ-6.8
func TestLeftoverBackup_TS_06_46(t *testing.T) {
	t.Run("A, a backup from an earlier check", func(t *testing.T) {
		dir, base, check := provenFixture(t, false)
		old := filepath.Join(dir, ".agent-fox", "revert", "old")
		if err := os.MkdirAll(old, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(old, "manifest.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, dir)
		counter, counted := counterCheck(t, "counted", project.KindTest)
		_ = check

		_, err := Check(context.Background(), dir, base, Options{Table: tableOf(counted)})

		var restore *RestoreError
		if !errors.As(err, &restore) || restore.Dir != old || !strings.Contains(err.Error(), old) {
			t.Fatalf("Check = %v, want a restore error naming %s", err, old)
		}
		if runs(t, counter) != 0 {
			t.Error("a check ran although a backup was in the way")
		}
		if data, err := os.ReadFile(filepath.Join(old, "manifest.json")); err != nil || string(data) != "{}" {
			t.Errorf("the old backup was touched: %q, %v", data, err)
		}
		after := snapshot(t, dir)
		if after.tree != before.tree || !bytes.Equal(after.index, before.index) || after.status != before.status {
			t.Error("the tree changed although the check refused")
		}
	})

	t.Run("B, the copies are gone", func(t *testing.T) {
		dir, base, _ := provenFixture(t, false)
		script := `if grep -q fixed lib.txt; then exit 0; fi; rm -rf .agent-fox/revert/*/files; exit 1`

		_, err := Check(context.Background(), dir, base, Options{Table: tableOf(shCheck("tests", project.KindTest, script))})

		var restore *RestoreError
		if !errors.As(err, &restore) {
			t.Fatalf("Check = %v, want a restore error", err)
		}
		if _, statErr := os.Stat(filepath.Join(restore.Dir, "manifest.json")); statErr != nil {
			t.Errorf("the backup %s was not kept: %v", restore.Dir, statErr)
		}
		if !strings.HasPrefix(restore.Dir, filepath.Join(dir, ".agent-fox", "revert")) || !strings.Contains(err.Error(), restore.Dir) {
			t.Errorf("the error %q does not name the backup directory %s", err, restore.Dir)
		}
	})

	t.Run("C, the tree is another one", func(t *testing.T) {
		dir, base, _ := provenFixture(t, false)
		script := `if grep -q fixed lib.txt; then exit 0; fi; echo stray > stray.txt; exit 1`

		_, err := Check(context.Background(), dir, base, Options{Table: tableOf(shCheck("tests", project.KindTest, script))})

		var restore *RestoreError
		if !errors.As(err, &restore) || !strings.Contains(err.Error(), "tree hash") {
			t.Fatalf("Check = %v, want a restore error about the tree hash", err)
		}
		if _, statErr := os.Stat(filepath.Join(restore.Dir, "manifest.json")); statErr != nil {
			t.Errorf("the backup was not kept: %v", statErr)
		}
		if got, _ := os.ReadFile(filepath.Join(dir, "lib.txt")); string(got) != "fixed\n" {
			t.Errorf("lib.txt holds %q: the paths that were reverted are put back even though the tree is not the same", got)
		}
	})
}

// TestGitMissingIsAGitError guards 06-REQ-10.6: git that cannot be run is a
// *GitError, not a usage error.
func TestGitMissingIsAGitError(t *testing.T) {
	dir, base := newRepo(t, nil)
	t.Setenv("PATH", t.TempDir())

	_, err := Check(context.Background(), dir, base, Options{Table: tableOf(shCheck("t", project.KindTest, "exit 0"))})

	var gitErr *GitError
	if !errors.As(err, &gitErr) {
		t.Errorf("Check = %v, want a *GitError", err)
	}
}

// TestCopyIndexKeepsTheModificationTime guards the listing of changed paths:
// git tells an edit that leaves no trace in the stat data from none by the
// age of the index file, so the copy that git refreshes in place of the real
// index must be as old as the real one.
func TestCopyIndexKeepsTheModificationTime(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "index"), filepath.Join(dir, "copy")
	if err := os.WriteFile(src, []byte("index bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2021, 5, 6, 7, 8, 9, 123_000_000, time.UTC)
	if err := os.Chtimes(src, old, old); err != nil {
		t.Fatal(err)
	}

	if err := copyIndex(src, dst); err != nil {
		t.Fatal(err)
	}

	if got, _ := os.ReadFile(dst); string(got) != "index bytes" {
		t.Errorf("copy = %q", got)
	}
	if info, err := os.Stat(dst); err != nil || !info.ModTime().Equal(old) {
		t.Errorf("modification time = %v (%v), want %v", info.ModTime(), err, old)
	}
	if err := copyIndex(filepath.Join(dir, "missing"), filepath.Join(dir, "copy2")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("copying an index that is not there = %v, want it to say so", err)
	}
}
