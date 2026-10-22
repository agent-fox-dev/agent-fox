package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/worker/recovery"
)

// attempt is one attempt as its steps share it: what resolve found out, and what the steps that
// follow it made.
type attempt struct {
	req     jobs.Request
	report  reporter
	started time.Time

	// pat is the token of the subscription, read once at the start and kept for the life of
	// the attempt, its cleanup included. prefix is the label prefix of the subscription.
	pat    string
	prefix string
	// record is the workspace on hub and repo its forge repository; forge is the forge as the
	// loops see it, through hub.
	record hub.Record
	repo   issuex.Repo
	forge  *hub.ForgeClient
	// settings are the variables of the workspace; maxAttempts is the most attempts a round may
	// make, which the result gives back to the jobs layer.
	settings    Settings
	maxAttempts int
	// input is what the loop is asked about.
	input loopInput
	// runID and sessionID are the hub session of the attempt, and the run id of its loop.
	runID, sessionID string
	// git runs every git command of the attempt, and cloneDir is the clone it runs them in.
	git      gitx.Runner
	cloneDir string
	// lease is the sandbox that hub leased, nil before it is granted and after it was released
	// by this attempt; boxes is the registry of the attempt, which holds its gateway, and
	// boxLabels the labels that the box of the loop is made with.
	lease     *hub.Lease
	boxes     *box.Registry
	boxLabels map[string]string
	// warnings are the warnings that go into the envelope of the attempt, beside the ones the
	// loop raised.
	warnings []toolio.Warning
	// releaseOnce makes the release of the lease happen once, whichever path asks for it first,
	// and releaseErr is how it went.
	releaseOnce  sync.Once
	releaseErr   error
	releaseNoted bool // the end of the attempt has logged a release that failed
	// labelPrefixVar is AF_LABEL_PREFIX as it was set, which nothing uses: the prefix of the labels
	// is the subscription's. It is kept to warn when the two differ.
	labelPrefixVar string
	// issue is the issue the input named, nil for a text and a file. labels are the labels it
	// carries as the attempt knows them: those it carried at resolve, and what the attempt wrote
	// since. marked says the attempt set in-progress on it.
	issue   *issuex.IssueRef
	labels  []string
	initial []string // the labels it carried at resolve
	marked  bool
	// labelsSet and labelsRemoved are what the attempt did to labels, as the result reports it: a
	// label that was set and taken off again is removed and not set. sideEffects are the writes to
	// the forge of the worker, to be added to the envelope.
	labelsSet, labelsRemoved []string
	sideEffects              []toolio.SideEffect
	// events are the events of the attempt, the loop's and the worker's steps, kept for the report
	// to hub; mu guards them, as the loop writes them from its own goroutines. now is the clock.
	mu     sync.Mutex
	events []hub.Event
	now    func() time.Time
	// closers are what the attempt gives back when it ends, last made first. scratch are the
	// directories it made for itself, which are removed when it ends however it ends.
	closers []func()
	scratch []string
	// journal is the journal of the attempt, nil before it is made. aborted is the stage a test stopped
	// the delivery at, see stageAbort.
	journal       *journalHandle
	aborted       string
	stagesReached map[string]int
	// resume is what the delivery carries on from, nil for one that begins the attempt afresh. pinned
	// are the settings the run was started with, and spent what the brain runs of the attempt cost
	// before this delivery.
	resume *resumeState
	pinned recovery.Settings
	spent  recovery.Spend
}

// onClose registers what the attempt gives back when it ends.
func (a *attempt) onClose(f func()) { a.closers = append(a.closers, f) }

// close gives back what the attempt took, in the reverse order, and removes the directories it made.
func (a *attempt) close() {
	for i := len(a.closers) - 1; i >= 0; i-- {
		a.closers[i]()
	}
	a.closers = nil
	a.removeScratch()
}

// abandon is what a process that died leaves of an attempt: nothing is given back. Only the
// directories it made for itself in the temporary directory of the machine, which no later delivery
// reads, are removed.
func (a *attempt) abandon() {
	a.closers = nil
	a.removeScratch()
}

