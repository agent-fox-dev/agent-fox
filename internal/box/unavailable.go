package box

import (
	"errors"

	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/gateway"
)

// ErrUnavailable marks an error whose cause is a sandbox that is gone or cannot be reached: the tool
// error code sandbox_unavailable, a transport failure that made agentkit's sandbox unavailable, or a
// gateway that refuses the token of a lease that has ended. A caller finds it with errors.Is; the
// error it marks is otherwise what it was, in its category, its retryable flag and its message.
var ErrUnavailable = errors.New("box: the sandbox is gone or cannot be reached")

// toolGone is the error code of a tool of a box whose sandbox is gone.
const toolGone = "sandbox_unavailable"

// goneError is an error that matches ErrUnavailable and is the error it wraps in every other way:
// its text, and the *toolio.Failure that errors.As finds in it.
type goneError struct{ err error }

func (e *goneError) Error() string        { return e.err.Error() }
func (e *goneError) Unwrap() error        { return e.err }
func (e *goneError) Is(target error) bool { return target == ErrUnavailable }

// gone marks the error as one that matches ErrUnavailable.
func gone(err error) error {
	if err == nil {
		return nil
	}
	return &goneError{err}
}

// goneIf marks the error when the cause says the sandbox is gone.
func goneIf(cause bool, err error) error {
	if cause {
		return gone(err)
	}
	return err
}

// isGone says whether an error of an operation of the box says the sandbox is gone or cannot be
// reached.
func isGone(err error) bool {
	return errors.Is(err, sandbox.ErrUnavailable) || errors.Is(err, gateway.ErrUnauthenticated)
}

// resultGone says whether the result of a tool says the sandbox is gone.
func resultGone(r core.ToolResult) bool { return !r.OK && r.Error == toolGone }
