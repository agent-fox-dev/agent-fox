package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

// call is one call the worker makes of hub.
type call struct {
	name, method string
	// uri is the request-URI, with {slug} for the escaped slug of the env.
	uri string
	// op is how the call names itself in its errors.
	op string
	// long calls have the long bound, and reads ones decode the answer of a 2xx.
	long, reads bool
	// done is the status hub answers that is a success all the same.
	done int
	do   func(e *env) error
}

const runID = "20250102_030405_abc123"

func calls() []call {
	return []call{
		{name: "WorkspaceRecord", method: "GET", uri: "/api/v1/workspaces/{slug}", op: "read the workspace record", reads: true,
			do: func(e *env) error { _, err := e.client.WorkspaceRecord(ctx, pat, e.slug); return err }},
		{name: "ResolvedVars", method: "GET", uri: "/api/v1/workspaces/{slug}/vars/resolved", op: "read the resolved variables", reads: true,
			do: func(e *env) error { _, err := e.client.ResolvedVars(ctx, pat, e.slug); return err }},
		{name: "OpenSession", method: "POST", uri: "/api/v1/sessions", op: "open a session", reads: true, do: func(e *env) error {
			_, err := e.client.OpenSession(ctx, pat, SessionRequest{WorkspaceSlug: e.slug, Archetype: "fix", Model: "m", RunID: runID})
			return err
		}},
		{name: "PostUsage", method: "POST", uri: "/api/v1/sessions/ses_0001/usage", op: "post the usage of the session",
			do: func(e *env) error {
				return e.client.PostUsage(ctx, pat, e.slug, "ses_0001", Usage{Model: "m", InputTokens: 1})
			}},
		{name: "CompleteSession", method: "POST", uri: "/api/v1/sessions/ses_0001/complete", op: "complete the session",
			do: func(e *env) error {
				return e.client.CompleteSession(ctx, pat, e.slug, "ses_0001", SessionCompleted, "")
			}},
		{name: "PostEvents", method: "POST", uri: "/api/v1/workspaces/{slug}/runs/" + runID + "/events/batch", op: "post the events of the run", reads: true,
			do: func(e *env) error {
				return e.client.PostEvents(ctx, pat, e.slug, runID, []Event{{Seq: 1, TS: "2026-01-01T00:00:00Z", Type: "step"}})
			}},
		{name: "Lease", method: "POST", uri: "/api/v1/workspaces/{slug}/sandboxes", op: "lease a sandbox", long: true, reads: true,
			do: func(e *env) error {
				_, err := e.client.Lease(ctx, pat, e.slug, LeaseRequest{Purpose: "fix"})
				return err
			}},
		{name: "ListSandboxes", method: "GET", uri: "/api/v1/workspaces/{slug}/sandboxes", op: "list the sandboxes", reads: true,
			do: func(e *env) error { _, err := e.client.ListSandboxes(ctx, pat, e.slug); return err }},
		{name: "ReleaseLease", method: "DELETE", uri: "/api/v1/workspaces/{slug}/sandboxes/sbx_1", op: "release the sandbox", done: 404,
			do: func(e *env) error { return e.client.ReleaseLease(ctx, pat, e.slug, "sbx_1") }},
		{name: "PushBranch", method: "POST", uri: "/api/v1/workspaces/{slug}/branches/fix%2F42-nil-map/push", op: "publish the branch", long: true,
			do: func(e *env) error { return e.client.PushBranch(ctx, pat, e.slug, "fix/42-nil-map") }},
		{name: "CreateLabel", method: "POST", uri: "/api/v1/workspaces/{slug}/labels", op: "create a label", reads: true, done: 409,
			do: func(e *env) error {
				_, err := e.client.CreateLabel(ctx, pat, e.slug, "af:fix", "0e8a16", "Fix it")
				return err
			}},
	}
}

func (e *env) uri(c call) string { return strings.ReplaceAll(c.uri, "{slug}", url.PathEscape(e.slug)) }

// inject makes the route of the call answer with the fault, and makes the call.
func (e *env) inject(t *testing.T, c call, f fakehub.Fault) error {
	t.Helper()
	e.answer(c.method, faultPath(t, e.uri(c)), f)
	return c.do(e)
}

