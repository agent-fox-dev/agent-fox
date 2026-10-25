package jobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestAnswer_QueuesTheNextAttemptAndItsRunnerIsGivenEveryAnswerSoFarInOrder(t *testing.T) {
	r := fakerunner.New(
		fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("First?", "yes", "no"))),
		fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Second?", "left", "right", "up"))),
	)
	e := newEnv(t, withRunner(r))
	job, err := e.Svc.Create(ctx, jobs.CreateRequest{
		Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage("42"), Context: []string{"first note", "second note"}},
		Source: jobs.Source{Kind: "label", By: "poll"}, RequestLabel: "af:fix",
	})
	if err != nil {
		t.Fatal(err)
	}
	e.start()
	e.waitState(job.ID, jobs.StateBlocked)
	doc, err := e.Svc.Answer(ctx, job.ID, "user-2", jobs.AnswerRequest{Answer: ptr("  free text \n")})
	if err != nil || doc.State != jobs.StateQueued || doc.FinishedAt != nil || doc.Blocked != nil || doc.CurrentAttempt != 2 {
		t.Fatalf("Answer = %+v, %v, want the job queued with attempt 2", doc, err)
	}
	testutil.Eventually(t, "the second question", func() bool { d := e.get(job.ID); return d.State == jobs.StateBlocked && d.CurrentAttempt == 2 })
	if _, err := e.Svc.Answer(ctx, job.ID, "user-3", jobs.AnswerRequest{Option: ptr("B")}); err != nil {
		t.Fatal(err)
	}
	d := e.waitState(job.ID, jobs.StateDone)
	if a := d.Attempts[1]; a.Trigger != (jobs.Trigger{Kind: jobs.TriggerAnswer, By: "user-2"}) || a.Answer.Option != nil || a.Answer.Text != "free text" {
		t.Errorf("attempt 2 = %+v %+v, want the answer in text, trimmed, by user-2", a, a.Answer)
	}
	if a := d.Attempts[2]; a.Trigger != (jobs.Trigger{Kind: jobs.TriggerAnswer, By: "user-3"}) || *a.Answer.Option != "B" || a.Answer.Text != "right" || a.Answer.Question != "Second?" {
		t.Errorf("attempt 3 = %+v %+v, want option B with its label as the text", a, a.Answer)
	}
	c := r.Calls()[2]
	want := []jobs.Answer{
		{Question: "First?", Options: []jobs.Option{{ID: "A", Label: "yes"}, {ID: "B", Label: "no"}}, Text: "free text"},
		{Question: "Second?", Options: []jobs.Option{{ID: "A", Label: "left"}, {ID: "B", Label: "right"}, {ID: "C", Label: "up"}}, Option: ptr("B"), Text: "right"},
	}
	if got := testutil.JSON(t, c.Answers); string(got) != string(testutil.JSON(t, want)) {
		t.Errorf("the third runner was given the answers %s, want %s", got, testutil.JSON(t, want))
	}
	if c.Attempt != 3 || c.Delivery != 1 || c.Round != 1 || !slices.Equal(c.Context, []string{"first note", "second note"}) || c.RequestLabel != "af:fix" || c.Dir != e.file(job.ID, 3, "") {
		t.Errorf("the third call = %+v", c.Request)
	}
}

