package toolio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TS-12-5 (unit): Every event carries the header ts, tool, session_id, type
// in that key order.
func TestTS12_5_EventHeaderKeyOrder(t *testing.T) {
	sid := "abcdef0123456789abcdef0123456789"
	var buf bytes.Buffer
	s := newEventsSink("impl", &buf)
	s.sessionID = sid

	for _, e := range everyEvent() {
		s.Emit(e)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != len(everyEvent()) {
		t.Fatalf("got %d lines, want %d", len(lines), len(everyEvent()))
	}
	for i, line := range lines {
		// Parse the JSON to check session_id value.
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if obj["session_id"] != sid {
			t.Errorf("line %d: session_id = %v, want %s", i, obj["session_id"], sid)
		}

		// Check key order by using json.Decoder with Token.
		dec := json.NewDecoder(strings.NewReader(line))
		tok, err := dec.Token() // opening {
		if err != nil || tok != json.Delim('{') {
			t.Fatalf("line %d: expected {, got %v", i, tok)
		}
		wantKeys := []string{"ts", "tool", "session_id", "type"}
		for ki, wantKey := range wantKeys {
			tok, err = dec.Token()
			if err != nil {
				t.Fatalf("line %d key %d: %v", i, ki, err)
			}
			gotKey, ok := tok.(string)
			if !ok || gotKey != wantKey {
				t.Errorf("line %d: key %d = %v, want %q", i, ki, tok, wantKey)
			}
			// Skip the value.
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				t.Fatalf("line %d: skipping value of %s: %v", i, wantKey, err)
			}
		}
	}
}

// TS-12-10 (integration): When stateHome() fails the run records both
// not-written warnings and keeps its exit code.
func TestTS12_10_StateHomeFailsBothWarnings(t *testing.T) {
	// Set ANTHROPIC_API_KEY so model resolution works even without HOME.
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	// Run with a good state dir first (reference).
	goodState := t.TempDir()
	t.Setenv("XDG_STATE_HOME", goodState)
	t.Setenv("HOME", t.TempDir())

	app2, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		return ExitOK, map[string]string{"stage": "done"}, nil
	})
	app2.Name = "tool"

	var stdoutGood, stderrGood bytes.Buffer
	codeGood := app2.Main(context.Background(), []string{"--dir", t.TempDir(), "x"},
		strings.NewReader(""), &stdoutGood, &stderrGood)

	// Now run with a bad HOME so stateHome() fails.
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")
	os.Unsetenv("HOME")
	os.Unsetenv("XDG_STATE_HOME")

	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		return ExitOK, map[string]string{"stage": "done"}, nil
	})
	app.Name = "tool"

	var stdoutBad, stderrBad bytes.Buffer
	codeBad := app.Main(context.Background(), []string{"--dir", t.TempDir(), "x"},
		strings.NewReader(""), &stdoutBad, &stderrBad)

	var envBad Envelope
	if err := json.Unmarshal(stdoutBad.Bytes(), &envBad); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdoutBad.String())
	}

	if codeBad != codeGood {
		t.Errorf("exit codes differ: bad=%d good=%d", codeBad, codeGood)
	}

	// Check warnings.
	warnCodes := map[WarnCode]string{}
	for _, w := range envBad.Warnings {
		warnCodes[w.Code] = w.Stage
	}
	if warnCodes[WarnReportFileNotWritten] != "report" {
		t.Errorf("missing report_file_not_written warning at stage report; warnings=%+v", envBad.Warnings)
	}
	if warnCodes[WarnEventsFileNotWritten] != "report" {
		t.Errorf("missing events_file_not_written warning at stage report; warnings=%+v", envBad.Warnings)
	}
}

