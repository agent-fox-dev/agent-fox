package conform

import (
	"fmt"
	"strings"
)

// Where an unmet item came from.
const (
	// SourceDeclared is a deviation the work itself declared: a task, or the
	// phase that resolves the review's findings.
	SourceDeclared = "declared"
	// SourceReview is a shortfall the independent review found.
	SourceReview = "review"
	// SourceStructural is a structural finding left in the change.
	SourceStructural = "structural"
	// SourceVerification is the checks failing in a clean environment.
	SourceVerification = "verification"
	// SourceUnresolved is a blocker nothing resolved or declared.
	SourceUnresolved = "unresolved"
)

// Unmet is one thing the change does not do that the specification asks
// for, or one defect it ships knowingly. A non-empty list is what stops a
// pull request from saying the work is done.
type Unmet struct {
	Source      string `json:"source" trust:"fact" description:"Where it came from: declared, review, structural, verification or unresolved."`
	Requirement string `json:"requirement,omitempty" trust:"model" description:"The requirement or decision id concerned, when there is one."`
	Test        string `json:"test,omitempty" trust:"model" description:"The test id concerned, when there is one."`
	What        string `json:"what" trust:"model" description:"What is not met, and why."`
	// Tracking is where the item is followed up: an erratum in the change, or
	// an issue. Empty means nothing tracks it, and the report says so.
	Tracking string `json:"tracking,omitempty" trust:"fact" description:"The erratum in the change or the issue that tracks it. Empty when nothing does."`
}

// Deviation is a requirement the work knows it does not meet, declared by
// the phase that wrote the work rather than left in a note.
type Deviation struct {
	// Key is what it answers: a requirement id, a test id, D-n, or a
	// review finding's key.
	Key    string `json:"key" trust:"model" description:"The requirement id, test id, D-n or finding key the deviation answers."`
	Test   string `json:"test,omitempty" trust:"model" description:"The test id concerned, when the key is a requirement."`
	Reason string `json:"reason" trust:"model" description:"Why the requirement is not met: what the code does instead and what stops it."`
	// Errata is the erratum the change adds or edits for it.
	Errata string `json:"errata,omitempty" trust:"model" description:"The erratum file in this change that records it."`
}

// RenderUnmet is the block a pull request opens with when the list is not
// empty.
func RenderUnmet(items []Unmet) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## ⚠️ Unmet requirements\n\n")
	fmt.Fprintf(&b, "**This change does not fully meet its specification.** %d item(s) below are known "+
		"to be unmet; the documentation describes what the code does, not what the specification asked.\n\n",
		len(items))
	b.WriteString("| Requirement | Test | What | Tracked in |\n|---|---|---|---|\n")
	for _, u := range items {
		track := u.Tracking
		if track == "" {
			track = "**not tracked**"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", dash(u.Requirement), dash(u.Test), cell(u.What), cell(track))
	}
	b.WriteString("\n")
	return b.String()
}

// RenderReview is the review's tables, for a pull request.
func RenderReview(r Review, blockers []Blocker) string {
	var b strings.Builder
	b.WriteString("## Conformance review\n\n")
	b.WriteString("An independent phase read the spec, the diff and the repository — not the reports of " +
		"the phases that wrote the change — and answered for every id in scope.\n\n")
	if s := strings.TrimSpace(r.Summary); s != "" {
		b.WriteString("> " + strings.ReplaceAll(s, "\n", "\n> ") + "\n\n")
	}
	if len(r.Requirements) > 0 {
		b.WriteString("| Requirement | Status | Evidence |\n|---|---|---|\n")
		for _, row := range r.Requirements {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", row.ID, mark(row.Status, StatusImplemented), cell(row.Evidence))
		}
		b.WriteString("\n")
	}
	if len(r.Tests) > 0 {
		b.WriteString("| Test | Function | Assessment | Evidence |\n|---|---|---|---|\n")
		for _, row := range r.Tests {
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", row.ID, dash(cell(row.Test)),
				mark(row.Assessment, AssessAssertsContract), cell(row.Evidence))
		}
		b.WriteString("\n")
	}
	if len(r.Decisions) > 0 {
		b.WriteString("| Decision | Status | Evidence |\n|---|---|---|\n")
		for _, row := range r.Decisions {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", row.ID, mark(row.Status, DecisionFollowed), cell(row.Evidence))
		}
		b.WriteString("\n")
	}
	if len(r.Docs) > 0 {
		b.WriteString("**Documentation the code contradicts:**\n\n")
		for _, d := range r.Docs {
			fmt.Fprintf(&b, "- `%s` says %q; `%s` does otherwise: %s\n", d.Doc, strings.TrimSpace(d.Statement),
				d.Code, strings.TrimSpace(d.Actual))
		}
		b.WriteString("\n")
	}
	if len(blockers) > 0 {
		b.WriteString("**Blocking, and neither fixed nor declared:**\n\n")
		for _, x := range blockers {
			fmt.Fprintf(&b, "- ❌ %s\n", strings.TrimSpace(x.What))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// RenderFindings is the structural findings, for a pull request.
func RenderFindings(fs []Finding) string {
	if len(fs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Structural checks\n\n")
	b.WriteString("Run by the tool over the files this change touches, independently of the project's own " +
		"checks:\n\n")
	for _, f := range fs {
		fmt.Fprintf(&b, "- `%s` (%s): %s\n", f.Ref(), f.Check, f.Message)
	}
	b.WriteString("\n")
	return b.String()
}

func mark(v, good string) string {
	label := strings.ReplaceAll(v, "_", " ")
	if v == good {
		return "✅ " + label
	}
	return "❌ " + label
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// cell keeps a table cell on one line and its pipes out of the table.
func cell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.ReplaceAll(s, "|", "\\|")
}
