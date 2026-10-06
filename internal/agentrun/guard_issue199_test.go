package agentrun

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
)

// Issue #199 (1): a program the phase's list names twice is offered once, in
// the refusal and in the shell's description.
func TestTheAllowlistNamesEachProgramOnce(t *testing.T) {
	g := Guard(GuardOptions{Programs: []string{"make", "go", "make", "make"}, AllowOperators: true})
	d := g(context.Background(), execCall("curl x"))
	if !d.Block || strings.Count(d.Reason, "make") != 1 {
		t.Errorf("reason = %q, want make listed once", d.Reason)
	}
	tools := SelectTools([]core.Tool{{Name: "execute", Description: "Run."}}, false, []string{"make", "go", "make"}, "execute")
	if strings.Count(tools[0].Description, "make") != 1 {
		t.Errorf("execute description lists make %d times: %s", strings.Count(tools[0].Description, "make"),
			tools[0].Description)
	}
}

// Issue #199 (3): an assignment from a command substitution runs when every
// program in it is allowed; the floor policy's misreading of `d=$(go list`
// does not refuse it, or name an argument as a program.
func TestAnAssignmentFromASubstitutionIsJudgedByItsPrograms(t *testing.T) {
	g := Guard(GuardOptions{Programs: []string{"go", "echo", "grep"}, AllowOperators: true})
	ctx := context.Background()
	cmd := `d=$(go list -m -f '{{.Dir}}' github.com/agentfox/agentkit-go/codesearch); echo $d; grep -rn "Invalidate" $d/*.go`
	if d := g(ctx, execCall(cmd)); d.Block {
		t.Errorf("refused: %s", d.Reason)
	}
	if d := g(ctx, execCall("d=$(curl -s x); echo $d")); !d.Block || !strings.Contains(d.Reason, "curl") ||
		strings.Contains(d.Reason, `"-s"`) {
		t.Errorf("a disallowed program in the substitution: block=%v reason=%q", d.Block, d.Reason)
	}
	ro := Guard(GuardOptions{Programs: []string{"go", "echo"}, ReadOnlyFiles: true})
	if d := ro(ctx, execCall("d=$(go list -m all)")); !d.Block || strings.Contains(d.Reason, `"list"`) ||
		strings.Contains(d.Reason, `"all)"`) {
		t.Errorf("a read-only phase's refusal names an argument as a program: %q", d.Reason)
	}
}

// Issue #199 (5): a backtick in a double-quoted pattern is a command
// substitution, and the refusal says that rather than naming a file as a
// program.
func TestABacktickInDoubleQuotesIsExplained(t *testing.T) {
	g := Guard(GuardOptions{Programs: []string{"grep"}, AllowOperators: true})
	d := g(context.Background(), execCall("grep -nE \"^#{1,3} |^\\| `\" docs/model-usage.md"))
	if !d.Block || !strings.Contains(d.Reason, "backtick") || strings.Contains(d.Reason, "model-usage.md") {
		t.Errorf("block=%v reason=%q", d.Block, d.Reason)
	}
	if d := g(context.Background(), execCall("grep -n '`' docs/model-usage.md")); d.Block {
		t.Errorf("a backtick in single quotes was refused: %s", d.Reason)
	}
}

// Issue #199 (2): a writing phase may remove files inside the repository —
// python could anyway — but not the repository itself, not under a protected
// directory, and nothing outside.
func TestRmIsConfinedToTheWorkspace(t *testing.T) {
	root := t.TempDir()
	spec := filepath.Join(root, ".specs", "09_x")
	if err := os.MkdirAll(spec, 0o755); err != nil {
		t.Fatal(err)
	}
	resolve := func(p string) (string, error) {
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		return filepath.Clean(p), nil
	}
	g := Guard(GuardOptions{Programs: append(append([]string(nil), ReadOnlyPrograms...), BuildPrograms...),
		AllowOperators: true, ResolvePath: resolve, ProtectedPaths: []string{spec}})
	ctx := context.Background()
	for _, cmd := range []string{"rm go.mod.orig.tmp go.sum.orig.tmp", "rm -f build/out.bin", "rm -rf tmp/mut",
		"rm -- -weird-name"} {
		if d := g(ctx, execCall(cmd)); d.Block {
			t.Errorf("refused %s: %s", cmd, d.Reason)
		}
	}
	for _, cmd := range []string{"rm /etc/hosts", "rm ../other/file", "rm -rf .", "rm -rf " + root,
		"rm .specs/09_x/tasks.json", "rm -rf .specs", "rm $HOME/x", "rm ~/x", "rm -rf *"} {
		if d := g(ctx, execCall(cmd)); !d.Block {
			t.Errorf("allowed %s", cmd)
		}
	}
	ro := Guard(GuardOptions{Programs: ReadOnlyPrograms, ReadOnlyFiles: true, ResolvePath: resolve})
	if d := ro(ctx, execCall("rm x.tmp")); !d.Block {
		t.Error("a read-only phase may run rm")
	}
}
