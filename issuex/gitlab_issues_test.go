package issuex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TS-03-9: CreateIssue posts issue metadata and returns created Issue with normalized open state
func TestGitLab_CreateIssue_TS_03_9(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.EscapedPath() != "/api/v4/projects/org%2Frepo/issues" {
			t.Errorf("expected path /api/v4/projects/org%%2Frepo/issues, got %s", r.URL.EscapedPath())
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode body: %v", err)
		}
		if req["title"] != "Bug title" {
			t.Errorf("expected title 'Bug title', got %v", req["title"])
		}
		if req["description"] != "Bug description" {
			t.Errorf("expected description 'Bug description', got %v", req["description"])
		}
		if req["labels"] != "bug,triage" {
			t.Errorf("expected labels 'bug,triage', got %v", req["labels"])
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{
			"iid": 42,
			"title": "Bug title",
			"description": "Bug description",
			"state": "opened",
			"web_url": "https://gitlab.com/org/repo/-/issues/42",
			"author": {"username": "alice"},
			"labels": ["bug", "triage"],
			"created_at": "2025-01-01T00:00:00Z",
			"updated_at": "2025-01-01T00:00:00Z"
		}`))
	}))
	defer srv.Close()

	c, err := NewGitLab(Options{BaseURL: srv.URL, Token: "glpat-tok"})
	if err != nil {
		t.Fatalf("NewGitLab failed: %v", err)
	}
	iss, err := c.CreateIssue(context.Background(), Repo{Owner: "org", Name: "repo"}, CreateIssueRequest{
		Title:  "Bug title",
		Body:   "Bug description",
		Labels: []string{"bug", "triage"},
	})
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	if iss.Number != 42 {
		t.Errorf("expected Number 42, got %d", iss.Number)
	}
	if iss.State != "open" {
		t.Errorf("expected State 'open', got %s", iss.State)
	}
	if iss.IsPR != false {
		t.Errorf("expected IsPR false, got %v", iss.IsPR)
	}
	if iss.Author.Login != "alice" {
		t.Errorf("expected Author.Login 'alice', got %s", iss.Author.Login)
	}
}

// TS-03-10: Issue lifecycle and label mutations without token are rejected with ErrNoToken
func TestGitLab_UnauthenticatedMutations_TS_03_10(t *testing.T) {
	c, err := NewGitLab(Options{BaseURL: "https://gitlab.example.com"})
	if err != nil {
		t.Fatalf("NewGitLab failed: %v", err)
	}
	ref := IssueRef{Repo: Repo{Owner: "org", Name: "repo"}, Number: 1}

	_, err1 := c.CreateIssue(context.Background(), ref.Repo, CreateIssueRequest{Title: "T"})
	if !IsNoToken(err1) {
		t.Errorf("CreateIssue: expected IsNoToken, got %v", err1)
	}

	_, err2 := c.UpdateIssue(context.Background(), ref, UpdateIssueRequest{})
	if !IsNoToken(err2) {
		t.Errorf("UpdateIssue: expected IsNoToken, got %v", err2)
	}

	err3 := c.CloseIssue(context.Background(), ref, "")
	if !IsNoToken(err3) {
		t.Errorf("CloseIssue: expected IsNoToken, got %v", err3)
	}

	_, err4 := c.AddComment(context.Background(), ref, "hello")
	if !IsNoToken(err4) {
		t.Errorf("AddComment: expected IsNoToken, got %v", err4)
	}

	err5 := c.AddLabels(context.Background(), ref, []string{"bug"})
	if !IsNoToken(err5) {
		t.Errorf("AddLabels: expected IsNoToken, got %v", err5)
	}

	err6 := c.RemoveLabel(context.Background(), ref, "bug")
	if !IsNoToken(err6) {
		t.Errorf("RemoveLabel: expected IsNoToken, got %v", err6)
	}

	err7 := c.CreateLabel(context.Background(), ref.Repo, Label{Name: "bug"})
	if !IsNoToken(err7) {
		t.Errorf("CreateLabel: expected IsNoToken, got %v", err7)
	}
}

// TS-03-11: ReadIssue fetches issue details and chronologically appends discussion comments
func TestGitLab_ReadIssue_TS_03_11(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/api/v4/projects/org%2Frepo/issues/5" {
			w.Write([]byte(`{
				"iid": 5, "title": "Title", "description": "Desc", "state": "opened",
				"author": {"username": "bob"}, "web_url": "https://gitlab.com/org/repo/-/issues/5"
			}`))
			return
		}
		if r.URL.EscapedPath() == "/api/v4/projects/org%2Frepo/issues/5/notes" {
			w.Write([]byte(`[
				{"id": 1, "body": "Comment 1", "system": false, "author": {"username": "carol"}},
				{"id": 2, "body": "system note", "system": true, "author": {"username": "gitlab-bot"}}
			]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c, err := NewGitLab(Options{BaseURL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatalf("NewGitLab failed: %v", err)
	}
	thread, err := c.ReadIssue(context.Background(), IssueRef{Repo: Repo{Owner: "org", Name: "repo"}, Number: 5})
	if err != nil {
		t.Fatalf("ReadIssue failed: %v", err)
	}
	if thread.Issue.Number != 5 {
		t.Errorf("expected Number 5, got %d", thread.Issue.Number)
	}
	if thread.Issue.State != "open" {
		t.Errorf("expected State 'open', got %s", thread.Issue.State)
	}
	if thread.Issue.IsPR != false {
		t.Errorf("expected IsPR false, got %v", thread.Issue.IsPR)
	}
	if len(thread.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(thread.Comments))
	}
	if thread.Comments[0].Body != "Comment 1" {
		t.Errorf("expected comment body 'Comment 1', got %s", thread.Comments[0].Body)
	}
}

// TS-03-12: ReadIssue handles comment fetch failure gracefully and annotates unauthenticated 404 errors
func TestGitLab_ReadIssue_CommentsErrAndUnauth404_TS_03_12(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/api/v4/projects/org%2Frepo/issues/7" {
			w.Write([]byte(`{"iid": 7, "title": "Issue 7", "state": "opened", "author": {"username": "u"}}`))
			return
		}
		if r.URL.EscapedPath() == "/api/v4/projects/org%2Frepo/issues/7/notes" {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message": "notes service unavailable"}`))
			return
		}
		if r.URL.EscapedPath() == "/api/v4/projects/org%2Frepo/issues/99" {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message": "404 Not Found"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	cAuth, err := NewGitLab(Options{BaseURL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatalf("NewGitLab failed: %v", err)
	}
	thread, err := cAuth.ReadIssue(context.Background(), IssueRef{Repo: Repo{Owner: "org", Name: "repo"}, Number: 7})
	if err != nil {
		t.Fatalf("expected nil error from ReadIssue, got %v", err)
	}
	if thread.Issue.Number != 7 {
		t.Errorf("expected Issue.Number 7, got %d", thread.Issue.Number)
	}
	if thread.CommentsErr == nil {
		t.Errorf("expected non-nil CommentsErr, got nil")
	}

	cUnauth, err := NewGitLab(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewGitLab failed: %v", err)
	}
	_, errNotFound := cUnauth.ReadIssue(context.Background(), IssueRef{Repo: Repo{Owner: "org", Name: "repo"}, Number: 99})
	if !IsNotFound(errNotFound) {
		t.Errorf("expected IsNotFound(err) == true, got %v", errNotFound)
	}
	if !strings.Contains(errNotFound.Error(), "GITLAB_TOKEN") {
		t.Errorf("expected error message to mention GITLAB_TOKEN, got %v", errNotFound)
	}
}

