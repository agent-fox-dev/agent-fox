package issuex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestGitHub_Issue_Create_TS_02_7 verifies TS-02-7:
// CreateIssue posts issue metadata and returns created Issue struct.
// Verifies: 02-REQ-3.1
func TestGitHub_Issue_Create_TS_02_7(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/repos/owner/repo/issues" {
			t.Errorf("expected /repos/owner/repo/issues, got %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		if body["title"] != "Bug title" {
			t.Errorf("expected title 'Bug title', got %v", body["title"])
		}
		if body["body"] != "Bug body" {
			t.Errorf("expected body 'Bug body', got %v", body["body"])
		}
		lbls, ok := body["labels"].([]any)
		if !ok || len(lbls) == 0 || lbls[0] != "bug" {
			t.Errorf("expected labels ['bug'], got %v", body["labels"])
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{
			"number": 12,
			"title": "Bug title",
			"body": "Bug body",
			"state": "open",
			"html_url": "https://github.com/owner/repo/issues/12",
			"user": {"login": "alice"},
			"labels": [{"name": "bug"}],
			"created_at": "2025-01-01T00:00:00Z",
			"updated_at": "2025-01-01T00:00:00Z"
		}`))
	}))
	defer srv.Close()

	c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"})
	issue, err := c.CreateIssue(context.Background(), Repo{Owner: "owner", Name: "repo"}, CreateIssueRequest{
		Title: "Bug title", Body: "Bug body", Labels: []string{"bug"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.Number != 12 {
		t.Errorf("expected number 12, got %d", issue.Number)
	}
	if issue.Title != "Bug title" {
		t.Errorf("expected title 'Bug title', got %s", issue.Title)
	}
	if issue.Author.Login != "alice" {
		t.Errorf("expected author alice, got %s", issue.Author.Login)
	}
	if len(issue.Labels) != 1 || issue.Labels[0] != "bug" {
		t.Errorf("expected labels ['bug'], got %v", issue.Labels)
	}
	if issue.IsPR {
		t.Errorf("expected IsPR false, got true")
	}
}

// TestGitHub_Issue_Unauthenticated_ErrNoToken_TS_02_8 verifies TS-02-8:
// Unauthenticated mutation operations immediately reject with ErrNoToken without network calls.
// Verifies: 02-REQ-3.2, 02-REQ-4.7, 02-REQ-5.7, 02-REQ-6.7
func TestGitHub_Issue_Unauthenticated_ErrNoToken_TS_02_8(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request on unauthenticated mutation: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: ""})
	ctx := context.Background()
	ref := IssueRef{Repo: Repo{Owner: "o", Name: "r"}, Number: 1}
	repo := Repo{Owner: "o", Name: "r"}

	_, err1 := c.CreateIssue(ctx, repo, CreateIssueRequest{Title: "t"})
	if !IsNoToken(err1) {
		t.Errorf("CreateIssue: expected IsNoToken, got %v", err1)
	}

	_, err2 := c.UpdateIssue(ctx, ref, UpdateIssueRequest{Title: "t"})
	if !IsNoToken(err2) {
		t.Errorf("UpdateIssue: expected IsNoToken, got %v", err2)
	}

	err3 := c.CloseIssue(ctx, ref, "closing")
	if !IsNoToken(err3) {
		t.Errorf("CloseIssue: expected IsNoToken, got %v", err3)
	}

	_, err4 := c.AddComment(ctx, ref, "cmt")
	if !IsNoToken(err4) {
		t.Errorf("AddComment: expected IsNoToken, got %v", err4)
	}

	err5 := c.AddLabels(ctx, ref, []string{"lbl"})
	if !IsNoToken(err5) {
		t.Errorf("AddLabels: expected IsNoToken, got %v", err5)
	}

	err6 := c.RemoveLabel(ctx, ref, "lbl")
	if !IsNoToken(err6) {
		t.Errorf("RemoveLabel: expected IsNoToken, got %v", err6)
	}

	err7 := c.CreateLabel(ctx, repo, Label{Name: "lbl"})
	if !IsNoToken(err7) {
		t.Errorf("CreateLabel: expected IsNoToken, got %v", err7)
	}

	_, err8 := c.CreatePullRequest(ctx, repo, CreatePullRequestRequest{Title: "t"})
	if !IsNoToken(err8) {
		t.Errorf("CreatePullRequest: expected IsNoToken, got %v", err8)
	}

	err9 := c.ClosePullRequest(ctx, ref)
	if !IsNoToken(err9) {
		t.Errorf("ClosePullRequest: expected IsNoToken, got %v", err9)
	}

	err10 := c.PostReviewComment(ctx, ref, "rev")
	if !IsNoToken(err10) {
		t.Errorf("PostReviewComment: expected IsNoToken, got %v", err10)
	}

	_, err11 := c.MergePullRequest(ctx, ref, MergeOptions{})
	if !IsNoToken(err11) {
		t.Errorf("MergePullRequest: expected IsNoToken, got %v", err11)
	}
}

type testHeaderRoundTripper struct {
	rt   http.RoundTripper
	name string
	val  string
}

func (h *testHeaderRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set(h.name, h.val)
	return h.rt.RoundTrip(req)
}

// TestGitHub_Issue_Read_TS_02_9 verifies TS-02-9:
// ReadIssue fetches issue metadata, sets IsPR, and handles comment retrieval and partial failures.
// Verifies: 02-REQ-3.3, 02-REQ-3.4
func TestGitHub_Issue_Read_TS_02_9(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/comments") {
			if r.Header.Get("X-Fail-Comments") == "1" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Write([]byte(`[{"body": "first comment", "user": {"login": "bob"}, "html_url": "https://cmt/1"}]`))
			return
		}
		w.Write([]byte(`{
			"number": 42,
			"title": "PR issue",
			"pull_request": {"url": "https://api.github.com/repos/o/r/pulls/42"}
		}`))
	}))
	defer srv.Close()

	c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"})
	thread, err := c.ReadIssue(context.Background(), IssueRef{Repo: Repo{Owner: "o", Name: "r"}, Number: 42})
	if err != nil {
		t.Fatalf("unexpected error on ReadIssue: %v", err)
	}
	if !thread.Issue.IsPR {
		t.Errorf("expected IsPR true, got false")
	}
	if len(thread.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(thread.Comments))
	}
	if thread.Comments[0].Body != "first comment" {
		t.Errorf("expected comment body 'first comment', got %s", thread.Comments[0].Body)
	}

	// Test partial comment failure resilience
	cFail := newTestGitHubClient(Options{
		BaseURL: srv.URL,
		Token:   "tok",
		HTTPClient: &http.Client{
			Transport: &testHeaderRoundTripper{rt: http.DefaultTransport, name: "X-Fail-Comments", val: "1"},
		},
	})
	threadFail, errFail := cFail.ReadIssue(context.Background(), IssueRef{Repo: Repo{Owner: "o", Name: "r"}, Number: 42})
	if errFail != nil {
		t.Fatalf("expected nil error on ReadIssue with comment failure, got: %v", errFail)
	}
	if threadFail.Issue.Number != 42 {
		t.Errorf("expected issue number 42, got %d", threadFail.Issue.Number)
	}
	if threadFail.CommentsErr == nil {
		t.Errorf("expected non-nil CommentsErr, got nil")
	}
}

// TestGitHub_Issue_ReadUnauthenticatedNotFound_TS_02_10 verifies TS-02-10:
// ReadIssue wraps ErrNotFound on 404 with private issue guidance for unauthenticated callers.
// Verifies: 02-REQ-3.5
func TestGitHub_Issue_ReadUnauthenticatedNotFound_TS_02_10(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message": "Not Found"}`))
	}))
	defer srv.Close()

	unauth := newTestGitHubClient(Options{BaseURL: srv.URL})
	_, err := unauth.ReadIssue(context.Background(), IssueRef{Repo: Repo{Owner: "o", Name: "r"}, Number: 999})
	if !IsNotFound(err) {
		t.Errorf("expected IsNotFound(err) == true, got %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "private") {
		t.Errorf("expected error to contain 'private', got %s", err.Error())
	}
}

