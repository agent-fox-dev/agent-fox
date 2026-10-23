package worker

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/worker/recovery"
)

// pricedRun is what a run of the priced brain says it cost.
type pricedRun struct {
	cost       float64
	in, out    int
	turns      int
	costSource string
}

// pricing is what the priced brain charges for the round it runs, and the runs it made.
var pricing struct {
	mu    sync.Mutex
	round map[int]pricedRun
	ran   []pricedRun
}

func init() {
	// The scripted brain of the tests of this package reports a cost for the rounds that a test priced: the
	// flag --script goes only with --brain scripted, so it is the scripted brain that is wrapped.
	scripted, err := brain.Default.Lookup(brain.ScriptedName)
	if err != nil {
		panic(err)
	}
	wrapped := scripted
	wrapped.New = func(o brain.Options) (brain.Brain, error) {
		inner, err := scripted.New(o)
		return pricedBrain{inner}, err
	}
	brain.Default.Register(wrapped)
}

// pricedBrain is the scripted brain that reports a cost for the round it runs, when a test set one.
type pricedBrain struct{ inner brain.Brain }

func (p pricedBrain) Run(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
	out, err := p.inner.Run(ctx, b)
	pricing.mu.Lock()
	defer pricing.mu.Unlock()
	if run, ok := pricing.round[b.Attempt]; ok {
		out.CostUSD, out.InputTokens, out.OutputTokens, out.CostSource = run.cost, run.in, run.out, run.costSource
		if run.turns > 0 {
			out.Turns = run.turns
		}
		pricing.ran = append(pricing.ran, run)
	}
	return out, err
}

// usePricing sets what the priced brain charges for each round, and makes the loop of the environment run it
// on the script.
func (e *env) usePricing(script string, rounds map[int]pricedRun) {
	e.t.Helper()
	pricing.mu.Lock()
	pricing.round, pricing.ran = rounds, nil
	pricing.mu.Unlock()
	e.t.Cleanup(func() {
		pricing.mu.Lock()
		pricing.round, pricing.ran = nil, nil
		pricing.mu.Unlock()
	})
	e.runner.loopFlags = func(*attempt) []string { return []string{"--brain", "scripted", "--script", script} }
}

