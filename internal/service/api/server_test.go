package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestProbesAnswerWithoutAToken(t *testing.T) {
	e := newEnv(t)
	for path, want := range map[string]map[string]any{
		"/healthz": {"status": "ok"},
		"/readyz":  {"status": "ready"},
		"/version": {"name": "agent-fox", "version": "1.2.3", "commit": "abc123"},
	} {
		r := e.get(t, path, "")
		if r.Status != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			t.Errorf("GET %s = %d %q, want 200 application/json", path, r.Status, r.Header.Get("Content-Type"))
		}
		got := r.Object(t)
		for k, v := range want {
			if got[k] != v {
				t.Errorf("GET %s = %s, want %s = %v", path, r.Body, k, v)
			}
		}
	}
	if n := e.Hub.Total(); n != 0 {
		t.Errorf("the probes made %d calls to hub, want none", n)
	}
}

func TestReadyz_IsNotReadyWithoutTheDatabase(t *testing.T) {
	e := newEnv(t)
	if err := e.Store.Close(); err != nil {
		t.Fatal(err)
	}
	failed(t, "GET /readyz", e.get(t, "/readyz", ""), 503, CodeNotReady)
	if r := e.get(t, "/healthz", ""); r.Status != 200 {
		t.Errorf("GET /healthz = %d, want 200 while the database is gone", r.Status)
	}
}

func TestCode_StatusOfEachCode(t *testing.T) {
	for code, status := range map[Code]int{
		CodeInvalidRequest: 400, CodeUnauthenticated: 401, CodeNotFound: 404, CodeMethodNotAllowed: 405,
		CodePayloadTooLarge: 413, CodeUnsupportedMedia: 415, CodeInvalidHubToken: 422, CodeForgeForbidden: 422,
		CodeInvalidState: 409, CodeDuplicateJob: 409, CodeNotSubscribed: 409, CodeTokenUnreadable: 409,
		CodeInternal: 500, CodeHubUnavailable: 502, CodeNotReady: 503, "made_up": 500,
	} {
		if got := code.Status(); got != status {
			t.Errorf("%s has the status %d, want %d", code, got, status)
		}
	}
}

func TestRouting_WhatTheMuxWouldAnswerInPlainTextIsAnEnvelope(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct {
		method, path string
		status       int
		code         Code
	}{
		{"GET", "/nowhere", 404, CodeNotFound},
		{"GET", "/api/v1/nowhere", 404, CodeNotFound},
		{"GET", "/", 404, CodeNotFound},
		{"GET", "/healthz/", 404, CodeNotFound},
		{"GET", "//healthz", 404, CodeNotFound},
		{"GET", "/x/../healthz", 404, CodeNotFound},
		{"GET", "/api/v1/workspaces/", 404, CodeNotFound},
		{"POST", "/healthz", 405, CodeMethodNotAllowed},
		{"DELETE", "/version", 405, CodeMethodNotAllowed},
		{"PATCH", "/api/v1/workspaces/widgets", 405, CodeMethodNotAllowed},
		{"POST", "/api/v1/workspaces", 405, CodeMethodNotAllowed},
	} {
		failed(t, tc.method+" "+tc.path, e.call(t, tc.method, tc.path, nil, ""), tc.status, tc.code)
	}
	if allow := e.call(t, "POST", "/healthz", nil, "").Header.Get("Allow"); !strings.Contains(allow, "GET") {
		t.Errorf("Allow = %q, want it to list GET", allow)
	}

	// The target of OPTIONS * is no path, and no route has it.
	w := httptest.NewRecorder()
	e.HTTP.Handler.ServeHTTP(w, httptest.NewRequest("OPTIONS", "*", nil))
	failed(t, "OPTIONS *", testutil.Response{Status: w.Code, Header: w.Header(), Body: w.Body.String()}, 404, CodeNotFound)
}

func TestPanic_IsAnsweredInternalAndTheServerGoesOn(t *testing.T) {
	e := newEnv(t)
	r := e.get(t, "/test/boom", "")
	failed(t, "GET /test/boom", r, 500, CodeInternal)
	if strings.Contains(r.Body, "the-panic-message-7f3a") {
		t.Errorf("the panic is in the answer: %s", r.Body)
	}
	recs := e.Log.Find("ERROR", "panic in handler")
	if len(recs) != 1 || !strings.Contains(recs[0]["stack"].(string), "goroutine") || recs[0]["panic"] != "the-panic-message-7f3a" {
		t.Errorf("the panic records are %v, want one with the panic and its stack", recs)
	}
	if reqs := e.requests(); len(reqs) != 1 || reqs[0]["status"] != float64(500) {
		t.Errorf("the request records are %v, want one with the status 500", reqs)
	}
	if r := e.get(t, "/healthz", ""); r.Status != 200 {
		t.Errorf("GET /healthz after the panic = %d, want 200", r.Status)
	}
}

func TestPanic_AfterTheAnswerBeganKeepsWhatWasSent(t *testing.T) {
	e := newEnv(t)
	if r := e.get(t, "/test/late", ""); r.Status != 202 {
		t.Errorf("GET /test/late = %d, want the status that was sent", r.Status)
	}
	if n := len(e.Log.Find("ERROR", "")); n != 1 {
		t.Errorf("%d ERROR records, want the one of the panic:\n%s", n, e.Log)
	}
	if r := e.get(t, "/healthz", ""); r.Status != 200 {
		t.Errorf("GET /healthz afterwards = %d, want 200", r.Status)
	}
}

