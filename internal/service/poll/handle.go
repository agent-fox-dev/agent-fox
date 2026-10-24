package poll

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/labels"
)

// The handling of the events of a workspace's stream. A label put on an issue is a hint: the issue as hub reads it now
// decides, and then the poll's own rules, under the workspace's lock, so that the stream and the cycles never make two
// jobs for one request, take back a label the worker put back, or make a job for a label whose removal is owed.

const (
	// maxEventID is the longest id of an event that is taken.
	maxEventID = 256
	// staleReading is how long the stream relies on its reading of the variables before it acts on an event.
	staleReading = 5 * time.Minute
)

// eventKind is what an event of the stream is to the service.
type eventKind int

const (
	ignoredEvent eventKind = iota
	requestEvent
	closedEvent
)

// kindOf says what the event is, the request label it names for a request, and whether it is malformed: a labeled event
// with no label or a request label, or a closed event, that has no id, no issue, or an id too long or with a control
// character in it.
func (st *stream) kindOf(ev hub.StreamEvent, prefix string) (kind eventKind, jobKind jobs.Kind, malformed bool) {
	if ev.Workspace != "" && ev.Workspace != st.slug {
		return ignoredEvent, "", false
	}
	switch ev.Type {
	case hub.EventIssueLabeled:
		for k, suffix := range requestSuffix {
			if strings.EqualFold(ev.Label, labels.Name(prefix, suffix)) {
				kind, jobKind = requestEvent, k
			}
		}
		if kind != requestEvent && ev.Label != "" {
			return ignoredEvent, "", false
		}
	case hub.EventIssueClosed:
		kind = closedEvent
	default:
		return ignoredEvent, "", false
	}
	malformed = ev.ID == "" || ev.Issue <= 0 || len(ev.ID) > maxEventID || strings.ContainsFunc(ev.ID, unicode.IsControl)
	if malformed || ev.Label == "" && kind != closedEvent {
		return ignoredEvent, "", malformed
	}
	return kind, jobKind, false
}

// handle acts on an event of the connection.
func (st *stream) handle(ctx context.Context, ev hub.StreamEvent) {
	if st.s.beforeEvent != nil {
		st.s.beforeEvent()
	}
	sub, err := st.s.cfg.Subscriptions.Get(ctx, st.slug)
	if err != nil {
		st.handlingFailed(ctx, failure{row: rowStore, operation: "read the subscription", err: err})
		return
	}
	kind, jobKind, malformed := st.kindOf(ev, sub.LabelPrefix)
	switch {
	case malformed:
		if !st.warnedMalformed {
			st.warnedMalformed = true
			st.s.cfg.Logger.Warn("the event stream: a malformed event of the forge is ignored; it has no id or no issue, or an id that cannot be one",
				"workspace", st.slug, "type", ev.Type, "issue", ev.Issue)
		}
		return
	case kind == requestEvent:
		st.request(ctx, ev, jobKind, labels.Name(sub.LabelPrefix, requestSuffix[jobKind]))
	case kind == closedEvent:
		st.closedIssue(ctx, ev)
	default:
		st.s.cfg.Logger.Debug("the event stream: an event is ignored", "workspace", st.slug, "type", ev.Type)
	}
}

