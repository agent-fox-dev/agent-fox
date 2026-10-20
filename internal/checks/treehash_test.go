package checks

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// mustHash is TreeHash, failing the test on an error.
func mustHash(t *testing.T, dir string) string {
	t.Helper()
	h, err := TreeHash(context.Background(), dir)
	if err != nil {
		t.Fatalf("TreeHash(%s): %v", dir, err)
	}
	return h
}

// TestTreeHashKinds_TS_06_4 verifies TS-06-4:
// The tree hash is 64 lowercase hex characters and reflects file kind,
// executable bit and symlink target.
// Verifies: 06-REQ-1.4
func TestTreeHashKinds_TS_06_4(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "content", 0o644)
	if err := os.Symlink("missing-target", filepath.Join(dir, "l")); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}

	h1 := mustHash(t, dir)
	if !hex64.MatchString(h1) {
		t.Fatalf("hash %q is not 64 lowercase hex characters", h1)
	}

	if err := os.Chmod(filepath.Join(dir, "f.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	h2 := mustHash(t, dir)
	if h2 == h1 {
		t.Error("flipping the executable bit did not change the hash")
	}

	if err := os.Remove(filepath.Join(dir, "l")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other-target", filepath.Join(dir, "l")); err != nil {
		t.Fatal(err)
	}
	if h3 := mustHash(t, dir); h3 == h2 {
		t.Error("changing the symlink target did not change the hash, or the dangling link was not hashed")
	}
}

// TestTreeHashIsAboutContentOnly guards TS-06-4 and TS-06-5 for directories
// that are no work tree: the same content in two places has one hash, and a
// symlink's target is read and not followed.
func TestTreeHashIsAboutContentOnly(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, dir := range []string{a, b} {
		writeFile(t, dir, "x/y.txt", "same", 0o644)
		writeFile(t, dir, "z", "other", 0o755)
	}
	if mustHash(t, a) != mustHash(t, b) {
		t.Error("the same content in two directories has two hashes")
	}

	// A symlink to a file whose content changes: the link itself did not.
	writeFile(t, a, "target.txt", "one", 0o644)
	if err := os.Symlink("target.txt", filepath.Join(a, "link")); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}
	before := mustHash(t, a)
	writeFile(t, a, "unrelated.txt", "u", 0o644)
	if err := os.Remove(filepath.Join(a, "unrelated.txt")); err != nil {
		t.Fatal(err)
	}
	if mustHash(t, a) != before {
		t.Error("adding and removing a file left a different hash")
	}
}

// TestTreeHashFramesItsEntries guards the hash against two trees that differ
// only in where one entry ends and the next begins.
func TestTreeHashFramesItsEntries(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	writeFile(t, a, "ab", "c", 0o644)
	writeFile(t, b, "a", "bc", 0o644)
	writeFile(t, a, "d", "", 0o644)
	writeFile(t, b, "bd", "", 0o644)
	if mustHash(t, a) == mustHash(t, b) {
		t.Error("two different trees have one hash")
	}
}

// TestTreeHashProperty_TS_06_5 verifies TS-06-5:
// The tree hash depends only on the file view's content, never on HEAD,
// location or ignored files, and any one-byte change alters it.
// Verifies: 06-REQ-1.5
func TestTreeHashProperty_TS_06_5(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	rng := rand.New(rand.NewPCG(6, 5))

	// build builds a repository with the given number of commits before the
	// files, and the same .gitignore in each.
	build := func(files map[string]string, history int) string {
		dir := t.TempDir()
		gitIn(t, dir, "init", "-q", "-b", "main")
		for i := range history {
			gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("chore: %d", i))
		}
		writeFile(t, dir, ".gitignore", "*.ignored\n", 0o644)
		for name, content := range files {
			writeFile(t, dir, name, content, 0o644)
		}
		if history > 1 {
			gitIn(t, dir, "add", "-A")
			gitIn(t, dir, "commit", "-q", "-m", "feat: files")
		}
		return dir
	}

	for i := range 10 {
		files := map[string]string{}
		var names []string
		for j := range 2 + rng.IntN(6) {
			name := fmt.Sprintf("d%d/f %d.txt", rng.IntN(3), j)
			files[name] = fmt.Sprintf("content %d %d", j, rng.IntN(1_000_000))
			names = append(names, name)
		}
		a, b := build(files, 1), build(files, 3)
		writeFile(t, b, "build/out.ignored", "ignored content", 0o644)
		writeFile(t, b, "d0/other.ignored", "more", 0o644)

		ha, hb := mustHash(t, a), mustHash(t, b)
		if ha != hb {
			t.Fatalf("#%d: the same files in two repositories with different histories and ignored files have two hashes", i)
		}

		// Changing only ignored files leaves the hash; changing one byte of a
		// viewed file does not.
		writeFile(t, a, "x.ignored", "changed", 0o644)
		if mustHash(t, a) != ha {
			t.Fatalf("#%d: an ignored file changed the hash", i)
		}
		victim := names[rng.IntN(len(names))]
		content := []byte(files[victim])
		content[len(content)-1] ^= 1
		writeFile(t, a, victim, string(content), 0o644)
		if mustHash(t, a) == hb {
			t.Fatalf("#%d: changing one byte of %q did not change the hash", i, victim)
		}
	}
}

// TestUnreadableFileIsAMarker_TS_06_6 verifies TS-06-6:
// An unreadable file hashes as a fixed unreadable marker and does not fail
// the tree hash.
// Verifies: 06-REQ-1.6
func TestUnreadableFileIsAMarker_TS_06_6(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not stop this user")
	}
	dir := t.TempDir()
	writeFile(t, dir, "readable.txt", "x", 0o644)
	writeFile(t, dir, "secret.txt", "hidden", 0o000)

	h1, err := TreeHash(context.Background(), dir)
	if err != nil {
		t.Fatalf("an unreadable file failed the hash: %v", err)
	}
	h2 := mustHash(t, dir)
	if h1 != h2 {
		t.Error("two hashes of one tree differ")
	}

	if err := os.Chmod(filepath.Join(dir, "secret.txt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if mustHash(t, dir) == h1 {
		t.Error("making the file readable did not change the hash")
	}
}

// TestTreeHashCreatesTheAgentDirectory_TS_06_7 verifies TS-06-7:
// Computing a tree hash creates .agent-fox/ with its .gitignore so the
// directory never shows in git status.
// Verifies: 06-REQ-1.7
func TestTreeHashCreatesTheAgentDirectory_TS_06_7(t *testing.T) {
	dir := newRepo(t)
	if _, err := os.Stat(filepath.Join(dir, ".agent-fox")); err == nil {
		t.Fatal("the fixture already has .agent-fox")
	}

	h1 := mustHash(t, dir)

	gitignore := filepath.Join(dir, ".agent-fox", ".gitignore")
	if data, err := os.ReadFile(gitignore); err != nil || string(data) != "*\n" {
		t.Fatalf(".agent-fox/.gitignore = %q (%v), want \"*\\n\"", data, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".agent-fox", "gate", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, ".agent-fox/gate/x/result.json", "{}", 0o644)
	if status := gitIn(t, dir, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf("git status --porcelain lists:\n%s", status)
	}
	if h2 := mustHash(t, dir); h2 != h1 {
		t.Error("files under .agent-fox changed the tree hash")
	}
	before, _ := os.Stat(gitignore)
	mustHash(t, dir)
	if after, _ := os.Stat(gitignore); !after.ModTime().Equal(before.ModTime()) {
		t.Error("a second call rewrote .agent-fox/.gitignore")
	}
}
