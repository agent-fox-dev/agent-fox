package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildBinary builds af with make build, into a directory of its own, with
// the version 9.9.9-test, and returns its path.
func buildBinary(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}
	dir := t.TempDir()
	build := exec.Command("make", "build", "VERSION=9.9.9-test", "BIN_DIR="+dir)
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("make build: %v\n%s", err, out)
	}
	return filepath.Join(dir, "af")
}

// execute runs the binary in dir and returns its exit code and output.
func execute(t *testing.T, binary, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("%s %v: %v", binary, args, err)
	}
	return code, out.String(), errb.String()
}

// oneDocument fails the test unless s is exactly one JSON document.
func oneDocument(t *testing.T, s string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("not a JSON document: %v\n%s", err, s)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		t.Fatalf("more than one JSON document (err = %v)\n%s", err, s)
	}
	return doc
}

// TestSmokeVersionAndSchemaFromTheBuiltBinary_TS_05_74 verifies TS-05-74:
// A tool fetches the version and the schema from the real built binary.
// Verifies: 05-PATH-1, 05-REQ-1.6, 05-REQ-10.1, 05-REQ-10.3
func TestSmokeVersionAndSchemaFromTheBuiltBinary_TS_05_74(t *testing.T) {
	binary := buildBinary(t)
	work := t.TempDir()

	code, stdout, stderr := execute(t, binary, work, "--version")
	version := oneDocument(t, stdout)
	if code != 0 || stderr != "" || version["name"] != "af" || version["version"] != "9.9.9-test" {
		t.Errorf("--version: exit code %d, stderr %q, document %v; want 0, nothing, af 9.9.9-test", code, stderr, version)
	}

	code, stdout, stderr = execute(t, binary, work, "--schema")
	oneDocument(t, stdout)
	golden, err := os.ReadFile(filepath.Join("testdata", "schema.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 || stderr != "" || stdout != string(golden) {
		t.Errorf("--schema: exit code %d, stderr %q, %d bytes; want 0, nothing and the golden file (%d bytes)", code, stderr, len(stdout), len(golden))
	}

	if _, err := os.Stat(filepath.Join(work, ".agent-fox")); err == nil {
		t.Error("the binary created a .agent-fox directory")
	}
}

// TestSmokeUnknownCommand_TS_05_77 verifies TS-05-77:
// An unknown command yields a usage envelope with nothing on stderr, from
// the shell wired like main and from the built binary.
// Verifies: 05-PATH-4, 05-REQ-1.2
func TestSmokeUnknownCommand_TS_05_77(t *testing.T) {
	check := func(t *testing.T, code int, stdout, stderr string) {
		t.Helper()
		env := oneDocument(t, stdout)
		errorObject, _ := env["error"].(map[string]any)
		message, _ := errorObject["message"].(string)
		if code != 2 || env["tool"] != "af" || env["status"] != "usage" || stderr != "" {
			t.Errorf("exit code %d, tool %v, status %v, stderr %q; want 2, af, usage, nothing", code, env["tool"], env["status"], stderr)
		}
		if !strings.Contains(message, `"nosuch"`) || !strings.Contains(message, "the commands are:") {
			t.Errorf("error.message = %q, want it to name the command and list the commands", message)
		}
	}

	t.Run("through Main", func(t *testing.T) {
		code, stdout, stderr := run(t, "nosuch")
		check(t, code, stdout, stderr)
	})
	t.Run("through the binary", func(t *testing.T) {
		code, stdout, stderr := execute(t, buildBinary(t), t.TempDir(), "nosuch")
		check(t, code, stdout, stderr)
	})
}
