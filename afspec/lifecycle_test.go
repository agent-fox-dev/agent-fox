package afspec

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Subtask 2.3: Spec.Transition and ValidTransition
// ---------------------------------------------------------------------------

// TestSpecTransition_DraftToActive verifies that Transition with a valid
// target state updates the spec status, persists to disk, and returns the
// updated Spec.
// Test Spec: TS-01-19, Requirement: 01-REQ-9.1
func TestSpecTransition_DraftToActive(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()

	spec := createDraftSpec(t, tmpDir)

	updated, err := spec.Transition("active", tmpDir)
	if err != nil {
		t.Fatalf("Transition returned unexpected error: %v", err)
	}
	if updated == nil {
		t.Fatal("Transition returned nil Spec")
	}
	if updated.Status != "active" {
		t.Errorf("updated.Status = %q, want %q", updated.Status, "active")
	}

	// Verify persisted to disk
	savedSpec, err := LoadSpec(tmpDir)
	if err != nil {
		t.Fatalf("LoadSpec after Transition failed: %v", err)
	}
	if savedSpec.Status != "active" {
		t.Errorf("saved spec Status = %q, want %q", savedSpec.Status, "active")
	}
}

// TestSpecTransition_ActiveToSealed verifies active → sealed transition.
// Requirement: 01-REQ-9.1
func TestSpecTransition_ActiveToSealed(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()

	spec := createDraftSpec(t, tmpDir)

	// First transition: draft → active
	active, err := spec.Transition("active", tmpDir)
	if err != nil {
		t.Fatalf("Transition draft→active failed: %v", err)
	}

	// Second transition: active → sealed
	sealed, err := active.Transition("sealed", tmpDir)
	if err != nil {
		t.Fatalf("Transition active→sealed failed: %v", err)
	}
	if sealed.Status != "sealed" {
		t.Errorf("sealed.Status = %q, want %q", sealed.Status, "sealed")
	}
}

// TestSpecTransition_InvalidTransition verifies that Transition with an
// invalid target state returns a LifecycleError without modifying the spec.
// Requirement: 01-REQ-9.E1
func TestSpecTransition_InvalidTransition(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()
	spec := createDraftSpec(t, tmpDir)

	// draft → sealed is not allowed
	_, err := spec.Transition("sealed", tmpDir)
	if err == nil {
		t.Fatal("expected LifecycleError for draft→sealed, got nil")
	}

	var lifecycleErr *LifecycleError
	if !errors.As(err, &lifecycleErr) {
		t.Errorf("errors.As(err, &LifecycleError{}) = false, want true; err type = %T", err)
	}

	var specErr *SpecError
	if !errors.As(err, &specErr) {
		t.Errorf("errors.As(err, &SpecError{}) = false, want true; err type = %T", err)
	}
}

// TestSpecTransition_ActiveToSuperseded verifies that Transition("superseded")
// on an active spec returns a LifecycleError without persisting any change.
// Active specs must be sealed first, then superseded via Supersede().
// Test Spec: TS-NS-1, Requirement: NS-REQ-1
func TestSpecTransition_ActiveToSuperseded(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()
	spec := createDraftSpec(t, tmpDir)

	// Transition to active first.
	active, err := spec.Transition("active", tmpDir)
	if err != nil {
		t.Fatalf("Transition draft→active failed: %v", err)
	}

	// Attempt active → superseded — must be rejected.
	_, err = active.Transition("superseded", tmpDir)
	if err == nil {
		t.Fatal("expected LifecycleError for active→superseded, got nil")
	}

	var lifecycleErr *LifecycleError
	if !errors.As(err, &lifecycleErr) {
		t.Errorf("errors.As(err, &LifecycleError{}) = false, want true; err type = %T", err)
	}

	var specErr *SpecError
	if !errors.As(err, &specErr) {
		t.Errorf("errors.As(err, &SpecError{}) = false, want true; err type = %T", err)
	}

	// Reload from disk and confirm status is still "active" (no partial write).
	saved, err := LoadSpec(tmpDir)
	if err != nil {
		t.Fatalf("LoadSpec after failed Transition failed: %v", err)
	}
	if saved.Status != "active" {
		t.Errorf("saved Status = %q after failed active→superseded, want %q", saved.Status, "active")
	}
}

// TestSpecTransition_ActiveToArchived verifies that Transition("archived") on
// an active spec returns a LifecycleError. Active specs must be sealed first;
// direct archiving from active is not permitted by the spec (Section 9.2).
// Test Spec: TS-NS-4, Requirement: NS-REQ-4
func TestSpecTransition_ActiveToArchived(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()
	spec := createDraftSpec(t, tmpDir)

	// Transition to active first.
	active, err := spec.Transition("active", tmpDir)
	if err != nil {
		t.Fatalf("Transition draft→active failed: %v", err)
	}

	// Attempt active → archived — must be rejected.
	_, err = active.Transition("archived", tmpDir)
	if err == nil {
		t.Fatal("expected LifecycleError for active→archived, got nil")
	}

	var lifecycleErr *LifecycleError
	if !errors.As(err, &lifecycleErr) {
		t.Errorf("errors.As(err, &LifecycleError{}) = false, want true; err type = %T", err)
	}

	var specErr *SpecError
	if !errors.As(err, &specErr) {
		t.Errorf("errors.As(err, &SpecError{}) = false, want true; err type = %T", err)
	}

	// Reload from disk and confirm status is still "active" (no partial write).
	saved, err := LoadSpec(tmpDir)
	if err != nil {
		t.Fatalf("LoadSpec after failed Transition failed: %v", err)
	}
	if saved.Status != "active" {
		t.Errorf("saved Status = %q after failed active→archived, want %q", saved.Status, "active")
	}
}

