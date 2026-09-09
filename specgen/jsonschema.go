package specgen

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/agentfox/agentkit-go/jsonx"
	"github.com/agentfox/agentkit-go/schema"
)

// ToolSchema converts an afspec JSON Schema document into the structured
// schema value a tool declares (core.Tool.InputSchema).
//
// It replaces the map[string]any pipeline of InlineRefs + CleanSchema, and the
// difference is not cosmetic. That pipeline walked every map in the document
// and deleted keys by name at every depth, which is correct for a schema
// object and wrong for a `properties` map, whose keys are property NAMES. The
// v2 test object has properties called `title` and `then`; both were deleted
// from the tool schema while `required` still listed them and
// additionalProperties stayed false, so the schema handed to the model for
// submit_test_spec could not be satisfied by any argument object. This walk
// knows which position it is in and only drops a keyword where a keyword is
// what it is reading.
//
// Decoding goes through jsonx rather than encoding/json because Go marshals
// map keys in sorted order unconditionally, and property order is
// model-visible: it is the order the model is shown the fields and, through
// the request body, part of the provider's cache prefix. schema.Schema carries
// PropertyOrder for exactly this reason, and a map cannot carry it.
func ToolSchema(raw []byte) (*schema.Schema, error) {
	v, err := jsonx.DecodeOrdered(raw)
	if err != nil {
		return nil, fmt.Errorf("specgen: decoding schema: %w", err)
	}
	if v.Kind != jsonx.KindObject {
		return nil, fmt.Errorf("specgen: schema root is not an object")
	}

	defs := map[string]jsonx.OrderedValue{}
	if d, ok := v.Object.Get("$defs"); ok && d.Kind == jsonx.KindObject {
		for _, m := range d.Object {
			defs[m.Key] = m.Value
		}
	}

	c := &schemaConv{defs: defs}
	s := c.object(v.Object, nil)
	if s == nil {
		return nil, fmt.Errorf("specgen: schema converted to nothing")
	}
	return s, nil
}

// droppedKeywords are removed wherever a schema keyword is being read.
//
// $schema, $id, title and default are metadata: they cost prompt tokens and
// say nothing the model can act on. The conditional keywords are dropped
// because the tool schema is a structural description, not the validation
// gate. afspec.ValidateGenerationStep runs the real schema — conditionals
// included — and its message names the rule that failed; the submit tool hands
// that message straight back to the model. Leaving `if`/`then`/`else` in would
// ask a tool-schema validator to interpret constructs the vendors do not
// uniformly support, in exchange for a rule that is already enforced somewhere
// that can explain itself.
//
// Every anyOf and not in the v2 schemas sits inside one of these allOf blocks,
// so dropping allOf removes them all; anyOf and oneOf are modelled rather than
// dropped, because a future schema could use one outside a conditional and
// silently losing it would be worse than emitting it.
var droppedKeywords = map[string]bool{
	"$schema": true,
	"$id":     true,
	"$anchor": true,
	"$defs":   true,
	"title":   true,
	"default": true,
	"allOf":   true,
	"if":      true,
	"then":    true,
	"else":    true,
	"not":     true,
}

type schemaConv struct {
	defs map[string]jsonx.OrderedValue
}

// schema converts one value in SCHEMA position.
func (c *schemaConv) schema(v jsonx.OrderedValue, resolving []string) *schema.Schema {
	switch v.Kind {
	case jsonx.KindObject:
		return c.object(v.Object, resolving)
	case jsonx.KindBool:
		// `true` accepts anything, `false` accepts nothing. Neither is
		// expressible as a typed Schema and neither appears in the v2
		// schemas; an untyped schema is the closest honest answer.
		return &schema.Schema{}
	default:
		return nil
	}
}

func (c *schemaConv) object(o jsonx.OrderedObject, resolving []string) *schema.Schema {
	// A lone $ref is replaced by what it names. A cycle is broken by leaving
	// an untyped schema behind rather than recursing forever; the v2 schemas
	// have no cycles, and an untyped node is a description the model can still
	// fill in, where a stack overflow is not.
	if ref, ok := o.Get("$ref"); ok && ref.Kind == jsonx.KindString {
		name := defName(scalarString(ref))
		if name != "" {
			if contains(resolving, name) {
				return &schema.Schema{}
			}
			if def, exists := c.defs[name]; exists {
				return c.schema(def, append(resolving, name))
			}
		}
		// A $ref this document cannot resolve is dropped rather than
		// forwarded: $ref in a tool schema is a dangling pointer for the
		// model as much as for a validator.
		return &schema.Schema{}
	}

	s := &schema.Schema{}
	for _, m := range o {
		if droppedKeywords[m.Key] {
			continue
		}
		c.keyword(s, m.Key, m.Value, resolving)
	}
	return s
}

