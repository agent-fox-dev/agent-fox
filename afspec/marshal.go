package afspec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"sync"
)

// schemaFieldOrder maps Go struct types to their JSON Schema property order.
// This ensures MarshalJSON produces fields in the same order as the Python library.
var schemaFieldOrder map[reflect.Type][]string

// omitWhenNilFields defines which fields should be omitted when their value is nil.
// Fields NOT listed here are always included (nil values become JSON null).
// This matches the Python library's behavior, where:
// - Criterion pattern fields (trigger, condition, etc.) are omitted when None
// - Schema-optional fields ($schema, external_apis, etc.) are omitted when absent
var omitWhenNilFields map[reflect.Type]map[string]bool

func init() {
	// Field orderings from JSON Schema properties order.
	// These MUST match the order in the corresponding .v2.json schema files.
	schemaFieldOrder = map[reflect.Type][]string{
		// requirements.v2.json
		reflect.TypeOf(RequirementsV2Json{}): {"$schema", "spec_id", "spec_name", "schema_version", "introduction", "glossary", "requirements", "execution_paths", "external_apis"},
		reflect.TypeOf(Requirement{}):        {"id", "title", "rationale", "criteria"},
		reflect.TypeOf(Criterion{}):          {"id", "pattern", "condition", "guard", "system", "action", "contract"},
		reflect.TypeOf(PathStep{}):           {"actor", "action"},
		reflect.TypeOf(ExecutionPath{}):      {"id", "title", "steps"},
		reflect.TypeOf(ExternalApiSymbol{}):  {"name", "import_path", "signature", "notes"},
		reflect.TypeOf(ExternalApi{}):        {"package", "version", "verified", "symbols"},

		// test_spec.v2.json
		reflect.TypeOf(TestSpecV2Json{}): {"$schema", "spec_id", "spec_name", "schema_version", "tests"},
		reflect.TypeOf(Test{}):           {"id", "kind", "verifies", "title", "given", "when", "then", "pseudocode", "real_components"},

		// tasks.v2.json
		reflect.TypeOf(TasksV2Json{}):  {"$schema", "spec_id", "spec_name", "schema_version", "test_commands", "dependencies", "tasks"},
		reflect.TypeOf(TestCommands{}): {"all_tests", "linter", "spec_tests"},
		reflect.TypeOf(Dependency{}):   {"spec", "reason"},
		reflect.TypeOf(Task{}):         {"id", "kind", "title", "criteria", "tests", "steps", "touches", "depends_on", "done_when", "state", "optional"},
	}

	// Fields omitted when nil. Everything else is always written, so a nil
	// value would appear as JSON null — which the v2 schemas reject.
	omitWhenNilFields = map[reflect.Type]map[string]bool{
		reflect.TypeOf(Criterion{}): {
			"condition": true,
			"guard":     true,
			"system":    true,
			"contract":  true,
		},
		reflect.TypeOf(Requirement{}): {
			"rationale": true,
		},
		reflect.TypeOf(RequirementsV2Json{}): {
			"glossary":      true,
			"external_apis": true,
		},
		reflect.TypeOf(ExternalApiSymbol{}): {
			"notes": true,
		},
		reflect.TypeOf(Test{}): {
			"pseudocode":      true,
			"real_components": true,
		},
		reflect.TypeOf(TestCommands{}): {
			"spec_tests": true,
		},
		reflect.TypeOf(Task{}): {
			"criteria":   true,
			"touches":    true,
			"depends_on": true,
			"done_when":  true,
			"optional":   true,
		},
	}
}

// structFieldInfo holds metadata about a single struct field for serialization.
type structFieldInfo struct {
	Index   []int  // reflect field index
	JsonKey string // JSON key name
	OmitNil bool   // omit when nil (pointer, slice, map, interface)
}

var (
	fieldInfoCache = map[reflect.Type][]structFieldInfo{}
	fieldInfoMu    sync.RWMutex
)