func (a *attempt) removeScratch() {
	for _, dir := range a.scratch {
		_ = os.RemoveAll(dir)
	}
	a.scratch = nil
}

// clock is the time now, by the clock of the worker.
func (a *attempt) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

// step tells the job that a step of the pipeline began.
func (a *attempt) step(name string) {
	data := struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	}{name, "worker"}
	raw, _ := json.Marshal(data)
	a.keep("step", stamp(a.clock()), raw)
	a.report.Emit("step", data)
}

// resolve reads what the attempt needs of the store and of hub, and refuses what cannot run,
// before anything is spent or written: the token and the label prefix, the record of the
// workspace, its variables, a fix that is not served, an issue that is not on the workspace's
// repository or is a pull request. Hub being unavailable is a transient error.
func (r *Runner) resolve(ctx context.Context, a *attempt) *outcome {
	a.step("resolve")
	a.maxAttempts = defaultMaxAttempts
	slug, kind := a.req.Workspace, a.req.Kind

	pat, err := r.cfg.Subscriptions.Token(ctx, slug)
	if err != nil {
		return r.envelopeOf(a, authRefusal(slug, tokenFault(err)))
	}
	a.pat = pat
	sub, err := r.cfg.Subscriptions.Get(ctx, slug)
	if err != nil {
		return r.envelopeOf(a, authRefusal(slug, tokenFault(err)))
	}
	a.prefix = sub.LabelPrefix

	record, err := r.cfg.Hub.WorkspaceRecord(ctx, pat, slug)
	if err != nil {
		return r.hubOutcome(ctx, a, "read the workspace record", err)
	}
	repo, ok := record.Repo()
	if !ok {
		return r.envelopeOf(a, &refusal{category: toolio.CategoryConfig,
			message: fmt.Sprintf("the forge repository of the workspace, %s, is none that agent-fox knows", record.ForgeURL)})
	}
	a.record, a.repo = record, repo
	a.forge = r.cfg.Hub.Forge(pat, slug, repo)

	vars, err := r.cfg.Hub.ResolvedVars(ctx, pat, slug)
	if err != nil {
		return r.hubOutcome(ctx, a, "read the variables", err)
	}
	a.labelPrefixVar = vars["AF_LABEL_PREFIX"]
	settings, bad := resolveSettings(kind, vars)
	if bad != nil {
		if bad.Name == "AF_MAX_ATTEMPTS" {
			a.maxAttempts = defaultMaxAttempts
		} else {
			a.maxAttempts = settings.MaxAttempts
		}
		return r.envelopeOf(a, &refusal{category: toolio.CategoryConfig, message: bad.Error(), hint: &toolio.FixHint{Var: bad.Name}})
	}
	a.settings, a.maxAttempts = settings, settings.MaxAttempts

	if kind == jobs.KindFix && (record.Mode == hub.ModeCarryPatch || settings.Land == landPatch) {
		return r.envelopeOf(a, &refusal{category: categoryUnsupported, message: "a fix on a carry-patch workspace, or landing as a patch, is not served: carry-patch landing comes in a later release"})
	}

	input, refused := parseInput(a.req.Input, repo)
	if refused != nil {
		return r.envelopeOf(a, refused)
	}
	a.input = input
	if input.kind == inputIssue {
		thread, err := a.forge.ReadIssue(ctx, input.ref)
		if err != nil {
			return r.forgeOutcome(ctx, a, input, err)
		}
		if thread.Issue.IsPR {
			return r.envelopeOf(a, usageRefusal("%s is a pull request or a merge request, and agent-fox takes an issue", input.url))
		}
		ref := input.ref
		a.issue, a.labels, a.initial = &ref, slices.Clone(thread.Issue.Labels), slices.Clone(thread.Issue.Labels)
	}
	return nil
}

// categoryUnsupported is the category of the refusal of work the service cannot do yet: the jobs
// layer's, which the carry-patch refusal reuses.
const categoryUnsupported = jobs.CategoryUnsupported

// tokenFault says why the token cannot be had.
func tokenFault(err error) string {
	switch {
	case errors.Is(err, store.ErrNotSubscribed):
		return "the workspace is not subscribed any more"
	case errors.Is(err, store.ErrUnreadable):
		return "the token of the subscription cannot be read with this key"
	}
	return "the token of the subscription cannot be had"
}

