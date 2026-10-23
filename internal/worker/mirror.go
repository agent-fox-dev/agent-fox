package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/labels"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/worker/recovery"
)

// The status labels of an issue, without the prefix of the subscription, from the label table.
const (
	statusInProgress = labels.InProgress
	statusReady      = labels.Ready
	statusNeedsHuman = labels.NeedsHuman
	statusLanded     = labels.Landed
	statusFailed     = labels.Failed
)

// statuses are the five status labels. Setting one removes the other four.
var statuses = labels.Statuses()

// The warnings of the writes to the forge, and of the prefix.
const (
	WarnStatusLabelFailed  = "status_label_failed"
	WarnCommentFailed      = "comment_failed"
	WarnRequestLabelFailed = "request_label_failed"
	WarnLabelPrefixIgnored = "label_prefix_ignored"
)

// label is the label of a status, with the prefix of the subscription: the one source of the
// prefix, since a variable that named another could name labels that no subscribe created.
func (a *attempt) label(status string) string { return labels.Name(a.prefix, status) }

// write makes one write to the forge, and notes it as a side effect of the attempt. A write that
// fails is a warning with the code, and never a change of what the attempt came to.
func (a *attempt) write(kind string, ref issuex.IssueRef, warning, what string, do func() error) bool {
	effect := toolio.SideEffect{Kind: kind, Target: ref.String(), State: toolio.StateDone}
	err := do()
	if err != nil {
		effect.State, effect.Detail = toolio.StateFailed, err.Error()
		a.warnings = append(a.warnings, toolio.Warning{Code: warning, Message: fmt.Sprintf("%s on %s failed: %v", what, ref.URL(), err)})
	}
	a.sideEffects = append(a.sideEffects, effect)
	return err == nil
}

// addLabel adds a label to the issue.
func (a *attempt) addLabel(ctx context.Context, ref issuex.IssueRef, label, warning string) bool {
	ok := a.write("add_labels", ref, warning, "adding the label "+label, func() error { return a.forge.AddLabels(ctx, ref, []string{label}) })
	if ok {
		if !slices.Contains(a.labels, label) {
			a.labels = append(a.labels, label)
		}
		if i := slices.Index(a.labelsRemoved, label); i >= 0 && slices.Contains(a.initial, label) {
			// Taken off and put back: the issue has it as it had it, and the result says neither.
			a.labelsRemoved = slices.Delete(a.labelsRemoved, i, i+1)
		} else if !slices.Contains(a.labelsSet, label) {
			a.labelsSet = append(a.labelsSet, label)
		}
	}
	return ok
}

// removeLabel removes a label the issue carries. A label that is gone already is removed.
func (a *attempt) removeLabel(ctx context.Context, ref issuex.IssueRef, label string) bool {
	ok := a.write("remove_label", ref, WarnStatusLabelFailed, "removing the label "+label, func() error {
		err := a.forge.RemoveLabel(ctx, ref, label)
		if issuex.IsNotFound(err) {
			return nil
		}
		return err
	})
	if ok {
		a.labels = slices.DeleteFunc(a.labels, func(l string) bool { return l == label })
		a.labelsSet = slices.DeleteFunc(a.labelsSet, func(l string) bool { return l == label })
		if !slices.Contains(a.labelsRemoved, label) {
			a.labelsRemoved = append(a.labelsRemoved, label)
		}
	}
	return ok
}

// setStatus sets a status label on the issue, and removes the others of the five that it
// carries. In a fix, in-progress does not remove ready: that is replaced only by the label the
// outcome of the fix sets.
func (a *attempt) setStatus(ctx context.Context, ref issuex.IssueRef, status string) bool {
	if !a.addLabel(ctx, ref, a.label(status), WarnStatusLabelFailed) {
		return false
	}
	for _, other := range statuses {
		keep := status == statusInProgress && a.req.Kind == jobs.KindFix && other == statusReady
		if other != status && !keep && slices.Contains(a.labels, a.label(other)) {
			a.removeLabel(ctx, ref, a.label(other))
		}
	}
	return true
}

// mark sets in-progress on the issue the input named, once the session is open and before
// anything is cloned or leased. A text and a file name no issue, and are not marked.
func (r *Runner) mark(ctx context.Context, a *attempt) *outcome {
	if a.issue != nil {
		a.marked = a.setStatus(ctx, *a.issue, statusInProgress)
	}
	return nil
}

// unmark takes in-progress off the issue the attempt marked.
func (a *attempt) unmark(ctx context.Context) {
	if a.marked && a.issue != nil {
		a.removeLabel(ctx, *a.issue, a.label(statusInProgress))
		a.marked = false
	}
}

