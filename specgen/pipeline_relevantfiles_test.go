package specgen

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-13-26 (integration): On a split, each scope's generation phases receive
// only that scope's relevant_files.
func TestTS_13_26_SplitScopesReceiveOwnRelevantFiles(t *testing.T) {
	ws := newWorkspace(t)
	a := splitAuthor(t)

	// Scope 1 (widget_core) gets relevant_files from the initial PRD.
	a.prd.RelevantFiles = []RelevantFile{{Path: "a.go", Why: "scope 1 file"}}

	// Scope 2 (widget_github) gets its own relevant_files.
	scope2PRD := a.prd
	scope2PRD.SpecName = "widget_github"
	scope2PRD.Title = "Scope widget_github"
	scope2PRD.RecommendedSplit = nil
	scope2PRD.RelevantFiles = []RelevantFile{{Path: "b.go", Why: "scope 2 file"}}
	a.followOn["widget_github"] = scope2PRD

	// Scope 3 (widget_adopt) gets its own relevant_files.
	scope3PRD := a.prd
	scope3PRD.SpecName = "widget_adopt"
	scope3PRD.Title = "Scope widget_adopt"
	scope3PRD.RecommendedSplit = nil
	scope3PRD.RelevantFiles = []RelevantFile{{Path: "c.go", Why: "scope 3 file"}}
	a.followOn["widget_adopt"] = scope3PRD

	// Record artifact requests to verify relevant_files passed to generation phases.
	var artifactReqs []artifactRequest
	origGenerate := a.GenerateArtifact
	_ = origGenerate // suppress unused warning

	// We need to capture the artifact requests. The scriptedAuthor already
	// records steps but not the full request. We'll wrap it.
	wrapper := &relevantFilesCapture{inner: a}

	got, err := Run(context.Background(), fileOptions(ws, wrapper))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_ = got

	// Check that scope 1's generation phases got scope 1's relevant_files.
	artifactReqs = wrapper.artifactReqs
	scope1Reqs := filterArtifactReqs(artifactReqs, "widget_core")
	for _, req := range scope1Reqs {
		if len(req.RelevantFiles) != 1 || req.RelevantFiles[0].Path != "a.go" {
			t.Errorf("scope 1 (%s) got RelevantFiles = %+v, want [{a.go}]", req.Step, req.RelevantFiles)
		}
	}

	// Check that scope 2's generation phases got scope 2's relevant_files.
	scope2Reqs := filterArtifactReqs(artifactReqs, "widget_github")
	for _, req := range scope2Reqs {
		if len(req.RelevantFiles) != 1 || req.RelevantFiles[0].Path != "b.go" {
			t.Errorf("scope 2 (%s) got RelevantFiles = %+v, want [{b.go}]", req.Step, req.RelevantFiles)
		}
	}

	// Check that scope 3's generation phases got scope 3's relevant_files.
	scope3Reqs := filterArtifactReqs(artifactReqs, "widget_adopt")
	for _, req := range scope3Reqs {
		if len(req.RelevantFiles) != 1 || req.RelevantFiles[0].Path != "c.go" {
			t.Errorf("scope 3 (%s) got RelevantFiles = %+v, want [{c.go}]", req.Step, req.RelevantFiles)
		}
	}
}

// relevantFilesCapture wraps a scriptedAuthor to capture artifact requests.
type relevantFilesCapture struct {
	inner        *scriptedAuthor
	artifactReqs []artifactRequest
	archReqs     []architectureRequest
}

func (r *relevantFilesCapture) WritePRD(ctx context.Context, req prdRequest) (PRD, agentrun.Result, error) {
	return r.inner.WritePRD(ctx, req)
}

func (r *relevantFilesCapture) GenerateArtifact(ctx context.Context, req artifactRequest) (map[string]any, agentrun.Result, error) {
	r.artifactReqs = append(r.artifactReqs, req)
	return r.inner.GenerateArtifact(ctx, req)
}

func (r *relevantFilesCapture) WriteArchitecture(ctx context.Context, req architectureRequest) (string, agentrun.Result, error) {
	r.archReqs = append(r.archReqs, req)
	return r.inner.WriteArchitecture(ctx, req)
}

func filterArtifactReqs(reqs []artifactRequest, specName string) []artifactRequest {
	var out []artifactRequest
	for _, r := range reqs {
		if r.SpecName == specName {
			out = append(out, r)
		}
	}
	return out
}

// TS-13-27 (unit): prdRequest does not carry relevant_files from a previous scope.
func TestTS_13_27_PrdRequestHasNoRelevantFiles(t *testing.T) {
	typ := reflect.TypeOf(prdRequest{})
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Name == "RelevantFiles" {
			t.Fatal("prdRequest must not have a RelevantFiles field")
		}
	}
	// Also check splitContext.
	scTyp := reflect.TypeOf(splitContext{})
	for i := 0; i < scTyp.NumField(); i++ {
		if scTyp.Field(i).Name == "RelevantFiles" {
			t.Fatal("splitContext must not carry RelevantFiles across scopes")
		}
	}
}

