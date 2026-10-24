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
//
// Before remove, it waits for the first try of each cancel cleanup its cancels marked, so that
// the issues are put back while the token is still there; it stops waiting the cancel grace
// after the cancels and the wait for the running attempts have ended. A cleanup still pending
// then ends at its next try, which finds the workspace no longer subscribed.
func (s *Service) Unsubscribe(ctx context.Context, slug string, remove func() error) error {
	clear := s.MarkUnsubscribing(slug)
	defer clear()

	ids, err := s.store.OpenJobIDs(ctx, slug)
	if err != nil {
		return err
	}
	var runs []*attemptRun
	var tries []<-chan struct{}
	for _, id := range ids {
		run, _, err := s.cancelWhatIsOpen(ctx, id, ErrUnsubscribed)
		if err != nil {
			return err
		}
		if run != nil {
			runs = append(runs, run)
		} else if try := s.tryOf(id); try != nil {
			tries = append(tries, try)
		}
	}
	for _, run := range runs {
		select {
		case <-run.settled:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := s.awaitTries(ctx, runs, tries); err != nil {
		return err
	}
	return remove()
}

// awaitTries waits for the first try of each cleanup the unsubscribe marked, while the token is still there: the tries
// of the jobs whose cancel reached no runner, and those of the jobs whose runner returned without a hub session, which
// start once the handler is through with what the runner returned. It stops waiting the cancel grace after it began,
// which is once the cancels and the wait for the running attempts have both ended.
func (s *Service) awaitTries(ctx context.Context, runs []*attemptRun, tries []<-chan struct{}) error {
	if s.cleaner == nil {
		return nil
	}
	over := s.clock.After(cancelGrace)
	wait := func(ch <-chan struct{}) (bool, error) {
		select {
		case <-ch:
			return true, nil
		case <-over:
			return false, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	for _, run := range runs {
		if ok, err := wait(run.done); !ok {
			return err
		}
		if try := s.tryOf(run.key.job); try != nil {
			tries = append(tries, try)
		}
	}
	for _, try := range tries {
		if ok, err := wait(try); !ok {
			return err
		}
	}
	return nil
}

// WorkspaceOf is the workspace of a job, or ErrNotFound.
func (s *Service) WorkspaceOf(ctx context.Context, id string) (string, error) {
	return s.store.WorkspaceOf(ctx, id)
}

// cancelWhatIsOpen cancels a job with the cause whatever open state it is in: a job that changes state between the look
// and the cancel is looked at again, at most 8 times, and one that has ended or is gone in the meantime is left. It
// returns the attempt of the job that is in a runner, if there is one, and whether it cancelled the job.
func (s *Service) cancelWhatIsOpen(ctx context.Context, id string, cause error) (*attemptRun, bool, error) {
	for range 8 {
		state, err := s.store.JobState(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		if !state.Open() {
			return nil, false, nil
		}
		if s.afterLook != nil {
			s.afterLook(id, state)
		}
		run, err := s.cancel(ctx, id, state, cause)
		var changed *StateError
		if errors.As(err, &changed) {
			continue // another action got there first: look again
		}
		return run, err == nil, err
	}
	return nil, false, fmt.Errorf("job %s kept changing state while it was cancelled", id)
}
