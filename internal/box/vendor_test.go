package box

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// TestSync_VendorsALocalReplaceOutsideTheWorkTreeWithAGoWork is issue #319:
// go.mod's local replace directives for agentkit-go point at a sibling
// checkout that is not part of the git work tree, so the box's sync (a copy
// of the work tree alone) cannot resolve them, and every build inside the box
// fails. The sibling here stands for agentkit-go; the box must copy it in
// under .agent-fox/vendor and redirect the replace to it with a go.work, not
// by changing go.mod: go.mod is a tracked file, and a change to it would look
// like part of whatever a brain commits in the box (issue #285's bug: the
// first version of this fix rewrote go.mod itself, and that rewrite leaked
// into the host's checkout once exported).
func TestSync_VendorsALocalReplaceOutsideTheWorkTreeWithAGoWork(t *testing.T) {
	testutil.Require(t, "git", "sh", "go")
	sibling := plainDir(t, map[string]string{
		"go.mod": "module example.com/sibling\n\ngo 1.21\n",
		"lib.go": "package sibling\n\nfunc Hi() string { return \"hi from the sibling\" }\n",
	})
	dir := hostRepo(t, nil)
	rel, err := filepath.Rel(dir, sibling)
	if err != nil {
		t.Fatal(err)
	}
	goMod := "module example.com/main\n\ngo 1.21\n\nrequire example.com/sibling v0.0.0\n\nreplace example.com/sibling => " +
		filepath.ToSlash(rel) + "\n"
	testutil.WriteFile(t, dir, "go.mod", goMod)
	testutil.WriteFile(t, dir, "main.go", "package main\n\nimport (\n\t\"fmt\"\n\n\t\"example.com/sibling\"\n)\n\nfunc main() { fmt.Println(sibling.Hi()) }\n")
	testutil.Commit(t, dir, "add a module with a local replace outside the work tree")

	s := openLocal(t, dir, &ledger{})

	if got := inBox(t, s, "cat go.mod"); got != strings.TrimRight(goMod, "\n") {
		t.Errorf("go.mod in the box = %q, want it unchanged, byte for byte: %q", got, goMod)
	}
	work := inBox(t, s, "cat go.work")
	if !strings.Contains(work, "example.com/sibling => ./.agent-fox/vendor/") {
		t.Errorf("go.work in the box has no replace of example.com/sibling reaching the box's copy:\n%s", work)
	}
	if got := inBox(t, s, "go build -o /dev/null ./... && echo ok"); got != "ok" {
		t.Errorf("go build in the box = %q, want ok: go.work's replace should let it resolve example.com/sibling", got)
	}
	// A brain that stages everything it finds, git add -A, must not pick up the
	// box's own go.work or vendored copy as if they were part of its change.
	if got := inBox(t, s, "git add -A && git status --porcelain | wc -l | tr -d ' '"); got != "0" {
		t.Errorf("git status after add -A staged %s entries, want 0: nothing of the box's own go.work or vendor", got)
	}
}

// TestSync_LeavesGoModAndMakesNoGoWorkWhenEveryReplaceIsInsideTheWorkTree
// checks that a repository with no replace outside its own tree (the common
// case) is synced exactly as before: go.mod reaches the box byte for byte,
// and the box gets no go.work.
func TestSync_LeavesGoModAndMakesNoGoWorkWhenEveryReplaceIsInsideTheWorkTree(t *testing.T) {
	testutil.Require(t, "git", "sh")
	goMod := "module example.com/main\n\ngo 1.21\n\nreplace example.com/inner => ./inner\n"
	dir := hostRepo(t, map[string]string{"go.mod": goMod, "inner/go.mod": "module example.com/inner\n\ngo 1.21\n"})

	s := openLocal(t, dir, &ledger{})

	if got := inBox(t, s, "cat go.mod"); got != strings.TrimRight(goMod, "\n") {
		t.Errorf("go.mod in the box = %q, want it unchanged: %q", got, goMod)
	}
	if got := inBox(t, s, "test -e go.work && echo yes || echo no"); got != "no" {
		t.Errorf("go.work = %q, want none made when nothing needs vendoring", got)
	}
	if got := inBox(t, s, "test -d .agent-fox/vendor && echo yes || echo no"); got != "no" {
		t.Errorf(".agent-fox/vendor = %q, want none made when nothing needs vendoring", got)
	}
}
