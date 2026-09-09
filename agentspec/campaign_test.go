package agentspec

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- CreateCampaign tests ---

// TestTS06_11_CreateCampaignSuccess verifies that CreateCampaign writes
// campaign.yaml atomically with correct name, description, created_at,
// and updated_at fields, and returns a valid Campaign when the parent
// directory exists and campaign.yaml does not yet exist at path.
// Test Spec: TS-06-11, Requirement: 06-REQ-3.1
func TestTS06_11_CreateCampaignSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	campaignDir := filepath.Join(tmpDir, "mycampaign")

	campaign, err := CreateCampaign(campaignDir, "My Campaign", "A test campaign")
	if err != nil {
		t.Fatalf("CreateCampaign() returned error: %v", err)
	}
	if campaign == nil {
		t.Fatal("CreateCampaign() returned nil campaign; want non-nil")
	}
	if campaign.Path != campaignDir {
		t.Errorf("campaign.Path = %q; want %q", campaign.Path, campaignDir)
	}
	if campaign.Metadata.Name != "My Campaign" {
		t.Errorf("campaign.Metadata.Name = %q; want %q", campaign.Metadata.Name, "My Campaign")
	}
	if campaign.Metadata.Description != "A test campaign" {
		t.Errorf("campaign.Metadata.Description = %q; want %q", campaign.Metadata.Description, "A test campaign")
	}

	// Verify campaign.yaml was written to disk.
	yamlPath := filepath.Join(campaignDir, "campaign.yaml")
	data, readErr := os.ReadFile(yamlPath)
	if readErr != nil {
		t.Fatalf("campaign.yaml not found at %s: %v", yamlPath, readErr)
	}
	content := string(data)
	if !strings.Contains(content, "name:") {
		t.Errorf("campaign.yaml missing 'name:' field; got:\n%s", content)
	}
	if !strings.Contains(content, "created_at:") {
		t.Errorf("campaign.yaml missing 'created_at:' field; got:\n%s", content)
	}
	if !strings.Contains(content, "updated_at:") {
		t.Errorf("campaign.yaml missing 'updated_at:' field; got:\n%s", content)
	}

	// Verify metadata timestamps are populated.
	if campaign.Metadata.CreatedAt.IsZero() {
		t.Error("campaign.Metadata.CreatedAt is zero; want non-zero timestamp")
	}
	if campaign.Metadata.UpdatedAt.IsZero() {
		t.Error("campaign.Metadata.UpdatedAt is zero; want non-zero timestamp")
	}
}

// TestTS06_12_CreateCampaignAlreadyExists verifies that CreateCampaign
// returns a CampaignError without modifying the existing campaign.yaml
// when one already exists at the given path.
// Test Spec: TS-06-12, Requirement: 06-REQ-3.2
func TestTS06_12_CreateCampaignAlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()
	campaignDir := filepath.Join(tmpDir, "existing")

	// Pre-create campaign.yaml with known content.
	if err := os.MkdirAll(campaignDir, 0o755); err != nil {
		t.Fatal(err)
	}
	originalContent := "name: Original\ndescription: Original desc\n"
	yamlPath := filepath.Join(campaignDir, "campaign.yaml")
	if err := os.WriteFile(yamlPath, []byte(originalContent), 0o644); err != nil {
		t.Fatal(err)
	}

	campaign, err := CreateCampaign(campaignDir, "New Name", "New Desc")
	if campaign != nil {
		t.Errorf("CreateCampaign() returned non-nil campaign; want nil")
	}

	var ce *CampaignError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T; want *CampaignError", err)
	}
	if ce.Category() != "campaign" {
		t.Errorf("CampaignError.Category() = %q; want %q", ce.Category(), "campaign")
	}

	// Verify existing campaign.yaml was not modified.
	data, readErr := os.ReadFile(yamlPath)
	if readErr != nil {
		t.Fatalf("failed to read campaign.yaml: %v", readErr)
	}
	if string(data) != originalContent {
		t.Errorf("campaign.yaml was modified; got:\n%s\nwant:\n%s", string(data), originalContent)
	}
}

