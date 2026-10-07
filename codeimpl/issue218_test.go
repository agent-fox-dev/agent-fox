package codeimpl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// Issue #218 (1): a deviation a task declared in an earlier run is carried by
// that task's commit, so the run that continues the branch still knows it was
// declared: the blocker it answers is not blocking.
func TestDeviationsDeclaredByAnEarlierRunSurviveContinuation(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	const reason = "the CLI library maps every usage error to exit 2"
	b := &scriptedBrain{implement: func(root string, task afspec.Task, attempt int) (Submission, error) {
		sub, err := goodWork(root, task, attempt)
		if task.Id == 1 {
			sub.Deviations = []conform.Deviation{{Key: "09-REQ-1", Reason: reason}}
		}
		return sub, err
	}}
	o := newOptions(ws, g, b)
	o.Task = 1
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("first run: %v", err)
	}
	body := gitOut(t, ws.Root, "log", "-1", "--format=%B", "impl/09-agent-mode-spec-cli")
	if !strings.Contains(body, "Deviation: ") || !strings.Contains(body, reason) {
		t.Fatalf("the task's commit does not carry its deviation:\n%s", body)
	}
	gitOut(t, ws.Root, "checkout", "-q", "main")

	b2 := &scriptedBrain{}
	b2.review = func(in conform.ReviewInput) (conform.Review, error) {
		r := conformingReview(in.Scope)
		r.Requirements[0] = conform.RequirementRow{ID: "09-REQ-1", Status: conform.StatusDifferent,
			Evidence: "task1.go:1 exits 2; the requirement says 3"}
		return r, nil
	}
	b2.resolve = func(string, resolveInput) (ResolveSubmission, error) {
		return ResolveSubmission{Summary: "Nothing to change.", CommitSubject: "none"}, nil
	}
	got, err := Run(context.Background(), newOptions(ws, g, b2))
	if err != nil {
		t.Fatalf("second run: %v (the earlier run's declaration was lost)", err)
	}
	if !got.Resumed || len(got.Blocking) != 0 {
		t.Errorf("Resumed=%v Blocking=%+v", got.Resumed, got.Blocking)
	}
	if len(got.Unmet) == 0 || got.Unmet[0].Source != conform.SourceDeclared ||
		got.Unmet[0].Requirement != "09-REQ-1" || got.Unmet[0].What != reason {
		t.Errorf("Unmet = %+v", got.Unmet)
	}
}

// Issue #218 (2): a review shortfall alone sends the change to the resolve
// phase, which is shown it, and a shortfall it declares is reported once.
func TestAShortfallAloneReachesTheResolvePhase(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &scriptedBrain{}
	b.review = func(in conform.ReviewInput) (conform.Review, error) {
		r := conformingReview(in.Scope)
		r.Tests[0] = conform.TestRow{ID: "TS-09-1", Assessment: conform.AssessWeaker,
			Evidence: "asserts the internal error code, not the process exit code"}
		return r, nil
	}
	b.resolve = func(root string, in resolveInput) (ResolveSubmission, error) {
		write(t, root, "ERRATA.md", "TS-09-1 asserts the code: task1_test.go:1.\n")
		return ResolveSubmission{Summary: "Declared it.", CommitSubject: "record the TS-09-1 erratum",
			Changes:    []FileChange{{Path: "ERRATA.md", Change: "added"}},
			Deviations: []conform.Deviation{{Key: "TS-09-1", Reason: "the exit code is not observable", Errata: "ERRATA.md"}}}, nil
	}
	got, err := Run(context.Background(), newOptions(ws, g, b))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(b.resolveIns) != 1 {
		t.Fatalf("the resolve phase ran %d time(s) for a weaker test", len(b.resolveIns))
	}
	in := b.resolveIns[0]
	if len(in.Shortfalls) != 1 || in.Shortfalls[0].Test != "TS-09-1" {
		t.Errorf("resolve input shortfalls = %+v", in.Shortfalls)
	}
	if p := resolvePrompt(in); !strings.Contains(p, "TS-09-1") || !strings.Contains(p, "weaker") {
		t.Errorf("the prompt does not show the shortfall:\n%s", p)
	}
	// The phase's own tool accepts the declaration of a shortfall by its id.
	var sink sinkResolve
	res := submitResolveTool(&sink.s, in.Blockers, in.Shortfalls).Execute(context.Background(),
		mustJSON(t, ResolveSubmission{Summary: "x", Deviations: []conform.Deviation{{Key: "TS-09-1",
			Reason: "the exit code is not observable from inside the test harness"}}}))
	if !res.OK {
		t.Errorf("a shortfall could not be declared: %+v", res)
	}
	var ts []conform.Unmet
	for _, u := range got.Unmet {
		if u.Test == "TS-09-1" {
			ts = append(ts, u)
		}
	}
	// The review after the resolve change still finds the test weaker, and
	// the declaration answers it: one item, the declared one.
	if len(ts) != 1 || ts[0].Source != conform.SourceDeclared || ts[0].Tracking != "ERRATA.md" {
		t.Errorf("TS-09-1 items = %+v", ts)
	}
}

