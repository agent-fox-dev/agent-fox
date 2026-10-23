package jobs_test

import (
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
)

// TestThePurgeReturnsNoErrorAndLeavesTheRowsOfThePollThatRefersToTheJob_TS_15_62 verifies TS-15-62: a finished job
// with a record of the label poll that refers to it, past the retention, is purged without an error, its rows in
// af_events, af_attempts and af_jobs go, and the record of the poll stays, since af_poll_requests has no foreign key
// to the job and the purge has no step for the poll.
// Verifies: 15-REQ-9.3
func TestThePurgeReturnsNoErrorAndLeavesTheRowsOfThePollThatRefersToTheJob_TS_15_62(t *testing.T) {
	clk := fakeclock.New(epoch)
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Done())), withClock(clk), withRetain(time.Hour))
	e.Start()
	job := e.createFromLabel("widgets", 42, "af:fix")
	e.waitRest(job.ID)
	if _, err := e.DB.ExecContext(ctx,
		`INSERT INTO af_poll_requests (workspace, issue, label, job_id, owed, seen, created_at) VALUES ('widgets', 42, 'af:fix', ?, 0, 'ended', ?)`,
		job.ID, epoch.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	clk.Advance(2 * time.Hour)
	// The purge of the hourly tick may have removed the job already; the call that the test makes finds it gone or
	// removes it, and either way returns no error.
	if _, err := e.Svc.Purge(ctx); err != nil {
		t.Fatalf("the purge: %v", err)
	}

	waitFor(t, "the rows of the job to be gone", func() bool { j, a, ev := e.rowsOf(job.ID); return j+a+ev == 0 })
	e.gone(job.ID)
	if got := e.count("SELECT COUNT(*) FROM af_poll_requests WHERE job_id = ?", job.ID); got != 1 {
		t.Errorf("%d rows of af_poll_requests refer to the purged job, want the one that was there", got)
	}
}
