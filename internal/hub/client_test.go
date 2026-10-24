package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var ctx = context.Background()

// seen is a request the test server received.
type seen struct {
	Method, Path, URI string
	Header            http.Header
}

// recorder is a server that records its requests and answers with a function.
type recorder struct {
	*httptest.Server
	mu   sync.Mutex
	seen []seen
}

func newRecorder(t *testing.T, answer http.HandlerFunc) *recorder {
	t.Helper()
	r := &recorder{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.seen = append(r.seen, seen{req.Method, req.URL.Path, req.RequestURI, req.Header.Clone()})
		r.mu.Unlock()
		answer(w, req)
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *recorder) requests() []seen {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]seen(nil), r.seen...)
}

// status answers every request with the status and body.
func status(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	}
}

// hangup closes the connection without answering.
func hangup(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }

// class names what kind of failure err is, and fails the test if it is more than one.
func class(t *testing.T, err error) string {
	t.Helper()
	var unavailable *UnavailableError
	kinds := map[string]bool{
		"refused":     errors.Is(err, ErrRefused),
		"not found":   errors.Is(err, ErrNotFound),
		"unavailable": errors.As(err, &unavailable),
	}
	var found []string
	for kind, is := range kinds {
		if is {
			found = append(found, kind)
		}
	}
	switch {
	case err == nil:
		return "ok"
	case len(found) == 1:
		return found[0]
	}
	t.Errorf("error %q is of the classes %v, want exactly one", err, found)
	return "?"
}

// TestTheClientSendsTheTwoCallsWithTheirHeaders_TS_11_30 verifies TS-11-30:
// The hub client sends the two GET calls with bearer, accept and user-agent
// headers and an escaped slug.
// Verifies: 11-REQ-5.1
func TestTheClientSendsTheTwoCallsWithTheirHeaders_TS_11_30(t *testing.T) {
	srv := newRecorder(t, status(200, `{"id":"usr_1"}`))
	for _, prefix := range []string{"", "/hub/prefix"} {
		t.Run("prefix "+prefix, func(t *testing.T) {
			before := len(srv.requests())
			c := New(srv.URL+prefix, "1.2.3")
			if _, err := c.User(ctx, "tok"); err != nil {
				t.Fatal(err)
			}
			if err := c.Workspace(ctx, "tok", "a b/c?"); err != nil {
				t.Fatal(err)
			}
			got := srv.requests()[before:]
			if len(got) != 2 {
				t.Fatalf("%d requests, want exactly the two calls: %+v", len(got), got)
			}
			if got[0].Method != "GET" || got[0].URI != prefix+"/api/v1/user" {
				t.Errorf("User: %s %s, want GET %s/api/v1/user", got[0].Method, got[0].URI, prefix)
			}
			if got[1].Method != "GET" || got[1].URI != prefix+"/api/v1/workspaces/a%20b%2Fc%3F" {
				t.Errorf("Workspace: %s %s, want GET %s/api/v1/workspaces/a%%20b%%2Fc%%3F", got[1].Method, got[1].URI, prefix)
			}
			for _, r := range got {
				if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("Accept") != "application/json" || r.Header.Get("User-Agent") != "agent-fox/1.2.3" {
					t.Errorf("%s headers = %v, want Bearer tok, application/json and agent-fox/1.2.3", r.URI, r.Header)
				}
			}
		})
	}

	t.Run("a trailing slash of the base is ignored", func(t *testing.T) {
		before := len(srv.requests())
		if _, err := New(srv.URL+"/p/", "1").User(ctx, "tok"); err != nil {
			t.Fatal(err)
		}
		if got := srv.requests()[before].URI; got != "/p/api/v1/user" {
			t.Errorf("URI = %s, want /p/api/v1/user", got)
		}
	})
}

