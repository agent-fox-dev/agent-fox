package toolio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestDispatch_TheLongestRegisteredPathWins(t *testing.T) {
	var gate, gateRun recorder
	app := newApp()
	app.Register(gate.command(Command{Path: "gate"}))
	app.Register(gateRun.command(Command{Path: "gate run", Dir: true}))
	dir := t.TempDir()

	r := run(t, app, "gate", "run", "--dir", dir)
	if r.code != 0 || r.env(t)["tool"] != "gate run" || gateRun.ran != 1 || gate.ran != 0 || gateRun.last.Dir != dir {
		t.Errorf("gate run: exit %d, envelope %s, gate run ran %d, gate %d", r.code, r.stdout, gateRun.ran, gate.ran)
	}
	if r := run(t, app, "gate"); r.env(t)["tool"] != "gate" || gate.ran != 1 {
		t.Errorf("gate: envelope %s, gate ran %d", r.stdout, gate.ran)
	}
}

func TestDispatch_NoOrAnUnknownCommandIsUsageListingTheCommands(t *testing.T) {
	var rec recorder
	app := newApp()
	app.Register(rec.command(Command{Path: "alpha"}))
	app.Register(rec.command(Command{Path: "beta gamma"}))
	for _, tc := range []struct {
		app  *App
		args []string
		want []string
	}{
		{app, nil, []string{"no command given", "alpha", "beta gamma"}},
		{app, []string{"nosuch", "--flag"}, []string{`unknown command "nosuch"`, "alpha", "beta gamma"}},
		{newApp(), []string{"nosuch"}, []string{"the commands are: none"}},
	} {
		r := run(t, tc.app, tc.args...)
		env := r.env(t)
		msg := fmt.Sprint(at(env, "error", "message"))
		if r.code != 2 || env["status"] != "usage" || env["tool"] != "af" || at(env, "error", "category") != CategoryUsage || r.stderr != "" {
			t.Errorf("%q: exit %d, envelope %s, stderr %q", tc.args, r.code, r.stdout, r.stderr)
		}
		for _, w := range tc.want {
			if !strings.Contains(msg, w) {
				t.Errorf("%q: message %q, want it to contain %q", tc.args, msg, w)
			}
		}
	}
	if rec.ran != 0 {
		t.Errorf("a command ran %d times", rec.ran)
	}
}

func TestOutcome_IsCopiedIntoTheEnvelope(t *testing.T) {
	for _, tc := range []struct {
		status   Status
		question *Question
		code     int
	}{
		{StatusDone, nil, 0},
		{StatusUnverified, nil, 4},
		{StatusNeedsHuman, &Question{Text: "which?"}, 3},
	} {
		app := newApp()
		app.Register(returning("X", Outcome{Status: tc.status, Summary: "ok", Result: map[string]int{"n": 1}, NeedsHuman: tc.question}, nil))

		r := run(t, app, "X")
		env := r.env(t)

		if r.code != tc.code || env["status"] != string(tc.status) || env["exit_code"] != float64(tc.code) || env["summary"] != "ok" ||
			!reflect.DeepEqual(env["result"], map[string]any{"n": 1.0}) || env["error"] != nil || (env["needs_human"] != nil) != (tc.question != nil) {
			t.Errorf("%s: exit %d, envelope %s", tc.status, r.code, r.stdout)
		}
	}
}

