package agentspec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-fox-dev/agentfox/afspec"
)

// The helpers below build the artifacts a model would return through its
// submit_* tool. They are complete and valid under format v2: every criterion
// is verified by a test, every test is owned by a task, the path has a smoke
// test and the final integration task owns it. Tests that need an *invalid*
// artifact start from one of these and break exactly one thing, so a failure
// message names the rule under test rather than a pile of unrelated ones.

// v2RequirementsArtifact returns a valid requirements artifact for the spec.
func v2RequirementsArtifact(specID, specName string) map[string]any {
	return map[string]any{
		"$schema":        "https://agent-fox.dev/schemas/requirements.v2.json",
		"spec_id":        specID,
		"spec_name":      specName,
		"schema_version": 2,
		"introduction":   "The widget service stores widgets and serves them back.",
		"glossary": map[string]any{
			"widget": "The unit of storage this service manages.",
		},
		"requirements": []any{
			map[string]any{
				"id":        specID + "-REQ-1",
				"title":     "Widget storage",
				"rationale": "A widget that cannot be stored cannot be served.",
				"criteria": []any{
					map[string]any{
						"id":        specID + "-REQ-1.1",
						"pattern":   "event_driven",
						"condition": "a client submits a widget with a name",
						"system":    "widget service",
						"action":    "persist the widget and return its identifier",
						"contract":  "HTTP 201 with body {id: string}",
					},
					map[string]any{
						"id":        specID + "-REQ-1.2",
						"pattern":   "unwanted",
						"condition": "the submitted widget has no name",
						"system":    "widget service",
						"action":    "reject the request and persist nothing",
						"contract":  "HTTP 400 with body {error: string}; the store is unchanged",
					},
				},
			},
		},
		"execution_paths": []any{
			map[string]any{
				"id":    specID + "-PATH-1",
				"title": "A client stores a widget and reads it back",
				"steps": []any{
					map[string]any{"actor": "client", "action": "submits a widget"},
					map[string]any{"actor": "widget service", "action": "persists it and returns the identifier"},
				},
			},
		},
	}
}

// v2TestSpecArtifact returns a test spec that verifies every criterion and
// path of v2RequirementsArtifact.
func v2TestSpecArtifact(specID, specName string) map[string]any {
	return map[string]any{
		"$schema":        "https://agent-fox.dev/schemas/test_spec.v2.json",
		"spec_id":        specID,
		"spec_name":      specName,
		"schema_version": 2,
		"tests": []any{
			map[string]any{
				"id":       "TS-" + specID + "-1",
				"kind":     "unit",
				"verifies": []any{specID + "-REQ-1.1"},
				"title":    "A named widget is persisted and its identifier returned",
				"given":    []any{"an empty store"},
				"when":     "a widget with a name is submitted",
				"then":     []any{"the status is 201", "the body carries a non-empty id"},
			},
			map[string]any{
				"id":       "TS-" + specID + "-2",
				"kind":     "unit",
				"verifies": []any{specID + "-REQ-1.2"},
				"title":    "A widget with no name is rejected and nothing is stored",
				"given":    []any{"an empty store"},
				"when":     "a widget without a name is submitted",
				"then":     []any{"the status is 400", "the store is still empty"},
			},
			map[string]any{
				"id":              "TS-" + specID + "-3",
				"kind":            "smoke",
				"verifies":        []any{specID + "-PATH-1"},
				"title":           "A client stores a widget end to end",
				"given":           []any{"the service is running against a real store"},
				"when":            "a widget is submitted and read back",
				"then":            []any{"the read returns the widget that was submitted"},
				"real_components": []any{"widget service", "store"},
			},
		},
	}
}

