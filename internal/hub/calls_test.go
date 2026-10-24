package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

const workerPAT = "pat-secret-xyz"

// workerEnv is a fake hub with a workspace that workerPAT sees, and a client of it.
type workerEnv struct {
	t      *testing.T
	hub    *fakehub.Hub
	client *Client
	slug   string
}

// newWorkerEnv makes the hub, with the workspace slug, and a client with short bounds for the
// tests that wait for them.
func newWorkerEnv(t *testing.T, slug string, w fakehub.Workspace) *workerEnv {
	t.Helper()
	h := fakehub.New(t)
	w.Slug, w.PAT = slug, workerPAT
	h.AddWorkspace(t, w)
	c := New(h.URL, "9.9.9")
	return &workerEnv{t: t, hub: h, client: c, slug: slug}
}

// path is the path of a route of the workspace, as it goes on the wire.
func (e *workerEnv) path(rest string) string {
	return "/api/v1/workspaces/" + url.PathEscape(e.slug) + rest
}

// call is one call of the worker, and the route it is made at.
type call struct {
	name, method, route string
	do                  func(e *workerEnv) error
}

// calls are every call the worker makes of hub: the route is relative to the slug for the ones
// that have it.
func calls() []call {
	return []call{
		{"workspace record", "GET", "", func(e *workerEnv) error { _, err := e.client.WorkspaceRecord(ctx, workerPAT, e.slug); return err }},
		{"resolved variables", "GET", "/vars/resolved", func(e *workerEnv) error { _, err := e.client.ResolvedVars(ctx, workerPAT, e.slug); return err }},
		{"open a session", "POST", "!/api/v1/sessions", func(e *workerEnv) error {
			_, err := e.client.OpenSession(ctx, workerPAT, SessionRequest{WorkspaceSlug: e.slug, Archetype: "fix", Model: "m", RunID: "20250102_030405_abc123"})
			return err
		}},
		{"post usage", "POST", "!/api/v1/sessions/ses_0001/usage", func(e *workerEnv) error {
			return e.client.PostUsage(ctx, workerPAT, e.slug, "ses_0001", Usage{Model: "m", InputTokens: 1, OutputTokens: 2})
		}},
		{"complete a session", "POST", "!/api/v1/sessions/ses_0001/complete", func(e *workerEnv) error {
			return e.client.CompleteSession(ctx, workerPAT, e.slug, "ses_0001", SessionCompleted, "")
		}},
		{"post events", "POST", "/runs/20250102_030405_abc123/events/batch", func(e *workerEnv) error {
			return e.client.PostEvents(ctx, workerPAT, e.slug, "20250102_030405_abc123", []Event{{Seq: 1, TS: "2026-01-01T00:00:00Z", Type: "step", Data: json.RawMessage(`{"name":"x"}`)}})
		}},
		{"lease", "POST", "/sandboxes", func(e *workerEnv) error {
			_, err := e.client.Lease(ctx, workerPAT, e.slug, LeaseRequest{Purpose: "fix", Labels: map[string]string{"job": "j", "attempt": "1"}})
			return err
		}},
		{"release", "DELETE", "/sandboxes/sbx_00000000000000000000000000000000", func(e *workerEnv) error {
			return e.client.ReleaseLease(ctx, workerPAT, e.slug, "sbx_00000000000000000000000000000000")
		}},
		{"push a branch", "POST", "/branches/fix%2F42-nil-map/push", func(e *workerEnv) error {
			return e.client.PushBranch(ctx, workerPAT, e.slug, "fix/42-nil-map")
		}},
	}
}

// uri is the request-URI of the call: a route that starts with ! is absolute.
func (e *workerEnv) uri(c call) string {
	if strings.HasPrefix(c.route, "!") {
		return c.route[1:]
	}
	return e.path(c.route)
}

