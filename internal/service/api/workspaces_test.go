package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/service/labelcleanup"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// seed stores a subscription directly, as a subscribe would have.
func (a *authHarness) seed(t *testing.T, slug, token string) store.Subscription {
	t.Helper()
	sub, _, err := a.Store.Upsert(ctx, slug, token, "usr_9", "usr_1", a.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

// list makes the request for the list of subscriptions and returns the slugs.
func (a *authHarness) list(t *testing.T, token string) (response, []string) {
	t.Helper()
	r := a.get(t, "/api/v1/workspaces", bearer(token)...)
	var slugs []string
	if r.Status == 200 {
		var body struct {
			Workspaces []struct {
				Slug string `json:"slug"`
			} `json:"workspaces"`
		}
		if err := json.Unmarshal([]byte(r.Body), &body); err != nil {
			t.Fatalf("the list is not JSON: %q", r.Body)
		}
		for _, w := range body.Workspaces {
			slugs = append(slugs, w.Slug)
		}
	}
	return r, slugs
}

// view makes the request for the subscription of a workspace.
func (a *authHarness) view(t *testing.T, slug, token string) response {
	t.Helper()
	return a.get(t, "/api/v1/workspaces/"+slug, bearer(token)...)
}

// remove makes the request that deletes the subscription of a workspace.
func (a *authHarness) remove(t *testing.T, slug, token string) response {
	t.Helper()
	return a.do(t, "DELETE", "/api/v1/workspaces/"+slug, nil, bearer(token)...)
}

// TestAHiddenWorkspaceIsAsAnUnsubscribedOneWithTheVisibilityCached_TS_11_41 verifies TS-11-41:
// A hidden workspace answers 404 byte-identical to an unsubscribed one, with
// visibility cached per (token, slug).
// Verifies: 11-REQ-6.5
func TestAHiddenWorkspaceIsAsAnUnsubscribedOneWithTheVisibilityCached_TS_11_41(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("A", "usr_1", []string{"widgets", "zzz"})
	a.Hub.AddUser("B", "usr_2", nil)
	a.seed(t, "widgets", "pat")

	hidden := a.view(t, "widgets", "B")   // subscribed, but B does not see it
	unsubscribed := a.view(t, "zzz", "A") // A sees it, but nobody subscribed it
	if hidden.Status != 404 || unsubscribed.Status != 404 || hidden.errorCode(t) != "not_found" {
		t.Fatalf("hidden = %d %s, unsubscribed = %d %s; want 404 not_found for both", hidden.Status, hidden.Body, unsubscribed.Status, unsubscribed.Body)
	}
	if hidden.Body != unsubscribed.Body {
		t.Errorf("the two 404 differ:\n%s\n%s", hidden.Body, unsubscribed.Body)
	}
	if strings.Contains(hidden.Body, "widgets") || strings.Contains(unsubscribed.Body, "zzz") {
		t.Error("a 404 names the slug")
	}

	a.view(t, "widgets", "B")
	if n := a.workspaceCalls("B", "widgets"); n != 1 {
		t.Errorf("hub was asked %d times about B and widgets, want the repeat served from the cache", n)
	}
	a.Clock.Advance(authTTL + time.Second)
	a.view(t, "widgets", "B")
	if n := a.workspaceCalls("B", "widgets"); n != 2 {
		t.Errorf("after the TTL hub was asked %d times, want 2", n)
	}

	// What hub shows is cached as well: the repeats of A are served from the cache.
	for range 3 {
		if r := a.view(t, "widgets", "A"); r.Status != 200 {
			t.Fatalf("A on widgets: %d %s", r.Status, r.Body)
		}
	}
	if n := a.workspaceCalls("A", "widgets"); n != 1 {
		t.Errorf("hub was asked %d times about A and widgets, want the found cached", n)
	}
	a.Clock.Advance(authTTL + time.Second)
	a.view(t, "widgets", "A")
	if n := a.workspaceCalls("A", "widgets"); n != 2 {
		t.Errorf("after the TTL hub was asked %d times about A and widgets, want 2", n)
	}
}

// TestTheViewHasExactlySixFieldsAndNoToken_TS_11_63 verifies TS-11-63:
// The subscription view has exactly the six fields, null token_user and no
// token or ciphertext.
// Verifies: 11-REQ-9.1
func TestTheViewHasExactlySixFieldsAndNoToken_TS_11_63(t *testing.T) {
	created := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	for name, user := range map[string]string{"no token user": "", "a token user": "usr_9"} {
		sub := store.Subscription{Slug: "widgets", TokenUser: user, SubscribedBy: "usr_1", TokenState: store.TokenOK, CreatedAt: created, UpdatedAt: created.Add(time.Hour)}
		data, err := json.Marshal(viewOf(sub))
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		if got := keysOf(doc); !slices.Equal(got, viewKeys) {
			t.Errorf("%s: the keys are %v, want exactly %v", name, got, viewKeys)
		}
		if user == "" && doc["token_user"] != nil || user != "" && doc["token_user"] != user {
			t.Errorf("%s: token_user = %v, want null for none and the id otherwise", name, doc["token_user"])
		}
		if doc["created_at"] != "2026-10-22T08:00:00Z" || doc["updated_at"] != "2026-10-22T09:00:00Z" {
			t.Errorf("%s: view = %s", name, data)
		}
		for key := range doc {
			for _, forbidden := range []string{"hub_token", "sealed", "cipher", "secret", "password"} {
				if strings.Contains(strings.ToLower(key), forbidden) {
					t.Errorf("%s: the view has the key %q", name, key)
				}
			}
		}
	}
	// What the view is made of cannot hold a token: the type has these six fields.
	if n := reflect.TypeFor[subscriptionView]().NumField(); n != 6 {
		t.Errorf("subscriptionView has %d fields, want 6", n)
	}
}

// TestTheListHoldsTheVisibleSubscriptionsSortedBySlug_TS_11_64 verifies TS-11-64:
// GET /workspaces lists only visible subscriptions sorted by slug.
// Verifies: 11-REQ-9.2
func TestTheListHoldsTheVisibleSubscriptionsSortedBySlug_TS_11_64(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("A", "usr_1", []string{"zeta", "widgets", "alpha"})
	a.Hub.AddUser("B", "usr_2", []string{"alpha", "unsubscribed"})
	a.Hub.AddUser("C", "usr_3", nil)
	for _, slug := range []string{"zeta", "widgets", "alpha"} {
		a.seed(t, slug, "pat-"+slug)
	}

	r, slugs := a.list(t, "A")
	if r.Status != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || !slices.Equal(slugs, []string{"alpha", "widgets", "zeta"}) {
		t.Errorf("A's list = %d %s, want alpha, widgets and zeta in that order", r.Status, r.Body)
	}
	var body struct {
		Workspaces []map[string]any `json:"workspaces"`
	}
	if err := json.Unmarshal([]byte(r.Body), &body); err != nil {
		t.Fatal(err)
	}
	for _, item := range body.Workspaces {
		if got := keysOf(item); !slices.Equal(got, viewKeys) {
			t.Errorf("an item of the list has the fields %v, want %v", got, viewKeys)
		}
	}
	for _, token := range []string{"pat-zeta", "pat-widgets", "pat-alpha"} {
		if strings.Contains(r.Body, token) {
			t.Errorf("the list holds the token %s", token)
		}
	}

	if _, slugs := a.list(t, "B"); !slices.Equal(slugs, []string{"alpha"}) {
		t.Errorf("B's list = %v, want only alpha", slugs)
	}
	r, slugs = a.list(t, "C")
	if r.Status != 200 || len(slugs) != 0 {
		t.Errorf("C's list = %d %s", r.Status, r.Body)
	}
	if r.Body != "{\"workspaces\":[]}\n" {
		t.Errorf("the list of a caller who sees nothing = %q, want an empty array and not null", r.Body)
	}
}

// TestTheListIsNotPartialWhenHubFails_TS_11_65 verifies TS-11-65:
// Listing answers 502 with no partial list when hub is unavailable for any
// subscription.
// Verifies: 11-REQ-9.3
func TestTheListIsNotPartialWhenHubFails_TS_11_65(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("A", "usr_1", []string{"alpha", "widgets", "zeta"})
	for _, slug := range []string{"alpha", "widgets", "zeta"} {
		a.seed(t, slug, "pat")
	}

	a.Hub.Fail(workspacePath+"widgets", 500)
	r, _ := a.list(t, "A")
	if r.Status != 502 || r.errorCode(t) != "hub_unavailable" {
		t.Fatalf("list = %d %s, want 502 hub_unavailable", r.Status, r.Body)
	}
	if _, has := r.JSON(t)["workspaces"]; has || strings.Contains(r.Body, "alpha") {
		t.Errorf("the body holds a partial list: %s", r.Body)
	}
	a.Hub.Clear()
	if r, slugs := a.list(t, "A"); r.Status != 200 || len(slugs) != 3 {
		t.Errorf("list after recovery = %d %s, want all three", r.Status, r.Body)
	}

	t.Run("a hub refusal is no partial list either", func(t *testing.T) {
		a := newAuthHarness(t)
		a.Hub.AddUser("A", "usr_1", []string{"alpha", "widgets"})
		a.seed(t, "alpha", "pat")
		a.seed(t, "widgets", "pat")
		a.Hub.FailOnce(workspacePath+"widgets", 401)
		r, _ := a.list(t, "A")
		if r.Status != 401 || r.errorCode(t) != "unauthenticated" || strings.Contains(r.Body, "alpha") {
			t.Errorf("list = %d %s, want 401 unauthenticated and no list", r.Status, r.Body)
		}
		if _, ok := a.srv.auth.identities.Get(hashToken("A")); ok {
			t.Error("the identity of a token that hub refused is still cached")
		}
	})
}

// TestAWorkspaceIsShownOrNotFound_TS_11_66 verifies TS-11-66:
// GET /workspaces/{slug} returns the view, or an identical 404 for unsubscribed
// and invisible slugs.
// Verifies: 11-REQ-9.4
func TestAWorkspaceIsShownOrNotFound_TS_11_66(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("A", "usr_1", []string{"widgets"})
	a.Hub.AddUser("B", "usr_2", []string{"nothing"})
	a.seed(t, "widgets", "pat")

	r := a.view(t, "widgets", "A")
	if r.Status != 200 {
		t.Fatalf("A on widgets: %d %s", r.Status, r.Body)
	}
	view := r.JSON(t)
	if got := keysOf(view); !slices.Equal(got, viewKeys) || view["slug"] != "widgets" || view["token_state"] != "ok" || view["subscribed_by"] != "usr_1" || view["token_user"] != "usr_9" {
		t.Errorf("view = %s", r.Body)
	}

	hidden, unsubscribed := a.view(t, "widgets", "B"), a.view(t, "nothing", "B")
	if hidden.Status != 404 || unsubscribed.Status != 404 || hidden.Body != unsubscribed.Body {
		t.Errorf("hidden = %d %s, unsubscribed = %d %s; want the same 404", hidden.Status, hidden.Body, unsubscribed.Status, unsubscribed.Body)
	}
	if strings.Contains(hidden.Body, "widgets") || strings.Contains(hidden.Body, "subscri") {
		t.Errorf("the 404 gives away that the workspace is subscribed: %s", hidden.Body)
	}
}

// TestADeleteRemovesTheRowAndItsToken_TS_11_67 verifies TS-11-67:
// DELETE removes the row and sealed token and answers 204, or 404 under the GET rule.
// Verifies: 11-REQ-9.5
func TestADeleteRemovesTheRowAndItsToken_TS_11_67(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("A", "usr_1", []string{"widgets"})
	a.Hub.AddUser("B", "usr_2", nil)
	a.seed(t, "widgets", "pat")

	if r := a.remove(t, "widgets", "B"); r.Status != 404 || r.errorCode(t) != "not_found" {
		t.Errorf("B's DELETE = %d %s, want 404 not_found", r.Status, r.Body)
	}
	if n := a.rows(t); n != 1 {
		t.Fatalf("%d rows after a refused DELETE, want the row to remain", n)
	}

	r := a.remove(t, "widgets", "A")
	if r.Status != 204 || r.Body != "" {
		t.Errorf("A's DELETE = %d %q, want 204 and no body", r.Status, r.Body)
	}
	if r.Header.Get("Content-Type") != "" {
		t.Errorf("a 204 has the Content-Type %q", r.Header.Get("Content-Type"))
	}
	if n := a.rows(t); n != 0 {
		t.Errorf("%d rows, want the row and its sealed token gone", n)
	}
	if r := a.remove(t, "widgets", "A"); r.Status != 404 {
		t.Errorf("the second DELETE = %d, want 404", r.Status)
	}
	if _, err := a.Store.Token(ctx, "widgets"); !errors.Is(err, store.ErrNotSubscribed) {
		t.Errorf("Token after the DELETE: %v, want ErrNotSubscribed", err)
	}
	if r := a.view(t, "widgets", "A"); r.Status != 404 {
		t.Errorf("GET after the DELETE = %d, want 404", r.Status)
	}
}

// otherKey is a key that is not the harness's.
var otherKey = func() []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(0x11 + 3*i)
	}
	return b
}()

