package fix

import (
	"context"
	"crypto/rand"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestContinue_AnotherRoundNeedsCostATurnAndAMinuteForTheBrain(t *testing.T) {
	const minute = time.Minute
	for name, tc := range map[string]struct {
		cost           float64
		turns          int
		left, baseline time.Duration
		want           bool
	}{
		"everything left":                      {1, 5, 10 * minute, minute, true},
		"no cost left":                         {0, 5, 10 * minute, minute, false},
		"cost spent beyond the budget":         {-0.5, 5, 10 * minute, minute, false},
		"no turn left":                         {1, 0, 10 * minute, minute, false},
		"one turn and a cent left":             {0.01, 1, 10 * minute, minute, true},
		"the reserve takes the time":           {1, 5, 4 * minute, minute, false},
		"just a minute left after the reserve": {1, 5, 5 * minute, minute, true},
		"a second short of that minute":        {1, 5, 5*minute - time.Second, minute, false},
		"no deadline":                          {1, 5, Unbounded, 10 * minute, true},
		"a deadline already passed":            {1, 5, -2 * time.Second, 0, false},
		"a slow baseline":                      {1, 5, 16 * minute, 5 * minute, false},
		"a slow baseline with time enough":     {1, 5, 17 * minute, 5 * minute, true},
	} {
		if got := Continue(tc.cost, tc.turns, tc.left, tc.baseline); got != tc.want {
			t.Errorf("%s: Continue = %v, want %v", name, got, tc.want)
		}
	}
	// The reserve is the gate and the revert check, three times the baseline, and a minute.
	if MeasureReserve(2*minute) != 7*minute || BrainTime(10*minute, minute) != 6*minute || BrainTime(Unbounded, minute) != Unbounded {
		t.Errorf("MeasureReserve(2m) = %v, BrainTime(10m, 1m) = %v", MeasureReserve(2*minute), BrainTime(10*minute, minute))
	}
}

// loopWorld is a measured checkout with a run directory in it, and the spy brain of a loop.
type loopWorld struct {
	*measureWorld
	run   *toolio.RunDir
	rep   *recorder
	spy   *spy
	tally *brain.Tally
	// afterGate is called when the gate of a measuring has run.
	afterGate func()
}

func newLoopWorld(t *testing.T) *loopWorld {
	t.Helper()
	m := newMeasureWorld(t)
	run, err := toolio.NewRunDir(m.repo.Dir, time.Now, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m.state.RunID = run.ID
	return &loopWorld{measureWorld: m, run: run, rep: &recorder{}, tally: &brain.Tally{}}
}

// loop runs the loop of the world over a local box, with the bounds and the rounds of the spy.
func (w *loopWorld) loop(ctx context.Context, budget float64, turns int, rounds ...func(context.Context, brain.Brief) (brain.Outcome, error)) (LoopResult, error) {
	w.spy = &spy{rounds: rounds}
	var res LoopResult
	err := box.With(ctx, box.Request{Provider: "local", HostDir: w.repo.Dir, RunID: w.run.ID, Tool: "fix", Record: w.run.Record}, func(sess *box.Session) error {
		var err error
		res, err = RunLoop(ctx, LoopParams{
			Dir: w.repo.Dir, Git: w.repo.Git, Session: sess, Name: "spy", Brain: w.spy, Tally: w.tally, RunDir: w.run, Report: &warnings{}, Reporter: w.rep,
			State: &w.state, Prefix: "# Fix mandate\n", Budget: budget, MaxTurns: turns,
			Gate: func(ctx context.Context, dir string, o checks.Options) (checks.Result, error) {
				got, err := w.gate(ctx, dir, o)
				if w.afterGate != nil {
					w.afterGate()
				}
				return got, err
			},
			Conform: w.conform,
		})
		return err
	})
	return res, err
}

// broken is a change the fixture gate refuses: the script prints nothing.
var broken = map[string]string{"src/greet.sh": ":\n"}

// fixed is a change the fixture gate takes.
var fixed = map[string]string{"src/greet.sh": "echo hello\n"}

// A refused change is the task of the next round: the box is brought back to the tree that was
// measured, protected paths put back, and given the files of the run again; the rounds share
// the bounds of the run, and their number runs through it.
func TestRunLoop_ARefusalIsTheTaskOfTheNextRound(t *testing.T) {
	w := newLoopWorld(t)
	noise := "i=0; while [ $i -lt 400 ]; do echo \"line $i of the noise\"; i=$((i+1)); done\necho \"FAIL: the last word\"; exit 1\n"
	seen := map[int]map[string]string{}
	look := func(n int, next func(context.Context, brain.Brief) (brain.Outcome, error)) func(context.Context, brain.Brief) (brain.Outcome, error) {
		return func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
			seen[n] = map[string]string{}
			for _, rel := range []string{".agent-fox/gate.json", ".agent-fox/guard.json", ".specs/a.md", "src/greet.sh", "tests/greet_test.sh"} {
				seen[n][rel], _ = boxFile(ctx, b.Box, rel)
			}
			return next(ctx, b)
		}
	}

	res, err := w.loop(context.Background(), 10, 150,
		look(1, round(t, map[string]string{".specs/a.md": "# rewritten\n", "src/greet.sh": ":\n", "tests/greet_test.sh": noise}, reportPayload, 1, 10)),
		look(2, round(t, map[string]string{"src/greet.sh": "echo hello\n", "tests/greet_test.sh": measuredFiles()["tests/greet_test.sh"]}, reportPayload, 1, 10)))

	if err != nil || res.End != EndLandable || res.Rounds != 2 || !res.Measured.Lands || res.Report == nil || res.Report.Subject != "say hello" || !res.HoldsChange || res.LastRefusal == nil {
		t.Fatalf("RunLoop = %+v, %v, want a landable change in round 2 after a refusal", res, err)
	}
	gate, _ := GateFile(w.state)
	guard, _ := GuardFile(w.state)
	for n, files := range seen {
		if files[".agent-fox/gate.json"] != string(gate) || files[".agent-fox/guard.json"] != string(guard) {
			t.Errorf("round %d: the box did not hold the files of the run's state", n)
		}
	}
	if seen[1]["src/greet.sh"] != "echo helo\n" || seen[2][".specs/a.md"] != "# spec\n" || seen[2]["src/greet.sh"] != ":\n" || !strings.HasPrefix(seen[2]["tests/greet_test.sh"], "i=0;") {
		t.Errorf("the box of round 2 holds %v, want the host's tree as it was measured, the protected path put back", seen[2])
	}
	first, second := w.spy.briefs[0], w.spy.briefs[1]
	inOrder(t, second.Task, "round 2", "refusal: gate, verdict: regressed", "make check", "FAIL: the last word", ".specs/a.md")
	if strings.Contains(first.Task, "refus") || len(between(t, second.Task, "The end of the output", "")) > MaxTail+400 {
		t.Errorf("round 1 holds a refusal, or the tail in round 2 is not capped:\n%s", second.Task)
	}
	if first.Attempt != 1 || second.Attempt != 2 || first.MaxCostUSD != 10 || first.MaxTurns != 150 || second.MaxCostUSD != 9 || second.MaxTurns != 140 || first.Timeout != 0 {
		t.Errorf("the rounds got attempts %d, %d, $%v and $%v, %d and %d turns, timeout %v: want the run's numbers and what remained of its bounds, and no timeout without a deadline",
			first.Attempt, second.Attempt, first.MaxCostUSD, second.MaxCostUSD, first.MaxTurns, second.MaxTurns, first.Timeout)
	}
	if kinds := ledgerKinds(t, w.run); countOf(kinds, LedgerMeasure) != 2 || countOf(kinds, LedgerRefusal) != 1 || countOf(kinds, LedgerApply) != 2 {
		t.Errorf("ledger kinds = %v, want two measures, one refusal and two applies", kinds)
	}
	if onDisk, err := LoadState(w.run.Path); err != nil || onDisk.Rounds != 2 || onDisk.Brain != "spy" || onDisk.CostUSD != 2 || onDisk.CostSource != toolio.CostMeasured {
		t.Errorf("fix.json = %+v, %v, want the rounds and what they cost kept", onDisk, err)
	}
	for _, name := range []string{"brief.1.md", "brief.2.md"} {
		if _, err := os.Stat(filepath.Join(w.run.Path, name)); err != nil {
			t.Error(err)
		}
	}
}

// A question ends the run, and the tree of the box is not applied.
func TestRunLoop_AQuestionEndsTheRunAndTouchesNothing(t *testing.T) {
	w := newLoopWorld(t)
	before, branches := w.repo.files(t), w.repo.branches(t)
	ask := func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		edits(t, b, broken)
		out, err := submit(ctx, b, `{"question":{"text":"Which one?","options":["x","y","z"]}}`)
		out.CostUSD, out.CostSource = 0.5, brain.CostMeasured
		return out, err
	}

	res, err := w.loop(context.Background(), 10, 150, ask, round(t, fixed, reportPayload, 0, 1))

	if err != nil || res.End != EndQuestion || res.Question == nil || !slices.Equal(res.Question.Options, []string{"x", "y", "z"}) || res.HoldsChange || res.Rounds != 1 || len(w.spy.briefs) != 1 {
		t.Errorf("RunLoop = %+v, %v, want the question of the one round", res, err)
	}
	if !maps.Equal(w.repo.files(t), before) || !slices.Equal(w.repo.branches(t), branches) || slices.Contains(ledgerKinds(t, w.run), LedgerApply) {
		t.Error("the checkout changed")
	}
	if w.state.Rounds != 1 || w.state.CostUSD != 0.5 {
		t.Errorf("state = rounds %d, cost %v, want the round and its cost kept", w.state.Rounds, w.state.CostUSD)
	}
}

