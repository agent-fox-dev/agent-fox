// Command triage triages a problem report against a codebase and files a
// structured issue on GitHub or GitLab.
//
//	triage [flags] <text | file | issue-url | ->
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
	"github.com/agentfox/agentkit-go/codesearch"
	"github.com/agentfox/agentkit-go/tools"
)

const usage = `triage — triage a problem report and file an issue on GitHub or GitLab

Usage:
  triage [flags] <input>

The input is exactly one of:
  a GitHub or GitLab issue or pull/merge-request URL
                                       the issue and its comments are the report
  a path to a readable file            the file's contents are the report
  any other text                       the text is the report
  -                                    the report is read from stdin

Output is one JSON object on stdout; progress goes to stderr.

The analysis is read-only: the tools it has cannot write a file, run a
command, or reach the network. The issue is filed on GitHub or GitLab by this
program after the run, and --dry-run suppresses that: it makes no remote
change, and triage has nothing else to do locally, so it does nothing at all.
--total-budget and --budget bound the same spend here (one phase); the lower
of the two applies.

Exit codes:
  0  triaged, and filed unless --dry-run
  1  failed
  2  usage error — nothing was fetched, nothing was written

Flags:
`

// triageFlags holds the values of triage's own flags. Both the ordinary Exec
// and the --preflight PreflightExec read them through triageOptions, so the
// two paths build identical issuetriage.Options.
type triageFlags struct {
	repo      string
	labels    string
	overwrite bool
}

// triageOptions builds the issuetriage.Options for one run from the parsed
// flags and what App.execute resolved.
func (f *triageFlags) triageOptions(d toolio.Deps) issuetriage.Options {
	target, _ := issuex.ParseRepo(f.repo)
	return issuetriage.Options{
		Input:     d.Input,
		Workspace: d.Workspace,
		Repo:      target,
		Labels:    splitLabels(f.labels),
		DryRun:    d.Common.DryRun,
		Overwrite: f.overwrite,
		Runner:    d.Runner,
		Forge:     d.Forge,
		Run:       d.Run,
		Progress:  d.Progress,

		RepoMapTokens: d.Common.RepoMapTokens,
	}
}

// failureResponse maps a pipeline error onto the exit code and error object,
// the same way for the ordinary run and for --preflight.
func failureResponse(result *issuetriage.Result, err error) (int, any, *toolio.ErrorInfo) {
	var f *issuetriage.Failure
	info := toolio.ErrorFrom("run", err)
	if errors.As(err, &f) {
		info.Stage, info.Category = f.Stage, f.Category
	}
	// A refusal before the model phase has no result. A nil *Result in the
	// interface is not a nil result: the shell would call SetDetail on it.
	if result == nil {
		return toolio.ExitCodeFor(info.Category), nil, info
	}
	return toolio.ExitCodeFor(info.Category), result, info
}

