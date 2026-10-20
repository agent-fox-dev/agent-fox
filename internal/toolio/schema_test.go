package toolio

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/issuex"
)

// update rewrites the golden files: go test ./internal/toolio ./cmd/af -update.
var update = flag.Bool("update", false, "rewrite the golden files")

// compareGolden compares got with the golden file at path. With rewrite it
// writes the file instead.
func compareGolden(path string, got []byte, rewrite bool) error {
	if rewrite {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, got, 0o644)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%v; create it with go test -update", err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("%s differs from the output (%d bytes, want %d); rewrite it with go test -update", path, len(got), len(want))
	}
	return nil
}

// envelopeFields are the envelope's fields, in order.
var envelopeFields = []string{
	"schema_version", "tool", "run_id", "status", "exit_code", "summary", "input", "result",
	"needs_human", "error", "warnings", "artifacts", "side_effects", "next", "usage",
	"started_at", "finished_at", "duration_ms",
}

// schemaOf runs af --schema (or af <args> --schema) and parses the document.
func schemaOf(t *testing.T, app *App, args ...string) (doc map[string]any, stdout string) {
	t.Helper()
	code, stdout, stderr := invoke(t, app, args...)
	if code != 0 || stderr != "" {
		t.Fatalf("exit code %d, stderr %q; want 0 and nothing\n%s", code, stderr, stdout)
	}
	return decode(t, stdout), stdout
}

// schemaApp has two commands: "gate run" with a result schema and "alpha"
// without.
func schemaApp() *App {
	app := newTestApp()
	app.Register(Command{
		Path:         "gate run",
		ResultSchema: json.RawMessage(`{"type":"object","properties":{"passed":{"type":"boolean"}}}`),
		Run:          func(*Run) (Outcome, error) { return Outcome{Status: StatusDone}, nil },
	})
	app.Register(Command{Path: "alpha", Run: func(*Run) (Outcome, error) { return Outcome{Status: StatusDone}, nil }})
	return app
}

// TestSchemaPrintsEnvelopeSchema_TS_05_68 verifies TS-05-68:
// af --schema prints the embedded envelope schema with commands under $defs.
// Verifies: 05-REQ-10.1
func TestSchemaPrintsEnvelopeSchema_TS_05_68(t *testing.T) {
	doc, stdout := schemaOf(t, schemaApp(), "--schema")

	if doc["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %v, want draft 2020-12", doc["$schema"])
	}
	if got := at(doc, "properties", "schema_version", "const"); got != "5.0.0" {
		t.Errorf("schema_version const = %v, want 5.0.0", got)
	}
	var required []string
	for _, f := range doc["required"].([]any) {
		required = append(required, f.(string))
	}
	if !slices.Equal(required, envelopeFields) {
		t.Errorf("required = %v, want %v", required, envelopeFields)
	}
	if result, _ := at(doc, "properties", "result").(map[string]any); result == nil || result["type"] != nil || result["oneOf"] != nil {
		t.Errorf("result = %v, want an open schema", result)
	}
	defs, _ := doc["$defs"].(map[string]any)
	if _, ok := defs["gate_run"]; !ok || len(defs) != 1 {
		t.Errorf("$defs = %v, want gate_run and nothing else", defs)
	}
	if !strings.HasSuffix(stdout, "}\n") || strings.HasSuffix(stdout, "\n\n") {
		t.Errorf("the schema does not end in one newline")
	}
}

// TestSchemaNarrowedToCommand_TS_05_69 verifies TS-05-69:
// af <command> --schema narrows tool and result to the command.
// Verifies: 05-REQ-10.2
func TestSchemaNarrowedToCommand_TS_05_69(t *testing.T) {
	doc, _ := schemaOf(t, schemaApp(), "gate", "run", "--schema")

	if got := at(doc, "properties", "tool", "const"); got != "gate run" {
		t.Errorf("tool const = %v, want gate run", got)
	}
	want := map[string]any{"oneOf": []any{
		map[string]any{"type": "null"},
		map[string]any{"$ref": "#/$defs/gate_run"},
	}}
	if got := at(doc, "properties", "result"); !reflect.DeepEqual(got, want) {
		t.Errorf("result = %v, want %v", got, want)
	}
	if defs, _ := doc["$defs"].(map[string]any); len(defs) != 1 || defs["gate_run"] == nil {
		t.Errorf("$defs = %v, want only gate_run", defs)
	}

	doc, _ = schemaOf(t, schemaApp(), "alpha", "--schema")
	if got := at(doc, "properties", "tool", "const"); got != "alpha" {
		t.Errorf("tool const = %v, want alpha", got)
	}
	if got := at(doc, "properties", "result"); !reflect.DeepEqual(got, map[string]any{"type": "null"}) {
		t.Errorf("result of a command without a schema = %v, want null", got)
	}

	code, stdout, _ := invoke(t, schemaApp(), "nosuch", "--schema")
	if code != 2 || decode(t, stdout)["status"] != "usage" {
		t.Errorf("--schema for an unknown command: exit code %d, status %v; want 2, usage", code, decode(t, stdout)["status"])
	}
}

// TestSchemaWithBadResultSchemaIsAFailure guards the rule behind TS-05-68: a
// command whose result schema is not JSON cannot make af --schema print a
// broken document.
func TestSchemaWithBadResultSchemaIsAFailure(t *testing.T) {
	app := newTestApp()
	app.Register(Command{Path: "broken", ResultSchema: json.RawMessage(`{"type":`), Run: func(*Run) (Outcome, error) { return Outcome{}, nil }})

	code, stdout, _ := invoke(t, app, "--schema")
	env := decode(t, stdout)

	if code != 1 || env["status"] != "failed" || at(env, "error", "category") != CategoryInternal {
		t.Errorf("exit code %d, status %v, category %v; want 1, failed, internal", code, env["status"], at(env, "error", "category"))
	}
}

