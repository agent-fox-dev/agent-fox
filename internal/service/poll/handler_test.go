package poll

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestNewRequest_MakesTheJobWritesTheRecordAndThenConsumesTheLabel(t *testing.T) {
	e := newEnv(t)
	e.issue(7, "af:triage")
	e.issue(42, "af:fix")
	var atRemoval [][]Record
	e.Hub.OnRequest(func(r fakehub.Request) {
		if r.Method == "DELETE" {
			atRemoval = append(atRemoval, e.records())
		}
	})

	e.cycle()

	var asked []string
	for _, r := range e.Jobs.creates {
		asked = append(asked, fmt.Sprintf("%s %s %s/%s %s", r.Kind, r.Body.Issue, r.Source.Kind, r.Source.By, r.RequestLabel))
	}
	if want := []string{"triage 7 label/label af:triage af:triage", "fix 42 label/label af:fix af:fix"}; !slices.Equal(asked, want) {
		t.Errorf("the jobs asked for are %q, want %q", asked, want)
	}
	if got := e.removals(); !slices.Equal(got, []string{"remove(7,af:triage)", "remove(42,af:fix)"}) {
		t.Errorf("removals %v, want one for each, in the order of the jobs", got)
	}
	if len(atRemoval) != 2 || len(atRemoval[1]) != 2 {
		t.Fatalf("the records at the removals were %+v, want the record of each job before its removal", atRemoval)
	}
	if r := atRemoval[1][1]; r.Issue != 42 || r.Label != "af:fix" || r.JobID != "job_2" || !r.Owed || r.Seen != SeenOpen {
		t.Errorf("at its removal the record of 42 is %+v, want the job, the removal owed and the job open", r)
	}
	if recs := e.records(); len(recs) != 2 || recs[0].Owed || recs[1].Owed {
		t.Errorf("after the removals the records are %+v, want them confirmed", recs)
	}
	if infos := e.Log.Find("INFO", "became a job"); len(infos) != 2 || infos[1]["job"] != "job_2" || infos[1]["issue"] != float64(42) || infos[1]["kind"] != "fix" {
		t.Errorf("the INFO records are %v, want each job named", infos)
	}
	e.cycle()
	if len(e.Jobs.creates) != 2 || len(e.removals()) != 2 {
		t.Errorf("the next cycle asked for %d jobs and removed %d labels in all, want nothing more", len(e.Jobs.creates), len(e.removals()))
	}
}

func TestNewRequest_ThatTheJobsLayerRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		err         error
		failRemoval bool
		absorbed    string // the job named as absorbing the label, "-" for none
		routes      []string
		warns       int
	}{
		"a duplicate absorbs the label":         {&jobs.DuplicateError{JobID: "job_api"}, false, "job_api", nil, 0},
		"a duplicate that names no job":         {jobs.ErrDuplicate, false, "", nil, 0},
		"a duplicate whose removal fails":       {&jobs.DuplicateError{JobID: "job_api"}, true, "job_api", nil, 1},
		"not subscribed ends the cycle at once": {jobs.ErrNotSubscribed, false, "-", []string{"vars", "workspace", "issues(af:triage)"}, 0},
		"a token unreadable ends the cycle":     {jobs.ErrTokenUnreadable, false, "-", []string{"vars", "workspace", "issues(af:triage)"}, 1},
		"another error ends the cycle":          {errors.New("disk full"), false, "-", []string{"vars", "workspace", "issues(af:triage)"}, 1},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.issue(7, "af:triage")
			e.issue(42, "af:fix")
			e.Jobs.fail("Create", tc.err)
			if tc.failRemoval {
				e.Hub.Fail(removalPath(7, "af:triage"), 503)
			}

			e.cycle()

			infos := e.Log.Find("INFO", "absorbed")
			if tc.absorbed == "-" {
				if len(infos) != 0 || !slices.Equal(e.routes(), tc.routes) || !slices.Contains(e.labels(7), "af:triage") || e.Jobs.count("Create") != 1 {
					t.Errorf("absorbed %v, hub asked %v, labels of 7 %v: want the cycle ended with the label left", infos, e.routes(), e.labels(7))
				}
			} else if len(infos) == 0 || infos[0]["job"] != tc.absorbed || !slices.Contains(e.removals(), "remove(7,af:triage)") {
				t.Errorf("absorbed %v and removals %v, want the label removed and absorbed by %q", infos, e.removals(), tc.absorbed)
			}
			if n := len(e.Log.Find("WARN", "")); n != tc.warns {
				t.Errorf("%d warnings, want %d: %s", n, tc.warns, e.Log)
			}
			recs := e.records()
			if tc.failRemoval != (len(recs) == 1 && recs[0].Issue == 7 && recs[0].JobID == "" && recs[0].Owed) || (!tc.failRemoval && len(recs) != 0) {
				t.Errorf("records %+v, want an owed removal with no job only when the removal failed", recs)
			}
		})
	}
}

