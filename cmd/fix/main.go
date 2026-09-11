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
                                       closes it
  a path to a readable file            the file's contents are the problem
  any other text                       the text is the problem
  -                                    the problem is read from stdin

Output is one JSON object on stdout; progress goes to stderr.

The working tree must be clean. The run branches from the current branch
(or the branch updated via -pull), writes the change, runs the project's own
checks, and only lands the work if they pass — comparing against a baseline
taken before anything changed, so a repository that was already failing is
reported honestly rather than as a regression.

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
		pull          pullFlag
	)

	app := toolio.App{
		Name:    "fix",
		Version: agentfox.Version,
		Usage:   usage,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "target repository as owner/repo or group/subgroup/project; default the input issue's, else the origin remote of --dir")
			fs.StringVar(&land, "land", string(codefix.LandPR), "what to do with a verified change: "+strings.Join(codefix.LandModes, ", "))
			fs.BoolVar(&dryRun, "dry-run", false, "make no remote change: push nothing, open nothing, post nothing")
			fs.StringVar(&verify, "verify", "", "the command that decides success; default detected from the project")
			fs.BoolVar(&noVerify, "no-verify", false, "run no checks; the result is then reported as unverified, not as a pass")
			fs.DurationVar(&verifyTimeout, "verify-timeout", checks.DefaultTimeout, "timeout for one verification run")
			fs.IntVar(&pushAttempts, "push-attempts", 4, "push retries, with exponential backoff")
			fs.StringVar(&allow, "allow", "", "comma-separated extra programs the implementation phase's shell may run")
			fs.BoolVar(&draft, "draft", false, "open the pull request as a draft")
			fs.Var(&pull, "pull", "checkout and pull origin before branching; optional branch name, default origin's default branch")
		},
		// Implementing is the expensive phase: it reads, writes, and runs the
		// suite repeatedly. Both numbers are ceilings, not targets.
		DefaultBounds: agentrun.Bounds{MaxTurns: 150, MaxBudgetUSD: 5.00},

		PreCheck: func(*toolio.Common) error {
			if _, ok := codefix.ParseLandMode(land); !ok {
				return toolio.Usagef("--land %q is not one of %s", land, strings.Join(codefix.LandModes, ", "))
			}
			if repo != "" {
				if _, ok := issuex.ParseRepo(repo); !ok {
					return toolio.Usagef("--repo %q cannot be parsed as a repository identifier", repo)
				}
			}
			if noVerify && verify != "" {
				return toolio.Usagef("--no-verify and --verify cannot both be given")
			}
			return nil
		},

		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			mode, _ := codefix.ParseLandMode(land)
			target, _ := issuex.ParseRepo(repo)

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
				Pull:          pull.set,
				PullBranch:    pull.branch,
				Runner:        d.Runner,
				Forge:         d.Forge,
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

	os.Exit(app.Main(context.Background(), normalizeArgs(os.Args[1:]), os.Stdin, os.Stdout, os.Stderr))
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
	"repo":           true,
	"land":           true,
	"verify":         true,
	"verify-timeout": true,
	"push-attempts":  true,
	"allow":          true,
	"dir":            true,
	"model":          true,
	"vendor":         true,
	"variant":        true,
	"max-turns":      true,
	"budget":         true,
	"phase-timeout":  true,
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
	for pIdx, prefix := range pullIndices {
		_ = prefix
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
