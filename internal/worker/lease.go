package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The warnings the worker adds to the envelope of an attempt.
const (
	// WarnLeaseShortenedTimeout says the timeout of the loop was cut to fit the lease.
	WarnLeaseShortenedTimeout = "lease_shortened_timeout"
	// WarnLeaseReleaseFailed says the lease could not be released, which leaves it to end by itself.
	WarnLeaseReleaseFailed = "lease_release_failed"
)

const (
	// leaseMargin is how long before the lease ends the loop has to be over, so that what follows
	// it, the mirror to the issue and the report, is done while the lease is held.
	leaseMargin = 2 * time.Minute
	// defaultReleaseTimeout bounds the release of the lease, which is made on a context that the
	// cancellation of the attempt does not reach.
	defaultReleaseTimeout = 30 * time.Second
)

// lease leases a sandbox of hub for the attempt, once the settings, the mode, the input and the
// session have passed and the clone is made: a request {purpose, labels: {job, attempt}} with the
// profile of AF_SANDBOX_PROFILE only when that is set. The recorder is told the sandbox. The box
// itself is made by the loop, in the registry of the attempt, which holds the gateway of the lease
// as the only provider it has.
//
// A lease that hub refuses with 401 or 403 is the credential and ends the attempt with an
// envelope. Any other answer, no answer, and a lease with less than two minutes left are the
// infrastructure: a transient error of category sandbox, with the reason that hub gave. A lease
// that expires before the timeout of the loop would end shortens that timeout to end two minutes
// before it, and the envelope says so.
func (r *Runner) lease(ctx context.Context, a *attempt) *outcome {
	a.step("lease")
	l, err := r.cfg.Hub.Lease(ctx, a.pat, a.req.Workspace, hub.LeaseRequest{
		Profile: a.settings.Profile,
		Purpose: string(a.req.Kind),
		Labels:  map[string]string{"job": a.req.JobID, "attempt": strconv.Itoa(a.req.Attempt)},
	})
	if err != nil {
		return r.leaseFailure(ctx, a, err)
	}
	a.lease = &l
	a.onClose(func() { _ = r.releaseLease(ctx, a) })
	a.report.Hub(a.sessionID, a.runID, l.ID)

	a.boxes = box.NewRegistry()
	a.boxes.Register(box.GatewayEntry(l.GatewayURL, l.Token))
	a.boxLabels = map[string]string{"af.job": a.req.JobID, "af.attempt": strconv.Itoa(a.req.Attempt)}

	left := l.ExpiresAt.Sub(r.cfg.Now())
	usable := (left - leaseMargin).Truncate(time.Second)
	if usable < time.Second {
		return transientFailure(toolio.CategorySandbox, fmt.Sprintf("hub leased a sandbox that expires at %s, with less than 2 minutes left: not enough to run the loop", l.ExpiresAt.UTC().Format(time.RFC3339)))
	}
	if a.settings.Timeout > usable {
		a.warnings = append(a.warnings, toolio.Warning{Code: WarnLeaseShortenedTimeout, Message: fmt.Sprintf(
			"AF_TIMEOUT %s would end after the lease: the timeout is cut to %s, so that the attempt ends 2 minutes before the lease expires at %s",
			a.settings.Timeout, usable, l.ExpiresAt.UTC().Format(time.RFC3339))})
		a.settings.Timeout = usable
	}
	return nil
}

// leaseFailure is the outcome of a lease that hub did not grant.
func (r *Runner) leaseFailure(ctx context.Context, a *attempt, err error) *outcome {
	if ctx.Err() != nil {
		return &outcome{err: context.Cause(ctx)}
	}
	if errors.Is(err, hub.ErrRefused) || errors.Is(err, hub.ErrForbidden) {
		return r.envelopeOf(a, authRefusal(a.req.Workspace, "hub does not take the token to lease a sandbox"))
	}
	reason := err.Error()
	var rejected *hub.RejectedError
	switch {
	case errors.As(err, &rejected):
		reason = fmt.Sprintf("hub refused the lease (%d): %s", rejected.Status, rejected.Message)
	case errors.Is(err, hub.ErrNotFound):
		reason = "hub does not find the workspace or the profile of the sandbox"
	}
	return transientFailure(toolio.CategorySandbox, "hub did not lease a sandbox: "+reason)
}

// releaseLease releases the lease of the attempt, once, whichever path gets here first: the end
// of the attempt, or the unwinding of a panic. It is made on a context that the cancellation of
// the attempt does not reach, bounded to 30 seconds, and after the box was closed, which the loop
// does before it returns. An attempt with no lease has nothing to release.
func (r *Runner) releaseLease(ctx context.Context, a *attempt) error {
	a.releaseOnce.Do(func() {
		if a.lease == nil {
			return
		}
		a.step("release")
		release, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.releaseBound())
		defer cancel()
		a.releaseErr = r.cfg.Hub.ReleaseLease(release, a.pat, a.req.Workspace, a.lease.ID)
	})
	return a.releaseErr
}

// releaseBound is how long the release of the lease may take.
func (r *Runner) releaseBound() time.Duration {
	if r.releaseTimeout > 0 {
		return r.releaseTimeout
	}
	return defaultReleaseTimeout
}

// finish ends the attempt: the lease is released, and the warnings of the attempt are added to the
// envelope it ended with, the failure to release the lease among them. A lease that cannot be
// released changes neither the status of the envelope nor the transient error, which are returned
// as they were; the failure is logged.
func (r *Runner) finish(ctx context.Context, a *attempt, out *outcome) (jobs.Result, error) {
	warnings := slices.Clone(a.warnings)
	if err := r.releaseLease(ctx, a); err != nil {
		r.cfg.Logger.Warn("the lease of the attempt could not be released", "workspace", a.req.Workspace, "job", a.req.JobID,
			"attempt", a.req.Attempt, "lease", a.lease.ID, "error", err.Error())
		warnings = append(warnings, toolio.Warning{Code: WarnLeaseReleaseFailed, Message: fmt.Sprintf(
			"the sandbox %s could not be released: %v; hub destroys it when it expires at %s", a.lease.ID, err, a.lease.ExpiresAt.UTC().Format(time.RFC3339))})
	}
	if len(out.result.Envelope) > 0 {
		am := toolio.Amendment{Warnings: warnings}
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
	}
	r.cleanup(ctx, a, out)
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
