package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

var ctx = context.Background()

// syncBuffer is a log destination that the server's goroutines may write to.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// records are the log records written so far.
func (s *syncBuffer) records(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("a log line is not JSON: %q", line)
		}
		out = append(out, rec)
	}
	return out
}

// requestRecords are the records of requests.
func (s *syncBuffer) requestRecords(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, rec := range s.records(t) {
		if rec["msg"] == "request" {
			out = append(out, rec)
		}
	}
	return out
}

// harness is a server over a real store.
type harness struct {
	*httptest.Server
	Handler http.Handler
	Http    *http.Server
	Store   *store.Store
	Logs    *syncBuffer
}

// newHarness starts the server; routes adds routes of the test to it.
func newHarness(t *testing.T, routes func(mux *http.ServeMux)) *harness {
	t.Helper()
	key, err := secrets.NewKey(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "agent-fox.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	logs := &syncBuffer{}
	version := (&toolio.App{Name: "agent-fox", Version: "1.2.3", Commit: "abc123", BuildTime: "2026-10-22T00:00:00Z"}).VersionDocument()
	srv := NewServer(Deps{Store: st, Logger: slog.New(slog.NewJSONHandler(logs, nil)), Version: version, Routes: routes})
	ts := httptest.NewUnstartedServer(srv.Handler)
	ts.Config.ErrorLog, ts.Config.ReadHeaderTimeout = srv.ErrorLog, srv.ReadHeaderTimeout
	ts.Start()
	t.Cleanup(ts.Close)
	return &harness{Server: ts, Handler: srv.Handler, Http: srv, Store: st, Logs: logs}
}

// response is what a request got.
type response struct {
	Status int
	Header http.Header
	Body   string
}

// JSON decodes the body.
func (r response) JSON(t *testing.T) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(r.Body), &v); err != nil {
		t.Fatalf("the body is not a JSON object: %q", r.Body)
	}
	return v
}

// errorCode is the code of the error envelope, and fails the test unless the body is one.
func (r response) errorCode(t *testing.T) string {
	t.Helper()
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	body := r.JSON(t)
	if len(body) != 1 {
		t.Errorf("body = %s, want only the error", r.Body)
	}
	detail, _ := body["error"].(map[string]any)
	if _, has := detail["error_type"]; has {
		t.Errorf("the body carries error_type: %s", r.Body)
	}
	code, _ := detail["code"].(string)
	if msg, _ := detail["message"].(string); code == "" || msg == "" {
		t.Errorf("body = %s, want an error with a code and a message", r.Body)
	}
	return code
}

// do makes a request that does not follow redirects.
func (h *harness) do(t *testing.T, method, path string, body io.Reader, headers ...string) response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, h.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response{resp.StatusCode, resp.Header, string(data)}
}

func (h *harness) get(t *testing.T, path string, headers ...string) response {
	t.Helper()
	return h.do(t, http.MethodGet, path, nil, headers...)
}

// TestTheProbesAnswerWithoutAToken_TS_11_47 verifies TS-11-47:
// /healthz, /readyz and /version answer 200 JSON without a token.
// Verifies: 11-REQ-7.1
func TestTheProbesAnswerWithoutAToken_TS_11_47(t *testing.T) {
	h := newHarness(t, nil)
	for path, want := range map[string]map[string]any{
		"/healthz": {"status": "ok"},
		"/readyz":  {"status": "ready"},
	} {
		r := h.get(t, path)
		if r.Status != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			t.Errorf("GET %s: %d %q, want 200 application/json", path, r.Status, r.Header.Get("Content-Type"))
		}
		if got := r.JSON(t); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("GET %s = %s, want %v", path, r.Body, want)
		}
	}

	r := h.get(t, "/version")
	if r.Status != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		t.Errorf("GET /version: %d %q, want 200 application/json", r.Status, r.Header.Get("Content-Type"))
	}
	doc := r.JSON(t)
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"build_time", "commit", "go_version", "name", "version"}; !slices.Equal(keys, want) {
		t.Errorf("/version holds the fields %v, want exactly %v", keys, want)
	}
	if doc["name"] != "agent-fox" || doc["version"] != "1.2.3" || doc["commit"] != "abc123" || doc["go_version"] == "" {
		t.Errorf("/version = %s, want the version document of the build", r.Body)
	}
}