// hubOutcome is the outcome of a call to hub that failed. A context that ended is its own end;
// hub being unavailable is a transient error of category hub; a token that hub does not take is
// the credential; a workspace that hub does not find is a configuration that the operator must
// change; and any other refusal of hub is a refusal that tells what hub said.
func (r *Runner) hubOutcome(ctx context.Context, a *attempt, what string, err error) *outcome {
	var rejected *hub.RejectedError
	var unavailable *hub.UnavailableError
	switch {
	case ctx.Err() != nil:
		return &outcome{err: context.Cause(ctx)}
	case errors.Is(err, hub.ErrRefused), errors.Is(err, hub.ErrForbidden):
		return r.envelopeOf(a, authRefusal(a.req.Workspace, "hub does not take the token to "+what))
	case errors.Is(err, hub.ErrNotFound):
		return r.envelopeOf(a, &refusal{category: toolio.CategoryConfig, message: fmt.Sprintf("hub does not find the workspace %s: unsubscribe it, or subscribe the workspace that exists", a.req.Workspace)})
	case errors.As(err, &rejected):
		return r.envelopeOf(a, &refusal{category: CategoryHub, message: fmt.Sprintf("hub rejected the request to %s: %s", what, rejected.Message)})
	case errors.As(err, &unavailable):
		return transientFailure(CategoryHub, fmt.Sprintf("hub is unavailable to %s: %v", what, err))
	}
	return transientFailure(CategoryHub, fmt.Sprintf("hub failed to %s: %v", what, err))
}

// forgeOutcome is the outcome of a read of the issue through hub's forge API that failed, in the
// types the loops tell apart.
func (r *Runner) forgeOutcome(ctx context.Context, a *attempt, input loopInput, err error) *outcome {
	var status *issuex.HTTPError
	var unavailable *hub.UnavailableError
	switch {
	case ctx.Err() != nil:
		return &outcome{err: context.Cause(ctx)}
	case issuex.IsNotFound(err):
		return r.envelopeOf(a, usageRefusal("the issue %s does not exist on the workspace's repository", input.url))
	case errors.As(err, &status) && (status.Status == http.StatusUnauthorized || status.Status == http.StatusForbidden):
		return r.envelopeOf(a, authRefusal(a.req.Workspace, "hub does not take the token to read the issue"))
	case errors.As(err, &status):
		return r.envelopeOf(a, &refusal{category: CategoryHub, message: fmt.Sprintf("hub rejected the request to read the issue: %s", status.Message)})
	case errors.As(err, &unavailable), issuex.IsRateLimited(err):
		return transientFailure(CategoryHub, fmt.Sprintf("hub is unavailable to read the issue: %v", err))
	}
	return transientFailure(CategoryHub, fmt.Sprintf("hub failed to read the issue: %v", err))
}

// openSession opens the hub session of the attempt, with a run id drawn in the form of one, which
// the loop will be run under, and the model of the settings. The job is told the session and the
// run at once. A session that hub does not open has cost nothing, and nothing has been written.
func (r *Runner) openSession(ctx context.Context, a *attempt) *outcome {
	a.step("session")
	if a.sessionID != "" { // the session of an earlier delivery is the session of the attempt
		a.report.Hub(a.sessionID, a.runID, "")
		a.noteSession()
		return nil
	}
	runID := a.runID // the run id of a session that was completed, which the run goes on under
	if runID == "" {
		var err error
		if runID, err = toolio.NewRunID(r.cfg.Now, r.cfg.Rand); err != nil {
			return &outcome{err: err}
		}
	}
	id, err := r.cfg.Hub.OpenSession(ctx, a.pat, hub.SessionRequest{
		WorkspaceSlug: a.req.Workspace, Archetype: string(a.req.Kind), Model: a.settings.Model, RunID: runID,
	})
	if err != nil {
		return r.hubOutcome(ctx, a, "open a session", err)
	}
	a.runID, a.sessionID = runID, id
	a.report.Hub(id, runID, "")
	a.noteSession()
	return nil
}
