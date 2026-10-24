package afcmd

import (
	"flag"
	"fmt"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/fix"
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
	// Submitted says the brain handed over a submission that was accepted. The host that
	// runs af fix run inside a box reads it from the result.
	Submitted bool `json:"submitted"`
}

// registerFixRun registers af fix run, which a box executes: the brain, in this process, on
// the brief that brain.Execute placed in the run directory of the box, over the directory it
// is in. It is no command for a person, and help does not list it.
func registerFixRun(app *toolio.App, c *Config) {
	var runID string
	var settings *brain.Settings
	app.Register(toolio.Command{
		Path:    "fix run",
		Summary: "Run one round of a brain in this process on the brief placed in a box",
		Hidden:  true,
		Usage:   "af fix run --run-id ID [--dir DIR] [--brain NAME] [--budget USD] [--max-turns N] [--timeout 45m]",
		Flags: func(fs *flag.FlagSet) {
			runID = ""
			fs.StringVar(&runID, "run-id", "", "the `id` of the run that was placed in the box")
			settings = brain.Flags(fs, brain.Defaults{Budget: fixDefaultBudget, MaxTurns: fixDefaultMaxTurns})
		},
		MaxArgs:        toolio.NoArgs,
		Dir:            true,
		Timeout:        true,
		DefaultTimeout: fixDefaultTimeout,
		Grace:          fixGrace,
		Run:            func(r *toolio.Run) (toolio.Outcome, error) { return fixRun(c, r, runID, settings) },
	})
}

// fixRun opens the run that was placed in the box and runs the brain on it.
func fixRun(c *Config, r *toolio.Run, runID string, s *brain.Settings) (toolio.Outcome, error) {
	if runID == "" {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: "--run-id is required: the id of the run that was placed in the box"}
	}
	run, err := toolio.OpenRunDir(r.Dir, runID)
	if err != nil {
		return toolio.Outcome{}, err
	}
	placed, ok, err := readPlaced(run, fix.FixReport.Name)
	if err != nil {
		return toolio.Outcome{}, err
	}
	if !ok {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("run %s has no brief placed in it: af fix run runs only in the box of af fix", runID)}
	}
	return fixDriverRun(c, r, run, placed, s)
}

// fixDriverRun runs the brain on the brief that a process outside the box placed in it,
// in this process and over the directory where it is, with no box of its own: the tree
// is the box's, the host applies it, and what this process hands back is the
// submission, made under the attempt and the nonce of run.json, and the usage of the
// run. It is af fix run as a box executes it.
func fixDriverRun(c *Config, r *toolio.Run, run *toolio.RunDir, placed placedRun, s *brain.Settings) (toolio.Outcome, error) {
	if err := s.ValidateIn(c.getenv(), c.boxes()); err != nil {
		return toolio.Outcome{}, err
	}
	entry, err := c.brains().Lookup(s.Brain)
	if err != nil {
		return toolio.Outcome{}, err
	}
	opts := c.brainOptions()
	opts.Script = s.Script
	b, err := entry.New(opts)
	if err != nil {
		return toolio.Outcome{}, err
	}
	sess, err := box.Here(r.Ctx, box.Request{HostDir: r.Dir, RunID: run.ID, Tool: "fix", Record: run.Record, Warn: r.Warn})
	if err != nil {
		return toolio.Outcome{}, err
	}
	defer func() { _ = sess.Close() }()
	var offered []string
	for _, tool := range sess.Tools() {
		offered = append(offered, tool.Name)
	}
	brief := fix.NewBrief(fix.BriefParams{
		RunID: run.ID, Prefix: placed.Prefix, Task: placed.Task, Round: placed.Run.Attempt, ToolNames: offered,
		MaxCostUSD: s.Budget, MaxTurns: s.MaxTurns, Timeout: timeLeft(r),
	})
	if placed.Hook != nil {
		brief.Hook = *placed.Hook
	}

	res, err := brain.Execute(r.Ctx, brain.Request{
		Session: sess, Name: s.Brain, Brain: b, RunDir: run, Report: r, Tally: &brain.Tally{}, Nonce: placed.Run.Nonce, Brief: brief,
	})
	if err != nil {
		return toolio.Outcome{}, err
	}
	submitted, err := fix.Decode(res.Payload)
	if err != nil {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryInternal, Message: err.Error()}
	}
	result := fixRunResult{RunID: run.ID, Round: placed.Run.Attempt, Submission: "report", SubmissionPath: res.Path, Question: submitted.Question, Submitted: true}
	if submitted.Question != nil {
		result.Submission = "question"
	}
	if err := run.Record(ledgerFixRun, map[string]any{"round": result.Round, "submission": result.Submission, "submission_path": result.SubmissionPath}); err != nil {
		r.Warn("ledger_unwritable", fmt.Sprintf("the ledger entry %s could not be written: %v", ledgerFixRun, err))
	}
	return toolio.Outcome{
		Status:  toolio.StatusDone,
		Summary: fmt.Sprintf("the %s brain submitted a %s for run %s", s.Brain, result.Submission, run.ID),
		Result:  result,
	}, nil
}
