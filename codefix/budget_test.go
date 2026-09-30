package codefix

import (
	"context"
	"errors"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-06-56 (integration): fix stops with category budget before the
// implementation phase when the analyse phase alone has already spent the
// --total-budget.
func TestTS06_56_FixStopsAtTheBoundaryWhenOverTotalBudget(t *testing.T) {
	ws, g := newRepo(t, 0)
	b := defaultBrain()
	b.analyzeCost = 0.50
	o := newOptions(ws, g, b)
	o.TotalBudgetUSD = 0.01

	got, err := Run(context.Background(), o)
	if err == nil {
		t.Fatal("want a budget failure")
	}
	var f *Failure
	if !errors.As(err, &f) {
		t.Fatalf("err = %v, want a *Failure", err)
	}
	if f.Category != agentrun.CategoryBudget {
		t.Errorf("Category = %q, want budget", f.Category)
	}
	if f.Stage != "budget" {
		t.Errorf("Stage = %q, want budget (the --total-budget fix_hint keys on it)", f.Stage)
	}
	if f.TotalBudgetUSD() != 0.01 {
		t.Errorf("TotalBudgetUSD() = %v", f.TotalBudgetUSD())
	}
	if b.analyzed != 1 || b.implemented != 0 {
		t.Errorf("analyzed=%d implemented=%d, want the implement phase never to start", b.analyzed, b.implemented)
	}
	if got == nil || got.Branch != "" {
		t.Errorf("a run stopped at the boundary must not have branched: %+v", got)
	}

	// The error object carries the ceiling, so the envelope can hint at
	// --total-budget rather than --budget.
	info := toolio.ErrorFrom("run", err)
	if info.Category != "budget" || info.TotalBudgetUSD() != 0.01 {
		t.Errorf("ErrorFrom = %+v", info)
	}
	if code := toolio.ExitCodeFor(info.Category); code != toolio.ExitFailed {
		t.Errorf("exit code = %d", code)
	}
}

// Under the ceiling, or with no ceiling at all, the boundary lets the run
// through.
func TestFixBoundaryLetsARunUnderBudgetThrough(t *testing.T) {
	for name, total := range map[string]float64{"unset": 0, "above the spend": 10} {
		ws, g := newRepo(t, 0)
		b := defaultBrain()
		b.analyzeCost = 0.50
		o := newOptions(ws, g, b)
		o.TotalBudgetUSD = total
		got, err := Run(context.Background(), o)
		if err != nil {
			t.Fatalf("%s: Run: %v", name, err)
		}
		if got.Stage != "landed" || b.implemented != 1 {
			t.Errorf("%s: Stage=%q implemented=%d", name, got.Stage, b.implemented)
		}
	}
}