// TS-03-13: UpdateIssue modifies issue attributes and CloseIssue posts closing comment before closing issue
func TestGitLab_UpdateAndCloseIssue_TS_03_13(t *testing.T) {
	var updatedBody string
	var stateEvent string
	var postedComment string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/notes") {
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			if b, ok := req["body"].(string); ok {
				postedComment = b
			}
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id": 10, "web_url": "https://gitlab.com/n/10"}`))
			return
		}
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/issues/12") {
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			if desc, ok := req["description"].(string); ok {
				updatedBody = desc
			}
			if se, ok := req["state_event"].(string); ok {
				stateEvent = se
			}
			w.Write([]byte(`{"iid": 12, "title": "T", "description": "new body", "state": "closed", "author": {"username": "u"}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c, err := NewGitLab(Options{BaseURL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatalf("NewGitLab failed: %v", err)
	}
	ref := IssueRef{Repo: Repo{Owner: "org", Name: "repo"}, Number: 12}
	newBody := "new body"
	iss, err := c.UpdateIssue(context.Background(), ref, UpdateIssueRequest{Body: newBody})
	if err != nil {
		t.Fatalf("UpdateIssue failed: %v", err)
	}
	if iss.Body != "new body" {
		t.Errorf("expected Body 'new body', got %s", iss.Body)
	}
	if updatedBody != "new body" {
		t.Errorf("expected server received updated body 'new body', got %s", updatedBody)
	}

	errClose := c.CloseIssue(context.Background(), ref, "resolved in v2")
	if errClose != nil {
		t.Fatalf("CloseIssue failed: %v", errClose)
	}
	if postedComment != "resolved in v2" {
		t.Errorf("expected posted comment 'resolved in v2', got %s", postedComment)
	}
	if stateEvent != "close" {
		t.Errorf("expected state_event 'close', got %s", stateEvent)
	}
}

// TS-03-14: ListIssues paginates issues matching filters and flags incomplete results when limit is reached
func TestGitLab_ListIssues_TS_03_14(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Query().Get("state") != "opened" {
			t.Errorf("expected state 'opened', got %s", r.URL.Query().Get("state"))
		}
		if r.URL.Query().Get("labels") != "feature,v1" {
			t.Errorf("expected labels 'feature,v1', got %s", r.URL.Query().Get("labels"))
		}
		if r.URL.Query().Get("assignee_username") != "alice" {
			t.Errorf("expected assignee_username 'alice', got %s", r.URL.Query().Get("assignee_username"))
		}
		if r.URL.Query().Get("order_by") != "created_at" {
			t.Errorf("expected order_by 'created_at', got %s", r.URL.Query().Get("order_by"))
		}
		if r.URL.Query().Get("sort") != "desc" {
			t.Errorf("expected sort 'desc', got %s", r.URL.Query().Get("sort"))
		}
		w.Header().Set("X-Next-Page", "2")
		w.Write([]byte(`[{"iid": 1, "title": "Issue 1", "state": "opened", "author": {"username": "a"}}]`))
	}))
	defer srv.Close()

	c, err := NewGitLab(Options{BaseURL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatalf("NewGitLab failed: %v", err)
	}
	list, err := c.ListIssues(context.Background(), Repo{Owner: "org", Name: "repo"}, IssueFilter{
		State:     "open",
		Labels:    []string{"feature", "v1"},
		Assignee:  "alice",
		Sort:      "created",
		Direction: "desc",
		Limit:     1,
	})
	if err != nil {
		t.Fatalf("ListIssues failed: %v", err)
	}
	if len(list.Issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(list.Issues))
	}
	if !list.Incomplete {
		t.Errorf("expected Incomplete true, got false")
	}
}

// TS-03-15: AddComment posts a note and ListComments paginates user notes up to 500 cap filtering system notes
func TestGitLab_Comments_TS_03_15(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/notes") {
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id": 42, "body": "a note", "web_url": "https://gitlab.com/note/42"}`))
			return
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/notes") {
			page := r.URL.Query().Get("page")
			if page == "1" {
				w.Header().Set("X-Next-Page", "2")
				w.Write([]byte(`[{"id": 1, "body": "user note", "system": false, "author": {"username": "u"}}, {"id": 2, "body": "audit", "system": true, "author": {"username": "bot"}}]`))
			} else {
				w.Write([]byte(`[]`))
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c, err := NewGitLab(Options{BaseURL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatalf("NewGitLab failed: %v", err)
	}
	ref := IssueRef{Repo: Repo{Owner: "org", Name: "repo"}, Number: 1}
	url, err := c.AddComment(context.Background(), ref, "a note")
	if err != nil {
		t.Fatalf("AddComment failed: %v", err)
	}
	if url != "https://gitlab.com/note/42" {
		t.Errorf("expected url https://gitlab.com/note/42, got %s", url)
	}

	comments, errList := c.ListComments(context.Background(), ref)
	if errList != nil {
		t.Fatalf("ListComments failed: %v", errList)
	}
	if len(comments.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments.Comments))
	}
	if comments.Comments[0].Body != "user note" {
		t.Errorf("expected comment 'user note', got %s", comments.Comments[0].Body)
	}
	if comments.Truncated {
		t.Errorf("expected Truncated false, got true")
	}
}

