package afspec

import (
	"strings"
	"testing"
)

func TestRenderRequirementsShowsEveryCriterionAndContract(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	out := spec.Requirements.Render()

	for _, want := range []string{
		"## Introduction",
		"## Glossary",
		"### 01-REQ-1: Data model",
		"**Rationale:**",
		"#### Criteria",
		"[01-REQ-1.1] WHEN a spec directory is loaded from disk, THE afspec library SHALL",
		"→ a non-nil *Spec and a nil error",
		"[01-REQ-1.2] IF a required artifact file is missing",
		"## Execution Paths",
		"### 01-PATH-1:",
		"1. **consumer** calls LoadSpec(dir)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the requirements render is missing %q", want)
		}
	}
}

// TestRenderShowsEveryContract guards §11.2. Under v1 return_contract was
// never rendered, so the coder never saw it.
func TestRenderShowsEveryContract(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	out := spec.Requirements.Render()

	for _, req := range spec.Requirements.Requirements {
		for _, c := range req.Criteria {
			contract := c.ContractText()
			if contract == "" {
				continue
			}
			if !strings.Contains(out, "→ "+contract) {
				t.Errorf("criterion %s has a contract that the render omits", c.Id)
			}
		}
	}
}

// TestRenderShowsEveryTestInFull guards §11.2: every test renders all of its
// fields whatever its kind. Under v1 edge-case and smoke tests rendered as an
// ID and a description only.
func TestRenderShowsEveryTestInFull(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	out := spec.TestSpec.Render()

	for _, test := range spec.TestSpec.Tests {
		if !strings.Contains(out, test.Id) || !strings.Contains(out, test.Title) {
			t.Fatalf("test %s is missing from the render", test.Id)
		}
		if !strings.Contains(out, test.When) {
			t.Errorf("test %s (%s): the When clause is not rendered", test.Id, test.Kind)
		}
		for _, then := range test.Then {
			if !strings.Contains(out, then) {
				t.Errorf("test %s (%s): a Then clause is not rendered", test.Id, test.Kind)
			}
		}
		for _, g := range test.Given {
			if !strings.Contains(out, g) {
				t.Errorf("test %s (%s): a Given clause is not rendered", test.Id, test.Kind)
			}
		}
		for _, rc := range test.RealComponents {
			if !strings.Contains(out, rc) {
				t.Errorf("test %s: real component %q is not rendered", test.Id, rc)
			}
		}
	}
}

func TestRenderTasksShowsThePlan(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	out := spec.Tasks.Render()

	for _, want := range []string{
		"### [ ] 1. Load and save spec artifacts (implement, pending)",
		"**Criteria:** 01-REQ-1",
		"**Tests:** TS-01-1, TS-01-2, TS-01-3",
		"**Steps:**",
		"**Touches:**",
		"`afspec/afspec.go`",
		"**Depends on:** 1, 2",
		"**Done when:**",
		"- the tests listed above exist, are executable and pass",
		"- `go test ./... -count=1` passes",
		"- `go vet ./...` passes",
		"`go test ./afspec/... -count=1` passes",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the tasks render is missing %q", want)
		}
	}
}

func TestRenderCombinedSectionOrder(t *testing.T) {
	spec := loadFixture(t, "../testdata/valid_spec_with_arch")
	out := spec.RenderCombined()

	sections := []string{"# PRD", "# Architecture", "# Requirements", "# Test Specification", "# Tasks"}
	last := -1
	for _, section := range sections {
		i := strings.Index(out, section)
		if i < 0 {
			t.Fatalf("section %q is missing", section)
		}
		if i < last {
			t.Errorf("section %q appears out of order", section)
		}
		last = i
	}
}

func TestRenderIndividualKeys(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	out := spec.RenderIndividual()
	for _, key := range []string{"prd", "requirements", "test_spec", "tasks"} {
		if out[key] == "" {
			t.Errorf("key %q is empty", key)
		}
	}
	if _, present := out["architecture"]; present {
		t.Error("the architecture key is present for a spec with no architecture.md")
	}

	withArch := loadFixture(t, "../testdata/valid_spec_with_arch").RenderIndividual()
	if withArch["architecture"] == "" {
		t.Error("the architecture key is missing for a spec that has one")
	}
}

// TestRenderIndividualScopedUsesTheTaskItself guards §11.1: scoping reads
// task.criteria and task.tests directly. The v1 inference chain is gone.
func TestRenderIndividualScopedUsesTheTaskItself(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	out := spec.RenderIndividualScoped(2)

	reqs := out["requirements"]
	if !strings.Contains(reqs, "## Spec Overview") {
		t.Error("the scoped requirements render has no overview")
	}
	if !strings.Contains(reqs, "**01-REQ-1:** Data model (other task)") {
		t.Error("the out-of-scope requirement is not summarised as one line")
	}
	if !strings.Contains(reqs, "**01-REQ-2:** Cross-file validation (included below)") {
		t.Error("the in-scope requirement is not marked as included")
	}
	if !strings.Contains(reqs, "### 01-REQ-2: Cross-file validation") {
		t.Error("the in-scope requirement is not rendered in full")
	}
	if strings.Contains(reqs, "### 01-REQ-1: Data model") {
		t.Error("an out-of-scope requirement was rendered in full")
	}

	tests := out["test_spec"]
	if !strings.Contains(tests, "TS-01-4") {
		t.Error("the task's own test is missing")
	}
	if strings.Contains(tests, "TS-01-1") {
		t.Error("a test belonging to another task was rendered")
	}

	tasks := out["tasks"]
	if !strings.Contains(tasks, "### [ ] 2. Cross-file validation") {
		t.Error("the target task is not rendered in full")
	}
	if !strings.Contains(tasks, "- [ ] 1. Load and save spec artifacts") {
		t.Error("another task is not summarised as one line")
	}
	if strings.Contains(tasks, "### [ ] 1. Load and save spec artifacts") {
		t.Error("another task was rendered in full")
	}
}

