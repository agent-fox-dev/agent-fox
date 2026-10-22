package jobs

import (
	"context"
	"errors"
	"fmt"
)

// Unsubscribe is what the removal of a subscription does about the jobs of the workspace.
// It marks the workspace as being unsubscribed, so that no job is made for it and an
// answer or a retry is refused as not subscribed; cancels every queued, running and
// blocked job of the workspace with the unsubscribe cause, which a runner reads as a
// cancel; waits for the running attempts to return, up to the grace of a cancel, and
// records one that has not as a cancel whose grace ran out; and then calls remove, the
// removal of the subscription and its token. The mark is cleared when remove has
// returned, whether it failed or not: the workspace is no longer subscribed, or it still
// is. The jobs and their history stay until retention removes them.
func (s *Service) Unsubscribe(ctx context.Context, slug string, remove func() error) error {
	clear := s.MarkUnsubscribing(slug)
	defer clear()

	ids, err := s.store.OpenJobIDs(ctx, slug)
	if err != nil {
		return err
	}
	var runs []*attemptRun
	for _, id := range ids {
		run, err := s.cancelWhatIsOpen(ctx, id)
		if err != nil {
			return err
		}
		if run != nil {
			runs = append(runs, run)
		}
	}
	for _, run := range runs {
		select {
		case <-run.settled:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return remove()
}

// WorkspaceOf is the workspace of a job, or ErrNotFound.
func (s *Service) WorkspaceOf(ctx context.Context, id string) (string, error) {
	return s.store.WorkspaceOf(ctx, id)
}

// cancelWhatIsOpen cancels a job with the unsubscribe cause whatever open state it is in:
// a job that changes state between the look and the cancel is looked at again, and one that
// has ended or is gone in the meantime is left. It returns the attempt of the job that is in
// a runner, if there is one.
func (s *Service) cancelWhatIsOpen(ctx context.Context, id string) (*attemptRun, error) {
	for range 8 {
		state, err := s.store.JobState(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if !state.Open() {
			return nil, nil
		}
		run, err := s.cancel(ctx, id, state, ErrUnsubscribed)
		var changed *StateError
		if errors.As(err, &changed) {
			continue // another action got there first: look again
		}
		return run, err
	}
	return nil, fmt.Errorf("job %s kept changing state while it was cancelled", id)
}
