package toolio_test

import (
	"reflect"
	"testing"

	"github.com/agent-fox-dev/agentfox/codefix"
	"github.com/agent-fox-dev/agentfox/codeimpl"
	"github.com/agent-fox-dev/agentfox/issuetriage"
	"github.com/agent-fox-dev/agentfox/specgen"
)

func isStringOrStringSlice(t reflect.Type) bool {
	return t.Kind() == reflect.String ||
		(t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.String)
}

// walkTrustFields visits every exported, non-skipped struct field reachable
// from t through struct, slice and pointer types, calling fn with its path.
func walkTrustFields(t reflect.Type, prefix string, seen map[reflect.Type]bool, fn func(path string, f reflect.StructField)) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || seen[t] {
		return
	}
	seen[t] = true
	defer delete(seen, t)
	for _, f := range reflect.VisibleFields(t) {
		if !f.IsExported() || f.Tag.Get("json") == "-" {
			continue
		}
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			continue // promoted fields are visited in its place
		}
		path := prefix + f.Name
		fn(path, f)
		if !isStringOrStringSlice(f.Type) {
			walkTrustFields(f.Type, path+".", seen, fn)
		}
	}
}

// TS-10-1 (property): every string- or []string-typed field reachable from
// the four Result trees carries a trust tag with a legal value.
//
// Verifies: 10-REQ-1.1
func TestTS10_1_EveryStringFieldClassified(t *testing.T) {
	roots := []reflect.Type{
		reflect.TypeOf(codefix.Result{}),
		reflect.TypeOf(codeimpl.Result{}),
		reflect.TypeOf(issuetriage.Result{}),
		reflect.TypeOf(specgen.Result{}),
	}
	for _, root := range roots {
		n := 0
		walkTrustFields(root, "", map[reflect.Type]bool{}, func(path string, f reflect.StructField) {
			if !isStringOrStringSlice(f.Type) {
				return
			}
			n++
			switch tag := f.Tag.Get("trust"); tag {
			case "fact", "model", "external":
			default:
				t.Errorf("%s: %s trust tag = %q, want fact, model or external", root, path, tag)
			}
		})
		if n == 0 {
			t.Errorf("%s: walk found no string fields", root)
		}
	}
}
