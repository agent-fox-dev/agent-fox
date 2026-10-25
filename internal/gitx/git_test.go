package gitx

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestIsRepoAndTopLevel(t *testing.T) {
	g, dir := newRepo(t)
	testutil.WriteFile(t, dir, "sub/a.txt", "a")
	outside := New(t.TempDir(), nil)
	want, _ := filepath.EvalSymlinks(dir)

	top, err := New(filepath.Join(dir, "sub"), nil).TopLevel(t.Context())

	if !g.IsRepo(t.Context()) || outside.IsRepo(t.Context()) {
		t.Errorf("IsRepo is %v in a repository and %v outside one", g.IsRepo(t.Context()), outside.IsRepo(t.Context()))
	}
	if err != nil || top != want {
		t.Errorf("TopLevel from a subdirectory = %q, %v; want %q", top, err, want)
	}
	if _, err := outside.TopLevel(t.Context()); err == nil {
		t.Error("TopLevel outside a repository succeeded")
	}
}

func TestRevParseHeadAndCurrentBranch(t *testing.T) {
	g, dir := newRepo(t)
	ctx := t.Context()
	first, err := g.RevParse(ctx, "HEAD")
	if err != nil || len(first) != 40 || strings.Trim(first, "0123456789abcdef") != "" {
		t.Fatalf("RevParse(HEAD) = %q, %v; want 40 hex digits", first, err)
	}
	commit(t, g, map[string]string{"second.txt": "2"})

	short, _ := g.Head(ctx)
	byName, _ := g.RevParse(ctx, "main")
	parent, _ := g.RevParse(ctx, "HEAD~1")
	branch, _ := g.CurrentBranch(ctx)

	if !strings.HasPrefix(byName, short) || len(short) >= 40 || parent != first || branch != "main" {
		t.Errorf("Head %q, RevParse(main) %q, RevParse(HEAD~1) %q, CurrentBranch %q", short, byName, parent, branch)
	}
	if got, err := g.RevParse(ctx, "no-such-ref"); err == nil {
		t.Errorf("RevParse of a ref that names nothing = %q, want an error", got)
	}
	testutil.Git(t, dir, "checkout", "-q", "--detach")
	if b, err := g.CurrentBranch(ctx); err != nil || b != "HEAD" {
		t.Errorf("CurrentBranch when detached = %q, %v; want HEAD", b, err)
	}
}

func TestDirtyUntrackedAndChangedFiles(t *testing.T) {
	g, dir := newRepo(t)
	ctx := t.Context()
	if dirty, err := g.DirtyFiles(ctx); err != nil || len(dirty) != 0 {
		t.Errorf("DirtyFiles of a clean tree = %v, %v", dirty, err)
	}
	commit(t, g, map[string]string{".gitignore": "*.log\n"})
	write(t, dir, map[string]string{"README.md": "# changed\n", "added_test.go": "package x\n", "x.log": "ignored"})

	dirty, dirtyErr := g.DirtyFiles(ctx)
	untracked, untrackedErr := g.UntrackedFiles(ctx)
	changed, changedErr := g.ChangedFiles(ctx, "HEAD")

	if dirtyErr != nil || !slices.Equal(dirty, []string{"M README.md", "?? added_test.go"}) {
		t.Errorf("DirtyFiles = %q, %v", dirty, dirtyErr)
	}
	if untrackedErr != nil || !slices.Equal(untracked, []string{"added_test.go"}) {
		t.Errorf("UntrackedFiles = %q, %v; want the new file, and not the ignored one", untracked, untrackedErr)
	}
	// An untracked file is a change: a fix that only added a test changed something.
	if changedErr != nil || !slices.Equal(changed, []string{"README.md", "added_test.go"}) {
		t.Errorf("ChangedFiles = %q, %v", changed, changedErr)
	}
	outside := New(t.TempDir(), nil)
	if _, err := outside.DirtyFiles(ctx); err == nil {
		t.Error("DirtyFiles outside a repository succeeded")
	}
	if _, err := outside.UntrackedFiles(ctx); err == nil {
		t.Error("UntrackedFiles outside a repository succeeded")
	}
	if _, err := g.ChangedFiles(ctx, "no-such-ref"); err == nil {
		t.Error("ChangedFiles since a ref that is not one succeeded")
	}
}

