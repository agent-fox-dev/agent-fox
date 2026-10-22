package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// binaries is the directory that make build wrote af and agent-fox into, once for
// all the tests of this package.
var binaries struct {
	once sync.Once
	dir  string
	err  error
}

// TestMain removes the binaries that the tests built.
func TestMain(m *testing.M) {
	code := m.Run()
	if binaries.dir != "" {
		os.RemoveAll(binaries.dir)
	}
	os.Exit(code)
}

// buildBinaries runs make build with the version 9.9.9-test into a directory of its
// own, and returns the directory. It is skipped when make is not installed.
func buildBinaries(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}
	binaries.once.Do(func() {
		binaries.dir, binaries.err = os.MkdirTemp("", "agent-fox-bin-")
		if binaries.err != nil {
			return
		}
		build := exec.Command("make", "build", "VERSION=9.9.9-test", "BIN_DIR="+binaries.dir)
		build.Dir = filepath.Join("..", "..")
		if out, err := build.CombinedOutput(); err != nil {
			binaries.err = &buildError{err, string(out)}
		}
	})
	if binaries.err != nil {
		t.Fatalf("make build: %v", binaries.err)
	}
	return binaries.dir
}

type buildError struct {
	err error
	out string
}

func (e *buildError) Error() string { return e.err.Error() + "\n" + e.out }

// agentFox is the path of the built agent-fox.
func agentFox(t *testing.T) string { return filepath.Join(buildBinaries(t), "agent-fox") }

// result is how a run of a binary ended.
type result struct {
	Code           int
	Stdout, Stderr string
}

// execute runs the binary in dir, with exactly the environment given, and waits
// for it to end.
func execute(t *testing.T, binary, dir string, env []string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	cmd := exec.Command(binary, args...)
	cmd.Dir, cmd.Env = dir, env
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("%s %v: %v", binary, args, err)
	}
	return result{code, out.String(), errb.String()}
}

// bareEnv is an environment with an empty home directory, and no variable that
// names a configuration or a state directory.
func bareEnv(t *testing.T) []string {
	t.Helper()
	return []string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH")}
}

// jsonObject decodes one JSON object, and fails the test if s is more than that.
func jsonObject(t *testing.T, s string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, s)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		t.Fatalf("more than one JSON value (%v)\n%s", err, s)
	}
	return doc
}

// versionFields are the five fields of the version document.
var versionFields = []string{"build_time", "commit", "go_version", "name", "version"}

// TestMakeBuildWritesAgentFoxBesideAf_TS_11_1 verifies TS-11-1:
// make build writes agent-fox beside af with the same ldflags.
// Verifies: 11-REQ-1.1
func TestMakeBuildWritesAgentFoxBesideAf_TS_11_1(t *testing.T) {
	dir := buildBinaries(t)
	for _, name := range []string{"af", "agent-fox"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("make build did not write %s: %v", name, err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("%s is not executable: %v", name, info.Mode())
		}
	}
	env := bareEnv(t)
	service := jsonObject(t, execute(t, filepath.Join(dir, "agent-fox"), t.TempDir(), env, "--version").Stdout)
	cli := jsonObject(t, execute(t, filepath.Join(dir, "af"), t.TempDir(), env, "--version").Stdout)
	if service["version"] != "9.9.9-test" {
		t.Errorf("agent-fox --version reports the version %v, want 9.9.9-test", service["version"])
	}
	for _, field := range []string{"version", "commit", "build_time"} {
		if service[field] != cli[field] || service[field] == "" {
			t.Errorf("%s: agent-fox has %q and af has %q, want the same, from the same ldflags", field, service[field], cli[field])
		}
	}
	if service["name"] != "agent-fox" || cli["name"] != "af" {
		t.Errorf("names: %v and %v, want agent-fox and af", service["name"], cli["name"])
	}

	// The build target is what the Makefile says, in the default location too.
	plan := exec.Command("make", "-n", "build", "VERSION=9.9.9-test")
	plan.Dir = filepath.Join("..", "..")
	planned, err := plan.CombinedOutput()
	if err != nil || !strings.Contains(string(planned), "-o bin/agent-fox ./cmd/agent-fox") || !strings.Contains(string(planned), "-o bin/af ./cmd/af") {
		t.Errorf("make -n build: %v\n%s", err, planned)
	}
}

