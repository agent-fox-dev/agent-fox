package poll

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
)

// noCycleDelay is the wait of the first cycle under noCycles: long, and unlike any wait of the stream.
const noCycleDelay = 1000 * time.Hour

// noCycles is an option under which the loops make no cycle while a test runs: their first cycle is 1000 hours away, so
// what hub sees is the stream's.
func noCycles() option {
	return withPoll(func(c *Config) { c.FirstDelay = func() time.Duration { return noCycleDelay } })
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

// minuteWithHeartbeats advances the clock a minute, the scan period, with a heartbeat of hub halfway, as hub sends one every
// 30 s, so that an open connection does not reach its idle limit.
func (e *env) minuteWithHeartbeats() {
	e.t.Helper()
	e.Clock.Advance(30 * time.Second)
	e.Hub.Heartbeat(slug)
	settle()
	e.Clock.Advance(30 * time.Second)
}

// TestASubscribedWorkspaceGetsOneConnectedStreamWithinAScan_TS_18_14 verifies TS-18-14.
// Verifies: 18-REQ-3.1
func TestASubscribedWorkspaceGetsOneConnectedStreamWithinAScan_TS_18_14(t *testing.T) {
	e := newEnv(t, noCycles())
	e.started()
	e.conns(slug, 1)
	e.addWorkspace("gadgets", "pat-GADGETS-0123456789")
	e.minuteWithHeartbeats() // the scan
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
	e := newEnv(t, noCycles(), withRunner(fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))))
	e.started()
	e.conns(slug, 1) // with no advance of the clock: no delay before the connect
	if got, want := e.fromPoll(), []string{"vars", "workspace", "GET /api/v1/events"}; !slices.Equal(got, want) {
		t.Errorf("hub saw %v with the token, want %v", got, want)
	}
	// A new label prefix of the subscription is the next event's, with no new connection.
	if _, _, err := e.Store.Upsert(ctx, slug, "bot:", pat, "usr_1", "usr_1", epoch); err != nil {
		t.Fatal(err)
	}
	e.issue(42, "bot:fix")
	e.labeled("prefixed", 42, "bot:fix")
	waitFor(t, "the job of bot:fix", func() bool { return e.jobsFor(42) == 1 })
	if n := len(e.Hub.EventsRequests()); n != 1 {
		t.Errorf("%d events requests, want the one connection", n)
	}
}

