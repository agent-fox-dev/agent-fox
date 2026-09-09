package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// --- TS-08-7: Verify emit writes pretty-printed JSON with 2-space indent ---

// TestTS08_07_EmitPrettyPrintedJSON verifies that emit writes valid
// JSON with 2-space indentation to stdout.
// Covers: TS-08-7, Requirement: 08-REQ-3.1
func TestTS08_07_EmitPrettyPrintedJSON(t *testing.T) {
	// Capture stdout by redirecting it
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	data := map[string]any{"ok": true, "value": 42}
	emitErr := emit(data)

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	if _, readErr := buf.ReadFrom(r); readErr != nil {
		t.Fatal(readErr)
	}
	output := buf.String()

	if emitErr != nil {
		t.Fatalf("emit() returned error: %v", emitErr)
	}

	// Verify output is valid JSON
	var parsed map[string]any
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("emit() output is not valid JSON: %v\noutput: %q", err, output)
	}

	// Verify 2-space indentation
	if !strings.Contains(output, "  \"ok\"") {
		t.Errorf("emit() output missing 2-space indentation; got:\n%s", output)
	}

	// Verify values
	if parsed["ok"] != true {
		t.Errorf("parsed.ok = %v; want true", parsed["ok"])
	}
	// JSON numbers are float64 by default
	if val, ok := parsed["value"].(float64); !ok || val != 42 {
		t.Errorf("parsed.value = %v; want 42", parsed["value"])
	}
}

// TestTS08_07_EmitBrokenPipeSilent verifies that emit handles broken
// pipe errors silently without returning an error.
// Covers: TS-08-7, 08-REQ-3.E2
func TestTS08_07_EmitBrokenPipeSilent(t *testing.T) {
	// Create a pipe and immediately close the read end to simulate
	// broken pipe
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r.Close() // Close read end to trigger EPIPE on write
	os.Stdout = w

	data := map[string]any{"ok": true, "value": "test"}
	emitErr := emit(data)

	w.Close()
	os.Stdout = oldStdout

	// emit should suppress broken pipe and return nil
	if emitErr != nil {
		t.Errorf("emit() with broken pipe returned error: %v; want nil", emitErr)
	}
}

// TestTS08_07_EmitUnmarshalableData verifies that emit returns an error
// when given data that cannot be marshalled to JSON.
// Covers: 08-REQ-3.E1
func TestTS08_07_EmitUnmarshalableData(t *testing.T) {
	// Channels cannot be marshalled to JSON
	ch := make(chan int)
	emitErr := emit(ch)

	if emitErr == nil {
		t.Error("emit() with unmarshalable data returned nil; want error")
	}
}

// --- TS-08-8: Verify emitOK merges fields with {ok: true} ---

// TestTS08_08_EmitOKMergesFields verifies that emitOK wraps data with
// {"ok": true} and calls emit to write valid indented JSON.
// Covers: TS-08-8, Requirement: 08-REQ-3.2
func TestTS08_08_EmitOKMergesFields(t *testing.T) {
	// Capture stdout
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	emitErr := emitOK("spec_dir", ".specs", "state", "INIT")

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	if _, readErr := buf.ReadFrom(r); readErr != nil {
		t.Fatal(readErr)
	}
	output := buf.String()

	if emitErr != nil {
		t.Fatalf("emitOK() returned error: %v", emitErr)
	}

	// Verify output is valid JSON
	var parsed map[string]any
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("emitOK() output is not valid JSON: %v\noutput: %q", err, output)
	}

	// Verify ok: true is present
	if parsed["ok"] != true {
		t.Errorf("parsed.ok = %v; want true", parsed["ok"])
	}

	// Verify merged fields
	if parsed["spec_dir"] != ".specs" {
		t.Errorf("parsed.spec_dir = %v; want %q", parsed["spec_dir"], ".specs")
	}
	if parsed["state"] != "INIT" {
		t.Errorf("parsed.state = %v; want %q", parsed["state"], "INIT")
	}
}

// TestTS08_08_EmitOKAlwaysHasOkTrue verifies the invariant that emitOK
// always produces an output with ok: true.
// Covers: 08-PROP-2
func TestTS08_08_EmitOKAlwaysHasOkTrue(t *testing.T) {
	// Capture stdout
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	// Even with no extra fields, ok: true should be present
	emitErr := emitOK()

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	if _, readErr := buf.ReadFrom(r); readErr != nil {
		t.Fatal(readErr)
	}
	output := buf.String()

	if emitErr != nil {
		t.Fatalf("emitOK() returned error: %v", emitErr)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("emitOK() output is not valid JSON: %v\noutput: %q", err, output)
	}

	if parsed["ok"] != true {
		t.Errorf("parsed.ok = %v; want true (08-PROP-2 invariant)", parsed["ok"])
	}
}
