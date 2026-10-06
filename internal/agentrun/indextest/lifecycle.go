package indextest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// Inner is what replaces an entry point's Exec in a lifecycle test: it gets the
// Deps the shell built, whose Index is the run's one index (nil when none could
// be built) and whose Runner carries it.
type Inner = func(context.Context, toolio.Deps) (int, any, *toolio.ErrorInfo)

// Harness is one cmd/ entry point, as the lifecycle tests see it.
type Harness struct {
	// NewApp is the entry point's newApp.
	NewApp func() toolio.App
	// Setup prepares the environment and a workspace and returns the argv of
	// an ordinary run, without --preflight.
	Setup func(t *testing.T) []string
}

// result is a decoded envelope and the exit code that came with it.
type result struct {
	code int
	env  map[string]any
}

func (r result) warning(code, severity string) bool {
	ws, _ := r.env["warnings"].([]any)
	for _, w := range ws {
		m, _ := w.(map[string]any)
		if m["code"] == code && m["severity"] == severity {
			return true
		}
	}
	return false
}

func (h Harness) factory(t *testing.T, f *Factory) {
	t.Helper()
	t.Cleanup(toolio.SetIndexBuilder(f.New))
}

// run drives the real shell. A non-nil inner replaces Exec; nil leaves the
// real closures, and preflight selects PreflightExec.
func (h Harness) run(t *testing.T, ctx context.Context, argv []string, inner Inner, preflight bool) result {
	t.Helper()
	app := h.NewApp()
	if inner != nil {
		app.Exec = inner
	}
	if preflight {
		argv = append([]string{"--preflight"}, argv...)
	}
	var stdout, stderr bytes.Buffer
	code := app.Main(ctx, argv, strings.NewReader(""), &stdout, &stderr)
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	return result{code: code, env: env}
}

func ok(context.Context, toolio.Deps) (int, any, *toolio.ErrorInfo) {
	return toolio.ExitOK, nil, nil
}

// TS10 is TS-16-10: Close is called exactly once when the run succeeds.
func TS10(t *testing.T, h Harness) {
	argv := h.Setup(t)

	f := &Factory{}
	h.factory(t, f)
	h.run(t, context.Background(), argv, ok, false)
	if f.Index == nil || f.Index.CloseCalls() != 1 {
		t.Errorf("ordinary run: Close calls = %v, want 1", f.Index)
	}

	// --preflight builds and closes the index through the real closure too.
	f = &Factory{}
	h.factory(t, f)
	r := h.run(t, context.Background(), argv, nil, true)
	if r.code != toolio.ExitOK {
		t.Fatalf("--preflight exited %d: %v", r.code, r.env)
	}
	if f.NewCalls() != 1 || f.Index == nil || f.Index.CloseCalls() != 1 {
		t.Errorf("--preflight: New calls = %d, Close calls = %v, want 1 and 1", f.NewCalls(), f.Index)
	}
}

// TS11 is TS-16-11: Close is called exactly once when the run fails.
func TS11(t *testing.T, h Harness) {
	argv := h.Setup(t)
	f := &Factory{}
	h.factory(t, f)
	r := h.run(t, context.Background(), argv, func(context.Context, toolio.Deps) (int, any, *toolio.ErrorInfo) {
		return toolio.ExitFailed, nil, &toolio.ErrorInfo{Stage: "run", Category: "internal", Message: "boom"}
	}, false)
	if r.code != toolio.ExitFailed {
		t.Errorf("exit = %d, want %d", r.code, toolio.ExitFailed)
	}
	if f.Index == nil || f.Index.CloseCalls() != 1 {
		t.Errorf("Close calls = %v, want 1", f.Index)
	}
}

// TS12 is TS-16-12: Close is called exactly once when the context is
// cancelled during the run.
func TS12(t *testing.T, h Harness) {
	argv := h.Setup(t)
	f := &Factory{}
	h.factory(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.run(t, ctx, argv, func(ctx context.Context, _ toolio.Deps) (int, any, *toolio.ErrorInfo) {
		cancel()
		<-ctx.Done()
		return toolio.ExitFailed, nil, &toolio.ErrorInfo{Stage: "run", Category: "cancelled", Message: ctx.Err().Error()}
	}, false)
	if f.Index == nil || f.Index.CloseCalls() != 1 {
		t.Errorf("Close calls = %v, want 1", f.Index)
	}
}

// TS13 is TS-16-13: when the index cannot be built the run goes on without
// one and says so with a low code_search_unavailable warning.
func TS13(t *testing.T, h Harness, errUnsupported error) {
	argv := h.Setup(t)
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"unsupported", errUnsupported},
		{"build failure", errors.New("disk full")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &Factory{Err: tc.err}
			h.factory(t, f)
			ran := false
			r := h.run(t, context.Background(), argv, func(_ context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
				ran = true
				if d.Index != nil {
					t.Errorf("index = %v, want nil", d.Index)
				}
				if d.Runner == nil {
					t.Error("no Runner: the run must continue without code_search")
				}
				return toolio.ExitOK, nil, nil
			}, false)
			if !ran || r.code != toolio.ExitOK {
				t.Errorf("ran = %v, exit = %d: the run must proceed", ran, r.code)
			}
			if !r.warning("code_search_unavailable", "low") {
				t.Errorf("no low code_search_unavailable warning: %v", r.env["warnings"])
			}

			// --preflight takes the same path through the real closure.
			f = &Factory{Err: tc.err}
			h.factory(t, f)
			r = h.run(t, context.Background(), argv, nil, true)
			if r.code != toolio.ExitOK {
				t.Fatalf("--preflight exited %d: %v", r.code, r.env)
			}
			if !r.warning("code_search_unavailable", "low") {
				t.Errorf("--preflight: no low code_search_unavailable warning: %v", r.env["warnings"])
			}
		})
	}

	// A build that works must not warn.
	f := &Factory{}
	h.factory(t, f)
	if r := h.run(t, context.Background(), argv, ok, false); r.warning("code_search_unavailable", "low") {
		t.Errorf("a built index produced a warning: %v", r.env["warnings"])
	}
}

// TS14 is TS-16-14: the index is built once, for the run's workspace, before
// the Runner, and the one instance reaches the pipeline.
func TS14(t *testing.T, h Harness) {
	argv := h.Setup(t)
	f := &Factory{}
	h.factory(t, f)
	var seen []tools.Index
	var root string
	h.run(t, context.Background(), argv, func(_ context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
		seen = append(seen, d.Index)
		if d.Runner == nil {
			t.Error("no Runner")
		}
		root = d.Workspace.Root
		return toolio.ExitOK, nil, nil
	}, false)
	if f.NewCalls() != 1 {
		t.Fatalf("New calls = %d, want 1", f.NewCalls())
	}
	if len(seen) != 1 || seen[0] != tools.Index(f.Index) {
		t.Errorf("the pipeline got %v, want the one built index %v", seen, f.Index)
	}
	if ws := f.Workspaces(); len(ws) != 1 || ws[0] == nil || ws[0].Root != root {
		t.Errorf("the index was built for %v, not the run's workspace %q", ws, root)
	}
	if f.Index.InvalidateCalls() != 0 {
		t.Errorf("the entry point invalidated the index: %v", f.Index.Events())
	}
}
