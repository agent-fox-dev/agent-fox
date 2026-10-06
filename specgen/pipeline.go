package specgen

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/repomap"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// DefaultSpecDirName is where spec packages live under the repository root.
const DefaultSpecDirName = ".specs"

// SpecDirEnv overrides the spec root. The --specs-dir flag wins over it.
const SpecDirEnv = "AF_SPEC_DIR"

// Options configure one spec generation run.
type Options struct {
	// Input is the classified argument: a product idea, a PRD file, or a
	// forge issue thread. Required.
	Input toolio.Input
	// Workspace roots the read-only source access. Required.
	Workspace *tools.Workspace
	// SpecsDir is where NN_name directories live. Empty means
	// <workspace>/.specs, or $AF_SPEC_DIR when that is set.
	SpecsDir string
	// Name overrides the spec name the model chooses. It must match
	// [a-z][a-z0-9_]*.
	Name string
	// Architecture runs the optional fifth phase and writes architecture.md.
	Architecture bool
	// Activate transitions a valid spec from draft to active, computing its
	// intent hash. A spec that does not validate is never activated.
	Activate bool
	// Comment posts the finished PRD back to the issue the input came from.
	Comment bool
	// DryRun writes nothing to disk and nothing to the forge. The generated
	// artifacts are still produced and reported.
	DryRun bool
	// TotalBudgetUSD caps the spend of the whole run. It is checked before
	// each scope's PRD phase after the first, the way codeimpl checks between
	// tasks; an input that does not split has no interior boundary and is
	// bound by the one phase's ceiling alone. Zero means no cap beyond the
	// per-phase bound.
	TotalBudgetUSD float64

	// RepoMapTokens is the token budget of the repository map in every
	// phase's user prompt (14-REQ-6.2). Zero, the zero value, disables it.
	RepoMapTokens int

	// Runner drives the model phases. Required unless author is injected.
	Runner *agentrun.Runner
	// Forge is the forge client, GitHub or GitLab, used only by Comment.
	Forge issuex.Client
	// Run records warnings and per-phase cost.
	Run *toolio.Run
	// Progress reports steps to stderr.
	Progress *toolio.Progress

	// author is the model half. It is unexported and injected by tests.
	author author
	// buildMap builds the repository map. It is unexported and injected by
	// tests; nil means repomap.Build.
	buildMap func(ctx context.Context, ws *tools.Workspace, budget int, inputPaths []string) (string, error)
}

// Result is what the tool reports as JSON.
//
// The fields of the first package this run wrote sit at the top level, so a
// run on an input that is one spec's worth of work reports exactly what it
// always did. An input that was more than that adds the further packages in
// FollowOnSpecs and the whole plan, scope by scope, in Split.
type Result struct {
	Package

	// FollowOnSpecs are the further packages this run wrote, in order, when
	// the input was more than one spec's worth of work.
	FollowOnSpecs []Package `json:"follow_on_specs,omitempty" description:"The further packages the run wrote, when the input was more than one spec of work."`
	// Split is every scope of the split with its state: done, invalid,
	// failed or pending. Empty when the input was one spec's worth of work.
	Split []ScopeReport `json:"split,omitempty" description:"Every scope of the split with its state: done, invalid, failed or pending."`
	// SplitPlan is the plan file left in the spec root while the split is
	// unfinished. Running spec on the same input again resumes from it.
	SplitPlan string `json:"split_plan,omitempty" trust:"fact" description:"The plan file left in the spec root while the split is unfinished."`
	// Stage is "preflight" on a --preflight run, which stops before any
	// model phase; it is absent on an ordinary run.
	Stage string `json:"stage,omitempty" trust:"fact" description:"preflight on a --preflight run, which stops before any model phase; absent on an ordinary run."`
	// DryRun records that nothing was written.
	DryRun bool `json:"dry_run,omitempty" description:"True when nothing was written."`
	// Preflight and Estimate are set only by a --preflight run whose checks
	// all passed (RunPreflight): the checks that were performed, and what the
	// real run would spend at most.
	Preflight []toolio.PreflightCheck `json:"preflight,omitempty" description:"Every check a --preflight run performed and its outcome. Present only under --preflight, when every refusing check passed."`
	Estimate  *toolio.Estimate        `json:"estimate,omitempty" description:"What the real run would spend at most: phases, per-phase ceilings and total. Present only under --preflight, when every refusing check passed."`

	// inputRef is how a next[] entry names the input to re-run it: the
	// literal file path or issue URL when the input had one, and the
	// needs_human.resume placeholder for raw text or stdin. It is not part
	// of the JSON result.
	inputRef string
}

// Summary returns one sentence describing the outcome in spec's vocabulary.
func (r Result) Summary() string {
	if r.Stage == "preflight" {
		return fmt.Sprintf("spec: preflight passed (%d checks)", len(r.Preflight))
	}
	var parts []string

	// Package(s) written / split progress
	if len(r.Split) > 0 {
		done := 0
		for _, s := range r.Split {
			if s.Status == ScopeDone {
				done++
			}
		}
		parts = append(parts, fmt.Sprintf("spec: split %d of %d scopes", done, len(r.Split)))
	} else {
		pkgName := r.SpecDir
		if pkgName == "" {
			pkgName = r.SpecID
		}
		if pkgName != "" {
			if len(r.FollowOnSpecs) > 0 {
				parts = append(parts, fmt.Sprintf("spec: wrote %s (+%d follow-on packages)", pkgName, len(r.FollowOnSpecs)))
			} else {
				parts = append(parts, fmt.Sprintf("spec: wrote %s", pkgName))
			}
		} else {
			parts = append(parts, "spec: done")
		}
	}

	totalQuestions := len(r.OpenQuestions)
	for _, f := range r.FollowOnSpecs {
		totalQuestions += len(f.OpenQuestions)
	}
	if totalQuestions > 0 {
		switch {
		case totalQuestions == 1:
			parts = append(parts, "1 open question")
		case len(r.FollowOnSpecs) > 0:
			// result.open_questions is the first package's; the rest are
			// counted per scope in split[].
			parts = append(parts, fmt.Sprintf("%d open questions across %d packages (per package in split[])",
				totalQuestions, 1+len(r.FollowOnSpecs)))
		default:
			parts = append(parts, fmt.Sprintf("%d open questions", totalQuestions))
		}
	}

	return strings.Join(parts, "; ")
}

