package fix

import (
	"context"
	"crypto/rand"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/policy"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TestAnotherRoundNeedsCostTurnsAndTimeForTheBrain_TS_09_47 verifies TS-09-47.
// Verifies: 09-REQ-7.2
func TestAnotherRoundNeedsCostTurnsAndTimeForTheBrain_TS_09_47(t *testing.T) {
	const min = time.Minute
	for name, tc := range map[string]struct {
		cost     float64
		turns    int
		left     time.Duration
		baseline time.Duration
		want     bool
	}{
		"everything left":                      {1, 5, 10 * min, min, true},
		"no cost left":                         {0, 5, 10 * min, min, false},
		"cost spent beyond the budget":         {-0.5, 5, 10 * min, min, false},
		"a cent left":                          {0.01, 1, 10 * min, min, true},
		"no turn left":                         {1, 0, 10 * min, min, false},
		"one turn left":                        {1, 1, 10 * min, min, true},
		"the reserve takes the time":           {1, 5, 4 * min, min, false},
		"just a minute left after the reserve": {1, 5, 5 * min, min, true},
		"a second short of that minute":        {1, 5, 5*min - time.Second, min, false},
		"no deadline":                          {1, 5, Unbounded, 10 * min, true},
		"a deadline already passed":            {1, 5, -2 * time.Second, 0, false},
		"a slow baseline":                      {1, 5, 16 * min, 5 * min, false},
		"a slow baseline with time enough":     {1, 5, 17 * min, 5 * min, true},
	} {
		if got := Continue(tc.cost, tc.turns, tc.left, tc.baseline); got != tc.want {
			t.Errorf("%s: Continue(%v, %d, %v, %v) = %v, want %v", name, tc.cost, tc.turns, tc.left, tc.baseline, got, tc.want)
		}
	}
	if got := BrainTime(10*min, min); got != 6*min {
		t.Errorf("BrainTime(10m, 1m) = %v, want the time left less 3 x 1m + 1m, 6m", got)
	}
	if got := BrainTime(Unbounded, min); got != Unbounded {
		t.Errorf("BrainTime with no deadline = %v, want Unbounded", got)
	}
	if got := MeasureReserve(2 * min); got != 7*min {
		t.Errorf("MeasureReserve(2m) = %v, want 3 x 2m + 1m", got)
	}
	if got := MeasureReserve(0); got != min {
		t.Errorf("MeasureReserve(0) = %v, want a minute", got)
	}
}

// loopWorld is a measured checkout with a run directory in it, and the spy brain of a loop.
type loopWorld struct {
	*measureWorld
	run    *toolio.RunDir
	rep    *recorder
	report *warnings
	tally  *brain.Tally
	spy    *loopSpy
	// afterGate is called when the gate of a measuring has run.
	afterGate func()
}

func newLoopWorld(t *testing.T) *loopWorld {
	t.Helper()
	m := newMeasureWorld(t, measuredFiles())
	run, err := toolio.NewRunDir(m.repo.Dir, time.Now, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m.state.RunID = run.ID
	return &loopWorld{measureWorld: m, run: run, rep: &recorder{}, report: &warnings{}, tally: &brain.Tally{}}
}

// loopSpy is a brain that does what its rounds say, one for each Run, and keeps the briefs.
type loopSpy struct {
	rounds []func(ctx context.Context, b brain.Brief) (brain.Outcome, error)
	briefs []brain.Brief
}

func (s *loopSpy) Run(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
	s.briefs = append(s.briefs, b)
	if len(s.briefs) > len(s.rounds) {
		return brain.Outcome{StopReason: brain.StopEnded}, nil
	}
	return s.rounds[len(s.briefs)-1](ctx, b)
}

// loop runs the loop of the world over a box, with the bounds.
func (w *loopWorld) loop(t *testing.T, ctx context.Context, budget float64, turns int, rounds ...func(context.Context, brain.Brief) (brain.Outcome, error)) (LoopResult, error) {
	t.Helper()
	w.spy = &loopSpy{rounds: rounds}
	var res LoopResult
	err := box.With(ctx, box.Request{Provider: "local", HostDir: w.repo.Dir, RunID: w.run.ID, Tool: "fix", Record: w.run.Record}, func(sess *box.Session) error {
		var err error
		res, err = RunLoop(ctx, LoopParams{
			Dir: w.repo.Dir, Git: w.repo.Git, Session: sess, Name: "spy", Brain: w.spy, Tally: w.tally, RunDir: w.run, Report: w.report, Reporter: w.rep,
			State: &w.state, Prefix: "# Fix mandate\n", Budget: budget, MaxTurns: turns,
			Gate: func(ctx context.Context, dir string, o checks.Options) (checks.Result, error) {
				got, err := w.measureWorld.gateSeam()(ctx, dir, o)
				if w.afterGate != nil {
					w.afterGate()
				}
				return got, err
			},
			Conform: w.measureWorld.conformSeam(),
		})
		return err
	})
	return res, err
}

// ledger is the kinds of the records of the ledger of the run.
func (w *loopWorld) ledger(t *testing.T) []string {
	t.Helper()
	entries, err := w.run.Ledger()
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range entries {
		kinds = append(kinds, e.Kind)
	}
	return kinds
}

// edits writes files into the box of the brief.
func edits(t *testing.T, b brain.Brief, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(b.Box.Spec().Workdir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// inBox is a file of the box of the brief.
func inBox(b brain.Brief, name string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(b.Box.Spec().Workdir, filepath.FromSlash(name)))
	return string(data), err == nil
}

// round makes a round of a spy that writes the files, submits the payload and has used the
// cost and the turns.
func round(t *testing.T, files map[string]string, payload string, cost float64, turns int) func(context.Context, brain.Brief) (brain.Outcome, error) {
	return func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		edits(t, b, files)
		out, err := submit(ctx, b, payload)
		out.CostUSD, out.Turns, out.CostSource = cost, turns, brain.CostMeasured
		return out, err
	}
}

// broken is a change that the fixture gate does not take: the script prints nothing.
var broken = map[string]string{"src/greet.sh": ":\n"}

// fixed is the change that the fixture gate takes.
var fixed = map[string]string{"src/greet.sh": "echo hello\n"}

const loopReportPayload = `{"subject":"say hello","root_cause":"the script prints nothing","change":"say hello","files":[{"path":"src/greet.sh","why":"the word"}]}`

// TestARefusalBecomesTheNextRoundWithTheBoxAsMeasured_TS_09_24 verifies TS-09-24, 36, 44
// and 46 for a refusal inside an attempt: gate.json and guard.json are in the box when each
// round starts, the box of round 2 holds the host tree with the protected path put back, the
// ledger holds the measure and the refusal, the task block of round 2 holds them, and the
// run-wide number of the round is the number of the brief.
// Verifies: 09-REQ-3.7, 09-REQ-5.6, 09-REQ-6.7, 09-REQ-7.1
func TestARefusalBecomesTheNextRoundWithTheBoxAsMeasured_TS_09_24(t *testing.T) {
	w := newLoopWorld(t)
	noise := "i=0; while [ $i -lt 400 ]; do echo \"line $i of the noise that a check prints\"; i=$((i+1)); done\necho \"FAIL: the last word\"; exit 1\n"
	seen := map[int]map[string]string{}
	snapshot := func(n int, ctx context.Context, b brain.Brief) {
		files := map[string]string{}
		for _, name := range []string{".agent-fox/gate.json", ".agent-fox/guard.json"} {
			data, err := fs.ReadFile(b.Box.FS(ctx), name)
			if err != nil {
				t.Errorf("round %d: %s is not in the box when the brain starts: %v", n, name, err)
			}
			files[name] = string(data)
		}
		for _, name := range []string{".specs/a.md", "src/greet.sh", "tests/greet_test.sh"} {
			files[name], _ = inBox(b, name)
		}
		seen[n] = files
	}
	first := func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		snapshot(1, ctx, b)
		return round(t, map[string]string{".specs/a.md": "# rewritten\n", "src/greet.sh": ":\n", "tests/greet_test.sh": noise}, loopReportPayload, 1, 10)(ctx, b)
	}
	second := func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		snapshot(2, ctx, b)
		return round(t, map[string]string{"src/greet.sh": "echo hello\n", "tests/greet_test.sh": measuredFiles()["tests/greet_test.sh"]}, loopReportPayload, 1, 10)(ctx, b)
	}

	res, err := w.loop(t, context.Background(), 10, 150, first, second)

	if err != nil {
		t.Fatal(err)
	}
	if res.End != EndLandable || res.Rounds != 2 || !res.Measured.Lands || res.Report == nil || res.Report.Subject != "say hello" || !res.HoldsChange {
		t.Fatalf("result = %+v, want a landable change in round 2", res)
	}
	gate, _ := GateFile(w.state)
	guard, _ := GuardFile(w.state)
	for n, files := range seen {
		if files[".agent-fox/gate.json"] != string(gate) || files[".agent-fox/guard.json"] != string(guard) {
			t.Errorf("round %d: the box held %q and %q, want the files of the run's state", n, files[".agent-fox/gate.json"], files[".agent-fox/guard.json"])
		}
	}
	if got := seen[2][".specs/a.md"]; got != "# spec\n" {
		t.Errorf("round 2: .specs/a.md = %q in the box, want its content at the base: the protected path was put back, and the box synced", got)
	}
	if seen[2]["src/greet.sh"] != ":\n" || !strings.HasPrefix(seen[2]["tests/greet_test.sh"], "i=0;") {
		t.Errorf("round 2: the box holds %q and %q, want the host tree as it was measured", seen[2]["src/greet.sh"], seen[2]["tests/greet_test.sh"])
	}
	if seen[1]["src/greet.sh"] != measuredFiles()["src/greet.sh"] {
		t.Errorf("round 1: the box did not hold the checkout: %q", seen[1]["src/greet.sh"])
	}

	task := w.spy.briefs[1].Task
	for _, want := range []string{"regressed", "make check", "FAIL: the last word", ".specs/a.md", "refusal: gate", "round 2"} {
		if !strings.Contains(task, want) {
			t.Errorf("the task block of round 2 lacks %q:\n%s", want, task)
		}
	}
	if strings.Contains(w.spy.briefs[0].Task, "refused") {
		t.Errorf("the task block of round 1 holds a refusal:\n%s", w.spy.briefs[0].Task)
	}
	if tail := task[strings.Index(task, "The end of the output"):]; len(tail) > MaxTail+400 {
		t.Errorf("the refusal in the task block is %d bytes, want the tails capped at %d each", len(tail), MaxTail)
	}
	kinds := w.ledger(t)
	count := func(k string) int { return strings.Count(strings.Join(kinds, " "), k) }
	if count("measure") != 2 || count("refusal") != 1 || count("apply") != 2 {
		t.Errorf("ledger kinds = %v, want two measures, one refusal and two applies", kinds)
	}
	if attempts := []int{w.spy.briefs[0].Attempt, w.spy.briefs[1].Attempt}; !slices.Equal(attempts, []int{1, 2}) {
		t.Errorf("Brief.Attempt = %v, want the run-wide number of the round", attempts)
	}
	if w.state.Rounds != 2 {
		t.Errorf("rounds in the state = %d", w.state.Rounds)
	}
	if onDisk, err := LoadState(w.run.Path); err != nil || onDisk.Rounds != 2 || onDisk.Brain != "spy" || onDisk.CostUSD != 2 || onDisk.CostSource != toolio.CostMeasured {
		t.Errorf("fix.json = %+v, %v, want the rounds and what they cost kept after each", onDisk, err)
	}
}