func TestAnswer_RefusesTheStateThenTheSubscriptionThenTheAnswerAndChangesNothing(t *testing.T) {
	r := fakerunner.New().Then(perIssue(map[int]fakerunner.Step{1: question(), 2: question(), 3: question(), 4: fakerunner.Failed("sandbox", false)}))
	e := newEnv(t, withRunner(r))
	e.Subs.set("orphan", "ok")
	e.Subs.set("locked", "ok")
	blocked, orphan, locked, failed, done := e.create("widgets", 1, ""), e.create("orphan", 2, ""), e.create("locked", 3, ""), e.create("widgets", 4, ""), e.create("widgets", 5, "")
	e.start()
	for _, d := range []jobs.Doc{blocked, orphan, locked, failed, done} {
		e.waitRest(d.ID)
	}
	e.Subs.set("orphan", "")
	e.Subs.set("locked", store.TokenUnreadable)
	before := e.snapshot(blocked.ID) + e.snapshot(orphan.ID) + e.snapshot(locked.ID) + e.snapshot(failed.ID)
	var state *jobs.StateError
	for _, id := range []string{failed.ID, done.ID} {
		if _, err := e.Svc.Answer(ctx, id, "u", jobs.AnswerRequest{}); !errors.As(err, &state) {
			t.Errorf("an answer to a job that is not blocked: %v, want a *StateError before the body is looked at", err)
		}
	}
	if _, err := e.Svc.Answer(ctx, "job_00000000000000000000", "u", jobs.AnswerRequest{Option: ptr("A")}); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("an answer to no job: %v", err)
	}
	if _, err := e.Svc.Answer(ctx, orphan.ID, "u", jobs.AnswerRequest{}); !errors.Is(err, jobs.ErrNotSubscribed) {
		t.Errorf("an answer on a workspace no longer subscribed: %v, want ErrNotSubscribed before the body", err)
	}
	if _, err := e.Svc.Answer(ctx, locked.ID, "u", jobs.AnswerRequest{Option: ptr("A")}); !errors.Is(err, jobs.ErrTokenUnreadable) {
		t.Errorf("an answer on a workspace whose token does not open: %v", err)
	}
	for name, tc := range map[string]struct {
		req   jobs.AnswerRequest
		field string
	}{
		"nothing":                     {jobs.AnswerRequest{}, "answer"},
		"an answer and an option":     {jobs.AnswerRequest{Answer: ptr("x"), Option: ptr("A")}, "answer"},
		"an answer of white space":    {jobs.AnswerRequest{Answer: ptr(" \t\n ")}, "answer"},
		"an answer that is not UTF-8": {jobs.AnswerRequest{Answer: ptr("a\xff")}, "answer"},
		"an answer with a NUL byte":   {jobs.AnswerRequest{Answer: ptr("a\x00")}, "answer"},
		"an answer over 8 KiB":        {jobs.AnswerRequest{Answer: ptr(strings.Repeat("a", 8193))}, "answer"},
		"an option that is not there": {jobs.AnswerRequest{Option: ptr("Z")}, "option"},
		"an option in lower case":     {jobs.AnswerRequest{Option: ptr("a")}, "option"},
	} {
		var invalid *jobs.ValidationError
		if _, err := e.Svc.Answer(ctx, blocked.ID, "u", tc.req); !errors.As(err, &invalid) || invalid.Field != tc.field {
			t.Errorf("%s: %v, want a *ValidationError of %s", name, err, tc.field)
		}
	}
	if after := e.snapshot(blocked.ID) + e.snapshot(orphan.ID) + e.snapshot(locked.ID) + e.snapshot(failed.ID); after != before {
		t.Errorf("a refusal changed a job")
	}
	// An answer of 8 KiB is taken.
	if _, err := e.Svc.Answer(ctx, blocked.ID, "u", jobs.AnswerRequest{Answer: ptr(strings.Repeat("a", 8192))}); err != nil {
		t.Errorf("an answer of 8 KiB: %v", err)
	}
}

func TestAnswer_AQuestionWithoutOptionsIsAnsweredInText(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Why?"))))))
	job := e.create("widgets", 1, "")
	e.start()
	e.waitState(job.ID, jobs.StateBlocked)
	var invalid *jobs.ValidationError
	if _, err := e.Svc.Answer(ctx, job.ID, "u", jobs.AnswerRequest{Option: ptr("A")}); !errors.As(err, &invalid) || !strings.Contains(err.Error(), "answer in text") {
		t.Errorf("an option to a question without options: %v", err)
	}
	if _, err := e.Svc.Answer(ctx, job.ID, "u", jobs.AnswerRequest{Answer: ptr("because")}); err != nil {
		t.Error(err)
	}
}