// TS-12-14 (integration): --report-file moves only the report; the events
// file keeps its default path.
func TestTS12_14_ReportFileMoveOnlyReport(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	reportDir := t.TempDir()
	reportPath := filepath.Join(reportDir, "custom-report.json")

	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		return ExitOK, map[string]string{"stage": "done"}, nil
	})

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{
		"--dir", t.TempDir(), "--report-file", reportPath, "x",
	}, strings.NewReader(""), &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}

	// The report should be at the custom path.
	if _, err := os.Stat(reportPath); err != nil {
		t.Errorf("report not at custom path %s: %v", reportPath, err)
	}

	// Parse the envelope to get the session_id.
	var env Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	sid := env.SessionID

	// The events file should be at the default path.
	eventsDir := filepath.Join(stateDir, "agent-fox", "events")
	matches, _ := filepath.Glob(filepath.Join(eventsDir, "*-"+sid+".jsonl"))
	if len(matches) != 1 {
		t.Errorf("expected exactly 1 events file matching *-%s.jsonl, got %d", sid, len(matches))
	}
}

// TS-12-15 (integration): The events file is created exclusively with mode
// 0600 and a pre-existing file is never truncated.
func TestTS12_15_EventsFileExclusiveCreate(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	// First run: creates the events file.
	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		return ExitOK, map[string]string{"stage": "done"}, nil
	})
	var stdout bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", t.TempDir(), "x"},
		strings.NewReader(""), &stdout, &bytes.Buffer{})
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}

	var env Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}

	// Find the events file.
	eventsDir := filepath.Join(stateDir, "agent-fox", "events")
	matches, _ := filepath.Glob(filepath.Join(eventsDir, "*.jsonl"))
	if len(matches) != 1 {
		t.Fatalf("expected 1 events file, got %d", len(matches))
	}
	eventsPath := matches[0]

	// Check mode is 0600.
	info, err := os.Stat(eventsPath)
	if err != nil {
		t.Fatalf("stat events file: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("events file mode = %o, want 0600", mode)
	}

	// Now write known bytes to a path that a second run would use.
	// We need to create a collision. We'll pre-create a file at a known path.
	knownBytes := []byte("DO NOT OVERWRITE ME\n")
	collisionPath := filepath.Join(eventsDir, "tool-collision-test.jsonl")
	if err := os.WriteFile(collisionPath, knownBytes, 0o644); err != nil {
		t.Fatalf("writing collision file: %v", err)
	}

	// Verify the original events file content is valid JSONL.
	content, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("reading events file: %v", err)
	}
	if len(content) == 0 {
		t.Error("events file is empty")
	}

	// Verify the collision file is unchanged.
	after, err := os.ReadFile(collisionPath)
	if err != nil {
		t.Fatalf("reading collision file: %v", err)
	}
	if string(after) != string(knownBytes) {
		t.Errorf("collision file was modified: got %q, want %q", after, knownBytes)
	}
}

// TS-12-16 (integration): Directory and file modes: events/ 0700, parents
// 0755, existing events/ untouched, report modes unchanged.
func TestTS12_16_DirectoryAndFileModes(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	// Case 1: fresh state dir.
	t.Run("fresh", func(t *testing.T) {
		stateDir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", stateDir)

		app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
			return ExitOK, map[string]string{"stage": "done"}, nil
		})
		var stdout bytes.Buffer
		code := app.Main(context.Background(), []string{"--dir", t.TempDir(), "x"},
			strings.NewReader(""), &stdout, &bytes.Buffer{})
		if code != ExitOK {
			t.Fatalf("code = %d", code)
		}

		// Check events/ mode.
		eventsDir := filepath.Join(stateDir, "agent-fox", "events")
		info, err := os.Stat(eventsDir)
		if err != nil {
			t.Fatalf("stat events dir: %v", err)
		}
		if mode := info.Mode().Perm(); mode != 0o700 {
			t.Errorf("events/ mode = %o, want 0700", mode)
		}

		// Check agent-fox/ mode.
		agentFoxDir := filepath.Join(stateDir, "agent-fox")
		info, err = os.Stat(agentFoxDir)
		if err != nil {
			t.Fatalf("stat agent-fox dir: %v", err)
		}
		if mode := info.Mode().Perm(); mode != 0o755 {
			t.Errorf("agent-fox/ mode = %o, want 0755", mode)
		}

		// Check runs/ mode.
		runsDir := filepath.Join(stateDir, "agent-fox", "runs")
		info, err = os.Stat(runsDir)
		if err != nil {
			t.Fatalf("stat runs dir: %v", err)
		}
		if mode := info.Mode().Perm(); mode != 0o755 {
			t.Errorf("runs/ mode = %o, want 0755", mode)
		}

		// Check report file mode.
		var env Envelope
		json.Unmarshal(stdout.Bytes(), &env)
		if env.ReportFile != "" {
			info, err = os.Stat(env.ReportFile)
			if err != nil {
				t.Fatalf("stat report file: %v", err)
			}
			if mode := info.Mode().Perm(); mode != 0o644 {
				t.Errorf("report file mode = %o, want 0644", mode)
			}
		}
	})

	// Case 2: pre-existing events/ with mode 0755 keeps its mode.
	t.Run("existing events dir", func(t *testing.T) {
		stateDir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", stateDir)

		eventsDir := filepath.Join(stateDir, "agent-fox", "events")
		if err := os.MkdirAll(eventsDir, 0o755); err != nil {
			t.Fatalf("creating events dir: %v", err)
		}

		app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
			return ExitOK, map[string]string{"stage": "done"}, nil
		})
		code := app.Main(context.Background(), []string{"--dir", t.TempDir(), "x"},
			strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
		if code != ExitOK {
			t.Fatalf("code = %d", code)
		}

		info, err := os.Stat(eventsDir)
		if err != nil {
			t.Fatalf("stat events dir: %v", err)
		}
		if mode := info.Mode().Perm(); mode != 0o755 {
			t.Errorf("pre-existing events/ mode changed to %o, want 0755", mode)
		}
	})
}

