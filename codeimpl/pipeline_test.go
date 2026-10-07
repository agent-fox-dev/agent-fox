package codeimpl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/agentrun/indextest"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// The tests below drive the REAL pipeline — the real pre-flight, the real
// git, the real spec loading and state writes, the real gate comparison —
// with the model half replaced by a scripted brain. The split that makes
// the program trustworthy is the same split that makes it testable.

// scriptedBrain stands in for the three phases.
type scriptedBrain struct {
	survey    Survey
	surveyErr error
	// implement decides what one attempt does, given the task and the
	// attempt number: it edits the tree and returns the report.
	implement func(root string, task afspec.Task, attempt int) (Submission, error)
	// repair decides what one repair attempt does. Nil means the phase is
	// never expected and fails the test if it runs.
	repair func(root string, attempt int) (RepairSubmission, error)

	// review decides what the conformance review answers. Nil means every
	// id in scope is answered met.
	review func(in conform.ReviewInput) (conform.Review, error)
	// resolve decides what the resolve phase does. Nil means the phase is
	// never expected and fails the test if it runs.
	resolve func(root string, in resolveInput) (ResolveSubmission, error)

	surveys    int
	inputs     []taskInput
	repairIns  []repairInput
	surveyIn   surveyInput
	reviewIns  []conform.ReviewInput
	resolveIns []resolveInput
}

func (b *scriptedBrain) Review(_ context.Context, in conform.ReviewInput) (conform.Review, agentrun.Result, error) {
	b.reviewIns = append(b.reviewIns, in)
	_ = conform.ReviewPrompt(in) // every prompt must render
	res := agentrun.Result{Name: conform.PhaseReview, Turns: 4}
	if b.review != nil {
		r, err := b.review(in)
		return r, res, err
	}
	return conformingReview(in.Scope), res, nil
}

// conformingReview answers every id in scope as met.
func conformingReview(sc conform.ReviewScope) conform.Review {
	r := conform.Review{Summary: "Every requirement and test in scope is met."}
	for _, id := range sc.Requirements {
		r.Requirements = append(r.Requirements, conform.RequirementRow{ID: id, Status: conform.StatusImplemented,
			Evidence: "task1.go:1 does it"})
	}
	for _, id := range sc.Tests {
		r.Tests = append(r.Tests, conform.TestRow{ID: id, Assessment: conform.AssessAssertsContract,
			Evidence: "task1_test.go:1 asserts the outcome"})
	}
	for _, d := range sc.Decisions {
		r.Decisions = append(r.Decisions, conform.DecisionRow{ID: d.ID, Status: conform.DecisionFollowed,
			Evidence: "task1.go:1 follows it"})
	}
	return r
}

func (b *scriptedBrain) Resolve(_ context.Context, in resolveInput) (ResolveSubmission, agentrun.Result, error) {
	b.resolveIns = append(b.resolveIns, in)
	_ = resolvePrompt(in) // every prompt must render
	res := agentrun.Result{Name: PhaseResolve, Turns: 6}
	if b.resolve == nil {
		return ResolveSubmission{}, res, errors.New("the resolve phase ran and the test did not script it")
	}
	sub, err := b.resolve(in.Root, in)
	return sub, res, err
}

func (b *scriptedBrain) Repair(_ context.Context, in repairInput) (RepairSubmission, agentrun.Result, error) {
	b.repairIns = append(b.repairIns, in)
	_ = repairPrompt(in) // every prompt must render
	res := agentrun.Result{Name: PhaseRepair, Turns: 5}
	if b.repair == nil {
		return RepairSubmission{}, res, errors.New("the repair phase ran and the test did not script it")
	}
	sub, err := b.repair(in.Root, in.Attempt)
	return sub, res, err
}

// RepairModel is what the report names; the scripted brain has none.
func (b *scriptedBrain) RepairModel() string { return "scripted-repair" }

func (b *scriptedBrain) Survey(_ context.Context, in surveyInput) (Survey, agentrun.Result, error) {
	b.surveys++
	b.surveyIn = in
	res := agentrun.Result{Name: PhaseSurvey, Turns: 3}
	if b.surveyErr != nil {
		return Survey{}, res, b.surveyErr
	}
	return b.survey, res, nil
}

func (b *scriptedBrain) Implement(_ context.Context, in taskInput) (Submission, agentrun.Result, error) {
	b.inputs = append(b.inputs, in)
	_ = taskPrompt(in) // every prompt must render
	res := agentrun.Result{Name: PhaseImplement, Turns: 7}
	sub, err := b.implement(in.Root, in.Task, in.Attempt)
	return sub, res, err
}

// goodWork is an implementation that writes one file per task and reports
// every owned test and done_when entry as passing.
func goodWork(root string, task afspec.Task, attempt int) (Submission, error) {
	name := filepath.Join(root, "task"+itoa(task.Id)+".go")
	if err := os.WriteFile(name, []byte("package x // task "+itoa(task.Id)+"\n"), 0o644); err != nil {
		return Submission{}, err
	}
	return passingReport(task, "implemented task "+itoa(task.Id)), nil
}

func passingReport(task afspec.Task, subject string) Submission {
	sub := Submission{
		Summary:       "Did what task " + itoa(task.Id) + " asked.",
		CommitSubject: subject,
		Changes:       []FileChange{{Path: "task" + itoa(task.Id) + ".go", Change: "added"}},
		Gotchas:       []string{"the widget counter is zero-based"},
	}
	for _, id := range task.Tests {
		sub.TestVerdicts = append(sub.TestVerdicts, Verdict{ID: id, Verdict: VerdictPass,
			Evidence:    "task" + itoa(task.Id) + "_test.go Test" + id + " passes when run with make test",
			RedEvidence: "go test failed before the change: Test" + id + " got the zero value"})
	}
	for i := range task.DoneWhen {
		sub.DoneWhenVerdicts = append(sub.DoneWhenVerdicts, Verdict{ID: "DW-" + itoa(i+1), Verdict: VerdictPass,
			Evidence: "ran the command in done_when and it exited zero"})
	}
	return sub
}

func itoa(n int) string { return strings.TrimSpace(strings.Repeat(" ", 0) + intString(n)) }

func intString(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return intString(n/10) + string(rune('0'+n%10))
}

// newSpecRepo builds a real repository holding a copy of the v2 example
// spec, activated, with test commands that resolve to a Makefile whose
// `test` target fails exactly when a FAIL file exists in the tree.
func newSpecRepo(t *testing.T) (*tools.Workspace, *gitx.Git, string) {
	t.Helper()
	return newSpecRepoWith(t, nil)
}

