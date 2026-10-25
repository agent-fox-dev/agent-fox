package afcmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// taskOf is the task block of the brief of a run: what follows its prefix.
func taskOf(t *testing.T, runDir string) string {
	t.Helper()
	_, task, ok := strings.Cut(testutil.ReadFile(t, filepath.Join(runDir, "brief.md")), "\n## Task\n")
	if !ok {
		t.Fatalf("the brief of %s has no task block", runDir)
	}
	return task
}

// remoteHas says whether the origin of the checkout has the branch.
func (e *env) remoteHas(branch string) bool {
	return strings.Contains(e.git("ls-remote", "--heads", "origin"), "refs/heads/"+branch)
}

func TestFix_LandsTheFixOfAnIssueAsAPullRequestAfterPostPush(t *testing.T) {
	e := newEnv(t, repoFiles(), true)
	base := e.git("rev-parse", "HEAD")
	var pushed []string
	e.cfg.PostPush = func(_ context.Context, branch string) error {
		if e.remoteHas(branch) && len(e.forge.PullRequests()) == 0 {
			pushed = append(pushed, branch)
		}
		return nil
	}

	o := e.fix(e.forge.IssueURL(7), scripted(t, fixSteps()...)...)

	branch, _ := o.res()["branch"].(string)
	if o.code != 0 || o.res()["outcome"] != "landed" || o.res()["closes"] != true || !strings.HasPrefix(branch, "fix/7-") {
		t.Fatalf("%v; want a landed change on the branch of issue 7", o)
	}
	if !slices.Equal(pushed, []string{branch}) {
		t.Errorf("PostPush saw %v, want the branch once, pushed and before any pull request", pushed)
	}
	if prs := e.forge.PullRequests(); len(prs) != 1 || prs[0].Head != branch || e.git("rev-list", "--count", base+".."+branch) != "1" {
		t.Errorf("pull requests %+v; want one for the branch, which holds one commit", prs)
	}
	if e.git("rev-parse", "--abbrev-ref", "HEAD") != "main" || e.git("status", "--porcelain") != "" {
		t.Error("the checkout is not back on the base, clean")
	}
	if !slices.Contains(ledger(t, e.runDir(o)), "fix.brief") || o.artifacts("brief") != 1 {
		t.Errorf("the brief is not recorded and listed: %v", o.Artifacts)
	}
}

func TestFix_APostPushErrorFailsTheRunAndOpensNoPullRequest(t *testing.T) {
	e := newEnv(t, repoFiles(), true)
	e.cfg.PostPush = func(context.Context, string) error { return errors.New("hub is unavailable: it answered 503") }

	o := e.fix(e.forge.IssueURL(7), scripted(t, fixSteps()...)...)

	if o.code != 1 || o.category() != "git" || o.Error.Retryable || !strings.Contains(o.message(), "fix/7-") || !strings.Contains(o.message(), "503") {
		t.Errorf("%v; want a git failure, not retryable, that names the branch and why", o)
	}
	if prs := e.forge.PullRequests(); len(prs) != 0 {
		t.Errorf("pull requests %+v, want none", prs)
	}
}

func TestFix_RefusesBeforeABoxIsOpened(t *testing.T) {
	for name, tc := range map[string]struct {
		input    func(e *env) string
		args     []string
		prepare  func(e *env)
		code     int
		category string
		mention  string
	}{
		"a text":                          {func(*env) string { return "the greeting says helo" }, []string{"--repo", "o/r"}, nil, 2, "usage", "af triage"},
		"a pull request":                  {func(e *env) string { return strings.Replace(e.forge.IssueURL(7), "/issues/", "/pull/", 1) }, nil, nil, 2, "usage", "pull or merge request"},
		"a landing that is none of three": {nil, []string{"--land", "bogus"}, nil, 2, "usage", "bogus"},
		"an unknown brain":                {nil, []string{"--brain", "nosuch"}, nil, 2, "usage", "nosuch"},
		"a brain with no credential":      {nil, []string{"--brain", "agentkit"}, nil, 1, "auth", "ANTHROPIC_API_KEY"},
		"a dirty tree":                    {nil, nil, func(e *env) { testutil.WriteFile(t, e.dir, "dirty.txt", "x") }, 1, "preflight", "dirty.txt"},
		"--land branch with no origin":    {nil, []string{"--land", "branch"}, func(e *env) { e.git("remote", "remove", "origin") }, 1, "preflight", "origin"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, repoFiles(), true)
			spy := e.spy(&spyBrain{})
			if tc.prepare != nil {
				tc.prepare(e)
			}
			input := e.forge.IssueURL(7)
			if tc.input != nil {
				input = tc.input(e)
			}

			o := e.fix(input, append([]string{"--brain", "spy"}, tc.args...)...)

			if o.code != tc.code || o.category() != tc.category || !strings.Contains(o.message(), tc.mention) && !strings.Contains(o.hint().Env, tc.mention) {
				t.Fatalf("%v; want exit code %d, category %s, mentioning %q", o, tc.code, tc.category, tc.mention)
			}
			if spy.made != 0 || e.opened.Load() != 0 || len(e.forge.Writes()) != 0 || e.git("branch", "--list", "fix/*") != "" {
				t.Errorf("%d brains made, %d boxes, %d writes, branches %q: a refused run does nothing", spy.made, e.opened.Load(), len(e.forge.Writes()), e.git("branch", "--list", "fix/*"))
			}
		})
	}
}