func (c *schemaConv) keyword(s *schema.Schema, key string, v jsonx.OrderedValue, resolving []string) {
	switch key {
	case "type":
		if v.Kind == jsonx.KindString {
			s.Type = schema.Type(scalarString(v))
		} else if v.Kind == jsonx.KindArray {
			// ["string","null"] is Schema.Nullable plus the concrete type.
			for _, t := range v.Array {
				if t.Kind != jsonx.KindString {
					continue
				}
				if name := scalarString(t); name == "null" {
					s.Nullable = true
				} else {
					s.Type = schema.Type(name)
				}
			}
		}
	case "description":
		s.Description = scalarString(v)
	case "properties":
		if v.Kind != jsonx.KindObject {
			return
		}
		// Property NAMES, not keywords: nothing is dropped by name here.
		s.Properties = make(map[string]*schema.Schema, len(v.Object))
		s.PropertyOrder = make([]string, 0, len(v.Object))
		for _, p := range v.Object {
			sub := c.schema(p.Value, resolving)
			if sub == nil {
				continue
			}
			s.Properties[p.Key] = sub
			s.PropertyOrder = append(s.PropertyOrder, p.Key)
		}
	case "required":
		s.Required = stringArray(v)
	case "items":
		s.Items = c.schema(v, resolving)
	case "additionalProperties":
		switch v.Kind {
		case jsonx.KindBool:
			s.AdditionalProperties = &schema.AdditionalProperties{Allowed: scalarString(v) == "true"}
		case jsonx.KindObject:
			s.AdditionalProperties = &schema.AdditionalProperties{Allowed: true, Schema: c.schema(v, resolving)}
		}
	case "enum":
		if v.Kind != jsonx.KindArray {
			return
		}
		for _, e := range v.Array {
			b, err := json.Marshal(e)
			if err != nil {
				continue
			}
			s.Enum = append(s.Enum, json.RawMessage(b))
		}
	case "const":
		if b, err := json.Marshal(v); err == nil {
			s.Const, s.HasConst = json.RawMessage(b), true
		}
	case "anyOf":
		s.AnyOf = c.schemaArray(v, resolving)
	case "oneOf":
		s.OneOf = c.schemaArray(v, resolving)
	case "pattern":
		s.Pattern = scalarString(v)
	case "format":
		s.Format = scalarString(v)
	case "minLength":
		s.MinLength = jsonIntPtr(v)
	case "maxLength":
		s.MaxLength = jsonIntPtr(v)
	case "minItems":
		s.MinItems = jsonIntPtr(v)
	case "maxItems":
		s.MaxItems = jsonIntPtr(v)
	case "uniqueItems":
		if v.Kind == jsonx.KindBool {
			b := scalarString(v) == "true"
			s.UniqueItems = &b
		}
	case "minimum":
		s.Minimum = jsonFloatPtr(v)
	case "maximum":
		s.Maximum = jsonFloatPtr(v)
	case "exclusiveMinimum":
		s.ExclusiveMinimum = jsonFloatPtr(v)
	case "exclusiveMaximum":
		s.ExclusiveMaximum = jsonFloatPtr(v)
	case "multipleOf":
		s.MultipleOf = jsonFloatPtr(v)
	default:
		// An unmodelled keyword rides through in authored order rather than
		// being silently dropped: the schema is somebody else's document and
		// this converter is not the authority on which of its keywords matter.
		s.Extra = append(s.Extra, jsonx.Member{Key: key, Value: v})
	}
}

func (c *schemaConv) schemaArray(v jsonx.OrderedValue, resolving []string) []*schema.Schema {
	if v.Kind != jsonx.KindArray {
		return nil
	}
	out := make([]*schema.Schema, 0, len(v.Array))
	for _, alt := range v.Array {
		if sub := c.schema(alt, resolving); sub != nil {
			out = append(out, sub)
		}
	}
	return out
}

// scalarString is the text of a string, number or bool scalar. A JSON string
// is unquoted; anything else is its verbatim literal.
func scalarString(v jsonx.OrderedValue) string {
	if v.Kind == jsonx.KindString {
		var s string
		if err := json.Unmarshal(v.Scalar, &s); err == nil {
			return s
		}
	}
	return string(v.Scalar)
}

func stringArray(v jsonx.OrderedValue) []string {
	if v.Kind != jsonx.KindArray {
		return nil
	}
	out := make([]string, 0, len(v.Array))
	for _, e := range v.Array {
		if e.Kind == jsonx.KindString {
			out = append(out, scalarString(e))
		}
	}
	return out
}

func jsonIntPtr(v jsonx.OrderedValue) *int {
	if v.Kind != jsonx.KindNumber {
		return nil
	}
	n, err := strconv.Atoi(string(v.Scalar))
	if err != nil {
		return nil
	}
	return &n
}

func jsonFloatPtr(v jsonx.OrderedValue) *float64 {
	if v.Kind != jsonx.KindNumber {
		return nil
	}
	f, err := strconv.ParseFloat(string(v.Scalar), 64)
	if err != nil {
		return nil
	}
	return &f
}

func defName(ref string) string {
	const prefix = "#/$defs/"
	if strings.HasPrefix(ref, prefix) {
		return ref[len(prefix):]
	}
	return ""
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