// newSpecRepoWith is newSpecRepo with the example's task list passed through
// edit before it is written, for a test that needs a different plan.
func newSpecRepoWith(t *testing.T, edit func(tasks []any) []any) (*tools.Workspace, *gitx.Git, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	ctx := context.Background()
	for _, argv := range [][]string{
		{"git", "init", "-q", "-b", "main"},
		{"git", "config", "user.email", "test@example.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "commit.gpgsign", "false"},
	} {
		if out, code, err := gitx.ExecRunner(ctx, dir, argv); err != nil || code != 0 {
			t.Fatalf("%v: %v (%d) %s", argv, err, code, out)
		}
	}
	write(t, dir, "go.mod", "module x\n")
	write(t, dir, "Makefile", "test:\n\t@test ! -f FAIL\nlint:\n\t@exit 0\n")
	write(t, dir, "AGENTS.md", "# House rules\n\nUse tabs.\n")

	specDir := filepath.Join(dir, ".specs", "09_agent_mode")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join("..", "testdata", "v2_example")
	for _, name := range []string{"prd.md", "requirements.json", "test_spec.json", "tasks.json"} {
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "tasks.json" {
			var doc map[string]any
			if err := json.Unmarshal(b, &doc); err != nil {
				t.Fatal(err)
			}
			doc["test_commands"] = map[string]any{"all_tests": "make test", "linter": "make lint"}
			// The example's touches name cmd/spec files the scripted work
			// never writes; a spec without them does not restrict the change,
			// and the scope check has a test of its own.
			for _, task := range doc["tasks"].([]any) {
				delete(task.(map[string]any), "touches")
			}
			if edit != nil {
				doc["tasks"] = edit(doc["tasks"].([]any))
			}
			if b, err = json.MarshalIndent(doc, "", "  "); err != nil {
				t.Fatal(err)
			}
		}
		write(t, specDir, name, string(b))
	}
	spec, err := afspec.LoadSpec(specDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spec.Transition("active", specDir); err != nil {
		t.Fatal(err)
	}

	g := gitx.New(dir, gitx.ExecRunner)
	g.SetSleep(func(time.Duration) {})
	if _, err := g.CommitAll(ctx, "chore: initial commit\n"); err != nil {
		t.Fatal(err)
	}
	ws, err := tools.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ws, g, specDir
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newOptions(ws *tools.Workspace, g *gitx.Git, b *scriptedBrain) Options {
	if b.implement == nil {
		b.implement = goodWork
	}
	if b.survey.Summary == "" {
		b.survey = Survey{Summary: "The CLI lives in cmd/spec; nothing exists yet."}
	}
	return Options{
		Input:         toolio.Input{Kind: toolio.KindText, Origin: "argument", Body: "09"},
		Workspace:     ws,
		Land:          LandNone,
		Git:           g,
		CheckRunner:   gitx.ExecRunner,
		VerifyTimeout: 30 * time.Second,
		Run:           toolio.NewRun("impl", "test"),
		Progress:      toolio.NewProgress(io.Discard, "impl", false, true),
		brain:         b,
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, code, err := gitx.ExecRunner(context.Background(), dir, append([]string{"git"}, args...))
	if err != nil || code != 0 {
		t.Fatalf("git %v: %v (%d) %s", args, err, code, out)
	}
	return strings.TrimSpace(out)
}

func taskStates(t *testing.T, specDir string) map[int]afspec.TaskState {
	t.Helper()
	spec, err := afspec.LoadSpec(specDir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[int]afspec.TaskState{}
	for _, task := range spec.Tasks.Tasks {
		out[task.Id] = task.State
	}
	return out
}

func failureOf(t *testing.T, err error) *Failure {
	t.Helper()
	var f *Failure
	if !errors.As(err, &f) {
		t.Fatalf("error is %T (%v), not a *Failure", err, err)
	}
	return f
}

// The work branch is cut from the branch checked out now, so that is the base
// the pull request targets and the checkout returns to — not origin's default
// (issue #217).
func TestTheBaseIsTheCheckedOutBranchNotOriginsDefault(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	gitOut(t, ws.Root, "update-ref", "refs/remotes/origin/main", "HEAD")
	gitOut(t, ws.Root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	gitOut(t, ws.Root, "checkout", "-q", "-b", "develop")

	got, err := Run(context.Background(), newOptions(ws, g, &scriptedBrain{}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.BaseBranch != "develop" {
		t.Errorf("BaseBranch = %q, want the checked-out develop", got.BaseBranch)
	}
}

// Run from the work branch itself, the base would be the branch: the diff
// under review would be empty and the pull request would target itself. The
// run refuses before anything is spent (issue #217).
func TestARunFromTheWorkBranchItselfIsRefused(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	gitOut(t, ws.Root, "checkout", "-q", "-b", "impl/09-agent-mode-spec-cli")
	b := &scriptedBrain{}

	_, err := Run(context.Background(), newOptions(ws, g, b))
	f := failureOf(t, err)
	if f.Stage != "preflight" || f.Category != "usage" || !strings.Contains(f.Error(), "impl/09-agent-mode-spec-cli") {
		t.Errorf("failure = %+v (%v), want a preflight usage refusal naming the branch", f, err)
	}
	if b.surveys != 0 || len(b.inputs) != 0 {
		t.Errorf("the model was called: surveys=%d inputs=%d", b.surveys, len(b.inputs))
	}
}

func TestPipelineImplementsEveryTaskInOrder(t *testing.T) {
	ws, g, specDir := newSpecRepo(t)
	b := &scriptedBrain{}
	got, err := Run(context.Background(), newOptions(ws, g, b))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Stage != "landed" || got.TasksDone != 3 || got.TasksRemaining != 0 {
		t.Errorf("Stage=%q done=%d remaining=%d", got.Stage, got.TasksDone, got.TasksRemaining)
	}
	if got.Branch != "impl/09-agent-mode-spec-cli" {
		t.Errorf("Branch = %q", got.Branch)
	}
	if got.BaseBranch != "main" || got.Resumed {
		t.Errorf("BaseBranch=%q Resumed=%v", got.BaseBranch, got.Resumed)
	}
	if b.surveys != 1 {
		t.Errorf("survey ran %d times", b.surveys)
	}
	if len(b.inputs) != 3 {
		t.Fatalf("%d implementation phases", len(b.inputs))
	}
	for i, in := range b.inputs {
		if in.Task.Id != i+1 {
			t.Errorf("phase %d implemented task %d", i, in.Task.Id)
		}
		if len(in.Prior) != i {
			t.Errorf("task %d saw %d prior reports, want %d", in.Task.Id, len(in.Prior), i)
		}
		if in.Survey == nil || in.Instructions == "" {
			t.Errorf("task %d was not given the survey and the project instructions", in.Task.Id)
		}
	}
	if len(b.inputs[2].Prior) == 2 && b.inputs[2].Prior[1].Gotchas[0] != "the widget counter is zero-based" {
		t.Error("the previous task's gotchas were not carried forward")
	}

	// Three commits, one per task, each carrying the task's state.
	log := gitOut(t, ws.Root, "log", "--format=%s", "main..HEAD")
	if want := "feat: implemented task 3\nfeat: implemented task 2\nfeat: implemented task 1"; log != want {
		t.Errorf("log =\n%s\nwant\n%s", log, want)
	}
	body := gitOut(t, ws.Root, "log", "-1", "--format=%B")
	if !strings.Contains(body, "Spec: 09_agent_mode, task 3") {
		t.Errorf("the commit lacks the Spec trailer:\n%s", body)
	}
	states := taskStates(t, specDir)
	for id := 1; id <= 3; id++ {
		if states[id] != afspec.TaskStateDone {
			t.Errorf("task %d is %s on disk", id, states[id])
		}
	}
	if shown := gitOut(t, ws.Root, "show", "--stat", "--format=", "HEAD~2"); !strings.Contains(shown, "tasks.json") ||
		strings.Contains(shown, "prd.md") {
		t.Errorf("the first task's commit should carry tasks.json and nothing else of the package:\n%s", shown)
	}
	if dirty, _ := g.DirtyFiles(context.Background()); len(dirty) != 0 {
		t.Errorf("tree is dirty after the run: %v", dirty)
	}
	if cur := gitOut(t, ws.Root, "rev-parse", "--abbrev-ref", "HEAD"); cur != got.Branch {
		t.Errorf("checked out %q after a landed run, want the work branch", cur)
	}
	for _, r := range got.Tasks {
		if r.Outcome != OutcomeDone || r.Verdict != string(checks.VerdictPass) || r.TestsOutcome != VerdictPass {
			t.Errorf("task %d: %+v", r.ID, r)
		}
		if len(r.ChangedFiles) != 2 { // the task's file and tasks.json? no: measured before the state write
			if len(r.ChangedFiles) != 1 || r.ChangedFiles[0] != "task"+itoa(r.ID)+".go" {
				t.Errorf("task %d ChangedFiles = %v — this list comes from git, not from the model", r.ID, r.ChangedFiles)
			}
		}
	}
}

// A task whose checks fail is retried once from the last landed commit, with
// the failure in its prompt; a second failure parks the work and returns the
// checkout to the base branch.
func TestPipelineRetriesThenParksAFailingTask(t *testing.T) {
	ws, g, specDir := newSpecRepo(t)
	b := &scriptedBrain{}
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		if task.Id == 2 {
			write(t, root, "FAIL", "")
			write(t, root, "half.go", "package x\n")
			return passingReport(task, "break the build"), nil
		}
		return goodWork(root, task, attempt)
	}
	got, err := Run(context.Background(), newOptions(ws, g, b))
	f := failureOf(t, err)
	if f.Category != CategoryUnverified || f.Stage != "verify" {
		t.Errorf("failure = %s/%s: %v", f.Stage, f.Category, err)
	}
	if got.Stage != "parked" || got.TasksDone != 1 || got.TasksRemaining != 2 {
		t.Errorf("Stage=%q done=%d remaining=%d", got.Stage, got.TasksDone, got.TasksRemaining)
	}
	if len(b.inputs) != 3 {
		t.Fatalf("%d implementation phases, want task 1 once and task 2 twice", len(b.inputs))
	}
	retry := b.inputs[2]
	if retry.Task.Id != 2 || retry.Attempt != 2 || retry.Previous == nil || retry.Previous.Gate == nil {
		t.Fatalf("the second attempt did not get the first one's failure: %+v", retry.Previous)
	}
	if !strings.Contains(taskPrompt(retry), "did not land because the checks did not pass (regressed)") {
		t.Error("the retry prompt does not say why the first attempt failed")
	}
	if _, err := os.Stat(filepath.Join(ws.Root, "half.go")); !os.IsNotExist(err) {
		t.Error("the first attempt's file survived into the second attempt's tree")
	}

	// The park: a wip commit on the branch, the task in progress in it, the
	// checkout back on main, and a clean tree.
	if cur := gitOut(t, ws.Root, "rev-parse", "--abbrev-ref", "HEAD"); cur != "main" {
		t.Errorf("checked out %q, want main", cur)
	}
	if dirty, _ := g.DirtyFiles(context.Background()); len(dirty) != 0 {
		t.Errorf("tree is dirty after parking: %v", dirty)
	}
	subject := gitOut(t, ws.Root, "log", "-1", "--format=%s", got.Branch)
	if !strings.HasPrefix(subject, "wip: task 2 of 09_agent_mode did not land") {
		t.Errorf("parked subject = %q", subject)
	}
	shown := gitOut(t, ws.Root, "show", got.Branch+":.specs/09_agent_mode/tasks.json")
	var doc afspec.TasksV2Json
	if err := json.Unmarshal([]byte(shown), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Tasks[0].State != afspec.TaskStateDone || doc.Tasks[1].State != afspec.TaskStateInProgress {
		t.Errorf("parked states = %s, %s", doc.Tasks[0].State, doc.Tasks[1].State)
	}
	report := got.Tasks[1]
	if report.Outcome != OutcomeUnverified || report.Attempts != 2 || report.Verdict != string(checks.VerdictRegressed) {
		t.Errorf("task 2 report = %+v", report)
	}

	// A second run continues: it discards the parked attempt, skips the
	// landed task, and finishes the spec.
	b2 := &scriptedBrain{}
	o := newOptions(ws, g, b2)
	got2, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if !got2.Resumed || got2.TasksSkipped != 1 || got2.TasksDone != 2 || got2.Stage != "landed" {
		t.Errorf("second run: resumed=%v skipped=%d done=%d stage=%q", got2.Resumed, got2.TasksSkipped, got2.TasksDone, got2.Stage)
	}
	if got2.Branch != got.Branch {
		t.Errorf("second run used %q, want the same branch %q", got2.Branch, got.Branch)
	}
	warned := strings.Join(toolio.WarningMessages(o.Run.Warnings()), "\n")
	if !strings.Contains(warned, "discarded the parked attempt at task 2") {
		t.Errorf("warnings = %q", warned)
	}
	log := gitOut(t, ws.Root, "log", "--format=%s", "main..HEAD")
	if strings.Contains(log, "wip:") {
		t.Errorf("the parked commit survived the resume:\n%s", log)
	}
	if got2.Tasks[0].Outcome != OutcomeSkipped {
		t.Errorf("task 1 was not reported as skipped: %+v", got2.Tasks[0])
	}
	states := taskStates(t, specDir)
	if states[1] != afspec.TaskStateDone || states[2] != afspec.TaskStateDone || states[3] != afspec.TaskStateDone {
		t.Errorf("states after the second run: %v", states)
	}
}

// A blocker from the survey stops the run before a branch exists and before
// a file is written.
func TestSurveyBlockerStopsBeforeAnyBranch(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &scriptedBrain{survey: Survey{Summary: "x", Blocker: &Blocker{
		Reason: "the PRD names a cobra root command; this repository has no CLI at all", Needed: "decide where the CLI lives"}}}
	got, err := Run(context.Background(), newOptions(ws, g, b))
	f := failureOf(t, err)
	if f.Category != CategoryBlocked {
		t.Errorf("category = %s", f.Category)
	}
	if got.Stage != "stopped" || got.Blocker == nil {
		t.Errorf("Stage=%q Blocker=%v", got.Stage, got.Blocker)
	}
	if len(b.inputs) != 0 {
		t.Error("a task was implemented after the survey blocked")
	}
	if g.LocalBranchExists(context.Background(), "impl/09-agent-mode-spec-cli") {
		t.Error("a branch was created")
	}
	if cur := gitOut(t, ws.Root, "rev-parse", "--abbrev-ref", "HEAD"); cur != "main" {
		t.Errorf("checked out %q", cur)
	}
}

// A task blocker parks the work and exits as blocked, not as unverified.
func TestTaskBlockerParksAndAsks(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &scriptedBrain{}
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		if task.Id == 2 {
			write(t, root, "partial.go", "package x\n")
			return Submission{Blocker: &Blocker{Reason: "REQ-2.2 contradicts the exit code table", Needed: "pick one"}}, nil
		}
		return goodWork(root, task, attempt)
	}
	got, err := Run(context.Background(), newOptions(ws, g, b))
	f := failureOf(t, err)
	if f.Category != CategoryBlocked {
		t.Errorf("category = %s: %v", f.Category, err)
	}
	if got.Stage != "parked" || got.Blocker == nil || got.Tasks[1].Outcome != OutcomeBlocked {
		t.Errorf("Stage=%q Blocker=%v task2=%+v", got.Stage, got.Blocker, got.Tasks[1])
	}
	if len(b.inputs) != 2 {
		t.Errorf("%d phases; a blocker must not be retried", len(b.inputs))
	}
	if cur := gitOut(t, ws.Root, "rev-parse", "--abbrev-ref", "HEAD"); cur != "main" {
		t.Errorf("checked out %q", cur)
	}
}

// A cancelled run still parks: the git steps that put the tree right run
// under a context the cancellation does not reach.
func TestCancellationParksTheWork(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := &scriptedBrain{}
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		if task.Id == 2 {
			write(t, root, "half.go", "package x\n")
			cancel()
			return Submission{}, &agentrun.Error{Phase: PhaseImplement, Cat: agentrun.CategoryAborted,
				Detail: "context canceled", Cause: context.Canceled}
		}
		return goodWork(root, task, attempt)
	}
	got, err := Run(ctx, newOptions(ws, g, b))
	f := failureOf(t, err)
	if f.Category != agentrun.CategoryAborted {
		t.Errorf("category = %s: %v", f.Category, err)
	}
	if got.Stage != "parked" || got.Tasks[1].Outcome != OutcomeAborted || got.Tasks[1].Commit == "" {
		t.Errorf("Stage=%q task2=%+v", got.Stage, got.Tasks[1])
	}
	if cur := gitOut(t, ws.Root, "rev-parse", "--abbrev-ref", "HEAD"); cur != "main" {
		t.Errorf("checked out %q, want main", cur)
	}
	if dirty, _ := g.DirtyFiles(context.Background()); len(dirty) != 0 {
		t.Errorf("tree is dirty after a cancelled run: %v", dirty)
	}
	if subject := gitOut(t, ws.Root, "log", "-1", "--format=%s", got.Branch); !strings.HasPrefix(subject, "wip:") {
		t.Errorf("no parked commit: %q", subject)
	}
}

// What the model writes under the spec package is reverted before the
// change is measured, and the state file the program writes is the one
// that is committed.
func TestSpecPackageEditsAreReverted(t *testing.T) {
	ws, g, specDir := newSpecRepo(t)
	b := &scriptedBrain{}
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		write(t, specDir, "tasks.json", "{}")
		write(t, specDir, "notes.md", "the model's notes")
		return goodWork(root, task, attempt)
	}
	o := newOptions(ws, g, b)
	o.Task = 1
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.TasksDone != 1 {
		t.Errorf("done = %d", got.TasksDone)
	}
	if _, err := os.Stat(filepath.Join(specDir, "notes.md")); !os.IsNotExist(err) {
		t.Error("the model's file under the package survived")
	}
	if states := taskStates(t, specDir); states[1] != afspec.TaskStateDone || states[2] != afspec.TaskStatePending {
		t.Errorf("states = %v", states)
	}
	warned := strings.Join(toolio.WarningMessages(o.Run.Warnings()), "\n")
	if !strings.Contains(warned, "changed the spec package") {
		t.Errorf("no warning about the reverted edit: %q", warned)
	}
	for _, r := range got.Tasks {
		if r.ID == 1 {
			for _, f := range r.ChangedFiles {
				if strings.HasPrefix(f, ".specs/") {
					t.Errorf("the reverted edit was counted as a change: %v", r.ChangedFiles)
				}
			}
		}
	}
}

// --task N implements one task and nothing else, and refuses a task whose
// dependency is not done.
func TestTaskFlagRunsOneTaskOnly(t *testing.T) {
	ws, g, specDir := newSpecRepo(t)
	b := &scriptedBrain{}
	o := newOptions(ws, g, b)
	o.Task = 2
	_, err := Run(context.Background(), o)
	if f := failureOf(t, err); f.Category != "usage" || !strings.Contains(err.Error(), "depends on task 1") {
		t.Errorf("task 2 before task 1: %v", err)
	}
	o.Task = 1
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.TasksDone != 1 || got.TasksRemaining != 2 || len(b.inputs) != 1 {
		t.Errorf("done=%d remaining=%d phases=%d", got.TasksDone, got.TasksRemaining, len(b.inputs))
	}
	if states := taskStates(t, specDir); states[1] != afspec.TaskStateDone || states[2] != afspec.TaskStatePending {
		t.Errorf("states = %v", states)
	}
}

// A report that says one of the task's own tests fails is an honest report
// of a task that is not done: it is not refused, and it is not landed.
func TestSelfReportedFailingTestDoesNotLand(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &scriptedBrain{}
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		sub, err := goodWork(root, task, attempt)
		if task.Id == 1 {
			sub.TestVerdicts[0].Verdict = VerdictFail
			sub.TestVerdicts[0].Evidence = "TestTS091 fails: the banner still reaches stdout under AF_AGENT=1"
		}
		return sub, err
	}
	o := newOptions(ws, g, b)
	o.TaskAttempts = 1
	got, err := Run(context.Background(), o)
	f := failureOf(t, err)
	if f.Category != CategoryUnverified {
		t.Errorf("category = %s: %v", f.Category, err)
	}
	if got.Tasks[0].TestsOutcome != VerdictFail || got.Tasks[0].Verdict != string(checks.VerdictPass) {
		t.Errorf("task 1 = %+v", got.Tasks[0])
	}
	if !strings.Contains(err.Error(), "the report itself says the task is not done") {
		t.Errorf("message = %v", err)
	}
}

// A phase that reports work and changed nothing is not landed.
func TestEmptyChangeIsNotLanded(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &scriptedBrain{}
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		return passingReport(task, "claimed a change"), nil
	}
	o := newOptions(ws, g, b)
	_, err := Run(context.Background(), o)
	if f := failureOf(t, err); f.Category != CategoryEmpty {
		t.Errorf("category = %s: %v", f.Category, err)
	}
	if len(b.inputs) != 2 {
		t.Errorf("%d phases; an empty change gets the second attempt like any other failure", len(b.inputs))
	}
}

// Pre-flight refuses what cannot be implemented before a token is spent.
func TestPreflightRefusals(t *testing.T) {
	t.Run("dirty tree", func(t *testing.T) {
		ws, g, _ := newSpecRepo(t)
		write(t, ws.Root, "loose.txt", "x")
		b := &scriptedBrain{}
		_, err := Run(context.Background(), newOptions(ws, g, b))
		if f := failureOf(t, err); f.Category != "usage" {
			t.Errorf("category = %s: %v", f.Category, err)
		}
		if b.surveys != 0 {
			t.Error("the survey ran on a dirty tree")
		}
	})
	t.Run("sealed spec", func(t *testing.T) {
		ws, g, specDir := newSpecRepo(t)
		spec, _ := afspec.LoadSpec(specDir)
		if _, err := spec.Transition("sealed", specDir); err != nil {
			t.Fatal(err)
		}
		_, _ = g.CommitAll(context.Background(), "chore: seal\n")
		_, err := Run(context.Background(), newOptions(ws, g, &scriptedBrain{}))
		if f := failureOf(t, err); f.Category != "usage" || !strings.Contains(err.Error(), "sealed") {
			t.Errorf("%s: %v", f.Category, err)
		}
	})
	t.Run("draft spec is implemented with a warning", func(t *testing.T) {
		ws, g, specDir := newSpecRepo(t)
		prd, _ := os.ReadFile(filepath.Join(specDir, "prd.md"))
		write(t, specDir, "prd.md", strings.Replace(string(prd), `status: "active"`, `status: "draft"`, 1))
		_, _ = g.CommitAll(context.Background(), "chore: back to draft\n")
		o := newOptions(ws, g, &scriptedBrain{})
		o.Task = 1
		if _, err := Run(context.Background(), o); err != nil {
			t.Fatalf("a draft was refused: %v", err)
		}
		if w := strings.Join(toolio.WarningMessages(o.Run.Warnings()), "\n"); !strings.Contains(w, "draft") {
			t.Errorf("no warning about the draft: %q", w)
		}
	})
	t.Run("invalid spec", func(t *testing.T) {
		ws, g, specDir := newSpecRepo(t)
		raw, _ := os.ReadFile(filepath.Join(specDir, "tasks.json"))
		write(t, specDir, "tasks.json", strings.Replace(string(raw), `"TS-09-2",`, "", 1))
		_, _ = g.CommitAll(context.Background(), "chore: orphan a test\n")
		_, err := Run(context.Background(), newOptions(ws, g, &scriptedBrain{}))
		if f := failureOf(t, err); f.Category != CategoryInvalidSpec || !strings.Contains(err.Error(), "C7") {
			t.Errorf("%s: %v", f.Category, err)
		}
	})
	t.Run("compound test command", func(t *testing.T) {
		ws, g, specDir := newSpecRepo(t)
		raw, _ := os.ReadFile(filepath.Join(specDir, "tasks.json"))
		write(t, specDir, "tasks.json", strings.Replace(string(raw), `"make test"`, `"make test && make lint"`, 1))
		_, _ = g.CommitAll(context.Background(), "chore: compound\n")
		_, err := Run(context.Background(), newOptions(ws, g, &scriptedBrain{}))
		if f := failureOf(t, err); f.Category != "usage" || !strings.Contains(err.Error(), "needs a shell") {
			t.Errorf("%s: %v", f.Category, err)
		}
	})
	t.Run("baseline that cannot run", func(t *testing.T) {
		ws, g, _ := newSpecRepo(t)
		o := newOptions(ws, g, &scriptedBrain{})
		o.VerifyCommand = "definitely-not-a-program-xyz"
		_, err := Run(context.Background(), o)
		if f := failureOf(t, err); f.Category != "usage" || !strings.Contains(err.Error(), "could not run before any change") {
			t.Errorf("%s: %v", f.Category, err)
		}
	})
	t.Run("pull request without a token", func(t *testing.T) {
		ws, g, _ := newSpecRepo(t)
		o := newOptions(ws, g, &scriptedBrain{})
		o.Land = LandPR
		_, err := Run(context.Background(), o)
		if f := failureOf(t, err); f.Category != "auth" {
			t.Errorf("%s: %v", f.Category, err)
		}
	})
	t.Run("unknown reference", func(t *testing.T) {
		ws, g, _ := newSpecRepo(t)
		o := newOptions(ws, g, &scriptedBrain{})
		o.Input.Body = "77"
		_, err := Run(context.Background(), o)
		if f := failureOf(t, err); f.Category != "usage" || !strings.Contains(err.Error(), "09_agent_mode") {
			t.Errorf("%s: %v", f.Category, err)
		}
	})
}

// Nothing to do is a successful run that touches nothing.
func TestCompleteSpecIsReportedAsComplete(t *testing.T) {
	ws, g, specDir := newSpecRepo(t)
	spec, _ := afspec.LoadSpec(specDir)
	spec.Tasks = spec.Tasks.CompleteTaskStates([]int{1, 2, 3})
	if err := saveTasks(spec, specDir); err != nil {
		t.Fatal(err)
	}
	_, _ = g.CommitAll(context.Background(), "chore: all done\n")
	b := &scriptedBrain{}
	got, err := Run(context.Background(), newOptions(ws, g, b))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Stage != "complete" || got.TasksSkipped != 3 || b.surveys != 0 {
		t.Errorf("Stage=%q skipped=%d surveys=%d", got.Stage, got.TasksSkipped, b.surveys)
	}
	if g.LocalBranchExists(context.Background(), "impl/09-agent-mode-spec-cli") {
		t.Error("a branch was created for nothing")
	}
}

// The run-level budget stops the run between tasks with the landed work
// committed and the checkout back on the base branch.
func TestTotalBudgetStopsBetweenTasks(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &costlyBrain{scriptedBrain: &scriptedBrain{}, cost: 3}
	o := newOptions(ws, g, b.scriptedBrain)
	o.brain = b
	o.NoSurvey = true
	o.TotalBudgetUSD = 5
	got, err := Run(context.Background(), o)
	if f := failureOf(t, err); f.Category != agentrun.CategoryBudget {
		t.Errorf("%s: %v", f.Category, err)
	}
	if got.TasksDone != 2 || got.Stage != "stopped" || got.CostUSD != 6 {
		t.Errorf("done=%d stage=%q cost=%v", got.TasksDone, got.Stage, got.CostUSD)
	}
	if cur := gitOut(t, ws.Root, "rev-parse", "--abbrev-ref", "HEAD"); cur != "main" {
		t.Errorf("checked out %q", cur)
	}
}

// costlyBrain charges a fixed amount per phase.
type costlyBrain struct {
	*scriptedBrain
	cost float64
}

func (b *costlyBrain) Implement(ctx context.Context, in taskInput) (Submission, agentrun.Result, error) {
	sub, res, err := b.scriptedBrain.Implement(ctx, in)
	res.Usage.CostUSD = b.cost
	return sub, res, err
}

// --repair on a red baseline: the repair phase runs once, before the first
// task, its commit is the first on the branch, and the tasks are then
// compared against the green gate it produced.
func TestRepairFixesARedBaselineBeforeTheFirstTask(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	write(t, ws.Root, "FAIL", "the suite is red")
	if _, err := g.CommitAll(context.Background(), "chore: break the build\n"); err != nil {
		t.Fatal(err)
	}
	b := &scriptedBrain{}
	b.repair = func(root string, attempt int) (RepairSubmission, error) {
		if err := os.Remove(filepath.Join(root, "FAIL")); err != nil {
			return RepairSubmission{}, err
		}
		return RepairSubmission{
			Cause:         "A FAIL marker was committed, and the test target refuses to run while it exists.",
			Summary:       "Removed the marker.",
			CommitSubject: "remove the FAIL marker the test target trips on.",
			Changes:       []FileChange{{Path: "FAIL", Change: "deleted"}},
		}, nil
	}
	o := newOptions(ws, g, b)
	o.Repair = true
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Stage != "landed" || got.TasksDone != 3 {
		t.Errorf("Stage=%q done=%d", got.Stage, got.TasksDone)
	}
	if len(b.repairIns) != 1 || len(b.inputs) != 3 {
		t.Fatalf("%d repair phases and %d task phases", len(b.repairIns), len(b.inputs))
	}
	in := b.repairIns[0]
	if in.Attempt != 1 || in.Previous != nil || in.Survey == nil || in.Branch != got.Branch {
		t.Errorf("repair input: %+v", in)
	}
	if len(in.Failing.failing()) != 1 || in.Failing.failing()[0].Command != "make test" {
		t.Errorf("the repair was not told what failed: %+v", in.Failing)
	}
	if !b.surveyIn.Repair || !strings.Contains(surveyPrompt(b.surveyIn), "will repair the failing checks") {
		t.Error("the survey was not told the checks will be repaired")
	}

	r := got.Repair
	if r == nil || r.Outcome != OutcomeDone || r.Attempts != 1 || r.Commit == "" || r.Model != "scripted-repair" {
		t.Fatalf("Repair = %+v", r)
	}
	if len(r.ChangedFiles) != 1 || r.ChangedFiles[0] != "FAIL" || r.Verification == nil || !r.Verification.OK() {
		t.Errorf("Repair facts = %+v", r)
	}
	// The run's baseline is still the red one — the truth about where the
	// branch started — and the first task was judged against green.
	if got.Baseline.OK() {
		t.Error("the result's baseline was overwritten by the repaired gate")
	}
	if !b.inputs[0].Baseline.OK() {
		t.Error("task 1 was not compared with the repaired gate")
	}
	if got.Tasks[0].Verdict != string(checks.VerdictPass) {
		t.Errorf("task 1 verdict = %q, want pass against the repaired baseline", got.Tasks[0].Verdict)
	}

	log := gitOut(t, ws.Root, "log", "--format=%s", "main..HEAD")
	want := "feat: implemented task 3\nfeat: implemented task 2\nfeat: implemented task 1\n" +
		"fix: remove the FAIL marker the test target trips on"
	if log != want {
		t.Errorf("log =\n%s\nwant\n%s", log, want)
	}
	body := gitOut(t, ws.Root, "log", "-1", "--format=%B", "HEAD~3")
	if !strings.Contains(body, "A FAIL marker was committed") || !strings.Contains(body, "Spec: 09_agent_mode, repair") {
		t.Errorf("the repair commit lacks the cause or the trailer:\n%s", body)
	}
	if !strings.Contains(pullRequestBody(got), "## The checks were repaired first") {
		t.Error("the pull request body does not mention the repair")
	}
}

// A repair that never makes the checks pass is retried with the failure
// in its prompt, then parked, and no task is implemented.
func TestRepairThatCannotFixTheChecksParksAndStops(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	write(t, ws.Root, "FAIL", "")
	if _, err := g.CommitAll(context.Background(), "chore: break the build\n"); err != nil {
		t.Fatal(err)
	}
	b := &scriptedBrain{}
	b.repair = func(root string, attempt int) (RepairSubmission, error) {
		write(t, root, "attempt"+itoa(attempt)+".go", "package x\n")
		return RepairSubmission{Cause: "no idea", Summary: "tried something", CommitSubject: "try something",
			Changes: []FileChange{{Path: "attempt.go", Change: "added"}}}, nil
	}
	o := newOptions(ws, g, b)
	o.Repair = true
	o.RepairAttempts = 2
	got, err := Run(context.Background(), o)
	f := failureOf(t, err)
	if f.Category != CategoryUnverified || f.Stage != PhaseRepair {
		t.Errorf("failure = %s/%s: %v", f.Stage, f.Category, err)
	}
	if !strings.Contains(err.Error(), "no task was implemented") {
		t.Errorf("the error does not say the tasks were not started: %v", err)
	}
	if got.Stage != "parked" || got.TasksDone != 0 || len(b.inputs) != 0 {
		t.Errorf("Stage=%q done=%d task phases=%d", got.Stage, got.TasksDone, len(b.inputs))
	}
	if len(b.repairIns) != 2 {
		t.Fatalf("%d repair phases, want 2", len(b.repairIns))
	}
	second := b.repairIns[1]
	if second.Attempt != 2 || second.Previous == nil || second.Previous.Gate == nil {
		t.Fatalf("the second attempt did not get the first one's failure: %+v", second.Previous)
	}
	if p := repairPrompt(second); !strings.Contains(p, "The previous attempt at the repair") ||
		!strings.Contains(p, "the checks still do not pass (still_failing)") {
		t.Error("the retry prompt does not carry the first attempt's failure")
	}
	if _, err := os.Stat(filepath.Join(ws.Root, "attempt1.go")); !os.IsNotExist(err) {
		t.Error("the first attempt's file survived into the second attempt's tree")
	}
	r := got.Repair
	if r == nil || r.Outcome != OutcomeUnverified || r.Attempts != 2 || r.Commit == "" {
		t.Fatalf("Repair = %+v", r)
	}
	if cur := gitOut(t, ws.Root, "rev-parse", "--abbrev-ref", "HEAD"); cur != "main" {
		t.Errorf("checked out %q, want main", cur)
	}
	subject := gitOut(t, ws.Root, "log", "-1", "--format=%s", got.Branch)
	if !strings.HasPrefix(subject, "wip: the repair of the checks before 09_agent_mode did not land") {
		t.Errorf("parked commit subject = %q", subject)
	}

	// A second run discards the parked repair and starts it again.
	b2 := &scriptedBrain{}
	b2.repair = func(root string, attempt int) (RepairSubmission, error) {
		if _, err := os.Stat(filepath.Join(root, "attempt2.go")); err == nil {
			t.Error("the parked attempt was not discarded before the second run")
		}
		if err := os.Remove(filepath.Join(root, "FAIL")); err != nil {
			return RepairSubmission{}, err
		}
		return RepairSubmission{Cause: "the marker", Summary: "removed it", CommitSubject: "remove the marker",
			Changes: []FileChange{{Path: "FAIL", Change: "deleted"}}}, nil
	}
	o2 := newOptions(ws, g, b2)
	o2.Repair = true
	got2, err := Run(context.Background(), o2)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !got2.Resumed || got2.TasksDone != 3 || got2.Repair == nil || got2.Repair.Outcome != OutcomeDone {
		t.Errorf("second run: resumed=%v done=%d repair=%+v", got2.Resumed, got2.TasksDone, got2.Repair)
	}
	if log := gitOut(t, ws.Root, "log", "--format=%s", "main..HEAD"); strings.Contains(log, "wip:") {
		t.Errorf("the parked repair is still on the branch:\n%s", log)
	}
}

// A repair blocker — the failure is not in the code — parks the attempt
// and exits as blocked, not as unverified.
func TestRepairBlockerAsksAPerson(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	write(t, ws.Root, "FAIL", "")
	if _, err := g.CommitAll(context.Background(), "chore: break the build\n"); err != nil {
		t.Fatal(err)
	}
	b := &scriptedBrain{}
	b.repair = func(root string, attempt int) (RepairSubmission, error) {
		return RepairSubmission{Blocker: &Blocker{Reason: "the suite needs a running Postgres", Needed: "start one"}}, nil
	}
	o := newOptions(ws, g, b)
	o.Repair = true
	got, err := Run(context.Background(), o)
	if f := failureOf(t, err); f.Category != CategoryBlocked {
		t.Errorf("category = %s: %v", f.Category, err)
	}
	if got.Blocker == nil || got.Repair == nil || got.Repair.Outcome != OutcomeBlocked || len(b.repairIns) != 1 {
		t.Errorf("Blocker=%v Repair=%+v phases=%d", got.Blocker, got.Repair, len(b.repairIns))
	}
	if len(b.inputs) != 0 {
		t.Error("a task was implemented after the repair blocked")
	}
}

// A green baseline has nothing to repair: the flag is a no-op and the
// phase never runs. Without the flag a red baseline is not repaired either.
func TestRepairRunsOnlyOnARedBaselineWithTheFlag(t *testing.T) {
	t.Run("green baseline", func(t *testing.T) {
		ws, g, _ := newSpecRepo(t)
		b := &scriptedBrain{} // repair unscripted: it fails the run if it runs
		o := newOptions(ws, g, b)
		o.Repair = true
		got, err := Run(context.Background(), o)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got.Repair != nil || len(b.repairIns) != 0 || got.TasksDone != 3 {
			t.Errorf("Repair=%+v phases=%d done=%d", got.Repair, len(b.repairIns), got.TasksDone)
		}
	})
	t.Run("red baseline without the flag", func(t *testing.T) {
		ws, g, _ := newSpecRepo(t)
		write(t, ws.Root, "FAIL", "")
		if _, err := g.CommitAll(context.Background(), "chore: break the build\n"); err != nil {
			t.Fatal(err)
		}
		b := &scriptedBrain{}
		got, err := Run(context.Background(), newOptions(ws, g, b))
		// The marker persists, so make test fails before and after task 1:
		// still_failing, which is not landable. The run parks, and never
		// tried to repair.
		if err == nil {
			t.Fatal("a red-before-and-after task landed")
		}
		if got.Repair != nil || len(b.repairIns) != 0 {
			t.Errorf("the repair ran without the flag: %+v", got.Repair)
		}
	})
}

// With --repair, the integration task's red checks are repaired on top of
// its work rather than retried from scratch, and task and fix land as one
// commit.
func TestRepairFixesTheChecksAfterTheIntegrationTask(t *testing.T) {
	ws, g, specDir := newSpecRepo(t)
	b := &scriptedBrain{}
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		if task.Kind == afspec.TaskKindIntegration {
			write(t, root, "FAIL", "the smoke tests found a wiring gap")
		}
		return goodWork(root, task, attempt)
	}
	b.repair = func(root string, attempt int) (RepairSubmission, error) {
		if err := os.Remove(filepath.Join(root, "FAIL")); err != nil {
			return RepairSubmission{}, err
		}
		write(t, root, "wiring.go", "package x // the gap\n")
		return RepairSubmission{
			Cause:         "Task 1's parser never registered its command, which only the smoke test exercises.",
			Summary:       "Registered it.",
			CommitSubject: "register the parser command",
			Changes:       []FileChange{{Path: "wiring.go", Change: "added"}, {Path: "FAIL", Change: "deleted"}},
		}, nil
	}
	o := newOptions(ws, g, b)
	o.Repair = true
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Stage != "landed" || got.TasksDone != 3 || got.Repair != nil {
		t.Errorf("Stage=%q done=%d baseline repair=%+v", got.Stage, got.TasksDone, got.Repair)
	}
	if len(b.inputs) != 3 || len(b.repairIns) != 1 {
		t.Fatalf("%d task phases and %d repair phases", len(b.inputs), len(b.repairIns))
	}
	in := b.repairIns[0]
	if in.Task == nil || in.Task.Id != 3 || len(in.Prior) != 2 || len(in.Failing.failing()) != 1 {
		t.Errorf("repair input: task=%v prior=%d failing=%d", in.Task, len(in.Prior), len(in.Failing.failing()))
	}
	if p := repairPrompt(in); !strings.Contains(p, "Where the work stands") || !strings.Contains(p, "after the task") {
		t.Error("the prompt does not describe the post-task situation")
	}
	r := got.Tasks[2]
	if r.Outcome != OutcomeDone || r.Attempts != 1 || r.Repair == nil || r.Repair.Outcome != OutcomeDone ||
		r.Repair.Failing == nil || r.Repair.Failing.OK() || r.Verdict != string(checks.VerdictPass) {
		t.Fatalf("task 3 = %+v repair=%+v", r, r.Repair)
	}

	log := gitOut(t, ws.Root, "log", "--format=%s", "main..HEAD")
	if want := "feat: implemented task 3\nfeat: implemented task 2\nfeat: implemented task 1"; log != want {
		t.Errorf("log =\n%s\nwant\n%s", log, want)
	}
	body := gitOut(t, ws.Root, "log", "-1", "--format=%B")
	if !strings.Contains(body, "were repaired in the same commit. Task 1's parser") {
		t.Errorf("the commit body does not carry the repair:\n%s", body)
	}
	shown := gitOut(t, ws.Root, "show", "--stat", "--format=", "HEAD")
	for _, f := range []string{"task3.go", "wiring.go", "tasks.json"} {
		if !strings.Contains(shown, f) {
			t.Errorf("the commit lacks %s:\n%s", f, shown)
		}
	}
	if _, err := os.Stat(filepath.Join(ws.Root, "FAIL")); !os.IsNotExist(err) {
		t.Error("the FAIL marker survived")
	}
	if states := taskStates(t, specDir); states[3] != afspec.TaskStateDone {
		t.Errorf("task 3 is %s", states[3])
	}
	if dirty, _ := g.DirtyFiles(context.Background()); len(dirty) != 0 {
		t.Errorf("tree is dirty after the run: %v", dirty)
	}
	if !strings.Contains(pullRequestBody(got), "failed after this task and were repaired") {
		t.Error("the pull request body does not mention the repair")
	}
}

// A repair after the integration task that never gets green parks task
// and attempt together as one wip: commit; the next run discards it and
// starts the task again.
func TestRepairAfterTheIntegrationTaskParksWhenItCannotFix(t *testing.T) {
	ws, g, specDir := newSpecRepo(t)
	b := &scriptedBrain{}
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		if task.Kind == afspec.TaskKindIntegration {
			write(t, root, "FAIL", "")
		}
		return goodWork(root, task, attempt)
	}
	b.repair = func(root string, attempt int) (RepairSubmission, error) {
		write(t, root, "guess"+itoa(attempt)+".go", "package x\n")
		return RepairSubmission{Cause: "unclear", Summary: "guessed", CommitSubject: "guess",
			Changes: []FileChange{{Path: "guess.go", Change: "added"}}}, nil
	}
	o := newOptions(ws, g, b)
	o.Repair = true
	o.RepairAttempts = 2
	got, err := Run(context.Background(), o)
	f := failureOf(t, err)
	if f.Category != CategoryUnverified || f.Stage != "verify" {
		t.Errorf("failure = %s/%s: %v", f.Stage, f.Category, err)
	}
	if got.Stage != "parked" || got.TasksDone != 2 || len(b.repairIns) != 2 || len(b.inputs) != 3 {
		t.Errorf("Stage=%q done=%d repairs=%d tasks=%d", got.Stage, got.TasksDone, len(b.repairIns), len(b.inputs))
	}
	r := got.Tasks[2]
	if r.Outcome != OutcomeUnverified || r.Repair == nil || r.Repair.Outcome != OutcomeUnverified || r.Repair.Attempts != 2 ||
		!strings.Contains(r.Error, "could not be repaired") {
		t.Errorf("task 3 = %+v repair=%+v", r, r.Repair)
	}
	if second := b.repairIns[1]; second.Previous == nil || second.Previous.Gate == nil {
		t.Error("the second repair attempt did not get the first one's failure")
	}
	if cur := gitOut(t, ws.Root, "rev-parse", "--abbrev-ref", "HEAD"); cur != "main" {
		t.Errorf("checked out %q, want main", cur)
	}
	log := gitOut(t, ws.Root, "log", "--format=%s", "main.."+got.Branch)
	if want := "wip: task 3 of 09_agent_mode did not land\nfeat: implemented task 2\nfeat: implemented task 1"; log != want {
		t.Errorf("log =\n%s\nwant\n%s", log, want)
	}
	shown := gitOut(t, ws.Root, "show", "--stat", "--format=", got.Branch)
	if !strings.Contains(shown, "task3.go") || !strings.Contains(shown, "guess2.go") || strings.Contains(shown, "guess1.go") {
		t.Errorf("the parked commit should hold the task's work and the last attempt only:\n%s", shown)
	}
	if states := taskStates(t, specDir); states[3] != afspec.TaskStatePending {
		// On the base branch the file is untouched; the branch records in_progress.
		t.Errorf("task 3 is %s on %s", states[3], "main")
	}

	// The next run discards the parked commit, implements task 3 again,
	// and this time the repair succeeds.
	b2 := &scriptedBrain{implement: b.implement}
	b2.repair = func(root string, attempt int) (RepairSubmission, error) {
		if _, err := os.Stat(filepath.Join(root, "guess2.go")); err == nil {
			t.Error("the parked attempt was not discarded")
		}
		if err := os.Remove(filepath.Join(root, "FAIL")); err != nil {
			return RepairSubmission{}, err
		}
		return RepairSubmission{Cause: "the marker", Summary: "removed it", CommitSubject: "remove the marker",
			Changes: []FileChange{{Path: "FAIL", Change: "deleted"}}}, nil
	}
	o2 := newOptions(ws, g, b2)
	o2.Repair = true
	got2, err := Run(context.Background(), o2)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !got2.Resumed || got2.TasksDone != 1 || got2.TasksSkipped != 2 || got2.Tasks[2].Repair == nil {
		t.Errorf("second run: resumed=%v done=%d skipped=%d repair=%+v", got2.Resumed, got2.TasksDone,
			got2.TasksSkipped, got2.Tasks[2].Repair)
	}
	if log := gitOut(t, ws.Root, "log", "--format=%s", "main..HEAD"); strings.Contains(log, "wip:") {
		t.Errorf("a wip commit remains:\n%s", log)
	}
}

// Only the integration task's failure is repaired: an earlier task that
// breaks the checks is retried from scratch, flag or no flag.
func TestRepairDoesNotApplyToAnEarlierTask(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &scriptedBrain{}
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		if task.Id == 2 {
			write(t, root, "FAIL", "")
			return passingReport(task, "break the build"), nil
		}
		return goodWork(root, task, attempt)
	}
	o := newOptions(ws, g, b)
	o.Repair = true
	got, err := Run(context.Background(), o)
	if f := failureOf(t, err); f.Category != CategoryUnverified {
		t.Errorf("category = %s: %v", f.Category, err)
	}
	if len(b.repairIns) != 0 || got.Tasks[1].Repair != nil || got.Tasks[1].Attempts != 2 {
		t.Errorf("task 2 was repaired instead of retried: repairs=%d report=%+v", len(b.repairIns), got.Tasks[1])
	}
}

