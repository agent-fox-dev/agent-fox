package worker

import (
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/worker/recovery"
)

// TestADeliveryStoppedAtAStageLeavesWhatAProcessThatDiedThereLeaves guards the seam the recovery
// tests are made with: the delivery returns at once, and nothing that the end of an attempt does has
// been done: the lease is held, the session is open and nothing is reported, the journal has no
// ending, the issue is marked, the clone holds the change that was applied and no branch was made for
// it.
func TestADeliveryStoppedAtAStageLeavesWhatAProcessThatDiedThereLeaves(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.runner.abortAfter = "applied"

	res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

	var stopped stageAbort
	if !errors.As(err, &stopped) || stopped.stage != "applied" || len(res.Envelope) != 0 {
		t.Fatalf("result %q, error %v, want the delivery stopped after applied", res.Envelope, err)
	}
	leases := e.hub.Leases()
	if len(leases) != 1 || leases[0].Released || len(e.hub.RequestsTo("DELETE", sandboxesPath+"/"+leases[0].ID)) != 0 {
		t.Errorf("leases = %+v, want the one lease held", leases)
	}
	sessions := e.hub.Sessions()
	if len(sessions) != 1 || sessions[0].Status != "open" || len(sessions[0].Usage) != 0 || len(e.hub.EventBatches(slug, sessions[0].RunID)) != 0 {
		t.Errorf("sessions = %+v, want one open and nothing reported", sessions)
	}
	j := journalOf(t, e)
	if len(j.Deliveries) != 1 || j.Deliveries[0].Ending != "" || len(j.Leases) != 1 || j.Leases[0].Released || j.Landing.Plan != nil {
		t.Errorf("journal = deliveries %+v, leases %+v, plan %v, want a delivery with no ending, the lease held and no plan", j.Deliveries, j.Leases, j.Landing.Plan)
	}
	if issue := e.issueNow(); !slices.Equal(issue.Labels, []string{"af:in-progress"}) || len(issue.Comments) != 0 {
		t.Errorf("issue 42 has the labels %v and %d comments, want in-progress and none", issue.Labels, len(issue.Comments))
	}
	out, err := exec.Command("git", "-C", filepath.Join(e.dir, cloneDirName), "status", "--porcelain").Output()
	if err != nil || !strings.Contains(string(out), "src/greet.sh") {
		t.Errorf("git status of the clone = %q, %v, want the applied change in the working tree", out, err)
	}
	if got := localBranches(t, e.dir); len(got) != 0 {
		t.Errorf("the clone has the branches %v, want none: a process that died parks nothing", got)
	}
	if _, err := recovery.NewStore(e.dir).Load(); err != nil {
		t.Errorf("the journal: %v", err)
	}
}
