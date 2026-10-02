package toolio

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TS-12-7 (integration): run_end.report_file equals envelope.report_file,
// including the empty string when no report is written.
//
// Verifies: 12-REQ-2.3
func TestTS12_7_RunEndReportFileEqualsEnvelopeReportFile(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	// Case 1: report is written (normal run).
	t.Run("report written", func(t *testing.T) {
		stateDir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", stateDir)

		app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
			return ExitOK, map[string]string{"stage": "done"}, nil
		})

		var stdout, stderr bytes.Buffer
		code := app.Main(context.Background(), []string{"--dir", t.TempDir(), "--emit-events", "x"},
			strings.NewReader(""), &stdout, &stderr)
		if code != ExitOK {
			t.Fatalf("code = %d, stderr = %s", code, stderr.String())
		}

		var env Envelope
		if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
			t.Fatalf("stdout is not JSON: %v", err)
		}

		// Parse events from stderr.
		events := parseEventLines(t, stderr.String())
		var runEnd map[string]any
		for _, ev := range events {
			if ev["type"] == "run_end" {
				runEnd = ev
			}
		}
		if runEnd == nil {
			t.Fatal("no run_end event found")
		}

		// run_end must have report_file key.
		rf, ok := runEnd["report_file"]
		if !ok {
			t.Fatal("run_end has no report_file key")
		}
		rfStr, _ := rf.(string)
		if rfStr != env.ReportFile {
			t.Errorf("run_end.report_file = %q, envelope.report_file = %q", rfStr, env.ReportFile)
		}
		if rfStr == "" {
			t.Error("report_file is empty but the report should have been written")
		}
	})

	// Case 2: report cannot be written (unwritable path).
	t.Run("report not written", func(t *testing.T) {
		stateDir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", stateDir)

		// Create a file where the directory would be, so MkdirAll fails.
		parentDir := filepath.Join(t.TempDir(), "blocker")
		if err := os.WriteFile(parentDir, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		unwritable := filepath.Join(parentDir, "sub", "report.json")

		app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
			return ExitOK, map[string]string{"stage": "done"}, nil
		})

		var stdout, stderr bytes.Buffer
		code := app.Main(context.Background(), []string{
			"--dir", t.TempDir(), "--emit-events", "--report-file", unwritable, "x",
		}, strings.NewReader(""), &stdout, &stderr)

		var env Envelope
		if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
			t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
		}

		// Parse events from stderr.
		events := parseEventLines(t, stderr.String())
		var runEnd map[string]any
		for _, ev := range events {
			if ev["type"] == "run_end" {
				runEnd = ev
			}
		}
		if runEnd == nil {
			t.Fatal("no run_end event found")
		}

		// run_end must have report_file key.
		rf, ok := runEnd["report_file"]
		if !ok {
			t.Fatal("run_end has no report_file key")
		}
		rfStr, _ := rf.(string)
		if rfStr != env.ReportFile {
			t.Errorf("run_end.report_file = %q, envelope.report_file = %q", rfStr, env.ReportFile)
		}
		// The report was not written, so both should be empty.
		if rfStr != "" {
			t.Errorf("report_file should be empty when report was not written, got %q", rfStr)
		}
		_ = code
	})
}

