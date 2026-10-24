package worker

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/worker/recovery"
)

// stepOutcome names the write of the outcome in the seam of the tests that make a write of the journal fail.
const stepOutcome = "outcome"

// storeOutcome writes the outcome of the attempt to recovery/outcome.json: the envelope it ends with, the text
// of its report, the most attempts and the labels it set and removed, and whether the mirror is done. It is made
// as soon as the outcome is known, before anything is written to the forge or to hub for it, and again when the
// mirror is done, so that a delivery that finds it carries the bookkeeping on and runs nothing. A write that
// fails is the warning journal_unwritable, and the delivery goes on without it.
func (r *Runner) storeOutcome(a *attempt, out *outcome, mirrored bool) {
	if a.journal == nil {
		return // the attempt ended before it was begun: there is nothing to carry on
	}
	envelope := a.secretFree(string(out.result.Envelope))
	if !json.Valid([]byte(envelope)) {
		a.journal.unwritable(a, stepOutcome, fmt.Errorf("the envelope would not be JSON once the credentials are taken out of it"))
		return
	}
	o := recovery.Outcome{
		Version: recovery.FormatVersion, Job: a.req.JobID, Attempt: a.req.Attempt, Envelope: envelope,
		Report: a.secretFree(out.result.Report), MaxAttempts: out.result.MaxAttempts,
		LabelsSet: slices.Clone(a.labelsSet), LabelsRemoved: slices.Clone(a.labelsRemoved), Mirrored: mirrored,
	}
	if h := a.journal; h.fault != nil {
		if err := h.fault(stepOutcome); err != nil {
			h.unwritable(a, stepOutcome, err)
			return
		}
	}
	if err := a.journal.store.WriteOutcome(o); err != nil {
		a.journal.unwritable(a, stepOutcome, err)
	}
}

// settle is what the attempt does with its outcome once the mirror is done: the envelope gets what the worker
// knows of the attempt, the warnings it raised, the writes it made, and the session and the sandbox it used, and
// the outcome is stored again as mirrored. The envelope of an outcome that was stored is as it is.
func (r *Runner) settle(a *attempt, out *outcome) {
	if a.stored != nil {
		if !a.stored.Mirrored {
			r.storeOutcome(a, out, true)
		}
		return
	}
	am := toolio.Amendment{Warnings: slices.Clone(a.warnings), SideEffects: a.sideEffects}
	if a.sessionID != "" {
		am.Artifacts = append(am.Artifacts, toolio.Artifact{Kind: "hub_session", Name: a.sessionID})
	}
	if a.lease != nil {
		am.Artifacts = append(am.Artifacts, toolio.Artifact{Kind: "sandbox", Name: a.lease.ID})
	}
	r.amend(a, out, am)
	r.storeOutcome(a, out, true)
	r.reached(a, "mirrored")
}

// amend adds to the envelope of the outcome, which stays as it was but for what is added.
func (r *Runner) amend(a *attempt, out *outcome, am toolio.Amendment) {
	patched, err := toolio.Amend(out.result.Envelope, am)
	if err != nil {
		r.cfg.Logger.Warn("the envelope of the attempt could not be amended", "job", a.req.JobID, "attempt", a.req.Attempt, "error", err.Error())
		return
	}
	out.result.Envelope = patched
}
