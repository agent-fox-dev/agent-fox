package afspec

import (
	"encoding/json"
	"strings"
	"testing"
)

// artifactMap re-encodes a typed artifact as the decoded JSON object a model's
// tool input arrives as.
func artifactMap(t *testing.T, v any) map[string]any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func partialFrom(t *testing.T, steps ...GenerationStep) PartialSpec {
	t.Helper()
	source := loadFixture(t, fixtureValidSpec)
	partial := PartialSpec{SpecID: source.SpecID, SpecName: source.SpecName}
	for _, step := range steps {
		switch step {
		case StepRequirements:
			partial.Requirements = source.Requirements
		case StepTestSpec:
			partial.TestSpec = source.TestSpec
		case StepTasks:
			partial.Tasks = source.Tasks
		}
	}
	return partial
}

func TestGenerationOrderIsRequirementsTestsTasks(t *testing.T) {
	want := []GenerationStep{StepRequirements, StepTestSpec, StepTasks}
	if len(GenerationSteps) != len(want) {
		t.Fatalf("GenerationSteps = %v; want %v", GenerationSteps, want)
	}
	for i, step := range want {
		if GenerationSteps[i] != step {
			t.Errorf("step %d = %q; want %q", i, GenerationSteps[i], step)
		}
	}
}

func TestValidateGenerationStepAcceptsTheCanonicalArtifacts(t *testing.T) {
	for _, step := range GenerationSteps {
		t.Run(string(step), func(t *testing.T) {
			var produced []GenerationStep
			for _, s := range GenerationSteps {
				produced = append(produced, s)
				if s == step {
					break
				}
			}
			result := ValidateGenerationStep(step, partialFrom(t, produced...))
			if !result.Valid {
				t.Fatalf("step %s rejected valid artifacts: %v", step, result.Errors)
			}
		})
	}
}

// TestValidateGenerationStepDefersUndecidableRules checks that a rule is only
// applied once the artifact it needs exists: after the requirements step
// nothing complains that no test verifies a criterion.
func TestValidateGenerationStepDefersUndecidableRules(t *testing.T) {
	result := ValidateGenerationStep(StepRequirements, partialFrom(t, StepRequirements))
	if !result.Valid {
		t.Fatalf("the requirements step reported errors that need later artifacts: %v", result.Errors)
	}
	for _, check := range []string{"C4", "C5", "C7", "C8", "C9"} {
		if hasCheck(result, check) {
			t.Errorf("rule %s fired at the requirements step, before it is decidable", check)
		}
	}
}

func TestValidateGenerationStepCatchesGapsAtTheTestStep(t *testing.T) {
	partial := partialFrom(t, StepRequirements, StepTestSpec)
	// Drop the test that verifies 01-REQ-2.1.
	tests := partial.TestSpec.Tests
	partial.TestSpec = &TestSpecV2Json{
		Schema: tests[0].Id, SpecId: partial.SpecID, SpecName: partial.SpecName,
		SchemaVersion: SchemaVersion,
		Tests:         append(append([]Test{}, tests[:3]...), tests[4]),
	}
	partial.TestSpec.Schema = "https://agent-fox.dev/schemas/test_spec.v2.json"

	result := ValidateGenerationStep(StepTestSpec, partial)
	if result.Valid {
		t.Fatal("a criterion verified by no test passed the test step")
	}
	if !hasCheck(result, "C4") {
		t.Errorf("checks = %v; want C4", errorChecks(result))
	}
}

// TestValidateGenerationStepCatchesOrphanedTests is the pipeline-side guard on
// the defect that motivated v2: under v1 the task generator never saw the test
// IDs and orphaned every edge-case and property test.
func TestValidateGenerationStepCatchesOrphanedTests(t *testing.T) {
	partial := partialFrom(t, StepRequirements, StepTestSpec, StepTasks)
	tasks := *partial.Tasks
	tasks.Tasks = append([]Task{}, tasks.Tasks...)
	tasks.Tasks[0].Tests = []string{"TS-01-1"}
	partial.Tasks = &tasks

	result := ValidateGenerationStep(StepTasks, partial)
	if result.Valid {
		t.Fatal("a plan that orphans two tests passed the tasks step")
	}
	if !hasCheck(result, "C7") {
		t.Errorf("checks = %v; want C7", errorChecks(result))
	}
}

