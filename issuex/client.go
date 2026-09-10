package issuex

import "context"

// Client is the forge-neutral client interface declaring unified repository,
// issue, and pull request operations across Git forges like GitHub and GitLab.
type Client interface {
	// Lifecycle methods
	Authenticated() bool
	Close() error

	// Repository operations
	GetRepository(ctx context.Context, repo Repo) (Repository, error)

	// Issue operations
	CreateIssue(ctx context.Context, repo Repo, req CreateIssueRequest) (Issue, error)
	ReadIssue(ctx context.Context, ref IssueRef) (IssueThread, error)
	UpdateIssue(ctx context.Context, ref IssueRef, req UpdateIssueRequest) (Issue, error)
	CloseIssue(ctx context.Context, ref IssueRef, comment string) error
	ListIssues(ctx context.Context, repo Repo, filter IssueFilter) (IssueList, error)
	AddComment(ctx context.Context, ref IssueRef, body string) (string, error)
	ListComments(ctx context.Context, ref IssueRef) (CommentList, error)
	AddLabels(ctx context.Context, ref IssueRef, labels []string) error
	RemoveLabel(ctx context.Context, ref IssueRef, label string) error
	CreateLabel(ctx context.Context, repo Repo, label Label) error

	// Pull request operations
	CreatePullRequest(ctx context.Context, repo Repo, req CreatePullRequestRequest) (PullRequest, error)
	ReadPullRequest(ctx context.Context, ref IssueRef) (PullRequest, error)
	ReadChangedFiles(ctx context.Context, ref IssueRef) ([]ChangedFile, error)
	GetPRState(ctx context.Context, ref IssueRef) (PRState, error)
	GetCIChecks(ctx context.Context, ref IssueRef) ([]CheckRun, error)
	GetPRReviews(ctx context.Context, ref IssueRef) ([]Review, error)
	PostReviewComment(ctx context.Context, ref IssueRef, body string) error
	MergePullRequest(ctx context.Context, ref IssueRef, opts MergeOptions) (MergeResult, error)
	ClosePullRequest(ctx context.Context, ref IssueRef) error
}

// ForgeClient is an alias for Client.
type ForgeClient = Client
