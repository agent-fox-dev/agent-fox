package poll

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

// issueHandler applies the rules of the poll to the issues of a listing, one at a time.
type issueHandler struct{}

// handle takes the issues of the listing in order, and says whether the cycle goes on. When it has taken them all, a
// complete listing settles the removals that are owed for issues it does not contain.
func (issueHandler) handle(ctx context.Context, c *cycleRun, l *listing) bool {
	for _, n := range l.Issues {
		if ctx.Err() != nil || !c.lockedIssue(ctx, l, n) {
			return false
		}
	}
	if l.Complete {
		c.settle(ctx, l)
	}
	c.s.drop(ctx, c.slug)
	return true
}

// report is the failure of the rules for an issue: the cycle's, or, for an event of the stream, the stream's.
func (c *cycleRun) report(ctx context.Context, f failure) {
	c.failed = true
	if c.onFail != nil {
		c.onFail(f)
		return
	}
	c.s.report(ctx, c.slug, c.st, f)
}

// newer says that the record was made at or after the listing was asked for: after the last record there was then, or at
// a later time. The stream's issue of an event is no listing, and has no newer record.
func (l *listing) newer(r Record) bool {
	return !l.Requested.IsZero() && (r.ID > l.Before || r.CreatedAt.After(l.Requested))
}

// lockedIssue applies the rules to issue n under the workspace's lock, which the event stream takes for an event, and
// gives it back before the next issue.
func (c *cycleRun) lockedIssue(ctx context.Context, l *listing, n int) bool {
	unlock := c.s.lockWorkspace(c.slug)
	defer unlock()
	return c.issue(ctx, l, n)
}

// request is the request to make a job of kind K for issue N from label L: the issue as its input, a source that names the
// label, and the label, which the poll consumes.
// A listing that names a source of its own (an event of hub's stream) makes the job with it.
func request(workspace string, l *listing, n int) jobs.CreateRequest {
	src := l.Source
	if src.Kind == "" {
		src = jobs.Source{Kind: "label", By: "label " + l.Label}
	}
	return jobs.CreateRequest{
		Workspace: workspace, Kind: l.Kind, Body: jobs.Body{Issue: json.RawMessage(strconv.Itoa(n))},
		Source: src, RequestLabel: l.Label,
	}
}

// issue applies the rules to issue n of the listing, and says whether the cycle goes on. The first rule that applies
// decides:
//
//  1. an open job of the kind for the issue absorbs the label: it is removed and no job is made;
//  2. a hold on the label for the issue is in place, because a job the poll made was cancelled while its attempt was in a
//     runner and the worker put the label back: nothing is done, and the label stays until a person asks again;
//  3. the removal of the label from the issue is still owed, for a job the poll made or for a label it absorbed: it is
//     removed and no job is made;
//  4. otherwise it is a new request, and a job is made.
func (c *cycleRun) issue(ctx context.Context, l *listing, n int) bool {

	recs, err := c.s.records.Find(ctx, c.slug, n, l.Label)
	if err != nil {
		c.report(ctx, failure{
			row: rowStore, operation: fmt.Sprintf("read the records of %s on the issue %d, so the label stays", l.Label, n), err: err,
		})
		return false
	}
	// A job may have been cancelled since the cycle began, and its cleanup may have put the label back: the records of
	// this label are brought up to date once more before it is taken for a new request. If a job cannot be read, nothing
	// is known of what became of it, and the label is not taken.
	for i := range recs {
		if recs[i], err = c.s.note(ctx, recs[i]); err != nil {
			c.report(ctx, failure{
				row: rowStore, operation: fmt.Sprintf("read how the job of %s on the issue %d stands, so the label stays", l.Label, n), err: err,
			})
			return false
		}
	}
	open, err := c.s.cfg.Jobs.OpenJob(ctx, c.slug, l.Kind, int64(n))
	switch {
	case err == nil:
		return c.absorb(ctx, l, n, open.ID)
	case !errors.Is(err, jobs.ErrNotFound):
		c.report(ctx, failure{
			row: rowJobs, operation: fmt.Sprintf("look up the open job for %s on the issue %d, so the label stays", l.Label, n), err: err,
		})
		return false
	}
	if slices.ContainsFunc(recs, func(r Record) bool { return r.Seen == SeenHolding }) {
		return true
	}
	if slices.ContainsFunc(recs, func(r Record) bool { return r.Owed }) {
		return c.retake(ctx, l, n)
	}
	// A record of the label on the issue made at or after the listing was asked for is newer than what the listing
	// shows: the other trigger took the label since. The label is left to the next cycle.
	if slices.ContainsFunc(recs, l.newer) {
		return true
	}
	return c.request(ctx, l, n)
}

