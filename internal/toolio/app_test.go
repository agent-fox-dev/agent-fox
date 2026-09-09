package toolio

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"strings"
	"testing"
)

// newApp builds an App whose Exec records what it was handed, so a test can
// assert on what the shared shell resolved before calling it.
func newApp(t *testing.T, exec func(context.Context, Deps) (int, any, *ErrorInfo)) (*App, *Deps) {
	t.Helper()
	var seen Deps
	var dryRun bool
	app := &App{
		Name:    "tool",
		Version: "test",
		Usage:   "usage\n",
		Flags:   func(fs *flag.FlagSet) { fs.BoolVar(&dryRun, "dry-run", false, "") },
		Exec: func(ctx context.Context, d Deps) (int, any, *ErrorInfo) {
			seen = d
			if exec != nil {
				return exec(ctx, d)
			}
			return ExitOK, map[string]string{"stage": "done"}, nil
		},
	}
	return app, &seen
}

func runApp(t *testing.T, app *App, argv []string, stdin string) (Envelope, int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), argv, strings.NewReader(stdin), &stdout, &stderr)

	var env Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not one JSON object (%v):\n%s", err, stdout.String())
	}
	return env, code, stderr.String()
}

func TestAppEmitsJSONOnEveryPath(t *testing.T) {
	// The model is resolved from the environment, so the tests below that
	// reach Exec need a credential the resolver accepts. A base URL is enough
	// for the ambient-credential case.
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	dir := t.TempDir()

	t.Run("success", func(t *testing.T) {
		app, seen := newApp(t, nil)
		env, code, _ := runApp(t, app, []string{"--dir", dir, "a problem report"}, "")
		if code != ExitOK || !env.OK {
			t.Fatalf("code=%d env=%+v", code, env)
		}
		if env.Input == nil || env.Input.Kind != string(KindText) {
			t.Errorf("Input = %+v", env.Input)
		}
		if env.Model == nil || env.Model.Spec == "" {
			t.Errorf("Model = %+v", env.Model)
		}
		if seen.Runner == nil || seen.Workspace == nil || seen.GitHub == nil {
			t.Error("Exec was called with an incomplete Deps")
		}
	})

	t.Run("no input", func(t *testing.T) {
		// A bare invocation with no positional argument is a person asking
		// what the tool does, not a program handing over work: the help text
		// goes to stderr and stdout stays empty, rather than pairing it with
		// a JSON envelope that only restates it.
		app, _ := newApp(t, nil)
		var stdout, stderr bytes.Buffer
		code := app.Main(context.Background(), []string{"--dir", dir}, strings.NewReader(""), &stdout, &stderr)
		if code != ExitUsage {
			t.Fatalf("code=%d", code)
		}
		if stdout.Len() != 0 {
			t.Errorf("stdout should be empty for a bare invocation, got %q", stdout.String())
		}
		if !strings.Contains(stderr.String(), "usage") {
			t.Errorf("the flag list should be printed for a bare invocation:\n%s", stderr.String())
		}
	})

	t.Run("-h prints help and exits 0 with no envelope", func(t *testing.T) {
		app, _ := newApp(t, nil)
		var stdout, stderr bytes.Buffer
		code := app.Main(context.Background(), []string{"-h"}, strings.NewReader(""), &stdout, &stderr)
		if code != ExitOK {
			t.Fatalf("code=%d", code)
		}
		if stdout.Len() != 0 {
			t.Errorf("stdout should be empty for -h, got %q", stdout.String())
		}
		if !strings.Contains(stderr.String(), "usage") {
			t.Errorf("the help text should be printed for -h:\n%s", stderr.String())
		}
	})

	t.Run("undefined flag", func(t *testing.T) {
		app, _ := newApp(t, nil)
		env, code, _ := runApp(t, app, []string{"--nope", "x"}, "")
		if code != ExitUsage || env.OK {
			t.Fatalf("code=%d env=%+v", code, env)
		}
	})

	t.Run("a directory that is not one", func(t *testing.T) {
		app, _ := newApp(t, nil)
		env, code, _ := runApp(t, app, []string{"--dir", "/definitely/not/here", "x"}, "")
		if code != ExitUsage || env.Error == nil {
			t.Fatalf("code=%d env=%+v", code, env)
		}
	})

	t.Run("a failing Exec", func(t *testing.T) {
		app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
			return ExitUnverified, map[string]string{"stage": "unverified"},
				&ErrorInfo{Stage: "verify", Category: "unverified", Message: "checks failed"}
		})
		env, code, _ := runApp(t, app, []string{"--dir", dir, "x"}, "")
		if code != ExitUnverified || env.OK {
			t.Fatalf("code=%d env=%+v", code, env)
		}
		if env.Error.Stage != "verify" {
			t.Errorf("Error = %+v", env.Error)
		}
		if env.Result == nil {
			t.Error("a partial result should still be reported")
		}
	})
}

