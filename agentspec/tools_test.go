package agentspec

import (
	"sync"
	"testing"
)

// --- TS-06-36: AssessmentTools returns correct tool definition ---

// TestTS06_36_AssessmentToolsSchema verifies that AssessmentTools() returns a
// slice with exactly one entry for submit_assessment with the correct input
// schema fields: quality (enum), summary (string), gaps (array), questions
// (array of objects).
// Test Spec: TS-06-36, Requirement: 06-REQ-11.1
func TestTS06_36_AssessmentToolsSchema(t *testing.T) {
	tools := AssessmentTools()
	if len(tools) != 1 {
		t.Fatalf("len(AssessmentTools()) = %d; want 1", len(tools))
	}

	tool := tools[0]
	name, ok := tool["name"].(string)
	if !ok || name != "submit_assessment" {
		t.Errorf("tool[\"name\"] = %v; want %q", tool["name"], "submit_assessment")
	}

	// Verify input_schema exists and has properties.
	inputSchema, ok := tool["input_schema"].(map[string]any)
	if !ok {
		t.Fatalf("tool[\"input_schema\"] is %T; want map[string]any", tool["input_schema"])
	}

	props, ok := inputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("input_schema[\"properties\"] is %T; want map[string]any", inputSchema["properties"])
	}

	// Check quality field has enum.
	qualityProp, ok := props["quality"].(map[string]any)
	if !ok {
		t.Fatalf("properties[\"quality\"] is %T; want map[string]any", props["quality"])
	}
	qualityEnum, ok := qualityProp["enum"].([]any)
	if !ok {
		t.Fatalf("quality[\"enum\"] is %T; want []any", qualityProp["enum"])
	}
	expectedEnums := map[string]bool{
		"ready":            false,
		"needs_refinement": false,
		"incomplete":       false,
	}
	for _, v := range qualityEnum {
		s, ok := v.(string)
		if !ok {
			continue
		}
		expectedEnums[s] = true
	}
	for k, found := range expectedEnums {
		if !found {
			t.Errorf("quality enum missing value %q", k)
		}
	}

	// Check summary field is string type.
	summaryProp, ok := props["summary"].(map[string]any)
	if !ok {
		t.Fatalf("properties[\"summary\"] is %T; want map[string]any", props["summary"])
	}
	if summaryProp["type"] != "string" {
		t.Errorf("summary[\"type\"] = %v; want %q", summaryProp["type"], "string")
	}

	// Check gaps field is array type.
	gapsProp, ok := props["gaps"].(map[string]any)
	if !ok {
		t.Fatalf("properties[\"gaps\"] is %T; want map[string]any", props["gaps"])
	}
	if gapsProp["type"] != "array" {
		t.Errorf("gaps[\"type\"] = %v; want %q", gapsProp["type"], "array")
	}

	// Check questions field is array type.
	questionsProp, ok := props["questions"].(map[string]any)
	if !ok {
		t.Fatalf("properties[\"questions\"] is %T; want map[string]any", props["questions"])
	}
	if questionsProp["type"] != "array" {
		t.Errorf("questions[\"type\"] = %v; want %q", questionsProp["type"], "array")
	}
}

// TestAssessmentTools_ReturnsNewSlice verifies that each call to
// AssessmentTools() returns a distinct slice value, preventing callers
// from mutating the shared definition.
// Edge Case: 06-REQ-11.E1
func TestAssessmentTools_ReturnsNewSlice(t *testing.T) {
	tools1 := AssessmentTools()
	tools2 := AssessmentTools()

	if len(tools1) == 0 || len(tools2) == 0 {
		t.Fatalf("AssessmentTools() returned empty slice; want non-empty")
	}

	// Mutate tools1 and verify tools2 is unaffected.
	tools1[0]["name"] = "mutated"

	tools3 := AssessmentTools()
	if len(tools3) == 0 {
		t.Fatal("AssessmentTools() returned empty slice after mutation")
	}

	name, ok := tools3[0]["name"].(string)
	if !ok || name != "submit_assessment" {
		t.Errorf("after mutation, AssessmentTools()[0][\"name\"] = %v; want %q (new slice expected)", tools3[0]["name"], "submit_assessment")
	}
}

