package toolio

import (
	"reflect"
	"slices"
	"strings"
)

// CheckDescriptions walks every field reachable from struct type t, following
// structs, slices, arrays, maps and pointers, and returns the Go name of each
// field that lacks the description tag (see DescriptionTag), in walk order.
// A field of t itself is named as is; a field of a nested type is named by
// the path of Go field names that reaches it ("Result.Files.Path"), so two
// types that share a field name are told apart. It returns nil when every
// field is described.
//
// It reads fields the way the generator does (SchemaFor): unexported fields,
// json:"-" fields and an embedded struct itself are skipped (the embedded
// struct's own fields are checked as the promoted fields they are), and a
// type that provides its own schema or its own MarshalJSON is not descended
// into, because its Go fields are not its JSON form.
func CheckDescriptions(t reflect.Type) []string {
	var missing []string
	checkDescriptions(t, "", nil, &missing)
	return missing
}

func checkDescriptions(t reflect.Type, prefix string, stack []reflect.Type, missing *[]string) {
	for {
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			t = t.Elem()
			continue
		case reflect.Map:
			t = t.Elem()
			continue
		}
		break
	}
	if t.Kind() != reflect.Struct || t == timeType ||
		t.Implements(schemaProviderType) || t.Implements(marshalerType) ||
		slices.Contains(stack, t) {
		return
	}
	stack = append(stack, t)
	for _, jf := range jsonFields(t) {
		path := prefix + jf.field.Name
		if strings.TrimSpace(jf.field.Tag.Get(DescriptionTag)) == "" {
			*missing = append(*missing, path)
		}
		checkDescriptions(jf.field.Type, path+".", stack, missing)
	}
}
