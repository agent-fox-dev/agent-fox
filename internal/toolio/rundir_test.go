package toolio

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

var runIDPattern = regexp.MustCompile(`^[0-9]{8}_[0-9]{6}_[0-9a-f]{6}$`)

// scriptedRand is a random source whose n-th draw (Read) yields chunks[n],
// and the last chunk for every draw after that.
type scriptedRand struct {
	chunks [][]byte
	draws  int
}

func (s *scriptedRand) Read(p []byte) (int, error) {
	chunk := s.chunks[min(s.draws, len(s.chunks)-1)]
	s.draws++
	return copy(p, chunk), nil
}

// runDirCommand is a test command with a run directory that records what
// the shell made of it.
type runDirCommand struct {
	ran     int
	runDir  *RunDir
	existed bool // the directory existed when Run was called
}

func (c *runDirCommand) command(run func(*Run) (Outcome, error)) Command {
	return Command{Path: "X", UsesRunDir: true, DryRun: true, Run: func(r *Run) (Outcome, error) {
		c.ran++
		c.runDir = r.RunDir
		if r.RunDir != nil {
			_, err := os.Stat(r.RunDir.Path)
			c.existed = err == nil
		}
		if run != nil {
			return run(r)
		}
		return Outcome{Status: StatusDone, Summary: "ok"}, nil
	}}
}

// agentFoxDir is where the shell keeps its local state under dir.
func agentFoxDir(dir string) string { return filepath.Join(dir, ".agent-fox") }

// TestRunDirectoryIsCreated_TS_05_60 verifies TS-05-60:
// A run directory is created under --dir with a well-formed id and
// artifacts.
// Verifies: 05-REQ-9.1
func TestRunDirectoryIsCreated_TS_05_60(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 26, 48, 0, time.FixedZone("plus2", 7200)) // 07:26:48 UTC
	for _, flags := range [][]string{nil, {"--dry-run"}} {
		t.Run(fmt.Sprint(flags), func(t *testing.T) {
			dir := t.TempDir()
			var c runDirCommand
			app := newTestApp()
			app.Now = func() time.Time { return now }
			app.Rand = &scriptedRand{chunks: [][]byte{{0xab, 0xcd, 0xef}}}
			app.Register(c.command(func(r *Run) (Outcome, error) {
				if err := r.RunDir.WriteReport("# the report\n"); err != nil {
					return Outcome{}, err
				}
				return Outcome{Status: StatusDone, Summary: "ok"}, nil
			}))

			code, stdout, _ := invoke(t, app, append([]string{"X", "--dir", dir}, flags...)...)
			env := decode(t, stdout)

			if code != 0 {
				t.Fatalf("exit code = %d\n%s", code, stdout)
			}
			id, _ := env["run_id"].(string)
			if !runIDPattern.MatchString(id) || id != "20261020_072648_abcdef" {
				t.Errorf("run_id = %v, want 20261020_072648_abcdef", env["run_id"])
			}
			path := filepath.Join(dir, ".agent-fox", "run", id)
			if !c.existed || c.runDir == nil || c.runDir.Path != path || c.runDir.ID != id {
				t.Errorf("the directory existed when Run was called: %v; Run.RunDir = %+v; want %s", c.existed, c.runDir, path)
			}
			want := []any{
				map[string]any{"kind": "run_dir", "path": path},
				map[string]any{"kind": "report", "path": filepath.Join(path, "report.md")},
			}
			if got := env["artifacts"]; !reflect.DeepEqual(got, want) {
				t.Errorf("artifacts = %v, want %v", got, want)
			}
			if data, err := os.ReadFile(filepath.Join(path, "report.md")); err != nil || string(data) != "# the report\n" {
				t.Errorf("report.md = %q (%v)", data, err)
			}
		})
	}

	t.Run("a command without a run directory", func(t *testing.T) {
		dir := t.TempDir()
		app := newTestApp()
		app.Register(Command{Path: "bare", Dir: true, Run: func(*Run) (Outcome, error) { return Outcome{Status: StatusDone}, nil }})

		_, stdout, _ := invoke(t, app, "bare", "--dir", dir)

		if env := decode(t, stdout); env["run_id"] != nil {
			t.Errorf("run_id = %v, want null", env["run_id"])
		}
		if _, err := os.Stat(agentFoxDir(dir)); err == nil {
			t.Error("a run directory was created for a command that did not ask for one")
		}
	})
}

