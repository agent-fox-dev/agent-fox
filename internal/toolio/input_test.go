package toolio

import (
	"bytes"
	"context"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"
)

// inputRecorder is a test command that takes an input and records it.
type inputRecorder struct {
	ran   int
	input Input
	args  []string
}

func (r *inputRecorder) command(mode InputMode) Command {
	return Command{Path: "X", Input: mode, Run: func(run *Run) (Outcome, error) {
		r.ran++
		r.input = run.Input
		r.args = run.Args
		return Outcome{Status: StatusDone, Summary: "ok"}, nil
	}}
}

// invokeStdin is invoke with the given stdin.
func invokeStdin(t *testing.T, app *App, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = app.Main(context.Background(), append([]string{"af"}, args...), strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

// warningCodes lists the codes of an envelope's warnings.
func warningCodes(env map[string]any) []string {
	var codes []string
	list, _ := env["warnings"].([]any)
	for _, w := range list {
		codes = append(codes, w.(map[string]any)["code"].(string))
	}
	return codes
}

// TestStdinIsReadUntilEOF_TS_05_34 verifies TS-05-34:
// The argument - reads stdin until EOF.
// Verifies: 05-REQ-6.1
func TestStdinIsReadUntilEOF_TS_05_34(t *testing.T) {
	var r inputRecorder
	app := newTestApp()
	app.Register(r.command(InputRequired))

	code, stdout, _ := invokeStdin(t, app, "multi\nline problem\n", "X", "-")
	env := decode(t, stdout)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, stdout)
	}
	if at(env, "input", "kind") != "stdin" || at(env, "input", "origin") != "" {
		t.Errorf("input = %v, want kind stdin and origin \"\"", env["input"])
	}
	if r.input.Body != "multi\nline problem" || r.input.Kind != KindStdin {
		t.Errorf("recorded input = %+v, want the trimmed stdin", r.input)
	}
}

// TestHTTPURLThatIsNotAnIssue_TS_05_36 verifies TS-05-36:
// An http(s) URL that is not an issue URL is a usage error with a
// self-hosting hint.
// Verifies: 05-REQ-6.3
func TestHTTPURLThatIsNotAnIssue_TS_05_36(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer srv.Close()
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITLAB_API_URL", srv.URL)

	for _, arg := range []string{"https://example.com/some/page", "http://github.com/o/r"} {
		t.Run(arg, func(t *testing.T) {
			var r inputRecorder
			app := newTestApp()
			app.Register(r.command(InputRequired))

			code, stdout, _ := invoke(t, app, "X", arg)
			env := decode(t, stdout)

			if code != 2 || env["status"] != "usage" || r.ran != 0 {
				t.Errorf("exit code %d, status %v, ran %d; want 2, usage, 0", code, env["status"], r.ran)
			}
			msg, _ := at(env, "error", "message").(string)
			for _, want := range []string{"github.com/<owner>/<repo>/issues/<number>", "/pull/<number>", "/-/issues/<number>", "/-/merge_requests/<number>"} {
				if !strings.Contains(msg, want) {
					t.Errorf("error.message = %q, want it to name the form %s", msg, want)
				}
			}
			hint, _ := at(env, "error", "fix_hint").(map[string]any)
			text := ""
			for _, v := range hint {
				text += v.(string) + " "
			}
			if !strings.Contains(text, "GITHUB_API_URL") || !strings.Contains(text, "GITLAB_API_URL") {
				t.Errorf("fix_hint = %v, want it to name GITHUB_API_URL and GITLAB_API_URL", hint)
			}
		})
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("the forge saw %d requests, want none", n)
	}
}

