package agentspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// TS-07-11: Embedded prompt templates
// ---------------------------------------------------------------------------

// TestSpec07_EmbeddedTemplates_AllAccessible verifies that all 9 prompt
// template names are embedded at compile time and accessible via the embedded
// filesystem.
// Test Spec: TS-07-11, Requirement: 07-REQ-3.1
func TestSpec07_EmbeddedTemplates_AllAccessible(t *testing.T) {
	names := []string{
		"assessment_system",
		"assessment_user",
		"refinement_system",
		"refinement_user",
		"generation_system",
		"generation_user_base",
		"generation_user_requirements",
		"generation_user_test_spec",
		"generation_user_tasks",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			content, err := templateFS.ReadFile("templates/" + name + ".md")
			if err != nil {
				t.Fatalf("templateFS.ReadFile(%q) returned error: %v", name, err)
			}
			if len(content) == 0 {
				t.Errorf("templateFS.ReadFile(%q) returned empty content; want non-empty", name)
			}
		})
	}
}

// TestSpec07_EmbeddedTemplates_NameListComplete verifies that
// PromptTemplateNames contains exactly the 9 expected template names.
// Test Spec: TS-07-11, Requirement: 07-REQ-3.1
func TestSpec07_EmbeddedTemplates_NameListComplete(t *testing.T) {
	if len(PromptTemplateNames) != 9 {
		t.Errorf("len(PromptTemplateNames) = %d; want 9", len(PromptTemplateNames))
	}
}

// ---------------------------------------------------------------------------
// TS-07-12: LoadPrompt with project-local override
// ---------------------------------------------------------------------------

// TestSpec07_LoadPrompt_ProjectLocalOverride verifies that LoadPrompt reads
// and returns the project-local override file content with YAML frontmatter
// stripped.
// Test Spec: TS-07-12, Requirement: 07-REQ-3.2
func TestSpec07_LoadPrompt_ProjectLocalOverride(t *testing.T) {
	tmpDir := t.TempDir()
	overrideDir := filepath.Join(tmpDir, ".spec", "prompts")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatal(err)
	}

	overrideContent := "---\ntitle: custom override\n---\nCustom assessment system prompt."
	overridePath := filepath.Join(overrideDir, "assessment_system.md")
	if err := os.WriteFile(overridePath, []byte(overrideContent), 0o644); err != nil {
		t.Fatal(err)
	}

	content, err := LoadPrompt("assessment_system", tmpDir)
	if err != nil {
		t.Fatalf("LoadPrompt() returned error: %v", err)
	}
	if content != "Custom assessment system prompt." {
		t.Errorf("LoadPrompt() = %q; want %q", content, "Custom assessment system prompt.")
	}
	if strings.HasPrefix(content, "---") {
		t.Error("LoadPrompt() returned content starting with '---'; YAML frontmatter should be stripped")
	}
}

// TestSpec07_LoadPrompt_OverrideWithoutFrontmatter verifies that LoadPrompt
// returns override content unchanged when there is no YAML frontmatter.
// Edge Case: 07-REQ-3.E3
func TestSpec07_LoadPrompt_OverrideWithoutFrontmatter(t *testing.T) {
	tmpDir := t.TempDir()
	overrideDir := filepath.Join(tmpDir, ".spec", "prompts")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rawContent := "No frontmatter here, just plain content."
	overridePath := filepath.Join(overrideDir, "assessment_system.md")
	if err := os.WriteFile(overridePath, []byte(rawContent), 0o644); err != nil {
		t.Fatal(err)
	}

	content, err := LoadPrompt("assessment_system", tmpDir)
	if err != nil {
		t.Fatalf("LoadPrompt() returned error: %v", err)
	}
	if content != rawContent {
		t.Errorf("LoadPrompt() = %q; want %q", content, rawContent)
	}
}

// ---------------------------------------------------------------------------
// TS-07-13: LoadPrompt embedded fallback
// ---------------------------------------------------------------------------

// TestSpec07_LoadPrompt_EmbeddedFallback verifies that LoadPrompt falls back
// to the embedded default template when no project-local override exists.
// Test Spec: TS-07-13, Requirement: 07-REQ-3.3
func TestSpec07_LoadPrompt_EmbeddedFallback(t *testing.T) {
	emptyTmpDir := t.TempDir()

	content, err := LoadPrompt("assessment_system", emptyTmpDir)
	if err != nil {
		t.Fatalf("LoadPrompt() returned error: %v", err)
	}
	if len(content) == 0 {
		t.Error("LoadPrompt() returned empty content; want non-empty embedded template")
	}
	// Frontmatter should be stripped from embedded template.
	if strings.HasPrefix(content, "---") {
		t.Error("LoadPrompt() returned content starting with '---'; YAML frontmatter should be stripped")
	}
}

