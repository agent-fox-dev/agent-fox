package jobs_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
)

// triageOf makes a triage job of the issue as the label poll does, and returns its id.
func (e *env) triageOf(workspace string, issue int) string {
	e.t.Helper()
	doc, err := e.Svc.Create(ctx, jobs.CreateRequest{
		Workspace: workspace, Kind: jobs.KindTriage, Body: jobs.Body{Issue: json.RawMessage(itoa(issue))},
		Source: jobs.Source{Kind: "label", By: "label af:triage"}, RequestLabel: "af:triage",
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return doc.ID
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// linked is the request of the fix job that the follow-up of the triage job T asks for on the issue.
func linked(workspace, triage string, issue int) jobs.CreateRequest {
	return jobs.CreateRequest{
		Workspace: workspace, Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage(itoa(issue))},
		Source: jobs.Source{Kind: "triage", By: triage}, ParentJobID: triage, RequestLabel: "af:fix",
	}
}

// followUpOf is the follow-up the document of the job shows.
func (e *env) followUpOf(id string) *jobs.FollowUp {
	e.t.Helper()
	return e.get(id).FollowUp
}

// TestALaterTriagesPendingFollowUpWithdrawsTheEarlierOne_TS_19_44 verifies TS-19-44.
// Verifies: 19-REQ-8.1
func TestALaterTriagesPendingFollowUpWithdrawsTheEarlierOne_TS_19_44(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New().Then(fakerunner.Done())))
	e.Start()
	t0, t1 := e.triageOf("widgets", 42), e.triageOf("widgets", 43) // 43's follow-up is another issue's, which 42's leave alone
	e.waitRest(t0)
	f, err := e.Svc.RecordFollowUp(ctx, t0, 42, "af:fix")
	if err != nil || f.State != "pending" || f.JobID != nil {
		t.Fatalf("T0's follow-up is %+v (%v), want pending", f, err)
	}
	if _, err := e.Svc.RecordFollowUp(ctx, t1, 43, "af:fix"); err != nil {
		t.Fatal(err)
	}
	t2 := e.triageOf("widgets", 42) // a later triage of 42
	f, err = e.Svc.RecordFollowUp(ctx, t2, 42, "af:fix")
	if err != nil || f.State != "pending" {
		t.Fatalf("T2's follow-up is %+v (%v), want pending", f, err)
	}
	if got := e.followUpOf(t0); got == nil || got.State != "withdrawn" {
		t.Errorf("T0's follow-up is %+v, want withdrawn", got)
	}
	if got := e.followUpOf(t1); got == nil || got.State != "pending" {
		t.Errorf("the follow-up of 43 is %+v, want it pending still", got)
	}
	if n := e.count("SELECT COUNT(*) FROM af_followups WHERE workspace = 'widgets' AND issue = 42 AND state = 'pending'"); n != 1 {
		t.Errorf("%d pending follow-ups of 42, want 1", n)
	}
	// A repeated record for one triage job leaves its one follow-up as it was.
	again, err := e.Svc.RecordFollowUp(ctx, t2, 42, "af:fix")
	if err != nil || again.State != "pending" || e.count("SELECT COUNT(*) FROM af_followups WHERE triage_job_id = ?", t2) != 1 {
		t.Errorf("a second record gave %+v (%v), want the one pending follow-up", again, err)
	}
	if pending, err := e.Svc.PendingFollowUp(ctx, "widgets", 42); err != nil || pending.TriageJobID != t2 || pending.Label != "af:fix" {
		t.Errorf("the pending follow-up of 42 is %+v (%v), want T2's", pending, err)
	}
	if _, err := e.Svc.PendingFollowUp(ctx, "widgets", 99); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("the pending follow-up of 99: %v, want ErrNotFound", err)
	}
}

// TestAnOpenFixJobAbsorbsTheFollowUp verifies the absorbed state that 19-REQ-7.4 asks of the jobs layer: a follow-up asked
// for while a fix job of the issue is open names that job.
// Verifies: 19-REQ-8.1
func TestAnOpenFixJobAbsorbsTheFollowUp(t *testing.T) {
	e := newEnv(t)
	fix := e.create("widgets", jobs.KindFix, 42)
	triage := e.triageOf("widgets", 42)
	f, err := e.Svc.RecordFollowUp(ctx, triage, 42, "af:fix")
	if err != nil || f.State != "absorbed" || f.JobID == nil || *f.JobID != fix.ID {
		t.Fatalf("the follow-up is %+v (%v), want absorbed by %s", f, err, fix.ID)
	}
	if _, err := e.Svc.PendingFollowUp(ctx, "widgets", 42); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("an absorbed follow-up is pending: %v", err)
	}
}

