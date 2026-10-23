package poll

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
)

// removals are the removals of labels that the poll sent, as "remove(<issue>,<label>)", in order.
func (e *env) removals() []string {
	var out []string
	for _, p := range e.fromPoll() {
		if strings.HasPrefix(p, "remove(") {
			out = append(out, p)
		}
	}
	return out
}

// labelsOf are the labels of the issue on hub.
func (e *env) labelsOf(n int) []string {
	issue, _ := e.Hub.IssueOf(slug, n)
	return issue.Labels
}

// recordsOf are the poll records of the workspace.
func (e *env) recordsOf() []Record {
	e.t.Helper()
	got, err := NewRecords(e.DB).ForWorkspace(ctx, slug)
	if err != nil {
		e.t.Fatal(err)
	}
	return got
}

// removalPath is the path of the removal of a label from an issue.
func removalPath(n int, label string) string {
	return fmt.Sprintf("/api/v1/workspaces/%s/issues/%d/labels/%s", slug, n, label)
}

// TestTheIssuesOfTheListingsBecomeJobsInTheOrderOfTheTriageListAndThenTheFixListInAscendingNumber_TS_15_23 verifies
// TS-15-23: for any set of open issues that carry a request label and any order that hub returns them in, the jobs are
// made in the order of the triage list in ascending number and then of the fix list in ascending number.
// Verifies: 15-REQ-4.2
func TestTheIssuesOfTheListingsBecomeJobsInTheOrderOfTheTriageListAndThenTheFixListInAscendingNumber_TS_15_23(t *testing.T) {
	e := newEnv(t)
	rng := rand.New(rand.NewPCG(15, 23))
	var want []string
	for round := range 12 {
		base := 1000 * (round + 1)
		var triage, fix, all []int
		for _, n := range rng.Perm(40)[:3+rng.IntN(6)] {
			number := base + n
			all = append(all, number)
			if rng.IntN(2) == 0 {
				triage = append(triage, number)
				e.Hub.AddIssue(slug, fakehub.Issue{Number: number, Labels: []string{"af:triage"}})
			} else {
				fix = append(fix, number)
				e.Hub.AddIssue(slug, fakehub.Issue{Number: number, Labels: []string{"af:fix"}})
			}
		}
		rng.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
		e.Hub.SetListOrder(slug, all...)
		slices.Sort(triage)
		slices.Sort(fix)
		for _, n := range triage {
			want = append(want, fmt.Sprintf("triage %d", n))
		}
		for _, n := range fix {
			want = append(want, fmt.Sprintf("fix %d", n))
		}

		e.cycle()
	}
	var got []string
	for _, j := range e.jobList() {
		got = append(got, fmt.Sprintf("%s %d", j.Kind, j.Issue.Int64))
	}
	if !slices.Equal(got, want) {
		t.Errorf("the jobs were made in the order\n%v\nwant\n%v", got, want)
	}
}