// TestSpecTransition_ImmutabilityCheck verifies that Transition does not
// modify the original spec — it returns a new copy.
// Test Spec: TS-01-54, Requirement: 01-REQ-28.1
func TestSpecTransition_ImmutabilityCheck(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()
	original := createDraftSpec(t, tmpDir)

	originalStatus := original.Status

	updated, err := original.Transition("active", tmpDir)
	if err != nil {
		t.Fatalf("Transition failed: %v", err)
	}

	// Original should be unchanged
	if original.Status != originalStatus {
		t.Errorf("original.Status changed from %q to %q after Transition", originalStatus, original.Status)
	}

	// Updated should have new status
	if updated.Status != "active" {
		t.Errorf("updated.Status = %q, want %q", updated.Status, "active")
	}
}

// TestValidTransition_AllowedTransitions verifies that ValidTransition
// returns true for all allowed transitions and false for disallowed ones.
// Test Spec: TS-01-20, Requirement: 01-REQ-9.2
func TestValidTransition_AllowedTransitions(t *testing.T) {
	defer requireImplemented(t)

	tests := []struct {
		current string
		target  string
		want    bool
	}{
		// Allowed transitions
		{"draft", "active", true},
		{"draft", "archived", true},
		{"active", "sealed", true},
		{"sealed", "superseded", true},
		{"sealed", "archived", true},
		{"superseded", "archived", true},

		// Disallowed transitions
		{"draft", "sealed", false},
		{"draft", "superseded", false},
		{"active", "archived", false},
		{"active", "superseded", false},
		{"active", "draft", false},
		{"sealed", "draft", false},
		{"sealed", "active", false},
		{"superseded", "draft", false},
		{"superseded", "active", false},
		{"superseded", "sealed", false},
		{"archived", "draft", false},
		{"archived", "active", false},
		{"archived", "sealed", false},
		{"archived", "superseded", false},
	}

	for _, tt := range tests {
		t.Run(tt.current+"→"+tt.target, func(t *testing.T) {
			defer requireImplemented(t)
			got := ValidTransition(tt.current, tt.target)
			if got != tt.want {
				t.Errorf("ValidTransition(%q, %q) = %v, want %v", tt.current, tt.target, got, tt.want)
			}
		})
	}
}

// TestValidTransition_ConsistentWithTransition verifies PROP-3: if
// ValidTransition returns false then Transition returns a LifecycleError.
// Property: 01-PROP-3
func TestValidTransition_ConsistentWithTransition(t *testing.T) {
	defer requireImplemented(t)

	// Test an invalid transition
	tmpDir := t.TempDir()
	spec := createDraftSpec(t, tmpDir)

	if ValidTransition("draft", "sealed") {
		t.Skip("draft→sealed is allowed — test precondition failed")
	}

	_, err := spec.Transition("sealed", tmpDir)
	if err == nil {
		t.Fatal("Transition should return error when ValidTransition returns false")
	}

	var lifecycleErr *LifecycleError
	if !errors.As(err, &lifecycleErr) {
		t.Errorf("expected LifecycleError, got %T", err)
	}
}

// ---------------------------------------------------------------------------
// Subtask 2.4: Spec.Supersede and MoveToArchive
// ---------------------------------------------------------------------------

// TestSpecSupersede_SealedSpec verifies that Supersede on a sealed spec
// transitions to superseded, prepends a deprecation banner to the PRD
// body, and persists to disk.
// Test Spec: TS-01-21, Requirement: 01-REQ-10.1
func TestSpecSupersede_SealedSpec(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()
	spec := createSealedSpec(t, tmpDir)

	updated, err := spec.Supersede("02", tmpDir)
	if err != nil {
		t.Fatalf("Supersede returned unexpected error: %v", err)
	}
	if updated == nil {
		t.Fatal("Supersede returned nil Spec")
	}
	if updated.Status != "superseded" {
		t.Errorf("updated.Status = %q, want %q", updated.Status, "superseded")
	}

	// PRDBody should start with a deprecation banner referencing "02"
	if !strings.Contains(updated.PRDBody, "02") {
		t.Errorf("PRDBody does not reference superseding spec ID '02': %q", updated.PRDBody[:min(100, len(updated.PRDBody))])
	}

	// Verify persisted to disk
	savedSpec, err := LoadSpec(tmpDir)
	if err != nil {
		t.Fatalf("LoadSpec after Supersede failed: %v", err)
	}
	if savedSpec.Status != "superseded" {
		t.Errorf("saved spec Status = %q, want %q", savedSpec.Status, "superseded")
	}
}

