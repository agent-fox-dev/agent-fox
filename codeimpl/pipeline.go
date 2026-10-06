package codeimpl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/agentfox/agentkit-go/core"
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

// RunState is what the pipeline establishes in pre-flight and carries
// through the task loop.
type RunState struct {
	Target issuex.Repo

	root       string
	git        *gitx.Git
	base       string
	target     issuex.Repo
	specsDir   string
	specDir    string
	relSpecDir string
	spec       *afspec.Spec
	profile    project.Profile
	gate       []string
	baseline   GateResult
	branch     string
	exists     bool
	todo       []int
	brain      brain
	// maps hands each phase its repository map, rebuilt when the tree changed.
	maps   *repomap.Source
	survey *Survey
	// start is the commit the branch's whole change is measured from: where
	// it left the base branch.
	start string
	prior []priorTask
	cost  float64
	// upstreamVerified and upstreamUnchecked are what checkUpstream found of
	// the spec's dependencies: those it confirmed sealed or done, and those it
	// could not look at (a missing or unreadable package). Preflight reports
	// them as they are.
	upstreamVerified, upstreamUnchecked int
	// index is the run's code-search index (Options.Index), nil when it has
	// none. It is kept on the state so that the helpers that change the tree
	// — discard, dropScratchFiles — invalidate it themselves, wherever they
	// are called from.
	index tools.Index
}

type runState = RunState

// invalidate marks the whole code-search index stale after a Go-initiated
// change to the tree, so the next phase's code_search results reflect it
// (16-REQ-4). A false positive is a no-op; a false negative returns results
// from a tree that no longer exists. A run without an index does nothing.
func (st *runState) invalidate() {
	if st.index != nil {
		st.index.Invalidate("")
	}
}

// runGate runs the project's checks and invalidates the index afterwards: the
// checks are the project's own programs, and they can write into the tree
// (generated code, snapshots, build output) the next phase will search.
func (st *runState) runGate(ctx context.Context, o Options, label string) GateResult {
	defer st.invalidate()
	return runGate(ctx, o, st.root, st.gate, label)
}

// Run drives the whole pipeline.
//
// The order is the shape of the program:
//
//	pre-flight   every check that can refuse the run, BEFORE a token is
//	             spent: the repository, the branch, the package, its
//	             validity and status, its test commands, its upstream specs
//	branch       chosen — and, when it already exists, checked out — before
//	             the package is read, so that a second run reads the state
//	             the first one committed
//	baseline     the spec's own checks, once, so "the checks pass" later
//	             has something to mean
//	survey       model, read-only. The one place the run may stop to ask.
//	repair       only when asked for and the baseline is red: model, with
//	             write tools, until the gate is green or the attempts are
//	             spent. Its commit is the first on the branch.
//	tasks        for each task the plan has not done: model, then git's
//	             account of the change, then the checks again, then — only
//	             on a landable comparison — the state write and the commit.
//	             When asked for, the integration task's red checks go to
//	             the same repair loop, on top of its work, before it lands
//	conformance  after the last task: the structural checks, the scope check,
//	             the checks again in a clean environment, an independent
//	             review; one phase to fix or declare what they find. What is
//	             left decides whether the pull request says the work is done
//	land         push, open the pull request — a draft when the conformance
//	             stage left blocking findings
func Run(ctx context.Context, o Options) (*Result, error) {
	if o.Workspace == nil {
		return nil, failf("preflight", agentrun.CategoryInternal, "no workspace configured")
	}
	if o.Runner == nil && o.brain == nil {
		return nil, failf("preflight", agentrun.CategoryInternal, "no runner configured")
	}
	o.applyDefaults()

	// Whether the run has an index is decided once, here, for every phase
	// (16-REQ-2.1). The index itself reaches the model through the Runner's
	// Config.Index, which the caller builds from the same Options.Index.
	indexed := o.Index != nil

	result := newResult(o)
	st, pfErr := preflight(ctx, o, result)
	if pfErr != nil {
		return result, pfErr
	}
	if len(st.todo) == 0 {
		result.Stage = "complete"
		o.Progress.Step("preflight", "every task of %s is done; nothing to implement", filepath.Base(st.specDir))
		return result, nil
	}

	b := o.brain
	if b == nil {
		programs := append([]string(nil), o.AllowPrograms...)
		for _, cmd := range st.gate {
			if p := checks.Program(cmd); p != "" {
				programs = append(programs, p)
			}
		}
		b = &agentBrain{runner: o.Runner, repairRunner: o.RepairRunner, extraPrograms: programs, protected: st.specDir,
			noTestFirst: o.NoTestFirst, codeSearch: indexed}
	}
	st.brain = b
	st.maps = repomap.NewSource(o.Workspace, o.RepoMapTokens, o.buildMap, o.treeState, st.git, o.Run)
	repair := o.Repair && len(st.baseline.failing()) > 0

	// ----------------------------------------------------------- survey --
	if !o.NoSurvey {
		if stop := overBudget(o, st); stop != nil {
			return stopped(ctx, o, st, result, stop)
		}
		done := o.Progress.Begin("surveying %s against %s", filepath.Base(st.specDir), st.root)
		survey, stats, err := b.Survey(ctx, surveyInput{
			Spec: st.spec, Root: st.root, Profile: st.profile, Gate: st.gate,
			Baseline: st.baseline, Pending: pendingTasks(st.spec, st.todo), Repair: repair,
			Context: o.Input.Context,
			RepoMap: st.maps.Get(ctx, PhaseSurvey, specPaths(st, nil)),
		})
		recordPhase(o.Run, st, stats)
		done(toolio.PhaseSummary(stats))
		result.CostUSD = st.cost
		if err != nil {
			return stopped(ctx, o, st, result, fail(PhaseSurvey, agentrun.CategoryOf(err), err))
		}
		st.survey = &survey
		result.Survey = &survey
		result.Stage = "surveyed"
		if survey.Blocker != nil {
			result.Blocker = survey.Blocker
			o.Progress.Step("survey", "stopped: the spec cannot be implemented as written")
			return stopped(ctx, o, st, result, failf(PhaseSurvey, CategoryBlocked,
				"the spec cannot be implemented as written: %s", strings.TrimSpace(survey.Blocker.Reason)))
		}
	}

	// ----------------------------------------------------------- branch --
	if !st.exists {
		if err := st.git.CreateBranch(ctx, st.branch); err != nil {
			return result, fail("branch", CategoryGit, err)
		}
		// The checkout moved the tree to the new branch: the first phase that
		// follows — repair, or task 1 — must search it (16-REQ-4.2).
		st.invalidate()
		o.Progress.Step("branch", "branched %s from %s", st.branch, st.base)
	}
	start, err := st.git.MergeBase(ctx, st.base)
	if err != nil {
		return result, fail("branch", CategoryGit, err)
	}
	st.start = start

	// ----------------------------------------------------------- repair --
	// Once, before the first task, and only on a red baseline: a green one
	// has nothing to repair, and a run without the flag is judged by
	// comparison instead.
	if repair {
		result.Stage = "repairing"
		if err := runRepair(ctx, o, st, result); err != nil {
			return result, err
		}
		result.Stage = "repaired"
	}
	result.Stage = "implementing"

	// ------------------------------------------------------------ tasks --
	for _, id := range st.todo {
		task, _ := st.spec.Tasks.GetTask(id)
		if dep, state, blocked := notReadyByID(st.spec, id); blocked {
			return stopped(ctx, o, st, result, failf("task", CategoryBlocked,
				"task %d depends on task %d, which is %s; decide whether task %d still applies",
				id, dep, state, id))
		}
		if stop := overBudget(o, st); stop != nil {
			return stopped(ctx, o, st, result, stop)
		}
		if err := transition(st.spec, id, afspec.TaskStateInProgress); err != nil {
			return result, fail("task", agentrun.CategoryInternal, err)
		}
		report, err := runTask(ctx, o, st, result, *task)
		setReport(result, report)
		result.CostUSD = st.cost
		if err != nil {
			return result, err
		}
	}

	// ------------------------------------------------------ conformance --
	if err := conformance(ctx, o, st, result); err != nil {
		return result, err
	}
	nonconformant := len(result.Blocking) > 0
	if nonconformant {
		o.Progress.Step(conform.PhaseReview, "%d blocking finding(s) were neither fixed nor declared; "+
			"the pull request will be a draft", len(result.Blocking))
		o.Draft = true
	}

	// ------------------------------------------------------------- land --
	result.Stage = "committed"
	if o.Land.Pushes() && !o.DryRun {
		stopTiming := o.Run.Time("git", "push")
		err := st.git.Push(ctx, st.branch, o.PushAttempts, func(m string) { o.Progress.Step("push", "%s", m) })
		stopTiming()
		// A failed push is the run's error, not a Run.Warn: no warning code.
		o.Run.RecordSideEffect("push", "origin "+st.branch, err == nil, "")
		if err != nil {
			return result, fail("push", CategoryGit, err)
		}
		result.Pushed = true
		result.Stage = "pushed"
		o.Progress.Step("push", "pushed origin/%s", st.branch)
	}
	trackDeviations(ctx, o, st, result)
	if o.Land == LandPR && result.Pushed && st.target.Valid() {
		// A failed pull request is not a failed run: the branch is pushed
		// and every task on it is verified, so the work is safe and a person
		// can open the request by hand. LandPRChanges records the warning.
		_, _ = LandPRChanges(ctx, o, st, result)
	}
	switch {
	case o.Land == LandPR && result.PullRequestURL != "":
		result.Stage = "landed"
	case o.Land == LandBranch && result.Pushed:
		result.Stage = "landed"
	case o.Land == LandNone:
		result.Stage = "landed"
	}
	o.Progress.Step("land", "%d task(s) landed on %s", result.TasksDone, st.branch)
	if nonconformant {
		keys := make([]string, len(result.Blocking))
		for i, b := range result.Blocking {
			keys[i] = b.Key
		}
		return result, failf(conform.PhaseReview, CategoryNonconformant,
			"every task landed, and %d blocking finding(s) were neither fixed nor declared (%s); the work "+
				"is on %s and is not presented as done", len(keys), strings.Join(keys, ", "), st.branch)
	}
	return result, nil
}

