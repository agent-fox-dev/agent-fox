package main

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/schema"

	"github.com/agent-fox-dev/agentfox/internal/triage"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// questionPayload is a suspected diagnosis that asks which of two readings is meant.
func questionPayload() string {
	return diagnosisPayload(func(m map[string]any) {
		m["confidence"] = "suspected"
		m["question"] = map[string]any{"text": "Which session?", "options": []any{"old", "new"}}
	})
}

// notReproducible is a diagnosis that found nothing to point at.
func notReproducible() string {
	return diagnosisPayload(func(m map[string]any) {
		m["confidence"] = "not_reproducible"
		m["affected_files"] = []any{}
		m["acceptance_criteria"] = []any{}
	})
}

func withConfidence(confidence string) string {
	return diagnosisPayload(func(m map[string]any) { m["confidence"] = confidence })
}

func scriptedLoop(e *triageEnv, input, payload string, args ...string) (int, map[string]any) {
	e.t.Helper()
	return e.loop(append([]string{input, "--dir", e.dir, "--brain", "scripted", "--script", script(e.t, submitStep(payload, "accepted"))}, args...)...)
}

// sideEffects are the side effects of an envelope as kind -> states.
func sideEffects(env map[string]any) map[string][]string {
	out := map[string][]string{}
	list, _ := env["side_effects"].([]any)
	for _, s := range list {
		m := s.(map[string]any)
		out[m["kind"].(string)] = append(out[m["kind"].(string)], m["state"].(string))
	}
	return out
}

func nextArgvs(env map[string]any) [][]string {
	var out [][]string
	list, _ := env["next"].([]any)
	for _, n := range list {
		var argv []string
		for _, a := range n.(map[string]any)["argv"].([]any) {
			argv = append(argv, a.(string))
		}
		out = append(out, argv)
	}
	return out
}

func hasNext(env map[string]any, want ...string) bool {
	return slices.ContainsFunc(nextArgvs(env), func(argv []string) bool { return slices.Equal(argv, want) })
}

// TestADryRunRunsTheBrainAndPlansTheWrites_TS_08_56 verifies TS-08-56:
// a dry run asks the brain, plans the writes of the landing and sends none.
// Verifies: 08-REQ-8.8
func TestADryRunRunsTheBrainAndPlansTheWrites_TS_08_56(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	spy := &spyBrain{run: submitting(diagnosisPayload(nil))}
	useSpy(t, spy)

	code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "spy", "--dry-run", "--label", "triaged")

	if code != 0 || env["status"] != "done" || resultOf(t, env)["action"] != "planned" {
		t.Fatalf("exit code %d, status %v, error %v, result %v", code, env["status"], env["error"], env["result"])
	}
	if spy.runs != 1 {
		t.Errorf("the brain ran %d times, want 1", spy.runs)
	}
	effects := sideEffects(env)
	for _, kind := range []string{"update_issue", "add_labels"} {
		if !slices.Equal(effects[kind], []string{"planned"}) {
			t.Errorf("side effects = %v, want %s planned", effects, kind)
		}
	}
	if n := len(e.forge.Writes()); n != 0 {
		t.Errorf("%d writes to the forge", n)
	}
	if env["error"] != nil {
		t.Errorf("a dry run was reported as a failure: %v", env["error"])
	}
}

