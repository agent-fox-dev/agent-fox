package fix

import (
	"fmt"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/checks"
)

// The headings of a pull-request body, in the order they come.
const (
	headingVerification = "## Verification (measured by af)"
	headingRootCause    = "## Root cause (reported)"
	headingChange       = "## Change (reported)"
	headingTests        = "## Tests (reported)"
	headingCriteria     = "## Criteria (reported)"
	headingDecisions    = "## Decisions (reported)"
	headingFiles        = "## Changed files (from git)"
)

// BodyCutMarker marks a place where the body of a pull request was cut to fit what the
// forge takes.
const BodyCutMarker = "[cut: the body is over the limit of the forge]"

// hiddenFilesLine is the line that ends a list of files that was cut: the marker, and how many files are
// not shown.
const hiddenFilesLine = "- %s (%d more files)\n"

// NotDeclared stands for the reason of a changed file the report did not name.
const NotDeclared = "(not declared)"

// none stands for a reported section that has nothing in it.
const none = "(none reported)"

// BodyFacts are the facts a pull-request body is rendered from.
type BodyFacts struct {
	RunID string
	// Verdict, Rows, RevertCheck and Reverted are what af measured; NoVerify says it
	// measured nothing because the run said not to.
	Verdict     string
	Rows        []checks.Row
	RevertCheck *RevertRef
	Reverted    []string
	NoVerify    bool
	// Report is what the brain says, and Changed the paths git says changed.
	Report  Report
	Changed []string
	// IssueURL is the issue, when there is one, and Closes whether the change closes it.
	IssueURL string
	Closes   bool
	// Brain, CostUSD and CostSource are what the run used.
	Brain      string
	CostUSD    float64
	CostSource string
}

// reportedSection is a part of the body that the brain wrote.
type reportedSection struct {
	heading string
	text    string
	cut     bool
}

// RenderBody renders the body of a pull request from the facts, in the order: the comment
// that names the run, what af measured, what the brain reported, the files git sees changed
// with the reason the brain gave for each, the line that closes or refers to the issue,
// and a footer. Within limit bytes, the reported sections are cut first, the least
// important first, behind a visible marker, then the list of files; what af measured and
// the Closes or Refs line are never cut, and if they are over the limit by themselves the
// body is.
//
// What the brain reported is quoted, so that a heading of it is not a section of the body
// and a comment in it hides nothing: only af's own lines are in the clear.
func RenderBody(f BodyFacts, limit int) string {
	sections := []reportedSection{
		{heading: headingRootCause, text: f.Report.RootCause},
		{heading: headingChange, text: f.Report.Change},
		{heading: headingTests, text: testsText(f.Report.Tests)},
		{heading: headingCriteria, text: criteriaText(f.Report.Criteria)},
		{heading: headingDecisions, text: listText(f.Report.Decisions)},
	}
	files := fileLines(f)
	hidden := 0 // files cut from the end of the list

	body := composeBody(f, sections, files, hidden)
	// The least important section goes first. A section is cut by the bytes the body is
	// over, once more for each time its quoting makes the cut come short.
	for i := len(sections) - 1; i >= 0 && len(body) > limit; i-- {
		for len(body) > limit && !(sections[i].cut && sections[i].text == "") {
			over := len(body) - limit
			keep := len(sections[i].text) - over - len(BodyCutMarker) - 2
			sections[i].cut = true
			if keep <= 0 {
				sections[i].text = ""
			} else {
				sections[i].text = cutAtRune(sections[i].text, keep)
			}
			body = composeBody(f, sections, files, hidden)
		}
	}
	if len(body) > limit && len(files) > 0 {
		// The files that go are the last ones, and the line that says so takes room too.
		over := len(body) - limit + len(fmt.Sprintf(hiddenFilesLine, BodyCutMarker, len(files)))
		for ; hidden < len(files) && over > 0; hidden++ {
			over -= len(files[len(files)-1-hidden]) + 1
		}
		body = composeBody(f, sections, files, hidden)
	}
	return body
}

