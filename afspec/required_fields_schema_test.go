package afspec

import (
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// Regression tests for issue #23: missing required fields in JSON schemas
// ---------------------------------------------------------------------------

// requireSchemaError is a helper that validates a JSON document against the
// named schema and asserts that validation fails. It returns the validation
// errors for further inspection.
func requireSchemaError(t *testing.T, doc any, schemaName string) []ValidationEntry {
	t.Helper()
	errs := validateArtifactSchema(doc, schemaName, schemaName)
	if len(errs) == 0 {
		t.Errorf("expected schema validation error for %s but got none", schemaName)
	}
	return errs
}

// requireSchemaOK is a helper that validates a JSON document against the
// named schema and asserts that validation succeeds.
func requireSchemaOK(t *testing.T, doc any, schemaName string) {
	t.Helper()
	errs := validateArtifactSchema(doc, schemaName, schemaName)
	if len(errs) != 0 {
		t.Errorf("expected schema validation to pass for %s but got errors: %v", schemaName, errs)
	}
}

// parseJSON is a helper that unmarshals a JSON string into map[string]any.
func parseJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	return doc
}

// ---------------------------------------------------------------------------
// AC-1: prd.md frontmatter missing `supersedes` fails schema validation
// NS-REQ-1, TS-NS-1
// ---------------------------------------------------------------------------

// TestRequiredFields_PRD_Supersedes verifies that a prd-frontmatter.v1.json
// document missing `supersedes` fails schema validation, while a complete
// document including `supersedes` passes.
func TestRequiredFields_PRD_Supersedes(t *testing.T) {
	// Complete (valid) frontmatter with all required fields.
	validDoc := parseJSON(t, `{
		"spec_id": "01",
		"spec_name": "test_feature",
		"title": "Test Feature",
		"status": "draft",
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z",
		"schema_version": 1,
		"owner": "test-author",
		"source": "https://example.com",
		"supersedes": [],
		"intent_hash": null
	}`)
	requireSchemaOK(t, validDoc, "prd-frontmatter.v1.json")

	// Missing supersedes → must fail.
	missingSupersedes := parseJSON(t, `{
		"spec_id": "01",
		"spec_name": "test_feature",
		"title": "Test Feature",
		"status": "draft",
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z",
		"schema_version": 1,
		"owner": "test-author",
		"source": "https://example.com",
		"intent_hash": null
	}`)
	errs := requireSchemaError(t, missingSupersedes, "prd-frontmatter.v1.json")
	found := false
	for _, e := range errs {
		if containsSubstring(e.Message+e.Path+e.Keyword, "supersedes") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected schema error mentioning 'supersedes', got: %v", errs)
	}
}

// TestRequiredFields_PRD_IntentHash verifies that a prd-frontmatter.v1.json
// document missing `intent_hash` fails schema validation, and that a document
// with `intent_hash: null` passes.
func TestRequiredFields_PRD_IntentHash(t *testing.T) {
	// intent_hash: null → must pass (null is valid for ["string", "null"]).
	withNullIntentHash := parseJSON(t, `{
		"spec_id": "01",
		"spec_name": "test_feature",
		"title": "Test Feature",
		"status": "draft",
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z",
		"schema_version": 1,
		"owner": "test-author",
		"source": "https://example.com",
		"supersedes": [],
		"intent_hash": null
	}`)
	requireSchemaOK(t, withNullIntentHash, "prd-frontmatter.v1.json")

	// Missing intent_hash → must fail.
	missingIntentHash := parseJSON(t, `{
		"spec_id": "01",
		"spec_name": "test_feature",
		"title": "Test Feature",
		"status": "draft",
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z",
		"schema_version": 1,
		"owner": "test-author",
		"source": "https://example.com",
		"supersedes": []
	}`)
	errs := requireSchemaError(t, missingIntentHash, "prd-frontmatter.v1.json")
	found := false
	for _, e := range errs {
		if containsSubstring(e.Message+e.Path+e.Keyword, "intent_hash") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected schema error mentioning 'intent_hash', got: %v", errs)
	}
}

// ---------------------------------------------------------------------------
// AC-2: task_dependency missing `sentinel` fails schema validation
// NS-REQ-2, TS-NS-2
// ---------------------------------------------------------------------------

