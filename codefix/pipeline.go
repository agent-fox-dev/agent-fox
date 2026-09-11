package codefix

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/ghapi"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// Options configure one fix run.
type Options struct {
	// Input is the classified argument. When it is a GitHub issue, the run
	// comments on it and the pull request closes it; otherwise the run works
	// from the text and writes nothing to GitHub but the pull request.
	Input toolio.Input
	// Workspace roots the file tools at the repository. Required.
	Workspace *tools.Workspace
	// Repo is the target repository for the pull request. Zero means the
	// input issue's, else the origin remote.
	Repo issuex.Repo
	// Land decides what happens once the change is verified.
	Land LandMode
	// DryRun makes no REMOTE change: nothing is pushed, no pull request is
	// opened, and comments are reported instead of posted. The branch and the
	// commit are still made locally — the implementation phase edits real
	// files, so containing them on a branch you can delete is safer than
	// leaving them loose.
	DryRun bool
	// VerifyCommand overrides detection. Empty means detect.
	VerifyCommand string
	// NoVerify runs nothing. The result is then reported as unverified — not
	// as a pass.
	NoVerify bool
	// VerifyTimeout bounds one verification run.
	VerifyTimeout time.Duration
	// PushAttempts is how many times a push is retried.
	PushAttempts int
	// AllowPrograms widens the implementation phase's shell allowlist.
	AllowPrograms []string
	// Draft opens the pull request as a draft.
	Draft bool
	// Pull checks out and pulls the base branch or PullBranch from origin before branching.
	Pull bool
	// PullBranch is the branch to checkout and pull when Pull is enabled.
	// Empty means the base branch.
	PullBranch string

	// Runner drives the model phases. Required.
	Runner *agentrun.Runner
	// Forge is the forge client.
	Forge issuex.Client
	// GitHub is deprecated: use Forge instead.
	GitHub *ghapi.Client
	// Git is the repository wrapper. Nil means one rooted at the workspace.
	Git *gitx.Git
	// CheckRunner runs the verification command. Nil means the reduced-env
	// runner, which strips credentials.
	CheckRunner gitx.Runner
	// Run records warnings and per-phase cost.
	Run *toolio.Run
	// Progress reports steps to stderr.
	Progress *toolio.Progress

	// brain is the model half. It is unexported and injected by tests, which
	// is what lets the whole pipeline run against a scripted provider.
	brain brain
}

// Failure carries the stage and category of a failed run.
type Failure struct {
	Stage    string
	Category string
	Err      error
}

func (f *Failure) Error() string        { return f.Err.Error() }
func (f *Failure) Unwrap() error        { return f.Err }
func (f *Failure) StageName() string    { return f.Stage }
func (f *Failure) CategoryName() string { return f.Category }

func fail(stage, category string, err error) *Failure {
	return &Failure{Stage: stage, Category: category, Err: err}
}

func failf(stage, category, format string, args ...any) *Failure {
	return &Failure{Stage: stage, Category: category, Err: fmt.Errorf(format, args...)}
}

// Categories this package adds to the shared vocabulary.
const (
	// CategoryAmbiguous means the run stopped to ask a person a question.
	// The caller maps it to the "needs a human" exit code.
	CategoryAmbiguous = "ambiguous"
	// CategoryUnverified means code was written and the checks do not pass.
	CategoryUnverified = "unverified"
	// CategoryGit is the git external system.
	CategoryGit = "git"
	// CategoryEmpty means the model reported a fix and changed nothing.
	CategoryEmpty = "empty_change"
)

