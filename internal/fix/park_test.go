package fix

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// recorder is a Reporter that keeps what it is told.
type recorder struct {
	warnings  []string
	messages  []string
	artifacts []toolio.Artifact
	effects   []toolio.SideEffect
	next      []toolio.Next
}

func (r *recorder) Warn(code, message string) {
	r.warnings = append(r.warnings, code)
	r.messages = append(r.messages, message)
}
func (r *recorder) Artifact(a toolio.Artifact)     { r.artifacts = append(r.artifacts, a) }
func (r *recorder) SideEffect(s toolio.SideEffect) { r.effects = append(r.effects, s) }
func (r *recorder) Next(n toolio.Next)             { r.next = append(r.next, n) }

// effect is the side effect of the kind, or the zero one.
func (r *recorder) effect(kind string) toolio.SideEffect {
	for _, e := range r.effects {
		if e.Kind == kind {
			return e
		}
	}
	return toolio.SideEffect{}
}

func (r *recorder) artifact(kind string) toolio.Artifact {
	for _, a := range r.artifacts {
		if a.Kind == kind {
			return a
		}
	}
	return toolio.Artifact{}
}

// landWorld is a checkout with a bare origin and a change in its working tree, a fake
// forge, and the state of a run on it.
type landWorld struct {
	repo   *testRepo
	forge  *fakeforge.Forge
	client issuex.Client
	target issuex.Repo
	state  State
	rep    *recorder
}

