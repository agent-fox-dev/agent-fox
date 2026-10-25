package hub

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

// forgeCall is a method of the forge client that sends a request, on issue 42.
type forgeCall struct {
	name, method, uri string
	do                func(ctx context.Context, e *forgeEnv) error
}

func forgeCalls() []forgeCall {
	const base = "/api/v1/workspaces/acme"
	return []forgeCall{
		{"GetRepository", "GET", base, func(ctx context.Context, e *forgeEnv) error { _, err := e.forge.GetRepository(ctx, e.repo); return err }},
		{"ReadIssue", "GET", base + "/issues/42", func(ctx context.Context, e *forgeEnv) error { _, err := e.forge.ReadIssue(ctx, e.ref(42)); return err }},
		{"ListComments", "GET", base + "/issues/42/comments", func(ctx context.Context, e *forgeEnv) error {
			_, err := e.forge.ListComments(ctx, e.ref(42))
			return err
		}},
		{"UpdateIssue", "PATCH", base + "/issues/42", func(ctx context.Context, e *forgeEnv) error {
			_, err := e.forge.UpdateIssue(ctx, e.ref(42), issuex.UpdateIssueRequest{Title: "T"})
			return err
		}},
		{"CreateIssue", "POST", base + "/issues", func(ctx context.Context, e *forgeEnv) error {
			_, err := e.forge.CreateIssue(ctx, e.repo, issuex.CreateIssueRequest{Title: "New"})
			return err
		}},
		{"AddComment", "POST", base + "/issues/42/comments", func(ctx context.Context, e *forgeEnv) error {
			_, err := e.forge.AddComment(ctx, e.ref(42), "a note")
			return err
		}},
		{"AddLabels", "POST", base + "/issues/42/labels", func(ctx context.Context, e *forgeEnv) error {
			return e.forge.AddLabels(ctx, e.ref(42), []string{"af:in-progress"})
		}},
		{"RemoveLabel", "DELETE", base + "/issues/42/labels/af:ready", func(ctx context.Context, e *forgeEnv) error {
			return e.forge.RemoveLabel(ctx, e.ref(42), "af:ready")
		}},
		{"CreatePullRequest", "POST", base + "/pulls", func(ctx context.Context, e *forgeEnv) error {
			_, err := e.forge.CreatePullRequest(ctx, e.repo, issuex.CreatePullRequestRequest{Title: "Fix", Head: "fix/42", Base: "main"})
			return err
		}},
		{"ListIssues", "GET", base + "/issues?state=open&labels=af%3Aready&limit=100", func(ctx context.Context, e *forgeEnv) error {
			_, err := e.forge.ListIssues(ctx, e.repo, openIssues("af:ready", 0))
			return err
		}},
	}
}

func TestForge_EachMethodSendsOneRequestAtItsRouteWithThePAT(t *testing.T) {
	e := newForgeEnv(t)
	if !e.forge.Authenticated() || e.forge.Close() != nil {
		t.Error("the client is not authenticated or does not close")
	}
	for _, c := range forgeCalls() {
		before := e.hub.Total()
		if err := c.do(ctx, e); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		got := e.hub.Requests()[before:]
		if len(got) != 1 || got[0].Method != c.method || got[0].URI != c.uri || got[0].TokenHash != fakehub.TokenHash(pat) {
			t.Errorf("%s: requests %+v, want one %s %s with the PAT", c.name, got, c.method, c.uri)
		}
	}
}

