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

type gitlabIssue struct {
	ID          int    `json:"id"`
	IID         int    `json:"iid"`
	Title       string `json:"title"`
	Description string `json:"description"`
	State       string `json:"state"`
	WebURL      string `json:"web_url"`
	Author      struct {
		Username string `json:"username"`
	} `json:"author"`
	Labels    []string   `json:"labels"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
}

func (g gitlabIssue) toIssue() Issue {
	st := g.State
	if st == "opened" {
		st = "open"
	}
	return Issue{
		Number:    g.IID,
		Title:     g.Title,
		Body:      g.Description,
		State:     st,
		URL:       g.WebURL,
		HTMLURL:   g.WebURL,
		Author:    User{Login: g.Author.Username},
		Labels:    g.Labels,
		CreatedAt: g.CreatedAt,
		UpdatedAt: g.UpdatedAt,
		ClosedAt:  g.ClosedAt,
		IsPR:      false,
	}
}

type gitlabNote struct {
	ID        int       `json:"id"`
	Body      string    `json:"body"`
	System    bool      `json:"system"`
	WebURL    string    `json:"web_url"`
	CreatedAt time.Time `json:"created_at"`
	Author    struct {
		Username string `json:"username"`
	} `json:"author"`
}

// CreateIssue creates an issue on GitLab.
func (c *gitlabClient) CreateIssue(ctx context.Context, repo Repo, req CreateIssueRequest) (Issue, error) {
	if !c.Authenticated() {
		return Issue{}, ErrNoToken
	}
	if !repo.Valid() && c.repo.Valid() {
		repo = c.repo
	}

	payload := map[string]any{
		"title":       req.Title,
		"description": req.Body,
	}
	if len(req.Labels) > 0 {
		payload["labels"] = strings.Join(req.Labels, ",")
	}

	path := fmt.Sprintf("/projects/%s/issues", projectPath(repo))
	var raw gitlabIssue
	if _, err := c.do(ctx, http.MethodPost, path, payload, &raw); err != nil {
		return Issue{}, err
	}
	return raw.toIssue(), nil
}

// ReadIssue reads an issue and its comments by issue iid.
func (c *gitlabClient) ReadIssue(ctx context.Context, ref IssueRef) (IssueThread, error) {
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}

	path := fmt.Sprintf("/projects/%s/issues/%d", projectPath(ref.Repo), ref.Number)
	var raw gitlabIssue
	if _, err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		if IsNotFound(err) && !c.Authenticated() {
			return IssueThread{}, fmt.Errorf("%w: issue or project may be private and require setting GITLAB_TOKEN: %w", ErrNotFound, err)
		}
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
func (c *gitlabClient) UpdateIssue(ctx context.Context, ref IssueRef, req UpdateIssueRequest) (Issue, error) {
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
		payload["description"] = req.Body
	}
	if req.State != "" {
		if req.State == "closed" {
			payload["state_event"] = "close"
		} else if req.State == "open" || req.State == "opened" {
			payload["state_event"] = "reopen"
		}
	}
	if len(req.Labels) > 0 {
		payload["labels"] = strings.Join(req.Labels, ",")
	}

	path := fmt.Sprintf("/projects/%s/issues/%d", projectPath(ref.Repo), ref.Number)
	var raw gitlabIssue
	if _, err := c.do(ctx, http.MethodPut, path, payload, &raw); err != nil {
		return Issue{}, err
	}
	return raw.toIssue(), nil
}

// CloseIssue closes an issue with an optional closing comment.
func (c *gitlabClient) CloseIssue(ctx context.Context, ref IssueRef, comment string) error {
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

	payload := map[string]any{"state_event": "close"}
	path := fmt.Sprintf("/projects/%s/issues/%d", projectPath(ref.Repo), ref.Number)
	_, err := c.do(ctx, http.MethodPut, path, payload, nil)
	return err
}

// ListIssues lists issues in a repository matching the provided filter criteria.
func (c *gitlabClient) ListIssues(ctx context.Context, repo Repo, filter IssueFilter) (IssueList, error) {
	if !repo.Valid() && c.repo.Valid() {
		repo = c.repo
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}

	var glState string
	switch filter.State {
	case "open":
		glState = "opened"
	case "closed":
		glState = "closed"
	case "all":
		glState = "all"
	default:
		glState = "opened"
	}

	orderBy := "created_at"
	switch filter.Sort {
	case "updated":
		orderBy = "updated_at"
	case "created":
		orderBy = "created_at"
	}

	sortDir := "desc"
	if filter.Direction != "" {
		sortDir = filter.Direction
	}

	q := make(url.Values)
	q.Set("state", glState)
	q.Set("order_by", orderBy)
	q.Set("sort", sortDir)
	q.Set("per_page", "100")
	if len(filter.Labels) > 0 {
		q.Set("labels", strings.Join(filter.Labels, ","))
	}
	if filter.Assignee != "" {
		q.Set("assignee_username", filter.Assignee)
	}

	var (
		collected  []Issue
		incomplete bool
		page       = 1
	)

	for {
		q.Set("page", strconv.Itoa(page))
		path := fmt.Sprintf("/projects/%s/issues?%s", projectPath(repo), q.Encode())
		var batch []gitlabIssue
		resp, _, err := c.doWithResponse(ctx, http.MethodGet, path, nil, &batch)
		if err != nil {
			return IssueList{}, err
		}
		if len(batch) == 0 {
			break
		}

		for _, item := range batch {
			if len(collected) >= limit {
				incomplete = true
				break
			}
			collected = append(collected, item.toIssue())
		}
		if incomplete || len(collected) >= limit {
			if len(batch) == 100 || (resp != nil && resp.Header.Get("X-Next-Page") != "") {
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

// AddComment posts a new note (comment) to an issue.
func (c *gitlabClient) AddComment(ctx context.Context, ref IssueRef, body string) (string, error) {
	if !c.Authenticated() {
		return "", ErrNoToken
	}
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}

	payload := map[string]any{"body": body}
	path := fmt.Sprintf("/projects/%s/issues/%d/notes", projectPath(ref.Repo), ref.Number)
	var note gitlabNote
	if _, err := c.do(ctx, http.MethodPost, path, payload, &note); err != nil {
		return "", err
	}

	if note.WebURL != "" {
		return note.WebURL, nil
	}
	// Fallback to {issue_url}#note_{note_id}
	return fmt.Sprintf("%s/%s/-/issues/%d#note_%d", strings.TrimRight(c.baseURL, "/"), ref.Repo.String(), ref.Number, note.ID), nil
}

// ListComments retrieves chronological comments on an issue up to 500 total notes, filtering out system notes.
func (c *gitlabClient) ListComments(ctx context.Context, ref IssueRef) (CommentList, error) {
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}

	var (
		all       []Comment
		truncated bool
	)

	for page := 1; page <= 5; page++ {
		path := fmt.Sprintf("/projects/%s/issues/%d/notes?per_page=100&page=%d&sort=asc&order_by=created_at",
			projectPath(ref.Repo), ref.Number, page)
		var batch []gitlabNote
		if _, err := c.do(ctx, http.MethodGet, path, nil, &batch); err != nil {
			return CommentList{}, err
		}
		for _, n := range batch {
			if n.System {
				continue
			}
			all = append(all, Comment{
				Body:      n.Body,
				User:      User{Login: n.Author.Username},
				CreatedAt: n.CreatedAt,
				HTMLURL:   n.WebURL,
			})
		}
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
func (c *gitlabClient) AddLabels(ctx context.Context, ref IssueRef, labels []string) error {
	if len(labels) == 0 {
		return nil
	}
	if !c.Authenticated() {
		return ErrNoToken
	}
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}

	payload := map[string]any{"add_labels": strings.Join(labels, ",")}
	path := fmt.Sprintf("/projects/%s/issues/%d", projectPath(ref.Repo), ref.Number)
	_, err := c.do(ctx, http.MethodPut, path, payload, nil)
	return err
}

// RemoveLabel removes a label from an issue, succeeding idempotently.
func (c *gitlabClient) RemoveLabel(ctx context.Context, ref IssueRef, label string) error {
	if !c.Authenticated() {
		return ErrNoToken
	}
	if !ref.Repo.Valid() && c.repo.Valid() {
		ref.Repo = c.repo
	}

	payload := map[string]any{"remove_labels": label}
	path := fmt.Sprintf("/projects/%s/issues/%d", projectPath(ref.Repo), ref.Number)
	_, err := c.do(ctx, http.MethodPut, path, payload, nil)
	return err
}

// CreateLabel creates a label in the repository, treating HTTP 409 or HTTP 400 with "already exists" as success.
func (c *gitlabClient) CreateLabel(ctx context.Context, repo Repo, label Label) error {
	if !c.Authenticated() {
		return ErrNoToken
	}
	if !repo.Valid() && c.repo.Valid() {
		repo = c.repo
	}

	color := label.Color
	if !strings.HasPrefix(color, "#") {
		color = "#" + color
	}

	payload := map[string]any{
		"name":  label.Name,
		"color": color,
	}
	if label.Description != "" {
		payload["description"] = label.Description
	}

	path := fmt.Sprintf("/projects/%s/labels", projectPath(repo))
	_, err := c.do(ctx, http.MethodPost, path, payload, nil)
	if err != nil {
		if IsConflict(err) {
			return nil
		}
		var he *HTTPError
		if errors.As(err, &he) {
			if he.Status == http.StatusConflict {
				return nil
			}
			if he.Status == http.StatusBadRequest && strings.Contains(strings.ToLower(he.Message), "already exists") {
				return nil
			}
		}
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return nil
		}
		return err
	}
	return nil
}
