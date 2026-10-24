package poll

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

// The trigger's side of AF_AUTO_FIX: a fix label on an issue whose pending follow-up a triage job recorded is the
// triage's request, put there by the worker, and becomes the fix job linked to the triage. It comes before every other
// rule: it is fresh evidence of a request, which a hold of the label would otherwise keep waiting for a person, and
// the service's own label answers no blocked job.

// followUp applies the follow-up rule to a fix label on issue n, and says whether it decided the sighting, and whether
// the cycle goes on. An open fix job absorbs the label and the follow-up; otherwise the linked fix job is made, and the
// follow-up made with it, every hold of the label on the issue ends, and the label is consumed. A duplicate refusal
// absorbs both; a follow-up that is no longer pending leaves the sighting to the other rules.
func (c *cycleRun) followUp(ctx context.Context, l *listing, n int, recs []Record) (decided, more bool) {
	f, err := c.s.cfg.Jobs.PendingFollowUp(ctx, c.slug, int64(n))
	switch {
	case errors.Is(err, jobs.ErrNotFound):
		return false, true
	case err != nil:
		c.report(ctx, failure{row: rowJobs, operation: fmt.Sprintf("look up the follow-up of the issue %d, so %s stays", n, l.Label), err: err})
		return true, false
	}
	open, err := c.s.cfg.Jobs.OpenJob(ctx, c.slug, jobs.KindFix, int64(n))
	switch {
	case err == nil:
		return true, c.absorbFollowUp(ctx, l, n, f, open.ID)
	case !errors.Is(err, jobs.ErrNotFound):
		c.report(ctx, failure{row: rowJobs, operation: fmt.Sprintf("look up the open job for %s on the issue %d, so the label stays", l.Label, n), err: err})
		return true, false
	}
	doc, err := c.s.cfg.Jobs.CreateLinked(ctx, f.TriageJobID, jobs.CreateRequest{
		Workspace: c.slug, Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage(strconv.Itoa(n))},
		Source: jobs.Source{Kind: "triage", By: f.TriageJobID}, ParentJobID: f.TriageJobID, RequestLabel: l.Label,
	})
	var dup *jobs.DuplicateError
	switch {
	case err == nil:
	case errors.As(err, &dup):
		return true, c.absorbFollowUp(ctx, l, n, f, dup.JobID)
	case errors.Is(err, jobs.ErrDuplicate):
		return true, c.absorbFollowUp(ctx, l, n, f, "")
	case errors.Is(err, jobs.ErrNotSubscribed):
		c.notSubscribed = true
		return true, false
	default:
		if pending, perr := c.s.cfg.Jobs.PendingFollowUp(ctx, c.slug, int64(n)); errors.Is(perr, jobs.ErrNotFound) || perr == nil && pending.TriageJobID != f.TriageJobID {
			return false, true // the follow-up moved on meanwhile: the other rules decide
		}
		c.report(ctx, failure{row: rowJobs, operation: fmt.Sprintf("make the fix job the triage asked for on the issue %d, so %s stays", n, l.Label), err: err})
		return true, false
	}
	for _, r := range recs {
		if r.Seen == SeenHolding {
			c.s.see(ctx, r, SeenEnded)
		}
	}
	c.s.cfg.Logger.Info("follow-up made: the fix label a triage asked for became the fix job linked to it",
		"workspace", c.slug, "kind", l.Kind, "issue", n, "label", l.Label, "job", doc.ID, "triage", f.TriageJobID)
	return true, c.consume(ctx, l, n, doc.ID)
}

// absorbFollowUp absorbs the label and the follow-up into the open fix job: the service's own label answers no job.
func (c *cycleRun) absorbFollowUp(ctx context.Context, l *listing, n int, f jobs.FollowUp, job string) bool {
	if err := c.s.cfg.Jobs.AbsorbFollowUp(ctx, f.TriageJobID, job); err != nil {
		c.s.cfg.Logger.Warn("the follow-up of a triage could not be marked absorbed", "workspace", c.slug, "issue", n, "triage", f.TriageJobID, "error", err.Error())
	}
	return c.absorb(ctx, l, n, job)
}
