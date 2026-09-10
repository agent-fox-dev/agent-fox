package codefix

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Acceptance criteria are the one part of a problem report that is already a
// specification: someone has written down, before any code exists, what the
// finished change has to do. A fix run that ignores them is guessing at a
// question that was already answered — and a fix run that says "done" without
// saying which criterion it satisfied is asking the reader to take its word
// for it.
//
// So they are extracted here, in Go, from the report's own text; both model
// phases are told about them; and the implementation phase cannot end without
// a verdict and a piece of evidence for every single one. The extraction is
// deterministic, the completeness check is deterministic, and the rendering
// is a pure function of the two — the only thing the model contributes is the
// judgement, which is the part that needs one.

// Criterion is one acceptance criterion, as the report stated it.
type Criterion struct {
	// ID is the label the report used ("AC-1"), or one derived from the
	// criterion's position when the report used none.
	ID string `json:"id"`
	// Text is the criterion itself, with its label and markdown emphasis
	// stripped and its line breaks folded.
	Text string `json:"text"`
}

// CriterionVerdict is the model's judgement on one criterion, with the
// evidence for it.
//
// It lives on Implementation rather than on Result because it is a claim the
// model made, not a fact this package established. What this package
// guarantees is narrower and still worth having: there is exactly one verdict
// per criterion, every verdict names a criterion that was actually asked for,
// and none of them is a bare "yes".
type CriterionVerdict struct {
	ID       string `json:"id"`
	Verdict  string `json:"verdict"`
	Evidence string `json:"evidence"`
}

// The two verdicts a criterion can get. There is deliberately no third:
// "partial" is where a change that does not meet the criterion goes to be
// described as though it did.
const (
	CriterionPass = "pass"
	CriterionFail = "fail"
)

// CriterionVerdicts is the enum the schema declares.
var CriterionVerdicts = []string{CriterionPass, CriterionFail}

// Passed reports whether v is a pass.
func (v CriterionVerdict) Passed() bool {
	return strings.EqualFold(strings.TrimSpace(v.Verdict), CriterionPass)
}

// Label renders the verdict as it appears in a comment.
func (v CriterionVerdict) Label() string {
	if v.Passed() {
		return "PASS"
	}
	return "FAIL"
}

// Mark is the tick or cross that leads the rendered line.
func (v CriterionVerdict) Mark() string {
	if v.Passed() {
		return "✅"
	}
	return "❌"
}

// maxCriteria bounds what one report can add to a prompt. A report with more
// than this many numbered criteria is a specification document, and the
// pipeline's answer to a specification document is the `spec` tool.
const maxCriteria = 30

// maxCriterionBytes bounds one criterion.
const maxCriterionBytes = 1000

// minEvidenceRunes is the floor under a piece of evidence. It is not a
// quality bar — nothing here can tell whether evidence is true — but it does
// refuse the one-word claim ("yes", "done", "works") that carries no
// information at all and costs nothing to write.
const minEvidenceRunes = 24

var (
	// atxHeading matches a markdown heading and captures its level and text.
	atxHeading = regexp.MustCompile(`^ {0,3}(#{1,6})\s+(.*?)\s*#*\s*$`)
	// criteriaTitle matches the heading text this parser is looking for,
	// however it is decorated: "Acceptance Criteria", "**Acceptance
	// criteria:**", "Acceptance Criteria (AC)".
	criteriaTitle = regexp.MustCompile(`(?i)^\s*(?:\*\*|__)?\s*acceptance\s+criteri(?:a|on)\b[^a-z0-9]*(?:\([^)]*\))?[^a-z0-9]*(?:\*\*|__)?\s*:?\s*$`)
	// listItem matches a bullet or an ordered item and captures its body.
	listItem = regexp.MustCompile(`^ {0,3}(?:[-*+]|\d{1,3}[.)])\s+(.*)$`)
	// taskBox matches the checkbox an item may carry.
	taskBox = regexp.MustCompile(`^\[[ xX]\]\s*`)
	// criterionID matches the label an item may open with, in the spellings
	// reports actually use: "**AC-1:**", "AC-1 —", "Criterion 2.",
	// "NS-REQ-3:". The emphasis may close on either side of the delimiter,
	// and the delimiter is required — a label the item does not separate
	// from its text is not a label.
	criterionID = regexp.MustCompile(`^(?:\*\*|__)?\s*((?i:ac|criteri(?:on|a)|requirement|req)[-_ ]?\d{1,3}|[A-Z][A-Z0-9]{0,9}(?:-[A-Z0-9]{1,10}){1,3})\s*(?:\*\*|__)?\s*[:.)\x{2013}\x{2014}-]\s*(?:\*\*|__)?\s*`)
	// acID matches the canonical "AC-1" shape, in any of its spellings.
	acID = regexp.MustCompile(`(?i)^ac[-_ ]?(\d{1,3})$`)
)