// TestAFollowUpStandsWhateverBecomesOfItsTriageJob_TS_19_45 verifies TS-19-45.
// Verifies: 19-REQ-8.2
func TestAFollowUpStandsWhateverBecomesOfItsTriageJob_TS_19_45(t *testing.T) {
	clk := fakeclock.New(epoch)
	e := newEnv(t, withClock(clk), withRunner(fakerunner.New(fakerunner.Failed("sandbox", false)).Then(fakerunner.Block(fakerunner.Step{}))))
	triage := e.triageOf("widgets", 42)
	if _, err := e.Svc.RecordFollowUp(ctx, triage, 42, "af:fix"); err != nil {
		t.Fatal(err)
	}
	want := jobs.FollowUp{Label: "af:fix", Issue: 42, State: "pending"}
	check := func(after string) {
		t.Helper()
		if got := e.followUpOf(triage); got == nil || got.Label != want.Label || got.Issue != want.Issue || got.State != want.State || got.JobID != nil {
			t.Errorf("after %s the follow-up is %+v, want %+v", after, got, want)
		}
	}
	e.Start()
	e.waitState(triage, jobs.StateFailed)
	check("a failure")
	if _, err := e.Svc.Retry(ctx, triage, "user-1"); err != nil {
		t.Fatal(err)
	}
	e.waitState(triage, jobs.StateRunning)
	check("a retry")
	if _, err := e.Svc.Cancel(ctx, triage); err != nil {
		t.Fatal(err)
	}
	check("a cancel")
	clk.Advance(48 * time.Hour)
	check("two days")
}

// TestACreateWithAnInvalidParentIsRefusedAndStoresNothing_TS_19_46 verifies TS-19-46.
// Verifies: 19-REQ-8.3
func TestACreateWithAnInvalidParentIsRefusedAndStoresNothing_TS_19_46(t *testing.T) {
	e := newEnv(t)
	e.addWorkspace("gadgets")
	t1, t3 := e.triageOf("widgets", 1), e.triageOf("widgets", 3)
	t2 := e.triageOf("gadgets", 2)
	fix := e.create("widgets", jobs.KindFix, 4)
	bad := map[string]jobs.CreateRequest{
		"a parent with the source label": {Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage("5")},
			Source: jobs.Source{Kind: "label", By: "label af:fix"}, RequestLabel: "af:fix", ParentJobID: t1},
		"a parent that is a fix job":    linked("widgets", fix.ID, 5),
		"a parent of another workspace": linked("widgets", t2, 5),
		"a parent other than source.by": func() jobs.CreateRequest { r := linked("widgets", t1, 5); r.ParentJobID = t3; return r }(),
		"a parent that does not exist":  linked("widgets", "job_00000000000000000000", 5),
	}
	before := e.count("SELECT COUNT(*) FROM af_jobs")
	for name, req := range bad {
		_, err := e.Svc.Create(ctx, req)
		var invalid *jobs.ValidationError
		if !errors.As(err, &invalid) {
			t.Errorf("%s: %v, want an invalid request", name, err)
		}
	}
	if n := e.count("SELECT COUNT(*) FROM af_jobs"); n != before {
		t.Errorf("%d jobs after the refusals, want %d", n, before)
	}
	if _, err := e.Svc.Create(ctx, linked("widgets", t1, 5)); err != nil {
		t.Errorf("a valid parent was refused: %v", err)
	}
}

