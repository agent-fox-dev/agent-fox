package poll

import (
	"context"
	"time"
)

// The limits of the cycles: at most this many run at once across the workspaces, and a cycle that has run this long
// is cancelled. Neither is configurable.
const (
	maxCycles  = 4
	cycleLimit = 5 * time.Minute
)

// loop is the loop of one workspace: the function that ends it and the cycle it has in flight.
type loop struct{ cancel context.CancelFunc }

// Start reads the subscriptions and starts one loop for each workspace that is subscribed, and then reads them every
// scan period, so that a workspace that is subscribed later gets a loop and a workspace that is gone loses its loop and
// the cycle it has in flight. It returns once the first scan has started its loops, which wait for their first cycle on
// their own. It does nothing if the poll was started already, or stopped.
func (s *Supervisor) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started || s.stopped {
		s.mu.Unlock()
		return
	}
	s.started = true
	ctx, s.cancel = context.WithCancel(ctx)
	s.mu.Unlock()

	s.cfg.Logger.Info("the label poll started")
	s.scan(ctx)
	ticker := s.cfg.Clock.NewTicker(s.cfg.ScanPeriod)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C():
				s.scan(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop starts no new cycle, cancels the cycles in flight, and returns once every loop and every cycle has returned.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	s.stopped = true
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
	if cancel != nil {
		s.cfg.Logger.Info("the label poll stopped")
	}
}

// scan makes the loops those of the subscriptions: one for each workspace that is subscribed, none for a workspace that
// is not, and the cycle in flight of a loop that is taken away is cancelled.
func (s *Supervisor) scan(ctx context.Context) {
	subs, err := s.cfg.Subscriptions.List(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.cfg.Logger.Warn("the subscriptions cannot be listed, so the loops of the poll stay as they are until the next scan", "error", err.Error())
		}
		return
	}
	subscribed := make(map[string]bool, len(subs))
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	for _, sub := range subs {
		subscribed[sub.Slug] = true
		if _, running := s.loops[sub.Slug]; !running {
			s.startLoop(ctx, sub.Slug)
		}
	}
	for slug, l := range s.loops {
		if !subscribed[slug] {
			l.cancel()
			delete(s.loops, slug)
			delete(s.states, slug)
			s.cfg.Logger.Info("the workspace is no longer subscribed, so its poll stopped", "workspace", slug)
		}
	}
}

// startLoop starts the loop of the workspace. The caller holds the lock.
func (s *Supervisor) startLoop(ctx context.Context, slug string) {
	ctx, cancel := context.WithCancel(ctx)
	l := &loop{cancel: cancel}
	s.loops[slug] = l
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.run(ctx, slug, l)
	}()
	s.cfg.Logger.Info("the poll started for a workspace", "workspace", slug)
}

// loopCount is how many loops there are for the workspace: one or none.
func (s *Supervisor) loopCount(slug string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.loops[slug]; ok {
		return 1
	}
	return 0
}

// run is the loop of a workspace: the first cycle after a delay, and each later cycle one interval after the one before
// it ended, so that two cycles of a workspace never overlap.
func (s *Supervisor) run(ctx context.Context, slug string, l *loop) {
	if !s.sleep(ctx, s.cfg.FirstDelay()) {
		return
	}
	for {
		next, stop, ran := s.cycleInSlot(ctx, slug)
		switch {
		case !ran:
			return
		case stop:
			s.forget(slug, l)
			return
		}
		if !s.sleep(ctx, next) {
			return
		}
	}
}

// sleep waits d by the clock of the poll, and says whether it waited it all: false means the context ended first.
func (s *Supervisor) sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-s.cfg.Clock.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

// cycleInSlot waits for a slot, makes a cycle in it and frees it. A cycle that has run for 5 minutes is cancelled, keeps
// what it has done, and is followed after the interval the workspace last read, as for a cycle that ended early. ran is
// false if the context ended before the cycle could start or while it ran.
func (s *Supervisor) cycleInSlot(ctx context.Context, slug string) (next time.Duration, stop, ran bool) {
	select {
	case s.slots <- struct{}{}:
	case <-ctx.Done():
		return 0, false, false
	}
	defer func() { <-s.slots }()

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cut := make(chan struct{})
	timer := s.cfg.Clock.After(cycleLimit)
	go func() {
		select {
		case <-timer:
			close(cut)
			cancel()
		case <-cctx.Done():
		}
	}()

	s.inflight.Add(1)
	next, stop = s.Cycle(cctx, slug)
	s.inflight.Add(-1)

	select {
	case <-cut:
		s.cfg.Logger.Warn("a cycle of the poll was cancelled after 5 minutes; what it did stands", "workspace", slug)
		return s.stateOf(slug).wait(), false, true
	default:
	}
	return next, stop, ctx.Err() == nil
}

// forget drops the loop of a workspace that the cycle found not to be subscribed, and what the poll remembers of it.
func (s *Supervisor) forget(slug string, l *loop) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loops[slug] == l {
		delete(s.loops, slug)
		delete(s.states, slug)
	}
}
