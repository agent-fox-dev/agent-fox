package jobs_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// items are the rows of the queue's table, each as type key group nonce status payload.
func (e *env) items() []string {
	e.t.Helper()
	return testutil.Column(e.t, e.DB, "SELECT type || ' ' || key || ' ' || group_key || ' ' || nonce || ' ' || status || ' ' || payload FROM jobs ORDER BY rowid")
}

func TestNew_RefusesAConfigurationItCannotRunOn(t *testing.T) {
	e := newEnv(t)
	for name, change := range map[string]func(*jobs.Config){
		"no database":      func(c *jobs.Config) { c.DB = nil },
		"no subscriptions": func(c *jobs.Config) { c.Subscriptions = nil },
		"no runner":        func(c *jobs.Config) { c.Runner = nil },
		"no directory":     func(c *jobs.Config) { c.AttemptsDir = "" },
		"no workers":       func(c *jobs.Config) { c.Workers = 0 },
	} {
		cfg := e.cfg
		change(&cfg)
		if svc, err := jobs.New(cfg); err == nil {
			svc.Close()
			t.Errorf("%s: New accepted the configuration", name)
		}
	}
	// What a configuration leaves out has a default: the clock, the log, the queue's timing and the remover.
	svc, err := jobs.New(jobs.Config{DB: e.DB, Subscriptions: e.Subs, Runner: fakerunner.New(), AttemptsDir: e.Dir, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStart_MakesThePrivateAttemptsDirectoryAndLeavesOneThatIsThere(t *testing.T) {
	e := newEnv(t)
	e.start()
	if info, err := os.Stat(e.Dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("stat = %v, %v, want a directory with the mode 0700", info, err)
	}
	kept := testutil.WriteFile(t, e.Dir, "job_x/1/envelope.json", "{}")
	if err := os.Chmod(e.Dir, 0o750); err != nil {
		t.Fatal(err)
	}
	e.restart(fakerunner.New())
	if info, _ := os.Stat(e.Dir); info.Mode().Perm() != 0o750 || testutil.ReadFile(t, kept) != "{}" {
		t.Error("a second start changed the directory or what is in it")
	}
	// A directory that cannot be made fails the start.
	e.cfg.AttemptsDir = filepath.Join(kept, "attempts")
	e.build()
	if err := e.Svc.Start(); err == nil {
		t.Error("Start made a directory under a file")
	}
}

func TestEnqueue_AnAttemptIsOneItemOfItsWorkspaceAndEnqueuingItAgainChangesNothing(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Block(fakerunner.Step{}))))
	job := e.create("widgets", 42, "")
	nonce := job.ID + ":1"
	again := func(when string, status string) {
		t.Helper()
		for range 2 {
			if err := e.Svc.Enqueue(ctx, job.ID, 1); err != nil {
				t.Fatal(err)
			}
		}
		want := fmt.Sprintf(`attempt %s widgets %s %s {"job_id":"%s","attempt":1}`, nonce, nonce, status, job.ID)
		if got := e.items(); len(got) != 1 || got[0] != want {
			t.Errorf("%s: the queue holds %v, want %s", when, got, want)
		}
	}
	again("pending", "queued")
	testutil.Exec(t, e.DB, "UPDATE jobs SET status = 'failed', available_at = '2999-01-01T00:00:00Z', retry_count = 1 WHERE nonce = ?", nonce)
	again("put back", "failed")
	testutil.Exec(t, e.DB, "UPDATE jobs SET status = 'queued', available_at = '2000-01-01T00:00:00Z' WHERE nonce = ?", nonce)
	e.start()
	e.waitCalls(1)
	again("claimed", "running")
	e.Runner.ReleaseAll()
	testutil.Eventually(t, "the item to be completed", func() bool { return testutil.Count(t, e.DB, "jobs", "status = 'completed'") == 1 })
	again("completed", "completed")
	if n := len(e.Runner.Calls()); n != 1 {
		t.Errorf("%d calls, want one", n)
	}
	if err := e.Svc.Enqueue(ctx, "job_00000000000000000000", 1); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("Enqueue of no job: %v, want ErrNotFound", err)
	}
}

func TestWorkers_NoMoreAttemptsThanTheCountAreInARunnerAtOnce(t *testing.T) {
	r := fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))
	e := newEnv(t, withRunner(r), withWorkers(2))
	var ids []string
	for i, ws := range []string{"widgets", "gadgets", "sprockets", "cogs", "widgets", "gadgets"} {
		e.Subs.set(ws, "ok")
		ids = append(ids, e.create(ws, i+1, "").ID)
	}
	e.start()
	testutil.Eventually(t, "two calls in the runner", func() bool { return r.Active() == 2 })
	for i := range ids {
		e.waitCalls(i + 1)
		r.Release(i)
	}
	for _, id := range ids {
		e.waitState(id, jobs.StateDone)
	}
	if r.MaxConcurrent() != 2 || len(r.Calls()) != len(ids) {
		t.Errorf("%d calls at most at once and %d in all, want 2 and %d", r.MaxConcurrent(), len(r.Calls()), len(ids))
	}
}

