package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// funcRunner is a Runner that is a function.
type funcRunner func(ctx context.Context, req Request) (Result, error)

func (f funcRunner) Run(ctx context.Context, req Request) (Result, error) { return f(ctx, req) }

// anySubscription is a subscription store in which every workspace is subscribed.
type anySubscription struct{}

func (anySubscription) Get(_ context.Context, slug string) (store.Subscription, error) {
	return store.Subscription{Slug: slug, TokenState: store.TokenOK}, nil
}

// failedEnvelope is the JSON of a failed envelope of the category.
func failedEnvelope(category string, retryable bool) json.RawMessage {
	raw, err := json.Marshal(toolio.Envelope{
		SchemaVersion: toolio.SchemaVersion, Tool: "fix", Status: toolio.StatusFailed, ExitCode: 1,
		Error: &toolio.ErrorInfo{Category: category, Message: "failed", Retryable: retryable},
	})
	if err != nil {
		panic(err)
	}
	return raw
}

// internalService is a service over a file with the runner, started.
func internalService(t *testing.T, runner Runner) *Service {
	t.Helper()
	svc, err := New(Config{
		DB: openDB(t), Subscriptions: anySubscription{}, Logger: nil, Workers: 1, Runner: runner,
		AttemptsDir: filepath.Join(t.TempDir(), "attempts"), PollInterval: 5 * time.Millisecond, RetryDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Close() })
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	return svc
}

