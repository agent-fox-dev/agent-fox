// Package fakeclock is a clock for tests of the jobs layer: time stands still until a
// test moves it, the waits that the layer asks for are recorded and end when the
// test advances the time to them, and its tickers tick when the time passes their
// period. It has the methods of jobs.Clock.
package fakeclock

import (
	"sync"
	"time"
)

// Clock is the fake clock.
type Clock struct {
	mu      sync.Mutex
	now     time.Time
	timers  []*timer
	tickers []*Ticker
	waits   []time.Duration
}

type timer struct {
	at time.Time
	ch chan time.Time
}

// New returns a clock that reads start.
func New(start time.Time) *Clock { return &Clock{now: start} }

// Now is the time on the clock.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// After records the wait d and returns a channel that gets the time once the clock has
// been advanced by d. A wait of nothing ends at once.
func (c *Clock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waits = append(c.waits, d)
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
		return ch
	}
	c.timers = append(c.timers, &timer{at: c.now.Add(d), ch: ch})
	return ch
}

// Waits are the durations that After was asked for, in order.
func (c *Clock) Waits() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.waits...)
}

// WaitForWaits waits until After has been asked for at least n times, and says
// whether it was within d of real time.
func (c *Clock) WaitForWaits(n int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if len(c.Waits()) >= n {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

// Pending is how many waits have not ended.
func (c *Clock) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

// Advance moves the time on by d: the waits that are due end, and each ticker ticks
// once for each of its periods that has passed (a tick is dropped when the last has
// not been read, as a ticker of the system does).
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.timers[:0]
	for _, t := range c.timers {
		if !t.at.After(c.now) {
			t.ch <- c.now
		} else {
			kept = append(kept, t)
		}
	}
	c.timers = kept
	for _, t := range c.tickers {
		for !t.stopped && !t.next.After(c.now) {
			select {
			case t.ch <- c.now:
			default:
			}
			t.next = t.next.Add(t.period)
		}
	}
}

// Ticker is a ticker of the fake clock.
type Ticker struct {
	clock   *Clock
	period  time.Duration
	next    time.Time
	ch      chan time.Time
	stopped bool
}

// NewTicker returns a ticker that ticks every d of the clock's time.
func (c *Clock) NewTicker(d time.Duration) interface {
	C() <-chan time.Time
	Stop()
} {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &Ticker{clock: c, period: d, next: c.now.Add(d), ch: make(chan time.Time, 1)}
	c.tickers = append(c.tickers, t)
	return t
}

// C is where the ticks arrive.
func (t *Ticker) C() <-chan time.Time { return t.ch }

// Stop ends the ticker.
func (t *Ticker) Stop() {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	t.stopped = true
}
