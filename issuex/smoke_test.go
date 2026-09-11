package issuex_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/issuex"
)

// TestSmoke_NoOpClientSilentIssueCreation_TS_01_30 verifies TS-01-30:
// No-Op client initialization and silent issue creation execution path.
// Verifies: 01-PATH-1
// Given: a caller instantiating a client with NoOp enabled
// When: the caller initializes NoOpClient and executes an issue creation workflow
// Then:
// - NewWithOptions returns an active NoOpClient
// - Authenticated() returns false
// - CreateIssue executes silently returning a zero Issue and nil error
func TestSmoke_NoOpClientSilentIssueCreation_TS_01_30(t *testing.T) {
	// Step 1: caller invokes NewWithOptions with Options{NoOp: true}
	// Step 2: client factory instantiates and returns a NoOpClient instance
	client, err := issuex.NewWithOptions(issuex.Options{NoOp: true})
	if err != nil {
		t.Fatalf("expected NewWithOptions(NoOp: true) to succeed, got error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil Client from NewWithOptions")
	}

	// Verify the concrete type is *NoOpClient
	if _, ok := client.(*issuex.NoOpClient); !ok {
		t.Errorf("expected client to be of type *NoOpClient, got %T", client)
	}

	// Step 3: caller invokes Authenticated() on the returned client
	// Step 4: no-op client returns false indicating unauthenticated mode
	if client.Authenticated() {
		t.Errorf("expected Authenticated() to return false, got true")
	}

	// Step 5: caller invokes CreateIssue with target repo and CreateIssueRequest data
	ctx := context.Background()
	targetRepo := issuex.Repo{Owner: "agent-fox-dev", Name: "agentfox"}
	req := issuex.CreateIssueRequest{
		Title:  "Integration test issue",
		Body:   "Verifying silent creation in NoOpClient",
		Labels: []string{"smoke", "test"},
	}
	issue, err := client.CreateIssue(ctx, targetRepo, req)

	// Step 6: no-op client completes silently and returns a zero-value Issue struct with nil error
	if err != nil {
		t.Fatalf("expected CreateIssue to return nil error, got: %v", err)
	}
	if !reflect.DeepEqual(issue, issuex.Issue{}) {
		t.Errorf("expected zero-value Issue, got: %+v", issue)
	}
}

