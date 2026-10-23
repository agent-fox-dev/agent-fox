package poll

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

// issueHandler applies the rules of the poll to the issues of a listing, one at a time.
type issueHandler struct{}

// handle takes the issues of the listing in order, and says whether the cycle goes on.
func (issueHandler) handle(ctx context.Context, c *cycleRun, l *listing) bool {
	for _, n := range l.Issues {
		if ctx.Err() != nil || !c.issue(ctx, l, n) {
			return false
		}
	}
	return true
}

// request is the request to make a job of kind K for issue N from label L: the issue as its input, a source that names the
// label, and the label, which the poll consumes.
func request(workspace string, l *listing, n int) jobs.CreateRequest {
	return jobs.CreateRequest{
		Workspace: workspace, Kind: l.Kind, Body: jobs.Body{Issue: json.RawMessage(strconv.Itoa(n))},
		Source: jobs.Source{Kind: "label", By: "label " + l.Label}, RequestLabel: l.Label,
	}
}

// issue applies the rules to issue n of the listing, and says whether the cycle goes on.
func (c *cycleRun) issue(ctx context.Context, l *listing, n int) bool {
	doc, err := c.s.cfg.Jobs.Create(ctx, request(c.slug, l, n))
	var dup *jobs.DuplicateError
	switch {
	case err == nil:
		return c.created(ctx, l, n, doc.ID)
	case errors.As(err, &dup):
		return c.absorb(ctx, l, n, dup.JobID)
	case errors.Is(err, jobs.ErrDuplicate):
		return c.absorb(ctx, l, n, "")
	case errors.Is(err, jobs.ErrNotSubscribed):
		// The workspace is being unsubscribed, or is gone: nothing is written to its issues.
		return false
	}
	c.s.cfg.Logger.Warn("a job could not be made for a request label, so the label stays on the issue and the cycle ends",
		"workspace", c.slug, "kind", l.Kind, "issue", n, "label", l.Label, "error", err.Error())
	return false
}

// created records the job that was made for the label, and consumes the label.
func (c *cycleRun) created(ctx context.Context, l *listing, n int, jobID string) bool {
	c.s.cfg.Logger.Info("a request label became a job", "workspace", c.slug, "kind", l.Kind, "issue", n, "label", l.Label, "job", jobID)
	// The record is written before the removal, so that a removal that fails, or a service that ends, leaves the label
	// owed. If it cannot be written the removal goes ahead all the same.
	_, werr := c.s.records.Insert(ctx, Record{
		Workspace: c.slug, Issue: n, Label: l.Label, JobID: jobID, Owed: true, Seen: SeenOpen, CreatedAt: c.s.cfg.Clock.Now(),
	})
	if werr != nil {
		c.s.cfg.Logger.Warn("the record of a request label could not be written, so a removal that fails will not be owed",
			"workspace", c.slug, "issue", n, "label", l.Label, "job", jobID, "error", werr.Error())
	}
	c.consume(ctx, l, n, werr == nil)
	return true
}

// absorb removes a request label from an issue that has an open job of the kind, and makes no job. If the removal fails
// it is owed, with no job.
func (c *cycleRun) absorb(ctx context.Context, l *listing, n int, jobID string) bool {
	c.s.cfg.Logger.Info("a request label was absorbed by an open job", "workspace", c.slug, "kind", l.Kind, "issue", n, "label", l.Label, "job", jobID)
	if err := c.removeLabel(ctx, l, n); err != nil {
		c.owe(ctx, l, n, err)
	}
	return true
}

// consume removes the label of a new request from the issue and, if that fails, leaves the removal owed on the record that
// was written for it.
func (c *cycleRun) consume(ctx context.Context, l *listing, n int, recorded bool) {
	err := c.removeLabel(ctx, l, n)
	if err != nil {
		c.s.cfg.Logger.Warn("a request label could not be removed from its issue, so the removal stays owed",
			"workspace", c.slug, "issue", n, "label", l.Label, "error", err.Error())
		return
	}
	if recorded {
		c.confirm(ctx, l, n)
	}
}

// removeLabel removes the label from the issue through hub: it is nil when the label is gone, which is also what hub says
// when it does not find it.
func (c *cycleRun) removeLabel(ctx context.Context, l *listing, n int) error {
	err := c.forge.RemoveLabel(ctx, issuex.IssueRef{Repo: c.repo, Number: n}, l.Label)
	if err != nil && issuex.IsNotFound(err) {
		return nil
	}
	return err
}

// confirm clears every owed removal of the label from the issue: it was confirmed.
func (c *cycleRun) confirm(ctx context.Context, l *listing, n int) {
	if _, err := c.s.records.ClearOwed(ctx, c.slug, n, l.Label); err != nil {
		c.s.cfg.Logger.Warn("the record of a request label could not be updated",
			"workspace", c.slug, "issue", n, "label", l.Label, "error", err.Error())
	}
}

// owe writes the record of a removal that failed for a label that was absorbed, unless the removal is owed already.
func (c *cycleRun) owe(ctx context.Context, l *listing, n int, cause error) {
	c.s.cfg.Logger.Warn("an absorbed request label could not be removed from its issue, so the removal is owed",
		"workspace", c.slug, "issue", n, "label", l.Label, "error", cause.Error())
	owed, err := c.s.records.Find(ctx, c.slug, n, l.Label)
	if err == nil {
		for _, r := range owed {
			if r.Owed {
				return
			}
		}
		_, err = c.s.records.Insert(ctx, Record{Workspace: c.slug, Issue: n, Label: l.Label, Owed: true, Seen: SeenEnded, CreatedAt: c.s.cfg.Clock.Now()})
	}
	if err != nil {
		c.s.cfg.Logger.Warn("the record of an owed removal could not be written",
			"workspace", c.slug, "issue", n, "label", l.Label, "error", err.Error())
	}
}