func TestOpenJob_OfTheKindAbsorbsTheLabelAndOneOfTheOtherKindDoesNot(t *testing.T) {
	for name, tc := range map[string]struct {
		kind  jobs.Kind
		state jobs.State
		owed  bool
	}{
		"queued":                       {jobs.KindFix, jobs.StateQueued, false},
		"running":                      {jobs.KindFix, jobs.StateRunning, false},
		"blocked with a removal owed":  {jobs.KindFix, jobs.StateBlocked, true},
		"a running job of triage only": {jobs.KindTriage, jobs.StateRunning, false},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.issue(42, "af:fix")
			id := e.Jobs.add(tc.kind, 42, tc.state, "")
			if tc.owed {
				e.record(Record{Issue: 42, Label: "af:fix", Owed: true, Seen: SeenEnded})
			}

			e.cycle()

			if !slices.Equal(e.removals(), []string{"remove(42,af:fix)"}) || len(e.records()) != len(e.Jobs.creates) || e.Jobs.count("Answer") != 0 {
				t.Errorf("removals %v, records %+v: want the label removed once, no owed record and no answer", e.removals(), e.records())
			}
			infos := e.Log.Find("INFO", "absorbed")
			if tc.kind == jobs.KindTriage {
				if len(infos) != 0 || len(e.Jobs.creates) != 1 {
					t.Errorf("absorbed %v and %d jobs made, want the fix job made beside the triage", infos, len(e.Jobs.creates))
				}
			} else if len(infos) != 1 || infos[0]["job"] != id || infos[0]["issue"] != float64(42) || infos[0]["kind"] != "fix" || len(e.Jobs.creates) != 0 {
				t.Errorf("absorbed %v and %d jobs made, want the label absorbed by %s and no job", infos, len(e.Jobs.creates), id)
			}
		})
	}
}

func TestOwedRemoval_IsWrittenOnceAndTakenAgainWithoutAJob(t *testing.T) {
	e := newEnv(t)
	e.issue(42, "af:fix")
	id := e.Jobs.add(jobs.KindFix, 42, jobs.StateRunning, "")
	e.Hub.Fail(removalPath(42, "af:fix"), 503)
	e.cycle()
	e.cycle()
	if recs := e.records(); len(recs) != 1 || !recs[0].Owed || recs[0].JobID != "" || !slices.Contains(e.labels(42), "af:fix") {
		t.Fatalf("records %+v and labels %v, want one owed removal with no job, and the label left", recs, e.labels(42))
	}
	// The open job ends; hub does not find the label, which confirms every owed removal of it.
	e.Hub.Clear()
	e.Hub.Fail(removalPath(42, "af:fix"), 404)
	e.Jobs.set(id, jobs.StateDone, &jobs.Standing{})
	e.record(Record{Issue: 42, Label: "af:fix", JobID: id, Owed: true})
	e.cycle()
	if len(e.removals()) != 3 || len(e.records()) != 0 || len(e.Jobs.creates) != 0 || len(e.Log.Find("ERROR", "")) != 0 {
		t.Errorf("removals %v, records %+v, logs %s: want one more removal, both records confirmed and dropped, and no job", e.removals(), e.records(), e.Log)
	}
}

func TestRemovalThatFails_StaysOwedAndTheCycleGoesOnOnlyForAnIssueOfItsOwn(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		level  string
		goesOn bool
	}{
		"409": {409, `{"error":{"code":409,"message":"the label is locked by hub","error_type":"forge_conflict"}}`, "ERROR", true},
		"502 of a forge that refuses hub's credential": {502,
			`{"error":{"code":502,"message":"the forge refused the token of hub","error_type":"forge_unauthenticated"}}`, "ERROR", false},
		"503": {503, "", "WARN", false},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.issue(42, "af:fix")
			e.issue(43, "af:fix")
			e.Hub.Inject("DELETE", removalPath(42, "af:fix"), fakehub.Fault{Status: tc.status, Body: tc.body, Once: true})

			e.cycle()

			if failures := e.Log.Find(tc.level, "remove the label af:fix from the issue 42"); len(failures) != 1 {
				t.Errorf("logs %s, want one %s about the removal", e.Log, tc.level)
			}
			if recs := e.records(); len(recs) == 0 || recs[0].Issue != 42 || !recs[0].Owed || !slices.Contains(e.labels(42), "af:fix") {
				t.Errorf("records %+v and labels %v, want the job of 42 made and its removal owed", recs, e.labels(42))
			}
			if went := len(e.Jobs.creates) == 2 && len(e.labels(43)) == 0; went != tc.goesOn {
				t.Errorf("issue 43 was taken: %v, want %v", went, tc.goesOn)
			}
			e.cycle()
			if len(e.labels(42)) != 0 || len(e.Jobs.creates) != 2 {
				t.Errorf("the next cycle left labels %v on 42 and made %d jobs in all, want the label removed and 43's job", e.labels(42), len(e.Jobs.creates))
			}
		})
	}
}

