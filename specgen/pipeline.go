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
	"github.com/agent-fox-dev/agentfox/internal/ghapi"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// DefaultSpecDirName is where spec packages live under the repository root.
const DefaultSpecDirName = ".specs"

// SpecDirEnv overrides the spec root. The --specs-dir flag wins over it.
const SpecDirEnv = "AF_SPEC_DIR"

// Options configure one spec generation run.
type Options struct {
	// Input is the classified argument: a product idea, a PRD file, or a
	// GitHub issue thread. Required.
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
	// DryRun writes nothing to disk and nothing to GitHub. The generated
	// artifacts are still produced and reported.
	DryRun bool

	// Runner drives the model phases. Required unless author is injected.
	Runner *agentrun.Runner
	// GitHub is the REST client, used only by Comment.
	GitHub *ghapi.Client
	// Run records warnings and per-phase cost.
	Run *toolio.Run
	// Progress reports steps to stderr.
	Progress *toolio.Progress

	// author is the model half. It is unexported and injected by tests.
	author author
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
	FollowOnSpecs []Package `json:"follow_on_specs,omitempty"`
	// Split is every scope of the split with its state: done, invalid,
	// failed or pending. Empty when the input was one spec's worth of work.
	Split []ScopeReport `json:"split,omitempty"`
	// SplitPlan is the plan file left in the spec root while the split is
	// unfinished. Running spec on the same input again resumes from it.
	SplitPlan string `json:"split_plan,omitempty"`
	// DryRun records that nothing was written.
	DryRun bool `json:"dry_run,omitempty"`
}

