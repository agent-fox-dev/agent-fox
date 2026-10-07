package codeimpl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

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

// shortfalls are the review's partial requirements and weaker tests.
func (a assessment) shortfalls() []conform.Unmet {
	if a.review == nil {
		return nil
	}
	return a.review.Shortfalls()
}

// needsResolve reports whether the stage found anything the resolve phase
// could fix or declare: a blocker, a structural finding, a file outside the
// spec's scope, or a review shortfall.
func (a assessment) needsResolve() bool {
	return len(a.blockers()) > 0 || len(a.findings) > 0 || len(a.outside) > 0 || len(a.shortfalls()) > 0
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
// writes, and every file a baseline repair changed: the repair fixes the
// repository, not the spec, and the pull request says so.
//
// A spec author cannot list every file the work will need, so the scope also
// admits the paths the survey's resolutions name — the tasks are told to
// follow those decisions, and a scope that forbade them would make the tasks
// choose between the two — and a test file that names one of the spec's test
// ids, which is the spec's own test wherever it had to live.
func specScope(st *runState) conform.Scope {
	var allow []string
	add := func(p string) {
		if !slices.Contains(allow, p) {
			allow = append(allow, p)
		}
	}
	for _, t := range st.spec.Tasks.Tasks {
		if len(t.Touches) == 0 {
			return conform.Scope{}
		}
		for _, p := range t.Touches {
			add(p)
		}
	}
	for _, p := range surveyPaths(st.survey, st.root) {
		add(p)
	}
	ids := specTestIDs(st.spec)
	return conform.Scope{Allow: allow, Exempt: func(p string) bool {
		if project.IsDocsFile(p) || underSpec(st, p) || slices.Contains(st.repairFiles, p) {
			return true
		}
		if !project.IsTestPath(p) || len(ids) == 0 {
			return false
		}
		content, err := os.ReadFile(filepath.Join(st.root, filepath.FromSlash(p)))
		return err == nil && namesSpecTest(string(content), ids)
	}}
}

// specTestIDs are the ids of the spec's test cases.
func specTestIDs(spec *afspec.Spec) []string {
	if spec == nil || spec.TestSpec == nil {
		return nil
	}
	var ids []string
	for _, t := range spec.TestSpec.Tests {
		ids = append(ids, t.Id)
	}
	return ids
}

var testIDRe = regexp.MustCompile(`^TS-(\d+)-(\d+)$`)

// namesSpecTest reports whether content names one of ids, in any spelling the
// repository's tests use for TS-16-14: TS-16-14, TS16_14, TS_16_14 or TS1614,
// in any case — a Python or Rust test is test_ts16_14. The id must end where
// its number does, so TS-16-1 is not named by TS-16-14.
func namesSpecTest(content string, ids []string) bool {
	content = strings.ToUpper(content)
	for _, id := range ids {
		m := testIDRe.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(id)))
		if m == nil {
			continue
		}
		for _, form := range []string{"TS-" + m[1] + "-" + m[2], "TS" + m[1] + "_" + m[2],
			"TS_" + m[1] + "_" + m[2], "TS" + m[1] + m[2]} {
			for rest := content; ; {
				i := strings.Index(rest, form)
				if i < 0 {
					break
				}
				rest = rest[i+len(form):]
				if rest == "" || rest[0] < '0' || rest[0] > '9' {
					return true
				}
			}
		}
	}
	return false
}