// TestEveryWorkerCallIsMadeAtItsRouteWithThePATAndTheEscapedSlug_TS_13_11 verifies
// TS-13-11: the nine calls of the worker are made at their methods and routes, each with the
// PAT as the bearer token, and the slug is escaped in the path.
// Verifies: 13-REQ-2.1
func TestEveryWorkerCallIsMadeAtItsRouteWithThePATAndTheEscapedSlug_TS_13_11(t *testing.T) {
	e := newWorkerEnv(t, "team/ws one?x#y%z", fakehub.Workspace{})
	// The calls that need a session or a lease use the ones the hub made.
	sessionID, err := e.client.OpenSession(ctx, workerPAT, SessionRequest{WorkspaceSlug: e.slug, Archetype: "fix", Model: "m", RunID: "20250102_030405_abc123"})
	if err != nil {
		t.Fatalf("open a session: %v", err)
	}
	lease, err := e.client.Lease(ctx, workerPAT, e.slug, LeaseRequest{Purpose: "fix"})
	if err != nil {
		t.Fatalf("lease: %v", err)
	}

	for _, c := range calls() {
		c.route = strings.ReplaceAll(c.route, "ses_0001", sessionID)
		c.route = strings.ReplaceAll(c.route, "sbx_00000000000000000000000000000000", lease.ID)
		before := len(e.hub.Requests())
		var err error
		switch c.name {
		case "post usage":
			err = e.client.PostUsage(ctx, workerPAT, e.slug, sessionID, Usage{Model: "m", InputTokens: 1})
		case "complete a session":
			err = e.client.CompleteSession(ctx, workerPAT, e.slug, sessionID, SessionCompleted, "")
		case "release":
			err = e.client.ReleaseLease(ctx, workerPAT, e.slug, lease.ID)
		default:
			err = c.do(e)
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		got := e.hub.Requests()[before:]
		if len(got) != 1 {
			t.Errorf("%s: %d requests, want 1", c.name, len(got))
			continue
		}
		if r := got[0]; r.Method != c.method || r.URI != e.uri(c) || r.TokenHash != fakehub.TokenHash(workerPAT) || r.Header.Get("Authorization") != "Bearer "+workerPAT {
			t.Errorf("%s: %s %s with token %s, want %s %s with the PAT", c.name, r.Method, r.URI, r.TokenHash[:8], c.method, e.uri(c))
		}
	}
}

// TestTheWorkersCallsDoNotTouchTheIdentityOrVisibilityCaches_TS_13_12 is in internal/service/api,
// where the caches are.

// TestARequestIsBoundedToTenSecondsFollowsNoRedirectAndReadsAtMostOneMiB_TS_13_13 verifies
// TS-13-13: an ordinary call that hub answers late fails as unavailable once its bound is over,
// a 302 is not followed, and an answer of 2 MiB is unavailable.
// Verifies: 13-REQ-2.2
func TestARequestIsBoundedToTenSecondsFollowsNoRedirectAndReadsAtMostOneMiB_TS_13_13(t *testing.T) {
	if got := New("http://127.0.0.1:1", "1").HTTP.Timeout; got != 10*time.Second {
		t.Errorf("the bound of an ordinary call = %v, want 10s", got)
	}
	for _, c := range calls() {
		if c.name == "lease" || c.name == "push a branch" {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			e := newWorkerEnv(t, "ws", fakehub.Workspace{})
			e.client.HTTP.Timeout = 80 * time.Millisecond // the 10 s of the client, scaled

			e.hub.Inject(c.method, e.pathOf(c), fakehub.Fault{Delay: 400 * time.Millisecond})
			start := time.Now()
			err := c.do(e)
			var unavailable *UnavailableError
			if !errors.As(err, &unavailable) || time.Since(start) > 350*time.Millisecond {
				t.Errorf("a late answer: %v after %v, want an unavailable error once the bound is over", err, time.Since(start))
			}

			e.hub.Clear()
			e.hub.Inject(c.method, e.pathOf(c), fakehub.Fault{Redirect: "/elsewhere"})
			err = c.do(e)
			if !errors.As(err, &unavailable) || len(e.hub.RequestsTo("", "/elsewhere")) != 0 {
				t.Errorf("a 302: %v, requests to the target %d, want unavailable and the redirect not followed", err, len(e.hub.RequestsTo("", "/elsewhere")))
			}

			e.hub.Clear()
			e.hub.Inject(c.method, e.pathOf(c), fakehub.Fault{Oversize: 2 << 20})
			err = c.do(e)
			if !errors.As(err, &unavailable) {
				t.Errorf("an answer of 2 MiB: %v, want unavailable", err)
			}
		})
	}
}

// pathOf is the decoded path of the call, as the hub matches rules by it.
func (e *workerEnv) pathOf(c call) string {
	raw := e.uri(c)
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		e.t.Fatal(err)
	}
	return decoded
}

// TestNoErrorValueOrMessageHoldsThePAT_TS_13_14 verifies TS-13-14: whichever way a call
// fails, no error, wrapped ones included, holds the PAT.
// Verifies: 13-REQ-2.2
func TestNoErrorValueOrMessageHoldsThePAT_TS_13_14(t *testing.T) {
	faults := map[string]fakehub.Fault{
		"401": {Status: 401}, "403": {Status: 403}, "404": {Status: 404}, "422": {Status: 422}, "500": {Status: 500},
		"hangup": {Hangup: true}, "timeout": {Delay: 300 * time.Millisecond},
		"the PAT in a message":  {Status: 422, Body: `{"error":{"code":422,"message":"the token ` + workerPAT + ` is no good","error_type":"bad"}}`},
		"the PAT in a non-JSON": {Status: 200, Body: "token " + workerPAT},
		// The transport quotes a Location it cannot parse in its error, before it asks whether to follow it.
		"the PAT in a Location that does not parse": {Redirect: "%zz/" + workerPAT},
	}
	for _, c := range calls() {
		for name, f := range faults {
			t.Run(c.name+" "+name, func(t *testing.T) {
				e := newWorkerEnv(t, "ws", fakehub.Workspace{})
				e.client.HTTP.Timeout, e.client.LongTimeout = 80*time.Millisecond, 80*time.Millisecond
				e.hub.Inject(c.method, e.pathOf(c), f)
				err := c.do(e)
				if err == nil {
					// A call whose answer is not read takes a 200 that says anything; a release takes a 404.
					if f.Status != 200 && f.Status != 404 {
						t.Fatalf("%s did not fail", name)
					}
					return
				}
				for e := err; e != nil; e = errors.Unwrap(e) {
					if text := fmt.Sprintf("%+v %v %#v", e, e.Error(), e); strings.Contains(text, workerPAT) {
						t.Errorf("an error holds the PAT: %s", text)
					}
				}
			})
		}
	}
}

