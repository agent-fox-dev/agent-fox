package jobs_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
)

// TestTheJobsLayerAnswersWhatTheReconciliationPassAsksOfIt pins the four questions the reconciliation pass puts to the
// layer: whether a job is one of this database's, whether an attempt is in a runner of this process, which attempts
// have ended, and where the files of an attempt are.
// Verifies: 14-REQ-8.2, 14-REQ-8.3, 14-REQ-8.4
func TestTheJobsLayerAnswersWhatTheReconciliationPassAsksOfIt(t *testing.T) {
	r := fakerunner.New(fakerunner.Done(), fakerunner.Block(fakerunner.Done()))
	e := newEnv(t, withRunner(r), withWorkers(2))
	e.addWorkspace("gadgets")
	e.Start()
	ended := e.create("widgets", jobs.KindFix, 1)
	e.waitState(ended.ID, jobs.StateDone)
	running := e.create("gadgets", jobs.KindFix, 2)
	waitFor(t, "the second job to be in the runner", func() bool { return e.Svc.Running(running.ID, 1) })

	if known, err := e.Svc.Known(ctx, ended.ID); err != nil || !known {
		t.Errorf("Known(%s) = %v, %v, want true", ended.ID, known, err)
	}
	if known, err := e.Svc.Known(ctx, "job_00000000000000000000"); err != nil || known {
		t.Errorf("Known of a job the database does not have = %v, %v, want false and no error", known, err)
	}
	if !e.Svc.Running(running.ID, 1) || e.Svc.Running(running.ID, 2) || e.Svc.Running(ended.ID, 1) {
		t.Errorf("Running: the attempt in the runner %v, a later attempt of it %v, the attempt that ended %v, want true, false, false",
			e.Svc.Running(running.ID, 1), e.Svc.Running(running.ID, 2), e.Svc.Running(ended.ID, 1))
	}
	got, err := e.Svc.EndedAttempts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].JobID != ended.ID || got[0].Attempt != 1 || got[0].Workspace != "widgets" || got[0].State != jobs.AttemptDone {
		t.Errorf("EndedAttempts = %+v, want the one attempt that is done, on widgets", got)
	}
	if dir := e.Svc.AttemptDir(ended.ID, 1); dir != filepath.Join(e.Dir, "attempts", ended.ID, "1") {
		t.Errorf("AttemptDir = %s, want attempts/<job>/1 beside the database", dir)
	}

	r.ReleaseAll()
	e.waitState(running.ID, jobs.StateDone)
	waitFor(t, "the runner to be left", func() bool { return !e.Svc.Running(running.ID, 1) })
	got, err = e.Svc.EndedAttempts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, a := range got {
		ids = append(ids, a.JobID)
	}
	slices.Sort(ids)
	if want := []string{ended.ID, running.ID}; !slices.Equal(ids, slices.Sorted(slices.Values(want))) {
		t.Errorf("EndedAttempts after the release = %v, want both jobs", ids)
	}
}