// TestAReplacedTokenMakesTheNextScanReconnectWithIt_TS_18_17 verifies TS-18-17.
// Verifies: 18-REQ-3.4
func TestAReplacedTokenMakesTheNextScanReconnectWithIt_TS_18_17(t *testing.T) {
	e := newEnv(t, noCycles(), withRunner(fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))))
	e.started()
	e.conns(slug, 1)
	const fresh = "pat-NEW-0123456789"
	e.Hub.AddWorkspaceToken(fresh, []string{slug})
	if _, _, err := e.Store.Upsert(ctx, slug, "af:", fresh, "usr_1", "usr_1", epoch.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	e.minuteWithHeartbeats() // the scan
	waitFor(t, "the connection with the new token", func() bool { return len(e.eventsOf(fresh)) == 1 && e.Hub.EventConns(slug) == 1 })
	if r := e.eventsOf(fresh)[0]; r.Header.Get("Authorization") != "Bearer "+fresh {
		t.Errorf("the events request carries %q", r.Header.Get("Authorization"))
	}

	// A token replaced while an event is acted on closes the connection, and leaves the event's rules to finish.
	e.issue(42, "af:fix")
	e.Hub.Delay("/api/v1/workspaces/widgets/issues/42", 500*time.Millisecond)
	e.labeled("during", 42, "af:fix")
	waitFor(t, "the read of 42", func() bool { return len(e.Hub.RequestsTo("GET", "/api/v1/workspaces/widgets/issues/42")) == 1 })
	const third = "pat-THIRD-0123456789"
	e.Hub.AddWorkspaceToken(third, []string{slug})
	if _, _, err := e.Store.Upsert(ctx, slug, "af:", third, "usr_1", "usr_1", epoch.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	e.minuteWithHeartbeats() // the scan
	waitFor(t, "the event's job and the connection with the third token", func() bool {
		return e.jobsFor(42) == 1 && e.taken("during") && len(e.eventsOf(third)) == 1
	})
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

	// The same when the store says so as an event is handled, on a connection that is open.
	if _, err := e.Store.Delete(ctx, slug); err != nil {
		t.Fatal(err)
	}
	e.labeled("gone", 42, "af:fix")
	waitFor(t, "the loop to go on the event", func() bool { return e.Poll.loopCount(slug) == 0 })
	e.conns(slug, 0)
	if got := e.Logs.level("ERROR"); len(got) != 0 {
		t.Errorf("ERROR records %v, want none for a workspace that is not subscribed", got)
	}
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
	// The handler of an event of widgets waits in its read of the issue.
	e.issue(42, "af:fix")
	e.Hub.Delay("/api/v1/workspaces/widgets/issues/42", time.Hour)
	e.labeled("held", 42, "af:fix")
	waitFor(t, "the read of 42", func() bool { return len(e.Hub.RequestsTo("GET", "/api/v1/workspaces/widgets/issues/42")) == 1 })
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

// streamWaits are the waits the clock has been asked for, but the loop's wait for its first cycle under noCycles.
func (e *env) streamWaits() []time.Duration {
	var out []time.Duration
	for _, w := range e.Clock.Waits() {
		if w != noCycleDelay {
			out = append(out, w)
		}
	}
	return out
}

// waits is how many waits of the stream the clock has been asked for so far.
func (e *env) waits() int { return len(e.streamWaits()) }

// nextWait waits for a wait of the stream after the n-th and returns it, with how many there are then.
func (e *env) nextWait(n int) (time.Duration, int) {
	e.t.Helper()
	waitFor(e.t, fmt.Sprintf("a wait after the %d-th", n), func() bool { return e.waits() > n })
	w := e.streamWaits()
	return w[len(w)-1], len(w)
}

// attemptAfter advances the clock by d less a tick and checks that no attempt is made, then by the tick and checks that
// one is.
func (e *env) attemptAfter(d time.Duration) {
	e.t.Helper()
	n := len(e.Hub.EventsRequests())
	e.Clock.Advance(d - time.Millisecond)
	settle()
	if got := len(e.Hub.EventsRequests()); got != n {
		e.t.Fatalf("an attempt came %v early", time.Millisecond)
	}
	e.Clock.Advance(time.Millisecond)
	waitFor(e.t, "the attempt", func() bool { return len(e.Hub.EventsRequests()) == n+1 })
}

// failing starts the poll with the events route failing with the status, and returns once the first attempt is made, with
// how many waits there were before it.
func (e *env) failing(status int) int {
	e.t.Helper()
	e.Hub.Fail("/api/v1/events", status)
	e.started()
	waitFor(e.t, "the first attempt", func() bool { return len(e.Hub.EventsRequests()) == 1 })
	return 0
}

// TestReconnectWaitsDoubleFromOneSecondToAMinute_TS_18_25 verifies TS-18-25.
// Verifies: 18-REQ-4.5
func TestReconnectWaitsDoubleFromOneSecondToAMinute_TS_18_25(t *testing.T) {
	e := newEnv(t, noCycles())
	e.started()
	e.conns(slug, 1)
	e.Hub.Heartbeat(slug) // hub's first frame of a connection
	settle()
	e.Hub.Fail("/api/v1/events", 503)
	n := e.waits()
	e.Hub.EndEvents(slug)
	w, n := e.nextWait(n)
	if w != time.Second {
		t.Fatalf("the wait after hub ended the stream is %v, want 1s", w)
	}
	e.attemptAfter(w)
	for _, want := range []time.Duration{1, 2, 4, 8, 16, 32, 60, 60} {
		w, n = e.nextWait(n)
		if w != want*time.Second {
			t.Fatalf("the wait is %v, want %v", w, want*time.Second)
		}
		e.attemptAfter(w)
	}
	if lists := len(e.Hub.ListRequests(slug)); lists != 0 {
		t.Errorf("%d cycles were brought forward", lists)
	}
}

// TestAfterHubIsFoundNotToServeTheStreamEveryWaitIs60s_TS_18_26 verifies TS-18-26.
// Verifies: 18-REQ-4.6
func TestAfterHubIsFoundNotToServeTheStreamEveryWaitIs60s_TS_18_26(t *testing.T) {
	for _, status := range []int{404, 405} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			e := newEnv(t, noCycles())
			n := e.failing(status)
			for range 3 {
				var w time.Duration
				if w, n = e.nextWait(n); w != time.Minute {
					t.Fatalf("the wait is %v, want 60s", w)
				}
				e.attemptAfter(w)
			}
		})
	}
}

