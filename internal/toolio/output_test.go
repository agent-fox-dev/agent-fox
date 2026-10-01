package toolio

import (
	"bytes"
	"context"
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
