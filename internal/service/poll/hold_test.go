package poll

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// cancelledResult is what a runner returns when it is cancelled and reports that it set the request label itself, as the
// cancel cleanup of the worker does when it puts the label back.
func cancelledResult() fakerunner.Step {
	return fakerunner.Return(jobs.Result{
		Envelope:  fakerunner.Env(toolio.StatusFailed, fakerunner.WithError(toolio.CategoryCancelled, "the job was cancelled", false)),
		LabelsSet: []string{"af:fix"},
	})
}

// takenByThePoll runs a cycle that makes the fix job for issue 42 from af:fix and consumes the label, and returns the
// job. The runner holds it until it is cancelled or released.
func (e *env) takenByThePoll() {
	e.t.Helper()
	e.issue(42, "af:fix")
	e.cycle()
	got := e.jobList()
	if len(got) != 1 {
		e.t.Fatalf("%d jobs after the cycle, want the one", len(got))
	}
	e.waitState(got[0].ID, jobs.StateRunning)
	// The job is running once it is claimed, a moment before its runner is called: the runner is waited for, so that a
	// cancel that follows reaches a runner that is in progress.
	if !e.Runner.Wait(1, 10*time.Second) {
		e.t.Fatal("the runner was not called")
	}
}

// cancelRunning cancels the only job, which is in the runner, and puts the request label back as the cleanup of the worker
// does before the runner returns.
func (e *env) cancelRunning() string {
	e.t.Helper()
	id := e.jobList()[0].ID
	if _, err := e.Jobs.Cancel(ctx, id); err != nil {
		e.t.Fatal(err)
	}
	e.Hub.AddLabel(slug, 42, "af:fix")
	return id
}

// runnerReturned waits until no runner of the job is in progress and what it returned is recorded: a moment passes
// between the two, and a purge that comes in it finds an attempt that is still running.
func (e *env) runnerReturned(id string) {
	e.t.Helper()
	waitFor(e.t, "the runner to return", func() bool {
		s, err := e.Jobs.Standing(ctx, id)
		if err != nil || s.RunnerInProgress {
			return false
		}
		doc, err := e.Jobs.Get(ctx, id)
		return err == nil && !slices.ContainsFunc(doc.Attempts, func(a jobs.AttemptDoc) bool { return a.State == jobs.AttemptRunning })
	})
}

// jobsFor are the jobs made for the issue.
func (e *env) jobsFor(issue int) int {
	n := 0
	for _, j := range e.jobList() {
		if j.Issue.Int64 == int64(issue) {
			n++
		}
	}
	return n
}

// removalsOf are the removals of af:fix from the issue.
func (e *env) removalsOf(issue int) int {
	n := 0
	for _, r := range e.removals() {
		if r == fmt.Sprintf("remove(%d,af:fix)", issue) {
			n++
		}
	}
	return n
}

// held says whether the poll has a record that holds af:fix on the issue.
func (e *env) held(issue int) (Record, bool) {
	for _, r := range e.recordsOf() {
		if r.Issue == issue && r.Label == "af:fix" && r.Seen == SeenHolding {
			return r, true
		}
	}
	return Record{}, false
}

// TestAListedIssueUnderAHoldIsLeftAlone_TS_15_31 verifies TS-15-31.
// Verifies: 15-REQ-5.3
func TestAListedIssueUnderAHoldIsLeftAlone_TS_15_31(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Block(cancelledResult()))))
	e.takenByThePoll()
	id := e.cancelRunning()
	e.runnerReturned(id)

	e.cycle()

	if _, ok := e.held(42); !ok {
		t.Fatalf("no hold on af:fix for 42: %+v", e.recordsOf())
	}
	if e.jobsFor(42) != 1 || e.removalsOf(42) != 1 || !slices.Contains(e.labelsOf(42), "af:fix") {
		t.Errorf("jobs %d, removals %d, labels %v: want no job made, no removal sent and the label left where the cancel put it",
			e.jobsFor(42), e.removalsOf(42), e.labelsOf(42))
	}
}

