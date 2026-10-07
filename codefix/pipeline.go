package codefix

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/repomap"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// Options configure one fix run.
type Options struct {
	// Input is the classified argument. When it is an issue on a forge
	// (GitHub or GitLab), the run comments on it and the pull request closes
	// it; otherwise the run works from the text and writes nothing to the
	// forge but the pull request.
	Input toolio.Input
	// Workspace roots the file tools at the repository. Required.
	Workspace *tools.Workspace
	// Repo is the target repository for the pull request. Zero means the
	// origin remote of the workspace, else the input issue's.
	Repo issuex.Repo
	// Land decides what happens once the change is verified.
	Land LandMode
	// BranchPrefix replaces the classification's prefix (fix/ for a bug,
	// feature/ otherwise) in the branch name. Empty keeps the default.
	BranchPrefix string
	// DryRun makes no REMOTE change: nothing is pushed, no pull request is
	// opened, and comments are reported instead of posted. The branch and the
	// commit are still made locally — the implementation phase edits real
	// files, so containing them on a branch you can delete is safer than
	// leaving them loose.
	DryRun bool
	// TotalBudgetUSD caps the spend of the whole run, across both model
	// phases. It is checked once, at the boundary between analyse and
	// implement, the way codeimpl checks it between tasks. Zero means no cap
	// beyond the per-phase bound.
	TotalBudgetUSD float64
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
	// NoReview skips the independent review of the change against the
	// requirement and test ids the report cites.
	NoReview bool
	// Now is the clock the run reads today's date from. Nil means time.Now.
	Now func() time.Time
	// RepoMapTokens is the token budget of the repository map in each phase's
	// user prompt (14-REQ-6.2). Zero, the zero value, disables the map.
	RepoMapTokens int

	// Index is the code-search index for this run. Nil means no indexed search.
	Index tools.Index
	// IndexUnavailable is why Index is nil: the message of the error the index
	// builder returned. RunPreflight reports it (16-REQ-7.2). Empty when the
	// index was built or the reason is not known.
	IndexUnavailable string

	// Runner drives the model phases. Required.
	Runner *agentrun.Runner
	// Forge is the forge client, GitHub or GitLab. Required unless DryRun,
	// or the input is not an issue and Land is not LandPR.
	Forge issuex.Client
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
	// buildMap builds the repository map and treeState reports whether the
	// tree moved between phases. Both are unexported and injected by tests;
	// nil means repomap.Build and the run's git wrapper.
	buildMap  repomap.BuildFunc
	treeState repomap.TreeState
}

// Failure carries the stage and category of a failed run.
type Failure struct {
	Stage    string
	Category string
	Err      error
	// TotalBudget is the --total-budget ceiling a budget stop was checked
	// against, so the envelope's fix_hint can name it. Zero otherwise.
	TotalBudget float64
	// Parked is true when the run committed the work as a wip: commit on its
	// branch and returned the checkout to the base branch before failing.
	Parked bool
}

// TotalBudgetUSD is the run-level ceiling behind a stage "budget" failure.
func (f *Failure) TotalBudgetUSD() float64 { return f.TotalBudget }

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

