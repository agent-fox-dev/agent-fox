package fix

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// proven is what af measured of a change that passes and is proven by its tests.
func proven() Measurement {
	return Measurement{
		Verdict: checks.VerdictPass, Landable: true, Lands: true, Closes: true,
		After:       &GateRef{GateID: "after", Passed: true},
		Rows:        []checks.Row{{Name: "make check", Before: "pass", After: "pass", Verdict: checks.VerdictPass}},
		RevertCheck: &RevertRef{Outcome: "proven", Reason: "tests_fail_without_the_change"},
		Changed:     []string{"src/greet.sh"},
	}
}

// notProven is the same without the proof.
func notProven() Measurement {
	m := proven()
	m.Closes, m.RevertCheck = false, &RevertRef{Outcome: "not_proven", Reason: "tests_pass_without_the_change"}
	return m
}

// unverified is what af measured under --no-verify: nothing.
func unverified() Measurement {
	return Measurement{Verdict: checks.VerdictUnverified, Lands: true, Changed: []string{"src/greet.sh"}}
}

// addX is a report of a feature.
func addX() Report {
	return Report{
		Type: "feat", Subject: "Add x", RootCause: "x was missing", Change: strings.Repeat("c", 3000),
		Files:    []File{{Path: "src/greet.sh", Why: "the word"}},
		Criteria: []Criterion{{Criterion: "x exists", Status: "met", Evidence: "the test"}},
	}
}

// written is the report.md of a landing.
type written struct{ text string }

func (w *written) write(text string) error { w.text = text; return nil }

// land lands the change of the world with the parameters of a verified, proven run, and
// those mutate sets.
func (w *landWorld) land(t *testing.T, input Input, mode string, mutate func(*LandParams)) (LandResult, *written) {
	t.Helper()
	got := &written{}
	p := LandParams{
		Git: w.repo.Git, State: w.state, Input: input, Report: addX(), Mode: mode, Forge: w.client, Target: w.target,
		Measured: proven(), Brain: "scripted", CostUSD: 0, CostSource: "none", Reporter: w.rep, WriteReport: got.write,
	}
	if mutate != nil {
		mutate(&p)
	}
	res, err := Land(context.Background(), p)
	if err != nil {
		t.Fatalf("Land: %v", err)
	}
	return res, got
}

func (w *landWorld) onBase(t *testing.T) {
	t.Helper()
	if branch, _ := w.repo.Git.CurrentBranch(context.Background()); branch != "main" {
		t.Errorf("checked out %q, want the base", branch)
	}
	if dirty, _ := w.repo.Git.DirtyFiles(context.Background()); len(dirty) != 0 {
		t.Errorf("the tree is dirty: %v", dirty)
	}
}

var fullHash = regexp.MustCompile(`^[0-9a-f]{40}$`)

