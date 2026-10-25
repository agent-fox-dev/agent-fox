package jobs_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// runEach makes one fix job of widgets for each step, which the runner does in turn, one at a time, and returns the
// jobs once they are at rest.
func runEach(t *testing.T, steps ...fakerunner.Step) (*env, []jobs.Doc) {
	t.Helper()
	e := newEnv(t, withRunner(fakerunner.New(steps...)), withWorkers(1))
	var ids []string
	for i := range steps {
		ids = append(ids, e.create("widgets", i+1, "").ID)
	}
	e.start()
	var docs []jobs.Doc
	for _, id := range ids {
		docs = append(docs, e.waitRest(id))
	}
	return e, docs
}

func TestFinish_StoresTheEnvelopeAndTheReportAsGivenAndTheJobTakesWhatTheyCarry(t *testing.T) {
	artifacts := []toolio.Artifact{{Kind: "pull_request", URL: "https://github.example.com/acme/widgets/pull/9", Number: 9}, {Kind: "branch", Name: "af/fix-1"}}
	effects := []toolio.SideEffect{{Kind: "label", Target: "acme/widgets#1", State: toolio.StateDone}}
	usage := toolio.Usage{Brain: "scripted", CostUSD: 1.25, CostSource: toolio.CostMeasured, InputTokens: 1000, OutputTokens: 200, Turns: 7}
	// A field that toolio.Envelope does not know, in an order of its own: decoded and written again it would change.
	given := bytes.Replace(fakerunner.Env(toolio.StatusDone, fakerunner.WithSummary("fixed it"), fakerunner.WithArtifacts(artifacts...),
		fakerunner.WithSideEffects(effects...), fakerunner.WithUsage(usage)),
		[]byte(`"status": "done",`), []byte(`"x_extension": {"z": 1, "a": [1, 2]}, "status": "done",`), 1)
	bare := fakerunner.Env(toolio.StatusDone)
	var kept *jobs.Recorder
	e, docs := runEach(t,
		fakerunner.Do(func(_ context.Context, req jobs.Request) (jobs.Result, error) {
			kept = req.Recorder
			return jobs.Result{Envelope: given, Report: "# Report\n", LabelsSet: []string{"af:fixed"}, LabelsRemoved: []string{"af:fix"}}, nil
		}),
		fakerunner.Return(jobs.Result{Envelope: bare}),
	)
	doc, other := docs[0], docs[1]
	for id, want := range map[string][]byte{doc.ID: given, other.ID: bare} {
		path := e.file(id, 1, "envelope.json")
		if got := testutil.ReadFile(t, path); got != string(want) {
			t.Errorf("envelope.json = %.60q, want the bytes the runner gave", got)
		}
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Errorf("envelope.json has the mode %v, want 0600", info.Mode().Perm())
		}
	}
	if got := testutil.ReadFile(t, e.file(doc.ID, 1, "report.md")); got != "# Report\n" {
		t.Errorf("report.md = %q", got)
	}
	if _, err := os.Stat(e.file(other.ID, 1, "report.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a report.md was written for no report: %v", err)
	}
	evs := e.events(doc.ID)
	end, last := evs[len(evs)-2], evs[len(evs)-1]
	if end.Type != jobs.EventAttemptEnd || string(end.Data) != `{"attempt":1,"exit_code":0,"state":"done","status":"done"}` {
		t.Errorf("the event before the last = %s %s, want attempt_end of a done attempt", end.Type, end.Data)
	}
	if last.Type != jobs.EventJobState || !strings.Contains(string(last.Data), `"state":"done","attempt":1,"at_rest":true,"result":{"status":"done","exit_code":0,"summary":"fixed it"`) {
		t.Errorf("the last event = %s %s, want job_state done at rest with its result", last.Type, last.Data)
	}
	if doc.State != jobs.StateDone || doc.StartedAt == nil || doc.FinishedAt == nil || doc.Result == nil || doc.Result.Summary != "fixed it" || doc.Result.Error != nil {
		t.Errorf("job = %s, result %+v", doc.State, doc.Result)
	}
	if !slices.Equal(doc.Artifacts, artifacts) || !slices.Equal(doc.SideEffects, effects) || doc.Usage != (jobs.UsageDoc{CostUSD: 1.25, InputTokens: 1000, OutputTokens: 200, Turns: 7}) {
		t.Errorf("artifacts %+v, side effects %+v, usage %+v, want those of the envelope", doc.Artifacts, doc.SideEffects, doc.Usage)
	}
	if doc.Labels == nil || !slices.Equal(doc.Labels.Set, []string{"af:fixed"}) || !slices.Equal(doc.Labels.Removed, []string{"af:fix"}) || doc.Labels.Restore != nil {
		t.Errorf("labels = %+v, want what the runner reported", doc.Labels)
	}
	if a := doc.Attempts[0]; a.State != jobs.AttemptDone || *a.Status != toolio.StatusDone || *a.ExitCode != 0 || a.Usage.Brain != "scripted" || a.FinishedAt == nil {
		t.Errorf("attempt = %+v", a)
	}
	// A runner that kept its recorder changes nothing once its attempt has ended.
	before := e.snapshot(doc.ID)
	kept.Emit("late", 1)
	kept.Hub("session", "run", "box")
	if after := e.snapshot(doc.ID); after != before {
		t.Errorf("a late report changed the job:\n%s\n%s", before, after)
	}
	if other.Labels != nil || other.Usage != (jobs.UsageDoc{}) || string(testutil.JSON(t, other.Artifacts)) != "[]" {
		t.Errorf("a job with nothing to show: labels %+v, usage %+v, artifacts %s", other.Labels, other.Usage, testutil.JSON(t, other.Artifacts))
	}
}

