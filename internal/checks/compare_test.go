package checks

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// fixtureID is the id the tests give their stored results: 12 hex digits made
// of the digit n.
func fixtureID(n int) string { return strings.Repeat(fmt.Sprintf("%x", n), 12) }

// storeFixture stores a result under id whose checks, named a, b, c, …, have
// the given statuses. It writes the result as a run would.
func storeFixture(t *testing.T, dir, id string, statuses ...string) Result {
	t.Helper()
	res := Result{ID: id, Tree: strings.Repeat("0", 64), TableSource: "file", Checks: []CheckResult{}, Skipped: []Skipped{}}
	res.Passed = len(statuses) > 0
	for i, status := range statuses {
		name := string(rune('a' + i))
		res.Checks = append(res.Checks, CheckResult{Name: name, Status: status, ExitCode: map[string]int{StatusPass: 0, StatusFail: 1}[status]})
		res.Passed = res.Passed && status == StatusPass
	}
	return storeNamed(t, dir, res)
}

// storeNamed stores res as it is.
func storeNamed(t *testing.T, dir string, res Result) Result {
	t.Helper()
	key := Key{ID: res.ID}
	if err := os.MkdirAll(key.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := storeResult(dir, key, res); err != nil {
		t.Fatal(err)
	}
	return res
}

// mustCompare is Compare, failing the test on an error.
func mustCompare(t *testing.T, dir, before, after string) Comparison {
	t.Helper()
	c, err := Compare(dir, before, after)
	if err != nil {
		t.Fatalf("Compare(%q, %q): %v", before, after, err)
	}
	return c
}

// TestCompareVerdictsPerCheck_TS_06_32 verifies TS-06-32:
// Per-check verdicts follow the four-row table with fail, error and timeout
// all counting as not pass.
// Verifies: 06-REQ-5.1
func TestCompareVerdictsPerCheck_TS_06_32(t *testing.T) {
	dir := t.TempDir()
	before := storeFixture(t, dir, fixtureID(1), StatusPass, StatusFail, StatusPass, StatusFail, StatusError, StatusPass)
	after := storeFixture(t, dir, fixtureID(2), StatusPass, StatusPass, StatusFail, StatusFail, StatusPass, StatusTimeout)

	c := mustCompare(t, dir, before.ID, after.ID)

	want := []Row{
		{"a", "pass", "pass", VerdictPass},
		{"b", "fail", "pass", VerdictPassWasAlreadyFailing},
		{"c", "pass", "fail", VerdictRegressed},
		{"d", "fail", "fail", VerdictStillFailing},
		{"e", "error", "pass", VerdictPassWasAlreadyFailing},
		{"f", "pass", "timeout", VerdictRegressed},
	}
	if !reflect.DeepEqual(c.Rows, want) {
		t.Errorf("rows = %+v, want %+v", c.Rows, want)
	}
	if c.Before != (Side{ID: before.ID, Passed: false}) || c.After != (Side{ID: after.ID, Passed: false}) {
		t.Errorf("before = %+v, after = %+v", c.Before, c.After)
	}
	if c.Verdict != VerdictRegressed || c.Landable {
		t.Errorf("verdict %s, landable %v; want regressed and not landable", c.Verdict, c.Landable)
	}

}

// TestCompareVerdictProperty_TS_06_33 verifies TS-06-33:
// The overall verdict is the highest-priority row verdict and landable is
// true only for pass and pass_was_already_failing.
// Verifies: 06-REQ-5.2
func TestCompareVerdictProperty_TS_06_33(t *testing.T) {
	statuses := []string{StatusPass, StatusFail, StatusError, StatusTimeout}
	order := []string{VerdictRegressed, VerdictStillFailing, VerdictPassWasAlreadyFailing, VerdictPass}
	rng := rand.New(rand.NewPCG(6, 33))

	for i := range 300 {
		dir := t.TempDir()
		n := 1 + rng.IntN(6)
		pick := func() []string {
			out := make([]string, n)
			for j := range out {
				out[j] = statuses[rng.IntN(len(statuses))]
			}
			return out
		}
		before := storeFixture(t, dir, fixtureID(1), pick()...)
		after := storeFixture(t, dir, fixtureID(2), pick()...)

		c := mustCompare(t, dir, before.ID, after.ID)

		var seen []string
		for _, r := range c.Rows {
			seen = append(seen, r.Verdict)
		}
		wantVerdict := order[slices.IndexFunc(order, func(v string) bool { return slices.Contains(seen, v) })]
		if c.Verdict != wantVerdict {
			t.Fatalf("#%d: verdict %s for rows %v, want %s", i, c.Verdict, seen, wantVerdict)
		}
		if landable := c.Verdict == VerdictPass || c.Verdict == VerdictPassWasAlreadyFailing; c.Landable != landable {
			t.Fatalf("#%d: landable %v with verdict %s", i, c.Landable, c.Verdict)
		}
		if len(c.Rows) != n {
			t.Fatalf("#%d: %d rows for %d checks", i, len(c.Rows), n)
		}
	}
}

// TestCompareWithNothingMeasured_TS_06_34 verifies TS-06-34:
// A result with no checks on either side compares as unverified and not
// landable.
// Verifies: 06-REQ-5.3
func TestCompareWithNothingMeasured_TS_06_34(t *testing.T) {
	dir := t.TempDir()
	full := storeFixture(t, dir, fixtureID(1), StatusPass, StatusPass)
	empty := storeFixture(t, dir, fixtureID(2))
	alsoEmpty := storeFixture(t, dir, fixtureID(3))

	for name, pair := range map[string][2]string{
		"empty then full": {empty.ID, full.ID},
		"full then empty": {full.ID, empty.ID},
		"both empty":      {empty.ID, alsoEmpty.ID},
	} {
		c, err := Compare(dir, pair[0], pair[1])
		if err != nil {
			t.Errorf("%s: %v, want no error", name, err)
			continue
		}
		if c.Verdict != VerdictUnverified || c.Landable || c.Rows == nil {
			t.Errorf("%s: verdict %s, landable %v, rows %v; want unverified, not landable and a list", name, c.Verdict, c.Landable, c.Rows)
		}
	}
}

// TestCompareRefusals_TS_06_36_and_37 verifies the library's part of TS-06-36
// and TS-06-37: an argument that names no result, and results whose checks
// differ, are usage errors that say which.
// Verifies: 06-REQ-5.5, 06-REQ-5.6
func TestCompareRefusals_TS_06_36_and_37(t *testing.T) {
	dir := t.TempDir()
	one := storeNamed(t, dir, Result{ID: fixtureID(1), Tree: strings.Repeat("0", 64), Checks: []CheckResult{{Name: "lint", Status: StatusPass}, {Name: "test", Status: StatusPass}}})
	two := storeNamed(t, dir, Result{ID: fixtureID(2), Tree: strings.Repeat("0", 64), Checks: []CheckResult{{Name: "test", Status: StatusPass}, {Name: "build", Status: StatusPass}}})

	for _, tc := range []struct {
		name          string
		before, after string
		contains      []string
	}{
		{"an unresolved after", one.ID, "nosuch", []string{"after", "nosuch"}},
		{"an unresolved before", "nosuch", one.ID, []string{"before", "nosuch"}},
		{"an id that is not stored", fixtureID(9), one.ID, []string{"before", fixtureID(9)}},
		{"different checks", one.ID, two.ID, []string{"only before: lint", "only after: build"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compare(dir, tc.before, tc.after)

			var usage *UsageError
			if !errors.As(err, &usage) {
				t.Fatalf("Compare = %v, want a *UsageError", err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(usage.Message, want) {
					t.Errorf("message %q does not mention %q", usage.Message, want)
				}
			}
		})
	}

}

// TestCacheProperty_TS_06_27 verifies TS-06-27:
// Any one-byte change to a viewed file forces a re-run and any change confined
// to ignored files is a cache hit.
// Verifies: 06-REQ-4.3
func TestCacheProperty_TS_06_27(t *testing.T) {
	requireSh(t)
	rng := rand.New(rand.NewPCG(6, 27))

	for i := range 12 {
		dir := newRepo(t)
		writeFile(t, dir, ".gitignore", "*.ignored\nignored_dir/\n", 0o644)
		var viewed []string
		for j := range 2 + rng.IntN(5) {
			name := fmt.Sprintf("d%d/f%d.txt", rng.IntN(3), j)
			writeFile(t, dir, name, fmt.Sprintf("content %d %d", j, rng.IntN(1_000_000)), 0o644)
			viewed = append(viewed, name)
		}
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-q", "-m", "feat: files")
		counter, check := counterCheck(t, "counted")
		table := tableOf(check)
		mustRun(t, dir, Options{Table: table})
		ran := 1

		for step := range 4 {
			ignored := rng.IntN(2) == 0
			if ignored {
				switch rng.IntN(3) {
				case 0:
					writeFile(t, dir, fmt.Sprintf("x%d.ignored", rng.IntN(3)), fmt.Sprint(rng.Int()), 0o644)
				case 1:
					writeFile(t, dir, fmt.Sprintf("ignored_dir/y%d", rng.IntN(3)), fmt.Sprint(rng.Int()), 0o755)
				default:
					_ = os.RemoveAll(filepath.Join(dir, "ignored_dir"))
					writeFile(t, dir, ".agent-fox/gate/noise.txt", fmt.Sprint(rng.Int()), 0o644)
				}
			} else {
				victim := filepath.Join(dir, filepath.FromSlash(viewed[rng.IntN(len(viewed))]))
				content, err := os.ReadFile(victim)
				if err != nil {
					t.Fatal(err)
				}
				content[rng.IntN(len(content))] ^= 1 << rng.IntN(7)
				if err := os.WriteFile(victim, content, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			res := mustRun(t, dir, Options{Table: table})

			if res.Cached != ignored {
				t.Fatalf("#%d step %d: cached = %v after a change to %s files, want %v", i, step, res.Cached, map[bool]string{true: "ignored", false: "viewed"}[ignored], ignored)
			}
			if !ignored {
				ran++
			}
			if runs(t, counter) != ran {
				t.Fatalf("#%d step %d: the check ran %d times, want %d", i, step, runs(t, counter), ran)
			}
		}
	}
}
