package fakehub

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// get makes a request to the hub with the token and returns the status and the body.
func get(t *testing.T, h *Hub, path, token string) (int, string) {
	t.Helper()
	status, body, err := try(h, path, token)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return status, body
}

// try is get that returns the transport error.
func try(h *Hub, path, token string) (int, string, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, h.URL+path, nil)
	if err != nil {
		return 0, "", err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), err
}

func sha(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// TestFakehubServesTheUserAndTheWorkspaceFromTheTable_TS_11_71 verifies TS-11-71:
// fakehub serves /user and /workspaces/{slug} from a token, user and workspace
// table, including workspace-only tokens.
// Verifies: 11-REQ-10.1
func TestFakehubServesTheUserAndTheWorkspaceFromTheTable_TS_11_71(t *testing.T) {
	h := New(t)
	h.AddUser("t1", "usr_1", []string{"widgets"})
	h.AddWorkspaceToken("t2", []string{"widgets"})

	status, body := get(t, h, "/api/v1/user", "t1")
	var user map[string]any
	if status != 200 || json.Unmarshal([]byte(body), &user) != nil || user["id"] != "usr_1" {
		t.Errorf("t1 on /user: %d %s, want 200 and the user usr_1", status, body)
	}
	if status, _ := get(t, h, "/api/v1/workspaces/widgets", "t1"); status != 200 {
		t.Errorf("t1 on widgets: %d, want 200", status)
	}
	if status, _ := get(t, h, "/api/v1/workspaces/other", "t1"); status != 404 && status != 403 {
		t.Errorf("t1 on other: %d, want 404 or 403", status)
	}

	if status, _ := get(t, h, "/api/v1/user", "t2"); status != 401 {
		t.Errorf("a workspace-only token on /user: %d, want 401", status)
	}
	if status, _ := get(t, h, "/api/v1/workspaces/widgets", "t2"); status != 200 {
		t.Errorf("a workspace-only token on widgets: %d, want 200", status)
	}
	if status, _ := get(t, h, "/api/v1/workspaces/other", "t2"); status != 404 && status != 403 {
		t.Errorf("a workspace-only token on other: %d, want 404 or 403", status)
	}

	for _, path := range []string{"/api/v1/user", "/api/v1/workspaces/widgets"} {
		if status, _ := get(t, h, path, "unknown"); status != 401 {
			t.Errorf("an unknown token on %s: %d, want 401", path, status)
		}
		if status, _ := get(t, h, path, ""); status != 401 {
			t.Errorf("no token on %s: %d, want 401", path, status)
		}
	}
	if status, _ := get(t, h, "/api/v1/elsewhere", "t1"); status != 404 {
		t.Errorf("an unknown route: %d, want 404", status)
	}
}

// TestFakehubUsersShareTheirWorkspacesAndTokensCanBeRevoked checks the table: a
// user's workspaces belong to the user, not to a token, and a revoked token is
// refused.
// Verifies: 11-REQ-10.1
func TestFakehubUsersShareTheirWorkspacesAndTokensCanBeRevoked(t *testing.T) {
	h := New(t)
	h.AddUser("a", "usr_1", []string{"widgets"})
	h.AddUser("b", "usr_1", nil)
	if status, _ := get(t, h, "/api/v1/workspaces/widgets", "b"); status != 200 {
		t.Errorf("a second token of the same user on widgets: %d, want 200", status)
	}
	h.Grant("usr_1", "gadgets")
	if status, _ := get(t, h, "/api/v1/workspaces/gadgets", "a"); status != 200 {
		t.Errorf("a granted workspace: %d, want 200", status)
	}
	h.Revoke("a")
	if status, _ := get(t, h, "/api/v1/user", "a"); status != 401 {
		t.Errorf("a revoked token on /user: %d, want 401", status)
	}
	if status, _ := get(t, h, "/api/v1/user", "b"); status != 200 {
		t.Errorf("the other token of the user after a revoke: %d, want 200", status)
	}
}

// TestFakehubCountsRecordsAndFailsRoutes_TS_11_72 verifies TS-11-72:
// fakehub counts and records requests by path and token hash and fails routes on
// demand.
// Verifies: 11-REQ-10.2
func TestFakehubCountsRecordsAndFailsRoutes_TS_11_72(t *testing.T) {
	h := New(t)
	h.AddUser("t1", "usr_1", []string{"widgets"})
	h.AddUser("t2", "usr_2", nil)

	get(t, h, "/api/v1/user", "t1")
	get(t, h, "/api/v1/user", "t1")
	get(t, h, "/api/v1/workspaces/widgets", "t1")
	get(t, h, "/api/v1/user", "t2")
	if got := h.Count("/api/v1/user", sha("t1")); got != 2 {
		t.Errorf("Count(/user, t1) = %d, want 2", got)
	}
	if got := h.Count("/api/v1/user", sha("t2")); got != 1 {
		t.Errorf("Count(/user, t2) = %d, want 1", got)
	}
	if got := h.Count("/api/v1/workspaces/widgets", sha("t2")); got != 0 {
		t.Errorf("Count(widgets, t2) = %d, want 0", got)
	}
	if got, total := h.CountFor(sha("t1")), h.Total(); got != 3 || total != 4 {
		t.Errorf("CountFor(t1) = %d, Total = %d; want 3 and 4", got, total)
	}
	if got := TokenHash("t1"); got != sha("t1") {
		t.Errorf("TokenHash = %q, want the hex SHA-256", got)
	}
	var recorded []string
	for _, r := range h.Requests() {
		recorded = append(recorded, r.Method+" "+r.Path+" "+r.TokenHash)
	}
	want := []string{
		"GET /api/v1/user " + sha("t1"), "GET /api/v1/user " + sha("t1"),
		"GET /api/v1/workspaces/widgets " + sha("t1"), "GET /api/v1/user " + sha("t2"),
	}
	if !slices.Equal(recorded, want) {
		t.Errorf("Requests = %v, want every request in order: %v", recorded, want)
	}
	if got := h.RequestsFor(sha("t1")); len(got) != 3 || got[2].Path != "/api/v1/workspaces/widgets" {
		t.Errorf("RequestsFor(t1) = %v, want its three requests in order", got)
	}
	if all := h.Requests(); len(all) == 0 || all[0].Header.Get("Authorization") != "Bearer t1" || all[0].URI != "/api/v1/user" {
		t.Errorf("Requests = %+v, want the first to hold the header and the URI as received", all)
	}

	h.Fail("/api/v1/user", 503)
	if status, _ := get(t, h, "/api/v1/user", "t1"); status != 503 {
		t.Errorf("a failed route answered %d, want 503", status)
	}
	if status, _ := get(t, h, "/api/v1/workspaces/widgets", "t1"); status != 200 {
		t.Errorf("another route answered %d while /user fails, want 200", status)
	}
	if got := h.Count("/api/v1/user", sha("t1")); got != 3 {
		t.Errorf("a failed request is counted too: Count = %d, want 3", got)
	}

	h.Hangup("/api/v1/user")
	if _, _, err := try(h, "/api/v1/user", "t1"); err == nil {
		t.Error("a route that hangs up answered; want a transport error")
	}

	h.Clear()
	if status, _ := get(t, h, "/api/v1/user", "t1"); status != 200 {
		t.Errorf("after Clear the route answered %d, want 200", status)
	}
}

// TestFakehubFailuresCanBeOnceOrForOneToken checks the other ways to fail a route.
// Verifies: 11-REQ-10.2
func TestFakehubFailuresCanBeOnceOrForOneToken(t *testing.T) {
	h := New(t)
	h.AddUser("t1", "usr_1", []string{"widgets"})
	h.AddUser("t2", "usr_2", []string{"widgets"})
	const route = "/api/v1/workspaces/widgets"

	h.FailOnce(route, 401)
	if status, _ := get(t, h, route, "t1"); status != 401 {
		t.Errorf("first request after FailOnce: %d, want 401", status)
	}
	if status, _ := get(t, h, route, "t1"); status != 200 {
		t.Errorf("second request after FailOnce: %d, want 200", status)
	}

	h.FailFor(route, "t2", 500)
	if status, _ := get(t, h, route, "t1"); status != 200 {
		t.Errorf("another token while t2 fails: %d, want 200", status)
	}
	for range 2 {
		if status, _ := get(t, h, route, "t2"); status != 500 {
			t.Errorf("the failing token: %d, want 500 every time", status)
		}
	}

	h.Clear()
	h.Delay("/api/v1/user", 80*time.Millisecond)
	start := time.Now()
	get(t, h, "/api/v1/user", "t1")
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Errorf("a delayed route answered after %v, want at least 80ms", elapsed)
	}
	start = time.Now()
	get(t, h, route, "t1")
	if elapsed := time.Since(start); elapsed > 70*time.Millisecond {
		t.Errorf("another route took %v while /user is delayed", elapsed)
	}
}

