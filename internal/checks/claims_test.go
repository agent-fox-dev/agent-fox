package checks

import "testing"

// Nothing the model writes may assert a verification result (#72).
func TestStripClaimsRemovesVerificationAssertions(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Skip the expiry check on cached credentials. `make check` passes.", "Skip the expiry check on cached credentials."},
		{"Fixes the counter. All tests pass.", "Fixes the counter."},
		{"The linter is clean and the build succeeds.", ""},
		{"Adds a guard.\nThe test suite is green.\nRenames the helper.", "Adds a guard.\n\nRenames the helper."},
		{"go test ./... passed on my machine", ""},
		{"Adds a test for the nil map. It asserts the count is one.", "Adds a test for the nil map. It asserts the count is one."},
		{"Route output to stderr.", "Route output to stderr."},
		{"", ""},
	} {
		if got := StripClaims(c.in); got != c.want {
			t.Errorf("StripClaims(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
