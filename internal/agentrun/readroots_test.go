package agentrun

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"

	"github.com/agent-fox-dev/agentfox/internal/project"
)

// Issue #198: a read-only phase's shell may read under a root the repository
// names as a local replace, and nowhere else outside the repository.
func TestGuardLetsAReadOnlyPhaseReadAReadRoot(t *testing.T) {
	parent := t.TempDir()
	repo, dep := filepath.Join(parent, "agent-fox"), filepath.Join(parent, "agentkit-go")
	for _, d := range []string{repo, dep, filepath.Join(parent, "secret")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	resolve := func(p string) (string, error) {
		if !filepath.IsAbs(p) {
			p = filepath.Join(repo, p)
		}
		return filepath.Clean(p), nil
	}
	g := Guard(GuardOptions{Programs: []string{"cat", "ls", "grep"}, ReadOnlyFiles: true, ResolvePath: resolve,
		ReadRoots: []string{dep}})
	ctx := context.Background()
	for _, cmd := range []string{"cat ../agentkit-go/tools/index.go", "ls ../agentkit-go", "grep -rn Index " + dep} {
		if d := g(ctx, execCall(cmd)); d.Block {
			t.Errorf("refused %s: %s", cmd, d.Reason)
		}
	}
	for _, cmd := range []string{"cat ../secret/key", "ls ..", "cat ../agentkit-go/../secret/key"} {
		if d := g(ctx, execCall(cmd)); !d.Block {
			t.Errorf("allowed %s", cmd)
		}
	}
}

// The model is told where the read roots are and how to read them, in the
// shell's and read_file's descriptions.
func TestReadRootsAreNamedInTheToolDescriptions(t *testing.T) {
	tools := []core.Tool{{Name: "execute", Description: "Run a command."}, {Name: "read_file", Description: "Read a file."},
		{Name: "write_file", Description: "Write a file."}}
	roots := []project.ReadRoot{{Module: "github.com/agentfox/agentkit-go", Path: "../agentkit-go"}}
	got := noteReadRoots(tools, roots, []string{"cat", "grep", "go"})
	for _, name := range []string{"execute", "read_file"} {
		for _, tl := range got {
			if tl.Name == name && (!strings.Contains(tl.Description, "../agentkit-go (replace of github.com/agentfox/agentkit-go)") ||
				!strings.Contains(tl.Description, "go doc")) {
				t.Errorf("%s does not name the read root: %s", name, tl.Description)
			}
		}
	}
	if got[2].Description != "Write a file." {
		t.Errorf("write_file's description changed: %s", got[2].Description)
	}
	if out := noteReadRoots(tools, nil, []string{"go"}); out[0].Description != "Run a command." {
		t.Errorf("no roots changed a description: %s", out[0].Description)
	}
	// A phase whose shell cannot run go is not sent to go doc.
	if out := noteReadRoots(tools, roots, ReadOnlyPrograms); strings.Contains(out[0].Description, "go doc") ||
		!strings.Contains(out[0].Description, "../agentkit-go") {
		t.Errorf("a read-only phase's note: %s", out[0].Description)
	}
}