func TestFinish_TheEnvelopeDecidesWhatBecomesOfTheJob(t *testing.T) {
	failed := func(category string, retryable bool) fakerunner.Step { return fakerunner.Failed(category, retryable) }
	cases := []struct {
		name    string
		step    fakerunner.Step
		state   jobs.State
		attempt jobs.AttemptState
		code    int
	}{
		{"done", fakerunner.Done(), jobs.StateDone, jobs.AttemptDone, 0},
		{"unverified, landed", fakerunner.Envelope(fakerunner.Env(toolio.StatusUnverified, fakerunner.WithOutcome("landed"))), jobs.StateDone, jobs.AttemptDone, 4},
		{"unverified, no result", fakerunner.Envelope(fakerunner.Env(toolio.StatusUnverified)), jobs.StateDone, jobs.AttemptDone, 4},
		{"unverified, parked", fakerunner.Envelope(fakerunner.Env(toolio.StatusUnverified, fakerunner.WithOutcome("parked"))), jobs.StateFailed, jobs.AttemptFailed, 4},
		{"needs_human", question(), jobs.StateBlocked, jobs.AttemptNeedsHuman, 3},
		{"a retryable failure of the sandbox", failed("sandbox", true), jobs.StateFailed, jobs.AttemptFailed, 1},
		{"a retryable failure of the forge", failed("forge", true), jobs.StateFailed, jobs.AttemptFailed, 1},
		{"a retryable timeout", failed("timeout", true), jobs.StateFailed, jobs.AttemptFailed, 1},
		{"a failure of the brain that cannot retry", failed("brain", false), jobs.StateFailed, jobs.AttemptFailed, 1},
		{"a no_result that cannot retry", failed("no_result", false), jobs.StateFailed, jobs.AttemptFailed, 1},
		{"usage", fakerunner.Envelope(fakerunner.Env(toolio.StatusUsage, fakerunner.WithError("usage", "bad flag", false))), jobs.StateFailed, jobs.AttemptFailed, 2},
	}
	var steps []fakerunner.Step
	for _, tc := range cases {
		steps = append(steps, tc.step)
	}
	_, docs := runEach(t, steps...)
	for i, tc := range cases {
		d := docs[i]
		if d.State != tc.state || len(d.Attempts) != 1 || d.Attempts[0].State != tc.attempt || d.Result.ExitCode != tc.code || (d.FinishedAt != nil) != tc.state.Finished() {
			t.Errorf("%s: job %s, attempts %+v, result %+v, want %s, one attempt %s, exit code %d", tc.name, d.State, d.Attempts, d.Result, tc.state, tc.attempt, tc.code)
		}
	}
}

func TestFinish_ARetryableFailureOfTheBrainIsTriedAgainUpToTheRoundsLimit(t *testing.T) {
	for _, tc := range []struct{ max, attempts int }{{0, 2}, {1, 1}, {3, 3}, {99, 10}} {
		t.Run(fmt.Sprint("a limit of ", tc.max), func(t *testing.T) {
			var steps []fakerunner.Step
			for i := range 12 {
				steps = append(steps, failedBrain([]string{"brain", "no_result"}[i%2], true, tc.max))
			}
			e := newEnv(t, withRunner(fakerunner.New(steps...)))
			job := e.create("widgets", 1, "")
			e.start()
			doc := e.waitRest(job.ID)
			if doc.State != jobs.StateFailed || len(doc.Attempts) != tc.attempts || doc.FinishedAt == nil {
				t.Fatalf("job %s with %d attempts, want failed with %d", doc.State, len(doc.Attempts), tc.attempts)
			}
			want := []string{"queued:false", "running:false"}
			for n := 2; n <= tc.attempts; n++ {
				if doc.Attempts[n-1].Trigger != (jobs.Trigger{Kind: jobs.TriggerAuto, By: "agent-fox"}) || doc.Attempts[n-2].State != jobs.AttemptFailed {
					t.Errorf("attempt %d: %+v", n, doc.Attempts[n-1])
				}
				want = append(want, "queued:false", "running:false") // the job waits for the automatic attempt, not at rest
			}
			if got := e.states(job.ID); !slices.Equal(got, append(want, "failed:true")) {
				t.Errorf("job_state events %v, want %v", got, append(want, "failed:true"))
			}
		})
	}
}