// TestTheLeaseAndTheBranchPushUseTheLongBound_TS_13_15 verifies TS-13-15: the two calls that
// make hub do slow work wait for it, to a bound of their own, which the context may shorten.
// Verifies: 13-REQ-2.3
func TestTheLeaseAndTheBranchPushUseTheLongBound_TS_13_15(t *testing.T) {
	if c := New("http://127.0.0.1:1", "1"); c.LongTimeout != 0 || c.longBound() != 5*time.Minute {
		t.Errorf("LongTimeout = %v and the bound %v, want 5 minutes", c.LongTimeout, c.longBound())
	}
	lease := calls()[6]
	push := calls()[8]
	for _, c := range []call{lease, push} {
		t.Run(c.name, func(t *testing.T) {
			e := newWorkerEnv(t, "ws", fakehub.Workspace{})
			e.client.HTTP.Timeout, e.client.LongTimeout = 100*time.Millisecond, 700*time.Millisecond // 10 s and 5 minutes, scaled

			e.hub.Inject(c.method, e.pathOf(c), fakehub.Fault{Delay: 350 * time.Millisecond})
			if err := c.do(e); err != nil {
				t.Errorf("answered after 3.5 times the ordinary bound: %v, want it to succeed", err)
			}

			e.hub.Clear()
			e.hub.Inject(c.method, e.pathOf(c), fakehub.Fault{Delay: 3 * time.Second})
			start := time.Now()
			var unavailable *UnavailableError
			if err := c.do(e); !errors.As(err, &unavailable) || time.Since(start) > 2*time.Second {
				t.Errorf("a late answer after %v: %v, want an unavailable error at the long bound", time.Since(start), err)
			}

			// The context of the attempt is shorter than the bound.
			short, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
			defer cancel()
			e.client.LongTimeout = time.Minute
			start = time.Now()
			var err error
			switch c.name {
			case "lease":
				_, err = e.client.Lease(short, workerPAT, e.slug, LeaseRequest{Purpose: "fix"})
			default:
				err = e.client.PushBranch(short, workerPAT, e.slug, "fix/42-nil-map")
			}
			if !errors.As(err, &unavailable) || time.Since(start) > time.Second {
				t.Errorf("with a context of 120ms: %v after %v, want the context to cut the call short", err, time.Since(start))
			}
		})
	}
}

// TestEachAnswerClassIsToldApartByErrorsIsAndAs_TS_13_16 verifies TS-13-16: 401 is refused, 403
// forbidden with a message that names the operation, says the PAT lacks the scope and points
// to PUT /api/v1/workspaces/{slug}, 404 not found, another 4xx rejected with hub's message,
// and 429, 5xx, a hang-up and a body that is not JSON unavailable.
// Verifies: 13-REQ-2.4
func TestEachAnswerClassIsToldApartByErrorsIsAndAs_TS_13_16(t *testing.T) {
	const message = "the title must not be blank"
	rejectedBody := `{"error":{"code":400,"message":"` + message + `","error_type":"invalid_title"}}`
	for _, c := range calls() {
		if c.name == "release" {
			continue // 404 is a release
		}
		t.Run(c.name, func(t *testing.T) {
			e := newWorkerEnv(t, "ws/1", fakehub.Workspace{})
			e.client.HTTP.Timeout, e.client.LongTimeout = 100*time.Millisecond, 100*time.Millisecond
			inject := func(f fakehub.Fault) error {
				e.hub.Clear()
				e.hub.Inject(c.method, e.pathOf(c), f)
				return c.do(e)
			}

			if err := inject(fakehub.Fault{Status: 401}); !errors.Is(err, ErrRefused) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
				t.Errorf("401: %v, want refused", err)
			}
			err := inject(fakehub.Fault{Status: 403})
			if !errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrRefused) {
				t.Fatalf("403: %v, want forbidden", err)
			}
			if text := err.Error(); !strings.Contains(text, "PUT /api/v1/workspaces/"+url.PathEscape("ws/1")) || !strings.Contains(text, "scope") || !strings.Contains(text, c.operation()) {
				t.Errorf("the 403 says %q, want the operation %q, the scope and PUT /api/v1/workspaces/ws%%2F1", text, c.operation())
			}
			if err := inject(fakehub.Fault{Status: 404}); !errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) {
				t.Errorf("404: %v, want not found", err)
			}
			for _, status := range []int{400, 409, 422} {
				err := inject(fakehub.Fault{Status: status, Body: rejectedBody})
				var rejected *RejectedError
				if !errors.As(err, &rejected) || !errors.Is(err, ErrRejected) || rejected.Status != status || rejected.Message != message || rejected.Code != "invalid_title" || !strings.Contains(err.Error(), message) {
					t.Errorf("%d: %v (%+v), want rejected with the message of hub", status, err, rejected)
				}
			}
			if err := inject(fakehub.Fault{Status: 422}); !errors.Is(err, ErrRejected) {
				t.Errorf("422 with the default error body: %v, want rejected", err)
			}
			for name, f := range map[string]fakehub.Fault{
				"429": {Status: 429}, "500": {Status: 500}, "503": {Status: 503}, "hang-up": {Hangup: true},
				"a timeout": {Delay: 400 * time.Millisecond}, "not JSON": {Status: 200, Body: "<html>"},
			} {
				var unavailable *UnavailableError
				if err := inject(f); !errors.As(err, &unavailable) || errors.Is(err, ErrRefused) || errors.Is(err, ErrRejected) {
					if name == "not JSON" && (c.name == "post usage" || c.name == "complete a session" || c.name == "push a branch" || c.name == "release") {
						continue // a call whose answer is not read does not look at it
					}
					t.Errorf("%s: %v, want unavailable", name, err)
				}
			}
		})
	}
}

