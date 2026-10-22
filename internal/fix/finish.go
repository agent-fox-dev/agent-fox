package fix

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The warnings of the end of a run.
const (
	WarnAttemptFailed = "attempt_failed"
	WarnTreeKept      = "dry_run_tree_kept"
	WarnParkFailed    = "park_failed"
	// WarnQuestionTreeKept is raised when a run that ended on a question could not put back the change
	// of a round before it.
	WarnQuestionTreeKept = "question_tree_kept"
)

// ParkTimeout bounds the parking of a change when the run was stopped: it is done on a
// context that the stop did not reach, and has this long to finish.
const ParkTimeout = 10 * time.Second

// verdictInterrupted is the verdict a change is parked under when the run was stopped before
// it could be judged.
const verdictInterrupted = "interrupted"

// FinishParams is what the end of a loop is turned into the end of the run with.
type FinishParams struct {
	Git   *gitx.Git
	State State
	Input Input
	// Mode is pr, branch or none; DryRun says nothing is written beyond the working tree,
	// and the forge is the one that refuses its writes.
	Mode   string
	DryRun bool
	Forge  issuex.Client
	Target issuex.Repo
	// Ended and LoopErr are what the loop returned.
	Ended   LoopResult
	LoopErr error
	// AttemptsMax is the most attempts the run could make, and Box the box it used.
	AttemptsMax int
	Box         *BoxInfo
	Reporter    Reporter
	// WriteReport keeps the body of the pull request as the report of the run.
	WriteReport func(text string) error
	// PostPush is called with the branch once it is pushed, before the pull request; see
	// LandParams.
	PostPush func(ctx context.Context, branch string) error
	// Steps is told how the landing or the parking comes on; its plan carries the result of the run as
	// it stands.
	Steps LandSteps
}

// Ending is the end of a run: the result, the status it has, and the question when it ended
// on one.
type Ending struct {
	Result   Result
	Status   toolio.Status
	Summary  string
	Question *Question
}

// Finish turns what the loop came to into the end of the run. A change that may land is
// landed, and one that was refused with no attempt left is parked, under the report it was
// made with; a run with nothing applied fails with the failure of its last round; a question
// is the end of the run, and the change that an earlier round applied and was refused is put
// back, since the run starts again from the base with the answer. Under a dry run the change is discarded
// instead and the writes are planned, with the status the real run would have had. A run
// that was stopped parks what it applied on a local branch, on a context that the stop did
// not reach, or discards it under a dry run, and returns the stop.
func Finish(ctx context.Context, p FinishParams) (Ending, error) {
	if p.LoopErr != nil {
		return Ending{}, p.stopped(ctx, p.LoopErr)
	}
	ended := p.Ended
	in := ResultInput{State: p.State, Attempts: ended.Attempts, Rounds: ended.Rounds, AttemptsMax: p.AttemptsMax, Land: p.Mode, Box: p.Box}
	switch ended.End {
	case EndQuestion:
		if ended.HoldsChange {
			p.discard(ctx, WarnQuestionTreeKept, "the change of an earlier round of the run")
		}
		in.Outcome = OutcomeNeedsHuman
		return Ending{Result: NewResult(in), Status: toolio.StatusNeedsHuman, Summary: "the brain asks a question, and nothing was applied to the checkout", Question: ended.Question}, nil
	case EndLandable:
		return p.landable(ctx, in)
	}
	return p.exhausted(ctx, in)
}

