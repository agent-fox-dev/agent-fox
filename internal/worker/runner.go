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
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
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
	// loopFlags, when it is set, gives flags put after the ones of the settings, which a test uses to
	// choose the scripted brain, that no variable may name.
	loopFlags func(a *attempt) []string
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
	return []stage{{"resolve", r.resolve}, {"session", r.openSession}, {"mark", r.mark}, {"clone", r.clone}, {"lease", r.lease}, {"run", run}}
}

// Run carries out the attempt: resolve, the session, the mark, the clone, the lease and the loop,
// and ends it with the mirror and the release of the lease. An attempt that has not ended
// by then has no envelope, which is a failure of the worker and not of the attempt.
func (r *Runner) Run(ctx context.Context, req jobs.Request) (jobs.Result, error) {
	a := &attempt{req: req, report: r.reporterOf(req), started: r.cfg.Now(), now: r.cfg.Now}
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

// finish ends the attempt, whatever it came to: the outcome is mirrored on the issue, the lease is
// released, the warnings, the side effects and the artifacts of the worker are added to the
// envelope it ended with, and what it made in the attempt directory is removed. The writes and the
// release are made on a context that a cancel or a shutdown does not reach, and that ends within
// the grace the jobs layer gives. An attempt that was cancelled returns a result, with the
// envelope of the loop or one that says it was cancelled; one that was shut down returns its
// cause and nothing else, since the layer hands it over again. A lease that cannot be released
// changes neither the status of the envelope nor the transient error, which are returned as they
// were; the failure is a warning of the envelope, or is logged when there is none.
func (r *Runner) finish(ctx context.Context, a *attempt, out *outcome) (jobs.Result, error) {
	how := stopOf(ctx)
	work := ctx
	if how != notStopped {
		var cancel context.CancelFunc
		work, cancel = context.WithTimeout(context.WithoutCancel(ctx), cleanupBound)
		defer cancel()
	}
	if how == stoppedByCancel {
		if len(out.result.Envelope) == 0 {
			*out = *r.cancelled(a, context.Cause(ctx))
		}
		out.err = nil
	}

	r.mirror(work, a, out, how)
	releaseErr := r.releaseLease(work, a)
	r.report(work, a, out)

	warnings := slices.Clone(a.warnings)
	if releaseErr != nil {
		r.cfg.Logger.Warn("the lease of the attempt could not be released", "workspace", a.req.Workspace, "job", a.req.JobID,
			"attempt", a.req.Attempt, "lease", a.lease.ID, "error", releaseErr.Error())
		warnings = append(warnings, toolio.Warning{Code: WarnLeaseReleaseFailed, Message: fmt.Sprintf(
			"the sandbox %s could not be released: %v; hub destroys it when it expires at %s", a.lease.ID, releaseErr, a.lease.ExpiresAt.UTC().Format(time.RFC3339))})
	}
	if len(out.result.Envelope) > 0 {
		if a.labelPrefixVar != "" && a.labelPrefixVar != a.prefix {
			warnings = append(warnings, toolio.Warning{Code: WarnLabelPrefixIgnored, Message: fmt.Sprintf(
				"AF_LABEL_PREFIX is %q and the prefix of the subscription is %q: the labels have the prefix of the subscription", a.labelPrefixVar, a.prefix)})
		}
		am := toolio.Amendment{Warnings: warnings, SideEffects: a.sideEffects}
		if a.sessionID != "" {
			am.Artifacts = append(am.Artifacts, toolio.Artifact{Kind: "hub_session", Name: a.sessionID})
		}
		if a.lease != nil {
			am.Artifacts = append(am.Artifacts, toolio.Artifact{Kind: "sandbox", Name: a.lease.ID})
		}
		patched, err := toolio.Amend(out.result.Envelope, am)
		if err != nil {
			r.cfg.Logger.Warn("the envelope of the attempt could not be amended", "job", a.req.JobID, "attempt", a.req.Attempt, "error", err.Error())
		} else {
			out.result.Envelope = patched
		}
	} else {
		for _, w := range a.warnings { // nothing carries them: they are logged
			r.cfg.Logger.Warn("a write of the attempt failed", "job", a.req.JobID, "attempt", a.req.Attempt, "code", w.Code, "message", w.Message)
		}
	}
	out.result.LabelsSet, out.result.LabelsRemoved = slices.Clone(a.labelsSet), slices.Clone(a.labelsRemoved)
	r.cleanup(ctx, a, out)

	if how == stoppedByShutdown {
		err := out.err
		if err == nil {
			err = context.Cause(ctx)
		}
		return jobs.Result{}, err
	}
	return out.result, out.err
}

// cleanup removes what the attempt made in its directory, once the lease is released: input/, and
// clone/ unless the attempt is to be handed over again or was shut down. The envelope, the report
// and the directory itself are the jobs layer's and are not touched.
func (r *Runner) cleanup(ctx context.Context, a *attempt, out *outcome) {
	remove := []string{filepath.Join(a.req.Dir, inputDirName)}
	if !keepsClone(ctx, out) {
		remove = append(remove, filepath.Join(a.req.Dir, cloneDirName))
	}
	for _, dir := range remove {
		if err := os.RemoveAll(dir); err != nil {
			r.cfg.Logger.Warn("a directory of the attempt could not be removed", "job", a.req.JobID, "attempt", a.req.Attempt, "dir", dir, "error", err.Error())
		}
	}
}
