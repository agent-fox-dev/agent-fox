package toolio

import (
	"reflect"
	"strconv"
	"strings"
)

// UntrustedFields returns the RFC 6901 JSON pointers, rooted at /result, of
// every field of result that is classified trust:"model" or trust:"external"
// and is present — a non-empty string or a non-empty []string — in this
// particular value, in declaration order (the order the value marshals in).
//
// A []string is one pointer naming the array. A slice of structs is walked
// element by element, each present classified sub-field getting its own
// indexed pointer. A nil pointer contributes nothing, and nothing is walked
// past it. A field classified fact, or not classified at all, is never
// listed. The walk is over the value it is given, so a trimmed view is
// reported as it is: there is no detail parameter.
//
// result may be a struct or a pointer to one; anything else, or a nil, has
// no classified fields and yields nil.
func UntrustedFields(result any) []string {
	if result == nil {
		return nil
	}
	var out []string
	walkUntrusted(reflect.ValueOf(result), "/result", &out)
	return out
}

func walkUntrusted(v reflect.Value, ptr string, out *[]string) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		if t == timeType || t.Implements(marshalerType) || t.Implements(schemaProviderType) {
			return
		}
		for _, jf := range jsonFields(t) {
			// FieldByIndexErr refuses to pass through a nil embedded pointer,
			// whose promoted fields are then simply absent.
			fv, err := v.FieldByIndexErr(jf.field.Index)
			if err != nil {
				continue
			}
			p := ptr + "/" + escapePointer(jf.name)
			switch strings.TrimSpace(jf.field.Tag.Get(TrustTag)) {
			case "model", "external":
				if isStringOrStrings(jf.field.Type) {
					if fv.Len() > 0 {
						*out = append(*out, p)
					}
					continue
				}
			}
			walkUntrusted(fv, p, out)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			walkUntrusted(v.Index(i), ptr+"/"+strconv.Itoa(i), out)
		}
	}
}

// escapePointer escapes a JSON object key as one RFC 6901 reference token.
func escapePointer(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}