func TestCalls_AreMadeAtTheirRoutesWithThePATAndTheEscapedSlug(t *testing.T) {
	e := newEnv(t, "team/ws one?x#y%z", fakehub.Workspace{})
	for _, c := range calls() {
		before := e.hub.Total()
		if err := c.do(e); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		got := e.hub.Requests()[before:]
		if len(got) != 1 {
			t.Errorf("%s: %d requests, want 1", c.name, len(got))
			continue
		}
		r := got[0]
		if r.Method != c.method || r.URI != e.uri(c) {
			t.Errorf("%s: %s %s, want %s %s", c.name, r.Method, r.URI, c.method, e.uri(c))
		}
		if r.Header.Get("Authorization") != "Bearer "+pat || r.Header.Get("Accept") != "application/json" || r.Header.Get("User-Agent") != "agent-fox/9.9.9" {
			t.Errorf("%s: headers %v, want the PAT as the bearer, JSON and the user agent", c.name, r.Header)
		}
		if body := r.Header.Get("Content-Type"); (c.method == "POST") != (body == "application/json") {
			t.Errorf("%s: Content-Type %q, want JSON exactly when there is a body", c.name, body)
		}
	}
	if pushes := e.hub.BranchPushes(); len(pushes) != 1 || pushes[0].Branch != "fix/42-nil-map" {
		t.Errorf("pushes = %+v, want the branch whole", pushes)
	}
}

// Every answer is told apart with errors.Is and errors.As, the same way for every call, and a
// refusal says what hub said.
func TestCalls_TellEveryAnswerApart(t *testing.T) {
	reason := hubError(422, "invalid_title", "the title must not be blank")
	forge := func(kind string) fakehub.Fault {
		return fakehub.Fault{Status: 502, Body: hubError(502, kind, "the forge refused the token of hub")}
	}
	cases := []struct {
		name  string
		fault fakehub.Fault
		want  string
	}{
		{"401", fakehub.Fault{Status: 401, Body: "nope"}, "refused"},
		{"403", fakehub.Fault{Status: 403}, "forbidden"},
		{"404", fakehub.Fault{Status: 404}, "not found"},
		{"400 with no kind", fakehub.Fault{Status: 400, Body: `{"error":{"code":400,"message":"the body is malformed"}}`}, "rejected"},
		{"409", fakehub.Fault{Status: 409}, "rejected"},
		{"422", fakehub.Fault{Status: 422, Body: reason}, "rejected"},
		{"502 forge_unauthenticated", forge("forge_unauthenticated"), "rejected"},
		{"502 forge_forbidden", forge("forge_forbidden"), "rejected"},
		{"502 forge_error", forge("forge_error"), "unavailable"},
		{"429", fakehub.Fault{Status: 429}, "unavailable"},
		{"500", fakehub.Fault{Status: 500}, "unavailable"},
		{"503", fakehub.Fault{Status: 503}, "unavailable"},
		{"a hang-up", fakehub.Fault{Hangup: true}, "unavailable"},
		{"a redirect", fakehub.Fault{Redirect: "/elsewhere"}, "unavailable"},
		{"2 MiB", fakehub.Fault{Oversize: 2 << 20}, "unavailable"},
		{"not JSON", fakehub.Fault{Status: 200, Body: "<html>"}, "unavailable"},
	}
	for _, c := range calls() {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, "ws/1", fakehub.Workspace{})
			for _, tc := range cases {
				err := e.inject(t, c, tc.fault)
				want := tc.want
				if tc.fault.Status != 0 && tc.fault.Status == c.done || tc.name == "not JSON" && !c.reads {
					want = "ok"
				}
				if got := kind(t, err); got != want {
					t.Errorf("%s: %v is %s, want %s", tc.name, err, got, want)
					continue
				}
				rejected, _ := errors.AsType[*RejectedError](err)
				switch {
				case tc.name == "401" && strings.Contains(err.Error(), "hub said"):
					t.Errorf("401: %q quotes hub, which gave no reason", err)
				case tc.name == "403" && !(strings.Contains(err.Error(), c.op) && strings.Contains(err.Error(), "scope") &&
					strings.Contains(err.Error(), "PUT /api/v1/workspaces/ws%2F1") && strings.Contains(err.Error(), "(hub said: injected failure)")):
					t.Errorf("403: %q, want the operation %q, the scope, the route that replaces the token and what hub said", err, c.op)
				case tc.name == "400 with no kind" && (rejected.Status != 400 || rejected.Code != "400" || rejected.Message != "the body is malformed"):
					t.Errorf("400: %+v, want the status as the code and hub's message", rejected)
				case tc.name == "422" && (rejected.Code != "invalid_title" || !strings.Contains(err.Error(), "the title must not be blank") || rejected.Op != c.op):
					t.Errorf("422: %+v, want the kind and the message of hub's error object", rejected)
				}
			}
			if n := len(e.hub.RequestsTo("", "/elsewhere")); n != 0 {
				t.Errorf("a redirect was followed %d times", n)
			}
		})
	}
}

