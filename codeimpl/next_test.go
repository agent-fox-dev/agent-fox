package codeimpl

import (
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/checks"
)

// TS-06-35 (unit): impl suggests itself on the same spec directory when the
// run parked (06-REQ-6.4).
func TestTS06_35_NextSuggestsSelfWhenParked(t *testing.T) {
	r := &Result{Stage: "parked", SpecDir: ".specs/09_thing"}
	n := r.Next()
	if len(n) != 1 {
		t.Fatalf("Next() = %+v, want one entry", n)
	}
	e := n[0]
	if e.Tool != "impl" || e.Input != ".specs/09_thing" {
		t.Errorf("entry = %+v", e)
	}
	if e.Flags == nil || len(e.Flags) != 0 {
		t.Errorf("flags = %#v, want a non-nil empty slice", e.Flags)
	}
	if e.Why != "the run parked; re-running continues from the last landed task" {
		t.Errorf("why = %q", e.Why)
	}

	// A run that did not park suggests nothing.
	r.Stage = "landed"
	if n := r.Next(); n != nil {
		t.Errorf("landed Next() = %+v, want nil", n)
	}
}

// TS-06-36 (unit): a park with a red baseline and no repair attempt adds
// --repair and says the baseline needs repairing first (06-REQ-6.5).
func TestTS06_36_NextAddsRepairForRedBaseline(t *testing.T) {
	red := GateResult{Checks: []checks.Result{{Command: "go test ./...", ExitCode: 1, OK: false}}}
	if len(red.failing()) == 0 {
		t.Fatal("fixture baseline must have a failing check")
	}
	r := &Result{Stage: "parked", SpecDir: ".specs/09_thing", Baseline: red}
	e := r.Next()[0]
	found := false
	for _, f := range e.Flags {
		if f == "--repair" {
			found = true
		}
	}
	if !found {
		t.Errorf("flags = %v, want --repair", e.Flags)
	}
	if !strings.Contains(e.Why, "baseline") || !strings.Contains(e.Why, "repair") {
		t.Errorf("why = %q, want it to say the baseline needs repairing first", e.Why)
	}

	// A repair was attempted: no --repair suggestion.
	r.Repair = &RepairReport{}
	if e := r.Next()[0]; len(e.Flags) != 0 {
		t.Errorf("flags after repair = %v, want none", e.Flags)
	}
}

// TS-06-39 (unit): impl's next is omitted when there is nothing to suggest
// (06-REQ-6.8).
func TestTS06_39_ImplNextOmittedWhenNothingToSuggest(t *testing.T) {
	var nilRes *Result
	if n := nilRes.Next(); n != nil {
		t.Errorf("nil Result Next() = %+v, want nil", n)
	}
	if n := (&Result{Stage: "landed"}).Next(); n != nil {
		t.Errorf("Next() = %+v, want nil", n)
	}
}