// TestANewRequestMakesOneJobWithALabelSourceAndRequestLabelAndThenConsumesTheLabel_TS_15_33 verifies TS-15-33 and goal 1:
// open issue 7 with af:triage and open issue 42 with af:fix become a triage job and then a fix job, with the source and
// the request label of the label, each label is removed after its job exists, the jobs are logged, and the next cycle
// makes no job.
// Verifies: 15-REQ-5.5
func TestANewRequestMakesOneJobWithALabelSourceAndRequestLabelAndThenConsumesTheLabel_TS_15_33(t *testing.T) {
	var e *env
	jobsAtRemoval := map[string]int{}
	e = newEnv(t, withBeforeRequest(func(r *http.Request) {
		if r.Method == http.MethodDelete {
			jobsAtRemoval[r.URL.Path] = len(e.jobList())
		}
	}))
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 7, Labels: []string{"af:triage"}})
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 42, Labels: []string{"af:fix"}})

	e.cycle()

	got := e.jobList()
	if len(got) != 2 {
		t.Fatalf("%d jobs, want 2: %+v", len(got), got)
	}
	if j := got[0]; j.Kind != "triage" || j.Issue.Int64 != 7 || j.SourceKind != "label" || j.SourceBy != "label af:triage" || j.RequestLabel != "af:triage" {
		t.Errorf("the first job is %+v, want the triage of 7 from the label af:triage", j)
	}
	if j := got[1]; j.Kind != "fix" || j.Issue.Int64 != 42 || j.SourceKind != "label" || j.SourceBy != "label af:fix" || j.RequestLabel != "af:fix" {
		t.Errorf("the second job is %+v, want the fix of 42 from the label af:fix", j)
	}
	if want := []string{"remove(7,af:triage)", "remove(42,af:fix)"}; !slices.Equal(e.removals(), want) {
		t.Errorf("hub was sent %v, want one removal for each, in the order of the jobs: %v", e.removals(), want)
	}
	if jobsAtRemoval[removalPath(7, "af:triage")] != 1 || jobsAtRemoval[removalPath(42, "af:fix")] != 2 {
		t.Errorf("jobs that existed at the removals: %v, want 1 at the first and 2 at the second: the job comes first", jobsAtRemoval)
	}
	if len(e.labelsOf(7))+len(e.labelsOf(42)) != 0 {
		t.Errorf("the labels are %v and %v, want them consumed", e.labelsOf(7), e.labelsOf(42))
	}
	infos := e.Logs.find("INFO", "became a job")
	if len(infos) != 2 || infos[0]["job"] != got[0].ID || infos[0]["kind"] != "triage" || infos[0]["issue"] != float64(7) || infos[0]["workspace"] != slug ||
		infos[1]["job"] != got[1].ID || infos[1]["kind"] != "fix" || infos[1]["issue"] != float64(42) {
		t.Errorf("the INFO records are %v, want each to name the workspace, the kind, the issue and the job", infos)
	}

	e.cycle()
	if n := len(e.jobList()); n != 2 {
		t.Errorf("%d jobs after the next cycle, want 2", n)
	}
	if n := len(e.removals()); n != 2 {
		t.Errorf("%d removals after the next cycle, want 2", n)
	}
}

// TestADuplicateRefusalIsHandledAsAbsorbedAndExactlyOneJobExists_TS_15_34 verifies TS-15-34.
// Verifies: 15-REQ-5.6
func TestADuplicateRefusalIsHandledAsAbsorbedAndExactlyOneJobExists_TS_15_34(t *testing.T) {
	for name, tc := range map[string]struct {
		failRemoval, bare bool
	}{
		"the label is removed":                      {false, false},
		"the removal fails and is owed":             {true, false},
		"a refusal that names no job":               {false, true},
		"a refusal that names no job and a failure": {true, true},
	} {
		t.Run(name, func(t *testing.T) {
			// The look-up of the open job finds none, so that it is the refusal of Create that tells of the API job.
			e := newEnv(t, withRunner(fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))),
				withJobs(func(inner Jobs) Jobs { return &blind{Jobs: inner, bare: tc.bare} }))
			// A job of the API for 42 appeared before the poll could make its own.
			e.Hub.AddIssue(slug, fakehub.Issue{Number: 42, Labels: []string{"af:fix"}})
			status, body := e.call("POST", "/api/v1/workspaces/"+slug+"/fix", `{"issue": 42}`)
			if status != http.StatusAccepted {
				t.Fatalf("POST fix = %d %s", status, body)
			}
			if tc.failRemoval {
				e.Hub.Inject("DELETE", removalPath(42, "af:fix"), fakehub.Fault{Status: 503})
			}

			e.cycle()

			if n := len(e.jobList()); n != 1 {
				t.Errorf("%d jobs for the issue, want exactly one", n)
			}
			if got := e.removals(); !slices.Equal(got, []string{"remove(42,af:fix)"}) {
				t.Errorf("removals %v, want one", got)
			}
			wantJob := e.jobList()[0].ID
			if tc.bare {
				wantJob = ""
			}
			if infos := e.Logs.find("INFO", "absorbed"); len(infos) != 1 || infos[0]["job"] != wantJob {
				t.Errorf("the INFO records are %v, want one for the absorbed label that names the job %q", infos, wantJob)
			}
			recs := e.recordsOf()
			switch {
			case tc.failRemoval && (len(recs) != 1 || recs[0].JobID != "" || !recs[0].Owed || recs[0].Issue != 42 || recs[0].Label != "af:fix"):
				t.Errorf("records %+v, want one for 42 and af:fix with the removal owed and no job", recs)
			case !tc.failRemoval && len(recs) != 0:
				t.Errorf("records %+v, want none for a label that was removed", recs)
			case !tc.failRemoval && len(e.labelsOf(42)) != 0:
				t.Errorf("the label is still on the issue: %v", e.labelsOf(42))
			}
		})
	}
}

