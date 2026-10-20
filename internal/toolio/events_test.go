package toolio

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// readEvents parses an event file into its lines.
func readEvents(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var events []map[string]any
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("%s: %q is not a JSON line: %v", path, line, err)
		}
		events = append(events, e)
	}
	return events
}

// eventTypes lists the types of events.
func eventTypes(events []map[string]any) []string {
	var types []string
	for _, e := range events {
		types = append(types, e["type"].(string))
	}
	return types
}

// TestEnvelopeCopiesAreIdentical_TS_05_13 verifies TS-05-13:
// The envelope is written byte-identically to envelope.json and --output.
// Verifies: 05-REQ-2.5
func TestEnvelopeCopiesAreIdentical_TS_05_13(t *testing.T) {
	t.Run("a run directory and --output", func(t *testing.T) {
		dir, out := t.TempDir(), filepath.Join(t.TempDir(), "envelope-copy.json")
		var c runDirCommand
		app := newTestApp()
		app.Register(c.command(nil))

		_, stdout, _ := invoke(t, app, "X", "--dir", dir, "--output", out)

		for name, path := range map[string]string{"envelope.json": filepath.Join(c.runDir.Path, "envelope.json"), "--output": out} {
			if data, err := os.ReadFile(path); err != nil || string(data) != stdout {
				t.Errorf("%s = %q (%v), want the stdout envelope %q", name, data, err, stdout)
			}
		}
	})

	t.Run("--output alone", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "envelope-copy.json")
		app := newTestApp()
		app.Register(returning("bare", Outcome{Status: StatusDone, Summary: "ok"}, nil))

		_, stdout, _ := invoke(t, app, "bare", "--output", out)

		if data, err := os.ReadFile(out); err != nil || string(data) != stdout {
			t.Errorf("--output = %q (%v), want the stdout envelope %q", data, err, stdout)
		}
	})
}

// TestUnwritableCopyIsAWarning_TS_05_14 verifies TS-05-14:
// An unwritable envelope copy becomes a warning and leaves the status
// unchanged.
// Verifies: 05-REQ-2.6
func TestUnwritableCopyIsAWarning_TS_05_14(t *testing.T) {
	t.Run("--output", func(t *testing.T) {
		app := newTestApp()
		app.Register(returning("bare", Outcome{Status: StatusDone, Summary: "ok"}, nil))

		code, stdout, _ := invoke(t, app, "bare", "--output", filepath.Join(t.TempDir(), "missing", "envelope.json"))
		env := decode(t, stdout)

		if code != 0 || env["status"] != "done" || !slices.Equal(warningCodes(env), []string{"output_unwritable"}) {
			t.Errorf("exit code %d, status %v, warnings %v; want 0, done, output_unwritable", code, env["status"], warningCodes(env))
		}
	})

	t.Run("envelope.json", func(t *testing.T) {
		var c runDirCommand
		app := newTestApp()
		app.Register(c.command(func(r *Run) (Outcome, error) {
			// A directory where the envelope is to be written.
			return Outcome{Status: StatusDone, Summary: "ok"}, os.Mkdir(filepath.Join(r.RunDir.Path, "envelope.json"), 0o755)
		}))

		code, stdout, _ := invoke(t, app, "X", "--dir", t.TempDir())
		env := decode(t, stdout)

		if code != 0 || env["status"] != "done" || !slices.Equal(warningCodes(env), []string{"envelope_unwritable"}) {
			t.Errorf("exit code %d, status %v, warnings %v; want 0, done, envelope_unwritable", code, env["status"], warningCodes(env))
		}
	})

	t.Run("one copy fails, the other still equals stdout", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "envelope-copy.json")
		var c runDirCommand
		app := newTestApp()
		app.Register(c.command(func(r *Run) (Outcome, error) {
			return Outcome{Status: StatusDone, Summary: "ok"}, os.Mkdir(filepath.Join(r.RunDir.Path, "envelope.json"), 0o755)
		}))

		_, stdout, _ := invoke(t, app, "X", "--dir", t.TempDir(), "--output", out)

		if data, err := os.ReadFile(out); err != nil || string(data) != stdout {
			t.Errorf("--output = %q (%v), want the stdout envelope, warning included: %q", data, err, stdout)
		}
		if !strings.Contains(stdout, "envelope_unwritable") {
			t.Errorf("stdout lacks the warning:\n%s", stdout)
		}
	})
}

