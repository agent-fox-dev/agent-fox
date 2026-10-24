package poll

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
)

// waitFor waits until the condition holds, for at most 10 seconds of real time.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// addWorkspace makes the workspace known to hub with its own token, and subscribes it with the prefix af:.
func (e *env) addWorkspace(workspace, token string) {
	e.t.Helper()
	e.Hub.AddWorkspace(e.t, fakehub.Workspace{Slug: workspace, PAT: token})
	if _, _, err := e.Store.Upsert(ctx, workspace, token, "usr_1", "usr_1", epoch); err != nil {
		e.t.Fatal(err)
	}
}

// started starts the poll, which is stopped when the test ends.
func (e *env) started() {
	e.t.Helper()
	e.Poll.Start(ctx)
	e.t.Cleanup(e.Poll.Stop)
}

// hits is how many requests hub has had with the token.
func (e *env) hits(token string) int { return e.Hub.CountFor(fakehub.TokenHash(token)) }

// settle gives the poll the time to do what it is not to do.
func settle() { time.Sleep(60 * time.Millisecond) }

// TestStartStartsExactlyOneLoopForEachSubscribedWorkspace_TS_15_1 verifies the part of TS-15-1 that the package shows:
// one loop for each workspace that is subscribed when the poll starts, and no second loop for a workspace if it is started
// again. The order of the start, after the jobs layer, is shown in the binary (cmd/agent-fox).
// Verifies: 15-REQ-1.1
func TestStartStartsExactlyOneLoopForEachSubscribedWorkspace_TS_15_1(t *testing.T) {
	e := newEnv(t, withPoll(func(c *Config) { c.FirstDelay = func() time.Duration { return time.Hour } }))
	e.addWorkspace("gadgets", "pat-GADGETS-0123456789")

	e.started()
	e.Poll.Start(ctx)

	if w, g := e.Poll.loopCount(slug), e.Poll.loopCount("gadgets"); w != 1 || g != 1 {
		t.Errorf("loops: widgets %d, gadgets %d, want one each", w, g)
	}
	if n := len(e.Logs.find("INFO", "the poll started for a workspace")); n != 2 {
		t.Errorf("%d loops were started, want 2", n)
	}
}

// TestAScanStartsALoopForANewSubscriptionAndNeverASecondForAnExistingOne_TS_15_2 verifies TS-15-2.
// Verifies: 15-REQ-1.2
func TestAScanStartsALoopForANewSubscriptionAndNeverASecondForAnExistingOne_TS_15_2(t *testing.T) {
	const token = "pat-GADGETS-0123456789"
	e := newEnv(t)
	e.started()
	if e.Poll.loopCount(slug) != 1 {
		t.Fatal("widgets has no loop")
	}

	e.addWorkspace("gadgets", token)
	e.Clock.Advance(60 * time.Second)
	waitFor(t, "the scan to start the loop of gadgets", func() bool { return e.Poll.loopCount("gadgets") == 1 })
	e.Clock.Advance(60 * time.Second)
	settle()

	if w, g := e.Poll.loopCount(slug), e.Poll.loopCount("gadgets"); w != 1 || g != 1 {
		t.Errorf("loops: widgets %d, gadgets %d, want one each after two scans", w, g)
	}
	if n := len(e.Logs.find("INFO", "the poll started for a workspace")); n != 2 {
		t.Errorf("%d loops were started in all, want 2: one for widgets and one for gadgets", n)
	}
	waitFor(t, "the first poll of gadgets", func() bool { return e.hits(token) > 0 })
}

