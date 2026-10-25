package fix

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// proven is what af measured of a change that passes and is proven by its tests.
func proven() Measurement {
	return Measurement{
		Verdict: checks.VerdictPass, Landable: true, Lands: true, Closes: true, After: &GateRef{GateID: "after", Passed: true},
		Rows:        []checks.Row{{Name: "make check", Before: "pass", After: "pass", Verdict: checks.VerdictPass}},
		RevertCheck: &RevertRef{Outcome: "proven", Reason: "tests_fail_without_the_change"}, Changed: []string{"src/greet.sh"},
	}
}

// addX is a report of a feature.
func addX() Report {
	return Report{
		Type: "feat", Subject: "Add x", RootCause: "x was missing", Change: strings.Repeat("c", 3000),
		Files:    []File{{Path: "src/greet.sh", Why: "the word"}},
		Criteria: []Criterion{{Criterion: "x exists", Status: "met", Evidence: "the test"}},
	}
}

// landing is the parameters of a landing of the world's change, verified and proven, with
// report.md kept in kept.
func (w *landWorld) landing(input Input, mode string, kept *string) LandParams {
	return LandParams{
		Git: w.repo.Git, State: w.state, Input: input, Report: addX(), Mode: mode, Forge: w.client, Target: w.target,
		Measured: proven(), Brain: "scripted", CostSource: toolio.CostNone, Reporter: w.rep,
		WriteReport: func(text string) error { *kept = text; return nil },
	}
}

// land lands the change with the landing as edit leaves it, and fails the test when it fails.
func (w *landWorld) land(t *testing.T, input Input, mode string, edit func(*LandParams)) (LandResult, string) {
	t.Helper()
	var kept string
	p := w.landing(input, mode, &kept)
	if edit != nil {
		edit(&p)
	}
	res, err := Land(context.Background(), p)
	if err != nil {
		t.Fatalf("Land: %v", err)
	}
	return res, kept
}

var textInput = Input{Kind: "text", Body: "the greeting says helo"}

func TestBranchName_IsTheIssueAndTheSlugOfTheTitle(t *testing.T) {
	for _, tc := range []struct {
		number int
		title  string
		want   string
	}{{42, "Crash on empty input", "fix/42-crash-on-empty-input"}, {0, "Handle nil", "fix/handle-nil"}, {0, "Fix the bug", "fix/issue"}} {
		if got := BranchName(tc.number, tc.title); got != tc.want {
			t.Errorf("BranchName(%d, %q) = %q, want %q", tc.number, tc.title, got, tc.want)
		}
	}
}

func TestMakeBranch_MakesANameNoBranchHasAtTheCommitTheRunBeganAt(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	ctx := context.Background()
	testutil.WriteFile(t, w.repo.Dir, "later.txt", "later\n")
	w.repo.git(t, "add", "later.txt")
	w.repo.git(t, "commit", "-q", "-m", "later", "later.txt")
	w.repo.git(t, "branch", "fix/handle-nil")
	w.repo.git(t, "push", "-q", "origin", "HEAD:refs/heads/fix/handle-nil-2")

	branch, err := MakeBranch(ctx, w.repo.Git, w.rep, 0, "Handle nil", w.state.BaseHead)

	if err != nil || branch != "fix/handle-nil-3" {
		t.Fatalf("MakeBranch = %q, %v, want a name that neither a local nor a remote branch has", branch, err)
	}
	if now, _ := w.repo.Git.CurrentBranch(ctx); now != branch || w.repo.git(t, "rev-parse", "HEAD") != w.state.BaseHead || w.repo.read(t, "src/greet.sh") != "echo hello, world\n" {
		t.Errorf("checked out %q at %s, want the branch at the base with the change carried along", now, w.repo.git(t, "rev-parse", "HEAD"))
	}
	if w.rep.artifact("branch").Name != branch || w.rep.effect(EffectBranch) != (toolio.SideEffect{Kind: EffectBranch, Target: branch, State: toolio.StateDone}) {
		t.Errorf("artifacts %v, effects %v, want the branch", w.rep.artifacts, w.rep.effects)
	}
	if _, err := MakeBranch(ctx, w.repo.Git, w.rep, 0, "Other", "no-such-commit"); failure(t, err).Category != toolio.CategoryGit || w.rep.effect(EffectBranch).State != toolio.StateFailed {
		t.Errorf("a branch from a commit that is not there: %v, effects %v", err, w.rep.effects)
	}
}

