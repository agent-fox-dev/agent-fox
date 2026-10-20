package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/triage"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// landedRun is a run of the attended session up to the landing: the brief of
// issue 7, and a valid diagnosis submitted.
func (e *triageEnv) landedRun() string {
	e.t.Helper()
	id := e.briefRun()
	if code, env := e.submit(id, diagnosisPayload(nil)); code != 0 {
		e.t.Fatalf("submit: exit code %d, error %v", code, env["error"])
	}
	return id
}

func (e *triageEnv) land(id string, args ...string) (int, map[string]any) {
	e.t.Helper()
	return e.run("", append([]string{"triage", "land", "--run-id", id, "--dir", e.dir}, args...)...)
}

// failedWith asserts a failed envelope of the category with the exit code.
func failedWith(t *testing.T, code int, env map[string]any, wantCode int, category string) map[string]any {
	t.Helper()
	errObj, _ := env["error"].(map[string]any)
	if code != wantCode || errObj["category"] != category || env["result"] != nil {
		t.Fatalf("exit code %d, status %v, error %v, result %v; want exit code %d, category %s and no result", code, env["status"], errObj, env["result"], wantCode, category)
	}
	return errObj
}

// closeCounter is a client that counts how often it is closed.
type closeCounter struct {
	issuex.Client
	closes *int
}

func (c closeCounter) Close() error {
	*c.closes++
	return c.Client.Close()
}

