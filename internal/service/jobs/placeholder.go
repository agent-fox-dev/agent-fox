package jobs

import (
	"context"
	"encoding/json"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// CategoryUnsupported is the category of the failure of work that the service cannot
// do yet.
const CategoryUnsupported = "unsupported"

// PlaceholderRunner is a runner for a service that has no worker: it ends every attempt
// with a failed envelope of category unsupported that is not retryable. It runs no brain
// and touches no hub, forge or box. The binary runs the worker (internal/worker) in its
// place; this one is what a test of the layer can use when the runner does not matter.
type PlaceholderRunner struct{}

// Run ends the attempt as unsupported.
func (PlaceholderRunner) Run(_ context.Context, req Request) (Result, error) {
	const message = "this service does not run attempts yet"
	envelope, err := json.MarshalIndent(toolio.Envelope{
		SchemaVersion: toolio.SchemaVersion,
		Tool:          string(req.Kind),
		Status:        toolio.StatusFailed,
		ExitCode:      toolio.ExitCode(toolio.StatusFailed),
		Summary:       message,
		Warnings:      []toolio.Warning{},
		Artifacts:     []toolio.Artifact{},
		SideEffects:   []toolio.SideEffect{},
		Next:          []toolio.Next{},
		Error:         &toolio.ErrorInfo{Category: CategoryUnsupported, Message: message, Retryable: false},
		Usage:         toolio.Usage{CostSource: toolio.CostNone},
	}, "", "  ")
	if err != nil {
		return Result{}, err
	}
	return Result{Envelope: append(envelope, '\n')}, nil
}
