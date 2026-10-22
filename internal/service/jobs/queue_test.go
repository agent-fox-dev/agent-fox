package jobs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// queueItem is a row of the jobs table of aflib/jobqueue.
type queueItem struct {
	Type, Key, Group, Nonce, Status, Payload string
}

// items are the rows of the queue's table, in the order they were made.
func (e *env) items() []queueItem {
	e.t.Helper()
	rows, err := e.DB.Query("SELECT type, key, group_key, nonce, status, payload FROM jobs ORDER BY rowid")
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []queueItem
	for rows.Next() {
		var it queueItem
		if err := rows.Scan(&it.Type, &it.Key, &it.Group, &it.Nonce, &it.Status, &it.Payload); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, it)
	}
	return out
}

// TestStartingTheServiceCreatesTheTablesAndTheAttemptsDirectory_TS_12_3 verifies
// TS-12-3: with the store opened and the service started, af_jobs, af_attempts and
// af_events are there beside the tables of aflib/jobqueue, and attempts/ beside the
// database has the mode 0700; a second start leaves the directory as it is.
// Verifies: 12-REQ-1.3
func TestStartingTheServiceCreatesTheTablesAndTheAttemptsDirectory_TS_12_3(t *testing.T) {
	e := newEnv(t)
	e.Start()

	rows, err := e.DB.Query("SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	for _, want := range []string{"af_jobs", "af_attempts", "af_events", "af_subscriptions", "af_schema_migrations", "jobs"} {
		if !slices.Contains(tables, want) {
			t.Errorf("tables = %v, want %s among them", tables, want)
		}
	}
	// The queue's table has the columns the layer needs of it.
	if _, err := e.DB.Exec("SELECT group_key, progress FROM jobs LIMIT 1"); err != nil {
		t.Errorf("the queue's table lacks a column: %v", err)
	}

	dir := filepath.Join(e.Dir, "attempts")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("stat %s = %v, %v, want a directory with the mode 0700", dir, info, err)
	}

	// A second start, with the directory there and something in it, leaves it alone.
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(dir, "job_x", "1", "envelope.json")
	if err := os.MkdirAll(filepath.Dir(kept), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kept, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := jobs.New(jobs.Config{DB: e.DB, Subscriptions: e.Store, AttemptsDir: dir, Runner: fakerunner.New(), Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := second.Start(); err != nil {
		t.Fatalf("the second start: %v", err)
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o750 {
		t.Errorf("the directory has the mode %v after the second start, want it as it was", info.Mode().Perm())
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("a file in the directory is gone after the second start: %v", err)
	}
}

// TestAnAttemptIsOneQueueItemAndEnqueuingItAgainIsANoOpInEveryState_TS_12_23
// verifies TS-12-23: an attempt is one item of type attempt with the nonce
// <job id>:<n> and the payload {job_id, attempt}, belonging to its workspace, and
// enqueuing it again while it is pending, while it is delayed and while it is
// claimed leaves one item and one call of the runner.
// Verifies: 12-REQ-4.1
func TestAnAttemptIsOneQueueItemAndEnqueuingItAgainIsANoOpInEveryState_TS_12_23(t *testing.T) {
	r := fakerunner.New(fakerunner.Block(fakerunner.Step{}))
	e := newEnv(t, withRunner(r))
	job := e.create("widgets", jobs.KindFix, 42)
	nonce := job.ID + ":1"

	only := func(when, status string) queueItem {
		t.Helper()
		items := e.items()
		if len(items) != 1 {
			t.Fatalf("%s: the queue holds %d items, want 1: %+v", when, len(items), items)
		}
		if items[0].Status != status {
			t.Errorf("%s: the item is %s, want %s", when, items[0].Status, status)
		}
		return items[0]
	}
	enqueueAgain := func() {
		t.Helper()
		for range 2 {
			if err := e.Svc.Enqueue(ctx, job.ID, 1); err != nil {
				t.Fatalf("Enqueue: %v", err)
			}
		}
	}

	// pending
	it := only("after the job is made", "queued")
	var payload struct {
		JobID   string `json:"job_id"`
		Attempt int    `json:"attempt"`
	}
	if err := json.Unmarshal([]byte(it.Payload), &payload); err != nil || payload.JobID != job.ID || payload.Attempt != 1 {
		t.Errorf("payload = %s, want {job_id: %s, attempt: 1}", it.Payload, job.ID)
	}
	// The workspace is the group the queue serializes by. The key is the nonce: the
	// queue refuses a second queued or running item with the same type and key.
	if it.Type != "attempt" || it.Nonce != nonce || it.Group != "widgets" || it.Key != nonce {
		t.Errorf("item = %+v, want type attempt, nonce %s, key %s and group widgets", it, nonce, nonce)
	}
	enqueueAgain()
	only("enqueued again while pending", "queued")

	// delayed: put back, to come again later
	if _, err := e.DB.Exec("UPDATE jobs SET status = 'failed', available_at = '2999-01-01T00:00:00Z', retry_count = 1 WHERE nonce = ?", nonce); err != nil {
		t.Fatal(err)
	}
	enqueueAgain()
	only("enqueued again while delayed", "failed")
	if _, err := e.DB.Exec("UPDATE jobs SET status = 'queued', available_at = '2000-01-01T00:00:00Z' WHERE nonce = ?", nonce); err != nil {
		t.Fatal(err)
	}

	// claimed
	e.Start()
	if !r.Wait(1, 10*time.Second) {
		t.Fatal("the runner was not called")
	}
	only("while claimed", "running")
	enqueueAgain()
	only("enqueued again while claimed", "running")
	time.Sleep(100 * time.Millisecond)
	if n := len(r.Calls()); n != 1 {
		t.Errorf("the runner was called %d times, want once", n)
	}
}

// TestNoMoreThanWorkersCountAttemptsAreInTheRunnerAtOnce_TS_12_24 verifies TS-12-24:
// for any count from 1 to 4 and any mix of jobs across 1 to 6 workspaces, released
// in random order, the runner never has more than count calls at once, and every job
// reaches it and ends.
// Verifies: 12-REQ-4.2
func TestNoMoreThanWorkersCountAttemptsAreInTheRunnerAtOnce_TS_12_24(t *testing.T) {
	rng := rand.New(rand.NewPCG(12, 24))
	reachedCount := 0
	for trial := range 8 {
		count := 1 + rng.IntN(4)
		workspaces := 1 + rng.IntN(6)
		total := 3 + rng.IntN(9)
		r := fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))
		e := newEnv(t, withRunner(r), withWorkers(count))
		slugs := []string{"widgets"}
		for i := 1; i < workspaces; i++ {
			slug := fmt.Sprintf("space%d", i)
			e.addWorkspace(slug)
			slugs = append(slugs, slug)
		}
		var ids []string
		used := map[string]bool{}
		for i := range total {
			slug := slugs[rng.IntN(len(slugs))]
			used[slug] = true
			ids = append(ids, e.create(slug, jobs.KindFix, i+1).ID)
		}
		e.Start()

		// With nothing released yet, as many calls begin as the count and the workspaces allow.
		want := min(count, len(used))
		waitFor(t, "the calls to begin", func() bool { return r.Active() >= want })
		time.Sleep(30 * time.Millisecond)
		if r.Active() != want {
			t.Errorf("trial %d: %d calls in the runner with count %d and %d workspaces, want %d", trial, r.Active(), count, len(used), want)
		}

		released := map[int]bool{}
		for len(released) < total {
			var blocked []int
			for _, c := range r.Calls() {
				if !released[c.Index] {
					blocked = append(blocked, c.Index)
				}
			}
			if len(blocked) == 0 {
				time.Sleep(2 * time.Millisecond)
				continue
			}
			pick := blocked[rng.IntN(len(blocked))]
			released[pick] = true
			r.Release(pick)
		}
		for _, id := range ids {
			e.waitState(id, jobs.StateDone)
		}
		if r.MaxConcurrent() > count {
			t.Errorf("trial %d: %d calls were in the runner at once with count %d", trial, r.MaxConcurrent(), count)
		}
		if len(r.Calls()) != total {
			t.Errorf("trial %d: the runner had %d calls for %d jobs", trial, len(r.Calls()), total)
		}
		if r.MaxConcurrent() == count {
			reachedCount++
		}
	}
	if reachedCount < 3 {
		t.Errorf("the bound was reached in %d of 8 trials, want it reached often enough for the test to mean something", reachedCount)
	}
}

// TestJobsOnOneWorkspaceNeverOverlapAndRunInOrderWhileTwoWorkspacesOverlap_TS_12_25
// verifies TS-12-25: with count 2, W1 and G1 are in the runner together and W2
// waits for W1 to return; and a burst of jobs on one workspace is called in the
// order it was made, one at a time.
// Verifies: 12-REQ-4.3
func TestJobsOnOneWorkspaceNeverOverlapAndRunInOrderWhileTwoWorkspacesOverlap_TS_12_25(t *testing.T) {
	r := fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))
	e := newEnv(t, withRunner(r), withWorkers(2))
	e.addWorkspace("gadgets")
	w1, w2, g1 := e.create("widgets", jobs.KindFix, 1), e.create("widgets", jobs.KindFix, 2), e.create("gadgets", jobs.KindFix, 3)
	e.Start()

	if !r.Wait(2, 10*time.Second) {
		t.Fatal("two calls did not begin")
	}
	first := []string{r.Calls()[0].JobID, r.Calls()[1].JobID}
	slices.Sort(first)
	if want := []string{g1.ID, w1.ID}; !slices.Equal(first, []string{min(want[0], want[1]), max(want[0], want[1])}) {
		t.Fatalf("the first two calls are %v, want the jobs %s and %s", first, w1.ID, g1.ID)
	}
	if r.Active() != 2 {
		t.Errorf("%d calls in the runner, want W1 and G1 together", r.Active())
	}
	time.Sleep(100 * time.Millisecond)
	if len(r.Calls()) != 2 {
		t.Fatalf("%d calls, want W2 to wait for W1", len(r.Calls()))
	}
	for _, c := range r.Calls() {
		if c.JobID == w1.ID {
			r.Release(c.Index)
		}
	}
	if !r.Wait(3, 10*time.Second) || r.Calls()[2].JobID != w2.ID {
		t.Fatalf("the third call is not W2 once W1 returned: %+v", r.Calls())
	}

	// A burst on one workspace runs in the order it was made, one at a time: the
	// calls are released as they come, and there is never more than one open.
	order := []string{w1.ID, w2.ID}
	for i := range 6 {
		order = append(order, e.create("widgets", jobs.KindFix, 10+i).ID)
	}
	released := map[int]bool{}
	for _, c := range r.Calls() {
		if c.JobID == w1.ID {
			released[c.Index] = true
		}
	}
	for len(released) < len(order) {
		var open []fakerunner.Call
		for _, c := range r.Calls() {
			if c.Workspace == "widgets" && !released[c.Index] {
				open = append(open, c)
			}
		}
		switch {
		case len(open) > 1:
			t.Fatalf("%d calls of widgets are in the runner at once", len(open))
		case len(open) == 1:
			released[open[0].Index] = true
			r.Release(open[0].Index)
		default:
			time.Sleep(2 * time.Millisecond)
		}
	}
	var seen []string
	for _, c := range r.Calls() {
		if c.Workspace == "widgets" {
			seen = append(seen, c.JobID)
		}
	}
	if !slices.Equal(seen, order) {
		t.Errorf("widgets were called in the order\n%v\nwant\n%v", seen, order)
	}
}

