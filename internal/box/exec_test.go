package box

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/core"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// execStub replaces the execute tool of a session with one that notes what it was
// asked and answers with the result.
func execStub(s *Session, answer func(args map[string]any) core.ToolResult) *[]map[string]any {
	var asked []map[string]any
	for i, tool := range s.tools {
		if tool.Name == "execute" {
			tool.Execute = func(_ context.Context, in json.RawMessage) core.ToolResult {
				var args map[string]any
				_ = json.Unmarshal(in, &args)
				asked = append(asked, args)
				return answer(args)
			}
			s.tools[i] = tool
		}
	}
	return &asked
}

// TestExecReturnsTheExitCodeAndCappedOutputAndANonZeroExitIsNotAnError_TS_10_38 verifies
// TS-10-38 for a box provider: the command runs through the execute tool, whose
// working directory is the root of the workspace, the exit code is a code and not an
// error, and the output is the last ExecOutputCap bytes of it.
// Verifies: 10-REQ-7.1
func TestExecReturnsTheExitCodeAndCappedOutputAndANonZeroExitIsNotAnError_TS_10_38(t *testing.T) {
	s := open(t, Default, "local", newRepo(t, 1, "main"), &ledger{})

	code, out, err := s.Exec(context.Background(), "exit 3", 0)
	if code != 3 || err != nil || out != "" {
		t.Errorf("exit 3 = %d, %q, %v; want the code 3, no output and no error", code, out, err)
	}
	code, out, err = s.Exec(context.Background(), "echo hi; echo oops >&2", 0)
	if code != 0 || err != nil || !strings.HasPrefix(out, "hi\n") || !strings.Contains(out, "oops") {
		t.Errorf("echo = %d, %q, %v; want the output of both streams", code, out, err)
	}
	code, out, err = s.Exec(context.Background(), "pwd", 0)
	if code != 0 || err != nil || strings.TrimSpace(out) != s.Root() {
		t.Errorf("pwd = %d, %q, %v; want the root of the workspace %q", code, out, err, s.Root())
	}

	// A command that prints more than the cap: the tail is what is kept.
	execStub(s, func(map[string]any) core.ToolResult {
		big := strings.Repeat("a", 3*ExecOutputCap) + "THE-END\n"
		return core.ToolResult{OK: true, Data: map[string]any{"output": big, "exit_code": float64(0), "outcome": "ok"}}
	})
	code, out, err = s.Exec(context.Background(), "print a lot", 0)
	if code != 0 || err != nil || len(out) != ExecOutputCap || !strings.HasSuffix(out, "THE-END\n") {
		t.Errorf("a long output = %d, %d bytes ending %q, %v; want the last %d bytes", code, len(out), out[max(0, len(out)-10):], err, ExecOutputCap)
	}
}

// TestExecPassesTheCommandAndTheTimeoutToTheExecuteTool guards how a box provider runs
// the command: as given, with the timeout in whole seconds rounded down and at least
// one, none for a command with no timeout, and a command that the tool says it killed
// for its time is the code of a timeout.
func TestExecPassesTheCommandAndTheTimeoutToTheExecuteTool(t *testing.T) {
	s := open(t, Default, "local", newRepo(t, 1, "main"), &ledger{})
	asked := execStub(s, func(args map[string]any) core.ToolResult {
		if args["command"] == "slow" {
			return core.ToolResult{OK: false, Error: "command_timeout", Data: map[string]any{"output": "partial", "exit_code": float64(-1), "outcome": "timeout"}}
		}
		return core.ToolResult{OK: false, Error: "command_signal", Data: map[string]any{"output": "", "exit_code": float64(137), "outcome": "signal"}}
	})

	code, out, err := s.Exec(context.Background(), "slow", 1500*time.Millisecond)
	if code != ExitTimeout || out != "partial" || err != nil {
		t.Errorf("a timeout = %d, %q, %v; want %d, the output so far and no error", code, out, err, ExitTimeout)
	}
	for _, timeout := range []time.Duration{0, 100 * time.Millisecond, 2500 * time.Millisecond, 3 * time.Minute} {
		if code, _, err := s.Exec(context.Background(), "killed", timeout); code != 137 || err != nil {
			t.Errorf("a command killed by a signal = %d, %v, want its 137", code, err)
		}
	}

	want := []any{float64(1), nil, float64(1), float64(2), float64(180)}
	if len(*asked) != len(want) {
		t.Fatalf("the execute tool was asked %d times, want %d", len(*asked), len(want))
	}
	for i, got := range *asked {
		if got["timeout_s"] != want[i] {
			t.Errorf("ask %d: %v, want timeout_s %v", i, got, want[i])
		}
	}
	if (*asked)[0]["command"] != "slow" {
		t.Errorf("the execute tool was asked %v, want the command as it is", (*asked)[0])
	}
}

