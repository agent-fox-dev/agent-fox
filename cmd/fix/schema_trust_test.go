package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/schematest"
)

// anyNodeHasKey reports whether any object nested in v has key.
func anyNodeHasKey(v any, key string) bool {
	switch x := v.(type) {
	case map[string]any:
		if _, ok := x[key]; ok {
			return true
		}
		for _, c := range x {
			if anyNodeHasKey(c, key) {
				return true
			}
		}
	case []any:
		for _, c := range x {
			if anyNodeHasKey(c, key) {
				return true
			}
		}
	}
	return false
}

// child descends through nested JSON objects by key.
func child(t *testing.T, v any, path ...string) map[string]any {
	t.Helper()
	cur, _ := v.(map[string]any)
	for _, k := range path {
		next, ok := cur[k].(map[string]any)
		if !ok {
			t.Fatalf("no object at %q (path %v)", k, path)
		}
		cur = next
	}
	return cur
}

// TS-10-14 (integration): x-trust appears only inside the result document's
// own Result subtree, never in flags and never on the envelope's own
// top-level properties.
//
// Verifies: 10-REQ-3.3
func TestTS10_14_XTrustOnlyInsideResultSubtree(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(schematest.Live(t, "fix"), &doc); err != nil {
		t.Fatal(err)
	}
	envProps := child(t, doc, "result", "properties")
	res := child(t, envProps, "result", "properties")

	if got := child(t, res, "verification", "properties", "output")["x-trust"]; got != "external" {
		t.Errorf("verification.output x-trust = %v, want external", got)
	}
	if got := child(t, res, "branch")["x-trust"]; got != "fact" {
		t.Errorf("branch x-trust = %v, want fact", got)
	}
	for _, k := range []string{"tool", "version", "ok", "status", "input", "model", "usage", "error", "warnings"} {
		node, present := envProps[k]
		if !present {
			continue
		}
		if anyNodeHasKey(node, "x-trust") {
			t.Errorf("envelope property %q carries x-trust", k)
		}
	}
	if anyNodeHasKey(doc["flags"], "x-trust") {
		t.Errorf("the flags document carries x-trust")
	}
}

// TS-10-15 (integration): The four golden files carrying x-trust still pass
// TestSchemaGolden and still compile against the 2020-12 meta-schema.
//
// Verifies: 10-REQ-3.4
func TestTS10_15_GoldenFilesWithXTrustPassAndCompile(t *testing.T) {
	for _, tool := range schemaTools {
		t.Run(tool, func(t *testing.T) {
			golden, err := os.ReadFile(schematest.GoldenFile(schematest.Root(t), tool))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(golden), `"x-trust"`) {
				t.Errorf("%s golden file carries no x-trust", tool)
			}
			if !testing.Short() {
				cmd := exec.Command("go", "test", "-count=1", "-run", "^TestSchemaGolden$", "./cmd/"+tool)
				cmd.Dir = schematest.Root(t)
				for _, e := range os.Environ() {
					if strings.HasPrefix(e, "UPDATE_GOLDEN=") || strings.HasPrefix(e, schematest.GoldenFileEnv+"=") {
						continue
					}
					cmd.Env = append(cmd.Env, e)
				}
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Errorf("TestSchemaGolden for %s failed: %v\n%s", tool, err, out)
				}
			}
			if err := schematest.CompileDocuments(schematest.Live(t, tool)); err != nil {
				t.Errorf("%s: %v", tool, err)
			}
		})
	}
}
