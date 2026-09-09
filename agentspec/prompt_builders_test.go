package agentspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// TS-07-16: DetectProjectLanguage with manifest files
// ---------------------------------------------------------------------------

// TestSpec07_DetectProjectLanguage_GoMod verifies that DetectProjectLanguage
// returns "Go" and non-empty tooling hints when a go.mod file is present.
// Test Spec: TS-07-16, Requirement: 07-REQ-3.6
func TestSpec07_DetectProjectLanguage_GoMod(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module example.com/test\n\ngo 1.21"), 0o644); err != nil {
		t.Fatal(err)
	}

	lang, hints := DetectProjectLanguage(tmpDir)
	if lang != "Go" {
		t.Errorf("DetectProjectLanguage() language = %q; want %q", lang, "Go")
	}
	if len(hints) == 0 {
		t.Error("DetectProjectLanguage() toolHints is empty; want non-empty Go tooling hints")
	}
}

// TestSpec07_DetectProjectLanguage_CargoToml verifies that DetectProjectLanguage
// returns "Rust" when a Cargo.toml file is present.
// Test Spec: TS-07-16, Requirement: 07-REQ-3.6
func TestSpec07_DetectProjectLanguage_CargoToml(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "Cargo.toml"), []byte("[package]\nname = \"test\""), 0o644); err != nil {
		t.Fatal(err)
	}

	lang, hints := DetectProjectLanguage(tmpDir)
	if lang != "Rust" {
		t.Errorf("DetectProjectLanguage() language = %q; want %q", lang, "Rust")
	}
	if len(hints) == 0 {
		t.Error("DetectProjectLanguage() toolHints is empty; want non-empty Rust tooling hints")
	}
}

// TestSpec07_DetectProjectLanguage_PackageJSON verifies that DetectProjectLanguage
// returns "TypeScript" or "JavaScript" when a package.json file is present.
// Test Spec: TS-07-16, Requirement: 07-REQ-3.6
func TestSpec07_DetectProjectLanguage_PackageJSON(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte("{\"name\": \"test\"}"), 0o644); err != nil {
		t.Fatal(err)
	}

	lang, hints := DetectProjectLanguage(tmpDir)
	if lang != "TypeScript" && lang != "JavaScript" {
		t.Errorf("DetectProjectLanguage() language = %q; want %q or %q", lang, "TypeScript", "JavaScript")
	}
	if len(hints) == 0 {
		t.Error("DetectProjectLanguage() toolHints is empty; want non-empty JS/TS tooling hints")
	}
}

// TestSpec07_DetectProjectLanguage_PyprojectToml verifies that DetectProjectLanguage
// returns "Python" when a pyproject.toml file is present.
// Test Spec: TS-07-16, Requirement: 07-REQ-3.6
func TestSpec07_DetectProjectLanguage_PyprojectToml(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "pyproject.toml"), []byte("[project]\nname = \"test\""), 0o644); err != nil {
		t.Fatal(err)
	}

	lang, hints := DetectProjectLanguage(tmpDir)
	if lang != "Python" {
		t.Errorf("DetectProjectLanguage() language = %q; want %q", lang, "Python")
	}
	if len(hints) == 0 {
		t.Error("DetectProjectLanguage() toolHints is empty; want non-empty Python tooling hints")
	}
}

// TestSpec07_DetectProjectLanguage_Gemfile verifies that DetectProjectLanguage
// returns "Ruby" when a Gemfile is present.
// Requirement: 07-REQ-3.6
func TestSpec07_DetectProjectLanguage_Gemfile(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "Gemfile"), []byte("source 'https://rubygems.org'"), 0o644); err != nil {
		t.Fatal(err)
	}

	lang, hints := DetectProjectLanguage(tmpDir)
	if lang != "Ruby" {
		t.Errorf("DetectProjectLanguage() language = %q; want %q", lang, "Ruby")
	}
	if len(hints) == 0 {
		t.Error("DetectProjectLanguage() toolHints is empty; want non-empty Ruby tooling hints")
	}
}

