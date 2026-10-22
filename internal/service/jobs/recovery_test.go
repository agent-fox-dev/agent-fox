package jobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

var epoch = time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)

var sandboxFailure = &toolio.Failure{Category: toolio.CategorySandbox, Message: "no box", Retryable: true}

// waitCalls waits until the runner has had n calls.
func waitCalls(t *testing.T, r *fakerunner.Runner, n int) {
	t.Helper()
	if !r.Wait(n, 10*time.Second) {
		t.Fatalf("the runner has had %d calls, want %d", len(r.Calls()), n)
	}
}

// settle gives the service the time to do what it is not to do.
func settle() { time.Sleep(100 * time.Millisecond) }

// TestTheFirstAndSecondTransientFailureHandTheSameAttemptOverAgainAfter30sAnd2min_TS_12_44
// verifies TS-12-44: a transient failure returns the attempt to queued with no
// envelope stored, counts it in transient_failures, and hands the same attempt over
// again after 30 s, and after the second after 2 min, on the clock the layer is
// given; the runner is called again with attempt 1 and one more delivery, and no
// attempt 2 is made.
// Verifies: 12-REQ-7.1
func TestTheFirstAndSecondTransientFailureHandTheSameAttemptOverAgainAfter30sAnd2min_TS_12_44(t *testing.T) {
	clk := fakeclock.New(epoch)
	r := fakerunner.New(fakerunner.TransientFailure(sandboxFailure), fakerunner.TransientFailure(sandboxFailure), fakerunner.Done())
	e := newEnv(t, withRunner(r), withClock(clk))
	job := e.create("widgets", jobs.KindFix, 1)
	e.Start()

	between := func(wantWaits []time.Duration, wantFailures int) {
		t.Helper()
		if !clk.WaitForWaits(len(wantWaits), 10*time.Second) {
			t.Fatalf("waits = %v, want %v", clk.Waits(), wantWaits)
		}
		if got := clk.Waits(); !slices.Equal(got, wantWaits) {
			t.Fatalf("waits = %v, want %v", got, wantWaits)
		}
		doc := e.get(job.ID)
		a := doc.Attempts[0]
		if a.State != jobs.AttemptQueued || doc.State != jobs.StateQueued || a.TransientFailures != wantFailures || a.Status != nil || a.FinishedAt != nil || doc.FinishedAt != nil {
			t.Errorf("during the wait: attempt %s, job %s, transient_failures %d, status %v, finished %v, want queued, queued, %d and no envelope", a.State, doc.State, a.TransientFailures, a.Status, a.FinishedAt, wantFailures)
		}
		if _, err := os.Stat(e.fileOf(job.ID, 1, "envelope.json")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("an envelope.json was stored between the tries: %v", err)
		}
		if len(doc.Attempts) != 1 {
			t.Errorf("%d attempts, want the same one", len(doc.Attempts))
		}
		if len(r.Calls()) != wantFailures {
			t.Errorf("%d calls during the wait, want %d: the attempt is not handed over before the wait ends", len(r.Calls()), wantFailures)
		}
	}
	between([]time.Duration{30 * time.Second}, 1)
	clk.Advance(29 * time.Second)
	settle()
	if len(r.Calls()) != 1 {
		t.Fatalf("the attempt was handed over again after 29 s")
	}
	clk.Advance(time.Second)
	between([]time.Duration{30 * time.Second, 2 * time.Minute}, 2)
	clk.Advance(2 * time.Minute)

	doc := e.waitRest(job.ID)
	if doc.State != jobs.StateDone || len(doc.Attempts) != 1 || doc.Attempts[0].TransientFailures != 2 {
		t.Errorf("job %s with %d attempts, transient_failures %d, want done, 1 and 2", doc.State, len(doc.Attempts), doc.Attempts[0].TransientFailures)
	}
	var attempts, deliveries []int
	for _, c := range r.Calls() {
		attempts, deliveries = append(attempts, c.Attempt), append(deliveries, c.Delivery)
	}
	if !slices.Equal(attempts, []int{1, 1, 1}) || !slices.Equal(deliveries, []int{1, 2, 3}) {
		t.Errorf("calls had the attempts %v and the deliveries %v, want [1 1 1] and [1 2 3]", attempts, deliveries)
	}
}