// TestAnAttemptRunsAfterTheOnesBeforeItInItsWorkspaceWhateverOrderTheQueueHasThem is
// the order of 12-REQ-4.3 kept by the layer and not by the luck of the queue, which
// claims by a time of one second: the queue is given the attempts in the opposite
// order, in the same second, and the runner is called in the order they were made.
// Verifies: 12-REQ-4.3
func TestAnAttemptRunsAfterTheOnesBeforeItInItsWorkspaceWhateverOrderTheQueueHasThem(t *testing.T) {
	r := fakerunner.New()
	e := newEnv(t, withRunner(r), withoutEnqueue())
	var made []jobs.Doc
	for i := range 5 {
		made = append(made, e.create("widgets", jobs.KindFix, i+1))
	}
	for i := len(made) - 1; i >= 0; i-- {
		if err := e.Svc.Enqueue(ctx, made[i].ID, 1); err != nil {
			t.Fatal(err)
		}
	}
	e.Start()
	for _, d := range made {
		e.waitState(d.ID, jobs.StateDone)
	}
	var got []string
	for _, c := range r.Calls() {
		got = append(got, c.JobID)
	}
	var want []string
	for _, d := range made {
		want = append(want, d.ID)
	}
	if !slices.Equal(got, want) {
		t.Errorf("the runner was called in the order\n%v\nwant\n%v", got, want)
	}
}

