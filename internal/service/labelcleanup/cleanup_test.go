package labelcleanup_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/api"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/service/labelcleanup"
	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

var ctx = context.Background()

// The caller of the tests and the token of widgets, as hub knows them.
const (
	callerToken = "caller-token"
	callerID    = "user-1"
	pat         = "pat-of-widgets-0123456789"
)

var epoch = time.Date(2026, 10, 23, 8, 0, 0, 0, time.UTC)

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

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// records are the log records of the level whose message contains the text.
func (s *syncBuffer) records(level, text string) []map[string]any {
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s.String()), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) == nil && rec["level"] == level && strings.Contains(fmt.Sprint(rec["msg"]), text) {
			out = append(out, rec)
		}
	}
	return out
}

// env is the service as cmd/agent-fox builds it for the cleanup: the real store over a file, the real jobs layer with the
// hub-backed cleaner, the real API server, a fake runner and a fake clock, and a fake hub with the workspace widgets.
type env struct {
	t      *testing.T
	Hub    *fakehub.Hub
	Store  *store.Store
	Svc    *jobs.Service
	Runner *fakerunner.Runner
	Clock  *fakeclock.Clock
	Logs   *syncBuffer
	URL    string
	dir    string
	key    secrets.Key
	client *hub.Client
}

// newEnv starts the service; the runner does the steps.
func newEnv(t *testing.T, steps ...fakerunner.Step) *env {
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
	fh.AddWorkspace(t, fakehub.Workspace{Slug: "widgets", PAT: pat})
	if _, _, err := st.Upsert(ctx, "widgets", "af:", pat, "", callerID, time.Now()); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, Hub: fh, Store: st, Runner: fakerunner.New(steps...), Clock: fakeclock.New(epoch), Logs: &syncBuffer{}, dir: dir, key: key, client: hub.New(fh.URL, "test")}
	e.Svc = e.service(e.Runner)
	srv := api.NewServer(api.Deps{Store: st, Hub: e.client, Logger: e.logger(), Jobs: e.Svc, Version: (&toolio.App{Name: "agent-fox", Version: "1.2.3"}).VersionDocument()})
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	e.URL = ts.URL
	t.Cleanup(e.Runner.ReleaseAll)
	return e
}

func (e *env) logger() *slog.Logger { return slog.New(slog.NewJSONHandler(e.Logs, nil)) }

