package main

import (
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-13-35 (smoke): impl's repair phase runs at its own effort, independent of
// the run's, through the real app — no stub PreflightExec, no warnings recorded
// by hand.
//
// Verifies: 13-PATH-4, 13-REQ-3.1, 13-REQ-3.2
//
// Real components: Common.Register, ResolveModelNamed, agentrun.ModelSpec,
// catalog.ClampThinkingLevel, impl.resolveRepairRunner, PreflightExec
func TestTS13_35_ImplRepairPhaseRunsAtOwnEffort(t *testing.T) {
	implEnv(t)
	t.Setenv("AF_MODEL_EFFORT", "")
	dir, _ := preflightSpecRepo(t)

	code, env := implPreflight(t, dir,
		"--model", "STANDARD", "--effort", "low",
		"--repair-model", "ADVANCED", "--repair-model-effort", "high")
	if code != toolio.ExitOK {
		t.Fatalf("code = %d, error %+v", code, env.Error)
	}

	// The run: STANDARD at low effort.
	if env.Model == nil || env.Model.Thinking != "low" || !strings.Contains(env.Model.ID, "sonnet") {
		t.Errorf("the run's model = %+v, want a sonnet model (STANDARD) at low", env.Model)
	}

	// The repair phase: ADVANCED at high — not the tier's xhigh, not the run's
	// low — resolved and its credential checked before any phase runs.
	entry, ok := env.repairEntry()
	if !ok || !entry.OK {
		t.Fatalf("no ok repair_model_credential entry: %+v", env.Result.Preflight)
	}
	if !strings.Contains(entry.Detail, "opus") || !strings.Contains(entry.Detail, "effort high") {
		t.Errorf("repair_model_credential detail = %q, want an opus model at effort high", entry.Detail)
	}
	if strings.Contains(entry.Detail, "xhigh") || strings.Contains(entry.Detail, "effort low") {
		t.Errorf("repair_model_credential detail = %q: the repair effort leaked from the tier or the run", entry.Detail)
	}

	// Nothing was clamped, so nothing is warned about.
	for _, w := range env.Warnings {
		if w.Code == toolio.WarnEffortClamped {
			t.Errorf("unexpected effort_clamped warning: %+v", w)
		}
	}
}