// TestFakehubNeverEchoesATokenInABody checks that nothing the fake hub answers
// holds the token it was sent.
// Verifies: 11-REQ-5.6
func TestFakehubNeverEchoesATokenInABody(t *testing.T) {
	h := New(t)
	h.AddUser("tok-SECRET-1", "usr_1", []string{"widgets"})
	h.Fail("/api/v1/workspaces/gadgets", 500)
	for _, path := range []string{"/api/v1/user", "/api/v1/workspaces/widgets", "/api/v1/workspaces/other", "/api/v1/workspaces/gadgets", "/api/v1/nowhere"} {
		for _, token := range []string{"tok-SECRET-1", "tok-SECRET-2"} {
			if _, body := get(t, h, path, token); strings.Contains(body, "SECRET") {
				t.Errorf("GET %s with %s answered %q, which holds the token", path, token, body)
			}
		}
	}
}

// eventsConn is a connection to the events route of the fake hub, read line by line.
type eventsConn struct {
	resp  *http.Response
	lines chan string
}

// openEvents opens the events route of the slug with the token.
func openEvents(t *testing.T, h *Hub, slug, token string) *eventsConn {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.URL+"/api/v1/events?workspace="+slug, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	c := &eventsConn{resp: resp, lines: make(chan string, 64)}
	if resp.StatusCode == http.StatusOK {
		go func() {
			defer close(c.lines)
			sc := bufio.NewScanner(resp.Body)
			for sc.Scan() {
				c.lines <- sc.Text()
			}
		}()
	} else {
		resp.Body.Close()
		close(c.lines)
	}
	return c
}