// TestAQuestionStopsTheRunAndWritesNothing_TS_08_57 verifies TS-08-57:
// a submission with a question ends needs_human, in the loop and in land, with
// the options in order, and nothing is written to the forge.
// Verifies: 08-REQ-9.1
func TestAQuestionStopsTheRunAndWritesNothing_TS_08_57(t *testing.T) {
	check := func(t *testing.T, e *triageEnv, code int, env map[string]any) {
		t.Helper()
		if code != 3 || env["status"] != "needs_human" {
			t.Fatalf("exit code %d, status %v, error %v, want 3 and needs_human", code, env["status"], env["error"])
		}
		nh, _ := env["needs_human"].(map[string]any)
		if nh["question"] != "Which session?" {
			t.Errorf("needs_human = %v", nh)
		}
		var labels, ids []string
		for _, o := range nh["options"].([]any) {
			labels = append(labels, o.(map[string]any)["label"].(string))
			ids = append(ids, o.(map[string]any)["id"].(string))
		}
		if !slices.Equal(labels, []string{"old", "new"}) || !slices.Equal(ids, []string{"A", "B"}) {
			t.Errorf("options = %v %v, want old and new as A and B", ids, labels)
		}
		resume, _ := nh["resume"].(map[string]any)
		if argv, _ := resume["argv"].([]any); len(argv) < 3 || argv[0] != "af" || argv[1] != "triage" || resume["flag"] != "--context" {
			t.Errorf("resume = %v, want the invocation and --context", resume)
		}
		if got := resultOf(t, env)["action"]; got != "none" {
			t.Errorf("action = %v, want none", got)
		}
		// The loop has a run directory of its own; land opens the one of the run it is given.
		id, _ := resultOf(t, env)["run_id"].(string)
		data := ledgerData(t, filepath.Join(e.dir, ".agent-fox", "run", id), "triage.question")
		if data["text"] != "Which session?" || !slices.Equal(anyStrings(data["options"]), []string{"old", "new"}) {
			t.Errorf("triage.question = %v", data)
		}
		if n := len(e.forge.Writes()); n != 0 {
			t.Errorf("%d writes to the forge: %v", n, e.forge.Writes())
		}
	}

	t.Run("the loop", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		code, env := scriptedLoop(e, e.forge.IssueURL(7), questionPayload())
		check(t, e, code, env)
	})
	t.Run("land", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		id := e.briefRun()
		if code, env := e.submit(id, questionPayload()); code != 0 {
			t.Fatalf("submit: exit code %d, error %v", code, env["error"])
		}
		code, env := e.land(id)
		check(t, e, code, env)
	})
}

// TestEveryWayARunEndsWellIsDone_TS_08_58 verifies TS-08-58:
// a landing, --land none, a dry run and a not_reproducible diagnosis without a
// question all end done.
// Verifies: 08-REQ-9.2
func TestEveryWayARunEndsWellIsDone_TS_08_58(t *testing.T) {
	for name, payload := range map[string]string{"a diagnosis": diagnosisPayload(nil), "a diagnosis that is not reproducible": notReproducible()} {
		for mode, args := range map[string][]string{"landing": nil, "--land none": {"--land", "none"}, "--dry-run": {"--dry-run"}} {
			t.Run(name+", "+mode, func(t *testing.T) {
				e := newTriageEnv(t, fakeforge.GitHub)
				code, env := scriptedLoop(e, e.forge.IssueURL(7), payload, args...)
				if code != 0 || env["status"] != "done" {
					t.Errorf("exit code %d, status %v, error %v, want done", code, env["status"], env["error"])
				}
			})
		}
	}
}

// valid checks a value against the embedded result schema of a command.
func valid(t *testing.T, name string, result any) {
	t.Helper()
	s, err := schema.Parse(resultSchema(name))
	if err != nil {
		t.Fatalf("the schema %s: %v", name, err)
	}
	if err := schema.Validate(s, result); err != nil {
		t.Errorf("the result does not validate against %s: %v\n%v", name, err, result)
	}
}

