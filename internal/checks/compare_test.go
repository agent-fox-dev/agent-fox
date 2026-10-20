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
	if err := recordLabel(dir, "baseline", before.ID); err != nil {
		t.Fatal(err)
	}

	c := mustCompare(t, dir, "baseline", after.ID)

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
	if c.Before != (Side{ID: before.ID, Label: "baseline", Passed: false}) || c.After != (Side{ID: after.ID, Label: "", Passed: false}) {
		t.Errorf("before = %+v, after = %+v", c.Before, c.After)
	}
	if c.Verdict != VerdictRegressed || c.Landable {
		t.Errorf("verdict %s, landable %v; want regressed and not landable", c.Verdict, c.Landable)
	}

	// An argument is a label before it is an id: a label that is spelled like
	// the id of another result names its own.
	other := storeFixture(t, dir, fixtureID(3), StatusFail, StatusFail, StatusFail, StatusFail, StatusFail, StatusFail)
	labels := fmt.Sprintf(`{"baseline": %q, %q: %q}`, before.ID, other.ID, after.ID)
	if err := os.WriteFile(filepath.Join(dir, ".agent-fox", "gate", "labels.json"), []byte(labels), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mustCompare(t, dir, "baseline", other.ID); got.After.ID != after.ID {
		t.Errorf("%s resolved to the result %s; want the label's, %s", other.ID, got.After.ID, after.ID)
	}
}

// TestCompareLabelOfAnIDIsTheStoredOne guards 06-REQ-5.4: a result addressed
// by id reports the label it was stored under.
func TestCompareLabelOfAnIDIsTheStoredOne(t *testing.T) {
	dir := t.TempDir()
	res := storeFixture(t, dir, fixtureID(1), StatusPass)
	res.Label = "earlier"
	storeNamed(t, dir, res)

	c := mustCompare(t, dir, res.ID, res.ID)

	if c.Before.Label != "earlier" || c.After.Label != "earlier" || c.Verdict != VerdictPass || !c.Landable {
		t.Errorf("comparison = %+v", c)
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
	if err := recordLabel(dir, "baseline", one.ID); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name          string
		before, after string
		contains      []string
	}{
		{"an unresolved after", "baseline", "nosuch", []string{"--after", "nosuch"}},
		{"an unresolved before", "nosuch", "baseline", []string{"--before", "nosuch"}},
		{"an id that is not stored", fixtureID(9), "baseline", []string{"--before", fixtureID(9)}},
		{"different checks", "baseline", two.ID, []string{"lint", "build", "--before", "--after"}},
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

	// A label whose result was deleted is unresolved, and says so.
	if err := os.RemoveAll(Key{ID: one.ID}.Dir(dir)); err != nil {
		t.Fatal(err)
	}
	var usage *UsageError
	if _, err := Compare(dir, "baseline", two.ID); !errors.As(err, &usage) || !strings.Contains(usage.Message, one.ID) {
		t.Errorf("Compare with a label whose result is gone = %v, want a usage error naming %s", err, one.ID)
	}
}

// TestValidateLabel_TS_06_31 verifies the library's part of TS-06-31:
// a label is a lower-case word of at most 32 characters that is not an id.
// Verifies: 06-REQ-4.7
func TestValidateLabel_TS_06_31(t *testing.T) {
	for _, label := range []string{"", "baseline", "after", "ok_label-1", "a", strings.Repeat("a", 32), "z9", "abcdef01234x"} {
		if err := ValidateLabel(label); err != nil {
			t.Errorf("ValidateLabel(%q) = %v, want it accepted", label, err)
		}
	}
	for _, label := range []string{"Bad", "1abc", strings.Repeat("a", 33), "a b", "0123456789ab", "abcdef012345", "-x", "_x", "a/b", "a.b", "é", "a\n"} {
		err := ValidateLabel(label)
		var usage *UsageError
		if !errors.As(err, &usage) {
			t.Errorf("ValidateLabel(%q) = %v, want a *UsageError", label, err)
		}
	}
}

// TestRecordLabel guards 06-REQ-4.6: the latest id for a label wins, other
// labels stay, the file is written atomically, and one that is damaged starts
// again.
func TestRecordLabel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".agent-fox", "gate", "labels.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, rec := range [][2]string{{"baseline", fixtureID(1)}, {"after", fixtureID(2)}, {"baseline", fixtureID(3)}} {
		if err := recordLabel(dir, rec[0], rec[1]); err != nil {
			t.Fatal(err)
		}
	}

	data, _ := os.ReadFile(path)
	want := fmt.Sprintf("{\n  \"after\": %q,\n  \"baseline\": %q\n}\n", fixtureID(2), fixtureID(3))
	if string(data) != want {
		t.Errorf("labels.json = %q, want %q", data, want)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}

	if err := os.WriteFile(path, []byte("{damaged"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := recordLabel(dir, "again", fixtureID(4)); err != nil {
		t.Fatalf("a damaged labels.json stops the label: %v", err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), `"again"`) {
		t.Errorf("labels.json = %q", data)
	}
}

// TestRunRecordsTheLabel_TS_06_30 verifies the engine's part of TS-06-30:
// the label is recorded on a miss and on a hit, and the latest result for a
// label wins; an invalid label stops the run before anything runs.
// Verifies: 06-REQ-4.6, 06-REQ-4.7
func TestRunRecordsTheLabel_TS_06_30(t *testing.T) {
	requireSh(t)
	dir := newRepo(t)
	counter, check := counterCheck(t, "counted")
	table := tableOf(check)
	labels := filepath.Join(dir, ".agent-fox", "gate", "labels.json")

	first := mustRun(t, dir, Options{Table: table, Label: "baseline"})
	writeFile(t, dir, "README.md", "# changed\n", 0o644)
	second := mustRun(t, dir, Options{Table: table, Label: "baseline"})
	third := mustRun(t, dir, Options{Table: table, Label: "after"})

	if first.ID == second.ID || !third.Cached || third.ID != second.ID {
		t.Fatalf("ids %s, %s, %s; cached %v; want the tree to change between 1 and 2 and the third to be a hit of the second", first.ID, second.ID, third.ID, third.Cached)
	}
	data, _ := os.ReadFile(labels)
	want := fmt.Sprintf("{\n  \"after\": %q,\n  \"baseline\": %q\n}\n", second.ID, second.ID)
	if string(data) != want {
		t.Errorf("labels.json = %q, want %q", data, want)
	}
	for _, label := range []string{"Bad", "0123456789ab"} {
		_, err := Run(t.Context(), dir, Options{Table: table, Label: label, NoCache: true})
		var usage *UsageError
		if !errors.As(err, &usage) {
			t.Errorf("Run with the label %q = %v, want a *UsageError", label, err)
		}
	}
	if runs(t, counter) != 2 {
		t.Errorf("the check ran %d times, want 2: the invalid labels ran nothing", runs(t, counter))
	}
	if after, _ := os.ReadFile(labels); string(after) != want {
		t.Errorf("labels.json changed to %q", after)
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
