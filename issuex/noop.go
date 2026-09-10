package issuex

import (
	"context"
	"errors"
)

var errNoOpPR = errors.New("pull request operations require an active forge client")

// Ensure NoOpClient implements Client at compile time.
var _ Client = (*NoOpClient)(nil)

// NoOpClient is an offline, forge-disabled client implementation.
// It returns zero values for issue reads and repository info, performs silent
// no-ops for issue writes, and returns an error for pull request operations.
type NoOpClient struct{}

// NewNoOp creates a new NoOpClient satisfying the Client interface.
func NewNoOp() Client {
	return &NoOpClient{}
}

// Authenticated returns false for NoOpClient.
func (c *NoOpClient) Authenticated() bool {
	return false
}

// Close returns nil for NoOpClient.
func (c *NoOpClient) Close() error {
	return nil
}

// GetRepository returns a zero-value Repository and nil error.
func (c *NoOpClient) GetRepository(ctx context.Context, repo Repo) (Repository, error) {
	return Repository{}, nil
}

// CreateIssue performs no remote operation and returns a zero-value Issue and nil.
func (c *NoOpClient) CreateIssue(ctx context.Context, repo Repo, req CreateIssueRequest) (Issue, error) {
	return Issue{}, nil
}

// ReadIssue returns a zero-value IssueThread and nil.
func (c *NoOpClient) ReadIssue(ctx context.Context, ref IssueRef) (IssueThread, error) {
	return IssueThread{}, nil
}

// UpdateIssue performs no remote operation and returns a zero-value Issue and nil.
func (c *NoOpClient) UpdateIssue(ctx context.Context, ref IssueRef, req UpdateIssueRequest) (Issue, error) {
	return Issue{}, nil
}

// CloseIssue performs no remote operation and returns nil.
func (c *NoOpClient) CloseIssue(ctx context.Context, ref IssueRef, comment string) error {
	return nil
}

// ListIssues returns a zero-value IssueList and nil.
func (c *NoOpClient) ListIssues(ctx context.Context, repo Repo, filter IssueFilter) (IssueList, error) {
	return IssueList{}, nil
}

// AddComment performs no remote operation and returns an empty ID and nil.
func (c *NoOpClient) AddComment(ctx context.Context, ref IssueRef, body string) (string, error) {
	return "", nil
}

// ListComments returns a zero-value CommentList and nil.
func (c *NoOpClient) ListComments(ctx context.Context, ref IssueRef) (CommentList, error) {
	return CommentList{}, nil
}

// AddLabels performs no remote operation and returns nil.
func (c *NoOpClient) AddLabels(ctx context.Context, ref IssueRef, labels []string) error {
	return nil
}

// RemoveLabel performs no remote operation and returns nil.
func (c *NoOpClient) RemoveLabel(ctx context.Context, ref IssueRef, label string) error {
	return nil
}

// CreateLabel performs no remote operation and returns nil.
func (c *NoOpClient) CreateLabel(ctx context.Context, repo Repo, label Label) error {
	return nil
}

// CreatePullRequest returns an error indicating that PR operations require an active forge client.
func (c *NoOpClient) CreatePullRequest(ctx context.Context, repo Repo, req CreatePullRequestRequest) (PullRequest, error) {
	return PullRequest{}, errNoOpPR
}

// ReadPullRequest returns an error indicating that PR operations require an active forge client.
func (c *NoOpClient) ReadPullRequest(ctx context.Context, ref IssueRef) (PullRequest, error) {
	return PullRequest{}, errNoOpPR
}

// ReadChangedFiles returns an error indicating that PR operations require an active forge client.
func (c *NoOpClient) ReadChangedFiles(ctx context.Context, ref IssueRef) ([]ChangedFile, error) {
	return nil, errNoOpPR
}

// GetPRState returns an error indicating that PR operations require an active forge client.
func (c *NoOpClient) GetPRState(ctx context.Context, ref IssueRef) (PRState, error) {
	return PRState{}, errNoOpPR
}

// GetCIChecks returns an error indicating that PR operations require an active forge client.
func (c *NoOpClient) GetCIChecks(ctx context.Context, ref IssueRef) ([]CheckRun, error) {
	return nil, errNoOpPR
}

// GetPRReviews returns an error indicating that PR operations require an active forge client.
func (c *NoOpClient) GetPRReviews(ctx context.Context, ref IssueRef) ([]Review, error) {
	return nil, errNoOpPR
}

// PostReviewComment returns an error indicating that PR operations require an active forge client.
func (c *NoOpClient) PostReviewComment(ctx context.Context, ref IssueRef, body string) error {
	return errNoOpPR
}

// MergePullRequest returns an error indicating that PR operations require an active forge client.
func (c *NoOpClient) MergePullRequest(ctx context.Context, ref IssueRef, opts MergeOptions) (MergeResult, error) {
	return MergeResult{}, errNoOpPR
}

// ClosePullRequest returns an error indicating that PR operations require an active forge client.
func (c *NoOpClient) ClosePullRequest(ctx context.Context, ref IssueRef) error {
	return errNoOpPR
}
