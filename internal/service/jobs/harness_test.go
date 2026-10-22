package jobs_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/api"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

var ctx = context.Background()

// The caller of the tests, as hub knows them.
const (
	callerToken = "caller-token"
	callerID    = "user-1"
)

// syncBuffer is a log destination that the service's goroutines may write to.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

// records are the log records written so far.
func (s *syncBuffer) records() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s.b.String()), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) == nil {
			out = append(out, rec)
		}
	}
	return out
}

// find are the records of the level whose message contains the text.
func (s *syncBuffer) find(level, text string) []map[string]any {
	var out []map[string]any
	for _, rec := range s.records() {
		if msg, _ := rec["msg"].(string); rec["level"] == level && strings.Contains(msg, text) {
			out = append(out, rec)
		}
	}
	return out
}

// env is the service under test: the real store over a file, the real jobs service,
// the real HTTP server, and a fake hub on which the caller sees widgets.
type env struct {
	t      *testing.T
	Hub    *fakehub.Hub
	Store  *store.Store
	DB     *sql.DB
	Svc    *jobs.Service
	Runner *fakerunner.Runner
	// Srv is the HTTP server of the API, which the tests may serve on a listener of their own
	// or call the handler of.
	Srv  *http.Server
	URL  string
	Logs *syncBuffer
	Dir  string
}

// withRunner is a configuration that makes the runner of the service r.
func withRunner(r jobs.Runner) func(*jobs.Config) { return func(c *jobs.Config) { c.Runner = r } }

// withWorkers is a configuration that makes [workers].count n.
func withWorkers(n int) func(*jobs.Config) { return func(c *jobs.Config) { c.Workers = n } }

// withClock is a configuration that gives the service the clock.
func withClock(c jobs.Clock) func(*jobs.Config) { return func(cfg *jobs.Config) { cfg.Clock = c } }

