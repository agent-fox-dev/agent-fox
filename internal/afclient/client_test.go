package afclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// seen is a request a scripted service saw.
type seen struct {
	Method, Path string
	Header       http.Header
	Body         string
}

// scripted starts a service that records its requests and answers each with status and body,
// and returns a client for it that sends the credential tok.
func scripted(t *testing.T, status int, body string) (*Client, *[]seen) {
	t.Helper()
	var mu sync.Mutex
	var requests []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, seen{r.Method, r.URL.EscapedPath(), r.Header.Clone(), string(data)})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	server, err := ParseServer(srv.URL, true, "")
	if err != nil {
		t.Fatal(err)
	}
	return New(server, "tok", "af/9.9.9", nil), &requests
}

// asError runs a GET of path against a service that answers status and body, and returns the
// error of the request.
func asError(t *testing.T, method, path string, status int, body string) *Error {
	t.Helper()
	c, _ := scripted(t, status, body)
	_, err := c.Do(context.Background(), method, path, nil, nil)
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("%s %s answered %d: err = %v, want an *Error", method, path, status, err)
	}
	return e
}

// TestEveryRequestCarriesTheBearerTheUserAgentAndTheAcceptHeaderAndBodiesAreJSON_TS_16_38
// verifies TS-16-38 for the requests the client can make so far: a PUT with a body, a POST
// with a body and a GET. The events request is checked with the follower.
// Verifies: 16-REQ-6.2
func TestEveryRequestCarriesTheBearerTheUserAgentAndTheAcceptHeaderAndBodiesAreJSON_TS_16_38(t *testing.T) {
	c, requests := scripted(t, 200, `{}`)
	for _, call := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, "/workspaces/widgets", map[string]string{"hub_token": "pat"}},
		{http.MethodPost, "/workspaces/widgets/fix", map[string]any{"issue": 42}},
		{http.MethodGet, "/jobs/j", nil},
	} {
		if _, err := c.Do(context.Background(), call.method, call.path, nil, call.body); err != nil {
			t.Fatalf("%s %s: %v", call.method, call.path, err)
		}
	}
	got := *requests
	if len(got) != 3 {
		t.Fatalf("%d requests, want 3", len(got))
	}
	for i, r := range got {
		if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("User-Agent") != "af/9.9.9" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("request %d (%s %s) headers = %v, want the bearer, af/9.9.9 and application/json", i, r.Method, r.Path, r.Header)
		}
		hasBody := r.Body != ""
		if hasBody != (r.Header.Get("Content-Type") == "application/json") {
			t.Errorf("request %d (%s %s): body %q with Content-Type %q, want the type exactly when there is a body", i, r.Method, r.Path, r.Body, r.Header.Get("Content-Type"))
		}
	}
	if got[0].Body != `{"hub_token":"pat"}` || got[1].Body != `{"issue":42}` || got[2].Body != "" {
		t.Errorf("bodies = %q, %q, %q", got[0].Body, got[1].Body, got[2].Body)
	}
}

