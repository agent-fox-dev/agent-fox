package toolio

import (
	"bytes"
	"encoding/json"
)

// SchemaDialect is the JSON Schema draft every generated document declares
// itself against.
const SchemaDialect = "https://json-schema.org/draft/2020-12/schema"

// SchemaField is one key of a SchemaObject.
type SchemaField struct {
	Key   string
	Value any
}

// SchemaObject is a JSON object whose keys keep the order they were added in.
//
// Go sorts map[string]any keys unconditionally on marshal, so a schema built
// from maps would reorder its properties. A slice of pairs with its own
// MarshalJSON keeps whatever order the generator chose: flag-name order for a
// flags document, declaration order for a struct.
type SchemaObject []SchemaField

// Set appends key, or replaces its value if the key is already present.
func (o *SchemaObject) Set(key string, value any) {
	for i := range *o {
		if (*o)[i].Key == key {
			(*o)[i].Value = value
			return
		}
	}
	*o = append(*o, SchemaField{Key: key, Value: value})
}

// Get returns the value stored under key.
func (o SchemaObject) Get(key string) (any, bool) {
	for _, f := range o {
		if f.Key == key {
			return f.Value, true
		}
	}
	return nil, false
}

// MarshalJSON renders the fields in order. A nil object is an empty one, not
// null.
func (o SchemaObject) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, f := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, err := json.Marshal(f.Key)
		if err != nil {
			return nil, err
		}
		buf.Write(k)
		buf.WriteByte(':')
		v, err := json.Marshal(f.Value)
		if err != nil {
			return nil, err
		}
		buf.Write(v)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
