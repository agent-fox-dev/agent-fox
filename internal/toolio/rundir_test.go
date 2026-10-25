package toolio

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// scriptedRand is a random source whose n-th read yields chunks[n], and the
// last chunk for every read after that.
type scriptedRand struct {
	chunks [][]byte
	draws  int
}

func (s *scriptedRand) Read(p []byte) (int, error) {
	chunk := s.chunks[min(s.draws, len(s.chunks)-1)]
	s.draws++
	return copy(p, chunk), nil
}

// fixedApp is an app whose clock stands at 2026-10-20 07:26:48 UTC and whose
// random source yields chunks.
func fixedApp(chunks ...[]byte) (*App, *scriptedRand) {
	rnd := &scriptedRand{chunks: chunks}
	app := newApp()
	app.Now = func() time.Time { return time.Date(2026, 10, 20, 9, 26, 48, 0, time.FixedZone("plus2", 7200)) }
	app.Rand = rnd
	return app, rnd
}

func TestRunDir_IsMadeUnderTheDirectoryBeforeTheCommandRuns(t *testing.T) {
	dir := t.TempDir()
	existed := false
	app, _ := fixedApp([]byte{0xab, 0xcd, 0xef})
	app.Register(Command{Path: "X", UsesRunDir: true, Run: func(r *Run) (Outcome, error) {
		_, err := os.Stat(r.RunDir.Path)
		existed = err == nil && r.RunDir.ID == "20261020_072648_abcdef"
		return Outcome{Status: StatusDone, Summary: "ok"}, r.RunDir.WriteReport("# the report\n")
	}})
	app.Register(Command{Path: "bare", Dir: true, Run: func(*Run) (Outcome, error) { return Outcome{Status: StatusDone}, nil }})

	env := run(t, app, "X", "--dir", dir).env(t)

	path := filepath.Join(dir, ".agent-fox", "run", "20261020_072648_abcdef")
	want := []any{map[string]any{"kind": "run_dir", "path": path}, map[string]any{"kind": "report", "path": filepath.Join(path, "report.md")}}
	if env["run_id"] != "20261020_072648_abcdef" || !existed || !reflect.DeepEqual(env["artifacts"], want) {
		t.Errorf("run_id %v, the directory existed when the command ran: %v, artifacts %v", env["run_id"], existed, env["artifacts"])
	}
	if got := testutil.ReadFile(t, filepath.Join(path, "report.md")); got != "# the report\n" {
		t.Errorf("report.md = %q", got)
	}

	other := t.TempDir()
	if env := run(t, app, "bare", "--dir", other).env(t); env["run_id"] != nil {
		t.Errorf("run_id = %v for a command without a run directory, want null", env["run_id"])
	}
	if _, err := os.Stat(agentFoxDir(other)); err == nil {
		t.Error("a command without a run directory made one")
	}
}

func TestRunDir_ARefusedInvocationMakesNothing(t *testing.T) {
	parent := t.TempDir()
	file := testutil.WriteFile(t, parent, "file.txt", "x")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"a missing directory", []string{"--dir", filepath.Join(parent, "missing")}},
		{"a file", []string{"--dir", file}},
		{"a blank input", []string{"   ", "--dir", parent}},
	} {
		var rec recorder
		app := newApp()
		app.Register(rec.command(Command{Path: "X", UsesRunDir: true, Input: InputOptional}))

		r := run(t, app, append([]string{"X"}, tc.args...)...)

		if r.code != 2 || rec.ran != 0 {
			t.Errorf("%s: exit %d, ran %d; want usage", tc.name, r.code, rec.ran)
		}
		if entries, _ := os.ReadDir(parent); len(entries) != 1 {
			t.Errorf("%s: %s now holds %v", tc.name, parent, entries)
		}
	}
}

func TestRunDir_ATakenIDIsDrawnAgainUpToFiveTimes(t *testing.T) {
	taken := []byte{1, 2, 3}
	for _, tc := range []struct {
		chunks [][]byte
		runID  any
	}{
		{[][]byte{taken, taken, taken, taken, {9, 9, 9}}, "20261020_072648_090909"},
		{[][]byte{taken}, nil},
	} {
		dir := t.TempDir()
		testutil.WriteFile(t, filepath.Join(agentFoxDir(dir), "run", "20261020_072648_010203"), "keep", "")
		var rec recorder
		app, rnd := fixedApp(tc.chunks...)
		app.Register(rec.command(Command{Path: "X", UsesRunDir: true}))

		r := run(t, app, "X", "--dir", dir)
		env := r.env(t)

		if env["run_id"] != tc.runID || rnd.draws != 5 {
			t.Errorf("run_id %v after %d draws, want %v after 5", env["run_id"], rnd.draws, tc.runID)
		}
		if tc.runID == nil && (r.code != 1 || at(env, "error", "category") != CategoryIO || rec.ran != 0) {
			t.Errorf("five taken ids: exit %d, envelope %s; want an io failure", r.code, r.stdout)
		}
	}
	if _, err := NewRunID(nil, failingRand{}); err == nil {
		t.Error("NewRunID with a random source that fails succeeded")
	}
}

