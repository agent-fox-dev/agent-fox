package issuex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TS-03-18 (unit): CreatePullRequest formats draft title prefix and rejects unauthenticated callers with ErrNoToken
// Verifies: 03-REQ-5.1, 03-REQ-5.7
func TestGitLab_MR_CreatePullRequest_TS_03_18(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		title, _ := req["title"].(string)
		if !strings.HasPrefix(title, "Draft: ") {
			t.Errorf("expected title to start with 'Draft: ', got %q", title)
		}
		if req["source_branch"] != "feature" {
			t.Errorf("expected source_branch 'feature', got %v", req["source_branch"])
		}
		if req["target_branch"] != "main" {
			t.Errorf("expected target_branch 'main', got %v", req["target_branch"])
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{
			"iid": 10,
			"title": "Draft: new feature",
			"description": "body",
			"state": "opened",
			"draft": true,
			"source_branch": "feature",
			"target_branch": "main",
			"sha": "abc1234",
			"author": {"username": "dev"},
			"web_url": "https://gitlab.com/org/repo/-/merge_requests/10"
		}`))
	}))
	defer srv.Close()

	c := newTestGitLabClient(Options{BaseURL: srv.URL, Token: "tok"})
	pr, err := c.CreatePullRequest(context.Background(), Repo{Owner: "org", Name: "repo"}, CreatePullRequestRequest{
		Title: "new feature",
		Body:  "body",
		Head:  "feature",
		Base:  "main",
		Draft: true,
	})
	if err != nil {
		t.Fatalf("CreatePullRequest failed: %v", err)
	}
	if pr.Number != 10 {
		t.Errorf("expected Number 10, got %d", pr.Number)
	}
	if !pr.Draft {
		t.Errorf("expected Draft true, got false")
	}
	if pr.Merged {
		t.Errorf("expected Merged false, got true")
	}

	cUnauth := newTestGitLabClient(Options{BaseURL: srv.URL})
	ref := IssueRef{Repo: Repo{Owner: "org", Name: "repo"}, Number: 10}
	_, errCreate := cUnauth.CreatePullRequest(context.Background(), ref.Repo, CreatePullRequestRequest{})
	if !IsNoToken(errCreate) {
		t.Errorf("expected IsNoToken for CreatePullRequest, got %v", errCreate)
	}

	errClose := cUnauth.ClosePullRequest(context.Background(), ref)
	if !IsNoToken(errClose) {
		t.Errorf("expected IsNoToken for ClosePullRequest, got %v", errClose)
	}
}

// TS-03-19 (unit): ReadPullRequest normalizes MR state, parses branch metadata, and annotates unauthenticated 404
// Verifies: 03-REQ-5.2, 03-REQ-5.3
func TestGitLab_MR_ReadPullRequest_TS_03_19(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/merge_requests/15") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"iid": 15,
				"title": "MR 15",
				"description": "desc",
				"state": "opened",
				"draft": false,
				"work_in_progress": true,
				"source_branch": "patch",
				"target_branch": "main",
				"sha": "def5678",
				"author": {"username": "alice"},
				"web_url": "https://gitlab.com/org/repo/-/merge_requests/15"
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message": "404 Not Found"}`))
	}))
	defer srv.Close()

	c := newTestGitLabClient(Options{BaseURL: srv.URL, Token: "tok"})
	pr, err := c.ReadPullRequest(context.Background(), IssueRef{Repo: Repo{Owner: "org", Name: "repo"}, Number: 15})
	if err != nil {
		t.Fatalf("ReadPullRequest failed: %v", err)
	}
	if pr.Number != 15 {
		t.Errorf("expected Number 15, got %d", pr.Number)
	}
	if pr.State != "open" {
		t.Errorf("expected State 'open', got %q", pr.State)
	}
	if !pr.Draft {
		t.Errorf("expected Draft true (from work_in_progress), got false")
	}
	if pr.HeadBranch != "patch" {
		t.Errorf("expected HeadBranch 'patch', got %q", pr.HeadBranch)
	}
	if pr.BaseBranch != "main" {
		t.Errorf("expected BaseBranch 'main', got %q", pr.BaseBranch)
	}
	if pr.HeadSHA != "def5678" {
		t.Errorf("expected HeadSHA 'def5678', got %q", pr.HeadSHA)
	}

	cUnauth := newTestGitLabClient(Options{BaseURL: srv.URL})
	_, errNotFound := cUnauth.ReadPullRequest(context.Background(), IssueRef{Repo: Repo{Owner: "org", Name: "repo"}, Number: 99})
	if !IsNotFound(errNotFound) {
		t.Errorf("expected IsNotFound true, got %v", errNotFound)
	}
	if !strings.Contains(errNotFound.Error(), "GITLAB_TOKEN") {
		t.Errorf("expected error to mention GITLAB_TOKEN, got %v", errNotFound)
	}
}

