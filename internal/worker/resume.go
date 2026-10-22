package worker

import (
	"encoding/json"
	"fmt"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// WarnAttemptResumed says a delivery carried on an attempt that an earlier one left, or began again after it
// discarded what it left, and from which stage.
const WarnAttemptResumed = "attempt_resumed"

// stageOutcome is the stage of a resumed delivery whose attempt has its outcome stored already: only the
// bookkeeping is left.
const stageOutcome = "outcome"

// resumed says whether the delivery carries an attempt on, or began again after it discarded what an earlier
// delivery left: a first delivery, and a later one that had nothing to carry on, are neither.
func (a *attempt) resumed() bool { return a.resume != nil || a.discarded != "" }

// resumeStage is the stage the delivery picks up from, which is the brain for a delivery that began again,
// and whether the brain is skipped.
func (a *attempt) resumeStage() (stage string, skipped bool) {
	if a.resume == nil {
		return stageBrain, false
	}
	return a.resume.stage, a.resume.stage != stageBrain
}

// announceResume tells the job, once, that the delivery resumes or discards a resume: the step event named
// resume, with the handover count, the stage, whether the brain is skipped and, for a resume that was
// discarded, why. It is made when the stage is final, which is after the clone is made ready for it.
func (a *attempt) announceResume() {
	if !a.resumed() || a.announced {
		return
	}
	a.announced = true
	stage, skipped := a.resumeStage()
	data := struct {
		Name         string `json:"name"`
		Source       string `json:"source"`
		Delivery     int    `json:"delivery"`
		Stage        string `json:"stage"`
		BrainSkipped bool   `json:"brain_skipped"`
		Reason       string `json:"reason,omitempty"`
	}{"resume", "worker", a.req.Delivery, stage, skipped, a.discarded}
	raw, _ := json.Marshal(data)
	a.keep("step", stamp(a.clock()), raw)
	a.report.Emit("step", data)
}

// resumedWarning is the warning that every resumed delivery adds to its envelope, or nil: it names the handover
// count, the stage, and whether the brain was skipped.
func (a *attempt) resumedWarning() *toolio.Warning {
	if !a.resumed() {
		return nil
	}
	stage, skipped := a.resumeStage()
	brain := "the brain ran"
	if skipped {
		brain = "the brain was skipped"
	}
	return &toolio.Warning{Code: WarnAttemptResumed, Message: fmt.Sprintf(
		"delivery %d of the attempt carried on from the stage %s, and %s", a.req.Delivery, stage, brain)}
}