// Resumable implements toolio.Resumabler. For specgen, this is true whenever
// SplitPlan is non-empty.
func (r Result) Resumable() bool {
	if r.Stage == "preflight" {
		return false
	}
	return r.SplitPlan != ""
}

// Package describes one specification package.
type Package struct {
	// SpecDir is the created package, relative to the repository root when
	// it is inside it.
	SpecDir  string `json:"spec_dir" trust:"fact" description:"The created package directory."`
	SpecID   string `json:"spec_id" trust:"fact" description:"The spec identifier."`
	SpecName string `json:"spec_name" trust:"fact" description:"The spec name."`
	Title    string `json:"title" trust:"model" description:"The spec title."`
	Status   string `json:"status" trust:"fact" description:"The spec status."`
	Source   string `json:"source" trust:"fact" description:"Where the spec came from."`
	// Artifacts are the files written, in the order they were produced.
	Artifacts []string `json:"artifacts" trust:"fact" description:"The files written, in the order they were produced."`

	// Counts summarize the package without reproducing it. A caller that
	// wants the content reads the files.
	Requirements   int `json:"requirements" description:"How many requirements the package has."`
	Criteria       int `json:"criteria" description:"How many criteria the package has."`
	ExecutionPaths int `json:"execution_paths" description:"How many execution paths the package has."`
	Tests          int `json:"tests" description:"How many tests the package has."`
	Tasks          int `json:"tasks" description:"How many tasks the package has."`

	// Validation is the format's own verdict on the package.
	Validation ValidationReport `json:"validation" description:"The format own verdict on the package."`
	// Traceability is derived from test.verifies and task.tests, never
	// stored — the format makes both computed, so reporting them here is the
	// only place they exist.
	Traceability TraceReport `json:"traceability" description:"Derived coverage of criteria and paths by tests and tasks."`

	// OpenQuestions are the decisions the PRD phase made under uncertainty
	// and would like a person to check. An empty list means the input and
	// the codebase settled everything.
	OpenQuestions []OpenQuestion `json:"open_questions,omitempty" description:"Decisions made under uncertainty that a person should check."`

	// CommentURL is set when the finished PRD was posted back to the issue.
	CommentURL string `json:"comment_url,omitempty" trust:"fact" description:"The comment the finished PRD was posted as, when it was."`

	// RelevantFiles are the files the PRD phase identified as important for
	// later generation phases. They live only in the envelope and the
	// run-time pipeline, never inside the spec package directory.
	RelevantFiles []RelevantFile `json:"relevant_files,omitempty" trust:"model" description:"Files the PRD phase found relevant for later phases."`

	// Detail records which view of this result was emitted: "summary" or
	// "full". It is present on both. It lives on Package (rather than on
	// Result, which embeds it) so it is set once for the first package and
	// carried the same way every other Package field is.
	Detail string `json:"detail" trust:"fact" description:"Which view of this result was emitted: summary or full."`
}

// SetDetail implements toolio.DetailedResult.
func (p *Package) SetDetail(d string) { p.Detail = d }

// Scope states, as reported in Result.Split.
const (
	// ScopeDone means the package exists and validates.
	ScopeDone = "done"
	// ScopeInvalid means the package exists and does not validate. It is
	// not written again; the errors name the rules to fix by hand.
	ScopeInvalid = "invalid"
	// ScopeFailed means this run stopped on this scope. The plan is kept,
	// and running spec on the same input again resumes here.
	ScopeFailed = "failed"
	// ScopePending means the scope was not reached.
	ScopePending = "pending"
)

// ScopeReport is one scope of a split and where it stands.
type ScopeReport struct {
	Name    string `json:"name" trust:"model" description:"The scope name."`
	Scope   string `json:"scope" trust:"model" description:"What the scope covers."`
	Status  string `json:"status" trust:"fact" description:"done, invalid, failed or pending."`
	SpecID  string `json:"spec_id,omitempty" trust:"fact" description:"The spec identifier, once the package exists."`
	SpecDir string `json:"spec_dir,omitempty" trust:"fact" description:"The package directory, once it exists."`
	// The package's own state, so the summary view, which describes only the
	// first package under result, still says whether every other one is
	// ready. Absent for a scope this run did not write.
	Valid              *bool `json:"valid,omitempty" description:"True when the package validates; absent when this run did not write it."`
	ErrorCount         int   `json:"error_count,omitempty" description:"How many validation errors the package has."`
	OpenQuestionsCount int   `json:"open_questions_count,omitempty" description:"How many open questions the package's PRD recorded."`
	TraceGaps          int   `json:"trace_gaps,omitempty" description:"Criteria and paths no test covers plus tests no task owns."`
}

// setSplit records the split report and fills each written scope's state from
// the package this run wrote for it.
func (r *Result) setSplit(report []ScopeReport) {
	pkgs := append([]Package{r.Package}, r.FollowOnSpecs...)
	for i := range report {
		if report[i].SpecDir == "" {
			continue
		}
		for _, p := range pkgs {
			if p.SpecDir != report[i].SpecDir {
				continue
			}
			valid := p.Validation.Valid
			report[i].Valid = &valid
			report[i].ErrorCount = p.Validation.ErrorCount
			report[i].OpenQuestionsCount = len(p.OpenQuestions)
			report[i].TraceGaps = len(p.Traceability.CriteriaUncovered) +
				len(p.Traceability.PathsUncovered) + len(p.Traceability.TestsUnowned)
			break
		}
	}
	r.Split = report
}