// mockAuthClient implements issuex.Client with configurable authentication and pull request creation.
type mockAuthClient struct {
	issuex.NoOpClient
	authenticated bool
	createdPR     issuex.PullRequest
	createPRErr   error
	capturedReq   issuex.CreatePullRequestRequest
	capturedRepo  issuex.Repo
	comments      []string
	commentRefs   []issuex.IssueRef
	commentErr    error
}

func (m *mockAuthClient) PostReviewComment(ctx context.Context, ref issuex.IssueRef, body string) error {
	if m.commentErr != nil {
		return m.commentErr
	}
	m.commentRefs = append(m.commentRefs, ref)
	m.comments = append(m.comments, body)
	return nil
}

func (m *mockAuthClient) Authenticated() bool {
	return m.authenticated
}

func (m *mockAuthClient) CreatePullRequest(ctx context.Context, repo issuex.Repo, req issuex.CreatePullRequestRequest) (issuex.PullRequest, error) {
	m.capturedRepo = repo
	m.capturedReq = req
	if m.createPRErr != nil {
		return issuex.PullRequest{}, m.createPRErr
	}
	return m.createdPR, nil
}

// TS-04-18 (unit): codeimpl declares forge-neutral Options and CategoryForge constant
// Verifies: 04-REQ-5.1
func TestTS0418_ForgeNeutralOptions(t *testing.T) {
	var o Options
	var _ issuex.Repo = o.Repo
	var _ issuex.Client = o.Forge
	if CategoryForge != "forge" {
		t.Errorf("CategoryForge = %q, want %q", CategoryForge, "forge")
	}
	if CategoryGitHub != CategoryForge {
		t.Errorf("CategoryGitHub = %q, want %q", CategoryGitHub, CategoryForge)
	}
}

