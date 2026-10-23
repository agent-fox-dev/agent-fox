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
	"net/http"
	"net/url"
	"slices"
	"strings"

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

// Clean makes one try of the cleanup of the request's job. Every operation and every way it can fail has one outcome:
// a failure that may pass leaves the cleanup pending, and a refusal that a retry cannot cure ends it label_not_set.
func (c *Cleaner) Clean(ctx context.Context, req jobs.CleanupRequest) jobs.CleanupResult {
	sub, err := c.Subscriptions.Get(ctx, req.Workspace)
	if err == nil {
		var token string
		if token, err = c.Subscriptions.Token(ctx, req.Workspace); err == nil {
			return c.clean(ctx, req, sub.LabelPrefix, token)
		}
	}
	if errors.Is(err, store.ErrNotSubscribed) {
		return ended(slog.LevelInfo, "the workspace is no longer subscribed, so nothing is written to the issue")
	}
	return pending("read the token of the subscription", err)
}

// clean is a try with the subscription's prefix and token.
func (c *Cleaner) clean(ctx context.Context, req jobs.CleanupRequest, prefix, token string) jobs.CleanupResult {
	const readRecord, readIssue = "read the workspace record", "read the issue"
	rec, err := c.Hub.WorkspaceRecord(ctx, token, req.Workspace)
	if err != nil {
		if kindOf(err) == notFound {
			return ended(slog.LevelError, fmt.Sprintf("%s: hub does not find the workspace %q", readRecord, req.Workspace))
		}
		return failed(readRecord, req.Workspace, err)
	}
	repo, ok := rec.Repo()
	if !ok {
		return ended(slog.LevelError, fmt.Sprintf("%s: the forge URL of the workspace %q names no repository", readRecord, req.Workspace))
	}
	forge := c.Hub.Forge(token, req.Workspace, repo)
	ref := issuex.IssueRef{Repo: repo, Number: int(req.Issue)}
	thread, err := forge.ReadIssue(ctx, ref)
	if err != nil {
		if kindOf(err) == notFound {
			return ended(slog.LevelInfo, fmt.Sprintf("%s: the issue is gone", readIssue))
		}
		return failed(readIssue, req.Workspace, err)
	}

	var res jobs.CleanupResult
	var kept []string // the removals hub refused
	for _, label := range removals(prefix, thread.Issue.Labels, req) {
		op := "remove the label " + label
		err := forge.RemoveLabel(ctx, ref, label)
		switch kind := kindOf(err); {
		case err == nil, kind == notFound:
			res.Removed = append(res.Removed, label)
		case kind == rejected:
			kept = append(kept, fmt.Sprintf("%s: %s", op, messageOf(err)))
		default:
			return written(res, failed(op, req.Workspace, err))
		}
	}
	res.RequestLabelOn = slices.Contains(thread.Issue.Labels, req.RequestLabel)
	if req.RequestLabel != "" && !res.RequestLabelOn {
		op := "add the request label " + req.RequestLabel
		err := forge.AddLabels(ctx, ref, []string{req.RequestLabel})
		switch kind := kindOf(err); {
		case err == nil:
			res.Set, res.RequestLabelOn = []string{req.RequestLabel}, true
		case kind == notFound, kind == rejected:
			return written(res, ended(slog.LevelError, fmt.Sprintf("%s: hub refused it: %s", op, messageOf(err))))
		default:
			return written(res, failed(op, req.Workspace, err))
		}
	}
	if len(kept) > 0 {
		res.Outcome, res.Level, res.Message = jobs.CleanupLabelNotSet, slog.LevelWarn, "hub refused a removal, so the label stays: "+strings.Join(kept, "; ")
		return res
	}
	res.Outcome = jobs.CleanupDone
	return res
}

// written is the result with what the try wrote before it ended.
func written(res, end jobs.CleanupResult) jobs.CleanupResult {
	end.Set, end.Removed, end.RequestLabelOn = res.Set, res.Removed, res.RequestLabelOn
	return end
}

// kind is how hub answered a call that failed, whether the hub client or its forge client made it.
type kind int

const (
	unavailable  kind = iota // 429, a 5xx other than a forge refusal, a transport failure, a timeout, a body not hub's
	refused                  // 401: hub refuses the token
	forbidden                // 403: hub forbids the call to the token
	forgeRefused             // 502 forge_forbidden or forge_unauthenticated: the forge refuses hub's credential
	notFound                 // 404
	rejected                 // another 4xx
)

