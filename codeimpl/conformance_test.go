package codeimpl

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// issueForge is a forge that also files issues, and remembers what it was
// asked.
type issueForge struct {
	*mockAuthClient
	issues []issuex.CreateIssueRequest
}

func (f *issueForge) CreateIssue(_ context.Context, _ issuex.Repo, req issuex.CreateIssueRequest) (issuex.Issue, error) {
	f.issues = append(f.issues, req)
	return issuex.Issue{Number: 70 + len(f.issues), HTMLURL: "https://github.com/grp/prj/issues/" + itoa(70+len(f.issues))}, nil
}

var fixedNow = func() time.Time { return time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC) }

// A run with nothing to find: the review is given the spec and the diff,
// and nothing any task wrote about itself; the checks run again in a clean
// environment; the pull request says the work is complete because nothing
// says otherwise.
func TestConformanceReviewIsIndependentAndAConformingRunIsComplete(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &scriptedBrain{survey: Survey{Summary: "The CLI lives in cmd/spec.", Drift: []Drift{
		{SpecRef: "09-REQ-2.1", Finding: "emit.go does not exist", Resolution: "Followed the spec: created emit.go"},
	}}}
	o := newOptions(ws, g, b)
	o.Now = fixedNow
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(b.reviewIns) != 1 || len(b.resolveIns) != 0 {
		t.Fatalf("review ran %d time(s), resolve %d", len(b.reviewIns), len(b.resolveIns))
	}
	in := b.reviewIns[0]
	if strings.Join(in.Scope.Requirements, ",") != "09-REQ-1,09-REQ-2.1,09-REQ-2.2" ||
		strings.Join(in.Scope.Tests, ",") != "TS-09-1,TS-09-2,TS-09-3,TS-09-4,TS-09-5,TS-09-6" {
		t.Errorf("review scope = %+v", in.Scope)
	}
	if len(in.Scope.Decisions) != 1 || in.Scope.Decisions[0].ID != "D-1" ||
		!strings.Contains(in.Scope.Decisions[0].Text, "created emit.go") {
		t.Errorf("the survey's decision is not a checklist item: %+v", in.Scope.Decisions)
	}
	base := gitOut(t, ws.Root, "rev-parse", "main")
	if in.Base != base || len(in.ChangedFiles) != 3 {
		t.Errorf("review base = %s (main is %s), changed = %v", in.Base, base, in.ChangedFiles)
	}
	prompt := conform.ReviewPrompt(in)
	for _, authored := range []string{"Did what task 1 asked.", "the widget counter is zero-based"} {
		if strings.Contains(prompt, authored) {
			t.Errorf("the review prompt carries the authors' own account (%q)", authored)
		}
	}

	if len(got.Unmet) != 0 || len(got.Blocking) != 0 {
		t.Errorf("Unmet = %+v, Blocking = %+v", got.Unmet, got.Blocking)
	}
	if got.FinalVerification == nil || !got.FinalVerification.OK() {
		t.Fatalf("FinalVerification = %+v", got.FinalVerification)
	}
	if env := got.Environment; env == nil || !env.Hermetic || env.InitDefaultBranch != "unset" || env.GitVersion == "" {
		t.Errorf("Environment = %+v", got.Environment)
	}
	body := pullRequestBody(got)
	for _, want := range []string{"3 of 3 task(s) landed in this run, and an independent review found every",
		"## Conformance review", "In a clean environment", "| init.defaultBranch | unset |"} {
		if !strings.Contains(body, want) {
			t.Errorf("the PR body lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Unmet requirements") || strings.Contains(body, "not complete") {
		t.Errorf("a conforming run's body reports unmet work:\n%s", body)
	}
}

// A requirement the review finds missing is answered by the resolve phase.
// Declared, with an erratum in the change, it is unmet and tracked; the pull
// request opens with it, and its summary does not say the work is done.
func TestADeclaredDeviationIsUnmetAndOpensThePullRequest(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &scriptedBrain{}
	b.review = func(in conform.ReviewInput) (conform.Review, error) {
		r := conformingReview(in.Scope)
		r.Requirements[0] = conform.RequirementRow{ID: "09-REQ-1", Status: conform.StatusDifferent,
			Evidence: "task1.go:1 exits 2; the requirement says 3"}
		r.Tests[0] = conform.TestRow{ID: "TS-09-1", Assessment: conform.AssessWeaker,
			Evidence: "asserts the internal error code, not the process exit code"}
		return r, nil
	}
	b.resolve = func(root string, in resolveInput) (ResolveSubmission, error) {
		write(t, root, "ERRATA.md", "Exit code 2: task1.go:1, TestTS09_1.\n")
		return ResolveSubmission{Summary: "Declared the exit code.", CommitSubject: "record the exit code erratum",
			Changes: []FileChange{{Path: "ERRATA.md", Change: "added"}},
			Deviations: []conform.Deviation{{Key: "09-REQ-1", Test: "TS-09-1",
				Reason: "the CLI library maps every usage error to exit 2", Errata: "ERRATA.md"}}}, nil
	}
	o := newOptions(ws, g, b)
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(b.resolveIns) != 1 || len(b.resolveIns[0].Blockers) != 1 || b.resolveIns[0].Blockers[0].Key != "09-REQ-1" {
		t.Fatalf("resolve input = %+v", b.resolveIns)
	}
	if len(b.reviewIns) != 2 {
		t.Errorf("the review ran %d time(s); the change the resolve phase landed is reviewed again", len(b.reviewIns))
	}
	if got.Resolve == nil || got.Resolve.Commit == "" {
		t.Fatalf("Resolve = %+v", got.Resolve)
	}
	if subject := gitOut(t, ws.Root, "log", "-1", "--format=%s"); subject != "fix: record the exit code erratum" {
		t.Errorf("head = %q", subject)
	}
	if len(got.Blocking) != 0 {
		t.Errorf("a declared blocker is still blocking: %+v", got.Blocking)
	}
	if len(got.Unmet) != 2 || got.Unmet[0].Requirement != "09-REQ-1" || got.Unmet[0].Tracking != "ERRATA.md" ||
		got.Unmet[1].Test != "TS-09-1" || got.Unmet[1].Source != conform.SourceReview {
		t.Errorf("Unmet = %+v", got.Unmet)
	}
	body := pullRequestBody(got)
	if !strings.HasPrefix(body, "## ⚠️ Unmet requirements") {
		t.Errorf("the body does not open with the unmet block:\n%s", body)
	}
	if !strings.Contains(body, "3 of 3 task(s) landed in this run. **The work is not complete:** 2 unmet item(s)") {
		t.Errorf("the summary line claims more than the run did:\n%s", body)
	}
	if !strings.Contains(got.Summary(), "2 unmet requirement(s)") {
		t.Errorf("Summary() = %q", got.Summary())
	}
	if !hasWarning(o.Run, toolio.WarnUnmetRequirements) {
		t.Error("no unmet_requirements warning")
	}
}

// A blocker nobody fixed or declared keeps the run from being presented as
// done: it lands as a draft and the run fails nonconformant (exit 4).
func TestAnUnansweredBlockerMakesTheRunNonconformant(t *testing.T) {
	o := implOriginFixture(t, filepath.Join(t.TempDir(), "origin.git"))
	b := o.brain.(*scriptedBrain)
	b.review = func(in conform.ReviewInput) (conform.Review, error) {
		r := conformingReview(in.Scope)
		r.Tests[1] = conform.TestRow{ID: "TS-09-2", Assessment: conform.AssessTautological,
			Evidence: "the assertion sits in a branch that never runs"}
		return r, nil
	}
	b.resolve = func(string, resolveInput) (ResolveSubmission, error) {
		return ResolveSubmission{Summary: "Looked at it; nothing to change."}, nil
	}
	forge := o.Forge.(*mockAuthClient)
	got, err := Run(context.Background(), o)
	f := failureOf(t, err)
	if f.Category != CategoryNonconformant || toolio.ExitCodeFor(f.Category) != toolio.ExitUnverified {
		t.Errorf("failure = %s/%s (exit %d)", f.Stage, f.Category, toolio.ExitCodeFor(f.Category))
	}
	if len(got.Blocking) != 1 || got.Blocking[0].Key != "TS-09-2" {
		t.Errorf("Blocking = %+v", got.Blocking)
	}
	if !got.Pushed || got.PullRequestURL == "" || !forge.capturedReq.Draft {
		t.Errorf("pushed=%v url=%q draft=%v: the work is published as a draft", got.Pushed, got.PullRequestURL,
			forge.capturedReq.Draft)
	}
	if !strings.HasPrefix(forge.capturedReq.Body, "## ❌ Not ready: blocking findings") {
		t.Errorf("the body does not open with the blocker:\n%s", forge.capturedReq.Body)
	}
}

// A deviation no erratum in the change records is filed as an issue when the
// run opens a pull request, and the unmet block links it.
func TestAnUntrackedDeviationIsFiledAsAnIssue(t *testing.T) {
	o := implOriginFixture(t, filepath.Join(t.TempDir(), "origin.git"))
	forge := &issueForge{mockAuthClient: o.Forge.(*mockAuthClient)}
	o.Forge = forge
	b := o.brain.(*scriptedBrain)
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		sub, err := goodWork(root, task, attempt)
		if task.Id == 2 {
			sub.Deviations = []conform.Deviation{{Key: "09-REQ-2.2",
				Reason: "the host library cannot emit the trailing newline the requirement asks for"}}
		}
		return sub, err
	}
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(forge.issues) != 1 || !strings.Contains(forge.issues[0].Title, "09-REQ-2.2") {
		t.Fatalf("issues filed = %+v", forge.issues)
	}
	if len(got.Unmet) != 1 || got.Unmet[0].Tracking != "https://github.com/grp/prj/issues/71" {
		t.Errorf("Unmet = %+v", got.Unmet)
	}
	if !strings.Contains(forge.capturedReq.Body, "https://github.com/grp/prj/issues/71") {
		t.Errorf("the PR body does not link the issue:\n%s", forge.capturedReq.Body)
	}
}

// A spec whose tasks list their files restricts the change to them. A file
// outside is fix-only: it cannot be declared, and the run does not land it as
// done until it is gone.
func TestAFileOutsideTheSpecsScopeMustBeReverted(t *testing.T) {
	ws, g, specDir := newSpecRepo(t)
	setTouches(t, specDir, map[int][]string{1: {"task1.go"}, 2: {"task2.go"}, 3: {"task3.go"}})
	b := &scriptedBrain{}
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		if task.Id == 2 {
			write(t, root, "stray.go", "package x // while I was here\n")
		}
		return goodWork(root, task, attempt)
	}
	b.resolve = func(root string, in resolveInput) (ResolveSubmission, error) {
		if len(in.Blockers) != 1 || in.Blockers[0].Key != "scope:stray.go" || in.Blockers[0].Declarable {
			t.Errorf("resolve blockers = %+v", in.Blockers)
		}
		if err := os.Remove(filepath.Join(root, "stray.go")); err != nil {
			t.Fatal(err)
		}
		return ResolveSubmission{Summary: "Reverted the stray file.", CommitSubject: "drop a change outside the spec",
			Changes: []FileChange{{Path: "stray.go", Change: "removed"}}}, nil
	}
	got, err := Run(context.Background(), newOptions(ws, g, b))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got.OutOfScope) != 0 || len(got.Blocking) != 0 {
		t.Errorf("OutOfScope = %v, Blocking = %+v", got.OutOfScope, got.Blocking)
	}
	if out := gitOut(t, ws.Root, "diff", "--name-only", "main", "HEAD"); strings.Contains(out, "stray.go") {
		t.Errorf("the branch still carries stray.go:\n%s", out)
	}

	// The resolve tool refuses to declare a fix-only finding.
	var sink sinkResolve
	res := submitResolveTool(&sink.s, []conform.Blocker{{Key: "scope:stray.go"}}).Execute(context.Background(),
		mustJSON(t, ResolveSubmission{Summary: "x", Deviations: []conform.Deviation{{Key: "scope:stray.go",
			Reason: "it is a harmless cleanup of a typo"}}}))
	if res.OK {
		t.Error("a scope finding was declared away")
	}
}

