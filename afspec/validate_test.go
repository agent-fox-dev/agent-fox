package afspec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Subtask 2.1: Spec.Validate, ValidateSchema, ValidateCrossFile
// ---------------------------------------------------------------------------

// TestValidate_FullyValidSpec verifies that Spec.Validate on a fully valid
// spec returns a ValidationResult with Valid true and empty Errors slice.
// Test Spec: TS-01-8, Requirement: 01-REQ-4.1
func TestValidate_FullyValidSpec(t *testing.T) {
	defer requireImplemented(t)

	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec returned unexpected error: %v", err)
	}

	result := spec.Validate()
	if !result.Valid {
		t.Errorf("Validate().Valid = false, want true; errors = %v", result.Errors)
	}
	if len(result.Errors) != 0 {
		t.Errorf("Validate().Errors has %d entries, want 0: %v", len(result.Errors), result.Errors)
	}
}

// TestValidate_SpecWithErrors verifies that Spec.Validate on a spec with
// one or more errors returns a ValidationResult with Valid false and a
// non-empty Errors slice.
// Test Spec: TS-01-9, Requirement: 01-REQ-4.2
func TestValidate_SpecWithErrors(t *testing.T) {
	defer requireImplemented(t)

	// Build a spec with a schema violation: a requirement missing required fields
	spec := buildInvalidSpec()

	result := spec.Validate()
	if result.Valid {
		t.Error("Validate().Valid = true, want false for invalid spec")
	}
	if len(result.Errors) == 0 {
		t.Error("Validate().Errors is empty, want at least one error for invalid spec")
	}
}

// TestValidate_WarningsButNoErrors verifies that Validate on a spec with
// warnings but no errors returns Valid true with non-empty Warnings.
// Requirement: 01-REQ-4.E1
func TestValidate_WarningsButNoErrors(t *testing.T) {
	defer requireImplemented(t)

	spec := buildSpecWithWarnings()

	result := spec.Validate()
	if !result.Valid {
		t.Errorf("Validate().Valid = false, want true (warnings only); errors = %v", result.Errors)
	}
	if len(result.Warnings) == 0 {
		t.Error("Validate().Warnings is empty, want at least one warning")
	}
}

// TestValidate_MinimalSpec verifies that Validate on a minimally populated
// spec does not panic.
// Requirement: 01-REQ-4.E2
func TestValidate_MinimalSpec(t *testing.T) {
	defer requireImplemented(t)

	spec := &Spec{
		SpecID:        "01",
		SpecName:      "minimal",
		Title:         "Minimal Spec",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test-author",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Minimal\n",
		Requirements: &RequirementsV1Json{
			SpecId:                "01",
			SpecName:              "minimal",
			SchemaVersion:         1,
			Introduction:          "Minimal spec.",
			Glossary:              RequirementsV1JsonGlossary{},
			Requirements:          []Requirement{},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "01",
			SpecName:      "minimal",
			SchemaVersion: 1,
			TestCases:     []TestCase{},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SpecId:        "01",
			SpecName:      "minimal",
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

	// Should not panic — just produce a result
	result := spec.Validate()
	_ = result // Don't assert valid/invalid — just ensure no panic
}

// TestValidate_ValidFieldConsistentWithErrors verifies the correctness
// property: Valid is true iff Errors is empty.
// Property: 01-PROP-4
func TestValidate_ValidFieldConsistentWithErrors(t *testing.T) {
	defer requireImplemented(t)

	// Valid spec: Valid should be true, Errors empty
	validSpec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec returned unexpected error: %v", err)
	}
	result := validSpec.Validate()
	if result.Valid && len(result.Errors) != 0 {
		t.Error("Valid is true but Errors is non-empty — violates PROP-4")
	}
	if !result.Valid && len(result.Errors) == 0 {
		t.Error("Valid is false but Errors is empty — violates PROP-4")
	}

	// Invalid spec: Valid should be false, Errors non-empty
	invalidSpec := buildInvalidSpec()
	result2 := invalidSpec.Validate()
	if result2.Valid && len(result2.Errors) != 0 {
		t.Error("Valid is true but Errors is non-empty — violates PROP-4")
	}
	if !result2.Valid && len(result2.Errors) == 0 {
		t.Error("Valid is false but Errors is empty — violates PROP-4")
	}
}

// TestValidateSchema_ValidSpec verifies that ValidateSchema on a structurally
// valid spec returns ValidationResult with Valid true and no errors.
// Test Spec: TS-01-10, Requirement: 01-REQ-5.1
func TestValidateSchema_ValidSpec(t *testing.T) {
	defer requireImplemented(t)

	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec returned unexpected error: %v", err)
	}

	result := spec.ValidateSchema()
	if !result.Valid {
		t.Errorf("ValidateSchema().Valid = false, want true; errors = %v", result.Errors)
	}
	if len(result.Errors) != 0 {
		t.Errorf("ValidateSchema().Errors has %d entries, want 0", len(result.Errors))
	}
}

// TestValidateSchema_SchemaViolation verifies that ValidateSchema maps
// each *jsonschema.ValidationError to a ValidationResult error entry
// with path, keyword/message fields populated.
// Test Spec: TS-01-11, Requirement: 01-REQ-5.2
func TestValidateSchema_SchemaViolation(t *testing.T) {
	defer requireImplemented(t)

	spec := buildSchemaViolatingSpec()

	result := spec.ValidateSchema()
	if result.Valid {
		t.Error("ValidateSchema().Valid = true, want false for schema-violating spec")
	}
	if len(result.Errors) == 0 {
		t.Fatal("ValidateSchema().Errors is empty, want at least one error")
	}

	// First error should have path and message populated
	e := result.Errors[0]
	if e.Path == "" && e.Message == "" {
		t.Error("expected error entry to have non-empty Path or Message")
	}
	if e.Message == "" {
		t.Error("expected error entry to have non-empty Message")
	}
}

// TestValidateSchema_MultipleArtifactErrors verifies that ValidateSchema
// collects errors from all artifacts rather than stopping at the first failure.
// Requirement: 01-REQ-5.E2
func TestValidateSchema_MultipleArtifactErrors(t *testing.T) {
	defer requireImplemented(t)

	// Build a spec with schema violations in multiple artifacts
	spec := buildMultiArtifactSchemaViolatingSpec()

	result := spec.ValidateSchema()
	if result.Valid {
		t.Error("ValidateSchema().Valid = true, want false")
	}
	// Should have errors from more than one artifact
	artifacts := map[string]bool{}
	for _, e := range result.Errors {
		if e.Artifact != "" {
			artifacts[e.Artifact] = true
		}
	}
	if len(artifacts) < 2 {
		t.Errorf("expected errors from at least 2 artifacts, got errors from %d: %v",
			len(artifacts), artifacts)
	}
}

// ---------------------------------------------------------------------------
// PRD frontmatter schema validation tests (Issue #6)
// ---------------------------------------------------------------------------

// TestValidateSchema_InvalidPRDStatus verifies that ValidateSchema returns
// at least one schema-category error with Artifact == "prd.md" when the
// Spec carries an invalid Status value not in the enum.
// Test Spec: TS-NS-2, Requirement: NS-REQ-2
func TestValidateSchema_InvalidPRDStatus(t *testing.T) {
	spec := buildSpecWithDanglingRef("01-REQ-1") // valid base spec
	spec.Status = "unknown"                      // invalid status

	result := spec.ValidateSchema()
	if result.Valid {
		t.Error("ValidateSchema().Valid = true, want false for invalid PRD status")
	}

	found := false
	for _, e := range result.Errors {
		if e.Category == "schema" && e.Check == "json_schema" && e.Artifact == "prd.md" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected at least one error with Category=schema, Check=json_schema, Artifact=prd.md; got errors: %v", result.Errors)
	}
}

// TestValidateSchema_InvalidPRDSchemaVersion verifies that ValidateSchema
// returns a schema error with Artifact == "prd.md" when SchemaVersion != 1.
// Test Spec: TS-NS-3, Requirement: NS-REQ-3
func TestValidateSchema_InvalidPRDSchemaVersion(t *testing.T) {
	spec := buildSpecWithDanglingRef("01-REQ-1") // valid base spec
	spec.SchemaVersion = 0                       // violates const: 1

	result := spec.ValidateSchema()
	if result.Valid {
		t.Error("ValidateSchema().Valid = true, want false for invalid schema_version")
	}

	found := false
	for _, e := range result.Errors {
		if e.Artifact == "prd.md" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected at least one error with Artifact=prd.md; got errors: %v", result.Errors)
	}
}

// TestValidateSchema_MissingPRDSpecID verifies that ValidateSchema returns
// a schema error with Artifact == "prd.md" when a required frontmatter
// field (spec_id) is empty.
// Test Spec: TS-NS-4, Requirement: NS-REQ-4
func TestValidateSchema_MissingPRDSpecID(t *testing.T) {
	spec := buildSpecWithDanglingRef("01-REQ-1") // valid base spec
	spec.SpecID = ""                             // violates minLength: 1

	result := spec.ValidateSchema()
	if result.Valid {
		t.Error("ValidateSchema().Valid = true, want false for empty spec_id")
	}

	found := false
	for _, e := range result.Errors {
		if e.Artifact == "prd.md" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected at least one error with Artifact=prd.md; got errors: %v", result.Errors)
	}
}

// ---------------------------------------------------------------------------
// Null $schema rejection tests (Issue #40)
// ---------------------------------------------------------------------------

// buildNullSchemaArtifacts builds raw map instances with "$schema": null for
// direct schema validation, bypassing Go marshaling omitempty behaviour.
// They are passed to validateArtifactSchema as map[string]any.
func nullSchemaRequirements() map[string]any {
	return map[string]any{
		"$schema":                nil,
		"spec_id":                "01",
		"spec_name":              "test",
		"schema_version":         1,
		"introduction":           "Test.",
		"glossary":               map[string]any{},
		"requirements":           []any{},
		"correctness_properties": []any{},
		"execution_paths":        []any{},
		"error_handling":         []any{},
	}
}

func nullSchemaTestSpec() map[string]any {
	return map[string]any{
		"$schema":         nil,
		"spec_id":         "01",
		"spec_name":       "test",
		"schema_version":  1,
		"test_cases":      []any{},
		"property_tests":  []any{},
		"edge_case_tests": []any{},
		"smoke_tests":     []any{},
		"coverage":        map[string]any{"requirements_covered": []any{}, "properties_covered": []any{}, "paths_covered": []any{}},
	}
}

func nullSchemaTasks() map[string]any {
	return map[string]any{
		"$schema":        nil,
		"spec_id":        "01",
		"spec_name":      "test",
		"schema_version": 1,
		"test_commands":  map[string]any{"spec_tests": "go test ./...", "all_tests": "go test ./...", "linter": "go vet ./..."},
		"dependencies":   []any{},
		"task_groups":    []any{},
		"traceability":   []any{},
	}
}

// TestValidateSchema_NullSchemaRequirements verifies AC-1/NS-REQ-1: a
// requirements artifact with "$schema": null fails JSON schema validation
// with at least one error referencing /$schema.
// Test Spec: TS-NS-1, Requirement: NS-REQ-1
func TestValidateSchema_NullSchemaRequirements(t *testing.T) {
	artifact := nullSchemaRequirements()
	errs := validateArtifactSchema(artifact, "requirements.v1.json", "requirements.json")
	if len(errs) == 0 {
		t.Fatal("expected validation errors for null $schema in requirements, got none")
	}
	found := false
	for _, e := range errs {
		if e.Category == "schema" && strings.Contains(e.Path, "$schema") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected at least one error with Category=schema and Path containing /$schema; got: %v", errs)
	}
}

// TestValidateSchema_NullSchemaTestSpec verifies AC-1/NS-REQ-2: a test_spec
// artifact with "$schema": null fails JSON schema validation.
// Test Spec: TS-NS-2, Requirement: NS-REQ-2
func TestValidateSchema_NullSchemaTestSpec(t *testing.T) {
	artifact := nullSchemaTestSpec()
	errs := validateArtifactSchema(artifact, "test_spec.v1.json", "test_spec.json")
	if len(errs) == 0 {
		t.Fatal("expected validation errors for null $schema in test_spec, got none")
	}
	found := false
	for _, e := range errs {
		if e.Category == "schema" && strings.Contains(e.Path, "$schema") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected at least one error with Category=schema and Path containing /$schema; got: %v", errs)
	}
}

// TestValidateSchema_NullSchemaTasks verifies AC-1/NS-REQ-2: a tasks artifact
// with "$schema": null fails JSON schema validation.
// Test Spec: TS-NS-2, Requirement: NS-REQ-2
func TestValidateSchema_NullSchemaTasks(t *testing.T) {
	artifact := nullSchemaTasks()
	errs := validateArtifactSchema(artifact, "tasks.v1.json", "tasks.json")
	if len(errs) == 0 {
		t.Fatal("expected validation errors for null $schema in tasks, got none")
	}
	found := false
	for _, e := range errs {
		if e.Category == "schema" && strings.Contains(e.Path, "$schema") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected at least one error with Category=schema and Path containing /$schema; got: %v", errs)
	}
}

// TestValidateSchema_ValidStringSchemaRequirements verifies AC-2/NS-REQ-3:
// a requirements artifact with a valid string URI for $schema passes validation.
// Test Spec: TS-NS-3, Requirement: NS-REQ-3
func TestValidateSchema_ValidStringSchemaRequirements(t *testing.T) {
	artifact := nullSchemaRequirements()
	artifact["$schema"] = "https://agent-fox.dev/schemas/requirements.v1.json"
	errs := validateArtifactSchema(artifact, "requirements.v1.json", "requirements.json")
	schemaErrs := []ValidationEntry{}
	for _, e := range errs {
		if e.Category == "schema" && strings.Contains(e.Path, "$schema") {
			schemaErrs = append(schemaErrs, e)
		}
	}
	if len(schemaErrs) > 0 {
		t.Errorf("expected no $schema errors for valid string URI; got: %v", schemaErrs)
	}
}

// ---------------------------------------------------------------------------
// Completeness guard tests (Issue #8)
// ---------------------------------------------------------------------------

// TestValidateCrossFile_CompletenessGuard_AllEmpty verifies that
// ValidateCrossFile returns a single 'completeness' error when all three
// artifacts have empty SpecId, and no other cross-file errors.
// Test Spec: TS-NS-1, TS-NS-5. Requirements: NS-REQ-1, NS-REQ-5.
func TestValidateCrossFile_CompletenessGuard_AllEmpty(t *testing.T) {
	spec := &Spec{
		SpecID:        "01",
		SpecName:      "test",
		Title:         "Test",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			SpecId:                "", // empty
			SpecName:              "test",
			SchemaVersion:         1,
			Introduction:          "Test.",
			Glossary:              RequirementsV1JsonGlossary{},
			Requirements:          []Requirement{},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "", // empty
			SpecName:      "test",
			SchemaVersion: 1,
			TestCases:     []TestCase{},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SpecId:        "", // empty
			SpecName:      "test",
			SchemaVersion: 1,
			TestCommands:  TestCommands{SpecTests: "test", AllTests: "test", Linter: "lint"},
			Dependencies:  []TaskDependency{},
			TaskGroups:    []TaskGroup{},
			Traceability:  []TraceabilityEntry{},
		},
	}

	result := spec.ValidateCrossFile()

	if result.Valid {
		t.Error("ValidateCrossFile().Valid = true, want false for incomplete spec")
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected exactly 1 error, got %d: %v", len(result.Errors), result.Errors)
	}

	e := result.Errors[0]
	if e.Category != "integrity" {
		t.Errorf("error Category = %q, want %q", e.Category, "integrity")
	}
	if e.Check != "completeness" {
		t.Errorf("error Check = %q, want %q", e.Check, "completeness")
	}
	// Message must mention all three artifact names
	for _, name := range []string{"requirements", "test_spec", "tasks"} {
		if !strings.Contains(e.Message, name) {
			t.Errorf("error Message %q does not contain %q", e.Message, name)
		}
	}
}

// TestValidateCrossFile_CompletenessGuard_OneEmpty verifies that
// ValidateCrossFile returns a completeness error naming only the
// incomplete artifact when exactly one has an empty SpecId.
// Test Spec: TS-NS-2. Requirement: NS-REQ-2.
func TestValidateCrossFile_CompletenessGuard_OneEmpty(t *testing.T) {
	spec := &Spec{
		SpecID:        "01",
		SpecName:      "test",
		Title:         "Test",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			SpecId:                "01",
			SpecName:              "test",
			SchemaVersion:         1,
			Introduction:          "Test.",
			Glossary:              RequirementsV1JsonGlossary{},
			Requirements:          []Requirement{},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "01",
			SpecName:      "test",
			SchemaVersion: 1,
			TestCases:     []TestCase{},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SpecId:        "", // only tasks is empty
			SpecName:      "test",
			SchemaVersion: 1,
			TestCommands:  TestCommands{SpecTests: "test", AllTests: "test", Linter: "lint"},
			Dependencies:  []TaskDependency{},
			TaskGroups:    []TaskGroup{},
			Traceability:  []TraceabilityEntry{},
		},
	}

	result := spec.ValidateCrossFile()

	if result.Valid {
		t.Error("ValidateCrossFile().Valid = true, want false for incomplete spec")
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected exactly 1 error, got %d: %v", len(result.Errors), result.Errors)
	}

	e := result.Errors[0]
	if e.Check != "completeness" {
		t.Errorf("error Check = %q, want %q", e.Check, "completeness")
	}
	if !strings.Contains(e.Message, "tasks") {
		t.Errorf("error Message %q does not contain %q", e.Message, "tasks")
	}
	if strings.Contains(e.Message, "requirements") {
		t.Errorf("error Message %q should not contain %q", e.Message, "requirements")
	}
	if strings.Contains(e.Message, "test_spec") {
		t.Errorf("error Message %q should not contain %q", e.Message, "test_spec")
	}
}

// TestValidateCrossFile_CompletenessGuard_SkipsDownstream verifies that
// when the completeness guard fires, no downstream rule errors
// (coverage_gap, dangling_reference, etc.) are emitted.
// Test Spec: TS-NS-3. Requirement: NS-REQ-3.
func TestValidateCrossFile_CompletenessGuard_SkipsDownstream(t *testing.T) {
	// Build a spec where requirements have criteria that would normally
	// produce coverage_gap errors, but test_spec has empty SpecId.
	spec := &Spec{
		SpecID:        "01",
		SpecName:      "test",
		Title:         "Test",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			SpecId:        "01",
			SpecName:      "test",
			SchemaVersion: 1,
			Introduction:  "Test.",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements: []Requirement{
				{
					Id:    "01-REQ-1",
					Title: "Requirement 1",
					UserStory: UserStory{
						Role: "dev", Goal: "test", Benefit: "validation",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "01-REQ-1.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "do something",
						},
					},
					EdgeCases: []Criterion{},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "", // empty — triggers completeness guard
			SpecName:      "test",
			SchemaVersion: 1,
			TestCases:     []TestCase{}, // no coverage → would normally be coverage_gap
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SpecId:        "01",
			SpecName:      "test",
			SchemaVersion: 1,
			TestCommands:  TestCommands{SpecTests: "test", AllTests: "test", Linter: "lint"},
			Dependencies:  []TaskDependency{},
			TaskGroups:    []TaskGroup{},
			Traceability:  []TraceabilityEntry{},
		},
	}

	result := spec.ValidateCrossFile()

	// Only the completeness error should be present
	for _, e := range result.Errors {
		if e.Check != "completeness" {
			t.Errorf("unexpected non-completeness error: Check=%q Message=%q", e.Check, e.Message)
		}
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected exactly 1 error (completeness), got %d: %v", len(result.Errors), result.Errors)
	}
}

// TestValidateCrossFile_NilRequirements verifies that ValidateCrossFile returns
// a completeness error when Requirements is nil, satisfying AC-1.
// Test Spec: TS-NS-1. Requirement: NS-REQ-1.
func TestValidateCrossFile_NilRequirements(t *testing.T) {
	spec := &Spec{
		SpecID:        "01",
		SpecName:      "test",
		Title:         "Test",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements:  nil, // missing
		TestSpec: &TestSpecV1Json{
			SpecId:        "01",
			SpecName:      "test",
			SchemaVersion: 1,
			TestCases:     []TestCase{},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SpecId:        "01",
			SpecName:      "test",
			SchemaVersion: 1,
			TestCommands:  TestCommands{SpecTests: "test", AllTests: "test", Linter: "lint"},
			Dependencies:  []TaskDependency{},
			TaskGroups:    []TaskGroup{},
			Traceability:  []TraceabilityEntry{},
		},
	}

	result := spec.ValidateCrossFile()

	if result.Valid {
		t.Error("ValidateCrossFile().Valid = true, want false when Requirements is nil")
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected exactly 1 error, got %d: %v", len(result.Errors), result.Errors)
	}
	e := result.Errors[0]
	if e.Category != "integrity" {
		t.Errorf("error Category = %q, want %q", e.Category, "integrity")
	}
	if e.Check != "completeness" {
		t.Errorf("error Check = %q, want %q", e.Check, "completeness")
	}
	if !strings.Contains(e.Message, "requirements") {
		t.Errorf("error Message %q does not contain %q", e.Message, "requirements")
	}
	if strings.Contains(e.Message, "test_spec") {
		t.Errorf("error Message %q should not contain %q", e.Message, "test_spec")
	}
	if strings.Contains(e.Message, "tasks") {
		t.Errorf("error Message %q should not contain %q", e.Message, "tasks")
	}
}

// TestValidateCrossFile_NilTestSpec verifies that ValidateCrossFile returns
// a completeness error when TestSpec is nil, satisfying AC-2.
// Test Spec: TS-NS-2. Requirement: NS-REQ-2.
func TestValidateCrossFile_NilTestSpec(t *testing.T) {
	spec := &Spec{
		SpecID:        "01",
		SpecName:      "test",
		Title:         "Test",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			SpecId:                "01",
			SpecName:              "test",
			SchemaVersion:         1,
			Introduction:          "Test.",
			Glossary:              RequirementsV1JsonGlossary{},
			Requirements:          []Requirement{},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: nil, // missing
		Tasks: &TasksV1Json{
			SpecId:        "01",
			SpecName:      "test",
			SchemaVersion: 1,
			TestCommands:  TestCommands{SpecTests: "test", AllTests: "test", Linter: "lint"},
			Dependencies:  []TaskDependency{},
			TaskGroups:    []TaskGroup{},
			Traceability:  []TraceabilityEntry{},
		},
	}

	result := spec.ValidateCrossFile()

	if result.Valid {
		t.Error("ValidateCrossFile().Valid = true, want false when TestSpec is nil")
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected exactly 1 error, got %d: %v", len(result.Errors), result.Errors)
	}
	e := result.Errors[0]
	if e.Check != "completeness" {
		t.Errorf("error Check = %q, want %q", e.Check, "completeness")
	}
	if !strings.Contains(e.Message, "test_spec") {
		t.Errorf("error Message %q does not contain %q", e.Message, "test_spec")
	}
	if strings.Contains(e.Message, "requirements") {
		t.Errorf("error Message %q should not contain %q", e.Message, "requirements")
	}
	if strings.Contains(e.Message, "tasks") {
		t.Errorf("error Message %q should not contain %q", e.Message, "tasks")
	}
}

// TestValidateCrossFile_NilTasks verifies that ValidateCrossFile returns
// a completeness error when Tasks is nil, satisfying AC-3.
// Test Spec: TS-NS-3. Requirement: NS-REQ-3.
func TestValidateCrossFile_NilTasks(t *testing.T) {
	spec := &Spec{
		SpecID:        "01",
		SpecName:      "test",
		Title:         "Test",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			SpecId:                "01",
			SpecName:              "test",
			SchemaVersion:         1,
			Introduction:          "Test.",
			Glossary:              RequirementsV1JsonGlossary{},
			Requirements:          []Requirement{},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "01",
			SpecName:      "test",
			SchemaVersion: 1,
			TestCases:     []TestCase{},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: nil, // missing
	}

	result := spec.ValidateCrossFile()

	if result.Valid {
		t.Error("ValidateCrossFile().Valid = true, want false when Tasks is nil")
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected exactly 1 error, got %d: %v", len(result.Errors), result.Errors)
	}
	e := result.Errors[0]
	if e.Check != "completeness" {
		t.Errorf("error Check = %q, want %q", e.Check, "completeness")
	}
	if !strings.Contains(e.Message, "tasks") {
		t.Errorf("error Message %q does not contain %q", e.Message, "tasks")
	}
	if strings.Contains(e.Message, "requirements") {
		t.Errorf("error Message %q should not contain %q", e.Message, "requirements")
	}
	if strings.Contains(e.Message, "test_spec") {
		t.Errorf("error Message %q should not contain %q", e.Message, "test_spec")
	}
}

// TestValidateCrossFile_AllNilArtifacts verifies that ValidateCrossFile returns
// exactly one completeness error listing all three artifact names when all
// three pointers are nil, satisfying AC-4.
// Test Spec: TS-NS-4. Requirement: NS-REQ-4.
func TestValidateCrossFile_AllNilArtifacts(t *testing.T) {
	spec := &Spec{
		SpecID:        "01",
		SpecName:      "test",
		Title:         "Test",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements:  nil, // missing
		TestSpec:      nil, // missing
		Tasks:         nil, // missing
	}

	result := spec.ValidateCrossFile()

	if result.Valid {
		t.Error("ValidateCrossFile().Valid = true, want false when all artifacts are nil")
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected exactly 1 error, got %d: %v", len(result.Errors), result.Errors)
	}
	e := result.Errors[0]
	if e.Category != "integrity" {
		t.Errorf("error Category = %q, want %q", e.Category, "integrity")
	}
	if e.Check != "completeness" {
		t.Errorf("error Check = %q, want %q", e.Check, "completeness")
	}
	for _, name := range []string{"requirements", "test_spec", "tasks"} {
		if !strings.Contains(e.Message, name) {
			t.Errorf("error Message %q does not contain %q", e.Message, name)
		}
	}
}

// TestValidateCrossFile_CompletenessGuard_NoRegression verifies that
// ValidateCrossFile proceeds normally with zero errors for a fully
// populated valid spec (existing test, but explicitly re-confirmed).
// Test Spec: TS-NS-5. Requirement: NS-REQ-5.
// (Covered by TestValidateCrossFile_ConsistentReferences below)

// TestValidateCrossFile_ConsistentReferences verifies that ValidateCrossFile
// on a spec with consistent cross-file references returns Valid true.
// Test Spec: TS-01-12, TS-NS-4. Requirement: 01-REQ-6.1, NS-REQ-4.
func TestValidateCrossFile_ConsistentReferences(t *testing.T) {
	defer requireImplemented(t)

	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec returned unexpected error: %v", err)
	}

	result := spec.ValidateCrossFile()
	if !result.Valid {
		t.Errorf("ValidateCrossFile().Valid = false, want true; errors = %v", result.Errors)
	}
	if len(result.Errors) != 0 {
		t.Errorf("ValidateCrossFile().Errors has %d entries, want 0", len(result.Errors))
	}
}

// TestValidateCrossFile_DanglingReference verifies that ValidateCrossFile
// records a dangling reference error when a test entry references a
// requirement ID that does not exist.
// Test Spec: TS-01-13, Requirement: 01-REQ-6.2
func TestValidateCrossFile_DanglingReference(t *testing.T) {
	defer requireImplemented(t)

	spec := buildSpecWithDanglingRef("XX-REQ-999")

	result := spec.ValidateCrossFile()
	if result.Valid {
		t.Error("ValidateCrossFile().Valid = true, want false for dangling reference")
	}

	// Find the dangling_reference error
	var found bool
	for _, e := range result.Errors {
		if e.Check == "dangling_reference" {
			found = true
			if e.Category != "integrity" {
				t.Errorf("dangling_reference error category = %q, want %q", e.Category, "integrity")
			}
			if !strings.Contains(e.Message, "XX-REQ-999") {
				t.Errorf("dangling_reference error message %q does not contain %q", e.Message, "XX-REQ-999")
			}
			break
		}
	}
	if !found {
		t.Error("expected a dangling_reference error in ValidationResult.Errors")
	}
}

// TestValidateCrossFile_NoTestEntries verifies that ValidateCrossFile reports
// coverage gaps for requirements with no test entries.
// Requirement: 01-REQ-6.E1
func TestValidateCrossFile_NoTestEntries(t *testing.T) {
	defer requireImplemented(t)

	spec := buildSpecWithNoTestEntries()

	result := spec.ValidateCrossFile()

	// Should report coverage gaps (as warnings or errors) for uncovered requirements
	hasGap := false
	for _, e := range result.Errors {
		if strings.Contains(e.Check, "coverage") || strings.Contains(e.Message, "coverage") {
			hasGap = true
			break
		}
	}
	for _, w := range result.Warnings {
		if strings.Contains(w.Check, "coverage") || strings.Contains(w.Message, "coverage") {
			hasGap = true
			break
		}
	}
	if !hasGap {
		t.Error("expected coverage gap warning or error for requirements with no test entries")
	}
}

// TestValidateCrossFile_IDFormatViolation verifies that ValidateCrossFile
// records an ID format validation error for malformed IDs.
// Requirement: 01-REQ-6.E2
func TestValidateCrossFile_IDFormatViolation(t *testing.T) {
	defer requireImplemented(t)

	spec := buildSpecWithBadID()

	result := spec.ValidateCrossFile()
	if result.Valid {
		t.Error("ValidateCrossFile().Valid = true, want false for ID format violation")
	}

	var found bool
	for _, e := range result.Errors {
		if e.Check == "id_format" {
			found = true
			if e.Category != "integrity" {
				t.Errorf("id_format error category = %q, want %q", e.Category, "integrity")
			}
			break
		}
	}
	if !found {
		t.Error("expected an id_format error in ValidationResult.Errors")
	}
}

// ---------------------------------------------------------------------------
// Subtask 2.2: ValidateCrossSpec and ValidateStructured
// ---------------------------------------------------------------------------