// Package describes one specification package.
type Package struct {
	// SpecDir is the created package, relative to the repository root when
	// it is inside it.
	SpecDir  string `json:"spec_dir"`
	SpecID   string `json:"spec_id"`
	SpecName string `json:"spec_name"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Source   string `json:"source"`
	// Artifacts are the files written, in the order they were produced.
	Artifacts []string `json:"artifacts"`

	// Counts summarize the package without reproducing it. A caller that
	// wants the content reads the files.
	Requirements   int `json:"requirements"`
	Criteria       int `json:"criteria"`
	ExecutionPaths int `json:"execution_paths"`
	Tests          int `json:"tests"`
	Tasks          int `json:"tasks"`

	// Validation is the format's own verdict on the package.
	Validation ValidationReport `json:"validation"`
	// Traceability is derived from test.verifies and task.tests, never
	// stored — the format makes both computed, so reporting them here is the
	// only place they exist.
	Traceability TraceReport `json:"traceability"`

	// OpenQuestions are the decisions the PRD phase made under uncertainty
	// and would like a person to check. An empty list means the input and
	// the codebase settled everything.
	OpenQuestions []OpenQuestion `json:"open_questions,omitempty"`

	// CommentURL is set when the finished PRD was posted back to the issue.
	CommentURL string `json:"comment_url,omitempty"`
}

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
	Name    string `json:"name"`
	Scope   string `json:"scope"`
	Status  string `json:"status"`
	SpecID  string `json:"spec_id,omitempty"`
	SpecDir string `json:"spec_dir,omitempty"`
}

// ValidationReport is afspec's verdict, flattened for JSON.
type ValidationReport struct {
	Valid        bool             `json:"valid"`
	ErrorCount   int              `json:"error_count"`
	WarningCount int              `json:"warning_count"`
	Errors       []ValidationItem `json:"errors,omitempty"`
	Warnings     []ValidationItem `json:"warnings,omitempty"`
}

// ValidationItem is one finding, carrying the rule that produced it.
type ValidationItem struct {
	Check    string `json:"check"`
	Artifact string `json:"artifact"`
	Entity   string `json:"entity,omitempty"`
	Message  string `json:"message"`
}

// TraceReport is the derived coverage summary.
type TraceReport struct {
	CriteriaCovered   int      `json:"criteria_covered"`
	CriteriaUncovered []string `json:"criteria_uncovered,omitempty"`
	PathsCovered      int      `json:"paths_covered"`
	PathsUncovered    []string `json:"paths_uncovered,omitempty"`
	TestsUnowned      []string `json:"tests_unowned,omitempty"`
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

// scoped prefixes a failure's message with the scope it happened in, keeping
// its stage and category so a caller still reads which step and whether a
// re-run could help.
func scoped(label string, err error) error {
	var f *Failure
	if errors.As(err, &f) {
		return &Failure{Stage: f.Stage, Category: f.Category, Err: fmt.Errorf("%s: %w", label, f.Err)}
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
	if o.Workspace == nil {
		return nil, failf("preflight", agentrun.CategoryInternal, "no workspace configured")
	}
	if o.Runner == nil && o.author == nil {
		return nil, failf("preflight", agentrun.CategoryInternal, "no runner configured")
	}
	root := o.Workspace.Root
	specsDir := resolveSpecsDir(o, root)

	if o.Name != "" && !specNameRE.MatchString(o.Name) {
		return nil, failf("preflight", "usage", "--name %q must match [a-z][a-z0-9_]*", o.Name)
	}
	if o.Comment && !o.DryRun {
		if o.Input.Issue == nil {
			return nil, failf("preflight", "usage",
				"--comment posts the PRD back to the issue it came from, and the input is %s",
				o.Input.Kind)
		}
		if o.GitHub == nil || !o.GitHub.Authenticated() {
			return nil, failf("preflight", "auth",
				"--comment needs a credential: set GITHUB_TOKEN or GH_TOKEN, or drop --comment")
		}
	}

	// The three tool schemas are converted here, before a token is spent.
	// They are derived from the format's own JSON Schemas, so a schema no
	// provider will accept is an internal error rather than a usage one — and
	// meeting it after the PRD phase, as a vendor's 400 on the first
	// generation step, costs the PRD phase to learn something that was
	// knowable at the start.
	for _, step := range afspec.GenerationSteps {
		if _, err := ArtifactSchema(step); err != nil {
			return nil, fail("preflight", agentrun.CategoryInternal, err)
		}
	}

	env := &runEnv{o: o, root: root, specsDir: specsDir, author: o.author}
	if env.author == nil {
		env.author = &agentAuthor{runner: o.Runner}
	}
	landscape, err := discoverLandscape(specsDir)
	if err != nil {
		o.Run.Warn("could not read the existing specs in %s: %v", specsDir, err)
	}
	env.landscape = landscape
	env.profile = DetectProfile(root)
	if env.profile.Known() {
		o.Progress.Detail("project: %s (from %s), tests %q, linter %q",
			env.profile.Language, env.profile.Manifest, env.profile.AllTests, env.profile.Linter)
	} else {
		o.Run.Warn("the project's language could not be determined from %s: the plan's test "+
			"commands cannot be checked against it", root)
	}

	result := &Result{DryRun: o.DryRun}

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
			o.Run.Warn("--name %q is ignored while resuming a split: the planned names are used", o.Name)
		}
		o.Progress.Step("resuming the split planned for %s: %d of %d scopes to write",
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
			o.Progress.Step("the input is %d specs' worth of work; writing every scope: %s",
				len(plan.Scopes), scopeNames(plan))
			if !o.DryRun {
				if err := checkPlanSlot(plan, specsDir); err != nil {
					return nil, fail("plan", "usage", err)
				}
				if err := plan.save(specsDir); err != nil {
					return nil, fail("plan", CategoryDisk, fmt.Errorf("recording the split: %w", err))
				}
				result.SplitPlan = relativeTo(root, plan.Path(specsDir))
			}
		}
		first = &prd
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
		o.Progress.Step("%s: %s", label, oneLine(sc.Scope))

		var prd PRD
		if first != nil && i == 0 {
			prd, first = *first, nil
		} else {
			prd, err = env.writePRD(ctx, &splitContext{Plan: plan, Index: i})
			if err != nil {
				result.Split = splitReport(root, specsDir, plan, i)
				return result, scoped(label, err)
			}
			// The plan names the package, not the model: a name the model
			// changed here would leave the next run unable to tell that
			// this scope was written.
			if prd.SpecName != sc.Name {
				o.Run.Warn("%s: the PRD phase named the spec %q; the planned name is used",
					label, prd.SpecName)
				prd.SpecName = sc.Name
			}
			if len(prd.RecommendedSplit) > 0 {
				o.Run.Warn("%s: the PRD phase reported this scope as %d specs' worth of work; "+
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
				result.Split = splitReport(root, specsDir, plan, i)
				return result, err
			}
			invalid = append(invalid, pkg.SpecDir)
			sc.Invalid = true
		}
		wrote++
		sc.SpecID, sc.Dir = pkg.SpecID, dirName
		if !o.DryRun {
			if err := plan.save(specsDir); err != nil {
				o.Run.Warn("the split plan could not be updated after %s: %v", label, err)
			}
		}
	}

	result.Split = splitReport(root, specsDir, plan, -1)
	if !o.DryRun {
		if err := plan.remove(specsDir); err != nil {
			o.Run.Warn("the split is complete and its plan could not be removed: %v", err)
		} else {
			result.SplitPlan = ""
		}
	}
	if len(invalid) > 0 {
		return result, failf("validate", CategoryInvalid,
			"%d of %d packages do not validate (%s); each is on disk and each error names the "+
				"rule it broke", len(invalid), len(plan.Scopes), strings.Join(invalid, ", "))
	}
	o.Progress.Step("the split is complete: %d packages", len(plan.Scopes))
	return result, nil
}

// runEnv is what every package of a run shares.
type runEnv struct {
	o         Options
	root      string
	specsDir  string
	author    author
	profile   Profile
	landscape []afspec.SpecMeta
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
		Profile:      e.profile,
		Landscape:    e.landscape,
		Split:        split,
	})
	recordPhase(o.Run, stats)
	done(phaseSummary(stats))
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
			Step:      step,
			SpecID:    specID,
			SpecName:  prd.SpecName,
			Root:      e.root,
			PRD:       spec.PRDBody,
			Profile:   e.profile,
			Landscape: e.landscape,
			Partial:   &partial,
		})
		recordPhase(o.Run, stats)
		done(phaseSummary(stats))
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
		})
		recordPhase(o.Run, stats)
		done(phaseSummary(stats))
		if err != nil {
			// The architecture document is optional by the format's own
			// definition, so failing to write it degrades the package rather
			// than failing the run that produced the four required artifacts.
			o.Run.Warn("architecture.md could not be written: %v", err)
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
		o.Progress.Step("wrote %s", specPath)
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
		o.Progress.Step("the package does not validate: %d error(s)", report.ErrorCount)
		return pkg, dirName, wrap(failf("validate", CategoryInvalid,
			"the generated package has %d validation error(s); it is on disk at %s and each "+
				"error names the rule it broke", report.ErrorCount, pkg.SpecDir))
	}
	o.Progress.Step("valid: %d requirements, %d tests, %d tasks",
		pkg.Requirements, pkg.Tests, pkg.Tasks)

	// --------------------------------------------------------- activate --
	if o.Activate && !o.DryRun {
		activated, err := validated.Transition("active", specPath)
		if err != nil {
			o.Run.Warn("the spec validates but could not be activated: %v", err)
		} else {
			pkg.Status = activated.Status
			e.landscape[len(e.landscape)-1].Status = activated.Status
			o.Progress.Step("activated %s", dirName)
		}
	}

	// ---------------------------------------------------------- comment --
	if o.Comment && !o.DryRun {
		body := prdComment(validated, pkg)
		url, err := o.GitHub.AddComment(ctx, *o.Input.Issue, body)
		if err != nil {
			o.Run.Warn("the PRD could not be posted on %s: %v", o.Input.Issue, err)
		} else {
			pkg.CommentURL = url
			o.Progress.Step("posted the PRD to %s", url)
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
// or 0 when there are none.
func maxSpecNumber(specsDir string) int {
	max := 0
	entries, err := os.ReadDir(specsDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			i := strings.IndexByte(name, '_')
			if i <= 0 {
				continue
			}
			if n, err := strconv.Atoi(name[:i]); err == nil && n > max {
				max = n
			}
		}
	}
	return max
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
	return fmt.Sprintf("· %d turns · %s↑ %s↓", res.Turns,
		toolio.FormatTokens(int64(res.Usage.InputTokens)),
		toolio.FormatTokens(int64(res.Usage.OutputTokens)))
}