// TestSpec07_LoadPrompt_EmbeddedFallbackAllNames verifies that LoadPrompt
// returns non-empty content from embedded templates for all 10 names.
// Test Spec: TS-07-13, Requirement: 07-REQ-3.3
func TestSpec07_LoadPrompt_EmbeddedFallbackAllNames(t *testing.T) {
	emptyTmpDir := t.TempDir()

	for _, name := range PromptTemplateNames {
		t.Run(name, func(t *testing.T) {
			content, err := LoadPrompt(name, emptyTmpDir)
			if err != nil {
				t.Fatalf("LoadPrompt(%q) returned error: %v", name, err)
			}
			if len(content) == 0 {
				t.Errorf("LoadPrompt(%q) returned empty content; want non-empty embedded template", name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TS-07-14: LoadPrompt path traversal prevention
// ---------------------------------------------------------------------------

// TestSpec07_LoadPrompt_PathTraversalRejected verifies that LoadPrompt returns
// an error without reading any file when the name contains characters outside
// [a-zA-Z0-9_-].
// Test Spec: TS-07-14, Requirement: 07-REQ-3.4
func TestSpec07_LoadPrompt_PathTraversalRejected(t *testing.T) {
	invalidNames := []string{
		"../etc/passwd",
		"foo/bar",
		"foo.bar",
		"foo bar",
		"foo\tbar",
		"../../etc/shadow",
		"templates/../../../etc/passwd",
	}
	for _, name := range invalidNames {
		t.Run(name, func(t *testing.T) {
			content, err := LoadPrompt(name, "/tmp")
			if err == nil {
				t.Fatalf("LoadPrompt(%q) returned nil error; want error for invalid name", name)
			}
			if content != "" {
				t.Errorf("LoadPrompt(%q) = %q; want empty string on error", name, content)
			}
			errMsg := strings.ToLower(err.Error())
			if !strings.Contains(errMsg, "invalid") && !strings.Contains(errMsg, "name") {
				t.Errorf("error message %q does not mention 'invalid' or 'name'", err.Error())
			}
		})
	}
}

// TestSpec07_LoadPrompt_EmptyNameRejected verifies that LoadPrompt returns an
// error when called with an empty name string.
// Edge Case: 07-REQ-3.E2
func TestSpec07_LoadPrompt_EmptyNameRejected(t *testing.T) {
	content, err := LoadPrompt("", "/tmp")
	if err == nil {
		t.Fatal("LoadPrompt(\"\") returned nil error; want error for empty name")
	}
	if content != "" {
		t.Errorf("LoadPrompt(\"\") = %q; want empty string on error", content)
	}
}

// ---------------------------------------------------------------------------
// Edge Case: LoadPrompt symlink rejection
// ---------------------------------------------------------------------------

// TestSpec07_LoadPrompt_SymlinkRejected verifies that LoadPrompt ignores
// symlink overrides and falls back to the embedded default template.
// Edge Case: 07-REQ-3.E1
func TestSpec07_LoadPrompt_SymlinkRejected(t *testing.T) {
	tmpDir := t.TempDir()
	overrideDir := filepath.Join(tmpDir, ".spec", "prompts")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a real file to symlink to.
	realFile := filepath.Join(tmpDir, "real_template.md")
	if err := os.WriteFile(realFile, []byte("symlinked content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a symlink at the override path.
	symlinkPath := filepath.Join(overrideDir, "assessment_system.md")
	if err := os.Symlink(realFile, symlinkPath); err != nil {
		t.Skip("symlinks not supported on this platform")
	}

	content, err := LoadPrompt("assessment_system", tmpDir)
	if err != nil {
		t.Fatalf("LoadPrompt() returned error: %v", err)
	}
	// Should fall back to embedded, not use the symlinked content.
	if content == "symlinked content" {
		t.Error("LoadPrompt() returned symlinked content; want embedded fallback (symlinks should be rejected)")
	}
	if len(content) == 0 {
		t.Error("LoadPrompt() returned empty content; want non-empty embedded template")
	}
}

// ---------------------------------------------------------------------------
// TS-07-15: LoadPromptTemplate variable substitution
// ---------------------------------------------------------------------------

// TestSpec07_LoadPromptTemplate_VariableSubstitution verifies that
// LoadPromptTemplate substitutes $variable references with values from vars
// and leaves unmatched references unchanged.
// Test Spec: TS-07-15, Requirement: 07-REQ-3.5
func TestSpec07_LoadPromptTemplate_VariableSubstitution(t *testing.T) {
	emptyTmpDir := t.TempDir()

	content, err := LoadPromptTemplate("assessment_user", emptyTmpDir, map[string]string{
		"spec_name": "my-spec",
		"prd_text":  "PRD content",
	})
	if err != nil {
		t.Fatalf("LoadPromptTemplate() returned error: %v", err)
	}
	if strings.Contains(content, "$spec_name") {
		t.Error("LoadPromptTemplate() output contains '$spec_name'; should be substituted with 'my-spec'")
	}
	if strings.Contains(content, "$prd_text") {
		t.Error("LoadPromptTemplate() output contains '$prd_text'; should be substituted with 'PRD content'")
	}
	if !strings.Contains(content, "my-spec") {
		t.Error("LoadPromptTemplate() output does not contain 'my-spec'; $spec_name should be replaced")
	}
	if !strings.Contains(content, "PRD content") {
		t.Error("LoadPromptTemplate() output does not contain 'PRD content'; $prd_text should be replaced")
	}
}

// TestSpec07_LoadPromptTemplate_UnmatchedVarsPreserved verifies that
// unmatched $variable references pass through unchanged (safe substitute).
// Edge Case: 07-REQ-3.E4, Correctness Property: 07-PROP-6
func TestSpec07_LoadPromptTemplate_UnmatchedVarsPreserved(t *testing.T) {
	emptyTmpDir := t.TempDir()

	// Only provide spec_name but not prd_text or spec_landscape_block.
	content, err := LoadPromptTemplate("assessment_user", emptyTmpDir, map[string]string{
		"spec_name": "my-spec",
	})
	if err != nil {
		t.Fatalf("LoadPromptTemplate() returned error: %v", err)
	}
	if !strings.Contains(content, "my-spec") {
		t.Error("LoadPromptTemplate() output does not contain 'my-spec'; matched var should be replaced")
	}
	// Unmatched variables should be preserved verbatim.
	if !strings.Contains(content, "$prd_text") {
		t.Error("LoadPromptTemplate() output does not contain '$prd_text'; unmatched vars should pass through unchanged")
	}
}

// TestSpec07_LoadPromptTemplate_EmptyVarsMap verifies that LoadPromptTemplate
// with an empty vars map returns template content unchanged (all $variables preserved).
// Edge Case: 07-REQ-3.E4
func TestSpec07_LoadPromptTemplate_EmptyVarsMap(t *testing.T) {
	emptyTmpDir := t.TempDir()

	content, err := LoadPromptTemplate("assessment_user", emptyTmpDir, map[string]string{})
	if err != nil {
		t.Fatalf("LoadPromptTemplate() returned error: %v", err)
	}
	if len(content) == 0 {
		t.Error("LoadPromptTemplate() returned empty content; want non-empty template with all vars preserved")
	}
	// All $variable references should still be present.
	if !strings.Contains(content, "$spec_name") {
		t.Error("LoadPromptTemplate() output does not contain '$spec_name'; empty vars map should leave all vars unchanged")
	}
	if !strings.Contains(content, "$prd_text") {
		t.Error("LoadPromptTemplate() output does not contain '$prd_text'; empty vars map should leave all vars unchanged")
	}
}

// TestSpec07_LoadPromptTemplate_MultipleVariables verifies that multiple
// variables in one template are all substituted.
// Requirement: 07-REQ-3.5
func TestSpec07_LoadPromptTemplate_MultipleVariables(t *testing.T) {
	emptyTmpDir := t.TempDir()

	content, err := LoadPromptTemplate("assessment_user", emptyTmpDir, map[string]string{
		"spec_name":            "test-spec",
		"prd_text":             "My PRD text",
		"spec_landscape_block": "## Landscape\n- Spec A",
	})
	if err != nil {
		t.Fatalf("LoadPromptTemplate() returned error: %v", err)
	}
	if strings.Contains(content, "$spec_name") {
		t.Error("output contains '$spec_name'; should be substituted")
	}
	if strings.Contains(content, "$prd_text") {
		t.Error("output contains '$prd_text'; should be substituted")
	}
	if strings.Contains(content, "$spec_landscape_block") {
		t.Error("output contains '$spec_landscape_block'; should be substituted")
	}
	if !strings.Contains(content, "test-spec") {
		t.Error("output does not contain 'test-spec'")
	}
	if !strings.Contains(content, "My PRD text") {
		t.Error("output does not contain 'My PRD text'")
	}
	if !strings.Contains(content, "## Landscape") {
		t.Error("output does not contain '## Landscape'")
	}
}

// TestSpec07_LoadPromptTemplate_InvalidNamePropagatesError verifies that
// LoadPromptTemplate propagates LoadPrompt errors for invalid names.
// Edge Case: 07-REQ-4.E2
func TestSpec07_LoadPromptTemplate_InvalidNamePropagatesError(t *testing.T) {
	content, err := LoadPromptTemplate("../bad/name", "/tmp", map[string]string{})
	if err == nil {
		t.Fatal("LoadPromptTemplate() returned nil error; want error for invalid name")
	}
	if content != "" {
		t.Errorf("LoadPromptTemplate() = %q; want empty string on error", content)
	}
}

// ---------------------------------------------------------------------------
// TS-NS-1..4: formatQABlock deterministic ordering (issue #34)
// ---------------------------------------------------------------------------

// TestNS_FormatQABlock_DeterministicOutput verifies that formatQABlock produces
// identical output across many calls with the same multi-key map, and that
// pairs are ordered lexicographically by question key.
// Test Spec: TS-NS-1, Requirement: NS-REQ-1
func TestNS_FormatQABlock_DeterministicOutput(t *testing.T) {
	answers := map[string]string{
		"beta":  "b",
		"alpha": "a",
	}
	first := formatQABlock(answers)
	for i := 0; i < 100; i++ {
		got := formatQABlock(answers)
		if got != first {
			t.Fatalf("call %d: formatQABlock() = %q; want %q (non-deterministic output)", i+1, got, first)
		}
	}
	// alpha must appear before beta.
	alphaIdx := strings.Index(first, "Q: alpha")
	betaIdx := strings.Index(first, "Q: beta")
	if alphaIdx < 0 {
		t.Error("output does not contain 'Q: alpha'")
	}
	if betaIdx < 0 {
		t.Error("output does not contain 'Q: beta'")
	}
	if alphaIdx >= betaIdx {
		t.Errorf("'Q: alpha' (pos %d) must appear before 'Q: beta' (pos %d)", alphaIdx, betaIdx)
	}
}

// TestNS_FormatQABlock_LexicographicOrder verifies three-key ordering.
// Test Spec: TS-NS-2, Requirement: NS-REQ-2
func TestNS_FormatQABlock_LexicographicOrder(t *testing.T) {
	answers := map[string]string{
		"zebra": "z",
		"apple": "a",
		"mango": "m",
	}
	out := formatQABlock(answers)
	appleIdx := strings.Index(out, "Q: apple")
	mangoIdx := strings.Index(out, "Q: mango")
	zebraIdx := strings.Index(out, "Q: zebra")
	if appleIdx < 0 || mangoIdx < 0 || zebraIdx < 0 {
		t.Fatalf("output missing expected keys: %q", out)
	}
	if !(appleIdx < mangoIdx && mangoIdx < zebraIdx) {
		t.Errorf("expected apple < mango < zebra; got positions apple=%d mango=%d zebra=%d", appleIdx, mangoIdx, zebraIdx)
	}
}

// TestNS_FormatQABlock_EmptyMap verifies that an empty map returns the sentinel string.
// Test Spec: TS-NS-3, Requirement: NS-REQ-3
func TestNS_FormatQABlock_EmptyMap(t *testing.T) {
	got := formatQABlock(map[string]string{})
	want := "No questions and answers provided."
	if got != want {
		t.Errorf("formatQABlock({}) = %q; want %q", got, want)
	}
}

// TestNS_RefinementUserPrompt_Deterministic verifies that RefinementUserPrompt
// returns identical output on repeated calls with the same multi-key answers map.
// Test Spec: TS-NS-4, Requirement: NS-REQ-4
func TestNS_RefinementUserPrompt_Deterministic(t *testing.T) {
	answers := map[string]string{
		"beta":  "b",
		"alpha": "a",
	}
	assessment := Assessment{}
	emptyTmpDir := t.TempDir()

	first, err := RefinementUserPrompt("PRD text", answers, assessment, emptyTmpDir, nil)
	if err != nil {
		t.Fatalf("RefinementUserPrompt() returned error: %v", err)
	}
	second, err := RefinementUserPrompt("PRD text", answers, assessment, emptyTmpDir, nil)
	if err != nil {
		t.Fatalf("RefinementUserPrompt() second call returned error: %v", err)
	}
	if first != second {
		t.Errorf("RefinementUserPrompt() is non-deterministic: first call and second call differ")
	}
}