// TestTheThirdTransientFailureFailsTheAttemptAndTheJobWithTheFailuresCategory_TS_12_45
// verifies TS-12-45: the third transient failure ends the attempt failed with an
// envelope that carries the failure's category, fails the job, counts 3, and makes
// no attempt 2, whatever the retryable flag and the category (brain too).
// Verifies: 12-REQ-7.2
func TestTheThirdTransientFailureFailsTheAttemptAndTheJobWithTheFailuresCategory_TS_12_45(t *testing.T) {
	for _, category := range []string{toolio.CategorySandbox, toolio.CategoryBrain, toolio.CategoryNoResult} {
		t.Run(category, func(t *testing.T) {
			clk := fakeclock.New(epoch)
			failure := &toolio.Failure{Category: category, Message: "transient " + category, Retryable: true}
			e := newEnv(t, withRunner(fakerunner.New().Then(fakerunner.TransientFailure(failure))), withClock(clk))
			job := e.create("widgets", jobs.KindFix, 1)
			e.Start()
			clk.WaitForWaits(1, 10*time.Second)
			clk.Advance(30 * time.Second)
			clk.WaitForWaits(2, 10*time.Second)
			clk.Advance(2 * time.Minute)
			doc := e.waitRest(job.ID)

			if doc.State != jobs.StateFailed || len(doc.Attempts) != 1 || doc.Attempts[0].State != jobs.AttemptFailed || doc.Attempts[0].TransientFailures != 3 {
				t.Fatalf("job %s with %d attempts, attempt %s, transient_failures %d, want failed, 1, failed and 3", doc.State, len(doc.Attempts), doc.Attempts[0].State, doc.Attempts[0].TransientFailures)
			}
			if doc.Result == nil || doc.Result.Error == nil || doc.Result.Error.Category != category || doc.Result.Error.Message != failure.Message || !doc.Result.Error.Retryable || doc.Result.ExitCode != 1 {
				t.Errorf("result = %+v, want the failure's category, message and flag, and exit code 1", doc.Result)
			}
			if _, err := os.Stat(e.fileOf(job.ID, 1, "envelope.json")); err != nil {
				t.Errorf("the synthesized envelope.json is not there: %v", err)
			}
			if got := clk.Waits(); !slices.Equal(got, []time.Duration{30 * time.Second, 2 * time.Minute}) {
				t.Errorf("waits = %v, want 30 s and 2 min, and no wait after the third", got)
			}
		})
	}
}

// TestShutdownStopsClaimingCancelsRunnersWithTheShutdownCauseAndWaitsUpTo30s_TS_12_46
// verifies TS-12-46: closing the service cancels the running attempt with a cause that
// is a shutdown and not a cancel, does not claim the job that waits, returns once the
// runner has returned, and returns after the wait of 30 s on the clock if the runner
// never does.
// Verifies: 12-REQ-7.3
func TestShutdownStopsClaimingCancelsRunnersWithTheShutdownCauseAndWaitsUpTo30s_TS_12_46(t *testing.T) {
	closeInBackground := func(e *env) <-chan error {
		done := make(chan error, 1)
		go func() { done <- e.Svc.Close() }()
		return done
	}

	t.Run("a runner that returns when it is told", func(t *testing.T) {
		clk := fakeclock.New(epoch)
		r := fakerunner.New(fakerunner.Block(fakerunner.Step{}))
		e := newEnv(t, withRunner(r), withWorkers(1), withClock(clk))
		e.addWorkspace("gadgets")
		held := e.create("widgets", jobs.KindFix, 1)
		waiting := e.create("gadgets", jobs.KindFix, 2)
		e.Start()
		waitCalls(t, r, 1)
		settle() // the second job's handler waits for the slot

		done := closeInBackground(e)
		waitFor(t, "the runner to be told", func() bool { return r.Calls()[0].Cause() != nil })
		if cause := r.Calls()[0].Cause(); jobs.IsCancel(cause) || !errors.Is(cause, jobs.ErrShutdown) {
			t.Errorf("cause = %v, want a shutdown and not a cancel", cause)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Close: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Close did not return once the runner had")
		}
		if n := len(r.Calls()); n != 1 {
			t.Errorf("the runner had %d calls, want only the one it held: the second job is not claimed", n)
		}
		if d := e.get(waiting.ID); d.State != jobs.StateQueued || d.Attempts[0].State != jobs.AttemptQueued || d.Attempts[0].Deliveries != 0 {
			t.Errorf("the job that waited is %s, attempt %s, deliveries %d, want it queued and not claimed", d.State, d.Attempts[0].State, d.Attempts[0].Deliveries)
		}
		if d := e.get(held.ID); d.State != jobs.StateRunning || d.Attempts[0].State != jobs.AttemptRunning {
			t.Errorf("the job that was held is %s, attempt %s, want it left running", d.State, d.Attempts[0].State)
		}
	})

	t.Run("a runner that never returns", func(t *testing.T) {
		clk := fakeclock.New(epoch)
		r := fakerunner.New(fakerunner.BlockIgnoringContext(fakerunner.Step{}))
		e := newEnv(t, withRunner(r), withClock(clk))
		e.create("widgets", jobs.KindFix, 1)
		e.Start()
		waitCalls(t, r, 1)

		done := closeInBackground(e)
		if !clk.WaitForWaits(1, 10*time.Second) || clk.Waits()[0] != 30*time.Second {
			t.Fatalf("waits = %v, want a wait of 30 s for the runners", clk.Waits())
		}
		select {
		case <-done:
			t.Fatal("Close returned before the 30 s were up and the runner had returned")
		case <-time.After(150 * time.Millisecond):
		}
		clk.Advance(30 * time.Second)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("Close did not return after the wait of 30 s")
		}
	})
}