func newLandWorld(t *testing.T, kind string) *landWorld {
	t.Helper()
	r := newTestRepo(t, gateFiles, true)
	f := fakeforge.New(t, kind, "o/r")
	target := issuex.Repo{Owner: "o", Name: "r"}
	client, err := f.NewForge("token")(issuex.Options{Repo: target})
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(gitDo(t, r.Dir, "rev-parse", "HEAD"))
	w := &landWorld{repo: r, forge: f, client: client, target: target, rep: &recorder{},
		state: State{RunID: "20261020_100000_aaaaaa", Base: "main", BaseHead: head}}
	r.Git.SetSleep(func(time.Duration) {})
	if err := os.WriteFile(filepath.Join(r.Dir, "src", "greet.sh"), []byte("echo hello, world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *landWorld) issueInput() {
	w.state.Issue = &StateIssue{URL: "https://github.com/o/r/issues/7", Title: "Greeting is wrong", Number: 7}
	w.state.Input = StateInput{Kind: "issue", Origin: w.state.Issue.URL, Key: w.state.Issue.URL}
}

// issue makes the run one of issue 7, and is its input.
func (w *landWorld) issue() Input {
	w.issueInput()
	return Input{Kind: "issue", Origin: w.state.Issue.URL, Body: "x"}
}

// park parks the change with the parameters of the world, and those mutate sets.
func (w *landWorld) park(t *testing.T, input Input, mode string, mutate func(*ParkParams)) (ParkResult, error) {
	t.Helper()
	p := ParkParams{
		Git: w.repo.Git, State: w.state, Input: input, Mode: mode, Forge: w.client, Target: w.target,
		Subject: "say hello", Verdict: "still_failing", Failing: []FailingCheck{{Name: "make check", Tail: "FAIL: greet printed helo"}}, Report: w.rep,
	}
	if mutate != nil {
		mutate(&p)
	}
	return Park(context.Background(), p)
}

func (w *landWorld) remoteHas(t *testing.T, branch string) bool {
	t.Helper()
	return remoteHasBranch(t, w.repo.Dir, branch)
}

// remoteHasBranch says whether the origin of the checkout in dir has the branch.
func remoteHasBranch(t *testing.T, dir, branch string) bool {
	t.Helper()
	return strings.TrimSpace(gitDo(t, dir, "ls-remote", "--heads", "origin", branch)) != ""
}

var fileInput = Input{Kind: "file", Origin: "/work/bug.md", Body: "The greeting says helo.\nIt should say hello."}

// TestBranchesAreNamedByTheIssueOrTheSubjectMadeUniqueAndCreatedAtTheBase_TS_09_54
// verifies TS-09-54.
// Verifies: 09-REQ-8.2
func TestBranchesAreNamedByTheIssueOrTheSubjectMadeUniqueAndCreatedAtTheBase_TS_09_54(t *testing.T) {
	if got := BranchName(42, "Crash on empty input"); got != "fix/42-crash-on-empty-input" {
		t.Errorf("BranchName(42) = %q", got)
	}
	if got := BranchName(0, "Handle nil"); got != "fix/handle-nil" {
		t.Errorf("BranchName(0) = %q", got)
	}
	if got := BranchName(0, "Fix the bug"); got != "fix/issue" {
		t.Errorf("BranchName of a title of stop words = %q, want the slug that is never empty", got)
	}

	w := newLandWorld(t, fakeforge.GitHub)
	ctx := context.Background()
	// A later commit on the base: the branch still starts at the commit the run began at.
	if err := os.WriteFile(filepath.Join(w.repo.Dir, "later.txt"), []byte("later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDo(t, w.repo.Dir, "add", "later.txt")
	gitDo(t, w.repo.Dir, "commit", "-q", "-m", "chore: later", "later.txt")
	// A local branch and a remote branch have the name already.
	gitDo(t, w.repo.Dir, "branch", "fix/handle-nil")
	gitDo(t, w.repo.Dir, "branch", "fix/handle-nil-2")
	gitDo(t, w.repo.Dir, "push", "-q", "origin", "fix/handle-nil-2")
	gitDo(t, w.repo.Dir, "branch", "-q", "-D", "fix/handle-nil-2")

	branch, err := MakeBranch(ctx, w.repo.Git, w.rep, 0, "Handle nil", w.state.BaseHead)

	if err != nil {
		t.Fatal(err)
	}
	if branch != "fix/handle-nil-3" {
		t.Errorf("branch = %q, want a name that neither a local nor a remote branch has", branch)
	}
	if now, _ := w.repo.Git.CurrentBranch(ctx); now != branch {
		t.Errorf("checked out %q", now)
	}
	if head := strings.TrimSpace(gitDo(t, w.repo.Dir, "rev-parse", "HEAD")); head != w.state.BaseHead {
		t.Errorf("the branch starts at %s, want the base %s", head, w.state.BaseHead)
	}
	if data, _ := os.ReadFile(filepath.Join(w.repo.Dir, "src", "greet.sh")); string(data) != "echo hello, world\n" {
		t.Errorf("greet.sh = %q, want the change carried along", data)
	}
	if a := w.rep.artifact("branch"); a.Name != branch {
		t.Errorf("artifacts = %v, want the branch", w.rep.artifacts)
	}
	if e := w.rep.effect(EffectBranch); e.Target != branch || e.State != toolio.StateDone {
		t.Errorf("effects = %v, want the branch done", w.rep.effects)
	}
	if _, err := MakeBranch(ctx, w.repo.Git, w.rep, 0, "Other", "no-such-commit"); err == nil {
		t.Error("a branch was made from a commit that is not there")
	} else if f := failure(t, err); f.Category != toolio.CategoryGit {
		t.Errorf("failure = %+v, want git", f)
	} else if e := w.rep.effects[len(w.rep.effects)-1]; e.Kind != EffectBranch || e.State != toolio.StateFailed {
		t.Errorf("last effect = %+v, want the branch failed", e)
	}
}

// TestParkingCommitsWipWithTheVerdictTheFailingChecksRefsAndTheTrailerWithoutHooks_TS_09_61
// verifies TS-09-61.
// Verifies: 09-REQ-9.1
func TestParkingCommitsWipWithTheVerdictTheFailingChecksRefsAndTheTrailerWithoutHooks_TS_09_61(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	w.issueInput()
	hook := filepath.Join(w.repo.Dir, ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch \"$(git rev-parse --show-toplevel)/hook-ran\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := w.park(t, Input{Kind: "issue", Origin: w.state.Issue.URL, Body: "x"}, LandNone, nil)

	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "fix/7-greeting-wrong" {
		t.Errorf("branch = %q, want the name of an issue", res.Branch)
	}
	msg := gitDo(t, w.repo.Dir, "log", "-1", "--format=%B", res.Branch)
	want := "wip: say hello\n\nParked by af fix: verdict still_failing\nFailing checks:\n- make check\n\nRefs https://github.com/o/r/issues/7\n\nAgent-Fox-Run: 20261020_100000_aaaaaa"
	if strings.TrimSpace(msg) != want {
		t.Errorf("message =\n%s\nwant\n%s", msg, want)
	}
	if _, err := os.Stat(filepath.Join(w.repo.Dir, "hook-ran")); err == nil {
		t.Error("the pre-commit hook ran")
	}
	if n := strings.Count(strings.TrimSpace(gitDo(t, w.repo.Dir, "rev-list", w.state.BaseHead+".."+res.Branch)), "\n") + 1; n != 1 {
		t.Errorf("%d commits above the base, want one", n)
	}
	if full := strings.TrimSpace(gitDo(t, w.repo.Dir, "rev-parse", res.Branch)); res.Commit != full || len(res.Commit) != 40 {
		t.Errorf("commit = %q, want the full hash %s", res.Commit, full)
	}

	// Without an issue, and without failing checks, the message has neither of those lines.
	w2 := newLandWorld(t, fakeforge.GitHub)
	res2, err := w2.park(t, fileInput, LandNone, func(p *ParkParams) { p.Failing = nil; p.Verdict = "regressed"; p.Subject = "Say hello\nright" })
	if err != nil {
		t.Fatal(err)
	}
	msg2 := strings.TrimSpace(gitDo(t, w2.repo.Dir, "log", "-1", "--format=%B", res2.Branch))
	if msg2 != "wip: Say hello right\n\nParked by af fix: verdict regressed\n\nAgent-Fox-Run: 20261020_100000_aaaaaa" {
		t.Errorf("message = %q", msg2)
	}
	if res2.Branch != "fix/say-hello-right" {
		t.Errorf("branch = %q, want the slug of the subject", res2.Branch)
	}
}

// TestAParkedRunPushesAndOpensADraftThatRefsTheIssue_TS_09_62 verifies TS-09-62: under
// --land pr the branch is pushed, and the draft pull request says Refs the issue.
// Verifies: 09-REQ-9.2
func TestAParkedRunPushesAndOpensADraftThatRefsTheIssue_TS_09_62(t *testing.T) {
	for _, kind := range []string{fakeforge.GitHub, fakeforge.GitLab} {
		t.Run("pr on "+kind, func(t *testing.T) {
			w := newLandWorld(t, kind)
			w.issueInput()

			res, err := w.park(t, Input{Kind: "issue", Origin: w.state.Issue.URL, Body: "x"}, LandPR, nil)

			if err != nil {
				t.Fatal(err)
			}
			var writes []string
			for _, r := range w.forge.Requests() {
				if r.Op != "" && r.Op != fakeforge.OpRepo {
					writes = append(writes, r.Op)
				}
			}
			if !slices.Equal(writes, []string{fakeforge.OpPullRequest}) {
				t.Errorf("requests = %v, want the pull request alone", writes)
			}
			if res.Branch != "fix/7-greeting-wrong" {
				t.Errorf("branch %q, want the branch named by the issue", res.Branch)
			}
			prs := w.forge.PullRequests()
			if len(prs) != 1 || !prs[0].Draft || prs[0].Head != res.Branch || prs[0].Base != "main" || !strings.Contains(prs[0].Title, "wip: ") ||
				!strings.Contains(prs[0].Body, "Refs "+w.state.Issue.URL) {
				t.Errorf("pull requests = %+v, want one draft from the branch whose body says Refs %s", prs, w.state.Issue.URL)
			}
			if res.PullRequest == nil || !res.PullRequest.Draft || res.PullRequest.Number != prs[0].Number || res.PullRequest.URL == "" || !res.Pushed {
				t.Errorf("result = %+v", res)
			}
			if !w.remoteHas(t, res.Branch) {
				t.Error("the branch is not on the remote")
			}
			for _, kind := range []string{EffectBranch, EffectCommit, EffectPush, EffectCreatePullRequest} {
				if e := w.rep.effect(kind); e.State != toolio.StateDone {
					t.Errorf("effect %s = %+v, want done", kind, e)
				}
			}
			for _, kind := range []string{"branch", "commit", "pull_request"} {
				if a := w.rep.artifact(kind); a.Name == "" && a.URL == "" {
					t.Errorf("artifact %s is missing: %v", kind, w.rep.artifacts)
				}
			}
		})
	}
}

// TestParkingFollowsTheModeOfTheLanding guards what a parked change sends and where: branch
// mode pushes and asks nothing of the forge, none mode keeps the branch here, and a run that
// was interrupted sends nothing beyond the machine.
// Verifies: 09-REQ-9.2, 09-REQ-9.4
func TestParkingFollowsTheModeOfTheLanding(t *testing.T) {
	text := Input{Kind: "text", Body: "the greeting says helo"}
	t.Run("branch mode", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)

		res, err := w.park(t, text, LandBranch, nil)

		if err != nil || !res.Pushed || !w.remoteHas(t, res.Branch) || res.Branch != "fix/say-hello" {
			t.Fatalf("result %+v, %v", res, err)
		}
		if n := len(w.forge.Requests()); n != 0 || res.PullRequest != nil {
			t.Errorf("%d requests to the forge, pull request %v: branch mode asks nothing of the forge", n, res.PullRequest)
		}
	})
	t.Run("branch mode with an issue", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		w.issueInput()

		res, err := w.park(t, Input{Kind: "issue", Origin: w.state.Issue.URL, Body: "x"}, LandBranch, nil)

		if err != nil || !res.Pushed || res.PullRequest != nil || len(w.forge.Requests()) != 0 {
			t.Errorf("result %+v, %v, %d requests: branch mode opens no pull request, even for an issue", res, err, len(w.forge.Requests()))
		}
	})
	t.Run("none mode", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)

		res, err := w.park(t, text, LandNone, nil)

		if err != nil || res.Pushed || w.remoteHas(t, res.Branch) || len(w.forge.Requests()) != 0 {
			t.Errorf("result %+v, %v, %d requests: none mode keeps the branch and writes nothing", res, err, len(w.forge.Requests()))
		}
		if gitDo(t, w.repo.Dir, "rev-parse", "--verify", res.Branch) == "" {
			t.Error("no local branch")
		}
		for _, n := range w.rep.next {
			if slices.Contains(n.Argv, "push") {
				t.Errorf("next = %v: there is nothing to push in none mode", w.rep.next)
			}
		}
	})
	t.Run("interrupted", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)

		res, err := w.park(t, text, LandPR, func(p *ParkParams) { p.Interrupted = true })

		if err != nil {
			t.Fatal(err)
		}
		if res.Pushed || w.remoteHas(t, res.Branch) || len(w.forge.Requests()) != 0 || res.PullRequest != nil || res.Branch != "fix/say-hello" {
			t.Errorf("result %+v, %d requests, want a local branch and nothing else", res, len(w.forge.Requests()))
		}
		if e := w.rep.effect(EffectPush); e.Kind != "" {
			t.Errorf("effects = %v, want no push", w.rep.effects)
		}
		for _, n := range w.rep.next {
			if slices.Contains(n.Argv, "push") {
				t.Errorf("next = %v: an interrupted run offers no push", w.rep.next)
			}
		}
	})
}

