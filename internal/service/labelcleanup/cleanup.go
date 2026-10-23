// Package labelcleanup is the hub-backed cancel cleanup of the jobs layer: one try reads the subscription's token, the
// workspace record and the issue's labels through hub, removes the status labels the cancelled job accounts for, and puts
// back the request label it consumed. It writes only what is missing, so a try can be repeated after a partial one or
// after a runner that wrote after all. The jobs layer decides when a try runs and records what it came to; this package
// knows hub and the label table, which the layer does not.
package labelcleanup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/labels"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

// Subscriptions is where a try reads the subscription of the workspace and its token.
type Subscriptions interface {
	Get(ctx context.Context, slug string) (store.Subscription, error)
	Token(ctx context.Context, slug string) (string, error)
}

// Cleaner is a jobs.Cleaner that cleans up through hub.
type Cleaner struct {
	Subscriptions Subscriptions
	Hub           *hub.Client
}

var _ jobs.Cleaner = (*Cleaner)(nil)

// Clean makes one try of the cleanup of the request's job.
func (c *Cleaner) Clean(ctx context.Context, req jobs.CleanupRequest) jobs.CleanupResult {
	sub, err := c.Subscriptions.Get(ctx, req.Workspace)
	if err != nil {
		return pending("read the subscription", err)
	}
	token, err := c.Subscriptions.Token(ctx, req.Workspace)
	if err != nil {
		return pending("read the token of the subscription", err)
	}
	rec, err := c.Hub.WorkspaceRecord(ctx, token, req.Workspace)
	if err != nil {
		return pending("read the workspace record", err)
	}
	repo, ok := rec.Repo()
	if !ok {
		return pending("read the forge repository of the workspace", errors.New("the forge URL names no repository"))
	}
	forge := c.Hub.Forge(token, req.Workspace, repo)
	ref := issuex.IssueRef{Repo: repo, Number: int(req.Issue)}
	thread, err := forge.ReadIssue(ctx, ref)
	if err != nil {
		return pending("read the issue", err)
	}

	var res jobs.CleanupResult
	for _, label := range removals(sub.LabelPrefix, thread.Issue.Labels, req) {
		if err := forge.RemoveLabel(ctx, ref, label); err != nil && !issuex.IsNotFound(err) {
			return pending("remove the label "+label, err)
		}
		res.Removed = append(res.Removed, label)
	}
	res.RequestLabelOn = slices.Contains(thread.Issue.Labels, req.RequestLabel)
	if req.RequestLabel != "" && !res.RequestLabelOn {
		if err := forge.AddLabels(ctx, ref, []string{req.RequestLabel}); err != nil {
			return pending("add the request label "+req.RequestLabel, err)
		}
		res.Set, res.RequestLabelOn = []string{req.RequestLabel}, true
	}
	res.Outcome = jobs.CleanupDone
	return res
}

// removals are the status labels of the issue that the cancelled job accounts for: in-progress, unless another job of
// the issue runs, and each other status label that the job's own attempts set and no other open job of the issue shows
// set. Every other label stays: a ready or landed of an earlier job, and a label a person set.
func removals(prefix string, onIssue []string, req jobs.CleanupRequest) []string {
	inProgress := labels.Name(prefix, labels.InProgress)
	var out []string
	for _, label := range onIssue {
		switch {
		case label == inProgress:
			if !req.OtherRunning {
				out = append(out, label)
			}
		case isStatus(prefix, label) && slices.Contains(req.LabelsSet, label) && !slices.Contains(req.OthersSet, label):
			out = append(out, label)
		}
	}
	return out
}

// isStatus says that the label is one of the status labels under the prefix.
func isStatus(prefix, label string) bool {
	for _, s := range labels.Statuses() {
		if label == labels.Name(prefix, s) {
			return true
		}
	}
	return false
}

// pending is a try that failed in a way that may pass: the cleanup stays pending.
func pending(op string, err error) jobs.CleanupResult {
	return jobs.CleanupResult{Outcome: jobs.CleanupPending, Level: slog.LevelWarn, Message: fmt.Sprintf("%s: %v", op, err)}
}
