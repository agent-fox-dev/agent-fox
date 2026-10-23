package hub

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

// createLabel creates the label af:fix of the label table on the workspace of the env.
func (e *workerEnv) createLabel() (bool, error) {
	return e.client.CreateLabel(ctx, workerPAT, e.slug, "af:fix", "0e8a16", "Ask agent-fox to fix this issue")
}

// labelPath is the decoded path of the label route of the env's workspace.
func (e *workerEnv) labelPath() string { return "/api/v1/workspaces/" + e.slug + "/labels" }

// TestCreateLabelSendsTheSpecifiedRequest_TS_17_20 verifies TS-17-20: the call is POST
// /api/v1/workspaces/{slug}/labels with exactly name, color and description, the PAT as the bearer, the client's 10-second
// bound, no redirect followed and the client's reading limit.
// Verifies: 17-REQ-4.1
func TestCreateLabelSendsTheSpecifiedRequest_TS_17_20(t *testing.T) {
	e := newWorkerEnv(t, "team/widgets", fakehub.Workspace{})
	if _, err := e.createLabel(); err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	reqs := e.hub.RequestsTo(http.MethodPost, e.labelPath())
	if len(reqs) != 1 {
		t.Fatalf("%d requests to the label route, want 1", len(reqs))
	}
	r := reqs[0]
	if r.URI != "/api/v1/workspaces/team%2Fwidgets/labels" || r.Header.Get("Authorization") != "Bearer "+workerPAT ||
		!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		t.Errorf("the request is %s %s with %q, want POST at the escaped slug with the PAT as the bearer", r.Method, r.URI, r.Header.Get("Authorization"))
	}
	var body map[string]any
	if err := json.Unmarshal(r.Body, &body); err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"name": "af:fix", "color": "0e8a16", "description": "Ask agent-fox to fix this issue"}; fmt.Sprint(body) != fmt.Sprint(want) {
		t.Errorf("the body is %v, want exactly %v", body, want)
	}

	if got := New("http://127.0.0.1:1", "1").HTTP.Timeout; got != 10*time.Second {
		t.Errorf("the bound = %v, want 10s", got)
	}
	e.client.HTTP.Timeout = 80 * time.Millisecond // the 10 s of the client, scaled
	var unavailable *UnavailableError
	e.hub.Inject(http.MethodPost, e.labelPath(), fakehub.Fault{Delay: 400 * time.Millisecond})
	start := time.Now()
	if _, err := e.createLabel(); !errors.As(err, &unavailable) || time.Since(start) > 350*time.Millisecond {
		t.Errorf("a late answer: %v after %v, want unavailable at the bound", err, time.Since(start))
	}
	e.hub.Clear()
	e.hub.Inject(http.MethodPost, e.labelPath(), fakehub.Fault{Redirect: "/elsewhere"})
	if _, err := e.createLabel(); !errors.As(err, &unavailable) || len(e.hub.RequestsTo("", "/elsewhere")) != 0 {
		t.Errorf("a 302: %v, want unavailable and the redirect not followed", err)
	}
	e.hub.Clear()
	e.hub.Inject(http.MethodPost, e.labelPath(), fakehub.Fault{Oversize: 2 << 20})
	if _, err := e.createLabel(); !errors.As(err, &unavailable) {
		t.Errorf("an answer of 2 MiB: %v, want unavailable", err)
	}
}

// TestCreateLabel201IsCreatedAnd200IsAlreadyThere_TS_17_21 verifies TS-17-21.
// Verifies: 17-REQ-4.2
func TestCreateLabel201IsCreatedAnd200IsAlreadyThere_TS_17_21(t *testing.T) {
	e := newWorkerEnv(t, "widgets", fakehub.Workspace{})
	if created, err := e.createLabel(); err != nil || !created {
		t.Errorf("first: created = %v, %v; want created", created, err)
	}
	if created, err := e.createLabel(); err != nil || created {
		t.Errorf("second: created = %v, %v; want already there", created, err)
	}
	// A stub that answers 201 and 200 with any label of hub's shape.
	e.hub.Clear()
	for status, want := range map[int]bool{201: true, 200: false} {
		e.hub.Inject(http.MethodPost, e.labelPath(), fakehub.Fault{Status: status, Body: `{"name":"af:fix","color":"0e8a16","description":""}`, Once: true})
		if created, err := e.createLabel(); err != nil || created != want {
			t.Errorf("%d: created = %v, %v; want %v", status, created, err, want)
		}
	}
}

// TestALabelThatHubSaysExistsIsAlreadyThereAndOtherRefusalsAreNot_TS_17_22 verifies TS-17-22.
// Verifies: 17-REQ-4.3
func TestALabelThatHubSaysExistsIsAlreadyThereAndOtherRefusalsAreNot_TS_17_22(t *testing.T) {
	e := newWorkerEnv(t, "widgets", fakehub.Workspace{})
	there := map[string]fakehub.Fault{
		"409":                          {Status: 409},
		"422 already_exists":           {Status: 422, Body: `{"error":{"code":422,"message":"Validation Failed","error_type":"already_exists"}}`},
		"400 ALREADY EXISTS":           {Status: 400, Body: `{"error":{"code":400,"message":"label ALREADY EXISTS on the project"}}`},
		"400 Label Already Exists":     {Status: 400, Body: `{"error":{"code":400,"message":"Label Already Exists","error_type":"bad_request"}}`},
		"422 code says already_exists": {Status: 422, Body: `{"error":{"code":"already_exists","message":"Validation Failed"}}`},
	}
	for name, f := range there {
		e.hub.Clear()
		e.hub.Inject(http.MethodPost, e.labelPath(), f)
		if created, err := e.createLabel(); err != nil || created {
			t.Errorf("%s: created = %v, %v; want already there", name, created, err)
		}
	}
	e.hub.Clear()
	e.hub.Inject(http.MethodPost, e.labelPath(), fakehub.Fault{Status: 400, Body: `{"error":{"code":400,"message":"bad name","error_type":"bad_request"}}`})
	var rejected *RejectedError
	if _, err := e.createLabel(); !errors.As(err, &rejected) || rejected.Message != "bad name" {
		t.Errorf("400 bad name: %v, want a *RejectedError", err)
	}
}

