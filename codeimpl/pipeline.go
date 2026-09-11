package codeimpl

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/ghapi"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// runState is what the pipeline establishes in pre-flight and carries
// through the task loop.
type runState struct {
	root       string
	git        *gitx.Git
	base       string
	target     ghapi.Repo
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
	survey     *Survey
	prior      []priorTask
	cost       float64
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
//	land         push, open the pull request
func Run(ctx context.Context, o Options) (*Result, error) {
	if o.Workspace == nil {
		return nil, failf("preflight", agentrun.CategoryInternal, "no workspace configured")
	}
	if o.Runner == nil && o.brain == nil {
		return nil, failf("preflight", agentrun.CategoryInternal, "no runner configured")
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
	if o.TaskAttempts <= 0 {
		o.TaskAttempts = DefaultTaskAttempts
	}
	if o.RepairAttempts <= 0 {
		o.RepairAttempts = DefaultRepairAttempts
	}

	result := &Result{Stage: "preflight", DryRun: o.DryRun, Verdict: string(checks.VerdictUnverified)}
	st, pfErr := preflight(ctx, o, result)
	if pfErr != nil {
		return result, pfErr
	}
	if len(st.todo) == 0 {
		result.Stage = "complete"
		o.Progress.Step("every task of %s is done; nothing to implement", filepath.Base(st.specDir))
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
		b = &agentBrain{runner: o.Runner, repairRunner: o.RepairRunner, extraPrograms: programs, protected: st.specDir}
	}
	st.brain = b
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
		})
		recordPhase(o.Run, st, stats)
		done(phaseSummary(stats))
		result.CostUSD = st.cost
		if err != nil {
			return stopped(ctx, o, st, result, fail(PhaseSurvey, agentrun.CategoryOf(err), err))
		}
		st.survey = &survey
		result.Survey = &survey
		result.Stage = "surveyed"
		if survey.Blocker != nil {
			result.Blocker = survey.Blocker
			o.Progress.Step("stopped: the spec cannot be implemented as written")
			return stopped(ctx, o, st, result, failf(PhaseSurvey, CategoryBlocked,
				"the spec cannot be implemented as written: %s", strings.TrimSpace(survey.Blocker.Reason)))
		}
	}

	// ----------------------------------------------------------- branch --
	if !st.exists {
		if err := st.git.CreateBranch(ctx, st.branch); err != nil {
			return result, fail("branch", CategoryGit, err)
		}
		o.Progress.Step("branched %s from %s", st.branch, st.base)
	}

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

	// ------------------------------------------------------------- land --
	result.Stage = "committed"
	if o.Land.Pushes() && !o.DryRun {
		if err := st.git.Push(ctx, st.branch, o.PushAttempts, func(m string) { o.Progress.Step("%s", m) }); err != nil {
			return result, fail("push", CategoryGit, err)
		}
		result.Pushed = true
		result.Stage = "pushed"
		o.Progress.Step("pushed origin/%s", st.branch)
	}
	if o.Land == LandPR && result.Pushed && st.target.Valid() {
		pr, err := o.GitHub.CreatePullRequest(ctx, st.target, pullRequestTitle(st.spec),
			pullRequestBody(result), st.branch, st.base, o.Draft)
		if err != nil {
			o.Run.Warn("the pull request could not be opened (the branch is pushed; open it by "+
				"hand from %s into %s): %v", st.branch, st.base, err)
		} else {
			result.PullRequestURL = pr.HTMLURL
			result.PullRequestNumber = pr.Number
			o.Progress.Step("opened %s", pr.HTMLURL)
		}
	}
	switch {
	case o.Land == LandPR && result.PullRequestURL != "":
		result.Stage = "landed"
	case o.Land == LandBranch && result.Pushed:
		result.Stage = "landed"
	case o.Land == LandNone:
		result.Stage = "landed"
	}
	o.Progress.Step("%d task(s) landed on %s", result.TasksDone, st.branch)
	return result, nil
}