// TestAJobCancelledInARunnerHoldsFromTheCancelWhateverTheRunnerReports_TS_15_45 verifies TS-15-45: whether the runner
// returns reporting the label as set, overruns the cancel grace so that its report is dropped, or is cancelled by the
// unsubscribe of the workspace, the record holds from the cancel, no job is made for the issue and the label stays.
// Verifies: 15-REQ-7.1
func TestAJobCancelledInARunnerHoldsFromTheCancelWhateverTheRunnerReports_TS_15_45(t *testing.T) {
	for name, tc := range map[string]struct {
		step   fakerunner.Step
		cancel func(e *env) string
		after  func(e *env, id string)
	}{
		"the runner returns reporting the label as set": {
			step:   fakerunner.Block(cancelledResult()),
			cancel: (*env).cancelRunning,
			after:  func(e *env, id string) { e.runnerReturned(id) },
		},
		"the runner overruns the grace and its report is dropped": {
			step:   fakerunner.BlockIgnoringContext(cancelledResult()),
			cancel: (*env).cancelRunning,
			after: func(e *env, id string) {
				e.Clock.Advance(61 * time.Second)
				waitFor(t, "the attempt to be recorded cancelled", func() bool {
					doc, err := e.Jobs.Get(ctx, id)
					return err == nil && doc.Attempts[0].State == jobs.AttemptCancelled
				})
				e.Runner.ReleaseAll()
				e.runnerReturned(id)
			},
		},
		"the workspace is unsubscribed": {
			step: fakerunner.Block(cancelledResult()),
			cancel: func(e *env) string {
				id := e.jobList()[0].ID
				if err := e.Jobs.Unsubscribe(ctx, slug, func() error { return nil }); err != nil {
					e.t.Fatal(err)
				}
				e.Hub.AddLabel(slug, 42, "af:fix")
				return id
			},
			after: func(e *env, id string) { e.runnerReturned(id) },
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, withRunner(fakerunner.New(tc.step)))
			e.takenByThePoll()
			id := tc.cancel(e)

			e.cycle() // in the window: the runner has not returned
			if _, ok := e.held(42); !ok {
				t.Fatalf("no hold on 42 while the runner is in progress: %+v", e.recordsOf())
			}
			tc.after(e, id)
			e.cycle()

			if _, ok := e.held(42); !ok {
				t.Errorf("no hold on 42 after the runner returned: %+v", e.recordsOf())
			}
			if e.jobsFor(42) != 1 || e.removalsOf(42) != 1 || !slices.Contains(e.labelsOf(42), "af:fix") {
				t.Errorf("jobs %d, removals %d, labels %v: want no job made, no removal beyond the first and the label left",
					e.jobsFor(42), e.removalsOf(42), e.labelsOf(42))
			}
		})
	}
}

// TestAHoldKeepsTheLabelAloneBeforeAndAfterTheRunnerReturnsAndAfterARestart_TS_15_46 verifies TS-15-46 and goal 4.
// Verifies: 15-REQ-7.2
func TestAHoldKeepsTheLabelAloneBeforeAndAfterTheRunnerReturnsAndAfterARestart_TS_15_46(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Block(cancelledResult()))))
	e.takenByThePoll()
	id := e.cancelRunning()
	check := func(when string) {
		t.Helper()
		if e.jobsFor(42) != 1 || e.removalsOf(42) != 1 || !slices.Contains(e.labelsOf(42), "af:fix") {
			t.Fatalf("%s: jobs %d, removals %d, labels %v; want the one job, the one removal that consumed the label, and the label in place",
				when, e.jobsFor(42), e.removalsOf(42), e.labelsOf(42))
		}
	}

	e.cycle()
	check("in the window before the runner returned")
	e.runnerReturned(id)
	for i := range 3 {
		e.cycle()
		check("after the runner returned, cycle " + string(rune('1'+i)))
		e.Clock.Advance(time.Minute)
	}

	restarted := New(e.Poll.cfg) // the poll starts again on the same database file
	for i := range 3 {
		restarted.Cycle(ctx, slug)
		check("after a restart, cycle " + string(rune('1'+i)))
		e.Clock.Advance(time.Minute)
	}
}

// TestTheReleaseClockStartsWhenNoRunnerOfTheJobIsInProgressOrWhenAPurgedJobIsFoundGone_TS_15_47 verifies TS-15-47.
// Verifies: 15-REQ-7.3
func TestTheReleaseClockStartsWhenNoRunnerOfTheJobIsInProgressOrWhenAPurgedJobIsFoundGone_TS_15_47(t *testing.T) {
	t.Run("the runner returns", func(t *testing.T) {
		e := newEnv(t, withRunner(fakerunner.New(fakerunner.BlockIgnoringContext(cancelledResult()))))
		e.takenByThePoll()
		id := e.cancelRunning()

		e.cycle()
		if rec, ok := e.held(42); !ok || !rec.ReleaseClock.IsZero() {
			t.Fatalf("while the runner is in progress the record is %+v, want a hold with no release clock", rec)
		}
		e.Clock.Advance(3 * time.Minute)
		e.cycle()
		if rec, _ := e.held(42); !rec.ReleaseClock.IsZero() {
			t.Fatalf("the clock started at %v with the runner still in progress", rec.ReleaseClock)
		}

		e.Runner.ReleaseAll()
		e.runnerReturned(id)
		e.Clock.Advance(time.Minute)
		first := e.Clock.Now()
		e.cycle()
		e.Clock.Advance(time.Minute)
		e.cycle()
		if rec, _ := e.held(42); !rec.ReleaseClock.Equal(first) {
			t.Errorf("the release clock is %v, want %v: the first cycle that saw no runner in progress", rec.ReleaseClock, first)
		}
	})

	t.Run("a purged job is found gone", func(t *testing.T) {
		e := newEnv(t, withRunner(fakerunner.New(fakerunner.Block(cancelledResult()))), withJobsConfig(func(c *jobs.Config) { c.Retain = time.Hour }))
		e.takenByThePoll()
		id := e.cancelRunning()
		e.runnerReturned(id)
		e.Clock.Advance(2 * time.Hour)
		waitFor(t, "retention to purge the job", func() bool { _, err := e.Jobs.Standing(ctx, id); return err != nil })
		e.Clock.Advance(7 * time.Minute)
		found := e.Clock.Now()

		e.cycle()

		if rec, ok := e.held(42); !ok || !rec.ReleaseClock.Equal(found) {
			t.Errorf("the record is %+v, want a hold whose clock is the time the job was found gone: %v", rec, found)
		}
	})
}

