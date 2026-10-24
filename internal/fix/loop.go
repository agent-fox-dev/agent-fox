package fix

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// Unbounded stands for the time left of a command that has no deadline.
const Unbounded time.Duration = math.MinInt64

// minBrainTime is the least brain time a round is started with after the first: less than
// this, and the measuring that has to follow would not fit the deadline.
const minBrainTime = time.Minute

// MeasureReserve is the time kept for measuring the change that a round leaves: the gate
// is run on it, and then the revert check, so three times what the baseline took, and a
// minute more.
func MeasureReserve(baseline time.Duration) time.Duration { return 3*baseline + time.Minute }

// BrainTime is the time a round may give the brain: what is left until the deadline of the
// command, less the reserve for measuring. A command with no deadline gives Unbounded.
func BrainTime(left, baseline time.Duration) time.Duration {
	if left == Unbounded {
		return Unbounded
	}
	return left - MeasureReserve(baseline)
}

// Continue says whether another round of an attempt may start: cost remains, a turn
// remains, and the brain has a minute of time.
func Continue(costLeft float64, turnsLeft int, left, baseline time.Duration) bool {
	if costLeft <= 0 || turnsLeft < 1 {
		return false
	}
	return left == Unbounded || BrainTime(left, baseline) >= minBrainTime
}

// timeLeft is the time until the deadline of the command, or Unbounded.
func timeLeft(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		return time.Until(deadline)
	}
	return Unbounded
}

// The ends of a loop.
const (
	EndLandable  = "landable"
	EndQuestion  = "question"
	EndExhausted = "exhausted"
)

// LoopParams is what the rounds of a run are made of.
type LoopParams struct {
	Dir     string
	Git     *gitx.Git
	Session *box.Session
	// Name and Brain are the brain, which every round runs; Tally is what the rounds cost.
	Name  string
	Brain brain.Brain
	Tally *brain.Tally
	// RunDir is the run's directory, Report gets the events of the brain, and Reporter the
	// warnings of the loop.
	RunDir   *toolio.RunDir
	Report   brain.Report
	Reporter Reporter
	// State is fix.json, which the loop keeps: the rounds and what they cost are written
	// after each one. Prefix is the part of the brief that every round shares.
	State  *State
	Prefix string
	// Attempts, Budget and MaxTurns are the bounds: up to Attempts attempts, each with the
	// budget in dollars and the turns.
	Attempts int
	Budget   float64
	MaxTurns int
	// Earlier is the question each answer of the run answers, when the caller knows them;
	// nil is to search the run directories for them.
	Earlier []Earlier
	// Gate and Conform are the seams of Measure.
	Gate    func(ctx context.Context, dir string, o checks.Options) (checks.Result, error)
	Conform func(ctx context.Context, dir, since string, o conform.Options) (conform.Result, error)
}

// LoopResult is what the rounds came to.
type LoopResult struct {
	// End is landable, question, or exhausted: no attempt is left, and nothing may land.
	End string
	// Measured is the measurement that lands, for a landable end, and the last that refused
	// the change otherwise; Report is the last report that was accepted.
	Measured Measurement
	Report   *Report
	Question *Question
	// HoldsChange says the tree of the checkout holds a change that a round applied.
	HoldsChange bool
	// LastRefusal is the last measurement that refused the change, when one did.
	LastRefusal *Measurement
	// LastFailure is why the last round did not come to a report, when it did not.
	LastFailure error
	// Attempts and Rounds are how many were made.
	Attempts, Rounds int
}

