package agentspec

import (
	"testing"
)

// --- TS-06-40: InlineRefs resolves all $ref entries ---

// TestTS06_40_InlineRefsResolvesRefs verifies that InlineRefs recursively
// replaces all $ref entries with their $defs definitions and removes the
// $defs key from the result.
// Test Spec: TS-06-40, Requirement: 06-REQ-14.1
func TestTS06_40_InlineRefsResolvesRefs(t *testing.T) {
	schema := map[string]any{
		"$defs": map[string]any{
			"Foo": map[string]any{"type": "string"},
		},
		"properties": map[string]any{
			"bar": map[string]any{"$ref": "#/$defs/Foo"},
		},
	}

	result := InlineRefs(schema)

	// $defs should be removed.
	if _, hasDefs := result["$defs"]; hasDefs {
		t.Error("result contains $defs key; want $defs removed")
	}

	// No $ref at any depth.
	if containsKeyAtAnyDepth(result, "$ref") {
		t.Error("result contains $ref at some depth; want all $ref resolved")
	}

	// properties.bar should have type=string (inlined from Foo).
	props, ok := result["properties"].(map[string]any)
	if !ok {
		t.Fatalf("result[\"properties\"] is %T; want map[string]any", result["properties"])
	}
	bar, ok := props["bar"].(map[string]any)
	if !ok {
		t.Fatalf("properties[\"bar\"] is %T; want map[string]any", props["bar"])
	}
	if bar["type"] != "string" {
		t.Errorf("properties[\"bar\"][\"type\"] = %v; want %q", bar["type"], "string")
	}
}

// TestInlineRefs_NestedRefs verifies that InlineRefs handles nested $ref
// chains where a definition references another definition.
// Requirement: 06-REQ-14.1
func TestInlineRefs_NestedRefs(t *testing.T) {
	schema := map[string]any{
		"$defs": map[string]any{
			"Inner": map[string]any{"type": "integer"},
			"Outer": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"value": map[string]any{"$ref": "#/$defs/Inner"},
				},
			},
		},
		"properties": map[string]any{
			"wrapper": map[string]any{"$ref": "#/$defs/Outer"},
		},
	}

	result := InlineRefs(schema)

	if _, hasDefs := result["$defs"]; hasDefs {
		t.Error("result contains $defs key; want removed")
	}
	if containsKeyAtAnyDepth(result, "$ref") {
		t.Error("result contains $ref at some depth; want all resolved")
	}

	// Verify the nested structure is fully resolved.
	props, ok := result["properties"].(map[string]any)
	if !ok {
		t.Fatalf("result[\"properties\"] is %T; want map[string]any", result["properties"])
	}
	wrapper, ok := props["wrapper"].(map[string]any)
	if !ok {
		t.Fatalf("wrapper is %T; want map[string]any", props["wrapper"])
	}
	wrapperProps, ok := wrapper["properties"].(map[string]any)
	if !ok {
		t.Fatalf("wrapper[\"properties\"] is %T; want map[string]any", wrapper["properties"])
	}
	value, ok := wrapperProps["value"].(map[string]any)
	if !ok {
		t.Fatalf("value is %T; want map[string]any", wrapperProps["value"])
	}
	if value["type"] != "integer" {
		t.Errorf("value[\"type\"] = %v; want %q", value["type"], "integer")
	}
}

// --- TS-06-41: InlineRefs with no $ref entries ---

// TestTS06_41_InlineRefsNoRefs verifies that InlineRefs returns the schema
// unchanged (except removing $defs if present) when there are no $ref entries.
// Test Spec: TS-06-41, Requirement: 06-REQ-14.2
func TestTS06_41_InlineRefsNoRefs(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	}

	result := InlineRefs(schema)

	if result["type"] != "object" {
		t.Errorf("result[\"type\"] = %v; want %q", result["type"], "object")
	}

	props, ok := result["properties"].(map[string]any)
	if !ok {
		t.Fatalf("result[\"properties\"] is %T; want map[string]any", result["properties"])
	}
	name, ok := props["name"].(map[string]any)
	if !ok {
		t.Fatalf("name is %T; want map[string]any", props["name"])
	}
	if name["type"] != "string" {
		t.Errorf("name[\"type\"] = %v; want %q", name["type"], "string")
	}

	if _, hasDefs := result["$defs"]; hasDefs {
		t.Error("result contains $defs key; want removed even when no $ref present")
	}
}