// TestFileIsReadWithAbsoluteOrigin_TS_05_37 verifies TS-05-37:
// An existing file is read with kind file and an absolute origin.
// Verifies: 05-REQ-6.4
func TestFileIsReadWithAbsoluteOrigin_TS_05_37(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bug.md"), []byte("the login fails\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	var r inputRecorder
	app := newTestApp()
	app.Register(r.command(InputRequired))

	code, stdout, _ := invoke(t, app, "X", "bug.md")
	env := decode(t, stdout)

	if code != 0 || at(env, "input", "kind") != "file" {
		t.Fatalf("exit code %d, input %v; want 0 and kind file", code, env["input"])
	}
	origin, _ := at(env, "input", "origin").(string)
	want, _ := filepath.EvalSymlinks(filepath.Join(dir, "bug.md"))
	got, _ := filepath.EvalSymlinks(origin)
	if !filepath.IsAbs(origin) || got != want {
		t.Errorf("origin = %q, want the absolute path of %q", origin, want)
	}
	if r.input.Body != "the login fails" {
		t.Errorf("Body = %q, want the file's content", r.input.Body)
	}
}

// TestBinaryFileOrDirectoryIsUsage_TS_05_38 verifies TS-05-38:
// A binary file or a directory is refused as usage.
// Verifies: 05-REQ-6.5
func TestBinaryFileOrDirectoryIsUsage_TS_05_38(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "early-nul.bin")
	late := filepath.Join(dir, "late-nul.txt")
	if err := os.WriteFile(binary, append([]byte("text"), 0, 'x'), 0o644); err != nil {
		t.Fatal(err)
	}
	// The NUL is past the first 8 KiB, which is all that is looked at.
	if err := os.WriteFile(late, append(bytes.Repeat([]byte("a"), 8192), 0, 'x'), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		arg  string
		code int
		want string
	}{
		{"a NUL in the first 8 KiB", binary, 2, "not a text file"},
		{"a directory", dir, 2, "directory"},
		{"a NUL after the first 8 KiB", late, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r inputRecorder
			app := newTestApp()
			app.Register(r.command(InputRequired))

			code, stdout, _ := invoke(t, app, "X", tc.arg)
			env := decode(t, stdout)

			if code != tc.code {
				t.Errorf("exit code = %d, want %d\n%s", code, tc.code, stdout)
			}
			if tc.code == 2 {
				if env["status"] != "usage" || r.ran != 0 {
					t.Errorf("status %v, ran %d; want usage, 0", env["status"], r.ran)
				}
				if msg, _ := at(env, "error", "message").(string); !strings.Contains(msg, tc.want) {
					t.Errorf("error.message = %q, want it to contain %q", msg, tc.want)
				}
			}
		})
	}
}

// TestUnreadableFileIsIOFailure_TS_05_39 verifies TS-05-39:
// An existing but unreadable file is failed/io.
// Verifies: 05-REQ-6.5
func TestUnreadableFileIsIOFailure_TS_05_39(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions do not stop this user")
	}
	path := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	var r inputRecorder
	app := newTestApp()
	app.Register(r.command(InputRequired))

	code, stdout, _ := invoke(t, app, "X", path)
	env := decode(t, stdout)

	if code != 1 || env["status"] != "failed" || at(env, "error", "category") != CategoryIO || r.ran != 0 {
		t.Errorf("exit code %d, status %v, category %v, ran %d; want 1, failed, io, 0", code, env["status"], at(env, "error", "category"), r.ran)
	}
}

// TestAnythingElseIsTrimmedText_TS_05_40 verifies TS-05-40:
// Anything else is text, trimmed.
// Verifies: 05-REQ-6.6
func TestAnythingElseIsTrimmedText_TS_05_40(t *testing.T) {
	t.Chdir(t.TempDir())
	var r inputRecorder
	app := newTestApp()
	app.Register(r.command(InputRequired))

	code, stdout, _ := invoke(t, app, "X", "  fix the login bug  ")
	env := decode(t, stdout)

	if code != 0 || at(env, "input", "kind") != "text" || at(env, "input", "origin") != "" {
		t.Errorf("exit code %d, input %v; want 0, kind text, origin \"\"", code, env["input"])
	}
	if r.input.Body != "fix the login bug" {
		t.Errorf("Body = %q, want the trimmed text", r.input.Body)
	}
}