// specPaths are the paths of the run's own input that the map reduces last
// (14-REQ-3): the spec package, and the files the current task touches.
func specPaths(st *runState, task *afspec.Task) []string {
	paths := []string{st.relSpecDir}
	if task != nil {
		paths = append(paths, task.Touches...)
	}
	return paths
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
	if o.TaskAttempts <= 0 {
		o.TaskAttempts = DefaultTaskAttempts
	}
	if o.RepairAttempts <= 0 {
		o.RepairAttempts = DefaultRepairAttempts
	}
}

// newResult is the result both Run and RunPreflight start from, so a run that
// refuses at its preflight stage reports the same fields either way.
func newResult(o Options) *Result {
	return &Result{Stage: "preflight", DryRun: o.DryRun, Land: string(o.Land), Verdict: string(checks.VerdictUnverified)}
}

// LandPRChanges opens the pull request — or, on GitLab, the merge request —
// for the pushed branch through the forge client, and degrades to a warning
// when it cannot: the work is already on the remote, and a run that wrote and
// verified every task is not reported as failed over a permissions error.
func LandPRChanges(ctx context.Context, o Options, st *RunState, result *Result) (*Result, error) {
	if o.Forge == nil {
		o.Run.Warn(toolio.WarnPullRequestNotOpened, "high", "the pull request could not be opened (the branch is pushed; open it by "+
			"hand from %s into %s): no forge client configured", st.branch, st.base)
		o.Run.RecordSideEffect("open_pr", st.target.String(), false, toolio.WarnPullRequestNotOpened)
		return result, failf("land", CategoryForge, "no forge client configured")
	}
	stopTiming := o.Run.Time("forge", "open_pr")
	pr, err := o.Forge.CreatePullRequest(ctx, st.target, issuex.CreatePullRequestRequest{
		Title: pullRequestTitle(st.spec),
		Body:  pullRequestBody(result),
		Head:  st.branch,
		Base:  st.base,
		Draft: o.Draft,
	})
	stopTiming()
	if err != nil {
		o.Run.Warn(toolio.WarnPullRequestNotOpened, "high", "the pull request could not be opened (the branch is pushed; open it by "+
			"hand from %s into %s): %v", st.branch, st.base, err)
		o.Run.RecordSideEffect("open_pr", st.target.String(), false, toolio.WarnPullRequestNotOpened)
		return result, err
	}
	o.Run.RecordSideEffect("open_pr", fmt.Sprintf("%s#%d", st.target, pr.Number), true, "")
	result.PullRequestURL = pr.URL
	result.PullRequestNumber = pr.Number
	if o.Progress != nil {
		o.Progress.Step("land", "opened %s", pr.URL)
	}
	result.Stage = "landed"
	return result, nil
}

