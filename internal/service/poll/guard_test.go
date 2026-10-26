package poll

import (
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

// outcome makes a fix job for issue 42 in the state, puts the label on it, runs the cycle that answers or retries it, and
// has the new attempt end in the same state at once. It returns the job's id and how many times the poll acted on it.
func outcome(e *env, state jobs.State) (id string, acted func() int) {
	id = e.waiting(42, state)
	e.comment(42, "alice", "an answer")
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.cycle()
	acted = func() int { return len(e.Jobs.answers) + len(e.Jobs.retries) }
	if acted() != 1 || len(e.labels(42)) != 0 {
		e.t.Fatalf("%d actions and labels %v after the first cycle, want one action and the label consumed", acted(), e.labels(42))
	}
	e.Jobs.set(id, state, &jobs.Standing{}) // the new attempt ends as the first did
	return id, acted
}

// A listing asked for after the outcome that still carries the label, because the forge's index lags its removal, does not
// answer or retry the job a second time, and posts no reply.
func TestForgeOutcome_ALaggingListingAskedForAfterItDoesNotRepeatIt(t *testing.T) {
	for _, state := range []jobs.State{jobs.StateBlocked, jobs.StateFailed} {
		e := newEnv(t)
		_, acted := outcome(e, state)
		comments := len(e.comments(42))

		e.Hub.AddLabel(slug, 42, "af:fix") // the index still shows the label
		e.cycle()
		e.cycle()

		if acted() != 1 || len(e.comments(42)) != comments || len(e.Jobs.creates) != 0 {
			t.Errorf("%s: %d actions, %d comments (was %d) and %d jobs made after a lagging listing, want the one action and nothing more",
				state, acted(), len(e.comments(42)), comments, len(e.Jobs.creates))
		}
	}
}

// A complete listing that lacks the label proves the removal, and a label a person puts on the job after that is answered
// or retried as before.
func TestForgeOutcome_ACompleteListingWithoutTheLabelReleasesTheGuard(t *testing.T) {
	for _, state := range []jobs.State{jobs.StateBlocked, jobs.StateFailed} {
		e := newEnv(t)
		_, acted := outcome(e, state)

		e.cycle() // the label is gone from the listing
		e.comment(42, "bob", "another answer")
		e.Hub.AddLabel(slug, 42, "af:fix")
		e.cycle()

		if acted() != 2 {
			t.Errorf("%s: %d actions after a fresh label, want 2", state, acted())
		}
	}
}

// The guard also ends with the release delay, so a label that stays on the issue is not ignored for good.
func TestForgeOutcome_TheGuardEndsWithTheReleaseDelay(t *testing.T) {
	for _, state := range []jobs.State{jobs.StateBlocked, jobs.StateFailed} {
		e := newEnv(t)
		_, acted := outcome(e, state)
		e.Hub.AddLabel(slug, 42, "af:fix")

		e.Clock.Advance(4 * time.Minute)
		e.cycle()
		if acted() != 1 {
			t.Fatalf("%s: %d actions inside the release delay, want 1", state, acted())
		}
		e.Clock.Advance(time.Minute)
		e.cycle()
		if acted() != 2 {
			t.Errorf("%s: %d actions once the release delay passed, want 2", state, acted())
		}
	}
}

// A record kept as a guard is dropped once it guards nothing and its job ended: a complete listing without the label
// releases it, and so does the release delay.
func TestForgeOutcome_AGuardIsDroppedOnceReleasedAndItsJobEnded(t *testing.T) {
	for _, release := range []string{"listing", "delay"} {
		e := newEnv(t)
		id, _ := outcome(e, jobs.StateBlocked)
		e.Jobs.set(id, jobs.StateDone, &jobs.Standing{})
		r := NewRecords(e.DB)
		if _, err := r.Drop(ctx, slug); err != nil || len(e.records()) != 1 {
			t.Fatalf("%s: Drop = %v with %d records left, want the guard kept", release, err, len(e.records()))
		}
		if release == "listing" {
			e.cycle() // the listing lacks the label
		} else {
			e.Clock.Advance(5 * time.Minute)
			recordReconciler{e.Poll}.reconcile(ctx, slug)
		}
		if n := len(e.records()); n != 0 {
			t.Errorf("%s: %d records after the guard was released and the job ended, want none", release, n)
		}
	}
}
