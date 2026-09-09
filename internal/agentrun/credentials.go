package agentrun

import (
	"os"
	"strings"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider"
	"github.com/agentfox/agentkit-go/provider/anthropic"
	"github.com/agentfox/agentkit-go/provider/google"
	"github.com/agentfox/agentkit-go/provider/ollama"
	"github.com/agentfox/agentkit-go/provider/openai"
)

// vendorAuth is the ordered credential table for a resolved model's wire.
func vendorAuth(m *core.Model) provider.VendorAuth {
	switch m.API {
	case anthropic.API:
		return anthropic.VendorAuth
	case google.API:
		return google.VendorAuth
	case ollama.API:
		return ollama.VendorAuth
	default:
		// Both OpenAI wires share one table keyed on the VENDOR, so that
		// "openrouter", "groq" and "deepseek" each read their own variable.
		return openai.AuthFor(m.Provider)
	}
}

// CheckCredentials reports whether a credential for the model's vendor can be
// found, before anything expensive happens.
//
// A credential has three states, not two. A deployment on an instance role or
// a workload identity has no key this process can read and a transport that
// will nonetheless authenticate; so does a gateway that authenticates by URL,
// which is what setting only a base URL leaves you in. Both are `ambient` and
// both must pass a check that an unconfigured environment fails, or every
// service-account deployment is rejected for a key it was never going to have.
func CheckCredentials(m *core.Model) error {
	if m == nil {
		return nil
	}
	table := vendorAuth(m)
	if provider.ResolveAuth(table, provider.Env{}).State != provider.CredentialNone {
		return nil
	}
	names := make([]string, 0, len(table.Vars)+1)
	for _, e := range table.Vars {
		names = append(names, e.Name)
	}
	if table.BaseURLVar != "" {
		names = append(names, table.BaseURLVar+" (for a gateway or a local server)")
	}
	return newError("", CategoryAuth, nil,
		"no credential for vendor %q (model %s): set one of %s",
		m.Provider, m.ID, strings.Join(names, ", "))
}

// retiredPlatformVars are the environment variables of the previous
// Anthropic-SDK integration that no longer route anything.
//
// CLAUDE_CODE_USE_VERTEX and CLAUDE_CODE_USE_BEDROCK selected a managed
// deployment of Claude in the Anthropic SDK. AgentKit has no wire for either
// — its Vertex support is Gemini on the Google wire, and there is no Bedrock
// implementation at all — so a request made with one of these set would go to
// api.anthropic.com with credentials that were never meant for it, and fail
// with an authentication error naming neither the variable that was set nor
// the reason it did nothing.
//
// Failing on them is louder than ignoring them, which is the point: a silent
// change of destination is the one outcome an operator cannot debug. The
// message names the way through, because a Bedrock or Vertex deployment
// usually already fronts an Anthropic-compatible gateway.
var retiredPlatformVars = []struct{ name, why string }{
	{"CLAUDE_CODE_USE_VERTEX", "Claude on Vertex AI"},
	{"CLAUDE_CODE_USE_BEDROCK", "Claude on AWS Bedrock"},
}

// CheckRetiredPlatformVars fails when a retired platform variable is set.
func CheckRetiredPlatformVars() error {
	for _, v := range retiredPlatformVars {
		if os.Getenv(v.name) == "" {
			continue
		}
		return newError("", CategoryAuth, nil,
			"%s is set, but %s is no longer supported: this build talks to vendors over "+
				"their own wire APIs and has no adapter for it. Point ANTHROPIC_BASE_URL at an "+
				"Anthropic-compatible gateway in front of it and set a token there, or unset %s "+
				"to call the Anthropic API directly. See docs/errata/agentkit_model_resolution.md",
			v.name, v.why, v.name)
	}
	return nil
}
