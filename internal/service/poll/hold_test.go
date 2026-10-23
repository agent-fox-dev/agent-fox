package poll

import (
	"fmt"
	"slices"
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
