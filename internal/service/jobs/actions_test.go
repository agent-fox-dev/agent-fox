package jobs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// answer posts an answer to the job as the caller.
func (e *env) answer(id string, body any) response {
	e.t.Helper()
	return e.call("POST", "/api/v1/jobs/"+id+"/answer", body)
}

// retry posts a retry of the job as the caller.
func (e *env) retry(id string, body any) response {
	e.t.Helper()
	return e.call("POST", "/api/v1/jobs/"+id+"/retry", body)
}

// question is a needs_human step with the question "Which?" and three options.
func question() fakerunner.Step {
	return fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which?", "one", "two", "three")))
}

// TestAnsweringABlockedJobQueuesTheNextAttemptWithTheQuestionTheOptionAndTheText_TS_12_52
// verifies TS-12-52: an answer by option and an answer in free text each answer 202 with the
// job queued and finished_at null; attempt 2 has the trigger answer and the caller, and
// the answer with the question, the option and the label of the option as the text; the
// free answer keeps the text, trimmed, with no option; and the runner of attempt 2 is
// given the question, its options and the text.
// Verifies: 12-REQ-8.3
func TestAnsweringABlockedJobQueuesTheNextAttemptWithTheQuestionTheOptionAndTheText_TS_12_52(t *testing.T) {
	r := fakerunner.New(question(), question(), fakerunner.Done(), fakerunner.Done())
	e := newEnv(t, withRunner(r))
	byOption := e.create("widgets", jobs.KindFix, 1)
	e.Start()
	e.waitState(byOption.ID, jobs.StateBlocked)
	inText := e.create("widgets", jobs.KindFix, 2)
	e.waitState(inText.ID, jobs.StateBlocked)

	resp := e.answer(byOption.ID, map[string]string{"option": "B"})
	doc := resp.Doc(t)
	if resp.Status != http.StatusAccepted || doc.State != jobs.StateQueued || doc.FinishedAt != nil || doc.Blocked != nil {
		t.Fatalf("status %d, job %s, finished_at %v, blocked %v, want 202, queued, null and null\n%s", resp.Status, doc.State, doc.FinishedAt, doc.Blocked, resp.Body)
	}
	resp = e.answer(inText.ID, map[string]string{"answer": "  free text \n"})
	if doc := resp.Doc(t); resp.Status != http.StatusAccepted || doc.State != jobs.StateQueued {
		t.Fatalf("free text: status %d, job %s", resp.Status, doc.State)
	}
	e.waitRest(byOption.ID)
	e.waitRest(inText.ID)

	a := e.get(byOption.ID).Attempts
	if len(a) != 2 || a[1].Trigger != (jobs.Trigger{Kind: jobs.TriggerAnswer, By: callerID}) {
		t.Fatalf("attempts = %+v, want attempt 2 with the trigger answer by %s", a, callerID)
	}
	b := "B"
	if a[1].Answer == nil || a[1].Answer.Question != "Which?" || a[1].Answer.Option == nil || *a[1].Answer.Option != b || a[1].Answer.Text != "two" {
		t.Errorf("answer = %+v, want the question, option B and the label two as the text", a[1].Answer)
	}
	free := e.get(inText.ID).Attempts[1].Answer
	if free == nil || free.Option != nil || free.Text != "free text" || free.Question != "Which?" {
		t.Errorf("free answer = %+v, want the text 'free text', trimmed, and no option", free)
	}

	// The runner of attempt 2 is given the question, the options and the text.
	for _, c := range r.Calls() {
		if c.Attempt != 2 {
			continue
		}
		if len(c.Answers) != 1 || c.Answers[0].Question != "Which?" || len(c.Answers[0].Options) != 3 || c.Answers[0].Options[1] != (jobs.Option{ID: "B", Label: "two"}) {
			t.Errorf("call for attempt 2 of %s: answers = %+v", c.JobID, c.Answers)
		}
		switch c.JobID {
		case byOption.ID:
			if c.Answers[0].Option == nil || *c.Answers[0].Option != "B" || c.Answers[0].Text != "two" {
				t.Errorf("by option: answer = %+v, want option B and the text two", c.Answers[0])
			}
		case inText.ID:
			if c.Answers[0].Option != nil || c.Answers[0].Text != "free text" {
				t.Errorf("in text: answer = %+v, want the text and no option", c.Answers[0])
			}
		}
	}
	if n := len(r.Calls()); n != 4 {
		t.Errorf("%d calls, want 4", n)
	}
}