// TestTheLandingCommitHasTheSpecifiedLayoutAndOneCommitAboveTheBase_TS_09_55 verifies
// TS-09-55.
// Verifies: 09-REQ-8.3
func TestTheLandingCommitHasTheSpecifiedLayoutAndOneCommitAboveTheBase_TS_09_55(t *testing.T) {
	for name, tc := range map[string]struct {
		measured Measurement
		line     string
	}{"proven": {proven(), "Closes https://github.com/o/r/issues/7"}, "not proven": {notProven(), "Refs https://github.com/o/r/issues/7"}} {
		t.Run(name, func(t *testing.T) {
			w := newLandWorld(t, fakeforge.GitHub)
			w.issueInput()
			hook := filepath.Join(w.repo.Dir, ".git", "hooks", "pre-commit")
			if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch \"$(git rev-parse --git-dir)/hook-ran\"\n"), 0o755); err != nil {
				t.Fatal(err)
			}

			res, _ := w.land(t, Input{Kind: "issue", Origin: w.state.Issue.URL, Body: "x"}, LandNone, func(p *LandParams) { p.Measured = tc.measured })

			msg := strings.TrimSpace(gitDo(t, w.repo.Dir, "log", "-1", "--format=%B", res.Branch))
			paras := strings.Split(msg, "\n\n")
			if len(paras) != 4 || paras[0] != "feat: Add x" || paras[1] != strings.Repeat("c", 2000) || paras[2] != tc.line || paras[3] != "Agent-Fox-Run: 20261020_100000_aaaaaa" {
				t.Errorf("message paragraphs = %q, want the subject, 2000 bytes of the change, %q and the trailer", paras, tc.line)
			}
			if n := strings.Count(strings.TrimSpace(gitDo(t, w.repo.Dir, "rev-list", w.state.BaseHead+".."+res.Branch)), "\n") + 1; n != 1 {
				t.Errorf("%d commits above the base, want one", n)
			}
			if !fullHash.MatchString(res.Commit) || res.Commit != strings.TrimSpace(gitDo(t, w.repo.Dir, "rev-parse", res.Branch)) {
				t.Errorf("commit = %q, want the full hash of the branch", res.Commit)
			}
			if _, err := os.Stat(filepath.Join(w.repo.Dir, ".git", "hook-ran")); err != nil {
				t.Error("the hooks of the repository did not run: a landing commit is made with them")
			}
			if res.Branch != "fix/7-greeting-wrong" || res.Outcome != OutcomeLanded {
				t.Errorf("branch %q, outcome %q", res.Branch, res.Outcome)
			}
		})
	}

	t.Run("no issue, and a change that is not valid to cut anywhere", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)

		res, _ := w.land(t, fileInput, LandNone, func(p *LandParams) { p.Report.Change = strings.Repeat("é", 1500) })

		paras := strings.Split(strings.TrimSpace(gitDo(t, w.repo.Dir, "log", "-1", "--format=%B", res.Branch)), "\n\n")
		if len(paras) != 3 || paras[0] != "feat: Add x" || !utf8.ValidString(paras[1]) || len(paras[1]) != 2000 || paras[2] != "Agent-Fox-Run: 20261020_100000_aaaaaa" {
			t.Errorf("paragraphs = %q, want no line about an issue and a change cut at a character", paras)
		}
		if res.Branch != "fix/x" {
			t.Errorf("branch = %q, want the slug of the subject", res.Branch)
		}
	})
}