// TestRequiredFields_TaskDependency_Sentinel verifies that a tasks.v1.json
// document with a dependency entry missing `sentinel` fails schema validation,
// while a dependency with `sentinel: false` passes.
func TestRequiredFields_TaskDependency_Sentinel(t *testing.T) {
	baseTasksJSON := `{
		"$schema": "https://agent-fox.dev/schemas/tasks.v1.json",
		"spec_id": "02",
		"spec_name": "test_dep",
		"schema_version": 1,
		"test_commands": {"spec_tests": "go test", "all_tests": "go test", "linter": "go vet"},
		"dependencies": [],
		"task_groups": [],
		"traceability": []
	}`

	// With sentinel: false → must pass (task_groups requires minItems:1).
	withSentinel := parseJSON(t, `{
		"$schema": "https://agent-fox.dev/schemas/tasks.v1.json",
		"spec_id": "02",
		"spec_name": "test_dep",
		"schema_version": 1,
		"test_commands": {"spec_tests": "go test", "all_tests": "go test", "linter": "go vet"},
		"dependencies": [
			{"depends_on_spec": "01", "from_group": 1, "to_group": 1, "relationship": "blocks", "sentinel": false}
		],
		"task_groups": [{"id":1,"kind":"tests","title":"Tests","subtasks":[],"verification":{"id":"1.V","checks":["ok"]}}],
		"traceability": []
	}`)
	requireSchemaOK(t, withSentinel, "tasks.v1.json")

	// Without sentinel → must fail.
	withoutSentinel := parseJSON(t, `{
		"$schema": "https://agent-fox.dev/schemas/tasks.v1.json",
		"spec_id": "02",
		"spec_name": "test_dep",
		"schema_version": 1,
		"test_commands": {"spec_tests": "go test", "all_tests": "go test", "linter": "go vet"},
		"dependencies": [
			{"depends_on_spec": "01", "from_group": 1, "to_group": 1, "relationship": "blocks"}
		],
		"task_groups": [],
		"traceability": []
	}`)
	errs := requireSchemaError(t, withoutSentinel, "tasks.v1.json")
	found := false
	for _, e := range errs {
		if containsSubstring(e.Message+e.Path+e.Keyword, "sentinel") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected schema error mentioning 'sentinel', got: %v", errs)
	}

	_ = baseTasksJSON // used for reference
}

// ---------------------------------------------------------------------------
// AC-3: traceability_entry missing `test_path` fails schema validation
// NS-REQ-3, TS-NS-3
// ---------------------------------------------------------------------------

// TestRequiredFields_TraceabilityEntry_TestPath verifies that a tasks.v1.json
// traceability entry missing `test_path` fails schema validation, while an
// entry with `test_path: null` passes.
func TestRequiredFields_TraceabilityEntry_TestPath(t *testing.T) {
	// With test_path: null → must pass (task_groups requires minItems:1).
	withNullTestPath := parseJSON(t, `{
		"$schema": "https://agent-fox.dev/schemas/tasks.v1.json",
		"spec_id": "02",
		"spec_name": "test_trace",
		"schema_version": 1,
		"test_commands": {"spec_tests": "go test", "all_tests": "go test", "linter": "go vet"},
		"dependencies": [],
		"task_groups": [{"id":1,"kind":"tests","title":"Tests","subtasks":[],"verification":{"id":"1.V","checks":["ok"]}}],
		"traceability": [
			{"requirement_id": "02-REQ-1.1", "test_spec_id": "TS-02-1", "task_id": "1.1", "test_path": null}
		]
	}`)
	requireSchemaOK(t, withNullTestPath, "tasks.v1.json")

	// Without test_path → must fail.
	withoutTestPath := parseJSON(t, `{
		"$schema": "https://agent-fox.dev/schemas/tasks.v1.json",
		"spec_id": "02",
		"spec_name": "test_trace",
		"schema_version": 1,
		"test_commands": {"spec_tests": "go test", "all_tests": "go test", "linter": "go vet"},
		"dependencies": [],
		"task_groups": [],
		"traceability": [
			{"requirement_id": "02-REQ-1.1", "test_spec_id": "TS-02-1", "task_id": "1.1"}
		]
	}`)
	errs := requireSchemaError(t, withoutTestPath, "tasks.v1.json")
	found := false
	for _, e := range errs {
		if containsSubstring(e.Message+e.Path+e.Keyword, "test_path") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected schema error mentioning 'test_path', got: %v", errs)
	}
}

// ---------------------------------------------------------------------------
// AC-4: JSON artifact missing `$schema` fails schema validation
// NS-REQ-4, TS-NS-4
// ---------------------------------------------------------------------------