// A round that comes to no report ends the run with its failure, and applies nothing.
func TestRunLoop_ARoundThatFailsEndsTheLoopWithItsFailure(t *testing.T) {
	for name, tc := range map[string]struct {
		out      brain.Outcome
		err      error
		category string
	}{
		"the budget is spent":           {brain.Outcome{StopReason: brain.StopBudget, CostUSD: 10, CostSource: brain.CostMeasured}, nil, toolio.CategoryBudget},
		"the turns are spent":           {brain.Outcome{StopReason: brain.StopTurns}, nil, toolio.CategoryBudget},
		"the credential is refused":     {brain.Outcome{}, &toolio.Failure{Category: toolio.CategoryAuth, Message: "no key"}, toolio.CategoryAuth},
		"the brain ends with no report": {brain.Outcome{StopReason: brain.StopEnded}, nil, toolio.CategoryNoResult},
	} {
		t.Run(name, func(t *testing.T) {
			w := newLoopWorld(t)
			before := w.repo.files(t)

			res, err := w.loop(context.Background(), 10, 150, func(_ context.Context, b brain.Brief) (brain.Outcome, error) {
				edits(t, b, broken)
				return tc.out, tc.err
			})

			if err != nil || len(w.spy.briefs) != 1 || res.End != EndExhausted || res.HoldsChange || res.LastFailure == nil || failure(t, res.LastFailure).Category != tc.category {
				t.Errorf("RunLoop = %+v, %v, want exhausted after one round with the failure %s", res, err, tc.category)
			}
			if !maps.Equal(w.repo.files(t), before) || slices.Contains(ledgerKinds(t, w.run), LedgerApply) {
				t.Error("the checkout changed")
			}
		})
	}
}