// TestARoundWithNoSubmissionAppliesNothingAndTheRunEnds_TS_09_37 verifies TS-09-37 in
// the loop.
// Verifies: 09-REQ-5.7
func TestARoundWithNoSubmissionAppliesNothingAndTheRunEnds_TS_09_37(t *testing.T) {
	w := newLoopWorld(t)
	before := snapshotOf(t, w.repo.Dir, allPaths(t, w.repo.Dir))
	editsOnly := func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		edits(t, b, broken)
		return brain.Outcome{StopReason: brain.StopEnded}, nil
	}

	res, err := w.loop(t, context.Background(), 10, 150, editsOnly)

	if err != nil {
		t.Fatal(err)
	}
	if res.End != EndExhausted || res.HoldsChange || res.LastFailure == nil || failure(t, res.LastFailure).Category != toolio.CategoryNoResult || res.Rounds != 1 {
		t.Errorf("result = %+v, want exhausted with the failure of a round that left no report", res)
	}
	if after := snapshotOf(t, w.repo.Dir, allPaths(t, w.repo.Dir)); !equalSnapshots(before, after) {
		t.Errorf("the checkout changed: %v", after)
	}
	if kinds := w.ledger(t); slices.Contains(kinds, "apply") || slices.Contains(kinds, "measure") {
		t.Errorf("ledger kinds = %v, want no apply and no measure", kinds)
	}
}

