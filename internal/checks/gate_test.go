package checks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/project"
)

// requireSh skips the test where there is no POSIX shell to write checks in.
func requireSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the checks of these tests are shell scripts")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is not installed")
	}
}

// shCheck is a check that runs script with sh.
func shCheck(name, script string) project.Check {
	return project.Check{Name: name, Argv: []string{"sh", "-c", script}, Kind: project.KindCheck, Source: "test", Available: true}
}

// tableOf is a gate table of the given checks.
func tableOf(checks ...project.Check) project.Table {
	return project.Table{Source: project.SourceFile, Checks: checks}
}

// mustRun is Run, failing the test on an error.
func mustRun(t *testing.T, dir string, opts Options) Result {
	t.Helper()
	res, err := Run(context.Background(), dir, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

// counterCheck is a check that appends a line to a counter file outside the
// repository, which is how the tests tell how often a check ran.
func counterCheck(t *testing.T, name string) (counter string, check project.Check) {
	t.Helper()
	counter = filepath.Join(t.TempDir(), "counter")
	return counter, shCheck(name, fmt.Sprintf("echo run >> '%s'; echo \"ran %s\"", counter, name))
}

// runs is how many lines the counter file holds.
func runs(t *testing.T, counter string) int {
	t.Helper()
	data, err := os.ReadFile(counter)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

// readLog reads the log a result names, relative to dir.
func readLog(t *testing.T, dir string, c CheckResult) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(c.Log)))
	if err != nil {
		t.Fatalf("the log of %s: %v", c.Name, err)
	}
	return string(data)
}

// TestRunRecordsEachCheck_TS_06_19 verifies TS-06-19:
// A gate run executes the available checks in order, writes one full log
// each, and records statuses, exit codes, durations and tails.
// Verifies: 06-REQ-3.1, 06-REQ-3.3
func TestRunRecordsEachCheck_TS_06_19(t *testing.T) {
	requireSh(t)
	dir := newRepo(t)
	table := tableOf(
		shCheck("pass", `i=0; while [ $i -lt 100 ]; do echo "line $i"; i=$((i+1)); done`),
		shCheck("fail", `echo failing; echo oops >&2; exit 3`),
		project.Check{Name: "missing", Argv: []string{"no-such-program-for-af"}, Kind: project.KindTest, Available: false},
		project.Check{Name: "vanished", Argv: []string{"./vanished.sh"}, Kind: project.KindTest, Available: true},
		shCheck("slow", `sleep 30`),
	)

	res := mustRun(t, dir, Options{Table: table, CheckTimeout: 400 * time.Millisecond})

	var names, statuses []string
	var codes []int
	for _, c := range res.Checks {
		names, statuses, codes = append(names, c.Name), append(statuses, c.Status), append(codes, c.ExitCode)
	}
	if want := []string{"pass", "fail", "vanished", "slow"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("checks = %v, want %v: the available checks, in order", names, want)
	}
	if want := []string{"pass", "fail", "error", "timeout"}; !reflect.DeepEqual(statuses, want) {
		t.Errorf("statuses = %v, want %v", statuses, want)
	}
	if want := []int{0, 3, -1, -1}; !reflect.DeepEqual(codes, want) {
		t.Errorf("exit codes = %v, want %v", codes, want)
	}
	if res.Checks[3].DurationMS < 300 || res.Checks[3].DurationMS > 10_000 {
		t.Errorf("the slow check took %d ms, want about its 400 ms deadline", res.Checks[3].DurationMS)
	}

	for i, slug := range []string{"pass", "fail", "vanished", "slow"} {
		want := fmt.Sprintf(".agent-fox/gate/%s/%02d-%s.log", res.ID, i+1, slug)
		if res.Checks[i].Log != want {
			t.Errorf("log of %s = %q, want %q (relative to the directory)", slug, res.Checks[i].Log, want)
		}
	}
	pass := readLog(t, dir, res.Checks[0])
	if strings.Count(pass, "\n") != 100 {
		t.Errorf("the log of pass holds %d lines, want all 100", strings.Count(pass, "\n"))
	}
	if log := readLog(t, dir, res.Checks[1]); !strings.Contains(log, "failing") || !strings.Contains(log, "oops") {
		t.Errorf("the log of fail is %q, want the output and the error output combined", log)
	}
	if log := readLog(t, dir, res.Checks[2]); !strings.Contains(log, "vanished.sh") {
		t.Errorf("the log of a check that could not start is %q, want it to say why", log)
	}

	tail := res.Checks[0].Tail
	if lines := strings.Split(strings.TrimSuffix(tail, "\n"), "\n"); len(lines) != 40 || lines[0] != "line 60" || lines[39] != "line 99" {
		t.Errorf("tail has %d lines from %q to %q, want the last 40: line 60 to line 99", len(lines), lines[0], lines[len(lines)-1])
	}

	if want := []Skipped{{Name: "missing", Reason: "unavailable"}}; !reflect.DeepEqual(res.Skipped, want) {
		t.Errorf("skipped = %v, want %v", res.Skipped, want)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, ".agent-fox", "gate", res.ID))
	for _, e := range entries {
		if strings.Contains(e.Name(), "missing") {
			t.Errorf("a skipped check has a log: %s", e.Name())
		}
	}
}