// restOf waits until the job is at rest.
func restOf(t *testing.T, svc *Service, id string) Doc {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		doc, err := svc.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		rest := doc.State == StateBlocked || doc.State == StateDone || doc.State == StateFailed || doc.State == StateCancelled
		for _, a := range doc.Attempts {
			rest = rest && a.State != AttemptRunning && a.State != AttemptQueued
		}
		if rest {
			return doc
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s did not come to rest: %s", id, doc.State)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestAnAutomaticAttemptHasTheSameContextAndAnswersAndARoundRestartsAtAHumanAction_TS_12_40
// is the rest of TS-12-40: the automatic attempt that follows a failure of the brain
// is given the context and the answers of the job, and each action of a person (an
// answer, a retry) starts a round of its own, with a full set of attempts.
// Verifies: 12-REQ-6.5
func TestAnAutomaticAttemptHasTheSameContextAndAnswersAndARoundRestartsAtAHumanAction_TS_12_40(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []Request
		script   []Result
	)
	needsHuman, err := json.Marshal(toolio.Envelope{
		SchemaVersion: toolio.SchemaVersion, Tool: "fix", Status: toolio.StatusNeedsHuman, ExitCode: 3,
		NeedsHuman: &toolio.NeedsHuman{Question: "Which?", Options: []toolio.Option{{ID: "A", Label: "one"}, {ID: "B", Label: "two"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	script = []Result{{Envelope: needsHuman}}
	for range 6 {
		script = append(script, Result{Envelope: failedEnvelope("brain", true)})
	}
	svc := internalService(t, funcRunner(func(_ context.Context, req Request) (Result, error) {
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, req)
		return script[len(requests)-1], nil
	}))

	issue := int64(1)
	job := newJob(KindFix, issue)
	job.Context = []string{"first note", "second note"}
	id, err := svc.store.CreateJob(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Enqueue(ctx, id, 1); err != nil {
		t.Fatal(err)
	}
	if doc := restOf(t, svc, id); doc.State != StateBlocked {
		t.Fatalf("job %s, want blocked", doc.State)
	}

	// A person answers: attempt 2, a round of its own.
	b := "B"
	answer := &Answer{Question: "Which?", Options: []Option{{ID: "A", Label: "one"}, {ID: "B", Label: "two"}}, Option: &b, Text: "two"}
	if n, err := svc.store.Requeue(ctx, id, StateBlocked, Trigger{Kind: TriggerAnswer, By: "user-2"}, answer); err != nil || n != 2 {
		t.Fatalf("Requeue = %d, %v, want attempt 2", n, err)
	}
	if err := svc.Enqueue(ctx, id, 2); err != nil {
		t.Fatal(err)
	}
	doc := restOf(t, svc, id)
	if doc.State != StateFailed || len(doc.Attempts) != 3 {
		t.Fatalf("job %s with %d attempts, want failed after the answer and one automatic attempt", doc.State, len(doc.Attempts))
	}
	triggers := func(doc Doc) []TriggerKind {
		var out []TriggerKind
		for _, a := range doc.Attempts {
			out = append(out, a.Trigger.Kind)
		}
		return out
	}
	if got := triggers(doc); !slices.Equal(got, []TriggerKind{TriggerCreate, TriggerAnswer, TriggerAuto}) {
		t.Errorf("triggers = %v, want create, answer, auto", got)
	}

	mu.Lock()
	if len(requests) != 3 {
		t.Fatalf("%d calls, want 3", len(requests))
	}
	for i, req := range requests[1:] {
		if !slices.Equal(req.Context, job.Context) {
			t.Errorf("attempt %d: context = %v, want %v", req.Attempt, req.Context, job.Context)
		}
		if len(req.Answers) != 1 || req.Answers[0].Question != "Which?" || req.Answers[0].Text != "two" || len(req.Answers[0].Options) != 2 ||
			req.Answers[0].Option == nil || *req.Answers[0].Option != "B" {
			t.Errorf("call %d (attempt %d): answers = %+v, want the one answer with its options", i+2, req.Attempt, req.Answers)
		}
	}
	if len(requests[0].Answers) != 0 {
		t.Errorf("attempt 1 was given the answers %+v", requests[0].Answers)
	}
	mu.Unlock()

	// A retry starts a round again: two more attempts, then failed.
	if n, err := svc.store.Requeue(ctx, id, StateFailed, Trigger{Kind: TriggerRetry, By: "user-2"}, nil); err != nil || n != 4 {
		t.Fatalf("Requeue = %d, %v, want attempt 4", n, err)
	}
	if err := svc.Enqueue(ctx, id, 4); err != nil {
		t.Fatal(err)
	}
	doc = restOf(t, svc, id)
	if got := triggers(doc); doc.State != StateFailed || !slices.Equal(got, []TriggerKind{TriggerCreate, TriggerAnswer, TriggerAuto, TriggerRetry, TriggerAuto}) {
		t.Errorf("after a retry: job %s, triggers %v, want create, answer, auto, retry, auto and failed", doc.State, got)
	}
	mu.Lock()
	defer mu.Unlock()
	if last := requests[len(requests)-1]; len(last.Answers) != 1 || fmt.Sprint(last.Context) != fmt.Sprint(job.Context) {
		t.Errorf("the attempt after the retry was given answers %+v and context %v, want the job's", last.Answers, last.Context)
	}
}

// TestAServiceThatIsStoppingGrantsNoSlotAndCancelsWhatItIsToRun checks the two places
// where a service that stops must not go on: it grants no slot, whether one is free or
// not, and an attempt that it was about to call the runner for, after it began to stop,
// is told of the shutdown before the runner is called.
// Verifies: 12-REQ-7.3
func TestAServiceThatIsStoppingGrantsNoSlotAndCancelsWhatItIsToRun(t *testing.T) {
	var cause error
	called := make(chan struct{})
	svc := internalService(t, funcRunner(func(ctx context.Context, req Request) (Result, error) {
		cause = context.Cause(ctx)
		close(called)
		return Result{}, nil
	}))
	if !svc.acquire() {
		t.Fatal("a service that is running granted no slot")
	}
	svc.release()

	id, err := svc.store.CreateJob(ctx, newJob(KindFix, 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.Claim(ctx, id, 1); err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	for range 200 { // a select between a free slot and the stop would choose either
		if svc.acquire() {
			t.Fatal("a service that is stopping granted a slot that was free")
		}
	}
	if _, _, got, err := svc.invoke(ctx, attemptPayload{JobID: id, Attempt: 1}); err != nil || !errors.Is(got, ErrShutdown) {
		t.Errorf("invoke after the stop: cause %v, err %v, want the shutdown", got, err)
	}
	<-called
	if !errors.Is(cause, ErrShutdown) || IsCancel(cause) {
		t.Errorf("the runner's context had the cause %v when it was called, want a shutdown and not a cancel", cause)
	}
}

// TestAnAttemptOfAFinishedJobDoesNotKeepTheNextOneWaiting checks that the first open
// attempt of a workspace is looked for among the attempts of jobs that are open: a job
// that is finished and has an attempt that is still marked queued (a crash can leave
// one) does not make the attempts after it hand their items back.
// Verifies: 12-REQ-4.3
func TestAnAttemptOfAFinishedJobDoesNotKeepTheNextOneWaiting(t *testing.T) {
	db := openDB(t)
	st := NewStore(db, newTestClock().Now)
	stale, err := st.CreateJob(ctx, newJob(KindFix, 1))
	if err != nil {
		t.Fatal(err)
	}
	next, err := st.CreateJob(ctx, newJob(KindFix, 2))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE af_jobs SET state = 'cancelled', finished_at = '2026-10-22T08:00:00.000Z' WHERE id = ?", stale); err != nil {
		t.Fatal(err)
	}
	if what, err := st.Plan(ctx, next, 1); err != nil || what != dispClaim {
		t.Errorf("Plan = %v, %v, want to claim: the attempt of the cancelled job is not in the way", what, err)
	}
	if what, err := st.Plan(ctx, stale, 1); err != nil || what != dispAck {
		t.Errorf("Plan of the attempt of the cancelled job = %v, %v, want to acknowledge", what, err)
	}
}
