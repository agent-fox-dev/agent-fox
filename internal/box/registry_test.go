package box

import (
	"slices"
	"testing"
)

// TestTheRegistryHoldsFourProviders_TS_10_1 verifies TS-10-1:
// landlock, podman, local and none are all registered, only landlock and podman
// isolate, and only none works in place.
// Verifies: 10-REQ-1.1
func TestTheRegistryHoldsFourProviders_TS_10_1(t *testing.T) {
	want := map[string]struct{ isolates, inPlace bool }{
		"landlock": {isolates: true},
		"podman":   {isolates: true},
		"local":    {},
		"none":     {inPlace: true},
	}
	for name, w := range want {
		e, err := Default.Lookup(name)
		if err != nil {
			t.Errorf("Lookup(%s): %v", name, err)
			continue
		}
		if e.Name != name {
			t.Errorf("Lookup(%s) is the entry %q", name, e.Name)
		}
		if got := e.Isolates(); got != w.isolates {
			t.Errorf("%s: Isolates() = %v, want %v", name, got, w.isolates)
		}
		if e.InPlace != w.inPlace {
			t.Errorf("%s: InPlace = %v, want %v", name, e.InPlace, w.inPlace)
		}
		// A provider that does not isolate raises box_unconfined, and one that does not.
		if e.Unconfined == w.isolates {
			t.Errorf("%s: Unconfined = %v, but Isolates() = %v", name, e.Unconfined, e.Isolates())
		}
	}
	if got := Default.Names(); !slices.Equal(got, []string{"landlock", "local", "none", "podman"}) {
		t.Errorf("the default registry holds %v, want the four providers", got)
	}
}
