package brain

import (
	"context"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/provider/anthropic"

	"github.com/agent-fox-dev/agentfox/internal/box"
)

// The brains of the matrix: scripted and agentkit, as the registry has them.
func matrixBrains(t *testing.T) []Entry {
	t.Helper()
	var out []Entry
	for _, name := range []string{ScriptedName, AgentkitName} {
		e, err := Default.Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func providerEntry(t *testing.T, name string) box.Entry {
	t.Helper()
	e, err := box.Default.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func envOf(m map[string]string) Getenv {
	return func(name string) (string, bool) { v, ok := m[name]; return v, ok }
}

// boxRequest is the request for a box that the matrix computes.
func boxRequest(t *testing.T, brain Entry, provider string, tools []string, env map[string]string, deadline time.Time) box.Request {
	t.Helper()
	return BoxRequest(BoxParams{
		Brain: brain, Provider: providerEntry(t, provider), Tools: tools, Getenv: envOf(env), Deadline: deadline,
		HostDir: "/work/checkout", RunID: "20261021_110000_abcdef", Tool: "fix",
	})
}

// wantPlacement is the placement the spec gives, by names.
func wantPlacement(brain, provider string) Placement {
	if brain == ScriptedName || provider == "local" {
		return PlacementHost
	}
	return PlacementBox
}

var providers = []string{"podman", "local"}

// TestPlacementIsAPureFunctionOfBrainAndProvider_TS_10_8 verifies TS-10-8: for every brain
// and provider, the placement is the one the spec gives, and the request carries it.
// Verifies: 10-REQ-2.1
func TestPlacementIsAPureFunctionOfBrainAndProvider_TS_10_8(t *testing.T) {
	for _, brain := range matrixBrains(t) {
		for _, provider := range providers {
			name := brain.Name + "/" + provider
			req := boxRequest(t, brain, provider, nil, nil, time.Time{})
			want := wantPlacement(brain.Name, provider)
			if req.Placement != string(want) {
				t.Errorf("%s: placement %q, want %q", name, req.Placement, want)
			}
			if got := PlacementOf(brain, providerEntry(t, provider)); got != want {
				t.Errorf("%s: PlacementOf = %q, want %q", name, got, want)
			}
			if req.Provider != provider || req.HostDir != "/work/checkout" || req.RunID != "20261021_110000_abcdef" || req.Tool != "fix" {
				t.Errorf("%s: request = %+v, want the provider, the checkout, the run and the tool of the params", name, req)
			}
		}
	}
}

// TestTheRegisteredBrainsCarryTheFactsOfTheirPlacement guards the entries the matrix
// reads: scripted has no credential and runs on the host, and agentkit is a driver that may
// run inside a box with the vendor's key.
func TestTheRegisteredBrainsCarryTheFactsOfTheirPlacement(t *testing.T) {
	scripted, _ := Default.Lookup(ScriptedName)
	if len(scripted.Credentials) != 0 || scripted.DriverInBox {
		t.Errorf("scripted = %+v, want no credential and no placement in a box", scripted)
	}
	agentkit, _ := Default.Lookup(AgentkitName)
	if !slices.Equal(agentkit.Credentials, []string{"ANTHROPIC_API_KEY"}) || !agentkit.DriverInBox {
		t.Errorf("agentkit = %+v, want the vendor key and a driver that may run in a box", agentkit)
	}
	if VendorKeyVar != anthropic.APIKeyVar {
		t.Errorf("the vendor key is %q, and the provider reads %q", VendorKeyVar, anthropic.APIKeyVar)
	}
}

// hostEnv is a host environment with arbitrary extra variables and some of the
// credentials, every value distinct.
func hostEnv(rng *rand.Rand, serial int) map[string]string {
	env := map[string]string{}
	for i := range rng.IntN(8) {
		name := fmt.Sprintf("EXTRA_%d_%d", serial, i)
		env[name] = fmt.Sprintf("value-of-%s", name)
	}
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "ANTHROPIC_API_KEY", "AF_MODEL", "PATH", "HOME", "AF_SANDBOX_IMAGE"} {
		if rng.IntN(2) == 0 {
			env[name] = "value-of-" + name
		}
	}
	return env
}

// TestEnvIsExactlyTheCredentialsOfThePlacement_TS_10_9 verifies TS-10-9:
// for any brain, provider and host environment, Env is empty for
// a brain on the host, and exactly the host's vendor key, when it is set, for a brain
// in a box; and no other host value is in any field of the request.
// Verifies: 10-REQ-2.2
func TestEnvIsExactlyTheCredentialsOfThePlacement_TS_10_9(t *testing.T) {
	rng := rand.New(rand.NewPCG(10, 9))
	for i := range 300 {
		env := hostEnv(rng, i)
		for _, brain := range matrixBrains(t) {
			for _, provider := range providers {
				req := boxRequest(t, brain, provider, nil, env, time.Time{})

				want := map[string]string{}
				if p := wantPlacement(brain.Name, provider); p != PlacementHost {
					if v, ok := env["ANTHROPIC_API_KEY"]; ok {
						want["ANTHROPIC_API_KEY"] = v
					}
				}
				if !maps.Equal(req.Env, want) {
					t.Fatalf("#%d %s/%s: Env = %v, want %v (host %v)", i, brain.Name, provider, req.Env, want, env)
				}
				dump := fmt.Sprintf("%#v", req)
				for name, v := range env {
					allowed := name == "ANTHROPIC_API_KEY" && len(want) > 0
					if name == "AF_SANDBOX_IMAGE" && provider == "podman" {
						continue // the image of a podman box is a setting, not a credential
					}
					if strings.Contains(dump, v) != allowed {
						t.Fatalf("#%d %s/%s: the request %s holds %s's value: %v, want %v", i, brain.Name, provider, dump, name, !allowed, allowed)
					}
				}
			}
		}
	}
}

// TestWithAllFourSecretsOnlyTheDriverInABoxGetsTheVendorKey_TS_10_10 verifies TS-10-10:
// with GITHUB_TOKEN, GH_TOKEN, GITLAB_TOKEN and ANTHROPIC_API_KEY on the host, the Env
// of agentkit in a box that isolates is {ANTHROPIC_API_KEY}, and scripted's and that of
// agentkit under local, on the host, are empty.
// Verifies: 10-REQ-2.2
func TestWithAllFourSecretsOnlyTheDriverInABoxGetsTheVendorKey_TS_10_10(t *testing.T) {
	env := map[string]string{"GITHUB_TOKEN": "gh-1", "GH_TOKEN": "gh-2", "GITLAB_TOKEN": "gl-1", "ANTHROPIC_API_KEY": "sk-vendor"}
	brains := matrixBrains(t)
	scripted, agentkit := brains[0], brains[1]

	if got := boxRequest(t, agentkit, "podman", nil, env, time.Time{}).Env; !maps.Equal(got, map[string]string{"ANTHROPIC_API_KEY": "sk-vendor"}) {
		t.Errorf("agentkit in the podman box: Env = %v, want only the vendor key", got)
	}
	if got := boxRequest(t, agentkit, "local", nil, env, time.Time{}).Env; len(got) != 0 {
		t.Errorf("agentkit under local: Env = %v, want it empty", got)
	}
	for _, provider := range providers {
		if got := boxRequest(t, scripted, provider, nil, env, time.Time{}).Env; len(got) != 0 {
			t.Errorf("scripted under %s: Env = %v, want it empty", provider, got)
		}
	}
	if got := boxRequest(t, agentkit, "podman", nil, map[string]string{"GITHUB_TOKEN": "x"}, time.Time{}).Env; got == nil || len(got) != 0 {
		t.Errorf("agentkit in a box without a key: Env = %#v, want an empty map", got)
	}
}

// TestNetworkIsNoneOnlyForAHostBrainWithoutAShell_TS_10_11 verifies TS-10-11:
// none for a brain on the host whose tools have no shell, open otherwise, and host
// under local whatever the brain and the tools.
// Verifies: 10-REQ-2.3
func TestNetworkIsNoneOnlyForAHostBrainWithoutAShell_TS_10_11(t *testing.T) {
	brains := matrixBrains(t)
	scripted, agentkit := brains[0], brains[1]
	readOnly := []string{"read_file", "list_files", "find_files", "search_files"}
	withShell := append(slices.Clone(readOnly), "execute")

	for _, tc := range []struct {
		name     string
		brain    Entry
		provider string
		tools    []string
		want     string
	}{
		{"scripted, no shell", scripted, "podman", readOnly, "none"},
		{"scripted, a shell", scripted, "podman", withShell, "open"},
		{"agentkit in the box, no shell", agentkit, "podman", readOnly, "open"},
		{"agentkit in the box, a shell", agentkit, "podman", withShell, "open"},
		{"every tool is a shell too", scripted, "podman", nil, "open"},
		{"run_command is a shell too", scripted, "podman", []string{"read_file", "run_command"}, "open"},
		{"no tools at all", scripted, "podman", []string{}, "none"},
		{"scripted under local", scripted, "local", readOnly, "host"},
		{"agentkit under local", agentkit, "local", withShell, "host"},
	} {
		if got := boxRequest(t, tc.brain, tc.provider, tc.tools, nil, time.Time{}).Network; got != tc.want {
			t.Errorf("%s: network %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestThePodmanImageIsAFSandboxImageOrTheToolsImage_TS_10_12 verifies TS-10-12:
// podman gets AF_SANDBOX_IMAGE, else quay.io/agentfox/tools:latest, and the other
// providers get no image.
// Verifies: 10-REQ-2.4
func TestThePodmanImageIsAFSandboxImageOrTheToolsImage_TS_10_12(t *testing.T) {
	agentkit := matrixBrains(t)[1]
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{nil, "quay.io/agentfox/tools:latest"},
		{map[string]string{"AF_SANDBOX_IMAGE": ""}, "quay.io/agentfox/tools:latest"},
		{map[string]string{"AF_SANDBOX_IMAGE": "example.test/img:1"}, "example.test/img:1"},
	} {
		if got := boxRequest(t, agentkit, "podman", nil, tc.env, time.Time{}).Image; got != tc.want {
			t.Errorf("podman with %v: image %q, want %q", tc.env, got, tc.want)
		}
		for _, provider := range []string{"local"} {
			if got := boxRequest(t, agentkit, provider, nil, tc.env, time.Time{}).Image; got != "" {
				t.Errorf("%s with %v: image %q, want none", provider, tc.env, got)
			}
		}
	}
}

// TestTTLIsTheDeadlinePlusTenMinutes_TS_10_13 verifies TS-10-13: the time to live is the
// time left on the deadline plus ten minutes, and the labels are those spec 07 sets.
// Verifies: 10-REQ-2.5
func TestTTLIsTheDeadlinePlusTenMinutes_TS_10_13(t *testing.T) {
	scripted := matrixBrains(t)[0]
	deadline := time.Now().Add(5 * time.Minute)

	for _, provider := range providers {
		req := boxRequest(t, scripted, provider, nil, nil, deadline)
		if req.TTL < 14*time.Minute+50*time.Second || req.TTL > 15*time.Minute {
			t.Errorf("%s: TTL = %v, want about 15 minutes", provider, req.TTL)
		}
	}
	if req := boxRequest(t, scripted, "podman", nil, nil, time.Time{}); req.TTL != 0 {
		t.Errorf("a command with no deadline: TTL = %v, want the provider's default (0)", req.TTL)
	}
	if req := boxRequest(t, scripted, "podman", nil, nil, time.Now().Add(-time.Hour)); req.TTL != 10*time.Minute {
		t.Errorf("a deadline that has passed: TTL = %v, want the ten minutes", req.TTL)
	}

	// The labels are those Open sets from the run and the tool, as in spec 07.
	req := boxRequest(t, scripted, "local", nil, nil, deadline)
	req.HostDir = newRepo(t)
	s, err := box.Open(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if labels := s.Spec().Labels; labels["af.run_id"] != req.RunID || labels["af.tool"] != "fix" || len(labels) != 2 {
		t.Errorf("labels = %v, want af.run_id and af.tool", labels)
	}
}

// TestACredentialIsOnlyForAPlacementInABox guards the names an entry gives for a placement.
func TestACredentialIsOnlyForAPlacementInABox(t *testing.T) {
	e := Entry{Credentials: []string{"ANTHROPIC_API_KEY"}}
	if got := e.CredentialNames(PlacementHost); len(got) != 0 {
		t.Errorf("host: %v", got)
	}
	if got := e.CredentialNames(PlacementBox); !slices.Equal(got, []string{"ANTHROPIC_API_KEY"}) {
		t.Errorf("box: %v", got)
	}
}

// TestTheRequestSaysWhetherTheProviderWasChosenByDefault guards the flag that makes the
// failure to open a default provider name --sandbox and AF_SANDBOX (TS-10-5).
func TestTheRequestSaysWhetherTheProviderWasChosenByDefault(t *testing.T) {
	scripted := matrixBrains(t)[0]
	for _, defaulted := range []bool{false, true} {
		req := BoxRequest(BoxParams{Brain: scripted, Provider: providerEntry(t, "podman"), Defaulted: defaulted})
		if req.Defaulted != defaulted {
			t.Errorf("Defaulted = %v, want %v", req.Defaulted, defaulted)
		}
	}
}