// TS-03-20 (unit): ReadChangedFiles parses file changes and computes additions and deletions from unified diff chunks
// Verifies: 03-REQ-5.4
func TestGitLab_MR_ReadChangedFiles_TS_03_20(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"changes": [
				{
					"old_path": "old.go",
					"new_path": "new.go",
					"new_file": true,
					"deleted_file": false,
					"diff": "@@ -0,0 +1,5 @@\n+line1\n+line2\n+line3"
				},
				{
					"old_path": "deleted.go",
					"new_path": "deleted.go",
					"new_file": false,
					"deleted_file": true,
					"diff": "@@ -1,3 +0,0 @@\n-line1\n-line2"
				},
				{
					"old_path": "mod.go",
					"new_path": "mod.go",
					"new_file": false,
					"deleted_file": false,
					"diff": "--- a/mod.go\n+++ b/mod.go\n@@ -1,2 +1,3 @@\n-old\n+new1\n+new2"
				}
			]
		}`))
	}))
	defer srv.Close()

	c := newTestGitLabClient(Options{BaseURL: srv.URL, Token: "tok"})
	files, err := c.ReadChangedFiles(context.Background(), IssueRef{Repo: Repo{Owner: "org", Name: "repo"}, Number: 1})
	if err != nil {
		t.Fatalf("ReadChangedFiles failed: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}
	if files[0].Status != "added" || files[0].Additions != 3 || files[0].Deletions != 0 {
		t.Errorf("files[0] expected added (+3,-0), got status=%s (+%d,-%d)", files[0].Status, files[0].Additions, files[0].Deletions)
	}
	if files[1].Status != "deleted" || files[1].Additions != 0 || files[1].Deletions != 2 {
		t.Errorf("files[1] expected deleted (+0,-2), got status=%s (+%d,-%d)", files[1].Status, files[1].Additions, files[1].Deletions)
	}
	if files[2].Status != "modified" || files[2].Additions != 2 || files[2].Deletions != 1 {
		t.Errorf("files[2] expected modified (+2,-1), got status=%s (+%d,-%d)", files[2].Status, files[2].Additions, files[2].Deletions)
	}
}

// TS-03-21 (unit): GetPRState retrieves merge request state and ClosePullRequest closes the merge request
// Verifies: 03-REQ-5.5, 03-REQ-5.6
func TestGitLab_MR_GetPRState_ClosePullRequest_TS_03_21(t *testing.T) {
	var closedMR bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/merge_requests/20") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"iid": 20, "state": "merged", "sha": "headsha123"}`))
			return
		}
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/merge_requests/20") {
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("failed to decode body: %v", err)
			}
			if req["state_event"] == "close" {
				closedMR = true
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"iid": 20, "state": "closed"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := newTestGitLabClient(Options{BaseURL: srv.URL, Token: "tok"})
	ref := IssueRef{Repo: Repo{Owner: "org", Name: "repo"}, Number: 20}
	state, errState := c.GetPRState(context.Background(), ref)
	if errState != nil {
		t.Fatalf("GetPRState failed: %v", errState)
	}
	if state.Number != 20 {
		t.Errorf("expected Number 20, got %d", state.Number)
	}
	if state.State != "merged" {
		t.Errorf("expected State 'merged', got %q", state.State)
	}
	if !state.Merged {
		t.Errorf("expected Merged true, got false")
	}
	if state.HeadSHA != "headsha123" {
		t.Errorf("expected HeadSHA 'headsha123', got %q", state.HeadSHA)
	}

	errClose := c.ClosePullRequest(context.Background(), ref)
	if errClose != nil {
		t.Fatalf("ClosePullRequest failed: %v", errClose)
	}
	if !closedMR {
		t.Errorf("expected ClosePullRequest to send state_event=close")
	}
}

