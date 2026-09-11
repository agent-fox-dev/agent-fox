package issuex

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type ghPullRequest struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
	Draft   bool   `json:"draft"`
	Merged  bool   `json:"merged"`
	User    User   `json:"user"`
	Head    struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"base"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
	MergedAt  *time.Time `json:"merged_at,omitempty"`
}

func (g ghPullRequest) toPullRequest() PullRequest {
	return PullRequest{
		Number:     g.Number,
		Title:      g.Title,
		Body:       g.Body,
		State:      g.State,
		URL:        g.HTMLURL,
		HTMLURL:    g.HTMLURL,
		Draft:      g.Draft,
		Merged:     g.Merged,
		HeadSHA:    g.Head.SHA,
		HeadBranch: g.Head.Ref,
		BaseBranch: g.Base.Ref,
		Author:     g.User,
		CreatedAt:  g.CreatedAt,
		UpdatedAt:  g.UpdatedAt,
		ClosedAt:   g.ClosedAt,
		MergedAt:   g.MergedAt,
	}
}

type ghChangedFile struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

func normalizeFileStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "removed", "deleted":
		return "deleted"
	case "added":
		return "added"
	case "modified":
		return "modified"
	default:
		return "modified"
	}
}

type ghCheckRunsResponse struct {
	TotalCount int `json:"total_count"`
	CheckRuns  []struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		HTMLURL    string `json:"html_url"`
		Output     struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
		} `json:"output"`
	} `json:"check_runs"`
}

type ghReview struct {
	User        User      `json:"user"`
	State       string    `json:"state"`
	Body        string    `json:"body"`
	SubmittedAt time.Time `json:"submitted_at"`
}

type ghMergeResponse struct {
	SHA     string `json:"sha"`
	Merged  bool   `json:"merged"`
	Message string `json:"message"`
}

// CreatePullRequest opens a pull request on GitHub.
func (c *githubClient) CreatePullRequest(ctx context.Context, repo Repo, req CreatePullRequestRequest) (PullRequest, error) {
	if !c.Authenticated() {
		return PullRequest{}, ErrNoToken
	}
	if !repo.Valid() && c.repo.Valid() {
		repo = c.repo
	}
	payload := map[string]any{
		"title": req.Title,
		"body":  req.Body,
		"head":  req.Head,
		"base":  req.Base,
		"draft": req.Draft,
	}
	var raw ghPullRequest
	path := fmt.Sprintf("/repos/%s/%s/pulls", repo.Owner, repo.Name)
	if err := c.do(ctx, http.MethodPost, path, payload, &raw); err != nil {
		return PullRequest{}, err
	}
	out := raw.toPullRequest()
	out.Merged = false
	return out, nil
}

// ReadPullRequest retrieves pull request metadata.
func (c *githubClient) ReadPullRequest(ctx context.Context, ref IssueRef) (PullRequest, error) {
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}
	var raw ghPullRequest
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", ref.Repo.Owner, ref.Repo.Name, ref.Number)
	if err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return PullRequest{}, err
	}
	return raw.toPullRequest(), nil
}

// ReadChangedFiles lists files modified by a pull request.
func (c *githubClient) ReadChangedFiles(ctx context.Context, ref IssueRef) ([]ChangedFile, error) {
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}
	var rawFiles []ghChangedFile
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/files?per_page=100", ref.Repo.Owner, ref.Repo.Name, ref.Number)
	if err := c.do(ctx, http.MethodGet, path, nil, &rawFiles); err != nil {
		return nil, err
	}
	files := make([]ChangedFile, len(rawFiles))
	for i, f := range rawFiles {
		files[i] = ChangedFile{
			Filename:  f.Filename,
			Status:    normalizeFileStatus(f.Status),
			Additions: f.Additions,
			Deletions: f.Deletions,
		}
	}
	return files, nil
}

// GetPRState retrieves lightweight state information for a pull request.
func (c *githubClient) GetPRState(ctx context.Context, ref IssueRef) (PRState, error) {
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}
	var raw ghPullRequest
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", ref.Repo.Owner, ref.Repo.Name, ref.Number)
	if err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return PRState{}, err
	}
	state := "open"
	if raw.Merged {
		state = "merged"
	} else if strings.EqualFold(raw.State, "closed") {
		state = "closed"
	}
	return PRState{
		Number:  raw.Number,
		State:   state,
		Merged:  raw.Merged,
		HeadSHA: raw.Head.SHA,
	}, nil
}