// ParseCriteria extracts the acceptance criteria from a problem report.
//
// It reads the first "Acceptance Criteria" section — the heading the `issue`
// tool writes, and the one people write by hand — and takes its list items.
// An item may label itself ("**AC-1:** …"); when none does, the label is the
// item's position, so the criteria can still be referred to one by one.
//
// It returns nil when the report has no such section, and that is the common
// case: most problems arrive as a paragraph of prose. Nothing downstream
// changes when it does — the criteria machinery is additive, and a run
// without criteria behaves exactly as it did before.
func ParseCriteria(body string) []Criterion {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	start, level, ok := findCriteriaSection(lines)
	if !ok {
		return nil
	}

	var (
		out   []Criterion
		item  []string
		used  = map[string]bool{}
		flush = func() {
			if len(item) == 0 {
				return
			}
			raw := strings.Join(item, " ")
			item = nil
			if c, ok := parseCriterion(raw, len(out)+1, used); ok {
				out = append(out, c)
			}
		}
	)

	for _, line := range lines[start:] {
		if h := atxHeading.FindStringSubmatch(line); h != nil {
			// A heading of the same level or higher ends the section. A
			// deeper one — "### AC-4, in detail" — does not, and its items
			// keep being read.
			if len(h[1]) <= level {
				break
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		indented := len(line)-len(strings.TrimLeft(line, " \t")) >= 2
		m := listItem.FindStringSubmatch(line)
		switch {
		case trimmed == "":
			// A blank line separates items but does not end the section: a
			// loose list has one between every pair of items.
			flush()
		case m != nil && !(indented && len(item) > 0):
			flush()
			item = append(item, m[1])
		case len(item) > 0 && indented:
			// An indented line — prose or a nested bullet — continues the
			// criterion above it rather than becoming one of its own.
			item = append(item, trimmed)
		default:
			// Unindented prose ends the list. Whatever follows it is
			// commentary on the criteria rather than one of them.
			flush()
			if len(out) > 0 {
				return out
			}
		}
		if len(out) >= maxCriteria {
			return out
		}
	}
	flush()
	return out
}

// findCriteriaSection locates the heading and reports the line after it plus
// the heading's level. A bold or plain "Acceptance criteria:" line counts as a
// level-6 heading, so that any real heading below it ends the section.
func findCriteriaSection(lines []string) (start, level int, ok bool) {
	for i, line := range lines {
		if h := atxHeading.FindStringSubmatch(line); h != nil {
			if criteriaTitle.MatchString(h[2]) {
				return i + 1, len(h[1]), true
			}
			continue
		}
		if t := strings.TrimSpace(line); t != "" && criteriaTitle.MatchString(t) {
			return i + 1, 6, true
		}
	}
	return 0, 0, false
}

// parseCriterion turns one list item into a Criterion.
func parseCriterion(raw string, position int, used map[string]bool) (Criterion, bool) {
	text := strings.TrimSpace(raw)
	text = taskBox.ReplaceAllString(text, "")

	id := ""
	if m := criterionID.FindStringSubmatch(text); m != nil {
		id = normalizeCriterionID(m[1])
		text = strings.TrimSpace(text[len(m[0]):])
	}
	text = strings.TrimSpace(strings.Trim(strings.TrimSpace(text), "*_"))
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return Criterion{}, false
	}
	if len(text) > maxCriterionBytes {
		text = strings.TrimSpace(text[:maxCriterionBytes]) + "…"
	}
	if id == "" || used[id] {
		id = unusedCriterionID(position, used)
	}
	used[id] = true
	return Criterion{ID: id, Text: text}, true
}

// normalizeCriterionID folds the spellings of an "AC-1" label into one and
// leaves any other label ("NS-REQ-3") as the report wrote it, upper-cased.
func normalizeCriterionID(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if m := acID.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return s
		}
		return fmt.Sprintf("AC-%d", n)
	}
	return s
}

