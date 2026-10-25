package afclient

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/afclient/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestDo_SendsTheCredentialTheUserAgentAndJSONUnderThePrefix(t *testing.T) {
	type seen struct{ method, target, auth, agent, accept, ctype, body string }
	got := make(chan seen, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		got <- seen{r.Method, r.URL.RequestURI(), r.Header.Get("Authorization"), r.Header.Get("User-Agent"),
			r.Header.Get("Accept"), r.Header.Get("Content-Type"), string(data)}
		w.Header().Set("X-Answer", "1")
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)
	c := clientAt(t, srv.URL+"/af/", nil)
	if c.Server().URL != srv.URL+"/af" {
		t.Errorf("Server() = %+v, want %s/af", c.Server(), srv.URL)
	}

	for _, call := range []struct {
		method, path string
		query        url.Values
		body         any
		want         seen
	}{
		{"PUT", "/workspaces/widgets", nil, map[string]string{"hub_token": "pat"},
			seen{"PUT", "/af/api/v1/workspaces/widgets", "", "", "", "application/json", `{"hub_token":"pat"}`}},
		{"GET", "/jobs", url.Values{"state": {"running"}, "limit": {"5"}}, nil,
			seen{"GET", "/af/api/v1/jobs?limit=5&state=running", "", "", "", "", ""}},
	} {
		resp, err := c.Do(context.Background(), call.method, call.path, call.query, call.body)
		if err != nil {
			t.Fatalf("%s %s: %v", call.method, call.path, err)
		}
		if resp.Status != 201 || string(resp.Body) != `{"ok":true}` || resp.Header.Get("X-Answer") != "1" {
			t.Errorf("%s %s: answer %d %q %v", call.method, call.path, resp.Status, resp.Body, resp.Header)
		}
		call.want.auth, call.want.agent, call.want.accept = "Bearer "+token, "af/test", "application/json"
		if saw := <-got; saw != call.want {
			t.Errorf("%s %s: the service saw %+v, want %+v", call.method, call.path, saw, call.want)
		}
	}
}

func TestDecode_RefusesWhatIsNotTheJSONExpected(t *testing.T) {
	type page struct {
		Jobs []map[string]any `json:"jobs"`
	}
	resp := &Response{Status: 200, Body: []byte(`{"jobs":[{"id":"j"}]}`)}
	var p page
	if err := resp.Decode(&p); err != nil || len(p.Jobs) != 1 || p.Jobs[0]["id"] != "j" {
		t.Errorf("Decode = %v, %+v, want the page", err, p)
	}
	for _, body := range []string{`not json`, `[1,2]`, ``, `{"jobs":"x"}`} {
		c := serve(t, answers(200, body), nil)
		resp, err := c.Do(context.Background(), "GET", "/jobs", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		e := asError(t, resp.Decode(&page{}))
		if e.Category != CategoryService || e.Retryable || e.Status != 200 || !strings.Contains(e.Error(), c.Server().URL+" (given by --server)") {
			t.Errorf("Decode of %q = %+v, want a service error that names the server and is not retryable", body, e)
		}
	}
}

func TestDo_ANon2xxAnswerIsItsErrorWithoutTheSecrets(t *testing.T) {
	const pat = "ghp_patpatpatpat"
	c := serve(t, answers(400, `{"error":{"code":"invalid_request","message":"bad `+token+` and `+pat+` and short1"}}`), nil)
	c.Redact(pat, "short1")

	_, err := c.Do(context.Background(), "GET", "/workspaces", nil, nil)

	e := asError(t, err)
	if e.Status != 400 || e.Code != "invalid_request" || e.Category != CategoryUsage {
		t.Errorf("error = %+v, want the 400 as a usage error", e)
	}
	if want := "bad *** and *** and short1 (invalid_request)"; e.Message != want {
		t.Errorf("message = %q, want %q: the credential and the registered value scrubbed, a value of under 8 bytes kept", e.Message, want)
	}
	if got := c.Scrub("x " + pat); got != "x ***" {
		t.Errorf("Scrub = %q", got)
	}
}

func TestDo_DoesNotFollowARedirect(t *testing.T) {
	var reached atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached.Add(1) }))
	t.Cleanup(target.Close)
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/v1/workspaces", http.StatusTemporaryRedirect)
	}, nil)

	_, err := c.Do(context.Background(), "GET", "/workspaces", nil, nil)

	if e := asError(t, err); e.Status != 307 || e.Category != CategoryService || e.Retryable {
		t.Errorf("error = %+v, want the 307 as a service error that is not retryable", e)
	}
	if n := reached.Load(); n != 0 {
		t.Errorf("the target of the redirect saw %d requests", n)
	}
}