func TestPanic_AbortHandlerClosesTheConnection(t *testing.T) {
	e := newEnv(t)
	resp, err := http.Get(e.URL + "/test/abort")
	if err == nil {
		resp.Body.Close()
		t.Fatalf("GET /test/abort = %d, want the connection closed without an answer", resp.StatusCode)
	}
	if n := len(e.Log.Find("ERROR", "panic in handler")); n != 0 {
		t.Errorf("an abort was logged as a panic %d times", n)
	}
}

func TestLog_WhatNetHTTPReportsIsARecord(t *testing.T) {
	e := newEnv(t)
	e.get(t, "/test/twice", "")
	if len(e.Log.Find("WARN", "superfluous")) != 1 {
		t.Errorf("the complaint of net/http is not a WARN record:\n%s", e.Log)
	}
}

func TestLog_EachRequestIsOneRecordWithoutTheQuery(t *testing.T) {
	e := newEnv(t)
	e.Hub.AddUser("tok", "usr_1", nil)
	e.get(t, "/healthz", "")
	e.get(t, pingPath+"?secret=query-7c1", "tok")
	e.get(t, pingPath, "refused")
	e.call(t, "POST", "/healthz", nil, "")

	want := []struct {
		method, path string
		status       float64
		user         any
	}{
		{"GET", "/healthz", 200, nil}, {"GET", pingPath, 200, "usr_1"}, {"GET", pingPath, 401, nil}, {"POST", "/healthz", 405, nil},
	}
	reqs := e.requests()
	if len(reqs) != len(want) {
		t.Fatalf("%d request records, want %d:\n%s", len(reqs), len(want), e.Log)
	}
	for i, w := range want {
		rec := reqs[i]
		if rec["method"] != w.method || rec["path"] != w.path || rec["status"] != w.status || rec["user"] != w.user || rec["duration"] == nil {
			t.Errorf("record %d = %v, want %s %s %v, the user %v and a duration", i, rec, w.method, w.path, w.status, w.user)
		}
	}
	if strings.Contains(e.Log.String(), "query-7c1") {
		t.Error("the query of a request is in the log")
	}
}

func TestLog_HoldsNoCredentialAndNoBody(t *testing.T) {
	e := newEnv(t)
	auth := testutil.Bearer("SECRET-TOKEN-91b")
	e.call(t, "PUT", "/test/echo", `{"hub_token":"SECRET-PAT-44c"}`, "", auth, jsonType)
	e.call(t, "PUT", "/test/echo", `{"hub_token":"SECRET-PAT-44c","x":1}`, "", auth, jsonType)
	e.call(t, "PUT", "/nowhere", `SECRET-BODY-3e9`, "", auth, jsonType)
	e.call(t, "GET", "/test/boom", nil, "", auth)
	for _, needle := range []string{"SECRET", "Bearer", "hub_token"} {
		if strings.Contains(e.Log.String(), needle) {
			t.Errorf("the log holds %q:\n%s", needle, e.Log)
		}
	}
}

func TestBody_IsReadByTheRulesOfTheAPI(t *testing.T) {
	e := newEnv(t)
	const prefix, suffix = `{"hub_token":"`, `"}`
	atLimit := prefix + strings.Repeat("x", maxBody-len(prefix)-len(suffix)) + suffix
	for name, tc := range map[string]struct {
		contentType, body string
		status            int
		code              Code
	}{
		"valid":                                {"application/json", `{"hub_token":"p"}`, 200, ""},
		"a charset and another case":           {"Application/JSON;charset=UTF-8", `{"hub_token":"p"}`, 200, ""},
		"exactly the limit":                    {"application/json", atLimit, 200, ""},
		"white space after the value":          {"application/json", "{\"hub_token\":\"p\"}\n\n", 200, ""},
		"text/plain":                           {"text/plain", `{"hub_token":"p"}`, 415, CodeUnsupportedMedia},
		"no content type":                      {"", `{"hub_token":"p"}`, 415, CodeUnsupportedMedia},
		"a near miss":                          {"application/jsonx", `{"hub_token":"p"}`, 415, CodeUnsupportedMedia},
		"one byte over the limit":              {"application/json", atLimit[:len(atLimit)-2] + "x" + suffix, 413, CodePayloadTooLarge},
		"over the limit and not JSON":          {"application/json", strings.Repeat("x", maxBody+1), 413, CodePayloadTooLarge},
		"a value and then more than the limit": {"application/json", `{"hub_token":"p"}` + strings.Repeat("x", maxBody), 413, CodePayloadTooLarge},
		"the limit of text that is not JSON":   {"application/json", strings.Repeat("x", maxBody), 400, CodeInvalidRequest},
		"malformed":                            {"application/json", `{`, 400, CodeInvalidRequest},
		"empty":                                {"application/json", ``, 400, CodeInvalidRequest},
		"an array":                             {"application/json", `[]`, 400, CodeInvalidRequest},
		"a field of the wrong type":            {"application/json", `{"hub_token":5}`, 400, CodeInvalidRequest},
		"an unknown field":                     {"application/json", `{"hub_token":"p","x":1}`, 400, CodeInvalidRequest},
		"a second value":                       {"application/json", `{"hub_token":"p"} {}`, 400, CodeInvalidRequest},
	} {
		var headers [][]string
		if tc.contentType != "" {
			headers = append(headers, []string{"Content-Type", tc.contentType})
		}
		r := e.call(t, "PUT", "/test/echo", tc.body, "", headers...)
		if tc.code == "" {
			if r.Status != 200 {
				t.Errorf("%s: %d %s, want 200", name, r.Status, r.Body)
			}
			continue
		}
		failed(t, name, r, tc.status, tc.code)
	}
}
