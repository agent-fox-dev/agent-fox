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

// The brains of the matrix. claude is not registered yet, so the matrix builds its
// entry the way the registry will.
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
	return append(out, Entry{Name: "claude", CostSource: CostReported, Credentials: []string{VendorKeyVar}, OwnHands: true})
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
func boxRequest(t *testing.T, brain Entry, provider string, borrowed bool, tools []string, env map[string]string, deadline time.Time) box.Request {
	t.Helper()
	return BoxRequest(BoxParams{
		Brain: brain, Provider: providerEntry(t, provider), BorrowedHands: borrowed, Tools: tools, Getenv: envOf(env), Deadline: deadline,
		HostDir: "/work/checkout", RunID: "20261021_110000_abcdef", Tool: "fix",
	})
}

// wantPlacement is the placement the spec gives, by names.
func wantPlacement(brain, provider string, borrowed bool) Placement {
	switch brain {
	case ScriptedName:
		return PlacementHost
	case AgentkitName:
		if borrowed || provider == "local" || provider == "none" {
			return PlacementHost
		}
		return PlacementBox
	}
	if provider == "none" {
		return PlacementCheckout
	}
	return PlacementBox
}

var providers = []string{"landlock", "podman", "local", "none"}

// TestPlacementIsAPureFunctionOfBrainProviderAndBorrowedHands_TS_10_8 verifies TS-10-8:
// for every brain, provider and borrowed-hands flag that is valid for the brain, the
// placement is the one the spec gives, and the request carries it.
// Verifies: 10-REQ-2.1
func TestPlacementIsAPureFunctionOfBrainProviderAndBorrowedHands_TS_10_8(t *testing.T) {
	for _, brain := range matrixBrains(t) {
		for _, provider := range providers {
			for _, borrowed := range []bool{false, true} {
				if borrowed && brain.Name != AgentkitName {
					continue // --borrowed-hands is refused with another brain
				}
				name := fmt.Sprintf("%s/%s/borrowed=%v", brain.Name, provider, borrowed)
				req := boxRequest(t, brain, provider, borrowed, nil, nil, time.Time{})
				want := wantPlacement(brain.Name, provider, borrowed)
				if req.Placement != string(want) {
					t.Errorf("%s: placement %q, want %q", name, req.Placement, want)
				}
				if got := PlacementOf(brain, providerEntry(t, provider), borrowed); got != want {
					t.Errorf("%s: PlacementOf = %q, want %q", name, got, want)
				}
				if req.Provider != provider || req.HostDir != "/work/checkout" || req.RunID != "20261021_110000_abcdef" || req.Tool != "fix" {
					t.Errorf("%s: request = %+v, want the provider, the checkout, the run and the tool of the params", name, req)
				}
			}
		}
	}
}