// countingTransport answers every request with a body that has no end and counts
// what is read of it.
type countingTransport struct{ read atomic.Int64 }

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: 200, Header: http.Header{}, Request: req,
		Body: io.NopCloser(readerFunc(func(p []byte) (int, error) {
			for i := range p {
				p[i] = ' '
			}
			c.read.Add(int64(len(p)))
			return len(p), nil
		})),
	}, nil
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// TestTheClientBoundsTimeAndBodyAndDoesNotFollowRedirects_TS_11_31 verifies TS-11-31:
// The hub client times out after 10 seconds or the context deadline, caps bodies
// at 1 MiB and does not follow redirects.
// Verifies: 11-REQ-5.2
func TestTheClientBoundsTimeAndBodyAndDoesNotFollowRedirects_TS_11_31(t *testing.T) {
	if got := New("http://127.0.0.1:1", "1").HTTP.Timeout; got != 10*time.Second {
		t.Errorf("the client's timeout = %v, want 10s", got)
	}

	t.Run("a context that is shorter ends the call", func(t *testing.T) {
		hang := newRecorder(t, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
		for name, call := range map[string]func(context.Context) error{
			"User":      func(ctx context.Context) error { _, err := New(hang.URL, "1").User(ctx, "t"); return err },
			"Workspace": func(ctx context.Context) error { return New(hang.URL, "1").Workspace(ctx, "t", "widgets") },
		} {
			short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			start := time.Now()
			err := call(short)
			cancel()
			if elapsed := time.Since(start); elapsed > 3*time.Second {
				t.Errorf("%s took %v with a context of 50ms", name, elapsed)
			}
			if got := class(t, err); got != "unavailable" {
				t.Errorf("%s: %v is %s, want unavailable", name, err, got)
			}
		}
	})

	t.Run("a redirect is not followed", func(t *testing.T) {
		target := newRecorder(t, status(200, `{"id":"usr_1"}`))
		redirect := newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
		})
		_, err := New(redirect.URL, "1").User(ctx, "tok")
		if got := class(t, err); got != "unavailable" {
			t.Errorf("User through a redirect: %v is %s, want unavailable", err, got)
		}
		if err := New(redirect.URL, "1").Workspace(ctx, "tok", "widgets"); class(t, err) != "unavailable" {
			t.Errorf("Workspace through a redirect: %v, want unavailable", err)
		}
		if n := len(target.requests()); n != 0 {
			t.Errorf("the target of the redirect received %d requests, want none: the token must not reach another host", n)
		}
	})

	t.Run("at most 1 MiB of a body is read", func(t *testing.T) {
		for name, call := range map[string]func(*Client) error{
			"User":      func(c *Client) error { _, err := c.User(ctx, "t"); return err },
			"Workspace": func(c *Client) error { return c.Workspace(ctx, "t", "widgets") },
		} {
			counter := &countingTransport{}
			c := New("http://hub.example.invalid", "1")
			c.HTTP.Transport = counter
			err := call(c)
			if got := class(t, err); got != "unavailable" {
				t.Errorf("%s with a body that has no end: %v is %s, want unavailable", name, err, got)
			}
			if read := counter.read.Load(); read > 1<<20+1 {
				t.Errorf("%s read %d bytes of the body, want at most %d", name, read, 1<<20+1)
			}
		}
	})

	t.Run("a valid object larger than 1 MiB is unavailable", func(t *testing.T) {
		big := `{"id":"usr_1","pad":"` + strings.Repeat("x", 2<<20) + `"}`
		srv := newRecorder(t, status(200, big))
		if _, err := New(srv.URL, "1").User(ctx, "t"); class(t, err) != "unavailable" {
			t.Errorf("User: %v, want unavailable", err)
		}
		if err := New(srv.URL, "1").Workspace(ctx, "t", "w"); class(t, err) != "unavailable" {
			t.Errorf("Workspace: %v, want unavailable", err)
		}
	})
}

// TestTheBodyOfAnErrorAnswerIsNotWhatDecidesIt checks that a refusal or a not-found
// is one whatever the size of the body that comes with it.
// Verifies: 11-REQ-5.2, 11-REQ-5.4
func TestTheBodyOfAnErrorAnswerIsNotWhatDecidesIt(t *testing.T) {
	big := strings.Repeat("e", 3<<20)
	for _, tc := range []struct {
		status          int
		user, workspace string
	}{{401, "refused", "refused"}, {403, "refused", "not found"}, {404, "unavailable", "not found"}} {
		srv := newRecorder(t, status(tc.status, big))
		if _, err := New(srv.URL, "1").User(ctx, "tok"); class(t, err) != tc.user {
			t.Errorf("User with a %d and a body of 3 MiB: %v is %s, want %s", tc.status, err, class(t, err), tc.user)
		}
		if err := New(srv.URL, "1").Workspace(ctx, "tok", "w"); class(t, err) != tc.workspace {
			t.Errorf("Workspace with a %d and a body of 3 MiB: %v is %s, want %s", tc.status, err, class(t, err), tc.workspace)
		}
	}
}

