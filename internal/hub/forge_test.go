package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage"
)

const forgeHost = "github.example.com"

// forgeEnv is a fake hub whose workspace "acme" has the forge repository acme/widgets on a
// GitHub host, with issue 42 (two comments), a pull request 43 and an issue 44, and a forge
// client of it.
type forgeEnv struct {
	*workerEnv
	repo  issuex.Repo
	forge *ForgeClient
}

func newForgeEnv(t *testing.T) *forgeEnv {
	t.Helper()
	e := newWorkerEnv(t, "acme", fakehub.Workspace{ForgeURL: "https://" + forgeHost + "/acme/widgets"})
	e.hub.AddIssue("acme", fakehub.Issue{Number: 42, Title: "Nil map", Body: "It panics.", Labels: []string{"af:ready"}, Comments: []fakehub.Comment{{Author: "bob", Body: "me too"}, {Author: "eve", Body: "same"}}})
	e.hub.AddIssue("acme", fakehub.Issue{Number: 43, Title: "A pull request", IsPR: true})
	e.hub.AddIssue("acme", fakehub.Issue{Number: 44, Title: "Plain"})
	repo := issuex.Repo{Host: forgeHost, Owner: "acme", Name: "widgets"}
	return &forgeEnv{workerEnv: e, repo: repo, forge: e.client.Forge(workerPAT, "acme", repo)}
}

func (e *forgeEnv) ref(n int) issuex.IssueRef { return issuex.IssueRef{Repo: e.repo, Number: n} }

// forgeCall is one method of the client, with the request it makes.
type forgeCall struct {
	name, method, path string
	do                 func(ctx context.Context, e *forgeEnv) error
}

// forgeCalls are the ten methods of the client that triage and fix need, on issue 42.
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
			_, err := e.forge.UpdateIssue(ctx, e.ref(42), issuex.UpdateIssueRequest{Title: "T", Body: "B"})
			return err
		}},
		{"CreateIssue", "POST", base + "/issues", func(ctx context.Context, e *forgeEnv) error {
			_, err := e.forge.CreateIssue(ctx, e.repo, issuex.CreateIssueRequest{Title: "New", Body: "text", Labels: []string{"af:ready"}})
			return err
		}},
		{"AddComment", "POST", base + "/issues/42/comments", func(ctx context.Context, e *forgeEnv) error {
			_, err := e.forge.AddComment(ctx, e.ref(42), "a note")
			return err
		}},
		{"AddLabels", "POST", base + "/issues/42/labels", func(ctx context.Context, e *forgeEnv) error {
			return e.forge.AddLabels(ctx, e.ref(42), []string{"af:in-progress"})
		}},
		{"RemoveLabel", "DELETE", base + "/issues/42/labels/af:ready", func(ctx context.Context, e *forgeEnv) error { return e.forge.RemoveLabel(ctx, e.ref(42), "af:ready") }},
		{"CreatePullRequest", "POST", base + "/pulls", func(ctx context.Context, e *forgeEnv) error {
			_, err := e.forge.CreatePullRequest(ctx, e.repo, issuex.CreatePullRequestRequest{Title: "Fix", Body: "Closes #42", Head: "fix/42-nil-map", Base: "main", Draft: true})
			return err
		}},
	}
}

