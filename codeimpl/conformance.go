package codeimpl

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// The conformance stage runs once, after the last task has landed and before
// anything is pushed. Every task was verified by the checks; this stage asks
// what the checks cannot: whether the finished change does what the spec
// says, in a clean environment, inside the files the spec allows, and
// without the defects a passing suite lets through.
//
//	assess    the program's facts — structural checks over the files the
//	          change touched, the scope check, the checks again in a clean
//	          environment — and an independent review on a fresh context
//	resolve   when any of that found something: one writing phase that fixes
//	          it or declares it a known deviation; its change lands only on
//	          checks that still pass
//	assess    again, after a change, so that what the pull request reports
//	          is the state it opens with
//
// What is left is sorted into what the pull request must say (Unmet) and
// what keeps it from being presented as done (Blocking).

// assessment is one measurement of the finished change.
type assessment struct {
	changed  []string
	findings []conform.Finding
	outside  []string
	hermetic *GateResult
	env      *checks.Environment
	review   *conform.Review
	// reviewErr is why the review did not complete, when it did not.
	reviewErr error
}

func (a assessment) blockers() []conform.Blocker {
	var out []conform.Blocker
	if a.review != nil {
		out = append(out, a.review.Blockers()...)
	}
	for _, p := range a.outside {
		out = append(out, conform.Blocker{Key: "scope:" + p, Declarable: false,
			What: p + " is outside the files the spec's tasks list; a change the spec did not ask for " +
				"belongs in a pull request of its own"})
	}
	if a.hermetic != nil && a.hermetic.Ran() && !a.hermetic.OK() {
		var cmds []string
		for _, c := range a.hermetic.failing() {
			cmds = append(cmds, fmt.Sprintf("`%s` (exit %d)", c.Command, c.ExitCode))
		}
		out = append(out, conform.Blocker{Key: "hermetic", Declarable: true,
			What: "the checks pass on this machine and fail in a clean environment (an empty HOME, no " +
				"global git configuration): " + strings.Join(cmds, ", ")})
	}
	return out
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// specScope is the union of the paths the spec's tasks list, when every
// task lists some: a spec that leaves one task open does not restrict the
// change. Documentation is exempt — the project's own rules ask for it to
// change with the code — and so is the spec package, which the program
// writes.
func specScope(st *runState) conform.Scope {
	var allow []string
	for _, t := range st.spec.Tasks.Tasks {
		if len(t.Touches) == 0 {
			return conform.Scope{}
		}
		for _, p := range t.Touches {
			if !slices.Contains(allow, p) {
				allow = append(allow, p)
			}
		}
	}
	return conform.Scope{Allow: allow, Exempt: func(p string) bool {
		return project.IsDocsFile(p) || underSpec(st, p)
	}}
}

func underSpec(st *runState, p string) bool {
	return p == st.relSpecDir || strings.HasPrefix(p, st.relSpecDir+"/")
}

// reviewScope is what the review answers for: the criteria and tests of
// every task that is done, and the survey's decisions.
func reviewScope(st *runState) conform.ReviewScope {
	var sc conform.ReviewScope
	for _, t := range st.spec.Tasks.Tasks {
		if t.State != afspec.TaskStateDone {
			continue
		}
		for _, c := range t.Criteria {
			if !slices.Contains(sc.Requirements, c) {
				sc.Requirements = append(sc.Requirements, c)
			}
		}
		for _, id := range t.Tests {
			if !slices.Contains(sc.Tests, id) {
				sc.Tests = append(sc.Tests, id)
			}
		}
	}
	sc.Decisions = surveyDecisions(st.survey)
	return sc
}

// surveyDecisions numbers the survey's resolutions as D-1, D-2, …: the
// checklist the review verifies.
func surveyDecisions(s *Survey) []conform.Decision {
	if s == nil {
		return nil
	}
	var out []conform.Decision
	for i, d := range s.Drift {
		if strings.TrimSpace(d.Resolution) == "" {
			continue
		}
		out = append(out, conform.Decision{ID: fmt.Sprintf("D-%d", i+1),
			Text: fmt.Sprintf("%s: %s", d.SpecRef, strings.TrimSpace(d.Resolution))})
	}
	return out
}

// changeSince is the change the branch carries, without the spec package.
func changeSince(ctx context.Context, st *runState) ([]string, conform.Added, error) {
	changed, err := st.git.ChangedFiles(ctx, st.start)
	if err != nil {
		return nil, nil, err
	}
	_, patch, err := st.git.DiffSince(ctx, st.start)
	if err != nil {
		return nil, nil, err
	}
	var out []string
	for _, p := range changed {
		if !underSpec(st, p) {
			out = append(out, p)
		}
	}
	return out, conform.ParseAdded(patch), nil
}

// assess measures the finished change. Only a git failure is an error.
func assess(ctx context.Context, o Options, st *runState, result *Result) (assessment, error) {
	var a assessment
	changed, added, err := changeSince(ctx, st)
	if err != nil {
		return a, err
	}
	a.changed = changed
	tracked, err := st.git.TrackedFiles(ctx)
	if err != nil {
		return a, err
	}
	runner := o.CheckRunner
	if runner == nil {
		runner = gitx.ReducedEnvRunner
	}
	done := o.Progress.Begin("structural checks over %d changed file(s)", len(changed))
	a.findings = conform.Scan(ctx, conform.ScanInput{Root: st.root, Changed: changed, Added: added,
		Tracked: tracked, Today: o.now(), MaxFuncLines: o.MaxFuncLines, Runner: runner})
	done(fmt.Sprintf("%d finding(s)", len(a.findings)))
	a.outside = specScope(st).Outside(changed)

	if len(st.gate) > 0 {
		a.hermetic, a.env = hermeticGate(ctx, o, st)
	}

	if !o.NoReview {
		stat, _ := st.git.DiffStat(ctx, st.start)
		done := o.Progress.Begin("independent conformance review")
		review, stats, err := st.brain.Review(ctx, conform.ReviewInput{
			Root: st.root, Base: st.start, Spec: st.spec.RenderCombined(), Scope: reviewScope(st),
			ChangedFiles: changed, DiffStat: stat, TestCommands: st.gate,
		})
		recordPhase(o.Run, st, stats)
		done(toolio.PhaseSummary(stats))
		result.CostUSD = st.cost
		if err != nil {
			a.reviewErr = err
		} else {
			a.review = &review
		}
	}
	return a, nil
}

// hermeticGate runs the gate once more under a clean environment and
// fingerprints it.
func hermeticGate(ctx context.Context, o Options, st *runState) (*GateResult, *checks.Environment) {
	home, err := os.MkdirTemp("", "af-hermetic-home-")
	if err != nil {
		return nil, nil
	}
	defer os.RemoveAll(home)
	build := o.HermeticRunner
	if build == nil {
		build = gitx.HermeticRunner
	}
	r := build(home)
	clean := o
	clean.CheckRunner = r
	g := runGate(ctx, clean, st.root, st.gate, "clean-environment verification")
	env := checks.Fingerprint(ctx, r, st.root, true)
	return &g, &env
}

// conformance runs the stage and records what it found on the result.
func conformance(ctx context.Context, o Options, st *runState, result *Result) error {
	result.Stage = "reviewing"
	a, err := assess(ctx, o, st, result)
	if err != nil {
		return fail(conform.PhaseReview, CategoryGit, err)
	}

	var declared []conform.Deviation
	if blockers := a.blockers(); !o.NoReview && (len(blockers) > 0 || len(a.findings) > 0) {
		if stop := overBudget(o, st); stop == nil {
			sub, landed, err := runResolve(ctx, o, st, result, a, blockers)
			if err != nil {
				return err
			}
			if sub != nil {
				declared = sub.Deviations
			}
			if landed {
				if a, err = assess(ctx, o, st, result); err != nil {
					return fail(conform.PhaseReview, CategoryGit, err)
				}
			}
		}
	}

	result.FinalVerification, result.Environment = a.hermetic, a.env
	result.Review, result.Structural, result.OutOfScope = a.review, a.findings, a.outside
	if a.reviewErr != nil {
		o.Run.Warn(toolio.WarnReviewNotRun, "high", "the conformance review did not complete, so nothing "+
			"independent checked the change against the spec: %v", a.reviewErr)
	}

	// What the work declared — in a task or in the resolve phase — is unmet
	// and reported as such; a blocker it declared is no longer a blocker.
	var unmet []conform.Unmet
	isDeclared := map[string]bool{}
	for _, d := range taskDeviations(result) {
		unmet = append(unmet, declaredUnmet(d, a.changed, st))
		isDeclared[strings.ToUpper(d.Key)] = true
	}
	for _, d := range declared {
		unmet = append(unmet, declaredUnmet(d, a.changed, st))
		isDeclared[strings.ToUpper(d.Key)] = true
	}
	var blocking []conform.Blocker
	for _, b := range a.blockers() {
		if b.Declarable && (isDeclared[strings.ToUpper(b.Key)] || isDeclared[strings.ToUpper(b.Requirement)] ||
			isDeclared[strings.ToUpper(b.Test)]) {
			continue
		}
		blocking = append(blocking, b)
	}
	if a.review != nil {
		unmet = append(unmet, a.review.Shortfalls()...)
	}
	if a.reviewErr != nil {
		unmet = append(unmet, conform.Unmet{Source: conform.SourceUnresolved,
			What: "the independent conformance review did not complete: " + a.reviewErr.Error()})
	}
	for _, f := range a.findings {
		unmet = append(unmet, conform.Unmet{Source: conform.SourceStructural,
			What: fmt.Sprintf("%s at `%s`: %s", f.Check, f.Ref(), f.Message)})
	}
	result.Unmet, result.Blocking = unmet, blocking
	if len(unmet) > 0 {
		o.Run.Warn(toolio.WarnUnmetRequirements, "high", "%d unmet item(s): the pull request opens with them "+
			"and does not say the work is complete", len(unmet))
	}
	result.Stage = "committed"
	return nil
}

// taskDeviations collects what the tasks declared, in task order.
func taskDeviations(result *Result) []conform.Deviation {
	var out []conform.Deviation
	for _, t := range result.Tasks {
		if t.Submission != nil && t.Outcome == OutcomeDone {
			out = append(out, t.Submission.Deviations...)
		}
	}
	return out
}

// declaredUnmet turns a declaration into an unmet item, tracked by its
// erratum when the change carries that file.
func declaredUnmet(d conform.Deviation, changed []string, st *runState) conform.Unmet {
	u := conform.Unmet{Source: conform.SourceDeclared, Requirement: d.Key, Test: d.Test,
		What: strings.TrimSpace(d.Reason)}
	if strings.HasPrefix(strings.ToUpper(d.Key), "TS-") && d.Test == "" {
		u.Requirement, u.Test = "", d.Key
	}
	if e := strings.TrimPrefix(strings.TrimSpace(d.Errata), "./"); e != "" && slices.Contains(changed, e) {
		if _, err := os.Stat(st.root + "/" + e); err == nil {
			u.Tracking = e
		}
	}
	return u
}

// runResolve runs the resolve phase once. It reports the submission, and
// whether its change landed as a commit. A change that does not pass the
// checks is discarded, and with it whatever the phase declared: a
// declaration whose erratum was thrown away is tracked by nothing.
func runResolve(ctx context.Context, o Options, st *runState, result *Result, a assessment,
	blockers []conform.Blocker) (*ResolveSubmission, bool, error) {

	result.Stage = "resolving"
	report := &ResolveReport{Outcome: OutcomePending}
	result.Resolve = report
	head, err := st.git.Head(ctx)
	if err != nil {
		return nil, false, fail(PhaseResolve, CategoryGit, err)
	}
	var hermetic *GateResult
	if a.hermetic != nil && !a.hermetic.OK() {
		hermetic = a.hermetic
	}
	done := o.Progress.Begin("resolving %d blocking finding(s) and %d structural finding(s)", len(blockers), len(a.findings))
	sub, stats, err := st.brain.Resolve(ctx, resolveInput{
		Spec: st.spec, Root: st.root, Branch: st.branch, Gate: st.gate, Baseline: st.baseline,
		Survey: st.survey, Blockers: blockers, Findings: a.findings, Hermetic: hermetic,
		Instructions: projectInstructions(st.root), Steering: steering(st.specsDir), Profile: st.profile,
		Now: o.now(),
	})
	recordPhase(o.Run, st, stats)
	done(toolio.PhaseSummary(stats))
	result.CostUSD = st.cost
	revertSpecDir(ctx, o, st, head)
	dropScratchFiles(ctx, o, st)
	flagGateEdits(ctx, o, st, head)

	discardWith := func(reason string) (*ResolveSubmission, bool, error) {
		report.Outcome, report.Error = "discarded", reason
		o.Progress.Step(PhaseResolve, "the resolve phase's change was discarded: %s", reason)
		if err := discard(ctx, st, head); err != nil {
			return nil, false, fail(PhaseResolve, CategoryGit, err)
		}
		return nil, false, nil
	}
	if err != nil {
		if agentrun.CategoryOf(err) == agentrun.CategoryAborted {
			if derr := discard(ctx, st, head); derr != nil {
				return nil, false, fail(PhaseResolve, CategoryGit, derr)
			}
			return nil, false, fail(PhaseResolve, agentrun.CategoryAborted, err)
		}
		report.Outcome = OutcomeFailed
		return discardWith(err.Error())
	}
	report.Submission = &sub

	changed, err := st.git.ChangedFiles(ctx, head)
	if err != nil {
		return nil, false, fail(PhaseResolve, CategoryGit, err)
	}
	if len(changed) == 0 {
		report.Outcome = OutcomeDone
		return &sub, false, nil
	}
	report.ChangedFiles = changed
	after := runGate(ctx, o, st.root, st.gate, "resolve verification")
	verdict := compareGate(st.baseline, after)
	report.Verification, report.Verdict = &after, verdict
	if !landable(verdict, len(st.gate) == 0) {
		return discardWith(fmt.Sprintf("the checks did not pass after it (%s)", verdict))
	}
	commit, err := st.git.CommitAll(ctx, resolveCommitMessage(st.spec, sub, after))
	if err != nil {
		return nil, false, fail("commit", CategoryGit, err)
	}
	report.Commit, report.Outcome = commit, OutcomeDone
	st.baseline = after
	result.Verification, result.Verdict = after, verdict
	o.Progress.Step(PhaseResolve, "the conformance findings were answered in %s (%s)", commit, verdict)
	return &sub, true, nil
}

// trackDeviations files an issue for every declared item no erratum in the
// change tracks, when the run opens a pull request and may write to the
// forge. What cannot be filed stays untracked, and the pull request says so.
func trackDeviations(ctx context.Context, o Options, st *runState, result *Result) {
	if o.Land != LandPR || o.DryRun || o.Forge == nil || !o.Forge.Authenticated() || !st.target.Valid() {
		for _, u := range result.Unmet {
			if u.Source == conform.SourceDeclared && u.Tracking == "" {
				o.Run.Warn(toolio.WarnDeviationNotTracked, "high", "the declared deviation %s is tracked by "+
					"no erratum in the change, and this run files no issue", firstNonEmpty(u.Requirement, u.Test))
			}
		}
		return
	}
	for i, u := range result.Unmet {
		if u.Source != conform.SourceDeclared || u.Tracking != "" {
			continue
		}
		id := firstNonEmpty(u.Requirement, u.Test)
		stop := o.Run.Time("forge", "create_issue")
		issue, err := o.Forge.CreateIssue(ctx, st.target, issuex.CreateIssueRequest{
			Title: fmt.Sprintf("Spec %s: %s is not met", st.spec.SpecID, id),
			Body: fmt.Sprintf("`impl` implemented specification `%s` on `%s` and declared this requirement "+
				"unmet.\n\n- **Requirement:** %s\n- **Test:** %s\n\n%s\n", st.relSpecDir, st.branch,
				orDash(u.Requirement), orDash(u.Test), u.What),
		})
		stop()
		if err != nil {
			o.Run.RecordSideEffect("create_issue", st.target.String(), false, toolio.WarnDeviationNotTracked)
			o.Run.Warn(toolio.WarnDeviationNotTracked, "high", "the declared deviation %s could not be filed "+
				"as an issue: %v", id, err)
			continue
		}
		url := firstNonEmpty(issue.HTMLURL, issue.URL)
		o.Run.RecordSideEffectOf("create_issue", "deviation", st.target.String(), url, true, "")
		result.Unmet[i].Tracking = url
	}
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
