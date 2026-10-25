package api

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestBearerToken_ReadsTheAuthorizationHeader(t *testing.T) {
	for header, want := range map[string]string{
		"Bearer tok": "tok", "bearer tok": "tok", "BEARER tok": "tok", "Bearer   tok  ": "tok", "Bearer a.b-c_d~+/=": "a.b-c_d~+/=",
		"Bearer a b": "", "Bearer a\tb": "", "": "", "Bearer": "", "Bearer ": "", "Bearer \t": "",
		"Basic tok": "", "Bearertok": "", " Bearer tok": "", "Token tok": "",
	} {
		r := httptest.NewRequest("GET", "/", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		if got, ok := bearerToken(r); got != want || ok != (want != "") {
			t.Errorf("bearerToken(%q) = %q, %v; want %q", header, got, ok, want)
		}
	}
}

func TestAuth_AnUnusableCredentialIsRefusedWithoutAskingHub(t *testing.T) {
	e := newEnv(t)
	e.Hub.AddUser("tok", "usr_1", nil)
	for _, header := range []string{"", "Basic abc", "Bearer", "Token tok", "Bearer two tokens"} {
		var headers [][]string
		if header != "" {
			headers = append(headers, []string{"Authorization", header})
		}
		r := e.call(t, "GET", pingPath, nil, "", headers...)
		failed(t, fmt.Sprintf("Authorization %q", header), r, 401, CodeUnauthenticated)
		if r.Header.Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("Authorization %q: WWW-Authenticate = %q, want Bearer", header, r.Header.Get("WWW-Authenticate"))
		}
	}
	if n := e.Hub.Total(); n != 0 {
		t.Errorf("hub was asked %d times about credentials that are not usable, want never", n)
	}
	if r := e.call(t, "GET", pingPath, nil, "", []string{"Authorization", "bearer tok"}); r.Status != 200 || r.Object(t)["user"] != "usr_1" {
		t.Errorf("a lower-case scheme: %d %s, want 200 for usr_1", r.Status, r.Body)
	}
}

func TestAuth_TheIdentityIsKeptForTheTTLByTheHashOfTheToken(t *testing.T) {
	e := newEnv(t)
	e.Hub.AddUser("t1", "usr_1", nil)
	for range 3 {
		e.get(t, pingPath, "t1")
	}
	e.Clock.Advance(cacheTTL - time.Second)
	e.get(t, pingPath, "t1")
	if n := e.userCalls("t1"); n != 1 {
		t.Errorf("hub was asked %d times within the TTL, want once", n)
	}
	e.Clock.Advance(2 * time.Second)
	if r := e.get(t, pingPath, "t1"); r.Status != 200 || e.userCalls("t1") != 2 {
		t.Errorf("after the TTL: %d, hub asked %d times; want 200 and a second call", r.Status, e.userCalls("t1"))
	}
	if _, ok := e.srv.auth.identities.Get(testutil.SHA256("t1")); !ok {
		t.Error("no identity is kept under the hex SHA-256 of the token")
	}
	if _, ok := e.srv.auth.identities.Get("t1"); ok {
		t.Error("an identity is kept under the token itself")
	}
}

func TestAuth_ARefusalIsKeptForTheTTL(t *testing.T) {
	for _, status := range []int{401, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			e := newEnv(t)
			e.Hub.AddUser("late", "usr_7", nil)
			e.Hub.FailFor(userPath, "late", status)
			for range 2 {
				failed(t, "a refused token", e.get(t, pingPath, "late"), 401, CodeUnauthenticated)
			}
			if n := e.userCalls("late"); n != 1 {
				t.Errorf("hub was asked %d times within the TTL, want the refusal kept after one", n)
			}
			e.Hub.Clear()
			e.Clock.Advance(cacheTTL)
			if r := e.get(t, pingPath, "late"); r.Status != 200 {
				t.Errorf("after the refusal expired: %d %s, want 200", r.Status, r.Body)
			}
		})
	}
}

func TestAuth_HubUnavailableIs502AndIsNotKept(t *testing.T) {
	for name, tc := range map[string]struct {
		path string
		fail func(e *env, path string)
	}{
		"a 500 on the identity":     {userPath, func(e *env, p string) { e.Hub.Fail(p, 500) }},
		"a 429 on the identity":     {userPath, func(e *env, p string) { e.Hub.Fail(p, 429) }},
		"a hang-up on the identity": {userPath, func(e *env, p string) { e.Hub.Hangup(p) }},
		"a 503 on the visibility":   {"/api/v1/workspaces/widgets", func(e *env, p string) { e.Hub.Fail(p, 503) }},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.Hub.AddUser("t", "usr_1", []string{"widgets"})
			tc.fail(e, tc.path)
			failed(t, "hub down", e.get(t, slugPath+"widgets", "t"), 502, CodeHubUnavailable)
			if len(e.Log.Find("WARN", "hub is unavailable")) != 1 {
				t.Errorf("no WARN record says hub is unavailable:\n%s", e.Log)
			}
			e.Hub.Clear()
			before := e.Hub.Count(tc.path, testutil.SHA256("t"))
			for range 2 {
				if r := e.get(t, slugPath+"widgets", "t"); r.Status != 200 {
					t.Errorf("after hub came back: %d %s, want 200", r.Status, r.Body)
				}
			}
			if n := e.Hub.Count(tc.path, testutil.SHA256("t")) - before; n != 1 {
				t.Errorf("hub was asked %d times after it came back, want once: the failure was not kept, the answer was", n)
			}
		})
	}
}