// TestTheResultsValidateAgainstTheDeclaredSchemas_TS_08_59 verifies TS-08-59:
// the result of the loop and of land holds every field and passes the schema of
// the command, with the labels in first-seen order and each once.
// Verifies: 08-REQ-9.3
func TestTheResultsValidateAgainstTheDeclaredSchemas_TS_08_59(t *testing.T) {
	required := []string{"run_id", "issue", "action", "diagnosis", "rejected_path_calls", "rejected_submissions", "labels", "original_report_sha256"}
	check := func(t *testing.T, name string, env map[string]any, action string, labels []string) {
		t.Helper()
		got := resultOf(t, env)
		for _, field := range required {
			if _, ok := got[field]; !ok {
				t.Errorf("result has no %s: %v", field, got)
			}
		}
		if sha, _ := got["original_report_sha256"].(string); !sha256Hex.MatchString(sha) {
			t.Errorf("original_report_sha256 = %q, want the hash of the original report", got["original_report_sha256"])
		}
		if got["action"] != action || !slices.Equal(anyStrings(got["labels"]), labels) {
			t.Errorf("action %v, labels %v, want %s and %v", got["action"], got["labels"], action, labels)
		}
		valid(t, name, got)
	}

	t.Run("an issue, replaced, with a label twice", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		code, env := scriptedLoop(e, e.forge.IssueURL(7), diagnosisPayload(nil), "--label", "a", "--label", "b", "--label", "a")
		if code != 0 {
			t.Fatalf("exit code %d, error %v", code, env["error"])
		}
		check(t, "triage", env, "replaced_body", []string{"a", "b"})
		if issue, _ := resultOf(t, env)["issue"].(map[string]any); issue["number"] != float64(7) || issue["url"] != e.forge.IssueURL(7) {
			t.Errorf("issue = %v", issue)
		}
	})
	t.Run("a text report, a created issue", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		code, env := scriptedLoop(e, "the app crashes on submit", diagnosisPayload(nil), "--repo", "o/r")
		if code != 0 {
			t.Fatalf("exit code %d, error %v", code, env["error"])
		}
		check(t, "triage", env, "created_issue", []string{})
	})
	t.Run("--land none, no issue", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		code, env := scriptedLoop(e, e.forge.IssueURL(7), diagnosisPayload(nil), "--land", "none")
		if code != 0 {
			t.Fatalf("exit code %d, error %v", code, env["error"])
		}
		check(t, "triage", env, "none", []string{})
		if got := resultOf(t, env)["issue"]; got != nil {
			t.Errorf("issue = %v, want null: nothing was written", got)
		}
	})
	t.Run("a dry run", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		code, env := scriptedLoop(e, e.forge.IssueURL(7), diagnosisPayload(nil), "--dry-run", "--label", "x")
		if code != 0 {
			t.Fatalf("exit code %d, error %v", code, env["error"])
		}
		check(t, "triage", env, "planned", []string{}) // the label is planned (a side effect), not added
	})
	t.Run("land", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		id := e.landedRun()
		code, env := e.land(id, "--label", "a", "--label", "a")
		if code != 0 {
			t.Fatalf("exit code %d, error %v", code, env["error"])
		}
		check(t, "triage_land", env, "replaced_body", []string{"a"})
	})
	t.Run("a question", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		code, env := scriptedLoop(e, e.forge.IssueURL(7), questionPayload())
		if code != 3 {
			t.Fatalf("exit code %d, error %v", code, env["error"])
		}
		check(t, "triage", env, "none", []string{})
	})
	t.Run("the report changed while the run was on", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		id := e.briefRun()
		if code, env := e.submit(id, diagnosisPayload(nil)); code != 0 {
			t.Fatalf("submit: exit code %d, error %v", code, env["error"])
		}
		var facts triage.Facts
		if err := json.Unmarshal(readFile(t, filepath.Join(e.dir, ".agent-fox", "run", id, "triage.json")), &facts); err != nil {
			t.Fatal(err)
		}
		e.forge.AddIssue(fakeforge.Issue{Number: 7, Title: "Login fails", Body: "Edited since the brief was made."})

		code, env := e.land(id)

		if code != 0 || !slices.Contains(warningCodes(env), "report_changed_during_triage") {
			t.Fatalf("exit code %d, error %v, warnings %v", code, env["error"], warningCodes(env))
		}
		if got := resultOf(t, env)["original_report_sha256"]; got == facts.OriginalReportSHA256 || !sha256Hex.MatchString(got.(string)) {
			t.Errorf("original_report_sha256 = %v, want the hash of the report the body now holds, not %s", got, facts.OriginalReportSHA256)
		}
	})
}

// kinds are the kinds of the artifacts of an envelope that a triage run's files
// are, sorted: the shell's run_dir and a brain's transcript are not among them.
func kinds(env map[string]any) []string {
	var out []string
	list, _ := env["artifacts"].([]any)
	for _, a := range list {
		if kind := a.(map[string]any)["kind"].(string); slices.Contains([]string{"brief", "submission", "diagnosis"}, kind) {
			out = append(out, kind)
		}
	}
	slices.Sort(out)
	return out
}

