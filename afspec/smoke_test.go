package afspec

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// ---------------------------------------------------------------------------
// Smoke tests: end-to-end integration verification (subtask 12.4)
// ---------------------------------------------------------------------------

// TestSmokeLoadRenderSave exercises the full LoadSpec → RenderCombined →
// Save round-trip on ./../testdata/valid_spec, verifying byte-for-byte fidelity
// for all artifact files.
// Test Spec: TS-01-SMOKE-1, TS-01-1, TS-01-35, TS-01-4, TS-01-52
// Requirements: 01-REQ-1.1, 01-REQ-18.1, 01-REQ-2.1, 01-REQ-27.1
func TestSmokeLoadRenderSave(t *testing.T) {
	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	// Verify loaded artifacts are non-nil.
	if spec.Requirements == nil {
		t.Fatal("spec.Requirements is nil after LoadSpec")
	}
	if spec.TestSpec == nil {
		t.Fatal("spec.TestSpec is nil after LoadSpec")
	}
	if spec.Tasks == nil {
		t.Fatal("spec.Tasks is nil after LoadSpec")
	}
	if spec.PRDBody == "" {
		t.Fatal("spec.PRDBody is empty after LoadSpec")
	}

	// Validate the loaded spec.
	result := spec.Validate()
	if !result.Valid {
		t.Fatalf("Validate returned Valid=false; Errors: %v", result.Errors)
	}

	// RenderCombined should produce non-empty Markdown.
	combined := spec.RenderCombined()
	if combined == "" {
		t.Fatal("RenderCombined returned empty string")
	}
	if len(combined) < 100 {
		t.Errorf("RenderCombined output suspiciously short (%d chars)", len(combined))
	}

	// Save to a temp directory and verify round-trip fidelity.
	tmpDir := t.TempDir()
	if err := spec.Save(tmpDir); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Compare each artifact file byte-for-byte with the golden fixture.
	artifacts := []string{"prd.md", "requirements.json", "test_spec.json", "tasks.json"}
	for _, name := range artifacts {
		golden, err := os.ReadFile(filepath.Join("./../testdata/valid_spec", name))
		if err != nil {
			t.Fatalf("cannot read golden %s: %v", name, err)
		}
		actual, err := os.ReadFile(filepath.Join(tmpDir, name))
		if err != nil {
			t.Fatalf("cannot read saved %s: %v", name, err)
		}
		if diff := cmp.Diff(string(golden), string(actual)); diff != "" {
			t.Errorf("%s round-trip mismatch (-golden +actual):\n%s", name, diff)
		}
	}

	// Verify no temp files remain.
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("cannot read tmpDir: %v", err)
	}
	for _, entry := range entries {
		if !slices.Contains(artifacts, entry.Name()) {
			t.Errorf("unexpected file in tmpDir after Save: %s", entry.Name())
		}
	}
}

// TestSmokeBootstrapFinalize exercises the full NewBootstrapSpec → set
// artifacts → Finalize → Validate chain using content from ./../testdata/valid_spec.
// Test Spec: TS-01-SMOKE-3, TS-01-42, TS-01-43
// Requirements: 01-REQ-21.1, 01-REQ-21.2
func TestSmokeBootstrapFinalize(t *testing.T) {
	// Load artifact content from the golden fixture to populate the bootstrap.
	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	bs := NewBootstrapSpec(spec.SpecID, spec.SpecName)
	if bs == nil {
		t.Fatal("NewBootstrapSpec returned nil")
	}
	if bs.SpecID != spec.SpecID {
		t.Errorf("bs.SpecID = %q, want %q", bs.SpecID, spec.SpecID)
	}
	if bs.SpecName != spec.SpecName {
		t.Errorf("bs.SpecName = %q, want %q", bs.SpecName, spec.SpecName)
	}

	// Set all required artifacts.
	bs.Requirements = spec.Requirements
	bs.TestSpec = spec.TestSpec
	bs.Tasks = spec.Tasks
	bs.PRDBody = spec.PRDBody

	// Finalize should succeed with no errors.
	assembled, errs := bs.Finalize()
	if errs != nil {
		t.Fatalf("Finalize returned errors: %v", errs)
	}
	if assembled == nil {
		t.Fatal("Finalize returned nil Spec")
	}

	// The assembled spec should have all artifacts populated.
	if assembled.Requirements == nil {
		t.Error("assembled.Requirements is nil")
	}
	if assembled.TestSpec == nil {
		t.Error("assembled.TestSpec is nil")
	}
	if assembled.Tasks == nil {
		t.Error("assembled.Tasks is nil")
	}
	if assembled.PRDBody == "" {
		t.Error("assembled.PRDBody is empty")
	}

	// The assembled spec should pass validation.
	result := assembled.Validate()
	if !result.Valid {
		t.Errorf("assembled spec Validate returned Valid=false; Errors: %v", result.Errors)
	}
}