// TestParkingEndsOnTheBaseWithACleanTreeAndSaysSo_TS_09_63 verifies TS-09-63.
// Verifies: 09-REQ-9.3
func TestParkingEndsOnTheBaseWithACleanTreeAndSaysSo_TS_09_63(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	w.issueInput()
	if err := os.WriteFile(filepath.Join(w.repo.Dir, "new.txt"), []byte("n\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := w.park(t, Input{Kind: "issue", Origin: w.state.Issue.URL, Body: "x"}, LandBranch, nil)

	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomeParked || res.Status != toolio.StatusUnverified || toolio.ExitCode(res.Status) != 4 {
		t.Errorf("outcome %q, status %q (exit %d), want parked, unverified and 4", res.Outcome, res.Status, toolio.ExitCode(res.Status))
	}
	if i := slices.Index(w.rep.warnings, WarnFixNotLanded); i < 0 || !strings.Contains(w.rep.messages[i], "still_failing") || !strings.Contains(w.rep.messages[i], res.Branch) {
		t.Errorf("warnings = %v %v, want fix_not_landed naming the verdict and the branch", w.rep.warnings, w.rep.messages)
	}
	var switchTo bool
	for _, n := range w.rep.next {
		if slices.Equal(n.Argv, []string{"git", "switch", res.Branch}) && n.Reason != "" {
			switchTo = true
		}
	}
	if !switchTo {
		t.Errorf("next = %v, want git switch %s", w.rep.next, res.Branch)
	}
	if branch, _ := w.repo.Git.CurrentBranch(context.Background()); branch != "main" {
		t.Errorf("checked out %q, want the base", branch)
	}
	if dirty, _ := w.repo.Git.DirtyFiles(context.Background()); len(dirty) != 0 {
		t.Errorf("the tree is dirty: %v", dirty)
	}
	if head := strings.TrimSpace(gitDo(t, w.repo.Dir, "rev-parse", "HEAD")); head != w.state.BaseHead {
		t.Errorf("HEAD = %s, want the base %s", head, w.state.BaseHead)
	}
	// The work is on the branch, and only there.
	if shown := gitDo(t, w.repo.Dir, "show", res.Branch+":src/greet.sh"); strings.TrimSpace(shown) != "echo hello, world" {
		t.Errorf("the branch holds %q", shown)
	}
	if data, _ := os.ReadFile(filepath.Join(w.repo.Dir, "src", "greet.sh")); string(data) != "echo hello\n" {
		t.Errorf("the base holds %q, want its own content back", data)
	}
	if _, err := os.Stat(filepath.Join(w.repo.Dir, "new.txt")); err == nil {
		t.Error("the new file is still on the base")
	}
}

// TestParkingKeepsTheWorkWhateverTheForgeDoes guards the order of a parking: the commit is
// first, and what fails after it is a warning that leaves the work on its branch.
func TestParkingKeepsTheWorkWhateverTheForgeDoes(t *testing.T) {
	text := Input{Kind: "text", Body: "the greeting says helo"}

	t.Run("a push that needs a retry", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		pushes := 0
		flaky := gitx.New(w.repo.Dir, func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
			if slices.Contains(argv, "push") {
				if pushes++; pushes <= 2 {
					return "fatal: unable to access: Could not resolve proxy", 128, nil
				}
			}
			return gitx.ExecRunner(ctx, dir, argv, stdin...)
		})
		flaky.SetSleep(func(time.Duration) {})

		res, err := w.park(t, text, LandBranch, func(p *ParkParams) { p.Git = flaky })

		if err != nil || !res.Pushed || !w.remoteHas(t, res.Branch) {
			t.Fatalf("result %+v, %v, want the push to get through", res, err)
		}
		if n := strings.Count(strings.Join(w.rep.warnings, " "), WarnPushRetried); n != 2 {
			t.Errorf("warnings = %v, want push_retried for each of the two retries", w.rep.warnings)
		}
	})
	t.Run("a push that fails", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		refusing := gitx.New(w.repo.Dir, func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
			if slices.Contains(argv, "push") {
				return "remote: Permission denied to user", 128, nil
			}
			return gitx.ExecRunner(ctx, dir, argv, stdin...)
		})

		res, err := w.park(t, text, LandPR, func(p *ParkParams) { p.Git = refusing })

		if err != nil {
			t.Fatalf("a push that fails must not lose the work: %v", err)
		}
		if res.Pushed || res.PullRequest != nil || !slices.Contains(w.rep.warnings, WarnPushFailed) || w.forge.Count(fakeforge.OpPullRequest) != 0 {
			t.Errorf("result %+v, warnings %v, want the warning and no pull request", res, w.rep.warnings)
		}
		if e := w.rep.effect(EffectPush); e.State != toolio.StateFailed {
			t.Errorf("effects = %v, want push failed", w.rep.effects)
		}
		var retry bool
		for _, n := range w.rep.next {
			if slices.Equal(n.Argv, []string{"git", "push", "-u", "origin", res.Branch}) {
				retry = true
			}
		}
		if !retry {
			t.Errorf("next = %v, want the push to try again", w.rep.next)
		}
		if shown := gitDo(t, w.repo.Dir, "rev-parse", "--verify", res.Branch); shown == "" {
			t.Error("the branch is gone")
		}
	})
	t.Run("a pull request that fails", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		w.forge.Fail(fakeforge.OpPullRequest, fakeforge.Failure{Status: 500, Message: "boom"})

		res, err := w.park(t, text, LandPR, nil)

		if err != nil || !res.Pushed || res.PullRequest != nil {
			t.Fatalf("result %+v, %v", res, err)
		}
		if i := slices.Index(w.rep.warnings, WarnPullRequestFailed); i < 0 || !strings.Contains(w.rep.messages[i], "boom") {
			t.Errorf("warnings = %v %v, want pull_request_failed with the forge's error", w.rep.warnings, w.rep.messages)
		}
		if e := w.rep.effect(EffectCreatePullRequest); e.State != toolio.StateFailed {
			t.Errorf("effects = %v", w.rep.effects)
		}
	})
}

