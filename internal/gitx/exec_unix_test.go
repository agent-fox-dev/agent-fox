//go:build !windows

package gitx

import (
	"context"
	"testing"
	"time"
)

// Issue #216: a timeout ends the whole command, not only its direct child. A
// grandchild that keeps the output pipe open — `go test` under `make` — would
// otherwise hold the run until it exits on its own.
func TestATimeoutEndsTheWholeProcessGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, _ = ExecRunner(ctx, t.TempDir(), []string{"sh", "-c", "sleep 30 & wait"})
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("the run took %s past a 300ms timeout: the grandchild kept it alive", d)
	}
}