// TestAFailingCommitHookParksTheWork_TS_09_56 verifies TS-09-56.
// Verifies: 09-REQ-8.4
func TestAFailingCommitHookParksTheWork_TS_09_56(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	w.issueInput()
	hook := filepath.Join(w.repo.Dir, ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'lint says no' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, _ := w.land(t, Input{Kind: "issue", Origin: w.state.Issue.URL, Body: "x"}, LandPR, nil)

	if res.Outcome != OutcomeParked || res.Status != toolio.StatusUnverified || toolio.ExitCode(res.Status) != 4 {
		t.Errorf("outcome %q, status %q, want parked and unverified", res.Outcome, res.Status)
	}
	msg := strings.TrimSpace(gitDo(t, w.repo.Dir, "log", "-1", "--format=%B", res.Branch))
	if !strings.HasPrefix(msg, "wip: Add x\n") || !strings.Contains(msg, "Parked by af fix: verdict pass\n") || !strings.Contains(msg, "Refs https://github.com/o/r/issues/7") || !strings.HasSuffix(msg, "Agent-Fox-Run: 20261020_100000_aaaaaa") {
		t.Errorf("message = %q, want the commit that parks", msg)
	}
	if i := slices.Index(w.rep.warnings, WarnCommitHookFailed); i < 0 || !strings.Contains(w.rep.messages[i], "lint says no") || !slices.Contains(w.rep.warnings, WarnFixNotLanded) {
		t.Errorf("warnings = %v %v, want commit_hook_failed with what the hook said, and fix_not_landed", w.rep.warnings, w.rep.messages)
	}
	if branches := strings.Fields(gitDo(t, w.repo.Dir, "for-each-ref", "--format=%(refname:short)", "refs/heads")); len(branches) != 2 {
		t.Errorf("branches = %v, want the base and the one branch the landing made", branches)
	}
	if prs := w.forge.PullRequests(); len(prs) != 1 || !prs[0].Draft || !strings.Contains(prs[0].Body, "Refs https://github.com/o/r/issues/7") || !strings.HasPrefix(prs[0].Title, "wip: ") {
		t.Errorf("pull requests = %+v, want a draft that refers to the issue", prs)
	}
	// What a reader of the parked work is told is that a hook refused it, and not that the gate did:
	// the gate said pass.
	if !strings.Contains(msg, "The commit was refused by a hook of the repository.") {
		t.Errorf("message = %q, want it to say that a hook refused the commit", msg)
	}
	if prs := w.forge.PullRequests(); len(prs) != 1 || !strings.Contains(prs[0].Body, "a hook of the repository refused the commit") || strings.Contains(prs[0].Body, "did not pass the gate") {
		t.Errorf("pull requests = %+v, want a body that says a hook refused the commit and not that the gate failed", prs)
	}
	if i := slices.Index(w.rep.warnings, WarnFixNotLanded); i < 0 || !strings.Contains(w.rep.messages[i], "refused by a hook") {
		t.Errorf("warnings = %v %v, want fix_not_landed to say a hook refused the commit", w.rep.warnings, w.rep.messages)
	}
	w.onBase(t)
}

// TestAPushThatFailsKeepsTheBranchAndFails_TS_09_57 verifies TS-09-57.
// Verifies: 09-REQ-8.5
func TestAPushThatFailsKeepsTheBranchAndFails_TS_09_57(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	pushes := 0
	rejecting := gitx.New(w.repo.Dir, func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
		if slices.Contains(argv, "push") {
			pushes++
			return "error: failed to push some refs: the remote hung up", 1, nil
		}
		return gitx.ExecRunner(ctx, dir, argv, stdin...)
	})
	rejecting.SetSleep(func(time.Duration) {})

	_, err := Land(context.Background(), LandParams{
		Git: rejecting, State: w.state, Input: Input{Kind: "text", Body: "x"}, Report: addX(), Mode: LandPR, Forge: w.client, Target: w.target,
		Measured: proven(), Reporter: w.rep,
	})

	if f := failure(t, err); f.Category != toolio.CategoryGit || toolio.ExitCode(toolio.StatusFailed) != 1 {
		t.Errorf("failure = %+v, want git", f)
	}
	if pushes != 3 || strings.Count(strings.Join(w.rep.warnings, " "), WarnPushRetried) != 2 {
		t.Errorf("%d pushes and warnings %v, want 3 attempts and push_retried for each of the 2 retries", pushes, w.rep.warnings)
	}
	branch := w.rep.artifact("branch").Name
	var hint bool
	for _, n := range w.rep.next {
		if slices.Equal(n.Argv, []string{"git", "push", "-u", "origin", branch}) {
			hint = true
		}
	}
	if !hint || branch == "" {
		t.Errorf("next = %v, want git push -u origin %s", w.rep.next, branch)
	}
	if e := w.rep.effect(EffectPush); e.State != toolio.StateFailed {
		t.Errorf("effects = %v, want the push failed", w.rep.effects)
	}
	if shown := gitDo(t, w.repo.Dir, "log", "-1", "--format=%s", branch); strings.TrimSpace(shown) != "feat: Add x" {
		t.Errorf("the commit of the branch is %q: it must be kept locally", shown)
	}
	if w.forge.Count(fakeforge.OpPullRequest) != 0 {
		t.Error("a pull request was opened for a branch that is not pushed")
	}
	w.onBase(t)
}

