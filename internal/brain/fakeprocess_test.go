package brain

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The fake process is this test binary, run under the name af or claude: TestMain sees
// the name and acts as the program instead of running the tests. A process that is
// started with env -i has none of the environment of the test, so what it does is
// told by a script in the tree it runs in, .agent-fox/fake.json, which the test places
// through the box, and what it did is noted in .agent-fox/fake-record.jsonl.
func TestMain(m *testing.M) {
	switch filepath.Base(os.Args[0]) {
	case ProgramAF, ProgramClaude:
		os.Exit(fakeProcess(filepath.Base(os.Args[0]), os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeScript is what a fake process is told to do.
type fakeScript struct {
	// What --version answers, and the exit code it ends with, for each program.
	AFVersion     string         `json:"af_version"`
	AFCommit      string         `json:"af_commit"`
	ClaudeVersion string         `json:"claude_version"`
	VersionExit   map[string]int `json:"version_exit"`

	// What a run does: wait, start a child that waits too, print, and end with a code.
	SleepMS     int    `json:"sleep_ms"`
	SpawnChild  bool   `json:"spawn_child"`
	Stdout      string `json:"stdout"`
	StdoutBytes int    `json:"stdout_bytes"`
	Stderr      string `json:"stderr"`
	StderrBytes int    `json:"stderr_bytes"`
	Exit        int    `json:"exit"`
}

// fakeRecord is a line of the record of a fake process.
type fakeRecord struct {
	Program    string   `json:"program"`
	Argv       []string `json:"argv"`
	Env        []string `json:"env"`
	Cwd        string   `json:"cwd"`
	StdinBytes int      `json:"stdin_bytes"`
	PID        int      `json:"pid"`
}

const (
	fakeScriptPath = ".agent-fox/fake.json"
	fakeRecordPath = ".agent-fox/fake-record.jsonl"
	fakeChildPath  = ".agent-fox/fake-child.pid"
)

// fakeProcess is the program: it returns the exit code of the process.
func fakeProcess(program string, args []string) int {
	var script fakeScript
	if data, err := os.ReadFile(fakeScriptPath); err == nil {
		_ = json.Unmarshal(data, &script)
	}
	stdinBytes := 0
	if len(args) != 1 || args[0] != "--version" {
		n, _ := io.Copy(io.Discard, os.Stdin)
		stdinBytes = int(n)
	}
	cwd, _ := os.Getwd()
	line, _ := json.Marshal(fakeRecord{Program: program, Argv: args, Env: os.Environ(), Cwd: cwd, StdinBytes: stdinBytes, PID: os.Getpid()})
	if f, err := os.OpenFile(fakeRecordPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		_, _ = f.Write(append(line, '\n'))
		_ = f.Close()
	}

	if len(args) == 1 && args[0] == "--version" {
		switch program {
		case ProgramAF:
			fmt.Printf("{\"name\":\"af\",\"version\":%q,\"commit\":%q}\n", orDefault(script.AFVersion, "v1.0.0"), orDefault(script.AFCommit, "abc123"))
		default:
			fmt.Printf("%s (Claude Code)\n", orDefault(script.ClaudeVersion, "2.0.0"))
		}
		return script.VersionExit[program]
	}

	if script.SpawnChild {
		child := exec.Command("sleep", "30")
		if err := child.Start(); err == nil {
			_ = os.WriteFile(fakeChildPath, []byte(strconv.Itoa(child.Process.Pid)), 0o644)
		}
	}
	if script.SleepMS > 0 {
		sleepMS(script.SleepMS)
	}
	fmt.Print(script.Stdout)
	fmt.Print(strings.Repeat("o", script.StdoutBytes))
	fmt.Fprint(os.Stderr, strings.Repeat("e", script.StderrBytes)+script.Stderr)
	return script.Exit
}

func sleepMS(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// fakeBin makes a directory with af and claude in it, each a link to this test binary.
func fakeBin(t *testing.T) (dir, af, claude string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{ProgramAF, ProgramClaude} {
		if err := os.Symlink(self, filepath.Join(dir, name)); err != nil {
			t.Skipf("cannot link the test binary as %s: %v", name, err)
		}
	}
	return dir, filepath.Join(dir, ProgramAF), filepath.Join(dir, ProgramClaude)
}