// TestRetryingAFailedJobQueuesTheNextAttemptWithTheTriggerRetry_TS_12_53 verifies TS-12-53:
// a retry with no body and with {} each answer 202 with the job queued and an
// attempt n+1 with the trigger retry; a body with a field that is not known is 400.
// Verifies: 12-REQ-8.4
func TestRetryingAFailedJobQueuesTheNextAttemptWithTheTriggerRetry_TS_12_53(t *testing.T) {
	failure := fakerunner.Failed("sandbox", false)
	r := fakerunner.New(failure, failure, failure, failure).Then(fakerunner.Done()) // the fourth job fails too, and is not retried
	e := newEnv(t, withRunner(r))
	a, b, c := e.create("widgets", jobs.KindFix, 1), e.create("widgets", jobs.KindFix, 2), e.create("widgets", jobs.KindFix, 3)
	failed := e.create("widgets", jobs.KindFix, 4) // the one that is refused a retry below
	e.Start()
	for _, id := range []string{a.ID, b.ID, c.ID, failed.ID} {
		e.waitState(id, jobs.StateFailed)
	}
	for name, body := range map[string]any{"no body": nil, "an empty object": "{}", "an object with white space": " { } \n"} {
		id := map[string]string{"no body": a.ID, "an empty object": b.ID, "an object with white space": c.ID}[name]
		resp := e.retry(id, body)
		doc := resp.Doc(t)
		if resp.Status != http.StatusAccepted || doc.State != jobs.StateQueued || doc.FinishedAt != nil {
			t.Errorf("%s: status %d, job %s, finished_at %v, want 202, queued and null\n%s", name, resp.Status, doc.State, doc.FinishedAt, resp.Body)
		}
		d := e.waitRest(id)
		if len(d.Attempts) != 2 || d.Attempts[1].Trigger != (jobs.Trigger{Kind: jobs.TriggerRetry, By: callerID}) || d.State != jobs.StateDone {
			t.Errorf("%s: attempts %+v, job %s, want attempt 2 with the trigger retry, and the job done", name, d.Attempts, d.State)
		}
	}
	// A body that is not empty is refused, and nothing is queued.
	for name, body := range map[string]any{"a field that is not known": `{"force": true}`, "a list": `[]`, "not JSON": `{`} {
		resp := e.retry(failed.ID, body)
		if code, _ := resp.errorOf(t); resp.Status != http.StatusBadRequest || code != "invalid_request" {
			t.Errorf("%s: status %d %s, want 400 invalid_request", name, resp.Status, code)
		}
	}
	if d := e.get(failed.ID); d.State != jobs.StateFailed || len(d.Attempts) != 1 {
		t.Errorf("a refused retry changed the job: %s with %d attempts", d.State, len(d.Attempts))
	}
}

// refuse checks a refusal: the status and code, a message that has the text, and that the
// job, its attempts and its events are as they were.
func refuse(t *testing.T, e *env, id string, resp response, status int, code, text string) {
	t.Helper()
	got, message := resp.errorOf(t)
	if resp.Status != status || got != code || !strings.Contains(message, text) {
		t.Errorf("status %d %s %q, want %d %s with %q in the message", resp.Status, got, message, status, code, text)
	}
}

