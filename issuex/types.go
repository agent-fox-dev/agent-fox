package issuex

import (
	"fmt"
	"strings"
	"time"
)

// Repo names a Git repository across GitHub or GitLab.
// For GitLab, Owner may contain nested group segments (e.g. "group/subgroup").
type Repo struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
	Host  string `json:"host,omitempty"`
}

// String returns "Owner/Name".
func (r Repo) String() string {
	return fmt.Sprintf("%s/%s", r.Owner, r.Name)
}

// Valid reports whether both Owner and Name are non-empty.
func (r Repo) Valid() bool {
	return r.Owner != "" && r.Name != ""
}

// IssueRef identifies an issue or pull request in a repository by number.
type IssueRef struct {
	Repo          Repo `json:"repo"`
	Number        int  `json:"number"`
	IsPullRequest bool `json:"is_pull_request"`
}

// String returns "owner/repo#number".
func (r IssueRef) String() string {
	return fmt.Sprintf("%s#%d", r.Repo.String(), r.Number)
}

// URL returns the canonical web URL for the issue or pull request.
func (r IssueRef) URL() string {
	host := strings.TrimSuffix(strings.TrimSpace(r.Repo.Host), "/")
	if host == "" {
		host = "github.com"
	}
	scheme := "https"
	if strings.HasPrefix(host, "http://") {
		scheme = "http"
		host = strings.TrimPrefix(host, "http://")
	} else if strings.HasPrefix(host, "https://") {
		host = strings.TrimPrefix(host, "https://")
	}

	isGitLab := strings.Contains(strings.ToLower(host), "gitlab")
	if isGitLab {
		kind := "issues"
		if r.IsPullRequest {
			kind = "merge_requests"
		}
		return fmt.Sprintf("%s://%s/%s/-/%s/%d", scheme, host, r.Repo.String(), kind, r.Number)
	}

	kind := "issues"
	if r.IsPullRequest {
		kind = "pull"
	}
	return fmt.Sprintf("%s://%s/%s/%s/%d", scheme, host, r.Repo.String(), kind, r.Number)
}

// User represents an account on the forge.
type User struct {
	Login string `json:"login"`
}

// Comment represents an issue comment.
type Comment struct {
	ID        int64     `json:"id,omitempty"`
	Body      string    `json:"body"`
	User      User      `json:"user"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	URL       string    `json:"url,omitempty"`
	HTMLURL   string    `json:"html_url,omitempty"`
}

// Issue represents an issue or pull request metadata.
type Issue struct {
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	State     string     `json:"state"`
	URL       string     `json:"url,omitempty"`
	HTMLURL   string     `json:"html_url,omitempty"`
	Author    User       `json:"author"`
	Labels    []string   `json:"labels,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
	IsPR      bool       `json:"is_pr"`
}

// IssueThread represents an issue and its comments.
type IssueThread struct {
	Issue       Issue     `json:"issue"`
	Comments    []Comment `json:"comments"`
	Truncated   bool      `json:"truncated"`
	CommentsErr error     `json:"comments_err,omitempty"`
}

// PullRequest represents a pull request or merge request.
type PullRequest struct {
	Number     int        `json:"number"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	State      string     `json:"state"`
	URL        string     `json:"url,omitempty"`
	HTMLURL    string     `json:"html_url,omitempty"`
	Draft      bool       `json:"draft"`
	Merged     bool       `json:"merged"`
	HeadSHA    string     `json:"head_sha"`
	HeadBranch string     `json:"head_branch"`
	BaseBranch string     `json:"base_branch"`
	Author     User       `json:"author"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ClosedAt   *time.Time `json:"closed_at,omitempty"`
	MergedAt   *time.Time `json:"merged_at,omitempty"`
}

// ChangedFile represents a file modified in a pull request.
type ChangedFile struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// PRState represents lightweight pull request state.
type PRState struct {
	Number  int    `json:"number"`
	State   string `json:"state"`
	Merged  bool   `json:"merged"`
	HeadSHA string `json:"head_sha"`
}

// CheckRun represents a CI check run or pipeline job.
type CheckRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Summary    string `json:"summary,omitempty"`
	URL        string `json:"url,omitempty"`
}

// Review represents a pull request review.
type Review struct {
	Author      User      `json:"author"`
	State       string    `json:"state"`
	Body        string    `json:"body"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// Label represents an issue label.
type Label struct {
	Name        string `json:"name"`
	Color       string `json:"color,omitempty"`
	Description string `json:"description,omitempty"`
}

// RepoPermissions represents repository permissions for the authenticated user.
type RepoPermissions struct {
	Push  bool `json:"push"`
	Pull  bool `json:"pull"`
	Admin bool `json:"admin"`
}

// Repository represents repository metadata.
type Repository struct {
	FullName         string          `json:"full_name"`
	DefaultBranch    string          `json:"default_branch"`
	Private          bool            `json:"private"`
	Archived         bool            `json:"archived"`
	Permissions      RepoPermissions `json:"permissions"`
	AllowMergeCommit bool            `json:"allow_merge_commit,omitempty"`
	AllowSquashMerge bool            `json:"allow_squash_merge,omitempty"`
	AllowRebaseMerge bool            `json:"allow_rebase_merge,omitempty"`
}

// IssueFilter configures issue list filtering.
type IssueFilter struct {
	State     string   `json:"state,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	Assignee  string   `json:"assignee,omitempty"`
	Sort      string   `json:"sort,omitempty"`
	Direction string   `json:"direction,omitempty"`
	Limit     int      `json:"limit,omitempty"`
}

// IssueList contains issues returned from a query.
type IssueList struct {
	Issues     []Issue `json:"issues"`
	Incomplete bool    `json:"incomplete"`
}

// CommentList contains comments returned from an issue.
type CommentList struct {
	Comments  []Comment `json:"comments"`
	Truncated bool      `json:"truncated"`
}

// CreateIssueRequest specifies parameters for creating an issue.
type CreateIssueRequest struct {
	Title     string   `json:"title"`
	Body      string   `json:"body,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	Assignees []string `json:"assignees,omitempty"`
}

// UpdateIssueRequest specifies parameters for updating an issue.
type UpdateIssueRequest struct {
	Title     string   `json:"title,omitempty"`
	Body      string   `json:"body,omitempty"`
	State     string   `json:"state,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	Assignees []string `json:"assignees,omitempty"`
}

// CreatePullRequestRequest specifies parameters for creating a pull request.
type CreatePullRequestRequest struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	Head  string `json:"head"`
	Base  string `json:"base"`
	Draft bool   `json:"draft,omitempty"`
}

// MergeMethod defines the strategy used when merging a pull request.
type MergeMethod string

const (
	MergeMethodDefault MergeMethod = ""
	MergeMethodMerge   MergeMethod = "merge"
	MergeMethodSquash  MergeMethod = "squash"
	MergeMethodRebase  MergeMethod = "rebase"
)

// MergeOptions specifies parameters for merging a pull request.
type MergeOptions struct {
	Method        MergeMethod `json:"method,omitempty"`
	CommitTitle   string      `json:"commit_title,omitempty"`
	CommitMessage string      `json:"commit_message,omitempty"`
	SHA           string      `json:"sha,omitempty"`
}

// MergeResult contains the result of a pull request merge.
type MergeResult struct {
	Merged  bool   `json:"merged"`
	SHA     string `json:"sha,omitempty"`
	Message string `json:"message,omitempty"`
}
