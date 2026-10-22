package fakeclock_test

import (
	"slices"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
)

var start = time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)

func fired(ch <-chan time.Time) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// TestTheFakeClockMovesOnlyWhenAdvancedAndEndsTheWaitsThatAreDue checks the clock the
// tests of the layer are written against: time stands still, a wait ends when the
// time reaches it, the waits asked for are recorded in order, a tick is dropped when
// the last one was not read, and a stopped ticker is silent.
func TestTheFakeClockMovesOnlyWhenAdvancedAndEndsTheWaitsThatAreDue(t *testing.T) {
	c := fakeclock.New(start)
	if !c.Now().Equal(start) {
		t.Fatalf("Now = %v, want %v", c.Now(), start)
	}
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
	if fired(short) {
		t.Error("the 30 s wait ended at 29 s")
	}
	c.Advance(time.Second)
	if !fired(short) || fired(long) || c.Pending() != 1 || !c.Now().Equal(start.Add(30*time.Second)) {
		t.Errorf("at 30 s: short fired, long not, pending 1, now %v", c.Now())
	}
	c.Advance(2 * time.Minute)
	if !fired(long) || c.Pending() != 0 {
		t.Error("the 2 min wait did not end")
	}

	ticker := c.NewTicker(time.Minute)
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
	if fired(ticker.C()) {
		t.Error("a stopped ticker ticked")
	}

	if c.WaitForWaits(4, 10*time.Millisecond) {
		t.Error("WaitForWaits said there were 4 waits")
	}
	go func() { time.Sleep(20 * time.Millisecond); c.After(time.Second) }()
	if !c.WaitForWaits(4, 5*time.Second) {
		t.Error("WaitForWaits did not see the 4th wait")
	}
}

// The fake clock is a clock of the layer.
var _ jobs.Clock = (*fakeclock.Clock)(nil)
