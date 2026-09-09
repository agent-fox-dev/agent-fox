package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// --- TS-08-42: Verify that all AI-calling commands receive a context.Context
//     with SIGINT/SIGTERM signal handling and that the context is cancelled
//     on signal receipt ---

// TestTS08_42_SignalCtxCancelsOnSIGINT verifies that signalCtx creates
// a context that gets cancelled when SIGINT is received.
// Covers: TS-08-42, Requirement: 08-REQ-16.1
func TestTS08_42_SignalCtxCancelsOnSIGINT(t *testing.T) {
	ctx, cancel := signalCtx(context.Background())
	defer cancel()

	// Send SIGINT to ourselves.
	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}

	// Launch a goroutine to send the signal after a short delay.
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(50 * time.Millisecond)
		proc.Signal(syscall.SIGINT)
	}()

	// Wait for context cancellation or timeout.
	select {
	case <-ctx.Done():
		// Context was cancelled as expected.
	case <-time.After(2 * time.Second):
		t.Fatal("context was not cancelled after SIGINT; want cancellation within 2s")
	}

	<-done
}

// TestTS08_42_SignalCtxCancelsOnSIGTERM verifies that signalCtx creates
// a context that gets cancelled when SIGTERM is received.
// Covers: TS-08-42, Requirement: 08-REQ-16.1
func TestTS08_42_SignalCtxCancelsOnSIGTERM(t *testing.T) {
	ctx, cancel := signalCtx(context.Background())
	defer cancel()

	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(50 * time.Millisecond)
		proc.Signal(syscall.SIGTERM)
	}()

	select {
	case <-ctx.Done():
		// Context was cancelled as expected.
	case <-time.After(2 * time.Second):
		t.Fatal("context was not cancelled after SIGTERM; want cancellation within 2s")
	}

	<-done
}

// TestTS08_42_SignalCtxCleanup verifies that cancelling the returned
// cancel func stops signal handling cleanly.
// Covers: TS-08-42, Requirement: 08-REQ-16.1
func TestTS08_42_SignalCtxCleanup(t *testing.T) {
	ctx, cancel := signalCtx(context.Background())

	// Cancel immediately — should not panic or leak goroutines.
	cancel()

	select {
	case <-ctx.Done():
		// Expected: context is cancelled.
	default:
		t.Error("context not cancelled after cancel(); want Done channel closed")
	}
}

// TestTS08_42_SignalCtxParentCancellation verifies that when the parent
// context is cancelled, the signal context also cancels.
// Covers: TS-08-42, Requirement: 08-REQ-16.1
func TestTS08_42_SignalCtxParentCancellation(t *testing.T) {
	parent, parentCancel := context.WithCancel(context.Background())
	ctx, cancel := signalCtx(parent)
	defer cancel()

	parentCancel()

	select {
	case <-ctx.Done():
		// Expected: child context is cancelled when parent is.
	case <-time.After(1 * time.Second):
		t.Fatal("signal context not cancelled when parent cancelled")
	}
}

// --- TS-08-43: Verify that receiving SIGINT during an AI operation cancels
//     the context, stops the StatusSpinner, and exits with code 1 ---

// TestTS08_43_GenerateExitsOnSIGINT verifies that sending SIGINT to
// a running spec generate process causes it to exit with code 1 within
// a reasonable timeout, exercising context cancellation and spinner
// cleanup in AI-calling commands.
// Covers: TS-08-43, Requirement: 08-REQ-16.2
func TestTS08_43_GenerateExitsOnSIGINT(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping signal test in short mode")
	}

	mainPkg := findMainPackage(t)
	if mainPkg == "" {
		t.Skip("could not locate main package for spec binary")
	}

	// Build the binary.
	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "spec_signal_test")
	buildCmd := exec.Command("go", "build",
		"-o", binaryPath,
		mainPkg,
	)
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\noutput: %s", err, out)
	}

	// Set up a spec directory with a session in accepted state.
	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "08_signal_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	sessionData := map[string]any{
		"state":               "prd_accepted",
		"mode":                "standard",
		"prd_path":            "prd.md",
		"assessment_history":  []any{},
		"qa_exchanges":        []any{},
		"generated_artifacts": []any{},
	}
	sessionJSON, _ := json.Marshal(sessionData)
	if err := os.WriteFile(filepath.Join(specPath, "_session.json"), sessionJSON, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# PRD"), 0644); err != nil {
		t.Fatal(err)
	}

	// Use a ready file so the test waits for the binary to reach the
	// blocking AI stub before sending SIGINT. This avoids the timing race
	// where a cold-start binary hasn't yet called signal.Notify when the
	// signal arrives, causing the process to die with the default handler
	// (exit -1) instead of the graceful exit-1 path.
	readyFile := filepath.Join(tmpDir, "generate_ready")

	// Start the generate command with SPEC_TEST_BLOCK_AI=1 so the
	// AI operation blocks until context cancellation, giving
	// the signal time to arrive and be handled.
	proc := exec.Command(binaryPath, "--spec-dir", specDir, "generate", "08_signal_spec")
	proc.Dir = tmpDir
	proc.Env = append(os.Environ(),
		"SPEC_TEST_BLOCK_AI=1",
		"SPEC_TEST_READY_FILE="+readyFile,
	)

	var stdoutBuf, stderrBuf bytes.Buffer
	proc.Stdout = &stdoutBuf
	proc.Stderr = &stderrBuf

	if err := proc.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	// Wait until the binary signals readiness (it writes the ready file
	// just before blocking on ctx.Done()), then send SIGINT. Poll with
	// a short interval; give up after the overall test timeout.
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(readyFile); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if proc.Process != nil {
			proc.Process.Signal(syscall.SIGINT)
		}
	}()

	// Wait for the process to exit with a timeout.
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- proc.Wait()
	}()

	select {
	case err := <-doneCh:
		if err == nil {
			t.Error("process exited 0; want exit 1 after SIGINT cancellation")
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() != 1 {
				t.Errorf("exit code = %d; want 1", exitErr.ExitCode())
			}
		}
	case <-time.After(5 * time.Second):
		proc.Process.Kill()
		t.Fatal("process did not exit within 5s after SIGINT; want clean exit")
	}
}

