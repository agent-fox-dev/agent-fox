package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

const authTTL = 60 * time.Second

const (
	userPath      = "/api/v1/user"
	workspacePath = "/api/v1/workspaces/"
	pingPath      = "/api/v1/test/ping"
	slugRoute     = "/api/v1/test/workspaces/"
)

// clock is the clock of the caches in the tests; only the test moves it.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// authHarness is a server whose hub is a fakehub and whose caches run on a clock
// of the test, with two routes of its own: one that needs a caller, and one in a
// workspace.
type authHarness struct {
	*harness
	Hub   *fakehub.Hub
	Clock *clock
	srv   *server
}

func newAuthHarness(t *testing.T) *authHarness {
	t.Helper()
	return newAuthHarnessOn(t, fakehub.New(t), "", nil)
}

// newAuthHarnessOn starts the server against the hub, over the store at the path
// opened with the key, or over a new store with the key of the harness if the
// path is empty.
func newAuthHarnessOn(t *testing.T, fake *fakehub.Hub, dbPath string, keyBytes []byte) *authHarness {
	t.Helper()
	a := &authHarness{Hub: fake, Clock: newClock()}
	if dbPath == "" {
		dbPath, keyBytes = filepath.Join(t.TempDir(), "agent-fox.db"), testKeyBytes
	}
	a.harness = newHarnessOn(t, dbPath, keyBytes, func(d *Deps) {
		d.Hub = hub.New(a.Hub.URL, "1.2.3")
		d.AuthCacheTTL = authTTL
		d.Now = a.Clock.Now
		d.Routes = func(mux *http.ServeMux, s *server) {
			a.srv = s
			mux.HandleFunc("GET "+pingPath, s.authed(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, map[string]string{"user": principalOf(r).user})
			}))
			mux.HandleFunc("GET "+slugRoute+"{slug}", s.inWorkspace(func(w http.ResponseWriter, r *http.Request, slug string) {
				writeJSON(w, http.StatusOK, map[string]string{"slug": slug, "user": principalOf(r).user, "path": r.PathValue("slug")})
			}))
		}
	})
	return a
}

// bearer is the headers of a request made with the token.
func bearer(token string) []string { return []string{"Authorization", "Bearer " + token} }

// ping makes the request that needs a caller.
func (a *authHarness) ping(t *testing.T, token string) response {
	t.Helper()
	return a.get(t, pingPath, bearer(token)...)
}

// workspace makes the request that needs a caller who sees the slug.
func (a *authHarness) workspace(t *testing.T, slug, token string) response {
	t.Helper()
	return a.get(t, slugRoute+slug, bearer(token)...)
}

// userCalls is how many times hub was asked whom the token belongs to.
func (a *authHarness) userCalls(token string) int {
	return a.Hub.Count(userPath, fakehub.TokenHash(token))
}

// workspaceCalls is how many times hub was asked whether the token sees the slug.
func (a *authHarness) workspaceCalls(token, slug string) int {
	return a.Hub.Count(workspacePath+slug, fakehub.TokenHash(token))
}

// TestRequestsWithoutAUsableCredentialAreRefusedWithoutHub_TS_11_37 verifies TS-11-37:
// A missing, non-Bearer or empty-token Authorization header is answered 401
// without calling hub.
// Verifies: 11-REQ-6.1
func TestRequestsWithoutAUsableCredentialAreRefusedWithoutHub_TS_11_37(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("tok", "usr_1", nil)

	for _, header := range []string{"", "Basic abc", "Bearer", "Bearer ", "Token tok", "Bearertok", "Bearer\t"} {
		var headers []string
		if header != "" {
			headers = []string{"Authorization", header}
		}
		r := a.get(t, pingPath, headers...)
		if r.Status != 401 || r.errorCode(t) != "unauthenticated" {
			t.Errorf("Authorization %q: %d %s, want 401 unauthenticated", header, r.Status, r.Body)
		}
		if r.Header.Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("Authorization %q: WWW-Authenticate = %q, want Bearer", header, r.Header.Get("WWW-Authenticate"))
		}
	}
	if n := a.Hub.Total(); n != 0 {
		t.Errorf("hub received %d requests for credentials that are not usable, want none", n)
	}

	r := a.get(t, pingPath, "Authorization", "bearer tok")
	if r.Status != 200 {
		t.Fatalf("a lowercase scheme: %d %s, want 200", r.Status, r.Body)
	}
	if n := a.userCalls("tok"); n != 1 {
		t.Errorf("hub was asked about the token %d times, want once", n)
	}
	if got := r.JSON(t)["user"]; got != "usr_1" {
		t.Errorf("the caller = %v, want usr_1", got)
	}
}