// TS-12-17 (unit): Each event is exactly one Write call ending with a
// newline.
func TestTS12_17_EventIsOneWriteCall(t *testing.T) {
	rec := &recordingWriter{}
	s := newEventsSink("fix", rec)
	s.sessionID = "abcdef0123456789abcdef0123456789"

	events := everyEvent()
	// Add an event with a very large field.
	bigMsg := strings.Repeat("x", 100000)
	events = append(events, newStepEvent("big", bigMsg))

	for _, e := range events {
		s.Emit(e)
	}

	if len(rec.calls) != len(events) {
		t.Fatalf("got %d Write calls, want %d", len(rec.calls), len(events))
	}
	for i, call := range rec.calls {
		if !strings.HasSuffix(call, "\n") {
			t.Errorf("call %d does not end with newline: %q", i, call[max(0, len(call)-20):])
		}
		if strings.Count(call, "\n") != 1 {
			t.Errorf("call %d has %d newlines, want 1", i, strings.Count(call, "\n"))
		}
		if !json.Valid([]byte(strings.TrimSuffix(call, "\n"))) {
			t.Errorf("call %d is not valid JSON: %q", i, call[:min(100, len(call))])
		}
	}
}

// recordingWriter records each Write call as a string.
type recordingWriter struct {
	calls []string
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	w.calls = append(w.calls, string(p))
	return len(p), nil
}

// TS-12-20 (integration): An uncreatable events file yields a low
// events_file_not_written warning and an unchanged exit code.
func TestTS12_20_UncreatableEventsFileWarning(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	// Make events/ a regular file so the directory cannot be created.
	stateDir := t.TempDir()
	agentFoxDir := filepath.Join(stateDir, "agent-fox")
	if err := os.MkdirAll(agentFoxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(agentFoxDir, "events")
	if err := os.WriteFile(eventsPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", stateDir)

	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		return ExitOK, map[string]string{"stage": "done"}, nil
	})

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", t.TempDir(), "--emit-events", "x"},
		strings.NewReader(""), &stdout, &stderr)

	var env Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}

	// Check for the warning.
	var found bool
	for _, w := range env.Warnings {
		if w.Code == WarnEventsFileNotWritten {
			found = true
			if w.Severity != "low" {
				t.Errorf("severity = %q, want low", w.Severity)
			}
			if w.Stage != "report" {
				t.Errorf("stage = %q, want report", w.Stage)
			}
		}
	}
	if !found {
		t.Errorf("events_file_not_written warning not found; warnings=%+v", env.Warnings)
	}

	// Check stderr for warning event under --emit-events.
	stderrEvents := parseEventLines(t, stderr.String())
	var warningFound bool
	for _, ev := range stderrEvents {
		if ev["type"] == "warning" && ev["code"] == string(WarnEventsFileNotWritten) {
			warningFound = true
		}
	}
	if !warningFound {
		t.Errorf("no warning event for events_file_not_written on stderr")
	}

	// Reference run with a writable state dir.
	goodState := t.TempDir()
	t.Setenv("XDG_STATE_HOME", goodState)
	app2, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		return ExitOK, map[string]string{"stage": "done"}, nil
	})
	var stdout2 bytes.Buffer
	codeRef := app2.Main(context.Background(), []string{"--dir", t.TempDir(), "--emit-events", "x"},
		strings.NewReader(""), &stdout2, &bytes.Buffer{})
	if code != codeRef {
		t.Errorf("exit codes differ: bad=%d ref=%d", code, codeRef)
	}
}