// TestAFailedRunStillListsItsArtifacts_TS_08_60 verifies TS-08-60:
// a run that failed after a valid submission lists the brief, the submission and
// the diagnosis; one that failed before has the brief alone.
// Verifies: 08-REQ-9.4
func TestAFailedRunStillListsItsArtifacts_TS_08_60(t *testing.T) {
	t.Run("a landing write failed", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		e.forge.Fail(fakeforge.OpBody, fakeforge.Failure{Status: 500, Message: "boom"})

		code, env := scriptedLoop(e, e.forge.IssueURL(7), diagnosisPayload(nil))

		failedWith(t, code, env, 1, "forge")
		if got := kinds(env); !slices.Equal(got, []string{"brief", "diagnosis", "submission"}) {
			t.Errorf("artifacts = %v, want the brief, the diagnosis and the submission once each", got)
		}
	})
	t.Run("no submission", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "scripted", "--script", script(t, map[string]any{"tool": "read_file", "args": map[string]any{"path": "main.go"}}))

		failedWith(t, code, env, 1, "no_result")
		if got := kinds(env); !slices.Equal(got, []string{"brief"}) {
			t.Errorf("artifacts = %v, want the brief alone", got)
		}
	})
	t.Run("a usage failure", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--label", "a,b")
		failedWith(t, code, env, 2, "usage")
	})
	t.Run("land, landed again", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		id := e.landedRun()
		for i := range 2 {
			code, env := e.land(id)
			if code != 0 {
				t.Fatalf("landing %d: exit code %d, error %v", i+1, code, env["error"])
			}
			if got := kinds(env); !slices.Equal(got, []string{"brief", "diagnosis", "submission"}) {
				t.Errorf("landing %d: artifacts = %v, want the brief, the diagnosis and the submission once each", i+1, got)
			}
		}
	})
	t.Run("land, refused after an earlier landing", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		id := e.landedRun()
		if code, env := e.land(id); code != 0 {
			t.Fatalf("land: exit code %d, error %v", code, env["error"])
		}
		e.app.NewForge = e.forge.NewForge("")

		code, env := e.land(id)

		failedWith(t, code, env, 1, "auth")
		if got := kinds(env); !slices.Equal(got, []string{"brief", "diagnosis", "submission"}) {
			t.Errorf("artifacts = %v, want the brief, the diagnosis and the submission once each", got)
		}
	})
}

// TestAFixIsSuggestedAfterAConfirmedOrProbableDiagnosis_TS_08_61 verifies TS-08-61:
// a done run that wrote to an issue suggests af fix when the diagnosis is
// confirmed or probable, in the loop and in land.
// Verifies: 08-REQ-9.5
func TestAFixIsSuggestedAfterAConfirmedOrProbableDiagnosis_TS_08_61(t *testing.T) {
	for confidence, want := range map[string]bool{"confirmed": true, "probable": true, "suspected": false} {
		t.Run(confidence, func(t *testing.T) {
			e := newTriageEnv(t, fakeforge.GitHub)
			code, env := scriptedLoop(e, e.forge.IssueURL(7), withConfidence(confidence))
			if code != 0 {
				t.Fatalf("exit code %d, error %v", code, env["error"])
			}
			if got := hasNext(env, "af", "fix", e.forge.IssueURL(7), "--dir", e.dir); got != want {
				t.Errorf("next = %v, want af fix: %v", nextArgvs(env), want)
			}
		})
	}
	t.Run("not_reproducible", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		_, env := scriptedLoop(e, e.forge.IssueURL(7), notReproducible())
		if hasNext(env, "af", "fix", e.forge.IssueURL(7), "--dir", e.dir) {
			t.Errorf("next = %v: there is nothing to fix", nextArgvs(env))
		}
	})
	t.Run("a created issue", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		_, env := scriptedLoop(e, "the app crashes on submit", withConfidence("confirmed"), "--repo", "o/r")
		created, _ := resultOf(t, env)["issue"].(map[string]any)
		if url, _ := created["url"].(string); url == "" || !hasNext(env, "af", "fix", url, "--dir", e.dir) {
			t.Errorf("next = %v, want af fix of the issue that was created", nextArgvs(env))
		}
	})
	t.Run("--land none, no issue", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		_, env := scriptedLoop(e, e.forge.IssueURL(7), withConfidence("confirmed"), "--land", "none")
		for _, argv := range nextArgvs(env) {
			if len(argv) > 1 && argv[1] == "fix" {
				t.Errorf("next = %v: no issue holds the diagnosis", nextArgvs(env))
			}
		}
	})
	t.Run("a question", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		_, env := scriptedLoop(e, e.forge.IssueURL(7), questionPayload())
		for _, argv := range nextArgvs(env) {
			if len(argv) > 1 && argv[1] == "fix" {
				t.Errorf("next = %v: the run is not done", nextArgvs(env))
			}
		}
	})
	t.Run("land", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		id := e.briefRun()
		if code, env := e.submit(id, withConfidence("probable")); code != 0 {
			t.Fatalf("submit: exit code %d, error %v", code, env["error"])
		}
		code, env := e.land(id)
		if code != 0 || !hasNext(env, "af", "fix", e.forge.IssueURL(7), "--dir", e.dir) {
			t.Errorf("exit code %d, next = %v, want af fix", code, nextArgvs(env))
		}
	})
}

