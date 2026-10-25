package jobs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

var ctx = context.Background()

// epoch is the time the clock of every service of the tests starts at.
var epoch = time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)

// stamp is a time as the layer stores and shows it.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// noPurge is a retention that keeps every job for the length of a test.
const noPurge = 100 * 365 * 24 * time.Hour

// sandboxFailure is a failure of the infrastructure, which a runner marks transient.
var sandboxFailure = &toolio.Failure{Category: toolio.CategorySandbox, Message: "no box", Retryable: true}

// subscriptions are the subscriptions of a test: a workspace is subscribed with the state of its token, and fail makes
// every read fail.
type subscriptions struct {
	mu    sync.Mutex
	state map[string]string
	fail  error
}

func (s *subscriptions) Get(_ context.Context, slug string) (store.Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return store.Subscription{}, s.fail
	}
	state, ok := s.state[slug]
	if !ok {
		return store.Subscription{}, fmt.Errorf("%w: %s", store.ErrNotSubscribed, slug)
	}
	return store.Subscription{Slug: slug, TokenState: state}, nil
}

// set subscribes the workspace with the state of its token, or unsubscribes it for "".
func (s *subscriptions) set(slug, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if state == "" {
		delete(s.state, slug)
	} else {
		s.state[slug] = state
	}
}

// env is a service under test over a state file of its own, with a scripted runner, a fake clock and a log. The
// workspaces widgets and gadgets are subscribed.
type env struct {
	t      *testing.T
	DB     *sql.DB
	Subs   *subscriptions
	Svc    *jobs.Service
	Store  *jobs.Store
	Runner *fakerunner.Runner
	Clock  *fakeclock.Clock
	Log    *testutil.Log
	Dir    string
	cfg    jobs.Config
	closed bool
	gates  []chan struct{}
}

type option func(*jobs.Config)

func withRunner(r jobs.Runner) option   { return func(c *jobs.Config) { c.Runner = r } }
func withWorkers(n int) option          { return func(c *jobs.Config) { c.Workers = n } }
func withRetain(d time.Duration) option { return func(c *jobs.Config) { c.Retain = d } }
func withCleaner(cl jobs.Cleaner) option {
	return func(c *jobs.Config) { c.Cleaner = cl }
}
func withRemover(remove func(string) error) option {
	return func(c *jobs.Config) { c.RemoveAll = remove }
}
func withFault(fault func(op string) error) option {
	return func(c *jobs.Config) { c.StoreFault = fault }
}

// withoutEnqueue leaves a job that is made out of the queue, for the test or the sweep to put it there.
func withoutEnqueue() option {
	return func(c *jobs.Config) { c.Enqueue = func(context.Context, string, int) error { return nil } }
}

// newEnv builds the service, which is not started. Its runner is a fakerunner that returns done envelopes unless an
// option gives another.
func newEnv(t *testing.T, opts ...option) *env {
	t.Helper()
	dir := t.TempDir()
	key, err := secrets.NewKey(testutil.Key())
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, filepath.Join(dir, "agent-fox.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &env{
		t: t, DB: st.DB(), Clock: fakeclock.New(epoch), Log: &testutil.Log{}, Dir: filepath.Join(dir, "attempts"),
		Subs: &subscriptions{state: map[string]string{"widgets": store.TokenOK, "gadgets": store.TokenOK}},
	}
	e.Store = jobs.NewStore(e.DB, e.Clock.Now)
	e.cfg = jobs.Config{
		DB: e.DB, Subscriptions: e.Subs, Logger: e.Log.Logger(), Clock: e.Clock, Workers: 2, AttemptsDir: e.Dir,
		Runner: fakerunner.New(), PollInterval: 5 * time.Millisecond, RetryDelay: time.Millisecond, Retain: noPurge,
	}
	for _, o := range opts {
		o(&e.cfg)
	}
	e.build()
	return e
}

