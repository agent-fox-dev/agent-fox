package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/worker/recovery"
)

// WarnBrainSpendUnrecorded says a brain run was cut short by the death of the process, so that its cost is
// not recorded and counts as nothing.
const WarnBrainSpendUnrecorded = "brain_spend_unrecorded"

// WarnResumeDiscarded says a delivery could not carry on what an earlier one left, and started from a fresh
// clone.
const WarnResumeDiscarded = "resume_discarded"

// The reasons a resume is discarded.
const (
	reasonJournalUnreadable = "journal_unreadable"
	reasonCloneInvalid      = "clone_invalid"
	reasonOriginChanged     = "origin_changed"
	reasonForgeChanged      = "forge_repository_changed"
	reasonRunMissing        = "run_missing"
	reasonLedgerUnreadable  = "ledger_unreadable"
)

// The stages a resumed delivery picks up from.
const stageBrain = "brain"

// resumeState is what a delivery that carries an attempt on holds: the journal of the delivery before it, and
// the stage it picks up from.
type resumeState struct {
	journal recovery.Journal
	stage   string
}

// start decides how the delivery begins, once the attempt is resolved and before anything is opened or
// taken: the first delivery begins afresh; a later one carries on what the earlier ones left when it is
// whole, begins afresh without a word when there is nothing to carry on, and says it discards what it
// cannot carry on. It makes the journal of the delivery.
func (r *Runner) start(ctx context.Context, a *attempt) *outcome {
	if out := r.prepareGit(a); out != nil {
		return out
	}
	h := &journalHandle{store: recovery.NewStore(a.req.Dir), fault: r.journalFault}
	a.journal = h
	fresh := recovery.Journal{
		Version: recovery.FormatVersion, Job: a.req.JobID, Attempt: a.req.Attempt, Kind: string(a.req.Kind),
		HubURL: stripUserinfo(a.record.CloneURL), Forge: a.repo.String(),
		Settings: recovery.Settings{
			Brain: a.settings.Brain, Model: a.settings.Model, Profile: a.settings.Profile, Land: a.settings.Land,
			Budget: a.settings.Budget, MaxTurns: a.settings.MaxTurns,
		},
	}
	a.pinned = fresh.Settings
	j := fresh
	if a.req.Delivery <= 1 {
		if err := h.store.Remove(); err != nil {
			return transientFailure(toolio.CategoryIO, fmt.Sprintf("the journal of an earlier attempt cannot be removed: %v", err))
		}
	} else {
		var out *outcome
		if j, out = r.laterDelivery(ctx, a, fresh); out != nil {
			h.j = j
			return out
		}
	}
	j.Deliveries = append(j.Deliveries, recovery.Delivery{Count: max(a.req.Delivery, 1), Started: a.clock()})
	h.j = j
	a.note(stepBegin, func(*recovery.Journal) {})
	if a.stored != nil { // the attempt is ended: nothing is opened, cloned, leased or run
		if a.sessionID != "" {
			a.report.Hub(a.sessionID, a.runID, "")
		}
		a.announceResume()
		return &outcome{result: jobs.Result{Envelope: []byte(a.stored.Envelope), Report: a.stored.Report, MaxAttempts: a.stored.MaxAttempts}}
	}
	return nil
}

