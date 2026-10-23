package worker

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

// loseBoxes makes the sandbox of the newest lease gone, as a sandbox that hub took away is, the first time a tool
// of the brain has been called in it, for the first n leases of the attempt. before is called with the lease just
// before it is lost.
func (e *env) loseBoxes(n int, before func(lost fakehub.Lease)) {
	killed := map[string]bool{}
	prev := e.sink.hook
	e.sink.hook = func(typ string, data json.RawMessage) {
		if prev != nil {
			prev(typ, data)
		}
		if typ != "tool_call" || len(killed) >= n {
			return
		}
		leases := e.hub.Leases()
		current := leases[len(leases)-1]
		if killed[current.ID] {
			return
		}
		killed[current.ID] = true
		if before != nil {
			before(current)
		}
		e.hub.KillSandbox(current.ID)
	}
}

// boxReplacedSteps are the step events named box_replaced that the environment has seen, as their data.
func boxReplacedSteps(e *env) []map[string]any {
	var out []map[string]any
	for _, ev := range e.sink.emitted() {
		var data map[string]any
		if ev.Type == "step" && json.Unmarshal(ev.Data, &data) == nil && data["name"] == "box_replaced" {
			out = append(out, data)
		}
	}
	return out
}

// TestTheFirstBoxLossIsReplacedInPlaceAndTheLostLeaseIsReleased_TS_14_44 verifies TS-14-44.
// Verifies: 14-REQ-7.2
func TestTheFirstBoxLossIsReplacedInPlaceAndTheLostLeaseIsReleased_TS_14_44(t *testing.T) {
	t.Run("a fix", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		e.loseBoxes(1, nil)
		var branches []string
		e.onStep("release", func() { branches = localBranches(t, e.dir) })

		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		if err != nil || envelope(t, res)["status"] != "done" {
			t.Fatalf("result %s, error %v", res.Envelope, err)
		}
		leases := e.hub.Leases()
		if len(leases) != 2 || !leases[0].Released || !leases[1].Released {
			t.Fatalf("leases %+v, want two, both given back", leases)
		}
		if leases[1].Purpose != leases[0].Purpose || leases[1].Profile != leases[0].Profile {
			t.Errorf("the replacement is for %q with the profile %q, want %q and %q", leases[1].Purpose, leases[1].Profile, leases[0].Purpose, leases[0].Profile)
		}
		w := warningOf(envelope(t, res), WarnBoxReplaced)
		if message, _ := w["message"].(string); w == nil || !strings.Contains(message, leases[0].ID) || !strings.Contains(message, leases[1].ID) {
			t.Errorf("the box_replaced warning is %v, want both sandbox ids", w)
		}
		j := journalOf(t, e)
		if len(j.Leases) != 2 || !j.Leases[0].Lost || !j.Leases[0].Released || j.Leases[1].Lost {
			t.Errorf("the journal has the leases %+v, want the first recorded as lost and given back", j.Leases)
		}
		if got := e.sink.lastHub()[2]; got != leases[1].ID {
			t.Errorf("the recorder was told the sandbox %q last, want the replacement %q", got, leases[1].ID)
		}
		if n := len(e.hub.RequestsTo("DELETE", sandboxesPath+"/"+leases[0].ID)); n != 1 {
			t.Errorf("the lost lease was deleted %d times, want once", n)
		}
		if n := len(e.hub.Gateway().Created()); n != 2 {
			t.Errorf("%d boxes were made, want the lost one and its replacement", n)
		}
		if !slices.Equal(branches, []string{"fix/42-greeting-wrong"}) {
			t.Errorf("the clone has the branches %v, want the one the change was landed on: the lost box's work is neither landed nor parked", branches)
		}
		if pulls := e.hub.Pulls(slug); len(pulls) != 1 || pulls[0].Draft {
			t.Errorf("pull requests %+v, want one that is not a draft", pulls)
		}
		if w := warningOf(envelope(t, res), WarnAttemptResumed); w != nil || len(resumeSteps(e)) != 0 {
			t.Errorf("the first delivery that went on in a new box says it resumed: %v, %v", w, resumeSteps(e))
		}
	})

	t.Run("a fix with one attempt", func(t *testing.T) {
		// With one attempt the loop does not try again in the box that is gone, so the only sign of the loss is
		// what the tools of the box said.
		e := loopEnv(t, fixFiles, map[string]string{"AF_MAX_ATTEMPTS": "1"}, nil, fixSteps()...)
		e.loseBoxes(1, nil)

		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		if err != nil || envelope(t, res)["status"] != "done" || warningOf(envelope(t, res), WarnBoxReplaced) == nil {
			t.Fatalf("result %s, error %v", res.Envelope, err)
		}
	})

	t.Run("a triage", func(t *testing.T) {
		look := map[string]any{"tool": "list_files", "args": map[string]any{"path": "."}, "expect": "ok"}
		e := loopEnv(t, triageFiles, nil, nil, look, look, submitStep(probableDiagnosis))
		e.loseBoxes(1, nil)

		res, err := e.run(e.request(jobs.KindTriage, issueInput("42")))

		if err != nil || envelope(t, res)["status"] != "done" {
			t.Fatalf("result %s, error %v", res.Envelope, err)
		}
		if leases := e.hub.Leases(); len(leases) != 2 || !leases[0].Released || !leases[1].Released || warningOf(envelope(t, res), WarnBoxReplaced) == nil {
			t.Errorf("leases %+v and the warning %v, want two leases given back and the replacement said", leases, warningOf(envelope(t, res), WarnBoxReplaced))
		}
	})
}

