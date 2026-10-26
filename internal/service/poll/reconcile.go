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
		if _, err := r.s.note(ctx, rec); err != nil {
			r.s.report(ctx, workspace, r.s.stateOf(workspace), failure{row: rowStore, operation: "read how the job of a record stands", err: err})
		}
	}
	r.s.drop(ctx, workspace)
}

// drop releases the guards that are older than the release delay, and deletes the records of the workspace for which
// nothing is owed, nothing holds and nothing guards.
func (s *Supervisor) drop(ctx context.Context, workspace string) {
	if _, err := s.records.ExpireGuards(ctx, workspace, s.cfg.Clock.Now().Add(-s.cfg.ReleaseDelay)); err != nil {
		s.cfg.Logger.Warn("the guards of the records of the poll could not be released", "workspace", workspace, "error", err.Error())
	}
	if _, err := s.records.Drop(ctx, workspace); err != nil {
		s.cfg.Logger.Warn("the records of the poll that hold nothing could not be dropped", "workspace", workspace, "error", err.Error())
	}
}

// note brings the record up to date with its job, and returns it as it is now. If the job cannot be read the record is
// returned as it was, with the error: what has become of the job is not known, and a caller that is about to take a label
// for a new request must not go on as if it were.
//
//   - A job that is done has ended, and holds nothing.
//   - A cancelled job holds its request label when its cancel reached a runner, whatever the runner reports (the worker's
//     cancel cleanup writes the label back), and when its cancel cleanup is pending or has finished with the label on
//     the issue. The hold starts at the cancel. Its release clock starts the first time the poll sees that no runner of
//     the job is in progress and no cleanup of it is pending.
//   - A cancelled job whose cancel reached no runner holds nothing when it got no cleanup, or when its cleanup ended
//     with the label not back; a record that held while the cleanup was pending stops holding when the poll sees that.
//   - A job that retention purged before the poll saw it end may have been cancelled with its label put back, which the
//     poll cannot know, so it holds from the moment it was found gone.
//   - A job that is queued, running, blocked or failed has not ended: a failed job can be retried, and then cancelled.
func (s *Supervisor) note(ctx context.Context, rec Record) (Record, error) {
	if rec.JobID == "" || rec.Seen == SeenEnded {
		return rec, nil
	}
	now := s.cfg.Clock.Now()
	standing, err := s.cfg.Jobs.Standing(ctx, rec.JobID)
	switch {
	case errors.Is(err, jobs.ErrNotFound):
		rec = s.startClock(ctx, s.hold(ctx, rec), now)
	case err != nil:
		return rec, err
	case standing.State == jobs.StateDone, standing.State == jobs.StateCancelled && !holds(standing):
		rec = s.see(ctx, rec, SeenEnded)
	case standing.State == jobs.StateCancelled:
		if rec = s.hold(ctx, rec); rec.ReleaseClock.IsZero() && !standing.RunnerInProgress && !standing.CleanupPending {
			rec = s.startClock(ctx, rec, now)
		}
	}
	return rec, nil
}

// holds says that a cancelled job holds its request label: its cancel reached a runner, or its cleanup is pending or put
// the label back.
func holds(st jobs.Standing) bool {
	return st.CancelledInRunner || st.CleanupPending || st.CleanupPutBack
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
