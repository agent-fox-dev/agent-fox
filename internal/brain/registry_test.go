package brain

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestRegistry_FindsABrainByNameAndRefusesAnyOther(t *testing.T) {
	r := NewRegistry()
	var built []string
	for _, name := range []string{"zeta", "alpha"} {
		r.Register(Entry{Name: name, New: func(Options) (Brain, error) { built = append(built, name); return idle, nil }})
	}

	if names := r.Names(); !slices.Equal(names, []string{"alpha", "zeta"}) {
		t.Errorf("Names = %v, want them sorted", names)
	}
	if b, err := r.New("alpha", Options{}); err != nil || b == nil || !slices.Equal(built, []string{"alpha"}) {
		t.Errorf("New(alpha) = %v, %v, built %v", b, err, built)
	}
	b, err := r.New("claude-x", Options{})
	f := asFailure(t, err)
	if b != nil || f.Category != toolio.CategoryUsage || !strings.Contains(f.Message, `"claude-x"`) || !strings.Contains(f.Message, "alpha, zeta") {
		t.Errorf("New(claude-x) = %v, %+v; want a usage failure that names it and lists the brains", b, f)
	}
	if len(built) != 1 {
		t.Errorf("a brain was built for a name that is not registered: %v", built)
	}
	r.Register(Entry{Name: "alpha", CostSource: CostReported})
	if e, err := r.Lookup("alpha"); err != nil || e.CostSource != CostReported {
		t.Errorf("a second registration of alpha = %+v, %v; want it to replace the first", e, err)
	}
}

func TestDefaultRegistry_HoldsAgentkitAndScripted(t *testing.T) {
	if names := Default.Names(); !slices.Equal(names, []string{AgentkitName, ScriptedName}) {
		t.Errorf("Default.Names = %v", names)
	}
	agentkit, _ := Default.Lookup(AgentkitName)
	if agentkit.CostSource != CostMeasured || !agentkit.DriverInBox || !slices.Equal(agentkit.Credentials, []string{VendorKeyVar}) {
		t.Errorf("agentkit = %+v, want measured cost, a driver in the box, and the vendor key", agentkit)
	}
	scripted, _ := Default.Lookup(ScriptedName)
	if scripted.CostSource != CostNone || scripted.DriverInBox || len(scripted.Credentials) != 0 {
		t.Errorf("scripted = %+v, want no cost, the host, and no credential", scripted)
	}
}

func TestBuildBoxEnv_IsTheHostValuesOfTheCredentialsOfThePlacement(t *testing.T) {
	host := envOf(map[string]string{"KEY": "k", "OTHER": "o", "EMPTY": "", "GITHUB_TOKEN": "ghp"})
	for _, tc := range []struct {
		name  string
		names []string
		p     Placement
		want  map[string]string
	}{
		{"in a box, the names that are set", []string{"KEY", "EMPTY", "UNSET"}, PlacementBox, map[string]string{"KEY": "k", "EMPTY": ""}},
		{"on the host, none", []string{"KEY"}, PlacementHost, map[string]string{}},
		{"no names, none", nil, PlacementBox, map[string]string{}},
	} {
		got := BuildBoxEnv(Entry{Credentials: tc.names}, tc.p, host)
		if got == nil || !maps.Equal(got, tc.want) {
			t.Errorf("%s: env = %#v, want %v", tc.name, got, tc.want)
		}
	}

	t.Setenv("AF_TEST_BOX_ENV", "value")
	if got := BuildBoxEnv(Entry{Credentials: []string{"AF_TEST_BOX_ENV", "AF_TEST_BOX_ENV_UNSET"}}, PlacementBox, nil); !maps.Equal(got, map[string]string{"AF_TEST_BOX_ENV": "value"}) {
		t.Errorf("with no lookup: env = %v, want the variable of the process that is set", got)
	}
}
