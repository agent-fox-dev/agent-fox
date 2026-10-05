package issuex_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agent-fox-dev/agentfox/issuex"
)

func clearForgeEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GITHUB_API_URL", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITLAB_API_URL", "")
	t.Setenv("GITLAB_TOKEN", "")
}

// TestNewWithOptions_NoOp_TS_01_14 verifies TS-01-14:
// NewWithOptions instantiates a NoOpClient when NoOp option is enabled.
// Verifies: 01-REQ-4.1
func TestNewWithOptions_NoOp_TS_01_14(t *testing.T) {
	client, err := issuex.NewWithOptions(issuex.Options{NoOp: true})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.Authenticated() {
		t.Errorf("expected Authenticated() to be false, got true")
	}
	if _, ok := client.(*issuex.NoOpClient); !ok {
		t.Errorf("expected client to be *issuex.NoOpClient, got %T", client)
	}
}

// TestNewWithOptions_ExplicitBaseURL_TS_01_15 verifies TS-01-15:
// NewWithOptions resolves forge type from explicit BaseURL containing github or gitlab.
// Verifies: 01-REQ-4.2, 01-REQ-4.3
func TestNewWithOptions_ExplicitBaseURL_TS_01_15(t *testing.T) {
	clearForgeEnv(t)

	// BaseURL containing api.github.com / github
	c1, err1 := issuex.NewWithOptions(issuex.Options{BaseURL: "https://api.github.com"})
	if err1 != nil {
		t.Errorf("expected nil error for GitHub BaseURL, got %v", err1)
	}
	if c1 == nil {
		t.Errorf("expected non-nil client for GitHub BaseURL")
	}

	// BaseURL containing gitlab
	c2, err2 := issuex.NewWithOptions(issuex.Options{BaseURL: "https://gitlab.example.com/api/v4"})
	if err2 != nil {
		t.Errorf("expected nil error for GitLab BaseURL, got %v", err2)
	}
	if c2 == nil {
		t.Errorf("expected non-nil client for GitLab BaseURL")
	}

	// Ambiguous BaseURL containing neither
	_, err3 := issuex.NewWithOptions(issuex.Options{BaseURL: "https://forge.example.com"})
	if !errors.Is(err3, issuex.ErrAmbiguousForge) {
		t.Errorf("expected ErrAmbiguousForge for ambiguous BaseURL, got %v", err3)
	}

	// Ambiguous BaseURL containing both
	_, err4 := issuex.NewWithOptions(issuex.Options{BaseURL: "https://gitlab.com/github/repo"})
	if !errors.Is(err4, issuex.ErrAmbiguousForge) {
		t.Errorf("expected ErrAmbiguousForge for BaseURL containing both, got %v", err4)
	}
}

// TestNewWithOptions_EnvironmentVariables_TS_01_16 verifies TS-01-16:
// NewWithOptions detects forge type and endpoints from forge-specific environment variables.
// Verifies: 01-REQ-4.4, 01-REQ-4.5
func TestNewWithOptions_EnvironmentVariables_TS_01_16(t *testing.T) {
	// GitHub only
	clearForgeEnv(t)
	t.Setenv("GITHUB_TOKEN", "gh-tok")
	c1, err1 := issuex.NewWithOptions(issuex.Options{})
	if err1 != nil {
		t.Errorf("expected nil error for GitHub env, got %v", err1)
	}
	if c1 == nil || !c1.Authenticated() {
		t.Errorf("expected authenticated client for GitHub env")
	}

	// GitHub with GH_TOKEN
	clearForgeEnv(t)
	t.Setenv("GH_TOKEN", "gh-fallback-tok")
	cGH, errGH := issuex.NewWithOptions(issuex.Options{})
	if errGH != nil {
		t.Errorf("expected nil error for GH_TOKEN, got %v", errGH)
	}
	if cGH == nil || !cGH.Authenticated() {
		t.Errorf("expected authenticated client for GH_TOKEN")
	}

	// GitLab only
	clearForgeEnv(t)
	t.Setenv("GITLAB_TOKEN", "gl-tok")
	c2, err2 := issuex.NewWithOptions(issuex.Options{})
	if err2 != nil {
		t.Errorf("expected nil error for GitLab env, got %v", err2)
	}
	if c2 == nil || !c2.Authenticated() {
		t.Errorf("expected authenticated client for GitLab env")
	}
}