// TestAssessmentTools_QuestionsObjectSchema verifies that the questions
// field contains an items schema with object properties including id,
// text, context, options, and required.
// Test Spec: TS-06-36 (detail), Requirement: 06-REQ-11.1
func TestAssessmentTools_QuestionsObjectSchema(t *testing.T) {
	tools := AssessmentTools()
	if len(tools) != 1 {
		t.Fatalf("len(AssessmentTools()) = %d; want 1", len(tools))
	}

	inputSchema, ok := tools[0]["input_schema"].(map[string]any)
	if !ok {
		t.Fatalf("input_schema not map[string]any")
	}
	props, ok := inputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties not map[string]any")
	}
	questionsProp, ok := props["questions"].(map[string]any)
	if !ok {
		t.Fatalf("questions not map[string]any")
	}

	// The items should be an object with its own properties.
	items, ok := questionsProp["items"].(map[string]any)
	if !ok {
		t.Fatalf("questions[\"items\"] is %T; want map[string]any", questionsProp["items"])
	}
	itemProps, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatalf("items[\"properties\"] is %T; want map[string]any", items["properties"])
	}

	requiredFields := []string{"id", "text", "context", "options", "required"}
	for _, field := range requiredFields {
		if _, exists := itemProps[field]; !exists {
			t.Errorf("questions items properties missing field %q", field)
		}
	}
}

// --- TS-06-37: RefinementTools returns correct tool definitions ---

// TestTS06_37_RefinementToolsSchema verifies that RefinementTools() returns
// a slice with exactly two entries: submit_prd_update at index 0 and
// submit_assessment at index 1, with submit_prd_update having an
// updated_prd string field.
// Test Spec: TS-06-37, Requirement: 06-REQ-12.1
func TestTS06_37_RefinementToolsSchema(t *testing.T) {
	tools := RefinementTools()
	if len(tools) != 2 {
		t.Fatalf("len(RefinementTools()) = %d; want 2", len(tools))
	}

	// Check first tool is submit_prd_update.
	tool0 := tools[0]
	name0, ok := tool0["name"].(string)
	if !ok || name0 != "submit_prd_update" {
		t.Errorf("tools[0][\"name\"] = %v; want %q", tool0["name"], "submit_prd_update")
	}

	// Check submit_prd_update has updated_prd field.
	schema0, ok := tool0["input_schema"].(map[string]any)
	if !ok {
		t.Fatalf("tools[0][\"input_schema\"] is %T; want map[string]any", tool0["input_schema"])
	}
	props0, ok := schema0["properties"].(map[string]any)
	if !ok {
		t.Fatalf("input_schema[\"properties\"] is %T; want map[string]any", schema0["properties"])
	}
	updatedPRD, ok := props0["updated_prd"].(map[string]any)
	if !ok {
		t.Fatalf("properties[\"updated_prd\"] is %T; want map[string]any", props0["updated_prd"])
	}
	if updatedPRD["type"] != "string" {
		t.Errorf("updated_prd[\"type\"] = %v; want %q", updatedPRD["type"], "string")
	}

	// Check second tool is submit_assessment.
	tool1 := tools[1]
	name1, ok := tool1["name"].(string)
	if !ok || name1 != "submit_assessment" {
		t.Errorf("tools[1][\"name\"] = %v; want %q", tool1["name"], "submit_assessment")
	}
}

// TestRefinementTools_ReturnsNewSlice verifies that each call to
// RefinementTools() returns a distinct slice value.
// Edge Case: 06-REQ-12.E1
func TestRefinementTools_ReturnsNewSlice(t *testing.T) {
	tools1 := RefinementTools()
	tools2 := RefinementTools()

	if len(tools1) == 0 || len(tools2) == 0 {
		t.Fatalf("RefinementTools() returned empty slice; want non-empty")
	}

	// Mutate tools1 and verify a new call is unaffected.
	tools1[0]["name"] = "mutated"

	tools3 := RefinementTools()
	if len(tools3) == 0 {
		t.Fatal("RefinementTools() returned empty slice after mutation")
	}

	name, ok := tools3[0]["name"].(string)
	if !ok || name != "submit_prd_update" {
		t.Errorf("after mutation, RefinementTools()[0][\"name\"] = %v; want %q", tools3[0]["name"], "submit_prd_update")
	}
}