// TestACompleteListingWithoutTheIssueRequested5MinutesAfterTheClockReleasesTheHold_TS_15_48 verifies TS-15-48 and the end
// of goal 4: a person removes the label, and a complete listing without 42 that was requested 5 minutes after the clock
// started releases the hold; adding the label again makes exactly one new job.
// Verifies: 15-REQ-7.4
func TestACompleteListingWithoutTheIssueRequested5MinutesAfterTheClockReleasesTheHold_TS_15_48(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Block(cancelledResult())).Then(fakerunner.Block(fakerunner.Step{}))))
	e.takenByThePoll()
	id := e.cancelRunning()
	e.runnerReturned(id)
	e.cycle() // the clock starts
	if rec, ok := e.held(42); !ok || rec.ReleaseClock.IsZero() {
		t.Fatalf("the record is %+v, want a hold with its clock started", rec)
	}

	e.Hub.RemoveLabel(slug, 42, "af:fix") // a person takes the label off
	e.Clock.Advance(5 * time.Minute)
	e.cycle()

	if _, ok := e.held(42); ok {
		t.Errorf("the hold is still in place: %+v", e.recordsOf())
	}
	e.Hub.AddLabel(slug, 42, "af:fix") // and asks again
	e.cycle()
	if e.jobsFor(42) != 2 {
		t.Errorf("%d jobs for 42, want exactly one new job after the hold was released", e.jobsFor(42))
	}
	e.cycle()
	if e.jobsFor(42) != 2 {
		t.Errorf("%d jobs for 42 after another cycle, want still 2", e.jobsFor(42))
	}
}

// TestAHoldIsKeptByAListingRequestedTooSoonOrIncomplete_TS_15_49 verifies TS-15-49 and the second half of goal 4: with the
// forge leaving a newly added label out of its lists for two lists and an interval of 1m, none of the three cycles after
// the runner returned makes a job or removes the label; a complete listing without the issue that was requested less than
// 5 minutes after the clock started does not release the hold.
// Verifies: 15-REQ-7.5
func TestAHoldIsKeptByAListingRequestedTooSoonOrIncomplete_TS_15_49(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.BlockIgnoringContext(cancelledResult()))), withVars(map[string]string{"AF_POLL_INTERVAL": "1m"}))
	e.takenByThePoll()
	id := e.jobList()[0].ID
	if _, err := e.Jobs.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	e.Hub.SetAdditionLag(slug, 2) // the label put back is left out of the next two lists
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.Runner.ReleaseAll()
	e.runnerReturned(id)

	for i := range 3 {
		before := len(e.Hub.ListRequests(slug))
		e.cycle()
		if len(e.Hub.ListRequests(slug)) != before+2 {
			t.Fatalf("cycle %d did not list", i+1)
		}
		if _, ok := e.held(42); !ok {
			t.Fatalf("cycle %d: the hold was released: %+v", i+1, e.recordsOf())
		}
		if e.jobsFor(42) != 1 || e.removalsOf(42) != 1 {
			t.Fatalf("cycle %d: jobs %d, removals %d; want no job made and no removal beyond the first", i+1, e.jobsFor(42), e.removalsOf(42))
		}
		e.Clock.Advance(time.Minute)
	}
	if !slices.Contains(e.labelsOf(42), "af:fix") {
		t.Error("the label is gone")
	}
}

// TestAnIncompleteListingReleasesNoHoldAndSettlesNoOwedRemoval_TS_15_25 verifies TS-15-25.
// Verifies: 15-REQ-4.4
func TestAnIncompleteListingReleasesNoHoldAndSettlesNoOwedRemoval_TS_15_25(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Block(cancelledResult())).Then(fakerunner.Done())))
	e.takenByThePoll()
	id := e.cancelRunning()
	e.runnerReturned(id)
	e.cycle() // the clock of the hold starts
	e.Hub.RemoveLabel(slug, 42, "af:fix")
	e.record(Record{Issue: 43, Label: "af:fix", Owed: true, Seen: SeenEnded})
	e.Clock.Advance(10 * time.Minute) // the release delay has elapsed
	// A page of 100 that carries the label is a listing that may have been cut by hub, which cannot say so: it is incomplete.
	// They are pull requests, which the listing leaves out, so that no job is made of them.
	for n := 1000; n < 1100; n++ {
		e.Hub.AddIssue(slug, fakehub.Issue{Number: n, Labels: []string{"af:fix"}, IsPR: true})
	}

	e.cycle()

	if _, ok := e.held(42); !ok {
		t.Errorf("an incomplete listing released the hold: %+v", e.recordsOf())
	}
	if owed := slices.ContainsFunc(e.recordsOf(), func(r Record) bool { return r.Issue == 43 && r.Owed }); !owed {
		t.Errorf("an incomplete listing settled the owed removal of 43: %+v", e.recordsOf())
	}

	for n := 1000; n < 1100; n++ { // a later listing is complete
		e.Hub.RemoveLabel(slug, n, "af:fix")
	}
	e.cycle()
	if _, ok := e.held(42); ok {
		t.Errorf("a complete listing did not release the hold: %d records", len(e.recordsOf()))
	}
	if slices.ContainsFunc(e.recordsOf(), func(r Record) bool { return r.Issue == 43 }) {
		t.Errorf("a complete listing did not settle the owed removal of 43: %+v", e.recordsOf())
	}
}

