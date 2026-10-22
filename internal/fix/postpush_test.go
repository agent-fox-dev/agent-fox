package fix

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// hooked is a PostPush hook that remembers what the world looked like when it was called.
type hooked struct {
	w        *landWorld
	t        *testing.T
	branches []string
	// onRemote and pulls are whether origin had the branch, and how many pull requests the forge
	// held, at the time of the call.
	onRemote []bool
	pulls    []int
	err      error
}

func (h *hooked) hook(_ context.Context, branch string) error {
	h.branches = append(h.branches, branch)
	h.onRemote = append(h.onRemote, h.w.remoteHas(h.t, branch))
	h.pulls = append(h.pulls, h.w.forge.Count(fakeforge.OpPullRequest))
	return h.err
}

func newHooked(t *testing.T, w *landWorld) *hooked { return &hooked{w: w, t: t} }

// TestThePostPushHookRunsOnceAfterThePushAndBeforeThePullRequest_TS_13_63 verifies the seam of
// TS-13-63 in the fix pipeline: the hook is told the branch once the push succeeded, and no pull
// request has been asked for by then.
// Verifies: 13-REQ-8.6
func TestThePostPushHookRunsOnceAfterThePushAndBeforeThePullRequest_TS_13_63(t *testing.T) {
	t.Run("a landing", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		h := newHooked(t, w)

		res, _ := w.land(t, Input{Kind: "text", Body: "x"}, LandPR, func(p *LandParams) { p.PostPush = h.hook })

		if !slices.Equal(h.branches, []string{res.Branch}) || res.Branch == "" {
			t.Fatalf("the hook was called with %v, want once with %q", h.branches, res.Branch)
		}
		if !h.onRemote[0] || h.pulls[0] != 0 {
			t.Errorf("at the call the branch was on origin: %v, and %d pull requests were asked for; want pushed, and none yet", h.onRemote[0], h.pulls[0])
		}
		if res.PullRequest == nil || !res.Pushed {
			t.Errorf("result = %+v, want the pull request opened after the hook", res)
		}
	})

	t.Run("a parking", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		w.issueInput()
		h := newHooked(t, w)

		res, err := w.park(t, Input{Kind: "issue", Origin: w.state.Issue.URL, Body: "x"}, LandPR, func(p *ParkParams) { p.PostPush = h.hook })

		if err != nil || !slices.Equal(h.branches, []string{res.Branch}) {
			t.Fatalf("park: %v; the hook was called with %v, want once with %q", err, h.branches, res.Branch)
		}
		if !h.onRemote[0] || h.pulls[0] != 0 || res.PullRequest == nil || !res.Pushed {
			t.Errorf("on origin %v, %d pull requests at the call, result %+v; want pushed first, then the draft pull request", h.onRemote[0], h.pulls[0], res)
		}
	})

	t.Run("a finished run lands and parks through the hook", func(t *testing.T) {
		for name, ended := range map[string]LoopResult{
			"landable": {End: EndLandable, Report: reportOf(addX()), Measured: proven(), HoldsChange: true, Attempts: 1, Rounds: 1},
			"exhausted": {End: EndExhausted, Report: reportOf(addX()), HoldsChange: true, Attempts: 2, Rounds: 2,
				LastRefusal: &Measurement{Verdict: "still_failing", Refusal: &Refusal{Failing: []FailingCheck{{Name: "make check", Tail: "FAIL"}}}}},
		} {
			t.Run(name, func(t *testing.T) {
				w := newLandWorld(t, fakeforge.GitHub)
				w.issueInput()
				h := newHooked(t, w)
				p := w.finishing(LandPR, ended)
				p.PostPush = h.hook

				end, err := Finish(context.Background(), p)

				if err != nil || len(h.branches) != 1 || !strings.HasPrefix(h.branches[0], "fix/7-") {
					t.Fatalf("Finish: %+v, %v; the hook was called with %v, want once with the branch of issue 7", end, err, h.branches)
				}
				if !h.onRemote[0] || h.pulls[0] != 0 {
					t.Errorf("at the call: on origin %v, %d pull requests", h.onRemote[0], h.pulls[0])
				}
			})
		}
	})
}

func reportOf(r Report) *Report { return &r }

