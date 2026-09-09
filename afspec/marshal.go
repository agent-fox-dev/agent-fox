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
	// These MUST match the order in the corresponding .v1.json schema files.
	schemaFieldOrder = map[reflect.Type][]string{
		// requirements.v1.json
		reflect.TypeOf(RequirementsV1Json{}):  {"$schema", "spec_id", "spec_name", "schema_version", "introduction", "glossary", "requirements", "correctness_properties", "execution_paths", "error_handling", "external_apis"},
		reflect.TypeOf(UserStory{}):           {"role", "goal", "benefit"},
		reflect.TypeOf(Criterion{}):           {"id", "ears_pattern", "trigger", "condition", "error_condition", "state", "feature", "system", "action", "return_contract"},
		reflect.TypeOf(Requirement{}):         {"id", "title", "user_story", "acceptance_criteria", "edge_cases"},
		reflect.TypeOf(CorrectnessProperty{}): {"id", "title", "for_any", "invariant", "validates"},
		reflect.TypeOf(PathStep{}):            {"actor", "action"},
		reflect.TypeOf(ExecutionPath{}):       {"id", "title", "steps"},
		reflect.TypeOf(ErrorHandlingEntry{}):  {"id", "condition", "behavior", "requirement_id"},
		reflect.TypeOf(ExternalApiSymbol{}):   {"name", "import_path", "signature", "notes"},
		reflect.TypeOf(ExternalApi{}):         {"package", "version", "symbols"},

		// test_spec.v1.json
		reflect.TypeOf(TestSpecV1Json{}): {"$schema", "spec_id", "spec_name", "schema_version", "test_cases", "property_tests", "edge_case_tests", "smoke_tests", "coverage"},
		reflect.TypeOf(TestCase{}):       {"id", "requirement_id", "kind", "description", "preconditions", "input", "expected", "assertion_pseudocode"},
		reflect.TypeOf(PropertyTest{}):   {"id", "property_id", "validates", "description", "for_any_strategy", "invariant_check"},
		reflect.TypeOf(EdgeCaseTest{}):   {"id", "requirement_id", "kind", "description", "preconditions", "input", "expected", "assertion_pseudocode"},
		reflect.TypeOf(SmokeTest{}):      {"id", "execution_path_id", "description", "trigger", "real_components", "mockable", "expected_effects"},
		reflect.TypeOf(Coverage{}):       {"requirements_covered", "properties_covered", "paths_covered", "gaps"},

		// tasks.v1.json
		reflect.TypeOf(TasksV1Json{}):         {"$schema", "spec_id", "spec_name", "schema_version", "test_commands", "dependencies", "task_groups", "traceability"},
		reflect.TypeOf(TestCommands{}):        {"spec_tests", "all_tests", "linter"},
		reflect.TypeOf(TaskDependency{}):      {"depends_on_spec", "from_group", "to_group", "relationship", "sentinel"},
		reflect.TypeOf(Subtask{}):             {"id", "title", "details", "test_spec_refs", "requirement_refs", "state", "optional"},
		reflect.TypeOf(VerificationSubtask{}): {"id", "checks"},
		reflect.TypeOf(TaskGroup{}):           {"id", "kind", "title", "subtasks", "verification"},
		reflect.TypeOf(TraceabilityEntry{}):   {"requirement_id", "test_spec_id", "task_id", "test_path"},
	}

	// Fields that should be omitted when nil. This matches the Python library's
	// behavior where these fields either don't exist on the Python model or
	// are pattern-specific fields on Criterion that use omitempty semantics.
	omitWhenNilFields = map[reflect.Type]map[string]bool{
		reflect.TypeOf(Criterion{}): {
			"trigger":         true,
			"condition":       true,
			"error_condition": true,
			"state":           true,
			"feature":         true,
		},
		reflect.TypeOf(RequirementsV1Json{}): {
			"$schema":       true,
			"external_apis": true,
		},
		reflect.TypeOf(TestSpecV1Json{}): {
			"$schema": true,
		},
		reflect.TypeOf(TasksV1Json{}): {
			"$schema": true,
		},
		reflect.TypeOf(ExternalApiSymbol{}): {
			"notes": true,
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
	// Use encoding/json for proper JSON string escaping
	data, _ := json.Marshal(s)
	w.buf.Write(data)
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
