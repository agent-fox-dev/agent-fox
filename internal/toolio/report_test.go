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

// TS-06-5 (integration): Every run writes the full-view envelope to a
// report file, regardless of --detail.
func TestTS06_5_ReportFileCarriesFullView(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	dir := t.TempDir()
	reportPath := filepath.Join(t.TempDir(), "report.json")

	app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
		return ExitOK, map[string]any{
			"stage":          "landed",
			"implementation": "moved the increment inside the retry guard",
		}, nil
	})

	env, code, _ := runApp(t, app, []string{
		"--dir", dir, "--detail", "summary", "--report-file", reportPath, "fix this bug",
	}, "")
	if code != ExitOK || !env.OK {
		t.Fatalf("code=%d env=%+v", code, env)
	}
	if env.ReportFile != reportPath {
		t.Fatalf("env.ReportFile = %q, want %q", env.ReportFile, reportPath)
	}

	b, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("report file was not written: %v", err)
	}
	var rf Envelope
	if err := json.Unmarshal(b, &rf); err != nil {
		t.Fatalf("report file is not one JSON object: %v", err)
	}
	m, ok := rf.Result.(map[string]any)
	if !ok {
		t.Fatalf("report file result is not an object: %T", rf.Result)
	}
	if _, ok := m["implementation"]; !ok {
		t.Errorf("report file result is missing the full view's %q field: %v", "implementation", m)
	}
}

// TS-06-6 (unit): The default report-file path is computed under
// XDG_STATE_HOME with a colon-free timestamp, and its parent directory is
// created.
func TestTS06_6_DefaultReportPathUnderXDGStateHome(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	dir := t.TempDir()

	app, _ := newApp(t, nil)
	env, code, _ := runApp(t, app, []string{"--dir", dir, "issue text"}, "")
	if code != ExitOK {
		t.Fatalf("code=%d", code)
	}
	if env.ReportFile == "" {
		t.Fatal("expected env.ReportFile to be set")
	}
	wantDir := filepath.Join(tmp, "agent-fox", "runs")
	if got := filepath.Dir(env.ReportFile); got != wantDir {
		t.Errorf("report file dir = %q, want %q", got, wantDir)
	}
	if _, err := os.Stat(env.ReportFile); err != nil {
		t.Fatalf("report file does not exist: %v", err)
	}
	base := filepath.Base(env.ReportFile)
	if strings.Contains(base, ":") {
		t.Errorf("report file name %q contains a colon", base)
	}
	if !strings.HasPrefix(base, "tool-") {
		t.Errorf("report file name %q does not start with the tool name", base)
	}
}

// TS-06-7 (unit): The report-file path falls back to ~/.local/state when
// XDG_STATE_HOME is unset.
func TestTS06_7_ReportPathFallsBackToHomeLocalState(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", "")
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	dir := t.TempDir()

	app, _ := newApp(t, nil)
	env, code, _ := runApp(t, app, []string{"--dir", dir, "spec text"}, "")
	if code != ExitOK {
		t.Fatalf("code=%d", code)
	}
	wantDir := filepath.Join(tmp, ".local", "state", "agent-fox", "runs")
	if got := filepath.Dir(env.ReportFile); got != wantDir {
		t.Errorf("report file dir = %q, want %q", got, wantDir)
	}
	if _, err := os.Stat(env.ReportFile); err != nil {
		t.Fatalf("report file does not exist: %v", err)
	}
}

// TS-06-8 (unit): A report file that cannot be written records a low
// warning and omits report_file, without failing the run.
func TestTS06_8_UnwritableReportFileRecordsWarningWithoutFailing(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	// The report's parent cannot be created because an ancestor is a regular
	// file. That fails the write for any user, root included; a read-only
	// directory (chmod 0500) would not, since root bypasses the mode.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(blocker, "sub", "x.json")

	app, _ := newApp(t, nil)
	env, code, _ := runApp(t, app, []string{"--dir", dir, "--report-file", reportPath, "some text"}, "")
	if code != ExitOK || !env.OK {
		t.Fatalf("code=%d env=%+v", code, env)
	}

	var found *Warning
	for i := range env.Warnings {
		if env.Warnings[i].Code == WarnReportFileNotWritten {
			found = &env.Warnings[i]
		}
	}
	if found == nil {
		t.Fatalf("expected a %q warning, got %+v", WarnReportFileNotWritten, env.Warnings)
	}
	if found.Severity != "low" {
		t.Errorf("warning severity = %q, want low", found.Severity)
	}
	if found.Stage != "report" {
		t.Errorf("warning stage = %q, want report", found.Stage)
	}
	if env.ReportFile != "" {
		t.Errorf("expected no report_file, got %q", env.ReportFile)
	}
	if _, err := os.Stat(reportPath); err == nil {
		t.Errorf("report file should not exist at %s", reportPath)
	}
}