// TestCancelAnswerAndRetryRefuseInTheOrderOfTheSpecAndChangeNothing_TS_12_54 verifies
// TS-12-54: an unknown job and a job on a workspace the caller cannot see are 404 for
// every action; a job in a state the action does not start from is 409 invalid_state
// naming the state, before the subscription is looked at; for answer and retry only, a
// workspace that is not subscribed is 409 not_subscribed and a token that does not open is
// 409 token_unreadable, while cancel still works; a body that is not exactly one valid
// answer is 400; and no refusal changes a state, an attempt or an event.
// Verifies: 12-REQ-8.5
func TestCancelAnswerAndRetryRefuseInTheOrderOfTheSpecAndChangeNothing_TS_12_54(t *testing.T) {
	r := fakerunner.New(
		question(),                          // blocked, on widgets
		fakerunner.Failed("sandbox", false), // failed, on widgets
		fakerunner.Done(),                   // done, on widgets
		question(),                          // blocked, on an unsubscribed workspace
		fakerunner.Failed("sandbox", false), // failed, on a workspace whose token does not open
		fakerunner.Done(),                   // on a workspace the caller cannot see
		fakerunner.Block(fakerunner.Done()), // holds widgets, so that the next job there is queued
	)
	e := newEnv(t, withRunner(r))
	for _, slug := range []string{"orphan", "locked", "secret"} {
		e.subscribe(slug)
	}
	e.Hub.Grant(callerID, "orphan", "locked") // secret is not shown to the caller
	e.Start()
	run := func(slug string, issue int, want jobs.State) jobs.Doc {
		t.Helper()
		doc, err := e.Svc.Create(ctx, jobs.CreateRequest{Workspace: slug, Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage(fmt.Sprint(issue))}, Source: jobs.Source{Kind: "api", By: callerID}})
		if err != nil {
			t.Fatal(err)
		}
		return e.waitState(doc.ID, want)
	}
	blocked, failed, done := run("widgets", 1, jobs.StateBlocked), run("widgets", 2, jobs.StateFailed), run("widgets", 3, jobs.StateDone)
	orphan := run("orphan", 4, jobs.StateBlocked)
	locked := run("locked", 5, jobs.StateFailed)
	hidden := run("secret", 6, jobs.StateDone)
	if _, err := e.DB.Exec("DELETE FROM af_subscriptions WHERE slug = 'orphan'"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Exec("UPDATE af_subscriptions SET sealed_token = x'0102' WHERE slug = 'locked'"); err != nil {
		t.Fatal(err)
	}
	run("widgets", 7, jobs.StateRunning)
	queued := run("widgets", 8, jobs.StateQueued)

	snapshot := func() string {
		out := ""
		for _, d := range []jobs.Doc{blocked, failed, done, orphan, locked, hidden} {
			out += e.snapshot(d.ID)
		}
		return out
	}
	before := snapshot()

	for _, action := range []string{"cancel", "answer", "retry"} {
		call := func(id string) response {
			switch action {
			case "answer":
				return e.answer(id, map[string]string{"option": "A"})
			case "retry":
				return e.retry(id, nil)
			}
			return e.cancel(id)
		}
		unknown, invisible := call("job_00000000000000000000"), call(hidden.ID)
		refuse(t, e, "", unknown, 404, "not_found", "")
		refuse(t, e, "", invisible, 404, "not_found", "")
		if string(unknown.Body) != string(invisible.Body) {
			t.Errorf("%s: an unknown job is answered %s and an invisible one %s, want the same answer", action, unknown.Body, invisible.Body)
		}
	}

	// A state the action does not start from, before the subscription is looked at.
	refuse(t, e, done.ID, e.cancel(done.ID), 409, "invalid_state", "done")
	refuse(t, e, done.ID, e.answer(done.ID, map[string]string{"option": "A"}), 409, "invalid_state", "done")
	refuse(t, e, done.ID, e.retry(done.ID, nil), 409, "invalid_state", "done")
	refuse(t, e, blocked.ID, e.retry(blocked.ID, nil), 409, "invalid_state", "blocked")
	refuse(t, e, failed.ID, e.answer(failed.ID, map[string]string{"answer": "x"}), 409, "invalid_state", "failed")
	refuse(t, e, queued.ID, e.answer(queued.ID, map[string]string{"answer": "x"}), 409, "invalid_state", "queued")
	refuse(t, e, queued.ID, e.retry(queued.ID, nil), 409, "invalid_state", "queued")
	// The state is looked at before the body: a bad body for a job that is not blocked is the state.
	refuse(t, e, done.ID, e.answer(done.ID, map[string]string{}), 409, "invalid_state", "done")

	// The subscription, for answer and retry only, after the state and before the body.
	refuse(t, e, orphan.ID, e.answer(orphan.ID, map[string]string{"option": "A"}), 409, "not_subscribed", "")
	refuse(t, e, orphan.ID, e.answer(orphan.ID, map[string]string{}), 409, "not_subscribed", "")
	refuse(t, e, locked.ID, e.retry(locked.ID, nil), 409, "token_unreadable", "")
	refuse(t, e, orphan.ID, e.retry(orphan.ID, nil), 409, "invalid_state", "blocked")
	refuse(t, e, locked.ID, e.answer(locked.ID, map[string]string{"option": "A"}), 409, "invalid_state", "failed")

	// A body that is not exactly one valid answer, for a job that can be answered.
	for name, body := range map[string]any{
		"an empty object":             map[string]string{},
		"an answer and an option":     map[string]string{"answer": "x", "option": "A"},
		"an answer of white space":    map[string]string{"answer": " \t\n "},
		"an empty answer":             map[string]string{"answer": ""},
		"an answer over 8 KiB":        map[string]string{"answer": strings.Repeat("a", 8193)},
		"an option that is not there": map[string]string{"option": "Z"},
		"an option in lower case":     map[string]string{"option": "a"},
		"an option of nothing":        map[string]string{"option": ""},
	} {
		resp := e.answer(blocked.ID, body)
		if code, _ := resp.errorOf(t); resp.Status != http.StatusBadRequest || code != "invalid_request" {
			t.Errorf("%s: status %d %s, want 400 invalid_request", name, resp.Status, code)
		}
	}
	for name, body := range map[string]any{"no body": nil, "not JSON": `{`, "an unknown field": `{"option":"A","extra":1}`, "text": `"A"`} {
		if resp := e.answer(blocked.ID, body); resp.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, resp.Status)
		}
	}
	if after := snapshot(); after != before {
		t.Errorf("a refusal changed a job:\nbefore %s\nafter  %s", before, after)
	}

	// Cancel still works on a workspace that is not subscribed.
	if resp := e.cancel(orphan.ID); resp.Status != 200 || resp.Doc(t).State != jobs.StateCancelled {
		t.Errorf("cancel on a workspace that is not subscribed: %d\n%s", resp.Status, resp.Body)
	}
}

