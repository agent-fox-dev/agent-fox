package jobs_test

import (
	"slices"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
)

// TestTheRunnerIsGivenThePositionOfTheAttemptInItsRound verifies the part of
// 13-REQ-8.1 that this layer owns: the attempts that follow one another by themselves are a
// round, so the position of an attempt is 1 for the first attempt of a job, 2 for the attempt that
// follows a failure that is tried again, and 1 again for the attempt an answer starts and for
// one that a retry starts.
// Verifies: 13-REQ-8.1, 13-REQ-9.4
func TestTheRunnerIsGivenThePositionOfTheAttemptInItsRound(t *testing.T) {
	r := fakerunner.New(
		fakerunner.Failed("brain", true),    // attempt 1: attempt 2 follows by itself
		question(),                          // attempt 2: blocked
		fakerunner.Failed("sandbox", false), // attempt 3 (the answer): failed for good
		fakerunner.Done(),                   // attempt 4 (the retry)
	)
	e := newEnv(t, withRunner(r))
	job := e.create("widgets", jobs.KindFix, 42)
	e.Start()
	e.waitState(job.ID, jobs.StateBlocked)
	if resp := e.answer(job.ID, map[string]string{"answer": "yes"}); resp.Status != 202 {
		t.Fatalf("answer: %d\n%s", resp.Status, resp.Body)
	}
	e.waitState(job.ID, jobs.StateFailed)
	if resp := e.call("POST", "/api/v1/jobs/"+job.ID+"/retry", nil); resp.Status != 202 {
		t.Fatalf("retry: %d\n%s", resp.Status, resp.Body)
	}
	e.waitState(job.ID, jobs.StateDone)

	var rounds, attempts []int
	for _, c := range r.Calls() {
		rounds, attempts = append(rounds, c.Round), append(attempts, c.Attempt)
	}
	if !slices.Equal(attempts, []int{1, 2, 3, 4}) || !slices.Equal(rounds, []int{1, 2, 1, 1}) {
		t.Errorf("attempts %v have the positions %v in their rounds, want [1 2 1 1]", attempts, rounds)
	}
}