// TS-12-8 (integration): The events_file artifact follows report_file and
// appears only when the file was created, also under --dry-run.
//
// Verifies: 12-REQ-2.4
func TestTS12_8_EventsFileArtifactFollowsReportFile(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	// Case 1: normal run — events_file artifact present.
	t.Run("normal run", func(t *testing.T) {
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

		var env Envelope
		if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
			t.Fatalf("stdout is not JSON: %v", err)
		}

		// Check artifact order: report_file then events_file.
		kinds := make([]ArtifactKind, len(env.Artifacts))
		for i, a := range env.Artifacts {
			kinds[i] = a.Kind
		}
		rfIdx, efIdx := -1, -1
		for i, k := range kinds {
			if k == ArtifactReportFile {
				rfIdx = i
			}
			if k == ArtifactEventsFile {
				efIdx = i
			}
		}
		if rfIdx < 0 {
			t.Fatal("no report_file artifact")
		}
		if efIdx < 0 {
			t.Fatal("no events_file artifact")
		}
		if efIdx != rfIdx+1 {
			t.Errorf("events_file at index %d, report_file at %d; want events_file right after report_file", efIdx, rfIdx)
		}

		// The events_file path should name an existing file.
		evPath := env.Artifacts[efIdx].Path
		if _, err := os.Stat(evPath); err != nil {
			t.Errorf("events_file path %q does not exist: %v", evPath, err)
		}

		// Also check the report file carries the same artifacts.
		if env.ReportFile != "" {
			report := readReportFile(t, env.ReportFile)
			var reportEFIdx int = -1
			for i, a := range report.Artifacts {
				if a.Kind == ArtifactEventsFile {
					reportEFIdx = i
				}
			}
			if reportEFIdx < 0 {
				t.Error("report file has no events_file artifact")
			}
		}
	})

	// Case 2: --dry-run — events_file artifact present.
	t.Run("dry-run", func(t *testing.T) {
		stateDir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", stateDir)

		app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
			return ExitOK, map[string]string{"stage": "done"}, nil
		})

		var stdout bytes.Buffer
		code := app.Main(context.Background(), []string{"--dir", t.TempDir(), "--dry-run", "x"},
			strings.NewReader(""), &stdout, &bytes.Buffer{})
		if code != ExitOK {
			t.Fatalf("code = %d", code)
		}

		var env Envelope
		if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
			t.Fatalf("stdout is not JSON: %v", err)
		}

		var found bool
		for _, a := range env.Artifacts {
			if a.Kind == ArtifactEventsFile {
				found = true
				if _, err := os.Stat(a.Path); err != nil {
					t.Errorf("events_file path %q does not exist: %v", a.Path, err)
				}
			}
		}
		if !found {
			t.Error("no events_file artifact under --dry-run")
		}
	})

	// Case 3: events file cannot be created — no events_file artifact.
	t.Run("no events file", func(t *testing.T) {
		stateDir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", stateDir)

		// Make events/ a regular file so the directory cannot be created.
		agentFoxDir := filepath.Join(stateDir, "agent-fox")
		if err := os.MkdirAll(agentFoxDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentFoxDir, "events"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}

		app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
			return ExitOK, map[string]string{"stage": "done"}, nil
		})

		var stdout bytes.Buffer
		code := app.Main(context.Background(), []string{"--dir", t.TempDir(), "x"},
			strings.NewReader(""), &stdout, &bytes.Buffer{})

		var env Envelope
		if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
			t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
		}

		for _, a := range env.Artifacts {
			if a.Kind == ArtifactEventsFile {
				t.Error("events_file artifact should not be present when the file was not created")
			}
		}
		_ = code
	})
}

// TS-12-11 (property): Report name, events name and envelope session_id
// always share the session id, and each file names the other.
//
// Verifies: 12-REQ-2.7
func TestTS12_11_ReportEventsEnvelopeShareSessionID(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	for _, tc := range []struct {
		name  string
		extra []string
	}{
		{"normal", nil},
		{"dry-run", []string{"--dry-run"}},
		{"emit-events", []string{"--emit-events"}},
		{"quiet", []string{"--quiet"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			t.Setenv("XDG_STATE_HOME", stateDir)

			app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
				return ExitOK, map[string]string{"stage": "done"}, nil
			})

			argv := append([]string{"--dir", t.TempDir()}, tc.extra...)
			argv = append(argv, "x")
			var stdout bytes.Buffer
			code := app.Main(context.Background(), argv,
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

			// Report file name contains session_id.
			if env.ReportFile == "" {
				t.Fatal("report_file is empty")
			}
			if !strings.HasSuffix(filepath.Base(env.ReportFile), sid+".json") {
				t.Errorf("report file %q does not end with %s.json", filepath.Base(env.ReportFile), sid)
			}

			// Events file artifact.
			var eventsPath string
			for _, a := range env.Artifacts {
				if a.Kind == ArtifactEventsFile {
					eventsPath = a.Path
				}
			}
			if eventsPath == "" {
				t.Fatal("no events_file artifact")
			}
			if !strings.HasSuffix(filepath.Base(eventsPath), sid+".jsonl") {
				t.Errorf("events file %q does not end with %s.jsonl", filepath.Base(eventsPath), sid)
			}

			// Stems match.
			reportStem := strings.TrimSuffix(filepath.Base(env.ReportFile), ".json")
			eventsStem := strings.TrimSuffix(filepath.Base(eventsPath), ".jsonl")
			if reportStem != eventsStem {
				t.Errorf("stems differ: report=%q events=%q", reportStem, eventsStem)
			}

			// Events file on disk.
			if _, err := os.Stat(eventsPath); err != nil {
				t.Errorf("events file %q does not exist: %v", eventsPath, err)
			}

			// run_end.report_file names the report on disk.
			eventsContent, err := os.ReadFile(eventsPath)
			if err != nil {
				t.Fatalf("reading events file: %v", err)
			}
			lines := strings.Split(strings.TrimSuffix(string(eventsContent), "\n"), "\n")
			var lastEv map[string]any
			if err := json.Unmarshal([]byte(lines[len(lines)-1]), &lastEv); err != nil {
				t.Fatalf("last line is not JSON: %v", err)
			}
			if lastEv["type"] != "run_end" {
				t.Fatalf("last event type = %v, want run_end", lastEv["type"])
			}
			if rf, _ := lastEv["report_file"].(string); rf != env.ReportFile {
				t.Errorf("run_end.report_file = %q, want %q", rf, env.ReportFile)
			}
		})
	}
}

