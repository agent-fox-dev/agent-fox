package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/worker/recovery"
)

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
		if cloneKept {
			a.discard(reasonJournalUnreadable, "the attempt directory has a clone and no journal")
		}
		return fresh, nil
	case err != nil:
		a.discard(reasonJournalUnreadable, err.Error())
		return fresh, nil
	case old.Job != fresh.Job || old.Attempt != fresh.Attempt || old.Kind != fresh.Kind:
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
	next := fresh
	next.Settings, next.Session, next.Leases, next.Deliveries = old.Settings, old.Session, old.Leases, old.Deliveries
	next.Carried, next.Spend = old.Carried, old.Spend
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
	ledger, err := ledgerSpendOf(a, runID)
	if err != nil {
		return old, transientFailure(toolio.CategoryIO, fmt.Sprintf("the ledger of the run cannot be read: %v", err))
	}
	if problem := r.disagrees(ctx, a, old, runID); problem != nil {
		return old, problem
	}
	if old.Session.Completed { // a session that was completed is not reported to again: the run goes on under a new one
		a.runID = old.Session.RunID
	}
	spent := carry(old.Spend, old.Carried, &ledger)
	a.resume = &resumeState{journal: old, stage: stageBrain}
	if accepted, _ := acceptedSubmission(a.req.Kind, a.cloneDir, runID); accepted != nil && accepted.stage(a.req.Kind) == stageApplied {
		a.resume.stage = stageApplied
	}
	r.leave(a, spent)
	old.Settings = a.pinned
	return old, nil
}

// prepareResumedClone makes the clone of a resumed delivery what the stage it picks up from needs: a fix
// whose brain is skipped has the change of the run in its working tree, and any other has the base alone,
// for the brain to run again. A change that no longer passes the check of its submission against the tree is
// not one to measure: the brain runs.
func (r *Runner) prepareResumedClone(ctx context.Context, a *attempt) *outcome {
	if a.resume.stage == stageApplied {
		if out := r.restoreChange(ctx, a); out != nil {
			return out
		}
		if stage, _ := skipStage(ctx, a.req.Kind, a.cloneDir, a.resume.journal.Run.ID); stage == stageApplied {
			return nil
		}
		a.resume.stage = stageBrain
	}
	return r.resetClone(ctx, a)
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
	a.warnings = append(a.warnings, toolio.Warning{Code: WarnResumeDiscarded, Message: fmt.Sprintf(
		"the delivery starts from a fresh clone (%s): %s", reason, a.secretFree(detail))})
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
// not cover, and ends the attempt with it: guessing could publish the wrong change.
func (r *Runner) disagrees(ctx context.Context, a *attempt, j recovery.Journal, runID string) *outcome {
	if j.Run.Base != "" && j.Run.BaseHead != "" {
		out, code, err := a.git(ctx, a.cloneDir, []string{"git", "rev-list", "--reverse", j.Run.BaseHead + "..refs/heads/" + j.Run.Base})
		if err == nil && code == 0 {
			for _, commit := range strings.Fields(out) {
				msg, _, _ := a.git(ctx, a.cloneDir, []string{"git", "log", "-1", "--format=%B", commit})
				if !strings.Contains(msg, "Agent-Fox-Run: "+runID) {
					return r.envelopeOf(a, &refusal{category: toolio.CategoryInternal, message: fmt.Sprintf(
						"the branch %s of the clone has the commit %.12s after the commit %.12s that the run began at, and it has no Agent-Fox-Run trailer for the run %s: it is not the run's, and the attempt is not carried on",
						j.Run.Base, commit, j.Run.BaseHead, runID)})
				}
			}
		}
	}
	if plan := j.Landing.Plan; plan != nil && plan.Branch != "" {
		out, code, err := a.git(ctx, a.cloneDir, []string{"git", "ls-remote", "--heads", "origin", "refs/heads/" + plan.Branch})
		switch {
		case ctx.Err() != nil:
			return &outcome{err: context.Cause(ctx)}
		case err != nil || code != 0:
			return transientFailure(toolio.CategoryGit, fmt.Sprintf("hub's git server cannot be asked for the branch %s: %s", plan.Branch, gitDetail(a, out, err)))
		}
		if sha, _, _ := strings.Cut(strings.TrimSpace(out), "\t"); sha != "" && sha != plan.Commit {
			return r.envelopeOf(a, &refusal{category: toolio.CategoryInternal, message: fmt.Sprintf(
				"the branch %s is on hub's git server at the commit %.12s, and the plan of the landing says %.12s: it is not the branch the run made, and the attempt is not carried on",
				plan.Branch, sha, plan.Commit)})
		}
	}
	return nil
}
