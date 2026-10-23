package poll

import (
	"context"
	"slices"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

// listLimit is how many issues a cycle lists for a label.
const listLimit = 100

// listing is what a cycle found under one request label.
type listing struct {
	Workspace string
	Kind      jobs.Kind
	Label     string
	// Issues are the numbers of the open issues that carry the label, in ascending order.
	Issues []int
	// Complete says that the listing holds every issue that carries the label: a listing that is not may leave out
	// issues past its last page, and proves nothing about them.
	Complete bool
	// Requested is when the listing was asked for.
	Requested time.Time
}

// cycleRun is a cycle that is running: the workspace it is for, and what it needs to act on it.
type cycleRun struct {
	s     *Supervisor
	slug  string
	pat   string
	repo  issuex.Repo
	forge issuex.Client
	st    *wsState
}

// noReconciler and noHandler are what a cycle does with the records and with the issues it lists until the rules of the
// poll are in.
type noReconciler struct{}

func (noReconciler) reconcile(context.Context, string) {}

type noHandler struct{}

func (noHandler) handle(context.Context, *cycleRun, *listing) bool { return true }

// Cycle makes one cycle of the workspace, and says how long to wait before the next and whether the loop of the workspace
// is to stop.
//
// A cycle first brings the records of the poll for the workspace up to date with their jobs, which asks nothing of hub.
// It then reads the label prefix and the token of the subscription and the resolved variables of the workspace. If
// AF_POLL_INTERVAL turns the poll off, it ends there. Otherwise it reads the workspace record, for the forge repository,
// and lists the open issues of the triage label and then of the fix label, and hands each listing to the rules.
func (s *Supervisor) Cycle(ctx context.Context, slug string) (next time.Duration, stop bool) {
	st := s.stateOf(slug)
	s.reconciler.reconcile(ctx, slug)

	sub, err := s.cfg.Subscriptions.Get(ctx, slug)
	if err != nil {
		s.endedBy(ctx, slug, "read the subscription", err)
		return st.wait(), false
	}
	pat, err := s.cfg.Subscriptions.Token(ctx, slug)
	if err != nil {
		s.endedBy(ctx, slug, "read the token", err)
		return st.wait(), false
	}
	vars, err := s.cfg.Hub.ResolvedVars(ctx, pat, slug)
	if err != nil {
		s.endedBy(ctx, slug, "read the resolved variables", err)
		return st.wait(), false
	}

	iv := parseInterval(vars[intervalVar])
	st.last = iv.every
	if iv.off {
		st.last = 0
		return iv.every, false
	}
	if value := vars[intervalVar]; iv.invalid && !st.warnedInterval[value] {
		st.warnedInterval[value] = true
		s.cfg.Logger.Warn("AF_POLL_INTERVAL is not a duration from 1m to 24h, so the poll uses 5m",
			"workspace", slug, "variable", intervalVar, "value", value)
	}
	if value := vars[prefixVar]; value != "" && value != sub.LabelPrefix && !st.warnedPrefix[value] {
		st.warnedPrefix[value] = true
		s.cfg.Logger.Warn("AF_LABEL_PREFIX differs from the label prefix of the subscription, which names the request labels, so the variable is ignored",
			"workspace", slug, "variable", prefixVar, "value", value, "label_prefix", sub.LabelPrefix)
	}

	rec, err := s.cfg.Hub.WorkspaceRecord(ctx, pat, slug)
	if err != nil {
		s.endedBy(ctx, slug, "read the workspace record", err)
		return iv.every, false
	}
	repo, ok := rec.Repo()
	if !ok {
		s.cfg.Logger.Warn("the forge URL of the workspace is none of a repository, so nothing is listed", "workspace", slug)
		return iv.every, false
	}
	run := &cycleRun{s: s, slug: slug, pat: pat, repo: repo, forge: s.cfg.Hub.Forge(pat, slug, repo), st: st}

	for _, kind := range []jobs.Kind{jobs.KindTriage, jobs.KindFix} {
		label := sub.LabelPrefix + string(kind)
		requested := s.cfg.Clock.Now()
		list, err := run.forge.ListIssues(ctx, repo, issuex.IssueFilter{State: "open", Labels: []string{label}, Limit: listLimit})
		if err != nil {
			s.endedBy(ctx, slug, "list the issues of "+label, err)
			return iv.every, false
		}
		numbers := make([]int, 0, len(list.Issues))
		for _, issue := range list.Issues {
			numbers = append(numbers, issue.Number)
		}
		slices.Sort(numbers)
		switch {
		case list.Incomplete && !st.incomplete[label]:
			st.incomplete[label] = true
			s.cfg.Logger.Warn("the listing of a request label is incomplete: more issues carry it than were listed, so no hold is released and no owed removal settled on it, and the issues past the last page wait for a later cycle",
				"workspace", slug, "label", label)
		case !list.Incomplete:
			delete(st.incomplete, label)
		}
		l := &listing{Workspace: slug, Kind: kind, Label: label, Issues: numbers, Complete: !list.Incomplete, Requested: requested}
		if !s.handler.handle(ctx, run, l) {
			break
		}
	}
	return iv.every, false
}

// endedBy logs that a call of hub or of the store ended the cycle, unless the cycle was cancelled, which says why.
func (s *Supervisor) endedBy(ctx context.Context, slug, operation string, err error) {
	if ctx.Err() != nil {
		return
	}
	s.cfg.Logger.Warn("the cycle ended: a call failed", "workspace", slug, "operation", operation, "error", err.Error())
}
