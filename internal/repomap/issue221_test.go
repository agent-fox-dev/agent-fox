package repomap

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/outline"
)

// Issue #221: the map outlines a non-Go file the way file_outline does, so
// the two show the same declarations for it. Without cgo AgentKit has no
// backend for Python and there is nothing to compare.
func TestTheMapOutlinesNonGoFilesLikeFileOutline(t *testing.T) {
	ws := newWS(t, map[string]string{"app/session.py": "def refresh():\n    pass\n"})
	abs := filepath.Join(ws.Root, "app", "session.py")
	want, err := outline.Outline(context.Background(), abs, nil, outline.Options{Root: ws.Root})
	if err != nil {
		t.Fatalf("Outline: %v", err)
	}
	if want.Backend != outline.BackendTreeSitter {
		t.Skipf("no tree-sitter backend in this build (backend %q)", want.Backend)
	}
	got, err := Build(context.Background(), ws, 6000, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, d := range want.Decls {
		if !strings.Contains(got, d.Name) {
			t.Errorf("the map does not show %s from session.py:\n%s", d.Name, got)
		}
	}
}

// Issue #221: test support files and Java integration tests are tests in the
// map too.
func TestTheMapKnowsTestSupportFiles(t *testing.T) {
	for _, p := range []string{"conftest.py", "tests/unit/conftest.py", "src/setupTests.ts", "jest.setup.js",
		"vitest.setup.ts", "internal/testutil/fake.go", "src/test/java/a/UserIT.java", "spec/models/user_spec.rb"} {
		if !isTestFile(p) {
			t.Errorf("isTestFile(%q) = false", p)
		}
	}
	if isTestFile("src/session.py") {
		t.Error("an implementation file counts as a test")
	}
}
