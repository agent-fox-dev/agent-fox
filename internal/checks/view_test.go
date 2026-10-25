package checks

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// In a work tree the view is git's: tracked and untracked files that are not
// ignored, unquoted, less the deleted ones, with a nested repository as one
// entry, and never .git or .agent-fox.
func TestFileViewInAWorkTree(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, ".gitignore", "ignored.log\n", 0o644)
	writeFile(t, dir, "a b.txt", "x", 0o644)
	writeFile(t, dir, "deleted.txt", "x", 0o644)
	writeFile(t, dir, "pkg/a.go", "package a", 0o644)
	testutil.Commit(t, dir, "chore: files")
	if err := os.Remove(filepath.Join(dir, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	// A tracked file under a directory that is a file now is deleted too.
	if err := os.RemoveAll(filepath.Join(dir, "pkg")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "pkg", "now a file", 0o644)
	writeFile(t, dir, "é/ü.go", "package x", 0o644)
	writeFile(t, dir, "ignored.log", "x", 0o644)
	writeFile(t, dir, ".agent-fox/x", "x", 0o644)
	testutil.Git(t, testutil.Repo(t, nil), "clone", "-q", ".", filepath.Join(dir, "nested"))

	view, err := FileView(t.Context(), dir)

	if want := []string{".gitignore", "README.md", "a b.txt", "nested", "pkg", "é/ü.go"}; err != nil || !slices.Equal(view, want) {
		t.Errorf("FileView = %q, %v; want %q", view, err, want)
	}
}

// Anywhere else the view is a walk that leaves out the directories of tools
// and builds at any depth, .agent-fox at the root, and a .git that is a file,
// which points at a repository elsewhere.
func TestFileViewOutsideAWorkTree(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{".git", ".hg", ".svn", "node_modules", "vendor", "target", "dist", "build", "__pycache__", ".venv", "venv", ".agent-fox"} {
		writeFile(t, dir, name+"/f.txt", "x", 0o644)
	}
	for _, name := range []string{"src/vendor/x.go", "src/deep/node_modules/y.js", "sub/.git"} {
		writeFile(t, dir, name, "x", 0o644)
	}
	for _, name := range []string{"src/a.go", "vendored/a.go", "build.sh", "sub/.agent-fox/x", "sub/.gitkeep", "é x/ü "} {
		writeFile(t, dir, name, "x", 0o644)
	}

	view, err := FileView(t.Context(), dir)

	if want := []string{"build.sh", "src/a.go", "sub/.agent-fox/x", "sub/.gitkeep", "vendored/a.go", "é x/ü "}; err != nil || !slices.Equal(view, want) {
		t.Errorf("FileView = %q, %v; want %q", view, err, want)
	}
}

// A directory given as a link is the directory it links to.
func TestFileViewOfALinkToADirectory(t *testing.T) {
	real := t.TempDir()
	writeFile(t, real, "src/a.go", "package a", 0o644)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}

	view, err := FileView(t.Context(), link)

	if want := []string{"src/a.go"}; err != nil || !slices.Equal(view, want) {
		t.Errorf("FileView = %q, %v; want %q", view, err, want)
	}
	before := mustHash(t, link)
	writeFile(t, real, "src/a.go", "package changed", 0o644)
	if mustHash(t, link) == before {
		t.Error("the tree hash through the link does not see the content change")
	}
}

// Something that is not a directory is an error, not an empty tree.
func TestFileViewNeedsADirectory(t *testing.T) {
	file := testutil.WriteFile(t, t.TempDir(), "file.txt", "x")
	for name, dir := range map[string]string{"a file": file, "nothing": filepath.Join(t.TempDir(), "missing")} {
		if view, err := FileView(t.Context(), dir); err == nil {
			t.Errorf("%s: FileView = %q, want an error", name, view)
		}
		if _, err := TreeHash(t.Context(), dir); err == nil {
			t.Errorf("%s: TreeHash succeeded", name)
		}
	}
}