// RunLoop runs the rounds of a run: a brain in a box that is held across them, the tree it
// leaves applied to the checkout and measured, and a refusal given back to it as the task of
// the next round, within the bounds of the attempt, and then the next attempt.
//
// A round that comes to a question ends the run, and the tree is not applied. A round that
// comes to a report has its tree applied and measured: a change that may land ends the loop,
// and one that is refused is the task of the next round while the attempt has cost, turns
// and time for it. An attempt ends when its bounds are spent, or when a round ends without a
// report; the next one starts from the tree as it is, with the whole budget and a brain that
// is told why the last one ended, unless the failure cannot be retried. The loop does not
// land and does not park: when it returns, the caller does, with the tree as it is. An
// error is a run that was stopped, or a measuring that could not be made, and the tree is
// where it was left.
func RunLoop(ctx context.Context, p LoopParams) (LoopResult, error) {
	st := p.State
	res := LoopResult{}
	earlier := p.Earlier
	if earlier == nil {
		earlier = FindEarlier(p.Dir, st.Input.Key, st.Context, st.RunID)
	}
	baseline := time.Duration(st.BaselineMS) * time.Millisecond
	placed := &Placed{}
	end := func(kind string, err error) (LoopResult, error) {
		res.End = kind
		res.HoldsChange = p.holdsChange(ctx)
		placed.Remove(p.Dir) // under none, the files af made for the rounds are not left as the person's
		return res, err
	}

	ran := 0
	var refusal *Refusal // what the last measured round was refused for
	previous := ""       // why the last attempt ended
	for attempt := 1; attempt <= max(1, p.Attempts); attempt++ {
		if err := ctx.Err(); err != nil {
			return end(EndExhausted, err)
		}
		if attempt > 1 && (cannotRetry(res.LastFailure) || !Continue(p.Budget, p.MaxTurns, timeLeft(ctx), baseline)) {
			break
		}
		res.Attempts = attempt
		start := p.Tally.Usage(p.Name)
		for inAttempt := 1; ; inAttempt++ {
			now := p.Tally.Usage(p.Name)
			costLeft, turnsLeft := p.Budget-(now.CostUSD-start.CostUSD), p.MaxTurns-(now.Turns-start.Turns)
			if inAttempt > 1 && !Continue(costLeft, turnsLeft, timeLeft(ctx), baseline) {
				previous = fmt.Sprintf("its bounds were spent: the attempt had $%.2f, %d turns and %s of time for the brain left", max(costLeft, 0), max(turnsLeft, 0), brainTimeWords(ctx, baseline))
				break
			}
			if ran > 0 { // the box holds the brain's last tree; it is to hold the one that was measured
				if err := p.Session.Sync(ctx); err != nil {
					res.LastFailure = err
					previous = "the box could not be brought back to the tree that was measured: " + err.Error()
					break
				}
			}
			ran++
			round := st.Rounds + 1
			res.Rounds++
			task := TaskBlock(TaskParams{RunID: st.RunID, Attempt: attempt, Round: inAttempt, Earlier: earlier, Refusal: refusal, PreviousAttempt: previous})
			rr, err := RunRound(ctx, RoundParams{
				Dir: p.Dir, Git: p.Git, Session: p.Session, Name: p.Name, Brain: p.Brain, Tally: p.Tally, RunDir: p.RunDir, Report: p.Report,
				State: *st, Prefix: p.Prefix, Task: task, Round: round, MaxCostUSD: costLeft, MaxTurns: turnsLeft, Timeout: brainTimeout(ctx, baseline),
				Placed: placed,
			})
			after := p.Tally.Usage(p.Name)
			st.Rounds = round
			st.AddUsage(toolio.Usage{Brain: p.Name, CostUSD: after.CostUSD - now.CostUSD, CostSource: after.CostSource})
			if werr := st.Write(p.RunDir.Path); werr != nil && err == nil {
				return end(EndExhausted, &toolio.Failure{Category: toolio.CategoryIO, Message: werr.Error()})
			}
			if err != nil {
				var moved *MovedError
				if errors.As(err, &moved) {
					return end(EndExhausted, err) // the checkout is not where the run began: it stops, and nothing is landed or parked, also when the run was stopped as well
				}
				if ctx.Err() != nil {
					return end(EndExhausted, ctx.Err())
				}
				res.LastFailure = err
				previous = "a round ended without a fix report: " + err.Error()
				break
			}
			res.LastFailure = nil
			previous = ""
			if q := rr.Submission.Question; q != nil {
				res.Question = q
				return end(EndQuestion, nil)
			}
			res.Report = rr.Submission.Report

			measured, err := Measure(ctx, MeasureParams{Dir: p.Dir, Git: p.Git, State: *st, Report: res.Report, Warn: p.Reporter.Warn, Gate: p.Gate, Conform: p.Conform})
			if err != nil {
				if ctx.Err() != nil { // a measuring that the stop kept from reading git is the stop
					return end(EndExhausted, ctx.Err())
				}
				return end(EndExhausted, err)
			}
			RecordMeasurement(p.record, measured)
			res.Measured = measured
			if measured.Lands {
				return end(EndLandable, nil)
			}
			res.LastRefusal, refusal = &measured, measured.Refusal
		}
	}
	return end(EndExhausted, nil)
}

// cannotRetry says a failure is one that another attempt cannot mend: a failure af marked
// as not retryable, or an error that is no failure af knows. No failure is nothing to retry.
func cannotRetry(err error) bool {
	if err == nil {
		return false
	}
	var f *toolio.Failure
	return !errors.As(err, &f) || !f.Retryable
}

// brainTimeout is the time the brain of a round is given: what the deadline leaves less the
// reserve, but never less than a second, since a timeout of nothing would be no timeout.
func brainTimeout(ctx context.Context, baseline time.Duration) time.Duration {
	left := BrainTime(timeLeft(ctx), baseline)
	if left == Unbounded {
		return 0
	}
	return max(left, time.Second)
}

// brainTimeWords is the time for the brain that is left, as a person reads it.
func brainTimeWords(ctx context.Context, baseline time.Duration) string {
	left := BrainTime(timeLeft(ctx), baseline)
	if left == Unbounded {
		return "no limit"
	}
	return left.Round(time.Second).String()
}

// holdsChange says whether the tree of the checkout has a change in it, on a context that a
// cancellation does not reach.
func (p LoopParams) holdsChange(ctx context.Context) bool {
	dirty, err := p.Git.DirtyFiles(context.WithoutCancel(ctx))
	return err == nil && len(dirty) > 0
}

// record notes an entry in the ledger of the run; a ledger that cannot be written is a
// warning.
func (p LoopParams) record(kind string, data any) {
	if err := p.RunDir.Record(kind, data); err != nil {
		p.Reporter.Warn("ledger_unwritable", fmt.Sprintf("the ledger entry %s could not be written: %v", kind, err))
	}
}
