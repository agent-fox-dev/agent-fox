package poll

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

func TestAnswer_TheCommentsSinceTheQuestionAnswerTheBlockedJob(t *testing.T) {
	e := newEnv(t)
	id := e.waiting(42, jobs.StateBlocked)
	e.comment(42, "alice", "use the greeting from the README")
	e.Hub.AddLabel(slug, 42, "af:fix")

	e.cycle()

	if want := "--- comment by alice ---\nuse the greeting from the README"; !slices.Equal(e.Jobs.answers, []string{want}) {
		t.Errorf("the answers are %q, want %q", e.Jobs.answers, want)
	}
	if got, want := e.routes()[3:], []string{"issues(af:fix)", "read(42)", "remove(42,af:fix)"}; !slices.Equal(got, want) {
		t.Errorf("hub was asked %v, want the issue read once, never its comment list, and the label removed", got)
	}
	if infos := e.Log.Find("INFO", "answered a blocked job"); len(infos) != 1 || infos[0]["job"] != id || infos[0]["issue"] != float64(42) {
		t.Errorf("the INFO records are %v", infos)
	}
	e.Jobs.set(id, jobs.StateBlocked, &jobs.Standing{}) // the new attempt asks again at once
	e.cycle()
	e.cycle()
	if len(e.Jobs.answers) != 1 || len(e.comments(42)) != 2 {
		t.Errorf("%d answers and %d comments after two more cycles, want no more: the label was consumed", len(e.Jobs.answers), len(e.comments(42)))
	}
}

func TestRetry_TheCommentsSinceTheFailureAreTheContextOfTheNextAttempt(t *testing.T) {
	for _, comment := range []string{"the log is in CI run 812", ""} {
		e := newEnv(t)
		id := e.waiting(42, jobs.StateFailed)
		if comment != "" {
			e.comment(42, "bob", comment)
		}
		e.Hub.AddLabel(slug, 42, "af:fix")
		comments := len(e.comments(42))

		e.cycle()

		want := [][]string{nil}
		if comment != "" {
			want = [][]string{{"--- comment by bob ---\n" + comment}}
		}
		if !slices.EqualFunc(e.Jobs.retries, want, slices.Equal) || len(e.Jobs.creates) != 0 || len(e.labels(42)) != 0 {
			t.Errorf("the retries are %q with %d jobs made and labels %v, want %q and the label consumed", e.Jobs.retries, len(e.Jobs.creates), e.labels(42), want)
		}
		if len(e.comments(42)) != comments || len(e.Log.Find("INFO", "retried a failed job")) != 1 || e.Log.Find("INFO", "retried")[0]["job"] != id {
			t.Errorf("comments %q and logs %s, want no reply and the retry logged", e.comments(42), e.Log)
		}
	}
}

// The record of every outcome is written before the removal and names the job only when the label is the job's own
// request label, which its cancel puts back.
func TestForgeOutcome_WritesItsRecordBeforeTheRemovalAndNamesOnlyAJobOfItsOwnLabel(t *testing.T) {
	for _, outcome := range []string{"answer", "retry", "no answer", "thread cut"} {
		for _, own := range []bool{true, false} {
			e := newEnv(t)
			state := jobs.StateBlocked
			if outcome == "retry" {
				state = jobs.StateFailed
			}
			id := e.waiting(42, state)
			if !own {
				e.Jobs.docs[0].RequestLabel = nil // a job of the API
			}
			if outcome != "no answer" {
				e.comment(42, "alice", "an answer")
			}
			if outcome == "thread cut" {
				e.Hub.Inject("GET", issuePath(42), fakehub.Fault{Status: 200, Body: e.threadBody(42, true, false), Once: true})
			}
			e.Hub.AddLabel(slug, 42, "af:fix")
			e.Hub.FailOnce(removalPath(42, "af:fix"), 503)

			e.cycle()

			want := map[bool]string{true: id, false: ""}[own]
			if recs := e.records(); len(recs) != 1 || !recs[0].Owed || recs[0].JobID != want {
				t.Errorf("%s, own label %v: records %+v, want one owed that names the job %q", outcome, own, recs, want)
			}
			e.cycle()
			if len(e.labels(42)) != 0 {
				t.Errorf("%s, own label %v: the next cycle left %v", outcome, own, e.labels(42))
			}
		}
	}
}