// composeBody puts the parts together.
func composeBody(f BodyFacts, sections []reportedSection, files []string, hidden int) string {
	var b strings.Builder
	b.WriteString(runMarker(f.RunID) + "\n\n")
	b.WriteString(verificationText(f))
	for _, s := range sections {
		text := strings.TrimSpace(s.text)
		b.WriteString("\n" + s.heading + "\n\n")
		switch {
		case text != "":
			b.WriteString(quote(text) + "\n")
		case !s.cut: // af's own word, not a claim of the brain, so it is not quoted
			b.WriteString(none + "\n")
		}
		if s.cut {
			b.WriteString("\n" + BodyCutMarker + "\n")
		}
	}
	b.WriteString("\n" + headingFiles + "\n\n")
	shown := files[:len(files)-hidden]
	if len(files) == 0 {
		b.WriteString(none + "\n")
	}
	for _, line := range shown {
		b.WriteString(line + "\n")
	}
	if hidden > 0 {
		fmt.Fprintf(&b, hiddenFilesLine, BodyCutMarker, hidden)
	}
	if f.IssueURL != "" {
		word := "Refs"
		if f.Closes {
			word = "Closes"
		}
		fmt.Fprintf(&b, "\n%s %s\n", word, f.IssueURL)
	}
	fmt.Fprintf(&b, "\n---\nRun `%s` · brain `%s` · cost $%.4f (%s)\n", f.RunID, oneLine(f.Brain), f.CostUSD, oneLine(f.CostSource))
	return b.String()
}

// verificationText is the section that af wrote from what it measured.
func verificationText(f BodyFacts) string {
	var b strings.Builder
	b.WriteString(headingVerification + "\n\n")
	if f.NoVerify {
		b.WriteString("Not verified: the run was made with --no-verify, so no gate was run and nothing was measured. A person has to check it.\n")
	} else {
		fmt.Fprintf(&b, "Verdict: **%s**\n", f.Verdict)
		if len(f.Rows) > 0 {
			b.WriteString("\n| Check | Baseline | After | Verdict |\n|---|---|---|---|\n")
			for _, r := range f.Rows {
				fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", cell(r.Name), cell(r.Before), cell(r.After), cell(r.Verdict))
			}
		}
		if f.RevertCheck != nil {
			fmt.Fprintf(&b, "\nRevert check: %s (%s)\n", f.RevertCheck.Outcome, f.RevertCheck.Reason)
		}
	}
	if len(f.Reverted) > 0 {
		b.WriteString("\nProtected paths that the change touched and af put back:\n")
		for _, p := range f.Reverted {
			fmt.Fprintf(&b, "- `%s`\n", codeSpan(p))
		}
	}
	return b.String()
}

// cell is a name as the cell of a table.
func cell(s string) string { return strings.ReplaceAll(oneLine(s), "|", `\|`) }

// codeSpan is a path as the text of a code span.
func codeSpan(s string) string { return strings.ReplaceAll(oneLine(s), "`", "'") }

// quote makes the text a block quote, with nothing of it left to start a comment.
func quote(text string) string {
	text = strings.ReplaceAll(text, "<!--", "&lt;!--")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight("> "+line, " ")
	}
	return strings.Join(lines, "\n")
}

func testsText(tests []Test) string {
	var lines []string
	for _, t := range tests {
		name := ""
		if t.Name != "" {
			name = " (" + singleLine(t.Name) + ")"
		}
		lines = append(lines, fmt.Sprintf("- `%s`%s — %s", codeSpan(t.Path), name, singleLine(t.Proves)))
	}
	return strings.Join(lines, "\n")
}

func criteriaText(criteria []Criterion) string {
	var lines []string
	for _, c := range criteria {
		line := fmt.Sprintf("- [%s] %s", c.Status, singleLine(c.Criterion))
		if c.Evidence != "" {
			line += " — " + singleLine(c.Evidence)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func listText(items []string) string {
	var lines []string
	for _, item := range items {
		lines = append(lines, "- "+singleLine(item))
	}
	return strings.Join(lines, "\n")
}

// fileLines are the files git says changed, each with the reason the report gave.
func fileLines(f BodyFacts) []string {
	why := map[string]string{}
	for _, file := range f.Report.Files {
		why[file.Path] = singleLine(file.Why)
	}
	var lines []string
	for _, p := range sortedUnique(f.Changed) {
		reason := why[p]
		if reason == "" {
			reason = NotDeclared
		}
		lines = append(lines, fmt.Sprintf("- `%s` — %s", codeSpan(p), reason))
	}
	return lines
}