// TestEachSupportedForgeMethodSendsOneRequestAtItsMappedRoute_TS_13_20 verifies TS-13-20.
// Verifies: 13-REQ-3.1
func TestEachSupportedForgeMethodSendsOneRequestAtItsMappedRoute_TS_13_20(t *testing.T) {
	e := newForgeEnv(t)
	if !e.forge.Authenticated() || e.forge.Close() != nil {
		t.Error("the client is not authenticated or does not close")
	}
	for _, c := range forgeCalls() {
		before := len(e.hub.Requests())
		if err := c.do(ctx, e); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		got := e.hub.Requests()[before:]
		if len(got) != 1 || got[0].Method != c.method || got[0].URI != c.path || got[0].TokenHash != fakehub.TokenHash(workerPAT) {
			t.Errorf("%s: requests %+v, want one %s %s with the PAT", c.name, got, c.method, c.path)
		}
	}

	// What they decode, and what they write, on a hub that nothing has written to.
	e = newForgeEnv(t)
	repo, err := e.forge.GetRepository(ctx, e.repo)
	if err != nil || repo.FullName != "acme/widgets" || repo.DefaultBranch != "main" || repo.Archived {
		t.Errorf("GetRepository = %+v, %v", repo, err)
	}
	thread, err := e.forge.ReadIssue(ctx, e.ref(42))
	if err != nil || thread.Issue.Number != 42 || thread.Issue.Title != "Nil map" || thread.Issue.Body != "It panics." || thread.Issue.Author.Login != "reporter" || thread.Issue.IsPR || len(thread.Comments) != 2 || thread.Comments[1].User.Login != "eve" || thread.Comments[1].Body != "same" {
		t.Errorf("ReadIssue = %+v, %v", thread, err)
	}
	list, err := e.forge.ListComments(ctx, e.ref(42))
	if err != nil || len(list.Comments) != 2 || list.Comments[0].Body != "me too" {
		t.Errorf("ListComments = %+v, %v", list, err)
	}
	updated, err := e.forge.UpdateIssue(ctx, e.ref(44), issuex.UpdateIssueRequest{Body: "only the body"})
	if err != nil || updated.Body != "only the body" || updated.Title != "Plain" {
		t.Errorf("UpdateIssue = %+v, %v; want the body replaced and the title as it was", updated, err)
	}
	titled, err := e.forge.UpdateIssue(ctx, e.ref(42), issuex.UpdateIssueRequest{Title: "Only the title"})
	if err != nil || titled.Title != "Only the title" || titled.Body != "It panics." {
		t.Errorf("UpdateIssue of the title = %+v, %v; want the body as it was", titled, err)
	}
	created, err := e.forge.CreateIssue(ctx, e.repo, issuex.CreateIssueRequest{Title: "Created", Body: "b"})
	if err != nil || created.Number < 100 || created.Title != "Created" {
		t.Errorf("CreateIssue = %+v, %v", created, err)
	}
	if issue, ok := e.hub.IssueOf("acme", created.Number); !ok || issue.Body != "b" {
		t.Errorf("the issue the hub holds = %+v, %v", issue, ok)
	}
	pull, err := e.forge.CreatePullRequest(ctx, e.repo, issuex.CreatePullRequestRequest{Title: "Fix", Body: "Closes", Head: "fix/1", Base: "main", Draft: true})
	if err != nil || pull.Number == 0 || !pull.Draft || pull.HTMLURL == "" {
		t.Errorf("CreatePullRequest = %+v, %v", pull, err)
	}
	var body map[string]any
	reqs := e.hub.RequestsTo("POST", "/api/v1/workspaces/acme/pulls")
	if len(reqs) == 0 {
		t.Fatal("no pull request was requested")
	}
	if err := json.Unmarshal(reqs[len(reqs)-1].Body, &body); err != nil || body["title"] != "Fix" || body["body"] != "Closes" || body["head"] != "fix/1" || body["base"] != "main" || body["draft"] != true {
		t.Errorf("the pull request body = %v (%v)", body, err)
	}
	comment, err := e.forge.AddComment(ctx, e.ref(42), "hello")
	if err != nil || !strings.Contains(comment, "issuecomment") {
		t.Errorf("AddComment = %q, %v; want the URL of the comment", comment, err)
	}
	if issue, _ := e.hub.IssueOf("acme", 42); len(issue.Comments) < 3 || issue.Comments[len(issue.Comments)-1].Body != "hello" {
		t.Errorf("the comments the hub holds = %+v", issue.Comments)
	}
	// A label with characters that need escaping.
	if err := e.forge.AddLabels(ctx, e.ref(44), []string{"a b/c"}); err != nil {
		t.Fatal(err)
	}
	if err := e.forge.RemoveLabel(ctx, e.ref(44), "a b/c"); err != nil {
		t.Errorf("RemoveLabel of a label that needs escaping: %v", err)
	}
	if issue, _ := e.hub.IssueOf("acme", 44); len(issue.Labels) != 0 {
		t.Errorf("labels after the remove = %v, want none", issue.Labels)
	}
}

