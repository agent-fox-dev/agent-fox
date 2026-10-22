package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/catalog"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// emitting makes the loop of a test emit n events through the sink of the loop, as the shell does,
// and end with the envelope.
func (e *env) emitting(n int, kind jobs.Kind, status toolio.Status, mods ...func(*toolio.Envelope)) {
	e.runner.loop = func(_ context.Context, a *attempt) *outcome {
		a.step("run") // as the loop's step does
		sink := a.eventSink(e.runner.cfg.Now)
		for i := 1; i <= n; i++ {
			sink([]byte(fmt.Sprintf(`{"seq":%d,"ts":"2026-10-22T08:00:00.%03dZ","run_id":%q,"type":"tick","data":{"i":%d}}`, i, i%1000, a.runID, i)))
		}
		all := append([]func(*toolio.Envelope){fakerunner.WithTool(string(kind))}, mods...)
		return &outcome{result: jobs.Result{Envelope: fakerunner.Env(status, all...), MaxAttempts: a.maxAttempts}}
	}
}

// postedEvents are the events the hub took for the run of the only session, by batch.
func (e *env) postedEvents(t *testing.T) (session fakehub.Session, batches [][]fakehub.EventRecord) {
	t.Helper()
	sessions := e.hub.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("%d sessions, want 1", len(sessions))
	}
	return sessions[0], e.hub.EventBatches(slug, sessions[0].RunID)
}

