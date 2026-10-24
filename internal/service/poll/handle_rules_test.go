package poll

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

// removalsTo are the removals of the label from the issue that hub received.
func (e *env) removalsTo(issue int, label string) int {
	return len(e.Hub.RequestsTo("DELETE", "/api/v1/workspaces/widgets/issues/"+strconv.Itoa(issue)+"/labels/"+label))
}

// apiJob makes an open fix job of the API for the issue.
func (e *env) apiJob(issue int) string {
	e.t.Helper()
	doc, err := e.Jobs.Create(ctx, jobs.CreateRequest{Workspace: slug, Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage(strconv.Itoa(issue))}, Source: jobs.Source{Kind: "api", By: "usr_1"}})
	if err != nil {
		e.t.Fatal(err)
	}
	return doc.ID
}

// TestALabelOnAnIssueWithAnOpenJobIsAbsorbedOrItsRemovalOwed_TS_18_40 verifies TS-18-40.
// Verifies: 18-REQ-6.4
func TestALabelOnAnIssueWithAnOpenJobIsAbsorbedOrItsRemovalOwed_TS_18_40(t *testing.T) {
	e := streaming(t)
	e.issue(60, "af:fix")
	e.issue(61, "af:fix")
	e.apiJob(60)
	e.apiJob(61)
	e.Hub.Inject("DELETE", "/api/v1/workspaces/widgets/issues/61/labels/af:fix", fakehub.Fault{Status: 422})
	e.labeled("a60", 60, "af:fix")
	e.labeled("a61", 61, "af:fix")
	waitFor(t, "both events", func() bool { return e.taken("a60") && e.removalsTo(61, "af:fix") == 1 })
	if e.jobsFor(60) != 1 || e.jobsFor(61) != 1 || e.removalsTo(60, "af:fix") != 1 {
		t.Errorf("jobs %d and %d, removals of 60 %d: want the label absorbed and no job made", e.jobsFor(60), e.jobsFor(61), e.removalsTo(60, "af:fix"))
	}
	waitFor(t, "the owed record of 61", func() bool {
		recs, _ := NewRecords(e.DB).Find(ctx, slug, 61, "af:fix")
		return len(recs) == 1 && recs[0].Owed
	})

	// A duplicate refusal of the jobs layer is an absorbed label too.
	d := streaming(t, withJobs(func(j Jobs) Jobs { return &refusing{Jobs: j, err: &jobs.DuplicateError{JobID: "job_other"}} }))
	d.issue(62, "af:fix")
	d.labeled("a62", 62, "af:fix")
	waitFor(t, "the removal of 62", func() bool { return d.removalsTo(62, "af:fix") == 1 })
	if d.jobsFor(62) != 0 {
		t.Error("a job was made despite the duplicate")
	}
}

// TestTheEventOfARestoredLabelIsHeld_TS_18_41 verifies TS-18-41.
// Verifies: 18-REQ-6.5
func TestTheEventOfARestoredLabelIsHeld_TS_18_41(t *testing.T) {
	e := streaming(t, withRunner(fakerunner.New(fakerunner.Block(cancelledResult()))))
	e.issue(42, "af:fix")
	e.labeled("first", 42, "af:fix")
	id := e.jobOf(42).ID
	waitFor(t, "the label to be taken", func() bool { return e.removalsTo(42, "af:fix") == 1 })
	if !e.Runner.Wait(1, 10*time.Second) {
		t.Fatal("the runner was not called")
	}
	if _, err := e.Jobs.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	e.Hub.AddLabel(slug, 42, "af:fix") // the worker's cancel cleanup puts it back
	e.runnerReturned(id)
	e.labeled("restore", 42, "af:fix")
	waitFor(t, "the event to be decided", func() bool { return e.taken("restore") })
	if e.jobsFor(42) != 1 || e.removalsTo(42, "af:fix") != 1 || !slices.Contains(e.labelsOf(42), "af:fix") {
		t.Errorf("%d jobs and %d removals, labels %v: want the label held", e.jobsFor(42), e.removalsTo(42, "af:fix"), e.labelsOf(42))
	}
}

