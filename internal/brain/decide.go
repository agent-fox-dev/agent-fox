package brain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
// A file counts when it parses, carries this run's kind, run_id, attempt and
// nonce, its payload_sha256 is the hash of its payload, and the kind accepts the
// payload against the box's tree. Then it is copied to the run directory. A
// valid submission wins over a brain error or a stop reason. Without one, the
// run fails with the first of: the brief's timeout, the cancelled context, the
// brain's error, the budget, the turns, no result.
func (r *run) decide(ctx context.Context, outcome Outcome, runCtxErr, runErr error) (Result, error) {
	readCtx := ctx
	if ctx.Err() != nil || runCtxErr != nil {
		var cancel context.CancelFunc
		readCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), readTimeout)
		defer cancel()
	}

	res, found, err := r.readSubmission(readCtx, outcome)
	if err != nil {
		return Result{Outcome: outcome}, err
	}
	r.addTranscript()
	if outcome.Submitted != found {
		r.req.Report.Warn("submission_claim_mismatch", fmt.Sprintf("the brain says it submitted: %v, and the submission file says %v", outcome.Submitted, found))
	}
	if found {
		if runErr != nil {
			r.req.Report.Warn("brain_error", fmt.Sprintf("the %s brain returned an error after it submitted, which does not change the submission: %v", r.req.Name, runErr))
		}
		return res, nil
	}
	return Result{Outcome: outcome}, r.noSubmission(ctx, outcome, runCtxErr, runErr)
}

// readSubmission reads the submission of the box and decides whether it counts.
// It returns an error only for what the host itself could not do.
func (r *run) readSubmission(ctx context.Context, outcome Outcome) (Result, bool, error) {
	b, req := r.brief, r.req
	fsys := req.Session.FS(ctx)
	data, err := fs.ReadFile(fsys, submission.SubmissionPath(b.RunID))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		r.record("submission_absent", map[string]any{"attempt": b.Attempt})
		return Result{}, false, nil
	case err != nil:
		return r.invalid(outcome, submission.Problem{Message: fmt.Sprintf("the submission file could not be read: %v", err)})
	}

	var file submission.File
	if err := json.Unmarshal(data, &file); err != nil {
		return r.invalid(outcome, submission.Problem{Message: fmt.Sprintf("the submission file is not valid JSON: %v", err)})
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
	} else if sha256Hex(payload.Bytes()) != file.PayloadSHA256 {
		add("payload_sha256 is not the hash of the payload")
	}
	if len(problems) > 0 {
		return r.invalid(outcome, problems...)
	}

	// The payload is checked again against the box's tree, which is not what
	// the brain's own check saw if it checked at all; what comes out of that is
	// the payload of the submission.
	normalised := json.RawMessage(payload.Bytes())
	if b.Kind.Validate != nil {
		var found []submission.Problem
		normalised, found = b.Kind.Validate(ctx, fsys, json.RawMessage(payload.Bytes()))
		if len(found) > 0 {
			return r.invalid(outcome, found...)
		}
	}
	var final bytes.Buffer
	if err := json.Compact(&final, normalised); err != nil || !bytes.HasPrefix(final.Bytes(), []byte("{")) {
		return r.invalid(outcome, submission.Problem{Message: "the kind returned a payload that is not a JSON object"})
	}

	out := data
	if !bytes.Equal(final.Bytes(), payload.Bytes()) {
		file.Payload, file.PayloadSHA256 = json.RawMessage(final.Bytes()), sha256Hex(final.Bytes())
		out = marshalFile(file)
	}
	path := filepath.Join(req.RunDir.Path, "submission.json")
	if err := agentdir.WriteFileAtomic(path, out); err != nil {
		return Result{}, false, &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot write %s: %v", path, err)}
	}
	req.Report.Artifact(toolio.Artifact{Kind: "submission", Path: path})
	r.record("submission_accepted", map[string]any{"attempt": b.Attempt, "sha256": sha256Hex(final.Bytes())})
	return Result{Outcome: outcome, Payload: json.RawMessage(final.Bytes()), Path: path}, true, nil
}

// invalid is a submission file that does not count: the warning, the ledger
// entry, and no submission.
func (r *run) invalid(outcome Outcome, problems ...submission.Problem) (Result, bool, error) {
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

// noSubmission is why a run without a submission failed, in order: the brief's
// own timeout; the context that was cancelled, or whose deadline passed, which
// the shell turns into the failure it knows; the brain's error; the cost bound;
// the turn bound; and anything else.
func (r *run) noSubmission(ctx context.Context, outcome Outcome, runCtxErr, runErr error) error {
	b, name := r.brief, r.req.Name
	switch {
	case b.Timeout > 0 && errors.Is(runCtxErr, context.DeadlineExceeded) && ctx.Err() == nil:
		return &toolio.Failure{
			Category: toolio.CategoryTimeout,
			Message:  fmt.Sprintf("the %s brain did not submit within the %s the brief allows", name, b.Timeout),
			FixHint:  &toolio.FixHint{Flag: "--timeout", Suggest: (2 * b.Timeout).String()},
		}
	case ctx.Err() != nil:
		return ctx.Err()
	case runErr != nil:
		return &toolio.Failure{Category: CategoryBrain, Retryable: true, Message: fmt.Sprintf("the %s brain failed, and left no submission: %v", name, runErr)}
	case outcome.StopReason == StopBudget:
		hint := &toolio.FixHint{Flag: "--budget"}
		if b.MaxCostUSD > 0 {
			hint.Suggest = decimal(2 * b.MaxCostUSD)
		}
		return &toolio.Failure{Category: CategoryBudget, Message: fmt.Sprintf("the %s brain used the budget of $%s and left no submission", name, decimal(b.MaxCostUSD)), FixHint: hint}
	case outcome.StopReason == StopTurns:
		hint := &toolio.FixHint{Flag: "--max-turns"}
		if b.MaxTurns > 0 {
			hint.Suggest = strconv.Itoa(2 * b.MaxTurns)
		}
		return &toolio.Failure{Category: CategoryBudget, Message: fmt.Sprintf("the %s brain used its %d turns and left no submission", name, b.MaxTurns), FixHint: hint}
	}
	return &toolio.Failure{Category: CategoryNoResult, Retryable: true, Message: fmt.Sprintf("the %s brain stopped (%s) and left no valid submission", name, stopWords(outcome.StopReason))}
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

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// marshalFile writes a submission file as the submit core does, with the payload
// as it is hashed (a JSON encoder would write it again).
func marshalFile(f submission.File) []byte {
	quote := func(s string) string {
		q, _ := json.Marshal(s)
		return string(q)
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, `{"kind":%s,"run_id":%s,"attempt":%d,"nonce":%s,"submitted_at":%s,"payload_sha256":%s,"payload":`,
		quote(f.Kind), quote(f.RunID), f.Attempt, quote(f.Nonce), quote(f.SubmittedAt), quote(f.PayloadSHA256))
	out.Write(f.Payload)
	out.WriteString("}\n")
	return out.Bytes()
}
