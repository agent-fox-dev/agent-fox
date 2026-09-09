package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- TS-08-12: Verify spec resolver matches by numeric prefix ---

// TestTS08_12_ResolveByNumericPrefix verifies that when the argument
// is purely numeric, the resolver matches entries whose directory name
// begins with that numeric prefix.
// Covers: TS-08-12, Requirement: 08-REQ-5.1
func TestTS08_12_ResolveByNumericPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(filepath.Join(specDir, "08_my_spec"), 0755); err != nil {
		t.Fatal(err)
	}

	path, err := resolveSpec(specDir, "08")
	if err != nil {
		t.Fatalf("resolveSpec(%q, %q) returned error: %v", specDir, "08", err)
	}

	expected := filepath.Join(specDir, "08_my_spec")
	if path != expected {
		t.Errorf("resolveSpec(%q, %q) = %q; want %q", specDir, "08", path, expected)
	}
}

// TestTS08_12_ResolveNumericPrefixMultipleDigits verifies resolution
// with a multi-digit numeric prefix.
// Covers: TS-08-12, 08-REQ-5.1
func TestTS08_12_ResolveNumericPrefixMultipleDigits(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(filepath.Join(specDir, "123_some_spec"), 0755); err != nil {
		t.Fatal(err)
	}
	// Also create a non-matching directory
	if err := os.MkdirAll(filepath.Join(specDir, "45_other_spec"), 0755); err != nil {
		t.Fatal(err)
	}

	path, err := resolveSpec(specDir, "123")
	if err != nil {
		t.Fatalf("resolveSpec(%q, %q) returned error: %v", specDir, "123", err)
	}

	expected := filepath.Join(specDir, "123_some_spec")
	if path != expected {
		t.Errorf("resolveSpec(%q, %q) = %q; want %q", specDir, "123", path, expected)
	}
}

// --- TS-08-13: Verify spec resolver matches by exact directory name ---

// TestTS08_13_ResolveByExactName verifies that when the argument is a
// non-numeric string, the resolver matches by exact directory name.
// Covers: TS-08-13, Requirement: 08-REQ-5.2
func TestTS08_13_ResolveByExactName(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(filepath.Join(specDir, "my_spec"), 0755); err != nil {
		t.Fatal(err)
	}

	path, err := resolveSpec(specDir, "my_spec")
	if err != nil {
		t.Fatalf("resolveSpec(%q, %q) returned error: %v", specDir, "my_spec", err)
	}

	expected := filepath.Join(specDir, "my_spec")
	if path != expected {
		t.Errorf("resolveSpec(%q, %q) = %q; want %q", specDir, "my_spec", path, expected)
	}
}

// TestTS08_13_ResolveExactNameWithNumber verifies that a name like
// "08_my_spec" matches by exact name, not numeric prefix.
// Covers: TS-08-13, 08-REQ-5.2
func TestTS08_13_ResolveExactNameWithNumber(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(filepath.Join(specDir, "08_my_spec"), 0755); err != nil {
		t.Fatal(err)
	}

	// "08_my_spec" is non-numeric (contains underscore), so it should
	// match by exact directory name
	path, err := resolveSpec(specDir, "08_my_spec")
	if err != nil {
		t.Fatalf("resolveSpec(%q, %q) returned error: %v", specDir, "08_my_spec", err)
	}

	expected := filepath.Join(specDir, "08_my_spec")
	if path != expected {
		t.Errorf("resolveSpec(%q, %q) = %q; want %q", specDir, "08_my_spec", path, expected)
	}
}

// --- 08-REQ-5.E1: No matches found ---

// TestTS08_ResolveNoMatches verifies that the resolver returns a
// descriptive error when no entries match.
// Covers: 08-REQ-5.E1
func TestTS08_ResolveNoMatches(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Create a directory that won't match
	if err := os.MkdirAll(filepath.Join(specDir, "01_something"), 0755); err != nil {
		t.Fatal(err)
	}

	_, err := resolveSpec(specDir, "99")
	if err == nil {
		t.Fatal("resolveSpec() with no matches returned nil error; want error")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "99") {
		t.Errorf("error message = %q; want it to reference the argument %q", errMsg, "99")
	}
}

// --- 08-REQ-5.E2: Multiple matches for numeric prefix ---