// TS-03-16: AddLabels and RemoveLabel manage issue labels with no-op on empty labels and idempotent removal
func TestGitLab_AddAndRemoveLabels_TS_03_16(t *testing.T) {
	var addedLabels string
	var removedLabels string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if al, ok := req["add_labels"].(string); ok {
			addedLabels = al
		}
		if rl, ok := req["remove_labels"].(string); ok {
			removedLabels = rl
		}
		w.Write([]byte(`{"iid": 1, "labels": ["bug"]}`))
	}))
	defer srv.Close()

	c, err := NewGitLab(Options{BaseURL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatalf("NewGitLab failed: %v", err)
	}
	ref := IssueRef{Repo: Repo{Owner: "org", Name: "repo"}, Number: 1}

	if err := c.AddLabels(context.Background(), ref, nil); err != nil {
		t.Fatalf("AddLabels with nil failed: %v", err)
	}
	if addedLabels != "" {
		t.Errorf("expected no network call for nil labels, got addedLabels=%s", addedLabels)
	}

	if err := c.AddLabels(context.Background(), ref, []string{"bug", "ui"}); err != nil {
		t.Fatalf("AddLabels failed: %v", err)
	}
	if addedLabels != "bug,ui" {
		t.Errorf("expected addedLabels 'bug,ui', got %s", addedLabels)
	}

	if err := c.RemoveLabel(context.Background(), ref, "bug"); err != nil {
		t.Fatalf("RemoveLabel failed: %v", err)
	}
	if removedLabels != "bug" {
		t.Errorf("expected removedLabels 'bug', got %s", removedLabels)
	}
}