// TestSmokeDiscoverAndGraph exercises the full DiscoverSpecs →
// BuildDependencyGraph → TopologicalSort chain using a synthetic workspace.
// Test Spec: TS-01-SMOKE-2, TS-01-23, TS-01-24
// Requirements: 01-REQ-12.1, 01-REQ-13.1, 01-REQ-13.2
func TestSmokeDiscoverAndGraph(t *testing.T) {
	root := t.TempDir()

	// Create two spec directories. Spec 02 depends on spec 01.
	dir01 := filepath.Join(root, "01_alpha")
	writeMinimalPRD(t, dir01, "01", "alpha", "draft")
	writeMinimalTasksJSON(t, dir01, "01", nil)

	dir02 := filepath.Join(root, "02_beta")
	writeMinimalPRD(t, dir02, "02", "beta", "draft")
	writeMinimalTasksJSON(t, dir02, "02", []TaskDependency{
		{DependsOnSpec: "01", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
	})

	// DiscoverSpecs should find both.
	metas, err := DiscoverSpecs(root)
	if err != nil {
		t.Fatalf("DiscoverSpecs returned unexpected error: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("len(metas) = %d, want 2", len(metas))
	}

	// BuildDependencyGraph.
	graph, err := BuildDependencyGraph(metas, root)
	if err != nil {
		t.Fatalf("BuildDependencyGraph returned unexpected error: %v", err)
	}
	if graph == nil {
		t.Fatal("BuildDependencyGraph returned nil graph")
	}
	if len(graph.Edges) < 1 {
		t.Fatalf("graph.Edges is empty, expected at least 1 edge")
	}

	// TopologicalSort should succeed and place 01 before 02.
	order, err := graph.TopologicalSort()
	if err != nil {
		t.Fatalf("TopologicalSort returned error: %v", err)
	}

	idx := func(slice []string, val string) int {
		for i, s := range slice {
			if s == val {
				return i
			}
		}
		return -1
	}

	idx01 := idx(order, "01")
	idx02 := idx(order, "02")
	if idx01 == -1 || idx02 == -1 {
		t.Fatalf("TopologicalSort missing spec IDs; order = %v", order)
	}
	if idx01 >= idx02 {
		t.Errorf("spec '01' (index %d) should appear before '02' (index %d) in topological order; order = %v",
			idx01, idx02, order)
	}
}

// TestSmokeLifecycleAndArchive exercises CreateSpec → Save → Transition
// through draft→active→sealed, then MoveToArchive.
// Test Spec: TS-01-SMOKE-4
// Requirements: 01-REQ-9.1, 01-REQ-11.1
func TestSmokeLifecycleAndArchive(t *testing.T) {
	tmpRoot := t.TempDir()
	specDir := filepath.Join(tmpRoot, "01_lifecycle_test")

	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("failed to create spec dir: %v", err)
	}
	writeSpecFixtures(t, specDir)

	spec, err := LoadSpec(specDir)
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}
	if spec.Status != "draft" {
		t.Fatalf("expected draft status, got %q", spec.Status)
	}

	// draft → active
	active, err := spec.Transition("active", specDir)
	if err != nil {
		t.Fatalf("Transition draft→active failed: %v", err)
	}
	if active.Status != "active" {
		t.Fatalf("expected active status, got %q", active.Status)
	}

	// active → sealed
	sealed, err := active.Transition("sealed", specDir)
	if err != nil {
		t.Fatalf("Transition active→sealed failed: %v", err)
	}
	if sealed.Status != "sealed" {
		t.Fatalf("expected sealed status, got %q", sealed.Status)
	}

	// MoveToArchive
	err = MoveToArchive(specDir, tmpRoot)
	if err != nil {
		t.Fatalf("MoveToArchive failed: %v", err)
	}

	archiveDir := filepath.Join(tmpRoot, "archive", "01_lifecycle_test")
	if _, err := os.Stat(archiveDir); os.IsNotExist(err) {
		t.Error("archive directory does not exist after MoveToArchive")
	}
	if _, err := os.Stat(specDir); !os.IsNotExist(err) {
		t.Error("original spec directory still exists after MoveToArchive")
	}
}