// ValidationReport is afspec's verdict, flattened for JSON.
type ValidationReport struct {
	Valid        bool             `json:"valid" description:"True when the package has no validation errors."`
	ErrorCount   int              `json:"error_count" description:"How many errors."`
	WarningCount int              `json:"warning_count" description:"How many warnings."`
	Errors       []ValidationItem `json:"errors,omitempty" description:"The errors found."`
	Warnings     []ValidationItem `json:"warnings,omitempty" description:"The warnings found."`
}

// ValidationItem is one finding, carrying the rule that produced it.
type ValidationItem struct {
	Check    string `json:"check" trust:"fact" description:"The rule that produced the finding."`
	Artifact string `json:"artifact" trust:"fact" description:"The artifact file the finding is about."`
	Entity   string `json:"entity,omitempty" trust:"fact" description:"The entity within the artifact, when there is one."`
	Message  string `json:"message" trust:"fact" description:"What is wrong."`
}

// TraceReport is the derived coverage summary.
type TraceReport struct {
	CriteriaCovered   int      `json:"criteria_covered" description:"How many criteria a test verifies."`
	CriteriaUncovered []string `json:"criteria_uncovered,omitempty" trust:"fact" description:"The criteria no test verifies."`
	PathsCovered      int      `json:"paths_covered" description:"How many execution paths a test exercises."`
	PathsUncovered    []string `json:"paths_uncovered,omitempty" trust:"fact" description:"The execution paths no test exercises."`
	TestsUnowned      []string `json:"tests_unowned,omitempty" trust:"fact" description:"The tests no task owns."`
}

