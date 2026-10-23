package poll

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
)

// The smoke tests follow the paths of the spec through the real code: the HTTP API, the jobs layer on a state file, the fake
// runner, the poll's cycle and its records, the hub forge client and the fake hub. The end to end test with the real worker is
// in internal/worker, which has the harness that runs it (TS-15-65).

// TestALabelAddedToAnIssueWithARunningApiFixJobIsAbsorbed_TS_15_66 verifies TS-15-66 and 15-PATH-2: POST /fix makes a job
// that runs, af:fix is added to the issue, and a cycle makes no second job, removes the label through hub and logs the
// absorbed label with the workspace, the kind, the issue and the job.
// Verifies: 15-PATH-2, 15-REQ-5.1
func TestALabelAddedToAnIssueWithARunningApiFixJobIsAbsorbed_TS_15_66(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))))
	e.issue(42)
	status, body := e.call("POST", "/api/v1/workspaces/"+slug+"/fix", `{"issue": 42}`)
	if status != http.StatusAccepted {
		t.Fatalf("POST fix = %d %s", status, body)
	}
	id := e.jobList()[0].ID
	e.waitState(id, jobs.StateRunning)
	e.Hub.AddLabel(slug, 42, "af:fix")

	e.cycle()

	if n := len(e.jobList()); n != 1 {
		t.Errorf("%d jobs, want the one of the API", n)
	}
	if got := e.removals(); !slices.Equal(got, []string{"remove(42,af:fix)"}) {
		t.Errorf("removals %v, want one of af:fix from 42", got)
	}
	infos := e.Logs.find("INFO", "absorbed")
	if len(infos) != 1 || infos[0]["workspace"] != slug || infos[0]["kind"] != "fix" || infos[0]["issue"] != float64(42) || infos[0]["job"] != id {
		t.Errorf("the INFO records are %v, want one that names the workspace, the kind, the issue and the job %s", infos, id)
	}
	if len(e.labelsOf(42)) != 0 {
		t.Errorf("the label is still on the issue: %v", e.labelsOf(42))
	}
}

// TestALabelTheCancelPutBackIsHeldReleasedAfterAManualRemovalAndThenMakesOneNewJob_TS_15_67 verifies TS-15-67 and
// 15-PATH-3: the poll makes a fix job for 42 and takes the label; the job is cancelled through the API while its runner
// holds it, the label is put back and the runner returns reporting it as set; cycles make no job and remove nothing; a person
// removes the label; 5 minutes pass and a complete listing without 42 releases the hold; and adding the label again makes
// exactly one new job.
// Verifies: 15-PATH-3, 15-REQ-7.4
func TestALabelTheCancelPutBackIsHeldReleasedAfterAManualRemovalAndThenMakesOneNewJob_TS_15_67(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Block(cancelledResult())).Then(fakerunner.Block(fakerunner.Step{}))))
	e.takenByThePoll()
	id := e.jobList()[0].ID

	if status, body := e.call("POST", "/api/v1/jobs/"+id+"/cancel", ""); status != http.StatusOK {
		t.Fatalf("cancel = %d %s", status, body)
	}
	e.Hub.AddLabel(slug, 42, "af:fix") // the cancel cleanup of the worker puts it back
	e.runnerReturned(id)

	for i := range 3 {
		e.cycle()
		if e.jobsFor(42) != 1 || e.removalsOf(42) != 1 || !slices.Contains(e.labelsOf(42), "af:fix") {
			t.Fatalf("during the hold, cycle %d: jobs %d, removals %d, labels %v; want no job made and no removal", i+1, e.jobsFor(42), e.removalsOf(42), e.labelsOf(42))
		}
		e.Clock.Advance(time.Minute)
	}

	e.Hub.RemoveLabel(slug, 42, "af:fix") // a person takes the label off
	e.Clock.Advance(5 * time.Minute)
	e.cycle() // a complete listing without 42, requested long enough after the runner returned
	if _, ok := e.held(42); ok {
		t.Fatalf("the hold is not released: %+v", e.recordsOf())
	}

	e.Hub.AddLabel(slug, 42, "af:fix") // and asks again
	e.cycle()
	if e.jobsFor(42) != 2 {
		t.Errorf("%d jobs for 42 after the label was added again, want exactly one new job: 2", e.jobsFor(42))
	}
	e.cycle()
	if e.jobsFor(42) != 2 {
		t.Errorf("%d jobs for 42 after another cycle, want 2", e.jobsFor(42))
	}
}

// TestAFailedRemovalIsOwedAndTheLabelIsTakenOnceNotTwice_TS_15_68 verifies TS-15-68 and 15-PATH-4: hub answers the first
// removal of af:fix from 42 with 503, so the cycle makes the job, leaves the removal owed and logs a WARN; the job ends
// done; the next cycle removes the label and makes no job, and the record is dropped; the cycle after it makes nothing.
// Verifies: 15-PATH-4, 15-REQ-6.6
func TestAFailedRemovalIsOwedAndTheLabelIsTakenOnceNotTwice_TS_15_68(t *testing.T) {
	e := newEnv(t)
	e.issue(42, "af:fix")
	e.Hub.Inject("DELETE", removalPath(42, "af:fix"), fakehub.Fault{Status: 503, Once: true})

	e.cycle()

	got := e.jobList()
	if len(got) != 1 {
		t.Fatalf("%d jobs after the first cycle, want one", len(got))
	}
	if warns := e.Logs.level("WARN"); len(warns) != 1 {
		t.Errorf("%d warnings after the failed removal, want exactly one: %v", len(warns), warns)
	}
	if recs := e.recordsOf(); len(recs) != 1 || !recs[0].Owed || recs[0].JobID != got[0].ID {
		t.Errorf("records %+v, want one with the removal owed and the job", recs)
	}
	e.waitState(got[0].ID, jobs.StateDone)

	e.cycle()

	if len(e.jobList()) != 1 || len(e.labelsOf(42)) != 0 || len(e.removals()) != 2 {
		t.Errorf("jobs %d, labels %v, removals %v; want the label removed by the second removal and no second job", len(e.jobList()), e.labelsOf(42), e.removals())
	}
	if recs := e.recordsOf(); len(recs) != 0 {
		t.Errorf("records %+v, want the record dropped", recs)
	}
	e.cycle()
	if len(e.jobList()) != 1 || len(e.removals()) != 2 {
		t.Errorf("the cycle after made %d jobs and %d removals, want nothing", len(e.jobList()), len(e.removals()))
	}
}
