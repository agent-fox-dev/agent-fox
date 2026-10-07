package main

import (
	"errors"
	"testing"

	"github.com/agent-fox-dev/agentfox/codeimpl"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// Issue #218 (5): every parked run exits 4, whatever stopped the task — an
// empty change, a phase without a result, a cancellation, the budget — as the
// usage text says. A blocked park still asks a person (3), and a failure that
// parked nothing keeps its own code.
func TestAParkedRunExitsUnverified(t *testing.T) {
	for _, tc := range []struct {
		category string
		parked   bool
		want     int
	}{
		{"empty_change", true, toolio.ExitUnverified},
		{"no_result", true, toolio.ExitUnverified},
		{"aborted", true, toolio.ExitUnverified},
		{"budget", true, toolio.ExitUnverified},
		{"unverified", true, toolio.ExitUnverified},
		{"blocked", true, toolio.ExitNeedsHuman},
		{"budget", false, toolio.ExitFailed},
		{"usage", false, toolio.ExitUsage},
	} {
		err := &codeimpl.Failure{Stage: "task", Category: tc.category, Err: errors.New("x"), Parked: tc.parked}
		info := toolio.ErrorFrom("run", err)
		if got := exitCode(err, info); got != tc.want {
			t.Errorf("%s (parked=%v): exit %d, want %d", tc.category, tc.parked, got, tc.want)
		}
	}
}
