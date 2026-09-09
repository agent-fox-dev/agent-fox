package issuetriage

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/ghapi"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// Options configure one triage run.
type Options struct {
	// Input is the classified argument. Required.
	Input toolio.Input
	// Workspace roots the read-only analysis. The model cannot read outside
	// it. Required.
	Workspace *tools.Workspace
	// Repo is the target repository. Zero means: the repository the input
	// issue came from, else the origin remote of the workspace.
	Repo ghapi.Repo
	// Labels are applied to a created issue.
	Labels []string
	// DryRun makes no change on GitHub. The rendered issue is still
	// produced and reported.
	DryRun bool
	// Overwrite rewrites the input issue in place — title and body — instead
	// of creating a new one. It needs an issue URL as the input, which the
	// caller checks before the run starts.
	Overwrite bool

	// Runner drives the model phase. Required.
	Runner *agentrun.Runner
	// GitHub is the REST client. It may be nil under DryRun with a
	// non-issue input.
	GitHub *ghapi.Client
	// Run records progress, warnings and per-phase cost.
	Run *toolio.Run
	// Progress reports steps to stderr.
	Progress *toolio.Progress
}

// Result is what the tool reports as JSON.
type Result struct {
	// Action is what happened on GitHub: created, updated, or none.
	Action string `json:"action"`
	// Repo is the repository the issue was filed in or would be.
	Repo string `json:"repo,omitempty"`
	// URL and Number identify the issue, when one was written.
	URL    string `json:"url,omitempty"`
	Number int    `json:"number,omitempty"`
	// UpstreamURL is the issue the report was read from, when it was one.
	UpstreamURL string `json:"upstream_url,omitempty"`

	Title              string    `json:"title"`
	Body               string    `json:"body"`
	Severity           string    `json:"severity"`
	SeverityRationale  string    `json:"severity_rationale"`
	Confidence         string    `json:"confidence"`
	Problem            string    `json:"problem"`
	Reproduction       string    `json:"reproduction"`
	RootCause          string    `json:"root_cause"`
	RelatedInstances   []string  `json:"related_instances,omitempty"`
	AffectedFiles      []FileRef `json:"affected_files"`
	SuggestedFix       Fix       `json:"suggested_fix"`
	AcceptanceCriteria []string  `json:"acceptance_criteria"`
	Labels             []string  `json:"labels,omitempty"`

	// RejectedPathCalls counts the file_issue calls refused for citing a
	// path that is not in the workspace, and RejectedPaths names them. A
	// nonzero count is the citation check working.
	RejectedPathCalls int      `json:"rejected_path_calls"`
	RejectedPaths     []string `json:"rejected_paths,omitempty"`
}

// Failure carries a stage and category alongside the message, so the caller
// can render the JSON error object without re-deriving either.
type Failure struct {
	Stage    string
	Category string
	Err      error
}

func (f *Failure) Error() string { return f.Err.Error() }
func (f *Failure) Unwrap() error { return f.Err }

// StageName and CategoryName satisfy toolio.StageError, so the command maps
// a failure onto the envelope without re-deriving either.
func (f *Failure) StageName() string    { return f.Stage }
func (f *Failure) CategoryName() string { return f.Category }

func fail(stage, category string, err error) *Failure {
	return &Failure{Stage: stage, Category: category, Err: err}
}

func failf(stage, category, format string, args ...any) *Failure {
	return &Failure{Stage: stage, Category: category, Err: fmt.Errorf(format, args...)}
}

