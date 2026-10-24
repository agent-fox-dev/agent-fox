package afcmd

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// registerGate registers af gate run.
func registerGate(app *toolio.App) {
	app.Register(toolio.Command{
		Path:           "gate run",
		Summary:        "Run the checks of the gate on the tree, or return the result stored for it",
		Usage:          "af gate run [--dir DIR]",
		MaxArgs:        toolio.NoArgs,
		Dir:            true,
		Timeout:        true,
		DefaultTimeout: 30 * time.Minute,
		ResultSchema:   resultSchema("gate_run"),
		Run:            runGate,
	})
}

// runGate runs the gate of the repository at r.Dir. A red gate, or one with
// nothing to run, is a measured fact and is done; a run that is stopped is an
// error, and stores nothing.
func runGate(r *toolio.Run) (toolio.Outcome, error) {
	detection, err := detect(r)
	if err != nil {
		return toolio.Outcome{}, err
	}
	res, err := checks.Run(r.Ctx, r.Dir, checks.Options{Table: detection.Gate})
	if err != nil {
		return toolio.Outcome{}, checksFailure(r, err)
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

// checksFailure is the error to return for an error of the checks package: a
// mistake in the invocation is a usage failure, a run that was stopped is
// returned as it is for the shell to say whether a signal or the timeout
// stopped it, and anything else is a failure to read or write.
func checksFailure(r *toolio.Run, err error) error {
	var usage *checks.UsageError
	switch {
	case errors.As(err, &usage):
		return &toolio.Failure{Category: toolio.CategoryUsage, Message: usage.Message}
	case r.Ctx.Err() != nil:
		return err
	}
	return &toolio.Failure{Category: toolio.CategoryIO, Message: err.Error()}
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
