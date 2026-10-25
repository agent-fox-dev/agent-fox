package conform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// A mistake in the arguments is refused before any file is touched.
func TestCheckRefusesBadArgumentsBeforeTouchingAnything(t *testing.T) {
	dir, _ := newRepo(t, map[string]string{"sub/a.txt": "a\n"})
	before := snapshot(t, dir) // makes .agent-fox, with its .gitignore
	notARepo := t.TempDir()
	for _, tc := range []struct{ name, dir, since, names string }{
		{"a subdirectory", filepath.Join(dir, "sub"), "HEAD", "--dir"},
		{"not a repository", notARepo, "HEAD", "--dir"},
		{"no --since", dir, "", "--since"},
		{"a ref that does not exist", dir, "nosuchref", "--since"},
		{"a ref that is not a commit", dir, "HEAD:sub/a.txt", "--since"},
		{"an option for a ref", dir, "--output=x", "--since"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Check(t.Context(), tc.dir, tc.since, Options{Table: tableOf(shCheck("t", project.KindTest, "exit 0"))})

			var usage *checks.UsageError
			if !errors.As(err, &usage) || !strings.Contains(err.Error(), tc.names) {
				t.Fatalf("Check = %v, want a usage error that names %s", err, tc.names)
			}
			requireState(t, dir, before)
			if entries, _ := os.ReadDir(filepath.Join(dir, ".agent-fox")); len(entries) != 1 {
				t.Errorf(".agent-fox holds %d entries, want only its .gitignore", len(entries))
			}
			if _, err := os.Stat(filepath.Join(notARepo, ".agent-fox")); err == nil {
				t.Error("a refused check made .agent-fox in the directory that is no repository")
			}
		})
	}
}

func TestCheckWithoutGitIsAGitError(t *testing.T) {
	dir, base := newRepo(t, nil)
	t.Setenv("PATH", t.TempDir())

	_, err := Check(t.Context(), dir, base, Options{Table: tableOf(shCheck("t", project.KindTest, "exit 0"))})

	var gitErr *GitError
	if !errors.As(err, &gitErr) || errors.Unwrap(err) == nil {
		t.Errorf("Check = %v, want a *GitError that wraps the cause", err)
	}
}

// Nothing changed, only tests changed, no test changed, or no check runs
// tests: the outcome says so, and no check runs.
func TestCheckWithNothingToMeasure(t *testing.T) {
	for _, tc := range []struct {
		name, kind, outcome, reason string
		files                       []string
	}{
		{"no changes", project.KindTest, OutcomeNoChanges, "no_paths_changed", nil},
		{"only tests", project.KindTest, OutcomeNoCodeChanges, "only_test_files_changed", []string{"tests/new.txt"}},
		{"only code", project.KindTest, OutcomeNoTestChanges, "no_test_files_changed", []string{"src/main.txt"}},
		{"no check that runs tests", project.KindLint, OutcomeInconclusive, "no_test_check", []string{"src/main.txt", "tests/new.txt"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, base := newRepo(t, map[string]string{"src/main.txt": "v1\n"})
			for _, f := range tc.files {
				testutil.WriteFile(t, dir, f, "v2\n")
			}
			before := snapshot(t, dir)
			counter, check := counterCheck(t, "the check", tc.kind)

			res := mustCheck(t, dir, base, Options{Table: tableOf(check)})

			if res.Outcome != tc.outcome || res.Reason != tc.reason || !res.Restored || res.Closes || res.Since != base {
				t.Errorf("result = %+v, want %s (%s), restored, since %s", res, tc.outcome, tc.reason, base)
			}
			if len(res.RevertedFiles) != 0 || res.Reverted != nil || res.After != "" || runs(t, counter) != 0 {
				t.Errorf("reverted %v and %v, after %q, %d runs; want nothing measured", res.RevertedFiles, res.Reverted, res.After, runs(t, counter))
			}
			requireState(t, dir, before)
		})
	}
}

