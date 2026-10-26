package poll

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

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

// report is the failure of the rules for an issue.
func (c *cycleRun) report(ctx context.Context, f failure) {
	c.failed = true
	c.s.report(ctx, c.slug, c.st, f)
}

// newer says that the record was made at or after the listing was asked for: after the last record there was then, or at
// a later time: a cycle of a loop that was replaced took the label since.
func (l *listing) newer(r Record) bool {
	return !l.Requested.IsZero() && (r.ID > l.Before || r.CreatedAt.After(l.Requested))
}

// lagging says that the record guards its label against a sighting that the forge's index may still show only because it
// lags the removal: the record is of an answer or a retry, and the listing was asked for before the release delay passed since
// it was made. A listing asked for before the record is covered by newer.
func (l *listing) lagging(r Record, delay time.Duration) bool {
	return r.Guard && !l.Requested.IsZero() && l.Requested.Before(r.CreatedAt.Add(delay))
}

// lockedIssue applies the rules to issue n under the workspace's lock, and gives it back before the next issue.
func (c *cycleRun) lockedIssue(ctx context.Context, l *listing, n int) bool {
	unlock := c.s.lockWorkspace(c.slug)
	defer unlock()
	return c.issue(ctx, l, n)
}

// request is the request to make a job of kind K for issue N from label L: the issue as its input, a source that names the
// label, and the label, which the poll consumes.
func request(workspace string, l *listing, n int) jobs.CreateRequest {
	return jobs.CreateRequest{
		Workspace: workspace, Kind: l.Kind, Body: jobs.Body{Issue: json.RawMessage(strconv.Itoa(n))},
		Source: jobs.Source{Kind: "label", By: l.by()}, RequestLabel: l.Label,
	}
}

// issue applies the rules to issue n of the listing, the sighting of its label, and says whether the cycle goes on. The
// first rule that applies decides:
//
//  0. a fix label on an issue whose pending follow-up a triage recorded becomes the fix job linked to the triage, or is
//     absorbed by an open fix job (followup.go);
//  1. an open job of the kind for the issue: a blocked one with no removal of the label owed is answered from the
//     forge, with the comments since its question, unless a record of the label is newer than the sighting, or guards
//     the label for the release delay after an answer or a retry (it may only lag the removal), which leaves
//     the label to the next cycle; any other absorbs the label, which is removed and no job is made;
//  2. a hold on the label for the issue is in place, because a job the poll made was cancelled while its attempt was in a
//     runner and the worker put the label back: nothing is done, and the label stays until a person asks again;
//  3. the removal of the label from the issue is still owed, for a job the poll made or for a label it absorbed: it is
//     removed and no job is made;
//  4. a record of the label on the issue is newer than the sighting: another cycle took the label since, and it is
//     left to the next cycle; so is a label that a guard covers, as in rule 1;
//  5. the latest job of the kind for the issue failed: it is retried from the forge, with the comments since its failure;
//  6. otherwise it is a new request, and a job is made.
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
	owed := slices.ContainsFunc(recs, func(r Record) bool { return r.Owed })
	open, err := c.s.cfg.Jobs.OpenJob(ctx, c.slug, l.Kind, int64(n))
	switch {
	case err == nil && open.State == jobs.StateBlocked && !owed:
		if slices.ContainsFunc(recs, c.leaves(l)) {
			return true
		}
		return c.answer(ctx, l, n, open)
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
	if owed {
		return c.retake(ctx, l, n)
	}
	// A record of the label on the issue made at or after the listing was asked for is newer than what the listing
	// shows: the other trigger took the label since. The label is left to the next cycle.
	if slices.ContainsFunc(recs, c.leaves(l)) {
		return true
	}
	latest, err := c.s.cfg.Jobs.LatestJob(ctx, c.slug, l.Kind, int64(n))
	switch {
	case err == nil && latest.State == jobs.StateFailed:
		return c.retry(ctx, l, n, latest)
	case err != nil && !errors.Is(err, jobs.ErrNotFound):
		c.report(ctx, failure{
			row: rowJobs, operation: fmt.Sprintf("look up the latest job for %s on the issue %d, so the label stays", l.Label, n), err: err,
		})
		return false
	}
	return c.request(ctx, l, n)
}

// leaves says whether the record leaves the label of the sighting to the next cycle: the record is newer than the
// listing, or guards the label against a listing that may lag the removal of an answer or a retry.
func (c *cycleRun) leaves(l *listing) func(Record) bool {
	return func(r Record) bool { return l.newer(r) || l.lagging(r, c.s.cfg.ReleaseDelay) }
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
		if r.Guard {
			// The listing was asked for after the record and lacks the label: the forge's index shows the removal.
			if _, err := c.s.records.ClearGuard(ctx, c.slug, r.Issue, l.Label); err != nil {
				c.s.cfg.Logger.Warn("a guard of a request label could not be released",
					"workspace", c.slug, "issue", r.Issue, "label", l.Label, "error", err.Error())
			}
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
	return c.consume(ctx, l, n, jobID, false)
}

// consume consumes the label of a sighting that was acted on: a new request, an answer, a retry or a reply. The record
// is written before the removal, so that a removal that fails, or a service that ends, leaves the label owed; it names
// the job when the label is the job's own request label, which a cancel puts back, and no job otherwise. If it cannot be
// written the removal goes ahead all the same. The record of an answer or a retry (guard) is kept as a guard once the
// removal is confirmed and the job ended, until the forge is known to show the label gone. A reply needs none: it is
// not repeated for the same thread, since it carries a marker, and it changes no job.
func (c *cycleRun) consume(ctx context.Context, l *listing, n int, jobID string, guard bool) bool {
	seen := SeenOpen
	if jobID == "" {
		seen = SeenEnded
	}
	_, werr := c.s.records.Insert(ctx, Record{
		Workspace: c.slug, Issue: n, Label: l.Label, JobID: jobID, Owed: true, Seen: seen, Guard: guard, CreatedAt: c.s.cfg.Clock.Now(),
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