// TestTheClientReadsTheUserAndTheWorkspace_TS_11_32 verifies TS-11-32:
// A 200 user object yields the id as a string whether sent as a string or number,
// and a 200 workspace is found.
// Verifies: 11-REQ-5.3
func TestTheClientReadsTheUserAndTheWorkspace_TS_11_32(t *testing.T) {
	for _, tc := range []struct {
		body string
		want User
	}{
		{`{"id":"usr_1","login":"ann"}`, User{ID: "usr_1", Login: "ann"}},
		{`{"id":42}`, User{ID: "42"}},
		{`{"id":"550e8400-e29b-41d4-a716-446655440000","username":"bob","email":"b@example.com"}`, User{ID: "550e8400-e29b-41d4-a716-446655440000", Login: "bob"}},
		{`{"id":"usr_2","login":"cy","username":"other"}`, User{ID: "usr_2", Login: "cy"}},
		{`{"id":"usr_3","login":7}`, User{ID: "usr_3"}},
		{`{"id":9007199254740993}`, User{ID: "9007199254740993"}},
	} {
		srv := newRecorder(t, status(200, tc.body))
		got, err := New(srv.URL, "1").User(ctx, "tok")
		if err != nil || got != tc.want {
			t.Errorf("User on %s = %+v, %v; want %+v", tc.body, got, err, tc.want)
		}
	}

	for _, body := range []string{`{"slug":"widgets"}`, `{}`} {
		srv := newRecorder(t, status(200, body))
		if err := New(srv.URL, "1").Workspace(ctx, "tok", "widgets"); err != nil {
			t.Errorf("Workspace on %s: %v, want found", body, err)
		}
	}
}

// TestHubStatusesMapToRefusedNotFoundAndUnavailable_TS_11_33 verifies TS-11-33:
// Hub 401, 403 and 404 map to refused, not found and unavailable per call.
// Verifies: 11-REQ-5.4
func TestHubStatusesMapToRefusedNotFoundAndUnavailable_TS_11_33(t *testing.T) {
	user := map[int]string{401: "refused", 403: "refused", 404: "unavailable"}
	workspace := map[int]string{401: "refused", 403: "not found", 404: "not found"}
	for code, want := range user {
		srv := newRecorder(t, status(code, `{"error":{"code":"x","message":"y"}}`))
		_, err := New(srv.URL, "1").User(ctx, "tok")
		if got := class(t, err); got != want {
			t.Errorf("User with %d: %v is %s, want %s", code, err, got, want)
		}
	}
	for code, want := range workspace {
		srv := newRecorder(t, status(code, `{"error":{"code":"x","message":"y"}}`))
		err := New(srv.URL, "1").Workspace(ctx, "tok", "widgets")
		if got := class(t, err); got != want {
			t.Errorf("Workspace with %d: %v is %s, want %s", code, err, got, want)
		}
	}
}

// TestEverythingElseIsUnavailable_TS_11_34 verifies TS-11-34:
// 3xx, 429, 5xx, transport errors, timeouts and bad 200 bodies are all unavailable.
// Verifies: 11-REQ-5.5
func TestEverythingElseIsUnavailable_TS_11_34(t *testing.T) {
	statuses := map[string]http.HandlerFunc{}
	for _, code := range []int{300, 301, 302, 307, 308, 400, 418, 429, 500, 502, 503, 504} {
		statuses[fmt.Sprint(code)] = status(code, `{"id":"usr_1"}`)
	}
	badBodies := map[string]http.HandlerFunc{
		"hangup":              hangup,
		"200 with HTML":       status(200, "<html>hello</html>"),
		"200 with no body":    status(200, ""),
		"200 with an array":   status(200, `[{"id":"usr_1"}]`),
		"200 with null":       status(200, `null`),
		"200 with a string":   status(200, `"usr_1"`),
		"200 with a cut body": status(200, `{"id":"usr_1"`),
	}
	userBodies := map[string]http.HandlerFunc{
		"200 without an id":    status(200, `{"login":"ann"}`),
		"200 with an empty id": status(200, `{"id":""}`),
		"200 with a null id":   status(200, `{"id":null}`),
		"200 with a bool id":   status(200, `{"id":true}`),
		"200 with an object":   status(200, `{"id":{"a":1}}`),
	}

	for name, h := range statuses {
		srv := newRecorder(t, h)
		_, err := New(srv.URL, "1").User(ctx, "tok")
		if got := class(t, err); got != "unavailable" {
			t.Errorf("User with %s: %v is %s, want unavailable", name, err, got)
		}
		if got := class(t, New(srv.URL, "1").Workspace(ctx, "tok", "w")); got != "unavailable" {
			t.Errorf("Workspace with %s is %s, want unavailable", name, got)
		}
	}
	for name, h := range badBodies {
		srv := newRecorder(t, h)
		if _, err := New(srv.URL, "1").User(ctx, "tok"); class(t, err) != "unavailable" {
			t.Errorf("User with %s: %v, want unavailable", name, err)
		}
		if err := New(srv.URL, "1").Workspace(ctx, "tok", "w"); class(t, err) != "unavailable" {
			t.Errorf("Workspace with %s: %v, want unavailable", name, err)
		}
	}
	for name, h := range userBodies {
		srv := newRecorder(t, h)
		if _, err := New(srv.URL, "1").User(ctx, "tok"); class(t, err) != "unavailable" {
			t.Errorf("User with %s: %v, want unavailable", name, err)
		}
	}

	// A host that does not listen at all.
	dead := httptest.NewServer(nil)
	dead.Close()
	if _, err := New(dead.URL, "1").User(ctx, "tok"); class(t, err) != "unavailable" {
		t.Errorf("User on a closed port: %v, want unavailable", err)
	}
}

