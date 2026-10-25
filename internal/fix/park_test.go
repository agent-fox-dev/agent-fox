package fix

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// parking is the parameters of a parking of the world's change after a gate that still fails.
func (w *landWorld) parking(input Input, mode string) ParkParams {
	return ParkParams{
		Git: w.repo.Git, State: w.state, Input: input, Mode: mode, Forge: w.client, Target: w.target,
		Subject: "say hello", Verdict: "still_failing", Failing: []FailingCheck{{Name: "make check", Tail: "FAIL: greet printed helo"}}, Report: w.rep,
	}
}

func (w *landWorld) park(t *testing.T, input Input, mode string, edit func(*ParkParams)) ParkResult {
	t.Helper()
	p := w.parking(input, mode)
	if edit != nil {
		edit(&p)
	}
	res, err := Park(context.Background(), p)
	if err != nil {
		t.Fatalf("Park: %v", err)
	}
	return res
}

// The commit that parks says it is work in progress, why, and which checks failed, and is
// made without the hooks of the repository, which could refuse it.
func TestPark_CommitsTheWorkInProgressWithoutTheHooks(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	w.repo.hook(t, "pre-commit", "#!/bin/sh\ntouch \"$(git rev-parse --git-dir)/hook-ran\"\nexit 1\n")

	res := w.park(t, w.withIssue(), LandNone, nil)

	want := "wip: say hello\n\nParked by af fix: verdict still_failing\nFailing checks:\n- make check\n\nRefs https://github.com/o/r/issues/7\n\nAgent-Fox-Run: " + runID
	if msg := w.repo.message(t, res.Branch); msg != want {
		t.Errorf("message =\n%s\nwant\n%s", msg, want)
	}
	if res.Branch != "fix/7-greeting-wrong" || res.Commit != w.repo.git(t, "rev-parse", res.Branch) || w.repo.git(t, "rev-list", "--count", w.state.BaseHead+".."+res.Branch) != "1" {
		t.Errorf("result = %+v, want one commit on the branch of the issue", res)
	}
	if w.repo.read(t, "src/greet.sh") != "echo hello\n" || w.repo.git(t, "show", res.Branch+":src/greet.sh") != "echo hello, world" {
		t.Error("the work is not on the branch alone")
	}
	if _, err := os.Stat(filepath.Join(w.repo.Dir, ".git", "hook-ran")); err == nil {
		t.Error("the pre-commit hook ran")
	}

	w2 := newLandWorld(t, fakeforge.GitHub)
	res2 := w2.park(t, textInput, LandNone, func(p *ParkParams) { p.Failing, p.Verdict, p.Subject = nil, "regressed", "Say hello\nright" })
	if msg := w2.repo.message(t, res2.Branch); msg != "wip: Say hello right\n\nParked by af fix: verdict regressed\n\nAgent-Fox-Run: "+runID || res2.Branch != "fix/say-hello-right" {
		t.Errorf("no issue and no failing check: %q on %s", msg, res2.Branch)
	}
}

// The subject of the commit is the brain's, else the title of the issue, else the first line
// of the report, cut to the limit of a subject.
func TestParkSubject_FallsBackFromTheBrainToTheIssueToTheReport(t *testing.T) {
	long := strings.Repeat("word ", 40)
	issue := &StateIssue{Title: "Greeting is wrong"}
	file := Input{Kind: "file", Body: "The greeting says helo.\nIt should say hello."}
	for _, tc := range []struct {
		p    ParkParams
		want string
	}{
		{ParkParams{Subject: "say hello", State: State{Issue: issue}, Input: file}, "say hello"},
		{ParkParams{State: State{Issue: issue}, Input: file}, "Greeting is wrong"},
		{ParkParams{Input: file}, "The greeting says helo. It should say hello."},
		{ParkParams{}, "unverified change"},
		{ParkParams{Subject: long}, strings.TrimSpace(long)[:maxSubject]},
	} {
		if got := parkSubject(tc.p); got != tc.want {
			t.Errorf("parkSubject(%+v) = %q, want %q", tc.p, got, tc.want)
		}
	}
}