// TestSpecSupersede_NonSealedSpec verifies that Supersede on a non-sealed
// spec returns a LifecycleError.
// Requirement: 01-REQ-10.E1
func TestSpecSupersede_NonSealedSpec(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()
	spec := createDraftSpec(t, tmpDir)

	_, err := spec.Supersede("02", tmpDir)
	if err == nil {
		t.Fatal("expected LifecycleError for Supersede on draft spec, got nil")
	}

	var lifecycleErr *LifecycleError
	if !errors.As(err, &lifecycleErr) {
		t.Errorf("errors.As(err, &LifecycleError{}) = false, want true; err type = %T", err)
	}

	var specErr *SpecError
	if !errors.As(err, &specErr) {
		t.Errorf("errors.As(err, &SpecError{}) = false, want true; err type = %T", err)
	}
}

// TestSpecSupersede_ActiveSpec verifies that Supersede on an active spec
// returns a LifecycleError (only sealed → superseded is allowed via Supersede).
// Requirement: 01-REQ-10.E1
func TestSpecSupersede_ActiveSpec(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()
	spec := createDraftSpec(t, tmpDir)

	active, err := spec.Transition("active", tmpDir)
	if err != nil {
		t.Fatalf("Transition draft→active failed: %v", err)
	}

	_, err = active.Supersede("02", tmpDir)
	if err == nil {
		t.Fatal("expected LifecycleError for Supersede on active spec, got nil")
	}

	var lifecycleErr *LifecycleError
	if !errors.As(err, &lifecycleErr) {
		t.Errorf("errors.As(err, &LifecycleError{}) = false, want true; err type = %T", err)
	}
}

// TestMoveToArchive_Success verifies that MoveToArchive transitions the
// spec to archived state and moves the directory to {root}/archive/.
// Test Spec: TS-01-22, Requirement: 01-REQ-11.1
func TestMoveToArchive_Success(t *testing.T) {
	defer requireImplemented(t)

	tmpRoot := t.TempDir()
	specDir := filepath.Join(tmpRoot, "01_myspec")

	// Create the spec directory with valid artifacts
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("failed to create spec dir: %v", err)
	}
	writeSpecFixtures(t, specDir)

	err := MoveToArchive(specDir, tmpRoot)
	if err != nil {
		t.Fatalf("MoveToArchive returned unexpected error: %v", err)
	}

	// Check that archive directory was created and contains the spec
	archiveDir := filepath.Join(tmpRoot, "archive", "01_myspec")
	if _, err := os.Stat(archiveDir); os.IsNotExist(err) {
		t.Errorf("archive dir %s does not exist", archiveDir)
	}

	// Check that original directory no longer exists
	if _, err := os.Stat(specDir); !os.IsNotExist(err) {
		t.Errorf("original spec dir %s still exists after archiving", specDir)
	}
}

// TestMoveToArchive_CreatesArchiveDir verifies that MoveToArchive creates
// the archive directory if it does not exist.
// Requirement: 01-REQ-11.E1
func TestMoveToArchive_CreatesArchiveDir(t *testing.T) {
	defer requireImplemented(t)

	tmpRoot := t.TempDir()
	specDir := filepath.Join(tmpRoot, "01_myspec")

	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("failed to create spec dir: %v", err)
	}
	writeSpecFixtures(t, specDir)

	// archive/ should not exist yet
	archiveParent := filepath.Join(tmpRoot, "archive")
	if _, err := os.Stat(archiveParent); !os.IsNotExist(err) {
		t.Fatal("archive directory exists before MoveToArchive — test precondition failed")
	}

	err := MoveToArchive(specDir, tmpRoot)
	if err != nil {
		t.Fatalf("MoveToArchive returned unexpected error: %v", err)
	}

	// archive/ should now exist
	if _, err := os.Stat(archiveParent); os.IsNotExist(err) {
		t.Error("archive directory was not created by MoveToArchive")
	}
}

// TestMoveToArchive_ConflictInArchive verifies that MoveToArchive returns
// a SaveError when the archive already contains a directory with the same name.
// Requirement: 01-REQ-11.E2
func TestMoveToArchive_ConflictInArchive(t *testing.T) {
	defer requireImplemented(t)

	tmpRoot := t.TempDir()
	specDir := filepath.Join(tmpRoot, "01_myspec")

	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("failed to create spec dir: %v", err)
	}
	writeSpecFixtures(t, specDir)

	// Pre-create a conflicting directory in archive/
	conflictDir := filepath.Join(tmpRoot, "archive", "01_myspec")
	if err := os.MkdirAll(conflictDir, 0o755); err != nil {
		t.Fatalf("failed to create conflict dir: %v", err)
	}

	err := MoveToArchive(specDir, tmpRoot)
	if err == nil {
		t.Fatal("expected SaveError for archive conflict, got nil")
	}

	var saveErr *SaveError
	if !errors.As(err, &saveErr) {
		t.Errorf("errors.As(err, &SaveError{}) = false, want true; err type = %T", err)
	}

	var specErr *SpecError
	if !errors.As(err, &specErr) {
		t.Errorf("errors.As(err, &SpecError{}) = false, want true; err type = %T", err)
	}
}

