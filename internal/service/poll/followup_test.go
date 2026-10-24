package poll

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
)

// askedFix makes a triage job of the issue as the poll does, waits until it is in the state, and records its follow-up of
// af:fix, as the worker does for a ready triage with AF_AUTO_FIX. It returns the triage job's id.
func (e *env) askedFix(n int, state jobs.State) string {
	e.t.Helper()
	if _, ok := e.Hub.IssueOf(slug, n); !ok {
		e.issue(n)
	}
	id := e.startedJob(jobs.KindTriage, n, "af:triage")
	e.waitState(id, state)
	f, err := e.Jobs.RecordFollowUp(ctx, id, int64(n), "af:fix")
	if err != nil || f.State != jobs.FollowUpPending {
		e.t.Fatalf("the follow-up is %+v (%v), want pending", f, err)
	}
	return id
}

// followUp is the follow-up the document of the triage job shows.
func (e *env) followUp(triage string) jobs.FollowUp {
	e.t.Helper()
	f := e.doc(triage).FollowUp
	if f == nil {
		e.t.Fatalf("the triage %s shows no follow-up", triage)
	}
	return *f
}

// fixJobs are the fix jobs of the issue, oldest first.
func (e *env) fixJobs(n int) []jobRow {
	var out []jobRow
	for _, j := range e.jobList() {
		if j.Kind == "fix" && j.Issue.Int64 == int64(n) {
			out = append(out, j)
		}
	}
	return out
}

// TestAFixLabelWithAPendingFollowUpMakesOneLinkedFixJob_TS_19_50 verifies TS-19-50.
// Verifies: 19-REQ-9.1
func TestAFixLabelWithAPendingFollowUpMakesOneLinkedFixJob_TS_19_50(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Done()).Then(fakerunner.Block(fakerunner.Step{}))))
	triage := e.askedFix(42, jobs.StateDone)
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.cycle()
	fixes := e.fixJobs(42)
	if len(fixes) != 1 {
		t.Fatalf("%d fix jobs of 42, want one", len(fixes))
	}
	d := e.doc(fixes[0].ID)
	if d.Source != (jobs.Source{Kind: "triage", By: triage}) || d.ParentJobID == nil || *d.ParentJobID != triage ||
		d.RequestLabel == nil || *d.RequestLabel != "af:fix" || string(d.Input.Issue) != "42" {
		t.Errorf("the fix job is %+v", d)
	}
	if f := e.followUp(triage); f.State != jobs.FollowUpMade || f.JobID == nil || *f.JobID != d.ID {
		t.Errorf("the follow-up is %+v, want made with %s", f, d.ID)
	}
	if e.hasLabel(42, "af:fix") {
		t.Error("af:fix is still on 42")
	}
	recs := e.recordsOf()
	if len(recs) != 1 || recs[0].JobID != d.ID || recs[0].Label != "af:fix" {
		t.Errorf("the records are %+v, want the poll's record of the fix job", recs)
	}
	if got := e.Logs.find("INFO", "follow-up"); len(got) != 1 || got[0]["job"] != d.ID || got[0]["triage"] != triage || got[0]["issue"] != float64(42) {
		t.Errorf("the INFO records are %v", got)
	}
}

// TestAPendingFollowUpAndAnOpenBlockedFixJobAbsorbTheLabelWithNoAnswer_TS_19_51 verifies TS-19-51.
// Verifies: 19-REQ-9.2
func TestAPendingFollowUpAndAnOpenBlockedFixJobAbsorbTheLabelWithNoAnswer_TS_19_51(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Done(), blockedStep()).Then(fakerunner.Block(fakerunner.Step{}))))
	triage := e.askedFix(42, jobs.StateDone)
	fix := e.blockFix(42) // made after the follow-up, as a person might
	e.comment(42, "alice", "an answer, which the service's own label does not give")
	e.Hub.AddLabel(slug, 42, "af:fix")
	jobsBefore := len(e.jobList())
	e.cycle()
	if len(e.doc(fix).Attempts) != 1 || len(e.jobList()) != jobsBefore {
		t.Errorf("%d attempts of the fix and %d jobs, want no answer and no job", len(e.doc(fix).Attempts), len(e.jobList()))
	}
	if f := e.followUp(triage); f.State != jobs.FollowUpAbsorbed || f.JobID == nil || *f.JobID != fix {
		t.Errorf("the follow-up is %+v, want absorbed by %s", f, fix)
	}
	if e.hasLabel(42, "af:fix") {
		t.Error("af:fix is still on 42")
	}
}

// duplicateLinked is a jobs layer whose linked fix job meets a duplicate refusal naming the job.
type duplicateLinked struct {
	Jobs
	job string
}

func (d duplicateLinked) CreateLinked(context.Context, string, jobs.CreateRequest) (jobs.Doc, error) {
	return jobs.Doc{}, &jobs.DuplicateError{JobID: d.job}
}