// An ordinary call ends at the client's timeout; the lease and the push wait past it, to the long
// bound or the context, whichever ends first.
func TestCalls_EndAtTheirBound(t *testing.T) {
	for _, c := range calls() {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, "ws", fakehub.Workspace{})
			e.client.HTTP.Timeout, e.client.LongTimeout = 20*time.Millisecond, 10*time.Second
			if !c.long {
				if err := e.inject(t, c, fakehub.Fault{Delay: time.Minute}); !isUnavailable(err) || !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("a late answer: %v, want unavailable at the bound", err)
				}
				return
			}
			if err := e.inject(t, c, fakehub.Fault{Delay: 80 * time.Millisecond}); kind(t, err) != "ok" {
				t.Errorf("an answer past the ordinary bound: %v, want it waited for", err)
			}
			e.client.LongTimeout = 30 * time.Millisecond
			if err := e.inject(t, c, fakehub.Fault{Delay: time.Minute}); !isUnavailable(err) {
				t.Errorf("an answer past the long bound: %v, want unavailable", err)
			}
			e.client.LongTimeout = time.Minute
			short, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
			defer cancel()
			var err error
			if c.name == "Lease" {
				_, err = e.client.Lease(short, pat, e.slug, LeaseRequest{})
			} else {
				err = e.client.PushBranch(short, pat, e.slug, "fix/42-nil-map")
			}
			if !isUnavailable(err) {
				t.Errorf("a context shorter than the long bound: %v, want unavailable", err)
			}
		})
	}
}

func TestCalls_NoErrorHoldsThePATOrTheURL(t *testing.T) {
	faults := map[string]fakehub.Fault{
		"401": {Status: 401, Body: hubError(401, "x", "the token "+pat)}, "403": {Status: 403, Body: hubError(403, "x", "the token "+pat)},
		"404": {Status: 404, Body: hubError(404, "x", pat)}, "422": {Status: 422, Body: hubError(422, "bad", "the token "+pat+" is no good")},
		"502": {Status: 502, Body: hubError(502, "forge_forbidden", pat)}, "500": {Status: 500}, "a hang-up": {Hangup: true},
		"a timeout": {Delay: time.Minute}, "not JSON": {Status: 200, Body: "token " + pat},
		// The transport quotes a Location it cannot parse in its error.
		"a Location that does not parse": {Redirect: "%zz/" + pat},
	}
	all := append(calls(),
		call{name: "User", method: "GET", uri: "/api/v1/user", do: func(e *env) error { _, err := e.client.User(ctx, pat); return err }},
		call{name: "Workspace", method: "GET", uri: "/api/v1/workspaces/{slug}", do: func(e *env) error { return e.client.Workspace(ctx, pat, e.slug) }},
	)
	for _, c := range all {
		e := newEnv(t, "ws", fakehub.Workspace{})
		e.client.HTTP.Timeout, e.client.LongTimeout = 20*time.Millisecond, 20*time.Millisecond
		for name, f := range faults {
			holdsSecret(t, c.name+", "+name, e.inject(t, c, f), e.hub.URL)
		}
	}
}

func TestWorkspaceRecord_ReadsTheRecord(t *testing.T) {
	e := newEnv(t, "acme", fakehub.Workspace{Branch: "trunk", Mode: "carry_patch", ForgeURL: "https://gitlab.example.com/g/p"})
	rec, err := e.client.WorkspaceRecord(ctx, pat, "acme")
	if want := (Record{CloneURL: e.hub.CloneURL("acme"), Branch: "trunk", Mode: ModeCarryPatch, ForgeURL: "https://gitlab.example.com/g/p"}); err != nil || rec != want {
		t.Errorf("record = %+v, %v; want %+v", rec, err, want)
	}
	if repo, ok := rec.Repo(); !ok || repo.Host != "gitlab.example.com" || repo.Owner != "g" || repo.Name != "p" {
		t.Errorf("Repo = %+v, %v; want the repository of the forge URL", repo, ok)
	}

	const base = `"git_url":"https://github.example.com/o/r","hub_url":"http://hub.example.com/git/acme.git","branch":"main"`
	for body, want := range map[string]string{
		`{` + base + `}`:                      ModeStandard,
		`{` + base + `,"mode":"carry_patch"}`: ModeCarryPatch,
		`{` + base + `,"workspace_mode":"standard","mode":"carry_patch"}`:                                 ModeStandard,
		`{"git_url":"https://github.example.com/o/r","hub_url":null,"branch":"main"}`:                     "",
		`{"git_url":"https://github.example.com/o/r","hub_url":"http://hub.example.com/x","branch":null}`: "",
		`{"hub_url":"http://hub.example.com/git/acme.git","branch":"main"}`:                               "",
		`[]`: "",
	} {
		e.answer("GET", "/api/v1/workspaces/acme", fakehub.Fault{Status: 200, Body: body})
		rec, err := e.client.WorkspaceRecord(ctx, pat, "acme")
		if want == "" && !isUnavailable(err) || want != "" && (err != nil || rec.Mode != want) {
			t.Errorf("%s: %+v, %v; want mode %q (none: unavailable)", body, rec, err, want)
		}
	}
}