// TestANotSubscribedRefusalEndsTheCycleAtOnceWithNoLabelWritten_TS_15_35 verifies TS-15-35.
// Verifies: 15-REQ-5.7
func TestANotSubscribedRefusalEndsTheCycleAtOnceWithNoLabelWritten_TS_15_35(t *testing.T) {
	var refused *refusing
	e := newEnv(t, withJobs(func(inner Jobs) Jobs { refused = &refusing{Jobs: inner, err: jobs.ErrNotSubscribed}; return refused }))
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 7, Labels: []string{"af:triage"}})
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 42, Labels: []string{"af:fix"}})

	e.cycle()

	if refused.calls.Load() != 1 {
		t.Errorf("the jobs layer was asked %d times, want once: the cycle ends at the refusal", refused.calls.Load())
	}
	if got := e.fromPoll(); !slices.Equal(got, []string{"vars", "workspace", "issues(af:triage)"}) {
		t.Errorf("hub was asked %v, want nothing after the first list", got)
	}
	if len(e.removals()) != 0 || len(e.jobList()) != 0 || len(e.Logs.level("WARN")) != 0 {
		t.Errorf("removals %v, jobs %v, warnings %v; want none", e.removals(), e.jobList(), e.Logs.level("WARN"))
	}
}

// TestATokenUnreadableOrOtherErrorOfTheJobsLayerLogsOneWarningLeavesTheLabelAndEndsTheCycle_TS_15_36 verifies TS-15-36.
// Verifies: 15-REQ-5.8
func TestATokenUnreadableOrOtherErrorOfTheJobsLayerLogsOneWarningLeavesTheLabelAndEndsTheCycle_TS_15_36(t *testing.T) {
	for name, cause := range map[string]error{"token unreadable": jobs.ErrTokenUnreadable, "another error": errors.New("the disk is full")} {
		t.Run(name, func(t *testing.T) {
			var refused *refusing
			e := newEnv(t, withJobs(func(inner Jobs) Jobs { refused = &refusing{Jobs: inner, err: cause}; return refused }))
			e.Hub.AddIssue(slug, fakehub.Issue{Number: 7, Labels: []string{"af:triage"}})
			e.Hub.AddIssue(slug, fakehub.Issue{Number: 42, Labels: []string{"af:fix"}})

			e.cycle()

			if len(e.removals()) != 0 {
				t.Errorf("removals %v, want none: the label stays", e.removals())
			}
			if warns := e.Logs.level("WARN"); len(warns) != 1 {
				t.Errorf("%d warnings, want exactly one: %v", len(warns), warns)
			}
			if refused.calls.Load() != 1 || len(e.jobList()) != 0 {
				t.Errorf("the jobs layer was asked %d times and has %d jobs; want one call: issue 42 of the fix list is not processed in this cycle", refused.calls.Load(), len(e.jobList()))
			}
			if !slices.Contains(e.labelsOf(7), "af:triage") || !slices.Contains(e.labelsOf(42), "af:fix") {
				t.Error("a label was taken off")
			}
		})
	}
}

// TestARecordIsWrittenAfterTheJobIsMadeAndBeforeTheRemovalWithTheJobAndTheRemovalOwed_TS_15_38 verifies TS-15-38.
// Verifies: 15-REQ-6.2
func TestARecordIsWrittenAfterTheJobIsMadeAndBeforeTheRemovalWithTheJobAndTheRemovalOwed_TS_15_38(t *testing.T) {
	var e *env
	var atRemoval []Record
	var jobAtRemoval []jobRow
	e = newEnv(t, withBeforeRequest(func(r *http.Request) {
		if r.Method == http.MethodDelete {
			atRemoval = e.recordsOf()
			jobAtRemoval = e.jobList()
		}
	}))
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 42, Labels: []string{"af:fix"}})

	e.cycle()

	if len(jobAtRemoval) != 1 || len(atRemoval) != 1 {
		t.Fatalf("at the removal there were jobs %v and records %v, want one of each", jobAtRemoval, atRemoval)
	}
	if r := atRemoval[0]; r.Workspace != slug || r.Issue != 42 || r.Label != "af:fix" || r.JobID != jobAtRemoval[0].ID || !r.Owed || r.Seen != SeenOpen {
		t.Errorf("at the removal the record is %+v, want widgets, 42, af:fix, the job %s, the removal owed and the job not seen to end", r, jobAtRemoval[0].ID)
	}
	if after := e.recordsOf(); len(after) != 1 || after[0].Owed {
		t.Errorf("after the removal the records are %+v, want the owed flag cleared", after)
	}
}

