package checks

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// gitIn runs git in dir with an identity of its own and none of the user's
// configuration, and returns what it printed.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{
		"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false",
	}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// newRepo makes a git repository with one commit, built the way gitx's
// newRepo builds it, and skips the test when git is missing.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	writeFile(t, dir, "README.md", "# x\n", 0o644)
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "chore: initial commit")
	return dir
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

// TestFileViewInAGitWorkTree_TS_06_1 verifies TS-06-1:
// In a git work tree the file view lists tracked and untracked-not-ignored
// files unquoted, drops ignored and deleted files, and is sorted.
// Verifies: 06-REQ-1.1, 06-REQ-1.3
func TestFileViewInAGitWorkTree_TS_06_1(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, ".gitignore", "ignored.log\n", 0o644)
	writeFile(t, dir, "a b.txt", "x", 0o644)
	writeFile(t, dir, "deleted.txt", "x", 0o644)
	writeFile(t, dir, "sub/.gitkeep", "", 0o644)
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "chore: files")
	if err := os.Remove(filepath.Join(dir, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "é/ü.go", "package x", 0o644)
	writeFile(t, dir, "ignored.log", "x", 0o644)
	writeFile(t, dir, ".agent-fox/x", "x", 0o644)

	view, err := FileView(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{".gitignore", "README.md", "a b.txt", "sub/.gitkeep", "é/ü.go"}
	if !slices.Equal(view, want) {
		t.Errorf("FileView = %q, want %q", view, want)
	}
}

// TestFileViewOutsideAGitWorkTree_TS_06_2 verifies TS-06-2:
// Outside a git work tree the file view is a standard-library walk that skips
// the fixed directory names.
// Verifies: 06-REQ-1.2, 06-REQ-1.3
func TestFileViewOutsideAGitWorkTree_TS_06_2(t *testing.T) {
	dir := t.TempDir()
	skipped := []string{".git", ".hg", ".svn", "node_modules", "vendor", "target", "dist", "build", "__pycache__", ".venv", "venv", ".agent-fox"}
	writeFile(t, dir, "src/a.go", "package a", 0o644)
	for _, name := range skipped {
		writeFile(t, dir, name+"/f.txt", "x", 0o644)
	}
	writeFile(t, dir, "src/vendor/x.go", "package x", 0o644)
	writeFile(t, dir, "src/deep/node_modules/y.js", "x", 0o644)

	view, err := FileView(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"src/a.go"}; !slices.Equal(view, want) {
		t.Errorf("FileView = %q, want %q", view, want)
	}
}

// TestFileViewKeepsWhatIsNotSkipped guards TS-06-2 in the other direction:
// names that merely contain a skipped name stay in the view, and so does a
// .agent-fox that is not at the root.
func TestFileViewKeepsWhatIsNotSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "vendored/a.go", "x", 0o644)
	writeFile(t, dir, "build.sh", "x", 0o755)
	writeFile(t, dir, "sub/.agent-fox/x", "x", 0o644)
	writeFile(t, dir, "sub/.gitkeep", "", 0o644)

	view, err := FileView(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"build.sh", "sub/.agent-fox/x", "sub/.gitkeep", "vendored/a.go"}; !slices.Equal(view, want) {
		t.Errorf("FileView = %q, want %q", view, want)
	}
}

// randomTree writes a random tree under dir, names with spaces and non-ASCII
// characters included, and returns its file names.
func randomTree(t *testing.T, rng *rand.Rand, dir string) []string {
	t.Helper()
	dirs := []string{"", "a/", "b c/", "é/", "a/ü x/"}
	suffixes := []string{"", " x", "é", ".go", " "}
	var names []string
	for i := range 3 + rng.IntN(8) {
		name := dirs[rng.IntN(len(dirs))] + fmt.Sprintf("f%d", i) + suffixes[rng.IntN(len(suffixes))]
		names = append(names, name)
		mode := os.FileMode(0o644)
		if rng.IntN(4) == 0 {
			mode = 0o755
		}
		writeFile(t, dir, name, fmt.Sprintf("content %d %d", i, rng.IntN(1000)), mode)
	}
	return names
}