func TestAnswerOrRetry_ThatTheJobsLayerRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		state    jobs.State
		method   string // the method that fails, the answer or the retry when empty
		err      error
		absorbed string // the job named as absorbing the label, "-" when the label stays
		warn     string
	}{
		"an answer to a job that moved":   {jobs.StateBlocked, "", &jobs.StateError{State: jobs.StateQueued}, "job_1", ""},
		"a retry of a job that moved":     {jobs.StateFailed, "", &jobs.StateError{State: jobs.StateQueued}, "job_1", ""},
		"a retry beaten by another job":   {jobs.StateFailed, "", &jobs.DuplicateError{JobID: "job_api"}, "job_api", ""},
		"a retry refused as a duplicate":  {jobs.StateFailed, "", jobs.ErrDuplicate, "job_1", ""},
		"an answer, not subscribed":       {jobs.StateBlocked, "", jobs.ErrNotSubscribed, "-", ""},
		"a retry, not subscribed":         {jobs.StateFailed, "", jobs.ErrNotSubscribed, "-", ""},
		"an answer, the token unreadable": {jobs.StateBlocked, "", jobs.ErrTokenUnreadable, "-", "answer the blocked job"},
		"a retry, the disk full":          {jobs.StateFailed, "", errors.New("disk full"), "-", "retry the failed job"},
		"the job cannot be read":          {jobs.StateBlocked, "Get", errors.New("locked"), "-", "read the job job_1 of af:fix on the issue 42"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			for _, n := range []int{42, 43} {
				e.waiting(n, tc.state)
				e.comment(n, "bob", "try again")
				e.Hub.AddLabel(slug, n, "af:fix")
			}
			if tc.method == "" {
				tc.method = map[jobs.State]string{jobs.StateBlocked: "Answer", jobs.StateFailed: "Retry"}[tc.state]
			}
			e.Jobs.fail(tc.method, tc.err)
			comments := len(e.comments(42))

			e.cycle()
			e.cycle()

			if len(e.comments(42)) != comments {
				t.Errorf("comments %q, want none posted", e.comments(42))
			}
			if infos := e.Log.Find("INFO", "absorbed"); tc.absorbed != "-" && (len(infos) == 0 || infos[0]["job"] != tc.absorbed || len(e.labels(42)) != 0) {
				t.Errorf("absorbed %v and labels %v, want the label absorbed by %s", infos, e.labels(42), tc.absorbed)
			}
			if tc.absorbed == "-" && (!slices.Contains(e.labels(42), "af:fix") || slices.Contains(e.routes(), "read(43)") || len(e.removals()) != 0) {
				t.Errorf("labels %v and hub asked %v, want the label left and the cycle ended", e.labels(42), e.routes())
			}
			if warns := e.Log.Find("WARN", ""); (tc.warn == "") != (len(warns) == 0) || (tc.warn != "" && (len(warns) != 1 || !strings.Contains(warns[0]["msg"].(string), tc.warn))) {
				t.Errorf("the warnings are %v, want one only for a refusal that says %q", warns, tc.warn)
			}
		})
	}
}

func TestAnswer_AnIssueThatCannotBeReadLeavesTheLabelAndEndsTheCycleUnlessTheIssueIsAtFault(t *testing.T) {
	forge := `{"error":{"code":502,"message":"the forge refused the token of hub","error_type":"forge_unauthenticated"}}`
	for name, tc := range map[string]struct {
		fault  fakehub.Fault
		level  string
		goesOn bool
	}{
		"503":                         {fakehub.Fault{Status: 503}, "WARN", false},
		"401":                         {fakehub.Fault{Status: 401}, "ERROR", false},
		"403":                         {fakehub.Fault{Status: 403}, "ERROR", false},
		"502 of the forge":            {fakehub.Fault{Status: 502, Body: forge}, "ERROR", false},
		"404":                         {fakehub.Fault{Status: 404}, "ERROR", true},
		"422":                         {fakehub.Fault{Status: 422}, "ERROR", true},
		"comments hub could not read": {fakehub.Fault{Status: 200}, "ERROR", true},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			for _, n := range []int{42, 43} {
				e.waiting(n, jobs.StateBlocked)
				e.comment(n, "alice", "an answer")
				e.Hub.AddLabel(slug, n, "af:fix")
			}
			if tc.fault.Status == 200 {
				tc.fault.Body = e.threadBody(42, false, true)
			}
			e.Hub.Inject("GET", issuePath(42), tc.fault)

			e.cycle()

			if !slices.Contains(e.labels(42), "af:fix") || len(e.comments(42)) != 2 || len(e.Log.Find(tc.level, "read the comments of the issue 42")) != 1 {
				t.Errorf("labels %v, comments %q, logs %s: want the label left, no reply and one %s", e.labels(42), e.comments(42), e.Log, tc.level)
			}
			if answered := len(e.Jobs.answers) == 1; answered != tc.goesOn {
				t.Errorf("43 answered in the same cycle: %v, want %v", answered, tc.goesOn)
			}
			e.Hub.Clear()
			e.cycle()
			if len(e.Jobs.answers) != 2 {
				t.Errorf("%d answers once the issue reads again, want both", len(e.Jobs.answers))
			}
		})
	}
}