// TestAHandoverMovesTheAttemptAndTheJobToRunningAndSaysSoBeforeTheRunnerIsCalled_TS_12_27
// is the flow of TS-12-27: the queue hands an attempt over; attempt and job are
// running with one delivery, and the last event when the runner is called is
// attempt_start {attempt 1, delivery 1}; and a claim transaction that fails changes
// nothing and calls nobody, and the attempt is handed over again once it can be.
// Verifies: 12-REQ-4.5
func TestAHandoverMovesTheAttemptAndTheJobToRunningAndSaysSoBeforeTheRunnerIsCalled_TS_12_27(t *testing.T) {
	var (
		mu       sync.Mutex
		failNext int
		atCall   struct {
			attempt, job, lastType, lastData string
			deliveries                       int
		}
	)
	var e *env
	r := fakerunner.New(fakerunner.Do(func(ctx context.Context, req jobs.Request) (jobs.Result, error) {
		mu.Lock()
		defer mu.Unlock()
		// The directory of the attempt is there when the runner is called.
		if want := filepath.Join(e.Dir, "attempts", req.JobID, "1"); req.Dir != want {
			t.Errorf("Dir = %s, want %s", req.Dir, want)
		}
		if info, err := os.Stat(req.Dir); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Errorf("the directory of the attempt at the call: %v, %v, want a directory with the mode 0700", info, err)
		}
		if req.Delivery != 1 || req.Attempt != 1 || req.Workspace != "widgets" || req.Kind != jobs.KindFix || req.Recorder == nil {
			t.Errorf("request = %+v, want attempt 1, delivery 1, widgets, fix and a recorder", req)
		}
		e.DB.QueryRow("SELECT state, deliveries FROM af_attempts WHERE job_id = ? AND n = 1", req.JobID).Scan(&atCall.attempt, &atCall.deliveries)
		e.DB.QueryRow("SELECT state FROM af_jobs WHERE id = ?", req.JobID).Scan(&atCall.job)
		e.DB.QueryRow("SELECT type, data FROM af_events WHERE job_id = ? ORDER BY seq DESC LIMIT 1", req.JobID).Scan(&atCall.lastType, &atCall.lastData)
		return jobs.Result{Envelope: fakerunner.Env(toolio.StatusDone)}, nil
	}))
	e = newEnv(t, withRunner(r), func(c *jobs.Config) {
		c.StoreFault = func(op string) error {
			mu.Lock()
			defer mu.Unlock()
			if op == jobs.OpClaim && failNext > 0 {
				failNext--
				return fmt.Errorf("injected failure of the claim")
			}
			return nil
		}
	})
	mu.Lock()
	failNext = 2
	mu.Unlock()
	job := e.create("widgets", jobs.KindFix, 1)
	e.Start()

	// The first two claims fail, and the attempt is handed back and tried again; the
	// third is the handover. (That a failed claim changes nothing is checked on the
	// store, where the failure can be told from the next try.)
	waitFor(t, "the claim to be tried twice", func() bool { mu.Lock(); defer mu.Unlock(); return failNext == 0 })
	e.waitState(job.ID, jobs.StateDone)
	mu.Lock()
	defer mu.Unlock()
	if atCall.attempt != "running" || atCall.job != "running" || atCall.deliveries != 1 {
		t.Errorf("when the runner was called: attempt %s, job %s, deliveries %d, want running, running and 1", atCall.attempt, atCall.job, atCall.deliveries)
	}
	var start struct {
		Attempt  int `json:"attempt"`
		Delivery int `json:"delivery"`
	}
	if atCall.lastType != "attempt_start" || json.Unmarshal([]byte(atCall.lastData), &start) != nil || start.Attempt != 1 || start.Delivery != 1 {
		t.Errorf("when the runner was called, the last event was %s %s, want attempt_start {attempt 1, delivery 1}", atCall.lastType, atCall.lastData)
	}
	if n := len(r.Calls()); n != 1 {
		t.Errorf("the runner was called %d times, want once: the failed claims called nobody", n)
	}
}