// TestExecOfATimeoutKillsTheCommandInTheBox runs a real command that outlives its time.
func TestExecOfATimeoutKillsTheCommandInTheBox(t *testing.T) {
	s := open(t, Default, "local", newRepo(t, 1, "main"), &ledger{})
	start := time.Now()

	code, _, err := s.Exec(context.Background(), "sleep 30", time.Second)

	if code != ExitTimeout || err != nil {
		t.Errorf("sleep 30 with a second = %d, %v, want the code of a timeout", code, err)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("Exec took %s for a second", d)
	}
}

// TestExecFailsAsASandboxFailureWhenTheToolCannotRun guards a result with no exit code:
// the box could not run the command, which is an error and not a code.
func TestExecFailsAsASandboxFailureWhenTheToolCannotRun(t *testing.T) {
	s := open(t, Default, "local", newRepo(t, 1, "main"), &ledger{})
	execStub(s, func(map[string]any) core.ToolResult {
		return core.ErrResult("sandbox_unavailable", "the box is gone")
	})

	_, _, err := s.Exec(context.Background(), "true", 0)

	f := asFailure(t, err)
	if f.Category != toolio.CategorySandbox || !strings.Contains(f.Message, "the box is gone") {
		t.Errorf("failure = %+v, want a sandbox failure with the reason", f)
	}
}

// TestExecOnTheCheckoutRunsShAtTheCheckoutWithTheReducedEnvironment_TS_10_38 verifies the
// none half of TS-10-38: the command runs with sh -c at the checkout, with no host
// credential but the ones the spec names.
// Verifies: 10-REQ-7.1
func TestExecOnTheCheckoutRunsShAtTheCheckoutWithTheReducedEnvironment_TS_10_38(t *testing.T) {
	dir := newRepo(t, 1, "main")
	t.Setenv("GITHUB_TOKEN", "ghp-host")
	t.Setenv("ANTHROPIC_API_KEY", "sk-host")
	t.Setenv("AF_EXEC_PLAIN", "plain")
	s := openNone(t, dir, &ledger{}, map[string]string{"ANTHROPIC_API_KEY": "sk-spec"})

	code, out, err := s.Exec(context.Background(), `pwd; echo "[$ANTHROPIC_API_KEY][$GITHUB_TOKEN][$AF_EXEC_PLAIN]"; exit 3`, 0)

	if code != 3 || err != nil {
		t.Errorf("exit = %d, %v, want 3 and no error", code, err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || lines[0] != dir || lines[1] != "[sk-spec][][plain]" {
		t.Errorf("output = %q, want the checkout, the key of the spec (not the host's), no forge token, and the plain variable", out)
	}

	execBig, _, err := func() (int, string, error) {
		c, o, e := s.Exec(context.Background(), `i=0; while [ $i -lt 5000 ]; do echo "line-$i-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"; i=$((i+1)); done; echo THE-END`, 0)
		return len(o), o, func() error {
			if c != 0 {
				return errors.New("exit code")
			}
			return e
		}()
	}()
	if err != nil || execBig != ExecOutputCap {
		t.Errorf("a long output has %d bytes (%v), want the last %d", execBig, err, ExecOutputCap)
	}

	start := time.Now()
	code, _, err = s.Exec(context.Background(), "sleep 30 & wait", 700*time.Millisecond)
	if code != ExitTimeout || err != nil || time.Since(start) > 15*time.Second {
		t.Errorf("a timeout = %d, %v after %s, want the code of a timeout, soon", code, err, time.Since(start))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.Exec(ctx, "true", 0); err == nil {
		t.Error("Exec with a cancelled context succeeded")
	}
}
