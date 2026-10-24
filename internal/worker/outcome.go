package worker

import (
	"slices"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// settle is what the attempt does with its outcome once the mirror is done: the envelope gets what the worker
// knows of the attempt, the warnings it raised, the writes it made, and the session and the sandbox it used.
func (r *Runner) settle(a *attempt, out *outcome) {
	am := toolio.Amendment{Warnings: slices.Clone(a.warnings), SideEffects: a.sideEffects}
	if a.sessionID != "" {
		am.Artifacts = append(am.Artifacts, toolio.Artifact{Kind: "hub_session", Name: a.sessionID})
	}
	if a.lease != nil {
		am.Artifacts = append(am.Artifacts, toolio.Artifact{Kind: "sandbox", Name: a.lease.ID})
	}
	r.amend(a, out, am)
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
