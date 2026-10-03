package issuetriage

import (
	"context"
	"fmt"
	"strings"

	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// Categories this package adds to the shared vocabulary.
const (
	CategoryForge  = "forge"
	CategoryGitHub = CategoryForge
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
	Repo issuex.Repo
	// Labels are applied to a created issue.
	Labels []string
	// DryRun makes no change on the forge. The rendered issue is still
	// produced and reported.
	DryRun bool
	// Overwrite rewrites the input issue in place — title and body — instead
	// of creating a new one. It needs an issue URL as the input, which the
	// caller checks before the run starts.
	Overwrite bool

	// Runner drives the model phase. Required.
	Runner *agentrun.Runner
	// Forge is the forge client, GitHub or GitLab. It may be nil under
	// DryRun with a non-issue input.
	Forge issuex.Client
	// Run records progress, warnings and per-phase cost.
	Run *toolio.Run
	// Progress reports steps to stderr.
	Progress *toolio.Progress
}

// Result is what the tool reports as JSON.
type Result struct {
	// Action is what happened on the forge: created, updated, or none.
	Action string `json:"action" trust:"fact" description:"What happened on the forge: created, updated or none."`
	// Repo is the repository the issue was filed in or would be.
	Repo string `json:"repo,omitempty" trust:"fact" description:"The repository the issue was filed in, or would be."`
	// URL and Number identify the issue, when one was written.
	URL    string `json:"url,omitempty" trust:"fact" description:"The URL of the issue, when one was written."`
	Number int    `json:"number,omitempty" description:"The number of the issue, when one was written."`
	// UpstreamURL is the issue the report was read from, when it was one.
	UpstreamURL string `json:"upstream_url,omitempty" trust:"fact" description:"The issue the report was read from, when it was one."`

	Title              string    `json:"title" trust:"model" description:"The issue title."`
	Body               string    `json:"body" trust:"model" description:"The issue body as rendered."`
	Severity           string    `json:"severity" trust:"model" description:"How severe the problem is."`
	SeverityRationale  string    `json:"severity_rationale" trust:"model" description:"Why that severity."`
	Confidence         string    `json:"confidence" trust:"model" description:"How confident the triage is in its diagnosis."`
	Problem            string    `json:"problem" trust:"model" description:"What is wrong."`
	Reproduction       string    `json:"reproduction" trust:"model" description:"How to reproduce it."`
	RootCause          string    `json:"root_cause" trust:"model" description:"The root cause."`
	RelatedInstances   []string  `json:"related_instances,omitempty" trust:"model" description:"Other places with the same problem."`
	AffectedFiles      []FileRef `json:"affected_files" description:"The files the problem touches, each with its role."`
	SuggestedFix       Fix       `json:"suggested_fix" description:"The proposed fix."`
	AcceptanceCriteria []string  `json:"acceptance_criteria" trust:"model" description:"What a fix has to satisfy."`
	Labels             []string  `json:"labels,omitempty" trust:"fact" description:"The labels applied to the issue."`

	// RejectedPathCalls counts the file_issue calls refused for citing a
	// path that is not in the workspace, and RejectedPaths names them. A
	// nonzero count is the citation check working.
	RejectedPathCalls int      `json:"rejected_path_calls" description:"How many file_issue calls were refused for citing a path that is not in the workspace."`
	RejectedPaths     []string `json:"rejected_paths,omitempty" trust:"model" description:"The paths those calls cited."`

	// Stage is "preflight" on a --preflight run, which stops before the
	// model phase; it is absent on an ordinary run.
	Stage string `json:"stage,omitempty" trust:"fact" description:"preflight on a --preflight run, which stops before the model phase; absent on an ordinary run."`

	// DryRun records that no remote change was made.
	DryRun bool `json:"dry_run,omitempty" description:"True when no remote change was made."`

	// Preflight and Estimate are set only by a --preflight run whose checks
	// all passed (RunPreflight): the checks that were performed, and what the
	// real run would spend at most.
	Preflight []toolio.PreflightCheck `json:"preflight,omitempty" description:"Every check a --preflight run performed and its outcome. Present only under --preflight, when every refusing check passed."`
	Estimate  *toolio.Estimate        `json:"estimate,omitempty" description:"What the real run would spend at most: phases, per-phase ceilings and total. Present only under --preflight, when every refusing check passed."`

	// Detail records which view of this result was emitted: "summary" or
	// "full". It is present on both.
	Detail string `json:"detail" trust:"fact" description:"Which view of this result was emitted: summary or full."`
}

// SetDetail implements toolio.DetailedResult.
func (r *Result) SetDetail(d string) { r.Detail = d }

// Summary returns one sentence describing the outcome in issue's vocabulary.
func (r Result) Summary() string {
	// An ordinary run never sets a stage, so "preflight" here is a
	// --preflight run, and only a run whose checks passed has a checklist.
	if r.Stage == "preflight" && len(r.Preflight) > 0 {
		return fmt.Sprintf("issue: preflight passed (%d checks)", len(r.Preflight))
	}
	action := r.Action
	if action == "" {
		action = "triaged"
	}
	target := ""
	if r.Repo != "" && r.Number > 0 {
		target = fmt.Sprintf(" %s#%d", r.Repo, r.Number)
	} else if r.URL != "" {
		target = " " + r.URL
	} else if r.Number > 0 {
		target = fmt.Sprintf(" #%d", r.Number)
	}

	filesCount := len(r.AffectedFiles)
	filesLabel := "files"
	if filesCount == 1 {
		filesLabel = "file"
	}

	sev := r.Severity
	if sev == "" {
		sev = "unknown"
	}

	return fmt.Sprintf("issue: %s%s (%s severity, %d %s cited)", action, target, sev, filesCount, filesLabel)
}

// Resumable implements toolio.Resumabler. For issuetriage, this is always false
// because issue has no notion of continuing a prior run.
func (r Result) Resumable() bool {
	// A preflight result is no exception: there is nothing to continue.
	return false
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
// refuse the run happens before the model is called, and the write to the forge
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

	target, f := Preflight(o)
	if f != nil {
		return nil, f
	}

	t := &triager{ws: o.Workspace}
	done := o.Progress.Begin("analysing %s", o.Input.Origin)
	res, runErr := o.Runner.Run(ctx, t.phase(o.Input, o.Workspace.Root))
	o.Run.AddPhase(toolio.PhaseFromResult(res, ""))
	done(toolio.PhaseSummary(res))

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
		o.Run.Warn(toolio.WarnRejectedPathCalls, "low", "%d %s call(s) rejected for citing paths not in the workspace: %s",
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
		DryRun:             o.DryRun,
	}
	if o.Input.Issue != nil {
		out.UpstreamURL = o.Input.Issue.URL()
	}
	if !target.Valid() {
		out.Repo = ""
	}

	if o.DryRun {
		if o.Progress != nil {
			o.Progress.Step("write", "dry run: nothing was written to the forge")
		}
		return out, nil
	}
	if err := Write(ctx, o, target, out); err != nil {
		return out, err
	}
	return out, nil
}

// Preflight is every check Run performs before its model phase, in the order
// Run performs them: the target repository, then the forge credential.
func Preflight(o Options) (issuex.Repo, *Failure) {
	target, err := ResolveTarget(o)
	if err != nil {
		return issuex.Repo{}, err
	}
	if err := CheckWriteCredential(o, target); err != nil {
		return issuex.Repo{}, err
	}
	return target, nil
}

// RunPreflight is triage --preflight: every check Run performs before its model
// phase, reported and then stopped at. It calls the one Preflight Run calls,
// never a copy of it, so a run that would refuse refuses here with the
// identical stage, category and message. A refusal returns no result: a
// partial checklist of what passed before it would be a second answer to
// "would this run start".
//
// Nothing is written to the forge and no phase is run.
func RunPreflight(o Options) (*Result, error) {
	if o.Workspace == nil {
		return nil, failf("preflight", "internal", "no workspace configured")
	}
	if o.Runner == nil {
		return nil, failf("preflight", "internal", "no runner configured")
	}
	target, f := Preflight(o)
	if f != nil {
		return nil, f
	}

	// Preflight returned no failure, so each condition it refuses on held.
	detail := target.String()
	if !target.Valid() {
		detail = "none (dry run)"
	}
	list := []toolio.PreflightCheck{{Check: "target_repository", OK: true, Detail: detail}}
	if !o.DryRun {
		list = append(list, toolio.PreflightCheck{Check: "forge_credential", OK: true})
	}

	est := &toolio.Estimate{Phases: 1}
	est.MaxTurnsPerPhase, est.MaxBudgetPerPhaseUSD = o.Runner.ResolvedBounds()
	est.MaxTotalUSD = float64(est.Phases) * est.MaxBudgetPerPhaseUSD

	return &Result{Stage: "preflight", DryRun: o.DryRun, Preflight: list, Estimate: est}, nil
}

// ResolveTarget decides which repository the issue belongs to.
//
// A --repo flag wins, then the repository the input issue came from, then the
// origin remote of the workspace. A dry run with none of the three is legal:
// the diagnosis is still worth printing.
func ResolveTarget(o Options) (issuex.Repo, *Failure) {
	if o.Repo.Valid() {
		return o.Repo, nil
	}
	if o.Input.Issue != nil {
		return o.Input.Issue.Repo, nil
	}
	root := ""
	if o.Workspace != nil {
		root = o.Workspace.Root
		if r, ok := issuex.DetectRepo(o.Workspace.Root); ok {
			return r, nil
		}
	}
	if o.DryRun {
		return issuex.Repo{}, nil
	}
	return issuex.Repo{}, failf("preflight", "usage",
		"no target repository: %s has no origin remote on a recognized forge and the input is not an "+
			"issue URL — pass --repo owner/repo, or --dry-run to print the diagnosis",
		root)
}

// CheckWriteCredential fails before the model is called when the run will
// write and cannot.
//
// This is the pre-flight the skill does not have. Discovering a missing token
// after a ten-minute analysis costs the analysis; discovering it in the first
// second costs nothing.
func CheckWriteCredential(o Options, target issuex.Repo) *Failure {
	if o.DryRun {
		return nil
	}
	if o.Forge == nil {
		return failf("preflight", "internal", "no forge client configured")
	}
	if !o.Forge.Authenticated() {
		verb := "creating an issue"
		if o.Overwrite {
			verb = "rewriting the issue"
		}
		return failf("preflight", "auth",
			"%s in %s needs a credential: set GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN, or pass --dry-run",
			verb, target)
	}
	return nil
}

// Write performs the one forge mutation this tool makes.
func Write(ctx context.Context, o Options, target issuex.Repo, out *Result) *Failure {
	if o.Forge == nil {
		return failf("write", "internal", "no forge client configured")
	}
	if o.Overwrite {
		if o.Input.Issue == nil {
			return failf("write", "internal", "no input issue reference to overwrite")
		}
		ref := *o.Input.Issue
		updated, err := o.Forge.UpdateIssue(ctx, ref, issuex.UpdateIssueRequest{
			Title: out.Title,
			Body:  out.Body,
		})
		// The write is recorded where it happens. A failure here is the
		// run's error rather than a Run.Warn, so there is no warning code
		// to pair with it.
		o.Run.RecordSideEffect("update_issue", ref.String(), err == nil, "")
		if err != nil {
			if issuex.IsNoToken(err) {
				return fail("write", "auth", err)
			}
			return fail("write", CategoryForge, fmt.Errorf("rewriting %s: %w", ref, err))
		}
		out.Action, out.URL, out.Number = "updated", updated.URL, updated.Number
		if out.URL == "" {
			out.URL = updated.HTMLURL
		}
		if o.Progress != nil {
			o.Progress.Step("write", "rewrote %s", out.URL)
		}
		return nil
	}

	created, err := o.Forge.CreateIssue(ctx, target, issuex.CreateIssueRequest{
		Title:  out.Title,
		Body:   out.Body,
		Labels: o.Labels,
	})
	o.Run.RecordSideEffect("create_issue", target.String(), err == nil, "")
	if err != nil {
		if issuex.IsNoToken(err) {
			return fail("write", "auth", err)
		}
		return fail("write", CategoryForge, fmt.Errorf("creating an issue in %s: %w", target, err))
	}
	out.Action, out.URL, out.Number = "created", created.URL, created.Number
	if out.URL == "" {
		out.URL = created.HTMLURL
	}
	if o.Progress != nil {
		o.Progress.Step("write", "filed %s", out.URL)
	}
	return nil
}

// joinLimited renders a bounded list of paths for a warning. A run that
// refused twenty citations should say so without printing twenty paths.
func joinLimited(ss []string, n int) string {
	ss = toolio.SortedUnique(ss)
	if len(ss) <= n {
		return strings.Join(ss, ", ")
	}
	return strings.Join(ss[:n], ", ") + fmt.Sprintf(" (and %d more)", len(ss)-n)
}
