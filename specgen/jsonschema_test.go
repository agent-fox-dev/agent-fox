package specgen

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/schema"

	"github.com/agent-fox-dev/agentfox/afspec"
)

// marshalSchema renders a converted schema the way a provider would send it.
func marshalSchema(t *testing.T, s *schema.Schema) map[string]any {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshalling the schema: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("re-reading the schema: %v", err)
	}
	return m
}

// TestAPropertyNamedLikeAKeywordSurvivesTheConversion is the regression this
// converter exists for.
//
// The map-walking pipeline it replaces deleted keys by name at every depth,
// including inside `properties`, whose keys are property NAMES. The v2 test
// object has properties called `title` and `then`; both were deleted from the
// tool schema while `required` still listed them and additionalProperties
// stayed false — so the schema declared for submit_test_spec could not be
// satisfied by any argument object the model could construct.
func TestAPropertyNamedLikeAKeywordSurvivesTheConversion(t *testing.T) {
	s, err := ArtifactSchema(afspec.StepTestSpec)
	if err != nil {
		t.Fatal(err)
	}
	test := s.Properties["tests"].Items
	if test == nil {
		t.Fatal("the tests array declares no item schema")
	}
	for _, name := range []string{"title", "then", "given", "when", "id", "kind", "verifies"} {
		if test.Properties[name] == nil {
			t.Errorf("the test object lost its %q property", name)
		}
	}
}

// TestEveryRequiredPropertyIsDeclared is the general form of the bug: whatever
// the converter drops, it must never leave a schema asking for a field it does
// not describe while forbidding anything it does not describe.
func TestEveryRequiredPropertyIsDeclared(t *testing.T) {
	for _, step := range afspec.GenerationSteps {
		s, err := ArtifactSchema(step)
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		walkSchema(s, func(path string, node *schema.Schema) {
			if node.Type != schema.TypeObject || len(node.Required) == 0 {
				return
			}
			for _, name := range node.Required {
				if node.Properties[name] == nil {
					t.Errorf("%s%s: %q is required but not declared", step, path, name)
				}
			}
		})
	}
}

func TestConversionResolvesRefsAndDropsDefs(t *testing.T) {
	s, err := ArtifactSchema(afspec.StepTasks)
	if err != nil {
		t.Fatal(err)
	}
	m := marshalSchema(t, s)
	if _, ok := m["$defs"]; ok {
		t.Error("$defs survived into the tool schema")
	}
	// tasks.items was a $ref; it must have become the definition itself.
	task := s.Properties["tasks"].Items
	if task == nil || len(task.Properties) == 0 {
		t.Fatal("the task item schema is empty, so its $ref was not resolved")
	}
	if raw, err := json.Marshal(s); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(raw), `"$ref"`) {
		t.Error("a $ref survived into the tool schema, where it is a dangling pointer")
	}
}

func TestMetadataAndConditionalsAreDropped(t *testing.T) {
	// The conditionals are enforced by afspec.ValidateGenerationStep, whose
	// message names the rule and reaches the model through the submit tool.
	// Carrying them here would ask a tool-schema validator to interpret
	// constructs the vendors do not uniformly support.
	for _, step := range afspec.GenerationSteps {
		s, err := ArtifactSchema(step)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		for _, kw := range []string{`"allOf"`, `"if"`, `"$id"`} {
			if strings.Contains(string(raw), kw) {
				t.Errorf("%s: %s survived into the tool schema", step, kw)
			}
		}
		// $schema is a PROPERTY of every v2 artifact, so its presence as a
		// key proves nothing; what must be gone is the meta-schema
		// declaration, which is the one whose value is a schema-store URL.
		if strings.Contains(string(raw), `"$schema":"https://json-schema.org`) {
			t.Errorf("%s: the meta-schema declaration survived into the tool schema", step)
		}
		if m := marshalSchema(t, s); m["$schema"] != nil {
			t.Errorf("%s: a top-level $schema keyword survived: %v", step, m["$schema"])
		}
	}
}

func TestPropertyOrderFollowsTheDocument(t *testing.T) {
	// Property order is model-visible and part of the provider's cache
	// prefix. A map cannot carry it, which is why the conversion decodes
	// through jsonx and schema.Schema keeps PropertyOrder.
	s, err := ArtifactSchema(afspec.StepTestSpec)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"$schema", "spec_id", "spec_name", "schema_version", "tests"}
	if !slices.Equal(s.PropertyOrder, want) {
		t.Errorf("PropertyOrder = %v, want %v", s.PropertyOrder, want)
	}
}