// TestTheRegisteredBrainsCarryTheFactsOfTheirPlacement guards the entries the matrix
// reads: scripted has no credential and borrows hands, agentkit is a driver that may
// run inside a box with the vendor's key, and neither brings hands of its own.
func TestTheRegisteredBrainsCarryTheFactsOfTheirPlacement(t *testing.T) {
	scripted, _ := Default.Lookup(ScriptedName)
	if len(scripted.Credentials) != 0 || scripted.OwnHands || scripted.DriverInBox {
		t.Errorf("scripted = %+v, want no credential, no hands of its own and no placement in a box", scripted)
	}
	agentkit, _ := Default.Lookup(AgentkitName)
	if !slices.Equal(agentkit.Credentials, []string{"ANTHROPIC_API_KEY"}) || agentkit.OwnHands || !agentkit.DriverInBox {
		t.Errorf("agentkit = %+v, want the vendor key, no hands of its own, and a driver that may run in a box", agentkit)
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
// for any brain, provider, borrowed-hands flag and host environment, Env is empty for
// a brain on the host, and exactly the host's vendor key, when it is set, for a brain
// in a box or on the checkout; and no other host value is in any field of the request.
// Verifies: 10-REQ-2.2
func TestEnvIsExactlyTheCredentialsOfThePlacement_TS_10_9(t *testing.T) {
	rng := rand.New(rand.NewPCG(10, 9))
	for i := range 300 {
		env := hostEnv(rng, i)
		for _, brain := range matrixBrains(t) {
			for _, provider := range providers {
				for _, borrowed := range []bool{false, true} {
					if borrowed && brain.Name != AgentkitName {
						continue
					}
					req := boxRequest(t, brain, provider, borrowed, nil, env, time.Time{})

					want := map[string]string{}
					if p := wantPlacement(brain.Name, provider, borrowed); p != PlacementHost {
						if v, ok := env["ANTHROPIC_API_KEY"]; ok {
							want["ANTHROPIC_API_KEY"] = v
						}
					}
					if !maps.Equal(req.Env, want) {
						t.Fatalf("#%d %s/%s/%v: Env = %v, want %v (host %v)", i, brain.Name, provider, borrowed, req.Env, want, env)
					}
					dump := fmt.Sprintf("%#v", req)
					for name, v := range env {
						allowed := name == "ANTHROPIC_API_KEY" && len(want) > 0
						if name == "AF_SANDBOX_IMAGE" && provider == "podman" {
							continue // the image of a podman box is a setting, not a credential
						}
						if strings.Contains(dump, v) != allowed {
							t.Fatalf("#%d %s/%s/%v: the request %s holds %s's value: %v, want %v", i, brain.Name, provider, borrowed, dump, name, !allowed, allowed)
						}
					}
				}
			}
		}
	}
}

// TestWithAllFourSecretsOnlyClaudeGetsTheVendorKey_TS_10_10 verifies TS-10-10:
// with GITHUB_TOKEN, GH_TOKEN, GITLAB_TOKEN and ANTHROPIC_API_KEY on the host,
// claude's Env is {ANTHROPIC_API_KEY}, and scripted's and agentkit's with borrowed
// hands are empty.
// Verifies: 10-REQ-2.2
func TestWithAllFourSecretsOnlyClaudeGetsTheVendorKey_TS_10_10(t *testing.T) {
	env := map[string]string{"GITHUB_TOKEN": "gh-1", "GH_TOKEN": "gh-2", "GITLAB_TOKEN": "gl-1", "ANTHROPIC_API_KEY": "sk-vendor"}
	brains := matrixBrains(t)
	scripted, agentkit, claude := brains[0], brains[1], brains[2]

	for _, provider := range providers {
		if got := boxRequest(t, claude, provider, false, nil, env, time.Time{}).Env; !maps.Equal(got, map[string]string{"ANTHROPIC_API_KEY": "sk-vendor"}) {
			t.Errorf("claude under %s: Env = %v, want only the vendor key", provider, got)
		}
		if got := boxRequest(t, scripted, provider, false, nil, env, time.Time{}).Env; len(got) != 0 {
			t.Errorf("scripted under %s: Env = %v, want it empty", provider, got)
		}
		if got := boxRequest(t, agentkit, provider, true, nil, env, time.Time{}).Env; len(got) != 0 {
			t.Errorf("agentkit with borrowed hands under %s: Env = %v, want it empty", provider, got)
		}
	}
	if got := boxRequest(t, claude, "podman", false, nil, map[string]string{"GITHUB_TOKEN": "x"}, time.Time{}).Env; got == nil || len(got) != 0 {
		t.Errorf("claude without a key: Env = %#v, want an empty map", got)
	}
}

// TestNetworkIsNoneOnlyForAHostBrainWithoutAShell_TS_10_11 verifies TS-10-11:
// none for a brain on the host whose tools have no shell, open otherwise, and host
// under local and none whatever the brain and the tools.
// Verifies: 10-REQ-2.3
func TestNetworkIsNoneOnlyForAHostBrainWithoutAShell_TS_10_11(t *testing.T) {
	brains := matrixBrains(t)
	scripted, agentkit, claude := brains[0], brains[1], brains[2]
	readOnly := []string{"read_file", "list_files", "find_files", "search_files"}
	withShell := append(slices.Clone(readOnly), "execute")

	for _, tc := range []struct {
		name     string
		brain    Entry
		provider string
		borrowed bool
		tools    []string
		want     string
	}{
		{"scripted, no shell", scripted, "landlock", false, readOnly, "none"},
		{"scripted, a shell", scripted, "landlock", false, withShell, "open"},
		{"agentkit borrowing, no shell", agentkit, "podman", true, readOnly, "none"},
		{"agentkit borrowing, a shell", agentkit, "podman", true, withShell, "open"},
		{"agentkit in the box, no shell", agentkit, "podman", false, readOnly, "open"},
		{"claude in podman, no shell", claude, "podman", false, readOnly, "open"},
		{"claude in landlock, a shell", claude, "landlock", false, withShell, "open"},
		{"every tool is a shell too", scripted, "podman", false, nil, "open"},
		{"run_command is a shell too", scripted, "podman", false, []string{"read_file", "run_command"}, "open"},
		{"no tools at all", scripted, "podman", false, []string{}, "none"},
		{"scripted under local", scripted, "local", false, readOnly, "host"},
		{"claude under local", claude, "local", false, withShell, "host"},
		{"agentkit under none", agentkit, "none", false, readOnly, "host"},
		{"claude under none", claude, "none", false, readOnly, "host"},
	} {
		if got := boxRequest(t, tc.brain, tc.provider, tc.borrowed, tc.tools, nil, time.Time{}).Network; got != tc.want {
			t.Errorf("%s: network %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestThePodmanImageIsAFSandboxImageOrTheToolsImage_TS_10_12 verifies TS-10-12:
// podman gets AF_SANDBOX_IMAGE, else quay.io/agentfox/tools:latest, and the other
// providers get no image.
// Verifies: 10-REQ-2.4
func TestThePodmanImageIsAFSandboxImageOrTheToolsImage_TS_10_12(t *testing.T) {
	claude := matrixBrains(t)[2]
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{nil, "quay.io/agentfox/tools:latest"},
		{map[string]string{"AF_SANDBOX_IMAGE": ""}, "quay.io/agentfox/tools:latest"},
		{map[string]string{"AF_SANDBOX_IMAGE": "example.test/img:1"}, "example.test/img:1"},
	} {
		if got := boxRequest(t, claude, "podman", false, nil, tc.env, time.Time{}).Image; got != tc.want {
			t.Errorf("podman with %v: image %q, want %q", tc.env, got, tc.want)
		}
		for _, provider := range []string{"landlock", "local", "none"} {
			if got := boxRequest(t, claude, provider, false, nil, tc.env, time.Time{}).Image; got != "" {
				t.Errorf("%s with %v: image %q, want none", provider, tc.env, got)
			}
		}
	}
}

// TestTTLIsTheDeadlinePlusTenMinutesAndLandlockIsRequired_TS_10_13 verifies TS-10-13:
// the time to live is the time left on the deadline plus ten minutes, Landlock is
// required of landlock and of nothing else, and the labels are those spec 07 sets.
// Verifies: 10-REQ-2.5
func TestTTLIsTheDeadlinePlusTenMinutesAndLandlockIsRequired_TS_10_13(t *testing.T) {
	scripted := matrixBrains(t)[0]
	deadline := time.Now().Add(5 * time.Minute)

	for _, provider := range []string{"landlock", "podman"} {
		req := boxRequest(t, scripted, provider, false, nil, nil, deadline)
		if req.TTL < 14*time.Minute+50*time.Second || req.TTL > 15*time.Minute {
			t.Errorf("%s: TTL = %v, want about 15 minutes", provider, req.TTL)
		}
		if req.RequireLandlock != (provider == "landlock") {
			t.Errorf("%s: RequireLandlock = %v", provider, req.RequireLandlock)
		}
	}
	for _, provider := range []string{"local", "none"} {
		if boxRequest(t, scripted, provider, false, nil, nil, deadline).RequireLandlock {
			t.Errorf("%s requires Landlock", provider)
		}
	}
	if req := boxRequest(t, scripted, "landlock", false, nil, nil, time.Time{}); req.TTL != 0 {
		t.Errorf("a command with no deadline: TTL = %v, want the provider's default (0)", req.TTL)
	}
	if req := boxRequest(t, scripted, "landlock", false, nil, nil, time.Now().Add(-time.Hour)); req.TTL != 10*time.Minute {
		t.Errorf("a deadline that has passed: TTL = %v, want the ten minutes", req.TTL)
	}

	// The labels are those Open sets from the run and the tool, as in spec 07.
	req := boxRequest(t, scripted, "local", false, nil, nil, deadline)
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

// TestACredentialIsOnlyForAPlacementInABoxOrOnTheCheckout guards the names an entry
// gives for a placement.
func TestACredentialIsOnlyForAPlacementInABoxOrOnTheCheckout(t *testing.T) {
	e := Entry{Credentials: []string{"ANTHROPIC_API_KEY"}}
	if got := e.CredentialNames(PlacementHost); len(got) != 0 {
		t.Errorf("host: %v", got)
	}
	for _, p := range []Placement{PlacementBox, PlacementCheckout} {
		if got := e.CredentialNames(p); !slices.Equal(got, []string{"ANTHROPIC_API_KEY"}) {
			t.Errorf("%s: %v", p, got)
		}
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
