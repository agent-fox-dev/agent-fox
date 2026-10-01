package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/schematest"
)

var schemaTools = []string{"spec", "issue", "fix", "impl"}

// TS-09-35 (integration): Each tool ships a checked-in golden file holding
// its --schema output's exact bytes.
func TestTS09_35_GoldenFilesHoldLiveOutput(t *testing.T) {
	root := schematest.Root(t)
	for _, tool := range schemaTools {
		t.Run(tool, func(t *testing.T) {
			path := filepath.Join(root, "cmd", tool, "testdata", "schema.golden.json")
			golden, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s: no golden file: %v", tool, err)
			}
			if live := schematest.Live(t, tool); !bytes.Equal(live, golden) {
				t.Errorf("%s --schema differs from %s", tool, path)
			}
		})
	}
}

// goldenTestRun runs fix's TestSchemaGolden in a subprocess against the golden
// file at path, with extra environment entries, and returns its exit code and
// combined output. UPDATE_GOLDEN is removed from the inherited environment so
// an outer update run cannot leak into it.
func goldenTestRun(t *testing.T, path string, extraEnv ...string) (int, string) {
	t.Helper()
	cmd := exec.Command("go", "test", "-count=1", "-run", "^TestSchemaGolden$", "./cmd/fix")
	cmd.Dir = schematest.Root(t)
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "UPDATE_GOLDEN=") || strings.HasPrefix(e, schematest.GoldenFileEnv+"=") {
			continue
		}
		cmd.Env = append(cmd.Env, e)
	}
	cmd.Env = append(cmd.Env, schematest.GoldenFileEnv+"="+path)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	}
	t.Fatalf("go test: %v\n%s", err, out)
	return -1, ""
}

// mutatedGolden writes a copy of fix's golden file that differs from the live
// output, and returns its path.
func mutatedGolden(t *testing.T) string {
	t.Helper()
	root := schematest.Root(t)
	b, err := os.ReadFile(schematest.GoldenFile(root, "fix"))
	if err != nil {
		t.Fatal(err)
	}
	b = bytes.Replace(b, []byte(`"type": "object"`), []byte(`"type": "MUTATED"`), 1)
	path := filepath.Join(t.TempDir(), "schema.golden.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TS-09-36 (integration): A drifted golden file fails the golden-file test
// with a byte-level diff naming the tool.
func TestTS09_36_DriftedGoldenFailsWithDiff(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test in a subprocess")
	}
	path := mutatedGolden(t)
	before, _ := os.ReadFile(path)
	code, out := goldenTestRun(t, path)
	if code == 0 {
		t.Fatalf("the golden test passed against a drifted file:\n%s", out)
	}
	if !strings.Contains(out, "fix") {
		t.Errorf("failure does not name the tool:\n%s", out)
	}
	if !strings.Contains(out, "MUTATED") || !strings.Contains(out, "diff") {
		t.Errorf("failure does not show a diff of the live and golden bytes:\n%s", out)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Errorf("the golden file was rewritten without the update path")
	}
}

// TS-09-37 (integration): The golden-file update path regenerates the golden
// file instead of failing.
func TestTS09_37_UpdatePathRegeneratesGolden(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test in a subprocess")
	}
	path := mutatedGolden(t)
	code, out := goldenTestRun(t, path, "UPDATE_GOLDEN=1")
	if code != 0 {
		t.Fatalf("the golden test failed with UPDATE_GOLDEN=1 (exit %d):\n%s", code, out)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if live := schematest.Live(t, "fix"); !bytes.Equal(got, live) {
		t.Errorf("the golden file was not regenerated from the live --schema output")
	}
}

// TS-09-38 (integration): Both flags and result of every tool's --schema
// output compile against the JSON Schema 2020-12 meta-schema.
func TestTS09_38_FlagsAndResultCompileAgainstMetaSchema(t *testing.T) {
	for _, tool := range schemaTools {
		t.Run(tool, func(t *testing.T) {
			if err := schematest.CompileDocuments(schematest.Live(t, tool)); err != nil {
				t.Errorf("%s: %v", tool, err)
			}
		})
	}
}

// TS-09-38 (unit): the check is not a no-op: documents that are not valid
// JSON Schema fail, and the failure names which document it was.
func TestTS09_38_InvalidSchemaFailsCompilation(t *testing.T) {
	bad := map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "bogus"}
	if err := schematest.CompileSchema(bad); err == nil {
		t.Error("a schema with an unknown type compiled")
	}
	if err := schematest.CompileSchema(map[string]any{"type": "object", "properties": map[string]any{}}); err != nil {
		t.Errorf("a valid schema did not compile: %v", err)
	}
	doc := []byte(`{"flags": {"type": "object"}, "result": {"type": 5}}`)
	err := schematest.CompileDocuments(doc)
	if err == nil || !strings.Contains(err.Error(), "result") {
		t.Errorf("CompileDocuments error = %v, want one naming the result document", err)
	}
}
