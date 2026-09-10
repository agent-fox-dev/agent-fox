package issuex_test

import (
	"context"
	"testing"

	"github.com/agent-fox-dev/agentfox/issuex"
)

// TestClientInterfaceDeclaration verifies TS-01-1: Client interface and
// ForgeClient type alias declare all repository, issue, and pull request methods.
// Verifies: 01-REQ-1.1, 01-REQ-1.2, 01-REQ-1.3
func TestClientInterfaceDeclaration_TS_01_1(t *testing.T) {
	// Client and ForgeClient are mutually assignable type aliases.
	var _ issuex.Client = (issuex.ForgeClient)(nil)
	var _ issuex.ForgeClient = (issuex.Client)(nil)

	// Verify all method signatures on issuex.Client.
	type methodChecker interface {
		Authenticated() bool
		Close() error
		GetRepository(ctx context.Context, repo issuex.Repo) (issuex.Repository, error)
		CreateIssue(ctx context.Context, repo issuex.Repo, req issuex.CreateIssueRequest) (issuex.Issue, error)
		ReadIssue(ctx context.Context, ref issuex.IssueRef) (issuex.IssueThread, error)
		UpdateIssue(ctx context.Context, ref issuex.IssueRef, req issuex.UpdateIssueRequest) (issuex.Issue, error)
		CloseIssue(ctx context.Context, ref issuex.IssueRef, comment string) error
		ListIssues(ctx context.Context, repo issuex.Repo, filter issuex.IssueFilter) (issuex.IssueList, error)
		AddComment(ctx context.Context, ref issuex.IssueRef, body string) (string, error)
		ListComments(ctx context.Context, ref issuex.IssueRef) (issuex.CommentList, error)
		AddLabels(ctx context.Context, ref issuex.IssueRef, labels []string) error
		RemoveLabel(ctx context.Context, ref issuex.IssueRef, label string) error
		CreateLabel(ctx context.Context, repo issuex.Repo, label issuex.Label) error
		CreatePullRequest(ctx context.Context, repo issuex.Repo, req issuex.CreatePullRequestRequest) (issuex.PullRequest, error)
		ReadPullRequest(ctx context.Context, ref issuex.IssueRef) (issuex.PullRequest, error)
		ReadChangedFiles(ctx context.Context, ref issuex.IssueRef) ([]issuex.ChangedFile, error)
		GetPRState(ctx context.Context, ref issuex.IssueRef) (issuex.PRState, error)
		GetCIChecks(ctx context.Context, ref issuex.IssueRef) ([]issuex.CheckRun, error)
		GetPRReviews(ctx context.Context, ref issuex.IssueRef) ([]issuex.Review, error)
		PostReviewComment(ctx context.Context, ref issuex.IssueRef, body string) error
		MergePullRequest(ctx context.Context, ref issuex.IssueRef, opts issuex.MergeOptions) (issuex.MergeResult, error)
		ClosePullRequest(ctx context.Context, ref issuex.IssueRef) error
	}

	var _ methodChecker = (issuex.Client)(nil)
}