// TestSmoke_GitRemoteAndIssueURLParsing_TS_01_31 verifies TS-01-31:
// Git remote and web issue URL parsing to domain models execution path.
// Verifies: 01-PATH-2
// Given: valid git remote URLs and web issue URLs
// When: the caller parses git remotes and issue URLs and formats their identifiers
// Then:
// - ParseRemote returns a valid Repo model with host, owner, and project name
// - ParseIssueURL returns a valid IssueRef model indicating issue or PR status
// - IssueRef.String() and IssueRef.URL() produce canonical formatted references
func TestSmoke_GitRemoteAndIssueURLParsing_TS_01_31(t *testing.T) {
	// Subcase A: GitHub repository, issue, and pull request
	t.Run("GitHub", func(t *testing.T) {
		// Step 1: caller provides a git remote URL string and a web issue URL string
		remoteURL := "git@github.com:agent-fox-dev/agentfox.git"
		issueWebURL := "https://github.com/agent-fox-dev/agentfox/issues/42"
		prWebURL := "https://github.com/agent-fox-dev/agentfox/pull/101"

		// Step 2: remote parser parses the remote URL into a Repo struct containing host, owner, and name
		repo, ok := issuex.ParseRemote(remoteURL)
		if !ok {
			t.Fatalf("ParseRemote(%q) failed unexpectedly", remoteURL)
		}
		expectedRepo := issuex.Repo{
			Host:  "github.com",
			Owner: "agent-fox-dev",
			Name:  "agentfox",
		}
		if repo != expectedRepo {
			t.Errorf("ParseRemote returned %+v, want %+v", repo, expectedRepo)
		}
		if !repo.Valid() {
			t.Errorf("expected repo.Valid() to be true for %+v", repo)
		}

		// Step 3: issue URL parser parses the web issue URL into an IssueRef struct containing Repo, issue number, and pull request flag
		issueRef, ok := issuex.ParseIssueURL(issueWebURL)
		if !ok {
			t.Fatalf("ParseIssueURL(%q) failed unexpectedly", issueWebURL)
		}
		if issueRef.Repo != expectedRepo {
			t.Errorf("issueRef.Repo = %+v, want %+v", issueRef.Repo, expectedRepo)
		}
		if issueRef.Number != 42 {
			t.Errorf("issueRef.Number = %d, want 42", issueRef.Number)
		}
		if issueRef.IsPullRequest {
			t.Errorf("issueRef.IsPullRequest = true, want false")
		}

		// Step 4 & 5: caller invokes String() and URL() on the parsed IssueRef to format identifiers
		if got := issueRef.String(); got != "agent-fox-dev/agentfox#42" {
			t.Errorf("issueRef.String() = %q, want %q", got, "agent-fox-dev/agentfox#42")
		}
		if got := issueRef.URL(); got != "https://github.com/agent-fox-dev/agentfox/issues/42" {
			t.Errorf("issueRef.URL() = %q, want %q", got, "https://github.com/agent-fox-dev/agentfox/issues/42")
		}

		// Also verify pull request parsing and formatting
		prRef, ok := issuex.ParseIssueURL(prWebURL)
		if !ok {
			t.Fatalf("ParseIssueURL(%q) failed unexpectedly", prWebURL)
		}
		if !prRef.IsPullRequest {
			t.Errorf("prRef.IsPullRequest = false, want true")
		}
		if prRef.Number != 101 {
			t.Errorf("prRef.Number = %d, want 101", prRef.Number)
		}
		if got := prRef.String(); got != "agent-fox-dev/agentfox#101" {
			t.Errorf("prRef.String() = %q, want %q", got, "agent-fox-dev/agentfox#101")
		}
		if got := prRef.URL(); got != "https://github.com/agent-fox-dev/agentfox/pull/101" {
			t.Errorf("prRef.URL() = %q, want %q", got, "https://github.com/agent-fox-dev/agentfox/pull/101")
		}
	})

	// Subcase B: GitLab repository (with nested group path), issue, and merge request
	t.Run("GitLab", func(t *testing.T) {
		// Step 1: caller provides a git remote URL string and a web issue URL string
		remoteURL := "https://gitlab.com/group/subgroup/project.git"
		issueWebURL := "https://gitlab.com/group/subgroup/project/-/issues/7"
		mrWebURL := "https://gitlab.com/group/subgroup/project/-/merge_requests/88"

		// Step 2: remote parser parses the remote URL into a Repo struct containing host, owner, and name
		repo, ok := issuex.ParseRemote(remoteURL)
		if !ok {
			t.Fatalf("ParseRemote(%q) failed unexpectedly", remoteURL)
		}
		expectedRepo := issuex.Repo{
			Host:  "gitlab.com",
			Owner: "group/subgroup",
			Name:  "project",
		}
		if repo != expectedRepo {
			t.Errorf("ParseRemote returned %+v, want %+v", repo, expectedRepo)
		}
		if !repo.Valid() {
			t.Errorf("expected repo.Valid() to be true for %+v", repo)
		}

		// Step 3: issue URL parser parses the web issue URL into an IssueRef struct containing Repo, issue number, and pull request flag
		issueRef, ok := issuex.ParseIssueURL(issueWebURL)
		if !ok {
			t.Fatalf("ParseIssueURL(%q) failed unexpectedly", issueWebURL)
		}
		if issueRef.Repo != expectedRepo {
			t.Errorf("issueRef.Repo = %+v, want %+v", issueRef.Repo, expectedRepo)
		}
		if issueRef.Number != 7 {
			t.Errorf("issueRef.Number = %d, want 7", issueRef.Number)
		}
		if issueRef.IsPullRequest {
			t.Errorf("issueRef.IsPullRequest = true, want false")
		}

		// Step 4 & 5: caller invokes String() and URL() on the parsed IssueRef to format identifiers
		if got := issueRef.String(); got != "group/subgroup/project#7" {
			t.Errorf("issueRef.String() = %q, want %q", got, "group/subgroup/project#7")
		}
		if got := issueRef.URL(); got != "https://gitlab.com/group/subgroup/project/-/issues/7" {
			t.Errorf("issueRef.URL() = %q, want %q", got, "https://gitlab.com/group/subgroup/project/-/issues/7")
		}

		// Also verify merge request parsing and formatting
		mrRef, ok := issuex.ParseIssueURL(mrWebURL)
		if !ok {
			t.Fatalf("ParseIssueURL(%q) failed unexpectedly", mrWebURL)
		}
		if !mrRef.IsPullRequest {
			t.Errorf("mrRef.IsPullRequest = false, want true")
		}
		if mrRef.Number != 88 {
			t.Errorf("mrRef.Number = %d, want 88", mrRef.Number)
		}
		if got := mrRef.String(); got != "group/subgroup/project#88" {
			t.Errorf("mrRef.String() = %q, want %q", got, "group/subgroup/project#88")
		}
		if got := mrRef.URL(); got != "https://gitlab.com/group/subgroup/project/-/merge_requests/88" {
			t.Errorf("mrRef.URL() = %q, want %q", got, "https://gitlab.com/group/subgroup/project/-/merge_requests/88")
		}
	})
}

