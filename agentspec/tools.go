package agentspec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/schema"

	"github.com/agent-fox-dev/agentfox/afspec"
)

// The tool a phase must call to produce its result.
const (
	ToolSubmitAssessment = "submit_assessment"
	ToolSubmitPRDUpdate  = "submit_prd_update"
)

// ArtifactToolName is the tool that submits one generation step's artifact.
func ArtifactToolName(step afspec.GenerationStep) string { return "submit_" + string(step) }

// constrainedJSON asks the wire to constrain sampling to the tool's schema
// where it can.
//
// StrictPrefer, not StrictRequire: constrained sampling is honoured on the
// OpenAI wires and ignored on Anthropic's, so it is a helpful narrowing and
// never the thing keeping the arguments well formed — the handler validates
// either way. StrictRequire would fail the whole request on an endpoint that
// cannot emit strict schemas, which is worse than an unconstrained call.
var constrainedJSON = &core.ConstrainedSampling{
	Type: core.ConstrainJSONSchema, Strict: core.StrictPrefer,
}

// assessmentSchema is the submit_assessment argument shape.
//
// It is authored with the combinators rather than converted from JSON,
// because unlike the artifact schemas it has no on-disk source of truth to
// drift from: this is the source of truth, and Assessment is what it decodes
// into.
func assessmentSchema() *schema.Schema {
	question := schema.Object(
		schema.Prop("id", schema.String("Unique identifier for the question.")),
		schema.Prop("text", schema.String("The question text.")),
		schema.Opt("context", schema.String("Context explaining why this question matters.")),
		schema.Opt("options", schema.Array(schema.String(), "Suggested answer options, if applicable.")),
		schema.Opt("required", schema.Bool("Whether an answer to this question is required.")),
	)
	return schema.Object(
		schema.Prop("quality", schema.Enum("Overall quality rating of the PRD.",
			"ready", "needs_refinement", "incomplete")),
		schema.Prop("summary", schema.String("Brief summary of the assessment.")),
		schema.Prop("gaps", schema.Array(schema.String(), "List of identified gaps in the PRD.")),
		schema.Prop("questions", schema.Array(question, "Clarifying questions for the PRD author.")),
	)
}

// submitAssessmentTool builds the terminating tool of the assess phase.
//
// The captured Assessment is where the result goes: there is no response
// parsing afterwards, because a tool call the loop validated and a handler
// decoded is already the typed value. dest is written only on success, so a
// rejected call leaves the previous state alone and the model's next attempt
// starts from the same place.
func submitAssessmentTool(dest *Assessment, done *bool) core.Tool {
	return core.Tool{
		Name: ToolSubmitAssessment,
		Description: "Submit the PRD quality assessment and end the run. " +
			"Call this once, after you have judged the PRD.",
		InputSchema:         assessmentSchema(),
		ConstrainedSampling: constrainedJSON,
		PromptGuidelines: []string{
			"Report the assessment by calling " + ToolSubmitAssessment + "; do not write it as prose.",
		},
		Execute: func(_ context.Context, in json.RawMessage) core.ToolResult {
			a, err := decodeAssessment(in)
			if err != nil {
				return core.ErrResult("invalid_assessment", err.Error())
			}
			*dest, *done = a, true
			res := core.OKResult(map[string]any{"accepted": true, "quality": a.Quality})
			res.Terminate = true
			return res
		},
	}
}