// laterDelivery is the journal of a delivery that is not the first, and what the delivery does with the
// work of the ones before it (see start). An outcome means the attempt ends here.
func (r *Runner) laterDelivery(ctx context.Context, a *attempt, fresh recovery.Journal) (recovery.Journal, *outcome) {
	_, cloneErr := os.Stat(a.cloneDir)
	cloneKept := cloneErr == nil
	old, err := a.journal.store.Load()
	switch {
	case errors.Is(err, recovery.ErrNotFound):
		r.dropOutcome(a)
		if cloneKept {
			a.discard(reasonJournalUnreadable, "the attempt directory has a clone and no journal")
		}
		return fresh, nil
	case err != nil:
		r.dropOutcome(a)
		a.discard(reasonJournalUnreadable, err.Error())
		return fresh, nil
	case old.Job != fresh.Job || old.Attempt != fresh.Attempt || old.Kind != fresh.Kind:
		r.dropOutcome(a)
		a.discard(reasonJournalUnreadable, "the journal is of another job, another attempt or another kind")
		return fresh, nil
	}

	// The journal reads: the leases of the deliveries before this one are given back, the settings the
	// run was started with are the ones it goes on with, and the session is the attempt's.
	r.releaseJournalLeases(ctx, a, &old)
	r.pin(a, old.Settings)
	if !old.Session.Completed && old.Session.ID != "" {
		a.sessionID, a.runID = old.Session.ID, old.Session.RunID
	}
	if stored := r.storedOutcome(a); stored != nil { // the attempt came to its outcome: only the bookkeeping is left
		a.stored = stored
		a.resume = &resumeState{journal: old, stage: stageOutcome}
		a.marked = a.issue != nil && slices.Contains(a.labels, a.label(statusInProgress))
		return old, nil
	}
	next := fresh
	next.Settings, next.Session, next.Leases, next.Deliveries = old.Settings, old.Session, old.Leases, old.Deliveries
	next.Carried, next.Spend = old.Carried, old.Spend
	next.Landing.CreatedIssue = old.Landing.CreatedIssue // an issue on the forge outlives the clone it was made from
	if old.Session.Completed {
		next.Session = recovery.Session{}
	}
	if old.CloneDir == "" || !cloneKept { // no clone was made: there is nothing to discard
		r.leave(a, old.Spend)
		return next, nil
	}

	runID := old.Run.ID
	if reason, detail := r.rejects(ctx, a, old, runID); reason != "" {
		removed := r.removedSpend(a, runID)
		next.Carried = carry(old.Spend, old.Carried, removed)
		next.Spend = next.Carried
		a.discard(reason, detail)
		r.leave(a, next.Carried)
		return next, nil
	}
	run, err := toolio.OpenRunDir(a.cloneDir, runID)
	if err != nil {
		return old, transientFailure(toolio.CategoryIO, fmt.Sprintf("the run directory cannot be opened: %v", err))
	}
	ledger, cut, err := scanLedger(run)
	if err != nil {
		return old, transientFailure(toolio.CategoryIO, fmt.Sprintf("the ledger of the run cannot be read: %v", err))
	}
	for _, c := range cut {
		a.warnings = append(a.warnings, toolio.Warning{Code: WarnBrainSpendUnrecorded, Message: fmt.Sprintf(
			"the brain run of round %d (ledger entry %d) has no end in the ledger: the process died while it ran, so what it cost is not known and counts as nothing", c.round, c.seq)})
	}
	remote, problem := r.disagrees(ctx, a, old, runID)
	if problem != nil {
		return old, problem
	}
	if old.Session.Completed { // a session that was completed is not reported to again: the run goes on under a new one
		a.runID = old.Session.RunID
	}
	spent := carry(old.Spend, old.Carried, &ledger)
	a.resume = &resumeState{journal: old, stage: stageBrain}
	if accepted, _ := acceptedSubmission(a.req.Kind, a.cloneDir, runID); accepted != nil {
		if stage := accepted.stage(a.req.Kind); stage != "" {
			a.resume.stage = stage
		}
	}
	if plan := old.Landing.Plan; plan != nil && remote != "" { // disagrees refused any commit but the plan's
		a.resume.stage = stagePublished // the change is on hub's git server: it is published, not redone
	}
	r.leave(a, spent)
	old.Settings = a.pinned
	return old, nil
}

// prepareResumedClone makes the clone of a resumed delivery what the stage it picks up from needs: a fix
// whose brain is skipped has the change of the run in its working tree, and any other has the base alone,
// for the brain to run again. A submission that no longer passes the check of its kind against the tree is
// not one to land or to measure: the brain runs. A question or a diagnosis needs no change in the tree.
func (r *Runner) prepareResumedClone(ctx context.Context, a *attempt) *outcome {
	if a.resume.stage == stagePublished {
		return nil
	}
	if a.resume.stage == stageApplied {
		if out := r.restoreChange(ctx, a); out != nil {
			return out
		}
	}
	if a.resume.stage != stageBrain {
		if stage, _ := skipStage(ctx, a.req.Kind, a.cloneDir, a.resume.journal.Run.ID); stage == a.resume.stage {
			return nil
		}
		a.resume.stage = stageBrain
	}
	return r.resetClone(ctx, a)
}