// TestParkingThatCannotBeMadeLeavesTheCheckoutAsItWas guards the ways parking itself can
// come to nothing: there is no change to keep, or the commit is refused.
func TestParkingThatCannotBeMadeLeavesTheCheckoutAsItWas(t *testing.T) {
	text := Input{Kind: "text", Body: "the greeting says helo"}
	t.Run("nothing to keep", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		gitDo(t, w.repo.Dir, "checkout", "-q", "--", "src/greet.sh")

		_, err := w.park(t, text, LandNone, nil)

		if !errorsIs(err, ErrNothingToPark) {
			t.Errorf("error = %v, want ErrNothingToPark", err)
		}
		if branches := strings.Fields(gitDo(t, w.repo.Dir, "for-each-ref", "--format=%(refname:short)", "refs/heads")); !slices.Equal(branches, []string{"main"}) {
			t.Errorf("branches = %v, want none made", branches)
		}
	})
	t.Run("a commit that fails", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		broken := gitx.New(w.repo.Dir, func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
			if slices.Contains(argv, "commit") {
				return "fatal: cannot commit", 128, nil
			}
			return gitx.ExecRunner(ctx, dir, argv, stdin...)
		})

		_, err := w.park(t, text, LandNone, func(p *ParkParams) { p.Git = broken })

		if f := failure(t, err); f.Category != toolio.CategoryGit {
			t.Errorf("failure = %+v, want git", f)
		}
		if e := w.rep.effect(EffectCommit); e.State != toolio.StateFailed {
			t.Errorf("effects = %v, want the commit failed", w.rep.effects)
		}
		if branch, _ := w.repo.Git.CurrentBranch(context.Background()); branch != "main" {
			t.Errorf("checked out %q, want the base again", branch)
		}
		if branches := strings.Fields(gitDo(t, w.repo.Dir, "for-each-ref", "--format=%(refname:short)", "refs/heads")); !slices.Equal(branches, []string{"main"}) {
			t.Errorf("branches = %v, want the empty branch removed", branches)
		}
		if data, _ := os.ReadFile(filepath.Join(w.repo.Dir, "src", "greet.sh")); string(data) != "echo hello, world\n" {
			t.Errorf("greet.sh = %q: the change must still be in the working tree", data)
		}
	})
}