// submitPRDUpdateTool builds the terminating tool of the refine phase.
//
// Refinement produces two things and used to need both in one response, with
// a hand-written check that the model had emitted them together. Here the
// rewritten PRD and the fresh assessment are two fields of one call, so
// "both, or neither" is the schema rather than a rule enforced after the fact.
func submitPRDUpdateTool(prd *string, assessment *Assessment, done *bool) core.Tool {
	s := schema.Object(
		schema.Prop("updated_prd", schema.String("The full updated PRD content.")),
		schema.Prop("assessment", assessmentSchema().Describe(
			"A fresh assessment of the updated PRD.")),
	)
	return core.Tool{
		Name: ToolSubmitPRDUpdate,
		Description: "Submit the rewritten PRD together with a fresh assessment of it, " +
			"and end the run. Call this once.",
		InputSchema:         s,
		ConstrainedSampling: constrainedJSON,
		PromptGuidelines: []string{
			"Submit the rewritten PRD and its new assessment together in one " +
				ToolSubmitPRDUpdate + " call.",
		},
		Execute: func(_ context.Context, in json.RawMessage) core.ToolResult {
			var payload struct {
				UpdatedPRD string          `json:"updated_prd"`
				Assessment json.RawMessage `json:"assessment"`
			}
			if err := json.Unmarshal(in, &payload); err != nil {
				return core.ErrResult("invalid_arguments", err.Error())
			}
			if strings.TrimSpace(payload.UpdatedPRD) == "" {
				return core.ErrResult("empty_prd",
					"updated_prd is empty. Submit the complete rewritten PRD, not a diff or a summary.")
			}
			a, err := decodeAssessment(payload.Assessment)
			if err != nil {
				return core.ErrResult("invalid_assessment", err.Error())
			}
			*prd, *assessment, *done = payload.UpdatedPRD, a, true
			res := core.OKResult(map[string]any{"accepted": true, "quality": a.Quality})
			res.Terminate = true
			return res
		},
	}
}

// submitArtifactTool builds the terminating tool of one generation step.
//
// This is where the repair loop went. The old pipeline called the model,
// parsed the tool_use block out of the response, validated it, and — on a
// failure — assembled a second conversation by hand out of the original
// prompt, the model's message and a synthetic tool_result, up to three times.
// Here the validation runs inside the handler and a violation is returned as a
// tool error. The loop appends it to the transcript the model is already
// holding and asks again, which is the same conversation the hand-assembled
// one was imitating — except that the system prompt and the tool schemas stay
// byte-identical across attempts, so the provider's cache prefix survives the
// repair instead of being rebuilt each time.
func submitArtifactTool(step afspec.GenerationStep, s *schema.Schema, partial *afspec.PartialSpec, dest *map[string]any, done *bool) core.Tool {
	name := ArtifactToolName(step)
	return core.Tool{
		Name: name,
		Description: fmt.Sprintf("Submit the complete %s artifact and end the run. "+
			"It is validated against the format v2 schema and every cross-file rule "+
			"decidable at this point; a violation comes back to you as an error to fix.", step),
		InputSchema:         s,
		ConstrainedSampling: constrainedJSON,
		PromptGuidelines: []string{
			"Submit the " + string(step) + " artifact by calling " + name + "; do not write JSON as prose.",
		},
		Execute: func(_ context.Context, in json.RawMessage) core.ToolResult {
			var content map[string]any
			if err := json.Unmarshal(in, &content); err != nil {
				return core.ErrResult("invalid_json", fmt.Sprintf(
					"the %s arguments are not a JSON object: %v", step, err))
			}
			validated, err := validateArtifactContent(content, step, partial)
			if err != nil {
				return core.ErrResult("validation_failed", err.Error())
			}
			*dest, *done = validated, true
			res := core.OKResult(map[string]any{"accepted": true, "artifact": string(step)})
			res.Terminate = true
			return res
		},
	}
}

// artifactSchemas caches the converted tool schema for each generation step.
//
// The conversion walks the whole document and is pure, so it runs once per
// step per process. sync.Once rather than a plain map because the cache is
// read from whatever goroutine a phase runs on.
var artifactSchemas sync.Map // afspec.GenerationStep -> *artifactSchemaEntry

type artifactSchemaEntry struct {
	once   sync.Once
	schema *schema.Schema
	err    error
}

