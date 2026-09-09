package afspec

import (
	"fmt"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Wiring verification smoke tests (task group 10)
// TS-04-SMOKE-1, TS-04-SMOKE-2, TS-04-SMOKE-3
// ---------------------------------------------------------------------------

// buildAllRulesViolatingSpec constructs a *Spec that violates all 7 cross-file
// rules simultaneously:
//  1. Missing property_test coverage (cross_file_3)
//  2. Missing smoke_test coverage (cross_file_4)
//  3. Unresolvable test_spec_id (cross_file_5)
//  4. Undefined backtick term (cross_file_6)
//  5. Duplicate traceability pair (cross_file_8)
//  6. Unresolvable requirement_ref (cross_file_9)
//  7. Unwanted criterion without return_contract (cross_file_10)
func buildAllRulesViolatingSpec() *Spec {
	return &Spec{
		SpecID:        "04",
		SpecName:      "smoke_crossfile",
		Title:         "Smoke Cross-File Spec",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test-author",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# Smoke\n",
		Requirements: &RequirementsV1Json{
			SpecId:        "04",
			SpecName:      "smoke_crossfile",
			SchemaVersion: 1,
			Introduction:  "Smoke test spec.",
			Glossary:      RequirementsV1JsonGlossary{}, // empty glossary => backtick terms undefined
			Requirements: []Requirement{
				{
					Id:    "04-REQ-1",
					Title: "Smoke Requirement",
					UserStory: UserStory{
						Role:    "author",
						Goal:    "validate",
						Benefit: "coverage",
					},
					AcceptanceCriteria: []Criterion{
						{
							Id:             "04-REQ-1.1",
							EarsPattern:    CriterionEarsPatternUbiquitous,
							System:         "the system",
							Action:         "call `UnknownTerm` and return result", // backtick term not in glossary => cross_file_6
							ReturnContract: nil,
						},
						{
							Id:             "04-REQ-1.2",
							EarsPattern:    CriterionEarsPatternUnwanted,
							System:         "the system",
							Action:         "reject the invalid input",
							ReturnContract: nil, // unwanted + nil return_contract => cross_file_10
						},
					},
					EdgeCases: []Criterion{},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{
				{
					Id:        "04-PROP-1",
					ForAny:    "any input",
					Invariant: "output is valid",
					Validates: []string{"04-REQ-1.1"},
				},
			}, // no matching property_test => cross_file_3
			ExecutionPaths: []ExecutionPath{
				{
					Id:    "04-PATH-1",
					Title: "Smoke Path",
					Steps: []PathStep{
						{Actor: "CLI", Action: "call"},
						{Actor: "System", Action: "respond"},
					},
				},
			}, // no matching smoke_test => cross_file_4
			ErrorHandling: []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			SpecId:        "04",
			SpecName:      "smoke_crossfile",
			SchemaVersion: 1,
			TestCases: []TestCase{
				{
					Id:                  "TS-04-1",
					RequirementId:       "04-REQ-1.1",
					Kind:                "unit",
					Description:         "basic test",
					Preconditions:       []string{},
					Expected:            "pass",
					AssertionPseudocode: "assert true",
				},
			},
			PropertyTests: []PropertyTest{}, // no property tests => cross_file_3 triggered
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{}, // no smoke tests => cross_file_4 triggered
			Coverage:      Coverage{},
		},
		Tasks: &TasksV1Json{
			SpecId:        "04",
			SpecName:      "smoke_crossfile",
			SchemaVersion: 1,
			TestCommands: TestCommands{
				SpecTests: "go test ./...",
				AllTests:  "go test ./...",
				Linter:    "go vet ./...",
			},
			Dependencies: []TaskDependency{},
			TaskGroups: []TaskGroup{
				{
					Id: 1, Title: "Tests", Kind: TaskGroupKindTests,
					Subtasks: []Subtask{
						{
							Id: "1.1", Title: "subtask A", State: SubtaskStatePending,
							Details:         []string{},
							TestSpecRefs:    []string{"TS-04-NONEXISTENT"}, // unresolvable test_spec_id => cross_file_5
							RequirementRefs: []string{"04-REQ-999"},        // unresolvable requirement_ref => cross_file_9
						},
					},
				},
			},
			Traceability: []TraceabilityEntry{
				{RequirementId: "04-REQ-1.1", TestSpecId: "TS-04-1", TaskId: "1.1", TestPath: TraceabilityEntryTestPath(strPtr("x_test.go"))},
				{RequirementId: "04-REQ-1.1", TestSpecId: "TS-04-1", TaskId: "1.1", TestPath: TraceabilityEntryTestPath(strPtr("x_test.go"))}, // duplicate => cross_file_8
			},
		},
	}
}

// TestSmoke_FullCrossFileValidation exercises ValidateCrossFile on a Spec
// that violates all 7 cross-file rules simultaneously, verifying the combined
// ValidationEntry slice contains errors from each rule.
// Test Spec: TS-04-SMOKE-1. Execution Path: 04-PATH-1.
// Requirements: 04-REQ-1 through 04-REQ-9.
func TestSmoke_FullCrossFileValidation(t *testing.T) {
	spec := buildAllRulesViolatingSpec()
	result := spec.ValidateCrossFile()

	// Should not panic and should return errors.
	if result.Valid {
		t.Fatal("ValidateCrossFile on all-violating spec returned Valid=true, want false")
	}

	// Expect at least 7 entries (one per violated rule).
	if len(result.Errors) < 7 {
		t.Errorf("ValidateCrossFile returned %d errors, want at least 7; errors: %v", len(result.Errors), result.Errors)
	}

	// Verify each rule contributed at least one error.
	checkIDs := []string{
		"cross_file_3",  // property test coverage
		"cross_file_4",  // smoke test coverage
		"cross_file_5",  // test_spec_id resolution
		"cross_file_6",  // glossary backtick term
		"cross_file_8",  // traceability deduplication
		"cross_file_9",  // requirement_refs resolution
		"cross_file_10", // unwanted return_contract
	}
	for _, check := range checkIDs {
		found := false
		for _, e := range result.Errors {
			if e.Check == check {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no error with Check=%q found in result; all checks: %v", check, smokeChecksFromEntries(result.Errors))
		}
	}

	// Verify all cross-file rule errors have Category='integrity'.
	for _, e := range result.Errors {
		isRuleError := false
		for _, check := range checkIDs {
			if e.Check == check {
				isRuleError = true
				break
			}
		}
		if isRuleError && e.Category != "integrity" {
			t.Errorf("error with Check=%q has Category=%q, want 'integrity'", e.Check, e.Category)
		}
	}

	// Verify specific content expectations from the test spec.
	smokeAssertReqID(t, result.Errors, "04-PROP-1", "uncovered correctness_property")
	smokeAssertMsgContains(t, result.Errors, "04-PATH-1", "uncovered execution_path_id")
	smokeAssertMsgContains(t, result.Errors, "TS-04-NONEXISTENT", "unresolvable test_spec_id")
	smokeAssertMsgContains(t, result.Errors, "UnknownTerm", "undefined backtick term")
	smokeAssertMsgContains(t, result.Errors, "04-REQ-1.1", "duplicate traceability pair")
	smokeAssertMsgContains(t, result.Errors, "04-REQ-999", "unresolvable requirement_ref")
	smokeAssertMsgContains(t, result.Errors, "04-REQ-1.2", "unwanted criterion missing return_contract")
}

// TestSmoke_ValidateStructuredOutputShape exercises ValidateStructured on a
// Spec with at least one integrity error and at least one warning, verifying
// the structured map has correct keys, field shapes, and valid=false.
// Test Spec: TS-04-SMOKE-2. Execution Path: 04-PATH-2.
// Requirements: 04-REQ-20.1, 04-REQ-20.2, 04-REQ-20.3.
func TestSmoke_ValidateStructuredOutputShape(t *testing.T) {
	// Build a spec with:
	// - An integrity error (uncovered correctness_property => cross_file_3)
	// - A warning (more than 10 requirements => scope limit)
	spec := makeMinimalSpec()
	spec.Requirements.CorrectnessProperties = []CorrectnessProperty{
		{Id: "04-PROP-1", ForAny: "any input", Invariant: "holds", Validates: []string{"04-REQ-1.1"}},
	}
	// No property tests => triggers cross_file_3 integrity error.

	// Add 11 requirements to trigger scope limit warning (04-REQ-19).
	for i := 1; i <= 11; i++ {
		spec.Requirements.Requirements = append(spec.Requirements.Requirements, Requirement{
			Id:    fmt.Sprintf("04-REQ-%d", i),
			Title: fmt.Sprintf("Requirement %d", i),
			UserStory: UserStory{
				Role:    "author",
				Goal:    "test",
				Benefit: "coverage",
			},
			AcceptanceCriteria: []Criterion{
				{
					Id:             fmt.Sprintf("04-REQ-%d.1", i),
					EarsPattern:    CriterionEarsPatternUbiquitous,
					System:         "the system",
					Action:         "do something",
					ReturnContract: nil,
				},
			},
			EdgeCases: []Criterion{},
		})
	}

	output := spec.ValidateStructured()

	// Must be a non-nil map.
	if output == nil {
		t.Fatal("ValidateStructured returned nil")
	}

	// 'valid' must be false (we have integrity errors).
	valid, ok := output["valid"].(bool)
	if !ok {
		t.Fatalf("output['valid'] is not bool, got %T", output["valid"])
	}
	if valid {
		t.Error("output['valid'] is true, want false (spec has integrity errors)")
	}

	// 'errors' must be a non-empty slice of map[string]any.
	errSlice, ok := output["errors"].([]map[string]any)
	if !ok {
		t.Fatalf("output['errors'] is not []map[string]any, got %T", output["errors"])
	}
	if len(errSlice) == 0 {
		t.Error("output['errors'] is empty, want at least one error entry")
	}

	// Each error map must contain 'category' and 'message'.
	// Schema errors additionally have 'artifact'; integrity errors have 'check'.
	for i, entry := range errSlice {
		if _, exists := entry["category"]; !exists {
			t.Errorf("error[%d] missing key 'category'", i)
		}
		if _, exists := entry["message"]; !exists {
			t.Errorf("error[%d] missing key 'message'", i)
		}
		cat, _ := entry["category"].(string)
		if cat == "schema" {
			if _, exists := entry["artifact"]; !exists {
				t.Errorf("error[%d] schema error missing 'artifact' key", i)
			}
		} else if cat == "integrity" {
			if _, exists := entry["check"]; !exists {
				t.Errorf("error[%d] integrity error missing 'check' key", i)
			}
		}
	}

	// Integrity errors must have category='integrity'.
	hasIntegrity := false
	for _, entry := range errSlice {
		if entry["category"] == "integrity" {
			hasIntegrity = true
			break
		}
	}
	if !hasIntegrity {
		t.Error("no error with category='integrity' found")
	}

	// 'warnings' must be a non-empty slice of map[string]any.
	warnSlice, ok := output["warnings"].([]map[string]any)
	if !ok {
		t.Fatalf("output['warnings'] is not []map[string]any, got %T", output["warnings"])
	}
	if len(warnSlice) == 0 {
		t.Error("output['warnings'] is empty, want at least one warning entry")
	}

	// Each warning map must contain 'category'="warning", 'message', and 'entity_id'.
	for i, entry := range warnSlice {
		if cat, _ := entry["category"].(string); cat != "warning" {
			t.Errorf("warning[%d] category = %q, want 'warning'", i, cat)
		}
		for _, key := range []string{"message", "entity_id"} {
			if _, exists := entry[key]; !exists {
				t.Errorf("warning[%d] missing key %q", i, key)
			}
		}
	}
}

// TestSmoke_CrossSpecAPIAndDependency exercises ValidateCrossSpec with specs
// that have a mismatched API symbol signature and an unknown dependency
// reference, verifying combined integrity errors are returned.
// Test Spec: TS-04-SMOKE-3. Execution Path: 04-PATH-3.
// Requirements: 04-REQ-10.1, 04-REQ-11.1.
func TestSmoke_CrossSpecAPIAndDependency(t *testing.T) {
	specA := &Spec{
		SpecID:        "specA",
		SpecName:      "spec_a",
		Title:         "Spec A",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# A\n",
		Requirements: &RequirementsV1Json{
			SpecId:        "specA",
			SpecName:      "spec_a",
			SchemaVersion: 1,
			Introduction:  "Spec A.",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements:  []Requirement{},
			ExternalApis: []ExternalApi{
				{
					Package: "pkg",
					Version: "v1",
					Symbols: []ExternalApiSymbol{
						{Name: "Foo", Signature: "func Foo() int", ImportPath: "pkg"},
					},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		Tasks: &TasksV1Json{
			SpecId:        "specA",
			SpecName:      "spec_a",
			SchemaVersion: 1,
			TestCommands: TestCommands{
				SpecTests: "go test ./...",
				AllTests:  "go test ./...",
				Linter:    "go vet ./...",
			},
			Dependencies: []TaskDependency{
				{DependsOnSpec: "specZ", FromGroup: 1, ToGroup: 1, Relationship: "blocks"}, // unknown spec
			},
			TaskGroups:   []TaskGroup{},
			Traceability: []TraceabilityEntry{},
		},
	}

	specB := &Spec{
		SpecID:        "specB",
		SpecName:      "spec_b",
		Title:         "Spec B",
		Status:        "draft",
		CreatedAt:     "2026-01-01T00:00:00Z",
		UpdatedAt:     "2026-01-01T00:00:00Z",
		Owner:         "test",
		Source:        "https://example.com",
		SchemaVersion: 1,
		PRDBody:       "# B\n",
		Requirements: &RequirementsV1Json{
			SpecId:        "specB",
			SpecName:      "spec_b",
			SchemaVersion: 1,
			Introduction:  "Spec B.",
			Glossary:      RequirementsV1JsonGlossary{},
			Requirements:  []Requirement{},
			ExternalApis: []ExternalApi{
				{
					Package: "pkg",
					Version: "v1",
					Symbols: []ExternalApiSymbol{
						{Name: "Foo", Signature: "func Foo() string", ImportPath: "pkg"}, // mismatched!
					},
				},
			},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		Tasks: &TasksV1Json{
			SpecId:        "specB",
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

	graph := &DependencyGraph{
		Edges: []DependencyEdge{
			{FromSpec: "specA", ToSpec: "specB", Relationship: "blocks"},
		},
	}

	result := ValidateCrossSpec([]*Spec{specA, specB}, graph)

	// Should not panic.
	if result.Valid {
		t.Fatal("ValidateCrossSpec returned Valid=true, want false")
	}

	// Expect at least 2 entries: API mismatch + unknown dependency.
	if len(result.Errors) < 2 {
		t.Errorf("ValidateCrossSpec returned %d errors, want at least 2; errors: %v", len(result.Errors), result.Errors)
	}

	// Verify API symbol mismatch error (cross_spec_1).
	hasMismatch := false
	for _, e := range result.Errors {
		if e.Check == "cross_spec_1" && strings.Contains(e.Message, "Foo") {
			hasMismatch = true
			break
		}
	}
	if !hasMismatch {
		t.Errorf("no error with Check='cross_spec_1' mentioning 'Foo' found; errors: %v", result.Errors)
	}

	// Verify unknown dependency error (cross_spec_3).
	hasUnknown := false
	for _, e := range result.Errors {
		if e.Check == "cross_spec_3" && strings.Contains(e.Message, "specZ") {
			hasUnknown = true
			break
		}
	}
	if !hasUnknown {
		t.Errorf("no error with Check='cross_spec_3' mentioning 'specZ' found; errors: %v", result.Errors)
	}

	// All errors must have Category='integrity'.
	for _, e := range result.Errors {
		if e.Category != "integrity" {
			t.Errorf("error with Check=%q has Category=%q, want 'integrity'", e.Check, e.Category)
		}
	}
}

// --- Smoke test helper functions ---

// smokeChecksFromEntries extracts unique Check values from a slice of
// ValidationEntry.
func smokeChecksFromEntries(entries []ValidationEntry) []string {
	seen := map[string]bool{}
	var checks []string
	for _, e := range entries {
		if e.Check != "" && !seen[e.Check] {
			seen[e.Check] = true
			checks = append(checks, e.Check)
		}
	}
	return checks
}

// smokeAssertReqID verifies at least one entry has a RequirementID matching
// the target string.
func smokeAssertReqID(t *testing.T, entries []ValidationEntry, target, desc string) {
	t.Helper()
	for _, e := range entries {
		if e.RequirementID == target {
			return
		}
	}
	t.Errorf("no ValidationEntry with RequirementID=%q found (%s)", target, desc)
}

// smokeAssertMsgContains verifies at least one entry's Message contains the
// target substring.
func smokeAssertMsgContains(t *testing.T, entries []ValidationEntry, target, desc string) {
	t.Helper()
	for _, e := range entries {
		if strings.Contains(e.Message, target) {
			return
		}
	}
	t.Errorf("no ValidationEntry Message containing %q found (%s)", target, desc)
}