// TestMoveToArchive_StatPermissionError verifies that MoveToArchive returns a
// SaveError (wrapping a SpecError) when os.Stat on the destination returns a
// non-ErrNotExist error (e.g. permission denied on the archive parent directory).
// The original spec directory must remain untouched (rename was not attempted).
// Test Spec: TS-NS-1, Requirement: NS-REQ-1
func TestMoveToArchive_StatPermissionError(t *testing.T) {
	defer requireImplemented(t)

	if os.Getuid() == 0 {
		t.Skip("cannot test permission errors as root")
	}

	tmpRoot := t.TempDir()
	specDir := filepath.Join(tmpRoot, "01_myspec")

	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("failed to create spec dir: %v", err)
	}
	writeSpecFixtures(t, specDir)

	// Create archive/ with mode 0o000 so that Stat on any child path fails
	// with permission denied rather than ErrNotExist.
	archiveParent := filepath.Join(tmpRoot, "archive")
	if err := os.MkdirAll(archiveParent, 0o000); err != nil {
		t.Fatalf("failed to create archive dir: %v", err)
	}
	t.Cleanup(func() {
		// Restore permissions so TempDir cleanup can remove the directory.
		_ = os.Chmod(archiveParent, 0o755)
	})

	err := MoveToArchive(specDir, tmpRoot)
	if err == nil {
		t.Fatal("expected SaveError for permission-denied Stat, got nil")
	}

	var saveErr *SaveError
	if !errors.As(err, &saveErr) {
		t.Errorf("errors.As(err, &SaveError{}) = false, want true; err type = %T: %v", err, err)
	}

	var specErr *SpecError
	if !errors.As(err, &specErr) {
		t.Errorf("errors.As(err, &SpecError{}) = false, want true; err type = %T: %v", err, err)
	}

	// The original spec directory must still exist — rename was not attempted.
	if _, statErr := os.Stat(specDir); os.IsNotExist(statErr) {
		t.Errorf("original spec dir %s was removed despite error — rename must not have been attempted", specDir)
	}
}

// ---------------------------------------------------------------------------
// Intent hash tests (issue #27)
// ---------------------------------------------------------------------------

// TestTransition_DraftToActive_SetsIntentHash verifies NS-REQ-1 / TS-NS-1:
// after draft→active, the returned Spec has a non-nil, non-empty IntentHash,
// and the persisted prd.md contains that same hash.
func TestTransition_DraftToActive_SetsIntentHash(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()
	spec := createDraftSpec(t, tmpDir)

	updated, err := spec.Transition("active", tmpDir)
	if err != nil {
		t.Fatalf("Transition returned unexpected error: %v", err)
	}

	if updated.IntentHash == nil {
		t.Fatal("updated.IntentHash is nil, want non-nil")
	}
	if *updated.IntentHash == "" {
		t.Fatal("updated.IntentHash is empty, want non-empty")
	}

	// Verify the persisted spec also has the hash.
	saved, err := LoadSpec(tmpDir)
	if err != nil {
		t.Fatalf("LoadSpec after Transition failed: %v", err)
	}
	if saved.IntentHash == nil {
		t.Fatal("saved spec IntentHash is nil after loading")
	}
	if *saved.IntentHash != *updated.IntentHash {
		t.Errorf("saved IntentHash %q != returned IntentHash %q", *saved.IntentHash, *updated.IntentHash)
	}
}

// TestComputeIntentHash_Deterministic verifies NS-REQ-2 / TS-NS-2:
// two successive calls with identical input return identical hex-digest strings.
func TestComputeIntentHash_Deterministic(t *testing.T) {
	defer requireImplemented(t)

	body := `# Spec

## Intent

Build a system that does something useful.

## Goals

- Goal 1
`
	h1, err := ComputeIntentHash(body)
	if err != nil {
		t.Fatalf("first ComputeIntentHash returned error: %v", err)
	}
	h2, err := ComputeIntentHash(body)
	if err != nil {
		t.Fatalf("second ComputeIntentHash returned error: %v", err)
	}
	if h1 != h2 {
		t.Errorf("ComputeIntentHash is not deterministic: %q != %q", h1, h2)
	}
	// SHA-256 produces a 64-character lowercase hex string.
	if len(h1) != 64 {
		t.Errorf("hash length = %d, want 64", len(h1))
	}
	for _, c := range h1 {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("hash contains non-hex character %q: %s", c, h1)
			break
		}
	}
}

// TestComputeIntentHash_DifferentAfterMutation verifies NS-REQ-3 / TS-NS-3:
// after activating a spec, mutating the ## Intent section produces a hash
// that differs from the stored one.
func TestComputeIntentHash_DifferentAfterMutation(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()
	spec := createDraftSpec(t, tmpDir)

	active, err := spec.Transition("active", tmpDir)
	if err != nil {
		t.Fatalf("Transition draft→active failed: %v", err)
	}
	stored := *active.IntentHash

	// Mutate the ## Intent section.
	mutated := strings.ReplaceAll(active.PRDBody,
		"Build a test feature that validates the spec library works correctly.",
		"Build a COMPLETELY DIFFERENT thing.")

	newHash, err := ComputeIntentHash(mutated)
	if err != nil {
		t.Fatalf("ComputeIntentHash on mutated body failed: %v", err)
	}
	if newHash == stored {
		t.Errorf("hash did not change after mutating ## Intent section: both are %q", stored)
	}
}

