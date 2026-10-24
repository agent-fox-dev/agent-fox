package poll

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
)

// streaming is the poll started with no cycles while the test runs and the stream of widgets connected.
func streaming(t *testing.T, opts ...option) *env {
	t.Helper()
	e := newEnv(t, append([]option{noCycles(), withRunner(fakerunner.New().Then(fakerunner.Block(fakerunner.Step{})))}, opts...)...)
	e.started()
	e.conns(slug, 1)
	return e
}

// labeled sends hub.forge.issue.labeled for the issue and the label.
func (e *env) labeled(id string, issue int, label string) {
	e.Hub.SendEvent(slug, fakehub.HubEvent{ID: id, Type: fakehub.EventIssueLabeled, Issue: issue, Label: label})
}

// forgeRequests are the requests of the forge API of widgets that hub received: its issues and their labels.
func (e *env) forgeRequests() []string {
	var out []string
	for _, r := range e.Hub.Requests() {
		if strings.HasPrefix(r.Path, "/api/v1/workspaces/widgets/issues") {
			out = append(out, r.Method+" "+r.Path)
		}
	}
	return out
}

// taken says whether the event id is recorded.
func (e *env) taken(id string) bool {
	e.t.Helper()
	ok, err := NewRecords(e.DB).EventTaken(ctx, slug, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return ok
}

// jobOf is the job made for the issue, waited for for at most a second.
func (e *env) jobOf(issue int) jobRow {
	e.t.Helper()
	var job jobRow
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, j := range e.jobList() {
			if j.Issue.Int64 == int64(issue) {
				return j
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	e.t.Fatalf("no job for %d within a second", issue)
	return job
}

// reconnected advances the clock a second at a time until the stream has made the n-th connection: the stream asks for its
// wait a moment after the last connection ended.
func (e *env) reconnected(n int) {
	e.t.Helper()
	waitFor(e.t, "the stream to connect again", func() bool {
		if len(e.Hub.EventsRequests()) >= n && e.Hub.EventConns(slug) == 1 {
			return true
		}
		e.Clock.Advance(time.Second)
		time.Sleep(5 * time.Millisecond)
		return false
	})
}

// TestEventsOfAWorkspaceAreHandledInOrderAndWorkspacesDoNotWait_TS_18_20 verifies TS-18-20.
// Verifies: 18-REQ-3.7
func TestEventsOfAWorkspaceAreHandledInOrderAndWorkspacesDoNotWait_TS_18_20(t *testing.T) {
	o := &ordered{hold: 41, reached: make(chan struct{}, 1), release: make(chan struct{})}
	e := newEnv(t, noCycles(), withRunner(fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))), withJobs(func(j Jobs) Jobs { o.Jobs = j; return o }))
	e.addWorkspace("gadgets", "pat-GADGETS-0123456789")
	e.issue(41, "af:fix")
	e.issue(42, "af:fix")
	e.Hub.AddIssue("gadgets", fakehub.Issue{Number: 7, Labels: []string{"af:fix"}})
	e.started()
	e.conns(slug, 1)
	e.conns("gadgets", 1)
	e.labeled("w1", 41, "af:fix")
	e.labeled("w2", 42, "af:fix")
	<-o.reached // the first event of widgets is held in the making of its job
	e.Hub.SendEvent("gadgets", fakehub.HubEvent{ID: "g1", Type: fakehub.EventIssueLabeled, Issue: 7, Label: "af:fix"})
	waitFor(t, "the event of gadgets", func() bool { o.mu.Lock(); defer o.mu.Unlock(); return slices.Contains(o.order, "create 7") })
	settle()
	if reads := len(e.Hub.RequestsTo("GET", "/api/v1/workspaces/widgets/issues/42")); reads != 0 {
		t.Fatalf("the second event of widgets was handled (%d reads) while the first was held", reads)
	}
	close(o.release)
	e.jobOf(42)
	o.mu.Lock()
	defer o.mu.Unlock()
	if i41, i42 := slices.Index(o.order, "create 41"), slices.Index(o.order, "create 42"); i41 < 0 || i42 < i41 {
		t.Errorf("the order is %v, want the events of widgets in the order they came", o.order)
	}
}

// TestAStaleReadingOfZeroStopsTheEventAndClosesTheConnection_TS_18_23 verifies the request event of TS-18-23 (the closed
// event joins it with the closed events).
// Verifies: 18-REQ-4.3
func TestAStaleReadingOfZeroStopsTheEventAndClosesTheConnection_TS_18_23(t *testing.T) {
	e := streaming(t)
	e.issue(42, "af:fix")
	e.Hub.Heartbeat(slug) // keeps the connection alive while the reading ages
	e.Clock.Advance(30 * time.Second)
	for range 11 {
		e.Hub.Heartbeat(slug)
		settle()
		e.Clock.Advance(30 * time.Second)
	}
	e.setVar("AF_POLL_INTERVAL", "0")
	reads := len(e.Hub.RequestsTo("GET", "/api/v1/workspaces/widgets/vars/resolved"))
	e.labeled("evt-1", 42, "af:fix")
	e.conns(slug, 0)
	if got := len(e.Hub.RequestsTo("GET", "/api/v1/workspaces/widgets/vars/resolved")); got != reads+1 {
		t.Errorf("%d reads of the variables before acting, want 1", got-reads)
	}
	if len(e.jobList()) != 0 || len(e.forgeRequests()) != 0 || e.taken("evt-1") {
		t.Errorf("jobs %v, forge requests %v, taken %v: want nothing done and nothing recorded", e.jobList(), e.forgeRequests(), e.taken("evt-1"))
	}
	n := len(e.Hub.RequestsTo("GET", "/api/v1/workspaces/widgets/vars/resolved"))
	e.Clock.Advance(5*time.Minute - time.Second)
	settle()
	if got := len(e.Hub.RequestsTo("GET", "/api/v1/workspaces/widgets/vars/resolved")); got != n {
		t.Errorf("the variables were read again before 5 minutes")
	}
	e.Clock.Advance(time.Second)
	waitFor(t, "the next reading", func() bool { return len(e.Hub.RequestsTo("GET", "/api/v1/workspaces/widgets/vars/resolved")) == n+1 })
}

// TestEventHandlingFailuresAreLoggedOncePerKindUntilACleanEvent_TS_18_33 verifies TS-18-33.
// Verifies: 18-REQ-5.5
func TestEventHandlingFailuresAreLoggedOncePerKindUntilACleanEvent_TS_18_33(t *testing.T) {
	e := streaming(t)
	for _, n := range []int{51, 52, 53, 54} {
		e.issue(n, "af:fix")
	}
	e.Hub.Fail("/api/v1/workspaces/widgets/issues/51", 503)
	e.Hub.Fail("/api/v1/workspaces/widgets/issues/52", 503)
	e.Hub.Fail("/api/v1/workspaces/widgets/issues/54", 503)
	e.labeled("e1", 51, "af:fix")
	e.labeled("e2", 52, "af:fix")
	waitFor(t, "both reads", func() bool { return len(e.Hub.RequestsTo("GET", "/api/v1/workspaces/widgets/issues/52")) == 1 })
	settle()
	warns := func() int { return len(e.Logs.find("WARN", "read the issue")) }
	if warns() != 1 {
		t.Fatalf("%d WARN records for two failing reads, want 1", warns())
	}
	if e.Hub.EventConns(slug) != 1 {
		t.Fatal("a failing read ended the connection")
	}
	e.labeled("e3", 53, "af:fix") // handled without failure
	e.jobOf(53)
	e.labeled("e4", 54, "af:fix")
	waitFor(t, "the failure to be logged again", func() bool { return warns() == 2 })
	if n := len(e.Logs.find("INFO", "recovered")); n != 0 {
		t.Errorf("%d recovery records of the connection, want none: handling failures do not touch the connection's records", n)
	}
}

// TestMalformedEventsAreIgnoredAndWarnedOncePerConnection_TS_18_34 verifies TS-18-34.
// Verifies: 18-REQ-5.6
func TestMalformedEventsAreIgnoredAndWarnedOncePerConnection_TS_18_34(t *testing.T) {
	e := streaming(t)
	bad := []string{
		`data: {"event_type":"hub.forge.issue.labeled","metadata":{"number":42,"label":"af:fix"}}` + "\n\n", // no id
		`data: {"id":"e1","event_type":"hub.forge.issue.labeled","metadata":{"label":"af:fix"}}` + "\n\n",   // no issue
		`data: {"id":"` + strings.Repeat("x", 257) + `","event_type":"hub.forge.issue.labeled","metadata":{"number":42,"label":"af:fix"}}` + "\n\n",
		`data: {"id":"e\u0007","event_type":"hub.forge.issue.labeled","metadata":{"number":42,"label":"af:fix"}}` + "\n\n",
		`data: {"id":"e5","event_type":"hub.forge.issue.closed"}` + "\n\n", // a closed event with no issue
	}
	send := func() {
		for _, f := range bad {
			e.Hub.SendFrame(slug, f)
		}
		settle()
	}
	send()
	if r := e.forgeRequests(); len(r) != 0 {
		t.Errorf("hub received %v", r)
	}
	if n := len(e.Logs.find("WARN", "malformed")); n != 1 {
		t.Errorf("%d WARN records on the connection, want 1", n)
	}
	e.Hub.EndEvents(slug)
	e.reconnected(2)
	send()
	if n := len(e.Logs.find("WARN", "malformed")); n != 2 {
		t.Errorf("%d WARN records after a second connection, want 2", n)
	}
}

// TestOtherIgnoredEventsLogNothingAboveDebug_TS_18_35 verifies TS-18-35.
// Verifies: 18-REQ-5.7
func TestOtherIgnoredEventsLogNothingAboveDebug_TS_18_35(t *testing.T) {
	e := streaming(t)
	before := len(e.Logs.records())
	e.Hub.SendFrame(slug, `data: {"id":"o1","event_type":"hub.workspace.update"}`+"\n\n")
	e.labeled("o2", 42, "af:ready")
	e.Hub.SendEvent(slug, fakehub.HubEvent{ID: "o3", Type: "hub.forge.issue.unlabeled", Issue: 42, Label: "af:fix"})
	e.Hub.SendEvent(slug, fakehub.HubEvent{ID: "o4", Type: fakehub.EventIssueLabeled, Issue: 42, Label: "af:fix", Workspace: "gadgets"})
	settle()
	if recs := e.Logs.records()[before:]; len(recs) != 0 {
		t.Errorf("records above DEBUG: %v", recs)
	}
	if r := e.forgeRequests(); len(r) != 0 {
		t.Errorf("hub received %v", r)
	}
}

// TestOnlyRequestEventsReachTheRules_TS_18_37 verifies TS-18-37.
// Verifies: 18-REQ-6.1
func TestOnlyRequestEventsReachTheRules_TS_18_37(t *testing.T) {
	e := streaming(t)
	e.issue(42, "af:fix", "af:ready", "bug")
	e.labeled("x1", 42, "af:ready")
	e.labeled("x2", 42, "bug")
	e.Hub.SendEvent(slug, fakehub.HubEvent{ID: "x3", Type: "hub.forge.issue.unlabeled", Issue: 42, Label: "af:fix"})
	e.Hub.SendEvent(slug, fakehub.HubEvent{ID: "x4", Type: "hub.forge.issue.reopened", Issue: 42})
	e.Hub.SendEvent(slug, fakehub.HubEvent{ID: "x5", Type: fakehub.EventIssueLabeled, Issue: 42, Label: "af:fix", Workspace: "gadgets"})
	e.Hub.SendEvent(slug, fakehub.HubEvent{ID: "x6", Type: fakehub.EventIssueClosed, Issue: 99})
	settle()
	if r := e.forgeRequests(); len(r) != 0 {
		t.Fatalf("hub received %v", r)
	}
	e.labeled("x7", 42, "AF:FIX")
	if j := e.jobOf(42); j.RequestLabel != "af:fix" {
		t.Errorf("the job's request label is %q, want the canonical af:fix", j.RequestLabel)
	}
}

// TestARepeatedIdAClosedIssueAPullRequestOrAMissingLabelDoNothing_TS_18_38 verifies TS-18-38.
// Verifies: 18-REQ-6.2
func TestARepeatedIdAClosedIssueAPullRequestOrAMissingLabelDoNothing_TS_18_38(t *testing.T) {
	e := streaming(t)
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 50, State: "closed", Labels: []string{"af:fix"}})
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 51, IsPR: true, Labels: []string{"af:fix"}})
	e.issue(52)
	e.issue(53, "AF:Fix")
	for _, n := range []int{50, 51, 52} {
		e.labeled(fmt.Sprintf("evt-%d", n), n, "af:fix")
	}
	e.labeled("evt-9", 53, "af:fix")
	e.jobOf(53)
	waitFor(t, "the event to be taken", func() bool { return e.taken("evt-9") })
	for _, n := range []int{50, 51, 52} {
		if e.jobsFor(n) != 0 || len(e.Hub.RequestsTo("DELETE", fmt.Sprintf("/api/v1/workspaces/widgets/issues/%d/labels/af:fix", n))) != 0 {
			t.Errorf("issue %d got a job or a removal", n)
		}
		if !e.taken(fmt.Sprintf("evt-%d", n)) {
			t.Errorf("the event of %d was not recorded as decided", n)
		}
	}
	before := len(e.forgeRequests())
	e.labeled("evt-9", 53, "af:fix")
	settle()
	if got := len(e.forgeRequests()); got != before {
		t.Errorf("the repeated event sent %d forge requests", got-before)
	}
}

