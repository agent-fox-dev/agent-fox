package toolio

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// envelopeSchema is the JSON Schema of the envelope, without the schemas of
// the commands' results. A test fails when its required list and the fields
// of Envelope disagree.
//
//go:embed schema/envelope.schema.json
var envelopeSchema []byte

// member is one key and value of a JSON object.
type member struct {
	key   string
	value json.RawMessage
}

// object is a JSON object that keeps the order of its keys, so that the
// schema is printed in the order it is written in.
type object []member

// parseObject reads a JSON object, keeping the order of its keys.
func parseObject(raw []byte) (object, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("not a JSON object")
	}
	var o object
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		o = append(o, member{tok.(string), value})
	}
	return o, nil
}

// get returns the value of key.
func (o object) get(key string) (json.RawMessage, bool) {
	for _, m := range o {
		if m.key == key {
			return m.value, true
		}
	}
	return nil, false
}

// set replaces the value of key, or adds the key at the end.
func (o *object) set(key string, value json.RawMessage) {
	for i := range *o {
		if (*o)[i].key == key {
			(*o)[i].value = value
			return
		}
	}
	*o = append(*o, member{key, value})
}

// MarshalJSON writes the object with its keys in order.
func (o object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := json.Marshal(m.key)
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(m.value)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// rawJSON encodes v compactly, without escaping <, > and &.
func rawJSON(v any) (json.RawMessage, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(b.Bytes()), nil
}

// defName is the key of a command's result schema under $defs: its path with
// spaces replaced by underscores.
func defName(path string) string { return strings.ReplaceAll(path, " ", "_") }

// schema renders the JSON Schema of the envelope. For the program (cmd nil)
// it is the embedded schema with the result schema of every registered
// command under $defs. For one command it is narrowed to it: tool is that
// command's path, and result is null or the command's result schema.
func (a *App) schema(cmd *Command) ([]byte, error) {
	root, err := parseObject(envelopeSchema)
	if err != nil {
		return nil, fmt.Errorf("the embedded envelope schema: %w", err)
	}
	raw, _ := root.get("properties")
	properties, err := parseObject(raw)
	if err != nil {
		return nil, fmt.Errorf("the embedded envelope schema: properties: %w", err)
	}

	commands := slices.Clone(a.commands)
	if cmd != nil {
		commands = []*Command{cmd}
	}
	slices.SortFunc(commands, func(x, y *Command) int { return strings.Compare(x.Path, y.Path) })
	var defs object
	for _, c := range commands {
		if c.ResultSchema == nil {
			continue
		}
		if !json.Valid(c.ResultSchema) {
			return nil, fmt.Errorf("the result schema of %q is not JSON", c.Path)
		}
		defs.set(defName(c.Path), c.ResultSchema)
	}

	if cmd != nil {
		tool, err := rawJSON(object{{"type", json.RawMessage(`"string"`)}, {"const", mustJSON(cmd.Path)}})
		if err != nil {
			return nil, err
		}
		result := json.RawMessage(`{"type":"null"}`)
		if cmd.ResultSchema != nil {
			result = json.RawMessage(fmt.Sprintf(`{"oneOf":[{"type":"null"},{"$ref":"#/$defs/%s"}]}`, defName(cmd.Path)))
		}
		properties.set("tool", tool)
		properties.set("result", result)
	}
	props, err := rawJSON(properties)
	if err != nil {
		return nil, err
	}
	defsJSON, err := rawJSON(defs)
	if err != nil {
		return nil, err
	}
	root.set("properties", props)
	root.set("$defs", defsJSON)
	return encodeIndented(root)
}

// mustJSON encodes a string, which cannot fail.
func mustJSON(s string) json.RawMessage {
	b, _ := rawJSON(s)
	return b
}