// TestAnItemWhoseJobOrAttemptIsGoneOrFinishedIsAcknowledgedWithoutCallingTheRunner_TS_12_28
// verifies TS-12-28: items for a job that was deleted, a job that is cancelled, a job
// that is done, and an attempt that is cancelled in a running job reach the queue's
// handler; the runner is not called, every item is completed in the queue, and no
// row of an attempt changes.
// Verifies: 12-REQ-4.6
func TestAnItemWhoseJobOrAttemptIsGoneOrFinishedIsAcknowledgedWithoutCallingTheRunner_TS_12_28(t *testing.T) {
	r := fakerunner.New()
	e := newEnv(t, withRunner(r))
	deleted := e.create("widgets", jobs.KindFix, 1)
	cancelled := e.create("widgets", jobs.KindFix, 2)
	done := e.create("widgets", jobs.KindFix, 3)
	oddAttempt := e.create("widgets", jobs.KindFix, 4)

	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := e.DB.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec("DELETE FROM af_events WHERE job_id = ?", deleted.ID)
	mustExec("DELETE FROM af_attempts WHERE job_id = ?", deleted.ID)
	mustExec("DELETE FROM af_jobs WHERE id = ?", deleted.ID)
	mustExec("UPDATE af_jobs SET state = 'cancelled', finished_at = '2026-10-22T08:00:00.000Z' WHERE id = ?", cancelled.ID)
	mustExec("UPDATE af_jobs SET state = 'done', finished_at = '2026-10-22T08:00:00.000Z' WHERE id = ?", done.ID)
	mustExec("UPDATE af_jobs SET state = 'running' WHERE id = ?", oddAttempt.ID)
	mustExec("UPDATE af_attempts SET state = 'cancelled', finished_at = '2026-10-22T08:00:00.000Z' WHERE job_id = ?", oddAttempt.ID)

	snapshot := func() string {
		rows, err := e.DB.Query("SELECT job_id, n, state, deliveries, transient_failures, started_at, finished_at, has_envelope FROM af_attempts ORDER BY job_id, n")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out string
		for rows.Next() {
			var job, state string
			var n, deliveries, transient, envelope int
			var started, finished sql.NullString
			if err := rows.Scan(&job, &n, &state, &deliveries, &transient, &started, &finished, &envelope); err != nil {
				t.Fatal(err)
			}
			out += fmt.Sprintln(job, n, state, deliveries, transient, started, finished, envelope)
		}
		return out
	}
	before := snapshot()
	e.Start()

	waitFor(t, "every item to be completed", func() bool {
		for _, it := range e.items() {
			if it.Status != "completed" {
				return false
			}
		}
		return len(e.items()) == 4
	})
	if calls := r.Calls(); len(calls) != 0 {
		t.Errorf("the runner was called for %d of them, want none", len(calls))
	}
	if after := snapshot(); after != before {
		t.Errorf("the attempt rows changed:\nbefore\n%safter\n%s", before, after)
	}
}

