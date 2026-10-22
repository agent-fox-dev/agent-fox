package jobs_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
)

// dirOf is the directory of the files of a job.
func (e *env) dirOf(id string) string { return filepath.Join(e.Dir, "attempts", id) }

// has reports whether the directory of the job is on disk.
func (e *env) has(id string) bool {
	_, err := os.Stat(e.dirOf(id))
	return err == nil
}

// rowsOf counts the rows of a job in af_jobs, af_attempts and af_events.
func (e *env) rowsOf(id string) (jobsN, attempts, events int) {
	e.t.Helper()
	return e.count("SELECT COUNT(*) FROM af_jobs WHERE id = ?", id),
		e.count("SELECT COUNT(*) FROM af_attempts WHERE job_id = ?", id),
		e.count("SELECT COUNT(*) FROM af_events WHERE job_id = ?", id)
}

// status is the status of GET /api/v1/jobs/:id.
func (e *env) status(id string) int { return e.call("GET", "/api/v1/jobs/"+id, nil).Status }

// gone checks that nothing of the job is left: not its directory, not a row, and the route
// answers the 404 of a job that is not there.
func (e *env) gone(id string) {
	e.t.Helper()
	if e.has(id) {
		e.t.Errorf("%s: the directory %s is still there", id, e.dirOf(id))
	}
	if j, a, ev := e.rowsOf(id); j+a+ev != 0 {
		e.t.Errorf("%s: %d rows in af_jobs, %d in af_attempts and %d in af_events, want none", id, j, a, ev)
	}
	if r := e.call("GET", "/api/v1/jobs/"+id, nil); r.Status != 404 {
		e.t.Errorf("%s: GET answers %d, want 404", id, r.Status)
	}
}

// kept checks that the job is whole: its directory, its rows, and the route reads it.
func (e *env) kept(id string) {
	e.t.Helper()
	if !e.has(id) {
		e.t.Errorf("%s: the directory is gone", id)
	}
	if j, a, ev := e.rowsOf(id); j != 1 || a == 0 || ev == 0 {
		e.t.Errorf("%s: %d rows in af_jobs, %d in af_attempts and %d in af_events, want the job whole", id, j, a, ev)
	}
	if st := e.status(id); st != 200 {
		e.t.Errorf("%s: GET answers %d, want 200", id, st)
	}
}

// TestThePurgeRemovesExpiredFinishedJobsOnTheHourlyTickAndLeavesTheYoungOnes_TS_12_5
// verifies TS-12-5: with a retention of 1 h and an injected clock, a done, a failed, a job
// cancelled while it was queued and one cancelled while it ran, all finished more than an hour
// ago, lose their attempts/<job id>/ directory and their rows in af_events, af_attempts and
// af_jobs when the clock passes the hourly tick, and GET /jobs/:id answers 404 for each; a job
// finished 31 minutes before the tick is untouched.
// Verifies: 12-REQ-1.5
func TestThePurgeRemovesExpiredFinishedJobsOnTheHourlyTickAndLeavesTheYoungOnes_TS_12_5(t *testing.T) {
	clk := fakeclock.New(epoch)
	r := fakerunner.New(
		fakerunner.Done(),                   // done
		fakerunner.Failed("sandbox", false), // failed
		fakerunner.Block(fakerunner.Step{}), // cancelled while it runs
		fakerunner.Done(),                   // the young one
	)
	e := newEnv(t, withRunner(r), withClock(clk), withRetain(time.Hour), withWorkers(1))
	done := e.create("widgets", jobs.KindFix, 1)
	failed := e.create("widgets", jobs.KindFix, 2)
	running := e.create("widgets", jobs.KindFix, 4)
	e.Start()
	e.waitState(done.ID, jobs.StateDone)
	e.waitState(failed.ID, jobs.StateFailed)
	waitCalls(t, r, 3)
	e.waitState(running.ID, jobs.StateRunning)
	queued := e.create("widgets", jobs.KindFix, 3) // behind the running one, so it never runs
	for _, id := range []string{queued.ID, running.ID} {
		if resp := e.call("POST", "/api/v1/jobs/"+id+"/cancel", nil); resp.Status != 200 {
			t.Fatalf("cancel %s: %d %s", id, resp.Status, resp.Body)
		}
	}
	e.waitRest(running.ID)
	expired := []string{done.ID, failed.ID, queued.ID, running.ID}

	clk.Advance(30 * time.Minute)
	young := e.create("widgets", jobs.KindFix, 5)
	e.waitRest(young.ID)
	for _, id := range append(expired[:2:2], running.ID, young.ID) {
		e.kept(id)
	}
	if e.has(queued.ID) {
		t.Fatal("the job that was cancelled while queued has a directory, so it ran")
	}

	// 61 minutes after the first four were finished, 31 after the last; the tick is at 60.
	clk.Advance(31 * time.Minute)
	waitFor(t, "the purge to remove the expired jobs", func() bool {
		for _, id := range expired {
			if e.status(id) != 404 {
				return false
			}
		}
		return true
	})
	for _, id := range expired {
		e.gone(id)
	}
	e.kept(young.ID)
	if body := e.call("GET", "/api/v1/jobs/"+done.ID, nil).Body; string(body) != string(e.call("GET", "/api/v1/jobs/job_00000000000000000000", nil).Body) {
		t.Errorf("a purged job is answered %s, want the 404 of a job that never was", body)
	}
}

