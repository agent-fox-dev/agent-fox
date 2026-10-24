package fix

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The side effects and the artifacts of a landing or a parking.
const (
	EffectBranch            = "branch"
	EffectCommit            = "commit"
	EffectPush              = "push"
	EffectCreatePullRequest = "create_pull_request"
)

// Reporter is where a landing says what it did: its warnings, what it produced, what it
// wrote beyond the working tree, and what to do next. A *toolio.Run is one.
type Reporter interface {
	Warn(code, message string)
	Artifact(a toolio.Artifact)
	SideEffect(s toolio.SideEffect)
	Next(n toolio.Next)
}

// IssueRef is an issue that was created.
type IssueRef struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// PullRequestRef is a pull request that was opened.
type PullRequestRef struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Draft  bool   `json:"draft"`
}

// runMarker is the comment that tells which run a pull request is from.
func runMarker(runID string) string { return "<!-- agent-fox:run " + runID + " -->" }

// BranchName is fix/<N>-<slug> for an issue, and fix/<slug> without one, where the slug is
// that of the title.
func BranchName(number int, title string) string {
	if number > 0 {
		return fmt.Sprintf("fix/%d-%s", number, gitx.Slug(title))
	}
	return "fix/" + gitx.Slug(title)
}

// MakeBranch names the branch for the issue and the title, makes the name unique among the
// local and the remote branches, creates it at the commit the run began at with the
// working tree carried along, and reports it.
func MakeBranch(ctx context.Context, g *gitx.Git, rep Reporter, number int, title, baseHead string) (string, error) {
	name := gitx.UniqueBranchName(ctx, g, BranchName(number, title))
	if err := g.CreateBranchFrom(ctx, name, baseHead); err != nil {
		rep.SideEffect(toolio.SideEffect{Kind: EffectBranch, Target: name, State: toolio.StateFailed, Detail: err.Error()})
		return "", &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("cannot create the branch %s at %s: %v", name, baseHead, err)}
	}
	rep.SideEffect(toolio.SideEffect{Kind: EffectBranch, Target: name, State: toolio.StateDone})
	rep.Artifact(toolio.Artifact{Kind: "branch", Name: name})
	return name, nil
}