func TestRunLoop_ABoxThatCannotBeSyncedEndsTheLoop(t *testing.T) {
	w := newLoopWorld(t)
	var bx *box.Session
	w.afterGate = func() { _ = bx.Close() } // the box is gone once the change is measured and refused
	closing := func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		bx = b.Box
		return round(t, broken, reportPayload, 0, 1)(ctx, b)
	}

	res, err := w.loop(context.Background(), 10, 150, closing)

	if err != nil || len(w.spy.briefs) != 1 || res.End != EndExhausted || res.LastFailure == nil || failure(t, res.LastFailure).Category != toolio.CategorySandbox || !res.HoldsChange {
		t.Errorf("RunLoop = %+v, %v, want one round and the failure of the sandbox, with the change in the tree", res, err)
	}
}

// The brain is given the time to the deadline less the reserve for measuring, never less than a
// second, and no round starts that would leave less than a minute.
func TestRunLoop_TheDeadlineBoundsTheBrainAndTheRounds(t *testing.T) {
	for name, tc := range map[string]struct {
		deadline time.Duration
		first    map[string]string
		timeout  func(time.Duration) bool
		rounds   int
	}{
		"a deadline that leaves the brain nothing": {30 * time.Second, fixed, func(d time.Duration) bool { return d == time.Second }, 1},
		"a deadline too short for a second round":  {90 * time.Second, broken, func(d time.Duration) bool { return d > 0 && d <= 90*time.Second-MeasureReserve(0) }, 1},
	} {
		t.Run(name, func(t *testing.T) {
			w := newLoopWorld(t)
			ctx, cancel := context.WithTimeout(context.Background(), tc.deadline)
			defer cancel()

			_, err := w.loop(ctx, 10, 150, round(t, tc.first, reportPayload, 1, 1), round(t, fixed, reportPayload, 1, 1))

			if err != nil || len(w.spy.briefs) != tc.rounds || !tc.timeout(w.spy.briefs[0].Timeout) {
				t.Errorf("RunLoop: %v, %d rounds, a timeout of %v", err, len(w.spy.briefs), w.spy.briefs[0].Timeout)
			}
		})
	}
}

