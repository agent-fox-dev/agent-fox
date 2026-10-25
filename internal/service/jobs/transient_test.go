package jobs_test

import (
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestTransient_MarksAnErrorAndKeepsWhatItWraps(t *testing.T) {
	err := jobs.Transient(sandboxFailure)
	if !jobs.IsTransient(err) || jobs.IsTransient(sandboxFailure) || jobs.Transient(nil) != nil || err.Error() != sandboxFailure.Error() {
		t.Errorf("Transient(%v) = %v", sandboxFailure, err)
	}
	if f, ok := errors.AsType[*toolio.Failure](err); !ok || f != sandboxFailure {
		t.Errorf("the failure in a transient error is not found: %v", f)
	}
	// A runner tells a cancel, which an unsubscribe is too, from a shutdown by the cause of its context.
	for cause, want := range map[error]bool{jobs.ErrCancelled: true, jobs.ErrUnsubscribed: true, jobs.ErrShutdown: false, nil: false} {
		if jobs.IsCancel(cause) != want {
			t.Errorf("IsCancel(%v) = %v", cause, !want)
		}
	}
}

func TestTransient_HandsTheSameAttemptOverAgainAfter30SecondsAndThen2Minutes(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.TransientFailure(sandboxFailure), fakerunner.TransientFailure(sandboxFailure), fakerunner.Done())))
	job := e.create("widgets", 1, "")
	e.start()
	between := func(waits []time.Duration, failures int) {
		t.Helper()
		e.waitWaits(len(waits))
		if got := e.Clock.Waits(); !slices.Equal(got, waits) {
			t.Fatalf("waits = %v, want %v", got, waits)
		}
		doc := e.get(job.ID)
		a := doc.Attempts[0]
		if doc.State != jobs.StateQueued || len(doc.Attempts) != 1 || a.State != jobs.AttemptQueued || a.TransientFailures != failures || a.Status != nil || doc.FinishedAt != nil {
			t.Errorf("during the wait: job %s, attempts %+v, want the one attempt queued with %d failures and no envelope", doc.State, doc.Attempts, failures)
		}
		if _, err := os.Stat(e.file(job.ID, 1, "envelope.json")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("an envelope was stored between the deliveries: %v", err)
		}
	}
	between([]time.Duration{30 * time.Second}, 1)
	e.Clock.Advance(29 * time.Second)
	if e.Clock.Pending() != 1 || len(e.Runner.Calls()) != 1 {
		t.Fatal("the attempt was handed over again before its 30 seconds were up")
	}
	e.Clock.Advance(time.Second)
	between([]time.Duration{30 * time.Second, 2 * time.Minute}, 2)
	e.Clock.Advance(2 * time.Minute)
	doc := e.waitRest(job.ID)
	if doc.State != jobs.StateDone || len(doc.Attempts) != 1 || doc.Attempts[0].TransientFailures != 2 || doc.Attempts[0].Deliveries != 3 {
		t.Errorf("job %s, attempts %+v, want done in one attempt of three deliveries", doc.State, doc.Attempts)
	}
	var deliveries []int
	for _, c := range e.Runner.Calls() {
		deliveries = append(deliveries, c.Attempt*10+c.Delivery)
	}
	if !slices.Equal(deliveries, []int{11, 12, 13}) {
		t.Errorf("the calls were attempt*10+delivery %v, want attempt 1 with deliveries 1, 2 and 3", deliveries)
	}
	if states := e.states(job.ID); !slices.Equal(states, []string{"queued:false", "running:false", "queued:false", "running:false", "queued:false", "running:false", "done:true"}) {
		t.Errorf("job_state events %v", states)
	}
}

func TestTransient_TheThirdFailureFailsTheAttemptWithItsCategoryAndNoAutomaticAttempt(t *testing.T) {
	// A transient failure of the brain is not a reason for an automatic attempt either.
	failure := &toolio.Failure{Category: toolio.CategoryBrain, Message: "transient brain", Retryable: true}
	e := newEnv(t, withRunner(fakerunner.New().Then(fakerunner.TransientFailure(failure))))
	job := e.create("widgets", 1, "")
	e.start()
	e.waitWaits(1)
	e.Clock.Advance(30 * time.Second)
	e.waitWaits(2)
	e.Clock.Advance(2 * time.Minute)
	doc := e.waitRest(job.ID)
	if doc.State != jobs.StateFailed || len(doc.Attempts) != 1 || doc.Attempts[0].State != jobs.AttemptFailed || doc.Attempts[0].TransientFailures != 3 {
		t.Fatalf("job %s, attempts %+v, want failed in one attempt with 3 transient failures", doc.State, doc.Attempts)
	}
	if f := doc.Result.Error; f.Category != "brain" || f.Message != "transient brain" || !f.Retryable || doc.Result.ExitCode != 1 {
		t.Errorf("result = %+v, want the failure's category, message and flag", doc.Result)
	}
	if got := e.Clock.Waits(); len(got) != 2 {
		t.Errorf("waits = %v, want none after the third", got)
	}
}

func TestTransient_ABackoffFreesItsSlotAndKeepsItsWorkspacesPlace(t *testing.T) {
	r := fakerunner.New(fakerunner.TransientFailure(sandboxFailure)).Then(fakerunner.Block(fakerunner.Done()))
	e := newEnv(t, withRunner(r), withWorkers(1))
	a, b := e.create("widgets", 1, ""), e.create("widgets", 2, "")
	e.start()
	e.waitWaits(1)
	// The only slot is free during the wait: another workspace's job takes it.
	c := e.create("gadgets", 3, "")
	e.waitCalls(2)
	if got := r.Calls()[1].JobID; got != c.ID {
		t.Fatalf("the call during the backoff is for %s, want %s", got, c.ID)
	}
	e.Clock.Advance(30 * time.Second) // A may go again, once C gives the slot back
	r.Release(1)
	e.waitCalls(3)
	if got := r.Calls()[2]; got.JobID != a.ID || got.Delivery != 2 {
		t.Fatalf("the third call is %s delivery %d, want A's second delivery, before B", got.JobID, got.Delivery)
	}
	r.Release(2)
	e.waitCalls(4)
	if got := r.Calls()[3].JobID; got != b.ID {
		t.Errorf("the fourth call is %s, want B", got)
	}
	r.Release(3)
	e.waitState(b.ID, jobs.StateDone)
	if r.MaxConcurrent() != 1 {
		t.Errorf("%d calls at once with one slot", r.MaxConcurrent())
	}
	if n := testutil.Count(t, e.DB, "af_attempts", "job_id = ?", a.ID); n != 1 {
		t.Errorf("A has %d attempts, want 1", n)
	}
}
