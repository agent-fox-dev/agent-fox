package hub

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/agent-fox-dev/aflib/issuex"
)

// Repo is the forge repository of the workspace, from the URL its record gives, or false when
// the URL is none that a forge has.
func (r Record) Repo() (issuex.Repo, bool) { return issuex.ParseRemote(r.ForgeURL) }

// ForgeClient is an issuex.Client for the one repository of a workspace, which reads and
// writes the forge through hub's forge API under /api/v1/workspaces/{slug}, with the PAT of the
// subscription and no forge token of its own. The loops of af call it as they call a forge
// directly, and the errors it returns are the types they already tell apart.
//
// It supports what triage and fix need: Authenticated, GetRepository, ReadIssue, ListComments,
// UpdateIssue, CreateIssue, AddComment, AddLabels, RemoveLabel and CreatePullRequest. Every
// other method sends nothing and says that the operation is not available through hub in this
// release.
type ForgeClient struct {
	hub  *Client
	pat  string
	slug string
	repo issuex.Repo
}

var (
	_ issuex.Client      = (*ForgeClient)(nil)
	_ issuex.BodyLimiter = (*ForgeClient)(nil)
)

// Forge returns the forge client of the workspace, whose forge repository is repo.
func (c *Client) Forge(pat, slug string, repo issuex.Repo) *ForgeClient {
	return &ForgeClient{hub: c, pat: pat, slug: slug, repo: repo}
}

// ForgeFactory returns a function with the signature of toolio.App.NewForge that gives the
// forge client of the workspace. It refuses, before any request, a repository in the options
// that is not the workspace's.
func (c *Client) ForgeFactory(pat, slug string, repo issuex.Repo) func(issuex.Options) (issuex.Client, error) {
	return func(o issuex.Options) (issuex.Client, error) {
		if o.Repo.Valid() {
			if err := checkRepo(repo, o.Repo); err != nil {
				return nil, err
			}
		}
		return c.Forge(pat, slug, repo), nil
	}
}

// checkRepo is the error for a repository other than the workspace's: host, owner and name are
// compared without case, and a host that is empty is the workspace's.
func checkRepo(workspace, got issuex.Repo) error {
	if strings.EqualFold(workspace.Owner, got.Owner) && strings.EqualFold(workspace.Name, got.Name) &&
		(got.Host == "" || strings.EqualFold(workspace.Host, got.Host)) {
		return nil
	}
	return fmt.Errorf("the issue is not on the repository of the workspace: %s is not %s", repoName(got), repoName(workspace))
}

// repoName is a repository as it is shown, with its host when it has one.
func repoName(r issuex.Repo) string {
	if r.Host == "" {
		return r.String()
	}
	return r.Host + "/" + r.String()
}

// Authenticated is true: hub holds the credential, and the PAT was checked when it was given.
func (f *ForgeClient) Authenticated() bool { return true }

// Close does nothing: the hub client keeps no connection of its own.
func (f *ForgeClient) Close() error { return nil }

// MaxBodyLength is the body limit of the forge: GitLab's when the host of the repository is
// GitLab's, and GitHub's otherwise.
func (f *ForgeClient) MaxBodyLength() int {
	if strings.Contains(strings.ToLower(f.repo.Host), "gitlab") {
		return issuex.GitLabMaxBodyLength
	}
	return issuex.GitHubMaxBodyLength
}

// notAvailable is the error of an operation that hub's forge API does not carry here.
func notAvailable(name string) error {
	return fmt.Errorf("%s is not available through hub in this release", name)
}

// GetRepository reads the record of the workspace, which is what the loops ask of the
// repository: that it is there, that it is not archived and which branch is its default.
func (f *ForgeClient) GetRepository(ctx context.Context, repo issuex.Repo) (issuex.Repository, error) {
	if err := checkRepo(f.repo, repo); err != nil {
		return issuex.Repository{}, err
	}
	rec, err := f.hub.WorkspaceRecord(ctx, f.pat, f.slug)
	if err != nil {
		return issuex.Repository{}, f.forgeError(err)
	}
	return issuex.Repository{
		FullName: f.repo.String(), DefaultBranch: rec.Branch,
		Permissions: issuex.RepoPermissions{Push: true, Pull: true},
	}, nil
}

// issuePath is the path of an issue under the workspace.
func (f *ForgeClient) issuePath(ref issuex.IssueRef, rest ...string) string {
	return workspacePath(f.slug, append([]string{"/issues/", strconv.Itoa(ref.Number)}, rest...)...)
}

// check refuses an issue that is not on the repository of the workspace.
func (f *ForgeClient) check(ref issuex.IssueRef) error { return checkRepo(f.repo, ref.Repo) }

