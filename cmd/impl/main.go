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
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox"
	"github.com/agent-fox-dev/agentfox/codeimpl"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
	"github.com/agentfox/agentkit-go/catalog"
	"github.com/agentfox/agentkit-go/core"
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

A task that owns tests must show it worked test-first: submit_task is refused
unless every test verdict carries red_evidence (the command and the failure
seen before implementing) or the submission gives a test_first_deviation
reason, which the pull request shows as "Test-first not followed". The evidence
is the model's own claim, not a measured fact. --no-test-first stops the tool
requiring it; the prompt still asks for test-first work.

Checks that fail before any change are recorded and every task is judged by
comparison. --repair makes them a phase of their own instead, at the two
points where the whole suite is what matters: before the first task, when
the baseline is red, and after the integration task, when its checks fail.
The model fixes what fails, the checks run again, and the run goes on from
green — or, after --repair-attempts, the last attempt is parked and the run
exits 4. --repair-model runs that phase on another model, for a repository
whose failure needs more than the run's model. When landing with --land=pr,
a pull request or merge request is opened on the target forge (GitHub or
GitLab).

--dry-run makes no remote change: nothing is pushed, no pull request is opened.
It still does everything local — the branch is created and the commits are
made. --total-budget caps the spend of the whole run, checked between phases
and tasks.

Exit codes:
  0  every task landed, and the branch was landed as --land asked
  1  failed; the stage is named in the JSON
  2  usage error — nothing was fetched, nothing was written
  3  stopped on purpose: the spec cannot be implemented as written, or an
     upstream spec is not done. The question is in the JSON.
  4  a task was implemented and its checks do not pass — or, with --repair,
     the checks could not be repaired. The work is parked on the branch as
     a wip: commit and the checkout is back on the base branch.

