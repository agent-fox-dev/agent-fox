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

// CredentialVars returns the ordered list of environment variable names
// accepted as credentials for the model's vendor.
func CredentialVars(m *core.Model) []string {
	if m == nil {
		return nil
	}
	table := vendorAuth(m)
	vars := make([]string, 0, len(table.Vars))
	for _, e := range table.Vars {
		vars = append(vars, e.Name)
	}
	return vars
}

// BaseURLVar is the environment variable that points the model's vendor at a
// gateway or a local server, or "" when it has none. Setting it alone is a
// credential: such a deployment is `ambient`.
func BaseURLVar(m *core.Model) string {
	if m == nil {
		return ""
	}
	return vendorAuth(m).BaseURLVar
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
	if m.API == anthropic.API {
		if err := checkVertexADC(); err != nil {
			if e, ok := err.(*Error); ok {
				e.Model = m
			}
			return err
		}
	}
	table := vendorAuth(m)
	if provider.ResolveAuth(table, provider.Env{}).State != provider.CredentialNone {
		return nil
	}
	vars := CredentialVars(m)
	names := append([]string(nil), vars...)
	if table.BaseURLVar != "" {
		names = append(names, table.BaseURLVar+" (for a gateway or a local server)")
	}
	err := newError("", CategoryAuth, nil,
		"no credential for vendor %q (model %s): set one of %s",
		m.Provider, m.ID, strings.Join(names, ", "))
	err.Model = m
	return err
}

// unsupportedPlatformVars are the environment variables that select a managed
// deployment of Claude which AgentKit has no wire for.
//
// CLAUDE_CODE_USE_VERTEX is not among them: AgentKit's Anthropic provider
// serves Claude on Vertex AI from the same wire implementation, selects it
// from that variable (read for truth, so `=0` is an explicit off), and its
// credential table reports a Vertex deployment as ambient, so CheckCredentials
// passes it. There is nothing for this package to add.
//
// CLAUDE_CODE_USE_BEDROCK is: AgentKit has no Bedrock implementation, so a
// request made with it set would go to api.anthropic.com with credentials that
// were never meant for it, and fail with an authentication error naming
// neither the variable that was set nor the reason it did nothing.
//
// Failing on it is louder than ignoring it, which is the point: a silent
// change of destination is the one outcome an operator cannot debug. The
// message names the way through, because a Bedrock deployment usually already
// fronts an Anthropic-compatible gateway. Drop the entry once AgentKit serves
// Bedrock.
var unsupportedPlatformVars = []struct{ name, why string }{
	{"CLAUDE_CODE_USE_BEDROCK", "Claude on AWS Bedrock"},
}

// CheckUnsupportedPlatformVars fails when a platform variable is set that
// selects a deployment AgentKit cannot reach.
func CheckUnsupportedPlatformVars() error {
	for _, v := range unsupportedPlatformVars {
		if os.Getenv(v.name) == "" {
			continue
		}
		return newError("", CategoryAuth, nil,
			"%s is set, but %s is not supported: the agent library has no adapter for it. "+
				"Point ANTHROPIC_BASE_URL at an Anthropic-compatible gateway in front of it and "+
				"set a token there, or unset %s to call the Anthropic API directly. "+
				"See docs/errata/agentkit_model_resolution.md",
			v.name, v.why, v.name)
	}
	return nil
}