// TestADifferentRepositoryFailsBeforeAnyRequestIsSent_TS_13_21 verifies TS-13-21.
// Verifies: 13-REQ-3.2
func TestADifferentRepositoryFailsBeforeAnyRequestIsSent_TS_13_21(t *testing.T) {
	e := newForgeEnv(t)
	factory := e.client.ForgeFactory(workerPAT, "acme", e.repo)
	for name, other := range map[string]issuex.Repo{
		"another host":  {Host: "gitlab.example.com", Owner: "acme", Name: "widgets"},
		"another owner": {Host: forgeHost, Owner: "other", Name: "widgets"},
		"another name":  {Host: forgeHost, Owner: "acme", Name: "gadgets"},
	} {
		before := e.hub.Total()
		ref := issuex.IssueRef{Repo: other, Number: 42}
		_, errFactory := factory(issuex.Options{Repo: other})
		_, errRepo := e.forge.GetRepository(ctx, other)
		_, errRead := e.forge.ReadIssue(ctx, ref)
		_, errCreate := e.forge.CreateIssue(ctx, other, issuex.CreateIssueRequest{Title: "t"})
		_, errPull := e.forge.CreatePullRequest(ctx, other, issuex.CreatePullRequestRequest{Title: "t", Head: "h", Base: "b"})
		errLabel := e.forge.AddLabels(ctx, ref, []string{"x"})
		for what, err := range map[string]error{"the factory": errFactory, "GetRepository": errRepo, "ReadIssue": errRead, "CreateIssue": errCreate, "CreatePullRequest": errPull, "AddLabels": errLabel} {
			if err == nil || !strings.Contains(err.Error(), "not on the repository of the workspace") {
				t.Errorf("%s, %s: %v, want the error that the issue is not on the workspace's repository", name, what, err)
			}
		}
		if e.hub.Total() != before {
			t.Errorf("%s: %d requests were sent", name, e.hub.Total()-before)
		}
	}

	// The same repository in another case is the repository.
	same := issuex.Repo{Host: strings.ToUpper(forgeHost), Owner: "ACME", Name: "Widgets"}
	if _, err := factory(issuex.Options{Repo: same}); err != nil {
		t.Errorf("the factory refused the repository in another case: %v", err)
	}
	if _, err := e.forge.ReadIssue(ctx, issuex.IssueRef{Repo: same, Number: 42}); err != nil {
		t.Errorf("ReadIssue refused the repository in another case: %v", err)
	}
	// The factory takes no repository at all, and gives a client.
	client, err := factory(issuex.Options{UserAgent: "af/1"})
	if err != nil || client == nil {
		t.Errorf("the factory with no repository = %v, %v", client, err)
	}
}

// TestARepoOrRefWithAnEmptyHostIsOnTheWorkspacesHost_TS_13_22 verifies TS-13-22.
// Verifies: 13-REQ-3.3
func TestARepoOrRefWithAnEmptyHostIsOnTheWorkspacesHost_TS_13_22(t *testing.T) {
	e := newForgeEnv(t)
	bare := issuex.Repo{Owner: "acme", Name: "widgets"}
	before := e.hub.Total()
	if _, err := e.forge.ReadIssue(ctx, issuex.IssueRef{Repo: bare, Number: 42}); err != nil {
		t.Errorf("ReadIssue with a ref with no host: %v", err)
	}
	if _, err := e.forge.GetRepository(ctx, bare); err != nil {
		t.Errorf("GetRepository with a repo with no host: %v", err)
	}
	if _, err := e.client.ForgeFactory(workerPAT, "acme", e.repo)(issuex.Options{Repo: bare}); err != nil {
		t.Errorf("the factory with a repo with no host: %v", err)
	}
	if got := e.hub.Total() - before; got != 2 {
		t.Errorf("%d requests, want the 2 of the matching ones", got)
	}

	before = e.hub.Total()
	wrong := issuex.Repo{Owner: "acme", Name: "gadgets"}
	if _, err := e.forge.ReadIssue(ctx, issuex.IssueRef{Repo: wrong, Number: 42}); err == nil || !strings.Contains(err.Error(), "not on the repository of the workspace") {
		t.Errorf("ReadIssue with a mismatching name: %v", err)
	}
	if e.hub.Total() != before {
		t.Errorf("a mismatching repository sent %d requests", e.hub.Total()-before)
	}
}