// TestARestartKeepsTheTokenReadableWithTheSameKeyOnly_TS_11_68 verifies TS-11-68:
// After restart the token_state is ok with the same key and unreadable with
// another, and the service keeps serving.
// Verifies: 11-REQ-9.6
func TestARestartKeepsTheTokenReadableWithTheSameKeyOnly_TS_11_68(t *testing.T) {
	fake := fakehub.New(t)
	fake.AddUser("c", "usr_1", []string{"widgets"})
	fake.AddUser("pat", "usr_9", []string{"widgets"})
	fake.AddUser("pat-2", "usr_8", []string{"widgets"})
	path := filepath.Join(t.TempDir(), "agent-fox.db")
	state := func(a *authHarness) string {
		t.Helper()
		r := a.view(t, "widgets", "c")
		if r.Status != 200 {
			t.Fatalf("GET = %d %s", r.Status, r.Body)
		}
		return r.JSON(t)["token_state"].(string)
	}

	first := newAuthHarnessOn(t, fake, path, testKeyBytes)
	if r := first.put(t, "widgets", "c", tokenBody(t, "pat")); r.Status != 201 {
		t.Fatalf("PUT = %d %s", r.Status, r.Body)
	}
	first.Stop(t)

	sameKey := newAuthHarnessOn(t, fake, path, testKeyBytes)
	if got := state(sameKey); got != "ok" {
		t.Errorf("token_state with the same key = %q, want ok", got)
	}
	sameKey.Stop(t)

	otherKeyed := newAuthHarnessOn(t, fake, path, otherKey)
	got := otherKeyed.view(t, "widgets", "c")
	if state := got.JSON(t)["token_state"]; state != "unreadable" {
		t.Errorf("token_state with another key = %v, want unreadable", state)
	}
	if strings.Contains(got.Body, "pat") {
		t.Errorf("the view holds the token: %s", got.Body)
	}
	if r := otherKeyed.get(t, "/healthz"); r.Status != 200 {
		t.Errorf("/healthz with another key = %d, want 200", r.Status)
	}
	if r, slugs := otherKeyed.list(t, "c"); r.Status != 200 || !slices.Equal(slugs, []string{"widgets"}) {
		t.Errorf("the list with another key = %d %s, want the unreadable subscription in it", r.Status, r.Body)
	}
	if r := otherKeyed.put(t, "widgets", "c", tokenBody(t, "pat-2")); r.Status != 200 || r.JSON(t)["token_state"] != "ok" {
		t.Errorf("PUT over an unreadable subscription = %d %s, want 200 and token_state ok", r.Status, r.Body)
	}
	if got := state(otherKeyed); got != "ok" {
		t.Errorf("token_state after the replacement = %q, want ok", got)
	}
	otherKeyed.Stop(t)

	// The replacement was sealed with the other key, so the first key cannot read it
	// now, and a DELETE removes the unreadable subscription.
	back := newAuthHarnessOn(t, fake, path, testKeyBytes)
	if got := state(back); got != "unreadable" {
		t.Errorf("token_state with the first key after the replacement = %q, want unreadable", got)
	}
	if r := back.remove(t, "widgets", "c"); r.Status != 204 {
		t.Errorf("DELETE of an unreadable subscription = %d %s, want 204", r.Status, r.Body)
	}
	if n := back.rows(t); n != 0 {
		t.Errorf("%d rows, want none", n)
	}
}

