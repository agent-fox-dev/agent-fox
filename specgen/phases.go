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
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
)

// ToolSubmitArchitecture is the terminating tool of the optional
// architecture phase.
const ToolSubmitArchitecture = "submit_architecture"

// Per-phase output caps, clamped to the model's own ceiling by the catalog.
//
// They differ by an order of magnitude because the artifacts do: a PRD is a
// document, and a tasks artifact for a ten-requirement spec is a large JSON
// object whose truncation is a validation failure rather than a shorter
// answer.
const (
	prdMaxTokens          = 32768
	generateMaxTokens     = 65536
	architectureMaxTokens = 32768
)

// specTemperature is low because every phase produces a structured artifact
// that is then validated: creativity here shows up as a schema violation. A
// model whose catalog row rejects sampling parameters has them dropped by its
// provider rather than by a branch here.
const specTemperature = 0.2

// author is the model-driven half of this package: the four steps where
// judgment is required and nothing else.
//
// Keeping it an interface is what makes the split checkable — a test swaps in
// a scripted provider and the pipeline does not notice.
type author interface {
	WritePRD(context.Context, prdRequest) (PRD, agentrun.Result, error)
	GenerateArtifact(context.Context, artifactRequest) (map[string]any, agentrun.Result, error)
	WriteArchitecture(context.Context, architectureRequest) (string, agentrun.Result, error)
}

type prdRequest struct {
	Root         string
	SourceKind   string
	SourceOrigin string
	Input        string
	Profile      Profile
	Landscape    []afspec.SpecMeta
}

type artifactRequest struct {
	Step      afspec.GenerationStep
	SpecID    string
	SpecName  string
	Root      string
	PRD       string
	Profile   Profile
	Landscape []afspec.SpecMeta
	// Partial carries the artifacts produced so far. The submit handler
	// validates against it and writes the accepted artifact back into it.
	Partial *afspec.PartialSpec
}

type architectureRequest struct {
	SpecID   string
	SpecName string
	Root     string
	PRD      string
	Partial  *afspec.PartialSpec
}

// agentAuthor runs the phases against the configured model.
type agentAuthor struct {
	runner *agentrun.Runner
}

func (a *agentAuthor) WritePRD(ctx context.Context, req prdRequest) (PRD, agentrun.Result, error) {
	var sink prdSink
	res, err := a.runner.Run(ctx, agentrun.Phase{
		Name:   "prd",
		System: prdSystemPrompt(),
		User: prdUserPrompt(req.Root, req.SourceKind, req.SourceOrigin, req.Input,
			req.Profile.LanguageBlock(), landscapeBlock(req.Landscape)),
		Terminator:         ToolSubmitPRD,
		Custom:             []core.Tool{submitPRDTool(&sink)},
		BuiltinTools:       agentrun.ReadOnlyFileTools,
		ReadOnly:           true,
		MaxTokens:          prdMaxTokens,
		Temperature:        specTemperature,
		LoadProjectContext: true,
	})
	if err != nil {
		return PRD{}, res, err
	}
	prd, ok := sink.get()
	if !ok {
		return PRD{}, res, agentrun.NoResultError("prd", ToolSubmitPRD, res)
	}
	return prd, res, nil
}