// failingRand is a random source that fails.
type failingRand struct{}

func (failingRand) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

func TestRunDir_TheCallerMayChooseItsID(t *testing.T) {
	for _, tc := range []struct {
		id    string
		taken bool
		code  int
	}{
		{"20250102_030405_abc123", false, 0},
		{"20261231_235959_0f9e8d", false, 0},
		{"20250102_030405_abc123", true, 1},
		{"run-1", false, 2},
		{"20250102_030405_ABC123", false, 2},
		{"20250102_030405_abc12", false, 2},
		{"../20250102_030405_abc123", false, 2},
		{"20250102_030405_abc123/x", false, 2},
	} {
		dir := t.TempDir()
		if tc.taken {
			testutil.WriteFile(t, filepath.Join(agentFoxDir(dir), "run", tc.id), "keep", "")
		}
		var rec recorder
		app := newApp()
		app.RunID = tc.id
		app.Register(rec.command(Command{Path: "X", UsesRunDir: true}))

		r := run(t, app, "X", "--dir", dir)
		env := r.env(t)

		if r.code != tc.code || (tc.code == 0) != (rec.ran == 1) {
			t.Errorf("%q (taken %v): exit %d, ran %d, envelope %s; want exit %d", tc.id, tc.taken, r.code, rec.ran, r.stdout, tc.code)
		}
		if tc.code == 0 && (env["run_id"] != tc.id || rec.last.RunDir.Path != filepath.Join(agentFoxDir(dir), "run", tc.id)) {
			t.Errorf("%q: run_id %v, run directory %+v", tc.id, env["run_id"], rec.last.RunDir)
		}
		if _, err := os.Stat(agentFoxDir(dir)); tc.code == 2 && err == nil {
			t.Errorf("%q: a malformed id made %s", tc.id, agentFoxDir(dir))
		}
	}
}

func TestRunDir_GitIgnoresItWithoutTouchingTheRepository(t *testing.T) {
	repo := testutil.Repo(t, map[string]string{".gitignore": "bin/\n"})
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Go(func() { _, errs[i] = NewRunDir(repo, nil, nil) })
	}
	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		t.Errorf("runs that started together failed: %v", err)
	}
	if got := testutil.ReadFile(t, filepath.Join(agentFoxDir(repo), ".gitignore")); got != "*\n" {
		t.Errorf(".agent-fox/.gitignore = %q", got)
	}
	if status := testutil.Git(t, repo, "status", "--porcelain"); status != "" || testutil.ReadFile(t, filepath.Join(repo, ".gitignore")) != "bin/\n" {
		t.Errorf("git status --porcelain:\n%s", status)
	}

	other := t.TempDir()
	custom := testutil.WriteFile(t, agentFoxDir(other), ".gitignore", "something else\n")
	if _, err := NewRunDir(other, nil, nil); err != nil || testutil.ReadFile(t, custom) != "something else\n" {
		t.Errorf("an existing .agent-fox/.gitignore: %v, now %q", err, testutil.ReadFile(t, custom))
	}
}