// TestGitHub_Issue_UpdateAndClose_TS_02_11 verifies TS-02-11:
// UpdateIssue patches issue fields and CloseIssue adds optional comment before closing.
// Verifies: 02-REQ-3.6, 02-REQ-3.7
func TestGitHub_Issue_UpdateAndClose_TS_02_11(t *testing.T) {
	var (
		mu    sync.Mutex
		calls []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/comments") {
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"html_url": "https://cmt/1"}`))
			return
		}
		if r.Method == "PATCH" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.Write([]byte(fmt.Sprintf(`{"number": 5, "title": "%v", "state": "%v"}`, body["title"], body["state"])))
			return
		}
	}))
	defer srv.Close()

	c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"})
	ctx := context.Background()
	ref := IssueRef{Repo: Repo{Owner: "o", Name: "r"}, Number: 5}

	upd, errUpd := c.UpdateIssue(ctx, ref, UpdateIssueRequest{Title: "New Title"})
	if errUpd != nil {
		t.Fatalf("unexpected error on UpdateIssue: %v", errUpd)
	}
	if upd.Title != "New Title" {
		t.Errorf("expected title 'New Title', got %s", upd.Title)
	}

	errClose := c.CloseIssue(ctx, ref, "Closing as completed")
	if errClose != nil {
		t.Fatalf("unexpected error on CloseIssue: %v", errClose)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) < 3 {
		t.Fatalf("expected at least 3 calls, got %v", calls)
	}
	if calls[1] != "POST /repos/o/r/issues/5/comments" {
		t.Errorf("expected call[1] to be comment POST, got %s", calls[1])
	}
	if calls[2] != "PATCH /repos/o/r/issues/5" {
		t.Errorf("expected call[2] to be issue PATCH, got %s", calls[2])
	}
}

// TestGitHub_Issue_List_TS_02_12 verifies TS-02-12:
// ListIssues paginates issue results, applies query filters, excludes pull requests, and flags incomplete results.
// Verifies: 02-REQ-3.8
func TestGitHub_Issue_List_TS_02_12(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != "open" {
			t.Errorf("expected state open, got %s", q.Get("state"))
		}
		if q.Get("labels") != "bug,p1" {
			t.Errorf("expected labels bug,p1, got %s", q.Get("labels"))
		}
		if q.Get("assignee") != "alice" {
			t.Errorf("expected assignee alice, got %s", q.Get("assignee"))
		}
		if q.Get("sort") != "updated" {
			t.Errorf("expected sort updated, got %s", q.Get("sort"))
		}
		if q.Get("direction") != "desc" {
			t.Errorf("expected direction desc, got %s", q.Get("direction"))
		}
		w.Write([]byte(`[
			{"number": 1, "title": "Genuine issue 1"},
			{"number": 2, "title": "Pull Request 2", "pull_request": {}},
			{"number": 3, "title": "Genuine issue 3"}
		]`))
	}))
	defer srv.Close()

	c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"})
	list, err := c.ListIssues(context.Background(), Repo{Owner: "o", Name: "r"}, IssueFilter{
		State: "open", Labels: []string{"bug", "p1"}, Assignee: "alice", Sort: "updated", Direction: "desc", Limit: 2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(list.Issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(list.Issues))
	}
	if list.Issues[0].Number != 1 {
		t.Errorf("expected issue 1, got %d", list.Issues[0].Number)
	}
	if list.Issues[1].Number != 3 {
		t.Errorf("expected issue 3, got %d", list.Issues[1].Number)
	}
	if list.Incomplete {
		t.Errorf("expected Incomplete false, got true")
	}
}

// TestGitHub_Comment_Operations_TS_02_13 verifies TS-02-13:
// AddComment posts comment and returns URL, and ListComments paginates up to 500 cap with truncation flag.
// Verifies: 02-REQ-4.1, 02-REQ-4.2
func TestGitHub_Comment_Operations_TS_02_13(t *testing.T) {
	makeCommentPageJSON := func(n int) []byte {
		var items []map[string]any
		for i := 0; i < n; i++ {
			items = append(items, map[string]any{
				"id":   i + 1,
				"body": fmt.Sprintf("comment %d", i+1),
				"user": map[string]any{"login": "user"},
			})
		}
		b, _ := json.Marshal(items)
		return b
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["body"] != "test comment" {
				t.Errorf("expected body 'test comment', got %s", body["body"])
			}
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"html_url": "https://github.com/o/r/issues/1#issuecomment-1"}`))
			return
		}
		page := r.URL.Query().Get("page")
		if page == "5" {
			w.Write(makeCommentPageJSON(100))
			return
		}
		w.Write(makeCommentPageJSON(100))
	}))
	defer srv.Close()

	c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"})
	ref := IssueRef{Repo: Repo{Owner: "o", Name: "r"}, Number: 1}

	url, err := c.AddComment(context.Background(), ref, "test comment")
	if err != nil {
		t.Fatalf("unexpected error on AddComment: %v", err)
	}
	if url != "https://github.com/o/r/issues/1#issuecomment-1" {
		t.Errorf("expected html_url 'https://github.com/o/r/issues/1#issuecomment-1', got %s", url)
	}

	cmts, errList := c.ListComments(context.Background(), ref)
	if errList != nil {
		t.Fatalf("unexpected error on ListComments: %v", errList)
	}
	if len(cmts.Comments) != 500 {
		t.Errorf("expected 500 comments, got %d", len(cmts.Comments))
	}
	if !cmts.Truncated {
		t.Errorf("expected Truncated true, got false")
	}
}