// TestAFailedReleaseOfTheLostLeaseIsLoggedAndTheBoxIsReplacedAllTheSame_TS_14_45 verifies TS-14-45.
// Verifies: 14-REQ-7.2
func TestAFailedReleaseOfTheLostLeaseIsLoggedAndTheBoxIsReplacedAllTheSame_TS_14_45(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	var logged bytes.Buffer
	e.runner.cfg.Logger = slog.New(slog.NewTextHandler(&logged, nil))
	e.loseBoxes(1, func(lost fakehub.Lease) {
		e.hub.Inject("DELETE", sandboxesPath+"/"+lost.ID, fakehub.Fault{Status: 500})
	})

	res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

	if err != nil || envelope(t, res)["status"] != "done" || warningOf(envelope(t, res), WarnBoxReplaced) == nil {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	leases := e.hub.Leases()
	if len(leases) != 2 || leases[0].Released || !leases[1].Released {
		t.Errorf("leases %+v, want the lost one still held, for the reconciliation, and the replacement given back", leases)
	}
	if !strings.Contains(logged.String(), "WARN") || !strings.Contains(logged.String(), leases[0].ID) || strings.Contains(logged.String(), pat) {
		t.Errorf("log = %q, want a WARN that names the lost lease and holds no token", logged.String())
	}
	if j := journalOf(t, e); len(j.Leases) != 2 || !j.Leases[0].Lost || j.Leases[0].Released {
		t.Errorf("the journal has the leases %+v, want the first recorded as lost and not given back", j.Leases)
	}
}

// TestTheDeadlineIsCutToTwoMinutesBeforeTheReplacementExpiresAndAShortReplacementIsRefused_TS_14_46 verifies
// TS-14-46.
// Verifies: 14-REQ-7.3
func TestTheDeadlineIsCutToTwoMinutesBeforeTheReplacementExpiresAndAShortReplacementIsRefused_TS_14_46(t *testing.T) {
	t.Run("ten minutes", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		seen := e.settingsSeen()
		e.loseBoxes(1, func(fakehub.Lease) { e.hub.SetLeaseTTL(10 * time.Minute) })

		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		if err != nil || envelope(t, res)["status"] != "done" {
			t.Fatalf("result %s, error %v", res.Envelope, err)
		}
		if seen.Timeout > 8*time.Minute || seen.Timeout < 7*time.Minute {
			t.Errorf("the loop was given %s, want what is left of the replacement lease but two minutes: 8 minutes, less the time of the lease", seen.Timeout)
		}
		w := warningOf(envelope(t, res), WarnLeaseShortenedTimeout)
		if w == nil {
			t.Errorf("the envelope has no %s warning", WarnLeaseShortenedTimeout)
		}
	})

	t.Run("ninety seconds", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		e.loseBoxes(1, func(fakehub.Lease) { e.hub.SetLeaseTTL(90 * time.Second) })

		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		f := transient(err)
		if f == nil || f.Category != "sandbox" || !f.Retryable || len(res.Envelope) != 0 {
			t.Fatalf("result %q, error %v, want a transient error of category sandbox that may be tried again", res.Envelope, err)
		}
		if leases := e.hub.Leases(); len(leases) != 2 || !leases[0].Released || !leases[1].Released {
			t.Errorf("leases %+v, want two, both given back", leases)
		}
	})
}

