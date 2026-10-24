package poll

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/labels"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

// listLimit is how many issues a cycle lists for a label.
const listLimit = 100

// requestSuffix is the suffix of the request label of each kind of job, from the label table.
var requestSuffix = map[jobs.Kind]string{jobs.KindTriage: labels.Triage, jobs.KindFix: labels.Fix}

// listing is what a cycle found under one request label.
type listing struct {
	Kind  jobs.Kind
	Label string
	// Issues are the numbers of the open issues that carry the label, in ascending order.
	Issues []int
	// Complete says that the listing holds every issue that carries the label: a listing that is not may leave out
	// issues past its last page, and proves nothing about them.
	Complete bool
	// Requested is when the listing was asked for; zero for the issue of an event of hub's stream, which is no listing.
	// Before is the last record of the poll when it was asked for: a record after it, or made after then, is newer than
	// what the listing shows.
	Requested time.Time
	Before    int64
	// Source is the source of the job a new request makes: the label's when it is empty.
	Source jobs.Source
	// Thread is the issue as the event of hub's stream read it, with its comments, which an answer or a retry takes
	// rather than reading the issue again; nil for a listing, whose rules read the issue when they need its comments.
	Thread *issuex.IssueThread
}

// cycleRun is a cycle that is running: the workspace it is for, and what it needs to act on it.
type cycleRun struct {
	s     *Supervisor
	slug  string
	repo  issuex.Repo
	forge issuex.Client
	st    *wsState
	// onFail, when it is set, is what a failure of the rules is reported to in place of the cycle's records; failed says
	// that one was, and notSubscribed that the jobs layer refused a job because the workspace is not subscribed.
	onFail        func(failure)
	failed        bool
	notSubscribed bool
}

// Cycle makes one cycle of the workspace, and says how long to wait before the next and whether the loop of the workspace
// is to stop.
//
// A cycle first brings the records of the poll for the workspace up to date with their jobs, which asks nothing of hub.
// It then reads the label prefix and the token of the subscription and the resolved variables of the workspace. If
// AF_POLL_INTERVAL turns the poll off, it ends there. Otherwise it reads the workspace record, for the forge repository,
// and lists the open issues of the triage label and then of the fix label, and hands each listing to the rules.
//
// A call that fails ends the cycle, and is logged as failure.go says. The next cycle is due after the interval the cycle
// read, or, if it ended before it could read it, after the interval the workspace last read.
func (s *Supervisor) Cycle(ctx context.Context, slug string) (next time.Duration, stop bool) {
	st := s.stateOf(slug)
	st.failures.begin()
	defer func() {
		if !stop && ctx.Err() == nil {
			s.completed(slug, st)
		}
	}()
	s.reconciler.reconcile(ctx, slug)

	sub, err := s.cfg.Subscriptions.Get(ctx, slug)
	if err != nil {
		return s.storeEnded(ctx, slug, st, "read the subscription", err)
	}
	pat, err := s.cfg.Subscriptions.Token(ctx, slug)
	if err != nil {
		return s.storeEnded(ctx, slug, st, "read the token of the subscription", err)
	}
	vars, err := s.cfg.Hub.ResolvedVars(ctx, pat, slug)
	if err != nil {
		s.report(ctx, slug, st, classify("read the resolved variables", err, false))
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
		s.report(ctx, slug, st, classify("read the workspace record", err, false))
		return iv.every, false
	}
	repo, ok := rec.Repo()
	if !ok {
		s.report(ctx, slug, st, failure{row: rowUnavailable, operation: "read the forge repository of the workspace", err: errors.New("the forge URL of the workspace is none of a repository")})
		return iv.every, false
	}
	run := &cycleRun{s: s, slug: slug, repo: repo, forge: s.cfg.Hub.Forge(pat, slug, repo), st: st}

	for _, kind := range []jobs.Kind{jobs.KindTriage, jobs.KindFix} {
		label := labels.Name(sub.LabelPrefix, requestSuffix[kind])
		requested := s.cfg.Clock.Now()
		before, err := s.records.Last(ctx)
		if err != nil {
			s.report(ctx, slug, st, failure{row: rowStore, operation: "read the records of the poll", err: err})
			return iv.every, false
		}
		list, err := run.forge.ListIssues(ctx, repo, issuex.IssueFilter{State: "open", Labels: []string{label}, Limit: listLimit})
		if err != nil {
			s.report(ctx, slug, st, classify("list the issues of "+label, err, false))
			return iv.every, false
		}
		numbers := make([]int, 0, len(list.Issues))
		for _, issue := range list.Issues {
			if asked(issue, label) {
				numbers = append(numbers, issue.Number)
			}
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
		l := &listing{Kind: kind, Label: label, Issues: numbers, Complete: !list.Incomplete, Requested: requested, Before: before}
		if !s.handler.handle(ctx, run, l) {
			break
		}
	}
	return iv.every, false
}

// asked says that the issue is one that the listing asked for: not closed, and carrying the label, whatever its case. Hub
// passes the filter on to the forge and the poll takes its answer for the issues of the label, but an issue that was not asked
// for is not a request, and a hub that dropped a parameter would otherwise turn every issue of the repository into a job.
func asked(issue issuex.Issue, label string) bool {
	return !strings.EqualFold(issue.State, "closed") && slices.ContainsFunc(issue.Labels, func(l string) bool { return strings.EqualFold(l, label) })
}

// storeEnded ends a cycle for which the store did not give the subscription or its token. A workspace that is not
// subscribed ends its loop; a token that does not open is an ERROR and no request of hub is made.
func (s *Supervisor) storeEnded(ctx context.Context, slug string, st *wsState, operation string, err error) (time.Duration, bool) {
	switch {
	case errors.Is(err, store.ErrNotSubscribed):
		if ctx.Err() == nil {
			s.cfg.Logger.Info("the workspace is not subscribed, so its poll stops", "workspace", slug)
		}
		return 0, true
	case errors.Is(err, store.ErrUnreadable):
		s.report(ctx, slug, st, failure{row: rowToken, operation: operation, err: err})
	default:
		s.report(ctx, slug, st, failure{row: rowStore, operation: operation, err: err})
	}
	return st.wait(), false
}