// TestSpec07_DetectProjectLanguage_BuildGradle verifies that DetectProjectLanguage
// returns "Java" or similar when a build.gradle file is present.
// Requirement: 07-REQ-3.6
func TestSpec07_DetectProjectLanguage_BuildGradle(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "build.gradle"), []byte("apply plugin: 'java'"), 0o644); err != nil {
		t.Fatal(err)
	}

	lang, hints := DetectProjectLanguage(tmpDir)
	if lang == "" {
		t.Error("DetectProjectLanguage() language is empty; want detected language for build.gradle")
	}
	if len(hints) == 0 {
		t.Error("DetectProjectLanguage() toolHints is empty; want non-empty tooling hints")
	}
}

// TestSpec07_DetectProjectLanguage_PomXml verifies that DetectProjectLanguage
// returns "Java" or similar when a pom.xml file is present.
// Requirement: 07-REQ-3.6
func TestSpec07_DetectProjectLanguage_PomXml(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "pom.xml"), []byte("<project/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	lang, hints := DetectProjectLanguage(tmpDir)
	if lang == "" {
		t.Error("DetectProjectLanguage() language is empty; want detected language for pom.xml")
	}
	if len(hints) == 0 {
		t.Error("DetectProjectLanguage() toolHints is empty; want non-empty tooling hints")
	}
}

// ---------------------------------------------------------------------------
// TS-07-17: DetectProjectLanguage with no manifest files
// ---------------------------------------------------------------------------

// TestSpec07_DetectProjectLanguage_EmptyDir verifies that DetectProjectLanguage
// returns empty strings when no recognized manifest files are present.
// Test Spec: TS-07-17, Requirement: 07-REQ-3.7
func TestSpec07_DetectProjectLanguage_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()

	lang, hints := DetectProjectLanguage(tmpDir)
	if lang != "" {
		t.Errorf("DetectProjectLanguage() language = %q; want empty string for empty dir", lang)
	}
	if hints != "" {
		t.Errorf("DetectProjectLanguage() toolHints = %q; want empty string for empty dir", hints)
	}
}

// TestSpec07_DetectProjectLanguage_UnrecognizedFiles verifies that
// DetectProjectLanguage returns empty strings when only unrecognized files exist.
// Requirement: 07-REQ-3.7
func TestSpec07_DetectProjectLanguage_UnrecognizedFiles(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# Hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "Makefile"), []byte("all:"), 0o644); err != nil {
		t.Fatal(err)
	}

	lang, hints := DetectProjectLanguage(tmpDir)
	if lang != "" {
		t.Errorf("DetectProjectLanguage() language = %q; want empty string for unrecognized files", lang)
	}
	if hints != "" {
		t.Errorf("DetectProjectLanguage() toolHints = %q; want empty string for unrecognized files", hints)
	}
}

// ---------------------------------------------------------------------------
// TS-07-18: FormatSpecLandscape
// ---------------------------------------------------------------------------

// TestSpec07_FormatSpecLandscape_ActiveAndArchived verifies that
// FormatSpecLandscape returns a non-empty markdown string with active and
// archived sections when given landscape entries of both types.
// Test Spec: TS-07-18, Requirement: 07-REQ-3.8
func TestSpec07_FormatSpecLandscape_ActiveAndArchived(t *testing.T) {
	landscape := []map[string]any{
		{"spec_id": "01", "title": "Spec One", "status": "active"},
		{"spec_id": "02", "title": "Spec Two", "status": "archived"},
	}

	result := FormatSpecLandscape(landscape)
	if len(result) == 0 {
		t.Fatal("FormatSpecLandscape() returned empty string; want non-empty markdown")
	}
	lower := strings.ToLower(result)
	if !strings.Contains(lower, "active") {
		t.Error("FormatSpecLandscape() does not contain 'active' section")
	}
	if !strings.Contains(lower, "archived") {
		t.Error("FormatSpecLandscape() does not contain 'archived' section")
	}
}