// ArtifactSchema returns the tool schema for one generation step, converted
// from the JSON Schema afspec embeds.
//
// The schema is not re-authored here. afspec/schemas/*.json is the format's
// definition and the same bytes validate the artifact after generation; a
// second hand-written copy for the tool declaration would be a copy that can
// disagree with the thing it is supposed to describe.
func ArtifactSchema(step afspec.GenerationStep) (*schema.Schema, error) {
	v, _ := artifactSchemas.LoadOrStore(step, &artifactSchemaEntry{})
	entry := v.(*artifactSchemaEntry)
	entry.once.Do(func() {
		name, ok := afspec.SchemaNameForStep(step)
		if !ok {
			entry.err = fmt.Errorf("agentspec: no schema for generation step %q", step)
			return
		}
		raw, ok := afspec.Schemas()[name]
		if !ok {
			entry.err = fmt.Errorf("agentspec: schema %q is not embedded", name)
			return
		}
		entry.schema, entry.err = ToolSchema(raw)
	})
	return entry.schema, entry.err
}

// decodeAssessment turns submit_assessment arguments into an Assessment.
//
// The quality enum is checked here as well as in the schema, because the
// schema is advice on the Anthropic wire and a rejection the model can read is
// worth more than a field silently set to something no consumer handles.
func decodeAssessment(in json.RawMessage) (Assessment, error) {
	if len(in) == 0 {
		return Assessment{}, fmt.Errorf("the assessment is missing")
	}
	var payload struct {
		Quality   string           `json:"quality"`
		Summary   string           `json:"summary"`
		Gaps      []string         `json:"gaps"`
		Questions []map[string]any `json:"questions"`
	}
	if err := json.Unmarshal(in, &payload); err != nil {
		return Assessment{}, fmt.Errorf("the assessment is not a JSON object: %v", err)
	}
	switch payload.Quality {
	case "ready", "needs_refinement", "incomplete":
	case "":
		return Assessment{}, fmt.Errorf("the assessment is missing the required 'quality' field")
	default:
		return Assessment{}, fmt.Errorf(
			"invalid assessment quality %q: must be one of [incomplete, needs_refinement, ready]",
			payload.Quality)
	}
	return Assessment{
		Quality:   payload.Quality,
		Summary:   payload.Summary,
		Gaps:      payload.Gaps,
		Questions: payload.Questions,
	}, nil
}

// validateArtifactContent decodes a model's arguments for one generation step,
// runs the artifact's v2 schema and every cross-file rule decidable at that
// point, and records the typed artifact in partial on success.
//
// A step that fails leaves partial untouched, so the next attempt validates
// against the same upstream artifacts as the one before it.
func validateArtifactContent(content map[string]any, step afspec.GenerationStep, partial *afspec.PartialSpec) (map[string]any, error) {
	decoded, err := afspec.DecodeArtifact(step, content)
	if err != nil {
		return nil, err
	}

	// Validate a copy of the accumulated state so that a failure cannot leave
	// a rejected artifact behind for the next attempt to validate against.
	candidate := *partial
	switch artifact := decoded.(type) {
	case *afspec.RequirementsV2Json:
		candidate.Requirements = artifact
	case *afspec.TestSpecV2Json:
		candidate.TestSpec = artifact
	case *afspec.TasksV2Json:
		candidate.Tasks = artifact
	}

	if result := afspec.ValidateGenerationStep(step, candidate); !result.Valid {
		return nil, formatValidationEntries(step, result.Errors)
	}

	*partial = candidate
	return content, nil
}

// formatValidationEntries turns validation errors into the tool error text the
// model reads. Each line names the rule that failed so the model can act on it
// rather than guess.
func formatValidationEntries(step afspec.GenerationStep, entries []afspec.ValidationEntry) error {
	return fmt.Errorf("the %s artifact has %d validation error(s):\n%s",
		step, len(entries), strings.TrimRight(afspec.FormatValidationEntries(entries), "\n"))
}