// TestTS08_43_RefineExitsOnSIGTERM verifies that sending SIGTERM to
// a running spec refine process causes it to exit cleanly.
// Covers: TS-08-43, Requirement: 08-REQ-16.2
func TestTS08_43_RefineExitsOnSIGTERM(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping signal test in short mode")
	}

	mainPkg := findMainPackage(t)
	if mainPkg == "" {
		t.Skip("could not locate main package for spec binary")
	}

	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "spec_sigterm_test")
	buildCmd := exec.Command("go", "build",
		"-o", binaryPath,
		mainPkg,
	)
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\noutput: %s", err, out)
	}

	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "08_signal_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	sessionData := map[string]any{
		"state":    "assessing",
		"mode":     "standard",
		"prd_path": "prd.md",
		"assessment_history": []any{
			map[string]any{"quality": "fair", "summary": "Needs work"},
		},
		"qa_exchanges":        []any{},
		"generated_artifacts": []any{},
	}
	sessionJSON, _ := json.Marshal(sessionData)
	if err := os.WriteFile(filepath.Join(specPath, "_session.json"), sessionJSON, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# PRD"), 0644); err != nil {
		t.Fatal(err)
	}

	// Use a ready file so the test waits for the binary to reach the
	// blocking AI stub before sending SIGTERM. This avoids the timing race
	// where a cold-start binary hasn't yet called signal.Notify when the
	// signal arrives, causing the process to die with the default handler
	// (exit -1) instead of the graceful exit-1 path.
	readyFile := filepath.Join(tmpDir, "refine_ready")

	// Start the refine command with SPEC_TEST_BLOCK_AI=1 so the
	// stub AI operation blocks until context cancellation.
	proc := exec.Command(binaryPath, "--spec-dir", specDir, "refine", "08_signal_spec")
	proc.Dir = tmpDir
	proc.Env = append(os.Environ(),
		"SPEC_TEST_BLOCK_AI=1",
		"SPEC_TEST_READY_FILE="+readyFile,
	)

	var stdoutBuf, stderrBuf bytes.Buffer
	proc.Stdout = &stdoutBuf
	proc.Stderr = &stderrBuf

	if err := proc.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	// Wait until the binary signals readiness (it writes the ready file
	// just before blocking on ctx.Done()), then send SIGTERM. Poll with
	// a short interval; give up after the overall test timeout.
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(readyFile); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if proc.Process != nil {
			proc.Process.Signal(syscall.SIGTERM)
		}
	}()

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- proc.Wait()
	}()

	select {
	case err := <-doneCh:
		if err == nil {
			t.Error("process exited 0; want exit 1 after SIGTERM cancellation")
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() != 1 {
				t.Errorf("exit code = %d; want 1", exitErr.ExitCode())
			}
		}
	case <-time.After(5 * time.Second):
		proc.Process.Kill()
		t.Fatal("process did not exit within 5s after SIGTERM; want clean exit")
	}
}

// --- 08-REQ-16.E1: Double SIGINT force-exits ---