// TestThePurgeRunsWhenTheServiceStarts_TS_12_5 verifies the other half of TS-12-5: a service
// that starts on a database that holds a job finished longer ago than the retention removes
// it, without waiting for the tick.
// Verifies: 12-REQ-1.5
func TestThePurgeRunsWhenTheServiceStarts_TS_12_5(t *testing.T) {
	clk := fakeclock.New(epoch)
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Done(), fakerunner.Done())), withClock(clk), withRetain(time.Hour))
	old := e.create("widgets", jobs.KindFix, 1)
	e.Start()
	e.waitRest(old.ID)
	clk.Advance(30 * time.Minute)
	recent := e.create("widgets", jobs.KindFix, 2)
	e.waitRest(recent.ID)
	if err := e.Svc.Close(); err != nil {
		t.Fatal(err)
	}

	clk.Advance(31 * time.Minute) // the service is closed: nothing ticks
	e.kept(old.ID)
	svc := e.newService(fakerunner.New(), clk, withRetain(time.Hour))
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the purge at start", func() bool { return e.status(old.ID) == 404 })
	e.gone(old.ID)
	e.kept(recent.ID)
}

// TestThePurgeKeepsBlockedRunningAndInProgressJobsAndCancelsYoungerThanTheRetention_TS_12_6
// verifies TS-12-6: past the retention counted from the question, a blocked job, a running
// job and a job whose cancel recorded its attempt cancelled while the runner has not returned are
// all kept, whole; a job cancelled while it was blocked is kept until the retention has passed
// since the cancel, though its question is older still, and is then removed; and the job whose
// runner is still in progress is removed once the runner has returned.
// Verifies: 12-REQ-1.6
func TestThePurgeKeepsBlockedRunningAndInProgressJobsAndCancelsYoungerThanTheRetention_TS_12_6(t *testing.T) {
	clk := fakeclock.New(epoch)
	r := fakerunner.New(
		question(), // blocked
		question(), // blocked, and cancelled later
		fakerunner.BlockIgnoringContext(fakerunner.Step{}), // cancelled, and the runner does not return
		fakerunner.Block(fakerunner.Step{}),                // running, on another workspace
	)
	e := newEnv(t, withRunner(r), withClock(clk), withRetain(time.Hour))
	e.addWorkspace("gadgets")
	blocked := e.create("widgets", jobs.KindFix, 1)
	cancelledWhileBlocked := e.create("widgets", jobs.KindFix, 2)
	overran := e.create("widgets", jobs.KindFix, 3)
	e.Start()
	e.waitState(blocked.ID, jobs.StateBlocked)
	e.waitState(cancelledWhileBlocked.ID, jobs.StateBlocked)
	waitCalls(t, r, 3)
	e.waitState(overran.ID, jobs.StateRunning)
	running := e.create("gadgets", jobs.KindFix, 1)
	waitCalls(t, r, 4)
	e.waitState(running.ID, jobs.StateRunning)

	// The cancel gives the runner 60 s; it does not return, and the attempt is recorded cancelled.
	if resp := e.call("POST", "/api/v1/jobs/"+overran.ID+"/cancel", nil); resp.Status != 200 {
		t.Fatalf("cancel: %d %s", resp.Status, resp.Body)
	}
	if !clk.WaitForWaits(1, 10*time.Second) {
		t.Fatal("the cancel started no grace")
	}
	clk.Advance(61 * time.Second)
	waitFor(t, "the attempt to be recorded cancelled", func() bool {
		return e.get(overran.ID).Attempts[0].State == jobs.AttemptCancelled
	})
	if r.Active() != 2 {
		t.Fatalf("%d runners in progress, want the two that were not returned", r.Active())
	}

	purge := func(when string) {
		t.Helper()
		if _, err := e.Svc.Purge(ctx); err != nil {
			t.Fatalf("purge %s: %v", when, err)
		}
	}
	// An hour and a minute after the question was asked.
	clk.Advance(time.Hour)
	purge("past the retention from the question")
	for name, id := range map[string]string{"blocked": blocked.ID, "cancelled while blocked": cancelledWhileBlocked.ID, "overran": overran.ID, "running": running.ID} {
		if e.status(id) != 200 || !e.has(id) {
			t.Errorf("%s job: status %d, directory %v, want it kept", name, e.status(id), e.has(id))
		}
	}

	// Cancelled while it is blocked, now: the question is an hour old, the cancel is new.
	if resp := e.call("POST", "/api/v1/jobs/"+cancelledWhileBlocked.ID+"/cancel", nil); resp.Status != 200 {
		t.Fatalf("cancel: %d %s", resp.Status, resp.Body)
	}
	purge("at the cancel")
	e.kept(cancelledWhileBlocked.ID)
	clk.Advance(59 * time.Minute)
	purge("59 minutes after the cancel")
	e.kept(cancelledWhileBlocked.ID)
	clk.Advance(2 * time.Minute)
	purge("61 minutes after the cancel")
	e.gone(cancelledWhileBlocked.ID)
	for name, id := range map[string]string{"blocked": blocked.ID, "overran": overran.ID, "running": running.ID} {
		if e.status(id) != 200 || !e.has(id) {
			t.Errorf("%s job: status %d, directory %v, want it kept", name, e.status(id), e.has(id))
		}
	}

	// The runner of the cancelled job returns at last: nothing is in progress, and the job goes.
	r.Release(2)
	waitFor(t, "the job whose runner returned to be purged", func() bool {
		purge("after the runner returned")
		return e.status(overran.ID) == 404
	})
	e.gone(overran.ID)
	e.kept(blocked.ID)
	e.kept(running.ID)
}