func TestForge_ReadsWhatHubAnswers(t *testing.T) {
	e := newForgeEnv(t)
	repo, err := e.forge.GetRepository(ctx, e.repo)
	if want := (issuex.Repository{FullName: "acme/widgets", DefaultBranch: "main", Permissions: issuex.RepoPermissions{Push: true, Pull: true}}); err != nil || !reflect.DeepEqual(repo, want) {
		t.Errorf("GetRepository = %+v, %v; want %+v", repo, err, want)
	}
	thread, err := e.forge.ReadIssue(ctx, e.ref(42))
	if err != nil || thread.Issue.Title != "Nil map" || thread.Issue.Body != "It panics." || thread.Issue.Author.Login != "reporter" || thread.Issue.IsPR ||
		len(thread.Comments) != 2 || thread.Comments[1].User.Login != "eve" || thread.Comments[1].Body != "same" || thread.CommentsErr != nil {
		t.Errorf("ReadIssue = %+v, %v", thread, err)
	}
	if list, err := e.forge.ListComments(ctx, e.ref(42)); err != nil || len(list.Comments) != 2 || list.Comments[0].Body != "me too" {
		t.Errorf("ListComments = %+v, %v", list, err)
	}
	if created, err := e.forge.CreateIssue(ctx, e.repo, issuex.CreateIssueRequest{Title: "Created", Body: "b"}); err != nil || created.Number < 100 || created.Title != "Created" {
		t.Errorf("CreateIssue = %+v, %v", created, err)
	}
	if pull, err := e.forge.CreatePullRequest(ctx, e.repo, issuex.CreatePullRequestRequest{Title: "Fix", Body: "Closes", Head: "fix/1", Base: "main", Draft: true}); err != nil || pull.Number == 0 || !pull.Draft || pull.HTMLURL == "" {
		t.Errorf("CreatePullRequest = %+v, %v", pull, err)
	}
	if got := e.sent(t, "POST", "/api/v1/workspaces/acme/pulls"); !reflect.DeepEqual(got, map[string]any{"title": "Fix", "body": "Closes", "head": "fix/1", "base": "main", "draft": true}) {
		t.Errorf("the pull request = %v", got)
	}
	if url, err := e.forge.AddComment(ctx, e.ref(42), "hello"); err != nil || !strings.Contains(url, "issuecomment") {
		t.Errorf("AddComment = %q, %v; want the URL of the comment", url, err)
	}
	if issue, _ := e.hub.IssueOf("acme", 42); issue.Comments[len(issue.Comments)-1].Body != "hello" {
		t.Errorf("the comments of the issue = %+v", issue.Comments)
	}
}

// What a request leaves empty is not sent, so hub leaves it as it is.
func TestForge_WritesOnlyWhatTheRequestSets(t *testing.T) {
	e := newForgeEnv(t)
	for _, c := range []struct {
		req  issuex.UpdateIssueRequest
		want map[string]any
	}{
		{issuex.UpdateIssueRequest{Body: "only the body"}, map[string]any{"body": "only the body"}},
		{issuex.UpdateIssueRequest{Title: "T", Body: "B", State: "closed", Labels: []string{"x"}, Assignees: []string{"bob"}},
			map[string]any{"title": "T", "body": "B", "state": "closed", "labels": []any{"x"}, "assignees": []any{"bob"}}},
	} {
		if _, err := e.forge.UpdateIssue(ctx, e.ref(44), c.req); err != nil {
			t.Fatal(err)
		}
		if got := e.sent(t, "PATCH", "/api/v1/workspaces/acme/issues/44"); !reflect.DeepEqual(got, c.want) {
			t.Errorf("UpdateIssue sent %v, want %v", got, c.want)
		}
	}
	if issue, _ := e.hub.IssueOf("acme", 44); issue.Title != "T" || issue.State != "closed" {
		t.Errorf("the issue = %+v", issue)
	}
	for _, c := range []struct {
		req  issuex.CreateIssueRequest
		want map[string]any
	}{
		{issuex.CreateIssueRequest{Title: "A"}, map[string]any{"title": "A"}},
		{issuex.CreateIssueRequest{Title: "B", Body: "b", Labels: []string{"x"}, Assignees: []string{"bob"}},
			map[string]any{"title": "B", "body": "b", "labels": []any{"x"}, "assignees": []any{"bob"}}},
	} {
		if _, err := e.forge.CreateIssue(ctx, e.repo, c.req); err != nil {
			t.Fatal(err)
		}
		if got := e.sent(t, "POST", "/api/v1/workspaces/acme/issues"); !reflect.DeepEqual(got, c.want) {
			t.Errorf("CreateIssue sent %v, want %v", got, c.want)
		}
	}

	if err := e.forge.AddLabels(ctx, e.ref(44), nil); err != nil || !reflect.DeepEqual(e.sent(t, "POST", "/api/v1/workspaces/acme/issues/44/labels"), []any{}) {
		t.Errorf("AddLabels of none = %v, want an empty list sent", err)
	}
	if err := e.forge.AddLabels(ctx, e.ref(44), []string{"a b/c"}); err != nil {
		t.Fatal(err)
	}
	if err := e.forge.RemoveLabel(ctx, e.ref(44), "a b/c"); err != nil || len(e.hub.RequestsTo("DELETE", "/api/v1/workspaces/acme/issues/44/labels/a b/c")) != 1 {
		t.Errorf("RemoveLabel of a label that needs escaping = %v", err)
	}
	if issue, _ := e.hub.IssueOf("acme", 44); !slices.Equal(issue.Labels, []string{"x"}) {
		t.Errorf("labels after the remove = %v, want the one it had before", issue.Labels)
	}
}