// TestInlineRefs_RemovesDefsWithNoRefs verifies that InlineRefs removes
// the $defs key even when no $ref references them.
// Requirement: 06-REQ-14.2
func TestInlineRefs_RemovesDefsWithNoRefs(t *testing.T) {
	schema := map[string]any{
		"$defs": map[string]any{
			"Unused": map[string]any{"type": "boolean"},
		},
		"type": "object",
	}

	result := InlineRefs(schema)

	if _, hasDefs := result["$defs"]; hasDefs {
		t.Error("result contains $defs key; want removed")
	}
	if result["type"] != "object" {
		t.Errorf("result[\"type\"] = %v; want %q", result["type"], "object")
	}
}

// --- Edge Case: 06-REQ-14.E1 — Unresolvable $ref ---

// TestInlineRefs_UnresolvableRef verifies that InlineRefs leaves
// unresolvable $ref entries in place and continues processing the
// rest of the schema.
// Edge Case: 06-REQ-14.E1
func TestInlineRefs_UnresolvableRef(t *testing.T) {
	schema := map[string]any{
		"$defs": map[string]any{
			"Known": map[string]any{"type": "string"},
		},
		"properties": map[string]any{
			"good": map[string]any{"$ref": "#/$defs/Known"},
			"bad":  map[string]any{"$ref": "#/$defs/NonExistent"},
		},
	}

	result := InlineRefs(schema)

	if _, hasDefs := result["$defs"]; hasDefs {
		t.Error("result contains $defs key; want removed")
	}

	props, ok := result["properties"].(map[string]any)
	if !ok {
		t.Fatalf("result[\"properties\"] is %T; want map[string]any", result["properties"])
	}

	// good should be resolved.
	good, ok := props["good"].(map[string]any)
	if !ok {
		t.Fatalf("good is %T; want map[string]any", props["good"])
	}
	if good["type"] != "string" {
		t.Errorf("good[\"type\"] = %v; want %q", good["type"], "string")
	}

	// bad should still have its $ref (unresolvable).
	bad, ok := props["bad"].(map[string]any)
	if !ok {
		t.Fatalf("bad is %T; want map[string]any", props["bad"])
	}
	if _, hasRef := bad["$ref"]; !hasRef {
		t.Error("bad should retain $ref for unresolvable reference")
	}
}

// --- Edge Case: 06-REQ-14.E2 — Nil or empty input ---

// TestInlineRefs_NilInput verifies that InlineRefs returns an empty map
// without panicking when the input is nil.
// Edge Case: 06-REQ-14.E2
func TestInlineRefs_NilInput(t *testing.T) {
	result := InlineRefs(nil)
	if result == nil {
		t.Fatal("InlineRefs(nil) returned nil; want non-nil empty map")
	}
	if len(result) != 0 {
		t.Errorf("InlineRefs(nil) returned map with %d entries; want empty", len(result))
	}
}

// TestInlineRefs_EmptyInput verifies that InlineRefs returns an empty map
// without panicking when the input is an empty map.
// Edge Case: 06-REQ-14.E2
func TestInlineRefs_EmptyInput(t *testing.T) {
	result := InlineRefs(map[string]any{})
	if result == nil {
		t.Fatal("InlineRefs({}) returned nil; want non-nil empty map")
	}
	if len(result) != 0 {
		t.Errorf("InlineRefs({}) returned map with %d entries; want empty", len(result))
	}
}

// --- Edge Case: 06-REQ-13.E2 — Circular $ref chain ---

// TestInlineRefs_CircularRef verifies that InlineRefs detects circular
// $ref chains and breaks them at the point of recurrence rather than
// looping infinitely.
// Edge Case: 06-REQ-13.E2
func TestInlineRefs_CircularRef(t *testing.T) {
	schema := map[string]any{
		"$defs": map[string]any{
			"A": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"b": map[string]any{"$ref": "#/$defs/B"},
				},
			},
			"B": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"a": map[string]any{"$ref": "#/$defs/A"},
				},
			},
		},
		"properties": map[string]any{
			"root": map[string]any{"$ref": "#/$defs/A"},
		},
	}

	// Should not hang or panic.
	result := InlineRefs(schema)

	if result == nil {
		t.Fatal("InlineRefs returned nil for circular schema; want non-nil")
	}
	if _, hasDefs := result["$defs"]; hasDefs {
		t.Error("result contains $defs key; want removed")
	}
}

// --- TS-06-42: CleanSchema strips metadata ---

