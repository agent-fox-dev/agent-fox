package toolio

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// invokeWithStdin is invoke with stdin.
func invokeWithStdin(t *testing.T, app *App, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = app.Main(context.Background(), append([]string{"af"}, args...), strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

// TestRawStdin guards 06-REQ-10.5: a command that asks for it reads stdin as
// it is, with no positional input; any other command is not given stdin.
func TestRawStdin(t *testing.T) {
	var got []byte
	var gotNil bool
	reader := Command{Path: "reader", MaxArgs: NoArgs, RawStdin: true, Run: func(r *Run) (Outcome, error) {
		var err error
		got, err = io.ReadAll(r.Stdin)
		return Outcome{Status: StatusDone, Summary: "read"}, err
	}}
	bystander := Command{Path: "bystander", MaxArgs: NoArgs, Run: func(r *Run) (Outcome, error) {
		gotNil = r.Stdin == nil
		return Outcome{Status: StatusDone, Summary: "ok"}, nil
	}}
	app := newTestApp()
	app.Register(reader)
	app.Register(bystander)

	// Not trimmed, not cut, not classified: the bytes as they come.
	payload := "  {\"tool\": \"Bash\"}\n\n\x00 tail  \n"
	code, stdout, _ := invokeWithStdin(t, app, payload, "reader")
	if code != 0 || string(got) != payload {
		t.Errorf("exit code %d, read %q; want 0 and %q", code, got, payload)
	}
	if kind := at(decode(t, stdout), "input", "kind"); kind != "none" {
		t.Errorf("input.kind = %v, want none: raw stdin is no input", kind)
	}

	invokeWithStdin(t, app, payload, "bystander")
	if !gotNil {
		t.Error("a command that did not ask for stdin was given it")
	}
}

// TestDocumentReplacesTheEnvelopeOnStdout guards 06-REQ-10.5: a command that
// returns a document prints it, and nothing else, to stdout and exits 0; the
// envelope is still made and still goes to --output.
func TestDocumentReplacesTheEnvelopeOnStdout(t *testing.T) {
	app := newTestApp()
	app.Register(Command{Path: "hook", MaxArgs: NoArgs, Run: func(*Run) (Outcome, error) {
		return Outcome{Status: StatusDone, Summary: "denied", Result: map[string]string{"decision": "deny"}, Document: []byte("{}\n")}, nil
	}})
	copyTo := filepath.Join(t.TempDir(), "envelope.json")

	code, stdout, stderr := invoke(t, app, "hook", "--output", copyTo)

	if code != 0 || stdout != "{}\n" || stderr != "" {
		t.Errorf("exit code %d, stdout %q, stderr %q; want 0, the document and nothing", code, stdout, stderr)
	}
	data, err := os.ReadFile(copyTo)
	if err != nil {
		t.Fatal(err)
	}
	env := decode(t, string(data))
	if env["status"] != "done" || env["tool"] != "hook" || at(env, "result", "decision") != "deny" {
		t.Errorf("the --output copy is %v, want the envelope of the run", env)
	}
}

// TestDocumentOnlyWithDone guards 06-REQ-10.5: a document that does not go
// with the status done is the command's bug, and the envelope says so.
func TestDocumentOnlyWithDone(t *testing.T) {
	outcomes := map[Status]Outcome{
		StatusUnverified: {Status: StatusUnverified, Summary: "x", Document: []byte("{}")},
		StatusNeedsHuman: {Status: StatusNeedsHuman, Summary: "x", Document: []byte("{}"), NeedsHuman: &Question{Text: "?"}},
	}
	for status, outcome := range outcomes {
		app := newTestApp()
		app.Register(returning("hook", outcome, nil))

		code, stdout, _ := invoke(t, app, "hook")

		env := decode(t, stdout)
		if code != 1 || env["status"] != "failed" || at(env, "error", "category") != "internal" {
			t.Errorf("status %s: exit code %d, envelope %v; want a failed/internal envelope", status, code, env)
		}
	}
}

// TestFailureDropsTheDocument guards 06-REQ-10.5: a command that fails after
// all prints the envelope, not a document it prepared.
func TestFailureDropsTheDocument(t *testing.T) {
	app := newTestApp()
	app.Register(Command{Path: "hook", Run: func(*Run) (Outcome, error) {
		return Outcome{Status: StatusDone, Document: []byte("{}")}, &Failure{Category: CategoryIO, Message: "broken"}
	}})

	code, stdout, _ := invoke(t, app, "hook")

	if env := decode(t, stdout); code != 1 || env["status"] != "failed" {
		t.Errorf("exit code %d, envelope %v; want a failed envelope", code, env)
	}
}

// TestFailureCategoriesOfRing1_TS_06_74 verifies the shell's part of TS-06-74:
// config, restore and git are failure categories like io: status failed,
// exit 1, the category and message as given.
// Verifies: 06-REQ-10.6
func TestFailureCategoriesOfRing1_TS_06_74(t *testing.T) {
	for _, category := range []string{CategoryConfig, CategoryRestore, CategoryGit, CategoryIO} {
		app := newTestApp()
		app.Register(returning("X", Outcome{}, &Failure{Category: category, Message: "it broke: " + category}))

		code, stdout, _ := invoke(t, app, "X")

		env := decode(t, stdout)
		if code != 1 || env["status"] != "failed" || at(env, "error", "category") != category || at(env, "error", "message") != "it broke: "+category {
			t.Errorf("category %s: exit code %d, envelope %v; want failed, exit 1 and the category and message given", category, code, env)
		}
	}
	if CategoryConfig != "config" || CategoryRestore != "restore" || CategoryGit != "git" {
		t.Errorf("the categories are %q, %q, %q; want config, restore, git", CategoryConfig, CategoryRestore, CategoryGit)
	}
}
