package poll

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

// The event stream is the immediate trigger of the poll's rules: every workspace that has a poll loop keeps one connection
// to hub's event stream (GET /api/v1/events?workspace=<slug>), and the events of a label put on an issue, or of an issue
// closed, are acted on as they come. The poll's cycles stay as the catch-up for anything the stream missed, since hub's
// stream cannot replay. The stream goes with its loop: it starts and stops with it, and AF_POLL_INTERVAL=0 turns it off
// with the poll.

// The timings of the stream, none of which is a setting: how long a stream whose poll is off waits before it reads the
// variables again, the first and the longest wait between connections, and how long a connection may receive nothing.
const (
	streamOffWait = 5 * time.Minute
	firstWait     = time.Second
	longestWait   = time.Minute
	idleLimit     = time.Minute
)

// stream is the event stream of one workspace.
type stream struct {
	s    *Supervisor
	slug string
	loop *loop

	mu sync.Mutex
	// closeConn ends the open connection, nil while there is none; updatedAt is when the subscription that the
	// connection's token came from was last written, and again says that it was replaced and the stream reconnects at
	// once.
	closeConn context.CancelFunc
	updatedAt time.Time
	again     bool

	// What the open connection works with: the token, the forge repository, when the variables were read and the
	// interval they gave.
	pat      string
	repo     issuex.Repo
	read     time.Time
	interval time.Duration

	// failures are the failures of the connection's steps that have been logged since the stream last had activity.
	failures map[failureKey]bool
	// next is the wait before the next connection after a failure: 1 s, doubled after each, up to a minute, and back to
	// 1 s with the first frame or comment of a connection. notServed says that hub was found not to serve the stream,
	// which makes every wait a minute.
	next      time.Duration
	notServed bool
	// active is when the stream last had activity, or started; outage says that the outage since was logged.
	active time.Time
	outage bool
}

// startStream starts the stream of the loop of the workspace, which ends with the loop's context. The caller holds the
// lock of the Supervisor.
func (s *Supervisor) startStream(ctx context.Context, slug string, l *loop) {
	st := &stream{s: s, slug: slug, loop: l, failures: map[failureKey]bool{}, next: firstWait, active: s.cfg.Clock.Now()}
	l.stream = st
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.streams.Add(1)
		defer s.streams.Add(-1)
		st.run(ctx)
	}()
}

// run connects, and connects again after each connection ends, until the context ends.
func (st *stream) run(ctx context.Context) {
	for ctx.Err() == nil {
		wait := st.connect(ctx)
		if ctx.Err() != nil || !st.s.sleep(ctx, wait) {
			return
		}
	}
}

// connect makes one connection: it reads the token of the subscription, the variables and the workspace record, connects
// at once and reads the stream until it ends. It returns how long to wait before the next connection.
func (st *stream) connect(ctx context.Context) time.Duration {
	s := st.s
	sub, err := s.cfg.Subscriptions.Get(ctx, st.slug)
	if err == nil {
		st.pat, err = s.cfg.Subscriptions.Token(ctx, st.slug)
	}
	switch {
	case errors.Is(err, store.ErrNotSubscribed):
		// As a cycle does: the loop goes, and the stream with it; a scan that finds the workspace again starts both.
		s.dropLoop(st.slug, st.loop)
		return 0
	case errors.Is(err, store.ErrUnreadable):
		return st.fail(ctx, failure{row: rowToken, operation: "read the token of the subscription", err: err})
	case err != nil:
		return st.fail(ctx, failure{row: rowStore, operation: "read the subscription", err: err})
	}
	vars, err := s.cfg.Hub.ResolvedVars(ctx, st.pat, st.slug)
	if err != nil {
		return st.fail(ctx, classify("read the resolved variables", err, false))
	}
	iv := parseInterval(vars[intervalVar])
	st.read, st.interval = s.cfg.Clock.Now(), iv.every
	if iv.off {
		return streamOffWait
	}
	rec, err := s.cfg.Hub.WorkspaceRecord(ctx, st.pat, st.slug)
	if err != nil {
		return st.fail(ctx, classify("read the workspace record", err, false))
	}
	repo, ok := rec.Repo()
	if !ok {
		return st.fail(ctx, failure{row: rowUnavailable, operation: "read the forge repository of the workspace", err: errors.New("the forge URL of the workspace is none of a repository")})
	}
	st.repo = repo

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	st.mu.Lock()
	st.closeConn, st.updatedAt, st.again = cancel, sub.UpdatedAt, false
	st.mu.Unlock()
	defer func() {
		st.mu.Lock()
		st.closeConn = nil
		st.mu.Unlock()
	}()
	es, err := s.cfg.Hub.Events(cctx, st.pat, st.slug)
	if rejected, ok := errors.AsType[*hub.RejectedError](err); errors.Is(err, hub.ErrNotFound) || ok && rejected.Status == http.StatusMethodNotAllowed {
		// The record was read with the token, so the token sees the workspace: hub lacks the route, and no other token
		// cures that. The poll finds the labels alone, and the stream looks again every minute.
		if !st.notServed {
			st.notServed = true
			s.cfg.Logger.Warn("hub does not serve the event stream of the workspace, so labels are found by the poll alone; the stream tries again every minute",
				"workspace", st.slug, "error", err.Error())
		}
		return st.wait(ctx)
	}
	if err != nil {
		return st.fail(ctx, classify("open the event stream", err, false))
	}
	if st.consume(cctx, es) {
		st.mu.Lock()
		again := st.again
		st.mu.Unlock()
		if again {
			return 0
		}
		return firstWait // a connection that had activity: the first wait, which it set the backoff back to
	}
	return st.wait(ctx)
}

