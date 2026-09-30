package toolio

import (
	"context"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TS-06-44 (unit): --input-kind is registered on Common accepting only file, text, issue or stdin
func TestTS06_44_InputKindIsRegisteredOnCommon(t *testing.T) {
	parse := func(args ...string) (*Common, error) {
		var c Common
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		c.Register(fs)
		if _, err := SplitArgs(fs, args); err != nil {
			return nil, err
		}
		return &c, nil
	}
	for _, k := range []string{"file", "text", "issue", "stdin"} {
		c, err := parse("--input-kind", k, "x")
		if err != nil {
			t.Fatalf("--input-kind %s: %v", k, err)
		}
		if c.InputKind != k {
			t.Errorf("InputKind = %q, want %q", c.InputKind, k)
		}
		if err := c.ValidInputKind(); err != nil {
			t.Errorf("--input-kind %s refused: %v", k, err)
		}
	}
	c, err := parse("x")
	if err != nil {
		t.Fatal(err)
	}
	if c.InputKind != "" || c.ValidInputKind() != nil {
		t.Errorf("the default must be empty (auto-classify) and valid: %q", c.InputKind)
	}
	c, err = parse("--input-kind", "bogus", "x")
	if err != nil {
		t.Fatal(err)
	}
	if c.ValidInputKind() == nil {
		t.Error("--input-kind bogus was accepted")
	}

	// And through Main: a usage error before Exec runs.
	var execCalled bool
	app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
		execCalled = true
		return ExitOK, nil, nil
	})
	env, code, _ := runApp(t, app, []string{"--input-kind", "bogus", "some text"}, "")
	if code != ExitUsage || env.Error == nil || env.Error.Category != "usage" {
		t.Errorf("code=%d error=%+v", code, env.Error)
	}
	if execCalled {
		t.Error("Exec ran with an invalid --input-kind")
	}
}

// TS-06-45 (unit): --input-kind file refuses a missing path, naming that it does not exist
func TestTS06_45_InputKindFileRefusesMissingPath(t *testing.T) {
	app, _ := newApp(t, nil)
	env, code, stderr := runApp(t, app, []string{"--dir", t.TempDir(), "--input-kind", "file", "/no/such/path.txt"}, "")
	if code != ExitUsage {
		t.Fatalf("code = %d, want %d", code, ExitUsage)
	}
	if env.Error == nil || env.Error.Category != "usage" || !strings.Contains(env.Error.Message, "does not exist") {
		t.Errorf("error = %+v", env.Error)
	}
	_ = stderr
}

// TS-06-46 (unit): --input-kind file refuses a directory, naming that it is one
func TestTS06_46_InputKindFileRefusesDirectory(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	app, _ := newApp(t, nil)
	env, code, _ := runApp(t, app, []string{"--dir", dir, "--input-kind", "file", src}, "")
	if code != ExitUsage {
		t.Fatalf("code = %d, want %d", code, ExitUsage)
	}
	if env.Error == nil || !strings.Contains(env.Error.Message, "directory") {
		t.Errorf("error = %+v", env.Error)
	}
}

