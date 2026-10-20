package toolio

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// defaultGrace is how long a command that has been told to stop gets to
// return before the shell gives up on it.
const defaultGrace = 10 * time.Second

func (a *App) grace() time.Duration {
	if a.Grace > 0 {
		return a.Grace
	}
	return defaultGrace
}

// commandResult is what a command returned.
type commandResult struct {
	out Outcome
	err error
}

// execute runs the command under sigCtx, which a signal cancels, and under
// the deadline timeout sets when it is positive. A command that is told to
// stop, by the signal or by the deadline, has the grace period to return;
// after that the shell stops waiting for it and answers for it, so that the
// process always ends with an envelope. A command that returns an error
// after a signal is reported as cancelled, whatever the error was; one that
// returns an outcome has finished its job. A command that returns after its
// deadline ran out is reported as timed out, whatever it returned.
func (a *App) execute(sigCtx context.Context, cmd *Command, run *Run, timeout time.Duration) (Outcome, error) {
	ctx := sigCtx
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(sigCtx, timeout)
		defer cancel()
	}
	run.Ctx = ctx

	done := make(chan commandResult, 1)
	go func() {
		out, err := call(cmd, run)
		done <- commandResult{out, err}
	}()

	var res commandResult
	select {
	case res = <-done:
	case <-ctx.Done():
		grace := time.NewTimer(a.grace())
		defer grace.Stop()
		select {
		case res = <-done:
		case <-grace.C:
			return Outcome{}, stopped(sigCtx, ctx, timeout, a.grace(), true)
		}
	}

	var panicked *panicError
	if errors.As(res.err, &panicked) {
		return res.out, res.err
	}
	if f := stopped(sigCtx, ctx, timeout, a.grace(), false); f != nil && (res.err != nil || f.Category == CategoryTimeout) {
		return Outcome{}, f
	}
	return res.out, res.err
}

// stopped is the failure for a command that was told to stop, or nil when it
// was not. A signal makes it cancelled; the deadline, timeout.
func stopped(sigCtx, ctx context.Context, timeout, grace time.Duration, abandoned bool) *Failure {
	switch {
	case sigCtx.Err() != nil:
		msg := "the command was cancelled"
		if abandoned {
			msg = fmt.Sprintf("the command did not stop within %s of being cancelled and was abandoned", grace)
		}
		return &Failure{Category: CategoryCancelled, Message: msg}
	case ctx.Err() != nil:
		msg := fmt.Sprintf("the command did not finish within %s", timeout)
		if abandoned {
			msg += fmt.Sprintf(" or stop within %s of the deadline and was abandoned", grace)
		}
		return &Failure{
			Category: CategoryTimeout,
			Message:  msg,
			FixHint:  &FixHint{Flag: "--timeout", Suggest: doubled(timeout).String()},
		}
	}
	return nil
}

// doubled is twice d, or d if that would overflow.
func doubled(d time.Duration) time.Duration {
	if d > 0 && d*2 < d {
		return d
	}
	return d * 2
}

// lineBreaks turns every line break into a space.
var lineBreaks = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ")

// oneLine is s on a single line.
func oneLine(s string) string {
	return strings.TrimSpace(lineBreaks.Replace(s))
}

// defaultSummary describes a failure whose message was empty, so that a
// status the shell set always has a summary.
func defaultSummary(category string) string {
	switch category {
	case CategoryUsage:
		return "the invocation is wrong"
	case CategoryInternal:
		return "an internal error stopped the command"
	case CategoryCancelled:
		return "the command was cancelled"
	case CategoryTimeout:
		return "the command timed out"
	}
	return "the command failed: " + category
}
