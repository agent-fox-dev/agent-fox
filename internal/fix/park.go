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
	dirty, err := p.Git.DirtyFiles(ctx)
	if err != nil {
		return ParkResult{}, &toolio.Failure{Category: toolio.CategoryGit, Message: err.Error()}
	}
	if len(dirty) == 0 {
		return ParkResult{}, ErrNothingToPark
	}

	l := newLanding(p.Git, p.Report, p.State, p.Input, p.Mode, p.Forge, p.Target, p.Interrupted)
	l.subject = parkSubject(p)
	if err := l.ensureIssue(ctx); err != nil {
		return ParkResult{}, err
	}
	if err := l.makeBranch(ctx); err != nil {
		return ParkResult{}, err
	}
	if _, err := l.g.CommitAllNoVerify(ctx, parkMessage(l.subject, p.Verdict, p.Failing, l.issueURL, p.State.RunID, false)); err != nil {
		l.rep.SideEffect(toolio.SideEffect{Kind: EffectCommit, Target: l.branch, State: toolio.StateFailed, Detail: err.Error()})
		l.undoBranch()
		return ParkResult{}, &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("cannot commit the parked change: %v", err)}
	}
	return l.afterParkCommit(ctx, p.Verdict, p.Failing)
}

// afterParkCommit finishes a parking once the commit that parks the change is made: it is
// reported, pushed unless the run was interrupted or the mode keeps it here, offered as a
// draft pull request that refers to the issue, and the checkout goes back to the base.
func (l *landing) afterParkCommit(ctx context.Context, verdict string, failing []FailingCheck) (ParkResult, error) {
	rep := l.rep
	res := ParkResult{Outcome: OutcomeParked, Status: toolio.StatusUnverified, Branch: l.branch, IssueCreated: l.created}
	if err := l.recordCommit(); err != nil {
		return res, l.finish(err)
	}
	res.Commit = l.commit

	if !l.interrupted && l.mode != LandNone {
		if err := l.push(ctx); err != nil {
			rep.Warn(WarnPushFailed, fmt.Sprintf("the branch %s was not pushed and is kept here: %v", l.branch, err))
		} else {
			res.Pushed = true
		}
		if res.Pushed && l.mode == LandPR {
			if l.skipPR || l.issueURL == "" {
				rep.Warn(WarnPullRequestSkipped, "no pull request was opened: there is no issue for it to refer to")
			} else {
				res.PullRequest = l.openPullRequest(ctx, "wip: "+l.subject, parkedBody(l.state.RunID, verdict, failing, l.issueURL, l.hookRefused), true)
			}
		}
	}
	if err := l.backToBase(); err != nil {
		return res, err
	}

	why := "verdict " + verdict
	if l.hookRefused {
		why += ", the commit was refused by a hook of the repository"
	}
	rep.Warn(WarnFixNotLanded, fmt.Sprintf("the change did not land (%s) and is parked on the branch %s", why, l.branch))
	rep.Next(toolio.Next{Argv: []string{"git", "switch", l.branch}, Reason: "look at the parked change"})
	if !res.Pushed && !l.interrupted && l.mode != LandNone {
		rep.Next(toolio.Next{Argv: []string{"git", "push", "-u", "origin", l.branch}, Reason: "publish the parked branch"})
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

// parkMessage is the message of the commit that parks a change; hook says a hook of the
// repository refused the commit of the landing, which is why the change is parked.
func parkMessage(subject, verdict string, failing []FailingCheck, issueURL, runID string, hook bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "wip: %s\n\nParked by af fix: verdict %s\n", subject, verdict)
	if hook {
		b.WriteString("The commit was refused by a hook of the repository.\n")
	}
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

// parkedBody is the body of the draft pull request of a parked change.
func parkedBody(runID, verdict string, failing []FailingCheck, issueURL string, hook bool) string {
	var b strings.Builder
	b.WriteString(runMarker(runID) + "\n")
	reason := "the change did not pass the gate"
	if hook {
		reason = "a hook of the repository refused the commit"
	}
	fmt.Fprintf(&b, "This draft pull request was opened by af fix because %s (verdict: %s). It keeps the work; it is not ready to merge.\n", reason, verdict)
	if len(failing) > 0 {
		b.WriteString("\nFailing checks:\n")
		for _, f := range failing {
			fmt.Fprintf(&b, "- %s\n", singleLine(f.Name))
		}
	}
	fmt.Fprintf(&b, "\nRefs %s\n\nRun %s.\n", issueURL, runID)
	return b.String()
}
