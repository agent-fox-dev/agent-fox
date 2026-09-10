// Command impl implements a specification package in a repository: one
// model phase per task, each verified by the project's own checks before it
// is committed, on one branch that is then landed.
//
//	impl [flags] <spec-dir | NN | name | NN_name | path to a spec file | ->
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
	"github.com/agent-fox-dev/agentfox/codeimpl"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/ghapi"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

const usage = `impl — implement a specification, task by task, verified, on a branch

Usage:
  impl [flags] <input>

The input names one spec package under the spec root (--specs-dir, else
$AF_SPEC_DIR, else <dir>/.specs). It is exactly one of:
  a spec directory                     .specs/09_agent_mode
  a spec id, name or directory name    09, agent_mode, 09_agent_mode
  a path to a file in the package      .specs/09_agent_mode/tasks.json
  -                                    the reference is read from stdin

Output is one JSON object on stdout; progress goes to stderr.

The working tree must be clean and the package must validate; a draft is
implemented with a warning, a sealed one is refused.
The run works on impl/<NN>-<slug> — continuing it when it already exists —
surveys the repository against the spec once, then implements every task
that is not done, in the plan's order: one model phase per task, the spec's
own linter and all_tests run before and after, and the task is committed
with its state only when the comparison is landable. A task that fails is
tried once more from the last commit; a second failure parks the work as a
wip: commit and returns the checkout to the base branch. A second run
continues from the last landed task.

Exit codes:
  0  every task landed, and the branch was landed as --land asked
  1  failed; the stage is named in the JSON
  2  usage error — nothing was fetched, nothing was written
  3  stopped on purpose: the spec cannot be implemented as written, or an
     upstream spec is not done. The question is in the JSON.
  4  a task was implemented and its checks do not pass. The work is parked
     on the branch as a wip: commit and the checkout is back on the base
     branch.

Flags:
`

func main() {
	var (
		specsDir      string
		task          int
		branch        string
		repo          string
		land          string
		dryRun        bool
		verify        string
		noVerify      bool
		verifyTimeout time.Duration
		pushAttempts  int
		allow         string
		draft         bool
		pull          bool
		noSurvey      bool
		attempts      int
		totalBudget   float64
	)

	app := toolio.App{
		Name:    "impl",
		Version: agentfox.Version,
		Usage:   usage,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&specsDir, "specs-dir", "", "where NN_name packages live; default <dir>/"+codeimpl.DefaultSpecDirName+" or $"+codeimpl.SpecDirEnv)
			fs.IntVar(&task, "task", 0, "implement only this task id; default every task that is not done")
			fs.StringVar(&branch, "branch", "", "the branch to work on, created if missing and continued if present; default impl/<NN>-<slug>")
			fs.StringVar(&repo, "repo", "", "target repository as owner/repo; default the origin remote of --dir")
			fs.StringVar(&land, "land", string(codeimpl.LandPR), "what to do once every task is done: "+strings.Join(codeimpl.LandModes, ", "))
			fs.BoolVar(&dryRun, "dry-run", false, "make no remote change: push nothing, open nothing")
			fs.StringVar(&verify, "verify", "", "one command that decides success, replacing the spec's linter and all_tests")
			fs.BoolVar(&noVerify, "no-verify", false, "run no checks; every task is then reported as unverified, not as a pass")
			fs.DurationVar(&verifyTimeout, "verify-timeout", checks.DefaultTimeout, "timeout for one check command")
			fs.IntVar(&pushAttempts, "push-attempts", 4, "push retries, with exponential backoff")
			fs.StringVar(&allow, "allow", "", "comma-separated extra programs the implementation phases' shell may run")
			fs.BoolVar(&draft, "draft", false, "open the pull request as a draft")
			fs.BoolVar(&pull, "pull", false, "checkout and pull the base branch from origin before anything else")
			fs.BoolVar(&noSurvey, "no-survey", false, "skip the read-only survey phase")
			fs.IntVar(&attempts, "task-attempts", codeimpl.DefaultTaskAttempts, "implementation attempts per task before the run parks")
			fs.Float64Var(&totalBudget, "total-budget", 0, "spend ceiling for the whole run, in dollars; 0 means only the per-phase bound")
		},
		// A task is roughly a fix's worth of work, and there are several of
		// them: the per-phase ceilings are fix's, and --total-budget caps the
		// run. Both numbers are ceilings, not targets.
		DefaultBounds: agentrun.Bounds{MaxTurns: 150, MaxBudgetUSD: 5.00},

		PreCheck: func(*toolio.Common) error {
			if _, ok := codeimpl.ParseLandMode(land); !ok {
				return toolio.Usagef("--land %q is not one of %s", land, strings.Join(codeimpl.LandModes, ", "))
			}
			if repo != "" {
				if _, ok := ghapi.ParseRepo(repo); !ok {
					return toolio.Usagef("--repo %q is not owner/repo", repo)
				}
			}
			if noVerify && verify != "" {
				return toolio.Usagef("--no-verify and --verify cannot both be given")
			}
			if task < 0 {
				return toolio.Usagef("--task %d is not a task id", task)
			}
			if attempts < 1 {
				return toolio.Usagef("--task-attempts must be at least 1")
			}
			if totalBudget < 0 {
				return toolio.Usagef("--total-budget cannot be negative")
			}
			return nil
		},
		CheckInput: func(in toolio.Input) error {
			if in.Kind == toolio.KindIssue {
				return toolio.Usagef("impl takes a spec package, not an issue: give a spec directory, id or name")
			}
			return nil
		},

		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			mode, _ := codeimpl.ParseLandMode(land)
			target, _ := ghapi.ParseRepo(repo)

			result, err := codeimpl.Run(ctx, codeimpl.Options{
				Input:          d.Input,
				Workspace:      d.Workspace,
				SpecsDir:       specsDir,
				Task:           task,
				Branch:         branch,
				Repo:           target,
				Land:           mode,
				DryRun:         dryRun,
				VerifyCommand:  verify,
				NoVerify:       noVerify,
				VerifyTimeout:  verifyTimeout,
				PushAttempts:   pushAttempts,
				AllowPrograms:  splitList(allow),
				Draft:          draft,
				Pull:           pull,
				NoSurvey:       noSurvey,
				TaskAttempts:   attempts,
				TotalBudgetUSD: totalBudget,
				Runner:         d.Runner,
				GitHub:         d.GitHub,
				CheckRunner:    gitx.ReducedEnvRunner,
				Run:            d.Run,
				Progress:       d.Progress,
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
