// Command spec turns a product idea into a complete, validated version 2
// specification package.
//
//	spec [flags] <text | file | github-url | ->
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
  a GitHub issue or pull-request URL   the issue and its comments are the idea
  a path to a readable file            the file's contents are the idea
  any other text                       the text is the idea
  -                                    the idea is read from stdin

Output is one JSON object on stdout; progress goes to stderr.

The run is unattended: nobody is waiting to answer questions, so the PRD phase
resolves every open question itself, records each in a '## Design Decisions'
section, and reports the ones it is least sure of as open_questions in the
result. It then generates requirements.json, test_spec.json and tasks.json in
that order — each validated against the format's schema and its cross-file
rules before it is written — writes the package under .specs/NN_name/, and
activates it if it validates.

Everything the model does here is read-only. The only thing written is the
spec package itself, by this program, after the run.

Exit codes:
  0  a valid package was written
  1  failed; the stage is named in the JSON. An 'invalid_spec' failure still
     leaves the package on disk with the broken rules named.
  2  usage error — nothing was fetched, nothing was written

Flags:
`

func main() {
	var (
		specsDir     string
		name         string
		architecture bool
		noActivate   bool
		comment      bool
		dryRun       bool
	)

	app := toolio.App{
		Name:    "spec",
		Version: agentfox.Version,
		Usage:   usage,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&specsDir, "specs-dir", "", "where NN_name packages live; default <dir>/"+specgen.DefaultSpecDirName+" or $"+specgen.SpecDirEnv)
			fs.StringVar(&name, "name", "", "override the spec name the model chooses; must match [a-z][a-z0-9_]*")
			fs.BoolVar(&architecture, "architecture", false, "also write the optional architecture.md")
			fs.BoolVar(&noActivate, "no-activate", false, "leave a valid package in draft instead of activating it")
			fs.BoolVar(&comment, "comment", false, "post the finished PRD back to the issue the input came from")
			fs.BoolVar(&dryRun, "dry-run", false, "write nothing to disk or GitHub; report the package that would be written")
		},
		// Generating an artifact is one long structured answer plus however
		// many repairs the rules demand. The turn ceiling is the repair
		// budget, and 60 is enough for a model that is converging and a stop
		// for one that is not.
		DefaultBounds: agentrun.Bounds{MaxTurns: 60, MaxBudgetUSD: 5.00},

		PreCheck: func(*toolio.Common) error {
			if name != "" && !specgen.ValidSpecName(name) {
				return toolio.Usagef("--name %q must match [a-z][a-z0-9_]*", name)
			}
			return nil
		},
		CheckInput: func(in toolio.Input) error {
			if comment && !dryRun && in.Issue == nil {
				return toolio.Usagef("--comment posts the PRD back to the issue it came from, "+
					"and %s is %s", in.Origin, in.Kind)
			}
			return nil
		},

		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			result, err := specgen.Run(ctx, specgen.Options{
				Input:        d.Input,
				Workspace:    d.Workspace,
				SpecsDir:     specsDir,
				Name:         name,
				Architecture: architecture,
				Activate:     !noActivate,
				Comment:      comment,
				DryRun:       dryRun,
				Runner:       d.Runner,
				GitHub:       d.GitHub,
				Run:          d.Run,
				Progress:     d.Progress,
			})
			if err != nil {
				info := toolio.ErrorFrom("run", err)
				return toolio.ExitCodeFor(info.Category), result, info
			}
			return toolio.ExitOK, result, nil
		},
	}

	os.Exit(app.Main(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