// Unless the run was interrupted, the parked branch is pushed, and under --land pr a draft
// pull request that refers to the issue is opened; nothing goes beyond the machine for a run
// that was interrupted or under --land none.
func TestPark_SendsWhatTheModeAsksFor(t *testing.T) {
	for name, tc := range map[string]struct {
		kind, mode   string
		interrupted  bool
		pushed, pull bool
	}{
		"pr on github":    {fakeforge.GitHub, LandPR, false, true, true},
		"pr on gitlab":    {fakeforge.GitLab, LandPR, false, true, true},
		"branch":          {fakeforge.GitHub, LandBranch, false, true, false},
		"none":            {fakeforge.GitHub, LandNone, false, false, false},
		"pr, interrupted": {fakeforge.GitHub, LandPR, true, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			w := newLandWorld(t, tc.kind)

			res := w.park(t, w.withIssue(), tc.mode, func(p *ParkParams) { p.Interrupted = tc.interrupted })

			if res.Outcome != OutcomeParked || res.Status != toolio.StatusUnverified || res.Pushed != tc.pushed || w.repo.remoteHas(t, res.Branch) != tc.pushed || (res.PullRequest != nil) != tc.pull || len(w.forge.Writes()) != map[bool]int{true: 1}[tc.pull] {
				t.Fatalf("result = %+v, %d writes to the forge", res, len(w.forge.Writes()))
			}
			if msg, _ := w.rep.warning(WarnFixNotLanded); !strings.Contains(msg, "still_failing") || !strings.Contains(msg, res.Branch) || !w.rep.nextIs("git", "switch", res.Branch) {
				t.Errorf("fix_not_landed %q, next %v, want the verdict, the branch, and the way to look at it", msg, w.rep.next)
			}
			if w.rep.nextIs("git", "push", "-u", "origin", res.Branch) || (w.rep.effect(EffectPush).Kind != "") != tc.pushed {
				t.Errorf("next %v, effects %v: a branch that was not to be pushed is offered no push", w.rep.next, w.rep.effects)
			}
			if tc.pull {
				pr := w.forge.PullRequests()[0]
				if !pr.Draft || pr.Head != res.Branch || pr.Base != "main" || !strings.Contains(pr.Title, "wip: say hello") || !strings.Contains(pr.Body, "Refs https://github.com/o/r/issues/7") || !strings.Contains(pr.Body, "- make check") {
					t.Errorf("pull request %+v, want a draft that refers to the issue and names the failing check", pr)
				}
				if !res.PullRequest.Draft || res.PullRequest.Number != pr.Number || w.rep.artifact("pull_request").URL == "" {
					t.Errorf("result %+v, artifacts %v", res.PullRequest, w.rep.artifacts)
				}
			}
			w.onBase(t)
		})
	}
}

// The commit comes first, and nothing after it can lose the work: a push or a pull request
// that fails is a warning.
func TestPark_KeepsTheWorkWhenThePushOrThePullRequestFails(t *testing.T) {
	t.Run("a push that needs two retries", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		pushes := 0
		res := w.park(t, textInput, LandBranch, func(p *ParkParams) {
			p.Git = answering(w.repo.Dir, func(argv []string) (string, int, bool) {
				if has(argv, "push") {
					pushes++
				}
				return "fatal: unable to access: Could not resolve proxy", 128, has(argv, "push") && pushes <= 2
			})
		})
		if !res.Pushed || !w.repo.remoteHas(t, res.Branch) || countOf(w.rep.warnings, WarnPushRetried) != 2 {
			t.Errorf("result %+v, warnings %v, want the push through after two retries", res, w.rep.warnings)
		}
	})
	t.Run("a push that fails", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		res := w.park(t, textInput, LandPR, func(p *ParkParams) {
			p.Git = answering(w.repo.Dir, func(argv []string) (string, int, bool) { return "remote: Permission denied", 128, has(argv, "push") })
		})
		if _, warned := w.rep.warning(WarnPushFailed); res.Pushed || res.PullRequest != nil || !warned || w.forge.Count(fakeforge.OpPullRequest) != 0 || w.rep.effect(EffectPush).State != toolio.StateFailed {
			t.Errorf("result %+v, warnings %v, want push_failed and no pull request", res, w.rep.warnings)
		}
		if !w.rep.nextIs("git", "push", "-u", "origin", res.Branch) || w.repo.git(t, "rev-parse", "--verify", res.Branch) != res.Commit {
			t.Errorf("next = %v, want the branch kept and the push to try again", w.rep.next)
		}
	})
	t.Run("a pull request that fails", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		w.forge.Fail(fakeforge.OpPullRequest, fakeforge.Failure{Status: 500, Message: "boom"})
		res := w.park(t, textInput, LandPR, nil)
		if msg, _ := w.rep.warning(WarnPullRequestFailed); !res.Pushed || res.PullRequest != nil || !strings.Contains(msg, "boom") || w.rep.effect(EffectCreatePullRequest).State != toolio.StateFailed {
			t.Errorf("result %+v, warnings %v %v, want pushed, and pull_request_failed with the forge's error", res, w.rep.warnings, w.rep.messages)
		}
	})
	t.Run("a stop while it pushes", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		p := w.parking(textInput, LandBranch)
		p.Git = answering(w.repo.Dir, func(argv []string) (string, int, bool) {
			if has(argv, "push") {
				cancel()
			}
			return "fatal: interrupted", 128, has(argv, "push")
		})
		res, err := Park(ctx, p)
		if err != nil || res.Pushed || res.Commit == "" {
			t.Errorf("Park = %+v, %v, want the work kept on a branch that is not pushed", res, err)
		}
		w.onBase(t)
	})
}

