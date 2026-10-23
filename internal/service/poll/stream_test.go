package poll

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

// noCycles is an option under which the loops make no cycle while a test runs: their first cycle is an hour away, so what
// hub sees is the stream's.
func noCycles() option {
	return withPoll(func(c *Config) { c.FirstDelay = func() time.Duration { return time.Hour } })
}

// conns waits until the workspace has n open connections to hub's event stream.
func (e *env) conns(workspace string, n int) {
	e.t.Helper()
	waitFor(e.t, "the event stream connections", func() bool { return e.Hub.EventConns(workspace) == n })
}

// eventsOf are the requests of the events route made with the token.
func (e *env) eventsOf(token string) []fakehub.Request {
	var out []fakehub.Request
	for _, r := range e.Hub.EventsRequests() {
		if r.TokenHash == fakehub.TokenHash(token) {
			out = append(out, r)
		}
	}
	return out
}

// TestASubscribedWorkspaceGetsOneConnectedStreamWithinAScan_TS_18_14 verifies TS-18-14.
// Verifies: 18-REQ-3.1
func TestASubscribedWorkspaceGetsOneConnectedStreamWithinAScan_TS_18_14(t *testing.T) {
	e := newEnv(t, noCycles())
	e.started()
	e.conns(slug, 1)
	e.addWorkspace("gadgets", "pat-GADGETS-0123456789")
	e.Clock.Advance(60 * time.Second)
	e.conns("gadgets", 1)
	time.Sleep(50 * time.Millisecond)
	if n := e.Hub.EventConns(slug); n != 1 {
		t.Errorf("%d connections of widgets, want exactly 1", n)
	}
}

// TestAnUnsubscribedWorkspaceLosesItsStreamAtTheNextScan_TS_18_15 verifies TS-18-15.
// Verifies: 18-REQ-3.2
func TestAnUnsubscribedWorkspaceLosesItsStreamAtTheNextScan_TS_18_15(t *testing.T) {
	e := newEnv(t, noCycles())
	e.started()
	e.conns(slug, 1)
	if _, err := e.Store.Delete(ctx, slug); err != nil {
		t.Fatal(err)
	}
	e.Clock.Advance(60 * time.Second)
	e.conns(slug, 0)
	n := len(e.Hub.EventsRequests())
	e.Clock.Advance(10 * time.Minute)
	settle()
	if got := len(e.Hub.EventsRequests()); got != n {
		t.Errorf("%d more events requests after the workspace was gone", got-n)
	}
}

// TestAConnectionReadsTheVariablesThenTheRecordThenConnectsAtOnce_TS_18_16 verifies TS-18-16.
// Verifies: 18-REQ-3.3
func TestAConnectionReadsTheVariablesThenTheRecordThenConnectsAtOnce_TS_18_16(t *testing.T) {
	e := newEnv(t, noCycles())
	e.started()
	e.conns(slug, 1) // with no advance of the clock: no delay before the connect
	if got, want := e.fromPoll(), []string{"vars", "workspace", "GET /api/v1/events"}; !slices.Equal(got, want) {
		t.Errorf("hub saw %v with the token, want %v", got, want)
	}
}

