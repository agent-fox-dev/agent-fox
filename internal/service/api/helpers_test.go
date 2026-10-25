package api

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// bg is the context of the calls the tests make to the store and the jobs layer.
var bg = context.Background()

// cacheTTL is how long the caches of the test servers keep hub's answers.
const cacheTTL = 60 * time.Second

// The paths of hub that the caches ask, and the routes of the tests behind the checks of the API.
const (
	userPath = "/api/v1/user"
	pingPath = "/test/ping"
	slugPath = "/test/ws/"
)

// jsonType is the header of a body sent as JSON.
var jsonType = []string{"Content-Type", "application/json"}

// clock is the clock of the caches, as nanoseconds after 2026-10-22 08:00 UTC; only the test moves it.
type clock struct{ ns atomic.Int64 }

func (c *clock) Now() time.Time { return time.Date(2026, 10, 22, 8, 0, 0, int(c.ns.Load()), time.UTC) }

func (c *clock) Advance(d time.Duration) { c.ns.Add(int64(d)) }

// config is what newEnv builds a server from.
type config struct {
	dir    string       // the data directory
	key    []byte       // the key of the store
	hub    *fakehub.Hub // a new one when nil
	jobs   bool
	runner *fakerunner.Runner // with jobs: nil leaves every job queued, as no attempt is run
}

// env is a server of the service on a loopback port, over a real store, with a fake hub, a
// clock of the test and a log, and, if asked, the jobs layer.
type env struct {
	URL   string
	Hub   *fakehub.Hub
	Store *store.Store
	Jobs  *jobs.Service
	Clock *clock
	Log   *testutil.Log
	Dir   string
	HTTP  *http.Server
	srv   *server
}

// newEnv starts a server. Besides the routes of the service it has the routes of testRoutes.
func newEnv(t *testing.T, opts ...func(*config)) *env {
	t.Helper()
	cfg := config{dir: t.TempDir(), key: testutil.Key()}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.hub == nil {
		cfg.hub = fakehub.New(t)
	}
	key, err := secrets.NewKey(cfg.key)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(bg, filepath.Join(cfg.dir, "agent-fox.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e := &env{Hub: cfg.hub, Store: st, Clock: &clock{}, Log: &testutil.Log{}, Dir: cfg.dir}
	deps := Deps{
		Store: st, Hub: hub.New(cfg.hub.URL, "1.2.3"), Logger: e.Log.Logger(),
		Version:      (&toolio.App{Name: "agent-fox", Version: "1.2.3", Commit: "abc123"}).VersionDocument(),
		AuthCacheTTL: cacheTTL, Now: e.Clock.Now,
		Routes: func(mux *http.ServeMux, s *server) {
			e.srv = s
			testRoutes(mux, s)
		},
	}
	if cfg.jobs {
		e.Jobs = newJobs(t, st, e.Log, cfg.dir, cfg.runner)
		deps.Jobs = e.Jobs
	}
	e.HTTP = NewServer(deps)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = e.HTTP.Serve(ln) }()
	t.Cleanup(func() { _ = e.HTTP.Close() })
	e.URL = "http://" + ln.Addr().String()
	return e
}

// newJobs is the jobs layer over the store. Without a runner it is not started and queues
// nothing, so that every job it makes stays queued.
func newJobs(t *testing.T, st *store.Store, log *testutil.Log, dir string, runner *fakerunner.Runner) *jobs.Service {
	t.Helper()
	cfg := jobs.Config{
		DB: st.DB(), Subscriptions: st, Logger: log.Logger(), Workers: 1, Runner: runner,
		AttemptsDir: filepath.Join(dir, "attempts"), PollInterval: 5 * time.Millisecond, RetryDelay: time.Millisecond,
	}
	if runner == nil {
		cfg.Runner = fakerunner.New()
		cfg.Enqueue = func(context.Context, string, int) error { return nil }
	}
	svc, err := jobs.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	if runner != nil {
		if err := svc.Start(); err != nil {
			t.Fatal(err)
		}
	}
	return svc
}

// testRoutes are the routes that the tests reach the checks and the helpers of the API through: one
// that needs a caller (pingPath), one in a workspace (slugPath + slug), three that panic or write
// twice, and one that reads a body (PUT /test/echo).
func testRoutes(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET "+pingPath, s.authed(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"user": principalOf(r).user})
	}))
	mux.HandleFunc("GET "+slugPath+"{slug}", s.inWorkspace(func(w http.ResponseWriter, r *http.Request, slug string) {
		writeJSON(w, http.StatusOK, map[string]string{"slug": slug, "user": principalOf(r).user})
	}))
	mux.HandleFunc("GET /test/boom", func(http.ResponseWriter, *http.Request) { panic("the-panic-message-7f3a") })
	mux.HandleFunc("GET /test/abort", func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
	mux.HandleFunc("GET /test/late", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("partial"))
		panic("late")
	})
	mux.HandleFunc("GET /test/twice", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.WriteHeader(http.StatusTeapot) // net/http complains of this one
	})
	mux.HandleFunc("PUT /test/echo", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			HubToken string `json:"hub_token"`
		}
		if decodeBody(w, r, &body) {
			writeJSON(w, http.StatusOK, map[string]string{"hub_token": body.HubToken})
		}
	})
}

