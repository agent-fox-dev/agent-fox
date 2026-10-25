package jobs_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// needsHuman ends an attempt blocked, with the status label it set.
func needsHuman() fakerunner.Step {
	return fakerunner.Return(jobs.Result{
		Envelope:  fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which?", "a")),
		LabelsSet: []string{"af:needs-human"}, LabelsRemoved: []string{"af:in-progress"},
	})
}

// pendingWith is a result that leaves the cleanup pending, as a hub that is unavailable does.
func pendingWith(message string) func(jobs.CleanupRequest) jobs.CleanupResult {
	return func(jobs.CleanupRequest) jobs.CleanupResult {
		return jobs.CleanupResult{Outcome: jobs.CleanupPending, Level: slog.LevelWarn, Message: message}
	}
}

// cleanupWarnings are the WARN records of the cancel cleanup of the job.
func (e *env) cleanupWarnings(id string) int {
	n := 0
	for _, rec := range e.Log.Find("WARN", "cancel cleanup") {
		if rec["job"] == id {
			n++
		}
	}
	return n
}

// idle waits until no try of a cleanup is in flight.
func (e *env) idle() {
	e.t.Helper()
	testutil.Eventually(e.t, "the tries to end", func() bool { return e.Svc.CleanupsInFlight() == 0 })
}

func TestCleanup_ACancelOfAJobThatWaitedIsCleanedUpInTheBackground(t *testing.T) {
	c := &fakeCleaner{hold: make(chan struct{})}
	e := newEnv(t, withRunner(fakerunner.New(needsHuman())), withCleaner(c))
	e.start()
	job := e.waitState(e.create("widgets", 42, "af:fix").ID, jobs.StateBlocked)
	doc, err := e.Svc.Cancel(ctx, job.ID)
	if err != nil || doc.Labels.Cleanup == nil || *doc.Labels.Cleanup != "pending" || *doc.Labels.Restore != "af:fix" {
		t.Fatalf("the cancel's answer: %+v, %v, want the cleanup pending and af:fix to be put back", doc.Labels, err)
	}
	testutil.Eventually(t, "the try", func() bool { return len(c.Tries()) == 1 })
	req := c.Tries()[0]
	if req.Workspace != "widgets" || req.JobID != job.ID || req.Issue != 42 || req.RequestLabel != "af:fix" || !slices.Equal(req.LabelsSet, []string{"af:needs-human"}) {
		t.Errorf("the try is about %+v", req)
	}
	close(c.hold)
	testutil.Eventually(t, "the cleanup to be done", func() bool { return e.cleanup(job.ID) == "done" })
	e.idle()
	d := e.get(job.ID)
	if d.Labels.Restore != nil || *d.Labels.Cleanup != "done" {
		t.Errorf("labels = %+v, want the request label put back and the cleanup done", d.Labels)
	}
	if done := e.Log.Find("INFO", "back in line"); len(done) != 1 || done[0]["job"] != job.ID || done[0]["issue"] != float64(42) {
		t.Errorf("INFO records = %v, want one naming the job and the issue", e.Log.Find("INFO", ""))
	}
	if st, _ := e.Svc.Standing(ctx, job.ID); st.CleanupPending || !st.CleanupPutBack || st.CancelCause != "cancelled" {
		t.Errorf("standing = %+v, want the cleanup over with the label put back", st)
	}
}

func TestCleanup_ATryIsToldWhatTheOtherOpenJobsOfTheIssueShow(t *testing.T) {
	c := &fakeCleaner{}
	e := newEnv(t, withCleaner(c), withoutEnqueue())
	job := e.create("widgets", 42, "af:fix")
	other, err := e.Svc.Create(ctx, jobs.CreateRequest{Workspace: "widgets", Kind: jobs.KindTriage, Body: jobs.Body{Issue: []byte("42")}, Source: jobs.Source{Kind: "api", By: "u"}})
	if err != nil {
		t.Fatal(err)
	}
	testutil.Exec(t, e.DB, `UPDATE af_jobs SET state = 'running', labels_set = '["af:triaging"]' WHERE id = ?`, other.ID)
	if _, err := e.Svc.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	testutil.Eventually(t, "the try", func() bool { return len(c.Tries()) == 1 })
	if req := c.Tries()[0]; !slices.Equal(req.OthersSet, []string{"af:triaging"}) || !req.OtherRunning {
		t.Errorf("the try is told others set %v, running %v, want the triage's", req.OthersSet, req.OtherRunning)
	}
}