// TS-06-47 (unit): --input-kind text uses the argument verbatim, ignoring a same-named file or a parseable URL
func TestTS06_47_InputKindTextIsVerbatim(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("report.txt", []byte("the file's own content"), 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := ResolveForced(context.Background(), "text", "report.txt", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if in.Body != "report.txt" || in.Kind != KindText {
		t.Errorf("input = %+v", in)
	}

	// A parseable issue URL is not fetched either: a nil forge would fail
	// ("no forge client configured") if the issue branch were entered.
	const url = "https://github.com/a/b/issues/1"
	in, err = ResolveForced(context.Background(), "text", url, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if in.Body != url || in.Kind != KindText || in.Issue != nil {
		t.Errorf("input = %+v", in)
	}

	// Verbatim means untrimmed, and no path warning is recorded.
	run := NewRun("tool", "test")
	in, err = ResolveForced(context.Background(), "text", " missing/file.go ", nil, nil, run)
	if err != nil {
		t.Fatal(err)
	}
	if in.Body != " missing/file.go " {
		t.Errorf("body = %q", in.Body)
	}
	if len(run.Envelope(0, nil, nil).Warnings) != 0 {
		t.Error("a forced text input must not be second-guessed with a warning")
	}
}

// TS-06-48 (unit): --input-kind issue refuses an argument that does not parse as an issue URL, before any HTTP request
func TestTS06_48_InputKindIssueRefusesNonURLBeforeHTTP(t *testing.T) {
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	var requests atomic.Int32
	http.DefaultTransport = testRoundTripper(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("no network in this test")
	})

	app, _ := newApp(t, nil)
	env, code, _ := runApp(t, app, []string{"--dir", t.TempDir(), "--input-kind", "issue", "not a url"}, "")
	if code != ExitUsage {
		t.Fatalf("code = %d, want %d", code, ExitUsage)
	}
	if env.Error == nil || env.Error.Category != "usage" {
		t.Errorf("error = %+v", env.Error)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("%d HTTP requests were made", n)
	}
}

// TS-06-49 (unit): --input-kind stdin refuses any argument other than "-"
func TestTS06_49_InputKindStdinRefusesOtherArguments(t *testing.T) {
	app, _ := newApp(t, nil)
	env, code, _ := runApp(t, app, []string{"--dir", t.TempDir(), "--input-kind", "stdin", "file.txt"}, "piped")
	if code != ExitUsage {
		t.Fatalf("code = %d, want %d", code, ExitUsage)
	}
	if env.Error == nil || env.Error.Category != "usage" {
		t.Errorf("error = %+v", env.Error)
	}

	// "-" is accepted.
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	app, _ = newApp(t, nil)
	env, code, _ = runApp(t, app, []string{"--dir", t.TempDir(), "--input-kind", "stdin", "-"}, "piped")
	if code != ExitOK || env.Input == nil || env.Input.Kind != string(KindStdin) {
		t.Errorf("code=%d input=%+v", code, env.Input)
	}
}

// TS-06-50 (property): A mismatched --input-kind is a usage error before anything is fetched or a model is resolved
func TestTS06_50_MismatchedInputKindIsAUsageError(t *testing.T) {
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	var requests atomic.Int32
	http.DefaultTransport = testRoundTripper(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("no network in this test")
	})
	// No credential is set: had a model been resolved, the failure would be
	// a preflight error, not a usage one.
	t.Setenv("ANTHROPIC_API_KEY", "")

	dir := t.TempDir()
	mismatches := []struct{ kind, arg string }{
		{"file", "https://github.com/a/b/issues/1"},
		{"file", filepath.Join(dir, "missing.txt")},
		{"file", dir},
		{"issue", "a plain string"},
		{"issue", "https://example.com/not/a/forge/issues/1"},
		{"stdin", "not-a-dash"},
	}
	for _, m := range mismatches {
		var execCalled bool
		app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
			execCalled = true
			return ExitOK, nil, nil
		})
		env, code, _ := runApp(t, app, []string{"--dir", dir, "--input-kind", m.kind, m.arg}, "")
		if code != ExitUsage {
			t.Errorf("%s %q: code = %d, want %d", m.kind, m.arg, code, ExitUsage)
		}
		if env.Error == nil || env.Error.Category != "usage" {
			t.Errorf("%s %q: error = %+v", m.kind, m.arg, env.Error)
		}
		if execCalled {
			t.Errorf("%s %q: Exec ran", m.kind, m.arg)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("%d HTTP requests were made", n)
	}
}

// A forced file is read like an ordinary one.
func TestResolveForcedFileReadsARegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.txt")
	if err := os.WriteFile(path, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := ResolveForced(context.Background(), "file", path, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if in.Kind != KindFile || in.Body != "body" || in.Origin != filepath.Clean(path) {
		t.Errorf("input = %+v", in)
	}
}
