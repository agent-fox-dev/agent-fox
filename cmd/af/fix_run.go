package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The ledger record of a round run by af fix run.
const ledgerFixRun = "fix.run"

// fixRunResult is the result of af fix run.
type fixRunResult struct {
	RunID string `json:"run_id"`
	Round int    `json:"round"`
	// Submission is report, or question for a round that asked one.
	Submission     string        `json:"submission"`
	SubmissionPath string        `json:"submission_path"`
	Question       *fix.Question `json:"question"`
	// Apply is what was applied to the checkout, and null for a question.
	Apply *fix.ApplyResult `json:"apply"`
}

// registerFixRun registers af fix run.
func registerFixRun(app *toolio.App) {
	var runID string
	var settings *brain.Settings
	app.Register(toolio.Command{
		Path:    "fix run",
		Summary: "Run one round of a brain on the brief of a fix run, and apply its tree to the checkout without measuring it",
		Usage:   "af fix run --run-id ID [--dir DIR] [--brain NAME] [--sandbox NAME] [--budget USD] [--max-turns N] [--timeout 45m]",
		Flags: func(fs *flag.FlagSet) {
			runID = ""
			fs.StringVar(&runID, "run-id", "", "the `id` of the run, made by af fix brief")
			settings = brain.Flags(fs, brain.Defaults{Sandbox: "local", Budget: fixDefaultBudget, MaxTurns: fixDefaultMaxTurns})
		},
		MaxArgs:        toolio.NoArgs,
		Dir:            true,
		Timeout:        true,
		DefaultTimeout: fixDefaultTimeout,
		Grace:          fixGrace,
		ResultSchema:   resultSchema("fix_run"),
		Run:            func(r *toolio.Run) (toolio.Outcome, error) { return fixRun(r, runID, settings) },
	})
}

// openFixRun opens the run directory of a fix run and reads its fix.json: a command that
// carries a run on needs both, and a directory without fix.json is no fix run.
func openFixRun(r *toolio.Run, runID string) (*toolio.RunDir, fix.State, error) {
	if runID == "" {
		return nil, fix.State{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: "--run-id is required: the id of the run that af fix brief made"}
	}
	run, err := toolio.OpenRunDir(r.Dir, runID)
	if err != nil {
		return nil, fix.State{}, err
	}
	state, err := fix.LoadState(run.Path)
	if err != nil {
		return nil, fix.State{}, notAFixRun(runID, err)
	}
	return run, state, nil
}

// notAFixRun is the usage failure for a run directory that fix.json cannot be read from.
func notAFixRun(runID string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("run %s has no %s, so it is not a fix run: run af fix brief first", runID, fix.StateFile)}
	}
	return &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("run %s: %v", runID, err)}
}

// fixRun runs one round of the brain on the brief of the run, in a box over the checkout,
// and applies the tree of an accepted report to the checkout. It does not measure the
// change, and it makes no branch and no commit.
func fixRun(r *toolio.Run, runID string, s *brain.Settings) (toolio.Outcome, error) {
	run, state, err := openFixRun(r, runID)
	if err != nil {
		return toolio.Outcome{}, err
	}
	if err := s.Validate(); err != nil {
		return toolio.Outcome{}, err
	}
	entry, err := fixRegistry.Lookup(s.Brain)
	if err != nil {
		return toolio.Outcome{}, err
	}
	if _, err := box.Default.Lookup(s.Sandbox); err != nil {
		return toolio.Outcome{}, err
	}
	document, err := os.ReadFile(filepath.Join(run.Path, "brief.md"))
	if err != nil {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("run %s has no brief (%v): run af fix brief first", runID, err)}
	}
	prefix, task, err := fix.SplitBrief(string(document), state)
	if err != nil {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("the brief of run %s is not the one fix.json was made with: %v", runID, err)}
	}
	opts := fixBrainOptions()
	opts.Script = s.Script
	b, err := entry.New(opts)
	if err != nil {
		return toolio.Outcome{}, err
	}

	round := state.Rounds + 1
	tally := &brain.Tally{}
	var out fix.RoundResult
	err = box.With(r.Ctx, box.Request{
		Provider: s.Sandbox, HostDir: r.Dir, RunID: runID, Tool: "fix",
		Env: brain.BuildBoxEnv(entry, opts.Getenv), Record: run.Record, Warn: r.Warn,
	}, func(sess *box.Session) error {
		var err error
		out, err = fix.RunRound(r.Ctx, fix.RoundParams{
			Dir: r.Dir, Git: gitx.New(r.Dir, gitx.ExecRunner), Session: sess, Name: s.Brain, Brain: b, Tally: tally,
			RunDir: run, Report: r, State: state, Prefix: prefix, Task: task, Round: round,
			MaxCostUSD: s.Budget, MaxTurns: s.MaxTurns, Timeout: timeLeft(r),
		})
		return err
	})
	if err != nil {
		return toolio.Outcome{}, err
	}

	state.Rounds = round
	state.AddUsage(tally.Usage(s.Brain))
	if err := state.Write(run.Path); err != nil {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryIO, Message: err.Error()}
	}
	result := fixRunResult{RunID: runID, Round: round, Submission: "report", SubmissionPath: out.Result.Path, Question: out.Submission.Question, Apply: out.Apply}
	summary := fmt.Sprintf("round %d of run %s: the tree of the report is in the checkout, uncommitted", round, runID)
	if out.Submission.Question != nil {
		result.Submission, summary = "question", fmt.Sprintf("round %d of run %s asked a question, and applied nothing", round, runID)
	} else {
		r.Next(toolio.Next{Argv: []string{"af", "fix", "land", "--run-id", runID, "--dir", r.Dir}, Reason: "measure the change in the checkout and land it"})
	}
	if err := run.Record(ledgerFixRun, map[string]any{"round": round, "submission": result.Submission, "submission_path": result.SubmissionPath}); err != nil {
		r.Warn("ledger_unwritable", fmt.Sprintf("the ledger entry %s could not be written: %v", ledgerFixRun, err))
	}
	return toolio.Outcome{Status: toolio.StatusDone, Summary: summary, Result: result}, nil
}
