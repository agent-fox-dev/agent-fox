package issuex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The tests here assert what a call DID — the requests the forge received and
// what came back — from the test body, so a call that sends nothing, or the
// wrong thing, fails them. A handler that asserts inside its own branch is
// never run when nothing is sent.

// request is one request a recording server received.
type request struct {
	Method, Path string
	Header       http.Header
	Body         map[string]any
}

type recorder struct {
	mu   sync.Mutex
	reqs []request
}

func (r *recorder) all() []request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]request(nil), r.reqs...)
}

// find returns the requests with the method and path.
func (r *recorder) find(method, path string) []request {
	var out []request
	for _, q := range r.all() {
		if q.Method == method && q.Path == path {
			out = append(out, q)
		}
	}
	return out
}

// recordingServer answers every request through respond (status, JSON body) and
// records each one with its decoded JSON body.
func recordingServer(t *testing.T, respond func(r request) (int, string)) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := request{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone()}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) == nil {
			q.Body = body
		}
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, q)
		rec.mu.Unlock()
		status, resp := respond(q)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// idleCloser is a RoundTripper that counts CloseIdleConnections calls, as
// http.Client.CloseIdleConnections makes them on its Transport.
type idleCloser struct{ closed atomic.Int32 }

func (c *idleCloser) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("no requests expected")
}
func (c *idleCloser) CloseIdleConnections() { c.closed.Add(1) }

// 02-REQ-1.4, 03-REQ-1.5: Close closes the idle connections of the transport it
// was given.
func TestClose_ClosesIdleTransportConnections(t *testing.T) {
	for name, build := range map[string]func(*http.Client) (Client, error){
		"github": func(hc *http.Client) (Client, error) { return NewGitHub(Options{Token: "t", HTTPClient: hc}) },
		"gitlab": func(hc *http.Client) (Client, error) { return NewGitLab(Options{Token: "t", HTTPClient: hc}) },
	} {
		t.Run(name, func(t *testing.T) {
			spy := &idleCloser{}
			c, err := build(&http.Client{Transport: spy})
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Close(); err != nil {
				t.Fatalf("Close() = %v, want nil", err)
			}
			if got := spy.closed.Load(); got != 1 {
				t.Errorf("CloseIdleConnections was called %d times, want 1", got)
			}
		})
	}
}

// 01-REQ-4.2..4.6: the forge NewWithOptions resolves is the one the input names,
// not merely some client: GitHub and GitLab URLs, environment variables and the
// origin remote each yield the matching adapter.
func TestNewWithOptions_ResolvesTheMatchingAdapter(t *testing.T) {
	clear := func(t *testing.T) {
		for _, v := range []string{"GITHUB_API_URL", "GITHUB_TOKEN", "GH_TOKEN", "GITLAB_API_URL", "GITLAB_TOKEN"} {
			t.Setenv(v, "")
		}
	}
	typeOf := func(c Client) string { return fmt.Sprintf("%T", c) }

	t.Run("explicit base URLs", func(t *testing.T) {
		clear(t)
		for opts, want := range map[string]string{
			"https://api.github.com":            "*issuex.githubClient",
			"https://gitlab.example.com/api/v4": "*issuex.gitlabClient",
		} {
			c, err := NewWithOptions(Options{BaseURL: opts})
			if err != nil {
				t.Fatalf("%s: %v", opts, err)
			}
			if got := typeOf(c); got != want {
				t.Errorf("BaseURL %s resolved to %s, want %s", opts, got, want)
			}
			ft, derr := DetectForge(Options{BaseURL: opts})
			if derr != nil || (ft == ForgeTypeGitHub) != strings.Contains(want, "github") {
				t.Errorf("DetectForge(%s) = %v, %v", opts, ft, derr)
			}
		}
	})

	t.Run("environment", func(t *testing.T) {
		for _, tc := range []struct{ env, val, want string }{
			{"GITHUB_TOKEN", "x", "*issuex.githubClient"},
			{"GH_TOKEN", "x", "*issuex.githubClient"},
			{"GITLAB_TOKEN", "x", "*issuex.gitlabClient"},
		} {
			clear(t)
			t.Setenv(tc.env, tc.val)
			dir := t.TempDir()
			old, _ := os.Getwd()
			if err := os.Chdir(dir); err != nil {
				t.Fatal(err)
			}
			c, err := NewWithOptions(Options{})
			_ = os.Chdir(old)
			if err != nil {
				t.Fatalf("%s: %v", tc.env, err)
			}
			if got := typeOf(c); got != tc.want {
				t.Errorf("%s resolved to %s, want %s", tc.env, got, tc.want)
			}
		}
	})

	t.Run("default endpoints", func(t *testing.T) {
		clear(t)
		gh, err := NewGitHub(Options{Token: "x"})
		if err != nil {
			t.Fatal(err)
		}
		if got := gh.(*githubClient).baseURL; got != "https://api.github.com" {
			t.Errorf("GitHub default base URL = %q", got)
		}
		gl, err := NewGitLab(Options{Token: "x"})
		if err != nil {
			t.Fatal(err)
		}
		if got := gl.(*gitlabClient).baseURL; got != "https://gitlab.com/api/v4" {
			t.Errorf("GitLab default base URL = %q", got)
		}
	})
}

