package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/afclient"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// startAF runs the shell of the binary in the background and returns the function that waits
// for it and gives what it wrote.
func startAF(t *testing.T, stdin string, args ...string) func() afResult {
	t.Helper()
	var out, errb bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- newApp().Main(context.Background(), append([]string{"af"}, args...), strings.NewReader(stdin), &out, &errb)
	}()
	return func() afResult {
		t.Helper()
		select {
		case code := <-done:
			res := afResult{code: code, stdout: out.String(), stderr: errb.String()}
			if out.Len() > 0 {
				res.env = oneDocument(t, res.stdout)
			}
			return res
		case <-time.After(30 * time.Second):
			t.Fatal("the invocation did not end")
			return afResult{}
		}
	}
}

// useClock makes the client commands take the timers of their requests from clock for the test.
func useClock(t *testing.T, clock afclient.Clock) {
	t.Helper()
	old := clientClock
	clientClock = clock
	t.Cleanup(func() { clientClock = old })
}

// useVersion makes the binary's build the version for the test.
func useVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

// answering is a handler that answers every request with status and body.
func answering(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// ready starts a fake service with handler and gives the invocations of the test a home with a
// credential and AF_URL naming the service.
func ready(t *testing.T, handler http.HandlerFunc) *fakeService {
	t.Helper()
	svc := newFakeService(t, handler)
	clientHome(t, str("token = \"T\"\n"))
	t.Setenv("AF_URL", svc.URL)
	return svc
}

// hint is the fix_hint of the error of the envelope.
func (r afResult) hint() map[string]any {
	h, _ := field(r.env, "error", "fix_hint").(map[string]any)
	return h
}

// retryable is the retryable flag of the error of the envelope.
func (r afResult) retryable() bool {
	b, _ := field(r.env, "error", "retryable").(bool)
	return b
}

// TestAServiceThatRedirectsGetsNoCredentialToTheTargetOfTheRedirect_TS_16_28 verifies
// TS-16-28: the redirect is the answer, and the target of its Location sees no request.
// Verifies: 16-REQ-4.7
func TestAServiceThatRedirectsGetsNoCredentialToTheTargetOfTheRedirect_TS_16_28(t *testing.T) {
	target := newFakeService(t, nil)
	svc := ready(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/v1/workspaces", http.StatusTemporaryRedirect)
	})
	res := afRun(t, "", "workspace", "list", "--server", svc.URL)
	if res.code != 1 || res.category() != "service" || res.retryable() {
		t.Errorf("exit %d, category %q, retryable %v, want 1 and service, not retryable", res.code, res.category(), res.retryable())
	}
	if n := len(target.Requests()); n != 0 {
		t.Errorf("the target of the redirect saw %d requests: %+v", n, target.Requests())
	}
	for _, r := range svc.Requests() {
		if r.Header.Get("Authorization") != "Bearer T" {
			t.Errorf("a request to the server carried %q", r.Header.Get("Authorization"))
		}
	}
}

// TestEveryRequestOfACommandCarriesTheBearerTheUserAgentOfTheBuildAndTheAcceptHeader_TS_16_38
// verifies TS-16-38 for the command: the User-Agent is af/ and the version that App.Identity
// reports.
// Verifies: 16-REQ-6.2
func TestEveryRequestOfACommandCarriesTheBearerTheUserAgentOfTheBuildAndTheAcceptHeader_TS_16_38(t *testing.T) {
	useVersion(t, "9.9.9-test")
	svc := ready(t, nil)
	if res := afRun(t, "", "workspace", "list"); res.code != 0 {
		t.Fatalf("exit %d, %s", res.code, res.stdout)
	}
	reqs := svc.Requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want 1", len(reqs))
	}
	h := reqs[0].Header
	if h.Get("Authorization") != "Bearer T" || h.Get("User-Agent") != "af/9.9.9-test" || h.Get("Accept") != "application/json" {
		t.Errorf("headers = %v, want Bearer T, af/9.9.9-test and application/json", h)
	}
}