// TestAQuestionEndsTheRunAndTouchesNothing_TS_09_30 verifies TS-09-30 in the loop.
// Verifies: 09-REQ-4.5
func TestAQuestionEndsTheRunAndTouchesNothing_TS_09_30(t *testing.T) {
	w := newLoopWorld(t)
	before := snapshotOf(t, w.repo.Dir, allPaths(t, w.repo.Dir))
	branches := w.localBranches(t)
	ask := func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		edits(t, b, broken)
		out, err := submit(ctx, b, `{"question":{"text":"Which one?","options":["x","y","z"],"root_cause":"two readings"}}`)
		out.CostUSD, out.CostSource = 0.5, brain.CostMeasured
		return out, err
	}

	res, err := w.loop(t, context.Background(), 10, 150, ask, round(t, fixed, loopReportPayload, 0, 1))

	if err != nil {
		t.Fatal(err)
	}
	if res.End != EndQuestion || res.Question == nil || res.Question.Text != "Which one?" || !slices.Equal(res.Question.Options, []string{"x", "y", "z"}) || res.HoldsChange || res.Rounds != 1 {
		t.Errorf("result = %+v", res)
	}
	if len(w.spy.briefs) != 1 {
		t.Errorf("the brain ran %d times: a question ends the run", len(w.spy.briefs))
	}
	if after := snapshotOf(t, w.repo.Dir, allPaths(t, w.repo.Dir)); !equalSnapshots(before, after) || !slices.Equal(w.localBranches(t), branches) {
		t.Errorf("the checkout changed: %v", after)
	}
	if kinds := w.ledger(t); slices.Contains(kinds, "apply") || slices.Contains(kinds, "measure") {
		t.Errorf("ledger kinds = %v", kinds)
	}
	if w.state.Rounds != 1 || w.state.CostUSD != 0.5 {
		t.Errorf("state = rounds %d, cost %v, want the round and its cost kept", w.state.Rounds, w.state.CostUSD)
	}
}