// TestReadyzAnswers503WhenTheDatabaseIsGone_TS_11_48 verifies TS-11-48:
// /readyz answers 503 not_ready when the database ping fails.
// Verifies: 11-REQ-7.2
func TestReadyzAnswers503WhenTheDatabaseIsGone_TS_11_48(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.Store.Close(); err != nil {
		t.Fatal(err)
	}
	r := h.get(t, "/readyz")
	if r.Status != 503 || r.errorCode(t) != "not_ready" {
		t.Errorf("GET /readyz = %d %s, want 503 not_ready", r.Status, r.Body)
	}
	if r := h.get(t, "/healthz"); r.Status != 200 {
		t.Errorf("GET /healthz = %d, want 200 while the database is gone", r.Status)
	}
}

// TestAPanicAnswers500AndTheProcessKeepsServing_TS_11_51 verifies TS-11-51:
// A handler panic answers 500 internal, logs the stack at ERROR and the process
// keeps serving.
// Verifies: 11-REQ-7.5
func TestAPanicAnswers500AndTheProcessKeepsServing_TS_11_51(t *testing.T) {
	h := newHarness(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("the-panic-message-7f3a") })
	})
	r := h.get(t, "/boom")
	if r.Status != 500 || r.errorCode(t) != "internal" {
		t.Errorf("GET /boom = %d %s, want 500 internal", r.Status, r.Body)
	}
	if strings.Contains(r.Body, "the-panic-message-7f3a") {
		t.Errorf("the panic message is in the response: %s", r.Body)
	}

	var logged bool
	for _, rec := range h.Logs.records(t) {
		if rec["level"] == "ERROR" {
			stack, _ := rec["stack"].(string)
			logged = logged || strings.Contains(stack, "goroutine")
		}
	}
	if !logged {
		t.Errorf("no ERROR record holds a stack trace:\n%s", h.Logs.String())
	}
	if reqs := h.Logs.requestRecords(t); len(reqs) != 1 || reqs[0]["status"] != float64(500) {
		t.Errorf("request records = %v, want one with status 500", reqs)
	}
	if r := h.get(t, "/healthz"); r.Status != 200 {
		t.Errorf("GET /healthz after the panic = %d, want 200", r.Status)
	}
}

// TestAPanicAfterTheResponseStartedIsRecoveredToo checks that a handler that has
// begun its answer and then panics ends that one request, not the process.
// Verifies: 11-REQ-7.5
func TestAPanicAfterTheResponseStartedIsRecoveredToo(t *testing.T) {
	h := newHarness(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /late", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
			io.WriteString(w, "partial")
			panic("late")
		})
	})
	if r := h.get(t, "/late"); r.Status != 202 {
		t.Errorf("GET /late = %d, want the status that was sent", r.Status)
	}
	if r := h.get(t, "/healthz"); r.Status != 200 {
		t.Errorf("GET /healthz afterwards = %d, want 200", r.Status)
	}
	var errors int
	for _, rec := range h.Logs.records(t) {
		if rec["level"] == "ERROR" {
			errors++
		}
	}
	if errors != 1 {
		t.Errorf("%d ERROR records, want the one of the panic:\n%s", errors, h.Logs.String())
	}
}

