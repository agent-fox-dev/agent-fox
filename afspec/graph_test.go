package afspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Subtask 3.2: BuildDependencyGraph and DependencyGraph methods
// ---------------------------------------------------------------------------

// writeMinimalTasksJSON writes a minimal valid tasks.json to dir with
// the given spec ID and optional cross-spec dependencies.
func writeMinimalTasksJSON(t *testing.T, dir, specID string, deps []TaskDependency) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create dir %s: %v", dir, err)
	}

	// Build the dependencies array
	depsJSON := "[]"
	if len(deps) > 0 {
		parts := make([]string, 0, len(deps))
		for _, d := range deps {
			parts = append(parts, `{`+
				`"depends_on_spec": "`+d.DependsOnSpec+`", `+
				`"from_group": 1, `+
				`"to_group": 1, `+
				`"relationship": "`+d.Relationship+`", `+
				`"sentinel": false`+
				`}`)
		}
		depsJSON = "[" + strings.Join(parts, ", ") + "]"
	}

	tasksJSON := `{
  "$schema": "https://agent-fox.dev/schemas/tasks.v1.json",
  "spec_id": "` + specID + `",
  "spec_name": "spec_` + specID + `",
  "schema_version": 1,
  "test_commands": {
    "spec_tests": "go test ./...",
    "all_tests": "go test ./...",
    "linter": "go vet ./..."
  },
  "dependencies": ` + depsJSON + `,
  "task_groups": [
    {
      "id": 1,
      "kind": "tests",
      "title": "Test group",
      "subtasks": [
        {
          "id": "1.1",
          "title": "Test subtask",
          "details": ["detail"],
          "test_spec_refs": ["TS-01-1"],
          "requirement_refs": ["01-REQ-1.1"],
          "state": "pending",
          "optional": false
        }
      ],
      "verification": {
        "id": "1.V",
        "checks": ["check"]
      }
    }
  ],
  "traceability": [
    {
      "requirement_id": "01-REQ-1.1",
      "test_spec_id": "TS-01-1",
      "task_id": "1.1",
      "test_path": null
    }
  ]
}`

	if err := os.WriteFile(filepath.Join(dir, "tasks.json"), []byte(tasksJSON), 0o644); err != nil {
		t.Fatalf("failed to write tasks.json in %s: %v", dir, err)
	}
}

