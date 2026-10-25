package checks

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// fixtureID is an id of 12 hex digits, each the digit n.
func fixtureID(n int) string { return strings.Repeat(string("0123456789abcdef"[n]), 12) }

// A row's verdict is of the one check: fail, error and timeout all count as
// not passing.
func TestCompareGivesAVerdictPerCheck(t *testing.T) {
	dir := t.TempDir()
	before := storeFixture(t, dir, fixtureID(1), StatusPass, StatusFail, StatusPass, StatusFail, StatusError, StatusPass)
	after := storeFixture(t, dir, fixtureID(2), StatusPass, StatusPass, StatusFail, StatusFail, StatusPass, StatusTimeout)

	c, err := Compare(dir, before.ID, after.ID)

	want := []Row{
		{"a", "pass", "pass", VerdictPass},
		{"b", "fail", "pass", VerdictPassWasAlreadyFailing},
		{"c", "pass", "fail", VerdictRegressed},
		{"d", "fail", "fail", VerdictStillFailing},
		{"e", "error", "pass", VerdictPassWasAlreadyFailing},
		{"f", "pass", "timeout", VerdictRegressed},
	}
	if err != nil || !reflect.DeepEqual(c.Rows, want) {
		t.Errorf("rows = %+v, %v; want %+v", c.Rows, err, want)
	}
	if c.Before != (Side{ID: before.ID}) || c.After != (Side{ID: after.ID}) {
		t.Errorf("before = %+v, after = %+v", c.Before, c.After)
	}
}

// The comparison's verdict is the first of regressed, still_failing,
// pass_was_already_failing and pass that a row has, and only the last two land.
func TestCompareVerdictIsTheWorstRow(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after []string
		verdict       string
		landable      bool
	}{
		{"all pass", []string{StatusPass, StatusPass}, []string{StatusPass, StatusPass}, VerdictPass, true},
		{"a fix", []string{StatusPass, StatusFail}, []string{StatusPass, StatusPass}, VerdictPassWasAlreadyFailing, true},
		{"a fix and a check still failing", []string{StatusFail, StatusTimeout}, []string{StatusPass, StatusError}, VerdictStillFailing, false},
		{"a regression whatever the others did", []string{StatusFail, StatusFail, StatusPass}, []string{StatusPass, StatusFail, StatusError}, VerdictRegressed, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			storeFixture(t, dir, fixtureID(1), tc.before...)
			after := storeFixture(t, dir, fixtureID(2), tc.after...)

			c, err := Compare(dir, fixtureID(1), fixtureID(2))

			if err != nil || c.Verdict != tc.verdict || c.Landable != tc.landable || c.After.Passed != after.Passed {
				t.Errorf("verdict %s, landable %v, %v; want %s, %v", c.Verdict, c.Landable, err, tc.verdict, tc.landable)
			}
		})
	}
}

// A side with no checks measured nothing.
func TestCompareWithNothingMeasuredIsUnverified(t *testing.T) {
	dir := t.TempDir()
	full := storeFixture(t, dir, fixtureID(1), StatusPass, StatusPass).ID
	empty := storeFixture(t, dir, fixtureID(2)).ID
	alsoEmpty := storeFixture(t, dir, fixtureID(3)).ID

	for name, pair := range map[string][2]string{
		"empty then full": {empty, full}, "full then empty": {full, empty}, "both empty": {empty, alsoEmpty},
	} {
		c, err := Compare(dir, pair[0], pair[1])
		if err != nil || c.Verdict != VerdictUnverified || c.Landable || c.Rows == nil {
			t.Errorf("%s: verdict %s, landable %v, rows %v, %v; want unverified, not landable and a list", name, c.Verdict, c.Landable, c.Rows, err)
		}
	}
}

// An id that names no stored result, and results of different checks, are
// mistakes of the caller that say which.
func TestCompareRefusesWhatCannotBeCompared(t *testing.T) {
	dir := t.TempDir()
	one := storeNamed(t, dir, Result{ID: fixtureID(1), Tree: "t", Checks: []CheckResult{{Name: "lint", Status: StatusPass}, {Name: "test", Status: StatusPass}}}).ID
	two := storeNamed(t, dir, Result{ID: fixtureID(2), Tree: "t", Checks: []CheckResult{{Name: "test", Status: StatusPass}, {Name: "build", Status: StatusPass}}}).ID
	three := storeNamed(t, dir, Result{ID: fixtureID(3), Tree: "t", Checks: []CheckResult{{Name: "lint", Status: StatusPass}}}).ID

	for _, tc := range []struct {
		name, before, after string
		says                []string
	}{
		{"an after that is no id", one, "nosuch", []string{"after", `"nosuch"`}},
		{"a before that is no id", "nosuch", one, []string{"before", `"nosuch"`}},
		{"an id that is not stored", fixtureID(9), one, []string{"before", fixtureID(9)}},
		{"different checks", one, two, []string{"only before: lint", "only after: build"}},
		{"a check fewer", one, three, []string{"only before: test", "only after: none"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compare(dir, tc.before, tc.after)

			var usage *UsageError
			if !errors.As(err, &usage) {
				t.Fatalf("Compare = %v, want a *UsageError", err)
			}
			for _, part := range tc.says {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("the error %q does not say %q", err, part)
				}
			}
		})
	}
}