// ReadIssue reads the issue with its comments, and marks it a pull or merge request when hub
// says it is one, so that the loops' refusal of one applies.
func (f *ForgeClient) ReadIssue(ctx context.Context, ref issuex.IssueRef) (issuex.IssueThread, error) {
	if err := f.check(ref); err != nil {
		return issuex.IssueThread{}, err
	}
	var thread struct {
		Issue     issuex.Issue     `json:"issue"`
		Comments  []issuex.Comment `json:"comments"`
		Truncated bool             `json:"truncated"`
	}
	err := f.hub.send(ctx, f.pat, operation{name: "read the issue", slug: f.slug, method: http.MethodGet, path: f.issuePath(ref), into: &thread})
	if err != nil {
		return issuex.IssueThread{}, f.forgeError(err)
	}
	return issuex.IssueThread{Issue: thread.Issue, Comments: thread.Comments, Truncated: thread.Truncated}, nil
}

// ListComments lists the comments of the issue.
func (f *ForgeClient) ListComments(ctx context.Context, ref issuex.IssueRef) (issuex.CommentList, error) {
	if err := f.check(ref); err != nil {
		return issuex.CommentList{}, err
	}
	var comments []issuex.Comment
	err := f.hub.send(ctx, f.pat, operation{name: "list the comments", slug: f.slug, method: http.MethodGet, path: f.issuePath(ref, "/comments"), into: &comments})
	if err != nil {
		return issuex.CommentList{}, f.forgeError(err)
	}
	return issuex.CommentList{Comments: comments}, nil
}

// UpdateIssue changes the title and the body of the issue, and the state and labels when the
// request has them; what it leaves empty is left as it is.
func (f *ForgeClient) UpdateIssue(ctx context.Context, ref issuex.IssueRef, req issuex.UpdateIssueRequest) (issuex.Issue, error) {
	if err := f.check(ref); err != nil {
		return issuex.Issue{}, err
	}
	body := map[string]any{}
	if req.Title != "" {
		body["title"] = req.Title
	}
	if req.Body != "" {
		body["body"] = req.Body
	}
	if req.State != "" {
		body["state"] = req.State
	}
	if len(req.Labels) > 0 {
		body["labels"] = req.Labels
	}
	if len(req.Assignees) > 0 {
		body["assignees"] = req.Assignees
	}
	var issue issuex.Issue
	err := f.hub.send(ctx, f.pat, operation{name: "update the issue", slug: f.slug, method: http.MethodPatch, path: f.issuePath(ref), body: body, into: &issue})
	if err != nil {
		return issuex.Issue{}, f.forgeError(err)
	}
	return issue, nil
}

// CreateIssue creates an issue.
func (f *ForgeClient) CreateIssue(ctx context.Context, repo issuex.Repo, req issuex.CreateIssueRequest) (issuex.Issue, error) {
	if err := checkRepo(f.repo, repo); err != nil {
		return issuex.Issue{}, err
	}
	body := map[string]any{"title": req.Title}
	if req.Body != "" {
		body["body"] = req.Body
	}
	if len(req.Labels) > 0 {
		body["labels"] = req.Labels
	}
	if len(req.Assignees) > 0 {
		body["assignees"] = req.Assignees
	}
	var issue issuex.Issue
	err := f.hub.send(ctx, f.pat, operation{name: "create the issue", slug: f.slug, method: http.MethodPost, path: workspacePath(f.slug, "/issues"), body: body, into: &issue})
	if err != nil {
		return issuex.Issue{}, f.forgeError(err)
	}
	return issue, nil
}

// AddComment adds a comment to the issue and returns the URL of the comment.
func (f *ForgeClient) AddComment(ctx context.Context, ref issuex.IssueRef, body string) (string, error) {
	if err := f.check(ref); err != nil {
		return "", err
	}
	var created struct {
		URL string `json:"url"`
	}
	err := f.hub.send(ctx, f.pat, operation{name: "add a comment", slug: f.slug, method: http.MethodPost, path: f.issuePath(ref, "/comments"), body: map[string]string{"body": body}, into: &created})
	if err != nil {
		return "", f.forgeError(err)
	}
	return created.URL, nil
}

// AddLabels adds labels to the issue.
func (f *ForgeClient) AddLabels(ctx context.Context, ref issuex.IssueRef, labels []string) error {
	if err := f.check(ref); err != nil {
		return err
	}
	names := labels
	if names == nil {
		names = []string{}
	}
	return f.forgeError(f.hub.send(ctx, f.pat, operation{name: "add labels", slug: f.slug, method: http.MethodPost, path: f.issuePath(ref, "/labels"), body: names}))
}

