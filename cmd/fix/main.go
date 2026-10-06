// Command fix diagnoses a problem against a repository, implements the fix on
// a branch, verifies it with the project's own checks, and lands it.
//
//	fix [flags] <text | file | issue-url | ->
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
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

const usage = `fix — diagnose a problem, implement it, verify it, land it

Usage:
  fix [flags] <input>

The input is exactly one of:
  a GitHub or GitLab issue or pull/merge-request URL
                                       the issue is the problem; the run
                                       comments on it and the pull request
                                       closes it, when the fix is proven
  a path to a readable file            the file's contents are the problem
  any other text                       the text is the problem
  -                                    the problem is read from stdin

Output is one JSON object on stdout; progress goes to stderr.

The working tree must be clean. The run branches from the current branch
(or the branch updated via -pull), writes the change, runs the project's own
checks, and only lands the work if they pass — comparing against a baseline
taken before anything changed, so a repository that was already failing is
reported honestly rather than as a regression.

A verified change is then proven before it is committed: the checks run once
more with its non-test files taken out and its tests left in. The issue is
closed (Closes #N) only when they then fail; a fix whose tests pass without it
only references the issue (Refs #N) and the run warns fix_not_proven. When the
report cites requirement or test ids a spec under .specs (or $AF_SPEC_DIR)
defines, an independent review on a fresh context checks the change against
them; a blocking finding keeps the issue open, makes the pull request a
draft, and the run exits 4. --no-review skips that review. Structural checks
over the touched files are reported in the pull request.

--dry-run makes no remote change: nothing is pushed, no pull request is opened,
no comment is posted. It still does everything local — the branch is created and
the commits are made. --total-budget is checked between the analyse and the
implement phases; a run already over it stops before the implementation starts.

Exit codes:
  0  fixed, verified, and landed as --land asked
  1  failed; the stage is named in the JSON
  2  usage error — nothing was fetched, nothing was written
  3  stopped on purpose: the problem reads two ways, and the question was
     posted to the issue. No branch, no code.
  4  code was written and the checks do not pass. The work is committed on
     the branch as a wip: commit and the checkout is back on the base branch.
     Also: the change landed and the review of the requirements the report
     cites found them unmet (category nonconformant).

Flags:
`

// fixFlags holds the values of fix's own flags. Both the ordinary Exec and
// the --preflight PreflightExec read them through fixOptions, so the two
// paths build identical codefix.Options and a check run under --preflight
// cannot pass against a configuration the real run would not use.
type fixFlags struct {
	repo          string
	land          string
	branchPrefix  string
	verify        string
	noVerify      bool
	verifyTimeout time.Duration
	pushAttempts  int
	allow         string
	draft         bool
	pull          pullFlag
	noReview      bool
}

// fixOptions builds the codefix.Options for one run from the parsed flags and
// what App.execute resolved.
func (f *fixFlags) fixOptions(d toolio.Deps) codefix.Options {
	mode, _ := codefix.ParseLandMode(f.land)
	target, _ := issuex.ParseRepo(f.repo)

	return codefix.Options{
		Input:          d.Input,
		Workspace:      d.Workspace,
		Repo:           target,
		Land:           mode,
		BranchPrefix:   f.branchPrefix,
		DryRun:         d.Common.DryRun,
		TotalBudgetUSD: d.Common.TotalBudgetUSD,
		VerifyCommand:  f.verify,
		NoVerify:       f.noVerify,
		VerifyTimeout:  f.verifyTimeout,
		PushAttempts:   f.pushAttempts,
		AllowPrograms:  splitList(f.allow),
		Draft:          f.draft,
		Pull:           f.pull.set,
		PullBranch:     f.pull.branch,
		NoReview:       f.noReview,
		Runner:         d.Runner,
		Forge:          d.Forge,
		CheckRunner:    gitx.ReducedEnvRunner,
		Run:            d.Run,
		Progress:       d.Progress,

		RepoMapTokens: d.Common.RepoMapTokens,
	}
}