// TestTailIsBoundedInBytes guards 06-REQ-3.3: the tail is capped at 4096
// bytes whatever the lines are, and never cuts a character in two.
func TestTailIsBoundedInBytes(t *testing.T) {
	requireSh(t)
	dir := newRepo(t)
	big := filepath.Join(t.TempDir(), "big.txt")
	long := strings.Repeat("x", 10_000) + "\n" + strings.Repeat("é", 3000) + "\n"
	if err := os.WriteFile(big, []byte(long), 0o644); err != nil {
		t.Fatal(err)
	}

	res := mustRun(t, dir, Options{Table: tableOf(shCheck("big", "cat '"+big+"'"))})

	tail := res.Checks[0].Tail
	if len(tail) == 0 || len(tail) > 4096 || !utf8.ValidString(tail) || !strings.HasSuffix(long, tail) {
		t.Errorf("tail is %d bytes, valid UTF-8 %v, a suffix of the output %v; want a valid suffix of at most 4096 bytes",
			len(tail), utf8.ValidString(tail), strings.HasSuffix(long, tail))
	}
	if log := readLog(t, dir, res.Checks[0]); log != long {
		t.Errorf("the log is %d bytes, want the whole output (%d)", len(log), len(long))
	}
}

// homeOf reads the HOME that the check named home printed.
func homeOf(t *testing.T, dir string, c CheckResult) string {
	t.Helper()
	line, _, _ := strings.Cut(readLog(t, dir, c), "\n")
	home, ok := strings.CutPrefix(line, "home=")
	if !ok || home == "" {
		t.Fatalf("the log of %s begins %q, want home=<path>", c.Name, line)
	}
	return home
}

