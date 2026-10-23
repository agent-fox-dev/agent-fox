package poll

import (
	"context"
	"errors"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

// recordReconciler brings the records of the poll up to date with their jobs. It asks the jobs layer and the store, and
// never hub, so a cycle can do it before it makes a request, whether or not the poll turns out to be enabled.
type recordReconciler struct{ s *Supervisor }

// reconcile notes what has become of the job of each record of the workspace whose job the poll has not seen end, and
// drops the records for which nothing is owed and nothing holds.
func (r recordReconciler) reconcile(ctx context.Context, workspace string) {
	recs, err := r.s.records.ForWorkspace(ctx, workspace)
	if err != nil {
		r.s.cfg.Logger.Warn("the records of the poll could not be read, so they are not brought up to date with their jobs",
			"workspace", workspace, "error", err.Error())
		return
	}
	for _, rec := range recs {
		r.s.note(ctx, rec)
	}
	r.s.drop(ctx, workspace)
}

// drop deletes the records of the workspace for which nothing is owed and nothing holds.
func (s *Supervisor) drop(ctx context.Context, workspace string) {
	if _, err := s.records.Drop(ctx, workspace); err != nil {
		s.cfg.Logger.Warn("the records of the poll that hold nothing could not be dropped", "workspace", workspace, "error", err.Error())
	}
}

// note brings the record up to date with its job, and returns it as it is now.
//
//   - A job that is done, or cancelled without its cancel having reached a runner, has ended, and holds nothing.
//   - A job whose cancel reached a runner holds its request label, which the worker's cancel cleanup writes back to the
//     issue. The hold starts at the cancel, whatever the runner reports. Its release clock starts the first time the poll
//     sees that no runner of the job is in progress.
//   - A job that retention purged before the poll saw it end may have been cancelled with its label put back, which the
//     poll cannot know, so it holds from the moment it was found gone.
//   - A job that is queued, running, blocked or failed has not ended: a failed job can be retried, and then cancelled.
func (s *Supervisor) note(ctx context.Context, rec Record) Record {
	if rec.JobID == "" || rec.Seen == SeenEnded {
		return rec
	}
	now := s.cfg.Clock.Now()
	standing, err := s.cfg.Jobs.Standing(ctx, rec.JobID)
	switch {
	case errors.Is(err, jobs.ErrNotFound):
		rec = s.startClock(ctx, s.hold(ctx, rec), now)
	case err != nil:
		s.cfg.Logger.Warn("the job of a record of the poll could not be read, so the record is left as it is",
			"workspace", rec.Workspace, "issue", rec.Issue, "label", rec.Label, "job", rec.JobID, "error", err.Error())
	case rec.Seen == SeenHolding:
		if rec.ReleaseClock.IsZero() && !standing.RunnerInProgress {
			rec = s.startClock(ctx, rec, now)
		}
	case standing.State == jobs.StateDone, standing.State == jobs.StateCancelled && !standing.CancelledInRunner:
		rec = s.see(ctx, rec, SeenEnded)
	case standing.State == jobs.StateCancelled:
		if rec = s.hold(ctx, rec); !standing.RunnerInProgress {
			rec = s.startClock(ctx, rec, now)
		}
	}
	return rec
}

// see records what the poll has seen of the job of the record.
func (s *Supervisor) see(ctx context.Context, rec Record, seen Seen) Record {
	if err := s.records.SetSeen(ctx, rec.ID, seen); err != nil {
		s.cfg.Logger.Warn("what the poll saw of a job could not be recorded", "workspace", rec.Workspace, "issue", rec.Issue, "label", rec.Label, "error", err.Error())
		return rec
	}
	rec.Seen = seen
	return rec
}

// hold makes the record hold its label.
func (s *Supervisor) hold(ctx context.Context, rec Record) Record {
	if rec.Seen != SeenHolding {
		rec = s.see(ctx, rec, SeenHolding)
	}
	return rec
}

// startClock starts the release clock of the record at now, if it has not started.
func (s *Supervisor) startClock(ctx context.Context, rec Record, now time.Time) Record {
	if !rec.ReleaseClock.IsZero() {
		return rec
	}
	if err := s.records.StartClock(ctx, rec.ID, now); err != nil {
		s.cfg.Logger.Warn("the release clock of a hold could not be started", "workspace", rec.Workspace, "issue", rec.Issue, "label", rec.Label, "error", err.Error())
		return rec
	}
	rec.ReleaseClock = now
	return rec
}