// TestTheInProcessReadReturnsWhatWasGivenAtSubscribe_TS_11_69 verifies TS-11-69:
// The in-process PAT read returns the plaintext given at subscribe.
// Verifies: 11-REQ-9.7
func TestTheInProcessReadReturnsWhatWasGivenAtSubscribe_TS_11_69(t *testing.T) {
	fake := fakehub.New(t)
	fake.AddUser("c", "usr_1", []string{"widgets"})
	fake.AddUser("p", "usr_9", []string{"widgets"})
	fake.AddUser("p2", "usr_8", []string{"widgets"})
	path := filepath.Join(t.TempDir(), "agent-fox.db")

	a := newAuthHarnessOn(t, fake, path, testKeyBytes)
	if r := a.put(t, "widgets", "c", tokenBody(t, "p")); r.Status != 201 {
		t.Fatalf("PUT = %d %s", r.Status, r.Body)
	}
	if got, err := a.Store.Token(ctx, "widgets"); err != nil || got != "p" {
		t.Errorf("Token = %q, %v; want p", got, err)
	}
	if r := a.put(t, "widgets", "c", tokenBody(t, "p2")); r.Status != 200 {
		t.Fatalf("PUT = %d %s", r.Status, r.Body)
	}
	if got, err := a.Store.Token(ctx, "widgets"); err != nil || got != "p2" {
		t.Errorf("Token after a replace = %q, %v; want p2", got, err)
	}
	a.Stop(t)

	b := newAuthHarnessOn(t, fake, path, testKeyBytes)
	if got, err := b.Store.Token(ctx, "widgets"); err != nil || got != "p2" {
		t.Errorf("Token after a restart with the same key = %q, %v; want p2", got, err)
	}
}