// TestAnOwedRemovalIsRetriedByASecondEventAndMakesNoJob_TS_18_42 verifies TS-18-42.
// Verifies: 18-REQ-6.6
func TestAnOwedRemovalIsRetriedByASecondEventAndMakesNoJob_TS_18_42(t *testing.T) {
	e := streaming(t)
	e.issue(42, "af:fix")
	e.Hub.Inject("DELETE", "/api/v1/workspaces/widgets/issues/42/labels/af:fix", fakehub.Fault{Status: 422, Once: true})
	e.labeled("first", 42, "af:fix")
	e.jobOf(42)
	waitFor(t, "the failed removal", func() bool { return e.removalsTo(42, "af:fix") == 1 })
	e.labeled("second", 42, "af:fix")
	waitFor(t, "the removal again", func() bool { return e.removalsTo(42, "af:fix") == 2 })
	if e.jobsFor(42) != 1 {
		t.Errorf("%d jobs, want no second one", e.jobsFor(42))
	}
}

// TestRefusalsOfTheJobsLayerLeaveTheLabel_TS_18_43 verifies TS-18-43.
// Verifies: 18-REQ-6.7
func TestRefusalsOfTheJobsLayerLeaveTheLabel_TS_18_43(t *testing.T) {
	t.Run("not subscribed", func(t *testing.T) {
		e := streaming(t, withJobs(func(j Jobs) Jobs { return &refusing{Jobs: j, err: jobs.ErrNotSubscribed} }))
		e.issue(42, "af:fix")
		e.labeled("e", 42, "af:fix")
		waitFor(t, "the issue read", func() bool { return len(e.Hub.RequestsTo("GET", "/api/v1/workspaces/widgets/issues/42")) == 1 })
		settle()
		if e.removalsTo(42, "af:fix") != 0 || e.taken("e") || len(e.Logs.level("WARN"))+len(e.Logs.level("ERROR")) != 0 {
			t.Errorf("removals %d, taken %v, records %v: want nothing written, recorded or logged", e.removalsTo(42, "af:fix"), e.taken("e"), e.Logs.records())
		}
	})
	t.Run("another refusal", func(t *testing.T) {
		e := streaming(t, withJobs(func(j Jobs) Jobs { return &refusing{Jobs: j, err: errors.New("the database is gone")} }))
		e.issue(42, "af:fix")
		e.labeled("e", 42, "af:fix")
		waitFor(t, "the WARN", func() bool { return len(e.Logs.find("WARN", "make a job")) == 1 })
		if e.removalsTo(42, "af:fix") != 0 || e.taken("e") {
			t.Errorf("removals %d, taken %v: want the label left and the event not recorded", e.removalsTo(42, "af:fix"), e.taken("e"))
		}
	})
}

// TestHubFailuresLeaveTheEventToThePoll_TS_18_44 verifies TS-18-44.
// Verifies: 18-REQ-6.8
func TestHubFailuresLeaveTheEventToThePoll_TS_18_44(t *testing.T) {
	for name, c := range map[string]struct {
		path, method string
		status       int
		stale        bool
	}{
		"the variable read 503": {"/api/v1/workspaces/widgets/vars/resolved", "GET", 503, true},
		"the issue read 503":    {"/api/v1/workspaces/widgets/issues/42", "GET", 503, false},
		"the issue read 401":    {"/api/v1/workspaces/widgets/issues/42", "GET", 401, false},
		"the removal 503":       {"/api/v1/workspaces/widgets/issues/42/labels/af:fix", "DELETE", 503, false},
	} {
		t.Run(name, func(t *testing.T) {
			e := streaming(t)
			e.issue(42, "af:fix")
			if c.stale {
				for range 11 { // the reading of the variables ages past 5 minutes, the connection kept alive
					e.Hub.Heartbeat(slug)
					settle()
					e.Clock.Advance(30 * time.Second)
				}
			}
			e.Hub.Inject(c.method, c.path, fakehub.Fault{Status: c.status})
			e.labeled("e", 42, "af:fix")
			waitFor(t, "the failure to be logged", func() bool {
				return len(e.Logs.find("WARN", "left to the poll"))+len(e.Logs.find("ERROR", "left to the poll")) == 1
			})
			if e.taken("e") {
				t.Error("the event was recorded")
			}
			if c.method == "DELETE" {
				recs, _ := NewRecords(e.DB).Find(ctx, slug, 42, "af:fix")
				if e.jobsFor(42) != 1 || len(recs) != 1 || !recs[0].Owed || !slices.Contains(e.labelsOf(42), "af:fix") {
					t.Errorf("jobs %d, records %+v, labels %v: want the job made, its removal owed and the label on the issue", e.jobsFor(42), recs, e.labelsOf(42))
				}
				return
			}
			if e.jobsFor(42) != 0 {
				t.Fatal("a job was made")
			}
			e.Hub.Clear()
			e.cycle()
			if e.jobsFor(42) != 1 {
				t.Errorf("the next cycle made %d jobs for 42, want 1", e.jobsFor(42))
			}
		})
	}
}

