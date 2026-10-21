// Package fakeclaude is the fake Claude Code and the fake af of the tests: the test
// binary itself, run under the name claude or af. The TestMain of a package that
// wants them hands such a process to Run instead of running the tests, and Install
// makes the directory they are found in.
//
// A process started with env -i has none of the environment of the test, so what a
// fake does is told by a script in the tree it runs in, ScriptPath, and what it did
// is noted in the files beside it. The script is JSON; the fields of Script are its
// format, and docs/development.md describes it. A test that has the tree of the process
// in hand places it at ScriptPath; one that has not (a command that opens its box itself)
// places it beside the programs, with WriteScript.
package fakeclaude

import (
	"context"
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

// The programs a fake can be, by the name the test binary is run under.
const (
	AF     = "af"
	Claude = "claude"
)

// The files of a fake, relative to the tree it runs in: the script it is told by, and
// what it noted.
const (
	ScriptPath   = ".agent-fox/fake.json"
	RecordPath   = ".agent-fox/fake-record.jsonl"
	ChildPath    = ".agent-fox/fake-child.pid"
	HooksPath    = ".agent-fox/fake-hooks.json"
	SubmitsPath  = ".agent-fox/fake-submits.json"
	CommandsPath = ".agent-fox/fake-commands.json"
	// BesideScript is the name of the script that is read when the tree has none: it is
	// in the directory of the program, which is found by the path the process was run by.
	BesideScript = "fake.json"
)

// Script is what a fake process is told to do.
type Script struct {
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
	// EnvToStderr makes it print its environment, a line for each variable, to its stderr.
	EnvToStderr bool `json:"env_to_stderr"`
	// NotesToStderr makes it print the answers of the hooks it ran to its stderr, as a line
	// "hooks: " and the JSON that HooksPath holds, for a test that cannot read the tree it ran in.
	NotesToStderr bool `json:"notes_to_stderr"`

	// What a fake claude does with the hook of its settings: it runs the hook command
	// with each payload and notes the answers. {run_id} and {root} in a payload are the id
	// of the run its settings file is in and the tree it runs in.
	HookPayloads []json.RawMessage `json:"hook_payloads"`
	// Submits are the payloads it submits through Options.Submit, one after the other,
	// and notes whether each was accepted.
	Submits []json.RawMessage `json:"submits"`
	// Commands are shell commands it runs at the tree, one after the other, and notes
	// the exit code and the output of each: {run_id} and {root} in a command are as in a
	// hook payload. They are how a fake claude runs af <agent> submit.
	Commands []string `json:"commands"`
	// ShowFiles are files of the tree it prints to its stderr, one line "file: " and the JSON
	// {path, content, missing} for each ({run_id} and {root} as above), for a test that cannot
	// read the tree it ran in.
	ShowFiles []string `json:"show_files"`
	// OutputJSON is what it prints last.
	OutputJSON string `json:"output_json"`
}

// shownFile is a line of what ShowFiles prints.
type shownFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Missing bool   `json:"missing"`
}

// Answer is what the hook that a fake claude ran printed, and how it ended.
type Answer struct {
	Stdout string `json:"stdout"`
	Exit   int    `json:"exit"`
}

// Submit is what a submission of a fake claude came to.
type Submit struct {
	Accepted bool     `json:"accepted"`
	Problems []string `json:"problems"`
}

// CommandResult is what a command of a fake claude came to.
type CommandResult struct {
	Command string `json:"command"`
	Exit    int    `json:"exit"`
	Output  string `json:"output"`
}

// Record is a line of the record of a fake process.
type Record struct {
	Program    string   `json:"program"`
	Argv       []string `json:"argv"`
	Env        []string `json:"env"`
	Cwd        string   `json:"cwd"`
	StdinBytes int      `json:"stdin_bytes"`
	PID        int      `json:"pid"`
	// Settings is the content of the file that --settings names, when there is one.
	Settings string `json:"settings"`
}

// Options are what a package supplies that a fake cannot know.
type Options struct {
	// Submit makes a submission of the script: the id of the run and the payload.
	Submit func(runID string, payload []byte) Submit
}

// Name is the program the test binary is run as, or "" when it is run as itself.
func Name() string {
	switch name := filepath.Base(os.Args[0]); name {
	case AF, Claude:
		return name
	}
	return ""
}

