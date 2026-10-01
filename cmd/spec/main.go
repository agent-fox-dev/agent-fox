// Command spec turns a product idea into a complete, validated version 2
// specification package.
//
//	spec [flags] <text | file | issue-url | ->
//
// It takes exactly one input and writes exactly one JSON object to stdout.
// Progress goes to stderr, so the two never interleave.
package main

import (
	"context"
	"flag"
	"os"

	"github.com/agent-fox-dev/agentfox"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/specgen"
)

const usage = `spec — turn a product idea into a validated specification package

Usage:
  spec [flags] <input>

The input is exactly one of:
  a GitHub or GitLab issue or pull/merge-request URL
                                       the issue and its comments are the idea
  a path to a readable file            the file's contents are the idea
  any other text                       the text is the idea
  -                                    the idea is read from stdin

Output is one JSON object on stdout; progress goes to stderr.

--dry-run makes no remote change and, for spec, nothing local either: it writes
no files under the spec root, posts no comment, and reports the package that
would have been written. --total-budget is checked before each scope of a split
begins its PRD phase (and a split that stops there keeps its plan to resume
from); an input that is one spec's worth of work has no interior boundary, so
there it lowers the one phase's ceiling like --budget.

The run is unattended: nobody is waiting to answer questions, so the PRD phase
resolves every open question itself, records each in a '## Design Decisions'
section, and reports the ones it is least sure of as open_questions in the
result. It then generates requirements.json, test_spec.json and tasks.json in
that order — each validated against the format's schema and its cross-file
rules before it is written — writes the package under .specs/NN_name/, and
activates it if it validates.

An input that is more than one spec's worth of work is split: the PRD phase
reports every scope, and spec writes all of them, one package per scope, each
built on the ones before it. The split is recorded in the spec root as
<first_scope>.split.json until the last package is written; a run that stops
early is resumed by running spec on the same input again.

Everything the model does here is read-only. The only thing written is the
spec package itself, by this program, after the run.

Exit codes:
  0  a valid package was written
  1  failed; the stage is named in the JSON. An 'invalid_spec' failure still
     leaves the package on disk with the broken rules named. A split that
     stopped early names the scope, and the plan remains to resume from.
  2  usage error — nothing was fetched, nothing was written

Flags:
`

// specFlags holds the values of spec's own flags. Both the ordinary Exec and
// the --preflight PreflightExec read them through specOptions, so the two
// paths build identical specgen.Options and a check run under --preflight
// cannot pass against a configuration the real run would not use.
type specFlags struct {
	specsDir     string
	name         string
	architecture bool
	noActivate   bool
	comment      bool
}

// specOptions builds the specgen.Options for one run from the parsed flags
// and what App.execute resolved.
func (f *specFlags) specOptions(d toolio.Deps) specgen.Options {
	return specgen.Options{
		Input:          d.Input,
		Workspace:      d.Workspace,
		SpecsDir:       f.specsDir,
		Name:           f.name,
		Architecture:   f.architecture,
		Activate:       !f.noActivate,
		Comment:        f.comment,
		DryRun:         d.Common.DryRun,
		TotalBudgetUSD: d.Common.TotalBudgetUSD,
		Runner:         d.Runner,
		Forge:          d.Forge,
		Run:            d.Run,
		Progress:       d.Progress,
	}
}

func newApp() toolio.App {
	f := &specFlags{}
	var common *toolio.Common

	return toolio.App{
		Name:             "spec",
		Version:          agentfox.Version,
		Usage:            usage,
		Description:      "Turns a product idea into a validated specification package written under the spec root.",
		InputDescription: "Exactly one of: a GitHub or GitLab issue or pull/merge-request URL (the issue and its comments are the idea), a path to a readable file (its contents are the idea), any other text (the text is the idea), or - to read the idea from stdin.",
		ExitCodes: map[int]string{
			toolio.ExitOK:     "a valid package was written",
			toolio.ExitFailed: "failed; the stage is named in the JSON, and an invalid_spec failure still leaves the package on disk",
			toolio.ExitUsage:  "usage error; nothing was fetched and nothing was written",
		},
		ResultSample: specgen.Result{},
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&f.specsDir, "specs-dir", "", "where NN_name packages live; default <dir>/"+specgen.DefaultSpecDirName+" or $"+specgen.SpecDirEnv)
			fs.StringVar(&f.name, "name", "", "override the spec name the model chooses; must match [a-z][a-z0-9_]*")
			fs.BoolVar(&f.architecture, "architecture", false, "also write the optional architecture.md")
			fs.BoolVar(&f.noActivate, "no-activate", false, "leave a valid package in draft instead of activating it")
			fs.BoolVar(&f.comment, "comment", false, "post the finished PRD back to the issue the input came from")
		},
		// Generating an artifact is one long structured answer plus however
		// many repairs the rules demand. The turn ceiling is the repair
		// budget, and 60 is enough for a model that is converging and a stop
		// for one that is not.
		DefaultBounds: agentrun.Bounds{MaxTurns: 60, MaxBudgetUSD: 5.00},
		// An input that does not split has one phase, so --total-budget lowers
		// its ceiling like issue's; a split also checks it between scopes.
		SinglePhase: true,

		PreCheck: func(c *toolio.Common) error {
			common = c
			if f.name != "" && !specgen.ValidSpecName(f.name) {
				return toolio.Usagef("--name %q must match [a-z][a-z0-9_]*", f.name)
			}
			return nil
		},
		CheckInput: func(in toolio.Input) error {
			if f.comment && !common.DryRun && in.Issue == nil {
				return toolio.Usagef("--comment posts the PRD back to the forge issue it came from (GitHub or GitLab), "+
					"and %s is %s", in.Origin, in.Kind)
			}
			return nil
		},

		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			result, err := specgen.Run(ctx, f.specOptions(d))
			if err != nil {
				info := toolio.ErrorFrom("run", err)
				return toolio.ExitCodeFor(info.Category), result, info
			}
			return toolio.ExitOK, result, nil
		},

		// --preflight: every check the ordinary run makes before its first
		// model call, against the identical options, then stop.
		PreflightExec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			result, err := specgen.RunPreflight(ctx, f.specOptions(d))
			if err != nil {
				info := toolio.ErrorFrom("run", err)
				return toolio.ExitCodeFor(info.Category), result, info
			}
			return toolio.ExitOK, result, nil
		},
	}
}

func main() {
	os.Exit(newApp().Main(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
