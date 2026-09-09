package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/tools"
	"github.com/spf13/cobra"

	"github.com/agent-fox-dev/agentfox/agentspec"
)

// Flags shared by the commands that call a model.
const (
	flagReadSource   = "read-source"
	flagTrustProject = "trust-project"
	flagMaxTurns     = "max-turns"
	flagMaxBudget    = "max-budget"
	flagVerbose      = "verbose"
)

// addAgentFlags registers the flags that shape a model-backed run.
//
// They are per-command rather than persistent because they only mean
// something to refine and generate; a flag offered on `spec list` that does
// nothing is a flag someone will eventually pass and expect to work.
func addAgentFlags(cmd *cobra.Command) {
	cmd.Flags().Bool(flagReadSource, false,
		"let the model read the source tree at --source while it works (read-only)")
	cmd.Flags().Bool(flagTrustProject, false,
		"admit skills and context files from the source tree into the system prompt")
	cmd.Flags().Int(flagMaxTurns, 0, "maximum turns for one phase (0: the default)")
	cmd.Flags().Float64(flagMaxBudget, 0, "maximum spend in USD for one phase (0: the default)")
	cmd.Flags().BoolP(flagVerbose, "v", false, "report what the model reads and submits, on stderr")
}

// runOptionsFor turns the command line into agentspec run options.
//
// The --source directory has existed as a global flag all along and reached
// nothing that used it. It is the workspace now: with --read-source the model
// gets the non-mutating built-in tools rooted there, so a spec can be written
// against the code it describes rather than against the PRD alone. Without the
// flag nothing changes — reading a repository costs turns and tokens, and that
// is the operator's decision to make rather than a default to discover in a
// bill.
func runOptionsFor(cmd *cobra.Command) (agentspec.RunOptions, error) {
	var o agentspec.RunOptions

	readSource, _ := cmd.Flags().GetBool(flagReadSource)
	trustProject, _ := cmd.Flags().GetBool(flagTrustProject)
	source, _ := cmd.Flags().GetString("source")
	if source == "" {
		source = "."
	}

	if readSource || trustProject {
		// tools.NewWorkspace accepts a root that does not exist yet —
		// containment still works lexically — but here it always should, and
		// a typo in --source would otherwise produce a run against an empty
		// tree rather than an error.
		if info, err := os.Stat(source); err != nil {
			return o, fmt.Errorf("cannot use %s as a workspace: %w", source, err)
		} else if !info.IsDir() {
			return o, fmt.Errorf("cannot use %s as a workspace: not a directory", source)
		}
		ws, err := tools.NewWorkspace(source)
		if err != nil {
			return o, fmt.Errorf("cannot use %s as a workspace: %w", source, err)
		}
		o.WorkDir = ws.Root
		if readSource {
			o.Workspace = ws
		}
	}
	o.TrustProject = trustProject

	o.MaxTurns, _ = cmd.Flags().GetInt(flagMaxTurns)
	o.MaxBudgetUSD, _ = cmd.Flags().GetFloat64(flagMaxBudget)

	if verbose, _ := cmd.Flags().GetBool(flagVerbose); verbose && !isAgentMode() {
		o.OnEvent = traceEvents(cmd.ErrOrStderr())
	}
	return o, nil
}

// traceEvents reports what the model does, as it does it.
//
// It is on stderr because stdout carries the JSON envelope every command
// emits, and it is off in agent mode for the same reason a banner is: the
// caller there is a program reading one document, not a person watching a run.
func traceEvents(w io.Writer) func(core.Event) {
	return func(e core.Event) {
		switch v := e.(type) {
		case core.ToolExecutionStartEvent:
			fmt.Fprintf(w, "  → %s\n", v.Name)
		case core.ToolResultEvent:
			if v.Message.IsError {
				fmt.Fprintf(w, "  ✗ %s\n", firstLine(v.Message.Content.Text()))
			}
		case core.ErrorEvent:
			fmt.Fprintf(w, "  ! %s\n", v.Message)
		}
	}
}

// firstLine trims a message to something that fits on one terminal line.
func firstLine(s string) string {
	const max = 160
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