// frame reads lines up to the next empty line and returns them joined, or "" when the connection ended.
func (c *eventsConn) frame(t *testing.T) string {
	t.Helper()
	var lines []string
	for {
		select {
		case line, ok := <-c.lines:
			if !ok {
				return strings.Join(lines, "\n")
			}
			if line == "" {
				return strings.Join(lines, "\n")
			}
			lines = append(lines, line)
		case <-time.After(5 * time.Second):
			t.Fatal("no frame within 5 seconds")
		}
	}
}

// TestTheFakeHubServesTheEventsRoute_TS_18_12 verifies TS-18-12.
// Verifies: 18-REQ-2.6
func TestTheFakeHubServesTheEventsRoute_TS_18_12(t *testing.T) {
	h := New(t)
	h.AddUser("pat", "usr_1", []string{"widgets"})
	h.AddUser("other", "usr_2", nil)

	if c := openEvents(t, h, "widgets", "unknown"); c.resp.StatusCode != 401 {
		t.Errorf("an unknown token: %d, want 401", c.resp.StatusCode)
	}
	if c := openEvents(t, h, "widgets", "other"); c.resp.StatusCode != h.HiddenStatus {
		t.Errorf("a token without access: %d, want %d", c.resp.StatusCode, h.HiddenStatus)
	}
	h.Fail("/api/v1/events", 503)
	if c := openEvents(t, h, "widgets", "pat"); c.resp.StatusCode != 503 {
		t.Errorf("Fail(503): %d", c.resp.StatusCode)
	}
	h.Clear()
	h.Hangup("/api/v1/events")
	req, _ := http.NewRequest(http.MethodGet, h.URL+"/api/v1/events?workspace=widgets", nil)
	req.Header.Set("Authorization", "Bearer pat")
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
		t.Error("Hangup: an answer, want the connection closed")
	}
	h.Clear()
	c := openEvents(t, h, "widgets", "pat")
	if c.resp.StatusCode != 200 || c.resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("a valid token: %d %s", c.resp.StatusCode, c.resp.Header.Get("Content-Type"))
	}
	waitConns(t, h, "widgets", 1)
	// Go's client tries a GET again on a connection that was closed, so the hang-up may be recorded more than once.
	if n := len(h.EventsRequests()); n < 5 {
		t.Errorf("%d requests to the events route recorded, want every one of at least 5", n)
	}
}