// branches are the local branches of the checkout.
func (w *measureWorld) localBranches(t *testing.T) []string {
	t.Helper()
	return strings.Fields(gitDo(t, w.repo.Dir, "for-each-ref", "--format=%(refname:short)", "refs/heads"))
}

// TestTheRoundsOfARunShareItsBounds_TS_09_46 verifies TS-09-46 as it stands: a refused round
// spends part of the budget of the run, the next round gets what remained of it, and the
// numbers of the rounds run through the run.
// Verifies: 09-REQ-7.1
func TestTheRoundsOfARunShareItsBounds_TS_09_46(t *testing.T) {
	w := newLoopWorld(t)

	res, err := w.loop(t, context.Background(), 10, 150,
		round(t, map[string]string{"src/greet.sh": ":\n", "change.go": "package x\n"}, loopReportPayload, 4, 20),
		round(t, fixed, loopReportPayload, 1, 5))

	if err != nil {
		t.Fatal(err)
	}
	if res.End != EndLandable || res.Rounds != 2 {
		t.Fatalf("result = %+v, want a landable change in round 2", res)
	}
	var numbers []int
	for _, b := range w.spy.briefs {
		numbers = append(numbers, b.Attempt)
	}
	if !slices.Equal(numbers, []int{1, 2}) {
		t.Errorf("Brief.Attempt = %v, want 1, 2", numbers)
	}
	if b := w.spy.briefs[0]; b.MaxCostUSD != 10 || b.MaxTurns != 150 {
		t.Errorf("round 1 got $%v and %d turns, want the whole run", b.MaxCostUSD, b.MaxTurns)
	}
	if b := w.spy.briefs[1]; b.MaxCostUSD != 6 || b.MaxTurns != 130 {
		t.Errorf("round 2 got $%v and %d turns, want what remained of the run", b.MaxCostUSD, b.MaxTurns)
	}
	if task := w.spy.briefs[1].Task; !strings.Contains(task, "round 2") || strings.Contains(task, "attempt") {
		t.Errorf("the task block of round 2 =\n%s\nwant the round, and no attempt", task)
	}
	for _, n := range []string{"brief.1.md", "brief.2.md"} {
		if _, err := os.Stat(filepath.Join(w.run.Path, n)); err != nil {
			t.Errorf("%s: %v", n, err)
		}
	}
	if w.state.Rounds != 2 || w.state.CostUSD != 5 {
		t.Errorf("state = rounds %d, cost %v", w.state.Rounds, w.state.CostUSD)
	}
	if w.spy.briefs[0].Timeout != 0 {
		t.Errorf("round 1 got a timeout of %v: a command with no deadline gives the brain none", w.spy.briefs[0].Timeout)
	}
}