// Failure carries the stage and category of a failed run.
type Failure struct {
	Stage    string
	Category string
	Err      error
	// TotalBudget is the --total-budget ceiling a budget stop was checked
	// against, so the envelope's fix_hint can name it. Zero otherwise.
	TotalBudget float64
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

// scoped prefixes a failure's message with the scope it happened in, keeping
// its stage and category so a caller still reads which step and whether a
// re-run could help.
func scoped(label string, err error) error {
	var f *Failure
	if errors.As(err, &f) {
		return &Failure{Stage: f.Stage, Category: f.Category, Err: fmt.Errorf("%s: %w", label, f.Err), TotalBudget: f.TotalBudget}
	}
	return fmt.Errorf("%s: %w", label, err)
}

// Categories this package adds to the shared vocabulary.
const (
	// CategoryInvalid means the package was generated and does not satisfy
	// the format's cross-file rules. The files are on disk and the errors
	// name the rules, because a spec you can read and fix is worth more than
	// no spec at all.
	CategoryInvalid = "invalid_spec"
	// CategoryDisk is a filesystem failure.
	CategoryDisk = "disk"
)

// Preflight validates the run options before spending turns or tokens.
func Preflight(ctx context.Context, o Options) (*Result, *Failure) {
	if o.Comment && !o.DryRun {
		if o.Input.Issue == nil {
			return nil, failf("preflight", "usage",
				"--comment posts the PRD back to the issue it came from, and the input is %s",
				o.Input.Kind)
		}
		if o.Forge == nil || !o.Forge.Authenticated() {
			return nil, failf("preflight", "auth",
				"--comment needs a forge credential: set GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN, or run without --comment")
		}
	}
	if o.Name != "" && !specNameRE.MatchString(o.Name) {
		return nil, failf("preflight", "usage", "--name %q must match [a-z][a-z0-9_]*", o.Name)
	}
	if o.Workspace == nil {
		return nil, failf("preflight", agentrun.CategoryInternal, "no workspace configured")
	}
	if o.Runner == nil && o.author == nil {
		return nil, failf("preflight", agentrun.CategoryInternal, "no runner configured")
	}
	for _, step := range afspec.GenerationSteps {
		if _, err := ArtifactSchema(step); err != nil {
			return nil, fail("preflight", agentrun.CategoryInternal, err)
		}
	}
	return nil, nil
}

// RunPreflight is spec --preflight: every check Run performs before its first
// model call, reported and then stopped at. It calls the one Preflight
// function Run calls, and the one findSplitPlan Run calls, never a copy of
// either, so a run that would refuse refuses here with the identical stage,
// category and message. A refusal returns no result: a partial checklist of
// what passed before it would be a second answer to "would this run start".
//
// Nothing is written: no package, no split plan, no comment, and no phase is
// run. What the package would be called is the model's to choose, so nothing
// about it is checked here.
func RunPreflight(ctx context.Context, o Options) (*Result, error) {
	if _, f := Preflight(ctx, o); f != nil {
		return nil, f
	}

	result := &Result{Stage: "preflight", DryRun: o.DryRun, inputRef: toolio.ResumePlaceholder(o.Input)}

	var list []toolio.PreflightCheck
	add := func(check string, ok bool, detail string) {
		list = append(list, toolio.PreflightCheck{Check: check, OK: ok, Detail: detail})
	}
	// The conditions below are the ones Preflight refuses on, so reaching
	// this line means each of them held.
	if o.Name != "" {
		add("name_flag", true, o.Name)
	}
	if o.Comment && !o.DryRun {
		add("comment_target", true, o.Input.Issue.String())
		add("comment_credential", true, "")
	}
	add("schemas_valid", true, "")

	// A dry run resumes nothing, so it looks for no plan, exactly as Run.
	packages := 1
	if !o.DryRun {
		plan, err := findSplitPlan(resolveSpecsDir(o, o.Workspace.Root), o.Input, o.Run)
		if err != nil {
			return nil, fail("preflight", "usage", err)
		}
		if plan != nil {
			packages = plan.Pending()
			add("split_plan", true, fmt.Sprintf("resuming a split: %d of %d scopes to write", plan.Pending(), len(plan.Scopes)))
		} else {
			add("split_plan", true, "no unfinished split for this input")
		}
	}
	result.Preflight = list

	// The phases the plan on disk already decides: one PRD phase and one per
	// generation step for each package still to write. Whether a PRD not yet
	// written calls for a split is the model's decision, so a fresh input is
	// the one-package figure, a lower bound.
	phases := 1 + len(afspec.GenerationSteps)
	if o.Architecture {
		phases++
	}
	phases *= packages
	est := &toolio.Estimate{Phases: phases}
	if o.Runner != nil {
		est.MaxTurnsPerPhase, est.MaxBudgetPerPhaseUSD = o.Runner.ResolvedBounds()
	}
	est.MaxTotalUSD = float64(phases) * est.MaxBudgetPerPhaseUSD
	result.Estimate = est
	return result, nil
}

// GeneratePRD runs spec generation and returns the primary package and its directory.
func GeneratePRD(ctx context.Context, o Options) (*Package, string, error) {
	res, err := Run(ctx, o)
	if res == nil {
		return nil, "", err
	}
	return &res.Package, res.SpecDir, err
}

// Run produces every specification package the input calls for.
//
// An input that is one spec's worth of work is one PRD phase and one
// package. An input the PRD phase reports as more than that is written as a
// split: the first package from the PRD just written, then one PRD phase and
// one package per remaining scope, each seeing the packages before it. The
// split is planned once, recorded in the spec root, and followed until every
// scope has a package — by this run, or by the next run on the same input if
// this one stops early.
//
// Within a package the order is the format's, not a preference:
// requirements, then tests, then tasks, each phase seeing the complete
// artifacts before it. A generator that cannot see a test id cannot own it,
// which is how the previous format produced specs whose edge-case and
// property tests belonged to no task.
func Run(ctx context.Context, o Options) (*Result, error) {
	if _, f := Preflight(ctx, o); f != nil {
		return nil, f
	}
	root := o.Workspace.Root
	specsDir := resolveSpecsDir(o, root)

	env := &runEnv{o: o, root: root, specsDir: specsDir, author: o.author}
	if env.author == nil {
		env.author = &agentAuthor{runner: o.Runner, ws: o.Workspace}
	}
	landscape, err := discoverLandscape(specsDir)
	if err != nil {
		o.Run.Warn(toolio.WarnSpecsDirUnreadable, "low", "could not read the existing specs in %s: %v", specsDir, err)
	}
	env.landscape = landscape
	env.profile = project.DetectProfile(root)
	if env.profile.Known() {
		o.Progress.Detail("project: %s (from %s), tests %q, linter %q",
			env.profile.Language, env.profile.Manifest, env.profile.AllTests, env.profile.Linter)
	} else {
		o.Run.Warn(toolio.WarnProjectLanguageUnknown, "low", "the project's language could not be determined from %s: the plan's test "+
			"commands cannot be checked against it", root)
	}

	// The spec tool never changes the tree, so the map is built once, before
	// the first phase, and every phase of every scope of a split reuses it
	// (14-REQ-8.2). A failure never fails the run: the phases go without it.
	env.repoMap = buildRepoMap(ctx, o)

	result := &Result{DryRun: o.DryRun, inputRef: toolio.ResumePlaceholder(o.Input)}

	// ---------------------------------------------------- resume or PRD --
	//
	// A plan left by an earlier run on this input is the split, already
	// decided; the PRD phase is not run again to re-decide it. A dry run
	// writes nothing and so also resumes nothing: it must not spend the
	// remaining scopes of a real plan without recording them.
	var plan *SplitPlan
	if !o.DryRun {
		plan, err = findSplitPlan(specsDir, o.Input, o.Run)
		if err != nil {
			return nil, fail("preflight", "usage", err)
		}
	}

	var first *PRD
	if plan != nil {
		if o.Name != "" {
			o.Run.Warn(toolio.WarnNameFlagIgnored, "low", "--name %q is ignored while resuming a split: the planned names are used", o.Name)
		}
		o.Run.Warn(toolio.WarnRelevantFilesUnavailable, "low",
			"the PRD phase was not run (resuming a split); the later phases run without the relevant-files block")
		o.Progress.Step("plan", "resuming the split planned for %s: %d of %d scopes to write",
			plan.Input.Origin, plan.Pending(), len(plan.Scopes))
	} else {
		prd, err := env.writePRD(ctx, nil)
		if err != nil {
			return nil, err
		}
		if o.Name != "" {
			prd.SpecName = o.Name
		}
		if len(prd.RecommendedSplit) > 0 {
			plan = newSplitPlan(o.Input, prd.RecommendedSplit)
			plan.Scopes[0].Name = prd.SpecName
			o.Progress.Step("plan", "the input is %d specs' worth of work; writing every scope: %s",
				len(plan.Scopes), scopeNames(plan))
			if !o.DryRun {
				if err := checkPlanSlot(plan, specsDir); err != nil {
					return nil, fail("plan", "usage", err)
				}
				if err := plan.save(specsDir); err != nil {
					return nil, fail("plan", CategoryDisk, fmt.Errorf("recording the split: %w", err))
				}
			}
		}
		first = &prd
	}

	// The plan is on disk from here until it is removed, whether this run
	// wrote it or found it, so the result names it from here: Resumable() and
	// next[] describe the disk, whichever way the run stops. A dry run reads
	// and writes no plan.
	if plan != nil && !o.DryRun {
		result.SplitPlan = relativeTo(root, plan.Path(specsDir))
	}

	// ---------------------------------------------------- one package --
	if plan == nil {
		pkg, _, err := env.buildPackage(ctx, *first, "")
		if pkg != nil {
			result.Package = *pkg
		}
		return result, err
	}

	// ------------------------------------------------------ the split --
	//
	// A package that does not validate stops nothing: it is on disk, its
	// errors name the rules, and the scopes after it are not made better by
	// being skipped. Every other failure stops the run where it is, with the
	// plan recording what exists, so the next run on the same input starts
	// from the scope that failed rather than from the beginning.
	var invalid []string
	wrote := 0
	for i := range plan.Scopes {
		sc := &plan.Scopes[i]
		if sc.Written() {
			continue
		}
		label := fmt.Sprintf("scope %d of %d (%s)", i+1, len(plan.Scopes), sc.Name)
		o.Progress.Step("write", "%s: %s", label, oneLine(sc.Scope))

		var prd PRD
		if first != nil && i == 0 {
			prd, first = *first, nil
		} else {
			// The interior boundary: this scope's PRD phase has not begun. A
			// run already over its ceiling stops here with the plan in place,
			// so the next run on the same input resumes from this scope.
			if spent := o.Run.CostUSD(); o.TotalBudgetUSD > 0 && spent >= o.TotalBudgetUSD {
				result.setSplit(splitReport(root, specsDir, plan, -1))
				bf := failf("budget", agentrun.CategoryBudget,
					"the run has spent $%.2f of its $%.2f total budget; the packages written so far "+
						"are on disk, re-run on the same input to continue", spent, o.TotalBudgetUSD)
				bf.TotalBudget = o.TotalBudgetUSD
				return result, scoped(label, bf)
			}
			prd, err = env.writePRD(ctx, &splitContext{Plan: plan, Index: i, SpecsDir: specsDir, SpecRoot: relativeTo(root, specsDir)})
			if err != nil {
				result.setSplit(splitReport(root, specsDir, plan, i))
				return result, scoped(label, err)
			}
			// The plan names the package, not the model: a name the model
			// changed here would leave the next run unable to tell that
			// this scope was written.
			if prd.SpecName != sc.Name {
				o.Run.Warn(toolio.WarnScopeRenamed, "low", "%s: the PRD phase named the spec %q; the planned name is used",
					label, prd.SpecName)
				prd.SpecName = sc.Name
			}
			if len(prd.RecommendedSplit) > 0 {
				o.Run.Warn(toolio.WarnScopeCountMismatch, "low", "%s: the PRD phase reported this scope as %d specs' worth of work; "+
					"it is written as one, since the split was already decided",
					label, len(prd.RecommendedSplit))
			}
		}

		pkg, dirName, err := env.buildPackage(ctx, prd, label)
		if pkg != nil {
			if wrote == 0 {
				result.Package = *pkg
			} else {
				result.FollowOnSpecs = append(result.FollowOnSpecs, *pkg)
			}
		}
		if err != nil {
			var f *Failure
			if !errors.As(err, &f) || f.Category != CategoryInvalid {
				result.setSplit(splitReport(root, specsDir, plan, i))
				return result, err
			}
			invalid = append(invalid, pkg.SpecDir)
			sc.Invalid = true
		}
		wrote++
		sc.SpecID, sc.Dir = pkg.SpecID, dirName
		if !o.DryRun {
			if err := plan.save(specsDir); err != nil {
				o.Run.Warn(toolio.WarnSplitPlanUpdateFailed, "high", "the split plan could not be updated after %s: %v", label, err)
			}
		}
	}

	result.setSplit(splitReport(root, specsDir, plan, -1))
	if !o.DryRun {
		if err := plan.remove(specsDir); err != nil {
			o.Run.Warn(toolio.WarnSplitPlanNotRemoved, "low", "the split is complete and its plan could not be removed: %v", err)
		} else {
			result.SplitPlan = ""
		}
	}
	if len(invalid) > 0 {
		return result, failf("validate", CategoryInvalid,
			"%d of %d packages do not validate (%s); each is on disk and each error names the "+
				"rule it broke", len(invalid), len(plan.Scopes), strings.Join(invalid, ", "))
	}
	o.Progress.Step("validate", "the split is complete: %d packages", len(plan.Scopes))
	return result, nil
}

// buildRepoMap builds the run's repository map. It never fails the run:
// navigation is an optimisation, so on an error the phases run without a map
// and a low warning records why (14-REQ-10.2). A zero RepoMapTokens disables
// the map. The idea's own words name the paths that are reduced last.
func buildRepoMap(ctx context.Context, o Options) string {
	build := o.buildMap
	if build == nil {
		build = repomap.Build
	}
	m, err := build(ctx, o.Workspace, o.RepoMapTokens, repomap.PathsIn(o.Input.Body))
	if err != nil {
		o.Run.Warn(toolio.WarnRepoMapBuildFailed, "low", "the repository map could not be built, so the phases run without it: %v", err)
		return ""
	}
	return m
}

// runEnv is what every package of a run shares.
type runEnv struct {
	o         Options
	root      string
	specsDir  string
	author    author
	profile   project.Profile
	landscape []afspec.SpecMeta
	// repoMap is the run's repository map, or "" when it is disabled or could
	// not be built.
	repoMap string
	// lastID is the highest numeric prefix this run has assigned, so a dry
	// run — which leaves nothing on disk to count — still numbers its
	// packages consecutively.
	lastID int
}

// writePRD runs the PRD phase, for the whole input or for one scope of it.
func (e *runEnv) writePRD(ctx context.Context, split *splitContext) (PRD, error) {
	o := e.o
	done := o.Progress.Begin("writing the PRD from %s", o.Input.Origin)
	prd, stats, err := e.author.WritePRD(ctx, prdRequest{
		Root:         e.root,
		SourceKind:   o.Input.Kind.String(),
		SourceOrigin: o.Input.Origin,
		Input:        o.Input.Body,
		Context:      o.Input.Context,
		Profile:      e.profile,
		Landscape:    e.landscape,
		SpecRoot:     relativeTo(e.root, e.specsDir),
		Steering:     project.Steering(e.specsDir),
		Split:        split,
		RepoMap:      e.repoMap,
	})
	scope := prd.SpecName
	if split != nil {
		scope = split.Scope().Name
	}
	recordPhase(o.Run, stats, scope)
	done(toolio.PhaseSummary(stats))
	if err != nil {
		return PRD{}, fail("prd", agentrun.CategoryOf(err), err)
	}
	return prd, nil
}

// allocateID is the next free numeric prefix: one past the highest on disk
// or the highest this run has used, whichever is larger.
func (e *runEnv) allocateID() string {
	n := maxSpecNumber(e.specsDir) + 1
	if n <= e.lastID {
		n = e.lastID + 1
	}
	e.lastID = n
	return formatSpecID(n)
}

// buildPackage turns one PRD into one package: the three generation phases,
// the optional architecture document, the write, the validation and the
// activation. label names the scope in progress messages and errors; it is
// empty for an undivided input.
//
// The package is returned alongside any error once it has a directory, so a
// caller can report what exists — an invalid package in particular is on
// disk and worth naming.
func (e *runEnv) buildPackage(ctx context.Context, prd PRD, label string) (*Package, string, error) {
	o := e.o
	specID := e.allocateID()
	dirName := specID + "_" + prd.SpecName
	if !afspec.IsSpecDirName(dirName) {
		return nil, "", failf("scaffold", "internal",
			"the derived directory name %q does not match NN_snake_case", dirName)
	}
	specPath := filepath.Join(e.specsDir, dirName)
	wrap := func(err error) error {
		if err == nil || label == "" {
			return err
		}
		return scoped(label, err)
	}

	pkg := &Package{
		SpecDir:       relativeTo(e.root, specPath),
		SpecID:        specID,
		SpecName:      prd.SpecName,
		Title:         prd.Title,
		Status:        "draft",
		Source:        sourceField(o.Input),
		OpenQuestions: prd.OpenQuestions,
		RelevantFiles: prd.RelevantFiles,
	}

	// ------------------------------------------------------- generation --
	spec := afspec.CreateSpec(specID, prd.SpecName)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	spec.CreatedAt, spec.UpdatedAt = now, now
	spec.Title = prd.Title
	spec.Source = pkg.Source
	spec.PRDBody = normalizeBody(prd.Body)

	partial := afspec.PartialSpec{SpecID: specID, SpecName: prd.SpecName}
	for _, step := range afspec.GenerationSteps {
		done := o.Progress.Begin("generating %s", afspec.ArtifactFileName(step))
		_, stats, err := e.author.GenerateArtifact(ctx, artifactRequest{
			Step:          step,
			SpecID:        specID,
			SpecName:      prd.SpecName,
			Root:          e.root,
			PRD:           spec.PRDBody,
			Profile:       e.profile,
			Landscape:     e.landscape,
			SpecRoot:      relativeTo(e.root, e.specsDir),
			Steering:      project.Steering(e.specsDir),
			Partial:       &partial,
			RelevantFiles: prd.RelevantFiles,
			RepoMap:       e.repoMap,
		})
		recordPhase(o.Run, stats, prd.SpecName)
		done(toolio.PhaseSummary(stats))
		if err != nil {
			return pkg, dirName, wrap(fail(string(step), agentrun.CategoryOf(err), err))
		}
		pkg.Artifacts = append(pkg.Artifacts, afspec.ArtifactFileName(step))
	}
	spec.Requirements = partial.Requirements
	spec.TestSpec = partial.TestSpec
	spec.Tasks = partial.Tasks

	if o.Architecture {
		done := o.Progress.Begin("writing architecture.md")
		doc, stats, err := e.author.WriteArchitecture(ctx, architectureRequest{
			SpecID: specID, SpecName: prd.SpecName, Root: e.root, PRD: spec.PRDBody, Partial: &partial,
			RelevantFiles: prd.RelevantFiles, RepoMap: e.repoMap,
		})
		recordPhase(o.Run, stats, prd.SpecName)
		done(toolio.PhaseSummary(stats))
		if err != nil {
			// The architecture document is optional by the format's own
			// definition, so failing to write it degrades the package rather
			// than failing the run that produced the four required artifacts.
			o.Run.Warn(toolio.WarnArchitectureNotWritten, "low", "architecture.md could not be written: %v", err)
		} else {
			spec.Architecture = doc
			pkg.Artifacts = append(pkg.Artifacts, "architecture.md")
		}
	}

	summarize(pkg, spec)

	// ------------------------------------------------------------ write --
	if !o.DryRun {
		if err := writeSpec(spec, e.specsDir, specPath); err != nil {
			return pkg, dirName, wrap(err)
		}
		o.Progress.Step("write", "wrote %s", specPath)
	}
	pkg.Artifacts = append([]string{"prd.md"}, pkg.Artifacts...)

	// --------------------------------------------------------- validate --
	//
	// What is validated is what was WRITTEN, re-read from disk, not the value
	// in memory. Save renders prd.md from the frontmatter fields and the body
	// and encodes the three artifacts canonically; validating the in-memory
	// spec would check the pipeline's intention rather than the package a
	// coder will open. A dry run has nothing on disk, so it validates the
	// value it would have written, with the directory name it would have used
	// — which is what makes the C1 folder check apply to both paths.
	validated := spec
	validated.Dir = specPath
	if !o.DryRun {
		loaded, err := afspec.LoadSpec(specPath)
		if err != nil {
			return pkg, dirName, wrap(fail("validate", CategoryDisk,
				fmt.Errorf("the package was written to %s and cannot be read back: %w", specPath, err)))
		}
		validated = loaded
	}

	report := reportOf(validated.Validate())
	pkg.Validation = report
	pkg.Traceability = traceOf(validated)

	// The package now exists as far as later scopes are concerned, valid or
	// not: the landscape lists it, so the next PRD is written against it.
	e.landscape = append(e.landscape, afspec.SpecMeta{
		SpecID: specID, SpecName: prd.SpecName, Status: pkg.Status, Dir: specPath,
	})

	if !report.Valid {
		o.Progress.Step("validate", "the package does not validate: %d error(s)", report.ErrorCount)
		return pkg, dirName, wrap(failf("validate", CategoryInvalid,
			"the generated package has %d validation error(s); it is on disk at %s and each "+
				"error names the rule it broke", report.ErrorCount, pkg.SpecDir))
	}
	o.Progress.Step("validate", "valid: %d requirements, %d tests, %d tasks",
		pkg.Requirements, pkg.Tests, pkg.Tasks)

	// --------------------------------------------------------- activate --
	if o.Activate && !o.DryRun {
		activated, err := validated.Transition("active", specPath)
		if err != nil {
			o.Run.Warn(toolio.WarnActivationFailed, "high", "the spec validates but could not be activated: %v", err)
		} else {
			pkg.Status = activated.Status
			e.landscape[len(e.landscape)-1].Status = activated.Status
			o.Progress.Step("activate", "activated %s", dirName)
		}
	}

	// ---------------------------------------------------------- comment --
	if o.Comment && !o.DryRun && o.Input.Issue != nil {
		body := prdComment(validated, pkg)
		stopTiming := o.Run.Time("forge", "comment:prd")
		url, err := o.Forge.AddComment(ctx, *o.Input.Issue, body)
		stopTiming()
		if err != nil {
			o.Run.Warn(toolio.WarnCommentNotPosted, "low", "the PRD could not be posted on %s: %v", o.Input.Issue, err)
			o.Run.RecordSideEffectOf("comment", "prd", o.Input.Issue.String(), "", false, toolio.WarnCommentNotPosted)
		} else {
			o.Run.RecordSideEffectOf("comment", "prd", o.Input.Issue.String(), url, true, "")
			pkg.CommentURL = url
			o.Progress.Step("prd", "posted the PRD to %s", url)
		}
	}
	return pkg, dirName, nil
}

// splitReport renders the plan for the result. failedAt is the index of the
// scope this run stopped on, or -1.
func splitReport(root, specsDir string, plan *SplitPlan, failedAt int) []ScopeReport {
	out := make([]ScopeReport, 0, len(plan.Scopes))
	for i, s := range plan.Scopes {
		r := ScopeReport{Name: s.Name, Scope: s.Scope, Status: ScopePending}
		switch {
		case s.Written():
			r.Status = ScopeDone
			if s.Invalid {
				r.Status = ScopeInvalid
			}
			r.SpecID = s.SpecID
			r.SpecDir = relativeTo(root, filepath.Join(specsDir, s.Dir))
		case i == failedAt:
			r.Status = ScopeFailed
		}
		out = append(out, r)
	}
	return out
}

func scopeNames(plan *SplitPlan) string {
	names := make([]string, 0, len(plan.Scopes))
	for _, s := range plan.Scopes {
		names = append(names, s.Name)
	}
	return strings.Join(names, ", ")
}

// writeSpec creates the package directory and saves the artifacts.
//
// The directory is removed again if the save fails, because a half-written
// spec directory is picked up by DiscoverSpecs and then reported as broken by
// every later run.
func writeSpec(spec *afspec.Spec, specsDir, specPath string) *Failure {
	if err := os.MkdirAll(specsDir, 0o755); err != nil {
		return fail("write", CategoryDisk, fmt.Errorf("creating %s: %w", specsDir, err))
	}
	if err := os.Mkdir(specPath, 0o755); err != nil {
		if os.IsExist(err) {
			return failf("write", CategoryDisk, "%s already exists", specPath)
		}
		return fail("write", CategoryDisk, fmt.Errorf("creating %s: %w", specPath, err))
	}
	if err := spec.Save(specPath); err != nil {
		_ = os.RemoveAll(specPath)
		return fail("write", CategoryDisk, fmt.Errorf("saving %s: %w", specPath, err))
	}
	return nil
}

// resolveSpecsDir picks the spec root: the flag, then the environment, then
// the repository's own .specs.
func resolveSpecsDir(o Options, root string) string {
	if o.SpecsDir != "" {
		if filepath.IsAbs(o.SpecsDir) {
			return filepath.Clean(o.SpecsDir)
		}
		return filepath.Join(root, o.SpecsDir)
	}
	if v := strings.TrimSpace(os.Getenv(SpecDirEnv)); v != "" {
		if filepath.IsAbs(v) {
			return filepath.Clean(v)
		}
		return filepath.Join(root, v)
	}
	return filepath.Join(root, DefaultSpecDirName)
}

// discoverLandscape lists the specs that already exist, so the new one is not
// written as though the repository had none.
func discoverLandscape(specsDir string) ([]afspec.SpecMeta, error) {
	if _, err := os.Stat(specsDir); err != nil {
		return nil, nil // a repository with no specs yet is not an error
	}
	metas, err := afspec.DiscoverSpecs(specsDir)
	if err != nil {
		return nil, err
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].SpecID < metas[j].SpecID })
	return metas, nil
}

