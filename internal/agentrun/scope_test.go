package agentrun

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/tools"
)

// TS-17-19 (unit): the change lives in agentrun alone: no pipeline package
// carries its own copy of the note's text, and the read-only allowlist is
// unchanged.
//
// Verifies: 17-REQ-7.2
func TestTS17_19_TheChangeStaysInAgentrun(t *testing.T) {
	for _, pkg := range []string{"codefix", "codeimpl", "specgen", "issuetriage", "internal/conform"} {
		files, err := filepath.Glob(filepath.Join("..", "..", pkg, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no source in %s: %v", pkg, err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range []string{"Tool guidelines:", "Read files with", tools.SearchOverExecuteGuideline} {
				if strings.Contains(string(b), s) {
					t.Errorf("%s carries %q, which belongs to agentrun's tools note", f, s)
				}
			}
		}
	}
	for _, p := range []string{"cat", "head", "tail", "wc", "rg", "grep"} {
		if !slices.Contains(ReadOnlyPrograms, p) {
			t.Errorf("ReadOnlyPrograms lost %s", p)
		}
	}
}