func TestRecordThatCannotBeWritten_LeavesTheRemovalToGoAhead(t *testing.T) {
	e := newEnv(t)
	e.issue(42, "af:fix")
	e.failRecordWrites("INSERT", "UPDATE", "DELETE")
	e.cycle()
	if len(e.Log.Find("WARN", "")) != 1 || !slices.Equal(e.removals(), []string{"remove(42,af:fix)"}) || len(e.Jobs.creates) != 1 {
		t.Errorf("logs %s, removals %v: want one WARN about the record, the job and the removal", e.Log, e.removals())
	}
}

func TestHold_LeavesTheLabelOnTheIssue(t *testing.T) {
	e := newEnv(t)
	e.issue(42, "af:fix")
	id := e.Jobs.add(jobs.KindFix, 42, jobs.StateCancelled, "af:fix")
	e.Jobs.set(id, jobs.StateCancelled, &jobs.Standing{CancelledInRunner: true, RunnerInProgress: true})
	e.record(Record{Issue: 42, Label: "af:fix", JobID: id, Seen: SeenHolding})
	for range 3 {
		e.cycle()
		e.Clock.Advance(time.Hour)
	}
	if len(e.removals()) != 0 || len(e.Jobs.creates) != 0 || e.Jobs.count("LatestJob") != 0 || !slices.Contains(e.labels(42), "af:fix") {
		t.Errorf("removals %v, %d jobs made, labels %v: want the label left alone", e.removals(), len(e.Jobs.creates), e.labels(42))
	}
}

// Another trigger may take the label between the list and the rules: its record, newer than the listing, leaves the
// label to the next cycle, whatever the job of the issue.
func TestNewerRecord_LeavesTheLabelToTheNextCycle(t *testing.T) {
	for _, state := range []jobs.State{"", jobs.StateBlocked, jobs.StateFailed} {
		t.Run(string(state)+" job", func(t *testing.T) {
			e := newEnv(t)
			e.issue(42, "af:fix")
			if state != "" {
				e.waiting(42, state)
			}
			e.comment(42, "alice", "here is more")
			e.onList("af:fix", func() { e.record(Record{Issue: 42, Label: "af:fix", Seen: SeenEnded}) })
			comments := len(e.comments(42))

			e.cycle()

			if len(e.Jobs.creates)+e.Jobs.count("Answer")+e.Jobs.count("Retry")+len(e.removals()) != 0 || len(e.comments(42)) != comments {
				t.Errorf("hub was asked %v and the jobs layer %v: want nothing done", e.routes(), e.Jobs.calls)
			}
			e.cycle()
			if len(e.labels(42)) != 0 {
				t.Errorf("the next cycle left the label: %v", e.labels(42))
			}
		})
	}
}

func TestCompleteListing_SettlesTheOwedRemovalsOfAnIssueWithoutTheLabel(t *testing.T) {
	e := newEnv(t)
	e.issue(42) // a person took the label off
	e.record(Record{Issue: 42, Label: "af:fix", JobID: e.Jobs.add(jobs.KindFix, 42, jobs.StateDone, "af:fix"), Owed: true})
	e.record(Record{Issue: 42, Label: "af:fix", Owed: true, Seen: SeenEnded})
	e.record(Record{Issue: 42, Label: "af:triage", Owed: true, Seen: SeenEnded})
	e.cycle()
	if len(e.records()) != 0 || len(e.removals()) != 0 {
		t.Errorf("records %+v and removals %v, want every owed removal settled and dropped with no removal", e.records(), e.removals())
	}
	e.Hub.AddLabel(slug, 42, "af:fix") // the latest job is done: a new request
	if e.cycle(); len(e.Jobs.creates) != 1 {
		t.Errorf("%d jobs made after the label was added again, want 1", len(e.Jobs.creates))
	}
}

