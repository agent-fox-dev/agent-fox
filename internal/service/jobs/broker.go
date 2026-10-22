package jobs

import "sync"

// SubscriberBuffer is how many events a subscriber may be behind by.
const SubscriberBuffer = 256

// Broker hands the events of a job to those who follow it. It never waits for a
// subscriber: the one who is more than SubscriberBuffer events behind is closed, and
// recovers from the stored events (see Follower), so that a client that does not
// read can neither hold up the runner that reports nor the other clients.
type Broker struct {
	mu     sync.Mutex
	subs   map[string]map[*Subscription]struct{}
	closed bool
}

// NewBroker returns a broker without subscribers.
func NewBroker() *Broker { return &Broker{subs: map[string]map[*Subscription]struct{}{}} }

// Subscription is one who follows the events of a job.
type Subscription struct {
	// C gets the events published after the subscription was made, in order. It is
	// closed when the subscriber has fallen behind, when it is cancelled and when
	// the broker is closed.
	C <-chan Event

	ch     chan Event
	jobID  string
	broker *Broker
	slow   bool // guarded by the broker's lock, as is the closing of ch
	done   bool
}

// Subscribe follows the events of the job. Following a closed broker is a closed
// subscription.
func (b *Broker) Subscribe(jobID string) *Subscription {
	ch := make(chan Event, SubscriberBuffer)
	s := &Subscription{C: ch, ch: ch, jobID: jobID, broker: b}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		s.done = true
		close(ch)
		return s
	}
	if b.subs[jobID] == nil {
		b.subs[jobID] = map[*Subscription]struct{}{}
	}
	b.subs[jobID][s] = struct{}{}
	return s
}

// Publish hands the event to the subscribers of its job without waiting for any.
func (b *Broker) Publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for s := range b.subs[ev.JobID] {
		select {
		case s.ch <- ev:
		default:
			s.slow = true
			b.dropLocked(s)
		}
	}
}

// Close closes every subscription. A broker that is closed delivers nothing.
func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	for _, set := range b.subs {
		for s := range set {
			b.dropLocked(s)
		}
	}
}

// dropLocked removes the subscription and closes its channel, once.
func (b *Broker) dropLocked(s *Subscription) {
	if set := b.subs[s.jobID]; set != nil {
		delete(set, s)
		if len(set) == 0 {
			delete(b.subs, s.jobID)
		}
	}
	if !s.done {
		s.done = true
		close(s.ch)
	}
}

// Slow reports whether the subscription was closed because the subscriber fell behind.
func (s *Subscription) Slow() bool {
	s.broker.mu.Lock()
	defer s.broker.mu.Unlock()
	return s.slow
}

// Cancel ends the subscription.
func (s *Subscription) Cancel() {
	s.broker.mu.Lock()
	defer s.broker.mu.Unlock()
	s.broker.dropLocked(s)
}