// TestAPurgeThatCannotRemoveTheDirectoryKeepsTheRowsLogsAWarningAndTriesAgain_TS_12_7
// verifies TS-12-7: with a remover that fails, an expired job keeps its directory and all its
// rows and is readable, and a WARN names it; when the remover works the next purge removes it.
// Verifies: 12-REQ-1.7
func TestAPurgeThatCannotRemoveTheDirectoryKeepsTheRowsLogsAWarningAndTriesAgain_TS_12_7(t *testing.T) {
	clk := fakeclock.New(epoch)
	var failing atomic.Bool
	var removals atomic.Int32
	remove := func(path string) error {
		removals.Add(1)
		if failing.Load() {
			return errors.New("device or resource busy")
		}
		return os.RemoveAll(path)
	}
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Failed("sandbox", false))), withClock(clk), withRetain(10*time.Minute), withRemover(remove))
	job := e.create("widgets", jobs.KindFix, 1)
	e.Start()
	e.waitState(job.ID, jobs.StateFailed)
	before, _, _ := e.rowsOf(job.ID)
	_, attempts, events := e.rowsOf(job.ID)

	clk.Advance(11 * time.Minute)
	failing.Store(true)
	if n, err := e.Svc.Purge(ctx); n != 0 || err != nil {
		t.Fatalf("a purge that could not remove a directory = %d, %v, want 0 jobs removed and no error", n, err)
	}
	e.kept(job.ID)
	if j, a, ev := e.rowsOf(job.ID); j != before || a != attempts || ev != events {
		t.Errorf("rows after the failed purge = %d, %d, %d, want %d, %d, %d: none removed", j, a, ev, before, attempts, events)
	}
	warned := e.Logs.find("WARN", "directory")
	if len(warned) != 1 || warned[0]["job"] != job.ID {
		t.Fatalf("warnings = %v, want one that names the job %s", e.Logs.records(), job.ID)
	}
	if removals.Load() != 1 || len(e.Logs.find("ERROR", "")) != 0 {
		t.Errorf("%d removals tried and %d errors logged, want 1 and none", removals.Load(), len(e.Logs.find("ERROR", "")))
	}

	// It is tried again at the next purge, and not given up on.
	if n, _ := e.Svc.Purge(ctx); n != 0 || removals.Load() != 2 {
		t.Errorf("the second failing purge removed %d jobs and tried %d times in all, want 0 and 2", n, removals.Load())
	}
	failing.Store(false)
	if n, err := e.Svc.Purge(ctx); n != 1 || err != nil {
		t.Fatalf("the purge once the directory can be removed = %d, %v, want 1 job removed", n, err)
	}
	e.gone(job.ID)
}

