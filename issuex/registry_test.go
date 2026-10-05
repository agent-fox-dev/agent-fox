package issuex

import (
	"errors"
	"testing"
)

// withAdapter replaces the adapter registered for a forge for the test's
// duration (nil removes it) and restores what was there.
func withAdapter(t *testing.T, forge ForgeType, factory func(Options) (Client, error)) {
	t.Helper()
	adaptersMu.Lock()
	old, had := adapters[forge]
	if factory == nil {
		delete(adapters, forge)
	} else {
		adapters[forge] = factory
	}
	adaptersMu.Unlock()
	t.Cleanup(func() {
		adaptersMu.Lock()
		defer adaptersMu.Unlock()
		if had {
			adapters[forge] = old
		} else {
			delete(adapters, forge)
		}
	})
}

// TestNewWithOptions_UnsupportedForge_TS_01_19 verifies TS-01-19 (01-REQ-4.8):
// a forge type that resolves while its provider adapter is not registered is
// ErrUnsupportedForge, with a nil client. The registry is how every forge
// reaches its adapter, GitHub and GitLab included, so the miss is exercised
// directly rather than through a third forge type the project does not support.
func TestNewWithOptions_UnsupportedForge_TS_01_19(t *testing.T) {
	for _, tc := range []struct {
		forge   ForgeType
		baseURL string
	}{
		{ForgeTypeGitHub, "https://api.github.com"},
		{ForgeTypeGitLab, "https://gitlab.example.com/api/v4"},
	} {
		t.Run(string(tc.forge), func(t *testing.T) {
			clearForgeEnvInternal(t)
			withAdapter(t, tc.forge, nil)

			client, err := NewWithOptions(Options{BaseURL: tc.baseURL, Token: "fake"})
			if client != nil {
				t.Errorf("expected nil client, got %v", client)
			}
			if !errors.Is(err, ErrUnsupportedForge) {
				t.Errorf("expected error wrapping ErrUnsupportedForge, got %v", err)
			}
		})
	}
}

// NewWithOptions builds the client through the adapter registered for the
// forge it resolves, so the registry is what decides, not a branch in the
// factory.
func TestNewWithOptions_BuildsThroughTheRegisteredAdapter(t *testing.T) {
	clearForgeEnvInternal(t)
	spy := NewNoOp()
	var got Options
	withAdapter(t, ForgeTypeGitHub, func(o Options) (Client, error) {
		got = o
		return spy, nil
	})

	client, err := NewWithOptions(Options{BaseURL: "https://api.github.com", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if client != spy {
		t.Errorf("NewWithOptions returned %T, want the registered adapter's client", client)
	}
	if got.BaseURL != "https://api.github.com" || got.Token != "t" {
		t.Errorf("the adapter received %+v, want the resolved options", got)
	}
}

// 01-REQ-4.2: a base URL that contains "github" is GitHub. A name that happens
// to contain another forge's name as well (a repository called bitbucket-mirror)
// does not make it ambiguous: only github together with gitlab does.
func TestGitHubBaseURLNamingAnotherForgeStillResolvesGitHub(t *testing.T) {
	clearForgeEnvInternal(t)
	for _, base := range []string{
		"https://github.com/bitbucket-mirror",
		"https://api.github.com/repos/bitbucket",
	} {
		ft, err := DetectForge(Options{BaseURL: base})
		if err != nil || ft != ForgeTypeGitHub {
			t.Errorf("DetectForge(%s) = %q, %v, want GitHub", base, ft, err)
		}
		c, err := NewWithOptions(Options{BaseURL: base})
		if err != nil {
			t.Errorf("NewWithOptions(%s): %v", base, err)
			continue
		}
		if _, ok := c.(*githubClient); !ok {
			t.Errorf("NewWithOptions(%s) = %T, want the GitHub client", base, c)
		}
	}

	// A name with both forges' names is still ambiguous.
	if _, err := DetectForge(Options{BaseURL: "https://gitlab.com/github/repo"}); !errors.Is(err, ErrAmbiguousForge) {
		t.Errorf("a base URL naming github and gitlab: err = %v, want ErrAmbiguousForge", err)
	}
}

func clearForgeEnvInternal(t *testing.T) {
	t.Helper()
	for _, v := range []string{"GITHUB_API_URL", "GITHUB_TOKEN", "GH_TOKEN", "GITLAB_API_URL", "GITLAB_TOKEN"} {
		t.Setenv(v, "")
	}
}