// TestThePullRequestIsDraftedForUnmetCriteriaAndNoVerifyAndItsFailureIsAWarning_TS_09_58
// verifies TS-09-58.
// Verifies: 09-REQ-8.6
func TestThePullRequestIsDraftedForUnmetCriteriaAndNoVerifyAndItsFailureIsAWarning_TS_09_58(t *testing.T) {
	t.Run("a partial criterion", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)

		res, report := w.land(t, w.issue(), LandPR, func(p *LandParams) {
			p.Report.Criteria = []Criterion{{Criterion: "a", Status: "met"}, {Criterion: "second half", Status: "partial"}, {Criterion: "b", Status: "unmet"}}
		})

		prs := w.forge.PullRequests()
		if len(prs) != 1 || !prs[0].Draft || prs[0].Title != "feat: Add x" || prs[0].Head != res.Branch || prs[0].Base != "main" {
			t.Fatalf("pull requests = %+v, want a draft titled feat: Add x from %s to main", prs, res.Branch)
		}
		if i := slices.Index(w.rep.warnings, WarnCriteriaUnmet); i < 0 || !strings.Contains(w.rep.messages[i], "second half") || res.PullRequest == nil || !res.PullRequest.Draft {
			t.Errorf("warnings = %v, result %+v, want criteria_unmet naming the criterion", w.rep.warnings, res)
		}
		if prs[0].Body != report.text || report.text == "" {
			t.Errorf("the body of the pull request is not report.md")
		}
	})
	t.Run("everything met and verified", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)

		res, _ := w.land(t, w.issue(), LandPR, nil)

		prs := w.forge.PullRequests()
		if len(prs) != 1 || prs[0].Draft || res.PullRequest == nil || res.PullRequest.Draft || slices.Contains(w.rep.warnings, WarnCriteriaUnmet) {
			t.Errorf("pull requests = %+v, warnings %v, want one that is ready for review", prs, w.rep.warnings)
		}
	})
	t.Run("not verified", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		w.state.NoVerify = true

		res, _ := w.land(t, w.issue(), LandPR, func(p *LandParams) { p.Measured = unverified() })

		prs := w.forge.PullRequests()
		if len(prs) != 1 || !prs[0].Draft || !strings.Contains(prs[0].Body, "Refs ") || strings.Contains(prs[0].Body, "Closes ") || res.PullRequest == nil {
			t.Errorf("pull requests = %+v, want a draft that says Refs", prs)
		}
		if slices.Contains(w.rep.warnings, WarnCriteriaUnmet) {
			t.Errorf("warnings = %v: nothing was unmet", w.rep.warnings)
		}
	})
	t.Run("a forge that refuses it", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		w.forge.Fail(fakeforge.OpPullRequest, fakeforge.Failure{Status: 500, Message: "boom"})

		res, _ := w.land(t, w.issue(), LandPR, nil)

		if res.Outcome != OutcomeLanded || res.Status != toolio.StatusDone || res.PullRequest != nil || !res.Pushed {
			t.Errorf("result = %+v, want the landing done, pushed, with no pull request", res)
		}
		if i := slices.Index(w.rep.warnings, WarnPullRequestFailed); i < 0 || !strings.Contains(w.rep.messages[i], "boom") {
			t.Errorf("warnings = %v %v, want pull_request_failed with the error of the forge", w.rep.warnings, w.rep.messages)
		}
		if e := w.rep.effect(EffectCreatePullRequest); e.State != toolio.StateFailed {
			t.Errorf("effects = %v", w.rep.effects)
		}
		w.onBase(t)
	})
}