// landable lands the change that may land, or plans to.
func (p FinishParams) landable(ctx context.Context, in ResultInput) (Ending, error) {
	ended := p.Ended
	in.Measured = &ended.Measured
	status := toolio.StatusDone
	if p.State.NoVerify {
		status = toolio.StatusUnverified
	}
	if p.DryRun {
		p.plan(ctx, false)
		p.discard(ctx, WarnTreeKept, "the change of the dry run")
		in.Outcome = OutcomeDryRun
		return Ending{Result: NewResult(in), Status: status, Summary: "a dry run: the change would land, and nothing was written"}, nil
	}
	landed, err := Land(ctx, LandParams{
		Git: p.Git, State: p.State, Input: p.Input, Report: *ended.Report, Mode: p.Mode, Forge: p.Forge, Target: p.Target, Measured: ended.Measured,
		Brain: p.State.Brain, CostUSD: p.State.CostUSD, CostSource: costSource(p.State), Reporter: p.Reporter, WriteReport: p.WriteReport,
		PostPush: p.PostPush, Steps: p.stepsFor(in),
	})
	if err != nil {
		if ctx.Err() != nil { // stopped while it landed: what was measured is parked, not left in the tree
			return Ending{}, p.stopped(ctx, ctx.Err())
		}
		return Ending{}, err
	}
	in.Outcome, in.Branch, in.Commit, in.Pushed, in.PullRequest, in.IssueCreated = landed.Outcome, landed.Branch, landed.Commit, landed.Pushed, landed.PullRequest, landed.IssueCreated
	return Ending{Result: NewResult(in), Status: landed.Status, Summary: summary(landed.Outcome, landed.Branch, landed.PullRequest)}, nil
}

// exhausted ends a run that has no attempt left and no change that may land: the change in
// the tree is parked, and with none the run fails.
func (p FinishParams) exhausted(ctx context.Context, in ResultInput) (Ending, error) {
	ended := p.Ended
	if !ended.HoldsChange {
		if ended.LastFailure != nil {
			return Ending{}, ended.LastFailure
		}
		return Ending{}, &toolio.Failure{Category: toolio.CategoryNoResult, Message: "no round of the brain left a change in the checkout: the last was refused as having changed nothing"}
	}
	verdict, failing := "unverified", []FailingCheck(nil)
	if refused := ended.LastRefusal; refused != nil {
		in.Measured = refused
		verdict = refused.Verdict
		if refused.Refusal != nil {
			failing = refused.Refusal.Failing
		}
	}
	if ended.LastFailure != nil {
		p.Reporter.Warn(WarnAttemptFailed, "the last round did not come to a report: "+ended.LastFailure.Error())
	}
	if p.DryRun {
		p.plan(ctx, true)
		p.discard(ctx, WarnTreeKept, "the change of the dry run")
		in.Outcome = OutcomeDryRun
		return Ending{Result: NewResult(in), Status: toolio.StatusUnverified, Summary: "a dry run: the change would be parked, and nothing was written"}, nil
	}
	parked, err := Park(ctx, ParkParams{
		Git: p.Git, State: p.State, Input: p.Input, Mode: p.Mode, Forge: p.Forge, Target: p.Target, Subject: p.subject(),
		Verdict: verdict, Failing: failing, Report: p.Reporter, PostPush: p.PostPush, Steps: p.stepsFor(in),
	})
	if err != nil {
		return Ending{}, err
	}
	in.Outcome, in.Branch, in.Commit, in.Pushed, in.PullRequest, in.IssueCreated = parked.Outcome, parked.Branch, parked.Commit, parked.Pushed, parked.PullRequest, parked.IssueCreated
	return Ending{Result: NewResult(in), Status: parked.Status, Summary: summary(parked.Outcome, parked.Branch, parked.PullRequest)}, nil
}

// stepsFor is the steps of the caller with a plan that carries the result of the run as it stands, which
// is the input of the result with what the landing came to so far.
func (p FinishParams) stepsFor(in ResultInput) LandSteps {
	steps := p.Steps
	if steps.Plan == nil {
		return steps
	}
	inner := steps.Plan
	steps.Plan = func(ctx context.Context, plan Plan) error {
		in := in
		in.Outcome, in.Branch, in.Commit = plan.Outcome, plan.Branch, plan.Commit
		plan.Result = NewResult(in)
		return inner(ctx, plan)
	}
	return steps
}

// subject is the subject of the last report that was accepted, or none.
func (p FinishParams) subject() string {
	if p.Ended.Report != nil {
		return p.Ended.Report.Subject
	}
	return ""
}