// TestRequiredFields_RequirementsSchema verifies that a requirements.v1.json
// document missing `$schema` fails schema validation.
func TestRequiredFields_RequirementsSchema(t *testing.T) {
	// Complete requirements → must pass.
	withSchema := parseJSON(t, `{
		"$schema": "https://agent-fox.dev/schemas/requirements.v1.json",
		"spec_id": "01",
		"spec_name": "test_feature",
		"schema_version": 1,
		"introduction": "Test.",
		"glossary": {},
		"requirements": [],
		"correctness_properties": [],
		"execution_paths": [],
		"error_handling": []
	}`)
	requireSchemaOK(t, withSchema, "requirements.v1.json")

	// Missing $schema → must fail.
	withoutSchema := parseJSON(t, `{
		"spec_id": "01",
		"spec_name": "test_feature",
		"schema_version": 1,
		"introduction": "Test.",
		"glossary": {},
		"requirements": [],
		"correctness_properties": [],
		"execution_paths": [],
		"error_handling": []
	}`)
	errs := requireSchemaError(t, withoutSchema, "requirements.v1.json")
	found := false
	for _, e := range errs {
		if containsSubstring(e.Message+e.Path+e.Keyword, "$schema") || containsSubstring(e.Message, "schema") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected schema error mentioning '$schema', got: %v", errs)
	}
}

// TestRequiredFields_TestSpecSchema verifies that a test_spec.v1.json
// document missing `$schema` fails schema validation.
func TestRequiredFields_TestSpecSchema(t *testing.T) {
	// Complete test_spec → must pass.
	withSchema := parseJSON(t, `{
		"$schema": "https://agent-fox.dev/schemas/test_spec.v1.json",
		"spec_id": "01",
		"spec_name": "test_feature",
		"schema_version": 1,
		"test_cases": [],
		"property_tests": [],
		"edge_case_tests": [],
		"smoke_tests": [],
		"coverage": {}
	}`)
	requireSchemaOK(t, withSchema, "test_spec.v1.json")

	// Missing $schema → must fail.
	withoutSchema := parseJSON(t, `{
		"spec_id": "01",
		"spec_name": "test_feature",
		"schema_version": 1,
		"test_cases": [],
		"property_tests": [],
		"edge_case_tests": [],
		"smoke_tests": [],
		"coverage": {}
	}`)
	errs := requireSchemaError(t, withoutSchema, "test_spec.v1.json")
	found := false
	for _, e := range errs {
		if containsSubstring(e.Message+e.Path+e.Keyword, "$schema") || containsSubstring(e.Message, "schema") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected schema error mentioning '$schema', got: %v", errs)
	}
}

// TestRequiredFields_TasksSchema verifies that a tasks.v1.json document
// missing `$schema` fails schema validation.
func TestRequiredFields_TasksSchema(t *testing.T) {
	// Complete tasks → must pass (task_groups requires minItems:1).
	withSchema := parseJSON(t, `{
		"$schema": "https://agent-fox.dev/schemas/tasks.v1.json",
		"spec_id": "01",
		"spec_name": "test_feature",
		"schema_version": 1,
		"test_commands": {"spec_tests": "go test", "all_tests": "go test", "linter": "go vet"},
		"dependencies": [],
		"task_groups": [{"id":1,"kind":"tests","title":"Tests","subtasks":[],"verification":{"id":"1.V","checks":["ok"]}}],
		"traceability": []
	}`)
	requireSchemaOK(t, withSchema, "tasks.v1.json")

	// Missing $schema → must fail.
	withoutSchema := parseJSON(t, `{
		"spec_id": "01",
		"spec_name": "test_feature",
		"schema_version": 1,
		"test_commands": {"spec_tests": "go test", "all_tests": "go test", "linter": "go vet"},
		"dependencies": [],
		"task_groups": [],
		"traceability": []
	}`)
	errs := requireSchemaError(t, withoutSchema, "tasks.v1.json")
	found := false
	for _, e := range errs {
		if containsSubstring(e.Message+e.Path+e.Keyword, "$schema") || containsSubstring(e.Message, "schema") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected schema error mentioning '$schema', got: %v", errs)
	}
}

// ---------------------------------------------------------------------------
// AC-4 via ValidateSchema: programmatic specs with $schema pass
// NS-REQ-5, TS-NS-5
// ---------------------------------------------------------------------------