func TestCompleteListing_ReleasesAHoldTheReleaseDelayAfterItsClockStarted(t *testing.T) {
	e := newEnv(t)
	e.issue(42)
	id := e.Jobs.add(jobs.KindFix, 42, jobs.StateCancelled, "af:fix")
	e.Jobs.set(id, jobs.StateCancelled, &jobs.Standing{CancelledInRunner: true})
	e.record(Record{Issue: 42, Label: "af:fix", JobID: id, Seen: SeenHolding, ReleaseClock: e.Clock.Now()})
	e.Clock.Advance(4 * time.Minute)
	e.cycle()
	if recs := e.records(); len(recs) != 1 || recs[0].Seen != SeenHolding {
		t.Fatalf("a listing 4 minutes after the clock left %+v, want the hold", recs)
	}
	e.Clock.Advance(time.Minute)
	e.cycle()
	if recs := e.records(); len(recs) != 0 {
		t.Fatalf("a listing 5 minutes after the clock left %+v, want the hold released and dropped", recs)
	}
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.cycle()
	if len(e.Jobs.creates) != 1 {
		t.Errorf("%d jobs made after the label was added again, want 1", len(e.Jobs.creates))
	}
}

// A page of 100 issues may have been cut by hub, so it proves nothing of the issues it leaves out. The issues here are
// pull requests, which make no job.
func TestIncompleteListing_ReleasesNoHoldAndSettlesNoOwedRemoval(t *testing.T) {
	e := newEnv(t)
	for n := 1000; n < 1100; n++ {
		e.Hub.AddIssue(slug, fakehub.Issue{Number: n, Labels: []string{"af:fix"}, IsPR: true})
	}
	e.issue(42)
	e.record(Record{Issue: 42, Label: "af:fix", JobID: "job_gone", Seen: SeenHolding, ReleaseClock: at(-60)})
	e.record(Record{Issue: 43, Label: "af:fix", Owed: true, Seen: SeenEnded})
	e.cycle()
	if recs := e.records(); len(recs) != 2 || recs[0].Seen != SeenHolding || !recs[1].Owed {
		t.Errorf("records %+v, want the hold and the owed removal kept", recs)
	}
}

func TestCompleteListing_SettlesNothingForARecordNewerThanIt(t *testing.T) {
	e := newEnv(t)
	old := e.Clock.Now().Add(-time.Hour)
	e.record(Record{Issue: 53, Label: "af:fix", Owed: true, Seen: SeenEnded, CreatedAt: old})
	e.record(Record{Issue: 54, Label: "af:fix", JobID: "job_old", Seen: SeenHolding, ReleaseClock: old, CreatedAt: old})
	e.onList("af:fix", func() {
		e.record(Record{Issue: 51, Label: "af:fix", Owed: true, Seen: SeenEnded})
		e.record(Record{Issue: 52, Label: "af:fix", JobID: "job_new", Seen: SeenHolding, ReleaseClock: old})
	})
	e.cycle()
	var left []int
	for _, r := range e.records() {
		left = append(left, r.Issue)
	}
	if !slices.Equal(left, []int{51, 52}) {
		t.Errorf("the records left are of %v, want the newer two, the older ones settled and released", left)
	}
}

func TestJobsOrRecordsThatCannotBeRead_LeaveTheLabelAndEndTheCycle(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(e *env)
		level string
		want  string
	}{
		"the records of the label": {func(e *env) {
			e.onList("af:fix", func() { testutil.Exec(e.t, e.DB, "DROP TABLE af_poll_requests") })
		}, "ERROR", "read the records of af:fix on the issue 42"},
		"how the job of a record stands": {func(e *env) {
			e.record(Record{Issue: 42, Label: "af:fix", JobID: e.Jobs.add(jobs.KindFix, 42, jobs.StateCancelled, "af:fix")})
			e.Jobs.fail("Standing", errors.New("the database is locked"))
		}, "ERROR", "read how the job of af:fix on the issue 42 stands"},
		"the open job":   {func(e *env) { e.Jobs.fail("OpenJob", errors.New("locked")) }, "WARN", "look up the open job"},
		"the latest job": {func(e *env) { e.Jobs.fail("LatestJob", errors.New("locked")) }, "WARN", "look up the latest job"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.issue(42, "af:fix")
			e.issue(43, "af:fix")
			tc.setup(e)
			e.cycle()
			if len(e.Log.Find(tc.level, tc.want)) != 1 || len(e.Jobs.creates) != 0 || len(e.removals()) != 0 {
				t.Errorf("logs %s, %d jobs, removals %v: want one %s that says %q, and nothing done", e.Log, len(e.Jobs.creates), e.removals(), tc.level, tc.want)
			}
		})
	}
}
