package repomap

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/agentfox/agentkit-go/outline"
)

// Issue #221: the map outlines a non-Go file the way file_outline does, with
// a ctags runner, so the two show the same declarations for it.
func TestTheMapOutlinesWithACtagsRunner(t *testing.T) {
	ws := newWS(t, map[string]string{"app/session.py": "def refresh():\n    pass\n"})
	oldRunner := ctagsRunner
	ctagsRunner = func(context.Context, []string) ([]byte, error) { return nil, nil }
	t.Cleanup(func() { ctagsRunner = oldRunner })
	var withRunner bool
	old := outlineFn
	outlineFn = func(ctx context.Context, abs string, src []byte, o outline.Options) (outline.File, error) {
		if filepath.Base(abs) == "session.py" {
			withRunner = o.Runner != nil
		}
		return old(ctx, abs, src, o)
	}
	t.Cleanup(func() { outlineFn = old })
	if _, err := Build(context.Background(), ws, 6000, nil); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !withRunner {
		t.Error("the map outlined session.py with no ctags runner")
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