func TestRetry_QueuesTheNextAttemptAndAnAttemptOfTheForgeCarriesItsContext(t *testing.T) {
	r := fakerunner.New(fakerunner.Failed("sandbox", false), fakerunner.Failed("sandbox", false), failedBrain("brain", true, 3), fakerunner.Done())
	e := newEnv(t, withRunner(r))
	job, err := e.Svc.Create(ctx, jobs.CreateRequest{Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage("42"), Context: []string{"job ctx"}},
		Source: jobs.Source{Kind: "api", By: "user-1"}})
	if err != nil {
		t.Fatal(err)
	}
	e.start()
	e.waitState(job.ID, jobs.StateFailed)
	doc, err := e.Svc.Retry(ctx, job.ID, "user-2")
	if err != nil || doc.State != jobs.StateQueued || doc.FinishedAt != nil {
		t.Fatalf("Retry = %+v, %v, want the job queued", doc, err)
	}
	testutil.Eventually(t, "the retry to fail", func() bool { d := e.get(job.ID); return d.State == jobs.StateFailed && d.CurrentAttempt == 2 })
	if _, err := e.Svc.RetryWithContext(ctx, job.ID, "label af:fix", []string{"--- comment by alice ---\nsee CI run 812"}); err != nil {
		t.Fatal(err)
	}
	d := e.waitState(job.ID, jobs.StateDone)
	var triggers []jobs.Trigger
	for _, a := range d.Attempts {
		triggers = append(triggers, a.Trigger)
	}
	if want := []jobs.Trigger{{Kind: jobs.TriggerCreate, By: "user-1"}, {Kind: jobs.TriggerRetry, By: "user-2"}, {Kind: jobs.TriggerRetry, By: "label af:fix"}, {Kind: jobs.TriggerAuto, By: "agent-fox"}}; !slices.Equal(triggers, want) {
		t.Errorf("triggers = %v, want %v", triggers, want)
	}
	if d.Attempts[1].Context != nil || !slices.Equal(d.Attempts[2].Context, []string{"--- comment by alice ---\nsee CI run 812"}) || d.Attempts[3].Context != nil {
		t.Errorf("the attempts' contexts are %q, %q and %q, want only the forge's retry to have one", d.Attempts[1].Context, d.Attempts[2].Context, d.Attempts[3].Context)
	}
	// The runner is given the job's context, then each attempt's up to its own; an automatic attempt keeps the retry's.
	var given, rounds []string
	for _, c := range r.Calls() {
		given, rounds = append(given, strings.Join(c.Context, "+")), append(rounds, fmt.Sprint(c.Round))
	}
	withComment := "job ctx+--- comment by alice ---\nsee CI run 812"
	if !slices.Equal(given, []string{"job ctx", "job ctx", withComment, withComment}) || !slices.Equal(rounds, []string{"1", "1", "1", "2"}) {
		t.Errorf("the runners were given the contexts %q in the rounds %v", given, rounds)
	}
}

func TestRetry_RefusesAJobThatDidNotFailAWorkspaceThatCannotTakeItAndAnIssueTakenMeanwhile(t *testing.T) {
	failure := fakerunner.Failed("sandbox", false)
	steps := map[int]fakerunner.Step{42: failure, 1: failure, 2: question()}
	e := newEnv(t, withRunner(fakerunner.New().Then(perIssue(steps))))
	e.Subs.set("locked", "ok")
	taken, locked, blocked := e.create("widgets", 42, ""), e.create("locked", 1, ""), e.create("gadgets", 2, "")
	e.start()
	e.waitState(taken.ID, jobs.StateFailed)
	e.waitState(locked.ID, jobs.StateFailed)
	e.waitState(blocked.ID, jobs.StateBlocked)
	e.Subs.set("locked", store.TokenUnreadable)
	steps[42] = fakerunner.Block(fakerunner.Done()) // nothing runs now: the next job of #42 holds it
	other := e.create("widgets", 42, "")            // the issue is free again, and taken
	var state *jobs.StateError
	if _, err := e.Svc.Retry(ctx, blocked.ID, "u"); !errors.As(err, &state) || state.State != jobs.StateBlocked {
		t.Errorf("a retry of a blocked job: %v, want a *StateError for blocked", err)
	}
	if _, err := e.Svc.Retry(ctx, locked.ID, "u"); !errors.Is(err, jobs.ErrTokenUnreadable) {
		t.Errorf("a retry on a workspace whose token does not open: %v", err)
	}
	var dup *jobs.DuplicateError
	if _, err := e.Svc.Retry(ctx, taken.ID, "u"); !errors.As(err, &dup) || dup.JobID != other.ID {
		t.Errorf("a retry of an issue another job has taken: %v, want the duplicate naming %s", err, other.ID)
	}
	if _, err := e.Svc.Retry(ctx, "job_00000000000000000000", "u"); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("a retry of no job: %v", err)
	}
	if d := e.get(taken.ID); d.State != jobs.StateFailed || len(d.Attempts) != 1 {
		t.Errorf("a refused retry changed the job: %s with %d attempts", d.State, len(d.Attempts))
	}
}