// Run drives the whole pipeline.
//
// The order below is the shape of the program, and most of it is a correction
// to the skill it replaces:
//
//	pre-flight   every check that can refuse the run, BEFORE anything is
//	             posted or fetched — the skill's most common failure ("you
//	             have uncommitted changes") leaves a public comment first.
//	baseline     the checks are run once before any change, so "the tests
//	             pass" later has something to mean.
//	analyse      model. Read-only tools.
//	branch       a fresh, unique name — never a force-push over an existing
//	             branch, which the skill stops to ask about inside a workflow
//	             that promised not to stop.
//	comment      the analysis, now that the run is known to be able to start.
//	implement    model. Read-write tools and a guarded shell.
//	verify       the checks again, compared with the baseline.
//	land         commit, push, open the pull request.
//	comment      the summary, LAST, so it can carry the pull request's URL
//	             and quote the verification that actually ran.
func Run(ctx context.Context, o Options) (*Result, error) {
	if o.Workspace == nil {
		return nil, failf("preflight", agentrun.CategoryInternal, "no workspace configured")
	}
	if o.Runner == nil && o.brain == nil {
		return nil, failf("preflight", agentrun.CategoryInternal, "no runner configured")
	}
	root := o.Workspace.Root
	git := o.Git
	if git == nil {
		git = gitx.New(root, nil)
	}
	if o.Land == "" {
		o.Land = LandPR
	}
	if o.PushAttempts <= 0 {
		o.PushAttempts = 4
	}
	if o.VerifyTimeout <= 0 {
		o.VerifyTimeout = checks.DefaultTimeout
	}

	result := &Result{Stage: "preflight", DryRun: o.DryRun, Verdict: string(checks.VerdictUnverified)}
	if o.Input.Issue != nil {
		result.IssueURL = o.Input.Issue.URL()
		result.IssueNumber = o.Input.Issue.Number
	}

	target, base, pfErr := preflight(ctx, o, git, result)
	if pfErr != nil {
		return result, pfErr
	}
	result.Repo = target.String()
	result.BaseBranch = base

	command := o.VerifyCommand
	if o.NoVerify {
		command = ""
	} else if command == "" {
		command = checks.Detect(root)
		if command == "" {
			o.Run.Warn("no verification command could be detected for %s: the change will be "+
				"reported as unverified", root)
		} else {
			o.Progress.Detail("verification command: %s", command)
		}
	}

	baseline := runChecks(ctx, o, root, command, "baseline")
	result.Baseline = baseline

	// The acceptance criteria are read out of the report before the model
	// sees it, so that what the change is measured against is fixed by the
	// input rather than by what the model chose to notice in it.
	criteria := ParseCriteria(o.Input.Body)
	result.AcceptanceCriteria = criteria
	if len(criteria) > 0 {
		o.Progress.Detail("acceptance criteria in the report: %s",
			strings.Join(criteriaIDs(criteria), ", "))
	}

	b := o.brain
	if b == nil {
		programs := append([]string(nil), o.AllowPrograms...)
		if p := checks.Program(command); p != "" {
			programs = append(programs, p)
		}
		b = &agentBrain{runner: o.Runner, extraPrograms: programs}
	}

	// ---------------------------------------------------------- analyse --
	done := o.Progress.Begin("analysing %s", o.Input.Origin)
	analysis, stats, err := b.Analyze(ctx, analysisInput{
		Input: o.Input, Baseline: baseline, VerifyCommand: command, Root: root,
		Criteria: criteria,
	})
	recordPhase(o.Run, stats)
	done(phaseSummary(stats))
	if err != nil {
		return result, fail("analyse", agentrun.CategoryOf(err), err)
	}

	result.Stage = "analysed"
	result.Classification = string(analysis.Classification)
	result.Title = analysis.Title
	result.Summary = analysis.Summary
	result.RootCause = analysis.RootCause
	result.Approach = analysis.Approach
	result.Assumptions = analysis.Assumptions

	if analysis.Ambiguity != nil {
		return stopOnAmbiguity(ctx, o, result, *analysis.Ambiguity)
	}

	// ----------------------------------------------------------- branch --
	branch := gitx.UniqueBranchName(ctx, git,
		gitx.BranchName(analysis.Classification.BranchPrefix(), result.IssueNumber, analysis.Title))
	if err := git.CreateBranch(ctx, branch); err != nil {
		return result, fail("branch", CategoryGit, err)
	}
	result.Branch = branch
	o.Progress.Step("branched %s from %s", branch, base)

	// The analysis comment goes up now: the run is known to be able to
	// start, a branch exists, and the comment can name it.
	postComment(ctx, o, result, analysisComment(analysis, criteria, branch, command, baseline),
		"analysis")

	// -------------------------------------------------------- implement --
	done = o.Progress.Begin("implementing")
	impl, stats, err := b.Implement(ctx, implementInput{
		Input: o.Input, Analysis: analysis, Baseline: baseline, VerifyCommand: command,
		Branch: branch, Root: root, Instructions: projectInstructions(root),
		Criteria: criteria,
	})
	recordPhase(o.Run, stats)
	done(phaseSummary(stats))
	if err != nil {
		return result, fail("implement", agentrun.CategoryOf(err), err)
	}
	result.Stage = "implemented"
	result.Implementation = &impl
	if strings.TrimSpace(impl.Summary) != "" {
		result.Summary = impl.Summary
	}

	// The outcome is derived from the verdicts here rather than taken from
	// the model, and a criterion that was not met is a warning on the run
	// even when the project's checks are green. It does not by itself stop
	// the change from landing: the criteria verdicts are the model's own
	// account of its work, and a run that refused to land on a self-reported
	// failure would be a run that taught the model not to report one. What
	// the reader gets instead is both facts, side by side and unmistakable —
	// a measured verification result, and a criterion-by-criterion answer
	// that says which parts of the ask are still open.
	result.CriteriaOutcome = criteriaOutcome(criteria, impl.CriteriaVerdicts)
	if result.CriteriaOutcome == CriterionFail {
		unmet := unmetCriteriaWarning(criteria, impl.CriteriaVerdicts)
		o.Run.Warn("%s", unmet)
		o.Progress.Step("%s", unmet)
	}

	// A run that reports a fix and changed nothing is a failed run, not an
	// empty commit. The evidence is git's, not the model's report.
	changed, err := git.ChangedFiles(ctx, "HEAD")
	if err != nil {
		return result, fail("verify", CategoryGit, err)
	}
	if len(changed) == 0 {
		return result, failf("verify", CategoryEmpty,
			"the implementation phase reported a change and no file differs from %s; "+
				"nothing was committed", base)
	}
	result.ChangedFiles = changed
	if stat, err := git.DiffStat(ctx, "HEAD"); err == nil {
		result.DiffStat = stat
	}

	// ----------------------------------------------------------- verify --
	after := runChecks(ctx, o, root, command, "verification")
	result.Verification = after
	verdict := checks.Compare(baseline, after)
	result.Verdict = string(verdict)

	landable := verdict.Landable() || (verdict == checks.VerdictUnverified && command == "")
	if !landable {
		return parkUnverified(ctx, o, git, result, impl, analysis, verdict, base)
	}

	// ------------------------------------------------------------- land --
	commit, err := git.CommitAll(ctx, commitMessage(analysis.Classification, impl, o.Input.Issue,
		strings.TrimSpace(impl.Summary)))
	if err != nil {
		return result, fail("commit", CategoryGit, err)
	}
	result.Commit = commit
	result.Stage = "committed"
	o.Progress.Step("committed %s", commit)

	if o.Land.Pushes() && !o.DryRun {
		if err := git.Push(ctx, branch, o.PushAttempts, func(m string) { o.Progress.Step("%s", m) }); err != nil {
			return result, fail("push", CategoryGit, err)
		}
		result.Pushed = true
		result.Stage = "pushed"
		o.Progress.Step("pushed origin/%s", branch)
	}

	if o.Land == LandPR && result.Pushed && target.Valid() {
		openPullRequest(ctx, o, target, result, analysis, impl, base, branch)
	}

	// "landed" means what --land asked for actually happened, so it is not set
	// for a mode that stopped earlier by design: --land=none ends at
	// "committed" and --land=branch at "pushed". A stage that always said
	// "landed" would be a template rather than a report.
	switch {
	case o.Land == LandPR && result.PullRequestURL != "":
		result.Stage = "landed"
	case o.Land == LandBranch && result.Pushed:
		result.Stage = "landed"
	case o.Land == LandNone && result.Commit != "":
		result.Stage = "landed"
	}

	postComment(ctx, o, result, summaryComment(result), "summary")
	return result, nil
}

