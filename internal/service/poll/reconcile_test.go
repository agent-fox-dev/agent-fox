package poll

import (
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

// The records are brought up to date with their jobs by every cycle, the poll off included.
func TestReconcile_NotesWhatBecameOfTheJobOfEachRecord(t *testing.T) {
	cancelled := jobs.StateCancelled
	for name, tc := range map[string]struct {
		state    jobs.State
		standing jobs.Standing
		purged   bool
		from     Record // what the record says before the cycle
		seen     Seen   // what it says after; "" when it was dropped
		clock    bool   // whether its release clock is the time of the cycle
	}{
		"queued":                               {state: jobs.StateQueued, seen: SeenOpen},
		"running":                              {state: jobs.StateRunning, seen: SeenOpen},
		"blocked":                              {state: jobs.StateBlocked, seen: SeenOpen},
		"failed, which can be retried":         {state: jobs.StateFailed, seen: SeenOpen},
		"done":                                 {state: jobs.StateDone, seen: SeenEnded},
		"done, with nothing owed":              {state: jobs.StateDone, from: Record{Seen: SeenOpen}},
		"cancelled with nothing put back":      {state: cancelled, seen: SeenEnded},
		"cancelled after a hold while pending": {state: cancelled, from: Record{Owed: true, Seen: SeenHolding}, seen: SeenEnded},
		"cancelled in a runner still in progress": {state: cancelled, standing: jobs.Standing{CancelledInRunner: true, RunnerInProgress: true}, seen: SeenHolding},
		"cancelled in a runner that returned":     {state: cancelled, standing: jobs.Standing{CancelledInRunner: true}, seen: SeenHolding, clock: true},
		"cancelled with its cleanup pending":      {state: cancelled, standing: jobs.Standing{CleanupPending: true}, seen: SeenHolding},
		"cancelled with the label put back":       {state: cancelled, standing: jobs.Standing{CleanupPutBack: true}, seen: SeenHolding, clock: true},
		"purged before the poll saw it end":       {purged: true, seen: SeenHolding, clock: true},
		"purged after its clock started":          {purged: true, from: Record{Owed: true, Seen: SeenHolding, ReleaseClock: at(-9)}, seen: SeenHolding},
		"seen to end before":                      {state: jobs.StateQueued, from: Record{Owed: true, Seen: SeenEnded}, seen: SeenEnded},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, withVars(map[string]string{intervalVar: "0"}))
			id := e.Jobs.add(jobs.KindFix, 42, tc.state, "af:fix")
			if e.Jobs.set(id, tc.state, &tc.standing); tc.purged {
				e.Jobs.set(id, "", nil)
			}
			from := tc.from
			if from == (Record{}) {
				from = Record{Owed: true, Seen: SeenOpen} // owed, so that the record stays to be read
			}
			from.Issue, from.Label, from.JobID = 42, "af:fix", id
			e.record(from)
			e.Clock.Advance(time.Minute)
			e.cycle()
			recs := e.records()
			switch {
			case tc.seen == "" && len(recs) != 0:
				t.Errorf("records %+v, want the record dropped", recs)
			case tc.seen == "":
			case len(recs) != 1 || recs[0].Seen != tc.seen:
				t.Errorf("records %+v, want the record %s", recs, tc.seen)
			case tc.clock != recs[0].ReleaseClock.Equal(e.Clock.Now()) || (!tc.clock && !recs[0].ReleaseClock.Equal(from.ReleaseClock)):
				t.Errorf("the release clock is %v, want it started now: %v", recs[0].ReleaseClock, tc.clock)
			}
		})
	}
}
