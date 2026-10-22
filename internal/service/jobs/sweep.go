package jobs

import (
	"context"
	"fmt"
	"time"
)

// sweepEvery is how often the attempts that are queued are put in the queue again.
const sweepEvery = time.Minute

// QueuedAttempts are the attempts that are queued, of jobs that are open, in the
// order they were made.
func (s *Store) QueuedAttempts(ctx context.Context) ([]attemptKey, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.job_id, a.n FROM af_attempts a JOIN af_jobs j ON j.id = a.job_id
		 WHERE a.state = 'queued' AND j.state IN ('queued', 'running') ORDER BY a.queue_seq`)
	if err != nil {
		return nil, fmt.Errorf("queued attempts: %w", err)
	}
	defer rows.Close()
	var out []attemptKey
	for rows.Next() {
		var k attemptKey
		if err := rows.Scan(&k.job, &k.attempt); err != nil {
			return nil, fmt.Errorf("queued attempts: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// sweep puts every attempt that is queued in the queue again. The queue ignores an
// attempt it holds already, so this repairs the two cases that lose work: a crash
// between storing an attempt and queueing it, and a queueing that failed.
func (s *Service) sweep(ctx context.Context) {
	queued, err := s.store.QueuedAttempts(ctx)
	if err != nil {
		s.log.Warn("the attempts that are queued could not be listed", "error", err.Error())
		return
	}
	for _, k := range queued {
		if err := s.Enqueue(ctx, k.job, k.attempt); err != nil {
			s.log.Warn("an attempt could not be put in the queue by the sweep; the next sweep will try again",
				"job", k.job, "attempt", k.attempt, "error", err.Error())
		}
	}
}

// sweepLoop sweeps on every tick until the service stops.
func (s *Service) sweepLoop(ticker Ticker) {
	defer s.loops.Done()
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C():
			s.sweep(context.Background())
		case <-s.stopping:
			return
		}
	}
}
