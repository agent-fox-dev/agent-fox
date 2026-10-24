//go:build !windows

package checks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// alive reports whether a process with the pid exists.
func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// TestCancellationStopsTheGroupAndStoresNothing_TS_06_24 verifies the engine's
// part of TS-06-24:
// A cancelled run stops the check's whole process group, stores no
// result.json, and returns the cancellation error; a following run executes.
// Verifies: 06-REQ-3.8
func TestCancellationStopsTheGroupAndStoresNothing_TS_06_24(t *testing.T) {
	requireSh(t)
	dir := newRepo(t)
	aside := t.TempDir()
	pidFile, proceed := filepath.Join(aside, "pid"), filepath.Join(aside, "proceed")
	check := shCheck("spawns", fmt.Sprintf("if [ -e '%s' ]; then echo fine; exit 0; fi; sleep 60 & echo $! > '%s'; wait", proceed, pidFile))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			if data, _ := os.ReadFile(pidFile); strings.TrimSpace(string(data)) != "" {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	_, err := Run(ctx, dir, Options{Table: tableOf(check)})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	data, _ := os.ReadFile(pidFile)
	pid, perr := strconv.Atoi(strings.TrimSpace(string(data)))
	if perr != nil {
		t.Fatalf("the check wrote %q to its pid file", data)
	}
	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(pid) {
		t.Errorf("the child of the check (pid %d) is still running", pid)
	}
	if results, _ := filepath.Glob(filepath.Join(dir, ".agent-fox", "gate", "*", "result.json")); len(results) != 0 {
		t.Errorf("a cancelled run stored %v", results)
	}
	if logs, _ := filepath.Glob(filepath.Join(dir, ".agent-fox", "gate", "*", "*.log")); len(logs) != 0 {
		t.Errorf("a cancelled run wrote %v", logs)
	}

	if err := os.WriteFile(proceed, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	res := mustRun(t, dir, Options{Table: tableOf(check)})
	if res.Cached || !res.Passed {
		t.Errorf("the run after the cancelled one: cached %v, passed %v; want a run that passes", res.Cached, res.Passed)
	}
}

// TestPerCheckDeadlineKillsTheGroup guards 06-REQ-3.3: a check that outlives
// its deadline is stopped with everything it started, and the run goes on.
func TestPerCheckDeadlineKillsTheGroup(t *testing.T) {
	requireSh(t)
	dir := newRepo(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	table := tableOf(
		shCheck("hangs", fmt.Sprintf("sleep 60 & echo $! > '%s'; wait", pidFile)),
		shCheck("after", "exit 0"),
	)

	res := mustRun(t, dir, Options{Table: table, CheckTimeout: 300 * time.Millisecond})

	if len(res.Checks) != 2 || res.Checks[0].Status != StatusTimeout || res.Checks[1].Status != StatusPass || res.Passed {
		t.Fatalf("checks = %+v, passed %v; want timeout then pass, and not passed", res.Checks, res.Passed)
	}
	data, _ := os.ReadFile(pidFile)
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("pid file = %q", data)
	}
	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(pid) {
		t.Errorf("the child of the timed-out check (pid %d) is still running", pid)
	}
}

// TestACheckThatLeavesAChildBehindStillPasses: a check that exits 0 and starts
// a process that keeps its output open (a server, a daemon) passed.
func TestACheckThatLeavesAChildBehindStillPasses(t *testing.T) {
	requireSh(t)
	dir := newRepo(t)

	res := mustRun(t, dir, Options{Table: tableOf(shCheck("daemon", "echo started; sleep 30 & exit 0"))})

	if len(res.Checks) != 1 || res.Checks[0].Status != StatusPass || res.Checks[0].ExitCode != 0 || !res.Passed {
		t.Errorf("checks = %+v, passed %v; want a pass", res.Checks, res.Passed)
	}
}