// TestAFailedLandingSuggestsLandingAgain_TS_08_62 verifies TS-08-62:
// a landing that failed after a valid submission suggests af triage land, naming
// the repository when the client was built from the URL of the input.
// Verifies: 08-REQ-9.6
func TestAFailedLandingSuggestsLandingAgain_TS_08_62(t *testing.T) {
	t.Run("an issue URL", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		e.forge.Fail(fakeforge.OpBody, fakeforge.Failure{Status: 500, Message: "boom"})

		code, env := scriptedLoop(e, e.forge.IssueURL(7), diagnosisPayload(nil))

		failedWith(t, code, env, 1, "forge")
		if want := []string{"af", "triage", "land", "--run-id", env["run_id"].(string), "--dir", e.dir, "--repo", "o/r"}; !hasNext(env, want...) {
			t.Errorf("next = %v, want %v", nextArgvs(env), want)
		}
	})
	t.Run("a text report with --repo", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		e.forge.Fail(fakeforge.OpCreate, fakeforge.Failure{Status: 500, Message: "boom"})

		code, env := scriptedLoop(e, "the app crashes on submit", diagnosisPayload(nil), "--repo", "o/r")

		failedWith(t, code, env, 1, "forge")
		if want := []string{"af", "triage", "land", "--run-id", env["run_id"].(string), "--dir", e.dir}; !hasNext(env, want...) {
			t.Errorf("next = %v, want %v", nextArgvs(env), want)
		}
	})
	t.Run("land itself", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		id := e.landedRun()
		e.forge.Fail(fakeforge.OpBody, fakeforge.Failure{Status: 500, Message: "boom"})
		code, env := e.land(id)
		failedWith(t, code, env, 1, "forge")
		if n := len(nextArgvs(env)); n != 0 {
			t.Errorf("next = %v: the loop suggests landing again, land does not", nextArgvs(env))
		}
	})
	t.Run("no submission", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "scripted", "--script", script(t, map[string]any{"tool": "read_file", "args": map[string]any{"path": "main.go"}}))
		failedWith(t, code, env, 1, "no_result")
		if n := len(nextArgvs(env)); n != 0 {
			t.Errorf("next = %v: there is nothing to land", nextArgvs(env))
		}
	})
}

// TestStepEventsStopWhereTheRunEnds_TS_08_63 verifies TS-08-63.
// Verifies: 08-REQ-9.7
func TestStepEventsStopWhereTheRunEnds_TS_08_63(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	_, env := scriptedLoop(e, e.forge.IssueURL(7), diagnosisPayload(nil))
	if got := stepNames(t, e.runDir(env)); !slices.Equal(got, []string{"preflight", "brief", "brain", "land"}) {
		t.Errorf("a full run: steps = %v", got)
	}

	_, env = e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--label", "a,b")
	if got := stepNames(t, e.runDir(env)); !slices.Equal(got, []string{"preflight"}) {
		t.Errorf("a preflight refusal: steps = %v", got)
	}

	_, env = e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "scripted", "--script", script(t, map[string]any{"tool": "read_file", "args": map[string]any{"path": "main.go"}}))
	if got := stepNames(t, e.runDir(env)); !slices.Equal(got, []string{"preflight", "brief", "brain"}) {
		t.Errorf("a run with no result: steps = %v", got)
	}
}

