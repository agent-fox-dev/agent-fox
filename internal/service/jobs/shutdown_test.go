package jobs_test

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestClose_StopsTheRunnersForAShutdownAndDiscardsWhatTheyReturn(t *testing.T) {
	r := fakerunner.New(
		fakerunner.Block(fakerunner.Done()),
		fakerunner.Block(fakerunner.Fail(errors.New("late failure"))),
		fakerunner.Block(fakerunner.Panic("late bang")),
	)
	e := newEnv(t, withRunner(r), withWorkers(3))
	e.Subs.set("sprockets", "ok")
	ids := []string{e.create("widgets", 1, "").ID, e.create("gadgets", 2, "").ID, e.create("sprockets", 3, "").ID}
	waiting := e.create("widgets", 4, "")
	e.start()
	e.waitCalls(3)
	e.close()
	for i, c := range r.Calls() {
		if cause := c.Cause(); jobs.IsCancel(cause) || !errors.Is(cause, jobs.ErrShutdown) {
			t.Errorf("call %d was stopped with %v, want a shutdown, which is not a cancel", i, cause)
		}
	}
	for _, id := range ids {
		doc := e.get(id)
		if doc.State != jobs.StateRunning || doc.Attempts[0].State != jobs.AttemptRunning || doc.Attempts[0].Status != nil || doc.FinishedAt != nil {
			t.Errorf("%s: job %s, attempt %+v, want both left running", id, doc.State, doc.Attempts[0])
		}
		if _, err := os.Stat(e.file(id, 1, "envelope.json")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: an envelope was stored after the shutdown", id)
		}
		if e.lastState(id) != "running:false" || e.events(id)[len(e.events(id))-1].Type != jobs.EventAttemptStart {
			t.Errorf("%s: the log goes on past the start of the attempt: %v", id, e.states(id))
		}
	}
	if d := e.get(waiting.ID); d.State != jobs.StateQueued || d.Attempts[0].Deliveries != 0 || len(r.Calls()) != 3 {
		t.Errorf("the job that waited is %s with %d deliveries, want it queued and never handed over", d.State, d.Attempts[0].Deliveries)
	}
	if recs := e.Log.Find("ERROR", ""); len(recs) != 0 {
		t.Errorf("ERROR records = %v, want none for what was discarded", recs)
	}
}

func TestClose_WaitsUpTo30SecondsForARunnerThatDoesNotReturn(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.BlockIgnoringContext(fakerunner.Done()))))
	e.create("widgets", 1, "")
	e.start()
	e.waitCalls(1)
	closed := make(chan error, 1)
	e.closed = true
	go func() { closed <- e.Svc.Close() }()
	e.waitWaits(1)
	if got := e.Clock.Waits(); got[0] != 30*time.Second {
		t.Fatalf("waits = %v, want 30 s for the runners", got)
	}
	select {
	case err := <-closed:
		t.Fatalf("Close returned (%v) while its runner had not, before the 30 seconds", err)
	default:
	}
	e.Clock.Advance(30 * time.Second)
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if warned := e.Log.Find("WARN", "did not return"); len(warned) != 1 {
		t.Errorf("warnings = %v, want one for the runner", e.Log.Records())
	}
}

func TestClose_LeavesAnAttemptThatWaitsOutABackoffQueuedForTheRestart(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.TransientFailure(sandboxFailure))))
	job := e.create("widgets", 1, "")
	e.start()
	e.waitWaits(1)
	e.close()
	if d := e.get(job.ID); d.State != jobs.StateQueued || d.Attempts[0].TransientFailures != 1 {
		t.Fatalf("job %s, attempt %+v, want it queued after one transient failure", d.State, d.Attempts[0])
	}
	r := fakerunner.New()
	e.restart(r)
	if d := e.waitState(job.ID, jobs.StateDone); d.Attempts[0].Deliveries != 2 || len(r.Calls()) != 1 {
		t.Errorf("after the restart: attempt %+v, %d calls, want the second delivery", d.Attempts[0], len(r.Calls()))
	}
}

func TestRestart_HandsTheInterruptedAttemptOverAgainWithOneMoreDelivery(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Block(fakerunner.Step{}))))
	job := e.create("widgets", 1, "")
	e.start()
	e.waitCalls(1)
	for _, delivery := range []int{2, 3} {
		e.restart(fakerunner.New(fakerunner.Block(fakerunner.Step{})))
		e.waitCalls(1)
		c := e.Runner.Calls()[0]
		doc := e.get(job.ID)
		if c.JobID != job.ID || c.Attempt != 1 || c.Delivery != delivery || len(doc.Attempts) != 1 || doc.Attempts[0].Deliveries != delivery || doc.Attempts[0].TransientFailures != 0 {
			t.Errorf("restart: call %+v, attempts %+v, want attempt 1 handed over with delivery %d", c.Request, doc.Attempts, delivery)
		}
		for _, w := range e.Clock.Waits() {
			if w != 30*time.Second {
				t.Errorf("the layer waited %v, want only the waits of the shutdowns", w)
			}
		}
	}
	if n := testutil.Count(t, e.DB, "af_attempts", "job_id = ?", job.ID); n != 1 {
		t.Errorf("%d attempts, want 1", n)
	}
}

func TestRestart_AJobMadeBeforeIsReadAndRunAfter(t *testing.T) {
	e := newEnv(t)
	job := e.create("widgets", 1, "af:fix") // made, and never run, by a layer that then stops
	e.close()
	r := fakerunner.New()
	e.restart(r)
	if d := e.get(job.ID); d.ID != job.ID || d.Workspace != "widgets" || *d.RequestLabel != "af:fix" {
		t.Errorf("after the restart the job reads %+v", d)
	}
	if d := e.waitState(job.ID, jobs.StateDone); len(d.Attempts) != 1 || len(r.Calls()) != 1 || r.Calls()[0].JobID != job.ID {
		t.Errorf("after the restart: %d attempts, %d calls, want the job run once", len(d.Attempts), len(r.Calls()))
	}
}
