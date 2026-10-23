package poll

import (
	"context"
	"errors"
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

// streamOffWait is how long a stream whose poll is off waits before it reads the variables again.
const streamOffWait = 5 * time.Minute

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
}

// startStream starts the stream of the loop of the workspace, which ends with the loop's context. The caller holds the
// lock of the Supervisor.
func (s *Supervisor) startStream(ctx context.Context, slug string, l *loop) {
	st := &stream{s: s, slug: slug, loop: l, failures: map[failureKey]bool{}}
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
	if err != nil {
		return st.fail(ctx, classify("open the event stream", err, false))
	}
	st.consume(cctx, es)
	st.mu.Lock()
	again := st.again
	st.mu.Unlock()
	if again {
		return 0
	}
	return reconnectWait
}

// reconnectWait is the wait before the stream connects again.
const reconnectWait = time.Second

// item is what the reader of a connection read.
type item struct {
	act hub.Activity
	err error
}

// consume reads the connection until it ends or its context does: a reader goroutine reads the stream, and this one acts on
// what it reads, one event at a time.
func (st *stream) consume(ctx context.Context, es *hub.EventStream) {
	items := make(chan item)
	read := make(chan struct{})
	go func() {
		defer close(read)
		for {
			a, err := es.Next()
			select {
			case items <- item{a, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() {
		_ = es.Close()
		<-read
	}()
	for {
		select {
		case it := <-items:
			if it.err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
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
		st.s.cfg.Logger.Log(ctx, f.level(), "the event stream: "+f.text(st.slug), attrs...)
	}
	return reconnectWait
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