// The repository is compared without case, and an empty host is the workspace's.
func TestForge_RefusesAnotherRepositoryBeforeAnyRequest(t *testing.T) {
	e := newForgeEnv(t)
	factory := e.client.ForgeFactory(pat, "acme", e.repo)
	methods := map[string]func(issuex.Repo) error{
		"the factory":   func(r issuex.Repo) error { _, err := factory(issuex.Options{Repo: r}); return err },
		"GetRepository": func(r issuex.Repo) error { _, err := e.forge.GetRepository(ctx, r); return err },
		"ReadIssue": func(r issuex.Repo) error {
			_, err := e.forge.ReadIssue(ctx, issuex.IssueRef{Repo: r, Number: 42})
			return err
		},
		"ListComments": func(r issuex.Repo) error {
			_, err := e.forge.ListComments(ctx, issuex.IssueRef{Repo: r, Number: 42})
			return err
		},
		"UpdateIssue": func(r issuex.Repo) error {
			_, err := e.forge.UpdateIssue(ctx, issuex.IssueRef{Repo: r, Number: 42}, issuex.UpdateIssueRequest{Title: "T"})
			return err
		},
		"CreateIssue": func(r issuex.Repo) error {
			_, err := e.forge.CreateIssue(ctx, r, issuex.CreateIssueRequest{Title: "t"})
			return err
		},
		"AddComment": func(r issuex.Repo) error {
			_, err := e.forge.AddComment(ctx, issuex.IssueRef{Repo: r, Number: 42}, "x")
			return err
		},
		"AddLabels": func(r issuex.Repo) error {
			return e.forge.AddLabels(ctx, issuex.IssueRef{Repo: r, Number: 42}, []string{"x"})
		},
		"RemoveLabel": func(r issuex.Repo) error {
			return e.forge.RemoveLabel(ctx, issuex.IssueRef{Repo: r, Number: 42}, "af:ready")
		},
		"CreatePullRequest": func(r issuex.Repo) error {
			_, err := e.forge.CreatePullRequest(ctx, r, issuex.CreatePullRequestRequest{Title: "t", Head: "h", Base: "b"})
			return err
		},
		"ListIssues": func(r issuex.Repo) error { _, err := e.forge.ListIssues(ctx, r, openIssues("af:ready", 0)); return err },
	}
	for name, other := range map[string]issuex.Repo{
		"another host":              {Host: "gitlab.example.com", Owner: "acme", Name: "widgets"},
		"another owner":             {Host: forgeHost, Owner: "other", Name: "widgets"},
		"another name":              {Host: forgeHost, Owner: "acme", Name: "gadgets"},
		"another name with no host": {Owner: "acme", Name: "gadgets"},
	} {
		before := e.hub.Total()
		for method, do := range methods {
			if err := do(other); err == nil || !strings.Contains(err.Error(), "the issue is not on the repository of the workspace") {
				t.Errorf("%s, %s: %v, want it refused", name, method, err)
			}
		}
		if n := e.hub.Total() - before; n != 0 {
			t.Errorf("%s: %d requests were sent", name, n)
		}
	}
	for name, same := range map[string]issuex.Repo{
		"another case": {Host: strings.ToUpper(forgeHost), Owner: "ACME", Name: "Widgets"},
		"no host":      {Owner: "acme", Name: "widgets"},
	} {
		for method, do := range methods {
			if err := do(same); err != nil {
				t.Errorf("%s, %s: %v, want the workspace's repository taken", name, method, err)
			}
		}
	}
	if client, err := factory(issuex.Options{UserAgent: "af/1"}); err != nil || client == nil {
		t.Errorf("the factory with no repository = %v, %v, want a client", client, err)
	}
}

// Hub says that an issue is a pull request, or that its comments could not be read, and the
// loops refuse the one and warn of the other.
func TestForge_ReadIssueSaysWhatHubSaysOfTheIssueAndItsComments(t *testing.T) {
	e := newForgeEnv(t)
	for n, want := range map[int]bool{42: false, 43: true} {
		if thread, err := e.forge.ReadIssue(ctx, e.ref(n)); err != nil || thread.Issue.IsPR != want {
			t.Errorf("issue %d: IsPR = %v, %v; want %v", n, thread.Issue.IsPR, err, want)
		}
	}
	for commentsErr, want := range map[string]bool{`{}`: true, `{"message":"x"}`: true, `null`: false, `false`: false} {
		e.answer("GET", e.path("/issues/42"), fakehub.Fault{Status: 200, Body: `{"issue":{"number":42},"comments":[],"truncated":true,"comments_err":` + commentsErr + `}`})
		thread, err := e.forge.ReadIssue(ctx, e.ref(42))
		if err != nil || thread.Issue.Number != 42 || !thread.Truncated || (thread.CommentsErr != nil) != want {
			t.Errorf("comments_err %s: %+v, %v; want an error of the comments %v", commentsErr, thread, err, want)
		}
	}
}