func TestRenderIndividualScopedIncludesTheVerifiedPaths(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)

	// Task 3 owns the smoke test, which verifies 01-PATH-1.
	if !strings.Contains(spec.RenderIndividualScoped(3)["requirements"], "### 01-PATH-1:") {
		t.Error("the path verified by the task's smoke test is missing from the scoped render")
	}
	// Task 2 owns no smoke test, so no path is in scope.
	if strings.Contains(spec.RenderIndividualScoped(2)["requirements"], "## Execution Paths") {
		t.Error("a path was rendered for a task whose tests verify none")
	}
}

func TestRenderIndividualScopedFallsBackForAnUnknownTask(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	scoped := spec.RenderIndividualScoped(99)
	full := spec.RenderIndividual()
	if scoped["requirements"] != full["requirements"] {
		t.Error("an unknown task ID should fall back to the unscoped render")
	}
}

// ---------------------------------------------------------------------------
// Budgeted rendering
// ---------------------------------------------------------------------------

func TestRenderCombinedDropsArchitectureFirst(t *testing.T) {
	spec := loadFixture(t, "../testdata/valid_spec_with_arch")
	full := spec.RenderCombined()

	budget := EstimateTokens(full) - 1
	out := spec.RenderCombined(WithMaxTokens(budget))
	if strings.Contains(out, "# Architecture") {
		t.Error("the architecture section survived a budget it could not fit in")
	}
	if !strings.Contains(out, "# Requirements") {
		t.Error("level 1 dropped more than the architecture section")
	}
}

// TestSlimTestRenderKeepsVerifiesAndThen guards the budget rule of the ADR:
// slimming may drop context but never what a test asserts or what it verifies.
func TestSlimTestRenderKeepsVerifiesAndThen(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	slim := renderTestSpecSlim(spec.TestSpec)

	for _, test := range spec.TestSpec.Tests {
		if !strings.Contains(slim, test.Id) {
			t.Errorf("test %s is missing from the slim render", test.Id)
		}
		for _, ref := range test.Verifies {
			if !strings.Contains(slim, ref) {
				t.Errorf("test %s: verifies %s is missing from the slim render", test.Id, ref)
			}
		}
		for _, then := range test.Then {
			if !strings.Contains(slim, then) {
				t.Errorf("test %s: a Then clause is missing from the slim render", test.Id)
			}
		}
	}
	if strings.Contains(slim, "**Pseudocode:**") {
		t.Error("the slim render kept the pseudocode it is meant to drop")
	}
	if EstimateTokens(slim) >= EstimateTokens(spec.TestSpec.Render()) {
		t.Error("the slim render is not smaller than the full one")
	}
}

func TestRenderIndividualAppliesTheBudget(t *testing.T) {
	spec := loadFixture(t, "../testdata/valid_spec_with_arch")
	full := spec.RenderIndividual()

	out := spec.RenderIndividual(WithMaxTokens(sumMapTokens(full) - 1))
	if _, present := out["architecture"]; present {
		t.Error("architecture was not dropped at level 1")
	}

	out = spec.RenderIndividual(WithMaxTokens(1))
	if strings.Contains(out["test_spec"], "**Pseudocode:**") {
		t.Error("the test spec was not slimmed at level 2")
	}
}

func TestZeroBudgetMeansUnlimited(t *testing.T) {
	spec := loadFixture(t, "../testdata/valid_spec_with_arch")
	if spec.RenderCombined(WithMaxTokens(0)) != spec.RenderCombined() {
		t.Error("WithMaxTokens(0) changed the output; it should disable the budget")
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	for i := 0; i < 5; i++ {
		if spec.RenderCombined() != spec.RenderCombined() {
			t.Fatal("two renders of the same spec differ")
		}
	}
}

func TestRenderExternalAPIFlagsUnverifiedPackages(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	spec.Requirements.ExternalApis = []ExternalApi{{
		Package: "example.com/guess", Version: "v0.1.0", Verified: false,
		Symbols: []ExternalApiSymbol{{Name: "Do", ImportPath: "example.com/guess", Signature: "func Do()"}},
	}}
	out := spec.Requirements.Render()
	if !strings.Contains(out, "UNVERIFIED") {
		t.Errorf("an unverified package is not flagged in the render:\n%s", out)
	}
}