// The commit af makes is one above the base, made with the hooks of the repository: the
// subject, the change as far as it goes, the line about the issue and the trailer.
func TestLand_MakesOneCommitWithTheLayoutOfTheMessage(t *testing.T) {
	notProven := proven()
	notProven.Closes = false
	for name, tc := range map[string]struct {
		issue  bool
		edit   func(*LandParams)
		branch string
		paras  []string
	}{
		"proven": {true, nil, "fix/7-greeting-wrong", []string{"feat: Add x", strings.Repeat("c", 2000), "Closes https://github.com/o/r/issues/7", "Agent-Fox-Run: " + runID}},
		"not proven": {true, func(p *LandParams) { p.Measured = notProven }, "fix/7-greeting-wrong",
			[]string{"feat: Add x", strings.Repeat("c", 2000), "Refs https://github.com/o/r/issues/7", "Agent-Fox-Run: " + runID}},
		"no change to say": {true, func(p *LandParams) { p.Report.Change = "  \n" }, "fix/7-greeting-wrong",
			[]string{"feat: Add x", "Closes https://github.com/o/r/issues/7", "Agent-Fox-Run: " + runID}},
		"no issue and no type": {false, func(p *LandParams) { p.Report.Type, p.Report.Change = "", "a"+strings.Repeat("é", 1500) }, "fix/x",
			[]string{"fix: Add x", "a" + strings.Repeat("é", 999), "Agent-Fox-Run: " + runID}},
	} {
		t.Run(name, func(t *testing.T) {
			w := newLandWorld(t, fakeforge.GitHub)
			input := textInput
			if tc.issue {
				input = w.withIssue()
			}
			w.repo.hook(t, "pre-commit", "#!/bin/sh\ntouch \"$(git rev-parse --git-dir)/hook-ran\"\n")

			res, _ := w.land(t, input, LandNone, tc.edit)

			if paras := strings.Split(w.repo.message(t, res.Branch), "\n\n"); strings.Join(paras, "|") != strings.Join(tc.paras, "|") || !utf8.ValidString(paras[1]) {
				t.Errorf("the message is %q, want %q", paras, tc.paras)
			}
			if res.Branch != tc.branch || res.Outcome != OutcomeLanded || res.Status != toolio.StatusDone || res.Commit != w.repo.git(t, "rev-parse", res.Branch) || len(res.Commit) != 40 {
				t.Errorf("result = %+v, want landed on %s with the full hash of the commit", res, tc.branch)
			}
			if w.repo.git(t, "rev-list", "--count", w.state.BaseHead+".."+res.Branch) != "1" {
				t.Error("want one commit above the base")
			}
			if _, err := os.Stat(w.repo.Dir + "/.git/hook-ran"); err != nil {
				t.Error("the hooks of the repository did not run")
			}
			w.onBase(t)
		})
	}
}

func TestLand_PushesAndOpensWhatTheModeAsksFor(t *testing.T) {
	for _, tc := range []struct {
		mode         string
		pushed, pull bool
	}{{LandPR, true, true}, {LandBranch, true, false}, {LandNone, false, false}} {
		t.Run(tc.mode, func(t *testing.T) {
			w := newLandWorld(t, fakeforge.GitHub)

			res, kept := w.land(t, w.withIssue(), tc.mode, nil)

			if res.Pushed != tc.pushed || w.repo.remoteHas(t, res.Branch) != tc.pushed || (res.PullRequest != nil) != tc.pull || (w.forge.Count(fakeforge.OpPullRequest) == 1) != tc.pull {
				t.Fatalf("result = %+v, %d requests to the forge", res, len(w.forge.Requests()))
			}
			if kept == "" {
				t.Error("report.md was not written: the body is kept in every mode")
			}
			if tc.pull {
				pr := w.forge.PullRequests()[0]
				if pr.Draft || pr.Title != "feat: Add x" || pr.Head != res.Branch || pr.Base != "main" || pr.Body != kept || res.PullRequest.Draft || res.PullRequest.URL == "" {
					t.Errorf("pull request %+v (%+v), want one that is no draft, from the branch to main, whose body is report.md", pr, res.PullRequest)
				}
				for _, kind := range []string{EffectBranch, EffectCommit, EffectPush, EffectCreatePullRequest} {
					if e := w.rep.effect(kind); e.State != toolio.StateDone {
						t.Errorf("effect %s = %+v, want done", kind, e)
					}
				}
			}
			if w.rep.effect(EffectPush).Kind != "" != tc.pushed {
				t.Errorf("effects = %v", w.rep.effects)
			}
			w.onBase(t)
		})
	}
}