func TestResolvedVars_ReadsEveryEncoding(t *testing.T) {
	vars := map[string]string{"AF_MODEL": "m1", "AF_TEXT": "a \"quoted\"\\ line\n\téü", "AF_EMPTY": ""}
	for _, form := range []string{"", "object", "namelist"} {
		for _, want := range []map[string]string{vars, {}} {
			e := newEnv(t, "ws", fakehub.Workspace{Vars: want, VarsForm: form})
			if got, err := e.client.ResolvedVars(ctx, pat, "ws"); err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("form %q: %v, %v; want %v", form, got, err, want)
			}
		}
	}
	e := newEnv(t, "ws", fakehub.Workspace{})
	for _, body := range []string{`[{"value":"x"}]`, `{}`, `{"vars":null}`, `"x"`, `null`} {
		e.answer("GET", e.path("/vars/resolved"), fakehub.Fault{Status: 200, Body: body})
		if _, err := e.client.ResolvedVars(ctx, pat, "ws"); !isUnavailable(err) {
			t.Errorf("%s: %v, want unavailable", body, err)
		}
	}
}

func TestSessions_AreOpenedReportedAndCompleted(t *testing.T) {
	e := newEnv(t, "acme", fakehub.Workspace{})
	id, err := e.client.OpenSession(ctx, pat, SessionRequest{WorkspaceSlug: "acme", Archetype: "triage", Model: "m-1", RunID: runID})
	if err != nil || id == "" {
		t.Fatalf("OpenSession = %q, %v", id, err)
	}
	if got := e.sent(t, "POST", "/api/v1/sessions"); !reflect.DeepEqual(got, map[string]any{"workspace_slug": "acme", "archetype": "triage", "model": "m-1", "run_id": runID}) {
		t.Errorf("the session request = %v", got)
	}
	if err := e.client.PostUsage(ctx, pat, "acme", id, Usage{Model: "m-1", Brain: "claude", InputTokens: 7, OutputTokens: 9, CostUSD: 0.5, CostSource: "reported", Turns: 3}); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"model": "m-1", "input_tokens": 7.0, "output_tokens": 9.0, "cost_usd": 0.5, "cost_source": "reported", "brain": "claude", "turns": 3.0}
	if got := e.sent(t, "POST", "/api/v1/sessions/"+id+"/usage"); !reflect.DeepEqual(got, want) {
		t.Errorf("the usage = %v, want %v", got, want)
	}
	for _, c := range []struct {
		status, message string
		want            map[string]any
	}{
		{SessionCompleted, "", map[string]any{"status": "completed"}},
		{SessionFailed, "the lease was refused", map[string]any{"status": "failed", "error_message": "the lease was refused"}},
	} {
		if err := e.client.CompleteSession(ctx, pat, "acme", id, c.status, c.message); err != nil {
			t.Fatal(err)
		}
		if got := e.sent(t, "POST", "/api/v1/sessions/"+id+"/complete"); !reflect.DeepEqual(got, c.want) {
			t.Errorf("the completion = %v, want %v", got, c.want)
		}
	}
	if s := e.hub.Sessions(); len(s) != 1 || s[0].Status != "failed" || s[0].Message != "the lease was refused" || len(s[0].Usage) != 1 {
		t.Errorf("sessions = %+v", s)
	}

	e.answer("POST", "/api/v1/sessions", fakehub.Fault{Status: 201, Body: `{"workspace_slug":"acme"}`})
	if _, err := e.client.OpenSession(ctx, pat, SessionRequest{WorkspaceSlug: "acme"}); !isUnavailable(err) {
		t.Errorf("a session with no id: %v, want unavailable", err)
	}
}