// 01-REQ-4.4, 01-REQ-4.5: GITHUB_API_URL and GITLAB_API_URL name where the
// requests go, and the credential travels with them in the forge's own header.
func TestNewWithOptions_HonoursTheAPIURLVariables(t *testing.T) {
	for _, v := range []string{"GITHUB_API_URL", "GITHUB_TOKEN", "GH_TOKEN", "GITLAB_API_URL", "GITLAB_TOKEN"} {
		t.Setenv(v, "")
	}
	nonGit := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(nonGit); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()
	repo := Repo{Owner: "o", Name: "r"}

	t.Run("github", func(t *testing.T) {
		srv, rec := recordingServer(t, func(request) (int, string) { return 200, `{"full_name": "o/r"}` })
		t.Setenv("GITHUB_API_URL", srv.URL+"/api/v3")
		t.Setenv("GITHUB_TOKEN", "gh-secret")
		c, err := NewWithOptions(Options{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.GetRepository(context.Background(), repo); err != nil {
			t.Fatalf("GetRepository: %v", err)
		}
		got := rec.find("GET", "/api/v3/repos/o/r")
		if len(got) != 1 || got[0].Header.Get("Authorization") != "Bearer gh-secret" {
			t.Errorf("requests = %+v, want one GET /api/v3/repos/o/r with the bearer token", rec.all())
		}
	})

	t.Run("gitlab", func(t *testing.T) {
		srv, rec := recordingServer(t, func(request) (int, string) { return 200, `{"path_with_namespace": "o/r"}` })
		t.Setenv("GITHUB_API_URL", "")
		t.Setenv("GITLAB_API_URL", srv.URL) // without /api/v4: it is added
		t.Setenv("GITLAB_TOKEN", "gl-secret")
		c, err := NewWithOptions(Options{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.GetRepository(context.Background(), repo); err != nil {
			t.Fatalf("GetRepository: %v", err)
		}
		got := rec.find("GET", "/api/v4/projects/o/r")
		if len(got) != 1 || got[0].Header.Get("PRIVATE-TOKEN") != "gl-secret" {
			t.Errorf("requests = %+v, want one GET /api/v4/projects/o%%2Fr with PRIVATE-TOKEN", rec.all())
		}
	})
}

// 02-REQ-5.6, 02-REQ-6.3: ClosePullRequest and PostReviewComment send what the
// contract says; the caller observes the requests, so a call that sends nothing
// fails.
func TestGitHub_PRWritesSendTheirRequests(t *testing.T) {
	srv, rec := recordingServer(t, func(request) (int, string) { return 200, `{}` })
	c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"})
	ref := IssueRef{Repo: Repo{Owner: "o", Name: "r"}, Number: 7}

	if err := c.ClosePullRequest(context.Background(), ref); err != nil {
		t.Fatalf("ClosePullRequest: %v", err)
	}
	patches := rec.find("PATCH", "/repos/o/r/pulls/7")
	if len(patches) != 1 || patches[0].Body["state"] != "closed" {
		t.Errorf("ClosePullRequest sent %+v, want one PATCH /repos/o/r/pulls/7 with state=closed", rec.all())
	}

	if err := c.PostReviewComment(context.Background(), ref, "Looks promising"); err != nil {
		t.Fatalf("PostReviewComment: %v", err)
	}
	posts := rec.find("POST", "/repos/o/r/pulls/7/reviews")
	if len(posts) != 1 || posts[0].Body["event"] != "COMMENT" || posts[0].Body["body"] != "Looks promising" {
		t.Errorf("PostReviewComment sent %+v, want one POST .../reviews with event=COMMENT and the body", rec.all())
	}
}

// 02-REQ-5.5: GetPRState reports open, closed and merged for what the forge
// says, not only merged.
func TestGitHub_GetPRStateReportsEachState(t *testing.T) {
	for _, tc := range []struct {
		name, json, wantState string
		wantMerged            bool
	}{
		{"open", `{"number": 5, "state": "open", "merged": false, "head": {"sha": "s"}}`, "open", false},
		{"closed without merging", `{"number": 5, "state": "closed", "merged": false, "head": {"sha": "s"}}`, "closed", false},
		{"merged overrides closed", `{"number": 5, "state": "closed", "merged": true, "head": {"sha": "s"}}`, "merged", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := recordingServer(t, func(request) (int, string) { return 200, tc.json })
			c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"})
			st, err := c.GetPRState(context.Background(), IssueRef{Repo: Repo{Owner: "o", Name: "r"}, Number: 5})
			if err != nil {
				t.Fatal(err)
			}
			if st.State != tc.wantState || st.Merged != tc.wantMerged || st.HeadSHA != "s" {
				t.Errorf("GetPRState = %+v, want state %q merged %v head s", st, tc.wantState, tc.wantMerged)
			}
		})
	}
}

// 02-REQ-6.4..6.6: MergePullRequest sends the merge method the contract picks —
// none when the repository enables no method — and wraps both 405 and 409 as a
// conflict.
func TestGitHub_MergePullRequestMethodAndConflicts(t *testing.T) {
	mergeRepo := func(allow string) func(request) (int, string) {
		return func(r request) (int, string) {
			if r.Method == "GET" {
				return 200, allow
			}
			return 200, `{"merged": true, "sha": "abc"}`
		}
	}
	ref := IssueRef{Repo: Repo{Owner: "o", Name: "r"}, Number: 10}

	t.Run("no method enabled: merge_method is omitted", func(t *testing.T) {
		srv, rec := recordingServer(t, mergeRepo(`{"allow_merge_commit": false, "allow_squash_merge": false, "allow_rebase_merge": false}`))
		c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"})
		if _, err := c.MergePullRequest(context.Background(), ref, MergeOptions{Method: MergeMethodDefault}); err != nil {
			t.Fatal(err)
		}
		puts := rec.find("PUT", "/repos/o/r/pulls/10/merge")
		if len(puts) != 1 {
			t.Fatalf("got %d merge requests, want 1: %+v", len(puts), rec.all())
		}
		if _, sent := puts[0].Body["merge_method"]; sent {
			t.Errorf("merge_method was sent (%v) although the repository enables no method", puts[0].Body["merge_method"])
		}
	})

	t.Run("default prefers merge, then squash, then rebase", func(t *testing.T) {
		for _, tc := range []struct{ allow, want string }{
			{`{"allow_merge_commit": true, "allow_squash_merge": true, "allow_rebase_merge": true}`, "merge"},
			{`{"allow_merge_commit": false, "allow_squash_merge": true, "allow_rebase_merge": true}`, "squash"},
			{`{"allow_merge_commit": false, "allow_squash_merge": false, "allow_rebase_merge": true}`, "rebase"},
		} {
			srv, rec := recordingServer(t, mergeRepo(tc.allow))
			c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"})
			if _, err := c.MergePullRequest(context.Background(), ref, MergeOptions{Method: MergeMethodDefault}); err != nil {
				t.Fatal(err)
			}
			puts := rec.find("PUT", "/repos/o/r/pulls/10/merge")
			if len(puts) != 1 || puts[0].Body["merge_method"] != tc.want {
				t.Errorf("repo %s: sent %+v, want merge_method %q", tc.allow, rec.all(), tc.want)
			}
		}
	})

	for _, status := range []int{http.StatusMethodNotAllowed, http.StatusConflict} {
		t.Run(fmt.Sprintf("%d is a conflict", status), func(t *testing.T) {
			srv, _ := recordingServer(t, func(request) (int, string) { return status, `{"message": "not mergeable"}` })
			c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"})
			_, err := c.MergePullRequest(context.Background(), ref, MergeOptions{Method: MergeMethodSquash})
			if !IsConflict(err) || !errors.Is(err, ErrConflict) {
				t.Errorf("status %d: err = %v, want ErrConflict", status, err)
			}
		})
	}
}

// 03-REQ-3.7: AddLabels with no labels sends no request at all; with labels it
// sends one PUT that names them.
func TestGitLab_AddLabelsSendsNothingWhenThereAreNone(t *testing.T) {
	srv, rec := recordingServer(t, func(request) (int, string) { return 200, `{"iid": 1}` })
	c, err := NewGitLab(Options{BaseURL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	ref := IssueRef{Repo: Repo{Owner: "org", Name: "repo"}, Number: 1}

	for _, labels := range [][]string{nil, {}} {
		if err := c.AddLabels(context.Background(), ref, labels); err != nil {
			t.Fatal(err)
		}
	}
	if got := rec.all(); len(got) != 0 {
		t.Fatalf("AddLabels with no labels sent %+v, want no request", got)
	}

	if err := c.AddLabels(context.Background(), ref, []string{"bug", "ui"}); err != nil {
		t.Fatal(err)
	}
	puts := rec.find("PUT", "/api/v4/projects/org/repo/issues/1")
	if len(puts) != 1 || puts[0].Body["add_labels"] != "bug,ui" {
		t.Errorf("AddLabels sent %+v, want one PUT with add_labels=bug,ui", rec.all())
	}
}

// 01-REQ-6.2 as built: the backoff is the Retry-After the forge sent plus one
// second (HTTPError.RetryAfter), the same for both adapters; see
// docs/errata/03_retry_after_buffer.md. The assertions are exact.
func TestRateLimitBackoffIsRetryAfterPlusOneSecond(t *testing.T) {
	for name, build := range map[string]func(string, func(time.Duration)) (Client, error){
		"github": func(url string, sleep func(time.Duration)) (Client, error) {
			c, err := NewGitHub(Options{BaseURL: url, Token: "t"})
			if err == nil {
				SetGitHubSleep(c, sleep)
			}
			return c, err
		},
		"gitlab": func(url string, sleep func(time.Duration)) (Client, error) {
			c, err := NewGitLab(Options{BaseURL: url, Token: "t"})
			if err == nil {
				SetGitLabSleep(c, sleep)
			}
			return c, err
		},
	} {
		t.Run(name, func(t *testing.T) {
			for retryAfter, want := range map[string]time.Duration{"1": 2 * time.Second, "2": 3 * time.Second, "10": 11 * time.Second} {
				var calls atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if calls.Add(1) == 1 {
						w.Header().Set("Retry-After", retryAfter)
						w.WriteHeader(http.StatusTooManyRequests)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"full_name": "o/r", "path_with_namespace": "o/r"}`))
				}))
				var slept []time.Duration
				c, err := build(srv.URL, func(d time.Duration) { slept = append(slept, d) })
				if err != nil {
					t.Fatal(err)
				}
				_, err = c.GetRepository(context.Background(), Repo{Owner: "o", Name: "r"})
				srv.Close()
				if err != nil {
					t.Fatalf("Retry-After %s: %v", retryAfter, err)
				}
				if len(slept) != 1 || slept[0] != want {
					t.Errorf("Retry-After %s: slept %v, want exactly one sleep of %v", retryAfter, slept, want)
				}
			}
		})
	}
}
