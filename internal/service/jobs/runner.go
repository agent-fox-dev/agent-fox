package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Runner carries out one attempt of a job. It is the seam between this layer and the
// attempt pipeline of the specs that follow (the hub session, the lease, the clone,
// the loops, the forge writes); this spec ships a placeholder runner and, in the
// package fakerunner, one that tests script.
//
// A runner returns either a Result, which carries the envelope of what the attempt
// came to, or an error. An error that the runner marks with Transient asks for the
// same attempt to be handed over again after a wait; any other error ends the attempt
// as failed.
//
// What every runner owes this layer, and what the tests of the worker check:
//
//  1. One attempt is one brain-level attempt. The runner runs the brain's loop once
//     per attempt (for a fix, the loop with one attempt in the run, its rounds
//     included), and does not start another brain-level attempt after a failure:
//     further attempts belong to this layer, bounded by the MaxAttempts it returns.
//  2. Infrastructure failures are transient errors, not failed envelopes: a box or a
//     lease that cannot be had, synced or exported, hub being unavailable or answering
//     5xx, a forge rate limit or a lost forge connection. A failed envelope says what
//     a brain-level attempt came to.
//  3. The runner reads the token of the workspace when the attempt starts and keeps it
//     for the life of the attempt, its cleanup included. An unsubscribe removes the
//     subscription while a cancelled attempt may still be cleaning up.
//  4. What the runner does when it is stopped depends on why: see IsCancel. Stopped for
//     a cancel, it releases what it holds and restores the request label within the
//     cancel grace. Stopped for a shutdown, it leaves the attempt resumable, and what
//     it returns is discarded.
type Runner interface {
	Run(ctx context.Context, req Request) (Result, error)
}

// Request is what a runner is given for an attempt, besides its context. The context
// is cancelled when the attempt has to stop, with a cause that says why: ErrCancelled
// for a cancel, ErrUnsubscribed for an unsubscribe (which is a cancel as well), and ErrShutdown for a shutdown (which is not).
type Request struct {
	JobID string
	// Attempt is the number of the attempt, from 1.
	Attempt int
	// Delivery is how many times this attempt has been handed to a runner, this
	// time included: 1 on the first. A handover after a shutdown or a crash counts.
	Delivery int
	// Round is the position of this attempt in its round, from 1: the attempts that follow one
	// another by themselves, because the one before failed in a way that is tried again, are a
	// round, and an answer, a retry or the first attempt of a job begins one.
	Round     int
	Workspace string
	Kind      Kind
	// Input is what the job was asked about, without the context.
	Input Input
	// Context is the strings of context of the request.
	Context []string
	// Answers are the questions answered so far, in order, each with the options it
	// offered and the answer.
	Answers []Answer
	Source  Source
	// RequestLabel is the label the trigger consumed when it made the job, or "".
	RequestLabel string
	// Dir is the absolute directory of the attempt, attempts/<job id>/<n>/ beside
	// the database. It exists.
	Dir string
	// Recorder is where the runner reports while it runs.
	Recorder *Recorder
}

// Result is what a runner returns when it has an envelope.
type Result struct {
	// Envelope is the JSON of the envelope of the attempt, as the command wrote it.
	// It is stored as it is.
	Envelope json.RawMessage
	// Report is the text of the report of the attempt, or "" when there is none.
	Report string
	// LabelsSet and LabelsRemoved are the labels the attempt set on the issue and
	// removed from it.
	LabelsSet, LabelsRemoved []string
	// MaxAttempts is the most attempts a round may make: 0 means 2, and the layer
	// keeps it between 1 and 10.
	MaxAttempts int
}

// The causes of the cancellation of the context of an attempt. IsCancel tells them
// apart.
var (
	// ErrCancelled: a person cancelled the job.
	ErrCancelled = errors.New("the job was cancelled")
	// ErrUnsubscribed: the workspace is being unsubscribed. It is a cancel as well.
	ErrUnsubscribed = fmt.Errorf("the workspace was unsubscribed: %w", ErrCancelled)
	// ErrShutdown: the service is stopping. It is not a cancel: the attempt is to be
	// left as it is, to be handed over again after the restart.
	ErrShutdown = errors.New("the service is shutting down")
)

// causeName is the cause of a cancel as the job document shows it: unsubscribed or cancelled.
func causeName(cause error) string {
	if errors.Is(cause, ErrUnsubscribed) {
		return "unsubscribed"
	}
	return "cancelled"
}

// cancelledMessage is what the envelope of an attempt that was cancelled says.
const cancelledMessage = "the job was cancelled"

// IsCancel reports whether the cause of the cancellation of an attempt's context
// (context.Cause) is a cancel or an unsubscribe, which a runner handles alike, and
// not a shutdown or no cancellation at all.
func IsCancel(cause error) bool { return errors.Is(cause, ErrCancelled) }

// transientError marks an error of a runner as one that asks for the same attempt to
// be handed over again.
type transientError struct{ err error }

func (e *transientError) Error() string { return e.err.Error() }
func (e *transientError) Unwrap() error { return e.err }

// Transient marks err as a failure of the infrastructure that the same attempt is to
// be handed over again for, after a wait. The error is still found with errors.As
// (a *toolio.Failure in it keeps its category, message and retryable flag).
func Transient(err error) error {
	if err == nil {
		return nil
	}
	return &transientError{err: err}
}

// IsTransient reports whether the error was marked with Transient.
func IsTransient(err error) bool {
	_, ok := errors.AsType[*transientError](err)
	return ok
}