func TestOutcome_AnErrorBecomesTheErrorObject(t *testing.T) {
	forge := &Failure{Category: CategoryForge, Message: "m", Retryable: true, FixHint: &FixHint{Env: "X"}}
	hint := map[string]any{"env": "X"}
	for _, tc := range []struct {
		name   string
		err    error
		status string
		want   map[string]any
	}{
		{"a failure", forge, "failed", map[string]any{"category": "forge", "message": "m", "retryable": true, "fix_hint": hint}},
		{"a wrapped failure", fmt.Errorf("while reading: %w", forge), "failed",
			map[string]any{"category": "forge", "message": "while reading: m", "retryable": true, "fix_hint": hint}},
		{"a usage failure", &Failure{Category: CategoryUsage, Message: "bad"}, "usage",
			map[string]any{"category": "usage", "message": "bad", "retryable": false, "fix_hint": nil}},
		{"a category of a later command", &Failure{Category: CategoryGit, Message: "it broke"}, "failed",
			map[string]any{"category": "git", "message": "it broke", "retryable": false, "fix_hint": nil}},
		{"a failure without a category", &Failure{Message: "m"}, "failed",
			map[string]any{"category": "internal", "message": "m", "retryable": false, "fix_hint": nil}},
		{"a plain error", errors.New("disk on fire"), "failed",
			map[string]any{"category": "internal", "message": "disk on fire", "retryable": false, "fix_hint": nil}},
	} {
		app := newApp()
		app.Register(returning("X", Outcome{Status: StatusDone, Result: "dropped"}, tc.err))

		r := run(t, app, "X")
		env := r.env(t)

		if r.code != ExitCode(Status(tc.status)) || env["status"] != tc.status || env["result"] != nil || !reflect.DeepEqual(env["error"], tc.want) {
			t.Errorf("%s: exit %d, status %v, result %v, error %v; want %s and %v", tc.name, r.code, env["status"], env["result"], env["error"], tc.status, tc.want)
		}
	}
}

func TestOutcome_APanicIsAnInternalFailureWithItsStack(t *testing.T) {
	app := newApp()
	app.Register(Command{Path: "X", UsesRunDir: true, Run: func(*Run) (Outcome, error) { panic("boom") }})
	app.Register(Command{Path: "bare", Dir: true, Run: func(*Run) (Outcome, error) { panic("boom") }})
	app.Register(Command{Path: "norun"})

	dir := t.TempDir()
	r := run(t, app, "X", "--dir", dir)
	env := r.env(t)
	if r.code != 1 || at(env, "error", "category") != CategoryInternal || !strings.Contains(r.stderr, "boom") || !strings.Contains(r.stderr, "goroutine") {
		t.Errorf("exit %d, envelope %s, stderr %q; want an internal failure and the stack on stderr", r.code, r.stdout, r.stderr)
	}
	panicTxt := testutil.ReadFile(t, filepath.Join(agentFoxDir(dir), "run", fmt.Sprint(env["run_id"]), "panic.txt"))
	if !strings.Contains(panicTxt, "boom") || !strings.Contains(panicTxt, "goroutine") {
		t.Errorf("panic.txt = %q, want the panic and its stack", panicTxt)
	}

	other := t.TempDir()
	if r := run(t, app, "bare", "--dir", other); r.code != 1 {
		t.Errorf("a panic without a run directory: exit %d", r.code)
	}
	if _, err := os.Stat(agentFoxDir(other)); err == nil {
		t.Error("a panic without a run directory created one")
	}
	if r := run(t, app, "norun"); r.code != 1 || at(r.env(t), "error", "category") != CategoryInternal {
		t.Errorf("a command with no Run: %s", r.stdout)
	}
}

func TestOutcome_AnInconsistentOneIsAnInternalFailure(t *testing.T) {
	for name, out := range map[string]Outcome{
		"needs_human without a question": {Status: StatusNeedsHuman},
		"needs_human with a blank text":  {Status: StatusNeedsHuman, NeedsHuman: &Question{Text: "  "}},
		"done with a question":           {Status: StatusDone, NeedsHuman: &Question{Text: "which?"}},
		"unverified with a question":     {Status: StatusUnverified, NeedsHuman: &Question{Text: "which?"}},
		"27 options":                     {Status: StatusNeedsHuman, NeedsHuman: &Question{Text: "which?", Options: make([]string, 27)}},
		"no status":                      {},
		"failed as an outcome":           {Status: StatusFailed},
	} {
		app := newApp()
		app.Register(returning("X", out, nil))

		r := run(t, app, "X")
		env := r.env(t)

		if r.code != 1 || at(env, "error", "category") != CategoryInternal || env["needs_human"] != nil {
			t.Errorf("%s: exit %d, envelope %s; want an internal failure", name, r.code, r.stdout)
		}
	}
}