// TestAfterLandingTheCheckoutIsOnTheBaseAndNoVerifyIsUnverified_TS_09_60 verifies TS-09-60.
// Verifies: 09-REQ-8.8
func TestAfterLandingTheCheckoutIsOnTheBaseAndNoVerifyIsUnverified_TS_09_60(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	w.issueInput()
	issue := Input{Kind: "issue", Origin: w.state.Issue.URL, Body: "x"}

	res, _ := w.land(t, issue, LandPR, nil)

	if res.Outcome != OutcomeLanded || res.Status != toolio.StatusDone || toolio.ExitCode(res.Status) != 0 || !res.Pushed || res.PullRequest == nil {
		t.Errorf("result = %+v, want landed, done, pushed, with a pull request", res)
	}
	if slices.Contains(w.rep.warnings, WarnNotVerified) {
		t.Errorf("warnings = %v", w.rep.warnings)
	}
	w.onBase(t)
	if head := strings.TrimSpace(gitDo(t, w.repo.Dir, "rev-parse", "HEAD")); head != w.state.BaseHead {
		t.Errorf("HEAD = %s, want the base", head)
	}
	if data, _ := os.ReadFile(filepath.Join(w.repo.Dir, "src", "greet.sh")); string(data) != "echo hello\n" {
		t.Errorf("greet.sh = %q on the base, want its own content", data)
	}
	for _, kind := range []string{EffectBranch, EffectCommit, EffectPush, EffectCreatePullRequest} {
		if e := w.rep.effect(kind); e.State != toolio.StateDone {
			t.Errorf("effect %s = %+v, want done", kind, e)
		}
	}

	w2 := newLandWorld(t, fakeforge.GitHub)
	w2.issueInput()
	w2.state.NoVerify = true
	res2, _ := w2.land(t, Input{Kind: "issue", Origin: w2.state.Issue.URL, Body: "x"}, LandPR, func(p *LandParams) { p.Measured = unverified() })
	if res2.Outcome != OutcomeLanded || res2.Status != toolio.StatusUnverified || toolio.ExitCode(res2.Status) != 4 || !slices.Contains(w2.rep.warnings, WarnNotVerified) {
		t.Errorf("--no-verify: result %+v, warnings %v, want landed, unverified and not_verified", res2, w2.rep.warnings)
	}
	w2.onBase(t)
}

// TestLandNoneMakesTheLocalBranchAndNothingElse_TS_09_64 verifies TS-09-64.
// Verifies: 09-REQ-9.4
func TestLandNoneMakesTheLocalBranchAndNothingElse_TS_09_64(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	w.issueInput()

	res, report := w.land(t, Input{Kind: "issue", Origin: w.state.Issue.URL, Body: "x"}, LandNone, nil)

	if res.Pushed || res.PullRequest != nil || w.remoteHas(t, res.Branch) || len(w.forge.Requests()) != 0 {
		t.Errorf("result %+v, %d requests: none mode writes nothing beyond the local branch", res, len(w.forge.Requests()))
	}
	if gitDo(t, w.repo.Dir, "rev-parse", "--verify", res.Branch) == "" || !fullHash.MatchString(res.Commit) {
		t.Errorf("no local branch or commit: %+v", res)
	}
	if e := w.rep.effect(EffectPush); e.Kind != "" {
		t.Errorf("effects = %v, want no push", w.rep.effects)
	}
	if report.text == "" {
		t.Error("report.md was not written: the body is kept in every mode")
	}
	w.onBase(t)
}

// TestAReportThatCannotBeWrittenIsAWarning guards that the body is kept beside the run and
// that failing to keep it stops nothing.
func TestAReportThatCannotBeWrittenIsAWarning(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)

	res, _ := w.land(t, fileInput, LandNone, func(p *LandParams) {
		p.WriteReport = func(string) error { return os.ErrPermission }
	})

	if res.Outcome != OutcomeLanded || !slices.Contains(w.rep.warnings, WarnReportUnwritable) {
		t.Errorf("result %+v, warnings %v, want a landing and the warning", res, w.rep.warnings)
	}
}

