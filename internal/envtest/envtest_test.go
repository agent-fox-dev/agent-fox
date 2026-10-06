package envtest

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/anthropic"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
)

var claude = &core.Model{Provider: "anthropic", API: anthropic.API}

// A base URL alone, or an auth token alone, is a credential, which is why a test
// of "no credential" that clears only ANTHROPIC_API_KEY passes or fails with
// the machine it runs on. ClearModelCredentials leaves none.
func TestClearModelCredentialsLeavesNoWayToBeCredentialed(t *testing.T) {
	for _, v := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("ANTHROPIC_API_KEY", "")
			t.Setenv(v, "set-by-the-host")
			if err := agentrun.CheckCredentials(claude); err != nil {
				t.Fatalf("with %s set alone the credential check should pass (that is the leak): %v", v, err)
			}

			ClearModelCredentials(t)
			if err := agentrun.CheckCredentials(claude); err == nil {
				t.Errorf("after ClearModelCredentials, %s still credentials the model", v)
			}
		})
	}
}

// The helper restores the host's values when the test ends.
func TestClearModelCredentialsRestoresTheEnvironment(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "http://gateway.example")
	t.Run("inner", func(t *testing.T) { ClearModelCredentials(t) })
	if got := agentrun.BaseURLVar(claude); got != "ANTHROPIC_BASE_URL" {
		t.Fatalf("BaseURLVar = %q", got)
	}
	if v := getenv("ANTHROPIC_BASE_URL"); v != "http://gateway.example" {
		t.Errorf("ANTHROPIC_BASE_URL = %q after the inner test, want the host's value back", v)
	}
}

func getenv(k string) string { return os.Getenv(k) }

// Under NoGitNetwork git refuses a forge's URL before it connects, so a test
// whose repository names a forge in its origin can neither reach it nor be
// asked for a credential, while a local remote keeps working.
func TestNoGitNetworkRefusesTheForgeBeforeAnyoneIsAsked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("WWW-Authenticate", `Basic realm="forge"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	asked := filepath.Join(t.TempDir(), "asked")
	askpass := filepath.Join(t.TempDir(), "askpass.sh")
	if err := os.WriteFile(askpass, []byte("#!/bin/sh\necho \"$1\" >> '"+asked+"'\necho someone\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Each variable goes through t.Setenv first, so the test restores it. An
	// empty GIT_ALLOW_PROTOCOL allows nothing, so it starts unset instead.
	t.Setenv("GIT_ALLOW_PROTOCOL", "")
	if err := os.Unsetenv("GIT_ALLOW_PROTOCOL"); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"GIT_TERMINAL_PROMPT": "1", "GIT_ASKPASS": askpass,
		"SSH_ASKPASS": askpass, "NO_PROXY": "127.0.0.1", "no_proxy": "127.0.0.1"} {
		t.Setenv(k, v)
	}
	NoGitNetwork()

	out, err := exec.Command("git", "ls-remote", "--heads", srv.URL+"/acme/widgets.git", "main").CombinedOutput()
	if err == nil {
		t.Fatalf("git ls-remote against a forge's URL succeeded: %s", out)
	}
	if !strings.Contains(string(out), "not allowed") {
		t.Errorf("git ls-remote output = %q, want the transport refused", out)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("git reached the forge %d time(s)", n)
	}
	if b, err := os.ReadFile(asked); err == nil {
		t.Errorf("git asked for %q", strings.TrimSpace(string(b)))
	}

	bare := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "ls-remote", bare).CombinedOutput(); err != nil {
		t.Errorf("a local remote no longer works under NoGitNetwork: %v\n%s", err, out)
	}
}