// --- TS-NS-1: ArtifactTool schema computation caching ---

// TestArtifactTool_CachedResultConsistent verifies that repeated calls to
// ArtifactTool for the same artifact name return structurally identical
// results, confirming the cache is used correctly.
// Test Spec: TS-NS-1, Requirement: NS-REQ-1
func TestArtifactTool_CachedResultConsistent(t *testing.T) {
	const artifact = "requirements"

	first := ArtifactTool(artifact)
	if len(first) != 1 {
		t.Fatalf("first call returned %d entries; want 1", len(first))
	}

	for i := 0; i < 5; i++ {
		got := ArtifactTool(artifact)
		if len(got) != 1 {
			t.Fatalf("call %d returned %d entries; want 1", i+2, len(got))
		}
		gotName, ok := got[0]["name"].(string)
		if !ok || gotName != "submit_requirements" {
			t.Errorf("call %d: tool name = %v; want %q", i+2, got[0]["name"], "submit_requirements")
		}
		// Verify input_schema is present on every call.
		if _, hasSchema := got[0]["input_schema"]; !hasSchema {
			t.Errorf("call %d: missing input_schema", i+2)
		}
	}
}

// --- TS-NS-3: Mutation isolation ---

// TestArtifactTool_MutationIsolation verifies that mutating the slice
// returned by one ArtifactTool call does not affect results of subsequent
// calls (mutation-isolation guarantee preserved post-caching).
// Test Spec: TS-NS-3, Requirement: NS-REQ-3
func TestArtifactTool_MutationIsolation(t *testing.T) {
	tools1 := ArtifactTool("requirements")
	if len(tools1) != 1 {
		t.Fatalf("first call returned %d entries; want 1", len(tools1))
	}

	// Mutate the tool name in the first result.
	tools1[0]["name"] = "mutated_name"

	// A second call must still return the correct, unaffected tool name.
	tools2 := ArtifactTool("requirements")
	if len(tools2) != 1 {
		t.Fatalf("second call returned %d entries; want 1", len(tools2))
	}
	name, ok := tools2[0]["name"].(string)
	if !ok || name != "submit_requirements" {
		t.Errorf("after mutation, second call tool name = %v; want %q", tools2[0]["name"], "submit_requirements")
	}
}

// TestArtifactTool_InputSchemaMutationIsolation verifies that mutating
// the input_schema map returned by one call does not corrupt subsequent calls.
// Requirement: NS-REQ-3
func TestArtifactTool_InputSchemaMutationIsolation(t *testing.T) {
	tools1 := ArtifactTool("requirements")
	if len(tools1) != 1 {
		t.Fatalf("first call returned %d entries; want 1", len(tools1))
	}

	// Mutate the input_schema map from the first result.
	schema1, ok := tools1[0]["input_schema"].(map[string]any)
	if !ok {
		t.Fatal("input_schema is not map[string]any")
	}
	schema1["__injected__"] = "mutation"

	// A second call must return an input_schema without the injected key.
	tools2 := ArtifactTool("requirements")
	if len(tools2) != 1 {
		t.Fatalf("second call returned %d entries; want 1", len(tools2))
	}
	schema2, ok := tools2[0]["input_schema"].(map[string]any)
	if !ok {
		t.Fatal("second call: input_schema is not map[string]any")
	}
	if _, present := schema2["__injected__"]; present {
		t.Error("second call input_schema contains injected key; cache was mutated")
	}
}

// --- TS-NS-4: Concurrent access ---

// TestArtifactTool_ConcurrentAccess verifies that concurrent calls to
// ArtifactTool from multiple goroutines are data-race-free. Run with
// go test -race to exercise the race detector.
// Test Spec: TS-NS-4, Requirement: NS-REQ-4
func TestArtifactTool_ConcurrentAccess(t *testing.T) {
	artifacts := []string{"requirements", "test_spec", "tasks"}

	var wg sync.WaitGroup
	for _, name := range artifacts {
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(artifactName string) {
				defer wg.Done()
				tools := ArtifactTool(artifactName)
				if len(tools) != 1 {
					t.Errorf("concurrent ArtifactTool(%q) returned %d entries; want 1", artifactName, len(tools))
				}
			}(name)
		}
	}
	wg.Wait()
}
