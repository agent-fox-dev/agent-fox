package afspec

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// The v2 schemas close every object with additionalProperties: false, but
// encoding/json drops a key the Go type has no field for, and the schema runs
// on the re-encoded value. A leaked v1 `traceability` field or a typo like
// `done-when` would then vanish silently instead of being rejected. These
// functions find such keys by walking the decoded JSON against the type's
// json tags, which mirror the schema the types are generated from.

var unmarshalerType = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()

// UnknownFields lists, as JSON pointers (`/tasks/0/done-when`), the keys in
// raw — a decoded JSON value — that the type of target, a pointer to the Go
// value it decodes into, has no field for. Nil when there are none.
func UnknownFields(raw any, target any) []string {
	return unknownIn(raw, reflect.TypeOf(target), "")
}

func unknownIn(v any, t reflect.Type, path string) []string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	// A type that decodes itself does not say what keys it accepts.
	if reflect.PointerTo(t).Implements(unmarshalerType) {
		return nil
	}
	var out []string
	switch t.Kind() {
	case reflect.Struct:
		obj, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		fields := jsonFields(t)
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p := path + "/" + k
			ft, known := fields[k]
			if !known {
				out = append(out, p)
				continue
			}
			out = append(out, unknownIn(obj[k], ft, p)...)
		}
	case reflect.Slice, reflect.Array:
		arr, ok := v.([]any)
		if !ok {
			return nil
		}
		for i, e := range arr {
			out = append(out, unknownIn(e, t.Elem(), fmt.Sprintf("%s/%d", path, i))...)
		}
	case reflect.Map:
		obj, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		for k, e := range obj {
			out = append(out, unknownIn(e, t.Elem(), path+"/"+k)...)
		}
		sort.Strings(out)
	}
	return out
}

// jsonFields maps each JSON key a struct type decodes to the field's type,
// following embedded structs as encoding/json does.
func jsonFields(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				for k, v := range jsonFields(ft) {
					if _, taken := out[k]; !taken {
						out[k] = v
					}
				}
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out[name] = f.Type
	}
	return out
}
