package agentspec

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/afspec"
)

// fencedJSONRe matches ```json … ``` blocks in a template.
var fencedJSONRe = regexp.MustCompile("(?s)```json\\n(.*?)```")

func loadTemplate(t *testing.T, name string) string {
	t.Helper()
	content, err := LoadPrompt(name, "")
	if err != nil {
		t.Fatalf("LoadPrompt(%q) = %v", name, err)
	}
	return content
}

// TestTemplateExamplesValidateAgainstTheSchema is the guard the ADR asks for:
// format v2 §12.3 requires every example in a prompt to validate against the
// schema of the artifact it illustrates. Under v1 the few-shot examples used
// "schema_version": "1.0" against an integer schema, a string user_story
// against an object, and a coverage object inside each test case — so the
// model was shown a shape the validator rejects.
func TestTemplateExamplesValidateAgainstTheSchema(t *testing.T) {
	cases := []struct {
		template string
		step     afspec.GenerationStep
	}{
		{"generation_user_requirements", afspec.StepRequirements},
		{"generation_user_test_spec", afspec.StepTestSpec},
		{"generation_user_tasks", afspec.StepTasks},
	}

	for _, tc := range cases {
		t.Run(tc.template, func(t *testing.T) {
			blocks := fencedJSONRe.FindAllStringSubmatch(loadTemplate(t, tc.template), -1)
			if len(blocks) == 0 {
				t.Fatalf("template %s has no fenced JSON example", tc.template)
			}

			schemaName, ok := afspec.SchemaNameForStep(tc.step)
			if !ok {
				t.Fatalf("no schema for step %s", tc.step)
			}

			validated := 0
			for i, block := range blocks {
				var content map[string]any
				if err := json.Unmarshal([]byte(block[1]), &content); err != nil {
					t.Errorf("example %d in %s is not valid JSON: %v", i, tc.template, err)
					continue
				}
				// Only full artifacts carry $schema; skip fragments.
				if _, isArtifact := content["$schema"]; !isArtifact {
					continue
				}
				validated++
				if entries := afspec.ValidateArtifactSchema(content, schemaName, tc.template); len(entries) > 0 {
					t.Errorf("example %d in %s does not validate against %s:\n%s",
						i, tc.template, schemaName, afspec.FormatValidationEntries(entries))
				}
			}
			if validated == 0 {
				t.Errorf("template %s has no complete artifact example to validate", tc.template)
			}
		})
	}
}

// TestSystemPromptExamplesValidateAsSkeletons checks the three top-level
// shapes in the system prompt. They are deliberately incomplete — empty
// requirements and tasks lists — so only their keys are checked, against the
// keys the schema allows.
func TestSystemPromptExamplesValidateAsSkeletons(t *testing.T) {
	blocks := fencedJSONRe.FindAllStringSubmatch(loadTemplate(t, "generation_system"), -1)
	if len(blocks) != 3 {
		t.Fatalf("the system prompt has %d JSON skeletons; want one per artifact", len(blocks))
	}

	allowed := map[string]map[string]bool{
		"requirements.v2.json": {
			"$schema": true, "spec_id": true, "spec_name": true, "schema_version": true,
			"introduction": true, "glossary": true, "requirements": true,
			"execution_paths": true, "external_apis": true,
		},
		"test_spec.v2.json": {
			"$schema": true, "spec_id": true, "spec_name": true,
			"schema_version": true, "tests": true,
		},
		"tasks.v2.json": {
			"$schema": true, "spec_id": true, "spec_name": true, "schema_version": true,
			"test_commands": true, "dependencies": true, "tasks": true,
		},
	}

	for _, block := range blocks {
		var content map[string]any
		if err := json.Unmarshal([]byte(block[1]), &content); err != nil {
			t.Errorf("a system-prompt skeleton is not valid JSON: %v", err)
			continue
		}
		schemaURI, _ := content["$schema"].(string)
		parts := strings.Split(schemaURI, "/")
		schemaFile := parts[len(parts)-1]

		keys, known := allowed[schemaFile]
		if !known {
			t.Errorf("a skeleton declares an unknown schema %q", schemaURI)
			continue
		}
		if version, _ := content["schema_version"].(float64); version != 2 {
			t.Errorf("%s skeleton declares schema_version %v; want 2", schemaFile, content["schema_version"])
		}
		for key := range content {
			if !keys[key] {
				t.Errorf("%s skeleton carries %q, which the v2 schema does not allow", schemaFile, key)
			}
		}
	}
}

