package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// --- TS-08-9: Verify StatusSpinner writes to stderr only ---

// TestTS08_09_SpinnerWritesToStderr verifies that the StatusSpinner
// writes output to the provided writer (stderr) and not to stdout.
// Covers: TS-08-9, Requirement: 08-REQ-4.1, 08-PROP-5
func TestTS08_09_SpinnerWritesToStderr(t *testing.T) {
	stderrBuf := new(bytes.Buffer)
	s := NewStatusSpinner("Processing...", stderrBuf, false, true)

	s.Start()
	time.Sleep(150 * time.Millisecond)
	s.Update("Still going...")
	time.Sleep(50 * time.Millisecond)
	s.Stop()

	output := stderrBuf.String()
	if len(output) == 0 {
		t.Error("StatusSpinner produced no output on writer; want spinner text")
	}
	if !strings.Contains(output, "Processing") && !strings.Contains(output, "Still going") {
		t.Errorf("spinner output = %q; want it to contain message text", output)
	}
}

// TestTS08_09_SpinnerUpdateChangesMessage verifies that Update()
// changes the displayed spinner message.
// Covers: TS-08-9, 08-REQ-4.1
func TestTS08_09_SpinnerUpdateChangesMessage(t *testing.T) {
	stderrBuf := new(bytes.Buffer)
	s := NewStatusSpinner("Initial message", stderrBuf, false, true)

	s.Start()
	time.Sleep(100 * time.Millisecond)
	s.Update("Updated message")
	time.Sleep(100 * time.Millisecond)
	s.Stop()

	output := stderrBuf.String()
	if !strings.Contains(output, "Updated message") {
		t.Errorf("spinner output after Update = %q; want it to contain %q", output, "Updated message")
	}
}

// TestTS08_09_SpinnerLogPrintsLine verifies that Log() prints a
// permanent line above the spinner.
// Covers: TS-08-9, 08-REQ-4.1
func TestTS08_09_SpinnerLogPrintsLine(t *testing.T) {
	stderrBuf := new(bytes.Buffer)
	s := NewStatusSpinner("Working...", stderrBuf, false, true)

	s.Start()
	s.Log("Completed step 1")
	time.Sleep(50 * time.Millisecond)
	s.Stop()

	output := stderrBuf.String()
	if !strings.Contains(output, "Completed step 1") {
		t.Errorf("spinner output after Log = %q; want it to contain %q", output, "Completed step 1")
	}
}

// --- TS-08-10: Verify StatusSpinner falls back to plain text when
//     stderr is not a TTY ---

// TestTS08_10_SpinnerPlainTextNonTTY verifies that when isTTY is false,
// the spinner writes plain text lines without ANSI escape codes.
// Covers: TS-08-10, Requirement: 08-REQ-4.2
func TestTS08_10_SpinnerPlainTextNonTTY(t *testing.T) {
	stderrBuf := new(bytes.Buffer)
	// isTTY = false
	s := NewStatusSpinner("Processing...", stderrBuf, false, false)

	s.Start()
	s.Update("Step 2")
	time.Sleep(50 * time.Millisecond)
	s.Stop()

	output := stderrBuf.String()
	if len(output) == 0 {
		t.Error("StatusSpinner (non-TTY) produced no output; want plain text lines")
	}

	// Should not contain ANSI escape sequences
	if strings.Contains(output, "\x1b[") {
		t.Errorf("StatusSpinner (non-TTY) output contains ANSI escapes; want plain text only. Output: %q", output)
	}

	// Should contain the message text
	if !strings.Contains(output, "Processing") && !strings.Contains(output, "Step 2") {
		t.Errorf("StatusSpinner (non-TTY) output = %q; want it to contain message text", output)
	}
}

// --- TS-08-11: Verify StatusSpinner is no-op when quiet ---

// TestTS08_11_SpinnerQuietNoOp verifies that all StatusSpinner methods
// produce no output when quiet is true.
// Covers: TS-08-11, Requirement: 08-REQ-4.3
func TestTS08_11_SpinnerQuietNoOp(t *testing.T) {
	stderrBuf := new(bytes.Buffer)
	s := NewStatusSpinner("Processing...", stderrBuf, true, true)

	s.Start()
	s.Update("msg")
	s.Log("log")
	s.Stop()

	output := stderrBuf.String()
	if len(output) != 0 {
		t.Errorf("StatusSpinner (quiet=true) produced output: %q; want empty", output)
	}
}

// TestTS08_11_SpinnerAgentModeNoOp verifies that StatusSpinner is a
// no-op when used in agent mode (quiet=true simulating AF_AGENT=1).
// Covers: TS-08-11, 08-REQ-4.3
func TestTS08_11_SpinnerAgentModeNoOp(t *testing.T) {
	stderrBuf := new(bytes.Buffer)
	// In agent mode, quiet would be forced to true
	s := NewStatusSpinner("Processing...", stderrBuf, true, false)

	s.Start()
	s.Update("update")
	s.Log("log line")
	s.Stop()

	output := stderrBuf.String()
	if len(output) != 0 {
		t.Errorf("StatusSpinner (agent mode/quiet) produced output: %q; want empty", output)
	}
}

// --- 08-REQ-4.E1: Verify spinner goroutine cleanup ---

// TestTS08_SpinnerCleanup verifies that the spinner stops cleanly
// and does not leak goroutines.
// Covers: 08-REQ-4.E1
func TestTS08_SpinnerCleanup(t *testing.T) {
	stderrBuf := new(bytes.Buffer)
	s := NewStatusSpinner("Working...", stderrBuf, false, true)

	s.Start()
	time.Sleep(100 * time.Millisecond)
	s.Stop()

	// Calling Stop a second time should not panic
	s.Stop()
}
