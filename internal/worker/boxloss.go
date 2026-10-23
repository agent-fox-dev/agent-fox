package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/worker/recovery"
)

// WarnBoxReplaced says the sandbox of the attempt was lost during the delivery, and was replaced by another.
const WarnBoxReplaced = "box_replaced"

// stepBoxLost names the record of a loss in the seam of the tests that make a write of the journal fail.
const stepBoxLost = "box_lost"

// replaceBox goes on in a new box after the one the loop ran in was lost: the sandbox is gone, or cannot be reached.
// The loop has stopped without landing or parking what it was making. The loss is recorded, the lost lease is given
// back, a replacement is leased with the profile and the purpose of the first, the timeout is cut to fit it, and the
// loop is run again from where the attempt stands: the change of a fix report that was accepted and applied is
// measured, and any other run begins again from the base with what is left of the budget. A box that is lost a
// second time in the delivery, a replacement that hub refuses or that has too little time left, and one that cannot
// be opened, end the delivery with a transient error, and the delivery that comes next resumes the attempt.
func (r *Runner) replaceBox(ctx context.Context, a *attempt) *outcome {
	lost := a.boxLost
	a.boxLost = nil
	old := a.lease
	r.releaseLost(ctx, a)

	if remaining := a.settings.Timeout - a.clock().Sub(a.loopStarted); remaining > time.Second {
		a.settings.Timeout = remaining.Truncate(time.Second)
	} else {
		a.settings.Timeout = time.Second
	}
	if out := r.takeLease(ctx, a, true); out != nil {
		return out
	}
	a.warnings = append(a.warnings, toolio.Warning{Code: WarnBoxReplaced, Message: fmt.Sprintf(
		"the sandbox %s was lost during the delivery, and was replaced by the sandbox %s: %v", old.ID, a.lease.ID, a.secretFree(lost.Error()))})
	a.emitStep(struct {
		Name   string `json:"name"`
		Source string `json:"source"`
		Lost   string `json:"lost"`
		New    string `json:"new"`
	}{"box_replaced", "worker", old.ID, a.lease.ID})

	if out := r.continueRun(ctx, a); out != nil {
		return out
	}
	// A box that is lost again ends the loop with the failure of a lost box, which is a transient error of category
	// sandbox: a delivery replaces a box once, and is not run again here.
	return r.runLoop(ctx, a)
}

// continueRun makes the clone and the budget what the run that goes on in the new box needs, as a delivery that
// resumes the run does: the stage is the one the run stands at, and the budget is what the brain runs so far have
// left of it. A run that was lost before the loop pinned what it found has nothing to go on from, and is a transient
// error: the delivery that comes next begins the attempt again.
func (r *Runner) continueRun(ctx context.Context, a *attempt) *outcome {
	j := a.journal.snapshot()
	if !a.runPinned(j.Run.ID) {
		return transientFailure(toolio.CategorySandbox, "the sandbox was lost before the run had pinned what it found, so there is nothing to go on from: the next delivery begins the attempt again")
	}
	if a.resume == nil {
		a.replacing = true
	}
	a.resume = &resumeState{journal: j, stage: a.runStage(j.Run.ID)}
	r.leave(a, j.Spend)
	if out := r.prepareResumedClone(ctx, a); out != nil {
		return out
	}
	return r.nothingLeft(a)
}

// releaseLost gives the lost lease back, and records the loss. A lease that hub does not find is released; one that
// cannot be released is logged, and stays recorded as held, for the reconciliation pass.
func (r *Runner) releaseLost(ctx context.Context, a *attempt) {
	lease := a.lease
	a.note(stepBoxLost, func(j *recovery.Journal) {
		for i := range j.Leases {
			if j.Leases[i].SandboxID == lease.ID {
				j.Leases[i].Lost = true
			}
		}
	})
	release, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.releaseBoundWithin(ctx))
	defer cancel()
	if err := r.cfg.Hub.ReleaseLease(release, a.pat, a.req.Workspace, lease.ID); err != nil {
		r.cfg.Logger.Warn("the lease of the sandbox that was lost could not be released", "workspace", a.req.Workspace, "job", a.req.JobID,
			"attempt", a.req.Attempt, "lease", lease.ID, "error", err.Error())
	} else {
		a.noteReleaseOf(lease.ID)
	}
	a.retired = append(a.retired, lease.Token)
	a.lease = nil
	a.releaseOnce, a.releaseErr = sync.Once{}, nil
}

// runPinned says whether the run in the clone has pinned what its preflight found, which a run needs to be carried on.
func (a *attempt) runPinned(runID string) bool {
	if runID == "" {
		return false
	}
	pinned := fix.StateFile
	if a.req.Kind == jobs.KindTriage {
		pinned = "triage.json"
	}
	_, err := os.Stat(filepath.Join(a.cloneDir, ".agent-fox", "run", runID, pinned))
	return err == nil
}
