package project

import (
	"os"
	"path/filepath"
	"testing"
)

// Issue #198: a go.mod `replace` that points at a local directory outside the
// repository is a root the model may read. A replacement by another module,
// a directory that does not exist, one inside the repository and one nested
// in another root are not.
func TestReadRootsFromGoModReplaces(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "agent-fox")
	for _, d := range []string{repo, filepath.Join(parent, "agentkit-go", "codesearch"),
		filepath.Join(parent, "other"), filepath.Join(repo, "vendored")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gomod := `module github.com/x/agent-fox

go 1.24

require github.com/agentfox/agentkit-go v0.0.0

replace github.com/agentfox/agentkit-go => ../agentkit-go

replace github.com/agentfox/agentkit-go/codesearch => ../agentkit-go/codesearch

replace (
	example.com/other v1.0.0 => ../other // a comment
	example.com/fork => github.com/me/fork v1.2.3
	example.com/gone => ../gone
	example.com/inside => ./vendored
)
`
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ReadRoots(repo)
	want := []ReadRoot{
		{Module: "github.com/agentfox/agentkit-go", Path: "../agentkit-go"},
		{Module: "example.com/other", Path: "../other"},
	}
	if len(got) != len(want) {
		t.Fatalf("ReadRoots = %+v, want %+v", got, want)
	}
	for i, w := range want {
		abs, _ := filepath.EvalSymlinks(filepath.Join(repo, w.Path))
		if got[i].Module != w.Module || got[i].Path != w.Path || got[i].Abs != abs {
			t.Errorf("ReadRoots[%d] = %+v, want %+v at %s", i, got[i], w, abs)
		}
	}
	if len(ReadRoots(t.TempDir())) != 0 {
		t.Error("a directory with no go.mod has read roots")
	}
}
