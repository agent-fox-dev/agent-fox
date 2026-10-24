package fakehub

import (
	"fmt"
	"net/http"
	"slices"
	"time"
)

// Comment is a comment of an issue. CreatedAt is when it was added: by the fake's clock for one a test or a request
// adds, and a fixed time for one an issue was added with and that names none.
type Comment struct {
	ID           int64
	Author, Body string
	CreatedAt    time.Time
}

// Issue is an issue of the forge repository of a workspace; IsPR makes it a pull or merge
// request.
type Issue struct {
	Number       int
	Title, Body  string
	State        string // open when empty
	Author       string
	Labels       []string
	Comments     []Comment
	IsPR         bool
	CreatedAt    time.Time
	LastUpdateBy string // the PAT-less description of the last write, for a test to read
}

// Pull is a pull request that was opened through the hub.
type Pull struct {
	Number      int
	Title, Body string
	Head, Base  string
	Draft       bool
}

// issueState is an Issue the hub keeps.
type issueState struct{ Issue }

// AddIssue adds an issue to the forge repository of the workspace.
func (h *Hub) AddIssue(slug string, i Issue) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws := h.workspaces[slug]
	if ws == nil {
		panic("fakehub: AddIssue: no workspace " + slug)
	}
	if i.State == "" {
		i.State = "open"
	}
	if i.Author == "" {
		i.Author = "reporter"
	}
	if i.CreatedAt.IsZero() {
		i.CreatedAt = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	}
	for k := range i.Comments {
		if i.Comments[k].ID == 0 {
			i.Comments[k].ID = int64(k + 1)
		}
		if i.Comments[k].CreatedAt.IsZero() {
			i.Comments[k].CreatedAt = time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC)
		}
	}
	ws.issues[i.Number] = &issueState{i}
	ws.nextItem = max(ws.nextItem, i.Number+1)
}

// IssueOf is the issue as the hub holds it now.
func (h *Hub) IssueOf(slug string, n int) (Issue, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws := h.workspaces[slug]
	if ws == nil || ws.issues[n] == nil {
		return Issue{}, false
	}
	i := ws.issues[n].Issue
	i.Labels = slices.Clone(i.Labels)
	i.Comments = slices.Clone(i.Comments)
	return i, true
}

// Pulls are the pull requests that were opened, in order.
func (h *Hub) Pulls(slug string) []Pull {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ws := h.workspaces[slug]; ws != nil {
		return slices.Clone(ws.pulls)
	}
	return nil
}

// issueJSON is the issue as hub's forge API writes it.
func issueJSON(slug string, i Issue) map[string]any {
	out := map[string]any{
		"number": i.Number, "title": i.Title, "body": i.Body, "state": i.State,
		"url":        fmt.Sprintf("https://api.example.com/repos/acme/widgets/issues/%d", i.Number),
		"html_url":   fmt.Sprintf("https://github.example.com/acme/widgets/issues/%d", i.Number),
		"author":     map[string]any{"login": i.Author},
		"created_at": stamp(i.CreatedAt), "updated_at": stamp(i.CreatedAt), "is_pr": i.IsPR,
	}
	if len(i.Labels) > 0 {
		out["labels"] = i.Labels
	}
	_ = slug
	return out
}

// commentJSON is the comment as hub's forge API writes it.
func commentJSON(c Comment) map[string]any {
	return map[string]any{"id": c.ID, "body": c.Body, "user": map[string]any{"login": c.Author}, "created_at": stamp(c.CreatedAt)}
}

// forgeRoute serves the forge API under /api/v1/workspaces/{slug}.
func (h *Hub) forgeRoute(w http.ResponseWriter, r *http.Request, ws *workspace, rest []string) {
	switch {
	case slices.Equal(rest, []string{"issues"}) && r.Method == http.MethodGet:
		h.listIssues(w, r, ws)
	case slices.Equal(rest, []string{"issues"}) && r.Method == http.MethodPost:
		h.createIssue(w, r, ws)
	case slices.Equal(rest, []string{"pulls"}) && r.Method == http.MethodPost:
		h.createPull(w, r, ws)
	case len(rest) >= 2 && rest[0] == "issues":
		n := number(rest[1])
		h.mu.Lock()
		issue := ws.issues[n]
		h.mu.Unlock()
		if n == 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "n must be a positive decimal integer")
			return
		}
		if issue == nil {
			writeError(w, http.StatusNotFound, "forge_not_found", "forge_not_found: the issue does not exist")
			return
		}
		h.issueRoute(w, r, ws, issue, rest[2:])
	default:
		writeError(w, http.StatusNotFound, "not_found", "not found")
	}
}

