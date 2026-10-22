package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/worker/recovery"
)

// onStep calls f when the delivery begins the step of the name, beside the hooks the environment has.
func (e *env) onStep(name string, f func()) {
	prev := e.sink.hook
	e.sink.hook = func(typ string, data json.RawMessage) {
		if prev != nil {
			prev(typ, data)
		}
		var step struct{ Name string }
		if typ == "step" && json.Unmarshal(data, &step) == nil && step.Name == name {
			f()
		}
	}
}

// outcomeFile is recovery/outcome.json of the attempt, with its mode, or the error of reading it.
func outcomeFile(e *env) (recovery.Outcome, os.FileMode, error) {
	path := filepath.Join(e.dir, recovery.DirName, recovery.OutcomeFile)
	info, err := os.Stat(path)
	if err != nil {
		return recovery.Outcome{}, 0, err
	}
	o, err := recovery.NewStore(e.dir).LoadOutcome()
	return o, info.Mode().Perm(), err
}

// storedOutcome is the outcome the attempt has stored.
func storedOutcome(t *testing.T, e *env) recovery.Outcome {
	t.Helper()
	o, _, err := outcomeFile(e)
	if err != nil {
		t.Fatalf("recovery/outcome.json cannot be read: %v", err)
	}
	return o
}

// withoutWarning is the envelope without the warnings of the code, and how many it had.
func withoutWarning(t *testing.T, data []byte, code string) (env map[string]any, n int) {
	t.Helper()
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("the envelope does not parse: %v", err)
	}
	var kept []any
	list, _ := env["warnings"].([]any)
	for _, w := range list {
		if m, _ := w.(map[string]any); m["code"] == code {
			n++
			continue
		}
		kept = append(kept, w)
	}
	env["warnings"] = kept
	return env, n
}

// forgeWrites counts the writes to the forge among the requests: those that are not reads of an issue, and the pull
// requests.
func forgeWrites(reqs []fakehub.Request) int {
	n := 0
	for _, r := range reqs {
		if r.Method != "GET" && (strings.Contains(r.Path, "/issues/") || strings.HasSuffix(r.Path, "/pulls")) {
			n++
		}
	}
	return n
}

// markedComments counts the comments of issue 42 that carry the marker of an attempt.
func markedComments(e *env) int {
	n := 0
	for _, c := range e.issueNow().Comments {
		if strings.Contains(c.Body, "<!-- agent-fox job=") {
			n++
		}
	}
	return n
}

// TestTheOutcomeIsStoredNotMirroredBeforeTheMirrorAndMirroredBeforeTheRelease_TS_14_39 verifies TS-14-39.
// Verifies: 14-REQ-6.1
func TestTheOutcomeIsStoredNotMirroredBeforeTheMirrorAndMirroredBeforeTheRelease_TS_14_39(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	var before, after recovery.Outcome
	var mode os.FileMode
	var atMirror, atRelease recovery.Journal
	var errBefore, errAfter error
	e.onStep("mirror", func() {
		before, _, errBefore = outcomeFile(e)
		atMirror = journalOf(t, e)
	})
	e.onStep("release", func() {
		after, mode, errAfter = outcomeFile(e)
		atRelease = journalOf(t, e)
	})

	res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if errBefore != nil || before.Mirrored || len(before.Envelope) == 0 || before.MaxAttempts == 0 || before.Report == "" {
		t.Errorf("at the mirror the outcome is %+v (%v), want one that holds the envelope, the report and the most attempts, not mirrored", before, errBefore)
	}
	if atMirror.Mirror.StatusLabels || atMirror.Mirror.Comment {
		t.Errorf("at the mirror the journal records %+v, want no step of it", atMirror.Mirror)
	}
	if errAfter != nil || !after.Mirrored || mode != 0o600 {
		t.Errorf("at the release the outcome is mirrored: %v (%v), mode %v, want mirrored and 0600", after.Mirrored, errAfter, mode)
	}
	if !atRelease.Mirror.StatusLabels || !atRelease.Mirror.Comment {
		t.Errorf("at the release the journal records %+v, want both steps of the mirror", atRelease.Mirror)
	}
	if strings.Contains(before.Envelope, `"hub_session"`) || !strings.Contains(after.Envelope, `"hub_session"`) {
		t.Errorf("the envelope was amended by the mirror: before %t, after %t, want the second write to have what the mirror added", strings.Contains(before.Envelope, `"hub_session"`), strings.Contains(after.Envelope, `"hub_session"`))
	}
	if !slices.Contains(after.LabelsSet, "af:landed") || !slices.Contains(after.LabelsRemoved, "af:in-progress") {
		t.Errorf("the labels set %v and removed %v, want landed set and in-progress removed", after.LabelsSet, after.LabelsRemoved)
	}
	if string(res.Envelope) != after.Envelope {
		t.Errorf("the envelope returned is not the one that was stored as mirrored")
	}
}

