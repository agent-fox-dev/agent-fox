package agentrun

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/guard"
)

// rootedResolver resolves a relative path against root, as the workspace does.
func rootedResolver(root string) func(string) (string, error) {
	return func(p string) (string, error) {
		if filepath.IsAbs(p) {
			return filepath.Clean(p), nil
		}
		return filepath.Join(root, p), nil
	}
}

// Issue #220 (1): an assignment in front of a program, or a program named by
// a path, can change what runs. A read-only phase refuses every assignment —
// GIT_EXTERNAL_DIFF and GIT_PAGER make git run a program — and every program
// named by a path; a writing phase keeps the harmless assignments it relies
// on (GOFLAGS=…) and refuses the ones that change which binary runs or what
// is loaded into it.
func TestAssignmentsAndPathProgramsCannotChangeWhatRuns(t *testing.T) {
	ctx := context.Background()
	ro := Guard(GuardOptions{Programs: []string{"ls", "cat", "git"}, ReadOnlyFiles: true})
	for _, cmd := range []string{
		"PATH=./scripts ls",
		"LD_PRELOAD=./x.so git status",
		"DYLD_INSERT_LIBRARIES=./x.dylib ls",
		"GIT_EXTERNAL_DIFF=./x git diff",
		"GIT_PAGER=./x git log",
		"LANG=C ls",
		"./scripts/ls",
		"/tmp/ls",
	} {
		if d := ro(ctx, execCall(cmd)); !d.Block {
			t.Errorf("read-only phase allowed %q", cmd)
		}
	}
	for _, cmd := range []string{"ls", "git status", "cat go.mod"} {
		if d := ro(ctx, execCall(cmd)); d.Block {
			t.Errorf("read-only phase refused %q: %s", cmd, d.Reason)
		}
	}
	if d := ro(ctx, argvCall("./scripts/ls")); !d.Block {
		t.Error("read-only phase allowed run_command ./scripts/ls")
	}

	w := Guard(GuardOptions{Programs: []string{"go", "ls", "make"}, AllowOperators: true})
	for _, cmd := range []string{
		"PATH=./scripts ls",
		"LD_PRELOAD=./x.so go build ./...",
		"GOFLAGS=-mod=mod PATH=/tmp go build ./...",
		"ls && PATH=. ls",
		"./scripts/go build",
		"ls && ./scripts/go build",
	} {
		if d := w(ctx, execCall(cmd)); !d.Block {
			t.Errorf("writing phase allowed %q", cmd)
		}
	}
	for _, cmd := range []string{
		"GOFLAGS=-mod=mod go build ./...",
		"CGO_ENABLED=0 go test ./pkg",
		"ls && CGO_ENABLED=0 go vet ./pkg",
	} {
		if d := w(ctx, execCall(cmd)); d.Block {
			t.Errorf("writing phase refused %q: %s", cmd, d.Reason)
		}
	}
}

// Issue #220 (1): a gate command spelled as a path is listed as that path, so
// the phase it judges can still run it, and no other path.
func TestAGateProgramListedAsAPathRunsAsSpelled(t *testing.T) {
	ctx := context.Background()
	g := Guard(GuardOptions{Programs: []string{"gradlew", "./gradlew"}, AllowOperators: true})
	if d := g(ctx, execCall("./gradlew test --tests ParserTest")); d.Block {
		t.Errorf("refused the listed ./gradlew: %s", d.Reason)
	}
	if d := g(ctx, execCall("./tools/gradlew test")); !d.Block {
		t.Error("allowed an unlisted path with a listed basename")
	}
}

