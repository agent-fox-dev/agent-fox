package box

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/core"
)

func TestExec_RunsTheCommandAtTheRootAndANonZeroExitIsACode(t *testing.T) {
	s := openLocal(t, hostRepo(t, nil), &ledger{})
	for _, tc := range []struct {
		command string
		code    int
		output  func(string) bool
	}{
		{"exit 3", 3, func(out string) bool { return out == "" }},
		{"echo hi; echo oops >&2", 0, func(out string) bool { return strings.HasPrefix(out, "hi\n") && strings.Contains(out, "oops") }},
		{"pwd", 0, func(out string) bool { return strings.TrimSpace(out) == s.Root() }},
	} {
		if code, out, err := s.Exec(context.Background(), tc.command, 0); code != tc.code || err != nil || !tc.output(out) {
			t.Errorf("%s = %d, %q, %v; want the code %d", tc.command, code, out, err, tc.code)
		}
	}
}

func TestExec_ACommandThatOutlivesItsTimeIsKilledAsATimeout(t *testing.T) {
	s := openLocal(t, hostRepo(t, nil), &ledger{})
	start := time.Now()

	code, _, err := s.Exec(context.Background(), "sleep 30", time.Second)

	if code != ExitTimeout || err != nil || time.Since(start) > 20*time.Second {
		t.Errorf("sleep 30 with a second = %d, %v after %s, want %d soon", code, err, time.Since(start), ExitTimeout)
	}
}

func TestExec_GivesTheToolTheTimeoutInWholeSecondsAndAtLeastOne(t *testing.T) {
	s := openLocal(t, hostRepo(t, nil), &ledger{})
	asked := stubTool(s, "execute", answer(core.ToolResult{OK: true, Data: map[string]any{"exit_code": 0}}))
	timeouts := map[time.Duration]any{0: nil, 100 * time.Millisecond: float64(1), 2500 * time.Millisecond: float64(2), 3 * time.Minute: float64(180)}

	for timeout, want := range timeouts {
		if _, _, err := s.Exec(context.Background(), "make test", timeout); err != nil {
			t.Fatal(err)
		}
		if got := (*asked)[len(*asked)-1]; got["timeout_s"] != want || got["command"] != "make test" {
			t.Errorf("a timeout of %s asked %v, want timeout_s %v and the command as given", timeout, got, want)
		}
	}
}

func TestExec_ReadsWhatTheToolAnswers(t *testing.T) {
	s := openLocal(t, hostRepo(t, nil), &ledger{})
	long := strings.Repeat("a", 3*ExecOutputCap) + "THE-END\n"
	for _, tc := range []struct {
		name   string
		result core.ToolResult
		code   int
		output string
	}{
		{"a timeout", core.ToolResult{Error: "command_timeout", Data: map[string]any{"output": "partial", "exit_code": -1, "outcome": "timeout"}}, ExitTimeout, "partial"},
		{"a signal", core.ToolResult{Error: "command_signal", Data: map[string]any{"output": "", "exit_code": float64(137), "outcome": "signal"}}, 137, ""},
		{"an output over the cap", core.ToolResult{OK: true, Data: map[string]any{"output": long, "exit_code": 0, "outcome": "ok"}}, 0, long[len(long)-ExecOutputCap:]},
	} {
		stubTool(s, "execute", answer(tc.result))
		if code, out, err := s.Exec(context.Background(), "x", 0); code != tc.code || out != tc.output || err != nil {
			t.Errorf("%s = %d, %d bytes, %v; want %d and %d bytes", tc.name, code, len(out), err, tc.code, len(tc.output))
		}
	}
}

func TestExec_ACommandThatCannotBeRunIsASandboxFailure(t *testing.T) {
	s := openLocal(t, hostRepo(t, nil), &ledger{})
	stubTool(s, "execute", answer(core.ErrResult("spawn_failed", "no shell")))

	_, _, err := s.Exec(context.Background(), "true", 0)
	if f := asFailure(t, err); f.Category != toolio.CategorySandbox || f.Retryable || f.Message != "the local box could not run the command: spawn_failed no shell" {
		t.Errorf("no exit code: %+v, want a sandbox failure with the reason", f)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stubTool(s, "execute", answer(core.ToolResult{OK: true, Data: map[string]any{"exit_code": 0}}))
	if _, _, err := s.Exec(ctx, "true", 0); !errors.Is(err, context.Canceled) {
		t.Errorf("an ended context: %v, want its error", err)
	}

	s.tools = nil
	_, _, err = s.Exec(context.Background(), "true", 0)
	if f := asFailure(t, err); !strings.Contains(f.Message, "cannot run a command: the box has no execute tool") {
		t.Errorf("no execute tool: %+v", f)
	}
}

func TestTail_IsTheLastBytesCutAtTheStartOfACharacter(t *testing.T) {
	for _, tc := range []struct {
		s    string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"abcdef", 3, "def"},
		{"aé", 1, ""},
		{"xéz", 3, "éz"},
		{"xéz", 2, "z"},
	} {
		if got := tail(tc.s, tc.n); got != tc.want {
			t.Errorf("tail(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
		}
	}
}

func TestIntOf_ReadsANumberAsTheProcessOrJSONHasIt(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want int
		ok   bool
	}{
		{3, 3, true}, {int64(4), 4, true}, {float64(5), 5, true}, {json.Number("6"), 6, true},
		{json.Number("6.5"), 0, false}, {"7", 0, false}, {nil, 0, false},
	} {
		if got, ok := intOf(tc.v); got != tc.want || ok != tc.ok {
			t.Errorf("intOf(%#v) = %d, %v; want %d, %v", tc.v, got, ok, tc.want, tc.ok)
		}
	}
}