// TestARoundThatFailsEndsTheRunWithItsFailure_TS_09_49 verifies TS-09-49 as it stands: a
// round that comes to no report ends the run, whatever the failure, and it is the run's.
// Verifies: 09-REQ-7.4
func TestARoundThatFailsEndsTheRunWithItsFailure_TS_09_49(t *testing.T) {
	for name, tc := range map[string]struct {
		round    func(context.Context, brain.Brief) (brain.Outcome, error)
		category string
		runs     int
	}{
		"the budget is spent": {func(context.Context, brain.Brief) (brain.Outcome, error) {
			return brain.Outcome{StopReason: brain.StopBudget, CostUSD: 10, CostSource: brain.CostMeasured}, nil
		}, toolio.CategoryBudget, 1},
		"the turns are spent": {func(context.Context, brain.Brief) (brain.Outcome, error) {
			return brain.Outcome{StopReason: brain.StopTurns}, nil
		}, toolio.CategoryBudget, 1},
		"the credential is refused": {func(context.Context, brain.Brief) (brain.Outcome, error) {
			return brain.Outcome{}, &toolio.Failure{Category: toolio.CategoryAuth, Message: "no key"}
		}, toolio.CategoryAuth, 1},
		"the brain ends with no report": {func(context.Context, brain.Brief) (brain.Outcome, error) {
			return brain.Outcome{StopReason: brain.StopEnded}, nil
		}, toolio.CategoryNoResult, 1},
	} {
		t.Run(name, func(t *testing.T) {
			w := newLoopWorld(t)

			res, err := w.loop(t, context.Background(), 10, 150, tc.round)

			if err != nil {
				t.Fatal(err)
			}
			if len(w.spy.briefs) != tc.runs || res.End != EndExhausted || res.LastFailure == nil || failure(t, res.LastFailure).Category != tc.category {
				t.Errorf("the brain ran %d times (want %d), result %+v, want exhausted with the failure of category %s", len(w.spy.briefs), tc.runs, res, tc.category)
			}
		})
	}
}

