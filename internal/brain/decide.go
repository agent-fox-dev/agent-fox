package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// readTimeout bounds the read of the submission when the run's own context has
// expired: the file may be there all the same.
const readTimeout = 30 * time.Second

// decide is the host's decision, after the brain, whether a submission exists
// and what the run came to. It is made from the submission file in the box, and
// from nothing the brain said or printed: the outcome counts only for the usage
// and for the warning that the claim and the file disagree.
//
// A file counts when it is no larger than a submission file may be, parses,
// carries this run's kind, run_id, attempt and nonce, its payload_sha256 is the
// hash of its payload, and the kind accepts the payload against the box's tree
// (submission.Check: at most 1 MiB, valid UTF-8, one JSON object). Then the
// submission is written to the run directory, from what was checked and nothing
// else in the file. A valid submission wins over a brain error, a panic or a
// stop reason, and a run directory that has no valid submission has no
// submission.json: a brain whose process could write to the host (the local
// box has no isolation) cannot leave one there. Without a submission, the run
// fails with the first of: the panic, the brief's timeout, the cancelled
// context, the brain's error, the budget, the turns, no result.
func (r *run) decide(ctx context.Context, outcome Outcome, runCtxErr, runErr, panicErr error) (Result, error) {
	readCtx := ctx
	if ctx.Err() != nil || runCtxErr != nil {
		var cancel context.CancelFunc
		readCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), readTimeout)
		defer cancel()
	}

	res, found, err := r.readSubmission(readCtx)
	if err != nil || !found {
		r.dropHostSubmission()
	}
	if err != nil {
		return Result{Outcome: outcome}, err
	}
	r.addTranscript()
	if outcome.Submitted != found {
		r.req.Report.Warn("submission_claim_mismatch", fmt.Sprintf("the brain says it submitted: %v, and the submission file says %v", outcome.Submitted, found))
	}
	if found {
		res.Outcome = outcome
		switch {
		case panicErr != nil:
			r.req.Report.Warn("brain_error", fmt.Sprintf("the %s brain panicked after it submitted, which does not change the submission: %v", r.req.Name, panicErr))
		case runErr != nil:
			r.req.Report.Warn("brain_error", fmt.Sprintf("the %s brain returned an error after it submitted, which does not change the submission: %v", r.req.Name, runErr))
		}
		return res, nil
	}
	return Result{Outcome: outcome}, r.noSubmission(ctx, outcome, runCtxErr, runErr, panicErr)
}

// hostSubmission is the submission of the run in the run directory.
func (r *run) hostSubmission() string { return filepath.Join(r.req.RunDir.Path, "submission.json") }

// clearHostSubmission removes the submission of the run directory, if there is
// one: the run has no valid submission, and a file there is a stale one of an
// earlier attempt or one that the brain's own process wrote.
func (r *run) clearHostSubmission() {
	if err := os.Remove(r.hostSubmission()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		r.req.Report.Warn("submission_not_removed", fmt.Sprintf("%s is not a submission af accepted, and could not be removed: %v", r.hostSubmission(), err))
	}
}

// startHostSubmission readies the host's submission.json for an attempt. Where the box
// has a tree of its own the file is a stale one of an earlier attempt, and goes. Where
// the box is the checkout it is the same file the brain writes, and the bytes the host
// accepted last are kept, to be put back if this attempt is not accepted.
func (r *run) startHostSubmission() {
	if !r.req.Session.InPlace() {
		r.clearHostSubmission()
		return
	}
	if data, err := os.ReadFile(r.hostSubmission()); err == nil {
		r.kept, r.hasKept = data, true
	}
}

