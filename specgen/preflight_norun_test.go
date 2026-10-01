package specgen

import (
	"context"
	"testing"
)

// TS-11-35 (unit, spec's share): RunPreflight never asks the model for
// anything: the provider sees no call and the envelope carries no usage.
//
// Verifies: 11-REQ-5.7
func TestTS11_35_RunPreflightNeverCallsTheRunner(t *testing.T) {
	o := preflightOptions(t)
	runner, provider := fauxRunner(t, o.Workspace)
	o.Runner = runner

	res, err := RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if n := provider.Calls(); n != 0 {
		t.Errorf("the provider saw %d model calls", n)
	}
	if env := o.Run.Envelope(0, res, nil); env.Usage != nil {
		t.Errorf("a phase was recorded: %+v", env.Usage)
	}
}
