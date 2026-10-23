package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// maxRound bounds the attempts of a round, whatever the runner says.
const (
	defaultRound = 2
	maxRound     = 10
)

// autoBy is who is named as the author of an automatic attempt.
const autoBy = "agent-fox"

// panicInfo is a panic of a runner, with the stack at the moment of the panic.
type panicInfo struct {
	value any
	stack []byte
}

// called is what a call of the runner came to.
type called struct {
	res      Result
	err      error
	panicked *panicInfo
}

// callRunner calls the runner and keeps a panic of it from stopping the worker.
func (s *Service) callRunner(ctx context.Context, req Request) (out called) {
	defer func() {
		if v := recover(); v != nil {
			out = called{panicked: &panicInfo{value: v, stack: debug.Stack()}}
		}
	}()
	out.res, out.err = s.runner.Run(ctx, req)
	return out
}

// verdict is what an attempt came to, and what becomes of the job.
type verdict struct {
	attempt AttemptState
	// job is the state the job goes to: done, blocked, failed, or queued when an
	// automatic attempt is made.
	job State
	// auto is true when an attempt n+1 is made.
	auto bool
}

// finish records what a call of the runner came to: it settles the envelope, applies
// the first of the rules that matches, and stores the files and the rows in one go.
// spent says that the attempt ends because its transient failures are used up, which
// is never a reason for an automatic attempt; cause is why the attempt's context was
// cancelled, if it was.
func (s *Service) finish(ctx context.Context, req Request, out called, spent bool, cause error) error {
	env, raw := s.envelopeOf(req, out, cause)
	attempts, err := loadAttempts(ctx, s.store.db, req.JobID)
	if err != nil {
		return err
	}
	max := roundLimit(out.res.MaxAttempts)
	v := ruling(env, attempts, req.Attempt, max, spent)

	end := endArgs{
		jobID: req.JobID, attempt: req.Attempt, verdict: v, env: env, maxAttempts: max,
		hasReport: out.res.Report != "", labelsSet: out.res.LabelsSet, labelsRemoved: out.res.LabelsRemoved,
	}
	files := func() error {
		if err := writePrivate(filepath.Join(req.Dir, "envelope.json"), raw); err != nil {
			return err
		}
		if end.hasReport {
			return writePrivate(filepath.Join(req.Dir, "report.md"), []byte(out.res.Report))
		}
		return nil
	}
	ended, next, cleanup, err := s.store.EndAttempt(ctx, end, files)
	if err != nil {
		return err
	}
	if cleanup {
		// The runner has returned: its run was forgotten before the attempt was finished.
		s.startTry(req.JobID)
	}
	if ended && v.auto {
		if err := s.enqueue(context.WithoutCancel(ctx), req.JobID, next); err != nil {
			s.log.Warn("the job is stored but its attempt is not in the queue; the sweep will queue it",
				"job", req.JobID, "attempt", next, "error", err.Error())
		}
	}
	return nil
}

// envelopeOf is the envelope of the attempt, and the bytes it is stored as: what the
// runner gave when that can be used, else an envelope of the layer's making.
func (s *Service) envelopeOf(req Request, out called, cause error) (toolio.Envelope, []byte) {
	synth := func(failure toolio.ErrorInfo) (toolio.Envelope, []byte) {
		env := synthesize(req.Kind, s.clock.Now(), failure)
		raw, err := encodeEnvelope(env)
		if err != nil { // an envelope of strings and numbers
			panic(fmt.Sprintf("jobs: the synthesized envelope cannot be encoded: %v", err))
		}
		return env, raw
	}
	switch {
	case out.panicked != nil:
		s.log.Error("the runner panicked", "job", req.JobID, "attempt", req.Attempt,
			"panic", fmt.Sprint(out.panicked.value), "stack", string(out.panicked.stack))
		return synth(toolio.ErrorInfo{Category: toolio.CategoryInternal, Message: "the runner panicked"})
	case out.err != nil && IsCancel(cause):
		// It returned after a cancel without an envelope.
		return synth(toolio.ErrorInfo{Category: toolio.CategoryCancelled, Message: "the job was cancelled"})
	case out.err != nil:
		if failure, ok := errors.AsType[*toolio.Failure](out.err); ok {
			return synth(toolio.ErrorInfo{Category: failure.Category, Message: failure.Message, Retryable: failure.Retryable, FixHint: failure.FixHint})
		}
		return synth(toolio.ErrorInfo{Category: toolio.CategoryInternal, Message: out.err.Error()})
	}
	env, problem := parseEnvelope(out.res.Envelope)
	if problem != "" && IsCancel(cause) {
		return synth(toolio.ErrorInfo{Category: toolio.CategoryCancelled, Message: "the job was cancelled"})
	}
	if problem != "" {
		s.log.Warn("the runner returned an envelope that cannot be used", "job", req.JobID, "attempt", req.Attempt, "problem", problem)
		return synth(toolio.ErrorInfo{Category: toolio.CategoryInternal, Message: "the runner returned an envelope that cannot be used: " + problem})
	}
	return env, out.res.Envelope
}

