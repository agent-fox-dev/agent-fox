package toolio

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/issuex"
)

// issueCommand is a test command that takes an input and uses a forge.
func issueCommand(rec *inputRecorder) Command {
	c := rec.command(InputRequired)
	c.UsesForge = true
	return c
}

// githubEnv points the environment at a GitHub forge at url, with no token.
func githubEnv(t *testing.T, url string) {
	t.Helper()
	t.Setenv("GITHUB_API_URL", url)
	t.Setenv("GITLAB_API_URL", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITLAB_TOKEN", "")
}

// gitlabEnv points the environment at a GitLab forge at url, with no token.
func gitlabEnv(t *testing.T, url string) {
	t.Helper()
	githubEnv(t, "")
	t.Setenv("GITLAB_API_URL", url)
}

// githubIssue is the document GitHub answers a read of issue 7 with.
func githubIssue(asPullRequest bool) string {
	pr := ""
	if asPullRequest {
		pr = `,"pull_request":{"url":"x"}`
	}
	return `{"number":7,"title":"Login fails","body":"  It crashes on submit.  ","state":"open",` +
		`"user":{"login":"alice"},"labels":[{"name":"bug"},{"name":"ui"}]` + pr + `}`
}

// githubForge answers the reads of issue 7 of o/r.
func githubForge(t *testing.T, asPullRequest bool, comments http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/issues/7/comments"):
			if comments != nil {
				comments(w, r)
				return
			}
			fmt.Fprint(w, `[{"body":"me too","user":{"login":"bob"}}]`)
		case strings.HasSuffix(r.URL.Path, "/issues/7"):
			fmt.Fprint(w, githubIssue(asPullRequest))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestIssueURLIsReadAndRendered_TS_05_35 verifies TS-05-35:
// A GitHub issue URL is read through the forge and rendered.
// Verifies: 05-REQ-6.2
func TestIssueURLIsReadAndRendered_TS_05_35(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		fixturePR bool
		wantPR    bool
	}{
		{"an issue", "/o/r/issues/7", false, false},
		{"a pull request URL", "/o/r/pull/7", false, true},
		{"an issue URL whose thread is a pull request", "/o/r/issues/7", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := githubForge(t, tc.fixturePR, nil)
			githubEnv(t, srv.URL)
			var rec inputRecorder
			app := newTestApp()
			app.Register(issueCommand(&rec))

			code, stdout, _ := invoke(t, app, "X", srv.URL+tc.path)
			env := decode(t, stdout)

			if code != 0 {
				t.Fatalf("exit code = %d\n%s", code, stdout)
			}
			ref, _ := issuex.ParseIssueURL(srv.URL + tc.path)
			ref.IsPullRequest = tc.wantPR
			if at(env, "input", "kind") != "issue" || at(env, "input", "origin") != ref.URL() {
				t.Errorf("input = %v, want kind issue and origin %s", env["input"], ref.URL())
			}
			in := rec.input
			if in.Issue == nil || in.Thread == nil {
				t.Fatalf("Input.Issue = %v, Input.Thread = %v; want both set", in.Issue, in.Thread)
			}
			if in.Issue.IsPullRequest != tc.wantPR {
				t.Errorf("Input.Issue.IsPullRequest = %v, want %v", in.Issue.IsPullRequest, tc.wantPR)
			}
			if in.Body != RenderThread(*in.Issue, *in.Thread) {
				t.Errorf("Body is not the rendering of the thread:\n%s", in.Body)
			}
			if !strings.Contains(in.Body, "me too") || !strings.Contains(in.Body, "Login fails") {
				t.Errorf("Body lacks the issue or its comment:\n%s", in.Body)
			}
		})
	}
}