// TestHermeticRunHasAHomeOfItsOwn_TS_06_20 verifies TS-06-20:
// --hermetic runs under HermeticRunner with a temporary HOME that is removed
// afterwards, including after cancellation.
// Verifies: 06-REQ-3.2
func TestHermeticRunHasAHomeOfItsOwn_TS_06_20(t *testing.T) {
	requireSh(t)
	realHome := os.Getenv("HOME")
	home := shCheck("home", `echo "home=$HOME"`)

	t.Run("completed", func(t *testing.T) {
		dir := newRepo(t)

		res := mustRun(t, dir, Options{Table: tableOf(home), Hermetic: true})

		seen := homeOf(t, dir, res.Checks[0])
		if !res.Hermetic || seen == realHome {
			t.Errorf("hermetic = %v, HOME seen = %q (real HOME %q); want true and another", res.Hermetic, seen, realHome)
		}
		if _, err := os.Stat(seen); err == nil {
			t.Errorf("the temporary HOME %s still exists", seen)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		dir := newRepo(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			for ctx.Err() == nil {
				if logs, _ := filepath.Glob(filepath.Join(dir, ".agent-fox", "gate", "*", "01-home.log")); len(logs) > 0 {
					cancel()
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()

		_, err := Run(ctx, dir, Options{Table: tableOf(home, shCheck("block", "sleep 60")), Hermetic: true})

		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want context.Canceled", err)
		}
		logs, _ := filepath.Glob(filepath.Join(dir, ".agent-fox", "gate", "*", "01-home.log"))
		if len(logs) != 1 {
			t.Fatalf("logs = %v", logs)
		}
		data, _ := os.ReadFile(logs[0])
		seen := strings.TrimPrefix(strings.SplitN(string(data), "\n", 2)[0], "home=")
		if seen == "" || seen == realHome {
			t.Fatalf("HOME seen = %q", seen)
		}
		if _, err := os.Stat(seen); err == nil {
			t.Errorf("the temporary HOME %s still exists after the run was cancelled", seen)
		}
	})
}

// TestResultFieldsAndPassed_TS_06_21 verifies TS-06-21:
// The result carries all fields and passed is true only when at least one
// check ran and all executed checks passed.
// Verifies: 06-REQ-3.4
func TestResultFieldsAndPassed_TS_06_21(t *testing.T) {
	requireSh(t)
	ok := func(name string) project.Check { return shCheck(name, "exit 0") }
	gone := project.Check{Name: "gone", Argv: []string{"no-such-program-for-af"}, Kind: project.KindTest}
	cases := []struct {
		name    string
		checks  []project.Check
		passed  bool
		ran     int
		skipped int
	}{
		{"all pass", []project.Check{ok("a"), ok("b")}, true, 2, 0},
		{"one fails", []project.Check{ok("a"), shCheck("b", "exit 1")}, false, 2, 0},
		{"a pass and a skipped check", []project.Check{ok("a"), gone}, true, 1, 1},
		{"nothing can run", []project.Check{gone}, false, 0, 1},
		{"no checks at all", nil, false, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := newRepo(t)

			res := mustRun(t, dir, Options{Table: tableOf(tc.checks...), Label: "x"})

			if res.Passed != tc.passed || len(res.Checks) != tc.ran || len(res.Skipped) != tc.skipped {
				t.Errorf("passed %v, %d ran, %d skipped; want %v, %d, %d", res.Passed, len(res.Checks), len(res.Skipped), tc.passed, tc.ran, tc.skipped)
			}
			data, err := json.Marshal(res)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"id", "tree", "table_source", "hermetic", "label", "cached", "passed", "checks", "skipped"} {
				if _, ok := fields[key]; !ok {
					t.Errorf("the result has no %q: %s", key, data)
				}
			}
			for _, key := range []string{"checks", "skipped"} {
				if _, ok := fields[key].([]any); !ok {
					t.Errorf("%s is %v, want an array even when empty", key, fields[key])
				}
			}
			if fields["table_source"] != "file" || fields["label"] != "x" || fields["cached"] != false || fields["hermetic"] != false {
				t.Errorf("table_source %v, label %v, cached %v, hermetic %v", fields["table_source"], fields["label"], fields["cached"], fields["hermetic"])
			}
			for _, s := range res.Skipped {
				if s.Reason == "" {
					t.Errorf("%s is skipped with no reason", s.Name)
				}
			}
			if !regexpID.MatchString(res.ID) || !hex64.MatchString(res.Tree) {
				t.Errorf("id %q, tree %q", res.ID, res.Tree)
			}
		})
	}
}

// TestNothingRunsWhenNothingCan guards 06-REQ-3.6: a table with no available
// check runs nothing and still gives a result that can be stored and cited.
func TestNothingRunsWhenNothingCan(t *testing.T) {
	requireSh(t)
	dir := newRepo(t)
	counter, check := counterCheck(t, "counted")
	check.Available = false

	res := mustRun(t, dir, Options{Table: tableOf(check)})

	if runs(t, counter) != 0 || len(res.Checks) != 0 || res.Passed {
		t.Errorf("%d runs, %d checks, passed %v; want nothing run", runs(t, counter), len(res.Checks), res.Passed)
	}
	if _, err := os.Stat(filepath.Join(dir, ".agent-fox", "gate", res.ID, "result.json")); err != nil {
		t.Errorf("an empty result is not stored: %v", err)
	}
}

// TestTreeChangedDuringTheRun_TS_06_23 verifies TS-06-23:
// A tree changed by the checks is stored under the earlier hash with the
// result flagged.
// Verifies: 06-REQ-3.7
func TestTreeChangedDuringTheRun_TS_06_23(t *testing.T) {
	requireSh(t)
	dir := newRepo(t)
	table := tableOf(shCheck("grows", "echo more >> README.md"))
	pre := mustHash(t, dir)

	res := mustRun(t, dir, Options{Table: table})

	if res.Tree != pre || !res.TreeChanged {
		t.Errorf("tree %s, changed %v; want the hash taken before the run (%s) and the flag", res.Tree, res.TreeChanged, pre)
	}
	if res.ID != KeyOf(pre, table.Checks, ModeReduced).ID {
		t.Errorf("id %s is not the key of the earlier tree", res.ID)
	}
	if mustHash(t, dir) == pre {
		t.Fatal("the check did not change the tree")
	}
	if _, err := os.Stat(filepath.Join(dir, ".agent-fox", "gate", res.ID, "result.json")); err != nil {
		t.Errorf("the result is not stored under the earlier id: %v", err)
	}
	stored := readResult(t, dir, res.ID)
	if stored.Tree != pre || stored.TreeChanged {
		t.Errorf("the stored result has tree %s and the flag %v; the flag belongs to the run, not to the result", stored.Tree, stored.TreeChanged)
	}
}

// readResult reads the stored result of id.
func readResult(t *testing.T, dir, id string) Result {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".agent-fox", "gate", id, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var res Result
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatalf("result.json is not JSON: %v\n%s", err, data)
	}
	return res
}

