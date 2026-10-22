package afcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// registerRevertCheck registers af revert-check.
func registerRevertCheck(app *toolio.App) {
	var since string
	app.Register(toolio.Command{
		Path:    "revert-check",
		Summary: "Check that the tests of a change fail without the rest of the change",
		Usage:   "af revert-check --since REF [--dir DIR]",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&since, "since", "", "the commit the change starts from: a `ref` or a hash")
		},
		MaxArgs:        toolio.NoArgs,
		Dir:            true,
		Timeout:        true,
		DefaultTimeout: 30 * time.Minute,
		// Once told to stop it has the tree to put back, which takes as long
		// as the tree is big; the shell does not abandon it half way.
		Grace:        10 * time.Minute,
		ResultSchema: resultSchema("revert_check"),
		Run:          func(r *toolio.Run) (toolio.Outcome, error) { return revertCheck(r, since) },
	})
}

// revertCheck measures whether the tests of the change in r.Dir since the
// commit fail without the rest of it. Every measured answer, not proven and
// inconclusive included, is done; only a check that could not be made, or a
// tree that could not be put back, is a failure.
func revertCheck(r *toolio.Run, since string) (toolio.Outcome, error) {
	if since == "" {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: "--since is required: name the commit the change starts from"}
	}
	detection, err := detect(r)
	if err != nil {
		return toolio.Outcome{}, err
	}
	res, err := conform.Check(r.Ctx, r.Dir, since, conform.Options{
		Table: detection.Gate,
		// The table is found again on the reverted tree: a Makefile the change
		// added is not there to run.
		TableNow: func(context.Context) (project.Table, error) {
			again, err := detect(r)
			return again.Gate, err
		},
	})
	if err != nil {
		return toolio.Outcome{}, conformFailure(r, err)
	}
	if res.Outcome != conform.OutcomeProven && res.Outcome != conform.OutcomeNoChanges {
		r.Warn("fix_not_proven", fmt.Sprintf("the change is not proven: %s (%s)", res.Outcome, res.Reason))
	}
	return toolio.Outcome{Status: toolio.StatusDone, Summary: revertSummary(res), Result: res}, nil
}

// conformFailure is the error to return for an error of the conform package:
// a tree that could not be put back is a restore failure that names its
// backup, git that failed is a git failure, and the rest is as for the checks.
func conformFailure(r *toolio.Run, err error) error {
	var restore *conform.RestoreError
	var gitErr *conform.GitError
	switch {
	case errors.As(err, &restore):
		return &toolio.Failure{Category: toolio.CategoryRestore, Message: restore.Error()}
	case errors.As(err, &gitErr):
		return &toolio.Failure{Category: toolio.CategoryGit, Message: gitErr.Error()}
	}
	return checksFailure(r, err)
}

// revertSummary is the one line that says what the revert check found.
func revertSummary(res conform.Result) string {
	switch res.Outcome {
	case conform.OutcomeProven:
		return fmt.Sprintf("proven: the tests fail without the change (%d files reverted)", len(res.RevertedFiles))
	case conform.OutcomeNotProven:
		return fmt.Sprintf("not proven: the tests still pass without the change (%d files reverted)", len(res.RevertedFiles))
	}
	return fmt.Sprintf("%s: %s", res.Outcome, res.Reason)
}