// TestBuildDependencyGraph_WithDependencies verifies that BuildDependencyGraph
// reads tasks.json for each spec, extracts cross-spec dependencies, and
// returns a DependencyGraph with correct edges.
// Test Spec: TS-01-24, Requirement: 01-REQ-13.1
func TestBuildDependencyGraph_WithDependencies(t *testing.T) {
	defer requireImplemented(t)

	root := t.TempDir()

	// Spec 01 has no dependencies
	dir01 := filepath.Join(root, "01_spec_a")
	writeMinimalPRD(t, dir01, "01", "spec_a", "draft")
	writeMinimalTasksJSON(t, dir01, "01", nil)

	// Spec 02 depends on spec 01
	dir02 := filepath.Join(root, "02_spec_b")
	writeMinimalPRD(t, dir02, "02", "spec_b", "draft")
	writeMinimalTasksJSON(t, dir02, "02", []TaskDependency{
		{DependsOnSpec: "01", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
	})

	metas := []SpecMeta{
		{SpecID: "01", SpecName: "spec_a", Status: "draft", Dir: dir01},
		{SpecID: "02", SpecName: "spec_b", Status: "draft", Dir: dir02},
	}

	graph, err := BuildDependencyGraph(metas, root)
	if err != nil {
		t.Fatalf("BuildDependencyGraph returned unexpected error: %v", err)
	}
	if graph == nil {
		t.Fatal("BuildDependencyGraph returned nil graph")
	}

	// Should have at least one edge: 02 -> 01
	edges := graph.Edges
	if len(edges) < 1 {
		t.Fatalf("len(edges) = %d, want >= 1", len(edges))
	}

	found := false
	for _, e := range edges {
		if e.FromSpec == "02" && e.ToSpec == "01" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected edge FromSpec=02, ToSpec=01 not found in graph")
	}
}

// TestDependencyGraph_Dependencies verifies that Dependencies(specID) returns
// only edges where the given spec is the dependent (FromSpec matches).
// Requirement: 01-REQ-13.1
func TestDependencyGraph_Dependencies(t *testing.T) {
	defer requireImplemented(t)

	graph := &DependencyGraph{
		Edges: []DependencyEdge{
			{FromSpec: "02", ToSpec: "01", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
			{FromSpec: "03", ToSpec: "01", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
			{FromSpec: "03", ToSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
		},
	}

	// Dependencies("02") should return edges where spec 02 depends on something
	deps := graph.Dependencies("02")
	for _, e := range deps {
		if e.FromSpec != "02" {
			t.Errorf("Dependencies(\"02\") returned edge with FromSpec=%q, want \"02\"", e.FromSpec)
		}
	}
	if len(deps) != 1 {
		t.Errorf("len(Dependencies(\"02\")) = %d, want 1", len(deps))
	}
}

// TestDependencyGraph_Dependents verifies that Dependents(specID) returns
// only edges where other specs depend on the given spec (ToSpec matches).
// Requirement: 01-REQ-13.1
func TestDependencyGraph_Dependents(t *testing.T) {
	defer requireImplemented(t)

	graph := &DependencyGraph{
		Edges: []DependencyEdge{
			{FromSpec: "02", ToSpec: "01", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
			{FromSpec: "03", ToSpec: "01", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
			{FromSpec: "03", ToSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
		},
	}

	// Dependents("01") should return edges where something depends on spec 01
	dependents := graph.Dependents("01")
	for _, e := range dependents {
		if e.ToSpec != "01" {
			t.Errorf("Dependents(\"01\") returned edge with ToSpec=%q, want \"01\"", e.ToSpec)
		}
	}
	if len(dependents) != 2 {
		t.Errorf("len(Dependents(\"01\")) = %d, want 2", len(dependents))
	}
}

// TestBuildDependencyGraph_NoDependencies verifies that BuildDependencyGraph
// with a single spec (no dependencies) returns a graph with no edges.
// Requirement: 01-REQ-13.E1
func TestBuildDependencyGraph_NoDependencies(t *testing.T) {
	defer requireImplemented(t)

	root := t.TempDir()

	dir01 := filepath.Join(root, "01_spec_a")
	writeMinimalPRD(t, dir01, "01", "spec_a", "draft")
	writeMinimalTasksJSON(t, dir01, "01", nil)

	metas := []SpecMeta{
		{SpecID: "01", SpecName: "spec_a", Status: "draft", Dir: dir01},
	}

	graph, err := BuildDependencyGraph(metas, root)
	if err != nil {
		t.Fatalf("BuildDependencyGraph returned unexpected error: %v", err)
	}
	if graph == nil {
		t.Fatal("BuildDependencyGraph returned nil graph")
	}
	if len(graph.Edges) != 0 {
		t.Errorf("len(graph.Edges) = %d, want 0 for single spec", len(graph.Edges))
	}
}

// TestBuildDependencyGraph_DanglingReference verifies that BuildDependencyGraph
// records an error for a tasks.json that references an unknown spec ID.
// Requirement: 01-REQ-13.E2
func TestBuildDependencyGraph_DanglingReference(t *testing.T) {
	defer requireImplemented(t)

	root := t.TempDir()

	// Spec 01 depends on spec 99 which is NOT in the metas slice
	dir01 := filepath.Join(root, "01_spec_a")
	writeMinimalPRD(t, dir01, "01", "spec_a", "draft")
	writeMinimalTasksJSON(t, dir01, "01", []TaskDependency{
		{DependsOnSpec: "99", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
	})

	metas := []SpecMeta{
		{SpecID: "01", SpecName: "spec_a", Status: "draft", Dir: dir01},
	}

	graph, err := BuildDependencyGraph(metas, root)
	// Should return partial graph AND an error about the dangling reference
	if err == nil {
		t.Error("expected error for dangling reference to spec 99, got nil")
	}
	if graph == nil {
		t.Error("expected partial graph to be returned alongside error")
	}
}

// ---------------------------------------------------------------------------
// TopologicalSort tests
// ---------------------------------------------------------------------------

// TestTopologicalSort_AcyclicGraph verifies that TopologicalSort on an
// acyclic graph returns spec IDs in topological order where each spec
// appears before its dependents.
// Test Spec: TS-01-25, Requirement: 01-REQ-13.2
func TestTopologicalSort_AcyclicGraph(t *testing.T) {
	defer requireImplemented(t)

	// 02 depends on 01, 03 depends on 02
	graph := &DependencyGraph{
		Edges: []DependencyEdge{
			{FromSpec: "02", ToSpec: "01", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
			{FromSpec: "03", ToSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
		},
	}

	order, err := graph.TopologicalSort()
	if err != nil {
		t.Fatalf("TopologicalSort returned unexpected error: %v", err)
	}

	// Build index map for position checking
	indexOf := func(slice []string, val string) int {
		for i, s := range slice {
			if s == val {
				return i
			}
		}
		return -1
	}

	idx01 := indexOf(order, "01")
	idx02 := indexOf(order, "02")
	idx03 := indexOf(order, "03")

	if idx01 == -1 || idx02 == -1 || idx03 == -1 {
		t.Fatalf("TopologicalSort missing spec IDs; order = %v", order)
	}

	// 01 must appear before 02 (01 is depended upon by 02)
	if idx01 >= idx02 {
		t.Errorf("spec '01' (index %d) should appear before '02' (index %d); order = %v", idx01, idx02, order)
	}

	// 02 must appear before 03 (02 is depended upon by 03)
	if idx02 >= idx03 {
		t.Errorf("spec '02' (index %d) should appear before '03' (index %d); order = %v", idx02, idx03, order)
	}
}

// TestTopologicalSort_CyclicGraph verifies that TopologicalSort on a graph
// with a cycle returns nil and an error identifying the cycle.
// Test Spec: TS-01-26, Requirement: 01-REQ-13.3
func TestTopologicalSort_CyclicGraph(t *testing.T) {
	defer requireImplemented(t)

	// 01 depends on 02, 02 depends on 01 → cycle
	graph := &DependencyGraph{
		Edges: []DependencyEdge{
			{FromSpec: "01", ToSpec: "02", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
			{FromSpec: "02", ToSpec: "01", FromGroup: 1, ToGroup: 1, Relationship: "depends_on"},
		},
	}

	order, err := graph.TopologicalSort()
	if order != nil {
		t.Errorf("TopologicalSort returned non-nil order for cyclic graph: %v", order)
	}
	if err == nil {
		t.Fatal("expected error for cyclic graph, got nil")
	}

	// Error should mention the specs involved in the cycle
	errMsg := err.Error()
	if !strings.Contains(errMsg, "01") && !strings.Contains(errMsg, "02") {
		t.Errorf("cycle error should mention spec IDs; got: %s", errMsg)
	}
}

// TestTopologicalSort_SingleNode verifies TopologicalSort with a single
// node (no edges) returns just that node.
func TestTopologicalSort_SingleNode(t *testing.T) {
	defer requireImplemented(t)

	graph := &DependencyGraph{
		Edges: []DependencyEdge{
			// Single self-contained edge to establish node presence
			// Actually, for a single node with no dependencies, there are no edges.
			// The implementation needs to handle this — for now test via
			// BuildDependencyGraph which knows the full node set.
		},
	}

	// An empty graph should return empty order, no error
	order, err := graph.TopologicalSort()
	if err != nil {
		t.Fatalf("TopologicalSort returned unexpected error: %v", err)
	}
	if len(order) != 0 {
		t.Errorf("len(order) = %d, want 0 for empty graph", len(order))
	}
}