// TestValidateCrossSpec_CompatibleSpecs verifies that ValidateCrossSpec
// with compatible specs returns Valid true.
// Test Spec: TS-01-14, Requirement: 01-REQ-7.1
func TestValidateCrossSpec_CompatibleSpecs(t *testing.T) {
	defer requireImplemented(t)

	specA := buildSpecA()
	specB := buildSpecB()
	graph := &DependencyGraph{
		Edges: []DependencyEdge{
			{FromSpec: "01", ToSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
		},
	}

	result := ValidateCrossSpec([]*Spec{specA, specB}, graph)
	if !result.Valid {
		t.Errorf("ValidateCrossSpec().Valid = false, want true; errors = %v", result.Errors)
	}
	if len(result.Errors) != 0 {
		t.Errorf("ValidateCrossSpec().Errors has %d entries, want 0", len(result.Errors))
	}
}

// TestValidateCrossSpec_SingleSpec verifies that ValidateCrossSpec with a
// single spec (no dependencies) returns Valid true.
// Requirement: 01-REQ-7.E1
func TestValidateCrossSpec_SingleSpec(t *testing.T) {
	defer requireImplemented(t)

	spec := buildSpecA()
	graph := &DependencyGraph{Edges: []DependencyEdge{}}

	result := ValidateCrossSpec([]*Spec{spec}, graph)
	if !result.Valid {
		t.Errorf("ValidateCrossSpec().Valid = false, want true for single spec; errors = %v", result.Errors)
	}
	if len(result.Errors) != 0 {
		t.Errorf("ValidateCrossSpec().Errors has %d entries, want 0", len(result.Errors))
	}
}

// TestValidateCrossSpec_GlossaryConflict verifies that ValidateCrossSpec
// records a glossary conflict error when two specs define the same term
// with different meanings.
// Requirement: 01-REQ-7.E2
func TestValidateCrossSpec_GlossaryConflict(t *testing.T) {
	defer requireImplemented(t)

	specA := buildSpecA()
	specB := buildSpecWithConflictingGlossary()
	graph := &DependencyGraph{
		Edges: []DependencyEdge{
			{FromSpec: "01", ToSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
		},
	}

	result := ValidateCrossSpec([]*Spec{specA, specB}, graph)
	if result.Valid {
		t.Error("ValidateCrossSpec().Valid = true, want false for glossary conflict")
	}

	var found bool
	for _, e := range result.Errors {
		if e.Check == "glossary_conflict" {
			found = true
			if e.Category != "integrity" {
				t.Errorf("glossary_conflict error category = %q, want %q", e.Category, "integrity")
			}
			break
		}
	}
	if !found {
		t.Error("expected a glossary_conflict error in ValidationResult.Errors")
	}
}

// TestValidateStructured_ValidSpec verifies that ValidateStructured on a
// valid spec returns a map with 'valid' true, 'errors' as empty slice,
// and 'warnings' key absent (matching Python behavior: omit when empty).
// Test Spec: TS-01-15, TS-NS-5, Requirement: 01-REQ-8.1, NS-REQ-5
func TestValidateStructured_ValidSpec(t *testing.T) {
	defer requireImplemented(t)

	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec returned unexpected error: %v", err)
	}

	result := spec.ValidateStructured()

	valid, ok := result["valid"].(bool)
	if !ok {
		t.Fatal("result['valid'] is not a bool")
	}
	if !valid {
		t.Error("result['valid'] = false, want true")
	}

	errs, ok := result["errors"].([]map[string]any)
	if !ok {
		t.Fatalf("result['errors'] is not []map[string]any, got %T", result["errors"])
	}
	if len(errs) != 0 {
		t.Errorf("result['errors'] has %d entries, want 0", len(errs))
	}

	// NS-REQ-5: 'warnings' key must be absent when no warnings exist
	if _, exists := result["warnings"]; exists {
		t.Error("result has 'warnings' key but should be absent when no warnings exist (NS-REQ-5)")
	}
}

// TestValidateStructured_SchemaErrors verifies that ValidateStructured on
// a spec with schema errors returns a map with 'valid' false and 'errors'
// entries with the schema shape: 'category'="schema", 'artifact', 'message',
// and optional 'path'.
// Test Spec: TS-01-16, TS-NS-4, Requirement: 01-REQ-8.2, NS-REQ-4
func TestValidateStructured_SchemaErrors(t *testing.T) {
	defer requireImplemented(t)

	spec := buildSchemaViolatingSpec()

	result := spec.ValidateStructured()

	valid, ok := result["valid"].(bool)
	if !ok {
		t.Fatal("result['valid'] is not a bool")
	}
	if valid {
		t.Error("result['valid'] = true, want false for schema-violating spec")
	}

	errs, ok := result["errors"].([]map[string]any)
	if !ok {
		t.Fatalf("result['errors'] is not []map[string]any, got %T", result["errors"])
	}
	if len(errs) == 0 {
		t.Fatal("result['errors'] is empty, want at least one entry")
	}

	// NS-REQ-4: Schema errors use {"category": "schema", "artifact": ..., "message": ...}
	// with optional "path" and "value".
	var foundSchema bool
	for _, e := range errs {
		if cat, _ := e["category"].(string); cat == "schema" {
			foundSchema = true
			if msg, _ := e["message"].(string); msg == "" {
				t.Error("schema error message is empty, want non-empty")
			}
			if _, hasArtifact := e["artifact"]; !hasArtifact {
				t.Error("schema error map missing 'artifact' key (NS-REQ-4)")
			}
			// Schema errors must NOT have 'file' key (uses 'artifact' instead)
			if _, hasFile := e["file"]; hasFile {
				t.Error("schema error map has 'file' key, want 'artifact' instead (NS-REQ-4)")
			}
			break
		}
	}
	if !foundSchema {
		t.Error("expected at least one error with category='schema'")
	}
}

// TestValidateStructured_IntegrityErrors verifies that ValidateStructured
// on a spec with integrity errors returns entries with the integrity shape:
// 'category'="integrity", 'check', 'message' — without 'file'/'path'.
// Test Spec: TS-01-17, TS-NS-4, Requirement: 01-REQ-8.3, NS-REQ-4
func TestValidateStructured_IntegrityErrors(t *testing.T) {
	defer requireImplemented(t)

	spec := buildSpecWithDanglingRef("XX-REQ-999")

	result := spec.ValidateStructured()

	valid, ok := result["valid"].(bool)
	if !ok {
		t.Fatal("result['valid'] is not a bool")
	}
	if valid {
		t.Error("result['valid'] = true, want false for integrity error")
	}

	errs, ok := result["errors"].([]map[string]any)
	if !ok {
		t.Fatalf("result['errors'] is not []map[string]any, got %T", result["errors"])
	}

	// NS-REQ-4: Integrity errors use {"category": "integrity", "check": ..., "message": ...}
	// without 'file'/'path' keys.
	var found bool
	for _, e := range errs {
		if cat, _ := e["category"].(string); cat == "integrity" {
			found = true
			if check, _ := e["check"].(string); check == "" {
				t.Error("integrity error 'check' is empty, want non-empty")
			}
			if msg, _ := e["message"].(string); msg == "" {
				t.Error("integrity error message is empty, want non-empty")
			}
			// Integrity errors must NOT have 'file' or 'path' keys
			if _, hasFile := e["file"]; hasFile {
				t.Error("integrity error map has 'file' key, should be absent (NS-REQ-4)")
			}
			if _, hasPath := e["path"]; hasPath {
				t.Error("integrity error map has 'path' key, should be absent (NS-REQ-4)")
			}
			break
		}
	}
	if !found {
		t.Error("expected at least one integrity error in result['errors']")
	}
}

// TestValidateStructured_WithWarnings verifies that ValidateStructured on
// a spec with warnings includes a 'warnings' key with entries containing
// 'category'="warning", 'message', and 'entity_id' fields.
// Test Spec: TS-01-18, TS-NS-3, Requirement: 01-REQ-8.4, NS-REQ-3
func TestValidateStructured_WithWarnings(t *testing.T) {
	defer requireImplemented(t)

	spec := buildSpecWithWarnings()

	result := spec.ValidateStructured()

	valid, ok := result["valid"].(bool)
	if !ok {
		t.Fatal("result['valid'] is not a bool")
	}
	if !valid {
		t.Errorf("result['valid'] = false, want true (warnings only); errors = %v", result["errors"])
	}

	warnings, ok := result["warnings"].([]map[string]any)
	if !ok {
		t.Fatalf("result['warnings'] is not []map[string]any, got %T", result["warnings"])
	}
	if len(warnings) == 0 {
		t.Fatal("result['warnings'] is empty, want at least one entry")
	}

	// NS-REQ-3: Each warning must have category="warning", message, and entity_id.
	w := warnings[0]
	if cat, _ := w["category"].(string); cat != "warning" {
		t.Errorf("warning category = %q, want %q (NS-REQ-3)", cat, "warning")
	}
	if msg, _ := w["message"].(string); msg == "" {
		t.Error("warning message is empty, want non-empty")
	}
	if eid, _ := w["entity_id"].(string); eid == "" {
		t.Error("warning entity_id is empty, want non-empty")
	}
}

// TestValidateStructured_WarningsKeyOmittedWhenEmpty verifies that the
// 'warnings' key is omitted from ValidateStructured() output when there
// are no warnings, matching Python's `if warning_dicts: output["warnings"] = warning_dicts`.
// Test Spec: TS-NS-5, Requirement: NS-REQ-5
func TestValidateStructured_WarningsKeyOmittedWhenEmpty(t *testing.T) {
	defer requireImplemented(t)

	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec returned unexpected error: %v", err)
	}

	result := spec.ValidateStructured()

	// NS-REQ-5: 'warnings' key must be absent when there are no warnings.
	if _, exists := result["warnings"]; exists {
		t.Error("result has 'warnings' key but should be absent when no warnings exist (NS-REQ-5)")
	}
}

// ---------------------------------------------------------------------------
// Test helpers for building test specs
// ---------------------------------------------------------------------------