// TestTS06_13_CreateCampaignParentNotExist verifies that CreateCampaign
// returns a CampaignError when the parent directory of the given path
// does not exist.
// Test Spec: TS-06-13, Requirement: 06-REQ-3.3
func TestTS06_13_CreateCampaignParentNotExist(t *testing.T) {
	// Use a path whose parent directory does not exist.
	campaign, err := CreateCampaign("/tmp/nonexistent_parent_06_test/mycampaign", "Name", "Desc")
	if campaign != nil {
		t.Errorf("CreateCampaign() returned non-nil campaign; want nil")
	}

	var ce *CampaignError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T; want *CampaignError", err)
	}
	if ce.Category() != "campaign" {
		t.Errorf("CampaignError.Category() = %q; want %q", ce.Category(), "campaign")
	}
}

// TestCreateCampaign_EmptyName verifies that CreateCampaign returns a
// CampaignError when name is an empty string.
// Edge Case: 06-REQ-3.E2
func TestCreateCampaign_EmptyName(t *testing.T) {
	tmpDir := t.TempDir()
	campaignDir := filepath.Join(tmpDir, "campaign")

	campaign, err := CreateCampaign(campaignDir, "", "A description")
	if campaign != nil {
		t.Errorf("CreateCampaign() returned non-nil campaign; want nil")
	}

	var ce *CampaignError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T; want *CampaignError", err)
	}
	if ce.Category() != "campaign" {
		t.Errorf("CampaignError.Category() = %q; want %q", ce.Category(), "campaign")
	}
}

// TestCreateCampaign_EmptyDescription verifies that CreateCampaign returns
// a CampaignError when description is an empty string.
// Edge Case: 06-REQ-3.E2
func TestCreateCampaign_EmptyDescription(t *testing.T) {
	tmpDir := t.TempDir()
	campaignDir := filepath.Join(tmpDir, "campaign")

	campaign, err := CreateCampaign(campaignDir, "Name", "")
	if campaign != nil {
		t.Errorf("CreateCampaign() returned non-nil campaign; want nil")
	}

	var ce *CampaignError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T; want *CampaignError", err)
	}
	if ce.Category() != "campaign" {
		t.Errorf("CampaignError.Category() = %q; want %q", ce.Category(), "campaign")
	}
}

// --- OpenCampaign tests ---