// TS-06-9 (unit): A --dry-run run still writes its report file.
func TestTS06_9_DryRunStillWritesReportFile(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()

	app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
		return ExitOK, map[string]any{"stage": "landed"}, nil
	})
	env, code, _ := runApp(t, app, []string{"--dir", dir, "--dry-run", "some report"}, "")
	if code != ExitOK {
		t.Fatalf("code=%d", code)
	}
	if env.ReportFile == "" {
		t.Fatal("expected a report file to have been written under --dry-run")
	}
	if _, err := os.Stat(env.ReportFile); err != nil {
		t.Fatalf("report file does not exist: %v", err)
	}
}

// TS-06-10 (unit): report_file names the path actually written when the
// write succeeds.
func TestTS06_10_ReportFileNamesThePathActuallyWritten(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	want := filepath.Join(t.TempDir(), "impl-report.json")

	app, _ := newApp(t, nil)
	env, code, _ := runApp(t, app, []string{"--dir", dir, "--report-file", want, "a spec directory"}, "")
	if code != ExitOK {
		t.Fatalf("code=%d", code)
	}
	if env.ReportFile != want {
		t.Errorf("env.ReportFile = %q, want %q", env.ReportFile, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("report file does not exist at %s: %v", want, err)
	}
}

// TS-06-11 (integration): A report file is written for a failed run and for
// a post-classification usage error.
func TestTS06_11_ReportFileWrittenForFailureAndUsageError(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()

	t.Run("failing run", func(t *testing.T) {
		app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
			return ExitFailed, map[string]string{"stage": "failed"},
				&ErrorInfo{Stage: "verify", Category: "internal", Message: "boom"}
		})
		env, code, _ := runApp(t, app, []string{"--dir", dir, "x"}, "")
		if code != ExitFailed {
			t.Fatalf("code=%d", code)
		}
		if env.ReportFile == "" {
			t.Fatal("expected a report file for a failing run")
		}
		if _, err := os.Stat(env.ReportFile); err != nil {
			t.Fatalf("report file does not exist: %v", err)
		}
	})

	t.Run("post-classification usage error", func(t *testing.T) {
		app, _ := newApp(t, nil)
		app.CheckInput = func(in Input) error {
			return Usagef("--input-kind issue needs a forge URL; %s is %s", in.Origin, in.Kind)
		}
		env, code, _ := runApp(t, app, []string{"--dir", dir, "not a url"}, "")
		if code != ExitUsage {
			t.Fatalf("code=%d", code)
		}
		if env.ReportFile == "" {
			t.Fatal("expected a report file for a post-classification usage error")
		}
		if _, err := os.Stat(env.ReportFile); err != nil {
			t.Fatalf("report file does not exist: %v", err)
		}
	})
}