// TestABoxThatCannotBeSyncedEndsTheRunWithItsFailure guards the way back to the measured
// tree: a box that cannot be brought to it is not run in, and the failure is the run's.
func TestABoxThatCannotBeSyncedEndsTheRunWithItsFailure(t *testing.T) {
	w := newLoopWorld(t)
	var bx *box.Session
	closing := func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		bx = b.Box
		return round(t, broken, loopReportPayload, 0, 1)(ctx, b)
	}
	w.afterGate = func() { _ = bx.Close() } // the box is gone once the change is measured and refused

	res, err := w.loop(t, context.Background(), 10, 150, closing)

	if err != nil {
		t.Fatal(err)
	}
	if len(w.spy.briefs) != 1 || res.End != EndExhausted || res.LastFailure == nil || failure(t, res.LastFailure).Category != toolio.CategorySandbox || res.Rounds != 1 {
		t.Errorf("the brain ran %d times, result %+v, want one round and the failure of the sandbox", len(w.spy.briefs), res)
	}
}

// TestTheBrainIsNeverGivenLessThanASecond guards the timeout of a round: a deadline that
// leaves nothing for the brain still gives it a second, since a timeout of nothing would be no
// timeout at all.
func TestTheBrainIsNeverGivenLessThanASecond(t *testing.T) {
	w := newLoopWorld(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := w.loop(t, ctx, 10, 150, round(t, fixed, loopReportPayload, 0, 1))

	if err != nil {
		t.Fatal(err)
	}
	if got := w.spy.briefs[0].Timeout; got != time.Second {
		t.Errorf("timeout = %v, want a second", got)
	}
}

// TestThereIsNoRoundAfterTheTimeForTheBrainIsOut guards 09-REQ-7.2 against a real deadline:
// with a command whose deadline leaves the brain less than a minute after the reserve, a
// refusal ends the run, whatever cost is left.
func TestThereIsNoRoundAfterTheTimeForTheBrainIsOut(t *testing.T) {
	w := newLoopWorld(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	res, err := w.loop(t, ctx, 10, 150, round(t, broken, loopReportPayload, 1, 1), round(t, fixed, loopReportPayload, 1, 1))

	if err != nil {
		t.Fatal(err)
	}
	if len(w.spy.briefs) != 1 || res.End != EndExhausted || !res.HoldsChange || res.LastRefusal == nil {
		t.Errorf("the brain ran %d times, result %+v, want one round, whatever cost remained: the deadline leaves no time to measure another", len(w.spy.briefs), res)
	}
	if b := w.spy.briefs[0]; b.Timeout <= 0 || b.Timeout > 90*time.Second-MeasureReserve(0) {
		t.Errorf("the first round got a timeout of %v, want the time to the deadline less the reserve", b.Timeout)
	}
}

// TestAStoppedRunReturnsTheStopWithWhatWasDone guards the end of a run that is cancelled in a
// round: the loop returns the context error and what it knows, for the caller to keep the work.
func TestAStoppedRunReturnsTheStopWithWhatWasDone(t *testing.T) {
	w := newLoopWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopping := func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		out, err := round(t, broken, loopReportPayload, 0, 1)(ctx, b)
		cancel() // the run is stopped as the brain ends, before its tree is applied
		return out, err
	}

	res, err := w.loop(t, ctx, 10, 150, round(t, broken, loopReportPayload, 0, 1), stopping)

	if err == nil || ctx.Err() == nil {
		t.Fatalf("error = %v, want the cancellation", err)
	}
	if res.Rounds != 2 || res.End == EndLandable {
		t.Errorf("result = %+v", res)
	}
}

// TestACommitMadeOutsideAfStopsTheLoopWithTheChangeInTheTree guards the errors of measuring: a
// failure to measure is not a round that failed, it ends the loop and leaves the work where it is.
func TestACommitMadeOutsideAfStopsTheLoopWithTheChangeInTheTree(t *testing.T) {
	w := newLoopWorld(t)
	foreign := func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		w.edit(t, "docs/other.md", "someone's\n")
		gitDo(t, w.repo.Dir, "add", "docs/other.md")
		gitDo(t, w.repo.Dir, "commit", "-q", "-m", "chore: made by hand", "docs/other.md")
		return round(t, map[string]string{"docs/readme.md": "readme, changed\n"}, loopReportPayload, 0, 1)(ctx, b)
	}

	res, err := w.loop(t, context.Background(), 10, 150, foreign, round(t, fixed, loopReportPayload, 0, 1))

	if f := failure(t, err); f.Category != toolio.CategoryGit || !strings.Contains(f.Message, "made by hand") {
		t.Errorf("error = %v, want git naming the commit", err)
	}
	if len(w.spy.briefs) != 1 || res.Rounds != 1 {
		t.Errorf("the brain ran %d times, result %+v: no second round after a failure to measure", len(w.spy.briefs), res)
	}
	if data, _ := os.ReadFile(filepath.Join(w.repo.Dir, "docs", "readme.md")); string(data) != "readme, changed\n" {
		t.Errorf("readme.md = %q: the applied change must stay in the tree", data)
	}
}