// TestTheServerSetsAReadHeaderTimeout_TS_11_52 verifies TS-11-52:
// The http.Server has a non-zero ReadHeaderTimeout.
// Verifies: 11-REQ-7.6
func TestTheServerSetsAReadHeaderTimeout_TS_11_52(t *testing.T) {
	h := newHarness(t, nil)
	if h.Http.ReadHeaderTimeout <= 0 {
		t.Errorf("ReadHeaderTimeout = %v, want it greater than zero", h.Http.ReadHeaderTimeout)
	}
}

// TestWhatNetHTTPReportsGoesToTheLogAsRecords checks that the server's own
// complaints are records of the log and not plain lines on stderr.
// Verifies: 11-REQ-7.7
func TestWhatNetHTTPReportsGoesToTheLogAsRecords(t *testing.T) {
	h := newHarness(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /twice", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.WriteHeader(http.StatusTeapot) // net/http reports this one
		})
	})
	h.get(t, "/twice")
	var reported bool
	for _, rec := range h.Logs.records(t) {
		if msg, _ := rec["msg"].(string); strings.Contains(msg, "superfluous") && rec["level"] == "WARN" {
			reported = true
		}
	}
	if !reported {
		t.Errorf("the complaint of net/http is not a WARN record:\n%s", h.Logs.String())
	}
}

// TestEveryCodeHasItsStatus checks the table of the error envelope.
// Verifies: 11-REQ-7.3
func TestEveryCodeHasItsStatus(t *testing.T) {
	want := map[Code]int{
		"invalid_request": 400, "unauthenticated": 401, "not_found": 404, "method_not_allowed": 405,
		"payload_too_large": 413, "unsupported_media_type": 415, "invalid_hub_token": 422,
		"internal": 500, "hub_unavailable": 502, "not_ready": 503,
	}
	for code, status := range want {
		if got := code.Status(); got != status {
			t.Errorf("%s has status %d, want %d", code, got, status)
		}
	}
	for _, code := range []Code{CodeInvalidRequest, CodeUnauthenticated, CodeNotFound, CodeMethodNotAllowed, CodePayloadTooLarge,
		CodeUnsupportedMedia, CodeInvalidHubToken, CodeInternal, CodeHubUnavailable, CodeNotReady} {
		if _, ok := want[code]; !ok {
			t.Errorf("the constant %s is not one of the codes of the table", code)
		}
	}
	if got := Code("made_up").Status(); got != 500 {
		t.Errorf("an unknown code has status %d, want 500", got)
	}
}

// TestErrorsAreEnvelopesIncludingTheOnesTheMuxWouldWriteInPlainText checks that
// an unknown route, a wrong method and a path that is not canonical answer in the
// envelope, and that no error carries error_type.
// Verifies: 11-REQ-7.3
func TestErrorsAreEnvelopesIncludingTheOnesTheMuxWouldWriteInPlainText(t *testing.T) {
	h := newHarness(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /err/{code}", func(w http.ResponseWriter, r *http.Request) {
			writeError(w, Code(r.PathValue("code")), "a message")
		})
	})
	for _, code := range []Code{CodeInvalidRequest, CodeUnauthenticated, CodeNotFound, CodeMethodNotAllowed, CodePayloadTooLarge,
		CodeUnsupportedMedia, CodeInvalidHubToken, CodeInternal, CodeHubUnavailable, CodeNotReady} {
		r := h.get(t, "/err/"+string(code))
		if r.Status != code.Status() || r.errorCode(t) != string(code) {
			t.Errorf("writeError(%s) = %d %s, want %d and the code", code, r.Status, r.Body, code.Status())
		}
	}

	for name, tc := range map[string]struct {
		method, path string
		status       int
		code         string
	}{
		"an unknown route":               {"GET", "/nowhere", 404, "not_found"},
		"an unknown route under /api/v1": {"GET", "/api/v1/nowhere", 404, "not_found"},
		"the root":                       {"GET", "/", 404, "not_found"},
		"a wrong method on /healthz":     {"POST", "/healthz", 405, "method_not_allowed"},
		"a wrong method on /version":     {"DELETE", "/version", 405, "method_not_allowed"},
		"a trailing slash":               {"GET", "/healthz/", 404, "not_found"},
		"a path that is not canonical":   {"GET", "//healthz", 404, "not_found"},
		"a path with a dot-dot segment":  {"GET", "/x/../healthz", 404, "not_found"},
	} {
		r := h.do(t, tc.method, tc.path, nil)
		if r.Status != tc.status || r.errorCode(t) != tc.code {
			t.Errorf("%s: %s %s = %d %s, want %d %s", name, tc.method, tc.path, r.Status, r.Body, tc.status, tc.code)
		}
	}
	if allow := h.do(t, "POST", "/healthz", nil).Header.Get("Allow"); !strings.Contains(allow, "GET") {
		t.Errorf("Allow = %q, want it to list GET", allow)
	}
}