// maxSpecNumber is the highest numeric prefix among the packages on disk,
// archived ones included, or 0 when there are none. An archived spec keeps
// its number: reusing it would make two specs answer to one ID.
func maxSpecNumber(specsDir string) int {
	return max(maxPrefixIn(specsDir), maxPrefixIn(filepath.Join(specsDir, "archive")))
}

// maxPrefixIn is the highest numeric prefix before the first `_` among the
// direct subdirectories of dir. A directory that cannot be read counts as
// empty.
func maxPrefixIn(dir string) int {
	highest := 0
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		i := strings.IndexByte(name, '_')
		if i <= 0 {
			continue
		}
		if n, err := strconv.Atoi(name[:i]); err == nil && n > highest {
			highest = n
		}
	}
	return highest
}

// formatSpecID zero-pads a prefix to two digits (and to three once past 99,
// so the directories keep sorting lexically).
func formatSpecID(n int) string {
	if n > 99 {
		return fmt.Sprintf("%03d", n)
	}
	return fmt.Sprintf("%02d", n)
}

// sourceField is the PRD frontmatter's provenance.
//
// It is set here rather than left blank, because "where did this spec come
// from" is the first question anyone asks of a generated spec and the answer
// is knowable at the moment it is written.
func sourceField(in toolio.Input) string {
	switch in.Kind {
	case toolio.KindIssue, toolio.KindFile:
		return in.Origin
	default:
		return "interactive"
	}
}

