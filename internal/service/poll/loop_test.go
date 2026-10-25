package poll

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func withConfig(f func(*Config)) option { return func(_ *env, c *Config) { f(c) } }

// started starts the poll, which is stopped when the test ends.
func (e *env) started() {
	e.Poll.Start(ctx)
	e.t.Cleanup(e.Poll.Stop)
}

func TestNew_DefaultsToTheWallClockARandomFirstDelayAScanOfAMinuteAndAReleaseDelayOfFiveMinutes(t *testing.T) {
	d := New(Config{})
	if _, wall := d.cfg.Clock.(jobs.SystemClock); !wall || d.cfg.ScanPeriod != time.Minute || d.cfg.ReleaseDelay != 5*time.Minute || d.cfg.Logger == nil {
		t.Errorf("defaults: clock %T, scan %s, release %s; want the wall clock, 1m and 5m", d.cfg.Clock, d.cfg.ScanPeriod, d.cfg.ReleaseDelay)
	}
	for range 200 {
		if delay := d.cfg.FirstDelay(); delay < 0 || delay > 30*time.Second {
			t.Fatalf("the default first delay is %s, want 0 to 30 seconds", delay)
		}
	}
}

func TestScan_MakesTheLoopsThoseOfTheSubscriptions(t *testing.T) {
	e := newEnv(t, withConfig(func(c *Config) { c.FirstDelay = func() time.Duration { return time.Hour } }))
	e.addWorkspace("gadgets", "pat-GADGETS-0123456789")
	scanCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(func() { cancel(); e.Poll.wg.Wait() })
	loops := func() []int { return []int{e.Poll.loopCount(slug), e.Poll.loopCount("gadgets")} }

	e.Poll.scan(scanCtx)
	e.Poll.scan(scanCtx)
	if !slices.Equal(loops(), []int{1, 1}) || len(e.Log.Find("INFO", "the poll started for a workspace")) != 2 {
		t.Errorf("loops %v after two scans, logs %s; want one loop for each workspace", loops(), e.Log)
	}
	e.Subs.fail("List", errors.New("locked"))
	e.Poll.scan(scanCtx)
	if !slices.Equal(loops(), []int{1, 1}) || len(e.Log.Find("WARN", "cannot be listed")) != 1 {
		t.Errorf("loops %v after a scan that failed, logs %s; want them as they were and a WARN", loops(), e.Log)
	}
	e.Subs.fail("List", nil)
	if _, err := e.Store.Delete(ctx, "gadgets"); err != nil {
		t.Fatal(err)
	}
	e.Poll.scan(scanCtx)
	if !slices.Equal(loops(), []int{1, 0}) || len(e.Log.Find("INFO", "no longer subscribed")) != 1 {
		t.Errorf("loops %v after gadgets went, want only the loop of widgets", loops())
	}
}

func TestStart_ScansEveryScanPeriodAndStopEndsEveryLoopAndCycle(t *testing.T) {
	e := newEnv(t)
	e.Hub.Delay(listPath, time.Hour) // the cycle of widgets waits at its first list
	e.Poll.Start(ctx)
	e.Poll.Start(ctx) // a second start does nothing
	testutil.Eventually(t, "the cycle to reach the list", func() bool { return e.Poll.inflight.Load() == 1 && len(e.Hub.ListRequests(slug)) == 1 })
	const token = "pat-GADGETS-0123456789"
	e.addWorkspace("gadgets", token)
	e.Clock.Advance(time.Minute)
	testutil.Eventually(t, "the first cycle of gadgets", func() bool { return e.Hub.CountFor(fakehub.TokenHash(token)) >= 4 })

	e.Poll.Stop()

	if e.Poll.inflight.Load() != 0 || len(e.Log.Find("INFO", "the label poll stopped")) != 1 {
		t.Errorf("%d cycles in flight after Stop, logs %s", e.Poll.inflight.Load(), e.Log)
	}
	e.Poll.Start(ctx)
	if n := len(e.Log.Find("INFO", "the label poll started")); n != 1 {
		t.Errorf("the poll started %d times, want once: a stopped poll does not start again", n)
	}
}

