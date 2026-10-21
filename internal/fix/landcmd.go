package fix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The ledger records of measuring a change.
const (
	LedgerMeasure = "measure"
	LedgerRefusal = "refusal"
)

// LandRunParams is what a change that is in the checkout is measured and landed with.
type LandRunParams struct {
	Dir string
	Git *gitx.Git
	// RunID and RunPath are the run and its directory, which holds the submission and the
	// original report.
	RunID, RunPath string
	State          State
	// Mode is pr, branch or none.
	Mode     string
	Forge    issuex.Client
	Target   issuex.Repo
	Reporter Reporter
	// Record notes an entry in the ledger of the run; it may be nil.
	Record func(kind string, data any)
	// WriteReport keeps the body of the pull request as the report of the run.
	WriteReport func(text string) error
	// Gate and Conform are the seams of Measure.
	Gate    func(ctx context.Context, dir string, o checks.Options) (checks.Result, error)
	Conform func(ctx context.Context, dir, since string, o conform.Options) (conform.Result, error)
}

// LandRunResult is what landing a run came to.
type LandRunResult struct {
	Result  Result
	Status  toolio.Status
	Summary string
}

// LandRun lands the change that is in the checkout, which a person made or a round of a
// brain left, once and without any round of a brain. The submission of the run must be a
// report. What it needs of the forge is checked first, then the change is measured: a
// change that may land is landed, and one that is refused is parked (a change that is
// nothing is not parked, since there is nothing to park).
func LandRun(ctx context.Context, p LandRunParams) (LandRunResult, error) {
	report, err := readReport(p)
	if err != nil {
		return LandRunResult{}, err
	}
	if err := CheckLanding(ctx, p.Git, PreflightParams{Land: p.Mode, Forge: p.Forge, Target: p.Target, Warn: p.Reporter.Warn}); err != nil {
		return LandRunResult{}, err
	}
	measured, err := Measure(ctx, MeasureParams{Dir: p.Dir, Git: p.Git, State: p.State, Report: &report, Warn: p.Reporter.Warn, Gate: p.Gate, Conform: p.Conform})
	if err != nil {
		return LandRunResult{}, err
	}
	RecordMeasurement(p.Record, measured)
	input := Input{Kind: p.State.Input.Kind, Origin: p.State.Input.Origin, Body: readOriginal(p.RunPath)}
	in := ResultInput{State: p.State, Rounds: p.State.Rounds, Measured: &measured, Land: p.Mode}

	if measured.Lands {
		landed, err := Land(ctx, LandParams{
			Git: p.Git, State: p.State, Input: input, Report: report, Mode: p.Mode, Forge: p.Forge, Target: p.Target, Measured: measured,
			Brain: p.State.Brain, CostUSD: p.State.CostUSD, CostSource: costSource(p.State), Reporter: p.Reporter, WriteReport: p.WriteReport,
		})
		if err != nil {
			return LandRunResult{}, err
		}
		in.Outcome, in.Branch, in.Commit, in.Pushed, in.PullRequest, in.IssueCreated = landed.Outcome, landed.Branch, landed.Commit, landed.Pushed, landed.PullRequest, landed.IssueCreated
		return LandRunResult{Result: NewResult(in), Status: landed.Status, Summary: summary(landed.Outcome, landed.Branch, landed.PullRequest)}, nil
	}

	if measured.Refusal != nil && measured.Refusal.Kind == RefusalNoChange {
		return LandRunResult{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: "there is no change in the checkout to land: the paths that changed were protected ones, and were put back; make the change, then run af fix land again"}
	}
	parked, err := Park(ctx, ParkParams{
		Git: p.Git, State: p.State, Input: input, Mode: p.Mode, Forge: p.Forge, Target: p.Target, Subject: report.Subject,
		Verdict: measured.Verdict, Failing: measured.Refusal.Failing, Report: p.Reporter,
	})
	if err != nil {
		return LandRunResult{}, err
	}
	in.Outcome, in.Branch, in.Commit, in.Pushed, in.PullRequest, in.IssueCreated = parked.Outcome, parked.Branch, parked.Commit, parked.Pushed, parked.PullRequest, parked.IssueCreated
	return LandRunResult{Result: NewResult(in), Status: parked.Status, Summary: summary(parked.Outcome, parked.Branch, parked.PullRequest)}, nil
}

// costSource is the source of the cost of the run, none when no round has been paid for.
func costSource(s State) string {
	if s.CostSource == "" {
		return toolio.CostNone
	}
	return s.CostSource
}

func summary(outcome, branch string, pr *PullRequestRef) string {
	text := fmt.Sprintf("%s the change on the branch %s", outcome, branch)
	if pr != nil {
		text += ", pull request " + pr.URL
	}
	return text
}

// RecordMeasurement notes what was measured in the ledger, and the refusal when there is
// one.
func RecordMeasurement(record func(kind string, data any), m Measurement) {
	if record == nil {
		return
	}
	data := map[string]any{"verdict": m.Verdict, "closes": m.Closes, "changed": m.Changed, "reverted": m.Reverted}
	if m.After != nil {
		data["after_gate_id"] = m.After.GateID
	}
	if m.RevertCheck != nil {
		data["revert_outcome"] = m.RevertCheck.Outcome
	}
	record(LedgerMeasure, data)
	if m.Refusal != nil {
		record(LedgerRefusal, map[string]any{"kind": m.Refusal.Kind})
	}
}

// CheckReport says whether the submission of the run is a report that can be landed, which
// is what LandRun needs of it first: a command that needs a forge for the landing checks it
// before it asks for one, so that a question is refused as a question.
func CheckReport(runID, runPath string, state State) error {
	_, err := readReport(LandRunParams{RunID: runID, RunPath: runPath, State: state})
	return err
}

// readReport reads the submission of the run, which must be a fix report: a question is
// answered with the command that asks again, and a run with no submission is told to
// submit one.
func readReport(p LandRunParams) (Report, error) {
	usage := func(format string, args ...any) error {
		return &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf(format, args...)}
	}
	data, err := os.ReadFile(filepath.Join(p.RunPath, "submission.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return Report{}, usage("run %s has no submission: submit the fix report first, with af fix submit --run-id %s", p.RunID, p.RunID)
	}
	if err != nil {
		return Report{}, usage("run %s: %v", p.RunID, err)
	}
	var file submission.File
	if err := json.Unmarshal(data, &file); err != nil || file.Kind != KindName {
		return Report{}, usage("submission.json of run %s is not a fix report: submit it again with af fix submit --run-id %s", p.RunID, p.RunID)
	}
	sub, err := Decode(file.Payload)
	if err != nil {
		return Report{}, usage("submission.json of run %s: %v", p.RunID, err)
	}
	if q := sub.Question; q != nil {
		input := p.State.Input.Origin
		if input == "" {
			input = "<input>"
		}
		return Report{}, &toolio.Failure{Category: toolio.CategorySubmission, Message: fmt.Sprintf(
			"the submission of run %s is a question, not a report, and a question cannot be landed: %q (%s). Answer it by running af fix brief %s --context <answer> again, then work and submit a report",
			p.RunID, q.Text, strings.Join(q.Options, ", "), input)}
	}
	return *sub.Report, nil
}

// readOriginal is the report as the run was given it, which the brief kept; it is empty
// when the run has none.
func readOriginal(runPath string) string {
	data, err := os.ReadFile(filepath.Join(runPath, "original.md"))
	if err != nil {
		return ""
	}
	return string(data)
}
