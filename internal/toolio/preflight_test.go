package toolio

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"reflect"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
)

const preflightUsage = "run every check that would refuse the run, then stop; makes no change beyond a verification baseline"

// preflightApp builds an App whose Exec and PreflightExec each record the
// Deps they were handed and how often they were called.
type preflightApp struct {
	*App
	execDeps, pfDeps   Deps
	execCalls, pfCalls int
	preChecks          int
	checkInputs        []Input
}

func newPreflightApp(t *testing.T, withPreflight bool) *preflightApp {
	t.Helper()
	pa := &preflightApp{}
	pa.App = &App{
		Name:    "tool",
		Version: "test",
		Usage:   "usage\n",
		PreCheck: func(*Common) error {
			pa.preChecks++
			return nil
		},
		CheckInput: func(in Input) error {
			pa.checkInputs = append(pa.checkInputs, in)
			return nil
		},
		Exec: func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
			pa.execCalls++
			pa.execDeps = d
			return ExitOK, map[string]string{"stage": "done"}, nil
		},
	}
	if withPreflight {
		pa.App.PreflightExec = func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
			pa.pfCalls++
			pa.pfDeps = d
			return ExitOK, map[string]string{"stage": "preflight"}, nil
		}
	}
	return pa
}

// TS-11-1 (unit): Common.Register adds a --preflight boolean flag defaulting
// to false with the documented usage string.
func TestTS11_1_RegisterAddsPreflightFlag(t *testing.T) {
	var c Common
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	c.Register(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if c.Preflight {
		t.Error("Preflight defaults to true, want false")
	}
	f := fs.Lookup("preflight")
	if f == nil {
		t.Fatal("--preflight is not registered")
	}
	if f.Usage != preflightUsage {
		t.Errorf("Usage = %q", f.Usage)
	}
	if f.DefValue != "false" {
		t.Errorf("DefValue = %q, want false", f.DefValue)
	}
	if _, ok := f.Value.(flag.Getter).Get().(bool); !ok {
		t.Errorf("--preflight is not a boolean flag")
	}
}

// TS-11-2 (integration): --schema takes priority over --preflight and exits
// before the input is classified.
func TestTS11_2_SchemaTakesPriorityOverPreflight(t *testing.T) {
	app := schemaApp(t)
	app.PreflightExec = func(context.Context, Deps) (int, any, *ErrorInfo) {
		t.Error("PreflightExec ran for --schema")
		return ExitOK, nil, nil
	}
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--schema", "--preflight", "some input"}, strings.NewReader(""), &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &m); err != nil {
		t.Fatalf("stdout is not the self-description document (%v):\n%s", err, stdout.String())
	}
	for _, k := range []string{"tool", "schema_version", "flags", "result"} {
		if _, ok := m[k]; !ok {
			t.Errorf("document has no %q key", k)
		}
	}
	if _, ok := m["ok"]; ok {
		t.Error("stdout is an envelope, not the self-description document")
	}
	if !bytes.Contains(m["flags"], []byte(`"preflight"`)) {
		t.Errorf("flags does not describe --preflight: %s", m["flags"])
	}
}

// TS-11-3 (integration): every other check and flag is honoured identically
// whether or not --preflight is set.
func TestTS11_3_OtherFlagsHonouredIdentically(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	argv := []string{"--dir", dir, "--model", "STANDARD", "--budget", "3", "--max-turns", "9", "input text"}

	plain := newPreflightApp(t, true)
	if _, code, _ := runApp(t, plain.App, argv, ""); code != ExitOK {
		t.Fatalf("plain code = %d", code)
	}
	pre := newPreflightApp(t, true)
	if _, code, _ := runApp(t, pre.App, append([]string{"--preflight"}, argv...), ""); code != ExitOK {
		t.Fatalf("preflight code = %d", code)
	}
	if plain.preChecks != 1 || pre.preChecks != 1 {
		t.Errorf("PreCheck ran %d / %d times, want 1 / 1", plain.preChecks, pre.preChecks)
	}
	if len(plain.checkInputs) != 1 || len(pre.checkInputs) != 1 {
		t.Fatalf("CheckInput ran %d / %d times, want 1 / 1", len(plain.checkInputs), len(pre.checkInputs))
	}
	if plain.checkInputs[0].Body != pre.checkInputs[0].Body || plain.checkInputs[0].Kind != pre.checkInputs[0].Kind {
		t.Errorf("CheckInput saw %+v vs %+v", plain.checkInputs[0], pre.checkInputs[0])
	}
	a, b := plain.execDeps, pre.pfDeps
	if a.Workspace.Root != b.Workspace.Root {
		t.Errorf("workspace root %q vs %q", a.Workspace.Root, b.Workspace.Root)
	}
	if a.Model.Model.ID != b.Model.Model.ID || a.Model.Thinking != b.Model.Thinking {
		t.Errorf("model %q/%v vs %q/%v", a.Model.Model.ID, a.Model.Thinking, b.Model.Model.ID, b.Model.Thinking)
	}
	if a.runner.Bounds != b.runner.Bounds {
		t.Errorf("bounds %+v vs %+v", a.runner.Bounds, b.runner.Bounds)
	}
}