// TestAJobCancelledWhileQueuedInBackoffOrBlockedHoldsNothing_TS_15_50 verifies TS-15-50.
// Verifies: 15-REQ-7.6
func TestAJobCancelledWhileQueuedInBackoffOrBlockedHoldsNothing_TS_15_50(t *testing.T) {
	sandbox := fakerunner.TransientFailure(&toolio.Failure{Category: toolio.CategorySandbox, Message: "the box is gone", Retryable: true})
	for name, tc := range map[string]struct {
		steps []fakerunner.Step
	}{
		"while queued":                {steps: []fakerunner.Step{fakerunner.Block(fakerunner.Step{})}},
		"while waiting out a backoff": {steps: []fakerunner.Step{sandbox}},
		"while blocked":               {steps: []fakerunner.Step{needsHuman}},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, withRunner(fakerunner.New(tc.steps...).Then(fakerunner.Done())))
			if name == "while queued" {
				e.startedJob(jobs.KindFix, 41, "af:fix") // runs first, and holds the workspace
				waitFor(t, "the first job to run", func() bool { return len(e.Runner.Calls()) == 1 })
			}
			e.issue(42, "af:fix")
			e.cycle()
			var id string
			for _, j := range e.jobList() {
				if j.Issue.Int64 == 42 {
					id = j.ID
				}
			}
			switch name {
			case "while waiting out a backoff":
				if !e.Clock.WaitForWaits(1, 10*time.Second) {
					t.Fatal("the job did not go into its backoff")
				}
			case "while blocked":
				e.waitState(id, jobs.StateBlocked)
			}
			if _, err := e.Jobs.Cancel(ctx, id); err != nil {
				t.Fatal(err)
			}
			e.Hub.AddLabel(slug, 42, "af:fix") // a person asks again

			e.cycle()

			if _, ok := e.held(42); ok {
				t.Errorf("the record holds: %+v", e.recordsOf())
			}
			if e.jobsFor(42) != 2 {
				t.Errorf("%d jobs for 42, want one new job at the next cycle", e.jobsFor(42))
			}
		})
	}
}

// TestAHoldSurvivesRetentionPurgingTheCancelledJob_TS_15_51 verifies TS-15-51: whether the poll saw the hold before the
// purge or found the job gone after it, the three cycles that follow make no job for the issue and leave the label.
// Verifies: 15-REQ-7.7
func TestAHoldSurvivesRetentionPurgingTheCancelledJob_TS_15_51(t *testing.T) {
	for name, sawBefore := range map[string]bool{"the poll saw the hold before the purge": true, "the poll finds the job gone": false} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, withRunner(fakerunner.New(fakerunner.Block(cancelledResult()))), withJobsConfig(func(c *jobs.Config) { c.Retain = time.Hour }))
			e.takenByThePoll()
			id := e.cancelRunning()
			e.runnerReturned(id)
			if sawBefore {
				e.cycle()
			}
			e.Clock.Advance(2 * time.Hour)
			waitFor(t, "retention to purge the job", func() bool { _, err := e.Jobs.Standing(ctx, id); return err != nil })

			for i := range 3 {
				e.cycle()
				if e.jobsFor(42) != 0 {
					t.Fatalf("cycle %d made a job for 42", i+1)
				}
				e.Clock.Advance(time.Minute)
			}

			if e.removalsOf(42) != 1 || !slices.Contains(e.labelsOf(42), "af:fix") {
				t.Errorf("removals %d, labels %v: want the label left in place and no removal beyond the first", e.removalsOf(42), e.labelsOf(42))
			}
			if _, ok := e.held(42); !ok {
				t.Errorf("the hold is gone: %+v", e.recordsOf())
			}
		})
	}
}

// blockedByThePoll runs a cycle that makes the fix job for issue 42 from af:fix, waits for it to be blocked with
// needs-human set on the issue as the worker sets it, and returns its id.
func (e *env) blockedByThePoll() string {
	e.t.Helper()
	e.issue(42, "af:fix")
	e.cycle()
	id := e.jobList()[0].ID
	e.waitState(id, jobs.StateBlocked)
	e.Hub.AddLabel(slug, 42, "af:needs-human")
	return id
}

// blockedStep ends an attempt blocked with needs-human set, as the worker reports it.
func blockedStep() fakerunner.Step {
	return fakerunner.Return(jobs.Result{
		Envelope:  fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which?", "a")),
		LabelsSet: []string{"af:needs-human"},
	})
}