// TS-03-22 (unit): GetCIChecks maps latest pipeline jobs to CheckRun records and returns empty slice when no pipelines exist
// Verifies: 03-REQ-6.1, 03-REQ-6.2
func TestGitLab_CI_GetCIChecks_TS_03_22(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/pipelines") {
			if strings.Contains(r.URL.Path, "merge_requests/1/") {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`[{"id": 101, "status": "running"}]`))
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[]`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/pipelines/101/jobs") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[
				{"id": 1, "name": "lint", "stage": "test", "status": "success", "web_url": "https://gitlab.com/job/1"},
				{"id": 2, "name": "test", "stage": "test", "status": "running", "web_url": "https://gitlab.com/job/2"},
				{"id": 3, "name": "deploy", "stage": "deploy", "status": "failed", "failure_reason": "script_failure", "web_url": "https://gitlab.com/job/3"}
			]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := newTestGitLabClient(Options{BaseURL: srv.URL, Token: "tok"})
	repo := Repo{Owner: "org", Name: "repo"}
	checks, err1 := c.GetCIChecks(context.Background(), IssueRef{Repo: repo, Number: 1})
	if err1 != nil {
		t.Fatalf("GetCIChecks failed: %v", err1)
	}
	if len(checks) != 3 {
		t.Fatalf("expected 3 checks, got %d", len(checks))
	}
	if checks[0].Name != "lint" || checks[0].Status != "completed" || checks[0].Conclusion != "success" {
		t.Errorf("checks[0] mismatch: %+v", checks[0])
	}
	if checks[1].Name != "test" || checks[1].Status != "in_progress" {
		t.Errorf("checks[1] mismatch: %+v", checks[1])
	}
	if checks[2].Name != "deploy" || checks[2].Status != "completed" || checks[2].Conclusion != "failure" {
		t.Errorf("checks[2] mismatch: %+v", checks[2])
	}
	if !strings.Contains(checks[2].Summary, "script_failure") {
		t.Errorf("checks[2].Summary expected to contain 'script_failure', got %q", checks[2].Summary)
	}

	emptyChecks, err2 := c.GetCIChecks(context.Background(), IssueRef{Repo: repo, Number: 2})
	if err2 != nil {
		t.Fatalf("GetCIChecks empty pipeline failed: %v", err2)
	}
	if len(emptyChecks) != 0 {
		t.Errorf("expected 0 checks for empty pipeline, got %d", len(emptyChecks))
	}
}

// TS-03-23 (unit): GetPRReviews aggregates approvals and non-system notes chronologically and PostReviewComment records a review note
// Verifies: 03-REQ-6.3, 03-REQ-6.4
func TestGitLab_Review_GetPRReviews_PostReviewComment_TS_03_23(t *testing.T) {
	var postedReview string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/approvals") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"approved_by": [{"user": {"username": "reviewer1"}}]}`))
			return
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/notes") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[
				{"id": 1, "body": "Looks good", "system": false, "author": {"username": "reviewer2"}, "created_at": "2025-01-02T00:00:00Z"},
				{"id": 2, "body": "audit event", "system": true, "author": {"username": "bot"}, "created_at": "2025-01-01T00:00:00Z"}
			]`))
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/notes") {
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("failed to decode body: %v", err)
			}
			postedReview = req["body"].(string)
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id": 3}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := newTestGitLabClient(Options{BaseURL: srv.URL, Token: "tok"})
	ref := IssueRef{Repo: Repo{Owner: "org", Name: "repo"}, Number: 1}
	reviews, err := c.GetPRReviews(context.Background(), ref)
	if err != nil {
		t.Fatalf("GetPRReviews failed: %v", err)
	}
	if len(reviews) != 2 {
		t.Fatalf("expected 2 reviews, got %d", len(reviews))
	}
	if reviews[0].State != "approved" || reviews[0].Author.Login != "reviewer1" {
		t.Errorf("reviews[0] expected approved by reviewer1, got %+v", reviews[0])
	}
	if reviews[1].State != "commented" || reviews[1].Author.Login != "reviewer2" {
		t.Errorf("reviews[1] expected commented by reviewer2, got %+v", reviews[1])
	}

	errPost := c.PostReviewComment(context.Background(), ref, "LGTM")
	if errPost != nil {
		t.Fatalf("PostReviewComment failed: %v", errPost)
	}
	if postedReview != "LGTM" {
		t.Errorf("expected posted review 'LGTM', got %q", postedReview)
	}
}

// TS-03-24 (unit): MergePullRequest executes merge with method strategy, wraps conflicts in ErrConflict, and rejects unauthenticated callers
// Verifies: 03-REQ-6.5, 03-REQ-6.6, 03-REQ-6.7
func TestGitLab_Merge_MergePullRequest_TS_03_24(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode body: %v", err)
		}
		if req["commit_message"] == "conflict" {
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"message": "Branch cannot be merged"}`))
			return
		}
		if req["commit_message"] == "not_acceptable" {
			w.WriteHeader(http.StatusNotAcceptable)
			w.Write([]byte(`{"message": "406 Not Acceptable"}`))
			return
		}
		if req["commit_message"] == "method_not_allowed" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			w.Write([]byte(`{"message": "405 Method Not Allowed"}`))
			return
		}
		if req["commit_message"] == "merge_default" {
			if _, hasSquash := req["squash"]; hasSquash {
				t.Errorf("expected squash omitted for MergeMethodDefault, got %v", req["squash"])
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"sha": "mergeshaDefault", "merge_commit_sha": "mergeshaDefault"}`))
			return
		}
		if req["commit_message"] == "merge_explicit" {
			if req["squash"] != false {
				t.Errorf("expected squash false for MergeMethodMerge, got %v", req["squash"])
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"sha": "mergeshaExplicit", "merge_commit_sha": "mergeshaExplicit"}`))
			return
		}
		if req["squash"] != true {
			t.Errorf("expected squash true, got %v", req["squash"])
		}
		if req["sha"] != "head123" {
			t.Errorf("expected sha 'head123', got %v", req["sha"])
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"sha": "mergesha999", "merge_commit_sha": "mergesha999"}`))
	}))
	defer srv.Close()

	c := newTestGitLabClient(Options{BaseURL: srv.URL, Token: "tok"})
	ref := IssueRef{Repo: Repo{Owner: "org", Name: "repo"}, Number: 1}

	res, err := c.MergePullRequest(context.Background(), ref, MergeOptions{
		Method:        MergeMethodSquash,
		CommitMessage: "merge commit",
		SHA:           "head123",
	})
	if err != nil {
		t.Fatalf("MergePullRequest failed: %v", err)
	}
	if !res.Merged {
		t.Errorf("expected res.Merged true, got false")
	}
	if res.SHA != "mergesha999" {
		t.Errorf("expected res.SHA 'mergesha999', got %q", res.SHA)
	}

	resDef, errDef := c.MergePullRequest(context.Background(), ref, MergeOptions{
		Method:        MergeMethodDefault,
		CommitMessage: "merge_default",
	})
	if errDef != nil {
		t.Fatalf("MergePullRequest default failed: %v", errDef)
	}
	if !resDef.Merged {
		t.Errorf("expected resDef.Merged true")
	}

	resMerge, errMerge := c.MergePullRequest(context.Background(), ref, MergeOptions{
		Method:        MergeMethodMerge,
		CommitMessage: "merge_explicit",
	})
	if errMerge != nil {
		t.Fatalf("MergePullRequest merge failed: %v", errMerge)
	}
	if !resMerge.Merged {
		t.Errorf("expected resMerge.Merged true")
	}

	_, errConflict := c.MergePullRequest(context.Background(), ref, MergeOptions{CommitMessage: "conflict"})
	if !IsConflict(errConflict) {
		t.Errorf("expected IsConflict true for 409 conflict, got %v", errConflict)
	}

	_, err405 := c.MergePullRequest(context.Background(), ref, MergeOptions{CommitMessage: "method_not_allowed"})
	if !IsConflict(err405) {
		t.Errorf("expected IsConflict true for 405 method not allowed, got %v", err405)
	}

	_, err406 := c.MergePullRequest(context.Background(), ref, MergeOptions{CommitMessage: "not_acceptable"})
	if !IsConflict(err406) {
		t.Errorf("expected IsConflict true for 406 not acceptable, got %v", err406)
	}

	cUnauth := newTestGitLabClient(Options{})
	_, errUnauth := cUnauth.MergePullRequest(context.Background(), ref, MergeOptions{})
	if !IsNoToken(errUnauth) {
		t.Errorf("expected IsNoToken for unauth MergePullRequest, got %v", errUnauth)
	}
	errPostUnauth := cUnauth.PostReviewComment(context.Background(), ref, "body")
	if !IsNoToken(errPostUnauth) {
		t.Errorf("expected IsNoToken for unauth PostReviewComment, got %v", errPostUnauth)
	}
}
