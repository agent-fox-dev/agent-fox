package jobs_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// atRest reports whether a job is blocked, done, failed or cancelled and none of its
// attempts is running or queued.
func atRest(doc jobs.Doc) bool {
	switch doc.State {
	case jobs.StateBlocked, jobs.StateDone, jobs.StateFailed, jobs.StateCancelled:
	default:
		return false
	}
	for _, a := range doc.Attempts {
		if a.State == jobs.AttemptRunning || a.State == jobs.AttemptQueued {
			return false
		}
	}
	return true
}

// waitRest waits until the job is at rest, and returns it.
func (e *env) waitRest(id string) jobs.Doc {
	e.t.Helper()
	var doc jobs.Doc
	waitFor(e.t, id+" to be at rest", func() bool {
		doc = e.get(id)
		return atRest(doc)
	})
	return doc
}

// runOne makes a fix job on widgets for the issue and runs it with the steps, and
// returns it once it is at rest.
func runOne(t *testing.T, steps ...fakerunner.Step) (*env, jobs.Doc) {
	t.Helper()
	e := newEnv(t, withRunner(fakerunner.New(steps...)))
	job := e.create("widgets", jobs.KindFix, 1)
	e.Start()
	return e, e.waitRest(job.ID)
}

// failedBrain is a failed envelope of the category that the layer tries again.
func failedBrain(retryable bool, max int) fakerunner.Step {
	return fakerunner.Return(jobs.Result{Envelope: fakerunner.Env(toolio.StatusFailed, fakerunner.WithError("brain", "the brain gave up", retryable)), MaxAttempts: max})
}

// fileOf is a file of an attempt.
func (e *env) fileOf(id string, n int, name string) string {
	return filepath.Join(e.Dir, "attempts", id, fmt.Sprint(n), name)
}

// events are the stored events of the job.
func (e *env) events(id string) []jobs.Event {
	e.t.Helper()
	evs, err := jobs.NewStore(e.DB, nil).EventsAfter(ctx, id, 0, 100000)
	if err != nil {
		e.t.Fatal(err)
	}
	return evs
}