// echoBody is what the body helper is tried on.
type echoBody struct {
	HubToken    string  `json:"hub_token"`
	LabelPrefix *string `json:"label_prefix"`
}

// TestTheBodyHelperRefusesWhatItShouldBeforeAnyWrite checks the shared reading of
// request bodies: the media type, the size, the syntax, unknown fields and
// trailing data.
// Verifies: 11-REQ-7.4
func TestTheBodyHelperRefusesWhatItShouldBeforeAnyWrite(t *testing.T) {
	h := newHarness(t, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /echo", func(w http.ResponseWriter, r *http.Request) {
			var body echoBody
			if !decodeBody(w, r, &body) {
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"hub_token": body.HubToken})
		})
	})
	put := func(contentType, body string) response {
		headers := []string{}
		if contentType != "" {
			headers = []string{"Content-Type", contentType}
		}
		return h.do(t, "PUT", "/echo", strings.NewReader(body), headers...)
	}

	// exactly 64 KiB is allowed, one byte more is not.
	const prefix, suffix = `{"hub_token":"`, `"}`
	atLimit := prefix + strings.Repeat("x", 64<<10-len(prefix)-len(suffix)) + suffix
	if len(atLimit) != 64<<10 {
		t.Fatalf("the body at the limit is %d bytes", len(atLimit))
	}

	for name, tc := range map[string]struct {
		contentType, body string
		status            int
		code              string
	}{
		"valid":                       {"application/json", `{"hub_token":"p"}`, 200, ""},
		"valid with a charset":        {"application/json; charset=utf-8", `{"hub_token":"p"}`, 200, ""},
		"valid with another case":     {"Application/JSON;charset=UTF-8", `{"hub_token":"p"}`, 200, ""},
		"exactly 64 KiB":              {"application/json", atLimit, 200, ""},
		"text/plain":                  {"text/plain", `{"hub_token":"p"}`, 415, "unsupported_media_type"},
		"no content type":             {"", `{"hub_token":"p"}`, 415, "unsupported_media_type"},
		"a near miss":                 {"application/jsonx", `{"hub_token":"p"}`, 415, "unsupported_media_type"},
		"not a media type":            {"json", `{"hub_token":"p"}`, 415, "unsupported_media_type"},
		"over 64 KiB":                 {"application/json", atLimit[:len(atLimit)-2] + "x" + suffix, 413, "payload_too_large"},
		"far over 64 KiB":             {"application/json", strings.Repeat(" ", 1<<20) + `{}`, 413, "payload_too_large"},
		"malformed JSON":              {"application/json", `{`, 400, "invalid_request"},
		"an empty body":               {"application/json", ``, 400, "invalid_request"},
		"an array":                    {"application/json", `[]`, 400, "invalid_request"},
		"a string for a string":       {"application/json", `{"hub_token":5}`, 400, "invalid_request"},
		"an unknown field":            {"application/json", `{"hub_token":"p","x":1}`, 400, "invalid_request"},
		"trailing data":               {"application/json", `{"hub_token":"p"} {}`, 400, "invalid_request"},
		"trailing garbage":            {"application/json", `{"hub_token":"p"} x`, 400, "invalid_request"},
		"trailing whitespace is fine": {"application/json", "{\"hub_token\":\"p\"}\n\n", 200, ""},
	} {
		r := put(tc.contentType, tc.body)
		if r.Status != tc.status {
			t.Errorf("%s: status %d (%s), want %d", name, r.Status, r.Body, tc.status)
			continue
		}
		if tc.code != "" && r.errorCode(t) != tc.code {
			t.Errorf("%s: body %s, want the code %s", name, r.Body, tc.code)
		}
	}
}

