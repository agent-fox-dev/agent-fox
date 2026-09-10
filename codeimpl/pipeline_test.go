package codeimpl

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The tests below drive the REAL pipeline — the real pre-flight, the real
// git, the real spec loading and state writes, the real gate comparison —
// with the model half replaced by a scripted brain. The split that makes
// the program trustworthy is the same split that makes it testable.

// scriptedBrain stands in for the two phases.
type scriptedBrain struct {
	survey    Survey
	surveyErr error
	// implement decides what one attempt does, given the task and the
	// attempt number: it edits the tree and returns the report.
	implement func(root string, task afspec.Task, attempt int) (Submission, error)

	surveys  int
	inputs   []taskInput
	surveyIn surveyInput
}

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
			Evidence: "task" + itoa(task.Id) + "_test.go Test" + id + " passes when run with make test"})
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
	warned := strings.Join(o.Run.Warnings(), "\n")
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
	warned := strings.Join(o.Run.Warnings(), "\n")
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
		if w := strings.Join(o.Run.Warnings(), "\n"); !strings.Contains(w, "draft") {
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
