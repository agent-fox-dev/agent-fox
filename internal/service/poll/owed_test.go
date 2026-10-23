package poll

import (
	"net/http"
	"slices"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// needsHuman is a step of the runner that asks a question, which blocks the job.
var needsHuman = fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which?", "a", "b")))

// withJobsConfig changes the configuration of the jobs layer before it is built.
func withJobsConfig(f func(*jobs.Config)) option { return func(c *envConfig) { c.jobs = f } }

// startedJob makes a job of the kind for the issue the way the poll does, and returns its id.
func (e *env) startedJob(kind jobs.Kind, issue int, label string) string {
	e.t.Helper()
	doc, err := e.Jobs.Create(ctx, request(slug, &listing{Kind: kind, Label: label}, issue))
	if err != nil {
		e.t.Fatal(err)
	}
	return doc.ID
}

// waitState waits until the job is in the state.
func (e *env) waitState(id string, state jobs.State) {
	e.t.Helper()
	waitFor(e.t, id+" to be "+string(state), func() bool {
		s, err := e.Jobs.Standing(ctx, id)
		return err == nil && s.State == state
	})
}

// record writes a record of the poll.
func (e *env) record(r Record) Record {
	e.t.Helper()
	if r.Workspace == "" {
		r.Workspace = slug
	}
	if r.Seen == "" {
		r.Seen = SeenOpen
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = e.Clock.Now()
	}
	out, err := NewRecords(e.DB).Insert(ctx, r)
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

// issue adds an open issue with the labels to the workspace on hub.
func (e *env) issue(n int, labels ...string) {
	e.Hub.AddIssue(slug, fakehub.Issue{Number: n, Labels: labels})
}

// TestALabelOnAnIssueWithAnOpenJobOfTheSameKindIsAbsorbedAndLogged_TS_15_29 verifies TS-15-29: a running job, and a
// blocked job, of the kind absorb the label, which is removed with no job made and an INFO record that names the
// workspace, the kind, the issue and the job; an open job of the other kind does not absorb it.
// Verifies: 15-REQ-5.1
func TestALabelOnAnIssueWithAnOpenJobOfTheSameKindIsAbsorbedAndLogged_TS_15_29(t *testing.T) {
	absorbed := func(t *testing.T, e *env, id string) {
		t.Helper()
		if jobs := e.jobList(); len(jobs) != 1 {
			t.Errorf("%d jobs, want the one that absorbed the label: %+v", len(jobs), jobs)
		}
		if got := e.removals(); !slices.Equal(got, []string{"remove(42,af:fix)"}) {
			t.Errorf("removals %v, want one of af:fix from 42", got)
		}
		infos := e.Logs.find("INFO", "absorbed")
		if len(infos) != 1 || infos[0]["job"] != id || infos[0]["kind"] != "fix" || infos[0]["issue"] != float64(42) || infos[0]["workspace"] != slug {
			t.Errorf("the INFO records are %v, want one that names the workspace, the kind, the issue and job %s", infos, id)
		}
	}

	t.Run("a running job of the API", func(t *testing.T) {
		e := newEnv(t, withRunner(fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))))
		e.issue(42)
		if status, body := e.call("POST", "/api/v1/workspaces/"+slug+"/fix", `{"issue": 42}`); status != http.StatusAccepted {
			t.Fatalf("POST fix = %d %s", status, body)
		}
		id := e.jobList()[0].ID
		e.waitState(id, jobs.StateRunning)
		e.Hub.AddLabel(slug, 42, "af:fix")

		e.cycle()

		absorbed(t, e, id)
		if len(e.labelsOf(42)) != 0 {
			t.Errorf("the label is still on the issue: %v", e.labelsOf(42))
		}
	})

	t.Run("a blocked job", func(t *testing.T) {
		e := newEnv(t, withRunner(fakerunner.New(needsHuman)))
		e.issue(42)
		id := e.startedJob(jobs.KindFix, 42, "af:fix")
		e.waitState(id, jobs.StateBlocked)
		e.Hub.AddLabel(slug, 42, "af:fix")

		e.cycle()

		absorbed(t, e, id)
	})

	t.Run("an open job of the other kind does not", func(t *testing.T) {
		e := newEnv(t, withRunner(fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))))
		e.issue(42)
		triage := e.startedJob(jobs.KindTriage, 42, "af:triage")
		e.waitState(triage, jobs.StateRunning)
		e.Hub.AddLabel(slug, 42, "af:fix")

		e.cycle()

		got := e.jobList()
		if len(got) != 2 || got[1].Kind != "fix" || got[1].Issue.Int64 != 42 || got[1].SourceBy != "label af:fix" {
			t.Errorf("jobs %+v, want the fix job to be made beside the open triage job", got)
		}
		if infos := e.Logs.find("INFO", "absorbed"); len(infos) != 0 {
			t.Errorf("an open job of the other kind absorbed the label: %v", infos)
		}
	})
}