// TS-03-17: CreateLabel formats hex color with leading hash and succeeds idempotently on existing labels
func TestGitLab_CreateLabel_TS_03_17(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		color, _ := req["color"].(string)
		if !strings.HasPrefix(color, "#") {
			t.Errorf("expected color to start with #, got %s", color)
		}
		name, _ := req["name"].(string)
		if name == "existing-409" {
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"message": "Label already exists"}`))
			return
		}
		if name == "existing-400" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"message": "Label already exists"}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id": 100, "name": "new-label", "color": "#FF0000"}`))
	}))
	defer srv.Close()

	c, err := NewGitLab(Options{BaseURL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatalf("NewGitLab failed: %v", err)
	}
	repo := Repo{Owner: "org", Name: "repo"}

	if err := c.CreateLabel(context.Background(), repo, Label{Name: "new-label", Color: "FF0000"}); err != nil {
		t.Errorf("CreateLabel new-label failed: %v", err)
	}
	if err := c.CreateLabel(context.Background(), repo, Label{Name: "existing-409", Color: "#FF0000"}); err != nil {
		t.Errorf("CreateLabel existing-409 failed: %v", err)
	}
	if err := c.CreateLabel(context.Background(), repo, Label{Name: "existing-400", Color: "#FF0000"}); err != nil {
		t.Errorf("CreateLabel existing-400 failed: %v", err)
	}
}
