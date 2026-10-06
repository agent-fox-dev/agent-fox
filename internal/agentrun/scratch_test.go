package agentrun

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"
)

// gitWorkspace is a workspace that is a git repository with one commit.
func gitWorkspace(t *testing.T) *tools.Workspace {
	t.Helper()
	ws := newWorkspace(t)
	for _, argv := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.email=t@x", "-c", "user.name=T",
		"-c", "commit.gpgsign=false", "add", "-A"}, {"-c", "user.email=t@x", "-c", "user.name=T", "-c",
		"commit.gpgsign=false", "commit", "-qm", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", ws.Root}, argv...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", argv, err, out)
		}
	}
	return ws
}

// probeTool ends the phase after looking at the tree from inside it: the
// scratch directories that exist, and whether git sees a file written into
// one.
func probeTool(root string, dirs *[]string, status *string) core.Tool {
	return core.Tool{
		Name: "submit", Description: "submit",
		Execute: func(context.Context, json.RawMessage) core.ToolResult {
			matches, _ := filepath.Glob(filepath.Join(root, ".agent-fox", "scratch", "*"))
			*dirs = nil
			for _, d := range matches {
				if info, err := os.Stat(d); err == nil && info.IsDir() {
					*dirs = append(*dirs, d)
					_ = os.WriteFile(filepath.Join(d, "mutate.py"), []byte("print(1)\n"), 0o644)
				}
			}
			out, _ := exec.Command("git", "-C", root, "status", "--porcelain", "--untracked-files=all").Output()
			*status = string(out)
			res := core.OKResult(map[string]any{"ok": true})
			res.Terminate = true
			return res
		},
	}
}

// Issue #209: a writing phase has a scratch directory inside the workspace
// that git does not see, and it is gone when the phase ends.
func TestAWritingPhaseHasAScratchDirectoryGitDoesNotSee(t *testing.T) {
	ws := gitWorkspace(t)
	p := faux.New(toolCallTurn("c1", "submit", map[string]any{}))
	r, err := NewRunner(fauxConfig(p, ws))
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	var status string
	if _, err := r.Run(context.Background(), Phase{Name: "implement", User: "go", Terminator: "submit",
		Custom: []core.Tool{probeTool(ws.Root, &dirs, &status)}, BuiltinTools: []string{"read_file", "write_file"},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("scratch directories during the phase: %q, want one", dirs)
	}
	if strings.TrimSpace(status) != "" {
		t.Errorf("git sees the scratch directory:\n%s", status)
	}
	if _, err := os.Stat(filepath.Join(ws.Root, ".agent-fox")); !os.IsNotExist(err) {
		t.Errorf(".agent-fox survived the phase: %v", err)
	}
}

// A read-only phase gets none, and a phase that fails still removes its own.
func TestScratchDirectoryLifetime(t *testing.T) {
	ws := gitWorkspace(t)
	p := faux.New(toolCallTurn("c1", "submit", map[string]any{}))
	r, _ := NewRunner(fauxConfig(p, ws))
	var dirs []string
	var status string
	if _, err := r.Run(context.Background(), Phase{Name: "survey", User: "go", Terminator: "submit", ReadOnly: true,
		Custom: []core.Tool{probeTool(ws.Root, &dirs, &status)}, BuiltinTools: []string{"read_file"},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(dirs) != 0 {
		t.Errorf("a read-only phase has a scratch directory: %q", dirs)
	}

	// A run cancelled mid-phase — Ctrl-C — still removes it.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	interrupt := core.Tool{Name: "work", Description: "work",
		Execute: func(context.Context, json.RawMessage) core.ToolResult {
			cancel()
			return core.OKResult(map[string]any{"ok": true})
		}}
	p2 := faux.New(toolCallTurn("c1", "work", map[string]any{}), toolCallTurn("c2", "submit", map[string]any{}))
	r2, _ := NewRunner(fauxConfig(p2, ws))
	if _, err := r2.Run(ctx, Phase{Name: "implement", User: "go", Terminator: "submit",
		Custom: []core.Tool{interrupt, probeTool(ws.Root, &dirs, &status)}, BuiltinTools: []string{"write_file"},
	}); err == nil {
		t.Fatal("a cancelled phase succeeded")
	}
	if _, err := os.Stat(filepath.Join(ws.Root, ".agent-fox")); !os.IsNotExist(err) {
		t.Errorf("a failed phase left .agent-fox behind: %v", err)
	}
}

// The writing tools name the scratch directory and what it is for.
func TestTheScratchDirectoryIsNamedInTheToolDescriptions(t *testing.T) {
	ts := []core.Tool{{Name: "execute", Description: "Run."}, {Name: "write_file", Description: "Write."},
		{Name: "edit_file", Description: "Edit."}, {Name: "read_file", Description: "Read."}}
	got := noteScratch(ts, ".agent-fox/scratch/ab12cd34")
	for _, tl := range got[:3] {
		if !strings.Contains(tl.Description, ".agent-fox/scratch/ab12cd34/") ||
			!strings.Contains(tl.Description, "deleted when this phase ends") {
			t.Errorf("%s: %s", tl.Name, tl.Description)
		}
	}
	if got[3].Description != "Read." {
		t.Errorf("read_file changed: %s", got[3].Description)
	}
	if out := noteScratch(ts, ""); out[0].Description != "Run." {
		t.Errorf("no scratch changed a description: %s", out[0].Description)
	}
}