// TestVersionAndHelpReadNoConfiguration_TS_11_2 verifies TS-11-2:
// --version prints the five-field document and --help prints usage, both exit 0
// without reading configuration.
// Verifies: 11-REQ-1.2
func TestVersionAndHelpReadNoConfiguration_TS_11_2(t *testing.T) {
	bin := agentFox(t)
	env, empty := bareEnv(t), t.TempDir()

	r := execute(t, bin, empty, env, "--version")
	doc := jsonObject(t, r.Stdout)
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if r.Code != 0 || r.Stderr != "" || !slices.Equal(keys, versionFields) || doc["name"] != "agent-fox" {
		t.Errorf("--version: exit %d, stderr %q, fields %v, name %v; want 0, nothing, %v, agent-fox", r.Code, r.Stderr, keys, doc["name"], versionFields)
	}
	if doc["go_version"] == "" || doc["version"] != "9.9.9-test" {
		t.Errorf("--version = %v, want the go version and 9.9.9-test", doc)
	}

	r = execute(t, bin, empty, env, "--help")
	for _, flag := range []string{"--config", "--version", "--help"} {
		if r.Code != 0 || !strings.Contains(r.Stdout+r.Stderr, flag) {
			t.Errorf("--help: exit %d, output %q; want 0 and a usage that names %s", r.Code, r.Stdout+r.Stderr, flag)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(r.Stdout+r.Stderr), "{") {
		t.Errorf("--help printed a JSON record: %q", r.Stdout+r.Stderr)
	}

	// Neither asks for a configuration, so a bad one is not looked at either.
	broken := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(broken, []byte("this is not toml ="), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--config", broken, "--version"}, {"--version", "--config", broken}, {"--config", broken, "--help"}} {
		if r := execute(t, bin, empty, env, args...); r.Code != 0 {
			t.Errorf("%v: exit %d, stderr %q; want 0 without reading the configuration", args, r.Code, r.Stderr)
		}
	}
}

// TestAnUnknownFlagOrAPositionalArgumentIsAPlainUsageError_TS_11_3 verifies TS-11-3:
// An unknown flag or a positional argument prints plain usage on stderr and exits 2.
// Verifies: 11-REQ-1.3
func TestAnUnknownFlagOrAPositionalArgumentIsAPlainUsageError_TS_11_3(t *testing.T) {
	bin, env, empty := agentFox(t), bareEnv(t), t.TempDir()
	for _, args := range [][]string{{"--bogus"}, {"extra"}, {"--config", "c.toml", "extra"}, {"--version", "extra"}, {"--config"}, {"-x"}, {"--version=maybe"}} {
		r := execute(t, bin, empty, env, args...)
		if r.Code != 2 {
			t.Errorf("%v: exit %d, want 2", args, r.Code)
		}
		if !strings.Contains(r.Stderr, "Usage") || strings.HasPrefix(strings.TrimSpace(r.Stderr), "{") {
			t.Errorf("%v: stderr %q, want plain-text usage and not a JSON record", args, r.Stderr)
		}
		if strings.Contains(r.Stderr, "listening") || r.Stdout != "" {
			t.Errorf("%v: stdout %q, stderr %q; want nothing started and nothing on stdout", args, r.Stdout, r.Stderr)
		}
	}
}

// TestRunParsesTheFlagsWithoutABuild checks the same paths in process: the exit
// codes and the streams of --version, --help and the usage errors.
// Verifies: 11-REQ-1.2, 11-REQ-1.3
func TestRunParsesTheFlagsWithoutABuild(t *testing.T) {
	version, commit, buildTime = "1.2.3", "abc123", "2026-10-22T00:00:00Z"
	t.Cleanup(func() { version, commit, buildTime = "", "", "" })

	var out, errb bytes.Buffer
	if code := run([]string{"--version"}, &out, &errb); code != 0 || errb.Len() != 0 {
		t.Fatalf("--version: exit %d, stderr %q", code, errb.String())
	}
	doc := jsonObject(t, out.String())
	if doc["name"] != "agent-fox" || doc["version"] != "1.2.3" || doc["commit"] != "abc123" || doc["build_time"] != "2026-10-22T00:00:00Z" {
		t.Errorf("--version = %v, want the identity of the build", doc)
	}

	out.Reset()
	errb.Reset()
	if code := run([]string{"-help"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "--config") {
		t.Errorf("-help: exit %d, stdout %q", code, out.String())
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"--config=x", "y"}, &out, &errb); code != 2 || out.Len() != 0 || !strings.Contains(errb.String(), `"y"`) || !strings.Contains(errb.String(), "Usage") {
		t.Errorf("a positional argument: exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
}