// TestAFirstFrameOrCommentResetsTheWaitToOneSecond_TS_18_27 verifies TS-18-27.
// Verifies: 18-REQ-4.7
func TestAFirstFrameOrCommentResetsTheWaitToOneSecond_TS_18_27(t *testing.T) {
	for name, send := range map[string]func(e *env){"a comment": func(e *env) { e.Hub.Comment(slug) }, "a frame": func(e *env) { e.Hub.Heartbeat(slug) }} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, noCycles())
			n := e.failing(503)
			var w time.Duration
			for range 3 {
				w, n = e.nextWait(n)
				e.attemptAfter(w)
			}
			e.Hub.Clear()
			if w, _ = e.nextWait(n); w != 8*time.Second {
				t.Fatalf("the backoff is %v, want 8s", w)
			}
			e.Clock.Advance(w)
			e.conns(slug, 1)
			send(e)
			settle()
			n = e.waits()
			e.Hub.EndEvents(slug)
			if w, _ = e.nextWait(n); w != time.Second {
				t.Errorf("the wait after a connection with activity ended is %v, want 1s", w)
			}
		})
	}
}

// TestASilentConnectionIsClosedAfter60sAndHandlingAnEventDoesNotCount_TS_18_28 verifies TS-18-28.
// Verifies: 18-REQ-4.8
func TestASilentConnectionIsClosedAfter60sAndHandlingAnEventDoesNotCount_TS_18_28(t *testing.T) {
	t.Run("silence", func(t *testing.T) {
		e := newEnv(t, noCycles())
		e.started()
		e.conns(slug, 1)
		waitFor(t, "the idle limit", func() bool { return e.waits() == 1 })
		e.Clock.Advance(59 * time.Second)
		settle()
		if e.Hub.EventConns(slug) != 1 {
			t.Fatal("the connection was closed before 60 s of silence")
		}
		n := e.waits()
		e.Clock.Advance(time.Second)
		e.conns(slug, 0)
		w, _ := e.nextWait(n)
		e.Clock.Advance(w)
		waitFor(t, "a new connection", func() bool { return len(e.Hub.EventsRequests()) == 2 })
	})
	t.Run("an event is handled for 120 s", func(t *testing.T) {
		release := make(chan struct{})
		e := newEnv(t, noCycles())
		e.Poll.beforeEvent = func() { <-release }
		e.started()
		e.conns(slug, 1)
		waitFor(t, "the idle limit", func() bool { return e.waits() == 1 })
		e.Hub.SendEvent(slug, fakehub.HubEvent{ID: "evt-1", Type: "hub.forge.issue.labeled", Issue: 42, Label: "af:fix"})
		settle() // the event is being handled
		e.Clock.Advance(120 * time.Second)
		settle()
		close(release)
		settle()
		if n := e.Hub.EventConns(slug); n != 1 || len(e.Hub.EventsRequests()) != 1 {
			t.Errorf("%d connections after %d requests, want the one connection kept: the time of the handling does not count", n, len(e.Hub.EventsRequests()))
		}
	})
}

