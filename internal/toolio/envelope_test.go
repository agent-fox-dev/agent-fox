package toolio

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// endings are a command for each way a run ends, with the status it ends in.
var endings = []struct {
	status string
	run    func(*Run) (Outcome, error)
}{
	{"done", func(*Run) (Outcome, error) { return Outcome{Status: StatusDone, Summary: "ok"}, nil }},
	{"unverified", func(*Run) (Outcome, error) { return Outcome{Status: StatusUnverified, Summary: "unchecked"}, nil }},
	{"needs_human", func(*Run) (Outcome, error) {
		return Outcome{Status: StatusNeedsHuman, NeedsHuman: &Question{Text: "which?", Options: make([]string, 26)}}, nil
	}},
	{"failed", func(*Run) (Outcome, error) {
		return Outcome{}, &Failure{Category: CategoryForge, Message: "m", FixHint: &FixHint{Flag: "--timeout", Env: "E", Var: "v", Suggest: "s"}}
	}},
	{"failed", func(*Run) (Outcome, error) { return Outcome{}, errors.New("plain") }},
	{"failed", func(*Run) (Outcome, error) { panic("boom") }},
	{"usage", func(*Run) (Outcome, error) { return Outcome{}, &Failure{Category: CategoryUsage, Message: "bad"} }},
}

func TestEnvelope_IsOneIndentedObjectWithEveryFieldInOrder(t *testing.T) {
	keys := []string{
		"schema_version", "tool", "run_id", "status", "exit_code", "summary", "input", "result",
		"needs_human", "error", "warnings", "artifacts", "side_effects", "next", "usage",
		"started_at", "finished_at", "duration_ms",
	}
	for _, e := range endings {
		app := newApp()
		app.Register(Command{Path: "X", Run: e.run})

		r := run(t, app, "X")

		var indented bytes.Buffer
		if err := json.Indent(&indented, []byte(strings.TrimSpace(r.stdout)), "", "  "); err != nil || indented.String()+"\n" != r.stdout {
			t.Errorf("%s: stdout is not one object, two-space indented, with one trailing newline (%v):\n%s", e.status, err, r.stdout)
		}
		if got := topLevelKeys(t, r.stdout); !slices.Equal(got, keys) {
			t.Errorf("%s: keys %v", e.status, got)
		}
		env := r.env(t)
		if env["schema_version"] != "5.0.0" || env["status"] != e.status {
			t.Errorf("%s: schema_version %v, status %v", e.status, env["schema_version"], env["status"])
		}
		for _, list := range []string{"warnings", "artifacts", "side_effects", "next"} {
			if !reflect.DeepEqual(env[list], []any{}) {
				t.Errorf("%s: %s = %v, want []", e.status, list, env[list])
			}
		}
	}
}

func TestEnvelope_TheStatusDecidesTheExitCodeAndWhatIsNull(t *testing.T) {
	for _, e := range endings {
		app := newApp()
		app.Register(Command{Path: "X", Run: e.run})

		r := run(t, app, "X")
		env := r.env(t)

		want := ExitCode(Status(e.status))
		if r.code != want || env["exit_code"] != float64(want) {
			t.Errorf("%s: Main returned %d and exit_code is %v, want %d", e.status, r.code, env["exit_code"], want)
		}
		if (env["needs_human"] != nil) != (e.status == "needs_human") || (env["error"] != nil) != (e.status == "failed" || e.status == "usage") {
			t.Errorf("%s: needs_human %v, error %v", e.status, env["needs_human"], env["error"])
		}
	}
	for s, code := range map[Status]int{StatusDone: 0, StatusFailed: 1, StatusUsage: 2, StatusNeedsHuman: 3, StatusUnverified: 4, "other": 1} {
		if ExitCode(s) != code {
			t.Errorf("ExitCode(%q) = %d, want %d", s, ExitCode(s), code)
		}
	}
}

func TestEnvelope_TimesAreUTCWithMillisecondsAndUsageIsNoBrain(t *testing.T) {
	start := time.Date(2026, 10, 20, 8, 26, 48, 123_000_000, time.FixedZone("plus1", 3600))
	calls := 0
	app := newApp()
	app.Now = func() time.Time {
		calls++
		if calls == 1 {
			return start
		}
		return start.Add(250 * time.Millisecond)
	}
	app.Register(returning("X", Outcome{Status: StatusDone, Summary: "ok"}, nil))

	r := run(t, app, "X")
	env := r.env(t)

	if env["started_at"] != "2026-10-20T07:26:48.123Z" || env["finished_at"] != "2026-10-20T07:26:48.373Z" || !strings.Contains(r.stdout, "\"duration_ms\": 250\n") {
		t.Errorf("started_at %v, finished_at %v; stdout:\n%s", env["started_at"], env["finished_at"], r.stdout)
	}
	want := map[string]any{"brain": "", "cost_usd": 0.0, "cost_source": "none", "input_tokens": 0.0, "output_tokens": 0.0, "turns": 0.0}
	if !reflect.DeepEqual(env["usage"], want) {
		t.Errorf("usage = %v, want %v", env["usage"], want)
	}
}

func TestEnvelope_AResultThatCannotBeEncodedIsAnInternalFailure(t *testing.T) {
	app := newApp()
	app.Register(returning("X", Outcome{Status: StatusDone, Result: make(chan int)}, nil))

	r := run(t, app, "X")

	if env := r.env(t); r.code != 1 || at(env, "error", "category") != CategoryInternal || env["result"] != nil {
		t.Errorf("exit %d, envelope %s; want an internal failure", r.code, r.stdout)
	}
}