// scenario is what a run of the service leaves behind, for a search for secrets.
type scenario struct {
	logs, caller, pat string
	bodies            []string // every response body, errors included
	files             map[string][]byte
	sealed            []byte
}

// runScenario subscribes, reads, lists and removes with a distinctive caller
// token, token and key, and provokes failures: a wrong token, hub being down and
// a token that does not open. It collects the log, the answers and the files.
func runScenario(t *testing.T) scenario {
	t.Helper()
	sc := scenario{caller: "caller-DISTINCT-54f1-token", pat: "pat-DISTINCT-23ab-secret"}
	a := newAuthHarness(t)
	a.Hub.AddUser(sc.caller, "usr_1", []string{"widgets", "gadgets", "broken"})
	a.Hub.AddUser(sc.pat, "usr_9", []string{"widgets", "gadgets"})
	a.Hub.AddUser("pat-DISTINCT-down-secret", "usr_8", []string{"gadgets"})
	record := func(r response) { sc.bodies = append(sc.bodies, r.Body) }

	record(a.put(t, "widgets", sc.caller, tokenBody(t, sc.pat)))
	record(a.put(t, "widgets", sc.caller, tokenBody(t, sc.pat)))
	record(a.put(t, "widgets", sc.caller, tokenBody(t, "pat-DISTINCT-wrong-secret")))                  // a token hub refuses
	record(a.put(t, "widgets", sc.caller, `{"hub_token":"pat-DISTINCT-bad-body-secret","unknown":1}`)) // a bad body
	a.Hub.FailFor(workspacePath+"gadgets", "pat-DISTINCT-down-secret", 500)
	record(a.put(t, "gadgets", sc.caller, tokenBody(t, "pat-DISTINCT-down-secret"))) // hub is down for the token
	a.Hub.Clear()
	record(a.view(t, "widgets", sc.caller))
	record(a.view(t, "widgets", "unknown-caller"))
	a.Hub.Fail(userPath, 500)
	record(a.view(t, "widgets", "another-caller-DISTINCT")) // hub is down for the caller
	a.Hub.Clear()
	if _, err := a.Store.DB().ExecContext(ctx, "INSERT INTO af_subscriptions (slug, label_prefix, sealed_token, subscribed_by, created_at, updated_at) VALUES ('broken', 'af:', x'0102030405', 'usr_1', '2026-10-22T08:00:00Z', '2026-10-22T08:00:00Z')"); err != nil {
		t.Fatal(err)
	}
	record(a.view(t, "broken", sc.caller)) // a token that does not open
	record(a.get(t, "/api/v1/workspaces", bearer(sc.caller)...))
	record(a.get(t, "/api/v1/workspaces/-bad", bearer(sc.caller)...))
	record(a.remove(t, "gadgets", sc.caller))

	if err := a.Store.DB().QueryRowContext(ctx, "SELECT sealed_token FROM af_subscriptions WHERE slug = 'widgets'").Scan(&sc.sealed); err != nil {
		t.Fatal(err)
	}
	sc.logs = a.Logs.String()
	sc.files = map[string][]byte{}
	entries, err := os.ReadDir(filepath.Dir(a.DBPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(filepath.Dir(a.DBPath), e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		sc.files[e.Name()] = data
	}
	return sc
}

// TestNeitherTheKeyNorATokenIsAnywhereButTheSealedColumn_TS_11_23 verifies TS-11-23:
// Neither the key nor a plaintext token appears in logs, errors or files outside
// the sealed column.
// Verifies: 11-REQ-3.6
func TestNeitherTheKeyNorATokenIsAnywhereButTheSealedColumn_TS_11_23(t *testing.T) {
	sc := runScenario(t)
	if len(sc.files) < 2 {
		t.Fatalf("the scenario left %d files in the data directory, want the database and its WAL at least", len(sc.files))
	}
	keyBase64 := []string{
		base64.StdEncoding.EncodeToString(testKeyBytes), base64.RawStdEncoding.EncodeToString(testKeyBytes),
		base64.URLEncoding.EncodeToString(testKeyBytes), hex.EncodeToString(testKeyBytes),
	}
	blobs := map[string][]byte{"the log": []byte(sc.logs), "the answers": []byte(strings.Join(sc.bodies, "\n"))}
	for name, data := range sc.files {
		blobs["the file "+name] = data
	}
	for name, blob := range blobs {
		if bytes.Contains(blob, testKeyBytes) {
			t.Errorf("%s holds the raw key", name)
		}
		for _, form := range keyBase64 {
			if bytes.Contains(blob, []byte(form)) {
				t.Errorf("%s holds the key as %q", name, form)
			}
		}
		for _, secret := range []string{sc.pat, sc.caller, "pat-DISTINCT-wrong-secret", "pat-DISTINCT-bad-body-secret", "pat-DISTINCT-down-secret"} {
			if bytes.Contains(blob, []byte(secret)) {
				t.Errorf("%s holds %q", name, secret)
			}
		}
	}
	// The sealed column is where the ciphertext is, and nowhere that a person reads.
	found := false
	for name, data := range sc.files {
		if !strings.HasSuffix(name, "-shm") && bytes.Contains(data, sc.sealed) {
			found = true
		}
	}
	if !found {
		t.Log("the sealed value is not in the files yet: it is in the WAL or the database, as the checkpoint decides")
	}
}

// TestNoCredentialBodyCiphertextOrKeyReachesTheLog_TS_11_54 verifies TS-11-54:
// Authorization headers, bodies, tokens, ciphertext and the key never reach the log.
// Verifies: 11-REQ-7.8
func TestNoCredentialBodyCiphertextOrKeyReachesTheLog_TS_11_54(t *testing.T) {
	sc := runScenario(t)
	if sc.logs == "" || !strings.Contains(sc.logs, `"msg":"request"`) {
		t.Fatalf("the scenario logged nothing:\n%q", sc.logs)
	}
	for _, needle := range []string{
		sc.caller, sc.pat, "Bearer", "bearer", "Authorization",
		base64.StdEncoding.EncodeToString(testKeyBytes), hex.EncodeToString(testKeyBytes),
		hex.EncodeToString(sc.sealed), base64.StdEncoding.EncodeToString(sc.sealed), base64.RawStdEncoding.EncodeToString(sc.sealed),
		"label_prefix", "hub_token", "distinct-prefix-54", "workspace not found", "token_state",
	} {
		if strings.Contains(sc.logs, needle) {
			t.Errorf("the log holds %q:\n%s", needle, sc.logs)
		}
	}
	sum := sha256.Sum256([]byte(sc.pat))
	if strings.Contains(sc.logs, hex.EncodeToString(sum[:])) {
		t.Error("the log holds the SHA-256 of the token, which is the key of a cache")
	}
}

// TestTheReadsNeverReturnATokenToAnyCaller_TS_11_62 verifies the reading half of
// TS-11-62 (the writing half is with the subscribe): for any token, slug and
// label prefix, no response of a read or a removal holds the token, and neither
// does the database file or its WAL at any point.
// Verifies: 11-REQ-8.8
func TestTheReadsNeverReturnATokenToAnyCaller_TS_11_62(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 6262))
	t.Logf("seed: PCG(11, 6262)")
	a := newAuthHarness(t)
	a.Hub.AddUser("caller", "usr_1", nil)
	const printable = "!\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~"
	random := func(lo, hi int) string {
		b := make([]byte, lo+rng.IntN(hi-lo+1))
		for i := range b {
			b[i] = printable[rng.IntN(len(printable))]
		}
		return string(b)
	}
	scan := func(i int, what, pat string, blobs ...[]byte) {
		t.Helper()
		for _, blob := range blobs {
			if bytes.Contains(blob, []byte(pat)) {
				t.Fatalf("iteration %d: %s holds the token", i, what)
			}
		}
	}
	files := func() [][]byte {
		var out [][]byte
		for _, f := range []string{a.DBPath, a.DBPath + "-wal"} {
			if data, err := os.ReadFile(f); err == nil {
				out = append(out, data)
			}
		}
		return out
	}

	for i := range 25 {
		slug := fmt.Sprintf("w%d", i)
		pat := random(8, 200)
		a.Hub.Grant("usr_1", slug)
		a.Hub.AddUser(pat, fmt.Sprintf("usr_p%d", i), []string{slug})

		put := a.put(t, slug, "caller", tokenBody(t, pat))
		if put.Status != 201 {
			t.Fatalf("iteration %d: PUT = %d %s", i, put.Status, put.Body)
		}
		get, list, other := a.view(t, slug, "caller"), a.get(t, "/api/v1/workspaces", bearer("caller")...), a.view(t, "nothing", "caller")
		scan(i, "an answer", pat, []byte(put.Body), []byte(get.Body), []byte(list.Body), []byte(other.Body))
		scan(i, "the database or its WAL before the removal", pat, files()...)
		del := a.remove(t, slug, "caller")
		if del.Status != 204 {
			t.Fatalf("iteration %d: DELETE = %d %s", i, del.Status, del.Body)
		}
		scan(i, "the database or its WAL after the removal", pat, files()...)
		scan(i, "the log", pat, []byte(a.Logs.String()))
	}
}