// v2TasksArtifact returns a plan that owns every test of v2TestSpecArtifact
// and every criterion of v2RequirementsArtifact.
func v2TasksArtifact(specID, specName string) map[string]any {
	return map[string]any{
		"$schema":        "https://agent-fox.dev/schemas/tasks.v2.json",
		"spec_id":        specID,
		"spec_name":      specName,
		"schema_version": 2,
		"test_commands": map[string]any{
			"all_tests": "go test ./... -count=1",
			"linter":    "go vet ./...",
		},
		"dependencies": []any{},
		"tasks": []any{
			map[string]any{
				"id":       1,
				"kind":     "implement",
				"title":    "Store and serve widgets",
				"criteria": []any{specID + "-REQ-1"},
				"tests":    []any{"TS-" + specID + "-1", "TS-" + specID + "-2"},
				"steps": []any{
					"Write the two unit tests and confirm they fail",
					"Add the widget row type and the store's Insert method",
					"Reject a widget with no name with 400, writing nothing",
				},
				"touches": []any{"store/widget.go"},
				"state":   "pending",
			},
			map[string]any{
				"id":       2,
				"kind":     "integration",
				"title":    "Verify the store-and-read path end to end",
				"criteria": []any{},
				"tests":    []any{"TS-" + specID + "-3"},
				"steps": []any{
					"Write the smoke test against a real store and confirm it passes",
					"Trace the path through the handler and the store; confirm no stub remains",
				},
				"depends_on": []any{1},
				"state":      "pending",
			},
		},
	}
}

// v2Artifact returns the valid artifact for one generation step.
func v2Artifact(step afspec.GenerationStep, specID, specName string) map[string]any {
	switch step {
	case afspec.StepRequirements:
		return v2RequirementsArtifact(specID, specName)
	case afspec.StepTestSpec:
		return v2TestSpecArtifact(specID, specName)
	case afspec.StepTasks:
		return v2TasksArtifact(specID, specName)
	default:
		return nil
	}
}

// v2ArtifactByName is v2Artifact keyed by the artifact name the pipeline uses.
func v2ArtifactByName(name, specID, specName string) map[string]any {
	return v2Artifact(afspec.GenerationStep(name), specID, specName)
}

// artifactNameFromContext maps an AICallOptions.Context value such as
// "GenerateArtifacts:test_spec:repair:1" to the artifact it is generating.
func artifactNameFromContext(context string) string {
	// Longest first: "test_spec" would otherwise never match if "tasks" did.
	for _, name := range []string{"requirements", "test_spec", "tasks"} {
		if containsSub(context, name) {
			return name
		}
	}
	return ""
}

func containsSub(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// mockGenerationCall returns an aiCallFunc that answers every generation step
// with the valid artifact for that step.
func mockGenerationCall(capture *aiCallCapture, specID, specName string) func(ctx context.Context, opts AICallOptions) (string, any, error) {
	return func(_ context.Context, opts AICallOptions) (string, any, error) {
		if capture != nil {
			capture.record(opts)
		}
		name := artifactNameFromContext(opts.Context)
		if name == "" {
			return "", nil, fmt.Errorf("unexpected AICall context %q", opts.Context)
		}
		return "", makeToolCallResponse("end_turn",
			makeArtifactToolCall(name, v2ArtifactByName(name, specID, specName))), nil
	}
}

// writeV2Artifacts writes the three valid artifacts into dir, so that a
// session test can start from a complete spec.
func writeV2Artifacts(t *testing.T, dir, specID, specName string) {
	t.Helper()
	for _, step := range afspec.GenerationSteps {
		data, err := json.MarshalIndent(v2Artifact(step, specID, specName), "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, afspec.ArtifactFileName(step))
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			t.Fatalf("cannot write %s: %v", path, err)
		}
	}
}

// v2PRD returns a prd.md whose frontmatter matches the spec identity, so that
// afspec.LoadSpec can read the spec and Validate can check rule C1.
func v2PRD(specID, specName, title string) string {
	return "---\n" +
		"spec_id: \"" + specID + "\"\n" +
		"spec_name: \"" + specName + "\"\n" +
		"title: \"" + title + "\"\n" +
		"status: \"draft\"\n" +
		"created_at: \"2026-01-01T00:00:00Z\"\n" +
		"updated_at: \"2026-01-01T00:00:00Z\"\n" +
		"intent_hash: null\n" +
		"schema_version: 2\n" +
		"---\n" +
		"# " + title + "\n\n## Intent\n\nStore widgets and serve them back.\n"
}

// newSpecDir creates a {NN}_{snake_case_name} directory inside a fresh temp
// directory and writes a matching prd.md into it. The directory name has to
// match the identity or rule C1 fails, which is the point of the rule.
func newSpecDir(t *testing.T, specID, specName string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), specID+"_"+specName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("cannot create %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prd.md"),
		[]byte(v2PRD(specID, specName, "Widget Service")), 0o644); err != nil {
		t.Fatalf("cannot write prd.md: %v", err)
	}
	return dir
}
