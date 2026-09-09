package afspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// specRoot builds a spec root containing the named fixtures, each copied into
// a properly named {NN}_{snake_case_name} directory.
func specRoot(t *testing.T, fixtures map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for dirName, fixture := range fixtures {
		dst := filepath.Join(root, dirName)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(fixture)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			data, err := os.ReadFile(filepath.Join(fixture, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func TestIsSpecDirName(t *testing.T) {
	valid := []string{"01_feature", "99_a", "123_long_feature_name", "01_a1_b2"}
	for _, name := range valid {
		if !IsSpecDirName(name) {
			t.Errorf("%q should be a spec directory name", name)
		}
	}
	invalid := []string{"1_feature", "01feature", "01_Feature", "01_", "_feature", "abc_feature", "01-feature"}
	for _, name := range invalid {
		if IsSpecDirName(name) {
			t.Errorf("%q should not be a spec directory name", name)
		}
	}
}

func TestParseSpecDirName(t *testing.T) {
	prefix, name, err := ParseSpecDirName("07_agent_mode")
	if err != nil {
		t.Fatalf("ParseSpecDirName = %v", err)
	}
	if prefix != "07" || name != "agent_mode" {
		t.Errorf("= %q/%q; want 07/agent_mode", prefix, name)
	}
	if _, _, err := ParseSpecDirName("not-a-spec"); err == nil {
		t.Error("a malformed name was accepted")
	}
}

func TestDiscoverSpecs(t *testing.T) {
	root := specRoot(t, map[string]string{
		"01_test_feature":  fixtureValidSpec,
		"02_draft_feature": "../testdata/draft_spec",
		"not_a_spec_dir":   fixtureValidSpec,
	})

	metas, err := DiscoverSpecs(root)
	if err != nil {
		t.Fatalf("DiscoverSpecs = %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("discovered %d specs; want the two properly named ones", len(metas))
	}
	if metas[0].SpecID != "01" || metas[1].SpecID != "02" {
		t.Errorf("specs are not in prefix order: %v", metas)
	}
	if metas[0].Status != "draft" || metas[0].SpecName != "test_feature" {
		t.Errorf("metadata for spec 01 is wrong: %+v", metas[0])
	}
}

func TestBuildDependencyGraph(t *testing.T) {
	root := specRoot(t, map[string]string{
		"01_test_feature":  fixtureValidSpec,
		"02_draft_feature": "../testdata/draft_spec",
	})

	metas, err := DiscoverSpecs(root)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := BuildDependencyGraph(metas, root)
	if err != nil {
		t.Fatalf("BuildDependencyGraph = %v", err)
	}
	if len(graph.Edges) != 1 {
		t.Fatalf("edges = %v; want the single 02 → 01 dependency", graph.Edges)
	}
	edge := graph.Edges[0]
	if edge.FromSpec != "02" || edge.ToSpec != "01" {
		t.Errorf("edge = %+v; want 02 depending on 01", edge)
	}
	if edge.Reason == "" {
		t.Error("the edge carries no reason")
	}

	if deps := graph.Dependencies("02"); len(deps) != 1 {
		t.Errorf("Dependencies(02) = %v; want one", deps)
	}
	if dependents := graph.Dependents("01"); len(dependents) != 1 {
		t.Errorf("Dependents(01) = %v; want one", dependents)
	}

	order, err := graph.TopologicalSort()
	if err != nil {
		t.Fatalf("TopologicalSort = %v", err)
	}
	if len(order) != 2 || order[0] != "01" || order[1] != "02" {
		t.Errorf("order = %v; want the upstream spec first", order)
	}
}

func TestBuildDependencyGraphReportsUnknownSpecs(t *testing.T) {
	root := specRoot(t, map[string]string{"02_draft_feature": "../testdata/draft_spec"})
	metas, err := DiscoverSpecs(root)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := BuildDependencyGraph(metas, root)
	if err == nil {
		t.Fatal("a dependency on a spec that is not in the root produced no error")
	}
	if !strings.Contains(err.Error(), "01") {
		t.Errorf("the error does not name the missing spec: %v", err)
	}
	if graph == nil {
		t.Error("a partial graph should still be returned")
	}
}

func TestTopologicalSortDetectsCycles(t *testing.T) {
	graph := &DependencyGraph{Edges: []DependencyEdge{
		{FromSpec: "01", ToSpec: "02"},
		{FromSpec: "02", ToSpec: "01"},
	}}
	if _, err := graph.TopologicalSort(); err == nil {
		t.Fatal("a two-spec cycle was not detected")
	}
}

func TestLoadDependentInterfaces(t *testing.T) {
	root := specRoot(t, map[string]string{
		"01_test_feature":  fixtureValidSpec,
		"02_draft_feature": "../testdata/draft_spec",
	})

	summaries := LoadDependentInterfaces("02", root)
	if len(summaries) != 1 {
		t.Fatalf("summaries = %v; want one for the upstream spec", summaries)
	}
	if _, ok := summaries[0]["glossary"]; !ok {
		t.Errorf("the summary carries no glossary: %v", summaries[0])
	}

	if got := LoadDependentInterfaces("01", root); len(got) != 0 {
		t.Errorf("a spec with no dependencies returned %v", got)
	}
	if got := LoadDependentInterfaces("02", filepath.Join(root, "nope")); got == nil || len(got) != 0 {
		t.Errorf("an unreadable root should degrade to an empty slice, got %v", got)
	}
}

func TestValidateCrossSpec(t *testing.T) {
	root := specRoot(t, map[string]string{
		"01_test_feature":  fixtureValidSpec,
		"02_draft_feature": "../testdata/draft_spec",
	})
	metas, err := DiscoverSpecs(root)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := BuildDependencyGraph(metas, root)
	if err != nil {
		t.Fatal(err)
	}
	specs := []*Spec{loadFixture(t, metas[0].Dir), loadFixture(t, metas[1].Dir)}

	result := ValidateCrossSpec(specs, graph)
	if !result.Valid {
		t.Fatalf("cross-spec validation failed on two consistent specs: %v", result.Errors)
	}
}

func TestValidateCrossSpecRejectsAnUnknownDependency(t *testing.T) {
	spec := loadFixture(t, "../testdata/draft_spec")
	result := ValidateCrossSpec([]*Spec{spec}, &DependencyGraph{})
	if result.Valid {
		t.Fatal("a dependency on a spec outside the root was accepted")
	}
	if result.Errors[0].Check != "cross_spec_unknown_dependency" {
		t.Errorf("check = %q", result.Errors[0].Check)
	}
}

func TestValidateCrossSpecWarnsOnGlossaryConflicts(t *testing.T) {
	a := loadFixture(t, fixtureValidSpec)
	b := loadFixture(t, fixtureValidSpec)
	b.SpecID = "02"
	b.Requirements.SpecId = "02"
	b.Requirements.Glossary["spec"] = "Something else entirely."
	b.Tasks.Dependencies = nil

	result := ValidateCrossSpec([]*Spec{a, b}, &DependencyGraph{})
	if !result.Valid {
		t.Fatalf("a glossary conflict must warn, not fail: %v", result.Errors)
	}
	if !hasWarningContaining(result, "defined differently") {
		t.Errorf("no glossary conflict warning: %v", result.Warnings)
	}
}

func TestValidateCrossSpecRequiresASharedActor(t *testing.T) {
	upstream := loadFixture(t, fixtureValidSpec)
	downstream := loadFixture(t, "../testdata/draft_spec")
	// Rename every actor so that the two specs share none.
	for i := range downstream.Requirements.ExecutionPaths {
		for j := range downstream.Requirements.ExecutionPaths[i].Steps {
			downstream.Requirements.ExecutionPaths[i].Steps[j].Actor = "unrelated actor"
		}
	}

	graph := &DependencyGraph{Edges: []DependencyEdge{{FromSpec: "02", ToSpec: "01"}}}
	result := ValidateCrossSpec([]*Spec{upstream, downstream}, graph)
	if result.Valid {
		t.Fatal("a dependency edge with no shared actor was accepted")
	}
	if !hasCheck(result, "cross_spec_actor") {
		t.Errorf("checks = %v; want cross_spec_actor", errorChecks(result))
	}
}

func TestValidateCrossSpecDetectsCycles(t *testing.T) {
	a := loadFixture(t, fixtureValidSpec)
	b := loadFixture(t, fixtureValidSpec)
	b.SpecID = "02"
	a.Tasks.Dependencies = []Dependency{{Spec: "02", Reason: "x"}}
	b.Tasks.Dependencies = []Dependency{{Spec: "01", Reason: "y"}}

	graph := &DependencyGraph{Edges: []DependencyEdge{
		{FromSpec: "01", ToSpec: "02"},
		{FromSpec: "02", ToSpec: "01"},
	}}
	result := ValidateCrossSpec([]*Spec{a, b}, graph)
	if !hasCheck(result, "cross_spec_cycle") {
		t.Errorf("checks = %v; want cross_spec_cycle", errorChecks(result))
	}
}