// TestTheReadRoutesNeedACallerAndAValidSlug checks the guards of the routes that
// read and remove, and the methods they do not take.
// Verifies: 11-REQ-6.1, 11-REQ-6.5, 11-REQ-7.3
func TestTheReadRoutesNeedACallerAndAValidSlug(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("A", "usr_1", []string{"widgets"})
	a.seed(t, "widgets", "pat")
	for _, tc := range []struct {
		method, path string
		headers      []string
		status       int
		code         string
	}{
		{"GET", "/api/v1/workspaces", nil, 401, "unauthenticated"},
		{"GET", "/api/v1/workspaces/widgets", nil, 401, "unauthenticated"},
		{"DELETE", "/api/v1/workspaces/widgets", nil, 401, "unauthenticated"},
		{"GET", "/api/v1/workspaces", bearer("nobody"), 401, "unauthenticated"},
		{"GET", "/api/v1/workspaces/-bad", bearer("A"), 400, "invalid_request"},
		{"DELETE", "/api/v1/workspaces/a%20b", bearer("A"), 400, "invalid_request"},
		{"POST", "/api/v1/workspaces", bearer("A"), 405, "method_not_allowed"},
		{"PUT", "/api/v1/workspaces", bearer("A"), 405, "method_not_allowed"},
		{"PATCH", "/api/v1/workspaces/widgets", bearer("A"), 405, "method_not_allowed"},
		{"POST", "/api/v1/workspaces/widgets", bearer("A"), 405, "method_not_allowed"},
		{"GET", "/api/v1/workspaces/", bearer("A"), 404, "not_found"},
	} {
		r := a.do(t, tc.method, tc.path, nil, tc.headers...)
		if r.Status != tc.status || r.errorCode(t) != tc.code {
			t.Errorf("%s %s: %d %s, want %d %s", tc.method, tc.path, r.Status, r.Body, tc.status, tc.code)
		}
	}
	if n := a.rows(t); n != 1 {
		t.Errorf("%d rows, want the seeded one untouched", n)
	}
}

