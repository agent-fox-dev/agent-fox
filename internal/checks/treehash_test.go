package checks

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// The steps change one tree in turn; each says whether the hash changes. The
// hash is of what the files hold, their executable bits and the targets of
// links, and of the type alone of anything else, which is not opened.
func TestTreeHashFollowsTheContentOnly(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, ".gitignore", "*.ignored\n", 0o644)
	writeFile(t, dir, "f.txt", "content", 0o644)
	testutil.Commit(t, dir, "chore: files")
	link := filepath.Join(dir, "l")
	relink := func(target string) func() {
		return func() {
			_ = os.Remove(link)
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlinks are not available: %v", err)
			}
		}
	}
	hash := mustHash(t, dir)
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(hash) {
		t.Fatalf("TreeHash = %q, want 64 lower-case hex characters", hash)
	}
	for _, step := range []struct {
		name    string
		change  func()
		changes bool
	}{
		{"nothing", func() {}, false},
		{"a commit of the same files", func() { testutil.Git(t, dir, "commit", "-q", "--allow-empty", "-m", "empty") }, false},
		{"an ignored file", func() { writeFile(t, dir, "x.ignored", "x", 0o644) }, false},
		{"a file of .agent-fox", func() { writeFile(t, dir, ".agent-fox/gate/x/result.json", "{}", 0o644) }, false},
		{"a byte of content", func() { writeFile(t, dir, "f.txt", "contenT", 0o644) }, true},
		{"the executable bit", func() { writeFile(t, dir, "f.txt", "contenT", 0o755) }, true},
		{"a link to nothing", relink("missing-target"), true},
		{"the target of the link", relink("f.txt"), true},
		// A nested repository is one entry, of its type: its files are its own.
		{"a nested repository", func() { testutil.Git(t, testutil.Repo(t, nil), "clone", "-q", ".", filepath.Join(dir, "nested")) }, true},
		{"a file of the nested repository", func() { writeFile(t, dir, "nested/README.md", "changed", 0o644) }, false},
		{"a file added and removed", func() {
			writeFile(t, dir, "tmp.txt", "x", 0o644)
			if err := os.Remove(filepath.Join(dir, "tmp.txt")); err != nil {
				t.Fatal(err)
			}
		}, false},
	} {
		step.change()

		now := mustHash(t, dir)

		if (now != hash) != step.changes {
			t.Errorf("%s: the hash changed %v, want %v", step.name, now != hash, step.changes)
		}
		hash = now
	}
}

// A named pipe is its type: it is not opened, which would wait for a writer.
func TestTreeHashOfANamedPipe(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "content", 0o644)
	before := mustHash(t, dir)
	if err := exec.Command("mkfifo", filepath.Join(dir, "pipe")).Run(); err != nil {
		t.Skipf("named pipes are not available: %v", err)
	}
	done := make(chan string, 1)
	go func() {
		h, _ := TreeHash(context.Background(), dir)
		done <- h
	}()

	select {
	case h := <-done:
		if h == before || len(h) != 64 {
			t.Errorf("TreeHash = %q, was %s without the pipe; want a hash that counts it", h, before)
		}
	case <-time.After(testutil.WaitTimeout):
		t.Fatal("TreeHash opened the named pipe and waits on it")
	}
}

// The same files hash the same wherever they are and whatever history the
// repository has.
func TestTreeHashIsTheSameForTheSameFiles(t *testing.T) {
	files := map[string]string{"x/y.txt": "same", "z z": "other"}
	plain := t.TempDir()
	for name, content := range files {
		testutil.WriteFile(t, plain, name, content)
	}
	repo := newRepo(t)
	testutil.Git(t, repo, "rm", "-q", "README.md")
	for name, content := range files {
		testutil.WriteFile(t, repo, name, content)
	}
	testutil.Commit(t, repo, "feat: the files")
	elsewhere := t.TempDir()
	for name, content := range files {
		testutil.WriteFile(t, elsewhere, name, content)
	}

	if a, b, c := mustHash(t, plain), mustHash(t, elsewhere), mustHash(t, repo); a != b || a != c {
		t.Errorf("the same files hash %s, %s and in a repository %s", a, b, c)
	}
}

// Every field is framed by its length, so trees that differ only in where
// one entry ends and the next begins differ.
func TestTreeHashFramesItsEntries(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	testutil.WriteFile(t, a, "ab", "c")
	testutil.WriteFile(t, b, "a", "bc")
	testutil.WriteFile(t, a, "d", "")
	testutil.WriteFile(t, b, "bd", "")
	if mustHash(t, a) == mustHash(t, b) {
		t.Error("two different trees have one hash")
	}
}

// A file that cannot be read is a fixed marker, and does not fail the hash.
func TestTreeHashOfAnUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not stop root")
	}
	dir := t.TempDir()
	writeFile(t, dir, "secret.txt", "hidden", 0o000)

	h1, err := TreeHash(t.Context(), dir)

	if err != nil || h1 != mustHash(t, dir) {
		t.Fatalf("TreeHash = %s, %v; want the same hash each time", h1, err)
	}
	writeFile(t, dir, "secret.txt", "hidden", 0o644)
	if mustHash(t, dir) == h1 {
		t.Error("making the file readable did not change the hash")
	}
}

// The hash makes .agent-fox with a .gitignore of its own, once, so that what
// af writes there never shows in git status.
func TestTreeHashMakesTheAgentDirectoryIgnored(t *testing.T) {
	dir := newRepo(t)

	mustHash(t, dir)

	gitignore := filepath.Join(dir, ".agent-fox", ".gitignore")
	if got := testutil.ReadFile(t, gitignore); got != "*\n" {
		t.Fatalf(".agent-fox/.gitignore = %q, want \"*\\n\"", got)
	}
	writeFile(t, dir, ".agent-fox/gate/x/result.json", "{}", 0o644)
	if status := testutil.Git(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("git status lists:\n%s", status)
	}
	before, _ := os.Stat(gitignore)
	mustHash(t, dir)
	if after, _ := os.Stat(gitignore); !after.ModTime().Equal(before.ModTime()) {
		t.Error("a second hash wrote .agent-fox/.gitignore again")
	}
}