// RunPreflight is impl --preflight: every check Run performs before its first
// model call, reported and then stopped at. It calls the one preflight
// function Run calls, never a copy of it, so a run that would refuse refuses
// here with the identical stage, category and message. That function already
// runs the baseline gate (the one command impl runs before any change), picks
// the tasks and reports them in result.Tasks; branch creation, the survey and
// every task happen in Run, which RunPreflight never reaches.
//
// What preflight itself does to the repository is therefore not suppressed:
// --pull fetches and fast-forwards the base branch, an existing continuation
// branch is checked out (and a parked wip: commit at its head discarded, as
// the ordinary run does), and the checkout is left where preflight put it.
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
	o.applyDefaults()

	result := newResult(o)
	st, pfErr := preflight(ctx, o, result)
	if pfErr != nil {
		return result, pfErr
	}

	var list []toolio.PreflightCheck
	add := func(check string, ok bool, detail string) {
		list = append(list, toolio.PreflightCheck{Check: check, OK: ok, Detail: detail})
	}
	// The conditions below are the ones preflight refuses on, so reaching this
	// line means each of them held.
	add("git_repository", true, "")
	add("clean_tree", true, "")
	if o.Pull {
		add("pull", true, "checked out and pulled "+st.base)
	}
	add("spec_resolved", true, st.relSpecDir)
	if o.Land == LandPR && !o.DryRun {
		add("forge_credential", true, "")
		add("land_target", true, st.target.String())
	}
	if o.Land.Pushes() && !o.DryRun {
		add("remote_configured", true, "origin")
	}
	if st.exists {
		add("branch", true, "continuing "+st.branch)
	} else {
		add("branch", true, st.branch+" (will be created)")
	}
	add("spec_valid", true, "")
	add("spec_status", true, st.spec.Status)
	switch {
	case o.NoVerify:
		add("test_commands", true, "skipped: --no-verify")
	case o.VerifyCommand != "":
		add("test_commands", true, "skipped: --verify replaces the spec's commands: "+o.VerifyCommand)
	case len(st.gate) == 0:
		add("test_commands", true, "the spec names no test commands")
	default:
		add("test_commands", true, strings.Join(st.gate, " · "))
	}
	if n := len(st.spec.Tasks.Dependencies); n > 0 {
		// What checkUpstream verified, not how many there are: a dependency it
		// could not look at is not reported as sealed or done. The entry stays
		// ok, since the ordinary run tolerates it (with an upstream_missing
		// warning).
		if st.upstreamUnchecked == 0 {
			add("dependencies", true, fmt.Sprintf("%d upstream spec(s) sealed or done", n))
		} else {
			add("dependencies", true, fmt.Sprintf("%d upstream spec(s): %d verified sealed or done, %d could not be checked",
				n, st.upstreamVerified, st.upstreamUnchecked))
		}
	} else {
		add("dependencies", true, "no upstream specs")
	}
	// With nothing to implement preflight returns before the baseline runs,
	// so st.baseline is empty: that is reported as not run, never as a pass.
	// With no gate nothing could run, and the entry is absent.
	if len(st.gate) > 0 {
		switch failing := st.baseline.failing(); {
		case len(st.todo) == 0:
			add("verify_baseline", false, "not run: no task remains to implement")
		case st.baseline.OK():
			add("verify_baseline", true, "passed")
		case len(failing) > 0:
			parts := make([]string, len(failing))
			for i, c := range failing {
				parts[i] = fmt.Sprintf("`%s` (exit %d)", c.Command, c.ExitCode)
			}
			add("verify_baseline", false, "failed: "+strings.Join(parts, ", "))
		default:
			add("verify_baseline", false, "failed")
		}
	}
	if o.RepairRunner != nil {
		// The model and the effort the repair phase will run at, so what the
		// flags asked for is what preflight reports.
		detail := ""
		if m := o.RepairRunner.Model(); m != nil {
			detail = fmt.Sprintf("%s (%s)", m.ID, m.Provider)
			if t := o.RepairRunner.Thinking(); t != core.ThinkingUnset {
				detail += fmt.Sprintf(", effort %s", t)
			}
		}
		add("repair_model_credential", true, detail)
	}
	if backend, err := agentrun.DetectSymbolBackend(o.Workspace); err == nil {
		add("symbol_backend", true, backend)
	}
	add("code_search_index", true, indexDetail(o.Index, o.IndexUnavailable))
	result.Preflight = list

	// The phases the plan on disk already decides: the survey (unless
	// skipped, and never when there is nothing to implement), one per
	// pending task, and the conformance review (unless skipped). A repair or
	// resolve phase is decided by what the run finds, so it is not counted.
	phases := len(st.todo)
	if phases > 0 && !o.NoSurvey {
		phases++
	}
	if phases > 0 && !o.NoReview {
		phases++ // the conformance review; its resolve phase runs only on findings
	}
	maxTurns, maxBudget := o.Runner.ResolvedBounds()
	result.Estimate = &toolio.Estimate{
		Phases:               phases,
		MaxTurnsPerPhase:     maxTurns,
		MaxBudgetPerPhaseUSD: maxBudget,
		MaxTotalUSD:          float64(phases) * maxBudget,
	}
	return result, nil
}

// Preflight runs every check that can refuse the run, in the order that
// costs least when it refuses.
func Preflight(ctx context.Context, o Options, result *Result) (*RunState, *Failure) {
	st, err := preflight(ctx, o, result)
	if st != nil {
		st.Target = st.target
	}
	return st, err
}

// preflight is every check that can refuse the run, in the order that
// costs least when it refuses.
func preflight(ctx context.Context, o Options, result *Result) (*runState, *Failure) {
	st := &runState{root: o.Workspace.Root, index: o.Index}
	st.git = o.Git
	if st.git == nil {
		st.git = gitx.New(st.root, nil)
	}
	git := st.git

	if !git.IsRepo(ctx) {
		return nil, failf("preflight", "usage", "%s is not a git repository; pass --dir", st.root)
	}
	dirty, err := git.DirtyFiles(ctx)
	if err != nil {
		return nil, fail("preflight", CategoryGit, err)
	}
	if len(dirty) > 0 {
		return nil, failf("preflight", "usage",
			"the working tree in %s has %d uncommitted change(s); commit or stash them first:\n%s",
			st.root, len(dirty), strings.Join(dirty, "\n"))
	}

	// The base branch is captured HERE, before any checkout. Asked later it
	// would name the work branch, and a pull request would target itself.
	st.base = git.BaseBranch(ctx)
	if o.Pull {
		if err := git.Checkout(ctx, st.base); err != nil {
			return nil, fail("preflight", CategoryGit, err)
		}
		if err := git.Pull(ctx, st.base); err != nil {
			return nil, fail("preflight", CategoryGit, err)
		}
	}
	result.BaseBranch = st.base

	// The package, by reference. It must be inside the repository: the
	// commit that lands a task carries the task's state, and a package
	// outside the work tree could carry nothing.
	st.specsDir = ResolveSpecsDir(o.SpecsDir, st.root)
	dir, err := Locate(o.Input, st.root, st.specsDir)
	if err != nil {
		return nil, fail("preflight", "usage", err)
	}
	// The workspace root is symlink-resolved; the package has to be too, or
	// a repository reached through a link would look like it is outside
	// itself.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	rel, err := filepath.Rel(st.root, dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return nil, failf("preflight", "usage",
			"%s is outside the repository %s; the task state is committed beside the work, so "+
				"the package has to be inside it", dir, st.root)
	}
	st.specDir, st.relSpecDir = dir, rel
	result.SpecDir = rel

	// A run that will write to the forge checks that it can before it spends
	// money on a model.
	st.target = o.Repo
	if !st.target.Valid() {
		if r, ok := issuex.DetectRepo(st.root); ok {
			st.target = r
		}
	}
	if o.Land == LandPR && !o.DryRun && (o.Forge == nil || !o.Forge.Authenticated()) {
		return nil, failf("preflight", "auth",
			"opening a pull request needs a credential: set GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN, "+
				"or pass --land=branch, --land=none or --dry-run")
	}
	if o.Land == LandPR && !o.DryRun && !st.target.Valid() {
		return nil, failf("preflight", "usage",
			"--land=pr needs a target repository: %s has no origin remote on a recognized forge — pass "+
				"--repo owner/repo, or --land=branch", st.root)
	}
	if o.Land.Pushes() && !o.DryRun && !git.HasRemote(ctx) {
		return nil, failf("preflight", "usage",
			"--land=%s pushes, and %s has no origin remote; pass --land=none", o.Land, st.root)
	}
	if st.target.Valid() {
		result.Repo = st.target.String()
	}
	st.Target = st.target

	// The branch is decided before the package is read, because the package
	// on the branch is what a second run continues from.
	peek, err := afspec.LoadSpec(dir)
	if err != nil {
		return nil, failf("preflight", "usage", "%s could not be loaded: %v", rel, err)
	}
	st.branch = strings.TrimSpace(o.Branch)
	if st.branch == "" {
		st.branch = "impl/" + peek.SpecID + "-" + gitx.Slug(peek.Title)
	}
	if git.LocalBranchExists(ctx, st.branch) {
		if err := git.Checkout(ctx, st.branch); err != nil {
			return nil, fail("preflight", CategoryGit, err)
		}
		st.exists = true
		result.Resumed = true
		o.Progress.Step("preflight", "continuing on %s", st.branch)
		if msg, err := git.HeadMessage(ctx); err == nil {
			what := ""
			if n, parked := parkedTask(msg); parked {
				what = fmt.Sprintf("task %d", n)
			} else if parkedRepair(msg) {
				what = "repairing the checks"
			}
			if what != "" {
				head, _ := git.Head(ctx)
				if err := git.ResetHard(ctx, "HEAD~1"); err != nil {
					return nil, fail("preflight", CategoryGit, err)
				}
				if err := git.Clean(ctx); err != nil {
					return nil, fail("preflight", CategoryGit, err)
				}
				o.Run.Warn(toolio.WarnParkedAttemptDiscarded, "low", "discarded the parked attempt at %s (%s); the work starts again "+
					"from the last landed commit", what, head)
			}
		}
	}
	// An existing branch was checked out, and a parked attempt may have been
	// reset away: the survey must not see the tree the run started on
	// (16-REQ-4.2).
	if st.exists {
		st.invalidate()
	}
	result.Branch = st.branch

	spec, err := afspec.LoadSpec(dir)
	if err != nil {
		return nil, failf("preflight", "usage", "%s could not be loaded: %v", rel, err)
	}
	st.spec = spec
	result.SpecID, result.SpecName, result.Title, result.Status = spec.SpecID, spec.SpecName, spec.Title, spec.Status

	v := spec.Validate()
	for _, w := range v.Warnings {
		if w.Check == "" {
			o.Run.Warn(toolio.WarnSpecValidationWarning, "low", "%s", w.Message)
			continue
		}
		o.Run.Warn(toolio.WarnSpecValidationWarning, "low", "%s: %s", w.Check, w.Message)
	}
	if !v.Valid {
		return nil, failf("preflight", CategoryInvalidSpec,
			"%s does not validate, so it is not a complete plan; %d error(s):\n%s", rel,
			len(v.Errors), strings.TrimRight(afspec.FormatValidationEntries(v.Errors), "\n"))
	}
	switch spec.Status {
	case "active":
	case "draft":
		o.Run.Warn(toolio.WarnDraftPackage, "low", "%s is a draft: it validates, so it is implemented, but its intent is not yet "+
			"frozen by activation", rel)
	default:
		return nil, failf("preflight", "usage", "%s is %s; there is nothing to implement", rel, spec.Status)
	}

	// The checks the run is judged by, refused before they are paid for.
	st.profile = project.DetectProfile(st.root)
	st.gate = gateCommands(spec.Tasks.TestCommands, o.VerifyCommand, o.NoVerify)
	if o.VerifyCommand == "" && !o.NoVerify {
		if err := st.profile.AuditTestCommands(spec.Tasks.TestCommands); err != nil {
			return nil, fail("preflight", "usage", err)
		}
		for _, f := range []struct{ name, cmd string }{
			{"linter", spec.Tasks.TestCommands.Linter}, {"all_tests", spec.Tasks.TestCommands.AllTests},
		} {
			if err := checkCommandShape(f.name, f.cmd); err != nil {
				return nil, fail("preflight", "usage", err)
			}
		}
	}
	if len(st.gate) == 0 {
		o.Run.Warn(toolio.WarnNoVerifyCommand, "high", "no verification command runs: every task will be reported as unverified")
	} else {
		o.Progress.Detail("gate: %s", strings.Join(st.gate, " · "))
	}
	result.Gate = st.gate

	if f := checkUpstream(o, st, result); f != nil {
		return nil, f
	}

	if f := selectTasks(o, st, result); f != nil {
		return nil, f
	}
	if len(st.todo) == 0 {
		return st, nil
	}

	// ---------------------------------------------------------- baseline --
	st.baseline = st.runGate(ctx, o, "baseline")
	result.Baseline = st.baseline
	if r, bad := st.baseline.couldNotRun(); bad {
		return nil, failf("preflight", "usage",
			"`%s` could not run before any change (%s), so no task could ever be verified by it; "+
				"fix the command in tasks.json, pass --verify, or raise --verify-timeout", r.Command, runFailure(r))
	}
	return st, nil
}

