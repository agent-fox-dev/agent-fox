package issuex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGitHub_PR_Create_Read_TS_02_16 tests TS-02-16:
// CreatePullRequest creates PR with draft flag and ReadPullRequest parses PR metadata.
// Verifies: 02-REQ-5.1, 02-REQ-5.2
func TestGitHub_PR_Create_Read_TS_02_16(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("failed to decode post body: %v", err)
			}
			if body["draft"] != true {
				t.Errorf("expected draft=true, got %v", body["draft"])
			}
			if body["head"] != "feature" {
				t.Errorf("expected head=feature, got %v", body["head"])
			}
			if body["base"] != "main" {
				t.Errorf("expected base=main, got %v", body["base"])
			}
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{
				"number": 88,
				"title": "PR 88",
				"draft": true,
				"merged": false,
				"head": {"ref": "feature", "sha": "abc123"},
				"base": {"ref": "main"}
			}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"number": 88,
			"title": "PR 88",
			"draft": true,
			"merged": false,
			"head": {"ref": "feature", "sha": "abc123"},
			"base": {"ref": "main"}
		}`))
	}))
	defer srv.Close()

	c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"})
	pr, err := c.CreatePullRequest(context.Background(), Repo{Owner: "o", Name: "r"}, CreatePullRequestRequest{
		Title: "PR 88",
		Head:  "feature",
		Base:  "main",
		Draft: true,
	})
	if err != nil {
		t.Fatalf("CreatePullRequest failed: %v", err)
	}
	if pr.Number != 88 {
		t.Errorf("expected pr.Number == 88, got %d", pr.Number)
	}
	if !pr.Draft {
		t.Errorf("expected pr.Draft == true, got false")
	}
	if pr.Merged {
		t.Errorf("expected pr.Merged == false, got true")
	}

	readPR, errRead := c.ReadPullRequest(context.Background(), IssueRef{Repo: Repo{Owner: "o", Name: "r"}, Number: 88})
	if errRead != nil {
		t.Fatalf("ReadPullRequest failed: %v", errRead)
	}
	if readPR.HeadBranch != "feature" {
		t.Errorf("expected HeadBranch == feature, got %s", readPR.HeadBranch)
	}
	if readPR.HeadSHA != "abc123" {
		t.Errorf("expected HeadSHA == abc123, got %s", readPR.HeadSHA)
	}
	if readPR.BaseBranch != "main" {
		t.Errorf("expected BaseBranch == main, got %s", readPR.BaseBranch)
	}
}

// TestGitHub_PR_Read_UnauthenticatedNotFound_TS_02_17 tests TS-02-17:
// ReadPullRequest wraps ErrNotFound on 404 with private repository guidance for unauthenticated callers.
// Verifies: 02-REQ-5.3
func TestGitHub_PR_Read_UnauthenticatedNotFound_TS_02_17(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message": "Not Found"}`))
	}))
	defer srv.Close()

	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	unauth := newTestGitHubClient(Options{BaseURL: srv.URL})
	_, err := unauth.ReadPullRequest(context.Background(), IssueRef{Repo: Repo{Owner: "o", Name: "r"}, Number: 99})
	if !IsNotFound(err) {
		t.Fatalf("expected IsNotFound(err) == true, got %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "private") {
		t.Errorf("expected error to contain 'private', got %q", err.Error())
	}
}