func TestCleanup_NoneIsOwedWithoutACleanerAnIssueOrAPersonsCancel(t *testing.T) {
	c := &fakeCleaner{}
	e := newEnv(t, withCleaner(c), withoutEnqueue())
	text, err := e.Svc.Create(ctx, jobs.CreateRequest{Workspace: "widgets", Kind: jobs.KindTriage, Body: jobs.Body{Text: ptr("red")}, Source: jobs.Source{Kind: "api", By: "u"}})
	if err != nil {
		t.Fatal(err)
	}
	closed := e.create("widgets", 42, "af:fix")
	if d, err := e.Svc.Cancel(ctx, text.ID); err != nil || d.Labels != nil {
		t.Errorf("a text triage: labels %+v, %v, want none", d.Labels, err)
	}
	if err := e.Svc.CancelFor(ctx, closed.ID, errors.New("the issue was closed")); err != nil {
		t.Fatal(err)
	}
	without := newEnv(t, withoutEnqueue())
	queued := without.create("widgets", 7, "af:fix")
	if d, err := without.Svc.Cancel(ctx, queued.ID); err != nil || d.Labels.Cleanup != nil || *d.Labels.Restore != "af:fix" {
		t.Errorf("a layer without a cleaner: labels %+v, %v, want the label to be put back and no cleanup", d.Labels, err)
	}
	if n := testutil.Count(t, e.DB, "af_jobs", "cleanup IS NOT NULL OR cleanup_wanted = 1") + testutil.Count(t, without.DB, "af_jobs", "cleanup IS NOT NULL"); n != 0 {
		t.Errorf("%d jobs have a cleanup marked, want none", n)
	}
	e.Svc.TryCleanup(closed.ID) // a try of a cleanup that is not pending writes nothing
	e.idle()
	if len(c.Tries()) != 0 {
		t.Errorf("%d tries, want none", len(c.Tries()))
	}
}

func TestCleanup_OnlyOneTryOfAJobIsInFlightAndAtMostFourInAll(t *testing.T) {
	c := &fakeCleaner{hold: make(chan struct{}), result: pendingWith("hub is unavailable")}
	e := newEnv(t, withCleaner(c), withoutEnqueue())
	var ids []string
	for n := range 7 {
		job := e.create("widgets", 50+n, "af:fix")
		if _, err := e.Svc.Cancel(ctx, job.ID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, job.ID)
	}
	testutil.Eventually(t, "the cap to fill", func() bool { return len(c.Tries()) == jobs.MaxCleanupTries })
	for range 3 {
		e.Svc.TryCleanup(ids[0])
	}
	if e.Svc.CleanupsInFlight() != 7 {
		t.Errorf("%d tries in flight, want one for each of the 7 jobs", e.Svc.CleanupsInFlight())
	}
	close(c.hold)
	e.idle()
	if len(c.Tries()) != 7 || c.Most() != jobs.MaxCleanupTries {
		t.Errorf("%d tries with %d at most at once, want 7 with %d", len(c.Tries()), c.Most(), jobs.MaxCleanupTries)
	}
	<-e.Svc.TryCleanup(ids[0]) // once the try has ended, another starts
	if len(c.Tries()) != 8 {
		t.Errorf("%d tries, want another once the first ended", len(c.Tries()))
	}
}

func TestCleanup_ATryThatRunsOutOfItsMinuteLeavesItPending(t *testing.T) {
	c := &fakeCleaner{hold: make(chan struct{})}
	e := newEnv(t, withCleaner(c), withoutEnqueue())
	job := e.create("widgets", 42, "af:fix")
	if _, err := e.Svc.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	testutil.Eventually(t, "the try", func() bool { return len(c.Tries()) == 1 })
	e.Clock.Advance(time.Minute)
	e.idle()
	if e.cleanup(job.ID) != "pending" || e.cleanupWarnings(job.ID) != 1 {
		t.Errorf("cleanup %s with %d warnings, want pending with one", e.cleanup(job.ID), e.cleanupWarnings(job.ID))
	}
}

