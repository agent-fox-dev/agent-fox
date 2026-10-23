package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

// TestEveryErrorIsAnEnvelopeWithItsMappedStatus_TS_11_49 verifies TS-11-49:
// Every error is an application/json envelope with code and message, no
// error_type, and the mapped status. Each of the ten codes is provoked the way a
// client can provoke it, through the whole service.
// Verifies: 11-REQ-7.3
func TestEveryErrorIsAnEnvelopeWithItsMappedStatus_TS_11_49(t *testing.T) {
	a := newAuthHarness(t)
	a.Hub.AddUser("caller", "usr_1", []string{"widgets", "gadgets"})
	a.Hub.AddUser("good-pat", "usr_9", []string{"widgets"})
	a.Hub.AddUser("fresh", "usr_2", nil) // a caller whom the service has not asked hub about
	bigBody := `{"hub_token":"` + strings.Repeat("x", 65*1024) + `"}`
	json := []string{"Content-Type", "application/json"}
	withToken := func(h ...string) []string { return append(bearer("caller"), h...) }

	type request struct {
		method, path string
		headers      []string
		body         string
		prepare      func()
	}
	for _, tc := range []struct {
		name   string
		req    request
		status int
		code   string
	}{
		{"invalid_request", request{"GET", "/api/v1/workspaces/-bad", withToken(), "", nil}, 400, "invalid_request"},
		{"unauthenticated", request{"GET", "/api/v1/workspaces", nil, "", nil}, 401, "unauthenticated"},
		{"not_found, an unknown route", request{"GET", "/nowhere", nil, "", nil}, 404, "not_found"},
		{"not_found, a workspace", request{"GET", "/api/v1/workspaces/widgets", withToken(), "", nil}, 404, "not_found"},
		{"method_not_allowed", request{"POST", "/healthz", nil, "", nil}, 405, "method_not_allowed"},
		{"payload_too_large", request{"PUT", "/api/v1/workspaces/widgets", withToken(json...), bigBody, nil}, 413, "payload_too_large"},
		{"unsupported_media_type", request{"PUT", "/api/v1/workspaces/widgets", withToken("Content-Type", "text/plain"), `{"hub_token":"good-pat"}`, nil}, 415, "unsupported_media_type"},
		{"invalid_hub_token", request{"PUT", "/api/v1/workspaces/widgets", withToken(json...), `{"hub_token":"refused-pat"}`, nil}, 422, "invalid_hub_token"},
		{"internal", request{"GET", "/api/v1/test/boom", nil, "", nil}, 500, "internal"},
		{"hub_unavailable", request{"GET", "/api/v1/workspaces", bearer("fresh"), "", func() { a.Hub.Fail(userPath, 500) }}, 502, "hub_unavailable"},
		{"not_ready", request{"GET", "/readyz", nil, "", func() { a.Store.Close() }}, 503, "not_ready"},
	} {
		if tc.req.prepare != nil {
			tc.req.prepare()
		}
		var body *strings.Reader
		if tc.req.body != "" {
			body = strings.NewReader(tc.req.body)
		}
		var r response
		if body == nil {
			r = a.do(t, tc.req.method, tc.req.path, nil, tc.req.headers...)
		} else {
			r = a.do(t, tc.req.method, tc.req.path, body, tc.req.headers...)
		}
		if r.Status != tc.status || r.errorCode(t) != tc.code {
			t.Errorf("%s: %s %s = %d %s, want %d %s", tc.name, tc.req.method, tc.req.path, r.Status, r.Body, tc.status, tc.code)
		}
		if e, _ := r.JSON(t)["error"].(map[string]any); len(e) != 2 {
			t.Errorf("%s: the error has the keys %v, want code and message only", tc.name, e)
		}
	}
}

// TestForgeForbiddenIsA422CodeAndNoSubscribeAnswerOrLogHoldsTheToken_TS_17_19 verifies TS-17-19.
// Verifies: 17-REQ-3.8
func TestForgeForbiddenIsA422CodeAndNoSubscribeAnswerOrLogHoldsTheToken_TS_17_19(t *testing.T) {
	if CodeForgeForbidden != "forge_forbidden" || CodeForgeForbidden.Status() != 422 {
		t.Fatalf("%s has status %d, want forge_forbidden with 422", CodeForgeForbidden, CodeForgeForbidden.Status())
	}
	const token = "pat-recognisable-9876543210"
	for name, f := range map[string]fakehub.Fault{
		"403":                 {Status: 403},
		"502 forge_forbidden": {Status: 502, Body: `{"error":{"code":502,"message":"the forge refused ` + token + `","error_type":"forge_forbidden"}}`},
	} {
		t.Run(name, func(t *testing.T) {
			a := newAuthHarness(t)
			a.Hub.AddUser("caller", "usr_1", []string{"widgets"})
			a.Hub.AddUser(token, "usr_9", []string{"widgets"})
			a.Hub.Inject("POST", "/api/v1/workspaces/widgets/labels", f)
			r := a.put(t, "widgets", "caller", tokenBody(t, token))
			var body map[string]map[string]any
			if err := json.Unmarshal([]byte(r.Body), &body); err != nil || r.Status != 422 || body["error"]["code"] != "forge_forbidden" || body["error"]["message"] == "" || len(body) != 1 || len(body["error"]) != 2 {
				t.Errorf("PUT = %d %s, want 422 {error: {code: forge_forbidden, message}}", r.Status, r.Body)
			}
			if strings.Contains(string(r.Body), token) || strings.Contains(a.Logs.String(), token) {
				t.Error("an answer or a log record holds the hub_token")
			}
		})
	}
}
