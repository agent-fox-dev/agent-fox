package fakehub

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// A token is a user's, who sees the workspaces granted to the user, or a workspace's, which /user
// refuses; hub does not tell a workspace that is missing from one that is not the token's.
func TestUserAndWorkspace_AnswerFromTheTokens(t *testing.T) {
	h := New(t)
	h.AddUser("tok-SECRET-1", "usr_1", []string{"widgets"})
	h.AddUser("tok-SECRET-2", "usr_1", nil)
	h.AddWorkspaceToken("tok-SECRET-3", []string{"widgets"})
	cases := []struct {
		method, path, token string
		want                int
	}{
		{"GET", "/api/v1/user", "tok-SECRET-1", 200},
		{"GET", "/api/v1/user", "tok-SECRET-3", 401},
		{"GET", "/api/v1/user", "tok-SECRET-9", 401},
		{"GET", "/api/v1/user", "", 401},
		{"GET", "/api/v1/workspaces/widgets", "tok-SECRET-1", 200},
		{"GET", "/api/v1/workspaces/widgets", "tok-SECRET-2", 200},
		{"GET", "/api/v1/workspaces/widgets", "tok-SECRET-3", 200},
		{"GET", "/api/v1/workspaces/other", "tok-SECRET-1", 404},
		{"GET", "/api/v1/workspaces/other", "tok-SECRET-3", 404},
		{"GET", "/api/v1/workspaces/widgets", "tok-SECRET-9", 401},
		{"GET", "/api/v1/workspaces/widgets", "", 401},
		{"POST", "/api/v1/workspaces/widgets", "tok-SECRET-1", 405},
		{"GET", "/api/v1/elsewhere", "tok-SECRET-1", 404},
		{"GET", "/api/v1/workspaces/", "tok-SECRET-1", 404},
	}
	for _, c := range cases {
		r := do(t, h, c.method, c.path, c.token, nil)
		if r.Status != c.want {
			t.Errorf("%s %s with %q: %d, want %d", c.method, c.path, c.token, r.Status, c.want)
		}
		if c.want != 200 {
			errorType(t, r)
		}
		if strings.Contains(r.Body, "SECRET") {
			t.Errorf("%s %s answered %q, which holds the token", c.method, c.path, r.Body)
		}
	}
	if user := do(t, h, "GET", "/api/v1/user", "tok-SECRET-1", nil).Object(t); user["id"] != "usr_1" || user["login"] != "login-usr_1" {
		t.Errorf("the user = %v", user)
	}
	if ws := do(t, h, "GET", "/api/v1/workspaces/widgets", "tok-SECRET-1", nil).Object(t); len(ws) != 1 || ws["slug"] != "widgets" {
		t.Errorf("a workspace that is granted and not added = %v, want only its slug", ws)
	}

	h.Grant("usr_1", "gadgets")
	h.Revoke("tok-SECRET-1")
	h.Revoke("tok-SECRET-3")
	for path, want := range map[string]int{"/api/v1/workspaces/gadgets": 200, "/api/v1/user": 200} {
		if got := do(t, h, "GET", path, "tok-SECRET-2", nil).Status; got != want {
			t.Errorf("the other token of the user on %s: %d, want %d", path, got, want)
		}
	}
	for _, token := range []string{"tok-SECRET-1", "tok-SECRET-3"} {
		if got := do(t, h, "GET", "/api/v1/workspaces/widgets", token, nil).Status; got != 401 {
			t.Errorf("a revoked token: %d, want 401", got)
		}
	}
	h.HiddenStatus = http.StatusForbidden
	if got := do(t, h, "GET", "/api/v1/workspaces/other", "tok-SECRET-2", nil).Status; got != 403 {
		t.Errorf("a hidden workspace with HiddenStatus 403: %d", got)
	}
}

func TestRequests_AreRecordedInOrderWithTheirTokenAndBody(t *testing.T) {
	h := New(t)
	h.AddUser("t1", "usr_1", []string{"a b"})
	h.AddUser("t2", "usr_2", nil)
	do(t, h, "GET", "/api/v1/user", "t1", nil)
	do(t, h, "GET", "/api/v1/workspaces/a%20b", "t1", nil)
	do(t, h, "POST", "/api/v1/sessions", "t2", `{"workspace_slug":"x"}`)
	do(t, h, "GET", "/api/v1/user", "t1", nil)

	all := h.Requests()
	var got []string
	for i, r := range all {
		got = append(got, r.Method+" "+r.Path+" "+r.URI)
		if r.Seq != i+1 {
			t.Errorf("request %d has the number %d", i, r.Seq)
		}
	}
	if want := []string{"GET /api/v1/user /api/v1/user", "GET /api/v1/workspaces/a b /api/v1/workspaces/a%20b", "POST /api/v1/sessions /api/v1/sessions", "GET /api/v1/user /api/v1/user"}; !slices.Equal(got, want) {
		t.Errorf("Requests = %v, want %v", got, want)
	}
	if all[0].TokenHash != testutil.SHA256("t1") || TokenHash("t1") != testutil.SHA256("t1") || TokenHash("") != "" ||
		all[0].Header.Get("Authorization") != "Bearer t1" || string(all[2].Body) != `{"workspace_slug":"x"}` {
		t.Errorf("the requests hold %+v, want the hash of the token, the headers and the body", all)
	}
	t1 := TokenHash("t1")
	if len(h.RequestsTo("GET", "/api/v1/user")) != 2 || len(h.RequestsTo("", "/api/v1/sessions")) != 1 || len(h.RequestsTo("POST", "/api/v1/user")) != 0 ||
		len(h.RequestsFor(t1)) != 3 || h.CountFor(t1) != 3 || h.Count("/api/v1/user", t1) != 2 || h.Count("/api/v1/user", TokenHash("t2")) != 0 || h.Total() != 4 {
		t.Errorf("the counts do not match the requests %v", got)
	}
}