// service builds a jobs layer over the state file with the hub-backed cleaner, as cmd/agent-fox does.
func (e *env) service(r jobs.Runner) *jobs.Service {
	e.t.Helper()
	svc, err := jobs.New(jobs.Config{
		DB: e.Store.DB(), Subscriptions: e.Store, Logger: e.logger(), Clock: e.Clock,
		Workers: 2, AttemptsDir: filepath.Join(e.dir, "attempts"), Runner: r,
		PollInterval: 5 * time.Millisecond, RetryDelay: time.Millisecond, Retain: 100 * 365 * 24 * time.Hour,
		Cleaner: &labelcleanup.Cleaner{Subscriptions: e.Store, Hub: e.client},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { svc.Close() })
	return svc
}

// start starts the workers.
func (e *env) start() {
	e.t.Helper()
	if err := e.Svc.Start(); err != nil {
		e.t.Fatal(err)
	}
}

// issue adds issue n to widgets with the labels.
func (e *env) issue(n int, labels ...string) {
	e.Hub.AddIssue("widgets", fakehub.Issue{Number: n, Title: fmt.Sprintf("issue %d", n), Labels: labels})
}

// labelsOf are the labels issue n carries now, sorted.
func (e *env) labelsOf(n int) []string {
	i, ok := e.Hub.IssueOf("widgets", n)
	if !ok {
		e.t.Fatalf("no issue %d", n)
	}
	return slices.Sorted(slices.Values(i.Labels))
}

// fromLabel makes a job of the kind for issue n, as the poll does for the label.
func (e *env) fromLabel(kind jobs.Kind, n int) jobs.Doc {
	e.t.Helper()
	doc, err := e.Svc.Create(ctx, jobs.CreateRequest{
		Workspace: "widgets", Kind: kind, Body: jobs.Body{Issue: json.RawMessage(fmt.Sprint(n))},
		Source: jobs.Source{Kind: "label", By: "label af:" + string(kind)}, RequestLabel: "af:" + string(kind),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return doc
}

// fromAPI makes a job of the kind for issue n through the API.
func (e *env) fromAPI(kind jobs.Kind, n int) jobs.Doc {
	e.t.Helper()
	r := e.call("POST", "/api/v1/workspaces/widgets/"+string(kind), map[string]any{"issue": n})
	if r.Status != http.StatusAccepted {
		e.t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	return r.doc(e.t)
}

// needsHuman is a step that ends the attempt blocked with needs-human set, as the worker reports it.
func needsHuman() fakerunner.Step {
	return fakerunner.Return(jobs.Result{
		Envelope:  fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which?", "a")),
		LabelsSet: []string{"af:needs-human"}, LabelsRemoved: []string{"af:in-progress"},
	})
}

// response is what a request got.
type response struct {
	Status int
	Body   []byte
}

func (r response) doc(t *testing.T) jobs.Doc {
	t.Helper()
	var d jobs.Doc
	if err := json.Unmarshal(r.Body, &d); err != nil {
		t.Fatalf("not a job (%d): %s", r.Status, r.Body)
	}
	return d
}

// call makes a request as the caller.
func (e *env) call(method, path string, body any) response {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.URL+path, reader)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+callerToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return response{Status: res.StatusCode, Body: raw}
}

// cancel cancels the job through the API, and returns what the route answered.
func (e *env) cancel(id string) jobs.Doc {
	e.t.Helper()
	r := e.call("POST", "/api/v1/jobs/"+id+"/cancel", nil)
	if r.Status != http.StatusOK {
		e.t.Fatalf("cancel: %d %s", r.Status, r.Body)
	}
	return r.doc(e.t)
}

// get is the job now.
func (e *env) get(id string) jobs.Doc {
	e.t.Helper()
	d, err := e.Svc.Get(ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return d
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

// waitState waits for the job to be in the state.
func (e *env) waitState(id string, state jobs.State) jobs.Doc {
	e.t.Helper()
	var d jobs.Doc
	waitFor(e.t, id+" to be "+string(state), func() bool { d = e.get(id); return d.State == state })
	return d
}

// cleanupOf is labels.cleanup, "" when it is null.
func cleanupOf(d jobs.Doc) string {
	if d.Labels == nil || d.Labels.Cleanup == nil {
		return ""
	}
	return *d.Labels.Cleanup
}

// waitCleanup waits until the job's cleanup is no longer pending, and returns the job.
func (e *env) waitCleanup(id string) jobs.Doc {
	e.t.Helper()
	var d jobs.Doc
	waitFor(e.t, "the cleanup of "+id+" to end", func() bool { d = e.get(id); return cleanupOf(d) != "" && cleanupOf(d) != "pending" })
	return d
}

// labelWrites are the label writes hub received for issue n, as "POST [names]" or "DELETE name".
func (e *env) labelWrites(n int) []string {
	var out []string
	prefix := fmt.Sprintf("/api/v1/workspaces/widgets/issues/%d/labels", n)
	for _, r := range e.Hub.Requests() {
		switch {
		case r.Method == "POST" && r.Path == prefix:
			out = append(out, "POST "+strings.TrimSpace(string(r.Body)))
		case r.Method == "DELETE" && strings.HasPrefix(r.Path, prefix+"/"):
			out = append(out, "DELETE "+strings.TrimPrefix(r.Path, prefix+"/"))
		}
	}
	return out
}

// TestCancellingABlockedPollFixJobAnswersAtOnceThenTheIssueShowsAfFix_TS_17_27 verifies TS-17-27.
// Verifies: 17-REQ-5.1, 17-REQ-5.7
func TestCancellingABlockedPollFixJobAnswersAtOnceThenTheIssueShowsAfFix_TS_17_27(t *testing.T) {
	e := newEnv(t, needsHuman())
	e.issue(42, "af:needs-human")
	e.start()
	job := e.fromLabel(jobs.KindFix, 42)
	e.waitState(job.ID, jobs.StateBlocked)

	e.Hub.Delay("/api/v1/workspaces/widgets/issues/42", 300*time.Millisecond) // the read before any write is held
	answered := e.cancel(job.ID)
	if w := e.labelWrites(42); len(w) != 0 {
		t.Errorf("label writes before the answer: %v", w)
	}
	if cleanupOf(answered) != "pending" || answered.Labels.Restore == nil || *answered.Labels.Restore != "af:fix" {
		t.Fatalf("the answer shows labels %+v, want cleanup pending and restore af:fix", answered.Labels)
	}
	d := e.waitCleanup(job.ID)
	if got := e.labelsOf(42); !slices.Equal(got, []string{"af:fix"}) {
		t.Errorf("issue 42 carries %v, want af:fix and no status label", got)
	}
	raw, _ := json.Marshal(d.Labels)
	if string(raw) != `{"set":["af:fix"],"removed":["af:needs-human"],"restore":null,"cleanup":"done"}` {
		t.Errorf("labels = %s", raw)
	}
}

// TestOtherCancelsThatReachNoRunnerRemoveOnlyWhatTheJobAccountsFor_TS_17_28 verifies TS-17-28.
// Verifies: 17-REQ-5.1, 17-REQ-6.3
func TestOtherCancelsThatReachNoRunnerRemoveOnlyWhatTheJobAccountsFor_TS_17_28(t *testing.T) {
	t.Run("a queued fix keeps landed and ready", func(t *testing.T) {
		e := newEnv(t, fakerunner.Block(fakerunner.Done()))
		e.issue(1)
		e.issue(43, "af:landed", "af:ready")
		e.start()
		e.fromAPI(jobs.KindFix, 1) // holds the workspace
		waitFor(t, "the first call", func() bool { return len(e.Runner.Calls()) == 1 })
		job := e.fromLabel(jobs.KindFix, 43)
		e.cancel(job.ID)
		if d := e.waitCleanup(job.ID); cleanupOf(d) != "done" {
			t.Errorf("cleanup %s, want done", cleanupOf(d))
		}
		if got := e.labelsOf(43); !slices.Equal(got, []string{"af:fix", "af:landed", "af:ready"}) {
			t.Errorf("issue 43 carries %v", got)
		}
	})
	t.Run("a queued re-triage keeps ready", func(t *testing.T) {
		e := newEnv(t, fakerunner.Block(fakerunner.Done()))
		e.issue(1)
		e.issue(44, "af:ready")
		e.start()
		e.fromAPI(jobs.KindFix, 1)
		waitFor(t, "the first call", func() bool { return len(e.Runner.Calls()) == 1 })
		job := e.fromLabel(jobs.KindTriage, 44)
		e.cancel(job.ID)
		if d := e.waitCleanup(job.ID); cleanupOf(d) != "done" {
			t.Errorf("cleanup %s, want done", cleanupOf(d))
		}
		if got := e.labelsOf(44); !slices.Equal(got, []string{"af:ready", "af:triage"}) {
			t.Errorf("issue 44 carries %v", got)
		}
	})
	t.Run("a job waiting out a transient backoff", func(t *testing.T) {
		e := newEnv(t, fakerunner.TransientFailure(&toolio.Failure{Category: "sandbox", Message: "no box", Retryable: true}))
		e.issue(45)
		e.start()
		job := e.fromLabel(jobs.KindFix, 45)
		if !e.Clock.WaitForWaits(1, 10*time.Second) {
			t.Fatal("the attempt did not go into its backoff")
		}
		e.cancel(job.ID)
		if d := e.waitCleanup(job.ID); cleanupOf(d) != "done" {
			t.Errorf("cleanup %s, want done", cleanupOf(d))
		}
		if got := e.labelsOf(45); !slices.Equal(got, []string{"af:fix"}) {
			t.Errorf("issue 45 carries %v", got)
		}
	})
	t.Run("a failed job retried and cancelled while queued", func(t *testing.T) {
		failed := fakerunner.Return(jobs.Result{
			Envelope:  fakerunner.Env(toolio.StatusFailed, fakerunner.WithError("forge", "the forge said no", false)),
			LabelsSet: []string{"af:failed"}, LabelsRemoved: []string{"af:in-progress"},
		})
		e := newEnv(t, failed, fakerunner.Block(fakerunner.Done()))
		e.issue(1)
		e.issue(46, "af:failed")
		e.start()
		job := e.fromLabel(jobs.KindFix, 46)
		e.waitState(job.ID, jobs.StateFailed)
		e.fromAPI(jobs.KindFix, 1) // holds the workspace, so that the retry stays queued
		waitFor(t, "the second call", func() bool { return len(e.Runner.Calls()) == 2 })
		if r := e.call("POST", "/api/v1/jobs/"+job.ID+"/retry", nil); r.Status != http.StatusAccepted && r.Status != http.StatusOK {
			t.Fatalf("retry: %d %s", r.Status, r.Body)
		}
		e.cancel(job.ID)
		if d := e.waitCleanup(job.ID); cleanupOf(d) != "done" {
			t.Errorf("cleanup %s, want done", cleanupOf(d))
		}
		if got := e.labelsOf(46); !slices.Equal(got, []string{"af:fix"}) {
			t.Errorf("issue 46 carries %v, want af:failed removed and af:fix back", got)
		}
	})
	t.Run("a blocked API fix job", func(t *testing.T) {
		e := newEnv(t, needsHuman())
		e.issue(47, "af:needs-human")
		e.start()
		job := e.fromAPI(jobs.KindFix, 47)
		e.waitState(job.ID, jobs.StateBlocked)
		e.cancel(job.ID)
		if d := e.waitCleanup(job.ID); cleanupOf(d) != "done" {
			t.Errorf("cleanup %s, want done", cleanupOf(d))
		}
		if got := e.labelsOf(47); len(got) != 0 {
			t.Errorf("issue 47 carries %v, want needs-human gone and nothing added", got)
		}
		for _, w := range e.labelWrites(47) {
			if strings.HasPrefix(w, "POST") {
				t.Errorf("a label was added: %s", w)
			}
		}
	})
}

// TestAClaimedAttemptNotYetCalledAndAShutDownAttemptsInProgressAreCleanedUp_TS_17_29 verifies TS-17-29: a job whose attempt
// is running in the file and in no runner of this process (left by a shutdown, waiting to be handed over again, or claimed
// and not yet called) is cleaned up as a job that waited, and loses the in-progress the shut-down attempt left.
// Verifies: 17-REQ-5.1, 17-REQ-6.2
func TestAClaimedAttemptNotYetCalledAndAShutDownAttemptsInProgressAreCleanedUp_TS_17_29(t *testing.T) {
	t.Run("left by a shutdown", func(t *testing.T) {
		e := newEnv(t, fakerunner.Block(fakerunner.Done()))
		e.issue(42, "af:in-progress")
		e.start()
		job := e.fromLabel(jobs.KindFix, 42)
		waitFor(t, "the call", func() bool { return len(e.Runner.Calls()) == 1 })
		if err := e.Svc.Close(); err != nil {
			t.Fatal(err)
		}
		// The restarted layer has not handed the attempt over again yet.
		e.Svc = e.service(fakerunner.New())
		d, err := e.Svc.Cancel(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if cleanupOf(d) != "pending" {
			t.Fatalf("after the cancel: labels %+v, want cleanup pending", d.Labels)
		}
		if d := e.waitCleanup(job.ID); cleanupOf(d) != "done" {
			t.Errorf("cleanup %s, want done", cleanupOf(d))
		}
		if got := e.labelsOf(42); !slices.Equal(got, []string{"af:fix"}) {
			t.Errorf("issue 42 carries %v, want in-progress gone and af:fix back", got)
		}
	})
	t.Run("claimed and not yet called", func(t *testing.T) {
		e := newEnv(t)
		e.issue(43, "af:in-progress")
		job := e.fromLabel(jobs.KindFix, 43)
		// A claim moves the attempt and the job to running a moment before the runner is called.
		for _, q := range []string{"UPDATE af_attempts SET state = 'running' WHERE job_id = ?", "UPDATE af_jobs SET state = 'running' WHERE id = ?"} {
			if _, err := e.Store.DB().ExecContext(ctx, q, job.ID); err != nil {
				t.Fatal(err)
			}
		}
		d := e.cancel(job.ID)
		if cleanupOf(d) != "pending" {
			t.Fatalf("after the cancel: labels %+v, want cleanup pending", d.Labels)
		}
		if d := e.waitCleanup(job.ID); cleanupOf(d) != "done" {
			t.Errorf("cleanup %s, want done", cleanupOf(d))
		}
		if got := e.labelsOf(43); !slices.Equal(got, []string{"af:fix"}) {
			t.Errorf("issue 43 carries %v", got)
		}
	})
}

// TestATryReadsTheTokenTheRecordAndTheIssueBeforeAnyWrite_TS_17_36 verifies TS-17-36.
// Verifies: 17-REQ-6.1
func TestATryReadsTheTokenTheRecordAndTheIssueBeforeAnyWrite_TS_17_36(t *testing.T) {
	e := newEnv(t, needsHuman())
	e.issue(42, "af:needs-human")
	e.start()
	job := e.fromLabel(jobs.KindFix, 42)
	e.waitState(job.ID, jobs.StateBlocked)
	before := len(e.Hub.Requests())
	e.cancel(job.ID)
	e.waitCleanup(job.ID)
	var seen []string
	for _, r := range e.Hub.Requests()[before:] {
		if r.TokenHash != fakehub.TokenHash(pat) {
			continue // the caller's own requests
		}
		seen = append(seen, r.Method+" "+r.Path)
	}
	want := []string{
		"GET /api/v1/workspaces/widgets", "GET /api/v1/workspaces/widgets/issues/42",
		"DELETE /api/v1/workspaces/widgets/issues/42/labels/af:needs-human", "POST /api/v1/workspaces/widgets/issues/42/labels",
	}
	if !slices.Equal(seen, want) {
		t.Errorf("hub saw %v with the stored token, want %v", seen, want)
	}
	for _, r := range e.Hub.Requests()[before:] {
		if strings.Contains(r.Path, "/issues/42") && r.TokenHash != fakehub.TokenHash(pat) {
			t.Errorf("%s %s was not made with the stored token", r.Method, r.Path)
		}
	}
}

// TestInProgressIsRemovedUnlessAnotherJobOfTheIssueIsRunning_TS_17_37 verifies TS-17-37.
// Verifies: 17-REQ-6.2
func TestInProgressIsRemovedUnlessAnotherJobOfTheIssueIsRunning_TS_17_37(t *testing.T) {
	t.Run("another job runs", func(t *testing.T) {
		e := newEnv(t, fakerunner.Block(fakerunner.Done()))
		e.issue(42, "af:in-progress")
		e.start()
		e.fromAPI(jobs.KindFix, 42)
		waitFor(t, "the fix to run", func() bool { return len(e.Runner.Calls()) == 1 })
		triage := e.fromLabel(jobs.KindTriage, 42) // queued behind the running fix
		e.cancel(triage.ID)
		e.waitCleanup(triage.ID)
		if got := e.labelsOf(42); !slices.Equal(got, []string{"af:in-progress", "af:triage"}) {
			t.Errorf("issue 42 carries %v, want in-progress kept and af:triage back", got)
		}
	})
	t.Run("no other job runs", func(t *testing.T) {
		e := newEnv(t)
		e.issue(42, "af:in-progress")
		triage := e.fromLabel(jobs.KindTriage, 42)
		e.cancel(triage.ID)
		e.waitCleanup(triage.ID)
		if got := e.labelsOf(42); !slices.Equal(got, []string{"af:triage"}) {
			t.Errorf("issue 42 carries %v, want in-progress removed", got)
		}
	})
}

// TestAStatusLabelAnotherOpenJobAccountsForStays_TS_17_38 verifies TS-17-38.
// Verifies: 17-REQ-6.3
func TestAStatusLabelAnotherOpenJobAccountsForStays_TS_17_38(t *testing.T) {
	e := newEnv(t, needsHuman(), needsHuman())
	e.issue(42, "af:needs-human", "af:failed") // af:failed was set by a person
	e.start()
	triage := e.fromLabel(jobs.KindTriage, 42)
	e.waitState(triage.ID, jobs.StateBlocked)
	fix := e.fromLabel(jobs.KindFix, 42)
	e.waitState(fix.ID, jobs.StateBlocked)
	e.cancel(triage.ID)
	e.waitCleanup(triage.ID)
	if got := e.labelsOf(42); !slices.Equal(got, []string{"af:failed", "af:needs-human", "af:triage"}) {
		t.Errorf("issue 42 carries %v, want needs-human and the person's label kept and af:triage back", got)
	}
}

// TestTheRequestLabelIsAddedOnlyWhenMissing_TS_17_39 verifies TS-17-39.
// Verifies: 17-REQ-6.4
func TestTheRequestLabelIsAddedOnlyWhenMissing_TS_17_39(t *testing.T) {
	for _, back := range []bool{false, true} {
		t.Run(fmt.Sprintf("a person put it back: %v", back), func(t *testing.T) {
			e := newEnv(t)
			if back {
				e.issue(42, "af:fix")
			} else {
				e.issue(42)
			}
			job := e.fromLabel(jobs.KindFix, 42)
			e.cancel(job.ID)
			d := e.waitCleanup(job.ID)
			adds := 0
			for _, w := range e.labelWrites(42) {
				if strings.HasPrefix(w, "POST") {
					adds++
				}
			}
			if want := map[bool]int{false: 1, true: 0}[back]; adds != want {
				t.Errorf("%d adds, want %d", adds, want)
			}
			if d.Labels.Restore != nil || cleanupOf(d) != "done" {
				t.Errorf("labels %+v, want restore null and cleanup done", d.Labels)
			}
		})
	}
}

// TestATryWritesOnlyWhatIsMissingAndARepeatedTryWritesNothing_TS_17_40 verifies TS-17-40, a property: whatever labels the
// issue starts with and however often a try runs, the first complete try writes exactly the missing removals and the
// missing addition, and every try after it writes nothing.
// Verifies: 17-REQ-6.5
func TestATryWritesOnlyWhatIsMissingAndARepeatedTryWritesNothing_TS_17_40(t *testing.T) {
	e := newEnv(t)
	c := &labelcleanup.Cleaner{Subscriptions: e.Store, Hub: e.client}
	all := []string{"af:triage", "af:fix", "af:in-progress", "af:ready", "af:needs-human", "af:landed", "af:failed", "bug", "help wanted"}
	status := []string{"af:in-progress", "af:ready", "af:needs-human", "af:landed", "af:failed"}
	rng := rand.New(rand.NewPCG(17, 40))
	for round := range 60 {
		n := 100 + round
		var start, own, others []string
		for _, l := range all {
			if rng.IntN(2) == 0 {
				start = append(start, l)
			}
		}
		for _, l := range status {
			if rng.IntN(2) == 0 {
				own = append(own, l)
			}
			if rng.IntN(4) == 0 {
				others = append(others, l)
			}
		}
		otherRunning := rng.IntN(3) == 0
		e.issue(n, start...)
		req := jobs.CleanupRequest{Workspace: "widgets", JobID: "job", Issue: int64(n), RequestLabel: "af:fix", LabelsSet: own, OthersSet: others, OtherRunning: otherRunning}

		var wantRemoved []string
		for _, l := range start {
			switch {
			case l == "af:in-progress" && !otherRunning:
				wantRemoved = append(wantRemoved, l)
			case l != "af:in-progress" && slices.Contains(status, l) && slices.Contains(own, l) && !slices.Contains(others, l):
				wantRemoved = append(wantRemoved, l)
			}
		}
		var wantSet []string
		if !slices.Contains(start, "af:fix") {
			wantSet = []string{"af:fix"}
		}
		res := c.Clean(ctx, req)
		if res.Outcome != jobs.CleanupDone || !slices.Equal(res.Removed, wantRemoved) || !slices.Equal(res.Set, wantSet) || !res.RequestLabelOn {
			t.Fatalf("round %d from %v (own %v, others %v, running %v): %+v, want removed %v and set %v", round, start, own, others, otherRunning, res, wantRemoved, wantSet)
		}
		after := e.labelsOf(n)
		writes := len(e.labelWrites(n))
		for range 1 + rng.IntN(3) {
			res := c.Clean(ctx, req)
			if res.Outcome != jobs.CleanupDone || len(res.Set) != 0 || len(res.Removed) != 0 {
				t.Fatalf("round %d: a repeated try came to %+v, want done with nothing written", round, res)
			}
		}
		if got := len(e.labelWrites(n)); got != writes {
			t.Errorf("round %d: repeated tries wrote %d times", round, got-writes)
		}
		if got := e.labelsOf(n); !slices.Equal(got, after) {
			t.Errorf("round %d: the labels changed from %v to %v", round, after, got)
		}
	}
}

// TestAFinishedTryRecordsItsLabelsOneInfoRecordAndLeavesTheEventLogAlone_TS_17_41 verifies TS-17-41.
// Verifies: 17-REQ-6.6
func TestAFinishedTryRecordsItsLabelsOneInfoRecordAndLeavesTheEventLogAlone_TS_17_41(t *testing.T) {
	events := func(e *env, id string) int {
		var n int
		if err := e.Store.DB().QueryRowContext(ctx, "SELECT count(*) FROM af_events WHERE job_id = ?", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	e := newEnv(t, needsHuman(), needsHuman())
	e.issue(42, "af:needs-human")
	e.issue(43, "af:fix") // a person put the label back and took needs-human off already
	e.start()
	job := e.fromLabel(jobs.KindFix, 42)
	e.waitState(job.ID, jobs.StateBlocked)
	quiet := e.fromLabel(jobs.KindFix, 43)
	e.waitState(quiet.ID, jobs.StateBlocked)

	e.cancel(job.ID)
	logged := events(e, job.ID)
	d := e.waitCleanup(job.ID)
	raw, _ := json.Marshal(d.Labels)
	if string(raw) != `{"set":["af:fix"],"removed":["af:needs-human"],"restore":null,"cleanup":"done"}` {
		t.Errorf("labels = %s", raw)
	}
	recs := e.Logs.records("INFO", "cancel cleanup")
	if len(recs) != 1 {
		t.Fatalf("%d INFO records of the cleanup, want 1", len(recs))
	}
	if r := recs[0]; r["workspace"] != "widgets" || r["job"] != job.ID || r["issue"] != float64(42) ||
		fmt.Sprint(r["set"]) != "[af:fix]" || fmt.Sprint(r["removed"]) != "[af:needs-human]" {
		t.Errorf("the record is %v", r)
	}
	if n := events(e, job.ID); n != logged {
		t.Errorf("the event log of the job went from %d to %d events", logged, n)
	}

	e.cancel(quiet.ID)
	d = e.waitCleanup(quiet.ID)
	if !slices.Equal(d.Labels.Set, []string{"af:needs-human"}) || !slices.Equal(d.Labels.Removed, []string{"af:in-progress"}) || d.Labels.Restore != nil {
		t.Errorf("a try that wrote nothing: labels %+v, want set and removed as the runner reported them and restore null", d.Labels)
	}
}