// escapeAll writes every byte of s as \u00XX.
func escapeAll(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		fmt.Fprintf(&b, "\\u%04x", s[i])
	}
	return b.String()
}

// TestTheLargestValidBodiesWithEveryByteEscapedAreAcceptedAndOneByteOverTheLimitIsNot_TS_12_18
// verifies TS-12-18: the largest triage body (128 KiB of text, a title of 256 bytes, 32
// KiB of context), the largest with a file, and the largest fix body, each with every
// byte written as \u00XX, answer 202 and not 413; an answer of 8 KiB written so is
// accepted; and a body one byte over the limit of its route, 1 MiB, 256 KiB and 64 KiB,
// answers 413 payload_too_large.
// Verifies: 12-REQ-3.4
func TestTheLargestValidBodiesWithEveryByteEscapedAreAcceptedAndOneByteOverTheLimitIsNot_TS_12_18(t *testing.T) {
	// The three largest requests take the first three calls and are done; the fourth job asks.
	r := fakerunner.New(fakerunner.Done(), fakerunner.Done(), fakerunner.Done(), question()).Then(fakerunner.Done())
	e := newEnv(t, withRunner(r))
	e.Start()
	notes := func() string {
		parts := make([]string, 4)
		for i := range parts {
			parts[i] = `"` + escapeAll(strings.Repeat("n", 8*1024)) + `"`
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	text := strings.Repeat("t", toolio.MaxInputBytes)
	bodies := map[string]struct{ path, body string }{
		"a triage of the largest text": {"triage", fmt.Sprintf(`{"text":"%s","title":"%s","context":%s}`, escapeAll(text), escapeAll(strings.Repeat("h", 256)), notes())},
		"a triage of the largest file": {"triage", fmt.Sprintf(`{"file":{"name":"%s","content":"%s"},"context":%s}`, escapeAll(strings.Repeat("f", 255)), escapeAll(text), notes())},
		"a fix of the largest":         {"fix", fmt.Sprintf(`{"issue":"%s","context":%s}`, escapeAll("a/b"+strings.Repeat("a", 2042)+"#99"), notes())},
	}
	for name, tc := range bodies {
		resp := e.call("POST", "/api/v1/workspaces/widgets/"+tc.path, tc.body)
		if resp.Status != http.StatusAccepted {
			t.Errorf("%s (%d bytes of %d): status %d, want 202\n%.300s", name, len(tc.body), map[string]int{"triage": 1 << 20, "fix": 256 << 10}[tc.path], resp.Status, resp.Body)
		}
	}
	if limit := map[string]int{"triage": 1 << 20, "fix": 256 << 10}; true {
		for path, n := range limit {
			resp := e.call("POST", "/api/v1/workspaces/widgets/"+path, strings.Repeat(" ", n+1))
			if code, _ := resp.errorOf(t); resp.Status != http.StatusRequestEntityTooLarge || code != "payload_too_large" {
				t.Errorf("%s over its limit: status %d %s, want 413 payload_too_large", path, resp.Status, code)
			}
		}
	}

	// The answer route keeps 64 KiB, and an answer of 8 KiB written byte by byte fits.
	job := e.create("widgets", jobs.KindFix, 500)
	e.waitState(job.ID, jobs.StateBlocked)
	long := fmt.Sprintf(`{"answer":"%s"}`, escapeAll(strings.Repeat("a", 8*1024)))
	if resp := e.answer(job.ID, long); resp.Status != http.StatusAccepted {
		t.Errorf("an answer of 8 KiB written as \\u00XX: status %d, want 202 (%d bytes)\n%.200s", resp.Status, len(long), resp.Body)
	}
	e.waitRest(job.ID)
	resp := e.answer(job.ID, strings.Repeat(" ", 64<<10+1))
	if code, _ := resp.errorOf(t); resp.Status != http.StatusRequestEntityTooLarge || code != "payload_too_large" {
		t.Errorf("an answer body over 64 KiB: status %d %s, want 413", resp.Status, code)
	}
}

// TestTheRunnerOfALaterAttemptIsGivenEveryQuestionAnsweredSoFarInOrder_TS_12_29 verifies
// TS-12-29: a job made by a trigger, with a request label, an issue and two strings of
// context, runs, is answered in free text, runs again, is answered with option B and
// runs a third time; that call is given the job id, attempt 3 and delivery 1, the
// workspace, the kind, the input, the context, the source and the request label, both
// questions with their options and answers in order, and the absolute directory
// attempts/<job id>/3 beside the database, which exists when it is called.
// Verifies: 12-REQ-5.1
func TestTheRunnerOfALaterAttemptIsGivenEveryQuestionAnsweredSoFarInOrder_TS_12_29(t *testing.T) {
	var dirs []os.FileInfo
	var mu sync.Mutex
	r := fakerunner.New(
		fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("First?", "yes", "no"))),
		fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Second?", "left", "right", "up"))),
		fakerunner.Do(func(_ context.Context, req jobs.Request) (jobs.Result, error) {
			mu.Lock()
			defer mu.Unlock()
			info, err := os.Stat(req.Dir)
			if err != nil {
				t.Errorf("the attempt directory is not there when the runner is called: %v", err)
			}
			dirs = append(dirs, info)
			return jobs.Result{Envelope: fakerunner.Env(toolio.StatusDone)}, nil
		}),
	)
	e := newEnv(t, withRunner(r))
	job, err := e.Svc.Create(ctx, jobs.CreateRequest{
		Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage("42"), Context: []string{"first note", "second note"}},
		Source: jobs.Source{Kind: "label", By: "poll"}, RequestLabel: "af:fix",
	})
	if err != nil {
		t.Fatal(err)
	}
	e.Start()
	e.waitState(job.ID, jobs.StateBlocked)
	if resp := e.answer(job.ID, map[string]string{"answer": "free text"}); resp.Status != 202 {
		t.Fatalf("answer: %d\n%s", resp.Status, resp.Body)
	}
	waitFor(t, "the second question", func() bool { d := e.get(job.ID); return d.State == jobs.StateBlocked && d.CurrentAttempt == 2 })
	if resp := e.answer(job.ID, map[string]string{"option": "B"}); resp.Status != 202 {
		t.Fatalf("answer: %d\n%s", resp.Status, resp.Body)
	}
	e.waitState(job.ID, jobs.StateDone)

	calls := r.Calls()
	if len(calls) != 3 {
		t.Fatalf("%d calls, want 3", len(calls))
	}
	c := calls[2]
	if c.JobID != job.ID || c.Attempt != 3 || c.Delivery != 1 || c.Workspace != "widgets" || c.Kind != jobs.KindFix || string(c.Input.Issue) != "42" ||
		!slices.Equal(c.Context, []string{"first note", "second note"}) || c.Source != (jobs.Source{Kind: "label", By: "poll"}) || c.RequestLabel != "af:fix" {
		t.Errorf("call 3 = %+v", c.Request)
	}
	b := "B"
	want := []jobs.Answer{
		{Question: "First?", Options: []jobs.Option{{ID: "A", Label: "yes"}, {ID: "B", Label: "no"}}, Text: "free text"},
		{Question: "Second?", Options: []jobs.Option{{ID: "A", Label: "left"}, {ID: "B", Label: "right"}, {ID: "C", Label: "up"}}, Option: &b, Text: "right"},
	}
	if got, _ := json.Marshal(c.Answers); string(got) != mustJSON(want) {
		t.Errorf("answers = %s, want %s", got, mustJSON(want))
	}
	if !filepath.IsAbs(c.Dir) || c.Dir != filepath.Join(e.Dir, "attempts", job.ID, "3") {
		t.Errorf("Dir = %s, want the absolute %s", c.Dir, filepath.Join(e.Dir, "attempts", job.ID, "3"))
	}
	mu.Lock()
	if len(dirs) != 1 || dirs[0] == nil || !dirs[0].IsDir() {
		t.Errorf("the attempt directory was not there as a directory when the runner was called: %v", dirs)
	}
	mu.Unlock()
}