// normalizeBody strips any frontmatter fence a model added anyway, and
// guarantees the body ends with exactly one newline.
func normalizeBody(body string) string {
	body = strings.TrimSpace(body)
	if strings.HasPrefix(body, "---\n") {
		if i := strings.Index(body[4:], "\n---\n"); i >= 0 {
			body = strings.TrimSpace(body[4+i+5:])
		}
	}
	return body + "\n"
}

func summarize(r *Package, s *afspec.Spec) {
	if s.Requirements != nil {
		r.Requirements = len(s.Requirements.Requirements)
		for _, req := range s.Requirements.Requirements {
			r.Criteria += len(req.Criteria)
		}
		r.ExecutionPaths = len(s.Requirements.ExecutionPaths)
	}
	if s.TestSpec != nil {
		r.Tests = len(s.TestSpec.Tests)
	}
	if s.Tasks != nil {
		r.Tasks = len(s.Tasks.Tasks)
	}
}

func reportOf(v afspec.ValidationResult) ValidationReport {
	out := ValidationReport{
		Valid:        v.Valid,
		ErrorCount:   len(v.Errors),
		WarningCount: len(v.Warnings),
	}
	for _, e := range v.Errors {
		out.Errors = append(out.Errors, itemOf(e))
	}
	for _, w := range v.Warnings {
		out.Warnings = append(out.Warnings, itemOf(w))
	}
	return out
}

