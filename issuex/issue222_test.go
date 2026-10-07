package issuex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// Issue #222 (1): a GitLab merge request is read, updated and commented on
// through the merge-request endpoints; issues and MRs have separate iid
// spaces, so the issue endpoint names another thread.
func TestAGitLabMergeRequestRefUsesTheMergeRequestEndpoints(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.EscapedPath())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/notes") {
			if r.Method == http.MethodPost {
				_, _ = w.Write([]byte(`{"id": 1}`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`{"iid": 5, "title": "t", "description": "d", "state": "opened"}`))
	}))
	defer srv.Close()

	c := newTestGitLabClient(Options{BaseURL: srv.URL, Token: "tok"})
	ref := IssueRef{Repo: Repo{Host: "gitlab.com", Owner: "g", Name: "p"}, Number: 5, IsPullRequest: true}
	ctx := context.Background()
	if _, err := c.ReadIssue(ctx, ref); err != nil {
		t.Fatalf("ReadIssue: %v", err)
	}
	if _, err := c.UpdateIssue(ctx, ref, UpdateIssueRequest{Body: "b"}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if _, err := c.AddComment(ctx, ref, "c"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	for _, p := range paths {
		if strings.Contains(p, "/issues/") {
			t.Errorf("a merge-request ref reached the issue endpoint: %s", p)
		}
	}
	if len(paths) == 0 || !strings.Contains(strings.Join(paths, "\n"), "/merge_requests/5") {
		t.Errorf("paths = %v", paths)
	}
}

// Issue #222 (2): a credential goes only to the host its configuration names.
func TestTheTokenGoesOnlyToTheHostItIsConfiguredFor(t *testing.T) {
	for _, tc := range []struct {
		name      string
		env       map[string]string
		host      string
		wantType  ForgeType
		wantBase  string
		wantToken string
	}{
		{"self-hosted GitLab, no API URL", map[string]string{"GITLAB_TOKEN": "gl"},
			"gitlab.acme.com", ForgeTypeGitLab, "https://gitlab.acme.com/api/v4", ""},
		{"self-hosted GitHub, no API URL", map[string]string{"GITHUB_TOKEN": "gh"},
			"github.acme.com", ForgeTypeGitHub, "https://github.acme.com/api/v3", ""},
		{"github.com with a GHES API URL", map[string]string{"GITHUB_TOKEN": "gh", "GITHUB_API_URL": "https://ghe.acme.com/api/v3"},
			"github.com", ForgeTypeGitHub, "https://api.github.com", ""},
		{"github.com with its own API URL", map[string]string{"GITHUB_TOKEN": "gh", "GITHUB_API_URL": "https://api.github.com"},
			"github.com", ForgeTypeGitHub, "https://api.github.com", "gh"},
		{"github.com, no API URL", map[string]string{"GITHUB_TOKEN": "gh"},
			"github.com", ForgeTypeGitHub, "https://api.github.com", "gh"},
		{"GHES named by its API URL", map[string]string{"GITHUB_TOKEN": "gh", "GITHUB_API_URL": "https://ghe.acme.com/api/v3"},
			"ghe.acme.com", ForgeTypeGitHub, "https://ghe.acme.com/api/v3", "gh"},
		{"gitlab.com", map[string]string{"GITLAB_TOKEN": "gl"},
			"gitlab.com", ForgeTypeGitLab, "https://gitlab.com/api/v4", "gl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITHUB_API_URL", "GITLAB_TOKEN", "GITLAB_API_URL"} {
				t.Setenv(k, tc.env[k])
			}
			ft, opts, err := detectForge(Options{Repo: Repo{Host: tc.host, Owner: "g", Name: "p"}})
			if err != nil {
				t.Fatalf("detectForge: %v", err)
			}
			if ft != tc.wantType || opts.BaseURL != tc.wantBase {
				t.Errorf("forge %q at %q, want %q at %q", ft, opts.BaseURL, tc.wantType, tc.wantBase)
			}
			c, err := NewWithOptions(Options{Repo: Repo{Host: tc.host, Owner: "g", Name: "p"}})
			if err != nil {
				t.Fatalf("NewWithOptions: %v", err)
			}
			if got := c.Authenticated(); got != (tc.wantToken != "") {
				t.Errorf("Authenticated() = %v, want a token only when it belongs to this host (%q)", got, tc.wantToken)
			}
		})
	}
}

