package afspec

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// Subtask 3.1: DiscoverSpecs and LoadSpecLandscape
// ---------------------------------------------------------------------------

// writeMinimalPRD writes a minimal valid prd.md to dir with the given metadata.
func writeMinimalPRD(t *testing.T, dir, specID, specName, status string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create dir %s: %v", dir, err)
	}
	prd := "---\n" +
		"spec_id: \"" + specID + "\"\n" +
		"spec_name: \"" + specName + "\"\n" +
		"title: \"" + specName + "\"\n" +
		"status: \"" + status + "\"\n" +
		"created_at: \"2026-01-01T00:00:00Z\"\n" +
		"updated_at: \"2026-01-01T00:00:00Z\"\n" +
		"owner: \"test\"\n" +
		"source: \"test\"\n" +
		"schema_version: 1\n" +
		"---\n" +
		"# " + specName + "\n"
	if err := os.WriteFile(filepath.Join(dir, "prd.md"), []byte(prd), 0o644); err != nil {
		t.Fatalf("failed to write prd.md in %s: %v", dir, err)
	}
}

// TestDiscoverSpecs_ValidWorkspace verifies that DiscoverSpecs scans a root
// directory for NN_snake_case subdirectories and returns SpecMeta entries
// with all fields populated.
// Test Spec: TS-01-23, Requirement: 01-REQ-12.1
func TestDiscoverSpecs_ValidWorkspace(t *testing.T) {
	defer requireImplemented(t)

	root := t.TempDir()

	// Create two valid spec directories
	writeMinimalPRD(t, filepath.Join(root, "01_first_spec"), "01", "first_spec", "draft")
	writeMinimalPRD(t, filepath.Join(root, "02_second_spec"), "02", "second_spec", "active")

	// Create a non-spec directory that should be ignored
	if err := os.MkdirAll(filepath.Join(root, "readme"), 0o755); err != nil {
		t.Fatalf("failed to create non-spec dir: %v", err)
	}

	metas, err := DiscoverSpecs(root)
	if err != nil {
		t.Fatalf("DiscoverSpecs returned unexpected error: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("len(metas) = %d, want 2", len(metas))
	}

	// Verify each entry has non-empty fields
	for _, meta := range metas {
		if meta.SpecID == "" {
			t.Errorf("meta.SpecID is empty for dir %s", meta.Dir)
		}
		if meta.SpecName == "" {
			t.Errorf("meta.SpecName is empty for dir %s", meta.Dir)
		}
		if meta.Status == "" {
			t.Errorf("meta.Status is empty for dir %s", meta.Dir)
		}
		if meta.Dir == "" {
			t.Error("meta.Dir is empty")
		}
	}
}

// TestDiscoverSpecs_NoMatchingDirs verifies that DiscoverSpecs returns an
// empty slice with no error when the root has no NN_snake_case subdirectories.
// Requirement: 01-REQ-12.E3
func TestDiscoverSpecs_NoMatchingDirs(t *testing.T) {
	defer requireImplemented(t)

	root := t.TempDir()

	// Create only non-spec directories
	if err := os.MkdirAll(filepath.Join(root, "readme"), 0o755); err != nil {
		t.Fatalf("failed to create non-spec dir: %v", err)
	}

	metas, err := DiscoverSpecs(root)
	if err != nil {
		t.Fatalf("DiscoverSpecs returned unexpected error: %v", err)
	}
	if len(metas) != 0 {
		t.Errorf("len(metas) = %d, want 0 for workspace with no spec dirs", len(metas))
	}
}

// TestDiscoverSpecs_RootNotExists verifies that DiscoverSpecs returns a
// LoadError when the root directory does not exist.
// Requirement: 01-REQ-12.E2
func TestDiscoverSpecs_RootNotExists(t *testing.T) {
	defer requireImplemented(t)

	_, err := DiscoverSpecs("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected LoadError for non-existent root, got nil")
	}

	var loadErr *LoadError
	if !errors.As(err, &loadErr) {
		t.Errorf("errors.As(err, &LoadError{}) = false, want true; err type = %T", err)
	}

	var specErr *SpecError
	if !errors.As(err, &specErr) {
		t.Errorf("errors.As(err, &SpecError{}) = false, want true; err type = %T", err)
	}
}