func TestDo_IsCutAtTheRequestBound(t *testing.T) {
	arrived := make(chan struct{}, 1)
	clk := fakeclock.New(epoch)
	c := serve(t, func(_ http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-r.Context().Done()
	}, clk)
	done := make(chan error, 1)
	go func() {
		_, err := c.Do(context.Background(), "GET", "/workspaces", nil, nil)
		done <- err
	}()
	<-arrived
	if !clk.WaitForWaits(1, testutil.WaitTimeout) || clk.Waits()[0] != 2*time.Minute {
		t.Fatalf("the request asked for the waits %v, want a bound of two minutes", clk.Waits())
	}

	clk.Advance(RequestBound)

	e := asError(t, <-done)
	if e.Category != CategoryService || !e.Retryable || !strings.Contains(e.Message, "did not answer within 2m0s") {
		t.Errorf("error = %+v, want a retryable service error that names the bound", e)
	}
}

func TestDo_ReturnsTheErrorOfAStoppedContext(t *testing.T) {
	arrived := make(chan struct{}, 1)
	c := serve(t, func(_ http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-r.Context().Done()
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-arrived
		cancel()
	}()

	_, err := c.Do(ctx, "GET", "/workspaces", nil, nil)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want the error of the context", err)
	}
}

// endless is the body of an answer that never ends: prefix, then spaces. It counts what is read.
type endless struct {
	prefix string
	read   int
}

func (e *endless) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
		if e.read < len(e.prefix) {
			p[i] = e.prefix[e.read]
		}
		e.read++
	}
	return len(p), nil
}

func (e *endless) Close() error { return nil }

func TestDo_ReadsAtMostEightMiBOfAnAnswer(t *testing.T) {
	server, _ := ParseServer("http://127.0.0.1:1", true, "")
	for _, c := range []struct {
		name string
		body io.ReadCloser
		ok   bool
	}{
		{"a body of exactly 8 MiB", io.NopCloser(strings.NewReader(`{"a":1}` + strings.Repeat(" ", MaxBody-7))), true},
		{"a body that never ends", &endless{prefix: `{"workspaces":[`}, false},
	} {
		client := New(server, token, "af/test", roundTrip(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: c.body, Request: r}, nil
		}))

		resp, err := client.Do(context.Background(), "GET", "/workspaces", nil, nil)

		if c.ok {
			if err != nil || len(resp.Body) != MaxBody {
				t.Errorf("%s: %v, want the whole body", c.name, err)
			}
			continue
		}
		if e := asError(t, err); e.Category != CategoryService || e.Retryable || !strings.Contains(e.Message, "longer than 8 MiB") {
			t.Errorf("%s: %+v, want a service error that is not retryable", c.name, e)
		}
		if n := c.body.(*endless).read; n > MaxBody+1<<16 {
			t.Errorf("%s: %d bytes were read, want little more than 8 MiB", c.name, n)
		}
	}
}

func TestDo_AnAnswerThatNeverCameNamesTheServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + ln.Addr().String()
	_ = ln.Close()
	shortBody := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, `{"a":`)
		w.(http.Flusher).Flush()
		hangUp(w)
	}
	for _, c := range []struct {
		name, cause string
		client      *Client
	}{
		{"nothing listens", "nothing is listening there", clientAt(t, closed, nil)},
		{"the connection drops", "EOF", serve(t, hangsUp(), nil)},
		{"the body is cut short", "EOF", serve(t, shortBody, nil)},
	} {
		_, err := c.client.Do(context.Background(), "GET", "/workspaces", nil, nil)

		e := asError(t, err)
		want := "the service at " + c.client.Server().URL + " (given by --server) cannot be reached: "
		if e.Status != 0 || e.Category != CategoryService || !e.Retryable || !strings.HasPrefix(e.Message, want) || !strings.Contains(e.Message, c.cause) {
			t.Errorf("%s: %+v, want a retryable service error %q... naming %q", c.name, e, want, c.cause)
		}
	}
}

func TestDo_ARequestThatCannotBeMadeIsNotAnAnswer(t *testing.T) {
	c := clientAt(t, "http://127.0.0.1:1", nil)
	for name, call := range map[string]struct {
		method string
		body   any
	}{
		"a method that is no method": {"BAD METHOD", nil},
		"a body that is not JSON":    {"POST", map[string]any{"c": make(chan int)}},
	} {
		_, err := c.Do(context.Background(), call.method, "/workspaces", nil, call.body)
		var e *Error
		if err == nil || errors.As(err, &e) {
			t.Errorf("%s: err = %v, want a plain error", name, err)
		}
	}
}