func TestLoop_CyclesStartTheFirstDelayAfterTheStartAndThenAnIntervalAfterTheLastEnded(t *testing.T) {
	e := newEnv(t, withVars(map[string]string{intervalVar: "2m"}), withConfig(func(c *Config) { c.FirstDelay = func() time.Duration { return 17 * time.Second } }))
	starts, ends := make(chan time.Time, 8), make(chan time.Time, 8)
	e.Poll.reconciler = reconcilerFunc(func(context.Context, string) { starts <- e.Clock.Now() })
	e.Poll.handler = slow{e.Clock, ends}
	e.started()

	e.Clock.WaitForWaits(1, testutil.WaitTimeout)
	e.Clock.Advance(17 * time.Second)
	var begun, ended []time.Time
	for k := range 3 {
		begun = append(begun, <-starts)
		// The cycle waits on its cut-off and on its slow listing, and then the loop on the next cycle.
		e.Clock.WaitForWaits(3*k+3, testutil.WaitTimeout)
		e.Clock.Advance(time.Minute)
		ended = append(ended, <-ends)
		e.Clock.WaitForWaits(3*k+4, testutil.WaitTimeout)
		e.Clock.Advance(2 * time.Minute)
	}
	if !begun[0].Equal(epoch.Add(17 * time.Second)) {
		t.Errorf("the first cycle started at %s, want 17 seconds after the start", begun[0])
	}
	for k := range 2 {
		if gap := begun[k+1].Sub(ended[k]); gap != 2*time.Minute {
			t.Errorf("cycle %d started %s after the one before ended, want 2m", k+2, gap)
		}
	}
}

// slow is a handler whose triage listing takes a minute of the clock, and that notes when the fix listing ends a cycle.
type slow struct {
	clock *fakeclock.Clock
	ends  chan time.Time
}

func (s slow) handle(ctx context.Context, _ *cycleRun, l *listing) bool {
	if l.Kind == jobs.KindFix {
		s.ends <- s.clock.Now()
		return true
	}
	select {
	case <-s.clock.After(time.Minute):
		return true
	case <-ctx.Done():
		return false
	}
}

func TestLoop_AtMostFourCyclesRunAtOnceAndADueCycleWaitsForASlot(t *testing.T) {
	e := newEnv(t)
	for i := range 5 {
		e.addWorkspace(fmt.Sprintf("ws%d", i), fmt.Sprintf("pat-WS%d-0123456789", i))
	}
	var running atomic.Int32
	release, started := make(chan struct{}), make(chan string, 8)
	e.Poll.reconciler = reconcilerFunc(func(ctx context.Context, workspace string) {
		if n := running.Add(1); n > maxCycles {
			t.Errorf("%d cycles run at once, want at most 4", n)
		}
		started <- workspace
		select {
		case <-release:
		case <-ctx.Done():
		}
		running.Add(-1)
	})

	e.started() // six workspaces, all due at once

	var first []string
	for range 4 {
		first = append(first, <-started)
	}
	if len(e.Poll.slots) != maxCycles {
		t.Errorf("%d slots taken, want all 4: the other cycles wait", len(e.Poll.slots))
	}
	release <- struct{}{} // one cycle ends
	if w := <-started; slices.Contains(first, w) {
		t.Errorf("the cycle that took the free slot is %s, one of the first four", w)
	}
}

func TestLoop_ACycleThatRunsFiveMinutesIsCancelledAndWhatItDidStands(t *testing.T) {
	e := newEnv(t, withVars(map[string]string{intervalVar: "2m"}))
	e.issue(7, "af:triage")
	e.issue(42, "af:fix")
	e.Hub.Inject("GET", listPath, fakehub.Fault{Delay: time.Hour, Skip: 1}) // the triage list is answered, the fix list hangs
	e.started()
	testutil.Eventually(t, "the fix list to hang", func() bool { return len(e.Hub.ListRequests(slug)) == 2 })

	e.Clock.Advance(cycleLimit)

	testutil.Eventually(t, "the loop to wait 2m for its next cycle", func() bool {
		w := e.Clock.Waits()
		return e.Poll.inflight.Load() == 0 && len(e.Poll.slots) == 0 && len(w) > 0 && w[len(w)-1] == 2*time.Minute
	})
	// The cancelled cycle reports no failure of its own: only the cut-off is said.
	if len(e.Jobs.creates) != 1 || len(e.labels(7)) != 0 || len(e.Log.Find("WARN", "")) != 1 || len(e.Log.Find("WARN", "cancelled after 5 minutes")) != 1 || len(e.Log.Find("ERROR", "")) != 0 {
		t.Errorf("%d jobs, labels of 7 %v, logs %s; want the triage job and its label taken, and the one WARN of the cut-off", len(e.Jobs.creates), e.labels(7), e.Log)
	}
}

func TestLoop_EndsWhenItsCycleFindsTheWorkspaceNotSubscribed(t *testing.T) {
	e := newEnv(t, withConfig(func(c *Config) { c.ScanPeriod = 1000 * time.Hour })) // only the cycle can end the loop
	e.started()
	testutil.Eventually(t, "the first cycle", func() bool { return len(e.Hub.ListRequests(slug)) == 2 })
	e.Clock.WaitForWaits(3, testutil.WaitTimeout) // the first delay, the cut-off of the cycle and the wait for the next
	if _, err := e.Store.Delete(ctx, slug); err != nil {
		t.Fatal(err)
	}
	e.Clock.Advance(5 * time.Minute)
	testutil.Eventually(t, "the loop to end", func() bool { return e.Poll.loopCount(slug) == 0 })
}
