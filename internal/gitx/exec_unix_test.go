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

// TestStdoutOnlyKeepsStderrOutOfWhatIsParsed: a runner asked for stdout only
// returns what the command printed on stdout when it succeeds, and the text of
// its failure when it does not. Without the request the two are mixed.
func TestStdoutOnlyKeepsStderrOutOfWhatIsParsed(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is not installed")
	}
	script := []string{"sh", "-c", "echo out; echo noise >&2"}

	mixed, _, _ := ExecRunner(context.Background(), t.TempDir(), script)
	clean, code, err := ExecRunner(StdoutOnly(context.Background()), t.TempDir(), script)

	if !strings.Contains(mixed, "noise") {
		t.Errorf("without the request the output is %q, want stderr in it", mixed)
	}
	if clean != "out\n" || code != 0 || err != nil {
		t.Errorf("stdout only: %q, exit code %d, error %v; want \"out\\n\"", clean, code, err)
	}

	failed, code, err := ExecRunner(StdoutOnly(context.Background()), t.TempDir(), []string{"sh", "-c", "echo partial; echo why it failed >&2; exit 3"})
	if err != nil || code != 3 || !strings.Contains(failed, "why it failed") {
		t.Errorf("a failure: %q, exit code %d, error %v; want its stderr in the text and exit code 3", failed, code, err)
	}
}

// TestSuccessWithALingeringChildIsSuccess: a command that exits 0 and leaves a
// process holding its output pipe is a command that succeeded; Go reports
// that as ErrWaitDelay, which is not a failure to run.
func TestSuccessWithALingeringChildIsSuccess(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is not installed")
	}
	start := time.Now()

	out, code, err := ExecRunner(context.Background(), t.TempDir(), []string{"sh", "-c", "echo done; sleep 20 & exit 0"})

	if err != nil || code != 0 || !strings.Contains(out, "done") {
		t.Errorf("output %q, exit code %d, error %v; want success", out, code, err)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("the run took %s", time.Since(start))
	}
}
