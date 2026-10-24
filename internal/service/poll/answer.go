package poll

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

// The forge's answer to a blocked job: a person comments on the issue and puts the request label back, and the comments
// since the job's question become the answer, as POST /api/v1/jobs/{id}/answer would give it.

// by is who a sighting names: the hub event of the stream, or the label for the poll, as the source of a new job would.
func (l *listing) by() string {
	if l.Source.By != "" {
		return l.Source.By
	}
	return "label " + l.Label
}

// thread reads issue n with its comments, through hub's issue read, which says when hub cut the comments or could not
// read them; a sighting of the stream takes the read it made. It says whether the thread can be read, and whether the
// cycle goes on: hub unavailable, or refusing or forbidding the token, ends it as a failed call of hub does, and a
// rejection of this one issue, or comments hub could not read, are reported and the cycle goes on with the next issue.
// Either way the label stays, so that the next sighting tries again.
func (c *cycleRun) thread(ctx context.Context, l *listing, n int) (thread issuex.IssueThread, ok, more bool) {
	operation := fmt.Sprintf("read the comments of the issue %d, so %s stays on it", n, l.Label)
	if l.Thread != nil && l.Thread.Issue.Number == n {
		thread = *l.Thread
	} else {
		var err error
		thread, err = c.forge.ReadIssue(ctx, issuex.IssueRef{Repo: c.repo, Number: n})
		if err != nil {
			f := classify(operation, err, false)
			if f.row == rowDenied && f.status == http.StatusNotFound {
				// The workspace was read for this cycle: what hub does not find is the issue.
				f.row, f.message = rowRejected, "hub does not find the issue"
			}
			c.report(ctx, f)
			// A rejection of the one issue lets the cycle go on; hub's 502 for a forge that refuses its credential (a
			// rejection with a 5xx status) would fail every issue, and ends it.
			return thread, false, f.row == rowRejected && f.status < http.StatusInternalServerError
		}
	}
	if thread.CommentsErr != nil {
		c.report(ctx, failure{row: rowRejected, operation: operation, err: thread.CommentsErr, message: thread.CommentsErr.Error()})
		return thread, false, true
	}
	return thread, true, true
}

// latest is the job's latest attempt as its document shows it, and when it finished: the anchor of the comments that
// count when no comment carries its marker.
func latest(doc jobs.Doc) (n int, finished time.Time) {
	n = doc.CurrentAttempt
	for _, a := range doc.Attempts {
		if a.N == n && a.FinishedAt != nil {
			finished, _ = time.Parse(time.RFC3339Nano, *a.FinishedAt)
		}
	}
	return n, finished
}

// read is what a sighting read of the comments of an issue for a job: the thread, the job's latest attempt, and the text
// of the comments a person wrote since that attempt.
type read struct {
	thread  issuex.IssueThread
	attempt int
	text    string
}

// since reads the comments a person wrote on the issue since the job's latest attempt. When hub cut the thread the
// comments read are no evidence of what is there, and the text is empty.
func (c *cycleRun) since(ctx context.Context, l *listing, n int, job jobs.Doc) (r read, ok, more bool) {
	r.thread, ok, more = c.thread(ctx, l, n)
	if !ok {
		return r, false, more
	}
	doc, err := c.s.cfg.Jobs.Get(ctx, job.ID)
	if err != nil {
		c.report(ctx, failure{row: rowJobs, operation: fmt.Sprintf("read the job %s of %s on the issue %d, so the label stays", job.ID, l.Label, n), err: err})
		return r, false, false
	}
	var finished time.Time
	r.attempt, finished = latest(doc)
	if !r.thread.Truncated {
		r.text = bound(render(counted(r.thread.Comments, doc.ID, r.attempt, finished)))
	}
	return r, true, true
}

// owns says whether the label is the job's own request label, which its cancel puts back: only then does the record of a
// sighting the job served name it, and hold the label after a cancel.
func owns(job jobs.Doc, label string) string {
	if job.RequestLabel != nil && *job.RequestLabel == label {
		return job.ID
	}
	return ""
}

// answer answers the blocked job with the comments a person wrote since its question, and consumes the label. A job that
// moved meanwhile absorbs it; a workspace that is not subscribed ends the cycle with nothing written; any other refusal is
// reported, and the label stays.
func (c *cycleRun) answer(ctx context.Context, l *listing, n int, job jobs.Doc) bool {
	r, ok, more := c.since(ctx, l, n, job)
	switch {
	case !ok:
		return more
	case r.thread.Truncated:
		return c.threadCut(ctx, l, n, job, r, "answer")
	case r.text == "":
		return c.noAnswer(ctx, l, n, job, r)
	}
	_, err := c.s.cfg.Jobs.Answer(ctx, job.ID, l.by(), jobs.AnswerRequest{Answer: &r.text})
	var state *jobs.StateError
	switch {
	case err == nil:
	case errors.As(err, &state):
		return c.absorb(ctx, l, n, job.ID)
	case errors.Is(err, jobs.ErrNotSubscribed):
		c.notSubscribed = true
		return false
	default:
		c.report(ctx, failure{row: rowJobs, operation: fmt.Sprintf("answer the blocked job of %s on the issue %d, so the label stays", l.Label, n), err: err})
		return false
	}
	c.s.cfg.Logger.Info("a request label answered a blocked job with the comments since its question",
		"workspace", c.slug, "kind", l.Kind, "issue", n, "label", l.Label, "job", job.ID)
	return c.consume(ctx, l, n, owns(job, l.Label))
}

// retry asks again for the failed job, the latest of its kind for the issue, with the comments a person wrote since its
// failure as the new attempt's context, none when there are none, and consumes the label. Another job that took the
// issue, or a job that moved meanwhile, absorbs it; a workspace that is not subscribed ends the cycle with nothing
// written; any other refusal is reported, and the label stays.
func (c *cycleRun) retry(ctx context.Context, l *listing, n int, job jobs.Doc) bool {
	r, ok, more := c.since(ctx, l, n, job)
	switch {
	case !ok:
		return more
	case r.thread.Truncated:
		return c.threadCut(ctx, l, n, job, r, "retry")
	}
	var notes []string
	if r.text != "" {
		notes = []string{r.text}
	}
	_, err := c.s.cfg.Jobs.RetryWithContext(ctx, job.ID, l.by(), notes)
	var (
		state *jobs.StateError
		dup   *jobs.DuplicateError
	)
	switch {
	case err == nil:
	case errors.As(err, &dup):
		return c.absorb(ctx, l, n, dup.JobID)
	case errors.As(err, &state), errors.Is(err, jobs.ErrDuplicate):
		return c.absorb(ctx, l, n, job.ID)
	case errors.Is(err, jobs.ErrNotSubscribed):
		c.notSubscribed = true
		return false
	default:
		c.report(ctx, failure{row: rowJobs, operation: fmt.Sprintf("retry the failed job of %s on the issue %d, so the label stays", l.Label, n), err: err})
		return false
	}
	c.s.cfg.Logger.Info("a request label retried a failed job with the comments since its failure",
		"workspace", c.slug, "kind", l.Kind, "issue", n, "label", l.Label, "job", job.ID)
	return c.consume(ctx, l, n, owns(job, l.Label))
}
