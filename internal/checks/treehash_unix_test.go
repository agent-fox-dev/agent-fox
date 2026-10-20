//go:build !windows

package checks

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestTreeHashOfANamedPipe_TS_06_4 verifies the named-pipe part of TS-06-4:
// a named pipe contributes only its type, and the call does not block.
// Verifies: 06-REQ-1.4
func TestTreeHashOfANamedPipe_TS_06_4(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "content", 0o644)
	before := mustHash(t, dir)
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
		t.Skipf("named pipes are not available: %v", err)
	}

	done := make(chan string, 1)
	go func() {
		h, err := TreeHash(context.Background(), dir)
		if err != nil {
			h = "error: " + err.Error()
		}
		done <- h
	}()
	select {
	case h := <-done:
		if !hex64.MatchString(h) {
			t.Fatalf("hash = %q", h)
		}
		if h == before {
			t.Error("a named pipe left the hash as it was; it should contribute its type")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("TreeHash blocked on a named pipe: it was opened")
	}
}