// Run is the program: it returns the exit code of the process.
func Run(program string, args []string, opts Options) int {
	var script Script
	data, err := os.ReadFile(ScriptPath)
	if err != nil {
		data, err = os.ReadFile(filepath.Join(filepath.Dir(os.Args[0]), BesideScript))
	}
	if err == nil {
		_ = json.Unmarshal(data, &script)
	}
	stdinBytes := 0
	if len(args) != 1 || args[0] != "--version" {
		n, _ := io.Copy(io.Discard, os.Stdin)
		stdinBytes = int(n)
	}
	cwd, _ := os.Getwd()
	settingsPath := argAfter(args, "--settings")
	settings, _ := os.ReadFile(settingsPath)
	line, _ := json.Marshal(Record{Program: program, Argv: args, Env: os.Environ(), Cwd: cwd, StdinBytes: stdinBytes, PID: os.Getpid(), Settings: string(settings)})
	if f, err := os.OpenFile(RecordPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		_, _ = f.Write(append(line, '\n'))
		_ = f.Close()
	}

	if len(args) == 1 && args[0] == "--version" {
		switch program {
		case AF:
			fmt.Printf("{\"name\":\"af\",\"version\":%q,\"commit\":%q}\n", orDefault(script.AFVersion, "v1.0.0"), orDefault(script.AFCommit, "abc123"))
		default:
			fmt.Printf("%s (Claude Code)\n", orDefault(script.ClaudeVersion, "2.0.0"))
		}
		return script.VersionExit[program]
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
	runID := filepath.Base(filepath.Dir(settingsPath))
	if len(script.HookPayloads) > 0 {
		var answers []Answer
		for _, payload := range script.HookPayloads {
			payload = []byte(strings.NewReplacer("{run_id}", runID, "{root}", cwd).Replace(string(payload)))
			answers = append(answers, runHook(string(settings), payload))
		}
		writeJSON(HooksPath, answers)
		if script.NotesToStderr {
			data, _ := json.Marshal(answers)
			fmt.Fprintf(os.Stderr, "hooks: %s\n", data)
		}
	}
	if len(script.Submits) > 0 && opts.Submit != nil {
		var results []Submit
		for _, payload := range script.Submits {
			results = append(results, opts.Submit(runID, payload))
		}
		writeJSON(SubmitsPath, results)
	}
	if len(script.Commands) > 0 {
		var results []CommandResult
		for _, command := range script.Commands {
			command = strings.NewReplacer("{run_id}", runID, "{root}", cwd).Replace(command)
			cmd := exec.Command("sh", "-c", command)
			out, err := cmd.CombinedOutput()
			result := CommandResult{Command: command, Output: string(out)}
			if ee, ok := err.(*exec.ExitError); ok {
				result.Exit = ee.ExitCode()
			} else if err != nil {
				result.Exit, result.Output = -1, err.Error()
			}
			results = append(results, result)
		}
		writeJSON(CommandsPath, results)
	}
	for _, path := range script.ShowFiles {
		path = strings.NewReplacer("{run_id}", runID, "{root}", cwd).Replace(path)
		data, err := os.ReadFile(path)
		line, _ := json.Marshal(shownFile{Path: path, Content: string(data), Missing: err != nil})
		fmt.Fprintf(os.Stderr, "file: %s\n", line)
	}
	fmt.Print(script.Stdout)
	fmt.Print(strings.Repeat("o", script.StdoutBytes))
	fmt.Fprint(os.Stderr, strings.Repeat("e", script.StderrBytes)+script.Stderr)
	if script.EnvToStderr {
		for _, kv := range os.Environ() {
			fmt.Fprintln(os.Stderr, kv)
		}
	}
	fmt.Print(script.OutputJSON)
	return script.Exit
}

// argAfter is the argument that follows the flag.
func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// runHook runs the command of the PreToolUse hook of the settings with the payload on
// its standard input.
func runHook(settings string, payload []byte) Answer {
	var s struct {
		Hooks struct {
			PreToolUse []struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(settings), &s); err != nil || len(s.Hooks.PreToolUse) == 0 || len(s.Hooks.PreToolUse[0].Hooks) == 0 {
		return Answer{Stdout: "no hook in the settings", Exit: -1}
	}
	cmd := exec.CommandContext(context.Background(), "sh", "-c", s.Hooks.PreToolUse[0].Hooks[0].Command)
	cmd.Stdin = strings.NewReader(string(payload))
	out, err := cmd.Output()
	answer := Answer{Stdout: string(out)}
	if ee, ok := err.(*exec.ExitError); ok {
		answer.Exit = ee.ExitCode()
	} else if err != nil {
		answer.Exit, answer.Stdout = -1, err.Error()
	}
	return answer
}

func writeJSON(path string, v any) {
	if data, err := json.Marshal(v); err == nil {
		_ = os.WriteFile(path, data, 0o644)
	}
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// WriteScript places the script beside the programs that Install made in dir.
func WriteScript(t testing.TB, dir string, s Script) {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, BesideScript), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Install makes a directory with af and claude in it, each a link to this test binary, and
// returns it with the paths of the two. The test is skipped where the binary cannot be linked.
func Install(t testing.TB) (dir, af, claude string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{AF, Claude} {
		if err := os.Symlink(self, filepath.Join(dir, name)); err != nil {
			t.Skipf("cannot link the test binary as %s: %v", name, err)
		}
	}
	return dir, filepath.Join(dir, AF), filepath.Join(dir, Claude)
}
