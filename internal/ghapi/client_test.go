package ghapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestClient points a Client at an httptest server, which is what makes
// every case below run with no network and no token.
func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := NewWithOptions(Options{BaseURL: srv.URL, Token: "test-token", UserAgent: "test"})
	c.sleep = func(time.Duration) {}
	return c
}

func TestReadIssuePagesComments(t *testing.T) {
	page1 := make([]Comment, 100)
	for i := range page1 {
		page1[i] = Comment{Body: fmt.Sprintf("c%d", i), User: User{Login: "someone"}}
	}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/acme/widgets/issues/42" && r.URL.RawQuery == "":
			writeJSON(w, Issue{Number: 42, Title: "it crashes", Body: "on save",
				State: "open", User: User{Login: "reporter"},
				Labels: []Label{{Name: "bug"}}})
		case strings.HasSuffix(r.URL.Path, "/comments"):
			if r.URL.Query().Get("page") == "1" {
				writeJSON(w, page1)
				return
			}
			writeJSON(w, []Comment{{Body: "last", User: User{Login: "maintainer"}}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	thread, err := c.ReadIssue(context.Background(), IssueRef{Repo: Repo{"acme", "widgets"}, Number: 42})
	if err != nil {
		t.Fatalf("ReadIssue: %v", err)
	}
	if thread.Issue.Title != "it crashes" {
		t.Errorf("title = %q", thread.Issue.Title)
	}
	if got := len(thread.Comments); got != 101 {
		t.Errorf("comments = %d, want 101 (both pages)", got)
	}
	if thread.Truncated {
		t.Error("Truncated: a thread that fit in two pages was reported as cut")
	}
	if got := thread.Issue.LabelNames(); len(got) != 1 || got[0] != "bug" {
		t.Errorf("labels = %v", got)
	}
}

// A readable issue whose comments cannot be read is still a usable report, so
// the comment failure is reported on the Thread rather than returned.
func TestReadIssueSurvivesUnreadableComments(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/comments") {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Resource not accessible"}`))
			return
		}
		writeJSON(w, Issue{Number: 1, Title: "t"})
	})
	thread, err := c.ReadIssue(context.Background(), IssueRef{Repo: Repo{"a", "b"}, Number: 1})
	if err != nil {
		t.Fatalf("ReadIssue: %v", err)
	}
	if thread.CommentsErr == nil {
		t.Fatal("CommentsErr: want the 403 reported")
	}
	if thread.Issue.Title != "t" {
		t.Errorf("the issue itself should still be readable, got %+v", thread.Issue)
	}
}

// The message GitHub puts in the body is more useful than the status text,
// and a 422 usually names the field that failed.
func TestHTTPErrorCarriesTheAPIMessage(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Validation Failed","errors":[{"resource":"Issue","field":"title","code":"missing_field"}]}`))
	})
	_, err := c.CreateIssue(context.Background(), Repo{"a", "b"}, "", "body", nil)
	if err == nil {
		t.Fatal("want an error")
	}
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("want an *HTTPError, got %T", err)
	}
	if he.Status != http.StatusUnprocessableEntity {
		t.Errorf("Status = %d", he.Status)
	}
	if !strings.Contains(he.Message, "Validation Failed") || !strings.Contains(he.Message, "title") {
		t.Errorf("Message = %q, want the API message and the failing field", he.Message)
	}
}

// Every write refuses without a token, and refuses BEFORE the request, so a
// caller can pre-flight the credential rather than discovering it mid-run.
func TestWritesRefuseWithoutAToken(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer srv.Close()
	c := NewWithOptions(Options{BaseURL: srv.URL, Token: " ", UserAgent: "test"})
	c.token = "" // as if neither GITHUB_TOKEN nor GH_TOKEN were set

	ref := IssueRef{Repo: Repo{"a", "b"}, Number: 1}
	ctx := context.Background()
	if _, err := c.CreateIssue(ctx, Repo{"a", "b"}, "t", "b", nil); !errors.Is(err, ErrNoToken) {
		t.Errorf("CreateIssue: want ErrNoToken, got %v", err)
	}
	if _, err := c.UpdateIssue(ctx, ref, "t", "b"); !errors.Is(err, ErrNoToken) {
		t.Errorf("UpdateIssue: want ErrNoToken, got %v", err)
	}
	if _, err := c.AddComment(ctx, ref, "b"); !errors.Is(err, ErrNoToken) {
		t.Errorf("AddComment: want ErrNoToken, got %v", err)
	}
	if _, err := c.CreatePullRequest(ctx, Repo{"a", "b"}, "t", "b", "h", "base", false); !errors.Is(err, ErrNoToken) {
		t.Errorf("CreatePullRequest: want ErrNoToken, got %v", err)
	}
	if called {
		t.Error("a write without a token must not reach the network")
	}
	if c.Authenticated() {
		t.Error("Authenticated() must be false with no token")
	}
}

// A secondary rate limit clears in seconds; failing a ten-minute autonomous
// run on it is a worse answer than waiting once.
func TestRateLimitedRequestIsRetriedOnce(t *testing.T) {
	var n int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"rate limited"}`))
			return
		}
		writeJSON(w, Issue{Number: 5, HTMLURL: "https://github.com/a/b/issues/5"})
	})
	got, err := c.CreateIssue(context.Background(), Repo{"a", "b"}, "t", "body", nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if n != 2 {
		t.Errorf("requests = %d, want 2 (one rate-limited, one retry)", n)
	}
	if got.Number != 5 {
		t.Errorf("issue = %+v", got)
	}
}

// Nothing else is retried: a 401 does not become a 200 on the second attempt,
// and a write retried blindly is how an issue gets filed twice.
func TestNonRateLimitFailuresAreNotRetried(t *testing.T) {
	var n int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		n++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	})
	if _, err := c.CreateIssue(context.Background(), Repo{"a", "b"}, "t", "b", nil); err == nil {
		t.Fatal("want an error")
	}
	if n != 1 {
		t.Errorf("requests = %d, want exactly 1", n)
	}
}

func TestCreateIssueSendsTitleBodyAndLabels(t *testing.T) {
	var payload map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/acme/widgets/issues" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		writeJSON(w, Issue{Number: 9, HTMLURL: "https://github.com/acme/widgets/issues/9"})
	})
	got, err := c.CreateIssue(context.Background(), Repo{"acme", "widgets"}, "title", "body", []string{"af:fix"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if payload["title"] != "title" || payload["body"] != "body" {
		t.Errorf("payload = %v", payload)
	}
	labels, _ := payload["labels"].([]any)
	if len(labels) != 1 || labels[0] != "af:fix" {
		t.Errorf("labels = %v", payload["labels"])
	}
	if got.HTMLURL == "" {
		t.Error("the created issue's URL should be returned")
	}
}

func TestCreatePullRequestSendsHeadAndBase(t *testing.T) {
	var payload map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&payload)
		writeJSON(w, PullRequest{Number: 3, HTMLURL: "https://github.com/acme/widgets/pull/3"})
	})
	pr, err := c.CreatePullRequest(context.Background(), Repo{"acme", "widgets"},
		"fix: it", "body", "fix/issue-1-it", "main", true)
	if err != nil {
		t.Fatalf("CreatePullRequest: %v", err)
	}
	if payload["head"] != "fix/issue-1-it" || payload["base"] != "main" || payload["draft"] != true {
		t.Errorf("payload = %v", payload)
	}
	if pr.Number != 3 {
		t.Errorf("pr = %+v", pr)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