// TestEachUnmetStatusDraftsThePullRequestAndNotApplicableDoesNot guards which criteria
// make a pull request a draft.
func TestEachUnmetStatusDraftsThePullRequestAndNotApplicableDoesNot(t *testing.T) {
	for status, want := range map[string]bool{"unmet": true, "partial": true, "met": false, "not_applicable": false} {
		t.Run(status, func(t *testing.T) {
			w := newLandWorld(t, fakeforge.GitHub)

			w.land(t, Input{Kind: "text", Body: "x"}, LandPR, func(p *LandParams) { p.Report.Criteria = []Criterion{{Criterion: "c", Status: status}} })

			if prs := w.forge.PullRequests(); len(prs) != 1 || prs[0].Draft != want || slices.Contains(w.rep.warnings, WarnCriteriaUnmet) != want {
				t.Errorf("pull requests %+v, warnings %v, want a draft: %v", prs, w.rep.warnings, want)
			}
		})
	}
}

// TestLandingIsAsAskedFor guards what each mode and each kind of change writes: branch mode
// pushes and opens nothing, a change with no text has no empty paragraph, a landing needs no
// place to keep the report, and a change with no issue is not given a pull request.
func TestLandingIsAsAskedFor(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	w.issueInput()

	res, _ := w.land(t, Input{Kind: "issue", Origin: w.state.Issue.URL, Body: "x"}, LandBranch, func(p *LandParams) {
		p.WriteReport = nil
		p.Report.Change = "  \n"
	})

	if !res.Pushed || res.PullRequest != nil || len(w.forge.Requests()) != 0 || !w.remoteHas(t, res.Branch) {
		t.Errorf("result %+v, %d requests: branch mode pushes and asks nothing of the forge", res, len(w.forge.Requests()))
	}
	paras := strings.Split(strings.TrimSpace(gitDo(t, w.repo.Dir, "log", "-1", "--format=%B", res.Branch)), "\n\n")
	if len(paras) != 3 || paras[1] != "Closes https://github.com/o/r/issues/7" {
		t.Errorf("paragraphs = %q, want no paragraph for a change that says nothing", paras)
	}
}

// TestACommitThatCannotBeMadeAtAllIsAFailureAndLeavesTheChangeInTheTree guards the end of
// the road of a landing: when the commit fails with the hooks and without them, the branch is
// taken away and the change stays in the working tree of the base.
func TestACommitThatCannotBeMadeAtAllIsAFailureAndLeavesTheChangeInTheTree(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	broken := gitx.New(w.repo.Dir, func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
		if slices.Contains(argv, "commit") {
			return "fatal: " + strings.Repeat("the object database is gone ", 40), 128, nil
		}
		return gitx.ExecRunner(ctx, dir, argv, stdin...)
	})

	_, err := Land(context.Background(), LandParams{Git: broken, State: w.state, Input: Input{Kind: "text", Body: "x"}, Report: addX(), Mode: LandNone,
		Forge: w.client, Target: w.target, Measured: proven(), Reporter: w.rep})

	if f := failure(t, err); f.Category != toolio.CategoryGit || !strings.Contains(f.Message, "cannot commit") {
		t.Errorf("failure = %v, want git", err)
	}
	if e := w.rep.effect(EffectCommit); e.State != toolio.StateFailed {
		t.Errorf("effects = %v, want the commit failed", w.rep.effects)
	}
	if branches := strings.Fields(gitDo(t, w.repo.Dir, "for-each-ref", "--format=%(refname:short)", "refs/heads")); !slices.Equal(branches, []string{"main"}) {
		t.Errorf("branches = %v, want the branch taken away", branches)
	}
	if data, _ := os.ReadFile(filepath.Join(w.repo.Dir, "src", "greet.sh")); string(data) != "echo hello, world\n" {
		t.Errorf("greet.sh = %q: the change must still be in the working tree", data)
	}
}

