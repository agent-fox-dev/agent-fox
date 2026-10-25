package toolio

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// inputDir is a working directory with a text file, a file with a NUL past
// the part that is looked at, a binary file and a directory, and the
// absolute path it resolves its names against.
func inputDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "bug.md", "the login fails\n")
	testutil.WriteFile(t, dir, "late-nul.txt", strings.Repeat("a", 8192)+"\x00x")
	testutil.WriteFile(t, dir, "early-nul.bin", "text\x00x")
	testutil.WriteFile(t, dir, "adir/keep", "")
	t.Chdir(dir)
	abs, _ := filepath.Abs(".")
	return abs
}

func TestInput_IsClassifiedAndRead(t *testing.T) {
	dir := inputDir(t)
	big := strings.Repeat("a", MaxInputBytes)
	for _, tc := range []struct {
		arg, stdin string
		kind       InputKind
		origin     string
		body       string
		truncated  bool
	}{
		{"-", "multi\nline problem\n", KindStdin, "", "multi\nline problem", false},
		{"bug.md", "", KindFile, filepath.Join(dir, "bug.md"), "the login fails", false},
		{"late-nul.txt", "", KindFile, filepath.Join(dir, "late-nul.txt"), strings.Repeat("a", 8192) + "\x00x", false},
		{"  fix the login bug  ", "", KindText, "", "fix the login bug", false},
		{"missing.md", "", KindText, "", "missing.md", false},
		{big, "", KindText, "", big, false},
		{big + "bcd", "", KindText, "", big + TruncationMarker, true},
	} {
		var rec recorder
		app := newApp()
		app.Register(rec.command(Command{Path: "X", Input: InputRequired}))

		r := runWith(t, context.Background(), app, strings.NewReader(tc.stdin), "X", tc.arg)

		name := tc.arg[:min(len(tc.arg), 20)]
		if r.code != 0 {
			t.Errorf("%q: exit %d, envelope %s", name, r.code, r.stdout)
			continue
		}
		env := r.env(t)
		in := rec.last.Input
		if in.Kind != tc.kind || in.Origin != tc.origin || in.Body != tc.body || in.Truncated != tc.truncated {
			t.Errorf("%q: input kind %s, origin %q, %d bytes, truncated %v; want %s, %q, %d bytes, %v", name, in.Kind, in.Origin, len(in.Body), in.Truncated, tc.kind, tc.origin, len(tc.body), tc.truncated)
		}
		want := map[string]any{"kind": string(tc.kind), "origin": tc.origin, "truncated": tc.truncated}
		if got := env["input"]; !reflect.DeepEqual(got, want) || slices.Contains(warningCodes(env), WarnInputTruncated) != tc.truncated {
			t.Errorf("%q: envelope input %v, warnings %v; want %v", name, got, warningCodes(env), want)
		}
	}
}

func TestInput_IsRefusedBeforeTheCommandRuns(t *testing.T) {
	inputDir(t)
	for _, tc := range []struct {
		name     string
		mode     InputMode
		args     []string
		stdin    io.Reader
		code     int
		category string
		want     string
	}{
		{"no input", InputRequired, nil, nil, 2, CategoryUsage, "no input given"},
		{"blank text", InputRequired, []string{"   "}, nil, 2, CategoryUsage, "empty"},
		{"a blank optional input", InputOptional, []string{"  "}, nil, 2, CategoryUsage, "empty"},
		{"an empty stdin", InputRequired, []string{"-"}, strings.NewReader(""), 2, CategoryUsage, "empty"},
		{"a blank stdin", InputRequired, []string{"-"}, strings.NewReader(" \n\t\n"), 2, CategoryUsage, "empty"},
		{"a stdin that cannot be read", InputRequired, []string{"-"}, &countingReader{}, 1, CategoryIO, "cannot read stdin"},
		{"a binary file", InputRequired, []string{"early-nul.bin"}, nil, 2, CategoryUsage, "not a text file"},
		{"a directory", InputRequired, []string{"adir"}, nil, 2, CategoryUsage, "is a directory"},
		{"an issue for a command without a forge", InputRequired, []string{"https://github.com/o/r/issues/7"}, nil, 2, CategoryUsage, "does not read issues"},
	} {
		var rec recorder
		app := newApp()
		app.Register(rec.command(Command{Path: "X", Input: tc.mode}))
		stdin := tc.stdin
		if stdin == nil {
			stdin = strings.NewReader("")
		}

		r := runWith(t, context.Background(), app, stdin, append([]string{"X"}, tc.args...)...)
		env := r.env(t)

		if r.code != tc.code || at(env, "error", "category") != tc.category || !strings.Contains(at(env, "error", "message").(string), tc.want) || rec.ran != 0 {
			t.Errorf("%s: exit %d, ran %d, envelope %s; want %s naming %q", tc.name, r.code, rec.ran, r.stdout, tc.category, tc.want)
		}
	}
}