// TestSave_ActiveSpec_IntentDrift verifies NS-REQ-4 / TS-NS-4:
// Save() on an active spec whose PRD body's ## Intent section has drifted
// from the stored IntentHash returns an IntentError.
func TestSave_ActiveSpec_IntentDrift(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()
	spec := createDraftSpec(t, tmpDir)

	active, err := spec.Transition("active", tmpDir)
	if err != nil {
		t.Fatalf("Transition draft→active failed: %v", err)
	}

	// Mutate the ## Intent section.
	active.PRDBody = strings.ReplaceAll(active.PRDBody,
		"Build a test feature that validates the spec library works correctly.",
		"Build a COMPLETELY DIFFERENT thing.")

	err = active.Save(tmpDir)
	if err == nil {
		t.Fatal("Save() returned nil error for intent drift, want IntentError")
	}

	var intentErr *IntentError
	if !errors.As(err, &intentErr) {
		t.Errorf("errors.As(err, &IntentError{}) = false, want true; err = %T: %v", err, err)
	}

	var specErr *SpecError
	if !errors.As(err, &specErr) {
		t.Errorf("errors.As(err, &SpecError{}) = false, want true; err = %T: %v", err, err)
	}
}

// TestTransition_DraftToActive_NoIntentSection verifies NS-REQ-5 / TS-NS-5:
// transitioning a spec whose prd.md body lacks a ## Intent section from
// draft to active returns an IntentError and does not persist the active state.
func TestTransition_DraftToActive_NoIntentSection(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()

	// Write fixtures without a ## Intent section.
	writeSpecFixturesNoIntent(t, tmpDir)

	spec, err := LoadSpec(tmpDir)
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	_, err = spec.Transition("active", tmpDir)
	if err == nil {
		t.Fatal("Transition returned nil error for spec without ## Intent, want IntentError")
	}

	var intentErr *IntentError
	if !errors.As(err, &intentErr) {
		t.Errorf("errors.As(err, &IntentError{}) = false, want true; err = %T: %v", err, err)
	}

	var specErr *SpecError
	if !errors.As(err, &specErr) {
		t.Errorf("errors.As(err, &SpecError{}) = false, want true; err = %T: %v", err, err)
	}

	// Status should still be draft on disk.
	saved, err := LoadSpec(tmpDir)
	if err != nil {
		t.Fatalf("LoadSpec after failed Transition failed: %v", err)
	}
	if saved.Status != "draft" {
		t.Errorf("saved Status = %q after failed Transition, want %q", saved.Status, "draft")
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// createDraftSpec creates a draft spec, saves it to dir, and returns it.
func createDraftSpec(t *testing.T, dir string) *Spec {
	t.Helper()
	writeSpecFixtures(t, dir)

	spec, err := LoadSpec(dir)
	if err != nil {
		t.Fatalf("LoadSpec failed while creating draft spec: %v", err)
	}
	if spec.Status != "draft" {
		t.Fatalf("expected draft status, got %q", spec.Status)
	}
	return spec
}

// createSealedSpec creates a sealed spec by transitioning draft → active → sealed.
func createSealedSpec(t *testing.T, dir string) *Spec {
	t.Helper()
	spec := createDraftSpec(t, dir)

	active, err := spec.Transition("active", dir)
	if err != nil {
		t.Fatalf("Transition draft→active failed: %v", err)
	}

	sealed, err := active.Transition("sealed", dir)
	if err != nil {
		t.Fatalf("Transition active→sealed failed: %v", err)
	}
	return sealed
}

// writeSpecFixtures writes minimal valid spec fixtures to a directory.
func writeSpecFixtures(t *testing.T, dir string) {
	t.Helper()

	prd := `---
spec_id: "01"
spec_name: "test_feature"
title: "Test Feature"
status: "draft"
created_at: "2026-01-01T00:00:00Z"
updated_at: "2026-01-01T00:00:00Z"
owner: "test-author"
source: "https://github.com/test/repo/issues/1"
supersedes: []
tags: ["test"]
intent_hash: null
schema_version: 1
---
# Test Feature

## Intent

Build a test feature that validates the spec library works correctly.

## Goals

- Validate loading and saving of specs.
- Ensure cross-file integrity checks work.

## Non-goals

- Production deployment.
`

	reqJSON := `{
  "$schema": "https://agent-fox.dev/schemas/requirements.v1.json",
  "spec_id": "01",
  "spec_name": "test_feature",
  "schema_version": 1,
  "introduction": "The test feature validates the spec library.",
  "glossary": {
    "spec": "A four-artifact package representing one feature.",
    "system": "The afspec library."
  },
  "requirements": [
    {
      "id": "01-REQ-1",
      "title": "Data Model",
      "user_story": {
        "role": "developer",
        "goal": "have typed models for spec artifacts",
        "benefit": "type safety when working with specs"
      },
      "acceptance_criteria": [
        {
          "id": "01-REQ-1.1",
          "ears_pattern": "event_driven",
          "trigger": "a spec is loaded from disk",
          "system": "the system",
          "action": "return a populated Spec instance",
          "return_contract": "a Spec instance"
        }
      ],
      "edge_cases": [
        {
          "id": "01-REQ-1.E1",
          "ears_pattern": "unwanted",
          "error_condition": "the spec directory is missing a required file",
          "system": "the system",
          "action": "raise a LoadError identifying the missing file",
          "return_contract": "raises LoadError with message listing the missing file path"
        }
      ]
    }
  ],
  "correctness_properties": [
    {
      "id": "01-PROP-1",
      "title": "Round-trip idempotency",
      "for_any": "valid Spec value",
      "invariant": "loading then saving then loading produces identical state",
      "validates": [
        "01-REQ-1.1"
      ]
    }
  ],
  "execution_paths": [
    {
      "id": "01-PATH-1",
      "title": "Load spec from disk",
      "steps": [
        {
          "actor": "consumer",
          "action": "call load_spec(dir)"
        },
        {
          "actor": "system",
          "action": "read and parse all four artifact files"
        }
      ]
    }
  ],
  "error_handling": [
    {
      "id": "01-ERR-1",
      "condition": "Artifact file missing",
      "behavior": "Raise LoadError listing missing files",
      "requirement_id": "01-REQ-1.E1"
    }
  ]
}`

	testSpecJSON := `{
  "$schema": "https://agent-fox.dev/schemas/test_spec.v1.json",
  "spec_id": "01",
  "spec_name": "test_feature",
  "schema_version": 1,
  "test_cases": [
    {
      "id": "TS-01-1",
      "requirement_id": "01-REQ-1.1",
      "kind": "unit",
      "description": "Spec type exports all four artifacts",
      "preconditions": [],
      "input": {},
      "expected": {
        "has_prd": true,
        "has_requirements": true,
        "has_test_spec": true,
        "has_tasks": true
      },
      "assertion_pseudocode": "spec = load_spec(dir); assert spec.prd is not None"
    }
  ],
  "property_tests": [
    {
      "id": "TS-01-P1",
      "property_id": "01-PROP-1",
      "validates": [
        "01-REQ-1.1"
      ],
      "description": "Round-trip idempotency",
      "for_any_strategy": "valid golden fixture spec folder",
      "invariant_check": "load(save(load(fixture))) == load(fixture)"
    }
  ],
  "edge_case_tests": [
    {
      "id": "TS-01-E1",
      "requirement_id": "01-REQ-1.E1",
      "kind": "unit",
      "description": "Missing file raises LoadError",
      "preconditions": [
        "directory with missing test_spec.json"
      ],
      "input": {
        "missing_file": "test_spec.json"
      },
      "expected": {
        "error_type": "LoadError"
      },
      "assertion_pseudocode": "with raises(LoadError): load_spec(incomplete_dir)"
    }
  ],
  "smoke_tests": [
    {
      "id": "TS-01-SMOKE-1",
      "execution_path_id": "01-PATH-1",
      "description": "Load spec from disk end-to-end",
      "trigger": "load_spec(golden_dir)",
      "real_components": [
        "io",
        "models"
      ],
      "mockable": [],
      "expected_effects": [
        "Returns Spec with all four artifacts populated"
      ]
    }
  ],
  "coverage": {
    "requirements_covered": [
      "01-REQ-1.1",
      "01-REQ-1.E1"
    ],
    "properties_covered": [
      "01-PROP-1"
    ],
    "paths_covered": [
      "01-PATH-1"
    ],
    "gaps": []
  }
}`

	tasksJSON := `{
  "$schema": "https://agent-fox.dev/schemas/tasks.v1.json",
  "spec_id": "01",
  "spec_name": "test_feature",
  "schema_version": 1,
  "test_commands": {
    "spec_tests": "pytest -q tests/",
    "all_tests": "pytest -q",
    "linter": "ruff check"
  },
  "dependencies": [],
  "task_groups": [
    {
      "id": 1,
      "kind": "tests",
      "title": "Write failing spec tests",
      "subtasks": [
        {
          "id": "1.1",
          "title": "Create test infrastructure",
          "details": [
            "Set up test files and fixtures"
          ],
          "test_spec_refs": [
            "TS-01-1"
          ],
          "requirement_refs": [
            "01-REQ-1.1"
          ],
          "state": "pending",
          "optional": false
        }
      ],
      "verification": {
        "id": "1.V",
        "checks": [
          "All spec tests exist and are syntactically valid",
          "All spec tests FAIL (no implementation yet)"
        ]
      }
    }
  ],
  "traceability": [
    {
      "requirement_id": "01-REQ-1.1",
      "test_spec_id": "TS-01-1",
      "task_id": "1.1",
      "test_path": null
    }
  ]
}`

	files := map[string]string{
		"prd.md":            prd,
		"requirements.json": reqJSON,
		"test_spec.json":    testSpecJSON,
		"tasks.json":        tasksJSON,
	}

	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}
}

// writeSpecFixturesNoIntent writes minimal valid spec fixtures to a directory
// but without a ## Intent section in prd.md. Used to test that Transition
// rejects activation when ## Intent is absent.
func writeSpecFixturesNoIntent(t *testing.T, dir string) {
	t.Helper()

	prd := `---
spec_id: "01"
spec_name: "test_feature"
title: "Test Feature"
status: "draft"
created_at: "2026-01-01T00:00:00Z"
updated_at: "2026-01-01T00:00:00Z"
owner: "test-author"
source: "https://github.com/test/repo/issues/1"
supersedes: []
tags: ["test"]
intent_hash: null
schema_version: 1
---
# Test Feature

## Goals

- Validate loading and saving of specs.
- Ensure cross-file integrity checks work.

## Non-goals

- Production deployment.
`

	reqJSON := `{
  "$schema": "https://agent-fox.dev/schemas/requirements.v1.json",
  "spec_id": "01",
  "spec_name": "test_feature",
  "schema_version": 1,
  "introduction": "The test feature validates the spec library.",
  "glossary": {
    "spec": "A four-artifact package representing one feature.",
    "system": "The afspec library."
  },
  "requirements": [
    {
      "id": "01-REQ-1",
      "title": "Data Model",
      "user_story": {
        "role": "developer",
        "goal": "have typed models for spec artifacts",
        "benefit": "type safety when working with specs"
      },
      "acceptance_criteria": [
        {
          "id": "01-REQ-1.1",
          "ears_pattern": "event_driven",
          "trigger": "a spec is loaded from disk",
          "system": "the system",
          "action": "return a populated Spec instance",
          "return_contract": "a Spec instance"
        }
      ],
      "edge_cases": [
        {
          "id": "01-REQ-1.E1",
          "ears_pattern": "unwanted",
          "error_condition": "the spec directory is missing a required file",
          "system": "the system",
          "action": "raise a LoadError identifying the missing file",
          "return_contract": "raises LoadError with message listing the missing file path"
        }
      ]
    }
  ],
  "correctness_properties": [],
  "execution_paths": [],
  "error_handling": []
}`

	testSpecJSON := `{
  "$schema": "https://agent-fox.dev/schemas/test_spec.v1.json",
  "spec_id": "01",
  "spec_name": "test_feature",
  "schema_version": 1,
  "test_cases": [],
  "property_tests": [],
  "edge_case_tests": [],
  "smoke_tests": [],
  "coverage": {
    "requirements_covered": [],
    "properties_covered": [],
    "paths_covered": [],
    "gaps": []
  }
}`

	tasksJSON := `{
  "$schema": "https://agent-fox.dev/schemas/tasks.v1.json",
  "spec_id": "01",
  "spec_name": "test_feature",
  "schema_version": 1,
  "test_commands": {
    "spec_tests": "pytest -q tests/",
    "all_tests": "pytest -q",
    "linter": "ruff check"
  },
  "dependencies": [],
  "task_groups": [
    {"id":1,"kind":"tests","title":"Write Tests","subtasks":[],"verification":{"id":"1.V","checks":["all tests pass"]}},
    {"id":2,"kind":"wiring_verification","title":"Wiring Verification","subtasks":[],"verification":{"id":"2.V","checks":["no stubs remain"]}}
  ],
  "traceability": []
}`

	files := map[string]string{
		"prd.md":            prd,
		"requirements.json": reqJSON,
		"test_spec.json":    testSpecJSON,
		"tasks.json":        tasksJSON,
	}

	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Issue #72: deep copy tests for Transition()
// ---------------------------------------------------------------------------

// writeSpecFixturesWithSupersedes writes minimal valid spec fixtures to dir
// with a non-empty Supersedes slice (["00"]) to enable element-assignment
// mutation tests.
func writeSpecFixturesWithSupersedes(t *testing.T, dir string) {
	t.Helper()

	prd := `---
spec_id: "01"
spec_name: "test_feature"
title: "Test Feature"
status: "draft"
created_at: "2026-01-01T00:00:00Z"
updated_at: "2026-01-01T00:00:00Z"
owner: "test-author"
source: "https://github.com/test/repo/issues/1"
supersedes: ["00"]
tags: ["test"]
intent_hash: null
schema_version: 1
---
# Test Feature

## Intent

Build a test feature that validates the spec library works correctly.

## Goals

- Validate loading and saving of specs.

## Non-goals

- Production deployment.
`

	reqJSON := `{
  "$schema": "https://agent-fox.dev/schemas/requirements.v1.json",
  "spec_id": "01",
  "spec_name": "test_feature",
  "schema_version": 1,
  "introduction": "The test feature validates the spec library.",
  "glossary": {"spec": "A four-artifact package."},
  "requirements": [
    {
      "id": "01-REQ-1",
      "title": "Data Model",
      "user_story": {
        "role": "developer",
        "goal": "have typed models",
        "benefit": "type safety"
      },
      "acceptance_criteria": [
        {
          "id": "01-REQ-1.1",
          "ears_pattern": "event_driven",
          "trigger": "a spec is loaded",
          "system": "the system",
          "action": "return a populated Spec",
          "return_contract": "a Spec instance"
        }
      ],
      "edge_cases": []
    }
  ],
  "correctness_properties": [],
  "execution_paths": [],
  "error_handling": []
}`

	testSpecJSON := `{
  "$schema": "https://agent-fox.dev/schemas/test_spec.v1.json",
  "spec_id": "01",
  "spec_name": "test_feature",
  "schema_version": 1,
  "test_cases": [],
  "property_tests": [],
  "edge_case_tests": [],
  "smoke_tests": [],
  "coverage": {
    "requirements_covered": [],
    "properties_covered": [],
    "paths_covered": [],
    "gaps": []
  }
}`

	tasksJSON := `{
  "$schema": "https://agent-fox.dev/schemas/tasks.v1.json",
  "spec_id": "01",
  "spec_name": "test_feature",
  "schema_version": 1,
  "test_commands": {
    "spec_tests": "pytest -q tests/",
    "all_tests": "pytest -q",
    "linter": "ruff check"
  },
  "dependencies": [],
  "task_groups": [
    {"id":1,"kind":"tests","title":"Write Tests","subtasks":[],"verification":{"id":"1.V","checks":["all tests pass"]}},
    {"id":2,"kind":"wiring_verification","title":"Wiring","subtasks":[],"verification":{"id":"2.V","checks":["no stubs"]}}
  ],
  "traceability": []
}`

	supersedes_files := map[string]string{
		"prd.md":            prd,
		"requirements.json": reqJSON,
		"test_spec.json":    testSpecJSON,
		"tasks.json":        tasksJSON,
	}
	for name, content := range supersedes_files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}
}

// TestTransition_SupersedesSliceIsolation verifies NS-REQ-1 / TS-NS-1:
// after Transition(), mutating the returned spec's Supersedes slice does not
// affect the original spec's Supersedes slice (and vice versa).
func TestTransition_SupersedesSliceIsolation(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()
	writeSpecFixturesWithSupersedes(t, tmpDir)

	original, err := LoadSpec(tmpDir)
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	// Precondition: non-empty Supersedes so element assignment is possible.
	if len(original.Supersedes) == 0 {
		t.Fatal("test precondition failed: Supersedes slice must be non-empty")
	}
	origSupersedes0 := original.Supersedes[0]
	origLen := len(original.Supersedes)

	returned, err := original.Transition("active", tmpDir)
	if err != nil {
		t.Fatalf("Transition draft→active failed: %v", err)
	}

	// Append to returned — must not grow original.
	returned.Supersedes = append(returned.Supersedes, "99")
	if len(original.Supersedes) != origLen {
		t.Errorf("original.Supersedes length changed from %d to %d after append to returned",
			origLen, len(original.Supersedes))
	}

	// Element assignment on returned — must not mutate original.
	returned.Supersedes[0] = "mutated"
	if original.Supersedes[0] != origSupersedes0 {
		t.Errorf("original.Supersedes[0] changed to %q after element assignment on returned",
			original.Supersedes[0])
	}
}

// TestTransition_TagsSliceIsolation verifies NS-REQ-2 / TS-NS-2:
// after Transition(), mutating the returned spec's Tags slice does not affect
// the original spec's Tags slice.
func TestTransition_TagsSliceIsolation(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()
	// Default fixture has tags: ["test"], sufficient for this test.
	original := createDraftSpec(t, tmpDir)

	if len(original.Tags) == 0 {
		t.Fatal("test precondition failed: Tags slice must be non-empty")
	}
	origTag0 := original.Tags[0]
	origTagLen := len(original.Tags)

	returned, err := original.Transition("active", tmpDir)
	if err != nil {
		t.Fatalf("Transition draft→active failed: %v", err)
	}

	// Append extra tag — must not affect original.
	returned.Tags = append(returned.Tags, "extra")
	if len(original.Tags) != origTagLen {
		t.Errorf("original.Tags length changed from %d to %d after append to returned",
			origTagLen, len(original.Tags))
	}

	// Element assignment — must not mutate original.
	returned.Tags[0] = "mutated"
	if original.Tags[0] != origTag0 {
		t.Errorf("original.Tags[0] changed to %q after element assignment on returned",
			original.Tags[0])
	}
}

// TestTransition_RequirementsArtifactIsolation verifies NS-REQ-3 / TS-NS-3:
// after Transition(), mutating the returned spec's Requirements.Requirements
// slice does not affect the original spec's Requirements artifact.
func TestTransition_RequirementsArtifactIsolation(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()
	// Default fixture has one requirement entry.
	original := createDraftSpec(t, tmpDir)

	if original.Requirements == nil {
		t.Fatal("test precondition failed: Requirements must be non-nil")
	}
	origLen := len(original.Requirements.Requirements)
	if origLen == 0 {
		t.Fatal("test precondition failed: Requirements.Requirements must be non-empty")
	}

	returned, err := original.Transition("active", tmpDir)
	if err != nil {
		t.Fatalf("Transition draft→active failed: %v", err)
	}

	// Append a duplicate of the first requirement as a dummy entry.
	returned.Requirements.Requirements = append(
		returned.Requirements.Requirements,
		returned.Requirements.Requirements[0],
	)

	// Original artifact must be unaffected.
	if len(original.Requirements.Requirements) != origLen {
		t.Errorf("original.Requirements.Requirements length changed from %d to %d after mutation of returned",
			origLen, len(original.Requirements.Requirements))
	}
}