// TS-06-12 (unit): A bare invocation, -h/--help and --version print no JSON
// and write no report file.
func TestTS06_12_HumanDrivenPathsWriteNoReportFile(t *testing.T) {
	dir := t.TempDir()

	t.Run("-h", func(t *testing.T) {
		want := filepath.Join(t.TempDir(), "h.json")
		app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
			t.Error("Exec ran for -h")
			return ExitOK, nil, nil
		})
		var stdout, stderr bytes.Buffer
		code := app.Main(context.Background(), []string{"--dir", dir, "--report-file", want, "-h"},
			strings.NewReader(""), &stdout, &stderr)
		if code != ExitOK {
			t.Fatalf("code=%d", code)
		}
		if stdout.Len() != 0 {
			t.Errorf("expected empty stdout, got %q", stdout.String())
		}
		if _, err := os.Stat(want); !os.IsNotExist(err) {
			t.Errorf("expected no report file at %s, stat err=%v", want, err)
		}
	})

	t.Run("--version", func(t *testing.T) {
		want := filepath.Join(t.TempDir(), "v.json")
		app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
			t.Error("Exec ran for --version")
			return ExitOK, nil, nil
		})
		var stdout, stderr bytes.Buffer
		code := app.Main(context.Background(), []string{"--dir", dir, "--report-file", want, "--version"},
			strings.NewReader(""), &stdout, &stderr)
		if code != ExitOK {
			t.Fatalf("code=%d", code)
		}
		if strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") {
			t.Errorf("expected no JSON, got %q", stdout.String())
		}
		if _, err := os.Stat(want); !os.IsNotExist(err) {
			t.Errorf("expected no report file at %s, stat err=%v", want, err)
		}
	})

	t.Run("bare invocation on a terminal", func(t *testing.T) {
		f, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
		if err != nil {
			t.Skipf("cannot open %s: %v", os.DevNull, err)
		}
		defer f.Close()
		if !isTerminal(f) {
			t.Skipf("%s is not reported as a terminal", os.DevNull)
		}
		want := filepath.Join(t.TempDir(), "bare.json")
		app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
			t.Error("Exec ran for a bare invocation")
			return ExitOK, nil, nil
		})
		var stderr bytes.Buffer
		code := app.Main(context.Background(), []string{"--dir", dir, "--report-file", want},
			strings.NewReader(""), f, &stderr)
		if code != ExitUsage {
			t.Fatalf("code=%d", code)
		}
		if _, err := os.Stat(want); !os.IsNotExist(err) {
			t.Errorf("expected no report file at %s, stat err=%v", want, err)
		}
	})
}

// TS-06-13 (unit): The report file's content is byte-for-byte identical to
// what --detail full would have printed for the same run, once the two
// runs' inherently non-deterministic fields (started_at, duration_ms,
// report_file itself, and the report_file entry artifacts carries — both
// name two different paths, one per run's own t.TempDir()) are set aside.
func TestTS06_13_ReportFileByteIdenticalToFullDetailStdout(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	dir := t.TempDir()
	deterministicExec := func(context.Context, Deps) (int, any, *ErrorInfo) {
		return ExitOK, map[string]any{
			"stage":  "done",
			"action": "created",
			"url":    "https://example.com/issues/1",
		}, nil
	}

	summaryReport := filepath.Join(t.TempDir(), "summary-report.json")
	appSummary, _ := newApp(t, deterministicExec)
	_, codeS, _ := runApp(t, appSummary, []string{
		"--dir", dir, "--detail", "summary", "--report-file", summaryReport, "same deterministic input",
	}, "")
	if codeS != ExitOK {
		t.Fatalf("summary run code=%d", codeS)
	}
	rfBytes, err := os.ReadFile(summaryReport)
	if err != nil {
		t.Fatalf("reading report file: %v", err)
	}

	fullReport := filepath.Join(t.TempDir(), "full-report.json")
	appFull, _ := newApp(t, deterministicExec)
	var stdoutFull, stderrFull bytes.Buffer
	codeF := appFull.Main(context.Background(), []string{
		"--dir", dir, "--detail", "full", "--report-file", fullReport, "same deterministic input",
	}, strings.NewReader(""), &stdoutFull, &stderrFull)
	if codeF != ExitOK {
		t.Fatalf("full run code=%d", codeF)
	}

	normalize := func(b []byte) string {
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unmarshal %s: %v", b, err)
		}
		delete(m, "started_at")
		delete(m, "duration_ms")
		delete(m, "report_file")
		delete(m, "session_id")
		delete(m, "artifacts")
		out, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(out)
	}

	if got, want := normalize(rfBytes), normalize(stdoutFull.Bytes()); got != want {
		t.Errorf("report file (summary run) != stdout (full run) once timing/path fields are set aside:\nreport: %s\nstdout: %s", got, want)
	}
}