// TestARecordThatCannotBeWrittenLogsOneWarningAndTheRemovalGoesAhead_TS_15_39 verifies TS-15-39.
// Verifies: 15-REQ-6.3
func TestARecordThatCannotBeWrittenLogsOneWarningAndTheRemovalGoesAhead_TS_15_39(t *testing.T) {
	e := newEnv(t)
	// The table can be read and not written: every insert, update and delete of a row is refused.
	for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
		if _, err := e.DB.ExecContext(ctx, "CREATE TRIGGER refuse_"+op+" BEFORE "+op+" ON af_poll_requests BEGIN SELECT RAISE(ABORT, 'the table is read only'); END"); err != nil {
			t.Fatal(err)
		}
	}
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 42, Labels: []string{"af:fix"}})

	e.cycle()

	if warns := e.Logs.level("WARN"); len(warns) != 1 {
		t.Errorf("%d warnings, want exactly one, about the record: %v", len(warns), warns)
	}
	if got := e.removals(); !slices.Equal(got, []string{"remove(42,af:fix)"}) {
		t.Errorf("removals %v, want the one of af:fix from 42", got)
	}
	if n := len(e.jobList()); n != 1 {
		t.Errorf("%d jobs, want the one", n)
	}
}

// TestACycleSpeaksOfNoTokenInItsLogsOrItsRowsWhateverHappens_TS_15_28 verifies TS-15-28: through success, a 403, a 503, a
// 409 whose message holds the token and a token that does not open, no log record and no row of a job or of a record of
// the poll contains the PAT.
// Verifies: 15-REQ-4.7
func TestACycleSpeaksOfNoTokenInItsLogsOrItsRowsWhateverHappens_TS_15_28(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))))
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 7, Labels: []string{"af:triage"}})
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 42, Labels: []string{"af:fix"}})
	e.cycle() // success

	e.Hub.Inject("GET", listPath, fakehub.Fault{Status: 403})
	e.cycle()
	e.Hub.Clear()
	e.Hub.Inject("GET", listPath, fakehub.Fault{Status: 503})
	e.cycle()
	e.Hub.Clear()
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 50, Labels: []string{"af:fix"}})
	e.Hub.Inject("DELETE", removalPath(50, "af:fix"), fakehub.Fault{Status: 409, Body: `{"error":{"code":409,"message":"conflict for ` + pat + `","error_type":"forge_conflict"}}`})
	e.cycle()
	e.Hub.Clear()
	if _, err := e.DB.ExecContext(ctx, "UPDATE af_subscriptions SET sealed_token = x'00' WHERE slug = ?", slug); err != nil {
		t.Fatal(err)
	}
	e.cycle() // the token does not open

	if strings.Contains(e.Logs.String(), pat) {
		t.Errorf("a log record holds the PAT:\n%s", e.Logs.String())
	}
	var dump strings.Builder
	for _, query := range []string{"SELECT * FROM af_jobs", "SELECT * FROM af_attempts", "SELECT * FROM af_events", "SELECT * FROM af_poll_requests"} {
		rows, err := e.DB.QueryContext(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintln(&dump, values...)
		}
		rows.Close()
	}
	if strings.Contains(dump.String(), pat) {
		t.Error("a row of a job or of the poll holds the PAT")
	}
	if len(e.Logs.records()) == 0 || dump.Len() == 0 {
		t.Error("nothing was logged or stored, so the check proves nothing")
	}
}