func TestWorkspace_RunsItsAttemptsOneAtATimeInTheOrderTheyWereMade(t *testing.T) {
	var mu sync.Mutex
	active, most := map[string]int{}, map[string]int{}
	release := make(chan struct{})
	r := fakerunner.New().Then(fakerunner.Do(func(_ context.Context, req jobs.Request) (jobs.Result, error) {
		mu.Lock()
		active[req.Workspace]++
		most[req.Workspace] = max(most[req.Workspace], active[req.Workspace])
		mu.Unlock()
		<-release
		mu.Lock()
		active[req.Workspace]--
		mu.Unlock()
		return jobs.Result{Envelope: fakerunner.Env(toolio.StatusDone)}, nil
	}))
	// The queue is given the attempts of widgets in the opposite order, in the same second.
	e := newEnv(t, withRunner(r), withWorkers(3), withoutEnqueue())
	e.openAtEnd(release)
	var widgets []string
	for i := range 5 {
		widgets = append(widgets, e.create("widgets", i+1, "").ID)
	}
	gadget := e.create("gadgets", 9, "")
	reversed := append([]string{gadget.ID}, widgets...)
	slices.Reverse(reversed[1:])
	for _, id := range reversed {
		if err := e.Svc.Enqueue(ctx, id, 1); err != nil {
			t.Fatal(err)
		}
	}
	e.start()
	// Two workspaces overlap.
	testutil.Eventually(t, "the first of widgets and the job of gadgets together", func() bool { return r.Active() == 2 })
	close(release)
	for _, id := range append(widgets, gadget.ID) {
		e.waitState(id, jobs.StateDone)
	}
	var order []string
	for _, c := range r.Calls() {
		if c.Workspace == "widgets" {
			order = append(order, c.JobID)
		}
	}
	if !slices.Equal(order, widgets) || most["widgets"] != 1 {
		t.Errorf("widgets ran in the order %v with %d at once, want %v one at a time", order, most["widgets"], widgets)
	}
}

func TestHandover_TheRunnerIsCalledWithTheAttemptAndTheJobRunningInADirectoryOfItsOwn(t *testing.T) {
	type seen struct {
		dir, attempt, job, last string
		mode                    os.FileMode
		req                     jobs.Request
	}
	var at seen
	var e *env
	r := fakerunner.New(fakerunner.Do(func(_ context.Context, req jobs.Request) (jobs.Result, error) {
		info, err := os.Stat(req.Dir)
		if err != nil {
			t.Error(err)
		}
		at = seen{dir: req.Dir, mode: info.Mode().Perm(), req: req,
			attempt: testutil.Column(t, e.DB, "SELECT state || deliveries FROM af_attempts WHERE job_id = ?", req.JobID)[0],
			job:     testutil.Column(t, e.DB, "SELECT state FROM af_jobs WHERE id = ?", req.JobID)[0],
			last:    testutil.Column(t, e.DB, "SELECT type || data FROM af_events WHERE job_id = ? ORDER BY seq DESC LIMIT 1", req.JobID)[0]}
		return jobs.Result{Envelope: fakerunner.Env(toolio.StatusDone)}, nil
	}))
	failures := 2
	e = newEnv(t, withRunner(r), withFault(func(op string) error {
		if op == jobs.OpClaim && failures > 0 {
			failures-- // a claim that fails is handed back, and tried again
			return errors.New("injected")
		}
		return nil
	}))
	job := e.create("widgets", 1, "af:fix")
	e.start()
	e.waitState(job.ID, jobs.StateDone)
	if at.dir != e.file(job.ID, 1, "") || !filepath.IsAbs(at.dir) || at.mode != 0o700 {
		t.Errorf("the runner was called in %s (%v), want %s with the mode 0700", at.dir, at.mode, e.file(job.ID, 1, ""))
	}
	if at.attempt != "running1" || at.job != "running" || at.last != `attempt_start{"attempt":1,"delivery":1}` {
		t.Errorf("at the call: attempt %s, job %s, last event %s, want running with one delivery, and attempt_start", at.attempt, at.job, at.last)
	}
	q := at.req
	if q.JobID != job.ID || q.Attempt != 1 || q.Delivery != 1 || q.Round != 1 || q.Workspace != "widgets" || q.Kind != jobs.KindFix ||
		string(q.Input.Issue) != "1" || q.Source != (jobs.Source{Kind: "label", By: "poll"}) || q.RequestLabel != "af:fix" || q.Recorder == nil || len(q.Answers) != 0 {
		t.Errorf("request = %+v", q)
	}
	if n := len(r.Calls()); n != 1 || len(e.Log.Find("WARN", "could not be claimed")) != 2 {
		t.Errorf("%d calls and %v, want one call after two claims that failed with a warning each", n, e.Log.Records())
	}
}