// TestTheFirstActivityAfterLoggedFailuresLogsOneInfoAndReArmsThem_TS_18_29 verifies TS-18-29.
// Verifies: 18-REQ-5.1
func TestTheFirstActivityAfterLoggedFailuresLogsOneInfoAndReArmsThem_TS_18_29(t *testing.T) {
	e := newEnv(t, noCycles())
	n := e.failing(503)
	waitFor(t, "the WARN", func() bool { return len(e.Logs.find("WARN", "event stream")) == 1 })
	e.Hub.Clear()
	w, _ := e.nextWait(n)
	e.Clock.Advance(w)
	e.conns(slug, 1)
	e.Hub.Comment(slug)
	waitFor(t, "the INFO", func() bool { return len(e.Logs.find("INFO", "event stream of the workspace has recovered")) == 1 })
	e.Hub.Fail("/api/v1/events", 503)
	n = e.waits()
	e.Hub.EndEvents(slug)
	w, _ = e.nextWait(n)
	e.Clock.Advance(w)
	waitFor(t, "the second WARN", func() bool { return len(e.Logs.find("WARN", "event stream")) == 2 })
	if got := len(e.Logs.find("INFO", "recovered")); got != 1 {
		t.Errorf("%d INFO records of the recovery, want 1", got)
	}
}

// TestRefusalsAndAnUnavailableHubAreEachLoggedOnceAndRetried_TS_18_30 verifies TS-18-30.
// Verifies: 18-REQ-5.2
func TestRefusalsAndAnUnavailableHubAreEachLoggedOnceAndRetried_TS_18_30(t *testing.T) {
	for _, c := range []struct {
		status int
		level  string
		says   string
	}{{401, "ERROR", "PUT /api/v1/workspaces/widgets"}, {403, "ERROR", "audit:read"}, {503, "WARN", "event stream"}} {
		t.Run(fmt.Sprint(c.status), func(t *testing.T) {
			e := newEnv(t, noCycles())
			n := e.failing(c.status)
			for range 3 {
				var w time.Duration
				w, n = e.nextWait(n)
				e.attemptAfter(w)
			}
			if got := e.Logs.find(c.level, c.says); len(got) != 1 {
				t.Errorf("%d %s records saying %q, want 1: %v", len(got), c.level, c.says, e.Logs.records())
			}
		})
	}
}

// TestA404OnTheEventsRouteIsOneWarnThatTheLabelsAreFoundByThePollAlone_TS_18_31 verifies TS-18-31.
// Verifies: 18-REQ-5.3
func TestA404OnTheEventsRouteIsOneWarnThatTheLabelsAreFoundByThePollAlone_TS_18_31(t *testing.T) {
	e := newEnv(t, noCycles())
	n := e.failing(404)
	for range 3 {
		var w time.Duration
		w, n = e.nextWait(n)
		e.attemptAfter(w)
	}
	if got := e.Logs.find("WARN", "does not serve the event stream"); len(got) != 1 || !strings.Contains(fmt.Sprint(got[0]["msg"]), "poll alone") {
		t.Errorf("the WARN records are %v, want one saying hub does not serve the stream and the poll finds the labels alone", got)
	}
	if got := len(e.Logs.level("ERROR")); got != 0 {
		t.Errorf("%d ERROR records, want none", got)
	}
	// Hub serving the stream again is a recovery, and the waits are the backoff's again.
	e.Hub.Clear()
	w, _ := e.nextWait(n)
	e.Clock.Advance(w)
	e.conns(slug, 1)
	e.Hub.Heartbeat(slug)
	waitFor(t, "the recovery", func() bool { return len(e.Logs.find("INFO", "has recovered")) == 1 })
	n = e.waits()
	e.Hub.EndEvents(slug)
	if w, _ = e.nextWait(n); w != time.Second {
		t.Errorf("the wait after the stream was served is %v, want 1s", w)
	}
}