// TestTheCauseOfACancelAndOfAnUnsubscribeIsACancelAndTheCauseOfAShutdownIsNot_TS_12_30
// verifies TS-12-30: the predicate on context.Cause is true for a cancel and for an
// unsubscribe, false for a shutdown and for a context that was not cancelled, and
// the shutdown cause is told from the other two.
// Verifies: 12-REQ-5.2
func TestTheCauseOfACancelAndOfAnUnsubscribeIsACancelAndTheCauseOfAShutdownIsNot_TS_12_30(t *testing.T) {
	cause := func(why error) error {
		ctx, cancel := context.WithCancelCause(context.Background())
		if why != nil {
			cancel(why)
		}
		defer cancel(nil)
		return context.Cause(ctx)
	}
	if !jobs.IsCancel(cause(jobs.ErrCancelled)) {
		t.Error("a cancel is not read as a cancel")
	}
	if !jobs.IsCancel(cause(jobs.ErrUnsubscribed)) {
		t.Error("an unsubscribe is not read as a cancel")
	}
	if jobs.IsCancel(cause(jobs.ErrShutdown)) {
		t.Error("a shutdown is read as a cancel")
	}
	if jobs.IsCancel(cause(nil)) || jobs.IsCancel(nil) {
		t.Error("a context that was not cancelled is read as cancelled")
	}
	if jobs.ErrShutdown == jobs.ErrCancelled || jobs.ErrShutdown == jobs.ErrUnsubscribed || jobs.ErrCancelled == jobs.ErrUnsubscribed {
		t.Error("two of the causes are the same error")
	}
}

