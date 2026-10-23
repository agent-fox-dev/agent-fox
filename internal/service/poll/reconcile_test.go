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

// seenOf are the records of the workspace by their issue.
func (e *env) seenOf() map[int]Record {
	e.t.Helper()
	out := map[int]Record{}
	for _, r := range e.recordsOf() {
		out[r.Issue] = r
	}
	return out
}

// TestACycleReconcilesItsRecordsWithTheirJobsBeforeAnyHubRequestEnabledOrNot_TS_15_40 verifies TS-15-40: with the poll
// off, a record of a job that is done or was cancelled without its cancel reaching a runner is ended, one whose cancel
// reached a runner that has returned holds, with its release clock at the time, and so does one whose job retention
// purged before the poll saw it end, from the time it was found gone; a record of a job that is queued, running,
// blocked or failed has not ended; and all of it is done before the first request of hub.
// Verifies: 15-REQ-6.4
func TestACycleReconcilesItsRecordsWithTheirJobsBeforeAnyHubRequestEnabledOrNot_TS_15_40(t *testing.T) {
	var e *env
	var atFirstRequest map[int]Record
	r := fakerunner.New(fakerunner.Done(), fakerunner.Done(), fakerunner.Failed("sandbox", false), needsHuman, fakerunner.Block(fakerunner.Step{}), fakerunner.Block(fakerunner.Step{}))
	e = newEnv(t, withRunner(r), withJobsConfig(func(c *jobs.Config) { c.Retain = time.Hour }), withVars(map[string]string{"AF_POLL_INTERVAL": "0"}),
		withBeforeRequest(func(*http.Request) {
			if atFirstRequest == nil {
				atFirstRequest = e.seenOf()
			}
		}))

	// The job that retention purges is made first, and the clock moves past the retention.
	purged := e.startedJob(jobs.KindFix, 1, "af:fix")
	e.waitState(purged, jobs.StateDone)
	e.Clock.Advance(2 * time.Hour)
	waitFor(t, "retention to purge the job", func() bool { _, err := e.Jobs.Standing(ctx, purged); return err != nil })

	done := e.startedJob(jobs.KindFix, 2, "af:fix")
	e.waitState(done, jobs.StateDone)
	failed := e.startedJob(jobs.KindFix, 3, "af:fix")
	e.waitState(failed, jobs.StateFailed)
	blocked := e.startedJob(jobs.KindFix, 4, "af:fix")
	e.waitState(blocked, jobs.StateBlocked)
	cancelledInRunner := e.startedJob(jobs.KindFix, 5, "af:fix")
	e.waitState(cancelledInRunner, jobs.StateRunning)
	// The job is running once it is claimed, a moment before its runner is called: the runner is waited for, so that the
	// cancel reaches a runner that is in progress.
	if !r.Wait(5, 10*time.Second) {
		t.Fatal("the runner of the job was not called")
	}
	if _, err := e.Jobs.Cancel(ctx, cancelledInRunner); err != nil {
		t.Fatal(err)
	}
	e.runnerReturned(cancelledInRunner)
	running := e.startedJob(jobs.KindFix, 6, "af:fix")
	e.waitState(running, jobs.StateRunning)
	queued := e.startedJob(jobs.KindFix, 7, "af:fix") // behind the running job
	cancelledQueued := e.startedJob(jobs.KindFix, 8, "af:fix")
	if _, err := e.Jobs.Cancel(ctx, cancelledQueued); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{purged, done, failed, blocked, cancelledInRunner, running, queued, cancelledQueued} {
		e.record(Record{Issue: i + 1, Label: "af:fix", JobID: id, Owed: true}) // owed, so that the record stays to be read
	}

	e.cycle()

	if atFirstRequest == nil {
		t.Fatal("hub was asked nothing")
	}
	if got := e.fromPoll(); !slices.Equal(got, []string{"vars"}) {
		t.Errorf("hub was asked %v, want only the variables: the poll is off", got)
	}
	now := e.Clock.Now()
	for _, tc := range []struct {
		issue int
		name  string
		seen  Seen
		clock bool
	}{
		{1, "purged before the poll saw it end", SeenHolding, true},
		{2, "done", SeenEnded, false},
		{3, "failed", SeenOpen, false},
		{4, "blocked", SeenOpen, false},
		{5, "cancelled in a runner that has returned", SeenHolding, true},
		{6, "running", SeenOpen, false},
		{7, "queued", SeenOpen, false},
		{8, "cancelled while queued", SeenEnded, false},
	} {
		for when, recs := range map[string]map[int]Record{"before the first request": atFirstRequest, "after the cycle": e.seenOf()} {
			rec, ok := recs[tc.issue]
			switch {
			case !ok:
				t.Errorf("%s, job %s: the record is gone", when, tc.name)
			case rec.Seen != tc.seen:
				t.Errorf("%s, job %s: the poll has seen %q, want %q", when, tc.name, rec.Seen, tc.seen)
			case tc.clock && !rec.ReleaseClock.Equal(now), !tc.clock && !rec.ReleaseClock.IsZero():
				t.Errorf("%s, job %s: the release clock is %v, want %v", when, tc.name, rec.ReleaseClock, map[bool]string{true: now.String(), false: "not started"}[tc.clock])
			}
		}
	}
}

// TestARecordIsDroppedOnceNothingIsOwedAndNothingHoldsAndNotBefore_TS_15_44 verifies TS-15-44: a record whose removal was
// confirmed and whose job is done, one whose hold was released, and a confirmed one that has no job are dropped; a
// record whose removal is still owed, and one whose job is open, stay.
// Verifies: 15-REQ-6.8
func TestARecordIsDroppedOnceNothingIsOwedAndNothingHoldsAndNotBefore_TS_15_44(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Done(), fakerunner.Done(), fakerunner.Block(fakerunner.Step{})).Then(fakerunner.Done())))
	owedJob := e.startedJob(jobs.KindFix, 10, "af:fix")
	e.waitState(owedJob, jobs.StateDone)
	doneJob := e.startedJob(jobs.KindFix, 12, "af:fix")
	e.waitState(doneJob, jobs.StateDone)
	openJob := e.startedJob(jobs.KindFix, 11, "af:fix")
	e.waitState(openJob, jobs.StateRunning)

	owed := e.record(Record{Issue: 10, Label: "af:fix", JobID: owedJob, Owed: true})
	open := e.record(Record{Issue: 11, Label: "af:fix", JobID: openJob})
	confirmed := e.record(Record{Issue: 12, Label: "af:fix", JobID: doneJob})
	released := e.record(Record{Issue: 13, Label: "af:fix", JobID: "job_gone", Seen: SeenEnded})
	noJob := e.record(Record{Issue: 14, Label: "af:fix", Seen: SeenEnded})
	e.issue(10, "af:fix") // its removal fails again, so it stays owed
	e.Hub.Inject("DELETE", removalPath(10, "af:fix"), fakehub.Fault{Status: 503})
	e.issue(11, "af:fix") // absorbed by the open job, and the record of it stays with the job

	e.cycle()

	var ids []int64
	for _, r := range e.recordsOf() {
		ids = append(ids, r.ID)
	}
	if want := []int64{owed.ID, open.ID}; !slices.Equal(ids, want) {
		t.Errorf("the records left are %v, want the owed one and the one whose job is open: %v (dropped: %d, %d, %d)", ids, want, confirmed.ID, released.ID, noJob.ID)
	}
}
