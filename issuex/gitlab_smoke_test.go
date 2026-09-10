package issuex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/issuex"
)

type smokeNoteAuthor struct {
	Username string `json:"username"`
}

type smokeNoteItem struct {
	ID        int64           `json:"id"`
	Body      string          `json:"body"`
	System    bool            `json:"system"`
	CreatedAt string          `json:"created_at"`
	WebURL    string          `json:"web_url"`
	Author    smokeNoteAuthor `json:"author"`
}

// TS-03-29 (smoke): Full issue lifecycle creates an issue, reads it with discussion comments, and closes it with an explanatory comment
// Verifies: 03-PATH-1, 03-REQ-3.1, 03-REQ-3.7
func TestSmoke_GitLabIssueLifecycle_TS_03_29(t *testing.T) {
	t.Parallel()

	var (
		issueCreated bool
		issueClosed  bool
		closingNote  string
		notes        []smokeNoteItem
		nextNoteID   int64 = 100
		createdTitle string
		createdBody  string
	)

	// Add an initial discussion note and a system audit note to test system note filtering
	notes = append(notes,
		smokeNoteItem{
			ID:        1,
			Body:      "System note event",
			System:    true,
			CreatedAt: "2025-01-01T10:00:00Z",
			Author:    smokeNoteAuthor{Username: "system_user"},
		},
		smokeNoteItem{
			ID:        2,
			Body:      "First user comment",
			System:    false,
			CreatedAt: "2025-01-01T10:05:00Z",
			WebURL:    "https://gitlab.example.com/group/subgroup/project/-/issues/42#note_2",
			Author:    smokeNoteAuthor{Username: "reporter"},
		},
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Verify Authorization token header
		if r.Header.Get("PRIVATE-TOKEN") != "glpat-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message": "401 Unauthorized"}`))
			return
		}

		escapedPath := r.URL.EscapedPath()

		// Step 4: POST /projects/{project_path}/issues -> Create issue
		if r.Method == http.MethodPost && strings.Contains(escapedPath, "/issues") && !strings.Contains(escapedPath, "/notes") {
			bodyBytes, _ := io.ReadAll(r.Body)
			var payload struct {
				Title       string `json:"title"`
				Description string `json:"description"`
				Labels      string `json:"labels"`
			}
			_ = json.Unmarshal(bodyBytes, &payload)
			createdTitle = payload.Title
			createdBody = payload.Description
			issueCreated = true

			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{
				"id": 1001,
				"iid": 42,
				"title": "` + payload.Title + `",
				"description": "` + payload.Description + `",
				"state": "opened",
				"web_url": "https://gitlab.example.com/group/subgroup/project/-/issues/42",
				"author": {"username": "alice"},
				"labels": ["bug"],
				"created_at": "2025-01-01T10:00:00Z",
				"updated_at": "2025-01-01T10:00:00Z"
			}`))
			return
		}

		// Step 6 & 8: Notes endpoint: GET (list comments) and POST (add comment)
		if strings.Contains(escapedPath, "/issues/42/notes") {
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode(notes)
				return
			}
			if r.Method == http.MethodPost {
				bodyBytes, _ := io.ReadAll(r.Body)
				var payload struct {
					Body string `json:"body"`
				}
				_ = json.Unmarshal(bodyBytes, &payload)
				closingNote = payload.Body
				nextNoteID++
				newNote := smokeNoteItem{
					ID:        nextNoteID,
					Body:      payload.Body,
					System:    false,
					CreatedAt: "2025-01-01T10:10:00Z",
					WebURL:    fmt.Sprintf("https://gitlab.example.com/group/subgroup/project/-/issues/42#note_%d", nextNoteID),
					Author:    smokeNoteAuthor{Username: "alice"},
				}
				notes = append(notes, newNote)
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(newNote)
				return
			}
		}

		// Step 6 & 8: Issue details endpoint: GET (read issue) and PUT (update/close issue)
		if strings.Contains(escapedPath, "/issues/42") {
			if r.Method == http.MethodGet {
				stateStr := "opened"
				if issueClosed {
					stateStr = "closed"
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(fmt.Sprintf(`{
					"id": 1001,
					"iid": 42,
					"title": "%s",
					"description": "%s",
					"state": "%s",
					"web_url": "https://gitlab.example.com/group/subgroup/project/-/issues/42",
					"author": {"username": "alice"},
					"labels": ["bug"],
					"created_at": "2025-01-01T10:00:00Z",
					"updated_at": "2025-01-01T10:05:00Z"
				}`, createdTitle, createdBody, stateStr)))
				return
			}
			if r.Method == http.MethodPut {
				bodyBytes, _ := io.ReadAll(r.Body)
				var payload struct {
					StateEvent string `json:"state_event"`
				}
				_ = json.Unmarshal(bodyBytes, &payload)
				if payload.StateEvent == "close" {
					issueClosed = true
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{
					"id": 1001,
					"iid": 42,
					"title": "Smoke Issue",
					"description": "Initial body",
					"state": "closed",
					"web_url": "https://gitlab.example.com/group/subgroup/project/-/issues/42",
					"author": {"username": "alice"},
					"labels": ["bug"],
					"created_at": "2025-01-01T10:00:00Z",
					"updated_at": "2025-01-01T10:10:00Z",
					"closed_at": "2025-01-01T10:10:00Z"
				}`))
				return
			}
		}

		http.NotFound(w, r)
	}))
	defer srv.Close()

	ctx := context.Background()

	// Step 1: caller invokes NewGitLab with authentication token and project repository target
	// Step 2: client factory constructs and returns an authenticated GitLab client with normalized API base URL
	c, err := issuex.NewGitLab(issuex.Options{
		BaseURL: srv.URL,
		Token:   "glpat-secret",
	})
	if err != nil {
		t.Fatalf("NewGitLab failed: %v", err)
	}
	if !c.Authenticated() {
		t.Fatal("expected Authenticated() == true")
	}

	repo := issuex.Repo{Owner: "group/subgroup", Name: "project"}

	// Step 3: caller calls CreateIssue with title, description, and labels
	// Step 4: GitLab client executes POST /projects/{project_path}/issues and returns created Issue with normalized open state
	iss, err := c.CreateIssue(ctx, repo, issuex.CreateIssueRequest{
		Title:  "Smoke Issue",
		Body:   "Initial body",
		Labels: []string{"bug"},
	})
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	if iss.Number != 42 {
		t.Errorf("iss.Number = %d, want 42", iss.Number)
	}
	if iss.State != "open" {
		t.Errorf("iss.State = %q, want 'open'", iss.State)
	}
	if iss.Title != "Smoke Issue" {
		t.Errorf("iss.Title = %q, want 'Smoke Issue'", iss.Title)
	}
	if iss.Body != "Initial body" {
		t.Errorf("iss.Body = %q, want 'Initial body'", iss.Body)
	}
	if iss.IsPR {
		t.Errorf("iss.IsPR = true, want false")
	}
	if !issueCreated {
		t.Error("expected issue creation request to be sent to server")
	}

	// Step 5: caller calls ReadIssue for the created issue reference
	// Step 6: GitLab client executes GET /projects/{project_path}/issues/{number} and fetches issue comments via ListComments filtering system notes
	ref := issuex.IssueRef{Repo: repo, Number: iss.Number}
	thread, err := c.ReadIssue(ctx, ref)
	if err != nil {
		t.Fatalf("ReadIssue failed: %v", err)
	}
	if thread.Issue.State != "open" {
		t.Errorf("thread.Issue.State = %q, want 'open'", thread.Issue.State)
	}
	if thread.Issue.Number != 42 {
		t.Errorf("thread.Issue.Number = %d, want 42", thread.Issue.Number)
	}
	if thread.CommentsErr != nil {
		t.Errorf("unexpected thread.CommentsErr: %v", thread.CommentsErr)
	}
	// System note must be filtered out; only 1 user comment
	if len(thread.Comments) != 1 {
		t.Fatalf("len(thread.Comments) = %d, want 1 (system note filtered out)", len(thread.Comments))
	}
	if thread.Comments[0].Body != "First user comment" {
		t.Errorf("thread.Comments[0].Body = %q, want 'First user comment'", thread.Comments[0].Body)
	}

	// Step 7: caller calls CloseIssue with an explanatory closing comment
	// Step 8: GitLab client executes AddComment to post the closing comment and PUTs the issue with state_event close
	err = c.CloseIssue(ctx, ref, "closing comment")
	if err != nil {
		t.Fatalf("CloseIssue failed: %v", err)
	}
	if !issueClosed {
		t.Error("expected issue to be closed on server")
	}
	if closingNote != "closing comment" {
		t.Errorf("closingNote = %q, want 'closing comment'", closingNote)
	}
}

// TS-03-30 (smoke): Full merge request lifecycle inspects CI pipeline jobs, posts review comment, and executes repository-default merge
// Verifies: 03-PATH-2, 03-REQ-6.1, 03-REQ-6.5
func TestSmoke_GitLabMergeRequestLifecycle_TS_03_30(t *testing.T) {
	t.Parallel()

	var (
		reviewNotePosted string
		mergeExecuted    bool
		receivedSquash   *bool
		receivedSHA      string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Header.Get("PRIVATE-TOKEN") != "glpat-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message": "401 Unauthorized"}`))
			return
		}

		escapedPath := r.URL.EscapedPath()

		// Step 2: ReadPullRequest -> GET /projects/{project_path}/merge_requests/10
		if r.Method == http.MethodGet && strings.HasSuffix(escapedPath, "/merge_requests/10") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"id": 501,
				"iid": 10,
				"title": "Feature branch MR",
				"description": "Implements core feature",
				"state": "opened",
				"web_url": "https://gitlab.example.com/group/subgroup/project/-/merge_requests/10",
				"source_branch": "feature/branch",
				"target_branch": "main",
				"sha": "0123456789abcdef0123456789abcdef01234567",
				"draft": false,
				"work_in_progress": false,
				"author": {"username": "bob"},
				"created_at": "2025-01-01T12:00:00Z",
				"updated_at": "2025-01-01T12:30:00Z"
			}`))
			return
		}

		// Step 4a: GetCIChecks -> GET /projects/{project_path}/merge_requests/10/pipelines?per_page=1
		if r.Method == http.MethodGet && strings.Contains(escapedPath, "/merge_requests/10/pipelines") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[
				{
					"id": 999,
					"iid": 55,
					"status": "success",
					"sha": "0123456789abcdef0123456789abcdef01234567",
					"ref": "feature/branch",
					"web_url": "https://gitlab.example.com/group/subgroup/project/-/pipelines/999"
				}
			]`))
			return
		}

		// Step 4b: GetCIChecks -> GET /projects/{project_path}/pipelines/999/jobs?per_page=100
		if r.Method == http.MethodGet && strings.Contains(escapedPath, "/pipelines/999/jobs") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[
				{
					"id": 1001,
					"name": "lint",
					"stage": "test",
					"status": "success",
					"web_url": "https://gitlab.example.com/group/subgroup/project/-/jobs/1001"
				},
				{
					"id": 1002,
					"name": "build",
					"stage": "build",
					"status": "running",
					"web_url": "https://gitlab.example.com/group/subgroup/project/-/jobs/1002"
				}
			]`))
			return
		}

		// Step 6: PostReviewComment -> POST /projects/{project_path}/merge_requests/10/notes
		if r.Method == http.MethodPost && strings.Contains(escapedPath, "/merge_requests/10/notes") {
			bodyBytes, _ := io.ReadAll(r.Body)
			var payload struct {
				Body string `json:"body"`
			}
			_ = json.Unmarshal(bodyBytes, &payload)
			reviewNotePosted = payload.Body

			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{
				"id": 3001,
				"body": "` + payload.Body + `",
				"system": false,
				"web_url": "https://gitlab.example.com/group/subgroup/project/-/merge_requests/10#note_3001",
				"author": {"username": "reviewer"},
				"created_at": "2025-01-01T13:00:00Z"
			}`))
			return
		}

		// Step 8: MergePullRequest -> PUT /projects/{project_path}/merge_requests/10/merge
		if r.Method == http.MethodPut && strings.Contains(escapedPath, "/merge_requests/10/merge") {
			bodyBytes, _ := io.ReadAll(r.Body)
			var payload struct {
				Squash *bool  `json:"squash"`
				SHA    string `json:"sha"`
			}
			_ = json.Unmarshal(bodyBytes, &payload)
			receivedSquash = payload.Squash
			receivedSHA = payload.SHA
			mergeExecuted = true

			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"id": 501,
				"iid": 10,
				"title": "Feature branch MR",
				"state": "merged",
				"merge_commit_sha": "fedcba9876543210fedcba9876543210fedcba98"
			}`))
			return
		}

		http.NotFound(w, r)
	}))
	defer srv.Close()

	ctx := context.Background()

	// Step 1: caller invokes NewWithOptions for a GitLab repository and calls ReadPullRequest
	c, err := issuex.NewWithOptions(issuex.Options{
		Repo:      issuex.Repo{Owner: "group/subgroup", Name: "project"},
		RemoteURL: "https://gitlab.com/group/subgroup/project.git",
		BaseURL:   srv.URL,
		Token:     "glpat-tok",
	})
	if err != nil {
		t.Fatalf("NewWithOptions failed: %v", err)
	}

	ref := issuex.IssueRef{Repo: issuex.Repo{Owner: "group/subgroup", Name: "project"}, Number: 10}

	// Step 2: GitLab client executes GET /projects/{project_path}/merge_requests/{number} and returns MR details with head commit SHA
	pr, err := c.ReadPullRequest(ctx, ref)
	if err != nil {
		t.Fatalf("ReadPullRequest failed: %v", err)
	}
	if pr.HeadSHA != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("pr.HeadSHA = %q, want '0123456789abcdef0123456789abcdef01234567'", pr.HeadSHA)
	}
	if pr.State != "open" {
		t.Errorf("pr.State = %q, want 'open'", pr.State)
	}
	if pr.Number != 10 {
		t.Errorf("pr.Number = %d, want 10", pr.Number)
	}

	// Step 3: caller calls GetCIChecks to verify MR CI pipeline job statuses
	// Step 4: GitLab client queries latest pipeline and its jobs, returning mapped CheckRun records
	checks, err := c.GetCIChecks(ctx, ref)
	if err != nil {
		t.Fatalf("GetCIChecks failed: %v", err)
	}
	if len(checks) != 2 {
		t.Fatalf("len(checks) = %d, want 2", len(checks))
	}
	// Check job 1: lint -> success
	if checks[0].Name != "lint" || checks[0].Status != "completed" || checks[0].Conclusion != "success" {
		t.Errorf("checks[0] = %+v, want completed/success", checks[0])
	}
	// Check job 2: build -> running (in_progress)
	if checks[1].Name != "build" || checks[1].Status != "in_progress" || checks[1].Conclusion != "" {
		t.Errorf("checks[1] = %+v, want in_progress", checks[1])
	}

	// Step 5: caller calls PostReviewComment with approval comments
	// Step 6: GitLab client executes POST /projects/{project_path}/merge_requests/{number}/notes with the comment body
	err = c.PostReviewComment(ctx, ref, "Looks great to me")
	if err != nil {
		t.Fatalf("PostReviewComment failed: %v", err)
	}
	if reviewNotePosted != "Looks great to me" {
		t.Errorf("reviewNotePosted = %q, want 'Looks great to me'", reviewNotePosted)
	}

	// Step 7: caller calls MergePullRequest with MergeMethodDefault
	// Step 8: GitLab client executes PUT /projects/{project_path}/merge_requests/{number}/merge and returns the MergeResult
	res, err := c.MergePullRequest(ctx, ref, issuex.MergeOptions{Method: issuex.MergeMethodDefault})
	if err != nil {
		t.Fatalf("MergePullRequest failed: %v", err)
	}
	if !res.Merged {
		t.Errorf("res.Merged = %v, want true", res.Merged)
	}
	if res.SHA != "fedcba9876543210fedcba9876543210fedcba98" {
		t.Errorf("res.SHA = %q, want 'fedcba9876543210fedcba9876543210fedcba98'", res.SHA)
	}
	if !mergeExecuted {
		t.Error("expected merge to be executed on server")
	}
	if receivedSquash != nil {
		t.Errorf("receivedSquash = %v, want nil for MergeMethodDefault", *receivedSquash)
	}
	_ = receivedSHA
}

// TS-03-31 (smoke): Rate-limited request invokes injected sleep function and transparently succeeds on retry
// Verifies: 03-PATH-3, 03-REQ-7.2
func TestSmoke_GitLabRateLimitBackoffAndRetry_TS_03_31(t *testing.T) {
	t.Parallel()

	var attempts int32
	var slept time.Duration

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := atomic.AddInt32(&attempts, 1)
		if att == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message": "429 Too Many Requests"}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": 123,
			"path_with_namespace": "group/project",
			"default_branch": "main",
			"visibility": "public",
			"archived": false,
			"permissions": {
				"project_access": {"access_level": 30},
				"group_access": null
			}
		}`))
	}))
	defer srv.Close()

	// Step 1: caller calls GetRepository on an authenticated GitLab client
	c, err := issuex.NewGitLab(issuex.Options{
		BaseURL: srv.URL,
		Token:   "glpat-tok",
	})
	if err != nil {
		t.Fatalf("NewGitLab failed: %v", err)
	}

	// Inject sleep function to avoid sleeping for real 2 seconds
	issuex.SetGitLabSleep(c, func(d time.Duration) {
		slept = d
	})

	// Step 2-5: GetRepository encounters HTTP 429, sleeps 2s, retries once, and returns Repository
	ctx := context.Background()
	repo, err := c.GetRepository(ctx, issuex.Repo{Owner: "group", Name: "project"})
	if err != nil {
		t.Fatalf("GetRepository failed: %v", err)
	}
	if slept != 2*time.Second {
		t.Errorf("slept = %v, want %v", slept, 2*time.Second)
	}
	if repo.FullName != "group/project" {
		t.Errorf("repo.FullName = %q, want 'group/project'", repo.FullName)
	}
	if repo.DefaultBranch != "main" {
		t.Errorf("repo.DefaultBranch = %q, want 'main'", repo.DefaultBranch)
	}
	if atomic.LoadInt32(&attempts) != 2 {
		t.Errorf("attempts = %d, want 2", atomic.LoadInt32(&attempts))
	}
}