// TestAFailedAbsorbingRemovalIsOwedAndLaterRemovedWithoutAJobOnceTheOpenJobEnded_TS_15_30 verifies TS-15-30 and the
// third part of goal 2.
// Verifies: 15-REQ-5.2
func TestAFailedAbsorbingRemovalIsOwedAndLaterRemovedWithoutAJobOnceTheOpenJobEnded_TS_15_30(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))))
	e.issue(42)
	if status, body := e.call("POST", "/api/v1/workspaces/"+slug+"/fix", `{"issue": 42}`); status != http.StatusAccepted {
		t.Fatalf("POST fix = %d %s", status, body)
	}
	id := e.jobList()[0].ID
	e.waitState(id, jobs.StateRunning)
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.Hub.Inject("DELETE", removalPath(42, "af:fix"), fakehub.Fault{Status: 503, Once: true})

	e.cycle()

	recs := e.recordsOf()
	if len(recs) != 1 || recs[0].JobID != "" || !recs[0].Owed || recs[0].Issue != 42 || recs[0].Label != "af:fix" {
		t.Fatalf("records %+v, want one for 42 and af:fix with the removal owed and no job", recs)
	}
	if len(e.jobList()) != 1 || !slices.Contains(e.labelsOf(42), "af:fix") {
		t.Fatalf("jobs %v, labels %v: no job is made and the label stays after a failed removal", e.jobList(), e.labelsOf(42))
	}

	e.Runner.ReleaseAll() // the API job ends done
	e.waitState(id, jobs.StateDone)
	e.cycle()

	if got := e.removals(); len(got) != 2 {
		t.Errorf("removals %v, want the failed one and the one of the next cycle", got)
	}
	if len(e.jobList()) != 1 || len(e.labelsOf(42)) != 0 || len(e.recordsOf()) != 0 {
		t.Errorf("jobs %d, labels %v, records %v: the label is removed, with no job, and the record is dropped", len(e.jobList()), e.labelsOf(42), e.recordsOf())
	}
	e.cycle()
	if len(e.jobList()) != 1 || len(e.removals()) != 2 {
		t.Errorf("the cycle after made %d jobs and %d removals, want nothing", len(e.jobList()), len(e.removals()))
	}
}

// TestAnOwedRemovalIsRetriedAndCreatesNoJob_TS_15_32 verifies TS-15-32.
// Verifies: 15-REQ-5.4
func TestAnOwedRemovalIsRetriedAndCreatesNoJob_TS_15_32(t *testing.T) {
	e := newEnv(t)
	e.issue(42, "af:fix")
	id := e.startedJob(jobs.KindFix, 42, "af:fix")
	e.waitState(id, jobs.StateDone)
	e.record(Record{Issue: 42, Label: "af:fix", JobID: id, Owed: true})

	e.cycle()

	if got := e.removals(); !slices.Equal(got, []string{"remove(42,af:fix)"}) {
		t.Errorf("removals %v, want one of af:fix from 42", got)
	}
	if n := len(e.jobList()); n != 1 {
		t.Errorf("%d jobs, want the one that was there", n)
	}
}