// The pre-checks run before anything is fetched, so a bad flag combination
// never costs a network call or a token.
func TestPreCheckRunsBeforeAnythingIsFetched(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	var execCalled bool
	app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
		execCalled = true
		return ExitOK, nil, nil
	})
	app.PreCheck = func(*Common) error { return Usagef("--a and --b cannot both be given") }

	env, code, _ := runApp(t, app, []string{"--dir", t.TempDir(), "x"}, "")
	if code != ExitUsage {
		t.Fatalf("code = %d", code)
	}
	if execCalled {
		t.Error("Exec ran after a failed pre-check")
	}
	if !strings.Contains(env.Error.Message, "cannot both be given") {
		t.Errorf("Error = %+v", env.Error)
	}
}

func TestCheckInputRunsAfterClassification(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	var got Input
	app, _ := newApp(t, nil)
	app.CheckInput = func(in Input) error {
		got = in
		return Usagef("--overwrite needs a GitHub issue URL; %s is %s", in.Origin, in.Kind)
	}

	env, code, _ := runApp(t, app, []string{"--dir", t.TempDir(), "just some text"}, "")
	if code != ExitUsage {
		t.Fatalf("code = %d", code)
	}
	if got.Kind != KindText {
		t.Errorf("CheckInput saw %+v", got)
	}
	if !strings.Contains(env.Error.Message, "is text") {
		t.Errorf("Error = %+v", env.Error)
	}
}

// A model that cannot be resolved, or a vendor this shell cannot authenticate
// to, fails before a prompt is built.
func TestAnUnresolvableModelFailsInPreflight(t *testing.T) {
	var execCalled bool
	app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
		execCalled = true
		return ExitOK, nil, nil
	})
	env, code, _ := runApp(t, app,
		[]string{"--dir", t.TempDir(), "--model", "nonexistent/model-9", "x"}, "")
	if code != ExitFailed {
		t.Fatalf("code = %d", code)
	}
	if execCalled {
		t.Error("Exec ran with no model")
	}
	if env.Error == nil || env.Error.Stage != "preflight" {
		t.Errorf("Error = %+v", env.Error)
	}
}

// A retired platform variable changes which service a request goes to, which
// is the one outcome an operator cannot debug. It fails loudly.
func TestARetiredPlatformVariableIsRefused(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("CLAUDE_CODE_USE_VERTEX", "1")
	app, _ := newApp(t, nil)
	env, code, _ := runApp(t, app, []string{"--dir", t.TempDir(), "x"}, "")
	if code == ExitOK {
		t.Fatal("the run was allowed")
	}
	if env.Error == nil || !strings.Contains(env.Error.Message, "CLAUDE_CODE_USE_VERTEX") {
		t.Errorf("the error must name the variable: %+v", env.Error)
	}
}

func TestVersionPrintsAndExits(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
		t.Error("Exec ran for --version")
		return ExitOK, nil, nil
	})
	code := app.Main(context.Background(), []string{"--version"}, strings.NewReader(""), &stdout, &stderr)
	if code != ExitOK {
		t.Errorf("code = %d", code)
	}
	if !strings.Contains(stdout.String(), "tool test") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestStdinIsReadThroughTheSharedShell(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	app, seen := newApp(t, nil)
	env, code, _ := runApp(t, app, []string{"--dir", t.TempDir(), "-"}, "piped problem report\n")
	if code != ExitOK {
		t.Fatalf("code = %d, env = %+v", code, env)
	}
	if seen.Input.Kind != KindStdin || seen.Input.Body != "piped problem report\n" {
		t.Errorf("Input = %+v", seen.Input)
	}
}

func TestMain(m *testing.M) {
	// The tests above resolve a model, and a stray credential variable in the
	// developer's shell would change which vendor they resolve against.
	for _, v := range []string{"AF_MODEL", "AGENTKIT_MODEL", "AF_MODEL_VENDOR",
		"CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_BEDROCK"} {
		_ = os.Unsetenv(v)
	}
	os.Exit(m.Run())
}
