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

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
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
		{"impl", "label", []string{"issue"}},
		{"fix", "specs-dir", []string{"spec", "impl"}},
		{"issue", "land", []string{"fix", "impl"}},
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

// ---- 07 progress_event_stream: --events / --events-file (task 2) ----

// eventsRun runs app with argv and returns the exit code, stdout and stderr.
func eventsRun(t *testing.T, app *App, argv []string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), argv, strings.NewReader(""), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
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

// emitSteps emits n step events through the sink Main attached to Progress.
func emitSteps(d Deps, n int) {
	for i := 0; i < n; i++ {
		d.Progress.events.Emit(newStepEvent("analyse", fmt.Sprintf("step %d", i)))
	}
}

// TS-07-1 (unit): Common.Register adds --events (default text) and
// --events-file next to the other shared flags.
func TestTS07_1_RegisterAddsEventsFlags(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	var c Common
	c.Register(fs)
	ev := fs.Lookup("events")
	if ev == nil || ev.DefValue != "text" {
		t.Fatalf("--events = %+v, want default text", ev)
	}
	ef := fs.Lookup("events-file")
	if ef == nil || ef.DefValue != "" {
		t.Fatalf("--events-file = %+v, want default empty", ef)
	}
	for _, name := range []string{"verbose", "quiet", "show-text", "report-file"} {
		if fs.Lookup(name) == nil {
			t.Errorf("--%s missing from the shared flag set", name)
		}
	}
	if c.Events != "text" {
		t.Errorf("Common.Events = %q, want text", c.Events)
	}
}

// TS-07-2 (unit): an invalid --events value is a usage error before anything
// is fetched or resolved, and nothing is written as an event.
func TestTS07_2_InvalidEventsIsUsageErrorBeforeAnything(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var execCalled bool
	app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
		execCalled = true
		return ExitOK, nil, nil
	})
	eventsPath := filepath.Join(t.TempDir(), "run.jsonl")
	// --version would exit 0, so a usage envelope here shows the check came
	// first.
	code, stdout, stderr := eventsRun(t, app, []string{"--events", "yaml", "--events-file", eventsPath, "--version"})
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
	if execCalled {
		t.Error("Exec ran with an invalid --events")
	}
	if strings.Contains(stderr, `"type":`) {
		t.Errorf("stderr carries an event: %s", stderr)
	}
	if _, err := os.Stat(eventsPath); err == nil {
		t.Error("the events file was opened despite the usage error")
	}

	// A bare invocation with a bad --events is still the events error.
	code, stdout, _ = eventsRun(t, app, []string{"--events", "yaml"})
	if code != ExitUsage || strings.Contains(stdout, NoInputMessage) {
		t.Errorf("bare invocation: code %d, stdout %s; want the --events error", code, stdout)
	}
}

// TS-07-3 (integration): --events-file is opened once, truncated, and each
// line is visible before the run exits.
func TestTS07_3_EventsFileTruncatedAndFlushedPerLine(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "run.jsonl")
	if err := os.WriteFile(path, []byte("stale content that is long enough to survive a short write"), 0o644); err != nil {
		t.Fatal(err)
	}
	var midRun []string
	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		emitSteps(d, 3)
		// Read while the run is still going: every line must already be there.
		midRun = fileLines(t, path)
		return ExitOK, nil, nil
	})
	code, _, stderr := eventsRun(t, app, []string{"--dir", t.TempDir(), "--events-file", path, "x"})
	if code != ExitOK {
		t.Fatalf("code = %d\n%s", code, stderr)
	}
	if len(midRun) != 3 {
		t.Fatalf("lines visible mid-run = %d, want 3: %v", len(midRun), midRun)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "stale") {
		t.Errorf("stale content survived: %s", raw)
	}
}