// preflight is every check that can refuse the run, in the order that
// costs least when it refuses.
func preflight(ctx context.Context, o Options, result *Result) (*runState, *Failure) {
	st := &runState{root: o.Workspace.Root}
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

	// A run that will write to GitHub checks that it can before it spends
	// money on a model.
	st.target = o.Repo
	if !st.target.Valid() {
		if r, ok := ghapi.DetectRepo(st.root); ok {
			st.target = r
		}
	}
	if o.Land == LandPR && !o.DryRun && !o.GitHub.Authenticated() {
		return nil, failf("preflight", "auth",
			"opening a pull request needs a credential: set GITHUB_TOKEN or GH_TOKEN, "+
				"or pass --land=branch, --land=none or --dry-run")
	}
	if o.Land == LandPR && !o.DryRun && !st.target.Valid() {
		return nil, failf("preflight", "usage",
			"--land=pr needs a target repository: %s has no GitHub origin remote — pass "+
				"--repo owner/repo, or --land=branch", st.root)
	}
	if o.Land.Pushes() && !o.DryRun && !git.HasRemote(ctx) {
		return nil, failf("preflight", "usage",
			"--land=%s pushes, and %s has no origin remote; pass --land=none", o.Land, st.root)
	}
	if st.target.Valid() {
		result.Repo = st.target.String()
	}

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
		o.Progress.Step("continuing on %s", st.branch)
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
				o.Run.Warn("discarded the parked attempt at %s (%s); the work starts again "+
					"from the last landed commit", what, head)
			}
		}
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
		o.Run.Warn("%s: %s", w.Check, w.Message)
	}
	if !v.Valid {
		return nil, failf("preflight", CategoryInvalidSpec,
			"%s does not validate, so it is not a complete plan; %d error(s):\n%s", rel,
			len(v.Errors), strings.TrimRight(afspec.FormatValidationEntries(v.Errors), "\n"))
	}
	switch spec.Status {
	case "active":
	case "draft":
		o.Run.Warn("%s is a draft: it validates, so it is implemented, but its intent is not yet "+
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
		o.Run.Warn("no verification command runs: every task will be reported as unverified")
	} else {
		o.Progress.Detail("gate: %s", strings.Join(st.gate, " · "))
	}
	result.Gate = st.gate

	if f := checkUpstream(o, st); f != nil {
		return nil, f
	}

	if f := selectTasks(o, st, result); f != nil {
		return nil, f
	}
	if len(st.todo) == 0 {
		return st, nil
	}

	// ---------------------------------------------------------- baseline --
	st.baseline = runGate(ctx, o, st.root, st.gate, "baseline")
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
func checkUpstream(o Options, st *runState) *Failure {
	deps := st.spec.Tasks.Dependencies
	if len(deps) == 0 {
		return nil
	}
	metas, err := afspec.DiscoverSpecs(st.specsDir)
	if err != nil {
		o.Run.Warn("the spec's dependencies could not be checked: %v", err)
		return nil
	}
	byID := map[string]afspec.SpecMeta{}
	for _, m := range metas {
		byID[m.SpecID] = m
	}
	for _, d := range deps {
		m, ok := byID[d.Spec]
		if !ok {
			o.Run.Warn("dependency on spec %s could not be checked: no such package under %s", d.Spec, st.specsDir)
			continue
		}
		if m.Status == "sealed" {
			continue
		}
		up, err := afspec.LoadSpec(m.Dir)
		if err != nil {
			o.Run.Warn("dependency on spec %s could not be checked: %v", d.Spec, err)
			continue
		}
		if up.Tasks == nil {
			continue
		}
		for _, t := range up.Tasks.Tasks {
			if t.State != afspec.TaskStateDone && t.State != afspec.TaskStateDropped {
				return failf("preflight", CategoryBlocked,
					"spec %s depends on spec %s (%s), whose task %d is %s; implement %s first",
					st.spec.SpecID, d.Spec, d.Reason, t.Id, t.State, filepath.Base(m.Dir))
			}
		}
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

		done := o.Progress.Begin("repairing the checks (attempt %d of %d)", attempt, o.RepairAttempts)
		sub, stats, err := st.brain.Repair(ctx, in)
		recordPhase(o.Run, st, stats)
		done(phaseSummary(stats))
		result.CostUSD = st.cost
		revertSpecDir(ctx, o, st, head)

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
			o.Progress.Step("stopped: the checks cannot be repaired in code")
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
		after := runGate(ctx, o, st.root, st.gate, "repair verification")
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
				o.Progress.Step("repair attempt %d did not make the checks pass (%s); discarding it", attempt, result.Verdict)
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
		Profile: st.profile,
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
	// The green gate is what the first task is compared with. The result
	// keeps the red one as the run's baseline, which is the truth about
	// where the branch started.
	st.baseline = after
	o.Progress.Step("the checks were repaired and committed as %s (%s)", commit, result.Verdict)
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
	o.Progress.Step("task %d: the checks failed after the integration task; repairing them before it lands", task.Id)

	if _, err := st.git.CommitAllNoVerify(ctx, holdCommitMessage(st.spec, task)); err != nil {
		return GateResult{}, fail("task", CategoryGit, err)
	}
	after, rf := repairLoop(ctx, o, st, result, rr, repairInput{
		Spec: st.spec, Root: st.root, Branch: st.branch, Gate: st.gate,
		Failing: failing, Survey: st.survey, Task: &task, Prior: st.prior,
		Instructions: projectInstructions(st.root), Steering: steering(st.specsDir),
		Profile: st.profile,
	})
	// Whatever happened, the hold is undone and the task's change is back
	// in the tree, staged, beside whatever the last attempt left.
	bg, cancel := background(ctx)
	err := st.git.ResetSoft(bg, head)
	cancel()
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
		})
		recordPhase(o.Run, st, stats)
		done(phaseSummary(stats))

		// Whatever the phase did under the spec package is taken back
		// before anything is measured: the state file is the program's.
		revertSpecDir(ctx, o, st, head)

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
			o.Progress.Step("stopped: task %d cannot be implemented as specified", task.Id)
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
		after := runGate(ctx, o, st.root, st.gate, "verification")
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
		if failure != nil {
			if stat, e := st.git.DiffStat(ctx, head); e == nil {
				failure.DiffStat = stat
			}
			if attempt < o.TaskAttempts {
				previous = failure
				o.Progress.Step("task %d attempt %d did not land: %s; discarding it", task.Id, attempt, failure.Reason)
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
		commit, err := st.git.CommitAll(ctx, commitMessage(st.spec, task, sub, repaired))
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
		o.Progress.Step("task %d landed as %s (%s)", task.Id, commit, verdict)
		return report, nil
	}
	return report, failf("task", agentrun.CategoryInternal, "task %d ended without an outcome", task.Id)
}

// discard throws an attempt away: the tracked files back to the last
// commit, the untracked ones removed. The next attempt starts where the
// last landed task left the tree.
func discard(ctx context.Context, st *runState, head string) error {
	bg, cancel := background(ctx)
	defer cancel()
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
		o.Run.Warn("the phase changed %s and the change could not be reverted: %v",
			strings.Join(under, ", "), err)
		return
	}
	o.Run.Warn("the phase changed the spec package (%s); the change was reverted — the package "+
		"is the tool's to write", strings.Join(under, ", "))
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
		o.Run.Warn("the task state could not be written before parking: %v", err)
	}
	commit, err := parkWork(ctx, o, st, result, wipCommitMessage(st.spec, task, reason))
	if err != nil {
		return report, failf(stage, category, "task %d did not land: %s. The work is loose on %s "+
			"and could not be committed", task.Id, reason, st.branch)
	}
	report.Commit = commit
	o.Progress.Step("task %d parked on %s as %s; the checkout is back on %s", task.Id, st.branch, commit, st.base)
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
	o.Progress.Step("the repair is parked on %s as %s; the checkout is back on %s", st.branch, commit, st.base)
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
		o.Run.Warn("the attempt could not be parked as a commit on %s; the tree is left as the "+
			"phase left it: %v", st.branch, err)
		return "", err
	}
	if err := st.git.Checkout(bg, st.base); err != nil {
		o.Run.Warn("could not return to %s: %v", st.base, err)
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
			o.Run.Warn("could not return to %s: %v", st.base, err)
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
		return failf("budget", agentrun.CategoryBudget,
			"the run has spent $%.2f of its $%.2f total budget; the tasks landed so far are on the "+
				"branch, re-run to continue", st.cost, o.TotalBudgetUSD)
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

func recordPhase(run *toolio.Run, st *runState, res agentrun.Result) {
	st.cost += res.Usage.CostUSD
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

// IsBlocked reports whether err is the run stopping to ask a person.
func IsBlocked(err error) bool {
	var f *Failure
	return errors.As(err, &f) && f.Category == CategoryBlocked
}
