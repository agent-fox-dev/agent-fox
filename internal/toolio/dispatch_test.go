package toolio

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
)

// recorder is a test command that records what it was run with.
type recorder struct {
	ran     int
	args    []string
	dir     string
	context []string
}

func (r *recorder) command(path string, mutate ...func(*Command)) Command {
	c := Command{Path: path, Run: func(run *Run) (Outcome, error) {
		r.ran++
		r.args = run.Args
		r.dir = run.Dir
		r.context = run.Context
		return Outcome{Status: StatusDone, Summary: "ok"}, nil
	}}
	for _, m := range mutate {
		m(&c)
	}
	return c
}

func withDir(c *Command)     { c.Dir = true }
func withContext(c *Command) { c.Context = true }

// TestDispatchLongestPath_TS_05_1 verifies TS-05-1:
// The shell dispatches to the command with the longest matching path.
// Verifies: 05-REQ-1.1
func TestDispatchLongestPath_TS_05_1(t *testing.T) {
	var gate, gateRun recorder
	app := newTestApp()
	app.Register(gate.command("gate"))
	app.Register(gateRun.command("gate run", withDir))
	dir := t.TempDir()

	code, stdout, _ := invoke(t, app, "gate", "run", "--dir", dir)
	if code != 0 || decode(t, stdout)["tool"] != "gate run" {
		t.Errorf("got exit code %d and tool %v, want 0 and gate run", code, decode(t, stdout)["tool"])
	}
	if gateRun.ran != 1 || gate.ran != 0 {
		t.Errorf("gate run ran %d times and gate %d, want 1 and 0", gateRun.ran, gate.ran)
	}
	if gateRun.dir != dir {
		t.Errorf("the remaining arguments did not reach the flag set: dir = %q, want %q", gateRun.dir, dir)
	}

	_, stdout, _ = invoke(t, app, "gate")
	if decode(t, stdout)["tool"] != "gate" || gate.ran != 1 {
		t.Errorf("tool = %v and gate ran %d times, want gate and 1", decode(t, stdout)["tool"], gate.ran)
	}
}

// TestUnknownCommandListsCommands_TS_05_2 verifies TS-05-2:
// No arguments or an unknown command gives a usage envelope listing the
// commands.
// Verifies: 05-REQ-1.2
func TestUnknownCommandListsCommands_TS_05_2(t *testing.T) {
	var r recorder
	app := newTestApp()
	app.Register(r.command("alpha"))
	app.Register(r.command("beta gamma"))

	for _, args := range [][]string{{}, {"nosuch"}} {
		code, stdout, stderr := invoke(t, app, args...)
		env := decode(t, stdout)
		if code != 2 || env["status"] != "usage" || env["tool"] != "af" || at(env, "error", "category") != "usage" {
			t.Errorf("%v: got exit code %d, status %v, tool %v, category %v", args, code, env["status"], env["tool"], at(env, "error", "category"))
		}
		msg, _ := at(env, "error", "message").(string)
		if !strings.Contains(msg, "alpha") || !strings.Contains(msg, "beta gamma") {
			t.Errorf("%v: error.message = %q, want it to list alpha and beta gamma", args, msg)
		}
		if stderr != "" {
			t.Errorf("%v: stderr = %q, want nothing", args, stderr)
		}
	}
	if r.ran != 0 {
		t.Errorf("a command ran %d times for a usage error", r.ran)
	}
}