// TestAFinishedAttemptStoresItsEnvelopeAndReportAndUpdatesTheJob_TS_12_36 verifies
// TS-12-36: the envelope of a finished attempt is stored as it was given in
// envelope.json, and a report that is not empty in report.md, both with the mode
// 0600; attempt_end says how the attempt ended; and the job's result, artifacts, side
// effects, labels, usage and finished_at are those of the attempt.
// Verifies: 12-REQ-6.1
func TestAFinishedAttemptStoresItsEnvelopeAndReportAndUpdatesTheJob_TS_12_36(t *testing.T) {
	artifacts := []toolio.Artifact{{Kind: "pull_request", URL: "https://github.example.com/acme/widgets/pull/9", Number: 9}, {Kind: "branch", Name: "af/fix-1"}}
	effects := []toolio.SideEffect{{Kind: "label", Target: "acme/widgets#1", State: toolio.StateDone}}
	usage := toolio.Usage{Brain: "scripted", CostUSD: 1.25, CostSource: toolio.CostMeasured, InputTokens: 1000, OutputTokens: 200, Turns: 7}
	// An envelope with a field that toolio.Envelope does not know, in front of the others
	// and in an order of its own: decoded and written again it would change.
	withReport := bytes.Replace(
		fakerunner.Env(toolio.StatusDone, fakerunner.WithSummary("fixed it"), fakerunner.WithArtifacts(artifacts...), fakerunner.WithSideEffects(effects...), fakerunner.WithUsage(usage)),
		[]byte(`"status": "done",`), []byte(`"x_extension": {"z": 1, "a": [1, 2]}, "status": "done",`), 1)
	withoutReport := fakerunner.Env(toolio.StatusDone, fakerunner.WithSummary("nothing to say"))

	r := fakerunner.New(
		fakerunner.Return(jobs.Result{Envelope: withReport, Report: "# Report\n\nAll good.\n", LabelsSet: []string{"af:fixed"}, LabelsRemoved: []string{"af:fix"}}),
		fakerunner.Return(jobs.Result{Envelope: withoutReport}),
	)
	e := newEnv(t, withRunner(r))
	first := e.create("widgets", jobs.KindFix, 1)
	second := e.create("widgets", jobs.KindFix, 2)
	e.Start()
	doc, other := e.waitRest(first.ID), e.waitRest(second.ID)

	for _, tc := range []struct {
		id   string
		want []byte
	}{{first.ID, withReport}, {second.ID, withoutReport}} {
		path := e.fileOf(tc.id, 1, "envelope.json")
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(tc.want) {
			t.Errorf("%s: envelope.json = %.60q (%v), want the bytes the runner gave", tc.id, got, err)
		}
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: envelope.json: %v, %v, want the mode 0600", tc.id, info, err)
		}
	}
	report := e.fileOf(first.ID, 1, "report.md")
	if got, err := os.ReadFile(report); err != nil || string(got) != "# Report\n\nAll good.\n" {
		t.Errorf("report.md = %q (%v)", got, err)
	}
	if info, err := os.Stat(report); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("report.md: %v, %v, want the mode 0600", info, err)
	}
	if _, err := os.Stat(e.fileOf(second.ID, 1, "report.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a report.md was written for an empty report: %v", err)
	}

	// attempt_end, then the job at rest.
	evs := e.events(first.ID)
	if len(evs) < 2 || evs[len(evs)-2].Type != "attempt_end" || evs[len(evs)-1].Type != "job_state" {
		t.Fatalf("the last events are %v, want attempt_end and job_state", evs)
	}
	var end struct {
		Attempt  int    `json:"attempt"`
		State    string `json:"state"`
		Status   string `json:"status"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.Unmarshal(evs[len(evs)-2].Data, &end); err != nil || end != (struct {
		Attempt  int    `json:"attempt"`
		State    string `json:"state"`
		Status   string `json:"status"`
		ExitCode int    `json:"exit_code"`
	}{1, "done", "done", 0}) {
		t.Errorf("attempt_end = %s, want {attempt 1, state done, status done, exit_code 0}", evs[len(evs)-2].Data)
	}
	var state struct {
		State  string `json:"state"`
		AtRest bool   `json:"at_rest"`
		Result struct {
			Status   string `json:"status"`
			ExitCode int    `json:"exit_code"`
		} `json:"result"`
	}
	if err := json.Unmarshal(evs[len(evs)-1].Data, &state); err != nil || state.State != "done" || !state.AtRest || state.Result.Status != "done" {
		t.Errorf("the last event = %s, want job_state done at rest with its result", evs[len(evs)-1].Data)
	}

	// The job follows the attempt.
	if doc.State != jobs.StateDone || doc.FinishedAt == nil || doc.StartedAt == nil {
		t.Errorf("job = %s, started %v, finished %v, want done and both set", doc.State, doc.StartedAt, doc.FinishedAt)
	}
	if doc.Result == nil || doc.Result.Status != toolio.StatusDone || doc.Result.ExitCode != 0 || doc.Result.Summary != "fixed it" || doc.Result.Error != nil {
		t.Errorf("result = %+v, want done, 0, 'fixed it' and no error", doc.Result)
	}
	if len(doc.Artifacts) != 2 || doc.Artifacts[0] != artifacts[0] || doc.Artifacts[1] != artifacts[1] || len(doc.SideEffects) != 1 || doc.SideEffects[0] != effects[0] {
		t.Errorf("artifacts %+v and side_effects %+v, want those of the envelope", doc.Artifacts, doc.SideEffects)
	}
	if doc.Usage != (jobs.UsageDoc{CostUSD: 1.25, InputTokens: 1000, OutputTokens: 200, Turns: 7}) {
		t.Errorf("usage = %+v, want the usage of the attempt", doc.Usage)
	}
	if doc.Labels == nil || !slices.Equal(doc.Labels.Set, []string{"af:fixed"}) || !slices.Equal(doc.Labels.Removed, []string{"af:fix"}) || doc.Labels.Restore != nil {
		t.Errorf("labels = %+v, want set af:fixed, removed af:fix and no restore", doc.Labels)
	}
	if other.Labels != nil || other.Usage != (jobs.UsageDoc{}) {
		t.Errorf("a job with no labels and no cost: labels %+v, usage %+v, want null and zero", other.Labels, other.Usage)
	}
	if a := doc.Attempts[0]; a.State != jobs.AttemptDone || a.Status == nil || *a.Status != toolio.StatusDone || a.ExitCode == nil || *a.ExitCode != 0 || a.Usage == nil || a.Usage.Brain != "scripted" || a.FinishedAt == nil {
		t.Errorf("attempt = %+v, want done with its status, exit code, usage and times", a)
	}
}

// TestDoneAndUnverifiedWithoutAParkedOutcomeEndTheJobDone_TS_12_38 verifies TS-12-38:
// done, and unverified with an outcome other than parked or none, record the attempt
// and the job done, with exit code 0 and 4.
// Verifies: 12-REQ-6.3
func TestDoneAndUnverifiedWithoutAParkedOutcomeEndTheJobDone_TS_12_38(t *testing.T) {
	for name, tc := range map[string]struct {
		envelope []byte
		code     int
	}{
		"done":                  {fakerunner.Env(toolio.StatusDone), 0},
		"unverified, landed":    {fakerunner.Env(toolio.StatusUnverified, fakerunner.WithOutcome("landed")), 4},
		"unverified, no result": {fakerunner.Env(toolio.StatusUnverified), 4},
	} {
		t.Run(name, func(t *testing.T) {
			_, doc := runOne(t, fakerunner.Envelope(tc.envelope))
			if doc.State != jobs.StateDone || doc.Attempts[0].State != jobs.AttemptDone || doc.Result == nil || doc.Result.ExitCode != tc.code {
				t.Errorf("job %s, attempt %s, result %+v, want done, done and exit code %d", doc.State, doc.Attempts[0].State, doc.Result, tc.code)
			}
			if len(doc.Attempts) != 1 {
				t.Errorf("%d attempts, want 1", len(doc.Attempts))
			}
		})
	}
}

// TestANeedsHumanEnvelopeBlocksTheJobWithLetteredOptions_TS_12_39 verifies TS-12-39:
// a needs_human envelope with a question records the attempt needs_human and the job
// blocked, with the question and options lettered A, B, C with the envelope's labels,
// no finished_at, and an at-rest job_state.
// Verifies: 12-REQ-6.4
func TestANeedsHumanEnvelopeBlocksTheJobWithLetteredOptions_TS_12_39(t *testing.T) {
	e, doc := runOne(t, fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which branch?", "main", "release", "a new one"))))
	if doc.State != jobs.StateBlocked || doc.Attempts[0].State != jobs.AttemptNeedsHuman {
		t.Fatalf("job %s, attempt %s, want blocked and needs_human", doc.State, doc.Attempts[0].State)
	}
	want := &jobs.Blocked{
		Reason: "needs_human", Question: "Which branch?", AnswerWith: "POST /api/v1/jobs/" + doc.ID + "/answer",
		Options: []jobs.Option{{ID: "A", Label: "main"}, {ID: "B", Label: "release"}, {ID: "C", Label: "a new one"}},
	}
	if got, _ := json.Marshal(doc.Blocked); string(got) != mustJSON(want) {
		t.Errorf("blocked = %s, want %s", got, mustJSON(want))
	}
	if doc.FinishedAt != nil {
		t.Errorf("finished_at = %v, want null for a job that is blocked", *doc.FinishedAt)
	}
	if doc.Result == nil || doc.Result.Status != toolio.StatusNeedsHuman || doc.Result.ExitCode != 3 {
		t.Errorf("result = %+v, want needs_human with exit code 3", doc.Result)
	}
	evs := e.events(doc.ID)
	var state struct {
		State   string        `json:"state"`
		AtRest  bool          `json:"at_rest"`
		Blocked *jobs.Blocked `json:"blocked"`
	}
	if last := evs[len(evs)-1]; last.Type != "job_state" || json.Unmarshal(last.Data, &state) != nil || state.State != "blocked" || !state.AtRest || state.Blocked == nil || state.Blocked.Question != "Which branch?" {
		t.Errorf("the last event is %s %s, want job_state blocked at rest with the question", last.Type, last.Data)
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestTheOptionsOfAQuestionAreLetteredInOrderWhateverIdsTheEnvelopeGaveThem verifies
// the lettering of 12-REQ-2.2: the ids are A, B, … by position.
// Verifies: 12-REQ-2.2
func TestTheOptionsOfAQuestionAreLetteredInOrderWhateverIdsTheEnvelopeGaveThem(t *testing.T) {
	_, doc := runOne(t, fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, func(e *toolio.Envelope) {
		e.NeedsHuman = &toolio.NeedsHuman{Question: "Which?", Options: []toolio.Option{{ID: "3", Label: "three"}, {ID: "x", Label: "ex"}, {ID: "A", Label: "again"}}}
	})))
	want := []jobs.Option{{ID: "A", Label: "three"}, {ID: "B", Label: "ex"}, {ID: "C", Label: "again"}}
	if doc.Blocked == nil || !slices.Equal(doc.Blocked.Options, want) {
		t.Errorf("blocked = %+v, want the options %v", doc.Blocked, want)
	}
	// A question without options is answered in free text.
	_, free := runOne(t, fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Why?"))))
	if free.Blocked == nil || free.Blocked.Options == nil || len(free.Blocked.Options) != 0 {
		t.Errorf("blocked = %+v, want a question with options [] and not null", free.Blocked)
	}
}

// TestAFailedEnvelopeOfTheBrainThatCanBeRetriedQueuesAnAutomaticAttempt_TS_12_40
// verifies TS-12-40: a failed envelope of category brain or no_result that is
// retryable makes attempt n+1 with the trigger auto, and the job is queued with no
// finished_at, until the round has the number of attempts that the runner allows:
// 0 means 2, 3 means 3, and 99 is cut to 10.
// Verifies: 12-REQ-6.5
func TestAFailedEnvelopeOfTheBrainThatCanBeRetriedQueuesAnAutomaticAttempt_TS_12_40(t *testing.T) {
	for _, tc := range []struct{ max, attempts int }{{0, 2}, {1, 1}, {2, 2}, {3, 3}, {10, 10}, {99, 10}, {-5, 2}} {
		t.Run(fmt.Sprintf("a maximum of %d", tc.max), func(t *testing.T) {
			// The failures alternate between the two categories that are tried again.
			var steps []fakerunner.Step
			for i := range 12 {
				category := []string{"brain", "no_result"}[i%2]
				steps = append(steps, fakerunner.Return(jobs.Result{
					Envelope:    fakerunner.Env(toolio.StatusFailed, fakerunner.WithError(category, "gave up", true)),
					MaxAttempts: tc.max,
				}))
			}
			e, doc := runOne(t, steps...)
			if doc.State != jobs.StateFailed || len(doc.Attempts) != tc.attempts || doc.FinishedAt == nil {
				t.Fatalf("job %s with %d attempts (finished %v), want failed with %d", doc.State, len(doc.Attempts), doc.FinishedAt, tc.attempts)
			}
			for i, a := range doc.Attempts {
				wantTrigger := jobs.TriggerAuto
				if i == 0 {
					wantTrigger = jobs.TriggerCreate
				}
				if a.Trigger.Kind != wantTrigger || a.State != jobs.AttemptFailed {
					t.Errorf("attempt %d: trigger %s, state %s, want %s and failed", a.N, a.Trigger.Kind, a.State, wantTrigger)
				}
			}
			// While an automatic attempt was queued the job was queued and not finished.
			sawQueued := 0
			for _, ev := range e.events(doc.ID) {
				var s struct {
					State   string `json:"state"`
					Attempt int    `json:"attempt"`
					AtRest  bool   `json:"at_rest"`
				}
				if ev.Type == "job_state" && json.Unmarshal(ev.Data, &s) == nil && s.State == "queued" && s.Attempt > 1 {
					sawQueued++
					if s.AtRest {
						t.Errorf("a job_state of an automatic attempt says at rest: %s", ev.Data)
					}
				}
			}
			if sawQueued != tc.attempts-1 {
				t.Errorf("%d job_state events for automatic attempts, want %d", sawQueued, tc.attempts-1)
			}
		})
	}
}

// TestRetryableFailuresThatAreNotOfTheBrainAndEverythingElseFailTheJob_TS_12_41
// verifies TS-12-41: a parked fix, a retryable failure of the sandbox, a retryable
// failure of the forge, a failure of the brain that is not retryable, and a usage
// failure each fail the attempt and the job, and no attempt n+1 is made; a parked fix
// has exit code 4.
// Verifies: 12-REQ-6.6
func TestRetryableFailuresThatAreNotOfTheBrainAndEverythingElseFailTheJob_TS_12_41(t *testing.T) {
	failed := func(category string, retryable bool) []byte {
		return fakerunner.Env(toolio.StatusFailed, fakerunner.WithError(category, "failed: "+category, retryable))
	}
	for name, tc := range map[string]struct {
		envelope []byte
		code     int
	}{
		"a parked fix":              {fakerunner.Env(toolio.StatusUnverified, fakerunner.WithOutcome("parked")), 4},
		"a retryable sandbox":       {failed("sandbox", true), 1},
		"a retryable forge":         {failed("forge", true), 1},
		"a brain that cannot retry": {failed("brain", false), 1},
		"a no_result that cannot":   {failed("no_result", false), 1},
		"a retryable timeout":       {failed("timeout", true), 1},
		"a usage failure":           {fakerunner.Env(toolio.StatusUsage, fakerunner.WithError("usage", "bad flag", false)), 2},
	} {
		t.Run(name, func(t *testing.T) {
			_, doc := runOne(t, fakerunner.Envelope(tc.envelope))
			if doc.State != jobs.StateFailed || doc.Attempts[0].State != jobs.AttemptFailed || len(doc.Attempts) != 1 {
				t.Errorf("job %s, attempt %s, %d attempts, want failed, failed and 1", doc.State, doc.Attempts[0].State, len(doc.Attempts))
			}
			if doc.Result == nil || doc.Result.ExitCode != tc.code {
				t.Errorf("result = %+v, want exit code %d", doc.Result, tc.code)
			}
			if doc.FinishedAt == nil {
				t.Error("a job that failed has no finished_at")
			}
		})
	}
}

// TestAnAttemptWithoutAUsableEnvelopeGetsASynthesizedOne_TS_12_42 verifies TS-12-42:
// an attempt that ends with a failure wrapped in another error, a plain error, a
// panic, an envelope with a status that is not one, a needs_human without a
// question, or no envelope at all, has an envelope.json of the schema version, the
// tool of the job's kind, status failed, empty arrays and no cost; the failure keeps
// its category, message and retryable flag, and the rest are internal and not
// retryable; the panic is logged as an error; and the exit code is 1.
// Verifies: 12-REQ-6.7
func TestAnAttemptWithoutAUsableEnvelopeGetsASynthesizedOne_TS_12_42(t *testing.T) {
	forge := &toolio.Failure{Category: toolio.CategoryForge, Message: "the forge hung up", Retryable: true, FixHint: &toolio.FixHint{Suggest: "try later"}}
	for name, tc := range map[string]struct {
		step      fakerunner.Step
		category  string
		retryable bool
		message   string
	}{
		"a failure wrapped in an error":     {fakerunner.Fail(fmt.Errorf("landing: %w", forge)), "forge", true, "the forge hung up"},
		"a plain error":                     {fakerunner.Fail(errors.New("disk on fire")), "internal", false, "disk on fire"},
		"a panic":                           {fakerunner.Panic("bang"), "internal", false, ""},
		"a status that is not one":          {fakerunner.Envelope(fakerunner.Env("running")), "internal", false, ""},
		"needs_human without a question":    {fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman)), "internal", false, ""},
		"an envelope that is not JSON":      {fakerunner.Envelope([]byte("not json at all")), "internal", false, ""},
		"needs_human with a blank question": {fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("  \t", "a"))), "internal", false, ""},
		"no envelope":                       {fakerunner.Return(jobs.Result{}), "internal", false, ""},
	} {
		t.Run(name, func(t *testing.T) {
			e, doc := runOne(t, tc.step)
			// A failure that the layer tries again for would run again; the others fail the job.
			if doc.State != jobs.StateFailed || len(doc.Attempts) != 1 || doc.Attempts[0].State != jobs.AttemptFailed {
				t.Fatalf("job %s with %d attempts, attempt %s, want failed, 1 and failed", doc.State, len(doc.Attempts), doc.Attempts[0].State)
			}
			raw, err := os.ReadFile(e.fileOf(doc.ID, 1, "envelope.json"))
			if err != nil {
				t.Fatalf("there is no envelope.json: %v", err)
			}
			var env toolio.Envelope
			if err := json.Unmarshal(raw, &env); err != nil {
				t.Fatalf("envelope.json is not an envelope: %v\n%s", err, raw)
			}
			if env.SchemaVersion != toolio.SchemaVersion || env.Tool != "fix" || env.Status != toolio.StatusFailed || env.ExitCode != 1 {
				t.Errorf("envelope = schema %s, tool %s, status %s, exit %d, want %s, fix, failed and 1", env.SchemaVersion, env.Tool, env.Status, env.ExitCode, toolio.SchemaVersion)
			}
			if len(env.Artifacts) != 0 || len(env.SideEffects) != 0 || len(env.Warnings) != 0 || len(env.Next) != 0 || env.Usage.CostSource != toolio.CostNone {
				t.Errorf("envelope arrays %v %v %v %v and cost source %q, want empty arrays and none", env.Artifacts, env.SideEffects, env.Warnings, env.Next, env.Usage.CostSource)
			}
			if !strings.Contains(string(raw), `"artifacts": []`) || !strings.Contains(string(raw), `"side_effects": []`) {
				t.Errorf("the arrays are not written as [] in\n%s", raw)
			}
			if env.Error == nil || env.Error.Category != tc.category || env.Error.Retryable != tc.retryable || env.Error.Message == "" {
				t.Fatalf("error = %+v, want category %s, retryable %v and a message", env.Error, tc.category, tc.retryable)
			}
			if tc.message != "" && env.Error.Message != tc.message {
				t.Errorf("message = %q, want %q", env.Error.Message, tc.message)
			}
			if tc.category == "forge" && (env.Error.FixHint == nil || env.Error.FixHint.Suggest != "try later") {
				t.Errorf("fix_hint = %+v, want the failure's", env.Error.FixHint)
			}
			if doc.Result == nil || doc.Result.ExitCode != 1 || doc.Result.Error == nil || doc.Result.Error.Category != tc.category {
				t.Errorf("result = %+v, want exit code 1 and category %s", doc.Result, tc.category)
			}
			if name == "a panic" {
				found := false
				for _, rec := range e.Logs.find("ERROR", "panic") {
					found = found || (rec["job"] == doc.ID && strings.Contains(fmt.Sprint(rec["panic"]), "bang"))
				}
				if !found {
					t.Errorf("no ERROR record names the job and the panic: %v", e.Logs.records())
				}
			}
			if name != "a panic" && len(e.Logs.find("ERROR", "")) != 0 {
				t.Errorf("an ERROR was logged: %v", e.Logs.find("ERROR", ""))
			}
		})
	}
}

// TestTheSynthesizedEnvelopeNamesTheToolOfTheJobsKind verifies that the tool of an
// envelope the layer makes is the kind of the job, a triage as well as a fix.
// Verifies: 12-REQ-6.7
func TestTheSynthesizedEnvelopeNamesTheToolOfTheJobsKind(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Fail(errors.New("first")), fakerunner.Fail(errors.New("second")))))
	fix := e.create("widgets", jobs.KindFix, 1)
	triage := e.create("widgets", jobs.KindTriage, 2)
	e.Start()
	for id, tool := range map[string]string{fix.ID: "fix", triage.ID: "triage"} {
		e.waitRest(id)
		raw, err := os.ReadFile(e.fileOf(id, 1, "envelope.json"))
		if err != nil || !strings.Contains(string(raw), `"tool": "`+tool+`"`) {
			t.Errorf("job %s: %v\n%s, want the tool %s", id, err, raw, tool)
		}
	}
}

// TestEveryJobDocumentHasExactlyTheDocumentedFieldsAndBlockedOnlyWhileBlocked_TS_12_9
// verifies TS-12-9 on the documents: a queued, a blocked and a done job each have
// the 20 fields, and one more, attempts, in the view of a single job; the input has
// no context; request_label is null for a job of the API; blocked is an object
// only while the job is blocked. (The routes serve the same documents: see
// TestTheRoutesServeTheDocumentsOfJobsAsTheyAre_TS_12_9_TS_12_10_TS_12_14.)
// Verifies: 12-REQ-2.1, 12-REQ-2.2
func TestEveryJobDocumentHasExactlyTheDocumentedFieldsAndBlockedOnlyWhileBlocked_TS_12_9(t *testing.T) {
	// The third job is held in the runner, so the fourth waits behind it, queued.
	r := fakerunner.New(
		fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which?", "X", "Y", "Z"))),
		fakerunner.Done(),
		fakerunner.Block(fakerunner.Step{}),
	)
	e := newEnv(t, withRunner(r))
	post := func(issue int) jobs.Doc {
		resp := e.call("POST", "/api/v1/workspaces/widgets/fix", map[string]any{"issue": issue, "context": []string{"a private note"}})
		return resp.Doc(t)
	}
	blocked, done, _, queued := post(1), post(2), post(3), post(4)
	e.Start()
	e.waitState(blocked.ID, jobs.StateBlocked)
	e.waitState(done.ID, jobs.StateDone)
	waitCalls(t, r, 3)
	if d := e.get(queued.ID); d.State != jobs.StateQueued {
		t.Fatalf("the fourth job is %s, want it queued behind the third", d.State)
	}

	fields := []string{"artifacts", "attempt_count", "blocked", "cancel_cause", "created_at", "current_attempt", "finished_at", "hub", "id", "input", "issue",
		"kind", "labels", "request_label", "result", "side_effects", "source", "started_at", "state", "usage", "workspace"}
	keys := func(doc jobs.Doc) []string {
		var m map[string]json.RawMessage
		raw, _ := json.Marshal(doc)
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		var out []string
		for k := range m {
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}
	for name, id := range map[string]string{"queued": queued.ID, "blocked": blocked.ID, "done": done.ID} {
		single := e.get(id)
		if got := keys(single); !slices.Equal(got, sortedWith(fields, "attempts")) {
			t.Errorf("%s: the fields of a single job are %v, want %v and attempts", name, got, fields)
		}
		list := single
		list.Attempts = nil
		if got := keys(list); !slices.Equal(got, fields) {
			t.Errorf("%s: the fields of a list entry are %v, want %v", name, got, fields)
		}
		if single.RequestLabel != nil || strings.Contains(mustJSON(single.Input), "private") || single.Source != (jobs.Source{Kind: "api", By: callerID}) {
			t.Errorf("%s: request_label %v, input %s, source %+v, want null, no context, and the api", name, single.RequestLabel, mustJSON(single.Input), single.Source)
		}
		if wantBlocked := name == "blocked"; (single.Blocked != nil) != wantBlocked {
			t.Errorf("%s: blocked = %v", name, single.Blocked)
		}
	}
	b := e.get(blocked.ID).Blocked
	if b == nil || b.Reason != "needs_human" || b.Question != "Which?" || b.AnswerWith != "POST /api/v1/jobs/"+blocked.ID+"/answer" ||
		len(b.Options) != 3 || b.Options[0] != (jobs.Option{ID: "A", Label: "X"}) || b.Options[1].ID != "B" || b.Options[2] != (jobs.Option{ID: "C", Label: "Z"}) {
		t.Errorf("blocked = %+v, want the question with options A, B, C and answer_with", b)
	}
	// Every field of a list entry is there when nothing is known too: the arrays are [].
	if q := e.get(queued.ID); mustJSON(q.Artifacts) != "[]" || mustJSON(q.SideEffects) != "[]" || q.Result != nil || q.Labels != nil || q.Hub != nil {
		t.Errorf("queued job: artifacts %s, side_effects %s, result %v, labels %v, hub %v, want [] [] and nulls", mustJSON(q.Artifacts), mustJSON(q.SideEffects), q.Result, q.Labels, q.Hub)
	}
}

func sortedWith(fields []string, extra string) []string {
	out := append(slices.Clone(fields), extra)
	slices.Sort(out)
	return out
}

// TestResultArtifactsAndSideEffectsAreThoseOfTheLatestFinishedAttempt_TS_12_10
// verifies TS-12-10: a job whose attempt 1 failed and whose attempt 2 was done has the
// result, artifacts and side effects of attempt 2, with the exit code of its status;
// a job nothing has finished has result null and empty arrays; and the exit code of
// the result is that of its status for every job that is finished.
// Verifies: 12-REQ-2.3
func TestResultArtifactsAndSideEffectsAreThoseOfTheLatestFinishedAttempt_TS_12_10(t *testing.T) {
	artifacts := []toolio.Artifact{{Kind: "pull_request", Number: 3}, {Kind: "branch", Name: "af/fix"}}
	effects := []toolio.SideEffect{{Kind: "comment", Target: "acme/widgets#1", State: toolio.StateDone}}
	_, doc := runOne(t,
		fakerunner.Return(jobs.Result{Envelope: fakerunner.Env(toolio.StatusFailed, fakerunner.WithError("brain", "gave up", true), fakerunner.WithArtifacts(toolio.Artifact{Kind: "stale"}), fakerunner.WithSummary("first")), MaxAttempts: 2}),
		fakerunner.Envelope(fakerunner.Env(toolio.StatusDone, fakerunner.WithSummary("second"), fakerunner.WithArtifacts(artifacts...), fakerunner.WithSideEffects(effects...))),
	)
	if len(doc.Attempts) != 2 || doc.Attempts[0].State != jobs.AttemptFailed || doc.Attempts[1].State != jobs.AttemptDone {
		t.Fatalf("attempts = %+v, want a failed one and a done one", doc.Attempts)
	}
	if doc.Result == nil || doc.Result.Status != toolio.StatusDone || doc.Result.ExitCode != 0 || doc.Result.Summary != "second" || doc.Result.Error != nil {
		t.Errorf("result = %+v, want done, 0, 'second' and no error: those of attempt 2", doc.Result)
	}
	if len(doc.Artifacts) != 2 || doc.Artifacts[0] != artifacts[0] || doc.Artifacts[1] != artifacts[1] || len(doc.SideEffects) != 1 || doc.SideEffects[0] != effects[0] {
		t.Errorf("artifacts %+v and side_effects %+v, want those of attempt 2", doc.Artifacts, doc.SideEffects)
	}
	for _, a := range doc.Attempts {
		if a.Status == nil || a.ExitCode == nil || *a.ExitCode != toolio.ExitCode(*a.Status) {
			t.Errorf("attempt %d: status %v, exit_code %v, want the exit code of the status", a.N, a.Status, a.ExitCode)
		}
	}

	// The exit code of the result is that of its status, whatever the status.
	for status, envelope := range map[toolio.Status][]byte{
		toolio.StatusFailed:     fakerunner.Env(toolio.StatusFailed, fakerunner.WithError("sandbox", "no box", false)),
		toolio.StatusUnverified: fakerunner.Env(toolio.StatusUnverified, fakerunner.WithOutcome("parked")),
		toolio.StatusUsage:      fakerunner.Env(toolio.StatusUsage, fakerunner.WithError("usage", "bad flag", false)),
		toolio.StatusNeedsHuman: fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which?", "a")),
	} {
		_, d := runOne(t, fakerunner.Envelope(envelope))
		if d.Result == nil || d.Result.Status != status || d.Result.ExitCode != toolio.ExitCode(status) {
			t.Errorf("%s: result = %+v, want the exit code %d", status, d.Result, toolio.ExitCode(status))
		}
	}

	// A job that nothing has finished.
	idle := newEnv(t)
	queued := idle.get(idle.create("widgets", jobs.KindFix, 1).ID)
	if queued.Result != nil || mustJSON(queued.Artifacts) != "[]" || mustJSON(queued.SideEffects) != "[]" {
		t.Errorf("an unfinished job: result %+v, artifacts %s, side_effects %s, want null, [] and []", queued.Result, mustJSON(queued.Artifacts), mustJSON(queued.SideEffects))
	}
}

// TestARunnerResultCarriesAMaximumThatZeroMakesTwo_TS_12_32 verifies TS-12-32 for the
// result: the maximum of attempts that the runner returns with a failed envelope of
// the brain is 0 for two, 1 for one, and 3 for three, and an error that is not
// transient is handled as a failed attempt.
// Verifies: 12-REQ-5.4
func TestARunnerResultCarriesAMaximumThatZeroMakesTwo_TS_12_32(t *testing.T) {
	for _, tc := range []struct{ max, attempts int }{{0, 2}, {1, 1}, {3, 3}} {
		var steps []fakerunner.Step
		for range 5 {
			steps = append(steps, failedBrain(true, tc.max))
		}
		_, doc := runOne(t, steps...)
		if len(doc.Attempts) != tc.attempts || doc.State != jobs.StateFailed {
			t.Errorf("a maximum of %d: %d attempts and %s, want %d and failed", tc.max, len(doc.Attempts), doc.State, tc.attempts)
		}
	}
}