// TestEachStepOfTheMirrorIsJournalledWhenItIsDone_TS_14_39 pins that the comment and the labels are recorded one
// by one: a delivery that dies between them has the first recorded and not the second, and the next one makes the
// second alone.
// Verifies: 14-REQ-6.1, 14-REQ-6.2
func TestEachStepOfTheMirrorIsJournalledWhenItIsDone_TS_14_39(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.stopAfter("mirror_comment")
	j := journalOf(t, e)
	if !j.Mirror.Comment || j.Mirror.StatusLabels {
		t.Fatalf("the journal records %+v, want the comment and not the labels", j.Mirror)
	}
	if stored := storedOutcome(t, e); stored.Mirrored {
		t.Fatalf("the outcome is mirrored after a delivery that died in the mirror")
	}

	res, err := e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	issue := e.issueNow()
	if !slices.Contains(issue.Labels, "af:landed") || slices.Contains(issue.Labels, "af:in-progress") || markedComments(e) != 1 {
		t.Errorf("issue 42 has the labels %v and %d comments, want landed, in-progress off, and one comment", issue.Labels, markedComments(e))
	}
	if !storedOutcome(t, e).Mirrored {
		t.Error("the outcome is not mirrored after the delivery that finished the mirror")
	}
}

// TestAStoredOutcomeIsReturnedWithOnlyAttemptResumedAddedAndNoLoopNoLeaseAndNoNewSession_TS_14_40 verifies
// TS-14-40.
// Verifies: 14-REQ-6.2
func TestAStoredOutcomeIsReturnedWithOnlyAttemptResumedAddedAndNoLoopNoLeaseAndNoNewSession_TS_14_40(t *testing.T) {
	cases := map[string]struct {
		stage  string
		steps  []map[string]any
		mutate func(*jobs.Request)
		writes bool // the delivery writes to the forge: the mirror is not done
		mirror bool // the delivery goes through the mirror, though it may find nothing to write: the outcome is not marked mirrored
		// usageFails makes hub refuse the usage of the session, which the delivery makes again.
		usageFails bool
	}{
		"stopped before the mirror":             {stage: "outcome_stored", steps: fixSteps(), writes: true, mirror: true},
		"stopped in the mirror":                 {stage: "mirror_comment", steps: fixSteps(), writes: true, mirror: true},
		"stopped with the mirror done":          {stage: "mirror_labels", steps: fixSteps(), mirror: true},
		"stopped after the mirror":              {stage: "mirrored", steps: fixSteps()},
		"stopped after the release":             {stage: "released", steps: fixSteps()},
		"stopped after the usage":               {stage: "usage_posted", steps: fixSteps()},
		"stopped after the events":              {stage: "events_posted", steps: fixSteps()},
		"stopped, and hub refuses the usage":    {stage: "outcome_stored", steps: fixSteps(), writes: true, mirror: true, usageFails: true},
		"a failed attempt, no mirror":           {stage: "outcome_stored", mutate: func(r *jobs.Request) { r.Round = 2 }, writes: true, mirror: true},
		"a failed attempt, mirror done":         {stage: "mirrored", mutate: func(r *jobs.Request) { r.Round = 2 }},
		"a failed attempt that another follows": {stage: "outcome_stored", writes: true, mirror: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := loopEnv(t, fixFiles, nil, nil, c.steps...)
			var mutate []func(*jobs.Request)
			if c.mutate != nil {
				mutate = append(mutate, c.mutate)
			}
			e.stopAfter(c.stage, mutate...)
			stored := storedOutcome(t, e)
			leases, runs, sessions, requests := len(e.hub.Leases()), e.brainRuns(), len(e.hub.RequestsTo("POST", "/api/v1/sessions")), e.hub.Total()
			mirrors := 0
			e.onStep("mirror", func() { mirrors++ })
			if c.usageFails {
				e.hub.Inject("POST", "/api/v1/sessions/"+e.hub.Sessions()[0].ID+"/usage", fakehub.Fault{Status: 500})
			}

			res, err := e.deliver(2, mutate...)

			if err != nil {
				t.Fatal(err)
			}
			got, n := withoutWarning(t, res.Envelope, WarnAttemptResumed)
			want, _ := withoutWarning(t, []byte(stored.Envelope), WarnAttemptResumed)
			if n != 1 || !reflect.DeepEqual(got, want) {
				t.Errorf("the envelope has %d attempt_resumed warnings and is\n%s\nwant the stored one\n%s", n, res.Envelope, stored.Envelope)
			}
			if got := e.brainRuns() - runs; got != 0 {
				t.Errorf("the brain was started %d times, want never", got)
			}
			if got := len(e.hub.Leases()) - leases; got != 0 {
				t.Errorf("%d leases were taken, want none", got)
			}
			if got := len(e.hub.RequestsTo("POST", "/api/v1/sessions")) - sessions; got != 0 || len(e.hub.Sessions()) != 1 {
				t.Errorf("%d sessions were opened, want the one of the first delivery to be reused", got)
			}
			for _, lease := range e.hub.Leases() {
				if !lease.Released {
					t.Errorf("the lease %s is still held", lease.ID)
				}
			}
			wantUsage := 1
			if c.usageFails {
				wantUsage = 0
			}
			if s := e.hub.Sessions()[0]; s.Status == "open" || s.CompletedSeq == 0 || len(s.Usage) != wantUsage {
				t.Errorf("the session is %+v, want it completed once with %d usage records", s, wantUsage)
			}
			if (mirrors > 0) != c.mirror {
				t.Errorf("the delivery went through the mirror %d times, want some: %v", mirrors, c.mirror)
			}
			if markedComments(e) > 1 {
				t.Errorf("issue 42 has %d comments with the marker, want at most one", markedComments(e))
			}
			if writes := forgeWrites(e.hub.Requests()[requests:]); (writes > 0) != c.writes {
				t.Errorf("the delivery made %d writes to the forge, want some: %v", writes, c.writes)
			}
			if issue := e.issueNow(); slices.Contains(issue.Labels, "af:in-progress") {
				t.Errorf("issue 42 has the labels %v, want in-progress off", issue.Labels)
			}
			if status := got["status"]; status == "done" {
				if issue := e.issueNow(); !slices.Contains(issue.Labels, "af:landed") || slices.Contains(issue.Labels, "af:in-progress") {
					t.Errorf("issue 42 has the labels %v, want landed and in-progress off", issue.Labels)
				}
			}
			if o := storedOutcome(t, e); !o.Mirrored {
				t.Error("the outcome is not marked mirrored after the delivery that found it")
			}
		})
	}
}

