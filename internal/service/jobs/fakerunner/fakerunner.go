// Package fakerunner is a jobs.Runner for tests: each call does what a script says
// (return an envelope, return an error, return a transient failure, panic, or block
// until its context is cancelled or it is released), and every call's inputs are
// recorded. It needs no network, no hub, no key and no container engine, and the
// tests of the packages built on the layer use it as this layer's tests do.
package fakerunner

import (
	"context"
	"sync"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// Step is what one call of the runner does. The zero Step returns a done envelope.
type Step struct {
	result    *jobs.Result
	err       error
	panics    bool
	panicWith any
	block     bool
	ignoreCtx bool
	then      *Step
	fn        func(ctx context.Context, req jobs.Request) (jobs.Result, error)
}

// Return makes the call return the result.
func Return(res jobs.Result) Step { return Step{result: &res} }

// Envelope makes the call return the envelope (see Env) with no report.
func Envelope(envelope []byte) Step { return Return(jobs.Result{Envelope: envelope}) }

// Done makes the call return a done envelope.
func Done(mods ...func(*toolio.Envelope)) Step { return Envelope(Env(toolio.StatusDone, mods...)) }

// Failed makes the call return a failed envelope of the category.
func Failed(category string, retryable bool, mods ...func(*toolio.Envelope)) Step {
	return Envelope(Env(toolio.StatusFailed, append([]func(*toolio.Envelope){WithError(category, "the attempt failed: "+category, retryable)}, mods...)...))
}

// Fail makes the call return the error.
func Fail(err error) Step { return Step{err: err} }

// TransientFailure makes the call return the failure, marked as transient.
func TransientFailure(f *toolio.Failure) Step { return Step{err: jobs.Transient(f)} }

// Panic makes the call panic with the value.
func Panic(v any) Step { return Step{panics: true, panicWith: v} }

// Block makes the call wait until its context is cancelled or the call is released,
// and then do then; a zero then returns the cause of the cancellation as an error
// when the context was cancelled, and a done envelope when the call was released.
func Block(then Step) Step { return Step{block: true, then: &then} }

// BlockIgnoringContext makes the call wait until it is released, whatever happens to
// its context, and then do then.
func BlockIgnoringContext(then Step) Step { return Step{block: true, ignoreCtx: true, then: &then} }

// Do makes the call run fn and return what it returns.
func Do(fn func(ctx context.Context, req jobs.Request) (jobs.Result, error)) Step {
	return Step{fn: fn}
}

// Call is a call of the runner: the inputs, and the means to see how it stands.
type Call struct {
	// Request is what the call was given, besides its context.
	jobs.Request
	// Index is the position of the call among the calls, from 0.
	Index int
	// Started is when the call began.
	Started time.Time

	ctx context.Context
}

// Cause is the cause of the cancellation of the context of the call, or nil while it
// has not been cancelled.
func (c Call) Cause() error { return context.Cause(c.ctx) }

// Runner is the fake runner.
type Runner struct {
	mu         sync.Mutex
	steps      []Step
	fallback   Step
	calls      []Call
	released   []chan struct{}
	closed     []bool
	releaseAll bool
	active     int
	peak       int
}

// New returns a runner whose first call does the first step, the second the second,
// and so on; the calls after the last step return a done envelope.
func New(steps ...Step) *Runner {
	return &Runner{steps: steps}
}

// Then sets what the calls after the steps do.
func (r *Runner) Then(step Step) *Runner {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fallback = step
	return r
}

// zero reports whether the step is the zero Step.
func (s Step) zero() bool {
	return s.result == nil && s.err == nil && !s.panics && !s.block && s.fn == nil
}

// Run is the call: it records its inputs and does the step it is scripted to.
func (r *Runner) Run(ctx context.Context, req jobs.Request) (jobs.Result, error) {
	r.mu.Lock()
	index := len(r.calls)
	step := r.fallback
	if index < len(r.steps) {
		step = r.steps[index]
	}
	release := make(chan struct{})
	r.calls = append(r.calls, Call{Request: req, Index: index, Started: time.Now(), ctx: ctx})
	r.released = append(r.released, release)
	r.closed = append(r.closed, false)
	if r.releaseAll {
		close(release)
		r.closed[index] = true
	}
	r.active++
	r.peak = max(r.peak, r.active)
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		r.active--
		r.mu.Unlock()
	}()
	return r.perform(ctx, req, step, release)
}

// perform does the step.
func (r *Runner) perform(ctx context.Context, req jobs.Request, step Step, release <-chan struct{}) (jobs.Result, error) {
	switch {
	case step.panics:
		panic(step.panicWith)
	case step.block:
		cancelled := false
		if step.ignoreCtx {
			<-release
		} else {
			select {
			case <-ctx.Done():
				cancelled = true
			case <-release:
			}
		}
		then := Step{}
		if step.then != nil {
			then = *step.then
		}
		if cancelled && then.zero() {
			return jobs.Result{}, context.Cause(ctx)
		}
		return r.perform(ctx, req, then, release)
	case step.fn != nil:
		return step.fn(ctx, req)
	case step.err != nil:
		return jobs.Result{}, step.err
	case step.result != nil:
		return *step.result, nil
	}
	return jobs.Result{Envelope: Env(toolio.StatusDone)}, nil
}

// Calls are the calls so far, in order.
func (r *Runner) Calls() []Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Call(nil), r.calls...)
}

// Wait waits until at least n calls have begun, and says whether they did within d.
func (r *Runner) Wait(n int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		r.mu.Lock()
		have := len(r.calls)
		r.mu.Unlock()
		if have >= n {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

// Release releases the call with the index, if it is blocked.
func (r *Runner) Release(index int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if index >= 0 && index < len(r.released) && !r.closed[index] {
		close(r.released[index])
		r.closed[index] = true
	}
}

// ReleaseAll releases every call that is blocked, now and later.
func (r *Runner) ReleaseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.releaseAll = true
	for i, ch := range r.released {
		if !r.closed[i] {
			close(ch)
			r.closed[i] = true
		}
	}
}

// Active is how many calls have begun and not returned.
func (r *Runner) Active() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

// MaxConcurrent is the most calls there have been in the runner at once.
func (r *Runner) MaxConcurrent() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.peak
}