// getStructFields returns the ordered field infos for a struct type,
// using the JSON Schema property order when available.
func getStructFields(t reflect.Type) []structFieldInfo {
	fieldInfoMu.RLock()
	if fields, ok := fieldInfoCache[t]; ok {
		fieldInfoMu.RUnlock()
		return fields
	}
	fieldInfoMu.RUnlock()

	fieldInfoMu.Lock()
	defer fieldInfoMu.Unlock()

	// Double-check after acquiring write lock
	if fields, ok := fieldInfoCache[t]; ok {
		return fields
	}

	// Build field map from struct
	fieldMap := make(map[string]structFieldInfo)
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		tag := sf.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		parts := strings.Split(tag, ",")
		jsonKey := parts[0]
		if jsonKey == "" {
			continue
		}

		omitNil := false
		if omitMap, ok := omitWhenNilFields[t]; ok {
			omitNil = omitMap[jsonKey]
		}

		fieldMap[jsonKey] = structFieldInfo{
			Index:   sf.Index,
			JsonKey: jsonKey,
			OmitNil: omitNil,
		}
	}

	// Order fields based on schema order
	order, ok := schemaFieldOrder[t]
	if !ok {
		// Fallback: use struct declaration order
		order = make([]string, 0, len(fieldMap))
		for i := 0; i < t.NumField(); i++ {
			sf := t.Field(i)
			tag := sf.Tag.Get("json")
			if tag == "" || tag == "-" {
				continue
			}
			parts := strings.Split(tag, ",")
			jsonKey := parts[0]
			if jsonKey != "" {
				order = append(order, jsonKey)
			}
		}
	}

	fields := make([]structFieldInfo, 0, len(order))
	for _, key := range order {
		if fi, ok := fieldMap[key]; ok {
			fields = append(fields, fi)
		}
	}

	fieldInfoCache[t] = fields
	return fields
}

// isNilValue checks whether a reflect.Value is nil (for pointer, interface, slice, map types).
func isNilValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map:
		return v.IsNil()
	default:
		return false
	}
}

// MarshalJSON serializes a spec artifact struct to deterministic JSON.
// Struct fields are serialized in JSON Schema property order and all
// map[string]T keys are sorted alphabetically. The output uses 2-space
// indentation with a trailing newline, matching the Python library's
// json.dumps(indent=2) output byte-for-byte.
func MarshalJSON(v interface{}) ([]byte, error) {
	if v == nil {
		return []byte("null\n"), nil
	}
	w := &jsonWriter{}
	if err := w.writeValue(reflect.ValueOf(v)); err != nil {
		return nil, err
	}
	w.buf.WriteByte('\n')
	return w.buf.Bytes(), nil
}

// jsonWriter is a custom JSON encoder that produces deterministic output
// matching the Python library's json.dumps(indent=2) format.
type jsonWriter struct {
	buf   bytes.Buffer
	depth int
}

func (w *jsonWriter) indent() {
	for i := 0; i < w.depth; i++ {
		w.buf.WriteString("  ")
	}
}

func (w *jsonWriter) writeValue(v reflect.Value) error {
	// Handle invalid (nil interface passed directly)
	if !v.IsValid() {
		w.buf.WriteString("null")
		return nil
	}

	// Dereference interfaces
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			w.buf.WriteString("null")
			return nil
		}
		return w.writeValue(v.Elem())
	}

	// Dereference pointers
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			w.buf.WriteString("null")
			return nil
		}
		return w.writeValue(v.Elem())
	}

	switch v.Kind() {
	case reflect.Struct:
		return w.writeStruct(v)
	case reflect.Map:
		return w.writeMap(v)
	case reflect.Slice:
		return w.writeSlice(v)
	case reflect.String:
		w.writeString(v.String())
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		fmt.Fprintf(&w.buf, "%d", v.Int())
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		fmt.Fprintf(&w.buf, "%d", v.Uint())
		return nil
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return fmt.Errorf("unsupported float value: %v", f)
		}
		if f == math.Trunc(f) && math.Abs(f) < 1e15 {
			// Whole number — output as integer to match Python behavior
			fmt.Fprintf(&w.buf, "%d", int64(f))
		} else {
			fmt.Fprintf(&w.buf, "%g", f)
		}
		return nil
	case reflect.Bool:
		if v.Bool() {
			w.buf.WriteString("true")
		} else {
			w.buf.WriteString("false")
		}
		return nil
	default:
		return fmt.Errorf("MarshalJSON: unsupported type %s (kind %s)", v.Type(), v.Kind())
	}
}

