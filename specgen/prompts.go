package specgen

import (
	"embed"
	"fmt"
	"strings"

	"github.com/agent-fox-dev/agentfox/afspec"
)

// templates are the prompts, kept as Markdown files rather than as Go string
// literals.
//
// They are prose a requirements engineer maintains, and a diff against a
// prompt is much easier to read as a diff against a document than as a diff
// against escaped string concatenation. They are embedded at compile time, so
// a binary still carries everything it needs.
//
//go:embed templates/*.md
var templates embed.FS

func template(name string) string {
	b, err := templates.ReadFile("templates/" + name)
	if err != nil {
		// The files are embedded at compile time; a missing one is a build
		// mistake, not a runtime condition, and failing loudly here beats
		// sending a prompt with a hole in it.
		panic("specgen: missing embedded template " + name + ": " + err.Error())
	}
	return string(b)
}

// fill substitutes {{name}} placeholders. It is deliberately not text/template:
// the values are prose that may itself contain braces, actions and pipes, and
// a templating language that interprets the DATA is a way to turn a PRD
// containing "{{" into a run-time error.
func fill(tmpl string, vars map[string]string) string {
	pairs := make([]string, 0, len(vars)*2)
	for k, v := range vars {
		pairs = append(pairs, "{{"+k+"}}", v)
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

// prdSystemPrompt is the PRD phase's mandate.
func prdSystemPrompt() string { return template("prd_system.md") }

// prdUserPrompt is the PRD phase's task.
//
// splitBlock is empty for an undivided input. For one scope of a split it
// carries the decided plan, so the same phase writes a follow-on PRD without
// a second template.
func prdUserPrompt(root, sourceKind, sourceOrigin, input, projectBlock, landscapeBlock, splitBlock string) string {
	return fill(template("prd_user.md"), map[string]string{
		"root":            root,
		"source_kind":     sourceKind,
		"source_origin":   sourceOrigin,
		"input":           strings.TrimSpace(input),
		"project_block":   projectBlock,
		"landscape_block": landscapeBlock,
		"split_block":     splitBlock,
	})
}

// generationSystemPrompt is shared by all three generation steps. It is one
// string rather than three because the format's rules are the same for all of
// them, and because a shared system prompt is a shared provider cache prefix.
func generationSystemPrompt() string { return template("generation_system.md") }

// generationUserPrompt assembles one generation step's task.
func generationUserPrompt(step afspec.GenerationStep, specID, specName, root, prd string,
	landscapeBlock, priorBlock, languageBlock string) string {

	base := fill(template("generation_user_base.md"), map[string]string{
		"artifact":        string(step),
		"spec_id":         specID,
		"spec_name":       specName,
		"root":            root,
		"prd":             strings.TrimSpace(prd),
		"landscape_block": landscapeBlock,
		"prior_block":     priorBlock,
		"language_block":  languageBlock,
	})
	return base + "\n" + stepInstructions(step)
}

func stepInstructions(step afspec.GenerationStep) string {
	switch step {
	case afspec.StepRequirements:
		return template("generation_user_requirements.md")
	case afspec.StepTestSpec:
		return template("generation_user_test_spec.md")
	case afspec.StepTasks:
		return template("generation_user_tasks.md")
	default:
		return ""
	}
}

// priorArtifactsBlock renders the artifacts an earlier step produced.
//
// The order is the format's rule and the reason is concrete: a generator that
// cannot see a test id cannot own it, which is how the previous format
// produced specs whose edge-case and property tests belonged to no task at
// all. Each step therefore sees the complete artifacts before it, in full,
// rather than a summary.
func priorArtifactsBlock(partial afspec.PartialSpec, step afspec.GenerationStep) string {
	var b strings.Builder
	render := func(title string, v any) {
		if v == nil {
			return
		}
		raw, err := afspec.MarshalJSON(v)
		if err != nil {
			return
		}
		fmt.Fprintf(&b, "\n## %s (already generated — use these IDs, do not invent one)\n\n```json\n%s\n```\n",
			title, strings.TrimSpace(string(raw)))
	}
	if step == afspec.StepRequirements {
		return ""
	}
	if partial.Requirements != nil {
		render("requirements.json", partial.Requirements)
	}
	if step == afspec.StepTasks && partial.TestSpec != nil {
		render("test_spec.json", partial.TestSpec)
	}
	return b.String()
}

// landscapeBlock lists the specs that already exist.
//
// Without it a spec is written as though the repository had none, which is
// how two specs end up owning the same behaviour under different names. It is
// metadata only — a name and a status — because the full text of every spec is
// more than a prompt can carry and more than this decision needs.
func landscapeBlock(metas []afspec.SpecMeta) string {
	if len(metas) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Existing specs in this repository\n\n")
	b.WriteString("| Spec | Status |\n|---|---|\n")
	for _, m := range metas {
		fmt.Fprintf(&b, "| `%s_%s` | %s |\n", m.SpecID, m.SpecName, m.Status)
	}
	b.WriteString("\nDo not restate what an existing spec already covers. If this work depends on " +
		"one, name it in the PRD's `## Dependencies` section with a reason, and the tasks " +
		"artifact's `dependencies` array will carry it.\n")
	return b.String()
}