// dropHostSubmission is what an attempt without a valid submission leaves in the run
// directory. With a tree of its own, no submission.json. In place, the file holds only
// what the host accepted: what the brain left there is moved to
// submission.rejected.<attempt>.json, unless it is the kept bytes, and the kept bytes
// are put back, or no file is left when there were none.
func (r *run) dropHostSubmission() {
	if !r.req.Session.InPlace() {
		r.clearHostSubmission()
		return
	}
	path := r.hostSubmission()
	if current, err := os.ReadFile(path); err == nil && !(r.hasKept && bytes.Equal(current, r.kept)) {
		rejected := filepath.Join(r.req.RunDir.Path, fmt.Sprintf("submission.rejected.%d.json", r.brief.Attempt))
		if err := os.Rename(path, rejected); err != nil {
			r.req.Report.Warn("submission_not_removed", fmt.Sprintf("%s is not a submission af accepted, and could not be moved aside: %v", path, err))
			return
		}
	}
	if r.hasKept {
		if _, err := os.Stat(path); err == nil {
			return // it is the kept bytes
		}
		if err := agentdir.WriteFileAtomic(path, r.kept); err != nil {
			r.req.Report.Warn("submission_not_removed", fmt.Sprintf("the submission af accepted before could not be put back in %s: %v", path, err))
		}
	}
}

// readSubmission reads the submission of the box and decides whether it counts.
// It returns an error only for what the host itself could not do.
func (r *run) readSubmission(ctx context.Context) (Result, bool, error) {
	b, req := r.brief, r.req
	fsys := req.Session.FS(ctx)
	data, err := fs.ReadFile(fsys, submission.SubmissionPath(b.RunID))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		r.record("submission_absent", map[string]any{"attempt": b.Attempt})
		return Result{}, false, nil
	case err != nil:
		return r.invalid(submission.Problem{Message: fmt.Sprintf("the submission file could not be read: %v", err)})
	case len(data) > submission.MaxFileBytes:
		return r.invalid(submission.Problem{Message: fmt.Sprintf("the submission file is larger than the %d bytes that one may be", submission.MaxFileBytes)})
	}

	var file submission.File
	if err := json.Unmarshal(data, &file); err != nil {
		return r.invalid(submission.Problem{Message: fmt.Sprintf("the submission file is not valid JSON: %v", err)})
	}
	if file.Nonce != r.nonce {
		// Left by an attempt before this one, or by a sync: not this run's.
		r.record("submission_stale", map[string]any{"attempt": b.Attempt, "file_attempt": file.Attempt})
		return Result{}, false, nil
	}

	var problems []submission.Problem
	add := func(format string, args ...any) {
		problems = append(problems, submission.Problem{Message: fmt.Sprintf(format, args...)})
	}
	if file.Kind != b.Kind.Name {
		add("kind is %q, and this run's is %q", file.Kind, b.Kind.Name)
	}
	if file.RunID != b.RunID {
		add("run_id is %q, and this run's is %q", file.RunID, b.RunID)
	}
	if file.Attempt != b.Attempt {
		add("attempt is %d, and this one is %d", file.Attempt, b.Attempt)
	}
	var payload bytes.Buffer
	if err := json.Compact(&payload, file.Payload); err != nil || !bytes.HasPrefix(payload.Bytes(), []byte("{")) {
		add("payload is not a JSON object")
	} else if submission.PayloadSHA256(payload.Bytes()) != file.PayloadSHA256 {
		add("payload_sha256 is not the hash of the payload")
	}
	if len(problems) > 0 {
		return r.invalid(problems...)
	}

	// The payload is checked again against the box's tree, which is not what
	// the brain's own check saw if it checked at all; what comes out of that is
	// the payload of the submission.
	final, found := submission.Check(ctx, b.Kind, fsys, payload.Bytes())
	if len(found) > 0 {
		return r.invalid(found...)
	}

	sum := submission.PayloadSHA256(final)
	out := submission.Marshal(submission.File{
		Kind: file.Kind, RunID: file.RunID, Attempt: file.Attempt, Nonce: file.Nonce,
		SubmittedAt: file.SubmittedAt, PayloadSHA256: sum, Payload: final,
	})
	path := r.hostSubmission()
	if err := agentdir.WriteFileAtomic(path, out); err != nil {
		return Result{}, false, &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot write %s: %v", path, err)}
	}
	req.Report.Artifact(toolio.Artifact{Kind: "submission", Path: path})
	r.record("submission_accepted", map[string]any{"attempt": b.Attempt, "sha256": sum})
	return Result{Payload: final, Path: path}, true, nil
}

