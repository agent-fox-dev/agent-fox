package afspec

import (
	"encoding/json"
	"fmt"
)

// GenerationStep names the artifact a generation pipeline has just produced.
// Format v2 §12.1 fixes the order: requirements, then tests, then tasks.
type GenerationStep string

const (
	StepRequirements GenerationStep = "requirements"
	StepTestSpec     GenerationStep = "test_spec"
	StepTasks        GenerationStep = "tasks"
)

// GenerationSteps is the mandatory generation order.
var GenerationSteps = []GenerationStep{StepRequirements, StepTestSpec, StepTasks}

// SchemaNameForStep returns the bundled schema that validates the artifact a
// step produces.
func SchemaNameForStep(step GenerationStep) (string, bool) {
	switch step {
	case StepRequirements:
		return RequirementsSchemaName, true
	case StepTestSpec:
		return TestSpecSchemaName, true
	case StepTasks:
		return TasksSchemaName, true
	default:
		return "", false
	}
}

// ArtifactFileName returns the on-disk file name for a step's artifact.
func ArtifactFileName(step GenerationStep) string {
	switch step {
	case StepRequirements:
		return "requirements.json"
	case StepTestSpec:
		return "test_spec.json"
	case StepTasks:
		return "tasks.json"
	default:
		return string(step)
	}
}

// PartialSpec holds the artifacts a generation run has produced so far. Any of
// them may be nil while generation is still in progress.
type PartialSpec struct {
	SpecID       string
	SpecName     string
	Requirements *RequirementsV2Json
	TestSpec     *TestSpecV2Json
	Tasks        *TasksV2Json
}

// DecodeArtifact turns the tool input a model returned — a decoded JSON object
// — into the typed artifact for the given step.
func DecodeArtifact(step GenerationStep, content map[string]any) (any, error) {
	data, err := json.Marshal(content)
	if err != nil {
		return nil, fmt.Errorf("cannot re-encode the %s artifact: %w", step, err)
	}
	switch step {
	case StepRequirements:
		var out RequirementsV2Json
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("cannot decode the requirements artifact: %w", err)
		}
		return &out, nil
	case StepTestSpec:
		var out TestSpecV2Json
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("cannot decode the test_spec artifact: %w", err)
		}
		return &out, nil
	case StepTasks:
		var out TasksV2Json
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("cannot decode the tasks artifact: %w", err)
		}
		return &out, nil
	default:
		return nil, fmt.Errorf("unknown generation step %q", step)
	}
}

// ValidateGenerationStep runs, on the artifacts produced so far, the schema of
// the artifact just produced plus every cross-file rule of §10.2 that is
// decidable at that point (§12.2):
//
//	after requirements  C1, C2 over requirements, C10
//	after test_spec     the above plus C2 over tests, C3, C4, C5, C11
//	after tasks         the above plus C6, C7, C8, C9
//
// A pipeline sends the returned errors back to the model for repair and does
// not write an artifact that still fails. Warnings are advisory.
func ValidateGenerationStep(step GenerationStep, partial PartialSpec) ValidationResult {
	spec := &Spec{
		SpecID:       partial.SpecID,
		SpecName:     partial.SpecName,
		Requirements: partial.Requirements,
		TestSpec:     partial.TestSpec,
		Tasks:        partial.Tasks,
	}

	var errors []ValidationEntry

	// Schema of the artifact just produced.
	switch step {
	case StepRequirements:
		if partial.Requirements != nil {
			errors = append(errors, ValidateArtifactSchema(partial.Requirements, RequirementsSchemaName, "requirements.json")...)
		}
	case StepTestSpec:
		if partial.TestSpec != nil {
			errors = append(errors, ValidateArtifactSchema(partial.TestSpec, TestSpecSchemaName, "test_spec.json")...)
		}
	case StepTasks:
		if partial.Tasks != nil {
			errors = append(errors, ValidateArtifactSchema(partial.Tasks, TasksSchemaName, "tasks.json")...)
		}
	}

	// A schema-invalid artifact makes the reference rules meaningless: they
	// would report consequences of the same defect. Two rules are worth
	// running anyway, because the schema expresses them as conditionals whose
	// failure message ("'not' failed", "missing property") does not say which
	// rule was broken, and a repair loop needs to be told.
	if len(errors) > 0 {
		errors = append(errors, spec.checkC10()...)
		errors = append(errors, spec.checkC11()...)
		return ValidationResult{Valid: false, Errors: errors}
	}

	idx := spec.buildIndex()

	errors = append(errors, spec.checkC1()...)
	errors = append(errors, spec.checkC2(idx)...)
	errors = append(errors, spec.checkC10()...)

	if step == StepTestSpec || step == StepTasks {
		errors = append(errors, spec.checkC3C5(idx)...)
		errors = append(errors, spec.checkC11()...)
	}
	if step == StepTasks {
		errors = append(errors, spec.checkC6C9(idx)...)
	}

	return ValidationResult{
		Valid:    len(errors) == 0,
		Errors:   errors,
		Warnings: spec.crossFileWarnings(idx),
	}
}

// FormatValidationEntries renders validation entries as the feedback a
// generation pipeline sends back to the model for repair. Each line names the
// rule so the model can act on it.
func FormatValidationEntries(entries []ValidationEntry) string {
	if len(entries) == 0 {
		return ""
	}
	out := ""
	for _, e := range entries {
		line := "- "
		if e.Check != "" {
			line += "[" + e.Check + "] "
		}
		if e.Path != "" {
			line += "at " + e.Path + ": "
		}
		line += e.Message
		out += line + "\n"
	}
	return out
}
