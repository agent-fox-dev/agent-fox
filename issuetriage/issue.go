// Package issuetriage turns a problem report into a structured, evidence-cited
// GitHub issue.
//
// It is the `af-issue` skill rebuilt as a program. The skill is ~450 lines of
// markdown handed to a coding CLI: a table for classifying the argument, a
// step for detecting the repository, an issue-body template as a fenced
// block, a labels menu, a `gh issue create` invocation, and a Guardrails
// section asking the CLI not to use the write tools it has. Every one of
// those is a mechanism here, and only the part that genuinely needs a model —
// read the code and work out why — is left as a prompt.
//
// The parts that are mechanisms cannot be talked out of:
//
//   - The read-only mandate is the resolved tool set, not a sentence.
//   - The issue template is a JSON Schema, so a missing severity is a
//     validation error the model repairs rather than a slightly worse
//     document.
//   - "Cite real files" is a check against the workspace, and a refusal comes
//     back to the model as a repairable error result.
//   - Filing the issue happens in Go, after the run. There is no create_issue
//     tool and no network tool of any kind, so no sequence of model outputs
//     can cause this package to write to GitHub.
package issuetriage

import (
	"fmt"
	"strings"

	"github.com/agentfox/agentkit-go/schema"
)

// Issue is the triage result: the af-issue issue template as a Go type.
//
// The skill states that template as a fenced markdown block and asks the
// model to fill it in, which is the one thing a prompt cannot check. Here the
// shape is a schema, so a missing field is a validation error handed back to
// the model, and the markdown is rendered from fields that already validated
// — the model never writes the document, only its contents.
type Issue struct {
	Title              string    `json:"title"`
	Problem            string    `json:"problem"`
	Reproduction       string    `json:"reproduction"`
	Confidence         string    `json:"confidence"`
	RootCause          string    `json:"root_cause"`
	RelatedInstances   []string  `json:"related_instances,omitempty"`
	AffectedFiles      []FileRef `json:"affected_files"`
	Fix                Fix       `json:"suggested_fix"`
	AcceptanceCriteria []string  `json:"acceptance_criteria"`
	Severity           string    `json:"severity"`
	SeverityRationale  string    `json:"severity_rationale"`
}

// FileRef is a path plus what that path has to do with the issue. The pair is
// the unit the skill asks for ("`path/to/file.py` — {role in the issue}"),
// and keeping it a struct rather than one preformatted string is what lets
// the tool handler check Path against the workspace without parsing prose.
type FileRef struct {
	Path string `json:"path"`
	Role string `json:"role"`
}

// Fix is the suggested repair. It is deliberately a proposal and not a patch:
// this package does not change code, and a diff written by a model that
// cannot run the tests is worth less than a clear description of what to
// change and why.
type Fix struct {
	Approach string    `json:"approach"`
	Files    []FileRef `json:"files"`
	Risks    string    `json:"risks"`
}

// Confidence and severity vocabularies, in the order the skill defines them.
// They are declared once and used twice — in the schema as an enum and in the
// prompt as the criteria table — so the two cannot drift apart.
var (
	ConfidenceLevels = []string{"Confirmed", "Probable", "Suspected"}
	SeverityLevels   = []string{"Critical", "High", "Medium", "Low"}
)