// TestAJobShowsItsAttemptsInOrderWithTheirTriggersAnswersAndCounts_TS_12_14 verifies
// TS-12-14 on the document of one job: attempt 1 (create) ended needs_human and
// attempt 2 (an answer by option B) ended failed after one transient failure; the
// attempts are in order of their numbers and each has its state, trigger, answer,
// deliveries, transient_failures, status, exit code, summary, error, usage and times.
// (The routes that serve it come with task 9.)
// Verifies: 12-REQ-2.7
func TestAJobShowsItsAttemptsInOrderWithTheirTriggersAnswersAndCounts_TS_12_14(t *testing.T) {
	clk := fakeclock.New(epoch)
	r := fakerunner.New(question(), fakerunner.TransientFailure(sandboxFailure), fakerunner.Failed("sandbox", false))
	e := newEnv(t, withRunner(r), withClock(clk))
	job := e.create("widgets", jobs.KindFix, 1)
	e.Start()
	e.waitState(job.ID, jobs.StateBlocked)
	clk.Advance(time.Minute)
	if resp := e.answer(job.ID, map[string]string{"option": "B"}); resp.Status != 202 {
		t.Fatalf("answer: %d", resp.Status)
	}
	clk.WaitForWaits(1, 10*time.Second)
	clk.Advance(30 * time.Second)
	doc := e.waitRest(job.ID)

	if doc.State != jobs.StateFailed || len(doc.Attempts) != 2 {
		t.Fatalf("job %s with %d attempts, want failed and 2", doc.State, len(doc.Attempts))
	}
	one, two := doc.Attempts[0], doc.Attempts[1]
	if one.N != 1 || two.N != 2 || one.State != jobs.AttemptNeedsHuman || two.State != jobs.AttemptFailed {
		t.Errorf("attempts = %d %s, %d %s, want 1 needs_human and 2 failed", one.N, one.State, two.N, two.State)
	}
	if one.Trigger != (jobs.Trigger{Kind: jobs.TriggerCreate, By: callerID}) || two.Trigger != (jobs.Trigger{Kind: jobs.TriggerAnswer, By: callerID}) {
		t.Errorf("triggers = %+v and %+v", one.Trigger, two.Trigger)
	}
	if one.Answer != nil || two.Answer == nil || two.Answer.Question != "Which?" || two.Answer.Option == nil || *two.Answer.Option != "B" || two.Answer.Text != "two" {
		t.Errorf("answers = %+v and %+v, want none, and the question, B and 'two'", one.Answer, two.Answer)
	}
	if one.Deliveries != 1 || two.Deliveries != 2 || one.TransientFailures != 0 || two.TransientFailures != 1 {
		t.Errorf("deliveries %d and %d, transient_failures %d and %d, want 1, 2, 0 and 1", one.Deliveries, two.Deliveries, one.TransientFailures, two.TransientFailures)
	}
	for _, a := range doc.Attempts {
		if a.Status == nil || a.ExitCode == nil || a.Summary == nil || a.Usage == nil || a.StartedAt == nil || a.FinishedAt == nil {
			t.Errorf("attempt %d lacks a field: %+v", a.N, a)
		}
	}
	if two.Error == nil || two.Error.Category != "sandbox" || one.Error != nil {
		t.Errorf("errors = %+v and %+v, want none and sandbox", one.Error, two.Error)
	}
	raw, _ := json.Marshal(doc.Attempts[1])
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	for _, f := range []string{"n", "state", "trigger", "answer", "deliveries", "transient_failures", "status", "exit_code", "summary", "error", "usage", "started_at", "finished_at"} {
		if _, ok := fields[f]; !ok {
			t.Errorf("the document of an attempt has no field %s: %s", f, raw)
		}
	}
	list := doc
	list.Attempts = nil
	if raw, _ := json.Marshal(list); strings.Contains(string(raw), `"attempts"`) {
		t.Errorf("an entry of a list has an attempts field: %.200s", raw)
	}
}