type sinkResolve struct{ s sink[ResolveSubmission] }

// Checks that pass only because of the machine's git configuration fail the
// final verification, which runs with an empty HOME and none.
func TestChecksThatNeedTheUsersGitConfigBlockTheRun(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	home := t.TempDir()
	write(t, home, ".gitconfig", "[init]\n\tdefaultBranch = main\n")
	t.Setenv("HOME", home)
	for _, k := range []string{"XDG_CONFIG_HOME", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	write(t, ws.Root, "Makefile", "test:\n\t@test ! -f FAIL\n\t@git config --get init.defaultBranch >/dev/null\nlint:\n\t@exit 0\n")
	if _, err := g.CommitAll(context.Background(), "chore: tests that lean on the user's git config\n"); err != nil {
		t.Fatal(err)
	}
	b := &scriptedBrain{resolve: func(string, resolveInput) (ResolveSubmission, error) {
		return ResolveSubmission{Summary: "Could not find the cause."}, nil
	}}
	got, err := Run(context.Background(), newOptions(ws, g, b))
	if f := failureOf(t, err); f.Category != CategoryNonconformant {
		t.Fatalf("failure = %+v", f)
	}
	if got.Verification.OK() == false || got.FinalVerification == nil || got.FinalVerification.OK() {
		t.Errorf("the checks should pass here and fail clean: last=%v clean=%+v", got.Verification.OK(), got.FinalVerification)
	}
	if len(got.Blocking) != 1 || got.Blocking[0].Key != "hermetic" {
		t.Errorf("Blocking = %+v", got.Blocking)
	}
	if !strings.Contains(pullRequestBody(got), "❌ `make test` fails") {
		t.Errorf("the body does not report the clean-environment failure:\n%s", pullRequestBody(got))
	}
}

// A task that waives test-first has its tests run with its implementation
// taken out: tests that still pass prove nothing, and the task does not land.
func TestWaivedTestFirstNeedsTestsThatFailWithoutTheWork(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	// make test fails when a tests/<name>.txt has no <name>.go beside it: the
	// tests depend on the implementation.
	write(t, ws.Root, "Makefile", "test:\n\t@test ! -f FAIL\n\t@[ -d tests ] || exit 0; for t in tests/*.txt; do "+
		"test -f \"$$(basename $$t .txt).go\" || exit 1; done\nlint:\n\t@exit 0\n")
	if _, err := g.CommitAll(context.Background(), "chore: tests that depend on the code\n"); err != nil {
		t.Fatal(err)
	}
	proving := func(root string, task afspec.Task, attempt int) (Submission, error) {
		sub, err := goodWork(root, task, attempt)
		if err := os.MkdirAll(filepath.Join(root, "tests"), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, root, filepath.Join("tests", "task"+itoa(task.Id)+".txt"), "proves task\n")
		sub.TestFirstDeviation = "an integration task over wiring the earlier tasks built; it passes immediately"
		return sub, err
	}
	b := &scriptedBrain{implement: proving}
	o := newOptions(ws, g, b)
	o.Task = 1
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rc := got.Tasks[0].RevertCheck
	if rc == nil || !rc.Ran || !rc.Proves || strings.Join(rc.Reverted, ",") != "task1.go" {
		t.Errorf("revert check = %+v", rc)
	}

	// Tests that do not depend on the work: the attempt does not land.
	ws, g, _ = newSpecRepo(t)
	b = &scriptedBrain{implement: func(root string, task afspec.Task, attempt int) (Submission, error) {
		sub, err := goodWork(root, task, attempt)
		sub.TestFirstDeviation = "an integration task over wiring the earlier tasks built; it passes immediately"
		return sub, err
	}}
	o = newOptions(ws, g, b)
	o.Task = 1
	got, err = Run(context.Background(), o)
	if f := failureOf(t, err); f.Category != CategoryUnverified {
		t.Fatalf("failure = %+v", f)
	}
	if len(b.inputs) != 2 || b.inputs[1].Previous == nil || !strings.Contains(b.inputs[1].Previous.Reason, "still pass") {
		t.Errorf("the retry was not told why: %+v", b.inputs[len(b.inputs)-1].Previous)
	}
	if got.Tasks[0].RevertCheck == nil || got.Tasks[0].RevertCheck.Proves {
		t.Errorf("revert check = %+v", got.Tasks[0].RevertCheck)
	}
}

// Every writing phase is told today's date; a date it writes in an ADR or an
// erratum is the program's, not one it chose.
func TestWritingPhasesAreGivenTodaysDate(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &scriptedBrain{}
	o := newOptions(ws, g, b)
	o.Now = fixedNow
	o.Task = 1
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if p := taskPrompt(b.inputs[0]); !strings.Contains(p, "Today's date is 2026-10-05") {
		t.Errorf("the task prompt carries no date:\n%s", p[:400])
	}
	if p := resolvePrompt(resolveInput{Spec: b.inputs[0].Spec, Now: fixedNow()}); !strings.Contains(p, "2026-10-05") {
		t.Error("the resolve prompt carries no date")
	}
}

// A task that changed documentation reports where each fact came from in the
// code, and each source is checked.
func TestDocumentationIsCopiedFromTheCode(t *testing.T) {
	_, _, specDir := newSpecRepo(t)
	root := filepath.Dir(filepath.Dir(specDir))
	write(t, root, "exit.go", "package x\n\nconst exitDiverged = 2\n")
	task := afspec.Task{Id: 1, Tests: []string{"TS-09-1"}}
	docs := func(context.Context) []string { return []string{"docs/cli.md"} }
	submit := func(s Submission) bool {
		var out sink[Submission]
		return submitTaskTool(&out, task, false, docs, root).Execute(context.Background(), mustJSON(t, s)).OK
	}
	sub := passingReport(task, "document the exit code")
	if submit(sub) {
		t.Error("a docs change with no doc_sources was accepted")
	}
	sub.DocSources = []conform.DocSource{{Claim: "exits 3", Source: "exit.go:3", Quote: "exitDiverged = 3"}}
	if submit(sub) {
		t.Error("a doc source the code contradicts was accepted")
	}
	sub.DocSources = []conform.DocSource{{Claim: "exits 2", Source: "exit.go:3", Quote: "exitDiverged = 2"}}
	if !submit(sub) {
		t.Error("a doc source that matches the code was refused")
	}
}

func setTouches(t *testing.T, specDir string, touches map[int][]string) {
	t.Helper()
	path := filepath.Join(specDir, "tasks.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for _, task := range doc["tasks"].([]any) {
		m := task.(map[string]any)
		m["touches"] = touches[int(m["id"].(float64))]
	}
	if b, err = json.MarshalIndent(doc, "", "  "); err != nil {
		t.Fatal(err)
	}
	write(t, specDir, "tasks.json", string(b))
	g := gitx.New(filepath.Dir(filepath.Dir(specDir)), gitx.ExecRunner)
	if _, err := g.CommitAll(context.Background(), "chore: restrict the spec's files\n"); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func hasWarning(r *toolio.Run, code toolio.WarnCode) bool {
	for _, w := range r.Warnings() {
		if w.Code == code {
			return true
		}
	}
	return false
}