// TestGitHub_PR_Files_State_Close_TS_02_18 tests TS-02-18:
// ReadChangedFiles normalizes file statuses, GetPRState inspects merge status, and ClosePullRequest closes PR.
// Verifies: 02-REQ-5.4, 02-REQ-5.5, 02-REQ-5.6
func TestGitHub_PR_Files_State_Close_TS_02_18(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/files") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[
				{"filename": "a.go", "status": "added", "additions": 10, "deletions": 0},
				{"filename": "b.go", "status": "modified", "additions": 2, "deletions": 1},
				{"filename": "c.go", "status": "removed", "additions": 0, "deletions": 5}
			]`))
			return
		}
		if r.Method == http.MethodPatch {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("failed to decode patch body: %v", err)
			}
			if body["state"] != "closed" {
				t.Errorf("expected state=closed, got %v", body["state"])
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"state": "closed"}`))
			return
		}
		// GET /pulls/5
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"number": 5, "state": "closed", "merged": true, "head": {"sha": "sha1"}}`))
	}))
	defer srv.Close()

	c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"})
	ref := IssueRef{Repo: Repo{Owner: "o", Name: "r"}, Number: 5}

	files, errFiles := c.ReadChangedFiles(context.Background(), ref)
	if errFiles != nil {
		t.Fatalf("ReadChangedFiles failed: %v", errFiles)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}
	if files[0].Status != "added" {
		t.Errorf("expected files[0].Status == added, got %s", files[0].Status)
	}
	if files[1].Status != "modified" {
		t.Errorf("expected files[1].Status == modified, got %s", files[1].Status)
	}
	if files[2].Status != "deleted" {
		t.Errorf("expected files[2].Status == deleted (mapped from removed), got %s", files[2].Status)
	}

	st, errSt := c.GetPRState(context.Background(), ref)
	if errSt != nil {
		t.Fatalf("GetPRState failed: %v", errSt)
	}
	if st.State != "merged" {
		t.Errorf("expected st.State == merged (merged=true overrides closed), got %s", st.State)
	}
	if !st.Merged {
		t.Errorf("expected st.Merged == true, got false")
	}

	errClose := c.ClosePullRequest(context.Background(), ref)
	if errClose != nil {
		t.Fatalf("ClosePullRequest failed: %v", errClose)
	}
}

// TestGitHub_PR_CI_Reviews_Comment_TS_02_19 tests TS-02-19:
// GetCIChecks retrieves commit check runs, GetPRReviews normalizes reviews, and PostReviewComment submits review.
// Verifies: 02-REQ-6.1, 02-REQ-6.2, 02-REQ-6.3
func TestGitHub_PR_CI_Reviews_Comment_TS_02_19(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/o/r/pulls/7" && r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"number": 7, "head": {"sha": "commit_sha_123"}}`))
			return
		}
		if r.URL.Path == "/repos/o/r/commits/commit_sha_123/check-runs" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"check_runs": [{"name": "ci/test", "status": "completed", "conclusion": "success", "output": {"summary": "Passed"}, "html_url": "https://ci/1"}]}`))
			return
		}
		if r.URL.Path == "/repos/o/r/pulls/7/reviews" && r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"user": {"login": "eve"}, "state": "CHANGES_REQUESTED", "body": "Needs fixes", "submitted_at": "2025-01-01T00:00:00Z"}]`))
			return
		}
		if r.URL.Path == "/repos/o/r/pulls/7/reviews" && r.Method == http.MethodPost {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("failed to decode post body: %v", err)
			}
			if body["event"] != "COMMENT" {
				t.Errorf("expected event=COMMENT, got %v", body["event"])
			}
			if body["body"] != "Looks promising" {
				t.Errorf("expected body='Looks promising', got %v", body["body"])
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
			return
		}
	}))
	defer srv.Close()

	c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"})
	ref := IssueRef{Repo: Repo{Owner: "o", Name: "r"}, Number: 7}

	checks, errChecks := c.GetCIChecks(context.Background(), ref)
	if errChecks != nil {
		t.Fatalf("GetCIChecks failed: %v", errChecks)
	}
	if len(checks) != 1 {
		t.Fatalf("expected 1 check run, got %d", len(checks))
	}
	if checks[0].Name != "ci/test" {
		t.Errorf("expected Name == ci/test, got %s", checks[0].Name)
	}
	if checks[0].Conclusion != "success" {
		t.Errorf("expected Conclusion == success, got %s", checks[0].Conclusion)
	}
	if checks[0].Summary != "Passed" {
		t.Errorf("expected Summary == Passed, got %s", checks[0].Summary)
	}

	reviews, errReviews := c.GetPRReviews(context.Background(), ref)
	if errReviews != nil {
		t.Fatalf("GetPRReviews failed: %v", errReviews)
	}
	if len(reviews) != 1 {
		t.Fatalf("expected 1 review, got %d", len(reviews))
	}
	if reviews[0].State != "changes_requested" {
		t.Errorf("expected State == changes_requested (normalized to lowercase), got %s", reviews[0].State)
	}
	if reviews[0].Author.Login != "eve" {
		t.Errorf("expected Author.Login == eve, got %s", reviews[0].Author.Login)
	}

	errComment := c.PostReviewComment(context.Background(), ref, "Looks promising")
	if errComment != nil {
		t.Fatalf("PostReviewComment failed: %v", errComment)
	}
}