func TestOpenRunDir_OpensAnExistingRunAndNothingElse(t *testing.T) {
	dir := t.TempDir()
	created, err := NewRunDir(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if opened, err := OpenRunDir(dir, created.ID); err != nil || opened.Path != created.Path || opened.ID != created.ID {
		t.Errorf("OpenRunDir = %+v, %v; want %+v", opened, err, created)
	}
	before := testutil.Files(t, dir)
	for _, id := range []string{"../x", "", "..", "a/b", "20261020_072648_abcdeg", "20261020_072648_ABCDEF", "20261020_072648_abcdef"} {
		var failure *Failure
		if rd, err := OpenRunDir(dir, id); rd != nil || !errors.As(err, &failure) || failure.Category != CategoryUsage {
			t.Errorf("OpenRunDir(%q) = %v, %v; want a usage failure", id, rd, err)
		}
	}
	if _, err := OpenRunDir(filepath.Join(dir, "missing"), created.ID); err == nil {
		t.Error("OpenRunDir of a missing directory succeeded")
	}
	if after := testutil.Files(t, dir); !reflect.DeepEqual(after, before) {
		t.Errorf("opening runs changed %s", dir)
	}
}

func TestLedger_ReadsBackWhatWasRecordedInOrder(t *testing.T) {
	dir := t.TempDir()
	rd, err := NewRunDir(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if entries, err := rd.Ledger(); err != nil || len(entries) != 0 {
		t.Errorf("a new ledger = %v, %v; want empty", entries, err)
	}
	records := []struct {
		kind string
		data any
	}{{"baseline", nil}, {"gate", 3.0}, {"ünïcode kind", "text\nwith a newline"}, {"", []any{1.0, "x", true}}, {"task_start", map[string]any{"nested": map[string]any{"ok": true}}}}
	for _, r := range records[:3] {
		if err := rd.Record(r.kind, r.data); err != nil {
			t.Fatal(err)
		}
	}
	again, err := OpenRunDir(dir, rd.ID) // a run opened again carries on where its ledger stopped
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records[3:] {
		if err := again.Record(r.kind, r.data); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := again.Ledger()
	if err != nil || len(entries) != len(records) {
		t.Fatalf("Ledger = %d entries, %v; want %d", len(entries), err, len(records))
	}
	for i, e := range entries {
		if e.Seq != i+1 || e.Kind != records[i].kind || e.TS == "" || !reflect.DeepEqual(testutil.Decode[any](t, e.Data), records[i].data) {
			t.Errorf("entry %d = %+v, want seq %d, kind %q and %v", i, e, i+1, records[i].kind, records[i].data)
		}
	}
	if len(testutil.JSONLines(t, filepath.Join(rd.Path, "ledger.jsonl"))) != len(records) {
		t.Error("ledger.jsonl does not hold one line per record")
	}
	if err := rd.Record("bad", make(chan int)); err == nil {
		t.Error("data that cannot be encoded was recorded")
	}
	testutil.WriteFile(t, rd.Path, "ledger.jsonl", "not json\n")
	if _, err := rd.Ledger(); err == nil {
		t.Error("a ledger line that is not JSON was read")
	}
}

func TestLedger_TheShellRecordsNothing(t *testing.T) {
	var rec recorder
	app := newApp()
	app.Register(rec.command(Command{Path: "X", UsesRunDir: true}))

	run(t, app, "X", "--dir", t.TempDir())

	if entries, err := rec.last.RunDir.Ledger(); err != nil || len(entries) != 0 {
		t.Errorf("the ledger of a run that recorded nothing: %v, %v", entries, err)
	}
}

func TestEnvelopeCopy_IsWhatStdoutGets(t *testing.T) {
	var rec recorder
	app := newApp()
	app.Register(rec.command(Command{Path: "X", UsesRunDir: true}))

	r := run(t, app, "X", "--dir", t.TempDir())

	if got := testutil.ReadFile(t, filepath.Join(rec.last.RunDir.Path, "envelope.json")); got != r.stdout {
		t.Errorf("envelope.json = %q, stdout %q", got, r.stdout)
	}
}

func TestEnvelopeCopy_ThatCannotBeWrittenIsAWarningAndAnEvent(t *testing.T) {
	var rec recorder
	rec.then = func(r *Run) (Outcome, error) {
		// A directory where the envelope is to be written.
		return Outcome{Status: StatusDone, Summary: "ok"}, os.Mkdir(filepath.Join(r.RunDir.Path, "envelope.json"), 0o755)
	}
	app := newApp()
	app.Register(rec.command(Command{Path: "X", UsesRunDir: true}))

	r := run(t, app, "X", "--dir", t.TempDir())

	events := testutil.JSONLines(t, filepath.Join(rec.last.RunDir.Path, "events.jsonl"))
	if r.code != 0 || !slices.Equal(warningCodes(r.env(t)), []string{WarnEnvelopeUnwritable}) {
		t.Errorf("exit %d, warnings %v; want done and %s", r.code, warningCodes(r.env(t)), WarnEnvelopeUnwritable)
	}
	if !slices.Equal(eventTypes(events), []string{"run_start", "warning", "run_end"}) || at(events[1], "data", "code") != WarnEnvelopeUnwritable {
		t.Errorf("events %v, want the warning before run_end", events)
	}
}
