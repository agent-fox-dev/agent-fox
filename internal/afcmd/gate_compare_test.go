package afcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// compareRun runs af gate compare in dir and returns the exit code and the
// envelope.
func compareRun(t *testing.T, dir string, args ...string) (int, map[string]any) {
	t.Helper()
	code, stdout, stderr := run(t, append([]string{"gate", "compare", "--dir", dir}, args...)...)
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
	return code, oneDocument(t, stdout)
}

// tick changes a file of the tree, so that the next gate run is another key.
func tick(t *testing.T, dir string, n int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "tick.txt"), []byte(strings.Repeat("x", n)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// idOf is the id of the result of an envelope, or "".
func idOf(env map[string]any) string {
	id, _ := field(env, "result", "id").(string)
	return id
}

// labelsBytes is labels.json as it is, or nothing.
func labelsBytes(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".agent-fox", "gate", "labels.json"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

// TestGateRunLabels_TS_06_30 verifies TS-06-30:
// --label records the id in labels.json on a miss and on a hit, and the
// latest result for a label wins.
// Verifies: 06-REQ-4.6
func TestGateRunLabels_TS_06_30(t *testing.T) {
	requireSh(t)
	dir := gitRepo(t, map[string]string{"tick.txt": "0\n"})
	counter := filepath.Join(t.TempDir(), "counter")
	writeGate(t, dir, counterScript(counter))

	_, first := gateRun(t, dir, "--label", "baseline")
	tick(t, dir, 1)
	_, second := gateRun(t, dir, "--label", "baseline")
	_, third := gateRun(t, dir, "--label", "after")

	for i, env := range []map[string]any{first, second, third} {
		if want := []string{"baseline", "baseline", "after"}[i]; field(env, "result", "label") != want {
			t.Errorf("run %d: label = %v, want %s", i+1, field(env, "result", "label"), want)
		}
	}
	if field(first, "result", "id") == field(second, "result", "id") || field(third, "result", "cached") != true || field(third, "result", "id") != field(second, "result", "id") {
		t.Fatalf("ids %v, %v, %v, third cached %v; want the second to be another tree and the third its hit",
			field(first, "result", "id"), field(second, "result", "id"), field(third, "result", "id"), field(third, "result", "cached"))
	}
	labels := labelsBytes(t, dir)
	for _, want := range []string{`"baseline": "` + idOf(second) + `"`, `"after": "` + idOf(third) + `"`} {
		if !strings.Contains(labels, want) {
			t.Errorf("labels.json = %s, want it to hold %s", labels, want)
		}
	}
	if strings.Contains(labels, idOf(first)) {
		t.Errorf("labels.json = %s, still holds the first result's id", labels)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, ".agent-fox", "gate"))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("a temporary file was left beside labels.json: %s", e.Name())
		}
	}
}

// TestGateRunRefusesBadLabels_TS_06_31 verifies TS-06-31:
// An invalid or id-shaped label is a usage error before any check runs and
// leaves labels.json unchanged.
// Verifies: 06-REQ-4.7
func TestGateRunRefusesBadLabels_TS_06_31(t *testing.T) {
	requireSh(t)
	dir := gitRepo(t, map[string]string{"tick.txt": "0\n"})
	counter := filepath.Join(t.TempDir(), "counter")
	writeGate(t, dir, counterScript(counter))
	gateRun(t, dir, "--label", "kept")
	ran, labels := lines(t, counter), labelsBytes(t, dir)
	tick(t, dir, 1)

	for _, bad := range []string{"Bad", "1abc", strings.Repeat("a", 33), "a b", "0123456789ab", "abcdef012345"} {
		code, env := gateRun(t, dir, "--label", bad)

		if code != 2 || env["status"] != "usage" || field(env, "error", "category") != "usage" {
			t.Errorf("--label %q: exit code %d, status %v; want 2 and usage", bad, code, env["status"])
		}
		if msg, _ := field(env, "error", "message").(string); !strings.Contains(msg, "--label") {
			t.Errorf("--label %q: message %q does not name the flag", bad, msg)
		}
		if lines(t, counter) != ran || labelsBytes(t, dir) != labels {
			t.Errorf("--label %q: the counter is %d (was %d) and labels.json changed: %v", bad, lines(t, counter), ran, labelsBytes(t, dir) != labels)
		}
	}

	if code, env := gateRun(t, dir, "--label", "ok_label-1"); code != 0 || field(env, "result", "label") != "ok_label-1" {
		t.Errorf("--label ok_label-1: exit code %d, label %v; want 0 and the label", code, field(env, "result", "label"))
	}
}