// Issue #222 (3): forge detection reads the origin of --dir, not of the
// process's working directory, and a host named by an API URL is that forge.
func TestDetectionReadsTheOriginOfTheGivenDirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	for _, argv := range [][]string{
		{"git", "init", "-q"},
		{"git", "remote", "add", "origin", "https://gitlab.com/team/app.git"},
	} {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v %s", argv, err, out)
		}
	}
	// Both forges configured: the origin decides. This test runs in a
	// checkout whose own origin is on GitHub.
	t.Setenv("GITHUB_TOKEN", "gh")
	t.Setenv("GITLAB_TOKEN", "gl")
	t.Setenv("GITHUB_API_URL", "")
	t.Setenv("GITLAB_API_URL", "")
	ft, _, err := detectForge(Options{Dir: dir})
	if err != nil || ft != ForgeTypeGitLab {
		t.Errorf("forge = %q (%v), want gitlab from %s's origin", ft, err, dir)
	}

	// A self-hosted host is matched against the configured API URLs before
	// anything else, whatever the checkout's origin.
	t.Setenv("GITLAB_API_URL", "https://git.example.com")
	ft, opts, err := detectForge(Options{Dir: dir, Repo: Repo{Host: "git.example.com", Owner: "team", Name: "app"}})
	if err != nil || ft != ForgeTypeGitLab || !strings.HasPrefix(opts.BaseURL, "https://git.example.com") {
		t.Errorf("forge = %q at %q (%v), want gitlab at git.example.com", ft, opts.BaseURL, err)
	}
}