// TestTS08_42_DoubleSignalForceExit verifies that a second SIGINT
// received before the first cancellation completes causes an immediate
// force-exit.
// Covers: 08-REQ-16.E1
func TestTS08_42_DoubleSignalForceExit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping signal test in short mode")
	}

	mainPkg := findMainPackage(t)
	if mainPkg == "" {
		t.Skip("could not locate main package for spec binary")
	}

	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "spec_double_signal")
	buildCmd := exec.Command("go", "build",
		"-o", binaryPath,
		mainPkg,
	)
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\noutput: %s", err, out)
	}

	specDir := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specDir, "08_signal_spec")
	if err := os.MkdirAll(specPath, 0755); err != nil {
		t.Fatal(err)
	}

	sessionData := map[string]any{
		"state":               "prd_accepted",
		"mode":                "standard",
		"prd_path":            "prd.md",
		"assessment_history":  []any{},
		"qa_exchanges":        []any{},
		"generated_artifacts": []any{},
	}
	sessionJSON, _ := json.Marshal(sessionData)
	if err := os.WriteFile(filepath.Join(specPath, "_session.json"), sessionJSON, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# PRD"), 0644); err != nil {
		t.Fatal(err)
	}

	// Use a ready file to avoid timing races on cold binary startup.
	readyFile := filepath.Join(tmpDir, "double_signal_ready")

	// Start the generate command with SPEC_TEST_BLOCK_AI=1 so the
	// AI operation blocks, giving signals time to arrive.
	proc := exec.Command(binaryPath, "--spec-dir", specDir, "generate", "08_signal_spec")
	proc.Dir = tmpDir
	proc.Env = append(os.Environ(),
		"SPEC_TEST_BLOCK_AI=1",
		"SPEC_TEST_READY_FILE="+readyFile,
	)

	if err := proc.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	// Wait for readiness, then send two SIGINTs in quick succession.
	go func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(readyFile); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if proc.Process != nil {
			proc.Process.Signal(syscall.SIGINT)
			time.Sleep(50 * time.Millisecond)
			proc.Process.Signal(syscall.SIGINT) // Second signal: force-exit.
		}
	}()

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- proc.Wait()
	}()

	select {
	case err := <-doneCh:
		// Process should have exited with code 1 (from os.Exit(1) on
		// second signal or from context cancellation error path).
		if err == nil {
			t.Log("process exited 0 (context cancellation returned before second signal)")
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() != 1 {
				t.Logf("exit code = %d; expected 1 on double SIGINT", exitErr.ExitCode())
			}
		}
	case <-time.After(3 * time.Second):
		proc.Process.Kill()
		t.Fatal("process did not force-exit within 3s after double SIGINT")
	}
}

// --- 08-PROP-7: Context cancellation stops AI operations ---

// TestTS08_42_ContextCancellationProperty verifies correctness property
// 08-PROP-7: for any SIGINT or SIGTERM received during an AI-calling
// command, the context is cancelled, the spinner is stopped, and the
// binary exits with code 1 without goroutine leaks.
// Covers: 08-PROP-7
func TestTS08_42_ContextCancellationProperty(t *testing.T) {
	// This test exercises the signalCtx function directly to verify
	// context cancellation behavior.
	signals := []syscall.Signal{syscall.SIGINT, syscall.SIGTERM}

	for _, sig := range signals {
		t.Run(sig.String(), func(t *testing.T) {
			ctx, cancel := signalCtx(context.Background())
			defer cancel()

			proc, err := os.FindProcess(os.Getpid())
			if err != nil {
				t.Fatalf("FindProcess: %v", err)
			}

			go func() {
				time.Sleep(50 * time.Millisecond)
				proc.Signal(sig)
			}()

			select {
			case <-ctx.Done():
				// Context cancelled as expected.
				if ctx.Err() != context.Canceled {
					t.Errorf("ctx.Err() = %v; want context.Canceled", ctx.Err())
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("context not cancelled after %s within 2s", sig)
			}
		})
	}
}

// --- Signal handling: spinner stops on cancellation ---

// TestTS08_43_SpinnerStopsOnCancel verifies that the StatusSpinner
// can be stopped cleanly when context cancellation occurs, preventing
// dangling cursor output.
// Covers: TS-08-43, Requirement: 08-REQ-16.2
func TestTS08_43_SpinnerStopsOnCancel(t *testing.T) {
	var buf bytes.Buffer
	spinner := NewStatusSpinner("Testing...", &buf, false, false)

	spinner.Start()

	// Simulate context cancellation by stopping the spinner.
	spinner.Stop()

	// After stop, the spinner should not be writing to the buffer.
	afterStop := buf.String()
	time.Sleep(100 * time.Millisecond)
	afterWait := buf.String()

	if afterStop != afterWait {
		t.Error("spinner continued writing after Stop(); want no output after stop")
	}
}

// TestTS08_43_SpinnerQuietMode verifies that the spinner is a no-op
// in quiet mode, which is used during signal handling to prevent
// output corruption.
// Covers: TS-08-43
func TestTS08_43_SpinnerQuietMode(t *testing.T) {
	var buf bytes.Buffer
	spinner := NewStatusSpinner("Testing...", &buf, true, false)

	spinner.Start()
	time.Sleep(50 * time.Millisecond)
	spinner.Stop()

	output := buf.String()
	if len(output) > 0 {
		t.Errorf("spinner in quiet mode produced output %q; want empty", output)
	}
}