// nothingLeft ends the attempt, without a clone or a lease, when the brain would run and the attempt has used its
// budget or its turns: one budget and one bound of turns are the attempt's, however many deliveries it takes.
func (r *Runner) nothingLeft(a *attempt) *outcome {
	if a.resume != nil && a.resume.stage != stageBrain {
		// No brain runs, so the bounds are not used; the command still takes them as valid values.
		a.settings.Budget, a.settings.MaxTurns = a.pinned.Budget, a.pinned.MaxTurns
		return nil
	}
	hint := ""
	switch {
	case a.settings.Budget <= 0:
		hint = "AF_MAX_BUDGET_USD"
	case a.settings.MaxTurns < 1:
		hint = "AF_MAX_TURNS"
	default:
		return nil
	}
	return r.envelopeOf(a, &refusal{category: toolio.CategoryBudget, hint: &toolio.FixHint{Var: hint},
		message: fmt.Sprintf("the attempt has used $%.2f of its budget of $%.2f and %d of its %d turns in the deliveries before this one, so there is nothing left for another run of the brain: raise %s and ask again",
			a.spent.CostUSD, a.pinned.Budget, a.spent.Turns, a.pinned.MaxTurns, hint)})
}

// leave notes what the attempt has spent, and what is left of the bounds it was pinned to, for the brain
// that runs next.
func (r *Runner) leave(a *attempt, spent recovery.Spend) {
	a.spent = spent
	a.settings.Budget = a.pinned.Budget - spent.CostUSD
	a.settings.MaxTurns = a.pinned.MaxTurns - spent.Turns
}

// pin makes the settings the run was started with the settings of the delivery. The timeout is not
// among them: it is read afresh for each delivery.
func (r *Runner) pin(a *attempt, s recovery.Settings) {
	if s.Brain == "" {
		return
	}
	a.pinned = s
	a.settings.Brain, a.settings.Model, a.settings.Profile, a.settings.Land = s.Brain, s.Model, s.Profile, s.Land
	a.settings.Budget, a.settings.MaxTurns = s.Budget, s.MaxTurns
}

// discard raises the warning of a resume that was not possible.
func (a *attempt) discard(reason, detail string) {
	if a.discarded == "" {
		a.discarded = reason
	}
	a.warnings = append(a.warnings, toolio.Warning{Code: WarnResumeDiscarded, Message: fmt.Sprintf(
		"the delivery starts from a fresh clone (%s): %s", reason, a.secretFree(detail))})
}

// storedOutcome is the outcome that an earlier delivery of this attempt stored, or nil when there is none, or
// when what is there is none of this job and attempt.
func (r *Runner) storedOutcome(a *attempt) *recovery.Outcome {
	o, err := a.journal.store.LoadOutcome()
	switch {
	case errors.Is(err, recovery.ErrNotFound):
		return nil
	case err != nil:
		r.cfg.Logger.Warn("the stored outcome of the attempt cannot be read", "job", a.req.JobID, "attempt", a.req.Attempt, "error", err.Error())
		return nil
	case o.Job != a.req.JobID || o.Attempt != a.req.Attempt || !json.Valid([]byte(o.Envelope)) || !strings.HasPrefix(strings.TrimSpace(o.Envelope), "{"):
		return nil
	}
	return &o
}

// dropOutcome removes an outcome that a journal that cannot be read leaves behind: the attempt is begun
// again, and the next delivery must not take the outcome of a run that is gone for its own.
func (r *Runner) dropOutcome(a *attempt) {
	if err := a.journal.store.RemoveOutcome(); err != nil {
		r.cfg.Logger.Warn("the stored outcome of an earlier run of the attempt cannot be removed", "job", a.req.JobID, "attempt", a.req.Attempt, "error", err.Error())
	}
}

// releaseJournalLeases gives back the leases that the journal records as not released, before the
// delivery takes its own. A lease that hub does not find is released; one that cannot be released
// is logged and stays recorded as held, for the reconciliation pass.
func (r *Runner) releaseJournalLeases(ctx context.Context, a *attempt, j *recovery.Journal) {
	for i := range j.Leases {
		l := &j.Leases[i]
		if l.Released {
			continue
		}
		release, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.releaseBound())
		err := r.cfg.Hub.ReleaseLease(release, a.pat, a.req.Workspace, l.SandboxID)
		cancel()
		if err != nil {
			r.cfg.Logger.Warn("a lease of an earlier delivery could not be released", "workspace", a.req.Workspace, "job", a.req.JobID,
				"attempt", a.req.Attempt, "lease", l.SandboxID, "error", err.Error())
			continue
		}
		l.Released = true
	}
}

