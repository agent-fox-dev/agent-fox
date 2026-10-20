package brain

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/catalog"
	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/provider/anthropic"
	"github.com/agent-fox-dev/agentkit-go/schema"
)

// This file is where the AgentKit brain meets agentkit-go: the model and its
// provider, the schema of the terminating tool, and the one wrapper over a
// provider that the brain needs. What the PRD assumed of the driver and what it
// is, as read from the module:
//
//   - agentkit.New(Config) returns an *Agent whose Run(ctx, prompt) runs the
//     first user message to a stop, and returns the result with the error of a
//     limit (the turn bound, the cost bound) as well;
//   - Config.Prefix is a []core.Message, sent after the system prompt on every
//     request, and not a string;
//   - the interceptor that decides before a call is Toolset.Guard (a
//     core.BeforeToolCall) and the one that sees the result is Toolset.After;
//   - a tool that ends the run is core.Tool.Terminating, and its result's
//     Terminate is its vote;
//   - the model is required, and the provider is Config.Provider (the faux
//     provider in tests) or the one built from a client;
//   - a call to a name that is not in the toolset is answered by the driver
//     itself, before the guard is asked, which is why the tools that the brief
//     does not allow are registered, to be denied by the gate, and are hidden
//     from the model by a wrapper over the provider (see visibleTools).

// defaultModel is the model of a run that names none: the default of the
// catalog for the vendor of the production provider.
func defaultModel() string {
	return catalog.Default().DefaultModelID("anthropic")
}

// lookupEnv is a Getenv as the Env that the provider of the vendor reads.
type lookupEnv Getenv

func (l lookupEnv) Lookup(name string) (string, bool) { return l(name) }

// anthropicProvider is the production provider for the model, with the
// credentials of the environment. A missing credential is failed/auth, with the
// variable to set, and is found here, when the brain is constructed, before any
// box is opened.
func anthropicProvider(model string, get Getenv) (core.ProviderClient, error) {
	client, _, err := anthropic.Resolve(lookupEnv(get))
	if errors.Is(err, anthropic.ErrNoCredentials) {
		return nil, &toolio.Failure{
			Category: toolio.CategoryAuth,
			Message:  "the agentkit brain needs a credential for the model: " + err.Error(),
			FixHint:  &toolio.FixHint{Env: anthropic.APIKeyVar},
		}
	}
	if err != nil {
		return nil, &toolio.Failure{Category: toolio.CategoryConfig, Message: fmt.Sprintf("the model provider cannot be configured from the environment: %v", err)}
	}
	m, _ := catalog.Lookup(model)
	return anthropic.Provider(m, anthropic.Options{Client: client}), nil
}

// inputSchema is the schema of the terminating tool, from the JSON Schema of the
// kind; a kind with none takes any object.
func inputSchema(raw []byte) (*schema.Schema, error) {
	if len(raw) == 0 {
		return schema.Object(), nil
	}
	s, err := schema.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("brain: the schema of the kind's tool is not usable: %w", err)
	}
	return s, nil
}

// visibleTools is a provider that leaves some tools out of what it offers the
// model, and passes the rest on. The tools that the brief does not allow stay in
// the driver's toolset, where a call to one is denied by the gate with the
// allowlist's own words (the driver answers a name that it does not know itself,
// without asking the guard, and says only that the tool is not there); the model
// is never told that they exist.
type visibleTools struct {
	inner  core.ProviderClient
	hidden []string
}

func (p visibleTools) Stream(ctx context.Context, req core.Request) (<-chan core.StreamEvent, error) {
	req.Tools = slices.DeleteFunc(slices.Clone(req.Tools), func(t core.ToolWire) bool { return slices.Contains(p.hidden, t.Name) })
	return p.inner.Stream(ctx, req)
}