// cleanupOf is the job's labels.cleanup, "" when it is null.
func (e *env) cleanupOf(id string) string {
	e.t.Helper()
	d, err := e.Jobs.Get(ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	if d.Labels == nil || d.Labels.Cleanup == nil {
		return ""
	}
	return *d.Labels.Cleanup
}

// sweepUntil advances the clock a minute at a time, so that the jobs layer's sweep tries the pending cleanup again, until
// the job's cleanup is the state: a sweep starts no try for a job whose try is still in flight.
func (e *env) sweepUntil(id, state string) {
	e.t.Helper()
	for range 30 {
		e.Clock.Advance(time.Minute)
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if e.cleanupOf(id) == state {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	e.t.Fatalf("the cleanup of %s is %q, want %q", id, e.cleanupOf(id), state)
}

// TestThePollHoldsALabelThatTheCleanupPutBack_TS_17_58 verifies TS-17-58.
// Verifies: 17-REQ-10.1
func TestThePollHoldsALabelThatTheCleanupPutBack_TS_17_58(t *testing.T) {
	e := newEnv(t, withCleanup(), withRunner(fakerunner.New(blockedStep())))
	id := e.blockedByThePoll()
	if _, err := e.Jobs.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the cleanup", func() bool { return e.cleanupOf(id) == "done" })
	if got := e.labelsOf(42); !slices.Equal(got, []string{"af:fix"}) {
		t.Fatalf("issue 42 carries %v, want af:fix put back", got)
	}
	removals := e.removalsOf(42)
	for i := range 3 {
		e.cycle()
		if e.jobsFor(42) != 1 || e.removalsOf(42) != removals {
			t.Fatalf("cycle %d: %d jobs for 42 and %d removals, want no new job and no removal", i+1, e.jobsFor(42), e.removalsOf(42)-removals)
		}
		e.Clock.Advance(time.Minute)
	}
	if _, ok := e.held(42); !ok {
		t.Errorf("the record does not hold: %+v", e.recordsOf())
	}
}

// TestACancelThatReachedARunnerHoldsWhateverTheRunnerReports_TS_17_59 verifies TS-17-59: with the cleanup in the layer, a
// runner that reported a hub session and one that did not both leave a hold.
// Verifies: 17-REQ-10.2
func TestACancelThatReachedARunnerHoldsWhateverTheRunnerReports_TS_17_59(t *testing.T) {
	for _, session := range []bool{true, false} {
		t.Run(fmt.Sprintf("hub session reported: %v", session), func(t *testing.T) {
			step := fakerunner.Do(func(ctx context.Context, req jobs.Request) (jobs.Result, error) {
				if session {
					req.Recorder.Hub("ses_1", "run_1", "")
				}
				<-ctx.Done()
				return jobs.Result{}, ctx.Err()
			})
			e := newEnv(t, withCleanup(), withRunner(fakerunner.New(step)))
			e.takenByThePoll()
			id := e.jobList()[0].ID
			if _, err := e.Jobs.Cancel(ctx, id); err != nil {
				t.Fatal(err)
			}
			if session {
				e.Hub.AddLabel(slug, 42, "af:fix") // the worker's own cleanup
			} else {
				waitFor(t, "the cleanup", func() bool { return e.cleanupOf(id) == "done" })
			}
			e.runnerReturned(id)
			for range 2 {
				e.cycle()
			}
			if _, ok := e.held(42); !ok {
				t.Errorf("the record does not hold: %+v", e.recordsOf())
			}
			if e.jobsFor(42) != 1 {
				t.Errorf("%d jobs for 42, want no new job", e.jobsFor(42))
			}
		})
	}
}

// TestACancelThatReachedNoRunnerHoldsNothingWithoutACleanupOrWithTheLabelNotBack_TS_17_60 verifies TS-17-60.
// Verifies: 17-REQ-10.3
func TestACancelThatReachedNoRunnerHoldsNothingWithoutACleanupOrWithTheLabelNotBack_TS_17_60(t *testing.T) {
	t.Run("no cleanup seam", func(t *testing.T) {
		e := newEnv(t, withRunner(fakerunner.New(blockedStep())))
		id := e.blockedByThePoll()
		if _, err := e.Jobs.Cancel(ctx, id); err != nil {
			t.Fatal(err)
		}
		e.cycle()
		if seen := e.seenOf()[42]; seen.Seen != SeenEnded && len(e.recordsOf()) != 0 {
			t.Errorf("the record is %+v, want it ended, not holding", seen)
		}
		if _, ok := e.held(42); ok {
			t.Error("the record holds")
		}
	})
	t.Run("the cleanup ended with the label not back", func(t *testing.T) {
		e := newEnv(t, withCleanup(), withRunner(fakerunner.New(blockedStep())))
		id := e.blockedByThePoll()
		e.Hub.Inject("POST", "/api/v1/workspaces/widgets/issues/42/labels", fakehub.Fault{Status: 400})
		if _, err := e.Jobs.Cancel(ctx, id); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the cleanup to end", func() bool { return e.cleanupOf(id) == "label_not_set" })
		e.cycle()
		if _, ok := e.held(42); ok {
			t.Errorf("the record holds: %+v", e.recordsOf())
		}
	})
	for name, end := range map[string]func(e *env){
		"a hold while pending stops when the cleanup ends with the label not back": func(e *env) {
			e.Hub.Clear()
			e.Hub.Inject("POST", "/api/v1/workspaces/widgets/issues/42/labels", fakehub.Fault{Status: 400})
		},
		"a cleanup abandoned before the label is back": func(e *env) {
			e.Clock.Advance(61 * time.Minute)
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, withCleanup(), withRunner(fakerunner.New(blockedStep())))
			id := e.blockedByThePoll()
			e.Hub.Fail("/api/v1/workspaces/widgets/issues/42/labels", 503)
			if _, err := e.Jobs.Cancel(ctx, id); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "the first try", func() bool { return len(e.Hub.RequestsTo("POST", "/api/v1/workspaces/widgets/issues/42/labels")) == 1 })
			e.cycle()
			if _, ok := e.held(42); !ok {
				t.Fatalf("while the cleanup is pending the record is %+v, want a hold", e.recordsOf())
			}
			end(e)
			e.sweepUntil(id, "label_not_set")
			e.cycle()
			if _, ok := e.held(42); ok {
				t.Errorf("the record still holds: %+v", e.recordsOf())
			}
		})
	}
}