// TestSpec07_FormatSpecLandscape_ActiveOnly verifies active specs appear in
// the active section table.
// Requirement: 07-REQ-3.8
func TestSpec07_FormatSpecLandscape_ActiveOnly(t *testing.T) {
	landscape := []map[string]any{
		{"spec_id": "01", "title": "Active Spec", "status": "active"},
	}

	result := FormatSpecLandscape(landscape)
	if len(result) == 0 {
		t.Fatal("FormatSpecLandscape() returned empty string; want non-empty markdown")
	}
	if !strings.Contains(result, "Active Spec") || !strings.Contains(result, "01") {
		t.Error("FormatSpecLandscape() does not contain the active spec data")
	}
}

// TestSpec07_FormatSpecLandscape_EmptySlice verifies that FormatSpecLandscape
// returns an empty string or minimal placeholder for an empty landscape.
// Edge Case: 07-REQ-3.E5
func TestSpec07_FormatSpecLandscape_EmptySlice(t *testing.T) {
	result := FormatSpecLandscape([]map[string]any{})
	// Empty or minimal placeholder is acceptable.
	if len(result) > 100 {
		t.Errorf("FormatSpecLandscape(empty) returned %d chars; want empty or minimal placeholder", len(result))
	}
}

// TestSpec07_FormatSpecLandscape_NilSlice verifies that FormatSpecLandscape
// does not panic on nil input.
// Edge Case: 07-REQ-3.E5
func TestSpec07_FormatSpecLandscape_NilSlice(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("FormatSpecLandscape(nil) panicked: %v", r)
		}
	}()

	result := FormatSpecLandscape(nil)
	if len(result) > 100 {
		t.Errorf("FormatSpecLandscape(nil) returned %d chars; want empty or minimal placeholder", len(result))
	}
}

// ---------------------------------------------------------------------------
// TS-07-19: AssessmentSystemPrompt
// ---------------------------------------------------------------------------

// TestSpec07_AssessmentSystemPrompt verifies that AssessmentSystemPrompt loads
// the assessment_system template and returns non-empty content.
// Test Spec: TS-07-19, Requirement: 07-REQ-4.1
func TestSpec07_AssessmentSystemPrompt(t *testing.T) {
	emptyTmpDir := t.TempDir()

	prompt, err := AssessmentSystemPrompt(emptyTmpDir)
	if err != nil {
		t.Fatalf("AssessmentSystemPrompt() returned error: %v", err)
	}
	if len(prompt) == 0 {
		t.Error("AssessmentSystemPrompt() returned empty string; want non-empty prompt text")
	}
}

// ---------------------------------------------------------------------------
// TS-07-20: AssessmentUserPrompt
// ---------------------------------------------------------------------------

// TestSpec07_AssessmentUserPrompt verifies that AssessmentUserPrompt loads
// and renders the assessment_user template with proper variable substitution.
// Test Spec: TS-07-20, Requirement: 07-REQ-4.2
func TestSpec07_AssessmentUserPrompt(t *testing.T) {
	emptyTmpDir := t.TempDir()

	prompt, err := AssessmentUserPrompt("My PRD", "my-spec", emptyTmpDir, []map[string]any{})
	if err != nil {
		t.Fatalf("AssessmentUserPrompt() returned error: %v", err)
	}
	if !strings.Contains(prompt, "my-spec") {
		t.Error("AssessmentUserPrompt() output does not contain 'my-spec'; $spec_name should be substituted")
	}
	if !strings.Contains(prompt, "My PRD") {
		t.Error("AssessmentUserPrompt() output does not contain 'My PRD'; $prd_text should be substituted")
	}
	// Verify $spec_name and $prd_text were actually replaced (not just appended).
	if strings.Contains(prompt, "$spec_name") {
		t.Error("AssessmentUserPrompt() output still contains '$spec_name'; should be substituted")
	}
	if strings.Contains(prompt, "$prd_text") {
		t.Error("AssessmentUserPrompt() output still contains '$prd_text'; should be substituted")
	}
}