// TestMissingOrBlankInputRefused_TS_05_41 verifies TS-05-41:
// A missing or blank input is refused before the command runs.
// Verifies: 05-REQ-6.7
func TestMissingOrBlankInputRefused_TS_05_41(t *testing.T) {
	t.Chdir(t.TempDir())
	cases := []struct {
		name  string
		args  []string
		stdin string
	}{
		{"no positional", []string{"X"}, ""},
		{"blank text", []string{"X", "   "}, ""},
		{"empty stdin", []string{"X", "-"}, ""},
		{"blank stdin", []string{"X", "-"}, " \n\t\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r inputRecorder
			app := newTestApp()
			app.Register(r.command(InputRequired))

			code, stdout, _ := invokeStdin(t, app, tc.stdin, tc.args...)

			if code != 2 || decode(t, stdout)["status"] != "usage" || r.ran != 0 {
				t.Errorf("exit code %d, status %v, ran %d; want 2, usage, 0", code, decode(t, stdout)["status"], r.ran)
			}
		})
	}

	// An optional input may be left out, but not left blank.
	var r inputRecorder
	app := newTestApp()
	app.Register(r.command(InputOptional))
	if code, _, _ := invoke(t, app, "X"); code != 0 || r.input.Kind != KindNone {
		t.Errorf("optional input left out: exit code %d, kind %q; want 0, none", code, r.input.Kind)
	}
	if code, _, _ := invoke(t, app, "X", "  "); code != 2 {
		t.Errorf("optional input left blank: exit code %d, want 2", code)
	}
}

// TestEnvelopeInputReflectsResolvedInput_TS_05_42 verifies TS-05-42:
// The envelope input object reflects the resolved input or none.
// Verifies: 05-REQ-6.8
func TestEnvelopeInputReflectsResolvedInput_TS_05_42(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bug.md")
	if err := os.WriteFile(path, []byte("broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	var r inputRecorder
	app := newTestApp()
	app.Register(r.command(InputRequired))
	app.Register(Command{Path: "bare", Run: func(*Run) (Outcome, error) { return Outcome{Status: StatusDone}, nil }})

	_, stdout, _ := invoke(t, app, "X", path)
	input, _ := decode(t, stdout)["input"].(map[string]any)
	if input["kind"] != "file" || input["origin"] != path && !strings.HasSuffix(input["origin"].(string), "bug.md") || input["truncated"] != false {
		t.Errorf("input = %v, want kind file, an origin ending in bug.md, truncated false", input)
	}

	_, stdout, _ = invoke(t, app, "bare")
	input, _ = decode(t, stdout)["input"].(map[string]any)
	if input["kind"] != "none" || input["origin"] != "" || input["truncated"] != false {
		t.Errorf("input = %v, want kind none, origin \"\", truncated false", input)
	}
}

// TestNoInputCommandReadsNothing guards the rule behind TS-05-34 and
// TS-05-37: a command that declares no input never has its positional
// argument read, from stdin or from the file system.
func TestNoInputCommandReadsNothing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "notes.bin"), []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	var r recorder
	app := newTestApp()
	app.Register(r.command("X"))
	stdin := &countingReader{}

	for _, arg := range []string{"-", "notes.bin", "https://example.com/page"} {
		var out bytes.Buffer
		code := app.Main(context.Background(), []string{"af", "X", arg}, stdin, &out, &bytes.Buffer{})
		if code != 0 || len(r.args) != 1 || r.args[0] != arg {
			t.Errorf("%q: exit code %d, args %q; want 0 and the argument as given\n%s", arg, code, r.args, out.String())
		}
	}
	if stdin.reads != 0 {
		t.Errorf("stdin was read %d times", stdin.reads)
	}
}

type countingReader struct{ reads int }

func (c *countingReader) Read([]byte) (int, error) {
	c.reads++
	return 0, os.ErrClosed
}