// roundLimit is the most attempts a round may make, as the runner reported it: 0
// means 2, and the number is kept between 1 and 10.
func roundLimit(reported int) int {
	switch {
	case reported == 0:
		return defaultRound
	case reported < 1:
		return defaultRound
	}
	return min(reported, maxRound)
}

// ruling applies the first of the rules of 12-REQ-6 that matches the envelope. The
// round of attempt n is the latest attempt whose trigger is not auto, up to n, and the
// automatic attempts after it.
func ruling(env toolio.Envelope, attempts []attemptRow, n, max int, spent bool) verdict {
	switch env.Status {
	case toolio.StatusDone:
		return verdict{attempt: AttemptDone, job: StateDone}
	case toolio.StatusUnverified:
		if outcomeOf(env) != "parked" {
			return verdict{attempt: AttemptDone, job: StateDone}
		}
	case toolio.StatusNeedsHuman:
		return verdict{attempt: AttemptNeedsHuman, job: StateBlocked}
	case toolio.StatusFailed:
		if e := env.Error; e != nil && !spent && e.Retryable && (e.Category == toolio.CategoryBrain || e.Category == toolio.CategoryNoResult) &&
			roundSize(attempts, n) < max {
			return verdict{attempt: AttemptFailed, job: StateQueued, auto: true}
		}
	}
	return verdict{attempt: AttemptFailed, job: StateFailed}
}

// roundSize is how many attempts the round of attempt n has made, n included.
func roundSize(attempts []attemptRow, n int) int {
	start := n
	for start > 1 {
		var trigger string
		for _, a := range attempts {
			if a.N == start {
				trigger = a.TriggerKind
			}
		}
		if TriggerKind(trigger) != TriggerAuto {
			break
		}
		start--
	}
	return n - start + 1
}