// TestTS06_42_CleanSchemaStripsMetadata verifies that CleanSchema recursively
// removes all title, default, and top-level $schema fields while preserving
// all description fields.
// Test Spec: TS-06-42, Requirement: 06-REQ-15.1
func TestTS06_42_CleanSchemaStripsMetadata(t *testing.T) {
	schema := map[string]any{
		"$schema":     "http://json-schema.org/draft-07/schema",
		"title":       "Root",
		"description": "Root desc",
		"properties": map[string]any{
			"foo": map[string]any{
				"title":       "Foo",
				"description": "Foo desc",
				"default":     "bar",
				"type":        "string",
			},
		},
	}

	result := CleanSchema(schema)

	// $schema should be removed at top level.
	if _, has := result["$schema"]; has {
		t.Error("result contains $schema; want removed")
	}

	// title should be removed at all levels.
	if _, has := result["title"]; has {
		t.Error("result contains top-level title; want removed")
	}

	// description should be preserved at top level.
	if result["description"] != "Root desc" {
		t.Errorf("result[\"description\"] = %v; want %q", result["description"], "Root desc")
	}

	// Check nested properties.
	props, ok := result["properties"].(map[string]any)
	if !ok {
		t.Fatalf("result[\"properties\"] is %T; want map[string]any", result["properties"])
	}
	foo, ok := props["foo"].(map[string]any)
	if !ok {
		t.Fatalf("foo is %T; want map[string]any", props["foo"])
	}

	if _, has := foo["title"]; has {
		t.Error("foo contains title; want removed")
	}
	if _, has := foo["default"]; has {
		t.Error("foo contains default; want removed")
	}
	if foo["description"] != "Foo desc" {
		t.Errorf("foo[\"description\"] = %v; want %q", foo["description"], "Foo desc")
	}
	if foo["type"] != "string" {
		t.Errorf("foo[\"type\"] = %v; want %q", foo["type"], "string")
	}
}

// --- TS-06-43: CleanSchema unchanged when no metadata ---

// TestTS06_43_CleanSchemaUnchanged verifies that CleanSchema returns the
// schema unchanged when it has no title, default, or $schema fields.
// Test Spec: TS-06-43, Requirement: 06-REQ-15.2
func TestTS06_43_CleanSchemaUnchanged(t *testing.T) {
	schema := map[string]any{
		"type":        "object",
		"description": "A clean schema",
		"properties": map[string]any{
			"x": map[string]any{
				"type":        "integer",
				"description": "An integer",
			},
		},
	}

	result := CleanSchema(schema)

	if result["type"] != "object" {
		t.Errorf("result[\"type\"] = %v; want %q", result["type"], "object")
	}
	if result["description"] != "A clean schema" {
		t.Errorf("result[\"description\"] = %v; want %q", result["description"], "A clean schema")
	}

	props, ok := result["properties"].(map[string]any)
	if !ok {
		t.Fatalf("result[\"properties\"] is %T; want map[string]any", result["properties"])
	}
	x, ok := props["x"].(map[string]any)
	if !ok {
		t.Fatalf("x is %T; want map[string]any", props["x"])
	}
	if x["type"] != "integer" {
		t.Errorf("x[\"type\"] = %v; want %q", x["type"], "integer")
	}
	if x["description"] != "An integer" {
		t.Errorf("x[\"description\"] = %v; want %q", x["description"], "An integer")
	}
}

// --- Edge Case: 06-REQ-15.E1 — Nil or empty input ---

// TestCleanSchema_NilInput verifies that CleanSchema returns an empty map
// without panicking when the input is nil.
// Edge Case: 06-REQ-15.E1
func TestCleanSchema_NilInput(t *testing.T) {
	result := CleanSchema(nil)
	if result == nil {
		t.Fatal("CleanSchema(nil) returned nil; want non-nil empty map")
	}
	if len(result) != 0 {
		t.Errorf("CleanSchema(nil) returned map with %d entries; want empty", len(result))
	}
}

// TestCleanSchema_EmptyInput verifies that CleanSchema returns an empty map
// without panicking when the input is empty.
// Edge Case: 06-REQ-15.E1
func TestCleanSchema_EmptyInput(t *testing.T) {
	result := CleanSchema(map[string]any{})
	if result == nil {
		t.Fatal("CleanSchema({}) returned nil; want non-nil empty map")
	}
	if len(result) != 0 {
		t.Errorf("CleanSchema({}) returned map with %d entries; want empty", len(result))
	}
}

// --- Edge Case: 06-REQ-15.E2 — Deeply nested objects ---