// TestReadIssueMarksAHubPullRequestAsAPullRequest_TS_13_23 verifies TS-13-23.
// Verifies: 13-REQ-3.4
func TestReadIssueMarksAHubPullRequestAsAPullRequest_TS_13_23(t *testing.T) {
	e := newForgeEnv(t)
	e.hub.AddIssue("acme", fakehub.Issue{Number: 45, Title: "A merge request", IsPR: true})
	for n, want := range map[int]bool{43: true, 45: true, 42: false, 44: false} {
		thread, err := e.forge.ReadIssue(ctx, e.ref(n))
		if err != nil || thread.Issue.IsPR != want {
			t.Errorf("issue %d: IsPR = %v, %v; want %v", n, thread.Issue.IsPR, err, want)
		}
	}
}

// TestTheBodyLimitFollowsTheRepositoryHost_TS_13_24 verifies TS-13-24.
// Verifies: 13-REQ-3.5
func TestTheBodyLimitFollowsTheRepositoryHost_TS_13_24(t *testing.T) {
	c := New("http://127.0.0.1:1", "1")
	for host, want := range map[string]int{
		"gitlab.example.com": issuex.GitLabMaxBodyLength, "GitLab.Example.com": issuex.GitLabMaxBodyLength,
		"github.example.com": issuex.GitHubMaxBodyLength, "git.example.com": issuex.GitHubMaxBodyLength, "": issuex.GitHubMaxBodyLength,
	} {
		forge := c.Forge(workerPAT, "acme", issuex.Repo{Host: host, Owner: "o", Name: "r"})
		if got := issuex.MaxBodyLength(forge); got != want {
			t.Errorf("host %q: MaxBodyLength = %d, want %d", host, got, want)
		}
	}
	if issuex.GitLabMaxBodyLength == issuex.GitHubMaxBodyLength {
		t.Fatal("the two limits are the same, so this test proves nothing")
	}
}

// TestUnlistedMethodsSendNothingAndSayTheyAreNotAvailable_TS_13_25 verifies TS-13-25.
// Verifies: 13-REQ-3.6
func TestUnlistedMethodsSendNothingAndSayTheyAreNotAvailable_TS_13_25(t *testing.T) {
	e := newForgeEnv(t)
	ref := e.ref(42)
	before := e.hub.Total()
	_, errList := e.forge.ListIssues(ctx, e.repo, issuex.IssueFilter{})
	errLabel := e.forge.CreateLabel(ctx, e.repo, issuex.Label{Name: "x"})
	errClose := e.forge.CloseIssue(ctx, ref, "bye")
	_, errPull := e.forge.ReadPullRequest(ctx, ref)
	_, errFiles := e.forge.ReadChangedFiles(ctx, ref)
	_, errState := e.forge.GetPRState(ctx, ref)
	_, errChecks := e.forge.GetCIChecks(ctx, ref)
	_, errReviews := e.forge.GetPRReviews(ctx, ref)
	errReview := e.forge.PostReviewComment(ctx, ref, "x")
	_, errMerge := e.forge.MergePullRequest(ctx, ref, issuex.MergeOptions{})
	errClosePR := e.forge.ClosePullRequest(ctx, ref)
	for name, err := range map[string]error{
		"ListIssues": errList, "CreateLabel": errLabel, "CloseIssue": errClose, "ReadPullRequest": errPull, "ReadChangedFiles": errFiles,
		"GetPRState": errState, "GetCIChecks": errChecks, "GetPRReviews": errReviews, "PostReviewComment": errReview,
		"MergePullRequest": errMerge, "ClosePullRequest": errClosePR,
	} {
		if err == nil || !strings.Contains(err.Error(), name+" is not available through hub in this release") {
			t.Errorf("%s: %v, want it not available through hub in this release", name, err)
		}
	}
	if e.hub.Total() != before {
		t.Errorf("%d requests were sent", e.hub.Total()-before)
	}
}