// TS-12-18 (integration): Runs that pass the sink build and end early leave
// an events file whose last line is run_end.
//
// Verifies: 12-REQ-4.4
func TestTS12_18_EarlyEndRunsLeaveRunEndAsLastLine(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	cases := []struct {
		name string
		exec func(context.Context, Deps) (int, any, *ErrorInfo)
		args []string
	}{
		{
			name: "later usage error (PreCheck)",
			exec: nil, // will be set below
			args: []string{"--dir", "TMPDIR", "x"},
		},
		{
			name: "dry-run",
			exec: func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
				return ExitOK, map[string]string{"stage": "done"}, nil
			},
			args: []string{"--dir", "TMPDIR", "--dry-run", "x"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			t.Setenv("XDG_STATE_HOME", stateDir)

			exec := tc.exec
			if tc.name == "later usage error (PreCheck)" {
				exec = func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
					return ExitOK, map[string]string{"stage": "done"}, nil
				}
			}

			app, _ := newApp(t, exec)
			if tc.name == "later usage error (PreCheck)" {
				app.PreCheck = func(*Common) error {
					return Usagef("bad flag combo")
				}
			}

			args := make([]string, len(tc.args))
			copy(args, tc.args)
			for i, a := range args {
				if a == "TMPDIR" {
					args[i] = t.TempDir()
				}
			}

			var stdout bytes.Buffer
			app.Main(context.Background(), args,
				strings.NewReader(""), &stdout, &bytes.Buffer{})

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
		})
	}
}

// TS-12-19 (integration): Invocations refused before the sink is built
// create no events file but still report.
//
// Verifies: 12-REQ-4.5
func TestTS12_19_PreSinkRefusalsCreateNoEventsFile(t *testing.T) {
	cases := []struct {
		name string
		args []string
		code int
	}{
		{"--version", []string{"--version"}, ExitOK},
		{"--schema", []string{"--schema"}, ExitOK},
		{"undefined flag", []string{"--nope", "x"}, ExitUsage},
		{"--events-file removed", []string{"--events-file", "/tmp/x.jsonl", "x"}, ExitUsage},
		{"--events removed", []string{"--events", "jsonl", "x"}, ExitUsage},
		{"invalid --detail", []string{"--detail", "wrong", "x"}, ExitUsage},
		{"invalid --total-budget", []string{"--total-budget", "-1", "x"}, ExitUsage},
		{"invalid --input-kind", []string{"--input-kind", "nonexistent", "x"}, ExitUsage},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			t.Setenv("XDG_STATE_HOME", stateDir)

			app, _ := newApp(t, nil)

			var stdout bytes.Buffer
			code := app.Main(context.Background(), tc.args,
				strings.NewReader(""), &stdout, &bytes.Buffer{})

			eventsDir := filepath.Join(stateDir, "agent-fox", "events")
			matches, _ := filepath.Glob(filepath.Join(eventsDir, "*.jsonl"))
			if len(matches) > 0 {
				t.Errorf("events files created: %v", matches)
			}

			if tc.code == ExitUsage {
				// For usage errors, a report should be written and have no events_file artifact.
				var env Envelope
				if err := json.Unmarshal(stdout.Bytes(), &env); err == nil {
					for _, a := range env.Artifacts {
						if a.Kind == ArtifactEventsFile {
							t.Error("envelope has events_file artifact for pre-sink usage error")
						}
					}
				}
			}
			if code != tc.code {
				t.Errorf("code = %d, want %d", code, tc.code)
			}
		})
	}
}