// ClosePullRequest closes a pull request without merging.
func (c *githubClient) ClosePullRequest(ctx context.Context, ref IssueRef) error {
	if !c.Authenticated() {
		return ErrNoToken
	}
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", ref.Repo.Owner, ref.Repo.Name, ref.Number)
	return c.do(ctx, http.MethodPatch, path, map[string]any{"state": "closed"}, nil)
}

// GetCIChecks retrieves check runs for the pull request's head commit.
func (c *githubClient) GetCIChecks(ctx context.Context, ref IssueRef) ([]CheckRun, error) {
	pr, err := c.ReadPullRequest(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}
	path := fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs?per_page=100", ref.Repo.Owner, ref.Repo.Name, pr.HeadSHA)
	var resp ghCheckRunsResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	checks := make([]CheckRun, len(resp.CheckRuns))
	for i, cr := range resp.CheckRuns {
		summary := cr.Output.Summary
		if summary == "" {
			summary = cr.Output.Title
		}
		checks[i] = CheckRun{
			Name:       cr.Name,
			Status:     cr.Status,
			Conclusion: cr.Conclusion,
			Summary:    summary,
			URL:        cr.HTMLURL,
		}
	}
	return checks, nil
}

// GetPRReviews retrieves review submissions for a pull request.
func (c *githubClient) GetPRReviews(ctx context.Context, ref IssueRef) ([]Review, error) {
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews?per_page=100", ref.Repo.Owner, ref.Repo.Name, ref.Number)
	var rawReviews []ghReview
	if err := c.do(ctx, http.MethodGet, path, nil, &rawReviews); err != nil {
		return nil, err
	}
	reviews := make([]Review, len(rawReviews))
	for i, r := range rawReviews {
		reviews[i] = Review{
			Author:      r.User,
			State:       strings.ToLower(strings.TrimSpace(r.State)),
			Body:        r.Body,
			SubmittedAt: r.SubmittedAt,
		}
	}
	return reviews, nil
}

// PostReviewComment submits a top-level review comment on a pull request.
func (c *githubClient) PostReviewComment(ctx context.Context, ref IssueRef, body string) error {
	if !c.Authenticated() {
		return ErrNoToken
	}
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", ref.Repo.Owner, ref.Repo.Name, ref.Number)
	payload := map[string]any{
		"event": "COMMENT",
		"body":  body,
	}
	return c.do(ctx, http.MethodPost, path, payload, nil)
}

// MergePullRequest executes a merge of a pull request using explicit or repository default strategies.
func (c *githubClient) MergePullRequest(ctx context.Context, ref IssueRef, opts MergeOptions) (MergeResult, error) {
	if !c.Authenticated() {
		return MergeResult{}, ErrNoToken
	}
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}

	var method string
	switch opts.Method {
	case MergeMethodMerge:
		method = "merge"
	case MergeMethodSquash:
		method = "squash"
	case MergeMethodRebase:
		method = "rebase"
	case MergeMethodDefault:
		repo, err := c.GetRepository(ctx, ref.Repo)
		if err != nil {
			return MergeResult{}, err
		}
		if repo.AllowMergeCommit {
			method = "merge"
		} else if repo.AllowSquashMerge {
			method = "squash"
		} else if repo.AllowRebaseMerge {
			method = "rebase"
		}
	default:
		method = string(opts.Method)
	}

	payload := make(map[string]any)
	if method != "" {
		payload["merge_method"] = method
	}
	if opts.CommitTitle != "" {
		payload["commit_title"] = opts.CommitTitle
	}
	if opts.CommitMessage != "" {
		payload["commit_message"] = opts.CommitMessage
	}
	if opts.SHA != "" {
		payload["sha"] = opts.SHA
	}

	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/merge", ref.Repo.Owner, ref.Repo.Name, ref.Number)
	var raw ghMergeResponse
	if err := c.do(ctx, http.MethodPut, path, payload, &raw); err != nil {
		return MergeResult{}, err
	}
	return MergeResult{
		Merged:  raw.Merged,
		SHA:     raw.SHA,
		Message: raw.Message,
	}, nil
}
