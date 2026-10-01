package toolio

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

// DescriptionTag is the struct tag a field's schema description is read
// from, alongside its json tag. A Go doc comment is not available to
// reflection at runtime, and a side table drifts from the field it describes;
// a tag sits on the field itself.
const DescriptionTag = "description"

// SchemaProvider is implemented by a type whose JSON form is produced by its
// own MarshalJSON, so reflecting over its Go fields would describe the wrong
// document. It returns the schema of what it actually marshals to.
type SchemaProvider interface {
	JSONSchema() SchemaObject
}

var (
	schemaProviderType = reflect.TypeOf((*SchemaProvider)(nil)).Elem()
	marshalerType      = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	timeType           = reflect.TypeOf(time.Time{})
)

// SchemaFor turns a Go type into a JSON Schema node, reading the type the way
// encoding/json does:
//
//   - a struct becomes {type: object, properties, required}, its properties in
//     declaration order (reflect.VisibleFields, so an anonymously embedded
//     struct's fields are promoted in place, exactly as they marshal);
//   - a field's name and optionality come from its json tag: omitempty (or
//     omitzero) leaves it out of required, json:"-" skips it, and unexported
//     fields are not visible at all;
//   - a slice or array becomes {type: array, items}; a pointer is its
//     pointee's schema, never separately nullable, because omitempty already
//     says whether it can be absent;
//   - a scalar becomes the matching primitive.
//
// A field's description is read from its description tag. A field without
// one gets none; the exhaustiveness check, not this function, is what keeps
// that from happening.
func SchemaFor(t reflect.Type) SchemaObject {
	return schemaFor(t, nil)
}

// schemaFor is SchemaFor with the chain of struct types being expanded, so a
// type that contains itself ends the recursion instead of continuing it.
func schemaFor(t reflect.Type, stack []reflect.Type) SchemaObject {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Implements(schemaProviderType) {
		return reflect.Zero(t).Interface().(SchemaProvider).JSONSchema()
	}
	switch t.Kind() {
	case reflect.Bool:
		return SchemaObject{{Key: "type", Value: "boolean"}}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return SchemaObject{{Key: "type", Value: "integer"}}
	case reflect.Float32, reflect.Float64:
		return SchemaObject{{Key: "type", Value: "number"}}
	case reflect.String:
		return SchemaObject{{Key: "type", Value: "string"}}
	case reflect.Slice, reflect.Array:
		return SchemaObject{
			{Key: "type", Value: "array"},
			{Key: "items", Value: schemaFor(t.Elem(), stack)},
		}
	case reflect.Map:
		return SchemaObject{
			{Key: "type", Value: "object"},
			{Key: "additionalProperties", Value: schemaFor(t.Elem(), stack)},
		}
	case reflect.Struct:
		if t == timeType {
			return SchemaObject{{Key: "type", Value: "string"}, {Key: "format", Value: "date-time"}}
		}
		if t.Implements(marshalerType) {
			// Its JSON form is its own business and this cannot know it.
			return SchemaObject{}
		}
		for _, seen := range stack {
			if seen == t {
				return SchemaObject{{Key: "type", Value: "object"}}
			}
		}
		return structSchema(t, append(stack, t))
	}
	// An interface, a channel, a func: any value, or none a schema can name.
	return SchemaObject{}
}

// jsonField is one field of a struct as encoding/json sees it.
type jsonField struct {
	name     string
	required bool
	field    reflect.StructField
}

// jsonFields lists the fields of struct type t that appear in its JSON form,
// in the order they marshal.
func jsonFields(t reflect.Type) []jsonField {
	var out []jsonField
	for _, f := range reflect.VisibleFields(t) {
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				// Its own fields are promoted in its place; the embedded
				// struct itself is not a property.
				continue
			}
		}
		if name == "" {
			name = f.Name
		}
		optional := false
		for _, o := range strings.Split(opts, ",") {
			if o == "omitempty" || o == "omitzero" {
				optional = true
			}
		}
		out = append(out, jsonField{name: name, required: !optional, field: f})
	}
	return out
}

// structSchema is the object schema of struct type t.
func structSchema(t reflect.Type, stack []reflect.Type) SchemaObject {
	props := SchemaObject{}
	required := []string{}
	for _, jf := range jsonFields(t) {
		node := withDescription(schemaFor(jf.field.Type, stack), jf.field.Tag.Get(DescriptionTag))
		props = append(props, SchemaField{Key: jf.name, Value: node})
		if jf.required {
			required = append(required, jf.name)
		}
	}
	return SchemaObject{
		{Key: "type", Value: "object"},
		{Key: "properties", Value: props},
		{Key: "required", Value: required},
	}
}

// withDescription returns node with its description set to desc, placed right
// after type so a reader sees what a property is before what is inside it.
// An empty desc leaves node as it is.
func withDescription(node SchemaObject, desc string) SchemaObject {
	if desc == "" {
		return node
	}
	out := make(SchemaObject, 0, len(node)+1)
	placed := false
	for _, f := range node {
		if f.Key == "description" {
			continue
		}
		out = append(out, f)
		if f.Key == "type" && !placed {
			out = append(out, SchemaField{Key: "description", Value: desc})
			placed = true
		}
	}
	if !placed {
		out = append(SchemaObject{{Key: "description", Value: desc}}, out...)
	}
	return out
}

// BuildResultDocument is the standalone JSON Schema 2020-12 document of the
// envelope a tool writes: SchemaFor(Envelope) with the envelope's own
// any-typed result field replaced by the schema of sample's type, the tool's
// concrete Result. sample is used only for its type, so a zero value does.
//
// The node that takes result's place keeps the description of the envelope
// field it replaces. A nil sample leaves result as the envelope declares it.
func BuildResultDocument(sample any) SchemaObject {
	env := SchemaFor(reflect.TypeOf(Envelope{}))
	props, _ := env.Get("properties")
	po, _ := props.(SchemaObject)
	if sample != nil {
		var desc string
		if f, ok := reflect.TypeOf(Envelope{}).FieldByName("Result"); ok {
			desc = f.Tag.Get(DescriptionTag)
		}
		po.Set("result", withDescription(SchemaFor(reflect.TypeOf(sample)), desc))
	}
	doc := SchemaObject{{Key: "$schema", Value: SchemaDialect}}
	for _, f := range env {
		if f.Key == "properties" {
			doc = append(doc, SchemaField{Key: "properties", Value: po})
			continue
		}
		doc = append(doc, f)
	}
	return doc
}
