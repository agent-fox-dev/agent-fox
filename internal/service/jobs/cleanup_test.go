package jobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
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
