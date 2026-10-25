package toolio

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"
)

// issueApp is an app whose command X takes an input and uses a forge, and the
// recorder of its runs.
func issueApp() (*App, *recorder) {
	rec := &recorder{}
	app := newApp()
	app.Register(rec.command(Command{Path: "X", Input: InputRequired, UsesForge: true}))
	return app, rec
}

func TestIssue_IsReadThroughTheForgeAndRendered(t *testing.T) {
	for _, tc := range []struct {
		name       string
		path       string
		threadIsPR bool
		wantPR     bool
	}{
		{"an issue", "/o/r/issues/7", false, false},
		{"a pull request URL", "/o/r/pull/7", false, true},
		{"an issue URL whose thread is a pull request", "/o/r/issues/7", true, true},
	} {
		srv := githubForge(t, tc.threadIsPR, nil)
		githubEnv(t, srv.URL)
		app, rec := issueApp()

		r := run(t, app, "X", srv.URL+tc.path)

		if r.code != 0 {
			t.Fatalf("%s: exit %d, envelope %s", tc.name, r.code, r.stdout)
		}
		in := rec.last.Input
		ref, _ := issuex.ParseIssueURL(srv.URL + tc.path)
		ref.IsPullRequest = tc.wantPR
		if in.Kind != KindIssue || in.Issue == nil || in.Thread == nil || *in.Issue != ref || at(r.env(t), "input", "origin") != ref.URL() {
			t.Fatalf("%s: input %+v, envelope input %v; want the issue %+v", tc.name, in, at(r.env(t), "input"), ref)
		}
		if in.Body != RenderThread(ref, *in.Thread) || !strings.Contains(in.Body, "Login fails") || !strings.Contains(in.Body, "me too") {
			t.Errorf("%s: the body is not the rendered thread:\n%s", tc.name, in.Body)
		}
	}
}

func TestIssue_ACutThreadOrUnreadableCommentsAreWarningsAndTheRunGoesOn(t *testing.T) {
	for _, tc := range []struct {
		name     string
		comments http.HandlerFunc
		warning  string
		body     string
	}{
		{"a thread longer than the client reads", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Link", `<http://example.invalid/next>; rel="next"`) // every page says there is another
			fmt.Fprint(w, `[{"body":"again","user":{"login":"bob"}}]`)
		}, WarnInputTruncated, "[... further comments not read ...]"},
		{"comments that cannot be read", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"message":"boom"}`, http.StatusInternalServerError)
		}, WarnCommentsUnreadable, "It crashes on submit."},
	} {
		srv := githubForge(t, false, tc.comments)
		githubEnv(t, srv.URL)
		app, rec := issueApp()

		r := run(t, app, "X", srv.URL+"/o/r/issues/7")
		env := r.env(t)

		if r.code != 0 || !slices.Contains(warningCodes(env), tc.warning) || !strings.Contains(rec.last.Input.Body, tc.body) {
			t.Errorf("%s: exit %d, warnings %v, body:\n%s", tc.name, r.code, warningCodes(env), rec.last.Input.Body)
		}
		if tc.warning == WarnInputTruncated && at(env, "input", "truncated") != true {
			t.Errorf("%s: input %v, want it truncated", tc.name, env["input"])
		}
	}
}

// answering is a forge that answers every request with status and body.
func answering(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "0")
		http.Error(w, body, status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestIssue_AReadThatFailsIsAFailureThatSaysWhatWouldFixIt(t *testing.T) {
	dropped := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			conn.Close()
		}
	}))
	defer dropped.Close()
	notFound := answering(t, http.StatusNotFound, `{"message":"Not Found"}`)
	for _, tc := range []struct {
		name      string
		srv       *httptest.Server
		gitlab    bool
		token     string
		path      string
		category  string
		retryable bool
		hint      string
	}{
		{"github, not found without a token", notFound, false, "", "/o/r/issues/7", CategoryAuth, false, "GITHUB_TOKEN"},
		{"gitlab, not found without a token", notFound, true, "", "/g/p/-/issues/7", CategoryAuth, false, "GITLAB_TOKEN"},
		{"a gitlab merge request", notFound, true, "", "/g/p/-/merge_requests/7", CategoryAuth, false, "GITLAB_TOKEN"},
		{"not found with a token", notFound, false, "a-token", "/o/r/issues/7", CategoryForge, false, ""},
		{"a rate limit", answering(t, http.StatusTooManyRequests, `{"message":"slow down"}`), false, "", "/o/r/issues/7", CategoryForge, true, ""},
		{"a dropped connection", dropped, false, "", "/o/r/issues/7", CategoryForge, true, ""},
		{"a refusal", answering(t, http.StatusForbidden, `{"message":"no"}`), false, "", "/o/r/issues/7", CategoryForge, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.gitlab {
				gitlabEnv(t, tc.srv.URL)
				t.Setenv("GITLAB_TOKEN", tc.token)
			} else {
				githubEnv(t, tc.srv.URL)
				t.Setenv("GITHUB_TOKEN", tc.token)
			}
			app, rec := issueApp()
			app.NewForge = func(o issuex.Options) (issuex.Client, error) {
				c, err := issuex.NewWithOptions(o)
				if err == nil && !tc.gitlab {
					issuex.SetGitHubSleep(c, func(time.Duration) {}) // no backoff
				}
				return c, err
			}

			r := run(t, app, "X", tc.srv.URL+tc.path)
			env := r.env(t)

			if r.code != 1 || rec.ran != 0 || at(env, "error", "category") != tc.category || at(env, "error", "retryable") != tc.retryable {
				t.Errorf("exit %d, ran %d, error %v; want %s, retryable %v", r.code, rec.ran, env["error"], tc.category, tc.retryable)
			}
			if tc.hint != "" && (at(env, "error", "fix_hint", "env") != tc.hint || !strings.Contains(fmt.Sprint(at(env, "error", "message")), "private")) {
				t.Errorf("error %v, want it to say the issue may be private and to name %s", env["error"], tc.hint)
			}
		})
	}
}

func TestIssue_AReadOfACancelledRunIsCancelled(t *testing.T) {
	srv := githubForge(t, false, nil)
	githubEnv(t, srv.URL)
	app, rec := issueApp()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := runWith(t, ctx, app, strings.NewReader(""), "X", srv.URL+"/o/r/issues/7")

	if r.code != 1 || at(r.env(t), "error", "category") != CategoryCancelled || rec.ran != 0 {
		t.Errorf("exit %d, ran %d, envelope %s; want cancelled", r.code, rec.ran, r.stdout)
	}
}

func TestIssue_TheTokenToSetIsTheForgesOfTheHost(t *testing.T) {
	for host, want := range map[string]string{"github.com": "GITHUB_TOKEN", "gitlab.com": "GITLAB_TOKEN", "gitlab.example.org": "GITLAB_TOKEN", "git.example.org": "GITHUB_TOKEN"} {
		if got := tokenVariable(issuex.IssueRef{Repo: issuex.Repo{Host: host, Owner: "o", Name: "r"}, Number: 1}); got != want {
			t.Errorf("%s: %s, want %s", host, got, want)
		}
	}
}