// TestFlagsBeforeOrAfterPositional_TS_05_3 verifies TS-05-3:
// Flags before or after the positional argument parse identically and --
// ends flags.
// Verifies: 05-REQ-1.3
func TestFlagsBeforeOrAfterPositional_TS_05_3(t *testing.T) {
	dir := t.TempDir()
	var r recorder
	app := newTestApp()
	app.Register(r.command("X", withDir))

	invoke(t, app, "X", "./bug.md", "--dir", dir)
	after := recorder{args: r.args, dir: r.dir}
	invoke(t, app, "X", "--dir", dir, "./bug.md")
	before := recorder{args: r.args, dir: r.dir}
	if !reflect.DeepEqual(after, before) || !reflect.DeepEqual(after.args, []string{"./bug.md"}) || after.dir != dir {
		t.Errorf("flags before the positional gave %+v, after it %+v", before, after)
	}

	invoke(t, app, "X", "--dir="+dir, "./bug.md")
	if r.dir != dir || !reflect.DeepEqual(r.args, []string{"./bug.md"}) {
		t.Errorf("--dir=value gave dir %q and args %v", r.dir, r.args)
	}

	invoke(t, app, "X", "--", "--dir")
	wd, _ := filepath.Abs(".")
	if !reflect.DeepEqual(r.args, []string{"--dir"}) || r.dir != wd {
		t.Errorf("after --, args = %v and dir = %q; want [--dir] and the default %q", r.args, r.dir, wd)
	}
}

// TestFlagValueMayLookLikeAFlag guards the interspersed parse behind
// TS-05-3: the word after a value flag is its value, whatever it looks like.
func TestFlagValueMayLookLikeAFlag(t *testing.T) {
	var r recorder
	app := newTestApp()
	app.Register(r.command("X", withContext))

	code, _, _ := invoke(t, app, "X", "text", "--context", "-x", "--context", "--")
	if code != 0 || !reflect.DeepEqual(r.context, []string{"-x", "--"}) || !reflect.DeepEqual(r.args, []string{"text"}) {
		t.Errorf("got exit code %d, context %q, args %q", code, r.context, r.args)
	}
}