// build makes the service of the configuration, closed when the test ends.
func (e *env) build() {
	e.t.Helper()
	svc, err := jobs.New(e.cfg)
	if err != nil {
		e.t.Fatal(err)
	}
	e.Svc, e.closed = svc, false
	e.Runner, _ = e.cfg.Runner.(*fakerunner.Runner)
	r := e.Runner
	e.t.Cleanup(func() {
		for _, g := range e.gates { // so that a test that fails while a runner waits on one does not hang
			select {
			case <-g:
			default:
				close(g)
			}
		}
		if r != nil {
			r.ReleaseAll()
		}
		if err := svc.Close(); err != nil {
			e.t.Errorf("Close: %v", err)
		}
	})
}

// close stops the service, as the end of the process does.
func (e *env) close() {
	e.t.Helper()
	e.closed = true
	if err := e.Svc.Close(); err != nil {
		e.t.Fatal(err)
	}
}

// openAtEnd makes the channels, which runners wait on, closed when the test ends if the test has not closed them.
func (e *env) openAtEnd(gates ...chan struct{}) { e.gates = append(e.gates, gates...) }

// start starts the service.
func (e *env) start() {
	e.t.Helper()
	if err := e.Svc.Start(); err != nil {
		e.t.Fatal(err)
	}
}

// restart closes the service and starts another on the same state file, as a new process does, with the runner and
// the options.
func (e *env) restart(r jobs.Runner, opts ...option) {
	e.t.Helper()
	if !e.closed {
		e.close()
	}
	e.cfg.Runner = r
	for _, o := range opts {
		o(&e.cfg)
	}
	e.build()
	e.start()
}

// create makes a fix job for the issue on the workspace, through the API when label is "", or from the label poll
// with the request label.
func (e *env) create(workspace string, issue int, label string) jobs.Doc {
	e.t.Helper()
	req := jobs.CreateRequest{Workspace: workspace, Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage(fmt.Sprint(issue))},
		Source: jobs.Source{Kind: "api", By: "user-1"}}
	if label != "" {
		req.Source, req.RequestLabel = jobs.Source{Kind: "label", By: "poll"}, label
	}
	doc, err := e.Svc.Create(ctx, req)
	if err != nil {
		e.t.Fatalf("create %s#%d: %v", workspace, issue, err)
	}
	return doc
}

