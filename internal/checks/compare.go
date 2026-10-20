package checks

import (
	"fmt"
	"slices"
	"strings"
)

// The verdicts of comparing two gate results.
const (
	VerdictPass                  = "pass"
	VerdictPassWasAlreadyFailing = "pass_was_already_failing"
	VerdictRegressed             = "regressed"
	VerdictStillFailing          = "still_failing"
	VerdictUnverified            = "unverified"
)

// verdictOrder is the order in which the verdict of a row decides the verdict
// of the comparison.
var verdictOrder = []string{VerdictRegressed, VerdictStillFailing, VerdictPassWasAlreadyFailing, VerdictPass}

// Side is one of the two results of a comparison.
type Side struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Passed bool   `json:"passed"`
}

// Row is the comparison of one check.
type Row struct {
	Name    string `json:"name"`
	Before  string `json:"before"`
	After   string `json:"after"`
	Verdict string `json:"verdict"`
}

// Comparison is what two gate results say together.
type Comparison struct {
	Before   Side   `json:"before"`
	After    Side   `json:"after"`
	Verdict  string `json:"verdict"`
	Landable bool   `json:"landable"`
	Rows     []Row  `json:"rows"`
}

// Compare compares the stored results that before and after name, each a label
// of labels.json or a gate id, check by check. Verdicts are of the checks: a
// check that passed and does not now is a regression whatever the others did,
// and the verdict of the comparison is the first of regressed, still_failing,
// pass_was_already_failing and pass that a row carries. If either result has
// no checks nothing was measured, and the verdict is unverified. An argument
// that names no result, or results that do not have the same checks, is a
// *UsageError.
func Compare(dir, before, after string) (Comparison, error) {
	b, bLabel, err := resolve(dir, "--before", before)
	if err != nil {
		return Comparison{}, err
	}
	a, aLabel, err := resolve(dir, "--after", after)
	if err != nil {
		return Comparison{}, err
	}
	c := Comparison{
		Before: Side{ID: b.ID, Label: bLabel, Passed: b.Passed},
		After:  Side{ID: a.ID, Label: aLabel, Passed: a.Passed},
		Rows:   []Row{},
	}
	if len(b.Checks) == 0 || len(a.Checks) == 0 {
		c.Verdict = VerdictUnverified
		return c, nil
	}
	if err := sameChecks(b, a); err != nil {
		return Comparison{}, err
	}

	statusAfter := map[string]string{}
	for _, check := range a.Checks {
		statusAfter[check.Name] = check.Status
	}
	seen := map[string]bool{}
	for _, check := range b.Checks {
		row := Row{Name: check.Name, Before: check.Status, After: statusAfter[check.Name]}
		row.Verdict = rowVerdict(row.Before == StatusPass, row.After == StatusPass)
		c.Rows = append(c.Rows, row)
		seen[row.Verdict] = true
	}
	for _, v := range verdictOrder {
		if seen[v] {
			c.Verdict = v
			break
		}
	}
	c.Landable = c.Verdict == VerdictPass || c.Verdict == VerdictPassWasAlreadyFailing
	return c, nil
}

// rowVerdict is the verdict of one check from whether it passed before and
// after. A check that failed, errored or timed out did not pass.
func rowVerdict(passedBefore, passedAfter bool) string {
	switch {
	case passedBefore && passedAfter:
		return VerdictPass
	case passedAfter:
		return VerdictPassWasAlreadyFailing
	case passedBefore:
		return VerdictRegressed
	}
	return VerdictStillFailing
}

// resolve finds the stored result that arg names, as the label it is in
// labels.json first and as a gate id second, and returns the label to report
// for it: arg itself when it was a label, else the label it was stored under.
func resolve(dir, flag, arg string) (Result, string, error) {
	if arg == "" {
		return Result{}, "", &UsageError{fmt.Sprintf("%s is required: give a label or a gate id", flag)}
	}
	if id, ok := readLabels(dir)[arg]; ok {
		res, ok := loadResult(dir, Key{ID: id})
		if !ok {
			return Result{}, "", &UsageError{fmt.Sprintf("%s %q is the label of the result %s, which is not stored any more", flag, arg, id)}
		}
		return res, arg, nil
	}
	if idPattern.MatchString(arg) {
		if res, ok := loadResult(dir, Key{ID: arg}); ok {
			return res, res.Label, nil
		}
	}
	return Result{}, "", &UsageError{fmt.Sprintf("%s %q is neither a label in .agent-fox/gate/labels.json nor the id of a stored gate result", flag, arg)}
}

// sameChecks refuses two results that were not measured by the same checks: a
// verdict between different tables says nothing.
func sameChecks(before, after Result) error {
	names := func(r Result) []string {
		var out []string
		for _, c := range r.Checks {
			out = append(out, c.Name)
		}
		return out
	}
	only := func(from, other []string) []string {
		var out []string
		for _, n := range from {
			if !slices.Contains(other, n) {
				out = append(out, n)
			}
		}
		return out
	}
	b, a := names(before), names(after)
	onlyBefore, onlyAfter := only(b, a), only(a, b)
	if len(onlyBefore) == 0 && len(onlyAfter) == 0 {
		return nil
	}
	list := func(names []string) string {
		if len(names) == 0 {
			return "none"
		}
		return strings.Join(names, ", ")
	}
	return &UsageError{fmt.Sprintf("the results were not measured by the same checks; only in --before: %s; only in --after: %s", list(onlyBefore), list(onlyAfter))}
}