// Issue #218 (2): every unmet item is tracked or warned about, not only the
// declared ones.
func TestEveryUntrackedUnmetItemIsWarnedAbout(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &scriptedBrain{}
	b.review = func(in conform.ReviewInput) (conform.Review, error) {
		r := conformingReview(in.Scope)
		r.Tests[0] = conform.TestRow{ID: "TS-09-1", Assessment: conform.AssessWeaker, Evidence: "asserts nothing"}
		return r, nil
	}
	b.resolve = func(string, resolveInput) (ResolveSubmission, error) {
		return ResolveSubmission{Summary: "Left it.", CommitSubject: "none"}, nil
	}
	o := newOptions(ws, g, b)
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var warned []string
	for _, w := range o.Run.Warnings() {
		if w.Code == toolio.WarnDeviationNotTracked {
			warned = append(warned, w.Message)
		}
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "TS-09-1") {
		t.Errorf("deviation_not_tracked warnings = %q, want one naming the weaker test", warned)
	}
}

// Issue #218 (3): the baseline repair's files are not "a change the spec did
// not ask for": the pull request says the repair is not part of the spec.
func TestTheBaselineRepairsFilesAreNotOutOfScope(t *testing.T) {
	ws, g, specDir := newSpecRepo(t)
	setTouches(t, specDir, map[int][]string{1: {"task1.go"}, 2: {"task2.go"}, 3: {"task3.go"}})
	write(t, ws.Root, "FAIL", "the suite is red")
	if _, err := g.CommitAll(context.Background(), "chore: break the build\n"); err != nil {
		t.Fatal(err)
	}
	b := &scriptedBrain{}
	b.repair = func(root string, attempt int) (RepairSubmission, error) {
		if err := os.Remove(filepath.Join(root, "FAIL")); err != nil {
			return RepairSubmission{}, err
		}
		write(t, root, "fixture.go", "package x // the fixture the suite needed\n")
		return RepairSubmission{Cause: "A FAIL marker was committed.", Summary: "Removed it.",
			CommitSubject: "remove the FAIL marker",
			Changes:       []FileChange{{Path: "FAIL", Change: "deleted"}, {Path: "fixture.go", Change: "added"}}}, nil
	}
	o := newOptions(ws, g, b)
	o.Repair = true
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got.OutOfScope) != 0 {
		t.Errorf("OutOfScope = %v; the repair's files are not the spec's to list", got.OutOfScope)
	}
}

// reviewCostBrain charges a fixed amount for the review.
type reviewCostBrain struct {
	*scriptedBrain
	cost float64
}

func (b *reviewCostBrain) Review(ctx context.Context, in conform.ReviewInput) (conform.Review, agentrun.Result, error) {
	r, res, err := b.scriptedBrain.Review(ctx, in)
	res.Usage.CostUSD = b.cost
	return r, res, err
}

// Issue #218 (4): the run-level budget is checked before the review: one the
// tasks spent is not run, and the report says why.
func TestTotalBudgetIsCheckedBeforeTheReview(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &costlyBrain{scriptedBrain: &scriptedBrain{}, cost: 2}
	o := newOptions(ws, g, b.scriptedBrain)
	o.brain = b
	o.NoSurvey = true
	o.TotalBudgetUSD = 6
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(b.reviewIns) != 0 {
		t.Errorf("the review ran %d time(s) past the total budget", len(b.reviewIns))
	}
	found := false
	for _, w := range o.Run.Warnings() {
		found = found || (w.Code == toolio.WarnReviewNotRun && strings.Contains(w.Message, "budget"))
	}
	if !found {
		t.Errorf("no review_not_run warning naming the budget: %+v", o.Run.Warnings())
	}
	if got.TasksDone != 3 || len(got.Unmet) == 0 {
		t.Errorf("done=%d unmet=%+v", got.TasksDone, got.Unmet)
	}
}

// Issue #218 (4): a resolve phase the budget withholds is warned about, so a
// nonconformant exit says the fix was never attempted.
func TestAResolveTheBudgetWithholdsIsWarned(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	inner := &scriptedBrain{}
	inner.review = func(in conform.ReviewInput) (conform.Review, error) {
		r := conformingReview(in.Scope)
		r.Requirements[0] = conform.RequirementRow{ID: "09-REQ-1", Status: conform.StatusDifferent,
			Evidence: "task1.go:1 exits 2"}
		return r, nil
	}
	b := &reviewCostBrain{scriptedBrain: inner, cost: 10}
	o := newOptions(ws, g, inner)
	o.brain = b
	o.NoSurvey = true
	o.TotalBudgetUSD = 5
	got, err := Run(context.Background(), o)
	if f := failureOf(t, err); f.Category != CategoryNonconformant {
		t.Fatalf("failure = %+v", f)
	}
	if len(inner.resolveIns) != 0 || len(got.Blocking) != 1 {
		t.Errorf("resolve ran %d time(s); Blocking = %+v", len(inner.resolveIns), got.Blocking)
	}
	if !hasWarning(o.Run, toolio.WarnResolveNotRun) {
		t.Errorf("no resolve_not_run warning: %+v", o.Run.Warnings())
	}
}