func newApp() toolio.App {
	f := &fixFlags{}
	var flags *flag.FlagSet

	return toolio.App{
		Name: "fix",

		Version:          agentfox.Version,
		Usage:            usage,
		Description:      "Diagnoses a problem, writes the change on a branch, verifies it with the project's own checks, and lands it.",
		InputDescription: "Exactly one of: a GitHub or GitLab issue or pull/merge-request URL (the issue is the problem), a path to a readable file (its contents are the problem), any other text (the text is the problem), or - to read the problem from stdin.",
		ExitCodes: map[int]string{
			toolio.ExitOK:         "fixed, verified, and landed as --land asked",
			toolio.ExitFailed:     "failed; the stage is named in the JSON",
			toolio.ExitUsage:      "usage error; nothing was fetched and nothing was written",
			toolio.ExitNeedsHuman: "stopped on purpose because the problem reads two ways; the question was posted to the issue, with no branch and no code",
			toolio.ExitUnverified: "code was written and the checks do not pass; the work is committed as a wip: commit and the checkout is back on the base branch",
		},
		ResultSample: codefix.Result{},
		Flags: func(fs *flag.FlagSet) {
			flags = fs
			fs.StringVar(&f.repo, "repo", "", "target repository as owner/repo or group/subgroup/project; default the origin remote of --dir, else the input issue's")
			fs.StringVar(&f.land, "land", string(codefix.LandPR),
				"what to do with a verified change: "+strings.Join(codefix.LandModes, ", ")+"; default $"+landEnv+", else pr")
			toolio.DeclareEnum(fs, "land", codefix.LandModes)
			fs.StringVar(&f.branchPrefix, "branch-prefix", "",
				"first segments of the branch name, e.g. feature; default $"+codefix.BranchPrefixEnv+", else fix for a bug and feature otherwise")
			fs.StringVar(&f.verify, "verify", "", "the command that decides success; default detected from the project")
			fs.BoolVar(&f.noVerify, "no-verify", false, "run no checks; the result is then reported as unverified, not as a pass")
			fs.DurationVar(&f.verifyTimeout, "verify-timeout", checks.DefaultTimeout, "timeout for one verification run")
			fs.IntVar(&f.pushAttempts, "push-attempts", 4, "push retries, with exponential backoff")
			fs.StringVar(&f.allow, "allow", "", "comma-separated extra programs the implementation phase's shell may run")
			fs.BoolVar(&f.draft, "draft", false, "open the pull request as a draft")
			fs.Var(&f.pull, "pull", "checkout and pull origin before branching; optional branch name, default origin's default branch")
			fs.BoolVar(&f.noReview, "no-review", false, "skip the independent review of the change against the requirement and test ids the report cites")
		},
		// Implementing is the expensive phase: it reads, writes, and runs the
		// suite repeatedly. Both numbers are ceilings, not targets.
		DefaultBounds: agentrun.Bounds{MaxTurns: 150, MaxBudgetUSD: 5.00},

		PreCheck: func(*toolio.Common) error {
			applyEnvDefaults(flags, map[string]*string{"land": &f.land, "branch-prefix": &f.branchPrefix})
			if _, ok := codefix.ParseLandMode(f.land); !ok {
				return toolio.Usagef("--land %q is not one of %s", f.land, strings.Join(codefix.LandModes, ", "))
			}
			if f.branchPrefix != "" && !codefix.ValidBranchPrefix(f.branchPrefix) {
				return toolio.Usagef("--branch-prefix %q is not a valid branch prefix: use slash-separated words of "+
					"letters, digits, dot, underscore and dash", f.branchPrefix)
			}
			if f.repo != "" {
				if _, ok := issuex.ParseRepo(f.repo); !ok {
					return toolio.Usagef("--repo %q cannot be parsed as a repository identifier", f.repo)
				}
			}
			if f.noVerify && f.verify != "" {
				return toolio.Usagef("--no-verify and --verify cannot both be given")
			}
			return nil
		},

		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			result, err := codefix.Run(ctx, f.fixOptions(d))
			if err != nil {
				info := toolio.ErrorFrom("run", err)
				return toolio.ExitCodeFor(info.Category), result, info
			}
			return toolio.ExitOK, result, nil
		},

		// --preflight: every check the ordinary run makes before its first
		// model call, against the identical options, then stop.
		PreflightExec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			result, err := codefix.RunPreflight(ctx, f.fixOptions(d))
			if err != nil {
				info := toolio.ErrorFrom("run", err)
				return toolio.ExitCodeFor(info.Category), result, info
			}
			return toolio.ExitOK, result, nil
		},
	}
}