// TestWhatAHookSaidIsInTheWarningButNotAllOfIt guards the warning of a hook that stopped the
// commit: it carries the beginning of what the hook said, on one line.
func TestWhatAHookSaidIsInTheWarningButNotAllOfIt(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	hook := filepath.Join(w.repo.Dir, ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ni=0; while [ $i -lt 400 ]; do echo \"complaint number $i\" >&2; i=$((i+1)); done\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	w.land(t, Input{Kind: "text", Body: "x"}, LandNone, nil)

	i := slices.Index(w.rep.warnings, WarnCommitHookFailed)
	if i < 0 || len(w.rep.messages[i]) > 600 || strings.Contains(w.rep.messages[i], "\n") || !strings.Contains(w.rep.messages[i], "complaint number") {
		t.Errorf("warning = %q", w.rep.messages[i])
	}
}

// TestTheMessageOfACommitLeavesOutWhatIsEmpty guards the layout of the message itself, before
// git cleans it up: no paragraph for a change or an issue that is not there.
func TestTheMessageOfACommitLeavesOutWhatIsEmpty(t *testing.T) {
	if got := landMessage("fix", "s", "  \n", "", false, "id"); got != "fix: s\n\nAgent-Fox-Run: id\n" {
		t.Errorf("message = %q", got)
	}
	if got := landMessage("feat", "s", "why\n", "u", true, "id"); got != "feat: s\n\nwhy\n\nCloses u\n\nAgent-Fox-Run: id\n" {
		t.Errorf("message = %q", got)
	}
	if got := landMessage("fix", "s", "", "u", false, "id"); got != "fix: s\n\nRefs u\n\nAgent-Fox-Run: id\n" {
		t.Errorf("message = %q", got)
	}
}

// TestAReportWithNoTypeIsAFix guards the type a commit gets when the report names none.
func TestAReportWithNoTypeIsAFix(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)

	res, _ := w.land(t, fileInput, LandNone, func(p *LandParams) { p.Report.Type = "" })

	if first, _, _ := strings.Cut(gitDo(t, w.repo.Dir, "log", "-1", "--format=%B", res.Branch), "\n"); first != "fix: Add x" {
		t.Errorf("subject line = %q", first)
	}
}

// TestThePullRequestBodyIsWithinWhatTheForgeTakesAndNamesTheRun guards the body that is sent:
// it fits the limit of the forge, whatever the brain reported, and its footer says what the run
// used.
func TestThePullRequestBodyIsWithinWhatTheForgeTakesAndNamesTheRun(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)

	w.land(t, Input{Kind: "text", Body: "x"}, LandPR, func(p *LandParams) {
		p.Report.RootCause = strings.Repeat("root cause words ", 8000)
		p.Brain, p.CostUSD, p.CostSource = "agentkit", 1.5, "measured"
	})

	prs := w.forge.PullRequests()
	if len(prs) != 1 || len(prs[0].Body) > 65536 || !strings.Contains(prs[0].Body, BodyCutMarker) {
		t.Fatalf("pull requests = %d, body of %d bytes, want one that fits and says it was cut", len(prs), len(prs[0].Body))
	}
	if !strings.Contains(prs[0].Body, "brain `agentkit` · cost $1.5000 (measured)") || !strings.Contains(prs[0].Body, w.state.RunID) {
		t.Errorf("the footer of the body = %q", prs[0].Body[max(0, len(prs[0].Body)-160):])
	}
}

// TestTwoFailuresAreBothReported guards a push that fails when the checkout cannot go back
// either: the person is told of both.
func TestTwoFailuresAreBothReported(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	doomed := gitx.New(w.repo.Dir, func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
		if slices.Contains(argv, "push") {
			return "remote: Permission denied to user", 128, nil
		}
		if slices.Contains(argv, "checkout") && slices.Contains(argv, "main") {
			return "fatal: cannot go back", 128, nil
		}
		return gitx.ExecRunner(ctx, dir, argv, stdin...)
	})

	_, err := Land(context.Background(), LandParams{Git: doomed, State: w.state, Input: Input{Kind: "text", Body: "x"}, Report: addX(), Mode: LandBranch,
		Forge: w.client, Target: w.target, Measured: proven(), Reporter: w.rep})

	if err == nil || !strings.Contains(err.Error(), "could not be pushed") || !strings.Contains(err.Error(), "cannot go back") {
		t.Errorf("error = %v, want both failures", err)
	}
}
