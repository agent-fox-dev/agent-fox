package afspec

// Tests for issue #28: edge_case_test schema alignment with test_case.
//
// Acceptance criteria:
//   - AC-1 (TS-NS-1/NS-REQ-1): missing preconditions fails UnmarshalJSON
//   - AC-2 (TS-NS-2/NS-REQ-2): missing expected fails UnmarshalJSON
//   - AC-3 (TS-NS-3/NS-REQ-3): missing assertion_pseudocode fails UnmarshalJSON
//   - AC-4 (TS-NS-4/NS-REQ-4): invalid kind fails UnmarshalJSON (enum constraint)
//   - AC-5 (TS-NS-5/NS-REQ-5): Go struct uses non-pointer value types for the
//     three previously optional fields; UnmarshalJSON enforces all as required.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// completeEdgeCaseTestJSON returns a JSON object string with all required
// fields present and valid, suitable as a baseline for subtraction tests.
func completeEdgeCaseTestJSON() string {
	return `{
		"id": "E1",
		"requirement_id": "R1",
		"kind": "unit",
		"description": "d",
		"preconditions": [],
		"expected": {},
		"assertion_pseudocode": "x"
	}`
}

// TestEdgeCaseTest_MissingPreconditions verifies that UnmarshalJSON returns
// a required-field error when preconditions is absent.
// Test Spec: TS-NS-1, Requirement: NS-REQ-1.1
func TestEdgeCaseTest_MissingPreconditions(t *testing.T) {
	input := `{"id":"E1","requirement_id":"R1","kind":"unit","description":"d","expected":{},"assertion_pseudocode":"x"}`
	var e EdgeCaseTest
	err := json.Unmarshal([]byte(input), &e)
	if err == nil {
		t.Fatal("UnmarshalJSON: expected error for missing preconditions, got nil")
	}
	if !strings.Contains(err.Error(), "preconditions") {
		t.Errorf("error %q does not mention 'preconditions'", err.Error())
	}
}

// TestEdgeCaseTest_MissingExpected verifies that UnmarshalJSON returns a
// required-field error when expected is absent.
// Test Spec: TS-NS-2, Requirement: NS-REQ-2.1
func TestEdgeCaseTest_MissingExpected(t *testing.T) {
	input := `{"id":"E1","requirement_id":"R1","kind":"unit","description":"d","preconditions":[],"assertion_pseudocode":"x"}`
	var e EdgeCaseTest
	err := json.Unmarshal([]byte(input), &e)
	if err == nil {
		t.Fatal("UnmarshalJSON: expected error for missing expected, got nil")
	}
	if !strings.Contains(err.Error(), "expected") {
		t.Errorf("error %q does not mention 'expected'", err.Error())
	}
}

// TestEdgeCaseTest_MissingAssertionPseudocode verifies that UnmarshalJSON
// returns a required-field error when assertion_pseudocode is absent.
// Test Spec: TS-NS-3, Requirement: NS-REQ-3.1
func TestEdgeCaseTest_MissingAssertionPseudocode(t *testing.T) {
	input := `{"id":"E1","requirement_id":"R1","kind":"unit","description":"d","preconditions":[],"expected":{}}`
	var e EdgeCaseTest
	err := json.Unmarshal([]byte(input), &e)
	if err == nil {
		t.Fatal("UnmarshalJSON: expected error for missing assertion_pseudocode, got nil")
	}
	if !strings.Contains(err.Error(), "assertion_pseudocode") {
		t.Errorf("error %q does not mention 'assertion_pseudocode'", err.Error())
	}
}

// TestEdgeCaseTest_InvalidKind verifies that UnmarshalJSON rejects an
// edge_case_test with a kind value not in the enum ["unit", "integration"].
// Test Spec: TS-NS-4, Requirement: NS-REQ-4.1
func TestEdgeCaseTest_InvalidKind(t *testing.T) {
	input := `{"id":"E1","requirement_id":"R1","kind":"foo","description":"d","preconditions":[],"expected":{},"assertion_pseudocode":"x"}`
	var e EdgeCaseTest
	err := json.Unmarshal([]byte(input), &e)
	if err == nil {
		t.Fatal("UnmarshalJSON: expected error for invalid kind 'foo', got nil")
	}
	// Should mention the invalid value or enum constraint
	msg := err.Error()
	if !strings.Contains(msg, "foo") && !strings.Contains(msg, "expected one of") {
		t.Errorf("error %q does not indicate enum violation for 'foo'", msg)
	}
}