// TestAfterTheReleaseOneUsageRecordTheEventsInBatchesAndTheCompletionAreReported_TS_13_76 verifies
// TS-13-76: once the lease is released, one usage record with the model, the brain, the tokens, the
// cost, its source and the turns; the events of the attempt, 1200 of them, in batches of 500, 500
// and 200 in the order they happened; and the completion of the session with the status of the
// envelope, or failed under a transient error; and no usage record when no brain ran.
// Verifies: 13-REQ-10.3
func TestAfterTheReleaseOneUsageRecordTheEventsInBatchesAndTheCompletionAreReported_TS_13_76(t *testing.T) {
	usage := toolio.Usage{Brain: "scripted", CostUSD: 1.25, CostSource: toolio.CostMeasured, InputTokens: 1000, OutputTokens: 200, Turns: 7}

	t.Run("a done attempt with a brain", func(t *testing.T) {
		e := mirrorEnv(t, nil, map[string]string{"AF_MODEL": "model-1"})
		// The worker's own steps are 8: resolve, session, clone, lease, run, mirror, release and report.
		e.emitting(1192, jobs.KindFix, toolio.StatusDone, withResult(fixResult("landed", "pr", true)), fakerunner.WithUsage(usage))

		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		if err != nil || envelope(t, res)["status"] != "done" {
			t.Fatalf("result %s, error %v", res.Envelope, err)
		}
		session, batches := e.postedEvents(t)
		var sizes []int
		var ids []string
		for _, b := range batches {
			sizes = append(sizes, len(b))
			for _, ev := range b {
				ids = append(ids, ev.ID)
			}
		}
		if !slices.Equal(sizes, []int{500, 500, 200}) {
			t.Errorf("batches of %v events, want 500, 500 and 200", sizes)
		}
		for i := range ids {
			if want := fmt.Sprintf("%s-%d", session.RunID, i+1); ids[i] != want {
				t.Fatalf("event %d has the id %s, want %s: the events are posted in the order they happened", i, ids[i], want)
			}
		}
		if len(session.Usage) != 1 {
			t.Fatalf("%d usage records, want one", len(session.Usage))
		}
		if got, want := session.Usage[0], (fakehub.Usage{Model: "model-1", Brain: "scripted", CostUSD: 1.25, CostSource: "measured", InputTokens: 1000, OutputTokens: 200, Turns: 7}); got != want {
			t.Errorf("usage = %+v, want %+v", got, want)
		}
		if session.Model != "model-1" || session.Status != "completed" {
			t.Errorf("session model %q, status %q, want model-1 and completed", session.Model, session.Status)
		}
		// The order: the release, then the usage, then the events, then the completion.
		lease := e.hub.Leases()[0]
		usageSeq := seqOf(e, "POST", "/api/v1/sessions/"+session.ID+"/usage", "")
		firstBatch := seqOf(e, "POST", "/api/v1/workspaces/acme/runs/", "/events/batch")
		if !(lease.ReleasedSeq < usageSeq && usageSeq < firstBatch && firstBatch < session.CompletedSeq) {
			t.Errorf("release %d, usage %d, events %d, completion %d: want that order", lease.ReleasedSeq, usageSeq, firstBatch, session.CompletedSeq)
		}
	})

	t.Run("no brain ran", func(t *testing.T) {
		e := mirrorEnv(t, nil, nil)
		e.endsWith(jobs.KindFix, toolio.StatusFailed, fakerunner.WithError("git", "the branch could not be pushed", false))

		if _, err := e.run(e.request(jobs.KindFix, issueInput("42"))); err != nil {
			t.Fatal(err)
		}
		session, _ := e.postedEvents(t)
		if len(session.Usage) != 0 || session.Status != "failed" {
			t.Errorf("usage %v, status %q, want no usage record and failed", session.Usage, session.Status)
		}
	})

	for name, tc := range map[string]struct {
		status toolio.Status
		mods   []func(*toolio.Envelope)
		want   string
	}{
		"unverified":  {toolio.StatusUnverified, []func(*toolio.Envelope){withResult(fixResult("landed", "pr", false))}, "completed"},
		"needs_human": {toolio.StatusNeedsHuman, []func(*toolio.Envelope){fakerunner.WithQuestion("Which?", "a", "b")}, "completed"},
		"failed":      {toolio.StatusFailed, []func(*toolio.Envelope){fakerunner.WithError("brain", "no", false)}, "failed"},
		"usage":       {toolio.StatusUsage, []func(*toolio.Envelope){fakerunner.WithError("usage", "no", false)}, "failed"},
		"timeout":     {toolio.StatusFailed, []func(*toolio.Envelope){fakerunner.WithError("timeout", "too slow", true)}, "timeout"},
	} {
		t.Run("the session of a "+name+" envelope", func(t *testing.T) {
			e := mirrorEnv(t, nil, nil)
			e.endsWith(jobs.KindFix, tc.status, tc.mods...)

			if _, err := e.run(e.request(jobs.KindFix, issueInput("42"))); err != nil {
				t.Fatal(err)
			}
			if session, _ := e.postedEvents(t); session.Status != tc.want {
				t.Errorf("the session was completed %q, want %q", session.Status, tc.want)
			}
		})
	}

	t.Run("a transient error", func(t *testing.T) {
		e := mirrorEnv(t, nil, nil)
		e.runner.loop = func(context.Context, *attempt) *outcome {
			return transientFailure(toolio.CategorySandbox, "the box was lost")
		}

		_, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		if !jobs.IsTransient(err) {
			t.Fatalf("error = %v", err)
		}
		// The delivery that comes to the outcome reports, and not the one that is handed over again
		// (14-REQ-1.6): the session stays open, with no usage and no events.
		session, batches := e.postedEvents(t)
		if session.Status != "open" || len(session.Usage) != 0 || len(batches) != 0 {
			t.Errorf("status %q, usage %v, %d batches of events, want the session open and nothing posted", session.Status, session.Usage, len(batches))
		}
	})

	t.Run("an attempt that stopped before the session reports nothing", func(t *testing.T) {
		e := mirrorEnv(t, nil, map[string]string{"AF_TIMEOUT": "never"})

		if _, err := e.run(e.request(jobs.KindFix, issueInput("42"))); err != nil {
			t.Fatal(err)
		}
		for _, r := range e.hub.Requests() {
			if strings.Contains(r.Path, "/usage") || strings.Contains(r.Path, "/events") || strings.Contains(r.Path, "/complete") || r.Path == sessionsPath {
				t.Errorf("%s %s: a report of an attempt that has no session", r.Method, r.Path)
			}
		}
	})
}

