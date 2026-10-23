package jobs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/service/labelcleanup"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// fakeCleaner is a cleaner of the cancel cleanup that a test scripts: it records every try and answers what result says,
// done with nothing written when result is nil. A try waits for hold to be closed when hold is set.
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
	defer func() {
		c.mu.Lock()
		c.active--
		c.mu.Unlock()
	}()
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return jobs.CleanupResult{Outcome: jobs.CleanupPending}
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

// withCleaner is a configuration that gives the service the cleaner.
func withCleaner(c jobs.Cleaner) func(*jobs.Config) {
	return func(cfg *jobs.Config) { cfg.Cleaner = c }
}

// cleanupOf is the job's labels.cleanup, "" when it is null.
func cleanupOf(d jobs.Doc) string {
	if d.Labels == nil || d.Labels.Cleanup == nil {
		return ""
	}
	return *d.Labels.Cleanup
}

// blockedJob makes a job of the label for the issue that ends blocked with needs-human set, and returns it.
func (e *env) blockedJob(r *fakerunner.Runner, workspace string, issue int) jobs.Doc {
	e.t.Helper()
	r.Then(fakerunner.Return(jobs.Result{
		Envelope:  fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which?", "a")),
		LabelsSet: []string{"af:needs-human"}, LabelsRemoved: []string{"af:in-progress"},
	}))
	doc := e.createFromLabel(workspace, issue, "af:fix")
	return e.waitState(doc.ID, jobs.StateBlocked)
}