func TestRetry_AnAttemptThatCannotBeQueuedIsStoredAndLeftToTheSweep(t *testing.T) {
	var e *env
	e = newEnv(t, withRunner(fakerunner.New(fakerunner.Failed("sandbox", false))), func(c *jobs.Config) {
		c.Enqueue = func(ctx context.Context, id string, n int) error {
			if n > 1 {
				return errors.New("the queue is down")
			}
			return e.Svc.Enqueue(ctx, id, n)
		}
	})
	job := e.create("widgets", 1, "")
	e.start()
	e.waitState(job.ID, jobs.StateFailed)
	if doc, err := e.Svc.Retry(ctx, job.ID, "u"); err != nil || doc.State != jobs.StateQueued {
		t.Fatalf("Retry = %+v, %v", doc, err)
	}
	if warned := e.Log.Find("WARN", "sweep will queue it"); len(warned) != 1 || warned[0]["attempt"] != float64(2) {
		t.Errorf("warnings = %v, want one for attempt 2", e.Log.Records())
	}
}

func TestActions_OfTwoTakenOnTheSameStateExactlyOneIsApplied(t *testing.T) {
	type action struct {
		name string
		do   func(svc *jobs.Service, id string) error
	}
	cancel := action{"cancel", func(svc *jobs.Service, id string) error { _, err := svc.Cancel(ctx, id); return err }}
	answer := action{"answer", func(svc *jobs.Service, id string) error {
		_, err := svc.Answer(ctx, id, "u", jobs.AnswerRequest{Answer: ptr("go on")})
		return err
	}}
	retry := action{"retry", func(svc *jobs.Service, id string) error { _, err := svc.Retry(ctx, id, "u"); return err }}
	for _, tc := range []struct {
		state jobs.State
		pairs [][2]action
	}{
		{jobs.StateQueued, [][2]action{{cancel, cancel}}},
		{jobs.StateRunning, [][2]action{{cancel, cancel}}},
		{jobs.StateBlocked, [][2]action{{cancel, cancel}, {answer, answer}, {cancel, answer}}},
		{jobs.StateFailed, [][2]action{{retry, retry}}},
	} {
		for _, pair := range tc.pairs {
			t.Run(fmt.Sprintf("%s, %s and %s", tc.state, pair[0].name, pair[1].name), func(t *testing.T) {
				// Each action, once it has seen the state, waits for the other to have seen it too. The service is not
				// started: nothing else moves the job.
				var seen sync.WaitGroup
				seen.Add(2)
				var armed bool
				e := newEnv(t, withFault(func(op string) error {
					if op == jobs.OpObserved && armed {
						seen.Done()
						seen.Wait()
					}
					return nil
				}))
				id := e.create("widgets", 1, "").ID
				testutil.Exec(t, e.DB, "UPDATE af_jobs SET state = ? WHERE id = ?", string(tc.state), id)
				if tc.state == jobs.StateBlocked {
					testutil.Exec(t, e.DB, "UPDATE af_attempts SET state = 'needs_human', status = 'needs_human', question = 'Which?', options = '[]' WHERE job_id = ?", id)
				}
				armed = true
				errs := make([]error, 2)
				var done sync.WaitGroup
				for i, a := range pair {
					done.Go(func() { errs[i] = a.do(e.Svc, id) })
				}
				done.Wait()
				applied := 0
				for _, err := range errs {
					var state *jobs.StateError
					switch {
					case err == nil:
						applied++
					case !errors.As(err, &state):
						t.Fatalf("an action failed with %v", err)
					}
				}
				if applied != 1 || len(e.get(id).Attempts) > 2 {
					t.Errorf("%d applied (%v), %d attempts, want exactly one applied and at most one new attempt", applied, errs, len(e.get(id).Attempts))
				}
			})
		}
	}
}
