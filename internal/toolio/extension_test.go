package toolio

import (
	"bytes"
	"context"
	"io"
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
