package agentfox

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/schematest"
)

// TS-17-35 (unit): Each tool's live --schema output equals its schema golden
// byte for byte, and neither names a find_references or Symbols-bound flag,
// field or environment variable.
//
// Verifies: 17-REQ-2.7
func TestTS17_35_SchemaGoldenUnchanged(t *testing.T) {
	root := schematest.Root(t)
	for _, tool := range []string{"spec", "triage", "fix", "impl"} {
		t.Run(tool, func(t *testing.T) {
			live := schematest.Live(t, tool)
			goldenPath := schematest.GoldenFile(root, tool)
			golden, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("cannot read golden file %s: %v", goldenPath, err)
			}
			if !bytes.Equal(live, golden) {
				t.Errorf("--schema output differs from the golden file %s; "+
					"if the change is intended, run with %s=1", goldenPath, schematest.UpdateEnv)
			}
			// The golden must not contain any of these strings.
			for _, s := range []string{"find_references", "go_typecheck", "typecheck", "max-files", "symbol-bound"} {
				if strings.Contains(string(golden), s) {
					t.Errorf("golden contains %q", s)
				}
			}
		})
	}
}
