package agentdir

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// TestEnsureCreatesTheDirectoryAndItsGitignore checks the behaviour that the
// run directory of spec 05 and the tree hash of spec 06 share.
func TestEnsureCreatesTheDirectoryAndItsGitignore(t *testing.T) {
	dir := t.TempDir()

	path, err := Ensure(dir)
	if err != nil {
		t.Fatal(err)
	}

	if want := filepath.Join(dir, ".agent-fox"); path != want {
		t.Errorf("Ensure returned %q, want %q", path, want)
	}
	if data, err := os.ReadFile(filepath.Join(path, ".gitignore")); err != nil || string(data) != "*\n" {
		t.Errorf(".gitignore = %q (%v), want \"*\\n\"", data, err)
	}
}

// TestEnsureLeavesAnExistingGitignoreAlone and runs together safely.
func TestEnsureLeavesAnExistingGitignoreAlone(t *testing.T) {
	dir := t.TempDir()
	agent := filepath.Join(dir, ".agent-fox")
	if err := os.MkdirAll(agent, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(agent, ".gitignore")
	if err := os.WriteFile(custom, []byte("something else\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Ensure(dir); err != nil {
		t.Fatal(err)
	}

	if data, _ := os.ReadFile(custom); string(data) != "something else\n" {
		t.Errorf("an existing .gitignore was changed to %q", data)
	}
}

// TestEnsureRunsTogetherSafely: runs that start together neither fail nor
// overwrite one another.
func TestEnsureRunsTogetherSafely(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = Ensure(dir)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d: %v", i, err)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(dir, ".agent-fox", ".gitignore")); string(data) != "*\n" {
		t.Errorf(".gitignore = %q after concurrent calls", data)
	}
}

// TestEnsureFailsInsideAFile: a path that cannot hold the directory is an
// error.
func TestEnsureFailsInsideAFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(file); err == nil {
		t.Error("Ensure inside a regular file succeeded")
	}
}

// TestWriteFileAtomicReplacesAndLeavesNothingBehind: the file holds the data,
// an earlier file is replaced, and no temporary file is left.
func TestWriteFileAtomicReplacesAndLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")

	for _, content := range []string{"first", "second, which is longer"} {
		if err := WriteFileAtomic(path, []byte(content)); err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != content {
			t.Fatalf("file = %q (%v), want %q", data, err, content)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v (%v), want 0644", info.Mode().Perm(), err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("the directory holds %d entries, want only result.json", len(entries))
	}
}

// TestWriteFileAtomicNeverShowsAPartialFile: a reader that runs while others
// write only ever sees one of the complete contents.
func TestWriteFileAtomicNeverShowsAPartialFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "labels.json")
	contents := []string{strings.Repeat("a", 64<<10), strings.Repeat("b", 96<<10), strings.Repeat("c", 32<<10)}
	if err := WriteFileAtomic(path, []byte(contents[0])); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var readers, writers sync.WaitGroup
	torn := make(chan string, 1)
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			data, err := os.ReadFile(path)
			if err == nil && !slices.Contains(contents, string(data)) {
				select {
				case torn <- fmt.Sprintf("%d bytes starting %q", len(data), data[:min(len(data), 8)]):
				default:
				}
				return
			}
		}
	}()
	for _, content := range contents {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for range 30 {
				if err := WriteFileAtomic(path, []byte(content)); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	writers.Wait()
	close(stop)
	readers.Wait()

	select {
	case what := <-torn:
		t.Errorf("a reader saw a file that was not written: %s", what)
	default:
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("the directory holds %d entries after the writes, want only labels.json", len(entries))
	}
}

// TestWriteFileAtomicNeedsTheDirectory: a missing directory is an error, not
// a tree of new ones, and nothing is left behind.
func TestWriteFileAtomicNeedsTheDirectory(t *testing.T) {
	dir := t.TempDir()

	if err := WriteFileAtomic(filepath.Join(dir, "missing", "x.json"), []byte("x")); err == nil {
		t.Error("WriteFileAtomic into a directory that does not exist succeeded")
	}
	if _, err := os.Stat(filepath.Join(dir, "missing")); err == nil {
		t.Error("WriteFileAtomic created the directory")
	}
}

// TestReplaceFileHasTheModeFromTheStart: the file is made with the mode it is
// to have, replaces what is there, and leaves nothing behind. A file that is
// private is never there with the mode of one that is not.
func TestReplaceFileHasTheModeFromTheStart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, mode := range []os.FileMode{0o600, 0o755, 0o644} {
		if err := ReplaceFile(path, []byte("new "+mode.String()), mode); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Errorf("mode = %v (%v), want %v", info.Mode().Perm(), err, mode)
		}
		if data, _ := os.ReadFile(path); string(data) != "new "+mode.String() {
			t.Errorf("content = %q", data)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the directory holds %d entries, want only secret.txt", len(entries))
	}
	if err := ReplaceFile(filepath.Join(dir, "missing", "x"), []byte("x"), 0o644); err == nil {
		t.Error("ReplaceFile into a directory that does not exist succeeded")
	}
}