// TestKey_TS_06_25 verifies TS-06-25:
// The gate id is the first 12 hex characters of SHA-256 over the tree hash,
// the canonical JSON of the table and the runner mode.
// Verifies: 06-REQ-4.1
func TestKey_TS_06_25(t *testing.T) {
	tree := strings.Repeat("ab", 32)
	checks := []project.Check{
		{Name: "make check", Argv: []string{"make", "check"}, Kind: project.KindCheck, Source: "Makefile:check", Available: true},
		{Name: "vet & <test>", Argv: []string{"go", "vet", "./..."}, Kind: project.KindLint, Source: "go.mod"},
	}
	canon := `[{"argv":["make","check"],"kind":"check","name":"make check"},{"argv":["go","vet","./..."],"kind":"lint","name":"vet & <test>"}]`
	sum := func(mode string) string {
		h := sha256.Sum256([]byte(tree + canon + mode))
		return hex.EncodeToString(h[:])
	}

	reduced := KeyOf(tree, checks, ModeReduced)
	hermetic := KeyOf(tree, checks, ModeHermetic)

	if reduced.Sum != sum("reduced") || reduced.ID != sum("reduced")[:12] || !regexpID.MatchString(reduced.ID) {
		t.Errorf("reduced key = %+v, want SHA-256 %s", reduced, sum("reduced"))
	}
	if hermetic.Sum != sum("hermetic") || hermetic.ID == reduced.ID {
		t.Errorf("hermetic key = %+v, want SHA-256 %s and another id than %s", hermetic, sum("hermetic"), reduced.ID)
	}

	changed := append([]project.Check(nil), checks...)
	changed[0].Argv = []string{"make", "test"}
	if KeyOf(tree, changed, ModeReduced).ID == reduced.ID {
		t.Error("a different table gives the same id")
	}
	if KeyOf(strings.Repeat("cd", 32), checks, ModeReduced).ID == reduced.ID {
		t.Error("a different tree gives the same id")
	}

	// The same table written with its keys in another order, and without what
	// is about the machine or the provenance, is the same table.
	var reordered []project.Check
	doc := `[{"source":"x","name":"make check","kind":"check","argv":["make","check"],"available":false},
	         {"kind":"lint","argv":["go","vet","./..."],"available":true,"name":"vet & <test>","source":"y"}]`
	if err := json.Unmarshal([]byte(doc), &reordered); err != nil {
		t.Fatal(err)
	}
	if KeyOf(tree, reordered, ModeReduced).ID != reduced.ID {
		t.Error("the same table with its keys in another order, other sources and other availability gives another id")
	}

	if got := KeyOf(tree, nil, ModeReduced).Sum; got != func() string { h := sha256.Sum256([]byte(tree + "[]reduced")); return hex.EncodeToString(h[:]) }() {
		t.Errorf("the key of an empty table is %s", got)
	}
}

