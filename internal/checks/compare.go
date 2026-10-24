package checks

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// idPattern is what a gate id looks like.
var idPattern = regexp.MustCompile(`^[0-9a-f]{12}$`)

// UsageError is a mistake in what the caller asked for, as against a failure
// to do it: an id that names no result, results that cannot be compared.
type UsageError struct {
	Message string
}

func (e *UsageError) Error() string { return e.Message }

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

// Compare compares the stored results with the gate ids before and after, check
// by check. Verdicts are of the checks: a
// check that passed and does not now is a regression whatever the others did,
// and the verdict of the comparison is the first of regressed, still_failing,
// pass_was_already_failing and pass that a row carries. If either result has
// no checks nothing was measured, and the verdict is unverified. An id that
// names no result, or results that do not have the same checks, is a
// *UsageError.
func Compare(dir, before, after string) (Comparison, error) {
	b, err := resolve(dir, "before", before)
	if err != nil {
		return Comparison{}, err
	}
	a, err := resolve(dir, "after", after)
	if err != nil {
		return Comparison{}, err
	}
	c := Comparison{
		Before: Side{ID: b.ID, Passed: b.Passed},
		After:  Side{ID: a.ID, Passed: a.Passed},
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

// resolve finds the stored result with the gate id, the one before or after.
func resolve(dir, which, id string) (Result, error) {
	if idPattern.MatchString(id) {
		if res, ok := loadResult(dir, Key{ID: id}); ok {
			return res, nil
		}
	}
	return Result{}, &UsageError{fmt.Sprintf("the result %s, %q, is not the id of a stored gate result", which, id)}
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
	return &UsageError{fmt.Sprintf("the results were not measured by the same checks; only before: %s; only after: %s", list(onlyBefore), list(onlyAfter))}
}
