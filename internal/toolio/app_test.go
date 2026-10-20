package toolio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// newTestApp returns an app with a fixed identity.
func newTestApp() *App {
	return &App{Version: "v0.0.0-test"}
}

// invoke runs the app with "af" as argv[0] followed by args, and returns the
// exit code and what the app wrote to stdout and stderr.
func invoke(t *testing.T, app *App, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = app.Main(context.Background(), append([]string{"af"}, args...), strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

// returning is a test command that ends with out and err.
func returning(path string, out Outcome, err error) Command {
	return Command{Path: path, Run: func(*Run) (Outcome, error) { return out, err }}
}

// decode parses stdout as exactly one JSON object.
func decode(t *testing.T, stdout string) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, stdout)
	}
	return env
}

// at follows path through nested JSON objects and returns what is there, or
// nil.
func at(m map[string]any, path ...string) any {
	var v any = m
	for _, p := range path {
		obj, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = obj[p]
	}
	return v
}

// TestTimestampsAndDefaultUsage_TS_05_12 verifies TS-05-12:
// Timestamps are RFC 3339 UTC with milliseconds and usage defaults to the
// no-brain value.
// Verifies: 05-REQ-2.4
func TestTimestampsAndDefaultUsage_TS_05_12(t *testing.T) {
	// A zone other than UTC proves the shell converts.
	start := time.Date(2026, 10, 20, 8, 26, 48, 123_000_000, time.FixedZone("plus1", 3600))
	calls := 0
	app := newTestApp()
	app.Now = func() time.Time {
		calls++
		if calls == 1 {
			return start
		}
		return start.Add(250 * time.Millisecond)
	}
	app.Register(returning("X", Outcome{Status: StatusDone, Summary: "ok"}, nil))

	_, stdout, _ := invoke(t, app, "X")
	env := decode(t, stdout)

	shape := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)
	for _, key := range []string{"started_at", "finished_at"} {
		if s, _ := env[key].(string); !shape.MatchString(s) {
			t.Errorf("%s = %q, want RFC 3339 UTC with milliseconds", key, env[key])
		}
	}
	if got := env["started_at"]; got != "2026-10-20T07:26:48.123Z" {
		t.Errorf("started_at = %v, want 2026-10-20T07:26:48.123Z", got)
	}
	if got := env["finished_at"]; got != "2026-10-20T07:26:48.373Z" {
		t.Errorf("finished_at = %v, want 2026-10-20T07:26:48.373Z", got)
	}
	if !strings.Contains(stdout, "\"duration_ms\": 250\n") {
		t.Errorf("duration_ms is not the integer 250:\n%s", stdout)
	}

	want := map[string]any{
		"brain": "", "cost_usd": float64(0), "cost_source": "none",
		"input_tokens": float64(0), "output_tokens": float64(0), "turns": float64(0),
	}
	if got := env["usage"]; !reflect.DeepEqual(got, want) {
		t.Errorf("usage = %v, want %v", got, want)
	}
}

// failingWriter is a stdout that always fails.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// TestFailingStdoutKeepsExitCode_TS_05_15 verifies TS-05-15:
// A failing stdout write does not change the exit code.
// Verifies: 05-REQ-2.7
func TestFailingStdoutKeepsExitCode_TS_05_15(t *testing.T) {
	cases := []struct {
		name string
		out  Outcome
		err  error
		want int
	}{
		{"done", Outcome{Status: StatusDone}, nil, 0},
		{"needs_human", Outcome{Status: StatusNeedsHuman, NeedsHuman: &Question{Text: "which?"}}, nil, 3},
		{"failed", Outcome{}, &Failure{Category: CategoryForge, Message: "m"}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp()
			app.Register(returning("X", tc.out, tc.err))
			code := app.Main(context.Background(), []string{"af", "X"}, strings.NewReader(""), failingWriter{}, io.Discard)
			if code != tc.want {
				t.Errorf("exit code = %d, want %d", code, tc.want)
			}
		})
	}
}

// TestOutcomeIsCopiedIntoEnvelope_TS_05_16 verifies TS-05-16:
// An outcome's status, summary and result are copied into the envelope.
// Verifies: 05-REQ-3.1
func TestOutcomeIsCopiedIntoEnvelope_TS_05_16(t *testing.T) {
	cases := []struct {
		name   string
		out    Outcome
		warn   bool
		status string
		code   int
	}{
		{"done", Outcome{Status: StatusDone}, false, "done", 0},
		{"unverified", Outcome{Status: StatusUnverified}, true, "unverified", 4},
		{"needs_human", Outcome{Status: StatusNeedsHuman, NeedsHuman: &Question{Text: "which?"}}, false, "needs_human", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := tc.out
			out.Summary = "ok"
			out.Result = map[string]any{"n": 1}
			app := newTestApp()
			app.Register(Command{Path: "X", Run: func(r *Run) (Outcome, error) {
				if tc.warn {
					r.Warn("not_verified", "the tests were not run")
				}
				return out, nil
			}})

			code, stdout, _ := invoke(t, app, "X")
			env := decode(t, stdout)

			if code != tc.code {
				t.Errorf("exit code = %d, want %d", code, tc.code)
			}
			if env["status"] != tc.status {
				t.Errorf("status = %v, want %s", env["status"], tc.status)
			}
			if env["summary"] != "ok" {
				t.Errorf("summary = %v, want ok", env["summary"])
			}
			if got, want := env["result"], map[string]any{"n": float64(1)}; !reflect.DeepEqual(got, want) {
				t.Errorf("result = %v, want %v", got, want)
			}
			if env["error"] != nil {
				t.Errorf("error = %v, want null", env["error"])
			}
			if tc.warn {
				want := []any{map[string]any{"code": "not_verified", "message": "the tests were not run"}}
				if got := env["warnings"]; !reflect.DeepEqual(got, want) {
					t.Errorf("warnings = %v, want %v", got, want)
				}
			}
		})
	}
}