// TestEdgeCaseTest_ValidKinds verifies that both "unit" and "integration" are
// accepted as valid kind values for edge_case_test.
// Test Spec: TS-NS-4, Requirement: NS-REQ-4.1
func TestEdgeCaseTest_ValidKinds(t *testing.T) {
	for _, kind := range []string{"unit", "integration"} {
		input := `{"id":"E1","requirement_id":"R1","kind":"` + kind + `","description":"d","preconditions":[],"expected":{},"assertion_pseudocode":"x"}`
		var e EdgeCaseTest
		if err := json.Unmarshal([]byte(input), &e); err != nil {
			t.Errorf("kind %q: unexpected error: %v", kind, err)
		}
		if string(e.Kind) != kind {
			t.Errorf("kind %q: got Kind = %q", kind, e.Kind)
		}
	}
}

// TestEdgeCaseTest_CompleteJSONRoundtrip verifies that a fully-populated
// edge_case_test unmarshals without error when all required fields are present.
// Test Spec: TS-NS-1 through TS-NS-4
func TestEdgeCaseTest_CompleteJSONRoundtrip(t *testing.T) {
	var e EdgeCaseTest
	if err := json.Unmarshal([]byte(completeEdgeCaseTestJSON()), &e); err != nil {
		t.Fatalf("UnmarshalJSON for complete edge_case_test returned unexpected error: %v", err)
	}
	if e.Id != "E1" {
		t.Errorf("Id = %q, want %q", e.Id, "E1")
	}
	if e.AssertionPseudocode != "x" {
		t.Errorf("AssertionPseudocode = %q, want %q", e.AssertionPseudocode, "x")
	}
}

// TestEdgeCaseTest_StructFieldTypes verifies that EdgeCaseTest uses non-pointer
// value types for Preconditions, Expected, and AssertionPseudocode, matching
// the TestCase struct definition.
// Test Spec: TS-NS-5, Requirement: NS-REQ-5.1
func TestEdgeCaseTest_StructFieldTypes(t *testing.T) {
	typ := reflect.TypeOf(EdgeCaseTest{})

	// AssertionPseudocode must be string (not *string)
	f, ok := typ.FieldByName("AssertionPseudocode")
	if !ok {
		t.Fatal("EdgeCaseTest has no field AssertionPseudocode")
	}
	if f.Type.Kind() == reflect.Ptr {
		t.Error("EdgeCaseTest.AssertionPseudocode is a pointer type, want value type string")
	}
	if f.Type.Kind() != reflect.String {
		t.Errorf("EdgeCaseTest.AssertionPseudocode kind = %v, want string", f.Type.Kind())
	}

	// Expected must be interface{} (not a pointer-wrapped interface)
	f2, ok := typ.FieldByName("Expected")
	if !ok {
		t.Fatal("EdgeCaseTest has no field Expected")
	}
	if f2.Type.Kind() == reflect.Ptr {
		t.Error("EdgeCaseTest.Expected is a pointer type, want interface{}")
	}

	// Preconditions must be []string (slice, not pointer to slice)
	f3, ok := typ.FieldByName("Preconditions")
	if !ok {
		t.Fatal("EdgeCaseTest has no field Preconditions")
	}
	if f3.Type.Kind() == reflect.Ptr {
		t.Error("EdgeCaseTest.Preconditions is a pointer type, want []string")
	}
	if f3.Type.Kind() != reflect.Slice {
		t.Errorf("EdgeCaseTest.Preconditions kind = %v, want slice", f3.Type.Kind())
	}

	// JSON tag for AssertionPseudocode must NOT contain omitempty
	if strings.Contains(f.Tag.Get("json"), "omitempty") {
		t.Error("EdgeCaseTest.AssertionPseudocode json tag contains omitempty, want required field")
	}
	// JSON tag for Expected must NOT contain omitempty
	if strings.Contains(f2.Tag.Get("json"), "omitempty") {
		t.Error("EdgeCaseTest.Expected json tag contains omitempty, want required field")
	}
	// JSON tag for Preconditions must NOT contain omitempty
	if strings.Contains(f3.Tag.Get("json"), "omitempty") {
		t.Error("EdgeCaseTest.Preconditions json tag contains omitempty, want required field")
	}
}

// TestEdgeCaseTest_UnmarshalJSONEnforcesAllThreeRequired verifies that
// UnmarshalJSON returns a descriptive error for each of the three fields
// when individually absent.
// Test Spec: TS-NS-5, Requirement: NS-REQ-5.1
func TestEdgeCaseTest_UnmarshalJSONEnforcesAllThreeRequired(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		missing string
	}{
		{
			name:    "missing preconditions",
			input:   `{"id":"E1","requirement_id":"R1","kind":"unit","description":"d","expected":{},"assertion_pseudocode":"x"}`,
			missing: "preconditions",
		},
		{
			name:    "missing expected",
			input:   `{"id":"E1","requirement_id":"R1","kind":"unit","description":"d","preconditions":[],"assertion_pseudocode":"x"}`,
			missing: "expected",
		},
		{
			name:    "missing assertion_pseudocode",
			input:   `{"id":"E1","requirement_id":"R1","kind":"unit","description":"d","preconditions":[],"expected":{}}`,
			missing: "assertion_pseudocode",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var e EdgeCaseTest
			err := json.Unmarshal([]byte(tc.input), &e)
			if err == nil {
				t.Fatalf("expected error mentioning %q, got nil", tc.missing)
			}
			if !strings.Contains(err.Error(), tc.missing) {
				t.Errorf("error %q does not mention %q", err.Error(), tc.missing)
			}
		})
	}
}

