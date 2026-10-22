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

// TestAServiceThatIsStoppingGrantsNoSlotAndDoesNotCallARunnerForAnAttemptItMustStop
// checks the two places where a service that stops must not go on: it grants no slot,
// whether one is free or not, and an attempt that was claimed as it began to stop is told
// of the shutdown before the runner is called, and the runner is not called.
// Verifies: 12-REQ-7.3
func TestAServiceThatIsStoppingGrantsNoSlotAndDoesNotCallARunnerForAnAttemptItMustStop(t *testing.T) {
	var calls int
	svc := internalService(t, funcRunner(func(ctx context.Context, req Request) (Result, error) {
		calls++
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
	_, out, cause, _, err := svc.invoke(ctx, attemptPayload{JobID: id, Attempt: 1})
	if err != nil || !errors.Is(cause, ErrShutdown) || IsCancel(cause) || !errors.Is(out.err, ErrShutdown) {
		t.Errorf("invoke after the stop: cause %v, err %v, out %v, want the shutdown, which is not a cancel", cause, err, out.err)
	}
	if calls != 0 {
		t.Errorf("the runner was called %d times for an attempt that had to stop", calls)
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

// TestACancelBeforeTheRunnerIsCalledStopsTheAttemptWithoutCallingIt checks the window
// between the claim of an attempt and the call of its runner: a job cancelled in it is
// seen when the attempt is made known to the layer, the runner is not called, and the
// attempt is recorded cancelled with an envelope of the category cancelled.
// Verifies: 12-REQ-8.1
func TestACancelBeforeTheRunnerIsCalledStopsTheAttemptWithoutCallingIt(t *testing.T) {
	var calls int
	svc := internalService(t, funcRunner(func(context.Context, Request) (Result, error) {
		calls++
		return Result{}, nil
	}))
	id, err := svc.store.CreateJob(ctx, newJob(KindFix, 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.store.Claim(ctx, id, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.cancel(ctx, id, StateRunning, ErrCancelled); err != nil { // the attempt is running, and no runner has it yet
		t.Fatal(err)
	}
	req, out, cause, overran, err := svc.invoke(ctx, attemptPayload{JobID: id, Attempt: 1})
	if err != nil || overran || calls != 0 || !IsCancel(cause) {
		t.Fatalf("invoke: cause %v, err %v, overran %v, %d calls, want the cancel and no call of the runner", cause, err, overran, calls)
	}
	if err := svc.finish(ctx, req, out, false, cause); err != nil {
		t.Fatal(err)
	}
	doc, err := svc.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	a := doc.Attempts[0]
	if doc.State != StateCancelled || a.State != AttemptCancelled || a.Error == nil || a.Error.Category != "cancelled" {
		t.Errorf("job %s, attempt %s %+v, want cancelled with an error of the category cancelled", doc.State, a.State, a.Error)
	}
}

// TestOfTwoActionsTakenOnTheSameStateExactlyOneIsApplied_TS_12_55 is TS-12-55 without
// luck: two actions are held after each has seen the state of the job, and let go
// together, so both act on the same state, whatever the actions are. Exactly one is
// applied; the other finds the state gone and is a *StateError. A cancel, an answer and a
// retry are conditional on the state they were taken on, and not on any state that an action
// of the same kind could start from.
// Verifies: 12-REQ-8.6
func TestOfTwoActionsTakenOnTheSameStateExactlyOneIsApplied_TS_12_55(t *testing.T) {
	type action struct {
		name string
		do   func(svc *Service, id string) error
	}
	cancel := action{"cancel", func(svc *Service, id string) error { _, err := svc.Cancel(ctx, id); return err }}
	answer := action{"answer", func(svc *Service, id string) error {
		a := "go on"
		_, err := svc.Answer(ctx, id, "user-2", AnswerRequest{Answer: &a})
		return err
	}}
	retry := action{"retry", func(svc *Service, id string) error { _, err := svc.Retry(ctx, id, "user-2"); return err }}

	for _, tc := range []struct {
		state State
		pairs [][2]action
	}{
		{StateQueued, [][2]action{{cancel, cancel}}},
		{StateRunning, [][2]action{{cancel, cancel}}},
		{StateBlocked, [][2]action{{cancel, cancel}, {answer, answer}, {cancel, answer}, {answer, cancel}}},
		{StateFailed, [][2]action{{retry, retry}}},
	} {
		for _, pair := range tc.pairs {
			t.Run(fmt.Sprintf("%s: %s and %s", tc.state, pair[0].name, pair[1].name), func(t *testing.T) {
				// The service is not started: nothing but the two actions moves the job.
				db := openDB(t)
				svc, err := New(Config{DB: db, Subscriptions: anySubscription{}, Workers: 1, Runner: funcRunner(func(context.Context, Request) (Result, error) { return Result{}, nil }),
					AttemptsDir: filepath.Join(t.TempDir(), "attempts")})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { svc.Close() })
				id, err := svc.store.CreateJob(ctx, newJob(KindFix, 1))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec("UPDATE af_jobs SET state = ? WHERE id = ?", string(tc.state), id); err != nil {
					t.Fatal(err)
				}
				if tc.state == StateBlocked {
					if _, err := db.Exec("UPDATE af_attempts SET state = 'needs_human', question = 'Which?', options = '[]' WHERE job_id = ?", id); err != nil {
						t.Fatal(err)
					}
				}
				if tc.state == StateFailed {
					if _, err := db.Exec("UPDATE af_jobs SET finished_at = '2026-10-22T08:00:00.000Z' WHERE id = ?", id); err != nil {
						t.Fatal(err)
					}
				}

				// Each action waits, once it has seen the state, for the other to have seen it.
				var seen sync.WaitGroup
				seen.Add(2)
				svc.store.SetFault(func(op string) error {
					if op == OpObserved {
						seen.Done()
						seen.Wait()
					}
					return nil
				})
				errs := make([]error, 2)
				var done sync.WaitGroup
				for i, a := range pair {
					done.Add(1)
					go func() {
						defer done.Done()
						errs[i] = a.do(svc, id)
					}()
				}
				done.Wait()

				applied := 0
				for _, err := range errs {
					var state *StateError
					switch {
					case err == nil:
						applied++
					case !errors.As(err, &state):
						t.Fatalf("an action failed with %v, want it applied or a *StateError", err)
					}
				}
				if applied != 1 {
					t.Errorf("%s and %s on the same %s job: %d applied (%v), want exactly one", pair[0].name, pair[1].name, tc.state, applied, errs)
				}
				doc, err := svc.Get(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if len(doc.Attempts) > 2 {
					t.Errorf("%d attempts, want at most one new", len(doc.Attempts))
				}
			})
		}
	}
}
