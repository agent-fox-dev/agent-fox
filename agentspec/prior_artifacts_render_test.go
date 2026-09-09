package agentspec

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// TS-NS-1 (issue #54): Requirements prior artifact renders as Markdown
// ---------------------------------------------------------------------------

// TestGenPrompts_PriorArtifacts_RequirementsMarkdown verifies that when
// GenerationUserPrompt is called with a priorArtifacts map containing a
// requirements entry, the returned prompt includes the requirements in Markdown
// format rather than raw JSON.
// Test Spec: TS-NS-1 (issue #54), Requirement: NS-REQ-1
func TestGenPrompts_PriorArtifacts_RequirementsMarkdown(t *testing.T) {
	emptyTmpDir := t.TempDir()

	priorArtifacts := map[string]any{
		"requirements": map[string]any{
			"spec_id":   "07",
			"spec_name": "Test Spec",
			"requirements": []any{
				map[string]any{
					"id":    "07-REQ-1",
					"title": "Test requirement",
					"acceptance_criteria": []any{
						map[string]any{
							"id": "07-REQ-1.1",
						},
					},
				},
			},
		},
	}

	prompt, err := GenerationUserPrompt(
		"PRD text",
		"test_spec",
		"07",
		emptyTmpDir,
		priorArtifacts,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("GenerationUserPrompt() returned error: %v", err)
	}

	// Must contain Markdown heading from renderRequirementsArtifact.
	if !strings.Contains(prompt, "## Requirements") {
		t.Error("prompt does not contain '## Requirements'; prior artifacts should be rendered as Markdown")
	}

	// Must NOT contain a raw JSON blob starting with '{' immediately after the
	// "Previously generated artifacts:" header.
	if strings.Contains(prompt, "Previously generated artifacts:\n{") {
		t.Error("prompt contains 'Previously generated artifacts:\\n{'; prior artifacts must not be serialized as raw JSON")
	}
}

// ---------------------------------------------------------------------------
// TS-NS-2 (issue #54): Markdown format preserves requirement IDs and titles
// ---------------------------------------------------------------------------

// TestGenPrompts_PriorArtifacts_IDsPreserved verifies that the compact Markdown
// format for a requirements prior artifact preserves all requirement IDs and
// titles needed for cross-referencing.
// Test Spec: TS-NS-2 (issue #54), Requirement: NS-REQ-2
func TestGenPrompts_PriorArtifacts_IDsPreserved(t *testing.T) {
	emptyTmpDir := t.TempDir()

	priorArtifacts := map[string]any{
		"requirements": map[string]any{
			"spec_id":   "07",
			"spec_name": "Test Spec",
			"requirements": []any{
				map[string]any{
					"id":                  "07-REQ-1",
					"title":               "Test requirement",
					"acceptance_criteria": []any{},
				},
			},
		},
	}

	prompt, err := GenerationUserPrompt(
		"PRD text",
		"test_spec",
		"07",
		emptyTmpDir,
		priorArtifacts,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("GenerationUserPrompt() returned error: %v", err)
	}

	if !strings.Contains(prompt, "07-REQ-1") {
		t.Error("prompt does not contain requirement ID '07-REQ-1'; Markdown rendering must preserve all requirement IDs")
	}
	if !strings.Contains(prompt, "Test requirement") {
		t.Error("prompt does not contain requirement title 'Test requirement'; Markdown rendering must preserve all requirement titles")
	}
}

// ---------------------------------------------------------------------------
// TS-NS-3 (issue #54): test_spec prior artifact renders as Markdown
// ---------------------------------------------------------------------------

// TestGenPrompts_PriorArtifacts_TestSpecMarkdown verifies that when
// GenerationUserPrompt is called with a priorArtifacts map containing a
// test_spec entry, the returned prompt includes the test spec in Markdown
// format with "## Test Cases" heading.
// Test Spec: TS-NS-3 (issue #54), Requirement: NS-REQ-3
func TestGenPrompts_PriorArtifacts_TestSpecMarkdown(t *testing.T) {
	emptyTmpDir := t.TempDir()

	priorArtifacts := map[string]any{
		"test_spec": map[string]any{
			"spec_id":   "07",
			"spec_name": "Test Spec",
			"test_cases": []any{
				map[string]any{
					"id":             "TS-07-1",
					"description":    "Verify basic behavior",
					"requirement_id": "07-REQ-1",
				},
			},
		},
	}

	prompt, err := GenerationUserPrompt(
		"PRD text",
		"tasks",
		"07",
		emptyTmpDir,
		priorArtifacts,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("GenerationUserPrompt() returned error: %v", err)
	}

	// Must contain "## Test Cases" heading from renderTestSpecArtifact.
	if !strings.Contains(prompt, "## Test Cases") {
		t.Error("prompt does not contain '## Test Cases'; test_spec prior artifact should render as Markdown")
	}
}

// ---------------------------------------------------------------------------
// TS-NS-4 (issue #54): Malformed prior artifact falls back gracefully
// ---------------------------------------------------------------------------

