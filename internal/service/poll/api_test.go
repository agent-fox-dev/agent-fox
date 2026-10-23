package poll

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"

	"github.com/agent-fox-dev/agentfox/internal/service/api"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The caller of the API in the tests, as hub knows them.
const (
	callerToken = "caller-token"
	callerID    = "user-1"
)

// String is everything that was logged.
func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// hookTransport calls before for each request of the hub client, and sends it.
type hookTransport struct {
	base   http.RoundTripper
	before func(*http.Request)
}

func (h hookTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	h.before(r)
	return h.base.RoundTrip(r)
}

// withBeforeRequest calls f with each request that the poll sends to hub, just before it goes.
func withBeforeRequest(f func(*http.Request)) option {
	return withPoll(func(c *Config) {
		base := c.Hub.HTTP.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		c.Hub.HTTP.Transport = hookTransport{base: base, before: f}
	})
}

// refusing is a jobs layer whose Create is refused with the error, and that counts the calls of Create.
type refusing struct {
	Jobs
	err   error
	calls atomic.Int32
}

func (r *refusing) Create(context.Context, jobs.CreateRequest) (jobs.Doc, error) {
	r.calls.Add(1)
	return jobs.Doc{}, r.err
}

// withJobs wraps the jobs layer that the poll is given.
func withJobs(wrap func(Jobs) Jobs) option {
	return withPoll(func(c *Config) { c.Jobs = wrap(c.Jobs) })
}

// held is a jobs layer whose Create waits, just before it makes the job, until it is released.
type held struct {
	Jobs
	reached, release chan struct{}
}

func newHeld(inner Jobs) *held {
	return &held{Jobs: inner, reached: make(chan struct{}, 8), release: make(chan struct{})}
}

func (h *held) Create(ctx context.Context, req jobs.CreateRequest) (jobs.Doc, error) {
	h.reached <- struct{}{}
	select {
	case <-h.release:
	case <-ctx.Done():
		return jobs.Doc{}, ctx.Err()
	}
	return h.Jobs.Create(ctx, req)
}

// api serves the HTTP API of the service over the store, the jobs layer and the hub of the environment, and returns its
// URL. The caller of the tests sees the workspace on hub.
func (e *env) api() string {
	e.t.Helper()
	if e.apiURL != "" {
		return e.apiURL
	}
	e.Hub.AddUser(callerToken, callerID, []string{slug})
	version := (&toolio.App{Name: "agent-fox", Version: "1.2.3"}).VersionDocument()
	srv := api.NewServer(api.Deps{Store: e.Store, Hub: e.Client, Logger: e.logger, Version: version, Jobs: e.Jobs})
	ts := httptest.NewUnstartedServer(srv.Handler)
	ts.Config.ErrorLog, ts.Config.ReadHeaderTimeout = srv.ErrorLog, srv.ReadHeaderTimeout
	ts.Start()
	e.t.Cleanup(ts.Close)
	e.apiURL = ts.URL
	return e.apiURL
}

// call makes a request to the API as the caller and returns the status and the body.
func (e *env) call(method, path, body string) (int, string) {
	e.t.Helper()
	req, err := http.NewRequest(method, e.api()+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+callerToken)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}