// TestACycleThatRacesTheDeleteOfItsWorkspaceMakesNoJobAfterTheDeleteBegan_TS_15_27 verifies TS-15-27.
// Verifies: 15-REQ-4.6
func TestACycleThatRacesTheDeleteOfItsWorkspaceMakesNoJobAfterTheDeleteBegan_TS_15_27(t *testing.T) {
	var gate *held
	e := newEnv(t, withJobs(func(inner Jobs) Jobs { gate = newHeld(inner); return gate }))
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 42, Labels: []string{"af:fix"}})
	cycled := make(chan struct{})
	go func() { e.Poll.Cycle(ctx, slug); close(cycled) }()
	select {
	case <-gate.reached: // the cycle is just before it makes the job
	case <-time.After(10 * time.Second):
		t.Fatal("the cycle did not get to make a job")
	}

	status, body := e.call("DELETE", "/api/v1/workspaces/"+slug, "")
	if status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", status, body)
	}
	close(gate.release)
	select {
	case <-cycled:
	case <-time.After(10 * time.Second):
		t.Fatal("the cycle did not end")
	}

	if jobs := e.jobList(); len(jobs) != 0 {
		t.Errorf("jobs %+v were made after the workspace was deleted", jobs)
	}
	if len(e.removals()) != 0 || !slices.Contains(e.labelsOf(42), "af:fix") {
		t.Errorf("removals %v, labels %v: the label must stay after the refusal", e.removals(), e.labelsOf(42))
	}
}

// TestARecordWhoseJobCannotBeReadEndsTheCycleAndTheLabelStays is the case that the rules must not turn into a second spend:
// the job of a record was cancelled in a runner and its cleanup put the label back, and the read of how the job stands
// fails. The label is not taken for a new request; the failure is logged, and the cycle ends.
// Verifies: 15-REQ-7.1
func TestARecordWhoseJobCannotBeReadEndsTheCycleAndTheLabelStays(t *testing.T) {
	var flaky *failing
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Block(cancelledResult())).Then(fakerunner.Block(fakerunner.Step{}))),
		withJobs(func(inner Jobs) Jobs { flaky = &failing{Jobs: inner}; return flaky }))
	e.takenByThePoll()
	id := e.cancelRunning()
	e.runnerReturned(id)
	flaky.standing.Store(true)

	e.cycle()

	if e.jobsFor(42) != 1 || e.removalsOf(42) != 1 || !slices.Contains(e.labelsOf(42), "af:fix") {
		t.Errorf("jobs %d, removals %d, labels %v: want no job made, no removal and the label left", e.jobsFor(42), e.removalsOf(42), e.labelsOf(42))
	}
	if len(e.Logs.level("WARN"))+len(e.Logs.level("ERROR")) == 0 {
		t.Error("the failure was not logged")
	}
	flaky.standing.Store(false)
	e.cycle()
	if _, ok := e.held(42); !ok || e.jobsFor(42) != 1 {
		t.Errorf("once the job can be read again the hold is in place: held %v, jobs %d", ok, e.jobsFor(42))
	}
}

// TestACycleThatEndsOnAFailureOfTheJobsLayerIsNotRecoveryOfAnEarlierFailure verifies the part of 15-REQ-8.7 that the paths
// of the rules must keep: a cycle that ends because the store or the jobs layer failed did not complete without failure, so
// it logs no recovery, and the failure that was logged before it is not logged again when it comes back.
// Verifies: 15-REQ-8.7
func TestACycleThatEndsOnAFailureOfTheJobsLayerIsNotRecoveryOfAnEarlierFailure(t *testing.T) {
	var flaky *failing
	e := newEnv(t, withJobs(func(inner Jobs) Jobs { flaky = &failing{Jobs: inner}; return flaky }))
	e.issue(42, "af:fix")
	e.Hub.Inject("GET", workspacePath+"/vars/resolved", fakehub.Fault{Status: 403})
	e.cycle() // an ERROR
	e.Hub.Clear()
	flaky.open.Store(true)

	e.cycle() // the jobs layer fails

	if n := len(e.failuresOf("INFO", "recovered")); n != 0 {
		t.Errorf("%d records of recovery after a cycle that ended on a failure, want none", n)
	}
	e.Hub.Inject("GET", workspacePath+"/vars/resolved", fakehub.Fault{Status: 403})
	e.cycle()
	if n := len(e.failuresOf("ERROR", "does not accept the token")); n != 1 {
		t.Errorf("%d ERROR records of the 403, want it logged once: nothing completed without a failure since", n)
	}
}