// issueRoute serves the routes of one issue.
func (h *Hub) issueRoute(w http.ResponseWriter, r *http.Request, ws *workspace, issue *issueState, rest []string) {
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		h.mu.Lock()
		out := map[string]any{"issue": issueJSON(ws.Slug, issue.Issue), "comments": commentsJSON(issue.Comments), "truncated": false}
		h.mu.Unlock()
		writeJSON(w, http.StatusOK, out)
	case len(rest) == 0 && r.Method == http.MethodPatch:
		var req struct {
			Title  *string   `json:"title"`
			Body   *string   `json:"body"`
			State  *string   `json:"state"`
			Labels *[]string `json:"labels"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		h.mu.Lock()
		if req.Title != nil {
			issue.Title = *req.Title
		}
		if req.Body != nil {
			issue.Body = *req.Body
		}
		if req.State != nil {
			issue.State = *req.State
		}
		if req.Labels != nil {
			ws.relabel(issue, *req.Labels)
		}
		out := issueJSON(ws.Slug, issue.Issue)
		h.mu.Unlock()
		writeJSON(w, http.StatusOK, out)
	case slices.Equal(rest, []string{"comments"}) && r.Method == http.MethodGet:
		h.mu.Lock()
		out := commentsJSON(issue.Comments)
		h.mu.Unlock()
		writeJSON(w, http.StatusOK, out)
	case slices.Equal(rest, []string{"comments"}) && r.Method == http.MethodPost:
		var req struct {
			Body string `json:"body"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		if req.Body == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "body must not be blank")
			return
		}
		h.mu.Lock()
		c := Comment{ID: int64(len(issue.Comments) + 1), Author: "agent-fox", Body: req.Body, CreatedAt: h.now()}
		issue.Comments = append(issue.Comments, c)
		h.mu.Unlock()
		writeJSON(w, http.StatusCreated, map[string]any{"url": fmt.Sprintf("https://github.example.com/acme/widgets/issues/%d#issuecomment-%d", issue.Number, c.ID)})
	case slices.Equal(rest, []string{"labels"}) && r.Method == http.MethodPost:
		var names []string
		if !readJSON(w, r, &names) {
			return
		}
		h.mu.Lock()
		next := slices.Clone(issue.Labels)
		for _, name := range names {
			if !slices.Contains(next, name) {
				next = append(next, name)
				h.labeled(ws.Slug, issue.Number, name)
			}
		}
		ws.relabel(issue, next)
		h.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case len(rest) == 2 && rest[0] == "labels" && r.Method == http.MethodDelete:
		h.mu.Lock()
		ws.relabel(issue, slices.DeleteFunc(slices.Clone(issue.Labels), func(l string) bool { return l == rest[1] }))
		h.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusNotFound, "not_found", "not found")
	}
}

// commentsJSON is the comments as hub's forge API writes them: a bare array.
func commentsJSON(comments []Comment) []map[string]any {
	out := []map[string]any{}
	for _, c := range comments {
		out = append(out, commentJSON(c))
	}
	return out
}

// createIssue answers POST …/issues.
func (h *Hub) createIssue(w http.ResponseWriter, r *http.Request, ws *workspace) {
	var req struct {
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Labels []string `json:"labels"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "title must not be blank")
		return
	}
	h.mu.Lock()
	n := ws.nextItem
	ws.nextItem++
	i := Issue{Number: n, Title: req.Title, Body: req.Body, State: "open", Author: "agent-fox", Labels: slices.Clone(req.Labels), CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	ws.issues[n] = &issueState{i}
	h.mu.Unlock()
	writeJSON(w, http.StatusCreated, issueJSON(ws.Slug, i))
}

// createPull answers POST …/pulls.
func (h *Hub) createPull(w http.ResponseWriter, r *http.Request, ws *workspace) {
	var req struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		Head  string `json:"head"`
		Base  string `json:"base"`
		Draft bool   `json:"draft"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Title == "" || req.Head == "" || req.Base == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "title, head and base must not be blank")
		return
	}
	h.mu.Lock()
	n := ws.nextItem
	ws.nextItem++
	ws.pulls = append(ws.pulls, Pull{Number: n, Title: req.Title, Body: req.Body, Head: req.Head, Base: req.Base, Draft: req.Draft})
	h.seq++
	h.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{
		"number": n, "title": req.Title, "body": req.Body, "state": "open", "draft": req.Draft, "head_branch": req.Head, "base_branch": req.Base,
		"url": fmt.Sprintf("https://api.example.com/repos/acme/widgets/pulls/%d", n), "html_url": fmt.Sprintf("https://github.example.com/acme/widgets/pull/%d", n),
	})
}

// Issues are the issues of the forge repository of the workspace, the ones that were added and the ones
// that were created through the hub, by number.
func (h *Hub) Issues(slug string) []Issue {
	h.mu.Lock()
	defer h.mu.Unlock()
	ws := h.workspaces[slug]
	if ws == nil {
		return nil
	}
	numbers := make([]int, 0, len(ws.issues))
	for n := range ws.issues {
		numbers = append(numbers, n)
	}
	slices.Sort(numbers)
	out := make([]Issue, 0, len(numbers))
	for _, n := range numbers {
		i := ws.issues[n].Issue
		i.Labels = slices.Clone(i.Labels)
		i.Comments = slices.Clone(i.Comments)
		out = append(out, i)
	}
	return out
}
