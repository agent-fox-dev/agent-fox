package fix

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The outcome of a run whose change was kept and not landed.
const OutcomeParked = "parked"

// The warnings of a parking.
const (
	WarnFixNotLanded        = "fix_not_landed"
	WarnPushRetried         = "push_retried"
	WarnPushFailed          = "push_failed"
	WarnPullRequestFailed   = "pull_request_failed"
	WarnPullRequestSkipped  = "pull_request_skipped"
	WarnIssueCreationFailed = "issue_creation_failed"
)

// ErrNothingToPark says the checkout holds no change: there is nothing to keep.
var ErrNothingToPark = errors.New("the checkout has no change, so there is nothing to park")

// maxPushAttempts is how many times a push is tried.
const maxPushAttempts = 3

// trailerName is the trailer that ends every commit af makes.
const trailerName = "Agent-Fox-Run"

// ParkParams is what a change that does not land is parked with.
type ParkParams struct {
	Git   *gitx.Git
	State State
	// Input is the report, as it was given.
	Input Input
	// Mode is pr, branch or none.
	Mode   string
	Forge  issuex.Client
	Target issuex.Repo
	// Subject is the subject of the fix, or empty when the brain never gave one.
	Subject string
	// Verdict and Failing say why the change did not land.
	Verdict string
	Failing []FailingCheck
	// Interrupted says the run was stopped: the change is kept on a local branch, and
	// nothing is pushed and nothing is written to the forge.
	Interrupted bool
	Report      Reporter
}

// ParkResult is what parking did.
type ParkResult struct {
	Outcome string
	// Status is the status of a run that parked: unverified.
	Status       toolio.Status
	Branch       string
	Commit       string
	Pushed       bool
	PullRequest  *PullRequestRef
	IssueCreated *IssueRef
}

// Park keeps the change that is in the working tree on a branch of its own, as a commit that
// says it is work in progress, and puts the checkout back on the base with a clean tree. The
// branch is named as a landed one is. Unless the run was interrupted, the branch is pushed
// (not under --land none) and, under --land pr, a draft pull request that refers to the
// issue is opened, the issue being made first for a text or a file. The commit is made
// first and without the hooks of the repository, and nothing after it can lose the work: a
// push, a pull request or an issue that fails is a warning.
func Park(ctx context.Context, p ParkParams) (ParkResult, error) {
	g, rep := p.Git, p.Report
	dirty, err := g.DirtyFiles(ctx)
	if err != nil {
		return ParkResult{}, &toolio.Failure{Category: toolio.CategoryGit, Message: err.Error()}
	}
	if len(dirty) == 0 {
		return ParkResult{}, ErrNothingToPark
	}

	subject := parkSubject(p)
	res := ParkResult{Outcome: OutcomeParked, Status: toolio.StatusUnverified}
	number, title, issueURL := 0, subject, ""
	if p.State.Issue != nil {
		number, title, issueURL = p.State.Issue.Number, p.State.Issue.Title, p.State.Issue.URL
	}
	skipPullRequest := false
	if !p.Interrupted && NeedsIssue(p.Input.Kind, p.Mode) {
		ref, err := CreateIssue(ctx, IssueParams{Forge: p.Forge, Target: p.Target, Title: subject, Original: p.Input.Body, RunID: p.State.RunID, Report: rep})
		switch {
		case err == nil:
			res.IssueCreated = &ref
			number, issueURL = ref.Number, ref.URL
		case ctx.Err() != nil:
			return ParkResult{}, ctx.Err()
		default:
			rep.Warn(WarnIssueCreationFailed, err.Error()+"; the change is parked without an issue")
			skipPullRequest = true
		}
	}

	branch, err := MakeBranch(ctx, g, rep, number, title, p.State.BaseHead)
	if err != nil {
		return ParkResult{}, err
	}
	res.Branch = branch
	// Whatever else happens, the checkout goes back to the base: a context that is
	// cancelled does not leave it on the branch.
	back := context.WithoutCancel(ctx)
	if _, err := g.CommitAllNoVerify(ctx, parkMessage(subject, p.Verdict, p.Failing, issueURL, p.State.RunID)); err != nil {
		rep.SideEffect(toolio.SideEffect{Kind: EffectCommit, Target: branch, State: toolio.StateFailed, Detail: err.Error()})
		_ = g.Checkout(back, p.State.Base)
		_ = g.DeleteBranch(back, branch)
		return ParkResult{}, &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("cannot commit the parked change: %v", err)}
	}
	if res.Commit, err = g.RevParse(back, "HEAD"); err != nil {
		return ParkResult{}, &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("cannot read the commit that was made: %v", err)}
	}
	rep.SideEffect(toolio.SideEffect{Kind: EffectCommit, Target: branch, State: toolio.StateDone})
	rep.Artifact(toolio.Artifact{Kind: "commit", Name: res.Commit})

	if !p.Interrupted && p.Mode != LandNone {
		res.Pushed = push(ctx, p, branch)
		if res.Pushed && p.Mode == LandPR {
			switch {
			case skipPullRequest || issueURL == "":
				rep.Warn(WarnPullRequestSkipped, "no pull request was opened: there is no issue for it to refer to")
			default:
				res.PullRequest = openDraft(ctx, p, branch, subject, issueURL)
			}
		}
	}

	if err := g.Checkout(back, p.State.Base); err != nil {
		return res, &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("the change is parked on %s, but the checkout cannot go back to %s: %v", branch, p.State.Base, err)}
	}
	if left, err := g.DirtyFiles(back); err == nil && len(left) > 0 {
		return res, &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("the change is parked on %s, but the tree of %s is not clean: %s", branch, p.State.Base, strings.Join(left, ", "))}
	}

	rep.Warn(WarnFixNotLanded, fmt.Sprintf("the change did not land (verdict %s) and is parked on the branch %s", p.Verdict, branch))
	rep.Next(toolio.Next{Argv: []string{"git", "switch", branch}, Reason: "look at the parked change"})
	if !res.Pushed && !p.Interrupted && p.Mode != LandNone {
		rep.Next(toolio.Next{Argv: []string{"git", "push", "-u", "origin", branch}, Reason: "publish the parked branch"})
	}
	return res, nil
}

