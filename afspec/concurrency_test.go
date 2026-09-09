package afspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Subtask 2.6: Concurrency model documentation and immutability
// ---------------------------------------------------------------------------

// TestSpecImmutability_TransitionReturnsNewCopy verifies that mutation
// methods on Spec return new Spec copies rather than modifying the
// receiver in place.
// Test Spec: TS-01-54, Requirement: 01-REQ-28.1
func TestSpecImmutability_TransitionReturnsNewCopy(t *testing.T) {
	defer requireImplemented(t)

	tmpDir := t.TempDir()
	writeSpecFixtures(t, tmpDir)

	original, err := LoadSpec(tmpDir)
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}
	if original.Status != "draft" {
		t.Fatalf("expected draft status, got %q", original.Status)
	}

	// Store original status before mutation
	originalStatus := original.Status

	updated, err := original.Transition("active", tmpDir)
	if err != nil {
		t.Fatalf("Transition failed: %v", err)
	}

	// The original spec variable should still have the old status
	if original.Status != originalStatus {
		t.Errorf("original.Status = %q, want %q (should not be modified by Transition)",
			original.Status, originalStatus)
	}

	// The returned spec should have the new status
	if updated.Status != "active" {
		t.Errorf("updated.Status = %q, want %q", updated.Status, "active")
	}

	// They should be different objects
	if original == updated {
		t.Error("Transition returned the same pointer — expected a new copy")
	}
}

// TestGoroutineSafetyDocumented verifies that the afspec package-level
// godoc contains a statement about lack of goroutine-safety.
// Test Spec: TS-01-55, Requirement: 01-REQ-28.2
//
// Note: This test inspects the Go source code documentation rather than
// runtime behavior, since the requirement is about documentation.
func TestGoroutineSafetyDocumented(t *testing.T) {
	// Find the Go source files in the current package directory
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() failed: %v", err)
	}

	goFiles, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("filepath.Glob failed: %v", err)
	}

	goroutineTerms := []string{"goroutine", "concurrent", "synchronize", "thread-safe", "not safe for concurrent"}
	found := false

	for _, gf := range goFiles {
		if strings.HasSuffix(gf, "_test.go") {
			continue
		}
		content, err := os.ReadFile(gf)
		if err != nil {
			continue
		}
		lower := strings.ToLower(string(content))
		for _, term := range goroutineTerms {
			if strings.Contains(lower, strings.ToLower(term)) {
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		t.Error("package-level godoc does not contain a statement about goroutine-safety; " +
			"expected a mention of 'goroutine', 'concurrent', 'synchronize', or similar")
	}
}