// Issue #220 (2): a leading `cd <subdir>` runs the rest there, so the checks
// judge it there: an operand is resolved from the subdirectory, a chain of cd
// is followed, and the whole suite is the suite only at the root.
func TestALeadingCdIsJudgedWhereTheCommandRuns(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for _, d := range []string{"internal/x", "pkg"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ro := Guard(GuardOptions{Programs: []string{"cat", "ls"}, ReadOnlyFiles: true, ResolvePath: rootedResolver(root)})
	for _, cmd := range []string{
		"cd internal && cat ../go.mod",
		"cd internal && cd x && cat ../../go.mod",
		"cd internal/x && ls ..",
	} {
		if d := ro(ctx, execCall(cmd)); d.Block {
			t.Errorf("refused %q: %s", cmd, d.Reason)
		}
	}
	for _, cmd := range []string{
		"cd internal && cat ../../etc/passwd",
		"cd internal && cd .. && cd .. && ls",
		"cd internal && ls ../..",
	} {
		if d := ro(ctx, execCall(cmd)); !d.Block {
			t.Errorf("allowed %q", cmd)
		}
	}

	w := Guard(GuardOptions{Programs: []string{"pytest", "rm"}, AllowOperators: true, Suite: []string{"pytest"},
		ResolvePath: rootedResolver(root)})
	if d := w(ctx, execCall("cd pkg && pytest")); d.Block {
		t.Errorf("refused a run of one directory's tests: %s", d.Reason)
	}
	for _, cmd := range []string{"pytest", "cd " + root + " && pytest", "cd pkg && cd .. && pytest"} {
		if d := w(ctx, execCall(cmd)); !d.Block {
			t.Errorf("allowed the whole suite: %q", cmd)
		}
	}
	// From pkg, `..` is the workspace itself.
	if d := w(ctx, execCall("cd pkg && rm -r ..")); !d.Block {
		t.Error("allowed rm of the workspace from a subdirectory")
	}
	if d := w(ctx, execCall("cd pkg && rm stale.txt")); d.Block {
		t.Errorf("refused rm of a file in the subdirectory: %s", d.Reason)
	}
}

// Issue #220 (3): `test` and `[` read paths, and git's -C, --git-dir and
// --work-tree point it at a repository: in a read-only phase they are held to
// the workspace like cat is.
func TestTestAndGitDirectoriesAreConfinedInAReadOnlyPhase(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ro := Guard(GuardOptions{Programs: []string{"test", "[", "git"}, ReadOnlyFiles: true, ResolvePath: rootedResolver(root)})
	for _, cmd := range []string{
		"test -r /etc/shadow",
		"[ -f /root/.ssh/id_rsa ]",
		"test -f ../x",
		"git -C /elsewhere log",
		"git --git-dir=/elsewhere/.git log",
		"git --work-tree /elsewhere status",
	} {
		if d := ro(ctx, execCall(cmd)); !d.Block {
			t.Errorf("allowed %q", cmd)
		}
	}
	for _, cmd := range []string{"test -f go.mod", "[ -d internal ]", "git -C sub log", "git log"} {
		if d := ro(ctx, execCall(cmd)); d.Block {
			t.Errorf("refused %q: %s", cmd, d.Reason)
		}
	}
}

// Issue #220 (minor): a quote inside a word does not hide the program, and
// the refusal names the real reason.
func TestAQuotedProgramIsJudgedAsTheProgram(t *testing.T) {
	g := Guard(GuardOptions{Programs: []string{"git"}, AllowOperators: true})
	d := g(context.Background(), execCall(`g"i"t push`))
	if !d.Block || !strings.Contains(d.Reason, "git push is the tool's job") {
		t.Errorf("reason = %q, want the git rule", d.Reason)
	}
}

// Issue #220 (minor): -c glued to its value is refused like -c, whatever the
// installed git makes of it.
func TestGitGluedConfigIsRefused(t *testing.T) {
	g := Guard(GuardOptions{Programs: []string{"git"}, AllowOperators: true})
	if d := g(context.Background(), execCall("git -ccore.pager=./x log")); !d.Block {
		t.Error("allowed git -c<name>=<value>")
	}
}

// Issue #220 (minor): this guard reads the floor policy's refusals to restate
// them and to tell a misreading from a finding. The shapes it relies on are
// pinned here, so a rewording upstream fails this test instead of silently
// changing what the model is told.
func TestTheFloorPolicysRefusalShapes(t *testing.T) {
	ctx := context.Background()
	floor := guard.Restricted(guard.Options{AllowedPrograms: []string{"ls"}, AllowShellOperators: true})
	d := floor(ctx, core.BeforeToolCallContext{ToolName: "execute", Arguments: map[string]any{"command": "curl x"}})
	if !d.Block || !strings.HasPrefix(d.Reason, floorPrefix) || !strings.Contains(d.Reason, `program "curl"`) ||
		!strings.Contains(d.Reason, floorNotAllowed) {
		t.Errorf("allowlist refusal = %q", d.Reason)
	}
}
