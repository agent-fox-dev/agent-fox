package toolio

import (
	"context"
	"errors"
	"fmt"

	"github.com/agent-fox-dev/aflib/issuex"
)

// ErrDryRun is wrapped by the error of every forge write a dry run refuses.
var ErrDryRun = errors.New("dry run: the forge was not written to")

// dryRunForge is a forge client that reads and does not write. It is the one
// place every forge write passes, so a dry run cannot reach the forge
// through any command, present or future.
type dryRunForge struct {
	inner  issuex.Client
	record func(SideEffect)
}

var (
	_ issuex.Client      = (*dryRunForge)(nil)
	_ issuex.BodyLimiter = (*dryRunForge)(nil)
)

// DryRunForge returns a client that passes the reads of inner through and
// refuses every write: nothing is sent, the error wraps ErrDryRun, and the
// write is passed to record, when one is given, as a planned side effect.
func DryRunForge(inner issuex.Client, record ...func(SideEffect)) issuex.Client {
	d := &dryRunForge{inner: inner}
	if len(record) > 0 {
		d.record = record[0]
	}
	return d
}

// refuse records the write of kind to target and returns the error that
// stands for it.
func (d *dryRunForge) refuse(kind, target string) error {
	if d.record != nil {
		d.record(SideEffect{Kind: kind, Target: target, State: StatePlanned})
	}
	return fmt.Errorf("%w: %s %s", ErrDryRun, kind, target)
}

// MaxBodyLength is the body limit of the forge behind the decorator.
func (d *dryRunForge) MaxBodyLength() int { return issuex.MaxBodyLength(d.inner) }

// The reads.

func (d *dryRunForge) Authenticated() bool { return d.inner.Authenticated() }
func (d *dryRunForge) Close() error        { return d.inner.Close() }

func (d *dryRunForge) GetRepository(ctx context.Context, repo issuex.Repo) (issuex.Repository, error) {
	return d.inner.GetRepository(ctx, repo)
}

func (d *dryRunForge) ReadIssue(ctx context.Context, ref issuex.IssueRef) (issuex.IssueThread, error) {
	return d.inner.ReadIssue(ctx, ref)
}

func (d *dryRunForge) ListIssues(ctx context.Context, repo issuex.Repo, filter issuex.IssueFilter) (issuex.IssueList, error) {
	return d.inner.ListIssues(ctx, repo, filter)
}

func (d *dryRunForge) ListComments(ctx context.Context, ref issuex.IssueRef) (issuex.CommentList, error) {
	return d.inner.ListComments(ctx, ref)
}

func (d *dryRunForge) ReadPullRequest(ctx context.Context, ref issuex.IssueRef) (issuex.PullRequest, error) {
	return d.inner.ReadPullRequest(ctx, ref)
}

func (d *dryRunForge) ReadChangedFiles(ctx context.Context, ref issuex.IssueRef) ([]issuex.ChangedFile, error) {
	return d.inner.ReadChangedFiles(ctx, ref)
}

func (d *dryRunForge) GetPRState(ctx context.Context, ref issuex.IssueRef) (issuex.PRState, error) {
	return d.inner.GetPRState(ctx, ref)
}

func (d *dryRunForge) GetCIChecks(ctx context.Context, ref issuex.IssueRef) ([]issuex.CheckRun, error) {
	return d.inner.GetCIChecks(ctx, ref)
}

func (d *dryRunForge) GetPRReviews(ctx context.Context, ref issuex.IssueRef) ([]issuex.Review, error) {
	return d.inner.GetPRReviews(ctx, ref)
}

// The writes.

func (d *dryRunForge) CreateIssue(_ context.Context, repo issuex.Repo, _ issuex.CreateIssueRequest) (issuex.Issue, error) {
	return issuex.Issue{}, d.refuse("create_issue", repo.String())
}

func (d *dryRunForge) UpdateIssue(_ context.Context, ref issuex.IssueRef, _ issuex.UpdateIssueRequest) (issuex.Issue, error) {
	return issuex.Issue{}, d.refuse("update_issue", ref.String())
}

func (d *dryRunForge) CloseIssue(_ context.Context, ref issuex.IssueRef, _ string) error {
	return d.refuse("close_issue", ref.String())
}

func (d *dryRunForge) AddComment(_ context.Context, ref issuex.IssueRef, _ string) (string, error) {
	return "", d.refuse("add_comment", ref.String())
}

func (d *dryRunForge) AddLabels(_ context.Context, ref issuex.IssueRef, _ []string) error {
	return d.refuse("add_labels", ref.String())
}

func (d *dryRunForge) RemoveLabel(_ context.Context, ref issuex.IssueRef, _ string) error {
	return d.refuse("remove_label", ref.String())
}

func (d *dryRunForge) CreateLabel(_ context.Context, repo issuex.Repo, _ issuex.Label) error {
	return d.refuse("create_label", repo.String())
}

func (d *dryRunForge) CreatePullRequest(_ context.Context, repo issuex.Repo, _ issuex.CreatePullRequestRequest) (issuex.PullRequest, error) {
	return issuex.PullRequest{}, d.refuse("create_pull_request", repo.String())
}

func (d *dryRunForge) PostReviewComment(_ context.Context, ref issuex.IssueRef, _ string) error {
	return d.refuse("post_review_comment", ref.String())
}

func (d *dryRunForge) MergePullRequest(_ context.Context, ref issuex.IssueRef, _ issuex.MergeOptions) (issuex.MergeResult, error) {
	return issuex.MergeResult{}, d.refuse("merge_pull_request", ref.String())
}

func (d *dryRunForge) ClosePullRequest(_ context.Context, ref issuex.IssueRef) error {
	return d.refuse("close_pull_request", ref.String())
}
