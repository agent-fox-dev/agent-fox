package codefix

import (
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/checks"
)

func TestCommitBodyStatesTheCheckNotTheModel(t *testing.T) {
	ok := checks.Result{Command: "make check", OK: true}
	got := commitBody("Skips the expiry check. `make check` passes.", ok)
	if strings.Contains(got, "passes") || !strings.Contains(got, "Checks (run by the tool): `make check` passed.") {
		t.Errorf("body = %q", got)
	}
	if got := commitBody("All tests pass.", checks.Result{Skipped: true}); got != "" {
		t.Errorf("an unverified run kept a claim: %q", got)
	}
	if got := commitBody("Fixes it.", checks.Result{Command: "make check", ExitCode: 1}); strings.Contains(got, "Checks (run") {
		t.Errorf("a failing check was reported as passing: %q", got)
	}
}