func TestFinish_AnAttemptWithoutAUsableEnvelopeGetsAFailedOneOfTheLayersMaking(t *testing.T) {
	forge := &toolio.Failure{Category: toolio.CategoryForge, Message: "the forge hung up", Retryable: true, FixHint: &toolio.FixHint{Suggest: "try later"}}
	cases := []struct {
		name      string
		step      fakerunner.Step
		category  string
		retryable bool
		message   string
	}{
		{"a failure wrapped in an error", fakerunner.Fail(fmt.Errorf("landing: %w", forge)), "forge", true, "the forge hung up"},
		{"a plain error", fakerunner.Fail(errors.New("disk on fire")), "internal", false, "disk on fire"},
		{"a panic", fakerunner.Panic("bang"), "internal", false, "the runner panicked"},
		{"a status that is not one", fakerunner.Envelope(fakerunner.Env("running")), "internal", false, `the runner returned an envelope that cannot be used: its status "running" is not one of done, failed, usage, needs_human and unverified`},
		{"needs_human without a question", fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman)), "internal", false, "the runner returned an envelope that cannot be used: its status is needs_human and it has no question"},
		{"needs_human with a blank question", fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion(" \t", "a"))), "internal", false, ""},
		{"not JSON", fakerunner.Envelope([]byte("not json at all")), "internal", false, ""},
		{"no envelope", fakerunner.Return(jobs.Result{}), "internal", false, "the runner returned an envelope that cannot be used: the runner returned no envelope"},
	}
	var steps []fakerunner.Step
	for _, tc := range cases {
		steps = append(steps, tc.step)
	}
	e, docs := runEach(t, steps...)
	for i, tc := range cases {
		d := docs[i]
		raw := testutil.ReadFile(t, e.file(d.ID, 1, "envelope.json"))
		env := testutil.Decode[toolio.Envelope](t, []byte(raw))
		if d.State != jobs.StateFailed || len(d.Attempts) != 1 || env.SchemaVersion != toolio.SchemaVersion || env.Tool != "fix" || env.Status != toolio.StatusFailed || env.ExitCode != 1 {
			t.Errorf("%s: job %s with %d attempts, envelope %s %s %s %d", tc.name, d.State, len(d.Attempts), env.SchemaVersion, env.Tool, env.Status, env.ExitCode)
		}
		if !strings.Contains(raw, `"artifacts": []`) || !strings.Contains(raw, `"side_effects": []`) || env.Usage.CostSource != toolio.CostNone || !strings.HasSuffix(raw, "}\n") {
			t.Errorf("%s: the envelope is not written as a command writes it:\n%s", tc.name, raw)
		}
		if env.Error == nil || env.Error.Category != tc.category || env.Error.Retryable != tc.retryable || env.Error.Message == "" || tc.message != "" && env.Error.Message != tc.message {
			t.Errorf("%s: error %+v, want %s (retryable %v) %q", tc.name, env.Error, tc.category, tc.retryable, tc.message)
		}
		if *d.Result.Error != *env.Error && tc.category != "forge" {
			t.Errorf("%s: the result's error %+v is not the envelope's", tc.name, d.Result.Error)
		}
	}
	if hint := docs[0].Result.Error.FixHint; hint == nil || hint.Suggest != "try later" {
		t.Errorf("the fix hint of the failure is %+v", hint)
	}
	if panics := e.Log.Find("ERROR", "panicked"); len(panics) != 1 || panics[0]["job"] != docs[2].ID || panics[0]["panic"] != "bang" || panics[0]["stack"] == "" {
		t.Errorf("ERROR records = %v, want one for the panic, naming the job, with its stack", e.Log.Find("ERROR", ""))
	}
	if n := len(e.Log.Find("ERROR", "")); n != 1 {
		t.Errorf("%d ERROR records, want the panic's only", n)
	}
	// A triage's envelope names its own tool.
	e.Runner.Then(fakerunner.Fail(errors.New("no")))
	triage, err := e.Svc.Create(ctx, jobs.CreateRequest{Workspace: "widgets", Kind: jobs.KindTriage, Body: jobs.Body{Text: ptr("x")}, Source: jobs.Source{Kind: "api", By: "u"}})
	if err != nil {
		t.Fatal(err)
	}
	e.waitRest(triage.ID)
	if raw := testutil.ReadFile(t, e.file(triage.ID, 1, "envelope.json")); !strings.Contains(raw, `"tool": "triage"`) {
		t.Errorf("the triage's envelope:\n%s", raw)
	}
}