// issueSchema is the contract.
//
// Note what is required and what is optional: related_instances is genuinely
// optional, but severity is not — an issue without one is not triaged, and
// leaving it optional would let the model skip the judgement call the whole
// run exists to make.
//
// The descriptions are not documentation. They are the only instruction the
// model gets about each field at the moment it fills it in, which is later
// and closer to the decision than anything in the system prompt.
func issueSchema() *schema.Schema {
	fileRef := schema.Object(
		schema.Prop("path", schema.String("Repository-relative path, exactly as it exists on disk")),
		schema.Prop("role", schema.String("One line: this file's part in the issue")),
	)
	return schema.Object(
		schema.Prop("title", schema.String(
			"Under 80 characters, '{component}: {defect}'. Name the defect, not the symptom.")),
		schema.Prop("problem", schema.String(
			"1-3 sentences: the observable symptom and the conditions it occurs under")),
		schema.Prop("reproduction", schema.String(
			"Steps or conditions to reproduce. If none can be established from the input, say "+
				"'Observed from error output; manual reproduction steps not established.'")),
		schema.Prop("confidence", schema.Enum(
			"Confirmed: you traced the exact path and can point at the line. "+
				"Probable: mechanism identified, trigger unverified. "+
				"Suspected: a hypothesis consistent with the evidence.", ConfidenceLevels...)),
		schema.Prop("root_cause", schema.String(
			"Why it happens, citing files, functions and line ranges. Trace trigger to fault. "+
				"Explain the mechanism, not the symptom.")),
		schema.Opt("related_instances", schema.Array(schema.String(),
			"Other places in the codebase with the same bug class. Omit if none.")),
		schema.Prop("affected_files", schema.Array(fileRef,
			"Every file involved in the root cause. Each path is checked against the workspace.").MinItemsN(1)),
		schema.Prop("suggested_fix", schema.Object(
			schema.Prop("approach", schema.String(
				"What to change, where, and why it addresses the root cause")),
			schema.Prop("files", schema.Array(fileRef,
				"Files to modify; role is what changes in each").MinItemsN(1)),
			schema.Prop("risks", schema.String("Risks and trade-offs, or 'None identified'")),
		)),
		schema.Prop("acceptance_criteria", schema.Array(
			schema.String("Given {precondition}, when {action}, then {outcome}"),
			"Testable conditions the fix must satisfy").MinItemsN(1)),
		schema.Prop("severity", schema.Enum(
			"Critical: data loss, security, a crash in production, blocks everyone. "+
				"High: core functionality broken, no workaround. "+
				"Medium: impaired, a workaround exists. "+
				"Low: cosmetic, or an edge case unlikely in normal use.", SeverityLevels...)),
		schema.Prop("severity_rationale", schema.String("One line justifying the severity")),
	)
}

// Render produces the issue body.
//
// It is a pure function of the validated struct, which is why two runs that
// reach the same diagnosis produce byte-identical documents — a property no
// amount of "follow this template" buys, and the reason a golden test of this
// package can exist at all.
func (i Issue) Render(sourceKind, origin string) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	p("## Problem\n\n%s\n\n", block(i.Problem))
	p("## Reproduction\n\n%s\n\n", block(i.Reproduction))

	p("## Root Cause Analysis\n\n**Confidence:** %s\n\n%s\n\n", i.Confidence, block(i.RootCause))
	p("### Related Instances\n\n")
	if len(i.RelatedInstances) == 0 {
		p("No related instances found.\n\n")
	} else {
		for _, r := range i.RelatedInstances {
			p("- %s\n", strings.TrimSpace(r))
		}
		p("\n")
	}

	p("## Affected Files\n\n%s\n", fileList(i.AffectedFiles))
	p("\n## Suggested Fix\n\n**Approach:**\n\n%s\n\n", block(i.Fix.Approach))
	p("**Files to modify:**\n\n%s\n", fileList(i.Fix.Files))
	p("\n**Risks:**\n\n%s\n\n", block(i.Fix.Risks))

	p("## Acceptance Criteria\n\n")
	for n, ac := range i.AcceptanceCriteria {
		p("- **AC-%d:** %s\n", n+1, strings.TrimSpace(ac))
	}
	p("\n## Severity\n\n**%s** — %s\n\n", i.Severity, strings.TrimSpace(i.SeverityRationale))

	p("---\n*Triaged by [`issue`](https://github.com/agent-fox-dev/agent-fox) from %s: %s.*\n", sourceKind, origin)
	return b.String()
}

func fileList(refs []FileRef) string {
	lines := make([]string, 0, len(refs))
	for _, f := range refs {
		lines = append(lines, fmt.Sprintf("- `%s` — %s", f.Path, strings.TrimSpace(f.Role)))
	}
	if len(lines) == 0 {
		return "_none_"
	}
	return strings.Join(lines, "\n")
}

// block normalizes model prose to a single trailing newline's worth of
// whitespace, so a rendered document does not inherit whichever spacing the
// model happened to emit.
func block(s string) string { return strings.TrimSpace(s) }
