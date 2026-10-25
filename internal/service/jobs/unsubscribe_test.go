package jobs_test

import (
	"context"
	"errors"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestUnsubscribe_CancelsTheOpenJobsWaitsForTheirRunnersAndThenRemoves(t *testing.T) {
	r := fakerunner.New(question(), fakerunner.BlockIgnoringContext(fakerunner.Done()))
	e := newEnv(t, withRunner(r))
	blocked := e.create("widgets", 1, "")
	e.start()
	e.waitState(blocked.ID, jobs.StateBlocked)
	running := e.create("widgets", 2, "")
	e.waitCalls(2)
	queued := e.create("widgets", 3, "")
	other := e.create("gadgets", 4, "")
	removing, removed := make(chan struct{}), make(chan error, 1)
	go func() {
		removed <- e.Svc.Unsubscribe(ctx, "widgets", func() error { close(removing); e.Subs.set("widgets", ""); return nil })
	}()
	testutil.Eventually(t, "the runner to be told", func() bool { return r.Calls()[1].Cause() != nil })
	if cause := r.Calls()[1].Cause(); !jobs.IsCancel(cause) || !errors.Is(cause, jobs.ErrUnsubscribed) {
		t.Errorf("the runner was stopped with %v, want the unsubscribe, which is a cancel", cause)
	}
	// While the runner has not returned, the workspace takes no job, answer or retry, and is not removed yet.
	if _, err := e.Svc.Create(ctx, jobs.CreateRequest{Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: []byte("99")}, Source: jobs.Source{Kind: "api", By: "u"}}); !errors.Is(err, jobs.ErrNotSubscribed) {
		t.Errorf("a create during the unsubscribe: %v, want ErrNotSubscribed", err)
	}
	for _, d := range []jobs.Doc{blocked, running, queued} {
		if doc := e.waitState(d.ID, jobs.StateCancelled); *doc.CancelCause != "unsubscribed" {
			t.Errorf("%s is cancelled by %s, want the unsubscribe", d.ID, *doc.CancelCause)
		}
	}
	select {
	case <-removing:
		t.Fatal("the subscription was removed before the running attempt returned")
	default:
	}
	r.ReleaseAll()
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
	if a := e.waitRest(running.ID).Attempts[0]; a.State != jobs.AttemptCancelled {
		t.Errorf("the running attempt is %s, want cancelled", a.State)
	}
	if d := e.get(other.ID); d.State == jobs.StateCancelled {
		t.Error("a job of another workspace was cancelled")
	}
	if e.lastState(queued.ID) != "cancelled:true" || len(e.get(queued.ID).Attempts) != 1 {
		t.Error("the queued job's history is not that of a cancel")
	}
}

func TestUnsubscribe_ClearsTheMarkWhenTheRemovalFailsAndStopsForItsContext(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.BlockIgnoringContext(fakerunner.Done()))))
	e.start()
	job := e.create("widgets", 1, "")
	e.waitCalls(1)
	cut, cancel := context.WithCancel(ctx)
	go func() {
		testutil.Eventually(t, "the cancel", func() bool { return e.get(job.ID).State == jobs.StateCancelled })
		cancel()
	}()
	if err := e.Svc.Unsubscribe(cut, "widgets", func() error { t.Error("removed"); return nil }); !errors.Is(err, context.Canceled) {
		t.Errorf("an unsubscribe whose context ends while it waits: %v", err)
	}
	refused := errors.New("removal refused")
	e.Runner.ReleaseAll()
	if err := e.Svc.Unsubscribe(ctx, "widgets", func() error { return refused }); !errors.Is(err, refused) {
		t.Errorf("Unsubscribe = %v, want the removal's error", err)
	}
	if _, err := e.Svc.Create(ctx, jobs.CreateRequest{Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: []byte("2")}, Source: jobs.Source{Kind: "api", By: "u"}}); err != nil {
		t.Errorf("a create once the unsubscribe has failed: %v, want the mark cleared", err)
	}
}

func TestUnsubscribe_WaitsForTheFirstTryOfEachCleanupUntilTheGraceIsOver(t *testing.T) {
	c := &fakeCleaner{hold: make(chan struct{})}
	e := newEnv(t, withRunner(fakerunner.New().Then(needsHuman())), withCleaner(c))
	e.start()
	cleaned := e.waitState(e.create("widgets", 42, "af:fix").ID, jobs.StateBlocked)
	stuck := e.waitState(e.create("gadgets", 7, "af:fix").ID, jobs.StateBlocked)
	// The try of widgets ends; the unsubscribe removes the subscription once it has.
	removed := make(chan error, 1)
	go func() { removed <- e.Svc.Unsubscribe(ctx, "widgets", func() error { return nil }) }()
	testutil.Eventually(t, "the try", func() bool { return len(c.Tries()) == 1 })
	select {
	case err := <-removed:
		t.Fatalf("the unsubscribe returned (%v) before the try ended", err)
	default:
	}
	close(c.hold)
	if err := <-removed; err != nil || e.cleanup(cleaned.ID) != "done" {
		t.Fatalf("Unsubscribe = %v with cleanup %s, want it done before the removal", err, e.cleanup(cleaned.ID))
	}
	// The try of gadgets does not end: the unsubscribe stops waiting once the grace is over.
	c.mu.Lock()
	c.hold = make(chan struct{})
	c.mu.Unlock()
	waits := len(e.Clock.Waits())
	go func() { removed <- e.Svc.Unsubscribe(ctx, "gadgets", func() error { return nil }) }()
	testutil.Eventually(t, "the second try", func() bool { return len(c.Tries()) == 2 })
	e.waitWaits(waits + 2) // the try's minute and the unsubscribe's grace
	e.Clock.Advance(cancelGrace)
	if err := <-removed; err != nil || e.cleanup(stuck.ID) != "pending" {
		t.Errorf("Unsubscribe = %v with cleanup %s, want it to return with the cleanup pending", err, e.cleanup(stuck.ID))
	}
}

func TestUnsubscribe_AJobThatChangesStateAsItIsCancelledIsLookedAtAgain(t *testing.T) {
	e := newEnv(t, withoutEnqueue())
	claimed := e.create("widgets", 1, "")
	ended := e.create("widgets", 2, "")
	once := map[string]bool{}
	e.Svc.AfterLook(func(id string, state jobs.State) {
		if once[id] {
			return
		}
		once[id] = true
		switch id {
		case claimed.ID: // claimed between the look and the cancel: the cancel finds it running
			testutil.Exec(t, e.DB, "UPDATE af_jobs SET state = 'running' WHERE id = ?", id)
		case ended.ID: // cancelled by a person meanwhile: it is left
			if _, err := e.Svc.Cancel(ctx, id); err != nil {
				t.Error(err)
			}
		}
	})
	if err := e.Svc.Unsubscribe(ctx, "widgets", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if d := e.get(claimed.ID); d.State != jobs.StateCancelled || *d.CancelCause != "unsubscribed" || len(e.states(claimed.ID)) != 2 {
		t.Errorf("the claimed job is %s (%v) with the states %v, want it cancelled once, from running", d.State, d.CancelCause, e.states(claimed.ID))
	}
	if d := e.get(ended.ID); d.State != jobs.StateCancelled || *d.CancelCause != "cancelled" {
		t.Errorf("the job cancelled meanwhile is %s by %v, want the person's cancel kept", d.State, d.CancelCause)
	}
}