// Preflight runs every check that can refuse the run.
//
// It happens before the model is called and before anything is posted, which
// is the ordering fix that matters most: discovering a dirty working tree
// after a ten-minute analysis costs the analysis, and discovering it after
// the analysis comment was posted costs the issue's readability too.
func Preflight(ctx context.Context, o Options, git *gitx.Git, result *Result) (issuex.Repo, string, *Failure) {
	if !git.IsRepo(ctx) {
		return issuex.Repo{}, "", failf("preflight", "usage",
			"%s is not a git repository; pass --dir", o.Workspace.Root)
	}
	dirty, err := git.DirtyFiles(ctx)
	if err != nil {
		return issuex.Repo{}, "", fail("preflight", CategoryGit, err)
	}
	if len(dirty) > 0 {
		return issuex.Repo{}, "", failf("preflight", "usage",
			"the working tree in %s has %d uncommitted change(s); commit or stash them first:\n%s",
			o.Workspace.Root, len(dirty), strings.Join(dirty, "\n"))
	}

	// The base branch is captured HERE, before any checkout. Asked later it
	// would name the feature branch, and a pull request would target itself.
	base := git.BaseBranch(ctx)
	if o.Pull || o.PullBranch != "" {
		targetBranch := o.PullBranch
		if targetBranch == "" {
			targetBranch = base
		}
		if err := git.Checkout(ctx, targetBranch); err != nil {
			return issuex.Repo{}, "", fail("preflight", CategoryGit, err)
		}
		if err := git.Pull(ctx, targetBranch); err != nil {
			return issuex.Repo{}, "", fail("preflight", CategoryGit, err)
		}
		base = targetBranch
	}

	target := o.Repo
	if !target.Valid() {
		if r, ok := issuex.DetectRepo(o.Workspace.Root); ok {
			target = r
		}
	}
	if !target.Valid() && o.Input.Issue != nil {
		target = o.Input.Issue.Repo
	}

	// A run that will write to the forge checks that it can before it spends
	// money on a model.
	needsToken := !o.DryRun && (o.Input.Issue != nil || o.Land == LandPR)
	if needsToken && (o.Forge == nil || !o.Forge.Authenticated()) {
		if o.Land == LandPR && o.Input.Issue == nil {
			return issuex.Repo{}, "", failf("preflight", "auth",
				"opening a pull request needs a credential: set GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN, "+
					"or pass --land=branch, --land=none or --dry-run")
		}
		return issuex.Repo{}, "", failf("preflight", "auth",
			"commenting on %s needs a credential: set GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN, or pass --dry-run",
			o.Input.Issue)
	}
	if o.Land == LandPR && !o.DryRun && !target.Valid() {
		return issuex.Repo{}, "", failf("preflight", "usage",
			"--land=pr needs a target repository: %s has no origin remote on a recognized forge and the input "+
				"is not an issue URL — pass --repo owner/repo, or --land=branch",
			o.Workspace.Root)
	}
	if o.Land.Pushes() && !o.DryRun && !git.HasRemote(ctx) {
		return issuex.Repo{}, "", failf("preflight", "usage",
			"--land=%s pushes, and %s has no origin remote; pass --land=none",
			o.Land, o.Workspace.Root)
	}
	result.Stage = "preflight"
	return target, base, nil
}

