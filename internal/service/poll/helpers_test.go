package poll

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

var ctx = context.Background()

// The workspace the poll works on in the tests, and its token.
const (
	slug     = "widgets"
	pat      = "pat-POLL-0123456789"
	wsPath   = "/api/v1/workspaces/" + slug
	varsPath = wsPath + "/vars/resolved"
	listPath = wsPath + "/issues"
)

var epoch = time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)

func issuePath(n int) string { return fmt.Sprintf("%s/issues/%d", wsPath, n) }

func removalPath(n int, label string) string { return fmt.Sprintf("%s/labels/%s", issuePath(n), label) }

// faults are the errors that the methods of a fake return, by method name, and how often each was called.
type faults struct {
	mu    sync.Mutex
	errs  map[string]error
	calls map[string]int
}

func (f *faults) fail(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errs == nil {
		f.errs = map[string]error{}
	}
	f.errs[method] = err
}

func (f *faults) call(method string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[method]++
	return f.errs[method]
}

func (f *faults) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

// subs are the subscriptions of the store, with faults.
type subs struct {
	Subscriptions
	faults
}

func (s *subs) List(ctx context.Context) ([]store.Subscription, error) {
	if err := s.call("List"); err != nil {
		return nil, err
	}
	return s.Subscriptions.List(ctx)
}

func (s *subs) Token(ctx context.Context, slug string) (string, error) {
	if err := s.call("Token"); err != nil {
		return "", err
	}
	return s.Subscriptions.Token(ctx, slug)
}

// fakeJobs is a jobs layer of documents in memory: a job is open while it is queued, running or blocked, an answer
// takes a blocked job and a retry a failed one to queued, and a purged job is not found.
type fakeJobs struct {
	faults
	mu       sync.Mutex
	docs     []*jobs.Doc
	standing map[string]jobs.Standing
	creates  []jobs.CreateRequest
	answers  []string
	retries  [][]string
}

// with calls the method, and hands use the last job that matches, nil when none does, under the lock.
func with[T any](f *fakeJobs, method string, match func(*jobs.Doc) bool, use func(*jobs.Doc) (T, error)) (T, error) {
	if err := f.call(method); err != nil {
		var zero T
		return zero, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.docs) - 1; i >= 0; i-- {
		if match(f.docs[i]) {
			return use(f.docs[i])
		}
	}
	return use(nil)
}

func found(d *jobs.Doc) (jobs.Doc, error) {
	if d == nil {
		return jobs.Doc{}, jobs.ErrNotFound
	}
	return *d, nil
}

func byID(id string) func(*jobs.Doc) bool { return func(d *jobs.Doc) bool { return d.ID == id } }

func of(kind jobs.Kind, issue int64, open bool) func(*jobs.Doc) bool {
	return func(d *jobs.Doc) bool {
		return d.Kind == kind && *d.Issue == issue && (!open || d.State == jobs.StateQueued || d.State == jobs.StateRunning || d.State == jobs.StateBlocked)
	}
}

// add makes a job of the kind for the issue in the state, with the request label, and returns its id.
func (f *fakeJobs) add(kind jobs.Kind, issue int, state jobs.State, label string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.put(kind, int64(issue), state, label).ID
}

func (f *fakeJobs) put(kind jobs.Kind, issue int64, state jobs.State, label string) *jobs.Doc {
	finished := epoch.Format(time.RFC3339Nano)
	d := &jobs.Doc{ID: "job_" + strconv.Itoa(len(f.docs)+1), Workspace: slug, Kind: kind, Issue: &issue, State: state, CurrentAttempt: 1,
		Attempts: []jobs.AttemptDoc{{N: 1, FinishedAt: &finished}}}
	if label != "" {
		d.RequestLabel = &label
	}
	f.docs = append(f.docs, d)
	return d
}

// set changes the state of the job and how it stands; a nil standing purges the job.
func (f *fakeJobs) set(id string, state jobs.State, st *jobs.Standing) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if st == nil {
		f.docs = slices.DeleteFunc(f.docs, byID(id))
		return
	}
	f.docs[slices.IndexFunc(f.docs, byID(id))].State = state
	if f.standing == nil {
		f.standing = map[string]jobs.Standing{}
	}
	f.standing[id] = *st
}

func (f *fakeJobs) Create(_ context.Context, req jobs.CreateRequest) (jobs.Doc, error) {
	var issue int64
	_ = json.Unmarshal(req.Body.Issue, &issue)
	return with(f, "Create", of(req.Kind, issue, true), func(d *jobs.Doc) (jobs.Doc, error) {
		if d != nil {
			return jobs.Doc{}, &jobs.DuplicateError{JobID: d.ID}
		}
		f.creates = append(f.creates, req)
		return *f.put(req.Kind, issue, jobs.StateQueued, req.RequestLabel), nil
	})
}