// A criterion the report says is unmet or partial makes the pull request a draft, and so does
// a change that was not verified, which lands unverified.
func TestLand_DraftsThePullRequestOfAChangeThatIsNotAllThere(t *testing.T) {
	unverified := Measurement{Verdict: checks.VerdictUnverified, Lands: true, Changed: []string{"src/greet.sh"}}
	for name, tc := range map[string]struct {
		status   string
		noVerify bool
		draft    bool
		warning  string
	}{
		"unmet":          {StatusUnmet, false, true, WarnCriteriaUnmet},
		"partial":        {StatusPartial, false, true, WarnCriteriaUnmet},
		"met":            {StatusMet, false, false, ""},
		"not applicable": {StatusNotApplicable, false, false, ""},
		"not verified":   {StatusMet, true, true, WarnNotVerified},
	} {
		t.Run(name, func(t *testing.T) {
			w := newLandWorld(t, fakeforge.GitHub)
			w.state.NoVerify = tc.noVerify

			res, _ := w.land(t, w.withIssue(), LandPR, func(p *LandParams) {
				p.Report.Criteria = []Criterion{{Criterion: "second half", Status: tc.status}}
				if tc.noVerify {
					p.Measured = unverified
				}
			})

			prs := w.forge.PullRequests()
			if len(prs) != 1 || prs[0].Draft != tc.draft || res.PullRequest.Draft != tc.draft {
				t.Fatalf("pull requests = %+v, want a draft: %v", prs, tc.draft)
			}
			if msg, ok := w.rep.warning(tc.warning); (tc.warning != "") != ok || (tc.warning == WarnCriteriaUnmet && !strings.Contains(msg, "second half ("+tc.status+")")) {
				t.Errorf("warnings = %v %v, want %q", w.rep.warnings, w.rep.messages, tc.warning)
			}
			if wantStatus := map[bool]toolio.Status{false: toolio.StatusDone, true: toolio.StatusUnverified}[tc.noVerify]; res.Status != wantStatus {
				t.Errorf("status = %q, want %q", res.Status, wantStatus)
			}
			if tc.noVerify && (strings.Contains(prs[0].Body, "Closes ") || !strings.Contains(prs[0].Body, "Refs ")) {
				t.Errorf("an unverified change closes the issue:\n%s", prs[0].Body)
			}
		})
	}
}

// A commit a hook of the repository refuses is parked on the branch, without the hooks, with
// the start of what the hook said on one line.
func TestLand_ACommitAHookRefusesIsParked(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	w.repo.hook(t, "pre-commit", "#!/bin/sh\necho 'lint says no' >&2\ni=0; while [ $i -lt 400 ]; do echo \"complaint number $i\" >&2; i=$((i+1)); done\nexit 1\n")

	res, _ := w.land(t, w.withIssue(), LandPR, nil)

	if res.Outcome != OutcomeParked || res.Status != toolio.StatusUnverified || w.repo.git(t, "rev-list", "--count", "--all", "^main") != "1" {
		t.Errorf("result = %+v, want the one branch the landing made, parked and unverified", res)
	}
	msg := w.repo.message(t, res.Branch)
	inOrder(t, msg, "wip: Add x\n", "Parked by af fix: verdict pass\n", "The commit was refused by a hook of the repository.", "Refs https://github.com/o/r/issues/7", "Agent-Fox-Run: "+runID)
	if hook, _ := w.rep.warning(WarnCommitHookFailed); !strings.Contains(hook, "lint says no complaint number 0") || len(hook) > 600 || strings.Contains(hook, "\n") {
		t.Errorf("commit_hook_failed = %q, want the start of what the hook said, on one line", hook)
	}
	if why, _ := w.rep.warning(WarnFixNotLanded); !strings.Contains(why, "refused by a hook") {
		t.Errorf("fix_not_landed = %q, want it to say a hook refused the commit", why)
	}
	if prs := w.forge.PullRequests(); len(prs) != 1 || !prs[0].Draft || !strings.HasPrefix(prs[0].Title, "wip: ") || !strings.Contains(prs[0].Body, "a hook of the repository refused the commit") || strings.Contains(prs[0].Body, "did not pass the gate") {
		t.Errorf("pull requests = %+v, want a draft that says a hook refused the commit", prs)
	}
	w.onBase(t)
}