func TestCleanup_APendingCleanupIsTriedAtEverySweepWithOneWarningUntilItIsDone(t *testing.T) {
	c := &fakeCleaner{result: pendingWith("hub is unavailable")}
	e := newEnv(t, withCleaner(c), withoutEnqueue())
	e.start()
	job := e.create("widgets", 42, "af:fix")
	if _, err := e.Svc.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	for tries := 1; tries <= 3; tries++ { // each sweep makes one try
		if tries > 1 {
			e.Clock.Advance(time.Minute)
		}
		testutil.Eventually(t, "a try", func() bool { return len(c.Tries()) == tries })
		e.idle()
	}
	if e.cleanup(job.ID) != "pending" || e.cleanupWarnings(job.ID) != 1 {
		t.Errorf("cleanup %s with %d warnings, want pending with the first failure's", e.cleanup(job.ID), e.cleanupWarnings(job.ID))
	}
	c.mu.Lock()
	c.result = nil
	c.mu.Unlock()
	e.Clock.Advance(time.Minute)
	testutil.Eventually(t, "the sweep to complete it", func() bool { return e.cleanup(job.ID) == "done" })
}

func TestCleanup_AnEndWithoutTheLabelsIsLoggedAtItsLevelWithWhatItWrote(t *testing.T) {
	c := &fakeCleaner{result: func(jobs.CleanupRequest) jobs.CleanupResult {
		return jobs.CleanupResult{Outcome: jobs.CleanupLabelNotSet, Set: []string{"af:x"}, Level: slog.LevelError, Message: "the forge refused"}
	}}
	e := newEnv(t, withCleaner(c), withoutEnqueue())
	job := e.create("widgets", 42, "af:fix")
	if _, err := e.Svc.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	testutil.Eventually(t, "the end", func() bool { return e.cleanup(job.ID) == "label_not_set" })
	e.idle()
	if logged := e.Log.Find("ERROR", "the forge refused"); len(logged) != 1 || logged[0]["job"] != job.ID {
		t.Errorf("ERROR records = %v", e.Log.Records())
	}
	if l := e.get(job.ID).Labels; !slices.Equal(l.Set, []string{"af:x"}) || len(l.Removed) != 0 || *l.Restore != "af:fix" {
		t.Errorf("labels = %+v, want what the try wrote, and the request label still to be put back", l)
	}
	if st, _ := e.Svc.Standing(ctx, job.ID); st.CleanupPending || st.CleanupPutBack {
		t.Errorf("standing = %+v, want the cleanup over without the label", st)
	}
}

func TestCleanup_WhatATryCameToThatCannotBeRecordedIsWarnedOfOnce(t *testing.T) {
	c := &fakeCleaner{}
	e := newEnv(t, withCleaner(c), withoutEnqueue())
	job := e.create("widgets", 42, "af:fix")
	testutil.Exec(t, e.DB, "CREATE TRIGGER no_record BEFORE UPDATE OF cleanup_warned ON af_jobs BEGIN SELECT RAISE(ABORT, 'refused'); END")
	if _, err := e.Svc.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		testutil.Eventually(t, "a try", func() bool { return e.Svc.CleanupsInFlight() == 0 && len(c.Tries()) > 0 })
		<-e.Svc.TryCleanup(job.ID)
	}
	if n := len(e.Log.Find("WARN", "could not be recorded")); n != 1 || e.cleanup(job.ID) != "pending" {
		t.Errorf("%d warnings, cleanup %s, want one warning and the cleanup pending", n, e.cleanup(job.ID))
	}
}

func TestCleanup_IsGivenUpAnHourAfterTheCancelWithOneError(t *testing.T) {
	c := &fakeCleaner{result: pendingWith("hub is unavailable")}
	e := newEnv(t, withCleaner(c), withoutEnqueue())
	e.start()
	job := e.create("widgets", 42, "af:fix")
	if _, err := e.Svc.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	for range 60 {
		e.idle()
		e.Clock.Advance(time.Minute)
	}
	testutil.Eventually(t, "the cleanup to be given up with an ERROR", func() bool {
		return e.cleanup(job.ID) == "label_not_set" && len(e.Log.Find("ERROR", "an hour after the cancel")) == 1
	})
	e.idle()
	tries := len(c.Tries())
	for range 3 {
		e.Clock.Advance(time.Minute)
	}
	e.idle()
	if len(c.Tries()) != tries || len(e.Log.Find("ERROR", "an hour after the cancel")) != 1 {
		t.Errorf("%d tries after it was given up and %d ERROR records, want none and one", len(c.Tries())-tries, len(e.Log.Find("ERROR", "an hour")))
	}
}