func main() {
	os.Exit(newApp().Main(context.Background(), normalizeArgs(os.Args[1:]), os.Stdin, os.Stdout, os.Stderr))
}

type pullFlag struct {
	set    bool
	branch string
}

func (p *pullFlag) String() string {
	if !p.set {
		return ""
	}
	if p.branch != "" {
		return p.branch
	}
	return "true"
}

func (p *pullFlag) Set(v string) error {
	p.set = true
	if v == "" || v == "true" {
		p.branch = ""
		return nil
	}
	if v == "false" {
		p.set = false
		p.branch = ""
		return nil
	}
	p.branch = v
	return nil
}

func (p *pullFlag) IsBoolFlag() bool {
	return true
}

// knownFixValueFlags lists flags that take a separate value token.
var knownFixValueFlags = map[string]bool{
	"repo":            true,
	"land":            true,
	"branch-prefix":   true,
	"verify":          true,
	"verify-timeout":  true,
	"push-attempts":   true,
	"allow":           true,
	"dir":             true,
	"model":           true,
	"vendor":          true,
	"effort":          true,
	"max-turns":       true,
	"budget":          true,
	"phase-timeout":   true,
	"input-kind":      true,
	"total-budget":    true,
	"detail":          true,
	"report-file":     true,
	"output":          true,
	"context":         true,
	"repo-map-tokens": true,
}

// normalizeArgs rewrites argv so that "-pull [branch]" doesn't cause the branch
// or the input to be parsed incorrectly when -pull is passed without "=".
// Specifically, if -pull is followed by a non-flag argument and there is at least
// one subsequent positional argument, the argument immediately following -pull
// is treated as the branch value and merged into -pull=<branch>.
func normalizeArgs(argv []string) []string {
	// First, identify non-flag tokens and where -pull occurs.
	// We need to know which tokens are values for flags vs actual positional args.
	var nonFlags []int
	pullIndices := make(map[int]string) // index of pull token -> flag prefix ("-pull" or "--pull")

	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			for j := i + 1; j < len(argv); j++ {
				nonFlags = append(nonFlags, j)
			}
			break
		}
		if a == "-pull" || a == "--pull" {
			pullIndices[i] = a
			continue
		}
		if strings.HasPrefix(a, "-") {
			name := strings.TrimLeft(a, "-")
			if idx := strings.Index(name, "="); idx != -1 {
				name = name[:idx]
			} else if knownFixValueFlags[name] && i+1 < len(argv) {
				i++ // skip flag's value argument
			}
			continue
		}
		nonFlags = append(nonFlags, i)
	}

	if len(pullIndices) == 0 {
		return argv
	}

	// For each pull flag without '=', check if the immediately next token is a non-flag,
	// and if there is at least one OTHER non-flag token (the positional input).
	pullCombines := make(map[int]int) // pull index -> value index to combine
	for pIdx := range pullIndices {
		valIdx := pIdx + 1
		if valIdx < len(argv) && !strings.HasPrefix(argv[valIdx], "-") {
			// Check if valIdx is in nonFlags
			isNonFlag := false
			for _, nf := range nonFlags {
				if nf == valIdx {
					isNonFlag = true
					break
				}
			}
			// If it's a non-flag and there are > 1 nonFlags total, this non-flag is the branch!
			if isNonFlag && len(nonFlags) > 1 {
				pullCombines[pIdx] = valIdx
			}
		}
	}

	if len(pullCombines) == 0 {
		return argv
	}

	out := make([]string, 0, len(argv))
	skipNext := false
	for i := 0; i < len(argv); i++ {
		if skipNext {
			skipNext = false
			continue
		}
		if valIdx, ok := pullCombines[i]; ok {
			prefix := pullIndices[i]
			out = append(out, prefix+"="+argv[valIdx])
			skipNext = true
			continue
		}
		out = append(out, argv[i])
	}
	return out
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
var envDefaults = map[string]string{"land": landEnv, "branch-prefix": codefix.BranchPrefixEnv}

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