// writePrivate writes a file that only the user can read, whole or not at all.
func writePrivate(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// endArgs is what an attempt came to, as the store records it.
type endArgs struct {
	jobID                    string
	attempt                  int
	verdict                  verdict
	env                      toolio.Envelope
	maxAttempts              int
	hasReport                bool
	labelsSet, labelsRemoved []string
}

// EndAttempt records the end of a running attempt in one transaction: the attempt's
// row, the labels of the job, the job's transition (or an automatic attempt n+1), and
// the events attempt_end and job_state. files is called inside the transaction, once
// the attempt is known to be running, so that nothing is written for an attempt that
// ended another way; a failure of it undoes the rest. It says whether the attempt was
// ended by this call, and the number of the automatic attempt made.
//
// An attempt of a cancelled job whose cancel cleanup is wanted, and whose runner reported no
// hub session, marks the cleanup pending: the worker writes nothing to an issue without its
// session. cleanup says that it did.
func (s *Store) EndAttempt(ctx context.Context, a endArgs, files func() error) (ended bool, next int, cleanup bool, err error) {
	err = s.run(ctx, a.jobID, func(x *txn) error {
		now := formatTime(s.now())
		env := a.env
		artifacts, sideEffects := env.Artifacts, env.SideEffects
		if artifacts == nil {
			artifacts = []toolio.Artifact{}
		}
		if sideEffects == nil {
			sideEffects = []toolio.SideEffect{}
		}
		var errorJSON, question, options any
		if env.Error != nil {
			errorJSON = mustJSON(env.Error)
		}
		if a.verdict.attempt == AttemptNeedsHuman {
			question, options = env.NeedsHuman.Question, mustJSON(lettered(env.NeedsHuman.Options))
		}
		// A job that was cancelled while the attempt ran: the attempt is cancelled, whatever it
		// came to (rule 1).
		res, err := x.ExecContext(ctx,
			`UPDATE af_attempts SET state = CASE WHEN (SELECT state FROM af_jobs WHERE id = ?) = 'cancelled' THEN 'cancelled' ELSE ? END,
			   max_attempts = ?, status = ?, exit_code = ?, summary = ?, error = ?,
			   artifacts = ?, side_effects = ?, usage = ?, question = ?, options = ?, has_envelope = 1, has_report = ?, finished_at = ?
			 WHERE job_id = ? AND n = ? AND state = 'running'`,
			a.jobID, string(a.verdict.attempt), a.maxAttempts, string(env.Status), toolio.ExitCode(env.Status), env.Summary, errorJSON,
			mustJSON(artifacts), mustJSON(sideEffects), mustJSON(env.Usage), question, options, a.hasReport, now,
			a.jobID, a.attempt)
		if err != nil {
			return fmt.Errorf("attempt %d of %s: %w", a.attempt, a.jobID, err)
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err // the attempt is not running: it ended another way, and nothing is changed
		}
		ended = true
		if files != nil {
			if err := files(); err != nil {
				return fmt.Errorf("files of attempt %d of %s: %w", a.attempt, a.jobID, err)
			}
		}
		if len(a.labelsSet) > 0 || len(a.labelsRemoved) > 0 {
			if _, err := x.ExecContext(ctx, "UPDATE af_jobs SET labels_set = ?, labels_removed = ? WHERE id = ?",
				mustJSON(nonNil(a.labelsSet)), mustJSON(nonNil(a.labelsRemoved)), a.jobID); err != nil {
				return fmt.Errorf("labels of %s: %w", a.jobID, err)
			}
		}
		var jobState State
		var requestLabel sql.NullString
		if err := x.QueryRowContext(ctx, "SELECT state, request_label FROM af_jobs WHERE id = ?", a.jobID).Scan(&jobState, &requestLabel); err != nil {
			return fmt.Errorf("job %s: %w", a.jobID, err)
		}
		attemptState := a.verdict.attempt
		if jobState == StateCancelled {
			attemptState = AttemptCancelled
		}
		if err := x.emit(ctx, a.attempt, EventAttemptEnd, map[string]any{
			"attempt": a.attempt, "state": attemptState, "status": env.Status, "exit_code": toolio.ExitCode(env.Status),
		}); err != nil {
			return err
		}
		if jobState == StateCancelled {
			cleanup, err = x.endCancelled(ctx, a, requestLabel)
			return err
		}

		if a.verdict.auto {
			next, err = x.requeue(ctx, StateRunning, Trigger{Kind: TriggerAuto, By: autoBy}, nil)
			return err
		}
		changed, err := s.transition(ctx, x.Tx, a.jobID, []State{StateRunning}, a.verdict.job)
		if err != nil {
			return err
		}
		if !changed {
			return x.refusal(ctx)
		}
		state := jobStateData{State: a.verdict.job, Attempt: a.attempt, AtRest: true}
		if a.verdict.job == StateBlocked {
			state.Blocked = &Blocked{Reason: "needs_human", Question: env.NeedsHuman.Question, Options: lettered(env.NeedsHuman.Options), AnswerWith: answerWith(a.jobID)}
		} else {
			state.Result = &ResultDoc{Status: env.Status, ExitCode: toolio.ExitCode(env.Status), Summary: env.Summary, Error: env.Error}
		}
		return x.emit(ctx, a.attempt, EventJobState, state)
	})
	if err != nil {
		return false, 0, false, err
	}
	return ended, next, cleanup, nil
}

// endCancelled records the end of an attempt of a job that was cancelled while it ran: the job stays cancelled, and
// the request label belongs back on the issue unless the runner reports that it set it itself. A cancel cleanup that is
// wanted is marked pending when the attempt reported no hub session, and endCancelled says whether it was.
func (x *txn) endCancelled(ctx context.Context, a endArgs, requestLabel sql.NullString) (cleanup bool, err error) {
	if requestLabel.Valid && slices.Contains(a.labelsSet, requestLabel.String) {
		if _, err := x.ExecContext(ctx, "UPDATE af_jobs SET labels_restore = NULL WHERE id = ?", a.jobID); err != nil {
			return false, fmt.Errorf("labels of %s: %w", a.jobID, err)
		}
	}
	res, err := x.ExecContext(ctx,
		`UPDATE af_jobs SET cleanup = 'pending' WHERE id = ? AND cleanup_wanted = 1 AND cleanup IS NULL
		   AND (SELECT hub_session FROM af_attempts WHERE job_id = ? AND n = ?) IS NULL`, a.jobID, a.jobID, a.attempt)
	if err != nil {
		return false, fmt.Errorf("cleanup of %s: %w", a.jobID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	env := a.env
	return n > 0, x.emit(ctx, a.attempt, EventJobState, jobStateData{
		State: StateCancelled, Attempt: a.attempt, AtRest: true,
		Result: &ResultDoc{Status: env.Status, ExitCode: toolio.ExitCode(env.Status), Summary: env.Summary, Error: env.Error},
	})
}

// lettered are the options of a question with the ids A, B, … in order, whatever ids
// the envelope gave them.
func lettered(options []toolio.Option) []Option {
	out := make([]Option, len(options))
	for i, o := range options {
		out[i] = Option{ID: string(rune('A' + i)), Label: o.Label}
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// mustJSON is the JSON of a value that is made of strings and numbers.
func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("jobs: %v cannot be encoded: %v", v, err))
	}
	return string(raw)
}