// TestAfterTheReplacementAnAppliedChangeIsMeasuredWithoutTheBrainAndOtherwiseTheLoopRunsWithWhatIsLeft_TS_14_47
// verifies TS-14-47.
// Verifies: 14-REQ-7.4
func TestAfterTheReplacementAnAppliedChangeIsMeasuredWithoutTheBrainAndOtherwiseTheLoopRunsWithWhatIsLeft_TS_14_47(t *testing.T) {
	t.Run("a box lost after the apply", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		killed := false
		e.runner.reachedHook = func(stage string) {
			if stage == "applied" && !killed {
				killed = true
				e.hub.KillSandbox(e.hub.Leases()[0].ID)
			}
		}

		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		if err != nil || envelope(t, res)["status"] != "done" || warningOf(envelope(t, res), WarnBoxReplaced) == nil {
			t.Fatalf("result %s, error %v", res.Envelope, err)
		}
		if got := e.brainRuns(); got != 1 {
			t.Errorf("the brain was started %d times, want the one of the box that was lost: the change is measured, not made again", got)
		}
		if pulls := e.hub.Pulls(slug); len(pulls) != 1 || len(e.hub.Leases()) != 2 {
			t.Errorf("%d pull requests and %d leases, want one and two", len(pulls), len(e.hub.Leases()))
		}
	})

	t.Run("a box lost during the brain", func(t *testing.T) {
		e := loopEnv(t, fixFiles, map[string]string{"AF_MAX_BUDGET_USD": "1.00", "AF_MAX_TURNS": "20"}, nil, fixSteps()...)
		e.usePricing(scriptFile(t, fixSteps()...), map[int]pricedRun{1: {cost: 0.25, turns: 5, costSource: "reported"}})
		seen := e.settingsSeen()
		e.loseBoxes(1, nil)
		brains, tree := 0, "not looked at"
		e.onStep("brain", func() {
			if brains++; brains == 2 {
				tree = gitOut(t, filepath.Join(e.dir, cloneDirName), "status", "--porcelain")
			}
		})

		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		if err != nil || envelope(t, res)["status"] != "done" {
			t.Fatalf("result %s, error %v", res.Envelope, err)
		}
		if !near(seen.Budget, 0.75) || seen.MaxTurns != 15 {
			t.Errorf("the brain was given $%.2f and %d turns, want the pinned $1.00 and 20 less the $0.25 and 5 that were spent: 0.75 and 15", seen.Budget, seen.MaxTurns)
		}
		if brains != 2 || tree != "" {
			t.Errorf("the brain was started %d times, and the tree at the second was %q, want a second run from a tree with no change in it", brains, tree)
		}
	})
}

// TestASecondLossOrAReplacementThatIsRefusedOrCannotBeOpenedEndsTheDeliveryAsARetryableSandboxError_TS_14_48
// verifies TS-14-48.
// Verifies: 14-REQ-7.5
func TestASecondLossOrAReplacementThatIsRefusedOrCannotBeOpenedEndsTheDeliveryAsARetryableSandboxError_TS_14_48(t *testing.T) {
	cases := map[string]struct {
		losses int
		before func(*env) func(fakehub.Lease)
		leases int
	}{
		"a second loss": {losses: 2, leases: 2},
		"the replacement lease is refused": {losses: 1, leases: 1, before: func(e *env) func(fakehub.Lease) {
			return func(fakehub.Lease) { e.hub.Inject("POST", sandboxesPath, fakehub.Fault{Status: 500}) }
		}},
		"the replacement lease is refused for the token": {losses: 1, leases: 1, before: func(e *env) func(fakehub.Lease) {
			return func(fakehub.Lease) { e.hub.Inject("POST", sandboxesPath, fakehub.Fault{Status: 403}) }
		}},
		"the replacement box cannot be opened": {losses: 1, leases: 2, before: func(e *env) func(fakehub.Lease) {
			return func(fakehub.Lease) { e.hub.Gateway().RefuseBoxes("no room for another box") }
		}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
			var before func(fakehub.Lease)
			if c.before != nil {
				before = c.before(e)
			}
			e.loseBoxes(c.losses, before)

			res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

			f := transient(err)
			if f == nil || f.Category != "sandbox" || !f.Retryable || len(res.Envelope) != 0 {
				t.Fatalf("result %q, error %v, want a transient error of category sandbox that may be tried again", res.Envelope, err)
			}
			leases := e.hub.Leases()
			if len(leases) != c.leases {
				t.Errorf("%d leases, want %d", len(leases), c.leases)
			}
			for _, l := range leases {
				if !l.Released {
					t.Errorf("the lease %s is still held", l.ID)
				}
			}
			if _, err := os.Stat(filepath.Join(e.dir, cloneDirName)); err != nil {
				t.Errorf("the clone is gone: %v", err)
			}
			if pulls := e.hub.Pulls(slug); len(pulls) != 0 || len(localBranches(t, e.dir)) != 0 {
				t.Errorf("%d pull requests and the branches %v, want none: the work of a lost box is neither landed nor parked", len(pulls), localBranches(t, e.dir))
			}
		})
	}
}