func (f *fakeJobs) OpenJob(_ context.Context, _ string, kind jobs.Kind, issue int64) (jobs.Doc, error) {
	return with(f, "OpenJob", of(kind, issue, true), found)
}

func (f *fakeJobs) LatestJob(_ context.Context, _ string, kind jobs.Kind, issue int64) (jobs.Doc, error) {
	return with(f, "LatestJob", of(kind, issue, false), found)
}

func (f *fakeJobs) Get(_ context.Context, id string) (jobs.Doc, error) {
	return with(f, "Get", byID(id), found)
}

func (f *fakeJobs) Standing(_ context.Context, id string) (jobs.Standing, error) {
	return with(f, "Standing", byID(id), func(d *jobs.Doc) (jobs.Standing, error) {
		if d == nil {
			return jobs.Standing{}, jobs.ErrNotFound
		}
		st := f.standing[id]
		st.State = d.State
		return st, nil
	})
}

// again moves the job from the state to queued with a new attempt, or refuses as the jobs layer does.
func (f *fakeJobs) again(method, id string, from jobs.State, note func()) (jobs.Doc, error) {
	return with(f, method, byID(id), func(d *jobs.Doc) (jobs.Doc, error) {
		if d.State != from {
			return jobs.Doc{}, &jobs.StateError{State: d.State}
		}
		note()
		d.State, d.CurrentAttempt = jobs.StateQueued, d.CurrentAttempt+1
		return *d, nil
	})
}

func (f *fakeJobs) Answer(_ context.Context, id, _ string, req jobs.AnswerRequest) (jobs.Doc, error) {
	return f.again("Answer", id, jobs.StateBlocked, func() { f.answers = append(f.answers, *req.Answer) })
}

func (f *fakeJobs) RetryWithContext(_ context.Context, id, _ string, notes []string) (jobs.Doc, error) {
	return f.again("Retry", id, jobs.StateFailed, func() { f.retries = append(f.retries, notes) })
}

// env is the poll under test: the store over a state file, the fake jobs layer, a fake hub that has the workspace
// widgets, and a clock that stands still until the test moves it. The poll is not started: a test calls Cycle.
type env struct {
	t     *testing.T
	Hub   *fakehub.Hub
	Store *store.Store
	DB    *sql.DB
	Subs  *subs
	Jobs  *fakeJobs
	Clock *fakeclock.Clock
	Log   *testutil.Log
	Poll  *Supervisor
}

type option func(*env, *Config)

func withVars(vars map[string]string) option {
	return func(e *env, _ *Config) { e.Hub.UpdateWorkspace(slug, func(w *fakehub.Workspace) { w.Vars = vars }) }
}

func openStore(t testing.TB, path string) *store.Store {
	t.Helper()
	key, err := secrets.NewKey(testutil.Key())
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, path, key)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func newEnv(t *testing.T, opts ...option) *env {
	t.Helper()
	st := openStore(t, filepath.Join(t.TempDir(), "agent-fox.db"))
	t.Cleanup(func() { st.Close() })
	e := &env{t: t, Hub: fakehub.New(t), Store: st, DB: st.DB(), Subs: &subs{Subscriptions: st}, Jobs: &fakeJobs{},
		Clock: fakeclock.New(epoch), Log: &testutil.Log{}}
	e.addWorkspace(slug, pat)
	cfg := Config{Subscriptions: e.Subs, Hub: hub.New(e.Hub.URL, "test"), Jobs: e.Jobs, DB: e.DB, Logger: e.Log.Logger(),
		Clock: e.Clock, FirstDelay: func() time.Duration { return 0 }}
	for _, o := range opts {
		o(e, &cfg)
	}
	e.Poll = New(cfg)
	return e
}

// addWorkspace makes the workspace known to hub with its token, and subscribes it.
func (e *env) addWorkspace(workspace, token string) {
	e.t.Helper()
	e.Hub.AddWorkspace(e.t, fakehub.Workspace{Slug: workspace, PAT: token})
	if _, _, err := e.Store.Upsert(ctx, workspace, token, "usr_1", "usr_1", epoch); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) cycle() time.Duration {
	e.t.Helper()
	next, stop := e.Poll.Cycle(ctx, slug)
	if stop {
		e.t.Fatal("the cycle stopped the loop")
	}
	return next
}

func (e *env) issue(n int, labels ...string) {
	e.Hub.AddIssue(slug, fakehub.Issue{Number: n, Labels: labels})
}

func (e *env) labels(n int) []string {
	issue, _ := e.Hub.IssueOf(slug, n)
	return issue.Labels
}

func (e *env) comment(n int, login, body string) { e.Hub.AddPersonComment(slug, n, login, body) }

