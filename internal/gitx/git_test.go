package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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

// Hardcoding "main" is wrong on every repository that uses another default.
// With no remote, the branch checked out now is the right answer — and only
// before the feature branch is created, which is why a pipeline asks in
// pre-flight and keeps the answer.
func TestBaseBranchFallsBackToTheCurrentBranch(t *testing.T) {
	g, _ := newRepo(t)
	ctx := context.Background()

	if got := g.BaseBranch(ctx); got != "main" {
		t.Errorf("BaseBranch = %q", got)
	}
	if err := g.CreateBranch(ctx, "release/2.0"); err != nil {
		t.Fatal(err)
	}
	if got := g.BaseBranch(ctx); got != "release/2.0" {
		t.Errorf("BaseBranch after a checkout = %q; asked too late it names the feature branch", got)
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
