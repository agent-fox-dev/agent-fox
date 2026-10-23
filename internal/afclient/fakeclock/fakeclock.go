// Package fakeclock is a clock for tests of the client of the agent-fox service. Time stands
// still until a test moves it. A wait of at most Quick ends at once and moves the time on, so
// that the pauses between the tries of a follower cost nothing and are recorded; a longer wait
// (the idle cut of a stream, the bound of a request) ends when the test advances the time to it.
// It has the methods of afclient.Clock.
package fakeclock

import (
	"sync"
	"time"
)

// Quick is the longest wait that ends at once.
const Quick = 10 * time.Second

// Clock is the fake clock.
type Clock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*timer
	waits  []time.Duration
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

// After records the wait d. A wait of at most Quick ends at once and moves the time on by d; a
// longer one ends once the clock has been advanced by d.
func (c *Clock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waits = append(c.waits, d)
	ch := make(chan time.Time, 1)
	if d <= Quick {
		if d > 0 {
			c.now = c.now.Add(d)
		}
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

// Sleeps are the waits of at most Quick, which a follower makes between its tries, in order.
func (c *Clock) Sleeps() []time.Duration {
	var out []time.Duration
	for _, d := range c.Waits() {
		if d <= Quick {
			out = append(out, d)
		}
	}
	return out
}

// Advance moves the time on by d: the waits that are due end.
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
}

// WaitForWaits waits until After has been asked for at least n waits longer than Quick, and
// says whether it was within d of real time.
func (c *Clock) WaitForWaits(n int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		long := 0
		for _, w := range c.Waits() {
			if w > Quick {
				long++
			}
		}
		if long >= n {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

// WaitForWait waits until After has been asked for the wait d at least n times, and says whether it
// was within timeout of real time.
func (c *Clock) WaitForWait(d time.Duration, n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		count := 0
		for _, w := range c.Waits() {
			if w == d {
				count++
			}
		}
		if count >= n {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}
