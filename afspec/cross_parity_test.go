package afspec

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Cross-implementation parity tests between Python and Go.
//
// Test Spec: TS-03-28, TS-03-29
// Requirements: 03-REQ-8
//
// These tests verify that the Go rendering implementation produces output
// structurally equivalent to what the Python implementation should produce
// for the same spec and budget values. Since we cannot call Python from Go,
// we validate against shared algorithmic contracts:
//
//   - EstimateTokens(text) == len(text) / 4 for any ASCII string (TS-03-29)
//   - RenderIndividual with WithMaxTokens applies the same truncation strategy:
//     Level 0 (full), Level 1 (drop architecture), Level 2 (slim test spec)
//   - Same keys present/absent and same truncation level for same budget (TS-03-28)
//
// Note: Go len() counts bytes, not runes. Parity with Python len() (which
// counts characters/codepoints) holds for ASCII-only inputs. All spec
// fixtures use ASCII content, so the contract is met.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// TS-03-29: Go EstimateTokens and Python estimate_tokens return the same
//           integer for any string input.
// Requirement: 03-REQ-8.2
// ---------------------------------------------------------------------------

func TestCrossParity_EstimateTokens_Empty(t *testing.T) {
	// Both Python and Go: estimate_tokens('') == 0
	result := EstimateTokens("")
	if result != 0 {
		t.Errorf("EstimateTokens(\"\") = %d, want 0", result)
	}
}

func TestCrossParity_EstimateTokens_SingleChar(t *testing.T) {
	// Both Python and Go: estimate_tokens('a') == 0
	result := EstimateTokens("a")
	if result != 0 {
		t.Errorf("EstimateTokens(\"a\") = %d, want 0", result)
	}
}

func TestCrossParity_EstimateTokens_FourChars(t *testing.T) {
	// Both Python and Go: estimate_tokens('abcd') == 1
	result := EstimateTokens("abcd")
	if result != 1 {
		t.Errorf("EstimateTokens(\"abcd\") = %d, want 1", result)
	}
}

func TestCrossParity_EstimateTokens_SixteenChars(t *testing.T) {
	// Both Python and Go: estimate_tokens of 16 chars == 4
	text := "abcdefghijklmnop"
	result := EstimateTokens(text)
	expected := len(text) / 4
	if result != expected || expected != 4 {
		t.Errorf("EstimateTokens(%q) = %d, want %d", text, result, expected)
	}
}

func TestCrossParity_EstimateTokens_ThousandChars(t *testing.T) {
	// Both Python and Go: estimate_tokens of 1000 chars == 250
	text := strings.Repeat("x", 1000)
	result := EstimateTokens(text)
	if result != 250 {
		t.Errorf("EstimateTokens(1000 chars) = %d, want 250", result)
	}
}

func TestCrossParity_EstimateTokens_FormulaMatch(t *testing.T) {
	// Verify the chars/4 formula for a range of string lengths.
	// Mirrors the Python test_estimate_tokens_parity_formula_match.
	testStrings := []string{
		"",
		"a",
		"ab",
		"abc",
		"abcd",
		"abcde",
		"abcdefghijklmnop",
		strings.Repeat("x", 100),
		strings.Repeat("y", 999),
		strings.Repeat("z", 1000),
	}
	for _, text := range testStrings {
		result := EstimateTokens(text)
		expected := len(text) / 4
		if result != expected {
			t.Errorf("EstimateTokens(%d chars) = %d, want %d", len(text), result, expected)
		}
	}
}

// ---------------------------------------------------------------------------
// TS-03-28: Go and Python produce structurally equivalent output
//           (same keys present/absent, same truncation level).
// Requirement: 03-REQ-8.1
// ---------------------------------------------------------------------------

func TestCrossParity_SameKeys_Level0(t *testing.T) {
	// At Level 0 (no truncation), Go produces the same keys as Python:
	// prd, requirements, test_spec, tasks, architecture.
	defer requireImplemented(t)

	spec := buildBudgetTestSpec()
	result := spec.RenderIndividual()

	expectedKeys := map[string]bool{
		"prd":          true,
		"requirements": true,
		"test_spec":    true,
		"tasks":        true,
		"architecture": true,
	}
	if len(result) != len(expectedKeys) {
		t.Errorf("Level 0: got %d keys, want %d", len(result), len(expectedKeys))
	}
	for key := range expectedKeys {
		if _, ok := result[key]; !ok {
			t.Errorf("Level 0: missing key %q", key)
		}
	}
}

func TestCrossParity_SameKeys_Level1(t *testing.T) {
	// At Level 1, architecture is absent. Same must hold for Python.
	// Both implementations should drop only the architecture key.
	defer requireImplemented(t)

	spec := buildBudgetTestSpec()

	// Compute budget that triggers Level 1
	level0 := spec.RenderIndividual()
	level0Tokens := sumTokens(level0)
	archTokens := EstimateTokens(level0["architecture"])
	budget := level0Tokens - archTokens + 5

	result := spec.RenderIndividual(WithMaxTokens(budget))

	expectedKeys := map[string]bool{
		"prd":          true,
		"requirements": true,
		"test_spec":    true,
		"tasks":        true,
	}
	if len(result) != len(expectedKeys) {
		t.Errorf("Level 1: got %d keys, want %d", len(result), len(expectedKeys))
	}
	for key := range expectedKeys {
		if _, ok := result[key]; !ok {
			t.Errorf("Level 1: missing key %q", key)
		}
	}
	if _, ok := result["architecture"]; ok {
		t.Error("Level 1: architecture should be absent")
	}
}

