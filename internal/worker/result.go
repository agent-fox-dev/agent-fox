package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// maxReport is how much of the report of a run is the report of the attempt.
const maxReport = 1 << 20

// loopOutcome is how the attempt ended when the loop returned, from the envelope it wrote. An
// attempt that was stopped is its context's end. A loop that failed in a way that is the
// infrastructure's, an envelope of status failed whose error is retryable and of category sandbox,
// forge or hub, is a transient error that carries the failure and no envelope, so that the same
// attempt is handed over again. Every other ending is the envelope, as the loop wrote it, with
// the report of the run beside it. A loop that returns after the attempt was stopped has the
// cause of the stop beside its envelope, which the end of the attempt makes use of.
func (r *Runner) loopOutcome(ctx context.Context, a *attempt, stdout []byte) *outcome {
	var env toolio.Envelope
	if err := json.Unmarshal(stdout, &env); err != nil {
		if ctx.Err() != nil {
			return &outcome{err: context.Cause(ctx)}
		}
		return &outcome{err: fmt.Errorf("worker: the loop wrote no envelope: %w", err)}
	}
	if ctx.Err() != nil { // the envelope the loop wrote when it was stopped goes with the cause
		return &outcome{err: context.Cause(ctx), result: jobs.Result{Envelope: stdout, Report: a.reportOf(env), MaxAttempts: a.maxAttempts}}
	}
	if f := infrastructureFailure(env.Error); f != nil {
		return &outcome{err: jobs.Transient(f)}
	}
	return &outcome{result: jobs.Result{Envelope: stdout, Report: a.reportOf(env), MaxAttempts: a.maxAttempts}}
}

// infrastructureFailure is the failure of an envelope that is the infrastructure's, or nil. A failure
// to read or write that is retryable is the plan of a landing that could not be recorded.
func infrastructureFailure(e *toolio.ErrorInfo) *toolio.Failure {
	if e == nil || !e.Retryable {
		return nil
	}
	switch e.Category {
	case toolio.CategorySandbox, toolio.CategoryForge, CategoryHub, toolio.CategoryIO:
		return &toolio.Failure{Category: e.Category, Message: e.Message, Retryable: true}
	}
	return nil
}

// reportOf is the text of the report of the run that the envelope names, or "". The file is the
// loop's own, in the clone.
func (a *attempt) reportOf(env toolio.Envelope) string {
	for _, artifact := range env.Artifacts {
		if artifact.Kind != "report" || artifact.Path == "" || !within(a.cloneDir, artifact.Path) {
			continue
		}
		f, err := os.Open(artifact.Path)
		if err != nil {
			return ""
		}
		defer f.Close()
		text, err := io.ReadAll(io.LimitReader(f, maxReport))
		if err != nil {
			return ""
		}
		return string(text)
	}
	return ""
}

// within says whether path is under dir.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// keepsClone says whether the clone is kept when the attempt returns: after a transient error,
// which the same attempt is handed over again for, and after a shutdown. Both find it removed and
// made afresh by the next delivery all the same.
func keepsClone(ctx context.Context, out *outcome) bool {
	return jobs.IsTransient(out.err) || errors.Is(context.Cause(ctx), jobs.ErrShutdown)
}
