// Package envtest makes a test independent of the environment it runs in.
//
// A test of a refusal ("no model credential", "no forge credential") must see
// an environment that has none, whatever the machine carries: a gateway, a CI
// proxy, a Claude Code session, a developer's own tokens. Setting the one
// variable the test is about leaves every other way of being credentialed in
// place, and the refusal then never happens.
package envtest

import (
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/anthropic"
	"github.com/agentfox/agentkit-go/provider/google"
	"github.com/agentfox/agentkit-go/provider/ollama"
	"github.com/agentfox/agentkit-go/provider/openai"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
)

// platformVars select a deployment platform or its project, which count as a
// credential on their own.
var platformVars = []string{
	"CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_BEDROCK",
	"ANTHROPIC_VERTEX_PROJECT_ID", "ANTHROPIC_VERTEX_BASE_URL",
}

// forgeVars are the forge credentials and endpoints the tools read.
var forgeVars = []string{
	"GITHUB_TOKEN", "GH_TOKEN", "GITHUB_API_URL",
	"GITLAB_TOKEN", "GITLAB_API_URL",
}

// ClearModelCredentials empties, for the test's duration, every environment
// variable through which a model's credential can be found: each vendor's key
// and token variables, its base-URL variable (a base URL alone is a credential
// for a gateway), and the platform selectors. A test then sets exactly the
// ones it means to.
func ClearModelCredentials(t testing.TB) {
	t.Helper()
	for _, m := range []*core.Model{
		{Provider: "anthropic", API: anthropic.API},
		{Provider: "openai", API: openai.API},
		{Provider: "google", API: google.API},
		{Provider: "ollama", API: ollama.API},
	} {
		for _, v := range agentrun.CredentialVars(m) {
			t.Setenv(v, "")
		}
		if v := agentrun.BaseURLVar(m); v != "" {
			t.Setenv(v, "")
		}
	}
	for _, v := range platformVars {
		t.Setenv(v, "")
	}
}

// ClearForgeCredentials empties the forge tokens and endpoints for the test's
// duration.
func ClearForgeCredentials(t testing.TB) {
	t.Helper()
	for _, v := range forgeVars {
		t.Setenv(v, "")
	}
}

// Clean clears every model and forge credential: the starting point of a test
// that sets only what it needs.
func Clean(t testing.TB) {
	t.Helper()
	ClearModelCredentials(t)
	ClearForgeCredentials(t)
}