// checkUpstream applies §8.2: every task of this spec runs after each
// upstream spec is sealed or its tasks are done.
func checkUpstream(o Options, st *runState, result *Result) *Failure {
	deps := st.spec.Tasks.Dependencies
	if len(deps) == 0 {
		return nil
	}
	st.upstreamVerified, st.upstreamUnchecked = 0, 0
	metas, err := afspec.DiscoverSpecs(st.specsDir)
	if err != nil {
		o.Run.Warn(toolio.WarnUpstreamMissing, "low", "the spec's dependencies could not be checked: %v", err)
		st.upstreamUnchecked = len(deps)
		return nil
	}
	byID := map[string]afspec.SpecMeta{}
	for _, m := range metas {
		byID[m.SpecID] = m
	}
	for _, d := range deps {
		m, ok := byID[d.Spec]
		if !ok {
			o.Run.Warn(toolio.WarnUpstreamMissing, "low", "dependency on spec %s could not be checked: no such package under %s", d.Spec, st.specsDir)
			st.upstreamUnchecked++
			continue
		}
		if m.Status == "sealed" {
			st.upstreamVerified++
			continue
		}
		up, err := afspec.LoadSpec(m.Dir)
		if err != nil {
			o.Run.Warn(toolio.WarnUpstreamMissing, "low", "dependency on spec %s could not be checked: %v", d.Spec, err)
			st.upstreamUnchecked++
			continue
		}
		if up.Tasks == nil {
			st.upstreamVerified++
			continue
		}
		for _, t := range up.Tasks.Tasks {
			if t.State != afspec.TaskStateDone && t.State != afspec.TaskStateDropped {
				reason := fmt.Sprintf("spec %s depends on spec %s (%s), whose task %d is %s; implement %s first",
					st.spec.SpecID, d.Spec, d.Reason, t.Id, t.State, filepath.Base(m.Dir))
				if result != nil {
					result.Blocker = &Blocker{
						Reason: reason,
						Needed: filepath.Base(m.Dir),
					}
				}
				return failf("preflight", CategoryBlocked, "%s", reason)
			}
		}
		st.upstreamVerified++
	}
	return nil
}