// TestSpec07_AssessmentUserPrompt_EmptyLandscape verifies that
// AssessmentUserPrompt with empty specLandscape produces valid output.
// Test Spec: TS-07-20, Requirement: 07-REQ-4.2
func TestSpec07_AssessmentUserPrompt_EmptyLandscape(t *testing.T) {
	emptyTmpDir := t.TempDir()

	prompt, err := AssessmentUserPrompt("PRD text", "test-spec", emptyTmpDir, nil)
	if err != nil {
		t.Fatalf("AssessmentUserPrompt() returned error: %v", err)
	}
	if len(prompt) == 0 {
		t.Error("AssessmentUserPrompt() returned empty string; want non-empty output")
	}
}

// ---------------------------------------------------------------------------
// TS-07-21: RefinementUserPrompt
// ---------------------------------------------------------------------------

// TestSpec07_RefinementUserPrompt verifies that RefinementUserPrompt loads
// and renders the refinement_user template with $prd_text, $assessment_block,
// $qa_block, and $spec_landscape_block substituted.
// Test Spec: TS-07-21, Requirement: 07-REQ-4.3
func TestSpec07_RefinementUserPrompt(t *testing.T) {
	emptyTmpDir := t.TempDir()

	prevAssessment := Assessment{
		Quality: "medium",
		Summary: "Needs more detail on auth flow",
		Gaps:    []string{"Missing auth details"},
	}

	prompt, err := RefinementUserPrompt(
		"Updated PRD",
		map[string]string{"q1": "answer1"},
		prevAssessment,
		emptyTmpDir,
		[]map[string]any{},
	)
	if err != nil {
		t.Fatalf("RefinementUserPrompt() returned error: %v", err)
	}
	if !strings.Contains(prompt, "Updated PRD") {
		t.Error("RefinementUserPrompt() output does not contain 'Updated PRD'; $prd_text should be substituted")
	}
	if strings.Contains(prompt, "$prd_text") {
		t.Error("RefinementUserPrompt() output still contains '$prd_text'; should be substituted")
	}
}

// ---------------------------------------------------------------------------
// TS-07-22: GenerationUserPrompt
// ---------------------------------------------------------------------------

// TestSpec07_GenerationUserPrompt_AllArtifacts verifies that GenerationUserPrompt
// loads generation_user_base and the artifact-specific template and returns a
// combined prompt for each valid artifact name.
// Test Spec: TS-07-22, Requirement: 07-REQ-4.4
func TestSpec07_GenerationUserPrompt_AllArtifacts(t *testing.T) {
	emptyTmpDir := t.TempDir()

	artifactNames := []string{"requirements", "test_spec", "tasks"}
	for _, name := range artifactNames {
		t.Run(name, func(t *testing.T) {
			prompt, err := GenerationUserPrompt(
				"PRD",
				name,
				"07",
				emptyTmpDir,
				map[string]any{},
				[]map[string]any{},
				[]map[string]any{},
			)
			if err != nil {
				t.Fatalf("GenerationUserPrompt(%q) returned error: %v", name, err)
			}
			if len(prompt) == 0 {
				t.Errorf("GenerationUserPrompt(%q) returned empty string; want non-empty prompt", name)
			}
		})
	}
}

// TestSpec07_GenerationUserPrompt_WithPriorArtifacts verifies that
// GenerationUserPrompt with priorArtifacts map injects prior context.
// Requirement: 07-REQ-4.4
func TestSpec07_GenerationUserPrompt_WithPriorArtifacts(t *testing.T) {
	emptyTmpDir := t.TempDir()

	priorArtifacts := map[string]any{
		"requirements": map[string]any{"spec_id": "07", "requirements": []any{}},
	}

	prompt, err := GenerationUserPrompt(
		"PRD text",
		"test_spec",
		"07",
		emptyTmpDir,
		priorArtifacts,
		[]map[string]any{},
		[]map[string]any{},
	)
	if err != nil {
		t.Fatalf("GenerationUserPrompt() returned error: %v", err)
	}
	if len(prompt) == 0 {
		t.Error("GenerationUserPrompt() returned empty string; want non-empty prompt")
	}
}

