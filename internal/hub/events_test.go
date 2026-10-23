package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// eventsServer is an httptest server for the events route that records the requests and answers with the handler.
type eventsServer struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []*http.Request
}

func newEventsServer(t *testing.T, answer http.HandlerFunc) *eventsServer {
	t.Helper()
	s := &eventsServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.reqs = append(s.reqs, r.Clone(context.Background()))
		s.mu.Unlock()
		answer(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

// last is the last request the server received.
func (s *eventsServer) last() *http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.reqs) == 0 {
		return nil
	}
	return s.reqs[len(s.reqs)-1]
}

// count is how many requests the server received.
func (s *eventsServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reqs)
}

// openStream answers 200 text/event-stream and keeps the connection until the client goes, sending what frames sends.
func openStream(frames <-chan string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		for {
			select {
			case f, ok := <-frames:
				if !ok {
					return
				}
				_, _ = io.WriteString(w, f)
				w.(http.Flusher).Flush()
			case <-r.Context().Done():
				return
			}
		}
	}
}

// hubError answers hub's error object with the status.
func hubError(status int, errorType, message string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"error":{"code":%d,"message":%q,"error_type":%q}}`, status, message, errorType)
	}
}

// TestTheEventsCallSendsTheDocumentedRequest_TS_18_1 verifies TS-18-1.
// Verifies: 18-REQ-1.1
func TestTheEventsCallSendsTheDocumentedRequest_TS_18_1(t *testing.T) {
	srv := newEventsServer(t, openStream(make(chan string)))
	c := New(srv.URL, "9.9.9")
	const slug = "wid gets&x"
	es, err := c.Events(ctx, "pat-1", slug)
	if err != nil || es == nil {
		t.Fatalf("Events = %v, %v; want an open stream", es, err)
	}
	defer es.Close()
	r := srv.last()
	if r.Method != "GET" || r.URL.Path != "/api/v1/events" || r.URL.RawQuery != "workspace="+url.QueryEscape(slug) {
		t.Errorf("the request is %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
	}
	if r.Header.Get("Authorization") != "Bearer pat-1" || r.Header.Get("Accept") != "text/event-stream" || r.Header.Get("User-Agent") != "agent-fox/9.9.9" {
		t.Errorf("the headers are %v", r.Header)
	}

	redirected := 0
	target := newEventsServer(t, func(w http.ResponseWriter, r *http.Request) { redirected++ })
	moved := newEventsServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/v1/events", http.StatusFound)
	})
	var unavailable *UnavailableError
	if _, err := New(moved.URL, "1").Events(ctx, "pat-1", "widgets"); !errors.As(err, &unavailable) || target.count() != 0 {
		t.Errorf("a redirect: %v and %d requests to its target, want unavailable and the redirect not followed", err, target.count())
	}
}

// TestHeadersAreBoundedAndTheBodyIsNot_TS_18_2 verifies TS-18-2: the wait for the headers has the client's bound, and an
// open stream is read past it, until the context ends.
// Verifies: 18-REQ-1.2
func TestHeadersAreBoundedAndTheBodyIsNot_TS_18_2(t *testing.T) {
	if got := New("http://127.0.0.1:1", "1").bound(false); got != 10*time.Second {
		t.Errorf("the bound of the headers is %v, want 10s", got)
	}
	slow := newEventsServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	})
	c := New(slow.URL, "1")
	c.HTTP.Timeout = 100 * time.Millisecond // the 10 s, scaled
	var unavailable *UnavailableError
	start := time.Now()
	if _, err := c.Events(ctx, "pat-1", "widgets"); !errors.As(err, &unavailable) || time.Since(start) > time.Second {
		t.Errorf("headers that do not come: %v after %v, want unavailable at the bound", err, time.Since(start))
	}

	frames := make(chan string)
	fast := newEventsServer(t, openStream(frames))
	c = New(fast.URL, "1")
	c.HTTP.Timeout = 100 * time.Millisecond
	sctx, cancel := context.WithCancel(ctx)
	es, err := c.Events(sctx, "pat-1", "widgets")
	if err != nil {
		t.Fatal(err)
	}
	defer es.Close()
	time.Sleep(300 * time.Millisecond) // well past the bound
	go func() { frames <- "data: {\"id\":\"e1\"}\n\n" }()
	buf := make([]byte, 64)
	n, err := es.body.Read(buf)
	if err != nil || !strings.Contains(string(buf[:n]), "e1") {
		t.Fatalf("the frame sent after the bound: %q, %v", buf[:n], err)
	}
	cancel()
	if _, err := io.ReadAll(es.body); err == nil {
		t.Error("the stream did not end when the context did")
	}
}

// TestA401IsRefusedAndA403IsForbiddenNamingAuditRead_TS_18_3 verifies TS-18-3.
// Verifies: 18-REQ-1.3
func TestA401IsRefusedAndA403IsForbiddenNamingAuditRead_TS_18_3(t *testing.T) {
	c := New(newEventsServer(t, hubError(401, "unauthorized", "unauthorized")).URL, "1")
	if _, err := c.Events(ctx, "pat-1", "widgets"); !errors.Is(err, ErrRefused) {
		t.Errorf("401: %v, want refused", err)
	}
	c = New(newEventsServer(t, hubError(403, "missing_scope", "missing scope")).URL, "1")
	_, err := c.Events(ctx, "pat-1", "widgets")
	if !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), "audit:read") || !strings.Contains(err.Error(), "PUT /api/v1/workspaces/widgets") {
		t.Errorf("403: %v, want forbidden naming audit:read and PUT /api/v1/workspaces/widgets", err)
	}
}

// TestA404IsNotFoundAndOther4xxAreRejections_TS_18_4 verifies TS-18-4.
// Verifies: 18-REQ-1.4
func TestA404IsNotFoundAndOther4xxAreRejections_TS_18_4(t *testing.T) {
	c := New(newEventsServer(t, hubError(404, "not_found", "workspace not found")).URL, "1")
	if _, err := c.Events(ctx, "pat-1", "widgets"); !errors.Is(err, ErrNotFound) {
		t.Errorf("404: %v, want not found", err)
	}
	for _, status := range []int{400, 409, 422} {
		c := New(newEventsServer(t, hubError(status, "bad_filter", "the filter is wrong")).URL, "1")
		_, err := c.Events(ctx, "pat-1", "widgets")
		var rejected *RejectedError
		if !errors.As(err, &rejected) || !errors.Is(err, ErrRejected) || rejected.Status != status || rejected.Code != "bad_filter" || rejected.Message != "the filter is wrong" {
			t.Errorf("%d: %v (%+v), want a rejection with hub's code and message", status, err, rejected)
		}
	}
	c = New(newEventsServer(t, hubError(429, "rate_limited", "slow down")).URL, "1")
	if _, err := c.Events(ctx, "pat-1", "widgets"); errors.Is(err, ErrRejected) {
		t.Errorf("429 is a rejection: %v", err)
	}
}

// TestUnavailableAnswersOfTheEventsRoute_TS_18_5 verifies TS-18-5.
// Verifies: 18-REQ-1.5
func TestUnavailableAnswersOfTheEventsRoute_TS_18_5(t *testing.T) {
	cases := map[string]struct {
		answer http.HandlerFunc
		status int
	}{
		"429":      {hubError(429, "rate_limited", "slow down"), 429},
		"500":      {hubError(500, "internal", "boom"), 500},
		"503":      {hubError(503, "unavailable", "too many connections"), 503},
		"redirect": {func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/elsewhere", http.StatusFound) }, 302},
		"text/html": {func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<html>")
		}, 200},
		"hangup": {func(w http.ResponseWriter, r *http.Request) { panic(http.ErrAbortHandler) }, 0},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			srv := newEventsServer(t, c.answer)
			_, err := New(srv.URL, "1").Events(ctx, "pat-1", "widgets")
			var unavailable *UnavailableError
			if !errors.As(err, &unavailable) || unavailable.Status != c.status {
				t.Errorf("%v (%+v), want unavailable with status %d", err, unavailable, c.status)
			}
			if name == "redirect" && srv.count() != 1 {
				t.Errorf("%d requests, want the redirect not followed", srv.count())
			}
		})
	}
}

// TestNoErrorOfTheEventsCallHoldsThePAT_TS_18_6 verifies TS-18-6, a property: whatever the PAT and the answer, and even
// when hub echoes the Authorization header into its answer, no returned error holds the PAT.
// Verifies: 18-REQ-1.6
func TestNoErrorOfTheEventsCallHoldsThePAT_TS_18_6(t *testing.T) {
	echo := func(status int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"error":{"code":%d,"message":"no for %s","error_type":"x %s"}}`, status, auth, auth)
		}
	}
	answers := map[string]http.HandlerFunc{
		"200": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, r.Header.Get("Authorization"))
		},
		"401": echo(401), "403": echo(403), "404": echo(404), "400": echo(400), "429": echo(429), "503": echo(503),
		"redirect": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/x?"+r.Header.Get("Authorization"), http.StatusFound)
		},
		"hangup": func(w http.ResponseWriter, r *http.Request) { panic(http.ErrAbortHandler) },
	}
	servers := map[string]string{}
	for name, a := range answers {
		servers[name] = newEventsServer(t, a).URL
	}
	rng := rand.New(rand.NewPCG(18, 6))
	for range 60 {
		b := make([]byte, 8+rng.IntN(57))
		for i := range b {
			b[i] = byte('!' + rng.IntN('~'-'!'+1))
		}
		pat := string(b)
		for name, base := range servers {
			_, err := New(base, "1").Events(ctx, pat, "widgets")
			if err == nil {
				t.Fatalf("%s: no error", name)
			}
			for e := err; e != nil; e = errors.Unwrap(e) {
				if strings.Contains(fmt.Sprintf("%v %+v", e, e), pat) {
					t.Fatalf("%s: an error holds the PAT %q: %v", name, pat, e)
				}
			}
		}
	}
}