// TestSchemaGolden_TS_05_70 verifies TS-05-70:
// The schema output matches the committed golden file and -update rewrites
// it.
// Verifies: 05-REQ-10.3
func TestSchemaGolden_TS_05_70(t *testing.T) {
	_, stdout := schemaOf(t, &App{}, "--schema")
	golden := filepath.Join("testdata", "schema.golden.json")
	if err := compareGolden(golden, []byte(stdout), *update); err != nil {
		t.Error(err)
	}
}

// TestGoldenMechanism verifies the other two halves of TS-05-70: a golden
// file that differs fails the comparison, and rewriting makes it pass.
func TestGoldenMechanism(t *testing.T) {
	path := filepath.Join(t.TempDir(), "testdata", "schema.golden.json")
	output := []byte("{\n  \"a\": 1\n}\n")

	if err := compareGolden(path, output, false); err == nil {
		t.Error("a missing golden file passed")
	}
	if err := compareGolden(path, output, true); err != nil {
		t.Fatal(err)
	}
	if err := compareGolden(path, output, false); err != nil {
		t.Errorf("a rewritten golden file fails: %v", err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := compareGolden(path, output, false); err == nil {
		t.Error("a modified golden file passed")
	}
}

// jsonTags lists the JSON names of the fields of v, in order.
func jsonTags(v any) []string {
	var tags []string
	typ := reflect.TypeOf(v)
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		tags = append(tags, name)
	}
	return tags
}

// requiredOfSchema reads the required list of the embedded schema.
func requiredOfSchema(t *testing.T) []string {
	t.Helper()
	var schema struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(envelopeSchema, &schema); err != nil {
		t.Fatalf("the embedded schema is not JSON: %v", err)
	}
	for _, name := range schema.Required {
		if _, ok := schema.Properties[name]; !ok {
			t.Errorf("%s is required but has no property", name)
		}
	}
	if len(schema.Properties) != len(schema.Required) {
		t.Errorf("the schema has %d properties and %d required fields", len(schema.Properties), len(schema.Required))
	}
	return schema.Required
}

// TestSchemaMatchesEnvelope_TS_05_71 verifies TS-05-71:
// The schema's required list equals the Envelope JSON tags in names and
// order.
// Verifies: 05-REQ-10.4
func TestSchemaMatchesEnvelope_TS_05_71(t *testing.T) {
	tags := jsonTags(Envelope{})
	required := requiredOfSchema(t)
	if !slices.Equal(tags, required) {
		t.Fatalf("Envelope fields %v, schema required %v", tags, required)
	}

	// The version the schema promises is the version the envelope carries.
	var schema struct {
		Properties struct {
			SchemaVersion struct {
				Const string `json:"const"`
			} `json:"schema_version"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(envelopeSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if got := schema.Properties.SchemaVersion.Const; got != SchemaVersion {
		t.Errorf("the schema's schema_version const is %q, toolio.SchemaVersion is %q", got, SchemaVersion)
	}

	// The comparison notices a field added, removed, renamed or moved.
	mutations := map[string][]string{
		"added":   append(slices.Clone(required), "extra"),
		"removed": required[1:],
		"renamed": append([]string{"version"}, required[1:]...),
		"moved":   append(slices.Clone(required[1:]), required[0]),
	}
	for name, mutated := range mutations {
		if slices.Equal(tags, mutated) {
			t.Errorf("a schema with a field %s matches the Envelope", name)
		}
	}
}

// TestShortCircuitsDoNoWork_TS_05_8 verifies TS-05-8:
// --schema, --version and --help short-circuit input, forge and run
// directory work.
// Verifies: 05-REQ-1.8
func TestShortCircuitsDoNoWork_TS_05_8(t *testing.T) {
	dir := t.TempDir()
	url := "https://github.com/o/r/issues/1"
	cases := []struct {
		name string
		args []string
	}{
		{"--schema first", []string{"X", "--schema", "-", "--dir", dir}},
		{"--version after the input", []string{"X", url, "--version"}},
		{"--help among the flags", []string{"X", url, "--help", "--dir", dir}},
		{"-h", []string{"X", "-h", "--dry-run"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ran, forges := 0, 0
			stdin := &countingReader{}
			app := newTestApp()
			app.NewForge = func(issuex.Options) (issuex.Client, error) {
				forges++
				return issuex.NewNoOp(), nil
			}
			app.Register(Command{Path: "X", Input: InputRequired, UsesForge: true, UsesRunDir: true, DryRun: true, Run: func(*Run) (Outcome, error) {
				ran++
				return Outcome{Status: StatusDone}, nil
			}})
			var out, errb bytes.Buffer

			code := app.Main(context.Background(), append([]string{"af"}, tc.args...), stdin, &out, &errb)

			if code != 0 || ran != 0 || forges != 0 || stdin.reads != 0 || errb.Len() != 0 || out.Len() == 0 {
				t.Errorf("exit code %d, ran %d, forges %d, stdin reads %d, stderr %q, stdout %d bytes; want 0, 0, 0, 0, \"\", something",
					code, ran, forges, stdin.reads, errb.String(), out.Len())
			}
			if _, err := os.Stat(agentFoxDir(dir)); err == nil {
				t.Error("a run directory was created")
			}
		})
	}
}