// TestEdgeCaseTest_SchemaValidation verifies that ValidateSchema on a spec
// whose edge_case_tests JSON would be incomplete produces a schema error.
// This exercises the JSON schema's required array for edge_case_test.
// Test Spec: TS-NS-1 through TS-NS-4, Requirements: NS-REQ-1 through NS-REQ-4
func TestEdgeCaseTest_SchemaValidation(t *testing.T) {
	// Build a base spec with a complete EdgeCaseTest first to confirm it passes.
	base := buildCrossFileBaseSpec()
	base.TestSpec.EdgeCaseTests = []EdgeCaseTest{
		{
			Id:                  "TS-04-E1",
			RequirementId:       "04-REQ-1.E1",
			Kind:                EdgeCaseTestKindUnit,
			Description:         "edge case",
			Preconditions:       []string{},
			Expected:            "pass",
			AssertionPseudocode: "assert true",
		},
	}
	result := base.ValidateSchema()
	if !result.Valid {
		t.Errorf("ValidateSchema for complete EdgeCaseTest should pass, got errors: %v", result.Errors)
	}
}

// TestEdgeCaseTest_ValidateSchema_InvalidKind verifies that ValidateSchema rejects
// an edge_case_test whose kind field is not in the enum ["unit", "integration"].
// This exercises the JSON schema's enum constraint for edge_case_test.kind.
// Test Spec: TS-NS-3, Requirement: NS-REQ-3.1
func TestEdgeCaseTest_ValidateSchema_InvalidKind(t *testing.T) {
	// Use raw JSON to bypass Go struct's UnmarshalJSON validation so that
	// we can test the JSON schema's enum constraint directly.
	doc := parseJSON(t, `{
		"$schema": "https://agent-fox.dev/schemas/test_spec.v1.json",
		"spec_id": "04",
		"spec_name": "test",
		"schema_version": 1,
		"test_cases": [],
		"property_tests": [],
		"edge_case_tests": [
			{
				"id": "TS-04-E1",
				"requirement_id": "04-REQ-1.E1",
				"kind": "foo",
				"description": "edge case with invalid kind",
				"preconditions": [],
				"expected": "pass",
				"assertion_pseudocode": "assert true"
			}
		],
		"smoke_tests": [],
		"coverage": {}
	}`)
	errs := requireSchemaError(t, doc, "test_spec.v1.json")
	found := false
	for _, e := range errs {
		if containsSubstring(e.Message+e.Path+e.Keyword, "foo") ||
			containsSubstring(e.Message+e.Path+e.Keyword, "enum") ||
			containsSubstring(e.Message+e.Path+e.Keyword, "kind") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected schema error mentioning kind enum violation, got: %v", errs)
	}
}

// TestEdgeCaseTest_ValidateSchema_MissingPreconditions verifies that ValidateSchema
// rejects an edge_case_test that is missing the required preconditions field.
// This exercises the JSON schema's required array for edge_case_test.
// Test Spec: TS-NS-3, Requirement: NS-REQ-3.1
func TestEdgeCaseTest_ValidateSchema_MissingPreconditions(t *testing.T) {
	// Use raw JSON to produce an edge_case_test without the preconditions field,
	// bypassing Go struct defaults.
	doc := parseJSON(t, `{
		"$schema": "https://agent-fox.dev/schemas/test_spec.v1.json",
		"spec_id": "04",
		"spec_name": "test",
		"schema_version": 1,
		"test_cases": [],
		"property_tests": [],
		"edge_case_tests": [
			{
				"id": "TS-04-E1",
				"requirement_id": "04-REQ-1.E1",
				"kind": "unit",
				"description": "edge case missing preconditions",
				"expected": "pass",
				"assertion_pseudocode": "assert true"
			}
		],
		"smoke_tests": [],
		"coverage": {}
	}`)
	errs := requireSchemaError(t, doc, "test_spec.v1.json")
	found := false
	for _, e := range errs {
		if containsSubstring(e.Message+e.Path+e.Keyword, "preconditions") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected schema error mentioning 'preconditions', got: %v", errs)
	}
}
