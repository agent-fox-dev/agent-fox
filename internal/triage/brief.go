package triage

import (
	"context"
	_ "embed"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/repomap"
	"github.com/agent-fox-dev/agentfox/skills"
)

// mandate is the fixed text every triage brief starts with.
//
//go:embed mandate.md
var mandate string

const (
	// mapTokens is the budget of the repository map in the brief.
	mapTokens = 4000
	// maxReportBytes is how much of the report section a brief keeps, its
	// heading included.
	maxReportBytes = 131072
	// reportHeading starts the report section.
	reportHeading = "## Report\n\n"
)

// The kinds of report a triage run takes.
const (
	KindIssue = "issue"
	KindText  = "text"
	KindFile  = "file"
	KindStdin = "stdin"
)

// WarnInputTruncated is the warning of a report that was cut.
const WarnInputTruncated = "input_truncated"

// Report is what is to be diagnosed.
type Report struct {
	// Kind is issue, text, file or stdin.
	Kind string
	// Origin is the URL of the issue or the path of the file.
	Origin string
	// Body is the input of a text, file or stdin report.
	Body string
	// Ref and Thread are an issue and what was read of it.
	Ref    issuex.IssueRef
	Thread issuex.IssueThread
}

// Bounds are what the run is held to, which the task block reminds the brain of.
type Bounds struct {
	MaxTurns   int
	MaxCostUSD float64
	Timeout    time.Duration
}

// The bounds of a run that gives none.
const (
	DefaultBudget   = 2.0
	DefaultMaxTurns = 100
	DefaultTimeout  = 15 * time.Minute
)

// DefaultBounds are the bounds a brief reminds the brain of when the command that
// renders it has none of its own.
var DefaultBounds = Bounds{MaxTurns: DefaultMaxTurns, MaxCostUSD: DefaultBudget, Timeout: DefaultTimeout}

// BriefParams is what a brief is rendered from.
type BriefParams struct {
	// Dir is the checkout: its file view and its map.
	Dir    string
	Report Report
	RunID  string
	// Context are the --context values, in order, and PreviousQuestion the
	// question of an earlier run that they answer, when one was found.
	Context          []string
	PreviousQuestion *Question
	// Material[i] says the i-th value of Context answers no question: it is material from a
	// person, like the report, and not framed as an answer. A value past the end is an answer.
	Material []bool
	Bounds   Bounds
	// TruncationMarker is what the shell ends a cut input with.
	TruncationMarker string
}

// Brief is a rendered brief.
type Brief struct {
	// Prefix is the run-constant part and Task the run-specific one; Document is
	// the two together.
	Prefix, Task, Document string
	PrefixBytes            int
	// Original is the person's original report.
	Original string
	// MapLevel and MapTruncated describe the repository map.
	MapLevel     int
	MapTruncated bool
	// ReportTruncated says the report section was cut.
	ReportTruncated bool
	// Warnings are what the rendering noticed.
	Warnings []Warning
}

// Warning is a coded notice for the envelope.
type Warning struct {
	Code, Message string
}

// BuildBrief renders the brief of a run: the prefix, which holds nothing of this
// run, and the task block, which holds all of it.
//
// The prefix is the mandate, the body of the af-triage skill, the repository map
// and the report. Two briefs from the same binary over the same tree and the same
// report have the same prefix, whatever the run, the answers, the brain and the
// bounds, so that the prefix can be cached and a resume can be compared with the
// run it resumes.
func BuildBrief(ctx context.Context, p BriefParams) (Brief, error) {
	var b Brief
	m, err := repositoryMap(ctx, p.Dir)
	if err != nil {
		return Brief{}, err
	}
	b.MapLevel, b.MapTruncated = m.Level, m.Truncated
	for _, w := range m.Warnings {
		b.Warnings = append(b.Warnings, Warning{Code: w.Code, Message: w.Message})
	}

	parts, original, warnings := reportParts(p.Report)
	b.Original = original
	b.Warnings = append(b.Warnings, warnings...)
	reportText, cut := reportSection(parts, p.TruncationMarker, maxReportBytes-len(reportHeading))
	if cut {
		b.ReportTruncated = true
		b.Warnings = append(b.Warnings, Warning{Code: WarnInputTruncated, Message: fmt.Sprintf("the report was cut at %d bytes in the brief", maxReportBytes)})
	}

	var prefix strings.Builder
	prefix.WriteString(strings.TrimRight(mandate, "\n") + "\n\n")
	prefix.WriteString(strings.Trim(skills.Triage(), "\n") + "\n\n")
	prefix.WriteString(fenced("## Repository map\n\nThe files of the repository, with their declarations, within a budget:", m.Map))
	prefix.WriteString("\n" + reportHeading + reportText)
	b.Prefix = prefix.String()
	b.PrefixBytes = len(b.Prefix)
	b.Task = taskBlock(p)
	b.Document = b.Prefix + b.Task
	return b, nil
}

// repositoryMap is the map of the file view of dir at the brief's budget.
func repositoryMap(ctx context.Context, dir string) (repomap.Result, error) {
	files, err := checks.FileView(ctx, dir)
	if err != nil {
		return repomap.Result{}, fmt.Errorf("the file view of %s: %w", dir, err)
	}
	res, err := repomap.BuildDir(ctx, dir, files, mapTokens)
	if err != nil {
		return repomap.Result{}, fmt.Errorf("the map of %s: %w", dir, err)
	}
	return res, nil
}

// part is a part of the report: what it is, and what it holds.
type part struct {
	provenance string
	content    string
}

