package triage

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// hostDir is a checkout with a regular file, a link to it, a link out of the
// checkout, a link to a directory, a dangling link and a nested repository.
func hostDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, dir, "real/f.go", "a\nb\nc\n")
	testutil.WriteFile(t, dir, "nested/.git/HEAD", "ref: refs/heads/main\n")
	outside := testutil.WriteFile(t, t.TempDir(), "outside.txt", "1\n2\n3\n4\n5\n")
	for name, target := range map[string]string{"linkfile": "real/f.go", "linkout": outside, "linkdir": "real", "dangling": "gone.go"} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Skipf("no symbolic links here: %v", err)
		}
	}
	return dir
}

func TestRegularFile_IsAFileOrALinkToOneInsideTheCheckout(t *testing.T) {
	dir := hostDir(t)
	for rel, want := range map[string]bool{
		"real/f.go": true, "linkfile": true,
		"linkout": false, "linkdir": false, "dangling": false, "nested": false, "real": false, "missing.go": false,
	} {
		if got := RegularFile(dir, rel); got != want {
			t.Errorf("RegularFile(%q) = %v, want %v", rel, got, want)
		}
	}
	if RegularFile(filepath.Join(dir, "missing"), "../linkfile") {
		t.Error("a link of a checkout that is not there is a file")
	}
}

// The lines of a file are counted only when it is a regular file inside the
// checkout and no larger than what is read.
func TestHostLines_CountsOnlyWhatItMayRead(t *testing.T) {
	dir := hostDir(t)
	lines := HostLines(dir)
	for rel, want := range map[string]int{"real/f.go": 3, "linkfile": 3} {
		if got, err := lines(rel); err != nil || got != want {
			t.Errorf("lines(%q) = %d, %v, want %d", rel, got, err, want)
		}
	}
	for _, rel := range []string{"linkout", "linkdir", "nested", "missing.go"} {
		if got, err := lines(rel); err == nil {
			t.Errorf("lines(%q) = %d, want an error", rel, got)
		}
	}
	if err := os.Truncate(testutil.WriteFile(t, dir, "big.bin", ""), maxHostRead+1); err != nil {
		t.Fatal(err)
	}
	if _, err := lines("big.bin"); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Errorf("lines of a file over the cap: err = %v", err)
	}
	if _, err := HostLines(filepath.Join(dir, "missing"))("real/f.go"); err == nil {
		t.Error("lines of a checkout that is not there were counted")
	}
}

// A pipe would block a read until someone writes to it; it is refused without
// being opened.
func TestHostLines_RefusesAPipeWithoutWaitingOnIt(t *testing.T) {
	testutil.Require(t, "mkfifo")
	dir := t.TempDir()
	if out, err := exec.Command("mkfifo", filepath.Join(dir, "pipe")).CombinedOutput(); err != nil {
		t.Skipf("no named pipe here: %v: %s", err, out)
	}

	if _, err := HostLines(dir)("pipe"); err == nil || !strings.Contains(err.Error(), "regular") {
		t.Errorf("lines of a pipe: err = %v, want a refusal", err)
	}
}