// TestAForgeThatRefusesHubsCredentialEndsTheCycleWithAnErrorAndDoesNotGoOnToTheNextIssue is hub's 502 forge_unauthenticated,
// which the hub client takes as a rejection since a retry cannot cure it: it is an ERROR that a person must act on, and
// every removal that follows would fail the same, so the cycle ends with the removal owed.
// Verifies: 15-REQ-8.5
func TestAForgeThatRefusesHubsCredentialEndsTheCycleWithAnErrorAndDoesNotGoOnToTheNextIssue(t *testing.T) {
	e := newEnv(t)
	e.issue(42, "af:fix")
	e.issue(43, "af:fix")
	e.Hub.Inject("DELETE", removalPath(42, "af:fix"), fakehub.Fault{Status: 502,
		Body: `{"error":{"code":502,"message":"the forge refused the token of hub","error_type":"forge_unauthenticated"}}`})

	e.cycle()

	if e.jobsFor(42) != 1 || e.jobsFor(43) != 0 {
		t.Errorf("jobs for 42: %d, for 43: %d; want the one for 42 and the cycle ended before 43", e.jobsFor(42), e.jobsFor(43))
	}
	errs := e.failuresOf("ERROR", "")
	if len(errs) != 1 || !strings.Contains(msgOf(errs[0]), "the forge refused the token of hub") {
		t.Errorf("ERROR records %v, want one that carries hub's message", errs)
	}
	if recs := e.recordsOf(); len(recs) != 1 || !recs[0].Owed {
		t.Errorf("records %+v, want the removal owed", recs)
	}
}

// TestIssuesThatHubReturnsAgainstItsFilterAreLeftAlone: the poll asks hub for the open issues that carry a label, and
// trusts the answer no further than it must. An issue that is closed, or that does not carry the label (whatever its case),
// is not a request: a hub that dropped a parameter would otherwise turn every issue of the repository into a job.
// Verifies: 15-REQ-4.5
func TestIssuesThatHubReturnsAgainstItsFilterAreLeftAlone(t *testing.T) {
	e := newEnv(t)
	e.Hub.Inject("GET", listPath, fakehub.Fault{Status: 200, Body: `[
		{"number": 4, "title": "closed", "state": "closed", "labels": ["af:fix"], "is_pr": false},
		{"number": 5, "title": "a bug", "state": "open", "labels": ["bug"], "is_pr": false},
		{"number": 6, "title": "no labels", "state": "open", "is_pr": false},
		{"number": 7, "title": "a request", "state": "open", "labels": ["AF:Fix", "bug"], "is_pr": false}]`})

	e.cycle()

	var fixes []int64
	for _, j := range e.jobList() {
		if j.Kind == "fix" {
			fixes = append(fixes, j.Issue.Int64)
		}
		if j.Kind == "triage" {
			t.Errorf("a triage job %+v was made from issues that do not carry af:triage", j)
		}
	}
	if !slices.Equal(fixes, []int64{7}) {
		t.Errorf("fix jobs for %v, want only issue 7: the closed issue and the ones without the label are not requests", fixes)
	}
}

// TestAPullRequestAClosedIssueAndAnIssueWithoutALabelMakeNoJobAndLoseNoLabel is TS-15-26 with the rules in place: the
// assertion of the spec, that no job is made and no label removed, which the recorder of the test above cannot fail.
// Verifies: 15-REQ-4.5
func TestAPullRequestAClosedIssueAndAnIssueWithoutALabelMakeNoJobAndLoseNoLabel(t *testing.T) {
	e := newEnv(t)
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 3, Title: "a pull request", Labels: []string{"af:fix"}, IsPR: true})
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 4, Title: "a closed issue", Labels: []string{"af:triage"}, State: "closed"})
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 5, Title: "a bug", Labels: []string{"bug"}})

	e.cycle()

	if jobs := e.jobList(); len(jobs) != 0 {
		t.Errorf("jobs %+v were made", jobs)
	}
	if got := e.removals(); len(got) != 0 {
		t.Errorf("removals %v were sent", got)
	}
	for n, label := range map[int]string{3: "af:fix", 4: "af:triage", 5: "bug"} {
		if !slices.Contains(e.labelsOf(n), label) {
			t.Errorf("issue %d lost %s: %v", n, label, e.labelsOf(n))
		}
	}
}
