package jobs

import "time"

// Clock is the time the layer reads, and the waits it makes by it. Production uses
// the system's; the tests move their own.
type Clock interface {
	// Now is the time.
	Now() time.Time
	// After returns a channel that gets the time once d has passed.
	After(d time.Duration) <-chan time.Time
	// NewTicker returns a ticker that ticks every d.
	NewTicker(d time.Duration) Ticker
}

// Ticker is the ticker of a Clock.
type Ticker interface {
	// C is the channel the ticks arrive on.
	C() <-chan time.Time
	// Stop ends the ticker.
	Stop()
}

// SystemClock is the clock of the system.
type SystemClock struct{}

// Now is time.Now.
func (SystemClock) Now() time.Time { return time.Now() }

// After is time.After.
func (SystemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// NewTicker is time.NewTicker.
func (SystemClock) NewTicker(d time.Duration) Ticker { return systemTicker{time.NewTicker(d)} }

type systemTicker struct{ t *time.Ticker }

func (s systemTicker) C() <-chan time.Time { return s.t.C }
func (s systemTicker) Stop()               { s.t.Stop() }
