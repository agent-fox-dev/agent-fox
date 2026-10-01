package toolio_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/codefix"
	"github.com/agent-fox-dev/agentfox/codeimpl"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuetriage"
	"github.com/agent-fox-dev/agentfox/specgen"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// obj asserts v is a SchemaObject.
func obj(t *testing.T, v any) toolio.SchemaObject {
	t.Helper()
	o, ok := v.(toolio.SchemaObject)
	if !ok {
		t.Fatalf("not a schema object: %T %v", v, v)
	}
	return o
}

// field returns the value under key of o.
func field(t *testing.T, o toolio.SchemaObject, key string) any {
	t.Helper()
	v, ok := o.Get(key)
	if !ok {
		t.Fatalf("schema has no %q: %s", key, mustJSON(t, o))
	}
	return v
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// propKeys is the property names of an object schema, in order.
func propKeys(t *testing.T, s toolio.SchemaObject) []string {
	t.Helper()
	var keys []string
	for _, f := range obj(t, field(t, s, "properties")) {
		keys = append(keys, f.Key)
	}
	return keys
}

// requiredOf is the required list of an object schema.
func requiredOf(t *testing.T, s toolio.SchemaObject) []string {
	t.Helper()
	r, ok := field(t, s, "required").([]string)
	if !ok {
		t.Fatalf("required is not a []string: %s", mustJSON(t, s))
	}
	return r
}

func has(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// jsonName is the name encoding/json gives a field, "" when it is skipped.
func jsonName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return ""
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		return f.Name
	}
	return name
}

// jsonFieldOrder is the property order encoding/json would marshal a struct
// in: reflect.VisibleFields, minus unexported fields, skipped fields and the
// anonymous struct whose own fields are promoted in its place.
func jsonFieldOrder(t reflect.Type) []string {
	var out []string
	for _, f := range reflect.VisibleFields(t) {
		if !f.IsExported() || jsonName(f) == "" {
			continue
		}
		if f.Anonymous && f.Type.Kind() == reflect.Struct && f.Tag.Get("json") == "" {
			continue
		}
		out = append(out, jsonName(f))
	}
	return out
}

// TS-09-22 (unit): A struct type renders as an object schema whose property
// order follows reflect.VisibleFields, including promoted embedded fields.
func TestTS09_22_StructOrderFollowsVisibleFields(t *testing.T) {
	typ := reflect.TypeOf(specgen.Result{})
	s := toolio.SchemaFor(typ)

	if got := field(t, s, "type"); got != "object" {
		t.Fatalf("type = %v, want object", got)
	}
	got := propKeys(t, s)
	want := jsonFieldOrder(typ)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("property order\n got %v\nwant %v", got, want)
	}
	if has(got, "Package") || has(got, "package") {
		t.Errorf("embedded Package is nested, not promoted: %v", got)
	}
	if !has(got, "spec_dir") || !has(got, "validation") {
		t.Errorf("Package's own fields are not promoted in place: %v", got)
	}
	if has(got, "inputRef") {
		t.Errorf("unexported field leaked into properties: %v", got)
	}
	// Promoted fields come first, as Package precedes the rest of Result.
	if got[0] != "spec_dir" {
		t.Errorf("first property = %q, want the first promoted field spec_dir", got[0])
	}
}

// TS-09-23 (unit): A field's JSON name and required-ness follow its json tag
// exactly as encoding/json reads it.
func TestTS09_23_JSONTagNameAndRequired(t *testing.T) {
	type T struct {
		A string `json:"a"`
		B string `json:"b,omitempty"`
		C string `json:"-"`
		D string // no tag: named after the field, always present
		e string
	}
	_ = T{}.e
	s := toolio.SchemaFor(reflect.TypeOf(T{}))

	keys := propKeys(t, s)
	if !reflect.DeepEqual(keys, []string{"a", "b", "D"}) {
		t.Errorf("properties = %v, want [a b D]", keys)
	}
	req := requiredOf(t, s)
	if !has(req, "a") || !has(req, "D") {
		t.Errorf("required = %v, want a and D", req)
	}
	if has(req, "b") {
		t.Errorf("omitempty field b is required: %v", req)
	}
	if has(keys, "c") || has(keys, "C") || has(keys, "-") {
		t.Errorf("json:\"-\" field is not skipped: %v", keys)
	}
}