// item is what the reader of a connection read.
type item struct {
	act hub.Activity
	err error
}

// consume reads the connection until it ends, its context does, or it receives nothing for a minute: a reader goroutine
// reads the stream, and this one acts on what it reads, one event at a time, while the minute does not run. It says
// whether the connection had activity.
func (st *stream) consume(ctx context.Context, es *hub.EventStream) (had bool) {
	items := make(chan item)
	read := make(chan struct{})
	rctx, stop := context.WithCancel(ctx)
	go func() {
		defer close(read)
		for {
			a, err := es.Next()
			select {
			case items <- item{a, err}:
			case <-rctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() {
		stop() // the reader stops offering what it reads, and the close ends its read
		_ = es.Close()
		<-read
	}()
	idle := st.s.cfg.Clock.After(idleLimit)
	for {
		select {
		case it := <-items:
			if it.err != nil {
				return had
			}
			if !had {
				had = true
				st.recovered(ctx)
			}
			st.active = st.s.cfg.Clock.Now()
			if it.act.Event != nil {
				st.handle(ctx, *it.act.Event)
			}
			idle = st.s.cfg.Clock.After(idleLimit) // from the end of the handling: its time does not count
		case <-idle:
			return had
		case <-ctx.Done():
			return had
		}
	}
}

// handle acts on an event of the connection.
func (st *stream) handle(ctx context.Context, ev hub.StreamEvent) {
	if st.s.beforeEvent != nil {
		st.s.beforeEvent()
	}
}

// recovered ends an episode of failures with the first activity of a connection: the backoff is back to its first wait,
// hub serves the stream after all, and what was logged is logged again if it comes back, with one INFO if anything was.
func (st *stream) recovered(ctx context.Context) {
	st.next, st.notServed = firstWait, false
	if len(st.failures) > 0 || st.outage {
		st.s.cfg.Logger.Info("the event stream of the workspace has recovered", "workspace", st.slug)
	}
	st.failures, st.outage = map[failureKey]bool{}, false
}

// wait is the wait before the next connection after one that failed or ended with no activity: the backoff, which
// doubles, or a minute while hub does not serve the stream. It logs the outage once it has lasted longer than the poll
// interval, unless hub does not serve the stream, which was said already.
func (st *stream) wait(ctx context.Context) time.Duration {
	limit := st.interval
	if limit <= 0 {
		limit = defaultInterval
	}
	if !st.outage && !st.notServed && st.s.cfg.Clock.Now().Sub(st.active) > limit {
		st.outage = true
		st.s.cfg.Logger.Warn("the event stream of the workspace has been down for longer than the poll interval, so labels are being found by the poll alone",
			"workspace", st.slug, "interval", limit.String())
	}
	if st.notServed {
		return longestWait
	}
	w := st.next
	st.next = min(2*st.next, longestWait)
	return w
}

// replaced closes the open connection when the subscription was written since the connection read it, so that the stream
// connects again at once with the token as it is now.
func (st *stream) replaced(updatedAt time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closeConn != nil && !st.updatedAt.IsZero() && !updatedAt.Equal(st.updatedAt) {
		st.again = true
		st.closeConn()
	}
}

// fail logs a failure of a step of a connection, unless the same was logged since the stream last had activity, and
// returns the wait before the next connection.
func (st *stream) fail(ctx context.Context, f failure) time.Duration {
	if ctx.Err() != nil {
		return 0
	}
	key := failureKey{f.row, f.operation}
	if !st.failures[key] {
		st.failures[key] = true
		attrs := []any{"workspace", st.slug, "operation", f.operation}
		if f.status != 0 {
			attrs = append(attrs, "status", f.status)
		}
		if f.err != nil {
			attrs = append(attrs, "error", f.err.Error())
		}
		st.s.cfg.Logger.Log(ctx, f.level(), "the event stream: "+st.text(f), attrs...)
	}
	return st.wait(ctx)
}

// text is the message of the record of a failure of the stream: the poll's, but for a forbidden event stream, which the
// scope audit:read cures.
func (st *stream) text(f failure) string {
	if f.row == rowDenied && f.status == http.StatusForbidden && f.operation == "open the event stream" {
		return fmt.Sprintf("hub forbids the token of the workspace to open the event stream: it lacks the scope audit:read; replace the token with PUT /api/v1/workspaces/%s", st.slug)
	}
	return f.text(st.slug)
}

// dropLoop takes away the loop of a workspace that the stream found not to be subscribed, and the stream with it.
func (s *Supervisor) dropLoop(slug string, l *loop) {
	s.forget(slug, l)
	l.cancel()
}

// eventRetention is how long the ids of the events that were acted on are kept.
const eventRetention = 7 * 24 * time.Hour

// expireEvents deletes the ids of the events that were acted on more than 7 days ago.
func (s *Supervisor) expireEvents(ctx context.Context) {
	if s.records == nil {
		return
	}
	if _, err := s.records.ExpireEvents(ctx, s.cfg.Clock.Now().Add(-eventRetention)); err != nil && ctx.Err() == nil {
		s.cfg.Logger.Warn("the ids of old events of hub's stream could not be deleted; the next scan tries again", "error", err.Error())
	}
}