func (w *jsonWriter) writeString(s string) {
	// encoding/json's Marshal escapes <, > and & as \u003c, \u003e and \u0026
	// for safe embedding in HTML. Spec text is full of those characters —
	// "<message>", "a && b" — and the escaping would make a spec that contains
	// one fail to round-trip byte-for-byte, so it is turned off here.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		// A Go string always marshals, so this cannot happen in practice.
		w.buf.WriteString(`""`)
		return
	}
	// Encode appends a newline that the caller does not want.
	w.buf.Write(bytes.TrimRight(buf.Bytes(), "\n"))
}

func (w *jsonWriter) writeStruct(v reflect.Value) error {
	fields := getStructFields(v.Type())

	// Collect non-omitted fields
	type fieldEntry struct {
		info  structFieldInfo
		value reflect.Value
	}
	entries := make([]fieldEntry, 0, len(fields))
	for _, fi := range fields {
		fv := v.FieldByIndex(fi.Index)
		if fi.OmitNil && isNilValue(fv) {
			continue
		}
		entries = append(entries, fieldEntry{fi, fv})
	}

	if len(entries) == 0 {
		w.buf.WriteString("{}")
		return nil
	}

	w.buf.WriteByte('{')
	w.depth++
	for i, entry := range entries {
		if i > 0 {
			w.buf.WriteByte(',')
		}
		w.buf.WriteByte('\n')
		w.indent()
		w.writeString(entry.info.JsonKey)
		w.buf.WriteString(": ")
		if err := w.writeValue(entry.value); err != nil {
			return err
		}
	}
	w.depth--
	w.buf.WriteByte('\n')
	w.indent()
	w.buf.WriteByte('}')
	return nil
}

func (w *jsonWriter) writeMap(v reflect.Value) error {
	if v.IsNil() {
		w.buf.WriteString("null")
		return nil
	}

	keys := v.MapKeys()
	if len(keys) == 0 {
		w.buf.WriteString("{}")
		return nil
	}

	// Sort keys alphabetically
	sortedKeys := make([]string, len(keys))
	for i, k := range keys {
		sortedKeys[i] = k.String()
	}
	sort.Strings(sortedKeys)

	w.buf.WriteByte('{')
	w.depth++
	for i, key := range sortedKeys {
		if i > 0 {
			w.buf.WriteByte(',')
		}
		w.buf.WriteByte('\n')
		w.indent()
		w.writeString(key)
		w.buf.WriteString(": ")
		if err := w.writeValue(v.MapIndex(reflect.ValueOf(key))); err != nil {
			return err
		}
	}
	w.depth--
	w.buf.WriteByte('\n')
	w.indent()
	w.buf.WriteByte('}')
	return nil
}

func (w *jsonWriter) writeSlice(v reflect.Value) error {
	if v.IsNil() {
		w.buf.WriteString("null")
		return nil
	}

	if v.Len() == 0 {
		w.buf.WriteString("[]")
		return nil
	}

	w.buf.WriteByte('[')
	w.depth++
	for i := 0; i < v.Len(); i++ {
		if i > 0 {
			w.buf.WriteByte(',')
		}
		w.buf.WriteByte('\n')
		w.indent()
		if err := w.writeValue(v.Index(i)); err != nil {
			return err
		}
	}
	w.depth--
	w.buf.WriteByte('\n')
	w.indent()
	w.buf.WriteByte(']')
	return nil
}
