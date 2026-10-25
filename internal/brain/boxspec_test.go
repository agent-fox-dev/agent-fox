package brain

import (
	"context"
	"fmt"
	"maps"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/optin"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// entries are the brain and the box provider of the names, as their registries have them.
func entries(t *testing.T, brain, provider string) (Entry, box.Entry) {
	t.Helper()
	b, err := Default.Lookup(brain)
	if err != nil {
		t.Fatal(err)
	}
	p, err := box.Default.Lookup(provider)
	if err != nil {
		t.Fatal(err)
	}
	return b, p
}

// request is the box request of the brain on the provider.
func request(t *testing.T, brain, provider string, tools []string, env map[string]string) box.Request {
	t.Helper()
	b, p := entries(t, brain, provider)
	return BoxRequest(BoxParams{Brain: b, Provider: p, Tools: tools, Getenv: envOf(env), HostDir: "/work/checkout", RunID: "20261021_110000_abcdef", Tool: "fix"})
}

func TestPlacementOf_IsTheBoxOnlyForADriverUnderAProviderThatIsolates(t *testing.T) {
	for _, tc := range []struct {
		brain, provider string
		want            Placement
	}{
		{AgentkitName, "podman", PlacementBox},
		{AgentkitName, "local", PlacementHost},
		{ScriptedName, "podman", PlacementHost},
		{ScriptedName, "local", PlacementHost},
	} {
		b, p := entries(t, tc.brain, tc.provider)
		if got := PlacementOf(b, p); got != tc.want {
			t.Errorf("%s under %s: %q, want %q", tc.brain, tc.provider, got, tc.want)
		}
		if got := request(t, tc.brain, tc.provider, nil, nil).Placement; got != string(tc.want) {
			t.Errorf("%s under %s: the request's placement is %q, want %q", tc.brain, tc.provider, got, tc.want)
		}
	}
}

func TestBoxRequest_EnvIsTheVendorKeyOfADriverInABoxAndNothingElse(t *testing.T) {
	host := map[string]string{"GITHUB_TOKEN": "gh-1", "GH_TOKEN": "gh-2", "GITLAB_TOKEN": "gl-1", "ANTHROPIC_API_KEY": "sk-vendor", "AF_MODEL": "m-9", "EXTRA": "x-7"}
	for _, tc := range []struct {
		brain, provider string
		host            map[string]string
		want            map[string]string
	}{
		{AgentkitName, "podman", host, map[string]string{"ANTHROPIC_API_KEY": "sk-vendor"}},
		{AgentkitName, "podman", map[string]string{"GITHUB_TOKEN": "gh-1"}, map[string]string{}},
		{AgentkitName, "local", host, map[string]string{}},
		{ScriptedName, "podman", host, map[string]string{}},
		{ScriptedName, "local", host, map[string]string{}},
	} {
		req := request(t, tc.brain, tc.provider, nil, tc.host)
		if req.Env == nil || !maps.Equal(req.Env, tc.want) {
			t.Errorf("%s under %s: Env = %#v, want %v", tc.brain, tc.provider, req.Env, tc.want)
		}
		dump := fmt.Sprintf("%#v", req)
		for name, v := range tc.host {
			if _, given := tc.want[name]; !given && strings.Contains(dump, v) {
				t.Errorf("%s under %s: the request holds the value of %s: %s", tc.brain, tc.provider, name, dump)
			}
		}
	}
}

func TestBoxRequest_NetworkIsNoneOnlyForABrainOnTheHostWithoutAShell(t *testing.T) {
	readOnly := []string{"read_file", "list_files", "search_files"}
	for _, tc := range []struct {
		brain, provider string
		tools           []string
		want            string
	}{
		{ScriptedName, "podman", readOnly, box.NetworkNone},
		{ScriptedName, "podman", []string{}, box.NetworkNone},
		{ScriptedName, "podman", append(readOnly, "execute"), box.NetworkOpen},
		{ScriptedName, "podman", []string{"run_command"}, box.NetworkOpen},
		{ScriptedName, "podman", nil, box.NetworkOpen}, // every tool, a shell among them
		{AgentkitName, "podman", readOnly, box.NetworkOpen},
		{ScriptedName, "local", readOnly, box.NetworkHost},
		{AgentkitName, "local", nil, box.NetworkHost},
	} {
		if got := request(t, tc.brain, tc.provider, tc.tools, nil).Network; got != tc.want {
			t.Errorf("%s under %s with %v: network %q, want %q", tc.brain, tc.provider, tc.tools, got, tc.want)
		}
	}
}

func TestBoxRequest_ImageIsAFSandboxImageOrTheToolsImageForPodman(t *testing.T) {
	for _, tc := range []struct {
		provider string
		env      map[string]string
		want     string
	}{
		{"podman", nil, DefaultImage},
		{"podman", map[string]string{"AF_SANDBOX_IMAGE": ""}, DefaultImage},
		{"podman", map[string]string{"AF_SANDBOX_IMAGE": "example.test/img:1"}, "example.test/img:1"},
		{"local", map[string]string{"AF_SANDBOX_IMAGE": "example.test/img:1"}, ""},
	} {
		if got := request(t, ScriptedName, tc.provider, nil, tc.env).Image; got != tc.want {
			t.Errorf("%s with %v: image %q, want %q", tc.provider, tc.env, got, tc.want)
		}
	}
}

func TestBoxRequest_CarriesTheRunAndLivesTenMinutesPastTheDeadline(t *testing.T) {
	b, p := entries(t, ScriptedName, "podman")
	record := func(string, any) error { return nil }
	for _, tc := range []struct {
		deadline time.Time
		min, max time.Duration
	}{
		{time.Time{}, 0, 0},
		{time.Now().Add(5 * time.Minute), 14*time.Minute + 50*time.Second, 15 * time.Minute},
		{time.Now().Add(-time.Hour), 10 * time.Minute, 10 * time.Minute},
	} {
		req := BoxRequest(BoxParams{
			Brain: b, Provider: p, Deadline: tc.deadline, Defaulted: true, HostDir: "/work/checkout", RunID: "r1", Tool: "fix",
			Record: record, Labels: map[string]string{"af.job": "j1"}, Registry: box.Default,
		})
		if req.TTL < tc.min || req.TTL > tc.max {
			t.Errorf("deadline %v: TTL = %v, want %v to %v", tc.deadline, req.TTL, tc.min, tc.max)
		}
		if req.Provider != "podman" || req.HostDir != "/work/checkout" || req.RunID != "r1" || req.Tool != "fix" || !req.Defaulted ||
			req.Labels["af.job"] != "j1" || req.Record == nil || req.Registry != box.Default {
			t.Errorf("request = %+v, want what the params pass through", req)
		}
	}
}

func TestBoxRequest_ABoxForABrainOnTheHostHoldsNoCredentialOfTheHost(t *testing.T) {
	secrets := map[string]string{"GITHUB_TOKEN": "ghp-host-token", "GH_TOKEN": "gh-host-token", "GITLAB_TOKEN": "gl-host-token", "ANTHROPIC_API_KEY": "sk-host-key"}
	for name, value := range secrets {
		t.Setenv(name, value)
	}
	for _, provider := range []string{"local", "podman"} {
		t.Run(provider, func(t *testing.T) {
			if provider == "podman" {
				image := os.Getenv("AF_SANDBOX_IMAGE")
				if image == "" {
					image = DefaultImage
				}
				optin.RequirePodman(t, image)
			}
			dir := testutil.Repo(t, nil)
			rd, err := toolio.NewRunDir(dir, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			b, p := entries(t, ScriptedName, provider)
			s, err := box.Open(context.Background(), BoxRequest(BoxParams{
				Brain: b, Provider: p, Getenv: os.LookupEnv, Tools: []string{"read_file"}, HostDir: dir, RunID: rd.ID, Tool: "fix",
				Record: rd.Record, Warn: func(string, string) {},
			}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })

			_, env, err := s.Exec(context.Background(), "env", time.Minute)

			if err != nil {
				t.Fatal(err)
			}
			for name, value := range secrets {
				if strings.Contains(env, name) || strings.Contains(env, value) {
					t.Errorf("the box holds %s:\n%s", name, env)
				}
			}
		})
	}
}
