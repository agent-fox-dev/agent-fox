package poll

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

// syncBuffer is a log destination that the goroutines of the poll may write to.
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

// level are the records of the level, whatever they say.
func (s *syncBuffer) level(level string) []map[string]any { return s.find(level, "") }

// The widgets of the tests: the workspace the poll works on, and its token.
const (
	slug = "widgets"
	pat  = "pat-POLL-0123456789"
)

// epoch is the instant the clock of the tests starts at.
var epoch = time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)

// env is the poll under test, with the real store over a file, the real jobs layer with the fake runner, a fake hub that
// has the workspace widgets, and a clock that stands still until the test moves it.
type env struct {
	t      *testing.T
	Hub    *fakehub.Hub
	Store  *store.Store
	DB     *sql.DB
	Jobs   *jobs.Service
	Runner *fakerunner.Runner
	Clock  *fakeclock.Clock
	Logs   *syncBuffer
	Dir    string
	Poll   *Supervisor
	Client *hub.Client
}

// option changes the environment before it is built.
type option func(*envConfig)

type envConfig struct {
	runner  *fakerunner.Runner
	vars    map[string]string
	workers int
	jobs    func(*jobs.Config)
	poll    func(*Config)
}

// withRunner makes the runner of the jobs layer r.
func withRunner(r *fakerunner.Runner) option { return func(c *envConfig) { c.runner = r } }

// withVars gives the workspace the variables.
func withVars(vars map[string]string) option { return func(c *envConfig) { c.vars = vars } }

// withPoll changes the configuration of the poll.
func withPoll(f func(*Config)) option { return func(c *envConfig) { c.poll = f } }

// newEnv builds the environment and starts the jobs layer. The poll is not started: a test calls its Cycle.
func newEnv(t *testing.T, opts ...option) *env {
	t.Helper()
	cfg := &envConfig{runner: fakerunner.New(), workers: 2}
	for _, o := range opts {
		o(cfg)
	}
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
	fh.AddWorkspace(t, fakehub.Workspace{Slug: slug, PAT: pat, Vars: cfg.vars})
	if _, _, err := st.Upsert(ctx, slug, "af:", pat, "usr_1", "usr_1", epoch); err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	clk := fakeclock.New(epoch)

	jc := jobs.Config{
		DB: st.DB(), Subscriptions: st, Logger: logger, Clock: clk, Workers: cfg.workers,
		AttemptsDir: filepath.Join(dir, "attempts"), Runner: cfg.runner,
		PollInterval: 5 * time.Millisecond, RetryDelay: time.Millisecond, Retain: 100 * 365 * 24 * time.Hour,
	}
	if cfg.jobs != nil {
		cfg.jobs(&jc)
	}
	svc, err := jobs.New(jc)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cfg.runner.ReleaseAll()
		svc.Close()
	})

	client := hub.New(fh.URL, "test")
	pc := Config{
		Subscriptions: st, Hub: client, Jobs: svc, DB: st.DB(), Logger: logger, Clock: clk,
		FirstDelay: func() time.Duration { return 0 },
	}
	if cfg.poll != nil {
		cfg.poll(&pc)
	}
	return &env{t: t, Hub: fh, Store: st, DB: st.DB(), Jobs: svc, Runner: cfg.runner, Clock: clk, Logs: logs, Dir: dir, Poll: New(pc), Client: client}
}

// setVar sets a variable of the workspace on the hub.
func (e *env) setVar(name, value string) {
	e.t.Helper()
	e.Hub.UpdateWorkspace(slug, func(w *fakehub.Workspace) {
		vars := map[string]string{}
		for k, v := range w.Vars {
			vars[k] = v
		}
		if value == "" {
			delete(vars, name)
		} else {
			vars[name] = value
		}
		w.Vars = vars
	})
}

// fromPoll are the requests that hub received with the token of the workspace, in order, as the route that was asked
// for: "vars", "workspace", "issues(<label>)", "remove(<issue>,<label>)", and the path of anything else.
func (e *env) fromPoll() []string {
	var out []string
	for _, r := range e.Hub.RequestsFor(fakehub.TokenHash(pat)) {
		out = append(out, route(r))
	}
	return out
}

// route names a request that the poll made.
func route(r fakehub.Request) string {
	base := "/api/v1/workspaces/" + slug
	switch {
	case r.Method == "GET" && r.Path == base+"/vars/resolved":
		return "vars"
	case r.Method == "GET" && r.Path == base:
		return "workspace"
	case r.Method == "GET" && r.Path == base+"/issues":
		i := strings.Index(r.URI, "labels=")
		label := r.URI[i+len("labels="):]
		label, _, _ = strings.Cut(label, "&")
		return "issues(" + strings.ReplaceAll(label, "%3A", ":") + ")"
	case r.Method == "DELETE" && strings.HasPrefix(r.Path, base+"/issues/") && strings.Contains(r.Path, "/labels/"):
		rest := strings.TrimPrefix(r.Path, base+"/issues/")
		n, label, _ := strings.Cut(rest, "/labels/")
		return "remove(" + n + "," + label + ")"
	}
	return r.Method + " " + r.Path
}

// cycle runs one cycle of the workspace and returns when the next is due.
func (e *env) cycle() time.Duration {
	e.t.Helper()
	next, stop := e.Poll.Cycle(ctx, slug)
	if stop {
		e.t.Fatal("the cycle stopped the loop")
	}
	return next
}

// jobList are the jobs of the workspace, oldest first.
func (e *env) jobList() []jobRow {
	e.t.Helper()
	rows, err := e.DB.QueryContext(ctx, "SELECT id, kind, issue, source_kind, source_by, COALESCE(request_label, ''), state FROM af_jobs WHERE workspace = ? ORDER BY created_at, rowid", slug)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []jobRow
	for rows.Next() {
		var j jobRow
		if err := rows.Scan(&j.ID, &j.Kind, &j.Issue, &j.SourceKind, &j.SourceBy, &j.RequestLabel, &j.State); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, j)
	}
	return out
}

// jobRow is a row of af_jobs.
type jobRow struct {
	ID, Kind                                  string
	Issue                                     sql.NullInt64
	SourceKind, SourceBy, RequestLabel, State string
}
