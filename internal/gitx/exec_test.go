package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestRunnerTellsAnExitCodeFromAFailureToRun(t *testing.T) {
	testutil.Require(t, "sh")
	for _, tc := range []struct {
		name     string
		argv     []string
		wantCode int
		wantErr  bool
	}{
		{"a success", []string{"sh", "-c", "echo hi"}, 0, false},
		{"a non-zero exit is information", []string{"sh", "-c", "exit 3"}, 3, false},
		{"a missing program is an error", []string{"definitely-not-a-program-xyz"}, -1, true},
		{"an empty command is an error", nil, -1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, code, err := ExecRunner(t.Context(), t.TempDir(), tc.argv)
			if code != tc.wantCode || (err != nil) != tc.wantErr {
				t.Errorf("exit code %d, error %v; want %d and an error %v", code, err, tc.wantCode, tc.wantErr)
			}
		})
	}
}

// Without the request the output is combined; with it a success is stdout
// alone and a failure carries its stderr.
func TestStdoutOnlyKeepsStderrOutOfASuccessOnly(t *testing.T) {
	testutil.Require(t, "sh")
	noisy := []string{"sh", "-c", "echo out; echo noise >&2"}

	mixed, _, _ := ExecRunner(t.Context(), t.TempDir(), noisy)
	clean, code, err := ExecRunner(StdoutOnly(t.Context()), t.TempDir(), noisy)
	failed, failCode, failErr := ExecRunner(StdoutOnly(t.Context()), t.TempDir(), []string{"sh", "-c", "echo partial; echo why >&2; exit 3"})

	if !strings.Contains(mixed, "noise") {
		t.Errorf("combined output = %q, want stderr in it", mixed)
	}
	if clean != "out\n" || code != 0 || err != nil {
		t.Errorf("stdout only = %q, %d, %v; want \"out\\n\"", clean, code, err)
	}
	if failed != "why\npartial\n" || failCode != 3 || failErr != nil {
		t.Errorf("a failure = %q, %d, %v; want its stderr, then its stdout, and exit code 3", failed, failCode, failErr)
	}
}

func TestRunnersGiveTheEnvironmentTheyName(t *testing.T) {
	testutil.Require(t, "sh")
	t.Setenv("AF_OLD", "kept")
	t.Setenv("AF_TEST_TOKEN", "secret")
	t.Setenv("ANTHROPIC_BASE", "vendor")
	script := `echo "[$AF_OLD][$AF_NEW][$AF_TEST_TOKEN][$ANTHROPIC_BASE]"`
	sh, _ := exec.LookPath("sh") // the environment ExplicitEnvRunner gives may have no PATH
	for _, tc := range []struct {
		name string
		run  Runner
		want string
	}{
		{"ExecRunner, the process's", ExecRunner, "[kept][][secret][vendor]"},
		{"EnvRunner, the process's and more", EnvRunner("AF_NEW=added"), "[kept][added][secret][vendor]"},
		{"ReducedEnvRunner, without credentials", ReducedEnvRunner, "[kept][][][]"},
		{"ExplicitEnvRunner, only what it is given", ExplicitEnvRunner([]string{"AF_NEW=given"}), "[][given][][]"},
		{"ExplicitEnvRunner of nothing, an empty one", ExplicitEnvRunner(nil), "[][][][]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code, err := tc.run(t.Context(), t.TempDir(), []string{sh, "-c", script})
			if err != nil || code != 0 || strings.TrimSpace(out) != tc.want {
				t.Errorf("output %q, exit code %d, error %v; want %s", out, code, err, tc.want)
			}
		})
	}
}

// A deadline ends everything the command started, not only its direct child,
// whichever runner runs it: a grandchild that holds the output pipe would
// otherwise hold the run.
func TestADeadlineKillsEverythingTheCommandStarted(t *testing.T) {
	testutil.Require(t, "sh")
	sh, _ := exec.LookPath("sh")
	for name, run := range map[string]Runner{"ExecRunner": ExecRunner, "ExplicitEnvRunner": ExplicitEnvRunner([]string{"PATH=" + os.Getenv("PATH")})} {
		t.Run(name, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "pid")
			ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
			defer cancel()

			_, code, _ := run(ctx, t.TempDir(), []string{sh, "-c", "sleep 60 & echo $! > '" + pidFile + "'; wait"})

			if code == 0 {
				t.Error("a killed command exited 0")
			}
			pid, err := strconv.Atoi(strings.TrimSpace(testutil.ReadFile(t, pidFile)))
			if err != nil {
				t.Fatal(err)
			}
			testutil.Eventually(t, "the grandchild to be killed", func() bool { return !alive(pid) })
		})
	}
}

// A command that exits 0 and leaves a process holding its output succeeded.
func TestACommandThatLeavesAChildBehindSucceeds(t *testing.T) {
	testutil.Require(t, "sh")
	start := time.Now()

	out, code, err := ExecRunner(t.Context(), t.TempDir(), []string{"sh", "-c", "echo done; sleep 20 & exit 0"})

	if err != nil || code != 0 || out != "done\n" {
		t.Errorf("output %q, exit code %d, error %v; want a success", out, code, err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("the run took %s: it waited for the child", d)
	}
}