// waitConns waits until the slug has n open connections to the events route.
func waitConns(t *testing.T, h *Hub, slug string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for h.EventConns(slug) != n {
		if time.Now().After(deadline) {
			t.Fatalf("%d connections of %s, want %d", h.EventConns(slug), slug, n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestTheFakeHubsEventSeams_TS_18_13 verifies TS-18-13.
// Verifies: 18-REQ-2.7
func TestTheFakeHubsEventSeams_TS_18_13(t *testing.T) {
	h := New(t)
	h.AddWorkspace(t, Workspace{Slug: "widgets", PAT: "pat"})
	h.AddWorkspace(t, Workspace{Slug: "gadgets", PAT: "pat-g"})
	h.AddIssue("widgets", Issue{Number: 42})
	a, b := openEvents(t, h, "widgets", "pat"), openEvents(t, h, "widgets", "pat")
	g := openEvents(t, h, "gadgets", "pat-g")
	waitConns(t, h, "widgets", 2)
	waitConns(t, h, "gadgets", 1)

	h.SendEvent("widgets", HubEvent{ID: "evt-1", Type: EventIssueLabeled, Issue: 42, Label: "af:fix"})
	for _, c := range []*eventsConn{a, b} {
		f := c.frame(t)
		if !strings.Contains(f, "event: audit_event") || !strings.Contains(f, `"id":"evt-1"`) || !strings.Contains(f, `"event_type":"hub.forge.issue.labeled"`) ||
			!strings.Contains(f, `"number":42`) || !strings.Contains(f, `"label":"af:fix"`) || !strings.Contains(f, `"workspace":"widgets"`) {
			t.Errorf("the event frame is %q", f)
		}
	}
	h.Heartbeat("widgets")
	h.Comment("widgets")
	for _, c := range []*eventsConn{a, b} {
		if f := c.frame(t); !strings.HasPrefix(f, "event: heartbeat") {
			t.Errorf("the heartbeat frame is %q", f)
		}
		if f := c.frame(t); !strings.HasPrefix(f, ":") {
			t.Errorf("the comment is %q", f)
		}
	}

	h.EmitLabeledOnAdd(true)
	req, _ := http.NewRequest(http.MethodPost, h.URL+"/api/v1/workspaces/widgets/issues/42/labels", strings.NewReader(`["af:fix"]`))
	req.Header.Set("Authorization", "Bearer pat")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, c := range []*eventsConn{a, b} {
		if f := c.frame(t); !strings.Contains(f, `"event_type":"hub.forge.issue.labeled"`) || !strings.Contains(f, `"label":"af:fix"`) || !strings.Contains(f, `"number":42`) {
			t.Errorf("the event of the label added through the forge API is %q", f)
		}
	}
	select {
	case line := <-g.lines:
		t.Errorf("the other workspace's connection received %q", line)
	case <-time.After(50 * time.Millisecond):
	}

	h.EndEvents("widgets")
	waitConns(t, h, "widgets", 0)
	if f := a.frame(t); f != "" {
		t.Errorf("after the end the connection read %q", f)
	}
	if h.EventConns("gadgets") != 1 {
		t.Error("ending widgets ended gadgets")
	}
	h.SetIssueState("widgets", 42, "closed")
	if i, _ := h.IssueOf("widgets", 42); i.State != "closed" {
		t.Errorf("the issue is %s, want closed", i.State)
	}
}

// TestAPersonsCommentAndAServiceCommentEachCarryTheirOwnTimeInTheOrderTheyWereAdded_TS_19_57 verifies TS-19-57: a test adds
// a person's comment, the service posts one a minute later by the fake's clock, and both the issue read and the comment list
// give them in the order they were added, with the person's login, and each with the time it was added at.
// Verifies: 19-REQ-10.2
func TestAPersonsCommentAndAServiceCommentEachCarryTheirOwnTimeInTheOrderTheyWereAdded_TS_19_57(t *testing.T) {
	const pat = "pat-widgets"
	h := New(t)
	h.AddWorkspace(t, Workspace{Slug: "widgets", PAT: pat})
	h.AddIssue("widgets", Issue{Number: 42})
	now := time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	h.SetClock(func() time.Time { return now })

	h.AddPersonComment("widgets", 42, "alice", "a")
	now = now.Add(time.Minute)
	status, _ := call(t, h, http.MethodPost, "/api/v1/workspaces/widgets/issues/42/comments", pat, map[string]string{"body": "b"})
	if status != http.StatusCreated {
		t.Fatalf("POST comment = %d", status)
	}

	type comment struct {
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"created_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	var read struct {
		Comments []comment `json:"comments"`
	}
	_, body := get(t, h, "/api/v1/workspaces/widgets/issues/42", pat)
	if err := json.Unmarshal([]byte(body), &read); err != nil {
		t.Fatal(err)
	}
	var listed []comment
	_, body = get(t, h, "/api/v1/workspaces/widgets/issues/42/comments", pat)
	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string][]comment{"the issue read": read.Comments, "the comment list": listed} {
		if len(got) != 2 || got[0].Body != "a" || got[1].Body != "b" {
			t.Fatalf("%s gives %+v, want a then b", name, got)
		}
		if got[0].User.Login != "alice" {
			t.Errorf("%s: the person's comment is by %q, want alice", name, got[0].User.Login)
		}
		if !got[0].CreatedAt.Equal(now.Add(-time.Minute)) || !got[1].CreatedAt.Equal(now) {
			t.Errorf("%s: the times are %v and %v, want each the time it was added", name, got[0].CreatedAt, got[1].CreatedAt)
		}
	}
	if issue, _ := h.IssueOf("widgets", 42); len(issue.Comments) != 2 || issue.Comments[0].Author != "alice" {
		t.Errorf("the hub holds %+v", issue.Comments)
	}
}