// TS-11-5 (unit): toolio.App carries a PreflightExec field with Exec's shape.
func TestTS11_5_AppCarriesPreflightExec(t *testing.T) {
	app := App{PreflightExec: func(context.Context, Deps) (int, any, *ErrorInfo) { return ExitOK, nil, nil }}
	code, _, _ := app.PreflightExec(context.Background(), Deps{})
	if code != ExitOK {
		t.Errorf("code = %d", code)
	}
	pf, _ := reflect.TypeOf(app).FieldByName("PreflightExec")
	ex, _ := reflect.TypeOf(app).FieldByName("Exec")
	if pf.Type != ex.Type {
		t.Errorf("PreflightExec is %v, Exec is %v", pf.Type, ex.Type)
	}
}

// TS-11-6 (integration): App.execute dispatches to PreflightExec, not Exec,
// under --preflight, passing the Deps Exec would have received.
func TestTS11_6_DispatchesToPreflightExec(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	argv := []string{"--dir", dir, "input text"}

	plain := newPreflightApp(t, true)
	runApp(t, plain.App, argv, "")
	if plain.execCalls != 1 || plain.pfCalls != 0 {
		t.Fatalf("plain run: Exec %d, PreflightExec %d", plain.execCalls, plain.pfCalls)
	}

	pre := newPreflightApp(t, true)
	env, code, _ := runApp(t, pre.App, append([]string{"--preflight"}, argv...), "")
	if code != ExitOK || !env.OK {
		t.Fatalf("code = %d env = %+v", code, env)
	}
	if pre.pfCalls != 1 {
		t.Errorf("PreflightExec called %d times, want 1", pre.pfCalls)
	}
	if pre.execCalls != 0 {
		t.Errorf("Exec called %d times under --preflight", pre.execCalls)
	}
	a, b := plain.execDeps, pre.pfDeps
	if a.Workspace.Root != b.Workspace.Root {
		t.Errorf("Workspace %q vs %q", a.Workspace.Root, b.Workspace.Root)
	}
	if b.Runner == nil || a.Runner == nil || b.Runner.Model().ID != a.Runner.Model().ID {
		t.Errorf("Runner differs: %v vs %v", a.Runner, b.Runner)
	}
	if b.Forge == nil || a.Forge == nil || b.Forge.Authenticated() != a.Forge.Authenticated() {
		t.Errorf("Forge differs")
	}
	if a.Model.Model.ID != b.Model.Model.ID || a.Model.Spec != b.Model.Spec {
		t.Errorf("Model differs: %+v vs %+v", a.Model, b.Model)
	}
	if a.Input.Kind != b.Input.Kind || a.Input.Body != b.Input.Body || a.Input.Origin != b.Input.Origin {
		t.Errorf("Input differs: %+v vs %+v", a.Input, b.Input)
	}
	if b.Common == nil || !b.Common.Preflight {
		t.Error("PreflightExec's Deps.Common does not carry --preflight")
	}
}

// TS-11-7 (unit): a tool that leaves PreflightExec nil reports an internal
// error under --preflight rather than silently running Exec.
func TestTS11_7_NilPreflightExecIsAnInternalError(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	pa := newPreflightApp(t, false)
	env, code, _ := runApp(t, pa.App, []string{"--preflight", "--dir", t.TempDir(), "input text"}, "")
	if code != ExitFailed {
		t.Errorf("code = %d, want %d", code, ExitFailed)
	}
	if env.OK || env.Error == nil {
		t.Fatalf("env = %+v", env)
	}
	if env.Error.Category != agentrun.CategoryInternal {
		t.Errorf("Category = %q, want %q", env.Error.Category, agentrun.CategoryInternal)
	}
	if pa.execCalls != 0 {
		t.Errorf("Exec ran %d times", pa.execCalls)
	}
}

// TS-11-13 (unit): PreflightCheck exposes Check, OK and Detail with the
// documented JSON and trust tags.
func TestTS11_13_PreflightCheckTags(t *testing.T) {
	typ := reflect.TypeOf(PreflightCheck{})
	for _, tc := range []struct{ name, json, trust string }{
		{"Check", "check", "fact"},
		{"OK", "ok", ""},
		{"Detail", "detail,omitempty", "fact"},
	} {
		f, ok := typ.FieldByName(tc.name)
		if !ok {
			t.Fatalf("no field %s", tc.name)
		}
		if got := f.Tag.Get("json"); got != tc.json {
			t.Errorf("%s json = %q, want %q", tc.name, got, tc.json)
		}
		if got := f.Tag.Get("trust"); got != tc.trust {
			t.Errorf("%s trust = %q, want %q", tc.name, got, tc.trust)
		}
		if f.Tag.Get("description") == "" {
			t.Errorf("%s has no description tag", tc.name)
		}
	}
	if typ.NumField() != 3 {
		t.Errorf("PreflightCheck has %d fields, want 3", typ.NumField())
	}
}

// TS-11-14 (unit): Estimate exposes its four fields with the documented JSON
// tags and a description each.
func TestTS11_14_EstimateTags(t *testing.T) {
	typ := reflect.TypeOf(Estimate{})
	for name, want := range map[string]string{
		"Phases":               "phases",
		"MaxTurnsPerPhase":     "max_turns_per_phase",
		"MaxBudgetPerPhaseUSD": "max_budget_per_phase_usd",
		"MaxTotalUSD":          "max_total_usd",
	} {
		f, ok := typ.FieldByName(name)
		if !ok {
			t.Fatalf("no field %s", name)
		}
		if got := f.Tag.Get("json"); got != want {
			t.Errorf("%s json = %q, want %q", name, got, want)
		}
		if f.Tag.Get("description") == "" {
			t.Errorf("%s has no description tag", name)
		}
	}
	if typ.NumField() != 4 {
		t.Errorf("Estimate has %d fields, want 4", typ.NumField())
	}
}