// TestHubForgeErrorsMapOntoTheIssuexTypesTheLoopsClassify_TS_13_26 verifies TS-13-26: for each
// method, 401 and 403 are an *issuex.HTTPError of that status with the message of the hub
// client, 404 is not found, 409 a conflict, 429 rate limited, a rejection is an HTTPError of
// its status, and 503 is an error that may be tried again; and the preflight of the fix loop,
// which classifies the errors of the forge, turns them into auth, forge and retryable.
// Verifies: 13-REQ-3.7
func TestHubForgeErrorsMapOntoTheIssuexTypesTheLoopsClassify_TS_13_26(t *testing.T) {
	for _, c := range forgeCalls() {
		t.Run(c.name, func(t *testing.T) {
			e := newForgeEnv(t)
			inject := func(f fakehub.Fault) error {
				e.hub.Clear()
				e.hub.Inject(c.method, mustUnescape(t, c.path), f)
				return c.do(ctx, e)
			}
			for _, status := range []int{401, 403} {
				err := inject(fakehub.Fault{Status: status})
				var he *issuex.HTTPError
				if !errors.As(err, &he) || he.Status != status || issuex.IsRateLimited(err) {
					t.Fatalf("%d: %v, want an *issuex.HTTPError of that status", status, err)
				}
				if status == 403 && (!strings.Contains(he.Message, "scope") || !strings.Contains(he.Message, "PUT /api/v1/workspaces/acme")) {
					t.Errorf("403: the message %q lacks the scope and the PUT route", he.Message)
				}
			}
			if err := inject(fakehub.Fault{Status: 404}); !issuex.IsNotFound(err) {
				t.Errorf("404: %v, want issuex's not found", err)
			}
			if err := inject(fakehub.Fault{Status: 409}); !errors.Is(err, issuex.ErrConflict) {
				t.Errorf("409: %v, want issuex.ErrConflict", err)
			}
			if err := inject(fakehub.Fault{Status: 429}); !issuex.IsRateLimited(err) {
				t.Errorf("429: %v, want issuex's rate limited", err)
			}
			err := inject(fakehub.Fault{Status: 422, Body: `{"error":{"code":422,"message":"a pull request exists","error_type":"forge_rejected"}}`})
			var he *issuex.HTTPError
			if !errors.As(err, &he) || he.Status != 422 || he.Message != "a pull request exists" {
				t.Errorf("422: %v, want an HTTPError with hub's message", err)
			}
			err = inject(fakehub.Fault{Status: 503})
			if err == nil || errors.As(err, &he) || issuex.IsNotFound(err) || issuex.IsRateLimited(err) || errors.Is(err, issuex.ErrConflict) {
				t.Errorf("503: %v, want an error that is none of the typed ones, which the loops may try again", err)
			}
			var unavailable *UnavailableError
			if !errors.As(err, &unavailable) {
				t.Errorf("503: %v does not wrap the unavailable error", err)
			}
			short, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			e.hub.Clear()
			e.hub.Inject(c.method, mustUnescape(t, c.path), fakehub.Fault{Delay: time.Second})
			if err := c.do(short, e); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("a context that ended: %v, want it to be the error of the context", err)
			}
		})
	}

	t.Run("the preflights of fix and triage yield auth, forge and retryable", func(t *testing.T) {
		e := newForgeEnv(t)
		g := gitx.New(t.TempDir(), nil)
		check := func(f fakehub.Fault) *toolio.Failure {
			e.hub.Clear()
			e.hub.Inject("GET", "/api/v1/workspaces/acme", f)
			err := fix.CheckLanding(ctx, g, fix.PreflightParams{Land: fix.LandPR, DryRun: true, Forge: e.forge, Target: e.repo})
			var failure *toolio.Failure
			if !errors.As(err, &failure) {
				t.Fatalf("CheckLanding = %v, want a failure", err)
			}
			return failure
		}
		for _, status := range []int{401, 403} {
			if f := check(fakehub.Fault{Status: status}); f == nil || f.Category != "auth" || f.Retryable {
				t.Errorf("%d: %+v, want auth, not retryable", status, f)
			}
		}
		if f := check(fakehub.Fault{Status: 404}); f == nil || f.Category != "forge" || f.Retryable {
			t.Errorf("404: %+v, want forge, not retryable", f)
		}
		for _, status := range []int{429, 503} {
			if f := check(fakehub.Fault{Status: status}); f == nil || f.Category != "forge" || !f.Retryable {
				t.Errorf("%d: %+v, want forge, retryable", status, f)
			}
		}
		e.hub.Clear()
		if err := fix.CheckLanding(ctx, g, fix.PreflightParams{Land: fix.LandPR, DryRun: true, Forge: e.forge, Target: e.repo}); err != nil {
			t.Errorf("with nothing wrong: %v", err)
		}

		// The triage preflight reads the repository the same way.
		refusal := func(f fakehub.Fault) *triage.Refusal {
			e.hub.Clear()
			e.hub.Inject("GET", "/api/v1/workspaces/acme", f)
			return triage.CheckLandTarget(ctx, triage.LandCheck{Forge: e.forge, Target: e.repo})
		}
		for _, status := range []int{401, 403} {
			if r := refusal(fakehub.Fault{Status: status}); r == nil || r.Category != triage.CategoryAuth || r.Retryable {
				t.Errorf("triage, %d: %+v, want auth, not retryable", status, r)
			}
		}
		if r := refusal(fakehub.Fault{Status: 404}); r == nil || r.Category != triage.CategoryForge || r.Retryable {
			t.Errorf("triage, 404: %+v, want forge, not retryable", r)
		}
		for _, status := range []int{429, 503} {
			if r := refusal(fakehub.Fault{Status: status}); r == nil || r.Category != triage.CategoryForge || !r.Retryable {
				t.Errorf("triage, %d: %+v, want forge, retryable", status, r)
			}
		}
		e.hub.Clear()
		if r := triage.CheckLandTarget(ctx, triage.LandCheck{Forge: e.forge, Target: e.repo}); r != nil {
			t.Errorf("triage with nothing wrong: %+v", r)
		}
	})
}