// get is the document of the job with its attempts.
func (e *env) get(id string) jobs.Doc {
	e.t.Helper()
	doc, err := e.Svc.Get(ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return doc
}

// waitState waits until the job is in the state, and returns it.
func (e *env) waitState(id string, state jobs.State) jobs.Doc {
	e.t.Helper()
	var doc jobs.Doc
	testutil.Eventually(e.t, id+" to be "+string(state), func() bool { doc = e.get(id); return doc.State == state })
	return doc
}

// atRest reports whether the job is blocked or finished and none of its attempts is queued or running.
func atRest(doc jobs.Doc) bool {
	if doc.State.Open() && doc.State != jobs.StateBlocked {
		return false
	}
	for _, a := range doc.Attempts {
		if a.State == jobs.AttemptRunning || a.State == jobs.AttemptQueued {
			return false
		}
	}
	return true
}

// waitRest waits until the job is at rest, and returns it.
func (e *env) waitRest(id string) jobs.Doc {
	e.t.Helper()
	var doc jobs.Doc
	testutil.Eventually(e.t, id+" to be at rest", func() bool { doc = e.get(id); return atRest(doc) })
	return doc
}

// waitCalls waits until the runner has had n calls.
func (e *env) waitCalls(n int) {
	e.t.Helper()
	testutil.Eventually(e.t, fmt.Sprintf("%d calls of the runner", n), func() bool { return len(e.Runner.Calls()) >= n })
}

// waitCall waits until the runner has been called for the job.
func (e *env) waitCall(id string) {
	e.t.Helper()
	testutil.Eventually(e.t, "a call for "+id, func() bool {
		return slices.ContainsFunc(e.Runner.Calls(), func(c fakerunner.Call) bool { return c.JobID == id })
	})
}

// waitWaits waits until the clock has been asked for n waits.
func (e *env) waitWaits(n int) {
	e.t.Helper()
	testutil.Eventually(e.t, fmt.Sprintf("%d waits on the clock", n), func() bool { return len(e.Clock.Waits()) >= n })
}

// events are the stored events of the job.
func (e *env) events(id string) []jobs.Event {
	e.t.Helper()
	evs, err := e.Store.EventsAfter(ctx, id, 0, 1<<20)
	if err != nil {
		e.t.Fatal(err)
	}
	return evs
}

// states are the job_state events of the job, as state and whether it says at rest.
func (e *env) states(id string) []string {
	e.t.Helper()
	var out []string
	for _, ev := range e.events(id) {
		if ev.Type == jobs.EventJobState {
			data := testutil.Decode[struct {
				State  string `json:"state"`
				AtRest bool   `json:"at_rest"`
			}](e.t, ev.Data)
			out = append(out, fmt.Sprintf("%s:%v", data.State, data.AtRest))
		}
	}
	return out
}

// lastState is the last job_state of the job, as states has it.
func (e *env) lastState(id string) string {
	e.t.Helper()
	s := e.states(id)
	if len(s) == 0 {
		e.t.Fatalf("%s has no job_state", id)
	}
	return s[len(s)-1]
}

// file is the path of a file of an attempt.
func (e *env) file(id string, n int, name string) string {
	return filepath.Join(e.Dir, id, fmt.Sprint(n), name)
}

// cleanup is the job's cleanup as the state file holds it, "" for none.
func (e *env) cleanup(id string) string {
	e.t.Helper()
	return testutil.Column(e.t, e.DB, "SELECT cleanup FROM af_jobs WHERE id = ?", id)[0]
}

// snapshot is what a late report must not change: the document of the job, its events and the files of its attempts.
func (e *env) snapshot(id string) string {
	e.t.Helper()
	doc := e.get(id)
	out := string(testutil.JSON(e.t, doc))
	for _, ev := range e.events(id) {
		out += fmt.Sprintf("|%d:%s:%s", ev.Seq, ev.Type, ev.Data)
	}
	return out + fmt.Sprint(testutil.Files(e.t, filepath.Join(e.Dir, id)))
}

// question is a needs_human step with the question "Which?" and three options.
func question() fakerunner.Step {
	return fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which?", "one", "two", "three")))
}

// failedBrain is a failed envelope of the brain, which the layer tries again when it is retryable, in a round of max.
func failedBrain(category string, retryable bool, max int) fakerunner.Step {
	return fakerunner.Return(jobs.Result{Envelope: fakerunner.Env(toolio.StatusFailed, fakerunner.WithError(category, "gave up", retryable)), MaxAttempts: max})
}

func ptr[T any](v T) *T { return &v }

// fakeCleaner is a cleaner of the cancel cleanup that a test scripts: it records every try and answers what result
// says, done with the request label put back when result is nil. A try waits for hold to be closed when hold is set,
// and gives up pending when its context ends first.
type fakeCleaner struct {
	mu     sync.Mutex
	tries  []jobs.CleanupRequest
	result func(jobs.CleanupRequest) jobs.CleanupResult
	hold   chan struct{}
	active int
	most   int
}

func (c *fakeCleaner) Clean(ctx context.Context, req jobs.CleanupRequest) jobs.CleanupResult {
	c.mu.Lock()
	c.tries = append(c.tries, req)
	c.active++
	c.most = max(c.most, c.active)
	hold, result := c.hold, c.result
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.active--; c.mu.Unlock() }()
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return jobs.CleanupResult{Outcome: jobs.CleanupPending, Level: 0, Message: context.Cause(ctx).Error()}
		}
	}
	if result == nil {
		return jobs.CleanupResult{Outcome: jobs.CleanupDone, RequestLabelOn: req.RequestLabel != ""}
	}
	return result(req)
}

// Tries are the tries made so far.
func (c *fakeCleaner) Tries() []jobs.CleanupRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.tries)
}

// Most is the most tries there have been at once.
func (c *fakeCleaner) Most() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.most
}

// perIssue is a step that does, for each call, the step of the issue of the call's job, so that a script does not
// depend on the order in which the jobs of several workspaces are claimed.
func perIssue(steps map[int]fakerunner.Step) fakerunner.Step {
	return fakerunner.Do(func(ctx context.Context, req jobs.Request) (jobs.Result, error) {
		var n int
		_ = json.Unmarshal(req.Input.Issue, &n)
		return fakerunner.New(steps[n]).Run(ctx, req)
	})
}