// TestAPostPushHookThatFailsEndsTheLandingFailedWithTheBranchKept_TS_13_63 verifies the failure
// half of TS-13-63 in the fix pipeline: the loop ends failed with category git, not retryable,
// the branch stays where it is pushed and is named in next, and no pull request is asked for.
// Verifies: 13-REQ-8.6
func TestAPostPushHookThatFailsEndsTheLandingFailedWithTheBranchKept_TS_13_63(t *testing.T) {
	t.Run("a landing", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		h := newHooked(t, w)
		h.err = errors.New("hub is unavailable: it answered 503")

		_, err := Land(context.Background(), LandParams{
			Git: w.repo.Git, State: w.state, Input: Input{Kind: "text", Body: "x"}, Report: addX(), Mode: LandPR, Forge: w.client, Target: w.target,
			Measured: proven(), Reporter: w.rep, PostPush: h.hook,
		})

		f := failure(t, err)
		if f.Category != toolio.CategoryGit || f.Retryable {
			t.Errorf("failure = %+v, want category git, not retryable", f)
		}
		branch := w.rep.artifact("branch").Name
		if branch == "" || !strings.Contains(f.Message, branch) || !strings.Contains(f.Message, "503") || !strings.Contains(f.Message, "is pushed, and could not be published") {
			t.Errorf("message = %q, want it to say that the branch %q is pushed and was not published, and why", f.Message, branch)
		}
		var named bool
		for _, n := range w.rep.next {
			named = named || slices.Contains(n.Argv, branch)
		}
		if !named {
			t.Errorf("next = %v, want one that names %s", w.rep.next, branch)
		}
		if !w.remoteHas(t, branch) {
			t.Error("the branch is not on origin: it has to be kept there")
		}
		if e := w.rep.effect(EffectPush); e.State != toolio.StateFailed || !strings.Contains(e.Detail, "503") {
			t.Errorf("push effect = %+v, want it failed with the reason", e)
		}
		if w.forge.Count(fakeforge.OpPullRequest) != 0 {
			t.Error("a pull request was asked for after the publication failed")
		}
		w.onBase(t)
	})

	t.Run("a parking", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		w.issueInput()
		h := newHooked(t, w)
		h.err = errors.New("hub is unavailable: it answered 503")

		res, err := w.park(t, Input{Kind: "issue", Origin: w.state.Issue.URL, Body: "x"}, LandPR, func(p *ParkParams) { p.PostPush = h.hook })

		if err != nil || res.Pushed || res.PullRequest != nil {
			t.Fatalf("park = %+v, %v, want a parked change that is not pushed, with no pull request", res, err)
		}
		if !slices.Contains(w.rep.warnings, WarnPushFailed) || !strings.Contains(strings.Join(w.rep.messages, " "), "is pushed, and was not published") || !strings.Contains(strings.Join(w.rep.messages, " "), "503") {
			t.Errorf("warnings = %v %v, want push_failed with the reason", w.rep.warnings, w.rep.messages)
		}
		if w.forge.Count(fakeforge.OpPullRequest) != 0 {
			t.Error("a pull request was asked for after the publication failed")
		}
		branch := res.Branch
		var names, pushes bool
		for _, n := range w.rep.next {
			names = names || slices.Contains(n.Argv, branch)
			pushes = pushes || slices.Equal(n.Argv, []string{"git", "push", "-u", "origin", branch})
		}
		if !names || pushes {
			t.Errorf("next = %v, want one that names %s and none that pushes a branch that is pushed already", w.rep.next, branch)
		}
		w.onBase(t)
	})
}

// TestNoPostPushHookIsCalledWhenNothingIsPushed pins the places where the hook must stay silent:
// a push that fails, a mode that does not push, and a run that was stopped.
// Verifies: 13-REQ-8.6
func TestNoPostPushHookIsCalledWhenNothingIsPushed(t *testing.T) {
	t.Run("a mode that does not push", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		h := newHooked(t, w)

		w.land(t, Input{Kind: "text", Body: "x"}, LandNone, func(p *LandParams) { p.PostPush = h.hook })

		if len(h.branches) != 0 {
			t.Errorf("the hook was called with %v", h.branches)
		}
	})

	t.Run("a push that fails", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		h := newHooked(t, w)
		gitDo(t, w.repo.Dir, "remote", "set-url", "origin", t.TempDir()+"/missing.git")

		_, err := Land(context.Background(), LandParams{
			Git: w.repo.Git, State: w.state, Input: Input{Kind: "text", Body: "x"}, Report: addX(), Mode: LandBranch, Forge: w.client, Target: w.target,
			Measured: proven(), Reporter: w.rep, PostPush: h.hook,
		})

		if f := failure(t, err); f.Category != toolio.CategoryGit || len(h.branches) != 0 {
			t.Errorf("failure %+v, hook calls %v, want git, and the hook never called", f, h.branches)
		}
	})

	t.Run("a run that was stopped", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		w.issueInput()
		h := newHooked(t, w)

		_, err := w.park(t, Input{Kind: "issue", Origin: w.state.Issue.URL, Body: "x"}, LandPR, func(p *ParkParams) { p.Interrupted, p.PostPush = true, h.hook })

		if err != nil || len(h.branches) != 0 {
			t.Errorf("park: %v, hook calls %v, want the hook never called", err, h.branches)
		}
	})
}
