package toolio

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"reflect"
	"testing"
	"time"
)

// docKeys returns the keys of the JSON object in raw, in document order.
func docKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("not an object: %s (%v)", raw, err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

// flagsDoc renders BuildFlagsDocument and decodes it as generic JSON.
func flagsDoc(t *testing.T, fs *flag.FlagSet) (raw []byte, doc map[string]any) {
	t.Helper()
	raw, err := json.Marshal(BuildFlagsDocument(fs))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid JSON %s: %v", raw, err)
	}
	return raw, doc
}

func propsOf(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	p, ok := doc["properties"].(map[string]any)
	if !ok {
		t.Fatalf("no properties object in %v", doc)
	}
	return p
}

func newCommonFlagSet() (*flag.FlagSet, *Common) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	c := &Common{}
	c.Register(fs)
	return fs, c
}

// TS-09-14 (unit): property order follows fs.VisitAll's name order over
// Common plus the tool's own flags.
func TestTS09_14_FlagsPropertyOrder(t *testing.T) {
	fs, _ := newCommonFlagSet()
	var z, a string
	fs.StringVar(&z, "zeta", "", "z")
	fs.StringVar(&a, "alpha", "", "a")

	var want []string
	fs.VisitAll(func(f *flag.Flag) {
		if f.Name != "schema" && f.Name != "version" {
			want = append(want, f.Name)
		}
	})

	raw, err := json.Marshal(BuildFlagsDocument(fs))
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	got := docKeys(t, top["properties"])
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("property order\n got %v\nwant %v", got, want)
	}
	if len(got) < 3 {
		t.Fatalf("expected the common flags too, got %v", got)
	}
}

// TS-09-15 (unit): the schema and version flags are excluded.
func TestTS09_15_FlagsExcludeSchemaAndVersion(t *testing.T) {
	fs, _ := newCommonFlagSet()
	if fs.Lookup("schema") == nil {
		t.Fatal("precondition: schema flag should be registered by Common")
	}
	if fs.Lookup("version") == nil {
		t.Fatal("precondition: version flag should be registered")
	}
	_, doc := flagsDoc(t, fs)
	props := propsOf(t, doc)
	if _, ok := props["schema"]; ok {
		t.Error("properties has a schema key")
	}
	if _, ok := props["version"]; ok {
		t.Error("properties has a version key")
	}
	if _, ok := props["dir"]; !ok {
		t.Error("properties lost the dir flag")
	}
}

// TS-09-16 (unit): a flag's description is its Usage string verbatim.
func TestTS09_16_FlagDescriptionIsUsage(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	var v string
	const usage = "the exact usage text <with> & \"odd\" chars"
	fs.StringVar(&v, "x", "", usage)
	_, doc := flagsDoc(t, fs)
	p := propsOf(t, doc)["x"].(map[string]any)
	if p["description"] != usage {
		t.Fatalf("description = %q, want %q", p["description"], usage)
	}
}

// TS-09-17 (property): StringVar, BoolVar, IntVar and Float64Var report the
// JSON type matching their Getter, and their own default.
func TestTS09_17_FlagTypesFromGetter(t *testing.T) {
	cases := []struct {
		name     string
		register func(fs *flag.FlagSet)
		wantType string
		wantDef  any
	}{
		{"string", func(fs *flag.FlagSet) { var v string; fs.StringVar(&v, "f", "x", "u") }, "string", "x"},
		{"string-empty", func(fs *flag.FlagSet) { var v string; fs.StringVar(&v, "f", "", "u") }, "string", ""},
		{"bool-true", func(fs *flag.FlagSet) { var v bool; fs.BoolVar(&v, "f", true, "u") }, "boolean", true},
		{"bool-false", func(fs *flag.FlagSet) { var v bool; fs.BoolVar(&v, "f", false, "u") }, "boolean", false},
		{"int", func(fs *flag.FlagSet) { var v int; fs.IntVar(&v, "f", 7, "u") }, "integer", float64(7)},
		{"int64", func(fs *flag.FlagSet) { var v int64; fs.Int64Var(&v, "f", -3, "u") }, "integer", float64(-3)},
		{"uint", func(fs *flag.FlagSet) { var v uint; fs.UintVar(&v, "f", 9, "u") }, "integer", float64(9)},
		{"float64", func(fs *flag.FlagSet) { var v float64; fs.Float64Var(&v, "f", 3.14, "u") }, "number", 3.14},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("t", flag.ContinueOnError)
			tc.register(fs)
			_, doc := flagsDoc(t, fs)
			p := propsOf(t, doc)["f"].(map[string]any)
			if p["type"] != tc.wantType {
				t.Errorf("type = %v, want %v", p["type"], tc.wantType)
			}
			if p["default"] != tc.wantDef {
				t.Errorf("default = %#v, want %#v", p["default"], tc.wantDef)
			}
			if _, ok := p["format"]; ok {
				t.Errorf("unexpected format key in %v", p)
			}
		})
	}
}