// TestShellWarningCodes_TS_05_23 verifies TS-05-23:
// Every warning code the shell raises matches the code pattern.
// Verifies: 05-REQ-3.8
func TestShellWarningCodes_TS_05_23(t *testing.T) {
	seen := map[string]bool{}
	collect := func(env map[string]any) {
		for _, code := range warningCodes(env) {
			seen[code] = true
			if !warningCodeFormat.MatchString(code) {
				t.Errorf("warning code %q does not match ^[a-z][a-z0-9_]*$", code)
			}
		}
	}
	t.Chdir(t.TempDir())

	// input_truncated
	var rec inputRecorder
	app := newTestApp()
	app.Register(rec.command(InputRequired))
	_, stdout, _ := invoke(t, app, "X", strings.Repeat("a", MaxInputBytes+1))
	collect(decode(t, stdout))

	// comments_unreadable
	srv := githubForge(t, false, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) })
	githubEnv(t, srv.URL)
	app = newTestApp()
	app.Register(issueCommand(&rec))
	_, stdout, _ = invoke(t, app, "X", srv.URL+"/o/r/issues/7")
	collect(decode(t, stdout))

	// output_unwritable and events_unwritable
	app = newTestApp()
	app.Register(returning("bare", Outcome{Status: StatusDone}, nil))
	missing := filepath.Join(t.TempDir(), "missing", "file")
	_, stdout, _ = invoke(t, app, "bare", "--output", missing, "--emit-events", missing)
	collect(decode(t, stdout))

	// envelope_unwritable
	var c runDirCommand
	app = newTestApp()
	app.Register(c.command(func(r *Run) (Outcome, error) {
		return Outcome{Status: StatusDone}, os.Mkdir(filepath.Join(r.RunDir.Path, "envelope.json"), 0o755)
	}))
	_, stdout, _ = invoke(t, app, "X", "--dir", t.TempDir())
	collect(decode(t, stdout))

	for _, code := range []string{"input_truncated", "comments_unreadable", "envelope_unwritable", "output_unwritable", "events_unwritable"} {
		if !seen[code] {
			t.Errorf("the shell did not raise %s", code)
		}
	}
	got := slices.Clone(WarningCodes)
	slices.Sort(got)
	var raised []string
	for code := range seen {
		raised = append(raised, code)
	}
	slices.Sort(raised)
	if !slices.Equal(got, raised) {
		t.Errorf("WarningCodes = %v, but the shell raised %v", got, raised)
	}
}