// surveyPaths are the repository paths the survey's resolutions name: a word
// with a slash in it that is a directory in the repository, ends in one, or
// names a file by its extension. A directory is returned with its trailing
// slash, which the scope reads as everything under it.
func surveyPaths(s *Survey, root string) []string {
	if s == nil {
		return nil
	}
	var out []string
	for _, d := range s.Drift {
		words := strings.FieldsFunc(d.Resolution, func(r rune) bool {
			return unicode.IsSpace(r) || strings.ContainsRune("`\"'()[]{}<>,;:*", r)
		})
		for _, w := range words {
			w = strings.TrimRight(w, ".!?")
			w = strings.TrimPrefix(w, "./")
			if !strings.Contains(w, "/") || strings.HasPrefix(w, "/") || strings.Contains(w, "..") {
				continue
			}
			if !strings.HasSuffix(w, "/") {
				if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(w))); err == nil && info.IsDir() {
					w += "/"
				} else if path.Ext(w) == "" {
					continue
				}
			}
			if !slices.Contains(out, w) {
				out = append(out, w)
			}
		}
	}
	return out
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
		var reused bool
		if a.hermetic, a.env, reused = st.reusableClean(ctx); reused {
			o.Progress.Detail("clean-environment verification: the last landing gate ran there on this tree")
		} else {
			a.hermetic, a.env = hermeticGate(ctx, o, st)
		}
		if a.hermetic != nil && a.hermetic.aborted() {
			return a, failf(conform.PhaseReview, agentrun.CategoryAborted,
				"the run was cancelled during the clean-environment verification; the tasks are landed on the "+
					"branch, re-run to review them")
		}
	}

	if !o.NoReview && overBudget(o, st) != nil {
		a.reviewErr = fmt.Errorf("it was not run: the run has spent $%.2f of its $%.2f total budget",
			st.cost, o.TotalBudgetUSD)
	} else if !o.NoReview {
		// The structural scan and the clean-environment gate ran on this tree
		// first; the reviewer must search what is there now.
		st.invalidate()
		stat, _ := st.git.DiffStat(ctx, st.start)
		done := o.Progress.Begin("independent conformance review")
		review, stats, err := st.brain.Review(ctx, conform.ReviewInput{
			Root: st.root, Base: st.start, Spec: st.spec.RenderCombined(), Scope: reviewScope(st),
			ChangedFiles: changed, DiffStat: stat, TestCommands: st.gate,
		})
		recordPhase(o.Run, st, stats)
		done(toolio.PhaseSummary(stats))
		result.CostUSD = st.cost
		switch {
		case agentrun.CategoryOf(err) == agentrun.CategoryAborted:
			// A cancelled review is the run stopping, not a review that
			// failed: nothing lands on it.
			return a, failf(conform.PhaseReview, agentrun.CategoryAborted,
				"the run was cancelled during the conformance review; the tasks are landed on the branch, "+
					"re-run to review them")
		case err != nil:
			a.reviewErr = err
		default:
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
	g := st.runGate(ctx, clean, "clean-environment verification")
	env := checks.Fingerprint(ctx, r, st.root, true)
	return &g, &env
}

// conformance runs the stage and records what it found on the result.
func conformance(ctx context.Context, o Options, st *runState, result *Result) error {
	result.Stage = "reviewing"
	a, err := assess(ctx, o, st, result)
	if err != nil {
		return assessFailure(err)
	}

	var declared []conform.Deviation
	resolved := false
	if !o.NoReview && a.needsResolve() {
		if stop := overBudget(o, st); stop != nil {
			o.Run.Warn(toolio.WarnResolveNotRun, "high", "the conformance stage found what the resolve phase "+
				"would fix or declare, and it was not run: the run has spent $%.2f of its $%.2f total budget; "+
				"every finding is reported as it stands", st.cost, o.TotalBudgetUSD)
		} else {
			sub, landed, err := runResolve(ctx, o, st, result, a, a.blockers())
			if err != nil {
				return err
			}
			if sub != nil {
				declared = sub.Deviations
			}
			if landed {
				if a, err = assess(ctx, o, st, result); err != nil {
					return assessFailure(err)
				}
				resolved = true
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
	//
	// A declaration made before the resolve phase's change landed describes
	// the code as it was. The review after that change is the independent
	// account of the code the pull request opens with, so a declaration it
	// finds met is retracted rather than reported, and never filed as an
	// issue. Without a landed change the declaration stands: the review never
	// sees it, and may not know what the work found it could not do.
	var unmet []conform.Unmet
	isDeclared := map[string]bool{}
	for _, d := range mergeDeclarations(slices.Concat(st.priorDeviations, taskDeviations(result), declared)) {
		if resolved && a.review != nil && declarationMet(d, *a.review) {
			o.Progress.Step(conform.PhaseReview, "the declared deviation %s is retracted: the review after "+
				"the resolve phase's change finds it met", d.Key)
			continue
		}
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
	// A shortfall the work declared is reported once, as the declaration.
	for _, u := range a.shortfalls() {
		if isDeclared[strings.ToUpper(u.Requirement)] || isDeclared[strings.ToUpper(u.Test)] {
			continue
		}
		unmet = append(unmet, u)
	}
	if a.reviewErr != nil {
		unmet = append(unmet, conform.Unmet{Source: conform.SourceUnresolved,
			What: "the independent conformance review did not complete: " + a.reviewErr.Error()})
	}
	for _, f := range a.findings {
		unmet = append(unmet, conform.Unmet{Source: conform.SourceStructural,
			What: fmt.Sprintf("%s at `%s`: %s", f.Check, f.Ref(), f.Message)})
	}
	// A file outside the spec's scope is reported, not blocking: the scope is
	// what the spec's author could foresee, and a change that passes its
	// checks is not held back as a draft over a file the author did not list.
	for _, p := range a.outside {
		unmet = append(unmet, conform.Unmet{Source: conform.SourceStructural,
			What: fmt.Sprintf("`%s` is outside the files the spec's tasks list; a change the spec did not "+
				"ask for belongs in a pull request of its own", p)})
	}
	result.Unmet, result.Blocking = unmet, blocking
	if len(unmet) > 0 {
		o.Run.Warn(toolio.WarnUnmetRequirements, "high", "%d unmet item(s): the pull request opens with them "+
			"and does not say the work is complete", len(unmet))
	}
	result.Stage = "committed"
	return nil
}

// assessFailure is the failure of an assessment that did not complete: its
// own, when it says why (a cancellation), else a git failure.
func assessFailure(err error) *Failure {
	var f *Failure
	if errors.As(err, &f) {
		return f
	}
	return fail(conform.PhaseReview, CategoryGit, err)
}

// taskDeviations collects what the tasks that landed in this run declared,
// in task order. What tasks landed by an earlier run declared is read from
// their commits (st.priorDeviations).
func taskDeviations(result *Result) []conform.Deviation {
	var out []conform.Deviation
	for _, t := range result.Tasks {
		if t.Submission != nil && t.Outcome == OutcomeDone {
			out = append(out, t.Submission.Deviations...)
		}
	}
	return out
}

// mergeDeclarations keeps one declaration per key, in the order the keys
// were first declared. A later declaration's reason replaces an earlier one's:
// it was written against more of the change. Its test and erratum do too, but
// an earlier one's carry over when it names none, so a merge never leaves an
// item less tracked than it was.
func mergeDeclarations(ds []conform.Deviation) []conform.Deviation {
	var out []conform.Deviation
	at := map[string]int{}
	for _, d := range ds {
		k := strings.ToUpper(strings.TrimSpace(d.Key))
		i, seen := at[k]
		if !seen {
			at[k] = len(out)
			out = append(out, d)
			continue
		}
		prev := out[i]
		if strings.TrimSpace(d.Test) == "" {
			d.Test = prev.Test
		}
		if strings.TrimSpace(d.Errata) == "" {
			d.Errata = prev.Errata
		}
		out[i] = d
	}
	return out
}

// declarationMet reports whether the review finds a declaration met: every
// id it names — its key, and its test — that the review answers for is
// answered implemented, asserts_contract or followed, and the review answers
// for at least one of them. A row answers for its own id and for that id's
// sub-criteria (16-REQ-3 for 16-REQ-3.1); an id the review does not answer
// for, such as a range, is neither met nor unmet.
func declarationMet(d conform.Deviation, r conform.Review) bool {
	answered := false
	for _, id := range []string{d.Key, d.Test} {
		met, ok := reviewAnswer(strings.ToUpper(strings.TrimSpace(id)), r)
		if !ok {
			continue
		}
		if !met {
			return false
		}
		answered = true
	}
	return answered
}

// reviewAnswer is the review's answer for one id: whether it is met, and
// whether the review answers for it at all. The most specific row wins.
func reviewAnswer(id string, r conform.Review) (met, ok bool) {
	if id == "" {
		return false, false
	}
	best := -1
	consider := func(rowID string, rowMet bool) {
		rowID = strings.ToUpper(strings.TrimSpace(rowID))
		if (id == rowID || strings.HasPrefix(id, rowID+".")) && len(rowID) > best {
			best, met, ok = len(rowID), rowMet, true
		}
	}
	for _, row := range r.Requirements {
		consider(row.ID, row.Status == conform.StatusImplemented)
	}
	for _, row := range r.Tests {
		consider(row.ID, row.Assessment == conform.AssessAssertsContract)
	}
	for _, row := range r.Decisions {
		consider(row.ID, row.Status == conform.DecisionFollowed)
	}
	return met, ok
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
	// The review's own checks may have touched the tree: the resolver starts
	// from what is there now.
	st.invalidate()
	done := o.Progress.Begin("resolving %d blocking finding(s), %d structural finding(s), %d file(s) out of scope "+
		"and %d shortfall(s)", len(blockers), len(a.findings), len(a.outside), len(a.shortfalls()))
	st.noteUntracked(ctx)
	sub, stats, err := st.brain.Resolve(ctx, resolveInput{
		Spec: st.spec, Root: st.root, Branch: st.branch, Gate: st.gate, Suite: st.suite, Baseline: st.baseline,
		Survey: st.survey, Blockers: blockers, Findings: a.findings, Outside: a.outside, Shortfalls: a.shortfalls(),
		Hermetic:     hermetic,
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
	after := st.landingGate(ctx, o, "resolve verification", true)
	if after.aborted() {
		bg, cancel := background(ctx)
		defer cancel()
		if err := discard(bg, st, head); err != nil {
			return nil, false, fail(PhaseResolve, CategoryGit, err)
		}
		return nil, false, failf(PhaseResolve, agentrun.CategoryAborted,
			"the run was cancelled while the resolve phase's change was verified; it was discarded")
	}
	verdict := compareGate(st.baseline, after)
	report.Verification, report.Verdict = &after, verdict
	if !landable(verdict, len(st.gate) == 0) {
		return discardWith(fmt.Sprintf("the checks did not pass after it (%s)", verdict))
	}
	commit, err := st.commit(ctx, o, resolveCommitMessage(st.spec, sub, after), false, sub.Changes)
	if err != nil {
		return nil, false, fail("commit", CategoryGit, err)
	}
	st.landedClean(commit, true)
	report.Commit, report.Outcome = commit, OutcomeDone
	st.baseline = after
	result.Verification, result.Verdict = after, verdict
	o.Progress.Step(PhaseResolve, "the conformance findings were answered in %s (%s)", commit, verdict)
	return &sub, true, nil
}

// trackDeviations files one issue for the unmet items nothing in the change
// tracks — declared deviations without an erratum, review shortfalls,
// structural findings, files out of scope — when the run opens a pull
// request and may write to the forge. It is one issue however many items there are: they are the work the
// change left over, and whoever picks it up — a person, or `fix` — resolves
// them together, on one branch, against one list of acceptance criteria. Every
// item it lists is tracked by that issue. What cannot be filed stays
// untracked, and the pull request says so.
func trackDeviations(ctx context.Context, o Options, st *runState, result *Result) {
	var untracked []int
	for i, u := range result.Unmet {
		if u.Tracking == "" {
			untracked = append(untracked, i)
		}
	}
	if len(untracked) == 0 {
		return
	}
	if o.Land != LandPR || o.DryRun || o.Forge == nil || !o.Forge.Authenticated() || !st.target.Valid() {
		for _, i := range untracked {
			o.Run.Warn(toolio.WarnDeviationNotTracked, "high", "the unmet item %s is tracked by no erratum "+
				"in the change, and this run files no issue", unmetID(result.Unmet[i]))
		}
		return
	}
	items := make([]conform.Unmet, 0, len(untracked))
	for _, i := range untracked {
		items = append(items, result.Unmet[i])
	}
	stop := o.Run.Time("forge", "create_issue")
	issue, err := o.Forge.CreateIssue(ctx, st.target, issuex.CreateIssueRequest{
		Title: deviationIssueTitle(st.spec.SpecID, items),
		Body:  deviationIssueBody(st.relSpecDir, st.branch, items),
	})
	stop()
	if err != nil {
		o.Run.RecordSideEffect("create_issue", st.target.String(), false, toolio.WarnDeviationNotTracked)
		o.Run.Warn(toolio.WarnDeviationNotTracked, "high", "the unmet item(s) %s could not be filed "+
			"as an issue: %v", unmetIDs(items), err)
		return
	}
	url := firstNonEmpty(issue.HTMLURL, issue.URL)
	o.Run.RecordSideEffectOf("create_issue", "deviation", st.target.String(), url, true, "")
	for _, i := range untracked {
		result.Unmet[i].Tracking = url
	}
}

// deviationIssueTitle names the issue trackDeviations files. One item keeps
// the title an issue per item had, `Spec <id>: <key> is not met`, so a reader
// that finds an item's issue by its title still finds it; several are
// counted, and the body lists their keys.
func deviationIssueTitle(specID string, items []conform.Unmet) string {
	if len(items) == 1 {
		return fmt.Sprintf("Spec %s: %s is not met", specID, unmetID(items[0]))
	}
	return fmt.Sprintf("Spec %s: %d requirements and tests are not met", specID, len(items))
}

// deviationIssueBody renders the one issue: what was implemented and where,
// each item with its requirement, its test and what the work said about it,
// and an Acceptance Criteria checklist with one criterion per item — the
// section `fix` extracts and answers criterion by criterion, so a `fix` run on
// the issue is held to every item at once.
//
// What each item says is the model's account, so it is quoted: a heading in it
// cannot open a section of its own and pass for the criteria.
func deviationIssueBody(specDir, branch string, items []conform.Unmet) string {
	var b strings.Builder
	what := fmt.Sprintf("declared %d requirement(s) or test(s) unmet", len(items))
	if slices.ContainsFunc(items, func(u conform.Unmet) bool { return u.Source != conform.SourceDeclared }) {
		what = fmt.Sprintf("left %d item(s) unmet", len(items))
	}
	fmt.Fprintf(&b, "`impl` implemented specification `%s` on `%s` and %s. They are filed together, as one "+
		"piece of work: each is met on the branch, or an erratum in the change records why it is not.\n\n",
		specDir, branch, what)
	b.WriteString("## Unmet items\n\n")
	for n, u := range items {
		fmt.Fprintf(&b, "### %d. %s\n\n- **Requirement:** %s\n- **Test:** %s\n\n", n+1, unmetID(u),
			orDash(u.Requirement), orDash(u.Test))
		if what := strings.TrimSpace(u.What); what != "" {
			b.WriteString("> " + strings.ReplaceAll(what, "\n", "\n> ") + "\n\n")
		}
	}
	b.WriteString("## Acceptance Criteria\n\n")
	for n, u := range items {
		fmt.Fprintf(&b, "- [ ] AC-%d: %s is met on `%s`, or an erratum in the change records why it is not\n",
			n+1, unmetID(u), branch)
	}
	return b.String()
}

// unmetID is the key an unmet item is known by: its requirement, else its
// test, else — a structural finding or a file out of scope, which have
// neither — the start of what it says.
func unmetID(u conform.Unmet) string {
	if id := firstNonEmpty(u.Requirement, u.Test); id != "" {
		return id
	}
	what, _, _ := strings.Cut(strings.TrimSpace(u.What), "\n")
	if r := []rune(what); len(r) > 80 {
		what = string(r[:80]) + "…"
	}
	return what
}

// unmetIDs lists the keys of items, for a message.
func unmetIDs(items []conform.Unmet) string {
	ids := make([]string, 0, len(items))
	for _, u := range items {
		ids = append(ids, unmetID(u))
	}
	return strings.Join(ids, ", ")
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