func TestPark_ACommitThatCannotBeMadeOrNothingToParkLeavesTheCheckoutAsItWas(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	p := w.parking(textInput, LandNone)
	p.Git = answering(w.repo.Dir, func(argv []string) (string, int, bool) { return "fatal: cannot commit", 128, has(argv, "commit") })

	_, err := Park(context.Background(), p)

	if f := failure(t, err); f.Category != toolio.CategoryGit || w.rep.effect(EffectCommit).State != toolio.StateFailed {
		t.Errorf("Park = %v, effects %v, want git and the commit failed", err, w.rep.effects)
	}
	if branches := w.repo.branches(t); len(branches) != 1 || w.repo.read(t, "src/greet.sh") != "echo hello, world\n" {
		t.Errorf("branches %v, want the empty branch removed and the change in the tree of the base", branches)
	}

	w.repo.git(t, "reset", "-q", "--hard")
	if _, err := Park(context.Background(), w.parking(textInput, LandNone)); !errors.Is(err, ErrNothingToPark) || len(w.repo.branches(t)) != 1 {
		t.Errorf("a clean checkout: %v, want ErrNothingToPark and no branch", err)
	}
}

func TestPark_ATreeThatIsNotCleanAfterwardsIsAFailure(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	looks := 0
	p := w.parking(textInput, LandNone)
	p.Git = answering(w.repo.Dir, func(argv []string) (string, int, bool) {
		if has(argv, "status") {
			looks++
		}
		return " M phantom.txt\n", 0, has(argv, "status") && looks > 1 // the first look is the park's own
	})

	res, err := Park(context.Background(), p)

	if f := failure(t, err); f.Category != toolio.CategoryGit || !strings.Contains(f.Message, "phantom.txt") || !strings.Contains(f.Message, res.Branch) {
		t.Errorf("Park = %v, want git naming the dirty path and the branch the work is on", err)
	}
}

// A branch the post-push hook does not take is pushed all the same and stays where it is; a
// parking says so and opens no pull request.
func TestPark_ThePostPushHookComesBeforeThePullRequestAndItsFailureIsAWarning(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	h := &postPush{w: w, t: t}
	res := w.park(t, w.withIssue(), LandPR, func(p *ParkParams) { p.PostPush = h.hook })
	if len(h.branches) != 1 || h.branches[0] != res.Branch || !h.onRemote[0] || h.pulls[0] != 0 || res.PullRequest == nil {
		t.Errorf("the hook saw %v (on origin %v, %v pull requests), result %+v", h.branches, h.onRemote, h.pulls, res)
	}

	w = newLandWorld(t, fakeforge.GitHub)
	h = &postPush{w: w, t: t, err: errors.New("hub is unavailable: it answered 503")}
	res = w.park(t, w.withIssue(), LandPR, func(p *ParkParams) { p.PostPush = h.hook })
	if msg, _ := w.rep.warning(WarnPushFailed); res.Pushed || res.PullRequest != nil || !strings.Contains(msg, "is pushed, and was not published") || !strings.Contains(msg, "503") {
		t.Errorf("result %+v, push_failed %q, want a branch that is pushed and not published, and no pull request", res, msg)
	}
	if !w.rep.nextIs("git", "show", "--stat", res.Branch) || w.rep.nextIs("git", "push", "-u", "origin", res.Branch) || w.forge.Count(fakeforge.OpPullRequest) != 0 {
		t.Errorf("next = %v, want the branch to look at, and no push of a branch that is pushed", w.rep.next)
	}
	w.onBase(t)

	w = newLandWorld(t, fakeforge.GitHub)
	h = &postPush{w: w, t: t}
	w.park(t, w.withIssue(), LandPR, func(p *ParkParams) { p.Interrupted, p.PostPush = true, h.hook })
	if len(h.branches) != 0 {
		t.Errorf("an interrupted run called the hook with %v", h.branches)
	}
}
