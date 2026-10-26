package box

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// TestSync_VendorsALocalReplaceOutsideTheWorkTreeAndRewritesGoMod is issue
// #319: go.mod's local replace directives for agentkit-go point at a sibling
// checkout that is not part of the git work tree, so the box's sync (a copy
// of the work tree alone) cannot resolve them, and every build inside the box
// fails. The sibling here stands for agentkit-go; the box must copy it in
// under .agent-fox/vendor and rewrite the replace to point there.
func TestSync_VendorsALocalReplaceOutsideTheWorkTreeAndRewritesGoMod(t *testing.T) {
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

	got := inBox(t, s, "cat go.mod")
	if strings.Contains(got, "=> "+filepath.ToSlash(rel)) {
		t.Errorf("go.mod in the box still has the original replace %q, want it rewritten to a path inside the box:\n%s", rel, got)
	}
	if !strings.Contains(got, "example.com/sibling => ./.agent-fox/vendor/") {
		t.Errorf("go.mod in the box has no rewritten replace of example.com/sibling:\n%s", got)
	}
	if got := inBox(t, s, "go build -o /dev/null ./... && echo ok"); got != "ok" {
		t.Errorf("go build in the box = %q, want ok: the vendored replace should let it resolve example.com/sibling", got)
	}
}

// TestSync_LeavesGoModAloneWhenEveryReplaceIsInsideTheWorkTree checks that a
// repository with no replace outside its own tree (the common case) is
// synced exactly as before: go.mod reaches the box byte for byte.
func TestSync_LeavesGoModAloneWhenEveryReplaceIsInsideTheWorkTree(t *testing.T) {
	testutil.Require(t, "git", "sh")
	goMod := "module example.com/main\n\ngo 1.21\n\nreplace example.com/inner => ./inner\n"
	dir := hostRepo(t, map[string]string{"go.mod": goMod, "inner/go.mod": "module example.com/inner\n\ngo 1.21\n"})

	s := openLocal(t, dir, &ledger{})

	if got := inBox(t, s, "cat go.mod"); got != strings.TrimRight(goMod, "\n") {
		t.Errorf("go.mod in the box = %q, want it unchanged: %q", got, goMod)
	}
	if got := inBox(t, s, "test -d .agent-fox/vendor && echo yes || echo no"); got != "no" {
		t.Errorf(".agent-fox/vendor = %q, want none made when nothing needs vendoring", got)
	}
}