func (a *agentAuthor) GenerateArtifact(ctx context.Context, req artifactRequest) (map[string]any, agentrun.Result, error) {
	toolSchema, err := ArtifactSchema(req.Step)
	if err != nil {
		return nil, agentrun.Result{Name: string(req.Step)}, err
	}
	var audit func(map[string]any) error
	if req.Step == afspec.StepTasks {
		audit = req.Profile.AuditTasks
	}

	var sink artifactSink
	name := ArtifactToolName(req.Step)
	res, err := a.runner.Run(ctx, agentrun.Phase{
		Name:   "generate:" + string(req.Step),
		System: generationSystemPrompt(),
		User: generationUserPrompt(req.Step, req.SpecID, req.SpecName, req.Root, req.PRD,
			landscapeBlock(req.Landscape),
			priorArtifactsBlock(*req.Partial, req.Step),
			req.Profile.LanguageBlock()),
		Terminator:         name,
		Custom:             []core.Tool{submitArtifactTool(req.Step, toolSchema, req.Partial, &sink, audit)},
		BuiltinTools:       agentrun.ReadOnlyFileTools,
		ReadOnly:           true,
		MaxTokens:          generateMaxTokens,
		Temperature:        specTemperature,
		LoadProjectContext: true,
	})
	if err != nil {
		return nil, res, err
	}
	content, ok := sink.get()
	if !ok {
		return nil, res, agentrun.NoResultError(string(req.Step), name, res)
	}
	return content, res, nil
}

func (a *agentAuthor) WriteArchitecture(ctx context.Context, req architectureRequest) (string, agentrun.Result, error) {
	var sink textSink
	user := fill(template("architecture_user.md"), map[string]string{
		"spec_id":     req.SpecID,
		"spec_name":   req.SpecName,
		"root":        req.Root,
		"prd":         strings.TrimSpace(req.PRD),
		"prior_block": priorArtifactsBlock(*req.Partial, afspec.StepTasks) + renderTasks(req.Partial),
	})
	res, err := a.runner.Run(ctx, agentrun.Phase{
		Name:               "architecture",
		System:             generationSystemPrompt(),
		User:               user,
		Terminator:         ToolSubmitArchitecture,
		Custom:             []core.Tool{submitArchitectureTool(&sink)},
		BuiltinTools:       agentrun.ReadOnlyFileTools,
		ReadOnly:           true,
		MaxTokens:          architectureMaxTokens,
		Temperature:        specTemperature,
		LoadProjectContext: true,
	})
	if err != nil {
		return "", res, err
	}
	doc, ok := sink.get()
	if !ok {
		return "", res, agentrun.NoResultError("architecture", ToolSubmitArchitecture, res)
	}
	return doc, res, nil
}

// renderTasks appends the tasks artifact, which priorArtifactsBlock does not
// emit for any generation step because no step follows it.
func renderTasks(partial *afspec.PartialSpec) string {
	if partial == nil || partial.Tasks == nil {
		return ""
	}
	raw, err := afspec.MarshalJSON(partial.Tasks)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("\n## tasks.json\n\n```json\n%s\n```\n", strings.TrimSpace(string(raw)))
}

// textSink is the guarded destination of a phase whose result is one document.
type textSink struct {
	mu   sync.Mutex
	val  string
	done bool
}

func (t *textSink) set(v string) {
	t.mu.Lock()
	t.val, t.done = v, true
	t.mu.Unlock()
}

func (t *textSink) get() (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.val, t.done
}

func submitArchitectureTool(sink *textSink) core.Tool {
	return core.Tool{
		Name:        ToolSubmitArchitecture,
		Description: "Submit the architecture document and end this phase. Call this once.",
		InputSchema: schema.Object(
			schema.Prop("document", schema.String(
				"The complete architecture document in Markdown, starting with a level-1 heading.")),
		),
		ConstrainedSampling: constrainedJSON,
		PromptGuidelines: []string{
			"Submit the document by calling " + ToolSubmitArchitecture + "; do not write it as prose.",
		},
		Execute: func(_ context.Context, in json.RawMessage) core.ToolResult {
			var payload struct {
				Document string `json:"document"`
			}
			if err := json.Unmarshal(in, &payload); err != nil {
				return core.ErrResult("invalid_arguments", err.Error())
			}
			if strings.TrimSpace(payload.Document) == "" {
				return core.ErrResult("empty_document",
					"document is empty. Submit the complete architecture document.")
			}
			sink.set(payload.Document)
			res := core.OKResult(map[string]any{"accepted": true})
			res.Terminate = true
			return res
		},
	}
}