// TestRequiredFields_ValidateSchemaPasses verifies that a Spec built
// programmatically with all newly-required fields passes ValidateSchema.
func TestRequiredFields_ValidateSchemaPasses(t *testing.T) {
	spec := &Spec{
		SpecID:        "01",
		SpecName:      "test_feature",
		Title:         "Test Feature",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test-author",
		Source:        "https://example.com",
		Supersedes:    []string{},
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			Schema:                "https://agent-fox.dev/schemas/requirements.v1.json",
			SpecId:                "01",
			SpecName:              "test_feature",
			SchemaVersion:         1,
			Introduction:          "Test.",
			Glossary:              RequirementsV1JsonGlossary{},
			Requirements:          []Requirement{},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			Schema:        "https://agent-fox.dev/schemas/test_spec.v1.json",
			SpecId:        "01",
			SpecName:      "test_feature",
			SchemaVersion: 1,
			TestCases:     []TestCase{},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			Schema:        "https://agent-fox.dev/schemas/tasks.v1.json",
			SpecId:        "01",
			SpecName:      "test_feature",
			SchemaVersion: 1,
			TestCommands: TestCommands{
				SpecTests: "go test ./...",
				AllTests:  "go test ./...",
				Linter:    "go vet ./...",
			},
			Dependencies: []TaskDependency{},
			// task_groups requires minItems:1 per Section 8.3.
			TaskGroups: []TaskGroup{
				{
					Id:       1,
					Kind:     TaskGroupKindTests,
					Title:    "Write Tests",
					Subtasks: []Subtask{},
					Verification: VerificationSubtask{
						Id:     "1.V",
						Checks: []string{"all tests pass"},
					},
				},
			},
			Traceability: []TraceabilityEntry{},
		},
	}

	result := spec.ValidateSchema()
	if !result.Valid {
		t.Errorf("ValidateSchema() returned errors for a complete spec: %v", result.Errors)
	}
}

// TestRequiredFields_ValidateSchemaFailsMissingSupersedes verifies that a
// Spec with Supersedes=nil fails schema validation because nil becomes null
// (not an array) in the prd-frontmatter JSON.
func TestRequiredFields_ValidateSchemaFailsMissingSupersedes(t *testing.T) {
	spec := &Spec{
		SpecID:        "01",
		SpecName:      "test_feature",
		Title:         "Test Feature",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test-author",
		Source:        "https://example.com",
		Supersedes:    nil, // nil → null in JSON → fails schema (must be array)
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			Schema:                "https://agent-fox.dev/schemas/requirements.v1.json",
			SpecId:                "01",
			SpecName:              "test_feature",
			SchemaVersion:         1,
			Introduction:          "Test.",
			Glossary:              RequirementsV1JsonGlossary{},
			Requirements:          []Requirement{},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			Schema:        "https://agent-fox.dev/schemas/test_spec.v1.json",
			SpecId:        "01",
			SpecName:      "test_feature",
			SchemaVersion: 1,
			TestCases:     []TestCase{},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			Schema:        "https://agent-fox.dev/schemas/tasks.v1.json",
			SpecId:        "01",
			SpecName:      "test_feature",
			SchemaVersion: 1,
			TestCommands: TestCommands{
				SpecTests: "go test ./...",
				AllTests:  "go test ./...",
				Linter:    "go vet ./...",
			},
			Dependencies: []TaskDependency{},
			TaskGroups:   []TaskGroup{},
			Traceability: []TraceabilityEntry{},
		},
	}

	result := spec.ValidateSchema()
	if result.Valid {
		t.Error("ValidateSchema() returned Valid=true, want false when Supersedes is nil (no supersedes field)")
	}
	found := false
	for _, e := range result.Errors {
		if containsSubstring(e.Message+e.Path, "supersedes") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a schema error mentioning 'supersedes', got: %v", result.Errors)
	}
}

// ---------------------------------------------------------------------------
// UnmarshalJSON enforcement: Go-level required checks
// ---------------------------------------------------------------------------

// TestRequiredFields_UnmarshalJSON_TaskDependency_Sentinel verifies that
// unmarshaling a task dependency JSON without `sentinel` returns an error.
func TestRequiredFields_UnmarshalJSON_TaskDependency_Sentinel(t *testing.T) {
	// With sentinel → must succeed.
	withSentinel := `{"depends_on_spec":"01","from_group":1,"to_group":1,"relationship":"blocks","sentinel":false}`
	var dep1 TaskDependency
	if err := json.Unmarshal([]byte(withSentinel), &dep1); err != nil {
		t.Errorf("Unmarshal with sentinel failed: %v", err)
	}

	// Without sentinel → must fail with "field sentinel".
	withoutSentinel := `{"depends_on_spec":"01","from_group":1,"to_group":1,"relationship":"blocks"}`
	var dep2 TaskDependency
	if err := json.Unmarshal([]byte(withoutSentinel), &dep2); err == nil {
		t.Error("Unmarshal without sentinel should fail but succeeded")
	} else if !containsSubstring(err.Error(), "sentinel") {
		t.Errorf("expected error mentioning 'sentinel', got: %v", err)
	}
}

