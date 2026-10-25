package poll

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The tests in this file run the poll against the real jobs layer over the state file, with the fake runner, for what
// only the two together show.

// withJobsLayer gives the poll the jobs layer, started with the runner.
func withJobsLayer(r *fakerunner.Runner, svc **jobs.Service) option {
	return func(e *env, c *Config) {
		s, err := jobs.New(jobs.Config{
			DB: e.DB, Subscriptions: e.Store, Logger: c.Logger, Clock: e.Clock, Workers: 2, Runner: r,
			AttemptsDir: filepath.Join(e.t.TempDir(), "attempts"), PollInterval: 5 * time.Millisecond, RetryDelay: time.Millisecond,
		})
		if err != nil {
			e.t.Fatal(err)
		}
		if err := s.Start(); err != nil {
			e.t.Fatal(err)
		}
		e.t.Cleanup(func() { r.ReleaseAll(); s.Close() })
		*svc, c.Jobs = s, s
	}
}

func waitState(t *testing.T, svc *jobs.Service, id string, state jobs.State) {
	t.Helper()
	testutil.Eventually(t, id+" to be "+string(state), func() bool {
		s, err := svc.Standing(ctx, id)
		return err == nil && s.State == state && !s.RunnerInProgress
	})
}

func jobsOf(t *testing.T, e *env) []string {
	return testutil.Column(t, e.DB, "SELECT kind || ' ' || issue || ' ' || source_kind || '/' || source_by || ' ' || COALESCE(request_label, '') FROM af_jobs ORDER BY created_at, rowid")
}

func TestWithTheJobsLayer_TheRunnerOfTheNextAttemptGetsTheCommentsOfTheIssue(t *testing.T) {
	for name, first := range map[string]fakerunner.Step{
		"an answer": fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which?", "a"))),
		"a retry":   fakerunner.Failed("sandbox", false),
	} {
		t.Run(name, func(t *testing.T) {
			var svc *jobs.Service
			r := fakerunner.New(first).Then(fakerunner.Block(fakerunner.Step{}))
			e := newEnv(t, withJobsLayer(r, &svc))
			e.issue(42, "af:fix")
			e.cycle()
			id := testutil.Column(t, e.DB, "SELECT id FROM af_jobs")[0]
			state, kind := jobs.StateBlocked, "question"
			if name == "a retry" {
				state, kind = jobs.StateFailed, "failure"
			}
			waitState(t, svc, id, state)
			e.comment(42, "agent-fox", marker(id, kind))
			e.comment(42, "alice", "use the greeting from the README")
			e.Hub.AddLabel(slug, 42, "af:fix")

			e.cycle()

			testutil.Eventually(t, "the runner of attempt 2", func() bool { return len(r.Calls()) == 2 })
			call := r.Calls()[1]
			got := call.Context
			if name == "an answer" && len(call.Answers) == 1 {
				got = []string{call.Answers[0].Question + " " + call.Answers[0].Text}
			}
			if len(got) == 0 || !strings.Contains(got[len(got)-1], "--- comment by alice ---\nuse the greeting from the README") {
				t.Errorf("the runner of attempt 2 got %q, want the comment", got)
			}
			if len(jobsOf(t, e)) != 1 || len(e.labels(42)) != 0 {
				t.Errorf("jobs %q and labels %v, want no new job and the label consumed", jobsOf(t, e), e.labels(42))
			}
		})
	}
}

// The jobs are made from the labels, and an open one absorbs its label. The worker's cancel cleanup puts the request label back on the issue of a job cancelled in its runner: the poll holds
// it until a person takes it off, and a complete listing the release delay after the runner returned releases it.
func TestWithTheJobsLayer_ALabelTheCancelPutBackIsHeldThenReleased(t *testing.T) {
	var svc *jobs.Service
	cancelled := fakerunner.Return(jobs.Result{
		Envelope:  fakerunner.Env(toolio.StatusFailed, fakerunner.WithError(toolio.CategoryCancelled, "the job was cancelled", false)),
		LabelsSet: []string{"af:fix"},
	})
	r := fakerunner.New(fakerunner.Block(cancelled)).Then(fakerunner.Block(fakerunner.Step{}))
	e := newEnv(t, withJobsLayer(r, &svc))
	e.issue(42, "af:fix")
	e.cycle()
	if !r.Wait(1, testutil.WaitTimeout) { // the cancel is to reach a runner
		t.Fatal("the runner was not called")
	}
	e.Hub.AddLabel(slug, 42, "af:fix") // absorbed by the open job
	e.cycle()
	if got, want := jobsOf(t, e), []string{"fix 42 label/label af:fix af:fix"}; !slices.Equal(got, want) {
		t.Fatalf("the jobs are %q, want %q", got, want)
	}
	id := testutil.Column(t, e.DB, "SELECT id FROM af_jobs")[0]
	if _, err := svc.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	e.Hub.AddLabel(slug, 42, "af:fix")
	waitState(t, svc, id, jobs.StateCancelled)

	for range 3 {
		e.cycle()
		e.Clock.Advance(time.Minute)
	}
	if len(jobsOf(t, e)) != 1 || len(e.removals()) != 2 || !slices.Contains(e.labels(42), "af:fix") {
		t.Fatalf("during the hold: jobs %q, removals %v, labels %v; want the label left alone", jobsOf(t, e), e.removals(), e.labels(42))
	}
	e.Hub.RemoveLabel(slug, 42, "af:fix") // a person takes it off
	e.Clock.Advance(5 * time.Minute)
	e.cycle()
	e.Hub.AddLabel(slug, 42, "af:fix") // and asks again
	e.cycle()
	e.cycle()
	if got := jobsOf(t, e); len(got) != 2 || len(e.labels(42)) != 0 {
		t.Errorf("jobs %q and labels %v, want exactly one new job once the hold was released", got, e.labels(42))
	}
}