// TestFileViewProperty_TS_06_3 verifies TS-06-3:
// Every file view is sorted, slash-separated, relative and free of .git/ and
// .agent-fox/ entries.
// Verifies: 06-REQ-1.3
func TestFileViewProperty_TS_06_3(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	rng := rand.New(rand.NewPCG(6, 3))
	for i := range 12 {
		for _, inGit := range []bool{false, true} {
			dir := t.TempDir()
			if inGit {
				gitIn(t, dir, "init", "-q", "-b", "main")
			}
			names := randomTree(t, rng, dir)
			writeFile(t, dir, ".agent-fox/y", "x", 0o644)

			view, err := FileView(context.Background(), dir)
			if err != nil {
				t.Fatal(err)
			}

			if !slices.IsSorted(view) || len(slices.Compact(slices.Clone(view))) != len(view) {
				t.Fatalf("#%d git=%v: the view is not sorted and unique: %q", i, inGit, view)
			}
			if len(view) != len(names) {
				t.Fatalf("#%d git=%v: the view has %d files, the tree %d: %q", i, inGit, len(view), len(names), view)
			}
			for _, p := range view {
				if strings.HasPrefix(p, "/") || strings.Contains(p, `\`) || strings.HasPrefix(p, ".git/") || strings.HasPrefix(p, ".agent-fox/") {
					t.Fatalf("#%d git=%v: %q should not be in the view", i, inGit, p)
				}
			}
		}
	}
}

// TestFileViewOfASymlinkedDirectory: a --dir that is a link to a directory is
// that directory; a walk does not follow a link at its root by itself.
func TestFileViewOfASymlinkedDirectory(t *testing.T) {
	real := t.TempDir()
	writeFile(t, real, "src/a.go", "package a", 0o644)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}

	view, err := FileView(context.Background(), link)
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"src/a.go"}; !slices.Equal(view, want) {
		t.Errorf("FileView = %q, want %q", view, want)
	}
	before := mustHash(t, link)
	writeFile(t, real, "src/a.go", "package changed", 0o644)
	if mustHash(t, link) == before {
		t.Error("the tree hash of a linked directory does not see its content change")
	}
}

// TestFileViewNeedsADirectory: something that is not a directory is an error
// and not an empty tree.
func TestFileViewNeedsADirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"a file": file, "nothing": filepath.Join(t.TempDir(), "missing")} {
		if view, err := FileView(context.Background(), dir); err == nil {
			t.Errorf("%s: FileView = %q, want an error", name, view)
		}
		if _, err := TreeHash(context.Background(), dir); err == nil {
			t.Errorf("%s: TreeHash succeeded", name)
		}
	}
}

// TestWalkViewLeavesOutGitFiles: a .git that is a file, which is how a linked
// worktree or a submodule points at its repository, is no part of the tree and
// holds a path of the machine.
func TestWalkViewLeavesOutGitFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package a", 0o644)
	writeFile(t, dir, ".git", "gitdir: /somewhere/on/this/machine\n", 0o644)
	writeFile(t, dir, "sub/.git", "gitdir: /elsewhere\n", 0o644)

	view, err := FileView(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"a.go"}; !slices.Equal(view, want) {
		t.Errorf("FileView = %q, want %q", view, want)
	}
}

// TestGitViewDropsWhatIsNoLongerThere: a tracked file under a directory that
// became a file is deleted, as one that is simply gone is.
func TestGitViewDropsWhatIsNoLongerThere(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "pkg/a.go", "package a", 0o644)
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "feat: a")
	if err := os.RemoveAll(filepath.Join(dir, "pkg")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "pkg", "now a file", 0o644)

	view, err := FileView(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"README.md", "pkg"}; !slices.Equal(view, want) {
		t.Errorf("FileView = %q, want %q: pkg/a.go is deleted", view, want)
	}
}