// TestEventStream_TS_05_66 verifies TS-05-66:
// Events are flushed JSON lines bracketed by run_start and run_end, also to
// --emit-events.
// Verifies: 05-REQ-9.7
func TestEventStream_TS_05_66(t *testing.T) {
	dir := t.TempDir()
	emitted := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(emitted, []byte("an earlier line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var c runDirCommand
	app := newTestApp()
	app.Register(c.command(func(r *Run) (Outcome, error) {
		r.Warn("first_warning", "one")
		r.Emit("step", map[string]any{"n": 1})
		r.Warn("second_warning", "two")
		r.Emit("heartbeat", nil)
		return Outcome{Status: StatusDone, Summary: "ok"}, nil
	}))

	_, stdout, _ := invoke(t, app, "X", "--dir", dir, "--emit-events", emitted)
	env := decode(t, stdout)

	events := readEvents(t, filepath.Join(c.runDir.Path, "events.jsonl"))
	wantTypes := []string{"run_start", "warning", "step", "warning", "heartbeat", "run_end"}
	if got := eventTypes(events); !slices.Equal(got, wantTypes) {
		t.Fatalf("event types = %v, want %v", got, wantTypes)
	}
	for i, e := range events {
		if e["seq"] != float64(i+1) || e["run_id"] != env["run_id"] || e["ts"] == "" {
			t.Errorf("event %d = %v, want seq %d, run_id %v and a ts", i, e, i+1, env["run_id"])
		}
	}
	wantStart := map[string]any{
		"tool": "X", "input_kind": "none",
		"argv": []any{"af", "X", "--dir", dir, "--emit-events", emitted},
	}
	if got := events[0]["data"]; !reflect.DeepEqual(got, wantStart) {
		t.Errorf("run_start data = %v, want %v", got, wantStart)
	}
	if got := events[1]["data"]; !reflect.DeepEqual(got, map[string]any{"code": "first_warning", "message": "one"}) {
		t.Errorf("warning data = %v", got)
	}
	end, _ := events[5]["data"].(map[string]any)
	if end["status"] != "done" || end["exit_code"] != float64(0) || end["duration_ms"] == nil {
		t.Errorf("run_end data = %v, want status done, exit_code 0 and duration_ms", end)
	}

	// The same lines are appended to the file --emit-events names.
	runLines, _ := os.ReadFile(filepath.Join(c.runDir.Path, "events.jsonl"))
	emitLines, _ := os.ReadFile(emitted)
	if want := "an earlier line\n" + string(runLines); string(emitLines) != want {
		t.Errorf("--emit-events file = %q, want the earlier line followed by the run's events", emitLines)
	}
}

// TestUnwritableEventSink_TS_05_67 verifies TS-05-67:
// An unwritable event sink raises events_unwritable exactly once.
// Verifies: 05-REQ-9.8
func TestUnwritableEventSink_TS_05_67(t *testing.T) {
	var c runDirCommand
	app := newTestApp()
	app.Register(c.command(func(r *Run) (Outcome, error) {
		for i := range 4 {
			r.Emit("step", map[string]any{"n": i})
		}
		return Outcome{Status: StatusDone, Summary: "ok"}, nil
	}))

	code, stdout, _ := invoke(t, app, "X", "--dir", t.TempDir(), "--emit-events", filepath.Join(t.TempDir(), "missing", "events.jsonl"))
	env := decode(t, stdout)

	if code != 0 || env["status"] != "done" {
		t.Errorf("exit code %d, status %v; want 0, done", code, env["status"])
	}
	if got := warningCodes(env); !slices.Equal(got, []string{"events_unwritable"}) {
		t.Errorf("warnings = %v, want exactly one events_unwritable", got)
	}
	events := readEvents(t, filepath.Join(c.runDir.Path, "events.jsonl"))
	if types := eventTypes(events); types[0] != "run_start" || types[len(types)-1] != "run_end" || len(types) < 6 {
		t.Errorf("the run directory's events = %v, want run_start … run_end", types)
	}
}

// TestEventsWithoutARunDirectory guards the rule behind TS-05-66: a command
// without a run directory still has its events written where --emit-events
// says, with no run id, and an invocation refused before its command ran has
// them bracketed too.
func TestEventsWithoutARunDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	emitted := filepath.Join(t.TempDir(), "events.jsonl")
	var rec inputRecorder
	app := newTestApp()
	app.Register(rec.command(InputRequired))

	invoke(t, app, "X", "text", "--emit-events", emitted)
	events := readEvents(t, emitted)
	if got, want := eventTypes(events), []string{"run_start", "run_end"}; !slices.Equal(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	if events[0]["run_id"] != nil || events[0]["data"].(map[string]any)["input_kind"] != "text" {
		t.Errorf("run_start = %v, want a null run_id and input_kind text", events[0])
	}

	refused := filepath.Join(t.TempDir(), "refused.jsonl")
	code, _, _ := invoke(t, app, "X", "   ", "--emit-events", refused)
	events = readEvents(t, refused)
	if code != 2 || !slices.Equal(eventTypes(events), []string{"run_start", "run_end"}) {
		t.Fatalf("a refused invocation: exit code %d, events %v; want 2 and run_start, run_end", code, eventTypes(events))
	}
	if end := events[1]["data"].(map[string]any); end["status"] != "usage" || end["exit_code"] != float64(2) {
		t.Errorf("run_end = %v, want status usage and exit_code 2", end)
	}
}

// TestEmitAfterTheRunIsDropped guards the abandonment of TS-05-25: an event
// from a command the shell has stopped waiting for is dropped, not written
// to a closed file.
func TestEmitAfterTheRunIsDropped(t *testing.T) {
	emitted := filepath.Join(t.TempDir(), "events.jsonl")
	var late *Run
	app := newTestApp()
	app.Register(Command{Path: "X", Run: func(r *Run) (Outcome, error) {
		late = r
		return Outcome{Status: StatusDone}, nil
	}})
	var out bytes.Buffer
	app.Main(context.Background(), []string{"af", "X", "--emit-events", emitted}, strings.NewReader(""), &out, &bytes.Buffer{})
	before, _ := os.ReadFile(emitted)

	late.Emit("step", nil)
	late.Warn("late_warning", "after the end")

	if after, _ := os.ReadFile(emitted); !bytes.Equal(before, after) {
		t.Errorf("events were written after run_end:\n%s", after)
	}
}
