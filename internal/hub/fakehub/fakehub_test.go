package fakehub

import (
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
