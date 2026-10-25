package agentdir

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestEnsure_MakesTheDirectoryAndItsGitignore(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	paths, errs := make([]string, 8), make([]error, 8)
	for i := range paths {
		wg.Go(func() { paths[i], errs[i] = Ensure(dir) })
	}
	wg.Wait()

	for i := range paths {
		if errs[i] != nil || paths[i] != filepath.Join(dir, ".agent-fox") {
			t.Errorf("call %d of 8 at once: %q, %v", i, paths[i], errs[i])
		}
	}
	if files := testutil.Files(t, dir); len(files) != 1 || files[".agent-fox/.gitignore"] != "*\n" {
		t.Errorf("the checkout holds %v, want .agent-fox/.gitignore with *", files)
	}
}

func TestEnsure_LeavesAGitignoreThatIsThere(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, ".agent-fox/.gitignore", "something else\n")

	if _, err := Ensure(dir); err != nil {
		t.Fatal(err)
	}

	if got := testutil.ReadFile(t, filepath.Join(dir, ".agent-fox", ".gitignore")); got != "something else\n" {
		t.Errorf(".gitignore = %q, want it as it was", got)
	}
}

func TestEnsure_FailsWhereTheDirectoryCannotBe(t *testing.T) {
	file := testutil.WriteFile(t, t.TempDir(), "file", "x")
	taken := t.TempDir()
	testutil.WriteFile(t, taken, ".agent-fox", "a file")
	missing := filepath.Join(t.TempDir(), "missing")
	for name, dir := range map[string]string{"a file": file, ".agent-fox is a file": taken, "a missing directory": missing} {
		if _, err := Ensure(dir); err == nil {
			t.Errorf("%s: Ensure succeeded", name)
		}
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Errorf("Ensure made the missing directory: %v", err)
	}
}

// writers are the two ways to replace a file, with the mode each gives it.
var writers = map[string]struct {
	write func(path string, data []byte) error
	mode  os.FileMode
}{
	"WriteFileAtomic":  {WriteFileAtomic, 0o644},
	"ReplaceFile 0600": {func(p string, d []byte) error { return ReplaceFile(p, d, 0o600) }, 0o600},
	"ReplaceFile 0755": {func(p string, d []byte) error { return ReplaceFile(p, d, 0o755) }, 0o755},
}

func TestReplace_ReplacesTheFileWithItsModeAndLeavesNothingElse(t *testing.T) {
	for name, w := range writers {
		dir := t.TempDir()
		path := testutil.WriteFile(t, dir, "result.json", "old, and longer than what replaces it")

		for _, content := range []string{"first", "second"} {
			if err := w.write(path, []byte(content)); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}

		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != w.mode {
			t.Errorf("%s: mode %v (%v), want %v", name, info.Mode().Perm(), err, w.mode)
		}
		if files := testutil.Files(t, dir); len(files) != 1 || files["result.json"] != "second" {
			t.Errorf("%s: the directory holds %v, want result.json with the last data only", name, files)
		}
	}
}

func TestReplace_FailsWithoutADirectoryOrOverOneAndLeavesNothing(t *testing.T) {
	for name, w := range writers {
		dir := t.TempDir()
		testutil.WriteFile(t, dir, "taken/x", "")
		for _, rel := range []string{"missing/x.json", "taken"} {
			if err := w.write(filepath.Join(dir, rel), []byte("x")); err == nil {
				t.Errorf("%s: writing %s succeeded", name, rel)
			}
		}
		if files := testutil.Files(t, dir); len(files) != 1 {
			t.Errorf("%s: the directory holds %v, want taken/x only", name, files)
		}
	}
}

func TestWriteFileAtomic_AReaderNeverSeesAPartialFile(t *testing.T) {
	dir := t.TempDir()
	contents := []string{strings.Repeat("a", 64<<10), strings.Repeat("b", 96<<10), strings.Repeat("c", 32<<10)}
	path := testutil.WriteFile(t, dir, "labels.json", contents[0])
	stop, torn := make(chan struct{}), make(chan int, 1)
	var reader, writers sync.WaitGroup
	reader.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if data, err := os.ReadFile(path); err == nil && !slices.Contains(contents, string(data)) {
				torn <- len(data)
				return
			}
		}
	})

	for _, content := range contents {
		writers.Go(func() {
			for range 30 {
				if err := WriteFileAtomic(path, []byte(content)); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	writers.Wait()
	close(stop)
	reader.Wait()

	select {
	case n := <-torn:
		t.Errorf("a reader saw a file of %d bytes that was never written", n)
	default:
	}
	if files := testutil.Files(t, dir); len(files) != 1 {
		t.Errorf("the directory holds %d files after the writes, want only labels.json", len(files))
	}
}