func TestOutcome_TheSummaryIsOneLineAndNeverEmptyWhenTheShellSetsTheStatus(t *testing.T) {
	multi := "first\nsecond\r\nthird\rfourth"
	for _, tc := range []struct {
		name string
		run  func(*Run) (Outcome, error)
	}{
		{"done", func(*Run) (Outcome, error) { return Outcome{Status: StatusDone, Summary: multi}, nil }},
		{"needs_human", func(*Run) (Outcome, error) {
			return Outcome{Status: StatusNeedsHuman, Summary: multi, NeedsHuman: &Question{Text: "which?"}}, nil
		}},
		{"a failure", func(*Run) (Outcome, error) { return Outcome{}, &Failure{Category: CategoryForge, Message: multi} }},
		{"a plain error", func(*Run) (Outcome, error) { return Outcome{}, errors.New(multi) }},
		{"a panic", func(*Run) (Outcome, error) { panic("boom\nwith two lines") }},
		{"an inconsistent outcome", func(*Run) (Outcome, error) { return Outcome{Status: StatusNeedsHuman}, nil }},
		{"usage without a message", func(*Run) (Outcome, error) { return Outcome{}, &Failure{Category: CategoryUsage} }},
		{"internal without a message", func(*Run) (Outcome, error) { return Outcome{}, &Failure{Category: CategoryInternal} }},
		{"cancelled without a message", func(*Run) (Outcome, error) { return Outcome{}, &Failure{Category: CategoryCancelled} }},
		{"timeout without a message", func(*Run) (Outcome, error) { return Outcome{}, &Failure{Category: CategoryTimeout} }},
		{"another category without a message", func(*Run) (Outcome, error) { return Outcome{}, &Failure{Category: CategoryBudget} }},
	} {
		app := newApp()
		app.Register(Command{Path: "X", Run: tc.run})

		summary := fmt.Sprint(run(t, app, "X").env(t)["summary"])

		if strings.ContainsAny(summary, "\r\n") || strings.TrimSpace(summary) == "" {
			t.Errorf("%s: summary %q, want one line that is not empty", tc.name, summary)
		}
	}
}

func TestNeedsHuman_TheOptionsAreLetteredInOrder(t *testing.T) {
	app := newApp()
	app.Register(returning("lettered", Outcome{Status: StatusNeedsHuman, NeedsHuman: &Question{Text: "Which?", Options: []string{"x", "y"}}}, nil))
	app.Register(returning("free", Outcome{Status: StatusNeedsHuman, NeedsHuman: &Question{Text: "Why?"}}, nil))

	r := run(t, app, "lettered")
	want := []any{map[string]any{"id": "A", "label": "x"}, map[string]any{"id": "B", "label": "y"}}
	if env := r.env(t); r.code != 3 || at(env, "needs_human", "question") != "Which?" || !reflect.DeepEqual(at(env, "needs_human", "options"), want) {
		t.Errorf("exit %d, needs_human %v; want the question and the options %v", r.code, env["needs_human"], want)
	}
	if options := at(run(t, app, "free").env(t), "needs_human", "options"); !reflect.DeepEqual(options, []any{}) {
		t.Errorf("a question for free text has the options %v, want []", options)
	}
}

func TestNeedsHuman_ResumeIsTheInvocationWithEveryAnswerSoFar(t *testing.T) {
	var contexts [][]string
	app := newApp()
	app.Register(Command{Path: "X", Context: true, Run: func(r *Run) (Outcome, error) {
		contexts = append(contexts, r.Context)
		return Outcome{Status: StatusNeedsHuman, NeedsHuman: &Question{Text: "which?", Options: []string{"x", "y"}}}, nil
	}})
	var out strings.Builder
	app.Main(context.Background(), []string{"/tmp/bin/whatever", "X", "text", "--context", "A"}, strings.NewReader(""), &out, io.Discard)
	env := testutil.Decode[map[string]any](t, []byte(out.String()))

	argv := strs(at(env, "needs_human", "resume", "argv"))
	if !slices.Equal(argv, []string{"af", "X", "text", "--context", "A"}) || at(env, "needs_human", "resume", "flag") != "--context" {
		t.Fatalf("resume = %v, want the invocation with af as argv[0] and the flag --context", at(env, "needs_human", "resume"))
	}
	app.Main(context.Background(), append(argv, "--context", "B"), strings.NewReader(""), io.Discard, io.Discard)
	if !reflect.DeepEqual(contexts, [][]string{{"A"}, {"A", "B"}}) {
		t.Errorf("the contexts of the two runs are %q, want [A] then [A B]", contexts)
	}
}