func TestHandler_AcknowledgesAnItemWhoseJobOrAttemptIsGoneOrFinished(t *testing.T) {
	e := newEnv(t)
	gone, cancelled, done, oddAttempt := e.create("widgets", 1, ""), e.create("widgets", 2, ""), e.create("widgets", 3, ""), e.create("widgets", 4, "")
	for _, table := range []string{"af_events", "af_attempts"} {
		testutil.Exec(t, e.DB, "DELETE FROM "+table+" WHERE job_id = ?", gone.ID)
	}
	testutil.Exec(t, e.DB, "DELETE FROM af_jobs WHERE id = ?", gone.ID)
	testutil.Exec(t, e.DB, "UPDATE af_jobs SET state = 'cancelled', finished_at = '2026-10-22T08:00:00.000Z' WHERE id = ?", cancelled.ID)
	testutil.Exec(t, e.DB, "UPDATE af_jobs SET state = 'done', finished_at = '2026-10-22T08:00:00.000Z' WHERE id = ?", done.ID)
	testutil.Exec(t, e.DB, "UPDATE af_jobs SET state = 'running' WHERE id = ?", oddAttempt.ID)
	testutil.Exec(t, e.DB, "UPDATE af_attempts SET state = 'cancelled' WHERE job_id = ?", oddAttempt.ID)
	testutil.Exec(t, e.DB, "INSERT INTO jobs (id, type, key, group_key, nonce, payload, available_at, submitted_by, created_at, updated_at) VALUES ('bad', 'attempt', 'bad', 'widgets', 'bad', 'not json', '2000-01-01T00:00:00Z', 'test', '2000-01-01T00:00:00Z', '2000-01-01T00:00:00Z')")
	snapshot := func() []string {
		return testutil.Column(t, e.DB, "SELECT job_id || n || state || deliveries || has_envelope FROM af_attempts ORDER BY job_id, n")
	}
	before := snapshot()
	e.start()
	testutil.Eventually(t, "every item to be through", func() bool {
		return testutil.Count(t, e.DB, "jobs", "status IN ('queued', 'running', 'failed')") == 0
	})
	if n := len(e.Runner.Calls()); n != 0 {
		t.Errorf("the runner was called %d times, want never", n)
	}
	if after := snapshot(); !slices.Equal(after, before) {
		t.Errorf("the attempts changed: %v, then %v", before, after)
	}
}

func TestSweep_QueuesTheAttemptsThatAreNotInTheQueueAtStartAndEveryMinute(t *testing.T) {
	r := fakerunner.New()
	var e *env
	failNext := true
	e = newEnv(t, withRunner(r), withWorkers(1), func(c *jobs.Config) {
		c.Enqueue = func(ctx context.Context, id string, n int) error {
			if failNext {
				failNext = false
				return errors.New("the queue is down")
			}
			return e.Svc.Enqueue(ctx, id, n)
		}
	})
	// A job whose attempt could not be queued is made all the same, with a warning.
	lost := e.create("widgets", 1, "")
	if warned := e.Log.Find("WARN", "sweep will queue it"); len(warned) != 1 || warned[0]["job"] != lost.ID {
		t.Fatalf("warnings = %v, want one naming %s", e.Log.Records(), lost.ID)
	}
	queued := e.create("widgets", 2, "")
	e.start()
	e.waitState(lost.ID, jobs.StateDone)
	e.waitState(queued.ID, jobs.StateDone)
	// One made after the start that the queue lost is put there by the minute's sweep.
	failNext = true
	later := e.create("gadgets", 3, "")
	e.Clock.Advance(time.Minute)
	e.waitState(later.ID, jobs.StateDone)
	for range 3 {
		e.Clock.Advance(time.Minute)
	}
	seen := map[string]int{}
	for _, c := range r.Calls() {
		seen[c.JobID]++
	}
	if seen[lost.ID] != 1 || seen[queued.ID] != 1 || seen[later.ID] != 1 || len(seen) != 3 {
		t.Errorf("calls by job = %v, want each job once", seen)
	}
}