// TestAJobWithoutAnIssueOrCancelledForAnotherCauseIsMarkedNoCleanup_TS_17_32 verifies TS-17-32.
// Verifies: 17-REQ-5.4
func TestAJobWithoutAnIssueOrCancelledForAnotherCauseIsMarkedNoCleanup_TS_17_32(t *testing.T) {
	c := &fakeCleaner{}
	e := newEnv(t, withCleaner(c), withoutEnqueue())
	r := e.call("POST", "/api/v1/workspaces/widgets/triage", map[string]any{"text": "the build is red", "title": "red"})
	if r.Status != http.StatusAccepted {
		t.Fatalf("create a text triage: %d %s", r.Status, r.Body)
	}
	text := r.Doc(t)
	if d := e.cancel(text.ID).Doc(t); d.State != jobs.StateCancelled || cleanupOf(d) != "" {
		t.Errorf("a text triage: state %s, labels %+v; want cancelled and no cleanup", d.State, d.Labels)
	}
	other := e.createFromLabel("widgets", 42, "af:fix")
	if err := e.Svc.CancelFor(ctx, other.ID, errors.New("the issue was closed")); err != nil {
		t.Fatal(err)
	}
	if d := e.get(other.ID); d.State != jobs.StateCancelled || cleanupOf(d) != "" {
		t.Errorf("another cause: state %s, labels %+v; want cancelled and no cleanup", d.State, d.Labels)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(c.Tries()); n != 0 {
		t.Errorf("%d tries, want none", n)
	}
}

// TestOnlyOneTryOfAJobsCleanupRunsAtATime_TS_17_33 verifies TS-17-33: while a try is in flight, a further start of a try of
// the same job starts none; once it has ended, a later start does.
// Verifies: 17-REQ-5.5
func TestOnlyOneTryOfAJobsCleanupRunsAtATime_TS_17_33(t *testing.T) {
	c := &fakeCleaner{hold: make(chan struct{}), result: func(jobs.CleanupRequest) jobs.CleanupResult {
		return jobs.CleanupResult{Outcome: jobs.CleanupPending, Level: slog.LevelWarn, Message: "hub is unavailable"}
	}}
	e := newEnv(t, withCleaner(c), withoutEnqueue())
	job := e.createFromLabel("widgets", 42, "af:fix")
	if d := e.cancel(job.ID).Doc(t); cleanupOf(d) != "pending" {
		t.Fatalf("labels = %+v, want cleanup pending", d.Labels)
	}
	waitFor(t, "the first try", func() bool { return len(c.Tries()) == 1 })
	for range 5 {
		e.Svc.TryCleanup(job.ID)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(c.Tries()); n != 1 {
		t.Fatalf("%d tries while the first was in flight, want 1", n)
	}
	close(c.hold)
	waitFor(t, "the first try to end", func() bool { return e.Svc.CleanupsInFlight() == 0 })
	<-e.Svc.TryCleanup(job.ID)
	if n := len(c.Tries()); n != 2 {
		t.Errorf("%d tries after the first ended and another was asked for, want 2", n)
	}
}

// TestALayerWithoutACleanerMarksNoCleanup_TS_17_34 verifies TS-17-34.
// Verifies: 17-REQ-5.6
func TestALayerWithoutACleanerMarksNoCleanup_TS_17_34(t *testing.T) {
	r := fakerunner.New(fakerunner.Block(fakerunner.Done()))
	e := newEnv(t, withRunner(r))
	e.addWorkspace("gadgets")
	e.Start()
	running := e.create("widgets", jobs.KindFix, 6)
	waitCalls(t, r, 1)
	queued := e.createFromLabel("widgets", 8, "af:fix") // behind the running job of widgets
	blocked := e.blockedJob(r, "gadgets", 7)
	if d := e.get(queued.ID); d.State != jobs.StateQueued {
		t.Fatalf("the queued job is %s", d.State)
	}
	defer e.cancel(running.ID)
	for _, id := range []string{blocked.ID, queued.ID} {
		d := e.cancel(id).Doc(t)
		if d.Labels == nil || d.Labels.Cleanup != nil {
			t.Errorf("labels = %+v, want today's labels with cleanup null", d.Labels)
		}
		raw, err := json.Marshal(d.Labels)
		if err != nil {
			t.Fatal(err)
		}
		var keys map[string]any
		if err := json.Unmarshal(raw, &keys); err != nil || len(keys) != 4 || keys["cleanup"] != nil {
			t.Errorf("labels is %s, want set, removed, restore and a null cleanup", raw)
		}
	}
	if n := e.count("SELECT count(*) FROM af_jobs WHERE cleanup IS NOT NULL OR cleanup_wanted = 1"); n != 0 {
		t.Errorf("%d jobs have a cleanup marked, want none", n)
	}
}

// TestTheLabelsMemberCleanupTakesFourValuesAndMakesLabelsNonNull_TS_17_35 verifies TS-17-35.
// Verifies: 17-REQ-5.7
func TestTheLabelsMemberCleanupTakesFourValuesAndMakesLabelsNonNull_TS_17_35(t *testing.T) {
	e := newEnv(t, withoutEnqueue())
	ids := map[string]string{}
	for i, value := range []string{"", "pending", "done", "label_not_set"} {
		r := e.call("POST", "/api/v1/workspaces/widgets/fix", map[string]any{"issue": 100 + i})
		if r.Status != http.StatusAccepted {
			t.Fatalf("create: %d %s", r.Status, r.Body)
		}
		id := r.Doc(t).ID
		ids[value] = id
		if value != "" {
			if _, err := e.DB.ExecContext(ctx, "UPDATE af_jobs SET cleanup = ? WHERE id = ?", value, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if d := e.get(ids[""]); d.Labels != nil {
		t.Errorf("no cleanup and no labels: labels = %+v, want null", d.Labels)
	}
	for _, value := range []string{"pending", "done", "label_not_set"} {
		r := e.call("GET", "/api/v1/jobs/"+ids[value], nil)
		var doc struct {
			Labels map[string]any `json:"labels"`
		}
		if err := json.Unmarshal(r.Body, &doc); err != nil {
			t.Fatal(err)
		}
		keys := make([]string, 0, len(doc.Labels))
		for k := range doc.Labels {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if !slices.Equal(keys, []string{"cleanup", "removed", "restore", "set"}) || doc.Labels["cleanup"] != value {
			t.Errorf("%s: labels = %v, want the members set, removed, restore, cleanup with cleanup %q", value, doc.Labels, value)
		}
	}
	// A value outside the four is refused by the database.
	if _, err := e.DB.ExecContext(ctx, "UPDATE af_jobs SET cleanup = 'maybe' WHERE id = ?", ids[""]); err == nil {
		t.Error("the database took a cleanup of 'maybe'")
	}
}

// cleanService serves the workspace widgets from the fake hub, with issue 42 carrying af:needs-human, and builds a service
// on the env's state file with the hub-backed cleaner, the runner and the clock, as cmd/agent-fox builds it.
func (e *env) cleanService(r jobs.Runner, clk jobs.Clock, configure ...func(*jobs.Config)) *jobs.Service {
	e.t.Helper()
	e.Hub.AddWorkspace(e.t, fakehub.Workspace{Slug: "widgets", PAT: "pat-of-widgets"})
	e.Hub.AddIssue("widgets", fakehub.Issue{Number: 42, Labels: []string{"af:needs-human"}})
	return e.newService(r, clk, append([]func(*jobs.Config){
		withCleaner(&labelcleanup.Cleaner{Subscriptions: e.Store, Hub: hub.New(e.Hub.URL, "test")}),
	}, configure...)...)
}

// blockedOn makes a fix job of the label for issue n on the service that ends blocked with needs-human set.
func blockedOn(t *testing.T, svc *jobs.Service, n int) jobs.Doc {
	t.Helper()
	doc, err := svc.Create(ctx, jobs.CreateRequest{
		Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage(fmt.Sprint(n))},
		Source: jobs.Source{Kind: "label", By: "poll"}, RequestLabel: "af:fix",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the job to be blocked", func() bool { d, _ := svc.Get(ctx, doc.ID); return d.State == jobs.StateBlocked })
	return doc
}

// needsHumanStep ends an attempt blocked with needs-human set.
func needsHumanStep() fakerunner.Step {
	return fakerunner.Return(jobs.Result{
		Envelope:  fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which?", "a")),
		LabelsSet: []string{"af:needs-human"},
	})
}

// cleanupIn is the job's cleanup as the database holds it.
func (e *env) cleanupIn(id string) string {
	e.t.Helper()
	var c sql.NullString
	if err := e.DB.QueryRowContext(ctx, "SELECT cleanup FROM af_jobs WHERE id = ?", id).Scan(&c); err != nil {
		e.t.Fatal(err)
	}
	return c.String
}

// issueLabels are the labels of issue 42.
func (e *env) issueLabels() []string {
	i, _ := e.Hub.IssueOf("widgets", 42)
	return slices.Sorted(slices.Values(i.Labels))
}

// TestAnUnavailableHubKeepsTheCleanupPendingWithOneWarnAndTheSweepCompletesIt_TS_17_43 verifies TS-17-43.
// Verifies: 17-REQ-7.2, 17-REQ-8.1
func TestAnUnavailableHubKeepsTheCleanupPendingWithOneWarnAndTheSweepCompletesIt_TS_17_43(t *testing.T) {
	clk := fakeclock.New(epoch)
	e := newEnv(t)
	svc := e.cleanService(fakerunner.New().Then(needsHumanStep()), clk)
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	job := blockedOn(t, svc, 42)
	e.Hub.Fail("/api/v1/workspaces/widgets/issues/42/labels", 503)
	if _, err := svc.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first warning", func() bool { return len(e.warnsFor(job.ID)) == 1 })
	clk.Advance(time.Minute) // a sweep, which fails again
	waitFor(t, "the second try", func() bool { return len(e.Hub.RequestsTo("POST", "/api/v1/workspaces/widgets/issues/42/labels")) >= 2 })
	waitFor(t, "the second try to end", func() bool { return svc.CleanupsInFlight() == 0 })
	d, _ := svc.Get(ctx, job.ID)
	if cleanupOf(d) != "pending" || d.Labels.Restore == nil || *d.Labels.Restore != "af:fix" {
		t.Errorf("labels %+v, want cleanup pending and restore af:fix", d.Labels)
	}
	if n := len(e.warnsFor(job.ID)); n != 1 {
		t.Errorf("%d WARN records across the failures, want 1", n)
	}

	e.Hub.Clear()
	clk.Advance(time.Minute)
	waitFor(t, "the sweep to complete the cleanup", func() bool { return e.cleanupIn(job.ID) == "done" })
	if got := e.issueLabels(); !slices.Equal(got, []string{"af:fix"}) {
		t.Errorf("issue 42 carries %v", got)
	}

	// An unreadable token, a store that fails and a record without a forge URL keep a cleanup pending too.
	for name, c := range map[string]struct{ fail, mend func() }{
		"an unreadable token": {
			func() {
				e.exec("UPDATE af_subscriptions SET sealed_token = x'00' WHERE slug = 'widgets'")
			},
			func() { e.subscribe("widgets") },
		},
		"a store that fails": {
			func() { e.exec("ALTER TABLE af_subscriptions RENAME TO af_subscriptions_away") },
			func() { e.exec("ALTER TABLE af_subscriptions_away RENAME TO af_subscriptions") },
		},
		"no forge URL": {
			func() { e.Hub.UpdateWorkspace("widgets", func(w *fakehub.Workspace) { w.ForgeURL = "" }) },
			func() {
				e.Hub.UpdateWorkspace("widgets", func(w *fakehub.Workspace) { w.ForgeURL = "https://github.example.com/acme/widgets" })
			},
		},
	} {
		e.Hub.AddIssue("widgets", fakehub.Issue{Number: 42, Labels: []string{"af:needs-human"}})
		other := blockedOn(t, svc, 42)
		c.fail()
		if _, err := svc.Cancel(ctx, other.ID); err != nil {
			t.Fatal(err)
		}
		waitFor(t, name+": the warning", func() bool { return len(e.warnsFor(other.ID)) == 1 })
		waitFor(t, name+": the try", func() bool { return svc.CleanupsInFlight() == 0 })
		if got := e.cleanupIn(other.ID); got != "pending" {
			t.Errorf("%s: cleanup %s, want pending", name, got)
		}
		c.mend()
		clk.Advance(time.Minute)
		waitFor(t, name+": the sweep to complete it", func() bool { return e.cleanupIn(other.ID) == "done" })
	}
}

// exec runs a statement on the state file.
func (e *env) exec(query string, args ...any) {
	e.t.Helper()
	if _, err := e.DB.ExecContext(ctx, query, args...); err != nil {
		e.t.Fatal(err)
	}
}

// warnsFor are the WARN records of the cancel cleanup of the job.
func (e *env) warnsFor(id string) []map[string]any {
	var out []map[string]any
	for _, rec := range e.Logs.find("WARN", "cancel cleanup") {
		if rec["job"] == id {
			out = append(out, rec)
		}
	}
	return out
}

// TestACleanupPendingAnHourAfterItsCancelEndsLabelNotSetWithOneError_TS_17_50 verifies TS-17-50.
// Verifies: 17-REQ-8.2
func TestACleanupPendingAnHourAfterItsCancelEndsLabelNotSetWithOneError_TS_17_50(t *testing.T) {
	clk := fakeclock.New(epoch)
	e := newEnv(t)
	svc := e.cleanService(fakerunner.New(needsHumanStep()), clk)
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	job := blockedOn(t, svc, 42)
	e.Hub.Fail("/api/v1/workspaces/widgets/issues/42/labels", 503)
	if _, err := svc.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	for range 61 {
		waitFor(t, "the tries to end", func() bool { return svc.CleanupsInFlight() == 0 })
		clk.Advance(time.Minute)
	}
	waitFor(t, "the abandonment", func() bool { return e.cleanupIn(job.ID) == "label_not_set" })
	if n := len(e.Logs.find("ERROR", "an hour")); n != 1 {
		t.Errorf("%d ERROR records of the abandonment, want 1", n)
	}
	waitFor(t, "the tries to end", func() bool { return svc.CleanupsInFlight() == 0 })
	requests := e.Hub.Total()
	for range 3 {
		clk.Advance(time.Minute)
	}
	time.Sleep(50 * time.Millisecond)
	if e.Hub.Total() != requests {
		t.Error("a try was made after the cleanup was abandoned")
	}
	if n := len(e.Logs.find("ERROR", "an hour")); n != 1 {
		t.Errorf("%d ERROR records of the abandonment after more sweeps, want 1", n)
	}
}

// TestStartAndTheSweepsDoNotWaitForATry_TS_17_51 verifies TS-17-51.
// Verifies: 17-REQ-8.3
func TestStartAndTheSweepsDoNotWaitForATry_TS_17_51(t *testing.T) {
	clk := fakeclock.New(epoch)
	e := newEnv(t)
	first := e.cleanService(fakerunner.New(), clk, withoutEnqueue())
	pending := e.createFromLabel("widgets", 42, "af:fix")
	e.createFromLabel("widgets", 43, "af:fix") // queued, to be handed over by the first sweep
	if _, err := first.Cancel(ctx, pending.ID); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	e.exec("UPDATE af_jobs SET cleanup = 'pending' WHERE id = ?", pending.ID) // as a stop during its try leaves it
	e.Hub.Delay("/api/v1/workspaces/widgets/issues/42", 30*time.Second)

	r := fakerunner.New(fakerunner.Done())
	svc := e.newService(r, clk, withCleaner(&labelcleanup.Cleaner{Subscriptions: e.Store, Hub: hub.New(e.Hub.URL, "test")}))
	start := time.Now()
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("Start took %v with a try held for 30 seconds", took)
	}
	waitFor(t, "the queued attempt to be handed over", func() bool { return len(r.Calls()) == 1 })
	if e.cleanupIn(pending.ID) != "pending" || svc.CleanupsInFlight() != 1 {
		t.Errorf("cleanup %s with %d tries in flight, want the try still running", e.cleanupIn(pending.ID), svc.CleanupsInFlight())
	}
}

// TestATryIsBoundedToOneMinuteAndTheTriesInFlightAreCapped_TS_17_52 verifies TS-17-52.
// Verifies: 17-REQ-8.4
func TestATryIsBoundedToOneMinuteAndTheTriesInFlightAreCapped_TS_17_52(t *testing.T) {
	clk := fakeclock.New(epoch)
	e := newEnv(t)
	svc := e.cleanService(fakerunner.New(), clk, withoutEnqueue())
	e.Hub.Inject("GET", "/api/v1/workspaces/widgets/issues/*", fakehub.Fault{Delay: time.Hour})
	var ids []string
	for n := range 7 {
		e.Hub.AddIssue("widgets", fakehub.Issue{Number: 50 + n})
		job := e.createFromLabel("widgets", 50+n, "af:fix")
		if _, err := svc.Cancel(ctx, job.ID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, job.ID)
	}
	waitFor(t, "the cap to fill", func() bool { return countIssueReads(e) >= jobs.MaxCleanupTries })
	time.Sleep(100 * time.Millisecond)
	if n := countIssueReads(e); n != jobs.MaxCleanupTries {
		t.Errorf("%d tries reached hub at once, want the cap of %d", n, jobs.MaxCleanupTries)
	}
	clk.Advance(time.Minute)
	waitFor(t, "the tries past their minute to end", func() bool { return countIssueReads(e) > jobs.MaxCleanupTries })
	for _, id := range ids {
		if got := e.cleanupIn(id); got != "pending" {
			t.Errorf("%s: cleanup %s, want pending after a try ran out of its minute", id, got)
		}
	}
	if c := hub.New("http://127.0.0.1:1", "x"); c.HTTP.Timeout != 10*time.Second {
		t.Errorf("each hub call is bounded by %v, want the client's 10 s", c.HTTP.Timeout)
	}
}

// countIssueReads is how many issue reads hub received.
func countIssueReads(e *env) int {
	n := 0
	for _, r := range e.Hub.Requests() {
		if r.Method == "GET" && strings.HasPrefix(r.Path, "/api/v1/workspaces/widgets/issues/") {
			n++
		}
	}
	return n
}

// TestARestartedServiceCompletesAPendingCleanupOrEndsOnePastItsHour_TS_17_53 verifies TS-17-53.
// Verifies: 17-REQ-8.5
func TestARestartedServiceCompletesAPendingCleanupOrEndsOnePastItsHour_TS_17_53(t *testing.T) {
	clk := fakeclock.New(epoch)
	e := newEnv(t)
	first := e.cleanService(fakerunner.New(), clk, withoutEnqueue())
	e.Hub.Inject("", "/api/v1/workspaces/widgets/issues/*", fakehub.Fault{Status: 503})
	recent := e.createFromLabel("widgets", 42, "af:fix")
	e.Hub.AddIssue("widgets", fakehub.Issue{Number: 43})
	old := e.createFromLabel("widgets", 43, "af:fix")
	for _, id := range []string{recent.ID, old.ID} {
		if _, err := first.Cancel(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "the first tries", func() bool { return first.CleanupsInFlight() == 0 })
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	e.exec("UPDATE af_jobs SET finished_at = ?, cleanup = 'pending' WHERE id = ?", epoch.Add(-2*time.Hour).Format("2006-01-02T15:04:05.000Z"), old.ID)
	e.Hub.Clear()
	reads, before := countIssueReads(e), e.Hub.Total()

	svc := e.newService(fakerunner.New(), clk, withCleaner(&labelcleanup.Cleaner{Subscriptions: e.Store, Hub: hub.New(e.Hub.URL, "test")}))
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the recent cleanup", func() bool { return e.cleanupIn(recent.ID) == "done" })
	if got := e.cleanupIn(old.ID); got != "label_not_set" {
		t.Errorf("the old cleanup is %s, want label_not_set", got)
	}
	for _, r := range e.Hub.Requests()[before:] {
		if r.Path == "/api/v1/workspaces/widgets/issues/43" {
			t.Error("the cleanup past its hour was tried")
		}
	}
	if countIssueReads(e) != reads+1 {
		t.Errorf("%d issue reads at the start, want the recent one only", countIssueReads(e)-reads)
	}
}

// TestShutdownCancelsATryInFlightAndLeavesTheCleanupPending_TS_17_54 verifies TS-17-54.
// Verifies: 17-REQ-8.6
func TestShutdownCancelsATryInFlightAndLeavesTheCleanupPending_TS_17_54(t *testing.T) {
	clk := fakeclock.New(epoch)
	e := newEnv(t)
	svc := e.cleanService(fakerunner.New(), clk, withoutEnqueue())
	e.Hub.Delay("/api/v1/workspaces/widgets/issues/42", time.Hour)
	job := e.createFromLabel("widgets", 42, "af:fix")
	if _, err := svc.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the try to reach hub", func() bool { return countIssueReads(e) == 1 })
	closed := make(chan error, 1)
	go func() { closed <- svc.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return while a try was in flight")
	}
	if svc.CleanupsInFlight() != 0 {
		t.Error("Close returned with a try in flight")
	}
	if got := e.cleanupIn(job.ID); got != "pending" {
		t.Errorf("cleanup %s, want pending", got)
	}
}

// TestThePurgeNeverRemovesAJobWhoseCleanupIsPending_TS_17_55 verifies TS-17-55.
// Verifies: 17-REQ-8.7
func TestThePurgeNeverRemovesAJobWhoseCleanupIsPending_TS_17_55(t *testing.T) {
	clk := fakeclock.New(epoch)
	e := newEnv(t, withClock(clk), withRetain(time.Hour), withoutEnqueue())
	pending := e.createFromLabel("widgets", 42, "af:fix")
	done := e.createFromLabel("widgets", 43, "af:fix")
	for id, cleanup := range map[string]string{pending.ID: "pending", done.ID: "done"} {
		e.cancel(id)
		if _, err := e.DB.ExecContext(ctx, "UPDATE af_jobs SET cleanup = ? WHERE id = ?", cleanup, id); err != nil {
			t.Fatal(err)
		}
	}
	clk.Advance(2 * time.Hour)
	if _, err := e.Svc.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM af_jobs WHERE id = ?", pending.ID); n != 1 {
		t.Error("the job whose cleanup is pending was purged")
	}
	if n := e.count("SELECT count(*) FROM af_jobs WHERE id = ?", done.ID); n != 0 {
		t.Error("the job whose cleanup is done was kept")
	}
}
