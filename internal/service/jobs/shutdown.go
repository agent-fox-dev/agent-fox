package jobs

import "time"

// shutdownWait is how long the runners are given to return when the service stops.
const shutdownWait = 30 * time.Second

// queueGrace is how long the queue waits for its handlers once the runners have
// returned or the wait for them is over.
const queueGrace = 200 * time.Millisecond

// Close stops the service: it stops claiming, cancels the attempts that are running
// with the shutdown cause (which a runner does not read as a cancel), and waits up to
// 30 seconds for their runners to return. What a runner returns after the cause is
// discarded: nothing is stored, and the attempt stays running, to be handed over again
// after the restart. Close can be called again.
func (s *Service) Close() error {
	s.stopOnce.Do(func() { close(s.stopping) })
	s.mu.Lock()
	runs := make([]*attemptRun, 0, len(s.inflight))
	for _, run := range s.inflight {
		runs = append(runs, run)
	}
	started := s.started
	s.mu.Unlock()
	for _, run := range runs {
		run.cancel(ErrShutdown)
	}
	if !started {
		return nil // there are no workers to wait for
	}

	if len(runs) > 0 {
		deadline := s.clock.After(shutdownWait)
	wait:
		for _, run := range runs {
			select {
			case <-run.returned:
			case <-deadline:
				s.log.Warn("a runner did not return within the time it was given at shutdown",
					"job", run.key.job, "attempt", run.key.attempt)
				break wait
			}
		}
	}
	s.loops.Wait()
	s.queue.Shutdown()
	return s.queue.Wait()
}