// TestTS08_ResolveAmbiguousNumericPrefix verifies that the resolver
// returns an error listing all ambiguous matches.
// Covers: 08-REQ-5.E2
func TestTS08_ResolveAmbiguousNumericPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	for _, name := range []string{"08_spec_a", "08_spec_b"} {
		if err := os.MkdirAll(filepath.Join(specDir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}

	_, err := resolveSpec(specDir, "08")
	if err == nil {
		t.Fatal("resolveSpec() with ambiguous prefix returned nil error; want error")
	}
	errMsg := err.Error()
	// Error should list the conflicting names
	if !strings.Contains(errMsg, "08_spec_a") || !strings.Contains(errMsg, "08_spec_b") {
		t.Errorf("error message = %q; want it to list ambiguous matches", errMsg)
	}
}

// --- 08-REQ-5.E3: Spec directory does not exist ---

// TestTS08_ResolveSpecDirNotExists verifies that the resolver returns
// an error when the spec directory doesn't exist.
// Covers: 08-REQ-5.E3
func TestTS08_ResolveSpecDirNotExists(t *testing.T) {
	_, err := resolveSpec("/nonexistent/spec/dir", "anything")
	if err == nil {
		t.Fatal("resolveSpec() with nonexistent dir returned nil error; want error")
	}
}

// --- NS-REQ-1/2/4: Numeric prefix requires underscore separator ---

// TestTS_NS_1_SingleDigitMatchesExactly verifies that resolveSpec("1") returns
// only the spec whose name begins with "1_", not "10_" or "11_".
// Covers: NS-REQ-1, TS-NS-1
func TestTS_NS_1_SingleDigitMatchesExactly(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	for _, name := range []string{"1_foo", "10_bar", "11_baz"} {
		if err := os.MkdirAll(filepath.Join(specDir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}

	path, err := resolveSpec(specDir, "1")
	if err != nil {
		t.Fatalf("resolveSpec(%q, %q) returned error: %v", specDir, "1", err)
	}

	expected := filepath.Join(specDir, "1_foo")
	if path != expected {
		t.Errorf("resolveSpec(%q, %q) = %q; want %q", specDir, "1", path, expected)
	}
}

// TestTS_NS_2_MultiDigitDoesNotMatchLongerPrefix verifies that resolveSpec("10")
// returns "10_bar" and does not match "1_foo".
// Covers: NS-REQ-2, TS-NS-2
func TestTS_NS_2_MultiDigitDoesNotMatchLongerPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	for _, name := range []string{"1_foo", "10_bar"} {
		if err := os.MkdirAll(filepath.Join(specDir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}

	path, err := resolveSpec(specDir, "10")
	if err != nil {
		t.Fatalf("resolveSpec(%q, %q) returned error: %v", specDir, "10", err)
	}

	expected := filepath.Join(specDir, "10_bar")
	if path != expected {
		t.Errorf("resolveSpec(%q, %q) = %q; want %q", specDir, "10", path, expected)
	}
}

// TestTS_NS_4_NoMatchWhenOnlyLongerPrefixExists verifies that resolveSpec("1")
// returns a not-found error when only "10_bar" exists.
// Covers: NS-REQ-4, TS-NS-4
func TestTS_NS_4_NoMatchWhenOnlyLongerPrefixExists(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(filepath.Join(specDir, "10_bar"), 0755); err != nil {
		t.Fatal(err)
	}

	_, err := resolveSpec(specDir, "1")
	if err == nil {
		t.Fatal("resolveSpec() with no matching spec returned nil error; want error")
	}
	if !strings.Contains(err.Error(), "no spec matching") {
		t.Errorf("error message = %q; want it to contain %q", err.Error(), "no spec matching")
	}
}

// --- 08-PROP-3: Deterministic resolution ---

// TestTS08_ResolveIsDeterministic verifies that the resolver always
// returns the same path for an unambiguous input.
// Covers: 08-PROP-3
func TestTS08_ResolveIsDeterministic(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(filepath.Join(specDir, "42_unique_spec"), 0755); err != nil {
		t.Fatal(err)
	}

	// Call resolveSpec multiple times
	var results []string
	for i := 0; i < 5; i++ {
		path, err := resolveSpec(specDir, "42")
		if err != nil {
			t.Fatalf("iteration %d: resolveSpec() returned error: %v", i, err)
		}
		results = append(results, path)
	}

	// All results should be identical
	for i := 1; i < len(results); i++ {
		if results[i] != results[0] {
			t.Errorf("resolveSpec() returned different results: %q vs %q", results[0], results[i])
		}
	}
}