// TestAFailingUsageEventsOrCompleteCallAddsAWarningAndNothingElseChanges_TS_13_77 verifies
// TS-13-77.
// Verifies: 13-REQ-10.4
func TestAFailingUsageEventsOrCompleteCallAddsAWarningAndNothingElseChanges_TS_13_77(t *testing.T) {
	usage := toolio.Usage{Brain: "scripted", CostUSD: 0.5, CostSource: toolio.CostMeasured, InputTokens: 10, OutputTokens: 5, Turns: 2}
	for name, tc := range map[string]struct {
		path, warning string
		posted        func(t *testing.T, e *env)
	}{
		"the usage": {"/api/v1/sessions/ses_0001/usage", "hub_usage_failed", func(t *testing.T, e *env) {
			session, batches := e.postedEvents(t)
			if len(session.Usage) != 0 || len(batches) == 0 || session.Status != "completed" {
				t.Errorf("usage %v, %d batches, status %q: only the usage was lost", session.Usage, len(batches), session.Status)
			}
		}},
		"the events": {"/api/v1/workspaces/acme/runs/*", "hub_events_failed", func(t *testing.T, e *env) {
			session, batches := e.postedEvents(t)
			if len(session.Usage) != 1 || len(batches) != 0 || session.Status != "completed" {
				t.Errorf("usage %v, %d batches, status %q: only the events were lost", session.Usage, len(batches), session.Status)
			}
		}},
		"the completion": {"/api/v1/sessions/ses_0001/complete", "hub_session_failed", func(t *testing.T, e *env) {
			session, batches := e.postedEvents(t)
			if len(session.Usage) != 1 || len(batches) == 0 || session.Status != "open" {
				t.Errorf("usage %v, %d batches, status %q: only the completion was lost", session.Usage, len(batches), session.Status)
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			e := mirrorEnv(t, nil, nil)
			e.endsWith(jobs.KindFix, toolio.StatusDone, withResult(fixResult("landed", "pr", true)), fakerunner.WithUsage(usage))
			e.hub.Inject("POST", tc.path, fakehub.Fault{Status: 500})

			res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

			env := envelope(t, res)
			if err != nil || env["status"] != "done" || env["exit_code"] != float64(0) || env["error"] != nil {
				t.Fatalf("status %v, exit code %v, error %v (%v): the status must not change", env["status"], env["exit_code"], env["error"], err)
			}
			if got := codes(env); !slices.Contains(got, tc.warning) {
				t.Errorf("warnings = %v, want %s", got, tc.warning)
			}
			for _, other := range []string{"hub_usage_failed", "hub_events_failed", "hub_session_failed"} {
				if other != tc.warning && slices.Contains(codes(env), other) {
					t.Errorf("warnings = %v: %s is the warning of another call", codes(env), other)
				}
			}
			tc.posted(t, e)
		})
	}

	t.Run("under a transient error the session is not completed, so there is no failure to log", func(t *testing.T) {
		e := mirrorEnv(t, nil, nil)
		var logged bytes.Buffer
		e.runner.cfg.Logger = slog.New(slog.NewTextHandler(&logged, nil))
		e.hub.Inject("POST", "/api/v1/sessions/ses_0001/complete", fakehub.Fault{Status: 500})
		e.runner.loop = func(context.Context, *attempt) *outcome {
			return transientFailure(toolio.CategorySandbox, "the box was lost")
		}

		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		if !jobs.IsTransient(err) || len(res.Envelope) != 0 {
			t.Fatalf("result %q, error %v, want the transient error as it was", res.Envelope, err)
		}
		if n := len(e.hub.RequestsTo("POST", "/api/v1/sessions/ses_0001/complete")); n != 0 {
			t.Errorf("the session was asked to complete %d times under a transient error, want never (14-REQ-1.6)", n)
		}
		if out := logged.String(); strings.Contains(out, pat) {
			t.Errorf("log = %q, want no token", out)
		}
	})
}

// TestTheReturnedEnvelopeDiffersFromTheLoopsOnlyByWhatWasAdded_TS_13_65 verifies TS-13-65: of the
// envelope the shell wrote, every field is as it was but the warnings, the side effects and the
// artifacts, which only gained entries; the result holds the report of the run or nothing, the
// labels, and AF_MAX_ATTEMPTS, or 2 when the attempt stopped before the variables were read.
// Verifies: 13-REQ-8.8
func TestTheReturnedEnvelopeDiffersFromTheLoopsOnlyByWhatWasAdded_TS_13_65(t *testing.T) {
	t.Run("a fix", func(t *testing.T) {
		e := loopEnv(t, fixFiles, map[string]string{"AF_MAX_ATTEMPTS": "4"}, nil, fixSteps()...)
		var was []byte
		var report []byte
		e.sink.hook = func(typ string, data json.RawMessage) {
			var step struct{ Name string }
			if typ != "step" || json.Unmarshal(data, &step) != nil || step.Name != "release" {
				return
			}
			dir := filepath.Join(e.dir, "clone", ".agent-fox", "run", e.sink.lastHub()[1])
			was = readFileOrNil(filepath.Join(dir, "envelope.json"))
			report = readFileOrNil(filepath.Join(dir, "report.md"))
		}

		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		if err != nil || len(was) == 0 {
			t.Fatalf("error %v, the envelope of the loop: %d bytes", err, len(was))
		}
		var before, after map[string]json.RawMessage
		if json.Unmarshal(was, &before) != nil || json.Unmarshal(res.Envelope, &after) != nil {
			t.Fatal("an envelope is not JSON")
		}
		for field, raw := range before {
			switch field {
			case "warnings", "artifacts", "side_effects":
				var was, now []json.RawMessage
				_ = json.Unmarshal(raw, &was)
				_ = json.Unmarshal(after[field], &now)
				if len(now) < len(was) || !slices.EqualFunc(was, now[:len(was)], func(a, b json.RawMessage) bool { return jsonEqual(a, b) }) {
					t.Errorf("%s: %s was %s, and the entries that were there are not all there in the same order", field, field, raw)
				}
			default:
				if !bytes.Equal(raw, after[field]) {
					t.Errorf("%s changed: %s, was %s", field, after[field], raw)
				}
			}
		}
		if len(after) != len(before) {
			t.Errorf("the fields are %d, were %d", len(after), len(before))
		}
		if res.MaxAttempts != 4 || res.Report == "" || res.Report != string(report) {
			t.Errorf("max attempts %d, report %d bytes (the report of the run is %d), want 4 and the report of the run", res.MaxAttempts, len(res.Report), len(report))
		}
		if !slices.Equal(res.LabelsRemoved, []string{"af:in-progress"}) || !slices.Equal(res.LabelsSet, []string{"af:landed"}) {
			t.Errorf("labels set %v removed %v, want landed set and in-progress removed", res.LabelsSet, res.LabelsRemoved)
		}
	})

	t.Run("a triage has no report", func(t *testing.T) {
		e := loopEnv(t, triageFiles, nil, nil, submitStep(probableDiagnosis))

		res, err := e.run(e.request(jobs.KindTriage, issueInput("42")))

		if err != nil || envelope(t, res)["status"] != "done" || res.Report != "" || res.MaxAttempts != 2 {
			t.Errorf("status %v, report %q, max attempts %d, error %v, want a done triage with no report and 2", envelope(t, res)["status"], res.Report, res.MaxAttempts, err)
		}
	})

	t.Run("an attempt that stopped before the variables were read", func(t *testing.T) {
		e := leaseEnv(t, nil)
		delete(e.subs.tokens, slug)

		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		if err != nil || res.MaxAttempts != 2 || res.Report != "" || len(res.LabelsSet) != 0 || len(res.LabelsRemoved) != 0 {
			t.Errorf("max attempts %d, report %q, labels %v %v, error %v, want 2, nothing and nothing", res.MaxAttempts, res.Report, res.LabelsSet, res.LabelsRemoved, err)
		}
	})
}

func readFileOrNil(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}

var runIDForm = regexp.MustCompile(`^[0-9]{8}_[0-9]{6}_[0-9a-f]{6}$`)

// TestTheSessionsModelTheRunIDAndTheBrainsModelAgree_TS_13_74 verifies the part of TS-13-74 that
// follows from the loop and the report: the model of the session is the catalog's default or
// AF_MODEL, the usage record carries it, the brain is given it, the run id is in the form and is
// the one of the envelope, and the session opens before the mark.
// Verifies: 13-REQ-10.1
func TestTheSessionsModelTheRunIDAndTheBrainsModelAgree_TS_13_74(t *testing.T) {
	def := catalog.Default().DefaultModelID("anthropic")
	for name, tc := range map[string]struct {
		vars  map[string]string
		model string
	}{"AF_MODEL unset": {nil, def}, "AF_MODEL set": {map[string]string{"AF_MODEL": "m1"}, "m1"}} {
		t.Run(name, func(t *testing.T) {
			e := mirrorEnv(t, nil, tc.vars)
			usage := toolio.Usage{Brain: "scripted", CostSource: toolio.CostMeasured, InputTokens: 1, OutputTokens: 1, Turns: 1}
			var given string
			e.runner.loop = func(_ context.Context, a *attempt) *outcome {
				given = e.runner.loopConfig(a).Brain.Model
				return &outcome{result: jobs.Result{Envelope: fakerunner.Env(toolio.StatusDone, fakerunner.WithTool("fix"), withResult(fixResult("landed", "pr", true)), fakerunner.WithUsage(usage),
					func(env *toolio.Envelope) { id := a.runID; env.RunID = &id }), MaxAttempts: a.maxAttempts}}
			}

			res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

			if err != nil {
				t.Fatal(err)
			}
			session, _ := e.postedEvents(t)
			if session.Model != tc.model || given != tc.model || len(session.Usage) != 1 || session.Usage[0].Model != tc.model {
				t.Errorf("the session has the model %q, the brain was given %q, the usage record has %+v; want %q for all", session.Model, given, session.Usage, tc.model)
			}
			if !runIDForm.MatchString(session.RunID) || envelope(t, res)["run_id"] != session.RunID {
				t.Errorf("the run id of the session is %q and the envelope's is %v", session.RunID, envelope(t, res)["run_id"])
			}
			if mark := seqOf(e, "POST", issuePath+"/labels", ""); !(session.Seq < mark) {
				t.Errorf("the session opened at %d and the issue was marked at %d: the session opens first", session.Seq, mark)
			}
		})
	}
}

// TestAFailedBatchEndsTheEventsAndALongFailureIsCutAndALineWithoutATimeIsDated pins three details
// of the report: after a batch that hub did not take the later ones are not posted, so that what hub
// has is the first of the events and no other; the message that completes a session is cut at 500
// bytes; and an event of the loop without a time is given the time it was kept.
// Verifies: 13-REQ-10.3, 13-REQ-10.4
func TestAFailedBatchEndsTheEventsAndALongFailureIsCutAndALineWithoutATimeIsDated(t *testing.T) {
	t.Run("a failed batch", func(t *testing.T) {
		e := mirrorEnv(t, nil, nil)
		e.hub.Inject("POST", "/api/v1/workspaces/acme/runs/*", fakehub.Fault{Status: 500, Skip: 1})
		e.emitting(1192, jobs.KindFix, toolio.StatusDone, withResult(fixResult("landed", "pr", true)))

		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		if err != nil {
			t.Fatal(err)
		}
		_, batches := e.postedEvents(t)
		if len(batches) != 1 || len(batches[0]) != 500 {
			t.Errorf("%d batches were taken, want the first alone", len(batches))
		}
		var failed int
		for _, code := range codes(envelope(t, res)) {
			if code == "hub_events_failed" {
				failed++
			}
		}
		if failed != 1 {
			t.Errorf("warnings = %v, want hub_events_failed once", codes(envelope(t, res)))
		}
	})

	t.Run("a long failure", func(t *testing.T) {
		e := mirrorEnv(t, nil, nil)
		e.endsWith(jobs.KindFix, toolio.StatusFailed, fakerunner.WithError("git", strings.Repeat("x", 2000), false))

		if _, err := e.run(e.request(jobs.KindFix, issueInput("42"))); err != nil {
			t.Fatal(err)
		}
		if session, _ := e.postedEvents(t); session.Status != "failed" || len(session.Message) != 500 {
			t.Errorf("status %q, a message of %d bytes, want failed and 500", session.Status, len(session.Message))
		}
	})

	t.Run("a line without a time", func(t *testing.T) {
		e := mirrorEnv(t, nil, nil)
		e.runner.loop = func(_ context.Context, a *attempt) *outcome {
			a.eventSink(e.runner.cfg.Now)([]byte(`{"seq":1,"run_id":"x","type":"tick","data":{}}`))
			return &outcome{result: jobs.Result{Envelope: fakerunner.Env(toolio.StatusDone, fakerunner.WithTool("fix"), withResult(fixResult("landed", "pr", true))), MaxAttempts: a.maxAttempts}}
		}

		if _, err := e.run(e.request(jobs.KindFix, issueInput("42"))); err != nil {
			t.Fatal(err)
		}
		_, batches := e.postedEvents(t)
		var checked bool
		for _, b := range batches {
			for _, ev := range b {
				if ev.Type == "tick" {
					checked = true
					if _, err := time.Parse(time.RFC3339Nano, ev.Timestamp); err != nil {
						t.Errorf("the event has the time %q: %v", ev.Timestamp, err)
					}
				}
			}
		}
		if !checked {
			t.Error("the event of the loop was not posted")
		}
	})
}
