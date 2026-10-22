package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

// put makes the PUT that subscribes slug, as the caller, with the body.
func (a *authHarness) put(t *testing.T, slug, caller, body string, headers ...string) response {
	t.Helper()
	all := append([]string{"Authorization", "Bearer " + caller, "Content-Type", "application/json"}, headers...)
	return a.do(t, "PUT", "/api/v1/workspaces/"+slug, strings.NewReader(body), all...)
}

// tokenBody is the body of a subscription with the token and, when given, the label prefix.
func tokenBody(t *testing.T, token string, prefix ...string) string {
	t.Helper()
	body := map[string]any{"hub_token": token}
	if len(prefix) > 0 {
		body["label_prefix"] = prefix[0]
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// rows is how many subscriptions the store holds.
func (a *authHarness) rows(t *testing.T) int {
	t.Helper()
	var n int
	if err := a.Store.DB().QueryRowContext(ctx, "SELECT count(*) FROM af_subscriptions").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// viewKeys are the fields of a subscription view.
var viewKeys = []string{"created_at", "label_prefix", "slug", "subscribed_by", "token_state", "token_user", "updated_at"}

// keysOf are the sorted keys of a JSON object.
func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// TestANewSubscriptionAnswers201WithTheViewAndStoresASealedRow_TS_11_55 verifies TS-11-55:
// A new subscription answers 201 with the view and stores a sealed row.
// Verifies: 11-REQ-8.1
func TestANewSubscriptionAnswers201WithTheViewAndStoresASealedRow_TS_11_55(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("caller", "usr_1", []string{"widgets", "gadgets"})
	a.Hub.AddUser("pat-9", "usr_9", []string{"widgets", "gadgets"})

	r := a.put(t, "widgets", "caller", `{"hub_token":"pat-9","label_prefix":"x:"}`)
	if r.Status != 201 || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("PUT = %d %s, want 201 application/json", r.Status, r.Body)
	}
	view := r.JSON(t)
	if got := keysOf(view); !slices.Equal(got, viewKeys) {
		t.Errorf("the view has the fields %v, want exactly %v", got, viewKeys)
	}
	if view["slug"] != "widgets" || view["label_prefix"] != "x:" || view["token_user"] != "usr_9" ||
		view["subscribed_by"] != "usr_1" || view["token_state"] != "ok" {
		t.Errorf("view = %s, want widgets, x:, usr_9, usr_1 and ok", r.Body)
	}
	created, err1 := time.Parse(time.RFC3339, view["created_at"].(string))
	updated, err2 := time.Parse(time.RFC3339, view["updated_at"].(string))
	if err1 != nil || err2 != nil || !created.Equal(updated) || !created.Equal(a.Clock.Now()) {
		t.Errorf("created_at %v and updated_at %v (%v, %v), want both the clock, %v", view["created_at"], view["updated_at"], err1, err2, a.Clock.Now())
	}
	if strings.Contains(r.Body, "pat-9") {
		t.Errorf("the answer holds the token: %s", r.Body)
	}
	if n := a.rows(t); n != 1 {
		t.Errorf("%d rows, want 1", n)
	}
	var first string
	if err := a.Store.DB().QueryRowContext(ctx, "SELECT hex(substr(sealed_token, 1, 1)) FROM af_subscriptions WHERE slug = 'widgets'").Scan(&first); err != nil || first != "01" {
		t.Errorf("the sealed token starts with %q (%v), want 01", first, err)
	}

	// Without a label_prefix, the prefix is af:.
	r = a.put(t, "gadgets", "caller", `{"hub_token":"pat-9"}`)
	if r.Status != 201 || r.JSON(t)["label_prefix"] != "af:" {
		t.Errorf("PUT without a label_prefix = %d %s, want 201 and af:", r.Status, r.Body)
	}
}

// TestReplacingASubscriptionAnswers200AndReplacesTheRowInFull_TS_11_56 verifies TS-11-56:
// Replacing a subscription answers 200 and replaces the row in full keeping created_at.
// Verifies: 11-REQ-8.2
func TestReplacingASubscriptionAnswers200AndReplacesTheRowInFull_TS_11_56(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("c1", "usr_1", []string{"widgets"})
	a.Hub.AddUser("c2", "usr_2", []string{"widgets"})
	a.Hub.AddUser("pat-1", "usr_9", []string{"widgets"})
	a.Hub.AddWorkspaceToken("pat-2", []string{"widgets"}) // /user refuses it

	first := a.put(t, "widgets", "c1", `{"hub_token":"pat-1","label_prefix":"x:"}`)
	if first.Status != 201 {
		t.Fatalf("PUT = %d %s", first.Status, first.Body)
	}
	a.Clock.Advance(2 * time.Hour)

	r := a.put(t, "widgets", "c2", `{"hub_token":"pat-2"}`)
	if r.Status != 200 {
		t.Fatalf("the replacement = %d %s, want 200", r.Status, r.Body)
	}
	view, old := r.JSON(t), first.JSON(t)
	if view["label_prefix"] != "af:" || view["token_user"] != nil || view["subscribed_by"] != "usr_2" || view["token_state"] != "ok" {
		t.Errorf("view = %s, want af:, null, usr_2 and ok", r.Body)
	}
	if view["created_at"] != old["created_at"] {
		t.Errorf("created_at = %v, want it kept: %v", view["created_at"], old["created_at"])
	}
	updated, _ := time.Parse(time.RFC3339, view["updated_at"].(string))
	if before, _ := time.Parse(time.RFC3339, old["updated_at"].(string)); !updated.After(before) {
		t.Errorf("updated_at = %v, want it after %v", updated, before)
	}
	if n := a.rows(t); n != 1 {
		t.Errorf("%d rows, want 1", n)
	}
	if token, err := a.Store.Token(ctx, "widgets"); err != nil || token != "pat-2" {
		t.Errorf("Token = %q, %v; want the new token pat-2", token, err)
	}
}

// TestBadFieldsAreRefusedWithoutCallingHubWithTheTokenOrWriting_TS_11_57 verifies TS-11-57:
// A missing or empty hub_token or a bad label_prefix answers 400 without calling
// hub with the PAT or writing.
// Verifies: 11-REQ-8.3
func TestBadFieldsAreRefusedWithoutCallingHubWithTheTokenOrWriting_TS_11_57(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("caller", "usr_1", []string{"widgets"})
	a.Hub.AddUser("pat", "usr_9", []string{"widgets"})

	for name, body := range map[string]string{
		"no hub_token":               `{}`,
		"an empty hub_token":         `{"hub_token":""}`,
		"a null hub_token":           `{"hub_token":null}`,
		"a hub_token with a newline": tokenBody(t, "pat\nx"),
		"a hub_token with a NUL":     tokenBody(t, "pat\x00"),
		"an empty label_prefix":      tokenBody(t, "pat", ""),
		"33 characters":              tokenBody(t, "pat", strings.Repeat("a", 33)),
		"33 multi-byte characters":   tokenBody(t, "pat", strings.Repeat("é", 33)),
		"a space":                    tokenBody(t, "pat", "a b"),
		"a trailing space":           tokenBody(t, "pat", "af: "),
		"a tab":                      tokenBody(t, "pat", "a\tb"),
		"a newline":                  tokenBody(t, "pat", "a\nb"),
		"a control character":        tokenBody(t, "pat", "a\x07b"),
		"a no-break space":           tokenBody(t, "pat", "a b"),
		"a comma":                    tokenBody(t, "pat", "a,b"),
		"a label_prefix of a number": `{"hub_token":"pat","label_prefix":5}`,
	} {
		r := a.put(t, "widgets", "caller", body)
		if r.Status != 400 || r.errorCode(t) != "invalid_request" {
			t.Errorf("%s: %d %s, want 400 invalid_request", name, r.Status, r.Body)
		}
	}
	if n := a.Hub.CountFor(fakehub.TokenHash("pat")); n != 0 {
		t.Errorf("hub saw %d requests carrying the token, want none", n)
	}
	if n := a.rows(t); n != 0 {
		t.Errorf("%d rows after the refused requests, want none", n)
	}

	for name, prefix := range map[string]string{"32 characters": strings.Repeat("a", 32), "32 multi-byte characters": strings.Repeat("é", 32), "one character": "x", "punctuation": "af:/_-"} {
		slug := "widgets"
		if r := a.put(t, slug, "caller", tokenBody(t, "pat", prefix)); r.Status != 201 && r.Status != 200 {
			t.Errorf("%s: %d %s, want it accepted", name, r.Status, r.Body)
		}
	}
}

// TestSubscribingMakesTwoFreshCallsWithTheTokenBypassingTheCaches_TS_11_58 verifies TS-11-58:
// Subscribe makes two fresh PAT calls, gate first then user best effort,
// bypassing both caches.
// Verifies: 11-REQ-8.4
func TestSubscribingMakesTwoFreshCallsWithTheTokenBypassingTheCaches_TS_11_58(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("caller", "usr_1", []string{"widgets"})
	a.Hub.AddWorkspaceToken("pat", []string{"widgets"}) // sees widgets, refused by /user
	if r := a.ping(t, "caller"); r.Status != 200 {
		t.Fatalf("warming the caches: %d", r.Status)
	}
	a.workspace(t, "widgets", "caller")

	for range 2 {
		if r := a.put(t, "widgets", "caller", tokenBody(t, "pat")); r.Status != 201 && r.Status != 200 {
			t.Fatalf("PUT = %d %s", r.Status, r.Body)
		} else if r.JSON(t)["token_user"] != nil {
			t.Errorf("token_user = %v, want null when hub refuses the token on /user", r.JSON(t)["token_user"])
		}
	}
	var paths []string
	for _, req := range a.Hub.RequestsFor(fakehub.TokenHash("pat")) {
		paths = append(paths, req.Path)
	}
	want := []string{workspacePath + "widgets", userPath, workspacePath + "widgets", userPath}
	if !slices.Equal(paths, want) {
		t.Errorf("hub saw %v with the token, want %v: the gate before the user call, both repeated", paths, want)
	}
	if n := a.userCalls("caller"); n != 1 {
		t.Errorf("the caller was put to hub %d times, want once: the caches serve the caller", n)
	}

	// The token did not make an entry that serves it as a caller.
	if _, ok := a.srv.auth.identities.Get(hashToken("pat")); ok {
		t.Error("the identity cache has an entry for the token of a subscription")
	}
	if _, ok := a.srv.auth.visibility.Get(visibilityKey{hashToken("pat"), "widgets"}); ok {
		t.Error("the visibility cache has an entry for the token of a subscription")
	}
	before := a.userCalls("pat")
	if r := a.ping(t, "pat"); r.Status != 401 {
		t.Errorf("the token of a subscription as a caller: %d, want hub's answer to /user, 401", r.Status)
	}
	if n := a.userCalls("pat"); n != before+1 {
		t.Errorf("hub was asked %d times about the token as a caller, want %d", n, before+1)
	}
}

// TestATokenHubRefusesOnTheGateAnswers422AndWritesNothing_TS_11_59 verifies TS-11-59:
// A PAT hub refuses on the gate answers 422 invalid_hub_token and writes nothing.
// Verifies: 11-REQ-8.5
func TestATokenHubRefusesOnTheGateAnswers422AndWritesNothing_TS_11_59(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("caller", "usr_1", []string{"widgets"})
	a.ping(t, "caller")
	a.workspace(t, "widgets", "caller")
	users, workspaces := a.userCalls("caller"), a.workspaceCalls("caller", "widgets")

	r := a.put(t, "widgets", "caller", tokenBody(t, "unknown-pat"))
	if r.Status != 422 || r.errorCode(t) != "invalid_hub_token" {
		t.Fatalf("PUT = %d %s, want 422 invalid_hub_token", r.Status, r.Body)
	}
	if msg := r.JSON(t)["error"].(map[string]any)["message"].(string); !strings.Contains(msg, "refused") {
		t.Errorf("message = %q, want it to say that hub refused the token", msg)
	}
	if n := a.rows(t); n != 0 {
		t.Errorf("%d rows, want none", n)
	}
	if n := a.Hub.Count(userPath, fakehub.TokenHash("unknown-pat")); n != 0 {
		t.Errorf("the user call was made %d times after the gate refused, want none", n)
	}
	if _, ok := a.srv.auth.identities.Get(hashToken("caller")); !ok {
		t.Error("the caller's identity entry is gone")
	}
	if r := a.workspace(t, "widgets", "caller"); r.Status != 200 {
		t.Errorf("the caller afterwards: %d", r.Status)
	}
	if a.userCalls("caller") != users || a.workspaceCalls("caller", "widgets") != workspaces {
		t.Errorf("the caller was put to hub again: %d user calls (was %d), %d workspace calls (was %d)",
			a.userCalls("caller"), users, a.workspaceCalls("caller", "widgets"), workspaces)
	}
}

// TestATokenThatDoesNotFindTheWorkspaceAnswers422NamingIt_TS_11_60 verifies TS-11-60:
// A PAT that does not find the workspace (403 or 404) answers 422 naming the
// workspace and writes nothing.
// Verifies: 11-REQ-8.6
func TestATokenThatDoesNotFindTheWorkspaceAnswers422NamingIt_TS_11_60(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("caller", "usr_1", []string{"widgets"})
	a.Hub.AddUser("p403", "usr_8", []string{"widgets"})
	a.Hub.AddUser("p404", "usr_9", []string{"widgets"})
	a.Hub.FailFor(workspacePath+"widgets", "p403", 403)
	a.Hub.FailFor(workspacePath+"widgets", "p404", 404)
	a.Hub.AddUser("elsewhere", "usr_7", []string{"gadgets"}) // a token that does not see the workspace

	for _, pat := range []string{"p403", "p404", "elsewhere"} {
		r := a.put(t, "widgets", "caller", tokenBody(t, pat))
		if r.Status != 422 || r.errorCode(t) != "invalid_hub_token" {
			t.Errorf("%s: %d %s, want 422 invalid_hub_token", pat, r.Status, r.Body)
		}
		if msg := r.JSON(t)["error"].(map[string]any)["message"].(string); !strings.Contains(msg, "widgets") {
			t.Errorf("%s: message = %q, want it to name the workspace", pat, msg)
		}
	}
	if n := a.rows(t); n != 0 {
		t.Errorf("%d rows, want none", n)
	}
}

// TestHubBeingUnavailableForTheTokenAnswers502AndWritesNothing_TS_11_61 verifies TS-11-61:
// Hub unavailable on the gate or the user call with the PAT answers 502 and
// writes nothing.
// Verifies: 11-REQ-8.7
func TestHubBeingUnavailableForTheTokenAnswers502AndWritesNothing_TS_11_61(t *testing.T) {
	for name, route := range map[string]string{"the gate": workspacePath + "widgets", "the user call": userPath} {
		t.Run(name, func(t *testing.T) {
			a := newAuthHarness(t)
			a.Hub.AddUser("caller", "usr_1", []string{"widgets"})
			a.Hub.AddUser("pat", "usr_9", []string{"widgets"})
			a.ping(t, "caller")
			a.workspace(t, "widgets", "caller")
			users, workspaces := a.userCalls("caller"), a.workspaceCalls("caller", "widgets")

			a.Hub.FailFor(route, "pat", 500)
			r := a.put(t, "widgets", "caller", tokenBody(t, "pat"))
			if r.Status != 502 || r.errorCode(t) != "hub_unavailable" {
				t.Errorf("PUT = %d %s, want 502 hub_unavailable", r.Status, r.Body)
			}
			if n := a.rows(t); n != 0 {
				t.Errorf("%d rows, want none", n)
			}
			if _, ok := a.srv.auth.identities.Get(hashToken("pat")); ok {
				t.Error("an identity entry was made for the token of the subscription")
			}
			if _, ok := a.srv.auth.visibility.Get(visibilityKey{hashToken("pat"), "widgets"}); ok {
				t.Error("a visibility entry was made for the token of the subscription")
			}
			a.workspace(t, "widgets", "caller")
			if a.userCalls("caller") != users || a.workspaceCalls("caller", "widgets") != workspaces {
				t.Error("the caller's entries were touched")
			}
		})
	}
}

// TestACallToHubWithTheTokenNeverTouchesTheCallersCaches_TS_11_44 verifies TS-11-44:
// PAT calls never read, fill or evict the caches even when the PAT equals the
// caller token.
// Verifies: 11-REQ-6.7
func TestACallToHubWithTheTokenNeverTouchesTheCallersCaches_TS_11_44(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("t", "usr_1", []string{"widgets"})
	hash := fakehub.TokenHash("t")

	// The caller's identity and visibility are cached by the first request, and a
	// subscription made with the caller's own token reaches hub regardless.
	if r := a.put(t, "widgets", "t", tokenBody(t, "t")); r.Status != 201 {
		t.Fatalf("PUT with the caller's token as the token = %d %s", r.Status, r.Body)
	}
	if n := a.workspaceCalls("t", "widgets"); n != 2 {
		t.Errorf("hub saw %d workspace calls with the token, want 2: the caller's check and the gate, which bypasses the cache", n)
	}
	if n := a.userCalls("t"); n != 2 {
		t.Errorf("hub saw %d user calls with the token, want 2: the caller's and the best effort one", n)
	}
	if r := a.put(t, "widgets", "t", tokenBody(t, "t")); r.Status != 200 {
		t.Fatalf("the second PUT = %d", r.Status)
	}
	if n := a.workspaceCalls("t", "widgets"); n != 3 {
		t.Errorf("hub saw %d workspace calls, want 3: the caller's check is cached, the gate is not", n)
	}

	// Hub now refuses the token on the workspace call. The caller's entries are
	// cached, so the caller is not put to hub; the gate is, and refuses: 422, and
	// not the 401 of a caller whom hub does not accept, and nothing is evicted.
	a.Hub.FailFor(workspacePath+"widgets", "t", 401)
	r := a.put(t, "widgets", "t", tokenBody(t, "t"))
	if r.Status != 422 || r.errorCode(t) != "invalid_hub_token" {
		t.Errorf("the token refused on the gate = %d %s, want 422 invalid_hub_token", r.Status, r.Body)
	}
	if _, ok := a.srv.auth.identities.Get(hashToken("t")); !ok {
		t.Error("a refusal of the token as a PAT evicted the caller's identity")
	}
	if _, ok := a.srv.auth.visibility.Get(visibilityKey{hashToken("t"), "widgets"}); !ok {
		t.Error("a refusal of the token as a PAT evicted the caller's visibility")
	}
	users := a.Hub.Count(userPath, hash)
	a.Hub.Clear()
	if r := a.ping(t, "t"); r.Status != 200 {
		t.Errorf("the caller after the refused PAT = %d, want 200 from the cache", r.Status)
	}
	if n := a.Hub.Count(userPath, hash); n != users {
		t.Errorf("hub saw %d user calls, want %d: no extra call after the refusal of the PAT", n, users)
	}
}

// TestBadRequestBodiesAreRefusedBeforeAnyWrite_TS_11_50 verifies TS-11-50:
// Bad request bodies are refused before any write with 415, 413 or 400.
// Verifies: 11-REQ-7.4
func TestBadRequestBodiesAreRefusedBeforeAnyWrite_TS_11_50(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("caller", "usr_1", []string{"widgets"})
	a.Hub.AddUser("p", "usr_9", []string{"widgets"})

	for name, tc := range map[string]struct {
		headers []string
		body    string
		status  int
		code    string
	}{
		"text/plain":       {[]string{"Content-Type", "text/plain"}, `{"hub_token":"p"}`, 415, "unsupported_media_type"},
		"no content type":  {[]string{"Content-Type", ""}, `{"hub_token":"p"}`, 415, "unsupported_media_type"},
		"over 64 KiB":      {nil, `{"hub_token":"` + strings.Repeat("x", 65536) + `"}`, 413, "payload_too_large"},
		"malformed JSON":   {nil, `{`, 400, "invalid_request"},
		"an unknown field": {nil, `{"hub_token":"p","x":1}`, 400, "invalid_request"},
		"trailing data":    {nil, `{"hub_token":"p"} {}`, 400, "invalid_request"},
		"an array":         {nil, `["p"]`, 400, "invalid_request"},
		"an empty body":    {nil, ``, 400, "invalid_request"},
	} {
		r := a.put(t, "widgets", "caller", tc.body, tc.headers...)
		if r.Status != tc.status || r.errorCode(t) != tc.code {
			t.Errorf("%s: %d %s, want %d %s", name, r.Status, r.Body, tc.status, tc.code)
		}
	}
	if n := a.Hub.CountFor(fakehub.TokenHash("p")); n != 0 {
		t.Errorf("hub saw %d requests carrying the token of a refused body", n)
	}
	if n := a.rows(t); n != 0 {
		t.Errorf("%d rows after the refused requests, want none", n)
	}
	if r := a.put(t, "widgets", "caller", `{"hub_token":"p"}`, "Content-Type", "application/json; charset=utf-8"); r.Status != 201 {
		t.Errorf("a charset parameter: %d %s, want 201", r.Status, r.Body)
	}
}

// TestOnlyACallerWhoSeesTheWorkspaceCanSubscribeIt checks the guards on the route:
// a caller whom hub refuses gets 401, one who does not see the workspace gets the
// 404 that an unknown workspace gets, and neither makes a call with the token or
// writes.
// Verifies: 11-REQ-6.5
func TestOnlyACallerWhoSeesTheWorkspaceCanSubscribeIt(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("blind", "usr_2", nil)
	a.Hub.AddUser("pat", "usr_9", []string{"widgets"})

	for name, tc := range map[string]struct {
		caller string
		slug   string
		status int
		code   string
	}{
		"a caller hub refuses":         {"nobody", "widgets", 401, "unauthenticated"},
		"a caller who does not see it": {"blind", "widgets", 404, "not_found"},
		"an invalid slug":              {"blind", "-x", 400, "invalid_request"},
		"a slug that does not exist":   {"blind", "nothing", 404, "not_found"},
	} {
		r := a.put(t, tc.slug, tc.caller, tokenBody(t, "pat"))
		if r.Status != tc.status || r.errorCode(t) != tc.code {
			t.Errorf("%s: %d %s, want %d %s", name, r.Status, r.Body, tc.status, tc.code)
		}
	}
	if r := a.do(t, "PUT", "/api/v1/workspaces/widgets", strings.NewReader(tokenBody(t, "pat")), "Content-Type", "application/json"); r.Status != 401 {
		t.Errorf("no credentials: %d, want 401", r.Status)
	}
	if n := a.Hub.CountFor(fakehub.TokenHash("pat")); n != 0 {
		t.Errorf("hub saw %d requests carrying the token, want none: nobody who may subscribe asked", n)
	}
	if n := a.rows(t); n != 0 {
		t.Errorf("%d rows, want none", n)
	}
	one, two := a.put(t, "widgets", "blind", tokenBody(t, "pat")), a.put(t, "gadgets", "blind", tokenBody(t, "pat"))
	if one.Body != two.Body {
		t.Errorf("the 404 differs by slug: %q and %q", one.Body, two.Body)
	}
}

// TestTheStoredTokenIsInNoFileAndNoAnswer_TS_11_62 verifies the writing half of
// TS-11-62 (the reading half is with the reads): a stored PAT's bytes appear in
// neither the database file, its WAL nor any response body.
// Verifies: 11-REQ-8.8
func TestTheStoredTokenIsInNoFileAndNoAnswer_TS_11_62(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 62))
	t.Logf("seed: PCG(11, 62)")
	a := newAuthHarness(t)
	a.Hub.AddUser("caller", "usr_1", nil)

	const printable = "!\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~"
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-"
	random := func(chars string, lo, hi int) string {
		b := make([]byte, lo+rng.IntN(hi-lo+1))
		for i := range b {
			b[i] = chars[rng.IntN(len(chars))]
		}
		return string(b)
	}

	path := ""
	if err := a.Store.DB().QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&path); err != nil || path == "" {
		t.Fatalf("the path of the database: %q, %v", path, err)
	}
	for i := range 40 {
		slug := fmt.Sprintf("w%d-%s", i, random(alphabet, 0, 20))
		prefix := strings.NewReplacer(",", "x").Replace(random(printable, 1, 32))
		first, second := random(printable, 8, 200), random(printable, 8, 200)
		a.Hub.Grant("usr_1", slug)
		a.Hub.AddUser(first, fmt.Sprintf("usr_a%d", i), []string{slug})
		a.Hub.AddUser(second, fmt.Sprintf("usr_b%d", i), []string{slug})

		for _, pat := range []string{first, second} { // a create, then a replacement
			r := a.put(t, slug, "caller", tokenBody(t, pat, prefix))
			if r.Status != 201 && r.Status != 200 {
				t.Fatalf("iteration %d: PUT = %d %s", i, r.Status, r.Body)
			}
			for _, other := range []string{first, second} {
				if strings.Contains(r.Body, other) {
					t.Fatalf("iteration %d: the answer holds the token %q: %s", i, other, r.Body)
				}
			}
			// before any checkpoint: the file and its WAL as they are right now
			for _, file := range []string{path, path + "-wal"} {
				if data, err := os.ReadFile(file); err == nil && (bytes.Contains(data, []byte(first)) || bytes.Contains(data, []byte(second))) {
					t.Fatalf("iteration %d: %s holds a token", i, file)
				}
			}
		}
	}
	if n := a.rows(t); n != 40 {
		t.Errorf("%d rows, want 40", n)
	}
	var tokens []string
	for _, slug := range column(t, a.Store.DB(), "SELECT slug FROM af_subscriptions ORDER BY slug") {
		token, err := a.Store.Token(ctx, slug)
		if err != nil {
			t.Fatalf("Token(%s): %v", slug, err)
		}
		tokens = append(tokens, token)
	}
	if err := a.Store.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range tokens {
		if bytes.Contains(data, []byte(token)) {
			t.Errorf("the database file holds a stored token after the close")
		}
	}
}

// column runs a query that answers one column and returns its values.
func column(t *testing.T, db interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}, query string) []string {
	t.Helper()
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}