// TestAnOutageLongerThanThePollIntervalIsOneWarn_TS_18_32 verifies TS-18-32.
// Verifies: 18-REQ-5.4
func TestAnOutageLongerThanThePollIntervalIsOneWarn_TS_18_32(t *testing.T) {
	outage := func(e *env, n int, until time.Duration) {
		start := e.Clock.Now()
		for e.Clock.Now().Sub(start) <= until {
			var w time.Duration
			w, n = e.nextWait(n)
			e.Clock.Advance(w)
		}
		settle()
	}
	t.Run("10m read", func(t *testing.T) {
		e := newEnv(t, noCycles(), withVars(map[string]string{"AF_POLL_INTERVAL": "10m"}))
		outage(e, e.failing(503), 10*time.Minute+time.Second)
		waitFor(t, "the outage WARN", func() bool { return len(e.Logs.find("WARN", "poll alone")) >= 1 })
		if got := len(e.Logs.find("WARN", "poll alone")); got != 1 {
			t.Errorf("%d outage WARN records, want 1", got)
		}
	})
	t.Run("not served", func(t *testing.T) {
		e := newEnv(t, noCycles(), withVars(map[string]string{"AF_POLL_INTERVAL": "10m"}))
		outage(e, e.failing(404), 10*time.Minute+time.Second)
		if got := e.Logs.find("WARN", "poll alone"); len(got) != 1 || !strings.Contains(fmt.Sprint(got[0]["msg"]), "does not serve") {
			t.Errorf("the WARN records are %v, want only the one that hub does not serve the stream", got)
		}
	})
	t.Run("no interval read", func(t *testing.T) {
		e := newEnv(t, noCycles())
		e.Hub.Fail("/api/v1/workspaces/widgets/vars/resolved", 503)
		e.started()
		waitFor(t, "the first read", func() bool { return len(e.Hub.RequestsTo("GET", "/api/v1/workspaces/widgets/vars/resolved")) == 1 })
		outage(e, 0, 5*time.Minute+time.Second)
		waitFor(t, "the outage WARN", func() bool { return len(e.Logs.find("WARN", "poll alone")) >= 1 })
		if got := len(e.Logs.find("WARN", "poll alone")); got != 1 {
			t.Errorf("%d outage WARN records after 5 minutes with no interval read, want 1", got)
		}
	})
}

// TestNoLogLineOfTheStreamHoldsThePAT_TS_18_36 verifies TS-18-36, a property: whatever the token, the failure and the
// events, no log line holds the token.
// Verifies: 18-REQ-5.8
func TestNoLogLineOfTheStreamHoldsThePAT_TS_18_36(t *testing.T) {
	rng := rand.New(rand.NewPCG(18, 36))
	echo := func(token string, status int) fakehub.Fault {
		return fakehub.Fault{Status: status, Body: fmt.Sprintf(`{"error":{"code":%d,"message":"no for %s","error_type":"x %s"}}`, status, token, token)}
	}
	for round := range 8 {
		b := make([]byte, 12+rng.IntN(20))
		for i := range b {
			b[i] = byte('a' + rng.IntN(26))
		}
		token := "pat-" + string(b)
		faults := []fakehub.Fault{echo(token, 401), echo(token, 403), echo(token, 404), echo(token, 429), echo(token, 503), {Hangup: true}, {Status: 200, Body: token}}
		f := faults[round%len(faults)]
		e := newEnv(t, noCycles())
		e.Hub.AddWorkspaceToken(token, []string{slug})
		if _, _, err := e.Store.Upsert(ctx, slug, "af:", token, "usr_1", "usr_1", epoch); err != nil {
			t.Fatal(err)
		}
		e.started()
		e.conns(slug, 1)
		for _, frame := range []string{"data: {\"id\":\"\",\"event_type\":\"hub.forge.issue.labeled\",\"metadata\":{\"label\":\"af:fix\"}}\n\n",
			"data: {\"id\":\"x\",\"event_type\":\"hub.other\"}\n\n", "data: " + token + "\n\n"} {
			e.Hub.SendFrame(slug, frame)
		}
		settle()
		e.Hub.Inject("GET", "/api/v1/events", f)
		n := e.waits()
		e.Hub.EndEvents(slug)
		for range 3 {
			var w time.Duration
			w, n = e.nextWait(n)
			e.Clock.Advance(w)
			settle()
		}
		if strings.Contains(e.Logs.String(), token) {
			t.Fatalf("round %d: a log line holds the token", round)
		}
	}
}
