package afspec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestMarshalJSON_MapKeysAlphabetical verifies that MarshalJSON sorts
// all map[string]T keys alphabetically in the output.
// Test Spec: TS-01-6, Requirement: 01-REQ-3.1
func TestMarshalJSON_MapKeysAlphabetical(t *testing.T) {
	defer requireImplemented(t)

	// Create a Requirements struct with glossary keys in non-alphabetical order.
	// Go maps don't guarantee insertion order, but we verify the output is sorted.
	req := &RequirementsV1Json{
		SpecId:        "01",
		SpecName:      "test",
		SchemaVersion: 1,
		Introduction:  "Test introduction.",
		Glossary: RequirementsV1JsonGlossary{
			"zebra": "Last animal",
			"apple": "A fruit",
			"mango": "Another fruit",
		},
		Requirements:          []Requirement{},
		CorrectnessProperties: []CorrectnessProperty{},
		ExecutionPaths:        []ExecutionPath{},
		ErrorHandling:         []ErrorHandlingEntry{},
	}

	data, err := MarshalJSON(req)
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}

	// Parse back and check key order in the raw JSON
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	// Extract glossary keys from the JSON output
	glossaryRaw, ok := raw["glossary"]
	if !ok {
		t.Fatal("output missing 'glossary' field")
	}

	// Verify keys are in alphabetical order by checking string positions
	glossaryStr := string(glossaryRaw)
	appleIdx := indexOf(glossaryStr, "apple")
	mangoIdx := indexOf(glossaryStr, "mango")
	zebraIdx := indexOf(glossaryStr, "zebra")

	if appleIdx < 0 || mangoIdx < 0 || zebraIdx < 0 {
		t.Fatalf("expected all glossary keys in output; got: %s", glossaryStr)
	}
	if !(appleIdx < mangoIdx && mangoIdx < zebraIdx) {
		t.Errorf("glossary keys not in alphabetical order: apple@%d, mango@%d, zebra@%d",
			appleIdx, mangoIdx, zebraIdx)
	}
}

// TestMarshalJSON_Deterministic verifies that MarshalJSON called twice
// on the same value returns identical byte slices.
// Correctness Property: 01-PROP-2
func TestMarshalJSON_Deterministic(t *testing.T) {
	defer requireImplemented(t)

	req := &RequirementsV1Json{
		SpecId:        "01",
		SpecName:      "test",
		SchemaVersion: 1,
		Introduction:  "Test.",
		Glossary: RequirementsV1JsonGlossary{
			"beta":  "B",
			"alpha": "A",
			"gamma": "C",
		},
		Requirements:          []Requirement{},
		CorrectnessProperties: []CorrectnessProperty{},
		ExecutionPaths:        []ExecutionPath{},
		ErrorHandling:         []ErrorHandlingEntry{},
	}

	data1, err := MarshalJSON(req)
	if err != nil {
		t.Fatalf("first MarshalJSON call failed: %v", err)
	}

	data2, err := MarshalJSON(req)
	if err != nil {
		t.Fatalf("second MarshalJSON call failed: %v", err)
	}

	if diff := cmp.Diff(string(data1), string(data2)); diff != "" {
		t.Errorf("MarshalJSON not deterministic (-first +second):\n%s", diff)
	}
}

// TestMarshalJSON_GoldenRequirements verifies that MarshalJSON produces
// byte-for-byte identical output to the Python library for requirements.json.
// Test Spec: TS-01-7, Requirement: 01-REQ-3.2
func TestMarshalJSON_GoldenRequirements(t *testing.T) {
	defer requireImplemented(t)

	goldenPath := filepath.Join("./../testdata", "valid_spec", "requirements.json")
	goldenData, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("failed to read golden fixture: %v", err)
	}

	// Unmarshal the golden fixture
	var req RequirementsV1Json
	if err := json.Unmarshal(goldenData, &req); err != nil {
		t.Fatalf("failed to unmarshal golden fixture: %v", err)
	}

	// Re-marshal using MarshalJSON
	data, err := MarshalJSON(&req)
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}

	if diff := cmp.Diff(string(goldenData), string(data)); diff != "" {
		t.Errorf("requirements.json round-trip mismatch (-want +got):\n%s", diff)
	}
}

// TestMarshalJSON_GoldenTestSpec verifies byte-for-byte fidelity for test_spec.json.
// Test Spec: TS-01-7, Requirement: 01-REQ-3.2
func TestMarshalJSON_GoldenTestSpec(t *testing.T) {
	defer requireImplemented(t)

	goldenPath := filepath.Join("./../testdata", "valid_spec", "test_spec.json")
	goldenData, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("failed to read golden fixture: %v", err)
	}

	var ts TestSpecV1Json
	if err := json.Unmarshal(goldenData, &ts); err != nil {
		t.Fatalf("failed to unmarshal golden fixture: %v", err)
	}

	data, err := MarshalJSON(&ts)
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}

	if diff := cmp.Diff(string(goldenData), string(data)); diff != "" {
		t.Errorf("test_spec.json round-trip mismatch (-want +got):\n%s", diff)
	}
}

// TestMarshalJSON_GoldenTasks verifies byte-for-byte fidelity for tasks.json.
// Test Spec: TS-01-7, Requirement: 01-REQ-3.2
func TestMarshalJSON_GoldenTasks(t *testing.T) {
	defer requireImplemented(t)

	goldenPath := filepath.Join("./../testdata", "valid_spec", "tasks.json")
	goldenData, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("failed to read golden fixture: %v", err)
	}

	var tasks TasksV1Json
	if err := json.Unmarshal(goldenData, &tasks); err != nil {
		t.Fatalf("failed to unmarshal golden fixture: %v", err)
	}

	data, err := MarshalJSON(&tasks)
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}

	if diff := cmp.Diff(string(goldenData), string(data)); diff != "" {
		t.Errorf("tasks.json round-trip mismatch (-want +got):\n%s", diff)
	}
}

// TestMarshalJSON_EmptyMap verifies that an empty map[string]T is serialized
// as an empty JSON object {}.
// Requirement: 01-REQ-3.E2
func TestMarshalJSON_EmptyMap(t *testing.T) {
	defer requireImplemented(t)

	req := &RequirementsV1Json{
		SpecId:                "01",
		SpecName:              "test",
		SchemaVersion:         1,
		Introduction:          "Test.",
		Glossary:              RequirementsV1JsonGlossary{},
		Requirements:          []Requirement{},
		CorrectnessProperties: []CorrectnessProperty{},
		ExecutionPaths:        []ExecutionPath{},
		ErrorHandling:         []ErrorHandlingEntry{},
	}

	data, err := MarshalJSON(req)
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}

	// Parse and check that glossary is an empty object, not null
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	glossaryRaw, ok := raw["glossary"]
	if !ok {
		t.Fatal("output missing 'glossary' field")
	}

	if string(glossaryRaw) != "{}" {
		t.Errorf("empty map serialized as %s, want {}", string(glossaryRaw))
	}
}

// indexOf returns the index of the first occurrence of substr in s, or -1.
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
