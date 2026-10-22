// Package worker carries out one attempt of a job against a subscribed hub workspace: it is the
// production Runner of the jobs layer. It reads the PAT of the subscription, the record of the
// workspace and its variables from hub, refuses what cannot run, opens a hub session, and runs the
// triage or fix loop of the af commands in this process against hub's forge, a clone from hub's
// git server, and a sandbox that hub leased, with nothing but the PAT and the token of the lease.
// Infrastructure failures come back as transient errors, never as failed envelopes.
package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

// CategoryHub is the category of the failure of a hub that is unavailable. The toolio categories
// are open strings, so it needs no mechanism of its own.
const CategoryHub = "hub"

// defaultMaxAttempts is the most attempts a round may make when the variables did not say.
const defaultMaxAttempts = 2

// Subscriptions is the store of the subscriptions, as the worker reads it: the subscription of a
// workspace, for its label prefix, and its token.
type Subscriptions interface {
	Get(ctx context.Context, slug string) (store.Subscription, error)
	Token(ctx context.Context, slug string) (string, error)
}

// Config is what a Runner is made of.
type Config struct {
	// Hub is the client of hub's API, and Subscriptions the store of the tokens.
	Hub           *hub.Client
	Subscriptions Subscriptions
	// Logger receives what is not reported to the job; the default logger when nil.
	Logger *slog.Logger
	// Version, Commit and BuildTime identify this af, for the comparison with the af of a box.
	Version, Commit, BuildTime string
	// Now is the clock and Rand the source of the random part of run ids; the real ones when nil.
	Now  func() time.Time
	Rand io.Reader
}

// Runner is the worker. It holds nothing of an attempt: every attempt is one call of Run, and
// attempts may run side by side.
type Runner struct {
	cfg Config

	// The seams below are for the tests of this package: they are not Config's, so that nothing
	// outside the package can set them. A brain that is scripted needs a script file on the
	// worker, which no hub variable may provide, so only a test can choose it.
	reporter reporter
	// gitWrap makes the runner of a git environment; nil is gitx.ExplicitEnvRunner. A test
	// puts a recorder in between.
	gitWrap func(env []string) gitx.Runner
	// cloneTimeout bounds the clone, the default one when it is 0, and sleep waits between the
	// tries of a call, the clock when it is nil.
	cloneTimeout time.Duration
	sleep        func(ctx context.Context, d time.Duration) error
	// releaseTimeout bounds the release of the lease, defaultReleaseTimeout when it is 0.
	releaseTimeout time.Duration
	// extraFlags are flags put after the ones of the settings, which a test uses to choose the
	// scripted brain, that no variable may name.
	extraFlags []string
	// loop, when it is set, is the step that stands for the loop in a test; nil is the loop.
	loop func(ctx context.Context, a *attempt) *outcome
}

var _ jobs.Runner = (*Runner)(nil)

// New makes the Runner.
func New(cfg Config) *Runner {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Runner{cfg: cfg}
}

// reporter is where the worker reports while an attempt runs: the events of the attempt and the
// references of its session on hub. A *jobs.Recorder is one.
type reporter interface {
	Emit(typ string, data any)
	Hub(sessionID, runID, sandboxID string)
}

// discard is the reporter of an attempt that was given none.
type discard struct{}

func (discard) Emit(string, any)           {}
func (discard) Hub(string, string, string) {}

// reporterOf is what the attempt reports to.
func (r *Runner) reporterOf(req jobs.Request) reporter {
	switch {
	case r.reporter != nil:
		return r.reporter
	case req.Recorder != nil:
		return req.Recorder
	}
	return discard{}
}

// outcome is how an attempt ends before its pipeline has run to the end: a result with an
// envelope, or an error, which is a transient error for an infrastructure failure.
type outcome struct {
	result jobs.Result
	err    error
}

// stage is a step of the pipeline of an attempt. It returns nil to go on, and otherwise the
// outcome that ends the attempt.
type stage struct {
	name string
	run  func(context.Context, *attempt) *outcome
}

// stages are the steps of an attempt, in order.
func (r *Runner) stages() []stage {
	run := r.run
	if r.loop != nil {
		run = r.loop
	}
	return []stage{{"resolve", r.resolve}, {"session", r.openSession}, {"clone", r.clone}, {"lease", r.lease}, {"run", run}}
}

// Run carries out the attempt: resolve, the session, the clone and the lease. An attempt that has not ended
// by then has no envelope, which is a failure of the worker and not of the attempt.
func (r *Runner) Run(ctx context.Context, req jobs.Request) (jobs.Result, error) {
	a := &attempt{req: req, report: r.reporterOf(req), started: r.cfg.Now()}
	defer a.close()
	return r.finish(ctx, a, r.pipeline(ctx, a))
}

// pipeline runs the steps of the attempt until one ends it, and returns how it ended.
func (r *Runner) pipeline(ctx context.Context, a *attempt) *outcome {
	for _, s := range r.stages() {
		if out := s.run(ctx, a); out != nil {
			return out
		}
	}
	return &outcome{err: errors.New("worker: the attempt ended without an envelope")}
}