// TestBadDirIsUsage_TS_05_61 verifies TS-05-61:
// A missing or non-directory --dir is a usage error and nothing is created.
// Verifies: 05-REQ-9.2
func TestBadDirIsUsage_TS_05_61(t *testing.T) {
	parent := t.TempDir()
	file := filepath.Join(parent, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(parent, "missing")

	// A directory that exists is accepted, so the refusals below are about
	// the directory and not about the flag.
	var control runDirCommand
	ok := newTestApp()
	ok.Register(control.command(nil))
	if code, _, _ := invoke(t, ok, "X", "--dir", t.TempDir()); code != 0 || control.ran != 1 {
		t.Fatalf("an existing --dir: exit code %d, ran %d; want 0, 1", code, control.ran)
	}

	for name, dir := range map[string]string{"a missing directory": missing, "a file": file} {
		t.Run(name, func(t *testing.T) {
			var c runDirCommand
			app := newTestApp()
			app.Register(c.command(nil))

			code, stdout, _ := invoke(t, app, "X", "--dir", dir)

			if code != 2 || decode(t, stdout)["status"] != "usage" || c.ran != 0 {
				t.Errorf("exit code %d, status %v, ran %d; want 2, usage, 0", code, decode(t, stdout)["status"], c.ran)
			}
			entries, _ := os.ReadDir(parent)
			for _, e := range entries {
				if e.Name() != "file.txt" {
					t.Errorf("%s was created next to the bad --dir", e.Name())
				}
			}
		})
	}
}

// TestRunIDCollisions_TS_05_62 verifies TS-05-62:
// Run id collisions are retried up to five times, then failed/io.
// Verifies: 05-REQ-9.3
func TestRunIDCollisions_TS_05_62(t *testing.T) {
	now := time.Date(2026, 10, 20, 7, 26, 48, 0, time.UTC)
	taken := []byte{1, 2, 3}
	clash := filepath.Join("run", "20261020_072648_010203")

	t.Run("five collisions", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(agentFoxDir(dir), clash), 0o755); err != nil {
			t.Fatal(err)
		}
		var c runDirCommand
		rnd := &scriptedRand{chunks: [][]byte{taken}}
		app := newTestApp()
		app.Now = func() time.Time { return now }
		app.Rand = rnd
		app.Register(c.command(nil))

		code, stdout, _ := invoke(t, app, "X", "--dir", dir)
		env := decode(t, stdout)

		if code != 1 || env["status"] != "failed" || at(env, "error", "category") != CategoryIO || c.ran != 0 {
			t.Errorf("exit code %d, status %v, category %v, ran %d; want 1, failed, io, 0", code, env["status"], at(env, "error", "category"), c.ran)
		}
		if rnd.draws != 5 {
			t.Errorf("%d draws, want 5", rnd.draws)
		}
	})

	t.Run("four collisions", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(agentFoxDir(dir), clash), 0o755); err != nil {
			t.Fatal(err)
		}
		var c runDirCommand
		rnd := &scriptedRand{chunks: [][]byte{taken, taken, taken, taken, {9, 9, 9}}}
		app := newTestApp()
		app.Now = func() time.Time { return now }
		app.Rand = rnd
		app.Register(c.command(nil))

		code, stdout, _ := invoke(t, app, "X", "--dir", dir)

		if code != 0 || decode(t, stdout)["run_id"] != "20261020_072648_090909" || rnd.draws != 5 {
			t.Errorf("exit code %d, run_id %v, %d draws; want 0, 20261020_072648_090909, 5", code, decode(t, stdout)["run_id"], rnd.draws)
		}
	})
}

