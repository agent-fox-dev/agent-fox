package specgen

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/schema"
)

// ToolSubmitPRD is the terminating tool of the PRD phase.
const ToolSubmitPRD = "submit_prd"

// specNameRE is the format's spec-name rule. It is checked in the tool
// handler as well as by afspec afterwards, because a name the model can
// correct on the next turn is better than a spec that fails to validate after
// three more phases have been paid for.
var specNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// PRD is the first phase's result.
type PRD struct {
	// SpecName is the snake_case half of the directory name.
	SpecName string `json:"spec_name"`
	// Title is the human-readable name, which becomes the PRD frontmatter's
	// title. The schema requires it to be non-empty, and a spec whose title
	// is blank fails validation — which is why it is asked for here rather
	// than derived from the directory name.
	Title string `json:"title"`
	// Body is the finished PRD in Markdown, without frontmatter.
	Body string `json:"body"`
	// OpenQuestions are the decisions the model would most like checked.
	OpenQuestions []OpenQuestion `json:"open_questions,omitempty"`
	// RecommendedSplit is set when the input describes more than one spec's
	// worth of work. The PRD then covers the first scope only.
	RecommendedSplit []SplitScope `json:"recommended_split,omitempty"`
}

// OpenQuestion is one decision made under uncertainty.
//
// It is the shape the "self-resolve, flag low confidence" contract takes: the
// question was answered, the answer is in the PRD, and this records that a
// different answer would have produced different work. A caller that wants a
// human in the loop reads this array; a caller that does not still gets a
// finished spec.
type OpenQuestion struct {
	Question string `json:"question"`
	Decision string `json:"decision"`
	Why      string `json:"why_unsure"`
}

// SplitScope is one spec's worth of the work, when the input held several.
type SplitScope struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
}

// prdSchema is the PRD phase's contract.
func prdSchema() *schema.Schema {
	return schema.Object(
		schema.Prop("spec_name", schema.String(
			"snake_case name for this spec, matching [a-z][a-z0-9_]*. Short and descriptive: "+
				"widget_counter, not the_new_widget_counting_feature.")),
		schema.Prop("title", schema.String(
			"Human-readable title for the PRD frontmatter. Non-empty.")),
		schema.Prop("body", schema.String(
			"The complete PRD in Markdown, WITHOUT YAML frontmatter. It must contain a "+
				"'## Intent' section — the spec cannot be activated without one — plus "+
				"'## Goals', '## Non-goals', '## Background', '## Requirements' and "+
				"'## Design Decisions'.")),
		schema.Opt("open_questions", schema.Array(schema.Object(
			schema.Prop("question", schema.String("The question the input left open")),
			schema.Prop("decision", schema.String("The answer you went with, as it appears in the PRD")),
			schema.Prop("why_unsure", schema.String("Why a reviewer should check this one")),
		), "Decisions you made where a different answer would change the shape of the work. "+
			"Omit when the input and the codebase settled everything.")),
		schema.Opt("recommended_split", schema.Array(schema.Object(
			schema.Prop("name", schema.String("snake_case name for this scope")),
			schema.Prop("scope", schema.String("What this spec would cover, in one or two sentences")),
		), "Set ONLY when the input is more than one spec's worth of work. List every scope, "+
			"first one first — the PRD you submit covers that first scope alone.")),
	)
}

// prdSink is the guarded destination the tool handler writes into.
type prdSink struct {
	mu   sync.Mutex
	val  PRD
	done bool
}

func (p *prdSink) set(v PRD) {
	p.mu.Lock()
	p.val, p.done = v, true
	p.mu.Unlock()
}

func (p *prdSink) get() (PRD, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.val, p.done
}

// submitPRDTool is the terminator of the PRD phase.
//
// The three checks it makes are the three that would otherwise fail later and
// more expensively: an unusable spec name fails afspec's C1 rule after three
// more phases, an empty title fails the frontmatter schema, and a body with
// no `## Intent` section cannot be activated at all. Each is repairable on
// the next turn, so each is an error result rather than a Go error.
func submitPRDTool(sink *prdSink) core.Tool {
	return core.Tool{
		Name: ToolSubmitPRD,
		Description: "Submit the finished PRD and end this phase. Call this once, after you " +
			"have read the code and resolved every open question.",
		InputSchema:         prdSchema(),
		ConstrainedSampling: constrainedJSON,
		PromptGuidelines: []string{
			"Submit the PRD by calling " + ToolSubmitPRD + "; do not write it as prose.",
			"The body must contain a '## Intent' section, or the spec cannot be activated.",
		},
		Execute: func(_ context.Context, in json.RawMessage) core.ToolResult {
			var prd PRD
			if err := json.Unmarshal(in, &prd); err != nil {
				return core.ErrResult("invalid_arguments", err.Error())
			}
			prd.SpecName = strings.TrimSpace(prd.SpecName)
			prd.Title = strings.TrimSpace(prd.Title)

			if !specNameRE.MatchString(prd.SpecName) {
				return core.ErrResult("invalid_spec_name", fmt.Sprintf(
					"spec_name %q must match [a-z][a-z0-9_]*: lowercase, starting with a letter, "+
						"words joined by underscores.", prd.SpecName))
			}
			if prd.Title == "" {
				return core.ErrResult("empty_title",
					"title is empty, and the PRD frontmatter schema requires a non-empty one.")
			}
			if !HasIntent(prd.Body) {
				return core.ErrResult("missing_intent",
					"the body has no '## Intent' section. It is required: the spec's intent hash "+
						"is computed from it, and a spec without one cannot be activated. Add it "+
						"as the first section, one short paragraph stating what this spec is for.")
			}
			if strings.Contains(prd.Body, "---\n") && strings.HasPrefix(strings.TrimSpace(prd.Body), "---") {
				return core.ErrResult("frontmatter_in_body",
					"the body starts with YAML frontmatter. Submit the Markdown body only — the "+
						"frontmatter is written by the tool.")
			}

			sink.set(prd)
			res := core.OKResult(map[string]any{"accepted": true, "spec_name": prd.SpecName})
			res.Terminate = true
			return res
		},
	}
}

// intentRE matches the `## Intent` heading, in the spellings a model
// plausibly emits.
var intentRE = regexp.MustCompile(`(?mi)^##\s+Intent\s*$`)

// HasIntent reports whether a PRD body carries the section afspec hashes.
func HasIntent(body string) bool { return intentRE.MatchString(body) }

// ValidSpecName reports whether s is a usable spec name. It is exported so a
// command can refuse a bad --name before anything is fetched.
func ValidSpecName(s string) bool { return specNameRE.MatchString(s) }