// rejects says why the clone, the run directory and the journal are not whole enough to carry on, or
// nothing.
func (r *Runner) rejects(ctx context.Context, a *attempt, j recovery.Journal, runID string) (reason, detail string) {
	g := gitx.New(a.cloneDir, a.git)
	if top, err := g.TopLevel(ctx); err != nil || !sameDir(top, a.cloneDir) {
		return reasonCloneInvalid, "the clone is not the top level of a git work tree"
	}
	out, code, err := a.git(ctx, a.cloneDir, []string{"git", "config", "--get", "remote.origin.url"})
	if origin := strings.TrimSpace(out); err != nil || code != 0 || origin != a.record.CloneURL {
		return reasonOriginChanged, fmt.Sprintf("the clone comes from %s, and the workspace is cloned from %s now", a.secretFree(origin), stripUserinfo(a.record.CloneURL))
	}
	if j.Forge != a.repo.String() {
		return reasonForgeChanged, fmt.Sprintf("the run was begun on %s, and the workspace's forge repository is %s now", j.Forge, a.repo)
	}
	run, err := toolio.OpenRunDir(a.cloneDir, runID)
	if runID == "" || err != nil {
		return reasonRunMissing, "the directory of the run is not in the clone"
	}
	pinned := fix.StateFile
	if a.req.Kind == jobs.KindTriage {
		pinned = "triage.json"
	}
	if _, err := os.Stat(filepath.Join(run.Path, pinned)); err != nil {
		return reasonRunMissing, fmt.Sprintf("the run directory has no %s", pinned)
	}
	if _, err := run.Ledger(); err != nil {
		return reasonLedgerUnreadable, err.Error()
	}
	return "", ""
}

// sameDir says whether two paths are one directory.
func sameDir(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && filepath.Clean(ra) == filepath.Clean(rb)
}

// ledgerSpendOf is what the ledger of the run in the clone records the brain runs to have cost.
func ledgerSpendOf(a *attempt, runID string) (recovery.Spend, error) {
	run, err := toolio.OpenRunDir(a.cloneDir, runID)
	if err != nil {
		return recovery.Spend{}, err
	}
	return ledgerSpend(run)
}

// removedSpend is what the ledger of the run that is to be removed records, or nil when it does not
// read.
func (r *Runner) removedSpend(a *attempt, runID string) *recovery.Spend {
	if runID == "" {
		return nil
	}
	spend, err := ledgerSpendOf(a, runID)
	if err != nil {
		return nil
	}
	return &spend
}

// disagrees finds what the journal, the clone and hub's git server say that the rules of the resume do
// not cover, and ends the attempt with it: guessing could publish the wrong change. It returns the commit
// that the branch of the plan of the landing is at on hub's git server, or "" when it is not there.
func (r *Runner) disagrees(ctx context.Context, a *attempt, j recovery.Journal, runID string) (remote string, out *outcome) {
	if j.Run.Base != "" && j.Run.BaseHead != "" {
		listed, code, err := a.git(ctx, a.cloneDir, []string{"git", "rev-list", "--reverse", j.Run.BaseHead + "..refs/heads/" + j.Run.Base})
		if err == nil && code == 0 {
			for _, commit := range strings.Fields(listed) {
				msg, _, _ := a.git(ctx, a.cloneDir, []string{"git", "log", "-1", "--format=%B", commit})
				if !strings.Contains(msg, "Agent-Fox-Run: "+runID) {
					return "", r.envelopeOf(a, &refusal{category: toolio.CategoryInternal, message: fmt.Sprintf(
						"the branch %s of the clone has the commit %.12s after the commit %.12s that the run began at, and it has no Agent-Fox-Run trailer for the run %s: it is not the run's, and the attempt is not carried on",
						j.Run.Base, commit, j.Run.BaseHead, runID)})
				}
			}
		}
	}
	plan := j.Landing.Plan
	if plan == nil || plan.Branch == "" {
		return "", nil
	}
	listed, code, err := a.git(ctx, a.cloneDir, []string{"git", "ls-remote", "--heads", "origin", "refs/heads/" + plan.Branch})
	switch {
	case ctx.Err() != nil:
		return "", &outcome{err: context.Cause(ctx)}
	case err != nil || code != 0:
		return "", transientFailure(toolio.CategoryGit, fmt.Sprintf("hub's git server cannot be asked for the branch %s: %s", plan.Branch, gitDetail(a, listed, err)))
	}
	sha, _, _ := strings.Cut(strings.TrimSpace(listed), "\t")
	if sha != "" && sha != plan.Commit {
		return "", r.envelopeOf(a, &refusal{category: toolio.CategoryInternal, message: fmt.Sprintf(
			"the branch %s is on hub's git server at the commit %.12s, and the plan of the landing says %.12s: it is not the branch the run made, and the attempt is not carried on",
			plan.Branch, sha, plan.Commit)})
	}
	return sha, nil
}
