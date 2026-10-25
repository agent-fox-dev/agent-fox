package hub

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

func TestUserAndWorkspace_SendTheTokenAtTheirPathsUnderTheBase(t *testing.T) {
	h := fakehub.New(t)
	for _, base := range []string{"", "/hub/prefix", "/p/"} {
		before := h.Total()
		c := New(h.URL+base, "1.2.3")
		_, _ = c.User(ctx, "tok")
		_ = c.Workspace(ctx, "tok", "a b/c?")
		got := h.Requests()[before:]
		prefix := strings.TrimSuffix(base, "/")
		if len(got) != 2 || got[0].URI != prefix+"/api/v1/user" || got[1].URI != prefix+"/api/v1/workspaces/a%20b%2Fc%3F" {
			t.Fatalf("base %q: requests %+v, want the user and the escaped workspace under the base", base, got)
		}
		for _, r := range got {
			if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("Accept") != "application/json" || r.Header.Get("User-Agent") != "agent-fox/1.2.3" {
				t.Errorf("%s %s with headers %v, want GET with the bearer, JSON and agent-fox/1.2.3", r.Method, r.URI, r.Header)
			}
		}
	}
}

func TestUser_ReadsTheIDAsWrittenAndTheLogin(t *testing.T) {
	h := fakehub.New(t)
	for body, want := range map[string]User{
		`{"id":"usr_1","login":"ann"}`: {ID: "usr_1", Login: "ann"},
		`{"id":42}`:                    {ID: "42"},
		`{"id":9007199254740993}`:      {ID: "9007199254740993"},
		`{"id":"u-2","username":"bob","email":"b@example.com"}`: {ID: "u-2", Login: "bob"},
		`{"id":"usr_2","login":"cy","username":"other"}`:        {ID: "usr_2", Login: "cy"},
		`{"id":"usr_3","login":7}`:                              {ID: "usr_3"},
	} {
		h.Clear()
		h.Inject("GET", "/api/v1/user", fakehub.Fault{Status: 200, Body: body})
		if got, err := New(h.URL, "1").User(ctx, "tok"); err != nil || got != want {
			t.Errorf("User on %s = %+v, %v; want %+v", body, got, err, want)
		}
	}
}

// The body of an answer that is not a 200 is never what decides it, whatever its size.
func TestUserAndWorkspace_TellTheStatusesApart(t *testing.T) {
	h := fakehub.New(t)
	big := strings.Repeat("e", 3<<20)
	for _, c := range []struct {
		fault           fakehub.Fault
		user, workspace string
	}{
		{fakehub.Fault{Status: 200, Body: `{"id":"usr_1"}`}, "ok", "ok"},
		{fakehub.Fault{Status: 200, Body: `{}`}, "unavailable", "ok"},
		{fakehub.Fault{Status: 401, Body: big}, "refused", "refused"},
		{fakehub.Fault{Status: 403, Body: big}, "refused", "not found"},
		{fakehub.Fault{Status: 404, Body: big}, "unavailable", "not found"},
		{fakehub.Fault{Redirect: "/elsewhere"}, "unavailable", "unavailable"},
		{fakehub.Fault{Status: 300, Body: "{}"}, "unavailable", "unavailable"},
		{fakehub.Fault{Status: 400}, "unavailable", "unavailable"},
		{fakehub.Fault{Status: 429}, "unavailable", "unavailable"},
		{fakehub.Fault{Status: 500}, "unavailable", "unavailable"},
		{fakehub.Fault{Status: 503}, "unavailable", "unavailable"},
	} {
		h.Clear()
		h.Inject("GET", "/api/v1/user", c.fault)
		h.Inject("GET", "/api/v1/workspaces/w", c.fault)
		_, uerr := New(h.URL, "1").User(ctx, "tok")
		werr := New(h.URL, "1").Workspace(ctx, "tok", "w")
		if got := kind(t, uerr); got != c.user {
			t.Errorf("User on %d %q: %v is %s, want %s", c.fault.Status, c.fault.Redirect, uerr, got, c.user)
		}
		if got := kind(t, werr); got != c.workspace {
			t.Errorf("Workspace on %d %q: %v is %s, want %s", c.fault.Status, c.fault.Redirect, werr, got, c.workspace)
		}
	}
	if n := len(h.RequestsTo("", "/elsewhere")); n != 0 {
		t.Errorf("a redirect was followed %d times: the token must not reach another host", n)
	}
	h.Clear()
	h.Inject("", "/api/v1/user", fakehub.Fault{Status: 503})
	_, err := New(h.URL, "1").User(ctx, "tok")
	if u, ok := errors.AsType[*UnavailableError](err); !ok || u.Status != 503 || err.Error() != "hub is unavailable: it answered 503" {
		t.Errorf("a 503 = %v, want an UnavailableError with the status", err)
	}
}

