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
	t     *testing.T
	Hub   *fakehub.Hub
	Store *store.Store
	DB    *sql.DB
	Svc   *jobs.Service
	URL   string
	Logs  *syncBuffer
	Dir   string
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

	cfg := jobs.Config{DB: st.DB(), Subscriptions: st, Logger: logger}
	for _, c := range configure {
		c(&cfg)
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

	e := &env{t: t, Hub: fh, Store: st, DB: st.DB(), Svc: svc, URL: ts.URL, Logs: logs, Dir: dir}
	e.subscribe("widgets")
	return e
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