// TestEveryOtherLabelAnswerIsClassedAsTheOtherCallsAndCarriesNoPAT_TS_17_23 verifies TS-17-23.
// Verifies: 17-REQ-4.4
func TestEveryOtherLabelAnswerIsClassedAsTheOtherCallsAndCarriesNoPAT_TS_17_23(t *testing.T) {
	e := newWorkerEnv(t, "widgets", fakehub.Workspace{})
	e.client.HTTP.Timeout = 100 * time.Millisecond
	forge := func(kind string) fakehub.Fault {
		return fakehub.Fault{Status: 502, Body: `{"error":{"code":502,"message":"the forge said no to ` + workerPAT + `","error_type":"` + kind + `"}}`}
	}
	isRejected := func(code string) func(error) bool {
		return func(err error) bool {
			var r *RejectedError
			return errors.As(err, &r) && (code == "" || r.Code == code)
		}
	}
	isUnavailable := func(err error) bool { var u *UnavailableError; return errors.As(err, &u) }
	cases := []struct {
		name  string
		fault fakehub.Fault
		is    func(error) bool
	}{
		{"401", fakehub.Fault{Status: 401}, func(err error) bool { return errors.Is(err, ErrRefused) }},
		{"403", fakehub.Fault{Status: 403}, func(err error) bool { return errors.Is(err, ErrForbidden) }},
		{"404", fakehub.Fault{Status: 404}, func(err error) bool { return errors.Is(err, ErrNotFound) }},
		{"405", fakehub.Fault{Status: 405, Body: `{"error":{"code":405,"message":"method not allowed","error_type":"method_not_allowed"}}`}, isRejected("method_not_allowed")},
		{"400", fakehub.Fault{Status: 400, Body: `{"error":{"code":400,"message":"invalid label name","error_type":"bad_request"}}`}, isRejected("bad_request")},
		{"429", fakehub.Fault{Status: 429}, isUnavailable},
		{"500", fakehub.Fault{Status: 500}, isUnavailable},
		{"redirect", fakehub.Fault{Redirect: "/elsewhere"}, isUnavailable},
		{"502 forge_forbidden", forge("forge_forbidden"), isRejected("forge_forbidden")},
		{"502 forge_unauthenticated", forge("forge_unauthenticated"), isRejected("forge_unauthenticated")},
		{"502 other", forge("forge_error"), isUnavailable},
		{"not hub's body", fakehub.Fault{Status: 201, Body: "<html>" + workerPAT}, isUnavailable},
		{"a hang-up", fakehub.Fault{Hangup: true}, isUnavailable},
		{"a timeout", fakehub.Fault{Delay: 400 * time.Millisecond}, isUnavailable},
	}
	for _, c := range cases {
		e.hub.Clear()
		e.hub.Inject(http.MethodPost, e.labelPath(), c.fault)
		created, err := e.createLabel()
		if err == nil || created || !c.is(err) {
			t.Errorf("%s: created = %v, %v; not the class it should be", c.name, created, err)
			continue
		}
		for e := err; e != nil; e = errors.Unwrap(e) {
			if text := fmt.Sprintf("%+v %v %#v", e, e.Error(), e); strings.Contains(text, workerPAT) {
				t.Errorf("%s: an error holds the PAT: %s", c.name, text)
			}
		}
	}
}

// TestForgeClientCreateLabelStaysNotAvailable_TS_17_24 verifies TS-17-24: the forge client's CreateLabel sends nothing and
// says that it is not available through hub, as before.
// Verifies: 17-REQ-4.5
func TestForgeClientCreateLabelStaysNotAvailable_TS_17_24(t *testing.T) {
	e := newWorkerEnv(t, "widgets", fakehub.Workspace{})
	rec, err := e.client.WorkspaceRecord(ctx, workerPAT, e.slug)
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := rec.Repo()
	before := e.hub.Total()
	err = e.client.Forge(workerPAT, e.slug, repo).CreateLabel(ctx, repo, issuex.Label{Name: "af:fix", Color: "0e8a16"})
	if err == nil || !strings.Contains(err.Error(), "CreateLabel is not available through hub in this release") {
		t.Errorf("ForgeClient.CreateLabel = %v, want the not-available error", err)
	}
	if e.hub.Total() != before {
		t.Error("ForgeClient.CreateLabel sent a request")
	}
	// The forge client's own test of the methods that are not available stands as it is.
	t.Run("TS-13-25", TestUnlistedMethodsSendNothingAndSayTheyAreNotAvailable_TS_13_25)
}