// TestStartedAtIsSetAtTheFirstClaimAndFinishedAtOnlyWhileTheJobIsFinished_TS_12_12
// verifies TS-12-12: started_at is null while a job is queued, set at the first claim and
// kept by later claims; finished_at is set exactly when the job is done, failed or
// cancelled, a cancel from queued or from blocked included, and is the time of the cancel
// and not of the question; a retry, an answer and an automatic attempt clear it.
// Verifies: 12-REQ-2.5
func TestStartedAtIsSetAtTheFirstClaimAndFinishedAtOnlyWhileTheJobIsFinished_TS_12_12(t *testing.T) {
	clk := fakeclock.New(epoch)
	gate := make(chan struct{})
	r := fakerunner.New(
		fakerunner.Failed("sandbox", false), // job 1: failed, then retried
		fakerunner.Done(),                   // job 1 again: done
		question(),                          // job 2: blocked
		fakerunner.Failed("brain", true),    // job 3: an automatic attempt follows
		fakerunner.Do(func(ctx_ context.Context, req jobs.Request) (jobs.Result, error) { // job 3, attempt 2, held
			<-gate
			return jobs.Result{Envelope: fakerunner.Env(toolio.StatusDone)}, nil
		}),
	)
	e := newEnv(t, withRunner(r), withClock(clk))
	invariant := func(doc jobs.Doc) {
		t.Helper()
		if (doc.FinishedAt != nil) != doc.State.Finished() {
			t.Errorf("job %s is %s with finished_at %v, want it set exactly when the job is done, failed or cancelled", doc.ID, doc.State, doc.FinishedAt)
		}
	}

	// A job cancelled while it is queued has a finished_at, and never started.
	idle := newEnv(t, withClock(fakeclock.New(epoch)))
	queuedJob := idle.create("widgets", jobs.KindFix, 1)
	if d := idle.get(queuedJob.ID); d.StartedAt != nil || d.FinishedAt != nil {
		t.Errorf("a queued job has started_at %v and finished_at %v", d.StartedAt, d.FinishedAt)
	}
	cancelled := idle.cancel(queuedJob.ID).Doc(t)
	invariant(cancelled)
	if cancelled.StartedAt != nil || cancelled.FinishedAt == nil || *cancelled.FinishedAt != "2026-10-22T08:00:00.000Z" {
		t.Errorf("cancelled while queued: started_at %v, finished_at %v, want null and the time of the cancel", cancelled.StartedAt, cancelled.FinishedAt)
	}

	// Failed, retried, done: started_at is the first claim's.
	one := e.create("widgets", jobs.KindFix, 1)
	if d := e.get(one.ID); d.StartedAt != nil {
		t.Fatalf("started_at = %v before any claim", *d.StartedAt)
	}
	clk.Advance(10 * time.Second)
	e.Start()
	failed := e.waitState(one.ID, jobs.StateFailed)
	invariant(failed)
	if failed.StartedAt == nil || *failed.StartedAt != "2026-10-22T08:00:10.000Z" || *failed.FinishedAt != "2026-10-22T08:00:10.000Z" {
		t.Fatalf("started_at %v, finished_at %v after the first run, want the time of the claim", failed.StartedAt, failed.FinishedAt)
	}
	clk.Advance(20 * time.Second)
	if resp := e.retry(one.ID, nil); resp.Status != 202 {
		t.Fatalf("retry: %d", resp.Status)
	}
	retried := e.get(one.ID)
	invariant(retried)
	done := e.waitState(one.ID, jobs.StateDone)
	invariant(done)
	if *done.StartedAt != *failed.StartedAt || *done.FinishedAt != "2026-10-22T08:00:30.000Z" {
		t.Errorf("after the retry: started_at %v (was %v), finished_at %v, want the first claim's and the time of the end", *done.StartedAt, *failed.StartedAt, *done.FinishedAt)
	}

	// Blocked, then cancelled an hour later: finished_at is the cancel's.
	two := e.create("widgets", jobs.KindFix, 2)
	blocked := e.waitState(two.ID, jobs.StateBlocked)
	invariant(blocked)
	asked := clk.Now()
	clk.Advance(time.Hour)
	cancelledBlocked := e.cancel(two.ID).Doc(t)
	invariant(cancelledBlocked)
	if cancelledBlocked.FinishedAt == nil || *cancelledBlocked.FinishedAt == asked.UTC().Format("2006-01-02T15:04:05.000Z") ||
		*cancelledBlocked.FinishedAt != clk.Now().UTC().Format("2006-01-02T15:04:05.000Z") {
		t.Errorf("cancelled while blocked: finished_at %v, want the time of the cancel and not %v, when the question was asked", cancelledBlocked.FinishedAt, asked)
	}

	// An automatic attempt clears finished_at while it waits to run and runs.
	three := e.create("widgets", jobs.KindFix, 3)
	waitFor(t, "the automatic attempt to run", func() bool { d := e.get(three.ID); return d.State == jobs.StateRunning && d.CurrentAttempt == 2 })
	running := e.get(three.ID)
	invariant(running)
	if running.FinishedAt != nil || running.Attempts[0].State != jobs.AttemptFailed {
		t.Errorf("during the automatic attempt: finished_at %v, first attempt %s", running.FinishedAt, running.Attempts[0].State)
	}
	close(gate)
	invariant(e.waitState(three.ID, jobs.StateDone))
}