Flags:
`

// implFlags holds the values of impl's own flags. Both the ordinary Exec and
// the --preflight PreflightExec read them through implOptions, so the two
// paths build identical codeimpl.Options and a check run under --preflight
// cannot pass against a configuration the real run would not use.
type implFlags struct {
	specsDir          string
	task              int
	branch            string
	repo              string
	land              string
	verify            string
	noVerify          bool
	verifyTimeout     time.Duration
	pushAttempts      int
	allow             string
	draft             bool
	pull              bool
	noSurvey          bool
	noTestFirst       bool
	attempts          int
	repair            bool
	repairTries       int
	repairModel       string
	repairModelEffort string
}

// implOptions builds the codeimpl.Options for one run from the parsed flags,
// what App.execute resolved and the repair runner (nil without --repair-model).
func (f *implFlags) implOptions(d toolio.Deps, repairRunner *agentrun.Runner) codeimpl.Options {
	mode, _ := codeimpl.ParseLandMode(f.land)
	target, _ := issuex.ParseRepo(f.repo)

	return codeimpl.Options{
		Input:          d.Input,
		Workspace:      d.Workspace,
		SpecsDir:       f.specsDir,
		Task:           f.task,
		Branch:         f.branch,
		Repo:           target,
		Land:           mode,
		DryRun:         d.Common.DryRun,
		VerifyCommand:  f.verify,
		NoVerify:       f.noVerify,
		VerifyTimeout:  f.verifyTimeout,
		PushAttempts:   f.pushAttempts,
		AllowPrograms:  splitList(f.allow),
		Draft:          f.draft,
		Pull:           f.pull,
		NoSurvey:       f.noSurvey,
		NoTestFirst:    f.noTestFirst,
		TaskAttempts:   f.attempts,
		TotalBudgetUSD: d.Common.TotalBudgetUSD,
		Repair:         f.repair,
		RepairAttempts: f.repairTries,
		RepairRunner:   repairRunner,
		Runner:         d.Runner,
		Forge:          d.Forge,
		CheckRunner:    gitx.ReducedEnvRunner,
		Run:            d.Run,
		Progress:       d.Progress,
	}
}

// resolveRepairRunner resolves --repair-model — and checks its credential —
// before anything runs, the way the run's own model is. Both Exec and
// PreflightExec call it. An empty model resolves to nothing: the repair phase
// then runs on the run's own runner.
func resolveRepairRunner(d toolio.Deps, repairModel, repairModelEffort string) (*agentrun.Runner, *toolio.ModelChoice, error) {
	if repairModel == "" && repairModelEffort == "" {
		return nil, nil, nil
	}

	var m *core.Model
	var tierThinking core.ThinkingLevel
	var spec string

	if repairModel != "" {
		// Resolve the repair model independently: the run's --effort and
		// $AF_MODEL_EFFORT do not carry over to a separately named repair model.
		var err error
		m, tierThinking, err = agentrun.ResolveModel(repairModel, d.Common.VendorName())
		if err != nil {
			return nil, nil, err
		}
		if err := agentrun.CheckCredentials(m); err != nil {
			return nil, nil, err
		}
		spec = repairModel
	} else {
		// No --repair-model: repair runs on the run's own model.
		m = d.Model.Model
		tierThinking = core.ThinkingUnset
		spec = d.Model.Spec
	}

	// Repair effort precedence:
	// (1) --repair-model-effort
	// (2) if --repair-model is given: that model's tier effort, otherwise
	//     the run's effective effort
	// (3) ThinkingUnset
	effective := core.ThinkingUnset
	switch {
	case repairModelEffort != "":
		effective = core.ThinkingLevel(repairModelEffort)
	case repairModel != "" && tierThinking != core.ThinkingUnset:
		effective = tierThinking
	case repairModel == "":
		// No --repair-model: inherit the run's effective effort.
		effective = d.Model.Thinking
	}

	choice := &toolio.ModelChoice{Model: m, Thinking: effective, Spec: spec}

	// Clamp the effort when it is set.
	if effective != core.ThinkingUnset {
		clamped, _, ok := catalog.ClampThinkingLevel(m, effective)
		if !ok {
			if repairModelEffort != "" {
				return nil, nil, toolio.Usagef("repair model %s supports no reachable thinking level for effort %q", m.ID, effective)
			}
			// Tier-sourced effort that cannot be clamped: fall back to unset.
			choice.Thinking = core.ThinkingUnset
		} else if clamped != effective {
			stage, _ := toolio.WarnStage(toolio.WarnEffortClamped)
			choice.Warnings = append(choice.Warnings, toolio.Warning{
				Code:     toolio.WarnEffortClamped,
				Severity: "low",
				Stage:    stage,
				Message:  fmt.Sprintf("repair: effort %s clamped to %s for model %s", effective, clamped, m.ID),
			})
			choice.Thinking = clamped
		}
	}

	// Build the runner from the run's configuration, overriding model and thinking.
	cfg := d.RunnerConfig()
	cfg.Model, cfg.Thinking = choice.Model, choice.Thinking
	r, err := agentrun.NewRunner(cfg)
	if err != nil {
		return nil, nil, err
	}
	d.Progress.Detail("repair model: %s (%s)", choice.Model.ID, choice.Model.Provider)
	// The repair phase's own warnings are recorded here, as the run's are for
	// its model, so every caller reports them (13-REQ-3.4).
	for _, w := range choice.Warnings {
		d.Run.Warn(w.Code, w.Severity, "%s", w.Message)
	}
	return r, choice, nil
}

// repairModelFailure is the exit code and envelope error for a repair model
// that could not be resolved, whose credential is missing, or whose effort it
// cannot serve. The last is a usage error, as it is for the run's own model.
func repairModelFailure(err error) (int, *toolio.ErrorInfo) {
	info := &toolio.ErrorInfo{Stage: "preflight", Category: agentrun.CategoryOf(err), Message: err.Error()}
	if info.Category == "usage" {
		info.Stage = "usage"
	}
	return toolio.ExitCodeFor(info.Category), info
}

func newApp() toolio.App {
	f := &implFlags{}
	var flags *flag.FlagSet

	return toolio.App{
		Name:             "impl",
		Version:          agentfox.Version,
		Usage:            usage,
		Description:      "Implements a specification package task by task on a branch, verifying each task with the spec's own checks, and lands the branch.",
		InputDescription: "Exactly one of: a spec directory, a spec id, name or directory name, a path to a file in the package, or - to read the reference from stdin. Issue URLs are refused.",
		ExitCodes: map[int]string{
			toolio.ExitOK:         "every task landed, and the branch was landed as --land asked",
			toolio.ExitFailed:     "failed; the stage is named in the JSON",
			toolio.ExitUsage:      "usage error; nothing was fetched and nothing was written",
			toolio.ExitNeedsHuman: "stopped on purpose because the spec cannot be implemented as written or an upstream spec is not done; the question is in the JSON",
			toolio.ExitUnverified: "a task was implemented and its checks do not pass, or could not be repaired; the work is parked on the branch as a wip: commit",
		},
		ResultSample: codeimpl.Result{},
		Flags: func(fs *flag.FlagSet) {
			flags = fs
			fs.StringVar(&f.specsDir, "specs-dir", "", "where NN_name packages live; default <dir>/"+codeimpl.DefaultSpecDirName+" or $"+codeimpl.SpecDirEnv)
			fs.IntVar(&f.task, "task", 0, "implement only this task id; default every task that is not done")
			fs.StringVar(&f.branch, "branch", "", "the branch to work on, created if missing and continued if present; default impl/<NN>-<slug>")
			fs.StringVar(&f.repo, "repo", "", "target repository as owner/repo or group/subgroup/project (GitHub or GitLab); default the origin remote of --dir")
			fs.StringVar(&f.land, "land", string(codeimpl.LandPR),
				"what to do once every task is done: "+strings.Join(codeimpl.LandModes, ", ")+"; default $"+landEnv+", else pr")
			toolio.DeclareEnum(fs, "land", codeimpl.LandModes)
			fs.StringVar(&f.verify, "verify", "", "one command that decides success, replacing the spec's linter and all_tests")
			fs.BoolVar(&f.noVerify, "no-verify", false, "run no checks; every task is then reported as unverified, not as a pass")
			fs.DurationVar(&f.verifyTimeout, "verify-timeout", checks.DefaultTimeout, "timeout for one check command")
			fs.IntVar(&f.pushAttempts, "push-attempts", 4, "push retries, with exponential backoff")
			fs.StringVar(&f.allow, "allow", "", "comma-separated extra programs the implementation phases' shell may run")
			fs.BoolVar(&f.draft, "draft", false, "open the pull request as a draft")
			fs.BoolVar(&f.pull, "pull", false, "checkout and pull the base branch from origin before anything else")
			fs.BoolVar(&f.noSurvey, "no-survey", false, "skip the read-only survey phase")
			fs.BoolVar(&f.noTestFirst, "no-test-first", false, "do not require red-first evidence (red_evidence or test_first_deviation) when a task is submitted")
			fs.IntVar(&f.attempts, "task-attempts", codeimpl.DefaultTaskAttempts, "implementation attempts per task before the run parks")
			fs.BoolVar(&f.repair, "repair", false, "repair the checks when they fail before the first task or after the integration task; the run stops if they cannot be repaired")
			fs.IntVar(&f.repairTries, "repair-attempts", codeimpl.DefaultRepairAttempts, "repair attempts before the run gives up")
			fs.StringVar(&f.repairModel, "repair-model", "", "model tier or catalog spec for the repair phase alone; implies --repair. Default the run's model")
			toolio.RegisterEffortFlag(fs, &f.repairModelEffort, "repair-model-effort",
				"reasoning effort for the repair phase: "+strings.Join(toolio.EffortValues(), ", ")+"; implies --repair. Default the repair model's tier effort")
		},
		// A task is roughly a fix's worth of work, and there are several of
		// them: the per-phase ceilings are fix's, and --total-budget caps the
		// run. Both numbers are ceilings, not targets.
		DefaultBounds: agentrun.Bounds{MaxTurns: 150, MaxBudgetUSD: 5.00},

		PreCheck: func(*toolio.Common) error {
			applyEnvDefaults(flags, map[string]*string{"land": &f.land})
			if _, ok := codeimpl.ParseLandMode(f.land); !ok {
				return toolio.Usagef("--land %q is not one of %s", f.land, strings.Join(codeimpl.LandModes, ", "))
			}
			if f.repo != "" {
				if _, ok := issuex.ParseRepo(f.repo); !ok {
					return toolio.Usagef("--repo %q cannot be parsed as a repository identifier", f.repo)
				}
			}
			if f.noVerify && f.verify != "" {
				return toolio.Usagef("--no-verify and --verify cannot both be given")
			}
			if f.task < 0 {
				return toolio.Usagef("--task %d is not a task id", f.task)
			}
			if f.attempts < 1 {
				return toolio.Usagef("--task-attempts must be at least 1")
			}
			if f.repairModel != "" || f.repairModelEffort != "" {
				f.repair = true
			}
			if f.repair && f.noVerify {
				return toolio.Usagef("--repair and --no-verify cannot both be given: nothing runs, so nothing can be repaired")
			}
			if f.repairTries < 1 {
				return toolio.Usagef("--repair-attempts must be at least 1")
			}
			return nil
		},
		CheckInput: func(in toolio.Input) error {
			if in.Kind == toolio.KindIssue {
				return toolio.Usagef("impl takes a spec package, not an issue (GitHub or GitLab): give a spec directory, id or name")
			}
			return nil
		},

		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			repairRunner, _, err := resolveRepairRunner(d, f.repairModel, f.repairModelEffort)
			if err != nil {
				code, info := repairModelFailure(err)
				return code, nil, info
			}

			result, err := codeimpl.Run(ctx, f.implOptions(d, repairRunner))
			if err != nil {
				info := toolio.ErrorFrom("run", err)
				return toolio.ExitCodeFor(info.Category), result, info
			}
			return toolio.ExitOK, result, nil
		},

		// --preflight: every check the ordinary run makes before its first
		// model call, against the identical options and the identically
		// resolved repair model, then stop.
		PreflightExec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			repairRunner, _, err := resolveRepairRunner(d, f.repairModel, f.repairModelEffort)
			if err != nil {
				code, info := repairModelFailure(err)
				return code, nil, info
			}

			result, err := codeimpl.RunPreflight(ctx, f.implOptions(d, repairRunner))
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

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// landEnv sets the default for --land, so a repository or a shell can say once
// that a run should not leave the machine (AF_LAND=none).
const landEnv = "AF_LAND"

// envDefaults maps a flag to the environment variable that supplies its value
// when the flag was not given. The flag's own default stays fixed, so --schema
// does not depend on the environment.
var envDefaults = map[string]string{"land": landEnv}

// applyEnvDefaults sets each flag the command line left alone from its
// environment variable, when that is set. An invalid value is then refused by
// the same check as a flag, so a mistyped AF_LAND=nnone cannot fall back to
// pushing.
func applyEnvDefaults(fs *flag.FlagSet, targets map[string]*string) {
	if fs == nil {
		return
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	for name, dst := range targets {
		if given[name] {
			continue
		}
		if v := os.Getenv(envDefaults[name]); v != "" {
			*dst = v
		}
	}
}