// newService builds another service on the same state file, as a restart does, with
// the runner and the clock; it is closed when the test ends.
func (e *env) newService(runner jobs.Runner, clock jobs.Clock) *jobs.Service {
	e.t.Helper()
	svc, err := jobs.New(jobs.Config{
		DB: e.DB, Subscriptions: e.Store, Logger: slog.New(slog.NewJSONHandler(e.Logs, nil)), Clock: clock,
		Workers: 2, AttemptsDir: filepath.Join(e.Dir, "attempts"), Runner: runner,
		PollInterval: 5 * time.Millisecond, RetryDelay: time.Millisecond, Retain: time.Hour,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { svc.Close() })
	return svc
}

// withRetain is a configuration that makes [database].retain d.
func withRetain(d time.Duration) func(*jobs.Config) { return func(c *jobs.Config) { c.Retain = d } }

// withRemover is a configuration that makes remove the function that removes the directory
// of a job.
func withRemover(remove func(string) error) func(*jobs.Config) {
	return func(c *jobs.Config) { c.RemoveAll = remove }
}

// withoutEnqueue is a configuration under which a job that is made is not put in the
// queue, so that a test puts the attempts there in the order it chooses.
func withoutEnqueue() func(*jobs.Config) {
	return func(c *jobs.Config) { c.Enqueue = func(context.Context, string, int) error { return nil } }
}

// newEnv starts the service. configure changes the configuration of the jobs
// service before it is built.
func newEnv(t *testing.T, configure ...func(*jobs.Config)) *env {
	t.Helper()
	dir := t.TempDir()
	key, err := secrets.NewKey(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, filepath.Join(dir, "agent-fox.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fh := fakehub.New(t)
	fh.AddUser(callerToken, callerID, []string{"widgets"})
	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))

	fake := fakerunner.New()
	cfg := jobs.Config{
		DB: st.DB(), Subscriptions: st, Logger: logger,
		Workers: 2, AttemptsDir: filepath.Join(dir, "attempts"), Runner: fake,
		// A poll and a delay that the tests do not wait on.
		PollInterval: 5 * time.Millisecond, RetryDelay: time.Millisecond,
	}
	for _, c := range configure {
		c(&cfg)
	}
	if r, ok := cfg.Runner.(*fakerunner.Runner); ok {
		fake = r
	}
	svc, err := jobs.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	version := (&toolio.App{Name: "agent-fox", Version: "1.2.3"}).VersionDocument()
	srv := api.NewServer(api.Deps{Store: st, Hub: hub.New(fh.URL, "test"), Logger: logger, Version: version, Jobs: svc})
	ts := httptest.NewUnstartedServer(srv.Handler)
	ts.Config.ErrorLog, ts.Config.ReadHeaderTimeout = srv.ErrorLog, srv.ReadHeaderTimeout
	ts.Start()
	t.Cleanup(ts.Close)

	e := &env{t: t, Hub: fh, Store: st, DB: st.DB(), Svc: svc, Runner: fake, Srv: srv, URL: ts.URL, Logs: logs, Dir: dir}
	e.subscribe("widgets")
	t.Cleanup(func() {
		fake.ReleaseAll()
		if err := svc.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return e
}

// Start starts the workers of the service.
func (e *env) Start() {
	e.t.Helper()
	if err := e.Svc.Start(); err != nil {
		e.t.Fatal(err)
	}
}

// addWorkspace makes the workspace one that the caller sees and that is subscribed.
func (e *env) addWorkspace(slug string) {
	e.t.Helper()
	e.Hub.Grant(callerID, slug)
	e.subscribe(slug)
}

// create makes a job of the kind for the issue on the workspace through the API, and
// returns it.
func (e *env) create(workspace string, kind jobs.Kind, issue int) jobs.Doc {
	e.t.Helper()
	r := e.call("POST", "/api/v1/workspaces/"+workspace+"/"+string(kind), map[string]any{"issue": issue})
	if r.Status != http.StatusAccepted {
		e.t.Fatalf("create %s %s %d: status %d\n%s", workspace, kind, issue, r.Status, r.Body)
	}
	return r.Doc(e.t)
}

// get is the document of a job with its attempts.
func (e *env) get(id string) jobs.Doc {
	e.t.Helper()
	doc, err := e.Svc.Get(ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return doc
}

// waitFor waits until the condition holds, for at most 10 seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// waitState waits until the job is in the state.
func (e *env) waitState(id string, state jobs.State) jobs.Doc {
	e.t.Helper()
	var doc jobs.Doc
	waitFor(e.t, id+" to be "+string(state), func() bool {
		doc = e.get(id)
		return doc.State == state
	})
	return doc
}

// subscribe subscribes the workspace, with a token that opens.
func (e *env) subscribe(slug string) {
	e.t.Helper()
	if _, _, err := e.Store.Upsert(ctx, slug, "af:", "pat-of-"+slug, callerID, callerID, time.Now()); err != nil {
		e.t.Fatal(err)
	}
}

// response is what a request got.
type response struct {
	Status int
	Header http.Header
	Body   []byte
}

// JSON decodes the body into a generic value.
func (r response) JSON(t *testing.T) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(r.Body, &v); err != nil {
		t.Fatalf("the body is not a JSON object (status %d): %v\n%s", r.Status, err, r.Body)
	}
	return v
}

// Doc decodes the body into a job document.
func (r response) Doc(t *testing.T) jobs.Doc {
	t.Helper()
	var d jobs.Doc
	if err := json.Unmarshal(r.Body, &d); err != nil {
		t.Fatalf("the body is not a job document (status %d): %v\n%s", r.Status, err, r.Body)
	}
	return d
}

// errorOf is the code and message of an error answer.
func (r response) errorOf(t *testing.T) (code, message string) {
	t.Helper()
	var body struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(r.Body, &body); err != nil {
		t.Fatalf("the body is not an error (status %d): %v\n%s", r.Status, err, r.Body)
	}
	return body.Error.Code, body.Error.Message
}

// call makes a request as the caller. A body that is not a string or bytes is sent as JSON.
func (e *env) call(method, path string, body any) response {
	return e.callAs(callerToken, method, path, body)
}

// callAs makes a request with the token.
func (e *env) callAs(token, method, path string, body any) response {
	e.t.Helper()
	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(b)
	case []byte:
		reader = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.URL+path, reader)
	if err != nil {
		e.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return response{Status: res.StatusCode, Header: res.Header, Body: raw}
}

// count is the number of rows a query counts.
func (e *env) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.DB.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}