// TS-04-19 (integration): codeimpl preflight resolves target repository via issuex.DetectRepo
// Verifies: 04-REQ-5.2
func TestTS0419_PreflightDetectsRepo(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	ctx := context.Background()

	// Add origin remote to the repo
	if out, code, err := gitx.ExecRunner(ctx, ws.Root, []string{
		"git", "remote", "add", "origin", "https://github.com/acme/proj.git",
	}); err != nil || code != 0 {
		t.Fatalf("git remote add: %v (%d) %s", err, code, out)
	}

	opts := newOptions(ws, g, &scriptedBrain{})
	opts.Repo = issuex.Repo{} // target repository unspecified
	result := &Result{Stage: "preflight"}

	st, err := Preflight(ctx, opts, result)
	if err != nil {
		t.Fatalf("Preflight failed: %v", err)
	}
	if !st.Target.Valid() {
		t.Errorf("st.Target is invalid: %+v", st.Target)
	}
	if st.Target.Owner != "acme" || st.Target.Name != "proj" {
		t.Errorf("st.Target = %+v, want Owner: acme, Name: proj", st.Target)
	}
}

// TS-04-20 (unit): codeimpl preflight returns auth failure when unauthenticated for LandPR
// Verifies: 04-REQ-5.3
func TestTS0420_PreflightAuthFailure(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	ctx := context.Background()

	opts := newOptions(ws, g, &scriptedBrain{})
	opts.Land = LandPR
	opts.DryRun = false
	opts.Forge = &mockAuthClient{authenticated: false}
	result := &Result{Stage: "preflight"}

	_, err := Preflight(ctx, opts, result)
	if err == nil {
		t.Fatal("expected preflight auth failure, got nil")
	}
	if err.StageName() != "preflight" {
		t.Errorf("StageName = %q, want %q", err.StageName(), "preflight")
	}
	if err.CategoryName() != "auth" {
		t.Errorf("CategoryName = %q, want %q", err.CategoryName(), "auth")
	}
	if !strings.Contains(err.Error(), "GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN") {
		t.Errorf("error %q should mention GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN", err.Error())
	}
}