// TestRequiredFields_UnmarshalJSON_TraceabilityEntry_TestPath verifies that
// unmarshaling a traceability entry without `test_path` returns an error,
// while one with `test_path: null` succeeds.
func TestRequiredFields_UnmarshalJSON_TraceabilityEntry_TestPath(t *testing.T) {
	// With test_path: null → must succeed.
	withNullTestPath := `{"requirement_id":"01-REQ-1.1","test_spec_id":"TS-01-1","task_id":"1.1","test_path":null}`
	var te1 TraceabilityEntry
	if err := json.Unmarshal([]byte(withNullTestPath), &te1); err != nil {
		t.Errorf("Unmarshal with test_path:null failed: %v", err)
	}

	// Without test_path → must fail with "field test_path".
	withoutTestPath := `{"requirement_id":"01-REQ-1.1","test_spec_id":"TS-01-1","task_id":"1.1"}`
	var te2 TraceabilityEntry
	if err := json.Unmarshal([]byte(withoutTestPath), &te2); err == nil {
		t.Error("Unmarshal without test_path should fail but succeeded")
	} else if !containsSubstring(err.Error(), "test_path") {
		t.Errorf("expected error mentioning 'test_path', got: %v", err)
	}
}

// TestRequiredFields_UnmarshalJSON_RequirementsV1Json_Schema verifies that
// unmarshaling a requirements JSON without `$schema` returns an error.
func TestRequiredFields_UnmarshalJSON_RequirementsV1Json_Schema(t *testing.T) {
	// With $schema → must succeed.
	withSchema := `{"$schema":"https://agent-fox.dev/schemas/requirements.v1.json","spec_id":"01","spec_name":"test","schema_version":1,"introduction":"Test","glossary":{},"requirements":[],"correctness_properties":[],"execution_paths":[],"error_handling":[]}`
	var req1 RequirementsV1Json
	if err := json.Unmarshal([]byte(withSchema), &req1); err != nil {
		t.Errorf("Unmarshal with $schema failed: %v", err)
	}

	// Without $schema → must fail.
	withoutSchema := `{"spec_id":"01","spec_name":"test","schema_version":1,"introduction":"Test","glossary":{},"requirements":[],"correctness_properties":[],"execution_paths":[],"error_handling":[]}`
	var req2 RequirementsV1Json
	if err := json.Unmarshal([]byte(withoutSchema), &req2); err == nil {
		t.Error("Unmarshal without $schema should fail but succeeded")
	} else if !containsSubstring(err.Error(), "$schema") {
		t.Errorf("expected error mentioning '$schema', got: %v", err)
	}
}

// TestRequiredFields_UnmarshalJSON_TestSpecV1Json_Schema verifies that
// unmarshaling a test_spec JSON without `$schema` returns an error.
func TestRequiredFields_UnmarshalJSON_TestSpecV1Json_Schema(t *testing.T) {
	// With $schema → must succeed.
	withSchema := `{"$schema":"https://agent-fox.dev/schemas/test_spec.v1.json","spec_id":"01","spec_name":"test","schema_version":1,"test_cases":[],"property_tests":[],"edge_case_tests":[],"smoke_tests":[],"coverage":{}}`
	var ts1 TestSpecV1Json
	if err := json.Unmarshal([]byte(withSchema), &ts1); err != nil {
		t.Errorf("Unmarshal with $schema failed: %v", err)
	}

	// Without $schema → must fail.
	withoutSchema := `{"spec_id":"01","spec_name":"test","schema_version":1,"test_cases":[],"property_tests":[],"edge_case_tests":[],"smoke_tests":[],"coverage":{}}`
	var ts2 TestSpecV1Json
	if err := json.Unmarshal([]byte(withoutSchema), &ts2); err == nil {
		t.Error("Unmarshal without $schema should fail but succeeded")
	} else if !containsSubstring(err.Error(), "$schema") {
		t.Errorf("expected error mentioning '$schema', got: %v", err)
	}
}