// TestOnlyTheDeliveryThatMakesTheOutcomePostsTheEventsAndCompletesTheSession_TS_14_41 verifies TS-14-41.
// Verifies: 14-REQ-6.3
func TestOnlyTheDeliveryThatMakesTheOutcomePostsTheEventsAndCompletesTheSession_TS_14_41(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "begun")
	e := loopEnv(t, blockFiles(marker), nil, nil, blockStep())
	begun := func() bool { _, err := os.Stat(marker); return err == nil }

	_, err, _, _ := runUntil(t, e, e.request(jobs.KindFix, issueInput("42")), begun, jobs.ErrShutdown)
	if !errors.Is(err, jobs.ErrShutdown) {
		t.Fatalf("delivery 1 returned %v, want the shutdown", err)
	}
	e.hub.Gateway().RefuseBoxes("no room for another box")
	if _, err := e.deliver(2); !jobs.IsTransient(err) {
		t.Fatalf("delivery 2 returned %v, want a transient error", err)
	}
	e.hub.Gateway().RefuseBoxes("")
	e.useScript(scriptFile(t, fixSteps()...))

	res, err := e.deliver(3)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("delivery 3: result %s, error %v", res.Envelope, err)
	}
	session, batches := e.postedEvents(t)
	if len(batches) != 1 {
		t.Errorf("%d batches of events were posted, want one sequence", len(batches))
	}
	if session.Status != "completed" || session.CompletedSeq == 0 || len(session.Usage) != 1 {
		t.Errorf("the session is %+v, want it completed once with one usage record", session)
	}
}