// invalid is a submission file that does not count: the warning, the ledger
// entry, and no submission.
func (r *run) invalid(problems ...submission.Problem) (Result, bool, error) {
	lines := make([]string, len(problems))
	for i, p := range problems {
		lines[i] = p.String()
	}
	r.req.Report.Warn("submission_invalid", "the submission file in the box does not count, so the run has no submission: "+strings.Join(lines, "; "))
	r.record("submission_invalid", map[string]any{"attempt": r.brief.Attempt, "problems": lines})
	return Result{}, false, nil
}

// addTranscript lists the brain's transcript as an artifact, if it left one. It
// is looked at, and never read.
func (r *run) addTranscript() {
	if info, err := os.Stat(r.brief.TranscriptPath); err == nil && info.Mode().IsRegular() {
		r.req.Report.Artifact(toolio.Artifact{Kind: "transcript", Path: r.brief.TranscriptPath})
	}
}

// noSubmission is why a run without a submission failed, in order: the panic of
// the brain; the brief's own timeout; the context that was cancelled, or whose
// deadline passed, which the shell turns into the failure it knows; the brain's
// error; the cost bound; the turn bound; and anything else.
func (r *run) noSubmission(ctx context.Context, outcome Outcome, runCtxErr, runErr, panicErr error) error {
	b, name := r.brief, r.req.Name
	switch {
	case panicErr != nil:
		return panicErr
	case b.Timeout > 0 && errors.Is(runCtxErr, context.DeadlineExceeded) && ctx.Err() == nil:
		return &toolio.Failure{
			Category: toolio.CategoryTimeout,
			Message:  fmt.Sprintf("the %s brain did not submit within the %s the brief allows", name, b.Timeout),
			FixHint:  &toolio.FixHint{Flag: "--timeout", Suggest: (2 * b.Timeout).String()},
		}
	case ctx.Err() != nil:
		return ctx.Err()
	case runErr != nil:
		var failure *toolio.Failure
		if errors.As(runErr, &failure) {
			return failure // a failure the brain names itself (a usage mistake, a configuration that cannot be used) keeps its category
		}
		return &toolio.Failure{Category: toolio.CategoryBrain, Retryable: true, Message: fmt.Sprintf("the %s brain failed, and left no submission: %v", name, runErr)}
	case outcome.StopReason == StopBudget:
		hint := &toolio.FixHint{Flag: "--budget"}
		if b.MaxCostUSD > 0 {
			hint.Suggest = decimal(2 * b.MaxCostUSD)
		}
		return &toolio.Failure{Category: toolio.CategoryBudget, Message: fmt.Sprintf("the %s brain used the budget of $%s and left no submission", name, decimal(b.MaxCostUSD)), FixHint: hint}
	case outcome.StopReason == StopTurns:
		hint := &toolio.FixHint{Flag: "--max-turns"}
		if b.MaxTurns > 0 {
			hint.Suggest = strconv.Itoa(2 * b.MaxTurns)
		}
		return &toolio.Failure{Category: toolio.CategoryBudget, Message: fmt.Sprintf("the %s brain used its %d turns and left no submission", name, b.MaxTurns), FixHint: hint}
	}
	return &toolio.Failure{Category: toolio.CategoryNoResult, Retryable: true, Message: fmt.Sprintf("the %s brain stopped (%s) and left no valid submission", name, stopWords(outcome.StopReason))}
}

// stopWords is a stop reason as it reads in a sentence.
func stopWords(reason string) string {
	if reason == "" {
		return "with no reason given"
	}
	return "reason " + reason
}

// decimal is a number of dollars with at least one decimal.
func decimal(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}
