package repomap

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
)

// gitRepo is a real git repository with one Go file, a workspace over it and
// a gitx.Git for the tree-change check.
func gitRepo(t *testing.T) (*tools.Workspace, *gitx.Git) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	hermetic(t)
	dir := t.TempDir()
	ctx := context.Background()
	for _, argv := range [][]string{
		{"git", "init", "-q", "-b", "main"},
		{"git", "config", "user.email", "test@example.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "commit.gpgsign", "false"},
	} {
		if out, code, err := gitx.ExecRunner(ctx, dir, argv); err != nil || code != 0 {
			t.Fatalf("%v: %v (%d) %s", argv, err, code, out)
		}
	}
	writeFile(t, dir, "alpha.go", "package x\n\nfunc Alpha() {}\n")
	g := gitx.New(dir, gitx.ExecRunner)
	if _, err := g.CommitAll(ctx, "chore: initial commit\n"); err != nil {
		t.Fatal(err)
	}
	ws, err := tools.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ws, g
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TS-14-41 (smoke): the repomap package works end to end against a real git
// repository. Build walks, outlines, renders and stays within the budget, the
// output is deterministic, and a Refresher over the real gitx.Git reuses the
// map while the tree is unchanged and rebuilds it after a write and after a
// commit, which is the path fix and impl take between phases.
//
// Verifies: 14-PATH-2, 14-REQ-9.2, 14-REQ-9.3
//
// Real components: Build, Block, TreeChangeDetector, Refresher, tools.Walk,
// outline.Outline, gitx.Git.Head and DirtyFiles.
func TestTS14_41_RefresherRebuildsOnlyWhenTheRealTreeChanges(t *testing.T) {
	ws, g := gitRepo(t)
	ctx := context.Background()

	const budget = 6000
	first, err := Build(ctx, ws, budget, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(first, "alpha.go") || !strings.Contains(first, "func Alpha L3") {
		t.Fatalf("the map lacks the repository's declaration:\n%s", first)
	}
	if again, _ := Build(ctx, ws, budget, nil); again != first {
		t.Error("two builds of the same tree differ")
	}
	if n := afspec.EstimateTokens(first); n > budget {
		t.Errorf("the map is %d tokens, over the budget %d", n, budget)
	}
	if block := Block(first); !strings.HasPrefix(block, "## Repository map") || !strings.Contains(block, first) {
		t.Errorf("Block does not wrap the map under its heading:\n%s", block)
	}

	builds := 0
	counting := func(ctx context.Context, w *tools.Workspace, b int, in []string) (string, error) {
		builds++
		return Build(ctx, w, b, in)
	}
	r := NewRefresher(ws, budget, counting, g)

	m1, err := r.Get(ctx, nil)
	if err != nil || m1 != first {
		t.Fatalf("first Get = (%q, %v), want the built map", m1, err)
	}
	if m, _ := r.Get(ctx, nil); m != m1 || builds != 1 {
		t.Errorf("an unchanged tree rebuilt the map (builds = %d)", builds)
	}

	// A write is a dirty file: the map is rebuilt and shows it.
	writeFile(t, ws.Root, "beta.go", "package x\n\nfunc Beta() {}\n")
	m2, err := r.Get(ctx, nil)
	if err != nil || builds != 2 || !strings.Contains(m2, "func Beta L3") {
		t.Fatalf("after a write: builds = %d, err = %v, map:\n%s", builds, err, m2)
	}
	if m, _ := r.Get(ctx, nil); m != m2 || builds != 2 {
		t.Errorf("an unchanged dirty tree rebuilt the map (builds = %d)", builds)
	}

	// A commit moves HEAD and clears the dirty set: another rebuild.
	if _, err := g.CommitAll(ctx, "feat: add beta\n"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, ws.Root, "gamma.go", "package x\n\nfunc Gamma() {}\n")
	if _, err := g.CommitAll(ctx, "feat: add gamma\n"); err != nil {
		t.Fatal(err)
	}
	m3, err := r.Get(ctx, nil)
	if err != nil || builds != 3 || !strings.Contains(m3, "func Gamma L3") {
		t.Fatalf("after a commit: builds = %d, err = %v, map:\n%s", builds, err, m3)
	}
}