// operation is how the call is named in the message of a 403.
func (c call) operation() string {
	return map[string]string{
		"workspace record": "read the workspace record", "resolved variables": "read the resolved variables", "open a session": "open a session",
		"post usage": "post the usage", "complete a session": "complete the session", "post events": "post the events",
		"lease": "lease a sandbox", "release": "release the sandbox", "push a branch": "publish the branch",
	}[c.name]
}

// TestA403OnTheWorkspaceRecordIsForbiddenNotNotFound_TS_13_17 verifies TS-13-17: where the
// core's call reads 403 as not found, the call for the record reads it as forbidden.
// Verifies: 13-REQ-2.5
func TestA403OnTheWorkspaceRecordIsForbiddenNotNotFound_TS_13_17(t *testing.T) {
	e := newWorkerEnv(t, "acme", fakehub.Workspace{})
	e.hub.Inject("GET", "/api/v1/workspaces/acme", fakehub.Fault{Status: 403})

	_, err := e.client.WorkspaceRecord(ctx, workerPAT, "acme")
	if !errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
		t.Fatalf("WorkspaceRecord = %v, want forbidden and not not found", err)
	}
	if !strings.Contains(err.Error(), "PUT /api/v1/workspaces/acme") {
		t.Errorf("the message %q does not point to PUT /api/v1/workspaces/acme", err)
	}
	// The core's call is unchanged.
	if err := e.client.Workspace(ctx, workerPAT, "acme"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Workspace on a 403 = %v, want not found", err)
	}
}

// TestASandboxReleaseAnswered404CountsAsReleased_TS_13_18 verifies TS-13-18.
// Verifies: 13-REQ-2.6
func TestASandboxReleaseAnswered404CountsAsReleased_TS_13_18(t *testing.T) {
	e := newWorkerEnv(t, "acme", fakehub.Workspace{})
	e.hub.Inject("DELETE", "/api/v1/workspaces/acme/sandboxes/sbx_00000000000000000000000000000000", fakehub.Fault{Status: 404})

	if err := e.client.ReleaseLease(ctx, workerPAT, "acme", "sbx_00000000000000000000000000000000"); err != nil {
		t.Errorf("a release answered 404 = %v, want nil", err)
	}
	// An id that hub does not know answers 404 as well.
	if err := e.client.ReleaseLease(ctx, workerPAT, "acme", "sbx_ffffffffffffffffffffffffffffffff"); err != nil {
		t.Errorf("a release of a lease hub does not know = %v, want nil", err)
	}
}