// TS-12-21 (unit): With no file and no --emit-events the sink is inactive
// and no heartbeat starts.
func TestTS12_21_NoFileNoEmitEventsSinkInactive(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	// Make events/ uncreatable.
	stateDir := t.TempDir()
	agentFoxDir := filepath.Join(stateDir, "agent-fox")
	if err := os.MkdirAll(agentFoxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(agentFoxDir, "events")
	if err := os.WriteFile(eventsPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", stateDir)

	var sinkActive bool
	var tickerCount int
	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		sinkActive = d.Run.eventSink().Active()
		tickerCount = d.Run.eventSink().activeTickerCount()
		return ExitOK, map[string]string{"stage": "done"}, nil
	})

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", t.TempDir(), "x"},
		strings.NewReader(""), &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}

	if sinkActive {
		t.Error("sink should be inactive when events file cannot be created and --emit-events is not set")
	}
	if tickerCount != 0 {
		t.Errorf("ticker count = %d, want 0", tickerCount)
	}

	// Verify stderr has no JSON events.
	if strings.Contains(stderr.String(), `"type":`) {
		t.Errorf("stderr contains JSON events: %s", stderr.String())
	}
}

// TS-12-22 (unit): The heartbeat ticks every 15 seconds for the whole run
// when an events file exists.
func TestTS12_22_HeartbeatTicksWithEventsFile(t *testing.T) {
	// Use the fake clock approach from heartbeat_test.go.
	clock := newFakeClock()
	var buf syncBuffer
	s := newEventsSink("fix", &buf)
	s.sessionID = "abcdef0123456789abcdef0123456789"
	s.now = clock.Now
	s.newTicker = clock.newTicker
	s.StartHeartbeat(func() float64 { return 0 })
	t.Cleanup(s.StopHeartbeat)

	// Advance 15s three times.
	for i := 0; i < 3; i++ {
		clock.Advance(15 * time.Second)
	}

	hbs := eventsOfType(t, &buf, "heartbeat")
	if len(hbs) != 3 {
		t.Fatalf("got %d heartbeats, want 3", len(hbs))
	}

	// Emit run_end to stop the heartbeat.
	s.Emit(newRunEndEvent("done", 0, ""))

	// Advance more; no additional heartbeats.
	clock.Advance(15 * time.Second)
	hbs = eventsOfType(t, &buf, "heartbeat")
	if len(hbs) != 3 {
		t.Errorf("got %d heartbeats after run_end, want 3", len(hbs))
	}

	// Verify the interval is 15s (the idle window).
	if s.idle != 15*time.Second {
		t.Errorf("idle = %v, want 15s", s.idle)
	}
}

// Verify that WarnEventsFileNotWritten is declared and mapped to stage "report".
func TestTS12_WarnEventsFileNotWrittenDeclared(t *testing.T) {
	stage, ok := WarnStage(WarnEventsFileNotWritten)
	if !ok {
		t.Fatal("WarnEventsFileNotWritten has no entry in the stage table")
	}
	if stage != "report" {
		t.Errorf("stage = %q, want report", stage)
	}
}