// TestTS06_14_OpenCampaignSuccess verifies that OpenCampaign reads and
// parses campaign.yaml and returns a Campaign with populated Path and
// Metadata fields.
// Test Spec: TS-06-14, Requirement: 06-REQ-4.1
func TestTS06_14_OpenCampaignSuccess(t *testing.T) {
	tmpDir := t.TempDir()

	yamlContent := `name: Test
description: Desc
created_at: 2026-01-01T00:00:00Z
updated_at: 2026-01-01T00:00:00Z
`
	if err := os.WriteFile(filepath.Join(tmpDir, "campaign.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	campaign, err := OpenCampaign(tmpDir)
	if err != nil {
		t.Fatalf("OpenCampaign() returned error: %v", err)
	}
	if campaign == nil {
		t.Fatal("OpenCampaign() returned nil campaign; want non-nil")
	}
	if campaign.Path != tmpDir {
		t.Errorf("campaign.Path = %q; want %q", campaign.Path, tmpDir)
	}
	if campaign.Metadata.Name != "Test" {
		t.Errorf("campaign.Metadata.Name = %q; want %q", campaign.Metadata.Name, "Test")
	}
	if campaign.Metadata.Description != "Desc" {
		t.Errorf("campaign.Metadata.Description = %q; want %q", campaign.Metadata.Description, "Desc")
	}
	if campaign.Metadata.CreatedAt.IsZero() {
		t.Error("campaign.Metadata.CreatedAt is zero; want non-zero")
	}
}

// TestTS06_15_OpenCampaignMissingFile verifies that OpenCampaign returns
// a CampaignError when campaign.yaml does not exist at the given path.
// Test Spec: TS-06-15, Requirement: 06-REQ-4.2
func TestTS06_15_OpenCampaignMissingFile(t *testing.T) {
	tmpDir := t.TempDir() // empty directory, no campaign.yaml

	campaign, err := OpenCampaign(tmpDir)
	if campaign != nil {
		t.Errorf("OpenCampaign() returned non-nil campaign; want nil")
	}

	var ce *CampaignError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T; want *CampaignError", err)
	}
	if ce.Category() != "campaign" {
		t.Errorf("CampaignError.Category() = %q; want %q", ce.Category(), "campaign")
	}
}

// TestTS06_16_OpenCampaignMalformedYAML verifies that OpenCampaign returns
// a CampaignError wrapping the parse error when campaign.yaml contains
// malformed YAML.
// Test Spec: TS-06-16, Requirement: 06-REQ-4.3
func TestTS06_16_OpenCampaignMalformedYAML(t *testing.T) {
	tmpDir := t.TempDir()

	malformed := "name: [invalid: yaml: ::::"
	if err := os.WriteFile(filepath.Join(tmpDir, "campaign.yaml"), []byte(malformed), 0o644); err != nil {
		t.Fatal(err)
	}

	campaign, err := OpenCampaign(tmpDir)
	if campaign != nil {
		t.Errorf("OpenCampaign() returned non-nil campaign; want nil")
	}

	var ce *CampaignError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T; want *CampaignError", err)
	}
	if ce.Category() != "campaign" {
		t.Errorf("CampaignError.Category() = %q; want %q", ce.Category(), "campaign")
	}
	// The CampaignError should wrap the underlying parse error.
	if errors.Unwrap(ce) == nil {
		t.Error("CampaignError.Unwrap() returned nil; want wrapped parse error")
	}
}

// TestOpenCampaign_MissingRequiredFields verifies that OpenCampaign returns
// a CampaignError when campaign.yaml is readable but missing required
// fields such as name or created_at.
// Edge Case: 06-REQ-4.E1
func TestOpenCampaign_MissingRequiredFields(t *testing.T) {
	tmpDir := t.TempDir()

	// YAML with description but missing name and created_at.
	yamlContent := "description: Only a description\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "campaign.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	campaign, err := OpenCampaign(tmpDir)
	if campaign != nil {
		t.Errorf("OpenCampaign() returned non-nil campaign; want nil")
	}

	var ce *CampaignError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T; want *CampaignError", err)
	}
	if ce.Category() != "campaign" {
		t.Errorf("CampaignError.Category() = %q; want %q", ce.Category(), "campaign")
	}
}

// TestOpenCampaign_EmptyPath verifies that OpenCampaign returns a
// CampaignError when path is an empty string without attempting
// filesystem access.
// Edge Case: 06-REQ-4.E2
func TestOpenCampaign_EmptyPath(t *testing.T) {
	campaign, err := OpenCampaign("")
	if campaign != nil {
		t.Errorf("OpenCampaign() returned non-nil campaign; want nil")
	}

	var ce *CampaignError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T; want *CampaignError", err)
	}
	if ce.Category() != "campaign" {
		t.Errorf("CampaignError.Category() = %q; want %q", ce.Category(), "campaign")
	}
}

// --- Campaign.Specs tests ---