// IsParked reports whether err is a run that parked its work on its branch:
// whatever stopped it, the work is a wip: commit and the checkout is back on
// the base branch, which the caller reports as exit 4.
func IsParked(err error) bool {
	var f *Failure
	return errors.As(err, &f) && f.Parked
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
	// CategoryNonconformant means the change landed and the review of the
	// requirements the report cites found them unmet: the issue stays open.
	CategoryNonconformant = "nonconformant"
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
	o.applyDefaults()

	// Whether the run has an index is decided once, here, for every phase
	// (16-REQ-2.1). It is the index the Runner's phases read, and so the one
	// the pipeline invalidates after a tree change (16-REQ-4).
	idx, idxErr := agentrun.RunIndex(o.Index, o.Runner)
	if idxErr != nil {
		return nil, failf("preflight", agentrun.CategoryInternal, "%v", idxErr)
	}
	o.Index = idx
	indexed := o.Index != nil

	result := newResult(o)

	target, base, pfErr := preflight(ctx, o, git, result)
	if pfErr != nil {
		return result, pfErr
	}
	result.Repo = target.String()
	result.BaseBranch = base

	command := resolveVerifyAndBaseline(ctx, o, root, result)
	baseline := result.Baseline

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
		programs = append(programs, checks.Programs(command)...)
		b = &agentBrain{runner: o.Runner, extraPrograms: programs, codeSearch: indexed}
	}
	o.brain = b

	// The map is built before the analysis and rebuilt before the
	// implementation only when HEAD or the dirty files moved in between
	// (14-REQ-9.1, 14-REQ-9.3). The report's own text names the paths that
	// are reduced last (14-REQ-3).
	maps := repomap.NewSource(o.Workspace, o.RepoMapTokens, o.buildMap, o.treeState, git, o.Run)
	reportPaths := repomap.PathsIn(o.Input.Body)

	// ---------------------------------------------------------- analyse --
	done := o.Progress.Begin("analysing %s", o.Input.Origin)
	analysis, stats, err := b.Analyze(ctx, analysisInput{
		Input: o.Input, Baseline: baseline, VerifyCommand: command, Root: root,
		Criteria: criteria, Instructions: projectInstructions(root),
		RepoMap: maps.Get(ctx, "analyse", reportPaths),
	})
	recordPhase(o.Run, stats)
	done(toolio.PhaseSummary(stats))
	if err != nil {
		return result, fail("analyse", agentrun.CategoryOf(err), err)
	}

	result.Stage = "analysed"
	result.Classification = string(analysis.Classification)
	result.Title = analysis.Title
	result.FixSummary = analysis.Summary
	result.RootCause = analysis.RootCause
	result.Approach = analysis.Approach
	result.Assumptions = analysis.Assumptions

	if analysis.Ambiguity != nil {
		return stopOnAmbiguity(ctx, o, result, *analysis.Ambiguity)
	}

	// ------------------------------------------------ total budget check --
	//
	// The one interior boundary: the analysis is paid for and nothing has
	// been written. A run already over its ceiling stops here, before a
	// branch exists and before the expensive phase starts.
	if f := overBudget(o); f != nil {
		return result, f
	}

	// ----------------------------------------------------------- branch --
	branch := gitx.UniqueBranchName(ctx, git,
		gitx.BranchName(branchPrefix(o, analysis.Classification), result.IssueNumber, analysis.Title))
	if err := git.CreateBranch(ctx, branch); err != nil {
		return result, fail("branch", CategoryGit, err)
	}
	result.Branch = branch
	o.Progress.Step("branch", "branched %s from %s", branch, base)

	// The checkout is a Go-initiated tree change: whatever the index learned
	// while the analyse phase ran is stale before the implement phase starts
	// (16-REQ-4.1).
	invalidate(o)

	// The analysis comment goes up now: the run is known to be able to
	// start, a branch exists, and the comment can name it.
	postComment(ctx, o, result, analysisComment(analysis, criteria, branch, command, baseline),
		"analysis")

	// -------------------------------------------------------- implement --
	done = o.Progress.Begin("implementing")
	implPaths := append([]string(nil), reportPaths...)
	for _, f := range analysis.Files {
		implPaths = append(implPaths, f.Path)
	}
	before := untrackedNow(ctx, git)
	impl, stats, err := b.Implement(ctx, implementInput{
		Input: o.Input, Analysis: analysis, Baseline: baseline, VerifyCommand: command,
		Branch: branch, Root: root, Instructions: projectInstructions(root),
		Criteria: criteria, Now: o.now(),
		RepoMap: maps.Get(ctx, "implement", implPaths),
	})
	recordPhase(o.Run, stats)
	done(toolio.PhaseSummary(stats))
	// From here on the phase may have written files. A run that stops
	// before it lands parks them, so the identical command can run again.
	parkOn := func(f *Failure) (*Result, error) {
		return parkFailure(ctx, o, git, before, result, impl, analysis, base, f)
	}
	if err != nil {
		return parkOn(fail("implement", agentrun.CategoryOf(err), err))
	}
	result.Stage = "implemented"
	result.Implementation = &impl
	if strings.TrimSpace(impl.Summary) != "" {
		result.FixSummary = impl.Summary
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
		o.Run.Warn(toolio.WarnCriteriaUnmet, "high", "%s", unmet)
		o.Progress.Step("implement", "%s", unmet)
	}

	// A run that reports a fix and changed nothing is a failed run, not an
	// empty commit. The evidence is git's, not the model's report.
	changed, err := git.ChangedFiles(ctx, "HEAD")
	if err != nil {
		return parkOn(fail("verify", CategoryGit, err))
	}
	if len(changed) == 0 {
		return parkOn(failf("verify", CategoryEmpty,
			"the implementation phase reported a change and no file differs from %s; "+
				"nothing was committed", base))
	}
	result.ChangedFiles = changed
	if msg := project.MissingDocs(root, changed); msg != "" {
		o.Run.Warn(toolio.WarnDocsNotUpdated, "low", "%s", msg)
	}
	if stat, err := git.DiffStat(ctx, "HEAD"); err == nil {
		result.DiffStat = stat
	}

	// ----------------------------------------------------------- verify --
	after := runChecks(ctx, o, root, command, "verification")
	result.Verification = after
	if after.Aborted {
		// Cancelled checks say nothing about the change: nothing is compared.
		return parkOn(failf("verify", agentrun.CategoryAborted, "the run was cancelled while `%s` ran",
			after.Command))
	}
	verdict := checks.Compare(baseline, after)
	result.Verdict = string(verdict)

	landable := verdict.Landable() || (verdict == checks.VerdictUnverified && command == "")
	if !landable {
		return parkUnverified(ctx, o, git, before, result, impl, analysis, verdict, base)
	}

	// ------------------------------------------------------------ prove --
	if err := prove(ctx, o, git, root, command, changed, before, impl, result); err != nil {
		var f *Failure
		if !errors.As(err, &f) {
			f = fail("verify", CategoryGit, err)
		}
		return parkOn(f)
	}
	if len(result.Blocking) > 0 {
		o.Draft = true
	}

	// ------------------------------------------------------------- land --
	commit, err := commitOwn(ctx, o, git, before, commitMessage(analysis.Classification, impl, o.Input.Issue,
		result.ClosesIssue, commitBody(impl.Summary, after)), impl.Changes)
	if err != nil {
		return parkOn(fail("commit", CategoryGit, err))
	}
	result.Commit = commit
	result.Stage = "committed"
	o.Progress.Step("commit", "committed %s", commit)

	if o.Land.Pushes() && !o.DryRun {
		stopTiming := o.Run.Time("git", "push")
		err := git.Push(ctx, branch, o.PushAttempts, func(m string) { o.Progress.Step("push", "%s", m) })
		stopTiming()
		// A failed push is the run's error, not a Run.Warn: no warning code.
		o.Run.RecordSideEffect("push", "origin "+branch, err == nil, "")
		if err != nil {
			// The change is committed on the branch; the checkout goes
			// back to where the run started.
			returnCheckout(ctx, o, git, base)
			return result, fail("push", CategoryGit, err)
		}
		result.Pushed = true
		result.Stage = "pushed"
		o.Progress.Step("push", "pushed origin/%s", branch)
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
	if len(result.Blocking) > 0 {
		return result, failf("review", CategoryNonconformant,
			"the change landed and the review of the requirements the report cites left %d blocking "+
				"finding(s); the issue stays open and the pull request is a draft", len(result.Blocking))
	}
	return result, nil
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// prove establishes what the verified change shows, before it is committed:
// whether its tests fail with the fix taken out, what the structural checks
// find in it, and — when the report cites requirement or test ids a spec in
// the repository defines — whether an independent review finds those met.
// The issue is closed only when the tests prove the fix and the review left
// nothing blocking.
func prove(ctx context.Context, o Options, git *gitx.Git, root, command string, changed []string,
	before map[string]bool, impl Implementation, result *Result) error {

	rc, err := heldRevert(ctx, o, git, root, command, changed, before, impl)
	// The revert took the fix out and put it back: the review phase that may
	// follow searches the tree as it is now.
	invalidate(o)
	if err != nil {
		return fail("verify", CategoryGit, err)
	}
	result.RevertCheck = &rc
	if rc.Check != nil && rc.Check.Aborted {
		return failf("verify", agentrun.CategoryAborted, "the run was cancelled during the revert check")
	}
	result.ClosesIssue = o.Input.Issue != nil && rc.Proves
	if o.Input.Issue != nil && !rc.Proves {
		o.Run.Warn(toolio.WarnFixNotProven, "high", "%s is referenced, not closed: %s", o.Input.Issue, rc.Reason)
		o.Progress.Step("verify", "the fix is not proven (%s); the issue will be referenced, not closed", rc.Reason)
	}

	_, patch, err := git.DiffSince(ctx, "HEAD")
	if err != nil {
		return fail("verify", CategoryGit, err)
	}
	tracked, err := git.TrackedFiles(ctx)
	if err != nil {
		return fail("verify", CategoryGit, err)
	}
	runner := o.CheckRunner
	if runner == nil {
		runner = gitx.ReducedEnvRunner
	}
	result.Structural = conform.Scan(ctx, conform.ScanInput{Root: root, Changed: changed,
		Added: conform.ParseAdded(patch), Tracked: tracked, Today: o.now(), Runner: runner})

	if o.NoReview {
		return nil
	}
	cited := conform.FindCited(o.Input.Body, afspec.ResolveSpecsDir("", root))
	if len(cited.Specs) == 0 {
		return nil
	}
	var spec strings.Builder
	for _, s := range cited.Specs {
		spec.WriteString(s.RenderCombined())
		spec.WriteString("\n\n")
	}
	stat, _ := git.DiffStat(ctx, "HEAD")
	done := o.Progress.Begin("reviewing the fix against %s", strings.Join(append(append([]string(nil),
		cited.Scope.Requirements...), cited.Scope.Tests...), ", "))
	review, stats, err := o.brain.Review(ctx, conform.ReviewInput{
		Root: root, Base: "HEAD", Spec: spec.String(), Scope: cited.Scope, ChangedFiles: changed,
		DiffStat: stat, TestCommands: nonEmpty(command),
		Context: "## The problem report the change answers\n\n" + reportBlock(o.Input),
	})
	recordPhase(o.Run, stats)
	done(toolio.PhaseSummary(stats))
	if err != nil {
		o.Run.Warn(toolio.WarnReviewNotRun, "high", "the review of the cited requirements did not complete: %v", err)
		result.ClosesIssue = false
		return nil
	}
	result.Review = &review
	result.Blocking = review.Blockers()
	if len(result.Blocking) > 0 {
		result.ClosesIssue = false
	}
	return nil
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// applyDefaults fills the options a caller may leave zero. Run and
// RunPreflight both apply it, so the two see the same Land mode — which
// decides whether a credential and a remote are checked — and the same
// verification timeout.
func (o *Options) applyDefaults() {
	if o.Land == "" {
		o.Land = LandPR
	}
	if o.PushAttempts <= 0 {
		o.PushAttempts = 4
	}
	if o.VerifyTimeout <= 0 {
		o.VerifyTimeout = checks.DefaultTimeout
	}
}

// newResult is the result both Run and RunPreflight start from, so a run that
// refuses at its preflight stage reports the same fields either way.
func newResult(o Options) *Result {
	result := &Result{Stage: "preflight", DryRun: o.DryRun, Land: string(o.Land)}
	if o.Input.Issue != nil {
		result.IssueURL = o.Input.Issue.URL()
		result.IssueNumber = o.Input.Issue.Number
	}
	return result
}

// resolveVerifyAndBaseline settles the command that decides success — the
// operator's, else the detected one, else none under --no-verify — and runs
// it once as the baseline, recording the outcome in result.Baseline. It is
// the one block Run and RunPreflight share after Preflight, so --no-verify
// skips the baseline identically for both.
func resolveVerifyAndBaseline(ctx context.Context, o Options, root string, result *Result) string {
	command := o.VerifyCommand
	if o.NoVerify {
		command = ""
	} else if command == "" {
		command = checks.Detect(root)
		if command == "" {
			o.Run.Warn(toolio.WarnNoVerifyCommand, "high", "no verification command could be detected for %s: the change will be "+
				"reported as unverified", root)
		} else {
			o.Progress.Detail("verification command: %s", command)
		}
	}

	result.Baseline = runChecks(ctx, o, root, command, "baseline")
	return command
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
			targetBranch = git.DefaultBranch(ctx)
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

// RunPreflight is fix --preflight: every check Run performs before its first
// model call, reported and then stopped at. It calls the same Preflight and
// the same verify-and-baseline block Run calls, never a copy of either, so a
// run that would refuse refuses here with the identical stage, category and
// message, and the run is never started: no branch, no commit, no forge write
// and no model phase. The one thing it runs is the baseline verification
// command, which Run runs at the same point.
//
// A refusal returns the failure with a result carrying none of the checklist:
// a partial list of what passed before the refusal would be a second answer
// to "would this run start".
func RunPreflight(ctx context.Context, o Options) (*Result, error) {
	if o.Workspace == nil {
		return nil, failf("preflight", agentrun.CategoryInternal, "no workspace configured")
	}
	if o.Runner == nil {
		return nil, failf("preflight", agentrun.CategoryInternal, "no runner configured")
	}
	root := o.Workspace.Root
	git := o.Git
	if git == nil {
		git = gitx.New(root, nil)
	}
	o.applyDefaults()
	// The preflight reports the index the Runner's phases would read.
	idx, idxErr := agentrun.RunIndex(o.Index, o.Runner)
	if idxErr != nil {
		return nil, failf("preflight", agentrun.CategoryInternal, "%v", idxErr)
	}
	o.Index = idx

	result := newResult(o)
	target, base, pfErr := preflight(ctx, o, git, result)
	if pfErr != nil {
		return result, pfErr
	}
	result.Repo = target.String()
	result.BaseBranch = base

	command := resolveVerifyAndBaseline(ctx, o, root, result)

	var list []toolio.PreflightCheck
	add := func(check string, ok bool, detail string) {
		list = append(list, toolio.PreflightCheck{Check: check, OK: ok, Detail: detail})
	}
	add("git_repository", true, "")
	add("clean_tree", true, "")
	add("base_branch", true, base)
	if o.Pull || o.PullBranch != "" {
		add("pull", true, "checked out and pulled "+base)
	}
	// The conditions below are the ones Preflight refuses on, so reaching
	// this line means each of them held.
	if !o.DryRun && (o.Input.Issue != nil || o.Land == LandPR) {
		add("forge_credential", true, "")
	}
	if o.Land == LandPR && !o.DryRun {
		add("land_target", true, target.String())
	}
	if o.Land.Pushes() && !o.DryRun {
		add("remote_configured", true, "origin")
	}
	switch {
	case command != "":
		add("verify_command", true, command)
	case o.NoVerify:
		add("verify_command", false, "none: --no-verify was given")
	default:
		add("verify_command", false, "none detected")
	}
	if !o.NoVerify {
		b := result.Baseline
		switch {
		case !b.Ran():
			add("verify_baseline", false, "not run: no verification command")
		case b.OK:
			add("verify_baseline", true, "passed")
		default:
			add("verify_baseline", false, fmt.Sprintf("failed (exit %d)", b.ExitCode))
		}
	}
	if backend, err := agentrun.DetectSymbolBackend(o.Workspace); err == nil {
		add("symbol_backend", true, backend)
	}
	add("code_search_index", true, indexDetail(o.Index, o.IndexUnavailable))
	result.Preflight = list

	maxTurns, maxBudget := o.Runner.ResolvedBounds()
	const phases = 2 // analyse and implement, fixed
	result.Estimate = &toolio.Estimate{
		Phases:               phases,
		MaxTurnsPerPhase:     maxTurns,
		MaxBudgetPerPhaseUSD: maxBudget,
		MaxTotalUSD:          phases * maxBudget,
	}

	// --preflight already means "make no remote change", so --dry-run adds
	// nothing to it and must not change the envelope (11-REQ-1.4). Refusals
	// above keep the real DryRun: they are the ordinary run's own result.
	result.DryRun = false
	return result, nil
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
	o.Progress.Step("analyse", "stopped: the report reads two ways and the code cannot settle which")
	return result, fail("analyse", CategoryAmbiguous, fmt.Errorf(
		"the input is ambiguous and the codebase cannot settle it: %s", strings.TrimSpace(a.Question)))
}

// heldRevert runs the revert check with the change held in a commit. While
// the fix is taken out, the only other copy is in this process's memory, and
// a process killed then would lose it; the hold commit keeps it on the
// branch. The commit is undone afterwards, whatever happened, under a context
// a cancellation does not reach, so the tree is as it was before.
func heldRevert(ctx context.Context, o Options, git *gitx.Git, root, command string, changed []string,
	before map[string]bool, impl Implementation) (_ conform.RevertResult, err error) {

	head, err := git.Head(ctx)
	if err != nil {
		return conform.RevertResult{}, err
	}
	if _, err := commitOwnHooks(ctx, o, git, before, revertHoldMessage(impl), nil, true); err != nil {
		return conform.RevertResult{}, err
	}
	defer func() {
		bg, cancel := background(ctx)
		defer cancel()
		if rerr := git.ResetSoft(bg, head); rerr != nil && err == nil {
			err = fmt.Errorf("the revert check's hold commit could not be undone: %w", rerr)
		}
	}()
	return conform.Revert(ctx, git, root, head, changed, func(ctx context.Context) checks.Result {
		return runChecks(ctx, o, root, command, "revert check")
	})
}

// background is the context the git steps that must complete run under: one
// a cancellation of the run does not reach, bounded so a hung git cannot hold
// the run forever.
func background(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
}

// parkWork commits what the implementation phase left as a wip: commit,
// when it left anything, and returns the checkout to the base branch. It
// runs under a context the cancellation does not reach: Ctrl-C is the case
// it exists for most, and a park the cancelled context killed mid-commit
// would leave the dirty tree parking is meant to prevent. It reports whether
// the work is committed.
func parkWork(ctx context.Context, o Options, git *gitx.Git, before map[string]bool, result *Result,
	message string, changes []FileChange, base string) bool {

	bg, cancel := background(ctx)
	defer cancel()
	parked := false
	if changed, err := git.ChangedFiles(bg, "HEAD"); err != nil || ownChange(changed, before) {
		if commit, err := commitOwn(bg, o, git, before, message, changes); err != nil {
			o.Run.Warn(toolio.WarnCommitNotParked, "high", "the work could not be committed on %s: %v",
				result.Branch, err)
		} else {
			result.Commit = commit
			parked = true
		}
	}
	returnCheckout(bg, o, git, base)
	return parked
}

// ownChange reports whether changed holds a file the run made: anything but
// the untracked files that were there before it.
func ownChange(changed []string, before map[string]bool) bool {
	for _, f := range changed {
		if !before[f] {
			return true
		}
	}
	return false
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// returnCheckout puts the checkout back on the base branch, under a context
// a cancellation does not reach.
func returnCheckout(ctx context.Context, o Options, git *gitx.Git, base string) {
	bg, cancel := background(ctx)
	defer cancel()
	if err := git.Checkout(bg, base); err != nil {
		o.Run.Warn(toolio.WarnCheckoutNotRestored, "low", "could not return to %s: %v", base, err)
	}
}

// parkFailure ends a run that stopped after the implementation phase began
// and before the change landed: the phase failed or was cancelled, the
// checks were cancelled, the proof or the commit failed. The work is parked
// like an unverified change, the failure keeps its own category, and it is
// marked parked so the caller exits 4. A run that left nothing to park only
// returns the checkout.
func parkFailure(ctx context.Context, o Options, git *gitx.Git, before map[string]bool, result *Result,
	impl Implementation, analysis Analysis, base string, f *Failure) (*Result, error) {

	result.Stage = "stopped"
	subject := firstNonEmpty(impl.CommitSubject, analysis.Title)
	if parkWork(ctx, o, git, before, result, parkedCommitMessage(subject, o.Input.Issue, f), impl.Changes, base) {
		f.Parked = true
		f.Err = fmt.Errorf("%w. The work is parked on %s (%s) and the checkout is back on %s",
			f.Err, result.Branch, result.Commit, base)
		o.Progress.Step(f.Stage, "stopped (%s); work parked on %s", f.Category, result.Branch)
	}
	return result, f
}

// parkUnverified commits the work as a wip: commit, returns the checkout to
// the base branch, and reports the failure.
//
// The work is kept rather than discarded because the implementation phase
// edited real files and a person may want to look at them; it is committed
// rather than left loose because a branch you can delete is easier to reason
// about than a dirty tree. It is emphatically not landed.
func parkUnverified(ctx context.Context, o Options, git *gitx.Git, before map[string]bool, result *Result,
	impl Implementation, analysis Analysis, verdict checks.Verdict, base string) (*Result, error) {

	result.Stage = "unverified"
	parked := parkWork(ctx, o, git, before, result, wipCommitMessage(impl, o.Input.Issue, verdict), impl.Changes, base)
	postComment(ctx, o, result, failureComment(result), "failure")

	o.Progress.Step("verify", "checks did not pass (%s); work parked on %s", verdict, result.Branch)
	f := failf("verify", CategoryUnverified,
		"`%s` did not pass after the change (%s); the work is on %s and was not landed",
		result.Verification.Command, verdict, result.Branch)
	f.Parked = parked
	return result, f
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
		o.Run.Warn(toolio.WarnPullRequestNotOpened, "high", "the pull request could not be opened (the branch is pushed; open it by "+
			"hand from %s into %s): no forge client configured", branch, base)
		o.Run.RecordSideEffect("open_pr", target.String(), false, toolio.WarnPullRequestNotOpened)
		return
	}
	stopTiming := o.Run.Time("forge", "open_pr")
	pr, err := o.Forge.CreatePullRequest(ctx, target, issuex.CreatePullRequestRequest{
		Title: pullRequestTitle(analysis.Classification, impl, o.Input.Issue),
		Body:  pullRequestBody(result),
		Head:  branch,
		Base:  base,
		Draft: o.Draft,
	})
	stopTiming()
	if err != nil {
		o.Run.Warn(toolio.WarnPullRequestNotOpened, "high", "the pull request could not be opened (the branch is pushed; open it by "+
			"hand from %s into %s): %v", branch, base, err)
		o.Run.RecordSideEffect("open_pr", target.String(), false, toolio.WarnPullRequestNotOpened)
		return
	}
	o.Run.RecordSideEffect("open_pr", fmt.Sprintf("%s#%d", target, pr.Number), true, "")
	result.PullRequestURL = pr.URL
	result.PullRequestNumber = pr.Number
	o.Progress.Step("land", "opened %s", pr.URL)
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
		result.dryRunComments = append(result.dryRunComments, kind)
		o.Progress.Detail("dry run: the %s comment was not posted", kind)
		return
	}
	if o.Forge == nil {
		o.Run.Warn(toolio.WarnCommentNotPosted, "low", "the %s comment could not be posted on %s: no forge client configured", kind, o.Input.Issue)
		o.Run.RecordSideEffectOf("comment", kind, o.Input.Issue.String(), "", false, toolio.WarnCommentNotPosted)
		return
	}
	stopTiming := o.Run.Time("forge", "comment:"+kind)
	url, err := o.Forge.AddComment(ctx, *o.Input.Issue, body)
	stopTiming()
	if err != nil {
		o.Run.Warn(toolio.WarnCommentNotPosted, "low", "the %s comment could not be posted on %s: %v", kind, o.Input.Issue, err)
		o.Run.RecordSideEffectOf("comment", kind, o.Input.Issue.String(), "", false, toolio.WarnCommentNotPosted)
		return
	}
	o.Run.RecordSideEffectOf("comment", kind, o.Input.Issue.String(), url, true, "")
	result.Comments = append(result.Comments, url)
	result.CommentRefs = append(result.CommentRefs, CommentRef{Kind: kind, URL: url})
	o.Progress.Detail("posted the %s comment", kind)
}

// invalidate marks the whole code-search index stale after a Go-initiated
// change to the tree, so the next phase's code_search results reflect it
// (16-REQ-4). A run without an index does nothing.
func invalidate(o Options) {
	if o.Index != nil {
		o.Index.Invalidate("")
	}
}

// runChecks runs the verification command and reports it. The command is the
// project's own program and can write into the tree, so the index is
// invalidated once it has run.
func runChecks(ctx context.Context, o Options, root, command, label string) checks.Result {
	if strings.TrimSpace(command) == "" {
		return checks.Result{Skipped: true}
	}
	defer invalidate(o)
	done := o.Progress.Begin("%s: %s", label, command)
	res := checks.Run(ctx, o.CheckRunner, root, command, o.VerifyTimeout)
	o.Progress.Check(res)
	o.Run.AddTiming("check", label, time.Duration(res.DurationMS)*time.Millisecond)
	status := "passed"
	if !res.OK {
		status = fmt.Sprintf("failed (exit %d)", res.ExitCode)
	}
	done(status)
	return res
}

// overBudget reports the run-level cap, checked between the two phases. The
// comparison and the wording follow codeimpl's overBudget.
func overBudget(o Options) *Failure {
	spent := o.Run.CostUSD()
	if o.TotalBudgetUSD > 0 && spent >= o.TotalBudgetUSD {
		f := failf("budget", agentrun.CategoryBudget,
			"the run has spent $%.2f of its $%.2f total budget on the analysis; the implementation "+
				"phase was not started", spent, o.TotalBudgetUSD)
		f.TotalBudget = o.TotalBudgetUSD
		return f
	}
	return nil
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

func recordPhase(run *toolio.Run, res agentrun.Result) {
	if run == nil || res.Name == "" {
		return
	}
	run.AddPhase(toolio.PhaseFromResult(res, ""))
}

// branchPrefix is the first segment of the branch name: the configured one,
// else the classification's.
func branchPrefix(o Options, c Classification) string {
	if o.BranchPrefix != "" {
		return o.BranchPrefix
	}
	return c.BranchPrefix()
}

// indexDetail is the detail of the informational code_search_index preflight
// check (16-REQ-7): "built" when the run has an index, otherwise why it has
// none. The fallback is search_files, so the check never refuses the run.
func indexDetail(idx tools.Index, reason string) string {
	if idx != nil {
		return "built"
	}
	if reason == "" {
		reason = "index not built"
	}
	return "unavailable: " + reason
}