// cutAtRune is s cut to at most n bytes at the end of a character.
func cutAtRune(s string, n int) string {
	if n >= len(s) {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// oneLine is a subject as a single line.
func singleLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// The outcome of a run that landed its change.
const OutcomeLanded = "landed"

// The warnings of a landing.
const (
	WarnCommitHookFailed = "commit_hook_failed"
	WarnCriteriaUnmet    = "criteria_unmet"
	WarnNotVerified      = "not_verified"
	WarnReportUnwritable = "report_unwritable"
)

// maxChangeBytes is how much of the report's change the message of the commit holds.
const maxChangeBytes = 2000

// LandParams is what a measured change that lands is landed with.
type LandParams struct {
	Git   *gitx.Git
	State State
	// Input is the report as it was given, and Report the fix report that goes with the
	// change.
	Input  Input
	Report Report
	// Mode is pr, branch or none.
	Mode   string
	Forge  issuex.Client
	Target issuex.Repo
	// Measured is what af measured of the change.
	Measured Measurement
	// Brain, CostUSD and CostSource are what the run used, for the footer of the body.
	Brain      string
	CostUSD    float64
	CostSource string
	Reporter   Reporter
	// WriteReport keeps the body as the report of the run; it may be nil.
	WriteReport func(text string) error
	// PostPush is called with the branch once it is pushed, before any pull request is asked for;
	// nil is none. An error from it fails the push.
	PostPush func(ctx context.Context, branch string) error
}

// LandResult is what landing did.
type LandResult struct {
	// Outcome is landed, or parked when a hook of the repository stopped the commit.
	Outcome string
	// Status is done, or unverified for a change that was landed without the gate and for
	// one that was parked.
	Status      toolio.Status
	Branch      string
	Commit      string
	Pushed      bool
	PullRequest *PullRequestRef
}

// Land lands a change that was measured: a branch named by the issue, one commit that af makes, with the hooks of the repository, the push and the pull
// request that the mode asks for, and the checkout back on the base with a clean tree. A
// commit that a hook stops is parked instead. A push that fails after its retries is a
// failure, with the branch and the commit kept; a pull request that fails after the push
// is a warning on a change that landed.
func Land(ctx context.Context, p LandParams) (LandResult, error) {
	l := newLanding(p.Git, p.Reporter, p.State, p.Input, p.Mode, p.Forge, p.Target, false)
	l.postPush = p.PostPush
	rep := p.Reporter
	l.subject = singleLine(p.Report.Subject)
	l.takeIssue()
	if err := l.makeBranch(ctx); err != nil {
		return LandResult{}, err
	}
	res := LandResult{Outcome: OutcomeLanded, Status: toolio.StatusDone, Branch: l.branch}

	typ := p.Report.Type
	if typ == "" {
		typ = DefaultType
	}
	closes := p.Measured.Closes
	body := RenderBody(BodyFacts{
		RunID: p.State.RunID, Verdict: p.Measured.Verdict, Rows: p.Measured.Rows, RevertCheck: p.Measured.RevertCheck, Reverted: p.Measured.Reverted,
		NoVerify: p.State.NoVerify, Report: p.Report, Changed: p.Measured.Changed, IssueURL: l.issueURL, Closes: closes,
		Brain: p.Brain, CostUSD: p.CostUSD, CostSource: p.CostSource,
	}, issuex.MaxBodyLength(p.Forge))
	if p.WriteReport != nil {
		if err := p.WriteReport(body); err != nil {
			rep.Warn(WarnReportUnwritable, fmt.Sprintf("report.md could not be written: %v", err))
		}
	}

	if _, err := l.g.CommitAll(ctx, landMessage(typ, l.subject, p.Report.Change, l.issueURL, closes, p.State.RunID)); err != nil {
		parked, perr := l.parkAfterRefusedCommit(ctx, p, err)
		return parked, perr
	}
	if err := l.recordCommit(); err != nil {
		return res, l.finish(err)
	}
	res.Commit = l.commit

	if p.Mode != LandNone {
		wantPR := p.Mode == LandPR
		draft := p.State.NoVerify
		if unmet := unmetCriteria(p.Report); wantPR && len(unmet) > 0 {
			draft = true
			rep.Warn(WarnCriteriaUnmet, "the pull request is a draft: "+strings.Join(unmet, "; "))
		}
		if err := l.push(ctx); err != nil {
			return res, l.finish(l.pushFailure(err))
		}
		res.Pushed = true
		if wantPR {
			res.PullRequest = l.openPullRequest(ctx, typ+": "+l.subject, body, draft)
		}
	}
	if err := l.backToBase(); err != nil {
		return res, err
	}
	if p.State.NoVerify {
		res.Status = toolio.StatusUnverified
		rep.Warn(WarnNotVerified, "the change was landed without the gate (--no-verify): nothing was measured, and a person has to check it")
	}
	return res, nil
}

// unmetCriteria are the criteria the report says are unmet or only partly met.
func unmetCriteria(r Report) []string {
	var out []string
	for _, c := range r.Criteria {
		if c.Status == StatusUnmet || c.Status == StatusPartial {
			out = append(out, fmt.Sprintf("%s (%s)", singleLine(c.Criterion), c.Status))
		}
	}
	return out
}

// landMessage is the message of the commit that lands a change: the subject, the change as
// far as it goes, the issue that is closed or referred to, and the trailer.
func landMessage(typ, subject, change, issueURL string, closes bool, runID string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\n", typ, subject)
	if change = strings.TrimSpace(cutAtRune(strings.TrimSpace(change), maxChangeBytes)); change != "" {
		fmt.Fprintf(&b, "\n%s\n", change)
	}
	if issueURL != "" {
		word := "Refs"
		if closes {
			word = "Closes"
		}
		fmt.Fprintf(&b, "\n%s %s\n", word, issueURL)
	}
	fmt.Fprintf(&b, "\n%s: %s\n", trailerName, runID)
	return b.String()
}

// parkAfterRefusedCommit parks the change on the branch that the landing made, when the
// commit was refused, on the guess that a hook of the repository did it: the same change is
// committed without the hooks, and if that works the hook is named in a warning.
func (l *landing) parkAfterRefusedCommit(ctx context.Context, p LandParams, refused error) (LandResult, error) {
	verdict := p.Measured.Verdict
	l.hookRefused = true
	if _, err := l.g.CommitAllNoVerify(ctx, parkMessage(l.subject, verdict, nil, l.issueURL, p.State.RunID, true)); err != nil {
		l.rep.SideEffect(toolio.SideEffect{Kind: EffectCommit, Target: l.branch, State: toolio.StateFailed, Detail: err.Error()})
		l.undoBranch()
		return LandResult{}, &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("cannot commit the change: %v", refused)}
	}
	l.rep.Warn(WarnCommitHookFailed, fmt.Sprintf("the commit was refused, by a hook of the repository: %s; the change is parked", firstChars(refused.Error(), 300)))
	parked, err := l.afterParkCommit(ctx, verdict, nil)
	return LandResult(parked), err
}

func firstChars(s string, n int) string {
	s = singleLine(s)
	if len(s) > n {
		return cutAtRune(s, n) + "..."
	}
	return s
}

// landing is what Land and Park share: the issue, the branch, the commit that was made, the
// push, the pull request and the way back to the base.
type landing struct {
	g      *gitx.Git
	rep    Reporter
	state  State
	input  Input
	mode   string
	forge  issuex.Client
	target issuex.Repo
	// interrupted says the run was stopped: nothing goes beyond the machine.
	interrupted bool

	subject string
	// number, title and issueURL name the issue of the run.
	number   int
	title    string
	issueURL string
	// hookRefused says the change is parked because a hook of the repository refused its commit,
	// and not because the gate refused the change.
	hookRefused bool
	branch      string
	commit      string
	pushed      bool
	// postPush is called with the branch once it is pushed; it may be nil. published says the
	// push was made and the hook did not take the branch.
	postPush  func(ctx context.Context, branch string) error
	published bool
	// back is a context that the cancellation of the run did not reach, for the way back.
	back context.Context
}

// publishError is a branch that was pushed to origin and that the post-push hook did not take.
type publishError struct{ err error }

func (e *publishError) Error() string { return e.err.Error() }
func (e *publishError) Unwrap() error { return e.err }

func newLanding(g *gitx.Git, rep Reporter, state State, input Input, mode string, forge issuex.Client, target issuex.Repo, interrupted bool) *landing {
	return &landing{g: g, rep: rep, state: state, input: input, mode: mode, forge: forge, target: target, interrupted: interrupted}
}

// takeIssue takes the issue the run is for, which names the branch and the pull request.
func (l *landing) takeIssue() {
	l.title = l.subject
	if l.state.Issue != nil {
		l.number, l.title, l.issueURL = l.state.Issue.Number, l.state.Issue.Title, l.state.Issue.URL
	}
}

// makeBranch makes the branch of the change, and the context the way back is made by.
func (l *landing) makeBranch(ctx context.Context) error {
	branch, err := MakeBranch(ctx, l.g, l.rep, l.number, l.title, l.state.BaseHead)
	if err != nil {
		return err
	}
	l.branch = branch
	// Whatever else happens, the checkout goes back to the base: a context that is
	// cancelled does not leave it on the branch.
	l.back = context.WithoutCancel(ctx)
	return nil
}

// undoBranch takes the branch away when nothing could be committed to it, and leaves the
// change in the working tree of the base.
func (l *landing) undoBranch() {
	_ = l.g.Checkout(l.back, l.state.Base)
	_ = l.g.DeleteBranch(l.back, l.branch)
}

// recordCommit reads the full hash of the commit that was made, and reports it.
func (l *landing) recordCommit() error {
	commit, err := l.g.RevParse(l.back, "HEAD")
	if err != nil {
		return &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("cannot read the commit that was made: %v", err)}
	}
	l.commit = commit
	l.rep.SideEffect(toolio.SideEffect{Kind: EffectCommit, Target: l.branch, State: toolio.StateDone})
	l.rep.Artifact(toolio.Artifact{Kind: "commit", Name: commit})
	return nil
}