// unusedCriterionID names an unlabelled criterion by its position, stepping
// past any label already taken.
func unusedCriterionID(position int, used map[string]bool) string {
	for n := position; ; n++ {
		if id := fmt.Sprintf("AC-%d", n); !used[id] {
			return id
		}
	}
}

// criteriaIDs lists the labels, in order.
func criteriaIDs(criteria []Criterion) []string {
	ids := make([]string, 0, len(criteria))
	for _, c := range criteria {
		ids = append(ids, c.ID)
	}
	return ids
}

// verdictsByID indexes a submission for rendering.
func verdictsByID(verdicts []CriterionVerdict) map[string]CriterionVerdict {
	byID := make(map[string]CriterionVerdict, len(verdicts))
	for _, v := range verdicts {
		byID[strings.ToUpper(strings.TrimSpace(v.ID))] = v
	}
	return byID
}

// checkVerdicts is the completeness check the submit tool applies.
//
// It is the whole mechanism, and it is four lines of comparison rather than a
// paragraph of prose in a system prompt: a submission that skips a criterion,
// invents one, or answers with a word is rejected and the model is told
// exactly which. The phase cannot end until every criterion has an answer.
func checkVerdicts(criteria []Criterion, got []CriterionVerdict) error {
	if len(criteria) == 0 {
		return nil
	}
	seen := map[string]bool{}
	known := map[string]bool{}
	for _, c := range criteria {
		known[c.ID] = true
	}
	for _, v := range got {
		id := normalizeCriterionID(v.ID)
		switch {
		case !known[id]:
			return fmt.Errorf("criteria_verdicts names %q, which is not one of the acceptance "+
				"criteria in the report (%s)", v.ID, strings.Join(criteriaIDs(criteria), ", "))
		case seen[id]:
			return fmt.Errorf("criteria_verdicts has two verdicts for %s; give exactly one", id)
		case !strings.EqualFold(v.Verdict, CriterionPass) && !strings.EqualFold(v.Verdict, CriterionFail):
			return fmt.Errorf("the verdict for %s is %q; it must be one of %s", id, v.Verdict,
				strings.Join(CriterionVerdicts, ", "))
		case len([]rune(strings.TrimSpace(v.Evidence))) < minEvidenceRunes:
			return fmt.Errorf("the evidence for %s says only %q; name the file and symbol that "+
				"implement it and the test that covers it, with the result you observed",
				id, strings.TrimSpace(v.Evidence))
		}
		seen[id] = true
	}
	var missing []string
	for _, c := range criteria {
		if !seen[c.ID] {
			missing = append(missing, c.ID)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("criteria_verdicts is missing %s; every acceptance criterion in the "+
			"report needs a verdict, including any the change does not meet",
			strings.Join(missing, ", "))
	}
	return nil
}

// normalizeVerdicts canonicalises a submission once it has passed the check,
// so that everything downstream — the JSON result, the rendered comment, the
// pull-request body — reads the same labels the report used.
func normalizeVerdicts(criteria []Criterion, got []CriterionVerdict) []CriterionVerdict {
	if len(criteria) == 0 || len(got) == 0 {
		return nil
	}
	byID := make(map[string]CriterionVerdict, len(got))
	for _, v := range got {
		byID[normalizeCriterionID(v.ID)] = v
	}
	out := make([]CriterionVerdict, 0, len(criteria))
	for _, c := range criteria {
		v, ok := byID[c.ID]
		if !ok {
			continue
		}
		out = append(out, CriterionVerdict{
			ID:       c.ID,
			Verdict:  strings.ToLower(strings.TrimSpace(v.Verdict)),
			Evidence: strings.TrimSpace(v.Evidence),
		})
	}
	return out
}

// criteriaOutcome is the one-word summary of the verdicts: "pass" when every
// criterion was met, "fail" when any was not or any is missing an answer, and
// "" when the report stated no criteria.
func criteriaOutcome(criteria []Criterion, verdicts []CriterionVerdict) string {
	if len(criteria) == 0 {
		return ""
	}
	byID := verdictsByID(verdicts)
	for _, c := range criteria {
		v, ok := byID[c.ID]
		if !ok || !v.Passed() {
			return CriterionFail
		}
	}
	return CriterionPass
}
