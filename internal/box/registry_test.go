package box

import (
	"slices"
	"testing"
)

// TestTheRegistryHoldsTwoProviders_TS_10_1 verifies TS-10-1 as it stands: podman and local
// are registered, and only podman isolates.
// Verifies: 10-REQ-1.1
func TestTheRegistryHoldsTwoProviders_TS_10_1(t *testing.T) {
	want := map[string]bool{"podman": true, "local": false}
	for name, isolates := range want {
		e, err := Default.Lookup(name)
		if err != nil {
			t.Errorf("Lookup(%s): %v", name, err)
			continue
		}
		if e.Name != name {
			t.Errorf("Lookup(%s) is the entry %q", name, e.Name)
		}
		if got := e.Isolates(); got != isolates {
			t.Errorf("%s: Isolates() = %v, want %v", name, got, isolates)
		}
		// A provider that does not isolate raises box_unconfined, and one that does not.
		if e.Unconfined == isolates {
			t.Errorf("%s: Unconfined = %v, but Isolates() = %v", name, e.Unconfined, e.Isolates())
		}
	}
	if got := Default.Names(); !slices.Equal(got, []string{"local", "podman"}) {
		t.Errorf("the default registry holds %v, want the two providers", got)
	}
}