// TestBearerTokenReadsTheHeaderTheWayTheRuleSays checks the parsing of the header
// on requests as they are made in memory, where nothing has trimmed them.
// Verifies: 11-REQ-6.1
func TestBearerTokenReadsTheHeaderTheWayTheRuleSays(t *testing.T) {
	for header, want := range map[string]string{
		"Bearer tok": "tok", "bearer tok": "tok", "BEARER tok": "tok", "Bearer   tok  ": "tok", "Bearer a.b-c_d~+/=": "a.b-c_d~+/=",
		"": "", "Bearer": "", "Bearer ": "", "Bearer    ": "", "Bearer 	": "", "Basic tok": "", "Bearertok": "", " Bearer tok": "", "Token tok": "",
	} {
		r := httptest.NewRequest("GET", "/", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		got, ok := bearerToken(r)
		if got != want || ok != (want != "") {
			t.Errorf("bearerToken(%q) = %q, %v; want %q, %v", header, got, ok, want, want != "")
		}
	}
}

// TestAValidatedTokenIsNotAskedAboutAgainWithinTheTTL_TS_11_38 verifies TS-11-38:
// A validated caller token is not sent to /user again within the TTL and is sent
// again after it.
// Verifies: 11-REQ-6.2
func TestAValidatedTokenIsNotAskedAboutAgainWithinTheTTL_TS_11_38(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("t1", "usr_1", nil)

	for range 3 {
		if r := a.ping(t, "t1"); r.Status != 200 {
			t.Fatalf("ping: %d %s", r.Status, r.Body)
		}
	}
	if n := a.userCalls("t1"); n != 1 {
		t.Errorf("hub was asked %d times within the TTL, want once", n)
	}
	a.Clock.Advance(authTTL - time.Second)
	a.ping(t, "t1")
	if n := a.userCalls("t1"); n != 1 {
		t.Errorf("hub was asked %d times just before the TTL, want once", n)
	}
	a.Clock.Advance(2 * time.Second)
	if r := a.ping(t, "t1"); r.Status != 200 {
		t.Fatalf("ping after the TTL: %d %s", r.Status, r.Body)
	}
	if n := a.userCalls("t1"); n != 2 {
		t.Errorf("hub was asked %d times after the TTL, want twice", n)
	}

	// The cache is keyed by the hash of the token, never by the token.
	if user, ok := a.srv.auth.identities.Get(hashToken("t1")); !ok || user.user != "usr_1" {
		t.Errorf("no identity under the hex SHA-256 of the token: %+v, %v", user, ok)
	}
	if _, ok := a.srv.auth.identities.Get("t1"); ok {
		t.Error("the identity cache has the raw token as a key")
	}
	if got, want := hashToken("t1"), fakehub.TokenHash("t1"); got != want || len(got) != 64 {
		t.Errorf("hashToken = %q, want the hex SHA-256 %q", got, want)
	}
}

// TestATokenHubRefusedIsNotAskedAboutAgainWithinTheTTL_TS_11_39 verifies TS-11-39:
// A token hub refused answers 401 and is not sent again within the TTL.
// Verifies: 11-REQ-6.3
func TestATokenHubRefusedIsNotAskedAboutAgainWithinTheTTL_TS_11_39(t *testing.T) {
	a := newAuthHarness(t)
	for range 2 {
		if r := a.ping(t, "bad"); r.Status != 401 || r.errorCode(t) != "unauthenticated" {
			t.Errorf("a refused token: %d %s, want 401 unauthenticated", r.Status, r.Body)
		}
	}
	if n := a.userCalls("bad"); n != 1 {
		t.Errorf("hub was asked %d times within the TTL, want once", n)
	}
	a.Clock.Advance(authTTL + time.Second)
	a.ping(t, "bad")
	if n := a.userCalls("bad"); n != 2 {
		t.Errorf("hub was asked %d times after the TTL, want twice", n)
	}

	t.Run("a 403 on the user call is a refusal too", func(t *testing.T) {
		a := newAuthHarness(t)
		a.Hub.AddUser("t", "usr_1", nil)
		a.Hub.FailFor(userPath, "t", 403)
		for range 2 {
			if r := a.ping(t, "t"); r.Status != 401 {
				t.Errorf("hub's 403: %d %s, want 401", r.Status, r.Body)
			}
		}
		if n := a.userCalls("t"); n != 1 {
			t.Errorf("hub was asked %d times, want the refusal cached after one", n)
		}
	})

	t.Run("a token that hub accepts after refusing it is accepted when the refusal expires", func(t *testing.T) {
		a := newAuthHarness(t)
		if r := a.ping(t, "late"); r.Status != 401 {
			t.Fatalf("before: %d", r.Status)
		}
		a.Hub.AddUser("late", "usr_7", nil)
		if r := a.ping(t, "late"); r.Status != 401 {
			t.Errorf("within the TTL of the refusal: %d, want the refusal served from the cache", r.Status)
		}
		a.Clock.Advance(authTTL)
		if r := a.ping(t, "late"); r.Status != 200 {
			t.Errorf("after the refusal expired: %d %s, want 200", r.Status, r.Body)
		}
	})
}

// TestHubBeingUnavailableIsAnswered502AndNeverCached_TS_11_40 verifies TS-11-40:
// Hub unavailable on identity or visibility answers 502 and caches nothing.
// Verifies: 11-REQ-6.4
func TestHubBeingUnavailableIsAnswered502AndNeverCached_TS_11_40(t *testing.T) {
	t.Run("the identity", func(t *testing.T) {
		a := newAuthHarness(t)
		a.Hub.AddUser("t", "usr_1", nil)
		for name, fail := range map[string]func(){
			"a 500":    func() { a.Hub.Fail(userPath, 500) },
			"a 429":    func() { a.Hub.Fail(userPath, 429) },
			"a hangup": func() { a.Hub.Hangup(userPath) },
		} {
			a.Hub.Clear()
			fail()
			r := a.ping(t, "t")
			if r.Status != 502 || r.errorCode(t) != "hub_unavailable" {
				t.Errorf("%s: %d %s, want 502 hub_unavailable", name, r.Status, r.Body)
			}
			// Nothing was kept of the failure: once hub is back, the next request asks
			// it again (net/http may repeat a GET after a hangup, so the failure itself
			// may have cost more than one call).
			a.Hub.Clear()
			failed := a.userCalls("t")
			if r := a.ping(t, "t"); r.Status != 200 {
				t.Errorf("%s: after hub recovered: %d %s, want 200", name, r.Status, r.Body)
			}
			if n := a.userCalls("t") - failed; n != 1 {
				t.Errorf("%s: hub was asked %d times by the request after the failure, want 1 (nothing cached)", name, n)
			}
			a.ping(t, "t")
			if n := a.userCalls("t") - failed; n != 1 {
				t.Errorf("%s: hub was asked %d times after the success, want it cached", name, n)
			}
			a.srv.auth.identities.Delete(hashToken("t"))
		}
	})

	t.Run("the visibility", func(t *testing.T) {
		a := newAuthHarness(t)
		a.Hub.AddUser("t", "usr_1", []string{"widgets"})
		a.ping(t, "t") // the identity is cached
		a.Hub.Fail(workspacePath+"widgets", 503)
		r := a.workspace(t, "widgets", "t")
		if r.Status != 502 || r.errorCode(t) != "hub_unavailable" {
			t.Errorf("hub down: %d %s, want 502 hub_unavailable", r.Status, r.Body)
		}
		a.Hub.Clear()
		if r := a.workspace(t, "widgets", "t"); r.Status != 200 {
			t.Errorf("after recovery: %d %s, want 200", r.Status, r.Body)
		}
		if n := a.workspaceCalls("t", "widgets"); n != 2 {
			t.Errorf("hub was asked %d times for the failure and the retry, want 2", n)
		}
		a.workspace(t, "widgets", "t")
		if n := a.workspaceCalls("t", "widgets"); n != 2 {
			t.Errorf("hub was asked %d times, want the success cached", n)
		}
	})
}

// TestVisibilityIsCachedPerTokenAndSlug checks the caching half of TS-11-41 at
// the guard: found and not found are each kept for the TTL, for the token and
// the slug they were asked about.
// Verifies: 11-REQ-6.5
func TestVisibilityIsCachedPerTokenAndSlug(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("A", "usr_1", []string{"widgets"})
	a.Hub.AddUser("B", "usr_2", nil)

	for range 3 {
		if r := a.workspace(t, "widgets", "B"); r.Status != 404 || r.errorCode(t) != "not_found" {
			t.Fatalf("B on widgets: %d %s, want 404 not_found", r.Status, r.Body)
		}
		if r := a.workspace(t, "widgets", "A"); r.Status != 200 {
			t.Fatalf("A on widgets: %d %s, want 200", r.Status, r.Body)
		}
	}
	if n := a.workspaceCalls("B", "widgets"); n != 1 {
		t.Errorf("hub was asked %d times about B and widgets, want the not-found cached", n)
	}
	if n := a.workspaceCalls("A", "widgets"); n != 1 {
		t.Errorf("hub was asked %d times about A and widgets, want the found cached", n)
	}
	a.workspace(t, "gadgets", "A")
	if n := a.workspaceCalls("A", "gadgets"); n != 1 {
		t.Errorf("hub was asked %d times about A and gadgets, want one for the new slug", n)
	}
	a.Clock.Advance(authTTL + time.Second)
	a.workspace(t, "widgets", "B")
	a.workspace(t, "widgets", "A")
	if a.workspaceCalls("B", "widgets") != 2 || a.workspaceCalls("A", "widgets") != 2 {
		t.Errorf("after the TTL, hub was asked %d and %d times, want 2 and 2", a.workspaceCalls("B", "widgets"), a.workspaceCalls("A", "widgets"))
	}

	// What hub does not show a caller is the same answer whichever slug it is.
	one, two := a.workspace(t, "widgets", "B"), a.workspace(t, "gadgets", "B")
	if one.Status != 404 || one.Body != two.Body {
		t.Errorf("the 404 for two slugs differs: %q and %q", one.Body, two.Body)
	}
}

// TestTheSlugIsCheckedFirstWithin400_TS_11_42 verifies TS-11-42:
// The slug syntax is checked first, before identity, with 400 invalid_request.
// Verifies: 11-REQ-6.5
func TestTheSlugIsCheckedFirstWithin400_TS_11_42(t *testing.T) {
	a := newAuthHarness(t)
	long := "a" + strings.Repeat("b", 99)
	for _, slug := range []string{"-bad", ".dot", "_under", "a%20b", "x!", "a%2Fb", "a" + strings.Repeat("b", 100), "%C3%A9", "a%00b"} {
		r := a.get(t, slugRoute+slug, bearer("nope")...)
		if r.Status != 400 || r.errorCode(t) != "invalid_request" {
			t.Errorf("slug %q: %d %s, want 400 invalid_request even with a token hub would refuse", slug, r.Status, r.Body)
		}
		if r := a.get(t, slugRoute+slug); r.Status != 400 {
			t.Errorf("slug %q without a token: %d, want 400 before the identity is looked at", slug, r.Status)
		}
	}
	if n := a.Hub.Total(); n != 0 {
		t.Errorf("hub received %d requests for invalid slugs, want none", n)
	}

	a.Hub.AddUser("good", "usr_1", []string{"a", "A.b_c-1", long, "0"})
	for _, slug := range []string{"a", "A.b_c-1", long, "0"} {
		r := a.workspace(t, slug, "good")
		if r.Status != 200 || r.JSON(t)["slug"] != slug {
			t.Errorf("slug %q: %d %s, want 200 with the slug", slug, r.Status, r.Body)
		}
	}
	if r := a.get(t, slugRoute+"a%2Eb", bearer("good")...); r.Status != 404 {
		t.Errorf("an escaped dot is a slug a.b that is not visible: %d, want 404", r.Status)
	}
}

// TestAHubRefusalOnACallerCallEvictsTheTokensEntries_TS_11_43 verifies TS-11-43:
// A hub 401 on a caller-token call evicts the identity and all visibility entries.
// Verifies: 11-REQ-6.6
func TestAHubRefusalOnACallerCallEvictsTheTokensEntries_TS_11_43(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("t", "usr_1", []string{"widgets", "gadgets", "gizmos"})
	a.Hub.AddUser("other", "usr_2", []string{"widgets"})
	for _, slug := range []string{"widgets", "gadgets"} {
		if r := a.workspace(t, slug, "t"); r.Status != 200 {
			t.Fatalf("warming %s: %d %s", slug, r.Status, r.Body)
		}
	}
	a.workspace(t, "widgets", "other") // another token's entries are not touched
	users := a.userCalls("t")

	a.Hub.FailOnce(workspacePath+"gizmos", 401)
	r := a.workspace(t, "gizmos", "t")
	if r.Status != 401 || r.errorCode(t) != "unauthenticated" {
		t.Errorf("hub's 401 on the workspace call: %d %s, want 401 unauthenticated", r.Status, r.Body)
	}
	if _, ok := a.srv.auth.identities.Get(hashToken("t")); ok {
		t.Error("the identity of the token is still cached")
	}

	if r := a.workspace(t, "widgets", "t"); r.Status != 200 {
		t.Fatalf("the next request: %d %s, want 200", r.Status, r.Body)
	}
	if got := a.userCalls("t"); got != users+1 {
		t.Errorf("hub was asked about the token %d times, want %d: the next request revalidates it", got, users+1)
	}
	if got := a.workspaceCalls("t", "widgets"); got != 2 {
		t.Errorf("hub was asked %d times about widgets, want 2: its visibility was evicted", got)
	}
	a.workspace(t, "gadgets", "t")
	if got := a.workspaceCalls("t", "gadgets"); got != 2 {
		t.Errorf("hub was asked %d times about gadgets, want 2: its visibility was evicted too", got)
	}
	a.workspace(t, "widgets", "other")
	if got := a.workspaceCalls("other", "widgets"); got != 1 {
		t.Errorf("hub was asked %d times about the other token, want its entries kept", got)
	}
}

// TestAnUnavailableOrRefusedIdentityStopsASlugRouteBeforeTheWorkspaceCall checks
// the order of the checks: identity, then visibility.
// Verifies: 11-REQ-6.5
func TestAnUnavailableOrRefusedIdentityStopsASlugRouteBeforeTheWorkspaceCall(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("t", "usr_1", []string{"widgets"})
	a.Hub.Fail(userPath, 500)
	if r := a.workspace(t, "widgets", "t"); r.Status != 502 {
		t.Errorf("hub down on the identity: %d, want 502", r.Status)
	}
	a.Hub.Clear()
	if r := a.workspace(t, "widgets", "nobody"); r.Status != 401 {
		t.Errorf("an unknown token: %d, want 401", r.Status)
	}
	if n := a.Hub.Count(workspacePath+"widgets", fakehub.TokenHash("t")) + a.Hub.Count(workspacePath+"widgets", fakehub.TokenHash("nobody")); n != 0 {
		t.Errorf("hub was asked %d workspace questions before the identity was settled, want none", n)
	}
}

// TestTheCallerIsInTheLogOnceKnown checks that the request record names the user.
// Verifies: 11-REQ-7.7
func TestTheCallerIsInTheLogOnceKnown(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("t", "usr_1", nil)
	a.ping(t, "t")
	a.ping(t, "bad")
	a.get(t, pingPath)
	reqs := a.Logs.requestRecords(t)
	if len(reqs) != 3 {
		t.Fatalf("%d request records, want 3", len(reqs))
	}
	if reqs[0]["user"] != "usr_1" || reqs[0]["status"] != float64(200) {
		t.Errorf("the record of the authenticated request = %v, want status 200 and user usr_1", reqs[0])
	}
	for _, rec := range reqs[1:] {
		if _, has := rec["user"]; has || rec["status"] != float64(401) {
			t.Errorf("the record of a refused request = %v, want status 401 and no user", rec)
		}
	}
	if strings.Contains(a.Logs.String(), "bad") && strings.Contains(a.Logs.String(), "Bearer") {
		t.Errorf("a credential is in the log:\n%s", a.Logs.String())
	}
}

// TestManyCallersAtOnceAreAuthenticatedWithoutARace runs the guards from many
// goroutines, for the race detector.
// Verifies: 11-REQ-6.8
func TestManyCallersAtOnceAreAuthenticatedWithoutARace(t *testing.T) {
	a := newAuthHarness(t)
	for i := range 8 {
		a.Hub.AddUser(fmt.Sprintf("t%d", i), fmt.Sprintf("usr_%d", i), []string{"widgets"})
	}
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 20 {
				token := fmt.Sprintf("t%d", (g+i)%10) // two of them are unknown to hub
				want := 200
				if (g+i)%10 >= 8 {
					want = 401
				}
				if r := a.workspace(t, "widgets", token); r.Status != want {
					t.Errorf("%s: %d %s, want %d", token, r.Status, r.Body, want)
				}
				if i%7 == 0 {
					a.Clock.Advance(10 * time.Second)
				}
			}
		}()
	}
	wg.Wait()
}