// parkSubject is the subject of the commit: the brain's, else the title of the issue, else
// the first line of the report, else a plain word for what it is.
func parkSubject(p ParkParams) string {
	for _, s := range []string{p.Subject, issueTitle(p.State), p.Input.Body} {
		if s = singleLine(s); s != "" {
			return cutAtRune(s, maxSubject)
		}
	}
	return "unverified change"
}

func issueTitle(s State) string {
	if s.Issue != nil {
		return s.Issue.Title
	}
	return ""
}

// parkMessage is the message of the commit that parks a change.
func parkMessage(subject, verdict string, failing []FailingCheck, issueURL, runID string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "wip: %s\n\nParked by af fix: verdict %s\n", subject, verdict)
	if len(failing) > 0 {
		b.WriteString("Failing checks:\n")
		for _, f := range failing {
			fmt.Fprintf(&b, "- %s\n", singleLine(f.Name))
		}
	}
	if issueURL != "" {
		fmt.Fprintf(&b, "\nRefs %s\n", issueURL)
	}
	fmt.Fprintf(&b, "\n%s: %s\n", trailerName, runID)
	return b.String()
}

// push publishes the branch, and says how it went.
func push(ctx context.Context, p ParkParams, branch string) bool {
	rep := p.Report
	err := p.Git.Push(ctx, branch, maxPushAttempts, func(msg string) { rep.Warn(WarnPushRetried, msg) })
	if err != nil {
		rep.SideEffect(toolio.SideEffect{Kind: EffectPush, Target: "origin/" + branch, State: toolio.StateFailed, Detail: err.Error()})
		rep.Warn(WarnPushFailed, fmt.Sprintf("the branch %s was not pushed and is kept here: %v", branch, err))
		return false
	}
	rep.SideEffect(toolio.SideEffect{Kind: EffectPush, Target: "origin/" + branch, State: toolio.StateDone})
	return true
}

// openDraft opens the draft pull request of a parked change, which refers to the issue and
// does not close it. A forge that refuses it is a warning.
func openDraft(ctx context.Context, p ParkParams, branch, subject, issueURL string) *PullRequestRef {
	rep := p.Report
	target := p.Target.String()
	pr, err := p.Forge.CreatePullRequest(ctx, p.Target, issuex.CreatePullRequestRequest{
		Title: "wip: " + subject, Body: parkedBody(p, issueURL), Head: branch, Base: p.State.Base, Draft: true,
	})
	if err != nil {
		rep.SideEffect(toolio.SideEffect{Kind: EffectCreatePullRequest, Target: target, State: toolio.StateFailed, Detail: err.Error()})
		rep.Warn(WarnPullRequestFailed, fmt.Sprintf("the branch is pushed, but the pull request was not opened: %v", err))
		return nil
	}
	ref := &PullRequestRef{Number: pr.Number, URL: firstNonEmpty(pr.HTMLURL, pr.URL), Draft: pr.Draft}
	rep.SideEffect(toolio.SideEffect{Kind: EffectCreatePullRequest, Target: target, State: toolio.StateDone})
	rep.Artifact(toolio.Artifact{Kind: "pull_request", URL: ref.URL, Number: ref.Number})
	return ref
}

// parkedBody is the body of the draft pull request of a parked change.
func parkedBody(p ParkParams, issueURL string) string {
	var b strings.Builder
	b.WriteString(runMarker(p.State.RunID) + "\n")
	fmt.Fprintf(&b, "This draft pull request was opened by af fix because the change did not pass the gate (verdict: %s). It keeps the work; it is not ready to merge.\n", p.Verdict)
	if len(p.Failing) > 0 {
		b.WriteString("\nFailing checks:\n")
		for _, f := range p.Failing {
			fmt.Fprintf(&b, "- %s\n", singleLine(f.Name))
		}
	}
	fmt.Fprintf(&b, "\nRefs %s\n\nRun %s.\n", issueURL, p.State.RunID)
	return b.String()
}