// The message goes through stdin, so a body and a trailer survive and nothing
// in it is read as a flag.
func TestCommitAllKeepsTheWholeMessage(t *testing.T) {
	g, dir := newRepo(t)
	testutil.WriteFile(t, dir, "a.txt", "x")
	msg := "fix: stop double counting (#42)\n\n--amend is not a flag here.\n\nCloses #42\n"

	short, err := g.CommitAll(t.Context(), msg)

	if err != nil {
		t.Fatal(err)
	}
	if head, _ := g.Head(t.Context()); short != head {
		t.Errorf("CommitAll = %q, want the short hash of HEAD, %s", short, head)
	}
	if got := testutil.Git(t, dir, "log", "-1", "--format=%B"); strings.TrimSpace(got) != strings.TrimSpace(msg) {
		t.Errorf("the message of HEAD = %q, want %q", got, msg)
	}
	if _, err := g.CommitAll(t.Context(), "chore: nothing\n"); err == nil {
		t.Error("a commit of a clean tree succeeded")
	}
}

// The park of unverified work commits past a hook that would refuse it.
func TestCommitAllNoVerifySkipsTheHooks(t *testing.T) {
	g, dir := newRepo(t)
	hook := testutil.WriteFile(t, dir, ".git/hooks/pre-commit", "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, dir, "a.txt", "a")

	if _, err := g.CommitAll(t.Context(), "chore: hooked\n"); err == nil {
		t.Fatal("the hook did not refuse the ordinary commit")
	}
	if _, err := g.CommitAllNoVerify(t.Context(), "wip: parked\n"); err != nil {
		t.Fatalf("CommitAllNoVerify: %v", err)
	}
	if msg := testutil.Git(t, dir, "log", "-1", "--format=%s"); msg != "wip: parked\n" {
		t.Errorf("the message of HEAD = %q", msg)
	}
}

func TestStageAllExceptLeavesTheNamedPathsUnstaged(t *testing.T) {
	g, dir := newRepo(t)
	commit(t, g, map[string]string{"old.txt": "old"})
	if err := os.Remove(filepath.Join(dir, "old.txt")); err != nil {
		t.Fatal(err)
	}
	// The paths are literal: a * in a name is not a pattern.
	write(t, dir, map[string]string{"README.md": "# changed\n", "new.txt": "n", "a*.txt": "left", "ab.txt": "staged"})

	err := g.StageAllExcept(t.Context(), []string{"a*.txt"})

	staged := strings.Fields(testutil.Git(t, dir, "diff", "--cached", "--name-only"))
	if want := []string{"README.md", "ab.txt", "new.txt", "old.txt"}; err != nil || !slices.Equal(staged, want) {
		t.Errorf("staged %q, %v; want %q", staged, err, want)
	}
}

func TestBranches(t *testing.T) {
	g, _ := newRepo(t)
	ctx := t.Context()
	if g.LocalBranchExists(ctx, "fix/issue-1-x") {
		t.Fatal("a branch that was never made exists")
	}

	if err := g.CreateBranch(ctx, "fix/issue-1-x"); err != nil {
		t.Fatal(err)
	}
	branch, _ := g.CurrentBranch(ctx)
	exists := g.LocalBranchExists(ctx, "fix/issue-1-x")
	again := g.CreateBranch(ctx, "fix/issue-1-x")
	back := g.Checkout(ctx, "main")
	deleted := g.DeleteBranch(ctx, "fix/issue-1-x")

	if branch != "fix/issue-1-x" || !exists || again == nil || back != nil || deleted != nil {
		t.Errorf("on %q, exists %v, made again: %v, back to main: %v, deleted: %v", branch, exists, again, back, deleted)
	}
	if g.LocalBranchExists(ctx, "fix/issue-1-x") {
		t.Error("the deleted branch is still there")
	}
	if err := g.Checkout(ctx, "no-such-branch"); err == nil {
		t.Error("a checkout of a branch that is not there succeeded")
	}
}

// The branch of a fix starts at the commit the run began at, whatever HEAD has
// moved to, and the change in the working tree comes along.
func TestCreateBranchFromStartsAtTheRefWithTheWorkingTree(t *testing.T) {
	g, dir := newRepo(t)
	ctx := t.Context()
	base, _ := g.RevParse(ctx, "HEAD")
	commit(t, g, map[string]string{"later.txt": "later\n"})
	write(t, dir, map[string]string{"README.md": "# changed\n", "untracked.txt": "u\n"})

	if err := g.CreateBranchFrom(ctx, "fix/x", base); err != nil {
		t.Fatal(err)
	}

	branch, _ := g.CurrentBranch(ctx)
	head, _ := g.RevParse(ctx, "HEAD")
	files := testutil.Files(t, dir, ".git")
	if branch != "fix/x" || head != base {
		t.Errorf("on %q at %s, want fix/x at %s", branch, head, base)
	}
	if _, later := files["later.txt"]; later || files["README.md"] != "# changed\n" || files["untracked.txt"] != "u\n" {
		t.Errorf("the tree is %v, want the base's with the changes carried along", files)
	}
	for _, tc := range []struct{ name, ref string }{{"fix/x", base}, {"fix/y", "no-such-ref"}, {"fix/z", "-bad"}, {"-z", base}} {
		if err := g.CreateBranchFrom(ctx, tc.name, tc.ref); err == nil {
			t.Errorf("CreateBranchFrom(%q, %q) succeeded", tc.name, tc.ref)
		}
	}
}

// Exit code 1 of git merge-base is an answer, not an error.
func TestIsAncestor(t *testing.T) {
	g, dir := newRepo(t)
	ctx := t.Context()
	base, _ := g.RevParse(ctx, "HEAD")
	tip := commit(t, g, map[string]string{"a.txt": "a\n"})
	testutil.Git(t, dir, "checkout", "-q", "-b", "side", base)
	side := commit(t, g, map[string]string{"c.txt": "c\n"})

	for _, tc := range []struct {
		name, a, b string
		want       bool
	}{
		{"the base of the tip", base, tip, true},
		{"the tip of itself", tip, tip, true},
		{"the tip of the base", tip, base, false},
		{"another branch", side, tip, false},
	} {
		if got, err := g.IsAncestor(ctx, tc.a, tc.b); err != nil || got != tc.want {
			t.Errorf("%s: IsAncestor = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
	if _, err := g.IsAncestor(ctx, "not-a-commit", tip); err == nil {
		t.Error("IsAncestor of a commit that is not there succeeded")
	}
}

// A run that continues a branch reads the earlier runs' commits, oldest first,
// each with its whole message and its files.
func TestCommitsSince(t *testing.T) {
	g, _ := newRepo(t)
	ctx := t.Context()
	base, _ := g.RevParse(ctx, "HEAD")
	for _, name := range []string{"a.go", "b.go"} {
		testutil.WriteFile(t, g.Dir, name, "package x\n")
		if _, err := g.CommitAll(ctx, "feat: "+name+"\n\nBody.\n\nTrailer: "+name+"\n"); err != nil {
			t.Fatal(err)
		}
	}

	commits, err := g.CommitsSince(ctx, base)

	if err != nil || len(commits) != 2 {
		t.Fatalf("CommitsSince = %+v, %v", commits, err)
	}
	for i, name := range []string{"a.go", "b.go"} {
		c := commits[i]
		if len(c.Hash) != 40 || !slices.Equal(c.Files, []string{name}) || c.Message != "feat: "+name+"\n\nBody.\n\nTrailer: "+name {
			t.Errorf("commit %d = %+v", i, c)
		}
	}
	if _, err := g.CommitsSince(ctx, "no-such-ref"); err == nil {
		t.Error("CommitsSince a ref that is not one succeeded")
	}
}

// Each way of throwing work away, from one state: a commit since the base,
// an edit on top of it and a new file in a new directory.
func TestDiscardingWork(t *testing.T) {
	edited := map[string]string{"README.md": "# edited\n", "held.txt": "held", "new/deep/junk.txt": "junk"}
	for _, tc := range []struct {
		name    string
		discard func(ctx context.Context, g *Git, base string) error
		want    map[string]string
		atBase  bool
		staged  []string
	}{
		{"ResetHard goes back to the base and leaves the untracked file", func(ctx context.Context, g *Git, base string) error { return g.ResetHard(ctx, base) },
			map[string]string{"README.md": "# test\n", "new/deep/junk.txt": "junk"}, true, nil},
		{"ResetSoft keeps the commit's work, staged", func(ctx context.Context, g *Git, base string) error { return g.ResetSoft(ctx, base) },
			edited, true, []string{"README.md", "held.txt"}},
		{"Clean removes the untracked files and directories", func(ctx context.Context, g *Git, _ string) error { return g.Clean(ctx) },
			map[string]string{"README.md": "# edited\n", "held.txt": "held"}, false, nil},
		{"CheckoutPaths sets a path to the ref", func(ctx context.Context, g *Git, base string) error { return g.CheckoutPaths(ctx, base, "README.md") },
			map[string]string{"README.md": "# test\n", "held.txt": "held", "new/deep/junk.txt": "junk"}, false, []string{"README.md"}},
		{"CheckoutPaths of no path does nothing", func(ctx context.Context, g *Git, base string) error { return g.CheckoutPaths(ctx, base) },
			edited, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, dir := newRepo(t)
			base, _ := g.RevParse(t.Context(), "HEAD")
			tip := commit(t, g, map[string]string{"held.txt": "held", "README.md": "# tip\n"})
			write(t, dir, map[string]string{"README.md": "# edited\n", "new/deep/junk.txt": "junk"})

			if err := tc.discard(t.Context(), g, base); err != nil {
				t.Fatal(err)
			}

			if got := testutil.Files(t, dir, ".git"); !maps.Equal(got, tc.want) {
				t.Errorf("files = %v, want %v", got, tc.want)
			}
			want := tip
			if tc.atBase {
				want = base
			}
			if head, _ := g.RevParse(t.Context(), "HEAD"); head != want {
				t.Errorf("HEAD = %s, want %s", head, want)
			}
			if staged := strings.Fields(testutil.Git(t, dir, "diff", "--cached", "--name-only")); !slices.Equal(staged, tc.staged) {
				t.Errorf("staged = %q, want %q", staged, tc.staged)
			}
		})
	}
	if err := New(t.TempDir(), nil).ResetHard(t.Context(), "HEAD"); err == nil {
		t.Error("ResetHard outside a repository succeeded")
	}
}

// Restore takes back one directory a pipeline owns, its untracked files
// included, and leaves the rest of the tree alone.
func TestRestoreRevertsOnePathOnly(t *testing.T) {
	g, dir := newRepo(t)
	commit(t, g, map[string]string{"owned/state.json": "{}"})
	write(t, dir, map[string]string{"owned/state.json": "edited", "owned/extra.txt": "x", "README.md": "# changed\n"})

	if err := g.Restore(t.Context(), "owned"); err != nil {
		t.Fatal(err)
	}

	if got, want := testutil.Files(t, dir, ".git"), map[string]string{"owned/state.json": "{}", "README.md": "# changed\n"}; !maps.Equal(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}
	if err := g.Restore(t.Context(), "no-such-path"); err == nil {
		t.Error("Restore of a path git does not know succeeded")
	}
}

func TestShowFileIsTheContentAtARef(t *testing.T) {
	g, _ := newRepo(t)
	base, _ := g.RevParse(t.Context(), "HEAD")
	commit(t, g, map[string]string{"README.md": "# later\n\n", "new.txt": "n"})

	for _, tc := range []struct {
		ref, path, want string
		exists          bool
	}{
		{base, "README.md", "# test\n", true}, // byte for byte, the last newline kept
		{"HEAD", "README.md", "# later\n\n", true},
		{base, "new.txt", "", false},
		{"HEAD", "no/such.txt", "", false},
	} {
		got, exists, err := g.ShowFile(t.Context(), tc.ref, tc.path)
		if err != nil || got != tc.want || exists != tc.exists {
			t.Errorf("ShowFile(%s, %s) = %q, %v, %v; want %q, %v", tc.ref, tc.path, got, exists, err, tc.want, tc.exists)
		}
	}
}

func TestStashSetsTheChangesAsideAndPutsThemBack(t *testing.T) {
	g, dir := newRepo(t)
	ctx := t.Context()
	if stashed, err := g.Stash(ctx); err != nil || stashed {
		t.Fatalf("Stash of a clean tree = %v, %v; want nothing set aside", stashed, err)
	}
	write(t, dir, map[string]string{"README.md": "# changed\n", "new.txt": "new"})

	stashed, err := g.Stash(ctx)

	if err != nil || !stashed {
		t.Fatalf("Stash = %v, %v; want the changes set aside", stashed, err)
	}
	if dirty, _ := g.DirtyFiles(ctx); len(dirty) != 0 {
		t.Errorf("the tree is %v after Stash, want it clean: the untracked file goes too", dirty)
	}
	if err := g.StashPop(ctx); err != nil {
		t.Fatal(err)
	}
	if files := testutil.Files(t, dir, ".git"); files["README.md"] != "# changed\n" || files["new.txt"] != "new" {
		t.Errorf("after StashPop the tree is %v, want both changes back", files)
	}
	if _, err := New(t.TempDir(), nil).Stash(ctx); err == nil {
		t.Error("Stash outside a repository succeeded")
	}
}
