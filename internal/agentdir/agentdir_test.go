package agentdir

import (
	"os"
	"path/filepath"
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
