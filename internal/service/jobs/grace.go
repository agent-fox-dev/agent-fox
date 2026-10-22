package jobs

import (
	"context"
	"path/filepath"
	"strconv"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// cancelGrace is how long the runner of a cancelled attempt has to return: the 30
// seconds that a fix run has to put its tree back and park, and 30 more for the worker
// to release the lease and put the label back through hub.
const cancelGrace = 60 * time.Second

// startGrace starts the timer of the grace for an attempt that was told to stop, once.
// The wait is asked for before this returns.
func (s *Service) startGrace(run *attemptRun) {
	run.graceOnce.Do(func() {
		deadline := s.clock.After(cancelGrace)
		go func() {
			select {
			case <-run.returned:
			case <-deadline:
				s.overrun(run)
			case <-s.stopping:
			}
		}()
	})
}

// overrun records an attempt whose runner did not return within the grace as cancelled,
// with an envelope of the category cancelled, and says so in the log of the job. The
// runner keeps its workspace's place and its slot until it returns, because its handler
// is still in the call; what it sends and returns from now on is dropped.
func (s *Service) overrun(run *attemptRun) {
	run.overran.Store(true)
	run.recorder.close()
	ended := s.settleCancelled(context.Background(), run.key, "the runner did not return within "+cancelGrace.String()+" of the cancel")
	if ended {
		s.log.Error("a runner did not return within the cancel grace; its attempt is recorded cancelled, and it keeps its place until it returns",
			"job", run.key.job, "attempt", run.key.attempt, "grace", cancelGrace.String())
	}
	run.settle()
}

// settleCancelled ends a running attempt of a job that is cancelled as cancelled, with an
// envelope of the category cancelled that the layer makes. It says whether it did; an
// attempt that is not running is left as it is.
func (s *Service) settleCancelled(ctx context.Context, key attemptKey, message string) bool {
	job, err := loadJob(ctx, s.store.db, key.job)
	if err != nil {
		s.log.Warn("the job of a cancelled attempt could not be read", "job", key.job, "attempt", key.attempt, "error", err.Error())
		return false
	}
	env := synthesize(Kind(job.Kind), s.clock.Now(), toolio.ErrorInfo{Category: toolio.CategoryCancelled, Message: message})
	raw, err := encodeEnvelope(env)
	if err != nil {
		return false
	}
	dir := filepath.Join(s.attemptsDir, key.job, strconv.Itoa(key.attempt))
	files := func() error {
		if err := makeDir(dir); err != nil {
			return err
		}
		return writePrivate(filepath.Join(dir, "envelope.json"), raw)
	}
	ended, _, err := s.store.EndAttempt(ctx, endArgs{jobID: key.job, attempt: key.attempt, verdict: verdict{attempt: AttemptCancelled, job: StateCancelled}, env: env, maxAttempts: defaultRound}, files)
	if err != nil {
		s.log.Warn("a cancelled attempt could not be recorded", "job", key.job, "attempt", key.attempt, "error", err.Error())
		return false
	}
	return ended
}