// The codes and categories that triage envelopes may carry: those of the shell,
// of spec 07 and of the map, and the six this scope adds. A failure category of
// triage's own is none.
var (
	allowedWarnings = []string{
		"input_truncated", "comments_unreadable", "ledger_unwritable", "events_unwritable", "envelope_unwritable", "output_unwritable", "invalid_warning_code",
		"map_outline_skipped",
		"brain_error", "submission_invalid", "submission_claim_mismatch", "submission_not_removed", "box_unconfined", "box_close_failed", "transcript_unwritable", "model_unpriced",
		"issue_closed", "report_changed_during_triage", "body_too_long", "labels_failed", "original_report_unparsed", "rejections_unreadable",
	}
	allowedCategories = []string{
		"usage", "auth", "forge", "io", "cancelled", "timeout", "internal", "config", "restore", "git", "sandbox", "submission", "brain", "no_result", "budget",
	}
	codeShape = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

var (
	seenMu        sync.Mutex
	seenEnvelopes []map[string]any
)

// see keeps an envelope for TS-08-64.
func see(env map[string]any) {
	seenMu.Lock()
	defer seenMu.Unlock()
	seenEnvelopes = append(seenEnvelopes, env)
}

// TestOnlyTheAllowedWarningCodesAndCategories_TS_08_64 verifies TS-08-64:
// the warnings and failure categories of the envelopes of the scenarios of this
// suite, those that ran before it and a battery of its own, are all allowed.
// Verifies: 08-REQ-9.8
func TestOnlyTheAllowedWarningCodesAndCategories_TS_08_64(t *testing.T) {
	// A battery that raises the warnings and fails the ways triage can.
	e := newTriageEnv(t, fakeforge.GitHub)
	e.forge.AddIssue(fakeforge.Issue{Number: 9, Title: "Old", Body: "It broke.", State: "closed"})
	scriptedLoop(e, e.forge.IssueURL(9), diagnosisPayload(nil), "--land", "none")
	e.loop("-", "--dir", e.dir) // an input with nothing in it
	e.run(strings.Repeat("a crash\n", 20000), "triage", "-", "--repo", "o/r", "--dir", e.dir, "--sandbox", "local")
	e.forge.Fail(fakeforge.OpLabels, fakeforge.Failure{Status: 500, Message: "boom"})
	scriptedLoop(e, e.forge.IssueURL(7), diagnosisPayload(nil), "--label", "x")
	e.forge.Fail(fakeforge.OpBody, fakeforge.Failure{Status: 401, Message: "bad credentials"})
	scriptedLoop(e, e.forge.IssueURL(7), diagnosisPayload(nil))
	scriptedLoop(e, e.forge.IssueURL(7), questionPayload())
	e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "scripted", "--script", script(t, map[string]any{"tool": "read_file", "args": map[string]any{"path": "main.go"}}))

	seenMu.Lock()
	envelopes := slices.Clone(seenEnvelopes)
	seenMu.Unlock()
	if len(envelopes) < 8 {
		t.Fatalf("only %d envelopes were seen", len(envelopes))
	}
	for _, env := range envelopes {
		for _, code := range warningCodes(env) {
			if !slices.Contains(allowedWarnings, code) || !codeShape.MatchString(code) {
				t.Errorf("the warning code %q is not an allowed code (command %v)", code, env["command"])
			}
		}
		if errObj, ok := env["error"].(map[string]any); ok {
			if category, _ := errObj["category"].(string); !slices.Contains(allowedCategories, category) || !codeShape.MatchString(category) {
				t.Errorf("the failure category %q is not an allowed category (command %v)", category, env["command"])
			}
		}
	}
	if got := warningCodes(map[string]any{"warnings": []any{}}); len(got) != 0 {
		t.Errorf("warningCodes of an envelope with none = %v", got)
	}
}

// TestTheSchemaHoldsTheFiveTriageResultSchemas_TS_08_68 verifies TS-08-68:
// af --schema names the result schema of each triage command. (That it equals
// the committed golden is TS-05-70.)
// Verifies: 08-REQ-10.4
func TestTheSchemaHoldsTheFiveTriageResultSchemas_TS_08_68(t *testing.T) {
	code, stdout, stderr := run(t, "--schema")
	if code != 0 || stderr != "" {
		t.Fatalf("exit code %d, stderr %q", code, stderr)
	}
	var doc struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"triage", "triage_brief", "triage_run", "triage_submit", "triage_land"} {
		def, ok := doc.Defs[name]
		if !ok {
			t.Errorf("af --schema has no result schema %s", name)
			continue
		}
		var want map[string]any
		if err := json.Unmarshal(resultSchema(name), &want); err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(def, &got); err != nil || got["description"] != want["description"] {
			t.Errorf("%s: the schema of --schema is not the embedded one (%v)", name, err)
		}
	}
}