// A run that is stopped returns the stop with what it knows, for the caller to keep the work.
func TestRunLoop_AStopIsReturnedWithWhatWasDone(t *testing.T) {
	for name, tc := range map[string]struct {
		at     string // before, in a round, or between rounds
		rounds int
	}{"before": {"before", 0}, "in a round": {"round", 2}, "between rounds": {"between", 1}} {
		t.Run(name, func(t *testing.T) {
			w := newLoopWorld(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stopping := round(t, broken, reportPayload, 0, 1)
			switch tc.at {
			case "before":
				cancel()
			case "round":
				stopping = func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
					out, err := round(t, broken, reportPayload, 0, 1)(ctx, b)
					cancel() // as the brain ends, before its tree is applied
					return out, err
				}
			case "between":
				w.afterGate = cancel // as the first change has been measured and refused
			}

			res, err := w.loop(ctx, 10, 150, round(t, broken, reportPayload, 0, 1), stopping)

			if err == nil || ctx.Err() == nil || res.Rounds != tc.rounds || len(w.spy.briefs) != tc.rounds || res.End == EndLandable {
				t.Errorf("RunLoop = %+v, %v, want the stop after %d rounds", res, err, tc.rounds)
			}
			if tc.at == "between" && res.LastRefusal == nil {
				t.Error("the refusal of the round before the stop is lost")
			}
		})
	}
}

// A failure to measure is not a round that failed: it ends the loop, and leaves the work where
// it is.
func TestRunLoop_AFailureToMeasureEndsTheLoopWithTheChangeInTheTree(t *testing.T) {
	w := newLoopWorld(t)
	foreign := func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		w.edit(t, "docs/other.md", "someone's\n")
		testutil.Commit(t, w.repo.Dir, "made by hand")
		return round(t, map[string]string{"docs/readme.md": "readme, changed\n"}, reportPayload, 0, 1)(ctx, b)
	}

	res, err := w.loop(context.Background(), 10, 150, foreign, round(t, fixed, reportPayload, 0, 1))

	if f := failure(t, err); f.Category != toolio.CategoryGit || !strings.Contains(f.Message, "made by hand") || len(w.spy.briefs) != 1 || res.Rounds != 1 {
		t.Errorf("RunLoop = %+v, %v, want git naming the commit, and no second round", res, err)
	}
	if w.repo.read(t, "docs/readme.md") != "readme, changed\n" {
		t.Error("the applied change is not in the tree")
	}
}
