package afcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// emptyPath puts the programs, as empty scripts, on a PATH of their own for the rest of the test.
func emptyPath(t *testing.T, programs ...string) {
	t.Helper()
	bin := t.TempDir()
	for _, p := range programs {
		if err := os.WriteFile(filepath.Join(bin, p), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
}

func TestLangDetect_SummarisesTheLanguagesAndTheGateAndWritesNothing(t *testing.T) {
	emptyPath(t, "go")
	for name, tc := range map[string]struct {
		files   map[string]string
		summary string
	}{
		"a repository": {map[string]string{"go.mod": "module x\n", "a.go": "package a\n", "b.go": "package a\n", "x.py": ""},
			"go (2), python (1); gate (detected): go vet ./..., go test ./..."},
		"an empty directory": {map[string]string{}, "no languages; gate (detected): no checks"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for path, content := range tc.files {
				testutil.WriteFile(t, dir, path, content)
			}

			o := measure(t, "lang", "detect", "--dir", dir)

			if o.code != 0 || o.Status != "done" || o.Summary != tc.summary || len(o.Warnings) != 0 {
				t.Errorf("%v, summary %q; want done with the summary %q and no warning", o, o.Summary, tc.summary)
			}
			if _, err := os.Stat(filepath.Join(dir, ".agent-fox")); err == nil {
				t.Error("lang detect without --write made .agent-fox")
			}
		})
	}
}

func TestLangDetect_WarnsOfEachCheckThatCannotRunHere(t *testing.T) {
	emptyPath(t)
	for name, tc := range map[string]struct {
		files map[string]string
		why   string
	}{
		"a program that is not on PATH": {map[string]string{"Cargo.toml": "[package]\nname = \"x\"\n"}, "cargo is not on PATH"},
		"a script that is not there": {map[string]string{".agent-fox/gate.json": `{"version":1,"checks":[{"name":"own","argv":["./check.sh"]}]}`},
			"./check.sh is not an executable file"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for path, content := range tc.files {
				testutil.WriteFile(t, dir, path, content)
			}

			o := measure(t, "lang", "detect", "--dir", dir)

			if o.code != 0 || len(o.Warnings) != 1 || o.Warnings[0].Code != "check_unavailable" || !strings.Contains(o.Warnings[0].Message, tc.why) {
				t.Errorf("%v; want done with one check_unavailable that says %q", o, tc.why)
			}
		})
	}
}

func TestLangDetect_WriteStoresTheDetectedTableOverAnyGateFile(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "Makefile", "check:\n\ttrue\n")
	testutil.WriteFile(t, dir, ".agent-fox/gate.json", "{not json")

	o := measure(t, "lang", "detect", "--dir", dir, "--write")

	gateFile := filepath.Join(dir, ".agent-fox", "gate.json")
	if o.code != 0 || o.artifacts("gate_file") != 1 || o.Artifacts[0].Path != gateFile {
		t.Fatalf("%v, artifacts %v; want done with gate.json listed", o, o.Artifacts)
	}
	again := measure(t, "lang", "detect", "--dir", dir)
	if again.Summary != "no languages; gate (file): make check" {
		t.Errorf("the next detection says %q, want the stored table read back", again.Summary)
	}
}

func TestLangDetect_FailsWithTheCategoryOfWhatIsWrong(t *testing.T) {
	broken := t.TempDir()
	testutil.WriteFile(t, broken, ".agent-fox/gate.json", `{"version":2,"checks":[]}`)
	unwritable := t.TempDir()
	testutil.WriteFile(t, unwritable, "Makefile", "check:\n\ttrue\n")
	testutil.WriteFile(t, unwritable, ".agent-fox", "a file, not a directory")
	for name, tc := range map[string]struct {
		args     []string
		category string
		mention  string
	}{
		"a gate.json that cannot be used":    {[]string{"--dir", broken}, "config", filepath.Join(broken, ".agent-fox", "gate.json")},
		"a directory that is not there":      {[]string{"--dir", filepath.Join(t.TempDir(), "nowhere")}, "io", "nowhere"},
		"a gate.json that cannot be written": {[]string{"--dir", unwritable, "--write"}, "io", "cannot write"},
	} {
		t.Run(name, func(t *testing.T) {
			o := measure(t, append([]string{"lang", "detect"}, tc.args...)...)

			if o.code != 1 || o.category() != tc.category || !strings.Contains(o.message(), tc.mention) || o.Result != nil {
				t.Errorf("%v; want failed/%s mentioning %q, with no result", o, tc.category, tc.mention)
			}
		})
	}
}