// TestSmoke_RateLimitErrorBackoffCalculation_TS_01_32 verifies TS-01-32:
// Rate-limit HTTP error inspection and backoff duration calculation execution path.
// Verifies: 01-PATH-3
// Given: an HTTP 429 response carrying a Retry-After header
// When: the caller receives a 429 response and inspects the rate limit status
// Then:
// - the transport builds an HTTPError preserving response headers
// - IsRateLimited reports true for the error
// - RetryAfter returns the backoff duration plus a 1-second safety buffer
func TestSmoke_RateLimitErrorBackoffCalculation_TS_01_32(t *testing.T) {
	// Step 1: caller receives an HTTP response with status 429 and Retry-After header
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Status:     "429 Too Many Requests",
		Header: http.Header{
			"Retry-After": []string{"30"},
		},
		Request: &http.Request{
			Method: "GET",
			URL:    &url.URL{Path: "/api/v1/repos/agent-fox-dev/agentfox/issues"},
		},
	}

	// Step 2: transport parses the response status and headers into an HTTPError struct
	httpErr := issuex.HTTPErrorFromResponse(resp)
	if httpErr == nil {
		t.Fatal("expected HTTPErrorFromResponse to return non-nil *HTTPError")
	}
	if httpErr.Status != http.StatusTooManyRequests {
		t.Errorf("httpErr.Status = %d, want %d", httpErr.Status, http.StatusTooManyRequests)
	}
	if httpErr.Method != "GET" {
		t.Errorf("httpErr.Method = %q, want GET", httpErr.Method)
	}
	if httpErr.Path != "/api/v1/repos/agent-fox-dev/agentfox/issues" {
		t.Errorf("httpErr.Path = %q, want /api/v1/repos/agent-fox-dev/agentfox/issues", httpErr.Path)
	}
	if httpErr.RetryAfterHeader != "30" {
		t.Errorf("httpErr.RetryAfterHeader = %q, want 30", httpErr.RetryAfterHeader)
	}

	// Step 3: caller invokes IsRateLimited(err) and err.RetryAfter() to inspect the error
	// Step 4: error classifier reports true for IsRateLimited and returns the backoff duration with a 1-second buffer
	if !issuex.IsRateLimited(httpErr) {
		t.Errorf("expected IsRateLimited(httpErr) to be true")
	}

	wait, ok := httpErr.RetryAfter()
	if !ok {
		t.Fatalf("expected RetryAfter() to return ok = true")
	}
	expectedWait := 31 * time.Second // 30s + 1s safety buffer
	if wait != expectedWait {
		t.Errorf("RetryAfter() duration = %v, want %v", wait, expectedWait)
	}

	// Verify integration through HandleResponse
	err := issuex.HandleResponse(resp, false)
	if err == nil {
		t.Fatal("expected HandleResponse to return non-nil error")
	}
	if !issuex.IsRateLimited(err) {
		t.Errorf("expected IsRateLimited(err) to be true for error returned by HandleResponse")
	}
	if !errors.Is(err, issuex.ErrRateLimited) {
		t.Errorf("expected errors.Is(err, ErrRateLimited) to be true")
	}
	var extractedHTTPError *issuex.HTTPError
	if !errors.As(err, &extractedHTTPError) {
		t.Fatalf("expected errors.As to extract *HTTPError from wrapped error")
	}
	extractedWait, extractedOK := extractedHTTPError.RetryAfter()
	if !extractedOK || extractedWait != expectedWait {
		t.Errorf("extracted HTTPError.RetryAfter() = (%v, %v), want (%v, true)", extractedWait, extractedOK, expectedWait)
	}

	// Verify secondary rate limiting trigger: 403 Forbidden with X-RateLimit-Remaining: 0 and X-RateLimit-Reset
	t.Run("SecondaryRateLimit_403WithReset", func(t *testing.T) {
		resetEpoch := time.Now().Add(45 * time.Second).Unix()
		resp403 := &http.Response{
			StatusCode: http.StatusForbidden,
			Status:     "403 Forbidden",
			Header: http.Header{
				"X-RateLimit-Remaining": []string{"0"},
				"X-RateLimit-Reset":     []string{fmt.Sprintf("%d", resetEpoch)},
			},
			Request: &http.Request{
				Method: "POST",
				URL:    &url.URL{Path: "/api/v1/repos/agent-fox-dev/agentfox/issues"},
			},
		}

		err403 := issuex.HandleResponse(resp403, true)
		if err403 == nil {
			t.Fatal("expected HandleResponse for 403 rate-limit to return error")
		}
		if !issuex.IsRateLimited(err403) {
			t.Errorf("expected IsRateLimited(err403) to be true")
		}
		var he *issuex.HTTPError
		if !errors.As(err403, &he) {
			t.Fatalf("expected errors.As to extract *HTTPError from err403")
		}
		d, ok := he.RetryAfter()
		if !ok {
			t.Fatalf("expected RetryAfter() on 403 with reset header to return ok = true")
		}
		// Duration should be roughly 45s + 1s buffer (~46s)
		if d < 40*time.Second || d > 50*time.Second {
			t.Errorf("unexpected RetryAfter() duration on 403: %v", d)
		}
	})
}

