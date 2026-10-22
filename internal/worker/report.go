package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The warnings of the report to hub.
const (
	WarnHubUsageFailed   = "hub_usage_failed"
	WarnHubEventsFailed  = "hub_events_failed"
	WarnHubSessionFailed = "hub_session_failed"
)

// eventBatch is the most events a batch to hub holds.
const eventBatch = 500

// maxSessionMessage is how much of a failure the completion of a session carries.
const maxSessionMessage = 500

// report tells hub how the attempt went, once the lease is released: one usage record when a brain
// ran, with the model the session was opened with and what the envelope says the brain cost; the
// events of the attempt in batches of at most 500, in the order they happened; and the completion
// of the session, with the status of the envelope, or failed when there is no envelope. A call that
// fails is a warning with its code and changes nothing else; with no envelope it is logged. An
// attempt that has no session has nothing to report.
func (r *Runner) report(ctx context.Context, a *attempt, out *outcome) {
	if a.sessionID == "" {
		return
	}
	a.step("report")
	slug := a.req.Workspace

	var env toolio.Envelope
	hasEnvelope := len(out.result.Envelope) > 0 && json.Unmarshal(out.result.Envelope, &env) == nil
	if hasEnvelope && env.Usage.Brain != "" {
		u := env.Usage
		err := r.cfg.Hub.PostUsage(ctx, a.pat, slug, a.sessionID, hub.Usage{
			Model: a.settings.Model, Brain: u.Brain, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
			CostUSD: u.CostUSD, CostSource: u.CostSource, Turns: u.Turns,
		})
		a.reportFailed(WarnHubUsageFailed, "posting the usage of the session", err)
	}
	for batch := range slices.Chunk(a.eventsOf(), eventBatch) {
		if err := r.cfg.Hub.PostEvents(ctx, a.pat, slug, a.runID, batch); err != nil {
			a.reportFailed(WarnHubEventsFailed, "posting the events of the run", err)
			break
		}
	}
	status, message := sessionEnd(a, out, env, hasEnvelope)
	a.reportFailed(WarnHubSessionFailed, "completing the session", r.cfg.Hub.CompleteSession(ctx, a.pat, slug, a.sessionID, status, message))
}

// reportFailed notes a call to hub that failed as a warning. The error holds no token.
func (a *attempt) reportFailed(code, what string, err error) {
	if err != nil {
		a.warnings = append(a.warnings, toolio.Warning{Code: code, Message: fmt.Sprintf("%s failed: %v", what, err)})
	}
}

// sessionEnd is the status the session is completed with, and the message that goes with a failure:
// done, unverified and needs_human are completed, a failure that is a timeout is a timeout, any
// other failure, and an attempt with no envelope, a transient error or a shutdown among them,
// is failed.
func sessionEnd(a *attempt, out *outcome, env toolio.Envelope, hasEnvelope bool) (status, message string) {
	if !hasEnvelope {
		message = "the attempt ended without an envelope"
		if out.err != nil {
			message = out.err.Error()
		}
		return hub.SessionFailed, bounded(a.scrub(message))
	}
	switch env.Status {
	case toolio.StatusDone, toolio.StatusUnverified, toolio.StatusNeedsHuman:
		return hub.SessionCompleted, ""
	}
	status = hub.SessionFailed
	if env.Error != nil {
		message = env.Error.Message
		if env.Error.Category == toolio.CategoryTimeout {
			status = hub.SessionTimeout
		}
	}
	return status, bounded(a.scrub(message))
}

// bounded cuts a message at a length that hub takes.
func bounded(s string) string {
	if len(s) > maxSessionMessage {
		return s[:maxSessionMessage]
	}
	return s
}