// TestWhatARunnerReturnsAfterAShutdownIsDiscardedAndTheAttemptStaysRunning_TS_12_47
// verifies TS-12-47: runners that, once they are told of the shutdown, return a done
// envelope, a plain error and a panic leave no envelope.json, an attempt and a job
// that are still running, no attempt_end and no job_state at rest, and no panic
// reaches the service.
// Verifies: 12-REQ-7.4
func TestWhatARunnerReturnsAfterAShutdownIsDiscardedAndTheAttemptStaysRunning_TS_12_47(t *testing.T) {
	r := fakerunner.New(
		fakerunner.Block(fakerunner.Done()),
		fakerunner.Block(fakerunner.Fail(errors.New("late failure"))),
		fakerunner.Block(fakerunner.Panic("late bang")),
	)
	e := newEnv(t, withRunner(r), withWorkers(3))
	e.addWorkspace("gadgets")
	e.addWorkspace("sprockets")
	ids := []string{e.create("widgets", jobs.KindFix, 1).ID, e.create("gadgets", jobs.KindFix, 2).ID, e.create("sprockets", jobs.KindFix, 3).ID}
	e.Start()
	waitCalls(t, r, 3)
	if err := e.Svc.Close(); err != nil {
		t.Fatal(err)
	}

	for _, id := range ids {
		if _, err := os.Stat(e.fileOf(id, 1, "envelope.json")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: an envelope.json was written after the shutdown: %v", id, err)
		}
		doc := e.get(id)
		if doc.State != jobs.StateRunning || doc.Attempts[0].State != jobs.AttemptRunning || doc.FinishedAt != nil || doc.Attempts[0].Status != nil {
			t.Errorf("%s: job %s, attempt %s, finished %v, want the attempt and the job still running", id, doc.State, doc.Attempts[0].State, doc.FinishedAt)
		}
		for _, ev := range e.events(id) {
			var state struct {
				AtRest bool `json:"at_rest"`
			}
			json.Unmarshal(ev.Data, &state)
			if ev.Type == "attempt_end" || (ev.Type == "job_state" && state.AtRest) {
				t.Errorf("%s: the log holds %s %s after a shutdown", id, ev.Type, ev.Data)
			}
		}
	}
	if recs := e.Logs.find("ERROR", ""); len(recs) != 0 {
		t.Errorf("an ERROR was logged for what was discarded: %v", recs)
	}
}

