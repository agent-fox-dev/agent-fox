package gitx

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// write creates a file under dir with the given content.
func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestListFilesIsNULSeparatedAndByteExact verifies the listing behind
// TS-06-1: names with spaces, a leading and a trailing space, and non-ASCII
// characters come back as they are, unquoted and untrimmed; ignored files are
// absent; the list is sorted and has no duplicates.
// Verifies: 06-REQ-1.1
func TestListFilesIsNULSeparatedAndByteExact(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	write(t, dir, ".gitignore", "ignored.log\n")
	write(t, dir, "a b.txt", "x")
	write(t, dir, " lead.txt", "x")
	if _, err := g.CommitAll(ctx, "chore: files\n"); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "é/ü.go", "x")
	write(t, dir, "trail .txt ", "x")
	write(t, dir, "ignored.log", "x")

	got, err := g.ListFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{" lead.txt", ".gitignore", "README.md", "a b.txt", "trail .txt ", "é/ü.go"}
	if !slices.Equal(got, want) {
		t.Errorf("ListFiles = %q, want %q", got, want)
	}
}

// TestChangedPaths_TS_06_8 verifies TS-06-8:
// ChangedPaths returns diff-from-ref plus untracked paths NUL-separated,
// byte-exact, de-duplicated and sorted.
// Verifies: 06-REQ-1.8
func TestChangedPaths_TS_06_8(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	write(t, dir, ".gitignore", "ignored.log\n")
	write(t, dir, " lead.txt", "before")
	base, err := g.CommitAll(ctx, "chore: base\n")
	if err != nil {
		t.Fatal(err)
	}

	write(t, dir, " lead.txt", "after")
	write(t, dir, "é x.go", "package x")
	if _, err := g.CommitAll(ctx, "feat: a file\n"); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "trail .txt ", "x")
	write(t, dir, "ignored.log", "x")

	got, err := g.ChangedPaths(ctx, base)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{" lead.txt", "trail .txt ", "é x.go"}
	if !slices.Equal(got, want) {
		t.Errorf("ChangedPaths = %q, want %q", got, want)
	}
}

// TestChangedPathsSeesBothSidesOfARename guards the rule behind TS-06-8: a
// renamed file is two changed paths, the old one and the new one, so that a
// revert puts the old one back.
func TestChangedPathsSeesBothSidesOfARename(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	write(t, dir, "old.go", "package a // a file long enough to be recognised as the same file after a rename\n")
	base, err := g.CommitAll(ctx, "chore: base\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "old.go"), filepath.Join(dir, "new.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CommitAll(ctx, "refactor: rename\n"); err != nil {
		t.Fatal(err)
	}

	got, err := g.ChangedPaths(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"new.go", "old.go"}; !slices.Equal(got, want) {
		t.Errorf("ChangedPaths = %q, want %q", got, want)
	}
}

// TestListingsOutsideARepositoryFail guards the two listings: a directory
// that is no work tree is an error, not an empty list.
func TestListingsOutsideARepositoryFail(t *testing.T) {
	g := New(t.TempDir(), ExecRunner)
	if _, err := g.ListFiles(context.Background()); err == nil {
		t.Error("ListFiles outside a repository succeeded")
	}
	if _, err := g.ChangedPaths(context.Background(), "HEAD"); err == nil {
		t.Error("ChangedPaths outside a repository succeeded")
	}
}

// TestChangedPathsOnAPrivateIndexLeavesTheRealOne guards the way revert-check
// lists what changed: git diff refreshes the stat data of the index and
// writes it back, so the listing runs against a copy of the index and the
// real one stays byte for byte as it was.
func TestChangedPathsOnAPrivateIndexLeavesTheRealOne(t *testing.T) {
	g, dir := newRepo(t)
	write(t, dir, "f.txt", "same\n")
	if _, err := g.CommitAll(context.Background(), "feat: f\n"); err != nil {
		t.Fatal(err)
	}
	// The content is the same and the stat data is not: what a refresh updates.
	later := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "f.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(dir, ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "index")
	if err := os.WriteFile(copyPath, before, 0o600); err != nil {
		t.Fatal(err)
	}

	paths, err := New(dir, EnvRunner("GIT_INDEX_FILE="+copyPath)).ChangedPaths(context.Background(), "HEAD")

	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Errorf("ChangedPaths = %q, want none: only the stat data of f.txt changed", paths)
	}
	if after, _ := os.ReadFile(index); !bytes.Equal(after, before) {
		t.Error("the real index changed although git was pointed at a copy")
	}
}

// TestListingsAreNotSpoiledByGitWarnings: git can write a warning to stderr
// and exit 0 (an untracked directory it may not open), and the warning must
// not become part of a name, nor hide the name it was written in front of.
func TestListingsAreNotSpoiledByGitWarnings(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions do not stop root")
	}
	g, dir := newRepo(t)
	write(t, dir, "a.go", "package a")
	write(t, dir, "private/secret.go", "package p")
	if err := os.Chmod(filepath.Join(dir, "private"), 0o000); err != nil {
		t.Skipf("cannot make a directory unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "private"), 0o755) })

	files, err := g.ListFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	changed, err := g.ChangedPaths(context.Background(), "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	for name, got := range map[string][]string{"ListFiles": files, "ChangedPaths": changed} {
		if !slices.Contains(got, "a.go") {
			t.Errorf("%s = %q, lacks a.go: a warning swallowed it", name, got)
		}
		for _, p := range got {
			if strings.Contains(p, "warning") || strings.Contains(p, "\n") {
				t.Errorf("%s has an entry made of a warning: %q", name, p)
			}
		}
	}
}
