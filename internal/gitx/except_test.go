package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CommitAllExcept commits everything but the paths it is told to leave, which
// stay in the tree untracked; the paths are literal, so a name with glob
// characters leaves only itself. CleanExcept removes every other untracked
// file.
func TestCommitAllExceptAndCleanExcept(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	for name, body := range map[string]string{
		"README.md":           "# changed\n",
		"mine.go":             "package x\n",
		"theirs [draft]*.md":  "not mine\n",
		"theirs.md":           "not mine either\n",
		".specs/17_x/prd.md":  "a spec someone is writing\n",
		"attempt/scratch.txt": "left by a discarded attempt\n",
	} {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	leave := []string{"theirs [draft]*.md", ".specs/17_x/prd.md", "attempt/scratch.txt"}
	if _, err := g.CommitAllExcept(ctx, "feat: mine\n", false, leave); err != nil {
		t.Fatal(err)
	}
	out, _, _ := ExecRunner(ctx, dir, []string{"git", "show", "--name-only", "--format=", "HEAD"})
	if got := strings.Fields(out); strings.Join(got, ",") != "README.md,mine.go,theirs.md" {
		t.Errorf("committed %q, want README.md, mine.go and theirs.md", got)
	}
	untracked, err := g.UntrackedFiles(ctx)
	if err != nil || len(untracked) != 3 {
		t.Fatalf("untracked = %q, %v", untracked, err)
	}

	if err := g.CleanExcept(ctx, leave[:2]); err != nil {
		t.Fatal(err)
	}
	for _, p := range leave[:2] {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			t.Errorf("%s was removed: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "attempt/scratch.txt")); !os.IsNotExist(err) {
		t.Errorf("attempt/scratch.txt survived CleanExcept: %v", err)
	}
}

// DiffSince shows an untracked file's content without leaving it marked
// intent-to-add: a marked file is no longer untracked, so a commit that leaves
// untracked files alone would take it, and `git reset --hard` deletes it.
func TestDiffSinceLeavesUntrackedFilesUntracked(t *testing.T) {
	g, dir := newRepo(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(dir, "theirs *.md"), []byte("not mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nameStatus, patch, err := g.DiffSince(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(nameStatus, "theirs *.md") || !strings.Contains(patch, "+not mine") {
		t.Errorf("the diff misses the untracked file:\n%s\n%s", nameStatus, patch)
	}
	if untracked, _ := g.UntrackedFiles(ctx); len(untracked) != 1 || untracked[0] != "theirs *.md" {
		t.Errorf("after DiffSince, untracked = %q", untracked)
	}
	if err := g.ResetHard(ctx, "HEAD"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "theirs *.md")); err != nil {
		t.Errorf("git reset --hard after DiffSince deleted the untracked file: %v", err)
	}
}