func TestCrossParity_SameKeys_Level2(t *testing.T) {
	// At Level 2, architecture absent and test_spec is slim.
	// Both Python and Go must produce the same key set.
	defer requireImplemented(t)

	spec := buildBudgetTestSpec()
	result := spec.RenderIndividual(WithMaxTokens(1))

	expectedKeys := map[string]bool{
		"prd":          true,
		"requirements": true,
		"test_spec":    true,
		"tasks":        true,
	}
	if len(result) != len(expectedKeys) {
		t.Errorf("Level 2: got %d keys, want %d", len(result), len(expectedKeys))
	}
	for key := range expectedKeys {
		if _, ok := result[key]; !ok {
			t.Errorf("Level 2: missing key %q", key)
		}
	}
}

func TestCrossParity_TruncationLevelMatches(t *testing.T) {
	// Both Python and Go apply the same truncation level for same budget.
	//
	// For a spec with architecture:
	// - High budget -> Level 0 (full)
	// - Medium budget (architecture removed) -> Level 1
	// - Low budget -> Level 2
	defer requireImplemented(t)

	spec := buildBudgetTestSpec()

	level0 := spec.RenderIndividual()
	level0Tokens := sumTokens(level0)
	archTokens := EstimateTokens(level0["architecture"])

	// Level 0: sufficient budget
	resultL0 := spec.RenderIndividual(WithMaxTokens(level0Tokens + 1000))
	if _, ok := resultL0["architecture"]; !ok {
		t.Error("Level 0: expected architecture to be present with sufficient budget")
	}

	// Level 1: budget triggers architecture removal
	budgetL1 := level0Tokens - archTokens + 5
	resultL1 := spec.RenderIndividual(WithMaxTokens(budgetL1))
	if _, ok := resultL1["architecture"]; ok {
		t.Error("Level 1: expected architecture to be absent")
	}
	// Test spec content should be full (assertion_pseudocode present)
	if ts, ok := resultL1["test_spec"]; ok {
		if !strings.Contains(ts, "assert result == expected") {
			t.Error("Level 1: test spec should be full (assertion_pseudocode present)")
		}
	}

	// Level 2: very small budget
	resultL2 := spec.RenderIndividual(WithMaxTokens(1))
	if _, ok := resultL2["architecture"]; ok {
		t.Error("Level 2: expected architecture to be absent")
	}
	// Test spec should be slim (assertion_pseudocode absent)
	if ts, ok := resultL2["test_spec"]; ok {
		if strings.Contains(ts, "assert result == expected") {
			t.Error("Level 2: test spec should be slim (assertion_pseudocode absent)")
		}
	}
}

func TestCrossParity_NoArchitecture_SameKeys(t *testing.T) {
	// Without architecture, Go produces same keys as Python.
	// Both should have: prd, requirements, test_spec, tasks.
	defer requireImplemented(t)

	spec := buildBudgetTestSpecNoArch()
	result := spec.RenderIndividual()

	expectedKeys := map[string]bool{
		"prd":          true,
		"requirements": true,
		"test_spec":    true,
		"tasks":        true,
	}
	if len(result) != len(expectedKeys) {
		t.Errorf("No-arch: got %d keys, want %d", len(result), len(expectedKeys))
	}
	if _, ok := result["architecture"]; ok {
		t.Error("No-arch: architecture should not be present")
	}
}

func TestCrossParity_NoArchitecture_SkipsLevel1(t *testing.T) {
	// Without architecture, Level 1 is skipped, proceeding to Level 2.
	// Both Python and Go must exhibit this behavior.
	defer requireImplemented(t)

	spec := buildBudgetTestSpecNoArch()

	// Force truncation — should go straight from Level 0 to Level 2
	result := spec.RenderIndividual(WithMaxTokens(1))

	if _, ok := result["architecture"]; ok {
		t.Error("No-arch truncation: architecture should not be present")
	}
	if _, ok := result["prd"]; !ok {
		t.Error("No-arch truncation: prd should be present")
	}
	if _, ok := result["test_spec"]; !ok {
		t.Error("No-arch truncation: test_spec should be present")
	}
}

// ---------------------------------------------------------------------------
// 03-REQ-8.E1: Same spec with architecture, Level 1 sufficient in both
// ---------------------------------------------------------------------------

func TestCrossParity_Level1_ArchitectureAbsentTestSpecIntact(t *testing.T) {
	// When Level 1 is sufficient, both Python and Go return results without
	// architecture and with test spec intact.
	defer requireImplemented(t)

	spec := buildBudgetTestSpec()

	level0 := spec.RenderIndividual()
	level0Tokens := sumTokens(level0)
	archTokens := EstimateTokens(level0["architecture"])

	// Budget so that Level 1 (removing architecture) fits
	budget := level0Tokens - archTokens + 5
	result := spec.RenderIndividual(WithMaxTokens(budget))

	// Architecture absent
	if _, ok := result["architecture"]; ok {
		t.Error("Level 1 edge case: architecture should be absent")
	}

	// Test spec intact (assertion_pseudocode still present)
	ts := result["test_spec"]
	if !strings.Contains(ts, "assert result == expected") {
		t.Error("Level 1 edge case: test spec should be intact (assertion_pseudocode present)")
	}
}