// TS-04-21 (unit): codeimpl preflight returns usage failure when target repository cannot be resolved
// Verifies: 04-REQ-5.4
func TestTS0421_PreflightTargetUsageFailure(t *testing.T) {
	ws, g, _ := newSpecRepo(t) // no origin remote
	ctx := context.Background()

	opts := newOptions(ws, g, &scriptedBrain{})
	opts.Land = LandPR
	opts.DryRun = false
	opts.Forge = &mockAuthClient{authenticated: true}
	opts.Repo = issuex.Repo{}
	result := &Result{Stage: "preflight"}

	_, err := Preflight(ctx, opts, result)
	if err == nil {
		t.Fatal("expected usage failure for unresolved target repo under LandPR, got nil")
	}
	if err.StageName() != "preflight" {
		t.Errorf("StageName = %q, want %q", err.StageName(), "preflight")
	}
	if err.CategoryName() != "usage" {
		t.Errorf("CategoryName = %q, want %q", err.CategoryName(), "usage")
	}
	if !strings.Contains(err.Error(), "no origin remote on a recognized forge") {
		t.Errorf("error %q should mention 'no origin remote on a recognized forge'", err.Error())
	}
}

// TS-04-22 (integration): codeimpl lands changes via Forge.CreatePullRequest
// Verifies: 04-REQ-5.5
func TestTS0422_LandPRChanges(t *testing.T) {
	ctx := context.Background()
	mockForge := &mockAuthClient{
		authenticated: true,
		createdPR: issuex.PullRequest{
			URL:    "https://gitlab.com/grp/prj/-/merge_requests/4",
			Number: 4,
		},
	}
	opts := Options{
		Land:  LandPR,
		Forge: mockForge,
		Draft: true,
	}
	target := issuex.Repo{Owner: "grp", Name: "prj", Host: "gitlab.com"}
	st := &RunState{
		Target: target,
		target: target,
		branch: "impl/09-test",
		base:   "main",
		spec: &afspec.Spec{
			SpecID: "09",
			Title:  "Agent Mode Spec CLI",
		},
	}
	result := &Result{
		TasksDone: 1,
	}
	res, err := LandPRChanges(ctx, opts, st, result)
	if err != nil {
		t.Fatalf("LandPRChanges: %v", err)
	}
	if res.PullRequestURL != "https://gitlab.com/grp/prj/-/merge_requests/4" {
		t.Errorf("PullRequestURL = %q, want https://gitlab.com/grp/prj/-/merge_requests/4", res.PullRequestURL)
	}
	if res.PullRequestNumber != 4 {
		t.Errorf("PullRequestNumber = %d, want 4", res.PullRequestNumber)
	}
	if res.Stage != "landed" {
		t.Errorf("Stage = %q, want 'landed'", res.Stage)
	}
	if mockForge.capturedRepo != target {
		t.Errorf("capturedRepo = %+v, want %+v", mockForge.capturedRepo, target)
	}
	if mockForge.capturedReq.Head != "impl/09-test" || mockForge.capturedReq.Base != "main" || !mockForge.capturedReq.Draft {
		t.Errorf("capturedReq = %+v", mockForge.capturedReq)
	}
}

// TS-06-27 (unit): LandPRChanges records open_pr on "<owner>/<repo>#<n>"
// when the forge answers.
func TestTS06_27_LandPRChangesRecordsOpenPR(t *testing.T) {
	target := issuex.Repo{Owner: "grp", Name: "prj", Host: "gitlab.com"}
	st := &RunState{Target: target, target: target, branch: "impl/09-test", base: "main",
		spec: &afspec.Spec{SpecID: "09", Title: "Agent Mode Spec CLI"}}
	opts := Options{Land: LandPR, Run: toolio.NewRun("impl", "test"),
		Forge: &mockAuthClient{authenticated: true, createdPR: issuex.PullRequest{URL: "https://gitlab.com/grp/prj/-/merge_requests/4", Number: 4}}}
	if _, err := LandPRChanges(context.Background(), opts, st, &Result{TasksDone: 1}); err != nil {
		t.Fatal(err)
	}
	se := opts.Run.SideEffects()
	if len(se) != 1 || se[0].Action != "open_pr" || se[0].Target != "grp/prj#4" || !se[0].OK || se[0].Warning != "" {
		t.Fatalf("SideEffects = %+v", se)
	}
}

// TS-06-28 (unit): a pull request that could not be opened, for either
// reason the site warns about, is recorded ok:false with the same WarnCode.
func TestTS06_28_LandPRChangesFailureSharesTheWarnCode(t *testing.T) {
	target := issuex.Repo{Owner: "grp", Name: "prj", Host: "gitlab.com"}
	st := &RunState{Target: target, target: target, branch: "impl/09-test", base: "main",
		spec: &afspec.Spec{SpecID: "09", Title: "Agent Mode Spec CLI"}}
	for name, forge := range map[string]issuex.Client{
		"refused":   &mockAuthClient{authenticated: true, createPRErr: errors.New("403")},
		"no client": nil,
	} {
		run := toolio.NewRun("impl", "test")
		opts := Options{Land: LandPR, Run: run, Forge: forge}
		if _, err := LandPRChanges(context.Background(), opts, st, &Result{}); err == nil {
			t.Fatalf("%s: want an error", name)
		}
		se := run.SideEffects()
		if len(se) != 1 || se[0].OK || se[0].Action != "open_pr" || se[0].Target != "grp/prj" ||
			se[0].Warning != toolio.WarnPullRequestNotOpened {
			t.Fatalf("%s: SideEffects = %+v", name, se)
		}
		if len(run.Warnings()) != 1 || run.Warnings()[0].Code != se[0].Warning {
			t.Errorf("%s: warnings %+v disagree with %+v", name, run.Warnings(), se)
		}
	}
}

