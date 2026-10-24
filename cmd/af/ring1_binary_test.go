package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestGateRunSignal_TS_06_24 verifies TS-06-24, the signal case, on the built
// binary: SIGINT stops the check's process group, stores no result.json and
// fails the command as cancelled.
// Verifies: 06-REQ-3.8
func TestGateRunSignal_TS_06_24(t *testing.T) {
	requireSh(t)
	if runtime.GOOS == "windows" {
		t.Skip("no signals to send")
	}
	binary := buildBinary(t)
	dir := gitRepo(t, nil)
	pidFile := spawningGate(t, dir)

	cmd := exec.Command(binary, "gate", "run", "--dir", dir)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if data, _ := os.ReadFile(pidFile); strings.TrimSpace(string(data)) != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	child := pidOf(t, pidFile)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()

	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("af ended with %v, want exit code 1", err)
	}
	env := oneDocument(t, stdout.String())
	if env["status"] != "failed" || field(env, "error", "category") != "cancelled" {
		t.Errorf("status %v, category %v; want failed and cancelled", env["status"], field(env, "error", "category"))
	}
	if !waitGone(child) {
		t.Error("the child of the check is still running")
	}
	if results, _ := filepath.Glob(filepath.Join(dir, ".agent-fox", "gate", "*", "result.json")); len(results) != 0 {
		t.Errorf("a cancelled run stored %v", results)
	}
}

// TestRevertCheckSignal_TS_06_45 verifies TS-06-45, the signal case, on the
// built binary: SIGINT while the reverted tree is tested still restores the
// tree and the index.
// Verifies: 06-REQ-6.7
func TestRevertCheckSignal_TS_06_45(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no signals to send")
	}
	for name, signal := range map[string]os.Signal{"SIGINT": os.Interrupt, "SIGTERM": syscall.SIGTERM, "SIGHUP": syscall.SIGHUP} {
		t.Run(name, func(t *testing.T) { revertCheckSignal(t, signal) })
	}
}