// Issue #222 (smaller): GitLab's PRIVATE-TOKEN is not carried across a
// redirect to another host; Go strips only its own auth headers.
func TestTheGitLabTokenDoesNotFollowACrossHostRedirect(t *testing.T) {
	var leaked string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("PRIVATE-TOKEN")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"iid": 1, "title": "t", "state": "opened"}`))
	}))
	defer other.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := strings.Replace(other.URL, "127.0.0.1", "localhost", 1) + r.URL.Path
		http.Redirect(w, r, target, http.StatusFound)
	}))
	defer first.Close()

	c, err := NewWithOptions(Options{BaseURL: first.URL + "/gitlab/api/v4", Token: "secret",
		Repo: Repo{Host: "gitlab.com", Owner: "g", Name: "p"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.ReadPullRequest(context.Background(), IssueRef{Repo: Repo{Host: "gitlab.com", Owner: "g", Name: "p"}, Number: 1})
	if leaked != "" {
		t.Errorf("PRIVATE-TOKEN %q reached another host after a redirect", leaked)
	}
}

// Issue #222 (smaller): GitHub's secondary rate limit — 403 while requests
// remain, saying so in its message — is a rate limit, waited out once. A 403
// with requests remaining and no such message is a refusal (TS-01-21).
func TestASecondaryRateLimitIsARateLimit(t *testing.T) {
	e := &HTTPError{Status: http.StatusForbidden, RetryAfterHeader: "3", RateLimitRemaining: "4999",
		Message: "You have exceeded a secondary rate limit. Please wait a few minutes before you try again."}
	if wait, ok := e.RetryAfter(); !ok || wait < 3*time.Second {
		t.Errorf("RetryAfter = %v, %v", wait, ok)
	}
	if !IsRateLimited(e) {
		t.Error("IsRateLimited = false for a secondary rate limit")
	}
	if _, ok := (&HTTPError{Status: http.StatusForbidden, RateLimitRemaining: "4999"}).RetryAfter(); ok {
		t.Error("a plain 403 is treated as a rate limit")
	}
}

// Issue #222 (smaller): changed files, check runs, reviews and MR notes are
// read past their first page.
func TestPullRequestListsArePaginated(t *testing.T) {
	page := func(r *http.Request) int {
		var n int
		_, _ = fmt.Sscan(r.URL.Query().Get("page"), &n)
		if n == 0 {
			n = 1
		}
		return n
	}
	many := func(n int, item func(i int) any) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = item(i)
		}
		return out
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		n := 100
		if page(r) > 1 {
			n = 7
		}
		if page(r) > 2 {
			n = 0
		}
		var body any
		switch {
		case strings.HasSuffix(r.URL.Path, "/files"):
			body = many(n, func(i int) any { return map[string]any{"filename": fmt.Sprintf("f%d", i), "status": "modified"} })
		case strings.HasSuffix(r.URL.Path, "/reviews"):
			body = many(n, func(i int) any { return map[string]any{"state": "COMMENTED", "body": "x"} })
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			body = map[string]any{"check_runs": many(n, func(i int) any { return map[string]any{"name": fmt.Sprint(i)} })}
		case strings.HasSuffix(r.URL.Path, "/pulls/1"):
			body = map[string]any{"number": 1, "head": map[string]any{"sha": "abc"}}
		default:
			body = map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()
	c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"})
	ref := IssueRef{Repo: Repo{Owner: "o", Name: "r"}, Number: 1, IsPullRequest: true}
	ctx := context.Background()
	if files, err := c.ReadChangedFiles(ctx, ref); err != nil || len(files) != 107 {
		t.Errorf("ReadChangedFiles = %d (%v), want 107", len(files), err)
	}
	if reviews, err := c.GetPRReviews(ctx, ref); err != nil || len(reviews) != 107 {
		t.Errorf("GetPRReviews = %d (%v), want 107", len(reviews), err)
	}
	if checks, err := c.GetCIChecks(ctx, ref); err != nil || len(checks) != 107 {
		t.Errorf("GetCIChecks = %d (%v), want 107", len(checks), err)
	}

	gl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/approvals"):
			_, _ = w.Write([]byte(`{"approved_by": []}`))
		case strings.HasSuffix(r.URL.Path, "/notes"):
			n := 100
			if page(r) > 1 {
				n = 3
			}
			if page(r) > 2 {
				n = 0
			}
			_ = json.NewEncoder(w).Encode(many(n, func(i int) any {
				return map[string]any{"id": i, "body": "note", "system": false, "author": map[string]any{"username": "u"}}
			}))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer gl.Close()
	g := newTestGitLabClient(Options{BaseURL: gl.URL, Token: "tok"})
	reviews, err := g.GetPRReviews(ctx, IssueRef{Repo: Repo{Owner: "g", Name: "p"}, Number: 2, IsPullRequest: true})
	if err != nil || len(reviews) != 103 {
		t.Errorf("GitLab GetPRReviews = %d (%v), want 103 notes", len(reviews), err)
	}
}

// Issue #222 (smaller): the rate-limit wait ends when the run is cancelled.
func TestTheRateLimitWaitHonoursCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c, err := NewGitHub(Options{BaseURL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _ = c.ReadIssue(ctx, IssueRef{Repo: Repo{Owner: "o", Name: "r"}, Number: 1})
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("the run waited %s past its cancellation", d)
	}
}

// Issue #222 (smaller): issue and PR URLs with a sub-page, and GitLab's
// legacy form without /-/, are read as the thread they name.
func TestParseIssueURLAcceptsSubPagesAndLegacyGitLabPaths(t *testing.T) {
	for raw, want := range map[string]IssueRef{
		"https://github.com/o/r/pull/42/files":            {Repo: Repo{Host: "github.com", Owner: "o", Name: "r"}, Number: 42, IsPullRequest: true},
		"https://github.com/o/r/pull/42/commits":          {Repo: Repo{Host: "github.com", Owner: "o", Name: "r"}, Number: 42, IsPullRequest: true},
		"https://gitlab.com/g/p/-/merge_requests/5/diffs": {Repo: Repo{Host: "gitlab.com", Owner: "g", Name: "p"}, Number: 5, IsPullRequest: true},
		"https://gitlab.com/g/sub/proj/issues/5":          {Repo: Repo{Host: "gitlab.com", Owner: "g/sub", Name: "proj"}, Number: 5},
		"https://gitlab.com/g/p/merge_requests/9":         {Repo: Repo{Host: "gitlab.com", Owner: "g", Name: "p"}, Number: 9, IsPullRequest: true},
		"https://gitlab.com/g/sub/proj/-/issues/7":        {Repo: Repo{Host: "gitlab.com", Owner: "g/sub", Name: "proj"}, Number: 7},
		"https://github.com/o/r/issues/3":                 {Repo: Repo{Host: "github.com", Owner: "o", Name: "r"}, Number: 3},
	} {
		got, ok := ParseIssueURL(raw)
		if !ok || got != want {
			t.Errorf("ParseIssueURL(%s) = %+v, %v; want %+v", raw, got, ok, want)
		}
	}
	for _, raw := range []string{
		"https://github.com/o/r/pull/42/unknown",
		"https://github.com/o/r/issues/x",
		"https://github.com/o/r",
	} {
		if _, ok := ParseIssueURL(raw); ok {
			t.Errorf("ParseIssueURL(%s) accepted", raw)
		}
	}
}