func errorsIs(err, target error) bool {
	return err != nil && strings.Contains(err.Error(), target.Error())
}

// TestTheSubjectOfAParkedChangeIsTheBrainsThenTheIssuesThenTheReports guards the subject a
// change is parked under when the brain never gave one.
func TestTheSubjectOfAParkedChangeIsTheBrainsThenTheIssuesThenTheReports(t *testing.T) {
	long := strings.Repeat("word ", 40)
	for name, tc := range map[string]struct {
		subject string
		issue   bool
		input   Input
		want    string
	}{
		"the brain's":             {"say hello", true, fileInput, "wip: say hello"},
		"the issue's":             {"", true, fileInput, "wip: Greeting is wrong"},
		"the report's first line": {"", false, fileInput, "wip: The greeting says helo. It should say hello."},
		"nothing":                 {"", false, Input{Kind: "text"}, "wip: unverified change"},
		"cut":                     {long, false, fileInput, "wip: " + strings.TrimSpace(long)[:72]},
	} {
		t.Run(name, func(t *testing.T) {
			w := newLandWorld(t, fakeforge.GitHub)
			if tc.issue {
				w.issueInput()
			}

			res, err := w.park(t, tc.input, LandNone, func(p *ParkParams) { p.Subject = tc.subject })

			if err != nil {
				t.Fatal(err)
			}
			if first, _, _ := strings.Cut(gitDo(t, w.repo.Dir, "log", "-1", "--format=%B", res.Branch), "\n"); first != tc.want {
				t.Errorf("subject line = %q, want %q", first, tc.want)
			}
		})
	}
}

