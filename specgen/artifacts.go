package specgen

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

// artifactSchemas caches the converted tool schema for each generation step.
// The conversion walks the whole document and is pure, so it runs once per
// step per process; sync.Map rather than a plain map because the cache is
// read from whatever goroutine a phase runs on.
var artifactSchemas sync.Map // afspec.GenerationStep -> *artifactSchemaEntry

type artifactSchemaEntry struct {
	once   sync.Once
	schema *schema.Schema
	uri    string
	err    error
}

// schemaProperty is the artifact's own `$schema` field: the URI of the schema
// the file is validated against.
//
// It is the one property of a v2 artifact the model is never asked for. A
// tool schema cannot even declare a property by that name — the vendors
// restrict property names to `^[a-zA-Z0-9_.-]{1,64}$`, and Anthropic rejects
// the whole request over it — and asking a model to reproduce a URL from
// memory was never the right shape anyway. The converter drops it and the
// submit handler writes it, from the schema document's own `$id`, so the
// value cannot drift from the schema it names.
const schemaProperty = "$schema"

// ArtifactSchema returns the tool schema for one generation step, converted
// from the JSON Schema afspec embeds.
//
// The schema is not re-authored here. afspec/schemas/*.json is the format's
// definition and the same bytes validate the artifact after generation; a
// second, hand-written copy for the tool declaration would be a copy that can
// disagree with the thing it is supposed to describe.
func ArtifactSchema(step afspec.GenerationStep) (*schema.Schema, error) {
	v, _ := artifactSchemas.LoadOrStore(step, &artifactSchemaEntry{})
	entry := v.(*artifactSchemaEntry)
	entry.once.Do(func() {
		name, ok := afspec.SchemaNameForStep(step)
		if !ok {
			entry.err = fmt.Errorf("specgen: no schema for generation step %q", step)
			return
		}
		raw, ok := afspec.Schemas()[name]
		if !ok {
			entry.err = fmt.Errorf("specgen: schema %q is not embedded", name)
			return
		}
		s, dropped, err := ToolSchema(raw)
		if err != nil {
			entry.err = err
			return
		}
		// The only property this package can supply for the model is the
		// artifact's own $schema. Anything else the converter had to drop is
		// a field the model would be asked for by the format and never shown
		// by the tool — an unwinnable repair loop — so it is a refusal here,
		// in a message that names the property, rather than a run that fails
		// three phases later without saying why.
		for _, name := range dropped {
			if name != schemaProperty {
				entry.err = fmt.Errorf("specgen: the %s schema declares a property named %q, "+
					"which a tool schema cannot carry (the vendors require "+
					"^[a-zA-Z0-9_.-]{1,64}$) and this package has no value for", step, name)
				return
			}
		}
		uri := schemaID(raw)
		if uri == "" && len(dropped) > 0 {
			entry.err = fmt.Errorf("specgen: the %s schema has no $id, so the %s field of the "+
				"artifact cannot be filled in", step, schemaProperty)
			return
		}
		entry.schema, entry.uri = s, uri
	})
	return entry.schema, entry.err
}

// artifactSchemaURI is the value the submit handler writes into the
// artifact's $schema field: the $id of the schema it will be validated
// against, read from the same embedded document.
func artifactSchemaURI(step afspec.GenerationStep) string {
	if _, err := ArtifactSchema(step); err != nil {
		return ""
	}
	v, _ := artifactSchemas.Load(step)
	return v.(*artifactSchemaEntry).uri
}

// schemaID reads a schema document's $id.
func schemaID(raw []byte) string {
	var doc struct {
		ID string `json:"$id"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	return doc.ID
}

// artifactSink is the guarded destination one generation step writes into.
type artifactSink struct {
	mu      sync.Mutex
	content map[string]any
	done    bool
}

func (a *artifactSink) set(c map[string]any) {
	a.mu.Lock()
	a.content, a.done = c, true
	a.mu.Unlock()
}

func (a *artifactSink) get() (map[string]any, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.content, a.done
}

// submitArtifactTool builds the terminating tool of one generation step.
//
// This is where the repair loop lives. The alternative — call the model, pull
// the tool_use block out of the response, validate it, and on a failure
// assemble a second conversation by hand out of the original prompt, the
// model's message and a synthetic tool_result — is the shape this replaces.
// Here the validation runs inside the handler and a violation is returned as
// a tool error. The loop appends it to the transcript the model is already
// holding and asks again, which is the same conversation the hand-assembled
// one was imitating, with two differences that matter: the system prompt and
// the tool schemas are byte-identical across the attempt and its correction,
// so the provider's cache prefix survives; and "how many attempts does the
// model get" and "how many turns may this phase take" became one number.
func submitArtifactTool(step afspec.GenerationStep, s *schema.Schema,
	partial *afspec.PartialSpec, sink *artifactSink, audit func(map[string]any) error) core.Tool {

	name := ArtifactToolName(step)
	return core.Tool{
		Name: name,
		Description: fmt.Sprintf("Submit the complete %s artifact and end this phase. "+
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
			// The artifact's $schema is a fact about the format rather than a
			// judgement about this spec, so it is written here rather than
			// asked for: the model is not shown the field, and what it would
			// have typed cannot be wrong.
			if uri := artifactSchemaURI(step); uri != "" {
				if content == nil {
					content = map[string]any{}
				}
				content[schemaProperty] = uri
			}
			if err := validateArtifactContent(content, step, partial); err != nil {
				return core.ErrResult("validation_failed", err.Error())
			}
			if audit != nil {
				if err := audit(content); err != nil {
					return core.ErrResult("project_mismatch", err.Error())
				}
			}
			sink.set(content)
			res := core.OKResult(map[string]any{"accepted": true, "artifact": string(step)})
			res.Terminate = true
			return res
		},
	}
}

// validateArtifactContent decodes a model's arguments for one generation
// step, runs the artifact's v2 schema and every cross-file rule decidable at
// that point, and records the typed artifact in partial on success.
//
// A step that fails leaves partial untouched, so the next attempt validates
// against the same upstream artifacts as the one before it.
func validateArtifactContent(content map[string]any, step afspec.GenerationStep, partial *afspec.PartialSpec) error {
	decoded, err := afspec.DecodeArtifact(step, content)
	if err != nil {
		return err
	}

	// Validate a COPY of the accumulated state, so a rejected artifact cannot
	// be left behind for the next attempt to validate against.
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
		return fmt.Errorf("the %s artifact has %d validation error(s):\n%s",
			step, len(result.Errors),
			strings.TrimRight(afspec.FormatValidationEntries(result.Errors), "\n"))
	}

	*partial = candidate
	return nil
}
