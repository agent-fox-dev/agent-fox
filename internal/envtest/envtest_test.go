package envtest

import (
	"os"
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