func TestConstraintsSurviveTheConversion(t *testing.T) {
	s, err := ArtifactSchema(afspec.StepTestSpec)
	if err != nil {
		t.Fatal(err)
	}
	test := s.Properties["tests"].Items
	kind := test.Properties["kind"]
	if len(kind.Enum) != 4 {
		t.Errorf("the kind enum has %d values, want 4", len(kind.Enum))
	}
	if test.Properties["id"].Pattern == "" {
		t.Error("the id pattern was lost")
	}
	if s.Properties["tests"].MinItems == nil || *s.Properties["tests"].MinItems != 1 {
		t.Error("minItems was lost from the tests array")
	}
	if test.Properties["verifies"].UniqueItems == nil {
		t.Error("uniqueItems was lost")
	}
	if s.Properties["schema_version"].HasConst == false {
		t.Error("the schema_version const was lost")
	}
	if s.AdditionalProperties == nil || s.AdditionalProperties.Allowed {
		t.Error("additionalProperties:false was lost")
	}
}

func TestDescriptionsSurviveTheConversion(t *testing.T) {
	s, err := ArtifactSchema(afspec.StepRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if s.Description == "" {
		t.Error("the root description was dropped; it is what tells the model what it is filling in")
	}
}

func TestAnUnmodelledKeywordRidesThrough(t *testing.T) {
	s, err := ToolSchema([]byte(`{"type":"object","x-vendor-hint":"keep me",
		"properties":{"a":{"type":"string"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	m := marshalSchema(t, s)
	if m["x-vendor-hint"] != "keep me" {
		t.Errorf("an unmodelled keyword was dropped: %v", m)
	}
}

func TestATypeArrayWithNullBecomesNullable(t *testing.T) {
	s, err := ToolSchema([]byte(`{"type":["string","null"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if s.Type != schema.TypeString || !s.Nullable {
		t.Errorf("type=%q nullable=%v, want a nullable string", s.Type, s.Nullable)
	}
}

func TestACircularRefIsBrokenRatherThanFollowed(t *testing.T) {
	s, err := ToolSchema([]byte(`{"type":"object","properties":{"node":{"$ref":"#/$defs/n"}},
		"$defs":{"n":{"type":"object","properties":{"child":{"$ref":"#/$defs/n"}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	node := s.Properties["node"]
	if node == nil || node.Properties["child"] == nil {
		t.Fatal("the first level of the cycle was not resolved")
	}
	if len(node.Properties["child"].Properties) != 0 {
		t.Error("the cycle was followed instead of being broken")
	}
}

func TestMalformedSchemaBytesAreAnError(t *testing.T) {
	if _, err := ToolSchema([]byte(`{"type":`)); err == nil {
		t.Error("expected an error for truncated JSON")
	}
	if _, err := ToolSchema([]byte(`[1,2,3]`)); err == nil {
		t.Error("expected an error for a non-object root")
	}
}

func TestArtifactSchemaIsCachedAndStable(t *testing.T) {
	// The conversion is pure and runs once per step per process; two calls
	// must not disagree, and one caller must not be able to change what the
	// next one sees.
	for _, step := range afspec.GenerationSteps {
		a, err := ArtifactSchema(step)
		if err != nil {
			t.Fatal(err)
		}
		b, err := ArtifactSchema(step)
		if err != nil {
			t.Fatal(err)
		}
		if a != b {
			t.Errorf("%s: two calls returned different schema values", step)
		}
	}
}

func TestArtifactSchemaRefusesAnUnknownStep(t *testing.T) {
	if _, err := ArtifactSchema(afspec.GenerationStep("nonsense")); err == nil {
		t.Error("expected an error for an unknown generation step")
	}
}

// walkSchema visits every schema node reachable from s.
func walkSchema(s *schema.Schema, fn func(path string, node *schema.Schema)) {
	var walk func(string, *schema.Schema)
	seen := map[*schema.Schema]bool{}
	walk = func(path string, node *schema.Schema) {
		if node == nil || seen[node] {
			return
		}
		seen[node] = true
		fn(path, node)
		for _, name := range node.PropertyOrder {
			walk(path+"."+name, node.Properties[name])
		}
		walk(path+"[]", node.Items)
		if node.AdditionalProperties != nil {
			walk(path+".additionalProperties", node.AdditionalProperties.Schema)
		}
		for i, alt := range node.AnyOf {
			walk(path+".anyOf["+string(rune('0'+i))+"]", alt)
		}
		for i, alt := range node.OneOf {
			walk(path+".oneOf["+string(rune('0'+i))+"]", alt)
		}
	}
	walk("", s)
}
