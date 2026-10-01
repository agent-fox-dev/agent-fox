package toolio

import (
	"reflect"
	"slices"
	"strings"
)

// TrustTag is the struct tag that classifies a string or []string field's
// provenance: "fact", "model" or "external".
const TrustTag = "trust"

// CheckTrust walks every field reachable from type t, following structs,
// slices, arrays, maps and pointers, and returns the dotted path of Go field
// names of each string- or []string-typed field that lacks a trust tag, in
// walk order ("Sub.Untagged"). A field of any other kind needs no tag and is
// never reported. It returns nil when every such field is classified.
//
// It walks the way SchemaFor does: unexported fields and json:"-" fields are
// skipped, an embedded struct's fields are promoted in its place, a type
// that provides its own schema or its own MarshalJSON is not descended into,
// and a type already on the current path is not entered again.
func CheckTrust(t reflect.Type) []string {
	var missing []string
	checkTrust(t, "", nil, &missing)
	return missing
}

func checkTrust(t reflect.Type, prefix string, stack []reflect.Type, missing *[]string) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice ||
		t.Kind() == reflect.Array || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || t == timeType ||
		t.Implements(schemaProviderType) || t.Implements(marshalerType) ||
		slices.Contains(stack, t) {
		return
	}
	stack = append(stack, t)
	for _, jf := range jsonFields(t) {
		path := prefix + jf.field.Name
		if isStringOrStrings(jf.field.Type) {
			if strings.TrimSpace(jf.field.Tag.Get(TrustTag)) == "" {
				*missing = append(*missing, path)
			}
			continue
		}
		checkTrust(jf.field.Type, path+".", stack, missing)
	}
}

// isStringOrStrings reports whether t is a string or a slice of strings.
func isStringOrStrings(t reflect.Type) bool {
	return t.Kind() == reflect.String ||
		(t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.String)
}
