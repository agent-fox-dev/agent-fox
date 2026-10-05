package issuex

import (
	"fmt"
	"net/http"
	"time"
)

func init() {
	RegisterAdapter(ForgeTypeGitHub, func(o Options) (Client, error) {
		return NewGitHub(o)
	})
	RegisterAdapter(ForgeTypeGitLab, func(o Options) (Client, error) {
		return NewGitLab(o)
	})
}

// NewWithOptions instantiates a Client based on the provided Options.
func NewWithOptions(o Options) (Client, error) {
	if o.NoOp {
		return NewNoOp(), nil
	}

	if o.UserAgent == "" {
		o.UserAgent = "agent-fox"
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	} else if o.HTTPClient.Timeout == 0 {
		clientCopy := *o.HTTPClient
		clientCopy.Timeout = 30 * time.Second
		o.HTTPClient = &clientCopy
	}

	// A host that names neither forge is ErrAmbiguousForge, and nothing is
	// sent to it: a credential must not leave for a host that is not yet
	// known to be the forge it belongs to. A self-hosted GitLab is named
	// with GITLAB_API_URL.
	forgeType, resolvedOpts, err := detectForge(o)
	if err != nil {
		return nil, err
	}

	if forgeType == ForgeTypeGitHub {
		return NewGitHub(resolvedOpts)
	}
	if forgeType == ForgeTypeGitLab {
		return NewGitLab(resolvedOpts)
	}

	adaptersMu.RLock()
	factory, ok := adapters[forgeType]
	adaptersMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s provider adapter is not registered", ErrUnsupportedForge, forgeType)
	}

	return factory(resolvedOpts)
}