func TestLand_ACommitThatCannotBeMadeLeavesTheChangeInTheTree(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	var kept string
	p := w.landing(textInput, LandNone, &kept)
	p.Git = answering(w.repo.Dir, func(argv []string) (string, int, bool) {
		return "fatal: the object database is gone", 128, has(argv, "commit")
	})

	_, err := Land(context.Background(), p)

	if f := failure(t, err); f.Category != toolio.CategoryGit || !strings.Contains(f.Message, "cannot commit") || w.rep.effect(EffectCommit).State != toolio.StateFailed {
		t.Errorf("Land = %v, effects %v, want git and the commit failed", err, w.rep.effects)
	}
	if branches := w.repo.branches(t); len(branches) != 1 || w.repo.read(t, "src/greet.sh") != "echo hello, world\n" {
		t.Errorf("branches %v, greet.sh %q, want the branch taken away and the change in the tree", branches, w.repo.read(t, "src/greet.sh"))
	}
}

// A push that fails after its retries fails the landing, with the branch and its commit kept
// here and the way to push it again.
func TestLand_APushThatFailsKeepsTheBranchAndFails(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	pushes := 0
	var kept string
	p := w.landing(textInput, LandPR, &kept)
	p.Git = answering(w.repo.Dir, func(argv []string) (string, int, bool) {
		if has(argv, "push") {
			pushes++
		}
		return "error: failed to push some refs: the remote hung up", 1, has(argv, "push")
	})

	_, err := Land(context.Background(), p)

	branch := w.rep.artifact("branch").Name
	if f := failure(t, err); f.Category != toolio.CategoryGit || !strings.Contains(f.Message, "could not be pushed") || pushes != 3 || countOf(w.rep.warnings, WarnPushRetried) != 2 {
		t.Errorf("Land = %v after %d pushes, warnings %v, want git after 3 attempts and 2 retries", err, pushes, w.rep.warnings)
	}
	if !w.rep.nextIs("git", "push", "-u", "origin", branch) || w.rep.effect(EffectPush).State != toolio.StateFailed || w.forge.Count(fakeforge.OpPullRequest) != 0 {
		t.Errorf("next %v, effects %v: want the way to push again, the push failed, and no pull request", w.rep.next, w.rep.effects)
	}
	if subject := w.repo.git(t, "log", "-1", "--format=%s", branch); subject != "feat: Add x" {
		t.Errorf("the branch holds %q, want the commit kept here", subject)
	}
	w.onBase(t)
}

func TestLand_AFailureToGoBackIsReportedWithTheFailureBeforeIt(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	var kept string
	p := w.landing(textInput, LandBranch, &kept)
	p.Git = answering(w.repo.Dir, func(argv []string) (string, int, bool) {
		switch {
		case has(argv, "push"):
			return "remote: Permission denied to user", 128, true
		case has(argv, "checkout") && has(argv, "main"):
			return "fatal: cannot go back", 128, true
		}
		return "", 0, false
	})

	_, err := Land(context.Background(), p)

	if err == nil || !strings.Contains(err.Error(), "could not be pushed") || !strings.Contains(err.Error(), "cannot go back") {
		t.Errorf("error = %v, want both failures", err)
	}
}

// The pull request is a warning: the branch is pushed, and the change has landed.
func TestLand_APullRequestTheForgeRefusesIsAWarning(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	w.forge.Fail(fakeforge.OpPullRequest, fakeforge.Failure{Status: 500, Message: "boom"})

	res, _ := w.land(t, w.withIssue(), LandPR, nil)

	if res.Outcome != OutcomeLanded || res.Status != toolio.StatusDone || res.PullRequest != nil || !res.Pushed || w.rep.effect(EffectCreatePullRequest).State != toolio.StateFailed {
		t.Errorf("result = %+v, effects %v, want the landing done and pushed, with no pull request", res, w.rep.effects)
	}
	if msg, ok := w.rep.warning(WarnPullRequestFailed); !ok || !strings.Contains(msg, "boom") {
		t.Errorf("warnings = %v %v, want pull_request_failed with the error of the forge", w.rep.warnings, w.rep.messages)
	}
	w.onBase(t)
}