// TestThePurgeKeepsAFinishedJobWhoseAttemptStillReadsRunning_TS_12_6 verifies 12-REQ-1.6 for
// the state a crash leaves: a job that is cancelled and old, whose attempt row still says
// running because the process ended before the runner returned, is not purged, and is when the
// attempt is recorded.
// Verifies: 12-REQ-1.6
func TestThePurgeKeepsAFinishedJobWhoseAttemptStillReadsRunning_TS_12_6(t *testing.T) {
	clk := fakeclock.New(epoch)
	// A retention of 10 minutes and a clock that moves 11, so that the timer of the service, which
	// fires every hour, is not what purges.
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Done())), withClock(clk), withRetain(10*time.Minute))
	job := e.create("widgets", jobs.KindFix, 1)
	e.Start()
	e.waitRest(job.ID)
	if _, err := e.DB.ExecContext(ctx, "UPDATE af_attempts SET state = 'running' WHERE job_id = ?", job.ID); err != nil {
		t.Fatal(err)
	}
	clk.Advance(11 * time.Minute)
	if n, err := e.Svc.Purge(ctx); n != 0 || err != nil {
		t.Fatalf("purge = %d, %v, want nothing removed", n, err)
	}
	e.kept(job.ID)
	if _, err := e.DB.ExecContext(ctx, "UPDATE af_attempts SET state = 'done' WHERE job_id = ?", job.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := e.Svc.Purge(ctx); n != 1 || err != nil {
		t.Fatalf("purge = %d, %v, want the job removed", n, err)
	}
	e.gone(job.ID)
}

// TestThePurgeGoesThroughMoreJobsThanOneReadHoldsAndPassesOverTheOnesItCannotRemove_TS_12_5
// verifies 12-REQ-1.5 and 12-REQ-1.7 for many jobs: 450 expired jobs, the oldest 210 of which
// have a directory that cannot be removed; one purge removes the other 240 and keeps the 210,
// and does not stop at them or go round them for ever.
// Verifies: 12-REQ-1.5, 12-REQ-1.7
func TestThePurgeGoesThroughMoreJobsThanOneReadHoldsAndPassesOverTheOnesItCannotRemove_TS_12_5(t *testing.T) {
	clk := fakeclock.New(epoch)
	stuck := map[string]bool{}
	remove := func(path string) error {
		if stuck[filepath.Base(path)] {
			return errors.New("device or resource busy")
		}
		return os.RemoveAll(path)
	}
	e := newEnv(t, withClock(clk), withRetain(time.Hour), withRemover(remove))
	var ids []string
	for i := range 450 {
		id := fmt.Sprintf("job_%020x", i)
		finished := epoch.Add(time.Duration(i) * time.Second).UTC().Format("2006-01-02T15:04:05.000Z")
		if _, err := e.DB.ExecContext(ctx,
			`INSERT INTO af_jobs (id, workspace, kind, input, context, issue, source_kind, source_by, state, created_at, finished_at)
			 VALUES (?, 'widgets', 'fix', '{"issue":1}', '[]', NULL, 'api', 'seed', ?, ?, ?)`,
			id, []string{"done", "failed", "cancelled"}[i%3], finished, finished); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(e.dirOf(id), 0o700); err != nil {
			t.Fatal(err)
		}
		if i < 210 {
			stuck[id] = true
		}
		ids = append(ids, id)
	}

	clk.Advance(3 * time.Hour)
	n, err := e.Svc.Purge(ctx)
	if err != nil || n != 240 {
		t.Fatalf("purge = %d, %v, want 240 removed", n, err)
	}
	for i, id := range ids {
		left := e.count("SELECT COUNT(*) FROM af_jobs WHERE id = ?", id)
		if want := map[bool]int{true: 1, false: 0}[i < 210]; left != want || e.has(id) != (i < 210) {
			t.Fatalf("job %d: %d rows, directory %v, want %d rows and a directory only for the first 210", i, left, e.has(id), want)
		}
	}
	if warned := len(e.Logs.find("WARN", "directory")); warned != 210 {
		t.Errorf("%d warnings, want one for each of the 210 directories that could not be removed", warned)
	}
}