// TestAListShowsAnUnreadableTokenAsSuch checks that a subscription whose token does
// not open is listed, with its state.
// Verifies: 11-REQ-9.6
func TestAListShowsAnUnreadableTokenAsSuch(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("A", "usr_1", []string{"alpha", "broken"})
	a.seed(t, "alpha", "pat")
	if _, err := a.Store.DB().ExecContext(ctx, "INSERT INTO af_subscriptions (slug, label_prefix, sealed_token, subscribed_by, created_at, updated_at) VALUES ('broken', 'af:', x'00', 'usr_1', '2026-10-22T08:00:00Z', '2026-10-22T08:00:00Z')"); err != nil {
		t.Fatal(err)
	}
	r, _ := a.list(t, "A")
	var body struct {
		Workspaces []struct {
			Slug       string `json:"slug"`
			TokenState string `json:"token_state"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal([]byte(r.Body), &body); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, w := range body.Workspaces {
		got = append(got, w.Slug+":"+w.TokenState)
	}
	if want := []string{"alpha:ok", "broken:unreadable"}; !slices.Equal(got, want) {
		t.Errorf("list = %v, want %v", got, want)
	}
}

// TestAnUnsubscribeCleansUpABlockedJobBeforeRemovingTheSubscription_TS_17_56 verifies TS-17-56: DELETE cancels the blocked
// job, waits for the first try of its cleanup, and only then removes the subscription and its token.
// Verifies: 17-REQ-9.1, 17-REQ-9.2
func TestAnUnsubscribeCleansUpABlockedJobBeforeRemovingTheSubscription_TS_17_56(t *testing.T) {
	fh := fakehub.New(t)
	fh.AddUser("caller", "usr_1", []string{"widgets"})
	fh.AddWorkspace(t, fakehub.Workspace{Slug: "widgets", PAT: "pat-of-widgets"})
	fh.AddIssue("widgets", fakehub.Issue{Number: 42, Labels: []string{"af:needs-human"}})
	runner := fakerunner.New(fakerunner.Return(jobs.Result{
		Envelope:  fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which?", "a")),
		LabelsSet: []string{"af:needs-human"},
	}))
	var svc *jobs.Service
	h := newHarnessWith(t, func(d *Deps) {
		d.Hub = hub.New(fh.URL, "1.2.3")
		var err error
		svc, err = jobs.New(jobs.Config{
			DB: d.Store.DB(), Subscriptions: d.Store, Logger: d.Logger, Workers: 1, Runner: runner,
			AttemptsDir: filepath.Join(t.TempDir(), "attempts"), PollInterval: 5 * time.Millisecond,
			Cleaner: &labelcleanup.Cleaner{Subscriptions: d.Store, Hub: d.Hub},
		})
		if err != nil {
			t.Fatal(err)
		}
		d.Jobs = svc
	})
	t.Cleanup(func() { svc.Close() })
	if _, _, err := h.Store.Upsert(ctx, "widgets", "pat-of-widgets", "", "usr_1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	job, err := svc.Create(ctx, jobs.CreateRequest{
		Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage("42")},
		Source: jobs.Source{Kind: "label", By: "poll"}, RequestLabel: "af:fix",
	})
	if err != nil {
		t.Fatal(err)
	}
	for d, _ := svc.Get(ctx, job.ID); d.State != jobs.StateBlocked; d, _ = svc.Get(ctx, job.ID) {
		time.Sleep(2 * time.Millisecond)
	}

	var mu sync.Mutex
	var subscribedAtWrites []bool
	fh.OnRequest(func(r fakehub.Request) {
		if strings.HasPrefix(r.Path, "/api/v1/workspaces/widgets/issues/42/labels") {
			_, err := h.Store.Get(ctx, "widgets")
			mu.Lock()
			subscribedAtWrites = append(subscribedAtWrites, err == nil)
			mu.Unlock()
		}
	})
	r := h.do(t, "DELETE", "/api/v1/workspaces/widgets", nil, "Authorization", "Bearer caller")
	if r.Status != 204 {
		t.Fatalf("DELETE = %d %s", r.Status, r.Body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(subscribedAtWrites) != 2 || slices.Contains(subscribedAtWrites, false) {
		t.Errorf("the subscription was there at the label writes: %v, want both writes made while it was", subscribedAtWrites)
	}
	i, _ := fh.IssueOf("widgets", 42)
	if !slices.Equal(i.Labels, []string{"af:fix"}) {
		t.Errorf("issue 42 carries %v, want af:fix and no af:needs-human", i.Labels)
	}
	if d, _ := svc.Get(ctx, job.ID); d.Labels == nil || d.Labels.Cleanup == nil || *d.Labels.Cleanup != "done" {
		t.Errorf("labels %+v, want cleanup done when the DELETE answered", d.Labels)
	}
	if _, err := h.Store.Get(ctx, "widgets"); !errors.Is(err, store.ErrNotSubscribed) {
		t.Errorf("the subscription is still there: %v", err)
	}
}