func TestOnRequest_SeesARequestBeforeItIsAnswered(t *testing.T) {
	h := New(t)
	seen, release := make(chan Request, 1), make(chan struct{})
	h.OnRequest(func(r Request) {
		seen <- r
		<-release
	})
	answered := make(chan int, 1)
	go func() {
		status, _ := try(h, "/api/v1/user", "t")
		answered <- status
	}()
	if r := <-seen; r.Path != "/api/v1/user" {
		t.Errorf("seen %+v", r)
	}
	select {
	case <-answered:
		t.Error("the request was answered before the observer returned")
	default:
	}
	close(release)
	if status := <-answered; status != 401 {
		t.Errorf("the answer = %d", status)
	}
	h.OnRequest(nil)
	do(t, h, "GET", "/api/v1/user", "t", nil)
}

func TestInject_AnswersAsTheFaultSays(t *testing.T) {
	const path = "/api/v1/workspaces/acme/vars/resolved"
	for name, c := range map[string]struct {
		fault  Fault
		status int
		check  func(testutil.Response) bool
	}{
		"a status":          {Fault{Status: 503}, 503, func(r testutil.Response) bool { return errorType(t, r) == "injected" }},
		"a status and body": {Fault{Status: 418, Body: "any body"}, 418, func(r testutil.Response) bool { return r.Body == "any body" }},
		"a body":            {Fault{Body: "<html>"}, 200, func(r testutil.Response) bool { return r.Body == "<html>" }},
		"a redirect":        {Fault{Redirect: "/elsewhere"}, 302, func(r testutil.Response) bool { return r.Header.Get("Location") == "/elsewhere" }},
		"oversize": {Fault{Oversize: 2 << 20}, 200, func(r testutil.Response) bool {
			return len(r.Body) > 2<<20 && len(testutil.Decode[map[string]string](t, []byte(r.Body))["pad"]) == 2<<20
		}},
		"a delay": {Fault{Delay: 30 * time.Millisecond}, 200, func(r testutil.Response) bool { return r.Body == "[]\n" }},
	} {
		h := newHub(t, Workspace{})
		h.Inject("GET", path, c.fault)
		start := time.Now()
		if r := do(t, h, "GET", path, pat, nil); r.Status != c.status || !c.check(r) || time.Since(start) < c.fault.Delay {
			t.Errorf("%s: %d %.100q after %v", name, r.Status, r.Body, time.Since(start))
		}
	}
	h := newHub(t, Workspace{})
	h.Inject("", path, Fault{Hangup: true})
	if _, err := try(h, path, pat); err == nil {
		t.Error("a hang-up answered")
	}
}

// The rule added last that matches the method, the path (or a prefix ending in "/*") and the
// token applies, for as many requests as it says.
func TestInject_TheRuleAddedLastThatMatchesApplies(t *testing.T) {
	h := newHub(t, Workspace{})
	h.AddUser("other", "usr_2", []string{"acme"})
	const issue = "/api/v1/workspaces/acme/issues/42"
	h.AddIssue("acme", Issue{Number: 42})
	statuses := func(n int, method, path, token string) []int {
		var out []int
		for range n {
			out = append(out, do(t, h, method, path, token, map[string]string{}).Status)
		}
		return out
	}
	for _, c := range []struct {
		name         string
		set          func()
		method, path string
		token        string
		want         []int
	}{
		{"a rule for the method", func() { h.Inject("GET", issue, Fault{Status: 500}) }, "PATCH", issue, pat, []int{200}},
		{"a rule for any method", func() { h.Inject("", issue, Fault{Status: 500}) }, "PATCH", issue, pat, []int{500}},
		{"a wildcard", func() { h.Inject("", "/api/v1/workspaces/acme/*", Fault{Status: 500}) }, "GET", issue, pat, []int{500}},
		{"a wildcard is not its prefix", func() { h.Inject("", "/api/v1/workspaces/acme/*", Fault{Status: 500}) }, "GET", "/api/v1/workspaces/acme", pat, []int{200}},
		{"the last rule", func() { h.Fail(issue, 500); h.Inject("", issue, Fault{Status: 502}) }, "GET", issue, pat, []int{502}},
		{"once", func() { h.FailOnce(issue, 401) }, "GET", issue, pat, []int{401, 200}},
		{"a skip", func() { h.Inject("GET", issue, Fault{Status: 429, Skip: 2}) }, "GET", issue, pat, []int{200, 200, 429, 429}},
		{"for a token", func() { h.FailFor(issue, "other", 500) }, "GET", issue, pat, []int{200}},
		{"for the token", func() { h.FailFor(issue, "other", 500) }, "GET", issue, "other", []int{500, 500}},
		{"cleared", func() { h.Fail(issue, 500); h.Hangup("/x"); h.Delay(issue, time.Hour); h.Clear() }, "GET", issue, pat, []int{200}},
	} {
		h.Clear()
		c.set()
		if got := statuses(len(c.want), c.method, c.path, c.token); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	h.Clear()
	h.Hangup(issue)
	if _, err := try(h, issue, pat); err == nil {
		t.Error("Hangup answered")
	}
	h.Clear()
	h.Delay(issue, 30*time.Millisecond)
	if start := time.Now(); do(t, h, "GET", issue, pat, nil).Status != 200 || time.Since(start) < 30*time.Millisecond {
		t.Errorf("Delay answered after %v", time.Since(start))
	}
	if n := len(h.RequestsTo("", issue)); n < 15 {
		t.Errorf("%d requests to the issue recorded, want every one, those that failed too", n)
	}
}
