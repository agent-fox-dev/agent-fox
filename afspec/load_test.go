package afspec

import (
	"errors"
	"testing"
)

// TestLoadSpec_ValidSpec verifies that LoadSpec with a valid spec directory
// reads all required artifacts and returns a populated Spec struct with nil error.
// Test Spec: TS-01-1, Requirement: 01-REQ-1.1
func TestLoadSpec_ValidSpec(t *testing.T) {
	defer requireImplemented(t)

	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec returned unexpected error: %v", err)
	}
	if spec == nil {
		t.Fatal("LoadSpec returned nil Spec")
	}
	if spec.Requirements == nil {
		t.Error("expected Requirements to be non-nil")
	}
	if spec.TestSpec == nil {
		t.Error("expected TestSpec to be non-nil")
	}
	if spec.Tasks == nil {
		t.Error("expected Tasks to be non-nil")
	}
	if spec.PRDBody == "" {
		t.Error("expected PRDBody to be non-empty")
	}
}

// TestLoadSpec_FrontmatterFields verifies that LoadSpec parses YAML frontmatter
// from prd.md and populates all frontmatter fields on the returned Spec.
// Test Spec: TS-01-2, Requirement: 01-REQ-1.2
func TestLoadSpec_FrontmatterFields(t *testing.T) {
	defer requireImplemented(t)

	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec returned unexpected error: %v", err)
	}

	if spec.SpecID != "01" {
		t.Errorf("SpecID = %q, want %q", spec.SpecID, "01")
	}
	if spec.SpecName != "test_feature" {
		t.Errorf("SpecName = %q, want %q", spec.SpecName, "test_feature")
	}
	if spec.Title != "Test Feature" {
		t.Errorf("Title = %q, want %q", spec.Title, "Test Feature")
	}
	if spec.Status != "draft" {
		t.Errorf("Status = %q, want %q", spec.Status, "draft")
	}
	if spec.CreatedAt != "2026-01-01T00:00:00Z" {
		t.Errorf("CreatedAt = %q, want %q", spec.CreatedAt, "2026-01-01T00:00:00Z")
	}
	if spec.UpdatedAt != "2026-01-01T00:00:00Z" {
		t.Errorf("UpdatedAt = %q, want %q", spec.UpdatedAt, "2026-01-01T00:00:00Z")
	}
	if spec.Owner != "test-author" {
		t.Errorf("Owner = %q, want %q", spec.Owner, "test-author")
	}
	if spec.Source != "https://github.com/test/repo/issues/1" {
		t.Errorf("Source = %q, want %q", spec.Source, "https://github.com/test/repo/issues/1")
	}
	if spec.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want %d", spec.SchemaVersion, 1)
	}
	// PRDBody should not contain the frontmatter delimiters
	if spec.PRDBody == "" {
		t.Error("PRDBody should not be empty")
	}
}

// TestLoadSpec_NoArchitecture verifies that LoadSpec succeeds and leaves
// the Architecture field empty when architecture.md is absent.
// Test Spec: TS-01-3, Requirement: 01-REQ-1.3
func TestLoadSpec_NoArchitecture(t *testing.T) {
	defer requireImplemented(t)

	spec, err := LoadSpec("./../testdata/no_arch_spec")
	if err != nil {
		t.Fatalf("LoadSpec returned unexpected error: %v", err)
	}
	if spec == nil {
		t.Fatal("LoadSpec returned nil Spec")
	}
	if spec.Architecture != "" {
		t.Errorf("Architecture = %q, want empty string", spec.Architecture)
	}
}

// TestLoadSpec_WithArchitecture verifies that LoadSpec reads architecture.md
// when present and populates the Architecture field.
// Requirement: 01-REQ-1.1
func TestLoadSpec_WithArchitecture(t *testing.T) {
	defer requireImplemented(t)

	spec, err := LoadSpec("./../testdata/valid_spec_with_arch")
	if err != nil {
		t.Fatalf("LoadSpec returned unexpected error: %v", err)
	}
	if spec == nil {
		t.Fatal("LoadSpec returned nil Spec")
	}
	if spec.Architecture == "" {
		t.Error("expected Architecture to be non-empty when architecture.md exists")
	}
}