// TestBothEncodingsOfTheResolvedVariablesDecodeToTheSameMap_TS_13_19 verifies TS-13-19, a
// property: for any map of names to values, hub's list of {key, value, origin}, the
// object {"vars": {…}} and the list of {name, value} decode to the map. The names of hub's
// fields are in no package but this one.
// Verifies: 13-REQ-2.7
func TestBothEncodingsOfTheResolvedVariablesDecodeToTheSameMap_TS_13_19(t *testing.T) {
	rng := rand.New(rand.NewSource(19))
	for round := range 12 {
		vars := map[string]string{}
		for range rng.Intn(6) {
			vars[fmt.Sprintf("AF_%c%d", 'A'+rng.Intn(26), rng.Intn(1000))] = randomValue(rng)
		}
		for _, form := range []string{"", "object", "namelist"} {
			e := newWorkerEnv(t, "ws", fakehub.Workspace{Vars: vars, VarsForm: form})
			got, err := e.client.ResolvedVars(ctx, workerPAT, "ws")
			if err != nil || !reflect.DeepEqual(got, vars) && !(len(got) == 0 && len(vars) == 0) {
				t.Errorf("round %d, form %q: %v, %v; want %v", round, form, got, err, vars)
			}
		}
	}

	names := []string{`"hub_url"`, `"workspace_mode"`, `"git_url"`, `"gateway_url"`, `"event_type"`, `"workspace_slug"`, `"is_pr"`}
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(path)
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules" || strings.Contains(slash, "internal/hub") || strings.HasPrefix(slash, "../../.specs") || strings.HasPrefix(slash, "../../docs")) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, name := range names {
			if strings.Contains(string(data), name) {
				t.Errorf("%s names the hub field %s: the field names of hub are known to internal/hub alone", slash, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// randomValue is a value of a variable: text with the characters that are awkward in JSON.
func randomValue(rng *rand.Rand) string {
	alphabet := []rune("abcXYZ019 _-./:\"\\\n\téü")
	var b strings.Builder
	for range rng.Intn(12) {
		b.WriteRune(alphabet[rng.Intn(len(alphabet))])
	}
	return b.String()
}

// bodyOf is the JSON the hub received in the one request with the method and path.
func (e *workerEnv) bodyOf(method, path string) map[string]any {
	e.t.Helper()
	reqs := e.hub.RequestsTo(method, path)
	if len(reqs) != 1 {
		e.t.Fatalf("%d requests %s %s, want 1", len(reqs), method, path)
	}
	var body map[string]any
	if len(reqs[0].Body) > 0 {
		if err := json.Unmarshal(reqs[0].Body, &body); err != nil {
			e.t.Fatalf("%s %s: the body is not an object: %v\n%s", method, path, err, reqs[0].Body)
		}
	}
	return body
}

// TestTheRecordTheVariablesAndTheBodiesOfTheCalls guards what the calls read and send: the
// record's fields, the mode that defaults to standard, an incomplete record that is no record,
// and the bodies of the calls that carry one.
func TestTheRecordTheVariablesAndTheBodiesOfTheCalls(t *testing.T) {
	e := newWorkerEnv(t, "acme", fakehub.Workspace{Branch: "trunk", Mode: "carry_patch", ForgeURL: "https://gitlab.example.com/g/p"})
	rec, err := e.client.WorkspaceRecord(ctx, workerPAT, "acme")
	want := Record{CloneURL: e.hub.CloneURL("acme"), Branch: "trunk", Mode: ModeCarryPatch, ForgeURL: "https://gitlab.example.com/g/p"}
	if err != nil || rec != want {
		t.Errorf("record = %+v, %v; want %+v", rec, err, want)
	}

	for name, body := range map[string]string{
		"a record without a clone URL":   `{"slug":"acme","git_url":"https://github.example.com/o/r","hub_url":null,"branch":"main"}`,
		"a record without a branch":      `{"slug":"acme","git_url":"https://github.example.com/o/r","hub_url":"http://hub.example.com/git/acme.git","branch":null}`,
		"a record without a forge URL":   `{"slug":"acme","hub_url":"http://hub.example.com/git/acme.git","branch":"main"}`,
		"a record that is not an object": `[]`,
	} {
		e.hub.Clear()
		e.hub.Inject("GET", "/api/v1/workspaces/acme", fakehub.Fault{Status: 200, Body: body})
		var unavailable *UnavailableError
		if _, err := e.client.WorkspaceRecord(ctx, workerPAT, "acme"); !errors.As(err, &unavailable) {
			t.Errorf("%s: %v, want unavailable", name, err)
		}
	}
	e.hub.Clear()
	e.hub.Inject("GET", "/api/v1/workspaces/acme", fakehub.Fault{Status: 200, Body: `{"slug":"acme","git_url":"https://github.example.com/o/r","hub_url":"http://hub.example.com/git/acme.git","branch":"main"}`})
	if rec, err := e.client.WorkspaceRecord(ctx, workerPAT, "acme"); err != nil || rec.Mode != ModeStandard {
		t.Errorf("a record with no mode = %+v, %v; want standard", rec, err)
	}
	e.hub.Clear()
	e.hub.Inject("GET", "/api/v1/workspaces/acme", fakehub.Fault{Status: 200, Body: `{"slug":"acme","git_url":"https://github.example.com/o/r","hub_url":"http://hub.example.com/git/acme.git","branch":"main","mode":"carry_patch"}`})
	if rec, err := e.client.WorkspaceRecord(ctx, workerPAT, "acme"); err != nil || rec.Mode != ModeCarryPatch {
		t.Errorf("a record whose mode is in the field the spec names = %+v, %v; want carry_patch", rec, err)
	}
	e.hub.Clear()

	e2 := newWorkerEnv(t, "acme", fakehub.Workspace{})
	id, err := e2.client.OpenSession(ctx, workerPAT, SessionRequest{WorkspaceSlug: "acme", Archetype: "triage", Model: "m-1", RunID: "20250102_030405_abc123"})
	if err != nil {
		t.Fatal(err)
	}
	body := e2.bodyOf("POST", "/api/v1/sessions")
	if !reflect.DeepEqual(body, map[string]any{"workspace_slug": "acme", "archetype": "triage", "model": "m-1", "run_id": "20250102_030405_abc123"}) {
		t.Errorf("the session request = %v", body)
	}
	if err := e2.client.PostUsage(ctx, workerPAT, "acme", id, Usage{Model: "m-1", Brain: "claude", InputTokens: 7, OutputTokens: 9, CostUSD: 0.5, CostSource: "reported", Turns: 3}); err != nil {
		t.Fatal(err)
	}
	if body := e2.bodyOf("POST", "/api/v1/sessions/"+id+"/usage"); body["model"] != "m-1" || body["input_tokens"] != 7.0 || body["output_tokens"] != 9.0 || body["cost_usd"] != 0.5 || body["turns"] != 3.0 || body["brain"] != "claude" || body["cost_source"] != "reported" {
		t.Errorf("the usage = %v", body)
	}
	if err := e2.client.CompleteSession(ctx, workerPAT, "acme", id, SessionFailed, "the lease was refused"); err != nil {
		t.Fatal(err)
	}
	if body := e2.bodyOf("POST", "/api/v1/sessions/"+id+"/complete"); !reflect.DeepEqual(body, map[string]any{"status": "failed", "error_message": "the lease was refused"}) {
		t.Errorf("the completion = %v", body)
	}
	if sessions := e2.hub.Sessions(); len(sessions) != 1 || sessions[0].Status != "failed" || sessions[0].Message != "the lease was refused" {
		t.Errorf("sessions = %+v", sessions)
	}
	e5 := newWorkerEnv(t, "acme", fakehub.Workspace{})
	if err := e5.client.CompleteSession(ctx, workerPAT, "acme", "ses_0001", SessionCompleted, ""); err == nil {
		t.Fatal("the completion of a session that is not there worked")
	}
	if body := e5.bodyOf("POST", "/api/v1/sessions/ses_0001/complete"); !reflect.DeepEqual(body, map[string]any{"status": "completed"}) {
		t.Errorf("the completion without a message = %v", body)
	}

	events := []Event{
		{Seq: 4, TS: "2026-01-01T00:00:00Z", Type: "step", Data: json.RawMessage(`{"name":"clone"}`)},
		{Seq: 5, TS: "2026-01-01T00:00:01Z", Type: "tool_call", Data: json.RawMessage(`"text"`)},
		{Seq: 6, TS: "2026-01-01T00:00:02Z", Type: "heartbeat"},
	}
	if err := e2.client.PostEvents(ctx, workerPAT, "acme", "20250102_030405_abc123", events); err != nil {
		t.Fatal(err)
	}
	batch := e2.hub.EventBatches("acme", "20250102_030405_abc123")
	if len(batch) != 1 || len(batch[0]) != 3 {
		t.Fatalf("batches = %+v", batch)
	}
	for i, wantPayload := range []string{`{"name":"clone"}`, `{"data":"text"}`, `{}`} {
		got := batch[0][i]
		if got.ID != fmt.Sprintf("20250102_030405_abc123-%d", i+4) || got.Type != events[i].Type || got.Timestamp != events[i].TS || got.RunID != "20250102_030405_abc123" || string(got.Payload) != wantPayload {
			t.Errorf("event %d = %+v, want id with the run and the number, payload %s", i, got, wantPayload)
		}
	}
	e2.hub.Clear()
	e2.hub.Inject("POST", "/api/v1/workspaces/acme/runs/r1/events/batch", fakehub.Fault{Status: 200, Body: `{"accepted":1,"duplicates":0,"errors":[{"index":1,"message":"bad event"}]}`})
	var rejected *RejectedError
	if err := e2.client.PostEvents(ctx, workerPAT, "acme", "r1", events); !errors.As(err, &rejected) || !strings.Contains(err.Error(), "bad event") {
		t.Errorf("a batch of which hub refused an event: %v, want a rejection that says which", err)
	}

	e3 := newWorkerEnv(t, "acme", fakehub.Workspace{})
	lease, err := e3.client.Lease(ctx, workerPAT, "acme", LeaseRequest{Purpose: "fix", Labels: map[string]string{"job": "job_1", "attempt": "2"}})
	if err != nil || lease.ID == "" || lease.Token == "" || lease.GatewayURL != e3.hub.Gateway().URL() || time.Until(lease.ExpiresAt) < 25*time.Minute {
		t.Errorf("lease = %+v, %v", lease, err)
	}
	if body := e3.bodyOf("POST", "/api/v1/workspaces/acme/sandboxes"); !reflect.DeepEqual(body, map[string]any{"purpose": "fix", "labels": map[string]any{"job": "job_1", "attempt": "2"}}) {
		t.Errorf("the lease request without a profile = %v", body)
	}
	e4 := newWorkerEnv(t, "acme", fakehub.Workspace{})
	if _, err := e4.client.Lease(ctx, workerPAT, "acme", LeaseRequest{Profile: "big", Purpose: "triage"}); err != nil {
		t.Fatal(err)
	}
	if body := e4.bodyOf("POST", "/api/v1/workspaces/acme/sandboxes"); !reflect.DeepEqual(body, map[string]any{"purpose": "triage", "profile": "big", "labels": map[string]any{}}) {
		t.Errorf("the lease request with a profile = %v", body)
	}
	e4.hub.Clear()
	e4.hub.Inject("POST", "/api/v1/workspaces/acme/sandboxes", fakehub.Fault{Status: 201, Body: `{"id":"sbx_1","gateway_url":"http://gateway.example.com","token":"t","expires_at":"yesterday"}`})
	var unavailable *UnavailableError
	if _, err := e4.client.Lease(ctx, workerPAT, "acme", LeaseRequest{}); !errors.As(err, &unavailable) {
		t.Errorf("a lease with no valid end: %v, want unavailable", err)
	}
	e4.hub.Clear()
	e4.hub.Inject("POST", "/api/v1/workspaces/acme/sandboxes", fakehub.Fault{Status: 201, Body: `{"id":"","gateway_url":"http://gateway.example.com","token":"t","expires_at":"2030-01-01T00:00:00Z"}`})
	if _, err := e4.client.Lease(ctx, workerPAT, "acme", LeaseRequest{}); !errors.As(err, &unavailable) {
		t.Errorf("a lease with no id: %v, want unavailable", err)
	}
	if err := e4.client.PushBranch(ctx, workerPAT, "acme", "fix/42-x"); err != nil {
		t.Fatal(err)
	}
	if pushes := e4.hub.BranchPushes(); len(pushes) != 1 || pushes[0].Branch != "fix/42-x" {
		t.Errorf("pushes = %+v", pushes)
	}
	e4.hub.Clear()
	e4.hub.Inject("POST", "/api/v1/sessions", fakehub.Fault{Status: 201, Body: `{"workspace_slug":"acme"}`})
	if _, err := e4.client.OpenSession(ctx, workerPAT, SessionRequest{WorkspaceSlug: "acme"}); !errors.As(err, &unavailable) {
		t.Errorf("a session with no id: %v, want unavailable", err)
	}
}

// TestHubsRealErrorObjectsAreReadAndTheTokenAndLengthAreKeptOutOfWhatIsReported pins how hub's
// answers are read: the code of its error object is the integer status and its machine-readable
// kind is error_type, which the reason survives; a reason that hub gives for one event of a batch
// is cut and has no token in it; and a 502 that says the forge refused hub's own credential is a
// rejection, which no retry cures, while the other failures of the forge stay unavailable.
// Verifies: 13-REQ-2.2, 13-REQ-2.4
func TestHubsRealErrorObjectsAreReadAndTheTokenAndLengthAreKeptOutOfWhatIsReported(t *testing.T) {
	t.Run("the integer code and the kind", func(t *testing.T) {
		e := newWorkerEnv(t, "acme", fakehub.Workspace{})
		e.hub.Inject("POST", e.path("/sandboxes"), fakehub.Fault{Status: 409, Body: `{"error":{"code":409,"message":"no capacity: 4 of 4 sandboxes are in use","error_type":"sandbox_capacity"}}`})

		_, err := e.client.Lease(ctx, workerPAT, "acme", LeaseRequest{Purpose: "fix"})

		var rejected *RejectedError
		if !errors.As(err, &rejected) || rejected.Message != "no capacity: 4 of 4 sandboxes are in use" || rejected.Code != "sandbox_capacity" || rejected.Status != 409 {
			t.Errorf("error = %v (%+v), want the reason and the kind of hub's error object", err, rejected)
		}
	})

	t.Run("an error object with no kind", func(t *testing.T) {
		e := newWorkerEnv(t, "acme", fakehub.Workspace{})
		e.hub.Inject("POST", e.path("/sandboxes"), fakehub.Fault{Status: 400, Body: `{"error":{"code":400,"message":"the body is malformed"}}`})

		_, err := e.client.Lease(ctx, workerPAT, "acme", LeaseRequest{Purpose: "fix"})

		var rejected *RejectedError
		if !errors.As(err, &rejected) || rejected.Message != "the body is malformed" || rejected.Code != "400" {
			t.Errorf("error = %v (%+v), want the reason and the status as the code", err, rejected)
		}
	})

	t.Run("the reason for one event", func(t *testing.T) {
		e := newWorkerEnv(t, "acme", fakehub.Workspace{})
		long := strings.Repeat("x", 5000)
		e.hub.Inject("POST", e.path("/runs/r1/events/batch"), fakehub.Fault{Status: 200, Body: `{"errors":[{"index":0,"message":"bad ` + workerPAT + ` ` + long + `"}]}`})

		err := e.client.PostEvents(ctx, workerPAT, "acme", "r1", []Event{{Seq: 1, TS: "2026-01-01T00:00:00Z", Type: "step"}})

		if err == nil || strings.Contains(err.Error(), workerPAT) || len(err.Error()) > 1500 || !strings.Contains(err.Error(), "bad [token]") {
			t.Errorf("error = %.200q (%d bytes), want the reason without the token and cut short", err, len(err.Error()))
		}
	})

	t.Run("the forge refusing the credential of hub", func(t *testing.T) {
		for kind, status := range map[string]int{"forge_unauthenticated": 502, "forge_forbidden": 502} {
			e := newWorkerEnv(t, "acme", fakehub.Workspace{})
			e.hub.Inject("POST", e.path("/issues/42/comments"), fakehub.Fault{Status: status, Body: `{"error":{"code":502,"message":"the forge refused the token of hub","error_type":"` + kind + `"}}`})

			_, err := e.client.Forge(workerPAT, "acme", issuex.Repo{Owner: "Org", Name: "Repo"}).AddComment(ctx, issuex.IssueRef{Repo: issuex.Repo{Owner: "Org", Name: "Repo"}, Number: 42}, "x")

			var unavailable *UnavailableError
			var he *issuex.HTTPError
			if err == nil || errors.As(err, &unavailable) || !errors.As(err, &he) || !strings.Contains(he.Message, "the forge refused the token of hub") {
				t.Errorf("%s: error = %v, want a refusal that is not unavailable and says why", kind, err)
			}
		}
		e := newWorkerEnv(t, "acme", fakehub.Workspace{})
		e.hub.Inject("POST", e.path("/issues/42/comments"), fakehub.Fault{Status: 502, Body: `{"error":{"code":502,"message":"the forge failed","error_type":"forge_error"}}`})
		_, err := e.client.Forge(workerPAT, "acme", issuex.Repo{Owner: "Org", Name: "Repo"}).AddComment(ctx, issuex.IssueRef{Repo: issuex.Repo{Owner: "Org", Name: "Repo"}, Number: 42}, "x")
		var unavailable *UnavailableError
		if !errors.As(err, &unavailable) {
			t.Errorf("forge_error: error = %v, want unavailable: the forge may recover", err)
		}
	})
}

// TestTheLeasesOfAWorkspaceAreListedWithTheirLabelsAndEnd checks the listing that the reconciliation
// pass reads: GET …/sandboxes with the PAT, the leases that hub holds as typed values, an empty list
// for a workspace with none, and the errors of the other calls for a token hub does not take.
// Verifies: 14-REQ-10.3
func TestTheLeasesOfAWorkspaceAreListedWithTheirLabelsAndEnd(t *testing.T) {
	e := newWorkerEnv(t, "acme", fakehub.Workspace{})

	if got, err := e.client.ListSandboxes(ctx, workerPAT, "acme"); err != nil || len(got) != 0 {
		t.Fatalf("ListSandboxes of a workspace with no lease = %v, %v, want an empty list", got, err)
	}
	a := e.hub.SeedLease("acme", map[string]string{"job": "job_1", "attempt": "2"}, "fix")
	b := e.hub.SeedLease("acme", nil, "triage")

	got, err := e.client.ListSandboxes(ctx, workerPAT, "acme")

	if err != nil || len(got) != 2 {
		t.Fatalf("ListSandboxes = %v, %v, want the two leases", got, err)
	}
	byID := map[string]LeaseInfo{}
	for _, l := range got {
		byID[l.ID] = l
	}
	if l := byID[a.ID]; l.Purpose != "fix" || l.Labels["job"] != "job_1" || l.Labels["attempt"] != "2" || !l.ExpiresAt.Equal(a.ExpiresAt.Truncate(time.Second)) || l.State == "" {
		t.Errorf("lease a listed as %+v, want purpose fix, its labels and its end %s", l, a.ExpiresAt)
	}
	if l := byID[b.ID]; l.Purpose != "triage" || len(l.Labels) != 0 {
		t.Errorf("lease b listed as %+v, want purpose triage and no labels", l)
	}
	if got := e.hub.RequestsTo("GET", e.path("/sandboxes")); len(got) != 2 {
		t.Errorf("hub saw %d listings, want 2", len(got))
	}
	if _, err := e.client.ListSandboxes(ctx, "not-the-token", "acme"); !errors.Is(err, ErrRefused) {
		t.Errorf("ListSandboxes with a token hub does not take = %v, want ErrRefused", err)
	}
	e.hub.Inject("GET", e.path("/sandboxes"), fakehub.Fault{Status: 503})
	var unavailable *UnavailableError
	if _, err := e.client.ListSandboxes(ctx, workerPAT, "acme"); !errors.As(err, &unavailable) {
		t.Errorf("ListSandboxes answered 503 = %v, want an UnavailableError", err)
	}
}
