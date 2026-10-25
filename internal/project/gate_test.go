package project

import (
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// TestDetect_AGateFileIsTheTable: it is used as it is, even with a Makefile
// beside it, with the source file.
func TestDetect_AGateFileIsTheTable(t *testing.T) {
	d := detect(t, map[string]string{
		"Makefile":             "check:\n\ttrue\n",
		".agent-fox/gate.json": `{"version":1,"checks":[{"name":"ci","argv":["./ci.sh"],"kind":"check","source":"ci"}]}`,
	})
	want := Table{Source: SourceFile, Checks: []Check{{Name: "ci", Argv: []string{"./ci.sh"}, Kind: KindCheck, Source: "ci"}}}
	if !reflect.DeepEqual(d.Gate, want) {
		t.Errorf("gate = %+v, want %+v", d.Gate, want)
	}
}

// TestDetect_FillsInWhatAGateFileLeavesOut: a check without a name is named
// by its command, without a kind is a check, without a source is the file's;
// availability is worked out, and what the file says of it is not read.
func TestDetect_FillsInWhatAGateFileLeavesOut(t *testing.T) {
	d := detect(t, map[string]string{
		".agent-fox/gate.json": `{"version":1,"checks":[{"argv":["sh","-c","true"]},{"name":"gone","argv":["no-such-program-anywhere"],"available":true},{"argv":["/bin/sh"],"kind":"lint"}]}`,
	})
	_, shErr := exec.LookPath("sh")
	_, absErr := os.Stat("/bin/sh")
	want := []Check{
		{Name: "sh -c true", Argv: []string{"sh", "-c", "true"}, Kind: KindCheck, Source: "gate.json", Available: shErr == nil},
		{Name: "gone", Argv: []string{"no-such-program-anywhere"}, Kind: KindCheck, Source: "gate.json"},
		{Name: "/bin/sh", Argv: []string{"/bin/sh"}, Kind: KindLint, Source: "gate.json", Available: absErr == nil},
	}
	if !reflect.DeepEqual(d.Gate.Checks, want) {
		t.Errorf("checks = %+v, want %+v", d.Gate.Checks, want)
	}
}

// TestDetect_RefusesAGateFileThatCannotBeUsed: the error wraps
// ErrGateConfig, names the file, and detection does not fall back.
func TestDetect_RefusesAGateFileThatCannotBeUsed(t *testing.T) {
	for name, content := range map[string]string{
		"not JSON":          `{"version":`,
		"another version":   `{"version":2,"checks":[]}`,
		"no version":        `{"checks":[]}`,
		"an empty argv":     `{"version":1,"checks":[{"name":"x","argv":[]}]}`,
		"an empty program":  `{"version":1,"checks":[{"name":"x","argv":[""]}]}`,
		"an unknown kind":   `{"version":1,"checks":[{"name":"x","argv":["a"],"kind":"other"}]}`,
		"a repeated name":   `{"version":1,"checks":[{"name":"x","argv":["a"]},{"name":"x","argv":["b"]}]}`,
		"an unknown field":  `{"version":1,"checks":[],"extra":true}`,
		"trailing document": `{"version":1,"checks":[]} {}`,
		"a directory":       "",
	} {
		t.Run(name, func(t *testing.T) {
			dir, list := fixture(t, map[string]string{"Makefile": "check:\n"})
			if name == "a directory" {
				if err := os.MkdirAll(GatePath(dir), 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				testutil.WriteFile(t, dir, ".agent-fox/gate.json", content)
			}

			_, err := Detect(dir, list)

			if !errors.Is(err, ErrGateConfig) || !strings.Contains(err.Error(), filepath.Join(dir, ".agent-fox", "gate.json")) {
				t.Errorf("Detect = %v, want an error wrapping ErrGateConfig that names the file", err)
			}
		})
	}

	// A .agent-fox that is a file holds no gate.json.
	if d := detect(t, map[string]string{"Makefile": "check:\n", ".agent-fox": ""}); d.Gate.Source != SourceDetected {
		t.Errorf("with .agent-fox a file: %+v", d.Gate)
	}
}

// TestDetectFresh_IgnoresTheGateFile: the table af lang detect --write stores
// comes from the repository's files whatever gate.json says, even when it
// cannot be read.
func TestDetectFresh_IgnoresTheGateFile(t *testing.T) {
	for name, gate := range map[string]string{
		"none":      "",
		"another":   `{"version":1,"checks":[{"name":"ci","argv":["./ci.sh"]}]}`,
		"not JSON":  `{not json`,
		"a version": `{"version":9,"checks":[]}`,
	} {
		files := map[string]string{"Makefile": "check:\n\ttrue\n"}
		if gate != "" {
			files = maps.Clone(files)
			files[".agent-fox/gate.json"] = gate
		}
		dir, list := fixture(t, files)

		if d := DetectFresh(dir, list); d.Gate.Source != SourceDetected || !reflect.DeepEqual(argvs(d), [][]string{{"make", "check"}}) {
			t.Errorf("%s: gate = %+v, want the detected make check", name, d.Gate)
		}
	}
}

// TestWriteGateFile_IsReadBackAsTheSameTable: what is written, through a
// rename and with the .gitignore of .agent-fox, is the bytes MarshalGateFile
// gives, holds nothing of the machine, and Detect reads it back as the same
// checks with the source file.
func TestWriteGateFile_IsReadBackAsTheSameTable(t *testing.T) {
	dir, list := fixture(t, map[string]string{"go.mod": "module x\n"})
	detected, err := Detect(dir, list)
	if err != nil {
		t.Fatal(err)
	}

	if err := WriteGateFile(dir, detected.Gate.Checks); err != nil {
		t.Fatal(err)
	}

	doc, err := MarshalGateFile(detected.Gate.Checks)
	if err != nil {
		t.Fatal(err)
	}
	written := testutil.ReadFile(t, GatePath(dir))
	if written != string(doc) || strings.Contains(written, "available") || !strings.Contains(written, `"version": 1`) {
		t.Errorf("gate.json = %s, want %s, with the version and without availability", written, doc)
	}
	again, err := Detect(dir, list)
	if err != nil || again.Gate.Source != SourceFile || !reflect.DeepEqual(again.Gate.Checks, detected.Gate.Checks) {
		t.Errorf("read back %+v, %v; want %+v with the source file", again.Gate, err, detected.Gate.Checks)
	}
	if got := testutil.Files(t, filepath.Join(dir, ".agent-fox")); len(got) != 2 || got[".gitignore"] != "*\n" {
		t.Errorf(".agent-fox holds %v, want gate.json and a .gitignore of *", got)
	}

	blocked, _ := fixture(t, map[string]string{".agent-fox": "a file"})
	if err := WriteGateFile(blocked, detected.Gate.Checks); err == nil {
		t.Error("WriteGateFile where .agent-fox is a file: no error")
	}
}