func TestPostEvents_SendsEachEventWithItsIDAndAnObjectForAPayload(t *testing.T) {
	e := newEnv(t, "acme", fakehub.Workspace{})
	events := []Event{
		{Seq: 4, TS: "2026-01-01T00:00:00Z", Type: "step", Data: json.RawMessage(`{"name":"clone"}`)},
		{Seq: 5, TS: "2026-01-01T00:00:01Z", Type: "tool_call", Data: json.RawMessage(`"text"`)},
		{Seq: 6, TS: "2026-01-01T00:00:02Z", Type: "heartbeat"},
		{Seq: 7, TS: "2026-01-01T00:00:03Z", Type: "heartbeat", Data: json.RawMessage(`null`)},
	}
	if err := e.client.PostEvents(ctx, pat, "acme", runID, events); err != nil {
		t.Fatal(err)
	}
	batches := e.hub.EventBatches("acme", runID)
	if len(batches) != 1 || len(batches[0]) != len(events) {
		t.Fatalf("batches = %+v", batches)
	}
	for i, payload := range []string{`{"name":"clone"}`, `{"data":"text"}`, `{}`, `{}`} {
		got, ev := batches[0][i], events[i]
		if got.ID != fmt.Sprintf("%s-%d", runID, ev.Seq) || got.Type != ev.Type || got.Timestamp != ev.TS || got.RunID != runID || string(got.Payload) != payload {
			t.Errorf("event %d = %+v, want the id of the run and the number, and the payload %s", i, got, payload)
		}
	}
}

// Hub takes the others of a batch with an event it does not take; what it says of that event is cut
// short and holds no token.
func TestPostEvents_AnEventHubDidNotTakeIsARejection(t *testing.T) {
	e := newEnv(t, "acme", fakehub.Workspace{})
	long := strings.Repeat("x", 5000)
	e.answer("POST", e.path("/runs/r1/events/batch"), fakehub.Fault{Status: 200, Body: `{"accepted":1,"errors":[{"index":1,"message":"bad ` + pat + ` ` + long + `"}]}`})
	err := e.client.PostEvents(ctx, pat, "acme", "r1", []Event{{Seq: 1}, {Seq: 2}})
	rejected, ok := errors.AsType[*RejectedError](err)
	if !ok || rejected.Code != "partial" || !strings.Contains(err.Error(), "1 of 2 events: bad [token]") || len(err.Error()) > maxMessage+200 {
		t.Errorf("error = %.200q (%d bytes), want a rejection that says which, cut short, without the token", err, len(err.Error()))
	}
}

func TestLease_SendsThePurposeTheLabelsAndAProfileAndReadsTheLease(t *testing.T) {
	for _, c := range []struct {
		req  LeaseRequest
		want map[string]any
	}{
		{LeaseRequest{Purpose: "fix", Labels: map[string]string{"job": "job_1"}}, map[string]any{"purpose": "fix", "labels": map[string]any{"job": "job_1"}}},
		{LeaseRequest{Profile: "big", Purpose: "triage"}, map[string]any{"purpose": "triage", "profile": "big", "labels": map[string]any{}}},
	} {
		e := newEnv(t, "acme", fakehub.Workspace{})
		lease, err := e.client.Lease(ctx, pat, "acme", c.req)
		if err != nil || !strings.HasPrefix(lease.ID, "sbx_") || lease.Token == "" || lease.GatewayURL != e.hub.Gateway().URL() || time.Until(lease.ExpiresAt) < 25*time.Minute {
			t.Errorf("lease = %+v, %v", lease, err)
		}
		if got := e.sent(t, "POST", e.path("/sandboxes")); !reflect.DeepEqual(got, c.want) {
			t.Errorf("the request = %v, want %v", got, c.want)
		}
	}
	e := newEnv(t, "acme", fakehub.Workspace{})
	for _, body := range []string{
		`{"id":"sbx_1","gateway_url":"http://gw.example.com","token":"t","expires_at":"yesterday"}`,
		`{"id":"","gateway_url":"http://gw.example.com","token":"t","expires_at":"2030-01-01T00:00:00Z"}`,
		`{"id":"sbx_1","gateway_url":"http://gw.example.com","expires_at":"2030-01-01T00:00:00Z"}`,
	} {
		e.answer("POST", e.path("/sandboxes"), fakehub.Fault{Status: 201, Body: body})
		if _, err := e.client.Lease(ctx, pat, "acme", LeaseRequest{}); !isUnavailable(err) {
			t.Errorf("%s: %v, want unavailable", body, err)
		}
	}
}