// TestARequestIsCutAtTwoMinutesOrAtTheTimeoutAndABodyAtEightMiB_TS_16_39 verifies TS-16-39:
// the bound of two minutes runs on the injected clock, --timeout is the shell's, no deadline
// is set without it, and no more than 8 MiB of a body is read.
// Verifies: 16-REQ-6.3
func TestARequestIsCutAtTwoMinutesOrAtTheTimeoutAndABodyAtEightMiB_TS_16_39(t *testing.T) {
	start := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

	t.Run("two minutes without --timeout", func(t *testing.T) {
		release := make(chan struct{})
		arrived := make(chan struct{}, 1)
		svc := ready(t, func(w http.ResponseWriter, r *http.Request) {
			arrived <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
			}
		})
		t.Cleanup(func() { close(release) })
		clk := fakeclock.New(start)
		useClock(t, clk)
		wait := startAF(t, "", "workspace", "list")
		if !clk.WaitForWaits(1, 10*time.Second) {
			t.Fatal("the request asked for no bound")
		}
		if w := clk.Waits()[0]; w != 2*time.Minute {
			t.Errorf("the bound is %s, want 2m0s", w)
		}
		select { // the bound is moved once the request has reached the service
		case <-arrived:
		case <-time.After(10 * time.Second):
			t.Fatal("the request did not reach the service")
		}
		clk.Advance(2 * time.Minute)
		res := wait()
		if res.code != 1 || res.category() != "service" || !res.retryable() {
			t.Errorf("exit %d, category %q, retryable %v, message %q, want 1, service, retryable", res.code, res.category(), res.retryable(), res.message())
		}
		if len(svc.Requests()) != 1 {
			t.Errorf("%d requests, want 1", len(svc.Requests()))
		}
	})

	t.Run("--timeout ends the command with the shell's failure", func(t *testing.T) {
		release := make(chan struct{})
		ready(t, func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		})
		t.Cleanup(func() { close(release) })
		clk := fakeclock.New(start)
		useClock(t, clk)
		res := startAF(t, "", "workspace", "list", "--timeout", "300ms")()
		if res.code != 1 || res.category() != "timeout" {
			t.Errorf("exit %d, category %q, message %q, want 1 and timeout", res.code, res.category(), res.message())
		}
	})

	t.Run("no deadline without --timeout", func(t *testing.T) {
		clientHome(t, str("token = \"T\"\n"))
		t.Setenv("AF_URL", "http://127.0.0.1:1")
		var deadline bool
		useTransport(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
			_, deadline = r.Context().Deadline()
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"workspaces":[]}`)), Request: r}, nil
		}))
		if res := afRun(t, "", "workspace", "list"); res.code != 0 {
			t.Fatalf("exit %d, %s", res.code, res.stdout)
		}
		if deadline {
			t.Error("the request has a deadline of its own, but no --timeout was given")
		}
	})

	t.Run("a body over 8 MiB", func(t *testing.T) {
		clientHome(t, str("token = \"T\"\n"))
		t.Setenv("AF_URL", "http://127.0.0.1:1")
		body := &endless{prefix: `{"workspaces":[`}
		useTransport(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: body, Request: r}, nil
		}))
		res := afRun(t, "", "workspace", "list")
		if res.code != 1 || res.category() != "service" || res.retryable() {
			t.Errorf("exit %d, category %q, retryable %v, want 1 and service, not retryable", res.code, res.category(), res.retryable())
		}
		if body.read > 8<<20+1 {
			t.Errorf("%d bytes of the body were read, want at most 8 MiB (+1 to see that it is over)", body.read)
		}
	})
}

// endless is a response body that never ends: prefix, then spaces.
type endless struct {
	prefix string
	read   int
}

func (e *endless) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if e.read < len(e.prefix) {
			p[n] = e.prefix[e.read]
		} else {
			p[n] = ' '
		}
		n++
		e.read++
	}
	return n, nil
}

func (e *endless) Close() error { return nil }

// TestAnAnswerThatRedirectsIsNotFollowedAndIsAServiceFailureThatIsNotRetryable_TS_16_40
// verifies TS-16-40.
// Verifies: 16-REQ-6.4
func TestAnAnswerThatRedirectsIsNotFollowedAndIsAServiceFailureThatIsNotRetryable_TS_16_40(t *testing.T) {
	for _, status := range []int{301, 302, 307, 308} {
		target := newFakeService(t, nil)
		ready(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", target.URL+"/elsewhere")
			w.WriteHeader(status)
		})
		res := afRun(t, "", "workspace", "list")
		if res.code != 1 || field(res.env, "status") != "failed" || res.category() != "service" || res.retryable() {
			t.Errorf("%d: exit %d, status %v, category %q, retryable %v, want failed, service, not retryable", status, res.code, field(res.env, "status"), res.category(), res.retryable())
		}
		if n := len(target.Requests()); n != 0 {
			t.Errorf("%d: the target saw %d requests", status, n)
		}
	}
}

// TestEachRowOfTheAnswerTableGivesItsStatusExitCodeCategoryAndRetryableFlag_TS_16_41
// verifies TS-16-41.
// Verifies: 16-REQ-6.5
func TestEachRowOfTheAnswerTableGivesItsStatusExitCodeCategoryAndRetryableFlag_TS_16_41(t *testing.T) {
	for _, c := range []struct {
		status    int
		envStatus string
		exit      int
		category  string
		retryable bool
	}{
		{400, "usage", 2, "usage", false}, {404, "usage", 2, "usage", false},
		{401, "failed", 1, "auth", false}, {422, "failed", 1, "auth", false},
		{409, "failed", 1, "conflict", false},
		{502, "failed", 1, "hub", true},
		{500, "failed", 1, "service", false},
		{503, "failed", 1, "service", true}, {504, "failed", 1, "service", true}, {507, "failed", 1, "service", true},
		{405, "failed", 1, "service", false}, {413, "failed", 1, "service", false}, {415, "failed", 1, "service", false}, {418, "failed", 1, "service", false},
	} {
		ready(t, answering(c.status, `{"error":{"code":"x","message":"m"}}`))
		res := afRun(t, "", "workspace", "list")
		if res.code != c.exit || field(res.env, "status") != c.envStatus || res.category() != c.category || res.retryable() != c.retryable {
			t.Errorf("%d: exit %d, status %v, category %q, retryable %v, want %d, %s, %s, %v",
				c.status, res.code, field(res.env, "status"), res.category(), res.retryable(), c.exit, c.envStatus, c.category, c.retryable)
		}
	}

	t.Run("a dropped connection", func(t *testing.T) {
		ready(t, func(w http.ResponseWriter, r *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		})
		res := afRun(t, "", "workspace", "list")
		if res.code != 1 || res.category() != "service" || !res.retryable() {
			t.Errorf("exit %d, category %q, retryable %v, want 1, service, retryable", res.code, res.category(), res.retryable())
		}
	})
	t.Run("a 200 that is not JSON", func(t *testing.T) {
		ready(t, answering(200, `this is not json`))
		res := afRun(t, "", "workspace", "list")
		if res.code != 1 || res.category() != "service" || res.retryable() {
			t.Errorf("exit %d, category %q, retryable %v, want 1, service, not retryable", res.code, res.category(), res.retryable())
		}
	})
	t.Run("the categories are those of the shell", func(t *testing.T) {
		for got, want := range map[string]string{
			afclient.CategoryUsage: toolio.CategoryUsage, afclient.CategoryAuth: toolio.CategoryAuth, afclient.CategoryConflict: toolio.CategoryConflict,
			afclient.CategoryHub: toolio.CategoryHub, afclient.CategoryService: toolio.CategoryService,
		} {
			if got != want {
				t.Errorf("afclient category %q, shell category %q", got, want)
			}
		}
	})
}

// TestAFailureMessageIsTheServicesMessageAndCodeAndATransportErrorNamesTheServer_TS_16_42
// verifies TS-16-42 for the command: the service's message and code, the empty body, and the
// server and its source for a transport error. The 404s are checked with the library.
// Verifies: 16-REQ-6.6
func TestAFailureMessageIsTheServicesMessageAndCodeAndATransportErrorNamesTheServer_TS_16_42(t *testing.T) {
	ready(t, answering(400, `{"error":{"code":"invalid_request","message":"bad"}}`))
	if res := afRun(t, "", "workspace", "list"); res.message() != "bad (invalid_request)" {
		t.Errorf("message %q, want %q", res.message(), "bad (invalid_request)")
	}
	ready(t, answering(500, ``))
	if res := afRun(t, "", "workspace", "list"); res.message() != "the service answered 500" {
		t.Errorf("message %q, want %q", res.message(), "the service answered 500")
	}

	dead := newFakeService(t, nil)
	dead.Close()
	clientHome(t, str("token = \"T\"\n"))
	t.Setenv("AF_URL", "")
	flagged := afRun(t, "", "workspace", "list", "--server", dead.URL)
	t.Setenv("AF_URL", dead.URL)
	fromEnv := afRun(t, "", "workspace", "list")
	for _, c := range []struct {
		name string
		res  afResult
		want string
	}{{"--server", flagged, "--server"}, {"AF_URL", fromEnv, "AF_URL"}} {
		if c.res.code != 1 || c.res.category() != "service" || !c.res.retryable() {
			t.Errorf("%s: exit %d, category %q, retryable %v, want 1, service, retryable", c.name, c.res.code, c.res.category(), c.res.retryable())
		}
		if m := c.res.message(); !strings.Contains(m, dead.URL) || !strings.Contains(m, c.want) {
			t.Errorf("%s: message %q, want it to name %s and %s", c.name, m, dead.URL, c.want)
		}
	}
}

// TestAFailureCarriesTheFixHintOfItsAnswerForACommand_TS_16_43 verifies TS-16-43 for the
// command: the hints of a 401, a 422 and an answer that has none.
// Verifies: 16-REQ-6.7
func TestAFailureCarriesTheFixHintOfItsAnswerForACommand_TS_16_43(t *testing.T) {
	ready(t, answering(401, `{"error":{"code":"unauthenticated","message":"no"}}`))
	if h := afRun(t, "", "workspace", "list").hint(); len(h) != 1 || h["suggest"] != afclient.CredentialSuggestion {
		t.Errorf("401: fix_hint = %v, want {suggest: %s}", h, afclient.CredentialSuggestion)
	}
	ready(t, answering(422, `{"error":{"code":"invalid_hub_token","message":"no"}}`))
	if h := afRun(t, "", "workspace", "list").hint(); len(h) != 1 || h["flag"] != "--hub-token-file" {
		t.Errorf("422: fix_hint = %v, want {flag: --hub-token-file}", h)
	}
	ready(t, answering(409, `{"error":{"code":"invalid_state","message":"no"}}`))
	if res := afRun(t, "", "workspace", "list"); res.env["error"].(map[string]any)["fix_hint"] != nil {
		t.Errorf("409 invalid_state: fix_hint = %v, want none", res.hint())
	}
}

// TestServiceDocumentsReachTheResultAsReceivedUnknownFieldsIncluded_TS_16_44 verifies TS-16-44
// for the command so far: the list of subscriptions with fields af does not know and nested
// values. The job and the page are checked with their commands.
// Verifies: 16-REQ-6.8
func TestServiceDocumentsReachTheResultAsReceivedUnknownFieldsIncluded_TS_16_44(t *testing.T) {
	doc := `{"workspaces":[{"slug":"widgets","future":{"a":[1,2,{"b":null}],"c":1.5e3},"unicode":"ä☃ <b>&"}],"cursor":null,"extra":[true]}`
	ready(t, answering(200, doc))
	res := afRun(t, "", "workspace", "list")
	if res.code != 0 {
		t.Fatalf("exit %d, %s", res.code, res.stdout)
	}
	var served, got any
	if err := json.Unmarshal([]byte(doc), &served); err != nil {
		t.Fatal(err)
	}
	got = res.env["result"]
	if !reflect.DeepEqual(got, served) {
		t.Errorf("result = %v, want the document as served: %v", got, served)
	}
}

// TestNoClientCommandHasADefaultTimeout_TS_16_39 verifies the last bullet of 16-REQ-6.3 for every client
// command: the help of a flag that has a default says so, and the help of --timeout says it only for the loops.
// Verifies: 16-REQ-6.3
func TestNoClientCommandHasADefaultTimeout_TS_16_39(t *testing.T) {
	timeoutLine := func(text string) string {
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(line, "--timeout") && strings.Contains(line, "no limit") {
				return line
			}
		}
		return ""
	}
	for _, path := range []string{"jobs submit", "workspace list", "workspace subscribe", "workspace get", "workspace unsubscribe",
		"jobs list", "jobs get", "jobs watch", "jobs cancel", "jobs answer", "jobs retry"} {
		code, stdout, _ := rawRun(t, "", append([]string{"help"}, strings.Fields(path)...)...)
		line := timeoutLine(stdout)
		if code != 0 || line == "" || strings.Contains(line, "(default") {
			t.Errorf("%s: exit %d, the --timeout line is %q, want one without a default", path, code, line)
		}
	}
	for _, cmd := range []string{"triage", "fix"} {
		_, stdout, _ := rawRun(t, "", "help", cmd)
		if loop := timeoutLine(stdout); !strings.Contains(loop, "(default") {
			t.Errorf("%s as a loop: the --timeout line is %q, want the default of the loop", cmd, loop)
		}
	}
}