// TestConcurrentRecorderCallsGiveGapFreeSeqsAndTheHubReferencesShowDuringTheRun_TS_12_31
// verifies TS-12-31: 32 goroutines of one runner call Emit 50 times each and set
// the hub references while the job is read; the stored numbers are 1 to k without a
// gap or a repeat, the race detector reports nothing, and the job shows its hub
// references during the run.
// Verifies: 12-REQ-5.3
func TestConcurrentRecorderCallsGiveGapFreeSeqsAndTheHubReferencesShowDuringTheRun_TS_12_31(t *testing.T) {
	emitted, release := make(chan struct{}), make(chan struct{})
	r := fakerunner.New(fakerunner.Do(func(ctx context.Context, req jobs.Request) (jobs.Result, error) {
		var wg sync.WaitGroup
		for g := range 32 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range 50 {
					req.Recorder.Emit("step", map[string]int{"goroutine": g, "i": i})
					if i == 25 {
						req.Recorder.Hub("session-1", "run-1", "box-1")
					}
				}
			}()
		}
		wg.Wait()
		close(emitted)
		<-release
		return jobs.Result{Envelope: fakerunner.Env(toolio.StatusDone)}, nil
	}))
	e := newEnv(t, withRunner(r))
	job := e.create("widgets", jobs.KindFix, 1)
	e.Start()

	// The job is read while the goroutines run.
	stop := make(chan struct{})
	var reads sync.WaitGroup
	reads.Add(1)
	go func() {
		defer reads.Done()
		for {
			select {
			case <-stop:
				return
			default:
				e.Svc.Get(ctx, job.ID)
			}
		}
	}()
	select {
	case <-emitted:
	case <-time.After(30 * time.Second):
		t.Fatal("the goroutines did not finish")
	}
	close(stop)
	reads.Wait()

	doc := e.get(job.ID)
	if doc.Hub == nil || doc.Hub.SessionID == nil || *doc.Hub.SessionID != "session-1" || *doc.Hub.RunID != "run-1" || *doc.Hub.SandboxID != "box-1" {
		t.Errorf("hub = %+v during the run, want session-1, run-1 and box-1", doc.Hub)
	}
	rows, err := e.DB.Query("SELECT seq FROM af_events WHERE job_id = ? ORDER BY seq", job.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var seq int
		if err := rows.Scan(&seq); err != nil {
			t.Fatal(err)
		}
		if n++; seq != n {
			t.Fatalf("the %dth event has seq %d, want %d", n, seq, n)
		}
	}
	// job_state queued, job_state running, attempt_start, and 32 x 50 events.
	if n != 3+32*50 {
		t.Errorf("%d events stored, want %d", n, 3+32*50)
	}
	close(release)
	e.waitState(job.ID, jobs.StateDone)
}

// TestAServiceNeedsWhatItRunsOn checks that New refuses a configuration that cannot
// run attempts: no database, no subscriptions, no runner, no directory and fewer than
// one worker.
func TestAServiceNeedsWhatItRunsOn(t *testing.T) {
	e := newEnv(t)
	good := jobs.Config{DB: e.DB, Subscriptions: e.Store, Runner: fakerunner.New(), AttemptsDir: filepath.Join(e.Dir, "attempts"), Workers: 1}
	for name, change := range map[string]func(*jobs.Config){
		"no database":      func(c *jobs.Config) { c.DB = nil },
		"no subscriptions": func(c *jobs.Config) { c.Subscriptions = nil },
		"no runner":        func(c *jobs.Config) { c.Runner = nil },
		"no directory":     func(c *jobs.Config) { c.AttemptsDir = "" },
		"no workers":       func(c *jobs.Config) { c.Workers = 0 },
		"negative workers": func(c *jobs.Config) { c.Workers = -1 },
	} {
		cfg := good
		change(&cfg)
		if svc, err := jobs.New(cfg); err == nil {
			svc.Close()
			t.Errorf("%s: New accepted the configuration", name)
		}
	}
	if svc, err := jobs.New(good); err != nil {
		t.Errorf("New refused a good configuration: %v", err)
	} else {
		svc.Close()
	}
}