// TestSmokeSubtaskTransitions exercises the full subtask state machine
// chain: pending → queued → in_progress → done, then Save to verify
// persistence.
// Test Spec: TS-01-SMOKE-5
// Requirements: 01-REQ-16.1
func TestSmokeSubtaskTransitions(t *testing.T) {
	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	tasks := spec.Tasks
	subtaskID := tasks.TaskGroups[0].Subtasks[0].Id

	// pending → queued
	tasks, err = tasks.TransitionSubtask(subtaskID, "queued")
	if err != nil {
		t.Fatalf("TransitionSubtask pending→queued failed: %v", err)
	}

	// queued → in_progress
	tasks, err = tasks.TransitionSubtask(subtaskID, "in_progress")
	if err != nil {
		t.Fatalf("TransitionSubtask queued→in_progress failed: %v", err)
	}

	// in_progress → done
	tasks, err = tasks.TransitionSubtask(subtaskID, "done")
	if err != nil {
		t.Fatalf("TransitionSubtask in_progress→done failed: %v", err)
	}

	// Verify the subtask is now done.
	for _, g := range tasks.TaskGroups {
		for _, s := range g.Subtasks {
			if s.Id == subtaskID {
				if s.State != SubtaskStateDone {
					t.Errorf("subtask %s state = %q, want %q", subtaskID, s.State, SubtaskStateDone)
				}
			}
		}
	}

	// Save with updated tasks to a temp dir.
	tmpDir := t.TempDir()
	updatedSpec := *spec
	updatedSpec.Tasks = tasks
	if err := updatedSpec.Save(tmpDir); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Reload and verify the state persisted.
	reloaded, err := LoadSpec(tmpDir)
	if err != nil {
		t.Fatalf("LoadSpec after Save failed: %v", err)
	}
	for _, g := range reloaded.Tasks.TaskGroups {
		for _, s := range g.Subtasks {
			if s.Id == subtaskID {
				if s.State != SubtaskStateDone {
					t.Errorf("reloaded subtask %s state = %q, want %q", subtaskID, s.State, SubtaskStateDone)
				}
			}
		}
	}
}

// TestSmokeRenderScoped exercises the full LoadSpec → RenderIndividualScoped
// chain, verifying scoped content filtering.
// Test Spec: TS-01-SMOKE-6, TS-01-37
// Requirements: 01-REQ-19.1
func TestSmokeRenderScoped(t *testing.T) {
	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	// RenderIndividualScoped with the existing group ID.
	result := spec.RenderIndividualScoped(1)
	if result == nil {
		t.Fatal("RenderIndividualScoped returned nil")
	}

	// Should have keys for prd, requirements, test_spec, tasks.
	for _, key := range []string{"prd", "requirements", "test_spec", "tasks"} {
		if _, ok := result[key]; !ok {
			t.Errorf("RenderIndividualScoped missing key %q", key)
		}
		if result[key] == "" {
			t.Errorf("RenderIndividualScoped[%q] is empty", key)
		}
	}

	// The requirements entry should contain the referenced requirement ID.
	assertContains(t, result["requirements"], "01-REQ-1", "scoped requirements")

	// The tasks entry should contain the group's subtask detail.
	assertContains(t, result["tasks"], "1.1", "scoped tasks")
}

// TestSmokeValidateCrossSpec exercises ValidateCrossSpec with compatible specs.
// Test Spec: TS-01-14
// Requirements: 01-REQ-7.1
func TestSmokeValidateCrossSpec(t *testing.T) {
	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	graph := &DependencyGraph{Edges: []DependencyEdge{}}

	result := ValidateCrossSpec([]*Spec{spec}, graph)
	if !result.Valid {
		t.Errorf("ValidateCrossSpec with single spec returned Valid=false; Errors: %v", result.Errors)
	}
}

// TestSmokeComputeIntentHash exercises ComputeIntentHash on a real PRD body.
// Test Spec: TS-01-47
// Requirements: 01-REQ-23.1
func TestSmokeComputeIntentHash(t *testing.T) {
	spec, err := LoadSpec("./../testdata/valid_spec")
	if err != nil {
		t.Fatalf("LoadSpec failed: %v", err)
	}

	hash, err := ComputeIntentHash(spec.PRDBody)
	if err != nil {
		t.Fatalf("ComputeIntentHash failed: %v", err)
	}
	if len(hash) != 64 {
		t.Errorf("hash length = %d, want 64 hex chars", len(hash))
	}

	// Same input should produce same hash (deterministic).
	hash2, err := ComputeIntentHash(spec.PRDBody)
	if err != nil {
		t.Fatalf("ComputeIntentHash second call failed: %v", err)
	}
	if hash != hash2 {
		t.Errorf("non-deterministic: hash1=%q, hash2=%q", hash, hash2)
	}
}
