package afspec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureValidSpec is the canonical positive fixture: spec 01, a complete v2
// package that satisfies every rule of §10.2.
const fixtureValidSpec = "../testdata/valid_spec"

// fixtureV2Example is the worked example from the format specification.
const fixtureV2Example = "../testdata/v2_example"

// loadFixture loads a spec fixture or fails the test.
func loadFixture(t *testing.T, dir string) *Spec {
	t.Helper()
	spec, err := LoadSpec(dir)
	if err != nil {
		t.Fatalf("LoadSpec(%q) = %v; want no error", dir, err)
	}
	return spec
}

// copyFixture copies a fixture into a temporary directory so a test can save
// over it without touching the checked-in files. The returned path is the
// copy's directory.
func copyFixture(t *testing.T, dir string) string {
	t.Helper()
	dst := t.TempDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q) = %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("ReadFile(%q) = %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil {
			t.Fatalf("WriteFile(%q) = %v", e.Name(), err)
		}
	}
	return dst
}

// errorChecks returns the Check field of every error in the result.
func errorChecks(result ValidationResult) []string {
	checks := make([]string, 0, len(result.Errors))
	for _, e := range result.Errors {
		checks = append(checks, e.Check)
	}
	return checks
}

// hasCheck reports whether the result carries an error with the given rule.
func hasCheck(result ValidationResult, check string) bool {
	for _, e := range result.Errors {
		if e.Check == check {
			return true
		}
	}
	return false
}

// hasWarningContaining reports whether any warning message contains sub.
func hasWarningContaining(result ValidationResult, sub string) bool {
	for _, w := range result.Warnings {
		if contains(w.Message, sub) {
			return true
		}
	}
	return false
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// firstDifference describes where two texts diverge, so a round-trip failure
// points at the offending line instead of dumping both files.
func firstDifference(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		w, g := "<missing>", "<missing>"
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w != g {
			return fmt.Sprintf("line %d:\n  want: %q\n  got:  %q", i+1, w, g)
		}
	}
	return "texts are equal"
}