// TestGitHub_PR_Merge_TS_02_20 tests TS-02-20:
// MergePullRequest executes explicit or repository default merge strategies and wraps conflict errors.
// Verifies: 02-REQ-6.4, 02-REQ-6.5, 02-REQ-6.6
func TestGitHub_PR_Merge_TS_02_20(t *testing.T) {
	var mergeMethodSent string
	var returnConflict bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/repos/o/r" {
			// repo settings: merge commit disabled, squash enabled
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"allow_merge_commit": false, "allow_squash_merge": true, "allow_rebase_merge": true}`))
			return
		}
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/merge") {
			if returnConflict {
				w.WriteHeader(http.StatusMethodNotAllowed) // 405
				w.Write([]byte(`{"message": "Pull Request is not mergeable"}`))
				return
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("failed to decode put body: %v", err)
			}
			if m, ok := body["merge_method"].(string); ok {
				mergeMethodSent = m
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"merged": true, "sha": "merged_sha_99", "message": "Pull Request successfully merged"}`))
			return
		}
	}))
	defer srv.Close()

	c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"})
	ref := IssueRef{Repo: Repo{Owner: "o", Name: "r"}, Number: 10}

	// Explicit squash
	res, err := c.MergePullRequest(context.Background(), ref, MergeOptions{Method: MergeMethodSquash})
	if err != nil {
		t.Fatalf("MergePullRequest (explicit squash) failed: %v", err)
	}
	if !res.Merged {
		t.Errorf("expected res.Merged == true, got false")
	}
	if mergeMethodSent != "squash" {
		t.Errorf("expected mergeMethodSent == squash, got %s", mergeMethodSent)
	}

	// Default method: queries repo, selects squash since merge_commit is false
	mergeMethodSent = ""
	resDef, errDef := c.MergePullRequest(context.Background(), ref, MergeOptions{Method: MergeMethodDefault})
	if errDef != nil {
		t.Fatalf("MergePullRequest (default method) failed: %v", errDef)
	}
	if !resDef.Merged {
		t.Errorf("expected resDef.Merged == true, got false")
	}
	if mergeMethodSent != "squash" {
		t.Errorf("expected mergeMethodSent == squash, got %s", mergeMethodSent)
	}

	// Conflict on 405/409
	returnConflict = true
	_, errConflict := c.MergePullRequest(context.Background(), ref, MergeOptions{})
	if !IsConflict(errConflict) {
		t.Fatalf("expected IsConflict(errConflict) == true, got %v", errConflict)
	}
}

// TestGitHub_PR_Unauthenticated verifies 02-REQ-5.7 and 02-REQ-6.7:
// Mutating PR operations reject unauthenticated calls immediately with ErrNoToken.
func TestGitHub_PR_Unauthenticated(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	unauth := newTestGitHubClient(Options{})
	ref := IssueRef{Repo: Repo{Owner: "o", Name: "r"}, Number: 1}

	_, err := unauth.CreatePullRequest(context.Background(), Repo{Owner: "o", Name: "r"}, CreatePullRequestRequest{
		Title: "PR",
		Head:  "feat",
		Base:  "main",
	})
	if !IsNoToken(err) {
		t.Errorf("CreatePullRequest unauthenticated expected ErrNoToken, got %v", err)
	}

	err = unauth.ClosePullRequest(context.Background(), ref)
	if !IsNoToken(err) {
		t.Errorf("ClosePullRequest unauthenticated expected ErrNoToken, got %v", err)
	}

	err = unauth.PostReviewComment(context.Background(), ref, "LGTM")
	if !IsNoToken(err) {
		t.Errorf("PostReviewComment unauthenticated expected ErrNoToken, got %v", err)
	}

	_, err = unauth.MergePullRequest(context.Background(), ref, MergeOptions{})
	if !IsNoToken(err) {
		t.Errorf("MergePullRequest unauthenticated expected ErrNoToken, got %v", err)
	}
}
