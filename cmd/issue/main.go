// Command issue triages a problem report against a codebase and files a
// structured issue on GitHub or GitLab.
//
//	issue [flags] <text | file | issue-url | ->
//
// It takes exactly one input and writes exactly one JSON object to stdout.
// Progress goes to stderr, so the two never interleave.
package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"strings"

	"github.com/agent-fox-dev/agentfox"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuetriage"
	"github.com/agent-fox-dev/agentfox/issuex"
)

const usage = `issue — triage a problem report and file an issue on GitHub or GitLab

Usage:
  issue [flags] <input>

The input is exactly one of:
  a GitHub or GitLab issue or pull/merge-request URL
                                       the issue and its comments are the report
  a path to a readable file            the file's contents are the report
  any other text                       the text is the report
  -                                    the report is read from stdin

Output is one JSON object on stdout; progress goes to stderr.

The analysis is read-only: the tools it has cannot write a file, run a
command, or reach the network. The issue is filed on GitHub or GitLab by this
program after the run, and --dry-run suppresses that.

Exit codes:
  0  triaged, and filed unless --dry-run
  1  failed
  2  usage error — nothing was fetched, nothing was written

Flags:
`

func main() {
	var (
		repo      string
		labels    string
		dryRun    bool
		overwrite bool
	)

	app := toolio.App{
		Name:    "issue",
		Version: agentfox.Version,
		Usage:   usage,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "target repository as owner/repo or group/subgroup/project; default the input issue's, else the origin remote of --dir")
			fs.StringVar(&labels, "label", "", "comma-separated labels for the created issue, e.g. af:fix")
			fs.BoolVar(&dryRun, "dry-run", false, "make no change on GitHub or GitLab; report the diagnosis only")
			fs.BoolVar(&overwrite, "overwrite", false, "rewrite the input issue in place instead of creating a new one")
		},
		// A triage reads: 100 turns is a lot of files, and $2 is more than
		// any single diagnosis has cost. Both are ceilings, not targets.
		DefaultBounds: agentrun.Bounds{MaxTurns: 100, MaxBudgetUSD: 2.00},

		PreCheck: func(*toolio.Common) error {
			if overwrite && (repo != "" || labels != "") {
				return toolio.Usagef("--overwrite rewrites the issue it was given, so it " +
					"cannot be combined with --repo or --label")
			}
			if repo != "" {
				if _, ok := issuex.ParseRepo(repo); !ok {
					return toolio.Usagef("--repo %q cannot be parsed as a repository identifier", repo)
				}
			}
			return nil
		},
		CheckInput: func(in toolio.Input) error {
			if overwrite && in.Issue == nil {
				return toolio.Usagef("--overwrite needs a forge issue URL (GitHub or GitLab) as the input; "+
					"%s is %s", in.Origin, in.Kind)
			}
			return nil
		},

		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			target, _ := issuex.ParseRepo(repo)
			result, err := issuetriage.Run(ctx, issuetriage.Options{
				Input:     d.Input,
				Workspace: d.Workspace,
				Repo:      target,
				Labels:    splitLabels(labels),
				DryRun:    dryRun,
				Overwrite: overwrite,
				Runner:    d.Runner,
				Forge:     d.Forge,
				Run:       d.Run,
				Progress:  d.Progress,
			})
			if err != nil {
				var f *issuetriage.Failure
				info := toolio.ErrorFrom("run", err)
				if errors.As(err, &f) {
					info.Stage, info.Category = f.Stage, f.Category
				}
				return toolio.ExitCodeFor(info.Category), result, info
			}
			return toolio.ExitOK, result, nil
		},
	}

	os.Exit(app.Main(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func splitLabels(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