// TestCacheHit_TS_06_26 verifies TS-06-26:
// Two gate runs on an unchanged tree execute a counter-script check once and
// the second is cached.
// Verifies: 06-REQ-4.2
func TestCacheHit_TS_06_26(t *testing.T) {
	requireSh(t)
	dir := newRepo(t)
	counter, check := counterCheck(t, "counted")
	opts := Options{Table: tableOf(check)}

	first := mustRun(t, dir, opts)
	logs := listing(t, filepath.Join(dir, ".agent-fox", "gate", first.ID))
	opts.Label = "after"
	second := mustRun(t, dir, opts)

	if runs(t, counter) != 1 {
		t.Errorf("the check ran %d times, want once", runs(t, counter))
	}
	if first.Cached || !second.Cached || first.ID != second.ID {
		t.Errorf("cached %v then %v, ids %s and %s; want false then true, and one id", first.Cached, second.Cached, first.ID, second.ID)
	}
	if !reflect.DeepEqual(first.Checks, second.Checks) || first.Tree != second.Tree || first.Passed != second.Passed {
		t.Errorf("the hit reports %+v, want the stored checks %+v", second.Checks, first.Checks)
	}
	if second.Label != "after" {
		t.Errorf("label of the hit = %q, want the one asked for", second.Label)
	}
	if after := listing(t, filepath.Join(dir, ".agent-fox", "gate", first.ID)); !reflect.DeepEqual(after, logs) {
		t.Errorf("the hit changed the result directory: %v, was %v", after, logs)
	}
}

// listing is the names and modification times of a directory's entries.
func listing(t *testing.T, dir string) map[string]time.Time {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]time.Time{}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = info.ModTime()
	}
	return out
}

// TestNoCacheReplacesTheResult_TS_06_28 verifies TS-06-28:
// NoCache re-executes the checks and replaces the stored result.
// Verifies: 06-REQ-4.4
func TestNoCacheReplacesTheResult_TS_06_28(t *testing.T) {
	requireSh(t)
	dir := newRepo(t)
	counter := filepath.Join(t.TempDir(), "counter")
	check := shCheck("counted", fmt.Sprintf("echo run >> '%s'; echo \"this is run $(wc -l < '%s' | tr -d ' ')\"", counter, counter))
	first := mustRun(t, dir, Options{Table: tableOf(check)})

	second := mustRun(t, dir, Options{Table: tableOf(check), NoCache: true})

	if runs(t, counter) != 2 || second.Cached || second.ID != first.ID {
		t.Errorf("%d runs, cached %v, ids %s and %s; want 2, false and one id", runs(t, counter), second.Cached, first.ID, second.ID)
	}
	stored := readResult(t, dir, first.ID)
	if stored.Cached || !reflect.DeepEqual(stored.Checks, second.Checks) {
		t.Errorf("result.json = %+v, want the new run %+v", stored, second)
	}
	if log := readLog(t, dir, stored.Checks[0]); !strings.Contains(log, "this is run 2") {
		t.Errorf("the log is %q, want the new run's", log)
	}
}