// git runs git in dir without the user's configuration.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// TestGitignoreIsCreatedOnce_TS_05_63 verifies TS-05-63:
// The .agent-fox/.gitignore is created once and the repository .gitignore is
// untouched.
// Verifies: 05-REQ-9.4
func TestGitignoreIsCreatedOnce_TS_05_63(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	ownIgnore := []byte("bin/\n")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), ownIgnore, 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".gitignore")
	git(t, repo, "commit", "-q", "-m", "ignore bin")

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = NewRunDir(repo, nil, nil)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("run %d failed: %v", i, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(agentFoxDir(repo), ".gitignore")); err != nil || string(data) != "*\n" {
		t.Errorf(".agent-fox/.gitignore = %q (%v), want \"*\\n\"", data, err)
	}
	if status := git(t, repo, "status", "--porcelain"); status != "" {
		t.Errorf("git status --porcelain shows:\n%s", status)
	}
	if data, _ := os.ReadFile(filepath.Join(repo, ".gitignore")); string(data) != string(ownIgnore) {
		t.Errorf("the repository's .gitignore is now %q", data)
	}

	// A file that is there already is not touched.
	other := t.TempDir()
	if err := os.MkdirAll(agentFoxDir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(agentFoxDir(other), ".gitignore")
	if err := os.WriteFile(custom, []byte("something else\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunDir(other, nil, nil); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(custom); string(data) != "something else\n" {
		t.Errorf("an existing .agent-fox/.gitignore was changed to %q", data)
	}
}

// TestOpenRunDir_TS_05_64 verifies TS-05-64:
// OpenRunDir opens an existing run and refuses bad ids as usage.
// Verifies: 05-REQ-9.5
func TestOpenRunDir_TS_05_64(t *testing.T) {
	dir := t.TempDir()
	created, err := NewRunDir(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	opened, err := OpenRunDir(dir, created.ID)
	if err != nil || opened.Path != created.Path || opened.ID != created.ID {
		t.Errorf("OpenRunDir = %+v (%v), want the run %+v", opened, err, created)
	}

	before, _ := os.ReadDir(dir)
	for _, id := range []string{"../x", "", "20261020_072648_abcdeg", "20261020_072648_ABCDEF", "20261020_072648_abcdef", "..", "a/b"} {
		rd, err := OpenRunDir(dir, id)
		failure, ok := err.(*Failure)
		if err == nil || !ok || failure.Category != CategoryUsage || rd != nil {
			t.Errorf("OpenRunDir(%q) = %v, %v; want a usage failure", id, rd, err)
		}
	}
	if after, _ := os.ReadDir(dir); len(after) != len(before) {
		t.Errorf("opening runs changed %s", dir)
	}
	if _, err := OpenRunDir(filepath.Join(dir, "missing"), created.ID); err == nil {
		t.Error("OpenRunDir of a missing directory succeeded")
	}
}

// TestLedger_TS_05_65 verifies TS-05-65:
// Ledger records read back in order with seq 1..n and the shell records
// nothing.
// Verifies: 05-REQ-9.6
func TestLedger_TS_05_65(t *testing.T) {
	rng := rand.New(rand.NewPCG(65, 5))
	kinds := []string{"baseline", "gate", "task_start", "a kind with spaces", "ünïcode", ""}
	value := func() any {
		switch rng.IntN(6) {
		case 0:
			return nil
		case 1:
			return rng.IntN(1000)
		case 2:
			return fmt.Sprintf("text %d\nwith a newline", rng.IntN(100))
		case 3:
			return []any{rng.IntN(5), "x", true}
		case 4:
			return map[string]any{"n": rng.IntN(9), "nested": map[string]any{"ok": rng.IntN(2) == 0}}
		}
		return rng.Float64()
	}

	for i := range 40 {
		rd, err := NewRunDir(t.TempDir(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		type record struct {
			kind string
			data any
		}
		var records []record
		for range rng.IntN(12) {
			records = append(records, record{kinds[rng.IntN(len(kinds))], value()})
		}
		for _, r := range records {
			if err := rd.Record(r.kind, r.data); err != nil {
				t.Fatalf("#%d: Record: %v", i, err)
			}
		}

		got, err := rd.Ledger()
		if err != nil || len(got) != len(records) {
			t.Fatalf("#%d: Ledger() = %d entries (%v), want %d", i, len(got), err, len(records))
		}
		for j, entry := range got {
			var data any
			if err := json.Unmarshal(entry.Data, &data); err != nil {
				t.Fatalf("#%d entry %d: data %s: %v", i, j, entry.Data, err)
			}
			want, _ := json.Marshal(records[j].data)
			var wantData any
			_ = json.Unmarshal(want, &wantData)
			if entry.Seq != j+1 || entry.Kind != records[j].kind || !reflect.DeepEqual(data, wantData) || entry.TS == "" {
				t.Fatalf("#%d entry %d = %+v, want seq %d, kind %q, data %v", i, j, entry, j+1, records[j].kind, wantData)
			}
		}
		if data, _ := os.ReadFile(filepath.Join(rd.Path, "ledger.jsonl")); len(records) > 0 && strings.Count(string(data), "\n") != len(records) {
			t.Fatalf("#%d: ledger.jsonl has %d lines, want one per record", i, strings.Count(string(data), "\n"))
		}
	}

	// A run opened again carries on where the ledger stopped.
	dir := t.TempDir()
	first, _ := NewRunDir(dir, nil, nil)
	_ = first.Record("one", 1)
	_ = first.Record("two", 2)
	again, _ := OpenRunDir(dir, first.ID)
	_ = again.Record("three", 3)
	if entries, _ := again.Ledger(); len(entries) != 3 || entries[2].Seq != 3 || entries[2].Kind != "three" {
		t.Errorf("a reopened ledger = %+v, want three entries ending with seq 3", entries)
	}

	// The shell itself records nothing.
	var c runDirCommand
	app := newTestApp()
	app.Register(c.command(nil))
	invoke(t, app, "X", "--dir", dir)
	entries, err := c.runDir.Ledger()
	if err != nil || len(entries) != 0 {
		t.Errorf("a run that records nothing has the ledger %+v (%v)", entries, err)
	}
}

// TestPanicTxtInRunDir_TS_05_19 verifies the run directory half of TS-05-19:
// A recovered panic's stack is also written to <run_dir>/panic.txt.
// Verifies: 05-REQ-3.4
func TestPanicTxtInRunDir_TS_05_19(t *testing.T) {
	dir := t.TempDir()
	var c runDirCommand
	app := newTestApp()
	app.Register(c.command(func(*Run) (Outcome, error) { panic("boom") }))
	app.Register(Command{Path: "bare", Dir: true, Run: func(*Run) (Outcome, error) { panic("boom") }})

	code, stdout, stderr := invoke(t, app, "X", "--dir", dir)
	env := decode(t, stdout)

	if code != 1 || at(env, "error", "category") != CategoryInternal {
		t.Errorf("exit code %d, category %v; want 1, internal", code, at(env, "error", "category"))
	}
	data, err := os.ReadFile(filepath.Join(c.runDir.Path, "panic.txt"))
	if err != nil || !strings.Contains(string(data), "boom") || !strings.Contains(string(data), "goroutine") {
		t.Errorf("panic.txt = %q (%v), want the panic and its stack", data, err)
	}
	if !strings.Contains(stderr, "boom") {
		t.Errorf("stderr = %q, want the stack too", stderr)
	}

	other := t.TempDir()
	code, stdout, _ = invoke(t, app, "bare", "--dir", other)
	if code != 1 || decode(t, stdout)["status"] != "failed" {
		t.Errorf("a panic without a run directory: exit code %d, status %v; want 1, failed", code, decode(t, stdout)["status"])
	}
	if _, err := os.Stat(agentFoxDir(other)); err == nil {
		t.Error("a panic without a run directory created one")
	}
}

// TestRefusedInvocationLeavesNoRunDirectory guards the rule behind TS-05-61:
// an invocation refused before its command can run creates nothing.
func TestRefusedInvocationLeavesNoRunDirectory(t *testing.T) {
	dir := t.TempDir()
	var c runDirCommand
	app := newTestApp()
	cmd := c.command(nil)
	cmd.Input = InputRequired
	app.Register(cmd)

	code, _, _ := invoke(t, app, "X", "   ", "--dir", dir)

	if code != 2 || c.ran != 0 {
		t.Fatalf("exit code %d, ran %d; want 2, 0", code, c.ran)
	}
	if _, err := os.Stat(agentFoxDir(dir)); err == nil {
		t.Error("a refused invocation created .agent-fox")
	}
}
