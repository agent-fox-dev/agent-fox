package afspec

import (
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestPRDParsing_ValidFrontmatter verifies that LoadSpec correctly parses
// all frontmatter fields from a valid prd.md.
// Test Spec: TS-01-1, Requirement: 01-REQ-1.1
func TestPRDParsing_ValidFrontmatter(t *testing.T) {
	defer requireImplemented(t)

	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	// Verify all frontmatter fields
	checks := []struct {
		field string
		got   string
		want  string
	}{
		{"SpecID", spec.SpecID, "01"},
		{"SpecName", spec.SpecName, "test_feature"},
		{"Title", spec.Title, "Test Feature"},
		{"Status", spec.Status, "draft"},
		{"CreatedAt", spec.CreatedAt, "2026-01-01T00:00:00Z"},
		{"UpdatedAt", spec.UpdatedAt, "2026-01-01T00:00:00Z"},
		{"Owner", spec.Owner, "test-author"},
		{"Source", spec.Source, "https://github.com/test/repo/issues/1"},
	}

	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
		}
	}

	if spec.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", spec.SchemaVersion)
	}

	// Verify tags
	if len(spec.Tags) != 1 || spec.Tags[0] != "test" {
		t.Errorf("Tags = %v, want [\"test\"]", spec.Tags)
	}

	// Verify supersedes is empty list
	if spec.Supersedes == nil {
		t.Error("Supersedes should not be nil (should be empty slice)")
	}

	// Verify intent_hash is null
	if spec.IntentHash != nil {
		t.Errorf("IntentHash = %v, want nil", spec.IntentHash)
	}
}

// TestPRDParsing_BodyExtraction verifies that the PRD body is extracted
// correctly, containing only the Markdown content after the closing ---.
// Test Spec: TS-01-1, Requirement: 01-REQ-1.1
func TestPRDParsing_BodyExtraction(t *testing.T) {
	defer requireImplemented(t)

	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	// Body should start with the content after the closing ---
	if spec.PRDBody == "" {
		t.Fatal("PRDBody should not be empty")
	}

	// Body should contain the markdown heading
	if !strings.Contains(spec.PRDBody, "# Test Feature") {
		t.Error("PRDBody should contain '# Test Feature'")
	}

	// Body should contain the Intent section
	if !strings.Contains(spec.PRDBody, "## Intent") {
		t.Error("PRDBody should contain '## Intent'")
	}

	// Body should NOT contain frontmatter delimiters
	lines := strings.Split(spec.PRDBody, "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "---" {
			t.Error("PRDBody should not contain frontmatter delimiter '---'")
			break
		}
	}
}

// TestPRDParsing_EmptyTags verifies parsing when tags is an empty list.
// Requirement: 01-REQ-1.2
func TestPRDParsing_EmptyTags(t *testing.T) {
	defer requireImplemented(t)

	spec, err := LoadSpec("./../testdata/draft_spec")
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	// Tags should be an empty slice, not nil
	if spec.Tags == nil {
		t.Error("Tags should not be nil for empty list")
	}
	if len(spec.Tags) != 0 {
		t.Errorf("Tags = %v, want empty slice", spec.Tags)
	}
}

// TestPRDRenderer_FieldOrder verifies that the PRD renderer outputs
// frontmatter fields in the fixed order matching Python's _render_prd().
// Test Spec: TS-01-53, Requirement: 01-REQ-27.2
func TestPRDRenderer_FieldOrder(t *testing.T) {
	defer requireImplemented(t)

	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	tmpDir := t.TempDir()
	if err := spec.Save(tmpDir); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	data, err := os.ReadFile(tmpDir + "/prd.md")
	if err != nil {
		t.Fatalf("failed to read rendered prd.md: %v", err)
	}

	rendered := string(data)

	// The fixed field order from Python's _FRONTMATTER_FIELDS:
	expectedFieldOrder := []string{
		"spec_id:",
		"spec_name:",
		"title:",
		"status:",
		"created_at:",
		"updated_at:",
		"owner:",
		"source:",
		"supersedes:",
		"tags:",
		"intent_hash:",
		"schema_version:",
	}

	lastIdx := -1
	for _, field := range expectedFieldOrder {
		idx := strings.Index(rendered, field)
		if idx < 0 {
			t.Errorf("rendered prd.md missing field %q", field)
			continue
		}
		if idx <= lastIdx {
			t.Errorf("field %q appears before expected position (at %d, previous field ended at %d)",
				field, idx, lastIdx)
		}
		lastIdx = idx
	}
}