// TestCachedResultWithSkippedChecks guards the cache against a result that
// was made where a program was missing: once the program can be run, the
// stored result is not the table's, and the checks run.
func TestCachedResultWithSkippedChecks(t *testing.T) {
	requireSh(t)
	dir := newRepo(t)
	counter, check := counterCheck(t, "counted")
	absent := check
	absent.Available = false
	first := mustRun(t, dir, Options{Table: tableOf(absent)})

	second := mustRun(t, dir, Options{Table: tableOf(check)})

	if first.ID != second.ID {
		t.Fatalf("availability changed the key: %s and %s", first.ID, second.ID)
	}
	if second.Cached || runs(t, counter) != 1 || len(second.Checks) != 1 {
		t.Errorf("cached %v, %d runs, %d checks; want a run now that the check can be run", second.Cached, runs(t, counter), len(second.Checks))
	}

	third := mustRun(t, dir, Options{Table: tableOf(absent)})
	if !third.Cached || len(third.Checks) != 1 {
		t.Errorf("cached %v, %d checks; a result that holds the measurement is good where the program is missing", third.Cached, len(third.Checks))
	}
}

// TestCorruptStoredResultIsReplaced guards 06-REQ-4.5: a result.json that is
// not a result is a miss, and is replaced.
func TestCorruptStoredResultIsReplaced(t *testing.T) {
	requireSh(t)
	dir := newRepo(t)
	counter, check := counterCheck(t, "counted")
	first := mustRun(t, dir, Options{Table: tableOf(check)})
	path := filepath.Join(dir, ".agent-fox", "gate", first.ID, "result.json")
	for name, junk := range map[string]string{"not JSON": "{", "another result": `{"id":"aaaaaaaaaaaa","passed":true}`} {
		if err := os.WriteFile(path, []byte(junk), 0o644); err != nil {
			t.Fatal(err)
		}

		res := mustRun(t, dir, Options{Table: tableOf(check)})

		if res.Cached || res.ID != first.ID {
			t.Errorf("%s: cached %v, id %s; want a run", name, res.Cached, res.ID)
		}
		if stored := readResult(t, dir, first.ID); stored.ID != first.ID {
			t.Errorf("%s: result.json was not replaced: %+v", name, stored)
		}
	}
	if runs(t, counter) != 3 {
		t.Errorf("the check ran %d times, want 3", runs(t, counter))
	}
}

// TestConcurrentRuns_TS_06_29 verifies TS-06-29:
// result.json is written after the logs, atomically, so concurrent runs never
// leave a partial file.
// Verifies: 06-REQ-4.5
func TestConcurrentRuns_TS_06_29(t *testing.T) {
	requireSh(t)
	dir := newRepo(t)
	table := tableOf(shCheck("slowish", "sleep 0.2; echo done"))

	stop := make(chan struct{})
	problems := make(chan string, 8)
	complain := func(format string, args ...any) {
		select {
		case problems <- fmt.Sprintf(format, args...):
		default:
		}
	}
	var reader sync.WaitGroup
	reader.Add(1)
	go func() {
		defer reader.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			matches, _ := filepath.Glob(filepath.Join(dir, ".agent-fox", "gate", "*", "result.json"))
			for _, path := range matches {
				data, err := os.ReadFile(path)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				var res Result
				if err != nil || json.Unmarshal(data, &res) != nil {
					complain("result.json is not complete JSON (%v): %q", err, data)
					continue
				}
				if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(res.Checks[0].Log))); err != nil {
					complain("result.json exists before its log: %v", err)
				}
			}
		}
	}()

	var runners sync.WaitGroup
	results := make([]Result, 10)
	errs := make([]error, 10)
	for i := range results {
		runners.Add(1)
		go func() {
			defer runners.Done()
			results[i], errs[i] = Run(context.Background(), dir, Options{Table: table, NoCache: true})
		}()
	}
	runners.Wait()
	close(stop)
	reader.Wait()

	close(problems)
	for p := range problems {
		t.Error(p)
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if results[i].ID != results[0].ID {
			t.Errorf("run %d has the id %s, run 0 %s", i, results[i].ID, results[0].ID)
		}
	}
	stored := readResult(t, dir, results[0].ID)
	if len(stored.Checks) != 1 || stored.Checks[0].Status != StatusPass {
		t.Errorf("result.json = %+v", stored)
	}
	readLog(t, dir, stored.Checks[0])
	entries, _ := os.ReadDir(filepath.Join(dir, ".agent-fox", "gate", results[0].ID))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
}