// push publishes the branch with its retries, and then hands it to the post-push hook, which
// is how a caller passes the branch on from where it was pushed. It reports how it went. A
// branch that the hook did not take is pushed all the same, and stays where it is.
func (l *landing) push(ctx context.Context) error {
	err := l.g.Push(ctx, l.branch, maxPushAttempts, func(msg string) { l.rep.Warn(WarnPushRetried, msg) })
	if err != nil {
		l.rep.SideEffect(toolio.SideEffect{Kind: EffectPush, Target: "origin/" + l.branch, State: toolio.StateFailed, Detail: err.Error()})
		return err
	}
	if l.postPush != nil {
		if err := l.postPush(ctx, l.branch); err != nil {
			l.rep.SideEffect(toolio.SideEffect{Kind: EffectPush, Target: "origin/" + l.branch, State: toolio.StateFailed, Detail: "pushed, and not published: " + err.Error()})
			return &publishError{err}
		}
	}
	l.pushed = true
	l.rep.SideEffect(toolio.SideEffect{Kind: EffectPush, Target: "origin/" + l.branch, State: toolio.StateDone})
	return nil
}

// pushFailure is the failure of a landing whose push failed, with the way on that it reports:
// to push the branch again, or, for a branch that is pushed and was not published, to look at it.
func (l *landing) pushFailure(err error) *toolio.Failure {
	var published *publishError
	if errors.As(err, &published) {
		l.rep.Next(toolio.Next{Argv: []string{"git", "show", "--stat", l.branch}, Reason: "the branch is pushed to origin and was not published; the change is on it"})
		return &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("the branch %s is pushed, and could not be published: %v", l.branch, published)}
	}
	l.rep.Next(toolio.Next{Argv: []string{"git", "push", "-u", "origin", l.branch}, Reason: "publish the branch that is committed here"})
	return &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("the branch %s is committed here, and could not be pushed: %v", l.branch, err)}
}

