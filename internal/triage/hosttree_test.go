package triage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostDir is a checkout with a regular file, a link to it, a link out of the
// checkout, a link to a directory, a nested repository and a pipe.
func hostDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	for path, content := range map[string]string{
		filepath.Join(dir, "real", "f.go"):           "a\nb\nc\n",
		filepath.Join(dir, "nested", ".git", "HEAD"): "ref: refs/heads/main\n",
		outside: "1\n2\n3\n4\n5\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{"linkfile": "real/f.go", "linkout": outside, "linkdir": "real"} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Skipf("no symbolic links here: %v", err)
		}
	}
	if err := makePipe(filepath.Join(dir, "pipe")); err != nil {
		t.Logf("no pipe: %v", err)
	}
	return dir
}

func TestRegularFileKnowsWhatADiagnosisMayCite(t *testing.T) {
	dir := hostDir(t)
	for rel, want := range map[string]bool{
		"real/f.go": true, "linkfile": true,
		"linkout": false, "linkdir": false, "nested": false, "real": false, "missing.go": false,
	} {
		if got := RegularFile(dir, rel); got != want {
			t.Errorf("RegularFile(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestHostLinesCountsOnlyWhatItMayRead(t *testing.T) {
	dir := hostDir(t)
	lines := HostLines(dir)

	for rel, want := range map[string]int{"real/f.go": 3, "linkfile": 3} {
		if got, err := lines(rel); err != nil || got != want {
			t.Errorf("lines(%q) = %d, %v, want %d", rel, got, err, want)
		}
	}
	for _, rel := range []string{"linkout", "linkdir", "nested", "missing.go"} {
		if got, err := lines(rel); err == nil {
			t.Errorf("lines(%q) = %d, want an error: a link that leads out, or no file, is not read", rel, got)
		}
	}
	if _, err := os.Lstat(filepath.Join(dir, "pipe")); err == nil {
		done := make(chan error, 1)
		go func() { _, err := lines("pipe"); done <- err }()
		if err := <-done; err == nil || !strings.Contains(err.Error(), "regular") {
			t.Errorf("lines of a pipe: err = %v, want a refusal that does not wait for a writer", err)
		}
	}

	big := filepath.Join(dir, "big.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxHostRead + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := lines("big.bin"); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Errorf("lines of a file over the cap: err = %v", err)
	}
}
