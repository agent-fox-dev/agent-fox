// Package schematest is the test support behind each tool's golden-file test
// of its --schema output. It is only imported from _test.go files.
//
// The golden file of a tool is cmd/<tool>/testdata/schema.golden.json. A test
// fails when the built tool's live --schema output differs from it, showing
// the diff; UPDATE_GOLDEN=1 rewrites the file from the live output instead,
// so a change to the schema is always a deliberate, reviewable one.
package schematest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	// UpdateEnv, when set to a non-empty value, makes CheckGolden rewrite the
	// golden file from the live output instead of failing on a difference.
	UpdateEnv = "UPDATE_GOLDEN"
	// GoldenFileEnv, when set, names the golden file CheckGolden compares
	// against in place of the tool's checked-in one. It exists so a test can
	// point the golden test at a deliberately drifted copy.
	GoldenFileEnv = "SCHEMA_GOLDEN_FILE"
)

// Root is the module root: the nearest directory above the working directory
// that holds go.mod.
func Root(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find the module root containing go.mod")
		}
		dir = parent
	}
}

// GoldenFile is the path of tool's checked-in golden file under root.
func GoldenFile(root, tool string) string {
	return filepath.Join(root, "cmd", tool, "testdata", "schema.golden.json")
}

// Live builds cmd/<tool> and returns the bytes it writes to stdout when run
// with only --schema. The tool runs with no credentials and an empty state
// directory: --schema must need neither.
func Live(t *testing.T, tool string) []byte {
	t.Helper()
	root := Root(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, tool)
	build := exec.Command("go", "build", "-o", bin, "./cmd/"+tool)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%s: go build ./cmd/%s failed: %v\n%s", tool, tool, err, out)
	}
	cmd := exec.Command(bin, "--schema")
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + dir, "XDG_STATE_HOME=" + dir, "PATH=" + os.Getenv("PATH")}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s --schema failed: %v\nstdout: %s\nstderr: %s", tool, err, stdout.String(), stderr.String())
	}
	return stdout.Bytes()
}

// CheckGolden is the whole golden-file test of one tool: its live --schema
// output equals its golden file byte for byte (or the file is rewritten, with
// UPDATE_GOLDEN set), and the flags and result documents in that output
// compile against the JSON Schema 2020-12 meta-schema.
func CheckGolden(t *testing.T, tool string) {
	t.Helper()
	live := Live(t, tool)
	path := os.Getenv(GoldenFileEnv)
	if path == "" {
		path = GoldenFile(Root(t), tool)
	}

	if os.Getenv(UpdateEnv) != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		if err := os.WriteFile(path, live, 0o644); err != nil {
			t.Fatalf("%s: writing %s: %v", tool, path, err)
		}
		t.Logf("%s: wrote %s from the live --schema output", tool, path)
	} else {
		golden, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: cannot read the golden file %s: %v\nrun with %s=1 to create it", tool, path, err, UpdateEnv)
		}
		if !bytes.Equal(golden, live) {
			t.Errorf("%s: --schema output differs from the golden file %s\n"+
				"if the change is intended, run with %s=1 and review the new golden file\n"+
				"diff (-golden +live):\n%s",
				tool, path, UpdateEnv, cmp.Diff(lines(golden), lines(live)))
		}
	}

	if err := CompileDocuments(live); err != nil {
		t.Errorf("%s: %v", tool, err)
	}
}

func lines(b []byte) []string { return strings.Split(string(b), "\n") }

// CompileDocuments compiles the flags and the result documents of a --schema
// output against the JSON Schema 2020-12 meta-schema. The error names the
// document that did not compile.
func CompileDocuments(raw []byte) error {
	var doc struct {
		Flags  json.RawMessage `json:"flags"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("--schema output is not a JSON object: %w", err)
	}
	for _, d := range []struct {
		name string
		raw  json.RawMessage
	}{{"flags", doc.Flags}, {"result", doc.Result}} {
		if len(d.raw) == 0 {
			return fmt.Errorf("the %s document is missing", d.name)
		}
		v, err := jsonschema.UnmarshalJSON(bytes.NewReader(d.raw))
		if err != nil {
			return fmt.Errorf("the %s document is not valid JSON: %w", d.name, err)
		}
		if err := CompileSchema(v); err != nil {
			return fmt.Errorf("the %s document does not compile as a JSON Schema: %w", d.name, err)
		}
	}
	return nil
}

// CompileSchema compiles doc, a decoded JSON Schema document, which also
// checks it against the meta-schema its $schema names (2020-12 when absent).
func CompileSchema(doc any) error {
	const url = "urn:agentfox:schematest:document.json"
	c := jsonschema.NewCompiler()
	if err := c.AddResource(url, doc); err != nil {
		return err
	}
	_, err := c.Compile(url)
	return err
}
