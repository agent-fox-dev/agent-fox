package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoWithDocs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// A change to documented behaviour that ships no docs is reported (#74).
func TestMissingDocs(t *testing.T) {
	root := repoWithDocs(t)
	for name, c := range map[string]struct {
		changed []string
		warn    bool
	}{
		"guard changed, no docs":   {[]string{"internal/agentrun/guard.go"}, true},
		"cli changed, no docs":     {[]string{"cmd/fix/main.go", "codefix/pipeline.go"}, true},
		"guard with docs":          {[]string{"internal/agentrun/guard.go", "docs/cli.md"}, false},
		"guard with a readme":      {[]string{"internal/agentrun/guard.go", "README.md"}, false},
		"only tests":               {[]string{"internal/agentrun/guard_test.go", "cmd/fix/testdata/x.json"}, false},
		"internal change, no docs": {[]string{"internal/gitx/git.go"}, false},
		"only docs":                {[]string{"docs/cli.md"}, false},
		"config file":              {[]string{"app/config.py"}, true},
		"non-source file":          {[]string{"cmd/fix/schema.golden.json"}, false},
	} {
		got := MissingDocs(root, c.changed)
		if (got != "") != c.warn {
			t.Errorf("%s: MissingDocs = %q, want warning=%v", name, got, c.warn)
		}
	}
	if got := MissingDocs(root, []string{"internal/agentrun/guard.go"}); !strings.Contains(got, "guard.go") {
		t.Errorf("the message does not name the file: %q", got)
	}

	// A repository with no documentation is not behind on it.
	if got := MissingDocs(t.TempDir(), []string{"cmd/fix/main.go"}); got != "" {
		t.Errorf("a repo with no docs was warned: %q", got)
	}
}