// TestBadFlagOrExtraPositional_TS_05_4 verifies TS-05-4:
// A bad flag or an extra positional gives a usage envelope and nothing on
// stderr.
// Verifies: 05-REQ-1.4
func TestBadFlagOrExtraPositional_TS_05_4(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown flag", []string{"X", "--bogus", "a"}, "--bogus"},
		{"extra positional", []string{"X", "a", "b"}, `"b"`},
		{"flag without a value", []string{"X", "a", "--dir"}, "--dir"},
		{"extra positional for a command without any", []string{"N", "a"}, `"a"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r recorder
			app := newTestApp()
			app.Register(r.command("X", withDir))
			app.Register(r.command("N", func(c *Command) { c.MaxArgs = NoArgs }))

			code, stdout, stderr := invoke(t, app, tc.args...)
			env := decode(t, stdout)

			if code != 2 || env["status"] != "usage" {
				t.Errorf("got exit code %d and status %v, want 2 and usage", code, env["status"])
			}
			if msg, _ := at(env, "error", "message").(string); !strings.Contains(msg, tc.want) {
				t.Errorf("error.message = %q, want it to name %s", msg, tc.want)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want nothing", stderr)
			}
			if r.ran != 0 {
				t.Error("the command ran")
			}
		})
	}
}

// TestHelp_TS_05_5 verifies TS-05-5:
// -h, --help and help print plain-text help with exit 0.
// Verifies: 05-REQ-1.5
func TestHelp_TS_05_5(t *testing.T) {
	var r recorder
	app := newTestApp()
	app.Register(r.command("alpha", withDir, func(c *Command) {
		c.Summary = "does the alpha thing"
		c.Usage = "af alpha [flags] <issue|file|text>"
	}))
	app.Register(r.command("beta"))

	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"-h"}, []string{"alpha", "beta"}},
		{[]string{"--help"}, []string{"alpha", "beta"}},
		{[]string{"help"}, []string{"alpha", "beta", "does the alpha thing"}},
		{[]string{"help", "alpha"}, []string{"af alpha [flags] <issue|file|text>", "--dir"}},
		{[]string{"alpha", "--help"}, []string{"af alpha [flags] <issue|file|text>", "--dir"}},
		{[]string{"alpha", "text", "-h", "--dir"}, []string{"--dir"}},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			code, stdout, stderr := invoke(t, app, tc.args...)
			if code != 0 {
				t.Errorf("exit code = %d, want 0", code)
			}
			if json.Valid([]byte(stdout)) {
				t.Errorf("help is JSON:\n%s", stdout)
			}
			for _, want := range tc.want {
				if !strings.Contains(stdout, want) {
					t.Errorf("help does not contain %q:\n%s", want, stdout)
				}
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want nothing", stderr)
			}
		})
	}
	if r.ran != 0 {
		t.Errorf("a command ran %d times for help", r.ran)
	}

	code, stdout, _ := invoke(t, app, "help", "nosuch")
	if code != 2 || decode(t, stdout)["status"] != "usage" {
		t.Errorf("help for an unknown command: exit code %d, status %v; want 2, usage", code, decode(t, stdout)["status"])
	}
}

// TestVersion_TS_05_6 verifies TS-05-6:
// --version reports ldflags values, else build info, else dev.
// Verifies: 05-REQ-1.6
func TestVersion_TS_05_6(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v9.9.9"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "deadbeef"},
			{Key: "vcs.time", Value: "2026-01-02T03:04:05Z"},
		},
	}
	cases := []struct {
		name string
		app  *App
		want [4]string // version, commit, build_time and nothing else
	}{
		{
			"ldflags",
			&App{Version: "v1.2.3", Commit: "abc123", BuildTime: "2026-10-20T00:00:00Z"},
			[4]string{"v1.2.3", "abc123", "2026-10-20T00:00:00Z"},
		},
		{
			"build info",
			&App{BuildInfo: func() (*debug.BuildInfo, bool) { return info, true }},
			[4]string{"v9.9.9", "deadbeef", "2026-01-02T03:04:05Z"},
		},
		{
			"a development build",
			&App{BuildInfo: func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true
			}},
			[4]string{"dev", "", ""},
		},
		{
			"no build info",
			&App{BuildInfo: func() (*debug.BuildInfo, bool) { return nil, false }},
			[4]string{"dev", "", ""},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := invoke(t, tc.app, "--version")
			if code != 0 || stderr != "" {
				t.Errorf("exit code %d, stderr %q; want 0 and nothing", code, stderr)
			}
			doc := decode(t, stdout)
			want := map[string]any{
				"name": "af", "version": tc.want[0], "commit": tc.want[1], "build_time": tc.want[2],
				"go_version": runtime.Version(),
			}
			if !reflect.DeepEqual(doc, want) {
				t.Errorf("version document = %v, want %v", doc, want)
			}
			if got := topLevelKeys(t, stdout); !slices.Equal(got, []string{"name", "version", "commit", "build_time", "go_version"}) {
				t.Errorf("keys = %v, want name, version, commit, build_time, go_version", got)
			}
		})
	}
}

// TestResumeKeepsPriorContext_TS_05_30 verifies TS-05-30:
// resume.argv is the received invocation with argv[0] replaced by af and
// prior --context kept.
// Verifies: 05-REQ-5.2
func TestResumeKeepsPriorContext_TS_05_30(t *testing.T) {
	var r recorder
	app := newTestApp()
	app.Register(Command{Path: "X", Context: true, Run: func(run *Run) (Outcome, error) {
		r.context = run.Context
		return Outcome{Status: StatusNeedsHuman, NeedsHuman: &Question{Text: "which?", Options: []string{"x", "y"}}}, nil
	}})

	var out bytes.Buffer
	args := []string{"/tmp/bin/whatever", "X", "text", "--context", "A"}
	if code := app.Main(context.Background(), args, strings.NewReader(""), &out, &bytes.Buffer{}); code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
	env := decode(t, out.String())
	wantArgv := []any{"af", "X", "text", "--context", "A"}
	if got := at(env, "needs_human", "resume", "argv"); !reflect.DeepEqual(got, wantArgv) {
		t.Errorf("resume.argv = %v, want %v", got, wantArgv)
	}
	if got := at(env, "needs_human", "resume", "flag"); got != "--context" {
		t.Errorf("resume.flag = %v, want --context", got)
	}

	// Running the resume with the answer appended gives every answer so far.
	var resume []string
	for _, a := range at(env, "needs_human", "resume", "argv").([]any) {
		resume = append(resume, a.(string))
	}
	out.Reset()
	app.Main(context.Background(), append(resume, "--context", "B"), strings.NewReader(""), &out, &bytes.Buffer{})
	if !reflect.DeepEqual(r.context, []string{"A", "B"}) {
		t.Errorf("Run.Context after the resume = %q, want [A B]", r.context)
	}
}

// TestContextVerbatimInOrder_TS_05_31 verifies TS-05-31:
// --context is repeatable and reaches the command verbatim in order.
// Verifies: 05-REQ-5.3
func TestContextVerbatimInOrder_TS_05_31(t *testing.T) {
	var r recorder
	app := newTestApp()
	app.Register(r.command("X", withContext))
	app.Register(r.command("plain"))

	invoke(t, app, "X", "--context", " B ", "--context", "x y", "--context", "B")
	if want := []string{" B ", "x y", "B"}; !reflect.DeepEqual(r.context, want) {
		t.Errorf("Run.Context = %q, want %q", r.context, want)
	}

	ran := r.ran
	code, stdout, _ := invoke(t, app, "plain", "--context", "A")
	if code != 2 || decode(t, stdout)["status"] != "usage" || r.ran != ran {
		t.Errorf("a command that did not opt in: exit code %d, status %v, ran %v", code, decode(t, stdout)["status"], r.ran != ran)
	}
}

// TestEmptyContextRefused_TS_05_32 verifies TS-05-32:
// An empty --context value is refused before the command runs.
// Verifies: 05-REQ-5.4
func TestEmptyContextRefused_TS_05_32(t *testing.T) {
	var control recorder
	ok := newTestApp()
	ok.Register(control.command("X", withContext))
	if code, _, _ := invoke(t, ok, "X", "--context", "A"); code != 0 || control.ran != 1 {
		t.Fatalf("a non-empty --context: exit code %d, ran %d; want 0, 1", code, control.ran)
	}

	for _, args := range [][]string{{"X", "--context", ""}, {"X", "--context="}, {"X", "--context", "A", "--context", ""}} {
		var r recorder
		app := newTestApp()
		app.Register(r.command("X", withContext))

		code, stdout, _ := invoke(t, app, args...)
		env := decode(t, stdout)
		if code != 2 || env["status"] != "usage" || r.ran != 0 {
			t.Errorf("%q: exit code %d, status %v, ran %d; want 2, usage, 0", args, code, env["status"], r.ran)
		}
		if msg, _ := at(env, "error", "message").(string); !strings.Contains(msg, "empty") {
			t.Errorf("%q: error.message = %q, want it to say the value is empty", args, msg)
		}
	}
}

// TestInconsistentOutcomeIsABug_TS_05_33 verifies TS-05-33:
// An inconsistent outcome is a command bug reported as failed/internal.
// Verifies: 05-REQ-5.5
func TestInconsistentOutcomeIsABug_TS_05_33(t *testing.T) {
	many := make([]string, 27)
	for i := range many {
		many[i] = "option"
	}
	bad := map[string]Outcome{
		"needs_human without a question": {Status: StatusNeedsHuman},
		"needs_human with a blank text":  {Status: StatusNeedsHuman, NeedsHuman: &Question{Text: "  "}},
		"done with a question":           {Status: StatusDone, NeedsHuman: &Question{Text: "which?"}},
		"unverified with a question":     {Status: StatusUnverified, NeedsHuman: &Question{Text: "which?"}},
		"27 options":                     {Status: StatusNeedsHuman, NeedsHuman: &Question{Text: "which?", Options: many}},
		"no status":                      {},
		"failed as an outcome":           {Status: StatusFailed},
	}
	for name, out := range bad {
		t.Run(name, func(t *testing.T) {
			app := newTestApp()
			app.Register(returning("X", out, nil))

			code, stdout, _ := invoke(t, app, "X")
			env := decode(t, stdout)

			if code != 1 || env["status"] != "failed" || at(env, "error", "category") != CategoryInternal || env["needs_human"] != nil {
				t.Errorf("got exit code %d, status %v, category %v, needs_human %v; want 1, failed, internal, null",
					code, env["status"], at(env, "error", "category"), env["needs_human"])
			}
		})
	}
}