// TestGitHub_Label_Add_TS_02_14 verifies TS-02-14:
// AddLabels appends labels on issues and skips network requests for empty label slices.
// Verifies: 02-REQ-4.3, 02-REQ-4.4
func TestGitHub_Label_Add_TS_02_14(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/repos/o/r/issues/10/labels" {
			t.Errorf("expected /repos/o/r/issues/10/labels, got %s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		lbls, ok := body["labels"].([]any)
		if !ok || len(lbls) != 2 {
			t.Errorf("expected 2 labels, got %v", body["labels"])
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"name": "bug"}, {"name": "ui"}]`))
	}))
	defer srv.Close()

	c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"})
	ref := IssueRef{Repo: Repo{Owner: "o", Name: "r"}, Number: 10}

	// Empty labels: should not touch server
	if err := c.AddLabels(context.Background(), ref, []string{}); err != nil {
		t.Errorf("expected nil error on empty labels, got %v", err)
	}
	if called {
		t.Errorf("expected server not to be called on empty labels")
	}

	// Non-empty labels: calls server
	if err := c.AddLabels(context.Background(), ref, []string{"bug", "ui"}); err != nil {
		t.Errorf("expected nil error on non-empty labels, got %v", err)
	}
	if !called {
		t.Errorf("expected server to be called on non-empty labels")
	}
}

// TestGitHub_Label_Idempotent_TS_02_15 verifies TS-02-15:
// RemoveLabel and CreateLabel perform idempotent label mutations on GitHub.
// Verifies: 02-REQ-4.5, 02-REQ-4.6
func TestGitHub_Label_Idempotent_TS_02_15(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			if r.URL.EscapedPath() != "/repos/o/r/issues/10/labels/special%20label" {
				t.Errorf("expected /repos/o/r/issues/10/labels/special%%20label, got %s", r.URL.EscapedPath())
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == "POST" {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["color"] != "ff0000" {
				t.Errorf("expected color 'ff0000', got %s", body["color"])
			}
			w.WriteHeader(http.StatusUnprocessableEntity)
			w.Write([]byte(`{"message": "Validation Failed", "errors": [{"resource": "Label", "code": "already_exists", "field": "name"}]}`))
			return
		}
	}))
	defer srv.Close()

	c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"})
	ref := IssueRef{Repo: Repo{Owner: "o", Name: "r"}, Number: 10}

	errRem := c.RemoveLabel(context.Background(), ref, "special label")
	if errRem != nil {
		t.Errorf("expected nil error on RemoveLabel 404, got %v", errRem)
	}

	errCreate := c.CreateLabel(context.Background(), Repo{Owner: "o", Name: "r"}, Label{
		Name: "special label", Color: "#ff0000", Description: "desc",
	})
	if errCreate != nil {
		t.Errorf("expected nil error on CreateLabel 422 already_exists, got %v", errCreate)
	}
}