// TestACancelledRunStillGoesBackToTheBase guards the end of a parking: the checkout is put
// back on the base by a context that the cancellation of the run did not reach.
func TestACancelledRunStillGoesBackToTheBase(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopping := gitx.New(w.repo.Dir, func(c context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
		if slices.Contains(argv, "push") {
			cancel() // the run is stopped while the branch is being pushed
			return "fatal: interrupted", 128, nil
		}
		return gitx.ExecRunner(c, dir, argv, stdin...)
	})
	stopping.SetSleep(func(time.Duration) {})

	res, err := Park(ctx, ParkParams{Git: stopping, State: w.state, Input: Input{Kind: "text", Body: "x"}, Mode: LandBranch, Forge: w.client, Target: w.target,
		Subject: "say hello", Verdict: "still_failing", Report: w.rep})

	if err != nil {
		t.Fatalf("a run that is stopped must still keep its work: %v", err)
	}
	if branch, _ := w.repo.Git.CurrentBranch(context.Background()); branch != "main" || res.Pushed || res.Commit == "" {
		t.Errorf("checked out %q, result %+v, want the base, an unpushed branch and a commit", branch, res)
	}
}

// TestAParkingThatLeavesTheTreeDirtyIsAFailure guards 09-REQ-9.3: a checkout that is not
// clean at the end is said so, with the branch the work is on.
func TestAParkingThatLeavesTheTreeDirtyIsAFailure(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	statuses := 0
	haunted := gitx.New(w.repo.Dir, func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
		if slices.Contains(argv, "status") {
			if statuses++; statuses > 1 { // the first look is the park's own, before anything is done
				return " M phantom.txt\n", 0, nil
			}
		}
		return gitx.ExecRunner(ctx, dir, argv, stdin...)
	})

	res, err := w.park(t, Input{Kind: "text", Body: "x"}, LandNone, func(p *ParkParams) { p.Git = haunted })

	if f := failure(t, err); f.Category != toolio.CategoryGit || !strings.Contains(f.Message, "phantom.txt") || !strings.Contains(f.Message, res.Branch) {
		t.Errorf("failure = %v, want git naming the dirty path and the branch the work is on", err)
	}
}
