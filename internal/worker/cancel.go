package worker

import (
	"context"
	"errors"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// cleanupBound is how long the writes to the forge, the release of the lease and the report to
// hub may take after the attempt was stopped, so that they are done within the grace of the jobs
// layer, which is a minute.
const cleanupBound = 45 * time.Second

// finishBound is how long the writes, the release and the report of an attempt that was not
// stopped may take in all: each call to hub has a bound of its own, and this is for the sum.
const finishBound = 5 * time.Minute

// stop says how the context of the attempt ended, if it did.
type stop int

const (
	notStopped stop = iota
	// stoppedByCancel is a cancel, and an unsubscribe, which is read as one.
	stoppedByCancel
	// stoppedByIssueClosed is a cancel because the issue was closed: the attempt stops as for any
	// cancel, and puts no label back.
	stoppedByIssueClosed
	// stoppedByShutdown is the shutdown of the service, after which the attempt is handed over again.
	stoppedByShutdown
	// stoppedOtherwise is any other end of the context.
	stoppedOtherwise
)

// stopOf says how the context of the attempt ended.
func stopOf(ctx context.Context) stop {
	if ctx.Err() == nil {
		return notStopped
	}
	switch cause := context.Cause(ctx); {
	case errors.Is(cause, jobs.ErrIssueClosed):
		return stoppedByIssueClosed
	case errors.Is(cause, jobs.ErrCancelled), errors.Is(cause, jobs.ErrUnsubscribed):
		return stoppedByCancel
	case errors.Is(cause, jobs.ErrShutdown):
		return stoppedByShutdown
	}
	return stoppedOtherwise
}

// cancel says that the attempt was stopped by a cancel of any kind.
func (s stop) cancel() bool { return s == stoppedByCancel || s == stoppedByIssueClosed }

// cancelled is the outcome of an attempt that was cancelled before the loop made an envelope: a
// failed envelope of category cancelled, built as the envelope of a refusal is.
func (r *Runner) cancelled(a *attempt, cause error) *outcome {
	message := "the attempt was cancelled before the loop ran to its end"
	switch {
	case errors.Is(cause, jobs.ErrIssueClosed):
		message = "the issue was closed before the loop ran to its end"
	case errors.Is(cause, jobs.ErrUnsubscribed):
		message = "the workspace was unsubscribed before the loop ran to its end"
	}
	return r.envelopeOf(a, &refusal{category: toolio.CategoryCancelled, message: message})
}