// TestTheReleaseClockStartsOnlyWhenNoRunnerRunsAndNoCleanupIsPending_TS_17_61 verifies TS-17-61.
// Verifies: 17-REQ-10.4
func TestTheReleaseClockStartsOnlyWhenNoRunnerRunsAndNoCleanupIsPending_TS_17_61(t *testing.T) {
	e := newEnv(t, withCleanup(), withRunner(fakerunner.New(blockedStep())))
	id := e.blockedByThePoll()
	e.Hub.Fail("/api/v1/workspaces/widgets/issues/42/labels", 503)
	if _, err := e.Jobs.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		e.cycle()
		if rec, ok := e.held(42); !ok || !rec.ReleaseClock.IsZero() {
			t.Fatalf("while the cleanup is pending the record is %+v, want a hold with no release clock", rec)
		}
		e.Clock.Advance(30 * time.Second)
	}
	e.Hub.Clear()
	e.sweepUntil(id, "done")
	first := e.Clock.Now()
	e.cycle()
	e.Clock.Advance(time.Minute)
	e.cycle()
	if rec, _ := e.held(42); !rec.ReleaseClock.Equal(first) {
		t.Errorf("the release clock is %v, want %v: the first cycle that saw no cleanup pending", rec.ReleaseClock, first)
	}
}

// cleanedUpAndHeld cancels the blocked job of issue 42 through the cause, waits for its cleanup to put af:fix back and runs
// the cycle that starts the release clock, and returns when that was.
func (e *env) cleanedUpAndHeld(cancel func(id string)) time.Time {
	e.t.Helper()
	id := e.blockedByThePoll()
	cancel(id)
	waitFor(e.t, "the cleanup", func() bool { return e.cleanupOf(id) == "done" })
	started := e.Clock.Now()
	e.cycle()
	if rec, ok := e.held(42); !ok || !rec.ReleaseClock.Equal(started) {
		e.t.Fatalf("the record is %+v, want a hold whose clock started at %v", rec, started)
	}
	return started
}

// releasedAfterTheDelay checks that a complete listing without the issue releases the hold only once it was requested the
// release delay after the clock started, and that the label added again then makes exactly one new job.
func (e *env) releasedAfterTheDelay(started time.Time) {
	e.t.Helper()
	e.Hub.RemoveLabel(slug, 42, "af:fix") // a person takes the label off
	e.Clock.Advance(started.Add(4 * time.Minute).Sub(e.Clock.Now()))
	e.cycle()
	if _, ok := e.held(42); !ok {
		e.t.Fatalf("a listing 4 minutes after the clock released the hold")
	}
	e.Clock.Advance(started.Add(5 * time.Minute).Sub(e.Clock.Now()))
	e.cycle()
	if _, ok := e.held(42); ok {
		e.t.Fatalf("a listing 5 minutes after the clock kept the hold: %+v", e.recordsOf())
	}
	jobsBefore := e.jobsFor(42)
	e.Hub.AddLabel(slug, 42, "af:fix") // and asks again
	e.cycle()
	e.cycle()
	if e.jobsFor(42) != jobsBefore+1 {
		e.t.Errorf("%d new jobs for 42, want exactly one", e.jobsFor(42)-jobsBefore)
	}
}

// TestACompleteListingWithoutTheIssueReleasesTheHoldAfterTheReleaseDelay_TS_17_62 verifies TS-17-62.
// Verifies: 17-REQ-10.5
func TestACompleteListingWithoutTheIssueReleasesTheHoldAfterTheReleaseDelay_TS_17_62(t *testing.T) {
	e := newEnv(t, withCleanup(), withRunner(fakerunner.New(blockedStep()).Then(fakerunner.Block(fakerunner.Step{}))))
	started := e.cleanedUpAndHeld(func(id string) {
		if _, err := e.Jobs.Cancel(ctx, id); err != nil {
			t.Fatal(err)
		}
	})
	e.releasedAfterTheDelay(started)
}

