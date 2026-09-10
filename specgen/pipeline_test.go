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

	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
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

	steps       []afspec.GenerationStep
	prdRequests []prdRequest
}

func (a *scriptedAuthor) WritePRD(_ context.Context, req prdRequest) (PRD, agentrun.Result, error) {
	a.prdRequests = append(a.prdRequests, req)
	res := agentrun.Result{Name: "prd", Turns: 3}
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
	if len(warnings) == 0 || !strings.Contains(strings.Join(warnings, "\n"), "architecture.md") {
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
