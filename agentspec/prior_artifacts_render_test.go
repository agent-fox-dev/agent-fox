package agentspec

import (
	"encoding/json"
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
		"requirements": v2RequirementsArtifact("07", "test"),
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

	// Must contain the Markdown heading the library renderer emits.
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
		"requirements": v2RequirementsArtifact("07", "test"),
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

	// The complete artifact, not a summary of IDs: every criterion sentence,
	// every contract and every path must survive into the downstream prompt
	// (format v2 §12.1).
	for _, want := range []string{
		"07-REQ-1", "Widget storage",
		"07-REQ-1.1", "WHEN a client submits a widget with a name",
		"07-REQ-1.2", "IF the submitted widget has no name",
		"→ HTTP 201 with body {id: string}",
		"07-PATH-1", "A client stores a widget and reads it back",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the downstream prompt is missing %q", want)
		}
	}
}

// ---------------------------------------------------------------------------
// The test_spec prior artifact renders as a table the task generator can use
// ---------------------------------------------------------------------------

// TestGenPrompts_PriorArtifacts_TestSpecTable verifies that the tasks step
// receives every test's id, kind and verifies list. Owning a test (rule C7)
// and owning every smoke test (rule C9) are impossible for a generator that
// cannot see them, which is exactly how v1 produced orphaned tests.
func TestGenPrompts_PriorArtifacts_TestSpecTable(t *testing.T) {
	emptyTmpDir := t.TempDir()

	priorArtifacts := map[string]any{
		"test_spec": v2TestSpecArtifact("07", "test"),
	}

	prompt, err := GenerationUserPrompt(
		"PRD text", "tasks", "07", emptyTmpDir, priorArtifacts, nil, nil,
	)
	if err != nil {
		t.Fatalf("GenerationUserPrompt() returned error: %v", err)
	}

	for _, want := range []string{
		"| Test | Kind | Verifies | Title |",
		"TS-07-1", "TS-07-2", "TS-07-3",
		"smoke",
		"07-PATH-1",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the tasks prompt is missing %q", want)
		}
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
// The size of a prior-artifact block
// ---------------------------------------------------------------------------

// TestGenPrompts_PriorArtifacts_TestTableIsCompact checks the one place where
// the prior-artifact block trades detail for size. Format v2 §12.1 requires
// the downstream step to see the *complete* upstream artifact, so the
// requirements render carries every criterion sentence and contract. The test
// list is the exception: the tasks step needs each test's id, kind and
// verifies list to own it, and the given/when/then would multiply the prompt
// without helping it decide which task a test belongs to.
func TestGenPrompts_PriorArtifacts_TestTableIsCompact(t *testing.T) {
	tests := v2TestSpecArtifact("07", "test")

	full, err := json.MarshalIndent(tests, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	table := renderPriorArtifact("test_spec", tests)

	if len(table) >= len(full)/2 {
		t.Errorf("the test table is %d chars against %d for the raw artifact; it should be far smaller",
			len(table), len(full))
	}

	// Everything the tasks step needs must survive.
	for _, want := range []string{"TS-07-1", "TS-07-2", "TS-07-3", "smoke", "07-PATH-1", "07-REQ-1.1"} {
		if !strings.Contains(table, want) {
			t.Errorf("the test table dropped %q", want)
		}
	}
	// Detail the tasks step does not need is dropped.
	if strings.Contains(table, "an empty store") {
		t.Error("the test table kept the Given clauses")
	}
}

// TestGenPrompts_PriorArtifacts_RequirementsAreComplete is the other half:
// the requirements render is not compressed. The v1 pipeline passed an
// ID-and-title summary here, so the test generator had to re-derive edge
// cases from the PRD and guess their IDs.
func TestGenPrompts_PriorArtifacts_RequirementsAreComplete(t *testing.T) {
	requirements := v2RequirementsArtifact("07", "test")
	rendered := renderPriorArtifact("requirements", requirements)

	for _, want := range []string{
		"07-REQ-1.1",
		"WHEN a client submits a widget with a name",
		"→ HTTP 201 with body {id: string}",
		"07-REQ-1.2",
		"IF the submitted widget has no name",
		"07-PATH-1",
		"**client** submits a widget",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the requirements render dropped %q", want)
		}
	}
}