// TestTheHoldAndReleaseRulesAreTheSameForAPersonsCancelAndAnUnsubscribe_TS_17_63 verifies TS-17-63.
// Verifies: 17-REQ-10.6
func TestTheHoldAndReleaseRulesAreTheSameForAPersonsCancelAndAnUnsubscribe_TS_17_63(t *testing.T) {
	for _, how := range []string{"a person", "an unsubscribe"} {
		t.Run(how, func(t *testing.T) {
			e := newEnv(t, withCleanup(), withRunner(fakerunner.New(blockedStep()).Then(fakerunner.Block(fakerunner.Step{}))))
			started := e.cleanedUpAndHeld(func(id string) {
				if how == "a person" {
					if _, err := e.Jobs.Cancel(ctx, id); err != nil {
						t.Fatal(err)
					}
					return
				}
				err := e.Jobs.Unsubscribe(ctx, slug, func() error { _, err := e.Store.Delete(ctx, slug); return err })
				if err != nil {
					t.Fatal(err)
				}
				// The workspace is subscribed again, and its poll goes on with the records it kept.
				if _, _, err := e.Store.Upsert(ctx, slug, "af:", pat, "usr_1", "usr_1", e.Clock.Now()); err != nil {
					t.Fatal(err)
				}
			})
			for range 2 {
				e.cycle()
				if e.jobsFor(42) != 1 {
					t.Fatalf("%d jobs for 42 while it is held", e.jobsFor(42))
				}
			}
			e.releasedAfterTheDelay(started)
		})
	}
}

// ordered is a jobs layer that notes each Create by its issue, and holds the Create of the issue until it is released.
type ordered struct {
	Jobs
	mu      sync.Mutex
	order   []string
	hold    int
	reached chan struct{}
	release chan struct{}
}

func (o *ordered) note(s string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.order = append(o.order, s)
}

func (o *ordered) Create(ctx context.Context, req jobs.CreateRequest) (jobs.Doc, error) {
	n := strings.TrimSpace(string(req.Body.Issue))
	o.note("create " + n)
	if n == strconv.Itoa(o.hold) {
		o.reached <- struct{}{}
		<-o.release
	}
	return o.Jobs.Create(ctx, req)
}

// TestTheRulesOfOneIssueTakeTheWorkspacesLockAndNotTheCycles_TS_18_49 verifies TS-18-49: something that takes the lock of
// the workspace while a cycle is in the rules of its first issue runs once they are done, and before the second issue's.
// Verifies: 18-REQ-7.5
func TestTheRulesOfOneIssueTakeTheWorkspacesLockAndNotTheCycles_TS_18_49(t *testing.T) {
	o := &ordered{hold: 41, reached: make(chan struct{}, 1), release: make(chan struct{})}
	e := newEnv(t, withRunner(fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))), withJobs(func(j Jobs) Jobs { o.Jobs = j; return o }))
	e.issue(41, "af:fix")
	e.issue(42, "af:fix")
	done := make(chan struct{})
	go func() { defer close(done); e.cycle() }()
	<-o.reached // the cycle is in the rules of 41
	locked := make(chan struct{})
	go func() {
		unlock := e.Poll.lockWorkspace(slug)
		o.note("event")
		time.Sleep(50 * time.Millisecond) // the cycle waits for the lock before the rules of 42
		unlock()
		close(locked)
	}()
	time.Sleep(50 * time.Millisecond)
	o.mu.Lock()
	early := slices.Contains(o.order, "event")
	o.mu.Unlock()
	if early {
		t.Error("the lock was taken while the cycle was in the rules of an issue")
	}
	close(o.release)
	<-locked
	<-done
	o.mu.Lock()
	defer o.mu.Unlock()
	if want := []string{"create 41", "event", "create 42"}; !slices.Equal(o.order, want) {
		t.Errorf("the order is %v, want %v", o.order, want)
	}
}

// TestACycleMakesNoJobForALabelWhoseRecordIsNewerThanItsListing_TS_18_50 verifies TS-18-50.
// Verifies: 18-REQ-7.6
func TestACycleMakesNoJobForALabelWhoseRecordIsNewerThanItsListing_TS_18_50(t *testing.T) {
	var e *env
	inserted := false
	e = newEnv(t, withRunner(fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))), withBeforeRequest(func(r *http.Request) {
		if !inserted && strings.Contains(r.URL.RawQuery, "labels=af%3Afix") {
			inserted = true
			// A job the stream made for the label after the listing was asked for, which has ended since.
			e.record(Record{Issue: 42, Label: "af:fix", JobID: "job_stream", Seen: SeenEnded, CreatedAt: e.Clock.Now()})
		}
	}))
	e.issue(42, "af:fix")
	e.cycle()
	if n := e.jobsFor(42); n != 0 {
		t.Fatalf("%d jobs for 42, want none from a listing older than the record", n)
	}
	if !slices.Contains(e.labelsOf(42), "af:fix") {
		t.Error("the label was taken off")
	}
	e.Clock.Advance(time.Minute)
	e.cycle()
	if n := e.jobsFor(42); n != 1 {
		t.Errorf("the next cycle made %d jobs for 42, want 1", n)
	}
}

