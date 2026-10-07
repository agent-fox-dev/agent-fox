package specgen

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-06-57 (integration): spec stops with category budget before a scope's
// PRD phase when --total-budget is already spent, leaving the split plan in
// place; an unsplit input has no interior boundary.
func TestTS06_57_SpecStopsBetweenScopesWhenOverTotalBudget(t *testing.T) {
	ws := newWorkspace(t)
	a := splitAuthor(t)
	a.prdCost = 0.30
	o := fileOptions(ws, a)
	o.TotalBudgetUSD = 0.25

	got, err := Run(context.Background(), o)
	if err == nil {
		t.Fatal("want a budget failure before the second scope")
	}
	var f *Failure
	if !errors.As(err, &f) {
		t.Fatalf("err = %v, want a *Failure", err)
	}
	if f.Category != agentrun.CategoryBudget || f.Stage != "budget" {
		t.Errorf("Stage=%q Category=%q, want budget/budget", f.Stage, f.Category)
	}
	if f.TotalBudgetUSD() != 0.25 {
		t.Errorf("TotalBudgetUSD() = %v", f.TotalBudgetUSD())
	}
	if !strings.Contains(err.Error(), "scope 2 of 3 (widget_github)") {
		t.Errorf("the error should name the scope it stopped before: %v", err)
	}
	info := toolio.ErrorFrom("run", err)
	if info.Category != "budget" || info.TotalBudgetUSD() != 0.25 {
		t.Errorf("ErrorFrom = %+v", info)
	}

	// Only the first scope's PRD phase ran; its package is reported and the
	// plan is left to resume from.
	if len(a.prdRequests) != 1 {
		t.Errorf("%d PRD phases ran, want 1", len(a.prdRequests))
	}
	if got == nil || got.SpecID != "01" {
		t.Fatalf("the first package should be reported: %+v", got)
	}
	planPath := filepath.Join(ws.Root, ".specs", "widget_core"+SplitPlanSuffix)
	if _, err := os.Stat(planPath); err != nil {
		t.Fatalf("the plan should remain: %v", err)
	}
	if got.SplitPlan == "" {
		t.Error("SplitPlan should name the plan left in place")
	}
	var states []string
	for _, s := range got.Split {
		states = append(states, s.Status)
	}
	// The run stopped on the second scope, so that scope is the one marked
	// failed, as for every other stop (#223); the third was never reached.
	if strings.Join(states, ",") != "done,failed,pending" {
		t.Errorf("scope states = %v", states)
	}

	// A resumed run with no ceiling finishes the split.
	b := splitAuthor(t)
	got, err = Run(context.Background(), fileOptions(ws, b))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(got.Split) != 3 || got.Split[2].Status != ScopeDone {
		t.Errorf("Split after resume = %+v", got.Split)
	}
}

// An input that does not split has no interior boundary: a tiny
// --total-budget does not stop it between phases (it is bound only by the
// one phase's folded ceiling, applied by the shared shell).
func TestTS06_57_UnsplitInputHasNoInteriorBoundary(t *testing.T) {
	ws := newWorkspace(t)
	a := newAuthor(t, "01", "test_feature")
	a.prdCost = 0.30
	o := newOptions(ws, a)
	o.TotalBudgetUSD = 0.01

	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.SpecID != "01" || !got.Validation.Valid {
		t.Errorf("the package should be written: %+v", got)
	}
}

// Without --total-budget, spend never stops a split.
func TestSplitIsNotStoppedWithoutATotalBudget(t *testing.T) {
	ws := newWorkspace(t)
	a := splitAuthor(t)
	a.prdCost = 100
	if _, err := Run(context.Background(), fileOptions(ws, a)); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
