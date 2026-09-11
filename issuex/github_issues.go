package issuex

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type ghIssue struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
	User    User   `json:"user"`
	Labels  []struct {
		Name string `json:"name"`
	} `json:"labels"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
	PullRequest *struct {
		URL     string `json:"url,omitempty"`
		HTMLURL string `json:"html_url,omitempty"`
	} `json:"pull_request,omitempty"`
}

func (g ghIssue) toIssue() Issue {
	var labelNames []string
	if len(g.Labels) > 0 {
		labelNames = make([]string, 0, len(g.Labels))
		for _, l := range g.Labels {
			if l.Name != "" {
				labelNames = append(labelNames, l.Name)
			}
		}
	}
	return Issue{
		Number:    g.Number,
		Title:     g.Title,
		Body:      g.Body,
		State:     g.State,
		URL:       g.HTMLURL,
		HTMLURL:   g.HTMLURL,
		Author:    g.User,
		Labels:    labelNames,
		CreatedAt: g.CreatedAt,
		UpdatedAt: g.UpdatedAt,
		ClosedAt:  g.ClosedAt,
		IsPR:      g.PullRequest != nil,
	}
}

// CreateIssue creates a new issue in the repository.
func (c *githubClient) CreateIssue(ctx context.Context, repo Repo, req CreateIssueRequest) (Issue, error) {
	if !c.Authenticated() {
		return Issue{}, ErrNoToken
	}
	if !repo.Valid() && c.repo.Valid() {
		repo = c.repo
	}
	payload := map[string]any{
		"title": req.Title,
		"body":  req.Body,
	}
	if len(req.Labels) > 0 {
		payload["labels"] = req.Labels
	}
	if len(req.Assignees) > 0 {
		payload["assignees"] = req.Assignees
	}
	var raw ghIssue
	path := fmt.Sprintf("/repos/%s/%s/issues", repo.Owner, repo.Name)
	if err := c.do(ctx, http.MethodPost, path, payload, &raw); err != nil {
		return Issue{}, err
	}
	out := raw.toIssue()
	out.IsPR = false
	return out, nil
}

// ReadIssue retrieves an issue and its comments by issue number.
func (c *githubClient) ReadIssue(ctx context.Context, ref IssueRef) (IssueThread, error) {
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}
	var raw ghIssue
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", ref.Repo.Owner, ref.Repo.Name, ref.Number)
	if err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return IssueThread{}, err
	}
	issue := raw.toIssue()
	cmts, cErr := c.ListComments(ctx, ref)
	if cErr != nil {
		return IssueThread{
			Issue:       issue,
			CommentsErr: cErr,
		}, nil
	}
	return IssueThread{
		Issue:     issue,
		Comments:  cmts.Comments,
		Truncated: cmts.Truncated,
	}, nil
}

// UpdateIssue updates fields on an existing issue.
func (c *githubClient) UpdateIssue(ctx context.Context, ref IssueRef, req UpdateIssueRequest) (Issue, error) {
	if !c.Authenticated() {
		return Issue{}, ErrNoToken
	}
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}
	payload := make(map[string]any)
	if req.Title != "" {
		payload["title"] = req.Title
	}
	if req.Body != "" {
		payload["body"] = req.Body
	}
	if req.State != "" {
		payload["state"] = req.State
	}
	if len(req.Labels) > 0 {
		payload["labels"] = req.Labels
	}
	if len(req.Assignees) > 0 {
		payload["assignees"] = req.Assignees
	}
	var raw ghIssue
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", ref.Repo.Owner, ref.Repo.Name, ref.Number)
	if err := c.do(ctx, http.MethodPatch, path, payload, &raw); err != nil {
		return Issue{}, err
	}
	return raw.toIssue(), nil
}

// CloseIssue closes an issue with an optional closing comment.
func (c *githubClient) CloseIssue(ctx context.Context, ref IssueRef, comment string) error {
	if !c.Authenticated() {
		return ErrNoToken
	}
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}
	if comment != "" {
		if _, err := c.AddComment(ctx, ref, comment); err != nil {
			return err
		}
	}
	payload := map[string]any{"state": "closed"}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", ref.Repo.Owner, ref.Repo.Name, ref.Number)
	return c.do(ctx, http.MethodPatch, path, payload, nil)
}

// ListIssues lists issues in a repository matching the provided filter criteria.
func (c *githubClient) ListIssues(ctx context.Context, repo Repo, filter IssueFilter) (IssueList, error) {
	if !repo.Valid() && c.repo.Valid() {
		repo = c.repo
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	state := filter.State
	if state == "" {
		state = "open"
	}

	q := make(url.Values)
	q.Set("state", state)
	q.Set("per_page", "100")
	if len(filter.Labels) > 0 {
		q.Set("labels", strings.Join(filter.Labels, ","))
	}
	if filter.Assignee != "" {
		q.Set("assignee", filter.Assignee)
	}
	if filter.Sort != "" {
		q.Set("sort", filter.Sort)
	}
	if filter.Direction != "" {
		q.Set("direction", filter.Direction)
	}

	var (
		collected  []Issue
		incomplete bool
		page       = 1
	)

	for {
		q.Set("page", strconv.Itoa(page))
		path := fmt.Sprintf("/repos/%s/%s/issues?%s", repo.Owner, repo.Name, q.Encode())
		var batch []ghIssue
		if err := c.do(ctx, http.MethodGet, path, nil, &batch); err != nil {
			return IssueList{}, err
		}
		if len(batch) == 0 {
			break
		}
		for _, item := range batch {
			if item.PullRequest != nil {
				continue
			}
			if len(collected) >= limit {
				incomplete = true
				break
			}
			collected = append(collected, item.toIssue())
		}
		if incomplete || len(collected) >= limit {
			if len(batch) == 100 {
				incomplete = true
			}
			break
		}
		if len(batch) < 100 {
			break
		}
		page++
	}

	return IssueList{
		Issues:     collected,
		Incomplete: incomplete,
	}, nil
}

// AddComment posts a new comment to an issue or pull request.
func (c *githubClient) AddComment(ctx context.Context, ref IssueRef, body string) (string, error) {
	if !c.Authenticated() {
		return "", ErrNoToken
	}
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}
	payload := map[string]any{"body": body}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", ref.Repo.Owner, ref.Repo.Name, ref.Number)
	var out struct {
		HTMLURL string `json:"html_url"`
	}
	if err := c.do(ctx, http.MethodPost, path, payload, &out); err != nil {
		return "", err
	}
	return out.HTMLURL, nil
}

// ListComments retrieves chronological comments on an issue up to 500 total comments.
func (c *githubClient) ListComments(ctx context.Context, ref IssueRef) (CommentList, error) {
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}
	var (
		all       []Comment
		truncated bool
	)
	for page := 1; page <= 5; page++ {
		path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments?per_page=100&page=%d", ref.Repo.Owner, ref.Repo.Name, ref.Number, page)
		var batch []Comment
		if err := c.do(ctx, http.MethodGet, path, nil, &batch); err != nil {
			return CommentList{}, err
		}
		all = append(all, batch...)
		if page == 5 && len(batch) == 100 {
			truncated = true
		}
		if len(batch) < 100 {
			break
		}
	}
	return CommentList{
		Comments:  all,
		Truncated: truncated,
	}, nil
}

// AddLabels adds one or more labels to an issue.
func (c *githubClient) AddLabels(ctx context.Context, ref IssueRef, labels []string) error {
	if len(labels) == 0 {
		return nil
	}
	if !c.Authenticated() {
		return ErrNoToken
	}
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}
	payload := map[string]any{"labels": labels}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/labels", ref.Repo.Owner, ref.Repo.Name, ref.Number)
	return c.do(ctx, http.MethodPost, path, payload, nil)
}

// RemoveLabel removes a label from an issue, treating 404 as success.
func (c *githubClient) RemoveLabel(ctx context.Context, ref IssueRef, label string) error {
	if !c.Authenticated() {
		return ErrNoToken
	}
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/labels/%s", ref.Repo.Owner, ref.Repo.Name, ref.Number, url.PathEscape(label))
	err := c.do(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		if IsNotFound(err) {
			return nil
		}
		var he *HTTPError
		if errors.As(err, &he) && he.Status == http.StatusNotFound {
			return nil
		}
		return err
	}
	return nil
}

// CreateLabel creates a label in the repository, treating already_exists as success.
func (c *githubClient) CreateLabel(ctx context.Context, repo Repo, label Label) error {
	if !c.Authenticated() {
		return ErrNoToken
	}
	if !repo.Valid() && c.repo.Valid() {
		repo = c.repo
	}
	color := strings.TrimPrefix(label.Color, "#")
	payload := map[string]any{
		"name": label.Name,
	}
	if color != "" {
		payload["color"] = color
	}
	if label.Description != "" {
		payload["description"] = label.Description
	}
	path := fmt.Sprintf("/repos/%s/%s/labels", repo.Owner, repo.Name)
	err := c.do(ctx, http.MethodPost, path, payload, nil)
	if err != nil {
		var he *HTTPError
		if errors.As(err, &he) && he.Status == http.StatusUnprocessableEntity {
			if strings.Contains(he.Message, "already_exists") {
				return nil
			}
		}
		if strings.Contains(err.Error(), "already_exists") {
			return nil
		}
		return err
	}
	return nil
}