func TestEnvelope_ACallerOutsideTheShellMakesAndFailsOneAsTheShellDoes(t *testing.T) {
	env := NewEnvelope("fix")
	env.Fail(&Failure{Category: CategoryConflict})
	env.Seal(time.Unix(100, 0), time.Unix(101, 500_000_000))
	data, err := env.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	got := testutil.Decode[map[string]any](t, data)
	if got["tool"] != "fix" || got["status"] != "failed" || got["exit_code"] != 1.0 || got["summary"] != "the command failed: conflict" ||
		got["duration_ms"] != 1500.0 || at(got, "error", "category") != "conflict" || at(got, "input", "kind") != "none" || !reflect.DeepEqual(got["warnings"], []any{}) {
		t.Errorf("envelope %s", data)
	}
}

func TestEnvelope_AFailureKeepsItsResultAndAFailureWithoutOneHasNone(t *testing.T) {
	for _, c := range []struct {
		name     string
		failure  Failure
		status   string
		wantNull bool
	}{
		{"failed", Failure{Category: CategoryConflict, Result: map[string]any{"job": "j"}}, "failed", false},
		{"usage", Failure{Category: CategoryUsage, Result: map[string]any{"job": "j"}}, "usage", false},
		{"no result", Failure{Category: CategoryConflict}, "failed", true},
	} {
		env := NewEnvelope("fix")
		env.Result = "left over"
		env.Fail(&c.failure)
		data, err := env.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		got := testutil.Decode[map[string]any](t, data)
		if got["status"] != c.status || (got["result"] == nil) != c.wantNull || (!c.wantNull && !reflect.DeepEqual(got["result"], map[string]any{"job": "j"})) {
			t.Errorf("%s: envelope %s", c.name, data)
		}
	}
}

func TestAmend_AddsToTheListsAndChangesNothingElse(t *testing.T) {
	env := NewEnvelope("fix")
	env.Result = struct {
		Zeta  int    `json:"zeta"`
		Alpha string `json:"alpha"`
	}{1, "a"} // fields out of alphabetical order, which a decoder into a map would sort
	env.Warnings = []Warning{{Code: "w1", Message: "the <first> & one"}}
	env.Artifacts = []Artifact{{Kind: "branch", Name: "fix/7-x"}}
	env.SideEffects = []SideEffect{{Kind: "push", Target: "origin/fix/7-x", State: StateDone}}
	env.Seal(time.Unix(100, 0), time.Unix(160, 0))
	data, err := env.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	amendment := Amendment{
		Warnings:    []Warning{{Code: "w2", Message: "hub said <no>"}},
		Artifacts:   []Artifact{{Kind: "hub_session", Name: "ses_1"}, {Kind: "sandbox", Name: "sbx_1"}},
		SideEffects: []SideEffect{{Kind: "label", Target: "issue 7", State: StateFailed}},
	}

	got, err := Amend(data, amendment)
	if err != nil {
		t.Fatal(err)
	}

	back := testutil.Decode[Envelope](t, got)
	if !reflect.DeepEqual(back.Warnings, append(env.Warnings, amendment.Warnings...)) ||
		!reflect.DeepEqual(back.Artifacts, append(env.Artifacts, amendment.Artifacts...)) ||
		!reflect.DeepEqual(back.SideEffects, append(env.SideEffects, amendment.SideEffects...)) {
		t.Errorf("the lists are %+v, %+v, %+v; want each followed by what was added", back.Warnings, back.Artifacts, back.SideEffects)
	}
	strip := func(doc []byte) string {
		m := testutil.Decode[map[string]json.RawMessage](t, doc)
		delete(m, "warnings")
		delete(m, "artifacts")
		delete(m, "side_effects")
		return string(testutil.JSON(t, m))
	}
	if strip(got) != strip(data) || strings.Index(string(got), `"zeta"`) > strings.Index(string(got), `"alpha"`) {
		t.Errorf("the document changed beyond the three lists:\n%s\nwas\n%s", got, data)
	}
	if !strings.Contains(string(got), "\n    {\n      \"kind\": \"hub_session\",\n      \"name\": \"ses_1\"\n    },\n") || !strings.Contains(string(got), "hub said <no>") {
		t.Errorf("what was added is not indented as the rest, or is escaped:\n%s", got)
	}
	if again, err := AddWarnings(got, Warning{Code: "w3", Message: "three"}); err != nil || len(testutil.Decode[Envelope](t, again).Warnings) != 3 {
		t.Errorf("AddWarnings: %v\n%s", err, again)
	}
	if same, err := Amend(data, Amendment{}); err != nil || !bytes.Equal(same, data) {
		t.Errorf("an empty amendment changed the document: %v", err)
	}
}

func TestAmend_RefusesWhatIsNoEnvelope(t *testing.T) {
	for _, in := range []string{"", "[]", "null", `{"status":"done"}`, `{"warnings": 3}`, `{"warnings": [`, `{"a": }`} {
		if got, err := AddWarnings([]byte(in), Warning{Code: "a"}); err == nil {
			t.Errorf("AddWarnings(%q) = %q, want an error", in, got)
		}
	}
	if _, err := Amend([]byte(`{"warnings": []}`), Amendment{Artifacts: []Artifact{{Kind: "x"}}}); err == nil {
		t.Error("an envelope with no artifacts was amended")
	}
}
