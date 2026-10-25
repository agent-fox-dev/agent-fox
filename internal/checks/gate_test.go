package checks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// A run takes the available checks in order, one full log each, and goes on
// past a check that fails, cannot start, or outlives its deadline; that one
// is stopped with everything it started.
func TestRunRecordsEachCheckInOrder(t *testing.T) {
	testutil.Require(t, "sh")
	dir := newRepo(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	table := tableOf(
		shCheck("pass", `i=0; while [ $i -lt 100 ]; do echo "line $i"; i=$((i+1)); done`),
		shCheck("fail", `echo failing; echo oops >&2; exit 3`),
		project.Check{Name: "missing", Argv: []string{"no-such-program-for-af"}, Kind: project.KindTest},
		project.Check{Name: "Vanished!", Argv: []string{"./vanished.sh"}, Kind: project.KindTest, Available: true},
		shCheck("hangs", fmt.Sprintf("echo started; sleep 60 & echo $! > '%s'; wait", pidFile)),
		shCheck("after", "exit 0"),
	)

	res := mustRun(t, dir, Options{Table: table, CheckTimeout: 400 * time.Millisecond})

	var got []string
	for _, c := range res.Checks {
		got = append(got, fmt.Sprintf("%s %s %d", c.Name, c.Status, c.ExitCode))
	}
	want := []string{"pass pass 0", "fail fail 3", "Vanished! error -1", "hangs timeout -1", "after pass 0"}
	if !reflect.DeepEqual(got, want) || res.Passed {
		t.Fatalf("checks = %q, passed %v; want %q and not passed", got, res.Passed, want)
	}
	if !reflect.DeepEqual(res.Skipped, []Skipped{{Name: "missing", Reason: ReasonUnavailable}}) {
		t.Errorf("skipped = %+v", res.Skipped)
	}
	for i, slug := range []string{"pass", "fail", "vanished", "hangs", "after"} {
		if want := fmt.Sprintf(".agent-fox/gate/%s/%02d-%s.log", res.ID, i+1, slug); res.Checks[i].Log != want {
			t.Errorf("log of %s = %q, want %q", res.Checks[i].Name, res.Checks[i].Log, want)
		}
	}
	if d := res.Checks[3].DurationMS; d < 300 || d > 10_000 {
		t.Errorf("the check that hangs took %d ms, want about its 400 ms deadline", d)
	}
	logs := map[string]string{}
	for _, c := range res.Checks {
		logs[c.Name] = readLog(t, dir, c)
	}
	if n := strings.Count(logs["pass"], "\n"); n != 100 || !strings.HasPrefix(res.Checks[0].Tail, "line 60\n") {
		t.Errorf("the log of pass has %d lines and the tail starts %.10q; want all 100, and the last 40", n, res.Checks[0].Tail)
	}
	for name, part := range map[string]string{"fail": "oops", "Vanished!": "vanished.sh", "hangs": "did not finish within 400ms"} {
		if !strings.Contains(logs[name], part) {
			t.Errorf("the log of %s = %q, want %q in it", name, logs[name], part)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, ".agent-fox", "gate", res.ID)); len(entries) != 6 {
		t.Errorf("the result directory holds %d entries, want five logs and the result: a skipped check has no log", len(entries))
	}
	requireGone(t, pid(t, testutil.ReadFile(t, pidFile)))
}

// The tail is the last 40 lines, of those at most the last 4096 bytes, and
// never half a character.
func TestTailOf(t *testing.T) {
	lines := func(from, to int) string {
		var b strings.Builder
		for i := from; i < to; i++ {
			fmt.Fprintf(&b, "line %d\n", i)
		}
		return b.String()
	}
	long := strings.Repeat("x", 10_000) + "\n" + strings.Repeat("é", 3000) + "\n"
	for _, tc := range []struct{ name, in, want string }{
		{"a short output", "one\ntwo\n", "one\ntwo\n"},
		{"many lines", lines(0, 100), lines(60, 100)},
		{"a last line with no newline", lines(0, 50) + "end", lines(11, 50) + "end"},
		{"long lines", long, long[len(long)-4095:]}, // 4096 bytes would start inside an é
		{"nothing", "", ""},
	} {
		got := tailOf(tc.in)
		if got != tc.want || !utf8.ValidString(got) {
			t.Errorf("%s: tail is %d bytes from %.12q, want %d bytes from %.12q", tc.name, len(got), got, len(tc.want), tc.want)
		}
	}
}

func TestSlug(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"make check", "make-check"},
		{"  Über Test!! ", "ber-test"},
		{"go vet ./...", "go-vet"},
		{strings.Repeat("abc ", 20), strings.TrimSuffix(strings.Repeat("abc-", 10), "-")}, // 40 characters at most
		{"!!!", "check"},
	} {
		if got := slug(tc.name); got != tc.want {
			t.Errorf("slug(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A run passed when one check or more ran and every one passed, and it is
// stored even when nothing could run.
func TestRunPassedOnlyWhenEveryCheckThatRanPassed(t *testing.T) {
	testutil.Require(t, "sh")
	dir := newRepo(t)
	ok := func(name string) project.Check { return shCheck(name, "exit 0") }
	gone := project.Check{Name: "gone", Argv: []string{"no-such-program-for-af"}, Kind: project.KindTest}
	for _, tc := range []struct {
		name         string
		checks       []project.Check
		passed       bool
		ran, skipped int
	}{
		{"all pass", []project.Check{ok("a"), ok("b")}, true, 2, 0},
		{"one fails", []project.Check{ok("a"), shCheck("b", "exit 1")}, false, 2, 0},
		{"a pass and a skipped check", []project.Check{ok("a"), gone}, true, 1, 1},
		{"nothing can run", []project.Check{gone}, false, 0, 1},
		{"no checks at all", nil, false, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := mustRun(t, dir, Options{Table: tableOf(tc.checks...)})

			if res.Passed != tc.passed || len(res.Checks) != tc.ran || len(res.Skipped) != tc.skipped || res.Cached {
				t.Errorf("passed %v, %d ran, %d skipped, cached %v; want %v, %d, %d", res.Passed, len(res.Checks), len(res.Skipped), res.Cached, tc.passed, tc.ran, tc.skipped)
			}
			if data, _ := json.Marshal(res); !strings.Contains(string(data), `"checks":[`) || !strings.Contains(string(data), `"skipped":[`) {
				t.Errorf("the result is %s, want lists of checks and skipped, empty or not", data)
			}
			if stored := readResult(t, dir, res.ID); stored.ID != res.ID || stored.Tree != res.Tree || stored.TableSource != project.SourceFile {
				t.Errorf("the stored result = %+v", stored)
			}
		})
	}
}

// A tree that the checks changed is stored under the hash from before the
// run, and the run, not the stored result, says so.
func TestRunOfChecksThatChangeTheTree(t *testing.T) {
	testutil.Require(t, "sh")
	dir := newRepo(t)
	table := tableOf(shCheck("grows", "echo more >> README.md"))
	pre := mustHash(t, dir)

	res := mustRun(t, dir, Options{Table: table})

	if res.Tree != pre || !res.TreeChanged || res.ID != KeyOf(pre, table.Checks).ID || mustHash(t, dir) == pre {
		t.Errorf("tree %s, changed %v, id %s; want the hash taken before the run, %s, and the flag", res.Tree, res.TreeChanged, res.ID, pre)
	}
	if stored := readResult(t, dir, res.ID); stored.Tree != pre || stored.TreeChanged {
		t.Errorf("the stored result has tree %s and the flag %v", stored.Tree, stored.TreeChanged)
	}
}

// The steps run in order on one repository; each says whether the checks ran
// again or the stored result was the answer.
func TestRunIsCachedByTreeAndTable(t *testing.T) {
	testutil.Require(t, "sh")
	dir := newRepo(t)
	writeFile(t, dir, ".gitignore", "*.ignored\n", 0o644)
	testutil.Commit(t, dir, "chore: ignore")
	counter, check := counterCheck(t, "counted")
	absent := check
	absent.Available = false
	var last Result
	spoil := func(content string) func() {
		return func() { writeFile(t, dir, ".agent-fox/gate/"+last.ID+"/result.json", content, 0o644) }
	}
	for _, step := range []struct {
		name   string
		change func()
		check  project.Check
		cached bool
	}{
		{"the first run", func() {}, check, false},
		{"the same tree", func() {}, check, true},
		{"an ignored file changes", func() { writeFile(t, dir, "x.ignored", "x", 0o644) }, check, true},
		{"a file of .agent-fox changes", func() { writeFile(t, dir, ".agent-fox/noise", "x", 0o644) }, check, true},
		{"a byte of a file changes", func() { writeFile(t, dir, "README.md", "# tesT\n", 0o644) }, check, false},
		{"an executable bit changes", func() { writeFile(t, dir, "README.md", "# tesT\n", 0o755) }, check, false},
		{"a stored result that is not JSON", spoil("{"), check, false},
		{"a stored result of another id", spoil(`{"id":"aaaaaaaaaaaa","tree":"x","passed":true}`), check, false},
		// A result made where the program was missing says nothing about its
		// check; one that measured the check is good where the program is missing.
		{"a new tree where the program is missing", func() { writeFile(t, dir, "new.txt", "n", 0o644) }, absent, false},
		{"the program is there again", func() {}, check, false},
		{"the program is missing again", func() {}, absent, true},
	} {
		before := runs(t, counter)
		step.change()

		res := mustRun(t, dir, Options{Table: tableOf(step.check)})

		ran := runs(t, counter) - before
		if res.Cached != step.cached || ran != map[bool]int{true: 0, false: 1}[step.cached || !step.check.Available] {
			t.Fatalf("%s: cached %v, the check ran %d times; want cached %v", step.name, res.Cached, ran, step.cached)
		}
		if step.cached && (res.ID != last.ID || !reflect.DeepEqual(res.Checks, last.Checks) || res.TableSource != project.SourceFile) {
			t.Errorf("%s: the hit is %+v, want the stored result %+v", step.name, res, last)
		}
		if stored := readResult(t, dir, res.ID); stored.ID != res.ID {
			t.Errorf("%s: the stored result is %+v", step.name, stored)
		}
		last = res
	}
}

// The id is the first 12 hex characters of the SHA-256 of the tree hash, the
// canonical JSON of the checks (what a check is, with its keys in order) and
// the mode; where a check came from and whether it can run here are not in it.
func TestKeyOf(t *testing.T) {
	tree := strings.Repeat("ab", 32)
	checks := []project.Check{
		{Name: "make check", Argv: []string{"make", "check"}, Kind: project.KindCheck, Source: "Makefile:check", Available: true},
		{Name: "vet & <test>", Argv: []string{"go", "vet", "./..."}, Kind: project.KindLint, Source: "go.mod"},
	}
	canon := `[{"argv":["make","check"],"kind":"check","name":"make check"},{"argv":["go","vet","./..."],"kind":"lint","name":"vet & <test>"}]`
	sum := testutil.SHA256(tree + canon + "reduced")
	elsewhere := []project.Check{checks[0], checks[1]}
	elsewhere[0].Source, elsewhere[0].Available, elsewhere[1].Available = "x", false, true
	changed := []project.Check{checks[0], checks[1]}
	changed[0].Argv = []string{"make", "test"}

	key := KeyOf(tree, checks)

	if key.Sum != sum || key.ID != sum[:12] {
		t.Errorf("KeyOf = %+v, want the SHA-256 %s", key, sum)
	}
	if KeyOf(tree, elsewhere) != key {
		t.Error("another source and other availability give another key")
	}
	if KeyOf(tree, changed).ID == key.ID || KeyOf(strings.Repeat("cd", 32), checks).ID == key.ID {
		t.Error("another table or another tree gives the same key")
	}
	if got := KeyOf(tree, nil).Sum; got != testutil.SHA256(tree+"[]reduced") {
		t.Errorf("the key of an empty table is %s", got)
	}
}

// Runs together write the result after its logs and atomically, so a reader
// never sees a partial result, or one before its logs.
func TestConcurrentRunsNeverShowAPartialResult(t *testing.T) {
	testutil.Require(t, "sh")
	dir := newRepo(t)
	table := tableOf(shCheck("slowish", "sleep 0.2; echo done"))
	stop := make(chan struct{})
	problems := make(chan string, 8)
	var reader sync.WaitGroup
	reader.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			matches, _ := filepath.Glob(filepath.Join(dir, ".agent-fox", "gate", "*", "result.json"))
			for _, path := range matches {
				data, err := os.ReadFile(path)
				var res Result
				switch {
				case errors.Is(err, os.ErrNotExist):
				case err != nil || json.Unmarshal(data, &res) != nil:
					problems <- fmt.Sprintf("result.json is not complete JSON (%v): %q", err, data)
					return
				default:
					if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(res.Checks[0].Log))); err != nil {
						problems <- fmt.Sprintf("result.json is there before its log: %v", err)
						return
					}
				}
			}
		}
	})

	var runners sync.WaitGroup
	ids := make([]string, 6)
	for i := range ids {
		runners.Go(func() {
			res, err := Run(context.Background(), dir, Options{Table: table})
			if err != nil {
				problems <- fmt.Sprintf("run %d: %v", i, err)
			}
			ids[i] = res.ID
		})
	}
	runners.Wait()
	close(stop)
	reader.Wait()
	close(problems)

	for p := range problems {
		t.Error(p)
	}
	stored := readResult(t, dir, ids[0])
	if len(stored.Checks) != 1 || stored.Checks[0].Status != StatusPass {
		t.Errorf("result.json = %+v", stored)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, ".agent-fox", "gate", ids[0]))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
	for i, id := range ids {
		if id != ids[0] {
			t.Errorf("run %d has the id %s, run 0 %s", i, id, ids[0])
		}
	}
}

// A run that its context stops ends the check with everything it started,
// stores nothing, and returns the context's error; the next run runs.
func TestRunStoppedByItsContext(t *testing.T) {
	testutil.Require(t, "sh")
	dir := newRepo(t)
	proceed, ready := filepath.Join(t.TempDir(), "proceed"), fifo(t)
	check := shCheck("spawns", fmt.Sprintf("if [ -e '%s' ]; then exit 0; fi; sleep 60 & echo $! > '%s'; wait", proceed, ready))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	child := make(chan string, 1)
	go func() {
		data, _ := os.ReadFile(ready) // returns once the check has started its child
		child <- string(data)
		cancel()
	}()

	_, err := Run(ctx, dir, Options{Table: tableOf(check)})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	requireGone(t, pid(t, <-child))
	if written, _ := filepath.Glob(filepath.Join(dir, ".agent-fox", "gate", "*", "*")); len(written) != 0 {
		t.Errorf("a stopped run wrote %v", written)
	}
	if err := os.WriteFile(proceed, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if res := mustRun(t, dir, Options{Table: tableOf(check)}); res.Cached || !res.Passed {
		t.Errorf("the next run: cached %v, passed %v; want a run that passes", res.Cached, res.Passed)
	}
}