// TS-09-18 (unit): a Duration flag is a string with format duration, default
// in the duration's own string form.
func TestTS09_18_DurationFlag(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	var v time.Duration
	fs.DurationVar(&v, "verify-timeout", 10*time.Minute, "timeout")
	_, doc := flagsDoc(t, fs)
	p := propsOf(t, doc)["verify-timeout"].(map[string]any)
	want := map[string]any{"type": "string", "format": "duration", "default": "10m0s", "description": "timeout"}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("got %v, want %v", p, want)
	}
}

// halfFlag is a hand-written flag.Value that is not a flag.Getter, like fix's
// --pull.
type halfFlag struct{ set bool }

func (h *halfFlag) String() string {
	if h == nil || !h.set {
		return ""
	}
	return "true"
}
func (h *halfFlag) Set(string) error { h.set = true; return nil }
func (h *halfFlag) IsBoolFlag() bool { return true }

// TS-09-19 (unit): a flag whose Value is not a flag.Getter is a plain string
// with its own String() as the default. (fix's own --pull flag is covered in
// cmd/fix, where it lives.)
func TestTS09_19_NonGetterFlagIsString(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	h := &halfFlag{}
	fs.Var(h, "pull", "checkout and pull")
	// Parsing after registration must not change the reported default.
	if err := fs.Parse([]string{"--pull"}); err != nil {
		t.Fatal(err)
	}
	_, doc := flagsDoc(t, fs)
	p := propsOf(t, doc)["pull"].(map[string]any)
	want := map[string]any{"type": "string", "default": "", "description": "checkout and pull"}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("got %v, want %v", p, want)
	}
}

// TS-09-20 (unit): a declared flag carries an enum; an undeclared one does
// not.
func TestTS09_20_DeclaredEnum(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	var land, repo string
	fs.StringVar(&land, "land", "pr", "what to do")
	fs.StringVar(&repo, "repo", "", "the repo")
	DeclareEnum(fs, "land", []string{"pr", "branch", "none"})
	DeclareEnum(fs, "missing", []string{"x"}) // ignored, must not panic

	_, doc := flagsDoc(t, fs)
	props := propsOf(t, doc)
	if got := props["land"].(map[string]any)["enum"]; !reflect.DeepEqual(got, []any{"pr", "branch", "none"}) {
		t.Errorf("land enum = %v", got)
	}
	if _, ok := props["repo"].(map[string]any)["enum"]; ok {
		t.Error("repo carries an enum")
	}
	if _, ok := props["missing"]; ok {
		t.Error("undeclared flag appeared")
	}
}

// TS-09-20 (unit, shared flags): --detail, the closed-set flag Common
// registers, declares its values.
func TestTS09_20_CommonEnums(t *testing.T) {
	fs, _ := newCommonFlagSet()
	_, doc := flagsDoc(t, fs)
	props := propsOf(t, doc)
	for name, want := range map[string][]any{
		"detail":     {"summary", "full"},
		"input-kind": {"file", "text", "issue", "stdin"},
	} {
		if got := props[name].(map[string]any)["enum"]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s enum = %v, want %v", name, got, want)
		}
	}
	for _, name := range []string{"dir", "model", "report-file"} {
		if _, ok := props[name].(map[string]any)["enum"]; ok {
			t.Errorf("%s must not carry an enum", name)
		}
	}
	// The removed flags should not exist.
	if _, ok := props["events"]; ok {
		t.Error("properties has an events key (removed)")
	}
	if _, ok := props["events-file"]; ok {
		t.Error("properties has an events-file key (removed)")
	}
	// --emit-events should exist as a boolean.
	if ee, ok := props["emit-events"]; !ok {
		t.Error("properties lacks emit-events")
	} else if ee.(map[string]any)["type"] != "boolean" {
		t.Errorf("emit-events type = %v, want boolean", ee.(map[string]any)["type"])
	}
}

// TS-09-21 (unit): the document is a standalone object schema with no
// required or additionalProperties key.
func TestTS09_21_FlagsDocumentShape(t *testing.T) {
	fs, _ := newCommonFlagSet()
	raw, doc := flagsDoc(t, fs)
	if doc["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %v", doc["$schema"])
	}
	if doc["type"] != "object" {
		t.Errorf("type = %v", doc["type"])
	}
	if _, ok := doc["required"]; ok {
		t.Error("has required")
	}
	if _, ok := doc["additionalProperties"]; ok {
		t.Error("has additionalProperties")
	}
	if got := docKeys(t, raw); !reflect.DeepEqual(got, []string{"$schema", "type", "properties"}) {
		t.Errorf("top-level key order = %v", got)
	}
}