// The tests must pass on the tree as it is before the revert can say anything.
func TestCheckOfATreeWhoseTestsDoNotPass(t *testing.T) {
	gone := project.Check{Name: "gone", Argv: []string{"no-such-program-for-af"}, Kind: project.KindTest}
	for _, tc := range []struct {
		name, reason string
		checks       []project.Check
	}{
		{"a test that fails", "after_not_green", []project.Check{shCheck("tests", project.KindTest, "exit 1"), shCheck("lint", project.KindLint, "exit 0")}},
		{"a test that cannot run", "test_check_unavailable", []project.Check{gone, shCheck("lint", project.KindLint, "exit 0")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, base, _ := fixed(t, "exit 0")
			before := snapshot(t, dir)

			res := mustCheck(t, dir, base, Options{Table: tableOf(tc.checks...)})

			if res.Outcome != OutcomeInconclusive || res.Reason != tc.reason || res.Closes || res.Reverted != nil || len(res.RevertedFiles) != 0 {
				t.Errorf("result = %+v, want inconclusive (%s) with nothing reverted", res, tc.reason)
			}
			if _, err := os.Stat(filepath.Join(dir, ".agent-fox", "gate", res.After, "result.json")); err != nil || res.After == "" {
				t.Errorf("after = %q (%v), want the id of a stored gate result", res.After, err)
			}
			if !slices.Equal(res.TestFiles, []string{"tests/check.sh"}) {
				t.Errorf("test files = %v", res.TestFiles)
			}
			requireState(t, dir, before)
		})
	}
}

// While the tests run, the paths that are not tests are as at the ref, in
// content, mode and kind, with the files the change added gone and the ones
// it deleted or renamed back; the tests, the index and the paths the change
// did not touch are as they were. Afterwards everything is as it was.
func TestCheckRevertsTheCodeAndKeepsTheTests(t *testing.T) {
	dir, _ := newRepo(t, map[string]string{
		"bin/tool": "#!/bin/sh\n", "lib.sh": "v1\n", "docs/readme.txt": "read me\n", "gone/inner/f.txt": "f\n",
		"old.txt": "old content, long enough to be a rename\n", "moved.txt": "moved content, long enough to be a rename\n", "staged.txt": "s1\n",
	})
	for _, name := range []string{"bin/tool", "lib.sh"} {
		writeFile(t, dir, name, testutil.ReadFile(t, filepath.Join(dir, name)), 0o755)
	}
	if err := os.Symlink("docs/readme.txt", filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}
	base := testutil.Commit(t, dir, "chore: modes and a link")

	writeFile(t, dir, "lib.sh", "v2\n", 0o755)
	writeFile(t, dir, "bin/tool", "#!/bin/sh\necho changed\n", 0o644)
	for _, p := range []string{"gone", "link"} {
		if err := os.RemoveAll(filepath.Join(dir, p)); err != nil {
			t.Fatal(err)
		}
	}
	testutil.WriteFile(t, dir, "link", "not a link any more\n")
	testutil.Git(t, dir, "mv", "old.txt", "renamed.txt")
	if err := os.Rename(filepath.Join(dir, "moved.txt"), filepath.Join(dir, "moved2.txt")); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, dir, "staged.txt", "s2\n")
	testutil.Git(t, dir, "add", "staged.txt")
	testutil.WriteFile(t, dir, "new.go", "package x\n")
	testutil.WriteFile(t, dir, "fresh/sub/x.go", "package x\n")
	testutil.WriteFile(t, dir, "fix_test.go", "package x\n")
	testutil.WriteFile(t, dir, "tests/t.txt", "t\n")
	mtime := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(dir, "docs/readme.txt"), mtime, mtime); err != nil {
		t.Fatal(err)
	}
	staged := testutil.Git(t, dir, "--no-optional-locks", "diff", "--cached", "--name-only")
	before := snapshot(t, dir)
	seen := filepath.Join(t.TempDir(), "seen")
	observe := fmt.Sprintf(`{
echo "lib=$(cat lib.sh) $(ls -l lib.sh | cut -c1-10)"
echo "tool=$(ls -l bin/tool | cut -c1-10) $(tail -1 bin/tool)"
if [ -L link ]; then echo "link=$(readlink link)"; else echo link=file; fi
for f in gone/inner/f.txt old.txt renamed.txt moved.txt moved2.txt new.go fresh/sub/x.go fix_test.go tests/t.txt; do [ -e "$f" ] && echo "has $f"; done
echo "staged=$(cat staged.txt)"
[ -s .agent-fox/revert.patch ] && echo patch
git --no-optional-locks diff --cached --name-only
} > '%s'`, seen)
	lintRuns, lint := counterCheck(t, "lint", project.KindLint)

	res := mustCheck(t, dir, base, Options{Table: tableOf(lint, shCheck("tests", project.KindCheck, observe))})

	want := "lib=v1 -rwxr-xr-x\ntool=-rwxr-xr-x #!/bin/sh\nlink=docs/readme.txt\n" +
		"has gone/inner/f.txt\nhas old.txt\nhas moved.txt\nhas fix_test.go\nhas tests/t.txt\nstaged=s1\npatch\n" + staged
	if got := testutil.ReadFile(t, seen); got != want {
		t.Errorf("during the run the tree was:\n%s\nwant:\n%s", got, want)
	}
	reverted := []string{"bin/tool", "fresh/sub/x.go", "gone/inner/f.txt", "lib.sh", "link", "moved.txt", "moved2.txt", "new.go", "old.txt", "renamed.txt", "staged.txt"}
	if !slices.Equal(res.RevertedFiles, reverted) || !slices.Equal(res.TestFiles, []string{"fix_test.go", "tests/t.txt"}) {
		t.Errorf("reverted %q, test files %q; want %q and the two tests", res.RevertedFiles, res.TestFiles, reverted)
	}
	if res.Outcome != OutcomeNotProven || runs(t, lintRuns) != 1 {
		t.Errorf("outcome %s, lint ran %d times; want not_proven and lint only on the tree as it is", res.Outcome, runs(t, lintRuns))
	}
	requireState(t, dir, before)
	if info, err := os.Stat(filepath.Join(dir, "docs/readme.txt")); err != nil || !info.ModTime().Equal(mtime) {
		t.Errorf("a path the change did not touch was written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "gone")); err == nil {
		t.Error("the directory the revert made is left behind")
	}
}