// TestARefusedFirstLeaseAnUnopenableFirstBoxOrAnotherSandboxFailureTakesNoReplacementLease_TS_14_49 verifies
// TS-14-49.
// Verifies: 14-REQ-7.6
func TestARefusedFirstLeaseAnUnopenableFirstBoxOrAnotherSandboxFailureTakesNoReplacementLease_TS_14_49(t *testing.T) {
	cases := map[string]func(*env){
		"hub is refused the first lease by 500":     func(e *env) { e.hub.Inject("POST", sandboxesPath, fakehub.Fault{Status: 500}) },
		"hub refuses the first lease for the token": func(e *env) { e.hub.Inject("POST", sandboxesPath, fakehub.Fault{Status: 403}) },
		"the first box cannot be opened":            func(e *env) { e.hub.Gateway().RefuseBoxes("no room for another box") },
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
			spoil(e)

			res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

			if err == nil && len(res.Envelope) == 0 {
				t.Fatal("the delivery came to no end")
			}
			if len(res.Envelope) > 0 && envelope(t, res)["status"] == "done" {
				t.Fatal("the delivery came to done: what was spoiled did not stop it")
			}
			if len(res.Envelope) > 0 && warningOf(envelope(t, res), WarnBoxReplaced) != nil {
				t.Errorf("the envelope has a box_replaced warning: %s", res.Envelope)
			}
			if n := len(e.hub.RequestsTo("POST", sandboxesPath)); n != 1 {
				t.Errorf("%d lease requests, want one: nothing was lost, so nothing is replaced", n)
			}
			if steps := boxReplacedSteps(e); len(steps) != 0 {
				t.Errorf("the delivery emitted %d box_replaced steps, want none", len(steps))
			}
		})
	}
}

// TestAReplacementEmitsABoxReplacedStepWithBothSandboxIds_TS_14_58 verifies TS-14-58.
// Verifies: 14-REQ-9.2
func TestAReplacementEmitsABoxReplacedStepWithBothSandboxIds_TS_14_58(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.loseBoxes(1, nil)

	res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	leases := e.hub.Leases()
	steps := boxReplacedSteps(e)
	if len(steps) != 1 || len(leases) != 2 || steps[0]["lost"] != leases[0].ID || steps[0]["new"] != leases[1].ID {
		t.Errorf("the box_replaced steps are %v for the leases %v, want one that names the lost sandbox and the new one", steps, leases)
	}
}

// TestAResumedDeliveryThatLosesItsBoxStillSaysWhereItResumedFrom pins that a delivery that resumed an attempt and
// then replaced its box has the stage it resumed from in its envelope, and not the stage it went on from after the
// replacement.
// Verifies: 14-REQ-9.3, 14-REQ-7.2
func TestAResumedDeliveryThatLosesItsBoxStillSaysWhereItResumedFrom(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.stopAfter("briefed")
	e.loseBoxes(1, nil)
	// The leases of the first delivery are not the ones to lose: the box lost is the newest.
	lost := len(e.hub.Leases())

	res, err := e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	env := envelope(t, res)
	w := warningOf(env, WarnAttemptResumed)
	if message, _ := w["message"].(string); w == nil || !strings.Contains(message, "stage "+stageBrain) || !strings.Contains(message, "delivery 2") {
		t.Errorf("the attempt_resumed warning is %v, want the stage the delivery resumed from", w)
	}
	if warningOf(env, WarnBoxReplaced) == nil || len(boxReplacedSteps(e)) != 1 || len(resumeSteps(e)) != 1 || len(e.hub.Leases())-lost != 2 {
		t.Errorf("box_replaced %v, %d box steps, %d resume steps, %d leases of the delivery, want the box replaced once in a delivery that resumed",
			warningOf(env, WarnBoxReplaced), len(boxReplacedSteps(e)), len(resumeSteps(e)), len(e.hub.Leases())-lost)
	}
}