func newApp() toolio.App {
	f := &triageFlags{}

	return toolio.App{
		Name:             issuetriage.ToolName,
		Version:          agentfox.Version,
		Usage:            usage,
		Description:      "Triages a problem report read-only and files it as an issue on GitHub or GitLab.",
		InputDescription: "Exactly one of: a GitHub or GitLab issue or pull/merge-request URL (the issue and its comments are the report), a path to a readable file (its contents are the report), any other text (the text is the report), or - to read the report from stdin.",
		ExitCodes: map[int]string{
			toolio.ExitOK:     "triaged, and filed unless --dry-run",
			toolio.ExitFailed: "failed",
			toolio.ExitUsage:  "usage error; nothing was fetched and nothing was written",
		},
		ResultSample: issuetriage.Result{},
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&f.repo, "repo", "", "target repository as owner/repo or group/subgroup/project; default the input issue's, else the origin remote of --dir")
			fs.StringVar(&f.labels, "label", "", "comma-separated labels for the created issue, e.g. af:fix")
			fs.BoolVar(&f.overwrite, "overwrite", false, "rewrite the input issue in place instead of creating a new one")
		},
		// A triage reads: 100 turns is a lot of files, and $2 is more than
		// any single diagnosis has cost. Both are ceilings, not targets.
		DefaultBounds: agentrun.Bounds{MaxTurns: 100, MaxBudgetUSD: 2.00},
		// One phase: --total-budget and --budget bound the same spend.
		SinglePhase: true,

		PreCheck: func(*toolio.Common) error {
			if f.overwrite && (f.repo != "" || f.labels != "") {
				return toolio.Usagef("--overwrite rewrites the issue it was given, so it " +
					"cannot be combined with --repo or --label")
			}
			if f.repo != "" {
				if _, ok := issuex.ParseRepo(f.repo); !ok {
					return toolio.Usagef("--repo %q cannot be parsed as a repository identifier", f.repo)
				}
			}
			return nil
		},
		CheckInput: func(in toolio.Input) error {
			if f.overwrite && in.Issue == nil {
				return toolio.Usagef("--overwrite needs a forge issue URL (GitHub or GitLab) as the input; "+
					"%s is %s", in.Origin, in.Kind)
			}
			return nil
		},

		Exec: indexed(func(ctx context.Context, d toolio.Deps, idx tools.Index) (int, any, *toolio.ErrorInfo) {
			o := f.triageOptions(d)
			o.Index = idx
			o.IndexUnavailable = indexReason(ctx)
			result, err := issuetriage.Run(ctx, o)
			if err != nil {
				return failureResponse(result, err)
			}
			return toolio.ExitOK, result, nil
		}),

		// --preflight: every check the ordinary run makes before its model
		// phase, against the identical options, then stop.
		PreflightExec: indexed(func(ctx context.Context, d toolio.Deps, idx tools.Index) (int, any, *toolio.ErrorInfo) {
			o := f.triageOptions(d)
			o.Index = idx
			o.IndexUnavailable = indexReason(ctx)
			result, err := issuetriage.RunPreflight(o)
			if err != nil {
				return failureResponse(result, err)
			}
			return toolio.ExitOK, result, nil
		}),
	}
}

// newIndex builds the run's code-search index. It is a variable so that a
// test can stand in for codesearch.New.
var newIndex = func(ws *tools.Workspace) (tools.Index, error) {
	return codesearch.New(ws, codesearch.Options{})
}

// openIndex builds the run's one code-search index and returns d with a Runner
// whose Config carries it, the index, and the function that closes it
// (16-REQ-3). When the index cannot be built it returns d unchanged, a nil
// index, why it could not be built and a no-op: navigation never fails a run.
func openIndex(d toolio.Deps) (toolio.Deps, tools.Index, string, func()) {
	unavailable := func(reason string) (toolio.Deps, tools.Index, string, func()) {
		d.Run.Warn(toolio.WarnCodeSearchUnavailable, "low",
			"code_search is unavailable and the run falls back to search_files: %s", reason)
		return d, nil, reason, func() {}
	}
	idx, err := newIndex(d.Workspace)
	if err != nil {
		return unavailable(err.Error())
	}
	if idx == nil {
		return unavailable("the index builder returned no index")
	}
	// The Runner the shell built has no index: build another from the same
	// configuration, so that every phase it runs offers code_search.
	cfg := d.RunnerConfig()
	cfg.Index = idx
	runner, err := agentrun.NewRunner(cfg)
	if err != nil {
		_ = idx.Close()
		return unavailable(err.Error())
	}
	d.Runner = runner
	return d, idx, "", func() { _ = idx.Close() }
}

// indexReasonKey keys, in the context the pipeline runs under, why the run has
// no code-search index. --preflight reports it (16-REQ-7.2).
type indexReasonKey struct{}

// indexReason is why the run has no index, or "" when it has one.
func indexReason(ctx context.Context) string {
	reason, _ := ctx.Value(indexReasonKey{}).(string)
	return reason
}

// indexed wraps a tool's pipeline: it builds the index before the pipeline
// starts and closes it on every way out, a panic included.
func indexed(inner func(context.Context, toolio.Deps, tools.Index) (int, any, *toolio.ErrorInfo)) func(context.Context, toolio.Deps) (int, any, *toolio.ErrorInfo) {
	return func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
		d, idx, reason, closeIndex := openIndex(d)
		defer closeIndex()
		return inner(context.WithValue(ctx, indexReasonKey{}, reason), d, idx)
	}
}

func main() {
	os.Exit(newApp().Main(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
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
