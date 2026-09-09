package agentspec

import (
	"maps"
	"strings"
)

// InlineRefs recursively resolves all $ref references in a JSON Schema
// map against the $defs section, replacing each $ref with the definition
// it points to. The $defs key is removed from the top-level result.
//
// If a $ref points to a definition not present in $defs, the $ref is
// left in place. If the input is nil or empty, an empty map is returned.
// Circular $ref chains are detected and broken at the point of recurrence.
func InlineRefs(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return map[string]any{}
	}

	// Extract $defs from the schema.
	var defs map[string]any
	if d, ok := schema["$defs"]; ok {
		if dm, ok := d.(map[string]any); ok {
			defs = dm
		}
	}

	// Deep copy the schema, resolving $ref entries along the way.
	result := inlineRefsWalk(schema, defs, nil)

	// Remove $defs from the top-level result.
	delete(result, "$defs")

	return result
}

// inlineRefsWalk recursively copies a map, resolving $ref entries against
// defs. The resolving set tracks which definition names are currently being
// resolved to detect circular references.
func inlineRefsWalk(m map[string]any, defs map[string]any, resolving map[string]bool) map[string]any {
	// If this node is a $ref, try to resolve it.
	if ref, ok := m["$ref"]; ok && len(m) == 1 {
		refStr, ok := ref.(string)
		if ok {
			defName := refToDefName(refStr)
			if defName != "" && defs != nil {
				if defVal, exists := defs[defName]; exists {
					if defMap, ok := defVal.(map[string]any); ok {
						// Check for circular reference.
						if resolving != nil && resolving[defName] {
							// Break the cycle: return the $ref as-is.
							return map[string]any{"$ref": refStr}
						}
						// Mark as resolving and recurse into the definition.
						newResolving := make(map[string]bool, len(resolving)+1)
						maps.Copy(newResolving, resolving)
						newResolving[defName] = true
						return inlineRefsWalk(defMap, defs, newResolving)
					}
				}
			}
		}
		// Unresolvable $ref: return as-is (deep copy).
		return map[string]any{"$ref": ref}
	}

	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyAndResolve(v, defs, resolving)
	}
	return out
}

// deepCopyAndResolve deep-copies a value, resolving any $ref entries found
// in nested maps.
func deepCopyAndResolve(v any, defs map[string]any, resolving map[string]bool) any {
	switch val := v.(type) {
	case map[string]any:
		return inlineRefsWalk(val, defs, resolving)
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = deepCopyAndResolve(item, defs, resolving)
		}
		return out
	default:
		return v
	}
}

// refToDefName extracts the definition name from a $ref string of the form
// "#/$defs/Name". Returns empty string if the format doesn't match.
func refToDefName(ref string) string {
	const prefix = "#/$defs/"
	if strings.HasPrefix(ref, prefix) {
		return ref[len(prefix):]
	}
	return ""
}

// CleanSchema recursively strips metadata noise from a JSON Schema map
// for the Anthropic API. It removes all title fields at every nesting
// level, all default fields at every nesting level, and the top-level
// $schema field. All description fields are preserved.
//
// If the input is nil or empty, an empty map is returned.
func CleanSchema(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return map[string]any{}
	}
	return cleanSchemaWalk(schema, true)
}

// cleanSchemaWalk recursively copies a map, stripping title and default
// keys at every level, and $schema at the top level only.
func cleanSchemaWalk(m map[string]any, isTopLevel bool) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		// Skip title and default at every level.
		if k == "title" || k == "default" {
			continue
		}
		// Skip $schema at top level only.
		if k == "$schema" && isTopLevel {
			continue
		}
		out[k] = cleanCopyValue(v)
	}
	return out
}

// cleanCopyValue deep-copies a value, stripping title and default from
// any nested maps.
func cleanCopyValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return cleanSchemaWalk(val, false)
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = cleanCopyValue(item)
		}
		return out
	default:
		return v
	}
}