func TestFix_ARepositoryWithNoGateIsRefusedInItsBoxBeforeTheBrainRuns(t *testing.T) {
	e := newEnv(t, map[string]string{"README.md": "# no gate\n"}, true)
	spy := e.spy(&spyBrain{})

	o := e.fix(e.forge.IssueURL(7), "--brain", "spy")

	if o.code != 1 || o.category() != "preflight" || o.hint().Flag != "--no-verify" || e.opened.Load() != 1 || spy.runs != 0 {
		t.Errorf("%v, %d boxes, %d runs; want a preflight failure with the hint --no-verify, from the one box, and no run", o, e.opened.Load(), spy.runs)
	}
}

func TestFix_AQuestionEndsTheRunAndTheAnswerResumesIt(t *testing.T) {
	e := newEnv(t, repoFiles(), true)
	head := e.git("rev-parse", "HEAD")
	question := `{"question":{"text":"Which greeting?","options":["x","y","z"]}}`

	o := e.fix(e.forge.IssueURL(7), scripted(t, writeStep("src/greet.sh", "echo half\n"), submitStep(question, "accepted"))...)

	nh := o.NeedsHuman
	if o.code != 3 || nh == nil || nh.Question != "Which greeting?" || len(nh.Options) != 3 || nh.Options[2].ID != "C" || nh.Resume.Flag != "--context" {
		t.Fatalf("%v, needs_human %+v; want the question, its options A to C, and how to answer", o, nh)
	}
	if !slices.Contains(ledger(t, e.runDir(o)), "fix.question") || e.git("status", "--porcelain") != "" || e.git("rev-parse", "HEAD") != head || len(e.forge.Writes()) != 0 {
		t.Errorf("ledger %v: a run that asks records the question and changes nothing", ledger(t, e.runDir(o)))
	}

	argv := slices.DeleteFunc(slices.Clone(nh.Resume.Argv), func(a string) bool { return a == "af" })
	again := e.af(append(argv, nh.Resume.Flag, "B")...)

	if task := taskOf(t, e.runDir(again)); !strings.Contains(task, "Which greeting?") || !strings.Contains(task, "\nB\n") || again.warned("question_not_found") {
		t.Errorf("the run that answers does not hold the question and the answer:\n%s", task)
	}
}

func TestFix_TheQuestionsOfTheCallerFrameTheContext(t *testing.T) {
	e := newEnv(t, repoFiles(), true)
	spy := e.spy(&spyBrain{})
	e.cfg.Earlier = &Earlier{Questions: []*toolio.Question{nil, {Text: "Which greeting?", Options: []string{"hello", "hi"}}}}

	o := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--context", "the report is old", "--context", "B")

	if spy.brief == nil || o.warned("question_not_found") {
		t.Fatalf("%v; want a run of the brain, with nothing to warn of", o)
	}
	for _, want := range []string{"Which greeting?", "B. hi", "Material from a person", "the report is old", "A person's answer"} {
		if !strings.Contains(spy.brief.Task, want) {
			t.Errorf("the task block lacks %q:\n%s", want, spy.brief.Task)
		}
	}

	e.cfg.Earlier = nil // af's own search finds no run that asked
	if o := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--context", "B"); !o.warned("question_not_found") {
		t.Errorf("warnings %v, want question_not_found for an answer that no run asked about", o.Warnings)
	}
}

func TestFix_TheBrainGetsTheBriefAndTheBoundsOfTheRunInALabelledBox(t *testing.T) {
	e := newEnv(t, repoFiles(), true)
	spy := e.spy(&spyBrain{})
	e.cfg.BoxLabels = map[string]string{"af.job": "job_1"}

	o := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--budget", "3.5", "--max-turns", "17")

	b := spy.brief
	if spy.runs != 1 || b == nil || b.MaxCostUSD != 3.5 || b.MaxTurns != 17 || b.Attempt != 1 || b.Kind.Name != "fix_report" {
		t.Fatalf("%v; the brain ran %d times with %+v, want one round with the bounds of the flags", o, spy.runs, b)
	}
	if brief := testutil.ReadFile(t, filepath.Join(e.runDir(o), "brief.md")); b.Prefix == "" || !strings.HasPrefix(brief, b.Prefix) {
		t.Error("the brain was given a prefix that is not the one of brief.md")
	}
	if l := b.Box.Spec().Labels; l["af.tool"] != "fix" || l["af.run_id"] != *o.RunID || l["af.job"] != "job_1" {
		t.Errorf("the box is labelled %v", l)
	}
}

func TestFix_AClosedIssueIsWarnedOfAndTheRunGoesOn(t *testing.T) {
	e := newEnv(t, repoFiles(), true)
	e.forge.AddIssue(fakeforge.Issue{Number: 9, Title: "Old", Body: "It broke.", State: "closed"})
	spy := e.spy(&spyBrain{})

	o := e.fix(e.forge.IssueURL(9), "--brain", "spy", "--land", "none")

	if !o.warned("issue_closed") || spy.runs != 1 {
		t.Errorf("%v, %d runs; want the warning issue_closed and a run of the brain", o, spy.runs)
	}
	if _, err := os.Stat(filepath.Join(e.runDir(o), "fix.json")); err != nil {
		t.Error(err)
	}
}