// openPullRequest opens the pull request of the branch. A forge that refuses it is a
// warning: the branch is pushed, and the change has landed.
func (l *landing) openPullRequest(ctx context.Context, title, body string, draft bool) *PullRequestRef {
	target := l.target.String()
	pr, err := l.forge.CreatePullRequest(ctx, l.target, issuex.CreatePullRequestRequest{Title: title, Body: body, Head: l.branch, Base: l.state.Base, Draft: draft})
	if err != nil {
		l.rep.SideEffect(toolio.SideEffect{Kind: EffectCreatePullRequest, Target: target, State: toolio.StateFailed, Detail: err.Error()})
		l.rep.Warn(WarnPullRequestFailed, fmt.Sprintf("the branch is pushed, but the pull request was not opened: %v", err))
		return nil
	}
	ref := &PullRequestRef{Number: pr.Number, URL: firstNonEmpty(pr.HTMLURL, pr.URL), Draft: pr.Draft}
	l.rep.SideEffect(toolio.SideEffect{Kind: EffectCreatePullRequest, Target: target, State: toolio.StateDone})
	l.rep.Artifact(toolio.Artifact{Kind: "pull_request", URL: ref.URL, Number: ref.Number})
	return ref
}

// backToBase puts the checkout on the base, and says if the tree is not clean.
func (l *landing) backToBase() error {
	if err := l.g.Checkout(l.back, l.state.Base); err != nil {
		return &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("the change is on the branch %s, but the checkout cannot go back to %s: %v", l.branch, l.state.Base, err)}
	}
	if left, err := l.g.DirtyFiles(l.back); err == nil && len(left) > 0 {
		return &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("the change is on the branch %s, but the tree of %s is not clean: %s", l.branch, l.state.Base, strings.Join(left, ", "))}
	}
	return nil
}

// finish puts the checkout back on the base after a failure, and returns the failure.
func (l *landing) finish(failure error) error {
	if err := l.backToBase(); err != nil {
		return errors.Join(failure, err)
	}
	return failure
}