// TestAfterARestartTheInterruptedAttemptIsHandedOverAgainWithOneMoreDelivery_TS_12_48
// verifies TS-12-48: a service started on the file where another stopped while a
// runner held attempt 1 hands that attempt over again, with handover count 2, no new
// attempt row, no transient failure and no wait; and once more, with 3.
// Verifies: 12-REQ-7.5
func TestAfterARestartTheInterruptedAttemptIsHandedOverAgainWithOneMoreDelivery_TS_12_48(t *testing.T) {
	r1 := fakerunner.New(fakerunner.Block(fakerunner.Step{}))
	e := newEnv(t, withRunner(r1))
	job := e.create("widgets", jobs.KindFix, 1)
	e.Start()
	waitCalls(t, r1, 1)
	if err := e.Svc.Close(); err != nil {
		t.Fatal(err)
	}

	for restart, wantDelivery := range []int{2, 3} {
		clk := fakeclock.New(epoch)
		r := fakerunner.New(fakerunner.Block(fakerunner.Step{}))
		svc := e.newService(r, clk)
		if err := svc.Start(); err != nil {
			t.Fatal(err)
		}
		waitCalls(t, r, 1)
		c := r.Calls()[0]
		if c.Attempt != 1 || c.Delivery != wantDelivery || c.JobID != job.ID {
			t.Errorf("restart %d: the runner got attempt %d, delivery %d, want attempt 1, delivery %d", restart+1, c.Attempt, c.Delivery, wantDelivery)
		}
		doc, err := svc.Get(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(doc.Attempts) != 1 || doc.Attempts[0].TransientFailures != 0 || doc.Attempts[0].Deliveries != wantDelivery || doc.State != jobs.StateRunning {
			t.Errorf("restart %d: %d attempts, transient_failures %d, deliveries %d, job %s, want 1, 0, %d and running", restart+1, len(doc.Attempts), doc.Attempts[0].TransientFailures, doc.Attempts[0].Deliveries, doc.State, wantDelivery)
		}
		if len(clk.Waits()) != 0 {
			t.Errorf("restart %d: the layer waited %v before the handover", restart+1, clk.Waits())
		}
		if err := svc.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if n := e.count("SELECT count(*) FROM af_attempts WHERE job_id = ?", job.ID); n != 1 {
		t.Errorf("%d attempt rows, want 1", n)
	}
}

// TestTheSweepQueuesEveryQueuedAttemptAtStartAndEveryMinuteAndNoneRunsTwice_TS_12_49
// verifies TS-12-49: attempts that are queued in the file but not in the queue reach
// the runner when the service starts, an attempt that the queue holds already is
// called once, and ticks of the sweep, while attempts are pending or claimed, make
// no second call of any attempt.
// Verifies: 12-REQ-7.6
func TestTheSweepQueuesEveryQueuedAttemptAtStartAndEveryMinuteAndNoneRunsTwice_TS_12_49(t *testing.T) {
	clk := fakeclock.New(epoch)
	r := fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))
	e := newEnv(t, withRunner(r), withClock(clk), withoutEnqueue(), withWorkers(1))
	e.addWorkspace("gadgets")
	var ids []string
	for i, ws := range []string{"widgets", "gadgets", "widgets", "gadgets", "widgets"} {
		ids = append(ids, e.create(ws, jobs.KindFix, i+1).ID)
	}
	if err := e.Svc.Enqueue(ctx, ids[2], 1); err != nil { // the queue holds this one already
		t.Fatal(err)
	}
	e.Start()
	waitCalls(t, r, 1)

	for range 3 { // ticks while one is claimed and the rest are pending
		clk.Advance(time.Minute)
		settle()
	}
	if len(r.Calls()) != 1 {
		t.Fatalf("the runner had %d calls, want only the one it holds: count 1", len(r.Calls()))
	}
	seen := map[string]int{}
	for released := 0; released < len(ids); released++ {
		waitCalls(t, r, released+1)
		seen[r.Calls()[released].JobID]++
		r.Release(released)
		clk.Advance(time.Minute) // a tick between the calls
	}
	for _, id := range ids {
		e.waitState(id, jobs.StateDone)
		if seen[id] != 1 {
			t.Errorf("job %s was called %d times, want once", id, seen[id])
		}
	}
	settle()
	if n := len(r.Calls()); n != len(ids) {
		t.Errorf("the runner had %d calls for %d jobs", n, len(ids))
	}
}

// TestAFailedEnqueueStillAnswers202AndTheSweepGetsTheAttemptToTheRunner_TS_12_21
// verifies TS-12-21: when the queue cannot be given the attempt of a job that was
// stored, the answer is still 202 with the queued job, a WARN names the job, and
// the next tick of the sweep gets the attempt to the runner, once.
// Verifies: 12-REQ-3.7
func TestAFailedEnqueueStillAnswers202AndTheSweepGetsTheAttemptToTheRunner_TS_12_21(t *testing.T) {
	clk := fakeclock.New(epoch)
	r := fakerunner.New()
	var e *env
	var failed atomic.Bool
	failed.Store(true)
	e = newEnv(t, withRunner(r), withClock(clk), func(c *jobs.Config) {
		c.Enqueue = func(ctx context.Context, id string, attempt int) error {
			if failed.CompareAndSwap(true, false) {
				return errors.New("the queue is down")
			}
			return e.Svc.Enqueue(ctx, id, attempt)
		}
	})
	e.Start()
	resp := e.call("POST", "/api/v1/workspaces/widgets/fix", map[string]any{"issue": 42})
	doc := resp.Doc(t)
	if resp.Status != 202 || doc.State != jobs.StateQueued {
		t.Fatalf("status %d, job %s, want 202 and queued\n%s", resp.Status, doc.State, resp.Body)
	}
	warned := false
	for _, rec := range e.Logs.find("WARN", "") {
		warned = warned || rec["job"] == doc.ID
	}
	if !warned {
		t.Errorf("no WARN names the job: %v", e.Logs.records())
	}
	settle()
	if len(r.Calls()) != 0 {
		t.Fatalf("the attempt reached the runner although it was not in the queue")
	}
	clk.Advance(time.Minute)
	e.waitState(doc.ID, jobs.StateDone)
	clk.Advance(time.Minute)
	settle()
	if calls := r.Calls(); len(calls) != 1 || calls[0].JobID != doc.ID || calls[0].Attempt != 1 {
		t.Errorf("the runner had the calls %+v, want attempt 1 of the job, once", calls)
	}
}