// What the tests do on the reverted tree is the outcome: a failure, or a
// deadline, proves the change; a pass does not; a check that cannot start
// there says nothing.
func TestCheckOutcomeIsWhatTheTestsDoWithoutTheChange(t *testing.T) {
	for _, tc := range []struct {
		name, test, outcome, reason, status string
		argv                                []string
	}{
		{"the tests fail", "grep -q fixed lib.txt", OutcomeProven, "tests_fail_without_the_change", checks.StatusFail, nil},
		{"the tests outlive their deadline", "grep -q fixed lib.txt || sleep 60", OutcomeProven, "tests_fail_without_the_change", checks.StatusTimeout, nil},
		{"the tests pass", "exit 0", OutcomeNotProven, "tests_pass_without_the_change", checks.StatusPass, nil},
		{"the check cannot start", "exit 0", OutcomeInconclusive, "reverted_check_error", checks.StatusError, []string{"./run.sh"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, base, check := fixed(t, tc.test)
			writeFile(t, dir, "run.sh", "#!/bin/sh\nexec sh tests/check.sh\n", 0o755) // added by the change
			if tc.argv != nil {
				check.Argv = tc.argv
			}
			before := snapshot(t, dir)

			res := mustCheck(t, dir, base, Options{Table: tableOf(check), CheckTimeout: time.Second})

			if res.Outcome != tc.outcome || res.Reason != tc.reason || res.Closes != (tc.outcome == OutcomeProven) || !res.Restored {
				t.Errorf("result = %+v, want %s (%s)", res, tc.outcome, tc.reason)
			}
			if r := res.Reverted; r == nil || len(r.Checks) != 1 || r.Checks[0].Status != tc.status || r.Passed != (tc.status == checks.StatusPass) || r.ID == res.After {
				t.Errorf("reverted = %+v, after %s; want a result of its own with one check that is %s", r, res.After, tc.status)
			}
			if res.Since != base || res.After == "" || !slices.Equal(res.RevertedFiles, []string{"lib.txt", "run.sh"}) || !slices.Equal(res.TestFiles, []string{"tests/check.sh"}) {
				t.Errorf("since %s, after %q, reverted %q, test files %q", res.Since, res.After, res.RevertedFiles, res.TestFiles)
			}
			requireState(t, dir, before)
		})
	}
}