// TestRenderThread_TS_05_43 verifies TS-05-43:
// RenderThread produces the documented format for each forge and shape.
// Verifies: 05-REQ-7.1
func TestRenderThread_TS_05_43(t *testing.T) {
	thread := func(labels []string, comments []issuex.Comment, truncated bool) issuex.IssueThread {
		return issuex.IssueThread{
			Issue:     issuex.Issue{Title: "T", Body: "\n  body text \n\n", State: "open", Author: issuex.User{Login: "a"}, Labels: labels},
			Comments:  comments,
			Truncated: truncated,
		}
	}
	ref := func(host string, pr bool) issuex.IssueRef {
		return issuex.IssueRef{Repo: issuex.Repo{Owner: "o", Name: "r", Host: host}, Number: 7, IsPullRequest: pr}
	}
	bob := issuex.Comment{Body: "hello", User: issuex.User{Login: "bob"}}
	carol := issuex.Comment{Body: "agreed", User: issuex.User{Login: "carol"}}

	cases := []struct {
		name string
		ref  issuex.IssueRef
		t    issuex.IssueThread
		want string
	}{
		{"github, no comments", ref("github.com", false), thread([]string{"bug", "ui"}, nil, false),
			"GitHub issue o/r#7 (state: open)\nTitle: T\nAuthor: a\nLabels: bug, ui\n\nbody text\n"},
		{"gitlab merge request", ref("gitlab.example", true), thread(nil, nil, false),
			"GitLab pull request o/r#7 (state: open)\nTitle: T\nAuthor: a\n\nbody text\n"},
		{"another host", ref("git.example.com", false), thread(nil, nil, false),
			"Forge issue o/r#7 (state: open)\nTitle: T\nAuthor: a\n\nbody text\n"},
		{"no host", ref("", false), thread(nil, nil, false),
			"GitHub issue o/r#7 (state: open)\nTitle: T\nAuthor: a\n\nbody text\n"},
		{"comments", ref("github.com", false), thread(nil, []issuex.Comment{bob, carol}, false),
			"GitHub issue o/r#7 (state: open)\nTitle: T\nAuthor: a\n\nbody text\n" +
				"\n--- comment by bob ---\nhello\n\n--- comment by carol ---\nagreed\n"},
		{"a truncated thread", ref("github.com", false), thread(nil, []issuex.Comment{bob}, true),
			"GitHub issue o/r#7 (state: open)\nTitle: T\nAuthor: a\n\nbody text\n" +
				"\n--- comment by bob ---\nhello\n\n[... further comments not read ...]\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RenderThread(tc.ref, tc.t); got != tc.want {
				t.Errorf("RenderThread =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// TestTruncatedThread_TS_05_46 verifies TS-05-46:
// A truncated thread marks the input truncated and warns.
// Verifies: 05-REQ-7.4
func TestTruncatedThread_TS_05_46(t *testing.T) {
	// Every page of comments says there is another, so the client stops at
	// its limit of pages and reports the thread as cut short.
	srv := githubForge(t, false, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<http://example.invalid/next>; rel="next"`)
		fmt.Fprint(w, `[{"body":"again","user":{"login":"bob"}}]`)
	})
	githubEnv(t, srv.URL)
	var rec inputRecorder
	app := newTestApp()
	app.Register(issueCommand(&rec))

	code, stdout, _ := invoke(t, app, "X", srv.URL+"/o/r/issues/7")
	env := decode(t, stdout)

	if code != 0 {
		t.Fatalf("exit code = %d\n%s", code, stdout)
	}
	if at(env, "input", "truncated") != true || !contains(warningCodes(env), "input_truncated") {
		t.Errorf("input.truncated = %v, warnings = %v; want true and input_truncated", at(env, "input", "truncated"), warningCodes(env))
	}
	if !strings.HasSuffix(strings.TrimSpace(rec.input.Body), "[... further comments not read ...]") {
		t.Errorf("Body does not end with the marker:\n%s", tail(rec.input.Body, 80))
	}
}

// TestUnreadableComments_TS_05_47 verifies TS-05-47:
// Unreadable comments raise a warning and the run continues.
// Verifies: 05-REQ-7.5
func TestUnreadableComments_TS_05_47(t *testing.T) {
	srv := githubForge(t, false, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"boom"}`, http.StatusInternalServerError)
	})
	githubEnv(t, srv.URL)
	var rec inputRecorder
	app := newTestApp()
	app.Register(issueCommand(&rec))

	code, stdout, _ := invoke(t, app, "X", srv.URL+"/o/r/issues/7")
	env := decode(t, stdout)

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v; want 0, done\n%s", code, env["status"], stdout)
	}
	if !contains(warningCodes(env), "comments_unreadable") {
		t.Errorf("warnings = %v, want comments_unreadable", warningCodes(env))
	}
	if !strings.Contains(rec.input.Body, "It crashes on submit.") {
		t.Errorf("Body lacks the issue's body:\n%s", rec.input.Body)
	}
}

// notFoundForge answers every request with 404.
func notFoundForge(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestUnauthenticatedNotFoundIsAuth_TS_05_48 verifies TS-05-48:
// An unauthenticated not-found read is failed/auth with a token hint.
// Verifies: 05-REQ-7.6
func TestUnauthenticatedNotFoundIsAuth_TS_05_48(t *testing.T) {
	cases := []struct {
		name string
		env  func(*testing.T, string)
		path string
		hint string
	}{
		{"github", githubEnv, "/o/r/issues/7", "GITHUB_TOKEN"},
		{"gitlab", gitlabEnv, "/g/p/-/issues/7", "GITLAB_TOKEN"},
		{"a gitlab merge request", gitlabEnv, "/g/p/-/merge_requests/7", "GITLAB_TOKEN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := notFoundForge(t)
			tc.env(t, srv.URL)
			var rec inputRecorder
			app := newTestApp()
			app.Register(issueCommand(&rec))

			code, stdout, _ := invoke(t, app, "X", srv.URL+tc.path)
			env := decode(t, stdout)

			if code != 1 || env["status"] != "failed" || at(env, "error", "category") != CategoryAuth || rec.ran != 0 {
				t.Errorf("exit code %d, status %v, category %v, ran %d; want 1, failed, auth, 0",
					code, env["status"], at(env, "error", "category"), rec.ran)
			}
			if msg, _ := at(env, "error", "message").(string); !strings.Contains(msg, "private") {
				t.Errorf("error.message = %q, want it to say the issue may be private", msg)
			}
			if got := at(env, "error", "fix_hint", "env"); got != tc.hint {
				t.Errorf("fix_hint.env = %v, want %s", got, tc.hint)
			}
		})
	}
}

// TestAuthenticatedNotFoundIsForge_TS_05_49 verifies TS-05-49:
// An authenticated not-found read is a non-retryable forge failure.
// Verifies: 05-REQ-7.7
func TestAuthenticatedNotFoundIsForge_TS_05_49(t *testing.T) {
	srv := notFoundForge(t)
	githubEnv(t, srv.URL)
	t.Setenv("GITHUB_TOKEN", "a-token")
	var rec inputRecorder
	app := newTestApp()
	app.Register(issueCommand(&rec))

	code, stdout, _ := invoke(t, app, "X", srv.URL+"/o/r/issues/7")
	env := decode(t, stdout)

	if code != 1 || env["status"] != "failed" || at(env, "error", "category") != CategoryForge || at(env, "error", "retryable") != false {
		t.Errorf("exit code %d, status %v, category %v, retryable %v; want 1, failed, forge, false",
			code, env["status"], at(env, "error", "category"), at(env, "error", "retryable"))
	}
}

// TestRateLimitOrTransportErrorIsRetryable_TS_05_50 verifies TS-05-50:
// A rate limit or transport error is a retryable forge failure.
// Verifies: 05-REQ-7.8
func TestRateLimitOrTransportErrorIsRetryable_TS_05_50(t *testing.T) {
	rateLimited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		http.Error(w, `{"message":"slow down"}`, http.StatusTooManyRequests)
	}))
	defer rateLimited.Close()
	dropped := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			conn.Close()
		}
	}))
	defer dropped.Close()
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Resource not accessible"}`, http.StatusForbidden)
	}))
	defer forbidden.Close()

	cases := []struct {
		name      string
		srv       *httptest.Server
		retryable bool
	}{
		{"a rate limit", rateLimited, true},
		{"a dropped connection", dropped, true},
		{"a refusal", forbidden, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			githubEnv(t, tc.srv.URL)
			var rec inputRecorder
			app := newTestApp()
			app.NewForge = func(o issuex.Options) (issuex.Client, error) {
				c, err := issuex.NewGitHub(issuex.Options{BaseURL: tc.srv.URL, Repo: o.Repo})
				issuex.SetGitHubSleep(c, func(time.Duration) {}) // do not wait out the backoff
				return c, err
			}
			app.Register(issueCommand(&rec))

			code, stdout, _ := invoke(t, app, "X", tc.srv.URL+"/o/r/issues/7")
			env := decode(t, stdout)

			if code != 1 || env["status"] != "failed" || at(env, "error", "category") != CategoryForge || at(env, "error", "retryable") != tc.retryable {
				t.Errorf("exit code %d, status %v, category %v, retryable %v; want 1, failed, forge, %v",
					code, env["status"], at(env, "error", "category"), at(env, "error", "retryable"), tc.retryable)
			}
		})
	}
}

