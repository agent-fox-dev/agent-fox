package toolio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/issuex"
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
		if seen.Runner == nil || seen.Workspace == nil || seen.GitHub == nil || seen.Forge == nil {
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

// roundTripFunc implements http.RoundTripper for tests.
type testRoundTripper func(*http.Request) (*http.Response, error)

func (f testRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// TS-04-9: Deps struct defines Forge field of type issuex.Client (04-REQ-3.1).
func TestDeps_ForgeField_TS_04_9(t *testing.T) {
	var d Deps
	var _ issuex.Client = d.Forge
}

// TS-04-10: App.execute instantiates host-specific forge client for recognized issue URLs (04-REQ-3.2, 04-REQ-3.5).
func TestApp_Execute_HostSpecificForgeClient_TS_04_10(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	dir := t.TempDir()

	oldTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = oldTransport }()

	var interceptedHost string
	http.DefaultTransport = testRoundTripper(func(req *http.Request) (*http.Response, error) {
		interceptedHost = req.URL.Host
		if strings.Contains(req.URL.Path, "/issues/42/notes") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`[]`)),
			}, nil
		}
		respJSON := `{
			"id": 42,
			"iid": 42,
			"title": "GitLab Test Issue",
			"description": "issue description",
			"state": "opened",
			"web_url": "https://gitlab.com/group/project/-/issues/42",
			"author": {"username": "alice"}
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(respJSON)),
		}, nil
	})

	var receivedForge issuex.Client
	app := &App{
		Name:    "testapp",
		Version: "1.0",
		Usage:   "usage\n",
		Exec: func(ctx context.Context, d Deps) (int, any, *ErrorInfo) {
			receivedForge = d.Forge
			return ExitOK, map[string]string{"stage": "done"}, nil
		},
	}

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", dir, "https://gitlab.com/group/project/-/issues/42"}, strings.NewReader(""), &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("code = %d stderr = %s", code, stderr.String())
	}
	if receivedForge == nil {
		t.Fatal("expected receivedForge to be non-nil in Deps.Forge")
	}
	if interceptedHost != "gitlab.com" {
		t.Errorf("expected request host to be gitlab.com, got %q", interceptedHost)
	}
	if !strings.Contains(fmt.Sprintf("%+v", receivedForge), "gitlab.com") {
		t.Errorf("expected forge client to target gitlab.com, got %+v", receivedForge)
	}
}

// TS-04-11: App.execute falls back to NoOpClient when forge client creation fails for non-issue input (04-REQ-3.3).
func TestApp_Execute_FallbackNoOp_TS_04_11(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITLAB_TOKEN", "")
	dir := t.TempDir()

	var receivedForge issuex.Client
	app := &App{
		Name:    "testapp",
		Version: "1.0",
		Usage:   "usage\n",
		Exec: func(ctx context.Context, d Deps) (int, any, *ErrorInfo) {
			receivedForge = d.Forge
			return ExitOK, map[string]string{"stage": "done"}, nil
		},
	}

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", dir, "plain text bug report"}, strings.NewReader(""), &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d (stderr: %s)", code, stderr.String())
	}
	if receivedForge == nil {
		t.Fatal("expected non-nil Deps.Forge")
	}
	if receivedForge.Authenticated() {
		t.Errorf("expected NoOpClient fallback to be unauthenticated")
	}
}

// TS-04-12: App.execute records warning when issue comments could not be read (04-REQ-3.4).
func TestApp_Execute_CommentsWarning_TS_04_12(t *testing.T) {
	// Unit verification per pseudocode
	mockRun := NewRun("test", "1.0")
	thread := issuex.IssueThread{CommentsErr: errors.New("rate limited")}
	in := Input{Kind: KindIssue, Thread: &thread}
	if in.Thread != nil && in.Thread.CommentsErr != nil {
		mockRun.Warn("the issue's comments could not be read: %v", in.Thread.CommentsErr)
	}
	warns := mockRun.Warnings()
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warns))
	}
	if !strings.Contains(warns[0], "the issue's comments could not be read: rate limited") {
		t.Errorf("unexpected warning message: %s", warns[0])
	}

	// Integration verification through App.execute
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	dir := t.TempDir()

	oldTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = oldTransport }()

	http.DefaultTransport = testRoundTripper(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/issues/42/notes") {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"message": "rate limited"}`)),
			}, nil
		}
		respJSON := `{
			"id": 42,
			"iid": 42,
			"title": "GitLab Test Issue",
			"description": "issue description",
			"state": "opened",
			"web_url": "https://gitlab.com/group/project/-/issues/42",
			"author": {"username": "alice"}
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(respJSON)),
		}, nil
	})

	app, _ := newApp(t, nil)
	env, code, _ := runApp(t, app, []string{"--dir", dir, "https://gitlab.com/group/project/-/issues/42"}, "")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	var foundWarning bool
	for _, w := range env.Warnings {
		if strings.Contains(w, "the issue's comments could not be read:") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Errorf("expected warning about unreadable comments, got warnings: %v", env.Warnings)
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