// TestABoxLostBeforeTheRunPinnedWhatItFoundEndsTheDeliveryAsATransientErrorAndTheNextOneBeginsAgain pins that a run
// that has nothing to go on from is not replaced in place: there is no stage to pick up from.
// Verifies: 14-REQ-7.5
func TestABoxLostBeforeTheRunPinnedWhatItFoundEndsTheDeliveryAsATransientErrorAndTheNextOneBeginsAgain(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	syncs := 0
	e.runner.reachedHook = func(stage string) {
		// The box is synced when it is opened, and again for the detection of the checks, which comes before the brief.
		if stage == "box_synced" {
			if syncs++; syncs == 2 {
				e.hub.KillSandbox(e.hub.Leases()[0].ID)
			}
		}
	}

	res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

	f := transient(err)
	if f == nil || f.Category != "sandbox" || !f.Retryable || len(res.Envelope) != 0 || !strings.Contains(f.Message, "pinned") {
		t.Fatalf("result %q, error %v, want a transient error of category sandbox that says nothing was pinned", res.Envelope, err)
	}
	if leases := e.hub.Leases(); len(leases) != 2 || !leases[0].Released || !leases[1].Released {
		t.Errorf("leases %+v, want two, both given back", leases)
	}
	e.runner.reachedHook = nil

	res, err = e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" || warningOf(envelope(t, res), WarnResumeDiscarded) == nil {
		t.Errorf("delivery 2: result %s, error %v, want the attempt begun again, with the resume discarded", res.Envelope, err)
	}
}

// TestTheReplacementGetsWhatIsLeftOfTheTimeOfTheDelivery pins that a replacement does not give the delivery its whole
// timeout again: the loop was begun at some time, and the timeout counts from then.
// Verifies: 14-REQ-7.3
func TestTheReplacementGetsWhatIsLeftOfTheTimeOfTheDelivery(t *testing.T) {
	e := loopEnv(t, fixFiles, map[string]string{"AF_TIMEOUT": "30m"}, nil, fixSteps()...)
	var late atomic.Int64
	e.runner.cfg.Now = func() time.Time { return time.Now().Add(time.Duration(late.Load())) }
	seen := e.settingsSeen()
	e.loseBoxes(1, func(fakehub.Lease) { late.Store(int64(25 * time.Minute)) })

	res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if seen.Timeout > 5*time.Minute || seen.Timeout < 4*time.Minute {
		t.Errorf("the loop was given %s again, want what is left of the 30 minutes after 25: 5 minutes, less the time of the test", seen.Timeout)
	}
}

// TestABoxLostInALaterRoundGoesOnFromABaseWithTheChangeOfTheEarlierRoundGone pins that the loop that is run again in
// a new box begins from the base: what an earlier round applied and the measuring refused is not in the tree.
// Verifies: 14-REQ-7.4
func TestABoxLostInALaterRoundGoesOnFromABaseWithTheChangeOfTheEarlierRoundGone(t *testing.T) {
	// The scripted brain has a round of steps for each run of the brain: the second round writes and submits again.
	e := loopEnv(t, fixFiles, nil, nil)
	e.useScript(scriptRounds(t, refusedRound(), refusedRound()))
	tools, brains := 0, 0
	var tree, before string
	prev := e.sink.hook
	e.sink.hook = func(typ string, data json.RawMessage) {
		if prev != nil {
			prev(typ, data)
		}
		if typ == "tool_call" {
			if tools++; tools == 3 { // the first tool call of the second round: the first round had a write and a submission
				before = gitOut(t, filepath.Join(e.dir, cloneDirName), "status", "--porcelain")
				e.hub.KillSandbox(e.hub.Leases()[0].ID)
			}
		}
	}
	e.onStep("brain", func() {
		if brains++; brains == 3 { // the first round of the new box
			tree = gitOut(t, filepath.Join(e.dir, cloneDirName), "status", "--porcelain")
		}
	})

	res, err := e.deliver(1) // a second round needs the time that e.run does not give

	if err != nil || warningOf(envelope(t, res), WarnBoxReplaced) == nil {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if !strings.Contains(before, "src/greet.sh") {
		t.Fatalf("the tree at the loss is %q, want the change of the first round, which the measuring refused", before)
	}
	if brains < 3 || tree != "" {
		t.Errorf("the brain was started %d times, and the tree at the third was %q, want a run in the new box from a tree with no change in it", brains, tree)
	}
}
