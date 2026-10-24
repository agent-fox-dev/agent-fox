package toolio

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
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

// sinkEvents collects the events of the invocations of app, through its EventSink.
func sinkEvents(t *testing.T, app *App) *[]map[string]any {
	t.Helper()
	var events []map[string]any
	app.EventSink = func(line []byte) {
		var e map[string]any
		if err := json.Unmarshal(line, &e); err != nil {
			t.Errorf("%q is not a JSON line: %v", line, err)
		}
		events = append(events, e)
	}
	return &events
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
// The envelope is written byte-identically to envelope.json.
// Verifies: 05-REQ-2.5
func TestEnvelopeCopiesAreIdentical_TS_05_13(t *testing.T) {
	dir := t.TempDir()
	var c runDirCommand
	app := newTestApp()
	app.Register(c.command(nil))

	_, stdout, _ := invoke(t, app, "X", "--dir", dir)

	if data, err := os.ReadFile(filepath.Join(c.runDir.Path, "envelope.json")); err != nil || string(data) != stdout {
		t.Errorf("envelope.json = %q (%v), want the stdout envelope %q", data, err, stdout)
	}
}

// TestUnwritableCopyIsAWarning_TS_05_14 verifies TS-05-14:
// An unwritable envelope copy becomes a warning and leaves the status
// unchanged.
// Verifies: 05-REQ-2.6
func TestUnwritableCopyIsAWarning_TS_05_14(t *testing.T) {
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

	// events_unwritable, which the stream raises through the run's Warn
	stream := newEventStream(time.Now, func(code, _ string) { collect(map[string]any{"warnings": []any{map[string]any{"code": code}}}) })
	stream.addFile(filepath.Join(t.TempDir(), "missing", "events.jsonl"))

	// envelope_unwritable
	var c runDirCommand
	app = newTestApp()
	app.Register(c.command(func(r *Run) (Outcome, error) {
		return Outcome{Status: StatusDone}, os.Mkdir(filepath.Join(r.RunDir.Path, "envelope.json"), 0o755)
	}))
	_, stdout, _ = invoke(t, app, "X", "--dir", t.TempDir())
	collect(decode(t, stdout))

	// invalid_warning_code
	app = newTestApp()
	app.Register(Command{Path: "bad", Run: func(r *Run) (Outcome, error) {
		r.Warn("Not A Code!", "from a command")
		return Outcome{Status: StatusDone}, nil
	}})
	_, stdout, _ = invoke(t, app, "bad")
	collect(decode(t, stdout))

	for _, code := range []string{"input_truncated", "comments_unreadable", "envelope_unwritable", "events_unwritable", "invalid_warning_code"} {
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
// the EventSink of the App.
// Verifies: 05-REQ-9.7
func TestEventStream_TS_05_66(t *testing.T) {
	dir := t.TempDir()
	var c runDirCommand
	app := newTestApp()
	var sunk []string
	app.EventSink = func(line []byte) { sunk = append(sunk, string(line)+"\n") }
	var seenWhileRunning []string
	app.Register(c.command(func(r *Run) (Outcome, error) {
		r.Warn("first_warning", "one")
		r.Emit("step", map[string]any{"n": 1})
		// Each line is on disk as soon as it is emitted, not when the run ends.
		data, _ := os.ReadFile(filepath.Join(r.RunDir.Path, "events.jsonl"))
		seenWhileRunning = append(seenWhileRunning, string(data), strings.Join(sunk, ""))
		r.Warn("second_warning", "two")
		r.Emit("heartbeat", nil)
		return Outcome{Status: StatusDone, Summary: "ok"}, nil
	}))

	_, stdout, _ := invoke(t, app, "X", "--dir", dir)
	env := decode(t, stdout)

	for i, seen := range seenWhileRunning {
		if lines := strings.Count(seen, "\n"); !strings.Contains(seen, `"type":"step"`) || strings.Contains(seen, "heartbeat") || lines < 3 {
			t.Errorf("sink %d had %d lines while the command ran, want run_start, a warning and the step and nothing later:\n%s", i, lines, seen)
		}
	}

	events := readEvents(t, filepath.Join(c.runDir.Path, "events.jsonl"))
	wantTypes := []string{"run_start", "warning", "step", "warning", "heartbeat", "run_end"}
	if got := eventTypes(events); !slices.Equal(got, wantTypes) {
		t.Fatalf("event types = %v, want %v", got, wantTypes)
	}
	for i, e := range events {
		ts, _ := e["ts"].(string)
		if e["seq"] != float64(i+1) || e["run_id"] != env["run_id"] || !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`).MatchString(ts) {
			t.Errorf("event %d = %v, want seq %d, run_id %v and a timestamp", i, e, i+1, env["run_id"])
		}
	}
	wantStart := map[string]any{
		"tool": "X", "input_kind": "none",
		"argv": []any{"af", "X", "--dir", dir},
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

	// The same lines go to the sink.
	runLines, _ := os.ReadFile(filepath.Join(c.runDir.Path, "events.jsonl"))
	if got := strings.Join(sunk, ""); got != string(runLines) {
		t.Errorf("the sink got %q, want the run's events", got)
	}
}

// TestUnwritableEventSink_TS_05_67 verifies TS-05-67:
// An unwritable event sink raises events_unwritable exactly once.
// Verifies: 05-REQ-9.8
func TestUnwritableEventSink_TS_05_67(t *testing.T) {
	var warned []string
	stream := newEventStream(time.Now, func(code, _ string) { warned = append(warned, code) })
	stream.addFile(filepath.Join(t.TempDir(), "missing", "events.jsonl"))
	stream.addFile(filepath.Join(t.TempDir(), "missing", "again.jsonl"))
	stream.begin("X", []string{"af", "X"}, KindNone, nil)
	for i := range 4 {
		stream.emit("step", map[string]any{"n": i})
	}

	if !slices.Equal(warned, []string{"events_unwritable"}) {
		t.Errorf("warnings = %v, want exactly one events_unwritable", warned)
	}
}

// TestEventsWithoutARunDirectory guards the rule behind TS-05-66: a command
// without a run directory still has its events given to the EventSink, with no
// run id, and an invocation refused before its command ran has
// them bracketed too.
func TestEventsWithoutARunDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	var rec inputRecorder
	app := newTestApp()
	app.Register(rec.command(InputRequired))
	sunk := sinkEvents(t, app)

	invoke(t, app, "X", "text")
	events := *sunk
	if got, want := eventTypes(events), []string{"run_start", "run_end"}; !slices.Equal(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	if events[0]["run_id"] != nil || events[0]["data"].(map[string]any)["input_kind"] != "text" {
		t.Errorf("run_start = %v, want a null run_id and input_kind text", events[0])
	}

	*sunk = nil
	code, _, _ := invoke(t, app, "X", "   ")
	events = *sunk
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
	var late *Run
	app := newTestApp()
	app.Register(Command{Path: "X", Run: func(r *Run) (Outcome, error) {
		late = r
		return Outcome{Status: StatusDone}, nil
	}})
	sunk := sinkEvents(t, app)
	var out bytes.Buffer
	app.Main(context.Background(), []string{"af", "X"}, strings.NewReader(""), &out, &bytes.Buffer{})
	before := len(*sunk)

	late.Emit("step", nil)
	late.Warn("late_warning", "after the end")

	if after := len(*sunk); after != before {
		t.Errorf("%d events were written after run_end", after-before)
	}
}

// TestRefusedInvocationsAreReported guards the rule behind TS-05-13 and
// TS-05-66: an invocation refused before its command ran still has its events,
// run_start to run_end, whether a flag, --dir or --timeout was refused.
func TestRefusedInvocationsAreReported(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"an extra positional", []string{"X", "a", "b"}},
		{"an unknown flag", []string{"X", "--bogus"}},
		{"a missing --dir", []string{"X", "--dir", "MISSING"}},
		{"a negative --timeout", []string{"X", "--timeout", "-1s"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			args := slices.Clone(tc.args)
			for i, a := range args {
				if a == "MISSING" {
					args[i] = filepath.Join(dir, "missing")
				}
			}
			var c runDirCommand
			app := newTestApp()
			command := c.command(nil)
			command.Timeout = true
			app.Register(command)
			sunk := sinkEvents(t, app)

			code, _, _ := invoke(t, app, args...)

			if code != 2 || c.ran != 0 {
				t.Fatalf("exit code %d, ran %d; want 2, 0", code, c.ran)
			}
			lines := *sunk
			if got, want := eventTypes(lines), []string{"run_start", "run_end"}; !slices.Equal(got, want) {
				t.Fatalf("event types = %v, want %v", got, want)
			}
			if end := lines[1]["data"].(map[string]any); end["status"] != "usage" || end["exit_code"] != float64(2) {
				t.Errorf("run_end = %v, want status usage and exit_code 2", end)
			}
		})
	}
}

// TestUnwritableCopyHasAWarningEvent guards TS-05-14 and TS-05-66: the
// warning for a copy of the envelope that cannot be written is an event like
// any other, and run_end is still the last.
func TestUnwritableCopyHasAWarningEvent(t *testing.T) {
	var c runDirCommand
	app := newTestApp()
	app.Register(c.command(func(r *Run) (Outcome, error) {
		return Outcome{Status: StatusDone, Summary: "ok"}, os.Mkdir(filepath.Join(r.RunDir.Path, "envelope.json"), 0o755)
	}))

	_, stdout, _ := invoke(t, app, "X", "--dir", t.TempDir())
	env := decode(t, stdout)

	events := readEvents(t, filepath.Join(c.runDir.Path, "events.jsonl"))
	if got, want := eventTypes(events), []string{"run_start", "warning", "run_end"}; !slices.Equal(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	if code := events[1]["data"].(map[string]any)["code"]; code != "envelope_unwritable" {
		t.Errorf("the warning event is for %v, want envelope_unwritable", code)
	}
	if !slices.Equal(warningCodes(env), []string{"envelope_unwritable"}) {
		t.Errorf("warnings = %v, want envelope_unwritable", warningCodes(env))
	}
}

// TestInvalidWarningCodeIsReported guards TS-05-23: a code that does not
// match the pattern is reported under invalid_warning_code, with the code it
// was given in the message, and never fails the run.
func TestInvalidWarningCodeIsReported(t *testing.T) {
	app := newTestApp()
	app.Register(Command{Path: "X", Run: func(r *Run) (Outcome, error) {
		r.Warn("Bad Code!", "a message")
		r.Warn("good_code", "another")
		return Outcome{Status: StatusDone}, nil
	}})

	code, stdout, _ := invoke(t, app, "X")
	env := decode(t, stdout)

	want := []any{
		map[string]any{"code": "invalid_warning_code", "message": `"Bad Code!": a message`},
		map[string]any{"code": "good_code", "message": "another"},
	}
	if code != 0 || env["status"] != "done" || !reflect.DeepEqual(env["warnings"], want) {
		t.Errorf("exit code %d, status %v, warnings %v; want 0, done, %v", code, env["status"], env["warnings"], want)
	}
}
