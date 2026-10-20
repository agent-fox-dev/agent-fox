package main

import (
	"flag"
	"fmt"
	"path/filepath"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// registerFixLand registers af fix land.
func registerFixLand(app *toolio.App) {
	var runID string
	var land landFlag
	app.Register(toolio.Command{
		Path:    "fix land",
		Summary: "Measure the change in the checkout and land it as a branch and a pull request, or park it",
		Usage:   "af fix land --run-id ID [--dir DIR] [--land pr|branch|none] [--timeout 45m]",
		Flags: func(fs *flag.FlagSet) {
			runID, land = "", fix.LandPR
			fs.StringVar(&runID, "run-id", "", "the `id` of the run, made by af fix brief, with a fix report submitted")
			fs.Var(&land, "land", "where the change goes, as a `mode`: pr (push the branch and open a pull request), branch (push it), or none (keep it on a local branch)")
		},
		MaxArgs:        toolio.NoArgs,
		Dir:            true,
		Timeout:        true,
		DefaultTimeout: fixDefaultTimeout,
		Grace:          fixGrace,
		ResultSchema:   resultSchema("fix_land"),
		Run:            func(r *toolio.Run) (toolio.Outcome, error) { return fixLand(r, runID, string(land)) },
	})
}

// repoOfState is the repository a run recorded, with its host.
func repoOfState(t fix.StateTarget) issuex.Repo {
	repo, _ := issuex.ParseRepo(t.Repo)
	repo.Host = t.Host
	return repo
}

// fixLand measures the change that is in the checkout, once, and lands it, or parks it when
// the measuring refuses it. No brain runs: this is what a person runs after working on their
// own checkout.
func fixLand(r *toolio.Run, runID, mode string) (toolio.Outcome, error) {
	run, state, err := openFixRun(r, runID)
	if err != nil {
		return toolio.Outcome{}, err
	}
	target := repoOfState(state.Target)
	var forge issuex.Client
	if mode == fix.LandPR {
		if !target.Valid() {
			return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf(
				"run %s recorded no repository to open the pull request in: use --land branch or --land none, or make the brief again with --repo owner/name", runID)}
		}
		if forge, err = r.ForgeFor(target); err != nil {
			return toolio.Outcome{}, err
		}
	}
	r.Artifact(toolio.Artifact{Kind: "run_dir", Path: run.Path})
	r.Artifact(toolio.Artifact{Kind: "submission", Path: filepath.Join(run.Path, "submission.json")})

	got, err := fix.LandRun(r.Ctx, fix.LandRunParams{
		Dir: r.Dir, Git: gitx.New(r.Dir, gitx.ExecRunner), RunID: runID, RunPath: run.Path, State: state, Mode: mode, Forge: forge, Target: target, Reporter: r,
		Record: func(kind string, data any) { recordIn(r, run, kind, data) },
		WriteReport: func(text string) error {
			if err := run.WriteReport(text); err != nil {
				return err
			}
			r.Artifact(toolio.Artifact{Kind: "report", Path: filepath.Join(run.Path, "report.md")})
			return nil
		},
	})
	if err != nil {
		return toolio.Outcome{}, err
	}
	return toolio.Outcome{Status: got.Status, Summary: got.Summary, Result: got.Result}, nil
}