// TS-09-24 (unit): A slice field, a pointer field and a scalar field each
// render as the expected JSON Schema node.
func TestTS09_24_SliceAndPointerAndScalar(t *testing.T) {
	type Sub struct {
		N int `json:"n"`
	}
	type T struct {
		L []string `json:"l"`
		P *Sub     `json:"p"`
		I int      `json:"i"`
		F float64  `json:"f"`
		B bool     `json:"b"`
		S string   `json:"s"`
	}
	s := toolio.SchemaFor(reflect.TypeOf(T{}))
	props := obj(t, field(t, s, "properties"))

	if got, want := mustJSON(t, field(t, props, "l")), `{"type":"array","items":{"type":"string"}}`; got != want {
		t.Errorf("slice = %s, want %s", got, want)
	}
	p := mustJSON(t, field(t, props, "p"))
	if want := mustJSON(t, toolio.SchemaFor(reflect.TypeOf(Sub{}))); p != want {
		t.Errorf("pointer = %s, want its pointee's schema %s", p, want)
	}
	if strings.Contains(p, "null") || strings.Contains(p, "nullable") {
		t.Errorf("pointer schema is marked nullable: %s", p)
	}
	for name, want := range map[string]string{
		"i": `{"type":"integer"}`, "f": `{"type":"number"}`,
		"b": `{"type":"boolean"}`, "s": `{"type":"string"}`,
	} {
		if got := mustJSON(t, field(t, props, name)); got != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
}

// walkDescribed visits every property node under s, recursively, calling fn
// with a dotted path and the node.
func walkDescribed(t *testing.T, path string, s toolio.SchemaObject, fn func(path string, node toolio.SchemaObject)) {
	t.Helper()
	if v, ok := s.Get("properties"); ok {
		for _, f := range obj(t, v) {
			node := obj(t, f.Value)
			p := path + "." + f.Key
			fn(p, node)
			walkDescribed(t, p, node, fn)
		}
	}
	if v, ok := s.Get("items"); ok {
		walkDescribed(t, path+"[]", obj(t, v), fn)
	}
}

// TS-09-25 (unit): Every field reachable from toolio.Envelope and from a
// tool's Result type carries a description sourced from the new struct tag.
func TestTS09_25_DescriptionsFromStructTag(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeOf(toolio.Envelope{}), reflect.TypeOf(codefix.Result{})} {
		s := toolio.SchemaFor(typ)
		walkDescribed(t, typ.Name(), s, func(path string, node toolio.SchemaObject) {
			d, ok := node.Get("description")
			if s, _ := d.(string); !ok || s == "" {
				t.Errorf("%s has no description", path)
			}
		})
		// Top-level descriptions are exactly the description tag.
		props := obj(t, field(t, s, "properties"))
		for _, f := range reflect.VisibleFields(typ) {
			name := jsonName(f)
			if !f.IsExported() || name == "" {
				continue
			}
			want := f.Tag.Get("description")
			if want == "" {
				t.Errorf("%s.%s has no description tag", typ.Name(), f.Name)
				continue
			}
			node := obj(t, field(t, props, name))
			if got := field(t, node, "description"); got != want {
				t.Errorf("%s.%s description = %q, want the tag %q", typ.Name(), f.Name, got, want)
			}
		}
	}
}

// Every field reachable from any of the four tools' Result types is
// described, not just codefix's.
func TestTS09_25_EveryResultTypeFullyDescribed(t *testing.T) {
	for _, sample := range []any{codefix.Result{}, codeimpl.Result{}, issuetriage.Result{}, specgen.Result{}} {
		typ := reflect.TypeOf(sample)
		walkDescribed(t, typ.String(), toolio.SchemaFor(typ), func(path string, node toolio.SchemaObject) {
			d, _ := node.Get("description")
			if s, _ := d.(string); s == "" {
				t.Errorf("%s has no description", path)
			}
		})
	}
}

// rawKeyOrder lists the keys of the object under key in raw, in document order.
func rawKeyOrder(t *testing.T, raw []byte, key string) []string {
	t.Helper()
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outer); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(outer[key]))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("%s is not an object: %s", key, outer[key])
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

