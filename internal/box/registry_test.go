package box

import (
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestDefault_OffersPodmanThatIsolatesAndLocalThatDoesNot(t *testing.T) {
	if got := Default.Names(); !slices.Equal(got, []string{"local", "podman"}) {
		t.Errorf("Default.Names() = %v, want local and podman", got)
	}
	for name, isolates := range map[string]bool{"podman": true, "local": false} {
		e, err := Default.Lookup(name)
		if err != nil || e.Name != name || e.Isolates() != isolates {
			t.Errorf("Lookup(%s) = %q, isolates %v, %v; want isolates %v", name, e.Name, e.Isolates(), err, isolates)
		}
	}
}

func TestLookup_AnUnknownNameIsAUsageFailureThatListsTheProviders(t *testing.T) {
	_, err := Default.Lookup("docker")

	f := asFailure(t, err)
	if f.Category != toolio.CategoryUsage || !strings.Contains(f.Message, `"docker"`) || !strings.Contains(f.Message, "local, podman") {
		t.Errorf("failure = %+v, want usage naming docker and listing local, podman", f)
	}
}

func TestRegister_ReplacesAProviderOfTheSameName(t *testing.T) {
	r := registry(Entry{Name: "x"}, Entry{Name: "x", Unconfined: true})

	e, err := r.Lookup("x")

	if err != nil || !e.Unconfined || !slices.Equal(r.Names(), []string{"x"}) {
		t.Errorf("Lookup(x) = %+v, %v with names %v, want the second entry alone", e, err, r.Names())
	}
}