// withJobs gives the server the jobs layer, with the runner (see newJobs).
func withJobs(runner *fakerunner.Runner) func(*config) {
	return func(c *config) { c.jobs, c.runner = true, runner }
}

// call makes a request to the server, with the bearer token unless it is empty.
func (e *env) call(t *testing.T, method, path string, body any, token string, headers ...[]string) testutil.Response {
	t.Helper()
	if token != "" {
		headers = append(headers, testutil.Bearer(token))
	}
	return testutil.Request(t, method, e.URL+path, body, headers...)
}

// get is a GET with the token.
func (e *env) get(t *testing.T, path, token string) testutil.Response {
	t.Helper()
	return e.call(t, http.MethodGet, path, nil, token)
}

// put subscribes the workspace as the caller with the token of the subscription.
func (e *env) put(t *testing.T, slug, caller, pat string) testutil.Response {
	t.Helper()
	return e.call(t, http.MethodPut, "/api/v1/workspaces/"+slug, map[string]string{"hub_token": pat}, caller)
}

// seed stores a subscription, as a subscribe would have.
func (e *env) seed(t *testing.T, slug, pat string) {
	t.Helper()
	if _, _, err := e.Store.Upsert(bg, slug, pat, "usr_9", "usr_1", e.Clock.Now()); err != nil {
		t.Fatal(err)
	}
}

// seedUnreadable stores a subscription whose sealed token does not open.
func (e *env) seedUnreadable(t *testing.T, slug string) {
	t.Helper()
	testutil.Exec(t, e.Store.DB(), "INSERT INTO af_subscriptions (slug, sealed_token, subscribed_by, created_at, updated_at) VALUES (?, x'0102030405', 'usr_1', '2026-10-22T08:00:00Z', '2026-10-22T08:00:00Z')", slug)
}

// rows is how many subscriptions the store holds.
func (e *env) rows(t *testing.T) int {
	t.Helper()
	return testutil.Count(t, e.Store.DB(), "af_subscriptions", "")
}

// userCalls is how many times hub was asked whom the token belongs to.
func (e *env) userCalls(token string) int { return e.Hub.Count(userPath, fakehub.TokenHash(token)) }

// workspaceCalls is how many times hub was asked whether the token sees the workspace.
func (e *env) workspaceCalls(token, slug string) int {
	return e.Hub.Count("/api/v1/workspaces/"+slug, fakehub.TokenHash(token))
}

// labelCalls is how many label creations hub received for the workspace.
func (e *env) labelCalls(slug string) int {
	return len(e.Hub.RequestsTo(http.MethodPost, "/api/v1/workspaces/"+slug+"/labels"))
}

// cached reports whether the caches hold an identity for the token and whether they hold
// its visibility of the workspace.
func (e *env) cached(token, slug string) (identity, visibility bool) {
	_, identity = e.srv.auth.identities.Get(hashToken(token))
	_, visibility = e.srv.auth.visibility.Get(visibilityKey{hashToken(token), slug})
	return identity, visibility
}

// requests are the records of the requests in the log.
func (e *env) requests() []map[string]any {
	var out []map[string]any
	for _, rec := range e.Log.Records() {
		if rec["msg"] == "request" {
			out = append(out, rec)
		}
	}
	return out
}

// message is the message of an error envelope.
func message(t *testing.T, r testutil.Response) string {
	t.Helper()
	detail, _ := r.Object(t)["error"].(map[string]any)
	msg, _ := detail["message"].(string)
	return msg
}

// failed checks that the answer is the error with the status and the code.
func failed(t *testing.T, what string, r testutil.Response, status int, code Code) {
	t.Helper()
	if r.Status != status || r.Error(t) != string(code) {
		t.Errorf("%s: %d %s, want %d %s", what, r.Status, r.Body, status, code)
	}
}