// TestSpec07_GenerationUserPrompt_UnrecognizedArtifact verifies that
// GenerationUserPrompt returns an error for unrecognized artifact names.
// Edge Case: 07-REQ-4.E1
func TestSpec07_GenerationUserPrompt_UnrecognizedArtifact(t *testing.T) {
	emptyTmpDir := t.TempDir()

	invalidNames := []string{"unknown", "readme", "prd", "config"}
	for _, name := range invalidNames {
		t.Run(name, func(t *testing.T) {
			prompt, err := GenerationUserPrompt(
				"PRD",
				name,
				"07",
				emptyTmpDir,
				map[string]any{},
				[]map[string]any{},
				[]map[string]any{},
			)
			if err == nil {
				t.Fatalf("GenerationUserPrompt(%q) returned nil error; want error for unrecognized artifact", name)
			}
			if prompt != "" {
				t.Errorf("GenerationUserPrompt(%q) = %q; want empty string on error", name, prompt)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Prompt builder error propagation
// ---------------------------------------------------------------------------

// TestSpec07_PromptBuilders_ErrorPropagation verifies that all prompt builder
// functions propagate errors from the underlying LoadPrompt/LoadPromptTemplate
// without panicking.
// Edge Case: 07-REQ-4.E2
func TestSpec07_PromptBuilders_ErrorPropagation(t *testing.T) {
	emptyTmpDir := t.TempDir()

	t.Run("AssessmentSystemPrompt", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("AssessmentSystemPrompt panicked: %v", r)
			}
		}()
		_, _ = AssessmentSystemPrompt(emptyTmpDir)
	})

	t.Run("AssessmentUserPrompt", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("AssessmentUserPrompt panicked: %v", r)
			}
		}()
		_, _ = AssessmentUserPrompt("prd", "spec", emptyTmpDir, nil)
	})

	t.Run("RefinementUserPrompt", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("RefinementUserPrompt panicked: %v", r)
			}
		}()
		_, _ = RefinementUserPrompt("prd", nil, Assessment{}, emptyTmpDir, nil)
	})

	t.Run("GenerationUserPrompt", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("GenerationUserPrompt panicked: %v", r)
			}
		}()
		_, _ = GenerationUserPrompt("prd", "requirements", "07", emptyTmpDir, nil, nil, nil)
	})
}

// ---------------------------------------------------------------------------
// RefinementSystemPrompt
// ---------------------------------------------------------------------------

// TestSpec07_RefinementSystemPrompt verifies that RefinementSystemPrompt loads
// the refinement_system template and returns non-empty content.
// Requirement: 07-REQ-4.3
func TestSpec07_RefinementSystemPrompt(t *testing.T) {
	emptyTmpDir := t.TempDir()

	prompt, err := RefinementSystemPrompt(emptyTmpDir)
	if err != nil {
		t.Fatalf("RefinementSystemPrompt() returned error: %v", err)
	}
	if len(prompt) == 0 {
		t.Error("RefinementSystemPrompt() returned empty string; want non-empty prompt text")
	}
}

// ---------------------------------------------------------------------------
// GenerationSystemPrompt
// ---------------------------------------------------------------------------

// TestSpec07_GenerationSystemPrompt verifies that GenerationSystemPrompt loads
// the generation_system template and returns non-empty content.
// Requirement: 07-REQ-4.4
func TestSpec07_GenerationSystemPrompt(t *testing.T) {
	emptyTmpDir := t.TempDir()

	prompt, err := GenerationSystemPrompt(emptyTmpDir)
	if err != nil {
		t.Fatalf("GenerationSystemPrompt() returned error: %v", err)
	}
	if len(prompt) == 0 {
		t.Error("GenerationSystemPrompt() returned empty string; want non-empty prompt text")
	}
}
