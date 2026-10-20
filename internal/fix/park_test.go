package fix

import (
	"context"
	"errors"
	"fmt"
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
	return strings.TrimSpace(gitDo(t, w.repo.Dir, "ls-remote", "--heads", "origin", branch)) != ""
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

// TestAFileOrTextReportGetsExactlyOneIssueWithTheOriginalReport_TS_09_53 verifies
// TS-09-53 at the function: the title is the subject, the body the original report
// under a heading and a line that names the run, and the issue is an artifact and a side
// effect.
// Verifies: 09-REQ-8.1
func TestAFileOrTextReportGetsExactlyOneIssueWithTheOriginalReport_TS_09_53(t *testing.T) {
	for kind, want := range map[string]bool{"issue": false, "file": true, "text": true, "stdin": true} {
		if got := NeedsIssue(kind, LandPR); got != want {
			t.Errorf("NeedsIssue(%s, pr) = %v, want %v", kind, got, want)
		}
		if NeedsIssue(kind, LandBranch) || NeedsIssue(kind, LandNone) {
			t.Errorf("NeedsIssue(%s) wants an issue where no pull request is opened", kind)
		}
	}

	for _, kind := range []string{fakeforge.GitHub, fakeforge.GitLab} {
		t.Run(kind, func(t *testing.T) {
			w := newLandWorld(t, kind)

			issue, err := CreateIssue(context.Background(), IssueParams{Forge: w.client, Target: w.target, Title: "say hello", Original: fileInput.Body, RunID: w.state.RunID, Report: w.rep})

			if err != nil {
				t.Fatal(err)
			}
			creates := w.forge.Count(fakeforge.OpCreate)
			if creates != 1 || len(w.forge.Writes()) != 1 {
				t.Fatalf("%d create requests and %d writes, want exactly one", creates, len(w.forge.Writes()))
			}
			sent := w.forge.Issue(issue.Number)
			if sent.Title != "say hello" || !strings.HasPrefix(sent.Body, "## Original report\n\n"+fileInput.Body) || !strings.Contains(sent.Body, w.state.RunID) {
				t.Errorf("issue = %+v, want the subject, the report under its heading and the run", sent)
			}
			if !strings.HasSuffix(strings.TrimSpace(sent.Body), w.state.RunID+".") || issue.Number == 0 || !strings.Contains(issue.URL, fmt.Sprint(issue.Number)) {
				t.Errorf("body ends %q, issue %+v", sent.Body[max(0, len(sent.Body)-60):], issue)
			}
			if a := w.rep.artifact("issue"); a.Number != issue.Number || a.URL != issue.URL {
				t.Errorf("artifacts = %v, want the issue", w.rep.artifacts)
			}
			if e := w.rep.effect(EffectCreateIssue); e.State != toolio.StateDone || e.Target != "o/r" {
				t.Errorf("effects = %v, want create_issue done on o/r", w.rep.effects)
			}
		})
	}
}

// TestAnIssueIsFittedToTheForgeAndItsFailureIsReported guards the body of the issue and
// what a refusal of the forge comes to: a body that is too long for the forge is cut in the
// report and keeps its heading and its last line, and a failure is a forge failure with the
// side effect failed.
func TestAnIssueIsFittedToTheForgeAndItsFailureIsReported(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	long := strings.Repeat("0123456789 abcdefghij\n", 6000) // 132 KB, beyond what GitHub takes

	issue, err := CreateIssue(context.Background(), IssueParams{Forge: w.client, Target: w.target, Title: "t", Original: long, RunID: w.state.RunID, Report: w.rep})

	if err != nil {
		t.Fatal(err)
	}
	body := w.forge.Issue(issue.Number).Body
	if len(body) > issuex.MaxBodyLength(w.client) || !strings.HasPrefix(body, "## Original report\n\n") || !strings.HasSuffix(strings.TrimSpace(body), w.state.RunID+".") || !strings.Contains(body, toolio.TruncationMarker) {
		t.Errorf("the body is %d bytes (limit %d), starts %q and ends %q", len(body), issuex.MaxBodyLength(w.client), body[:40], body[max(0, len(body)-80):])
	}

	stopped, stop := context.WithCancel(context.Background())
	stop()
	if _, err := CreateIssue(stopped, IssueParams{Forge: w.client, Target: w.target, Title: "t", Original: "x", RunID: w.state.RunID, Report: w.rep}); !errors.Is(err, context.Canceled) {
		t.Errorf("a stopped run: %v, want the stop and not a failure of the forge", err)
	}
	w3 := newLandWorld(t, fakeforge.GitHub)
	w3.forge.Fail(fakeforge.OpCreate, fakeforge.Failure{Status: 401, Message: "Bad credentials"})
	_, err = CreateIssue(context.Background(), IssueParams{Forge: w3.client, Target: w3.target, Title: "t", Original: "x", RunID: w3.state.RunID, Report: w3.rep})
	if f := failure(t, err); f.Category != toolio.CategoryAuth || f.Retryable {
		t.Errorf("a credential the forge refuses: %+v, want auth", f)
	}

	w2 := newLandWorld(t, fakeforge.GitHub)
	w2.forge.Fail(fakeforge.OpCreate, fakeforge.Failure{Status: 500, Message: "boom"})
	_, err = CreateIssue(context.Background(), IssueParams{Forge: w2.client, Target: w2.target, Title: "t", Original: "x", RunID: w2.state.RunID, Report: w2.rep})
	if f := failure(t, err); f.Category != toolio.CategoryForge || !f.Retryable || !strings.Contains(f.Message, "boom") {
		t.Errorf("failure = %+v, want a retryable forge failure that carries the error", f)
	}
	if e := w2.rep.effect(EffectCreateIssue); e.State != toolio.StateFailed {
		t.Errorf("effects = %v, want create_issue failed", w2.rep.effects)
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
// --land pr the issue is made first for a text, the branch is pushed, and the draft pull
// request says Refs; under --land branch there is a push and no request to the forge; an
// interrupted run is neither pushed nor given a pull request, nor an issue.
// Verifies: 09-REQ-9.2
func TestAParkedRunPushesAndOpensADraftThatRefsTheIssue_TS_09_62(t *testing.T) {
	text := Input{Kind: "text", Body: "the greeting says helo"}
	for _, kind := range []string{fakeforge.GitHub, fakeforge.GitLab} {
		t.Run("pr on "+kind, func(t *testing.T) {
			w := newLandWorld(t, kind)

			res, err := w.park(t, text, LandPR, nil)

			if err != nil {
				t.Fatal(err)
			}
			requests := w.forge.Requests()
			var writes []string
			for _, r := range requests {
				if r.Op != "" && r.Op != fakeforge.OpRepo {
					writes = append(writes, r.Op)
				}
			}
			if !slices.Equal(writes, []string{fakeforge.OpCreate, fakeforge.OpPullRequest}) {
				t.Errorf("requests = %v, want the issue and then the pull request", writes)
			}
			if res.IssueCreated == nil || res.IssueCreated.Number == 0 || res.Branch != fmt.Sprintf("fix/%d-say-hello", res.IssueCreated.Number) {
				t.Errorf("issue %+v, branch %q, want the branch named by the issue that was made", res.IssueCreated, res.Branch)
			}
			prs := w.forge.PullRequests()
			if len(prs) != 1 || !prs[0].Draft || prs[0].Head != res.Branch || prs[0].Base != "main" || !strings.Contains(prs[0].Title, "wip: say hello") ||
				!strings.Contains(prs[0].Body, "Refs "+res.IssueCreated.URL) {
				t.Errorf("pull requests = %+v, want one draft from the branch whose body says Refs %s", prs, res.IssueCreated.URL)
			}
			if res.PullRequest == nil || !res.PullRequest.Draft || res.PullRequest.Number != prs[0].Number || res.PullRequest.URL == "" || !res.Pushed {
				t.Errorf("result = %+v", res)
			}
			if !w.remoteHas(t, res.Branch) {
				t.Error("the branch is not on the remote")
			}
			for _, kind := range []string{EffectBranch, EffectCommit, EffectPush, EffectCreateIssue, EffectCreatePullRequest} {
				if e := w.rep.effect(kind); e.State != toolio.StateDone {
					t.Errorf("effect %s = %+v, want done", kind, e)
				}
			}
			for _, kind := range []string{"branch", "commit", "issue", "pull_request"} {
				if a := w.rep.artifact(kind); a.Name == "" && a.URL == "" {
					t.Errorf("artifact %s is missing: %v", kind, w.rep.artifacts)
				}
			}
		})
	}

	t.Run("an existing issue is not made again", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		w.issueInput()

		res, err := w.park(t, Input{Kind: "issue", Origin: w.state.Issue.URL, Body: "x"}, LandPR, nil)

		if err != nil || res.IssueCreated != nil || w.forge.Count(fakeforge.OpCreate) != 0 {
			t.Fatalf("result %+v, %v, %d issues created, want none", res, err, w.forge.Count(fakeforge.OpCreate))
		}
		if prs := w.forge.PullRequests(); len(prs) != 1 || !strings.Contains(prs[0].Body, "Refs https://github.com/o/r/issues/7") {
			t.Errorf("pull requests = %+v", prs)
		}
	})
	t.Run("branch mode", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)

		res, err := w.park(t, text, LandBranch, nil)

		if err != nil || !res.Pushed || !w.remoteHas(t, res.Branch) || res.Branch != "fix/say-hello" {
			t.Fatalf("result %+v, %v", res, err)
		}
		if n := len(w.forge.Requests()); n != 0 || res.PullRequest != nil || res.IssueCreated != nil {
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
		if res.Pushed || w.remoteHas(t, res.Branch) || len(w.forge.Requests()) != 0 || res.PullRequest != nil || res.IssueCreated != nil || res.Branch != "fix/say-hello" {
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
	t.Run("an issue that cannot be made", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		w.forge.Fail(fakeforge.OpCreate, fakeforge.Failure{Status: 500, Message: "no issues today"})

		res, err := w.park(t, text, LandPR, nil)

		if err != nil || res.IssueCreated != nil || res.PullRequest != nil || res.Branch != "fix/say-hello" || !res.Pushed {
			t.Fatalf("result %+v, %v, want the work parked and pushed under a branch with no issue number", res, err)
		}
		if !slices.Contains(w.rep.warnings, WarnIssueCreationFailed) || !slices.Contains(w.rep.warnings, WarnPullRequestSkipped) || w.forge.Count(fakeforge.OpPullRequest) != 0 {
			t.Errorf("warnings = %v, want issue_creation_failed and pull_request_skipped", w.rep.warnings)
		}
	})
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