// TestADuplicateRefusalOfTheLinkedFixJobAbsorbsTheLabelAndTheFollowUp_TS_19_52 verifies TS-19-52.
// Verifies: 19-REQ-9.3
func TestADuplicateRefusalOfTheLinkedFixJobAbsorbsTheLabelAndTheFollowUp_TS_19_52(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Done())), withJobs(func(j Jobs) Jobs { return duplicateLinked{Jobs: j, job: "job_00000000000000000042"} }))
	triage := e.askedFix(42, jobs.StateDone)
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.cycle()
	if n := len(e.fixJobs(42)); n != 0 {
		t.Errorf("%d fix jobs, want none", n)
	}
	if f := e.followUp(triage); f.State != jobs.FollowUpAbsorbed || f.JobID == nil || *f.JobID != "job_00000000000000000042" {
		t.Errorf("the follow-up is %+v, want absorbed by the job of the refusal", f)
	}
	if e.hasLabel(42, "af:fix") {
		t.Error("af:fix is still on 42")
	}
}

// TestTheStreamMakesTheLinkedFixJobWhileTheTriageRunsAndOverAnEarlierHold_TS_19_53 verifies TS-19-53.
// Verifies: 19-REQ-9.4
func TestTheStreamMakesTheLinkedFixJobWhileTheTriageRunsAndOverAnEarlierHold_TS_19_53(t *testing.T) {
	e := streaming(t, withRunner(fakerunner.New(fakerunner.Block(cancelledResult())).Then(fakerunner.Block(fakerunner.Step{}))))
	e.takenByThePoll() // an earlier fix job of af:fix, cancelled in its runner: af:fix comes back and is held
	earlier := e.cancelRunning()
	e.runnerReturned(earlier)
	e.cycle()
	if _, ok := e.held(42); !ok {
		t.Fatal("af:fix on 42 is not held")
	}
	triage := e.askedFix(42, jobs.StateRunning)
	e.labeled("evt-fix", 42, "af:fix")
	waitFor(t, "the event", func() bool { return e.taken("evt-fix") })
	fixes := e.fixJobs(42)
	if len(fixes) != 2 || fixes[1].SourceKind != "triage" || fixes[1].SourceBy != triage {
		t.Fatalf("the fix jobs of 42 are %+v, want the cancelled one and one linked to %s", fixes, triage)
	}
	if d := e.doc(fixes[1].ID); d.ParentJobID == nil || *d.ParentJobID != triage {
		t.Errorf("the linked fix job's parent is %v", d.ParentJobID)
	}
	if _, ok := e.held(42); ok {
		t.Error("the hold of af:fix on 42 did not end")
	}
	if e.hasLabel(42, "af:fix") {
		t.Error("af:fix is still on 42")
	}
}

// TestASecondSightingAfterTheLinkedFixJobIsAbsorbed_TS_19_54 verifies TS-19-54.
// Verifies: 19-REQ-9.5
func TestASecondSightingAfterTheLinkedFixJobIsAbsorbed_TS_19_54(t *testing.T) {
	e := streaming(t, withRunner(fakerunner.New(fakerunner.Done()).Then(fakerunner.Block(fakerunner.Step{}))))
	e.Hub.SetRemovalLag(slug, 2)
	triage := e.askedFix(42, jobs.StateDone)
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.labeled("evt-1", 42, "af:fix")
	waitFor(t, "the first event", func() bool { return e.taken("evt-1") })
	comments := e.comments(42)
	e.cycle() // its listing still shows af:fix on 42
	e.labeled("evt-2", 42, "af:fix")
	waitFor(t, "the second event", func() bool { return e.taken("evt-2") })
	if fixes := e.fixJobs(42); len(fixes) != 1 || fixes[0].SourceBy != triage {
		t.Errorf("the fix jobs of 42 are %+v, want the one linked job", fixes)
	}
	if e.comments(42) != comments {
		t.Errorf("%d comments were posted, want none", e.comments(42)-comments)
	}
}

// TestAFixLabelWithNoPendingFollowUpMakesAJobOfTheLabelOrTheEvent_TS_19_55 verifies TS-19-55.
// Verifies: 19-REQ-9.6
func TestAFixLabelWithNoPendingFollowUpMakesAJobOfTheLabelOrTheEvent_TS_19_55(t *testing.T) {
	for _, followUp := range []string{"none", "withdrawn"} {
		for _, by := range []string{"the poll", "the stream"} {
			t.Run(followUp+", "+by, func(t *testing.T) {
				e := streaming(t, withRunner(fakerunner.New(fakerunner.Done()).Then(fakerunner.Block(fakerunner.Step{}))))
				e.issue(42)
				if followUp == "withdrawn" {
					triage := e.askedFix(42, jobs.StateDone)
					if err := e.Jobs.WithdrawFollowUp(ctx, triage); err != nil {
						t.Fatal(err)
					}
				}
				e.Hub.AddLabel(slug, 42, "af:fix")
				want := "label"
				if by == "the poll" {
					e.cycle()
				} else {
					want = "event"
					e.labeled("evt-1", 42, "af:fix")
					waitFor(t, "the event", func() bool { return e.taken("evt-1") })
				}
				fixes := e.fixJobs(42)
				if len(fixes) != 1 || fixes[0].SourceKind != want {
					t.Fatalf("the fix jobs are %+v, want one of the source %s", fixes, want)
				}
				if d := e.doc(fixes[0].ID); d.ParentJobID != nil {
					raw, _ := json.Marshal(d.ParentJobID)
					t.Errorf("the fix job has the parent %s, want none", raw)
				}
			})
		}
	}
}