// TestLandBuildsItsClientForTheRecordedRepositoryAndClosesIt_TS_08_6 verifies TS-08-6:
// af triage land builds its client through NewForge with the recorded
// repository and its host, af/<version> and --dir, wraps it for --dry-run, and
// closes it once when the command returns.
// Verifies: 08-REQ-1.6
func TestLandBuildsItsClientForTheRecordedRepositoryAndClosesIt_TS_08_6(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	id := e.landedRun()
	// The run was made against a host that the environment does not name.
	factsPath := filepath.Join(e.dir, ".agent-fox", "run", id, "triage.json")
	var facts triage.Facts
	if err := json.Unmarshal(readFile(t, factsPath), &facts); err != nil {
		t.Fatal(err)
	}
	facts.Target.Host, facts.Issue.Host = "ghe.example.com", "ghe.example.com"
	data, _ := json.Marshal(facts)
	if err := os.WriteFile(factsPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	var options []issuex.Options
	closes := 0
	build := e.forge.NewForge("test-token")
	e.app.NewForge = func(o issuex.Options) (issuex.Client, error) {
		options = append(options, o)
		client, err := build(o)
		return closeCounter{Client: client, closes: &closes}, err
	}

	t.Run("a real landing", func(t *testing.T) {
		code, env := e.land(id)

		if code != 0 || env["status"] != "done" {
			t.Fatalf("exit code %d, error %v", code, env["error"])
		}
		if len(options) != 1 {
			t.Fatalf("NewForge was called %d times, want once", len(options))
		}
		o := options[0]
		if o.Repo != (issuex.Repo{Owner: "o", Name: "r", Host: "ghe.example.com"}) || !strings.HasPrefix(o.UserAgent, "af/") || o.Dir != e.dir {
			t.Errorf("options = %+v, want the recorded repository with its host, af/<version> and --dir %s", o, e.dir)
		}
		if closes != 1 {
			t.Errorf("the client was closed %d times, want once", closes)
		}
		if n := e.forge.Count(fakeforge.OpBody); n != 1 {
			t.Errorf("%d body writes, want 1", n)
		}
	})

	t.Run("a dry run", func(t *testing.T) {
		options, closes = nil, 0
		writes := len(e.forge.Writes())

		code, env := e.land(id, "--dry-run", "--label", "triaged")

		if code != 0 || env["status"] != "done" || resultOf(t, env)["action"] != "planned" {
			t.Fatalf("exit code %d, result %v, error %v", code, env["result"], env["error"])
		}
		if len(e.forge.Writes()) != writes {
			t.Errorf("a dry run wrote to the forge: %v", e.forge.Writes()[writes:])
		}
		var planned []string
		for _, s := range env["side_effects"].([]any) {
			m := s.(map[string]any)
			if m["state"] == "planned" {
				planned = append(planned, m["kind"].(string))
			}
		}
		if !slices.Equal(planned, []string{"update_issue", "add_labels"}) {
			t.Errorf("planned side effects = %v, want update_issue and add_labels", planned)
		}
		if closes != 1 {
			t.Errorf("the client was closed %d times, want once", closes)
		}
	})
}

// TestLandWithAnotherRepoBuildsNothing_TS_08_7 verifies TS-08-7:
// --repo that names another repository than the one recorded is a usage failure
// that names both, with no client built and nothing written.
// Verifies: 08-REQ-1.7
func TestLandWithAnotherRepoBuildsNothing_TS_08_7(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	id := e.landedRun()
	built := 0
	e.app.NewForge = func(issuex.Options) (issuex.Client, error) { built++; return nil, os.ErrInvalid }

	code, env := e.land(id, "--repo", "other/name")

	errObj := failedWith(t, code, env, 2, "usage")
	message := errObj["message"].(string)
	if !strings.Contains(message, "o/r") || !strings.Contains(message, "other/name") {
		t.Errorf("message = %q, want it to name both repositories", message)
	}
	if built != 0 {
		t.Errorf("NewForge was called %d times, want none", built)
	}
	if _, err := os.Stat(filepath.Join(e.dir, ".agent-fox", "run", id, "diagnosis.md")); err == nil {
		t.Error("diagnosis.md was written")
	}
	if n := len(e.forge.Writes()); n != 0 {
		t.Errorf("%d writes to the forge", n)
	}

	// The same repository, spelled the way a person might, is the one recorded.
	e.app.NewForge = e.forge.NewForge("test-token")
	if code, env := e.land(id, "--repo", "O/R"); code != 0 {
		t.Errorf("--repo O/R: exit code %d, error %v, want the recorded repository accepted", code, env["error"])
	}
}

// TestLandingIsTheSameWhateverRejectionsHolds_TS_08_42 verifies TS-08-42:
// the same submission lands with the same action and the same body for any
// content of rejections.jsonl.
// Verifies: 08-REQ-6.4
func TestLandingIsTheSameWhateverRejectionsHolds_TS_08_42(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	id := e.landedRun()
	rejections := filepath.Join(e.dir, ".agent-fox", "run", id, "rejections.jsonl")
	many := strings.Repeat(`{"ts":"2026-10-20T08:00:00Z","problems":[],"unresolved_paths":["x.go"]}`+"\n", 40)
	var bodies, actions []string

	for _, content := range []*string{nil, &many, ptr("{garbage\nnot json at all\n")} {
		_ = os.Remove(rejections)
		if content != nil {
			if err := os.WriteFile(rejections, []byte(*content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		before := len(e.forge.Writes())

		code, env := e.land(id)

		if code != 0 || env["status"] != "done" {
			t.Fatalf("exit code %d, error %v", code, env["error"])
		}
		actions = append(actions, resultOf(t, env)["action"].(string))
		writes := e.forge.Writes()[before:]
		if len(writes) != 1 {
			t.Fatalf("%d writes, want 1: %v", len(writes), writes)
		}
		bodies = append(bodies, writes[0].Raw)
	}

	if bodies[0] != bodies[1] || bodies[1] != bodies[2] {
		t.Errorf("the bodies differ:\n%s\n%s\n%s", bodies[0], bodies[1], bodies[2])
	}
	if actions[0] != "replaced_body" || actions[1] != actions[0] || actions[2] != actions[0] {
		t.Errorf("actions = %v", actions)
	}
}

func ptr(s string) *string { return &s }

// TestLandWithNoSubmissionWritesNothing guards 08-REQ-8.1 through the command.
func TestLandWithNoSubmissionWritesNothing(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	id := e.briefRun()

	code, env := e.land(id)

	failedWith(t, code, env, 1, "no_result")
	if _, err := os.Stat(filepath.Join(e.dir, ".agent-fox", "run", id, "diagnosis.md")); err == nil {
		t.Error("diagnosis.md was written")
	}
	if n := len(e.forge.Writes()); n != 0 {
		t.Errorf("%d writes to the forge", n)
	}
	if arts := artifactsOf(env, "brief"); len(arts) != 1 {
		t.Errorf("artifacts = %v, want the brief listed", env["artifacts"])
	}
}

// TestLandNoneNeedsNoForge guards 08-REQ-8.2 through the command: with --land
// none no client is built, no credential is needed and diagnosis.md is written.
func TestLandNoneNeedsNoForge(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	id := e.landedRun()
	e.app.NewForge = func(issuex.Options) (issuex.Client, error) { t.Error("a client was built"); return nil, os.ErrInvalid }
	before := len(e.forge.Requests())

	code, env := e.land(id, "--land", "none")

	if code != 0 || env["status"] != "done" || resultOf(t, env)["action"] != "none" {
		t.Fatalf("exit code %d, result %v, error %v", code, env["result"], env["error"])
	}
	if len(e.forge.Requests()) != before {
		t.Error("a request reached the forge")
	}
	md := readFile(t, filepath.Join(e.dir, ".agent-fox", "run", id, "diagnosis.md"))
	if !bytes.Contains(md, []byte("## Diagnosis")) || !bytes.Contains(md, []byte(originalReport)) {
		t.Errorf("diagnosis.md:\n%s", md)
	}
	if arts := artifactsOf(env, "diagnosis"); len(arts) != 1 {
		t.Errorf("artifacts = %v, want the diagnosis", env["artifacts"])
	}
}

// TestLandRefusesWhatCannotLand guards 08-REQ-2.6 and 08-REQ-2.2 through the
// command: no credential is auth with the message and the variable to set, and
// the flags are checked.
func TestLandRefusesWhatCannotLand(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	id := e.landedRun()

	t.Run("no credential", func(t *testing.T) {
		e.app.NewForge = e.forge.NewForge("")
		code, env := e.land(id)
		errObj := failedWith(t, code, env, 1, "auth")
		hint, _ := errObj["fix_hint"].(map[string]any)
		if want := "creating or updating an issue in o/r needs a credential: set GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN, or pass --dry-run or --land none"; errObj["message"] != want || hint["env"] != "GITHUB_TOKEN" {
			t.Errorf("error = %v, want %q and GITHUB_TOKEN", errObj, want)
		}
	})
	e.app.NewForge = e.forge.NewForge("test-token")
	for name, args := range map[string][]string{
		"labels with --land none":          {"--land", "none", "--label", "x"},
		"a label with a comma":             {"--label", "a,b"},
		"a land mode that is none of them": {"--land", "pr"},
	} {
		t.Run(name, func(t *testing.T) {
			code, env := e.land(id, args...)
			failedWith(t, code, env, 2, "usage")
		})
	}
	if n := len(e.forge.Writes()); n != 0 {
		t.Errorf("%d writes to the forge by refused landings", n)
	}
}

// TestLandOfATextReportCreatesOneIssueAndLaterLandingsUpdateIt guards 08-REQ-8.5
// through the command: the issue the ledger says an earlier landing created is
// updated, and not created again.
func TestLandOfATextReportCreatesOneIssueAndLaterLandingsUpdateIt(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	code, env := e.run("", "triage", "brief", "the app crashes on submit", "--repo", "o/r", "--dir", e.dir)
	if code != 0 {
		t.Fatalf("triage brief: exit code %d, error %v", code, env["error"])
	}
	id := env["run_id"].(string)
	if code, env := e.submit(id, diagnosisPayload(nil)); code != 0 {
		t.Fatalf("submit: exit code %d, error %v", code, env["error"])
	}

	for i := range 3 {
		code, env := e.land(id, "--label", "triaged")
		if code != 0 {
			t.Fatalf("landing %d: exit code %d, error %v", i+1, code, env["error"])
		}
		want := "created_issue"
		if i > 0 {
			want = "replaced_body"
		}
		if got := resultOf(t, env)["action"]; got != want {
			t.Errorf("landing %d: action %v, want %s", i+1, got, want)
		}
	}

	if got := e.forge.Count(fakeforge.OpCreate); got != 1 {
		t.Errorf("%d create requests, want exactly 1", got)
	}
	issue := e.forge.Issue(100)
	if issue.Title != "Run starts the wrong thing" || !strings.Contains(issue.Body, "the app crashes on submit") || !slices.Contains(issue.Labels, "triaged") {
		t.Errorf("the created issue = %+v", issue)
	}
	if kinds := ledgerKinds(t, filepath.Join(e.dir, ".agent-fox", "run", id)); strings.Count(strings.Join(kinds, ","), "triage.land") != 3 {
		t.Errorf("ledger = %v, want three triage.land records", kinds)
	}
}

// TestTheLandSchemaMatchesItsResult guards 08-REQ-10.4 for af triage land.
func TestTheLandSchemaMatchesItsResult(t *testing.T) {
	checkSchema(t, "triage_land", triageResult{})
}