// TestUnsubscribingStopsTheLoopAndCancelsTheCycleInFlight_TS_15_3 verifies TS-15-3.
// Verifies: 15-REQ-1.3
func TestUnsubscribingStopsTheLoopAndCancelsTheCycleInFlight_TS_15_3(t *testing.T) {
	e := newEnv(t)
	e.Hub.Delay(listPath, time.Hour) // the cycle waits at its first list
	e.started()
	waitFor(t, "the cycle to reach the list", func() bool { return len(e.Hub.ListRequests(slug)) > 0 })
	if e.Poll.inflight.Load() != 1 {
		t.Fatalf("%d cycles in flight, want the one that waits for hub", e.Poll.inflight.Load())
	}

	if _, err := e.Store.Delete(ctx, slug); err != nil {
		t.Fatal(err)
	}
	e.Clock.Advance(60 * time.Second) // the scan
	waitFor(t, "the loop to go and its cycle to be cancelled", func() bool { return e.Poll.loopCount(slug) == 0 && e.Poll.inflight.Load() == 0 })

	n := e.hits(pat)
	e.Clock.Advance(time.Hour)
	settle()
	if e.hits(pat) != n {
		t.Errorf("hub saw %d more requests with the token of the workspace after the scan", e.hits(pat)-n)
	}
	if e.Poll.loopCount(slug) != 0 {
		t.Error("the loop is back")
	}
}

// timeline notes when the cycles of a workspace start and end, and how many ran at once. The cycle stands for a slow hub: its
// first listing waits for d of the injected clock, and the cycle ends with its second.
type timeline struct {
	mu              sync.Mutex
	clock           *fakeclock.Clock
	hold            time.Duration
	starts, ends    []time.Time
	running, peak   int
	holding, listed chan struct{}
}

func newTimeline(clock *fakeclock.Clock, hold time.Duration) *timeline {
	return &timeline{clock: clock, hold: hold, holding: make(chan struct{}, 64), listed: make(chan struct{}, 64)}
}

func (tl *timeline) reconcile(context.Context, string) {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	tl.starts = append(tl.starts, tl.clock.Now())
	tl.running++
	tl.peak = max(tl.peak, tl.running)
}

func (tl *timeline) handle(ctx context.Context, _ *cycleRun, l *listing) bool {
	if l.Kind == jobs.KindTriage {
		wait := tl.clock.After(tl.hold)
		tl.holding <- struct{}{}
		select {
		case <-wait:
		case <-ctx.Done():
			return false
		}
		return true
	}
	tl.mu.Lock()
	tl.ends = append(tl.ends, tl.clock.Now())
	tl.running--
	tl.mu.Unlock()
	tl.listed <- struct{}{}
	return true
}

// TestTheFirstCycleWaitsTheInjectedDelayLaterCyclesStartOneIntervalAfterTheLastEndedAndNeverOverlap_TS_15_4 verifies TS-15-4.
// Verifies: 15-REQ-1.4
func TestTheFirstCycleWaitsTheInjectedDelayLaterCyclesStartOneIntervalAfterTheLastEndedAndNeverOverlap_TS_15_4(t *testing.T) {
	e := newEnv(t, withVars(map[string]string{"AF_POLL_INTERVAL": "2m"}), withPoll(func(c *Config) { c.FirstDelay = func() time.Duration { return 17 * time.Second } }))
	tl := newTimeline(e.Clock, 2*time.Minute)
	e.Poll.reconciler, e.Poll.handler = tl, tl
	e.started()

	e.Clock.WaitForWaits(1, 10*time.Second) // the loop waits for its first cycle
	e.Clock.Advance(17 * time.Second)
	for k := 1; k <= 4; k++ {
		// A cycle: its cut-off timer, the wait of its slow listing, and the wait of the loop after it.
		if !e.Clock.WaitForWaits(1+3*(k-1)+2, 10*time.Second) {
			t.Fatalf("cycle %d did not reach its slow listing", k)
		}
		<-tl.holding
		e.Clock.Advance(2 * time.Minute)
		<-tl.listed
		if !e.Clock.WaitForWaits(1+3*k, 10*time.Second) {
			t.Fatalf("the loop did not wait for cycle %d", k+1)
		}
		e.Clock.Advance(2 * time.Minute)
	}

	tl.mu.Lock()
	defer tl.mu.Unlock()
	if len(tl.starts) < 4 || len(tl.ends) < 4 {
		t.Fatalf("%d starts and %d ends, want 4 cycles", len(tl.starts), len(tl.ends))
	}
	if want := epoch.Add(17 * time.Second); !tl.starts[0].Equal(want) {
		t.Errorf("the first cycle started at %s, want %s: 17 seconds after the start", tl.starts[0], want)
	}
	for i := range 3 {
		if gap := tl.starts[i+1].Sub(tl.ends[i]); gap != 2*time.Minute {
			t.Errorf("cycle %d started %s after cycle %d ended, want 2m0s", i+2, gap, i+1)
		}
	}
	if tl.peak != 1 {
		t.Errorf("%d cycles of one workspace ran at once, want 1", tl.peak)
	}
	for range 1000 {
		if d := randomDelay(); d < 0 || d > 30*time.Second {
			t.Fatalf("the default first delay is %s, want a time from 0 to 30 seconds", d)
		}
	}
}

