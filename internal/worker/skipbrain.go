package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage"
)

// The stages a resumed delivery picks up from, beside stageBrain: a fix report that was accepted and
// applied to the clone, and a submission that is landed without a change to the tree (a question, a
// diagnosis).
const (
	stageApplied   = "applied"
	stageSubmitted = "submitted"
)

// skipStage says what the submission of a run in the clone lets a resumed delivery skip: the brain is
// skipped when submission.json is a submission of the kind of the run, for this run, whose payload is
// what its hash says; the last submission_accepted record of the ledger carries that hash; the payload
// passes the check of its kind against the tree of the clone; and, for a fix report only, an apply
// record follows that record. The stage is applied for a fix report, submitted for a fix question and a
// triage, and empty, with the reason, when the brain has to run.
func skipStage(ctx context.Context, kind jobs.Kind, cloneDir, runID string) (stage, why string) {
	accepted, why := acceptedSubmission(kind, cloneDir, runID)
	if accepted == nil {
		return "", why
	}
	if _, problems := submission.Check(ctx, kindOfRun(kind, cloneDir), os.DirFS(cloneDir), accepted.payload); len(problems) > 0 {
		return "", "the payload no longer passes the check of its kind against the tree: " + problems[0].String()
	}
	if stage = accepted.stage(kind); stage == "" {
		return "", "the fix report was accepted and no apply of the tree follows"
	}
	return stage, ""
}

// stage is the stage that the submission allows, before it is checked against the tree: a fix report
// needs the apply of its tree, and a fix question or a triage does not.
func (s *acceptedPayload) stage(kind jobs.Kind) string {
	switch {
	case kind != jobs.KindFix || s.question:
		return stageSubmitted
	case s.applied:
		return stageApplied
	}
	return ""
}

// kindOfRun is the submission kind of the runs of the kind of job, over the clone.
func kindOfRun(kind jobs.Kind, cloneDir string) submission.Kind {
	if kind == jobs.KindTriage {
		return triage.Kind(cloneDir)
	}
	return fix.FixReport
}

// acceptedSubmission is a submission that the host accepted.
type acceptedPayload struct {
	payload  []byte
	question bool
	applied  bool
}

// acceptedSubmission reads the submission of the run and the ledger that says the host accepted it. It
// returns nothing, and why, when the file is not a submission of the run that the host accepted last.
func acceptedSubmission(kind jobs.Kind, cloneDir, runID string) (*acceptedPayload, string) {
	run, err := toolio.OpenRunDir(cloneDir, runID)
	if err != nil {
		return nil, "the run directory cannot be opened: " + err.Error()
	}
	data, err := os.ReadFile(filepath.Join(run.Path, "submission.json"))
	if err != nil {
		return nil, "the run has no submission: " + err.Error()
	}
	var file submission.File
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, "submission.json is not a submission file: " + err.Error()
	}
	wantKind := fix.KindName
	if kind == jobs.KindTriage {
		wantKind = triage.KindName
	}
	var payload bytes.Buffer
	switch {
	case file.Kind != wantKind:
		return nil, fmt.Sprintf("the submission is of the kind %q, and the run's is %q", file.Kind, wantKind)
	case file.RunID != runID:
		return nil, fmt.Sprintf("the submission is for the run %q, and this is %q", file.RunID, runID)
	case json.Compact(&payload, file.Payload) != nil || !bytes.HasPrefix(payload.Bytes(), []byte("{")):
		return nil, "the payload of the submission is not a JSON object"
	case submission.PayloadSHA256(payload.Bytes()) != file.PayloadSHA256:
		return nil, "the payload of the submission is not what its hash says"
	}
	entries, err := run.Ledger()
	if err != nil {
		return nil, "the ledger cannot be read: " + err.Error()
	}
	last := -1
	for i, e := range entries {
		if e.Kind == "submission_accepted" {
			last = i
		}
	}
	var accepted struct {
		SHA256 string `json:"sha256"`
	}
	if last < 0 || json.Unmarshal(entries[last].Data, &accepted) != nil || accepted.SHA256 != file.PayloadSHA256 {
		return nil, "the last submission that the host accepted is not this one"
	}
	out := &acceptedPayload{payload: payload.Bytes()}
	if kind == jobs.KindFix {
		sub, err := fix.Decode(payload.Bytes())
		if err != nil {
			return nil, err.Error()
		}
		out.question = sub.Question != nil
		for _, e := range entries[last+1:] {
			if e.Kind == fix.LedgerApply {
				out.applied = true
			}
		}
	}
	return out, ""
}
