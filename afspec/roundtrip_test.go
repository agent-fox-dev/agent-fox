package afspec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestRoundTrip_ValidSpec verifies that LoadSpec followed by Save produces
// byte-for-byte identical output to the original fixture files.
// Test Spec: TS-01-52, Requirement: 01-REQ-27.1
func TestRoundTrip_ValidSpec(t *testing.T) {
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

	artifacts := []string{
		"prd.md",
		"requirements.json",
		"test_spec.json",
		"tasks.json",
	}

	for _, name := range artifacts {
		t.Run(name, func(t *testing.T) {
			expected, err := os.ReadFile(filepath.Join(fixtureDir, name))
			if err != nil {
				t.Fatalf("failed to read expected: %v", err)
			}

			actual, err := os.ReadFile(filepath.Join(tmpDir, name))
			if err != nil {
				t.Fatalf("failed to read actual: %v", err)
			}

			if diff := cmp.Diff(string(expected), string(actual)); diff != "" {
				t.Errorf("round-trip mismatch for %s (-want +got):\n%s", name, diff)
			}
		})
	}
}

// TestRoundTrip_DraftSpec verifies round-trip fidelity for the draft_spec fixture.
// Test Spec: TS-01-52, Requirement: 01-REQ-27.1
func TestRoundTrip_DraftSpec(t *testing.T) {
	defer requireImplemented(t)

	fixtureDir := "testdata/draft_spec"
	if _, err := os.Stat(fixtureDir); os.IsNotExist(err) {
		t.Skip("testdata/draft_spec not available")
	}

	spec, err := LoadSpec(fixtureDir)
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	tmpDir := t.TempDir()
	if err := spec.Save(tmpDir); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	artifacts := []string{
		"prd.md",
		"requirements.json",
		"test_spec.json",
		"tasks.json",
	}

	for _, name := range artifacts {
		t.Run(name, func(t *testing.T) {
			expected, err := os.ReadFile(filepath.Join(fixtureDir, name))
			if err != nil {
				t.Fatalf("failed to read expected: %v", err)
			}

			actual, err := os.ReadFile(filepath.Join(tmpDir, name))
			if err != nil {
				t.Fatalf("failed to read actual: %v", err)
			}

			if diff := cmp.Diff(string(expected), string(actual)); diff != "" {
				t.Errorf("round-trip mismatch for %s (-want +got):\n%s", name, diff)
			}
		})
	}
}

// TestRoundTrip_PRDFrontmatter verifies that the PRD frontmatter is rendered
// using the hand-written renderer with fixed field order, producing
// byte-for-byte identical output to the Python library.
// Test Spec: TS-01-53, Requirement: 01-REQ-27.2
func TestRoundTrip_PRDFrontmatter(t *testing.T) {
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

	expected, err := os.ReadFile(filepath.Join(fixtureDir, "prd.md"))
	if err != nil {
		t.Fatalf("failed to read expected prd.md: %v", err)
	}

	actual, err := os.ReadFile(filepath.Join(tmpDir, "prd.md"))
	if err != nil {
		t.Fatalf("failed to read actual prd.md: %v", err)
	}

	if diff := cmp.Diff(string(expected), string(actual)); diff != "" {
		t.Errorf("prd.md frontmatter round-trip mismatch (-want +got):\n%s", diff)
	}
}

// TestRoundTrip_AllFixtures verifies round-trip fidelity across all available
// fixtures in testdata/.
// Test Spec: TS-01-52, Requirement: 01-REQ-27.1
func TestRoundTrip_AllFixtures(t *testing.T) {
	defer requireImplemented(t)

	fixtures := []string{
		"./../testdata/valid_spec",
	}

	// Add draft_spec if available
	if _, err := os.Stat("testdata/draft_spec"); err == nil {
		fixtures = append(fixtures, "testdata/draft_spec")
	}

	artifacts := []string{
		"prd.md",
		"requirements.json",
		"test_spec.json",
		"tasks.json",
	}

	for _, fixtureDir := range fixtures {
		t.Run(filepath.Base(fixtureDir), func(t *testing.T) {
			defer requireImplemented(t)

			spec, err := LoadSpec(fixtureDir)
			if err != nil {
				t.Fatalf("LoadSpec(%s) failed: %v", fixtureDir, err)
			}

			tmpDir := t.TempDir()
			if err := spec.Save(tmpDir); err != nil {
				t.Fatalf("Save failed: %v", err)
			}

			for _, name := range artifacts {
				t.Run(name, func(t *testing.T) {
					expected, err := os.ReadFile(filepath.Join(fixtureDir, name))
					if err != nil {
						t.Fatalf("failed to read expected: %v", err)
					}

					actual, err := os.ReadFile(filepath.Join(tmpDir, name))
					if err != nil {
						t.Fatalf("failed to read actual: %v", err)
					}

					if diff := cmp.Diff(string(expected), string(actual)); diff != "" {
						t.Errorf("round-trip mismatch for %s (-want +got):\n%s", name, diff)
					}
				})
			}
		})
	}
}