// TestSmoke_GitHubIssueLifecycle_TS_02_25 verifies TS-02-25:
// GitHub issue creation, reading with comments, and closing with comment end-to-end flow.
// Verifies: 02-PATH-1
// Given: an authenticated GitHub client connected to a mock GitHub REST server
// When: creating an issue, reading the issue thread with comments, and closing the issue with a comment
// Then: the issue is created with expected number, read back with comment thread, and closed with the closing comment persisted
func TestSmoke_GitHubIssueLifecycle_TS_02_25(t *testing.T) {
	type issueRecord struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		State   string `json:"state"`
		HTMLURL string `json:"html_url"`
		User    struct {
			Login string `json:"login"`
		} `json:"user"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
	}

	type commentRecord struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		CreatedAt time.Time `json:"created_at"`
		HTMLURL   string    `json:"html_url"`
	}

	var mu sync.Mutex
	issues := make(map[int]*issueRecord)
	comments := make(map[int][]*commentRecord)
	nextCommentID := 1

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		w.Header().Set("Content-Type", "application/json")

		// Create issue: POST /repos/agentfox/agent-fox/issues
		if r.Method == http.MethodPost && r.URL.Path == "/repos/agentfox/agent-fox/issues" {
			var req struct {
				Title  string   `json:"title"`
				Body   string   `json:"body"`
				Labels []string `json:"labels"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			var lbls []struct {
				Name string `json:"name"`
			}
			for _, l := range req.Labels {
				lbls = append(lbls, struct {
					Name string `json:"name"`
				}{Name: l})
			}
			rec := &issueRecord{
				Number:  42,
				Title:   req.Title,
				Body:    req.Body,
				State:   "open",
				HTMLURL: "https://github.com/agentfox/agent-fox/issues/42",
				User: struct {
					Login string `json:"login"`
				}{Login: "smoke-author"},
				Labels:    lbls,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
			issues[42] = rec
			comments[42] = []*commentRecord{
				{
					ID:   1,
					Body: "Initial diagnostic comment",
					User: struct {
						Login string `json:"login"`
					}{Login: "smoke-author"},
					CreatedAt: time.Now(),
					HTMLURL:   "https://github.com/agentfox/agent-fox/issues/42#issuecomment-1",
				},
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(rec)
			return
		}

		// Read issue: GET /repos/agentfox/agent-fox/issues/42
		if r.Method == http.MethodGet && r.URL.Path == "/repos/agentfox/agent-fox/issues/42" {
			rec, ok := issues[42]
			if !ok {
				http.Error(w, `{"message": "Not Found"}`, http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(rec)
			return
		}

		// Patch issue (Close): PATCH /repos/agentfox/agent-fox/issues/42
		if r.Method == http.MethodPatch && r.URL.Path == "/repos/agentfox/agent-fox/issues/42" {
			rec, ok := issues[42]
			if !ok {
				http.Error(w, `{"message": "Not Found"}`, http.StatusNotFound)
				return
			}
			var req struct {
				State string `json:"state"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if req.State != "" {
				rec.State = req.State
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(rec)
			return
		}

		// List comments: GET /repos/agentfox/agent-fox/issues/42/comments
		if r.Method == http.MethodGet && r.URL.Path == "/repos/agentfox/agent-fox/issues/42/comments" {
			cmts := comments[42]
			if cmts == nil {
				cmts = []*commentRecord{}
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(cmts)
			return
		}

		// Add comment: POST /repos/agentfox/agent-fox/issues/42/comments
		if r.Method == http.MethodPost && r.URL.Path == "/repos/agentfox/agent-fox/issues/42/comments" {
			var req struct {
				Body string `json:"body"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			nextCommentID++
			c := &commentRecord{
				ID:   nextCommentID,
				Body: req.Body,
				User: struct {
					Login string `json:"login"`
				}{Login: "smoke-author"},
				CreatedAt: time.Now(),
				HTMLURL:   fmt.Sprintf("https://github.com/agentfox/agent-fox/issues/42#issuecomment-%d", nextCommentID),
			}
			comments[42] = append(comments[42], c)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(c)
			return
		}

		http.NotFound(w, r)
	}))
	defer srv.Close()

	ctx := context.Background()

	// 1. Construct authenticated client targeting mock server
	c, err := issuex.NewGitHub(issuex.Options{
		BaseURL: srv.URL,
		Token:   "ghp_mocktoken",
	})
	if err != nil {
		t.Fatalf("NewGitHub failed: %v", err)
	}
	if !c.Authenticated() {
		t.Fatal("expected authenticated client")
	}

	repo := issuex.Repo{Owner: "agentfox", Name: "agent-fox"}

	// 2. Create issue
	issue, err := c.CreateIssue(ctx, repo, issuex.CreateIssueRequest{
		Title:  "Smoke issue",
		Body:   "Issue body",
		Labels: []string{"smoke"},
	})
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	if issue.Number != 42 {
		t.Errorf("issue.Number = %d, want 42", issue.Number)
	}
	if issue.Title != "Smoke issue" {
		t.Errorf("issue.Title = %q, want 'Smoke issue'", issue.Title)
	}
	if issue.Body != "Issue body" {
		t.Errorf("issue.Body = %q, want 'Issue body'", issue.Body)
	}
	if issue.State != "open" {
		t.Errorf("issue.State = %q, want 'open'", issue.State)
	}
	if issue.IsPR {
		t.Errorf("expected issue.IsPR to be false")
	}

	ref := issuex.IssueRef{Repo: repo, Number: issue.Number}

	// 3. Read issue with comments
	thread, err := c.ReadIssue(ctx, ref)
	if err != nil {
		t.Fatalf("ReadIssue failed: %v", err)
	}
	if thread.Issue.Number != issue.Number {
		t.Errorf("thread.Issue.Number = %d, want %d", thread.Issue.Number, issue.Number)
	}
	if thread.CommentsErr != nil {
		t.Errorf("unexpected CommentsErr: %v", thread.CommentsErr)
	}
	if len(thread.Comments) != 1 {
		t.Errorf("len(thread.Comments) = %d, want 1", len(thread.Comments))
	}

	// 4. Close issue with comment
	err = c.CloseIssue(ctx, ref, "Completed smoke test")
	if err != nil {
		t.Fatalf("CloseIssue failed: %v", err)
	}

	// Verify the closing comment was added and the state updated to closed
	threadClosed, err := c.ReadIssue(ctx, ref)
	if err != nil {
		t.Fatalf("ReadIssue after close failed: %v", err)
	}
	if threadClosed.Issue.State != "closed" {
		t.Errorf("threadClosed.Issue.State = %q, want 'closed'", threadClosed.Issue.State)
	}
	if len(threadClosed.Comments) != 2 {
		t.Fatalf("len(threadClosed.Comments) = %d, want 2", len(threadClosed.Comments))
	}
	if threadClosed.Comments[1].Body != "Completed smoke test" {
		t.Errorf("unexpected closing comment: %q", threadClosed.Comments[1].Body)
	}
}

// TestSmoke_PullRequestWorkflowAndDefaultMerge_TS_02_26 verifies TS-02-26:
// Pull request check runs inspection, review commenting, and repository-policy merge end-to-end flow.
// Verifies: 02-PATH-2
// Given: an authenticated GitHub client configured via NewWithOptions targeting a repository with merge commit policy
// When: reading a pull request, inspecting CI check runs, submitting a review comment, and executing repository default merge
// Then:
// - PR metadata and head SHA are resolved
// - CI check runs are returned
// - review comment is submitted
// - PR is merged successfully using repository allowed strategy
func TestSmoke_PullRequestWorkflowAndDefaultMerge_TS_02_26(t *testing.T) {
	var mu sync.Mutex
	var reviewCommentReceived string
	var mergeMethodReceived string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		w.Header().Set("Content-Type", "application/json")

		// 1. Read PR: GET /repos/agentfox/agent-fox/pulls/10
		if r.Method == http.MethodGet && r.URL.Path == "/repos/agentfox/agent-fox/pulls/10" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"number": 10,
				"title": "Smoke pull request",
				"body": "PR description",
				"state": "open",
				"html_url": "https://github.com/agentfox/agent-fox/pull/10",
				"draft": false,
				"merged": false,
				"user": {"login": "smoke-developer"},
				"head": {"ref": "feat/smoke-test", "sha": "c0ffee1234567890abcdef1234567890abcdef12"},
				"base": {"ref": "main", "sha": "1234567890abcdef1234567890abcdef12345678"},
				"created_at": "2026-09-10T20:00:00Z",
				"updated_at": "2026-09-10T20:00:00Z"
			}`))
			return
		}

		// 2. Get CI checks: GET /repos/agentfox/agent-fox/commits/{head_sha}/check-runs
		if r.Method == http.MethodGet && r.URL.Path == "/repos/agentfox/agent-fox/commits/c0ffee1234567890abcdef1234567890abcdef12/check-runs" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"total_count": 1,
				"check_runs": [
					{
						"name": "continuous-integration",
						"status": "completed",
						"conclusion": "success",
						"html_url": "https://github.com/agentfox/agent-fox/runs/99",
						"output": {
							"title": "Build & Lint Passed",
							"summary": "All tests passed cleanly"
						}
					}
				]
			}`))
			return
		}

		// 3. Post review comment: POST /repos/agentfox/agent-fox/pulls/10/reviews
		if r.Method == http.MethodPost && r.URL.Path == "/repos/agentfox/agent-fox/pulls/10/reviews" {
			var req struct {
				Body  string `json:"body"`
				Event string `json:"event"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			reviewCommentReceived = req.Body
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"id": 888,
				"user": {"login": "smoke-reviewer"},
				"state": "COMMENTED",
				"body": "` + req.Body + `",
				"submitted_at": "2026-09-10T20:10:00Z"
			}`))
			return
		}

		// 4. Repository metadata (for MergeMethodDefault): GET /repos/agentfox/agent-fox
		if r.Method == http.MethodGet && r.URL.Path == "/repos/agentfox/agent-fox" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"full_name": "agentfox/agent-fox",
				"default_branch": "main",
				"private": false,
				"archived": false,
				"permissions": {"push": true, "pull": true, "admin": true},
				"allow_merge_commit": true,
				"allow_squash_merge": true,
				"allow_rebase_merge": true
			}`))
			return
		}

		// 5. Merge PR: PUT /repos/agentfox/agent-fox/pulls/10/merge
		if r.Method == http.MethodPut && r.URL.Path == "/repos/agentfox/agent-fox/pulls/10/merge" {
			var req struct {
				MergeMethod string `json:"merge_method"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mergeMethodReceived = req.MergeMethod
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"sha": "merge_commit_sha_c0ffee",
				"merged": true,
				"message": "Pull Request successfully merged"
			}`))
			return
		}

		http.NotFound(w, r)
	}))
	defer srv.Close()

	ctx := context.Background()

	// 1. Invoke NewWithOptions targeting GitHub repository
	c, err := issuex.NewWithOptions(issuex.Options{
		Repo:      issuex.Repo{Owner: "agentfox", Name: "agent-fox"},
		RemoteURL: "https://github.com/agentfox/agent-fox.git",
		Token:     "ghp_mocktoken",
		BaseURL:   srv.URL,
	})
	if err != nil {
		t.Fatalf("NewWithOptions failed: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil Client from NewWithOptions")
	}

	ref := issuex.IssueRef{Repo: issuex.Repo{Owner: "agentfox", Name: "agent-fox"}, Number: 10}

	// 2. Read PR and assert HeadSHA is resolved
	pr, err := c.ReadPullRequest(ctx, ref)
	if err != nil {
		t.Fatalf("ReadPullRequest failed: %v", err)
	}
	if pr.HeadSHA == "" {
		t.Fatalf("expected non-empty pr.HeadSHA")
	}
	if pr.HeadSHA != "c0ffee1234567890abcdef1234567890abcdef12" {
		t.Errorf("pr.HeadSHA = %q, want 'c0ffee1234567890abcdef1234567890abcdef12'", pr.HeadSHA)
	}

	// 3. Get CI check runs
	checks, err := c.GetCIChecks(ctx, ref)
	if err != nil {
		t.Fatalf("GetCIChecks failed: %v", err)
	}
	if len(checks) == 0 {
		t.Fatalf("expected at least 1 check run, got 0")
	}
	if checks[0].Name != "continuous-integration" {
		t.Errorf("checks[0].Name = %q, want 'continuous-integration'", checks[0].Name)
	}
	if checks[0].Conclusion != "success" {
		t.Errorf("checks[0].Conclusion = %q, want 'success'", checks[0].Conclusion)
	}

	// 4. Post review comment
	err = c.PostReviewComment(ctx, ref, "Smoke review approved")
	if err != nil {
		t.Fatalf("PostReviewComment failed: %v", err)
	}
	if reviewCommentReceived != "Smoke review approved" {
		t.Errorf("reviewCommentReceived = %q, want 'Smoke review approved'", reviewCommentReceived)
	}

	// 5. Merge PR with repository default merge policy
	res, err := c.MergePullRequest(ctx, ref, issuex.MergeOptions{Method: issuex.MergeMethodDefault})
	if err != nil {
		t.Fatalf("MergePullRequest failed: %v", err)
	}
	if !res.Merged {
		t.Errorf("expected res.Merged == true")
	}
	if res.SHA != "merge_commit_sha_c0ffee" {
		t.Errorf("res.SHA = %q, want 'merge_commit_sha_c0ffee'", res.SHA)
	}
	if mergeMethodReceived != "merge" {
		t.Errorf("mergeMethodReceived = %q, want 'merge'", mergeMethodReceived)
	}
}

// TestSmoke_RateLimitBackoffAndRetry_TS_02_27 verifies TS-02-27:
// Rate-limit backoff and retry recovery on GitHub REST request end-to-end flow.
// Verifies: 02-PATH-3
// Given: an authenticated GitHub client with an injected zero-delay sleep function and mock server simulating 403 quota exhaustion on first attempt
// When: calling GetRepository on the client encountering an initial rate limit response with 2 second backoff
// Then: injected sleep function is invoked with 2 seconds, the request is retried once, and the repository metadata is successfully returned
func TestSmoke_RateLimitBackoffAndRetry_TS_02_27(t *testing.T) {
	var slept time.Duration
	var sleepCalls int

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := atomic.AddInt32(&attempts, 1)
		if att == 1 {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message": "API rate limit exceeded"}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"full_name": "agentfox/agent-fox",
			"default_branch": "main",
			"private": false,
			"archived": false,
			"permissions": {"push": true, "pull": true, "admin": true}
		}`))
	}))
	defer srv.Close()

	c, err := issuex.NewGitHub(issuex.Options{
		BaseURL: srv.URL,
		Token:   "ghp_mocktoken",
	})
	if err != nil {
		t.Fatalf("NewGitHub failed: %v", err)
	}

	issuex.SetGitHubSleep(c, func(d time.Duration) {
		slept = d
		sleepCalls++
	})

	ctx := context.Background()
	repo, err := c.GetRepository(ctx, issuex.Repo{Owner: "agentfox", Name: "agent-fox"})
	if err != nil {
		t.Fatalf("GetRepository failed: %v", err)
	}
	if repo.FullName != "agentfox/agent-fox" {
		t.Errorf("repo.FullName = %q, want 'agentfox/agent-fox'", repo.FullName)
	}
	if sleepCalls != 1 {
		t.Errorf("expected 1 sleep call, got %d", sleepCalls)
	}
	if slept != 2*time.Second {
		t.Errorf("slept = %v, want %v", slept, 2*time.Second)
	}
	if atomic.LoadInt32(&attempts) != 2 {
		t.Errorf("expected 2 server attempts, got %d", atomic.LoadInt32(&attempts))
	}
}