func TestAuth_TheVisibilityIsKeptPerTokenAndWorkspace(t *testing.T) {
	e := newEnv(t)
	e.Hub.AddUser("A", "usr_1", []string{"widgets", "gadgets"})
	e.Hub.AddUser("B", "usr_2", nil)
	for range 3 {
		failed(t, "B on widgets", e.get(t, slugPath+"widgets", "B"), 404, CodeNotFound)
		if r := e.get(t, slugPath+"widgets", "A"); r.Status != 200 || r.Object(t)["slug"] != "widgets" || r.Object(t)["user"] != "usr_1" {
			t.Fatalf("A on widgets: %d %s, want 200 with the slug and the user", r.Status, r.Body)
		}
	}
	e.get(t, slugPath+"gadgets", "A")
	if e.workspaceCalls("B", "widgets") != 1 || e.workspaceCalls("A", "widgets") != 1 || e.workspaceCalls("A", "gadgets") != 1 {
		t.Errorf("hub was asked %d, %d and %d times, want once for each token and workspace",
			e.workspaceCalls("B", "widgets"), e.workspaceCalls("A", "widgets"), e.workspaceCalls("A", "gadgets"))
	}
	e.Clock.Advance(cacheTTL + time.Second)
	e.get(t, slugPath+"widgets", "B")
	e.get(t, slugPath+"widgets", "A")
	if e.workspaceCalls("B", "widgets") != 2 || e.workspaceCalls("A", "widgets") != 2 {
		t.Error("hub was not asked again after the TTL")
	}
	if one, two := e.get(t, slugPath+"widgets", "B"), e.get(t, slugPath+"other", "B"); one.Body != two.Body || strings.Contains(one.Body, "widgets") {
		t.Errorf("the 404 depends on the workspace: %q and %q", one.Body, two.Body)
	}
}

func TestAuth_TheSlugIsCheckedBeforeTheCaller(t *testing.T) {
	e := newEnv(t)
	for _, slug := range []string{"-bad", ".dot", "_under", "a%20b", "x!", "a%2Fb", "a" + strings.Repeat("b", 100), "%C3%A9", "a%00b"} {
		failed(t, "the slug "+slug, e.get(t, slugPath+slug, "refused"), 400, CodeInvalidRequest)
		failed(t, "the slug "+slug+" without a token", e.get(t, slugPath+slug, ""), 400, CodeInvalidRequest)
	}
	if n := e.Hub.Total(); n != 0 {
		t.Errorf("hub was asked %d times about invalid slugs, want never", n)
	}
	valid := []string{"a", "A.b_c-1", "a" + strings.Repeat("b", 99), "0"}
	e.Hub.AddUser("good", "usr_1", valid)
	for _, slug := range valid {
		if r := e.get(t, slugPath+slug, "good"); r.Status != 200 || r.Object(t)["slug"] != slug {
			t.Errorf("the slug %q: %d %s, want 200", slug, r.Status, r.Body)
		}
	}
}

func TestAuth_TheIdentityIsSettledBeforeTheVisibility(t *testing.T) {
	e := newEnv(t)
	e.Hub.AddUser("t", "usr_1", []string{"widgets"})
	e.Hub.Fail(userPath, 500)
	failed(t, "hub down on the identity", e.get(t, slugPath+"widgets", "t"), 502, CodeHubUnavailable)
	e.Hub.Clear()
	failed(t, "a token hub refuses", e.get(t, slugPath+"widgets", "nobody"), 401, CodeUnauthenticated)
	if n := e.workspaceCalls("t", "widgets") + e.workspaceCalls("nobody", "widgets"); n != 0 {
		t.Errorf("hub was asked %d times about the workspace before the caller was known, want never", n)
	}
}

func TestAuth_HubRefusingTheTokenForgetsEverythingKeptAboutIt(t *testing.T) {
	e := newEnv(t)
	e.Hub.AddUser("t", "usr_1", []string{"widgets", "gadgets", "gizmos"})
	e.Hub.AddUser("other", "usr_2", []string{"widgets"})
	e.get(t, slugPath+"widgets", "t")
	e.get(t, slugPath+"gadgets", "t")
	e.get(t, slugPath+"widgets", "other")

	e.Hub.FailOnce("/api/v1/workspaces/gizmos", 401)
	failed(t, "hub refuses on the workspace call", e.get(t, slugPath+"gizmos", "t"), 401, CodeUnauthenticated)
	for _, slug := range []string{"widgets", "gadgets"} {
		if identity, visibility := e.cached("t", slug); identity || visibility {
			t.Errorf("%s: the identity (%v) or the visibility (%v) of the refused token is still kept", slug, identity, visibility)
		}
	}
	if _, visibility := e.cached("other", "widgets"); !visibility {
		t.Error("the entries of another token were forgotten")
	}
	if r := e.get(t, slugPath+"widgets", "t"); r.Status != 200 || e.userCalls("t") != 2 || e.workspaceCalls("t", "widgets") != 2 {
		t.Errorf("the next request: %d, %d identity and %d visibility calls; want 200 and both asked again", r.Status, e.userCalls("t"), e.workspaceCalls("t", "widgets"))
	}
}

func TestAuth_ManyCallersAtOnce(t *testing.T) {
	e := newEnv(t)
	for i := range 8 {
		e.Hub.AddUser(fmt.Sprintf("t%d", i), fmt.Sprintf("usr_%d", i), []string{"widgets"})
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 10 {
				token, want := fmt.Sprintf("t%d", (g+i)%10), 200
				if (g+i)%10 >= 8 {
					want = 401 // two of the tokens are unknown to hub
				}
				if r := e.get(t, slugPath+"widgets", token); r.Status != want {
					t.Errorf("%s: %d %s, want %d", token, r.Status, r.Body, want)
				}
				if i%4 == 0 {
					e.Clock.Advance(10 * time.Second)
				}
			}
		})
	}
	wg.Wait()
}
