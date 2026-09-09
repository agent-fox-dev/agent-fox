package agentspec

import (
	"encoding/json"
	"sync"

	"github.com/agent-fox-dev/agentfox/afspec"
)

// artifactEntry caches the cleaned input_schema for one artifact type.
// sync.Once guarantees the computation runs exactly once per process
// lifetime, even under concurrent callers.
type artifactEntry struct {
	once   sync.Once
	schema map[string]any // written inside once.Do; safe to read after once.Do returns
}

// artifactEntries holds one entry per supported artifact type. The map
// is initialised at package load time and never mutated afterwards, so
// concurrent reads are safe without additional locking.
var artifactEntries = map[string]*artifactEntry{
	"requirements": {},
	"test_spec":    {},
	"tasks":        {},
}

// deepCopyJSONMap returns a deep copy of a JSON-compatible map by
// marshalling to JSON and unmarshalling into a fresh map. This is used
// to prevent callers from mutating the cached schema.
func deepCopyJSONMap(m map[string]any) map[string]any {
	b, err := json.Marshal(m)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return map[string]any{}
	}
	return out
}

// assessmentToolDef builds a fresh submit_assessment tool definition.
func assessmentToolDef() map[string]any {
	return map[string]any{
		"name":        "submit_assessment",
		"description": "Submit a PRD quality assessment with quality rating, summary, gaps, and clarifying questions.",
		"input_schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"quality": map[string]any{
					"type":        "string",
					"enum":        []any{"ready", "needs_refinement", "incomplete"},
					"description": "Overall quality rating of the PRD.",
				},
				"summary": map[string]any{
					"type":        "string",
					"description": "Brief summary of the assessment.",
				},
				"gaps": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "string",
					},
					"description": "List of identified gaps in the PRD.",
				},
				"questions": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id": map[string]any{
								"type":        "string",
								"description": "Unique identifier for the question.",
							},
							"text": map[string]any{
								"type":        "string",
								"description": "The question text.",
							},
							"context": map[string]any{
								"type":        "string",
								"description": "Context explaining why this question matters.",
							},
							"options": map[string]any{
								"type": "array",
								"items": map[string]any{
									"type": "string",
								},
								"description": "Suggested answer options, if applicable.",
							},
							"required": map[string]any{
								"type":        "boolean",
								"description": "Whether an answer to this question is required.",
							},
						},
						"required": []any{"id", "text"},
					},
					"description": "Clarifying questions for the PRD author.",
				},
			},
			"required": []any{"quality", "summary", "gaps", "questions"},
		},
	}
}

// AssessmentTools returns a slice containing the Anthropic tool definition
// for the assessment phase. The returned slice contains exactly one entry
// for the submit_assessment tool with input schema fields: quality (enum),
// summary (string), gaps (array of strings), and questions (array of
// objects with id, text, context, options, required).
//
// Each call returns a new slice to prevent callers from mutating the
// shared definition.
func AssessmentTools() []map[string]any {
	return []map[string]any{assessmentToolDef()}
}

// RefinementTools returns a slice containing the Anthropic tool definitions
// for the refinement phase. The returned slice contains exactly two entries:
// submit_prd_update (with updated_prd string field) at index 0 and
// submit_assessment (same schema as AssessmentTools) at index 1.
//
// Each call returns a new slice to prevent callers from mutating the
// shared definition.
func RefinementTools() []map[string]any {
	return []map[string]any{
		{
			"name":        "submit_prd_update",
			"description": "Submit an updated PRD document.",
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"updated_prd": map[string]any{
						"type":        "string",
						"description": "The full updated PRD content.",
					},
				},
				"required": []any{"updated_prd"},
			},
		},
		assessmentToolDef(),
	}
}

// ArtifactTool returns a slice containing the Anthropic tool definition for
// submitting a specific artifact type. It retrieves the JSON Schema for the
// artifact from afspec.Schemas(), resolves all $ref references via
// InlineRefs, strips metadata via CleanSchema, and embeds the cleaned
// schema as the input_schema of the submit_{artifactName} tool.
//
// The schema computation (InlineRefs + CleanSchema) runs exactly once per
// artifact type per process lifetime. Every call returns a fresh deep copy
// of the cached result so that callers cannot corrupt the shared cache.
//
// Returns an empty slice without panicking if artifactName is not one of
// requirements, test_spec, or tasks, or if Schemas() does not contain an
// entry for the requested artifact.
func ArtifactTool(artifactName string) []map[string]any {
	entry, ok := artifactEntries[artifactName]
	if !ok {
		return []map[string]any{}
	}

	// Compute the cleaned schema exactly once, storing it in entry.schema.
	entry.once.Do(func() {
		schemaKey := artifactName + ".v1.json"
		schemas := afspec.Schemas()
		schemaBytes, schemaOk := schemas[schemaKey]
		if !schemaOk {
			// entry.schema remains nil; callers below will return an empty slice.
			return
		}

		var schemaMap map[string]any
		if err := json.Unmarshal(schemaBytes, &schemaMap); err != nil {
			return
		}

		// Resolve all $ref references inline, then strip metadata.
		entry.schema = CleanSchema(InlineRefs(schemaMap))
	})

	if entry.schema == nil {
		return []map[string]any{}
	}

	// Return a fresh deep copy so callers cannot mutate the cached schema.
	toolName := "submit_" + artifactName
	return []map[string]any{
		{
			"name":         toolName,
			"description":  "Submit the " + artifactName + " artifact.",
			"input_schema": deepCopyJSONMap(entry.schema),
		},
	}
}
