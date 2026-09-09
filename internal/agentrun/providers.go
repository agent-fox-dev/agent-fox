package agentrun

import (
	agentkit "github.com/agentfox/agentkit-go"
	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/anthropic"
	"github.com/agentfox/agentkit-go/provider/google"
	"github.com/agentfox/agentkit-go/provider/ollama"
	"github.com/agentfox/agentkit-go/provider/openai"
	"github.com/agentfox/agentkit-go/provider/openairesponses"
)

// DefaultProviders returns a registry holding every first-party wire API.
//
// All five are registered rather than only the default vendor's, because the
// model is resolved from a catalog spec an operator writes: resolving
// "openai/gpt-6-astra" and then refusing to serve it would be a failure two
// layers away from its cause. Registration is a pure function returning a
// fresh registry; nothing is installed by import side effect.
func DefaultProviders() core.ProviderRegistry {
	reg := agentkit.DefaultProviders()
	reg.Register(anthropic.Provider(anthropic.Options{}))
	reg.Register(openai.Provider(openai.Options{}))
	reg.Register(openairesponses.Provider(openairesponses.Options{}))
	reg.Register(google.Provider(google.Options{}))
	reg.Register(ollama.Provider(ollama.Options{}))
	return reg
}