// mirror writes what the attempt came to on the issue, as the status label and the comment, once
// the loop is over and before the lease is released. The ctx is the one the writes are made on.
//
//   - A shutdown writes nothing: the labels stay for the attempt that is handed over again.
//   - A cancel takes in-progress off and puts the request label of the job back on the issue,
//     which the person who cancelled had consumed, leaves ready in place, and sets no status
//     label and posts no comment.
//   - A transient error, and any other end with no envelope, takes in-progress off and sets nothing.
//   - An envelope is mapped to its label and its comment.
func (r *Runner) mirror(ctx context.Context, a *attempt, out *outcome, how stop) {
	if a.sessionID == "" || how == stoppedByShutdown {
		return
	}
	a.step("mirror")
	switch {
	case how == stoppedByCancel:
		if a.issue != nil {
			a.unmark(ctx)
			if a.req.RequestLabel != "" {
				a.addLabel(ctx, *a.issue, a.req.RequestLabel, WarnRequestLabelFailed)
			}
		}
	case out.err != nil || len(out.result.Envelope) == 0:
		a.unmark(ctx)
	default:
		r.mirrorEnvelope(ctx, a, out.result.Envelope)
	}
}

// plan is what an envelope is mirrored as: a status label, or none, and a comment, or none.
type plan struct {
	status, comment string
}

// mirrorEnvelope maps the envelope the attempt ended with to the issue.
func (r *Runner) mirrorEnvelope(ctx context.Context, a *attempt, data []byte) {
	var env toolio.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		a.unmark(ctx)
		return
	}
	res := readResult(env)
	ref := a.mirrorIssue(env, res)
	if ref == nil { // no issue to write to: nothing of the mirror is left to do
		a.noteComment(true)
		a.noteLabels(true)
		return
	}
	if a.issue == nil || *a.issue != *ref { // an issue the loop created: it carries no label of ours
		a.labels = nil
	}
	p := a.planOf(env, res)
	done := a.mirrorDone()
	a.noteComment(done.Comment || p.comment == "" || a.comment(ctx, *ref, p.comment, env, res))
	r.reached(a, "mirror_comment")
	labelled := true
	switch {
	case done.StatusLabels:
	case p.status != "":
		labelled = a.setStatus(ctx, *ref, p.status)
	case a.marked:
		a.unmark(ctx)
	}
	a.noteLabels(labelled)
	r.reached(a, "mirror_labels")
}

// noteComment and noteLabels record the steps of the mirror when they are done: the comment, and the status
// label.
func (a *attempt) noteComment(done bool) {
	a.note(stepComment, func(j *recovery.Journal) { j.Mirror.Comment = j.Mirror.Comment || done })
}

func (a *attempt) noteLabels(done bool) {
	a.note(stepStatusLabels, func(j *recovery.Journal) { j.Mirror.StatusLabels = j.Mirror.StatusLabels || done })
}

// mirrorDone are the steps of the mirror that the journal records as done, for a delivery that finishes the
// mirror of an outcome that an earlier one stored: those are not made again, and the others are, since a status
// label that is written again changes nothing and a comment is posted only when its marker is not on the issue.
func (a *attempt) mirrorDone() recovery.Mirror {
	if a.stored == nil || a.journal == nil {
		return recovery.Mirror{}
	}
	a.journal.mu.Lock()
	defer a.journal.mu.Unlock()
	return a.journal.j.Mirror
}

// mirrorIssue is the issue the mirror writes to: the one the input named, or, for a triage of a
// text or a file that ended done, the one the loop created; and none when there is neither.
func (a *attempt) mirrorIssue(env toolio.Envelope, res loopResult) *issuex.IssueRef {
	if a.issue != nil {
		return a.issue
	}
	if a.req.Kind == jobs.KindTriage && env.Status == toolio.StatusDone {
		if number := res.triageIssueNumber(); number > 0 {
			return &issuex.IssueRef{Repo: a.repo, Number: number}
		}
	}
	return nil
}

// planOf maps the envelope to its status label and its comment.
func (a *attempt) planOf(env toolio.Envelope, res loopResult) plan {
	switch env.Status {
	case toolio.StatusNeedsHuman:
		return plan{statusNeedsHuman, commentQuestion}
	case toolio.StatusFailed, toolio.StatusUsage:
		if a.automaticAttemptFollows(env) {
			return plan{}
		}
		return plan{statusFailed, commentFailure}
	}
	if a.req.Kind == jobs.KindTriage {
		switch res.confidence() {
		case "suspected", "not_reproducible":
			return plan{statusNeedsHuman, commentReason}
		}
		return plan{statusReady, ""}
	}
	switch {
	case res.Outcome == "parked":
		return plan{statusNeedsHuman, commentFixSummary}
	case res.land(a.settings.Land) == "none":
		return plan{"", commentFixSummary}
	}
	return plan{statusLanded, commentFixSummary}
}

// automaticAttemptFollows says whether the jobs layer starts another attempt by itself, which
// then ends the job or not: the envelope is failed, of category brain or no_result, retryable,
// and the attempt is not the last of its round.
func (a *attempt) automaticAttemptFollows(env toolio.Envelope) bool {
	e := env.Error
	return env.Status == toolio.StatusFailed && e != nil && e.Retryable &&
		(e.Category == toolio.CategoryBrain || e.Category == toolio.CategoryNoResult) &&
		max(a.req.Round, 1) < a.maxAttempts
}
