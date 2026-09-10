package issuex_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
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