// TestATransientBackoffKeepsItsWorkspacesPlaceAndFreesItsSlot_TS_12_26 is the half of
// TS-12-26 about a backoff: with count 1, while attempt A waits out its backoff, the
// next job B of its workspace does not start but a job C of another workspace uses
// the slot, and when the wait is over A is handed over again before B.
// Verifies: 12-REQ-4.4
func TestATransientBackoffKeepsItsWorkspacesPlaceAndFreesItsSlot_TS_12_26(t *testing.T) {
	clk := fakeclock.New(epoch)
	r := fakerunner.New(fakerunner.TransientFailure(sandboxFailure)).Then(fakerunner.Block(fakerunner.Done()))
	e := newEnv(t, withRunner(r), withWorkers(1), withClock(clk))
	e.addWorkspace("gadgets")
	a, b := e.create("widgets", jobs.KindFix, 1), e.create("widgets", jobs.KindFix, 2)
	e.Start()

	waitCalls(t, r, 1)
	if !clk.WaitForWaits(1, 10*time.Second) {
		t.Fatal("A did not go into its backoff")
	}
	settle()
	if len(r.Calls()) != 1 {
		t.Fatalf("B was called during A's backoff: %d calls", len(r.Calls()))
	}
	c := e.create("gadgets", jobs.KindFix, 3)
	waitCalls(t, r, 2)
	if got := r.Calls()[1]; got.JobID != c.ID {
		t.Fatalf("the call during the backoff is for %s, want C (%s): the slot is free", got.JobID, c.ID)
	}

	clk.Advance(30 * time.Second) // A may go again, and needs the slot that C holds
	settle()
	if len(r.Calls()) != 2 {
		t.Fatalf("A was handed over while C held the only slot")
	}
	r.Release(1) // C returns
	waitCalls(t, r, 3)
	if got := r.Calls()[2]; got.JobID != a.ID || got.Delivery != 2 {
		t.Fatalf("the third call is %s delivery %d, want A's second delivery before B", got.JobID, got.Delivery)
	}
	settle()
	if len(r.Calls()) != 3 {
		t.Fatalf("B was called while A held its place")
	}
	r.Release(2) // A returns
	waitCalls(t, r, 4)
	if got := r.Calls()[3]; got.JobID != b.ID {
		t.Errorf("the fourth call is %s, want B (%s)", got.JobID, b.ID)
	}
	r.Release(3)
	e.waitState(b.ID, jobs.StateDone)
}

// TestAHandoverClearsTheHubReferencesOfTheOneBefore checks that the hub field shows
// what the runner of the current delivery has reported and not what an earlier one
// did: the references of a delivery that ended in a transient failure are gone when the
// attempt is handed over again.
// Verifies: 12-REQ-2.6
func TestAHandoverClearsTheHubReferencesOfTheOneBefore(t *testing.T) {
	clk := fakeclock.New(epoch)
	r := fakerunner.New(
		fakerunner.Do(func(_ context.Context, req jobs.Request) (jobs.Result, error) {
			req.Recorder.Hub("session-1", "run-1", "box-1")
			return jobs.Result{}, jobs.Transient(sandboxFailure)
		}),
		fakerunner.Block(fakerunner.Step{}),
	)
	e := newEnv(t, withRunner(r), withClock(clk))
	job := e.create("widgets", jobs.KindFix, 1)
	e.Start()
	clk.WaitForWaits(1, 10*time.Second)
	if doc := e.get(job.ID); doc.Hub != nil {
		t.Errorf("hub = %+v while the attempt waits to be handed over, want null", doc.Hub)
	}
	clk.Advance(30 * time.Second)
	waitCalls(t, r, 2)
	if doc := e.get(job.ID); doc.State != jobs.StateRunning || doc.Hub != nil {
		t.Errorf("job %s with hub %+v in the second delivery, before it reported, want running and null", doc.State, doc.Hub)
	}
	r.ReleaseAll()
	e.waitRest(job.ID)
}
