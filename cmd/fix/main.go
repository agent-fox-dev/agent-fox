// Command fix diagnoses a problem against a repository, implements the fix on
// a branch, verifies it with the project's own checks, and lands it.
//
//	fix [flags] <text | file | github-url | ->
//
// It takes exactly one input and writes exactly one JSON object to stdout.
// Progress goes to stderr, so the two never interleave.
package main

import (
	"context"
	"flag"
	"os"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox"
	"github.com/agent-fox-dev/agentfox/codefix"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/ghapi"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

const usage = `fix — diagnose a problem, implement it, verify it, land it

Usage:
  fix [flags] <input>

The input is exactly one of:
  a GitHub issue or pull-request URL   the issue is the problem; the run
                                       comments on it and the pull request
                                       closes it
  a path to a readable file            the file's contents are the problem
  any other text                       the text is the problem
  -                                    the problem is read from stdin

Output is one JSON object on stdout; progress goes to stderr.

The working tree must be clean. The run branches from the current branch,
writes the change, runs the project's own checks, and only lands the work if
they pass — comparing against a baseline taken before anything changed, so a
repository that was already failing is reported honestly rather than as a
regression.

Exit codes:
  0  fixed, verified, and landed as --land asked
  1  failed; the stage is named in the JSON
  2  usage error — nothing was fetched, nothing was written
  3  stopped on purpose: the problem reads two ways, and the question was
     posted to the issue. No branch, no code.
  4  code was written and the checks do not pass. The work is committed on
     the branch as a wip: commit and the checkout is back on the base branch.

Flags:
`

func main() {
	var (
		repo          string
		land          string
		dryRun        bool
		verify        string
		noVerify      bool
		verifyTimeout time.Duration
		pushAttempts  int
		allow         string
		draft         bool
	)

	app := toolio.App{
		Name:    "fix",
		Version: agentfox.Version,
		Usage:   usage,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "target repository as owner/repo; default the input issue's, else the origin remote of --dir")
			fs.StringVar(&land, "land", string(codefix.LandPR), "what to do with a verified change: "+strings.Join(codefix.LandModes, ", "))
			fs.BoolVar(&dryRun, "dry-run", false, "make no remote change: push nothing, open nothing, post nothing")
			fs.StringVar(&verify, "verify", "", "the command that decides success; default detected from the project")
			fs.BoolVar(&noVerify, "no-verify", false, "run no checks; the result is then reported as unverified, not as a pass")
			fs.DurationVar(&verifyTimeout, "verify-timeout", checks.DefaultTimeout, "timeout for one verification run")
			fs.IntVar(&pushAttempts, "push-attempts", 4, "push retries, with exponential backoff")
			fs.StringVar(&allow, "allow", "", "comma-separated extra programs the implementation phase's shell may run")
			fs.BoolVar(&draft, "draft", false, "open the pull request as a draft")
		},
		// Implementing is the expensive phase: it reads, writes, and runs the
		// suite repeatedly. Both numbers are ceilings, not targets.
		DefaultBounds: agentrun.Bounds{MaxTurns: 150, MaxBudgetUSD: 5.00},

		PreCheck: func(*toolio.Common) error {
			if _, ok := codefix.ParseLandMode(land); !ok {
				return toolio.Usagef("--land %q is not one of %s", land, strings.Join(codefix.LandModes, ", "))
			}
			if repo != "" {
				if _, ok := ghapi.ParseRepo(repo); !ok {
					return toolio.Usagef("--repo %q is not owner/repo", repo)
				}
			}
			if noVerify && verify != "" {
				return toolio.Usagef("--no-verify and --verify cannot both be given")
			}
			return nil
		},

		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			mode, _ := codefix.ParseLandMode(land)
			target, _ := ghapi.ParseRepo(repo)

			result, err := codefix.Run(ctx, codefix.Options{
				Input:         d.Input,
				Workspace:     d.Workspace,
				Repo:          target,
				Land:          mode,
				DryRun:        dryRun,
				VerifyCommand: verify,
				NoVerify:      noVerify,
				VerifyTimeout: verifyTimeout,
				PushAttempts:  pushAttempts,
				AllowPrograms: splitList(allow),
				Draft:         draft,
				Runner:        d.Runner,
				GitHub:        d.GitHub,
				CheckRunner:   gitx.ReducedEnvRunner,
				Run:           d.Run,
				Progress:      d.Progress,
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

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
