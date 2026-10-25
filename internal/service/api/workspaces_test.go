package api

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// listed are the workspaces of the list the caller gets, as slug:token_state.
func listed(t *testing.T, e *env, caller string) []string {
	t.Helper()
	r := e.get(t, "/api/v1/workspaces", caller)
	if r.Status != 200 {
		t.Fatalf("the list of %s = %d %s", caller, r.Status, r.Body)
	}
	body := testutil.Decode[struct {
		Workspaces []subscriptionView `json:"workspaces"`
	}](t, []byte(r.Body))
	out := []string{}
	for _, w := range body.Workspaces {
		out = append(out, w.Slug+":"+w.TokenState)
	}
	return out
}

func TestListWorkspaces_TheSubscriptionsTheCallerSeesBySlug(t *testing.T) {
	e := newEnv(t)
	e.Hub.AddUser("A", "usr_1", []string{"zeta", "widgets", "alpha", "broken"})
	e.Hub.AddUser("B", "usr_2", []string{"alpha", "unsubscribed"})
	e.Hub.AddUser("C", "usr_3", nil)
	for _, slug := range []string{"zeta", "widgets", "alpha"} {
		e.seed(t, slug, "pat-"+slug)
	}
	e.seedUnreadable(t, "broken")

	if got, want := listed(t, e, "A"), []string{"alpha:ok", "broken:unreadable", "widgets:ok", "zeta:ok"}; !slices.Equal(got, want) {
		t.Errorf("A's list = %v, want %v", got, want)
	}
	if got := listed(t, e, "B"); !slices.Equal(got, []string{"alpha:ok"}) {
		t.Errorf("B's list = %v, want only alpha", got)
	}
	if r := e.get(t, "/api/v1/workspaces", "C"); r.Body != "{\"workspaces\":[]}\n" {
		t.Errorf("the list of a caller who sees nothing = %q, want an empty array", r.Body)
	}
	if r := e.get(t, "/api/v1/workspaces", "A"); strings.Contains(r.Body, "pat-") {
		t.Errorf("the list holds a token: %s", r.Body)
	}
	failed(t, "no caller", e.get(t, "/api/v1/workspaces", ""), 401, CodeUnauthenticated)
}

func TestListWorkspaces_IsNeverPartial(t *testing.T) {
	for name, tc := range map[string]struct {
		hub, status int
		code        Code
	}{"hub down": {500, 502, CodeHubUnavailable}, "hub refuses": {401, 401, CodeUnauthenticated}} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.Hub.AddUser("A", "usr_1", []string{"alpha", "widgets"})
			e.seed(t, "alpha", "pat")
			e.seed(t, "widgets", "pat")
			e.Hub.FailOnce("/api/v1/workspaces/widgets", tc.hub)
			r := e.get(t, "/api/v1/workspaces", "A")
			failed(t, "the list", r, tc.status, tc.code)
			if strings.Contains(r.Body, "alpha") {
				t.Errorf("the answer holds a part of the list: %s", r.Body)
			}
			if got := listed(t, e, "A"); len(got) != 2 {
				t.Errorf("the list once hub answers = %v, want both", got)
			}
		})
	}
}

func TestGetWorkspace_TheViewOrTheSame404(t *testing.T) {
	e := newEnv(t)
	e.Hub.AddUser("A", "usr_1", []string{"widgets", "nothing"})
	e.Hub.AddUser("B", "usr_2", nil)
	e.seed(t, "widgets", "pat")

	r := e.get(t, "/api/v1/workspaces/widgets", "A")
	if view := r.Object(t); r.Status != 200 || view["slug"] != "widgets" || view["token_state"] != "ok" || view["subscribed_by"] != "usr_1" || view["token_user"] != "usr_9" {
		t.Errorf("A on widgets = %d %s", r.Status, r.Body)
	}
	hidden, unsubscribed := e.get(t, "/api/v1/workspaces/widgets", "B"), e.get(t, "/api/v1/workspaces/nothing", "A")
	failed(t, "hidden", hidden, 404, CodeNotFound)
	if hidden.Body != unsubscribed.Body || strings.Contains(hidden.Body, "widgets") || strings.Contains(hidden.Body, "subscri") {
		t.Errorf("the 404s say which they are: %q and %q", hidden.Body, unsubscribed.Body)
	}
}

