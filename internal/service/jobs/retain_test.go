package jobs_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// whole says whether the job has its directory and all its rows.
func (e *env) whole(id string) bool {
	_, err := os.Stat(filepath.Join(e.Dir, id))
	return err == nil && testutil.Count(e.t, e.DB, "af_jobs", "id = ?", id) == 1 &&
		testutil.Count(e.t, e.DB, "af_attempts", "job_id = ?", id) > 0 && testutil.Count(e.t, e.DB, "af_events", "job_id = ?", id) > 0
}

// gone says whether nothing of the job is left: not its directory, not a row, and it is not found.
func (e *env) gone(id string) bool {
	_, statErr := os.Stat(filepath.Join(e.Dir, id))
	_, getErr := e.Svc.Get(ctx, id)
	return errors.Is(statErr, os.ErrNotExist) && errors.Is(getErr, jobs.ErrNotFound) &&
		testutil.Count(e.t, e.DB, "af_attempts", "job_id = ?", id)+testutil.Count(e.t, e.DB, "af_events", "job_id = ?", id) == 0
}

// purge purges now, and fails the test on an error.
func (e *env) purge() int {
	e.t.Helper()
	n, err := e.Svc.Purge(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestPurge_RemovesTheJobsFinishedLongerAgoThanTheRetentionOnTheHourlyTick(t *testing.T) {
	r := fakerunner.New(fakerunner.Done(), fakerunner.Failed("sandbox", false), fakerunner.Block(fakerunner.Step{}), fakerunner.Done())
	e := newEnv(t, withRunner(r), withRetain(time.Hour), withWorkers(1))
	done, failed, running := e.create("widgets", 1, ""), e.create("widgets", 2, ""), e.create("widgets", 3, "")
	e.start()
	e.waitState(failed.ID, jobs.StateFailed)
	e.waitCalls(3)
	queued := e.create("widgets", 4, "") // behind the running one, so it never runs
	for _, id := range []string{queued.ID, running.ID} {
		if _, err := e.Svc.Cancel(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	e.waitRest(running.ID)
	// The label poll's record of a job does not keep it.
	testutil.Exec(t, e.DB, `INSERT INTO af_poll_requests (workspace, issue, label, job_id, owed, seen, created_at) VALUES ('widgets', 1, 'af:fix', ?, 0, 'ended', ?)`, done.ID, stamp(epoch))
	e.Clock.Advance(30 * time.Minute)
	young := e.waitRest(e.create("widgets", 5, "").ID)
	e.Clock.Advance(31 * time.Minute) // the tick, 61 minutes after the four were finished and 31 after the young one
	for _, id := range []string{done.ID, failed.ID, queued.ID, running.ID} {
		testutil.Eventually(t, id+" to be purged", func() bool { return e.gone(id) })
	}
	if !e.whole(young.ID) {
		t.Error("the job finished 31 minutes ago is not whole")
	}
	if n := testutil.Count(t, e.DB, "af_poll_requests", "job_id = ?", done.ID); n != 1 {
		t.Errorf("%d records of the poll, want the one that refers to the purged job kept", n)
	}
	testutil.Eventually(t, "the purge of the tick to be logged", func() bool { return len(e.Log.Find("INFO", "expired jobs were removed")) > 0 })
}

func TestPurge_RunsWhenTheServiceStartsAndKeepsAJob720HoursByDefault(t *testing.T) {
	e := newEnv(t, withRetain(0))
	old := e.create("widgets", 1, "")
	e.start()
	e.waitRest(old.ID)
	e.Clock.Advance(719 * time.Hour)
	if n := e.purge(); n != 0 || !e.whole(old.ID) {
		t.Fatalf("a job finished 719 h ago was purged")
	}
	e.close()
	e.Clock.Advance(2 * time.Hour)
	e.restart(fakerunner.New())
	testutil.Eventually(t, "the purge at the start", func() bool { return e.gone(old.ID) })
}

func TestPurge_KeepsWhatIsOpenWhatARunnerHoldsAndAPendingCleanup(t *testing.T) {
	returned := make(chan struct{})
	r := fakerunner.New().Then(perIssue(map[int]fakerunner.Step{
		1: question(), 2: question(), 5: question(),
		3: fakerunner.Do(func(context.Context, jobs.Request) (jobs.Result, error) { <-returned; return jobs.Result{}, nil }),
		4: fakerunner.Block(fakerunner.Step{}),
	}))
	e := newEnv(t, withRunner(r), withRetain(time.Hour), withWorkers(5))
	e.openAtEnd(returned)
	e.Subs.set("cogs", "ok")
	e.Subs.set("sprockets", "ok")
	blocked, cancelledLater := e.create("widgets", 1, ""), e.create("sprockets", 2, "")
	overran, running := e.create("gadgets", 3, ""), e.create("cogs", 4, "")
	pending := e.create("widgets", 5, "af:fix")
	e.start()
	e.waitState(blocked.ID, jobs.StateBlocked)
	e.waitState(cancelledLater.ID, jobs.StateBlocked)
	e.waitCall(overran.ID)
	e.waitCall(running.ID)
	if _, err := e.Svc.Cancel(ctx, overran.ID); err != nil {
		t.Fatal(err)
	}
	e.waitWaits(1)
	e.Clock.Advance(61 * time.Second) // the attempt is recorded cancelled; its runner has not returned
	testutil.Eventually(t, "the overrun", func() bool { return e.get(overran.ID).Attempts[0].State == jobs.AttemptCancelled })
	e.waitState(pending.ID, jobs.StateBlocked)
	if _, err := e.Svc.Cancel(ctx, pending.ID); err != nil {
		t.Fatal(err)
	}
	testutil.Exec(t, e.DB, "UPDATE af_jobs SET cleanup = 'pending' WHERE id = ?", pending.ID)
	e.Clock.Advance(time.Hour)
	e.purge()
	for _, d := range []jobs.Doc{blocked, cancelledLater, overran, running, pending} {
		if !e.whole(d.ID) {
			t.Errorf("%s is not whole an hour on", d.ID)
		}
	}
	// A job cancelled while blocked counts from the cancel, not from its question.
	if _, err := e.Svc.Cancel(ctx, cancelledLater.ID); err != nil {
		t.Fatal(err)
	}
	e.Clock.Advance(59 * time.Minute)
	if e.purge(); !e.whole(cancelledLater.ID) {
		t.Error("a job cancelled 59 minutes ago was purged")
	}
	e.Clock.Advance(2 * time.Minute)
	e.purge()
	if !e.gone(cancelledLater.ID) || !e.whole(overran.ID) || !e.whole(pending.ID) {
		t.Error("the job cancelled 61 minutes ago is kept, or a job held by its runner or its cleanup is not")
	}
	close(returned) // the runner of the overrun returns at last
	testutil.Eventually(t, "the purge of the overrun", func() bool { e.purge(); return e.gone(overran.ID) })
	if !e.whole(blocked.ID) || !e.whole(running.ID) {
		t.Error("a blocked or a running job was purged")
	}
}

func TestPurge_ADirectoryThatCannotBeRemovedKeepsTheRowsAndIsTriedAgain(t *testing.T) {
	failing := true
	removals := 0
	e := newEnv(t, withRetain(10*time.Minute), withRemover(func(path string) error {
		removals++
		if failing {
			return errors.New("device or resource busy")
		}
		return os.RemoveAll(path)
	}))
	job := e.create("widgets", 1, "")
	e.start()
	e.waitRest(job.ID)
	e.Clock.Advance(11 * time.Minute)
	for range 2 {
		if n := e.purge(); n != 0 || !e.whole(job.ID) {
			t.Fatalf("a purge whose removal failed removed %d jobs", n)
		}
	}
	if warned := e.Log.Find("WARN", "could not be removed"); len(warned) != 2 || warned[0]["job"] != job.ID || removals != 2 {
		t.Errorf("warnings %v after %d removals, want one naming the job for each of the two", warned, removals)
	}
	failing = false
	if n := e.purge(); n != 1 || !e.gone(job.ID) {
		t.Errorf("the purge once the directory can be removed removed %d jobs", n)
	}
}

func TestPurge_GoesThroughMoreJobsThanOneReadHoldsPastTheOnesItCannotRemove(t *testing.T) {
	stuck := map[string]bool{}
	e := newEnv(t, withRetain(time.Hour), withRemover(func(path string) error {
		if stuck[filepath.Base(path)] {
			return errors.New("busy")
		}
		return os.RemoveAll(path)
	}))
	for i := range 450 {
		id := fmt.Sprintf("job_%020x", i)
		finished := stamp(epoch.Add(time.Duration(i) * time.Second))
		testutil.Exec(t, e.DB, `INSERT INTO af_jobs (id, workspace, kind, input, context, source_kind, source_by, state, created_at, finished_at)
			VALUES (?, 'widgets', 'fix', '{"issue":1}', '[]', 'api', 'seed', ?, ?, ?)`, id, []string{"done", "failed", "cancelled"}[i%3], finished, finished)
		stuck[id] = i < 210
	}
	e.Clock.Advance(3 * time.Hour)
	if n := e.purge(); n != 240 || testutil.Count(t, e.DB, "af_jobs", "") != 210 || len(e.Log.Find("WARN", "could not be removed")) != 210 {
		t.Errorf("the purge removed %d of 450 jobs and kept %d, want 240 removed and the 210 stuck kept with a warning each", n, testutil.Count(t, e.DB, "af_jobs", ""))
	}
}

func TestStorePurge_LooksAtTheJobAgainAndTakesOnlyOneFinishedBeforeTheCutoff(t *testing.T) {
	e := newEnv(t, withoutEnqueue())
	removed := 0
	remove := func() error { removed++; return nil }
	later := stamp(epoch.Add(24 * time.Hour))
	for i, state := range []jobs.State{jobs.StateQueued, jobs.StateRunning, jobs.StateBlocked} {
		// A time left over from before does not make an open job go: its state says it is open.
		id := e.create("widgets", 10+i, "").ID
		testutil.Exec(t, e.DB, "UPDATE af_jobs SET state = ?, finished_at = ? WHERE id = ?", string(state), stamp(epoch), id)
		if ok, err := e.Store.Purge(ctx, id, later, remove); ok || err != nil {
			t.Errorf("a %s job: purged %v, %v", state, ok, err)
		}
	}
	done := e.create("widgets", 1, "").ID
	testutil.Exec(t, e.DB, "UPDATE af_jobs SET state = 'done', finished_at = ? WHERE id = ?", stamp(epoch), done)
	if ok, _ := e.Store.Purge(ctx, done, stamp(epoch), remove); ok || removed != 0 {
		t.Errorf("a job finished at the cutoff was purged, or a directory was removed (%d)", removed)
	}
	if ok, err := e.Store.Purge(ctx, done, stamp(epoch.Add(time.Millisecond)), remove); !ok || err != nil || removed != 1 {
		t.Errorf("a job finished before the cutoff: purged %v, %v, %d removals", ok, err, removed)
	}
	var dirErr *jobs.DirectoryError
	other := e.create("widgets", 2, "").ID
	testutil.Exec(t, e.DB, "UPDATE af_jobs SET state = 'done', finished_at = ? WHERE id = ?", stamp(epoch), other)
	busy := errors.New("busy")
	if _, err := e.Store.Purge(ctx, other, later, func() error { return busy }); !errors.As(err, &dirErr) || dirErr.JobID != other || !errors.Is(err, busy) || err.Error() != "the attempts directory of "+other+": busy" {
		t.Errorf("a removal that fails: %v, want a *DirectoryError naming the job", err)
	}
}