// TestValidateGenerationStepSkipsReferenceRulesOnSchemaFailure keeps a
// schema-invalid artifact from producing a cascade of reference errors that
// are all consequences of the same defect.
func TestValidateGenerationStepSkipsReferenceRulesOnSchemaFailure(t *testing.T) {
	partial := partialFrom(t, StepRequirements)
	reqs := *partial.Requirements
	reqs.Introduction = "" // violates minLength
	partial.Requirements = &reqs

	result := ValidateGenerationStep(StepRequirements, partial)
	if result.Valid {
		t.Fatal("a schema-invalid artifact was accepted")
	}
	for _, e := range result.Errors {
		if e.Category != "schema" {
			t.Errorf("a reference rule ran on a schema-invalid artifact: %+v", e)
		}
	}
}

// TestValidateGenerationStepNamesTheRuleBehindAConditional checks the two
// rules the schema expresses as conditionals. The schema's own message for
// those ("missing property", "'not' failed") does not say which rule broke, so
// C10 and C11 run alongside it to give the repair loop something to act on.
func TestValidateGenerationStepNamesTheRuleBehindAConditional(t *testing.T) {
	t.Run("unwanted criterion without a contract", func(t *testing.T) {
		partial := partialFrom(t, StepRequirements)
		reqs := *partial.Requirements
		reqs.Requirements = append([]Requirement{}, reqs.Requirements...)
		reqs.Requirements[0].Criteria = append([]Criterion{}, reqs.Requirements[0].Criteria...)
		reqs.Requirements[0].Criteria[1].Contract = nil
		partial.Requirements = &reqs

		result := ValidateGenerationStep(StepRequirements, partial)
		if !hasCheck(result, "C10") {
			t.Errorf("checks = %v; want C10 alongside the schema error", errorChecks(result))
		}
	})

	t.Run("unit test carrying real components", func(t *testing.T) {
		partial := partialFrom(t, StepRequirements, StepTestSpec)
		ts := *partial.TestSpec
		ts.Tests = append([]Test{}, ts.Tests...)
		ts.Tests[0].RealComponents = []string{"a database"}
		partial.TestSpec = &ts

		result := ValidateGenerationStep(StepTestSpec, partial)
		if !hasCheck(result, "C11") {
			t.Errorf("checks = %v; want C11 alongside the schema error", errorChecks(result))
		}
	})
}

func TestDecodeArtifact(t *testing.T) {
	source := loadFixture(t, fixtureValidSpec)

	decoded, err := DecodeArtifact(StepRequirements, artifactMap(t, source.Requirements))
	if err != nil {
		t.Fatalf("DecodeArtifact = %v", err)
	}
	reqs, ok := decoded.(*RequirementsV2Json)
	if !ok {
		t.Fatalf("DecodeArtifact returned %T; want *RequirementsV2Json", decoded)
	}
	if len(reqs.Requirements) != 2 {
		t.Errorf("requirements = %d; want 2", len(reqs.Requirements))
	}

	if _, err := DecodeArtifact(StepTestSpec, artifactMap(t, source.TestSpec)); err != nil {
		t.Errorf("DecodeArtifact(test_spec) = %v", err)
	}
	if _, err := DecodeArtifact(StepTasks, artifactMap(t, source.Tasks)); err != nil {
		t.Errorf("DecodeArtifact(tasks) = %v", err)
	}
	if _, err := DecodeArtifact("nonsense", map[string]any{}); err == nil {
		t.Error("an unknown step was accepted")
	}
	if _, err := DecodeArtifact(StepTasks, map[string]any{"tasks": "not an array"}); err == nil {
		t.Error("a type-mismatched artifact decoded without error")
	}
}

func TestSchemaNameAndFileNameForStep(t *testing.T) {
	for _, step := range GenerationSteps {
		name, ok := SchemaNameForStep(step)
		if !ok || !strings.HasSuffix(name, ".v2.json") {
			t.Errorf("SchemaNameForStep(%s) = %q, %v", step, name, ok)
		}
		if file := ArtifactFileName(step); !strings.HasSuffix(file, ".json") {
			t.Errorf("ArtifactFileName(%s) = %q", step, file)
		}
	}
	if _, ok := SchemaNameForStep("nonsense"); ok {
		t.Error("an unknown step resolved to a schema")
	}
}

func TestFormatValidationEntries(t *testing.T) {
	if got := FormatValidationEntries(nil); got != "" {
		t.Errorf("= %q; want empty for no entries", got)
	}
	out := FormatValidationEntries([]ValidationEntry{
		{Check: "C7", Message: "test TS-01-3 is not owned by any task"},
		{Check: "json_schema", Path: "/tests/0/kind", Message: "value must be one of …"},
	})
	for _, want := range []string{"[C7]", "TS-01-3", "at /tests/0/kind"} {
		if !strings.Contains(out, want) {
			t.Errorf("the feedback is missing %q:\n%s", want, out)
		}
	}
}