func TestDeleteWorkspace_RemovesTheSubscriptionAndItsToken(t *testing.T) {
	e := newEnv(t)
	e.Hub.AddUser("A", "usr_1", []string{"widgets", "unreadable", "nothing"})
	e.Hub.AddUser("B", "usr_2", nil)
	e.seed(t, "widgets", "pat")
	e.seedUnreadable(t, "unreadable")

	failed(t, "B's DELETE", e.call(t, "DELETE", "/api/v1/workspaces/widgets", nil, "B"), 404, CodeNotFound)
	failed(t, "an unsubscribed workspace", e.call(t, "DELETE", "/api/v1/workspaces/nothing", nil, "A"), 404, CodeNotFound)
	failed(t, "no caller", e.call(t, "DELETE", "/api/v1/workspaces/widgets", nil, ""), 401, CodeUnauthenticated)
	failed(t, "an invalid slug", e.call(t, "DELETE", "/api/v1/workspaces/a%20b", nil, "A"), 400, CodeInvalidRequest)
	if e.rows(t) != 2 {
		t.Fatalf("%d rows after the refused DELETEs, want both", e.rows(t))
	}
	for _, slug := range []string{"widgets", "unreadable"} {
		r := e.call(t, "DELETE", "/api/v1/workspaces/"+slug, nil, "A")
		if r.Status != 204 || r.Body != "" || r.Header.Get("Content-Type") != "" {
			t.Errorf("DELETE %s = %d %q (%q), want 204 with nothing", slug, r.Status, r.Body, r.Header.Get("Content-Type"))
		}
	}
	if _, err := e.Store.Token(bg, "widgets"); !errors.Is(err, store.ErrNotSubscribed) || e.rows(t) != 0 {
		t.Errorf("the token after the DELETE: %v, and %d rows; want neither", err, e.rows(t))
	}
	failed(t, "a second DELETE", e.call(t, "DELETE", "/api/v1/workspaces/widgets", nil, "A"), 404, CodeNotFound)
}

func TestDeleteWorkspace_CancelsTheOpenJobsFirst(t *testing.T) {
	e := newEnv(t, withJobs(nil))
	e.Hub.AddUser("A", "usr_1", []string{"widgets"})
	e.seed(t, "widgets", "pat")
	job := createJob(t, e, "widgets", "fix", map[string]any{"issue": 7})

	if r := e.call(t, "DELETE", "/api/v1/workspaces/widgets", nil, "A"); r.Status != 204 {
		t.Fatalf("DELETE = %d %s", r.Status, r.Body)
	}
	if doc, err := e.Jobs.Get(bg, job); err != nil || doc.State != jobs.StateCancelled {
		t.Errorf("the job is %+v, %v; want it cancelled", doc.State, err)
	}
	if e.rows(t) != 0 {
		t.Error("the subscription is still there")
	}
}

func TestWorkspaces_AStoreThatFailsIsAnInternalError(t *testing.T) {
	e := newEnv(t)
	e.Hub.AddUser("A", "usr_1", []string{"widgets"})
	e.Hub.AddUser("pat", "usr_9", []string{"widgets"})
	if err := e.Store.Close(); err != nil {
		t.Fatal(err)
	}
	for what, r := range map[string]testutil.Response{
		"the list":         e.get(t, "/api/v1/workspaces", "A"),
		"the subscription": e.get(t, "/api/v1/workspaces/widgets", "A"),
		"a removal":        e.call(t, "DELETE", "/api/v1/workspaces/widgets", nil, "A"),
		"a subscribe":      e.put(t, "widgets", "A", "pat"),
	} {
		failed(t, what, r, 500, CodeInternal)
		if message(t, r) != "internal error" {
			t.Errorf("%s: the message is %q, want nothing of the cause", what, message(t, r))
		}
	}
	if n := len(e.Log.Find("ERROR", "the subscription")); n != 4 {
		t.Errorf("%d ERROR records name what failed, want 4:\n%s", n, e.Log)
	}
}