// TestRequiredFields_UnmarshalJSON_TasksV1Json_Schema verifies that
// unmarshaling a tasks JSON without `$schema` returns an error.
func TestRequiredFields_UnmarshalJSON_TasksV1Json_Schema(t *testing.T) {
	// With $schema → must succeed (task_groups must have ≥1 entry per minItems:1).
	withSchema := `{"$schema":"https://agent-fox.dev/schemas/tasks.v1.json","spec_id":"01","spec_name":"test","schema_version":1,"test_commands":{"spec_tests":"go test","all_tests":"go test","linter":"go vet"},"dependencies":[],"task_groups":[{"id":1,"kind":"tests","title":"Tests","subtasks":[],"verification":{"id":"1.V","checks":["ok"]}}],"traceability":[]}`
	var t1 TasksV1Json
	if err := json.Unmarshal([]byte(withSchema), &t1); err != nil {
		t.Errorf("Unmarshal with $schema failed: %v", err)
	}

	// Without $schema → must fail.
	withoutSchema := `{"spec_id":"01","spec_name":"test","schema_version":1,"test_commands":{"spec_tests":"go test","all_tests":"go test","linter":"go vet"},"dependencies":[],"task_groups":[],"traceability":[]}`
	var t2 TasksV1Json
	if err := json.Unmarshal([]byte(withoutSchema), &t2); err == nil {
		t.Error("Unmarshal without $schema should fail but succeeded")
	} else if !containsSubstring(err.Error(), "$schema") {
		t.Errorf("expected error mentioning '$schema', got: %v", err)
	}
}

// TestRequiredFields_UnmarshalJSON_PRD_Supersedes verifies that unmarshaling
// a prd-frontmatter JSON without `supersedes` returns an error.
func TestRequiredFields_UnmarshalJSON_PRD_Supersedes(t *testing.T) {
	// With supersedes → must succeed.
	withSupersedes := `{"spec_id":"01","spec_name":"test","title":"Test","status":"draft","created_at":"2026-01-01","updated_at":"2026-01-01","schema_version":1,"owner":"test","source":"https://example.com","supersedes":[],"intent_hash":null}`
	var prd1 PrdFrontmatterV1Json
	if err := json.Unmarshal([]byte(withSupersedes), &prd1); err != nil {
		t.Errorf("Unmarshal with supersedes failed: %v", err)
	}

	// Without supersedes → must fail.
	withoutSupersedes := `{"spec_id":"01","spec_name":"test","title":"Test","status":"draft","created_at":"2026-01-01","updated_at":"2026-01-01","schema_version":1,"owner":"test","source":"https://example.com","intent_hash":null}`
	var prd2 PrdFrontmatterV1Json
	if err := json.Unmarshal([]byte(withoutSupersedes), &prd2); err == nil {
		t.Error("Unmarshal without supersedes should fail but succeeded")
	} else if !containsSubstring(err.Error(), "supersedes") {
		t.Errorf("expected error mentioning 'supersedes', got: %v", err)
	}
}

// TestRequiredFields_UnmarshalJSON_PRD_IntentHash verifies that unmarshaling
// a prd-frontmatter JSON without `intent_hash` returns an error, while one
// with `intent_hash: null` succeeds.
func TestRequiredFields_UnmarshalJSON_PRD_IntentHash(t *testing.T) {
	// With intent_hash: null → must succeed.
	withNullIntentHash := `{"spec_id":"01","spec_name":"test","title":"Test","status":"draft","created_at":"2026-01-01","updated_at":"2026-01-01","schema_version":1,"owner":"test","source":"https://example.com","supersedes":[],"intent_hash":null}`
	var prd1 PrdFrontmatterV1Json
	if err := json.Unmarshal([]byte(withNullIntentHash), &prd1); err != nil {
		t.Errorf("Unmarshal with intent_hash:null failed: %v", err)
	}

	// Without intent_hash → must fail.
	withoutIntentHash := `{"spec_id":"01","spec_name":"test","title":"Test","status":"draft","created_at":"2026-01-01","updated_at":"2026-01-01","schema_version":1,"owner":"test","source":"https://example.com","supersedes":[]}`
	var prd2 PrdFrontmatterV1Json
	if err := json.Unmarshal([]byte(withoutIntentHash), &prd2); err == nil {
		t.Error("Unmarshal without intent_hash should fail but succeeded")
	} else if !containsSubstring(err.Error(), "intent_hash") {
		t.Errorf("expected error mentioning 'intent_hash', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

// containsSubstring returns true if s contains substr (case-sensitive).
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