// The table of checks found again on the reverted tree must be the one that
// was measured; when it is not, or cannot be found, nothing runs there.
func TestCheckIsInconclusiveWhenTheRevertChangesTheTable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		now     func(project.Check) (project.Table, error)
		changed bool
	}{
		{"another table", func(project.Check) (project.Table, error) { return tableOf(), nil }, true},
		{"a table that cannot be found", func(project.Check) (project.Table, error) { return project.Table{}, errors.New("no Makefile") }, true},
		{"the same table", func(c project.Check) (project.Table, error) { return tableOf(c), nil }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, base, _ := fixed(t, "exit 0")
			counter, counted := counterCheck(t, "counted", project.KindTest)
			before := snapshot(t, dir)

			res := mustCheck(t, dir, base, Options{Table: tableOf(counted), TableNow: func(context.Context) (project.Table, error) { return tc.now(counted) }})

			if got := res.Reason == "table_changed_by_revert"; got != tc.changed || (tc.changed && (res.Outcome != OutcomeInconclusive || res.Reverted != nil)) {
				t.Errorf("result = %+v; want the table changed: %v", res, tc.changed)
			}
			if want := map[bool]int{true: 1, false: 2}[tc.changed]; runs(t, counter) != want || !slices.Equal(res.RevertedFiles, []string{"lib.txt"}) {
				t.Errorf("the check ran %d times, reverted %q; want %d and lib.txt", runs(t, counter), res.RevertedFiles, want)
			}
			requireState(t, dir, before)
		})
	}
}

// A nested repository is no part of the change: it is not reverted.
func TestCheckLeavesANestedRepositoryAlone(t *testing.T) {
	dir, base, check := fixed(t, "grep -q fixed lib.txt")
	nested := filepath.Join(dir, "vendor", "nested")
	testutil.Git(t, testutil.Repo(t, map[string]string{"n.txt": "n\n"}), "clone", "-q", ".", nested)
	before := snapshot(t, dir)

	res := mustCheck(t, dir, base, Options{Table: tableOf(check)})

	if res.Outcome != OutcomeProven || !slices.Equal(res.RevertedFiles, []string{"lib.txt"}) {
		t.Errorf("result = %+v, want proven, with only lib.txt reverted", res)
	}
	requireState(t, dir, before)
}

// A tracked file under a directory that is a file now is back during the run,
// and the file is back after it.
func TestCheckOfADirectoryThatBecameAFile(t *testing.T) {
	dir, base := newRepo(t, map[string]string{"pkg/a.txt": "a\n"})
	if err := os.RemoveAll(filepath.Join(dir, "pkg")); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, dir, "pkg", "now a file\n")
	testutil.WriteFile(t, dir, "tests/t.txt", "t\n")
	before := snapshot(t, dir)

	res := mustCheck(t, dir, base, Options{Table: tableOf(shCheck("tests", project.KindTest, `! grep -q '^a$' pkg/a.txt 2>/dev/null`))})

	if res.Outcome != OutcomeProven || !slices.Equal(res.RevertedFiles, []string{"pkg", "pkg/a.txt"}) {
		t.Errorf("result = %+v, want proven, with pkg and pkg/a.txt reverted", res)
	}
	requireState(t, dir, before)
}

// What git writes to stderr is not read as what it printed.
func TestCheckIsNotMisledByWhatGitWritesToStderr(t *testing.T) {
	dir, base, check := fixed(t, "grep -q fixed lib.txt")
	before := snapshot(t, dir)
	t.Setenv("GIT_TRACE", "1")

	res, err := Check(t.Context(), dir, base, Options{Table: tableOf(check)})

	if err := os.Unsetenv("GIT_TRACE"); err != nil {
		t.Fatal(err)
	}
	if err != nil || res.Outcome != OutcomeProven {
		t.Errorf("Check = %+v, %v; want proven although git traces to stderr", res, err)
	}
	requireState(t, dir, before)
}