// TS-03-32 (smoke): Ambiguous host probe detects self-hosted GitLab instance and fetches repository metadata
// Verifies: 03-PATH-4, 03-REQ-1.4, 03-REQ-2.1
func TestSmoke_GitLabAmbiguousHostProbe_TS_03_32(t *testing.T) {
	t.Parallel()

	var (
		versionProbed  bool
		repoPathCalled string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Step 2: client factory executes GET /api/v4/version against the host to detect self-hosted GitLab instance
		if r.Method == http.MethodGet && r.URL.Path == "/api/v4/version" {
			versionProbed = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"version": "16.8.1-ee", "revision": "abcdef"}`))
			return
		}

		// Step 5: GitLab client executes GET /projects/{project_path} using URL-encoded project path
		// Expect selfhosted%2Fsub%2Fapp
		if r.Method == http.MethodGet && strings.Contains(r.URL.RawPath, "selfhosted%2Fsub%2Fapp") ||
			strings.Contains(r.URL.EscapedPath(), "selfhosted%2Fsub%2Fapp") {
			repoPathCalled = r.URL.EscapedPath()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"id": 987,
				"path_with_namespace": "selfhosted/sub/app",
				"default_branch": "main",
				"visibility": "private",
				"archived": false,
				"permissions": {
					"project_access": {"access_level": 40},
					"group_access": null
				}
			}`))
			return
		}

		http.NotFound(w, r)
	}))
	defer srv.Close()

	// Step 1: caller invokes NewWithOptions with an ambiguous host URL and repository target
	// RemoteURL points to srv.URL (which does not contain "github" or "gitlab")
	remoteURL := srv.URL + "/selfhosted/sub/app.git"
	c, err := issuex.NewWithOptions(issuex.Options{
		Repo:      issuex.Repo{Owner: "selfhosted/sub", Name: "app"},
		RemoteURL: remoteURL,
		Token:     "glpat-tok",
	})
	if err != nil {
		t.Fatalf("NewWithOptions failed: %v", err)
	}

	// Step 3: client factory constructs and returns a GitLab client configured for the self-hosted instance BaseURL
	if !c.Authenticated() {
		t.Fatal("expected client to be authenticated")
	}
	if !versionProbed {
		t.Error("expected version probe GET /api/v4/version to be executed")
	}

	// Step 4: caller calls GetRepository to retrieve project metadata
	// Step 5: GitLab client executes GET /projects/{project_path} using URL-encoded project path and returns the Repository struct
	ctx := context.Background()
	repo, err := c.GetRepository(ctx, issuex.Repo{Owner: "selfhosted/sub", Name: "app"})
	if err != nil {
		t.Fatalf("GetRepository failed: %v", err)
	}
	if repo.FullName != "selfhosted/sub/app" {
		t.Errorf("repo.FullName = %q, want 'selfhosted/sub/app'", repo.FullName)
	}
	if !repo.Private {
		t.Errorf("repo.Private = %v, want true", repo.Private)
	}
	if !repo.Permissions.Admin {
		t.Errorf("repo.Permissions.Admin = %v, want true (access_level 40)", repo.Permissions.Admin)
	}
	if repoPathCalled == "" {
		t.Error("expected GET /projects/selfhosted%2Fsub%2Fapp to be called")
	}
}
