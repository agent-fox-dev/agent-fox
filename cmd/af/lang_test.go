package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// repoWith makes a directory holding files (path to content).
func repoWith(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// onPath puts programs, as empty executable scripts, on a PATH of their own
// for the rest of the test.
func onPath(t *testing.T, programs ...string) {
	t.Helper()
	bin := t.TempDir()
	for _, p := range programs {
		if err := os.WriteFile(filepath.Join(bin, p), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
}

// field follows keys through nested objects.
func field(doc map[string]any, keys ...string) any {
	var v any = doc
	for _, k := range keys {
		obj, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = obj[k]
	}
	return v
}

// codes lists the codes of the warnings of an envelope.
func codes(env map[string]any) []string {
	var out []string
	warnings, _ := env["warnings"].([]any)
	for _, w := range warnings {
		out = append(out, field(w.(map[string]any), "code").(string))
	}
	return out
}

// checksOf lists the argv of the checks of a lang detect result.
func checksOf(env map[string]any) [][]string {
	var out [][]string
	list, _ := field(env, "result", "gate", "checks").([]any)
	for _, c := range list {
		var argv []string
		for _, a := range field(c.(map[string]any), "argv").([]any) {
			argv = append(argv, a.(string))
		}
		out = append(out, argv)
	}
	return out
}

// TestLangDetectBrokenGateFile_TS_06_15 verifies TS-06-15:
// A gate.json that does not parse or names another version fails with
// category config naming the file.
// Verifies: 06-REQ-2.6
func TestLangDetectBrokenGateFile_TS_06_15(t *testing.T) {
	cases := map[string]string{
		"A, not JSON":      `{not json`,
		"B, other version": `{"version":2,"checks":[]}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := repoWith(t, map[string]string{"Makefile": "check:\n\ttrue\n", ".agent-fox/gate.json": content})

			code, stdout, stderr := run(t, "lang", "detect", "--dir", dir)

			env := oneDocument(t, stdout)
			message, _ := field(env, "error", "message").(string)
			if code != 1 || env["status"] != "failed" || field(env, "error", "category") != "config" || stderr != "" {
				t.Errorf("exit code %d, status %v, category %v, stderr %q; want 1, failed, config, nothing", code, env["status"], field(env, "error", "category"), stderr)
			}
			if want := filepath.Join(dir, ".agent-fox", "gate.json"); !strings.Contains(message, want) {
				t.Errorf("error.message = %q, want it to contain %s", message, want)
			}
			if env["result"] != nil {
				t.Errorf("result = %v: the Makefile was used as a fallback", env["result"])
			}
		})
	}
}

// TestLangDetectUnavailableCheck_TS_06_17 verifies TS-06-17:
// A selected check whose program is missing stays in the table as
// unavailable with a check_unavailable warning.
// Verifies: 06-REQ-2.7
func TestLangDetectUnavailableCheck_TS_06_17(t *testing.T) {
	dir := repoWith(t, map[string]string{"Cargo.toml": "[package]\nname = \"x\"\n"})
	onPath(t) // nothing on PATH

	code, stdout, _ := run(t, "lang", "detect", "--dir", dir)

	env := oneDocument(t, stdout)
	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v; want 0 and done", code, env["status"])
	}
	checks, _ := field(env, "result", "gate", "checks").([]any)
	if len(checks) != 1 {
		t.Fatalf("checks = %v, want cargo test", checks)
	}
	check := checks[0].(map[string]any)
	if !reflect.DeepEqual(check["argv"], []any{"cargo", "test"}) || check["available"] != false {
		t.Errorf("check = %v, want cargo test with available false", check)
	}
	var named bool
	for _, w := range env["warnings"].([]any) {
		w := w.(map[string]any)
		if w["code"] == "check_unavailable" && strings.Contains(w["message"].(string), "cargo test") {
			named = true
		}
	}
	if !named {
		t.Errorf("warnings = %v, want check_unavailable naming cargo test", env["warnings"])
	}

	// With the program on PATH the same check is available and nothing is
	// said.
	onPath(t, "cargo")
	_, stdout, _ = run(t, "lang", "detect", "--dir", dir)
	again := oneDocument(t, stdout)
	if len(codes(again)) != 0 || !reflect.DeepEqual(checksOf(again), [][]string{{"cargo", "test"}}) {
		t.Errorf("with cargo on PATH: warnings %v, checks %v; want none and cargo test", codes(again), checksOf(again))
	}
}

// TestLangDetectWrite_TS_06_18 verifies TS-06-18:
// --write stores the freshly detected table atomically, ignoring any
// existing gate.json, and a later detect reads it back as file.
// Verifies: 06-REQ-2.8
func TestLangDetectWrite_TS_06_18(t *testing.T) {
	dir := repoWith(t, map[string]string{
		"Makefile":             "check:\n\ttrue\n",
		".agent-fox/gate.json": `{"version":1,"checks":[{"name":"other","argv":["./other.sh"],"kind":"test"}]}`,
	})

	code, stdout, _ := run(t, "lang", "detect", "--dir", dir, "--write")

	first := oneDocument(t, stdout)
	if code != 0 || first["status"] != "done" {
		t.Fatalf("exit code %d, status %v; want 0 and done", code, first["status"])
	}
	if field(first, "result", "gate", "source") != "detected" || !reflect.DeepEqual(checksOf(first), [][]string{{"make", "check"}}) {
		t.Errorf("the --write run reports source %v, checks %v; want the detected make check", field(first, "result", "gate", "source"), checksOf(first))
	}

	path := filepath.Join(dir, ".agent-fox", "gate.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var written struct {
		Version int `json:"version"`
		Checks  []struct {
			Name string   `json:"name"`
			Argv []string `json:"argv"`
			Kind string   `json:"kind"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(data, &written); err != nil {
		t.Fatalf("gate.json is not JSON: %v\n%s", err, data)
	}
	if written.Version != 1 || len(written.Checks) != 1 || !reflect.DeepEqual(written.Checks[0].Argv, []string{"make", "check"}) || written.Checks[0].Kind != "check" {
		t.Errorf("gate.json = %s, want version 1 and the one check make check", data)
	}
	entries, err := os.ReadDir(filepath.Join(dir, ".agent-fox"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "tmp") {
			t.Errorf("a temporary file was left in .agent-fox: %s", e.Name())
		}
	}
	var artifact bool
	for _, a := range first["artifacts"].([]any) {
		if a.(map[string]any)["path"] == path {
			artifact = true
		}
	}
	if !artifact {
		t.Errorf("artifacts = %v, want gate.json (%s) among them", first["artifacts"], path)
	}

	_, stdout, _ = run(t, "lang", "detect", "--dir", dir)
	second := oneDocument(t, stdout)
	if field(second, "result", "gate", "source") != "file" || !reflect.DeepEqual(checksOf(second), checksOf(first)) {
		t.Errorf("the second run reports source %v, checks %v; want file and %v", field(second, "result", "gate", "source"), checksOf(second), checksOf(first))
	}
}

// TestLangDetectWriteRepairsABrokenGateFile guards 06-REQ-2.8: "ignoring any
// existing gate.json" includes one that does not parse, which --write
// replaces.
func TestLangDetectWriteRepairsABrokenGateFile(t *testing.T) {
	dir := repoWith(t, map[string]string{"Makefile": "check:\n\ttrue\n", ".agent-fox/gate.json": `{not json`})

	code, stdout, _ := run(t, "lang", "detect", "--dir", dir, "--write")

	if env := oneDocument(t, stdout); code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, envelope %v; want 0 and done", code, env)
	}
	_, stdout, _ = run(t, "lang", "detect", "--dir", dir)
	if got := checksOf(oneDocument(t, stdout)); !reflect.DeepEqual(got, [][]string{{"make", "check"}}) {
		t.Errorf("after the repair the checks are %v, want make check", got)
	}
}

// TestLangDetectReportsTheRepository guards 06-REQ-2.1 on the command: the
// result is the detection, and the command writes nothing without --write.
func TestLangDetectReportsTheRepository(t *testing.T) {
	dir := repoWith(t, map[string]string{"go.mod": "module x\n", "a.go": "package a\n", "b.go": "package a\n", "x.py": ""})
	onPath(t, "go")

	code, stdout, _ := run(t, "lang", "detect", "--dir", dir)

	env := oneDocument(t, stdout)
	if code != 0 || env["status"] != "done" || env["tool"] != "lang detect" || env["summary"] == "" {
		t.Fatalf("exit code %d, envelope %v; want 0, done, tool lang detect and a summary", code, env)
	}
	langs, _ := field(env, "result", "languages").([]any)
	if len(langs) != 2 || field(langs[0].(map[string]any), "name") != "go" || field(langs[0].(map[string]any), "files") != float64(2) {
		t.Errorf("languages = %v, want go (2 files) then python", langs)
	}
	if got := checksOf(env); !reflect.DeepEqual(got, [][]string{{"go", "vet", "./..."}, {"go", "test", "./..."}}) {
		t.Errorf("checks = %v", got)
	}
	for _, key := range []string{"suite", "programs"} {
		if list, _ := field(env, "result", key).([]any); len(list) == 0 {
			t.Errorf("result.%s = %v, want it filled", key, field(env, "result", key))
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".agent-fox")); err == nil {
		t.Error("lang detect without --write created .agent-fox")
	}

	empty := repoWith(t, nil)
	_, stdout, _ = run(t, "lang", "detect", "--dir", empty)
	env = oneDocument(t, stdout)
	if env["status"] != "done" || len(field(env, "result", "languages").([]any)) != 0 || len(checksOf(env)) != 0 {
		t.Errorf("an empty directory: %v, want done with no languages and no checks", env)
	}
}

// TestLangDetectNeedsADirectory guards the io category: a --dir that is
// not a directory cannot be read.
func TestLangDetectNeedsADirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nowhere")

	code, stdout, _ := run(t, "lang", "detect", "--dir", missing)

	env := oneDocument(t, stdout)
	if code != 1 || env["status"] != "failed" || field(env, "error", "category") != "io" {
		t.Errorf("exit code %d, status %v, category %v; want 1, failed, io", code, env["status"], field(env, "error", "category"))
	}
}