// firstThenHold is a step that does the first step for the first attempt of a job, and
// holds every later attempt until it is cancelled, so that what an action queues does not
// end at once and give the other action a state of its own to act on.
func firstThenHold(first fakerunner.Step) fakerunner.Step {
	var mu sync.Mutex
	var firsts = map[string]bool{}
	return fakerunner.Do(func(ctx context.Context, req jobs.Request) (jobs.Result, error) {
		mu.Lock()
		done := firsts[req.JobID]
		firsts[req.JobID] = true
		mu.Unlock()
		if !done {
			return fakerunner.New(first).Run(ctx, req)
		}
		<-ctx.Done()
		return jobs.Result{}, context.Cause(ctx)
	})
}

// raceAction is an action a person takes on a job.
type raceAction struct {
	name string
	do   func(e *env, id string) response
}

var (
	doCancel = raceAction{"cancel", func(e *env, id string) response { return e.cancel(id) }}
	doAnswer = raceAction{"answer", func(e *env, id string) response { return e.answer(id, map[string]string{"option": "A"}) }}
	doRetry  = raceAction{"retry", func(e *env, id string) response { return e.retry(id, nil) }}
)

// TestForAnyTwoActionsRacingOnOneJobExactlyOneIsAppliedAndTheOtherIsInvalidState_TS_12_55
// verifies TS-12-55 over HTTP: for a job that is queued, running, blocked or failed, and any
// pair of actions that the state allows, fired at the same moment, one answers 2xx and the
// other 409 invalid_state, and the job has at most one new attempt; a pair of two
// different actions may also arrive in an order in which both are valid, an answer and then
// a cancel, which the test accepts only as that. That exactly one is applied when both were
// taken on the same state is shown without luck in the package, where the two are held
// between the sight of the state and the change.
// Verifies: 12-REQ-8.6
func TestForAnyTwoActionsRacingOnOneJobExactlyOneIsAppliedAndTheOtherIsInvalidState_TS_12_55(t *testing.T) {
	rng := rand.New(rand.NewPCG(12, 55))
	type scenario struct {
		state jobs.State
		steps func() *fakerunner.Runner
		pairs [][2]raceAction
		start bool
	}
	for _, sc := range []scenario{
		{jobs.StateQueued, func() *fakerunner.Runner { return fakerunner.New() }, [][2]raceAction{{doCancel, doCancel}}, false},
		{jobs.StateRunning, func() *fakerunner.Runner { return fakerunner.New().Then(fakerunner.Block(fakerunner.Done())) }, [][2]raceAction{{doCancel, doCancel}}, true},
		{jobs.StateBlocked, func() *fakerunner.Runner { return fakerunner.New().Then(firstThenHold(question())) }, [][2]raceAction{{doCancel, doCancel}, {doAnswer, doAnswer}, {doCancel, doAnswer}, {doAnswer, doCancel}}, true},
		{jobs.StateFailed, func() *fakerunner.Runner {
			return fakerunner.New().Then(firstThenHold(fakerunner.Failed("sandbox", false)))
		}, [][2]raceAction{{doRetry, doRetry}}, true},
	} {
		t.Run(string(sc.state), func(t *testing.T) {
			e := newEnv(t, withRunner(sc.steps()), withWorkers(1))
			if sc.start {
				e.Start()
			}
			for round := range 12 {
				pair := sc.pairs[rng.IntN(len(sc.pairs))]
				job := e.create("widgets", jobs.KindFix, round+1)
				if sc.state != jobs.StateQueued {
					e.waitState(job.ID, sc.state)
				}
				before := len(e.get(job.ID).Attempts)

				start := make(chan struct{})
				var wg sync.WaitGroup
				got := make([]response, 2)
				for i, action := range pair {
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						got[i] = action.do(e, job.ID)
					}()
				}
				close(start)
				wg.Wait()

				ok, invalid := 0, 0
				for _, resp := range got {
					if resp.Status/100 == 2 {
						ok++
					} else if code, _ := resp.errorOf(t); resp.Status == 409 && code == "invalid_state" {
						invalid++
					}
				}
				mixed := pair[0].name != pair[1].name
				switch {
				case ok == 1 && invalid == 1:
				case mixed && ok == 2:
					// Two different actions that arrive one after the other are both valid in that
					// order: an answer, then a cancel of the job it queued. The job ends cancelled.
					if d := e.get(job.ID); d.State != jobs.StateCancelled || len(d.Attempts) != before+1 {
						t.Fatalf("round %d, %s and %s both succeeded, and the job is %s with %d attempts, want it cancelled after an answer", round, pair[0].name, pair[1].name, d.State, len(d.Attempts))
					}
				default:
					t.Fatalf("round %d, %s and %s on a %s job: statuses %d and %d (%s / %s), want one 2xx and one 409 invalid_state",
						round, pair[0].name, pair[1].name, sc.state, got[0].Status, got[1].Status, got[0].Body, got[1].Body)
				}
				if after := len(e.get(job.ID).Attempts); after > before+1 {
					t.Errorf("round %d: %d attempts after two actions on a job that had %d, want at most one new", round, after, before)
				}
				// Whatever won, the job is consistent: finished_at exactly when finished.
				if doc := e.get(job.ID); (doc.FinishedAt != nil) != doc.State.Finished() {
					t.Errorf("round %d: job %s with finished_at %v", round, doc.State, doc.FinishedAt)
				}
				// Let go of the workspace for the next round: a job that is still open is cancelled
				// (again, if it moved on between the sight of its state and the cancel).
				waitFor(t, "the job to be at rest", func() bool {
					d := e.get(job.ID)
					if atRest(d) {
						return true
					}
					if d.State == jobs.StateQueued || d.State == jobs.StateRunning {
						e.cancel(job.ID)
					}
					return false
				})
			}
		})
	}
}