// TestPRDRenderer_GoldenMatch verifies that the rendered prd.md is
// byte-for-byte identical to the Python-generated golden fixture.
// Test Spec: TS-01-53, Requirement: 01-REQ-27.2
func TestPRDRenderer_GoldenMatch(t *testing.T) {
	defer requireImplemented(t)

	fixtureDir := "./../testdata/valid_spec"
	spec, err := LoadSpec(fixtureDir)
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	tmpDir := t.TempDir()
	if err := spec.Save(tmpDir); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	expected, err := os.ReadFile(fixtureDir + "/prd.md")
	if err != nil {
		t.Fatalf("failed to read golden prd.md: %v", err)
	}

	actual, err := os.ReadFile(tmpDir + "/prd.md")
	if err != nil {
		t.Fatalf("failed to read rendered prd.md: %v", err)
	}

	if diff := cmp.Diff(string(expected), string(actual)); diff != "" {
		t.Errorf("prd.md golden match failed (-want +got):\n%s", diff)
	}
}

// TestPRDRenderer_ValueFormatting verifies explicit value formatting rules
// in the hand-written frontmatter renderer.
// Test Spec: TS-01-53, Requirement: 01-REQ-27.2
func TestPRDRenderer_ValueFormatting(t *testing.T) {
	defer requireImplemented(t)

	fixtureDir := "./../testdata/valid_spec"
	spec, err := LoadSpec(fixtureDir)
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	tmpDir := t.TempDir()
	if err := spec.Save(tmpDir); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	data, err := os.ReadFile(tmpDir + "/prd.md")
	if err != nil {
		t.Fatalf("failed to read rendered prd.md: %v", err)
	}

	rendered := string(data)

	// Verify specific formatting rules:
	// 1. Strings should be double-quoted
	if !strings.Contains(rendered, `spec_id: "01"`) {
		t.Error("spec_id should be double-quoted")
	}

	// 2. Status should be double-quoted
	if !strings.Contains(rendered, `status: "draft"`) {
		t.Error("status should be double-quoted")
	}

	// 3. Null values should render as 'null'
	if !strings.Contains(rendered, "intent_hash: null") {
		t.Error("null intent_hash should render as 'null'")
	}

	// 4. Empty list should render as []
	if !strings.Contains(rendered, "supersedes: []") {
		t.Error("empty supersedes should render as '[]'")
	}

	// 5. Non-empty list should render inline with quoted strings
	if !strings.Contains(rendered, `tags: ["test"]`) {
		t.Error("tags should render as inline list with quoted strings")
	}

	// 6. Integer values should not be quoted
	if !strings.Contains(rendered, "schema_version: 1") {
		t.Error("schema_version should render as unquoted integer")
	}

	// 7. Frontmatter should start with ---
	if !strings.HasPrefix(rendered, "---\n") {
		t.Error("prd.md should start with '---'")
	}

	// 8. Frontmatter should have a closing ---
	if !strings.Contains(rendered, "\n---\n") {
		t.Error("prd.md should have closing '---' delimiter")
	}
}

// TestPRDRenderer_DraftFixtureMatch verifies rendering against the draft_spec
// golden fixture, testing edge cases like empty tags and empty supersedes.
// Test Spec: TS-01-52, TS-01-53, Requirement: 01-REQ-27.1, 01-REQ-27.2
func TestPRDRenderer_DraftFixtureMatch(t *testing.T) {
	defer requireImplemented(t)

	fixtureDir := "./../testdata/draft_spec"
	spec, err := LoadSpec(fixtureDir)
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	tmpDir := t.TempDir()
	if err := spec.Save(tmpDir); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	expected, err := os.ReadFile(fixtureDir + "/prd.md")
	if err != nil {
		t.Fatalf("failed to read golden prd.md: %v", err)
	}

	actual, err := os.ReadFile(tmpDir + "/prd.md")
	if err != nil {
		t.Fatalf("failed to read rendered prd.md: %v", err)
	}

	if diff := cmp.Diff(string(expected), string(actual)); diff != "" {
		t.Errorf("draft prd.md golden match failed (-want +got):\n%s", diff)
	}
}
