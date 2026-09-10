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

	forgeType, resolvedOpts, err := detectForge(o)
	if err != nil {
		return nil, err
	}

	if forgeType == ForgeTypeGitHub {
		return NewGitHub(resolvedOpts)
	}

	adaptersMu.RLock()
	factory, ok := adapters[forgeType]
	adaptersMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s provider adapter is not registered", ErrUnsupportedForge, forgeType)
	}

	return factory(resolvedOpts)
}