// kindOf tells how a call failed. The forge client turns the hub client's errors into issuex's, so both are read.
func kindOf(err error) kind {
	if httpErr, ok := errors.AsType[*issuex.HTTPError](err); ok {
		switch s := httpErr.Status; {
		case s == http.StatusUnauthorized:
			return refused
		case s == http.StatusForbidden:
			return forbidden
		case s == http.StatusBadGateway:
			return forgeRefused
		case s == http.StatusNotFound:
			return notFound
		case s >= 400 && s < 500 && s != http.StatusTooManyRequests:
			return rejected
		}
		return unavailable
	}
	rej, isRejected := errors.AsType[*hub.RejectedError](err)
	switch {
	case errors.Is(err, hub.ErrRefused):
		return refused
	case errors.Is(err, hub.ErrForbidden):
		return forbidden
	case isRejected && rej.Status == http.StatusBadGateway:
		return forgeRefused
	case errors.Is(err, hub.ErrNotFound), issuex.IsNotFound(err):
		return notFound
	case isRejected, errors.Is(err, issuex.ErrConflict):
		return rejected
	}
	return unavailable
}

// messageOf is hub's message of a refusal: the forge client's or the hub client's, else the error.
func messageOf(err error) string {
	if httpErr, ok := errors.AsType[*issuex.HTTPError](err); ok && httpErr.Message != "" {
		return httpErr.Message
	}
	if rej, ok := errors.AsType[*hub.RejectedError](err); ok {
		return rej.Message
	}
	return err.Error()
}

// failed is the outcome of an operation that hub did not do: a refusal of the token or of the forge's credential, and any
// other refusal, end the cleanup with an ERROR that names the operation and carries hub's message, and point to where the
// token is replaced when the token is what was refused; anything else may pass, and leaves it pending.
func failed(op, slug string, err error) jobs.CleanupResult {
	switch kindOf(err) {
	case refused, forbidden:
		return ended(slog.LevelError, fmt.Sprintf("%s: hub refused the token of the subscription: %s; replace the token with PUT /api/v1/workspaces/%s",
			op, messageOf(err), url.PathEscape(slug)))
	case forgeRefused:
		return ended(slog.LevelError, fmt.Sprintf("%s: the forge refused hub's credential for the repository: %s", op, messageOf(err)))
	case rejected, notFound:
		return ended(slog.LevelError, fmt.Sprintf("%s: hub refused it: %s", op, messageOf(err)))
	}
	return pending(op, err)
}

// ended is a cleanup that ends without the labels, with what is logged.
func ended(level slog.Level, message string) jobs.CleanupResult {
	return jobs.CleanupResult{Outcome: jobs.CleanupLabelNotSet, Level: level, Message: message}
}

// removals are the status labels of the issue that the cancelled job accounts for: in-progress under the subscription's
// prefix, unless another job of the issue runs, and each other status label that the job's own attempts set (whatever
// prefix it was written under) and no other open job of the issue shows set. Every other label stays: a ready or landed
// of an earlier job, and a label a person set.
func removals(prefix string, onIssue []string, req jobs.CleanupRequest) []string {
	inProgress := labels.Name(prefix, labels.InProgress)
	var out []string
	for _, label := range onIssue {
		switch {
		case label == inProgress:
			if !req.OtherRunning {
				out = append(out, label)
			}
		case isStatus(label) && slices.Contains(req.LabelsSet, label) && !slices.Contains(req.OthersSet, label):
			out = append(out, label)
		}
	}
	return out
}

// isStatus says that the label is a status label: its name ends in a status suffix. The job's labels.set holds the full
// names its attempts wrote, under the prefix the subscription had then, which a replace may have changed since.
func isStatus(label string) bool {
	for _, s := range labels.Statuses() {
		if strings.HasSuffix(label, s) {
			return true
		}
	}
	return false
}

// pending is a try that failed in a way that may pass: the cleanup stays pending.
func pending(op string, err error) jobs.CleanupResult {
	return jobs.CleanupResult{Outcome: jobs.CleanupPending, Level: slog.LevelWarn, Message: fmt.Sprintf("%s: %v", op, err)}
}