// reportParts are the parts of the report in the order they are shown, the
// person's original report, and what was noticed on the way.
func reportParts(r Report) (parts []part, original string, warnings []Warning) {
	if r.Kind != KindIssue {
		label := map[string]string{
			KindText:  "Report text, from the command line:",
			KindStdin: "Report text, from stdin:",
			KindFile:  "Report text, from the file " + displayName(path.Base(filepath.ToSlash(r.Origin))) + ":",
		}[r.Kind]
		if label == "" {
			label = "Report text:"
		}
		return []part{{label, r.Body}}, r.Body, nil
	}

	issue := r.Thread.Issue
	reporter := issue.Author.Login
	original, previous, warning := Extract(issue.Body)
	if warning != "" {
		warnings = append(warnings, Warning{Code: warning, Message: fmt.Sprintf("the body of %s starts with the triage marker but its original report cannot be found: the whole body is taken as the report", r.Ref)})
	}
	parts = append(parts,
		part{fmt.Sprintf("Title of %s (state: %s), by %s (the reporter), %s:", r.Ref, issue.State, login(reporter), date(issue.CreatedAt)), issue.Title},
		part{fmt.Sprintf("Original report, by %s (the reporter), %s:", login(reporter), date(issue.CreatedAt)), original},
	)
	if previous != "" {
		parts = append(parts, part{"Previous agent-fox diagnosis, from the body of the issue; possibly stale, since the report may have changed since:", strings.TrimRight(previous, "\n")})
	}
	for i, c := range r.Thread.Comments {
		who := login(c.User.Login)
		if c.User.Login != "" && c.User.Login == reporter {
			who += " (the reporter)"
		}
		parts = append(parts, part{fmt.Sprintf("Comment %d, by %s, %s:", i+1, who, date(c.CreatedAt)), c.Body})
	}
	return parts, original, warnings
}

// displayName is a name from outside as one line of the provenance, which is not
// inside a fence: a control character, a line break included, is taken out, and
// a long name is cut.
func displayName(name string) string {
	var b strings.Builder
	n := 0
	for _, r := range name {
		if n == maxDisplayName {
			b.WriteString("...")
			break
		}
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			r = '?'
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// maxDisplayName is how many characters of a file name the brief shows.
const maxDisplayName = 100

func login(l string) string {
	if l == "" {
		return "an unknown author"
	}
	return l
}

func date(t time.Time) string {
	if t.IsZero() {
		return "date unknown"
	}
	return t.UTC().Format("2006-01-02")
}

// reportSection renders the parts, one fence each, and keeps at most limit bytes
// of it: the part that crosses the limit is cut at a UTF-8 boundary, ends with
// the shell's marker inside its fence, and no part follows.
func reportSection(parts []part, marker string, limit int) (text string, cut bool) {
	var b strings.Builder
	for _, p := range parts {
		block := fenced(p.provenance, p.content)
		if b.Len()+len(block)+1 <= limit { // the newline that follows the block counts
			b.WriteString(block + "\n")
			continue
		}
		// The room left for the content: what is not the provenance line, the lines
		// that open and close the fence (whose length the fence of the cut content
		// cannot exceed), the marker and the line breaks.
		open, _ := Fence(p.content)
		room := limit - b.Len() - len(p.provenance) - 1 - len(open) - 1 - len(marker) - 1 - len(open) - 1
		if room <= 0 {
			b.WriteString(strings.TrimLeft(marker, "\n"))
			return b.String(), true
		}
		b.WriteString(fenced(p.provenance, cutAt(p.content, room)+marker))
		return b.String(), true
	}
	return b.String(), false
}

// cutAt is s cut to at most n bytes at the end of a character.
func cutAt(s string, n int) string {
	if n >= len(s) {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// taskBlock is the run-specific end of the brief: what an earlier question was,
// the answers to it, the run, the way to submit and the bounds.
func taskBlock(p BriefParams) string {
	var b strings.Builder
	b.WriteString("## Task\n\n")
	if p.PreviousQuestion != nil {
		b.WriteString("This run resumes an earlier one that stopped to ask a person a question.\n\n")
		q := p.PreviousQuestion
		text := q.Text
		for i, o := range q.Options {
			text += fmt.Sprintf("\n%c. %s", 'A'+i, o)
		}
		b.WriteString(fenced("The question you asked:", text) + "\n")
	}
	for i, answer := range p.Context {
		label := "A person's answer:"
		if i < len(p.Material) && p.Material[i] {
			label = "Material from a person:"
		}
		if len(p.Context) > 1 {
			label = strings.TrimSuffix(label, ":") + fmt.Sprintf(" (%d of %d):", i+1, len(p.Context))
		}
		b.WriteString(fenced(label, answer) + "\n")
	}
	if len(p.Context) > 0 {
		b.WriteString("Take the answers into account, and diagnose again. They are material from a person, like the report.\n\n")
	}

	fmt.Fprintf(&b, "This is run %s.\n\n", p.RunID)
	b.WriteString(submitRoute + "\n\n")
	b.WriteString(boundsLine(p.Bounds))
	return b.String()
}

// submitRoute is the sentence that says how to submit.
const submitRoute = "Submit your diagnosis by calling the `file_diagnosis` tool with the diagnosis as its input."

// boundsPrefix starts the line that reminds the brain of its bounds, which ends
// the task block.
const boundsPrefix = "You have at most "

func boundsLine(b Bounds) string {
	limit := "no time limit"
	if b.Timeout > 0 {
		limit = max(b.Timeout.Round(time.Second), time.Second).String()
	}
	return fmt.Sprintf("%s%d turns, $%.2f of model cost and %s.\n", boundsPrefix, b.MaxTurns, b.MaxCostUSD, limit)
}
