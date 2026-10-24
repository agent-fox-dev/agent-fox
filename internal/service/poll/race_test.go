package poll

import (
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
)

// TestRacingCreatorsForOneWorkspaceKindAndIssueYieldExactlyOneJob_TS_15_60 verifies TS-15-60: a poll cycle that finds
// af:fix on 42 and POST /api/v1/workspaces/widgets/fix {"issue": 42} race, 50 times, each on a fresh database. Whichever
// wins, exactly one fix job for 42 exists, the loser is refused as a duplicate (the API with a 409; the poll absorbs the
// label), and af:fix is removed from 42, as the poll's new request or as an absorbed label. Run it with -race.
// Verifies: 15-REQ-9.1
func TestRacingCreatorsForOneWorkspaceKindAndIssueYieldExactlyOneJob_TS_15_60(t *testing.T) {
	for i := range 50 {
		t.Run(fmt.Sprintf("race %d", i+1), func(t *testing.T) {
			// The job that wins is held by the runner, so that the other creator meets it open. The cycle is held just
			// before it makes its job, with its look-up of the open job behind it, and is let go as the POST is sent: the
			// two creators meet in the jobs layer.
			var gate *held
			e := newEnv(t, withRunner(fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))),
				withJobs(func(inner Jobs) Jobs { gate = newHeld(inner); return gate }))
			e.Hub.AddIssue(slug, fakehub.Issue{Number: 42, Labels: []string{"af:fix"}})
			e.api() // built before the race, so that it is the creators that race

			var status int
			var body string
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); e.Poll.Cycle(ctx, slug) }()
			select {
			case <-gate.reached:
			case <-time.After(10 * time.Second):
				t.Fatal("the cycle did not get to make a job")
			}
			go func() {
				defer wg.Done()
				status, body = e.call("POST", "/api/v1/workspaces/"+slug+"/fix", `{"issue": 42}`)
			}()
			close(gate.release)
			wg.Wait()

			if status != http.StatusAccepted && status != http.StatusConflict {
				t.Errorf("POST fix = %d %s, want 202 or the duplicate refusal 409", status, body)
			}
			if jobs := e.jobList(); len(jobs) != 1 || jobs[0].Kind != "fix" || jobs[0].Issue.Int64 != 42 {
				t.Errorf("jobs %+v, want exactly one fix job for 42", jobs)
			}
			if len(e.removals()) == 0 || len(e.labelsOf(42)) != 0 {
				t.Errorf("removals %v and labels %v: af:fix must be removed from 42", e.removals(), e.labelsOf(42))
			}
		})
	}
}

// TestAStreamEventAndACycleOnOneNewLabelEndWithOneJob_TS_18_52 verifies TS-18-52, a property: in 50 rounds, a new label seen
// by an event of the stream and by a cycle at the same moment, the forge's listing lagging the removal, ends with exactly one
// job for the issue and the label off it.
// Verifies: 18-REQ-7.8
func TestAStreamEventAndACycleOnOneNewLabelEndWithOneJob_TS_18_52(t *testing.T) {
	e := streaming(t)
	e.Hub.SetRemovalLag(slug, 2)
	for round := range 50 {
		n := 1000 + round
		e.issue(n, "af:fix")
		id := fmt.Sprintf("race-%d", round)
		left := len(e.Logs.find("WARN", "left to the poll"))
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); e.labeled(id, n, "af:fix") }()
		go func() { defer wg.Done(); e.cycle() }()
		wg.Wait()
		waitFor(t, "the event to be handled", func() bool { return e.taken(id) || len(e.Logs.find("WARN", "left to the poll")) > left })
		if got := e.jobsFor(n); got != 1 {
			t.Fatalf("round %d: %d jobs for %d, want 1", round, got, n)
		}
		if slices.Contains(e.labelsOf(n), "af:fix") {
			t.Fatalf("round %d: the label is still on %d", round, n)
		}
	}
}
