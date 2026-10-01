package toolio_test

import (
	"testing"

	"github.com/agent-fox-dev/agentfox/codefix"
	"github.com/agent-fox-dev/agentfox/codeimpl"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuetriage"
	"github.com/agent-fox-dev/agentfox/specgen"
)

// TS-11-40 (unit): Resumable() is false for every Result whose Stage is
// preflight, even when impl's Branch is populated (the name the branch would
// have) or spec names a split plan.
//
// Verifies: 11-REQ-6.4
func TestTS11_40_ResumableIsFalseForEveryPreflightResult(t *testing.T) {
	cases := map[string]toolio.Resumabler{
		"impl":  codeimpl.Result{Stage: "preflight", Branch: "feature/x"},
		"fix":   codefix.Result{Stage: "preflight"},
		"spec":  specgen.Result{Stage: "preflight", SplitPlan: ".specs/x.split.json"},
		"issue": issuetriage.Result{Stage: "preflight"},
	}
	for tool, r := range cases {
		if r.Resumable() {
			t.Errorf("%s: Resumable() = true for a preflight result", tool)
		}
	}
	// The ordinary rules are untouched: an impl run on a branch is resumable.
	if !(codeimpl.Result{Stage: "implementing", Branch: "feature/x"}).Resumable() {
		t.Error("an ordinary impl run on a branch is no longer resumable")
	}
}
