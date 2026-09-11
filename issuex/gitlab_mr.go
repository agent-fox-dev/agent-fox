package issuex

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

type gitlabMergeRequest struct {
	ID             int    `json:"id"`
	IID            int    `json:"iid"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	State          string `json:"state"`
	Draft          bool   `json:"draft"`
	WorkInProgress bool   `json:"work_in_progress"`
	SourceBranch   string `json:"source_branch"`
	TargetBranch   string `json:"target_branch"`
	SHA            string `json:"sha"`
	WebURL         string `json:"web_url"`
	Author         struct {
		Username string `json:"username"`
	} `json:"author"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	MergedAt  *time.Time `json:"merged_at,omitempty"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
}

func (g gitlabMergeRequest) toPullRequest() PullRequest {
	st := strings.ToLower(strings.TrimSpace(g.State))
	merged := st == "merged"
	if st == "opened" {
		st = "open"
	}
	draft := g.Draft || g.WorkInProgress
	return PullRequest{
		Number:     g.IID,
		Title:      g.Title,
		Body:       g.Description,
		State:      st,
		URL:        g.WebURL,
		HTMLURL:    g.WebURL,
		Draft:      draft,
		Merged:     merged,
		HeadSHA:    g.SHA,
		HeadBranch: g.SourceBranch,
		BaseBranch: g.TargetBranch,
		Author:     User{Login: g.Author.Username},
		CreatedAt:  g.CreatedAt,
		UpdatedAt:  g.UpdatedAt,
		ClosedAt:   g.ClosedAt,
		MergedAt:   g.MergedAt,
	}
}

type gitlabChangesResponse struct {
	Changes []struct {
		OldPath     string `json:"old_path"`
		NewPath     string `json:"new_path"`
		NewFile     bool   `json:"new_file"`
		DeletedFile bool   `json:"deleted_file"`
		Diff        string `json:"diff"`
	} `json:"changes"`
}

type gitlabPipeline struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
}

type gitlabJob struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Stage         string `json:"stage"`
	Status        string `json:"status"`
	FailureReason string `json:"failure_reason"`
	WebURL        string `json:"web_url"`
}

type gitlabApprovalsResponse struct {
	ApprovedBy []struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	} `json:"approved_by"`
}

type gitlabMergeResponse struct {
	SHA            string `json:"sha"`
	MergeCommitSHA string `json:"merge_commit_sha"`
}

// CreatePullRequest opens a new merge request on GitLab.
func (c *gitlabClient) CreatePullRequest(ctx context.Context, repo Repo, req CreatePullRequestRequest) (PullRequest, error) {
	if !c.Authenticated() {
		return PullRequest{}, ErrNoToken
	}
	if !repo.Valid() && c.repo.Valid() {
		repo = c.repo
	}

	title := req.Title
	if req.Draft && !strings.HasPrefix(title, "Draft: ") {
		title = "Draft: " + title
	}

	payload := map[string]any{
		"source_branch": req.Head,
		"target_branch": req.Base,
		"title":         title,
		"description":   req.Body,
	}

	path := fmt.Sprintf("/projects/%s/merge_requests", projectPath(repo))
	var raw gitlabMergeRequest
	if _, err := c.do(ctx, http.MethodPost, path, payload, &raw); err != nil {
		return PullRequest{}, err
	}
	out := raw.toPullRequest()
	out.Merged = false
	return out, nil
}

// ReadPullRequest retrieves merge request metadata by MR iid.
func (c *gitlabClient) ReadPullRequest(ctx context.Context, ref IssueRef) (PullRequest, error) {
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}

	path := fmt.Sprintf("/projects/%s/merge_requests/%d", projectPath(ref.Repo), ref.Number)
	var raw gitlabMergeRequest
	if _, err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		if IsNotFound(err) && !c.Authenticated() {
			return PullRequest{}, fmt.Errorf("%w: repository or merge request may be private and require setting GITLAB_TOKEN: %w", ErrNotFound, err)
		}
		return PullRequest{}, err
	}
	return raw.toPullRequest(), nil
}

// ReadChangedFiles lists files modified by a merge request and computes additions/deletions.
func (c *gitlabClient) ReadChangedFiles(ctx context.Context, ref IssueRef) ([]ChangedFile, error) {
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}

	path := fmt.Sprintf("/projects/%s/merge_requests/%d/changes", projectPath(ref.Repo), ref.Number)
	var resp gitlabChangesResponse
	if _, err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}

	files := make([]ChangedFile, len(resp.Changes))
	for i, ch := range resp.Changes {
		filename := ch.NewPath
		if ch.DeletedFile && ch.OldPath != "" {
			filename = ch.OldPath
		} else if filename == "" {
			filename = ch.OldPath
		}

		status := "modified"
		if ch.NewFile {
			status = "added"
		} else if ch.DeletedFile {
			status = "deleted"
		}

		var additions, deletions int
		lines := strings.Split(ch.Diff, "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
				continue
			}
			if strings.HasPrefix(line, "+") {
				additions++
			} else if strings.HasPrefix(line, "-") {
				deletions++
			}
		}

		files[i] = ChangedFile{
			Filename:  filename,
			Status:    status,
			Additions: additions,
			Deletions: deletions,
		}
	}
	return files, nil
}

// GetPRState retrieves lightweight state information for a merge request.
func (c *gitlabClient) GetPRState(ctx context.Context, ref IssueRef) (PRState, error) {
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}

	path := fmt.Sprintf("/projects/%s/merge_requests/%d", projectPath(ref.Repo), ref.Number)
	var raw gitlabMergeRequest
	if _, err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return PRState{}, err
	}

	st := strings.ToLower(strings.TrimSpace(raw.State))
	merged := (st == "merged")
	if st == "opened" {
		st = "open"
	}
	return PRState{
		Number:  raw.IID,
		State:   st,
		Merged:  merged,
		HeadSHA: raw.SHA,
	}, nil
}

// ClosePullRequest closes a merge request without merging.
func (c *gitlabClient) ClosePullRequest(ctx context.Context, ref IssueRef) error {
	if !c.Authenticated() {
		return ErrNoToken
	}
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}

	path := fmt.Sprintf("/projects/%s/merge_requests/%d", projectPath(ref.Repo), ref.Number)
	_, err := c.do(ctx, http.MethodPut, path, map[string]any{"state_event": "close"}, nil)
	return err
}

// GetCIChecks retrieves CI pipeline job runs for the merge request.
func (c *gitlabClient) GetCIChecks(ctx context.Context, ref IssueRef) ([]CheckRun, error) {
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}

	path := fmt.Sprintf("/projects/%s/merge_requests/%d/pipelines?per_page=1", projectPath(ref.Repo), ref.Number)
	var pipelines []gitlabPipeline
	if _, err := c.do(ctx, http.MethodGet, path, nil, &pipelines); err != nil {
		return nil, err
	}
	if len(pipelines) == 0 {
		return []CheckRun{}, nil
	}

	jobsPath := fmt.Sprintf("/projects/%s/pipelines/%d/jobs?per_page=100", projectPath(ref.Repo), pipelines[0].ID)
	var jobs []gitlabJob
	if _, err := c.do(ctx, http.MethodGet, jobsPath, nil, &jobs); err != nil {
		return nil, err
	}

	checks := make([]CheckRun, len(jobs))
	for i, job := range jobs {
		var status string
		var conclusion string
		switch strings.ToLower(strings.TrimSpace(job.Status)) {
		case "running", "pending", "created":
			status = "in_progress"
		case "success":
			status = "completed"
			conclusion = "success"
		case "failed":
			status = "completed"
			conclusion = "failure"
		case "canceled":
			status = "completed"
			conclusion = "cancelled"
		case "skipped":
			status = "completed"
			conclusion = "skipped"
		default:
			status = "in_progress"
		}

		summary := fmt.Sprintf("%s: %s", job.Stage, job.Status)
		if job.FailureReason != "" {
			summary = fmt.Sprintf("%s: %s (%s)", job.Stage, job.Status, job.FailureReason)
		}

		checks[i] = CheckRun{
			Name:       job.Name,
			Status:     status,
			Conclusion: conclusion,
			Summary:    summary,
			URL:        job.WebURL,
		}
	}
	return checks, nil
}

// GetPRReviews retrieves approvals and discussion notes for a merge request.
func (c *gitlabClient) GetPRReviews(ctx context.Context, ref IssueRef) ([]Review, error) {
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}

	approvalsPath := fmt.Sprintf("/projects/%s/merge_requests/%d/approvals", projectPath(ref.Repo), ref.Number)
	var approvals gitlabApprovalsResponse
	if _, err := c.do(ctx, http.MethodGet, approvalsPath, nil, &approvals); err != nil {
		return nil, err
	}

	notesPath := fmt.Sprintf("/projects/%s/merge_requests/%d/notes?per_page=100&sort=asc", projectPath(ref.Repo), ref.Number)
	var notes []gitlabNote
	if _, err := c.do(ctx, http.MethodGet, notesPath, nil, &notes); err != nil {
		return nil, err
	}

	var reviews []Review
	for _, app := range approvals.ApprovedBy {
		reviews = append(reviews, Review{
			Author: User{Login: app.User.Username},
			State:  "approved",
		})
	}

	for _, n := range notes {
		if n.System {
			continue
		}
		reviews = append(reviews, Review{
			Author:      User{Login: n.Author.Username},
			State:       "commented",
			Body:        n.Body,
			SubmittedAt: n.CreatedAt,
		})
	}

	sort.SliceStable(reviews, func(i, j int) bool {
		return reviews[i].SubmittedAt.Before(reviews[j].SubmittedAt)
	})

	return reviews, nil
}

// PostReviewComment posts a top-level review discussion comment on a merge request.
func (c *gitlabClient) PostReviewComment(ctx context.Context, ref IssueRef, body string) error {
	if !c.Authenticated() {
		return ErrNoToken
	}
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}

	path := fmt.Sprintf("/projects/%s/merge_requests/%d/notes", projectPath(ref.Repo), ref.Number)
	_, err := c.do(ctx, http.MethodPost, path, map[string]any{"body": body}, nil)
	return err
}

// MergePullRequest merges a merge request using explicit or repository merge strategies.
func (c *gitlabClient) MergePullRequest(ctx context.Context, ref IssueRef, opts MergeOptions) (MergeResult, error) {
	if !c.Authenticated() {
		return MergeResult{}, ErrNoToken
	}
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}

	payload := make(map[string]any)
	commitMsg := opts.CommitMessage
	if commitMsg == "" {
		commitMsg = opts.CommitTitle
	}
	if commitMsg != "" {
		payload["commit_message"] = commitMsg
	}
	if opts.SHA != "" {
		payload["sha"] = opts.SHA
	}

	switch opts.Method {
	case MergeMethodSquash:
		payload["squash"] = true
	case MergeMethodMerge:
		payload["squash"] = false
	case MergeMethodDefault:
		// Governed by repository settings; omit squash parameter
	}

	path := fmt.Sprintf("/projects/%s/merge_requests/%d/merge", projectPath(ref.Repo), ref.Number)
	var raw gitlabMergeResponse
	if _, err := c.do(ctx, http.MethodPut, path, payload, &raw); err != nil {
		if IsConflict(err) {
			return MergeResult{}, fmt.Errorf("%w: merge request cannot be merged: %w", ErrConflict, err)
		}
		return MergeResult{}, err
	}

	sha := raw.MergeCommitSHA
	if sha == "" {
		sha = raw.SHA
	}

	return MergeResult{
		Merged:  true,
		SHA:     sha,
		Message: "merged",
	}, nil
}