// TestTS06_17_SpecsSortedAndExcludesArchive verifies that Campaign.Specs()
// returns subdirectory names matching {NN}_{snake_case} sorted by numeric
// prefix ascending, and excludes the archive/ subdirectory.
// Test Spec: TS-06-17, Requirement: 06-REQ-5.1
func TestTS06_17_SpecsSortedAndExcludesArchive(t *testing.T) {
	tmpDir := t.TempDir()

	// Write campaign.yaml so we have a valid campaign.
	yamlContent := "name: Test\ndescription: Desc\ncreated_at: 2026-01-01T00:00:00Z\nupdated_at: 2026-01-01T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "campaign.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create spec subdirectories out of order, plus archive/.
	for _, dir := range []string{"03_gamma", "01_alpha", "02_beta", "archive"} {
		if err := os.Mkdir(filepath.Join(tmpDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	campaign := &Campaign{Path: tmpDir}
	specs, err := campaign.Specs()
	if err != nil {
		t.Fatalf("campaign.Specs() returned error: %v", err)
	}
	if len(specs) != 3 {
		t.Fatalf("len(specs) = %d; want 3", len(specs))
	}
	if specs[0] != "01_alpha" {
		t.Errorf("specs[0] = %q; want %q", specs[0], "01_alpha")
	}
	if specs[1] != "02_beta" {
		t.Errorf("specs[1] = %q; want %q", specs[1], "02_beta")
	}
	if specs[2] != "03_gamma" {
		t.Errorf("specs[2] = %q; want %q", specs[2], "03_gamma")
	}
	// Verify archive is not present.
	for _, s := range specs {
		if s == "archive" {
			t.Error("specs contains 'archive'; should be excluded")
		}
	}
}

// TestTS06_18_SpecsEmptyCampaign verifies that Campaign.Specs() returns an
// empty slice when the campaign directory contains no matching subdirectories.
// Test Spec: TS-06-18, Requirement: 06-REQ-5.2
func TestTS06_18_SpecsEmptyCampaign(t *testing.T) {
	tmpDir := t.TempDir()

	yamlContent := "name: Empty\ndescription: No specs\ncreated_at: 2026-01-01T00:00:00Z\nupdated_at: 2026-01-01T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "campaign.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	campaign := &Campaign{Path: tmpDir}
	specs, err := campaign.Specs()
	if err != nil {
		t.Fatalf("campaign.Specs() returned error: %v", err)
	}
	if len(specs) != 0 {
		t.Errorf("len(specs) = %d; want 0", len(specs))
	}
}

// TestSpecs_SkipsNonMatchingDirs verifies that Campaign.Specs() silently
// skips subdirectories that do not match the {NN}_{snake_case} pattern.
// Edge Case: 06-REQ-5.E2
func TestSpecs_SkipsNonMatchingDirs(t *testing.T) {
	tmpDir := t.TempDir()

	yamlContent := "name: Test\ndescription: Desc\ncreated_at: 2026-01-01T00:00:00Z\nupdated_at: 2026-01-01T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "campaign.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a mix of matching and non-matching directories.
	for _, dir := range []string{"01_alpha", "not-a-spec", "README", ".hidden", "02_beta"} {
		if err := os.Mkdir(filepath.Join(tmpDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	campaign := &Campaign{Path: tmpDir}
	specs, err := campaign.Specs()
	if err != nil {
		t.Fatalf("campaign.Specs() returned error: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("len(specs) = %d; want 2; got %v", len(specs), specs)
	}
	if specs[0] != "01_alpha" {
		t.Errorf("specs[0] = %q; want %q", specs[0], "01_alpha")
	}
	if specs[1] != "02_beta" {
		t.Errorf("specs[1] = %q; want %q", specs[1], "02_beta")
	}
}

// TestSpecs_ExcludesArchiveWithActiveSpecs verifies that Campaign.Specs()
// excludes archive/ from the returned list but includes all active spec
// directories when archive/ exists alongside them.
// Edge Case: 06-REQ-5.E3
func TestSpecs_ExcludesArchiveWithActiveSpecs(t *testing.T) {
	tmpDir := t.TempDir()

	yamlContent := "name: Test\ndescription: Desc\ncreated_at: 2026-01-01T00:00:00Z\nupdated_at: 2026-01-01T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "campaign.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{"01_alpha", "archive"} {
		if err := os.Mkdir(filepath.Join(tmpDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	campaign := &Campaign{Path: tmpDir}
	specs, err := campaign.Specs()
	if err != nil {
		t.Fatalf("campaign.Specs() returned error: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("len(specs) = %d; want 1; got %v", len(specs), specs)
	}
	if specs[0] != "01_alpha" {
		t.Errorf("specs[0] = %q; want %q", specs[0], "01_alpha")
	}
}

// TestSpecs_UnreadableDir verifies that Campaign.Specs() returns a
// CampaignError wrapping the OS error when the campaign directory
// is not readable due to filesystem permissions.
// Edge Case: 06-REQ-5.E1
func TestSpecs_UnreadableDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test skipped when running as root (permissions not enforced)")
	}

	tmpDir := t.TempDir()

	yamlContent := "name: Test\ndescription: Desc\ncreated_at: 2026-01-01T00:00:00Z\nupdated_at: 2026-01-01T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "campaign.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Remove read permission from the campaign directory.
	if err := os.Chmod(tmpDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(tmpDir, 0o755) })

	campaign := &Campaign{Path: tmpDir}
	_, err := campaign.Specs()

	var ce *CampaignError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T; want *CampaignError", err)
	}
	if ce.Category() != "campaign" {
		t.Errorf("CampaignError.Category() = %q; want %q", ce.Category(), "campaign")
	}
}

// --- Campaign.NewSpec tests ---

// TestTS06_19_NewSpecSuccess verifies that Campaign.NewSpec creates the
// spec directory, writes prd.md with correct YAML frontmatter and PRD
// body, and returns a SpecSession in StateInit.
// Test Spec: TS-06-19, Requirement: 06-REQ-6.1
func TestTS06_19_NewSpecSuccess(t *testing.T) {
	tmpDir := t.TempDir()

	// Write campaign.yaml.
	yamlContent := "name: Test\ndescription: Desc\ncreated_at: 2026-01-01T00:00:00Z\nupdated_at: 2026-01-01T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "campaign.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write a temporary PRD file.
	prdContent := "# My PRD\nSome content."
	prdPath := filepath.Join(t.TempDir(), "prd.md")
	if err := os.WriteFile(prdPath, []byte(prdContent), 0o644); err != nil {
		t.Fatal(err)
	}

	campaign := &Campaign{Path: tmpDir, Metadata: CampaignMetadata{Name: "Test"}}
	session, err := campaign.NewSpec("my_spec", prdPath, "standard", "docs/prds/my.md")
	if err != nil {
		t.Fatalf("campaign.NewSpec() returned error: %v", err)
	}
	if session == nil {
		t.Fatal("campaign.NewSpec() returned nil session; want non-nil")
	}
	if session.State() != StateInit {
		t.Errorf("session.State() = %q; want %q", session.State(), StateInit)
	}

	// Verify spec directory was created.
	specDir := filepath.Join(tmpDir, "01_my_spec")
	info, statErr := os.Stat(specDir)
	if statErr != nil {
		t.Fatalf("spec directory %s does not exist: %v", specDir, statErr)
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory", specDir)
	}

	// Verify prd.md exists with correct content.
	prdOnDisk, readErr := os.ReadFile(filepath.Join(specDir, "prd.md"))
	if readErr != nil {
		t.Fatalf("prd.md not found: %v", readErr)
	}
	prdStr := string(prdOnDisk)
	for _, field := range []string{"spec_id:", "spec_name:", "status: draft", "schema_version: 1", "source: docs/prds/my.md"} {
		if !strings.Contains(prdStr, field) {
			t.Errorf("prd.md missing %q; got:\n%s", field, prdStr)
		}
	}
	if !strings.Contains(prdStr, "# My PRD") {
		t.Errorf("prd.md missing PRD body '# My PRD'; got:\n%s", prdStr)
	}

	// Verify _session.json exists.
	sessionPath := filepath.Join(specDir, "_session.json")
	if _, statErr := os.Stat(sessionPath); statErr != nil {
		t.Errorf("_session.json not found at %s: %v", sessionPath, statErr)
	}
}

// TestTS06_20_NewSpecInvalidName verifies that Campaign.NewSpec returns a
// CampaignError without creating any directories or files when specName
// does not match [a-z][a-z0-9_]*.
// Test Spec: TS-06-20, Requirement: 06-REQ-6.2
func TestTS06_20_NewSpecInvalidName(t *testing.T) {
	tmpDir := t.TempDir()

	yamlContent := "name: Test\ndescription: Desc\ncreated_at: 2026-01-01T00:00:00Z\nupdated_at: 2026-01-01T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "campaign.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	prdPath := filepath.Join(t.TempDir(), "prd.md")
	if err := os.WriteFile(prdPath, []byte("# PRD"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Snapshot directory listing before the call.
	beforeEntries, _ := os.ReadDir(tmpDir)

	campaign := &Campaign{Path: tmpDir, Metadata: CampaignMetadata{Name: "Test"}}
	session, err := campaign.NewSpec("Invalid-Name", prdPath, "standard", "source")
	if session != nil {
		t.Errorf("campaign.NewSpec() returned non-nil session; want nil")
	}

	var ce *CampaignError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T; want *CampaignError", err)
	}
	if ce.Category() != "campaign" {
		t.Errorf("CampaignError.Category() = %q; want %q", ce.Category(), "campaign")
	}

	// Verify no new directories were created.
	afterEntries, _ := os.ReadDir(tmpDir)
	if len(afterEntries) != len(beforeEntries) {
		t.Errorf("directory entries changed: before=%d, after=%d", len(beforeEntries), len(afterEntries))
	}
}

// TestTS06_21_NewSpecMissingPRD verifies that Campaign.NewSpec returns a
// CampaignError without creating the spec directory when prdPath does not
// exist or is not readable.
// Test Spec: TS-06-21, Requirement: 06-REQ-6.3
func TestTS06_21_NewSpecMissingPRD(t *testing.T) {
	tmpDir := t.TempDir()

	yamlContent := "name: Test\ndescription: Desc\ncreated_at: 2026-01-01T00:00:00Z\nupdated_at: 2026-01-01T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "campaign.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	campaign := &Campaign{Path: tmpDir, Metadata: CampaignMetadata{Name: "Test"}}
	session, err := campaign.NewSpec("valid_name", "/nonexistent/prd.md", "standard", "source")
	if session != nil {
		t.Errorf("campaign.NewSpec() returned non-nil session; want nil")
	}

	var ce *CampaignError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T; want *CampaignError", err)
	}
	if ce.Category() != "campaign" {
		t.Errorf("CampaignError.Category() = %q; want %q", ce.Category(), "campaign")
	}

	// Verify no spec directory was created.
	specDir := filepath.Join(tmpDir, "01_valid_name")
	if _, statErr := os.Stat(specDir); statErr == nil {
		t.Errorf("spec directory %s should not exist, but it does", specDir)
	}
}

// TestNewSpec_EmptyName verifies that Campaign.NewSpec returns a
// CampaignError when specName is an empty string.
// Edge Case: 06-REQ-6.E3
func TestNewSpec_EmptyName(t *testing.T) {
	tmpDir := t.TempDir()

	yamlContent := "name: Test\ndescription: Desc\ncreated_at: 2026-01-01T00:00:00Z\nupdated_at: 2026-01-01T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "campaign.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	prdPath := filepath.Join(t.TempDir(), "prd.md")
	if err := os.WriteFile(prdPath, []byte("# PRD"), 0o644); err != nil {
		t.Fatal(err)
	}

	campaign := &Campaign{Path: tmpDir, Metadata: CampaignMetadata{Name: "Test"}}
	session, err := campaign.NewSpec("", prdPath, "standard", "source")
	if session != nil {
		t.Errorf("campaign.NewSpec() returned non-nil session; want nil")
	}

	var ce *CampaignError
	if !errors.As(err, &ce) {
		t.Fatalf("error type = %T; want *CampaignError", err)
	}
	if ce.Category() != "campaign" {
		t.Errorf("CampaignError.Category() = %q; want %q", ce.Category(), "campaign")
	}
}

// TestNewSpec_ArchivePrefixCounting verifies that Campaign.NewSpec includes
// archived spec prefixes when computing the next numeric prefix, so the
// new spec's prefix is strictly greater than all existing prefixes in both
// active and archive directories.
// Edge Case: 06-REQ-6.E2
func TestNewSpec_ArchivePrefixCounting(t *testing.T) {
	tmpDir := t.TempDir()

	yamlContent := "name: Test\ndescription: Desc\ncreated_at: 2026-01-01T00:00:00Z\nupdated_at: 2026-01-01T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "campaign.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create an active spec at prefix 01 and archived specs at prefixes
	// 02 and 05 inside archive/.
	if err := os.Mkdir(filepath.Join(tmpDir, "01_first"), 0o755); err != nil {
		t.Fatal(err)
	}
	archiveDir := filepath.Join(tmpDir, "archive")
	if err := os.Mkdir(archiveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"02_second", "05_fifth"} {
		if err := os.Mkdir(filepath.Join(archiveDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Write a PRD file.
	prdPath := filepath.Join(t.TempDir(), "prd.md")
	if err := os.WriteFile(prdPath, []byte("# Archive Test PRD"), 0o644); err != nil {
		t.Fatal(err)
	}

	campaign := &Campaign{Path: tmpDir, Metadata: CampaignMetadata{Name: "Test"}}
	session, err := campaign.NewSpec("new_spec", prdPath, "standard", "source")
	if err != nil {
		t.Fatalf("campaign.NewSpec() returned error: %v", err)
	}
	if session == nil {
		t.Fatal("campaign.NewSpec() returned nil session; want non-nil")
	}

	// The new spec should be at prefix 06 (max(01, 02, 05) + 1).
	expectedDir := filepath.Join(tmpDir, "06_new_spec")
	if _, statErr := os.Stat(expectedDir); statErr != nil {
		t.Errorf("expected spec directory %s does not exist: %v", expectedDir, statErr)
	}
}

// TestNewSpec_InvalidNameVariants tests multiple invalid specName patterns
// to confirm that only [a-z][a-z0-9_]* is accepted.
// Requirement: 06-REQ-6.2
func TestNewSpec_InvalidNameVariants(t *testing.T) {
	tmpDir := t.TempDir()

	yamlContent := "name: Test\ndescription: Desc\ncreated_at: 2026-01-01T00:00:00Z\nupdated_at: 2026-01-01T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "campaign.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	prdPath := filepath.Join(t.TempDir(), "prd.md")
	if err := os.WriteFile(prdPath, []byte("# PRD"), 0o644); err != nil {
		t.Fatal(err)
	}

	invalidNames := []string{
		"UpperCase",
		"123_starts_with_digit",
		"has-hyphen",
		"has spaces",
		"_leading_underscore",
		"ALLCAPS",
	}

	for _, name := range invalidNames {
		t.Run(name, func(t *testing.T) {
			campaign := &Campaign{Path: tmpDir, Metadata: CampaignMetadata{Name: "Test"}}
			session, err := campaign.NewSpec(name, prdPath, "standard", "source")
			if session != nil {
				t.Errorf("NewSpec(%q) returned non-nil session; want nil", name)
			}
			var ce *CampaignError
			if !errors.As(err, &ce) {
				t.Errorf("NewSpec(%q) error type = %T; want *CampaignError", name, err)
			}
		})
	}
}