// TestEveryJobDocumentCarriesParentJobIDAndFollowUp_TS_19_47 verifies TS-19-47.
// Verifies: 19-REQ-8.4
func TestEveryJobDocumentCarriesParentJobIDAndFollowUp_TS_19_47(t *testing.T) {
	e := newEnv(t)
	triage := e.triageOf("widgets", 42)
	if _, err := e.Svc.RecordFollowUp(ctx, triage, 42, "af:fix"); err != nil {
		t.Fatal(err)
	}
	fix, err := e.Svc.CreateLinked(ctx, triage, linked("widgets", triage, 42))
	if err != nil {
		t.Fatal(err)
	}
	other := e.create("widgets", jobs.KindFix, 7)

	fields := func(id string) map[string]json.RawMessage {
		raw := e.call("GET", "/api/v1/jobs/"+id, nil).Body
		var doc map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	td := fields(triage)
	var fu map[string]json.RawMessage
	if err := json.Unmarshal(td["follow_up"], &fu); err != nil {
		t.Fatalf("the triage's follow_up is %s", td["follow_up"])
	}
	keys := []string{}
	for k := range fu {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if string(td["parent_job_id"]) != "null" || !slices.Equal(keys, []string{"issue", "job_id", "label", "state"}) ||
		string(fu["state"]) != `"made"` || string(fu["job_id"]) != `"`+fix.ID+`"` || string(fu["issue"]) != "42" || string(fu["label"]) != `"af:fix"` {
		t.Errorf("the triage shows parent_job_id %s and follow_up %s", td["parent_job_id"], td["follow_up"])
	}
	fd := fields(fix.ID)
	if string(fd["parent_job_id"]) != `"`+triage+`"` || string(fd["follow_up"]) != "null" || string(fd["source"]) != `{"kind":"triage","by":"`+triage+`"}` {
		t.Errorf("the fix shows parent_job_id %s, follow_up %s and source %s", fd["parent_job_id"], fd["follow_up"], fd["source"])
	}
	od := fields(other.ID)
	if string(od["parent_job_id"]) != "null" || string(od["follow_up"]) != "null" {
		t.Errorf("an ordinary job shows parent_job_id %s and follow_up %s", od["parent_job_id"], od["follow_up"])
	}
	// The list shows them too.
	list := string(e.call("GET", "/api/v1/workspaces/widgets/jobs", nil).Body)
	if !json.Valid([]byte(list)) || !strings.Contains(list, `"parent_job_id":"`+triage+`"`) || !strings.Contains(list, `"follow_up":{`) {
		t.Errorf("the list does not show the fields:\n%s", list)
	}
}

// TestThePurgeTakesATriagesFollowUpAndKeepsTheFixJobsParent_TS_19_48 verifies TS-19-48.
// Verifies: 19-REQ-8.5
func TestThePurgeTakesATriagesFollowUpAndKeepsTheFixJobsParent_TS_19_48(t *testing.T) {
	clk := fakeclock.New(epoch)
	e := newEnv(t, withClock(clk), withRetain(time.Hour), withRunner(fakerunner.New(fakerunner.Done()).Then(fakerunner.Block(fakerunner.Step{}))))
	triage := e.triageOf("widgets", 42)
	e.Start()
	e.waitRest(triage)
	if _, err := e.Svc.RecordFollowUp(ctx, triage, 42, "af:fix"); err != nil {
		t.Fatal(err)
	}
	fix, err := e.Svc.CreateLinked(ctx, triage, linked("widgets", triage, 42))
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(2 * time.Hour)
	if _, err := e.Svc.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT COUNT(*) FROM af_jobs WHERE id = ?", triage); n != 0 {
		t.Fatal("the triage job was not purged")
	}
	if n := e.count("SELECT COUNT(*) FROM af_followups WHERE triage_job_id = ?", triage); n != 0 {
		t.Errorf("%d follow-ups of the purged triage, want none", n)
	}
	if d := e.get(fix.ID); d.ParentJobID == nil || *d.ParentJobID != triage {
		t.Errorf("the fix job's parent is %v, want %s", d.ParentJobID, triage)
	}
}

// TestTheLatestJobOfAKindForAnIssueIsTheNewestByCreatedAt verifies the lookup that the forge's retry rule (19-REQ-1.5)
// rests on.
// Verifies: 19-REQ-8.4
func TestTheLatestJobOfAKindForAnIssueIsTheNewestByCreatedAt(t *testing.T) {
	clk := fakeclock.New(epoch)
	e := newEnv(t, withClock(clk), withRunner(fakerunner.New(fakerunner.Failed("sandbox", false), fakerunner.Done())))
	first := e.create("widgets", jobs.KindFix, 42)
	e.Start()
	e.waitRest(first.ID)
	clk.Advance(time.Minute)
	second := e.create("widgets", jobs.KindFix, 42)
	e.waitRest(second.ID)
	e.create("widgets", jobs.KindTriage, 42)
	if got, err := e.Svc.LatestJob(ctx, "widgets", jobs.KindFix, 42); err != nil || got.ID != second.ID {
		t.Errorf("the latest fix job of 42 is %s (%v), want %s", got.ID, err, second.ID)
	}
	if _, err := e.Svc.LatestJob(ctx, "widgets", jobs.KindFix, 43); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("the latest fix job of 43: %v, want ErrNotFound", err)
	}
}
