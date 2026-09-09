package afspec

import "testing"

func TestAddRequirementRejectsDuplicates(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	req := *spec.Requirements

	added, err := AddRequirement(req, Requirement{
		Id:       "01-REQ-3",
		Title:    "Third",
		Criteria: []Criterion{UbiquitousCriterion("01-REQ-3.1", "loader", "do a thing")},
	})
	if err != nil {
		t.Fatalf("AddRequirement = %v", err)
	}
	if len(added.Requirements) != 3 || len(req.Requirements) != 2 {
		t.Errorf("added=%d original=%d; the original must not change", len(added.Requirements), len(req.Requirements))
	}

	if _, err := AddRequirement(req, Requirement{Id: "01-REQ-1", Title: "Dup"}); err == nil {
		t.Error("a duplicate requirement ID was accepted")
	}
}

func TestGetAndRemoveRequirement(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	req := *spec.Requirements

	got, ok := GetRequirement(req, "01-REQ-2")
	if !ok || got.Title != "Cross-file validation" {
		t.Fatalf("GetRequirement = %v, %v", got, ok)
	}
	got.Title = "changed"
	if req.Requirements[1].Title == "changed" {
		t.Error("GetRequirement returned a view into the artifact")
	}

	reduced, removed := RemoveRequirement(req, "01-REQ-1")
	if !removed || len(reduced.Requirements) != 1 {
		t.Errorf("RemoveRequirement removed=%v len=%d", removed, len(reduced.Requirements))
	}
	if _, removed := RemoveRequirement(req, "01-REQ-9"); removed {
		t.Error("RemoveRequirement reported removing a requirement that does not exist")
	}
}

func TestGlossaryMutators(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	req := *spec.Requirements

	withTerm := SetGlossaryEntry(req, "coder", "The agent that executes one task at a time.")
	if withTerm.Glossary["coder"] == "" {
		t.Error("SetGlossaryEntry did not add the term")
	}
	if _, present := req.Glossary["coder"]; present {
		t.Error("SetGlossaryEntry mutated the original")
	}

	without, removed := RemoveGlossaryEntry(withTerm, "coder")
	if !removed {
		t.Error("RemoveGlossaryEntry reported nothing removed")
	}
	if _, present := without.Glossary["coder"]; present {
		t.Error("the term survived removal")
	}
	if _, removed := RemoveGlossaryEntry(req, "absent"); removed {
		t.Error("RemoveGlossaryEntry reported removing a term that is not there")
	}
}

func TestAddCriterionAndPath(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)

	r, err := AddCriterion(spec.Requirements.Requirements[0],
		UbiquitousCriterion("01-REQ-1.4", "loader", "do another thing"))
	if err != nil {
		t.Fatalf("AddCriterion = %v", err)
	}
	if len(r.Criteria) != 4 || len(spec.Requirements.Requirements[0].Criteria) != 3 {
		t.Error("AddCriterion mutated the original requirement")
	}
	if _, err := AddCriterion(r, UbiquitousCriterion("01-REQ-1.1", "loader", "dup")); err == nil {
		t.Error("a duplicate criterion ID was accepted")
	}

	if _, ok := GetCriterion(r, "01-REQ-1.4"); !ok {
		t.Error("GetCriterion did not find the criterion just added")
	}

	withPath, err := AddExecutionPath(*spec.Requirements, ExecutionPath{
		Id: "01-PATH-2", Title: "Another path",
		Steps: []PathStep{{Actor: "operator", Action: "does something"}, {Actor: "system", Action: "responds"}},
	})
	if err != nil {
		t.Fatalf("AddExecutionPath = %v", err)
	}
	if len(withPath.ExecutionPaths) != 2 {
		t.Errorf("paths = %d; want 2", len(withPath.ExecutionPaths))
	}
	if _, err := AddExecutionPath(withPath, ExecutionPath{Id: "01-PATH-1", Title: "Dup"}); err == nil {
		t.Error("a duplicate path ID was accepted")
	}
}

func TestAddTestAndTestsOfKind(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)

	added, err := AddTest(*spec.TestSpec, Test{
		Id: "TS-01-6", Kind: TestKindUnit, Verifies: []string{"01-REQ-1.1"},
		Title: "Another", Given: []string{}, When: "x happens", Then: []string{"y"},
	})
	if err != nil {
		t.Fatalf("AddTest = %v", err)
	}
	if len(added.Tests) != 6 || len(spec.TestSpec.Tests) != 5 {
		t.Error("AddTest mutated the original")
	}
	if _, err := AddTest(added, Test{Id: "TS-01-1"}); err == nil {
		t.Error("a duplicate test ID was accepted")
	}

	if got := len(spec.TestSpec.TestsOfKind(TestKindSmoke)); got != 1 {
		t.Errorf("smoke tests = %d; want 1", got)
	}
	if got := len(spec.TestSpec.TestsOfKind(TestKindUnit)); got != 2 {
		t.Errorf("unit tests = %d; want 2", got)
	}

	got, ok := GetTest(*spec.TestSpec, "TS-01-3")
	if !ok || got.Kind != TestKindProperty {
		t.Errorf("GetTest = %v, %v", got, ok)
	}
}

func TestAddTaskAndDependency(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)

	added, err := AddTask(*spec.Tasks, Task{
		Id: 4, Kind: TaskKindImplement, Title: "Another",
		Criteria: []string{"01-REQ-1.1"}, Tests: []string{"TS-01-1"},
		Steps: []string{"do it"}, State: TaskStatePending,
	})
	if err != nil {
		t.Fatalf("AddTask = %v", err)
	}
	if len(added.Tasks) != 4 || len(spec.Tasks.Tasks) != 3 {
		t.Error("AddTask mutated the original")
	}
	if _, err := AddTask(added, Task{Id: 1}); err == nil {
		t.Error("a duplicate task ID was accepted")
	}

	withDep := AddDependency(*spec.Tasks, Dependency{Spec: "00", Reason: "uses the loader"})
	if len(withDep.Dependencies) != 1 || len(spec.Tasks.Dependencies) != 0 {
		t.Error("AddDependency mutated the original")
	}
}

func TestAddExternalAPI(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	added, err := AddExternalAPI(*spec.Requirements, ExternalApi{
		Package: "github.com/spf13/cobra", Version: "v1.10.2", Verified: true,
		Symbols: []ExternalApiSymbol{{Name: "Command", ImportPath: "github.com/spf13/cobra", Signature: "type Command struct{}"}},
	})
	if err != nil {
		t.Fatalf("AddExternalAPI = %v", err)
	}
	if len(added.ExternalApis) != 1 {
		t.Fatalf("external APIs = %d; want 1", len(added.ExternalApis))
	}
	if _, err := AddExternalAPI(added, ExternalApi{Package: "github.com/spf13/cobra", Version: "v1", Verified: false}); err == nil {
		t.Error("a duplicate package was accepted")
	}
}

func TestExtractInterfaceSummaryReportsContracts(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	summary := extractInterfaceSummary(spec)

	glossary, ok := summary["glossary"].(map[string]string)
	if !ok || glossary["spec"] == "" {
		t.Errorf("glossary missing from the summary: %v", summary)
	}

	contracts, ok := summary["contracts"].([]map[string]string)
	if !ok || len(contracts) == 0 {
		t.Fatalf("contracts missing from the summary: %v", summary)
	}
	for _, c := range contracts {
		if c["criterion_id"] == "" || c["contract"] == "" {
			t.Errorf("incomplete contract entry: %v", c)
		}
	}
}