// TS-12-24 (integration): --quiet silences stderr under both --emit-events
// settings and leaves the file complete.
//
// Verifies: 12-REQ-5.2
func TestTS12_24_QuietSilencesStderrLeavesFileComplete(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	for _, flags := range [][]string{
		{"--quiet"},
		{"--quiet", "--emit-events"},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			stateDir := t.TempDir()
			t.Setenv("XDG_STATE_HOME", stateDir)

			app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
				d.Progress.Step("preflight", "hello")
				return ExitOK, map[string]string{"stage": "done"}, nil
			})

			argv := append([]string{"--dir", t.TempDir()}, flags...)
			argv = append(argv, "x")
			var stdout, stderr bytes.Buffer
			code := app.Main(context.Background(), argv,
				strings.NewReader(""), &stdout, &stderr)
			if code != ExitOK {
				t.Fatalf("code = %d", code)
			}

			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}

			// Events file should have the full stream ending with run_end.
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
		})
	}
}

// TS-12-29 (property): Under --emit-events the stderr bytes equal the events
// file bytes.
//
// Verifies: 12-REQ-6.1
func TestTS12_29_EmitEventsStderrEqualsFile(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	for _, extra := range [][]string{
		nil,
		{"--verbose"},
		{"--show-text"},
		{"--verbose", "--show-text"},
	} {
		name := "default"
		if len(extra) > 0 {
			name = strings.Join(extra, "+")
		}
		t.Run(name, func(t *testing.T) {
			stateDir := t.TempDir()
			t.Setenv("XDG_STATE_HOME", stateDir)

			app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
				d.Progress.Step("preflight", "hello")
				d.Progress.Raw("some prose")
				return ExitOK, map[string]string{"stage": "done"}, nil
			})

			argv := append([]string{"--dir", t.TempDir(), "--emit-events"}, extra...)
			argv = append(argv, "x")
			var stdout, stderr bytes.Buffer
			code := app.Main(context.Background(), argv,
				strings.NewReader(""), &stdout, &stderr)
			if code != ExitOK {
				t.Fatalf("code = %d", code)
			}

			// Find the events file.
			eventsDir := filepath.Join(stateDir, "agent-fox", "events")
			matches, _ := filepath.Glob(filepath.Join(eventsDir, "*.jsonl"))
			if len(matches) != 1 {
				t.Fatalf("expected 1 events file, got %d", len(matches))
			}

			fileContent, err := os.ReadFile(matches[0])
			if err != nil {
				t.Fatalf("reading events file: %v", err)
			}

			if string(fileContent) != stderr.String() {
				t.Errorf("stderr and events file differ.\nstderr len=%d\nfile len=%d",
					stderr.Len(), len(fileContent))
				// Show first difference.
				stderrStr := stderr.String()
				fileStr := string(fileContent)
				for i := 0; i < len(stderrStr) && i < len(fileStr); i++ {
					if stderrStr[i] != fileStr[i] {
						t.Errorf("first difference at byte %d: stderr=%q file=%q",
							i, stderrStr[max(0, i-20):min(len(stderrStr), i+20)],
							fileStr[max(0, i-20):min(len(fileStr), i+20)])
						break
					}
				}
			}
		})
	}
}