// recordBrainRun appends to the ledger of the run of the attempt the records of a brain run that cost what the
// run says: the beginning, and the end unless it is cut short.
func (e *env) recordBrainRun(t *testing.T, round int, run pricedRun, cutShort bool) (startSeq int) {
	t.Helper()
	rd, err := toolio.OpenRunDir(filepath.Join(e.dir, cloneDirName), e.hub.Sessions()[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := rd.Record("brain_start", map[string]any{"brain": "scripted", "attempt": round}); err != nil {
		t.Fatal(err)
	}
	entries, _ := rd.Ledger()
	startSeq = entries[len(entries)-1].Seq
	if cutShort {
		return startSeq
	}
	if err := rd.Record("brain_end", map[string]any{
		"brain": "scripted", "stop_reason": "ended", "submitted_claim": false, "cost_usd": run.cost, "cost_source": run.costSource,
		"turns": run.turns, "duration_ms": 10, "input_tokens": run.in, "output_tokens": run.out,
	}); err != nil {
		t.Fatal(err)
	}
	return startSeq
}

func near(got, want float64) bool { return math.Abs(got-want) < 0.0001 }

// TestABrainRunOnAResumedDeliveryGetsThePinnedBudgetAndTurnsLessWhatTheAttemptSpent_TS_14_22
// verifies TS-14-22.
// Verifies: 14-REQ-4.1
func TestABrainRunOnAResumedDeliveryGetsThePinnedBudgetAndTurnsLessWhatTheAttemptSpent_TS_14_22(t *testing.T) {
	e := loopEnv(t, fixFiles, map[string]string{"AF_MAX_BUDGET_USD": "1.00", "AF_MAX_TURNS": "20"}, nil, fixSteps()...)
	e.stopAfter("briefed")
	e.recordBrainRun(t, 1, pricedRun{cost: 0.30, turns: 5, costSource: "reported"}, false)
	seen := e.settingsSeen()

	res, err := e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if !near(seen.Budget, 0.70) || seen.MaxTurns != 15 {
		t.Errorf("the brain was given $%.2f and %d turns, want the pinned $1.00 and 20 less the $0.30 and 5 that were spent: 0.70 and 15", seen.Budget, seen.MaxTurns)
	}
}

// TestWithNoBudgetOrNoTurnsLeftTheAttemptEndsFailedWithCategoryBudgetAndTheBrainDoesNotRun_TS_14_23
// verifies TS-14-23.
// Verifies: 14-REQ-4.2
func TestWithNoBudgetOrNoTurnsLeftTheAttemptEndsFailedWithCategoryBudgetAndTheBrainDoesNotRun_TS_14_23(t *testing.T) {
	for name, spent := range map[string]pricedRun{
		"the whole budget": {cost: 1.00, turns: 3, costSource: "reported"},
		"all the turns":    {cost: 0.10, turns: 20, costSource: "reported"},
	} {
		t.Run(name, func(t *testing.T) {
			e := loopEnv(t, fixFiles, map[string]string{"AF_MAX_BUDGET_USD": "1.00", "AF_MAX_TURNS": "20"}, nil, fixSteps()...)
			e.stopAfter("briefed")
			e.recordBrainRun(t, 1, spent, false)
			runs, leases := e.brainRuns(), len(e.hub.Leases())

			res, err := e.deliver(2)

			if err != nil {
				t.Fatal(err)
			}
			category, _, retryable, hint := errorOf(envelope(t, res))
			if envelope(t, res)["status"] != "failed" || category != "budget" || retryable {
				t.Errorf("envelope %s, want a failed one of category budget that is not retryable", res.Envelope)
			}
			if hint == nil {
				t.Error("the failure carries no fix hint: a person has to know which variable to raise")
			}
			if got := e.brainRuns() - runs; got != 0 {
				t.Errorf("the brain was started %d times, want never", got)
			}
			if got := len(e.hub.Leases()) - leases; got != 0 {
				t.Errorf("%d leases were taken for a delivery that does not run the brain", got)
			}
			// What the attempt spent is its usage, in the envelope and in the one record of the session, though no brain ran.
			usage, _ := envelope(t, res)["usage"].(map[string]any)
			session := e.hub.Sessions()[0]
			if !near(usage["cost_usd"].(float64), spent.cost) || usage["turns"] != float64(spent.turns) || usage["brain"] != journalOf(t, e).Settings.Brain {
				t.Errorf("the envelope's usage is %v, want the $%.2f and %d turns that the attempt spent", usage, spent.cost, spent.turns)
			}
			if len(session.Usage) != 1 || !near(session.Usage[0].CostUSD, spent.cost) || session.Usage[0].Turns != spent.turns {
				t.Errorf("the usage records of the session are %+v, want one that holds what the attempt spent", session.Usage)
			}
		})
	}
}

// TestABrainRunCutShortByTheDeathOfTheProcessCountsAsZeroAndRaisesBrainSpendUnrecorded_TS_14_24
// verifies TS-14-24.
// Verifies: 14-REQ-4.3
func TestABrainRunCutShortByTheDeathOfTheProcessCountsAsZeroAndRaisesBrainSpendUnrecorded_TS_14_24(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.stopAfter("briefed")
	cut := e.recordBrainRun(t, 1, pricedRun{}, true)
	e.recordBrainRun(t, 2, pricedRun{cost: 0.20, turns: 3, costSource: "reported"}, false)

	res, err := e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	w := warningOf(envelope(t, res), "brain_spend_unrecorded")
	if w == nil {
		t.Fatalf("the warnings %v hold no brain_spend_unrecorded", codes(envelope(t, res)))
	}
	message := w["message"].(string)
	seq := regexp.MustCompile(`ledger entry (\d+)`).FindStringSubmatch(message)
	round := regexp.MustCompile(`round (\d+)`).FindStringSubmatch(message)
	if seq == nil || round == nil || seq[1] != strconv.Itoa(cut) || round[1] != "1" {
		t.Errorf("the warning says %q, want the ledger entry %d and the round 1", message, cut)
	}
	if usage, _ := envelope(t, res)["usage"].(map[string]any); !near(usage["cost_usd"].(float64), 0.20) {
		t.Errorf("usage = %v, want only the 0.20 that was recorded", usage)
	}
	if n := countWarnings(envelope(t, res), "brain_spend_unrecorded"); n != 1 {
		t.Errorf("%d warnings brain_spend_unrecorded, want one for the one run that was cut short", n)
	}
}

// countWarnings is how many warnings of the envelope have the code.
func countWarnings(env map[string]any, code string) int {
	n := 0
	for _, c := range codes(env) {
		if c == code {
			n++
		}
	}
	return n
}

// TestTheSpendOfTheJournalIsRefreshedAfterEveryBrainRunAndIsTheCarriedSpendPlusTheLedger_TS_14_25
// verifies TS-14-25.
// Verifies: 14-REQ-4.4
func TestTheSpendOfTheJournalIsRefreshedAfterEveryBrainRunAndIsTheCarriedSpendPlusTheLedger_TS_14_25(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.stopAfter("briefed")
	editJournal(t, e, func(j *recovery.Journal) {
		j.Carried = recovery.Spend{CostUSD: 0.10, CostSource: "reported", InputTokens: 100, OutputTokens: 10, Turns: 1}
		j.Spend = j.Carried
	})
	e.usePricing(scriptRounds(t, refusedRound(), fixSteps()), map[int]pricedRun{
		1: {cost: 0.20, in: 1000, out: 200, turns: 4, costSource: "measured"},
		2: {cost: 0.15, in: 500, out: 100, turns: 3, costSource: "reported"},
	})
	var spend recovery.Spend
	var rounds int
	e.atRelease(func() {
		spend = journalOf(t, e).Spend
		rounds = e.runStateOf(t).Rounds
	})

	res, err := e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	want := recovery.Spend{CostUSD: 0.45, CostSource: "measured", InputTokens: 1600, OutputTokens: 310, Turns: 8}
	if !near(spend.CostUSD, want.CostUSD) || spend.CostSource != want.CostSource || spend.InputTokens != want.InputTokens || spend.OutputTokens != want.OutputTokens || spend.Turns != want.Turns {
		t.Errorf("the journal records the spend %+v, want %+v: the carried 0.10 and the two runs", spend, want)
	}
	if rounds != 2 {
		t.Errorf("the run has %d rounds, want 2", rounds)
	}
}

// TestTheUsageOfAnAttemptIsTheCarriedSpendPlusTheRunsOfTheLedgerWhateverTheDeliveries_TS_14_28 verifies
// TS-14-28, a property: for deliveries that are stopped at generated stages and then finished, the usage
// the envelope reports and the usage record hub holds are the cost of every brain run that was made in all
// the deliveries, with tokens and turns.
// Verifies: 14-REQ-4.7
func TestTheUsageOfAnAttemptIsTheCarriedSpendPlusTheRunsOfTheLedgerWhateverTheDeliveries_TS_14_28(t *testing.T) {
	rng := rand.New(rand.NewSource(14028))
	for i := range 6 {
		// The first delivery can be stopped before the brief, or after the submission or the apply of its
		// tree; a later one runs the brain again, which passes the last two stages, unless the tree was
		// applied: the delivery after that skips the brain and finishes.
		stops := []string{"briefed", "submission_accepted", "applied"}
		var sequence []string
		for range rng.Intn(3) + 1 {
			stop := stops[rng.Intn(len(stops))]
			sequence = append(sequence, stop)
			stops = []string{"submission_accepted", "applied"}
			if stop == "applied" {
				break
			}
		}
		cost, in, out := float64(rng.Intn(40)+5)/100, rng.Intn(900)+100, rng.Intn(300)+10
		t.Run(fmt.Sprintf("%d: %v", i, sequence), func(t *testing.T) {
			e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
			e.usePricing(scriptFile(t, fixSteps()...), map[int]pricedRun{1: {cost: cost, in: in, out: out, turns: 7, costSource: "reported"}})
			for n, stop := range sequence {
				e.runner.abortAfter = stop
				_, err := e.runner.Run(context.Background(), e.request(jobs.KindFix, issueInput("42"), func(r *jobs.Request) { r.Delivery = n + 1 }))
				e.runner.abortAfter = ""
				if _, stopped := err.(stageAbort); !stopped {
					t.Fatalf("delivery %d returned %v, want it stopped after %s", n+1, err, stop)
				}
			}

			res, err := e.deliver(len(sequence) + 1)

			if err != nil || envelope(t, res)["status"] != "done" {
				t.Fatalf("result %s, error %v", res.Envelope, err)
			}
			pricing.mu.Lock()
			ran := append([]pricedRun(nil), pricing.ran...)
			pricing.mu.Unlock()
			var want recovery.Spend
			for _, r := range ran {
				want = addSpend(want, recovery.Spend{CostUSD: r.cost, InputTokens: int64(r.in), OutputTokens: int64(r.out), Turns: r.turns})
			}
			usage, _ := envelope(t, res)["usage"].(map[string]any)
			if !near(usage["cost_usd"].(float64), want.CostUSD) || int64(usage["input_tokens"].(float64)) != want.InputTokens || int64(usage["output_tokens"].(float64)) != want.OutputTokens || int(usage["turns"].(float64)) != want.Turns {
				t.Errorf("usage = %v after %d brain runs, want cost %.2f, %d input tokens, %d output tokens and %d turns", usage, len(ran), want.CostUSD, want.InputTokens, want.OutputTokens, want.Turns)
			}
			sessions := e.hub.Sessions()
			if len(sessions) != 1 || len(sessions[0].Usage) != 1 || !near(sessions[0].Usage[0].CostUSD, want.CostUSD) || sessions[0].Usage[0].InputTokens != want.InputTokens {
				t.Errorf("sessions = %+v, want one usage record with the total", sessions)
			}
		})
	}
}

// TestAnAttemptThatSpentItsBudgetStillLandsAChangeThatIsApplied pins that the budget is for the brain: a
// delivery that skips it has nothing to refuse.
// Verifies: 14-REQ-4.2
func TestAnAttemptThatSpentItsBudgetStillLandsAChangeThatIsApplied(t *testing.T) {
	e := loopEnv(t, fixFiles, map[string]string{"AF_MAX_BUDGET_USD": "1.00", "AF_MAX_TURNS": "20"}, nil, fixSteps()...)
	e.stopAfter("applied")
	e.recordBrainRun(t, 1, pricedRun{cost: 1.00, turns: 20, costSource: "reported"}, false)
	runs := e.brainRuns()

	res, err := e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v, want the applied change landed", res.Envelope, err)
	}
	if got := e.brainRuns() - runs; got != 0 {
		t.Errorf("the brain was started %d times, want never", got)
	}
}

// TestABrainRunThatIsBegunAtTheEndOfTheLedgerIsCutShortToo pins the last run of a ledger, which no later run
// of the brain comes after.
// Verifies: 14-REQ-4.3
func TestABrainRunThatIsBegunAtTheEndOfTheLedgerIsCutShortToo(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.stopAfter("briefed")
	e.recordBrainRun(t, 1, pricedRun{cost: 0.20, turns: 3, costSource: "reported"}, false)
	cut := e.recordBrainRun(t, 2, pricedRun{}, true)

	res, err := e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	w := warningOf(envelope(t, res), "brain_spend_unrecorded")
	if w == nil || !regexp.MustCompile(fmt.Sprintf(`ledger entry %d\b`, cut)).MatchString(w["message"].(string)) {
		t.Errorf("the warnings %v, want brain_spend_unrecorded for the ledger entry %d", codes(envelope(t, res)), cut)
	}
}

// TestADiscardedResumeThatSpentTheBudgetEndsWithoutACloneOrABrain pins the same rule for a delivery that
// begins from a fresh clone: the spend that outlives the clone is the attempt's.
// Verifies: 14-REQ-4.2, 14-REQ-4.5
func TestADiscardedResumeThatSpentTheBudgetEndsWithoutACloneOrABrain(t *testing.T) {
	e := loopEnv(t, fixFiles, map[string]string{"AF_MAX_BUDGET_USD": "1.00"}, nil, fixSteps()...)
	spy := e.spyOnGit()
	e.stopAfter("briefed")
	editJournal(t, e, func(j *recovery.Journal) { j.Spend = recovery.Spend{CostUSD: 1.00, CostSource: "reported", Turns: 3} })
	f, err := os.OpenFile(filepath.Join(e.runDirOf(), "ledger.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("torn\n")
	_ = f.Close()
	clones := clonesOf(spy)

	res, err := e.deliver(2)

	if err != nil {
		t.Fatal(err)
	}
	if category, _, retryable, _ := errorOf(envelope(t, res)); envelope(t, res)["status"] != "failed" || category != "budget" || retryable {
		t.Errorf("envelope %s, want a failed one of category budget", res.Envelope)
	}
	if got := clonesOf(spy) - clones; got != 0 {
		t.Errorf("%d clones were made for an attempt that cannot run the brain", got)
	}
	if w := warningOf(envelope(t, res), "resume_discarded"); w == nil {
		t.Errorf("the warnings %v, want the discard to be said", codes(envelope(t, res)))
	}
}