// TestADecidedEventLeavesOneRowAndARepeatDoesNothing_TS_18_45 verifies the request event of TS-18-45 (the closed event joins
// it with the closed events).
// Verifies: 18-REQ-7.1
func TestADecidedEventLeavesOneRowAndARepeatDoesNothing_TS_18_45(t *testing.T) {
	e := streaming(t)
	e.issue(42, "af:fix")
	e.labeled("evt-1", 42, "af:fix")
	e.jobOf(42)
	waitFor(t, "the row", func() bool { return e.taken("evt-1") })
	var at string
	if err := e.DB.QueryRowContext(ctx, "SELECT taken_at FROM af_hub_events WHERE workspace = ? AND event_id = 'evt-1'", slug).Scan(&at); err != nil || at == "" {
		t.Errorf("the row has taken_at %q (%v)", at, err)
	}
	before := len(e.forgeRequests())
	e.labeled("evt-1", 42, "af:fix")
	settle()
	if got := len(e.forgeRequests()); got != before || e.jobsFor(42) != 1 {
		t.Errorf("the repeat made %d forge requests and %d jobs", got-before, e.jobsFor(42))
	}
}

// TestIgnoredPollOffAndFailedEventsAreNotRecorded_TS_18_46 verifies TS-18-46.
// Verifies: 18-REQ-7.2
func TestIgnoredPollOffAndFailedEventsAreNotRecorded_TS_18_46(t *testing.T) {
	e := streaming(t)
	e.issue(42, "af:fix")
	e.issue(43, "af:fix")
	e.labeled("ignored", 42, "bug")
	e.Hub.SendEvent(slug, fakehub.HubEvent{ID: "closed-no-job", Type: fakehub.EventIssueClosed, Issue: 70})
	e.Hub.Inject("GET", "/api/v1/workspaces/widgets/issues/43", fakehub.Fault{Status: 503})
	e.labeled("failed", 43, "af:fix")
	waitFor(t, "the failed read", func() bool { return len(e.Hub.RequestsTo("GET", "/api/v1/workspaces/widgets/issues/43")) == 1 })
	for range 11 {
		e.Hub.Heartbeat(slug)
		settle()
		e.Clock.Advance(30 * time.Second)
	}
	e.setVar("AF_POLL_INTERVAL", "0")
	e.labeled("off", 42, "af:fix")
	e.conns(slug, 0)
	var n int
	if err := e.DB.QueryRowContext(ctx, "SELECT count(*) FROM af_hub_events").Scan(&n); err != nil || n != 0 {
		t.Errorf("%d rows in af_hub_events (%v), want none", n, err)
	}
}

// TestARecordedIdIsIgnoredAfterARestart_TS_18_47 verifies TS-18-47.
// Verifies: 18-REQ-7.3
func TestARecordedIdIsIgnoredAfterARestart_TS_18_47(t *testing.T) {
	e := streaming(t)
	e.issue(42, "af:fix")
	e.labeled("evt-1", 42, "af:fix")
	e.jobOf(42)
	waitFor(t, "the row", func() bool { return e.taken("evt-1") })
	e.Poll.Stop()

	key, err := secrets.NewKey(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(ctx, filepath.Join(e.Dir, "agent-fox.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	e.Hub.AddLabel(slug, 42, "af:fix") // the label is on the issue again, so only the id can keep the event from acting
	sup := New(Config{Subscriptions: reopened, Hub: e.Client, Jobs: e.Jobs, DB: reopened.DB(), Logger: e.logger, Clock: e.Clock,
		FirstDelay: func() time.Duration { return noCycleDelay }})
	sup.Start(ctx)
	t.Cleanup(sup.Stop)
	waitFor(t, "the new stream", func() bool { return e.Hub.EventConns(slug) == 1 })
	before := len(e.forgeRequests())
	e.labeled("evt-1", 42, "af:fix")
	settle()
	if got := len(e.forgeRequests()); got != before || e.jobsFor(42) != 1 {
		t.Errorf("after the restart the recorded event made %d forge requests and %d jobs", got-before, e.jobsFor(42))
	}
	if ok, _ := NewRecords(reopened.DB()).EventTaken(ctx, slug, "evt-1"); !ok {
		t.Error("the row is gone")
	}
}