// TestACompleteListingSettlesNothingForARecordNewerThanIt_TS_18_51 verifies TS-18-51.
// Verifies: 18-REQ-7.7
func TestACompleteListingSettlesNothingForARecordNewerThanIt_TS_18_51(t *testing.T) {
	var e *env
	var newOwed, newHeld Record
	inserted := false
	e = newEnv(t, withBeforeRequest(func(r *http.Request) {
		if !inserted && strings.Contains(r.URL.RawQuery, "labels=af%3Afix") {
			inserted = true
			newOwed = e.record(Record{Issue: 51, Label: "af:fix", Owed: true, Seen: SeenEnded, CreatedAt: e.Clock.Now()})
			newHeld = e.record(Record{Issue: 52, Label: "af:fix", JobID: "job_new", Seen: SeenHolding, ReleaseClock: e.Clock.Now().Add(-time.Hour), CreatedAt: e.Clock.Now()})
		}
	}))
	old := e.Clock.Now().Add(-time.Hour)
	oldOwed := e.record(Record{Issue: 53, Label: "af:fix", Owed: true, Seen: SeenEnded, CreatedAt: old})
	oldHeld := e.record(Record{Issue: 54, Label: "af:fix", JobID: "job_old", Seen: SeenHolding, ReleaseClock: old, CreatedAt: old})
	e.cycle() // complete listings, none of which holds 51 to 54
	recs := map[int64]Record{}
	for _, r := range e.recordsOf() {
		recs[r.ID] = r
	}
	if r, ok := recs[newOwed.ID]; !ok || !r.Owed {
		t.Errorf("the newer owed record is %+v (kept %v), want it still owed", r, ok)
	}
	if r, ok := recs[newHeld.ID]; !ok || r.Seen != SeenHolding {
		t.Errorf("the newer hold is %+v (kept %v), want it still holding", r, ok)
	}
	if r, ok := recs[oldOwed.ID]; ok && r.Owed {
		t.Errorf("the older owed record is %+v, want it settled", r)
	}
	if r, ok := recs[oldHeld.ID]; ok && r.Seen == SeenHolding {
		t.Errorf("the older hold is %+v, want it released", r)
	}
}

// TestAnIssueClosedJobIsEndedAndHoldsNothing_TS_18_65 verifies TS-18-65.
// Verifies: 18-REQ-9.6
func TestAnIssueClosedJobIsEndedAndHoldsNothing_TS_18_65(t *testing.T) {
	r := fakerunner.New(fakerunner.Block(cancelledResult()), blockedStep(), fakerunner.Block(cancelledResult()))
	e := newEnv(t, withCleanup(), withRunner(r))
	e.takenByThePoll() // issue 42, running
	inRunner := e.jobList()[0].ID
	e.issue(43, "af:fix")
	e.Hub.Inject("DELETE", "/api/v1/workspaces/widgets/issues/43/labels/af:needs-human", fakehub.Fault{Status: 503})
	if _, err := e.Jobs.CancelIssue(ctx, slug, 42); err != nil {
		t.Fatal(err)
	}
	e.runnerReturned(inRunner)
	e.cycle() // makes the job of 43, which blocks
	var blocked string
	for _, j := range e.jobList() {
		if j.Issue.Int64 == 43 {
			blocked = j.ID
		}
	}
	e.waitState(blocked, jobs.StateBlocked)
	e.Hub.AddLabel(slug, 43, "af:needs-human")
	if _, err := e.Jobs.CancelIssue(ctx, slug, 43); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the cleanup's first try", func() bool {
		return len(e.Hub.RequestsTo("DELETE", "/api/v1/workspaces/widgets/issues/43/labels/af:needs-human")) > 0
	})
	if c := e.cleanupOf(blocked); c != "pending" {
		t.Fatalf("the cleanup of 43 is %q, want pending", c)
	}
	e.cycle()
	for _, n := range []int{42, 43} {
		if _, ok := e.held(n); ok {
			t.Errorf("the record of %d holds: %+v", n, e.recordsOf())
		}
	}

	// A person's cancel of a job in a runner still holds.
	e.issue(44, "af:fix")
	e.cycle()
	var person string
	for _, j := range e.jobList() {
		if j.Issue.Int64 == 44 {
			person = j.ID
		}
	}
	e.waitState(person, jobs.StateRunning)
	if !e.Runner.Wait(3, 10*time.Second) {
		t.Fatal("the runner of 44 was not called")
	}
	if _, err := e.Jobs.Cancel(ctx, person); err != nil {
		t.Fatal(err)
	}
	e.cycle()
	if _, ok := e.held(44); !ok {
		t.Errorf("a person's cancel in a runner does not hold: %+v", e.recordsOf())
	}
}