// TestAtMostFourCyclesRunAtOnceAndADueCycleWaitsForASlot_TS_15_5 verifies TS-15-5.
// Verifies: 15-REQ-1.5
func TestAtMostFourCyclesRunAtOnceAndADueCycleWaitsForASlot_TS_15_5(t *testing.T) {
	e := newEnv(t)
	for i := range 5 {
		e.addWorkspace(fmt.Sprintf("ws%d", i), fmt.Sprintf("pat-WS%d-0123456789", i))
	}
	var running, peak, begun atomic.Int32
	release := map[string]chan struct{}{}
	started := make(chan string, 16)
	var mu sync.Mutex
	e.Poll.reconciler = reconcilerFunc(func(ctx context.Context, workspace string) {
		mu.Lock()
		gate := release[workspace]
		if gate == nil {
			gate = make(chan struct{})
			release[workspace] = gate
		}
		mu.Unlock()
		n := running.Add(1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		begun.Add(1)
		started <- workspace
		select {
		case <-gate:
		case <-ctx.Done():
		}
		running.Add(-1)
	})

	e.started() // six workspaces, all due at once

	var first []string
	for range 4 {
		select {
		case w := <-started:
			first = append(first, w)
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d cycles started: %v", len(first), first)
		}
	}
	settle()
	if begun.Load() != 4 || e.Poll.inflight.Load() != 4 {
		t.Errorf("%d cycles have begun and %d are in flight, want 4 and 4: the other two wait for a slot", begun.Load(), e.Poll.inflight.Load())
	}

	mu.Lock()
	close(release[first[0]])
	mu.Unlock()
	select {
	case w := <-started:
		if slices.Contains(first, w) {
			t.Errorf("the cycle that started when a slot was free is one of the first four: %s", w)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no waiting cycle started when a cycle ended")
	}
	settle()
	if begun.Load() != 5 {
		t.Errorf("%d cycles have begun, want 5", begun.Load())
	}
	if peak.Load() > 4 {
		t.Errorf("%d cycles ran at once, want at most 4", peak.Load())
	}
}

// TestACycleThatRunsFor5MinutesIsCancelledKeepsWhatItDidAndFreesItsSlot_TS_15_6 verifies TS-15-6: the triage job for
// 7 was made, and its label taken, by an earlier cycle; the list of the fix label hangs, the cycle is cancelled when 5
// minutes of the injected clock have passed, the job stands, the slot is free, and the next cycle is due after the
// interval the workspace last read.
// Verifies: 15-REQ-1.6
func TestACycleThatRunsFor5MinutesIsCancelledKeepsWhatItDidAndFreesItsSlot_TS_15_6(t *testing.T) {
	e := newEnv(t, withVars(map[string]string{"AF_POLL_INTERVAL": "2m"}))
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 7})
	if _, err := e.Jobs.Create(ctx, jobs.CreateRequest{
		Workspace: slug, Kind: jobs.KindTriage, Body: jobs.Body{Issue: json.RawMessage("7")},
		Source: jobs.Source{Kind: "label", By: "label af:triage"}, RequestLabel: "af:triage",
	}); err != nil {
		t.Fatal(err)
	}
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 42, Labels: []string{"af:fix"}})
	e.Hub.Inject("GET", listPath, fakehub.Fault{Delay: time.Hour, Skip: 1}) // the triage list is answered, the fix list hangs
	e.started()
	waitFor(t, "the fix list to hang", func() bool { return len(e.Hub.ListRequests(slug)) == 2 })

	e.Clock.Advance(5 * time.Minute)

	waitFor(t, "the cycle to be cancelled and its slot freed", func() bool { return e.Poll.inflight.Load() == 0 && len(e.Poll.slots) == 0 })
	if got := e.jobList(); len(got) != 1 || got[0].Issue.Int64 != 7 {
		t.Errorf("jobs %+v, want the triage job for 7 to stand", got)
	}
	if issue, _ := e.Hub.IssueOf(slug, 7); slices.Contains(issue.Labels, "af:triage") {
		t.Error("the label af:triage is back on issue 7")
	}
	waitFor(t, "the loop to wait for its next cycle, an interval of 2m after the one it last read", func() bool {
		w := e.Clock.Waits()
		return len(w) > 0 && w[len(w)-1] == 2*time.Minute
	})
	if n := len(e.Logs.find("WARN", "cancelled after 5 minutes")); n != 1 {
		t.Errorf("%d records of the cut-off, want 1", n)
	}
}