// TestTemplatesUseNoVersion1Fields catches a template that still *uses* a
// field or ID family v2 removed. A template may name one in prose — saying
// "there is no edge_cases array" is exactly how a model unlearns v1 — so the
// check looks for the JSON key form ("edge_cases":) and for ID examples, not
// for the bare word.
func TestTemplatesUseNoVersion1Fields(t *testing.T) {
	bannedKeys := []string{
		"acceptance_criteria",
		"edge_cases",
		"edge_case_tests",
		"correctness_properties",
		"error_handling",
		"task_groups",
		"wiring_verification",
		"traceability",
		"ears_pattern",
		"return_contract",
		"user_story",
		"for_any_strategy",
		"invariant_check",
		"assertion_pseudocode",
		"requirement_refs",
		"test_spec_refs",
		"expected_effects",
		"mockable",
	}
	bannedLiterals := []string{
		"-PROP-", "-ERR-", "TS-05-P", "TS-05-E", "SMOKE-",
		".v1.json", "pending_reevaluation",
	}

	for _, name := range PromptTemplateNames {
		content := loadTemplate(t, name)
		for _, key := range bannedKeys {
			if strings.Contains(content, `"`+key+`"`) {
				t.Errorf("template %s uses the version 1 field %q as a JSON key", name, key)
			}
		}
		for _, literal := range bannedLiterals {
			if strings.Contains(content, literal) {
				t.Errorf("template %s still shows the version 1 construct %q", name, literal)
			}
		}
	}
}

// TestGenerationTemplatesStateTheCompletenessRules checks that the rules a
// generation is validated against are actually stated to the model. A model
// held to a rule it was never told is a model that repairs by guessing.
func TestGenerationTemplatesStateTheCompletenessRules(t *testing.T) {
	system := loadTemplate(t, "generation_system")
	for _, phrase := range []string{
		"Every criterion is verified by",
		"Every test is",
		"Exactly one task has kind",
		"integration",
		"real_components",
		"contract",
	} {
		if !strings.Contains(system, phrase) {
			t.Errorf("the generation system prompt does not state %q", phrase)
		}
	}

	tests := loadTemplate(t, "generation_user_test_spec")
	for _, phrase := range []string{"verifies", "smoke", "real_components", "at most four criteria"} {
		if !strings.Contains(tests, phrase) {
			t.Errorf("the test_spec template does not state %q", phrase)
		}
	}

	tasks := loadTemplate(t, "generation_user_tasks")
	for _, phrase := range []string{
		"Every test in the test spec is listed",
		"Every criterion is covered",
		"integration",
		"every",
		"touches",
		"depends_on",
	} {
		if !strings.Contains(tasks, phrase) {
			t.Errorf("the tasks template does not state %q", phrase)
		}
	}
}

// TestRequirementsTemplateCoversEveryPattern keeps the EARS field rules in the
// one template that needs them.
func TestRequirementsTemplateCoversEveryPattern(t *testing.T) {
	content := loadTemplate(t, "generation_user_requirements")
	for _, pattern := range []string{
		"ubiquitous", "event_driven", "complex_event", "state_driven", "unwanted", "optional",
	} {
		if !strings.Contains(content, pattern) {
			t.Errorf("the requirements template does not cover the %q pattern", pattern)
		}
	}
	for _, field := range []string{"`condition`", "`guard`", "`contract`", "`rationale`"} {
		if !strings.Contains(content, field) {
			t.Errorf("the requirements template does not mention %s", field)
		}
	}
	if !strings.Contains(content, "at most 10 requirements") {
		t.Error("the requirements template does not state the scope limit")
	}
}

// TestEARSRulesStayInTheRequirementsTemplate keeps the pattern tables out of
// the test_spec and tasks prompts, which do not need them.
func TestEARSRulesStayInTheRequirementsTemplate(t *testing.T) {
	for _, name := range []string{"generation_user_test_spec", "generation_user_tasks"} {
		content := loadTemplate(t, name)
		for _, token := range []string{"event_driven", "complex_event", "state_driven"} {
			if strings.Contains(content, token) {
				t.Errorf("template %s carries the EARS token %q; pattern rules belong in the requirements prompt", name, token)
			}
		}
	}
}

// TestAssessmentPromptChecksSpecScope guards §12.4: the coordinator decides
// before writing requirements whether the PRD fits in ten of them.
func TestAssessmentPromptChecksSpecScope(t *testing.T) {
	content := loadTemplate(t, "assessment_system")
	for _, phrase := range []string{"at most 10 requirements", "split"} {
		if !strings.Contains(content, phrase) {
			t.Errorf("the assessment system prompt does not state %q", phrase)
		}
	}
}

func TestEveryTemplateLoads(t *testing.T) {
	for _, name := range PromptTemplateNames {
		content := loadTemplate(t, name)
		if strings.TrimSpace(content) == "" {
			t.Errorf("template %s is empty", name)
		}
		if strings.HasPrefix(content, "---") {
			t.Errorf("template %s still carries its YAML frontmatter", name)
		}
	}
}