func TestReleaseLease_ReleasesTheLease(t *testing.T) {
	e := newEnv(t, "acme", fakehub.Workspace{})
	lease, err := e.client.Lease(ctx, pat, "acme", LeaseRequest{Purpose: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.client.ReleaseLease(ctx, pat, "acme", lease.ID); err != nil || len(e.hub.Deleted()) != 1 || e.hub.Deleted()[0] != lease.ID {
		t.Errorf("ReleaseLease = %v, released %v; want the lease released", err, e.hub.Deleted())
	}
}

func TestListSandboxes_ListsTheLeasesWithTheirLabelsAndEnd(t *testing.T) {
	e := newEnv(t, "acme", fakehub.Workspace{})
	if got, err := e.client.ListSandboxes(ctx, pat, "acme"); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("a workspace with no lease = %v, %v, want an empty list", got, err)
	}
	a := e.hub.SeedLease("acme", map[string]string{"job": "job_1", "attempt": "2"}, "fix")
	b := e.hub.SeedLease("acme", nil, "triage")
	got, err := e.client.ListSandboxes(ctx, pat, "acme")
	if err != nil || len(got) != 2 {
		t.Fatalf("ListSandboxes = %v, %v, want the two leases", got, err)
	}
	byID := map[string]LeaseInfo{got[0].ID: got[0], got[1].ID: got[1]}
	if l := byID[a.ID]; l.Purpose != "fix" || !reflect.DeepEqual(l.Labels, a.Labels) || !l.ExpiresAt.Equal(a.ExpiresAt.Truncate(time.Second)) || l.State != "leased" {
		t.Errorf("lease a listed as %+v, want %+v", l, a)
	}
	if l := byID[b.ID]; l.Purpose != "triage" || len(l.Labels) != 0 {
		t.Errorf("lease b listed as %+v, want triage with no labels", l)
	}
	for _, body := range []string{`[{"id":"","expires_at":"2030-01-01T00:00:00Z"}]`, `[{"id":"sbx_1","expires_at":"soon"}]`} {
		e.answer("GET", e.path("/sandboxes"), fakehub.Fault{Status: 200, Body: body})
		if _, err := e.client.ListSandboxes(ctx, pat, "acme"); !isUnavailable(err) {
			t.Errorf("%s: %v, want unavailable", body, err)
		}
	}
}

func TestCreateLabel_SendsTheLabelAndSaysWhetherHubCreatedIt(t *testing.T) {
	e := newEnv(t, "acme", fakehub.Workspace{})
	create := func() (bool, error) { return e.client.CreateLabel(ctx, pat, "acme", "af:fix", "0e8a16", "Fix it") }
	if created, err := create(); err != nil || !created {
		t.Errorf("first: %v, %v; want created", created, err)
	}
	if got := e.sent(t, "POST", e.path("/labels")); !reflect.DeepEqual(got, map[string]any{"name": "af:fix", "color": "0e8a16", "description": "Fix it"}) {
		t.Errorf("the request = %v", got)
	}
	if created, err := create(); err != nil || created {
		t.Errorf("second: %v, %v; want already there", created, err)
	}

	there := []fakehub.Fault{
		{Status: 409},
		{Status: 422, Body: hubError(422, "already_exists", "Validation Failed")},
		{Status: 400, Body: `{"error":{"code":400,"message":"label ALREADY EXISTS on the project"}}`},
		{Status: 422, Body: `{"error":{"code":"already_exists","message":"Validation Failed"}}`},
	}
	for _, f := range there {
		e.answer("POST", e.path("/labels"), f)
		if created, err := create(); err != nil || created {
			t.Errorf("%d %s: %v, %v; want already there", f.Status, f.Body, created, err)
		}
	}
	for _, c := range []struct {
		fault fakehub.Fault
		want  string
	}{
		{fakehub.Fault{Status: 400, Body: hubError(400, "bad_request", "bad name")}, "rejected"},
		{fakehub.Fault{Status: 502, Body: hubError(502, "forge_forbidden", "the label already exists")}, "rejected"},
		{fakehub.Fault{Status: 201, Body: "null"}, "unavailable"},
	} {
		e.answer("POST", e.path("/labels"), c.fault)
		if created, err := create(); created || kind(t, err) != c.want {
			t.Errorf("%d %s: %v, %v; want %s", c.fault.Status, c.fault.Body, created, err, c.want)
		}
	}
}