func mustUnescape(t *testing.T, path string) string {
	t.Helper()
	p, err := url.PathUnescape(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestAThreadWhoseCommentsCouldNotBeReadSaysSo pins that hub's comments_err, the answer 200 of an
// issue that was read whose comments were not, reaches the loops as the error of the comments, from
// which they raise the warning comments_unreadable.
// Verifies: 13-REQ-3.1
func TestAThreadWhoseCommentsCouldNotBeReadSaysSo(t *testing.T) {
	e := newForgeEnv(t)
	ref := issuex.IssueRef{Repo: e.repo, Number: 42}

	thread, err := e.forge.ReadIssue(ctx, ref)
	if err != nil || thread.CommentsErr != nil {
		t.Fatalf("a thread whose comments were read: %v, comments error %v", err, thread.CommentsErr)
	}

	e.hub.Inject("GET", e.path("/issues/42"), fakehub.Fault{Status: 200, Body: `{"issue":{"number":42,"title":"Nil map"},"comments":[],"truncated":false,"comments_err":{}}`})
	thread, err = e.forge.ReadIssue(ctx, ref)

	if err != nil || thread.Issue.Number != 42 || thread.CommentsErr == nil {
		t.Errorf("thread %+v, error %v, want the issue and an error for the comments", thread, err)
	}
	for _, null := range []string{`"comments_err":null`, `"comments_err":false`} {
		e.hub.Clear()
		e.hub.Inject("GET", e.path("/issues/42"), fakehub.Fault{Status: 200, Body: `{"issue":{"number":42,"title":"Nil map"},"comments":[],"truncated":false,` + null + `}`})
		if thread, err = e.forge.ReadIssue(ctx, ref); err != nil || thread.CommentsErr != nil {
			t.Errorf("%s: comments error %v, %v, want none", null, thread.CommentsErr, err)
		}
	}
}