// TestCleanSchema_DeeplyNested verifies that CleanSchema recursively strips
// title and default fields from nested objects in properties and items arrays.
// Edge Case: 06-REQ-15.E2
func TestCleanSchema_DeeplyNested(t *testing.T) {
	schema := map[string]any{
		"type":        "object",
		"title":       "TopLevel",
		"description": "Top desc",
		"properties": map[string]any{
			"nested": map[string]any{
				"type":        "object",
				"title":       "Nested",
				"description": "Nested desc",
				"properties": map[string]any{
					"deep": map[string]any{
						"type":        "string",
						"title":       "Deep",
						"default":     "deep_default",
						"description": "Deep desc",
					},
				},
			},
			"arr": map[string]any{
				"type":  "array",
				"title": "Array",
				"items": map[string]any{
					"type":    "object",
					"title":   "Item",
					"default": "item_default",
					"properties": map[string]any{
						"field": map[string]any{
							"type":        "string",
							"title":       "Field",
							"description": "Field desc",
						},
					},
				},
			},
		},
	}

	result := CleanSchema(schema)

	// No title or default at any depth.
	if containsKeyAtAnyDepth(result, "title") {
		t.Error("result contains title at some depth; want all removed")
	}
	if containsKeyAtAnyDepth(result, "default") {
		t.Error("result contains default at some depth; want all removed")
	}

	// All description fields preserved.
	if result["description"] != "Top desc" {
		t.Errorf("top description = %v; want %q", result["description"], "Top desc")
	}

	props, ok := result["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties not map[string]any")
	}
	nested, ok := props["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested not map[string]any")
	}
	if nested["description"] != "Nested desc" {
		t.Errorf("nested description = %v; want %q", nested["description"], "Nested desc")
	}
}

// --- Correctness Property: 06-PROP-8 ---

// TestCleanSchema_PreservesAllDescriptions verifies the correctness
// property that every description field present in the input is present
// in the output at the same nesting path.
// Correctness Property: 06-PROP-8
func TestCleanSchema_PreservesAllDescriptions(t *testing.T) {
	schema := map[string]any{
		"description": "root",
		"properties": map[string]any{
			"a": map[string]any{
				"description": "a desc",
				"title":       "A Title",
				"properties": map[string]any{
					"b": map[string]any{
						"description": "b desc",
						"default":     42,
					},
				},
			},
		},
	}

	result := CleanSchema(schema)
	if result == nil {
		t.Fatal("CleanSchema returned nil; want non-nil map")
	}

	// Verify root description.
	if result["description"] != "root" {
		t.Errorf("root description = %v; want %q", result["description"], "root")
	}

	props, ok := result["properties"].(map[string]any)
	if !ok {
		t.Fatalf("result[\"properties\"] is %T; want map[string]any", result["properties"])
	}
	a, ok := props["a"].(map[string]any)
	if !ok {
		t.Fatalf("props[\"a\"] is %T; want map[string]any", props["a"])
	}
	if a["description"] != "a desc" {
		t.Errorf("a description = %v; want %q", a["description"], "a desc")
	}

	aProps, ok := a["properties"].(map[string]any)
	if !ok {
		t.Fatalf("a[\"properties\"] is %T; want map[string]any", a["properties"])
	}
	b, ok := aProps["b"].(map[string]any)
	if !ok {
		t.Fatalf("aProps[\"b\"] is %T; want map[string]any", aProps["b"])
	}
	if b["description"] != "b desc" {
		t.Errorf("b description = %v; want %q", b["description"], "b desc")
	}
}

// --- TS-06-38: ArtifactTool for requirements ---

// TestTS06_38_ArtifactToolRequirements verifies that ArtifactTool("requirements")
// returns a tool definition with a fully inlined, cleaned artifact schema used
// directly as input_schema (no intermediate 'content' property wrapper).
// Test Spec: TS-06-38, Requirement: 06-REQ-13.1
func TestTS06_38_ArtifactToolRequirements(t *testing.T) {
	tools := ArtifactTool("requirements")
	if len(tools) != 1 {
		t.Fatalf("len(ArtifactTool(\"requirements\")) = %d; want 1", len(tools))
	}

	tool := tools[0]
	name, ok := tool["name"].(string)
	if !ok || name != "submit_requirements" {
		t.Errorf("tool[\"name\"] = %v; want %q", tool["name"], "submit_requirements")
	}

	// input_schema is the cleaned artifact schema directly — no 'content' wrapper.
	inputSchema, ok := tool["input_schema"].(map[string]any)
	if !ok {
		t.Fatalf("input_schema is %T; want map[string]any", tool["input_schema"])
	}
	props, ok := inputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("input_schema[\"properties\"] is %T; want map[string]any", inputSchema["properties"])
	}

	// The flat schema must NOT have a 'content' wrapper key.
	if _, hasContent := props["content"]; hasContent {
		t.Error("input_schema.properties has a 'content' key; want flat artifact fields at top level")
	}

	// The flat schema should expose the artifact-level 'requirements' key directly.
	if _, hasReqs := props["requirements"]; !hasReqs {
		t.Error("input_schema.properties missing 'requirements' key; want top-level artifact field")
	}

	// The schema should have no $ref, $defs, title, default, or $schema anywhere.
	if containsKeyAtAnyDepth(inputSchema, "$ref") {
		t.Error("input_schema contains $ref at some depth; want all resolved")
	}
	if containsKeyAtAnyDepth(inputSchema, "$defs") {
		t.Error("input_schema contains $defs; want removed")
	}
	if containsKeyAtAnyDepth(inputSchema, "title") {
		t.Error("input_schema contains title at some depth; want removed")
	}
	if containsKeyAtAnyDepth(inputSchema, "default") {
		t.Error("input_schema contains default at some depth; want removed")
	}
	if _, has := inputSchema["$schema"]; has {
		t.Error("input_schema contains $schema; want removed")
	}
}

