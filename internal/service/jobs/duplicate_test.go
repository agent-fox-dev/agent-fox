package jobs_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

// TestACreatorThatLosesToAJobThatEndsBeforeItLooksItUpIsNotToldThatTheJobIsMissing is the gap in the atomic check of
// 15-REQ-9.1. The partial unique index keeps a second open job of a kind out of an issue, and the creator that loses
// asks for the job that won, to name it in the refusal. If that job has ended in between, there is none to name, and
// the creator must not be answered "not found": it tries to make its job again, and the issue has exactly one open
// job, its own.
// Verifies: 15-REQ-9.1
func TestACreatorThatLosesToAJobThatEndsBeforeItLooksItUpIsNotToldThatTheJobIsMissing(t *testing.T) {
	var e *env
	var once sync.Once
	var winner jobs.Doc
	e = newEnv(t, func(c *jobs.Config) {
		c.StoreFault = func(op string) error {
			if op == jobs.OpDuplicate {
				// The job that won the issue ends now: after the insert was refused, before it is looked up.
				once.Do(func() {
					if _, err := e.Svc.Cancel(ctx, winner.ID); err != nil {
						t.Errorf("cancel the winner: %v", err)
					}
				})
			}
			return nil
		}
	})
	winner = e.createFromLabel("widgets", 42, "af:fix")

	loser := e.createFromLabel("widgets", 42, "af:fix")

	if loser.ID == winner.ID || loser.State != jobs.StateQueued {
		t.Errorf("the second creator got %+v, want a job of its own", loser)
	}
	if n := e.count("SELECT COUNT(*) FROM af_jobs WHERE workspace = 'widgets' AND kind = 'fix' AND issue = 42 AND state IN ('queued', 'running', 'blocked')"); n != 1 {
		t.Errorf("%d open jobs for the issue, want exactly one", n)
	}

	// While the job that won is open, the loser is told which job it is.
	_, err := e.Svc.Create(ctx, jobs.CreateRequest{
		Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: []byte("42")}, Source: jobs.Source{Kind: "label", By: "label af:fix"}, RequestLabel: "af:fix",
	})
	var dup *jobs.DuplicateError
	if !errors.Is(err, jobs.ErrDuplicate) || !errors.As(err, &dup) || dup.JobID != loser.ID {
		t.Errorf("a creator that loses to an open job got %v, want the duplicate refusal naming %s", err, loser.ID)
	}
}
