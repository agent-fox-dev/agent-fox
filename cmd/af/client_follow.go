package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/agent-fox-dev/agentfox/internal/afclient"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// warnAttemptEnvelopeUnavailable: the job is at rest and the envelope of its last attempt could not be
// fetched, so the result has none.
const warnAttemptEnvelopeUnavailable = "attempt_envelope_unavailable"

// jobDoc is what the commands read of the document of a job at rest. Its result is that of the
// latest attempt that finished: its status, summary and error come from that attempt's envelope.
type jobDoc struct {
	State  string `json:"state"`
	Result *struct {
		Status  string            `json:"status"`
		Summary string            `json:"summary"`
		Error   *toolio.ErrorInfo `json:"error"`
	} `json:"result"`
	Blocked *struct {
		Question string `json:"question"`
		Options  []struct {
			Label string `json:"label"`
		} `json:"options"`
	} `json:"blocked"`
}

// followJob follows the job id, whose artifact the command has recorded, from the event after
// since until it is at rest, and ends the command by what the job came to. echo writes each event to
// stderr as well as emitting it; watch does and --wait does not.
//
// At rest it fetches the envelope of the last finished attempt, appends that envelope's warnings
// to the command's own, and gives {job, envelope}: done or unverified for a job that is done (done
// when it has no result), needs_human for a blocked job, answered by af jobs answer <id> --wait, and
// the failure of the result for a job that failed. A job cancelled on the service is the category
// job_cancelled. A following that the shell stops, by a signal or by --timeout, never cancels the
// job; it names af jobs watch and af jobs cancel in next, and returns the error of the context to
// the shell.
func followJob(r *toolio.Run, client *afclient.Client, id string, since int64, echo bool) (toolio.Outcome, error) {
	srv := client.Server()
	interrupted := func(err error) error {
		if r.Ctx.Err() == nil || !errors.Is(err, r.Ctx.Err()) {
			return nil
		}
		r.Next(toolio.Next{Argv: withServer(srv, "af", "jobs", "watch", id), Reason: "the job may still be running: follow it again"})
		r.Next(toolio.Next{Argv: withServer(srv, "af", "jobs", "cancel", id), Reason: "cancel the job on the service, if it is no longer wanted"})
		return err
	}

	rest, err := client.Follow(r.Ctx, id, since, func(ev afclient.Event) { emitEvent(r, ev, echo) })
	if err != nil {
		if stop := interrupted(err); stop != nil {
			return toolio.Outcome{}, stop
		}
		return toolio.Outcome{}, followFailure(r, srv, id, err)
	}
	envelope, unavailable, err := client.Envelope(r.Ctx, id, rest.Job)
	if err != nil {
		return toolio.Outcome{}, interrupted(err)
	}
	if unavailable != "" {
		r.Warn(warnAttemptEnvelopeUnavailable, unavailable)
	}
	if envelope != nil {
		var doc struct {
			Warnings []toolio.Warning `json:"warnings"`
		}
		if json.Unmarshal(envelope, &doc) == nil {
			for _, w := range doc.Warnings {
				r.Warn(w.Code, w.Message)
			}
		}
	}
	if envelope == nil {
		envelope = nullEnvelope
	}
	return jobOutcome(srv, id, jobResult{Job: rest.Job, Envelope: envelope})
}

// jobOutcome is how the command ends for the job at rest of the result.
func jobOutcome(srv afclient.Server, id string, result jobResult) (toolio.Outcome, error) {
	var doc jobDoc
	if err := json.Unmarshal(result.Job, &doc); err != nil {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryService, Message: fmt.Sprintf("the document of the job %s is not the JSON expected", id)}
	}
	summary := fmt.Sprintf("the job %s is %s", id, doc.State)
	if doc.Result != nil && doc.Result.Summary != "" {
		summary = doc.Result.Summary
	}

	switch doc.State {
	case "blocked":
		question := fmt.Sprintf("the job %s waits for an answer", id)
		var labels []string
		if b := doc.Blocked; b != nil {
			if b.Question != "" {
				question = b.Question
			}
			for _, o := range b.Options {
				labels = append(labels, o.Label)
			}
		}
		return toolio.Outcome{
			Status: toolio.StatusNeedsHuman, Summary: summary, Result: result,
			NeedsHuman: &toolio.Question{Text: question, Options: labels, ResumeArgv: withServer(srv, "af", "jobs", "answer", id, "--wait")},
		}, nil
	case "cancelled":
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryJobCancelled, Message: fmt.Sprintf("the job %s was cancelled on the service", id)}
	case "failed":
		status := ""
		if doc.Result != nil {
			status = doc.Result.Status
		}
		if status == "unverified" {
			return toolio.Outcome{Status: toolio.StatusUnverified, Summary: summary, Result: result}, nil
		}
		return toolio.Outcome{}, jobFailure(id, doc)
	}
	if doc.Result != nil && doc.Result.Status == "unverified" {
		return toolio.Outcome{Status: toolio.StatusUnverified, Summary: summary, Result: result}, nil
	}
	return toolio.Outcome{Status: toolio.StatusDone, Summary: summary, Result: result}, nil
}

// jobFailure is the failure of a job that failed: the error of its result, with its category,
// message, retryable flag and fix hint, or an internal failure when the result has no error. A
// result of status usage is a usage failure.
func jobFailure(id string, doc jobDoc) error {
	if doc.Result == nil || doc.Result.Error == nil {
		return &toolio.Failure{Category: toolio.CategoryInternal, Message: fmt.Sprintf("the job %s failed on the service and its result has no error", id)}
	}
	e := doc.Result.Error
	f := &toolio.Failure{Category: e.Category, Message: e.Message, Retryable: e.Retryable, FixHint: e.FixHint}
	if doc.Result.Status == "usage" {
		f.Category = toolio.CategoryUsage
	}
	return f
}

// emitEvent emits an event of the job as job_event, and writes it to stderr when echo is set.
func emitEvent(r *toolio.Run, ev afclient.Event, echo bool) {
	r.Emit("job_event", ev.Document)
	if echo {
		echoEvent(r, ev)
	}
}