func preflight(ctx context.Context, o Options, git *gitx.Git, result *Result) (issuex.Repo, string, *Failure) {
	return Preflight(ctx, o, git, result)
}

// stopOnAmbiguity ends the run with a question rather than a change.
//
// No branch is created and no code is written, which is why the ambiguity
// check happens between the analysis and the branch: a run that stops here
// leaves the repository exactly as it found it.
func stopOnAmbiguity(ctx context.Context, o Options, result *Result, a Ambiguity) (*Result, error) {
	result.Stage = "stopped"
	result.Ambiguity = &a
	postComment(ctx, o, result, ambiguityComment(a), "clarification")
	o.Progress.Step("stopped: the report reads two ways and the code cannot settle which")
	return result, fail("analyse", CategoryAmbiguous, fmt.Errorf(
		"the input is ambiguous and the codebase cannot settle it: %s", strings.TrimSpace(a.Question)))
}

// parkUnverified commits the work as a wip: commit, returns the checkout to
// the base branch, and reports the failure.
//
// The work is kept rather than discarded because the implementation phase
// edited real files and a person may want to look at them; it is committed
// rather than left loose because a branch you can delete is easier to reason
// about than a dirty tree. It is emphatically not landed.
func parkUnverified(ctx context.Context, o Options, git *gitx.Git, result *Result,
	impl Implementation, analysis Analysis, verdict checks.Verdict, base string) (*Result, error) {

	result.Stage = "unverified"
	if commit, err := git.CommitAll(ctx, wipCommitMessage(impl, o.Input.Issue, verdict)); err != nil {
		o.Run.Warn("the unverified work could not be committed on %s: %v", result.Branch, err)
	} else {
		result.Commit = commit
	}
	if err := git.Checkout(ctx, base); err != nil {
		o.Run.Warn("could not return to %s: %v", base, err)
	}
	postComment(ctx, o, result, failureComment(result), "failure")

	o.Progress.Step("checks did not pass (%s); work parked on %s", verdict, result.Branch)
	return result, failf("verify", CategoryUnverified,
		"`%s` did not pass after the change (%s); the work is on %s and was not landed",
		result.Verification.Command, verdict, result.Branch)
}

// OpenPullRequest opens the pull request and degrades to a warning when it
// cannot.
func OpenPullRequest(ctx context.Context, o Options, target issuex.Repo, result *Result,
	analysis Analysis, impl Implementation, base, branch string) {
	openPullRequest(ctx, o, target, result, analysis, impl, base, branch)
}