// buildInvalidSpec creates a Spec with a schema violation: requirement
// with missing required fields.
func buildInvalidSpec() *Spec {
	return &Spec{
		SpecID:        "01",
		SpecName:      "test_feature",
		Title:         "Test Feature",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test-author",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			SpecId:        "01",
			SpecName:      "test_feature",
			SchemaVersion: 1,
			Introduction:  "Test.",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements: []Requirement{
				{
					// Missing ID and Title — schema violation
					Id:    "", // empty ID violates minLength
					Title: "", // empty title violates minLength
					UserStory: UserStory{
						Role:    "dev",
						Goal:    "test",
						Benefit: "testing",
					},
					AcceptanceCriteria: []Criterion{},
					EdgeCases:          []Criterion{},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
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
}

// buildSchemaViolatingSpec creates a Spec where requirements.json has a
// schema violation (requirement with empty ID).
func buildSchemaViolatingSpec() *Spec {
	return buildInvalidSpec() // Same as buildInvalidSpec — empty ID violates schema
}

// buildMultiArtifactSchemaViolatingSpec creates a Spec with schema
// violations in multiple artifacts.
func buildMultiArtifactSchemaViolatingSpec() *Spec {
	spec := buildInvalidSpec()
	// Also make test_spec have violations
	spec.TestSpec = &TestSpecV1Json{
		SpecId:        "01",
		SpecName:      "test_feature",
		SchemaVersion: 1,
		TestCases: []TestCase{
			{
				Id:                  "", // empty — schema violation
				RequirementId:       "",
				Kind:                "unit",
				Description:         "Bad test case",
				Preconditions:       []string{},
				Expected:            "something",
				AssertionPseudocode: "assert true",
			},
		},
		PropertyTests: []PropertyTest{},
		EdgeCaseTests: []EdgeCaseTest{},
		SmokeTests:    []SmokeTest{},
		Coverage:      Coverage{},
	}
	return spec
}

// buildSpecWithDanglingRef creates a Spec where a test case references
// a requirement ID that does not exist in requirements.json.
func buildSpecWithDanglingRef(danglingID string) *Spec {
	return &Spec{
		SpecID:        "01",
		SpecName:      "test_feature",
		Title:         "Test Feature",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test-author",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			SpecId:        "01",
			SpecName:      "test_feature",
			SchemaVersion: 1,
			Introduction:  "Test.",
			Glossary:      RequirementsV1JsonGlossary{"spec": "A specification."},
			Requirements: []Requirement{
				{
					Id:    "01-REQ-1",
					Title: "Data Model",
					UserStory: UserStory{
						Role:    "developer",
						Goal:    "have typed models",
						Benefit: "type safety",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:             "01-REQ-1.1",
							EarsPattern:    CriterionEarsPatternUbiquitous,
							System:         "the system",
							Action:         "return a populated Spec",
							ReturnContract: nil,
						},
					},
					EdgeCases: []Criterion{},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "01",
			SpecName:      "test_feature",
			SchemaVersion: 1,
			TestCases: []TestCase{
				{
					Id:                  "TS-01-1",
					RequirementId:       danglingID, // references non-existent requirement
					Kind:                "unit",
					Description:         "Test with dangling ref",
					Preconditions:       []string{},
					Expected:            "error",
					AssertionPseudocode: "assert false",
				},
			},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
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
}

// buildSpecWithWarnings creates a Spec that passes validation but triggers
// at least one warning (vague language in a criterion action field).
// All criteria have test coverage so no coverage_gap errors are produced.
func buildSpecWithWarnings() *Spec {
	return &Spec{
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
			Schema:        "https://agent-fox.dev/schemas/requirements.v1.json",
			SpecId:        "01",
			SpecName:      "test_feature",
			SchemaVersion: 1,
			Introduction:  "Test.",
			Glossary:      RequirementsV1JsonGlossary{"spec": "A specification."},
			Requirements: []Requirement{
				{
					Id:    "01-REQ-1",
					Title: "Data Model",
					UserStory: UserStory{
						Role:    "developer",
						Goal:    "have typed models",
						Benefit: "type safety",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:             "01-REQ-1.1",
							EarsPattern:    CriterionEarsPatternUbiquitous,
							System:         "the system",
							Action:         "properly return a populated Spec",
							ReturnContract: nil,
						},
					},
					EdgeCases: []Criterion{},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths: []ExecutionPath{
				{Id: "01-PATH-1", Title: "Load spec", Steps: []PathStep{
					{Actor: "caller", Action: "invoke load"},
					{Actor: "system", Action: "return result"},
				}},
			},
			ErrorHandling: []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			Schema:        "https://agent-fox.dev/schemas/test_spec.v1.json",
			SpecId:        "01",
			SpecName:      "test_feature",
			SchemaVersion: 1,
			TestCases: []TestCase{
				{
					Id:                  "TS-01-1",
					RequirementId:       "01-REQ-1.1",
					Kind:                "unit",
					Description:         "Test requirement 1",
					Preconditions:       []string{},
					Expected:            "pass",
					AssertionPseudocode: "assert true",
				},
			},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests: []SmokeTest{
				{Id: "TS-01-SMOKE-1", ExecutionPathId: "01-PATH-1", Description: "Smoke test", Trigger: "invoke load", RealComponents: []string{"core"}, Mockable: []string{}, ExpectedEffects: []string{"returns result"}},
			},
			Coverage: Coverage{
				RequirementsCovered: []string{"01-REQ-1.1"},
			},
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
			TaskGroups: []TaskGroup{
				{
					Id: 1, Kind: TaskGroupKindTests, Title: "Tests",
					Subtasks: []Subtask{
						{Id: "1.1", Title: "Write tests", State: SubtaskStatePending, Details: []string{"create tests"}, TestSpecRefs: []string{"TS-01-1"}, RequirementRefs: []string{"01-REQ-1"}, Optional: false},
					},
					Verification: VerificationSubtask{Id: "1.V", Checks: []string{"all tests pass"}},
				},
				{
					Id: 2, Kind: TaskGroupKindWiringVerification, Title: "Wiring Verification",
					Subtasks: []Subtask{
						{Id: "2.1", Title: "Stub and dead-code audit", State: SubtaskStatePending, Details: []string{"verify wiring"}, TestSpecRefs: []string{"TS-01-SMOKE-1"}, RequirementRefs: []string{"01-REQ-1"}, Optional: false},
					},
					Verification: VerificationSubtask{Id: "2.V", Checks: []string{"smoke tests pass"}},
				},
			},
			Traceability: []TraceabilityEntry{},
		},
	}
}

// buildSpecWithNoTestEntries creates a Spec with requirements but no test entries.
func buildSpecWithNoTestEntries() *Spec {
	return &Spec{
		SpecID:        "01",
		SpecName:      "test_feature",
		Title:         "Test Feature",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test-author",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			SpecId:        "01",
			SpecName:      "test_feature",
			SchemaVersion: 1,
			Introduction:  "Test.",
			Glossary:      RequirementsV1JsonGlossary{"spec": "A specification."},
			Requirements: []Requirement{
				{
					Id:    "01-REQ-1",
					Title: "Data Model",
					UserStory: UserStory{
						Role:    "developer",
						Goal:    "have typed models",
						Benefit: "type safety",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:             "01-REQ-1.1",
							EarsPattern:    CriterionEarsPatternUbiquitous,
							System:         "the system",
							Action:         "return a populated Spec",
							ReturnContract: nil,
						},
					},
					EdgeCases: []Criterion{},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "01",
			SpecName:      "test_feature",
			SchemaVersion: 1,
			TestCases:     []TestCase{}, // no test entries
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
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
}

// buildSpecWithBadID creates a Spec with a malformed ID in a requirement.
func buildSpecWithBadID() *Spec {
	return &Spec{
		SpecID:        "01",
		SpecName:      "test_feature",
		Title:         "Test Feature",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test-author",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			SpecId:        "01",
			SpecName:      "test_feature",
			SchemaVersion: 1,
			Introduction:  "Test.",
			Glossary:      RequirementsV1JsonGlossary{"spec": "A specification."},
			Requirements: []Requirement{
				{
					Id:    "bad!!id!!format",
					Title: "Bad ID Requirement",
					UserStory: UserStory{
						Role:    "developer",
						Goal:    "test ID format",
						Benefit: "validation",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:             "bad!!criterion!!id",
							EarsPattern:    CriterionEarsPatternEventDriven,
							System:         "the system",
							Action:         "return error",
							ReturnContract: nil,
						},
					},
					EdgeCases: []Criterion{},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
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
}

// buildSpecA creates a test Spec with ID "01" for cross-spec tests.
func buildSpecA() *Spec {
	return &Spec{
		SpecID:        "01",
		SpecName:      "spec_a",
		Title:         "Spec A",
		Status:        "active",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test-author",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Spec A\n",
		Requirements: &RequirementsV1Json{
			SpecId:                "01",
			SpecName:              "spec_a",
			SchemaVersion:         1,
			Introduction:          "Spec A.",
			Glossary:              RequirementsV1JsonGlossary{"widget": "A UI component."},
			Requirements:          []Requirement{},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "01",
			SpecName:      "spec_a",
			SchemaVersion: 1,
			TestCases:     []TestCase{},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SpecId:        "01",
			SpecName:      "spec_a",
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
}

// buildSpecB creates a test Spec with ID "02" for cross-spec tests,
// with a compatible glossary.
func buildSpecB() *Spec {
	return &Spec{
		SpecID:        "02",
		SpecName:      "spec_b",
		Title:         "Spec B",
		Status:        "active",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test-author",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Spec B\n",
		Requirements: &RequirementsV1Json{
			SpecId:                "02",
			SpecName:              "spec_b",
			SchemaVersion:         1,
			Introduction:          "Spec B.",
			Glossary:              RequirementsV1JsonGlossary{"gadget": "A device."},
			Requirements:          []Requirement{},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "02",
			SpecName:      "spec_b",
			SchemaVersion: 1,
			TestCases:     []TestCase{},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SpecId:        "02",
			SpecName:      "spec_b",
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
}

// buildSpecWithConflictingGlossary creates a Spec whose glossary defines
// the same term as specA but with a different meaning.
func buildSpecWithConflictingGlossary() *Spec {
	spec := buildSpecB()
	// Add "widget" with a different definition than spec A
	spec.Requirements.Glossary["widget"] = "A mechanical gear component."
	return spec
}

// ---------------------------------------------------------------------------
// Task Group 1: Cross-file rules 3, 4, 5, 6, 8, 9
// Test helpers
// ---------------------------------------------------------------------------

// filterByCheck returns all ValidationEntry values whose Check field
// matches the given check name.
func filterByCheck(entries []ValidationEntry, check string) []ValidationEntry {
	var filtered []ValidationEntry
	for _, e := range entries {
		if e.Check == check {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// buildCrossFileBaseSpec returns a minimal *Spec with consistent IDs and
// all required fields populated. Callers mutate the returned spec to add
// specific test data for cross-file rules.
func buildCrossFileBaseSpec() *Spec {
	return &Spec{
		SpecID:        "04",
		SpecName:      "test_crossfile",
		Title:         "Cross-File Test Spec",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test",
		Source:        "https://example.com",
		Supersedes:    []string{},
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			Schema:                "https://agent-fox.dev/schemas/requirements.v1.json",
			SpecId:                "04",
			SpecName:              "test_crossfile",
			SchemaVersion:         1,
			Introduction:          "Test spec for cross-file validation.",
			Glossary:              RequirementsV1JsonGlossary{},
			Requirements:          []Requirement{},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			Schema:        "https://agent-fox.dev/schemas/test_spec.v1.json",
			SpecId:        "04",
			SpecName:      "test_crossfile",
			SchemaVersion: 1,
			TestCases:     []TestCase{},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			Schema:        "https://agent-fox.dev/schemas/tasks.v1.json",
			SpecId:        "04",
			SpecName:      "test_crossfile",
			SchemaVersion: 1,
			TestCommands: TestCommands{
				SpecTests: "go test ./...",
				AllTests:  "go test ./...",
				Linter:    "go vet ./...",
			},
			Dependencies: []TaskDependency{},
			// task_groups requires minItems:1 per Section 8.3; scaffold with
			// the mandatory first ("tests") and last ("wiring_verification") groups.
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
				{
					Id:       2,
					Kind:     TaskGroupKindWiringVerification,
					Title:    "Wiring Verification",
					Subtasks: []Subtask{},
					Verification: VerificationSubtask{
						Id:     "2.V",
						Checks: []string{"no stubs remain"},
					},
				},
			},
			Traceability: []TraceabilityEntry{},
		},
	}
}

// ---------------------------------------------------------------------------
// Subtask 1.1: Property test coverage rule (04-REQ-1)
// Test Spec: TS-04-1, TS-04-2
// ---------------------------------------------------------------------------

// TestValidateCrossFile_PropertyTestCoverage verifies that ValidateCrossFile
// produces one integrity error per correctness_property that lacks a matching
// property_test (matched by property_id), with the Path field set to the
// JSON path of the uncovered property.
// Test Spec: TS-04-1, TS-04-2. Requirements: 04-REQ-1.1, 04-REQ-1.2,
// 04-REQ-1.E1, 04-REQ-1.E2, 04-REQ-1.E3.
func TestValidateCrossFile_PropertyTestCoverage(t *testing.T) {
	tests := []struct {
		name             string
		spec             *Spec
		wantErrorCount   int      // expected count of cross_file_3 errors
		wantUncoveredIDs []string // property IDs expected in RequirementID fields
		wantHasPath      bool     // if true, assert Path is non-empty on each error
	}{
		{
			name: "one_uncovered_property",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.CorrectnessProperties = []CorrectnessProperty{
					{Id: "04-PROP-1", Title: "Prop A", ForAny: "any input", Invariant: "holds", Validates: []string{"04-REQ-1.1"}},
					{Id: "04-PROP-2", Title: "Prop B", ForAny: "any input", Invariant: "holds", Validates: []string{"04-REQ-1.1"}},
				}
				s.TestSpec.PropertyTests = []PropertyTest{
					{Id: "TS-04-P1", PropertyId: "04-PROP-1", Description: "test A", ForAnyStrategy: "random", InvariantCheck: "assert true", Validates: []string{"04-REQ-1.1"}},
				}
				return s
			}(),
			wantErrorCount:   1,
			wantUncoveredIDs: []string{"04-PROP-2"},
			wantHasPath:      true,
		},
		{
			name: "all_properties_covered",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.CorrectnessProperties = []CorrectnessProperty{
					{Id: "04-PROP-1", Title: "Prop A", ForAny: "any input", Invariant: "holds", Validates: []string{"04-REQ-1.1"}},
				}
				s.TestSpec.PropertyTests = []PropertyTest{
					{Id: "TS-04-P1", PropertyId: "04-PROP-1", Description: "test A", ForAnyStrategy: "random", InvariantCheck: "assert true", Validates: []string{"04-REQ-1.1"}},
				}
				return s
			}(),
			wantErrorCount: 0,
		},
		{
			name: "no_correctness_properties",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				// No correctness properties — rule should be skipped
				return s
			}(),
			wantErrorCount: 0,
		},
		{
			name: "no_test_spec_all_uncovered",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.CorrectnessProperties = []CorrectnessProperty{
					{Id: "04-PROP-1", Title: "Prop A", ForAny: "any input", Invariant: "holds", Validates: []string{"04-REQ-1.1"}},
					{Id: "04-PROP-2", Title: "Prop B", ForAny: "any input", Invariant: "holds", Validates: []string{"04-REQ-1.1"}},
				}
				// No property tests — simulate a test_spec with no coverage by
				// clearing the PropertyTests slice (nil would trigger the nil-artifact
				// completeness guard and return early before coverage checks).
				s.TestSpec.PropertyTests = []PropertyTest{}
				return s
			}(),
			wantErrorCount:   2,
			wantUncoveredIDs: []string{"04-PROP-1", "04-PROP-2"},
		},
		{
			name: "duplicate_property_tests_still_covered",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.CorrectnessProperties = []CorrectnessProperty{
					{Id: "04-PROP-1", Title: "Prop A", ForAny: "any input", Invariant: "holds", Validates: []string{"04-REQ-1.1"}},
				}
				s.TestSpec.PropertyTests = []PropertyTest{
					{Id: "TS-04-P1", PropertyId: "04-PROP-1", Description: "test A", ForAnyStrategy: "random", InvariantCheck: "assert true", Validates: []string{"04-REQ-1.1"}},
					{Id: "TS-04-P2", PropertyId: "04-PROP-1", Description: "test A dup", ForAnyStrategy: "random", InvariantCheck: "assert true", Validates: []string{"04-REQ-1.1"}},
				}
				return s
			}(),
			wantErrorCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.spec.ValidateCrossFile()
			errors := filterByCheck(result.Errors, "cross_file_3")

			if len(errors) != tt.wantErrorCount {
				t.Errorf("cross_file_3 error count = %d, want %d; errors: %v",
					len(errors), tt.wantErrorCount, errors)
			}

			for _, e := range errors {
				if e.Category != "integrity" {
					t.Errorf("cross_file_3 error category = %q, want %q", e.Category, "integrity")
				}
				if e.Message == "" {
					t.Error("cross_file_3 error has empty Message")
				}
			}

			// Check that uncovered property IDs appear in RequirementID fields
			for _, wantID := range tt.wantUncoveredIDs {
				found := false
				for _, e := range errors {
					if e.RequirementID == wantID {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected uncovered property %q in RequirementID fields, not found", wantID)
				}
			}

			// TS-04-2: Path field should reference the correctness_property location
			if tt.wantHasPath {
				for _, e := range errors {
					if e.Path == "" {
						t.Errorf("cross_file_3 error for %q has empty Path, want non-empty JSON path", e.RequirementID)
					}
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Issue #24: correctness_property.validates referential integrity
// Test Spec: TS-NS-1, TS-NS-2, TS-NS-3, TS-NS-4
// Requirements: NS-REQ-1, NS-REQ-2, NS-REQ-3, NS-REQ-4
// ---------------------------------------------------------------------------

// buildBaseSpecWithCriterion returns a spec with one requirement whose
// acceptance criterion has ID "04-REQ-1.1", for use in validates-ref tests.
func buildBaseSpecWithCriterion() *Spec {
	s := buildCrossFileBaseSpec()
	s.Requirements.Requirements = []Requirement{
		{
			Id:    "04-REQ-1",
			Title: "Req 1",
			UserStory: UserStory{
				Role: "dev", Goal: "test", Benefit: "validation",
			},
			AcceptanceCriteria: []Criterion{
				{
					Id:          "04-REQ-1.1",
					EarsPattern: CriterionEarsPatternUbiquitous,
					System:      "the system",
					Action:      "do something",
				},
			},
			EdgeCases: []Criterion{},
		},
	}
	return s
}

// TestValidateCrossFile_ValidatesRefIntegrity verifies that ValidateCrossFile
// emits a validates_ref integrity error for each unknown criterion ID in a
// correctness property's validates list, and emits no error when all IDs are
// valid criterion IDs. Requirements: NS-REQ-1.1, NS-REQ-2.1, NS-REQ-3.1,
// NS-REQ-4.1.
func TestValidateCrossFile_ValidatesRefIntegrity(t *testing.T) {
	tests := []struct {
		name           string
		spec           *Spec
		wantErrorCount int    // expected count of validates_ref errors
		wantMsgContain string // substring expected in at least one error message
	}{
		{
			// TS-NS-1: single non-existent criterion ID emits one error
			name: "single_unknown_criterion_emits_error",
			spec: func() *Spec {
				s := buildBaseSpecWithCriterion()
				s.Requirements.CorrectnessProperties = []CorrectnessProperty{
					{
						Id:        "04-PROP-1",
						Title:     "Prop 1",
						ForAny:    "any input",
						Invariant: "holds",
						Validates: []string{"XX-REQ-99.1"},
					},
				}
				return s
			}(),
			wantErrorCount: 1,
			wantMsgContain: "XX-REQ-99.1",
		},
		{
			// TS-NS-2: all validates entries resolve to valid criterion IDs — no error
			name: "valid_criterion_ids_no_error",
			spec: func() *Spec {
				s := buildBaseSpecWithCriterion()
				s.Requirements.CorrectnessProperties = []CorrectnessProperty{
					{
						Id:        "04-PROP-1",
						Title:     "Prop 1",
						ForAny:    "any input",
						Invariant: "holds",
						Validates: []string{"04-REQ-1.1"},
					},
				}
				return s
			}(),
			wantErrorCount: 0,
		},
		{
			// TS-NS-3: two unknown IDs in one property → two separate errors
			name: "two_unknown_ids_emit_two_errors",
			spec: func() *Spec {
				s := buildBaseSpecWithCriterion()
				s.Requirements.CorrectnessProperties = []CorrectnessProperty{
					{
						Id:        "04-PROP-1",
						Title:     "Prop 1",
						ForAny:    "any input",
						Invariant: "holds",
						Validates: []string{"04-REQ-1.1", "MISSING-REQ-9.9", "ALSO-MISSING-REQ-8.8"},
					},
				}
				return s
			}(),
			wantErrorCount: 2,
			wantMsgContain: "MISSING-REQ-9.9",
		},
		{
			// TS-NS-4: top-level requirement ID is NOT a valid criterion target
			name: "top_level_req_id_emits_error",
			spec: func() *Spec {
				s := buildBaseSpecWithCriterion()
				s.Requirements.CorrectnessProperties = []CorrectnessProperty{
					{
						Id:        "04-PROP-1",
						Title:     "Prop 1",
						ForAny:    "any input",
						Invariant: "holds",
						// "04-REQ-1" is a top-level requirement ID, not a criterion
						Validates: []string{"04-REQ-1"},
					},
				}
				return s
			}(),
			wantErrorCount: 1,
			wantMsgContain: "04-REQ-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.spec.ValidateCrossFile()
			errs := filterByCheck(result.Errors, "validates_ref")

			if len(errs) != tt.wantErrorCount {
				t.Errorf("validates_ref error count = %d, want %d; errors: %v",
					len(errs), tt.wantErrorCount, errs)
			}

			for _, e := range errs {
				if e.Category != "integrity" {
					t.Errorf("validates_ref error category = %q, want %q", e.Category, "integrity")
				}
				if e.Message == "" {
					t.Error("validates_ref error has empty Message")
				}
			}

			if tt.wantMsgContain != "" {
				found := false
				for _, e := range errs {
					if strings.Contains(e.Message, tt.wantMsgContain) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("no validates_ref error message contains %q; errors: %v", tt.wantMsgContain, errs)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Subtask 1.2: Execution path smoke test coverage rule (04-REQ-2)
// Test Spec: TS-04-3
// ---------------------------------------------------------------------------

// TestValidateCrossFile_ExecutionPathSmokeCoverage verifies that
// ValidateCrossFile produces one integrity error per execution_path that
// lacks a matching smoke_test (matched by execution_path_id).
// Test Spec: TS-04-3. Requirements: 04-REQ-2.1, 04-REQ-2.E1, 04-REQ-2.E2.
func TestValidateCrossFile_ExecutionPathSmokeCoverage(t *testing.T) {
	tests := []struct {
		name             string
		spec             *Spec
		wantErrorCount   int
		wantUncoveredIDs []string // execution_path IDs expected in error Messages
	}{
		{
			name: "one_uncovered_execution_path",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.ExecutionPaths = []ExecutionPath{
					{Id: "04-PATH-1", Title: "Path A", Steps: []PathStep{{Actor: "CLI", Action: "step 1"}, {Actor: "System", Action: "step 2"}}},
					{Id: "04-PATH-2", Title: "Path B", Steps: []PathStep{{Actor: "CLI", Action: "step 1"}, {Actor: "System", Action: "step 2"}}},
				}
				s.TestSpec.SmokeTests = []SmokeTest{
					{Id: "TS-04-SMOKE-1", ExecutionPathId: "04-PATH-1", Description: "smoke A", Trigger: "run", ExpectedEffects: []string{"pass"}, Mockable: []string{}, RealComponents: []string{"all"}},
				}
				return s
			}(),
			wantErrorCount:   1,
			wantUncoveredIDs: []string{"04-PATH-2"},
		},
		{
			name: "all_paths_covered",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.ExecutionPaths = []ExecutionPath{
					{Id: "04-PATH-1", Title: "Path A", Steps: []PathStep{{Actor: "CLI", Action: "step 1"}, {Actor: "System", Action: "step 2"}}},
				}
				s.TestSpec.SmokeTests = []SmokeTest{
					{Id: "TS-04-SMOKE-1", ExecutionPathId: "04-PATH-1", Description: "smoke A", Trigger: "run", ExpectedEffects: []string{"pass"}, Mockable: []string{}, RealComponents: []string{"all"}},
				}
				return s
			}(),
			wantErrorCount: 0,
		},
		{
			name: "no_execution_paths",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				// No execution paths — rule should be skipped
				return s
			}(),
			wantErrorCount: 0,
		},
		{
			name: "no_test_spec_all_uncovered",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.ExecutionPaths = []ExecutionPath{
					{Id: "04-PATH-1", Title: "Path A", Steps: []PathStep{{Actor: "CLI", Action: "step 1"}, {Actor: "System", Action: "step 2"}}},
					{Id: "04-PATH-2", Title: "Path B", Steps: []PathStep{{Actor: "CLI", Action: "step 1"}, {Actor: "System", Action: "step 2"}}},
				}
				// No smoke tests — simulate a test_spec with no path coverage by
				// clearing the SmokeTests slice (nil would trigger the nil-artifact
				// completeness guard and return early before coverage checks).
				s.TestSpec.SmokeTests = []SmokeTest{}
				return s
			}(),
			wantErrorCount:   2,
			wantUncoveredIDs: []string{"04-PATH-1", "04-PATH-2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.spec.ValidateCrossFile()
			errors := filterByCheck(result.Errors, "cross_file_4")

			if len(errors) != tt.wantErrorCount {
				t.Errorf("cross_file_4 error count = %d, want %d; errors: %v",
					len(errors), tt.wantErrorCount, errors)
			}

			for _, e := range errors {
				if e.Category != "integrity" {
					t.Errorf("cross_file_4 error category = %q, want %q", e.Category, "integrity")
				}
				if e.Message == "" {
					t.Error("cross_file_4 error has empty Message")
				}
			}

			// Verify uncovered path IDs appear in error messages
			for _, wantID := range tt.wantUncoveredIDs {
				found := false
				for _, e := range errors {
					if strings.Contains(e.Message, wantID) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected uncovered path %q in error Message, not found", wantID)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Subtask 1.3: test_spec_id resolution rule (04-REQ-3)
// Test Spec: TS-04-4
// ---------------------------------------------------------------------------

// TestValidateCrossFile_TestSpecIDResolution verifies that ValidateCrossFile
// produces one integrity error per unresolvable test_spec_id found in
// traceability entries or subtask test_spec_refs.
// Test Spec: TS-04-4. Requirements: 04-REQ-3.1, 04-REQ-3.E1, 04-REQ-3.E2.
func TestValidateCrossFile_TestSpecIDResolution(t *testing.T) {
	tests := []struct {
		name           string
		spec           *Spec
		wantErrorCount int
		wantMissing    []string // unresolvable IDs expected in Messages
	}{
		{
			name: "unresolvable_in_traceability_and_subtask_refs",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Tasks.Traceability = []TraceabilityEntry{
					{RequirementId: "04-REQ-1", TestSpecId: "TS-04-NONEXISTENT", TaskId: "4.1"},
				}
				s.Tasks.TaskGroups = []TaskGroup{
					{
						Id:    1,
						Title: "Group 1",
						Kind:  TaskGroupKindStandard,
						Subtasks: []Subtask{
							{
								Id: "1.1", Title: "sub1", State: SubtaskStatePending,
								Details:         []string{"detail"},
								RequirementRefs: []string{},
								TestSpecRefs:    []string{"TS-04-ALSO-MISSING"},
							},
						},
						Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
					},
				}
				return s
			}(),
			wantErrorCount: 2,
			wantMissing:    []string{"TS-04-NONEXISTENT", "TS-04-ALSO-MISSING"},
		},
		{
			name: "same_unresolvable_id_in_both_locations",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Tasks.Traceability = []TraceabilityEntry{
					{RequirementId: "04-REQ-1", TestSpecId: "TS-04-GHOST", TaskId: "4.1"},
				}
				s.Tasks.TaskGroups = []TaskGroup{
					{
						Id:    1,
						Title: "Group 1",
						Kind:  TaskGroupKindStandard,
						Subtasks: []Subtask{
							{
								Id: "1.1", Title: "sub1", State: SubtaskStatePending,
								Details:         []string{"detail"},
								RequirementRefs: []string{},
								TestSpecRefs:    []string{"TS-04-GHOST"},
							},
						},
						Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
					},
				}
				return s
			}(),
			wantErrorCount: 2, // one per occurrence, not deduplicated
			wantMissing:    []string{"TS-04-GHOST"},
		},
		{
			name: "all_ids_resolve",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.TestSpec.TestCases = []TestCase{
					{Id: "TS-04-1", RequirementId: "04-REQ-1.1", Kind: "unit", Description: "test", Preconditions: []string{}, Expected: "pass", AssertionPseudocode: "assert true"},
				}
				s.Requirements.Requirements = []Requirement{
					{
						Id:    "04-REQ-1",
						Title: "Req 1",
						UserStory: UserStory{
							Role: "dev", Goal: "test", Benefit: "coverage",
						},
						AcceptanceCriteria: []Criterion{
							{Id: "04-REQ-1.1", EarsPattern: CriterionEarsPatternUbiquitous, System: "the system", Action: "do something", ReturnContract: nil},
						},
						EdgeCases: []Criterion{},
					},
				}
				s.Tasks.Traceability = []TraceabilityEntry{
					{RequirementId: "04-REQ-1.1", TestSpecId: "TS-04-1", TaskId: "4.1"},
				}
				return s
			}(),
			wantErrorCount: 0,
		},
		{
			name: "no_traceability_no_refs",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				// No traceability entries, no subtask test_spec_refs
				return s
			}(),
			wantErrorCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.spec.ValidateCrossFile()
			errors := filterByCheck(result.Errors, "cross_file_5")

			if len(errors) != tt.wantErrorCount {
				t.Errorf("cross_file_5 error count = %d, want %d; errors: %v",
					len(errors), tt.wantErrorCount, errors)
			}

			for _, e := range errors {
				if e.Category != "integrity" {
					t.Errorf("cross_file_5 error category = %q, want %q", e.Category, "integrity")
				}
			}

			// Verify unresolvable IDs appear in error messages
			for _, wantID := range tt.wantMissing {
				found := false
				for _, e := range errors {
					if strings.Contains(e.Message, wantID) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected unresolvable ID %q in error Message, not found", wantID)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Subtask 1.4: Glossary backtick term check (04-REQ-4)
// Test Spec: TS-04-5, TS-04-6
// ---------------------------------------------------------------------------

// TestValidateCrossFile_GlossaryBacktickTerms verifies that ValidateCrossFile
// extracts backtick-wrapped terms from criterion and property fields and
// produces one integrity error per term not present in the glossary, after
// applying exclusion rules (numeric, single-char, quoted, >80 chars).
// Test Spec: TS-04-5. Requirements: 04-REQ-4.1, 04-REQ-4.E1 through 04-REQ-4.E5.
func TestValidateCrossFile_GlossaryBacktickTerms(t *testing.T) {
	tests := []struct {
		name           string
		spec           *Spec
		wantErrorCount int
		wantTerms      []string // terms expected in error Messages
		wantAbsent     []string // terms that should NOT appear in errors
	}{
		{
			name: "missing_glossary_term_with_numeric_excluded",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.Requirements = []Requirement{
					{
						Id:    "04-REQ-1",
						Title: "Req 1",
						UserStory: UserStory{
							Role: "dev", Goal: "test", Benefit: "validation",
						},
						AcceptanceCriteria: []Criterion{
							{
								Id:             "04-REQ-1.1",
								EarsPattern:    CriterionEarsPatternUbiquitous,
								System:         "the system",
								Action:         "call `MyTerm` with value `-1`",
								ReturnContract: nil,
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				// Glossary does not contain 'MyTerm'
				return s
			}(),
			wantErrorCount: 1,
			wantTerms:      []string{"MyTerm"},
			wantAbsent:     []string{"-1"}, // numeric exclusion
		},
		{
			name: "term_in_glossary_no_error",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.Glossary = RequirementsV1JsonGlossary{
					"MyTerm": "A defined term.",
				}
				s.Requirements.Requirements = []Requirement{
					{
						Id:    "04-REQ-1",
						Title: "Req 1",
						UserStory: UserStory{
							Role: "dev", Goal: "test", Benefit: "validation",
						},
						AcceptanceCriteria: []Criterion{
							{
								Id:             "04-REQ-1.1",
								EarsPattern:    CriterionEarsPatternUbiquitous,
								System:         "the system",
								Action:         "call `MyTerm` method",
								ReturnContract: nil,
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				return s
			}(),
			wantErrorCount: 0,
		},
		{
			name: "single_char_excluded",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.Requirements = []Requirement{
					{
						Id:    "04-REQ-1",
						Title: "Req 1",
						UserStory: UserStory{
							Role: "dev", Goal: "test", Benefit: "validation",
						},
						AcceptanceCriteria: []Criterion{
							{
								Id:             "04-REQ-1.1",
								EarsPattern:    CriterionEarsPatternUbiquitous,
								System:         "the system",
								Action:         "set `x` to zero",
								ReturnContract: nil,
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				return s
			}(),
			wantErrorCount: 0,
			wantAbsent:     []string{"x"},
		},
		{
			name: "longer_than_80_chars_excluded",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				longTerm := strings.Repeat("a", 81) // 81 chars
				s.Requirements.Requirements = []Requirement{
					{
						Id:    "04-REQ-1",
						Title: "Req 1",
						UserStory: UserStory{
							Role: "dev", Goal: "test", Benefit: "validation",
						},
						AcceptanceCriteria: []Criterion{
							{
								Id:             "04-REQ-1.1",
								EarsPattern:    CriterionEarsPatternUbiquitous,
								System:         "the system",
								Action:         "use `" + longTerm + "` here",
								ReturnContract: nil,
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				return s
			}(),
			wantErrorCount: 0,
		},
		{
			name: "decimal_numeric_excluded",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.Requirements = []Requirement{
					{
						Id:    "04-REQ-1",
						Title: "Req 1",
						UserStory: UserStory{
							Role: "dev", Goal: "test", Benefit: "validation",
						},
						AcceptanceCriteria: []Criterion{
							{
								Id:             "04-REQ-1.1",
								EarsPattern:    CriterionEarsPatternUbiquitous,
								System:         "the system",
								Action:         "return `3.14` as the result",
								ReturnContract: nil,
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				return s
			}(),
			wantErrorCount: 0,
			wantAbsent:     []string{"3.14"},
		},
		{
			name: "quoted_string_excluded",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.Requirements = []Requirement{
					{
						Id:    "04-REQ-1",
						Title: "Req 1",
						UserStory: UserStory{
							Role: "dev", Goal: "test", Benefit: "validation",
						},
						AcceptanceCriteria: []Criterion{
							{
								Id:             "04-REQ-1.1",
								EarsPattern:    CriterionEarsPatternUbiquitous,
								System:         "the system",
								Action:         "return `\"hello world\"` as string",
								ReturnContract: nil,
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				return s
			}(),
			wantErrorCount: 0,
		},
		{
			name: "multiple_occurrences_one_error_each",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.Requirements = []Requirement{
					{
						Id:    "04-REQ-1",
						Title: "Req 1",
						UserStory: UserStory{
							Role: "dev", Goal: "test", Benefit: "validation",
						},
						AcceptanceCriteria: []Criterion{
							{
								Id:             "04-REQ-1.1",
								EarsPattern:    CriterionEarsPatternUbiquitous,
								System:         "the system",
								Action:         "call `UndefinedTerm` method",
								ReturnContract: nil,
							},
						},
						EdgeCases: []Criterion{},
					},
					{
						Id:    "04-REQ-2",
						Title: "Req 2",
						UserStory: UserStory{
							Role: "dev", Goal: "test", Benefit: "validation",
						},
						AcceptanceCriteria: []Criterion{
							{
								Id:             "04-REQ-2.1",
								EarsPattern:    CriterionEarsPatternUbiquitous,
								System:         "the system",
								Action:         "use `UndefinedTerm` again",
								ReturnContract: nil,
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				return s
			}(),
			wantErrorCount: 2, // one per occurrence, not deduplicated
			wantTerms:      []string{"UndefinedTerm"},
		},
		{
			name: "correctness_property_fields_checked",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.CorrectnessProperties = []CorrectnessProperty{
					{
						Id:        "04-PROP-1",
						Title:     "Prop 1",
						ForAny:    "any `PropTerm` input",
						Invariant: "the `InvTerm` holds",
						Validates: []string{"04-REQ-1.1"},
					},
				}
				return s
			}(),
			wantErrorCount: 2,
			wantTerms:      []string{"PropTerm", "InvTerm"},
		},
		{
			name: "empty_glossary_all_terms_flagged",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.Glossary = RequirementsV1JsonGlossary{} // empty
				s.Requirements.Requirements = []Requirement{
					{
						Id:    "04-REQ-1",
						Title: "Req 1",
						UserStory: UserStory{
							Role: "dev", Goal: "test", Benefit: "validation",
						},
						AcceptanceCriteria: []Criterion{
							{
								Id:             "04-REQ-1.1",
								EarsPattern:    CriterionEarsPatternUbiquitous,
								System:         "the system",
								Action:         "call `TermA` and `TermB`",
								ReturnContract: nil,
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				return s
			}(),
			wantErrorCount: 2,
			wantTerms:      []string{"TermA", "TermB"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.spec.ValidateCrossFile()
			errors := filterByCheck(result.Errors, "cross_file_6")

			if len(errors) != tt.wantErrorCount {
				t.Errorf("cross_file_6 error count = %d, want %d; errors: %v",
					len(errors), tt.wantErrorCount, errors)
			}

			for _, e := range errors {
				if e.Category != "integrity" {
					t.Errorf("cross_file_6 error category = %q, want %q", e.Category, "integrity")
				}
			}

			// Verify expected terms appear in error messages
			for _, wantTerm := range tt.wantTerms {
				found := false
				for _, e := range errors {
					if strings.Contains(e.Message, wantTerm) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected term %q in error Message, not found", wantTerm)
				}
			}

			// Verify excluded terms do NOT appear in error messages
			for _, absentTerm := range tt.wantAbsent {
				for _, e := range errors {
					if strings.Contains(e.Message, absentTerm) {
						t.Errorf("excluded term %q should not appear in error Message, but found in: %q",
							absentTerm, e.Message)
					}
				}
			}
		})
	}
}

// TestValidateCrossFile_BacktickRegexPackageLevel verifies that the backtick
// extraction regex is compiled at package initialization time (package-level
// var) rather than inside the ValidateCrossFile function body.
// Test Spec: TS-04-6. Requirement: 04-REQ-4.2.
func TestValidateCrossFile_BacktickRegexPackageLevel(t *testing.T) {
	// Read all non-test Go source files in the package directory
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("failed to read package directory: %v", err)
	}

	var foundPackageLevel bool
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("failed to read %s: %v", name, err)
		}
		content := string(data)

		// Check for a package-level backtick regex variable
		// (declared outside any function body as a var or with MustCompile)
		if strings.Contains(content, "backtick") && strings.Contains(content, "regexp.MustCompile") {
			foundPackageLevel = true
		}
	}

	if !foundPackageLevel {
		t.Error("no package-level backtick regex variable found; " +
			"expected a var declaration with regexp.MustCompile for backtick term extraction")
	}
}

// ---------------------------------------------------------------------------
// Subtask 1.5: Traceability deduplication rule (04-REQ-5)
// Test Spec: TS-04-7
// ---------------------------------------------------------------------------

// TestValidateCrossFile_TraceabilityDeduplication verifies that
// ValidateCrossFile produces one integrity error per duplicate
// (requirement_id, test_spec_id) pair beyond the first occurrence.
// Test Spec: TS-04-7. Requirements: 04-REQ-5.1, 04-REQ-5.E1, 04-REQ-5.E2.
func TestValidateCrossFile_TraceabilityDeduplication(t *testing.T) {
	tests := []struct {
		name           string
		spec           *Spec
		wantErrorCount int
		wantPairInMsg  []string // strings expected in error Messages
	}{
		{
			name: "three_duplicates_two_errors",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Tasks.Traceability = []TraceabilityEntry{
					{RequirementId: "04-REQ-1", TestSpecId: "TS-04-1", TaskId: "4.1"},
					{RequirementId: "04-REQ-1", TestSpecId: "TS-04-1", TaskId: "4.1"},
					{RequirementId: "04-REQ-1", TestSpecId: "TS-04-1", TaskId: "4.1"},
				}
				return s
			}(),
			wantErrorCount: 2, // N-1 = 3-1 = 2
			wantPairInMsg:  []string{"04-REQ-1", "TS-04-1"},
		},
		{
			name: "two_duplicates_one_error",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Tasks.Traceability = []TraceabilityEntry{
					{RequirementId: "04-REQ-1", TestSpecId: "TS-04-1", TaskId: "4.1"},
					{RequirementId: "04-REQ-1", TestSpecId: "TS-04-1", TaskId: "4.2"},
				}
				return s
			}(),
			wantErrorCount: 1,
			wantPairInMsg:  []string{"04-REQ-1", "TS-04-1"},
		},
		{
			name: "all_unique_no_error",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Tasks.Traceability = []TraceabilityEntry{
					{RequirementId: "04-REQ-1", TestSpecId: "TS-04-1", TaskId: "4.1"},
					{RequirementId: "04-REQ-1", TestSpecId: "TS-04-2", TaskId: "4.2"},
					{RequirementId: "04-REQ-2", TestSpecId: "TS-04-1", TaskId: "4.3"},
				}
				return s
			}(),
			wantErrorCount: 0,
		},
		{
			name: "no_traceability_entries",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				// No traceability entries
				return s
			}(),
			wantErrorCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.spec.ValidateCrossFile()
			errors := filterByCheck(result.Errors, "cross_file_8")

			if len(errors) != tt.wantErrorCount {
				t.Errorf("cross_file_8 error count = %d, want %d; errors: %v",
					len(errors), tt.wantErrorCount, errors)
			}

			for _, e := range errors {
				if e.Category != "integrity" {
					t.Errorf("cross_file_8 error category = %q, want %q", e.Category, "integrity")
				}
				if e.Message == "" {
					t.Error("cross_file_8 error has empty Message")
				}
			}

			// Verify pair identifiers appear in error messages
			if tt.wantErrorCount > 0 {
				for _, wantStr := range tt.wantPairInMsg {
					found := false
					for _, e := range errors {
						if strings.Contains(e.Message, wantStr) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected %q in cross_file_8 error Message, not found", wantStr)
					}
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Subtask 1.6: Subtask requirement_refs resolution rule (04-REQ-6)
// Test Spec: TS-04-8
// ---------------------------------------------------------------------------

// TestValidateCrossFile_SubtaskRequirementRefs verifies that
// ValidateCrossFile produces one integrity error per subtask
// requirement_refs entry that does not resolve to a known requirement ID,
// criterion ID, or edge case ID.
// Test Spec: TS-04-8. Requirements: 04-REQ-6.1, 04-REQ-6.E1, 04-REQ-6.E2.
func TestValidateCrossFile_SubtaskRequirementRefs(t *testing.T) {
	tests := []struct {
		name           string
		spec           *Spec
		wantErrorCount int
		wantMissing    []string // unresolvable refs expected in Messages
		wantAbsent     []string // valid refs that should NOT appear as errors
	}{
		{
			name: "one_unresolvable_ref",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.Requirements = []Requirement{
					{
						Id:    "04-REQ-1",
						Title: "Req 1",
						UserStory: UserStory{
							Role: "dev", Goal: "test", Benefit: "validation",
						},
						AcceptanceCriteria: []Criterion{
							{Id: "04-REQ-1.1", EarsPattern: CriterionEarsPatternUbiquitous, System: "the system", Action: "do something", ReturnContract: nil},
						},
						EdgeCases: []Criterion{
							{Id: "04-REQ-1.E1", EarsPattern: CriterionEarsPatternUbiquitous, System: "the system", Action: "handle edge", ReturnContract: nil},
						},
					},
				}
				s.Tasks.TaskGroups = []TaskGroup{
					{
						Id:    1,
						Title: "Group 1",
						Kind:  TaskGroupKindStandard,
						Subtasks: []Subtask{
							{
								Id: "1.1", Title: "sub1", State: SubtaskStatePending,
								Details:         []string{"detail"},
								RequirementRefs: []string{"04-REQ-1", "04-REQ-UNKNOWN", "04-REQ-1.E1"},
								TestSpecRefs:    []string{},
							},
						},
						Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
					},
				}
				return s
			}(),
			wantErrorCount: 1,
			wantMissing:    []string{"04-REQ-UNKNOWN"},
			wantAbsent:     []string{"04-REQ-1.E1"}, // edge case ID is valid
		},
		{
			name: "all_refs_resolve_including_edge_cases",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.Requirements = []Requirement{
					{
						Id:    "04-REQ-1",
						Title: "Req 1",
						UserStory: UserStory{
							Role: "dev", Goal: "test", Benefit: "validation",
						},
						AcceptanceCriteria: []Criterion{
							{Id: "04-REQ-1.1", EarsPattern: CriterionEarsPatternUbiquitous, System: "the system", Action: "do something", ReturnContract: nil},
						},
						EdgeCases: []Criterion{
							{Id: "04-REQ-1.E1", EarsPattern: CriterionEarsPatternUbiquitous, System: "the system", Action: "handle edge", ReturnContract: nil},
						},
					},
				}
				s.Tasks.TaskGroups = []TaskGroup{
					{
						Id:    1,
						Title: "Group 1",
						Kind:  TaskGroupKindStandard,
						Subtasks: []Subtask{
							{
								Id: "1.1", Title: "sub1", State: SubtaskStatePending,
								Details:         []string{"detail"},
								RequirementRefs: []string{"04-REQ-1", "04-REQ-1.1", "04-REQ-1.E1"},
								TestSpecRefs:    []string{},
							},
						},
						Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
					},
				}
				return s
			}(),
			wantErrorCount: 0,
		},
		{
			name: "no_subtasks_no_requirement_refs",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				// No task groups or subtasks
				return s
			}(),
			wantErrorCount: 0,
		},
		{
			name: "subtask_with_empty_requirement_refs",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Tasks.TaskGroups = []TaskGroup{
					{
						Id:    1,
						Title: "Group 1",
						Kind:  TaskGroupKindStandard,
						Subtasks: []Subtask{
							{
								Id: "1.1", Title: "sub1", State: SubtaskStatePending,
								Details:         []string{"detail"},
								RequirementRefs: []string{}, // empty — nothing to resolve
								TestSpecRefs:    []string{},
							},
						},
						Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
					},
				}
				return s
			}(),
			wantErrorCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.spec.ValidateCrossFile()
			errors := filterByCheck(result.Errors, "cross_file_9")

			if len(errors) != tt.wantErrorCount {
				t.Errorf("cross_file_9 error count = %d, want %d; errors: %v",
					len(errors), tt.wantErrorCount, errors)
			}

			for _, e := range errors {
				if e.Category != "integrity" {
					t.Errorf("cross_file_9 error category = %q, want %q", e.Category, "integrity")
				}
			}

			// Verify unresolvable refs appear in error messages
			for _, wantRef := range tt.wantMissing {
				found := false
				for _, e := range errors {
					if strings.Contains(e.Message, wantRef) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected unresolvable ref %q in error Message, not found", wantRef)
				}
			}

			// Verify valid refs do NOT appear as errors
			for _, absentRef := range tt.wantAbsent {
				for _, e := range errors {
					if strings.Contains(e.Message, absentRef) && !strings.Contains(e.Message, "04-REQ-UNKNOWN") {
						t.Errorf("valid ref %q should not appear as error, but found in: %q",
							absentRef, e.Message)
					}
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Task Group 2: Cross-file rules 7, 8, 9, 10 and task group structure
// Cross-spec rules 1, 3, 4, 5
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Subtask 2.1: Unwanted pattern return_contract rule (04-REQ-7)
// Test Spec: TS-04-9
// ---------------------------------------------------------------------------

// TestValidateCrossFile_UnwantedReturnContract verifies that ValidateCrossFile
// produces one integrity error for each criterion with ears_pattern='unwanted'
// that has a null or empty return_contract, and no error for unwanted criteria
// with a non-empty return_contract.
// Test Spec: TS-04-9. Requirements: 04-REQ-7.1, 04-REQ-7.E1, 04-REQ-7.E2.
func TestValidateCrossFile_UnwantedReturnContract(t *testing.T) {
	tests := []struct {
		name             string
		spec             *Spec
		wantErrorCount   int
		wantCriterionIDs []string // criterion IDs expected in error Messages
	}{
		{
			name: "null_return_contract_produces_error",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.Requirements = []Requirement{
					{
						Id:        "04-REQ-99",
						Title:     "Unwanted Test",
						UserStory: UserStory{Role: "dev", Goal: "test", Benefit: "validation"},
						AcceptanceCriteria: []Criterion{
							{
								Id: "04-REQ-99.1", EarsPattern: CriterionEarsPatternUnwanted,
								System: "the system", Action: "detect invalid input",
								ReturnContract: nil, // null — should trigger error
							},
							{
								Id: "04-REQ-99.2", EarsPattern: CriterionEarsPatternUnwanted,
								System: "the system", Action: "detect another error",
								ReturnContract: strPtr("returns error"), // non-null — no error
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				return s
			}(),
			wantErrorCount:   1,
			wantCriterionIDs: []string{"04-REQ-99.1"},
		},
		{
			name: "empty_string_return_contract_treated_as_null",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.Requirements = []Requirement{
					{
						Id:        "04-REQ-99",
						Title:     "Unwanted Test",
						UserStory: UserStory{Role: "dev", Goal: "test", Benefit: "validation"},
						AcceptanceCriteria: []Criterion{
							{
								Id: "04-REQ-99.1", EarsPattern: CriterionEarsPatternUnwanted,
								System: "the system", Action: "detect invalid input",
								ReturnContract: strPtr(""), // empty string — treated same as null
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				return s
			}(),
			wantErrorCount:   1,
			wantCriterionIDs: []string{"04-REQ-99.1"},
		},
		{
			name: "non_unwanted_pattern_null_contract_no_error",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.Requirements = []Requirement{
					{
						Id:        "04-REQ-99",
						Title:     "Non-Unwanted Test",
						UserStory: UserStory{Role: "dev", Goal: "test", Benefit: "validation"},
						AcceptanceCriteria: []Criterion{
							{
								Id: "04-REQ-99.1", EarsPattern: CriterionEarsPatternUbiquitous,
								System: "the system", Action: "do something",
								ReturnContract: nil, // null but not 'unwanted' — no error from this rule
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				return s
			}(),
			wantErrorCount: 0,
		},
		{
			name:           "no_unwanted_criteria_skips_rule",
			spec:           buildCrossFileBaseSpec(),
			wantErrorCount: 0,
		},
		{
			name: "edge_case_criteria_also_checked",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Requirements.Requirements = []Requirement{
					{
						Id:        "04-REQ-99",
						Title:     "Unwanted Edge Case Test",
						UserStory: UserStory{Role: "dev", Goal: "test", Benefit: "validation"},
						AcceptanceCriteria: []Criterion{
							{
								Id: "04-REQ-99.1", EarsPattern: CriterionEarsPatternUbiquitous,
								System: "the system", Action: "do something",
								ReturnContract: nil,
							},
						},
						EdgeCases: []Criterion{
							{
								Id: "04-REQ-99.E1", EarsPattern: CriterionEarsPatternUnwanted,
								System: "the system", Action: "handle edge error",
								ReturnContract: nil, // null unwanted edge case — should trigger error
							},
						},
					},
				}
				return s
			}(),
			wantErrorCount:   1,
			wantCriterionIDs: []string{"04-REQ-99.E1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.spec.ValidateCrossFile()
			errors := filterByCheck(result.Errors, "cross_file_10")

			if len(errors) != tt.wantErrorCount {
				t.Errorf("cross_file_10 error count = %d, want %d; errors: %v",
					len(errors), tt.wantErrorCount, errors)
			}

			for _, e := range errors {
				if e.Category != "integrity" {
					t.Errorf("cross_file_10 error category = %q, want %q", e.Category, "integrity")
				}
				if e.Message == "" {
					t.Error("cross_file_10 error has empty Message")
				}
			}

			// Check that expected criterion IDs appear in error messages
			for _, wantID := range tt.wantCriterionIDs {
				found := false
				for _, e := range errors {
					if strings.Contains(e.Message, wantID) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected criterion %q in error Message, not found", wantID)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Subtask 2.2: Task group structure validation (04-REQ-8)
// Test Spec: TS-04-10, TS-04-11
// ---------------------------------------------------------------------------

// TestValidateCrossFile_TaskGroupStructure verifies that ValidateCrossFile
// produces a schema-category error when the first task group does not have
// kind='tests' or the last task group does not have kind='wiring_verification',
// or when more than one wiring_verification group exists (spec 8.3).
// Test Spec: TS-04-10, TS-04-11, TS-NS-1, TS-NS-2, TS-NS-3, TS-NS-4.
// Requirements: 04-REQ-8.1, 04-REQ-8.2, 04-REQ-8.E1, 04-REQ-8.E2,
// NS-REQ-1, NS-REQ-2, NS-REQ-3, NS-REQ-4.
func TestValidateCrossFile_TaskGroupStructure(t *testing.T) {
	tests := []struct {
		name                   string
		spec                   *Spec
		wantErrorCount         int
		wantFirstGroupErr      bool // error about first group not being 'tests'
		wantLastGroupErr       bool // error about last group not being 'wiring_verification'
		wantDuplicateWiringErr bool // error about more than one wiring_verification group
	}{
		{
			name: "first_group_not_tests",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Tasks.TaskGroups = []TaskGroup{
					{Id: 1, Title: "Group 1", Kind: TaskGroupKindStandard, Subtasks: []Subtask{}, Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}}},
					{Id: 2, Title: "Group 2", Kind: TaskGroupKindWiringVerification, Subtasks: []Subtask{}, Verification: VerificationSubtask{Id: "2.V", Checks: []string{"check"}}},
				}
				return s
			}(),
			wantErrorCount:    1,
			wantFirstGroupErr: true,
		},
		{
			name: "last_group_not_wiring_verification",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Tasks.TaskGroups = []TaskGroup{
					{Id: 1, Title: "Group 1", Kind: TaskGroupKindTests, Subtasks: []Subtask{}, Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}}},
					{Id: 2, Title: "Group 2", Kind: TaskGroupKindStandard, Subtasks: []Subtask{}, Verification: VerificationSubtask{Id: "2.V", Checks: []string{"check"}}},
				}
				return s
			}(),
			wantErrorCount:   1,
			wantLastGroupErr: true,
		},
		{
			name: "correct_structure_no_errors",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Tasks.TaskGroups = []TaskGroup{
					{Id: 1, Title: "Group 1", Kind: TaskGroupKindTests, Subtasks: []Subtask{}, Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}}},
					{Id: 2, Title: "Group 2", Kind: TaskGroupKindWiringVerification, Subtasks: []Subtask{}, Verification: VerificationSubtask{Id: "2.V", Checks: []string{"check"}}},
				}
				return s
			}(),
			wantErrorCount: 0,
		},
		{
			name: "no_task_groups_skips_check",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Tasks.TaskGroups = []TaskGroup{} // empty
				return s
			}(),
			wantErrorCount: 0,
		},
		{
			name: "single_group_violates_both_rules",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Tasks.TaskGroups = []TaskGroup{
					{Id: 1, Title: "Group 1", Kind: TaskGroupKindStandard, Subtasks: []Subtask{}, Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}}},
				}
				return s
			}(),
			wantErrorCount:    2,
			wantFirstGroupErr: true,
			wantLastGroupErr:  true,
		},
		// TS-NS-1: Two wiring_verification groups must fail with a schema error.
		{
			name: "duplicate_wiring_verification",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Tasks.TaskGroups = []TaskGroup{
					{Id: 1, Title: "Tests", Kind: TaskGroupKindTests, Subtasks: []Subtask{}, Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}}},
					{Id: 2, Title: "Wiring 1", Kind: TaskGroupKindWiringVerification, Subtasks: []Subtask{}, Verification: VerificationSubtask{Id: "2.V", Checks: []string{"check"}}},
					{Id: 3, Title: "Standard", Kind: TaskGroupKindStandard, Subtasks: []Subtask{}, Verification: VerificationSubtask{Id: "3.V", Checks: []string{"check"}}},
					{Id: 4, Title: "Wiring 2", Kind: TaskGroupKindWiringVerification, Subtasks: []Subtask{}, Verification: VerificationSubtask{Id: "4.V", Checks: []string{"check"}}},
				}
				return s
			}(),
			wantErrorCount:         1,
			wantDuplicateWiringErr: true,
		},
		// TS-NS-3: Three wiring_verification groups must also fail validation.
		{
			name: "triple_wiring_verification",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Tasks.TaskGroups = []TaskGroup{
					{Id: 1, Title: "Tests", Kind: TaskGroupKindTests, Subtasks: []Subtask{}, Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}}},
					{Id: 2, Title: "Wiring 1", Kind: TaskGroupKindWiringVerification, Subtasks: []Subtask{}, Verification: VerificationSubtask{Id: "2.V", Checks: []string{"check"}}},
					{Id: 3, Title: "Wiring 2", Kind: TaskGroupKindWiringVerification, Subtasks: []Subtask{}, Verification: VerificationSubtask{Id: "3.V", Checks: []string{"check"}}},
					{Id: 4, Title: "Wiring 3", Kind: TaskGroupKindWiringVerification, Subtasks: []Subtask{}, Verification: VerificationSubtask{Id: "4.V", Checks: []string{"check"}}},
				}
				return s
			}(),
			wantErrorCount:         1,
			wantDuplicateWiringErr: true,
		},
		// TS-NS-4: Two wiring_verification groups AND last group not wiring_verification
		// must produce both errors independently (last-group check + duplicate check).
		{
			name: "duplicate_wiring_verification_last_not_wiring",
			spec: func() *Spec {
				s := buildCrossFileBaseSpec()
				s.Tasks.TaskGroups = []TaskGroup{
					{Id: 1, Title: "Tests", Kind: TaskGroupKindTests, Subtasks: []Subtask{}, Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}}},
					{Id: 2, Title: "Wiring 1", Kind: TaskGroupKindWiringVerification, Subtasks: []Subtask{}, Verification: VerificationSubtask{Id: "2.V", Checks: []string{"check"}}},
					{Id: 3, Title: "Wiring 2", Kind: TaskGroupKindWiringVerification, Subtasks: []Subtask{}, Verification: VerificationSubtask{Id: "3.V", Checks: []string{"check"}}},
					{Id: 4, Title: "Standard", Kind: TaskGroupKindStandard, Subtasks: []Subtask{}, Verification: VerificationSubtask{Id: "4.V", Checks: []string{"check"}}},
				}
				return s
			}(),
			wantErrorCount:         2,
			wantLastGroupErr:       true,
			wantDuplicateWiringErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.spec.ValidateCrossFile()
			errors := filterByCheck(result.Errors, "task_group_structure")

			if len(errors) != tt.wantErrorCount {
				t.Errorf("task_group_structure error count = %d, want %d; errors: %v",
					len(errors), tt.wantErrorCount, errors)
			}

			for _, e := range errors {
				if e.Category != "schema" {
					t.Errorf("task_group_structure error category = %q, want %q", e.Category, "schema")
				}
			}

			if tt.wantFirstGroupErr {
				found := false
				for _, e := range errors {
					lower := strings.ToLower(e.Message)
					if strings.Contains(lower, "tests") || strings.Contains(lower, "first") {
						found = true
						break
					}
				}
				if !found {
					t.Error("expected error mentioning 'tests' or 'first' for first group rule, not found")
				}
			}

			if tt.wantLastGroupErr {
				found := false
				for _, e := range errors {
					lower := strings.ToLower(e.Message)
					if strings.Contains(lower, "wiring_verification") || strings.Contains(lower, "last") {
						found = true
						break
					}
				}
				if !found {
					t.Error("expected error mentioning 'wiring_verification' or 'last' for last group rule, not found")
				}
			}

			if tt.wantDuplicateWiringErr {
				found := false
				for _, e := range errors {
					lower := strings.ToLower(e.Message)
					hasWiring := strings.Contains(lower, "wiring_verification")
					hasQuantifier := strings.Contains(lower, "once") ||
						strings.Contains(lower, "one") ||
						strings.Contains(lower, "duplicate") ||
						strings.Contains(lower, "multiple") ||
						strings.Contains(lower, "most") ||
						strings.Contains(lower, "found")
					if hasWiring && hasQuantifier {
						found = true
						break
					}
				}
				if !found {
					t.Error("expected error about multiple wiring_verification groups (mentioning 'wiring_verification' and 'once'/'one'/'duplicate'/'multiple'/'most'/'found'), not found")
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Subtask 2.3: Wiring verification group semantics (04-REQ-9)
// Test Spec: TS-04-12, TS-04-13, TS-04-14
// ---------------------------------------------------------------------------

// TestValidateCrossFile_WiringVerificationSemantics verifies that
// ValidateCrossFile checks the wiring_verification group for meaningful
// smoke test refs and a stub audit subtask. It produces integrity errors
// when test_spec_refs are empty, no SMOKE reference is found, and no
// subtask mentions 'stub' or 'dead'.
// Test Spec: TS-04-12, TS-04-13, TS-04-14. Requirements: 04-REQ-9.1,
// 04-REQ-9.2, 04-REQ-9.3, 04-REQ-9.E1, 04-REQ-9.E2.
func TestValidateCrossFile_WiringVerificationSemantics(t *testing.T) {
	// buildWiringSpec creates a spec with a proper first group (tests) and a
	// wiring_verification group containing the given subtasks.
	buildWiringSpec := func(subtasks []Subtask) *Spec {
		s := buildCrossFileBaseSpec()
		s.Tasks.TaskGroups = []TaskGroup{
			{
				Id: 1, Title: "Tests", Kind: TaskGroupKindTests,
				Subtasks:     []Subtask{},
				Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
			},
			{
				Id: 2, Title: "Wiring", Kind: TaskGroupKindWiringVerification,
				Subtasks:     subtasks,
				Verification: VerificationSubtask{Id: "2.V", Checks: []string{"check"}},
			},
		}
		return s
	}

	t.Run("all_subtasks_empty_test_spec_refs", func(t *testing.T) {
		spec := buildWiringSpec([]Subtask{
			{Id: "2.1", Title: "subtask A", State: SubtaskStatePending, Details: []string{"detail"}, RequirementRefs: []string{}, TestSpecRefs: []string{}},
			{Id: "2.2", Title: "subtask B", State: SubtaskStatePending, Details: []string{"detail"}, RequirementRefs: []string{}, TestSpecRefs: []string{}},
		})
		// Add a smoke test so that wiring checks A and B are enforced (not in bootstrap mode).
		spec.TestSpec.SmokeTests = []SmokeTest{{
			Id: "TS-04-SMOKE-1", Description: "Smoke test", Trigger: "run",
			RealComponents: []string{"all"}, Mockable: []string{}, ExpectedEffects: []string{"ok"},
		}}
		result := spec.ValidateCrossFile()

		var refErrors []ValidationEntry
		for _, e := range result.Errors {
			if e.Category == "integrity" && strings.Contains(strings.ToLower(e.Message), "test_spec_refs") {
				refErrors = append(refErrors, e)
			}
		}
		if len(refErrors) < 1 {
			t.Errorf("expected at least 1 integrity error about empty test_spec_refs in wiring group, got %d; all errors: %v",
				len(refErrors), result.Errors)
		}
	})

	t.Run("no_smoke_ref_in_test_spec_refs", func(t *testing.T) {
		spec := buildWiringSpec([]Subtask{
			{
				Id: "2.1", Title: "verify", State: SubtaskStatePending,
				Details: []string{"detail"}, RequirementRefs: []string{},
				TestSpecRefs: []string{"TS-04-1", "TS-04-2"}, // no SMOKE entries
			},
		})
		// Add a smoke test so that wiring check B is enforced (not in bootstrap mode).
		spec.TestSpec.SmokeTests = []SmokeTest{{
			Id: "TS-04-SMOKE-1", Description: "Smoke test", Trigger: "run",
			RealComponents: []string{"all"}, Mockable: []string{}, ExpectedEffects: []string{"ok"},
		}}
		result := spec.ValidateCrossFile()

		var smokeErrors []ValidationEntry
		for _, e := range result.Errors {
			if e.Category == "integrity" && strings.Contains(strings.ToLower(e.Message), "smoke") {
				smokeErrors = append(smokeErrors, e)
			}
		}
		if len(smokeErrors) < 1 {
			t.Errorf("expected at least 1 integrity error about missing smoke reference, got %d; all errors: %v",
				len(smokeErrors), result.Errors)
		}
	})

	t.Run("no_stub_or_dead_mention", func(t *testing.T) {
		spec := buildWiringSpec([]Subtask{
			{
				Id: "2.1", Title: "Run smoke tests", State: SubtaskStatePending,
				Details: []string{"Execute all integration tests"}, RequirementRefs: []string{},
				TestSpecRefs: []string{"TS-04-SMOKE-1"},
			},
		})
		result := spec.ValidateCrossFile()

		var stubErrors []ValidationEntry
		for _, e := range result.Errors {
			lower := strings.ToLower(e.Message)
			if e.Category == "integrity" && (strings.Contains(lower, "stub") || strings.Contains(lower, "dead")) {
				stubErrors = append(stubErrors, e)
			}
		}
		if len(stubErrors) < 1 {
			t.Errorf("expected at least 1 integrity error about missing stub/dead-code audit, got %d; all errors: %v",
				len(stubErrors), result.Errors)
		}
	})

	t.Run("fully_valid_wiring_group", func(t *testing.T) {
		spec := buildWiringSpec([]Subtask{
			{
				Id: "2.1", Title: "Run smoke tests", State: SubtaskStatePending,
				Details: []string{"detail"}, RequirementRefs: []string{},
				TestSpecRefs: []string{"TS-04-SMOKE-1"},
			},
			{
				Id: "2.2", Title: "Audit stub removal", State: SubtaskStatePending,
				Details: []string{"Verify all stubs are replaced"}, RequirementRefs: []string{},
				TestSpecRefs: []string{"TS-04-SMOKE-2"},
			},
		})
		result := spec.ValidateCrossFile()

		// Should have no wiring-specific integrity errors
		for _, e := range result.Errors {
			lower := strings.ToLower(e.Message)
			isWiringErr := (strings.Contains(lower, "wiring") &&
				(strings.Contains(lower, "test_spec_refs") ||
					strings.Contains(lower, "smoke") ||
					strings.Contains(lower, "stub") ||
					strings.Contains(lower, "dead")))
			if e.Category == "integrity" && isWiringErr {
				t.Errorf("unexpected wiring verification error: %v", e)
			}
		}
	})

	t.Run("no_wiring_group_skips_checks", func(t *testing.T) {
		s := buildCrossFileBaseSpec()
		s.Tasks.TaskGroups = []TaskGroup{
			{
				Id: 1, Title: "Tests", Kind: TaskGroupKindTests,
				Subtasks:     []Subtask{},
				Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
			},
		}
		result := s.ValidateCrossFile()

		// No wiring verification errors should appear when no wiring group exists
		for _, e := range result.Errors {
			lower := strings.ToLower(e.Message)
			isWiringErr := (strings.Contains(lower, "wiring") &&
				(strings.Contains(lower, "test_spec_refs") ||
					strings.Contains(lower, "smoke") ||
					strings.Contains(lower, "stub") ||
					strings.Contains(lower, "dead")))
			if e.Category == "integrity" && isWiringErr {
				t.Errorf("unexpected wiring verification error when no wiring group: %v", e)
			}
		}
	})

	// TS-NS-2: When smoke tests exist and the wiring group has no subtasks,
	// all three wiring verification checks (A, B, C) must fire.
	t.Run("wiring_group_no_subtasks_with_smoke_three_errors", func(t *testing.T) {
		spec := buildWiringSpec([]Subtask{}) // no subtasks
		// Add a smoke test so that A and B are enforced (not in bootstrap mode).
		spec.TestSpec.SmokeTests = []SmokeTest{{
			Id: "TS-04-SMOKE-1", Description: "Smoke test", Trigger: "run",
			RealComponents: []string{"all"}, Mockable: []string{}, ExpectedEffects: []string{"ok"},
		}}
		result := spec.ValidateCrossFile()

		var wiringErrors int
		for _, e := range result.Errors {
			lower := strings.ToLower(e.Message)
			if e.Category == "integrity" && (strings.Contains(lower, "test_spec_refs") ||
				strings.Contains(lower, "smoke") ||
				strings.Contains(lower, "stub") || strings.Contains(lower, "dead")) {
				wiringErrors++
			}
		}
		if wiringErrors != 3 {
			t.Errorf("expected 3 wiring verification errors for empty subtask group with smoke tests, got %d; all errors: %v",
				wiringErrors, result.Errors)
		}
	})

	// TS-NS-2: When no smoke tests exist (bootstrap mode), checks A and B are
	// skipped. Check C still runs; the wiring group uses "check" (no stub
	// mention) so C fires — 1 error total.
	t.Run("wiring_group_no_subtasks_no_smoke_skips_a_b", func(t *testing.T) {
		spec := buildWiringSpec([]Subtask{}) // no subtasks, no smoke tests
		result := spec.ValidateCrossFile()

		var wiringErrors int
		for _, e := range result.Errors {
			lower := strings.ToLower(e.Message)
			if e.Category == "integrity" && (strings.Contains(lower, "test_spec_refs") ||
				strings.Contains(lower, "smoke") ||
				strings.Contains(lower, "stub") || strings.Contains(lower, "dead")) {
				wiringErrors++
			}
		}
		// A and B are skipped (no smoke tests). C fires because "check" does
		// not mention stub or dead-code. So exactly 1 wiring error expected.
		if wiringErrors != 1 {
			t.Errorf("expected 1 wiring error (check C only) for bootstrap spec with no smoke tests, got %d; all errors: %v",
				wiringErrors, result.Errors)
		}
		// Confirm no error is about test_spec_refs or smoke (only C fires).
		for _, e := range result.Errors {
			lower := strings.ToLower(e.Message)
			if e.Category == "integrity" && (strings.Contains(lower, "test_spec_refs") || strings.Contains(lower, "smoke test pattern")) {
				t.Errorf("unexpected check A/B error in bootstrap mode: %v", e.Message)
			}
		}
	})

	// buildWiringSpecWithChecks creates a spec with a proper first group (tests)
	// and a wiring_verification group containing the given subtasks, with custom
	// Verification.Checks on the wiring group.
	buildWiringSpecWithChecks := func(subtasks []Subtask, checks []string) *Spec {
		s := buildCrossFileBaseSpec()
		s.Tasks.TaskGroups = []TaskGroup{
			{
				Id: 1, Title: "Tests", Kind: TaskGroupKindTests,
				Subtasks:     []Subtask{},
				Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
			},
			{
				Id: 2, Title: "Wiring", Kind: TaskGroupKindWiringVerification,
				Subtasks:     subtasks,
				Verification: VerificationSubtask{Id: "2.V", Checks: checks},
			},
		}
		return s
	}

	// TS-NS-1: Verification.Checks fallback — "audit dead code" in checks
	// suppresses the stub-audit error even without subtask mentions.
	t.Run("verification_checks_fallback", func(t *testing.T) {
		spec := buildWiringSpecWithChecks(
			[]Subtask{
				{
					Id: "2.1", Title: "Run smoke tests", State: SubtaskStatePending,
					Details: []string{"Execute integration tests"}, RequirementRefs: []string{},
					TestSpecRefs: []string{"TS-04-SMOKE-1"},
				},
			},
			[]string{"audit dead code"},
		)
		result := spec.ValidateCrossFile()

		var stubErrors []ValidationEntry
		for _, e := range result.Errors {
			lower := strings.ToLower(e.Message)
			if e.Category == "integrity" && (strings.Contains(lower, "stub") || strings.Contains(lower, "dead")) {
				stubErrors = append(stubErrors, e)
			}
		}
		if len(stubErrors) != 0 {
			t.Errorf("expected 0 stub-audit errors when Verification.Checks contains 'audit dead code', got %d; errors: %v",
				len(stubErrors), stubErrors)
		}
	})

	// TS-NS-4: Verification.Checks with "dead_code" satisfies sub-check C.
	t.Run("verification_checks_dead_code_underscore", func(t *testing.T) {
		spec := buildWiringSpecWithChecks(
			[]Subtask{
				{
					Id: "2.1", Title: "Run smoke tests", State: SubtaskStatePending,
					Details: []string{"Execute integration tests"}, RequirementRefs: []string{},
					TestSpecRefs: []string{"TS-04-SMOKE-1"},
				},
			},
			[]string{"remove dead_code paths"},
		)
		result := spec.ValidateCrossFile()

		var stubErrors []ValidationEntry
		for _, e := range result.Errors {
			lower := strings.ToLower(e.Message)
			if e.Category == "integrity" && (strings.Contains(lower, "stub") || strings.Contains(lower, "dead")) {
				stubErrors = append(stubErrors, e)
			}
		}
		if len(stubErrors) != 0 {
			t.Errorf("expected 0 stub-audit errors when Verification.Checks contains 'dead_code', got %d; errors: %v",
				len(stubErrors), stubErrors)
		}
	})

	// TS-NS-4: Verification.Checks with "deadline" does NOT satisfy sub-check C.
	t.Run("verification_checks_deadline_does_not_match", func(t *testing.T) {
		spec := buildWiringSpecWithChecks(
			[]Subtask{
				{
					Id: "2.1", Title: "Run smoke tests", State: SubtaskStatePending,
					Details: []string{"Execute integration tests"}, RequirementRefs: []string{},
					TestSpecRefs: []string{"TS-04-SMOKE-1"},
				},
			},
			[]string{"remove deadline checks"},
		)
		result := spec.ValidateCrossFile()

		var stubErrors []ValidationEntry
		for _, e := range result.Errors {
			lower := strings.ToLower(e.Message)
			if e.Category == "integrity" && (strings.Contains(lower, "stub") || strings.Contains(lower, "dead")) {
				stubErrors = append(stubErrors, e)
			}
		}
		if len(stubErrors) < 1 {
			t.Errorf("expected at least 1 stub-audit error when Verification.Checks has 'deadline' (not 'dead code'), got %d; errors: %v",
				len(stubErrors), result.Errors)
		}
	})

	// TS-NS-3: Subtask title "Remove deadline entries" does NOT satisfy sub-check C.
	t.Run("dead_word_alone_does_not_match", func(t *testing.T) {
		spec := buildWiringSpec([]Subtask{
			{
				Id: "2.1", Title: "Run smoke tests", State: SubtaskStatePending,
				Details: []string{"detail"}, RequirementRefs: []string{},
				TestSpecRefs: []string{"TS-04-SMOKE-1"},
			},
			{
				Id: "2.2", Title: "Remove deadline entries", State: SubtaskStatePending,
				Details: []string{"Check deadline handling"}, RequirementRefs: []string{},
				TestSpecRefs: []string{"TS-04-SMOKE-2"},
			},
		})
		result := spec.ValidateCrossFile()

		var stubErrors []ValidationEntry
		for _, e := range result.Errors {
			lower := strings.ToLower(e.Message)
			if e.Category == "integrity" && (strings.Contains(lower, "stub") || strings.Contains(lower, "dead")) {
				stubErrors = append(stubErrors, e)
			}
		}
		if len(stubErrors) < 1 {
			t.Errorf("expected at least 1 stub-audit error when subtask title has 'deadline' (bare 'dead'), got %d; errors: %v",
				len(stubErrors), result.Errors)
		}
	})

	// TS-NS-3: Subtask title "Remove dead-code artifacts" DOES satisfy sub-check C.
	t.Run("dead_code_phrase_matches", func(t *testing.T) {
		spec := buildWiringSpec([]Subtask{
			{
				Id: "2.1", Title: "Run smoke tests", State: SubtaskStatePending,
				Details: []string{"detail"}, RequirementRefs: []string{},
				TestSpecRefs: []string{"TS-04-SMOKE-1"},
			},
			{
				Id: "2.2", Title: "Remove dead-code artifacts", State: SubtaskStatePending,
				Details: []string{"Clean up"}, RequirementRefs: []string{},
				TestSpecRefs: []string{"TS-04-SMOKE-2"},
			},
		})
		result := spec.ValidateCrossFile()

		var stubErrors []ValidationEntry
		for _, e := range result.Errors {
			lower := strings.ToLower(e.Message)
			if e.Category == "integrity" && (strings.Contains(lower, "stub") || strings.Contains(lower, "dead")) {
				stubErrors = append(stubErrors, e)
			}
		}
		if len(stubErrors) != 0 {
			t.Errorf("expected 0 stub-audit errors when subtask title has 'dead-code', got %d; errors: %v",
				len(stubErrors), stubErrors)
		}
	})
}

// ---------------------------------------------------------------------------
// Subtask 2.4: Cross-spec duplicate API symbol rule (04-REQ-10)
// Test Spec: TS-04-15
// ---------------------------------------------------------------------------

// TestValidateCrossSpec_DuplicateAPISymbol verifies that ValidateCrossSpec
// produces one integrity error per mismatched API symbol signature between
// two specs connected by a DependencyEdge.
// Test Spec: TS-04-15. Requirements: 04-REQ-10.1, 04-REQ-10.E1, 04-REQ-10.E2.
func TestValidateCrossSpec_DuplicateAPISymbol(t *testing.T) {
	tests := []struct {
		name           string
		specs          []*Spec
		graph          *DependencyGraph
		wantErrorCount int
		wantSymbol     string // symbol name expected in error Messages
	}{
		{
			name: "mismatched_signature_produces_error",
			specs: func() []*Spec {
				a := buildSpecA()
				a.Requirements.ExternalApis = []ExternalApi{
					{Package: "pkg", Version: "v1.0", Symbols: []ExternalApiSymbol{
						{Name: "Foo", ImportPath: "pkg/", Signature: "func Foo() int"},
					}},
				}
				b := buildSpecB()
				b.Requirements.ExternalApis = []ExternalApi{
					{Package: "pkg", Version: "v1.0", Symbols: []ExternalApiSymbol{
						{Name: "Foo", ImportPath: "pkg/", Signature: "func Foo() string"},
					}},
				}
				return []*Spec{a, b}
			}(),
			graph: &DependencyGraph{Edges: []DependencyEdge{
				{FromSpec: "01", ToSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
			}},
			wantErrorCount: 1,
			wantSymbol:     "Foo",
		},
		{
			name: "matching_signature_no_error",
			specs: func() []*Spec {
				a := buildSpecA()
				a.Requirements.ExternalApis = []ExternalApi{
					{Package: "pkg", Version: "v1.0", Symbols: []ExternalApiSymbol{
						{Name: "Foo", ImportPath: "pkg/", Signature: "func Foo() int"},
					}},
				}
				b := buildSpecB()
				b.Requirements.ExternalApis = []ExternalApi{
					{Package: "pkg", Version: "v1.0", Symbols: []ExternalApiSymbol{
						{Name: "Foo", ImportPath: "pkg/", Signature: "func Foo() int"},
					}},
				}
				return []*Spec{a, b}
			}(),
			graph: &DependencyGraph{Edges: []DependencyEdge{
				{FromSpec: "01", ToSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
			}},
			wantErrorCount: 0,
		},
		{
			name: "same_name_not_connected_no_error",
			specs: func() []*Spec {
				a := buildSpecA()
				a.Requirements.ExternalApis = []ExternalApi{
					{Package: "pkg", Version: "v1.0", Symbols: []ExternalApiSymbol{
						{Name: "Foo", ImportPath: "pkg/", Signature: "func Foo() int"},
					}},
				}
				b := buildSpecB()
				b.Requirements.ExternalApis = []ExternalApi{
					{Package: "pkg", Version: "v1.0", Symbols: []ExternalApiSymbol{
						{Name: "Foo", ImportPath: "pkg/", Signature: "func Foo() string"},
					}},
				}
				return []*Spec{a, b}
			}(),
			graph:          &DependencyGraph{Edges: []DependencyEdge{}}, // no connection
			wantErrorCount: 0,
		},
		{
			name: "no_shared_symbols_no_error",
			specs: func() []*Spec {
				a := buildSpecA()
				a.Requirements.ExternalApis = []ExternalApi{
					{Package: "pkg", Version: "v1.0", Symbols: []ExternalApiSymbol{
						{Name: "Foo", ImportPath: "pkg/", Signature: "func Foo() int"},
					}},
				}
				b := buildSpecB()
				b.Requirements.ExternalApis = []ExternalApi{
					{Package: "pkg", Version: "v1.0", Symbols: []ExternalApiSymbol{
						{Name: "Bar", ImportPath: "pkg/", Signature: "func Bar() int"},
					}},
				}
				return []*Spec{a, b}
			}(),
			graph: &DependencyGraph{Edges: []DependencyEdge{
				{FromSpec: "01", ToSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
			}},
			wantErrorCount: 0,
		},
		{
			name:           "empty_dependency_graph_no_error",
			specs:          []*Spec{buildSpecA(), buildSpecB()},
			graph:          &DependencyGraph{Edges: []DependencyEdge{}},
			wantErrorCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateCrossSpec(tt.specs, tt.graph)
			errors := filterByCheck(result.Errors, "cross_spec_1")

			if len(errors) != tt.wantErrorCount {
				t.Errorf("cross_spec_1 error count = %d, want %d; errors: %v",
					len(errors), tt.wantErrorCount, errors)
			}

			for _, e := range errors {
				if e.Category != "integrity" {
					t.Errorf("cross_spec_1 error category = %q, want %q", e.Category, "integrity")
				}
			}

			if tt.wantSymbol != "" {
				found := false
				for _, e := range errors {
					if strings.Contains(e.Message, tt.wantSymbol) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected symbol %q in error Message, not found", tt.wantSymbol)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Subtask 2.5: Cross-spec unknown dependency and interface contract rules
// (04-REQ-11, 04-REQ-12)
// Test Spec: TS-04-16, TS-04-17
// ---------------------------------------------------------------------------

// TestValidateCrossSpec_UnknownDependency verifies that ValidateCrossSpec
// produces one integrity error per depends_on_spec value that does not
// match a key in the provided spec map.
// Test Spec: TS-04-16. Requirements: 04-REQ-11.1, 04-REQ-11.E1.
func TestValidateCrossSpec_UnknownDependency(t *testing.T) {
	tests := []struct {
		name           string
		specs          []*Spec
		graph          *DependencyGraph
		wantErrorCount int
		wantUnknown    string // unknown spec_id expected in error Messages
	}{
		{
			name: "unknown_depends_on_spec_produces_error",
			specs: func() []*Spec {
				a := buildSpecA()
				a.Tasks.Dependencies = []TaskDependency{
					{DependsOnSpec: "specZ", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
				}
				return []*Spec{a, buildSpecB()}
			}(),
			graph:          &DependencyGraph{Edges: []DependencyEdge{}},
			wantErrorCount: 1,
			wantUnknown:    "specZ",
		},
		{
			name: "all_dependencies_resolve_no_error",
			specs: func() []*Spec {
				a := buildSpecA()
				a.Tasks.Dependencies = []TaskDependency{
					{DependsOnSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
				}
				return []*Spec{a, buildSpecB()}
			}(),
			graph:          &DependencyGraph{Edges: []DependencyEdge{}},
			wantErrorCount: 0,
		},
		{
			name:           "no_task_dependencies_no_error",
			specs:          []*Spec{buildSpecA(), buildSpecB()},
			graph:          &DependencyGraph{Edges: []DependencyEdge{}},
			wantErrorCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateCrossSpec(tt.specs, tt.graph)
			errors := filterByCheck(result.Errors, "cross_spec_3")

			if len(errors) != tt.wantErrorCount {
				t.Errorf("cross_spec_3 error count = %d, want %d; errors: %v",
					len(errors), tt.wantErrorCount, errors)
			}

			for _, e := range errors {
				if e.Category != "integrity" {
					t.Errorf("cross_spec_3 error category = %q, want %q", e.Category, "integrity")
				}
			}

			if tt.wantUnknown != "" {
				found := false
				for _, e := range errors {
					if strings.Contains(e.Message, tt.wantUnknown) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected unknown spec %q in error Message, not found", tt.wantUnknown)
				}
			}
		})
	}
}

// TestValidateCrossSpec_InterfaceContractMismatch verifies that
// ValidateCrossSpec produces one integrity error when a downstream spec's
// criterion references a backtick-wrapped symbol with a different
// return_contract than the upstream spec's definition.
// Test Spec: TS-04-17. Requirements: 04-REQ-12.1, 04-REQ-12.E1.
func TestValidateCrossSpec_InterfaceContractMismatch(t *testing.T) {
	tests := []struct {
		name           string
		specs          []*Spec
		graph          *DependencyGraph
		wantErrorCount int
		wantSymbol     string // symbol expected in error Messages
	}{
		{
			name: "different_return_contracts_produces_error",
			specs: func() []*Spec {
				a := buildSpecA()
				a.Requirements.Requirements = []Requirement{
					{
						Id: "01-REQ-1", Title: "Req 1",
						UserStory: UserStory{Role: "dev", Goal: "test", Benefit: "validation"},
						AcceptanceCriteria: []Criterion{
							{
								Id: "01-REQ-1.1", EarsPattern: CriterionEarsPatternUbiquitous,
								System: "the system", Action: "call `Foo`",
								ReturnContract: strPtr("returns int"),
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				b := buildSpecB()
				b.Requirements.Requirements = []Requirement{
					{
						Id: "02-REQ-1", Title: "Req 1",
						UserStory: UserStory{Role: "dev", Goal: "test", Benefit: "validation"},
						AcceptanceCriteria: []Criterion{
							{
								Id: "02-REQ-1.1", EarsPattern: CriterionEarsPatternUbiquitous,
								System: "the system", Action: "call `Foo`",
								ReturnContract: strPtr("returns string"),
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				return []*Spec{a, b}
			}(),
			graph: &DependencyGraph{Edges: []DependencyEdge{
				{FromSpec: "01", ToSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
			}},
			wantErrorCount: 1,
			wantSymbol:     "Foo",
		},
		{
			name: "matching_contracts_no_error",
			specs: func() []*Spec {
				a := buildSpecA()
				a.Requirements.Requirements = []Requirement{
					{
						Id: "01-REQ-1", Title: "Req 1",
						UserStory: UserStory{Role: "dev", Goal: "test", Benefit: "validation"},
						AcceptanceCriteria: []Criterion{
							{
								Id: "01-REQ-1.1", EarsPattern: CriterionEarsPatternUbiquitous,
								System: "the system", Action: "call `Foo`",
								ReturnContract: strPtr("returns int"),
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				b := buildSpecB()
				b.Requirements.Requirements = []Requirement{
					{
						Id: "02-REQ-1", Title: "Req 1",
						UserStory: UserStory{Role: "dev", Goal: "test", Benefit: "validation"},
						AcceptanceCriteria: []Criterion{
							{
								Id: "02-REQ-1.1", EarsPattern: CriterionEarsPatternUbiquitous,
								System: "the system", Action: "call `Foo`",
								ReturnContract: strPtr("returns int"),
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				return []*Spec{a, b}
			}(),
			graph: &DependencyGraph{Edges: []DependencyEdge{
				{FromSpec: "01", ToSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
			}},
			wantErrorCount: 0,
		},
		{
			name: "both_null_contracts_no_error",
			specs: func() []*Spec {
				a := buildSpecA()
				a.Requirements.Requirements = []Requirement{
					{
						Id: "01-REQ-1", Title: "Req 1",
						UserStory: UserStory{Role: "dev", Goal: "test", Benefit: "validation"},
						AcceptanceCriteria: []Criterion{
							{
								Id: "01-REQ-1.1", EarsPattern: CriterionEarsPatternUbiquitous,
								System: "the system", Action: "call `Foo`",
								ReturnContract: nil,
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				b := buildSpecB()
				b.Requirements.Requirements = []Requirement{
					{
						Id: "02-REQ-1", Title: "Req 1",
						UserStory: UserStory{Role: "dev", Goal: "test", Benefit: "validation"},
						AcceptanceCriteria: []Criterion{
							{
								Id: "02-REQ-1.1", EarsPattern: CriterionEarsPatternUbiquitous,
								System: "the system", Action: "call `Foo`",
								ReturnContract: nil,
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				return []*Spec{a, b}
			}(),
			graph: &DependencyGraph{Edges: []DependencyEdge{
				{FromSpec: "01", ToSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
			}},
			wantErrorCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateCrossSpec(tt.specs, tt.graph)
			errors := filterByCheck(result.Errors, "cross_spec_4")

			if len(errors) != tt.wantErrorCount {
				t.Errorf("cross_spec_4 error count = %d, want %d; errors: %v",
					len(errors), tt.wantErrorCount, errors)
			}

			for _, e := range errors {
				if e.Category != "integrity" {
					t.Errorf("cross_spec_4 error category = %q, want %q", e.Category, "integrity")
				}
			}

			if tt.wantSymbol != "" {
				found := false
				for _, e := range errors {
					if strings.Contains(e.Message, tt.wantSymbol) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected symbol %q in error Message, not found", tt.wantSymbol)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Subtask 2.6: Cross-spec-5 actor reference existence (Python semantics)
// ---------------------------------------------------------------------------

// TestValidateCrossSpec_MissingActorReference verifies that ValidateCrossSpec
// produces a cross_spec_5 error when the downstream spec has no execution
// path step referencing any actor from the upstream spec's execution paths,
// and no error when such a reference exists (case-insensitive).
func TestValidateCrossSpec_MissingActorReference(t *testing.T) {
	tests := []struct {
		name           string
		specs          []*Spec
		graph          *DependencyGraph
		wantErrorCount int
		wantInMessage  []string // strings expected in error Messages
	}{
		{
			name: "downstream_has_no_upstream_actor_reference",
			specs: func() []*Spec {
				a := buildSpecA()
				a.Requirements.ExecutionPaths = []ExecutionPath{
					{
						Id: "01-PATH-1", Title: "Path X",
						Steps: []PathStep{
							{Actor: "svc-a", Action: "do something"},
							{Actor: "svc-a", Action: "do more"},
						},
					},
				}
				b := buildSpecB()
				b.Requirements.ExecutionPaths = []ExecutionPath{
					{
						Id: "02-PATH-1", Title: "Path Y",
						Steps: []PathStep{
							{Actor: "svc-b", Action: "independent work"},
							{Actor: "svc-b", Action: "more work"},
						},
					},
				}
				return []*Spec{a, b}
			}(),
			graph: &DependencyGraph{Edges: []DependencyEdge{
				{FromSpec: "01", ToSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
			}},
			wantErrorCount: 1,
			wantInMessage:  []string{"02", "01"},
		},
		{
			name: "downstream_references_upstream_actor_no_error",
			specs: func() []*Spec {
				a := buildSpecA()
				a.Requirements.ExecutionPaths = []ExecutionPath{
					{
						Id: "01-PATH-1", Title: "Path X",
						Steps: []PathStep{
							{Actor: "svc-a", Action: "do something"},
							{Actor: "svc-a", Action: "do more"},
						},
					},
				}
				b := buildSpecB()
				b.Requirements.ExecutionPaths = []ExecutionPath{
					{
						Id: "02-PATH-1", Title: "Path Y",
						Steps: []PathStep{
							{Actor: "svc-b", Action: "independent work"},
							{Actor: "svc-a", Action: "calls upstream"},
						},
					},
				}
				return []*Spec{a, b}
			}(),
			graph: &DependencyGraph{Edges: []DependencyEdge{
				{FromSpec: "01", ToSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
			}},
			wantErrorCount: 0,
		},
		{
			name: "case_insensitive_actor_match_no_error",
			specs: func() []*Spec {
				a := buildSpecA()
				a.Requirements.ExecutionPaths = []ExecutionPath{
					{
						Id: "01-PATH-1", Title: "Path X",
						Steps: []PathStep{
							{Actor: "SVC-A", Action: "do something"},
							{Actor: "SVC-A", Action: "do more"},
						},
					},
				}
				b := buildSpecB()
				b.Requirements.ExecutionPaths = []ExecutionPath{
					{
						Id: "02-PATH-1", Title: "Path Y",
						Steps: []PathStep{
							{Actor: "svc-b", Action: "work"},
							{Actor: "svc-a", Action: "calls upstream (case differs)"},
						},
					},
				}
				return []*Spec{a, b}
			}(),
			graph: &DependencyGraph{Edges: []DependencyEdge{
				{FromSpec: "01", ToSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
			}},
			wantErrorCount: 0,
		},
		{
			name: "upstream_has_no_execution_paths_no_error",
			specs: func() []*Spec {
				a := buildSpecA()
				// No execution paths for upstream
				b := buildSpecB()
				b.Requirements.ExecutionPaths = []ExecutionPath{
					{
						Id: "02-PATH-1", Title: "Path Y",
						Steps: []PathStep{
							{Actor: "svc-b", Action: "work"},
							{Actor: "svc-b", Action: "more work"},
						},
					},
				}
				return []*Spec{a, b}
			}(),
			graph: &DependencyGraph{Edges: []DependencyEdge{
				{FromSpec: "01", ToSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
			}},
			wantErrorCount: 0,
		},
		{
			name: "no_execution_paths_at_all_no_error",
			specs: func() []*Spec {
				a := buildSpecA()
				b := buildSpecB()
				return []*Spec{a, b}
			}(),
			graph: &DependencyGraph{Edges: []DependencyEdge{
				{FromSpec: "01", ToSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
			}},
			wantErrorCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateCrossSpec(tt.specs, tt.graph)
			errors := filterByCheck(result.Errors, "cross_spec_5")

			if len(errors) != tt.wantErrorCount {
				t.Errorf("cross_spec_5 error count = %d, want %d; errors: %v",
					len(errors), tt.wantErrorCount, errors)
			}

			for _, e := range errors {
				if e.Category != "integrity" {
					t.Errorf("cross_spec_5 error category = %q, want %q", e.Category, "integrity")
				}
			}

			for _, want := range tt.wantInMessage {
				found := false
				for _, e := range errors {
					if strings.Contains(e.Message, want) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected %q in error Message, not found", want)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Issue #14: Glossary conflict scope — all spec pairs, not just edges
// Test Spec: TS-NS-1. Requirements: NS-REQ-1.
// ---------------------------------------------------------------------------

// TestValidateCrossSpec_GlossaryConflictNoEdge verifies that glossary
// conflict detection fires for spec pairs with no dependency edge.
func TestValidateCrossSpec_GlossaryConflictNoEdge(t *testing.T) {
	// Three specs: A (01), B (02), C (03).
	// A and C define 'widget' differently but share NO dependency edge.
	// B is connected to A by an edge.
	specA := buildSpecA() // has glossary "widget": "A UI component."
	specB := buildSpecB()
	specC := &Spec{
		SpecID:        "03",
		SpecName:      "spec_c",
		Title:         "Spec C",
		Status:        "active",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test-author",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Spec C\n",
		Requirements: &RequirementsV1Json{
			SpecId:                "03",
			SpecName:              "spec_c",
			SchemaVersion:         1,
			Introduction:          "Spec C.",
			Glossary:              RequirementsV1JsonGlossary{"widget": "A physical knob."},
			Requirements:          []Requirement{},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "03",
			SpecName:      "spec_c",
			SchemaVersion: 1,
			TestCases:     []TestCase{},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SpecId:        "03",
			SpecName:      "spec_c",
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

	// Only edge: A→B. No edge between A and C.
	graph := &DependencyGraph{
		Edges: []DependencyEdge{
			{FromSpec: "01", ToSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
		},
	}

	result := ValidateCrossSpec([]*Spec{specA, specB, specC}, graph)
	glossaryErrors := filterByCheck(result.Errors, "glossary_conflict")

	if len(glossaryErrors) < 1 {
		t.Fatalf("expected at least 1 glossary_conflict error, got %d", len(glossaryErrors))
	}

	// Verify that the A/C conflict is detected (both spec IDs mentioned).
	found := false
	for _, e := range glossaryErrors {
		if strings.Contains(e.Message, "01") && strings.Contains(e.Message, "03") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a glossary_conflict error mentioning spec 01 and spec 03; got: %v", glossaryErrors)
	}
}

// ---------------------------------------------------------------------------
// Issue #14: cross-spec-4 set-based contract comparison
// Test Spec: TS-NS-4. Requirements: NS-REQ-4.
// ---------------------------------------------------------------------------

// TestValidateCrossSpec_ContractSetOverlap verifies that cross-spec-4 treats
// return_contract values as sets and errors only when sets are disjoint.
func TestValidateCrossSpec_ContractSetOverlap(t *testing.T) {
	tests := []struct {
		name           string
		specs          []*Spec
		graph          *DependencyGraph
		wantErrorCount int
	}{
		{
			name: "overlapping_contract_sets_no_error",
			specs: func() []*Spec {
				// Upstream has two criteria for Foo with contracts "int" and "error"
				a := buildSpecA()
				a.Requirements.Requirements = []Requirement{
					{
						Id: "01-REQ-1", Title: "Req 1",
						UserStory: UserStory{Role: "dev", Goal: "test", Benefit: "validation"},
						AcceptanceCriteria: []Criterion{
							{
								Id: "01-REQ-1.1", EarsPattern: CriterionEarsPatternUbiquitous,
								System: "the system", Action: "call `Foo`",
								ReturnContract: strPtr("int"),
							},
							{
								Id: "01-REQ-1.2", EarsPattern: CriterionEarsPatternUbiquitous,
								System: "the system", Action: "call `Foo` on error",
								ReturnContract: strPtr("error"),
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				// Downstream has one criterion for Foo with contract "int"
				b := buildSpecB()
				b.Requirements.Requirements = []Requirement{
					{
						Id: "02-REQ-1", Title: "Req 1",
						UserStory: UserStory{Role: "dev", Goal: "test", Benefit: "validation"},
						AcceptanceCriteria: []Criterion{
							{
								Id: "02-REQ-1.1", EarsPattern: CriterionEarsPatternUbiquitous,
								System: "the system", Action: "call `Foo`",
								ReturnContract: strPtr("int"),
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				return []*Spec{a, b}
			}(),
			graph: &DependencyGraph{Edges: []DependencyEdge{
				{FromSpec: "01", ToSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
			}},
			wantErrorCount: 0, // sets {"int","error"} and {"int"} overlap on "int"
		},
		{
			name: "disjoint_contract_sets_produces_error",
			specs: func() []*Spec {
				a := buildSpecA()
				a.Requirements.Requirements = []Requirement{
					{
						Id: "01-REQ-1", Title: "Req 1",
						UserStory: UserStory{Role: "dev", Goal: "test", Benefit: "validation"},
						AcceptanceCriteria: []Criterion{
							{
								Id: "01-REQ-1.1", EarsPattern: CriterionEarsPatternUbiquitous,
								System: "the system", Action: "call `Foo`",
								ReturnContract: strPtr("int"),
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				b := buildSpecB()
				b.Requirements.Requirements = []Requirement{
					{
						Id: "02-REQ-1", Title: "Req 1",
						UserStory: UserStory{Role: "dev", Goal: "test", Benefit: "validation"},
						AcceptanceCriteria: []Criterion{
							{
								Id: "02-REQ-1.1", EarsPattern: CriterionEarsPatternUbiquitous,
								System: "the system", Action: "call `Foo`",
								ReturnContract: strPtr("string"),
							},
						},
						EdgeCases: []Criterion{},
					},
				}
				return []*Spec{a, b}
			}(),
			graph: &DependencyGraph{Edges: []DependencyEdge{
				{FromSpec: "01", ToSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
			}},
			wantErrorCount: 1, // sets {"int"} and {"string"} are disjoint
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateCrossSpec(tt.specs, tt.graph)
			errors := filterByCheck(result.Errors, "cross_spec_4")

			if len(errors) != tt.wantErrorCount {
				t.Errorf("cross_spec_4 error count = %d, want %d; errors: %v",
					len(errors), tt.wantErrorCount, errors)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Warning rule tests (task group 8 / test spec TS-04-19 through TS-04-25)
// ---------------------------------------------------------------------------

// makeMinimalSpec creates a minimal Spec with matching IDs across all artifacts.
func makeMinimalSpec() *Spec {
	return &Spec{
		SpecID:        "04",
		SpecName:      "test_warnings",
		Title:         "Test Warnings",
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
			SpecId:                "04",
			SpecName:              "test_warnings",
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
			SpecId:        "04",
			SpecName:      "test_warnings",
			SchemaVersion: 1,
			TestCases:     []TestCase{},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			Schema:        "https://agent-fox.dev/schemas/tasks.v1.json",
			SpecId:        "04",
			SpecName:      "test_warnings",
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
}

// TS-04-19: Validate appends a warning when a task group's total
// test_spec_refs count across all subtasks exceeds 15.
// Requirement: 04-REQ-14.1
func TestValidate_Warning_GroupTestSpecRefsCeiling(t *testing.T) {
	defer requireImplemented(t)
	spec := makeMinimalSpec()
	spec.Tasks.TaskGroups = []TaskGroup{
		{
			Id: 1, Title: "Group 1", Kind: TaskGroupKindTests,
			Subtasks: []Subtask{
				{Id: "1.1", Title: "s1", State: SubtaskStatePending, Details: []string{},
					TestSpecRefs: []string{"a", "b", "c", "d", "e", "f"}, RequirementRefs: []string{}},
				{Id: "1.2", Title: "s2", State: SubtaskStatePending, Details: []string{},
					TestSpecRefs: []string{"g", "h", "i", "j", "k", "l"}, RequirementRefs: []string{}},
				{Id: "1.3", Title: "s3", State: SubtaskStatePending, Details: []string{},
					TestSpecRefs: []string{"m", "n", "o", "p", "q"}, RequirementRefs: []string{}},
			},
			Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
		},
	}
	result := spec.Validate()
	var found bool
	for _, w := range result.Warnings {
		lower := strings.ToLower(w.Message)
		if strings.Contains(w.Message, "17") || strings.Contains(lower, "test_spec_refs") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning about group test_spec_refs count 17, got warnings: %v", result.Warnings)
	}
}

// 04-REQ-14.E1: Exactly 15 total test_spec_refs produces no warning.
func TestValidate_Warning_GroupTestSpecRefsCeiling_ExactlyAtThreshold(t *testing.T) {
	defer requireImplemented(t)
	spec := makeMinimalSpec()
	spec.Tasks.TaskGroups = []TaskGroup{
		{
			Id: 1, Title: "Group 1", Kind: TaskGroupKindTests,
			Subtasks: []Subtask{
				{Id: "1.1", Title: "s1", State: SubtaskStatePending, Details: []string{},
					TestSpecRefs: []string{"a", "b", "c", "d", "e"}, RequirementRefs: []string{}},
				{Id: "1.2", Title: "s2", State: SubtaskStatePending, Details: []string{},
					TestSpecRefs: []string{"f", "g", "h", "i", "j"}, RequirementRefs: []string{}},
				{Id: "1.3", Title: "s3", State: SubtaskStatePending, Details: []string{},
					TestSpecRefs: []string{"k", "l", "m", "n", "o"}, RequirementRefs: []string{}},
			},
			Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
		},
	}
	result := spec.Validate()
	for _, w := range result.Warnings {
		lower := strings.ToLower(w.Message)
		if (strings.Contains(w.Message, "15") || strings.Contains(lower, "test_spec_refs")) &&
			strings.Contains(lower, "exceed") {
			t.Errorf("expected no group test_spec_refs ceiling warning at exactly 15, got: %s", w.Message)
		}
	}
}

// TS-04-20: Validate appends a warning when a task group has more than 6
// non-verification subtasks.
// Requirement: 04-REQ-15.1
func TestValidate_Warning_GroupSubtaskCount(t *testing.T) {
	defer requireImplemented(t)
	spec := makeMinimalSpec()
	subtasks := make([]Subtask, 7)
	for i := range subtasks {
		subtasks[i] = Subtask{
			Id: fmt.Sprintf("1.%d", i+1), Title: fmt.Sprintf("s%d", i+1),
			State: SubtaskStatePending, Details: []string{},
			TestSpecRefs: []string{}, RequirementRefs: []string{},
		}
	}
	spec.Tasks.TaskGroups = []TaskGroup{
		{
			Id: 1, Title: "Group 1", Kind: TaskGroupKindTests,
			Subtasks:     subtasks,
			Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
		},
	}
	result := spec.Validate()
	var found bool
	for _, w := range result.Warnings {
		lower := strings.ToLower(w.Message)
		if strings.Contains(w.Message, "7") || strings.Contains(lower, "subtask") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning about group subtask count exceeding 6, got warnings: %v", result.Warnings)
	}
}

// 04-REQ-15.E1: Exactly 6 non-verification subtasks produces no warning.
func TestValidate_Warning_GroupSubtaskCount_ExactlyAtThreshold(t *testing.T) {
	defer requireImplemented(t)
	spec := makeMinimalSpec()
	subtasks := make([]Subtask, 6)
	for i := range subtasks {
		subtasks[i] = Subtask{
			Id: fmt.Sprintf("1.%d", i+1), Title: fmt.Sprintf("s%d", i+1),
			State: SubtaskStatePending, Details: []string{},
			TestSpecRefs: []string{}, RequirementRefs: []string{},
		}
	}
	spec.Tasks.TaskGroups = []TaskGroup{
		{
			Id: 1, Title: "Group 1", Kind: TaskGroupKindTests,
			Subtasks:     subtasks,
			Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
		},
	}
	result := spec.Validate()
	for _, w := range result.Warnings {
		lower := strings.ToLower(w.Message)
		if strings.Contains(lower, "subtask") && strings.Contains(lower, "exceed") {
			t.Errorf("expected no subtask count warning at exactly 6, got: %s", w.Message)
		}
	}
}

// TS-04-21: Validate appends a warning when a single subtask has more than 8
// test_spec_refs entries.
// Requirement: 04-REQ-16.1
func TestValidate_Warning_SubtaskTestSpecRefsCeiling(t *testing.T) {
	defer requireImplemented(t)
	spec := makeMinimalSpec()
	spec.Tasks.TaskGroups = []TaskGroup{
		{
			Id: 1, Title: "Group 1", Kind: TaskGroupKindTests,
			Subtasks: []Subtask{
				{Id: "1.1", Title: "big subtask", State: SubtaskStatePending, Details: []string{},
					TestSpecRefs: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}, RequirementRefs: []string{}},
			},
			Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
		},
	}
	result := spec.Validate()
	var found bool
	for _, w := range result.Warnings {
		lower := strings.ToLower(w.Message)
		if strings.Contains(w.Message, "9") || strings.Contains(lower, "test_spec_refs") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning about subtask test_spec_refs count 9, got warnings: %v", result.Warnings)
	}
}

// 04-REQ-16.E1: Exactly 8 test_spec_refs produces no warning.
func TestValidate_Warning_SubtaskTestSpecRefsCeiling_ExactlyAtThreshold(t *testing.T) {
	defer requireImplemented(t)
	spec := makeMinimalSpec()
	spec.Tasks.TaskGroups = []TaskGroup{
		{
			Id: 1, Title: "Group 1", Kind: TaskGroupKindTests,
			Subtasks: []Subtask{
				{Id: "1.1", Title: "subtask", State: SubtaskStatePending, Details: []string{},
					TestSpecRefs: []string{"a", "b", "c", "d", "e", "f", "g", "h"}, RequirementRefs: []string{}},
			},
			Verification: VerificationSubtask{Id: "1.V", Checks: []string{"check"}},
		},
	}
	result := spec.Validate()
	for _, w := range result.Warnings {
		lower := strings.ToLower(w.Message)
		if strings.Contains(lower, "test_spec_refs") && strings.Contains(lower, "exceed") && w.EntityID == "1.1" {
			t.Errorf("expected no subtask test_spec_refs warning at exactly 8, got: %s", w.Message)
		}
	}
}

// TS-04-22: Validate appends a warning when a criterion has error_condition
// or error-indicating action keyword and null return_contract.
// Requirement: 04-REQ-17.1
func TestValidate_Warning_ErrorPathReturnContract(t *testing.T) {
	defer requireImplemented(t)
	spec := makeMinimalSpec()
	spec.Requirements.Requirements = []Requirement{
		{
			Id: "04-REQ-1", Title: "Error Handling",
			UserStory: UserStory{Role: "developer", Goal: "handle errors", Benefit: "reliability"},
			AcceptanceCriteria: []Criterion{{
				Id: "04-REQ-1.1", EarsPattern: CriterionEarsPatternEventDriven, System: "the system",
				Action: "handle the error", ErrorCondition: strPtr("network timeout"), ReturnContract: nil,
			}},
			EdgeCases: []Criterion{},
		},
	}
	result := spec.Validate()
	var found bool
	for _, w := range result.Warnings {
		lower := strings.ToLower(w.Message)
		if strings.Contains(w.Message, "04-REQ-1.1") || strings.Contains(lower, "return_contract") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning about missing return_contract on error path, got warnings: %v", result.Warnings)
	}
}

// TS-04-22 variant: error-indicating keyword in action triggers warning.
func TestValidate_Warning_ErrorPathReturnContract_ActionKeyword(t *testing.T) {
	defer requireImplemented(t)
	spec := makeMinimalSpec()
	spec.Requirements.Requirements = []Requirement{
		{
			Id: "04-REQ-1", Title: "Error keyword",
			UserStory: UserStory{Role: "developer", Goal: "handle errors", Benefit: "reliability"},
			AcceptanceCriteria: []Criterion{{
				Id: "04-REQ-1.1", EarsPattern: CriterionEarsPatternEventDriven, System: "the system",
				Action: "reject the invalid input", ReturnContract: nil,
			}},
			EdgeCases: []Criterion{},
		},
	}
	result := spec.Validate()
	var found bool
	for _, w := range result.Warnings {
		lower := strings.ToLower(w.Message)
		if strings.Contains(w.Message, "04-REQ-1.1") || strings.Contains(lower, "return_contract") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning for error keyword in action, got warnings: %v", result.Warnings)
	}
}

// 04-REQ-17: No warning when return_contract is non-null.
func TestValidate_Warning_ErrorPathReturnContract_NonNull(t *testing.T) {
	defer requireImplemented(t)
	spec := makeMinimalSpec()
	spec.Requirements.Requirements = []Requirement{
		{
			Id: "04-REQ-1", Title: "Error With Contract",
			UserStory: UserStory{Role: "developer", Goal: "handle errors", Benefit: "reliability"},
			AcceptanceCriteria: []Criterion{{
				Id: "04-REQ-1.1", EarsPattern: CriterionEarsPatternEventDriven, System: "the system",
				Action: "handle the error", ErrorCondition: strPtr("network timeout"),
				ReturnContract: CriterionReturnContract(strPtr("returns HTTP 503")),
			}},
			EdgeCases: []Criterion{},
		},
	}
	result := spec.Validate()
	for _, w := range result.Warnings {
		if w.EntityID == "04-REQ-1.1" && strings.Contains(strings.ToLower(w.Message), "return_contract") {
			t.Errorf("expected no error path warning when contract is set, got: %s", w.Message)
		}
	}
}

// 04-REQ-17.E1: Unwanted pattern with null return_contract produces
// both an integrity error (cross_file_10) and a warning.
func TestValidate_Warning_ErrorPathReturnContract_UnwantedDualEntry(t *testing.T) {
	defer requireImplemented(t)
	spec := makeMinimalSpec()
	spec.Requirements.Requirements = []Requirement{
		{
			Id: "04-REQ-1", Title: "Unwanted Error Path",
			UserStory: UserStory{Role: "developer", Goal: "handle errors", Benefit: "reliability"},
			AcceptanceCriteria: []Criterion{{
				Id: "04-REQ-1.1", EarsPattern: CriterionEarsPatternUnwanted, System: "the system",
				Action: "fail with error", ErrorCondition: strPtr("unexpected input"), ReturnContract: nil,
			}},
			EdgeCases: []Criterion{},
		},
	}
	result := spec.Validate()
	var hasError bool
	for _, e := range result.Errors {
		if e.Check == "cross_file_10" && e.EntityID == "04-REQ-1.1" {
			hasError = true
			break
		}
	}
	if !hasError {
		t.Error("expected integrity error from cross_file_10 for unwanted criterion")
	}
	var hasWarning bool
	for _, w := range result.Warnings {
		if w.EntityID == "04-REQ-1.1" && strings.Contains(strings.ToLower(w.Message), "return_contract") {
			hasWarning = true
			break
		}
	}
	if !hasWarning {
		t.Error("expected warning about missing return_contract on error path")
	}
}

// TS-04-23: Validate appends one warning per vague word occurrence.
// Requirement: 04-REQ-18.1
func TestValidate_Warning_VagueLanguageDetection(t *testing.T) {
	defer requireImplemented(t)
	spec := makeMinimalSpec()
	spec.Requirements.Requirements = []Requirement{
		{
			Id: "04-REQ-1", Title: "Vague Criterion",
			UserStory: UserStory{Role: "developer", Goal: "detect vagueness", Benefit: "clarity"},
			AcceptanceCriteria: []Criterion{{
				Id: "04-REQ-1.1", EarsPattern: CriterionEarsPatternUbiquitous, System: "the system",
				Action: "handle appropriate cases", Trigger: strPtr("properly configured"), ReturnContract: nil,
			}},
			EdgeCases: []Criterion{},
		},
	}
	result := spec.Validate()
	var vagueWarnings []ValidationEntry
	for _, w := range result.Warnings {
		lower := strings.ToLower(w.Message)
		if strings.Contains(lower, "appropriate") || strings.Contains(lower, "properly") {
			vagueWarnings = append(vagueWarnings, w)
		}
	}
	if len(vagueWarnings) != 2 {
		t.Errorf("expected 2 vague warnings (appropriate + properly), got %d: %v", len(vagueWarnings), vagueWarnings)
	}
}

// 04-REQ-18.E1: One warning per occurrence across multiple fields/requirements.
func TestValidate_Warning_VagueLanguageDetection_MultipleOccurrences(t *testing.T) {
	defer requireImplemented(t)
	spec := makeMinimalSpec()
	spec.Requirements.Requirements = []Requirement{
		{
			Id: "04-REQ-1", Title: "First",
			UserStory: UserStory{Role: "dev", Goal: "detect", Benefit: "clarity"},
			AcceptanceCriteria: []Criterion{{
				Id: "04-REQ-1.1", EarsPattern: CriterionEarsPatternUbiquitous, System: "sys",
				Action: "handle correctly", ReturnContract: nil,
			}},
			EdgeCases: []Criterion{},
		},
		{
			Id: "04-REQ-2", Title: "Second",
			UserStory: UserStory{Role: "dev", Goal: "detect", Benefit: "clarity"},
			AcceptanceCriteria: []Criterion{{
				Id: "04-REQ-2.1", EarsPattern: CriterionEarsPatternUbiquitous, System: "sys",
				Action: "provide adequate response with reasonable detail", ReturnContract: nil,
			}},
			EdgeCases: []Criterion{},
		},
	}
	result := spec.Validate()
	var vagueWarnings []ValidationEntry
	for _, w := range result.Warnings {
		lower := strings.ToLower(w.Message)
		if strings.Contains(lower, "correctly") || strings.Contains(lower, "adequate") || strings.Contains(lower, "reasonable") {
			vagueWarnings = append(vagueWarnings, w)
		}
	}
	if len(vagueWarnings) != 3 {
		t.Errorf("expected 3 vague warnings, got %d: %v", len(vagueWarnings), vagueWarnings)
	}
}

// No vague warnings for clean language.
func TestValidate_Warning_VagueLanguageDetection_NoVagueTerms(t *testing.T) {
	defer requireImplemented(t)
	spec := makeMinimalSpec()
	spec.Requirements.Requirements = []Requirement{
		{
			Id: "04-REQ-1", Title: "Clean",
			UserStory: UserStory{Role: "dev", Goal: "clear specs", Benefit: "clarity"},
			AcceptanceCriteria: []Criterion{{
				Id: "04-REQ-1.1", EarsPattern: CriterionEarsPatternUbiquitous, System: "sys",
				Action: "return a 200 status code", ReturnContract: nil,
			}},
			EdgeCases: []Criterion{},
		},
	}
	result := spec.Validate()
	for _, w := range result.Warnings {
		if strings.Contains(strings.ToLower(w.Message), "vague") {
			t.Errorf("expected no vague warnings, got: %s", w.Message)
		}
	}
}

// TS-04-24: Vague language regex is compiled at package level.
// Requirement: 04-REQ-18.2
func TestValidate_Warning_VagueLanguageRegexPackageLevel(t *testing.T) {
	if vagueLanguageRe == nil {
		t.Fatal("vagueLanguageRe is nil; expected package-level compiled regex")
	}
	// All Python-aligned vague terms must match.
	for _, term := range []string{"appropriate", "properly", "correctly", "reasonable", "relevant", "adequate", "suitable", "as needed", "if necessary", "etc"} {
		if !vagueLanguageRe.MatchString(term) {
			t.Errorf("vagueLanguageRe should match %q", term)
		}
	}
	// Go-only adverb forms that were removed, plus non-vague terms, must not match.
	for _, term := range []string{"appropriately", "adequately", "sufficiently", "return", "validate", "check"} {
		if vagueLanguageRe.MatchString(term) {
			t.Errorf("vagueLanguageRe should not match %q", term)
		}
	}
}

// TS-13-1: errorKeywordRe matches all Python-equivalent error-path terms.
func TestErrorKeywordRe_PythonAligned(t *testing.T) {
	mustMatch := []string{
		"error", "fail", "reject", "denied", "deny",
		"invalid", "unauthorized", "unauthorised",
		"forbidden", "timeout", "not found",
		// Case-insensitive checks.
		"access denied", "Unauthorized", "Forbidden", "Not Found",
	}
	for _, term := range mustMatch {
		if !errorKeywordRe.MatchString(term) {
			t.Errorf("errorKeywordRe should match %q", term)
		}
	}
	mustNotMatch := []string{"success", "ok", "valid"}
	for _, term := range mustNotMatch {
		if errorKeywordRe.MatchString(term) {
			t.Errorf("errorKeywordRe should not match %q", term)
		}
	}
}

// TS-13-2: Vague language check scans error_handling behavior field.
func TestValidate_Warning_VagueLanguageInErrorHandlingBehavior(t *testing.T) {
	defer requireImplemented(t)
	spec := makeMinimalSpec()
	spec.Requirements.Requirements = []Requirement{
		{
			Id: "04-REQ-1", Title: "Test Req",
			UserStory: UserStory{Role: "dev", Goal: "test", Benefit: "test"},
			AcceptanceCriteria: []Criterion{{
				Id: "04-REQ-1.1", EarsPattern: CriterionEarsPatternUbiquitous,
				System: "sys", Action: "do something", ReturnContract: nil,
			}},
			EdgeCases: []Criterion{},
		},
	}
	spec.Requirements.ErrorHandling = []ErrorHandlingEntry{
		{
			Id:            "04-ERR-1",
			Condition:     "when request fails",
			Behavior:      "returns appropriate error",
			RequirementId: "04-REQ-1",
		},
	}
	result := spec.Validate()
	var found bool
	for _, w := range result.Warnings {
		if w.Category == "warning" &&
			strings.Contains(w.Message, "appropriate") &&
			strings.Contains(w.Message, "04-ERR-1") &&
			strings.Contains(w.Message, "behavior") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a vague language warning for error_handling behavior containing 'appropriate', got warnings: %v", result.Warnings)
	}
}

// TS-13-3: No vague language warning for clean error_handling behavior.
func TestValidate_Warning_VagueLanguageInErrorHandlingBehavior_Clean(t *testing.T) {
	defer requireImplemented(t)
	spec := makeMinimalSpec()
	spec.Requirements.Requirements = []Requirement{
		{
			Id: "04-REQ-1", Title: "Test Req",
			UserStory: UserStory{Role: "dev", Goal: "test", Benefit: "test"},
			AcceptanceCriteria: []Criterion{{
				Id: "04-REQ-1.1", EarsPattern: CriterionEarsPatternUbiquitous,
				System: "sys", Action: "do something", ReturnContract: nil,
			}},
			EdgeCases: []Criterion{},
		},
	}
	spec.Requirements.ErrorHandling = []ErrorHandlingEntry{
		{
			Id:            "04-ERR-1",
			Condition:     "when request fails",
			Behavior:      "return HTTP 500 with JSON error body",
			RequirementId: "04-REQ-1",
		},
	}
	result := spec.Validate()
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "vague") && strings.Contains(w.Message, "04-ERR-1") {
			t.Errorf("expected no vague warning for clean behavior, got: %s", w.Message)
		}
	}
}

// TS-13-4: appropriate and properly still trigger vague warnings after regex update.
func TestValidate_Warning_VagueLanguage_AppropriateAndProperly(t *testing.T) {
	defer requireImplemented(t)
	spec := makeMinimalSpec()
	spec.Requirements.Requirements = []Requirement{
		{
			Id: "04-REQ-1", Title: "Vague",
			UserStory: UserStory{Role: "dev", Goal: "detect", Benefit: "clarity"},
			AcceptanceCriteria: []Criterion{{
				Id: "04-REQ-1.1", EarsPattern: CriterionEarsPatternUbiquitous,
				System: "sys", Action: "handle appropriate cases properly", ReturnContract: nil,
			}},
			EdgeCases: []Criterion{},
		},
	}
	result := spec.Validate()
	foundAppropriate := false
	foundProperly := false
	for _, w := range result.Warnings {
		lower := strings.ToLower(w.Message)
		if strings.Contains(lower, `"appropriate"`) {
			foundAppropriate = true
		}
		if strings.Contains(lower, `"properly"`) {
			foundProperly = true
		}
	}
	if !foundAppropriate {
		t.Error("expected vague warning for 'appropriate' — must still match after regex update")
	}
	if !foundProperly {
		t.Error("expected vague warning for 'properly' — must still match after regex update")
	}
}

// TS-04-25: Validate appends scope limit warning when > 10 requirements.
// Requirement: 04-REQ-19.1
func TestValidate_Warning_ScopeLimit(t *testing.T) {
	defer requireImplemented(t)
	spec := makeMinimalSpec()
	for i := 1; i <= 11; i++ {
		spec.Requirements.Requirements = append(spec.Requirements.Requirements, Requirement{
			Id: fmt.Sprintf("04-REQ-%d", i), Title: fmt.Sprintf("Req %d", i),
			UserStory: UserStory{Role: "dev", Goal: "scope", Benefit: "manageable"},
			AcceptanceCriteria: []Criterion{{
				Id: fmt.Sprintf("04-REQ-%d.1", i), EarsPattern: CriterionEarsPatternUbiquitous,
				System: "sys", Action: fmt.Sprintf("do %d", i), ReturnContract: nil,
			}},
			EdgeCases: []Criterion{},
		})
	}
	result := spec.Validate()
	var found bool
	for _, w := range result.Warnings {
		lower := strings.ToLower(w.Message)
		if strings.Contains(lower, "split") || strings.Contains(lower, "large") || strings.Contains(w.Message, "11") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected scope limit warning for 11 reqs, got warnings: %v", result.Warnings)
	}
}

// 04-REQ-19.E1: Exactly 10 requirements produces no warning.
func TestValidate_Warning_ScopeLimit_ExactlyAtThreshold(t *testing.T) {
	defer requireImplemented(t)
	spec := makeMinimalSpec()
	for i := 1; i <= 10; i++ {
		spec.Requirements.Requirements = append(spec.Requirements.Requirements, Requirement{
			Id: fmt.Sprintf("04-REQ-%d", i), Title: fmt.Sprintf("Req %d", i),
			UserStory: UserStory{Role: "dev", Goal: "scope", Benefit: "manageable"},
			AcceptanceCriteria: []Criterion{{
				Id: fmt.Sprintf("04-REQ-%d.1", i), EarsPattern: CriterionEarsPatternUbiquitous,
				System: "sys", Action: fmt.Sprintf("do %d", i), ReturnContract: nil,
			}},
			EdgeCases: []Criterion{},
		})
	}
	result := spec.Validate()
	for _, w := range result.Warnings {
		lower := strings.ToLower(w.Message)
		if strings.Contains(lower, "split") || strings.Contains(lower, "large") || strings.Contains(lower, "too") {
			t.Errorf("expected no scope limit warning at exactly 10 reqs, got: %s", w.Message)
		}
	}
}

// ---------------------------------------------------------------------------
// Task Group 9: ValidateStructured output shape alignment (04-REQ-20)
// Test Spec: TS-04-26, TS-04-27, TS-04-28
// Property: 04-PROP-5
// ---------------------------------------------------------------------------

// TestValidateStructured_OutputShape_ValidKeys verifies that
// ValidateStructured returns a map with keys 'errors' and 'valid'.
// 'warnings' key is only present when warnings exist (NS-REQ-5).
// 'valid' is true when errors is empty.
// Test Spec: TS-04-26, TS-NS-5. Requirements: 04-REQ-20.1, NS-REQ-5.
func TestValidateStructured_OutputShape_ValidKeys(t *testing.T) {
	defer requireImplemented(t)

	t.Run("valid_spec_all_keys_present", func(t *testing.T) {
		// Use the golden valid spec: task_groups now requires minItems:1 so a
		// programmatically-built spec with empty task_groups is invalid.
		spec, err := LoadSpec("./../testdata/valid_spec")
		if err != nil {
			t.Fatalf("LoadSpec returned unexpected error: %v", err)
		}
		output := spec.ValidateStructured()

		// Assert required keys exist
		if _, ok := output["valid"]; !ok {
			t.Fatal("output missing 'valid' key")
		}
		if _, ok := output["errors"]; !ok {
			t.Fatal("output missing 'errors' key")
		}

		// NS-REQ-5: 'warnings' key must be absent for valid spec with no warnings
		if _, exists := output["warnings"]; exists {
			t.Error("output has 'warnings' key but should be absent for valid spec with no warnings (NS-REQ-5)")
		}

		// Assert types
		valid, ok := output["valid"].(bool)
		if !ok {
			t.Fatalf("output['valid'] is not bool, got %T", output["valid"])
		}
		if !valid {
			t.Error("output['valid'] = false, want true for valid spec")
		}

		errs, ok := output["errors"].([]map[string]any)
		if !ok {
			t.Fatalf("output['errors'] is not []map[string]any, got %T", output["errors"])
		}
		if len(errs) != 0 {
			t.Errorf("output['errors'] has %d entries, want 0", len(errs))
		}
	})

	t.Run("spec_with_errors_valid_is_false", func(t *testing.T) {
		spec := buildInvalidSpec()
		output := spec.ValidateStructured()

		valid, ok := output["valid"].(bool)
		if !ok {
			t.Fatalf("output['valid'] is not bool, got %T", output["valid"])
		}
		if valid {
			t.Error("output['valid'] = true, want false for spec with errors")
		}

		errs, ok := output["errors"].([]map[string]any)
		if !ok {
			t.Fatalf("output['errors'] is not []map[string]any, got %T", output["errors"])
		}
		if len(errs) == 0 {
			t.Error("output['errors'] is empty, want non-empty for spec with errors")
		}
	})
}

// TestValidateStructured_OutputShape_ErrorFields verifies that error maps
// in ValidateStructured output use two distinct shapes:
//   - Schema errors: {"category": "schema", "artifact": ..., "message": ...}
//     with optional "path" and "value".
//   - Integrity errors: {"category": "integrity", "check": ..., "message": ...}
//
// Test Spec: TS-04-27, TS-NS-4. Requirements: 04-REQ-20.2, NS-REQ-4.
func TestValidateStructured_OutputShape_ErrorFields(t *testing.T) {
	defer requireImplemented(t)

	// Build a spec that triggers both a schema error and an integrity error.
	// Schema error: requirement with empty ID violates JSON schema.
	// Integrity error: correctness property with no matching property test.
	spec := &Spec{
		SpecID:        "04",
		SpecName:      "test_output_shape",
		Title:         "Test Output Shape",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test-author",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			SpecId:        "04",
			SpecName:      "test_output_shape",
			SchemaVersion: 1,
			Introduction:  "Test spec for output shape validation.",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements: []Requirement{
				{
					Id:    "", // empty ID — schema violation
					Title: "", // empty title — schema violation
					UserStory: UserStory{
						Role: "dev", Goal: "test", Benefit: "validation",
					},
					AcceptanceCriteria: []Criterion{},
					EdgeCases:          []Criterion{},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{
				{
					Id: "04-PROP-1", Title: "Uncovered Prop",
					ForAny: "any input", Invariant: "holds",
					Validates: []string{"04-REQ-1.1"},
				},
			},
			ExecutionPaths: []ExecutionPath{},
			ErrorHandling:  []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "04",
			SpecName:      "test_output_shape",
			SchemaVersion: 1,
			TestCases:     []TestCase{},
			PropertyTests: []PropertyTest{}, // no property tests — triggers cross_file_3
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SpecId:        "04",
			SpecName:      "test_output_shape",
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

	output := spec.ValidateStructured()

	errs, ok := output["errors"].([]map[string]any)
	if !ok {
		t.Fatalf("output['errors'] is not []map[string]any, got %T", output["errors"])
	}
	if len(errs) == 0 {
		t.Fatal("output['errors'] is empty, want at least one entry")
	}

	// Every error map must have 'category' and 'message'.
	for i, e := range errs {
		if _, exists := e["category"]; !exists {
			t.Errorf("errors[%d] missing key 'category'", i)
		}
		if _, exists := e["message"]; !exists {
			t.Errorf("errors[%d] missing key 'message'", i)
		}
	}

	// NS-REQ-4: Verify at least one schema error with distinct shape
	var hasSchema bool
	for _, e := range errs {
		if cat, _ := e["category"].(string); cat == "schema" {
			hasSchema = true
			// Schema errors must have 'artifact', not 'file'
			if _, hasArtifact := e["artifact"]; !hasArtifact {
				t.Error("schema error missing 'artifact' key (NS-REQ-4)")
			}
			// Schema errors must NOT have 'check' key
			if _, hasCheck := e["check"]; hasCheck {
				t.Error("schema error has 'check' key, which belongs to integrity shape (NS-REQ-4)")
			}
			break
		}
	}
	if !hasSchema {
		t.Error("expected at least one error with category='schema'")
	}

	// NS-REQ-4: Verify at least one integrity error with distinct shape
	var hasIntegrity bool
	for _, e := range errs {
		if cat, _ := e["category"].(string); cat == "integrity" {
			hasIntegrity = true
			// Integrity errors must have 'check', not 'artifact'
			if _, hasCheck := e["check"]; !hasCheck {
				t.Error("integrity error missing 'check' key (NS-REQ-4)")
			}
			// Integrity errors must NOT have 'artifact' or 'file' keys
			if _, hasArtifact := e["artifact"]; hasArtifact {
				t.Error("integrity error has 'artifact' key, which belongs to schema shape (NS-REQ-4)")
			}
			break
		}
	}
	if !hasIntegrity {
		t.Error("expected at least one error with category='integrity'")
	}
}

// TestValidateStructured_OutputShape_WarningFields verifies that each
// warning map in ValidateStructured output includes 'category'="warning",
// 'message', and 'entity_id' fields with non-empty values.
// Test Spec: TS-04-28, TS-NS-3. Requirements: 04-REQ-20.3, NS-REQ-3.
func TestValidateStructured_OutputShape_WarningFields(t *testing.T) {
	defer requireImplemented(t)

	// Build a spec with 11 requirements to trigger scope limit warning.
	spec := makeMinimalSpec()
	for i := 1; i <= 11; i++ {
		spec.Requirements.Requirements = append(spec.Requirements.Requirements, Requirement{
			Id:    fmt.Sprintf("04-REQ-%d", i),
			Title: fmt.Sprintf("Requirement %d", i),
			UserStory: UserStory{
				Role: "developer", Goal: "scope test", Benefit: "manageable specs",
			},
			AcceptanceCriteria: []Criterion{
				{
					Id: fmt.Sprintf("04-REQ-%d.1", i), EarsPattern: CriterionEarsPatternUbiquitous,
					System: "the system", Action: fmt.Sprintf("do action %d", i),
					ReturnContract: nil,
				},
			},
			EdgeCases: []Criterion{},
		})
	}

	output := spec.ValidateStructured()

	warnings, ok := output["warnings"].([]map[string]any)
	if !ok {
		t.Fatalf("output['warnings'] is not []map[string]any, got %T", output["warnings"])
	}
	if len(warnings) == 0 {
		t.Fatal("output['warnings'] is empty, want at least one entry")
	}

	// NS-REQ-3: Each warning must have category="warning", message, entity_id.
	for i, w := range warnings {
		if cat, _ := w["category"].(string); cat != "warning" {
			t.Errorf("warnings[%d] category = %q, want 'warning' (NS-REQ-3)", i, cat)
		}
		msg, _ := w["message"].(string)
		if msg == "" {
			t.Errorf("warnings[%d] 'message' is empty, want non-empty", i)
		}
		if _, hasEntityID := w["entity_id"]; !hasEntityID {
			t.Errorf("warnings[%d] missing 'entity_id' key (NS-REQ-3)", i)
		}
	}
}

// TestValidateStructured_ValidFlagConsistency is the property test for
// 04-PROP-5: the 'valid' field in ValidateStructured output is true if
// and only if the 'errors' slice is empty.
// Property: 04-PROP-5. Validates: 04-REQ-20.1, 04-REQ-20.E1, 04-REQ-20.E2.
func TestValidateStructured_ValidFlagConsistency(t *testing.T) {
	defer requireImplemented(t)

	cases := []struct {
		name string
		spec *Spec
	}{
		{"valid_spec", func() *Spec {
			s, _ := LoadSpec("./../testdata/valid_spec")
			return s
		}()},
		{"invalid_spec", buildInvalidSpec()},
		{"warnings_only_spec", buildSpecWithWarnings()},
		{"minimal_spec", makeMinimalSpec()},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if tt.spec == nil {
				t.Skip("spec not loaded")
			}
			output := tt.spec.ValidateStructured()

			valid, ok := output["valid"].(bool)
			if !ok {
				t.Fatalf("output['valid'] is not bool, got %T", output["valid"])
			}

			errs, ok := output["errors"].([]map[string]any)
			if !ok {
				t.Fatalf("output['errors'] is not []map[string]any, got %T", output["errors"])
			}

			// PROP-5 invariant: valid == (len(errors) == 0)
			if valid && len(errs) != 0 {
				t.Errorf("valid is true but errors has %d entries — violates PROP-5", len(errs))
			}
			if !valid && len(errs) == 0 {
				t.Error("valid is false but errors is empty — violates PROP-5")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// EARS constraint validation tests (issue #5)
// ---------------------------------------------------------------------------

// buildEarsSpec is a helper that creates a minimal Spec with a single
// requirement containing the given acceptance criteria.
func buildEarsSpec(criteria []Criterion) *Spec {
	return &Spec{
		SpecID:        "01",
		SpecName:      "test_ears",
		Title:         "Test EARS",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			SpecId:        "01",
			SpecName:      "test_ears",
			SchemaVersion: 1,
			Introduction:  "Test.",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements: []Requirement{
				{
					Id:    "01-REQ-1",
					Title: "Test requirement",
					UserStory: UserStory{
						Role:    "dev",
						Goal:    "test",
						Benefit: "testing",
					},
					AcceptanceCriteria: criteria,
					EdgeCases:          []Criterion{},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "01",
			SpecName:      "test_ears",
			SchemaVersion: 1,
			TestCases:     []TestCase{},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SpecId:        "01",
			SpecName:      "test_ears",
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
}

// TS-NS-1: Valid EARS field combinations produce no EARS constraint errors.
// Requirement: NS-REQ-1
func TestValidateSchema_EarsConstraints_ValidCombinations(t *testing.T) {
	criteria := []Criterion{
		{
			Id:          "01-REQ-1.1",
			EarsPattern: CriterionEarsPatternUbiquitous,
			Action:      "the system SHALL do X",
			System:      "system",
			// ubiquitous: no pattern fields required
		},
		{
			Id:          "01-REQ-1.2",
			EarsPattern: CriterionEarsPatternEventDriven,
			Action:      "the system SHALL do X",
			System:      "system",
			Trigger:     strPtr("When user clicks"),
		},
		{
			Id:          "01-REQ-1.3",
			EarsPattern: CriterionEarsPatternComplexEvent,
			Action:      "the system SHALL do X",
			System:      "system",
			Trigger:     strPtr("When user clicks"),
			Condition:   strPtr("while logged in"),
		},
		{
			Id:          "01-REQ-1.4",
			EarsPattern: CriterionEarsPatternStateDriven,
			Action:      "the system SHALL do X",
			System:      "system",
			State:       strPtr("while in state A"),
		},
		{
			Id:             "01-REQ-1.5",
			EarsPattern:    CriterionEarsPatternUnwanted,
			Action:         "the system SHALL do X",
			System:         "system",
			ErrorCondition: strPtr("if error occurs"),
			ReturnContract: CriterionReturnContract(strPtr("returns error")),
		},
		{
			Id:          "01-REQ-1.6",
			EarsPattern: CriterionEarsPatternOptional,
			Action:      "the system SHALL do X",
			System:      "system",
			Feature:     strPtr("where feature F is enabled"),
		},
	}

	spec := buildEarsSpec(criteria)
	result := spec.ValidateSchema()

	// Filter for ears_constraint errors only
	var earsErrors []ValidationEntry
	for _, e := range result.Errors {
		if e.Check == "ears_constraint" {
			earsErrors = append(earsErrors, e)
		}
	}

	if len(earsErrors) != 0 {
		for _, e := range earsErrors {
			t.Errorf("unexpected EARS constraint error: %s", e.Message)
		}
	}
}

// TS-NS-2: Missing required field produces a schema-category error.
// Requirement: NS-REQ-2
func TestValidateSchema_EarsConstraints_MissingRequired(t *testing.T) {
	criteria := []Criterion{
		{
			Id:          "01-REQ-1.1",
			EarsPattern: CriterionEarsPatternEventDriven,
			Action:      "the system SHALL do X",
			System:      "system",
			// Trigger is nil — required for event_driven
		},
	}

	spec := buildEarsSpec(criteria)
	result := spec.ValidateSchema()

	found := false
	for _, e := range result.Errors {
		if e.Category == "schema" &&
			strings.Contains(e.Message, "trigger") &&
			strings.Contains(e.Message, "01-REQ-1.1") {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("expected schema error mentioning 'trigger' and '01-REQ-1.1', got errors: %v", result.Errors)
	}
}

// TS-NS-3: Forbidden field set produces a schema-category error.
// Requirement: NS-REQ-3
func TestValidateSchema_EarsConstraints_ForbiddenField(t *testing.T) {
	criteria := []Criterion{
		{
			Id:          "01-REQ-1.1",
			EarsPattern: CriterionEarsPatternUbiquitous,
			Action:      "the system SHALL do X",
			System:      "system",
			Trigger:     strPtr("some trigger"), // forbidden for ubiquitous
		},
	}

	spec := buildEarsSpec(criteria)
	result := spec.ValidateSchema()

	found := false
	for _, e := range result.Errors {
		if e.Category == "schema" &&
			strings.Contains(e.Message, "trigger") &&
			strings.Contains(e.Message, "must not have") &&
			strings.Contains(e.Message, "01-REQ-1.1") {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("expected schema error mentioning 'trigger', 'must not have', and '01-REQ-1.1', got errors: %v", result.Errors)
	}
}

// TS-NS-4: Invalid ears_pattern value produces a schema-category error.
// Requirement: NS-REQ-4
func TestValidateSchema_EarsConstraints_InvalidPattern(t *testing.T) {
	criteria := []Criterion{
		{
			Id:          "01-REQ-1.1",
			EarsPattern: CriterionEarsPattern("bogus"),
			Action:      "the system SHALL do X",
			System:      "system",
		},
	}

	spec := buildEarsSpec(criteria)
	result := spec.ValidateSchema()

	if result.Valid {
		t.Fatal("expected Valid=false for invalid ears_pattern")
	}

	found := false
	for _, e := range result.Errors {
		if e.Category == "schema" &&
			strings.Contains(e.Message, "bogus") {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("expected schema error mentioning 'bogus', got errors: %v", result.Errors)
	}
}

// TS-NS-5: EARS constraint validation is wired into Validate().
// Requirement: NS-REQ-5
func TestValidate_EarsConstraints_WiredIntoValidate(t *testing.T) {
	criteria := []Criterion{
		{
			Id:          "01-REQ-1.1",
			EarsPattern: CriterionEarsPatternEventDriven,
			Action:      "the system SHALL do X",
			System:      "system",
			// Trigger is nil — required for event_driven
		},
	}

	spec := buildEarsSpec(criteria)
	result := spec.Validate()

	if result.Valid {
		t.Fatal("expected Validate().Valid=false for event_driven criterion missing trigger")
	}

	found := false
	for _, e := range result.Errors {
		if strings.Contains(e.Message, "trigger") &&
			strings.Contains(e.Message, "01-REQ-1.1") {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("expected Validate() error mentioning 'trigger' and '01-REQ-1.1', got errors: %v", result.Errors)
	}
}

// ---------------------------------------------------------------------------
// Coverage gap severity and no-criteria tests (issue #10)
// ---------------------------------------------------------------------------

// TestValidateCrossFile_CoverageGapCriterionIsError verifies that an uncovered
// acceptance criterion produces an error (not a warning) with Check "coverage_gap".
// Test Spec: TS-NS-1, Requirement: NS-REQ-1
func TestValidateCrossFile_CoverageGapCriterionIsError(t *testing.T) {
	defer requireImplemented(t)

	spec := &Spec{
		SpecID:        "01",
		SpecName:      "test_cov",
		Title:         "Coverage Gap Test",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			SpecId:        "01",
			SpecName:      "test_cov",
			SchemaVersion: 1,
			Introduction:  "Test.",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements: []Requirement{
				{
					Id:    "01-REQ-1",
					Title: "Some Requirement",
					UserStory: UserStory{
						Role:    "developer",
						Goal:    "test coverage",
						Benefit: "confidence",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "01-REQ-1.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "do something",
						},
					},
					EdgeCases: []Criterion{},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "01",
			SpecName:      "test_cov",
			SchemaVersion: 1,
			TestCases:     []TestCase{}, // No test covering 01-REQ-1.1
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SpecId:        "01",
			SpecName:      "test_cov",
			SchemaVersion: 1,
			TestCommands:  TestCommands{},
			Dependencies:  []TaskDependency{},
			TaskGroups:    []TaskGroup{},
			Traceability:  []TraceabilityEntry{},
		},
	}

	result := spec.ValidateCrossFile()

	if result.Valid {
		t.Error("ValidateCrossFile().Valid = true, want false for uncovered acceptance criterion")
	}

	foundInErrors := false
	for _, e := range result.Errors {
		if e.Check == "coverage_gap" && e.Category == "integrity" {
			foundInErrors = true
			break
		}
	}
	if !foundInErrors {
		t.Errorf("expected coverage_gap entry in Errors with category 'integrity', got errors: %v", result.Errors)
	}

	// Ensure coverage_gap does NOT appear in warnings
	for _, w := range result.Warnings {
		if w.Check == "coverage_gap" {
			t.Error("coverage_gap entry found in Warnings — should be in Errors only")
		}
	}
}

// TestValidateCrossFile_CoverageGapEdgeCaseIsError verifies that an uncovered
// edge case criterion produces an error (not a warning) with Check "coverage_gap".
// Test Spec: TS-NS-2, Requirement: NS-REQ-2
func TestValidateCrossFile_CoverageGapEdgeCaseIsError(t *testing.T) {
	defer requireImplemented(t)

	spec := &Spec{
		SpecID:        "01",
		SpecName:      "test_cov_ec",
		Title:         "Coverage Gap Edge Case Test",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			SpecId:        "01",
			SpecName:      "test_cov_ec",
			SchemaVersion: 1,
			Introduction:  "Test.",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements: []Requirement{
				{
					Id:    "01-REQ-1",
					Title: "Some Requirement",
					UserStory: UserStory{
						Role:    "developer",
						Goal:    "test coverage",
						Benefit: "confidence",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "01-REQ-1.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "do something",
						},
					},
					EdgeCases: []Criterion{
						{
							Id:          "01-REQ-1.E1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "handle edge case",
						},
					},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "01",
			SpecName:      "test_cov_ec",
			SchemaVersion: 1,
			TestCases: []TestCase{
				{
					Id:                  "TS-01-1",
					RequirementId:       "01-REQ-1.1",
					Kind:                "unit",
					Description:         "covers acceptance criterion",
					Preconditions:       []string{},
					Expected:            "pass",
					AssertionPseudocode: "assert true",
				},
			},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{}, // No edge case test covering 01-REQ-1.E1
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{RequirementsCovered: []string{"01-REQ-1.1"}},
		},
		Tasks: &TasksV1Json{
			SpecId:        "01",
			SpecName:      "test_cov_ec",
			SchemaVersion: 1,
			TestCommands:  TestCommands{},
			Dependencies:  []TaskDependency{},
			TaskGroups:    []TaskGroup{},
			Traceability:  []TraceabilityEntry{},
		},
	}

	result := spec.ValidateCrossFile()

	if result.Valid {
		t.Error("ValidateCrossFile().Valid = true, want false for uncovered edge case")
	}

	foundInErrors := false
	for _, e := range result.Errors {
		if e.Check == "coverage_gap" && e.Category == "integrity" {
			foundInErrors = true
			break
		}
	}
	if !foundInErrors {
		t.Errorf("expected coverage_gap entry in Errors with category 'integrity', got errors: %v", result.Errors)
	}

	// Ensure coverage_gap does NOT appear in warnings
	for _, w := range result.Warnings {
		if w.Check == "coverage_gap" {
			t.Error("coverage_gap entry found in Warnings — should be in Errors only")
		}
	}
}

// TestValidateCrossFile_NoCriteria verifies that a requirement with no
// acceptance criteria and no edge cases produces an error with Check "no_criteria".
// Test Spec: TS-NS-3, Requirement: NS-REQ-3
func TestValidateCrossFile_NoCriteria(t *testing.T) {
	defer requireImplemented(t)

	spec := &Spec{
		SpecID:        "01",
		SpecName:      "test_no_criteria",
		Title:         "No Criteria Test",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			SpecId:        "01",
			SpecName:      "test_no_criteria",
			SchemaVersion: 1,
			Introduction:  "Test.",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements: []Requirement{
				{
					Id:    "01-REQ-1",
					Title: "Empty Requirement",
					UserStory: UserStory{
						Role:    "developer",
						Goal:    "test no criteria",
						Benefit: "parity",
					},
					AcceptanceCriteria: []Criterion{},
					EdgeCases:          []Criterion{},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "01",
			SpecName:      "test_no_criteria",
			SchemaVersion: 1,
			TestCases:     []TestCase{},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SpecId:        "01",
			SpecName:      "test_no_criteria",
			SchemaVersion: 1,
			TestCommands:  TestCommands{},
			Dependencies:  []TaskDependency{},
			TaskGroups:    []TaskGroup{},
			Traceability:  []TraceabilityEntry{},
		},
	}

	result := spec.ValidateCrossFile()

	if result.Valid {
		t.Error("ValidateCrossFile().Valid = true, want false for requirement with no criteria")
	}

	foundNoCriteria := false
	for _, e := range result.Errors {
		if e.Check == "no_criteria" && strings.Contains(e.RequirementID, "01-REQ-1") {
			foundNoCriteria = true
			break
		}
	}
	if !foundNoCriteria {
		t.Errorf("expected no_criteria error referencing 01-REQ-1, got errors: %v", result.Errors)
	}
}

// TestValidateCrossFile_FullyCoveredSpec verifies that a spec where every
// criterion has test coverage and every requirement has at least one criterion
// passes with no coverage_gap errors.
// Test Spec: TS-NS-5, Requirement: NS-REQ-5
func TestValidateCrossFile_FullyCoveredSpec(t *testing.T) {
	defer requireImplemented(t)

	spec := &Spec{
		SpecID:        "01",
		SpecName:      "test_full_cov",
		Title:         "Fully Covered Spec",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			SpecId:        "01",
			SpecName:      "test_full_cov",
			SchemaVersion: 1,
			Introduction:  "Test.",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements: []Requirement{
				{
					Id:    "01-REQ-1",
					Title: "Covered Requirement",
					UserStory: UserStory{
						Role:    "developer",
						Goal:    "full coverage",
						Benefit: "confidence",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "01-REQ-1.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "return data",
						},
					},
					EdgeCases: []Criterion{
						{
							Id:          "01-REQ-1.E1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "handle edge case",
						},
					},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "01",
			SpecName:      "test_full_cov",
			SchemaVersion: 1,
			TestCases: []TestCase{
				{
					Id:                  "TS-01-1",
					RequirementId:       "01-REQ-1.1",
					Kind:                "unit",
					Description:         "covers acceptance criterion",
					Preconditions:       []string{},
					Expected:            "pass",
					AssertionPseudocode: "assert true",
				},
			},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{
				{
					Id:                  "TS-01-E1",
					RequirementId:       "01-REQ-1.E1",
					Kind:                "unit",
					Description:         "covers edge case",
					Preconditions:       []string{},
					Expected:            "pass",
					AssertionPseudocode: "assert true",
				},
			},
			SmokeTests: []SmokeTest{},
			Coverage: Coverage{
				RequirementsCovered: []string{"01-REQ-1.1", "01-REQ-1.E1"},
			},
		},
		Tasks: &TasksV1Json{
			SpecId:        "01",
			SpecName:      "test_full_cov",
			SchemaVersion: 1,
			TestCommands:  TestCommands{},
			Dependencies:  []TaskDependency{},
			TaskGroups:    []TaskGroup{},
			Traceability:  []TraceabilityEntry{},
		},
	}

	result := spec.ValidateCrossFile()

	// Check no coverage_gap errors
	for _, e := range result.Errors {
		if e.Check == "coverage_gap" {
			t.Errorf("unexpected coverage_gap error: %v", e)
		}
	}
	// Check no coverage_gap warnings
	for _, w := range result.Warnings {
		if w.Check == "coverage_gap" {
			t.Errorf("unexpected coverage_gap warning: %v", w)
		}
	}

	if !result.Valid {
		t.Errorf("ValidateCrossFile().Valid = false, want true for fully covered spec; errors = %v", result.Errors)
	}
}

// ---------------------------------------------------------------------------
// Cross-file rule 1: Traceability requirement_id resolution
// TS-NS-1, TS-NS-3
// ---------------------------------------------------------------------------

// TestCrossFile1_TraceabilityDanglingRef verifies that a traceability entry
// referencing a non-existent ID produces a cross_file_1 error, and that
// valid requirement, criterion, or edge_case IDs do not.
// Test Spec: TS-NS-1, TS-NS-3
// Requirements: NS-REQ-1, NS-REQ-3
func TestCrossFile1_TraceabilityDanglingRef(t *testing.T) {
	tests := []struct {
		name         string
		reqID        string // traceability entry's RequirementId
		wantError    bool
		wantCheck    string
		wantArtifact string
	}{
		{
			name:         "bogus requirement_id produces cross_file_1 error",
			reqID:        "04-REQ-GHOST",
			wantError:    true,
			wantCheck:    "cross_file_1",
			wantArtifact: "tasks.json",
		},
		{
			name:      "valid criterion ID produces no cross_file_1 error",
			reqID:     "04-REQ-1.1",
			wantError: false,
		},
		{
			name:      "top-level requirement ID produces no cross_file_1 error",
			reqID:     "04-REQ-1",
			wantError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := makeMinimalSpec()
			spec.Requirements.Requirements = []Requirement{
				{
					Id:    "04-REQ-1",
					Title: "Test Requirement",
					UserStory: UserStory{
						Role:    "author",
						Goal:    "test",
						Benefit: "coverage",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "04-REQ-1.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "do something",
						},
					},
					EdgeCases: []Criterion{},
				},
			}
			spec.TestSpec.TestCases = []TestCase{
				{
					Id:                  "TS-04-1",
					RequirementId:       "04-REQ-1.1",
					Kind:                "unit",
					Description:         "basic test",
					Preconditions:       []string{},
					Expected:            "pass",
					AssertionPseudocode: "assert true",
				},
			}
			spec.Tasks.Traceability = []TraceabilityEntry{
				{
					RequirementId: tc.reqID,
					TestSpecId:    "TS-04-1",
					TaskId:        "1.1",
				},
			}

			result := spec.ValidateCrossFile()

			// Filter for cross_file_1 errors
			var crossFile1Errors []ValidationEntry
			for _, e := range result.Errors {
				if e.Check == "cross_file_1" {
					crossFile1Errors = append(crossFile1Errors, e)
				}
			}

			if tc.wantError {
				if len(crossFile1Errors) == 0 {
					t.Fatalf("expected cross_file_1 error for RequirementId=%q, got none; all errors: %v", tc.reqID, result.Errors)
				}
				e := crossFile1Errors[0]
				if e.Category != "integrity" {
					t.Errorf("cross_file_1 error Category = %q, want 'integrity'", e.Category)
				}
				if e.Artifact != tc.wantArtifact {
					t.Errorf("cross_file_1 error Artifact = %q, want %q", e.Artifact, tc.wantArtifact)
				}
				if !strings.Contains(e.Message, tc.reqID) {
					t.Errorf("cross_file_1 error Message = %q, want it to contain %q", e.Message, tc.reqID)
				}
			} else {
				if len(crossFile1Errors) > 0 {
					t.Errorf("expected no cross_file_1 errors for RequirementId=%q, got %v", tc.reqID, crossFile1Errors)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Cross-file rule 1: ErrorHandling requirement_id resolution
// TS-NS-2, TS-NS-4
// ---------------------------------------------------------------------------

// TestCrossFile1_ErrorHandlingDanglingRef verifies that an error_handling entry
// referencing a non-existent requirement/criterion/edge_case ID produces a
// cross_file_1 error, and that valid IDs do not.
// Test Spec: TS-NS-2, TS-NS-4
// Requirements: NS-REQ-2, NS-REQ-4
func TestCrossFile1_ErrorHandlingDanglingRef(t *testing.T) {
	tests := []struct {
		name         string
		reqID        string // error_handling entry's RequirementId
		wantError    bool
		wantCheck    string
		wantArtifact string
	}{
		{
			name:         "bogus requirement_id produces cross_file_1 error",
			reqID:        "04-REQ-999",
			wantError:    true,
			wantCheck:    "cross_file_1",
			wantArtifact: "requirements.json",
		},
		{
			name:      "valid top-level requirement ID produces no cross_file_1 error",
			reqID:     "04-REQ-1",
			wantError: false,
		},
		{
			name:      "valid criterion ID produces no cross_file_1 error",
			reqID:     "04-REQ-1.1",
			wantError: false,
		},
		{
			name:      "valid edge_case ID produces no cross_file_1 error",
			reqID:     "04-REQ-1.E1",
			wantError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := makeMinimalSpec()
			spec.Requirements.Requirements = []Requirement{
				{
					Id:    "04-REQ-1",
					Title: "Test Requirement",
					UserStory: UserStory{
						Role:    "author",
						Goal:    "test",
						Benefit: "coverage",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "04-REQ-1.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "do something",
						},
					},
					EdgeCases: []Criterion{
						{
							Id:          "04-REQ-1.E1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "handle edge case",
						},
					},
				},
			}
			spec.Requirements.ErrorHandling = []ErrorHandlingEntry{
				{
					Id:            "04-ERR-1",
					Condition:     "invalid input",
					Behavior:      "return error",
					RequirementId: tc.reqID,
				},
			}

			result := spec.ValidateCrossFile()

			// Filter for cross_file_1 errors
			var crossFile1Errors []ValidationEntry
			for _, e := range result.Errors {
				if e.Check == "cross_file_1" {
					crossFile1Errors = append(crossFile1Errors, e)
				}
			}

			if tc.wantError {
				if len(crossFile1Errors) == 0 {
					t.Fatalf("expected cross_file_1 error for RequirementId=%q, got none; all errors: %v", tc.reqID, result.Errors)
				}
				e := crossFile1Errors[0]
				if e.Category != "integrity" {
					t.Errorf("cross_file_1 error Category = %q, want 'integrity'", e.Category)
				}
				if e.Artifact != tc.wantArtifact {
					t.Errorf("cross_file_1 error Artifact = %q, want %q", e.Artifact, tc.wantArtifact)
				}
				if !strings.Contains(e.Message, tc.reqID) {
					t.Errorf("cross_file_1 error Message = %q, want it to contain %q", e.Message, tc.reqID)
				}
			} else {
				if len(crossFile1Errors) > 0 {
					t.Errorf("expected no cross_file_1 errors for RequirementId=%q, got %v", tc.reqID, crossFile1Errors)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Issue #12: Cross-file rule 7 — spec ID/name consistency (prd.md source)
// ---------------------------------------------------------------------------

// TestValidateCrossFile_CrossFile7_SpecIdMismatch verifies that
// ValidateCrossFile returns errors with Check == "cross_file_7" (not
// "spec_id_mismatch") when an artifact's spec_id differs from the PRD
// frontmatter value.
// Test Spec: TS-NS-1. Requirement: NS-REQ-1.
func TestValidateCrossFile_CrossFile7_SpecIdMismatch(t *testing.T) {
	spec := buildCrossFileBaseSpec()
	spec.Requirements.SpecId = "99" // differs from prd frontmatter "04"

	result := spec.ValidateCrossFile()

	cf7 := filterByCheck(result.Errors, "cross_file_7")
	if len(cf7) == 0 {
		t.Fatal("expected at least one cross_file_7 error for spec_id mismatch, got none")
	}

	// Must NOT use old check name
	old := filterByCheck(result.Errors, "spec_id_mismatch")
	if len(old) != 0 {
		t.Errorf("expected no spec_id_mismatch errors, got %d: %v", len(old), old)
	}
}

// TestValidateCrossFile_CrossFile7_SpecNameMismatch verifies that
// ValidateCrossFile returns errors with Check == "cross_file_7" (not
// "spec_name_mismatch") when an artifact's spec_name differs from the
// PRD frontmatter value.
// Test Spec: TS-NS-2. Requirement: NS-REQ-2.
func TestValidateCrossFile_CrossFile7_SpecNameMismatch(t *testing.T) {
	spec := buildCrossFileBaseSpec()
	spec.TestSpec.SpecName = "wrong_name" // differs from prd frontmatter "test_crossfile"

	result := spec.ValidateCrossFile()

	cf7 := filterByCheck(result.Errors, "cross_file_7")
	if len(cf7) == 0 {
		t.Fatal("expected at least one cross_file_7 error for spec_name mismatch, got none")
	}

	// Must NOT use old check name
	old := filterByCheck(result.Errors, "spec_name_mismatch")
	if len(old) != 0 {
		t.Errorf("expected no spec_name_mismatch errors, got %d: %v", len(old), old)
	}
}

// TestValidateCrossFile_CrossFile7_MessageReferencesPrdMd verifies that
// cross_file_7 error messages reference "prd.md" as the authoritative
// source and name both the prd value and the artifact value.
// Test Spec: TS-NS-3. Requirement: NS-REQ-3.
func TestValidateCrossFile_CrossFile7_MessageReferencesPrdMd(t *testing.T) {
	spec := buildCrossFileBaseSpec()
	spec.Requirements.SpecId = "99" // prd has "04"

	result := spec.ValidateCrossFile()

	cf7 := filterByCheck(result.Errors, "cross_file_7")
	if len(cf7) == 0 {
		t.Fatal("expected at least one cross_file_7 error, got none")
	}

	// Find the requirements.json spec_id error
	var found bool
	for _, e := range cf7 {
		if e.Artifact == "requirements.json" && strings.Contains(e.Message, "spec_id") {
			found = true
			if !strings.Contains(e.Message, "prd.md") {
				t.Errorf("cross_file_7 Message %q does not contain 'prd.md'", e.Message)
			}
			if !strings.Contains(e.Message, "04") {
				t.Errorf("cross_file_7 Message %q does not contain prd value '04'", e.Message)
			}
			if !strings.Contains(e.Message, "99") {
				t.Errorf("cross_file_7 Message %q does not contain artifact value '99'", e.Message)
			}
			break
		}
	}
	if !found {
		t.Error("expected a cross_file_7 error for requirements.json spec_id, not found")
	}
}

// TestValidateCrossFile_CrossFile7_AllThreeArtifacts verifies that
// all three artifacts (requirements.json, test_spec.json, tasks.json)
// produce cross_file_7 errors for both spec_id and spec_name mismatches,
// yielding exactly six errors total.
// Test Spec: TS-NS-4. Requirement: NS-REQ-4.
func TestValidateCrossFile_CrossFile7_AllThreeArtifacts(t *testing.T) {
	spec := buildCrossFileBaseSpec()
	// Set all artifact spec_id/spec_name to differ from PRD frontmatter
	spec.Requirements.SpecId = "99"
	spec.Requirements.SpecName = "wrong_req"
	spec.TestSpec.SpecId = "98"
	spec.TestSpec.SpecName = "wrong_ts"
	spec.Tasks.SpecId = "97"
	spec.Tasks.SpecName = "wrong_tasks"

	result := spec.ValidateCrossFile()

	cf7 := filterByCheck(result.Errors, "cross_file_7")
	if len(cf7) != 6 {
		t.Fatalf("expected exactly 6 cross_file_7 errors, got %d: %v", len(cf7), cf7)
	}

	// Check all three artifacts are represented
	artifacts := map[string]int{}
	for _, e := range cf7 {
		artifacts[e.Artifact]++
	}
	for _, name := range []string{"requirements.json", "test_spec.json", "tasks.json"} {
		if artifacts[name] != 2 {
			t.Errorf("expected 2 cross_file_7 errors for %s, got %d", name, artifacts[name])
		}
	}
}

// TestValidateCrossFile_CrossFile7_ConsistentNoCrossFile7 verifies that
// ValidateCrossFile returns no cross_file_7 errors when all artifacts
// share the same spec_id and spec_name as the PRD frontmatter.
// Test Spec: TS-NS-5. Requirement: NS-REQ-5.
func TestValidateCrossFile_CrossFile7_ConsistentNoCrossFile7(t *testing.T) {
	// Use the valid_spec fixture
	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec returned unexpected error: %v", err)
	}

	result := spec.ValidateCrossFile()

	cf7 := filterByCheck(result.Errors, "cross_file_7")
	if len(cf7) != 0 {
		t.Errorf("expected no cross_file_7 errors for consistent spec, got %d: %v", len(cf7), cf7)
	}
}

// TestValidateCrossFile_CrossFile7_ConsistentBaseSpec verifies no
// cross_file_7 errors on the buildCrossFileBaseSpec helper which has
// all IDs and names consistent.
func TestValidateCrossFile_CrossFile7_ConsistentBaseSpec(t *testing.T) {
	spec := buildCrossFileBaseSpec()

	result := spec.ValidateCrossFile()

	cf7 := filterByCheck(result.Errors, "cross_file_7")
	if len(cf7) != 0 {
		t.Errorf("expected no cross_file_7 errors for consistent base spec, got %d: %v", len(cf7), cf7)
	}
}

// ---------------------------------------------------------------------------
// Issue #11: ID format regexes accept alphanumeric spec_id prefixes
// ---------------------------------------------------------------------------

// TestValidateCrossFile_AlphaPrefixIDs verifies that all nine Go ID-format
// patterns accept alphanumeric (letter-only) spec_id prefixes.
// Test Spec: TS-NS-1, Requirement: NS-REQ-1
func TestValidateCrossFile_AlphaPrefixIDs(t *testing.T) {
	spec := buildSpecWithAlphaPrefix()

	result := spec.ValidateCrossFile()

	// No id_format errors should be present.
	for _, e := range result.Errors {
		if e.Check == "id_format" {
			t.Errorf("unexpected id_format error: %s (path=%s)", e.Message, e.Path)
		}
	}
	if !result.Valid {
		t.Errorf("ValidateCrossFile().Valid = false, want true for alpha-prefix IDs; errors: %v", result.Errors)
	}
}

// TestValidateCrossFile_LongNumericPrefixIDs verifies that ID-format patterns
// accept numeric prefixes longer than two digits (e.g., "123-REQ-1").
// Test Spec: TS-NS-2, Requirement: NS-REQ-2
func TestValidateCrossFile_LongNumericPrefixIDs(t *testing.T) {
	spec := buildSpecWithLongNumericPrefix()

	result := spec.ValidateCrossFile()

	for _, e := range result.Errors {
		if e.Check == "id_format" {
			t.Errorf("unexpected id_format error: %s (path=%s)", e.Message, e.Path)
		}
	}
}

// TestValidateCrossFile_NoDigitTwoInPatterns verifies that none of the nine
// ID-format pattern variables contain the literal `\d{2}`.
// Test Spec: TS-NS-3, Requirement: NS-REQ-3
func TestValidateCrossFile_NoDigitTwoInPatterns(t *testing.T) {
	patterns := map[string]string{
		"requirementIDPattern":   requirementIDPattern.String(),
		"testCaseIDPattern":      testCaseIDPattern.String(),
		"propertyIDPattern":      propertyIDPattern.String(),
		"pathIDPattern":          pathIDPattern.String(),
		"errorHandlingIDPattern": errorHandlingIDPattern.String(),
		"smokeTestIDPattern":     smokeTestIDPattern.String(),
		"propertyTestIDPattern":  propertyTestIDPattern.String(),
		"edgeCaseTestIDPattern":  edgeCaseTestIDPattern.String(),
		"criterionIDPattern":     criterionIDPattern.String(),
	}

	for name, pat := range patterns {
		if strings.Contains(pat, `\d{2}`) {
			t.Errorf("%s still contains \\d{2}: %s", name, pat)
		}
	}
}

// TestValidateCrossFile_AlphaPrefixFixture loads the alpha_prefix_spec
// testdata fixture and verifies Go produces no id_format errors.
// Test Spec: TS-NS-5, Requirement: NS-REQ-5
func TestValidateCrossFile_AlphaPrefixFixture(t *testing.T) {
	spec, err := LoadSpec("./../testdata/alpha_prefix_spec")
	if err != nil {
		t.Fatalf("LoadSpec returned unexpected error: %v", err)
	}

	result := spec.ValidateCrossFile()

	for _, e := range result.Errors {
		if e.Check == "id_format" {
			t.Errorf("unexpected id_format error: %s (path=%s)", e.Message, e.Path)
		}
	}
}

// buildSpecWithAlphaPrefix creates a Spec using letter-only prefix "abc" for
// all nine ID types: requirement, criterion, edge_case, property, path, error,
// test_case, property_test, edge_case_test, smoke_test.
func buildSpecWithAlphaPrefix() *Spec {
	return &Spec{
		SpecID:        "abc",
		SpecName:      "alpha_feature",
		Title:         "Alpha Feature",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test-author",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Alpha Feature\n",
		Requirements: &RequirementsV1Json{
			SpecId:        "abc",
			SpecName:      "alpha_feature",
			SchemaVersion: 1,
			Introduction:  "Test.",
			Glossary:      RequirementsV1JsonGlossary{"spec": "A specification."},
			Requirements: []Requirement{
				{
					Id:    "abc-REQ-1",
					Title: "Alpha Prefix Support",
					UserStory: UserStory{
						Role:    "developer",
						Goal:    "use alpha prefixes",
						Benefit: "flexibility",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "abc-REQ-1.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "accept alpha prefix IDs",
						},
					},
					EdgeCases: []Criterion{
						{
							Id:             "abc-REQ-1.E1",
							EarsPattern:    CriterionEarsPatternUnwanted,
							ErrorCondition: strPtr("the spec has a malformed ID"),
							System:         "the system",
							Action:         "raise validation error",
							ReturnContract: CriterionReturnContract(strPtr("raises ValidationError")),
						},
					},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{
				{
					Id:        "abc-PROP-1",
					Title:     "ID format consistency",
					ForAny:    "valid Spec with alpha prefix",
					Invariant: "all IDs match pattern",
					Validates: []string{"abc-REQ-1.1"},
				},
			},
			ExecutionPaths: []ExecutionPath{
				{
					Id:    "abc-PATH-1",
					Title: "Validate spec with alpha prefix",
					Steps: []PathStep{
						{Actor: "consumer", Action: "call validate"},
						{Actor: "system", Action: "check IDs"},
					},
				},
			},
			ErrorHandling: []ErrorHandlingEntry{
				{
					Id:            "abc-ERR-1",
					Condition:     "Malformed ID",
					Behavior:      "Report id_format error",
					RequirementId: "abc-REQ-1.E1",
				},
			},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "abc",
			SpecName:      "alpha_feature",
			SchemaVersion: 1,
			TestCases: []TestCase{
				{
					Id:                  "TS-abc-1",
					RequirementId:       "abc-REQ-1.1",
					Kind:                "unit",
					Description:         "Alpha prefix IDs pass validation",
					Preconditions:       []string{},
					Expected:            "pass",
					AssertionPseudocode: "assert valid",
				},
			},
			PropertyTests: []PropertyTest{
				{
					Id:             "TS-abc-P1",
					PropertyId:     "abc-PROP-1",
					Validates:      []string{"abc-REQ-1.1"},
					Description:    "ID format consistency",
					ForAnyStrategy: "valid spec with alpha prefix",
					InvariantCheck: "all IDs match pattern",
				},
			},
			EdgeCaseTests: []EdgeCaseTest{
				{
					Id:                  "TS-abc-E1",
					RequirementId:       "abc-REQ-1.E1",
					Kind:                "unit",
					Description:         "Malformed ID raises error",
					Preconditions:       []string{},
					Expected:            "raises ValidationError",
					AssertionPseudocode: "assert error is ValidationError",
				},
			},
			SmokeTests: []SmokeTest{
				{
					Id:              "TS-abc-SMOKE-1",
					ExecutionPathId: "abc-PATH-1",
					Description:     "Validate end-to-end",
					Trigger:         "validate_cross_file(spec)",
					RealComponents:  []string{"validation"},
					Mockable:        []string{},
					ExpectedEffects: []string{"valid result"},
				},
			},
			Coverage: Coverage{
				RequirementsCovered: []string{"abc-REQ-1.1", "abc-REQ-1.E1"},
				PropertiesCovered:   []string{"abc-PROP-1"},
				PathsCovered:        []string{"abc-PATH-1"},
			},
		},
		Tasks: &TasksV1Json{
			SpecId:        "abc",
			SpecName:      "alpha_feature",
			SchemaVersion: 1,
			TestCommands: TestCommands{
				SpecTests: "go test ./...",
				AllTests:  "go test ./...",
				Linter:    "go vet ./...",
			},
			Dependencies: []TaskDependency{},
			TaskGroups: []TaskGroup{
				{
					Id:    1,
					Kind:  TaskGroupKindTests,
					Title: "Write failing spec tests",
					Subtasks: []Subtask{
						{
							Id:              "1.1",
							Title:           "Create test infrastructure",
							Details:         []string{"Set up tests"},
							TestSpecRefs:    []string{"TS-abc-1"},
							RequirementRefs: []string{"abc-REQ-1.1"},
							State:           SubtaskStatePending,
							Optional:        false,
						},
					},
					Verification: VerificationSubtask{
						Id:     "1.V",
						Checks: []string{"All spec tests pass"},
					},
				},
				{
					Id:    2,
					Kind:  TaskGroupKindWiringVerification,
					Title: "Wiring verification",
					Subtasks: []Subtask{
						{
							Id:              "2.1",
							Title:           "Stub and dead-code audit",
							Details:         []string{"Verify all paths"},
							TestSpecRefs:    []string{"TS-abc-SMOKE-1"},
							RequirementRefs: []string{"abc-REQ-1.1"},
							State:           SubtaskStatePending,
							Optional:        false,
						},
					},
					Verification: VerificationSubtask{
						Id:     "2.V",
						Checks: []string{"All smoke tests pass"},
					},
				},
			},
			Traceability: []TraceabilityEntry{
				{
					RequirementId: "abc-REQ-1.1",
					TestSpecId:    "TS-abc-1",
					TaskId:        "1.1",
				},
			},
		},
	}
}

// buildSpecWithLongNumericPrefix creates a Spec using a 3-digit numeric
// prefix "123" to verify that prefixes longer than 2 digits are accepted.
func buildSpecWithLongNumericPrefix() *Spec {
	return &Spec{
		SpecID:        "123",
		SpecName:      "long_numeric_feature",
		Title:         "Long Numeric Feature",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test-author",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Long Numeric Feature\n",
		Requirements: &RequirementsV1Json{
			SpecId:        "123",
			SpecName:      "long_numeric_feature",
			SchemaVersion: 1,
			Introduction:  "Test.",
			Glossary:      RequirementsV1JsonGlossary{"spec": "A specification."},
			Requirements: []Requirement{
				{
					Id:    "123-REQ-1",
					Title: "Long Numeric Prefix",
					UserStory: UserStory{
						Role:    "developer",
						Goal:    "use long numeric prefixes",
						Benefit: "flexibility",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "123-REQ-1.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "accept long numeric prefix IDs",
						},
					},
					EdgeCases: []Criterion{},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths: []ExecutionPath{
				{
					Id:    "123-PATH-1",
					Title: "Validate spec with long numeric prefix",
					Steps: []PathStep{
						{Actor: "consumer", Action: "call validate"},
						{Actor: "system", Action: "check IDs"},
					},
				},
			},
			ErrorHandling: []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "123",
			SpecName:      "long_numeric_feature",
			SchemaVersion: 1,
			TestCases: []TestCase{
				{
					Id:                  "TS-123-1",
					RequirementId:       "123-REQ-1.1",
					Kind:                "unit",
					Description:         "Long numeric prefix IDs pass validation",
					Preconditions:       []string{},
					Expected:            "pass",
					AssertionPseudocode: "assert valid",
				},
			},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests: []SmokeTest{
				{
					Id:              "TS-123-SMOKE-1",
					ExecutionPathId: "123-PATH-1",
					Description:     "Validate end-to-end",
					Trigger:         "validate_cross_file(spec)",
					RealComponents:  []string{"validation"},
					Mockable:        []string{},
					ExpectedEffects: []string{"valid result"},
				},
			},
			Coverage: Coverage{
				RequirementsCovered: []string{"123-REQ-1.1"},
				PathsCovered:        []string{"123-PATH-1"},
			},
		},
		Tasks: &TasksV1Json{
			SpecId:        "123",
			SpecName:      "long_numeric_feature",
			SchemaVersion: 1,
			TestCommands: TestCommands{
				SpecTests: "go test ./...",
				AllTests:  "go test ./...",
				Linter:    "go vet ./...",
			},
			Dependencies: []TaskDependency{},
			TaskGroups: []TaskGroup{
				{
					Id:    1,
					Kind:  TaskGroupKindTests,
					Title: "Write failing spec tests",
					Subtasks: []Subtask{
						{
							Id:              "1.1",
							Title:           "Create test infrastructure",
							Details:         []string{"Set up tests"},
							TestSpecRefs:    []string{"TS-123-1"},
							RequirementRefs: []string{"123-REQ-1.1"},
							State:           SubtaskStatePending,
							Optional:        false,
						},
					},
					Verification: VerificationSubtask{
						Id:     "1.V",
						Checks: []string{"All spec tests pass"},
					},
				},
				{
					Id:    2,
					Kind:  TaskGroupKindWiringVerification,
					Title: "Wiring verification",
					Subtasks: []Subtask{
						{
							Id:              "2.1",
							Title:           "Stub and dead-code audit",
							Details:         []string{"Verify all paths"},
							TestSpecRefs:    []string{"TS-123-SMOKE-1"},
							RequirementRefs: []string{"123-REQ-1.1"},
							State:           SubtaskStatePending,
							Optional:        false,
						},
					},
					Verification: VerificationSubtask{
						Id:     "2.V",
						Checks: []string{"All smoke tests pass"},
					},
				},
			},
			Traceability: []TraceabilityEntry{
				{
					RequirementId: "123-REQ-1.1",
					TestSpecId:    "TS-123-1",
					TaskId:        "1.1",
				},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Issue #7: ID prefix validation and duplicate detection
// ---------------------------------------------------------------------------

// TestValidateCrossFile_PrefixMismatchRequirements verifies that
// ValidateCrossFile returns an id_format error when a requirement ID
// has a prefix that does not match the artifact's spec_id.
// Test Spec: TS-NS-1. Requirement: NS-REQ-1.
func TestValidateCrossFile_PrefixMismatchRequirements(t *testing.T) {
	spec := &Spec{
		SpecID:        "01",
		SpecName:      "test",
		Title:         "Test",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			SpecId:        "01",
			SpecName:      "test",
			SchemaVersion: 1,
			Introduction:  "Test.",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements: []Requirement{
				{
					Id:    "02-REQ-1", // prefix '02' does not match spec_id '01'
					Title: "Mismatched prefix",
					UserStory: UserStory{
						Role: "dev", Goal: "test", Benefit: "validation",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "01-REQ-1.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "do something",
						},
					},
					EdgeCases: []Criterion{},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "01",
			SpecName:      "test",
			SchemaVersion: 1,
			TestCases: []TestCase{
				{
					Id:                  "TS-01-1",
					RequirementId:       "01-REQ-1.1",
					Kind:                "unit",
					Description:         "test",
					Preconditions:       []string{},
					Expected:            "pass",
					AssertionPseudocode: "assert true",
				},
			},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SpecId:        "01",
			SpecName:      "test",
			SchemaVersion: 1,
			TestCommands:  TestCommands{SpecTests: "test", AllTests: "test", Linter: "lint"},
			Dependencies:  []TaskDependency{},
			TaskGroups:    []TaskGroup{},
			Traceability:  []TraceabilityEntry{},
		},
	}

	result := spec.ValidateCrossFile()

	idFormatErrors := filterByCheck(result.Errors, "id_format")
	var found bool
	for _, e := range idFormatErrors {
		if strings.Contains(e.Message, "02") && strings.Contains(e.Message, "01") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected id_format error mentioning prefix '02' and spec_id '01', got errors: %v", idFormatErrors)
	}
}

// TestValidateCrossFile_PrefixMismatchTestSpec verifies that
// ValidateCrossFile returns an id_format error when a test case ID
// has a prefix that does not match the test_spec's spec_id.
// Test Spec: TS-NS-2. Requirement: NS-REQ-2.
func TestValidateCrossFile_PrefixMismatchTestSpec(t *testing.T) {
	spec := &Spec{
		SpecID:        "01",
		SpecName:      "test",
		Title:         "Test",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			SpecId:        "01",
			SpecName:      "test",
			SchemaVersion: 1,
			Introduction:  "Test.",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements: []Requirement{
				{
					Id:    "01-REQ-1",
					Title: "Req 1",
					UserStory: UserStory{
						Role: "dev", Goal: "test", Benefit: "validation",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "01-REQ-1.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "do something",
						},
					},
					EdgeCases: []Criterion{},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "01",
			SpecName:      "test",
			SchemaVersion: 1,
			TestCases: []TestCase{
				{
					Id:                  "TS-02-1", // prefix '02' does not match spec_id '01'
					RequirementId:       "01-REQ-1.1",
					Kind:                "unit",
					Description:         "test",
					Preconditions:       []string{},
					Expected:            "pass",
					AssertionPseudocode: "assert true",
				},
			},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SpecId:        "01",
			SpecName:      "test",
			SchemaVersion: 1,
			TestCommands:  TestCommands{SpecTests: "test", AllTests: "test", Linter: "lint"},
			Dependencies:  []TaskDependency{},
			TaskGroups:    []TaskGroup{},
			Traceability:  []TraceabilityEntry{},
		},
	}

	result := spec.ValidateCrossFile()

	idFormatErrors := filterByCheck(result.Errors, "id_format")
	var found bool
	for _, e := range idFormatErrors {
		if strings.Contains(e.Message, "TS-02-1") && strings.Contains(e.Message, "01") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected id_format error mentioning 'TS-02-1' and spec_id '01', got errors: %v", idFormatErrors)
	}
}

// TestValidateCrossFile_DuplicateRequirementIDs verifies that
// ValidateCrossFile returns an id_format error when two requirements
// share the same ID.
// Test Spec: TS-NS-3. Requirement: NS-REQ-3.
func TestValidateCrossFile_DuplicateRequirementIDs(t *testing.T) {
	spec := &Spec{
		SpecID:        "01",
		SpecName:      "test",
		Title:         "Test",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			SpecId:        "01",
			SpecName:      "test",
			SchemaVersion: 1,
			Introduction:  "Test.",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements: []Requirement{
				{
					Id:    "01-REQ-1",
					Title: "Req 1",
					UserStory: UserStory{
						Role: "dev", Goal: "test", Benefit: "validation",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "01-REQ-1.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "do something",
						},
					},
					EdgeCases: []Criterion{},
				},
				{
					Id:    "01-REQ-1", // duplicate
					Title: "Req 1 duplicate",
					UserStory: UserStory{
						Role: "dev", Goal: "test", Benefit: "validation",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "01-REQ-1.2",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "do another thing",
						},
					},
					EdgeCases: []Criterion{},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "01",
			SpecName:      "test",
			SchemaVersion: 1,
			TestCases: []TestCase{
				{
					Id: "TS-01-1", RequirementId: "01-REQ-1.1",
					Kind: "unit", Description: "test", Preconditions: []string{},
					Expected: "pass", AssertionPseudocode: "assert true",
				},
				{
					Id: "TS-01-2", RequirementId: "01-REQ-1.2",
					Kind: "unit", Description: "test2", Preconditions: []string{},
					Expected: "pass", AssertionPseudocode: "assert true",
				},
			},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SpecId:        "01",
			SpecName:      "test",
			SchemaVersion: 1,
			TestCommands:  TestCommands{SpecTests: "test", AllTests: "test", Linter: "lint"},
			Dependencies:  []TaskDependency{},
			TaskGroups:    []TaskGroup{},
			Traceability:  []TraceabilityEntry{},
		},
	}

	result := spec.ValidateCrossFile()

	idFormatErrors := filterByCheck(result.Errors, "id_format")
	var found bool
	for _, e := range idFormatErrors {
		if strings.Contains(strings.ToLower(e.Message), "duplicate") && strings.Contains(e.Message, "01-REQ-1") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected id_format error with 'duplicate' and '01-REQ-1', got errors: %v", idFormatErrors)
	}
}

// TestValidateCrossFile_DuplicateCriterionIDsPerRequirement verifies that
// duplicate criterion IDs are detected per-requirement (not across
// requirements). The same criterion ID in different requirements should NOT
// produce an error.
// Test Spec: TS-NS-4. Requirement: NS-REQ-4.
func TestValidateCrossFile_DuplicateCriterionIDsPerRequirement(t *testing.T) {
	spec := &Spec{
		SpecID:        "01",
		SpecName:      "test",
		Title:         "Test",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Test\n",
		Requirements: &RequirementsV1Json{
			SpecId:        "01",
			SpecName:      "test",
			SchemaVersion: 1,
			Introduction:  "Test.",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements: []Requirement{
				{
					Id:    "01-REQ-1",
					Title: "Req 1",
					UserStory: UserStory{
						Role: "dev", Goal: "test", Benefit: "validation",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "01-REQ-1.1",
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "do something",
						},
						{
							Id:          "01-REQ-1.1", // duplicate within same requirement
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "do something else",
						},
					},
					EdgeCases: []Criterion{},
				},
				{
					Id:    "01-REQ-2",
					Title: "Req 2",
					UserStory: UserStory{
						Role: "dev", Goal: "test", Benefit: "validation",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:          "01-REQ-1.1", // same ID but different requirement — NOT a duplicate
							EarsPattern: CriterionEarsPatternUbiquitous,
							System:      "the system",
							Action:      "do third thing",
						},
					},
					EdgeCases: []Criterion{},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "01",
			SpecName:      "test",
			SchemaVersion: 1,
			TestCases: []TestCase{
				{
					Id: "TS-01-1", RequirementId: "01-REQ-1.1",
					Kind: "unit", Description: "test", Preconditions: []string{},
					Expected: "pass", AssertionPseudocode: "assert true",
				},
			},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SpecId:        "01",
			SpecName:      "test",
			SchemaVersion: 1,
			TestCommands:  TestCommands{SpecTests: "test", AllTests: "test", Linter: "lint"},
			Dependencies:  []TaskDependency{},
			TaskGroups:    []TaskGroup{},
			Traceability:  []TraceabilityEntry{},
		},
	}

	result := spec.ValidateCrossFile()

	// Count duplicate id_format errors for '01-REQ-1.1'
	idFormatErrors := filterByCheck(result.Errors, "id_format")
	dupCount := 0
	for _, e := range idFormatErrors {
		if strings.Contains(strings.ToLower(e.Message), "duplicate") && strings.Contains(e.Message, "01-REQ-1.1") {
			dupCount++
		}
	}

	// Exactly one duplicate error: within the first requirement.
	// The same ID in the second requirement does NOT produce an error
	// because Python uses per-requirement seen sets.
	if dupCount != 1 {
		t.Errorf("expected exactly 1 duplicate id_format error for '01-REQ-1.1', got %d; errors: %v", dupCount, idFormatErrors)
	}
}

// TestValidateCrossFile_ValidIDsNoPrefixOrDuplicateErrors verifies that
// a fully valid spec with correct prefixes and unique IDs produces no
// id_format errors.
// Test Spec: TS-NS-5. Requirement: NS-REQ-5.
func TestValidateCrossFile_ValidIDsNoPrefixOrDuplicateErrors(t *testing.T) {
	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec returned unexpected error: %v", err)
	}

	result := spec.ValidateCrossFile()

	idFormatErrors := filterByCheck(result.Errors, "id_format")
	if len(idFormatErrors) != 0 {
		t.Errorf("expected no id_format errors on valid spec, got %d: %v", len(idFormatErrors), idFormatErrors)
	}
}

// TestExtractReqPrefix verifies the helper function for extracting
// spec_id prefixes from requirement-like IDs.
func TestExtractReqPrefix(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"01-REQ-1", "01"},
		{"abc-REQ-1.1", "abc"},
		{"01-PROP-1", "01"},
		{"01-PATH-1", "01"},
		{"01-ERR-1", "01"},
		{"myspec-REQ-1.E1", "myspec"},
		{"bad-format-no-marker", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got := extractReqPrefix(tt.id)
			if got != tt.want {
				t.Errorf("extractReqPrefix(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

// TestExtractTestPrefix verifies the helper function for extracting
// spec_id prefixes from test-like IDs.
func TestExtractTestPrefix(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"TS-01-1", "01"},
		{"TS-abc-1", "abc"},
		{"TS-01-SMOKE-1", "01"},
		{"TS-01-P1", "01"},
		{"TS-01-E1", "01"},
		{"not-a-ts-id", ""},
		{"TS-", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got := extractTestPrefix(tt.id)
			if got != tt.want {
				t.Errorf("extractTestPrefix(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Issue #41: task_groups minItems:1 constraint
// TS-NS-1, TS-NS-2, TS-NS-4
// ---------------------------------------------------------------------------

// TestValidateSchema_EmptyTaskGroups verifies AC-1/NS-REQ-1: schema validation
// returns an error for a tasks artifact with an empty task_groups array.
// Test Spec: TS-NS-1, Requirement: NS-REQ-1
func TestValidateSchema_EmptyTaskGroups(t *testing.T) {
	spec := buildSpecWithDanglingRef("01-REQ-1.1")
	// The base spec has empty task_groups — exercise schema rejection.
	if len(spec.Tasks.TaskGroups) != 0 {
		t.Skip("buildSpecWithDanglingRef already has task groups; test assumption changed")
	}

	result := spec.ValidateSchema()

	if result.Valid {
		t.Error("ValidateSchema().Valid = true for empty task_groups, want false")
	}

	found := false
	for _, e := range result.Errors {
		if e.Artifact == "tasks.json" && strings.Contains(e.Path, "task_groups") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected at least one error with Artifact=tasks.json and Path containing task_groups; got: %v", result.Errors)
	}
}

// TestValidateSchema_NonEmptyTaskGroups verifies AC-2/NS-REQ-2: schema
// validation passes when task_groups contains at least one task group.
// Test Spec: TS-NS-2, Requirement: NS-REQ-2
func TestValidateSchema_NonEmptyTaskGroups(t *testing.T) {
	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec returned unexpected error: %v", err)
	}
	if len(spec.Tasks.TaskGroups) == 0 {
		t.Fatal("testdata/valid_spec has no task groups — fix the fixture")
	}

	result := spec.ValidateSchema()

	taskGroupsErr := false
	for _, e := range result.Errors {
		if e.Artifact == "tasks.json" && strings.Contains(e.Path, "task_groups") {
			taskGroupsErr = true
			break
		}
	}
	if taskGroupsErr {
		t.Errorf("unexpected task_groups minItems error on valid spec: %v", result.Errors)
	}
}

// TestUnmarshalJSON_TaskGroups_MinItems verifies AC-4/NS-REQ-4: the generated
// Go UnmarshalJSON enforces the minItems:1 constraint on task_groups.
// Test Spec: TS-NS-4, Requirement: NS-REQ-4
func TestUnmarshalJSON_TaskGroups_MinItems(t *testing.T) {
	emptyGroups := `{
		"$schema": "https://agent-fox.dev/schemas/tasks.v1.json",
		"spec_id": "01",
		"spec_name": "test",
		"schema_version": 1,
		"test_commands": {"spec_tests": "go test", "all_tests": "go test", "linter": "go vet"},
		"dependencies": [],
		"task_groups": [],
		"traceability": []
	}`

	var tasks TasksV1Json
	err := json.Unmarshal([]byte(emptyGroups), &tasks)
	if err == nil {
		t.Error("json.Unmarshal with empty task_groups returned nil error, want non-nil")
	}
	if err != nil && !strings.Contains(err.Error(), "task_groups") {
		t.Errorf("error %q does not mention task_groups", err.Error())
	}
}

// TestUnmarshalJSON_TaskGroups_OneGroup verifies that UnmarshalJSON succeeds
// with a single task group (boundary condition for minItems:1).
// Test Spec: TS-NS-2 (boundary), Requirement: NS-REQ-2
func TestUnmarshalJSON_TaskGroups_OneGroup(t *testing.T) {
	oneGroup := `{
		"$schema": "https://agent-fox.dev/schemas/tasks.v1.json",
		"spec_id": "01",
		"spec_name": "test",
		"schema_version": 1,
		"test_commands": {"spec_tests": "go test", "all_tests": "go test", "linter": "go vet"},
		"dependencies": [],
		"task_groups": [
			{"id": 1, "kind": "tests", "title": "Group 1", "subtasks": [], "verification": {"id": "1.V", "checks": ["check"]}}
		],
		"traceability": []
	}`

	var tasks TasksV1Json
	if err := json.Unmarshal([]byte(oneGroup), &tasks); err != nil {
		t.Errorf("json.Unmarshal with one task_group returned unexpected error: %v", err)
	}
	if len(tasks.TaskGroups) != 1 {
		t.Errorf("TaskGroups length = %d, want 1", len(tasks.TaskGroups))
	}
}

// ---------------------------------------------------------------------------
// Issue #65: subtask and verification ID format validation in ValidateCrossFile
// Requirements: NS-REQ-1 through NS-REQ-5
// ---------------------------------------------------------------------------

// buildBaseSpecForIDFormat returns a minimal spec with group id=3 for use in
// subtask/verification ID format tests. It deliberately uses a group ID of 3
// to avoid conflating prefix "1" with a group 1 that already exists in
// buildCrossFileBaseSpec.
func buildBaseSpecForIDFormat(subtasks []Subtask, verID string) *Spec {
	s := buildCrossFileBaseSpec()
	// Replace the default two-group layout with a single group whose ID is 3,
	// plus the mandatory last group (wiring_verification with ID 4) so the
	// task_group_structure rule is also satisfied.
	s.Tasks.TaskGroups = []TaskGroup{
		{
			Id:       3,
			Kind:     TaskGroupKindTests,
			Title:    "Tests",
			Subtasks: subtasks,
			Verification: VerificationSubtask{
				Id:     verID,
				Checks: []string{"all tests pass"},
			},
		},
		{
			Id:    4,
			Kind:  TaskGroupKindWiringVerification,
			Title: "Wiring Verification",
			Subtasks: []Subtask{
				{
					Id: "4.1", Title: "Stub and dead-code audit", State: SubtaskStatePending,
					Details:         []string{"Verify all stubs removed"},
					RequirementRefs: []string{},
					TestSpecRefs:    []string{"TS-04-SMOKE-1"},
				},
			},
			Verification: VerificationSubtask{
				Id:     "4.V",
				Checks: []string{"smoke tests pass"},
			},
		},
	}
	return s
}

// TestValidateCrossFile_SubtaskIDFormat tests AC-1 through AC-5 for issue #65:
// subtask and verification subtask ID format validation in ValidateCrossFile.
// Requirements: NS-REQ-1, NS-REQ-2, NS-REQ-3, NS-REQ-4, NS-REQ-5.
func TestValidateCrossFile_SubtaskIDFormat(t *testing.T) {
	tests := []struct {
		name             string
		subtasks         []Subtask
		verID            string
		wantIDFormatErrs int      // expected count of id_format errors
		wantEntityIDs    []string // EntityID values expected in id_format errors
		wantNoEntityIDs  []string // EntityID values that must NOT appear in id_format errors
	}{
		{
			// AC-1 / NS-REQ-1: well-formed subtask "3.2" in group 3 passes.
			name: "well_formed_subtask_passes",
			subtasks: []Subtask{
				{Id: "3.2", Title: "Subtask", State: SubtaskStatePending, Details: []string{"detail"}, RequirementRefs: []string{}, TestSpecRefs: []string{}},
			},
			verID:            "3.V",
			wantIDFormatErrs: 0,
			wantNoEntityIDs:  []string{"3.2"},
		},
		{
			// AC-2 / NS-REQ-2: malformed subtask ID "banana" fails with id_format.
			name: "malformed_subtask_id_fails",
			subtasks: []Subtask{
				{Id: "banana", Title: "Subtask", State: SubtaskStatePending, Details: []string{"detail"}, RequirementRefs: []string{}, TestSpecRefs: []string{}},
			},
			verID:            "3.V",
			wantIDFormatErrs: 1,
			wantEntityIDs:    []string{"banana"},
		},
		{
			// AC-3 / NS-REQ-3: well-formed verification ID "3.V" passes.
			name: "well_formed_verification_id_passes",
			subtasks: []Subtask{
				{Id: "3.1", Title: "Subtask", State: SubtaskStatePending, Details: []string{"detail"}, RequirementRefs: []string{}, TestSpecRefs: []string{}},
			},
			verID:            "3.V",
			wantIDFormatErrs: 0,
			wantNoEntityIDs:  []string{"3.V"},
		},
		{
			// AC-4 / NS-REQ-4: verification ID "3.2" (numeric suffix) fails.
			name: "verification_id_with_numeric_suffix_fails",
			subtasks: []Subtask{
				{Id: "3.1", Title: "Subtask", State: SubtaskStatePending, Details: []string{"detail"}, RequirementRefs: []string{}, TestSpecRefs: []string{}},
			},
			verID:            "3.2",
			wantIDFormatErrs: 1,
			wantEntityIDs:    []string{"3.2"},
		},
		{
			// AC-5 / NS-REQ-5: subtask "5.1" in group 3 fails (prefix mismatch).
			name: "subtask_prefix_mismatch_fails",
			subtasks: []Subtask{
				{Id: "5.1", Title: "Subtask", State: SubtaskStatePending, Details: []string{"detail"}, RequirementRefs: []string{}, TestSpecRefs: []string{}},
			},
			verID:            "3.V",
			wantIDFormatErrs: 1,
			wantEntityIDs:    []string{"5.1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := buildBaseSpecForIDFormat(tt.subtasks, tt.verID)
			result := spec.ValidateCrossFile()

			idFormatErrs := filterByCheck(result.Errors, "id_format")

			// Filter to only those referencing tasks.json (to avoid noise from
			// any requirements/test_spec id_format errors that don't exist here
			// since the base spec has no requirements).
			var taskIDErrs []ValidationEntry
			for _, e := range idFormatErrs {
				if e.Artifact == "tasks.json" {
					taskIDErrs = append(taskIDErrs, e)
				}
			}

			if len(taskIDErrs) != tt.wantIDFormatErrs {
				t.Errorf("tasks.json id_format error count = %d, want %d; all id_format errors: %v",
					len(taskIDErrs), tt.wantIDFormatErrs, idFormatErrs)
			}

			// For failure cases, check category and artifact.
			for _, e := range taskIDErrs {
				if e.Category != "integrity" {
					t.Errorf("id_format error category = %q, want %q", e.Category, "integrity")
				}
				if e.Artifact != "tasks.json" {
					t.Errorf("id_format error artifact = %q, want %q", e.Artifact, "tasks.json")
				}
				if e.Message == "" {
					t.Error("id_format error message is empty")
				}
			}

			// Verify expected EntityIDs appear in errors.
			for _, wantID := range tt.wantEntityIDs {
				found := false
				for _, e := range taskIDErrs {
					if e.EntityID == wantID {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected EntityID %q in id_format errors, not found; errors: %v",
						wantID, taskIDErrs)
				}
			}

			// Verify absent EntityIDs do NOT appear in id_format errors.
			for _, absentID := range tt.wantNoEntityIDs {
				for _, e := range taskIDErrs {
					if e.EntityID == absentID {
						t.Errorf("EntityID %q should NOT appear in id_format errors, but found: %v",
							absentID, e)
					}
				}
			}

			// AC-2 specific: ValidateCrossFile returns Valid=false when there
			// are id_format errors.
			if tt.wantIDFormatErrs > 0 && result.Valid {
				t.Error("result.Valid = true, want false when id_format errors present")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TS-NS-1 / TS-NS-2 / TS-NS-3 / TS-NS-4 / TS-NS-5: Folder-name validation
// ---------------------------------------------------------------------------

// writeFolderNameSpecDir creates a complete, cross-file-consistent spec dir at
// dirPath. prdSpecID and prdSpecName go into prd.md; all JSON artifacts also
// use prdSpecID/prdSpecName so that no cross-file-7 error is emitted — the
// only errors we expect are from the folder-name check.
func writeFolderNameSpecDir(t *testing.T, dirPath, prdSpecID, prdSpecName string) {
	t.Helper()
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dirPath, err)
	}

	prd := fmt.Sprintf("---\nspec_id: %q\nspec_name: %q\ntitle: %q\n"+
		"status: \"draft\"\ncreated_at: \"2026-01-01T00:00:00Z\"\n"+
		"updated_at: \"2026-01-01T00:00:00Z\"\nowner: \"test\"\n"+
		"source: \"test\"\nsupersedes: []\nintent_hash: null\nschema_version: 1\n---\n# Test\n",
		prdSpecID, prdSpecName, prdSpecName)
	if err := os.WriteFile(filepath.Join(dirPath, "prd.md"), []byte(prd), 0o644); err != nil {
		t.Fatalf("WriteFile prd.md: %v", err)
	}

	req := fmt.Sprintf(
		`{"$schema":"https://agent-fox.dev/schemas/requirements.v1.json",`+
			`"spec_id":%q,"spec_name":%q,"schema_version":1,"introduction":"Test",`+
			`"glossary":{},"requirements":[],"correctness_properties":[],`+
			`"execution_paths":[],"error_handling":[]}`,
		prdSpecID, prdSpecName)
	if err := os.WriteFile(filepath.Join(dirPath, "requirements.json"), []byte(req), 0o644); err != nil {
		t.Fatalf("WriteFile requirements.json: %v", err)
	}

	ts := fmt.Sprintf(
		`{"$schema":"https://agent-fox.dev/schemas/test_spec.v1.json",`+
			`"spec_id":%q,"spec_name":%q,"schema_version":1,`+
			`"test_cases":[],"property_tests":[],"edge_case_tests":[],`+
			`"smoke_tests":[],"coverage":{}}`,
		prdSpecID, prdSpecName)
	if err := os.WriteFile(filepath.Join(dirPath, "test_spec.json"), []byte(ts), 0o644); err != nil {
		t.Fatalf("WriteFile test_spec.json: %v", err)
	}

	tasks := fmt.Sprintf(
		`{"$schema":"https://agent-fox.dev/schemas/tasks.v1.json",`+
			`"spec_id":%q,"spec_name":%q,"schema_version":1,`+
			`"test_commands":{"spec_tests":"go test","all_tests":"go test","linter":"go vet"},`+
			`"dependencies":[],"task_groups":[`+
			`{"id":1,"kind":"tests","title":"Tests","subtasks":[`+
			`{"id":"1.1","title":"Run tests","details":["Run all tests"],`+
			`"test_spec_refs":[],"requirement_refs":[],"state":"pending","optional":false}`+
			`],"verification":{"id":"1.V","checks":["verify"]}}],"traceability":[]}`,
		prdSpecID, prdSpecName)
	if err := os.WriteFile(filepath.Join(dirPath, "tasks.json"), []byte(tasks), 0o644); err != nil {
		t.Fatalf("WriteFile tasks.json: %v", err)
	}
}

// countFolderNameErrors returns the ValidationEntry values whose Check field
// is "folder_name".
func countFolderNameErrors(entries []ValidationEntry) []ValidationEntry {
	var out []ValidationEntry
	for _, e := range entries {
		if e.Check == "folder_name" {
			out = append(out, e)
		}
	}
	return out
}

// TS-NS-1: spec_id in prd.md frontmatter does not match the numeric folder prefix.
// Requirement: NS-REQ-1.1
func TestValidateCrossFile_FolderName_SpecIDMismatch(t *testing.T) {
	defer requireImplemented(t)

	base := t.TempDir()
	dirPath := filepath.Join(base, "05_my_feature")
	// Spec lives in 05_my_feature but prd.md declares spec_id "99".
	writeFolderNameSpecDir(t, dirPath, "99", "my_feature")

	spec, err := LoadSpec(dirPath)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	result := spec.ValidateCrossFile()

	folderErrs := countFolderNameErrors(result.Errors)
	if len(folderErrs) == 0 {
		t.Fatal("expected at least one folder_name error for spec_id mismatch, got none")
	}

	// The error must reference both the folder prefix and the frontmatter spec_id.
	found := false
	for _, e := range folderErrs {
		if strings.Contains(e.Message, "99") && strings.Contains(e.Message, "05") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("folder_name error should reference both '99' (spec_id) and '05' (folder prefix); errors: %v", folderErrs)
	}
}

// TS-NS-2: spec_name in prd.md frontmatter does not match the folder name suffix.
// Requirement: NS-REQ-2.1
func TestValidateCrossFile_FolderName_SpecNameMismatch(t *testing.T) {
	defer requireImplemented(t)

	base := t.TempDir()
	dirPath := filepath.Join(base, "05_my_feature")
	// Spec lives in 05_my_feature but prd.md declares spec_name "other_thing".
	writeFolderNameSpecDir(t, dirPath, "05", "other_thing")

	spec, err := LoadSpec(dirPath)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	result := spec.ValidateCrossFile()

	folderErrs := countFolderNameErrors(result.Errors)
	if len(folderErrs) == 0 {
		t.Fatal("expected at least one folder_name error for spec_name mismatch, got none")
	}

	// The error must reference both the folder suffix and the frontmatter spec_name.
	found := false
	for _, e := range folderErrs {
		if strings.Contains(e.Message, "other_thing") && strings.Contains(e.Message, "my_feature") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("folder_name error should reference both 'other_thing' (spec_name) and 'my_feature' (folder suffix); errors: %v", folderErrs)
	}
}

// TS-NS-3: A spec whose frontmatter spec_id and spec_name exactly match the folder
// prefix and suffix passes without folder-name errors.
// Requirement: NS-REQ-3.1
func TestValidateCrossFile_FolderName_MatchingSpec(t *testing.T) {
	defer requireImplemented(t)

	base := t.TempDir()
	dirPath := filepath.Join(base, "05_my_feature")
	// Spec lives in 05_my_feature and prd.md matches: spec_id "05", spec_name "my_feature".
	writeFolderNameSpecDir(t, dirPath, "05", "my_feature")

	spec, err := LoadSpec(dirPath)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	result := spec.ValidateCrossFile()

	folderErrs := countFolderNameErrors(result.Errors)
	if len(folderErrs) != 0 {
		t.Errorf("expected zero folder_name errors for matching spec, got: %v", folderErrs)
	}
}

// TS-NS-4: Folder-name validation is skipped when the directory name does not
// match the NN_snake_case pattern.
// Requirement: NS-REQ-4.1
func TestValidateCrossFile_FolderName_NonSpecDirectory(t *testing.T) {
	defer requireImplemented(t)

	base := t.TempDir()
	// "scratch" does not match the NN_snake_case pattern.
	dirPath := filepath.Join(base, "scratch")
	// Deliberately use mismatched values so that any folder-name check would fire.
	writeFolderNameSpecDir(t, dirPath, "99", "wrong_name")

	spec, err := LoadSpec(dirPath)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	result := spec.ValidateCrossFile()

	folderErrs := countFolderNameErrors(result.Errors)
	if len(folderErrs) != 0 {
		t.Errorf("expected zero folder_name errors for non-spec directory, got: %v", folderErrs)
	}
}

// TS-NS-5: Both spec_id and spec_name mismatches are reported independently.
// Requirement: NS-REQ-5.1
func TestValidateCrossFile_FolderName_BothMismatches(t *testing.T) {
	defer requireImplemented(t)

	base := t.TempDir()
	dirPath := filepath.Join(base, "05_my_feature")
	// Both spec_id and spec_name are wrong.
	writeFolderNameSpecDir(t, dirPath, "99", "other_thing")

	spec, err := LoadSpec(dirPath)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	result := spec.ValidateCrossFile()

	folderErrs := countFolderNameErrors(result.Errors)
	if len(folderErrs) != 2 {
		t.Errorf("expected exactly 2 folder_name errors (one for spec_id, one for spec_name), got %d: %v",
			len(folderErrs), folderErrs)
	}

	// One error must reference spec_id, another spec_name.
	hasSpecIDErr := false
	hasSpecNameErr := false
	for _, e := range folderErrs {
		if strings.Contains(e.Message, "spec_id") {
			hasSpecIDErr = true
		}
		if strings.Contains(e.Message, "spec_name") {
			hasSpecNameErr = true
		}
	}
	if !hasSpecIDErr {
		t.Errorf("no folder_name error mentions 'spec_id': %v", folderErrs)
	}
	if !hasSpecNameErr {
		t.Errorf("no folder_name error mentions 'spec_name': %v", folderErrs)
	}
}

// ---------------------------------------------------------------------------
// Task group ID minimum constraint tests (Issue #79)
// ---------------------------------------------------------------------------

// minimalValidTasksDoc returns a raw tasks document map with one task group
// using the given id, for use with validateArtifactSchema.
func minimalTasksDocWithGroupID(id any) map[string]any {
	return map[string]any{
		"$schema":        "https://agent-fox.dev/schemas/tasks.v1.json",
		"spec_id":        "01",
		"spec_name":      "test",
		"schema_version": 1,
		"test_commands": map[string]any{
			"spec_tests": "pytest -q",
			"all_tests":  "pytest -q",
			"linter":     "ruff check",
		},
		"dependencies": []any{},
		"task_groups": []any{
			map[string]any{
				"id":       id,
				"kind":     "tests",
				"title":    "Test group",
				"subtasks": []any{},
				"verification": map[string]any{
					"id":     "1.V",
					"checks": []any{"check"},
				},
			},
		},
		"traceability": []any{},
	}
}

// TestValidateSchema_TaskGroupIDZero verifies NS-REQ-1: a tasks artifact with
// task_group id=0 fails JSON schema validation with a minimum-constraint error.
// Test Spec: TS-NS-1, Requirement: NS-REQ-1
func TestValidateSchema_TaskGroupIDZero(t *testing.T) {
	artifact := minimalTasksDocWithGroupID(0)
	errs := validateArtifactSchema(artifact, "tasks.v1.json", "tasks.json")
	if len(errs) == 0 {
		t.Fatal("expected validation errors for task_group id=0, got none")
	}
	found := false
	for _, e := range errs {
		if e.Category == "schema" && strings.Contains(e.Path, "id") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected at least one schema error referencing 'id'; got: %v", errs)
	}
}

// TestValidateSchema_TaskGroupIDNegative verifies NS-REQ-2: a tasks artifact
// with task_group id=-5 fails JSON schema validation.
// Test Spec: TS-NS-2, Requirement: NS-REQ-2
func TestValidateSchema_TaskGroupIDNegative(t *testing.T) {
	artifact := minimalTasksDocWithGroupID(-5)
	errs := validateArtifactSchema(artifact, "tasks.v1.json", "tasks.json")
	if len(errs) == 0 {
		t.Fatal("expected validation errors for task_group id=-5, got none")
	}
	found := false
	for _, e := range errs {
		if e.Category == "schema" && strings.Contains(e.Path, "id") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected at least one schema error referencing 'id'; got: %v", errs)
	}
}

// TestValidateSchema_TaskGroupIDPositive verifies NS-REQ-3: a tasks artifact
// with a valid task_group id ≥ 1 passes schema validation without errors.
// Test Spec: TS-NS-3, Requirement: NS-REQ-3
func TestValidateSchema_TaskGroupIDPositive(t *testing.T) {
	for _, id := range []int{1, 2, 100} {
		artifact := minimalTasksDocWithGroupID(id)
		errs := validateArtifactSchema(artifact, "tasks.v1.json", "tasks.json")
		schemaErrs := []ValidationEntry{}
		for _, e := range errs {
			if e.Category == "schema" && strings.Contains(e.Path, "id") {
				schemaErrs = append(schemaErrs, e)
			}
		}
		if len(schemaErrs) > 0 {
			t.Errorf("id=%d: expected no schema id-constraint errors; got: %v", id, schemaErrs)
		}
	}
}