// TestErrorsAreToldApartAndNeverHoldTheToken_TS_11_35 verifies TS-11-35:
// Hub errors are told apart by errors.Is or errors.As and never contain the token.
// Verifies: 11-REQ-5.6
func TestErrorsAreToldApartAndNeverHoldTheToken_TS_11_35(t *testing.T) {
	const token = "tok-SECRET-123"
	dead := httptest.NewServer(nil)
	dead.Close()
	var errs []error
	for _, h := range []http.HandlerFunc{status(401, "{}"), status(403, "{}"), status(404, "{}"), status(500, "{}"), status(302, ""), hangup, status(200, "nope")} {
		srv := newRecorder(t, h)
		_, uerr := New(srv.URL, "1").User(ctx, token)
		errs = append(errs, uerr, New(srv.URL, "1").Workspace(ctx, token, "widgets"))
	}
	_, uerr := New(dead.URL, "1").User(ctx, token)
	errs = append(errs, uerr, New(dead.URL, "1").Workspace(ctx, token, "widgets"))
	short, cancel := context.WithTimeout(ctx, time.Nanosecond)
	defer cancel()
	_, uerr = New(dead.URL, "1").User(short, token)
	errs = append(errs, uerr)

	for _, err := range errs {
		if err == nil {
			continue
		}
		class(t, err)
		if strings.Contains(err.Error(), token) {
			t.Errorf("error %q contains the token", err)
		}
		if strings.Contains(err.Error(), "127.0.0.1") && strings.Contains(err.Error(), "http://") {
			t.Errorf("error %q holds the URL of the call", err)
		}
	}
	var unavailable *UnavailableError
	if err := New(dead.URL, "1").Workspace(ctx, token, "widgets"); !errors.As(err, &unavailable) || unavailable.Err == nil {
		t.Errorf("a transport failure = %v, want an UnavailableError that wraps the cause", err)
	}
	if err := New(newRecorder(t, status(503, "{}")).URL, "1").Workspace(ctx, token, "w"); !errors.As(err, &unavailable) || unavailable.Status != 503 {
		t.Errorf("a 503 = %v, want an UnavailableError with the status", err)
	}
}

// TestNoErrorMessageEverHoldsTheToken_TS_11_36 verifies TS-11-36:
// No hub error message ever contains the token.
// Verifies: 11-REQ-5.6
func TestNoErrorMessageEverHoldsTheToken_TS_11_36(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 36))
	t.Logf("seed: PCG(11, 36)")

	var mode atomic.Value
	mode.Store("200")
	srv := newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		switch m := mode.Load().(string); m {
		case "hangup":
			panic(http.ErrAbortHandler)
		case "hang":
			<-r.Context().Done()
		case "bad body":
			_, _ = io.WriteString(w, "{not json "+r.Header.Get("Authorization"))
		case "echo":
			status(200, `{"id":"`+r.Header.Get("Authorization")+`"}`)(w, r)
		case "bad location": // the transport quotes a Location it cannot parse in its error
			w.Header().Set("Location", "%zz/"+r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusFound)
		default:
			var code int
			_, _ = fmt.Sscan(m, &code)
			status(code, `{"error":{"message":"`+strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")+`"}}`)(w, r)
		}
	})

	const printable = "!\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~"
	modes := []string{"200", "301", "302", "400", "401", "403", "404", "418", "429", "500", "503", "hangup", "hang", "bad body", "echo", "bad location"}
	for range 120 {
		token := make([]byte, 8+rng.IntN(57))
		for i := range token {
			token[i] = printable[rng.IntN(len(printable))]
		}
		m := modes[rng.IntN(len(modes))]
		mode.Store(m)
		callCtx := ctx
		if m == "hang" {
			var cancel context.CancelFunc
			callCtx, cancel = context.WithTimeout(ctx, 20*time.Millisecond)
			defer cancel()
		}
		c := New(srv.URL, "1")
		_, userErr := c.User(callCtx, string(token))
		wsErr := c.Workspace(callCtx, string(token), "widgets")
		for _, err := range []error{userErr, wsErr} {
			if err != nil && strings.Contains(err.Error(), string(token)) {
				t.Fatalf("mode %s: error %q contains the token %q", m, err, token)
			}
		}
	}
}