// TestGenPrompts_PriorArtifacts_MalformedFallback verifies that when a prior
// artifact entry cannot be rendered as a typed Markdown block (because the
// value is not a map), GenerationUserPrompt falls back to JSON serialization
// and returns a non-empty prompt without error.
// Test Spec: TS-NS-4 (issue #54), Requirement: NS-REQ-4
func TestGenPrompts_PriorArtifacts_MalformedFallback(t *testing.T) {
	emptyTmpDir := t.TempDir()

	// Pass a string instead of a map for the requirements entry.
	priorArtifacts := map[string]any{
		"requirements": "not-a-map",
	}

	prompt, err := GenerationUserPrompt(
		"PRD text",
		"tasks",
		"07",
		emptyTmpDir,
		priorArtifacts,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("GenerationUserPrompt() returned error: %v; want nil (fallback to JSON for malformed entry)", err)
	}
	if len(prompt) == 0 {
		t.Error("GenerationUserPrompt() returned empty prompt; want non-empty (fallback representation must be included)")
	}
	// The prior artifact block should still contain some representation of the entry.
	if !strings.Contains(prompt, "requirements") {
		t.Error("prompt does not contain 'requirements'; the prior artifact key must appear in the fallback representation")
	}
}

// ---------------------------------------------------------------------------
// TS-NS-5 (issue #54): Prompt is at least 30% shorter than JSON baseline
// ---------------------------------------------------------------------------

// TestGenPrompts_PriorArtifacts_TokenReduction verifies that the prior artifacts
// block produced by renderPriorArtifact is at least 30% shorter than the
// previous json.MarshalIndent approach for a representative fixture with 5+
// requirements and 5+ test cases.
// Test Spec: TS-NS-5 (issue #54), Requirement: NS-REQ-5
func TestGenPrompts_PriorArtifacts_TokenReduction(t *testing.T) {
	// Build representative prior artifacts with 5+ requirements and 5+ test cases.
	var reqs []any
	for i := 1; i <= 6; i++ {
		reqs = append(reqs, map[string]any{
			"id":    fmt.Sprintf("07-REQ-%d", i),
			"title": fmt.Sprintf("Requirement number %d with a descriptive title", i),
			"user_story": map[string]any{
				"role":    "developer",
				"goal":    fmt.Sprintf("accomplish goal %d for the system", i),
				"benefit": fmt.Sprintf("so that benefit %d is realized by users", i),
			},
			"acceptance_criteria": []any{
				map[string]any{
					"id":              fmt.Sprintf("07-REQ-%d.1", i),
					"ears_pattern":    "ubiquitous",
					"system":          "The system",
					"action":          fmt.Sprintf("performs action %d correctly", i),
					"return_contract": nil,
				},
			},
			"edge_cases": []any{},
		})
	}
	reqArtifact := map[string]any{
		"spec_id":                "07",
		"spec_name":              "Test Spec For Token Reduction",
		"schema_version":         1,
		"introduction":           "This spec defines requirements for the system under test.",
		"glossary":               map[string]any{"SystemUnderTest": "The component being specified"},
		"requirements":           reqs,
		"correctness_properties": []any{},
		"execution_paths":        []any{},
		"error_handling":         []any{},
	}

	var cases []any
	for i := 1; i <= 6; i++ {
		cases = append(cases, map[string]any{
			"id":                   fmt.Sprintf("TS-07-%d", i),
			"description":          fmt.Sprintf("Test case %d verifies the expected system behavior", i),
			"requirement_id":       fmt.Sprintf("07-REQ-%d", i),
			"kind":                 "acceptance",
			"preconditions":        []any{fmt.Sprintf("precondition %d is met", i)},
			"input":                fmt.Sprintf("input value %d", i),
			"expected":             fmt.Sprintf("expected output %d", i),
			"assertion_pseudocode": fmt.Sprintf("assert result == expected_%d", i),
		})
	}
	tsArtifact := map[string]any{
		"spec_id":         "07",
		"spec_name":       "Test Spec For Token Reduction",
		"schema_version":  1,
		"test_cases":      cases,
		"property_tests":  []any{},
		"edge_case_tests": []any{},
		"smoke_tests":     []any{},
		"coverage": map[string]any{
			"requirements_covered": []any{},
		},
	}

	priorArtifacts := map[string]any{
		"requirements": reqArtifact,
		"test_spec":    tsArtifact,
	}

	// --- Old approach: json.MarshalIndent over the whole map ---
	oldData, err := json.MarshalIndent(priorArtifacts, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() returned error: %v", err)
	}
	oldBlock := "Previously generated artifacts:\n" + string(oldData)
	oldLen := len(oldBlock)

	// --- New approach: renderPriorArtifact per entry ---
	var newBlock strings.Builder
	newBlock.WriteString("Previously generated artifacts:\n\n")
	for _, k := range []string{"requirements", "test_spec"} {
		newBlock.WriteString(fmt.Sprintf("### %s\n\n", k))
		newBlock.WriteString(renderPriorArtifact(k, priorArtifacts[k]))
		newBlock.WriteString("\n")
	}
	newLen := newBlock.Len()

	threshold := int(float64(oldLen) * 0.70)
	if newLen > threshold {
		t.Errorf("new prior artifacts block (%d chars) is not at least 30%% shorter than old JSON block (%d chars); ratio = %.2f (want <= 0.70)",
			newLen, oldLen, float64(newLen)/float64(oldLen))
	}
}