// TestWithoutARetentionInTheConfigurationAFinishedJobIsKeptFor720Hours_TS_12_5 verifies the
// default: a service that is not told [database].retain keeps a finished job for 720 h and
// removes it after.
// Verifies: 12-REQ-1.5
func TestWithoutARetentionInTheConfigurationAFinishedJobIsKeptFor720Hours_TS_12_5(t *testing.T) {
	clk := fakeclock.New(epoch)
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Done())), withClock(clk), withRetain(0)) // 0: the default
	job := e.create("widgets", jobs.KindFix, 1)
	e.Start()
	e.waitRest(job.ID)
	clk.Advance(719 * time.Hour)
	if n, _ := e.Svc.Purge(ctx); n != 0 {
		t.Fatalf("a job finished 719 h ago was purged")
	}
	e.kept(job.ID)
	// The hourly timer of the service fires on this advance too, and may purge before the call does.
	clk.Advance(2 * time.Hour)
	if _, err := e.Svc.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a job finished 721 h ago to be purged", func() bool { return e.status(job.ID) == 404 })
	e.gone(job.ID)
}

// TestPurgingOneJobLooksAtItAgainAndTakesOnlyAJobFinishedBeforeTheCutoff_TS_12_6 verifies
// 12-REQ-1.6 at the store: whatever a list of expired jobs said, a job that is blocked, running
// or queued is not removed, nor is a job that was finished at the cutoff or after it, and then
// the directory is not touched either; a job finished before the cutoff is removed with its
// directory.
// Verifies: 12-REQ-1.6
func TestPurgingOneJobLooksAtItAgainAndTakesOnlyAJobFinishedBeforeTheCutoff_TS_12_6(t *testing.T) {
	const layout = "2006-01-02T15:04:05.000Z"
	clk := fakeclock.New(epoch)
	r := fakerunner.New(question(), fakerunner.Block(fakerunner.Step{}))
	e := newEnv(t, withRunner(r), withClock(clk), withWorkers(1))
	blocked := e.create("widgets", jobs.KindFix, 1)
	running := e.create("widgets", jobs.KindFix, 2)
	e.Start()
	e.waitState(blocked.ID, jobs.StateBlocked)
	e.waitState(running.ID, jobs.StateRunning)
	queued := e.create("widgets", jobs.KindFix, 3) // behind the running one

	store := jobs.NewStore(e.DB, clk.Now)
	forever := epoch.Add(24 * time.Hour).UTC().Format(layout)
	var removed []string
	remove := func(id string) func() error {
		return func() error { removed = append(removed, id); return nil }
	}
	for name, job := range map[string]jobs.Doc{"blocked": blocked, "running": running, "queued": queued} {
		if ok, err := store.Purge(ctx, job.ID, forever, remove(job.ID)); ok || err != nil {
			t.Errorf("%s job: purged = %v, %v, want it left alone", name, ok, err)
		}
		// Not even one that has a finished_at it should not have: it is the state that says whether
		// a job is open, and a time left over from before must not make a blocked job go.
		if _, err := e.DB.ExecContext(ctx, "UPDATE af_jobs SET finished_at = ? WHERE id = ?", epoch.UTC().Format(layout), job.ID); err != nil {
			t.Fatal(err)
		}
		if ok, err := store.Purge(ctx, job.ID, forever, remove(job.ID)); ok || err != nil {
			t.Errorf("%s job with a stale finished_at: purged = %v, %v, want it left alone", name, ok, err)
		}
		if _, err := e.DB.ExecContext(ctx, "UPDATE af_jobs SET finished_at = NULL WHERE id = ?", job.ID); err != nil {
			t.Fatal(err)
		}
	}

	clk.Advance(time.Minute)
	finished := epoch.Add(time.Minute)
	if resp := e.call("POST", "/api/v1/jobs/"+running.ID+"/cancel", nil); resp.Status != 200 {
		t.Fatalf("cancel: %d %s", resp.Status, resp.Body)
	}
	e.waitRest(running.ID)
	if ok, err := store.Purge(ctx, running.ID, finished.UTC().Format(layout), remove(running.ID)); ok || err != nil {
		t.Errorf("a job finished at the cutoff: purged = %v, %v, want it left alone: only older than the cutoff expires", ok, err)
	}
	if len(removed) != 0 {
		t.Fatalf("the directories of %v were removed, want none", removed)
	}
	if ok, err := store.Purge(ctx, running.ID, finished.Add(time.Millisecond).UTC().Format(layout), remove(running.ID)); !ok || err != nil || !slices.Equal(removed, []string{running.ID}) {
		t.Errorf("a job finished before the cutoff: purged = %v, %v, directories removed %v, want it removed", ok, err, removed)
	}
}