// TestEachRequestIsOneRecordWithTheCallerWhenKnown checks the access log.
// Verifies: 11-REQ-7.7
func TestEachRequestIsOneRecordWithTheCallerWhenKnown(t *testing.T) {
	h := newHarness(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /who", func(w http.ResponseWriter, r *http.Request) {
			setCaller(r, "usr_1")
			w.WriteHeader(http.StatusNoContent)
		})
	})
	h.get(t, "/healthz")
	h.get(t, "/who?secret=query-7c1")
	h.get(t, "/nowhere")
	h.do(t, "POST", "/healthz", nil)

	reqs := h.Logs.requestRecords(t)
	if len(reqs) != 4 {
		t.Fatalf("%d request records, want one for each of the 4 requests:\n%s", len(reqs), h.Logs.String())
	}
	for i, want := range []struct {
		method, path string
		status       float64
		user         string
	}{
		{"GET", "/healthz", 200, ""}, {"GET", "/who", 204, "usr_1"}, {"GET", "/nowhere", 404, ""}, {"POST", "/healthz", 405, ""},
	} {
		rec := reqs[i]
		if rec["method"] != want.method || rec["path"] != want.path || rec["status"] != want.status {
			t.Errorf("record %d = %v, want %s %s %v", i, rec, want.method, want.path, want.status)
		}
		if _, ok := rec["duration"]; !ok {
			t.Errorf("record %d has no duration: %v", i, rec)
		}
		if user, has := rec["user"]; want.user == "" && has || want.user != "" && user != want.user {
			t.Errorf("record %d: user = %v (present %v), want %q", i, user, has, want.user)
		}
		for _, field := range []string{"time", "level", "msg"} {
			if _, ok := rec[field]; !ok {
				t.Errorf("record %d has no %s: %v", i, field, rec)
			}
		}
	}
	if strings.Contains(h.Logs.String(), "query-7c1") {
		t.Error("the query string of a request is in the log")
	}
}

// TestNoCredentialOrBodyIsEverLogged checks that the Authorization header and the
// bodies of requests and responses stay out of the log.
// Verifies: 11-REQ-7.8
func TestNoCredentialOrBodyIsEverLogged(t *testing.T) {
	h := newHarness(t, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT /echo", func(w http.ResponseWriter, r *http.Request) {
			var body echoBody
			if decodeBody(w, r, &body) {
				io.WriteString(w, "response-body-5d2")
			}
		})
		mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	})
	auth := []string{"Authorization", "Bearer SECRET-TOKEN-91b", "Content-Type", "application/json"}
	h.do(t, "PUT", "/echo", strings.NewReader(`{"hub_token":"SECRET-PAT-44c"}`), auth...)
	h.do(t, "PUT", "/echo", strings.NewReader(`{"hub_token":"SECRET-PAT-44c","x":1}`), auth...)
	h.do(t, "PUT", "/nowhere", strings.NewReader(`SECRET-BODY-3e9`), auth...)
	h.get(t, "/boom", auth...)
	h.get(t, "/healthz", auth...)
	for _, needle := range []string{"SECRET", "Bearer", "response-body-5d2", "hub_token"} {
		if strings.Contains(h.Logs.String(), needle) {
			t.Errorf("the log holds %q:\n%s", needle, h.Logs.String())
		}
	}
}