func TestForge_MaxBodyLengthFollowsTheHost(t *testing.T) {
	c := New("http://127.0.0.1:1", "1")
	for host, want := range map[string]int{
		"gitlab.example.com": issuex.GitLabMaxBodyLength, "GitLab.Example.com": issuex.GitLabMaxBodyLength,
		"github.example.com": issuex.GitHubMaxBodyLength, "git.example.com": issuex.GitHubMaxBodyLength, "": issuex.GitHubMaxBodyLength,
	} {
		if got := issuex.MaxBodyLength(c.Forge(pat, "acme", issuex.Repo{Host: host, Owner: "o", Name: "r"})); got != want {
			t.Errorf("host %q: %d, want %d", host, got, want)
		}
	}
}

func TestForge_TheOtherMethodsSendNothingAndAreNotAvailable(t *testing.T) {
	e := newForgeEnv(t)
	ref := e.ref(42)
	errs := map[string]error{
		"CreateLabel":       e.forge.CreateLabel(ctx, e.repo, issuex.Label{Name: "x"}),
		"CloseIssue":        e.forge.CloseIssue(ctx, ref, "bye"),
		"PostReviewComment": e.forge.PostReviewComment(ctx, ref, "x"),
		"ClosePullRequest":  e.forge.ClosePullRequest(ctx, ref),
	}
	_, errs["ReadPullRequest"] = e.forge.ReadPullRequest(ctx, ref)
	_, errs["ReadChangedFiles"] = e.forge.ReadChangedFiles(ctx, ref)
	_, errs["GetPRState"] = e.forge.GetPRState(ctx, ref)
	_, errs["GetCIChecks"] = e.forge.GetCIChecks(ctx, ref)
	_, errs["GetPRReviews"] = e.forge.GetPRReviews(ctx, ref)
	_, errs["MergePullRequest"] = e.forge.MergePullRequest(ctx, ref, issuex.MergeOptions{})
	for name, err := range errs {
		if err == nil || err.Error() != name+" is not available through hub in this release" {
			t.Errorf("%s: %v, want it not available through hub", name, err)
		}
	}
	if n := e.hub.Total(); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

// The errors are the issuex types that the loops of af tell apart: auth, not found, conflict, rate
// limited, a refusal with its status, and anything else, which they may try again.
func TestForge_HubErrorsAreTheIssuexErrorsTheLoopsTellApart(t *testing.T) {
	httpError := func(status int, message string) func(error) bool {
		return func(err error) bool {
			he, ok := errors.AsType[*issuex.HTTPError](err)
			return ok && he.Status == status && strings.Contains(he.Message, message)
		}
	}
	retryable := func(err error) bool {
		_, typed := errors.AsType[*issuex.HTTPError](err)
		return isUnavailable(err) && !typed && !issuex.IsNotFound(err) && !issuex.IsRateLimited(err) && !errors.Is(err, issuex.ErrConflict)
	}
	cases := []struct {
		name  string
		fault fakehub.Fault
		is    func(error) bool
	}{
		{"401", fakehub.Fault{Status: 401}, httpError(401, "hub refused the token")},
		{"403", fakehub.Fault{Status: 403}, httpError(403, "PUT /api/v1/workspaces/acme")},
		{"404", fakehub.Fault{Status: 404}, issuex.IsNotFound},
		{"409", fakehub.Fault{Status: 409}, func(err error) bool { return errors.Is(err, issuex.ErrConflict) }},
		{"429", fakehub.Fault{Status: 429}, issuex.IsRateLimited},
		{"422", fakehub.Fault{Status: 422, Body: hubError(422, "forge_rejected", "a pull request exists")}, httpError(422, "a pull request exists")},
		{"502 forge_forbidden", fakehub.Fault{Status: 502, Body: hubError(502, "forge_forbidden", "the forge refused hub")}, httpError(502, "the forge refused hub")},
		{"503", fakehub.Fault{Status: 503}, retryable},
		{"a hang-up", fakehub.Fault{Hangup: true}, retryable},
	}
	for _, c := range forgeCalls() {
		t.Run(c.name, func(t *testing.T) {
			e := newForgeEnv(t)
			for _, tc := range cases {
				e.answer(c.method, faultPath(t, c.uri), tc.fault)
				if err := c.do(ctx, e); !tc.is(err) {
					t.Errorf("%s: %v, not the error it should be", tc.name, err)
				}
			}
			e.answer(c.method, faultPath(t, c.uri), fakehub.Fault{Delay: time.Minute})
			short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
			defer cancel()
			if err := c.do(short, e); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("a context that ended: %v, want the error of the context", err)
			}
		})
	}
}