func TestUserAndWorkspace_AnAnswerThatIsNotHubsIsUnavailable(t *testing.T) {
	h := fakehub.New(t)
	answers := map[string]fakehub.Fault{
		"a hang-up": {Hangup: true}, "HTML": {Status: 200, Body: "<html>"}, "a blank body": {Status: 200, Body: " "}, "an array": {Status: 200, Body: `[{"id":"u"}]`},
		"null": {Status: 200, Body: "null"}, "a string": {Status: 200, Body: `"u"`}, "a cut body": {Status: 200, Body: `{"id":"u"`},
		"an object over 1 MiB": {Oversize: 2 << 20},
	}
	// What only a user is read for.
	users := map[string]string{"no id": `{"login":"ann"}`, "an empty id": `{"id":""}`, "a null id": `{"id":null}`, "a bool id": `{"id":true}`, "an object id": `{"id":{"a":1}}`}
	for name, body := range users {
		answers[name] = fakehub.Fault{Status: 200, Body: body}
	}
	for name, f := range answers {
		h.Clear()
		h.Inject("GET", "/api/v1/user", f)
		h.Inject("GET", "/api/v1/workspaces/w", f)
		if _, err := New(h.URL, "1").User(ctx, "tok"); kind(t, err) != "unavailable" {
			t.Errorf("User on %s: %v, want unavailable", name, err)
		}
		if _, user := users[name]; !user && kind(t, New(h.URL, "1").Workspace(ctx, "tok", "w")) != "unavailable" {
			t.Errorf("Workspace on %s: want unavailable", name)
		}
	}

	dead := httptest.NewServer(nil)
	dead.Close()
	err := New(dead.URL, "1").Workspace(ctx, "tok", "w")
	if u, ok := errors.AsType[*UnavailableError](err); !ok || u.Status != 0 || u.Unwrap() == nil || !strings.HasPrefix(err.Error(), "hub is unavailable: ") {
		t.Errorf("a closed port = %v, want an UnavailableError that wraps the cause", err)
	}

	h.Clear()
	h.Inject("", "/api/v1/user", fakehub.Fault{Delay: time.Minute})
	h.Inject("", "/api/v1/workspaces/w", fakehub.Fault{Delay: time.Minute})
	short, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if _, err := New(h.URL, "1").User(short, "tok"); !errors.Is(err, context.DeadlineExceeded) || !isUnavailable(err) {
		t.Errorf("User past the context: %v, want unavailable with the error of the context", err)
	}
	if err := New(h.URL, "1").Workspace(short, "tok", "w"); !isUnavailable(err) {
		t.Errorf("Workspace past the context: %v, want unavailable", err)
	}
}

// endless answers 200 with a body that has no end, and counts what is read of it.
type endless struct{ read atomic.Int64 }

func (e *endless) RoundTrip(req *http.Request) (*http.Response, error) {
	body := readerFunc(func(p []byte) (int, error) {
		clear(p)
		e.read.Add(int64(len(p)))
		return len(p), nil
	})
	return &http.Response{StatusCode: 200, Header: http.Header{}, Request: req, Body: io.NopCloser(body)}, nil
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func TestUserAndWorkspace_ReadAtMostOneMiB(t *testing.T) {
	for name, call := range map[string]func(*Client) error{
		"User":      func(c *Client) error { _, err := c.User(ctx, "t"); return err },
		"Workspace": func(c *Client) error { return c.Workspace(ctx, "t", "w") },
	} {
		transport := &endless{}
		c := New("http://hub.example.invalid", "1")
		c.HTTP.Transport = transport
		if err := call(c); !isUnavailable(err) {
			t.Errorf("%s with a body that has no end: %v, want unavailable", name, err)
		}
		if read := transport.read.Load(); read > maxBody+64<<10 {
			t.Errorf("%s read %d bytes of the body, want about 1 MiB", name, read)
		}
	}
}

func TestBound_IsTheClientsTimeoutOrTenSecondsAndTheLongOneOrFiveMinutes(t *testing.T) {
	if c := New("http://127.0.0.1:1", "1"); c.bound(false) != 10*time.Second || c.bound(true) != 5*time.Minute {
		t.Errorf("New: bounds %v and %v, want 10s and 5m", c.bound(false), c.bound(true))
	}
	if c := (&Client{}); c.bound(false) != 10*time.Second {
		t.Errorf("a client with no HTTP client: bound %v, want 10s", c.bound(false))
	}
	c := &Client{HTTP: &http.Client{Timeout: 3 * time.Second}, LongTimeout: 7 * time.Second}
	if c.bound(false) != 3*time.Second || c.bound(true) != 7*time.Second {
		t.Errorf("bounds %v and %v, want the client's 3s and 7s", c.bound(false), c.bound(true))
	}
}