// Verify that the events file is created for runs that pass the sink build
// (--dry-run, --preflight, etc.) and that the last line is run_end.
func TestTS12_EventsFileCreatedForDryRun(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		return ExitOK, map[string]string{"stage": "done"}, nil
	})

	var stdout bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", t.TempDir(), "--dry-run", "x"},
		strings.NewReader(""), &stdout, &bytes.Buffer{})
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}

	eventsDir := filepath.Join(stateDir, "agent-fox", "events")
	matches, _ := filepath.Glob(filepath.Join(eventsDir, "*.jsonl"))
	if len(matches) != 1 {
		t.Fatalf("expected 1 events file, got %d", len(matches))
	}

	lines := fileLines(t, matches[0])
	if len(lines) == 0 {
		t.Fatal("events file is empty")
	}
	var lastEv map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &lastEv); err != nil {
		t.Fatalf("last line is not JSON: %v", err)
	}
	if lastEv["type"] != "run_end" {
		t.Errorf("last event type = %v, want run_end", lastEv["type"])
	}
}

// Verify that no events file is created for usage errors before the sink.
func TestTS12_NoEventsFileForPreSinkUsageErrors(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)

	for _, args := range [][]string{
		{"--detail", "wrong", "x"},             // invalid --detail
		{"--total-budget", "-1", "x"},          // invalid --total-budget
		{"--input-kind", "nonexistent", "x"},   // invalid --input-kind
		{"--events", "jsonl", "x"},             // removed flag
		{"--events-file", "/tmp/x.jsonl", "x"}, // removed flag
	} {
		app, _ := newApp(t, nil)
		var stdout bytes.Buffer
		app.Main(context.Background(), args,
			strings.NewReader(""), &stdout, &bytes.Buffer{})

		eventsDir := filepath.Join(stateDir, "agent-fox", "events")
		matches, _ := filepath.Glob(filepath.Join(eventsDir, "*.jsonl"))
		if len(matches) > 0 {
			t.Errorf("args %v: events files created: %v", args, matches)
		}

		// Check that the envelope has no events_file artifact.
		var env Envelope
		if err := json.Unmarshal(stdout.Bytes(), &env); err == nil {
			for _, a := range env.Artifacts {
				if a.Kind == ArtifactEventsFile {
					t.Errorf("args %v: envelope has events_file artifact", args)
				}
			}
		}
	}
}

// Verify the heartbeat runs for every run that has an events file, even
// without --emit-events.
func TestTS12_HeartbeatRunsWithEventsFileNoEmitEvents(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	var tickerCount int
	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		tickerCount = d.Run.eventSink().activeTickerCount()
		return ExitOK, map[string]string{"stage": "done"}, nil
	})

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", t.TempDir(), "x"},
		strings.NewReader(""), &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	if tickerCount != 1 {
		t.Errorf("ticker count = %d, want 1 (heartbeat should run with events file)", tickerCount)
	}
}

// Verify that the events file and report file share the same session_id stem.
func TestTS12_EventsAndReportShareSessionID(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		return ExitOK, map[string]string{"stage": "done"}, nil
	})

	var stdout bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", t.TempDir(), "x"},
		strings.NewReader(""), &stdout, &bytes.Buffer{})
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}

	var env Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}

	sid := env.SessionID
	if sid == "" {
		t.Fatal("session_id is empty")
	}

	// Check report file name contains session_id.
	if env.ReportFile == "" {
		t.Fatal("report_file is empty")
	}
	if !strings.Contains(filepath.Base(env.ReportFile), sid) {
		t.Errorf("report file %q does not contain session_id %s", env.ReportFile, sid)
	}

	// Check events file exists and contains session_id.
	eventsDir := filepath.Join(stateDir, "agent-fox", "events")
	matches, _ := filepath.Glob(filepath.Join(eventsDir, fmt.Sprintf("*-%s.jsonl", sid)))
	if len(matches) != 1 {
		t.Errorf("expected 1 events file matching *-%s.jsonl, got %d", sid, len(matches))
	}

	// Verify the stems match (same tool-timestamp-sessionid).
	reportStem := strings.TrimSuffix(filepath.Base(env.ReportFile), ".json")
	if len(matches) > 0 {
		eventsStem := strings.TrimSuffix(filepath.Base(matches[0]), ".jsonl")
		if reportStem != eventsStem {
			t.Errorf("stems differ: report=%q events=%q", reportStem, eventsStem)
		}
	}
}
