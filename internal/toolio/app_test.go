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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/statetest"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// newApp builds an App whose Exec records what it was handed, so a test can
// assert on what the shared shell resolved before calling it.
func newApp(t *testing.T, exec func(context.Context, Deps) (int, any, *ErrorInfo)) (*App, *Deps) {
	t.Helper()
	var seen Deps
	app := &App{
		Name:    "tool",
		Version: "test",
		Usage:   "usage\n",
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

// TS-06-55 (unit, App half): a one-phase tool's effective per-phase ceiling is
// min(--budget, --total-budget), and it is the ceiling the runner and the
// envelope's bounds carry.
func TestTS06_55_AppAppliesTheLowerCeilingToTheOnePhase(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, tc := range []struct{ budget, total string }{{"10", "4"}, {"4", "10"}} {
		app, seen := newApp(t, nil)
		app.SinglePhase = true
		app.DefaultBounds = agentrun.Bounds{MaxTurns: 10, MaxBudgetUSD: 2}
		_, code, _ := runApp(t, app, []string{"--dir", t.TempDir(), "--budget", tc.budget, "--total-budget", tc.total, "x"}, "")
		if code != ExitOK {
			t.Fatalf("code = %d", code)
		}
		if got := seen.runner.Bounds.MaxBudgetUSD; got != 4 {
			t.Errorf("--budget %s --total-budget %s: runner ceiling = %v, want 4", tc.budget, tc.total, got)
		}
	}

	// A multi-phase tool (SinglePhase unset) keeps the per-phase ceiling.
	app, seen := newApp(t, nil)
	app.DefaultBounds = agentrun.Bounds{MaxTurns: 10, MaxBudgetUSD: 2}
	_, _, _ = runApp(t, app, []string{"--dir", t.TempDir(), "--budget", "10", "--total-budget", "4", "x"}, "")
	if got := seen.runner.Bounds.MaxBudgetUSD; got != 10 {
		t.Errorf("multi-phase runner ceiling = %v, want 10", got)
	}
}

// A negative --total-budget is a usage error, refused before anything runs.
func TestNegativeTotalBudgetIsAUsageError(t *testing.T) {
	app, _ := newApp(t, nil)
	env, code, _ := runApp(t, app, []string{"--dir", t.TempDir(), "--total-budget", "-1", "x"}, "")
	if code != ExitUsage || env.OK {
		t.Fatalf("code=%d env=%+v", code, env)
	}
	if !strings.Contains(env.Error.Message, "--total-budget") {
		t.Errorf("Error = %+v", env.Error)
	}
}

// TS-06-58 (unit): a flag another tool defines, given to a tool that does not
// use it, is rejected naming both the flag and the accepting tool.
func TestTS06_58_UnsupportedFlagNamesTheAcceptingTool(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cases := []struct {
		tool, flag string
		accepting  []string
	}{
		{"impl", "label", []string{"triage"}},
		{"fix", "specs-dir", []string{"spec", "impl"}},
		{"triage", "land", []string{"fix", "impl"}},
		{"spec", "repair", []string{"impl"}},
	}
	for _, tc := range cases {
		app, _ := newApp(t, nil)
		app.Name = tc.tool
		env, code, stderr := runApp(t, app, []string{"--dir", t.TempDir(), "--" + tc.flag, "x", "input"}, "")
		if code != ExitUsage || env.OK {
			t.Fatalf("%s --%s: code=%d env=%+v", tc.tool, tc.flag, code, env)
		}
		if env.Error == nil || env.Error.Category != "usage" {
			t.Fatalf("%s --%s: Error = %+v", tc.tool, tc.flag, env.Error)
		}
		for _, msg := range []string{env.Error.Message, stderr} {
			if !strings.Contains(msg, "--"+tc.flag) {
				t.Errorf("%s --%s: %q does not name the flag", tc.tool, tc.flag, msg)
			}
			if !strings.Contains(msg, tc.tool+" does not accept") {
				t.Errorf("%s --%s: %q does not name the rejecting tool", tc.tool, tc.flag, msg)
			}
			for _, a := range tc.accepting {
				if !strings.Contains(msg, a) {
					t.Errorf("%s --%s: %q does not name %s", tc.tool, tc.flag, msg, a)
				}
			}
		}
		if strings.Contains(env.Error.Message, "flag provided but not defined") {
			t.Errorf("%s --%s: the generic message leaked: %q", tc.tool, tc.flag, env.Error.Message)
		}
	}
}

// TS-06-59 (unit): a flag no tool defines keeps Go's own message.
func TestTS06_59_UnknownFlagKeepsGoMessage(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	app, _ := newApp(t, nil)
	app.Name = "impl"
	env, code, stderr := runApp(t, app, []string{"--dir", t.TempDir(), "--nonexistent-flag", "x", "input"}, "")
	if code != ExitUsage || env.OK {
		t.Fatalf("code=%d env=%+v", code, env)
	}
	want := "flag provided but not defined: -nonexistent-flag"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr %q lacks %q", stderr, want)
	}
	if env.Error == nil || env.Error.Message != want {
		t.Errorf("Error = %+v, want message %q exactly", env.Error, want)
	}
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
		if seen.Runner == nil || seen.Workspace == nil || seen.Forge == nil {
			t.Error("Exec was called with an incomplete Deps")
		}
	})

	t.Run("no input", func(t *testing.T) {
		// A bare invocation with no positional argument when stdout is not a
		// terminal emits a usage envelope, and prints help text to stderr.
		app, _ := newApp(t, nil)
		var stdout, stderr bytes.Buffer
		code := app.Main(context.Background(), []string{"--dir", dir}, strings.NewReader(""), &stdout, &stderr)
		if code != ExitUsage {
			t.Fatalf("code=%d", code)
		}
		var env Envelope
		if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
			t.Fatalf("stdout is not one JSON object (%v):\n%s", err, stdout.String())
		}
		if env.OK || env.ExitCode != ExitUsage {
			t.Errorf("env = %+v", env)
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

// A platform variable for a deployment the agent library cannot reach changes
// which service a request goes to, which is the one outcome an operator cannot
// debug. It fails loudly.
func TestAnUnsupportedPlatformVariableIsRefused(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	app, _ := newApp(t, nil)
	env, code, _ := runApp(t, app, []string{"--dir", t.TempDir(), "x"}, "")
	if code == ExitOK {
		t.Fatal("the run was allowed")
	}
	if env.Error == nil || !strings.Contains(env.Error.Message, "CLAUDE_CODE_USE_BEDROCK") {
		t.Errorf("the error must name the variable: %+v", env.Error)
	}
}

// vertexWithADC selects Claude on Vertex AI with no token in the environment,
// and points Application Default Credentials at adc: a file path, which need
// not exist.
func vertexWithADC(t *testing.T, adc string) {
	t.Helper()
	for _, v := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN"} {
		t.Setenv(v, "")
		_ = os.Unsetenv(v)
	}
	t.Setenv("CLAUDE_CODE_USE_VERTEX", "1")
	t.Setenv("ANTHROPIC_VERTEX_PROJECT_ID", "test-project")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adc)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

// Claude on Vertex AI is served by the agent library: a Vertex deployment has
// no Anthropic key, authenticates with a token minted from Application Default
// Credentials, and must pass pre-flight when those can be found.
func TestClaudeOnVertexPassesPreflightWithADC(t *testing.T) {
	adc := filepath.Join(t.TempDir(), "adc.json")
	if err := os.WriteFile(adc, []byte(`{"type":"authorized_user","client_id":"c","client_secret":"s","refresh_token":"r"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	vertexWithADC(t, adc)
	var execCalled bool
	app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
		execCalled = true
		return ExitOK, nil, nil
	})
	env, code, _ := runApp(t, app, []string{"--dir", t.TempDir(), "x"}, "")
	if code != ExitOK || !execCalled {
		t.Fatalf("code = %d, exec ran = %v, Error = %+v", code, execCalled, env.Error)
	}
}

// With no Google credential anywhere, the run fails in pre-flight naming the
// way to log in, rather than on its first request with a Google 401 that
// names nothing an operator can act on.
func TestClaudeOnVertexWithoutADCFailsInPreflight(t *testing.T) {
	vertexWithADC(t, filepath.Join(t.TempDir(), "missing.json"))
	app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
		t.Error("Exec ran with no credential")
		return ExitOK, nil, nil
	})
	env, code, _ := runApp(t, app, []string{"--dir", t.TempDir(), "x"}, "")
	if code == ExitOK {
		t.Fatal("the run was allowed")
	}
	if env.Error == nil || env.Error.Category != "auth" ||
		!strings.Contains(env.Error.Message, "gcloud auth application-default login") {
		t.Errorf("Error = %+v", env.Error)
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

// A GitHub issue URL must be fetched from the API host (api.github.com),
// not the web host (github.com).
func TestApp_Execute_GitHubIssueURLUsesAPIHost(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("GITHUB_API_URL", "")
	dir := t.TempDir()

	oldTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = oldTransport }()

	var hosts []string
	http.DefaultTransport = testRoundTripper(func(req *http.Request) (*http.Response, error) {
		hosts = append(hosts, req.URL.Host)
		body := `{"number": 34, "title": "T", "body": "b", "state": "open", "user": {"login": "alice"}}`
		if strings.HasSuffix(req.URL.Path, "/comments") {
			body = `[]`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})

	app := &App{
		Name:    "testapp",
		Version: "1.0",
		Usage:   "usage\n",
		Exec: func(ctx context.Context, d Deps) (int, any, *ErrorInfo) {
			return ExitOK, map[string]string{"stage": "done"}, nil
		},
	}

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", dir, "https://github.com/agent-fox-dev/hub/issues/34"}, strings.NewReader(""), &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("code = %d stderr = %s", code, stderr.String())
	}
	if len(hosts) == 0 {
		t.Fatal("expected at least one API request")
	}
	for _, h := range hosts {
		if h != "api.github.com" {
			t.Errorf("request host = %q, want api.github.com", h)
		}
	}
}

// TS-04-10: App.execute instantiates host-specific forge client for recognized issue URLs (04-REQ-3.2, 04-REQ-3.5).
func TestApp_Execute_HostSpecificForgeClient_TS_04_10(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	dir := t.TempDir()

	oldTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = oldTransport }()

	var interceptedHost, interceptedUA string
	http.DefaultTransport = testRoundTripper(func(req *http.Request) (*http.Response, error) {
		interceptedHost = req.URL.Host
		interceptedUA = req.Header.Get("User-Agent")
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
	// The client carries the tool's own user agent, <name>/<version>.
	if interceptedUA != "testapp/1.0" {
		t.Errorf("expected User-Agent testapp/1.0, got %q", interceptedUA)
	}
}

// TS-04-11: App.execute falls back to NoOpClient when forge client creation fails for non-issue input (04-REQ-3.3).
func TestApp_Execute_FallbackNoOp_TS_04_11(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("GITHUB_API_URL", "")
	t.Setenv("GITLAB_API_URL", "")
	dir := t.TempDir()
	// Forge detection falls back to the origin remote of the process's working
	// directory; run from one with none, so the fallback is what is under test
	// and not whatever remote the checkout running the suite has.
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()

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
	// A GitHub or GitLab client built without a token is unauthenticated too;
	// only the fallback itself is a NoOpClient.
	if _, ok := receivedForge.(*issuex.NoOpClient); !ok {
		t.Errorf("expected the fallback to be *issuex.NoOpClient, got %T", receivedForge)
	}
}

// TS-04-12: App.execute records warning when issue comments could not be read (04-REQ-3.4).
func TestApp_Execute_CommentsWarning_TS_04_12(t *testing.T) {
	// Unit verification per pseudocode
	mockRun := NewRun("test", "1.0")
	thread := issuex.IssueThread{CommentsErr: errors.New("rate limited")}
	in := Input{Kind: KindIssue, Thread: &thread}
	if in.Thread != nil && in.Thread.CommentsErr != nil {
		mockRun.Warn(WarnCommentsUnreadable, "low", "the issue's comments could not be read: %v", in.Thread.CommentsErr)
	}
	warns := mockRun.Warnings()
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warns))
	}
	if !strings.Contains(warns[0].Message, "the issue's comments could not be read: rate limited") {
		t.Errorf("unexpected warning message: %s", warns[0].Message)
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
		if strings.Contains(w.Message, "the issue's comments could not be read:") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Errorf("expected warning about unreadable comments, got warnings: %v", env.Warnings)
	}
}

// TS-05-1 (unit): A whitespace-only positional argument is folded into the bare-invocation case before Resolve runs, with no model resolution attempted
func TestTS_05_1(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")

	dir := t.TempDir()
	app, _ := newApp(t, nil)
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", dir, "   "}, strings.NewReader(""), &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage (%d), got %d", ExitUsage, code)
	}
	if !strings.Contains(stderr.String(), "usage") {
		t.Errorf("expected stderr to contain usage help text, got:\n%s", stderr.String())
	}
}

// TS-05-2 (unit): A bare invocation with stdout not a terminal emits a usage envelope naming the four accepted input shapes
func TestTS_05_2(t *testing.T) {
	dir := t.TempDir()
	app, _ := newApp(t, nil)
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", dir}, strings.NewReader(""), &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("expected exit code %d, got %d", ExitUsage, code)
	}
	var env Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not one JSON object (%v):\n%s", err, stdout.String())
	}
	if env.OK {
		t.Errorf("expected ok=false, got true")
	}
	if env.ExitCode != ExitUsage {
		t.Errorf("expected exit_code=%d, got %d", ExitUsage, env.ExitCode)
	}
	if env.Error == nil {
		t.Fatalf("expected error object, got nil")
	}
	if env.Error.Stage != "usage" {
		t.Errorf("expected error.stage=usage, got %q", env.Error.Stage)
	}
	if env.Error.Category != "usage" {
		t.Errorf("expected error.category=usage, got %q", env.Error.Category)
	}
	const wantMsg = "no input: give a report, a file path, a GitHub or GitLab issue URL, or - to read stdin"
	if env.Error.Message != wantMsg {
		t.Errorf("expected error.message=%q, got %q", wantMsg, env.Error.Message)
	}
	var raw map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &raw); err == nil {
		if status, ok := raw["status"]; ok && status != "usage" {
			t.Errorf("expected status=usage, got %v", status)
		}
	}
}

// TS-05-3 (unit): A bare invocation with stdout a terminal writes nothing to stdout
func TestTS_05_3(t *testing.T) {
	f, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer f.Close()

	if !isTerminal(f) {
		t.Skipf("%s is not reported as terminal", os.DevNull)
	}

	dir := t.TempDir()
	app, _ := newApp(t, nil)
	var stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", dir}, strings.NewReader(""), f, &stderr)
	if code != ExitUsage {
		t.Fatalf("expected exit code %d, got %d", ExitUsage, code)
	}
	info, err := f.Stat()
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("expected stdout size 0, got %d", info.Size())
	}
	if !strings.Contains(stderr.String(), "usage") {
		t.Errorf("the help text should be printed to stderr:\n%s", stderr.String())
	}
}

// TS-05-4 (unit): -h/--help and --version exit before the bare-invocation check and never emit an envelope
func TestTS_05_4(t *testing.T) {
	app, _ := newApp(t, nil)

	var out1, err1 bytes.Buffer
	code1 := app.Main(context.Background(), []string{"-h"}, strings.NewReader(""), &out1, &err1)
	if code1 != ExitOK {
		t.Fatalf("expected exit code 0 for -h, got %d", code1)
	}
	if out1.Len() != 0 {
		t.Errorf("expected empty stdout for -h, got %q", out1.String())
	}

	var out2, err2 bytes.Buffer
	code2 := app.Main(context.Background(), []string{"--version"}, strings.NewReader(""), &out2, &err2)
	if code2 != ExitOK {
		t.Fatalf("expected exit code 0 for --version, got %d", code2)
	}
	trimmed := strings.TrimSpace(out2.String())
	if strings.HasPrefix(trimmed, "{") {
		t.Errorf("expected version sentence, got JSON envelope: %s", trimmed)
	}
	if !strings.Contains(trimmed, "tool test") {
		t.Errorf("expected version output to contain 'tool test', got: %s", trimmed)
	}
}

// TS-05-5 (unit): The bare-invocation envelope's error.message is byte-identical to the message toolio.ErrNoInput's own path produces
func TestTS_05_5(t *testing.T) {
	dir := t.TempDir()
	app, _ := newApp(t, nil)

	// Bare invocation (no positional arg)
	var outBare, errBare bytes.Buffer
	codeBare := app.Main(context.Background(), []string{"--dir", dir}, strings.NewReader(""), &outBare, &errBare)
	if codeBare != ExitUsage {
		t.Fatalf("expected exit code %d for bare invocation, got %d", ExitUsage, codeBare)
	}
	var envBare Envelope
	if err := json.Unmarshal(outBare.Bytes(), &envBare); err != nil {
		t.Fatalf("failed to unmarshal bare envelope: %v", err)
	}

	// Resolve ErrNoInput path (e.g. stdin piped as "-" with only whitespace)
	envStdin, codeStdin, _ := runApp(t, app, []string{"--dir", dir, "-"}, "   \n")
	if codeStdin != ExitUsage {
		t.Fatalf("expected exit code %d for stdin with only whitespace, got %d", ExitUsage, codeStdin)
	}

	if envBare.Error == nil || envStdin.Error == nil {
		t.Fatalf("expected both envelopes to have an error, got envBare=%+v, envStdin=%+v", envBare.Error, envStdin.Error)
	}
	if envBare.Error.Message != envStdin.Error.Message {
		t.Errorf("bare invocation error message %q != stdin error message %q", envBare.Error.Message, envStdin.Error.Message)
	}
	const expected = "no input: give a report, a file path, a GitHub or GitLab issue URL, or - to read stdin"
	if envBare.Error.Message != expected {
		t.Errorf("expected message %q, got %q", expected, envBare.Error.Message)
	}
}

// TS-06-2 (unit): An unrecognized --detail value is a usage error before any input is resolved
func TestTS06_2_UnrecognizedDetailIsUsageError(t *testing.T) {
	var execCalled bool
	app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
		execCalled = true
		return ExitOK, nil, nil
	})
	env, code, _ := runApp(t, app, []string{"--detail", "wrong", "some text"}, "")
	if code != ExitUsage {
		t.Fatalf("expected exit code %d, got %d", ExitUsage, code)
	}
	if env.Error == nil || env.Error.Category != "usage" {
		t.Errorf("expected error.category == usage, got %+v", env.Error)
	}
	if execCalled {
		t.Error("Exec ran with an invalid --detail: nothing should have been resolved")
	}
}

// ---- 07 progress_event_stream: --emit-events (task 2) ----

// eventsRun runs app with argv and returns the exit code, stdout and stderr.
func eventsRun(t *testing.T, app *App, argv []string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), argv, strings.NewReader(""), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// stateEventsFile returns the one events file a run left under
// $XDG_STATE_HOME/agent-fox/events/ (12-REQ-1.1): the file is always written,
// whatever reaches stderr.
func stateEventsFile(t *testing.T, state string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(state, "agent-fox", "events", "*.jsonl"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("want exactly one events file under %s, got %v (%v)", state, matches, err)
	}
	return matches[0]
}

// eventTypesOfLines returns the type of every event in the lines.
func eventTypesOfLines(t *testing.T, lines []string) []string {
	t.Helper()
	var out []string
	for _, l := range lines {
		var e struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatalf("events file line is not JSON: %q: %v", l, err)
		}
		out = append(out, e.Type)
	}
	return out
}

// stepMessages returns the message of every step event in the lines.
func stepMessages(t *testing.T, lines []string) []string {
	t.Helper()
	var out []string
	for _, l := range lines {
		var e struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatalf("events file line is not JSON: %q: %v", l, err)
		}
		if e.Type == "step" {
			out = append(out, e.Message)
		}
	}
	return out
}

// fileLines reads path and returns its lines, requiring every one to be
// complete valid JSON and the content to end in a newline.
func fileLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if len(raw) == 0 {
		return nil
	}
	if raw[len(raw)-1] != '\n' {
		t.Fatalf("%s ends in a partial line: %q", path, raw)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	for _, l := range lines {
		if !json.Valid([]byte(l)) {
			t.Fatalf("line is not valid JSON: %q", l)
		}
	}
	return lines
}

// stepLines keeps only the step events among lines, dropping the run_start and
// run_end the shared shell wraps around every run.
func stepLines(t *testing.T, lines []string) []string {
	t.Helper()
	var out []string
	for _, l := range lines {
		var ev struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			t.Fatalf("line is not JSON: %q", l)
		}
		if ev.Type == "step" {
			out = append(out, l)
		}
	}
	return out
}

// emitSteps emits n step events through the sink Main attached to Progress.
func emitSteps(d Deps, n int) {
	for i := 0; i < n; i++ {
		d.Progress.events.Emit(newStepEvent("analyse", fmt.Sprintf("step %d", i)))
	}
}

// TS-07-1 (unit): Common.Register adds --emit-events (default false) next to
// the other shared flags.
func TestTS07_1_RegisterAddsEmitEventsFlag(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	var c Common
	c.Register(fs)
	ev := fs.Lookup("emit-events")
	if ev == nil || ev.DefValue != "false" {
		t.Fatalf("--emit-events = %+v, want default false", ev)
	}
	for _, name := range []string{"verbose", "quiet", "show-text", "report-file"} {
		if fs.Lookup(name) == nil {
			t.Errorf("--%s missing from the shared flag set", name)
		}
	}
	if c.EmitEvents {
		t.Errorf("Common.EmitEvents = true, want false")
	}
}

// TS-07-2 (unit): passing the removed --events flag is a usage error with
// the rename message.
func TestTS07_2_RemovedEventsFlagIsUsageError(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var execCalled bool
	app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
		execCalled = true
		return ExitOK, nil, nil
	})
	code, stdout, stderr := eventsRun(t, app, []string{"--events", "yaml", "--version"})
	if code != ExitUsage {
		t.Fatalf("code = %d, want %d", code, ExitUsage)
	}
	var env Envelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is not an envelope: %v\n%s", err, stdout)
	}
	if env.Error == nil || env.Error.Stage != "usage" {
		t.Errorf("error = %+v, want stage usage", env.Error)
	}
	if !strings.Contains(env.Error.Message, "--events was renamed --emit-events") {
		t.Errorf("message = %q, want rename message", env.Error.Message)
	}
	if execCalled {
		t.Error("Exec ran with a removed --events flag")
	}
	if strings.Contains(stderr, `"type":`) {
		t.Errorf("stderr carries an event: %s", stderr)
	}
}

// TS-07-4 (integration): without --emit-events stderr stays free of JSON.
func TestTS07_4_DefaultModeKeepsStderrHuman(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		emitSteps(d, 2)
		return ExitOK, nil, nil
	})
	code, _, stderr := eventsRun(t, app, []string{"--dir", t.TempDir(), "x"})
	if code != ExitOK {
		t.Fatalf("code = %d\n%s", code, stderr)
	}
	if strings.Contains(stderr, `{"ts"`) {
		t.Errorf("stderr carries JSON without --emit-events: %s", stderr)
	}

	// The stream is not lost: the events file always carries it, from
	// run_start to run_end, with the steps the run emitted.
	lines := fileLines(t, stateEventsFile(t, state))
	types := eventTypesOfLines(t, lines)
	if len(types) < 4 || types[0] != "run_start" || types[len(types)-1] != "run_end" {
		t.Fatalf("events file types = %v, want run_start ... run_end", types)
	}
	if got := len(stepMessages(t, lines)); got != 2 {
		t.Errorf("the events file holds %d step events, want the 2 the run emitted", got)
	}
}

// --emit-events puts the stream on stderr.
func TestEmitEventsPutsStreamOnStderr(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		emitSteps(d, 2)
		return ExitOK, nil, nil
	})
	code, _, stderr := eventsRun(t, app, []string{"--dir", t.TempDir(), "--emit-events", "x"})
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(stderr, `"type":"step"`) {
		t.Errorf("stderr does not carry events under --emit-events: %s", stderr)
	}
}

// The removed --events-file flag is a usage error with the removal message.
func TestEventsFileFlagIsRemovedUsageError(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	app, _ := newApp(t, nil)
	env, code, _ := runApp(t, app, []string{"--events-file", filepath.Join(t.TempDir(), "no", "such", "dir.jsonl"), "x"}, "")
	if code != ExitUsage || env.Error == nil || env.Error.Stage != "usage" {
		t.Errorf("code %d error %+v, want a usage error", code, env.Error)
	}
	if !strings.Contains(env.Error.Message, "was removed") {
		t.Errorf("message = %q, want removal message", env.Error.Message)
	}
}

// TS-07-5 (unit): --quiet silences stderr under --emit-events.
func TestTS07_5_QuietSuppressesStderr(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		d.Progress.Step("preflight", "hello")
		emitSteps(d, 2)
		return ExitOK, nil, nil
	})
	code, _, stderr := eventsRun(t, app, []string{"--dir", t.TempDir(), "--quiet", "--emit-events", "x"})
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	if stderr != "" {
		t.Errorf("--emit-events --quiet: stderr = %q, want empty", stderr)
	}

	// --quiet silences stderr, not the file (12-REQ-5.2): it holds the full
	// stream, including the step the run emitted before emitSteps.
	lines := fileLines(t, stateEventsFile(t, state))
	types := eventTypesOfLines(t, lines)
	if len(types) < 5 || types[0] != "run_start" || types[len(types)-1] != "run_end" {
		t.Fatalf("under --quiet the events file types = %v, want run_start ... run_end", types)
	}
	steps := stepMessages(t, lines)
	if len(steps) != 3 || steps[0] != "hello" {
		t.Errorf("under --quiet the events file holds steps %q, want hello and the 2 emitted", steps)
	}
}

// TS-07-6 (integration): a run whose context is cancelled mid-stream leaves
// the events on stderr as exactly the lines emitted before, none partial.
func TestTS07_6_CancelledRunLeavesAValidPrefix(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const n = 5
	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		emitSteps(d, n)
		cancel()
		return ExitFailed, nil, &ErrorInfo{Stage: "analyse", Category: "internal", Message: "cancelled"}
	})
	var stdout, stderr bytes.Buffer
	app.Main(ctx, []string{"--dir", t.TempDir(), "--emit-events", "x"}, strings.NewReader(""), &stdout, &stderr)
	seen := stepLines(t, parseEventLinesAsStrings(t, stderr.String()))
	if len(seen) != n {
		t.Fatalf("step lines on stderr = %d, want %d", len(seen), n)
	}
	for i, l := range seen {
		var ev StepEvent
		if err := json.Unmarshal([]byte(l), &ev); err != nil || ev.Message != fmt.Sprintf("step %d", i) {
			t.Errorf("line %d = %q, want step %d", i, l, i)
		}
	}
}

// parseEventLinesAsStrings returns every JSON line in s as a string slice.
func parseEventLinesAsStrings(t *testing.T, s string) []string {
	t.Helper()
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if !strings.HasPrefix(l, "{") {
			continue
		}
		if !json.Valid([]byte(l)) {
			t.Fatalf("line is not valid JSON: %q", l)
		}
		out = append(out, l)
	}
	return out
}

// ---- 07 progress_event_stream: run_start / run_end (task 3) ----

// parseEventLines returns every JSON object line in s, in order.
func parseEventLines(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(s, "\n") {
		if !strings.HasPrefix(l, "{") {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			t.Fatalf("event line is not JSON: %q (%v)", l, err)
		}
		out = append(out, ev)
	}
	return out
}

// runCapturingEvents runs app under --emit-events and returns the events
// read from stderr, the envelope and the exit code.
func runCapturingEvents(t *testing.T, app *App, extra ...string) ([]map[string]any, Envelope, int) {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	argv := append([]string{"--dir", t.TempDir(), "--emit-events"}, extra...)
	argv = append(argv, "x")
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), argv, strings.NewReader(""), &stdout, &stderr)
	var env Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not one JSON object (%v):\n%s", err, stdout.String())
	}
	events := parseEventLines(t, stderr.String())
	if len(events) == 0 {
		t.Fatal("no events were written")
	}
	return events, env, code
}

func eventTypes(events []map[string]any) []string {
	var out []string
	for _, ev := range events {
		out = append(out, fmt.Sprint(ev["type"]))
	}
	return out
}

// TS-09-32 (integration): the run_start event carries schema_version under
// the same key and value as the envelope.
func TestTS09_32_RunStartCarriesSchemaVersion(t *testing.T) {
	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		return ExitOK, nil, nil
	})
	events, env, code := runCapturingEvents(t, app)
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	if len(events) == 0 || events[0]["type"] != "run_start" {
		t.Fatalf("events = %v, want run_start first", eventTypes(events))
	}
	if got := events[0]["schema_version"]; got != SchemaVersion {
		t.Errorf("run_start schema_version = %v, want %q", got, SchemaVersion)
	}
	if env.SchemaVersion != SchemaVersion {
		t.Errorf("envelope schema_version = %q, want %q", env.SchemaVersion, SchemaVersion)
	}
}

// TS-07-12 (unit): run_start is written right after the model is resolved,
// before the first phase_start, carrying input_kind and model.
func TestTS07_12_RunStartRightAfterTheModelIsResolved(t *testing.T) {
	app, seen := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		// What a pipeline's first phase would emit: run_start must precede it.
		d.Progress.events.Emit(newPhaseStartEvent("analyse", "", 10, 1))
		return ExitOK, nil, nil
	})
	events, _, code := runCapturingEvents(t, app)
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	if len(events) < 2 || events[0]["type"] != "run_start" {
		t.Fatalf("events = %v, want run_start first", eventTypes(events))
	}
	if events[1]["type"] != "phase_start" {
		t.Errorf("second event = %v, want the first phase_start", events[1]["type"])
	}
	start := events[0]
	if start["input_kind"] != seen.Input.Kind.String() {
		t.Errorf("input_kind = %v, want %s", start["input_kind"], seen.Input.Kind)
	}
	model, ok := start["model"].(map[string]any)
	if !ok {
		t.Fatalf("model = %v, want an object", start["model"])
	}
	if model["id"] != seen.Model.Model.ID || model["vendor"] != seen.Model.Model.Provider || model["spec"] != seen.Model.Spec {
		t.Errorf("model = %v, want spec %q id %q vendor %q", model, seen.Model.Spec, seen.Model.Model.ID, seen.Model.Model.Provider)
	}
	if len(model) != 3 {
		t.Errorf("model has keys beyond spec/id/vendor: %v", model)
	}
}

// TS-07-13 (unit): run_end is the last event and carries the exit code and
// status the envelope carries.
func TestTS07_13_RunEndImmediatelyBeforeTheEnvelope(t *testing.T) {
	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		emitSteps(d, 2)
		return ExitFailed, nil, &ErrorInfo{Stage: "analyse", Category: "internal", Message: "boom"}
	})
	events, env, code := runCapturingEvents(t, app)
	last := events[len(events)-1]
	if last["type"] != "run_end" {
		t.Fatalf("last event = %v, want run_end: %v", last["type"], eventTypes(events))
	}
	if int(last["exit_code"].(float64)) != code || code != ExitFailed {
		t.Errorf("run_end.exit_code = %v, exit code %d", last["exit_code"], code)
	}
	if last["status"] != env.Status || last["status"] != "failed" {
		t.Errorf("run_end.status = %v, envelope status %q", last["status"], env.Status)
	}
	if len(last) != 7 { // ts, tool, session_id, type, status, exit_code, report_file
		t.Errorf("run_end carries unexpected fields: %v", last)
	}
}

// run_end lands before a byte of the envelope: the stdout writer sees an
// already-complete stream.
func TestTS07_13_RunEndIsWrittenBeforeStdout(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	app, _ := newApp(t, nil)
	var stderr bytes.Buffer
	var atFirstWrite string
	stdout := writerFunc(func(p []byte) (int, error) {
		if atFirstWrite == "" {
			atFirstWrite = stderr.String()
		}
		return len(p), nil
	})
	app.Main(context.Background(), []string{"--dir", t.TempDir(), "--emit-events", "x"}, strings.NewReader(""), stdout, &stderr)
	events := parseEventLines(t, atFirstWrite)
	if len(events) == 0 || events[len(events)-1]["type"] != "run_end" {
		t.Errorf("events before the envelope = %v, want a stream ending in run_end", eventTypes(events))
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// TS-07-14 (unit): neither event is emitted on -h/--help or on a bare
// invocation on a terminal; and a bare invocation whose stdout is not a
// terminal emits its usage envelope but still no event, because no sink has
// been built by then.
func TestTS07_14_NoRunStartOrRunEndOnHumanDrivenPaths(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	app, _ := newApp(t, nil)
	tty, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer tty.Close()

	for _, argv := range [][]string{
		{"--emit-events", "-h"},
		{"--emit-events"},
	} {
		var stdout, stderr bytes.Buffer
		app.Main(context.Background(), argv, strings.NewReader(""), &stdout, &stderr)
		if n := len(parseEventLines(t, stderr.String())); n != 0 {
			t.Errorf("%v: %d event lines on stderr, want 0", argv, n)
		}
		if argv[len(argv)-1] == "-h" && stdout.Len() != 0 {
			t.Errorf("-h wrote to stdout: %s", stdout.String())
		}
	}

	// A bare invocation on a terminal writes nothing to stdout either.
	var stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--emit-events"}, strings.NewReader(""), tty, &stderr)
	if code != ExitUsage {
		t.Errorf("bare invocation on a terminal: code %d, want %d", code, ExitUsage)
	}
	if n := len(parseEventLines(t, stderr.String())); n != 0 {
		t.Errorf("bare invocation on a terminal: %d event lines, want 0", n)
	}

}

// TS-07-15 (property): run_end.status is read off the exit code that decides
// the envelope's ok field, for every exit code.
func TestTS07_15_RunEndStatusMatchesEnvelopeForEveryExitCode(t *testing.T) {
	for _, code := range []int{ExitOK, ExitFailed, ExitUsage, ExitNeedsHuman, ExitUnverified} {
		code := code
		t.Run(StatusFor(code), func(t *testing.T) {
			app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
				if code == ExitOK {
					return code, nil, nil
				}
				return code, nil, &ErrorInfo{Stage: "analyse", Category: "internal", Message: "stop"}
			})
			events, env, got := runCapturingEvents(t, app)
			if got != code {
				t.Fatalf("exit code = %d, want %d", got, code)
			}
			end := events[len(events)-1]
			if end["type"] != "run_end" {
				t.Fatalf("last event = %v, want run_end", end["type"])
			}
			if (end["status"] == "done") != env.OK {
				t.Errorf("run_end.status = %v but envelope ok = %v", end["status"], env.OK)
			}
			if end["status"] != env.Status || end["status"] != StatusFor(code) {
				t.Errorf("run_end.status = %v, envelope status %q, want %q", end["status"], env.Status, StatusFor(code))
			}
			if int(end["exit_code"].(float64)) != env.ExitCode {
				t.Errorf("run_end.exit_code = %v, envelope %d", end["exit_code"], env.ExitCode)
			}
		})
	}
}

// TS-07-16 (property): across runs of varying length and outcome, run_start is
// the first event, exactly once, and run_end is the last.
func TestTS07_16_RunStartFirstAndRunEndLast(t *testing.T) {
	fail := func(code int) (int, any, *ErrorInfo) {
		return code, nil, &ErrorInfo{Stage: "verify", Category: "internal", Message: "stop"}
	}
	scenarios := []struct {
		name string
		exec func(context.Context, Deps) (int, any, *ErrorInfo)
	}{
		{"empty success", func(context.Context, Deps) (int, any, *ErrorInfo) { return ExitOK, nil, nil }},
		{"long success", func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
			emitSteps(d, 50)
			d.Progress.events.Emit(newPhaseStartEvent("implement", "1", 5, 1))
			d.Progress.events.Emit(newPhaseEndEvent("implement", "end_turn", 3, 0.1, 10))
			return ExitOK, nil, nil
		}},
		{"failure", func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
			emitSteps(d, 3)
			return fail(ExitFailed)
		}},
		{"blocked", func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
			emitSteps(d, 1)
			return fail(ExitNeedsHuman)
		}},
		{"unverified", func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
			emitSteps(d, 7)
			return fail(ExitUnverified)
		}},
	}
	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			app, _ := newApp(t, sc.exec)
			events, _, _ := runCapturingEvents(t, app)
			types := eventTypes(events)
			if len(types) < 2 || types[0] != "run_start" || types[len(types)-1] != "run_end" {
				t.Fatalf("events = %v, want run_start first and run_end last", types)
			}
			starts, ends := 0, 0
			for _, ty := range types {
				switch ty {
				case "run_start":
					starts++
				case "run_end":
					ends++
				}
			}
			if starts != 1 || ends != 1 {
				t.Errorf("run_start x%d, run_end x%d, want one of each", starts, ends)
			}
		})
	}
}

// 07-REQ-3.5: run_start is the first event even when a warning is recorded
// before the model is resolved — an events file that could not be created, an
// input cut at MaxInputBytes. The warnings are not lost; they follow run_start
// in the order they were recorded.
func TestTS07_16_RunStartFirstWhenAWarningPrecedesModelResolution(t *testing.T) {
	scenarios := []struct {
		name  string
		setup func(t *testing.T) (stdin string)
		code  WarnCode
	}{
		{"unwritable events directory", func(t *testing.T) string {
			// A state home that is a file: agent-fox/events cannot be created.
			f := filepath.Join(t.TempDir(), "state")
			if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("XDG_STATE_HOME", f)
			return ""
		}, WarnEventsFileNotWritten},
		{"input cut at the byte ceiling", func(t *testing.T) string {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			return strings.Repeat("a line of log output\n", MaxInputBytes/10)
		}, WarnInputTruncated},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			t.Setenv("ANTHROPIC_API_KEY", "test-key")
			stdin := sc.setup(t)
			app, _ := newApp(t, nil)
			argv := []string{"--dir", t.TempDir(), "--emit-events", "-"}
			if stdin == "" {
				argv, stdin = []string{"--dir", t.TempDir(), "--emit-events", "x"}, ""
			}
			var stdout, stderr bytes.Buffer
			app.Main(context.Background(), argv, strings.NewReader(stdin), &stdout, &stderr)

			events := parseEventLines(t, stderr.String())
			types := eventTypes(events)
			if len(types) < 3 || types[0] != "run_start" || types[len(types)-1] != "run_end" {
				t.Fatalf("events = %v, want run_start first and run_end last", types)
			}
			var warned bool
			for _, ev := range events {
				if ev["type"] == "warning" && ev["code"] == string(sc.code) {
					warned = true
				}
			}
			if !warned {
				t.Errorf("the %s warning was lost from the stream: %v", sc.code, types)
			}
		})
	}
}

// slowResult takes a few milliseconds to encode, as a large result does. It is
// encoded while the report file is written, which is after the report
// envelope is built and before the stdout envelope is.
type slowResult struct{}

func (slowResult) MarshalJSON() ([]byte, error) {
	time.Sleep(5 * time.Millisecond)
	return []byte(`{"stage":"done"}`), nil
}

// 06-REQ-2.8: under --detail full the report file is byte for byte what is
// printed to stdout, so duration_ms is the one the report file carries, not a
// second reading of the clock taken after the file was written.
func TestReportFileIsByteIdenticalToDetailFullStdout(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	report := filepath.Join(t.TempDir(), "report.json")
	app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
		return ExitOK, slowResult{}, nil
	})

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(),
		[]string{"--dir", t.TempDir(), "--detail", "full", "--report-file", report, "x"},
		strings.NewReader(""), &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	file, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(file, []byte(`"duration_ms"`)) {
		t.Fatalf("the report has no duration_ms:\n%s", file)
	}
	if !bytes.Equal(file, stdout.Bytes()) {
		t.Errorf("the report file differs from --detail full stdout:\nfile:\n%s\nstdout:\n%s", file, stdout.Bytes())
	}
}

// 08-REQ-3.1: a copy that does not land is visible as an output_not_written
// warning. --output is validated once, in Main; if the path has become a
// directory by the time the envelope is emitted, emit does not re-validate it
// away and drop the copy silently — the write fails and is reported.
func TestOutputThatBecameADirectoryIsReportedNotDropped(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	out := filepath.Join(t.TempDir(), "result.json")
	app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
		// Validation has passed; now the path turns into a directory.
		if err := os.MkdirAll(out, 0o755); err != nil {
			t.Fatal(err)
		}
		return ExitOK, map[string]string{"stage": "done"}, nil
	})

	env, code, _ := runApp(t, app, []string{"--dir", t.TempDir(), "--output", out, "x"}, "")
	if code != ExitOK || !env.OK {
		t.Fatalf("code = %d, ok = %v: a copy that did not land must not change the outcome", code, env.OK)
	}
	var warned bool
	for _, w := range env.Warnings {
		if w.Code == WarnOutputNotWritten {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no %s warning for a copy that could not be written: %+v", WarnOutputNotWritten, env.Warnings)
	}
	if info, err := os.Stat(out); err != nil || !info.IsDir() {
		t.Errorf("the directory at --output was disturbed: %v %v", info, err)
	}
}

func TestMain(m *testing.M) {
	// The tests above resolve a model, and a stray credential variable in the
	// developer's shell would change which vendor they resolve against.
	for _, v := range []string{"AF_MODEL", "AGENTKIT_MODEL", "AF_MODEL_VENDOR",
		"AF_MODEL_EFFORT",
		"CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_BEDROCK",
		"ANTHROPIC_VERTEX_PROJECT_ID", "ANTHROPIC_VERTEX_BASE_URL"} {
		_ = os.Unsetenv(v)
	}
	os.Exit(statetest.Run(m))
}
