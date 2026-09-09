package afspec

import (
	"strings"
	"testing"
)

// strPtr returns a pointer to the given string. Useful for constructing
// optional *string fields in test fixtures.
func strPtr(s string) *string {
	return &s
}

// assertContains is a test helper that fails if s does not contain substr.
func assertContains(t *testing.T, s, substr, label string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("%s: expected output to contain %q, but it did not.\nOutput (first 500 chars): %s",
			label, substr, truncate(s, 500))
	}
}

// assertNotContains is a test helper that fails if s contains substr.
func assertNotContains(t *testing.T, s, substr, label string) {
	t.Helper()
	if strings.Contains(s, substr) {
		t.Errorf("%s: expected output NOT to contain %q, but it did", label, substr)
	}
}

// truncate returns the first n characters of s, or s if shorter.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// requireImplemented recovers from panics caused by "not implemented" stubs
// and converts them to test failures. Place as a deferred call at the top of
// any test that calls unimplemented functions.
//
//	func TestFoo(t *testing.T) {
//	    defer requireImplemented(t)
//	    // ... test body that may call unimplemented stubs
//	}
func requireImplemented(t *testing.T) {
	t.Helper()
	if r := recover(); r != nil {
		t.Fatalf("not yet implemented (panic: %v)", r)
	}
}