func TestNeedsHuman_AQuestionMayNameTheCommandThatAnswersIt(t *testing.T) {
	app := newApp()
	app.Register(returning("ask", Outcome{Status: StatusNeedsHuman, NeedsHuman: &Question{
		Text: "Which?", ResumeArgv: []string{"af", "jobs", "answer", "j", "--wait"},
	}}, nil))

	resume := at(run(t, app, "ask").env(t), "needs_human", "resume")

	if !slices.Equal(strs(at(resume, "argv")), []string{"af", "jobs", "answer", "j", "--wait"}) || at(resume, "flag") != "--context" {
		t.Errorf("resume = %v, want the question's argv and the flag --context", resume)
	}
}

func TestRun_WarningsAreRecordedOnceInTheOrderRaisedAndNeverChangeTheStatus(t *testing.T) {
	var seen []Warning
	app := newApp()
	app.Register(Command{Path: "X", Run: func(r *Run) (Outcome, error) {
		for _, w := range []string{"alpha:one", "beta_2:two", "alpha:one", "alpha:two", "Bad Code!:a message", "beta_2:two"} {
			code, msg, _ := strings.Cut(w, ":")
			r.Warn(code, msg)
		}
		seen = r.Warnings()
		return Outcome{Status: StatusDone, Summary: "ok"}, nil
	}})

	r := run(t, app, "X")

	want := []Warning{{"alpha", "one"}, {"beta_2", "two"}, {"alpha", "two"}, {WarnInvalidCode, `"Bad Code!": a message`}}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("Run.Warnings = %v, want %v", seen, want)
	}
	if got := testutil.Decode[Envelope](t, []byte(r.stdout)); r.code != 0 || got.Status != StatusDone || !reflect.DeepEqual(got.Warnings, want) {
		t.Errorf("exit %d, status %s, warnings %v; want done and %v", r.code, got.Status, got.Warnings, want)
	}
}

func TestRun_WhatTheCommandRecordsIsInTheEnvelope(t *testing.T) {
	app := newApp()
	app.Register(Command{Path: "X", MaxArgs: NoArgs, Run: func(r *Run) (Outcome, error) {
		r.Artifact(Artifact{Kind: "file", Path: filepath.Join("out", "result.txt")})
		r.Artifact(Artifact{Kind: "issue", URL: "https://example.com/o/r/issues/1", Number: 1})
		r.SideEffect(SideEffect{Kind: "push", Target: "origin/main", State: StateDone})
		r.Next(Next{Reason: "look"})
		r.SetUsage(Usage{Brain: "scripted", CostUSD: 0.5, CostSource: CostReported, Turns: 2})
		r.SetInput(KindFile, "/abs/notes.txt", true)
		return Outcome{Status: StatusDone, Summary: "ok"}, nil
	}})

	env := run(t, app, "X").env(t)

	abs, _ := filepath.Abs(filepath.Join("out", "result.txt"))
	for _, c := range []struct {
		field string
		want  any
	}{
		{"artifacts", []any{
			map[string]any{"kind": "file", "path": abs},
			map[string]any{"kind": "issue", "url": "https://example.com/o/r/issues/1", "number": 1.0},
		}},
		{"side_effects", []any{map[string]any{"kind": "push", "target": "origin/main", "state": "done"}}},
		{"next", []any{map[string]any{"argv": []any{}, "reason": "look"}}},
		{"usage", map[string]any{"brain": "scripted", "cost_usd": 0.5, "cost_source": "reported", "input_tokens": 0.0, "output_tokens": 0.0, "turns": 2.0}},
		{"input", map[string]any{"kind": "file", "origin": "/abs/notes.txt", "truncated": true}},
	} {
		if !reflect.DeepEqual(env[c.field], c.want) {
			t.Errorf("%s = %v, want %v", c.field, env[c.field], c.want)
		}
	}
}

