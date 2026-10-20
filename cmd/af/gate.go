package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// registerGate registers af gate run.
func registerGate(app *toolio.App) {
	var label string
	var hermetic, noCache bool
	app.Register(toolio.Command{
		Path:    "gate run",
		Summary: "Run the checks of the gate on the tree, or return the result stored for it",
		Usage:   "af gate run [--dir DIR] [--label LABEL] [--hermetic] [--no-cache]",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&label, "label", "", "name the result, so that gate compare can find it")
			fs.BoolVar(&hermetic, "hermetic", false, "run the checks with a clean HOME and no git configuration")
			fs.BoolVar(&noCache, "no-cache", false, "run the checks even when a result is stored for this tree")
		},
		MaxArgs:        toolio.NoArgs,
		Dir:            true,
		Timeout:        true,
		DefaultTimeout: 30 * time.Minute,
		ResultSchema:   resultSchema("gate_run"),
		Run:            func(r *toolio.Run) (toolio.Outcome, error) { return runGate(r, label, hermetic, noCache) },
	})
}

// runGate runs the gate of the repository at r.Dir. A red gate, or one with
// nothing to run, is a measured fact and is done; a run that is stopped is an
// error, and stores nothing.
func runGate(r *toolio.Run, label string, hermetic, noCache bool) (toolio.Outcome, error) {
	detection, err := detect(r)
	if err != nil {
		return toolio.Outcome{}, err
	}
	res, err := checks.Run(r.Ctx, r.Dir, checks.Options{Table: detection.Gate, Hermetic: hermetic, NoCache: noCache, Label: label})
	if err != nil {
		if r.Ctx.Err() != nil {
			return toolio.Outcome{}, err // the shell says whether it was a signal or the timeout
		}
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryIO, Message: err.Error()}
	}

	if len(res.Checks) == 0 {
		r.Warn("no_checks", fmt.Sprintf("no check of the gate table can be run here (%d skipped), so nothing was measured", len(res.Skipped)))
	}
	if res.TreeChanged {
		r.Warn("tree_changed_during_gate", "the checks changed the tree; the result is kept under the hash of the tree they ran on")
	}
	r.Artifact(toolio.Artifact{Kind: "gate_result", Path: filepath.Join(r.Dir, ".agent-fox", "gate", res.ID, "result.json")})
	return toolio.Outcome{Status: toolio.StatusDone, Summary: gateSummary(res), Result: res}, nil
}

// gateSummary is the one line that says how the gate went.
func gateSummary(res checks.Result) string {
	cached := ""
	if res.Cached {
		cached = " (stored result)"
	}
	var names, red []string
	for _, c := range res.Checks {
		names = append(names, c.Name)
		if c.Status != checks.StatusPass {
			red = append(red, fmt.Sprintf("%s (%s)", c.Name, c.Status))
		}
	}
	switch {
	case len(res.Checks) == 0:
		return fmt.Sprintf("no check could run: %d skipped%s", len(res.Skipped), cached)
	case res.Passed:
		return fmt.Sprintf("gate passed%s: %s", cached, strings.Join(names, ", "))
	}
	return fmt.Sprintf("gate failed%s: %s", cached, strings.Join(red, ", "))
}