// TestOversizedBodyIsCut_TS_05_44 verifies TS-05-44:
// An oversized body is cut with the marker and a warning.
// Verifies: 05-REQ-7.2
func TestOversizedBodyIsCut_TS_05_44(t *testing.T) {
	const marker = "\n[... input truncated at 131072 bytes ...]\n"
	t.Chdir(t.TempDir())

	t.Run("ascii", func(t *testing.T) {
		var r inputRecorder
		app := newTestApp()
		app.Register(r.command(InputRequired))
		body := strings.Repeat("a", 131072) + "bcd"

		code, stdout, _ := invoke(t, app, "X", body)
		env := decode(t, stdout)

		if code != 0 {
			t.Fatalf("exit code = %d\n%s", code, stdout)
		}
		if want := strings.Repeat("a", 131072) + marker; r.input.Body != want {
			t.Errorf("Body is %d bytes and ends %q, want the first 131072 bytes and the marker", len(r.input.Body), tail(r.input.Body, 60))
		}
		if at(env, "input", "truncated") != true || !contains(warningCodes(env), "input_truncated") {
			t.Errorf("input.truncated = %v, warnings = %v; want true and input_truncated", at(env, "input", "truncated"), warningCodes(env))
		}
	})

	t.Run("a rune on the limit is dropped whole", func(t *testing.T) {
		var r inputRecorder
		app := newTestApp()
		app.Register(r.command(InputRequired))
		// "é" takes bytes 131071 and 131072.
		body := strings.Repeat("a", 131071) + "é" + "tail"

		invoke(t, app, "X", body)

		if want := strings.Repeat("a", 131071) + marker; r.input.Body != want {
			t.Errorf("Body is %d bytes, want 131071 bytes and the marker", len(r.input.Body))
		}
	})

	t.Run("a body at the limit is whole", func(t *testing.T) {
		var r inputRecorder
		app := newTestApp()
		app.Register(r.command(InputRequired))

		_, stdout, _ := invoke(t, app, "X", strings.Repeat("a", 131072))

		if r.input.Truncated || len(r.input.Body) != 131072 || at(decode(t, stdout), "input", "truncated") != false {
			t.Errorf("a body of exactly 131072 bytes was cut (truncated %v, %d bytes)", r.input.Truncated, len(r.input.Body))
		}
	})
}

// tail is the last n bytes of s, or all of it.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// TestTruncateProperty_TS_05_45 verifies TS-05-45:
// Truncation always yields valid UTF-8 within the bound and is the identity
// below it.
// Verifies: 05-REQ-7.3
func TestTruncateProperty_TS_05_45(t *testing.T) {
	const marker = "\n[... input truncated at 131072 bytes ...]\n"
	runes := []rune{'a', 'é', '€', '😀'} // 1, 2, 3 and 4 bytes
	rng := rand.New(rand.NewPCG(45, 7))

	for i := range 120 {
		// Sizes cluster around the limit, where the cut falls inside a rune.
		target := rng.IntN(300001)
		if i%3 == 0 {
			target = 131072 - 4 + rng.IntN(9)
		}
		var b strings.Builder
		for b.Len() < target {
			b.WriteRune(runes[rng.IntN(len(runes))])
		}
		s := b.String()

		got, cut := Truncate(s)

		if !utf8.ValidString(got) {
			t.Fatalf("#%d: Truncate of %d bytes is not valid UTF-8", i, len(s))
		}
		if len(got) > 131072+len(marker) {
			t.Fatalf("#%d: Truncate of %d bytes is %d bytes", i, len(s), len(got))
		}
		if len(s) <= 131072 && (got != s || cut) {
			t.Fatalf("#%d: Truncate changed a body of %d bytes", i, len(s))
		}
		if len(s) > 131072 && (!cut || !strings.HasSuffix(got, marker) || !strings.HasPrefix(s, strings.TrimSuffix(got, marker))) {
			t.Fatalf("#%d: Truncate of %d bytes did not cut the body at a prefix and mark it", i, len(s))
		}
	}
}
