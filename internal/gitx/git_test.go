package gitx

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// newRepo builds a real, throwaway git repository. These tests drive the
// actual git binary because the wrapper's whole job is to get git's own
// behaviour right, and a fake git would only ever confirm the wrapper's
// assumptions about it.
func newRepo(t *testing.T) (*Git, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	ctx := context.Background()
	for _, argv := range [][]string{
		{"git", "init", "-q", "-b", "main"},
		{"git", "config", "user.email", "test@example.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "commit.gpgsign", "false"},
	} {
		if out, code, err := ExecRunner(ctx, dir, argv); err != nil || code != 0 {
			t.Fatalf("%v: %v (%d) %s", argv, err, code, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := New(dir, ExecRunner)
	g.SetSleep(func(time.Duration) {})
	if _, err := g.CommitAll(ctx, "chore: initial commit\n"); err != nil {
		t.Fatal(err)
	}
	return g, dir
}

func TestRepoBasics(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()

	if !g.IsRepo(ctx) {
		t.Fatal("IsRepo = false in a repository")
	}
	if New(t.TempDir(), ExecRunner).IsRepo(ctx) {
		t.Error("IsRepo should be false outside a repository")
	}
	if b, err := g.CurrentBranch(ctx); err != nil || b != "main" {
		t.Errorf("CurrentBranch = %q, %v", b, err)
	}
	if head, err := g.Head(ctx); err != nil || head == "" {
		t.Errorf("Head = %q, %v", head, err)
	}
	if dirty, err := g.DirtyFiles(ctx); err != nil || len(dirty) != 0 {
		t.Errorf("DirtyFiles on a clean tree = %v, %v", dirty, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err := g.DirtyFiles(ctx)
	if err != nil || len(dirty) != 1 {
		t.Fatalf("DirtyFiles after a write = %v, %v", dirty, err)
	}
	if !strings.Contains(dirty[0], "new.txt") {
		t.Errorf("dirty line = %q", dirty[0])
	}
}

// ChangedFiles must see an untracked file: the implementation phase often
// adds a test file, and a diff that ignores it would let a run report a fix
// that changed nothing.
func TestChangedFilesIncludesUntrackedFiles(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "added_test.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := g.ChangedFiles(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"README.md": true, "added_test.go": true}
	if len(got) != 2 {
		t.Fatalf("ChangedFiles = %v, want both files", got)
	}
	for _, f := range got {
		if !want[f] {
			t.Errorf("unexpected %q in %v", f, got)
		}
	}
}

// A commit message with a body and a trailer must survive intact, which is
// why it goes through stdin rather than as an argument.
func TestCommitAllKeepsAMultiParagraphMessage(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := "fix: stop double counting (#42)\n\nThe counter was incremented twice.\n\nCloses #42\n"
	commit, err := g.CommitAll(ctx, msg)
	if err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
	if commit == "" {
		t.Fatal("CommitAll returned no hash")
	}
	out, _, err := ExecRunner(ctx, dir, []string{"git", "log", "-1", "--format=%B"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"fix: stop double counting (#42)", "incremented twice", "Closes #42"} {
		if !strings.Contains(out, want) {
			t.Errorf("the commit message lost %q:\n%s", want, out)
		}
	}
}

func TestBranchLifecycle(t *testing.T) {
	g, _ := newRepo(t)
	ctx := context.Background()

	if g.LocalBranchExists(ctx, "fix/issue-1-x") {
		t.Fatal("a branch that was never created exists")
	}
	if err := g.CreateBranch(ctx, "fix/issue-1-x"); err != nil {
		t.Fatal(err)
	}
	if !g.LocalBranchExists(ctx, "fix/issue-1-x") {
		t.Error("the created branch is not reported")
	}
	if b, _ := g.CurrentBranch(ctx); b != "fix/issue-1-x" {
		t.Errorf("CurrentBranch = %q", b)
	}
	if err := g.Checkout(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	if err := g.DeleteBranch(ctx, "fix/issue-1-x"); err != nil {
		t.Fatal(err)
	}
	if g.LocalBranchExists(ctx, "fix/issue-1-x") {
		t.Error("the deleted branch is still reported")
	}
}

// Re-running on the same issue is normal — the first attempt was rejected in
// review — so a name in use yields a fresh one rather than a force-push.
func TestUniqueBranchNameAvoidsAnExistingBranch(t *testing.T) {
	g, _ := newRepo(t)
	ctx := context.Background()

	first := UniqueBranchName(ctx, g, "fix/issue-42-counter")
	if first != "fix/issue-42-counter" {
		t.Fatalf("first = %q", first)
	}
	if err := g.CreateBranch(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := g.Checkout(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	second := UniqueBranchName(ctx, g, "fix/issue-42-counter")
	if second != "fix/issue-42-counter-2" {
		t.Errorf("second = %q, want a suffixed name", second)
	}
}

// A run that continues a branch reads the earlier runs' commits: each with its
// whole message and its files, oldest first.
func TestCommitsSinceListsMessagesAndFiles(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	base, _ := g.Head(ctx)
	for i, name := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := g.CommitAll(ctx, fmt.Sprintf("feat: %s\n\nBody %d.\n\nSpec: 09_x, task %d\n", name, i, i+1)); err != nil {
			t.Fatal(err)
		}
	}
	commits, err := g.CommitsSince(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 || !slices.Equal(commits[0].Files, []string{"a.go"}) ||
		!strings.Contains(commits[0].Message, "Spec: 09_x, task 1") || !slices.Equal(commits[1].Files, []string{"b.go"}) {
		t.Errorf("commits = %+v", commits)
	}
}

func TestPushRetriesTransientFailuresOnly(t *testing.T) {
	ctx := context.Background()

	t.Run("retries then succeeds", func(t *testing.T) {
		var n int
		g := New("/tmp", func(context.Context, string, []string, ...string) (string, int, error) {
			n++
			if n < 3 {
				return "fatal: unable to access: Could not resolve proxy", 128, nil
			}
			return "", 0, nil
		})
		var logged int
		g.SetSleep(func(time.Duration) {})
		if err := g.Push(ctx, "b", 4, func(string) { logged++ }); err != nil {
			t.Fatalf("Push: %v", err)
		}
		if n != 3 {
			t.Errorf("attempts = %d, want 3", n)
		}
		if logged != 2 {
			t.Errorf("retries logged = %d, want 2", logged)
		}
	})

	t.Run("does not retry a bad credential", func(t *testing.T) {
		var n int
		g := New("/tmp", func(context.Context, string, []string, ...string) (string, int, error) {
			n++
			return "remote: Permission denied to user", 128, nil
		})
		g.SetSleep(func(time.Duration) { t.Error("a permission failure must not be retried") })
		if err := g.Push(ctx, "b", 4, nil); err == nil {
			t.Fatal("want an error")
		}
		if n != 1 {
			t.Errorf("attempts = %d, want exactly 1", n)
		}
	})
}

func TestSlug(t *testing.T) {
	cases := []struct{ in, want string }{
		{"session: token refresh skips expiry check", "session-token-refresh-skips-expiry"},
		{"Fix the bug", "issue"}, // all stop words: never the empty string
		{"", "issue"},
		{"Add support for UTF-8 in the parser!!", "support-utf-8-parser"},
		{strings.Repeat("verylongword-", 10), "verylongword-verylongword-verylongword"}, // cut to 40 at a hyphen
	}
	for _, c := range cases {
		if got := Slug(c.in); got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, in := range []string{"Fix the bug", "", "!!!"} {
		if got := Slug(in); got == "" {
			t.Errorf("Slug(%q) is empty; the branch name would end in a hyphen", in)
		}
	}
}

func TestBranchName(t *testing.T) {
	if got := BranchName("fix", 42, "counter double counts"); got != "fix/issue-42-counter-double-counts" {
		t.Errorf("BranchName = %q", got)
	}
	// With no issue — the input was text or a file — the number is left out
	// rather than written as zero.
	if got := BranchName("feature", 0, "add a widget cache"); got != "feature/widget-cache" {
		t.Errorf("BranchName without an issue = %q", got)
	}
}

func TestPull(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	ctx := context.Background()
	// Create an origin "remote" bare repo, and two clones: alice and bob.
	originDir := t.TempDir()
	for _, argv := range [][]string{
		{"git", "init", "--bare", "-b", "main", originDir},
	} {
		if out, code, err := ExecRunner(ctx, originDir, argv); err != nil || code != 0 {
			t.Fatalf("%v: %v (%d) %s", argv, err, code, out)
		}
	}

	aliceDir := t.TempDir()
	for _, argv := range [][]string{
		{"git", "clone", originDir, aliceDir},
		{"git", "config", "user.email", "alice@example.com"},
		{"git", "config", "user.name", "Alice"},
		{"git", "config", "commit.gpgsign", "false"},
	} {
		if out, code, err := ExecRunner(ctx, aliceDir, argv); err != nil || code != 0 {
			t.Fatalf("%v: %v (%d) %s", argv, err, code, out)
		}
	}

	bobDir := t.TempDir()
	for _, argv := range [][]string{
		{"git", "clone", originDir, bobDir},
		{"git", "config", "user.email", "bob@example.com"},
		{"git", "config", "user.name", "Bob"},
		{"git", "config", "commit.gpgsign", "false"},
	} {
		if out, code, err := ExecRunner(ctx, bobDir, argv); err != nil || code != 0 {
			t.Fatalf("%v: %v (%d) %s", argv, err, code, out)
		}
	}

	aliceGit := New(aliceDir, ExecRunner)
	bobGit := New(bobDir, ExecRunner)

	// Alice creates an initial commit on main and pushes it to origin.
	if err := os.WriteFile(filepath.Join(aliceDir, "file.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := aliceGit.CommitAll(ctx, "feat: initial commit\n"); err != nil {
		t.Fatal(err)
	}
	if err := aliceGit.Push(ctx, "main", 1, nil); err != nil {
		t.Fatal(err)
	}

	// Bob pulls main from origin.
	if err := bobGit.Pull(ctx, "main"); err != nil {
		t.Fatalf("bob Pull main: %v", err)
	}
	bobContent, err := os.ReadFile(filepath.Join(bobDir, "file.txt"))
	if err != nil || string(bobContent) != "hello\n" {
		t.Fatalf("bob file.txt = %q, %v", string(bobContent), err)
	}

	// Conflict case: Alice and Bob both edit file.txt differently and commit.
	if err := os.WriteFile(filepath.Join(aliceDir, "file.txt"), []byte("alice version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := aliceGit.CommitAll(ctx, "feat: alice change\n"); err != nil {
		t.Fatal(err)
	}
	if err := aliceGit.Push(ctx, "main", 1, nil); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(bobDir, "file.txt"), []byte("bob version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := bobGit.CommitAll(ctx, "feat: bob change\n"); err != nil {
		t.Fatal(err)
	}

	// Bob pulls and expects a merge conflict error.
	err = bobGit.Pull(ctx, "main")
	if err == nil {
		t.Fatal("expected conflict error when pulling incompatible changes, got nil")
	}
	if !strings.Contains(err.Error(), "git pull origin main") {
		t.Errorf("error = %v, expected git pull origin main context", err)
	}
}

func TestResetHardDiscardsAnAttempt(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	head, err := g.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "junk.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CommitAll(ctx, "wip: junk\n"); err != nil {
		t.Fatal(err)
	}
	if err := g.ResetHard(ctx, head); err != nil {
		t.Fatal(err)
	}
	if now, _ := g.Head(ctx); now != head {
		t.Errorf("Head = %q, want %q", now, head)
	}
}

func TestRunnerSeparatesExitCodesFromExecFailures(t *testing.T) {
	if _, err := exec.LookPath("false"); err != nil {
		t.Skip("false is not installed")
	}
	ctx := context.Background()
	if _, code, err := ExecRunner(ctx, t.TempDir(), []string{"false"}); err != nil || code == 0 {
		t.Errorf("a non-zero exit should be information: code=%d err=%v", code, err)
	}
	_, _, err := ExecRunner(ctx, t.TempDir(), []string{"definitely-not-a-program-xyz"})
	if err == nil {
		t.Error("a missing program should be an error, not an exit code")
	}
	if _, _, err := ExecRunner(ctx, t.TempDir(), nil); err == nil {
		t.Error("an empty command should be an error")
	}
}

func TestCleanRemovesUntrackedFiles(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(dir, "new", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new", "deep", "junk.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.Clean(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new")); !os.IsNotExist(err) {
		t.Error("the untracked directory survived Clean")
	}
	if dirty, _ := g.DirtyFiles(ctx); len(dirty) != 0 {
		t.Errorf("tree is dirty after Clean: %v", dirty)
	}
}

// Restore takes back one directory and leaves the rest of the tree alone:
// the edit outside it is what the next step commits, the edits inside it are
// the pipeline's to make.
func TestRestoreRevertsOnePathOnly(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(dir, "owned"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "owned", "state.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CommitAll(ctx, "chore: add state\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "owned", "state.json"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "owned", "extra.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.Restore(ctx, "owned"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "owned", "state.json")); string(b) != "{}" {
		t.Errorf("state.json = %q, want the committed content", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "owned", "extra.txt")); !os.IsNotExist(err) {
		t.Error("the untracked file under the restored path survived")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "README.md")); string(b) != "# changed\n" {
		t.Error("Restore touched a file outside the path it was given")
	}
}

func TestCommitAllNoVerifySkipsAFailingHook(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CommitAll(ctx, "chore: hooked\n"); err == nil {
		t.Fatal("the hook did not refuse the ordinary commit")
	}
	if _, err := g.CommitAllNoVerify(ctx, "wip: parked\n\nSpec: 09_x, task 1\n"); err != nil {
		t.Fatalf("CommitAllNoVerify: %v", err)
	}
	out, err := exec.Command("git", "-C", dir, "log", "-1", "--format=%B").Output()
	if err != nil {
		t.Fatal(err)
	}
	if msg := string(out); !strings.HasPrefix(msg, "wip: parked") || !strings.Contains(msg, "Spec: 09_x, task 1") {
		t.Errorf("the message of HEAD = %q", msg)
	}
}

func TestResetSoftKeepsTheWork(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	head, err := g.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "held.txt"), []byte("held"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CommitAllNoVerify(ctx, "wip: hold\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "loose.txt"), []byte("loose"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.ResetSoft(ctx, head); err != nil {
		t.Fatal(err)
	}
	if got, _ := g.Head(ctx); got != head {
		t.Errorf("HEAD = %s, want %s", got, head)
	}
	changed, err := g.ChangedFiles(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 2 {
		t.Errorf("ChangedFiles = %v, want the held and the loose file", changed)
	}
}

func TestUntrackedFilesSkipsTrackedAndIgnored(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	for name, body := range map[string]string{".gitignore": "ignored.log\n", "ignored.log": "x", "mid.go.txt": "x"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := g.UntrackedFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != ".gitignore,mid.go.txt" {
		t.Errorf("UntrackedFiles = %v", got)
	}
}

// TestRevParseGivesTheFullHash guards the hash that a fix run records as its base:
// 40 hex digits, of the commit a ref names, and an error for one that names none.
func TestRevParseGivesTheFullHash(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()

	full, err := g.RevParse(ctx, "HEAD")

	if err != nil || len(full) != 40 || strings.Trim(full, "0123456789abcdef") != "" {
		t.Fatalf("RevParse(HEAD) = %q, %v, want 40 hex digits", full, err)
	}
	short, _ := g.Head(ctx)
	if !strings.HasPrefix(full, short) {
		t.Errorf("RevParse = %s, and the short head is %s", full, short)
	}
	if byName, err := g.RevParse(ctx, "main"); err != nil || byName != full {
		t.Errorf("RevParse(main) = %q, %v, want %s", byName, err, full)
	}
	if err := os.WriteFile(filepath.Join(dir, "second.txt"), []byte("2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CommitAll(ctx, "chore: second\n"); err != nil {
		t.Fatal(err)
	}
	if parent, err := g.RevParse(ctx, "HEAD~1"); err != nil || parent != full {
		t.Errorf("RevParse(HEAD~1) = %q, %v, want the first commit, %s", parent, err, full)
	}
	if got, err := g.RevParse(ctx, "no-such-ref"); err == nil {
		t.Errorf("RevParse of a ref that names nothing = %q, want an error", got)
	}
}

// TestIgnoredNamesWhatTheRulesIgnore guards the paths a fix run does not copy to the
// host: the ignored ones, and not a tracked one that a rule matches.
func TestIgnoredNamesWhatTheRulesIgnore(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	for name, content := range map[string]string{
		".gitignore": "*.log\nbuild/\n", "a.go": "package a\n", "kept.log": "tracked\n", "x.log": "ignored\n", "build/out.bin": "bin\n", "docs/n.md": "n\n",
	} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, code, err := g.git(ctx, "add", "-f", ".gitignore", "kept.log"); err != nil || code != 0 {
		t.Fatalf("git add: %v %d %s", err, code, out)
	}
	if _, err := g.CommitAll(ctx, "chore: files\n"); err != nil {
		t.Fatal(err)
	}

	got, err := g.Ignored(ctx, []string{"a.go", "x.log", "build/out.bin", "kept.log", "docs/n.md", "sub/new.log"})

	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if want := []string{"build/out.bin", "sub/new.log", "x.log"}; !slices.Equal(got, want) {
		t.Errorf("Ignored = %v, want %v: the tracked kept.log is not ignored", got, want)
	}
	if none, err := g.Ignored(ctx, []string{"a.go", "docs/n.md"}); err != nil || len(none) != 0 {
		t.Errorf("Ignored of paths that none ignores = %v, %v, want none", none, err)
	}
	if none, err := g.Ignored(ctx, nil); err != nil || len(none) != 0 {
		t.Errorf("Ignored of no path = %v, %v", none, err)
	}
	if _, err := New(t.TempDir(), ExecRunner).Ignored(ctx, []string{"a"}); err == nil {
		t.Error("Ignored outside a repository succeeded")
	}
	if odd, err := g.Ignored(ctx, []string{"with space.log", "new\nline.log", "ünï.log"}); err != nil || len(odd) != 3 {
		t.Errorf("Ignored of odd names = %v, %v, want all three: no name is quoted", odd, err)
	}
}

// TestCreateBranchFromStartsAtTheRefAndCarriesTheWorkingTree guards the branch of a fix:
// it starts at the commit the run began at, whatever HEAD has moved to, and the change in
// the working tree comes along.
func TestCreateBranchFromStartsAtTheRefAndCarriesTheWorkingTree(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	base, err := g.RevParse(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CommitAll(ctx, "chore: a later commit"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("u\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := g.CreateBranchFrom(ctx, "fix/x", base); err != nil {
		t.Fatal(err)
	}

	if branch, _ := g.CurrentBranch(ctx); branch != "fix/x" {
		t.Errorf("branch = %q", branch)
	}
	if head, _ := g.RevParse(ctx, "HEAD"); head != base {
		t.Errorf("HEAD = %s, want the ref %s", head, base)
	}
	if _, err := os.Stat(filepath.Join(dir, "other.txt")); err == nil {
		t.Error("the later commit is on the branch")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "README.md")); string(data) != "# changed\n" {
		t.Errorf("README.md = %q, want the change carried along", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "untracked.txt")); err != nil {
		t.Errorf("the untracked file did not come along: %v", err)
	}
	if err := g.CreateBranchFrom(ctx, "fix/x", base); err == nil {
		t.Error("a branch that exists was made again")
	}
	if err := g.CreateBranchFrom(ctx, "fix/y", "no-such-ref"); err == nil {
		t.Error("a branch was made from a ref that is not one")
	}
	if err := g.CreateBranchFrom(ctx, "fix/z", "-bad"); err == nil {
		t.Error("a ref that is an option was taken")
	}
}

// TestIsAncestorTellsTheDescendantsFromTheRest guards the question a fix run asks of the
// checkout when a brain worked on it in place: is the commit it began at an ancestor of
// where the checkout is now. Exit code 1 of git is an answer, not an error.
func TestIsAncestorTellsTheDescendantsFromTheRest(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	base, err := g.RevParse(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"a.txt": "a\n", "b.txt": "b\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := g.CommitAll(ctx, "add "+name+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	tip, _ := g.RevParse(ctx, "HEAD")
	if out, code, err := ExecRunner(ctx, dir, []string{"git", "checkout", "-q", "-b", "side", base}); err != nil || code != 0 {
		t.Fatalf("%s %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CommitAll(ctx, "add c.txt\n"); err != nil {
		t.Fatal(err)
	}
	side, _ := g.RevParse(ctx, "HEAD")

	for _, tc := range []struct {
		name, a, b string
		want       bool
	}{
		{"the base of the tip", base, tip, true},
		{"the tip of itself", tip, tip, true},
		{"the tip of the base", tip, base, false},
		{"the other branch", side, tip, false},
		{"the tip of the other branch", tip, side, false},
	} {
		got, err := g.IsAncestor(ctx, tc.a, tc.b)
		if err != nil || got != tc.want {
			t.Errorf("%s: IsAncestor = %v, %v, want %v", tc.name, got, err, tc.want)
		}
	}
	if _, err := g.IsAncestor(ctx, "not-a-commit", tip); err == nil {
		t.Error("IsAncestor of a commit that is not there succeeded: only exit code 1 means no")
	}
}

func TestStashSetsTheChangesAsideAndPutsThemBack(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	if stashed, err := g.Stash(ctx); err != nil || stashed {
		t.Fatalf("Stash on a clean tree = %v, %v, want nothing set aside", stashed, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	stashed, err := g.Stash(ctx)

	if err != nil || !stashed {
		t.Fatalf("Stash = %v, %v, want the changes set aside", stashed, err)
	}
	if dirty, _ := g.DirtyFiles(ctx); len(dirty) != 0 {
		t.Errorf("the tree is %v after Stash, want it clean: the untracked file goes with the rest", dirty)
	}
	if err := g.StashPop(ctx); err != nil {
		t.Fatal(err)
	}
	readme, _ := os.ReadFile(filepath.Join(dir, "README.md"))
	added, _ := os.ReadFile(filepath.Join(dir, "new.txt"))
	if string(readme) != "# changed\n" || string(added) != "new" {
		t.Errorf("README.md is %q and new.txt %q after StashPop, want both changes back", readme, added)
	}
}