// Run performs the triage and, unless DryRun says otherwise, writes the issue.
//
// The order is the correction the skill needs most: every check that can
// refuse the run happens before the model is called, and the write to GitHub
// happens after it. The skill posts to the issue in step 5 and checks whether
// it can work in step 6, which leaves a public comment describing work that
// never began.
func Run(ctx context.Context, o Options) (*Result, error) {
	if o.Workspace == nil {
		return nil, failf("preflight", "internal", "no workspace configured")
	}
	if o.Runner == nil {
		return nil, failf("preflight", "internal", "no runner configured")
	}

	target, err := resolveTarget(o)
	if err != nil {
		return nil, err
	}
	if err := checkWriteCredential(o, target); err != nil {
		return nil, err
	}

	t := &triager{ws: o.Workspace}
	done := o.Progress.Begin("analysing %s", o.Input.Origin)
	res, runErr := o.Runner.Run(ctx, t.phase(o.Input, o.Workspace.Root))
	o.Run.AddPhase(toolio.PhaseInfo{
		Name:         res.Name,
		Turns:        res.Turns,
		StopReason:   string(res.StopReason),
		InputTokens:  int64(res.Usage.InputTokens),
		OutputTokens: int64(res.Usage.OutputTokens),
		CostUSD:      res.Usage.CostUSD,
		DurationMS:   res.Elapsed.Milliseconds(),
	})
	done(fmt.Sprintf("· %d turns · %s↑ %s↓",
		res.Turns,
		toolio.FormatTokens(int64(res.Usage.InputTokens)),
		toolio.FormatTokens(int64(res.Usage.OutputTokens))))

	if runErr != nil {
		return nil, fail("analyse", agentrun.CategoryOf(runErr), runErr)
	}
	issue, ok := t.Result()
	if !ok {
		return nil, fail("analyse", agentrun.CategoryNoResult,
			agentrun.NoResultError("triage", ToolFileIssue, res))
	}

	rejected, badPaths := t.Rejections()
	if rejected > 0 {
		o.Run.Warn("%d %s call(s) rejected for citing paths not in the workspace: %s",
			rejected, ToolFileIssue, joinLimited(badPaths, 5))
	}

	out := &Result{
		Action:             "none",
		Repo:               target.String(),
		Title:              issue.Title,
		Body:               issue.Render(o.Input.Kind.String(), o.Input.Origin),
		Severity:           issue.Severity,
		SeverityRationale:  issue.SeverityRationale,
		Confidence:         issue.Confidence,
		Problem:            issue.Problem,
		Reproduction:       issue.Reproduction,
		RootCause:          issue.RootCause,
		RelatedInstances:   issue.RelatedInstances,
		AffectedFiles:      issue.AffectedFiles,
		SuggestedFix:       issue.Fix,
		AcceptanceCriteria: issue.AcceptanceCriteria,
		Labels:             o.Labels,
		RejectedPathCalls:  rejected,
		RejectedPaths:      toolio.SortedUnique(badPaths),
	}
	if o.Input.Issue != nil {
		out.UpstreamURL = o.Input.Issue.URL()
	}
	if !target.Valid() {
		out.Repo = ""
	}

	if o.DryRun {
		o.Progress.Step("dry run: nothing was written to GitHub")
		return out, nil
	}
	if err := write(ctx, o, target, out); err != nil {
		return out, err
	}
	return out, nil
}

// resolveTarget decides which repository the issue belongs to.
//
// A --repo flag wins, then the repository the input issue came from, then the
// origin remote of the workspace. A dry run with none of the three is legal:
// the diagnosis is still worth printing.
func resolveTarget(o Options) (ghapi.Repo, *Failure) {
	if o.Repo.Valid() {
		return o.Repo, nil
	}
	if o.Input.Issue != nil {
		return o.Input.Issue.Repo, nil
	}
	if r, ok := ghapi.DetectRepo(o.Workspace.Root); ok {
		return r, nil
	}
	if o.DryRun {
		return ghapi.Repo{}, nil
	}
	return ghapi.Repo{}, failf("preflight", "usage",
		"no target repository: %s has no GitHub origin remote and the input is not an "+
			"issue URL — pass --repo owner/repo, or --dry-run to print the diagnosis",
		o.Workspace.Root)
}

// checkWriteCredential fails before the model is called when the run will
// write and cannot.
//
// This is the pre-flight the skill does not have. Discovering a missing token
// after a ten-minute analysis costs the analysis; discovering it in the first
// second costs nothing.
func checkWriteCredential(o Options, target ghapi.Repo) *Failure {
	if o.DryRun {
		return nil
	}
	if o.GitHub == nil {
		return failf("preflight", "internal", "no GitHub client configured")
	}
	if !o.GitHub.Authenticated() {
		verb := "creating an issue"
		if o.Overwrite {
			verb = "rewriting the issue"
		}
		return failf("preflight", "auth",
			"%s in %s needs a credential: set GITHUB_TOKEN or GH_TOKEN, or pass --dry-run",
			verb, target)
	}
	return nil
}

// write performs the one GitHub mutation this tool makes.
func write(ctx context.Context, o Options, target ghapi.Repo, out *Result) *Failure {
	if o.Overwrite {
		ref := *o.Input.Issue
		updated, err := o.GitHub.UpdateIssue(ctx, ref, out.Title, out.Body)
		if err != nil {
			return fail("write", "github", fmt.Errorf("rewriting %s: %w", ref, err))
		}
		out.Action, out.URL, out.Number = "updated", updated.HTMLURL, updated.Number
		o.Progress.Step("rewrote %s", updated.HTMLURL)
		return nil
	}

	created, err := o.GitHub.CreateIssue(ctx, target, out.Title, out.Body, o.Labels)
	if err != nil {
		if errors.Is(err, ghapi.ErrNoToken) {
			return fail("write", "auth", err)
		}
		return fail("write", "github", fmt.Errorf("creating an issue in %s: %w", target, err))
	}
	out.Action, out.URL, out.Number = "created", created.HTMLURL, created.Number
	o.Progress.Step("filed %s", created.HTMLURL)
	return nil
}

func joinLimited(ss []string, n int) string {
	ss = toolio.SortedUnique(ss)
	if len(ss) <= n {
		return join(ss)
	}
	return join(ss[:n]) + fmt.Sprintf(" (and %d more)", len(ss)-n)
}

func join(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