// TestANewRequestLabelBecomesOneEventJobWithinASecond_TS_18_39 verifies TS-18-39.
// Verifies: 18-REQ-6.3
func TestANewRequestLabelBecomesOneEventJobWithinASecond_TS_18_39(t *testing.T) {
	var order []string
	var mu sync.Mutex
	e := streaming(t)
	e.issue(42, "af:fix")
	e.Hub.SetRemovalLag(slug, 2)
	e.Hub.OnRequest(func(r fakehub.Request) {
		if r.Method == "DELETE" {
			recs, _ := NewRecords(e.DB).Find(ctx, slug, 42, "af:fix")
			n := len(recs)
			mu.Lock()
			order = append(order, fmt.Sprintf("removal with %d records", n))
			mu.Unlock()
		}
	})
	e.labeled("evt-1", 42, "af:fix")
	j := e.jobOf(42)
	doc, err := e.Jobs.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Source != (jobs.Source{Kind: "event", By: "evt-1"}) || doc.RequestLabel == nil || *doc.RequestLabel != "af:fix" || string(doc.Input.Issue) != "42" || doc.Kind != jobs.KindFix {
		t.Errorf("the job is %+v", doc)
	}
	// The fake hub records a request before it calls the observer.
	waitFor(t, "the removal", func() bool { mu.Lock(); defer mu.Unlock(); return len(order) == 1 })
	mu.Lock()
	if !slices.Equal(order, []string{"removal with 1 records"}) {
		t.Errorf("the removal came %v, want once, after the record was written", order)
	}
	mu.Unlock()
	waitFor(t, "the event to be recorded", func() bool { return e.taken("evt-1") })
	before := len(e.forgeRequests())
	e.labeled("evt-1", 42, "af:fix")
	settle()
	if got := len(e.forgeRequests()); got != before || e.jobsFor(42) != 1 {
		t.Errorf("the same event again: %d forge requests and %d jobs", got-before, e.jobsFor(42))
	}
	e.cycle() // the forge still lists 42 under af:fix
	if e.jobsFor(42) != 1 {
		t.Errorf("a cycle with the removal lagging made %d jobs for 42, want still 1", e.jobsFor(42))
	}
}