// TestFailureBecomesErrorObject_TS_05_17 verifies TS-05-17:
// A *Failure becomes the error object, with status by category.
// Verifies: 05-REQ-3.2
func TestFailureBecomesErrorObject_TS_05_17(t *testing.T) {
	forge := &Failure{Category: CategoryForge, Message: "m", Retryable: true, FixHint: &FixHint{Env: "X"}}
	cases := []struct {
		name   string
		err    error
		status string
		code   int
		want   map[string]any
	}{
		{
			"forge", forge, "failed", 1,
			map[string]any{"category": "forge", "message": "m", "retryable": true, "fix_hint": map[string]any{"env": "X"}},
		},
		{
			"wrapped", fmt.Errorf("while reading: %w", forge), "failed", 1,
			map[string]any{"category": "forge", "message": "while reading: m", "retryable": true, "fix_hint": map[string]any{"env": "X"}},
		},
		{
			"usage", &Failure{Category: CategoryUsage, Message: "bad flag"}, "usage", 2,
			map[string]any{"category": "usage", "message": "bad flag", "retryable": false, "fix_hint": nil},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp()
			app.Register(returning("X", Outcome{}, tc.err))

			code, stdout, _ := invoke(t, app, "X")
			env := decode(t, stdout)

			if code != tc.code || env["status"] != tc.status {
				t.Errorf("exit code, status = %d, %v; want %d, %s", code, env["status"], tc.code, tc.status)
			}
			if got := env["error"]; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("error = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPlainErrorBecomesInternal_TS_05_18 verifies TS-05-18:
// A plain error becomes failed/internal with the error text.
// Verifies: 05-REQ-3.3
func TestPlainErrorBecomesInternal_TS_05_18(t *testing.T) {
	app := newTestApp()
	app.Register(returning("X", Outcome{}, errors.New("disk on fire")))

	code, stdout, _ := invoke(t, app, "X")
	env := decode(t, stdout)

	if code != 1 || env["status"] != "failed" {
		t.Errorf("exit code, status = %d, %v; want 1, failed", code, env["status"])
	}
	if got := at(env, "error", "category"); got != CategoryInternal {
		t.Errorf("error.category = %v, want internal", got)
	}
	if got, _ := at(env, "error", "message").(string); !strings.Contains(got, "disk on fire") {
		t.Errorf("error.message = %q, want it to contain the error text", got)
	}
	if got := at(env, "error", "retryable"); got != false {
		t.Errorf("error.retryable = %v, want false", got)
	}
}

// TestPanicIsRecovered_TS_05_19 verifies TS-05-19:
// A panic is recovered, its stack written to stderr, and one envelope
// printed.
// Verifies: 05-REQ-3.4
func TestPanicIsRecovered_TS_05_19(t *testing.T) {
	app := newTestApp()
	app.Register(Command{Path: "X", Run: func(*Run) (Outcome, error) { panic("boom") }})

	code, stdout, stderr := invoke(t, app, "X")
	env := decode(t, stdout) // exactly one envelope

	if code != 1 || env["status"] != "failed" {
		t.Errorf("exit code, status = %d, %v; want 1, failed", code, env["status"])
	}
	if got := at(env, "error", "category"); got != CategoryInternal {
		t.Errorf("error.category = %v, want internal", got)
	}
	if !strings.Contains(stderr, "boom") || !strings.Contains(stderr, "goroutine") {
		t.Errorf("stderr should hold the panic and its stack, got:\n%s", stderr)
	}
}

// TestCategoryConstants_TS_05_22 verifies TS-05-22:
// The seven failure categories are exported constants with the documented
// strings, and the failures the shell sets report one of them.
// Verifies: 05-REQ-3.7
func TestCategoryConstants_TS_05_22(t *testing.T) {
	got := []string{CategoryUsage, CategoryAuth, CategoryForge, CategoryIO, CategoryCancelled, CategoryTimeout, CategoryInternal}
	want := []string{"usage", "auth", "forge", "io", "cancelled", "timeout", "internal"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("categories = %v, want %v", got, want)
	}

	app := newTestApp()
	app.Register(Command{Path: "panics", Run: func(*Run) (Outcome, error) { panic("boom") }})
	_, stdout, _ := invoke(t, app, "panics")
	if c := at(decode(t, stdout), "error", "category"); c != CategoryInternal {
		t.Errorf("panic category = %v, want %s", c, CategoryInternal)
	}
	_, stdout, _ = invoke(t, app, "nosuch")
	if c := at(decode(t, stdout), "error", "category"); c != CategoryUsage {
		t.Errorf("unknown command category = %v, want %s", c, CategoryUsage)
	}
}