// TestGateCompareVerdicts_TS_06_35 verifies TS-06-35:
// gate compare returns a done envelope with exit 0 and both sides' id, label
// and passed for each of the five verdicts.
// Verifies: 06-REQ-5.4
func TestGateCompareVerdicts_TS_06_35(t *testing.T) {
	requireSh(t)
	dir := gitRepo(t, map[string]string{"tick.txt": "0\n"})
	marker := filepath.Join(t.TempDir(), "marker")
	writeGate(t, dir, []string{"m", "sh", "-c", "test -e '" + marker + "'"})
	step := 0
	measure := func(label string, green bool) {
		t.Helper()
		if green {
			if err := os.WriteFile(marker, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		} else {
			_ = os.Remove(marker)
		}
		step++
		tick(t, dir, step)
		if code, env := gateRun(t, dir, "--label", label); code != 0 || field(env, "result", "passed") != green {
			t.Fatalf("%s: exit code %d, passed %v; want 0 and %v", label, code, field(env, "result", "passed"), green)
		}
	}
	measure("pass-before", true)
	measure("pass-after", true)
	measure("paf-before", false)
	measure("paf-after", true)
	measure("reg-before", true)
	measure("reg-after", false)
	measure("still-before", false)
	measure("still-after", false)
	writeGate(t, dir) // a table with no check
	step++
	tick(t, dir, step)
	gateRun(t, dir, "--label", "empty-after")

	for _, tc := range []struct {
		before, after, verdict string
		landable               bool
		passedBefore, passedAf bool
	}{
		{"pass-before", "pass-after", "pass", true, true, true},
		{"paf-before", "paf-after", "pass_was_already_failing", true, false, true},
		{"reg-before", "reg-after", "regressed", false, true, false},
		{"still-before", "still-after", "still_failing", false, false, false},
		{"pass-before", "empty-after", "unverified", false, true, false},
	} {
		t.Run(tc.verdict, func(t *testing.T) {
			code, env := compareRun(t, dir, "--before", tc.before, "--after", tc.after)

			if code != 0 || env["status"] != "done" || env["tool"] != "gate compare" || env["summary"] == "" {
				t.Fatalf("exit code %d, status %v; want 0 and done", code, env["status"])
			}
			if field(env, "result", "verdict") != tc.verdict || field(env, "result", "landable") != tc.landable {
				t.Errorf("verdict %v, landable %v; want %s and %v", field(env, "result", "verdict"), field(env, "result", "landable"), tc.verdict, tc.landable)
			}
			before, _ := field(env, "result", "before").(map[string]any)
			after, _ := field(env, "result", "after").(map[string]any)
			if before["label"] != tc.before || after["label"] != tc.after || before["passed"] != tc.passedBefore || after["passed"] != tc.passedAf {
				t.Errorf("before = %v, after = %v; want the labels %s and %s and passed %v and %v", before, after, tc.before, tc.after, tc.passedBefore, tc.passedAf)
			}
			if id, _ := before["id"].(string); len(id) != 12 {
				t.Errorf("before.id = %v", before["id"])
			}
			rows, _ := field(env, "result", "rows").([]any)
			if tc.verdict == "unverified" {
				if len(rows) != 0 {
					t.Errorf("rows = %v, want none", rows)
				}
			} else if len(rows) != 1 || field(rows[0].(map[string]any), "verdict") != tc.verdict || field(rows[0].(map[string]any), "name") != "m" {
				t.Errorf("rows = %v, want one row for m with the verdict %s", rows, tc.verdict)
			}
		})
	}

	// Addressed by id as well as by label.
	_, env := compareRun(t, dir, "--before", "pass-before", "--after", "pass-after")
	beforeID, _ := field(env, "result", "before", "id").(string)
	afterID, _ := field(env, "result", "after", "id").(string)
	if code, byID := compareRun(t, dir, "--before", beforeID, "--after", afterID); code != 0 || field(byID, "result", "verdict") != "pass" {
		t.Errorf("by id: exit code %d, verdict %v", code, field(byID, "result", "verdict"))
	}
}

// TestGateCompareRefusals_TS_06_36_and_37 verifies TS-06-36 and TS-06-37:
// a missing or unresolved --before/--after, and results of different checks,
// are usage errors that say which.
// Verifies: 06-REQ-5.5, 06-REQ-5.6
func TestGateCompareRefusals_TS_06_36_and_37(t *testing.T) {
	requireSh(t)
	dir := gitRepo(t, map[string]string{"tick.txt": "0\n"})
	ok := func(name string) []string { return []string{name, "sh", "-c", "exit 0"} }
	writeGate(t, dir, ok("lint"), ok("test"))
	gateRun(t, dir, "--label", "baseline")
	tick(t, dir, 1)
	writeGate(t, dir, ok("test"), ok("build"))
	gateRun(t, dir, "--label", "other")

	for _, tc := range []struct {
		name     string
		args     []string
		contains []string
	}{
		{"no --before", nil, []string{"--before"}},
		{"no --after", []string{"--before", "baseline"}, []string{"--after"}},
		{"an unresolved --after", []string{"--before", "baseline", "--after", "nosuch"}, []string{"--after", "nosuch"}},
		{"an unresolved --before", []string{"--before", "nosuch", "--after", "baseline"}, []string{"--before", "nosuch"}},
		{"different checks", []string{"--before", "baseline", "--after", "other"}, []string{"only in --before: lint", "only in --after: build"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, env := compareRun(t, dir, tc.args...)

			if code != 2 || env["status"] != "usage" {
				t.Fatalf("exit code %d, status %v; want 2 and usage", code, env["status"])
			}
			msg, _ := field(env, "error", "message").(string)
			for _, want := range tc.contains {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q does not mention %q", msg, want)
				}
			}
		})
	}

	// `test` is on both sides and is not what differs.
	_, env := compareRun(t, dir, "--before", "baseline", "--after", "other")
	if msg, _ := field(env, "error", "message").(string); strings.Contains(msg, "only in --before: lint, test") || strings.Contains(msg, "build, test") {
		t.Errorf("message %q lists a check that both results have", msg)
	}
}