// TS-13-28 (unit): Package carries RelevantFiles with correct JSON tag and trust classification.
func TestTS_13_28_PackageRelevantFilesJSONTag(t *testing.T) {
	// When populated, the JSON contains "relevant_files".
	pkg := Package{
		RelevantFiles: []RelevantFile{{Path: "a.go", Why: "reason"}},
	}
	b, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"relevant_files"`) {
		t.Errorf("JSON should contain 'relevant_files' key, got %s", string(b))
	}

	// When nil, the key is absent (omitempty).
	pkg2 := Package{}
	b2, err := json.Marshal(pkg2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b2), `"relevant_files"`) {
		t.Errorf("JSON should not contain 'relevant_files' key when nil, got %s", string(b2))
	}

	// Check the trust tag.
	typ := reflect.TypeOf(Package{})
	field, ok := typ.FieldByName("RelevantFiles")
	if !ok {
		t.Fatal("Package has no RelevantFiles field")
	}
	trustTag := field.Tag.Get("trust")
	if trustTag != "model" {
		t.Errorf("trust tag = %q, want %q", trustTag, "model")
	}
}

// TS-13-29 (integration): On a split, each Package in FollowOnSpecs carries
// its own scope's relevant_files.
func TestTS_13_29_SplitPackagesCarryOwnRelevantFiles(t *testing.T) {
	ws := newWorkspace(t)
	a := splitAuthor(t)

	// Scope 1 (widget_core) gets relevant_files from the initial PRD.
	a.prd.RelevantFiles = []RelevantFile{{Path: "a.go", Why: "scope 1 file"}}

	// Scope 2 (widget_github) gets its own relevant_files.
	scope2PRD := a.prd
	scope2PRD.SpecName = "widget_github"
	scope2PRD.Title = "Scope widget_github"
	scope2PRD.RecommendedSplit = nil
	scope2PRD.RelevantFiles = []RelevantFile{{Path: "b.go", Why: "scope 2 file"}}
	a.followOn["widget_github"] = scope2PRD

	// Scope 3 (widget_adopt) gets its own relevant_files.
	scope3PRD := a.prd
	scope3PRD.SpecName = "widget_adopt"
	scope3PRD.Title = "Scope widget_adopt"
	scope3PRD.RecommendedSplit = nil
	scope3PRD.RelevantFiles = []RelevantFile{{Path: "c.go", Why: "scope 3 file"}}
	a.followOn["widget_adopt"] = scope3PRD

	got, err := Run(context.Background(), fileOptions(ws, a))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// result.Package is scope 1.
	if len(got.RelevantFiles) != 1 || got.RelevantFiles[0].Path != "a.go" {
		t.Errorf("scope 1 Package.RelevantFiles = %+v, want [{a.go}]", got.RelevantFiles)
	}

	// FollowOnSpecs[0] is scope 2.
	if len(got.FollowOnSpecs) < 1 {
		t.Fatalf("expected at least 1 FollowOnSpec, got %d", len(got.FollowOnSpecs))
	}
	if len(got.FollowOnSpecs[0].RelevantFiles) != 1 || got.FollowOnSpecs[0].RelevantFiles[0].Path != "b.go" {
		t.Errorf("scope 2 Package.RelevantFiles = %+v, want [{b.go}]", got.FollowOnSpecs[0].RelevantFiles)
	}

	// FollowOnSpecs[1] is scope 3.
	if len(got.FollowOnSpecs) < 2 {
		t.Fatalf("expected at least 2 FollowOnSpecs, got %d", len(got.FollowOnSpecs))
	}
	if len(got.FollowOnSpecs[1].RelevantFiles) != 1 || got.FollowOnSpecs[1].RelevantFiles[0].Path != "c.go" {
		t.Errorf("scope 3 Package.RelevantFiles = %+v, want [{c.go}]", got.FollowOnSpecs[1].RelevantFiles)
	}
}

// TS-13-30 (unit): relevant_files is not persisted inside the spec package directory.
func TestTS_13_30_RelevantFilesNotPersistedInSpecDir(t *testing.T) {
	ws := newWorkspace(t)
	a := newAuthor(t, "01", "test_feature")
	a.prd.RelevantFiles = []RelevantFile{{Path: "a.go", Why: "reason"}}

	got, err := Run(context.Background(), newOptions(ws, a))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	dir := filepath.Join(ws.Root, got.SpecDir)
	for _, name := range []string{"prd.md", "requirements.json", "test_spec.json", "tasks.json"} {
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(string(content), "relevant_files") {
			t.Errorf("%s contains 'relevant_files'; it must not be persisted in the spec package", name)
		}
	}
}

// TS-13-31 (integration): A resumed split run emits a low-severity warning
// with code relevant_files_unavailable.
func TestTS_13_31_ResumedSplitEmitsRelevantFilesUnavailableWarning(t *testing.T) {
	ws := newWorkspace(t)

	// First run: write scope 1, fail on scope 2 to leave a plan.
	a := splitAuthor(t)
	a.prd.RelevantFiles = []RelevantFile{{Path: "a.go", Why: "scope 1 file"}}
	a.followOnErr["widget_github"] = errBudget

	_, err := Run(context.Background(), fileOptions(ws, a))
	if err == nil {
		t.Fatal("expected an error from the first run")
	}

	// Second run: resume from the plan. The PRD phase does not run, so
	// relevant_files are unavailable.
	b := splitAuthor(t)
	run := toolio.NewRun("spec", "test")
	o := fileOptions(ws, b)
	o.Run = run

	_, err = Run(context.Background(), o)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}

	// Check for the warning.
	warnings := run.Warnings()
	var found bool
	for _, w := range warnings {
		if w.Code == toolio.WarnRelevantFilesUnavailable {
			found = true
			if w.Severity != "low" {
				t.Errorf("severity = %q, want %q", w.Severity, "low")
			}
		}
	}
	if !found {
		t.Errorf("expected a warning with code %q; warnings = %+v",
			toolio.WarnRelevantFilesUnavailable, warnings)
	}
}

var errBudget = failf("prd", "budget", "budget exceeded")
