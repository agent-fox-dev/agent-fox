package main

import (
	"errors"
	"testing"

	"github.com/agent-fox-dev/agentfox/codefix"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// Issue #216: a run that parks its work exits 4, as the docs say, whatever
// stopped it — the checks, a cancellation, a provider error after the model
// had written files. A failure that parked nothing keeps its own code.
func TestAParkedFixExitsUnverified(t *testing.T) {
	for _, tc := range []struct {
		category string
		parked   bool
		want     int
	}{
		{"unverified", true, toolio.ExitUnverified},
		{"aborted", true, toolio.ExitUnverified},
		{"api", true, toolio.ExitUnverified},
		{"aborted", false, toolio.ExitFailed},
		{"empty_change", false, toolio.ExitFailed},
	} {
		err := &codefix.Failure{Stage: "implement", Category: tc.category, Err: errors.New("x"), Parked: tc.parked}
		if got := exitCode(err, toolio.ErrorFrom("run", err)); got != tc.want {
			t.Errorf("%s (parked=%v): exit %d, want %d", tc.category, tc.parked, got, tc.want)
		}
	}
}