// TestDiscoverSpecs_MalformedPRD verifies that DiscoverSpecs skips entries
// with missing or malformed prd.md and continues scanning.
// Requirement: 01-REQ-12.E1
func TestDiscoverSpecs_MalformedPRD(t *testing.T) {
	defer requireImplemented(t)

	root := t.TempDir()

	// Create one valid spec
	writeMinimalPRD(t, filepath.Join(root, "01_valid_spec"), "01", "valid_spec", "draft")

	// Create one spec dir without a prd.md (malformed)
	malformedDir := filepath.Join(root, "02_broken_spec")
	if err := os.MkdirAll(malformedDir, 0o755); err != nil {
		t.Fatalf("failed to create malformed dir: %v", err)
	}

	metas, err := DiscoverSpecs(root)
	// Should return the valid entry; err may be non-nil for the skipped entry
	if len(metas) != 1 {
		t.Errorf("len(metas) = %d, want 1 (valid spec only); err = %v", len(metas), err)
	}
	if len(metas) > 0 && metas[0].SpecID != "01" {
		t.Errorf("metas[0].SpecID = %q, want %q", metas[0].SpecID, "01")
	}
}

// ---------------------------------------------------------------------------
// LoadSpecLandscape tests
// ---------------------------------------------------------------------------

// TestLoadSpecLandscape_IncludeArchive verifies that LoadSpecLandscape with
// includeArchive=true scans all spec directories including archive/ and
// excludes the currentSpecID entry.
// Test Spec: TS-01-30, Requirement: 01-REQ-15.1
func TestLoadSpecLandscape_IncludeArchive(t *testing.T) {
	defer requireImplemented(t)

	root := t.TempDir()

	// Create non-archived specs
	writeMinimalPRD(t, filepath.Join(root, "01_spec_a"), "01", "spec_a", "draft")
	writeMinimalPRD(t, filepath.Join(root, "02_spec_b"), "02", "spec_b", "active")

	// Create an archived spec
	writeMinimalPRD(t, filepath.Join(root, "archive", "03_spec_c"), "03", "spec_c", "archived")

	metas, err := LoadSpecLandscape(root, true, "01")
	if err != nil {
		t.Fatalf("LoadSpecLandscape returned unexpected error: %v", err)
	}

	// Should have spec 02 and spec 03 (from archive), but NOT spec 01 (excluded)
	ids := make(map[string]bool)
	for _, m := range metas {
		ids[m.SpecID] = true
	}

	if ids["01"] {
		t.Error("currentSpecID '01' should be excluded from results")
	}
	if !ids["02"] {
		t.Error("spec '02' should be in results")
	}
	if !ids["03"] {
		t.Error("archived spec '03' should be in results when includeArchive=true")
	}
}

// TestLoadSpecLandscape_ExcludeArchive verifies that LoadSpecLandscape with
// includeArchive=false returns only non-archived spec directories.
// Test Spec: TS-01-31, Requirement: 01-REQ-15.2
func TestLoadSpecLandscape_ExcludeArchive(t *testing.T) {
	defer requireImplemented(t)

	root := t.TempDir()

	// Create non-archived specs
	writeMinimalPRD(t, filepath.Join(root, "01_spec_a"), "01", "spec_a", "draft")
	writeMinimalPRD(t, filepath.Join(root, "02_spec_b"), "02", "spec_b", "active")

	// Create an archived spec
	writeMinimalPRD(t, filepath.Join(root, "archive", "03_spec_c"), "03", "spec_c", "archived")

	metas, err := LoadSpecLandscape(root, false, "")
	if err != nil {
		t.Fatalf("LoadSpecLandscape returned unexpected error: %v", err)
	}

	ids := make(map[string]bool)
	for _, m := range metas {
		ids[m.SpecID] = true
	}

	if ids["03"] {
		t.Error("archived spec '03' should NOT be in results when includeArchive=false")
	}
	if !ids["01"] {
		t.Error("spec '01' should be in results")
	}
	if !ids["02"] {
		t.Error("spec '02' should be in results")
	}
}

// TestLoadSpecLandscape_EmptyRoot verifies that LoadSpecLandscape returns an
// empty slice with no error when the root contains no spec directories.
// Requirement: 01-REQ-15.E1
func TestLoadSpecLandscape_EmptyRoot(t *testing.T) {
	defer requireImplemented(t)

	root := t.TempDir()

	metas, err := LoadSpecLandscape(root, true, "")
	if err != nil {
		t.Fatalf("LoadSpecLandscape returned unexpected error: %v", err)
	}
	if len(metas) != 0 {
		t.Errorf("len(metas) = %d, want 0 for empty root", len(metas))
	}
}