// guardsAreTheDefaults is here so that the protected paths of a round are checked once: the
// default list is what the hook gets.
func TestTheHookOfEveryRoundHasTheDefaultProtectedPaths(t *testing.T) {
	w := newLoopWorld(t)

	_, err := w.loop(t, context.Background(), 10, 150, round(t, fixed, loopReportPayload, 0, 1))

	if err != nil {
		t.Fatal(err)
	}
	if got := w.spy.briefs[0].Hook.ProtectedPaths; !slices.Equal(got[:len(policy.DefaultProtectedPaths())], policy.DefaultProtectedPaths()) || fmt.Sprint(got) == "" {
		t.Errorf("protected paths = %v", got)
	}
}

// TestThereIsNoRoundForARunThatIsAlreadyStopped guards the start of a loop: a context that is
// cancelled gets no round.
func TestThereIsNoRoundForARunThatIsAlreadyStopped(t *testing.T) {
	w := newLoopWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := w.loop(t, ctx, 10, 150, round(t, fixed, loopReportPayload, 0, 1))

	if err == nil || len(w.spy.briefs) != 0 || res.Rounds != 0 {
		t.Errorf("error %v, the brain ran %d times, result %+v", err, len(w.spy.briefs), res)
	}
}

// TestARunStoppedBetweenRoundsIsTheStopAndNotAnExhaustedRun guards the way a cancellation is
// told when it comes while the box is being synced: the run does not end with a failure of the
// sandbox, the stop is returned.
func TestARunStoppedBetweenRoundsIsTheStopAndNotAnExhaustedRun(t *testing.T) {
	w := newLoopWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.afterGate = cancel // the run is stopped as the first change has been measured and refused

	res, err := w.loop(t, ctx, 10, 150, round(t, broken, loopReportPayload, 1, 1), round(t, fixed, loopReportPayload, 1, 1))

	if err == nil || ctx.Err() == nil {
		t.Fatalf("error = %v, want the stop", err)
	}
	if len(w.spy.briefs) != 1 || res.LastRefusal == nil {
		t.Errorf("the brain ran %d times, result %+v", len(w.spy.briefs), res)
	}
}
