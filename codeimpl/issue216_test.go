package codeimpl

import (
	"context"
	"testing"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
)

// cancelOnCall is a check runner that runs the real command, except that on
// its nth call it cancels the run and returns what a killed process returns.
func cancelOnCall(n int, cancel context.CancelFunc) gitx.Runner {
	calls := 0
	return func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
		calls++
		if calls == n {
			cancel()
			<-ctx.Done()
			return "", -1, nil
		}
		return gitx.ExecRunner(ctx, dir, argv, stdin...)
	}
}

func assertAborted(t *testing.T, err error) *Failure {
	t.Helper()
	f := failureOf(t, err)
	if f.Category != agentrun.CategoryAborted {
		t.Fatalf("failure = %+v (%v), want category aborted", f, err)
	}
	return f
}

func assertOnMain(t *testing.T, root string) {
	t.Helper()
	if cur := gitOut(t, root, "rev-parse", "--abbrev-ref", "HEAD"); cur != "main" {
		t.Errorf("checked out %q, want main", cur)
	}
	if dirty := gitOut(t, root, "status", "--porcelain"); dirty != "" {
		t.Errorf("the tree is dirty:\n%s", dirty)
	}
}

// Issue #216 (2): a baseline check the cancellation killed is aborted, not
// "the command could not run, fix tasks.json".
func TestACancelledBaselineIsAbortedNotAUsageError(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := newOptions(ws, g, &scriptedBrain{})
	o.CheckRunner = cancelOnCall(1, cancel)
	_, err := Run(ctx, o)
	assertAborted(t, err)
}

// Issue #216 (2): a task's gate the cancellation killed parks the task as
// aborted — not gate_failed, not unverified.
func TestACancelledTaskGateParksTheTaskAsAborted(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := newOptions(ws, g, &scriptedBrain{})
	o.NoSurvey = true
	o.CheckRunner = cancelOnCall(3, cancel) // after the two baseline checks
	got, err := Run(ctx, o)
	if f := assertAborted(t, err); !f.Parked {
		t.Errorf("the task was not parked: %+v", f)
	}
	if got.Tasks[0].Outcome != OutcomeAborted || got.Tasks[0].Verdict == VerdictGateFailed {
		t.Errorf("task 1 = %+v", got.Tasks[0])
	}
	assertOnMain(t, ws.Root)
}

// Issue #216 (4): a cancellation between tasks stops the run before the next
// one, with the landed work committed and the checkout back on the base.
func TestCancellationBetweenTasksStopsTheRun(t *testing.T) {
	ws, _, _ := newSpecRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := &scriptedBrain{}
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		sub, err := goodWork(root, task, attempt)
		if task.Id == 1 {
			cancel()
		}
		return sub, err
	}
	// The gate and git ignore the cancellation here, so task 1 still lands
	// and only the check between tasks can stop the run.
	uncancelled := func(c context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
		return gitx.ExecRunner(context.WithoutCancel(c), dir, argv, stdin...)
	}
	o := newOptions(ws, gitx.New(ws.Root, uncancelled), b)
	o.NoSurvey = true
	o.CheckRunner = uncancelled
	got, err := Run(ctx, o)
	assertAborted(t, err)
	if len(b.inputs) != 1 || got.TasksDone > 1 {
		t.Errorf("%d task phase(s) ran, %d done; the run went on after the cancellation", len(b.inputs), got.TasksDone)
	}
	assertOnMain(t, ws.Root)
}

// Issue #216 (4): a cancelled review is not "the review did not complete" on
// a run that then lands: the run stops, aborted, and lands nothing.
func TestACancelledReviewDoesNotLand(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &scriptedBrain{}
	b.review = func(conform.ReviewInput) (conform.Review, error) {
		return conform.Review{}, &agentrun.Error{Phase: conform.PhaseReview, Cat: agentrun.CategoryAborted,
			Cause: context.Canceled}
	}
	got, err := Run(context.Background(), newOptions(ws, g, b))
	assertAborted(t, err)
	if got.Stage == "landed" {
		t.Error("a run cancelled during the review landed")
	}
	assertOnMain(t, ws.Root)
}

// Issue #216 (2): a clean-environment gate the cancellation killed is not a
// hermetic blocker on a draft pull request: the run is aborted.
func TestACancelledCleanEnvironmentGateIsNotABlocker(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := newOptions(ws, g, &scriptedBrain{})
	o.NoSurvey = true
	o.HermeticRunner = func(string) gitx.Runner { return cancelOnCall(1, cancel) }
	got, err := Run(ctx, o)
	assertAborted(t, err)
	if len(got.Blocking) != 0 {
		t.Errorf("Blocking = %+v", got.Blocking)
	}
}
