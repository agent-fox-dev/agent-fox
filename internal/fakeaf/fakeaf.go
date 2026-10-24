// Package fakeaf is the fake af of the tests: the test binary itself, run under the name
// af. The TestMain of a package that wants it hands such a process to Run instead of
// running the tests, and Install makes the directory it is found in. A package that wants
// the real af instead runs its own app when Name says the binary was run as af.
//
// A process started with env -i has none of the environment of the test, so what the
// fake does is told by a script in the tree it runs in, ScriptPath, and what it did is
// noted in the files beside it. The script is JSON; the fields of Script are its format,
// and docs/development.md describes it.
package fakeaf

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

// AF is the name the test binary is run under to be the fake.
const AF = "af"

// The files of the fake, relative to the tree it runs in: the script it is told by, and
// what it noted.
const (
	ScriptPath = ".agent-fox/fake.json"
	RecordPath = ".agent-fox/fake-record.jsonl"
	ChildPath  = ".agent-fox/fake-child.pid"
)

// Script is what the fake is told to do.
type Script struct {
	// What --version answers, and the exit code it ends with.
	AFVersion   string         `json:"af_version"`
	AFCommit    string         `json:"af_commit"`
	VersionExit map[string]int `json:"version_exit"`

	// What a run does: wait, start a child that waits too, print, and end with a code.
	SleepMS     int    `json:"sleep_ms"`
	SpawnChild  bool   `json:"spawn_child"`
	Stderr      string `json:"stderr"`
	StderrBytes int    `json:"stderr_bytes"`
	Exit        int    `json:"exit"`
	// OutputJSON is what it prints on its stdout.
	OutputJSON string `json:"output_json"`
}

// Record is a line of the record of the fake.
type Record struct {
	Program string   `json:"program"`
	Argv    []string `json:"argv"`
	Env     []string `json:"env"`
	Cwd     string   `json:"cwd"`
	PID     int      `json:"pid"`
}

// Name is the program the test binary is run as: AF, or "" when it is run as itself.
func Name() string {
	if filepath.Base(os.Args[0]) == AF {
		return AF
	}
	return ""
}

// Run is the fake af: it returns the exit code of the process.
func Run(args []string) int {
	var script Script
	if data, err := os.ReadFile(ScriptPath); err == nil {
		_ = json.Unmarshal(data, &script)
	}
	version := len(args) == 1 && args[0] == "--version"
	if !version {
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
	cwd, _ := os.Getwd()
	line, _ := json.Marshal(Record{Program: AF, Argv: args, Env: os.Environ(), Cwd: cwd, PID: os.Getpid()})
	if f, err := os.OpenFile(RecordPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		_, _ = f.Write(append(line, '\n'))
		_ = f.Close()
	}

	if version {
		fmt.Printf("{\"name\":\"af\",\"version\":%q,\"commit\":%q}\n", orDefault(script.AFVersion, "v1.0.0"), orDefault(script.AFCommit, "abc123"))
		return script.VersionExit[AF]
	}
	if script.SpawnChild {
		child := exec.Command("sleep", "30")
		if err := child.Start(); err == nil {
			_ = os.WriteFile(ChildPath, []byte(strconv.Itoa(child.Process.Pid)), 0o644)
		}
	}
	if script.SleepMS > 0 {
		time.Sleep(time.Duration(script.SleepMS) * time.Millisecond)
	}
	fmt.Fprint(os.Stderr, strings.Repeat("e", script.StderrBytes)+script.Stderr)
	fmt.Print(script.OutputJSON)
	return script.Exit
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// LinkSelf makes a directory with af in it, a link to this test binary, for a TestMain to point
// brain.DefaultAFPath at, and returns the directory and the path of af. The caller removes the
// directory when the tests are over.
func LinkSelf() (dir, af string, err error) {
	self, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	if dir, err = os.MkdirTemp("", "af-self-*"); err != nil {
		return "", "", err
	}
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		return "", "", err
	}
	af = filepath.Join(dir, AF)
	if err := os.Symlink(self, af); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", err
	}
	return dir, af, nil
}

// Install makes a directory with af in it, a link to this test binary, and returns it with
// the path of af. The test is skipped where the binary cannot be linked.
func Install(t testing.TB) (dir, af string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	af = filepath.Join(dir, AF)
	if err := os.Symlink(self, af); err != nil {
		t.Skipf("cannot link the test binary as af: %v", err)
	}
	return dir, af
}
