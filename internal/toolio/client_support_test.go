package toolio

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestARunMaySetTheInputItReportsInTheEnvelope_TS_16_49 verifies, for the shell, what TS-16-49
// relies on: a command that reads its own input says what it was, and the envelope reports it.
// Verifies: 16-REQ-7.5
func TestARunMaySetTheInputItReportsInTheEnvelope_TS_16_49(t *testing.T) {
	app := newTestApp()
	app.Register(Command{Path: "own", MaxArgs: NoArgs, Run: func(r *Run) (Outcome, error) {
		r.SetInput(KindFile, "/abs/notes.txt", true)
		return Outcome{Status: StatusDone, Summary: "ok"}, nil
	}})
	app.Register(Command{Path: "none", MaxArgs: NoArgs, Run: func(r *Run) (Outcome, error) { return Outcome{Status: StatusDone, Summary: "ok"}, nil }})
	_, stdout, _ := invoke(t, app, "own")
	env := decode(t, stdout)
	if at(env, "input", "kind") != "file" || at(env, "input", "origin") != "/abs/notes.txt" || at(env, "input", "truncated") != true {
		t.Errorf("input = %v, want the file the command set", env["input"])
	}
	_, stdout, _ = invoke(t, app, "none")
	if at(decode(t, stdout), "input", "kind") != "none" {
		t.Errorf("a command that sets no input reports %v, want none", at(decode(t, stdout), "input"))
	}
}

// TestAValueARunRedactsIsInNoOutputOfTheInvocation_TS_16_27 verifies, for the shell, what TS-16-27
// relies on: a secret that a command registers is in neither the envelope on stdout, nor the events
// of the sink and the run directory, wherever the command put it: in a
// warning, in a result, in an event of its own or in its error. A value that is too short to be
// a secret is left alone.
// Verifies: 16-REQ-4.6
func TestAValueARunRedactsIsInNoOutputOfTheInvocation_TS_16_27(t *testing.T) {
	const secret = `sup"er\secret-value<&>`
	for name, fail := range map[string]bool{"done": false, "failed": true} {
		app := newTestApp()
		app.Register(Command{Path: "leak", MaxArgs: NoArgs, UsesRunDir: true, Run: func(r *Run) (Outcome, error) {
			r.Redact(secret, "short")
			r.Emit("note", map[string]string{"said": "the secret is " + secret, "short": "short"})
			r.Warn("leaked", "a warning with "+secret)
			if fail {
				return Outcome{}, &Failure{Category: CategoryInternal, Message: "it failed with " + secret}
			}
			return Outcome{Status: StatusDone, Summary: "ok " + secret, Result: map[string]string{"echo": secret, "short": "short"}}, nil
		}})
		dir := t.TempDir()
		var sunk bytes.Buffer
		app.EventSink = func(line []byte) { sunk.Write(line) }
		_, stdout, stderr := invoke(t, app, "leak", "--dir", dir)
		env := decode(t, stdout)
		if strings.Contains(stdout, "secret-value") || strings.Contains(stderr, "secret-value") {
			t.Errorf("%s: the output holds the secret:\n%s%s", name, stdout, stderr)
		}
		if strings.Contains(sunk.String(), "secret-value") {
			t.Errorf("%s: the events of the sink hold the secret:\n%s", name, sunk.String())
		}
		runs, _ := filepath.Glob(filepath.Join(dir, ".agent-fox", "run", "*", "*"))
		for _, f := range runs {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "secret-value") || strings.Contains(string(data), `sup\"er`) {
				t.Errorf("%s: %s holds the secret:\n%s", name, f, data)
			}
		}
		if name == "done" && at(env, "result", "short") != "short" {
			t.Errorf("a value shorter than a secret is redacted: result = %v", env["result"])
		}
	}
}
