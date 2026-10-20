//go:build !windows

package gitx

import (
	"context"
	"os/exec"
	"strings"
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

// TestEnvRunnerAddsToTheEnvironment: the extra variables are there for the
// command, and the rest of the environment is too.
func TestEnvRunnerAddsToTheEnvironment(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is not installed")
	}
	t.Setenv("AF_ENV_RUNNER_OLD", "kept")

	out, code, err := EnvRunner("AF_ENV_RUNNER_NEW=added")(context.Background(), t.TempDir(),
		[]string{"sh", "-c", `echo "$AF_ENV_RUNNER_NEW $AF_ENV_RUNNER_OLD"`})

	if err != nil || code != 0 || strings.TrimSpace(out) != "added kept" {
		t.Errorf("EnvRunner ran with output %q, exit code %d, error %v; want \"added kept\"", out, code, err)
	}
}