func TestCleanup_ARestartTriesThePendingCleanupsAndGivesUpThosePastTheirHour(t *testing.T) {
	c := &fakeCleaner{result: pendingWith("hub is unavailable")}
	e := newEnv(t, withCleaner(c), withoutEnqueue())
	recent, old := e.create("widgets", 42, "af:fix"), e.create("widgets", 43, "af:fix")
	for _, id := range []string{recent.ID, old.ID} {
		if _, err := e.Svc.Cancel(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	e.idle()
	testutil.Exec(t, e.DB, "UPDATE af_jobs SET finished_at = ? WHERE id = ?", stamp(epoch.Add(-2*time.Hour)), old.ID)
	done := &fakeCleaner{}
	e.restart(fakerunner.New(), withCleaner(done))
	testutil.Eventually(t, "the recent cleanup", func() bool { return e.cleanup(recent.ID) == "done" })
	e.idle()
	if e.cleanup(old.ID) != "label_not_set" || len(done.Tries()) != 1 {
		t.Errorf("the old cleanup is %s after %d tries, want label_not_set without a try", e.cleanup(old.ID), len(done.Tries()))
	}
}

func TestCleanup_StartDoesNotWaitForATryAndAShutdownCutsItShort(t *testing.T) {
	c := &fakeCleaner{hold: make(chan struct{})}
	e := newEnv(t, withCleaner(c), withoutEnqueue())
	job := e.create("widgets", 42, "af:fix")
	testutil.Exec(t, e.DB, "UPDATE af_jobs SET state = 'cancelled', finished_at = ?, cleanup = 'pending' WHERE id = ?", stamp(epoch), job.ID)
	waiting := e.create("widgets", 43, "") // queued, to be handed over by the first sweep
	e.start()
	e.waitState(waiting.ID, jobs.StateDone)
	testutil.Eventually(t, "the try to be held", func() bool { return len(c.Tries()) == 1 && e.Svc.CleanupsInFlight() == 1 })
	e.close()
	if e.Svc.CleanupsInFlight() != 0 || e.cleanup(job.ID) != "pending" || e.cleanupWarnings(job.ID) != 0 {
		t.Errorf("after the shutdown: %d tries, cleanup %s, %d warnings, want none in flight and the cleanup pending", e.Svc.CleanupsInFlight(), e.cleanup(job.ID), e.cleanupWarnings(job.ID))
	}
	<-e.Svc.TryCleanup(job.ID) // a stopped layer starts none
	if len(c.Tries()) != 1 {
		t.Errorf("%d tries, want no more after the stop", len(c.Tries()))
	}
}

func TestCleanup_ACancelOfAClaimedAttemptWhoseRunnerWasNotCalledIsMarkedAtOnce(t *testing.T) {
	c := &fakeCleaner{hold: make(chan struct{})}
	e := newEnv(t, withCleaner(c), withoutEnqueue())
	job := e.create("widgets", 42, "af:fix")
	testutil.Exec(t, e.DB, "UPDATE af_attempts SET state = 'running' WHERE job_id = ?", job.ID)
	testutil.Exec(t, e.DB, "UPDATE af_jobs SET state = 'running' WHERE id = ?", job.ID)
	forget := e.Svc.RegisterRun(job.ID, 1)
	defer forget()
	if st, _ := e.Svc.Standing(ctx, job.ID); !st.RunnerInProgress || !e.Svc.Running(job.ID, 1) {
		t.Errorf("standing = %+v, want the run known", st)
	}
	if d, err := e.Svc.Cancel(ctx, job.ID); err != nil || d.Labels.Cleanup == nil || *d.Labels.Cleanup != "pending" {
		t.Fatalf("the cancel's answer: %+v, %v, want the cleanup pending", d.Labels, err)
	}
	testutil.Eventually(t, "the try", func() bool { return len(c.Tries()) == 1 })
	close(c.hold)
}

func TestCleanup_ARunnerCancelledBeforeItOpenedAHubSessionIsCleanedUpOnceItReturns(t *testing.T) {
	for _, overrun := range []bool{false, true} {
		t.Run(map[bool]string{false: "it returns", true: "it overruns its grace"}[overrun], func(t *testing.T) {
			c := &fakeCleaner{}
			release := make(chan struct{})
			r := fakerunner.New(needsHuman(), fakerunner.Do(func(ctx context.Context, _ jobs.Request) (jobs.Result, error) {
				if !overrun {
					<-ctx.Done()
				}
				<-release
				return jobs.Result{}, context.Cause(ctx)
			}))
			e := newEnv(t, withRunner(r), withCleaner(c))
			e.openAtEnd(release)
			e.start()
			job := e.waitState(e.create("widgets", 42, "af:fix").ID, jobs.StateBlocked)
			if _, err := e.Svc.Answer(ctx, job.ID, "u", jobs.AnswerRequest{Option: ptr("A")}); err != nil {
				t.Fatal(err)
			}
			e.waitCalls(2)
			if _, err := e.Svc.Cancel(ctx, job.ID); err != nil {
				t.Fatal(err)
			}
			if st, _ := e.Svc.Standing(ctx, job.ID); !st.CleanupPending || !st.CancelledInRunner || !st.RunnerInProgress {
				t.Errorf("standing = %+v, want the cleanup owed while the runner is in progress", st)
			}
			if overrun {
				e.waitWaits(1)
				e.Clock.Advance(cancelGrace)
				testutil.Eventually(t, "the attempt to be recorded", func() bool { return e.get(job.ID).Attempts[1].State == jobs.AttemptCancelled })
				if e.cleanup(job.ID) != "pending" {
					t.Errorf("cleanup %q once the overrun is recorded, want pending", e.cleanup(job.ID))
				}
				e.Clock.Advance(time.Minute) // a sweep does not try it while the runner is in progress
			}
			if len(c.Tries()) != 0 {
				t.Fatalf("a try ran while the runner was in progress")
			}
			close(release)
			testutil.Eventually(t, "the cleanup", func() bool { return e.cleanup(job.ID) == "done" })
		})
	}
}

func TestCleanup_ACancelThatReachesARunnerWithAHubSessionOwesNone(t *testing.T) {
	for _, how := range []string{"a person", "an unsubscribe"} {
		t.Run(how, func(t *testing.T) {
			c := &fakeCleaner{}
			r := fakerunner.New(needsHuman(), fakerunner.Do(func(ctx context.Context, req jobs.Request) (jobs.Result, error) {
				req.Recorder.Hub("ses_1", "run_1", "sbx_1")
				<-ctx.Done()
				return jobs.Result{LabelsSet: []string{"af:fix"}}, context.Cause(ctx)
			}))
			e := newEnv(t, withRunner(r), withCleaner(c))
			e.start()
			job := e.waitState(e.create("widgets", 42, "af:fix").ID, jobs.StateBlocked)
			if _, err := e.Svc.Answer(ctx, job.ID, "u", jobs.AnswerRequest{Option: ptr("A")}); err != nil {
				t.Fatal(err)
			}
			testutil.Eventually(t, "the hub session", func() bool { return e.get(job.ID).Hub != nil })
			if how == "a person" {
				if _, err := e.Svc.Cancel(ctx, job.ID); err != nil {
					t.Fatal(err)
				}
			} else if err := e.Svc.Unsubscribe(ctx, "widgets", func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			d := e.waitRest(job.ID)
			if d.Labels.Cleanup != nil || len(c.Tries()) != 0 || d.Labels.Restore != nil {
				t.Errorf("labels %+v and %d tries, want no cleanup: the runner put the label back", d.Labels, len(c.Tries()))
			}
			if st, _ := e.Svc.Standing(ctx, job.ID); st.CleanupPending || !st.CancelledInRunner {
				t.Errorf("standing = %+v", st)
			}
		})
	}
}