func TestLand_AReportThatCannotBeKeptIsAWarning(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)

	res, _ := w.land(t, textInput, LandNone, func(p *LandParams) { p.WriteReport = func(string) error { return os.ErrPermission } })

	if _, ok := w.rep.warning(WarnReportUnwritable); res.Outcome != OutcomeLanded || !ok {
		t.Errorf("result %+v, warnings %v, want a landing and the warning", res, w.rep.warnings)
	}
}

// Whatever the brain reported, the body fits what the forge takes, and its footer says what
// the run used.
func TestLand_TheBodyFitsTheForgeAndNamesTheRun(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)

	w.land(t, textInput, LandPR, func(p *LandParams) {
		p.Report.RootCause = strings.Repeat("root cause words ", 8000)
		p.Brain, p.CostUSD, p.CostSource = "agentkit", 1.5, toolio.CostMeasured
	})

	prs := w.forge.PullRequests()
	if len(prs) != 1 || len(prs[0].Body) > 65536 || !strings.Contains(prs[0].Body, BodyCutMarker) || !strings.Contains(prs[0].Body, "Run `"+runID+"` · brain `agentkit` · cost $1.5000 (measured)") {
		t.Errorf("pull requests = %d, want one whose body fits, says it was cut and names the run", len(prs))
	}
}

// The post-push hook is how a caller passes the branch on from where it was pushed: it is told
// the branch once it is pushed, before any pull request, and a branch it does not take fails
// the landing with the branch kept where it is.
func TestLand_ThePostPushHookComesBetweenThePushAndThePullRequest(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	h := &postPush{w: w, t: t}

	res, _ := w.land(t, textInput, LandPR, func(p *LandParams) { p.PostPush = h.hook })

	if len(h.branches) != 1 || h.branches[0] != res.Branch || !h.onRemote[0] || h.pulls[0] != 0 || res.PullRequest == nil {
		t.Errorf("the hook saw %v (on origin %v, %v pull requests), result %+v, want once with the pushed branch and no pull request yet", h.branches, h.onRemote, h.pulls, res)
	}

	w = newLandWorld(t, fakeforge.GitHub)
	h = &postPush{w: w, t: t, err: errors.New("hub is unavailable: it answered 503")}
	var kept string
	p := w.landing(textInput, LandPR, &kept)
	p.PostPush = h.hook

	_, err := Land(context.Background(), p)

	branch := w.rep.artifact("branch").Name
	if f := failure(t, err); f.Category != toolio.CategoryGit || f.Retryable || !strings.Contains(f.Message, branch+" is pushed, and could not be published") || !strings.Contains(f.Message, "503") {
		t.Errorf("failure = %+v, want git saying the branch is pushed and was not published", f)
	}
	if !w.rep.nextIs("git", "show", "--stat", branch) || !w.repo.remoteHas(t, branch) || !strings.Contains(w.rep.effect(EffectPush).Detail, "503") || w.forge.Count(fakeforge.OpPullRequest) != 0 {
		t.Errorf("next %v, effects %v: want the branch to look at, kept on origin, the push failed, and no pull request", w.rep.next, w.rep.effects)
	}
	w.onBase(t)
}

func TestLand_NoPostPushHookIsCalledWhenNothingIsPushed(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	h := &postPush{w: w, t: t}
	w.land(t, textInput, LandNone, func(p *LandParams) { p.PostPush = h.hook })

	w2 := newLandWorld(t, fakeforge.GitHub)
	w2.repo.git(t, "remote", "set-url", "origin", t.TempDir()+"/missing.git")
	var kept string
	p := w2.landing(textInput, LandBranch, &kept)
	p.PostPush = h.hook
	_, err := Land(context.Background(), p)

	if err == nil || len(h.branches) != 0 {
		t.Errorf("Land = %v, the hook was called with %v, want a failed push and no call", err, h.branches)
	}
}