// TestLoadSpec_MissingRequiredFile verifies that LoadSpec returns a LoadError
// when a required artifact is missing from the directory.
// Test Spec: TS-01-51, Requirement: 01-REQ-1.E1, 01-REQ-26.2
func TestLoadSpec_MissingRequiredFile(t *testing.T) {
	defer requireImplemented(t)

	_, err := LoadSpec("testdata/missing_req")
	if err == nil {
		t.Fatal("expected error when requirements.json is missing, got nil")
	}

	var loadErr *LoadError
	if !errors.As(err, &loadErr) {
		t.Errorf("expected errors.As(err, &LoadError{}) to return true, got false; err type = %T", err)
	}

	var specErr *SpecError
	if !errors.As(err, &specErr) {
		t.Errorf("expected errors.As(err, &SpecError{}) to return true, got false; err type = %T", err)
	}
}

// TestLoadSpec_MalformedJSON verifies that LoadSpec returns a LoadError
// when a JSON artifact contains malformed JSON.
// Requirement: 01-REQ-1.E2
func TestLoadSpec_MalformedJSON(t *testing.T) {
	defer requireImplemented(t)

	_, err := LoadSpec("testdata/malformed_json")
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}

	var loadErr *LoadError
	if !errors.As(err, &loadErr) {
		t.Errorf("expected errors.As(err, &LoadError{}) to return true, got false; err type = %T", err)
	}

	var specErr *SpecError
	if !errors.As(err, &specErr) {
		t.Errorf("expected errors.As(err, &SpecError{}) to return true, got false; err type = %T", err)
	}
}

// TestLoadSpec_MalformedYAML verifies that LoadSpec returns a LoadError
// when prd.md contains malformed YAML frontmatter.
// Requirement: 01-REQ-1.E3
func TestLoadSpec_MalformedYAML(t *testing.T) {
	defer requireImplemented(t)

	_, err := LoadSpec("testdata/malformed_yaml")
	if err == nil {
		t.Fatal("expected error for malformed YAML frontmatter, got nil")
	}

	var loadErr *LoadError
	if !errors.As(err, &loadErr) {
		t.Errorf("expected errors.As(err, &LoadError{}) to return true, got false; err type = %T", err)
	}

	var specErr *SpecError
	if !errors.As(err, &specErr) {
		t.Errorf("expected errors.As(err, &SpecError{}) to return true, got false; err type = %T", err)
	}
}

// TestLoadSpec_NonexistentDir verifies that LoadSpec returns a LoadError
// when the directory path does not exist.
// Requirement: 01-REQ-1.E6
func TestLoadSpec_NonexistentDir(t *testing.T) {
	defer requireImplemented(t)

	_, err := LoadSpec("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for nonexistent directory, got nil")
	}

	var loadErr *LoadError
	if !errors.As(err, &loadErr) {
		t.Errorf("expected errors.As(err, &LoadError{}) to return true, got false; err type = %T", err)
	}

	var specErr *SpecError
	if !errors.As(err, &specErr) {
		t.Errorf("expected errors.As(err, &SpecError{}) to return true, got false; err type = %T", err)
	}
}

// TestLoadSpec_ErrorTypes verifies the table-driven error type assertions
// for all LoadSpec error scenarios.
// Requirement: 01-REQ-26.1, 01-REQ-26.2
func TestLoadSpec_ErrorTypes(t *testing.T) {
	tests := []struct {
		name string
		dir  string
	}{
		{"missing required file", "testdata/missing_req"},
		{"malformed JSON", "testdata/malformed_json"},
		{"malformed YAML", "testdata/malformed_yaml"},
		{"nonexistent directory", "/nonexistent/dir"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer requireImplemented(t)

			_, err := LoadSpec(tt.dir)
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			var loadErr *LoadError
			if !errors.As(err, &loadErr) {
				t.Errorf("errors.As(err, &LoadError{}) = false, want true")
			}

			var specErr *SpecError
			if !errors.As(err, &specErr) {
				t.Errorf("errors.As(err, &SpecError{}) = false, want true")
			}
		})
	}
}
