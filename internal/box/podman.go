package box

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
)

// GOOS is the operating system the messages of the podman provider are made for; a test
// replaces it.
var GOOS = runtime.GOOS

// podmanEntry is the provider that runs the tools of a box in a container of the
// engine on PATH.
func podmanEntry() Entry {
	return Entry{
		Name:    "podman",
		ByName:  true,
		Build:   func(ProviderConfig) (sandbox.Provider, error) { return newPodmanProvider() },
		Explain: explainPodman,
	}
}

// imageProblems are what the engine says of an image that is not there and cannot
// be pulled, lower case: the provider hands its errors on as text.
var imageProblems = []string{
	"image not known", "image not found", "no such image", "manifest unknown", "name unknown",
	"pull access denied", "unable to pull", "failed to pull", "error pulling", "repository does not exist",
	"initializing source",
}

// explainPodman says what an error of the podman provider is. A run that was stopped is
// the error of its context, not a fault of the engine. An image that has no agent in it
// (the provider's own error) is a failure of the image, which no other run will mend.
// An image that is missing and cannot be pulled is a failure that another run may get
// past, once the image is there; anything else is an engine that cannot be reached (it
// is not running, it is not installed, a machine is not started), and no other run will
// do better until someone has looked.
func explainPodman(req Request, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, sandbox.ErrNoAgent) {
		image := req.Image
		if image == "" {
			image = "of the sandbox"
		}
		return &toolio.Failure{
			Category: toolio.CategorySandbox,
			Message:  fmt.Sprintf("the image %s has no agent to run the tools of the box with: %v; build the tools image (make build-tools-container) or name another with AF_SANDBOX_IMAGE", image, err),
			FixHint:  &toolio.FixHint{Env: "AF_SANDBOX_IMAGE"},
		}
	}
	text := strings.ToLower(err.Error())
	for _, problem := range imageProblems {
		if strings.Contains(text, problem) {
			image := req.Image
			if image == "" {
				image = "of the sandbox"
			}
			return &toolio.Failure{
				Category:  toolio.CategorySandbox,
				Retryable: true,
				Message:   fmt.Sprintf("the image %s is missing and cannot be pulled: %v; build it (make build-tools-container) or name another with AF_SANDBOX_IMAGE", image, err),
				FixHint:   &toolio.FixHint{Env: "AF_SANDBOX_IMAGE"},
			}
		}
	}
	message := fmt.Sprintf("the podman engine cannot be reached: %v", err)
	if GOOS != "linux" {
		message += "; if its machine is not running, start it with `podman machine start`"
	}
	return &toolio.Failure{Category: toolio.CategorySandbox, Message: message, FixHint: &toolio.FixHint{Flag: "--sandbox"}}
}
