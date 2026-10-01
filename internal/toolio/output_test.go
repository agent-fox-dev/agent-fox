package toolio

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TS-08-1 (unit): Common.Register adds an --output flag defaulting to empty.
func TestTS08_1_RegisterAddsOutputFlag(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	var c Common
	c.Register(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if fs.Lookup("output") == nil {
		t.Fatal("--output is not registered")
	}
	if c.Output != "" {
		t.Errorf("Output = %q, want empty", c.Output)
	}
	got, err := c.ResolveOutput()
	if err != nil || got != "" {
		t.Errorf("ResolveOutput() = %q, %v; want empty, nil", got, err)
	}
}

// TS-08-2 (unit): --output resolves against the process working directory,
// not --dir.
func TestTS08_2_OutputResolvesAgainstCwdNotDir(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	c := Common{Dir: dir, Output: filepath.Join("sub", "out.json")}
	got, err := c.ResolveOutput()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cwd, "sub", "out.json"); got != want {
		t.Errorf("resolved = %q, want %q", got, want)
	}
	if strings.HasPrefix(got, dir) {
		t.Errorf("resolved %q falls under --dir %q", got, dir)
	}
}

func outputUsageRun(t *testing.T, argv []string) (Envelope, int, bool) {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var execCalled bool
	app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
		execCalled = true
		return ExitOK, nil, nil
	})
	env, code, _ := runApp(t, app, argv, "")
	return env, code, execCalled
}

// TS-08-3 (unit): --output - is refused as a usage error before anything is
// fetched.
func TestTS08_3_OutputDashIsUsageError(t *testing.T) {
	env, code, execCalled := outputUsageRun(t, []string{"--dir", t.TempDir(), "--output", "-", "some report"})
	if code != ExitUsage {
		t.Fatalf("code = %d, want %d", code, ExitUsage)
	}
	if env.Error == nil || env.Error.Category != "usage" {
		t.Fatalf("Error = %+v", env.Error)
	}
	if !strings.Contains(env.Error.Message, "--output") {
		t.Errorf("message %q does not name --output", env.Error.Message)
	}
	if execCalled {
		t.Error("Exec ran")
	}
}

// TS-08-4 (unit): an --output path that is an existing directory is a usage
// error caught before anything is fetched.
func TestTS08_4_OutputDirectoryIsUsageError(t *testing.T) {
	d := t.TempDir()
	env, code, execCalled := outputUsageRun(t, []string{"--dir", t.TempDir(), "--output", d, "some report"})
	if code != ExitUsage {
		t.Fatalf("code = %d, want %d", code, ExitUsage)
	}
	if env.Error == nil || env.Error.Category != "usage" {
		t.Fatalf("Error = %+v", env.Error)
	}
	if execCalled {
		t.Error("Exec ran")
	}
}

// TS-08-5 (integration): --output validation runs before PreCheck,
// Workspace(), Resolve and model resolution.
func TestTS08_5_OutputValidatedBeforePreCheck(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// No ANTHROPIC_API_KEY and a --dir that does not exist: if anything past
	// validation ran, the failure would be a different one.
	var preCheck, checkInput, execCalled bool
	app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
		execCalled = true
		return ExitOK, nil, nil
	})
	app.PreCheck = func(*Common) error { preCheck = true; return nil }
	app.CheckInput = func(Input) error { checkInput = true; return nil }

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(),
		[]string{"--dir", filepath.Join(t.TempDir(), "missing"), "--output", "-", "x"},
		strings.NewReader(""), &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("code = %d, want %d", code, ExitUsage)
	}
	if preCheck || checkInput || execCalled {
		t.Errorf("PreCheck=%v CheckInput=%v Exec=%v, want none called", preCheck, checkInput, execCalled)
	}
	if !strings.Contains(stderr.String(), "--output") {
		t.Errorf("stderr %q does not name --output", stderr.String())
	}
}

// outputWarning returns the first warning with the given code, or nil.
func outputWarning(ws []Warning, code string) *Warning {
	for i := range ws {
		if string(ws[i].Code) == code {
			return &ws[i]
		}
	}
	return nil
}

// unwritableOutput returns a path whose parent cannot be created, because an
// ancestor is a regular file (this holds when running as root too).
func unwritableOutput(t *testing.T) string {
	t.Helper()
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(blocker, "sub", "out.json")
}

// TS-08-16 (unit): a failed --output write is a low-severity
// output_not_written warning visible on the same run's stdout envelope, with
// the exit code unaffected.
func TestTS08_16_FailedOutputWriteIsLowWarning(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	unwritable := unwritableOutput(t)

	app, _ := newApp(t, nil)
	env, code, _ := runApp(t, app, []string{"--dir", t.TempDir(), "--output", unwritable, "x"}, "")
	if code != ExitOK || !env.OK || env.ExitCode != ExitOK || env.Status != "done" {
		t.Errorf("code=%d ok=%v exit_code=%d status=%q, want a clean success", code, env.OK, env.ExitCode, env.Status)
	}
	w := outputWarning(env.Warnings, "output_not_written")
	if w == nil {
		t.Fatalf("no output_not_written warning on the stdout envelope: %+v", env.Warnings)
	}
	if w.Severity != "low" || w.Stage != "emit" {
		t.Errorf("warning = %+v, want severity low, stage emit", *w)
	}
	if !strings.Contains(w.Message, unwritable) {
		t.Errorf("message %q does not name the path %q", w.Message, unwritable)
	}
	if strings.TrimSpace(strings.TrimPrefix(w.Message, unwritable)) == "" {
		t.Errorf("message %q does not carry the underlying error", w.Message)
	}
	if _, err := os.Stat(unwritable); err == nil {
		t.Error("a file exists at the unwritable path")
	}
	n := 0
	for _, w := range env.Warnings {
		if string(w.Code) == "output_not_written" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("output_not_written appears %d times, want 1", n)
	}
}

// TS-08-17 (unit): the central WarnCode table maps output_not_written to
// stage emit.
func TestTS08_17_WarnTableMapsOutputNotWritten(t *testing.T) {
	stage, ok := WarnStage(WarnCode("output_not_written"))
	if !ok {
		t.Fatal("the WarnCode table has no output_not_written entry")
	}
	if stage != "emit" {
		t.Errorf("stage = %q, want emit", stage)
	}
	found := false
	for _, c := range DeclaredWarnCodes() {
		if c == WarnCode("output_not_written") {
			found = true
		}
	}
	if !found {
		t.Error("DeclaredWarnCodes() does not list output_not_written")
	}
}

// TS-08-18 (unit): when Emit's own fallback envelope also fails to write to
// --output, no second warnings entry is added.
func TestTS08_18_FallbackWriteFailureAddsNoWarning(t *testing.T) {
	unwritable := unwritableOutput(t)

	r := NewRun("tool", "test")
	env := r.Envelope(ExitOK, map[string]any{"bad": make(chan int)}, nil)
	var buf bytes.Buffer
	code := EmitWithOutput(&buf, unwritable, env)
	if code != ExitFailed {
		t.Errorf("code = %d, want %d", code, ExitFailed)
	}
	var got Envelope
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not the fallback envelope: %v\n%s", err, buf.String())
	}
	if got.Error == nil || got.Error.Stage != "emit" {
		t.Errorf("stdout is not the marshal-failure fallback: %+v", got)
	}
	if outputWarning(got.Warnings, "output_not_written") != nil || len(got.Warnings) != 0 {
		t.Errorf("fallback carries warnings %+v, want none", got.Warnings)
	}
	if _, err := os.Stat(unwritable); err == nil {
		t.Error("a file exists at the unwritable path")
	}
}
