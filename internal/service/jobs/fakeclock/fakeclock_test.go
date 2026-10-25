package fakeclock_test

import (
	"slices"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// The fake clock is a clock of the jobs layer.
var _ jobs.Clock = (*fakeclock.Clock)(nil)

var start = time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)

func fired(ch <-chan time.Time) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestAfter_EndsWhenTheClockIsAdvancedToIt(t *testing.T) {
	c := fakeclock.New(start)
	short, long, none := c.After(30*time.Second), c.After(2*time.Minute), c.After(0)
	if !fired(none) {
		t.Error("a wait of nothing did not end at once")
	}
	if fired(short) || fired(long) || c.Pending() != 2 {
		t.Fatalf("a wait ended before the time reached it (pending %d)", c.Pending())
	}
	if want := []time.Duration{30 * time.Second, 2 * time.Minute, 0}; !slices.Equal(c.Waits(), want) {
		t.Errorf("Waits = %v, want %v", c.Waits(), want)
	}
	c.Advance(29 * time.Second)
	if fired(short) || !c.Now().Equal(start.Add(29*time.Second)) {
		t.Errorf("the 30 s wait ended at 29 s, or the clock reads %v", c.Now())
	}
	c.Advance(time.Second)
	if !fired(short) || fired(long) || c.Pending() != 1 {
		t.Errorf("at 30 s: want the short wait over and the long one pending (pending %d)", c.Pending())
	}
	c.Advance(2 * time.Minute)
	if !fired(long) || c.Pending() != 0 {
		t.Error("the 2 min wait did not end")
	}
}

func TestTicker_TicksPerPeriodDropsUnreadTicksAndStops(t *testing.T) {
	c := fakeclock.New(start)
	ticker := c.NewTicker(time.Minute)
	if c.ActiveTickers() != 1 {
		t.Errorf("ActiveTickers = %d, want 1", c.ActiveTickers())
	}
	c.Advance(59 * time.Second)
	if fired(ticker.C()) {
		t.Error("the ticker ticked before its period")
	}
	c.Advance(time.Second)
	if !fired(ticker.C()) {
		t.Error("the ticker did not tick after its period")
	}
	c.Advance(3 * time.Minute) // three ticks, none read: one is kept
	if !fired(ticker.C()) || fired(ticker.C()) {
		t.Error("a ticker that was not read holds more or less than one tick")
	}
	ticker.Stop()
	c.Advance(time.Hour)
	if fired(ticker.C()) || c.ActiveTickers() != 0 {
		t.Errorf("a stopped ticker ticked or is counted active (%d)", c.ActiveTickers())
	}
}

func TestWaitForWaitsAndTickers_ReportWhetherTheyCameInTime(t *testing.T) {
	c := fakeclock.New(start)
	if c.WaitForWaits(1, time.Millisecond) || c.WaitForActiveTickers(1, time.Millisecond) {
		t.Fatal("a wait or a ticker was reported where there is none")
	}
	go func() {
		c.After(time.Second)
		c.NewTicker(time.Second)
	}()
	if !c.WaitForWaits(1, testutil.WaitTimeout) {
		t.Error("WaitForWaits did not see the wait")
	}
	if !c.WaitForActiveTickers(1, testutil.WaitTimeout) {
		t.Error("WaitForActiveTickers did not see the ticker")
	}
}