// request acts on a label of a request put on an issue: under the workspace's lock, it skips an event already taken,
// makes sure the poll is on, reads the issue, and leaves an issue that is closed, a pull request or without the label
// alone; otherwise it applies the poll's rules to the issue and the label, with the event as the source of a new job.
func (st *stream) request(ctx context.Context, ev hub.StreamEvent, kind jobs.Kind, label string) {
	unlock := st.s.lockWorkspace(st.slug)
	defer unlock()
	if taken, err := st.s.records.EventTaken(ctx, st.slug, ev.ID); err != nil {
		st.handlingFailed(ctx, failure{row: rowStore, operation: "read the events that were taken", err: err})
		return
	} else if taken {
		return
	}
	if !st.pollOn(ctx) {
		return
	}
	forge := st.s.cfg.Hub.Forge(st.pat, st.slug, st.repo)
	thread, err := forge.ReadIssue(ctx, issuex.IssueRef{Repo: st.repo, Number: ev.Issue})
	if err != nil {
		// One operation for every issue: the failure is logged once until an event is handled, whichever issue it names.
		st.handlingFailed(ctx, classify("read the issue of an event", err, false))
		return
	}
	issue := thread.Issue
	if strings.EqualFold(issue.State, "closed") || issue.IsPR || !carries(issue, label) {
		st.decided(ctx, ev)
		return
	}
	c := &cycleRun{s: st.s, slug: st.slug, repo: st.repo, forge: forge, st: st.s.stateOf(st.slug)}
	c.onFail = func(f failure) { st.handlingFailed(ctx, f) }
	c.issue(ctx, &listing{Kind: kind, Label: label, Source: jobs.Source{Kind: "event", By: ev.ID}}, ev.Issue)
	if !c.failed && !c.notSubscribed {
		st.decided(ctx, ev)
	}
}

// closedIssue acts on an issue that was closed.
func (st *stream) closedIssue(ctx context.Context, ev hub.StreamEvent) {}

// carries says that the issue carries the label, whatever its case.
func carries(issue issuex.Issue, label string) bool {
	for _, l := range issue.Labels {
		if strings.EqualFold(l, label) {
			return true
		}
	}
	return false
}

// pollOn makes sure the poll of the workspace is on before an event is acted on: a reading of the variables older than 5
// minutes is renewed, and one that turns the poll off closes the connection, which reads them again 5 minutes later. It
// says whether the event may be acted on.
func (st *stream) pollOn(ctx context.Context) bool {
	if st.s.cfg.Clock.Now().Sub(st.read) <= staleReading {
		return true
	}
	vars, err := st.s.cfg.Hub.ResolvedVars(ctx, st.pat, st.slug)
	if err != nil {
		st.handlingFailed(ctx, classify("read the resolved variables", err, false))
		return false
	}
	iv := parseInterval(vars[intervalVar])
	st.read, st.interval = st.s.cfg.Clock.Now(), iv.every
	if iv.off {
		st.mu.Lock()
		st.off = true
		if st.closeConn != nil {
			st.closeConn()
		}
		st.mu.Unlock()
		return false
	}
	return true
}

// decided records the event as taken, and ends the episode of the failures of the handling of events.
func (st *stream) decided(ctx context.Context, ev hub.StreamEvent) {
	if err := st.s.records.TakeEvent(ctx, st.slug, ev.ID, st.s.cfg.Clock.Now()); err != nil {
		st.handlingFailed(ctx, failure{row: rowStore, operation: "record the event that was taken", err: err})
		return
	}
	st.handling = map[failureKey]bool{}
}

// handlingFailed logs a failure of the handling of an event, unless the same was logged since an event of the workspace
// was last handled without failure. These records are the handling's own: they neither silence nor are silenced by the
// connection's or the cycles'.
func (st *stream) handlingFailed(ctx context.Context, f failure) {
	if ctx.Err() != nil {
		return
	}
	key := failureKey{f.row, f.operation}
	if st.handling[key] {
		return
	}
	st.handling[key] = true
	attrs := []any{"workspace", st.slug, "operation", f.operation}
	if f.status != 0 {
		attrs = append(attrs, "status", f.status)
	}
	if f.err != nil {
		attrs = append(attrs, "error", f.err.Error())
	}
	st.s.cfg.Logger.Log(ctx, f.level(), "the event stream: an event is left to the poll: "+st.handlingText(f), attrs...)
}

// handlingText is the message of a failure of the handling of an event.
func (st *stream) handlingText(f failure) string {
	if f.row == rowUnavailable {
		return fmt.Sprintf("hub could not be asked to %s", f.operation)
	}
	return st.text(f)
}