// TestCancelledReadIsCancelled guards the rule behind TS-05-50: a read that
// ends because the run was cancelled is cancelled, not a forge failure.
func TestCancelledReadIsCancelled(t *testing.T) {
	srv := githubForge(t, false, nil)
	githubEnv(t, srv.URL)
	var rec inputRecorder
	app := newTestApp()
	app.Register(issueCommand(&rec))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var out strings.Builder
	code := app.Main(ctx, []string{"af", "X", srv.URL + "/o/r/issues/7"}, strings.NewReader(""), &out, &strings.Builder{})
	env := decode(t, out.String())

	if code != 1 || at(env, "error", "category") != CategoryCancelled || rec.ran != 0 {
		t.Errorf("exit code %d, category %v, ran %d; want 1, cancelled, 0", code, at(env, "error", "category"), rec.ran)
	}
}

// TestIssueURLWithoutAForgeIsUsage guards the rule behind TS-05-35: a command
// that declares an input but no forge cannot read an issue.
func TestIssueURLWithoutAForgeIsUsage(t *testing.T) {
	srv := githubForge(t, false, nil)
	githubEnv(t, srv.URL)
	var rec inputRecorder
	app := newTestApp()
	app.Register(rec.command(InputRequired))

	code, stdout, _ := invoke(t, app, "X", srv.URL+"/o/r/issues/7")

	if code != 2 || decode(t, stdout)["status"] != "usage" || rec.ran != 0 {
		t.Errorf("exit code %d, status %v, ran %d; want 2, usage, 0", code, decode(t, stdout)["status"], rec.ran)
	}
}