// stopped ends a run that did not come to an end of its loop, or that was stopped while it
// landed: the work is kept or discarded, and the error is the cause. A run that was stopped
// parks what it applied on a local branch; one that failed to measure leaves the work where
// it is, unless a dry run is to leave nothing.
func (p FinishParams) stopped(ctx context.Context, cause error) error {
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), ParkTimeout)
	defer cancel()
	switch {
	case p.DryRun:
		p.discard(detached, WarnTreeKept, "the change of the dry run")
	case errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded):
		_, err := Park(detached, ParkParams{
			Git: p.Git, State: p.State, Input: p.Input, Mode: LandNone, Subject: p.subject(), Verdict: verdictInterrupted, Interrupted: true, Report: p.Reporter,
		})
		if err != nil && !errors.Is(err, ErrNothingToPark) {
			p.Reporter.Warn(WarnParkFailed, fmt.Sprintf("the change could not be parked after the run was stopped, and is in the working tree: %v", err))
		}
	}
	return cause
}

// discard puts the checkout back as the run found it: the applied change goes. A checkout that
// is no longer at the commit the run began at has been moved by someone, and is left alone,
// with the warning of the code, which says what the change was.
func (p FinishParams) discard(ctx context.Context, code, what string) {
	branch, berr := p.Git.CurrentBranch(ctx)
	head, herr := p.Git.RevParse(ctx, "HEAD")
	if berr != nil || herr != nil || branch != p.State.Base || head != p.State.BaseHead {
		p.Reporter.Warn(code, fmt.Sprintf("the checkout is not on %s at %s any more, so %s was not discarded", p.State.Base, p.State.BaseHead, what))
		return
	}
	if err := p.Git.ResetHard(ctx, p.State.BaseHead); err != nil {
		p.Reporter.Warn(code, fmt.Sprintf("%s could not be discarded: %v", what, err))
		return
	}
	if err := p.Git.Clean(ctx); err != nil {
		p.Reporter.Warn(code, fmt.Sprintf("the files of %s could not be removed: %v", what, err))
	}
}

// plan lists what the real run would have written beyond the working tree: the issue of a text
// (which the forge of a dry run refuses and records), the branch, the commit, the push, and the
// pull request. Nothing is written: the forge refuses its writes, and git is only asked.
func (p FinishParams) plan(ctx context.Context, parked bool) {
	rep := p.Reporter
	subject := parkSubject(ParkParams{State: p.State, Input: p.Input, Subject: p.subject()})
	number, title := 0, subject
	if p.State.Issue != nil {
		number, title = p.State.Issue.Number, p.State.Issue.Title
	} else if NeedsIssue(p.Input.Kind, p.Mode) {
		// The forge of a dry run refuses it, and records it as planned.
		_, _ = p.Forge.CreateIssue(ctx, p.Target, issuex.CreateIssueRequest{Title: subject, Body: issueBody(p.Input.Body, p.State.RunID, issuex.MaxBodyLength(p.Forge))})
	}
	branch := gitx.UniqueBranchName(ctx, p.Git, BranchName(number, title))
	rep.SideEffect(toolio.SideEffect{Kind: EffectBranch, Target: branch, State: toolio.StatePlanned})
	rep.SideEffect(toolio.SideEffect{Kind: EffectCommit, Target: branch, State: toolio.StatePlanned})
	if p.Mode == LandNone {
		return
	}
	rep.SideEffect(toolio.SideEffect{Kind: EffectPush, Target: "origin/" + branch, State: toolio.StatePlanned})
	if p.Mode != LandPR {
		return
	}
	// The forge of a dry run refuses it, and records it as planned.
	title, draft := "wip: "+subject, true
	if !parked {
		report := *p.Ended.Report
		typ := report.Type
		if typ == "" {
			typ = DefaultType
		}
		title, draft = typ+": "+singleLine(report.Subject), p.State.NoVerify || len(unmetCriteria(report)) > 0
	}
	_, _ = p.Forge.CreatePullRequest(ctx, p.Target, issuex.CreatePullRequestRequest{Title: title, Head: branch, Base: p.State.Base, Draft: draft})
}