// RemoveLabel removes a label from the issue.
func (f *ForgeClient) RemoveLabel(ctx context.Context, ref issuex.IssueRef, label string) error {
	if err := f.check(ref); err != nil {
		return err
	}
	return f.forgeError(f.hub.send(ctx, f.pat, operation{name: "remove a label", slug: f.slug, method: http.MethodDelete, path: f.issuePath(ref, "/labels/", url.PathEscape(label))}))
}

// CreatePullRequest opens a pull request: POST …/pulls {title, body, head, base, draft}.
func (f *ForgeClient) CreatePullRequest(ctx context.Context, repo issuex.Repo, req issuex.CreatePullRequestRequest) (issuex.PullRequest, error) {
	if err := checkRepo(f.repo, repo); err != nil {
		return issuex.PullRequest{}, err
	}
	var pull issuex.PullRequest
	err := f.hub.send(ctx, f.pat, operation{
		name: "open a pull request", slug: f.slug, method: http.MethodPost, path: workspacePath(f.slug, "/pulls"), into: &pull,
		body: map[string]any{"title": req.Title, "body": req.Body, "head": req.Head, "base": req.Base, "draft": req.Draft},
	})
	if err != nil {
		return issuex.PullRequest{}, f.forgeError(err)
	}
	return pull, nil
}

// forgeError turns an error of a call of hub into the error the loops tell apart: a refusal
// or a forbidding is an *issuex.HTTPError with status 401 or 403 that carries the message of
// the call; not found is issuex's not-found error; 409 is its conflict; a rate limit is
// its rate-limited error; another refusal of hub is an *issuex.HTTPError with its status;
// and hub being unavailable is an error that is not one of those, which they may try again, as
// is an error of the context.
func (f *ForgeClient) forgeError(err error) error {
	if err == nil {
		return nil
	}
	var rejected *RejectedError
	var unavailable *UnavailableError
	switch {
	case errors.Is(err, ErrRefused):
		return &issuex.HTTPError{Status: http.StatusUnauthorized, Message: err.Error()}
	case errors.Is(err, ErrForbidden):
		return &issuex.HTTPError{Status: http.StatusForbidden, Message: err.Error()}
	case errors.Is(err, ErrNotFound):
		return fmt.Errorf("%v: %w", err, issuex.ErrNotFound)
	case errors.As(err, &rejected) && rejected.Status == http.StatusConflict:
		return fmt.Errorf("%v: %w", err, issuex.ErrConflict)
	case errors.As(err, &rejected):
		return &issuex.HTTPError{Status: rejected.Status, Message: rejected.Message}
	case errors.As(err, &unavailable) && unavailable.Status == http.StatusTooManyRequests:
		return fmt.Errorf("%v: %w", err, issuex.ErrRateLimited)
	}
	return err
}

// The operations that hub's forge API does not carry here.

func (f *ForgeClient) ListIssues(context.Context, issuex.Repo, issuex.IssueFilter) (issuex.IssueList, error) {
	return issuex.IssueList{}, notAvailable("ListIssues")
}

func (f *ForgeClient) CloseIssue(context.Context, issuex.IssueRef, string) error {
	return notAvailable("CloseIssue")
}

func (f *ForgeClient) CreateLabel(context.Context, issuex.Repo, issuex.Label) error {
	return notAvailable("CreateLabel")
}

func (f *ForgeClient) ReadPullRequest(context.Context, issuex.IssueRef) (issuex.PullRequest, error) {
	return issuex.PullRequest{}, notAvailable("ReadPullRequest")
}

func (f *ForgeClient) ReadChangedFiles(context.Context, issuex.IssueRef) ([]issuex.ChangedFile, error) {
	return nil, notAvailable("ReadChangedFiles")
}

func (f *ForgeClient) GetPRState(context.Context, issuex.IssueRef) (issuex.PRState, error) {
	return issuex.PRState{}, notAvailable("GetPRState")
}

func (f *ForgeClient) GetCIChecks(context.Context, issuex.IssueRef) ([]issuex.CheckRun, error) {
	return nil, notAvailable("GetCIChecks")
}

func (f *ForgeClient) GetPRReviews(context.Context, issuex.IssueRef) ([]issuex.Review, error) {
	return nil, notAvailable("GetPRReviews")
}

func (f *ForgeClient) PostReviewComment(context.Context, issuex.IssueRef, string) error {
	return notAvailable("PostReviewComment")
}

func (f *ForgeClient) MergePullRequest(context.Context, issuex.IssueRef, issuex.MergeOptions) (issuex.MergeResult, error) {
	return issuex.MergeResult{}, notAvailable("MergePullRequest")
}

func (f *ForgeClient) ClosePullRequest(context.Context, issuex.IssueRef) error {
	return notAvailable("ClosePullRequest")
}
