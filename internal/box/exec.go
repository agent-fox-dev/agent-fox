package box

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// ExecOutputCap is the most output Exec returns: the last of it.
const ExecOutputCap = 64 << 10

// ExitTimeout is the exit code Exec reports for a command that was killed because
// its time ran out, as timeout(1) does.
const ExitTimeout = 124

// Exec runs a shell command in the tree, where a process of the box has it: through
// the box's execute tool, which runs it at the root of the workspace, with the
// environment the provider gives the tools of the box. It returns the exit code and
// the last ExecOutputCap bytes of what the command printed, the two streams together.
// A command that exits with a code other than 0 is not an error. One that is still
// running when timeout (when it is above 0) is over is killed, with the processes it
// started, and its code is ExitTimeout. A box counts whole seconds, rounded down, and
// at least one. The error is for a command that could not be run, and for a context
// that ended.
func (s *Session) Exec(ctx context.Context, command string, timeout time.Duration) (int, string, error) {
	args := map[string]any{"command": command}
	if timeout > 0 {
		args["timeout_s"] = max(1, int(timeout/time.Second))
	}
	r, err := s.call(ctx, toolExecute, args)
	if err != nil {
		return -1, "", &toolio.Failure{Category: toolio.CategorySandbox, Retryable: s.remote, Message: fmt.Sprintf("the %s box cannot run a command: %v", s.provider, err)}
	}
	if ctx.Err() != nil {
		return -1, "", ctx.Err()
	}
	output, _ := r.Data["output"].(string)
	outcome, _ := r.Data["outcome"].(string)
	code, hasCode := intOf(r.Data["exit_code"])
	switch {
	case outcome == "timeout":
		return ExitTimeout, tail(output, ExecOutputCap), nil
	case !hasCode:
		return -1, "", s.goneIf(resultGone(r), &toolio.Failure{Category: toolio.CategorySandbox, Retryable: s.remote, Message: fmt.Sprintf("the %s box could not run the command: %s %s", s.provider, r.Error, r.Detail)})
	}
	return code, tail(output, ExecOutputCap), nil
}

// tail is the last n bytes of s, cut at the start of a character.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}

// intOf is a number of a tool result, which is an int in the process and a float64
// once it has been through JSON.
func intOf(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	}
	return 0, false
}
