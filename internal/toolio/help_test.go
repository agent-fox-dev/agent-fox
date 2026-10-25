package toolio

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"
)

func TestHelp_IsPlainTextOnStdoutAndRunsNothing(t *testing.T) {
	var rec recorder
	app := newApp()
	app.Register(rec.command(Command{Path: "alpha", Dir: true, Summary: "does the alpha thing", Usage: "af alpha [flags] <issue|file|text>"}))
	app.Register(rec.command(Command{Path: "beta", Timeout: true, DefaultTimeout: time.Hour, Context: true}))
	app.Register(rec.command(Command{Path: "inbox", Hidden: true, Summary: "run in a box"}))
	for _, tc := range []struct {
		args       []string
		want, lack []string
	}{
		{[]string{"-h"}, []string{"Commands:", "alpha  does the alpha thing", "beta"}, []string{"inbox"}},
		{[]string{"--help"}, []string{"alpha", "beta"}, nil},
		{[]string{"help"}, []string{"alpha", "beta", "af help <command>"}, []string{"inbox"}},
		{[]string{"help", "alpha"}, []string{"Usage: af alpha [flags] <issue|file|text>", "does the alpha thing", "--dir string", "(default .)"}, nil},
		{[]string{"alpha", "--help"}, []string{"Usage: af alpha [flags] <issue|file|text>"}, nil},
		{[]string{"alpha", "text", "-h", "--dir"}, []string{"--dir"}, nil},
		{[]string{"beta", "-help"}, []string{"Usage: af beta [flags]", "--timeout duration", "(default 1h0m0s)", "--context"}, []string{"(default )"}},
		{[]string{"help", "inbox"}, []string{"run in a box"}, nil},
		// "--help" after a flag with its value is a request.
		{[]string{"beta", "--context", "answer", "--help"}, []string{"Usage: af beta"}, nil},
	} {
		r := run(t, app, tc.args...)
		if r.code != 0 || r.stderr != "" || json.Valid([]byte(r.stdout)) {
			t.Errorf("%q: exit %d, stderr %q, stdout %q; want plain text and exit 0", tc.args, r.code, r.stderr, r.stdout)
		}
		for _, w := range tc.want {
			if !strings.Contains(r.stdout, w) {
				t.Errorf("%q: the help lacks %q:\n%s", tc.args, w, r.stdout)
			}
		}
		for _, l := range tc.lack {
			if strings.Contains(r.stdout, l) {
				t.Errorf("%q: the help has %q:\n%s", tc.args, l, r.stdout)
			}
		}
	}
	if rec.ran != 0 {
		t.Errorf("a command ran %d times for help", rec.ran)
	}
	if r := run(t, newApp(), "help"); !strings.Contains(r.stdout, "(none registered)") {
		t.Errorf("the help of an app without commands:\n%s", r.stdout)
	}
}

func TestHelp_TheValueOfAFlagIsNoRequest(t *testing.T) {
	var rec recorder
	app := newApp()
	app.Register(rec.command(Command{Path: "X", Context: true}))

	r := run(t, app, "X", "text", "--context", "--help")

	if r.code != 0 || rec.ran != 1 || !json.Valid([]byte(r.stdout)) || rec.last.Context[0] != "--help" {
		t.Errorf("exit %d, ran %d, stdout %q; want the command run with the context --help", r.code, rec.ran, r.stdout)
	}
	if r := run(t, app, "X", "--", "--help"); rec.ran != 2 || rec.last.Args[0] != "--help" {
		t.Errorf("after --, --help is an argument: %s", r.stdout)
	}
}

func TestHelp_ForAnUnknownCommandIsUsage(t *testing.T) {
	r := run(t, newApp(), "help", "nosuch")
	if r.code != 2 || r.env(t)["status"] != "usage" {
		t.Errorf("exit %d, stdout %s; want a usage envelope", r.code, r.stdout)
	}
}

// countingReader is a stdin that counts its reads and has nothing to give.
type countingReader struct{ reads int }

func (c *countingReader) Read([]byte) (int, error) {
	c.reads++
	return 0, os.ErrClosed
}

func TestHelpAndVersion_ReadNoInputBuildNoForgeAndCreateNothing(t *testing.T) {
	dir := t.TempDir()
	url := "https://github.com/o/r/issues/1"
	for _, args := range [][]string{
		{"X", "--version", "-", "--dir", dir},
		{"X", url, "--version"},
		{"X", url, "--help", "--dir", dir},
		{"X", "-h", "--dry-run"},
	} {
		ran, forges := 0, 0
		stdin := &countingReader{}
		app := newApp()
		app.NewForge = func(issuex.Options) (issuex.Client, error) {
			forges++
			return issuex.NewNoOp(), nil
		}
		app.Register(Command{Path: "X", Input: InputRequired, UsesForge: true, UsesRunDir: true, DryRun: true, Run: func(*Run) (Outcome, error) {
			ran++
			return Outcome{Status: StatusDone}, nil
		}})
		var out, errb bytes.Buffer

		code := app.Main(context.Background(), append([]string{"af"}, args...), stdin, &out, &errb)

		if code != 0 || ran != 0 || forges != 0 || stdin.reads != 0 || errb.Len() != 0 || out.Len() == 0 {
			t.Errorf("%q: exit %d, ran %d, forges %d, stdin reads %d, stderr %q", args, code, ran, forges, stdin.reads, errb.String())
		}
		if _, err := os.Stat(agentFoxDir(dir)); err == nil {
			t.Errorf("%q: a run directory was created", args)
		}
	}
}