// openPullRequest opens the pull request and degrades to a warning when it
// cannot.
//
// A failed pull request is not a failed run: the branch is pushed and the
// change is verified, so the work is safe and a person can open the PR by
// hand. Failing the run here would throw away a successful fix over a
// permissions error.
func openPullRequest(ctx context.Context, o Options, target issuex.Repo, result *Result,
	analysis Analysis, impl Implementation, base, branch string) {

	if o.Forge == nil {
		o.Run.Warn("the pull request could not be opened (the branch is pushed; open it by "+
			"hand from %s into %s): no forge client configured", branch, base)
		return
	}
	pr, err := o.Forge.CreatePullRequest(ctx, target, issuex.CreatePullRequestRequest{
		Title: pullRequestTitle(analysis.Classification, impl, o.Input.Issue),
		Body:  pullRequestBody(result),
		Head:  branch,
		Base:  base,
		Draft: o.Draft,
	})
	if err != nil {
		o.Run.Warn("the pull request could not be opened (the branch is pushed; open it by "+
			"hand from %s into %s): %v", branch, base, err)
		return
	}
	result.PullRequestURL = pr.URL
	result.PullRequestNumber = pr.Number
	o.Progress.Step("opened %s", pr.URL)
}

// PostComment writes one comment to the issue, or reports what it would have
// written under a dry run.
func PostComment(ctx context.Context, o Options, result *Result, body, kind string) {
	postComment(ctx, o, result, body, kind)
}

// postComment writes one comment to the issue, or reports what it would have
// written under a dry run.
//
// A comment that cannot be posted is a warning and never a failed run: the
// work is the deliverable, and a run that has already written and verified a
// change should not be reported as a failure because a token lost a scope.
func postComment(ctx context.Context, o Options, result *Result, body, kind string) {
	if o.Input.Issue == nil {
		return
	}
	if o.DryRun {
		o.Progress.Detail("dry run: the %s comment was not posted", kind)
		return
	}
	if o.Forge == nil {
		o.Run.Warn("the %s comment could not be posted on %s: no forge client configured", kind, o.Input.Issue)
		return
	}
	url, err := o.Forge.AddComment(ctx, *o.Input.Issue, body)
	if err != nil {
		o.Run.Warn("the %s comment could not be posted on %s: %v", kind, o.Input.Issue, err)
		return
	}
	result.Comments = append(result.Comments, url)
	o.Progress.Detail("posted the %s comment", kind)
}

// runChecks runs the verification command and reports it.
func runChecks(ctx context.Context, o Options, root, command, label string) checks.Result {
	if strings.TrimSpace(command) == "" {
		return checks.Result{Skipped: true}
	}
	done := o.Progress.Begin("%s: %s", label, command)
	res := checks.Run(ctx, o.CheckRunner, root, command, o.VerifyTimeout)
	status := "passed"
	if !res.OK {
		status = fmt.Sprintf("failed (exit %d)", res.ExitCode)
	}
	done(status)
	return res
}

func recordPhase(run *toolio.Run, res agentrun.Result) {
	if run == nil || res.Name == "" {
		return
	}
	run.AddPhase(toolio.PhaseInfo{
		Name:         res.Name,
		Turns:        res.Turns,
		StopReason:   string(res.StopReason),
		InputTokens:  int64(res.Usage.InputTokens),
		OutputTokens: int64(res.Usage.OutputTokens),
		CostUSD:      res.Usage.CostUSD,
		DurationMS:   res.Elapsed.Milliseconds(),
	})
}

func phaseSummary(res agentrun.Result) string {
	s := fmt.Sprintf("· %d turns · %s↑ %s↓", res.Turns,
		toolio.FormatTokens(int64(res.Usage.InputTokens)),
		toolio.FormatTokens(int64(res.Usage.OutputTokens)))
	if res.Blocked > 0 {
		s += fmt.Sprintf(" · %d tools blocked", res.Blocked)
	}
	return s
}

// unmetCriteriaWarning names the criteria that were not met.
func unmetCriteriaWarning(criteria []Criterion, verdicts []CriterionVerdict) string {
	byID := verdictsByID(verdicts)
	var unmet []string
	for _, c := range criteria {
		if v, ok := byID[c.ID]; !ok || !v.Passed() {
			unmet = append(unmet, c.ID)
		}
	}
	return fmt.Sprintf("acceptance criteria reported as not met: %s (%d of %d); the change is "+
		"reported with the verdict and the evidence for each",
		strings.Join(unmet, ", "), len(unmet), len(criteria))
}