// TestAReplacedTokenMakesTheNextScanReconnectWithIt_TS_18_17 verifies TS-18-17.
// Verifies: 18-REQ-3.4
func TestAReplacedTokenMakesTheNextScanReconnectWithIt_TS_18_17(t *testing.T) {
	e := newEnv(t, noCycles())
	e.started()
	e.conns(slug, 1)
	const fresh = "pat-NEW-0123456789"
	e.Hub.AddWorkspaceToken(fresh, []string{slug})
	if _, _, err := e.Store.Upsert(ctx, slug, "af:", fresh, "usr_1", "usr_1", epoch.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	e.Clock.Advance(60 * time.Second)
	waitFor(t, "the connection with the new token", func() bool { return len(e.eventsOf(fresh)) == 1 && e.Hub.EventConns(slug) == 1 })
	if r := e.eventsOf(fresh)[0]; r.Header.Get("Authorization") != "Bearer "+fresh {
		t.Errorf("the events request carries %q", r.Header.Get("Authorization"))
	}
}

// TestAStreamThatFindsTheWorkspaceNotSubscribedTakesTheLoopWithIt_TS_18_18 verifies TS-18-18.
// Verifies: 18-REQ-3.5
func TestAStreamThatFindsTheWorkspaceNotSubscribedTakesTheLoopWithIt_TS_18_18(t *testing.T) {
	e := newEnv(t, noCycles())
	e.started()
	e.conns(slug, 1)
	if _, err := e.Store.Delete(ctx, slug); err != nil {
		t.Fatal(err)
	}
	waits := len(e.Clock.Waits())
	e.Hub.EndEvents(slug) // the stream reconnects, and the store says the workspace is not subscribed
	e.Clock.WaitForWaits(waits+1, 10*time.Second)
	e.Clock.Advance(time.Second)
	waitFor(t, "the loop to go", func() bool { return e.Poll.loopCount(slug) == 0 })
	e.conns(slug, 0)
	if _, _, err := e.Store.Upsert(ctx, slug, "af:", pat, "usr_1", "usr_1", epoch); err != nil {
		t.Fatal(err)
	}
	e.Clock.Advance(60 * time.Second)
	waitFor(t, "the loop to come back", func() bool { return e.Poll.loopCount(slug) == 1 })
	e.conns(slug, 1)
}

// counting are subscriptions that count the reads of the token.
type counting struct {
	Subscriptions
	tokens atomic.Int32
}

func (c *counting) Token(ctx context.Context, slug string) (string, error) {
	c.tokens.Add(1)
	return c.Subscriptions.Token(ctx, slug)
}

// TestAnUnreadableTokenIsOneErrorAndTheStreamKeepsTrying_TS_18_19 verifies TS-18-19.
// Verifies: 18-REQ-3.6
func TestAnUnreadableTokenIsOneErrorAndTheStreamKeepsTrying_TS_18_19(t *testing.T) {
	subs := &counting{}
	e := newEnv(t, withPoll(func(c *Config) {
		c.FirstDelay = func() time.Duration { return time.Hour } // no cycle: what is logged is the stream's
		subs.Subscriptions = c.Subscriptions
		c.Subscriptions = subs
	}))
	if _, err := e.DB.ExecContext(ctx, "UPDATE af_subscriptions SET sealed_token = x'00' WHERE slug = ?", slug); err != nil {
		t.Fatal(err)
	}
	e.started()
	waitFor(t, "the first read", func() bool { return subs.tokens.Load() >= 1 })
	for range 4 {
		before := subs.tokens.Load()
		// The loop has waited for its first cycle once, and the stream once after each of its attempts.
		e.Clock.WaitForWaits(1+int(before), 10*time.Second)
		e.Clock.Advance(time.Minute)
		waitFor(t, "another attempt", func() bool { return subs.tokens.Load() > before })
	}
	recs := e.Logs.find("ERROR", "token")
	if len(recs) != 1 {
		t.Fatalf("%d ERROR records about the token, want 1: %v", len(recs), recs)
	}
	if strings.Contains(e.Logs.String(), pat) {
		t.Error("a record holds the PAT")
	}
}

// TestStoppingThePollStopsTheStreamsAndWhatTheyAreDoing_TS_18_21 verifies TS-18-21: Stop cancels the streams, one of them
// held in a hub call, and returns once they have returned.
// Verifies: 18-REQ-3.8
func TestStoppingThePollStopsTheStreamsAndWhatTheyAreDoing_TS_18_21(t *testing.T) {
	e := newEnv(t, noCycles())
	e.addWorkspace("gadgets", "pat-GADGETS-0123456789")
	e.Hub.Delay("/api/v1/workspaces/gadgets", time.Hour) // the stream of gadgets waits in its record read
	e.Poll.Start(ctx)
	e.conns(slug, 1)
	waitFor(t, "the read of gadgets", func() bool { return len(e.Hub.RequestsTo("GET", "/api/v1/workspaces/gadgets")) == 1 })
	stopped := make(chan struct{})
	go func() { e.Poll.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return")
	}
	if e.Poll.streams.Load() != 0 {
		t.Errorf("%d streams still running", e.Poll.streams.Load())
	}
	e.conns(slug, 0)
}

// TestWithTheIntervalZeroNoConnectionIsOpenedUntilItIs5m_TS_18_22 verifies TS-18-22.
// Verifies: 18-REQ-4.1, 18-REQ-4.2
func TestWithTheIntervalZeroNoConnectionIsOpenedUntilItIs5m_TS_18_22(t *testing.T) {
	e := newEnv(t, withVars(map[string]string{"AF_POLL_INTERVAL": "0"}))
	e.issue(42, "af:fix")
	e.started()
	waitFor(t, "the first reads", func() bool { return len(e.Hub.RequestsTo("GET", "/api/v1/workspaces/widgets/vars/resolved")) >= 2 })
	e.Clock.Advance(5 * time.Minute)
	settle()
	if n := len(e.Hub.EventsRequests()); n != 0 || len(e.jobList()) != 0 {
		t.Fatalf("%d events requests and %d jobs with the poll off, want none", n, len(e.jobList()))
	}
	e.setVar("AF_POLL_INTERVAL", "5m")
	e.Clock.Advance(5 * time.Minute)
	e.conns(slug, 1)
}

// TestAnyOtherIntervalLeavesTheStreamOnAndItLogsNothingOfIt_TS_18_24 verifies TS-18-24.
// Verifies: 18-REQ-4.4
func TestAnyOtherIntervalLeavesTheStreamOnAndItLogsNothingOfIt_TS_18_24(t *testing.T) {
	for _, v := range []string{"", "bogus", "1m", "24h"} {
		t.Run(v, func(t *testing.T) {
			vars := map[string]string{}
			if v != "" {
				vars["AF_POLL_INTERVAL"] = v
			}
			e := newEnv(t, noCycles(), withVars(vars))
			e.started()
			e.conns(slug, 1)
			if strings.Contains(e.Logs.String(), "AF_POLL_INTERVAL") {
				t.Error("the stream logged something of AF_POLL_INTERVAL")
			}
		})
	}
}

// TestTheScanDeletesEventIdsOlderThan7Days_TS_18_48 verifies TS-18-48.
// Verifies: 18-REQ-7.4
func TestTheScanDeletesEventIdsOlderThan7Days_TS_18_48(t *testing.T) {
	e := newEnv(t, noCycles())
	recs := NewRecords(e.DB)
	if err := recs.TakeEvent(ctx, slug, "old", epoch.Add(-7*24*time.Hour-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := recs.TakeEvent(ctx, slug, "new", epoch.Add(-6*24*time.Hour-23*time.Hour)); err != nil {
		t.Fatal(err)
	}
	e.started()
	waitFor(t, "the scan", func() bool { taken, _ := recs.EventTaken(ctx, slug, "old"); return !taken })
	if taken, _ := recs.EventTaken(ctx, slug, "new"); !taken {
		t.Error("the id of 6 days and 23 hours was deleted")
	}
}
