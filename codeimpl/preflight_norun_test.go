package codeimpl

import (
	"context"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
)

// TS-11-35 (unit, impl's share): RunPreflight never asks the model for
// anything: the provider sees no call and the envelope carries no usage.
//
// Verifies: 11-REQ-5.7
func TestTS11_35_RunPreflightNeverCallsTheRunner(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	o := preflightOptions(t, ws, g)
	p := faux.New()
	runner, err := agentrun.NewRunner(agentrun.Config{
		Model:     faux.Model(),
		Providers: core.ProviderRegistry{faux.API: p.APIProvider()},
		Workspace: ws,
		Bounds:    agentrun.Bounds{MaxTurns: 8, MaxBudgetUSD: 1, MaxAttempts: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	o.Runner = runner

	res, err := RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if n := p.Calls(); n != 0 {
		t.Errorf("the provider saw %d model calls", n)
	}
	if env := o.Run.Envelope(0, res, nil); env.Usage != nil {
		t.Errorf("a phase was recorded: %+v", env.Usage)
	}
}