// TestArtifactTool_PreservesDescriptions verifies that ArtifactTool
// preserves description fields in the cleaned schema.
// Requirement: 06-REQ-13.1 (description preservation)
func TestArtifactTool_PreservesDescriptions(t *testing.T) {
	tools := ArtifactTool("requirements")
	if len(tools) != 1 {
		t.Fatalf("ArtifactTool(\"requirements\") returned %d entries; want 1", len(tools))
	}

	// input_schema is the cleaned artifact schema directly.
	inputSchema, ok := tools[0]["input_schema"].(map[string]any)
	if !ok {
		t.Fatalf("input_schema is %T; want map[string]any", tools[0]["input_schema"])
	}

	// The requirements schema should have description fields at some depth.
	if !containsKeyAtAnyDepth(inputSchema, "description") {
		t.Error("input_schema has no description fields; want descriptions preserved")
	}
}

// TestArtifactTool_TestSpec verifies that ArtifactTool works for the
// test_spec artifact type.
// Requirement: 06-REQ-13.1
func TestArtifactTool_TestSpec(t *testing.T) {
	tools := ArtifactTool("test_spec")
	if len(tools) != 1 {
		t.Fatalf("len(ArtifactTool(\"test_spec\")) = %d; want 1", len(tools))
	}

	name, ok := tools[0]["name"].(string)
	if !ok || name != "submit_test_spec" {
		t.Errorf("tool name = %v; want %q", tools[0]["name"], "submit_test_spec")
	}
}

// TestArtifactTool_Tasks verifies that ArtifactTool works for the
// tasks artifact type.
// Requirement: 06-REQ-13.1
func TestArtifactTool_Tasks(t *testing.T) {
	tools := ArtifactTool("tasks")
	if len(tools) != 1 {
		t.Fatalf("len(ArtifactTool(\"tasks\")) = %d; want 1", len(tools))
	}

	name, ok := tools[0]["name"].(string)
	if !ok || name != "submit_tasks" {
		t.Errorf("tool name = %v; want %q", tools[0]["name"], "submit_tasks")
	}
}

// --- TS-06-39: ArtifactTool with unknown artifact name ---

// TestTS06_39_ArtifactToolUnknown verifies that ArtifactTool returns an
// empty slice without panicking when called with an artifactName not in
// {requirements, test_spec, tasks}.
// Test Spec: TS-06-39, Requirement: 06-REQ-13.2
func TestTS06_39_ArtifactToolUnknown(t *testing.T) {
	tools := ArtifactTool("unknown_artifact")
	if len(tools) != 0 {
		t.Errorf("len(ArtifactTool(\"unknown_artifact\")) = %d; want 0", len(tools))
	}
}

// TestArtifactTool_EmptyName verifies that ArtifactTool returns an empty
// slice when called with an empty string.
// Edge Case: 06-REQ-13.E1
func TestArtifactTool_EmptyName(t *testing.T) {
	tools := ArtifactTool("")
	if len(tools) != 0 {
		t.Errorf("len(ArtifactTool(\"\")) = %d; want 0", len(tools))
	}
}

// --- Helper functions ---

// containsKeyAtAnyDepth recursively checks if a map[string]any contains
// the given key at any nesting level.
func containsKeyAtAnyDepth(m map[string]any, key string) bool {
	for k, v := range m {
		if k == key {
			return true
		}
		switch val := v.(type) {
		case map[string]any:
			if containsKeyAtAnyDepth(val, key) {
				return true
			}
		case []any:
			for _, item := range val {
				if itemMap, ok := item.(map[string]any); ok {
					if containsKeyAtAnyDepth(itemMap, key) {
						return true
					}
				}
			}
		}
	}
	return false
}