func TestRun_ARedactedValueIsInNoOutputOfTheInvocation(t *testing.T) {
	const secret = `sup"er\secret-value<&>`
	for name, fail := range map[string]bool{"done": false, "failed": true} {
		app := newApp()
		app.Register(Command{Path: "leak", MaxArgs: NoArgs, UsesRunDir: true, Run: func(r *Run) (Outcome, error) {
			r.Redact(secret, "short")
			r.Emit("note", map[string]string{"said": "the secret is " + secret})
			r.Warn("leaked", "a warning with "+secret)
			if fail {
				return Outcome{}, &Failure{Category: CategoryInternal, Message: "it failed with " + secret}
			}
			return Outcome{Status: StatusDone, Summary: "ok " + secret, Result: map[string]string{"echo": secret, "short": "short"}}, nil
		}})
		var sunk strings.Builder
		app.EventSink = func(line []byte) { sunk.Write(line) }
		dir := t.TempDir()

		r := run(t, app, "leak", "--dir", dir)

		outputs := map[string]string{"stdout": r.stdout, "stderr": r.stderr, "the sink": sunk.String()}
		for path, content := range testutil.Files(t, agentFoxDir(dir)) {
			outputs[path] = content
		}
		for where, text := range outputs {
			if strings.Contains(text, "secret-value") {
				t.Errorf("%s: %s holds the secret:\n%s", name, where, text)
			}
		}
		if !fail && at(r.env(t), "result", "short") != "short" {
			t.Errorf("a value too short to be a secret was redacted: %s", r.stdout)
		}
	}
}

// brokenWriter is a stdout that cannot be written to.
type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestPublish_ABrokenStdoutKeepsTheExitCode(t *testing.T) {
	for want, end := range map[int]func(*Run) (Outcome, error){
		0: func(*Run) (Outcome, error) { return Outcome{Status: StatusDone}, nil },
		3: func(*Run) (Outcome, error) {
			return Outcome{Status: StatusNeedsHuman, NeedsHuman: &Question{Text: "which?"}}, nil
		},
		1: func(*Run) (Outcome, error) { return Outcome{}, &Failure{Category: CategoryForge, Message: "m"} },
	} {
		app := newApp()
		app.Register(Command{Path: "X", Run: end})
		if code := app.Main(context.Background(), []string{"af", "X"}, strings.NewReader(""), brokenWriter{}, io.Discard); code != want {
			t.Errorf("exit %d, want %d", code, want)
		}
	}
}

func TestRun_RawStdinIsGivenAsItIsToTheCommandThatAsksForIt(t *testing.T) {
	var got []byte
	var rec recorder
	app := newApp()
	app.Register(Command{Path: "reader", MaxArgs: NoArgs, RawStdin: true, Run: func(r *Run) (Outcome, error) {
		var err error
		got, err = io.ReadAll(r.Stdin)
		return Outcome{Status: StatusDone, Summary: "read"}, err
	}})
	app.Register(rec.command(Command{Path: "bystander", MaxArgs: NoArgs}))
	payload := "  {\"tool\": \"Bash\"}\n\n\x00 tail  \n"

	r := runWith(t, context.Background(), app, strings.NewReader(payload), "reader")
	if r.code != 0 || string(got) != payload || at(r.env(t), "input", "kind") != "none" {
		t.Errorf("exit %d, read %q, input %v; want the bytes as they came and no input", r.code, got, at(r.env(t), "input"))
	}
	runWith(t, context.Background(), app, strings.NewReader(payload), "bystander")
	if rec.last.Stdin != nil {
		t.Error("a command that did not ask for stdin was given it")
	}
}