// TestUsageIsPostedOnceWithTheAttemptsTotalWhateverTheDeliveriesStopAt_TS_14_42 verifies TS-14-42, a property: for
// deliveries stopped at generated stages of the end of an attempt, and then finished, the hub holds exactly one usage
// record, with the total of the attempt, and one sequence of events, and the session is completed.
// Verifies: 14-REQ-6.4, 14-REQ-6.3
func TestUsageIsPostedOnceWithTheAttemptsTotalWhateverTheDeliveriesStopAt_TS_14_42(t *testing.T) {
	rng := rand.New(rand.NewSource(14042))
	stages := []string{"outcome_stored", "mirror_comment", "mirror_labels", "mirrored", "released", "usage_posted", "events_posted", "session_completed"}
	for i := range 6 {
		var plan []string
		for range rng.Intn(3) + 1 {
			plan = append(plan, stages[rng.Intn(len(stages))])
		}
		t.Run(fmt.Sprintf("a fix %d: %v", i, plan), func(t *testing.T) {
			e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
			var last jobs.Result
			delivery := 0
			for {
				delivery++
				if delivery > len(plan)+3 {
					t.Fatalf("the attempt did not come to an end in %d deliveries", delivery-1)
				}
				e.runner.abortAfter = ""
				if delivery <= len(plan) {
					e.runner.abortAfter = plan[delivery-1]
				}
				res, err := e.runner.Run(context.Background(), e.request(jobs.KindFix, issueInput("42"), func(r *jobs.Request) { r.Delivery = delivery }))
				var stopped stageAbort
				if errors.As(err, &stopped) {
					continue
				}
				if err != nil {
					t.Fatalf("delivery %d: %v", delivery, err)
				}
				last = res
				break
			}
			e.runner.abortAfter = ""
			if delivery < 2 {
				t.Fatalf("the first delivery was not stopped after %s: it is a stage of the end of every attempt", plan[0])
			}

			sessions := e.hub.Sessions()
			if len(sessions) != 1 {
				t.Fatalf("%d sessions, want 1", len(sessions))
			}
			s := sessions[0]
			var env struct {
				Usage struct {
					Turns       int     `json:"turns"`
					InputTokens int64   `json:"input_tokens"`
					CostUSD     float64 `json:"cost_usd"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(last.Envelope, &env); err != nil {
				t.Fatal(err)
			}
			if len(s.Usage) != 1 || s.Usage[0].Turns != env.Usage.Turns || s.Usage[0].CostUSD != env.Usage.CostUSD || s.Usage[0].InputTokens != env.Usage.InputTokens {
				t.Errorf("the hub holds the usage %+v, want exactly one record with the total of the attempt %+v", s.Usage, env.Usage)
			}
			if s.Status != "completed" {
				t.Errorf("the session is %q, want it completed", s.Status)
			}
			if batches := e.hub.EventBatches(slug, s.RunID); len(batches) != 1 {
				t.Errorf("%d batches of events, want one sequence", len(batches))
			}
			if markedComments(e) != 1 {
				t.Errorf("issue 42 has %d comments with the marker, want one", markedComments(e))
			}
		})
	}
}

// TestAResumedDeliveryEmitsAResumeStepAndItsEnvelopeSaysWhatItResumedFrom_TS_14_57 verifies TS-14-57.
// Verifies: 14-REQ-9.1, 14-REQ-9.3
func TestAResumedDeliveryEmitsAResumeStepAndItsEnvelopeSaysWhatItResumedFrom_TS_14_57(t *testing.T) {
	code := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	cases := map[string]struct {
		stage   string
		skipped bool
		reason  string
		steps   []map[string]any
		prepare func(*testing.T, *env)
		extra   string // a warning that is there as well
	}{
		"the brain runs again": {stage: "briefed", steps: fixSteps()},
		"a discarded resume": {stage: "briefed", steps: fixSteps(), reason: reasonForgeChanged, extra: WarnResumeDiscarded,
			prepare: func(t *testing.T, e *env) { editJournal(t, e, func(j *recovery.Journal) { j.Forge = "other/repo" }) }},
		"an applied change":  {stage: "applied", skipped: true, steps: fixSteps()},
		"a published change": {stage: "pushed", skipped: true, steps: fixSteps()},
		"a question":         {stage: "submission_accepted", skipped: true, steps: []map[string]any{submitStep(questionReport)}},
		"a stored outcome":   {stage: "mirrored", skipped: true, steps: fixSteps()},
	}
	wantStage := map[string]string{
		"the brain runs again": stageBrain, "a discarded resume": stageBrain, "an applied change": stageApplied,
		"a published change": stagePublished, "a question": stageSubmitted, "a stored outcome": stageOutcome,
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := loopEnv(t, fixFiles, nil, nil, c.steps...)
			e.stopAfter(c.stage)
			if c.prepare != nil {
				c.prepare(t, e)
			}
			if got := countEvents(e, "step"); got == 0 {
				t.Fatal("the first delivery emitted no step")
			}
			before := len(resumeSteps(e))

			res, err := e.deliver(2)

			if err != nil {
				t.Fatal(err)
			}
			steps := resumeSteps(e)[before:]
			if len(steps) != 1 {
				t.Fatalf("the delivery emitted %d resume steps, want one", len(steps))
			}
			step := steps[0]
			if step["delivery"] != float64(2) || step["stage"] != wantStage[name] || step["brain_skipped"] != c.skipped || step["reason"] != orNil(c.reason) {
				t.Errorf("the resume step is %v, want delivery 2, stage %s, brain skipped %v, reason %q", step, wantStage[name], c.skipped, c.reason)
			}
			env := envelope(t, res)
			w := warningOf(env, WarnAttemptResumed)
			message, _ := w["message"].(string)
			skipped := "the brain ran"
			if c.skipped {
				skipped = "the brain was skipped"
			}
			if w == nil || !strings.Contains(message, "delivery 2") || !strings.Contains(message, "stage "+wantStage[name]) || !strings.Contains(message, skipped) {
				t.Errorf("the attempt_resumed warning is %v, want the handover count, the stage %s and %q", w, wantStage[name], skipped)
			}
			if c.extra != "" && warningOf(env, c.extra) == nil {
				t.Errorf("the envelope has no %s warning", c.extra)
			}
			list, _ := env["warnings"].([]any)
			for _, item := range list {
				if m, _ := item.(map[string]any); !code.MatchString(fmt.Sprint(m["code"])) {
					t.Errorf("the warning code %v does not match %s", m["code"], code)
				}
			}
		})
	}
}

// orNil is the value a step carries for a reason: absent when there is none.
func orNil(reason string) any {
	if reason == "" {
		return nil
	}
	return reason
}

// resumeSteps are the step events named resume that the environment has seen, as their data.
func resumeSteps(e *env) []map[string]any {
	var out []map[string]any
	for _, ev := range e.sink.emitted() {
		var data map[string]any
		if ev.Type == "step" && json.Unmarshal(ev.Data, &data) == nil && data["name"] == "resume" {
			out = append(out, data)
		}
	}
	return out
}

// TestAnOutcomeThatAJournalWhichCannotBeReadLeavesBehindIsNotTakenForTheNextDeliverysOwn pins that the attempt
// that is begun again after its journal was lost does not leave the outcome of the run that is gone for a
// later delivery to find.
// Verifies: 14-REQ-6.2
func TestAnOutcomeThatAJournalWhichCannotBeReadLeavesBehindIsNotTakenForTheNextDeliverysOwn(t *testing.T) {
	journal := func(e *env) string { return filepath.Join(e.dir, recovery.DirName, recovery.JournalFile) }
	for name, spoil := range map[string]func(*testing.T, *env){
		"the journal is gone": func(t *testing.T, e *env) {
			if err := os.Remove(journal(e)); err != nil {
				t.Fatal(err)
			}
		},
		"the journal is no journal": func(t *testing.T, e *env) {
			if err := os.WriteFile(journal(e), []byte("not a journal"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"the journal is another job's": func(t *testing.T, e *env) {
			editJournal(t, e, func(j *recovery.Journal) { j.Job = "job_ffffffffffffffffffff" })
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
			e.stopAfter("outcome_stored")
			spoil(t, e)
			e.hub.Gateway().RefuseBoxes("no room for another box")

			_, err := e.deliver(2)

			if !jobs.IsTransient(err) {
				t.Fatalf("delivery 2 returned %v, want a transient error", err)
			}
			if _, _, err := outcomeFile(e); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("recovery/outcome.json is there after a delivery that began again and did not come to an outcome (%v), want it gone", err)
			}
		})
	}
}

// TestAnOutcomeOfAnotherJobOrAnotherAttemptIsNotTakenForTheAttemptsOwn pins that a stored outcome is the
// attempt's only when it names its job and its attempt: the delivery picks up from what the clone holds for any other.
// Verifies: 14-REQ-6.2
func TestAnOutcomeOfAnotherJobOrAnotherAttemptIsNotTakenForTheAttemptsOwn(t *testing.T) {
	for name, change := range map[string]func(*recovery.Outcome){
		"another job":     func(o *recovery.Outcome) { o.Job = "job_ffffffffffffffffffff" },
		"another attempt": func(o *recovery.Outcome) { o.Attempt++ },
	} {
		t.Run(name, func(t *testing.T) {
			e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
			e.stopAfter("outcome_stored")
			o := storedOutcome(t, e)
			change(&o)
			if err := recovery.NewStore(e.dir).WriteOutcome(o); err != nil {
				t.Fatal(err)
			}

			res, err := e.deliver(2)

			if err != nil || envelope(t, res)["status"] != "done" {
				t.Fatalf("result %s, error %v", res.Envelope, err)
			}
			// The change was published by the first delivery, so that is what the delivery finds, and not an outcome.
			if steps := resumeSteps(e); len(steps) != 1 || steps[0]["stage"] != stagePublished {
				t.Errorf("the resume steps are %v, want one that picks up from the published change: the outcome is not the attempt's", steps)
			}
		})
	}
}

// TestATransientErrorThatCarriesAnEnvelopeStoresNoOutcome pins that an outcome is stored for an outcome only: a
// delivery that is handed over again is none, whatever it holds.
// Verifies: 14-REQ-1.6
func TestATransientErrorThatCarriesAnEnvelopeStoresNoOutcome(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.runner.loop = func(_ context.Context, a *attempt) *outcome {
		return &outcome{err: jobs.Transient(errors.New("the box was lost")), result: jobs.Result{Envelope: []byte(`{"status":"failed"}`)}}
	}

	_, err := e.run(e.request(jobs.KindFix, issueInput("42")))

	if !jobs.IsTransient(err) {
		t.Fatalf("the delivery returned %v, want a transient error", err)
	}
	if _, _, err := outcomeFile(e); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("recovery/outcome.json is there after a delivery that is handed over again (%v), want none", err)
	}
}
