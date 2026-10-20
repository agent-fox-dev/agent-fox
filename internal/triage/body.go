package triage

import (
	"fmt"
	"strings"
)

// The markers that make the body a contract: af reads them back to find the
// person's original report, and `af fix` reads the headings.
const (
	// markerPrefix and markerSuffix surround the run id on the first line of a
	// body this package rendered.
	markerPrefix = "<!-- agent-fox:triage v1 run="
	markerSuffix = " -->"
	// BeginMarker and EndMarker enclose the original report.
	BeginMarker = "<!-- agent-fox:original-report:begin -->"
	EndMarker   = "<!-- agent-fox:original-report:end -->"
	// originalHeading starts the section that holds the original report.
	originalHeading = "## Original report"
)

// WarnOriginalUnparsed is the warning of a body that starts with the triage
// marker and whose original-report markers cannot be paired.
const WarnOriginalUnparsed = "original_report_unparsed"

// Render is the issue body for the diagnosis d of run runID, with the original
// report kept verbatim between the original-report markers:
//
//	<!-- agent-fox:triage v1 run=<run_id> -->
//	## Diagnosis, ## Affected files, ## Suggested fix, ## Acceptance Criteria,
//	## Question (each left out when it is empty, the first always there)
//	<sub>…</sub>
//	## Original report, in a <details> block
//
// It is deterministic, and Extract inverts it: for any original, Extract of the
// body returns it byte for byte, so rendering again gives the same body.
func Render(d Diagnosis, runID, original string) string {
	var b strings.Builder
	b.WriteString(diagnosisPart(d, runID, " The original report is kept below."))
	b.WriteString("\n\n" + originalHeading + "\n\n<details>\n<summary>Original report</summary>\n\n")
	b.WriteString(BeginMarker + "\n" + original + "\n" + EndMarker + "\n\n</details>\n")
	return b.String()
}

// RenderDiagnosisOnly is the diagnosis without the original-report section: the
// comment that stands in for the body when the body would be too long for the
// forge.
func RenderDiagnosisOnly(d Diagnosis, runID string) string {
	return diagnosisPart(d, runID, "") + "\n"
}

// renderedSize is the size of the diagnosis as the body carries it, without the
// original-report section: what the cap of 16384 bytes is measured on.
func renderedSize(d Diagnosis, runID string) int {
	return len(diagnosisPart(d, runID, " The original report is kept below."))
}

// diagnosisPart is the body up to and including the attribution line, which
// ends with tail after the run id.
func diagnosisPart(d Diagnosis, runID, tail string) string {
	var b strings.Builder
	b.WriteString(markerPrefix + runID + markerSuffix + "\n")
	b.WriteString("## Diagnosis\n\n")
	fmt.Fprintf(&b, "**Severity:** %s · **Confidence:** %s\n\n", d.Severity, d.Confidence)
	b.WriteString(d.RootCause + "\n\n")

	if len(d.AffectedFiles) > 0 {
		b.WriteString("## Affected files\n\n")
		for _, f := range d.AffectedFiles {
			if f.Line > 0 {
				fmt.Fprintf(&b, "- `%s:%d` — %s\n", f.Path, f.Line, f.Note)
			} else {
				fmt.Fprintf(&b, "- `%s` — %s\n", f.Path, f.Note)
			}
		}
		b.WriteString("\n")
	}
	if d.SuggestedFix != "" {
		b.WriteString("## Suggested fix\n\n" + d.SuggestedFix + "\n\n")
	}
	if len(d.AcceptanceCriteria) > 0 {
		b.WriteString("## Acceptance Criteria\n\n")
		for _, c := range d.AcceptanceCriteria {
			b.WriteString("- [ ] " + c + "\n")
		}
		b.WriteString("\n")
	}
	if d.Question != nil {
		b.WriteString("## Question\n\n" + d.Question.Text + "\n\n")
		for i, o := range d.Question.Options {
			fmt.Fprintf(&b, "%c. %s\n", 'A'+i, o)
		}
		if len(d.Question.Options) > 0 {
			b.WriteString("\n")
		}
	}
	fmt.Fprintf(&b, "<sub>Diagnosed by agent-fox `af triage` (run %s).%s</sub>", runID, tail)
	return b.String()
}

// Extract splits a body into the person's original report and the previous
// diagnosis, and says whether it could not:
//
//   - a body whose first line does not start with the triage marker is wholly the
//     original;
//   - a body that is ours has its original between the first begin marker
//     followed by a newline and the last newline followed by the end marker, and
//     its previous diagnosis between the first line and the original-report
//     heading. A body that a browser saved with CRLF line endings is ours all the
//     same, and is read with LF endings, so its original has them too;
//   - a body that starts with the marker but whose first line is not the marker,
//     or whose markers cannot be paired, or that has anything after the original
//     but the close of its details block, is wholly the original, with the warning
//     original_report_unparsed: losing a person's words is worse than nesting an
//     old diagnosis in the report.
func Extract(body string) (original, previous, warning string) {
	first, _, _ := strings.Cut(body, "\n")
	if !strings.HasPrefix(first, markerPrefix) {
		return body, "", ""
	}
	if line, ok := strings.CutSuffix(first, "\r"); ok && strings.HasSuffix(line, markerSuffix) {
		return Extract(strings.ReplaceAll(body, "\r\n", "\n"))
	}
	if !strings.HasSuffix(first, markerSuffix) {
		return body, "", WarnOriginalUnparsed
	}
	begin := strings.Index(body, BeginMarker+"\n")
	end := strings.LastIndex(body, "\n"+EndMarker)
	if begin < 0 || end < begin+len(BeginMarker)+1 {
		return body, "", WarnOriginalUnparsed
	}
	if strings.TrimSpace(body[end+1+len(EndMarker):]) != "</details>" {
		return body, "", WarnOriginalUnparsed // something was written after the original report
	}
	original = body[begin+len(BeginMarker)+1 : end]

	// The heading is the last one before the begin marker: the diagnosis and the
	// attribution line come before it, and nothing a diagnosis holds can be mistaken
	// for the heading that is followed by the marker.
	heading := strings.LastIndex(body[:begin], "\n"+originalHeading+"\n")
	if heading < len(first) {
		return body, "", WarnOriginalUnparsed
	}
	return original, body[len(first)+1 : heading+1], ""
}
