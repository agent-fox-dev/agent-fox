package poll

import (
	"context"
	"fmt"
	"strings"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

// The replies of the forge path: a person who put the label back gets told on the issue why it did nothing. The service
// edits no comment of its own (hub's forge API has no edit), so each reply is a comment with a marker of its own, which
// keeps it out of later answers.

// The kinds of the replies, in their markers.
const (
	replyNoAnswer  = "no_answer"
	replyThreadCut = "thread_cut"
)

// replyMarker is the marker of a reply about the job's attempt.
func replyMarker(job string, attempt int, kind string) string {
	return fmt.Sprintf("<!-- agent-fox job=%s attempt=%d kind=%s -->", job, attempt, kind)
}

// noAnswer answers a sighting of a blocked job that found no comment of a person since its question: one reply says so
// and asks for a comment and the label again, unless the latest comment of the issue is that reply already, and the label
// is consumed. No attempt is made.
func (c *cycleRun) noAnswer(ctx context.Context, l *listing, n int, job jobs.Doc, r read) bool {
	body := fmt.Sprintf("No answer found since the question; comment, then add the label `%s` again.", l.Label)
	c.reply(ctx, l, n, job, r, replyNoAnswer, body)
	c.s.cfg.Logger.Info("a request label found no answer since the question of a blocked job",
		"workspace", c.slug, "kind", l.Kind, "issue", n, "label", l.Label, "job", job.ID)
	return c.consume(ctx, l, n, owns(job, l.Label))
}

// threadCut answers a sighting whose issue read hub cut: the comments read may not be all of them, so nothing is taken
// from them. One WARN, one reply that names af and the API for the job, and the label is consumed; no attempt is made.
func (c *cycleRun) threadCut(ctx context.Context, l *listing, n int, job jobs.Doc, r read, action string) bool {
	c.s.cfg.Logger.Warn("hub returned only part of the comments of the issue, so the forge path took nothing from them; a reply names af and the API",
		"workspace", c.slug, "issue", n, "job", job.ID, "label", l.Label)
	body := fmt.Sprintf("agent-fox could not read the whole thread of this issue: hub returned only part of its comments, so agent-fox took nothing from them. "+
		"Use `af jobs %s %s`, or `POST /api/v1/jobs/%s/%s`.", action, job.ID, job.ID, action)
	c.reply(ctx, l, n, job, r, replyThreadCut, body)
	return c.consume(ctx, l, n, owns(job, l.Label))
}

// reply posts the reply of the kind on the issue, ending with its marker, unless the latest comment of the issue carries
// that marker already, so that a sighting of the poll and one of the stream do not both post it. A reply that cannot be
// posted is a WARN, and the label is consumed all the same.
func (c *cycleRun) reply(ctx context.Context, l *listing, n int, job jobs.Doc, r read, kind, body string) {
	marker := replyMarker(job.ID, r.attempt, kind)
	if cs := r.thread.Comments; len(cs) > 0 && strings.Contains(cs[len(cs)-1].Body, marker) {
		return
	}
	if _, err := c.forge.AddComment(ctx, issuex.IssueRef{Repo: c.repo, Number: n}, body+"\n\n"+marker+"\n"); err != nil {
		c.s.cfg.Logger.Warn("the reply of the forge path could not be posted; the label is removed all the same",
			"workspace", c.slug, "issue", n, "job", job.ID, "label", l.Label, "reply", kind, "error", err.Error())
	}
}