// TS-12-31 (unit): The sink records fields in full, unsanitised and
// unabbreviated.
//
// Verifies: 12-REQ-6.3
func TestTS12_31_SinkRecordsFieldsInFull(t *testing.T) {
	var buf bytes.Buffer
	s := newEventsSink("fix", &buf)
	s.sessionID = "abcdef0123456789abcdef0123456789"

	// A text event with 1 MiB of prose including ANSI escapes, tabs, unicode
	// and a label-like prefix.
	bigText := strings.Repeat("x", 1024*1024) + "\033[31m\ttab\t\U0001F600" + "[model] label-like"
	s.Emit(newTextEvent("implement", 1, bigText))

	// A tool_call with a 100 KiB arguments object.
	bigArgs := `{"key":"` + strings.Repeat("v", 100*1024) + `"}`
	s.Emit(newToolCallEvent("implement", "bash", false,
		json.RawMessage(bigArgs), true, nil, ""))

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}

	// Check text event.
	var textEv map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &textEv); err != nil {
		t.Fatalf("text line is not JSON: %v", err)
	}
	if textEv["text"] != bigText {
		t.Errorf("text field was truncated or modified: len=%d want=%d",
			len(textEv["text"].(string)), len(bigText))
	}

	// Check tool_call event.
	var tcEv map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &tcEv); err != nil {
		t.Fatalf("tool_call line is not JSON: %v", err)
	}
	argsRaw, err := json.Marshal(tcEv["arguments"])
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}
	// The arguments should be compacted but the same content.
	var compacted bytes.Buffer
	json.Compact(&compacted, []byte(bigArgs))
	if string(argsRaw) != compacted.String() {
		t.Errorf("arguments were truncated or modified: len=%d want=%d",
			len(argsRaw), compacted.Len())
	}

	// No ellipsis or truncation marker.
	full := buf.String()
	if strings.Contains(full, "...") || strings.Contains(full, "truncated") || strings.Contains(full, "ellipsis") {
		t.Error("output contains truncation markers")
	}
}

// TS-12-32 (property): The events file does not depend on --verbose,
// --show-text or --quiet.
//
// Verifies: 12-REQ-6.4
func TestTS12_32_EventsFileIndependentOfHumanFlags(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	combos := [][]string{
		nil,
		{"--verbose"},
		{"--show-text"},
		{"--quiet"},
		{"--verbose", "--show-text"},
		{"--verbose", "--quiet"},
		{"--show-text", "--quiet"},
		{"--verbose", "--show-text", "--quiet"},
	}

	// Collect event types from each run.
	type runResult struct {
		types []string
	}
	var results []runResult

	for _, flags := range combos {
		stateDir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", stateDir)

		app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
			d.Progress.Step("preflight", "hello")
			d.Progress.Raw("some prose")
			return ExitOK, map[string]string{"stage": "done"}, nil
		})

		argv := append([]string{"--dir", t.TempDir()}, flags...)
		argv = append(argv, "x")
		var stdout bytes.Buffer
		code := app.Main(context.Background(), argv,
			strings.NewReader(""), &stdout, &bytes.Buffer{})
		if code != ExitOK {
			t.Fatalf("flags %v: code = %d", flags, code)
		}

		eventsDir := filepath.Join(stateDir, "agent-fox", "events")
		matches, _ := filepath.Glob(filepath.Join(eventsDir, "*.jsonl"))
		if len(matches) != 1 {
			t.Fatalf("flags %v: expected 1 events file, got %d", flags, len(matches))
		}

		content, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatalf("flags %v: reading events file: %v", flags, err)
		}

		var types []string
		for _, line := range strings.Split(strings.TrimSuffix(string(content), "\n"), "\n") {
			if line == "" {
				continue
			}
			var ev map[string]any
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				t.Fatalf("flags %v: line is not JSON: %v", flags, err)
			}
			types = append(types, ev["type"].(string))
		}
		results = append(results, runResult{types: types})
	}

	// All runs should have the same sequence of event types.
	base := results[0].types
	for i, r := range results[1:] {
		if len(r.types) != len(base) {
			t.Errorf("combo %d (%v): %d event types, want %d (base)",
				i+1, combos[i+1], len(r.types), len(base))
			continue
		}
		for j, ty := range r.types {
			if ty != base[j] {
				t.Errorf("combo %d (%v): event %d type = %q, want %q",
					i+1, combos[i+1], j, ty, base[j])
			}
		}
	}
}