// TestNewWithOptions_GitOriginFallback_TS_01_17 verifies TS-01-17:
// NewWithOptions falls back to local git origin remote when environment variables are inconclusive.
// Verifies: 01-REQ-4.6
func TestNewWithOptions_GitOriginFallback_TS_01_17(t *testing.T) {
	clearForgeEnv(t)

	// GitLab remote fallback with no env vars
	glDir := setupTempGitRepoWithOrigin(t, "git@gitlab.com:team/project.git")
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working dir: %v", err)
	}
	if err := os.Chdir(glDir); err != nil {
		t.Fatalf("failed to chdir to %s: %v", glDir, err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	cGL, errGL := issuex.NewWithOptions(issuex.Options{})
	if errGL != nil {
		t.Errorf("expected nil error for gitlab origin remote fallback, got %v", errGL)
	}
	if cGL == nil {
		t.Errorf("expected non-nil client for gitlab origin remote")
	}

	// GitHub remote fallback with both env vars present
	ghDir := setupTempGitRepoWithOrigin(t, "git@github.com:team/project.git")
	if err := os.Chdir(ghDir); err != nil {
		t.Fatalf("failed to chdir to %s: %v", ghDir, err)
	}
	t.Setenv("GITHUB_TOKEN", "gh-tok")
	t.Setenv("GITLAB_TOKEN", "gl-tok")

	cGH, errGH := issuex.NewWithOptions(issuex.Options{})
	if errGH != nil {
		t.Errorf("expected nil error for github origin remote with both env vars, got %v", errGH)
	}
	if cGH == nil || !cGH.Authenticated() {
		t.Errorf("expected authenticated client for github origin remote")
	}
}

// TestNewWithOptions_AmbiguousForge_TS_01_18 verifies TS-01-18:
// NewWithOptions returns an error wrapping ErrAmbiguousForge when forge cannot be determined.
// Verifies: 01-REQ-4.7
func TestNewWithOptions_AmbiguousForge_TS_01_18(t *testing.T) {
	clearForgeEnv(t)

	nonGitDir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working dir: %v", err)
	}
	if err := os.Chdir(nonGitDir); err != nil {
		t.Fatalf("failed to chdir to %s: %v", nonGitDir, err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	client, err := issuex.NewWithOptions(issuex.Options{})
	if client != nil {
		t.Errorf("expected nil client, got %v", client)
	}
	if !errors.Is(err, issuex.ErrAmbiguousForge) {
		t.Errorf("expected error wrapping ErrAmbiguousForge, got %v", err)
	}
}

// TestNewWithOptions_UnclassifiedHostSendsNothing verifies 01-REQ-4.7 and the
// rule behind it: a host whose name says neither github nor gitlab is
// ErrAmbiguousForge, and no request, with or without a credential, leaves for
// it. The server answers like a GitLab would, so a probe would succeed.
func TestNewWithOptions_UnclassifiedHostSendsNothing(t *testing.T) {
	var requests atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Errorf("unexpected request %s %s with PRIVATE-TOKEN %q", r.Method, r.URL.Path, r.Header.Get("PRIVATE-TOKEN"))
		_, _ = w.Write([]byte(`{"version": "16.8.0-ee"}`))
	})
	plain := httptest.NewServer(handler)
	defer plain.Close()
	secure := httptest.NewTLSServer(handler)
	defer secure.Close()
	secureHost := strings.TrimPrefix(secure.URL, "https://")

	nonGitDir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working dir: %v", err)
	}
	if err := os.Chdir(nonGitDir); err != nil {
		t.Fatalf("failed to chdir to %s: %v", nonGitDir, err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	cases := map[string]issuex.Options{
		"BaseURL":   {BaseURL: plain.URL},
		"RemoteURL": {RemoteURL: plain.URL + "/corp/app.git", Repo: issuex.Repo{Owner: "corp", Name: "app"}},
		"Repo.Host": {Repo: issuex.Repo{Owner: "corp", Name: "app", Host: secureHost}, HTTPClient: secure.Client()},
		"Token":     {BaseURL: plain.URL, Token: "glpat-explicit"},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			clearForgeEnv(t)
			t.Setenv("GITLAB_TOKEN", "glpat-SECRET-from-env")
			t.Setenv("GITHUB_TOKEN", "ghp-SECRET-from-env")

			client, err := issuex.NewWithOptions(opts)
			if client != nil {
				t.Errorf("expected nil client, got %T", client)
			}
			if !errors.Is(err, issuex.ErrAmbiguousForge) {
				t.Errorf("expected ErrAmbiguousForge, got %v", err)
			}
		})
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("%d request(s) reached an unclassified host, want 0", n)
	}
}

// TS-01-19 (the registry miss) is in registry_test.go, which can reach the registry.

// TestNew_Shorthand verifies New(userAgent) constructor shorthand.
func TestNew_Shorthand(t *testing.T) {
	clearForgeEnv(t)

	nonGitDir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working dir: %v", err)
	}
	if err := os.Chdir(nonGitDir); err != nil {
		t.Fatalf("failed to chdir to %s: %v", nonGitDir, err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	client, err := issuex.New("my-agent/1.0")
	if client != nil {
		t.Errorf("expected nil client, got %v", client)
	}
	if !errors.Is(err, issuex.ErrAmbiguousForge) {
		t.Errorf("expected ErrAmbiguousForge, got %v", err)
	}
}