func TestInput_AWebURLThatIsNoIssueIsUsageNamingTheAcceptedForms(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer srv.Close()
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITLAB_API_URL", srv.URL)
	for _, arg := range []string{"https://example.com/some/page", "HTTP://github.com/o/r"} {
		var rec recorder
		app := newApp() // a real forge client, whose forge is the counting server
		app.Register(rec.command(Command{Path: "X", Input: InputRequired, UsesForge: true}))

		r := run(t, app, "X", arg)
		env := r.env(t)

		msg := at(env, "error", "message").(string)
		hint := at(env, "error", "fix_hint", "env").(string) + " " + at(env, "error", "fix_hint", "suggest").(string)
		if r.code != 2 || rec.ran != 0 || !strings.Contains(hint, "GITHUB_API_URL") || !strings.Contains(hint, "GITLAB_API_URL") {
			t.Errorf("%s: exit %d, ran %d, envelope %s", arg, r.code, rec.ran, r.stdout)
		}
		for _, form := range []string{"github.com/<owner>/<repo>/issues/<number>", "/pull/<number>", "/-/issues/<number>", "/-/merge_requests/<number>"} {
			if !strings.Contains(msg, form) {
				t.Errorf("%s: message %q does not name %s", arg, msg, form)
			}
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("the forge saw %d requests, want none", n)
	}
}

func TestInput_AnOptionalOneMayBeLeftOut(t *testing.T) {
	var rec recorder
	app := newApp()
	app.Register(rec.command(Command{Path: "X", Input: InputOptional}))

	r := run(t, app, "X")

	if r.code != 0 || rec.last.Input.Kind != KindNone || at(r.env(t), "input", "kind") != "none" {
		t.Errorf("exit %d, input %+v", r.code, rec.last.Input)
	}
}

func TestInput_ACommandWithoutOneReadsNoArgument(t *testing.T) {
	inputDir(t)
	var rec recorder
	app := newApp()
	app.Register(rec.command(Command{Path: "X"}))
	stdin := &countingReader{}
	for _, arg := range []string{"-", "early-nul.bin", "https://example.com/page"} {
		var out bytes.Buffer
		code := app.Main(context.Background(), []string{"af", "X", arg}, stdin, &out, io.Discard)
		if code != 0 || !slices.Equal(rec.last.Args, []string{arg}) || rec.last.Input.Kind != KindNone {
			t.Errorf("%q: exit %d, args %q, input %v", arg, code, rec.last.Args, rec.last.Input.Kind)
		}
	}
	if stdin.reads != 0 {
		t.Errorf("stdin was read %d times", stdin.reads)
	}
}

func TestInput_ReadingStdinGivesUpWhenTheRunIsCancelled(t *testing.T) {
	stdin, w := io.Pipe() // a terminal nobody types into
	defer w.Close()
	var rec recorder
	app := newApp()
	app.Register(rec.command(Command{Path: "X", Input: InputRequired}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := runWith(t, ctx, app, stdin, "X", "-")

	if r.code != 1 || at(r.env(t), "error", "category") != CategoryCancelled || rec.ran != 0 {
		t.Errorf("exit %d, ran %d, envelope %s; want cancelled", r.code, rec.ran, r.stdout)
	}
}

func TestInput_AFileThatCannotBeReadIsAnIOFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not stop root")
	}
	path := testutil.WriteFile(t, t.TempDir(), "secret.md", "x")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	var rec recorder
	app := newApp()
	app.Register(rec.command(Command{Path: "X", Input: InputRequired}))

	if r := run(t, app, "X", path); r.code != 1 || at(r.env(t), "error", "category") != CategoryIO || rec.ran != 0 {
		t.Errorf("exit %d, ran %d, envelope %s; want an io failure", r.code, rec.ran, r.stdout)
	}
}

func TestTruncate_CutsAtTheLastRuneBoundaryAndMarksTheCut(t *testing.T) {
	a := func(n int) string { return strings.Repeat("a", n) }
	for _, tc := range []struct {
		name, in, want string
		cut            bool
	}{
		{"a body below the limit", "short", "short", false},
		{"a body at the limit", a(MaxInputBytes), a(MaxInputBytes), false},
		{"a body over the limit", a(MaxInputBytes) + "bcd", a(MaxInputBytes) + TruncationMarker, true},
		{"a rune across the limit", a(MaxInputBytes-1) + "é" + "tail", a(MaxInputBytes-1) + TruncationMarker, true},
	} {
		if got, cut := Truncate(tc.in); got != tc.want || cut != tc.cut {
			t.Errorf("%s: %d bytes, cut %v; want %d bytes, cut %v", tc.name, len(got), cut, len(tc.want), tc.cut)
		}
	}
	for _, r := range []string{"é", "€", "😀"} {
		for pad := range 4 {
			in := a(MaxInputBytes-4+pad) + strings.Repeat(r, 3)
			got, cut := Truncate(in)
			body := strings.TrimSuffix(got, TruncationMarker)
			if !cut || !utf8.ValidString(got) || len(body) > MaxInputBytes || !strings.HasPrefix(in, body) || len(body) < MaxInputBytes-len(r)+1 {
				t.Errorf("%q after %d bytes: cut %v, a body of %d bytes", r, MaxInputBytes-4+pad, cut, len(body))
			}
		}
	}
}