// implOriginFixture gives a real spec repository a bare origin, so the
// pipeline's own push runs for real.
func implOriginFixture(t *testing.T, originPath string) Options {
	t.Helper()
	ws, g, _ := newSpecRepo(t)
	ctx := context.Background()
	for _, argv := range [][]string{
		{"git", "init", "-q", "--bare", "-b", "main", originPath},
		{"git", "remote", "add", "origin", originPath},
	} {
		if out, code, err := gitx.ExecRunner(ctx, ws.Root, argv); err != nil || code != 0 {
			t.Fatalf("%v: %v (%d) %s", argv, err, code, out)
		}
	}
	o := newOptions(ws, g, &scriptedBrain{})
	o.Repo = issuex.Repo{Owner: "grp", Name: "prj", Host: "github.com"}
	o.Forge = &mockAuthClient{authenticated: true,
		createdPR: issuex.PullRequest{URL: "https://github.com/grp/prj/pull/3", Number: 3}}
	o.Land = LandPR
	o.PushAttempts = 1
	return o
}

// TS-06-27 / TS-06-29 (integration): a real impl run records its push and
// then its pull request, in that order.
func TestTS06_29_ImplRecordsPushThenOpenPR(t *testing.T) {
	o := implOriginFixture(t, filepath.Join(t.TempDir(), "origin.git"))
	res, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	se := o.Run.SideEffects()
	if len(se) != 2 || se[0].Action != "push" || se[1].Action != "open_pr" {
		t.Fatalf("SideEffects = %+v", se)
	}
	if se[0].Target != "origin "+res.Branch || !se[0].OK {
		t.Errorf("push entry = %+v, branch %q", se[0], res.Branch)
	}
	if se[1].Target != "grp/prj#3" || !se[1].OK {
		t.Errorf("open_pr entry = %+v", se[1])
	}
}

// TS-06-28 (integration): a failed push is recorded ok:false, with no
// warning code because the push site records no Run.Warn.
func TestTS06_28_ImplFailedPushIsRecordedNotOK(t *testing.T) {
	o := implOriginFixture(t, filepath.Join(t.TempDir(), "origin.git"))
	if out, code, err := gitx.ExecRunner(context.Background(), o.Workspace.Root,
		[]string{"git", "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git")}); err != nil || code != 0 {
		t.Fatalf("set-url: %v %s", err, out)
	}
	if _, err := Run(context.Background(), o); err == nil {
		t.Fatal("want the push to fail the run")
	}
	se := o.Run.SideEffects()
	if len(se) != 1 || se[0].Action != "push" || se[0].OK || se[0].Warning != "" {
		t.Fatalf("SideEffects = %+v", se)
	}
}

// TS-06-30 (integration): --dry-run --land=pr records no side effect.
func TestTS06_30_ImplDryRunRecordsNoSideEffects(t *testing.T) {
	o := implOriginFixture(t, filepath.Join(t.TempDir(), "origin.git"))
	o.DryRun = true
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if se := o.Run.SideEffects(); len(se) != 0 {
		t.Errorf("dry run recorded %+v", se)
	}
}

