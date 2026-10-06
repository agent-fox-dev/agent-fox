package specgen

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/agentrun/indextest"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// The tests below drive the REAL pipeline — the real scaffolding, the real
// afspec validation, the real lifecycle transition — with the model half
// replaced by a scripted author whose artifacts come from the repository's own
// fixture. That fixture is a valid version 2 package, so a pipeline that
// mangles it fails here rather than in production.

const fixtureSpecID = "01"

// loadFixture reads testdata/valid_spec and re-labels its artifacts with the
// spec id the pipeline will assign, so the C1 rule (ids agree everywhere) is
// exercised rather than sidestepped.
func loadFixture(t *testing.T, specID, specName string) (map[afspec.GenerationStep]map[string]any, string) {
	t.Helper()
	root := filepath.Join("..", "testdata", "valid_spec")

	artifacts := map[afspec.GenerationStep]map[string]any{}
	for _, step := range afspec.GenerationSteps {
		raw, err := os.ReadFile(filepath.Join(root, afspec.ArtifactFileName(step)))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ReplaceAll(string(raw), `"`+fixtureSpecID+`-`, `"`+specID+`-`)
		text = strings.ReplaceAll(text, `TS-`+fixtureSpecID+`-`, `TS-`+specID+`-`)
		text = strings.ReplaceAll(text, `"spec_id": "`+fixtureSpecID+`"`, `"spec_id": "`+specID+`"`)
		text = strings.ReplaceAll(text, `"spec_name": "test_feature"`, `"spec_name": "`+specName+`"`)

		var content map[string]any
		if err := json.Unmarshal([]byte(text), &content); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		artifacts[step] = content
	}

	prd, err := os.ReadFile(filepath.Join(root, "prd.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(prd)
	if i := strings.Index(body[4:], "\n---\n"); i >= 0 {
		body = strings.TrimSpace(body[4+i+5:])
	}
	return artifacts, body
}

// scriptedAuthor stands in for the four model phases.
type scriptedAuthor struct {
	t         *testing.T
	prd       PRD
	artifacts map[afspec.GenerationStep]map[string]any
	// primary is the "NN_name" the artifacts above were relabelled for. A
	// package with another id or name — a later scope of a split — gets the
	// fixture relabelled for it on demand.
	primary string
	arch    string

	prdErr      error
	artifactErr map[afspec.GenerationStep]error
	// skipStepValidation submits an artifact without the per-step check,
	// standing in for a violation that only whole-package validation can see.
	skipStepValidation bool
	// breakScope names the split scope whose tasks artifact is submitted
	// with one test unowned, so that package does not validate.
	breakScope string

	// followOn scripts the PRD phase for one scope of a split, by scope
	// name. A scope with no script gets the fixture PRD under the planned
	// name; one in followOnErr fails instead.
	followOn    map[string]PRD
	followOnErr map[string]error

	// prdCost is the spend every PRD phase reports.
	prdCost float64

	steps       []afspec.GenerationStep
	prdRequests []prdRequest
}

func (a *scriptedAuthor) WritePRD(_ context.Context, req prdRequest) (PRD, agentrun.Result, error) {
	a.prdRequests = append(a.prdRequests, req)
	res := agentrun.Result{Name: "prd", Turns: 3}
	res.Usage.CostUSD = a.prdCost
	if req.Split == nil {
		return a.prd, res, a.prdErr
	}
	name := req.Split.Scope().Name
	if err := a.followOnErr[name]; err != nil {
		return PRD{}, res, err
	}
	if prd, ok := a.followOn[name]; ok {
		return prd, res, nil
	}
	prd := a.prd
	prd.SpecName, prd.Title, prd.RecommendedSplit = name, "Scope "+name, nil
	return prd, res, nil
}

func (a *scriptedAuthor) GenerateArtifact(_ context.Context, req artifactRequest) (map[string]any, agentrun.Result, error) {
	a.steps = append(a.steps, req.Step)
	res := agentrun.Result{Name: "generate:" + string(req.Step), Turns: 2}
	if err := a.artifactErr[req.Step]; err != nil {
		return nil, res, err
	}
	content := a.artifacts[req.Step]
	if req.SpecID+"_"+req.SpecName != a.primary {
		fixtures, _ := loadFixture(a.t, req.SpecID, req.SpecName)
		content = fixtures[req.Step]
	}
	skip := a.skipStepValidation
	if a.breakScope != "" && req.SpecName == a.breakScope && req.Step == afspec.StepTasks {
		dropOneOwnedTest(content)
		skip = true
	}
	// The pipeline validates through the same handler the tool uses, so the
	// scripted author goes through it too rather than around it.
	if skip {
		decoded, err := afspec.DecodeArtifact(req.Step, content)
		if err != nil {
			return nil, res, err
		}
		switch v := decoded.(type) {
		case *afspec.RequirementsV2Json:
			req.Partial.Requirements = v
		case *afspec.TestSpecV2Json:
			req.Partial.TestSpec = v
		case *afspec.TasksV2Json:
			req.Partial.Tasks = v
		}
		return content, res, nil
	}
	if err := validateArtifactContent(content, req.Step, req.Partial); err != nil {
		return nil, res, err
	}
	return content, res, nil
}

// dropOneOwnedTest breaks rule C7 in a tasks artifact: task 1 owns
// TS-NN-1..3, and dropping one leaves that test owned by nothing.
func dropOneOwnedTest(tasksContent map[string]any) {
	tasks := tasksContent["tasks"].([]any)
	first := tasks[0].(map[string]any)
	tests := first["tests"].([]any)
	first["tests"] = tests[:len(tests)-1]
}

func (a *scriptedAuthor) WriteArchitecture(context.Context, architectureRequest) (string, agentrun.Result, error) {
	if a.arch == "" {
		return "", agentrun.Result{Name: "architecture"}, errors.New("no architecture scripted")
	}
	return a.arch, agentrun.Result{Name: "architecture", Turns: 2}, nil
}

func newAuthor(t *testing.T, specID, specName string) *scriptedAuthor {
	t.Helper()
	artifacts, body := loadFixture(t, specID, specName)
	return &scriptedAuthor{
		t: t,
		prd: PRD{
			SpecName: specName,
			Title:    "Test Feature",
			Body:     body,
			OpenQuestions: []OpenQuestion{{
				Question: "Should the loader follow symlinks?",
				Decision: "Yes, matching the rest of the project.",
				Why:      "No existing code settles it either way.",
			}},
		},
		artifacts:   artifacts,
		primary:     specID + "_" + specName,
		artifactErr: map[afspec.GenerationStep]error{},
		followOn:    map[string]PRD{},
		followOnErr: map[string]error{},
	}
}

func newWorkspace(t *testing.T) *tools.Workspace {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := tools.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func newOptions(ws *tools.Workspace, a author) Options {
	return Options{
		Input: toolio.Input{
			Kind: toolio.KindText, Origin: "argument",
			Body: "a library that loads and validates spec packages",
		},
		Workspace: ws,
		Activate:  true,
		Run:       toolio.NewRun("spec", "test"),
		Progress:  toolio.NewProgress(io.Discard, "spec", false, true),
		author:    a,
	}
}

func TestPipelineWritesAValidatedPackage(t *testing.T) {
	ws := newWorkspace(t)
	a := newAuthor(t, "01", "test_feature")

	got, err := Run(context.Background(), newOptions(ws, a))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got.SpecID != "01" || got.SpecName != "test_feature" {
		t.Errorf("id/name = %q/%q", got.SpecID, got.SpecName)
	}
	if got.SpecDir != filepath.Join(".specs", "01_test_feature") {
		t.Errorf("SpecDir = %q", got.SpecDir)
	}
	if !got.Validation.Valid {
		t.Fatalf("the package does not validate: %+v", got.Validation.Errors)
	}
	if got.Status != "active" {
		t.Errorf("Status = %q, want a valid package to be activated", got.Status)
	}
	if got.Source != "interactive" {
		t.Errorf("Source = %q", got.Source)
	}

	// The order is the format's rule, not a preference.
	want := []afspec.GenerationStep{afspec.StepRequirements, afspec.StepTestSpec, afspec.StepTasks}
	if len(a.steps) != len(want) {
		t.Fatalf("steps = %v", a.steps)
	}
	for i := range want {
		if a.steps[i] != want[i] {
			t.Fatalf("steps = %v, want %v", a.steps, want)
		}
	}

	// The files are on disk and load back.
	dir := filepath.Join(ws.Root, got.SpecDir)
	for _, name := range []string{"prd.md", "requirements.json", "test_spec.json", "tasks.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	loaded, err := afspec.LoadSpec(dir)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if loaded.Title != "Test Feature" {
		t.Errorf("title = %q; an empty one fails the frontmatter schema", loaded.Title)
	}
	if loaded.IntentHash == nil || *loaded.IntentHash == "" {
		t.Error("activation should have computed the intent hash")
	}
	if res := loaded.Validate(); !res.Valid {
		t.Errorf("the saved package does not validate: %+v", res.Errors)
	}

	// The counts and the derived traceability are reported rather than stored.
	if got.Requirements == 0 || got.Tests == 0 || got.Tasks == 0 {
		t.Errorf("counts = %+v", got)
	}
	if len(got.Traceability.CriteriaUncovered) != 0 || len(got.Traceability.TestsUnowned) != 0 {
		t.Errorf("traceability = %+v", got.Traceability)
	}
	if got.Traceability.CriteriaCovered != got.Criteria {
		t.Errorf("covered %d of %d criteria", got.Traceability.CriteriaCovered, got.Criteria)
	}

	// The decisions made under uncertainty are surfaced, not hidden.
	if len(got.OpenQuestions) != 1 {
		t.Errorf("OpenQuestions = %+v", got.OpenQuestions)
	}
}

// The numeric prefix is the next free one, so a second spec does not collide
// with the first.
func TestNextSpecIDFollowsTheExistingPackages(t *testing.T) {
	ws := newWorkspace(t)
	if _, err := Run(context.Background(), newOptions(ws, newAuthor(t, "01", "first_feature"))); err != nil {
		t.Fatalf("first run: %v", err)
	}
	second := newAuthor(t, "02", "second_feature")
	got, err := Run(context.Background(), newOptions(ws, second))
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got.SpecID != "02" {
		t.Errorf("SpecID = %q, want the next free prefix", got.SpecID)
	}
}

// An archived spec keeps its number, so the next package never reuses it.
func TestMaxSpecNumberCountsArchivedSpecs(t *testing.T) {
	mk := func(t *testing.T, dirs ...string) string {
		root := t.TempDir()
		for _, d := range dirs {
			if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}
	cases := []struct {
		name string
		dirs []string
		want int
	}{
		{"only archived", []string{"archive/01_a", "archive/02_b"}, 2},
		{"archived above active", []string{"02_x", "archive/05_y"}, 5},
		{"active above archived", []string{"07_x", "archive/05_y"}, 7},
		{"no archive directory", []string{"01_a", "03_c"}, 3},
		{"nothing at all", nil, 0},
	}
	for _, c := range cases {
		if got := maxSpecNumber(mk(t, c.dirs...)); got != c.want {
			t.Errorf("%s: maxSpecNumber = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestAllocatedIDsSkipArchivedSpecsAndStayIncreasing(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"archive/01_a", "archive/02_b"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e := &runEnv{specsDir: root}
	for _, want := range []string{"03", "04", "05"} {
		if got := e.allocateID(); got != want {
			t.Errorf("allocateID = %s, want %s", got, want)
		}
	}
}

// A package that does not validate is still written: a spec you can read and
// fix is worth more than no spec at all, and the errors name the rules.
//
// The break is rule C7 — a test no task owns — introduced with the per-step
// check bypassed, which is what a model that satisfies each step and still
// produces an inconsistent package looks like from the pipeline's side.
func TestAnInvalidPackageIsWrittenAndReported(t *testing.T) {
	ws := newWorkspace(t)
	a := newAuthor(t, "01", "test_feature")
	a.skipStepValidation = true

	dropOneOwnedTest(a.artifacts[afspec.StepTasks])

	got, err := Run(context.Background(), newOptions(ws, a))
	if err == nil {
		t.Fatal("want an error for an invalid package")
	}
	var f *Failure
	if !errors.As(err, &f) || f.Category != CategoryInvalid {
		t.Fatalf("err = %v (%T)", err, err)
	}
	if got == nil || got.Validation.Valid {
		t.Fatalf("result = %+v", got)
	}
	if got.Status == "active" {
		t.Error("an invalid package must not be activated")
	}
	if _, err := os.Stat(filepath.Join(ws.Root, got.SpecDir, "tasks.json")); err != nil {
		t.Errorf("the package should still be on disk: %v", err)
	}
	if got.Validation.ErrorCount == 0 {
		t.Fatal("no errors were reported")
	}
	var sawC7 bool
	for _, e := range got.Validation.Errors {
		if e.Check == "" {
			t.Errorf("an error with no rule name: %+v", e)
		}
		if e.Check == "C7" {
			sawC7 = true
		}
	}
	if !sawC7 {
		t.Errorf("want the C7 rule named: %+v", got.Validation.Errors)
	}
	// The derived trace names the same gap without parsing a message.
	if len(got.Traceability.TestsUnowned) != 1 {
		t.Errorf("TestsUnowned = %v", got.Traceability.TestsUnowned)
	}
}

// --dry-run produces the package and writes nothing.
func TestDryRunWritesNothing(t *testing.T) {
	ws := newWorkspace(t)
	o := newOptions(ws, newAuthor(t, "01", "test_feature"))
	o.DryRun = true

	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !got.Validation.Valid {
		t.Errorf("the package should still be validated: %+v", got.Validation.Errors)
	}
	if got.Status != "draft" {
		t.Errorf("Status = %q, want draft under --dry-run", got.Status)
	}
	if _, err := os.Stat(filepath.Join(ws.Root, ".specs")); !os.IsNotExist(err) {
		t.Error("--dry-run created the spec root")
	}
}

// A failure in a generation step leaves nothing half-written.
func TestAFailedGenerationStepWritesNothing(t *testing.T) {
	ws := newWorkspace(t)
	a := newAuthor(t, "01", "test_feature")
	a.artifactErr[afspec.StepTasks] = errors.New("budget exceeded")

	_, err := Run(context.Background(), newOptions(ws, a))
	if err == nil {
		t.Fatal("want an error")
	}
	if entries, _ := os.ReadDir(filepath.Join(ws.Root, ".specs")); len(entries) != 0 {
		t.Errorf("a half-written package was left behind: %v", entries)
	}
}

// The optional architecture document is optional by the format's own
// definition, so failing to write it degrades the package rather than failing
// the run that produced the four required artifacts.
func TestAFailedArchitectureIsAWarningNotAFailure(t *testing.T) {
	ws := newWorkspace(t)
	a := newAuthor(t, "01", "test_feature")
	o := newOptions(ws, a)
	o.Architecture = true

	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !got.Validation.Valid {
		t.Errorf("the package should still validate: %+v", got.Validation.Errors)
	}
	warnings := o.Run.Warnings()
	if len(warnings) == 0 || !strings.Contains(strings.Join(toolio.WarningMessages(warnings), "\n"), "architecture.md") {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestArchitectureIsWrittenWhenAsked(t *testing.T) {
	ws := newWorkspace(t)
	a := newAuthor(t, "01", "test_feature")
	a.arch = "# Architecture: test_feature\n\n## Overview\n\nIt loads specs.\n"
	o := newOptions(ws, a)
	o.Architecture = true

	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(ws.Root, got.SpecDir, "architecture.md"))
	if err != nil {
		t.Fatalf("architecture.md: %v", err)
	}
	if !strings.HasPrefix(string(b), "# Architecture") {
		t.Errorf("architecture.md = %q", string(b))
	}
}

// An unusable --name is refused before anything is written.
func TestAnInvalidNameIsRefused(t *testing.T) {
	ws := newWorkspace(t)
	o := newOptions(ws, newAuthor(t, "01", "test_feature"))
	o.Name = "Not Valid"

	_, err := Run(context.Background(), o)
	if err == nil {
		t.Fatal("want an error")
	}
	var f *Failure
	if !errors.As(err, &f) || f.Stage != "preflight" || f.Category != "usage" {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws.Root, ".specs")); !os.IsNotExist(err) {
		t.Error("nothing should have been written")
	}
}

func TestSourceRecordsProvenance(t *testing.T) {
	cases := []struct {
		in   toolio.Input
		want string
	}{
		{toolio.Input{Kind: toolio.KindText, Origin: "argument"}, "interactive"},
		{toolio.Input{Kind: toolio.KindStdin, Origin: "stdin"}, "interactive"},
		{toolio.Input{Kind: toolio.KindFile, Origin: "docs/prd.md"}, "docs/prd.md"},
		{toolio.Input{Kind: toolio.KindIssue, Origin: "https://github.com/a/b/issues/1"},
			"https://github.com/a/b/issues/1"},
	}
	for _, c := range cases {
		if got := sourceField(c.in); got != c.want {
			t.Errorf("sourceField(%s) = %q, want %q", c.in.Kind, got, c.want)
		}
	}
}

func TestNormalizeBodyStripsFrontmatterAndSettlesWhitespace(t *testing.T) {
	got := normalizeBody("---\ntitle: x\n---\n\n## Intent\n\nDo the thing.\n\n\n")
	if strings.HasPrefix(got, "---") {
		t.Errorf("frontmatter survived: %q", got)
	}
	if !strings.HasPrefix(got, "## Intent") || !strings.HasSuffix(got, "\n") {
		t.Errorf("normalizeBody = %q", got)
	}
}

// What is validated is what was written, re-read from disk. Validating the
// in-memory value would check the pipeline's intention rather than the package
// a coder will open.
func TestValidationReadsThePackageBackFromDisk(t *testing.T) {
	ws := newWorkspace(t)
	got, err := Run(context.Background(), newOptions(ws, newAuthor(t, "01", "test_feature")))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	dir := filepath.Join(ws.Root, got.SpecDir)

	// The round trip is the claim: what validated is what LoadSpec returns.
	loaded, err := afspec.LoadSpec(dir)
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if loaded.Dir == "" {
		t.Fatal("the loaded spec has no directory, so the C1 folder check is vacuous")
	}
	res := loaded.Validate()
	if res.Valid != got.Validation.Valid {
		t.Errorf("the reported verdict (%v) disagrees with the package on disk (%v)",
			got.Validation.Valid, res.Valid)
	}
	if len(res.Errors) != got.Validation.ErrorCount {
		t.Errorf("error counts disagree: %d reported, %d on disk", got.Validation.ErrorCount, len(res.Errors))
	}
}

// A dry run has nothing on disk, so it validates the value it would have
// written — including the directory name it would have used.
func TestDryRunStillValidatesAgainstTheDirectoryName(t *testing.T) {
	ws := newWorkspace(t)
	o := newOptions(ws, newAuthor(t, "01", "test_feature"))
	o.DryRun = true

	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !got.Validation.Valid {
		t.Errorf("validation = %+v", got.Validation.Errors)
	}
	if got.Traceability.CriteriaCovered == 0 {
		t.Error("the traceability report should be derived under --dry-run too")
	}
}

type mockForgeClient struct {
	issuex.NoOpClient
	authenticated bool
	commentURL    string
	commentErr    error
	commentCalls  int
	lastBody      string
	lastRef       issuex.IssueRef
}

func (m *mockForgeClient) Authenticated() bool {
	return m.authenticated
}

func (m *mockForgeClient) AddComment(ctx context.Context, ref issuex.IssueRef, body string) (string, error) {
	m.commentCalls++
	m.lastRef = ref
	m.lastBody = body
	if m.commentErr != nil {
		return "", m.commentErr
	}
	return m.commentURL, nil
}

// TS-04-29 (unit): specgen Options defines Forge field of type issuex.Client
// Verifies: 04-REQ-7.1
func TestTS0429_OptionsDefinesForgeField(t *testing.T) {
	var o Options
	var _ issuex.Client = o.Forge
}

// TS-04-30 (unit): specgen preflight returns auth failure when --comment is enabled without credentials
// Verifies: 04-REQ-7.2
func TestTS0430_PreflightAuthFailureWithoutCredentials(t *testing.T) {
	ctx := context.Background()
	issueRef := issuex.IssueRef{
		Repo:   issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"},
		Number: 42,
	}

	// Case 1: unauthenticated Forge client
	opts := Options{
		Comment: true,
		DryRun:  false,
		Input:   toolio.Input{Kind: toolio.KindIssue, Issue: &issueRef},
		Forge:   &mockForgeClient{authenticated: false},
	}
	_, err := Preflight(ctx, opts)
	if err == nil {
		t.Fatal("expected auth failure, got nil")
	}
	if err.StageName() != "preflight" {
		t.Errorf("StageName = %q, want preflight", err.StageName())
	}
	if err.CategoryName() != "auth" {
		t.Errorf("CategoryName = %q, want auth", err.CategoryName())
	}
	wantMsg := "--comment needs a forge credential: set GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN, or run without --comment"
	if !strings.Contains(err.Error(), wantMsg) {
		t.Errorf("error = %q, want message containing %q", err.Error(), wantMsg)
	}

	// Case 2: nil Forge client
	optsNil := Options{
		Comment: true,
		DryRun:  false,
		Input:   toolio.Input{Kind: toolio.KindIssue, Issue: &issueRef},
		Forge:   nil,
	}
	_, errNil := Preflight(ctx, optsNil)
	if errNil == nil {
		t.Fatal("expected auth failure for nil Forge, got nil")
	}
	if errNil.StageName() != "preflight" {
		t.Errorf("StageName = %q, want preflight", errNil.StageName())
	}
	if errNil.CategoryName() != "auth" {
		t.Errorf("CategoryName = %q, want auth", errNil.CategoryName())
	}
	if !strings.Contains(errNil.Error(), wantMsg) {
		t.Errorf("error = %q, want message containing %q", errNil.Error(), wantMsg)
	}
}

// TS-04-31 (integration): specgen posts PRD comment via Forge.AddComment and records warning on failure
// Verifies: 04-REQ-7.3, 04-REQ-7.4
func TestTS0431_PostsPRDCommentAndWarnsOnFailure(t *testing.T) {
	ctx := context.Background()
	issueRef := issuex.IssueRef{
		Repo:   issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"},
		Number: 42,
	}

	// Success case: PRD comment is posted via Forge.AddComment and populates pkg.CommentURL
	ws := newWorkspace(t)
	a := newAuthor(t, "01", "test_feature")
	mockForge := &mockForgeClient{
		authenticated: true,
		commentURL:    "https://forge/issue/1#note_9",
	}
	opts := newOptions(ws, a)
	opts.Comment = true
	opts.DryRun = false
	opts.Input = toolio.Input{Kind: toolio.KindIssue, Issue: &issueRef}
	opts.Forge = mockForge

	pkg, _, err := GeneratePRD(ctx, opts)
	if err != nil {
		t.Fatalf("GeneratePRD failed: %v", err)
	}
	if pkg == nil {
		t.Fatal("expected non-nil Package")
	}
	if pkg.CommentURL != "https://forge/issue/1#note_9" {
		t.Errorf("CommentURL = %q, want %q", pkg.CommentURL, "https://forge/issue/1#note_9")
	}
	if mockForge.commentCalls != 1 {
		t.Errorf("commentCalls = %d, want 1", mockForge.commentCalls)
	}
	if mockForge.lastRef != issueRef {
		t.Errorf("lastRef = %+v, want %+v", mockForge.lastRef, issueRef)
	}
	if !strings.Contains(mockForge.lastBody, "## Intent") {
		t.Errorf("comment body should contain PRD markdown, got: %s", mockForge.lastBody)
	}

	// Error degraded to warning case: comment failure does not fail the pipeline run
	wsErr := newWorkspace(t)
	aErr := newAuthor(t, "01", "test_feature")
	mockForgeErr := &mockForgeClient{
		authenticated: true,
		commentErr:    errors.New("forbidden"),
	}
	run := toolio.NewRun("spec", "test")
	optsErr := newOptions(wsErr, aErr)
	optsErr.Comment = true
	optsErr.DryRun = false
	optsErr.Input = toolio.Input{Kind: toolio.KindIssue, Issue: &issueRef}
	optsErr.Forge = mockForgeErr
	optsErr.Run = run

	pkgErr, _, err2 := GeneratePRD(ctx, optsErr)
	if err2 != nil {
		t.Fatalf("expected successful Result despite comment error, got: %v", err2)
	}
	if pkgErr == nil {
		t.Fatal("expected non-nil Package")
	}
	if pkgErr.CommentURL != "" {
		t.Errorf("CommentURL = %q, want empty string on failure", pkgErr.CommentURL)
	}
	warnings := run.Warnings()
	if len(warnings) != 1 {
		t.Fatalf("len(warnings) = %d, want 1", len(warnings))
	}
	if !strings.Contains(warnings[0].Message, "the PRD could not be posted") {
		t.Errorf("warning %q should contain 'the PRD could not be posted'", warnings[0].Message)
	}
}

// TS-06-27 / TS-06-28 (integration): the PRD comment is recorded as a
// comment on "<owner>/<repo>#<n>" — ok when posted, ok:false carrying the
// warning code the same site recorded when it was not.
func TestTS06_27_28_PRDCommentIsRecordedAsASideEffect(t *testing.T) {
	ctx := context.Background()
	issueRef := issuex.IssueRef{
		Repo:   issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"},
		Number: 42,
	}
	for _, tc := range []struct {
		name    string
		forge   *mockForgeClient
		wantOK  bool
		wantWrn toolio.WarnCode
	}{
		{"posted", &mockForgeClient{authenticated: true, commentURL: "https://forge/c"}, true, ""},
		{"refused", &mockForgeClient{authenticated: true, commentErr: errors.New("forbidden")}, false, toolio.WarnCommentNotPosted},
	} {
		run := toolio.NewRun("spec", "test")
		opts := newOptions(newWorkspace(t), newAuthor(t, "01", "test_feature"))
		opts.Comment = true
		opts.DryRun = false
		opts.Input = toolio.Input{Kind: toolio.KindIssue, Issue: &issueRef}
		opts.Forge = tc.forge
		opts.Run = run
		if _, _, err := GeneratePRD(ctx, opts); err != nil {
			t.Fatalf("%s: GeneratePRD: %v", tc.name, err)
		}
		se := run.SideEffects()
		if len(se) != 1 || se[0].Action != "comment" || se[0].Target != "acme/widgets#42" ||
			se[0].OK != tc.wantOK || se[0].Warning != tc.wantWrn {
			t.Errorf("%s: SideEffects = %+v", tc.name, se)
		}
	}
}

// TS-06-30 (unit): under --dry-run the PRD comment is never attempted, so
// nothing is recorded.
func TestTS06_30_DryRunPRDCommentRecordsNothing(t *testing.T) {
	issueRef := issuex.IssueRef{
		Repo:   issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"},
		Number: 42,
	}
	run := toolio.NewRun("spec", "test")
	opts := newOptions(newWorkspace(t), newAuthor(t, "01", "test_feature"))
	opts.Comment = true
	opts.DryRun = true
	opts.Input = toolio.Input{Kind: toolio.KindIssue, Issue: &issueRef}
	opts.Forge = &mockForgeClient{authenticated: true, commentURL: "https://forge/c"}
	opts.Run = run
	if _, _, err := GeneratePRD(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if se := run.SideEffects(); len(se) != 0 {
		t.Errorf("dry run recorded %+v", se)
	}
}

func TestSpecgenResultSummary(t *testing.T) {
	rSingle := Result{
		Package: Package{
			SpecDir: ".specs/05_envelope_decidable",
			OpenQuestions: []OpenQuestion{
				{Question: "Q1"},
				{Question: "Q2"},
			},
		},
	}
	wantSingle := "spec: wrote .specs/05_envelope_decidable; 2 open questions"
	if rSingle.Summary() != wantSingle {
		t.Errorf("got %q, want %q", rSingle.Summary(), wantSingle)
	}

	rSplit := Result{
		Split: []ScopeReport{
			{Scope: "scope1", Status: ScopeDone},
			{Scope: "scope2", Status: ScopeDone},
			{Scope: "scope3", Status: ScopeFailed},
		},
		Package: Package{
			OpenQuestions: []OpenQuestion{
				{Question: "Q1"},
			},
		},
	}
	wantSplit := "spec: split 2 of 3 scopes; 1 open question"
	if rSplit.Summary() != wantSplit {
		t.Errorf("got %q, want %q", rSplit.Summary(), wantSplit)
	}
}

// fakeIndex is a tools.Index test double. Tools returns a code_search tool, as
// the real codesearch index does.
type fakeIndex struct{ closed int }

func (f *fakeIndex) Symbols(context.Context, tools.SymbolQuery) (tools.SymbolAnswer, bool, error) {
	return tools.SymbolAnswer{}, false, nil
}
func (f *fakeIndex) Tools() []core.Tool {
	return []core.Tool{{
		Name: "code_search", Description: "ranked search",
		Execute: func(context.Context, json.RawMessage) core.ToolResult { return core.OKResult(map[string]any{}) },
	}}
}
func (f *fakeIndex) Invalidate(string) {}
func (f *fakeIndex) Close() error      { f.closed++; return nil }

// indexedRunner is a Runner whose Config carries idx — the Config the shell
// builds from the same index it hands the tool's Options.
func indexedRunner(t *testing.T, ws *tools.Workspace, p *faux.Provider, idx tools.Index) *agentrun.Runner {
	t.Helper()
	r, err := agentrun.NewRunner(agentrun.Config{
		Model:         faux.Model(),
		Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
		Workspace:     ws,
		Bounds:        agentrun.Bounds{MaxTurns: 8, MaxBudgetUSD: 1, MaxAttempts: 1},
		SessionPrefix: "spec",
		Index:         idx,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// wireTools is the names of the tools the first request offered the model.
func wireTools(t *testing.T, p *faux.Provider) map[string]bool {
	t.Helper()
	reqs := p.Requests()
	if len(reqs) == 0 {
		t.Fatal("no request reached the model")
	}
	got := map[string]bool{}
	for _, w := range reqs[0].Tools {
		got[w.Name] = true
	}
	return got
}

// TS-16-4 (unit): specgen.Options.Index is forwarded to agentrun.Config.Index.
// Run builds the real agentAuthor, so the PRD phase is granted code_search,
// and the model is offered it because the index reached the Runner's Config.
//
// Verifies: 16-REQ-1.5, 16-REQ-2.1
func TestTS16_4_IndexReachesThePRDPhase(t *testing.T) {
	ws := newWorkspace(t)
	idx := &fakeIndex{}
	p := faux.New()
	o := newOptions(ws, nil)
	o.author = nil
	o.Runner = indexedRunner(t, ws, p, idx)
	o.Index = idx

	// The unscripted model ends the PRD phase with no result: the run fails
	// there, which is all this test needs of it.
	_, _ = Run(context.Background(), o)

	got := wireTools(t, p)
	if !got["code_search"] {
		t.Errorf("code_search was not offered to the PRD phase: %v", got)
	}
	for _, n := range agentrun.ReadOnlyFileTools {
		if !got[n] {
			t.Errorf("%s is missing from the PRD phase", n)
		}
	}
}

// 16-REQ-2.1: the architecture phase is granted code_search too.
func TestTS16_4_IndexReachesTheArchitecturePhase(t *testing.T) {
	ws := newWorkspace(t)
	p := faux.New()
	a := &agentAuthor{runner: indexedRunner(t, ws, p, &fakeIndex{}), ws: ws, codeSearch: true}

	_, _, _ = a.WriteArchitecture(context.Background(), architectureRequest{Partial: &afspec.PartialSpec{}})

	if got := wireTools(t, p); !got["code_search"] {
		t.Errorf("code_search was not offered to the architecture phase: %v", got)
	}
}

// 16-REQ-2.2: with no index no phase names code_search.
func TestTS16_4_NilIndexLeavesTheGrantUnchanged(t *testing.T) {
	ws := newWorkspace(t)
	p := faux.New()
	o := newOptions(ws, nil)
	o.author = nil
	o.Runner = indexedRunner(t, ws, p, nil)
	_, _ = Run(context.Background(), o)
	if got := wireTools(t, p); got["code_search"] {
		t.Errorf("the PRD phase offered code_search without an index: %v", got)
	}
}

// The grant is a copy: appending code_search must not grow the shared
// ReadOnlyFileTools slice.
func TestTS16_4_TheSharedReadOnlyListIsNotMutated(t *testing.T) {
	before := len(agentrun.ReadOnlyFileTools)
	got := withCodeSearch(agentrun.ReadOnlyFileTools, true)
	if len(agentrun.ReadOnlyFileTools) != before {
		t.Fatal("ReadOnlyFileTools was modified")
	}
	if got[len(got)-1] != "code_search" || len(got) != before+1 {
		t.Errorf("grant = %v", got)
	}
	if same := withCodeSearch(agentrun.ReadOnlyFileTools, false); len(same) != before {
		t.Errorf("grant without an index = %v", same)
	}
}

// TS-16-22 (unit): the spec pipeline never changes the working tree, so it
// never invalidates the index (16-REQ-4.8).
//
// Verifies: 16-REQ-4.8
func TestTS16_22_SpecgenNeverInvalidatesTheIndex(t *testing.T) {
	ws := newWorkspace(t)
	idx := &indextest.Index{}
	o := newOptions(ws, newAuthor(t, "01", "test_feature"))
	o.Index = idx

	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := idx.InvalidateCalls(); n != 0 {
		t.Errorf("Invalidate was called %d times; spec never changes the tree: %v", n, idx.Events())
	}
}
