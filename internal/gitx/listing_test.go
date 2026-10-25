package gitx

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// Names with spaces at either end and non-ASCII characters come back as they
// are, unquoted and untrimmed, sorted and once each; an ignored file does not.
func TestListFilesIsByteExact(t *testing.T) {
	g, dir := newRepo(t)
	commit(t, g, map[string]string{".gitignore": "ignored.log\n", "a b.txt": "x", " lead.txt": "x"})
	write(t, dir, map[string]string{"é/ü.go": "x", "trail .txt ": "x", "ignored.log": "x", "README.md": "# changed\n"})

	got, err := g.ListFiles(t.Context())

	if want := []string{" lead.txt", ".gitignore", "README.md", "a b.txt", "trail .txt ", "é/ü.go"}; err != nil || !slices.Equal(got, want) {
		t.Errorf("ListFiles = %q, %v; want %q", got, err, want)
	}
}

// The tracked files an ignore rule matches: committed before the rule.
func TestTrackedIgnored(t *testing.T) {
	g, dir := newRepo(t)
	commit(t, g, map[string]string{"gen/api.pb.go": "x", "plain.go": "x"})
	write(t, dir, map[string]string{".gitignore": "gen/\n*.log\n", "untracked.log": "x"})

	got, err := g.TrackedIgnored(t.Context())

	if want := []string{"gen/api.pb.go"}; err != nil || !slices.Equal(got, want) {
		t.Errorf("TrackedIgnored = %q, %v; want %q", got, err, want)
	}
}

func TestGitlinksAreTheSubmodulesOfTheIndex(t *testing.T) {
	g, dir := newRepo(t)
	head := commit(t, g, map[string]string{"plain.go": "x"})
	for _, path := range []string{"z sub", "vendor/lib"} {
		testutil.Git(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+head+","+path)
	}

	got, err := g.Gitlinks(t.Context())

	if want := []string{"vendor/lib", "z sub"}; err != nil || !slices.Equal(got, want) {
		t.Errorf("Gitlinks = %q, %v; want %q", got, err, want)
	}
}

// What differs from the ref, committed or not, and the untracked files that
// are not ignored, byte for byte; a rename is both of its paths, so that a
// revert puts the old one back.
func TestChangedPaths(t *testing.T) {
	g, dir := newRepo(t)
	base := commit(t, g, map[string]string{
		".gitignore": "ignored.log\n", " lead.txt": "before", "same.txt": "same",
		"old.go": "package a // long enough to be found again as the same file after a rename\n",
	})
	if err := os.Rename(filepath.Join(dir, "old.go"), filepath.Join(dir, "new.go")); err != nil {
		t.Fatal(err)
	}
	commit(t, g, map[string]string{" lead.txt": "after", "é x.go": "package x"})
	write(t, dir, map[string]string{"trail .txt ": "x", "ignored.log": "x"})

	got, err := g.ChangedPaths(t.Context(), base)

	if want := []string{" lead.txt", "new.go", "old.go", "trail .txt ", "é x.go"}; err != nil || !slices.Equal(got, want) {
		t.Errorf("ChangedPaths = %q, %v; want %q", got, err, want)
	}
	if _, err := g.ChangedPaths(t.Context(), "--output=x"); err == nil {
		t.Error("ChangedPaths took an option for a ref")
	}
}

// git diff refreshes the stat data of the index and writes it back; pointed at
// a copy of the index, it leaves the real one byte for byte as it was.
func TestChangedPathsOnAPrivateIndexLeavesTheRealOne(t *testing.T) {
	g, dir := newRepo(t)
	commit(t, g, map[string]string{"f.txt": "same\n"})
	earlier := time.Now().Add(-time.Hour) // the content is the same, the stat data is not
	if err := os.Chtimes(filepath.Join(dir, "f.txt"), earlier, earlier); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(dir, ".git", "index")
	before := []byte(testutil.ReadFile(t, index))
	private := testutil.WriteFile(t, t.TempDir(), "index", string(before))

	paths, err := New(dir, EnvRunner("GIT_INDEX_FILE="+private)).ChangedPaths(t.Context(), "HEAD")

	if err != nil || len(paths) != 0 {
		t.Errorf("ChangedPaths = %q, %v; want none: only the stat data of f.txt changed", paths, err)
	}
	if after := []byte(testutil.ReadFile(t, index)); !bytes.Equal(after, before) {
		t.Error("the real index changed although git was pointed at a copy")
	}
}

// git writes to stderr and exits 0 (a trace, a warning about a directory it
// cannot open); that is no part of a name and hides none.
func TestListingsAreNotSpoiledByWhatGitWritesToStderr(t *testing.T) {
	g, dir := newRepo(t)
	write(t, dir, map[string]string{"a.go": "package a", "b.log": "x"})
	t.Setenv("GIT_TRACE", "1")

	files, filesErr := g.ListFiles(t.Context())
	changed, changedErr := g.ChangedPaths(t.Context(), "HEAD")
	ignored, ignoredErr := g.Ignored(t.Context(), []string{"a.go"})

	if filesErr != nil || !slices.Equal(files, []string{"README.md", "a.go", "b.log"}) {
		t.Errorf("ListFiles = %q, %v", files, filesErr)
	}
	if changedErr != nil || !slices.Equal(changed, []string{"a.go", "b.log"}) {
		t.Errorf("ChangedPaths = %q, %v", changed, changedErr)
	}
	if ignoredErr != nil || len(ignored) != 0 {
		t.Errorf("Ignored = %q, %v", ignored, ignoredErr)
	}
}

// A directory that is no work tree is an error, not an empty list.
func TestListingsOutsideARepositoryFail(t *testing.T) {
	g := New(t.TempDir(), nil)
	for name, list := range map[string]func() ([]string, error){
		"ListFiles":      func() ([]string, error) { return g.ListFiles(t.Context()) },
		"TrackedIgnored": func() ([]string, error) { return g.TrackedIgnored(t.Context()) },
		"Gitlinks":       func() ([]string, error) { return g.Gitlinks(t.Context()) },
		"ChangedPaths":   func() ([]string, error) { return g.ChangedPaths(t.Context(), "HEAD") },
		"Ignored":        func() ([]string, error) { return g.Ignored(t.Context(), []string{"a"}) },
	} {
		if got, err := list(); err == nil {
			t.Errorf("%s = %q, want an error", name, got)
		}
	}
}

// What a fix run does not copy to the host: the ignored paths, and not a
// tracked one that a rule matches.
func TestIgnoredIsWhatTheRulesIgnore(t *testing.T) {
	g, dir := newRepo(t)
	write(t, dir, map[string]string{".gitignore": "*.log\nbuild/\n", "kept.log": "tracked\n"})
	testutil.Git(t, dir, "add", "-f", ".gitignore", "kept.log")
	testutil.Commit(t, dir, "chore: files")

	for _, tc := range []struct {
		name  string
		paths []string
		want  []string
	}{
		{"a mix", []string{"a.go", "x.log", "build/out.bin", "kept.log", "docs/n.md", "sub/new.log"}, []string{"x.log", "build/out.bin", "sub/new.log"}},
		{"none ignored", []string{"a.go", "docs/n.md"}, nil},
		{"no path", nil, nil},
		{"odd names, unquoted", []string{"with space.log", "new\nline.log", "ünï.log"}, []string{"with space.log", "new\nline.log", "ünï.log"}},
	} {
		if got, err := g.Ignored(t.Context(), tc.paths); err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s: Ignored = %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
}
