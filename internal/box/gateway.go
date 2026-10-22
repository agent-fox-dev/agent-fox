package box

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/gateway"
)

// GatewayName is the name of the gateway provider.
const GatewayName = "gateway"

// GatewayEntry is the provider of a box that hub leased: the gateway at url, opened with the
// token of the lease. It is made for one attempt and held in the registry of that attempt, never
// in Default, so that it is no choice of --sandbox: it needs a lease, which only the worker has.
//
// It counts as isolating, its programs are called by name from the image of the lease's
// profile, as under podman, and the credentials of the brain are in the environment the
// profile gives the box, so nothing of the host's is sent. A gateway that cannot be reached, or
// that refuses the box, is a sandbox failure that may be tried again.
func GatewayEntry(url, token string) Entry {
	return Entry{
		Name:                GatewayName,
		ByName:              true,
		SuppliesCredentials: true,
		Remote:              true,
		Build: func(cfg ProviderConfig) (sandbox.Provider, error) {
			ctx := cfg.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			provider, err := gateway.Dial(ctx, url, gateway.Options{Token: token})
			if err != nil {
				return nil, err
			}
			return provider, nil
		},
		Explain: func(req Request, err error) error { return explainGateway(token, err) },
	}
}

// explainGateway says what an error of the gateway is: a run that was stopped is the error of
// its context, and anything else, a gateway that cannot be reached, a token it does not take or
// a box it will not make, is a sandbox failure to try again. The token is never in the message.
func explainGateway(token string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	message := err.Error()
	if token != "" {
		message = strings.ReplaceAll(message, token, "[token]")
	}
	return &toolio.Failure{Category: toolio.CategorySandbox, Retryable: true, Message: fmt.Sprintf("the gateway cannot give a box: %s", message)}
}