// TestDecodeReadsTheDocumentAndRefusesWhatIsNotTheJSONExpected_TS_16_41 verifies the last row
// of the table of TS-16-41 for the library: a body that is not the JSON that was expected is a
// service failure that is not retryable.
// Verifies: 16-REQ-6.5
func TestDecodeReadsTheDocumentAndRefusesWhatIsNotTheJSONExpected_TS_16_41(t *testing.T) {
	type page struct {
		Jobs []map[string]any `json:"jobs"`
	}
	c, _ := scripted(t, 200, `{"jobs":[{"id":"j"}]}`)
	resp, err := c.Do(context.Background(), http.MethodGet, "/jobs", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var p page
	if err := resp.Decode(&p); err != nil || len(p.Jobs) != 1 || p.Jobs[0]["id"] != "j" {
		t.Errorf("Decode = %v, %+v, want the page", err, p)
	}
	for _, body := range []string{`not json`, `[1,2]`, ``, `{"jobs":"x"}`} {
		c, _ := scripted(t, 200, body)
		resp, err := c.Do(context.Background(), http.MethodGet, "/jobs", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		var e *Error
		if err := resp.Decode(&page{}); !errors.As(err, &e) || e.Category != CategoryService || e.Retryable {
			t.Errorf("Decode of %q = %v, want a service error that is not retryable", body, err)
		}
	}
}

// TestAnErrorMessageIsTheServicesMessageAndCodeAndAFourOhFourNamesWhatWasAskedFor_TS_16_42
// verifies TS-16-42 for the library.
// Verifies: 16-REQ-6.6
func TestAnErrorMessageIsTheServicesMessageAndCodeAndAFourOhFourNamesWhatWasAskedFor_TS_16_42(t *testing.T) {
	if e := asError(t, "GET", "/workspaces", 400, `{"error":{"code":"invalid_request","message":"bad"}}`); e.Message != "bad (invalid_request)" || e.Code != "invalid_request" {
		t.Errorf("message %q code %q, want %q", e.Message, e.Code, "bad (invalid_request)")
	}
	for name, body := range map[string]string{"empty": ``, "not JSON": `oops`, "no error object": `{"x":1}`, "empty object": `{"error":{}}`} {
		if e := asError(t, "GET", "/workspaces", 500, body); e.Message != "the service answered 500" {
			t.Errorf("%s: message %q, want %q", name, e.Message, "the service answered 500")
		}
	}
	if e := asError(t, "GET", "/workspaces", 400, `{"error":{"message":"just text"}}`); e.Message != "just text" {
		t.Errorf("a message without a code: %q, want it as it is", e.Message)
	}

	notFound := `{"error":{"code":"not_found","message":"job not found"}}`
	job := asError(t, "GET", "/jobs/job_abc/events", 404, notFound)
	for _, want := range []string{"job_abc", "does not exist", "hub", "job not found (not_found)"} {
		if !strings.Contains(job.Message, want) {
			t.Errorf("404 on a job: %q does not contain %q", job.Message, want)
		}
	}
	ws := asError(t, "GET", "/workspaces/a%20b%2Fc/jobs", 404, notFound)
	for _, want := range []string{"workspace", `a b/c`, "does not exist", "hub"} {
		if !strings.Contains(ws.Message, want) {
			t.Errorf("404 on a workspace: %q does not contain %q", ws.Message, want)
		}
	}
}

// TestAFailureCarriesTheFixHintOfItsAnswer_TS_16_43 verifies TS-16-43 for the library: the
// real slug in the hint of a workspace route, and the placeholder when the route names a job.
// Verifies: 16-REQ-6.7
func TestAFailureCarriesTheFixHintOfItsAnswer_TS_16_43(t *testing.T) {
	subscribe := func(slug string) *Hint {
		return &Hint{Suggest: "af workspace subscribe " + slug + " --hub-token-file FILE"}
	}
	for _, c := range []struct {
		name         string
		method, path string
		status       int
		code         string
		want         *Hint
	}{
		{"401", "GET", "/workspaces", 401, "unauthenticated", &Hint{Suggest: "af login --token-file FILE"}},
		{"404 on a workspace route", "GET", "/workspaces/w", 404, "not_found", subscribe("w")},
		{"404 on a workspace job list", "GET", "/workspaces/w/jobs", 404, "not_found", subscribe("w")},
		{"404 on a queue route", "POST", "/workspaces/a%20b/fix", 404, "not_found", subscribe("a b")},
		{"404 on a job route", "GET", "/jobs/j", 404, "not_found", nil},
		{"404 on an unknown route", "GET", "/nothing", 404, "not_found", nil},
		{"409 not_subscribed on a workspace route", "POST", "/workspaces/w/fix", 409, "not_subscribed", subscribe("w")},
		{"409 token_unreadable on a workspace route", "POST", "/workspaces/w/triage", 409, "token_unreadable", subscribe("w")},
		{"409 not_subscribed on a job route", "POST", "/jobs/j/cancel", 409, "not_subscribed", subscribe("<slug>")},
		{"409 token_unreadable on a job route", "POST", "/jobs/j/retry", 409, "token_unreadable", subscribe("<slug>")},
		{"409 invalid_state", "POST", "/jobs/j/cancel", 409, "invalid_state", nil},
		{"409 duplicate_job", "POST", "/workspaces/w/fix", 409, "duplicate_job", nil},
		{"422", "PUT", "/workspaces/w", 422, "invalid_hub_token", &Hint{Flag: "--hub-token-file"}},
		{"500", "GET", "/workspaces", 500, "internal", nil},
	} {
		e := asError(t, c.method, c.path, c.status, `{"error":{"code":"`+c.code+`","message":"m"}}`)
		if !reflect.DeepEqual(e.Hint, c.want) {
			t.Errorf("%s: hint = %+v, want %+v", c.name, e.Hint, c.want)
		}
	}
}

// TestTheAnswerTableGivesEachRowItsCategoryAndRetryableFlag_TS_16_41 verifies the table of
// TS-16-41 for the library.
// Verifies: 16-REQ-6.5
func TestTheAnswerTableGivesEachRowItsCategoryAndRetryableFlag_TS_16_41(t *testing.T) {
	for _, c := range []struct {
		status    int
		category  string
		retryable bool
	}{
		{400, CategoryUsage, false}, {404, CategoryUsage, false},
		{401, CategoryAuth, false}, {422, CategoryAuth, false},
		{409, CategoryConflict, false},
		{502, CategoryHub, true},
		{500, CategoryService, false},
		{503, CategoryService, true}, {504, CategoryService, true}, {507, CategoryService, true}, {599, CategoryService, true},
		{405, CategoryService, false}, {413, CategoryService, false}, {415, CategoryService, false}, {418, CategoryService, false},
		{301, CategoryService, false}, {302, CategoryService, false}, {307, CategoryService, false}, {308, CategoryService, false},
	} {
		e := asError(t, "GET", "/workspaces", c.status, ``)
		if e.Status != c.status || e.Category != c.category || e.Retryable != c.retryable {
			t.Errorf("%d: status %d, category %q, retryable %v, want %q and %v", c.status, e.Status, e.Category, e.Retryable, c.category, c.retryable)
		}
	}
}