// TestShutdownStartsNoNewCycleAndCancelsTheCyclesInFlight_TS_15_7 verifies the part of TS-15-7 that the package shows:
// Stop returns with the cycle in flight cancelled, and the clock moving on starts no cycle. That it returns before the
// jobs layer stops is shown in the binary.
// Verifies: 15-REQ-1.7
func TestShutdownStartsNoNewCycleAndCancelsTheCyclesInFlight_TS_15_7(t *testing.T) {
	e := newEnv(t)
	e.Hub.Delay(listPath, time.Hour)
	e.started()
	waitFor(t, "the cycle to reach the list", func() bool { return len(e.Hub.ListRequests(slug)) > 0 })

	stopped := make(chan struct{})
	go func() { e.Poll.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return with a cycle in flight")
	}
	if e.Poll.inflight.Load() != 0 {
		t.Errorf("%d cycles are in flight after Stop", e.Poll.inflight.Load())
	}

	n := e.Hub.Total()
	e.Clock.Advance(time.Hour)
	settle()
	if e.Hub.Total() != n {
		t.Errorf("hub had %d requests after Stop", e.Hub.Total()-n)
	}
	e.addWorkspace("gadgets", "pat-GADGETS-0123456789")
	e.Clock.Advance(time.Hour)
	settle()
	if e.Poll.loopCount("gadgets") != 0 {
		t.Error("a loop was started after Stop")
	}
}

// TestTheSupervisorDefaultsToTheWallClockARandomDelayAScanOfSixtySecondsAndAReleaseDelayOfFiveMinutes_TS_15_8 verifies
// TS-15-8.
// Verifies: 15-REQ-1.8
func TestTheSupervisorDefaultsToTheWallClockARandomDelayAScanOfSixtySecondsAndAReleaseDelayOfFiveMinutes_TS_15_8(t *testing.T) {
	d := New(Config{})
	if _, wall := d.cfg.Clock.(jobs.SystemClock); !wall || d.cfg.ScanPeriod != 60*time.Second || d.cfg.ReleaseDelay != 5*time.Minute {
		t.Errorf("defaults: clock %T, scan %s, release %s; want the wall clock, 1m0s and 5m0s", d.cfg.Clock, d.cfg.ScanPeriod, d.cfg.ReleaseDelay)
	}
	for range 1000 {
		if delay := d.cfg.FirstDelay(); delay < 0 || delay > 30*time.Second {
			t.Fatalf("the default delay is %s, want a time from 0 to 30 seconds", delay)
		}
	}

	clk := fakeclock.New(epoch)
	i := New(Config{Clock: clk, FirstDelay: func() time.Duration { return 7 * time.Second }, ScanPeriod: 11 * time.Second, ReleaseDelay: 13 * time.Second})
	if i.cfg.Clock != jobs.Clock(clk) || i.cfg.FirstDelay() != 7*time.Second || i.cfg.ScanPeriod != 11*time.Second || i.cfg.ReleaseDelay != 13*time.Second {
		t.Errorf("the injected values are not used: %+v", i.cfg)
	}
}