func (e *env) comments(n int) []string {
	issue, _ := e.Hub.IssueOf(slug, n)
	var out []string
	for _, c := range issue.Comments {
		out = append(out, c.Body)
	}
	return out
}

// routes are the requests the poll made of hub, in order: "vars", "workspace", "issues(<label>)",
// "remove(<issue>,<label>)", "read(<issue>)", and the method and path of anything else.
func (e *env) routes() []string {
	var out []string
	for _, r := range e.Hub.RequestsFor(fakehub.TokenHash(pat)) {
		rest, _ := strings.CutPrefix(r.Path, wsPath)
		var n int
		var label string
		switch {
		case r.Method == http.MethodGet && rest == "/vars/resolved":
			out = append(out, "vars")
		case r.Method == http.MethodGet && rest == "":
			out = append(out, "workspace")
		case r.Method == http.MethodGet && rest == "/issues":
			label, _, _ = strings.Cut(strings.SplitN(r.URI, "labels=", 2)[1], "&")
			out = append(out, "issues("+strings.ReplaceAll(label, "%3A", ":")+")")
		case r.Method == http.MethodDelete:
			_, _ = fmt.Sscanf(strings.ReplaceAll(rest, "/", " "), " issues %d labels %s", &n, &label)
			out = append(out, fmt.Sprintf("remove(%d,%s)", n, label))
		case r.Method == http.MethodGet && strings.HasPrefix(rest, "/issues/"):
			out = append(out, "read("+strings.TrimPrefix(rest, "/issues/")+")")
		default:
			out = append(out, r.Method+" "+r.Path)
		}
	}
	return out
}

func (e *env) removals() []string {
	return slices.DeleteFunc(e.routes(), func(r string) bool { return !strings.HasPrefix(r, "remove(") })
}

func (e *env) records() []Record {
	e.t.Helper()
	got, err := NewRecords(e.DB).ForWorkspace(ctx, slug)
	if err != nil {
		e.t.Fatal(err)
	}
	return got
}

// record writes a record of the poll for the workspace, made now and open when it says nothing else.
func (e *env) record(r Record) Record {
	e.t.Helper()
	r.Workspace = slug
	if r.CreatedAt.IsZero() {
		r.CreatedAt = e.Clock.Now()
	}
	out, err := NewRecords(e.DB).Insert(ctx, r)
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

// onList calls f once, when the poll asks hub for the list of the label.
func (e *env) onList(label string, f func()) {
	var once sync.Once
	e.Hub.OnRequest(func(r fakehub.Request) {
		if r.Path == listPath && strings.Contains(r.URI, "labels="+strings.ReplaceAll(label, ":", "%3A")) {
			once.Do(f)
		}
	})
}

// failRecordWrites makes the statements of the kinds (INSERT, UPDATE, DELETE) on the records fail.
func (e *env) failRecordWrites(ops ...string) {
	for _, op := range ops {
		testutil.Exec(e.t, e.DB, "CREATE TRIGGER refuse_"+op+" BEFORE "+op+" ON af_poll_requests BEGIN SELECT RAISE(ABORT, 'read only'); END")
	}
}

// marker is the marker of the job's first attempt that a comment of the worker carries.
func marker(id, kind string) string {
	return fmt.Sprintf("<!-- agent-fox job=%s attempt=1 kind=%s -->", id, kind)
}

// waiting makes a fix job for the issue in the state (blocked or failed) with the label af:fix as its request label,
// and the worker's comment that ends the attempt, and returns its id.
func (e *env) waiting(n int, state jobs.State) string {
	if _, ok := e.Hub.IssueOf(slug, n); !ok {
		e.issue(n)
	}
	id := e.Jobs.add(jobs.KindFix, n, state, "af:fix")
	kind := "question"
	if state == jobs.StateFailed {
		kind = "failure"
	}
	e.comment(n, "agent-fox", "Which?\n\n"+marker(id, kind))
	return id
}

// threadBody is hub's read of the issue as the fake holds it, cut or with comments it could not read.
func (e *env) threadBody(n int, truncated, commentsErr bool) string {
	issue, _ := e.Hub.IssueOf(slug, n)
	comments := []map[string]any{}
	for _, c := range issue.Comments {
		comments = append(comments, map[string]any{"id": c.ID, "body": c.Body, "user": map[string]any{"login": c.Author}, "created_at": c.CreatedAt})
	}
	out := map[string]any{"issue": map[string]any{"number": n, "state": "open", "labels": issue.Labels}, "comments": comments, "truncated": truncated}
	if commentsErr {
		out["comments_err"], out["comments"] = map[string]any{}, []any{}
	}
	return string(testutil.JSON(e.t, out))
}