// TestASuccessfulOrNotFoundRemovalConfirmsEveryOwedRemovalForTheIssueAndLabelWithNoFailureRecord_TS_15_41 verifies
// TS-15-41.
// Verifies: 15-REQ-6.5
func TestASuccessfulOrNotFoundRemovalConfirmsEveryOwedRemovalForTheIssueAndLabelWithNoFailureRecord_TS_15_41(t *testing.T) {
	for name, status := range map[string]int{"hub answers 404": 404, "hub answers 204": 0} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.issue(42, "af:fix")
			e.record(Record{Issue: 42, Label: "af:fix", Owed: true, Seen: SeenEnded})
			e.record(Record{Issue: 42, Label: "af:fix", Owed: true, Seen: SeenEnded})
			if status != 0 {
				e.Hub.Inject("DELETE", removalPath(42, "af:fix"), fakehub.Fault{Status: status})
			}

			e.cycle()

			if recs := e.recordsOf(); len(recs) != 0 {
				t.Errorf("records %+v, want both owed removals of 42 confirmed, and so dropped", recs)
			}
			if warns, errs := e.Logs.level("WARN"), e.Logs.level("ERROR"); len(warns)+len(errs) != 0 {
				t.Errorf("failures were logged: %v %v", warns, errs)
			}
			if n := len(e.jobList()); n != 0 {
				t.Errorf("%d jobs were made for a label whose removal was owed", n)
			}
		})
	}
}

// TestAFailedRemovalLeavesTheJobStandingAndOwedAndTheNextCycleRemovesWithoutAJob_TS_15_42 verifies TS-15-42 and goal 3.
// Verifies: 15-REQ-6.6
func TestAFailedRemovalLeavesTheJobStandingAndOwedAndTheNextCycleRemovesWithoutAJob_TS_15_42(t *testing.T) {
	e := newEnv(t)
	e.issue(42, "af:fix")
	e.Hub.Inject("DELETE", removalPath(42, "af:fix"), fakehub.Fault{Status: 503, Once: true})

	e.cycle()

	got := e.jobList()
	if len(got) != 1 {
		t.Fatalf("%d jobs, want the one that was made before the removal failed", len(got))
	}
	e.waitState(got[0].ID, jobs.StateDone)
	if !slices.Contains(e.labelsOf(42), "af:fix") {
		t.Fatal("the label was removed")
	}

	e.cycle()
	if n := len(e.jobList()); n != 1 || len(e.labelsOf(42)) != 0 || len(e.removals()) != 2 {
		t.Errorf("jobs %d, labels %v, removals %v: the next cycle removes the label and makes no job", n, e.labelsOf(42), e.removals())
	}
	if recs := e.recordsOf(); len(recs) != 0 {
		t.Errorf("records %+v, want the record dropped: nothing is owed and its job ended", recs)
	}
	e.cycle()
	if n := len(e.jobList()); n != 1 || len(e.removals()) != 2 {
		t.Errorf("the cycle after made %d jobs and %d removals, want nothing", n, len(e.removals()))
	}

	e.Hub.AddLabel(slug, 42, "af:fix") // a person asks again
	e.cycle()
	if n := len(e.jobList()); n != 2 {
		t.Errorf("%d jobs after the label was added again, want exactly one new job", n)
	}
}

// TestACompleteListingWithoutTheIssueSettlesEveryOwedRemovalForIt_TS_15_43 verifies TS-15-43.
// Verifies: 15-REQ-6.7
func TestACompleteListingWithoutTheIssueSettlesEveryOwedRemovalForIt_TS_15_43(t *testing.T) {
	e := newEnv(t)
	e.issue(42) // a person removed the label by hand
	id := e.startedJob(jobs.KindFix, 42, "af:fix")
	e.waitState(id, jobs.StateDone)
	e.record(Record{Issue: 42, Label: "af:fix", JobID: id, Owed: true})
	e.record(Record{Issue: 42, Label: "af:fix", Owed: true, Seen: SeenEnded})
	e.record(Record{Issue: 42, Label: "af:triage", Owed: true, Seen: SeenEnded})

	e.cycle()

	if recs := e.recordsOf(); len(recs) != 0 {
		t.Errorf("records %+v, want every owed removal settled by the complete listings that do not contain 42, and dropped", recs)
	}
	if len(e.removals()) != 0 {
		t.Errorf("removals %v, want none: the label is gone", e.removals())
	}
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.cycle()
	if n := len(e.jobList()); n != 2 {
		t.Errorf("%d jobs after the label was added again, want a new one", n)
	}
}