// Issue #218 (5): every park is a parked run, whatever its category, so the
// caller exits 4 for it as the docs say.
func TestEveryParkIsMarkedParked(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &scriptedBrain{implement: func(root string, task afspec.Task, attempt int) (Submission, error) {
		return passingReport(task, "claimed a change"), nil
	}}
	_, err := Run(context.Background(), newOptions(ws, g, b))
	if f := failureOf(t, err); f.Category != CategoryEmpty || !f.Parked || !IsParked(err) {
		t.Errorf("failure = %+v; an empty change is parked", f)
	}
	if IsParked(failf("preflight", "usage", "x")) {
		t.Error("a failure that parked nothing is reported parked")
	}
}

// Issue #218 (smaller): tests that fail to compile without the
// implementation prove nothing about its behaviour; that is recorded, not
// counted as proof, and does not fail the attempt.
func TestACompileFailureIsNotRevertProof(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	write(t, ws.Root, "Makefile", "test:\n\t@test ! -f FAIL\n\t@[ -d tests ] || exit 0; for t in tests/*.txt; do "+
		"test -f \"$$(basename $$t .txt).go\" || { echo 'FAIL\tx [build failed]'; exit 1; }; done\nlint:\n\t@exit 0\n")
	if _, err := g.CommitAll(context.Background(), "chore: tests that need the code to build\n"); err != nil {
		t.Fatal(err)
	}
	b := &scriptedBrain{implement: func(root string, task afspec.Task, attempt int) (Submission, error) {
		sub, err := goodWork(root, task, attempt)
		if err := os.MkdirAll(filepath.Join(root, "tests"), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, root, filepath.Join("tests", "task"+itoa(task.Id)+".txt"), "needs task\n")
		sub.TestFirstDeviation = "an integration task; it passes immediately"
		return sub, err
	}}
	o := newOptions(ws, g, b)
	o.Task = 1
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rc := got.Tasks[0].RevertCheck
	if rc == nil || !rc.Ran || rc.Proves || !rc.CompileFailed {
		t.Errorf("revert check = %+v", rc)
	}
}

// Issue #218 (smaller): an untracked scratch-looking file that was there
// before the phase is someone else's, and is left alone.
func TestScratchFilesThatPredateThePhaseAreLeftAlone(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	write(t, ws.Root, "notes.bak", "a person's notes\n")
	write(t, ws.Root, "left.orig", "the phase's leftover\n")
	st := &runState{root: ws.Root, git: g, untrackedBefore: map[string]bool{"notes.bak": true}}
	o := newOptions(ws, g, &scriptedBrain{})
	dropScratchFiles(context.Background(), o, st)
	if _, err := os.Stat(filepath.Join(ws.Root, "notes.bak")); err != nil {
		t.Errorf("a file that predates the phase was deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws.Root, "left.orig")); !os.IsNotExist(err) {
		t.Errorf("the phase's own leftover was kept: %v", err)
	}
}

// Issue #218 (smaller): a run with nothing to do on an existing work branch
// returns the checkout to the base branch.
func TestNothingToDoReturnsTheCheckout(t *testing.T) {
	ws, g, specDir := newSpecRepo(t)
	gitOut(t, ws.Root, "checkout", "-q", "-b", "impl/09-agent-mode-spec-cli")
	spec, _ := afspec.LoadSpec(specDir)
	spec.Tasks = spec.Tasks.CompleteTaskStates([]int{1, 2, 3})
	if err := saveTasks(spec, specDir); err != nil {
		t.Fatal(err)
	}
	_, _ = g.CommitAll(context.Background(), "chore: all done\n")
	gitOut(t, ws.Root, "checkout", "-q", "main")

	got, err := Run(context.Background(), newOptions(ws, g, &scriptedBrain{}))
	if err != nil || got.Stage != "complete" {
		t.Fatalf("Stage=%q err=%v", got.Stage, err)
	}
	if cur := gitOut(t, ws.Root, "rev-parse", "--abbrev-ref", "HEAD"); cur != "main" {
		t.Errorf("checked out %q, want main", cur)
	}
}

// Issue #221: a Python or Rust test names a spec test in snake_case.
func TestNamesSpecTestInSnakeCase(t *testing.T) {
	ids := []string{"TS-16-14"}
	for content, want := range map[string]bool{
		"def test_ts16_14_rejects_expiry():": true,
		"fn ts_16_14_counts_once() {":        true,
		"def test_ts16_140():":               false,
		"def test_unrelated():":              false,
	} {
		if got := namesSpecTest(content, ids); got != want {
			t.Errorf("namesSpecTest(%q) = %v, want %v", content, got, want)
		}
	}
}