// TS-05-17 (integration): checkUpstream sets Result.Blocker naming the dependency and the upstream package when a dependency is neither sealed nor done
func TestTS05_17_CheckUpstreamSetsBlocker(t *testing.T) {
	ws, g, specDirB := newSpecRepo(t)
	root := ws.Root

	// specDirB is 09_agent_mode. We will make a new upstream spec 08_upstream
	specDirA := filepath.Join(root, ".specs", "08_upstream")
	if err := os.MkdirAll(specDirA, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join("..", "testdata", "v2_example")
	for _, name := range []string{"prd.md", "requirements.json", "test_spec.json", "tasks.json"} {
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "prd.md" {
			b = bytes.ReplaceAll(b, []byte(`spec_id: "09"`), []byte(`spec_id: "08"`))
			b = bytes.ReplaceAll(b, []byte("agent_mode"), []byte("upstream"))
		}
		if name == "requirements.json" {
			b = bytes.ReplaceAll(b, []byte(`"spec_id": "09"`), []byte(`"spec_id": "08"`))
			b = bytes.ReplaceAll(b, []byte("09-"), []byte("08-"))
			b = bytes.ReplaceAll(b, []byte("agent_mode"), []byte("upstream"))
		}
		if name == "test_spec.json" {
			b = bytes.ReplaceAll(b, []byte(`"spec_id": "09"`), []byte(`"spec_id": "08"`))
			b = bytes.ReplaceAll(b, []byte("TS-09-"), []byte("TS-08-"))
			b = bytes.ReplaceAll(b, []byte("09-"), []byte("08-"))
		}
		if name == "tasks.json" {
			b = bytes.ReplaceAll(b, []byte("09-"), []byte("08-"))
			b = bytes.ReplaceAll(b, []byte("TS-09-"), []byte("TS-08-"))
			var doc map[string]any
			if err := json.Unmarshal(b, &doc); err != nil {
				t.Fatal(err)
			}
			doc["spec"] = "08"
			doc["spec_id"] = "08"
			doc["test_commands"] = map[string]any{"all_tests": "make test", "linter": "make lint"}
			// The example's touches name cmd/spec files the scripted work
			// never writes; a spec without them does not restrict the change,
			// and the scope check has a test of its own.
			for _, task := range doc["tasks"].([]any) {
				delete(task.(map[string]any), "touches")
			}
			if b, err = json.MarshalIndent(doc, "", "  "); err != nil {
				t.Fatal(err)
			}
		}
		write(t, specDirA, name, string(b))
	}
	specA, err := afspec.LoadSpec(specDirA)
	if err != nil {
		t.Fatal(err)
	}
	// Transition specA to active
	if _, err := specA.Transition("active", specDirA); err != nil {
		t.Fatal(err)
	}
	// Keep at least one task undone in specA (they are all pending by default)

	// Now configure specB (09) to depend on 08
	tasksBFile := filepath.Join(specDirB, "tasks.json")
	bB, err := os.ReadFile(tasksBFile)
	if err != nil {
		t.Fatal(err)
	}
	var docB map[string]any
	if err := json.Unmarshal(bB, &docB); err != nil {
		t.Fatal(err)
	}
	docB["dependencies"] = []map[string]any{
		{"spec": "08", "reason": "needs upstream foundation"},
	}
	bB, err = json.MarshalIndent(docB, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	write(t, specDirB, "tasks.json", string(bB))

	ctx := context.Background()
	if _, err := g.CommitAll(ctx, "chore: add upstream spec and dependency\n"); err != nil {
		t.Fatal(err)
	}

	opts := newOptions(ws, g, &scriptedBrain{})
	opts.Input = toolio.Input{Kind: toolio.KindText, Origin: "argument", Body: "09"}

	result, err := Run(ctx, opts)
	if err == nil {
		t.Fatal("expected Run to fail with blocked failure, got nil error")
	}

	var f *Failure
	if !errors.As(err, &f) || f.Category != CategoryBlocked {
		t.Fatalf("expected CategoryBlocked, got err = %v", err)
	}
	if result == nil || result.Blocker == nil {
		t.Fatalf("expected result.Blocker to be set, got %+v", result)
	}
	if !strings.Contains(result.Blocker.Reason, "08") {
		t.Errorf("expected Blocker.Reason to contain '08', got %q", result.Blocker.Reason)
	}
	if !strings.Contains(result.Blocker.Needed, "08_upstream") {
		t.Errorf("expected Blocker.Needed to contain '08_upstream', got %q", result.Blocker.Needed)
	}

	// Verify that toolio.Envelope populates needs_human from this Result with ExitNeedsHuman
	run := toolio.NewRun("impl", "v1")
	env := run.Envelope(toolio.ExitNeedsHuman, result, &toolio.ErrorInfo{Stage: f.Stage, Category: f.Category})
	if env.NeedsHuman == nil {
		t.Fatal("expected env.NeedsHuman to be non-nil")
	}
	if env.NeedsHuman.Question != result.Blocker.Reason {
		t.Errorf("expected NeedsHuman.Question == %q, got %q", result.Blocker.Reason, env.NeedsHuman.Question)
	}
	if env.NeedsHuman.Needed != result.Blocker.Needed {
		t.Errorf("expected NeedsHuman.Needed == %q, got %q", result.Blocker.Needed, env.NeedsHuman.Needed)
	}
	if len(env.NeedsHuman.Options) != 0 {
		t.Errorf("expected no options, got %v", env.NeedsHuman.Options)
	}
}

// TS-05-16 (integration): codeimpl's Blocker maps onto needs_human's question and needed, with no options
func TestTS05_16_CodeimplBlockerMapsOntoNeedsHuman(t *testing.T) {
	run := toolio.NewRun("impl", "v1")
	result := &Result{
		Blocker: &Blocker{
			Reason: "the spec names a module the PRD does not create",
			Needed: "a decision on module X",
		},
	}
	env := run.Envelope(toolio.ExitNeedsHuman, result, &toolio.ErrorInfo{Stage: "survey", Category: CategoryBlocked})

	if env.NeedsHuman == nil {
		t.Fatal("expected env.NeedsHuman to be non-nil")
	}
	if env.NeedsHuman.Question != result.Blocker.Reason {
		t.Errorf("expected Question %q, got %q", result.Blocker.Reason, env.NeedsHuman.Question)
	}
	if env.NeedsHuman.Needed != result.Blocker.Needed {
		t.Errorf("expected Needed %q, got %q", result.Blocker.Needed, env.NeedsHuman.Needed)
	}
	if len(env.NeedsHuman.Options) != 0 {
		t.Errorf("expected len(Options) == 0, got %d", len(env.NeedsHuman.Options))
	}
}

// fakeIndex is a tools.Index test double. Tools returns a code_search tool, as
// the real codesearch index does.
type fakeIndex struct{ closed int }

func (f *fakeIndex) Symbols(context.Context, tools.SymbolQuery) (tools.SymbolAnswer, bool, error) {
	return tools.SymbolAnswer{}, false, nil
}
func (f *fakeIndex) Tools() []core.Tool {
	return []core.Tool{{
		Name: "code_search", Description: "ranked search",
		Execute: func(context.Context, json.RawMessage) core.ToolResult { return core.OKResult(map[string]any{}) },
	}}
}
func (f *fakeIndex) Invalidate(string) {}
func (f *fakeIndex) Close() error      { f.closed++; return nil }

// indexedRunner is a Runner whose Config carries idx — the Config the shell
// builds from the same index it hands the tool's Options.
func indexedRunner(t *testing.T, ws *tools.Workspace, p *faux.Provider, idx tools.Index) *agentrun.Runner {
	t.Helper()
	r, err := agentrun.NewRunner(agentrun.Config{
		Model:         faux.Model(),
		Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
		Workspace:     ws,
		Bounds:        agentrun.Bounds{MaxTurns: 8, MaxBudgetUSD: 1, MaxAttempts: 1},
		SessionPrefix: "impl",
		Index:         idx,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// wireTools is the names of the tools the first request offered the model.
func skipIfNoFindReferences(t *testing.T, ws *tools.Workspace) {
	t.Helper()
	built, err := tools.All(tools.Options{Workspace: ws})
	if err != nil {
		t.Skipf("tools.All failed: %v", err)
	}
	for _, tl := range built {
		if tl.Name == "find_references" {
			return
		}
	}
	t.Skip("find_references not offered by the replace target")
}

func wireTools(t *testing.T, p *faux.Provider) map[string]bool {
	t.Helper()
	reqs := p.Requests()
	if len(reqs) == 0 {
		t.Fatal("no request reached the model")
	}
	got := map[string]bool{}
	for _, w := range reqs[0].Tools {
		got[w.Name] = true
	}
	return got
}

// TS-16-5 (unit): codeimpl.Options.Index is forwarded to agentrun.Config.Index.
// Run builds the real agentBrain, so the survey phase is granted code_search,
// and the model is offered it because the index reached the Runner's Config.
//
// Verifies: 16-REQ-1.6, 16-REQ-2.1
func TestTS16_5_IndexReachesTheSurveyPhase(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	skipIfNoFindReferences(t, ws)
	idx := &fakeIndex{}
	p := faux.New()
	o := newOptions(ws, g, &scriptedBrain{})
	o.brain = nil
	o.Runner = indexedRunner(t, ws, p, idx)
	o.Index = idx

	// The unscripted model ends the survey with no result: the run stops
	// there, which is all this test needs of it.
	_, _ = Run(context.Background(), o)

	got := wireTools(t, p)
	if !got["code_search"] {
		t.Errorf("code_search was not offered to the survey phase: %v", got)
	}
	for _, n := range agentrun.ReadOnlyFileTools {
		if !got[n] {
			t.Errorf("%s is missing from the survey phase", n)
		}
	}
}

// 16-REQ-2.1: the writing phases — implement, repair, resolve — share one
// grant, and it carries code_search.
func TestTS16_5_TheWritingPhasesAreGrantedCodeSearch(t *testing.T) {
	task := afspec.Task{Id: 3, Title: "three"}
	in := taskInput{Spec: &afspec.Spec{}, Task: task}
	var out sink[Submission]

	on := (&agentBrain{protected: "/spec", codeSearch: true}).implementPhase(in, &out)
	if !slices.Contains(on.BuiltinTools, "code_search") || !slices.Contains(on.BuiltinTools, "write_file") {
		t.Errorf("implement grant = %v, want code_search beside the write tools", on.BuiltinTools)
	}
	_, grant := (&agentBrain{codeSearch: true}).writingPhase()
	if !slices.Contains(grant, "code_search") {
		t.Errorf("writing grant = %v", grant)
	}

	// 16-REQ-2.2
	off := (&agentBrain{protected: "/spec"}).implementPhase(in, &out)
	if slices.Contains(off.BuiltinTools, "code_search") {
		t.Errorf("implement grant without an index = %v", off.BuiltinTools)
	}
}

// 16-REQ-2.2: with no index the survey is offered no code_search.
func TestTS16_5_NilIndexLeavesTheSurveyGrantUnchanged(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	p := faux.New()
	o := newOptions(ws, g, &scriptedBrain{})
	o.brain = nil
	o.Runner = indexedRunner(t, ws, p, nil)
	_, _ = Run(context.Background(), o)
	if got := wireTools(t, p); got["code_search"] {
		t.Errorf("the survey offered code_search without an index: %v", got)
	}
}

// The grant is a copy: appending code_search must not grow the shared
// ReadOnlyFileTools slice.
func TestTS16_5_TheSharedReadOnlyListIsNotMutated(t *testing.T) {
	before := len(agentrun.ReadOnlyFileTools)
	got := agentrun.WithCodeSearch(agentrun.ReadOnlyFileTools, true)
	if len(agentrun.ReadOnlyFileTools) != before {
		t.Fatal("ReadOnlyFileTools was modified")
	}
	if got[len(got)-1] != "code_search" || len(got) != before+1 {
		t.Errorf("grant = %v", got)
	}
	if same := agentrun.WithCodeSearch(agentrun.ReadOnlyFileTools, false); len(same) != before {
		t.Errorf("grant without an index = %v", same)
	}
}

// ---------------------------------------------------------------------------
// Spec 16, task 6: the code-search index is invalidated after every
// Go-initiated tree change in impl, before the next phase starts.
//
// The test double stamps each Invalidate with the process-wide sequence number
// of the indextest package and with a snapshot of the tree at that moment (HEAD,
// the branch and `git status`), and a traced brain and check runner stamp the
// phases and the gate runs the same way. "After the commit" is then a fact about
// the snapshot — HEAD is the commit — and "before the next phase" a fact about
// the sequence numbers.
// ---------------------------------------------------------------------------

// treeSnap is the tree at the moment of an Invalidate.
type treeSnap struct {
	Seq    int64
	Rel    string
	Head   string
	Branch string
	Status string
}

// snapIndex is an indextest.Index that snapshots the tree on every Invalidate.
type snapIndex struct {
	*indextest.Index
	root  string
	snaps []treeSnap
}

func (s *snapIndex) Invalidate(rel string) {
	s.Index.Invalidate(rel)
	ev := s.Index.Events()
	snap := treeSnap{Seq: ev[len(ev)-1].Seq, Rel: rel}
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = s.root
		out, _ := cmd.Output()
		return strings.TrimSpace(string(out))
	}
	snap.Head = git("rev-parse", "HEAD")
	snap.Branch = git("rev-parse", "--abbrev-ref", "HEAD")
	snap.Status = git("status", "--porcelain", "-uall")
	s.snaps = append(s.snaps, snap)
}

// mark is one phase start or gate run, in the same order as the Invalidate calls.
type mark struct {
	Kind          string // survey, implement, repair, check
	Task, Attempt int
	Seq           int64
}

type trace struct{ marks []mark }

func (tr *trace) add(kind string, task, attempt int) {
	tr.marks = append(tr.marks, mark{Kind: kind, Task: task, Attempt: attempt, Seq: indextest.Next()})
}

// find returns the first mark of the kind for the task and attempt (zero
// values match any).
func (tr *trace) find(t *testing.T, kind string, task, attempt int) mark {
	t.Helper()
	for _, m := range tr.marks {
		if m.Kind == kind && (task == 0 || m.Task == task) && (attempt == 0 || m.Attempt == attempt) {
			return m
		}
	}
	t.Fatalf("no %s mark for task %d attempt %d in %v", kind, task, attempt, tr.marks)
	return mark{}
}

// firstAfter is the first mark of the kind that follows seq.
func (tr *trace) firstAfter(t *testing.T, kind string, seq int64) mark {
	t.Helper()
	for _, m := range tr.marks {
		if m.Kind == kind && m.Seq > seq {
			return m
		}
	}
	t.Fatalf("no %s mark after %d in %v", kind, seq, tr.marks)
	return mark{}
}

// lastBefore is the last mark of the kind that precedes seq.
func (tr *trace) lastBefore(t *testing.T, kind string, seq int64) mark {
	t.Helper()
	var got *mark
	for i, m := range tr.marks {
		if m.Kind == kind && m.Seq < seq {
			got = &tr.marks[i]
		}
	}
	if got == nil {
		t.Fatalf("no %s mark before %d in %v", kind, seq, tr.marks)
	}
	return *got
}

// tracedBrain stamps the phases the scripted brain runs.
type tracedBrain struct {
	*scriptedBrain
	tr *trace
}

func (b *tracedBrain) Survey(ctx context.Context, in surveyInput) (Survey, agentrun.Result, error) {
	b.tr.add("survey", 0, 0)
	return b.scriptedBrain.Survey(ctx, in)
}

func (b *tracedBrain) Repair(ctx context.Context, in repairInput) (RepairSubmission, agentrun.Result, error) {
	task := 0
	if in.Task != nil {
		task = in.Task.Id
	}
	b.tr.add("repair", task, in.Attempt)
	return b.scriptedBrain.Repair(ctx, in)
}

func (b *tracedBrain) Implement(ctx context.Context, in taskInput) (Submission, agentrun.Result, error) {
	b.tr.add("implement", in.Task.Id, in.Attempt)
	return b.scriptedBrain.Implement(ctx, in)
}

func (b *tracedBrain) Review(ctx context.Context, in conform.ReviewInput) (conform.Review, agentrun.Result, error) {
	b.tr.add("review", 0, 0)
	return b.scriptedBrain.Review(ctx, in)
}

func (b *tracedBrain) Resolve(ctx context.Context, in resolveInput) (ResolveSubmission, agentrun.Result, error) {
	b.tr.add("resolve", 0, 0)
	return b.scriptedBrain.Resolve(ctx, in)
}

// indexedRun runs the pipeline with a snapshotting index, a traced brain and a
// traced check runner.
func indexedRun(t *testing.T, ws *tools.Workspace, g *gitx.Git, b *scriptedBrain,
	configure func(*Options)) (*Result, error, *snapIndex, *trace) {
	t.Helper()
	idx := &snapIndex{Index: &indextest.Index{}, root: ws.Root}
	tr := &trace{}
	o := newOptions(ws, g, b)
	o.brain = &tracedBrain{scriptedBrain: b, tr: tr}
	o.Index = idx
	o.CheckRunner = func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
		tr.add("check", 0, 0)
		return gitx.ExecRunner(ctx, dir, argv, stdin...)
	}
	if configure != nil {
		configure(&o)
	}
	got, err := Run(context.Background(), o)
	return got, err, idx, tr
}

// between is the snapshots taken after lo and before hi.
func (s *snapIndex) between(lo, hi int64) []treeSnap {
	var out []treeSnap
	for _, sn := range s.snaps {
		if sn.Seq > lo && sn.Seq < hi {
			out = append(out, sn)
		}
	}
	return out
}

// anySnap reports whether one of the snapshots satisfies ok.
func anySnap(snaps []treeSnap, ok func(treeSnap) bool) bool {
	for _, s := range snaps {
		if ok(s) {
			return true
		}
	}
	return false
}

const implBranch = "impl/09-agent-mode-spec-cli"

// TS-16-16 (unit): the index is invalidated after the branch is created, before
// the first phase that follows it. In this pipeline the survey runs before the
// branch is created, so a new branch is invalidated before the first
// implementation phase, and an existing branch — checked out in pre-flight — is
// invalidated before the survey.
//
// Verifies: 16-REQ-4.2
func TestTS16_16_InvalidatesAfterBranchCreationBeforeThePhasesThatFollow(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	_, err, idx, tr := indexedRun(t, ws, g, &scriptedBrain{}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	survey := tr.find(t, "survey", 0, 0)
	first := tr.find(t, "implement", 1, 1)
	onBranch := func(s treeSnap) bool { return s.Branch == implBranch && s.Rel == "" }
	if !anySnap(idx.between(survey.Seq, first.Seq), onBranch) {
		t.Errorf("no Invalidate(\"\") on %s between the survey and the first implementation phase: %+v",
			implBranch, idx.snaps)
	}
}

func TestTS16_16_InvalidatesAfterCheckingOutAnExistingBranchBeforeTheSurvey(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	gitOut(t, ws.Root, "branch", implBranch)
	got, err, idx, tr := indexedRun(t, ws, g, &scriptedBrain{}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !got.Resumed {
		t.Fatal("the run did not continue on the existing branch")
	}
	survey := tr.find(t, "survey", 0, 0)
	if !anySnap(idx.between(0, survey.Seq), func(s treeSnap) bool { return s.Branch == implBranch && s.Rel == "" }) {
		t.Errorf("no Invalidate(\"\") on %s before the survey: %+v", implBranch, idx.snaps)
	}
}

// TS-16-17 (unit): after revertSpecDir and dropScratchFiles, before the gate.
//
// Verifies: 16-REQ-4.3
func TestTS16_17_InvalidatesAfterRevertAndDropBeforeTheGate(t *testing.T) {
	ws, g, specDir := newSpecRepo(t)
	b := &scriptedBrain{}
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		write(t, specDir, "prd.md", "the model rewrote the PRD")
		write(t, root, "notes.bak", "scratch")
		return goodWork(root, task, attempt)
	}
	_, err, idx, tr := indexedRun(t, ws, g, b, func(o *Options) { o.Task = 1 })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	impl := tr.find(t, "implement", 1, 1)
	gate := tr.firstAfter(t, "check", impl.Seq)
	ok := anySnap(idx.between(impl.Seq, gate.Seq), func(s treeSnap) bool {
		return s.Rel == "" && !strings.Contains(s.Status, "prd.md") && !strings.Contains(s.Status, "notes.bak") &&
			strings.Contains(s.Status, "task1.go")
	})
	if !ok {
		t.Errorf("no Invalidate(\"\") between the phase and the gate with the PRD reverted and the scratch file dropped: %+v",
			idx.between(impl.Seq, gate.Seq))
	}
}

// TS-16-18 (unit): after discard, before the next attempt.
//
// Verifies: 16-REQ-4.4
func TestTS16_18_InvalidatesAfterDiscardBeforeTheNextAttempt(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &scriptedBrain{}
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		if task.Id == 2 && attempt == 1 {
			write(t, root, "FAIL", "")
			write(t, root, "half.go", "package x\n")
			return passingReport(task, "break the build"), nil
		}
		return goodWork(root, task, attempt)
	}
	got, err, idx, tr := indexedRun(t, ws, g, b, func(o *Options) { o.TaskAttempts = 2 })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Tasks[1].Attempts != 2 {
		t.Fatalf("task 2 took %d attempts, want 2", got.Tasks[1].Attempts)
	}
	second := tr.find(t, "implement", 2, 2)
	gate := tr.lastBefore(t, "check", second.Seq)
	ok := anySnap(idx.between(gate.Seq, second.Seq), func(s treeSnap) bool {
		return s.Rel == "" && !strings.Contains(s.Status, "FAIL") && !strings.Contains(s.Status, "half.go")
	})
	if !ok {
		t.Errorf("no Invalidate(\"\") between the failed gate and attempt 2 with the attempt discarded: %+v",
			idx.between(gate.Seq, second.Seq))
	}
}

// TS-16-19 (unit): after the commit of a landed task, before the next task.
//
// Verifies: 16-REQ-4.5
func TestTS16_19_InvalidatesAfterACommitBeforeTheNextTask(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	_, err, idx, tr := indexedRun(t, ws, g, &scriptedBrain{}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	commit1 := gitOut(t, ws.Root, "rev-parse", "HEAD~2")
	first := tr.find(t, "implement", 1, 1)
	second := tr.find(t, "implement", 2, 1)
	ok := anySnap(idx.between(first.Seq, second.Seq), func(s treeSnap) bool {
		return s.Rel == "" && s.Head == commit1 && s.Status == ""
	})
	if !ok {
		t.Errorf("no Invalidate(\"\") with HEAD at task 1's commit %s before task 2's phase: %+v",
			commit1, idx.between(first.Seq, second.Seq))
	}
}

// TS-16-20 (unit): after the baseline repair's commit, before the first task.
//
// Verifies: 16-REQ-4.6
func TestTS16_20_InvalidatesAfterTheRepairCommitBeforeTheFirstTask(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	write(t, ws.Root, "FAIL", "the suite is red")
	if _, err := g.CommitAll(context.Background(), "chore: break the build\n"); err != nil {
		t.Fatal(err)
	}
	b := &scriptedBrain{}
	b.repair = func(root string, attempt int) (RepairSubmission, error) {
		if err := os.Remove(filepath.Join(root, "FAIL")); err != nil {
			return RepairSubmission{}, err
		}
		return RepairSubmission{
			Cause: "A FAIL marker was committed.", Summary: "Removed the marker.",
			CommitSubject: "remove the FAIL marker the test target trips on.",
			Changes:       []FileChange{{Path: "FAIL", Change: "deleted"}},
		}, nil
	}
	_, err, idx, tr := indexedRun(t, ws, g, b, func(o *Options) { o.Repair = true })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	repairCommit := gitOut(t, ws.Root, "rev-parse", "HEAD~3")
	repair := tr.find(t, "repair", 0, 1)
	first := tr.find(t, "implement", 1, 1)
	ok := anySnap(idx.between(repair.Seq, first.Seq), func(s treeSnap) bool {
		return s.Rel == "" && s.Head == repairCommit && s.Status == ""
	})
	if !ok {
		t.Errorf("no Invalidate(\"\") with HEAD at the repair commit %s before task 1: %+v",
			repairCommit, idx.between(repair.Seq, first.Seq))
	}
}

// TS-16-21 (unit): after the repair that follows the integration task, before
// the task's own commit. The repair's hold commit is undone first, so the
// snapshot shows HEAD back at the previous task's commit with the repair's work
// still in the index, and no commit for task 3 yet.
//
// Verifies: 16-REQ-4.7
func TestTS16_21_InvalidatesAfterTheRepairAfterTaskBeforeTheTaskCommit(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &scriptedBrain{}
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		if task.Kind == afspec.TaskKindIntegration {
			write(t, root, "FAIL", "the smoke tests found a wiring gap")
		}
		return goodWork(root, task, attempt)
	}
	b.repair = func(root string, attempt int) (RepairSubmission, error) {
		if err := os.Remove(filepath.Join(root, "FAIL")); err != nil {
			return RepairSubmission{}, err
		}
		write(t, root, "wiring.go", "package x // the gap\n")
		return RepairSubmission{
			Cause: "Task 1's parser never registered its command.", Summary: "Registered it.",
			CommitSubject: "register the parser command",
			Changes:       []FileChange{{Path: "wiring.go", Change: "added"}},
		}, nil
	}
	_, err, idx, tr := indexedRun(t, ws, g, b, func(o *Options) { o.Repair = true })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	commit2 := gitOut(t, ws.Root, "rev-parse", "HEAD~1")
	repair := tr.find(t, "repair", 3, 1)
	ok := anySnap(idx.snaps, func(s treeSnap) bool {
		return s.Seq > repair.Seq && s.Rel == "" && s.Head == commit2 &&
			strings.Contains(s.Status, "wiring.go") && !strings.Contains(s.Status, "FAIL")
	})
	if !ok {
		t.Errorf("no Invalidate(\"\") after the repair with HEAD at task 2's commit %s and the repair's work uncommitted: %+v",
			commit2, idx.snaps)
	}
}

// TS-16-30 (integration): a run of a two-task spec — the example's first two
// tasks folded into task 1, its integration task as task 2 (twoTasks) —
// through the real brain, the real phases, the real Runner and tool set,
// offers code_search to both tasks, invalidates the index with task 1's
// commit in place before task 2's phase starts, and leaves closing the index
// to its caller.
//
// "code_search was in both tasks' BuiltinTools" is read off what reached the
// wire: the tools a request offers are the phase's BuiltinTools that tools.All
// built, so code_search is offered only when the phase named it and the
// Runner's Config.Index provided it. Each task's requests are checked apart.
//
// Verifies: 16-REQ-4.5, 16-REQ-2.1
func TestTS16_30_EveryTaskSeesThePreviousCommitThroughTheIndex(t *testing.T) {
	ws, g, _ := newSpecRepoWith(t, twoTasks)
	probe := &indextest.Probe{Root: ws.Root}

	task := func(n, subject string, tests []string, doneWhen bool) []faux.Turn {
		var verdicts []map[string]any
		for _, id := range tests {
			verdicts = append(verdicts, map[string]any{"id": id, "verdict": "pass",
				"evidence":     "task" + n + ".go: " + id + " passes when run with make test",
				"red_evidence": "go test failed before the change: " + id + " got the zero value"})
		}
		args := map[string]any{
			"summary": "implemented task " + n, "commit_subject": subject,
			"test_verdicts": verdicts,
			"changes":       []map[string]any{{"path": "task" + n + ".go", "change": "added"}},
		}
		if doneWhen {
			args["done_when_verdicts"] = []map[string]any{{"id": "DW-1", "verdict": "pass",
				"evidence": "ran the command in done_when and it exited zero"}}
		}
		return []faux.Turn{
			indextest.ToolTurn("s"+n, "code_search", map[string]any{"query": "task"}),
			indextest.ToolTurn("w"+n, "write_file", map[string]any{"path": "task" + n + ".go", "content": "package x\n"}),
			indextest.ToolTurn("t"+n, "submit_task", args),
		}
	}
	const turnsPerTask = 3
	turns := append(task("1", "feat: land task one", []string{"TS-09-1", "TS-09-2", "TS-09-3", "TS-09-4", "TS-09-5"}, false),
		task("2", "feat: land task two", []string{"TS-09-6"}, true)...)
	p := faux.New(turns...)

	o := newOptions(ws, g, &scriptedBrain{})
	o.brain = nil
	o.Runner = indexedRunner(t, ws, p, probe)
	o.Index = probe
	o.NoSurvey, o.NoReview = true, true

	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.TasksDone != 2 {
		t.Fatalf("TasksDone = %d, want 2 (stage %q)", got.TasksDone, got.Stage)
	}

	// One phase per task, one request per scripted turn: the first three
	// requests are task 1's, the next three task 2's.
	offered := indextest.Offered(p)
	if len(offered) != 2*turnsPerTask {
		t.Fatalf("%d requests reached the model, want %d (two tasks of %d turns)", len(offered), 2*turnsPerTask, turnsPerTask)
	}
	for i, names := range offered {
		if !indextest.Has(names, "code_search") {
			t.Errorf("task %d's request %d was not offered code_search: %v", i/turnsPerTask+1, i%turnsPerTask+1, names)
		}
	}
	s := probe.Searches()
	if len(s) != 2 {
		t.Fatalf("code_search reached the index %d times, want 2 (once per task)", len(s))
	}
	commit := gitOut(t, ws.Root, "log", "--format=%H", "--grep", "task one")
	if !anyIndexSnap(probe.Between(s[0], s[1]), func(sn indextest.Snap) bool {
		return sn.Rel == "" && sn.Head == commit && sn.Status == ""
	}) {
		t.Errorf("no Invalidate(\"\") with HEAD at task 1's commit %s before task 2's phase: %+v",
			commit, probe.Between(s[0], s[1]))
	}
	if probe.InvalidateCalls() < 2 {
		t.Errorf("Invalidate was called %d times, want at least 2", probe.InvalidateCalls())
	}
	if n := probe.CloseCalls(); n != 0 {
		t.Errorf("the pipeline closed the index %d times: closing is the entry point's job", n)
	}
}

func anyIndexSnap(snaps []indextest.Snap, ok func(indextest.Snap) bool) bool {
	for _, s := range snaps {
		if ok(s) {
			return true
		}
	}
	return false
}

// The project's checks are its own programs and can write into the tree: the
// index is invalidated after every gate run, before whatever runs next
// (16-REQ-4, design decision 9).
func TestInvalidatesAfterEveryGateRun(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	_, err, idx, tr := indexedRun(t, ws, g, &scriptedBrain{}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	gates := 0
	for i, m := range tr.marks {
		if m.Kind != "check" {
			continue
		}
		gates++
		// The next mark that is not another check run of the same step.
		next := int64(1) << 62
		for _, later := range tr.marks[i+1:] {
			if later.Kind != "check" {
				next = later.Seq
				break
			}
		}
		if len(idx.between(m.Seq, next)) == 0 {
			t.Errorf("no Invalidate after the gate run at seq %d before seq %d", m.Seq, next)
		}
	}
	if gates == 0 {
		t.Fatal("no gate ran")
	}
}

// The conformance review and the resolve phase each start from a tree the
// stage's own checks may have touched: the index is invalidated before each.
func TestInvalidatesBeforeTheReviewAndResolvePhases(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &scriptedBrain{
		review: func(in conform.ReviewInput) (conform.Review, error) {
			r := conformingReview(in.Scope)
			if len(r.Requirements) > 0 {
				r.Requirements[0].Status = conform.StatusMissing
				r.Requirements[0].Evidence = ""
			}
			return r, nil
		},
		resolve: func(string, resolveInput) (ResolveSubmission, error) {
			return ResolveSubmission{Summary: "nothing to change"}, nil
		},
	}
	var hermetic []int64
	_, _, idx, tr := indexedRun(t, ws, g, b, func(o *Options) {
		o.NoReview = false
		o.HermeticRunner = func(home string) gitx.Runner {
			inner := gitx.HermeticRunner(home)
			return func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
				if len(hermetic) == 0 {
					hermetic = append(hermetic, indextest.Next())
				}
				return inner(ctx, dir, argv, stdin...)
			}
		}
	})
	review := tr.find(t, "review", 0, 0)
	resolve := tr.find(t, "resolve", 0, 0)
	if len(hermetic) == 0 {
		t.Fatal("the clean-environment gate did not run")
	}
	if len(idx.between(hermetic[0], review.Seq)) == 0 {
		t.Errorf("no Invalidate between the clean-environment gate (%d) and the review phase (%d)", hermetic[0], review.Seq)
	}
	if len(idx.between(review.Seq, resolve.Seq)) == 0 {
		t.Errorf("no Invalidate between the review (%d) and the resolve phase (%d)", review.Seq, resolve.Seq)
	}
}

// The pipeline grants and invalidates the index its Runner's phases read
// (16-REQ-1.6): a Runner built on another index is refused before any phase
// runs, and the Runner's index is the run's when the Options carry none.
func TestTS16_5_TheRunnersIndexIsTheRunsIndex(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	p := faux.New()
	o := newOptions(ws, g, &scriptedBrain{})
	o.brain = nil
	o.Runner = indexedRunner(t, ws, p, &fakeIndex{})
	o.Index = &fakeIndex{}
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "not the one its runner was built with") {
		t.Fatalf("Run with two indexes: err = %v, want a refusal", err)
	}
	if n := len(p.Requests()); n != 0 {
		t.Errorf("%d requests reached the model after the refusal", n)
	}

	p = faux.New()
	o = newOptions(ws, g, &scriptedBrain{})
	o.brain = nil
	o.Runner = indexedRunner(t, ws, p, &fakeIndex{})
	_, _ = Run(context.Background(), o)
	if got := wireTools(t, p); !got["code_search"] {
		t.Errorf("the Runner's index was not taken as the run's: %v", got)
	}
}

// 16-REQ-2.1: the independent review phase, which conform.RunReview builds, is
// granted code_search by this tool's brain like every other phase, and is not
// without an index (16-REQ-2.2).
func TestTheReviewPhaseIsGrantedCodeSearch(t *testing.T) {
	ws, _, _ := newSpecRepo(t)
	for _, on := range []bool{true, false} {
		var idx tools.Index
		if on {
			idx = &fakeIndex{}
		}
		p := faux.New()
		b := &agentBrain{runner: indexedRunner(t, ws, p, idx), codeSearch: on}
		_, _, _ = b.Review(context.Background(), conform.ReviewInput{
			Root: ws.Root, Base: "HEAD", Spec: "spec", Scope: conform.ReviewScope{Requirements: []string{"01-REQ-1"}},
		})
		if got := wireTools(t, p); got["code_search"] != on {
			t.Errorf("index %v: the review phase offered %v", on, got)
		}
	}
}