// request makes the job for a new request, and consumes the label. If the jobs layer refuses with a duplicate, a job of
// the kind for the issue was made in the meantime, and the label is absorbed.
func (c *cycleRun) request(ctx context.Context, l *listing, n int) bool {
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
		c.notSubscribed = true
		return false
	}
	c.report(ctx, failure{
		row: rowJobs, operation: fmt.Sprintf("make a job for %s on the issue %d, so the label stays on the issue", l.Label, n), err: err,
	})
	return false
}

// retake removes a label whose removal is owed: the label was taken already, so no job is made.
func (c *cycleRun) retake(ctx context.Context, l *listing, n int) bool {
	removed, more := c.removeLabel(ctx, l, n)
	if removed {
		c.confirm(ctx, l, n)
	}
	return more
}

// settle uses a listing that is complete, which holds every issue that carries the label. An issue that it does not
// contain does not carry the label, whoever took it off, so nothing is owed for it, and a hold on the label for it is
// released, provided the listing was requested at least the release delay after the release clock of the hold started:
// until then the runner may still be writing the label, and the forge's list may not show a label just put back yet.
func (c *cycleRun) settle(ctx context.Context, l *listing) {
	unlock := c.s.lockWorkspace(c.slug)
	defer unlock()
	recs, err := c.s.records.ForWorkspace(ctx, c.slug)
	if err != nil {
		c.s.cfg.Logger.Warn("the records of the poll could not be read, so the owed removals are not settled",
			"workspace", c.slug, "label", l.Label, "error", err.Error())
		return
	}
	settled := map[int]bool{}
	for _, r := range recs {
		// A record made at or after the listing was asked for is newer than what the listing says of its issue.
		if r.Label != l.Label || slices.Contains(l.Issues, r.Issue) || l.newer(r) {
			continue
		}
		if r.Seen == SeenHolding && !r.ReleaseClock.IsZero() && !l.Requested.Before(r.ReleaseClock.Add(c.s.cfg.ReleaseDelay)) {
			c.s.see(ctx, r, SeenEnded)
		}
		if !r.Owed || settled[r.Issue] {
			continue
		}
		settled[r.Issue] = true
		if _, err := c.s.records.ClearOwed(ctx, c.slug, r.Issue, l.Label); err != nil {
			c.s.cfg.Logger.Warn("an owed removal could not be settled",
				"workspace", c.slug, "issue", r.Issue, "label", l.Label, "error", err.Error())
		}
	}
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
	removed, more := c.removeLabel(ctx, l, n)
	if removed && werr == nil {
		c.confirm(ctx, l, n)
	}
	return more
}

// absorb removes a request label from an issue that has an open job of the kind, and makes no job. If the removal fails
// it is owed, with no job.
func (c *cycleRun) absorb(ctx context.Context, l *listing, n int, jobID string) bool {
	c.s.cfg.Logger.Info("a request label was absorbed by an open job", "workspace", c.slug, "kind", l.Kind, "issue", n, "label", l.Label, "job", jobID)
	removed, more := c.removeLabel(ctx, l, n)
	if removed {
		c.confirm(ctx, l, n)
	} else {
		c.owe(ctx, l, n)
	}
	return more
}

// removeLabel removes the label from the issue through hub. It says whether the label is gone, which is also what hub says
// when it does not find it, and whether the cycle goes on. A removal that fails is logged and stays owed; the cycle goes
// on with the next issue only if hub rejected the request, since an issue of its own cannot make the others fail, and ends
// if hub cannot be asked or does not accept the token.
func (c *cycleRun) removeLabel(ctx context.Context, l *listing, n int) (removed, more bool) {
	err := c.forge.RemoveLabel(ctx, issuex.IssueRef{Repo: c.repo, Number: n}, l.Label)
	if err == nil || issuex.IsNotFound(err) {
		return true, true
	}
	f := classify(fmt.Sprintf("remove the label %s from the issue %d", l.Label, n), err, true)
	c.report(ctx, f)
	return false, f.row == rowRemoval
}

// confirm clears every owed removal of the label from the issue: it was confirmed.
func (c *cycleRun) confirm(ctx context.Context, l *listing, n int) {
	if _, err := c.s.records.ClearOwed(ctx, c.slug, n, l.Label); err != nil {
		c.s.cfg.Logger.Warn("the record of a request label could not be updated",
			"workspace", c.slug, "issue", n, "label", l.Label, "error", err.Error())
	}
}

// owe writes the record of a removal that failed for a label that was absorbed, unless the removal is owed already.
func (c *cycleRun) owe(ctx context.Context, l *listing, n int) {
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