func TestRestart_TheTokenOpensWithTheSameKeyOnly(t *testing.T) {
	fh, dir, otherKey := fakehub.New(t), t.TempDir(), make([]byte, 32)
	fh.AddUser("c", "usr_1", []string{"widgets"})
	fh.AddUser("pat", "usr_9", []string{"widgets"})
	fh.AddUser("pat-2", "usr_8", []string{"widgets"})
	start := func(key []byte) *env {
		return newEnv(t, func(c *config) { c.dir, c.key, c.hub = dir, key, fh })
	}
	stop := func(e *env) {
		_ = e.HTTP.Close()
		if err := e.Store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	state := func(e *env) any { return e.get(t, "/api/v1/workspaces/widgets", "c").Object(t)["token_state"] }

	first := start(testutil.Key())
	if r := first.put(t, "widgets", "c", "pat"); r.Status != 201 {
		t.Fatalf("PUT = %d %s", r.Status, r.Body)
	}
	stop(first)

	same := start(testutil.Key())
	if got, err := same.Store.Token(bg, "widgets"); state(same) != "ok" || err != nil || got != "pat" {
		t.Errorf("with the same key: %v, the token %q, %v; want ok and pat", state(same), got, err)
	}
	stop(same)

	other := start(otherKey)
	if got := state(other); got != "unreadable" || other.get(t, "/healthz", "").Status != 200 {
		t.Errorf("with another key: %v, want unreadable and the service up", got)
	}
	if got := listed(t, other, "c"); !slices.Equal(got, []string{"widgets:unreadable"}) {
		t.Errorf("the list with another key = %v", got)
	}
	if r := other.put(t, "widgets", "c", "pat-2"); r.Status != 200 || r.Object(t)["token_state"] != "ok" {
		t.Errorf("PUT over an unreadable subscription = %d %s, want 200 and ok", r.Status, r.Body)
	}
	stop(other)

	back := start(testutil.Key())
	if got := state(back); got != "unreadable" {
		t.Errorf("with the first key after the replacement: %v, want unreadable", got)
	}
}

func TestSecrets_NeitherTheKeyNorATokenIsOutsideTheSealedColumn(t *testing.T) {
	const caller, pat, wrong, down = "caller-DISTINCT-54f1", "pat-DISTINCT-23ab", "pat-DISTINCT-wrong", "pat-DISTINCT-down"
	e := newEnv(t)
	e.Hub.AddUser(caller, "usr_1", []string{"widgets", "gadgets", "broken"})
	e.Hub.AddUser(pat, "usr_9", []string{"widgets", "gadgets"})
	e.Hub.AddUser(down, "usr_8", []string{"gadgets"})
	e.Hub.FailFor("/api/v1/workspaces/gadgets", down, 500)
	e.seedUnreadable(t, "broken")
	answers := []testutil.Response{
		e.put(t, "widgets", caller, pat), e.put(t, "widgets", caller, pat), e.put(t, "widgets", caller, wrong),
		e.call(t, "PUT", "/api/v1/workspaces/widgets", `{"hub_token":"`+wrong+`","x":1}`, caller, jsonType),
		e.put(t, "gadgets", caller, down), e.get(t, "/api/v1/workspaces/widgets", caller), e.get(t, "/api/v1/workspaces/broken", caller),
		e.get(t, "/api/v1/workspaces", caller), e.get(t, "/api/v1/workspaces/widgets", "unknown-caller"),
		e.call(t, "DELETE", "/api/v1/workspaces/gadgets", nil, caller),
	}
	sealed := testutil.Column(t, e.Store.DB(), "SELECT hex(sealed_token) FROM af_subscriptions WHERE slug = 'widgets'")[0]

	blobs := map[string]string{"the log": e.Log.String()}
	for _, r := range answers {
		blobs["the answers"] += r.Body
	}
	for name, content := range testutil.Files(t, e.Dir) {
		blobs["the file "+name] = content
	}
	key := testutil.Key()
	secrets := []string{caller, pat, wrong, down, string(key), hex.EncodeToString(key), base64.StdEncoding.EncodeToString(key), base64.RawURLEncoding.EncodeToString(key)}
	for name, blob := range blobs {
		for _, s := range secrets {
			if strings.Contains(blob, s) {
				t.Errorf("%s holds the secret %q", name, s)
			}
		}
	}
	for _, s := range []string{"Bearer", "Authorization", "hub_token", testutil.SHA256(pat), testutil.SHA256(caller), strings.ToLower(sealed)} {
		if strings.Contains(e.Log.String(), s) || strings.Contains(e.Log.String(), strings.ToUpper(s)) {
			t.Errorf("the log holds %q", s)
		}
	}
}