func itemOf(e afspec.ValidationEntry) ValidationItem {
	return ValidationItem{
		Check:    e.Check,
		Artifact: e.Artifact,
		Entity:   e.EntityID,
		Message:  e.Message,
	}
}

// traceOf derives the coverage summary.
//
// It is computed rather than read, because format v2 makes coverage and
// traceability derived: there is no coverage object in the artifacts to
// report, and the numbers here are the only place they exist. The matrix
// carries one link per criterion AND one per execution path, distinguished by
// Kind, which is why this walks it once rather than asking for coverage twice.
func traceOf(s *afspec.Spec) TraceReport {
	var out TraceReport
	for _, link := range s.ComputeTraceability().Links {
		switch link.Kind {
		case "path":
			if link.Covered {
				out.PathsCovered++
			} else {
				out.PathsUncovered = append(out.PathsUncovered, link.ID)
			}
		default:
			if link.Covered {
				out.CriteriaCovered++
			} else {
				out.CriteriaUncovered = append(out.CriteriaUncovered, link.ID)
			}
		}
	}
	out.TestsUnowned = unownedTests(s)
	return out
}

// unownedTests are the tests no task lists. Rule C7 already reports them as
// errors; repeating them here is what lets a caller act on the gap without
// parsing validation messages.
func unownedTests(s *afspec.Spec) []string {
	if s.TestSpec == nil || s.Tasks == nil {
		return nil
	}
	owned := map[string]bool{}
	for _, t := range s.Tasks.Tasks {
		for _, id := range t.Tests {
			owned[id] = true
		}
	}
	var out []string
	for _, t := range s.TestSpec.Tests {
		if !owned[t.Id] {
			out = append(out, t.Id)
		}
	}
	return out
}