// selectTasks decides which tasks this run implements, in order, and
// releases any a parked run left in progress.
func selectTasks(o Options, st *runState, result *Result) *Failure {
	tasks := st.spec.Tasks.Tasks
	result.TasksTotal = len(tasks)
	for _, t := range tasks {
		if err := reopen(st.spec, t.Id); err != nil {
			return fail("preflight", agentrun.CategoryInternal, err)
		}
	}
	tasks = st.spec.Tasks.Tasks
	for _, t := range tasks {
		r := TaskReport{ID: t.Id, Kind: string(t.Kind), Title: t.Title, Outcome: OutcomePending}
		if t.State == afspec.TaskStateDone || t.State == afspec.TaskStateDropped {
			result.TasksSkipped++
			r.Outcome = OutcomeSkipped
		}
		result.Tasks = append(result.Tasks, r)
	}

	if o.Task > 0 {
		t, ok := st.spec.Tasks.GetTask(o.Task)
		if !ok {
			return failf("preflight", "usage", "--task %d: the spec has no such task", o.Task)
		}
		if t.State != afspec.TaskStatePending {
			return failf("preflight", "usage", "--task %d is %s; there is nothing to implement", o.Task, t.State)
		}
		if dep, state, blocked := notReadyByID(st.spec, o.Task); blocked {
			return failf("preflight", "usage", "--task %d depends on task %d, which is %s", o.Task, dep, state)
		}
		st.todo = []int{o.Task}
	} else {
		for _, t := range tasks {
			if t.State == afspec.TaskStatePending {
				st.todo = append(st.todo, t.Id)
			}
		}
	}
	result.TasksRemaining = result.TasksTotal - result.TasksSkipped
	if len(st.todo) > 0 {
		o.Progress.Detail("tasks to implement: %s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(st.todo)), ", "), "[]"))
	}
	return nil
}

func notReadyByID(spec *afspec.Spec, id int) (int, afspec.TaskState, bool) {
	for i, t := range spec.Tasks.Tasks {
		if t.Id == id {
			return notReady(spec, i)
		}
	}
	return 0, "", false
}

func pendingTasks(spec *afspec.Spec, todo []int) []afspec.Task {
	out := make([]afspec.Task, 0, len(todo))
	for _, id := range todo {
		if t, ok := spec.Tasks.GetTask(id); ok {
			out = append(out, *t)
		}
	}
	return out
}

// repairFailure is why a repair loop did not end green. Unless the
// category is git's, the tree holds the last attempt, loose, for the
// caller to park.
type repairFailure struct {
	reason   string
	outcome  string
	category string
}

// repairLoop runs the repair phase until the gate is green or the attempts
// are spent. Every attempt starts from the commit at HEAD: one that does not
// end green is discarded, with its failure in the next attempt's prompt.
// The loop commits nothing and parks nothing — what a green gate or a spent
// loop means is the caller's, and differs between the baseline and the
// integration task.
func repairLoop(ctx context.Context, o Options, st *runState, result *Result, report *RepairReport,
	base repairInput) (GateResult, *repairFailure) {

	gitErr := func(err error) (GateResult, *repairFailure) {
		return GateResult{}, &repairFailure{reason: err.Error(), outcome: OutcomeFailed, category: CategoryGit}
	}
	head, err := st.git.Head(ctx)
	if err != nil {
		return gitErr(err)
	}
	var previous *attemptFailure

	for attempt := 1; attempt <= o.RepairAttempts; attempt++ {
		report.Attempts = attempt
		if stop := overBudget(o, st); stop != nil {
			return GateResult{}, &repairFailure{reason: stop.Error(), outcome: OutcomeAborted, category: agentrun.CategoryBudget}
		}
		in := base
		in.Attempt, in.Attempts, in.Previous = attempt, o.RepairAttempts, previous
		in.RepoMap = st.maps.Get(ctx, PhaseRepair, specPaths(st, base.Task))

		done := o.Progress.Begin("repairing the checks (attempt %d of %d)", attempt, o.RepairAttempts)
		sub, stats, err := st.brain.Repair(ctx, in)
		recordPhase(o.Run, st, stats)
		done(toolio.PhaseSummary(stats))
		result.CostUSD = st.cost
		revertSpecDir(ctx, o, st, head)
		dropScratchFiles(ctx, o, st)
		flagGateEdits(ctx, o, st, head)

		if err != nil {
			cat := agentrun.CategoryOf(err)
			switch cat {
			case agentrun.CategoryNoResult, agentrun.CategoryMaxTurns:
				failure := &attemptFailure{Reason: "the phase ended without submitting a report: " + err.Error()}
				if stat, e := st.git.DiffStat(ctx, head); e == nil {
					failure.DiffStat = stat
				}
				if attempt < o.RepairAttempts {
					previous = failure
					if err := discard(ctx, st, head); err != nil {
						return gitErr(err)
					}
					continue
				}
				return GateResult{}, &repairFailure{reason: failure.Reason, outcome: OutcomeFailed, category: cat}
			case agentrun.CategoryAborted:
				return GateResult{}, &repairFailure{reason: err.Error(), outcome: OutcomeAborted, category: cat}
			default:
				return GateResult{}, &repairFailure{reason: err.Error(), outcome: OutcomeFailed, category: cat}
			}
		}
		report.Submission = &sub

		if sub.Blocker != nil {
			result.Blocker = sub.Blocker
			o.Progress.Step("task", "stopped: the checks cannot be repaired in code")
			return GateResult{}, &repairFailure{
				reason:   "the checks cannot be repaired in code: " + strings.TrimSpace(sub.Blocker.Reason),
				outcome:  OutcomeBlocked,
				category: CategoryBlocked,
			}
		}

		changed, err := st.git.ChangedFiles(ctx, head)
		if err != nil {
			return gitErr(err)
		}
		if len(changed) == 0 {
			failure := &attemptFailure{Reason: "the phase reported a repair and no file differs from the commit it started from"}
			if attempt < o.RepairAttempts {
				previous = failure
				continue
			}
			return GateResult{}, &repairFailure{reason: failure.Reason, outcome: OutcomeFailed, category: CategoryEmpty}
		}
		report.ChangedFiles = changed
		if stat, err := st.git.DiffStat(ctx, head); err == nil {
			report.DiffStat = stat
		}

		// The gate, held to green rather than compared: the comparison is
		// what the repair exists to make unnecessary.
		after := st.runGate(ctx, o, "repair verification")
		report.Verification = &after
		result.Verification = after
		result.Verdict = compareGate(st.baseline, after)

		if r, bad := after.couldNotRun(); bad {
			return GateResult{}, &repairFailure{
				reason: fmt.Sprintf("`%s` could not run after the repair (%s), so the work was never measured",
					r.Command, runFailure(r)),
				outcome:  OutcomeUnverified,
				category: CategoryUnverified,
			}
		}
		if !after.OK() {
			failure := &attemptFailure{Reason: fmt.Sprintf("the checks still do not pass (%s)", result.Verdict), Gate: &after}
			if stat, e := st.git.DiffStat(ctx, head); e == nil {
				failure.DiffStat = stat
			}
			if attempt < o.RepairAttempts {
				previous = failure
				o.Progress.Step("task", "repair attempt %d did not make the checks pass (%s); discarding it", attempt, result.Verdict)
				if err := discard(ctx, st, head); err != nil {
					return gitErr(err)
				}
				continue
			}
			return GateResult{}, &repairFailure{reason: failure.Reason, outcome: OutcomeUnverified, category: CategoryUnverified}
		}
		return after, nil
	}
	return GateResult{}, &repairFailure{reason: "the repair ended without an outcome", outcome: OutcomeFailed, category: agentrun.CategoryInternal}
}

// newRepairReport starts a report with what is known before the loop.
func newRepairReport(st *runState, failing GateResult) *RepairReport {
	report := &RepairReport{Outcome: OutcomePending, Failing: &failing}
	if m, ok := st.brain.(interface{ RepairModel() string }); ok {
		report.Model = m.RepairModel()
	}
	return report
}

// runRepair drives the baseline repair to a green gate, committed as its
// own fix:, or to a parked attempt. It is runTask's shape with a different
// bar: not "no worse than before" but green, because "before" is what it
// exists to replace.
func runRepair(ctx context.Context, o Options, st *runState, result *Result) error {
	report := newRepairReport(st, st.baseline)
	result.Repair = report

	after, rf := repairLoop(ctx, o, st, result, report, repairInput{
		Spec: st.spec, Root: st.root, Branch: st.branch, Gate: st.gate,
		Failing: st.baseline, Survey: st.survey,
		Instructions: projectInstructions(st.root), Steering: steering(st.specsDir),
		Profile: st.profile, Now: o.now(),
	})
	if rf != nil {
		report.Outcome, report.Error = rf.outcome, rf.reason
		switch rf.category {
		case CategoryGit:
			return failf(PhaseRepair, CategoryGit, "%s", rf.reason)
		case agentrun.CategoryBudget:
			// Between attempts the tree is clean: nothing to park.
			_, err := stopped(ctx, o, st, result, failf("budget", agentrun.CategoryBudget, "%s", rf.reason))
			return err
		}
		return parkRepair(ctx, o, st, result, report, rf.reason, rf.outcome, rf.category)
	}

	commit, err := st.git.CommitAll(ctx, repairCommitMessage(st.spec, *report.Submission))
	// The commit runs the repository's hooks, which may rewrite files
	// (16-REQ-4.6).
	st.invalidate()
	if err != nil {
		return fail("commit", CategoryGit, err)
	}
	if dirty, err := st.git.DirtyFiles(ctx); err == nil && len(dirty) > 0 {
		return failf("commit", CategoryGit,
			"the tree is dirty after committing the repair — a hook changed files the commit does "+
				"not carry:\n%s", strings.Join(dirty, "\n"))
	}
	report.Commit = commit
	report.Outcome = OutcomeDone
	report.Verdict, result.Verdict = VerdictBaselineRepaired, VerdictBaselineRepaired
	// The green gate is what the first task is compared with. The result
	// keeps the red one as the run's baseline, which is the truth about
	// where the branch started.
	st.baseline = after
	o.Progress.Step("task", "the checks were repaired and committed as %s (%s)", commit, result.Verdict)
	return nil
}

// repairAfterTask drives the repair of the checks that failed after the
// integration task, on top of the task's work, and reports the green gate.
//
// The task's change is held in a provisional commit while the loop runs,
// so that each attempt can be discarded back to it and not to the commit
// before the task; the hold is undone — soft, keeping the change — before
// the task is committed for real, or parked. On a failure the task is
// parked here, the report is filled in, and the error is the run's.
func repairAfterTask(ctx context.Context, o Options, st *runState, result *Result, report *TaskReport,
	task afspec.Task, head string, failing GateResult) (GateResult, error) {

	rr := newRepairReport(st, failing)
	report.Repair = rr
	o.Progress.Step("task", "task %d: the checks failed after the integration task; repairing them before it lands", task.Id)

	if _, err := st.git.CommitAllNoVerify(ctx, holdCommitMessage(st.spec, task)); err != nil {
		return GateResult{}, fail("task", CategoryGit, err)
	}
	after, rf := repairLoop(ctx, o, st, result, rr, repairInput{
		Spec: st.spec, Root: st.root, Branch: st.branch, Gate: st.gate,
		Failing: failing, Survey: st.survey, Task: &task, Prior: st.prior,
		Instructions: projectInstructions(st.root), Steering: steering(st.specsDir),
		Profile: st.profile, Now: o.now(),
	})
	// Whatever happened, the hold is undone and the task's change is back
	// in the tree, staged, beside whatever the last attempt left.
	bg, cancel := background(ctx)
	err := st.git.ResetSoft(bg, head)
	cancel()
	// The hold commit is gone and the tree holds the task's change plus the
	// repair's: what the task's own commit will carry (16-REQ-4.7).
	st.invalidate()
	if err != nil {
		return GateResult{}, fail("task", CategoryGit, err)
	}
	if rf != nil {
		rr.Outcome, rr.Error = rf.outcome, rf.reason
		if rf.category == CategoryGit {
			return GateResult{}, failf("task", CategoryGit, "%s", rf.reason)
		}
		stage := "task"
		if rf.category == CategoryUnverified {
			stage = "verify"
		}
		var perr error
		*report, perr = park(ctx, o, st, result, *report, task,
			"the checks failed after the task and could not be repaired: "+rf.reason, rf.outcome, rf.category, stage)
		return GateResult{}, perr
	}
	rr.Outcome = OutcomeDone
	return after, nil
}

// runTask drives one task to a landed commit or to a parked attempt.
func runTask(ctx context.Context, o Options, st *runState, result *Result, task afspec.Task) (TaskReport, error) {
	report := TaskReport{ID: task.Id, Kind: string(task.Kind), Title: task.Title}
	var previous *attemptFailure

	for attempt := 1; attempt <= o.TaskAttempts; attempt++ {
		report.Attempts = attempt
		head, err := st.git.Head(ctx)
		if err != nil {
			return report, fail("task", CategoryGit, err)
		}

		done := o.Progress.Begin("task %d/%d: %s (attempt %d of %d)", task.Id, result.TasksTotal,
			task.Title, attempt, o.TaskAttempts)
		sub, stats, err := st.brain.Implement(ctx, taskInput{
			Spec: st.spec, Task: task, Root: st.root, Branch: st.branch, Gate: st.gate,
			Baseline: st.baseline, Survey: st.survey, Prior: st.prior,
			Attempt: attempt, Attempts: o.TaskAttempts, Previous: previous,
			Instructions: projectInstructions(st.root), Steering: steering(st.specsDir),
			Profile: st.profile,
			Context: o.Input.Context,
			Now:     o.now(),
			Scope:   specScope(st).Allow,
			// A discarded attempt, a reset and every earlier commit change
			// the tree, so the map is checked before each attempt and a
			// declaration task N-1 added is in task N's map (14-REQ-9.2).
			RepoMap: st.maps.Get(ctx, PhaseImplement, specPaths(st, &task)),
		})
		recordPhase(o.Run, st, stats)
		done(toolio.PhaseSummary(stats))

		// Whatever the phase did under the spec package is taken back
		// before anything is measured: the state file is the program's.
		revertSpecDir(ctx, o, st, head)
		dropScratchFiles(ctx, o, st)
		flagGateEdits(ctx, o, st, head)

		if err != nil {
			cat := agentrun.CategoryOf(err)
			switch cat {
			case agentrun.CategoryNoResult, agentrun.CategoryMaxTurns:
				// The phase ended without a result. That is an attempt the
				// model can learn from, not a broken run.
				failure := &attemptFailure{Reason: "the phase ended without submitting a report: " + err.Error()}
				if stat, e := st.git.DiffStat(ctx, head); e == nil {
					failure.DiffStat = stat
				}
				if attempt < o.TaskAttempts {
					previous = failure
					if err := discard(ctx, st, head); err != nil {
						return report, fail("task", CategoryGit, err)
					}
					continue
				}
				return park(ctx, o, st, result, report, task, failure.Reason, OutcomeFailed, cat, "task")
			case agentrun.CategoryAborted:
				return park(ctx, o, st, result, report, task, err.Error(), OutcomeAborted, cat, "task")
			default:
				return park(ctx, o, st, result, report, task, err.Error(), OutcomeFailed, cat, "task")
			}
		}
		report.Submission = &sub

		if sub.Blocker != nil {
			result.Blocker = sub.Blocker
			o.Progress.Step("task", "stopped: task %d cannot be implemented as specified", task.Id)
			return park(ctx, o, st, result, report, task,
				"the task cannot be implemented as specified: "+strings.TrimSpace(sub.Blocker.Reason),
				OutcomeBlocked, CategoryBlocked, "task")
		}

		// The change is git's account, not the model's.
		changed, err := st.git.ChangedFiles(ctx, head)
		if err != nil {
			return report, fail("task", CategoryGit, err)
		}
		if len(changed) == 0 {
			failure := &attemptFailure{Reason: "the phase reported work and no file differs from the last commit"}
			if attempt < o.TaskAttempts {
				previous = failure
				continue
			}
			return park(ctx, o, st, result, report, task, failure.Reason, OutcomeFailed, CategoryEmpty, "task")
		}
		report.ChangedFiles = changed
		if stat, err := st.git.DiffStat(ctx, head); err == nil {
			report.DiffStat = stat
		}

		// The gate, compared with the one before this task.
		after := st.runGate(ctx, o, "verification")
		verdict := compareGate(st.baseline, after)
		report.Verification = &after
		report.Verdict = verdict
		result.Verification = after
		result.Verdict = verdict
		report.TestsOutcome = verdictsOutcome(task.Tests, sub.TestVerdicts)
		doneWhen := verdictsOutcome(doneWhenIDs(task), sub.DoneWhenVerdicts)

		var failure *attemptFailure
		switch {
		case verdict == VerdictGateFailed:
			r, _ := after.couldNotRun()
			return park(ctx, o, st, result, report, task,
				fmt.Sprintf("`%s` could not run after the change (%s), so the work was never measured", r.Command, runFailure(r)),
				OutcomeUnverified, CategoryUnverified, "verify")
		case report.TestsOutcome == VerdictFail || doneWhen == VerdictFail:
			// The model's own account comes first: a task its author says is
			// not done is retried from scratch, not repaired.
			failure = &attemptFailure{
				Reason:   "the report itself says the task is not done: a test it owns, or a done_when entry, was answered fail",
				Verdicts: append(failedVerdicts(sub.TestVerdicts), failedVerdicts(sub.DoneWhenVerdicts)...),
			}
		case !landable(verdict, len(st.gate) == 0) && o.Repair && task.Kind == afspec.TaskKindIntegration:
			// The whole suite, after the last task, is the run's final
			// verification. What it finds is repaired on top of the work,
			// not thrown away with it: a wiring gap between earlier tasks
			// is not this task's to redo.
			green, err := repairAfterTask(ctx, o, st, result, &report, task, head, after)
			if err != nil {
				return report, err
			}
			after, verdict = green, compareGate(st.baseline, green)
			report.Verification, report.Verdict = &after, verdict
			result.Verification, result.Verdict = after, verdict
		case !landable(verdict, len(st.gate) == 0):
			failure = &attemptFailure{Reason: fmt.Sprintf("the checks did not pass (%s)", verdict), Gate: &after}
		}
		if failure == nil && len(st.gate) > 0 && len(task.Tests) > 0 &&
			(o.NoTestFirst || strings.TrimSpace(sub.TestFirstDeviation) != "") {
			rc, err := revertCheck(ctx, o, st, head)
			if err != nil {
				return report, fail("task", CategoryGit, err)
			}
			report.RevertCheck = &rc
			if rc.Ran && !rc.Proves {
				failure = &attemptFailure{Reason: "test-first was waived, and with the implementation taken " +
					"out the checks still pass (" + strings.Join(rc.Reverted, ", ") + " put back as they " +
					"were): the tests the task owns do not depend on the work. Make them fail when the " +
					"wired component is removed or the behaviour inverted"}
			}
		}
		if failure != nil {
			if stat, e := st.git.DiffStat(ctx, head); e == nil {
				failure.DiffStat = stat
			}
			if attempt < o.TaskAttempts {
				previous = failure
				o.Progress.Step("task", "task %d attempt %d did not land: %s; discarding it", task.Id, attempt, failure.Reason)
				if err := discard(ctx, st, head); err != nil {
					return report, fail("task", CategoryGit, err)
				}
				continue
			}
			return park(ctx, o, st, result, report, task, failure.Reason, OutcomeUnverified, CategoryUnverified, "verify")
		}

		// ---------------------------------------------------------- land --
		if err := transition(st.spec, task.Id, afspec.TaskStateDone); err != nil {
			return report, fail("task", agentrun.CategoryInternal, err)
		}
		if err := saveTasks(st.spec, st.specDir); err != nil {
			return report, fail("task", agentrun.CategoryInternal, err)
		}
		var repaired *RepairSubmission
		if report.Repair != nil && report.Repair.Outcome == OutcomeDone {
			repaired = report.Repair.Submission
		}
		var landedGate GateResult
		if report.Verification != nil {
			landedGate = *report.Verification
		}
		commit, err := st.git.CommitAll(ctx, commitMessage(st.spec, task, sub, repaired, landedGate))
		// Commit hooks may rewrite files, so the tree after the commit is not
		// the tree the index last saw (16-REQ-4.5).
		st.invalidate()
		if err != nil {
			return report, fail("commit", CategoryGit, err)
		}
		if dirty, err := st.git.DirtyFiles(ctx); err == nil && len(dirty) > 0 {
			return report, failf("commit", CategoryGit,
				"the tree is dirty after committing task %d — a hook changed files the commit does "+
					"not carry:\n%s", task.Id, strings.Join(dirty, "\n"))
		}
		report.Commit = commit
		report.Outcome = OutcomeDone
		result.TasksDone++
		result.TasksRemaining--
		st.baseline = after
		st.prior = append(st.prior, priorTask{
			ID: task.Id, Title: task.Title, Summary: sub.Summary, Gotchas: sub.Gotchas, Files: changed,
		})
		o.Progress.Step("task", "task %d landed as %s (%s)", task.Id, commit, verdict)
		return report, nil
	}
	return report, failf("task", agentrun.CategoryInternal, "task %d ended without an outcome", task.Id)
}

// discard throws an attempt away: the tracked files back to the last
// commit, the untracked ones removed. The next attempt starts where the
// last landed task left the tree. The index is invalidated whatever the
// outcome: a reset that failed halfway still changed the tree (16-REQ-4.4).
func discard(ctx context.Context, st *runState, head string) error {
	bg, cancel := background(ctx)
	defer cancel()
	defer st.invalidate()
	if err := st.git.ResetHard(bg, head); err != nil {
		return err
	}
	return st.git.Clean(bg)
}

// revertSpecDir takes back any change the phase made under the spec
// package. The write tools refuse it, but a shell can reach it, and the
// state file is the program's to write.
func revertSpecDir(ctx context.Context, o Options, st *runState, head string) {
	bg, cancel := background(ctx)
	defer cancel()
	changed, err := st.git.ChangedFiles(bg, head)
	if err != nil {
		return
	}
	var under []string
	for _, f := range changed {
		if f == st.relSpecDir || strings.HasPrefix(f, st.relSpecDir+"/") {
			under = append(under, f)
		}
	}
	if len(under) == 0 {
		return
	}
	if err := st.git.Restore(bg, st.relSpecDir); err != nil {
		o.Run.Warn(toolio.WarnSpecEditReverted, "high", "the phase changed %s and the change could not be reverted: %v",
			strings.Join(under, ", "), err)
		return
	}
	o.Run.Warn(toolio.WarnSpecEditReverted, "high", "the phase changed the spec package (%s); the change was reverted — the package "+
		"is the tool's to write", strings.Join(under, ", "))
}

// dropScratchFiles keeps the model's leftovers out of the commit. Untracked
// backup files (*.bak, *.orig) and anything named *scratch* are deleted and
// reported; an untracked .txt at the repository root is only reported, since
// a task can legitimately add one (requirements.txt).
func dropScratchFiles(ctx context.Context, o Options, st *runState) {
	// Every phase's tree is first put right by revertSpecDir and then by this
	// function, so the index is invalidated here, once the tree is as the
	// gate will see it (16-REQ-4.3).
	defer st.invalidate()
	bg, cancel := background(ctx)
	defer cancel()
	files, err := st.git.UntrackedFiles(bg)
	if err != nil {
		return
	}
	for _, f := range files {
		base := path.Base(f)
		switch {
		case strings.HasSuffix(base, ".bak"), strings.HasSuffix(base, ".orig"),
			strings.Contains(strings.ToLower(base), "scratch"):
			if err := os.Remove(filepath.Join(st.root, filepath.FromSlash(f))); err != nil {
				o.Run.Warn(toolio.WarnScratchFileSuspected, "low", "%s looks like a scratch file and could not be removed: %v", f, err)
				continue
			}
			o.Run.Warn(toolio.WarnScratchFileRemoved, "high", "the phase left the scratch file %s; it was removed before the commit", f)
		case !strings.Contains(f, "/") && strings.HasSuffix(base, ".txt"):
			o.Run.Warn(toolio.WarnScratchFileSuspected, "low", "%s is a new .txt file at the repository root; it is committed — check that it is not a scratch file", f)
		}
	}
}

// park commits the attempt as a wip: commit with the task recorded as in
// progress, returns the checkout to the base branch, and reports the
// failure.
//
// It runs under a context that survives the run's cancellation, because
// the one case it exists for most is Ctrl-C: a park that the cancelled
// context killed mid-commit would leave the tree dirty on the work branch,
// which is exactly what parking is meant to prevent.
func park(ctx context.Context, o Options, st *runState, result *Result, report TaskReport,
	task afspec.Task, reason, outcome, category, stage string) (TaskReport, error) {

	report.Outcome = outcome
	report.Error = reason

	if err := saveTasks(st.spec, st.specDir); err != nil {
		o.Run.Warn(toolio.WarnStateNotSaved, "high", "the task state could not be written before parking: %v", err)
	}
	commit, err := parkWork(ctx, o, st, result, wipCommitMessage(st.spec, task, reason))
	if err != nil {
		return report, failf(stage, category, "task %d did not land: %s. The work is loose on %s "+
			"and could not be committed", task.Id, reason, st.branch)
	}
	report.Commit = commit
	o.Progress.Step("park", "task %d parked on %s as %s; the checkout is back on %s", task.Id, st.branch, commit, st.base)
	return report, failf(stage, category, "task %d did not land: %s. The work is parked on %s (%s) "+
		"and the checkout is back on %s", task.Id, reason, st.branch, commit, st.base)
}

// parkRepair is park for the repair: the same wip: commit and the same
// return to the base branch, with no task state to record.
func parkRepair(ctx context.Context, o Options, st *runState, result *Result, report *RepairReport,
	reason, outcome, category string) error {

	report.Outcome = outcome
	report.Error = reason
	commit, err := parkWork(ctx, o, st, result, wipRepairMessage(st.spec, reason))
	if err != nil {
		return failf(PhaseRepair, category, "the checks could not be repaired: %s. The attempt is loose on %s "+
			"and could not be committed", reason, st.branch)
	}
	report.Commit = commit
	o.Progress.Step("park", "the repair is parked on %s as %s; the checkout is back on %s", st.branch, commit, st.base)
	return failf(PhaseRepair, category, "the checks could not be repaired: %s. The last attempt is parked on "+
		"%s (%s) and the checkout is back on %s; no task was implemented", reason, st.branch, commit, st.base)
}

// parkWork commits whatever the phase left as a wip: commit and returns
// the checkout to the base branch. It is the half of parking that does not
// know what was being attempted. On a commit that fails the tree is left as
// the phase left it, on the work branch, and the warning says so.
func parkWork(ctx context.Context, o Options, st *runState, result *Result, message string) (string, error) {
	bg, cancel := background(ctx)
	defer cancel()
	result.Stage = "parked"

	commit, err := st.git.CommitAllNoVerify(bg, message)
	if err != nil {
		o.Run.Warn(toolio.WarnCommitNotParked, "high", "the attempt could not be parked as a commit on %s; the tree is left as the "+
			"phase left it: %v", st.branch, err)
		return "", err
	}
	if err := st.git.Checkout(bg, st.base); err != nil {
		o.Run.Warn(toolio.WarnCheckoutNotRestored, "low", "could not return to %s: %v", st.base, err)
	}
	return commit, nil
}

// stopped ends a run before or between tasks, with a clean tree: nothing to
// park, only a checkout to return.
func stopped(ctx context.Context, o Options, st *runState, result *Result, f *Failure) (*Result, error) {
	result.Stage = "stopped"
	bg, cancel := background(ctx)
	defer cancel()
	if cur, err := st.git.CurrentBranch(bg); err == nil && cur == st.branch && st.branch != st.base {
		if err := st.git.Checkout(bg, st.base); err != nil {
			o.Run.Warn(toolio.WarnCheckoutNotRestored, "low", "could not return to %s: %v", st.base, err)
		}
	}
	return result, f
}

// background is the context the git steps that must complete run under: a
// cancelled run's own context would refuse to start them.
func background(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
}

// overBudget reports the run-level cap, checked before each phase.
func overBudget(o Options, st *runState) *Failure {
	if o.TotalBudgetUSD > 0 && st.cost >= o.TotalBudgetUSD {
		f := failf("budget", agentrun.CategoryBudget,
			"the run has spent $%.2f of its $%.2f total budget; the tasks landed so far are on the "+
				"branch, re-run to continue", st.cost, o.TotalBudgetUSD)
		f.TotalBudget = o.TotalBudgetUSD
		return f
	}
	return nil
}

func setReport(result *Result, r TaskReport) {
	for i := range result.Tasks {
		if result.Tasks[i].ID == r.ID {
			result.Tasks[i] = r
			return
		}
	}
	result.Tasks = append(result.Tasks, r)
}

// IsBlocked reports whether err is the run stopping to ask a person.
func IsBlocked(err error) bool {
	var f *Failure
	return errors.As(err, &f) && f.Category == CategoryBlocked
}

func recordPhase(run *toolio.Run, st *runState, res agentrun.Result) {
	st.cost += res.Usage.CostUSD
	if run == nil || res.Name == "" {
		return
	}
	run.AddPhase(toolio.PhaseFromResult(res, ""))
}

// revertCheck stands in for the red run a task that waived test-first did
// not make: the tests run with the implementation taken out, and must fail.
// The implementation is the task's own non-test change when it made one; a
// task that only added tests — an integration task over wiring the earlier
// tasks built — is measured against the whole branch's implementation.
func revertCheck(ctx context.Context, o Options, st *runState, head string) (conform.RevertResult, error) {
	// The revert takes the implementation out and puts it back: the tree the
	// index saw is not the tree the next phase starts from.
	defer st.invalidate()
	check := func(ctx context.Context) checks.Result {
		cmd := st.gate[len(st.gate)-1]
		done := o.Progress.Begin("revert check: %s with the implementation taken out", cmd)
		r := checks.Run(ctx, o.CheckRunner, st.root, cmd, o.VerifyTimeout)
		done(fmt.Sprintf("exit %d", r.ExitCode))
		return r
	}
	without := func(files []string) []string {
		var out []string
		for _, f := range files {
			if !underSpec(st, f) {
				out = append(out, f)
			}
		}
		return out
	}
	changed, err := st.git.ChangedFiles(ctx, head)
	if err != nil {
		return conform.RevertResult{}, err
	}
	rc, err := conform.Revert(ctx, st.git, st.root, head, without(changed), check)
	if err != nil || rc.Ran || head == st.start {
		return rc, err
	}
	all, err := st.git.ChangedFiles(ctx, st.start)
	if err != nil {
		return conform.RevertResult{}, err
	}
	return conform.Revert(ctx, st.git, st.root, st.start, without(all), check)
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