// TS-07-4 (integration): under --events text the file carries the JSONL stream
// and stderr stays free of JSON.
func TestTS07_4_EventsFileWithTextKeepsStderrHuman(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "run.jsonl")
	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		emitSteps(d, 2)
		return ExitOK, nil, nil
	})
	code, _, stderr := eventsRun(t, app, []string{"--dir", t.TempDir(), "--events", "text", "--events-file", path, "x"})
	if code != ExitOK {
		t.Fatalf("code = %d\n%s", code, stderr)
	}
	if strings.Contains(stderr, `{"ts"`) {
		t.Errorf("stderr carries JSON under --events text: %s", stderr)
	}
	lines := fileLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("events file has %d lines, want the 2 emitted events", len(lines))
	}
	for _, l := range lines {
		var ev map[string]any
		if err := json.Unmarshal([]byte(l), &ev); err != nil || ev["type"] != "step" || ev["tool"] != "tool" {
			t.Errorf("bad event line %q (%v)", l, err)
		}
	}
}

// --events jsonl puts the stream on stderr as well as in the file.
func TestEventsJSONLWritesStderrAndFile(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "run.jsonl")
	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		emitSteps(d, 2)
		return ExitOK, nil, nil
	})
	code, _, stderr := eventsRun(t, app, []string{"--dir", t.TempDir(), "--events", "jsonl", "--events-file", path, "x"})
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	file, _ := os.ReadFile(path)
	if len(file) == 0 || !strings.Contains(stderr, string(file)) {
		t.Errorf("stderr does not carry the file's stream.\nstderr: %s\nfile: %s", stderr, file)
	}
}

// An events file that cannot be opened is a usage error, not a silent loss.
func TestEventsFileThatCannotBeOpenedIsAUsageError(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	app, _ := newApp(t, nil)
	env, code, _ := runApp(t, app, []string{"--events-file", filepath.Join(t.TempDir(), "no", "such", "dir.jsonl"), "x"}, "")
	if code != ExitUsage || env.Error == nil || env.Error.Stage != "usage" {
		t.Errorf("code %d error %+v, want a usage error", code, env.Error)
	}
}

// TS-07-5 (unit): --quiet silences stderr under both --events values and
// leaves the events file whole.
func TestTS07_5_QuietSuppressesStderrNotTheFile(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, mode := range []string{"jsonl", "text"} {
		path := filepath.Join(t.TempDir(), "run.jsonl")
		app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
			d.Progress.Step("hello")
			emitSteps(d, 2)
			return ExitOK, nil, nil
		})
		code, _, stderr := eventsRun(t, app, []string{"--dir", t.TempDir(), "--quiet", "--events", mode, "--events-file", path, "x"})
		if code != ExitOK {
			t.Fatalf("%s: code = %d", mode, code)
		}
		if stderr != "" {
			t.Errorf("--events %s --quiet: stderr = %q, want empty", mode, stderr)
		}
		if n := len(fileLines(t, path)); n != 2 {
			t.Errorf("--events %s --quiet: events file has %d lines, want the full stream (2)", mode, n)
		}
	}
}

// TS-07-6 (integration): a run whose context is cancelled mid-stream leaves
// the events file as exactly the lines emitted before, none partial.
func TestTS07_6_CancelledRunLeavesAValidPrefix(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "run.jsonl")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const n = 5
	var seen []string
	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		emitSteps(d, n)
		cancel()
		seen = fileLines(t, path)
		return ExitFailed, nil, &ErrorInfo{Stage: "analyse", Category: "internal", Message: "cancelled"}
	})
	var stdout, stderr bytes.Buffer
	app.Main(ctx, []string{"--dir", t.TempDir(), "--events-file", path, "x"}, strings.NewReader(""), &stdout, &stderr)
	if len(seen) != n {
		t.Fatalf("lines at cancellation = %d, want %d", len(seen), n)
	}
	for i, l := range seen {
		var ev StepEvent
		if err := json.Unmarshal([]byte(l), &ev); err != nil || ev.Message != fmt.Sprintf("step %d", i) {
			t.Errorf("line %d = %q, want step %d", i, l, i)
		}
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