// TS-09-26 (unit): An object schema's properties marshal in declaration
// order, not Go map key order.
func TestTS09_26_PropertiesMarshalInDeclarationOrder(t *testing.T) {
	type T struct {
		Z int `json:"z"`
		A int `json:"a"`
		M int `json:"m"`
	}
	b, err := json.Marshal(toolio.SchemaFor(reflect.TypeOf(T{})))
	if err != nil {
		t.Fatal(err)
	}
	if got := rawKeyOrder(t, b, "properties"); !reflect.DeepEqual(got, []string{"z", "a", "m"}) {
		t.Errorf("properties order = %v, want [z a m]; %s", got, b)
	}
}

// withoutDescription is the JSON of o with its own top-level description
// removed: the substituted node keeps the envelope field's description.
func withoutDescription(t *testing.T, o toolio.SchemaObject) string {
	t.Helper()
	var out toolio.SchemaObject
	for _, f := range o {
		if f.Key != "description" {
			out = append(out, f)
		}
	}
	return mustJSON(t, out)
}

// TS-09-27 (unit): The envelope's Result field is substituted with the schema
// of the running tool's own concrete Result type.
func TestTS09_27_EnvelopeResultSubstituted(t *testing.T) {
	doc := toolio.BuildResultDocument(codefix.Result{})
	got := obj(t, field(t, obj(t, field(t, doc, "properties")), "result"))
	want := toolio.SchemaFor(reflect.TypeOf(codefix.Result{}))

	if withoutDescription(t, got) != withoutDescription(t, want) {
		t.Errorf("result.properties.result is not codefix.Result's schema:\n got %s\nwant %s", mustJSON(t, got), mustJSON(t, want))
	}
	if f, _ := got.Get("type"); f != "object" {
		t.Errorf("type = %v", f)
	}
	if _, ok := got.Get("properties"); !ok {
		t.Errorf("substituted node is a bare placeholder: %s", mustJSON(t, got))
	}
	// The envelope field's own description survives the substitution.
	if d, _ := got.Get("description"); d == nil || d == "" {
		t.Errorf("substituted node lost the envelope field's description")
	}
	// A different tool gets a different substitution.
	other := obj(t, field(t, obj(t, field(t, toolio.BuildResultDocument(issuetriage.Result{}), "properties")), "result"))
	if withoutDescription(t, other) == withoutDescription(t, got) {
		t.Errorf("two tools' results produced the same substituted schema")
	}
	// Everything else is still the envelope's own.
	if keys := propKeys(t, doc); !has(keys, "tool") || !has(keys, "ok") || !has(keys, "warnings") {
		t.Errorf("envelope properties missing from document: %v", keys)
	}
}

// Each tool's result document compiles against the 2020-12 meta-schema.
func TestTS09_28_ResultDocumentCompilesAgainstMetaSchema(t *testing.T) {
	for _, sample := range []any{codefix.Result{}, codeimpl.Result{}, issuetriage.Result{}, specgen.Result{}} {
		name := reflect.TypeOf(sample).String()
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(mustJSON(t, toolio.BuildResultDocument(sample))))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource("urn:test:result", doc); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := c.Compile("urn:test:result"); err != nil {
			t.Errorf("%s: result document is not valid JSON Schema 2020-12: %v", name, err)
		}
	}
}

// TS-09-28 (unit): The result document is a standalone, independently valid
// JSON Schema 2020-12 document.
func TestTS09_28_ResultDocumentIsStandalone(t *testing.T) {
	doc := toolio.BuildResultDocument(specgen.Result{})

	if got := field(t, doc, "$schema"); got != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %v", got)
	}
	if got := field(t, doc, "type"); got != "object" {
		t.Errorf("type = %v", got)
	}
	field(t, doc, "properties")
	req := requiredOf(t, doc)
	if !has(req, "tool") || !has(req, "ok") {
		t.Errorf("required = %v, want the envelope's always-present fields", req)
	}
	raw := mustJSON(t, doc)
	if strings.Contains(raw, "$ref") || strings.Contains(raw, "$id") {
		t.Errorf("document refers outside itself")
	}
	// $schema is first, as in the flags document.
	if !strings.HasPrefix(raw, `{"$schema":`) {
		t.Errorf("document does not lead with $schema: %.60s", raw)
	}
}
