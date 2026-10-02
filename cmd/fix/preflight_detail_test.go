package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/codefix"
	"github.com/agent-fox-dev/agentfox/internal/schematest"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// fixPreflightEnv isolates the run from the host's state and credentials.
func fixPreflightEnv(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
}

// runFixRaw drives the real App and returns the exit code and both streams.
func runFixRaw(t *testing.T, argv ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := newApp().Main(context.Background(), normalizeArgs(argv), strings.NewReader(""), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// TS-11-41 (integration, fix's share): --detail summary keeps preflight and
// estimate in the trimmed result. The other tools' shares are in cmd/impl,
// cmd/spec and cmd/issue.
//
// Verifies: 11-REQ-7.1
func TestTS11_41_DetailSummaryKeepsPreflightAndEstimate_Fix(t *testing.T) {
	fixPreflightEnv(t)
	dir := preflightRepo(t)
	code, env := runFix(t, "--preflight", "--detail", "summary", "--land", "none", "--verify", "true", "--dir", dir, "the counter double-counts")
	if code != toolio.ExitOK {
		t.Fatalf("code = %d: %v", code, env)
	}
	res, _ := env["result"].(map[string]any)
	if list, ok := res["preflight"].([]any); !ok || len(list) == 0 {
		t.Errorf("--detail summary dropped result.preflight: %v", res)
	}
	if est, ok := res["estimate"].(map[string]any); !ok || len(est) == 0 {
		t.Errorf("--detail summary dropped result.estimate: %v", res)
	}
}

// TS-11-42 (unit): a --preflight result leaves artifacts[] and side_effects[]
// empty.
//
// Verifies: 11-REQ-7.2
func TestTS11_42_PreflightLeavesArtifactsAndSideEffectsEmpty(t *testing.T) {
	fixPreflightEnv(t)
	dir := preflightRepo(t)
	for _, detail := range []string{"summary", "full"} {
		code, env := runFix(t, "--preflight", "--detail", detail, "--land", "none", "--verify", "true", "--dir", dir, "the counter double-counts")
		if code != toolio.ExitOK {
			t.Fatalf("--detail %s: code = %d: %v", detail, code, env)
		}
		if list, _ := env["side_effects"].([]any); len(list) != 0 {
			t.Errorf("--detail %s: side_effects = %v, want empty", detail, list)
		}
		// The only artifact is the envelope's own report file, which the
		// shell lists on every run; nothing the run itself made.
		arts, _ := env["artifacts"].([]any)
		for _, a := range arts {
			if kind := a.(map[string]any)["kind"]; kind != "report_file" {
				t.Errorf("--detail %s: artifact %v, want only the report_file", detail, a)
			}
		}
	}
}

// TS-11-43 (integration): a --preflight run's event stream opens with
// run_start, closes with run_end, and in between holds only step events (and
// heartbeats): no phase, turn or tool-call event, because no model ran.
//
// Verifies: 11-REQ-7.3
func TestTS11_43_PreflightEmitsOnlyStepEventsBetweenStartAndEnd(t *testing.T) {
	fixPreflightEnv(t)
	dir := preflightRepo(t)
	code, stdout, stderr := runFixRaw(t, "--preflight", "--emit-events", "--land", "none", "--verify", "true", "--dir", dir, "the counter double-counts")
	if code != toolio.ExitOK {
		t.Fatalf("code = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	var types []string
	for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
		if line == "" {
			continue
		}
		var e struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("stderr line is not a JSON event: %q: %v", line, err)
		}
		types = append(types, e.Type)
	}
	if len(types) < 3 {
		t.Fatalf("events = %v, want run_start, at least one event of the reused checks, run_end", types)
	}
	if types[0] != "run_start" || types[len(types)-1] != "run_end" {
		t.Errorf("events = %v, want run_start first and run_end last", types)
	}
	// The baseline run --preflight keeps reports its result as a check
	// event, and a no-verify-command warning as a warning event; both come
	// from the reused checks, not from a model phase.
	for _, ty := range types[1 : len(types)-1] {
		switch ty {
		case "step", "check", "warning", "heartbeat":
		default:
			t.Errorf("unexpected %q event in a --preflight run: %v", ty, types)
		}
	}
}

// TS-11-44 (integration): --output persists the --preflight envelope, byte
// for byte what stdout carries.
//
// Verifies: 11-REQ-7.4
func TestTS11_44_OutputPersistsThePreflightEnvelope(t *testing.T) {
	fixPreflightEnv(t)
	dir := preflightRepo(t)
	path := filepath.Join(t.TempDir(), "envelope.json")
	code, stdout, stderr := runFixRaw(t, "--preflight", "--output", path, "--land", "none", "--verify", "true", "--dir", dir, "the counter double-counts")
	if code != toolio.ExitOK {
		t.Fatalf("code = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("--output file not written: %v", err)
	}
	if string(got) != stdout {
		t.Errorf("--output file differs from stdout:\nfile:\n%s\nstdout:\n%s", got, stdout)
	}
	var env map[string]any
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("--output file is not JSON: %v", err)
	}
	if res, _ := env["result"].(map[string]any); res["stage"] != "preflight" {
		t.Errorf("persisted result.stage = %v, want preflight", res["stage"])
	}
}

// goldenDoc is the part of a golden --schema file TS-11-45 reads.
type goldenDoc struct {
	Flags  map[string]any `json:"flags"`
	Result map[string]any `json:"result"`
}

// property finds the schema of the property named name anywhere under doc:
// the first entry of any "properties" object with that key.
func property(doc any, name string) map[string]any {
	switch x := doc.(type) {
	case map[string]any:
		if props, ok := x["properties"].(map[string]any); ok {
			if p, ok := props[name].(map[string]any); ok {
				return p
			}
		}
		for _, c := range x {
			if p := property(c, name); p != nil {
				return p
			}
		}
	case []any:
		for _, c := range x {
			if p := property(c, name); p != nil {
				return p
			}
		}
	}
	return nil
}

// TS-11-45 (unit): every tool's golden --schema file lists --preflight among
// its flags and preflight/estimate among its result fields, each with a
// description; and every PreflightCheck and Estimate field carries a
// description tag.
//
// Verifies: 11-REQ-7.5
func TestTS11_45_GoldenFilesListPreflightFlagAndResultFields(t *testing.T) {
	root := schematest.Root(t)
	for _, tool := range schemaTools {
		t.Run(tool, func(t *testing.T) {
			raw, err := os.ReadFile(schematest.GoldenFile(root, tool))
			if err != nil {
				t.Fatal(err)
			}
			var doc goldenDoc
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			if f := property(doc.Flags, "preflight"); f == nil || f["type"] != "boolean" {
				t.Errorf("%s: flags document has no boolean \"preflight\" entry: %v", tool, f)
			}
			for _, field := range []string{"preflight", "estimate"} {
				p := property(doc.Result, field)
				if p == nil {
					t.Errorf("%s: result document has no %q property", tool, field)
					continue
				}
				if d, _ := p["description"].(string); d == "" {
					t.Errorf("%s: result.%s has no description", tool, field)
				}
			}
			// The nested fields are reachable too: every one is described.
			for _, name := range []string{"check", "ok", "detail", "phases", "max_turns_per_phase", "max_budget_per_phase_usd", "max_total_usd"} {
				if !anyNodeHasKey(doc.Result, name) {
					t.Errorf("%s: result document never mentions field %q", tool, name)
				}
			}
		})
	}

	for _, typ := range []reflect.Type{reflect.TypeOf(toolio.PreflightCheck{}), reflect.TypeOf(toolio.Estimate{})} {
		if missing := toolio.CheckDescriptions(typ); len(missing) != 0 {
			t.Errorf("%s fields without a description tag: %v", typ, missing)
		}
	}
	if missing := toolio.CheckDescriptions(reflect.TypeOf(codefix.Result{})); len(missing) != 0 {
		t.Errorf("codefix.Result fields without a description tag: %v", missing)
	}
}

// TS-11-46 (unit): CheckTrust passes with PreflightCheck.Check/.Detail tagged
// fact, and a preflight-only result's untrusted_fields array is empty.
//
// Verifies: 11-REQ-7.6
func TestTS11_46_PreflightResultHasNoUntrustedFields(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeOf(toolio.PreflightCheck{}), reflect.TypeOf(toolio.Estimate{}), reflect.TypeOf(codefix.Result{})} {
		if missing := toolio.CheckTrust(typ); len(missing) != 0 {
			t.Errorf("%s fields without a trust tag: %v", typ, missing)
		}
	}
	for _, name := range []string{"Check", "Detail"} {
		f, ok := reflect.TypeOf(toolio.PreflightCheck{}).FieldByName(name)
		if !ok || f.Tag.Get("trust") != "fact" {
			t.Errorf("PreflightCheck.%s trust tag = %q, want fact", name, f.Tag.Get("trust"))
		}
	}

	fixPreflightEnv(t)
	dir := preflightRepo(t)
	for _, detail := range []string{"summary", "full"} {
		code, env := runFix(t, "--preflight", "--detail", detail, "--land", "none", "--verify", "true", "--dir", dir, "the counter double-counts")
		if code != toolio.ExitOK {
			t.Fatalf("--detail %s: code = %d: %v", detail, code, env)
		}
		if v, present := env["untrusted_fields"]; present {
			if list, _ := v.([]any); len(list) != 0 {
				t.Errorf("--detail %s: untrusted_fields = %v, want empty", detail, v)
			}
		}
	}
}