// prdComment is what is posted back to the issue a spec was generated from.
// A split posts one comment per package, each carrying its own PRD.
func prdComment(s *afspec.Spec, r *Package) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Specification: `%s`\n\n", filepath.Base(r.SpecDir))
	fmt.Fprintf(&b, "Generated from this issue: %d requirements, %d criteria, %d tests, "+
		"%d tasks, %d execution paths. The package validates.\n\n",
		r.Requirements, r.Criteria, r.Tests, r.Tasks, r.ExecutionPaths)
	if len(r.OpenQuestions) > 0 {
		b.WriteString("### Decisions worth checking\n\n")
		for _, q := range r.OpenQuestions {
			fmt.Fprintf(&b, "- **%s** — %s (%s)\n",
				strings.TrimSpace(q.Question), strings.TrimSpace(q.Decision), strings.TrimSpace(q.Why))
		}
		b.WriteString("\n")
	}
	b.WriteString("### PRD\n\n")
	b.WriteString(strings.TrimSpace(s.PRDBody))
	b.WriteString("\n\n---\n*Written by `spec`. It is not a substitute for review.*\n")
	return b.String()
}

func relativeTo(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

// recordPhase puts a finished phase in the envelope, labelled with the spec
// it worked on: a split runs the same phases once per scope, and without the
// label the report cannot say what any one spec cost.
func recordPhase(run *toolio.Run, res agentrun.Result, scope string) {
	if run == nil || res.Name == "" {
		return
	}
	run.AddPhase(toolio.PhaseFromResult(res, scope))
}
